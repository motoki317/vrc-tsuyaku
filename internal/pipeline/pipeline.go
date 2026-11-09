// Package pipeline runs capture, recognition, translation and output in phrase order.
package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/vad"
)

// Event reports a pipeline result. The app also uses this type for status updates.
// PhraseID is unique across Run calls and links the recognized, translated,
// page and sent events for one phrase.
// Original accompanies a translation. A sent event means Send returned nil.
type Event struct {
	PhraseID   uint64       `json:"phraseId,omitempty"`
	Kind       string       `json:"kind"`
	Code       errcode.Code `json:"code,omitempty"`
	Text       string       `json:"text"`
	Original   string       `json:"original,omitempty"`
	SourceLang string       `json:"sourceLang,omitempty"`
	TargetLang string       `json:"targetLang,omitempty"`
}

var nextPhraseID atomic.Uint64

// PhraseSettings is captured before recognition and stays fixed through sending.
type PhraseSettings struct {
	SourceLang string
	TargetLang string
	Template   string
}

// Stages supplies the operations for one Run.
// Send is optional and calls pageSent after each page it successfully writes.
// Run emits a page event for each callback and a sent event after Send returns nil.
// Emit must be safe for concurrent calls and must not block the pipeline.
type Stages struct {
	Source      audio.Source
	Probability func(context.Context, []float32) (float32, error)
	Recognize   func(context.Context, []float32) (string, error)
	Settings    func() PhraseSettings
	Translate   func(ctx context.Context, source, target, phrase string) (string, error)
	Send        func(ctx context.Context, format string, message chatbox.Message, pageSent func(string)) error
	Emit        func(Event)
}

// Run preserves phrase order through unbuffered stages. A slow stage applies
// backpressure to capture. A template error skips that phrase's send and emits an
// error event. Other errors cancel every stage and Run waits for them.
// Cancellation discards capture's queued audio, the partial VAD window and the
// segmenter's pending phrase. A finite test source can return nil to end input.
// Run then pads the partial VAD window with zeros and flushes the final phrase.
func Run(ctx context.Context, s Stages) error {
	if s.Source == nil || s.Probability == nil || s.Recognize == nil || s.Settings == nil || s.Translate == nil {
		return errors.New("pipeline requires a source, VAD, recognizer, settings and translator")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	phrases := make(chan []float32)
	type result struct {
		id                    uint64
		original, translation string
		source, target        string
		template              string
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
		speech := func(text string) { emit(Event{Kind: "speech", Text: text}) }
		defer func() {
			if segmenter.Active() {
				speech("end")
			}
		}()
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
			active := segmenter.Active()
			phrase, err := segmenter.Push(window[:], p)
			if err != nil {
				return err
			}
			if !active && segmenter.Active() {
				speech("start")
			}
			if active && (!segmenter.Active() || len(phrase) > 0) {
				speech("end")
			}
			if len(phrase) > 0 && segmenter.Active() {
				speech("start")
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
		if segmenter.Active() {
			speech("end")
		}
		return publish(segmenter.Flush())
	})
	start(func() error {
		defer close(texts)
		for phrase := range phrases {
			if err := ctx.Err(); err != nil {
				return err
			}
			settings := s.Settings()
			source, target := settings.SourceLang, settings.TargetLang
			text, err := s.Recognize(ctx, phrase)
			if err != nil {
				return err
			}
			if text == "" {
				continue
			}
			id := nextPhraseID.Add(1)
			emit(Event{PhraseID: id, Kind: "recognized", Text: text, SourceLang: source, TargetLang: target})
			select {
			case texts <- result{id: id, original: text, source: source, target: target, template: settings.Template}:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		return nil
	})
	start(func() error {
		defer close(translations)
		for r := range texts {
			translated, err := s.Translate(ctx, catalog.TranslatorSource(r.source), r.target, r.original)
			if err != nil {
				return err
			}
			r.translation = translated
			emit(Event{PhraseID: r.id, Kind: "translated", Original: r.original, Text: translated, SourceLang: r.source, TargetLang: r.target})
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
				message := chatbox.Message{Original: r.original, Translation: r.translation, SourceLang: r.source, TargetLang: r.target}
				if err := s.Send(ctx, r.template, message, func(page string) {
					emit(Event{PhraseID: r.id, Kind: "page", Text: page, SourceLang: r.source, TargetLang: r.target})
				}); err != nil {
					if ctx.Err() != nil {
						return context.Cause(ctx)
					}
					emit(Event{PhraseID: r.id, Kind: "error", Code: errcode.Of(err), Text: err.Error(), SourceLang: r.source, TargetLang: r.target})
					if errors.Is(err, chatbox.ErrTemplate) {
						continue
					}
					return err
				}
				emit(Event{PhraseID: r.id, Kind: "sent", SourceLang: r.source, TargetLang: r.target})
			}
		}
		return nil
	})
	wg.Wait()
	return context.Cause(ctx)
}
