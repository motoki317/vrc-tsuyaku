package audio

import (
	"context"
	"errors"
	"math"
	"os"
	"testing"
	"time"
)

func TestCancelledCapture(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := Microphone("unused-test-source").Capture(ctx, func([]float32) error {
		t.Fatal("consumer called after cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Capture = %v", err)
	}
}

func testMicrophone(t *testing.T) Source {
	t.Helper()
	id := os.Getenv("VRC_TSUYAKU_TEST_MICROPHONE")
	if id == "" {
		t.Skip("set VRC_TSUYAKU_TEST_MICROPHONE to a test source made from a null sink")
	}
	return Microphone(id)
}

// TestMicrophone checks capture from the source named by VRC_TSUYAKU_TEST_MICROPHONE.
// On Linux, use a remap source of a null sink monitor. The example names must
// be unused, and both module loads must succeed before the test can run.
// Only a person runs these checks on Windows because Windows lacks a built-in null capture device.
//
//	(
//	    set -e
//	    sink_module=$(pactl load-module module-null-sink sink_name=vrc_test)
//	    trap 'pactl unload-module "$sink_module"' EXIT
//	    (
//	        source_module=$(pactl load-module module-remap-source source_name=vrc_test_mic master=vrc_test.monitor)
//	        trap 'pactl unload-module "$source_module"' EXIT
//	        nix develop -c env VRC_TSUYAKU_TEST_MICROPHONE=vrc_test_mic go test -v -timeout=10m ./internal/audio -run '^TestMicrophone(Continuous|ConsumerStall)?$'
//	    )
//	)
func TestMicrophone(t *testing.T) {
	source := testMicrophone(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var count int
	done := errors.New("recorded enough audio")
	err := source.Capture(ctx, func(samples []float32) error {
		for _, v := range samples {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatal("non-finite audio sample")
			}
		}
		count += len(samples)
		if count >= SampleRate/10 {
			return done
		}
		return nil
	})
	if !errors.Is(err, done) {
		t.Fatalf("Capture = %v, samples = %d", err, count)
	}
	t.Run("unavailable", func(t *testing.T) {
		err := Microphone("not-a-microphone").Capture(ctx, func([]float32) error {
			t.Fatal("consumer called for an unavailable microphone")
			return nil
		})
		if err == nil || ctx.Err() != nil {
			t.Fatalf("unavailable microphone capture = %v, context = %v", err, ctx.Err())
		}
	})
}

// TestMicrophoneConsumerStall checks capture during consumer stalls.
// Use the [TestMicrophone] setup with the command below. VRC_TSUYAKU_TEST_MICROPHONE names its null-sink test source.
// Only a person runs this on Windows because Windows lacks a built-in null capture device.
//
//	nix develop -c env VRC_TSUYAKU_TEST_MICROPHONE=vrc_test_mic go test -v -timeout=10m ./internal/audio -run '^TestMicrophoneConsumerStall$'
func TestMicrophoneConsumerStall(t *testing.T) {
	source := testMicrophone(t)
	for _, stall := range []time.Duration{750 * time.Millisecond, 1250 * time.Millisecond} {
		t.Run(stall.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			var start time.Time
			var samples int
			done := errors.New("captured two seconds after stall")
			err := source.Capture(ctx, func(chunk []float32) error {
				if start.IsZero() {
					start = time.Now()
					time.Sleep(stall)
					return nil
				}
				samples += len(chunk)
				if samples >= 2*SampleRate {
					return done
				}
				return nil
			})
			if stall > time.Second {
				if !errors.Is(err, errCaptureOverflow) {
					t.Fatalf("long stall: Capture = %v", err)
				}
				t.Logf("long stall reported %v", err)
				return
			}
			if err != done {
				t.Fatalf("short stall: Capture = %v", err)
			}
			elapsed := time.Since(start)
			duration := time.Duration(samples) * time.Second / SampleRate
			// Wall time independently detects discarded backlog, even from a
			// silent device. Allow scheduling jitter and a capture packet.
			if elapsed-duration > 150*time.Millisecond || duration-elapsed > 150*time.Millisecond {
				t.Fatalf("short stall lost or duplicated audio: %s audio in %s", duration, elapsed)
			}
			t.Logf("short stall retained %d samples (%s) in %s", samples, duration, elapsed)
		})
	}
}

// TestMicrophoneContinuous checks sustained capture from the test source.
// Use the [TestMicrophone] setup with the command below. VRC_TSUYAKU_TEST_MICROPHONE names its null-sink test source.
// Only a person runs this on Windows because Windows lacks a built-in null capture device.
//
//	nix develop -c env VRC_TSUYAKU_TEST_MICROPHONE=vrc_test_mic go test -v -timeout=10m ./internal/audio -run '^TestMicrophoneContinuous$'
func TestMicrophoneContinuous(t *testing.T) {
	source := testMicrophone(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var count int
	done := errors.New("captured ten seconds")
	start := time.Now()
	err := source.Capture(ctx, func(samples []float32) error {
		count += len(samples)
		if count >= 10*SampleRate {
			return done
		}
		return nil
	})
	if err != done {
		t.Fatalf("continuous capture = %v after %d samples", err, count)
	}
	t.Logf("test source delivered %d samples in %s", count, time.Since(start))
}
