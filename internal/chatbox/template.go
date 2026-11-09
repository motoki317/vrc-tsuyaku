package chatbox

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
	"unicode/utf8"
)

// ErrTemplate distinguishes a phrase's formatting failure from a failed UDP send.
var ErrTemplate = errors.New("chatbox template")

type Message struct {
	Original    string `json:"original"`
	Translation string `json:"translation"`
	SourceLang  string `json:"sourceLang"`
	TargetLang  string `json:"targetLang"`
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
