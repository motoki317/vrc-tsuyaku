// Reimplements NeMo's GPU-accelerated phrase-boosting tree (GPU-PB) for greedy
// transducer decoding (Apache-2.0), https://github.com/NVIDIA-NeMo/Speech.
// Paper: https://arxiv.org/abs/2508.07014. No upstream code is copied.
// Rollback of unfinished hints is VRC Tsuyaku's own addition.
// See ../../THIRD_PARTY_NOTICES.md.

package asr

import (
	"context"
	"fmt"
	"slices"
)

const maxHintRollbacks = 8

type checkpointDecoder interface {
	tdtDecoder
	// The restore function consumes its checkpoint and can run only once.
	checkpoint() (restore func() error, err error)
}

type decodeStep struct{ frame, tokens int }
type hintCheckpoint struct {
	step               decodeStep
	lastFrame, symbols int
	logProbSum         float64
	scoredTokens       int
	live               []hintMatch
	restore            func() error
}

// greedyTDT keeps decoding without hints in a separate loop because the parity
// tests pin that loop's statements.
func speculativeTDT(ctx context.Context, frames int, cfg tdtConfig, decoder tdtDecoder) ([]int, float64, error) {
	d, ok := decoder.(checkpointDecoder)
	if !ok {
		return nil, 0, fmt.Errorf("decoder cannot checkpoint hint state")
	}
	hints := newHintState(cfg.hints, cfg.blank)
	var tokens []int
	var logProbSum float64
	var scoredTokens int
	var checkpoint *hintCheckpoint
	unbiasedNext := false
	frame, lastFrame, symbols, rollbacks := 0, -1, 0, 0
	rollback := func() error {
		if err := checkpoint.restore(); err != nil {
			return fmt.Errorf("restore decoder at frame %d: %w", checkpoint.step.frame, err)
		}
		unbiasedNext = true
		frame, lastFrame, symbols = checkpoint.step.frame, checkpoint.lastFrame, checkpoint.symbols
		tokens = tokens[:checkpoint.step.tokens]
		logProbSum, scoredTokens = checkpoint.logProbSum, checkpoint.scoredTokens
		hints.live = append(hints.live[:0], checkpoint.live...)
		checkpoint = nil
		rollbacks++
		if rollbacks >= maxHintRollbacks {
			hints = nil
		}
		return nil
	}
	for frame < frames || checkpoint != nil {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if frame >= frames {
			if err := rollback(); err != nil {
				return nil, 0, err
			}
			continue
		}
		logits, err := d.Scores(frame)
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
		unbiasedToken := token
		if hints != nil && !unbiasedNext && (token != cfg.blank || len(hints.live) > 0) {
			var biased int
			if token == cfg.blank {
				biased = hints.continueOverBlank(logits[:cfg.blank+1], cfg.blank)
			} else {
				biased = hints.choose(logits[:cfg.blank], token)
			}
			if biased != token && checkpoint == nil {
				restore, err := d.checkpoint()
				if err != nil {
					return nil, 0, fmt.Errorf("checkpoint decoder at frame %d: %w", frame, err)
				}
				checkpoint = &hintCheckpoint{
					step: decodeStep{frame, len(tokens)}, lastFrame: lastFrame, symbols: symbols,
					logProbSum: logProbSum, scoredTokens: scoredTokens, live: slices.Clone(hints.live), restore: restore,
				}
			}
			token = biased
		}
		unbiasedNext = false
		labelFrame := frame
		frame += duration
		if token == cfg.blank {
			continue
		}
		if hints != nil {
			completed, oldest := hints.accept(token, len(tokens))
			if checkpoint != nil {
				if completed >= 0 && completed <= checkpoint.step.tokens {
					checkpoint = nil
				} else if oldest > checkpoint.step.tokens {
					if err := rollback(); err != nil {
						return nil, 0, err
					}
					continue
				}
			}
		}
		tokens = append(tokens, token)
		if token == unbiasedToken {
			logProbSum += tokenLogProb(logits[:cfg.blank+1], token)
			scoredTokens++
		}
		if labelFrame == lastFrame {
			symbols++
		} else {
			lastFrame, symbols = labelFrame, 1
		}
		if err := d.Accept(token); err != nil {
			return nil, 0, fmt.Errorf("accept token %d at frame %d: %w", token, labelFrame, err)
		}
		if frame < frames && symbols >= cfg.maxSymbols && lastFrame == frame {
			frame++
		}
	}
	return tokens, meanLogProb(logProbSum, scoredTokens), nil
}
