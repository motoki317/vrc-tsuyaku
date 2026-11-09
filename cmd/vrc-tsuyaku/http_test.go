package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestExitRequiresTokenAndFlushesBeforeCancellation(t *testing.T) {
	a := testApp(t)
	for _, token := range []string{"", "wrong", a.token + "x"} {
		if w := request(a, "POST", "/api/exit", "", token); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized exit = %d", w.Code)
		}
		if a.ctx.Err() != nil {
			t.Fatal("unauthorized request cancelled the app")
		}
	}
	if w := request(a, "GET", "/api/exit", "", a.token); w.Code < http.StatusBadRequest || a.ctx.Err() != nil {
		t.Fatal("GET exited the app or accepted the wrong method", w.Code)
	}
	w := &exitResponseWriter{ResponseRecorder: httptest.NewRecorder(), ctx: a.ctx}
	r := httptest.NewRequest("POST", "/api/exit", nil)
	r.Header.Set("Authorization", "Bearer "+a.token)
	a.handler().ServeHTTP(w, r)
	if w.Code != http.StatusAccepted || w.Header().Get("Content-Length") != "0" || !w.Flushed || w.cancelledBeforeFlush {
		t.Fatalf("exit response: status %d, length %q, flushed %v, cancelled before flush %v", w.Code, w.Header().Get("Content-Length"), w.Flushed, w.cancelledBeforeFlush)
	}
	if !errors.Is(a.ctx.Err(), context.Canceled) {
		t.Fatal("Exit did not cancel the app")
	}
	for range 2 {
		if w := request(a, "POST", "/api/exit", "", a.token); w.Code != http.StatusAccepted {
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
	if w := request(a, "POST", "/api/exit", "", a.token); w.Code != http.StatusAccepted {
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
