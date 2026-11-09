package chatbox

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	message := Message{Original: "こんにちは", Translation: "Hello", SourceLang: "ja", TargetLang: "en"}
	for _, tc := range []struct {
		name, format string
		message      Message
		want         []string
	}{
		{"default", DefaultTemplate, message, []string{"[ja] こんにちは\n[en] Hello"}},
		{"translation only", "{{.Translation}}", message, []string{"Hello"}},
		{"reordered", "{{.TargetLang}}: {{.Translation}} / {{.SourceLang}}: {{.Original}}", message, []string{"en: Hello / ja: こんにちは"}},
		{"empty", "", message, []string{}},
		{"whitespace", " \t\n", message, []string{}},
		{"literal and function", `{{printf "%s <%s>" .Original .TargetLang}}`, message, []string{"こんにちは <en>"}},
		{"subtemplate", `{{define "line"}}{{.Translation}}{{end}}{{template "line" .}}`, message, []string{"Hello"}},
		{"rune limit includes labels", "[{{.SourceLang}}] {{.Original}}", Message{Original: strings.Repeat("猫", 145), SourceLang: "ja"}, []string{"[ja]", strings.Repeat("猫", 144), "猫"}},
		{"sentence boundary", "{{.Translation}}", Message{Translation: "Hello! " + strings.Repeat("猫", 144)}, []string{"Hello!", strings.Repeat("猫", 144)}},
		{"line limit", "{{.Original}}", Message{Original: strings.Repeat("a\n", 9) + "a"}, []string{strings.Repeat("a\n", 8) + "a", "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(tc.format, tc.message)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Render = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestTemplateFailures(t *testing.T) {
	for _, format := range []string{"{{", "{{unknown .Original}}", "{{if .Original}}"} {
		if err := ValidateTemplate(format); !errors.Is(err, ErrTemplate) {
			t.Fatalf("syntax validation: %q: %v", format, err)
		}
		if got, err := Render(format, Message{}); got != nil || !errors.Is(err, ErrTemplate) {
			t.Fatalf("invalid syntax rendered: %q: %q, %v", format, got, err)
		}
	}
	for _, tc := range []struct {
		format, want string
		message      Message
	}{
		{"partial {{.Unknown}}", "Unknown", Message{}},
		{`partial {{index .Translation 3}}`, "index", Message{Translation: "a"}},
		{`partial {{printf "%c" 0}}`, "NUL", Message{}},
		{"{{.Translation}}", "UTF-8", Message{Translation: "bad\xff"}},
		{"{{.Original}}", "NUL", Message{Original: "bad\x00"}},
	} {
		if got, err := Render(tc.format, tc.message); got != nil || !errors.Is(err, ErrTemplate) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("execution failure: %q, %v", got, err)
		}
	}
}

func TestValidateTemplateSampleExecution(t *testing.T) {
	for _, field := range []string{"Original", "Translation", "SourceLang", "TargetLang"} {
		format := "{{if ." + field + "}}[{{.SourceLang}}] {{.Orignal}}{{end}}"
		if err := ValidateTemplate(format); !errors.Is(err, ErrTemplate) || !strings.Contains(err.Error(), "Orignal") {
			t.Fatalf("sample field %s did not expose the typo: %v", field, err)
		}
	}
	format := `{{if eq .Original "short"}}{{index .Translation 100}}{{else}}{{.Translation}}{{end}}`
	if err := ValidateTemplate(format); err != nil {
		t.Fatal("sample triggered a phrase-dependent error", err)
	}
	if _, err := Render(format, Message{Original: "short", Translation: "a"}); !errors.Is(err, ErrTemplate) {
		t.Fatal("phrase-dependent execution error was lost", err)
	}
}
