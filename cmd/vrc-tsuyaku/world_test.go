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
)

func TestFlowSources(t *testing.T) {
	if !reflect.DeepEqual(flowSource("self", "mic-id"), audio.Microphone("mic-id")) {
		t.Fatal("self source does not select the configured microphone")
	}
	if !reflect.DeepEqual(flowSource("world", "ignored"), audio.Application("VRChat.exe", "VRChat")) {
		t.Fatal("world source does not select VRChat")
	}
}

// Playback is external to the test and must target a named null sink.
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
		r.Header.Set("Authorization", "Bearer "+a.token)
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
	a.source = func(name, _ string) audio.Source {
		if name != "world" {
			t.Errorf("world capture check tried to open %s", name)
			return audio.Application("vrc-tsuyaku-test-forbidden-source")
		}
		return audio.Application(player)
	}
}
