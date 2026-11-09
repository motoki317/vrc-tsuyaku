package asr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	ort "github.com/getcharzp/onnxruntime_purego"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

type cachedTestModel struct {
	closes int
}

func (m *cachedTestModel) close() { m.closes++ }

type inferenceTestModel struct {
	infer   func(context.Context) error
	release func()
}

func testInferenceModels(t *testing.T) (*Engine, map[string]inferenceTestModel) {
	t.Helper()
	e := &Engine{
		ort: &ort.Engine{}, models: make(map[catalog.HaasFiles]*cachedModel[*Haas]),
		parakeets: make(map[catalog.ParakeetFiles]*cachedModel[*Parakeet]), sileros: make(map[string]*cachedModel[*Silero]),
	}
	t.Cleanup(e.Close)
	haas, releaseHaas, err := loadModel(e, e.models, catalog.HaasFiles{}, func() (*Haas, error) {
		return &Haas{engine: e, encoder: &ort.Session{}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	parakeet, releaseParakeet, err := loadModel(e, e.parakeets, catalog.ParakeetFiles{}, func() (*Parakeet, error) {
		return &Parakeet{engine: e, encoder: &ort.Session{}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	silero, releaseSilero, err := loadModel(e, e.sileros, "silero", func() (*Silero, error) {
		return &Silero{engine: e, session: &ort.Session{}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return e, map[string]inferenceTestModel{
		"Haas": {infer: func(ctx context.Context) error {
			_, err := haas.Transcribe(ctx, []float32{0})
			return err
		}, release: releaseHaas},
		"Parakeet": {infer: func(ctx context.Context) error {
			_, err := parakeet.Transcribe(ctx, []float32{0})
			return err
		}, release: releaseParakeet},
		"Silero": {infer: func(ctx context.Context) error {
			_, err := silero.NewStream().Probability(ctx, make([]float32, 512))
			return err
		}, release: releaseSilero},
	}
}

// Pause cancellation checking so inference needs no native resources or model files.
type pausedInferenceContext struct {
	context.Context
	entered chan struct{}
	resume  chan struct{}
}

func (c *pausedInferenceContext) Err() error {
	close(c.entered)
	<-c.resume
	return context.Canceled
}

func pauseInference(t *testing.T, infer func(context.Context) error) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	paused := &pausedInferenceContext{Context: ctx, entered: make(chan struct{}), resume: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- infer(paused) }()
	resume := sync.OnceFunc(func() {
		close(paused.resume)
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Error("inference did not stop at cancellation", err)
		}
	})
	t.Cleanup(resume)
	select {
	case <-paused.entered:
	case <-time.After(time.Second):
		t.Fatal("inference did not reach cancellation checking")
	}
	return resume
}

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
	for _, name := range []string{"Haas", "Parakeet", "Silero"} {
		t.Run(name, func(t *testing.T) {
			_, models := testInferenceModels(t)
			model := models[name]
			resume := pauseInference(t, model.infer)
			released := make(chan struct{})
			go func() { model.release(); close(released) }()
			select {
			case <-released:
				t.Fatal("released a model during inference")
			case <-time.After(20 * time.Millisecond):
			}
			resume()
			select {
			case <-released:
			case <-time.After(time.Second):
				t.Fatal("release did not finish after inference")
			}
			if err := model.infer(t.Context()); err == nil {
				t.Fatal("released model accepted inference")
			}
		})
	}
}

func TestReleaseAllowsOtherModelInference(t *testing.T) {
	e, models := testInferenceModels(t)
	_, releaseShared, err := loadModel(e, e.parakeets, catalog.ParakeetFiles{}, func() (*Parakeet, error) {
		t.Fatal("shared model was not cached")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resume := pauseInference(t, models["Parakeet"].infer)
	var pending []<-chan struct{}
	t.Cleanup(func() {
		resume()
		for _, done := range pending {
			<-done
		}
	})
	for _, tc := range []struct {
		name    string
		release func()
	}{
		{"last release of another model", models["Haas"].release},
		{"shared release of the active model", releaseShared},
	} {
		done := make(chan struct{})
		pending = append(pending, done)
		go func() { tc.release(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error(tc.name + " waited for unrelated inference")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := make(chan error, 1)
	go func() { result <- models["Silero"].infer(ctx) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Error("release blocked inference on a third model")
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
