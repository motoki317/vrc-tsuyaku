package chatbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type recordedWrite struct {
	packet []byte
	at     time.Time
}

func send(q *Queue, ctx context.Context, original, translation string) error {
	return q.Send(ctx, "{{.Original}}\n{{.Translation}}", Message{Original: original, Translation: translation}, nil)
}

type recordingSocket struct {
	delay time.Duration
	err   error
	short bool

	// mu guards the record, which a Send on another goroutine can append to while the test reads it.
	mu     sync.Mutex
	writes []recordedWrite
	closed int
}

func (s *recordingSocket) Write(p []byte) (int, error) {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.writes = append(s.writes, recordedWrite{bytes.Clone(p), time.Now()})
	s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	if s.short {
		return len(p) - 1, nil
	}
	return len(p), nil
}

func (s *recordingSocket) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	return nil
}

func (s *recordingSocket) recorded() ([]recordedWrite, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.writes), s.closed
}

func TestQueueTiming(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := &recordingSocket{}
		q := newQueue(socket)
		defer q.Close()
		start := time.Now()
		done := make(chan error, 1)
		go func() { done <- send(q, t.Context(), strings.Repeat("猫", 145), "English") }()
		synctest.Wait()
		if writes, _ := socket.recorded(); len(writes) != 1 || !writes[0].at.Equal(start) {
			t.Fatalf("first page did not send immediately: %v", writes)
		}
		time.Sleep(1500*time.Millisecond - time.Nanosecond)
		synctest.Wait()
		if writes, _ := socket.recorded(); len(writes) != 1 {
			t.Fatal("second page sent before 1.5 seconds")
		}
		time.Sleep(time.Nanosecond)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := send(q, t.Context(), "next", "次"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Second)
		if err := send(q, t.Context(), "after idle", "待機後"); err != nil {
			t.Fatal(err)
		}
		wantTimes := []time.Duration{0, 1500 * time.Millisecond, 3 * time.Second, 8 * time.Second}
		wantTexts := []string{strings.Repeat("猫", 144), "猫\nEnglish", "next\n次", "after idle\n待機後"}
		writes, _ := socket.recorded()
		if len(writes) != len(wantTimes) {
			t.Fatalf("writes = %d, want %d", len(writes), len(wantTimes))
		}
		for i, w := range writes {
			if got := w.at.Sub(start); got != wantTimes[i] {
				t.Fatalf("page %d at %s, want %s", i, got, wantTimes[i])
			}
			if got := decodePacket(t, w.packet); got != wantTexts[i] {
				t.Fatalf("page %d = %q, want %q", i, got, wantTexts[i])
			}
		}
	})
}

func TestQueueSlowWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := &recordingSocket{delay: 2 * time.Second}
		q := newQueue(socket)
		defer q.Close()
		if err := send(q, t.Context(), strings.Repeat("x", 300), ""); err != nil {
			t.Fatal(err)
		}
		writes, _ := socket.recorded()
		for i := 1; i < len(writes); i++ {
			if gap := writes[i].at.Sub(writes[i-1].at); gap != 3500*time.Millisecond {
				t.Fatalf("write gap = %s, want 3.5s", gap)
			}
		}
	})
}

func TestQueueConcurrentMessages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := &recordingSocket{}
		q := newQueue(socket)
		defer q.Close()
		done := make(chan error, 2)
		go func() { done <- send(q, t.Context(), strings.Repeat("a", 145), "A") }()
		synctest.Wait()
		go func() { done <- send(q, t.Context(), strings.Repeat("b", 145), "B") }()
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		var got []string
		writes, _ := socket.recorded()
		for _, w := range writes {
			got = append(got, decodePacket(t, w.packet))
		}
		want := []string{strings.Repeat("a", 144), "a\nA", strings.Repeat("b", 144), "b\nB"}
		if !slices.Equal(got, want) {
			t.Fatalf("interleaved messages: %q", got)
		}
	})
}

func TestQueueCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := &recordingSocket{}
		q := newQueue(socket)
		defer q.Close()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- send(q, ctx, strings.Repeat("a", 145), "A") }()
		synctest.Wait()
		waiting, cancelWaiting := context.WithCancel(t.Context())
		other := make(chan error, 1)
		go func() { other <- send(q, waiting, "queued", "待機") }()
		synctest.Wait()
		cancelWaiting()
		if err := <-other; !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancellation = %v", err)
		}
		time.Sleep(time.Second)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("page cancellation = %v", err)
		}
		if writes, _ := socket.recorded(); len(writes) != 1 {
			t.Fatalf("sent %d pages after cancellation", len(writes))
		}
		if err := send(q, t.Context(), "new", "新"); err != nil {
			t.Fatal(err)
		}
		writes, _ := socket.recorded()
		if gap := writes[1].at.Sub(writes[0].at); gap != 1500*time.Millisecond {
			t.Fatalf("cancellation reset the rate limit: %s", gap)
		}
	})
}

func TestQueueClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := &recordingSocket{}
		q := newQueue(socket)
		done := make(chan error, 2)
		go func() { done <- send(q, t.Context(), strings.Repeat("a", 300), "") }()
		synctest.Wait()
		go func() { done <- send(q, t.Context(), "queued", "") }()
		synctest.Wait()
		for range 2 {
			if err := q.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for range 2 {
			if err := <-done; !errors.Is(err, net.ErrClosed) {
				t.Fatalf("close during Send = %v", err)
			}
		}
		if err := send(q, t.Context(), "later", ""); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Send after Close = %v", err)
		}
		if writes, closed := socket.recorded(); len(writes) != 1 || closed != 1 {
			t.Fatalf("writes = %d, closes = %d", len(writes), closed)
		}
	})
}

func TestQueueWriteErrors(t *testing.T) {
	for _, short := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			wantErr := errors.New("write failed")
			socket := &recordingSocket{}
			q := newQueue(socket)
			defer q.Close()
			if err := send(q, t.Context(), "first", ""); err != nil {
				t.Fatal(err)
			}
			socket.err = wantErr
			if short {
				socket.err, socket.short, wantErr = nil, true, io.ErrShortWrite
			}
			if err := send(q, t.Context(), strings.Repeat("x", 300), ""); !errors.Is(err, wantErr) {
				t.Fatalf("write error = %v, want %v", err, wantErr)
			}
			if writes, _ := socket.recorded(); len(writes) != 2 {
				t.Fatalf("retried or continued after a write error: %d writes", len(writes))
			}
			socket.err, socket.short = nil, false
			if err := send(q, t.Context(), "later", ""); err != nil {
				t.Fatal(err)
			}
			writes, _ := socket.recorded()
			if gap := writes[2].at.Sub(writes[1].at); gap != 1500*time.Millisecond {
				t.Fatalf("failed write reset the rate limit: %s", gap)
			}
		})
	}
}

func TestQueueRejectsInvalidText(t *testing.T) {
	socket := &recordingSocket{}
	q := newQueue(socket)
	defer q.Close()
	for _, input := range []string{"bad\x00text", "bad\xfftext"} {
		for _, parts := range [][2]string{{input, "valid"}, {"valid", input}} {
			if err := send(q, t.Context(), parts[0], parts[1]); err == nil {
				t.Fatalf("accepted %q", parts)
			}
		}
	}
	if err := send(q, t.Context(), " \n", "\t"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := send(q, ctx, "cancelled", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("already cancelled Send = %v", err)
	}
	if writes, _ := socket.recorded(); len(writes) != 0 {
		t.Fatalf("unexpected writes: %d", len(writes))
	}
}

func TestQueueTemplateFailureSendsNothing(t *testing.T) {
	socket := &recordingSocket{}
	q := newQueue(socket)
	defer q.Close()
	for _, format := range []string{"{{", `partial {{index .Translation 100}}`, `partial {{printf "%c" 0}}`} {
		if err := q.Send(t.Context(), format, Message{Translation: "short"}, func(string) {
			t.Error("page callback after a template failure")
		}); !errors.Is(err, ErrTemplate) {
			t.Fatalf("template error = %v", err)
		}
	}
	if writes, _ := socket.recorded(); len(writes) != 0 {
		t.Fatalf("sent partial output: %d writes", len(writes))
	}
	if err := q.Send(t.Context(), DefaultTemplate, Message{Original: "こんにちは", Translation: "Hello", SourceLang: "ja", TargetLang: "en"}, nil); err != nil {
		t.Fatal(err)
	}
	if writes, _ := socket.recorded(); len(writes) != 1 || decodePacket(t, writes[0].packet) != "[ja] こんにちは\n[en] Hello" {
		t.Fatalf("queue did not recover: %v", writes)
	}
}

func TestQueueAddress(t *testing.T) {
	for _, address := range []string{"", "localhost:9000", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:65536"} {
		if q, err := New(address); err == nil {
			q.Close()
			t.Fatalf("accepted address %q", address)
		}
	}
}

func decodePacket(t *testing.T, packet []byte) string {
	t.Helper()
	if len(packet)%4 != 0 {
		t.Fatalf("unaligned packet length %d", len(packet))
	}
	readString := func() string {
		t.Helper()
		n := bytes.IndexByte(packet, 0)
		if n < 0 {
			t.Fatal("OSC string has no terminator")
		}
		text := string(packet[:n])
		size := (n + 4) &^ 3
		if size > len(packet) {
			t.Fatal("truncated OSC padding")
		}
		for _, b := range packet[n:size] {
			if b != 0 {
				t.Fatal("nonzero OSC padding")
			}
		}
		packet = packet[size:]
		return text
	}
	if got := readString(); got != "/chatbox/input" {
		t.Fatalf("OSC address = %q", got)
	}
	if got := readString(); got != ",sTF" {
		t.Fatalf("OSC type tags = %q", got)
	}
	text := readString()
	if len(packet) != 0 {
		t.Fatalf("unexpected OSC argument data: % x", packet)
	}
	return text
}

func TestQueueUDP(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	q, err := New(listener.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err := send(q, t.Context(), strings.Repeat("猫", 145), "Hello."); err != nil {
		t.Fatal(err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 2048)
	for _, want := range []string{strings.Repeat("猫", 144), "猫\nHello."} {
		n, _, err := listener.ReadFromUDP(packet)
		if err != nil {
			t.Fatal(err)
		}
		if got := decodePacket(t, packet[:n]); got != want {
			t.Fatalf("received text = %q, want %q", got, want)
		}
	}
}

func TestQueueDefaultTemplateUDP(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	q, err := New(listener.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	var reported []string
	if err := q.Send(t.Context(), DefaultTemplate, Message{Original: "こんにちは", Translation: "Hello", SourceLang: "ja", TargetLang: "en"}, func(page string) {
		reported = append(reported, page)
	}); err != nil {
		t.Fatal(err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 2048)
	n, _, err := listener.ReadFromUDP(packet)
	if err != nil {
		t.Fatal(err)
	}
	want := "[ja] こんにちは\n[en] Hello"
	if got := decodePacket(t, packet[:n]); got != want || !slices.Equal(reported, []string{want}) {
		t.Fatalf("received %q, callback %q, want %q", got, reported, want)
	}
}
