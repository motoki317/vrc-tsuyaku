package asr

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

func TestPadAudio(t *testing.T) {
	for _, n := range []int{1, 15999, 16000, 16001} {
		audio := make([]float32, n)
		audio[0], audio[n-1] = 1, 2
		before := slices.Clone(audio)
		got := padAudio(audio)
		if len(got) != max(n, 16000) {
			t.Fatalf("%d samples: padded length %d", n, len(got))
		}
		left := (len(got) - n) / 2
		if !slices.Equal(got[left:left+n], audio) || !slices.Equal(audio, before) {
			t.Fatalf("%d samples: padding moved or changed samples", n)
		}
		for _, value := range append(slices.Clone(got[:left]), got[left+n:]...) {
			if value != 0 {
				t.Fatalf("%d samples: nonzero padding", n)
			}
		}
	}
}

func TestNewEngineRequiresLibraryPath(t *testing.T) {
	if e, err := NewEngine(""); err == nil {
		e.Close()
		t.Fatal("accepted an empty ORT library path")
	} else if !strings.Contains(err.Error(), "path is empty") {
		t.Fatalf("empty library path: %v", err)
	}
}

func TestClosedEngine(t *testing.T) {
	h := &Haas{engine: &Engine{}}
	if _, err := h.Transcribe(t.Context(), []float32{0}); err == nil {
		t.Fatal("accepted transcription after close")
	}
}

func TestHaasLifecycle(t *testing.T) {
	lib := requireEnv(t, "VRC_TSUYAKU_TEST_ORT_LIB")
	bundle := requireEnv(t, "VRC_TSUYAKU_TEST_HAAS_BUNDLE")
	wavDir := requireEnv(t, "VRC_TSUYAKU_TEST_HAAS_WAV_DIR")
	refs, err := readReferences(requireEnv(t, "VRC_TSUYAKU_TEST_HAAS_REF_FP32"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(lib)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	if other, err := NewEngine(lib); err == nil {
		other.Close()
		t.Fatal("accepted a second live engine")
	}
	h := loadTestHaas(t, e, bundle, false)
	if again := loadTestHaas(t, e, bundle, false); again != h {
		t.Fatal("model sessions were not shared")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.Transcribe(ctx, []float32{0}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	for _, audio := range [][]float32{nil, {float32(math.NaN())}, {float32(math.Inf(1))}} {
		if _, err := h.Transcribe(t.Context(), audio); err == nil {
			t.Fatalf("accepted audio %v", audio)
		}
	}
	audio := readTestWAV(t, wavDir, refs[0].ID)
	// Overlapping calls share sessions but must not share prediction state.
	results := make(chan error, 2)
	for range 2 {
		go func() {
			text, err := h.Transcribe(t.Context(), audio)
			if err == nil && text != refs[0].Hyp {
				err = errors.New("concurrent transcription differs from reference: " + text)
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	e.Close()
	e.Close()
	if _, err := h.Transcribe(t.Context(), audio); err == nil {
		t.Fatal("transcription succeeded after close")
	}
	if _, _, err := e.LoadHaas(catalog.HaasBundle(bundle, false)); err == nil {
		t.Fatal("load succeeded after close")
	}
	if _, err := NewEngine(catalog.HaasBundle(bundle, false).Vocabulary); err == nil || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("different library after close: %v", err)
	}
	// A new engine must recover after the previous environment closes.
	next, err := NewEngine(lib)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}
