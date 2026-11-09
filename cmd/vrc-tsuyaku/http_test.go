package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/download"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

type exitResponseWriter struct {
	*httptest.ResponseRecorder
	ctx                  context.Context
	cancelledBeforeFlush bool
}

func (w *exitResponseWriter) Flush() {
	w.cancelledBeforeFlush = w.ctx.Err() != nil
	w.ResponseRecorder.Flush()
}

func localRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 54321}
	r.Host = local.String()
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, local))
}

func TestHostCheck(t *testing.T) {
	a := testApp(t)
	for _, tc := range []struct {
		host string
		want int
	}{
		{"127.0.0.1:54321", http.StatusOK},
		{"localhost:54321", http.StatusOK},
		{"LoCaLhOsT:54321", http.StatusOK},
		{"evil.example:54321", http.StatusForbidden},
		{"127.0.0.1.evil.example:54321", http.StatusForbidden},
		{"localhost.evil.example:54321", http.StatusForbidden},
		{"localhost.:54321", http.StatusForbidden},
		{"127.0.0.1:54322", http.StatusForbidden},
		{"localhost:54322", http.StatusForbidden},
		{"127.0.0.2:54321", http.StatusForbidden},
		{"[::1]:54321", http.StatusForbidden},
		{"127.0.0.1", http.StatusForbidden},
		{"localhost", http.StatusForbidden},
		{"", http.StatusForbidden},
	} {
		for _, method := range []string{"GET", "HEAD"} {
			for _, path := range []string{"/", "/app.js", "/api/state"} {
				t.Run(tc.host+method+path, func(t *testing.T) {
					r := localRequest(method, path, "")
					r.Host = tc.host
					w := httptest.NewRecorder()
					a.handler().ServeHTTP(w, r)
					if w.Code != tc.want {
						t.Fatalf("status = %d, want %d", w.Code, tc.want)
					}
					checkSecurityHeaders(t, w)
					if w.Code == http.StatusForbidden && w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
						t.Fatal("Host rejection did not return plain text")
					}
				})
			}
		}
	}
	for _, host := range []string{"127.0.0.1:12345", "localhost:12345"} {
		r := localRequest("GET", "/api/state", "")
		r.Host = host
		local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, local))
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatal("Host check did not use the connection's local port", w.Code)
		}
	}
	r := localRequest("GET", "/api/state", "").WithContext(context.Background())
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("missing connection address did not fail closed", w.Code)
	}
}

func TestOriginCheck(t *testing.T) {
	a := testApp(t)
	for _, host := range []string{"127.0.0.1:54321", "localhost:54321", "LOCALHOST:54321"} {
		for _, tc := range []struct {
			name    string
			origins []string
			want    int
		}{
			{"absent", nil, http.StatusNotFound},
			{"own", []string{"http://" + host}, http.StatusNotFound},
			{"foreign", []string{"http://evil.example"}, http.StatusForbidden},
			{"null", []string{"null"}, http.StatusForbidden},
			{"empty", []string{""}, http.StatusForbidden},
			{"HTTPS", []string{"https://" + host}, http.StatusForbidden},
			{"wrong port", []string{"http://127.0.0.1:54322"}, http.StatusForbidden},
			{"path", []string{"http://" + host + "/"}, http.StatusForbidden},
			{"multiple", []string{"http://" + host, "http://evil.example"}, http.StatusForbidden},
			{"joined", []string{"http://" + host + ", http://evil.example"}, http.StatusForbidden},
		} {
			for _, method := range []string{"POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
				for _, path := range []string{"/api/unknown", "/unknown.txt"} {
					t.Run(host+"/"+tc.name+method+path, func(t *testing.T) {
						r := localRequest(method, path, "")
						r.Host = host
						for _, origin := range tc.origins {
							r.Header.Add("Origin", origin)
						}
						w := httptest.NewRecorder()
						a.handler().ServeHTTP(w, r)
						if w.Code != tc.want {
							t.Fatalf("status = %d, want %d", w.Code, tc.want)
						}
						if w.Code == http.StatusForbidden {
							checkSecurityHeaders(t, w)
							if w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
								t.Fatal("Origin rejection did not return plain text")
							}
						}
					})
				}
			}
		}
	}
	for _, method := range []string{"GET", "HEAD"} {
		for _, path := range []string{"/", "/api/state"} {
			r := localRequest(method, path, "")
			r.Header.Set("Origin", "http://evil.example")
			w := httptest.NewRecorder()
			a.handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("foreign Origin rejected for %s %s: %d", method, path, w.Code)
			}
			checkSecurityHeaders(t, w)
		}
	}
}

func TestHeaderChecksBeforeEveryRoute(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"/", "/app.js", "/api/state", "/api/events", "/api/settings", "/api/microphones", "/api/downloads/unknown", "/api/flows/self/start", "/api/chatbox/preview", "/api/open/data", "/api/exit", "/api/unknown"} {
		for _, header := range []string{"Host", "Origin"} {
			r := localRequest("POST", path, "")
			if header == "Host" {
				r.Host = "evil.example:54321"
				r.Header.Set("Origin", "http://"+r.Host)
			} else {
				r.Header.Set("Origin", "http://evil.example")
			}
			w := httptest.NewRecorder()
			a.handler().ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s %s: %d", header, path, w.Code)
			}
		}
	}
}

func checkSecurityHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for name, want := range map[string]string{
		"Cache-Control":           "no-store",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
	} {
		if got := w.Header().Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	for name := range w.Header() {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Fatal("unexpected CORS header", name)
		}
	}
}

func TestExitChecksHeadersAndFlushesBeforeCancellation(t *testing.T) {
	a := testApp(t)
	for _, header := range []string{"Host", "Origin"} {
		r := localRequest("POST", "/api/exit", "")
		if header == "Host" {
			r.Host = "evil.example"
		} else {
			r.Header.Set("Origin", "http://evil.example")
		}
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("foreign %s exit = %d", header, w.Code)
		}
		if a.ctx.Err() != nil {
			t.Fatal("rejected request cancelled the app")
		}
	}
	if w := request(a, "GET", "/api/exit", ""); w.Code < http.StatusBadRequest || a.ctx.Err() != nil {
		t.Fatal("GET exited the app or accepted the wrong method", w.Code)
	}
	w := &exitResponseWriter{ResponseRecorder: httptest.NewRecorder(), ctx: a.ctx}
	r := localRequest("POST", "/api/exit", "")
	a.handler().ServeHTTP(w, r)
	if w.Code != http.StatusAccepted || w.Header().Get("Content-Length") != "0" || !w.Flushed || w.cancelledBeforeFlush {
		t.Fatalf("exit response: status %d, length %q, flushed %v, cancelled before flush %v", w.Code, w.Header().Get("Content-Length"), w.Flushed, w.cancelledBeforeFlush)
	}
	if !errors.Is(a.ctx.Err(), context.Canceled) {
		t.Fatal("Exit did not cancel the app")
	}
	for range 2 {
		if w := request(a, "POST", "/api/exit", ""); w.Code != http.StatusAccepted {
			t.Fatalf("repeated exit = %d", w.Code)
		}
	}
}

func TestExitCancelsActiveWork(t *testing.T) {
	a, starts := pendingDownloadApp(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.translator, err = translate.New(executable, "unused-model")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.translator.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	a.startEnabled()
	waitFor(t, func() bool { return starts.Load() == 2 })
	state := a.artifacts[catalog.Haas]
	state.State = "missing"
	a.artifacts[catalog.Haas] = state
	downloading := make(chan struct{})
	cancelled := make(chan struct{})
	a.downloadArtifact = func(ctx context.Context, _, _ string, _ func(download.Progress)) error {
		close(downloading)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}
	if err := a.startDownload(catalog.Haas, false); err != nil {
		t.Fatal(err)
	}
	<-downloading
	if w := request(a, "POST", "/api/exit", ""); w.Code != http.StatusAccepted {
		t.Fatal(w.Code)
	}
	if err := a.close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("download did not observe cancellation")
	}
	for name, run := range a.flows {
		if run.status.State != "stopped" {
			t.Fatalf("%s flow remains active", name)
		}
		select {
		case <-run.done:
		default:
			t.Fatalf("%s flow did not finish", name)
		}
	}
	if err := a.translator.Wait(t.Context()); errcode.Of(err) != errcode.TranslatorExited {
		t.Fatalf("llama-server remains active: %v", err)
	}
}
