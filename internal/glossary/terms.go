package glossary

import (
	"strings"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

type Term struct {
	Source   string
	Reading  string
	Target   string
	soundKey string
}

// Terms applies entries in either order of the flow's languages.
// The first entry wins when its folded source word or reading equals either of a later term's.
func Terms(entries []Entry, source, target string) []Term {
	target = catalog.BaseLanguage(target)
	detect := catalog.TranslatorSource(source) == ""
	seen := make(map[string]bool)
	var terms []Term
	for _, entry := range entries {
		var term Term
		switch {
		case entry.TargetLang == target && (detect || entry.SourceLang == source):
			term = Term{Source: entry.Source, Target: entry.Target}
		case entry.SourceLang == target && (detect || entry.TargetLang == source):
			term = Term{Source: entry.Target, Target: entry.Source}
		default:
			continue
		}
		if source == catalog.Japanese {
			term.Reading = entry.Reading
		}
		if source == catalog.English && strings.IndexFunc(term.Source, func(r rune) bool { return !asciiLetter(r) }) < 0 {
			if key := soundKey(term.Source); len(key) >= 5 {
				term.soundKey = key
			}
		}
		word, reading := Fold(term.Source), Fold(term.Reading)
		if seen[word] || (reading != "" && seen[reading]) {
			continue
		}
		seen[word] = true
		if reading != "" {
			seen[reading] = true
		}
		terms = append(terms, term)
	}
	return terms
}

// Words returns the source spellings and readings in term order, without folded duplicates.
func Words(terms []Term) []string {
	var words []string
	seen := make(map[string]bool)
	for _, term := range terms {
		for _, word := range []string{term.Source, term.Reading} {
			folded := Fold(word)
			if folded != "" && !seen[folded] {
				words = append(words, word)
				seen[folded] = true
			}
		}
	}
	return words
}
