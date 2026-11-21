package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Translate sends a phrase with its flow's recent turns and returns trimmed text.
// A nil history sends a single turn. Calls with separate histories can run
// concurrently after Start succeeds. Call Start again after a server exit.
// Translate rejects empty and whitespace-only phrases.
// A response that reaches the token limit returns an error instead of partial text.
// An empty source omits source_lang so that PLaMo detects the input language.
func (s *Supervisor) Translate(ctx context.Context, source, target, phrase string, terms []glossary.Term, history *History) (string, error) {
	if strings.TrimSpace(phrase) == "" {
		return "", errors.New("translation phrase is empty")
	}
	if source != "" && !catalog.IsTarget(source) {
		return "", fmt.Errorf("unsupported translation source %q", source)
	}
	if !catalog.IsTarget(target) {
		return "", fmt.Errorf("unsupported translation target %q", target)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	run := s.currentRun()
	if run == nil {
		return "", errors.New("llama-server is not started: call Start before translating")
	}
	if err := run.child.exitError(); err != nil {
		return "", err
	}
	address := run.child.log.URL()
	if address == "" {
		return "", errors.New("llama-server has not reported its listening address: call Start before translating")
	}
	languages := map[string]any{"target_lang": target}
	if source != "" {
		languages["source_lang"] = source
	}
	terms = budgetTerms(terms)
	if len(terms) > 0 {
		pairs := make([]map[string]string, 0, len(terms))
		for _, term := range terms {
			pairs = append(pairs, map[string]string{"source": term.Source, "target": term.Target})
		}
		languages["glossary"] = pairs
	}
	var messages []chatMessage
	if history != nil {
		if history.source != source || history.target != target {
			*history = History{source: source, target: target}
		}
		messages = make([]chatMessage, 0, 2*len(history.turns)+1)
		for _, turn := range history.turns {
			messages = append(messages, chatMessage{"user", turn.original}, chatMessage{"assistant", turn.translation})
		}
	}
	messages = append(messages, chatMessage{"user", phrase})
	data, err := run.postJSON(ctx, address+"/v1/chat/completions", "translate phrase", "translation", map[string]any{
		"model":                catalog.TranslationAlias,
		"messages":             messages,
		"chat_template_kwargs": languages,
		"temperature":          0, "max_tokens": answerTokens, "stream": false,
		"cache_prompt": true,
		"stop":         []string{"<|plamo:tag|>", "<|plamo:bos|>"},
	})
	if err != nil {
		return "", err
	}
	var result struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("decode translation: %w", err)
	}
	if len(result.Choices) != 1 {
		return "", fmt.Errorf("translation returned %d choices, want one", len(result.Choices))
	}
	if result.Choices[0].FinishReason == "length" {
		return "", errors.New("incomplete translation: server reached the token limit")
	}
	text := strings.TrimSpace(result.Choices[0].Message.Content)
	if text == "" {
		return "", errors.New("translation response contains no text")
	}
	const reservedMarkup = "<|plamo:"
	// PLaMo's chat template rejects reserved markup in history messages.
	if history == nil || strings.Contains(phrase, reservedMarkup) || strings.Contains(text, reservedMarkup) {
		return text, nil
	}
	tokens, err := run.countTokens(ctx, address, phrase, text)
	if err != nil {
		return "", err
	}
	history.append(phrase, text, tokens+turnTags)
	return text, nil
}

func (r *serverRun) countTokens(ctx context.Context, address, phrase, translation string) (int, error) {
	data, err := r.postJSON(ctx, address+"/tokenize", "tokenize translation", "tokenization", map[string]any{
		"content":       []string{phrase, translation},
		"add_special":   false,
		"parse_special": false,
	})
	if err != nil {
		return 0, err
	}
	var result struct {
		Tokens []int `json:"tokens"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, fmt.Errorf("decode tokenization: %w", err)
	}
	if result.Tokens == nil {
		return 0, errors.New("tokenization response contains no token array")
	}
	return len(result.Tokens), nil
}

func (r *serverRun) postJSON(ctx context.Context, url, action, response string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, errors.Join(err, r.child.exitError()))
	}
	defer resp.Body.Close()
	const maxResponse = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", response, errors.Join(err, r.child.exitError()))
	}
	if len(data) > maxResponse {
		return nil, fmt.Errorf("%s response exceeds 1 MiB", response)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s HTTP %d: %.4096s", response, resp.StatusCode, data)
	}
	return data, nil
}
