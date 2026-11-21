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

// Match NeMo's TDT cap so zero-duration predictions cannot hold a frame indefinitely.
const tdtMaxSymbolsPerFrame = 10

// Models can emit low-confidence text for noise. Spoken fillers still count as
// speech, so apply the confidence cutoff without inspecting the transcript.
//
// The cutoff sweep ran int8 Haas on Japanese audio and int8 Parakeet v2 and v3
// on English audio, all on CPU. It used the pipeline's amplifyPhrase gain.
// Speech sets were audiobook clips cut at pauses, 60 synthetic chat sentences,
// and synthetic sentences with avatar names (120 Japanese, 60 English). The
// sweep ran the name sentences with and without the names as hints. The
// synthetic sentences used clean audio, white noise 15 dB below speech
// and an Opus round trip. Synthesized short answers used the clean and
// white-noise conditions. The sweep also included synthesized Japanese fillers.
// Other inputs were noise, hum, tones, buzz, clicks, bursts and audiobook clips
// low-passed at 350 Hz as a stand-in for humming.
//
// Each count uses transcripts below the cutoff out of all nonempty transcripts.
// The short-answer counts use only correct transcripts. Parakeet v2 still
// heard words in the low-passed clips, so those clips did not test v2 on humming.
// The sweep did not cover casual headset speech, human murmurs or v3 outside English.
func (v vocabulary) decodeConfident(tokens []int, confidence, threshold float64) (string, error) {
	if confidence < threshold {
		return "", nil
	}
	return v.decode(tokens)
}

// A decoder owns one phrase's prediction state. Scores computes candidate token
// and duration scores. Accept commits a token. A checkpoint restore replaces the state.
// The Scores slice remains valid until the next Scores or Accept call.
// While decoding, call Accept only for emitted nonblank tokens.
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
	// Subtract the winning score before exponentiating to keep the sum finite.
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
