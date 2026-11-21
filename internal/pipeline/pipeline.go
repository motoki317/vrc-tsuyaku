// Package pipeline runs capture, recognition, translation and output in phrase order.
package pipeline

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
	"github.com/motoki317/vrc-tsuyaku/internal/vad"
)

// Kind identifies an event's payload in the event stream.
type Kind string

const (
	// KindSpeech carries whether speech started or ended.
	KindSpeech Kind = "speech"
	// KindRecognized carries the recognized phrase after the glossary rewrites it.
	KindRecognized Kind = "recognized"
	// KindSkipped carries empty text for a phrase the waiting queue dropped.
	KindSkipped Kind = "skipped"
	// KindTranslated carries the translation of the recognized phrase.
	KindTranslated Kind = "translated"
	// KindPage carries one chatbox page written to VRChat, including earlier phrases if they joined.
	KindPage Kind = "page"
	// KindMerged carries the joined translations of the message's phrases.
	KindMerged Kind = "merged"
	// KindSent carries empty text once the flow writes all chatbox pages for the phrase.
	KindSent Kind = "sent"
	// KindError carries the diagnostic text.
	KindError Kind = "error"
)

type speechText string

const (
	// speechStart means the flow hears speech begin.
	speechStart speechText = "start"
	// speechEnd means the flow hears speech end.
	speechEnd speechText = "end"
)

// Event reports a pipeline result. The app also uses this type for status updates.
// PhraseID is unique across Run calls and links the recognized, translated,
// page, merged and sent events for one phrase.
// A skipped event carries its own phrase ID and empty text. It has no languages
// because Run drops the phrase before it snapshots settings.
// A message is one or more phrases that Run's last stage shows together.
// Into names the message's first phrase and stays fixed over a chain of merges.
// A merged event carries the joined originals and translations in Original and Text.
// A page event carries the written text under its phrase's ID. When Run's last
// stage merges the phrase into the page on screen, the text also holds earlier phrases.
type Event struct {
	PhraseID   uint64       `json:"phraseId,omitempty"`
	Into       uint64       `json:"into,omitempty"`
	Kind       Kind         `json:"kind"`
	Code       errcode.Code `json:"code,omitempty"`
	Text       string       `json:"text"`
	Original   string       `json:"original,omitempty"`
	SourceLang string       `json:"sourceLang,omitempty"`
	TargetLang string       `json:"targetLang,omitempty"`
}

var nextPhraseID atomic.Uint64

// Run snapshots PhraseSettings before recognition and uses it through sending.
// Template renders the chatbox pages. Without Send it only measures whether phrases fit in one page together.
type PhraseSettings struct {
	SourceLang  string
	TargetLang  string
	Template    string
	ReadingTime chatbox.ReadingTime
	Terms       []glossary.Term
}

// Stages supplies the operations for one Run.
// Send is optional and writes one rendered page. Run emits a page event after
// each successful write and a sent event after all of a phrase's pages succeed.
// Without Send the flow has no chatbox, and the web UI keeps every message:
// Run waits for nothing and emits no page or sent events, but still joins
// phrases into messages and emits merged.
// Emit must be safe for concurrent calls and must not block the pipeline.
// The capture callback emits speech events.
// CutLevel is optional and reads the cut level in dBFS once per window.
// A nil CutLevel, or a return value of 0, selects the automatic gain.
// Meter is optional and must not block. Run calls it from one goroutine in capture order.
// It receives each window's level before the gain. Digital silence is -240 dBFS.
// Close is optional. The caller runs it after all stage calls finish.
// Recognize receives the flow's terms in PhraseSettings.Terms.
// Translate receives the terms used by Rewrite and the translator's source language.
type Stages struct {
	Close       func()
	Source      audio.Source
	CutLevel    func() float64
	Meter       func(level float64)
	Probability func(context.Context, []float32) (float32, error)
	Recognize   func(context.Context, []float32, PhraseSettings) (string, error)
	Settings    func() PhraseSettings
	Translate   func(context.Context, string, PhraseSettings) (string, error)
	Send        func(ctx context.Context, page string) error
	Emit        func(Event)
}

// Run recognizes waiting phrases in order. Up to 64 translated phrases can wait
// for Send so chatbox reading time does not delay recognition or translation.
// A full send buffer stalls translation and recognition. Capture keeps running
// and drops the oldest waiting phrase when the waiting queue fills.
// The recognition stage emits skipped events in phrase order before recognizing
// the next kept phrase, which starts a new message.
// With Send, a template error skips that phrase's send and emits an error event.
// Without Send, a template error emits no event, and no later phrase joins a message from before that phrase.
// Reading time and the message that a phrase can join last for this Run only.
// For other errors, Run cancels every stage and waits for all stages to end.
// Cancellation discards capture's queued audio, the partial VAD window and the
// segmenter's pending phrase, along with phrases waiting for recognition or Send
// and any skipped events the recognition stage has yet to reach.
// A finite test source can return nil to end input.
// Run drains waiting phrases and recognizes the final phrase after padding any
// partial VAD window with zeros.
func Run(ctx context.Context, s Stages) error {
	if s.Source == nil || s.Probability == nil || s.Recognize == nil || s.Settings == nil || s.Translate == nil {
		return errors.New("pipeline requires a source, VAD, recognizer, settings and translator")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	phrases := &phraseQueue{ready: make(chan struct{}, 1)}
	texts := make(chan result)
	translations := make(chan result, 64)
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
		defer close(phrases.ready)
		return segment(ctx, s, phrases, emit)
	})
	start(func() error { return recognize(ctx, s, phrases, texts, emit) })
	start(func() error { return translate(ctx, s, texts, translations, emit) })
	start(func() error { return show(ctx, s, translations, emit) })
	wg.Wait()
	return context.Cause(ctx)
}

type result struct {
	id                    uint64
	original, translation string
	settings              PhraseSettings
	queued                time.Time
	followsSkip           bool
}

// Keep the queue short because each waiting phrase delays the newest subtitle
// by one more translation during overload.
const maxWaitingPhrases = 2

type phraseQueue struct {
	mu      sync.Mutex
	waiting [][]float32
	skipped int
	ready   chan struct{}
}

func (q *phraseQueue) push(phrase []float32, dropOldest bool) {
	q.mu.Lock()
	if dropOldest && len(q.waiting) == maxWaitingPhrases {
		q.waiting = slices.Delete(q.waiting, 0, 1)
		q.skipped++
	}
	q.waiting = append(q.waiting, phrase)
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

func (q *phraseQueue) take(ctx context.Context) ([]float32, int) {
	for {
		// Taking a phrase also claims the skips that precede it.
		q.mu.Lock()
		if len(q.waiting) > 0 {
			phrase := q.waiting[0]
			q.waiting = slices.Delete(q.waiting, 0, 1)
			skipped := q.skipped
			q.skipped = 0
			q.mu.Unlock()
			return phrase, skipped
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, 0
		case _, ok := <-q.ready:
			if !ok {
				return nil, 0
			}
		}
	}
}

func segment(ctx context.Context, s Stages, phrases *phraseQueue, emit func(Event)) error {
	var segmenter vad.Segmenter
	level := leveler{searching: true}
	speech := func(text speechText) { emit(Event{Kind: KindSpeech, Text: string(text)}) }
	defer func() {
		if segmenter.Active() {
			speech(speechEnd)
		}
	}()
	var window, scored [vad.WindowSamples]float32
	n := 0
	inputEnded := false
	publish := func(phrase []float32) {
		if len(phrase) == 0 {
			return
		}
		amplifyPhrase(phrase)
		phrases.push(phrase, !inputEnded)
	}
	score := func() error {
		var cutLevel float64
		if s.CutLevel != nil {
			cutLevel = s.CutLevel()
		}
		power, db, open := level.process(window[:], scored[:], cutLevel)
		if s.Meter != nil {
			s.Meter(db)
		}
		p, err := s.Probability(ctx, scored[:])
		if err != nil {
			return err
		}
		if !open {
			p = 0
		}
		active := segmenter.Active()
		phrase, err := segmenter.Push(window[:], p)
		if err != nil {
			return err
		}
		if segmenter.Active() && p >= vad.StartProbability {
			level.heard(power)
		}
		if !active && segmenter.Active() {
			speech(speechStart)
		}
		if active && (!segmenter.Active() || len(phrase) > 0) {
			level.ended()
			speech(speechEnd)
		}
		if len(phrase) > 0 && segmenter.Active() {
			speech(speechStart)
		}
		level.after(segmenter.Active(), p)
		publish(phrase)
		return nil
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
	// Keep the final audio because no more capture callbacks can add to the backlog.
	inputEnded = true
	if n > 0 {
		clear(window[n:])
		if err := score(); err != nil {
			return err
		}
	}
	if segmenter.Active() {
		speech(speechEnd)
	}
	publish(segmenter.Flush())
	return nil
}

// recognize reads audio phrases and snapshots settings for each phrase.
// For transcripts with a letter or digit, it emits a recognized event and sends the rewritten transcript with its settings.
// It closes texts when input ends or the stage returns an error.
func recognize(ctx context.Context, s Stages, phrases *phraseQueue, texts chan<- result, emit func(Event)) error {
	defer close(texts)
	followsSkip := false
	for {
		phrase, skipped := phrases.take(ctx)
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		if phrase == nil {
			return nil
		}
		for range skipped {
			emit(Event{Kind: KindSkipped, PhraseID: nextPhraseID.Add(1)})
		}
		followsSkip = followsSkip || skipped > 0
		settings := s.Settings()
		source, target := settings.SourceLang, settings.TargetLang
		text, err := s.Recognize(ctx, phrase, settings)
		if err != nil {
			return err
		}
		// A recognizer writes text for sound that is not speech, such as "――" for a breath.
		// Text without a letter or digit holds nothing to translate.
		if !strings.ContainsFunc(text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			continue
		}
		text, settings.Terms = glossary.Rewrite(text, settings.Terms)
		id := nextPhraseID.Add(1)
		emit(Event{PhraseID: id, Kind: KindRecognized, Text: text, SourceLang: source, TargetLang: target})
		select {
		case texts <- result{id: id, original: text, settings: settings, followsSkip: followsSkip}:
			followsSkip = false
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// translate reads recognized phrases and emits a translated event before it queues each result.
// It closes translations when input ends or the stage returns an error.
func translate(ctx context.Context, s Stages, texts <-chan result, translations chan<- result, emit func(Event)) error {
	defer close(translations)
	for r := range texts {
		settings := r.settings
		settings.SourceLang = catalog.TranslatorSource(settings.SourceLang)
		translated, err := s.Translate(ctx, r.original, settings)
		if err != nil {
			return err
		}
		r.translation = translated
		emit(Event{PhraseID: r.id, Kind: KindTranslated, Original: r.original, Text: translated, SourceLang: r.settings.SourceLang, TargetLang: r.settings.TargetLang})
		r.queued = time.Now()
		select {
		case translations <- r:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return nil
}

// show reads translated phrases and emits merged events when phrases join a message.
// With Send, it writes chatbox pages and emits page, sent and error events.
// It ends when input closes, cancellation takes effect or a display error other than a template error occurs.
func show(ctx context.Context, s Stages, translations <-chan result, emit func(Event)) error {
	display := display{send: s.Send, emit: emit}
	for r := range translations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.followsSkip {
			display.breakJoin()
		}
		message := chatbox.Message{Original: r.original, Translation: r.translation, SourceLang: r.settings.SourceLang, TargetLang: r.settings.TargetLang}
		if err := display.show(ctx, r.id, r.settings.Template, r.settings.ReadingTime, message, r.queued); err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			emit(Event{PhraseID: r.id, Kind: KindError, Code: errcode.Of(err), Text: err.Error(), SourceLang: r.settings.SourceLang, TargetLang: r.settings.TargetLang})
			if errors.Is(err, chatbox.ErrTemplate) {
				continue
			}
			return err
		}
		if s.Send != nil {
			emit(Event{PhraseID: r.id, Kind: KindSent, SourceLang: r.settings.SourceLang, TargetLang: r.settings.TargetLang})
		}
	}
	return nil
}
