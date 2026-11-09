// Reimplements NeMo 2.7.3 greedy Token-and-Duration Transducer (TDT) decoding
// (Apache-2.0),
// https://github.com/NVIDIA/NeMo.
// No upstream source code is copied.
// See ../../THIRD_PARTY_NOTICES.md.

package asr

import (
	"context"
	"fmt"
	"math"
)

// A decoder owns one phrase's prediction state. Scores computes candidate token
// and duration scores. Only Accept commits a token to that state.
// The Scores slice remains valid until the next Scores or Accept call.
// greedyTDT calls Accept to commit every nonblank token.
type tdtDecoder interface {
	Scores(frame int) ([]float32, error)
	Accept(token int) error
}

type tdtConfig struct {
	blank      int
	durations  []int
	maxSymbols int
}

func greedyTDT(ctx context.Context, frames int, cfg tdtConfig, decoder tdtDecoder) ([]int, error) {
	var tokens []int
	lastFrame, symbols := -1, 0
	for frame := 0; frame < frames; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		logits, err := decoder.Scores(frame)
		if err != nil {
			return nil, fmt.Errorf("score frame %d: %w", frame, err)
		}
		if len(logits) != cfg.blank+1+len(cfg.durations) {
			return nil, fmt.Errorf("frame %d: unexpected logits length %d", frame, len(logits))
		}
		token, err := argmax(logits[:cfg.blank+1])
		if err != nil {
			return nil, fmt.Errorf("frame %d tokens: %w", frame, err)
		}
		durationID, err := argmax(logits[cfg.blank+1:])
		if err != nil {
			return nil, fmt.Errorf("frame %d durations: %w", frame, err)
		}
		duration := cfg.durations[durationID]
		if token == cfg.blank && duration == 0 {
			duration = 1
		}
		labelFrame := frame
		frame += duration
		if token == cfg.blank {
			continue
		}
		tokens = append(tokens, token)
		if labelFrame == lastFrame {
			symbols++
		} else {
			lastFrame, symbols = labelFrame, 1
		}
		if err := decoder.Accept(token); err != nil {
			return nil, fmt.Errorf("accept token %d at frame %d: %w", token, labelFrame, err)
		}
		if frame < frames && symbols >= cfg.maxSymbols && lastFrame == frame {
			frame++
		}
	}
	return tokens, nil
}

func argmax(values []float32) (int, error) {
	best := 0
	for i, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return 0, fmt.Errorf("non-finite logit at index %d", i)
		}
		if value > values[best] {
			best = i
		}
	}
	return best, nil
}
