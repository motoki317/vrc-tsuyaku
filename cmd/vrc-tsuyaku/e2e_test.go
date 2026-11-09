package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	"unicode"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
)

type testSource struct {
	gate    <-chan struct{}
	samples []float32
	ended   chan time.Time
}

func (s testSource) Capture(ctx context.Context, consume func([]float32) error) error {
	select {
	case <-s.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	started := time.Now()
	position := 0
	ended := false
	for {
		timer := time.NewTimer(time.Until(started.Add(time.Duration(position+320) * time.Second / audio.SampleRate)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		chunk := make([]float32, 320)
		if position < len(s.samples) {
			copy(chunk, s.samples[position:min(position+320, len(s.samples))])
		}
		if err := consume(chunk); err != nil {
			return err
		}
		position += 320
		if !ended && len(s.samples) > 0 && position >= len(s.samples) {
			s.ended <- started.Add(time.Duration(len(s.samples)) * time.Second / audio.SampleRate)
			ended = true
		}
	}
}

func TestEndToEnd(t *testing.T) {
	dir, wav := os.Getenv("VRC_TSUYAKU_TEST_APP_DATA_DIR"), os.Getenv("VRC_TSUYAKU_TEST_APP_WAV")
	if dir == "" || wav == "" {
		t.Skip("set VRC_TSUYAKU_TEST_APP_DATA_DIR and VRC_TSUYAKU_TEST_APP_WAV")
	}
	samples := testWAV(t, wav)
	t.Logf("app PID %d, clip %.3f s, data directory %s", os.Getpid(), float64(len(samples))/16000, dir)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	a, err := newApp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	server := httptest.NewServer(a.handler())
	defer server.Close()
	call := func(method, path string, body any, want int) {
		t.Helper()
		var data []byte
		if body != nil {
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+"/api/"+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		b, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s: HTTP %d: %s", path, response.StatusCode, b)
		}
	}
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	settings := config.Defaults()
	settings.Chatbox.Address = listener.LocalAddr().String()
	call("PUT", "settings", settings, 200)
	for id := range a.artifacts {
		a.mu.Lock()
		state := a.artifacts[id]
		a.mu.Unlock()
		if state.State == "installed" {
			t.Logf("already installed: %s", id)
			continue
		}
		t.Logf("download through API: %s", id)
		call("POST", "downloads/"+id, map[string]bool{"acceptLicense": true}, 202)
		deadline := time.Now().Add(20 * time.Minute)
		for {
			a.mu.Lock()
			state = a.artifacts[id]
			a.mu.Unlock()
			if state.State == "installed" {
				break
			}
			if state.State == "missing" {
				t.Fatalf("download %s: %s", id, state.Error)
			}
			if time.Now().After(deadline) {
				t.Fatalf("download timeout: %s", id)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	gate := make(chan struct{})
	ended := make(chan time.Time, 1)
	a.source = func(name, _ string) audio.Source {
		if name == "self" {
			return testSource{gate, samples, ended}
		}
		return testSource{gate: gate}
	}
	call("POST", "flows/self/start", nil, 200)
	call("POST", "flows/world/start", nil, 200)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		a.mu.Lock()
		self, world := a.flows["self"].status, a.flows["world"].status
		a.mu.Unlock()
		if self.State == "error" || world.State == "error" {
			t.Fatal(self, world)
		}
		if self.State == "running" && world.State == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("flow startup timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(gate)
	if err := listener.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 4096)
	n, _, err := listener.ReadFromUDP(packet)
	received := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	text := decodeTestOSC(t, packet[:n])
	var clipEnd time.Time
	select {
	case clipEnd = <-ended:
	case <-time.After(6 * time.Second):
		t.Fatal("clip did not finish")
	}
	history, _ := a.events.after(0)
	original, translation := "", ""
	for _, e := range history {
		if e.Flow == "self" && e.Kind == "translated" {
			original = e.Original
			translation = e.Text
		}
	}
	if original == "" || translation == "" || text != "[ja] "+original+"\n[en] "+translation {
		t.Fatalf("page %q, original %q, translation %q", text, original, translation)
	}
	japanese, english := false, false
	for _, r := range original {
		japanese = japanese || unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
	}
	for _, r := range translation {
		english = english || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
	}
	if !japanese || !english {
		t.Fatalf("unexpected output scripts: %q", text)
	}
	// Let the page callback publish before collecting the stage log.
	time.Sleep(100 * time.Millisecond)
	history, _ = a.events.after(0)
	for _, kind := range []string{"recognized", "translated", "page"} {
		found := false
		for _, e := range history {
			if e.Flow == "self" && e.Kind == kind {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %s event", kind)
		}
	}
	appPeak, childPeak, err := peakMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("OSC page: %q", text)
	t.Logf("end-of-clip to page: %.3f s; peak RSS app %.1f MiB, llama-server %.1f MiB, sum %.1f MiB", received.Sub(clipEnd).Seconds(), float64(appPeak)/(1<<20), float64(childPeak)/(1<<20), float64(appPeak+childPeak)/(1<<20))
	if latency := received.Sub(clipEnd); latency > 2*time.Second {
		t.Errorf("latency %s exceeds 2 s", latency)
	}
	if appPeak+childPeak > 6500000000 {
		t.Errorf("combined peak RSS exceeds 6.5 GB")
	}
}

func testWAV(t *testing.T, path string) []float32 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 44 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || string(b[36:40]) != "data" || binary.LittleEndian.Uint16(b[20:22]) != 1 || binary.LittleEndian.Uint16(b[22:24]) != 1 || binary.LittleEndian.Uint32(b[24:28]) != 16000 || binary.LittleEndian.Uint16(b[34:36]) != 16 || int(binary.LittleEndian.Uint32(b[40:44])) != len(b)-44 {
		t.Fatal("expected canonical 16 kHz mono PCM16 WAV")
	}
	x := make([]float32, (len(b)-44)/2)
	for i := range x {
		x[i] = float32(int16(binary.LittleEndian.Uint16(b[44+2*i:]))) / 32768
	}
	return x
}

func decodeTestOSC(t *testing.T, b []byte) string {
	t.Helper()
	read := func() string {
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			t.Fatal("unterminated OSC string")
		}
		s := string(b[:i])
		n := (i + 4) &^ 3
		if n > len(b) {
			t.Fatal("truncated OSC padding")
		}
		b = b[n:]
		return s
	}
	if address, tags := read(), read(); address != "/chatbox/input" || tags != ",sTF" {
		t.Fatalf("OSC %s %s", address, tags)
	}
	text := read()
	if len(b) != 0 {
		t.Fatalf("invalid page %q", text)
	}
	return text
}
