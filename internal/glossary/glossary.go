package glossary

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

const (
	MaxEntries = 200
	MaxRunes   = 32
)

type Entry struct {
	SourceLang string `yaml:"source_lang"`
	Source     string `yaml:"source"`
	TargetLang string `yaml:"target_lang"`
	Target     string `yaml:"target"`
	Reading    string `yaml:"reading,omitempty"`
}

func Validate(entries []Entry) error {
	if len(entries) > MaxEntries {
		return errcode.With(errcode.GlossaryLimit, fmt.Errorf("glossary: at most %d entries", MaxEntries))
	}
	for i, entry := range entries {
		for _, field := range []struct{ name, value string }{
			{"SourceLang", entry.SourceLang}, {"TargetLang", entry.TargetLang},
		} {
			if !catalog.IsTarget(field.value) || catalog.BaseLanguage(field.value) != field.value {
				return invalid(errcode.GlossaryLanguage, i, field.name, "unsupported translation language")
			}
		}
		if entry.SourceLang == entry.TargetLang {
			return invalid(errcode.GlossarySameLanguage, i, "TargetLang", "choose different source and target languages")
		}
		for _, field := range []struct{ name, value string }{
			{"Source", entry.Source}, {"Target", entry.Target}, {"Reading", entry.Reading},
		} {
			if utf8.RuneCountInString(field.value) > MaxRunes {
				return invalid(errcode.GlossaryLength, i, field.name, fmt.Sprintf("at most %d Unicode code points", MaxRunes))
			}
			if field.name == "Reading" {
				if field.value != "" && entry.SourceLang != catalog.Japanese && entry.TargetLang != catalog.Japanese {
					return invalid(errcode.GlossaryReading, i, field.name, "a reading needs a Japanese word")
				}
				for _, r := range field.value {
					kana := (r >= 'ぁ' && r <= 'ゖ') || (r >= 'ァ' && r <= 'ヺ') || r == 'ー'
					if !kana {
						return invalid(errcode.GlossaryReading, i, field.name, "use only hiragana, katakana and the long vowel mark")
					}
				}
				continue
			}
			if strings.TrimSpace(field.value) == "" {
				return invalid(errcode.GlossaryRequired, i, field.name, "enter a word")
			}
			for _, r := range field.value {
				if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
					return invalid(errcode.GlossaryControl, i, field.name, "control characters and line breaks are not allowed")
				}
			}
		}
	}
	return nil
}

func invalid(code errcode.Code, index int, field, message string) error {
	return errcode.At(code, index, field, fmt.Errorf("glossary[%d].%s: %s", index, field, message))
}

func Fold(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'ぁ' && r <= 'ゖ' {
			r += 'ァ' - 'ぁ'
		}
		folded := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < folded {
				folded = next
			}
		}
		return unicode.ToLower(folded)
	}, s)
}
