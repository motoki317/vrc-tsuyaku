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

func TestReadingTime(t *testing.T) {
	for _, tc := range []struct {
		name, original, translation string
		want                        time.Duration
	}{
		{"greeting", "こんにちは", "Hello", 3700 * time.Millisecond},
		{"weather", "今日は寒いですね。", "It's cold today.", 5060 * time.Millisecond},
		{"longer original", strings.Repeat("猫", 25), strings.Repeat("a", 60), 10500 * time.Millisecond},
		{"longer translation", "猫", "It's cold today.", 3600 * time.Millisecond},
		{"original only", "猫", "", 2340 * time.Millisecond},
		{"translation only", "", "a b!", 2400 * time.Millisecond},
		{"empty", "", "", 2 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Message{Original: tc.original, Translation: tc.translation, SourceLang: "ja", TargetLang: "en"}
			if got := DefaultReadingTime().Duration(m); got != tc.want {
				t.Fatalf("Duration = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestReadingTimeSettings(t *testing.T) {
	m := Message{Original: "猫", SourceLang: "ja"}
	for _, tc := range []struct {
		reading        ReadingTime
		message, bound time.Duration
	}{
		{ReadingTime{Base: time.Second, Factor: 1}, 1170 * time.Millisecond, 7 * time.Second},
		{ReadingTime{Base: 10 * time.Second, Factor: 1}, 10170 * time.Millisecond, 16 * time.Second},
		{ReadingTime{Base: time.Second, Factor: 8}, 2360 * time.Millisecond, 49 * time.Second},
		{ReadingTime{Base: 10 * time.Second, Factor: 8}, 11360 * time.Millisecond, 58 * time.Second},
		{DefaultReadingTime(), 2340 * time.Millisecond, 14 * time.Second},
	} {
		if got := tc.reading.Duration(m); got != tc.message {
			t.Errorf("%+v: Duration = %s, want %s", tc.reading, got, tc.message)
		}
		if got := tc.reading.BacklogBound(); got != tc.bound {
			t.Errorf("%+v: BacklogBound = %s, want %s", tc.reading, got, tc.bound)
		}
	}
}

func TestReadingTimePageShares(t *testing.T) {
	m := Message{Original: "こんにちは", Translation: "Hello", SourceLang: "ja", TargetLang: "en"}
	reading := DefaultReadingTime()
	for _, tc := range []struct {
		pages []string
		want  []time.Duration
	}{
		{[]string{}, []time.Duration{}},
		{[]string{"[en] Hello"}, []time.Duration{3700 * time.Millisecond}},
		{[]string{"猫", "abc"}, []time.Duration{2425 * time.Millisecond, 3275 * time.Millisecond}},
	} {
		if got := reading.PageDurations(m, tc.pages); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("PageDurations(%q) = %v, want %v", tc.pages, got, tc.want)
		}
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
