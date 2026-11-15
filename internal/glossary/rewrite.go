package glossary

import (
	"slices"
	"strings"
	"unicode"
)

// Rewrite replaces source words and readings with the terms' source words.
// It returns the terms it uses in term order and never matches its own output.
func Rewrite(text string, terms []Term) (string, []Term) {
	if len(terms) == 0 {
		return text, nil
	}
	type candidate struct {
		word  []rune
		term  int
		short bool
	}
	var candidates []candidate
	exactWords := make(map[string]bool, len(terms))
	soundTerms := make(map[string]int)
	for index, term := range terms {
		exactWords[strings.ToLower(term.Source)] = true
		if term.soundKey != "" {
			if _, exists := soundTerms[term.soundKey]; !exists {
				soundTerms[term.soundKey] = index
			}
		}
		for _, word := range []string{term.Source, term.Reading} {
			if word != "" {
				folded := []rune(Fold(word))
				candidates = append(candidates, candidate{folded, index, len(folded) < 3 && !allHiragana([]rune(term.Source))})
			}
		}
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int { return len(b.word) - len(a.word) })
	original, folded := []rune(text), []rune(Fold(text))
	found := make([]bool, len(terms))
	var out strings.Builder
	for i := 0; i < len(folded); {
		termIndex := -1
		length := 0
		if len(soundTerms) > 0 && soundWordRune(original[i]) && (i == 0 || !soundWordRune(original[i-1])) {
			end := i + 1
			for end < len(original) && soundWordRune(original[end]) {
				end++
			}
			word := string(original[i:end])
			if !exactWords[strings.ToLower(word)] {
				if index, ok := soundTerms[soundKey(word)]; ok {
					termIndex, length = index, end-i
				}
			}
		}
		for _, candidate := range candidates {
			if len(candidate.word) < length {
				break
			}
			if !matches(original, folded, i, candidate.word) {
				continue
			}
			if candidate.short && allHiragana(original[i:i+len(candidate.word)]) {
				continue
			}
			termIndex = candidate.term
			length = len(candidate.word)
			break
		}
		if termIndex >= 0 {
			out.WriteString(terms[termIndex].Source)
			found[termIndex] = true
		} else {
			out.WriteRune(original[i])
			length = 1
		}
		i += length
	}
	var used []Term
	for i, term := range terms {
		if found[i] {
			used = append(used, term)
		}
	}
	return out.String(), used
}

func allHiragana(text []rune) bool {
	for _, r := range text {
		if r != 'ー' && !unicode.Is(unicode.Hiragana, r) {
			return false
		}
	}
	return true
}

func matches(original, text []rune, start int, word []rune) bool {
	end := start + len(word)
	if len(word) == 0 || end > len(text) || !slices.Equal(text[start:end], word) {
		return false
	}
	if start > 0 && spacedLetter(word[0]) && spacedLetter(text[start-1]) {
		return false
	}
	if start > 0 && katakanaLetter(original[start]) && (katakanaLetter(original[start-1]) || original[start-1] == 'ー') {
		return false
	}
	last := word[len(word)-1]
	if end < len(text) {
		if (katakanaLetter(original[end-1]) || original[end-1] == 'ー') && katakanaLetter(original[end]) {
			return false
		}
		if spacedLetter(last) && spacedLetter(text[end]) {
			return false
		}
		if (unicode.In(last, unicode.Hiragana, unicode.Katakana) || last == 'ー') && strings.ContainsRune("ーァィゥェォャュョ", text[end]) {
			return false
		}
	}
	return true
}

func katakanaLetter(r rune) bool {
	return unicode.IsLetter(r) && unicode.Is(unicode.Katakana, r)
}

func spacedLetter(r rune) bool {
	// The long vowel mark and the listed scripts join words without spaces.
	// Other letters and digits need word boundaries.
	return r != 'ー' && (unicode.IsLetter(r) || unicode.IsDigit(r)) &&
		!unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Bopomofo,
			unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar)
}
