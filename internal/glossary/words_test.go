package glossary

import (
	"slices"
	"testing"
)

func TestWords(t *testing.T) {
	entries := []Entry{{SourceLang: "ja", Source: "桔梗", TargetLang: "en", Target: "Kikyo", Reading: "ききょう"}, {SourceLang: "ja", Source: "マヌカ", TargetLang: "en", Target: "Manuka", Reading: "まぬか"}}
	for _, tc := range []struct {
		source, target string
		want           []string
	}{
		{"ja", "en", []string{"桔梗", "ききょう", "マヌカ"}},
		{"en", "ja", []string{"Kikyo", "Manuka"}},
		{"auto", "ja", []string{"Kikyo", "Manuka"}},
		{"ja", "de", nil},
	} {
		if got := Words(Terms(entries, tc.source, tc.target)); !slices.Equal(got, tc.want) {
			t.Fatalf("%s to %s: %v want %v", tc.source, tc.target, got, tc.want)
		}
	}
	if got := Words([]Term{{Source: "lilToon"}, {Source: "LILTOON"}}); !slices.Equal(got, []string{"lilToon"}) {
		t.Fatalf("case duplicates: %v", got)
	}
}
