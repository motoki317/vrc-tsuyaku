package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchUpdate(t *testing.T) {
	const releaseURL = "https://github.com/motoki317/vrc-tsuyaku/releases/tag/v1.2.3"
	for _, tc := range []struct {
		name, version, tag string
		status             int
		body               string
		want               updateState
	}{
		{name: "newer", version: "v1.2.2", tag: "v1.2.3", want: updateState{State: "available", Version: "v1.2.3", URL: releaseURL}},
		{name: "equal", version: "v1.2.3", tag: "v1.2.3", want: updateState{State: "current"}},
		{name: "older", version: "v1.2.4", tag: "v1.2.3", want: updateState{State: "current"}},
		{name: "numeric order", version: "v1.9.0", tag: "v1.10.0", want: updateState{State: "available", Version: "v1.10.0", URL: releaseURL}},
		{name: "dirty equal", version: "v1.2.3+dirty", tag: "v1.2.3", want: updateState{State: "current"}},
		{name: "dirty newer", version: "v1.2.2+dirty", tag: "v1.2.3", want: updateState{State: "available", Version: "v1.2.3", URL: releaseURL}},
		{name: "pseudo-version before release", version: "v1.2.3-0.20261109010203-0123456789ab", tag: "v1.2.3", want: updateState{State: "available", Version: "v1.2.3", URL: releaseURL}},
		{name: "pseudo-version after release", version: "v1.2.4-0.20261109010203-0123456789ab", tag: "v1.2.3", want: updateState{State: "current"}},
		{name: "dirty pseudo-version", version: "v1.2.4-0.20261109010203-0123456789ab+dirty", tag: "v1.2.3", want: updateState{State: "current"}},
		{name: "development", version: "(devel)", tag: "v1.2.3", want: updateState{State: "available", Version: "v1.2.3", URL: releaseURL}},
		{name: "invalid own version", version: "invalid", tag: "v1.2.3", want: updateState{State: "available", Version: "v1.2.3", URL: releaseURL}},
		{name: "fallback equal", version: "(devel)", tag: "v0.0.0", want: updateState{State: "current"}},
		{name: "prerelease", version: "v1.2.3-rc.1", tag: "v1.2.3", want: updateState{State: "available", Version: "v1.2.3", URL: releaseURL}},
		{name: "no releases", version: "v1.2.3", status: http.StatusNotFound, body: `{"message":"Not Found"}`, want: updateState{State: "current"}},
		{name: "server error", version: "v1.2.3", status: http.StatusInternalServerError, want: updateState{State: "failed"}},
		{name: "rate limit", version: "v1.2.3", status: http.StatusForbidden, want: updateState{State: "failed"}},
		{name: "malformed JSON", version: "v1.2.3", body: `{`, want: updateState{State: "failed"}},
		{name: "trailing JSON", version: "v1.2.3", body: `{"tag_name":"v2.0.0"} {}`, want: updateState{State: "failed"}},
		{name: "invalid tag", version: "v1.2.3", tag: "release", want: updateState{State: "failed"}},
		{name: "missing tag", version: "v1.2.3", body: `{}`, want: updateState{State: "failed"}},
		{name: "null response", version: "v1.2.3", body: `null`, want: updateState{State: "failed"}},
		{name: "oversized response", version: "v1.2.3", body: `{"body":"` + strings.Repeat("x", 1<<20) + `","tag_name":"v2.0.0"}`, want: updateState{State: "failed"}},
		{name: "redirect", version: "v1.2.3", status: http.StatusFound, want: updateState{State: "failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/latest" || r.URL.RawQuery != "" || r.ContentLength != 0 {
					t.Errorf("unexpected request: %s %s, content length %d", r.Method, r.URL, r.ContentLength)
				}
				if r.Header.Get("Accept") != "application/vnd.github+json" || r.UserAgent() != "vrc-tsuyaku/"+tc.version || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected request headers")
				}
				status := tc.status
				if status == 0 {
					status = http.StatusOK
				}
				if status == http.StatusFound {
					w.Header().Set("Location", "/redirected")
				}
				w.WriteHeader(status)
				if tc.body != "" {
					fmt.Fprint(w, tc.body)
				} else {
					_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": tc.tag, "html_url": releaseURL})
				}
			}))
			defer server.Close()
			if got := fetchUpdate(t.Context(), server.URL+"/latest", server.Client(), tc.version); got != tc.want {
				t.Fatalf("update = %+v, want %+v", got, tc.want)
			}
			if calls.Load() != 1 {
				t.Fatalf("requests = %d, want 1", calls.Load())
			}
		})
	}
}

func TestUpdateTimeout(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 100 * time.Millisecond
	if got := fetchUpdate(t.Context(), server.URL, client, "v1.0.0"); got != (updateState{State: "failed"}) {
		t.Fatalf("timeout update = %+v", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("requests = %d, want 1", calls.Load())
	}
}

func TestUpdateStateAndEvent(t *testing.T) {
	for _, state := range []string{"available", "current", "failed"} {
		t.Run(state, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					close(started)
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				switch state {
				case "available":
					fmt.Fprint(w, `{"tag_name":"v2.0.0","html_url":"https://example.com/release"}`)
				case "current":
					w.WriteHeader(http.StatusNotFound)
				case "failed":
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer server.Close()
			a := testApp(t)
			a.info = appBuildInfo(&debug.BuildInfo{Main: debug.Module{Version: "v1.0.0"}})
			a.checkUpdate(server.URL, server.Client())
			defer a.close()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("update did not start in the background")
			}
			for range 3 {
				if got := updateFromState(t, a); got != (updateState{State: "checking"}) {
					t.Fatalf("initial update = %+v", got)
				}
			}
			close(release)
			a.updateWG.Wait()
			want := updateState{State: state}
			if state == "available" {
				want.Version, want.URL = "v2.0.0", "https://example.com/release"
			}
			for range 3 {
				if got := updateFromState(t, a); got != want {
					t.Fatalf("completed update = %+v, want %+v", got, want)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("requests = %d, want 1", calls.Load())
			}
			history, _ := a.events.after(0)
			if len(history) != 1 || history[0].Kind != "update" || history[0].Text != state || history[0].Flow != "" {
				t.Fatalf("update events = %+v", history)
			}
		})
	}
}

func TestCloseCancelsUpdate(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	a := testApp(t)
	a.checkUpdate(server.URL, server.Client())
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("update did not start")
	}
	if err := a.close(); err != nil {
		t.Fatal(err)
	}
	if got := updateFromState(t, a); got != (updateState{State: "failed"}) {
		t.Fatalf("cancelled update = %+v", got)
	}
}

func TestUpdateRequestDeadline(t *testing.T) {
	if latestReleaseURL != "https://api.github.com/repos/motoki317/vrc-tsuyaku/releases/latest" {
		t.Fatal("unexpected release endpoint", latestReleaseURL)
	}
	client := &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if remaining := time.Until(deadline); !ok || remaining <= 9*time.Second || remaining > 10*time.Second {
			t.Errorf("request deadline = %s, present = %v", remaining, ok)
		}
		return nil, context.DeadlineExceeded
	})}
	if got := fetchUpdate(t.Context(), "http://127.0.0.1/latest", client, "v1.0.0"); got != (updateState{State: "failed"}) {
		t.Fatalf("request failure update = %+v", got)
	}
}

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func updateFromState(t *testing.T, a *app) updateState {
	t.Helper()
	w := request(a, "GET", "/api/state", "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	var state struct {
		Update updateState `json:"update"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state.Update
}
