// Reimplements NeMo's GPU-accelerated phrase-boosting tree (GPU-PB) for greedy
// transducer decoding (Apache-2.0), https://github.com/NVIDIA-NeMo/Speech.
// Paper: https://arxiv.org/abs/2508.07014. No upstream code is copied.
// See ../../THIRD_PARTY_NOTICES.md.

package asr

import (
	"strings"

	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
)

// These token bonuses come from tuning Haas and Parakeet v2 on synthetic Japanese
// and English name clips against a glossary of 200 entries.
// Parakeet v3 uses the same values, with validation on the English clips only.
const (
	firstHintBonus = 1.0
	laterHintBonus = 8.0
)

type pieceTrie struct {
	children map[rune]*pieceTrie
	tokens   []int
}

func (t *pieceTrie) add(piece string, token int) {
	for _, r := range piece {
		if t.children == nil {
			t.children = make(map[rune]*pieceTrie)
		}
		if t.children[r] == nil {
			t.children[r] = &pieceTrie{}
		}
		t = t.children[r]
	}
	t.tokens = append(t.tokens, token)
}

type hintVocabulary struct{ start, continuation pieceTrie }

func newHintVocabulary(v vocabulary, wordStarts bool) *hintVocabulary {
	h := &hintVocabulary{}
	for token, piece := range v {
		if strings.HasPrefix(piece, "<") && strings.HasSuffix(piece, ">") {
			continue
		}
		piece = glossary.Fold(piece)
		if wordStarts {
			if strings.HasPrefix(piece, "▁") {
				h.start.add(strings.ReplaceAll(strings.TrimPrefix(piece, "▁"), "▁", " "), token)
			}
			h.continuation.add(strings.ReplaceAll(piece, "▁", " "), token)
		} else {
			piece = strings.ReplaceAll(piece, "▁", "")
			if piece != "" {
				h.start.add(piece, token)
				h.continuation.add(piece, token)
			}
		}
	}
	return h
}

type hintEdge struct {
	token, next int
	bonus       float32
}
type hintNode struct{ edges []hintEdge }
type compiledHints struct {
	starts []hintEdge
	nodes  []hintNode
}

// compile runs once per Transcribe call. It includes only token sequences that
// can spell a whole hint word, so an unspellable word cannot change the output.
func (v *hintVocabulary) compile(words []string) *compiledHints {
	if len(words) == 0 {
		return nil
	}
	h := &compiledHints{}
	for _, word := range words {
		word = glossary.Fold(word)
		runes := []rune(word)
		if len(runes) < 3 {
			continue
		}
		base := len(h.nodes)
		h.nodes = append(h.nodes, make([]hintNode, len(runes)+1)...)
		viable := make([]bool, len(runes)+1)
		viable[len(runes)] = true
		edges := func(trie *pieceTrie, offset int, allowEmpty bool) []hintEdge {
			var result []hintEdge
			add := func(end int) {
				if !viable[end] || (end == offset && !allowEmpty) {
					return
				}
				var bonus float32
				if end > offset {
					bonus = laterHintBonus
					if offset == 0 {
						bonus = firstHintBonus
					}
				}
				for _, token := range trie.tokens {
					result = append(result, hintEdge{token, base + end, bonus})
				}
			}
			add(offset)
			for end := offset; end < len(runes); end++ {
				trie = trie.children[runes[end]]
				if trie == nil {
					break
				}
				add(end + 1)
			}
			return result
		}
		for offset := len(runes) - 1; offset >= 0; offset-- {
			h.nodes[base+offset].edges = edges(&v.continuation, offset, false)
			viable[offset] = len(h.nodes[base+offset].edges) > 0
		}
		h.starts = append(h.starts, edges(&v.start, 0, true)...)
	}
	if len(h.starts) == 0 {
		return nil
	}
	return h
}

type hintState struct {
	hints      *compiledHints
	live, next []hintMatch
	positions  []int
	bonuses    []float32
	touched    []int
}

type hintMatch struct{ node, start int }

func newHintState(h *compiledHints, vocabularySize int) *hintState {
	return &hintState{hints: h, positions: make([]int, len(h.nodes)), bonuses: make([]float32, vocabularySize)}
}

func (s *hintState) choose(logits []float32, token int) int {
	add := func(edges []hintEdge) {
		for _, edge := range edges {
			if edge.bonus > s.bonuses[edge.token] {
				if s.bonuses[edge.token] == 0 {
					s.touched = append(s.touched, edge.token)
				}
				s.bonuses[edge.token] = edge.bonus
			}
		}
	}
	add(s.hints.starts)
	for _, match := range s.live {
		add(s.hints.nodes[match.node].edges)
	}
	best := logits[token] + s.bonuses[token]
	for _, candidate := range s.touched {
		score := logits[candidate] + s.bonuses[candidate]
		if score > best || (score == best && candidate < token) {
			token, best = candidate, score
		}
		s.bonuses[candidate] = 0
	}
	s.touched = s.touched[:0]
	return token
}

// accept returns the earliest start token index among hints completed by token,
// or -1 if none complete. oldest is the earliest start among surviving partial
// matches, or index+1 if none survive.
func (s *hintState) accept(token, index int) (completed, oldest int) {
	completed, oldest = -1, index+1
	add := func(edges []hintEdge, start int) {
		for _, edge := range edges {
			if edge.token != token {
				continue
			}
			if len(s.hints.nodes[edge.next].edges) == 0 {
				if completed < 0 || start < completed {
					completed = start
				}
				continue
			}
			if s.positions[edge.next] == 0 {
				s.next = append(s.next, hintMatch{edge.next, start})
				s.positions[edge.next] = len(s.next)
			} else {
				match := &s.next[s.positions[edge.next]-1]
				match.start = min(match.start, start)
			}
		}
	}
	add(s.hints.starts, index)
	for _, match := range s.live {
		add(s.hints.nodes[match.node].edges, match.start)
	}
	s.live, s.next = s.next, s.live[:0]
	for _, match := range s.live {
		s.positions[match.node] = 0
		oldest = min(oldest, match.start)
	}
	return completed, oldest
}
