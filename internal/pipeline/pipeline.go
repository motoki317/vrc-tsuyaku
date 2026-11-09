// Package pipeline runs capture, recognition, translation and output in phrase order.
package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/vad"
)

// Event reports a pipeline result. The app also uses this type for status updates.
// PhraseID is unique across Run calls and links the recognized, translated,
// page and sent events for one phrase.
// Original accompanies a translation. A sent event means Send returned nil.
type Event struct {
	PhraseID uint64 `json:"phraseId,omitempty"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Original string `json:"original,omitempty"`
}

var nextPhraseID atomic.Uint64

// Stages supplies the operations for one Run.
// Send is optional and calls pageSent after each page it successfully writes.
// Run emits a page event for each callback and a sent event after Send returns nil.
// Emit must be safe for concurrent calls and must not block the pipeline.
type Stages struct {
	Source      audio.Source
	Probability func(context.Context, []float32) (float32, error)
	Recognize   func(context.Context, []float32) (string, error)
	Translate   func(context.Context, string) (string, error)
	Send        func(ctx context.Context, original, translation string, pageSent func(string)) error
	Emit        func(Event)
}

// Run preserves phrase order through unbuffered stages. A slow stage applies
// backpressure to capture. An error cancels every stage and Run waits for them.
// Cancellation discards capture's queued audio, the partial VAD window and the
// segmenter's pending phrase. A finite test source can return nil to end input.
// Run then pads the partial VAD window with zeros and flushes the final phrase.
func Run(ctx context.Context, s Stages) error {
	if s.Source == nil || s.Probability == nil || s.Recognize == nil || s.Translate == nil {
		return errors.New("pipeline requires a source, VAD, recognizer and translator")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	phrases := make(chan []float32)
	type result struct {
		id                    uint64
		original, translation string
	}
	texts := make(chan result)
	translations := make(chan result)
	emit := func(e Event) {
		if s.Emit != nil {
			s.Emit(e)
		}
	}
	var wg sync.WaitGroup
	start := func(run func() error) {
		wg.Go(func() {
			if err := run(); err != nil {
				cancel(err)
			}
		})
	}
	start(func() error {
		defer close(phrases)
		var segmenter vad.Segmenter
		var window [vad.WindowSamples]float32
		n := 0
		publish := func(phrase []float32) error {
			if len(phrase) == 0 {
				return nil
			}
			select {
			case phrases <- phrase:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		score := func() error {
			p, err := s.Probability(ctx, window[:])
			if err != nil {
				return err
			}
			phrase, err := segmenter.Push(window[:], p)
			if err != nil {
				return err
			}
			return publish(phrase)
		}
		err := s.Source.Capture(ctx, func(chunk []float32) error {
			for len(chunk) > 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
				count := copy(window[n:], chunk)
				n += count
				chunk = chunk[count:]
				if n == len(window) {
					if err := score(); err != nil {
						return err
					}
					n = 0
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if n > 0 {
			clear(window[n:])
			if err := score(); err != nil {
				return err
			}
		}
		return publish(segmenter.Flush())
	})
	start(func() error {
		defer close(texts)
		for phrase := range phrases {
			if err := ctx.Err(); err != nil {
				return err
			}
			text, err := s.Recognize(ctx, phrase)
			if err != nil {
				return err
			}
			if text == "" {
				continue
			}
			id := nextPhraseID.Add(1)
			emit(Event{PhraseID: id, Kind: "recognized", Text: text})
			select {
			case texts <- result{id: id, original: text}:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		return nil
	})
	start(func() error {
		defer close(translations)
		for r := range texts {
			translated, err := s.Translate(ctx, r.original)
			if err != nil {
				return err
			}
			r.translation = translated
			emit(Event{PhraseID: r.id, Kind: "translated", Original: r.original, Text: translated})
			select {
			case translations <- r:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		return nil
	})
	start(func() error {
		for r := range translations {
			if s.Send != nil {
				if err := s.Send(ctx, r.original, r.translation, func(page string) { emit(Event{PhraseID: r.id, Kind: "page", Text: page}) }); err != nil {
					return err
				}
				emit(Event{PhraseID: r.id, Kind: "sent"})
			}
		}
		return nil
	})
	wg.Wait()
	return context.Cause(ctx)
}
