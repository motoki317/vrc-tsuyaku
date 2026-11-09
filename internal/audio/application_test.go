package audio

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestApplicationCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Application("unused").Capture(ctx, func([]float32) error { t.Fatal("called consumer"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// The two test players must emit 440 Hz and 1000 Hz into a named null sink.
// They must refuse to play if that sink is missing.
func TestApplicationIsolation(t *testing.T) {
	name := os.Getenv("VRC_TSUYAKU_TEST_APPLICATION")
	if name == "" {
		t.Skip("set VRC_TSUYAKU_TEST_APPLICATION to the 440 Hz test player's name")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var samples []float32
	done := errors.New("captured three seconds")
	err := Application(name).Capture(ctx, func(chunk []float32) error {
		samples = append(samples, chunk...)
		if len(samples) >= 3*SampleRate {
			return done
		}
		return nil
	})
	if err != done {
		t.Fatal(err)
	}
	amplitude := func(frequency float64) float64 {
		var re, im float64
		for i, x := range samples {
			phase := 2 * math.Pi * frequency * float64(i) / SampleRate
			re += float64(x) * math.Cos(phase)
			im += float64(x) * math.Sin(phase)
		}
		return 2 * math.Hypot(re, im) / float64(len(samples))
	}
	wanted, other := amplitude(440), amplitude(1000)
	if wanted < .001 || other > wanted*.01 {
		t.Fatalf("selected 440 Hz amplitude %g; other 1000 Hz amplitude %g", wanted, other)
	}
	t.Logf("%d samples: selected 440 Hz %g, rejected 1000 Hz %g", len(samples), wanted, other)
}

func TestApplicationAbsent(t *testing.T) {
	if os.Getenv("VRC_TSUYAKU_TEST_APPLICATION_ABSENT") == "" {
		t.Skip("set VRC_TSUYAKU_TEST_APPLICATION_ABSENT for the no-playback process lookup check")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err := Application("vrc-tsuyaku-test-absent-process").Capture(ctx, func([]float32) error { t.Fatal("captured absent process"); return nil })
	if err == nil || !strings.Contains(err.Error(), "is not running") {
		t.Fatalf("absent process: %v", err)
	}
}
