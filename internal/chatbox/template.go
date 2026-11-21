package chatbox

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

// ErrTemplate distinguishes a phrase's formatting failure from a failed UDP send.
var ErrTemplate = errcode.With(errcode.Template, errors.New("chatbox template"))

const (
	MinReadingBaseSeconds     = 1.0
	MaxReadingBaseSeconds     = 10.0
	DefaultReadingBaseSeconds = 2.0
	MinReadingFactor          = 1.0
	MaxReadingFactor          = 8.0
	DefaultReadingFactor      = 2.0
)

// ReadingTime gives viewers a fixed time to notice the chatbox. The factor
// adds a margin over the time it takes to read the message aloud.
type ReadingTime struct {
	Base   time.Duration
	Factor float64
}

func DefaultReadingTime() ReadingTime {
	return NewReadingTime(DefaultReadingBaseSeconds, DefaultReadingFactor)
}

func NewReadingTime(baseSeconds, factor float64) ReadingTime {
	return ReadingTime{Base: time.Duration(baseSeconds * float64(time.Second)), Factor: factor}
}

func (r ReadingTime) duration(aloud time.Duration) time.Duration {
	return r.Base + time.Duration(r.Factor*float64(aloud))
}

func (r ReadingTime) Duration(m Message) time.Duration {
	return r.duration(SlowerReading(m).duration())
}

// PageDurations divides the reading time among the rendered pages by rune count.
// Each page gets the full base time so the viewer has time to notice it.
func (r ReadingTime) PageDurations(m Message, pages []string) []time.Duration {
	times := make([]time.Duration, len(pages))
	if len(pages) == 0 {
		return times
	}
	total := r.Duration(m)
	if len(pages) == 1 {
		times[0] = total
		return times
	}
	var runes int
	for _, page := range pages {
		runes += utf8.RuneCountInString(page)
	}
	remaining := total - r.Base
	for i, page := range pages {
		n := utf8.RuneCountInString(page)
		share := time.Duration(float64(remaining) * (float64(n) / float64(runes)))
		times[i] = r.Base + share
		remaining -= share
		runes -= n
	}
	return times
}

// Six seconds of speech is about 35 Japanese characters.
const backlogReadAloudTime = 6 * time.Second

// BacklogBound limits how long a waiting page can fall behind its translation.
func (r ReadingTime) BacklogBound() time.Duration {
	return r.duration(backlogReadAloudTime)
}

type Message struct {
	Original    string `json:"original"`
	Translation string `json:"translation"`
	SourceLang  string `json:"sourceLang"`
	TargetLang  string `json:"targetLang"`
}

// Reading keeps the count and rate together so the preview can show the calculation.
type Reading struct {
	Characters   int
	PerCharacter time.Duration
}

func (r Reading) duration() time.Duration {
	return time.Duration(r.Characters) * r.PerCharacter
}

// SlowerReading uses the side that takes longer to read aloud, even when the
// template hides that side. The original wins a tie.
func SlowerReading(m Message) Reading {
	original := Reading{utf8.RuneCountInString(m.Original), catalog.ReadAloudRate(m.SourceLang)}
	translation := Reading{utf8.RuneCountInString(m.Translation), catalog.ReadAloudRate(m.TargetLang)}
	if translation.duration() > original.duration() {
		return translation
	}
	return original
}

// Join separates nonempty originals and translations with one space and keeps m's languages.
func (m Message) Join(next Message) Message {
	join := func(a, b string) string {
		if a == "" {
			return b
		}
		if b == "" {
			return a
		}
		return a + " " + b
	}
	m.Original = join(m.Original, next.Original)
	m.Translation = join(m.Translation, next.Translation)
	return m
}

func parseTemplate(format string) (*template.Template, error) {
	t, err := template.New("chatbox").Option("missingkey=error").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTemplate, err)
	}
	return t, nil
}

// ValidateTemplate executes a sample because parsing alone accepts field typos.
func ValidateTemplate(format string) error {
	_, err := Render(format, Message{
		Original: "original", Translation: "translation",
		SourceLang: "source", TargetLang: "target",
	})
	return err
}

// Render splits formatted text into pages of at most 144 runes and nine lines.
// Splits prefer sentence endings, then whitespace, then rune boundaries.
// Render trims whitespace at page edges. Whitespace-only text renders to no pages.
// Render returns no pages if the template fails or renders invalid text.
func Render(format string, message Message) ([]string, error) {
	t, err := parseTemplate(format)
	if err != nil {
		return nil, err
	}
	var rendered strings.Builder
	if err := t.Execute(&rendered, message); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTemplate, err)
	}
	text := rendered.String()
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("%w: text must be valid UTF-8", ErrTemplate)
	}
	if strings.ContainsRune(text, 0) {
		return nil, fmt.Errorf("%w: text must not contain NUL", ErrTemplate)
	}
	return pages(text), nil
}
