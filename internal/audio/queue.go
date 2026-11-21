package audio

import (
	"context"
	"errors"
	"sync"
)

var errCaptureOverflow = errors.New("audio capture buffer overflow")

// captureQueueSamples bounds the backlog to one second of audio so a stalled consumer cannot grow the queue indefinitely.
const captureQueueSamples = SampleRate

// Bound queued audio by sample count, since packet sizes vary by device.
// Producers never wait for the consumer, so they can keep draining the OS buffer.
type captureQueue struct {
	mu       sync.Mutex
	samples  []float32
	ready    chan struct{}
	failures chan error
}

func newCaptureQueue() *captureQueue {
	return &captureQueue{ready: make(chan struct{}, 1), failures: make(chan error, 1)}
}

func (q *captureQueue) push(samples []float32) error {
	if len(samples) == 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(samples) > captureQueueSamples-len(q.samples) {
		return errCaptureOverflow
	}
	q.samples = append(q.samples, samples...)
	select {
	case q.ready <- struct{}{}:
	default:
	}
	return nil
}

func (q *captureQueue) take() []float32 {
	q.mu.Lock()
	defer q.mu.Unlock()
	samples := q.samples
	q.samples = nil
	return samples
}

func (q *captureQueue) fail(err error) {
	select {
	case q.failures <- err:
	default:
	}
}

func (q *captureQueue) consume(ctx context.Context, consume func([]float32) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case err := <-q.failures:
			return err
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-q.failures:
			return err
		case <-q.ready:
			samples := q.take()
			if len(samples) == 0 {
				continue
			}
			if err := consume(samples); err != nil {
				return err
			}
		}
	}
}
