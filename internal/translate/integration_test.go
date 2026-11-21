package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
)

func TestPLaMoTranslation(t *testing.T) {
	server := os.Getenv("VRC_TSUYAKU_TEST_LLAMA_SERVER")
	model := os.Getenv("VRC_TSUYAKU_TEST_PLAMO_GGUF")
	if server == "" || model == "" {
		t.Skip("set VRC_TSUYAKU_TEST_LLAMA_SERVER and VRC_TSUYAKU_TEST_PLAMO_GGUF")
	}
	s, err := New(server, model)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source, target string
		phrase         string
		japanese       bool
	}{
		{"ja", "en", "こんにちは、元気ですか？", false},
		{"en", "ja", "How are you today?", true},
		{"ja", "ja-easy", "本日の会議は延期されました。詳細については追ってご連絡いたします。", true},
		{"en", "ja-easy", "The meeting has been postponed until further notice.", true},
		{"", "ja", "La reunión se ha pospuesto hasta nuevo aviso.", true},
	} {
		text, err := s.Translate(ctx, tc.source, tc.target, tc.phrase, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		latin, japanese := 0, 0
		for _, r := range text {
			if unicode.Is(unicode.Latin, r) {
				latin++
			}
			if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) {
				japanese++
			}
		}
		if tc.japanese && japanese == 0 || !tc.japanese && (latin == 0 || japanese != 0) {
			t.Fatalf("wrong output script: %q", text)
		}
		t.Logf("%s -> %s: %s -> %s", tc.source, tc.target, tc.phrase, text)
	}
	t.Run("glossary", func(t *testing.T) {
		terms := []glossary.Term{{Source: "Qlo", Target: "九狼"}, {Source: "Poppy Alley", Target: "ポピー横丁"}}
		text, err := s.Translate(ctx, "en", "ja", "Qlo and I went to Poppy Alley last night.", terms, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, term := range terms {
			if !strings.Contains(text, term.Target) {
				t.Fatalf("translation %q lacks glossary target %q", text, term.Target)
			}
		}
		t.Logf("glossary translation: %s", text)
	})

	transport := &promptUsageTransport{RoundTripper: s.run.client.Transport}
	s.run.client.Transport = transport
	for _, tc := range []struct {
		source, target string
		first, next    string
	}{
		{"ja", "en", "今から買い物に行くよ。", "何か欲しいものはある？"},
		{"zh-hant", "zh-hant", "我現在要去買東西。", "你有什麼想要的嗎？"},
	} {
		t.Run(tc.source+" to "+tc.target+" with history", func(t *testing.T) {
			var history History
			first, err := s.Translate(ctx, tc.source, tc.target, tc.first, nil, &history)
			if err != nil {
				t.Fatal(err)
			}
			if len(history.turns) != 1 {
				t.Fatalf("first translation recorded %d turns, want one", len(history.turns))
			}
			counted := history.turns[0].tokens
			if _, err := s.Translate(ctx, tc.source, tc.target, tc.next, nil, nil); err != nil {
				t.Fatal(err)
			}
			without := transport.promptTokens
			next, err := s.Translate(ctx, tc.source, tc.target, tc.next, nil, &history)
			if err != nil {
				t.Fatal(err)
			}
			added := transport.promptTokens - without
			if without <= 0 || added <= 0 || added > counted {
				t.Fatalf("server reports %d tokens without history and %d with history, counted the pair as %d", without, transport.promptTokens, counted)
			}
			t.Logf("%q then %q. History added %d prompt tokens and counted %d.", first, next, added, counted)
		})
	}
}

type promptUsageTransport struct {
	http.RoundTripper
	promptTokens int
}

func (tr *promptUsageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := tr.RoundTripper.RoundTrip(req)
	if err != nil || req.URL.Path != "/v1/chat/completions" || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var result struct {
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	tr.promptTokens = result.Usage.PromptTokens
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, nil
}
