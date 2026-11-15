package glossary

import (
	"slices"
	"strings"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

func TestValidate(t *testing.T) {
	valid := Entry{SourceLang: "ja", Source: "桔梗", TargetLang: "en", Target: "Kikyo", Reading: "ききょう"}
	for _, tc := range []struct {
		name, field, value string
		code               errcode.Code
	}{
		{"missing language", "SourceLang", "", errcode.GlossaryLanguage},
		{"unknown language", "TargetLang", "unknown", errcode.GlossaryLanguage},
		{"detection", "SourceLang", "auto", errcode.GlossaryLanguage},
		{"recognizer only", "SourceLang", "pt", errcode.GlossaryLanguage},
		{"source variant", "SourceLang", "ja-easy", errcode.GlossaryLanguage},
		{"target variant", "TargetLang", "ja-easy", errcode.GlossaryLanguage},
		{"same language", "TargetLang", "ja", errcode.GlossarySameLanguage},
		{"missing source", "Source", "", errcode.GlossaryRequired},
		{"blank target", "Target", " \u3000 ", errcode.GlossaryRequired},
		{"source newline", "Source", "桔\n梗", errcode.GlossaryControl},
		{"target tab", "Target", "Kik\tyo", errcode.GlossaryControl},
		{"target NUL", "Target", "Kik\x00yo", errcode.GlossaryControl},
		{"source DEL", "Source", "桔\x7f梗", errcode.GlossaryControl},
		{"source C1", "Source", "桔\u0085梗", errcode.GlossaryControl},
		{"source line separator", "Source", "桔\u2028梗", errcode.GlossaryControl},
		{"target paragraph separator", "Target", "Kik\u2029yo", errcode.GlossaryControl},
		{"long source", "Source", strings.Repeat("桔", MaxRunes+1), errcode.GlossaryLength},
		{"long target", "Target", strings.Repeat("😀", MaxRunes+1), errcode.GlossaryLength},
		{"long reading", "Reading", strings.Repeat("キ", MaxRunes+1), errcode.GlossaryLength},
		{"reading kanji", "Reading", "桔梗", errcode.GlossaryReading},
		{"reading Latin", "Reading", "kikyo", errcode.GlossaryReading},
		{"reading space", "Reading", "き きょう", errcode.GlossaryReading},
		{"reading newline", "Reading", "ききょう\n", errcode.GlossaryReading},
		{"reading punctuation", "Reading", "キキョウ・", errcode.GlossaryReading},
		{"reading half-width", "Reading", "ｶ", errcode.GlossaryReading},
		{"reading hiragana iteration", "Reading", "ゝ", errcode.GlossaryReading},
		{"reading katakana iteration", "Reading", "ヾ", errcode.GlossaryReading},
		{"reading supplementary", "Reading", "𛀁", errcode.GlossaryReading},
		{"reading hiragana ligature", "Reading", "ゟ", errcode.GlossaryReading},
		{"reading katakana ligature", "Reading", "ヿ", errcode.GlossaryReading},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := valid
			switch tc.field {
			case "SourceLang":
				entry.SourceLang = tc.value
			case "TargetLang":
				entry.TargetLang = tc.value
			case "Source":
				entry.Source = tc.value
			case "Target":
				entry.Target = tc.value
			case "Reading":
				entry.Reading = tc.value
			}
			err := Validate([]Entry{valid, entry})
			location := errcode.LocationOf(err)
			if err == nil || errcode.Of(err) != tc.code || location == nil || location.Index != 1 || location.Field != tc.field {
				t.Fatalf("validation = %v (%s, %+v), want %s at row 1 %s", err, errcode.Of(err), location, tc.code, tc.field)
			}
		})
	}
	for _, entries := range [][]Entry{
		nil, {}, {valid},
		{{SourceLang: "en", Source: "Kikyo", TargetLang: "ja", Target: "桔梗", Reading: "キキョー"}},
		{{SourceLang: "ja", Source: " 桔梗 ", TargetLang: "en", Target: " Kikyo "}},
		{{SourceLang: "en", Source: "Modular Avatar", TargetLang: "fr", Target: "Modular Avatar"}},
		{{SourceLang: "ja", Source: strings.Repeat("😀", MaxRunes), TargetLang: "en", Target: strings.Repeat("𠮷", MaxRunes), Reading: strings.Repeat("キ", MaxRunes)}},
		{{SourceLang: "ja", Source: "word", TargetLang: "en", Target: "word", Reading: "ぁゖァヺー"}},
		slices.Repeat([]Entry{valid}, MaxEntries),
	} {
		if err := Validate(entries); err != nil {
			t.Fatalf("valid entries rejected: %v", err)
		}
	}
	valid.SourceLang, valid.TargetLang = "en", "fr"
	if err := Validate([]Entry{valid}); errcode.Of(err) != errcode.GlossaryReading {
		t.Fatal("reading without Japanese accepted", err)
	}
	if err := Validate(slices.Repeat([]Entry{valid}, MaxEntries+1)); errcode.Of(err) != errcode.GlossaryLimit || errcode.LocationOf(err) != nil {
		t.Fatal("list limit not checked", err)
	}
}

func TestTranslatorLanguages(t *testing.T) {
	for _, language := range catalog.Languages().Targets {
		if catalog.BaseLanguage(language) != language {
			continue
		}
		other := "ja"
		if language == other {
			other = "en"
		}
		if err := Validate([]Entry{{SourceLang: language, Source: "word", TargetLang: other, Target: "word"}}); err != nil {
			t.Fatalf("translator language %q rejected: %v", language, err)
		}
	}
}

func TestFold(t *testing.T) {
	for _, tc := range []struct{ left, right string }{
		{"まぬか", "マヌカ"},
		{"ゔぁゖ", "ヴァヶ"},
		{"lilToon ブイすい", "LILTOON ぶいスイ"},
		{"Ångström Σςσ Kſ", "ångström σσσ ks"},
		{"桔梗 😀", "桔梗 😀"},
		{"", ""},
	} {
		if Fold(tc.left) != Fold(tc.right) || Fold(Fold(tc.left)) != Fold(tc.left) {
			t.Errorf("fold differs or is not idempotent: %q -> %q, %q -> %q", tc.left, Fold(tc.left), tc.right, Fold(tc.right))
		}
	}
	for _, tc := range []struct{ left, right string }{
		{"キョ", "キヨ"}, {"ハッカ", "ハッカー"}, {"カ", "ｶ"}, {"が", "か\u3099"}, {"ß", "ss"},
	} {
		if Fold(tc.left) == Fold(tc.right) {
			t.Errorf("fold changed spelling: %q, %q", tc.left, tc.right)
		}
	}
}
