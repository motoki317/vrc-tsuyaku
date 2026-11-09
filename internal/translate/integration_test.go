package translate

import (
	"context"
	"os"
	"testing"
	"time"
	"unicode"
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
		text, err := s.Translate(ctx, tc.source, tc.target, tc.phrase)
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
}
