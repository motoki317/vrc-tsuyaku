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
	if len(languages.Spoken) != 2 {
		t.Fatal(languages.Spoken)
	}
	for _, tc := range []struct{ code, recognizer string }{{"ja", Haas}, {"en", Parakeet}} {
		language, ok := Spoken(tc.code)
		if !ok || language.Recognizer != tc.recognizer || language.Auto {
			t.Fatalf("spoken %s: %+v, %v", tc.code, language, ok)
		}
		for _, goos := range []string{"linux", "windows"} {
			a, err := Lookup(tc.recognizer, goos)
			if err != nil || !slices.Contains(a.Languages, tc.code) {
				t.Fatalf("recognizer %s: %+v, %v", tc.code, a, err)
			}
			required, err := Required(tc.code)
			if err != nil || !reflect.DeepEqual(required, append(slices.Clone(languages.CommonArtifacts), tc.recognizer)) {
				t.Fatalf("required %s: %v, %v", tc.code, required, err)
			}
			for _, id := range required {
				if _, err := Lookup(id, goos); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, invalid := range []string{"", "auto", "de", "ja-easy", "JA"} {
		if _, ok := Spoken(invalid); ok {
			t.Fatalf("accepted spoken %q", invalid)
		}
		if _, err := Required(invalid); err == nil {
			t.Fatalf("accepted requirements for %q", invalid)
		}
	}
	model, _ := Lookup(PLaMo, "linux")
	if !reflect.DeepEqual(model.Languages, languages.Targets) {
		t.Fatal("model languages differ from targets")
	}
}

func TestLanguageCopies(t *testing.T) {
	before := Languages()
	changed := Languages()
	changed.Targets[0] = "changed"
	changed.Spoken[0].Recognizer = "changed"
	changed.CommonArtifacts[0] = "changed"
	required, _ := Required("ja")
	required[0] = "changed"
	if !reflect.DeepEqual(before, Languages()) {
		t.Fatal("caller mutated language catalog")
	}
}
