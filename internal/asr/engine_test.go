package asr

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	ort "github.com/getcharzp/onnxruntime_purego"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

type cachedTestModel struct {
	closes int
}

func (m *cachedTestModel) close() { m.closes++ }

func TestModelCacheReleasesUnused(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint("shared=", shared), func(t *testing.T) {
			e := &Engine{ort: &ort.Engine{}}
			defer e.Close()
			cache := make(map[string]*cachedModel[*cachedTestModel])
			defer closeModels(cache)
			loads := 0
			load := func(key string) (*cachedTestModel, func()) {
				t.Helper()
				model, release, err := loadModel(e, cache, key, func() (*cachedTestModel, error) {
					loads++
					return &cachedTestModel{}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				return model, release
			}
			haas, releaseSelf := load("Haas")
			var releaseWorld func()
			if shared {
				var world *cachedTestModel
				world, releaseWorld = load("Haas")
				if world != haas || loads != 1 {
					t.Fatal("two flows did not share one model")
				}
			}
			releaseSelf()
			releaseSelf()
			if shared && (haas.closes != 0 || cache["Haas"] == nil) {
				t.Fatal("restarting one flow released the other flow's model")
			}
			if !shared && (haas.closes != 1 || cache["Haas"] != nil) {
				t.Fatal("unused Haas model stayed cached")
			}
			v3, releaseV3 := load("Parakeet v3")
			if shared {
				releaseWorld()
			}
			if haas.closes != 1 || len(cache) != 1 || cache["Parakeet v3"].model != v3 {
				t.Fatal("switch retained an unused model or released the new model")
			}
			releaseV3()
			if v3.closes != 1 || len(cache) != 0 {
				t.Fatal("stopping the last flow retained its model")
			}
			next, releaseNext := load("Haas")
			if next == haas || loads != 3 {
				t.Fatal("restarting reused a released model")
			}
			releaseSelf()
			if next.closes != 0 {
				t.Fatal("an old release closed a new cache entry")
			}
			releaseNext()
		})
	}
}

func TestReleaseWaitsForInference(t *testing.T) {
	e := &Engine{ort: &ort.Engine{}}
	defer e.Close()
	cache := make(map[string]*cachedModel[*cachedTestModel])
	model, release, err := loadModel(e, cache, "model", func() (*cachedTestModel, error) { return &cachedTestModel{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	e.mu.RLock()
	released := make(chan struct{})
	go func() { release(); close(released) }()
	select {
	case <-released:
		e.mu.RUnlock()
		t.Fatal("released a model during inference")
	case <-time.After(20 * time.Millisecond):
	}
	e.mu.RUnlock()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("release did not finish after inference")
	}
	if model.closes != 1 {
		t.Fatal("model did not close exactly once")
	}
}

func TestReleaseAfterEngineClose(t *testing.T) {
	e := &Engine{ort: &ort.Engine{}, models: make(map[catalog.HaasFiles]*cachedModel[*Haas])}
	model := &Haas{engine: e}
	_, release, err := loadModel(e, e.models, catalog.HaasFiles{}, func() (*Haas, error) { return model, nil })
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	release()
	release()
	if len(e.models) != 0 {
		t.Fatal("engine close retained the model cache")
	}
}

func TestReleasedModelsRejectInference(t *testing.T) {
	e := &Engine{ort: &ort.Engine{}}
	defer e.Close()
	for _, model := range []Recognizer{&Haas{engine: e}, &Parakeet{engine: e}} {
		if _, err := model.Transcribe(t.Context(), []float32{0}); err == nil {
			t.Fatal("released recognizer accepted inference")
		}
	}
	if _, err := (&Silero{engine: e}).NewStream().Probability(t.Context(), make([]float32, 512)); err == nil {
		t.Fatal("released Silero session accepted inference")
	}
}

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
