package translate

import "slices"

const (
	// Cap the number of short turns independently of the prompt's token budget.
	historyTurns = 10
	// Reserve the rest of the slot for the current phrase, glossary and answer.
	historyTokens = 400

	// Trim to half of both limits in one batch. Between trims, the retained turns
	// stay at the start of each request so llama-server can reuse the cached prefix.
	historyTrimTurns  = historyTurns / 2
	historyTrimTokens = historyTokens / 2
)

// History holds recent phrase and translation pairs for one flow. Its zero
// value is ready to use. Calls that share a History must run sequentially in phrase order.
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
	first := 0
	for len(h.turns)-first > historyTrimTurns || total > historyTrimTokens {
		total -= h.turns[first].tokens
		first++
	}
	h.turns = slices.Delete(h.turns, 0, first)
}
