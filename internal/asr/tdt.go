// Reimplements NeMo 2.7.3 greedy Token-and-Duration Transducer (TDT) decoding
// (Apache-2.0),
// https://github.com/NVIDIA/NeMo.
// Judging a phrase by the mean log-probability of its tokens against a
// threshold follows avg_logprob and logprob_threshold of OpenAI Whisper (MIT),
// https://github.com/openai/whisper/blob/86098128c0b4f24f0e2aa2994de830614b474227/whisper/transcribe.py.
// No source code of either project is copied.
// See ../../THIRD_PARTY_NOTICES.md.

package asr

import (
	"context"
	"fmt"
	"math"
)

// decodeConfident returns no text for a phrase whose confidence is under
// threshold, because the models also write text, with low confidence, for
// sound that is not words.
func (v vocabulary) decodeConfident(tokens []int, confidence, threshold float64) (string, error) {
	if confidence < threshold {
		return "", nil
	}
	return v.decode(tokens)
}

// A decoder owns one phrase's prediction state. Scores computes candidate token
// and duration scores. Accept commits a token. A checkpoint restore replaces the state.
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
	hints      *compiledHints
}

func greedyTDT(ctx context.Context, frames int, cfg tdtConfig, decoder tdtDecoder) ([]int, float64, error) {
	if cfg.hints != nil {
		return speculativeTDT(ctx, frames, cfg, decoder)
	}
	var tokens []int
	var logProbSum float64
	lastFrame, symbols := -1, 0
	for frame := 0; frame < frames; {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		logits, err := decoder.Scores(frame)
		if err != nil {
			return nil, 0, fmt.Errorf("score frame %d: %w", frame, err)
		}
		if len(logits) != cfg.blank+1+len(cfg.durations) {
			return nil, 0, fmt.Errorf("frame %d: unexpected logits length %d", frame, len(logits))
		}
		token, err := argmax(logits[:cfg.blank+1])
		if err != nil {
			return nil, 0, fmt.Errorf("frame %d tokens: %w", frame, err)
		}
		durationID, err := argmax(logits[cfg.blank+1:])
		if err != nil {
			return nil, 0, fmt.Errorf("frame %d durations: %w", frame, err)
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
		logProbSum += tokenLogProb(logits[:cfg.blank+1], token)
		if labelFrame == lastFrame {
			symbols++
		} else {
			lastFrame, symbols = labelFrame, 1
		}
		if err := decoder.Accept(token); err != nil {
			return nil, 0, fmt.Errorf("accept token %d at frame %d: %w", token, labelFrame, err)
		}
		if frame < frames && symbols >= cfg.maxSymbols && lastFrame == frame {
			frame++
		}
	}
	return tokens, meanLogProb(logProbSum, len(tokens)), nil
}

func tokenLogProb(logits []float32, best int) float64 {
	// Shift by the winning score to keep exp finite.
	maxLogit := float64(logits[best])
	var sum float64
	for _, logit := range logits {
		sum += math.Exp(float64(logit) - maxLogit)
	}
	return -math.Log(sum)
}

func meanLogProb(sum float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return sum / float64(count)
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
