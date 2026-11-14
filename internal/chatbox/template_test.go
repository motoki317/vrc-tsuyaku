package chatbox

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestMessageJoin(t *testing.T) {
	for _, a := range []string{"", "a"} {
		for _, b := range []string{"", "b"} {
			for _, x := range []string{"", "x"} {
				for _, y := range []string{"", "y"} {
					got := (Message{Original: a, Translation: x, SourceLang: "ja", TargetLang: "en"}).Join(Message{Original: b, Translation: y})
					want := Message{Original: strings.TrimSpace(a + " " + b), Translation: strings.TrimSpace(x + " " + y), SourceLang: "ja", TargetLang: "en"}
					if got != want {
						t.Fatalf("join (%q, %q) + (%q, %q) = %+v, want %+v", a, x, b, y, got, want)
					}
				}
			}
		}
	}
}

func TestReadUntil(t *testing.T) {
	since := time.Unix(100, 0)
	for _, tc := range []struct {
		name, page string
		shown, end time.Duration
	}{
		{"long page", strings.Repeat("猫", 100), 0, 12 * time.Second},
		{"full page", strings.Repeat("a", 144), 0, 17280 * time.Millisecond},
		{"short page", "hi", 0, 1500 * time.Millisecond},
		{"merged long page", strings.Repeat("猫", 100) + " hm", time.Second, 12360 * time.Millisecond},
		{"merged short page", "a b", time.Second, 2500 * time.Millisecond},
		{"late merge", strings.Repeat("a", 101), 12 * time.Second, 13500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReadUntil(tc.page, since, since.Add(tc.shown)); !got.Equal(since.Add(tc.end)) {
				t.Fatalf("ReadUntil = %s after first showing, want %s", got.Sub(since), tc.end)
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
