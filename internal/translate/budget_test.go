package translate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
)

func TestTranslationTermBudget(t *testing.T) {
	s, dir := fixture(t, true, false)
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := glossary.Term{Source: strings.Repeat("🪿", 32), Target: strings.Repeat("稀", 32)}
	oversized := glossary.Term{Source: strings.Repeat("字", 240), Target: "x"}
	last := glossary.Term{Source: "later", Target: "後"}
	exact := glossary.Term{Source: strings.Repeat("字", 235), Target: "last"}
	second := glossary.Term{Source: strings.Repeat("𐀀", 32), Target: first.Target}
	third := glossary.Term{Source: strings.Repeat("稀", 32), Target: first.Target}
	fourth := glossary.Term{Source: strings.Repeat("字", 32), Target: first.Target}
	fifth := glossary.Term{Source: strings.Repeat("後", 12), Target: first.Target}
	for i, tc := range []struct{ terms, want []glossary.Term }{
		{[]glossary.Term{first, oversized, last}, []glossary.Term{first, last}},
		{[]glossary.Term{oversized}, nil},
		{[]glossary.Term{exact, last}, []glossary.Term{exact}},
		{[]glossary.Term{first, second, third, fourth, fifth}, []glossary.Term{first, second, third, fifth}},
	} {
		if _, err := s.Translate(t.Context(), "ja", "en", "phrase", tc.terms); err != nil {
			t.Fatal(err)
		}
		assertRequestTerms(t, requests(t, dir, i+1)[i].Body, tc.want)
	}

	text := []rune("一丁丂七丄丅丆万丈三上下丌不与丏丐丑丒专且丕世丗丘丙业丛东丝丞丟")
	var entries []glossary.Entry
	for length := 1; len(entries) < glossary.MaxEntries; length++ {
		for start := 0; start+length <= len(text) && len(entries) < glossary.MaxEntries; start++ {
			entries = append(entries, glossary.Entry{SourceLang: "ja", Source: string(text[start : start+length]), TargetLang: "en", Target: strings.Repeat("🪿", 32)})
		}
	}
	if err := glossary.Validate(entries); err != nil {
		t.Fatal(err)
	}
	terms := glossary.Terms(entries, "ja", "en")
	var words []string
	for _, term := range terms {
		words = append(words, term.Source)
	}
	_, matched := glossary.Rewrite(strings.Join(words, ","), terms)
	if len(matched) != 200 {
		t.Fatalf("matched %d terms", len(matched))
	}
	if _, err := s.Translate(t.Context(), "ja", "en", string(text), matched); err != nil {
		t.Fatal(err)
	}
	// The first seven one-character sources cost 34 each. The eighth exceeds 240.
	assertRequestTerms(t, requests(t, dir, 5)[4].Body, matched[:7])
}

func assertRequestTerms(t *testing.T, data []byte, want []glossary.Term) {
	t.Helper()
	var body struct {
		Kwargs struct {
			Terms []struct{ Source, Target string } `json:"glossary"`
		} `json:"chat_template_kwargs"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	var got []glossary.Term
	for _, term := range body.Kwargs.Terms {
		got = append(got, glossary.Term{Source: term.Source, Target: term.Target})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request terms = %+v, want %+v", got, want)
	}
	if len(want) == 0 && strings.Contains(string(data), `"glossary"`) {
		t.Fatalf("empty glossary sent: %s", data)
	}
}
