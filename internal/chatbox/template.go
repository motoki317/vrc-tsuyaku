package chatbox

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

// ErrTemplate distinguishes a phrase's formatting failure from a failed UDP send.
var ErrTemplate = errcode.With(errcode.Template, errors.New("chatbox template"))

// A viewer notices a chatbox some time after it appears and reads it at a
// distance, so the rate is slower than subtitle guides allow. Netflix's guides
// allow 20 characters per second in English (50 ms each) and 4 in Japanese
// (250 ms each). At 50 ms per rune, pages turned over too fast in VRChat.
// https://partnerhelp.netflixstudios.com/hc/en-us/articles/217350977-English-USA-Timed-Text-Style-Guide
// https://backlothelp.netflix.com/hc/en-us/articles/215767517
const runeReadingTime = 120 * time.Millisecond

// PageReadingTime bounds each page's wait when translated phrases back up.
const PageReadingTime = maxRunes * runeReadingTime

// ReadUntil returns when a page's reading time ends. since is when the page's
// message first appeared, and shown is when this page did; they differ once a
// phrase joined the message. Every rune gets 120 ms counted from since, and the
// page stays at least 1.5 s from shown, so a phrase can still join a page that
// was just written.
func ReadUntil(page string, since, shown time.Time) time.Time {
	end := since.Add(time.Duration(utf8.RuneCountInString(page)) * runeReadingTime)
	if floor := shown.Add(pageInterval); floor.After(end) {
		return floor
	}
	return end
}

type Message struct {
	Original    string `json:"original"`
	Translation string `json:"translation"`
	SourceLang  string `json:"sourceLang"`
	TargetLang  string `json:"targetLang"`
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
// Whitespace at page edges is omitted. Whitespace-only text renders to no pages.
// A template error or invalid rendered text fails before any page is returned.
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
