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
)

// Translate sends one phrase as the only user turn and returns trimmed text.
// It rejects empty or whitespace-only phrases. Calls can run concurrently after
// Start succeeds. Translate reports a server exit as an error without relaunching
// it. Call Start to relaunch.
// A response that reaches the token limit returns an error instead of partial text.
// An empty source omits source_lang so that PLaMo detects the input language.
func (s *Supervisor) Translate(ctx context.Context, source, target, phrase string) (string, error) {
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
	languages := map[string]string{"target_lang": target}
	if source != "" {
		languages["source_lang"] = source
	}
	body, err := json.Marshal(map[string]any{
		"model":                catalog.TranslationAlias,
		"messages":             []map[string]string{{"role": "user", "content": phrase}},
		"chat_template_kwargs": languages,
		"temperature":          0, "max_tokens": 512, "stream": false,
		"stop": []string{"<|plamo:tag|>", "<|plamo:bos|>"},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, run.url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := run.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("translate phrase: %w", errors.Join(err, run.child.exitError()))
	}
	defer resp.Body.Close()
	const maxResponse = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return "", fmt.Errorf("read translation: %w", errors.Join(err, run.child.exitError()))
	}
	if len(data) > maxResponse {
		return "", errors.New("translation response exceeds 1 MiB")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("translation HTTP %d: %.4096s", resp.StatusCode, data)
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
	return text, nil
}
