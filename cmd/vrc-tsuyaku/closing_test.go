package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"
)

type eventResponseWriter struct {
	*httptest.ResponseRecorder
}

func (*eventResponseWriter) SetWriteDeadline(time.Time) error { return nil }

func openEventStream(t *testing.T, a *app) func() {
	t.Helper()
	r := localRequest("GET", "/api/events", "")
	ctx, cancel := context.WithCancel(r.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.handler().ServeHTTP(&eventResponseWriter{httptest.NewRecorder()}, r.WithContext(ctx))
	}()
	synctest.Wait()
	return func() {
		cancel()
		<-done
	}
}

func signalClosing(t *testing.T, a *app) {
	t.Helper()
	w := request(a, "POST", "/api/closing", "")
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("closing response = %d %q", w.Code, w.Body.String())
	}
	checkSecurityHeaders(t, w)
}

func TestCloseGrace(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, *app)
	}{
		{"signal without stream", func(t *testing.T, a *app) {
			signalClosing(t, a)
			time.Sleep(a.closeGrace / 2)
			if a.ctx.Err() != nil {
				t.Fatal("app exited before the grace ended")
			}
			time.Sleep(a.closeGrace / 2)
			synctest.Wait()
			if a.ctx.Err() == nil {
				t.Fatal("app did not exit after the grace")
			}
		}},
		{"signal with open stream", func(t *testing.T, a *app) {
			stop := openEventStream(t, a)
			signalClosing(t, a)
			time.Sleep(2 * a.closeGrace)
			if a.ctx.Err() != nil {
				t.Fatal("app exited with an open stream")
			}
			stop()
			time.Sleep(2 * a.closeGrace)
			if a.ctx.Err() != nil {
				t.Fatal("a later stream drop rearmed the exit")
			}
		}},
		{"reload during grace", func(t *testing.T, a *app) {
			stop := openEventStream(t, a)
			signalClosing(t, a)
			stop()
			time.Sleep(a.closeGrace / 2)
			stop = openEventStream(t, a)
			defer stop()
			time.Sleep(a.closeGrace)
			if a.ctx.Err() != nil {
				t.Fatal("app exited after a stream opened during the grace")
			}
		}},
		{"two pages close", func(t *testing.T, a *app) {
			stopFirst := openEventStream(t, a)
			stopLast := openEventStream(t, a)
			signalClosing(t, a)
			stopFirst()
			time.Sleep(2 * a.closeGrace)
			if a.ctx.Err() != nil {
				t.Fatal("closing one page exited the app while another remained")
			}
			signalClosing(t, a)
			stopLast()
			time.Sleep(a.closeGrace)
			synctest.Wait()
			if a.ctx.Err() == nil {
				t.Fatal("closing the last page did not exit the app")
			}
		}},
		{"later signal restarts grace", func(t *testing.T, a *app) {
			signalClosing(t, a)
			time.Sleep(3 * a.closeGrace / 4)
			signalClosing(t, a)
			time.Sleep(a.closeGrace / 2)
			if a.ctx.Err() != nil {
				t.Fatal("app exited on the first signal's deadline")
			}
			time.Sleep(a.closeGrace / 2)
			synctest.Wait()
			if a.ctx.Err() == nil {
				t.Fatal("app did not exit on the later signal's deadline")
			}
		}},
		{"stream drops without signal", func(t *testing.T, a *app) {
			stop := openEventStream(t, a)
			stop()
			time.Sleep(10 * a.closeGrace)
			if a.ctx.Err() != nil {
				t.Fatal("a stream drop without a signal exited the app")
			}
		}},
		{"page never opens", func(t *testing.T, a *app) {
			time.Sleep(10 * a.closeGrace)
			if a.ctx.Err() != nil {
				t.Fatal("app exited without a page or a closing signal")
			}
		}},
		{"wrong method", func(t *testing.T, a *app) {
			if w := request(a, "GET", "/api/closing", ""); w.Code < http.StatusBadRequest {
				t.Fatalf("GET accepted the wrong method: %d", w.Code)
			}
			time.Sleep(2 * a.closeGrace)
			if a.ctx.Err() != nil {
				t.Fatal("GET armed the exit")
			}
		}},
		{"failed stream", func(t *testing.T, a *app) {
			a.handler().ServeHTTP(httptest.NewRecorder(), localRequest("GET", "/api/events", ""))
			signalClosing(t, a)
			time.Sleep(a.closeGrace)
			synctest.Wait()
			if a.ctx.Err() == nil {
				t.Fatal("a stream that failed to set its deadline prevented exit")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a := testApp(t)
				if a.closeGrace != 5*time.Second {
					t.Fatalf("default close grace = %s", a.closeGrace)
				}
				a.closeGrace = 100 * time.Millisecond
				tc.run(t, a)
			})
		})
	}
}

func TestCloseStopsGraceTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := testApp(t)
		a.closeGrace = 100 * time.Millisecond
		signalClosing(t, a)
		if err := a.close(); err != nil {
			t.Fatal(err)
		}
		signalClosing(t, a)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.closeTimer != nil {
			t.Fatal("shutdown left or rearmed the close timer")
		}
	})
}
