package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type startupWriter struct {
	url chan string
}

func (w startupWriter) Write(p []byte) (int, error) {
	w.url <- strings.TrimSpace(string(p))
	return len(p), nil
}

func TestRunExit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	for _, trigger := range []string{"exit", "interrupt"} {
		t.Run(trigger, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			resume := make(chan struct{})
			release := sync.OnceFunc(func() { close(resume) })
			updating, cleaning := make(chan struct{}), make(chan struct{})
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			transport.RegisterProtocol("https", updateTransport(func(r *http.Request) (*http.Response, error) {
				close(updating)
				<-r.Context().Done()
				close(cleaning)
				<-resume
				return nil, r.Context().Err()
			}))
			http.DefaultTransport = transport
			t.Cleanup(func() {
				http.DefaultTransport = original
				transport.CloseIdleConnections()
			})
			out := startupWriter{url: make(chan string, 1)}
			done := make(chan struct{})
			var runErr error
			dir := t.TempDir()
			go func() {
				defer close(done)
				runErr = run(ctx, []string{"-data-dir", dir}, out, io.Discard)
			}()
			t.Cleanup(func() {
				cancel()
				release()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("run did not stop")
				}
			})
			var address *url.URL
			select {
			case startup := <-out.url:
				var err error
				address, err = url.Parse(startup)
				if err != nil {
					t.Fatal("invalid startup URL")
				}
			case <-done:
				t.Fatalf("run stopped before startup: %v", runErr)
			case <-time.After(5 * time.Second):
				t.Fatal("run did not report startup")
			}
			token := address.Fragment
			address.Fragment = ""
			address.Path = "/api/exit"
			client := &http.Client{Timeout: 5 * time.Second}
			post := func(authorized bool, want int) {
				t.Helper()
				req, err := http.NewRequestWithContext(t.Context(), "POST", address.String(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if authorized {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("incomplete exit response: %v", err)
				}
				if resp.StatusCode != want {
					t.Fatalf("exit status = %d, want %d", resp.StatusCode, want)
				}
				if want == http.StatusAccepted && (len(body) != 0 || resp.ContentLength != 0) {
					t.Fatal("Exit did not return a complete empty response")
				}
			}
			post(false, http.StatusUnauthorized)
			select {
			case <-updating:
			case <-time.After(5 * time.Second):
				t.Fatal("update request did not start")
			}
			if trigger == "exit" {
				post(true, http.StatusAccepted)
			} else {
				cancel()
			}
			select {
			case <-cleaning:
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown did not cancel the update request")
			}
			select {
			case <-done:
				t.Fatal("run returned before cleanup finished")
			default:
			}
			post(true, http.StatusAccepted)
			release()
			select {
			case <-done:
				if runErr != nil {
					t.Fatalf("run returned an error: %v", runErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("run did not stop after shutdown")
			}
		})
	}
}
