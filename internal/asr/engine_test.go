package asr

import (
	"context"
	"errors"
	"testing"
	"time"

	ort "github.com/getcharzp/onnxruntime_purego"
)

func TestLoadAllowsInferenceAndCloseWaits(t *testing.T) {
	// A zero native engine needs no library. Cancellation ends inference before
	// it reaches ORT, and Close has no native resources to release.
	e := &Engine{ort: &ort.Engine{}}
	if err := e.beginLoad(); err != nil {
		t.Fatal(err)
	}
	loading := true
	inference := false
	t.Cleanup(func() {
		if inference {
			e.mu.RUnlock()
		}
		if loading {
			e.endLoad()
		}
		e.Close()
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := make(chan error, 1)
	go func() {
		_, err := (&Haas{engine: e}).Transcribe(ctx, []float32{0})
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("model load blocked inference")
	}
	// Hold a second inference lifetime beyond the end of the load.
	e.mu.RLock()
	inference = true
	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned during a model load")
	case <-time.After(20 * time.Millisecond):
	}
	e.endLoad()
	loading = false
	select {
	case <-closed:
		t.Fatal("Close returned during inference")
	case <-time.After(20 * time.Millisecond):
	}
	e.mu.RUnlock()
	inference = false
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after the load and inference")
	}
	if err := e.beginLoad(); err == nil {
		e.endLoad()
		t.Fatal("load succeeded after Close")
	}
}
