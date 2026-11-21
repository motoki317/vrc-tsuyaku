package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
)

func TestFlowSources(t *testing.T) {
	if !reflect.DeepEqual(flowSource("self", "mic-id"), audio.Microphone("mic-id")) {
		t.Fatal("self source does not select the configured microphone")
	}
	if !reflect.DeepEqual(flowSource("world", "ignored"), audio.Application("VRChat.exe", "VRChat")) {
		t.Fatal("world source does not select VRChat")
	}
}

// TestWorldCapture captures only the external test player on Linux.
//
//   - VRC_TSUYAKU_TEST_APP_DATA_DIR names a test directory with the world flow's downloads.
//   - VRC_TSUYAKU_TEST_WORLD_APPLICATION names the test player's executable or PulseAudio application.name.
//
// Build the embedded page first.
//
//	nix develop -c just web
//
// Start the player just before running go test. It must play silence while the models load
// (about 20 s), then English speech, then silence, and then exit.
// The player must use a uniquely named PulseAudio null sink with the stream's
// no-move flag and exit if the sink is missing. Stop the player before unloading the sink.
//
//	nix develop -c go test ./cmd/vrc-tsuyaku -run '^TestWorldCapture$' -v -count=1 -timeout=3m
//
// Only a person runs the separate native Windows checks, when audible test audio will disturb no one.
//
//  1. Start two test players with different executable names and different audio.
//  2. Run vrc-tsuyaku record -app <program>.exe -seconds 3 isolated.wav for one player.
//  3. Verify that the WAV contains only that player's audio.
//  4. Start VRChat and switch on Others' voices. Join a world with English
//     speakers or play an English video in a world's player.
//  5. Verify that the Translation screen shows English sentences and Japanese translations.
//  6. Exit VRChat and verify that the world flow shows an error. Restart VRChat and press Try again.
func TestWorldCapture(t *testing.T) {
	dir, player := os.Getenv("VRC_TSUYAKU_TEST_APP_DATA_DIR"), os.Getenv("VRC_TSUYAKU_TEST_WORLD_APPLICATION")
	if dir == "" || player == "" {
		t.Skip("set VRC_TSUYAKU_TEST_APP_DATA_DIR and VRC_TSUYAKU_TEST_WORLD_APPLICATION")
	}
	if strings.EqualFold(player, "VRChat.exe") {
		t.Fatal("use an isolated test player, not VRChat")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	a, err := newApp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	isolateWorldCapture(t, a, player)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	request := func(method, path string) *http.Response {
		t.Helper()
		r, err := http.NewRequestWithContext(ctx, method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			t.Fatalf("%s: %s", path, response.Status)
		}
		return response
	}
	response := request("POST", "/api/flows/world/start")
	response.Body.Close()
	response = request("GET", "/api/events")
	defer response.Body.Close()
	translated := false
	decoder := json.NewDecoder(response.Body)
	for {
		var e event
		if err := decoder.Decode(&e); err != nil {
			t.Fatal(err)
		}
		if e.Flow == "" && e.Kind == "settings" && e.Text == "saved" {
			continue
		}
		if e.Flow != "world" {
			t.Fatalf("unexpected flow event: %+v", e)
		}
		t.Logf("%s: %s", e.Kind, e.Text)
		if e.Kind == "error" {
			if !translated {
				t.Fatal(e.Text)
			}
			if e.Text != player+" playback stream ended or moved to another output" && e.Text != player+" exited" {
				t.Fatalf("unexpected capture failure: %s", e.Text)
			}
			return
		}
		if e.Kind == "translated" {
			english, japanese := false, false
			for _, r := range e.Original {
				english = english || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
			}
			for _, r := range e.Text {
				japanese = japanese || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Han, r)
			}
			if !english || !japanese {
				t.Fatalf("unexpected translation: %q -> %q", e.Original, e.Text)
			}
			t.Logf("UI event stream: %q -> %q", e.Original, e.Text)
			translated = true
		}
	}
}

func isolateWorldCapture(t *testing.T, a *app, player string) {
	t.Helper()
	a.source = func(name config.Flow, _ string) audio.Source {
		if name != "world" {
			t.Errorf("world capture check tried to open %s", name)
			return audio.Application("vrc-tsuyaku-test-forbidden-source")
		}
		return audio.Application(player)
	}
}
