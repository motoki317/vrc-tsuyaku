package translate

import "slices"

// History holds recent translations from one flow. Its zero value is ready to
// use. Calls that share a History must run in order and never concurrently.
type History struct {
	source, target string
	turns          []historyTurn
}

type historyTurn struct {
	original, translation string
	tokens                int
}

func (h *History) append(original, translation string, tokens int) {
	h.turns = append(h.turns, historyTurn{original, translation, tokens})
	total := 0
	for _, turn := range h.turns {
		total += turn.tokens
	}
	if len(h.turns) <= historyTurns && total <= historyTokens {
		return
	}
	// Drop several turns at once so subsequent requests can reuse the prompt prefix.
	first := 0
	for len(h.turns)-first > historyTurns/2 || total > historyTokens/2 {
		total -= h.turns[first].tokens
		first++
	}
	h.turns = slices.Delete(h.turns, 0, first)
}
