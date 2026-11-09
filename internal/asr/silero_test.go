package asr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestSileroProbabilities(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the upstream probability reference is generated on Linux")
	}
	lib := requireEnv(t, "VRC_TSUYAKU_TEST_ORT_LIB")
	modelPath := requireEnv(t, "VRC_TSUYAKU_TEST_SILERO_MODEL")
	wavPath := requireEnv(t, "VRC_TSUYAKU_TEST_SILERO_WAV")
	refPath := requireEnv(t, "VRC_TSUYAKU_TEST_SILERO_REF")
	data, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Samples       int       `json:"sample_count"`
		Probabilities []float32 `json:"probabilities"`
	}
	if err := json.Unmarshal(data, &ref); err != nil {
		t.Fatal(err)
	}
	audio := readTestWAV(t, filepath.Dir(wavPath), strings.TrimSuffix(filepath.Base(wavPath), ".wav"))
	if len(audio) == 0 || len(audio) != ref.Samples || len(ref.Probabilities) != (len(audio)+511)/512 {
		t.Fatalf("reference has %d samples/%d probabilities for %d audio samples", ref.Samples, len(ref.Probabilities), len(audio))
	}
	e, err := NewEngine(lib)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	model, release, err := e.LoadSilero(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	stream := model.NewStream()
	var maxDifference float64
	for i, want := range ref.Probabilities {
		var window [512]float32
		copy(window[:], audio[i*512:min((i+1)*512, len(audio))])
		got, err := stream.Probability(t.Context(), window[:])
		if err != nil {
			t.Fatalf("window %d: %v", i, err)
		}
		difference := math.Abs(float64(got) - float64(want))
		if math.IsNaN(float64(want)) || want < 0 || want > 1 || difference > 1e-6 {
			t.Fatalf("window %d: got %.9g, want %.9g, difference %.9g", i, got, want, difference)
		}
		maxDifference = max(maxDifference, difference)
	}
	t.Logf("%d/%d windows within 1e-6; maximum absolute difference %.9g", len(ref.Probabilities), len(ref.Probabilities), maxDifference)
}

func TestSileroLifecycle(t *testing.T) {
	lib := requireEnv(t, "VRC_TSUYAKU_TEST_ORT_LIB")
	modelPath := requireEnv(t, "VRC_TSUYAKU_TEST_SILERO_MODEL")
	e, err := NewEngine(lib)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	if _, _, err := e.LoadSilero(""); err == nil {
		t.Fatal("accepted an empty model path")
	}
	if _, _, err := e.LoadSilero(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("loaded a missing model")
	}
	model, release, err := e.LoadSilero(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if again, releaseAgain, err := e.LoadSilero(modelPath); err != nil || again != model {
		t.Fatalf("session not shared: %v", err)
	} else {
		defer releaseAgain()
	}
	audio := make([]float32, 512)
	for i := range audio {
		audio[i] = float32(math.Sin(float64(i)*0.1)) * 0.1
	}
	before := slices.Clone(audio)
	stream := model.NewStream()
	want := make([]float32, 20)
	for i := range want {
		want[i], err = stream.Probability(t.Context(), audio)
		if err != nil {
			t.Fatal(err)
		}
	}
	state, history := stream.state, stream.context
	for _, invalid := range [][]float32{nil, {0}, make([]float32, 513), append(make([]float32, 511), float32(math.NaN())), append(make([]float32, 511), float32(math.Inf(1)))} {
		if _, err := stream.Probability(t.Context(), invalid); err == nil {
			t.Fatal("accepted invalid audio")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := stream.Probability(ctx, audio); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled inference: %v", err)
	}
	if state != stream.state || history != stream.context {
		t.Fatal("rejected input changed stream state")
	}
	stream.Reset()
	// A reset stream and a new stream must reproduce the same sequence while
	// sharing a session, even when their calls overlap.
	results := make(chan error, 2)
	for _, candidate := range []*SileroStream{stream, model.NewStream()} {
		go func() {
			for i, expected := range want {
				got, err := candidate.Probability(t.Context(), audio)
				if err != nil || got != expected {
					results <- fmt.Errorf("window %d: got %g, want %g, error %v", i, got, expected, err)
					return
				}
			}
			results <- nil
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(audio, before) {
		t.Fatal("inference changed caller audio")
	}
	e.Close()
	e.Close()
	if _, err := stream.Probability(t.Context(), audio); err == nil {
		t.Fatal("inference succeeded after close")
	}
	if _, _, err := e.LoadSilero(modelPath); err == nil {
		t.Fatal("load succeeded after close")
	}
}
