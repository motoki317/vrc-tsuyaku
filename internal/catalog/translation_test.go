package catalog

import (
	"reflect"
	"slices"
	"testing"
)

func TestLanguages(t *testing.T) {
	languages := Languages()
	if len(languages.Targets) != 54 || !slices.IsSorted(languages.Targets) {
		t.Fatalf("target codes: %v", languages.Targets)
	}
	for i, code := range languages.Targets {
		if !IsTarget(code) || (i > 0 && languages.Targets[i-1] == code) {
			t.Fatalf("invalid or duplicate target %q", code)
		}
	}
	for _, invalid := range []string{"", "auto", "JA", "zh", "ja-Easy", "unsupported"} {
		if IsTarget(invalid) {
			t.Fatalf("accepted target %q", invalid)
		}
	}
	if len(languages.Spoken) != 20 {
		t.Fatal(languages.Spoken)
	}
	seen := make(map[string]bool)
	for _, language := range languages.Spoken {
		if seen[language.Code] {
			t.Fatalf("duplicate spoken %q", language.Code)
		}
		seen[language.Code] = true
		for _, goos := range []string{"linux", "windows"} {
			a, err := Lookup(language.Recognizer, goos)
			if err != nil || (!language.Auto && !slices.Contains(a.Languages, language.Code)) || slices.Contains(a.Languages, "auto") {
				t.Fatalf("recognizer %s: %+v, %v", language.Code, a, err)
			}
			for _, translator := range languages.Translators {
				required, err := Required(language.Code, translator.ID)
				if err != nil || !reflect.DeepEqual(required, append(slices.Clone(languages.CommonArtifacts), translator.Artifact, language.Recognizer)) {
					t.Fatalf("required %s/%s: %v, %v", language.Code, translator.ID, required, err)
				}
				for _, id := range required {
					if _, err := Lookup(id, goos); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	for _, invalid := range []string{"", "cs", "da", "el", "fi", "mt", "ro", "uk", "ja-easy", "JA"} {
		if _, ok := Spoken(invalid); ok {
			t.Fatalf("accepted spoken %q", invalid)
		}
		if _, err := Required(invalid, DefaultTranslator()); err == nil {
			t.Fatalf("accepted requirements for %q", invalid)
		}
	}
	for _, translator := range languages.Translators {
		model, _ := Lookup(translator.Artifact, "linux")
		if !reflect.DeepEqual(model.Languages, languages.Targets) {
			t.Fatal("model languages differ from targets")
		}
	}
}

func TestTranslators(t *testing.T) {
	choices := Translators()
	if len(choices) != 2 || choices[0].ID != Translator2B || choices[1].ID != Translator08B {
		t.Fatalf("translator order: %+v", choices)
	}
	if slices.Contains(Languages().CommonArtifacts, PLaMo) || slices.Contains(Languages().CommonArtifacts, PLaMo08B) {
		t.Fatal("translator in common artifacts")
	}
	defaults := 0
	seen := make(map[string]bool)
	for _, choice := range choices {
		if seen[choice.ID] || choice.Speed <= 0 {
			t.Fatalf("invalid or duplicate translator: %+v", choice)
		}
		seen[choice.ID] = true
		if choice.Default {
			defaults++
			if choice.ID != Translator2B || choice.ID != DefaultTranslator() || choice.Speed != 1 {
				t.Fatalf("default translator: %+v", choice)
			}
		}
		got, ok := LookupTranslator(choice.ID)
		if !ok || got != choice {
			t.Fatalf("translator lookup: %+v, %v", got, ok)
		}
		if _, err := Lookup(choice.Artifact, "linux"); err != nil {
			t.Fatal(err)
		}
	}
	defaultModel, _ := Lookup(choices[0].Artifact, "linux")
	smallModel, _ := Lookup(choices[1].Artifact, "linux")
	if defaults != 1 || defaultModel.MemoryBytes <= smallModel.MemoryBytes || choices[1].Speed <= choices[0].Speed {
		t.Fatalf("translator costs or defaults: %+v", choices)
	}
	for _, invalid := range []string{"", "unknown", PLaMo, PLaMo08B} {
		if _, err := Required("ja", invalid); err == nil {
			t.Fatalf("accepted requirements for translator %q", invalid)
		}
		if _, ok := LookupTranslator(invalid); ok {
			t.Fatalf("accepted unknown translator %q", invalid)
		}
	}
	choices[0].Artifact = "changed"
	if !reflect.DeepEqual(Translators(), Languages().Translators) || Translators()[0].Artifact != PLaMo {
		t.Fatal("caller mutated translators")
	}
}

func TestSpokenRecognizerMapping(t *testing.T) {
	for _, tc := range []struct {
		code, recognizer string
		auto             bool
	}{
		{"ja", Haas, false}, {"en", Parakeet, false},
		{"bg", ParakeetV3, false}, {"de", ParakeetV3, false}, {"es", ParakeetV3, false},
		{"et", ParakeetV3, false}, {"fr", ParakeetV3, false}, {"hr", ParakeetV3, false},
		{"hu", ParakeetV3, false}, {"it", ParakeetV3, false}, {"lt", ParakeetV3, false},
		{"lv", ParakeetV3, false}, {"nl", ParakeetV3, false}, {"pl", ParakeetV3, false},
		{"pt", ParakeetV3, false}, {"ru", ParakeetV3, false}, {"sk", ParakeetV3, false},
		{"sl", ParakeetV3, false}, {"sv", ParakeetV3, false}, {"auto", ParakeetV3, true},
	} {
		language, ok := Spoken(tc.code)
		if !ok || language.Recognizer != tc.recognizer || language.Auto != tc.auto {
			t.Fatalf("spoken %s: %+v, %v", tc.code, language, ok)
		}
	}
}

func TestTranslatorSource(t *testing.T) {
	for _, tc := range []struct{ spoken, want string }{
		{"ja", "ja"}, {"en", "en"}, {"de", "de"},
		{"auto", ""}, {"pt", ""}, {"sv", ""},
		{"cs", "cs"}, {"unsupported", "unsupported"},
	} {
		if got := TranslatorSource(tc.spoken); got != tc.want {
			t.Fatalf("source for %q = %q, want %q", tc.spoken, got, tc.want)
		}
	}
}

func TestLanguageCopies(t *testing.T) {
	before := Languages()
	changed := Languages()
	changed.Targets[0] = "changed"
	changed.Spoken[0].Recognizer = "changed"
	changed.CommonArtifacts[0] = "changed"
	changed.Translators[0].Artifact = "changed"
	required, _ := Required("ja", DefaultTranslator())
	required[0] = "changed"
	if !reflect.DeepEqual(before, Languages()) {
		t.Fatal("caller mutated language catalog")
	}
}
