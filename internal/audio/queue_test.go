package audio

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestCaptureQueueCapacityAndOrder(t *testing.T) {
	q := newCaptureQueue()
	if err := q.push(nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-q.ready:
		t.Fatal("empty packet woke the consumer")
	default:
	}
	// Fill exactly one second with varying packet sizes. Packet boundaries
	// must not change the capacity or lose, duplicate, or reorder samples.
	for start := 0; start < SampleRate; {
		count := min(1+start%503, SampleRate-start)
		packet := make([]float32, count)
		for i := range count {
			packet[i] = float32(start + i)
		}
		if err := q.push(packet); err != nil {
			t.Fatalf("one second should fit: %v", err)
		}
		clear(packet)
		start += count
	}
	samples := q.take()
	if len(samples) != SampleRate {
		t.Fatalf("queued %d samples, want %d", len(samples), SampleRate)
	}
	for i, v := range samples {
		if v != float32(i) {
			t.Fatalf("sample %d = %g", i, v)
		}
	}
	// Dequeuing frees capacity without changing the consumer's retained slice.
	if err := q.push(make([]float32, SampleRate)); err != nil {
		t.Fatal(err)
	}
	if err := q.push([]float32{1}); err != errCaptureOverflow {
		t.Fatalf("one second plus one sample = %v", err)
	}
	if samples[len(samples)-1] != SampleRate-1 {
		t.Fatal("subsequent packets changed retained samples")
	}
}

func TestCaptureQueueOverflowDuringConsumption(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newCaptureQueue()
		if err := q.push([]float32{1}); err != nil {
			t.Fatal(err)
		}
		resume := make(chan struct{})
		result := make(chan error, 1)
		calls := 0
		go func() {
			result <- q.consume(t.Context(), func([]float32) error {
				calls++
				<-resume
				return nil
			})
		}()
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("consumer calls = %d", calls)
		}
		if err := q.push(make([]float32, SampleRate)); err != nil {
			t.Fatal(err)
		}
		err := q.push([]float32{1})
		if err != errCaptureOverflow {
			t.Fatalf("overflow during consumption = %v", err)
		}
		q.fail(err)
		close(resume)
		if err := <-result; err != errCaptureOverflow || calls != 1 {
			t.Fatalf("consume after overflow = %v, calls = %d", err, calls)
		}
	})
}

func TestCaptureQueueStops(t *testing.T) {
	t.Run("consumer error", func(t *testing.T) {
		q := newCaptureQueue()
		if err := q.push([]float32{1}); err != nil {
			t.Fatal(err)
		}
		failure := errors.New("consumer failed")
		if err := q.consume(t.Context(), func([]float32) error { return failure }); err != failure {
			t.Fatalf("consumer error was wrapped or lost: %v", err)
		}
	})
	t.Run("cancel while waiting", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			q := newCaptureQueue()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- q.consume(ctx, func([]float32) error { return errors.New("unexpected audio") })
			}()
			synctest.Wait()
			cancel()
			if err := <-result; err != context.Canceled {
				t.Fatalf("cancellation = %v", err)
			}
		})
	})
}
