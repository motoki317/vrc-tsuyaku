// Uses probability hysteresis and silence confirmation from silero-vad v6.2.3
// (MIT), src/silero_vad/utils_vad.py, with its default thresholds of 0.5 and 0.35.
// The app sets 300 ms pre-roll, 500 ms end silence, a 250 ms minimum and a 15 s cut.
// https://github.com/snakers4/silero-vad/tree/5cd7945676eb32225748052e2e6a0580e4686a08
// No upstream source code is copied. See ../../THIRD_PARTY_NOTICES.md.

// Package vad groups 16 kHz mono audio into phrases using speech probabilities.
package vad

import (
	"fmt"
	"math"
	"slices"
)

// WindowSamples is the number of samples scored by one Silero inference call.
const WindowSamples = 512

// StartProbability is the probability at which a phrase starts.
// It is exported so that pipeline's gain learning counts the same windows as speech.
const StartProbability = 0.5

const (
	endProbability = 0.35
	preRollSamples = 16000 * 300 / 1000
	endSamples     = 16000 * 500 / 1000
	minimumSamples = 16000 * 250 / 1000
	maximumSamples = 16000 * 15
)

// Segmenter starts a phrase at probability >= 0.5, with up to 300 ms of pre-roll.
// A window below 0.35 starts the end silence. Later windows below 0.5 extend it,
// and a window at 0.5 or above cancels it. After 500 ms of end silence, the phrase
// ends and retains that audio. Phrases under 250 ms are dropped.
// Pre-roll and end silence do not count toward that minimum.
//
// At 15 seconds of audio, including pre-roll, it emits a phrase and continues
// from the following window without pre-roll. The cut preserves the active
// speech flag and the end-silence timer.
// End and cut checks use whole windows, so each can round up by less than 32 ms.
//
// The zero value is ready to use. Call methods sequentially for one audio stream.
type Segmenter struct {
	history, phrase []float32
	preRoll         int
	silence         int
	active          bool
}

// Active includes the silence confirmation and continues across a forced cut.
func (s *Segmenter) Active() bool { return s.active }

// Push consumes one window and its probability, returning a completed phrase or
// nil. It rejects a window of the wrong size, non-finite audio, or a probability
// outside [0,1], without changing state. The segmenter copies input audio,
// and the caller owns the returned slice.
func (s *Segmenter) Push(audio []float32, probability float32) ([]float32, error) {
	if len(audio) != WindowSamples {
		return nil, fmt.Errorf("VAD requires %d samples, got %d", WindowSamples, len(audio))
	}
	if math.IsNaN(float64(probability)) || probability < 0 || probability > 1 {
		return nil, fmt.Errorf("invalid speech probability %g", probability)
	}
	for i, sample := range audio {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			return nil, fmt.Errorf("audio sample %d is not finite", i)
		}
	}
	if !s.active && probability >= StartProbability {
		s.active = true
		s.phrase = slices.Clone(s.history)
		s.preRoll = len(s.history)
	}
	s.history = append(s.history, audio...)
	if len(s.history) > preRollSamples {
		copy(s.history, s.history[len(s.history)-preRollSamples:])
		s.history = s.history[:preRollSamples]
	}
	if !s.active {
		return nil, nil
	}
	s.phrase = append(s.phrase, audio...)
	if probability >= StartProbability {
		s.silence = 0
	} else if probability < endProbability || s.silence > 0 {
		s.silence += WindowSamples
	}
	if s.silence >= endSamples {
		phrase := s.finish()
		s.active = false
		s.silence = 0
		return phrase, nil
	}
	if len(s.phrase) >= maximumSamples {
		return s.finish(), nil
	}
	return nil, nil
}

// Flush returns the pending phrase without waiting for the end silence, if it
// meets the minimum duration. It then resets the stream.
func (s *Segmenter) Flush() []float32 {
	phrase := s.finish()
	s.Reset()
	return phrase
}

// Reset discards the pending phrase and pre-roll, for example when capture restarts.
func (s *Segmenter) Reset() {
	*s = Segmenter{}
}

func (s *Segmenter) finish() []float32 {
	phrase := s.phrase
	// The end silence can start before a cut and fill the entire continuation.
	if max(0, len(phrase)-s.preRoll-s.silence) < minimumSamples {
		phrase = nil
	}
	s.phrase = nil
	s.preRoll = 0
	return phrase
}
