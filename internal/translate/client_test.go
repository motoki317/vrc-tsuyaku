package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func requests(t *testing.T, dir string, count int) []recordedRequest {
	t.Helper()
	var records []recordedRequest
	eventually(t, func() bool {
		data, err := os.ReadFile(filepath.Join(dir, "requests.jsonl"))
		if err != nil {
			return false
		}
		records = nil
		for line := range bytes.SplitSeq(bytes.TrimSpace(data), []byte("\n")) {
			var record recordedRequest
			if json.Unmarshal(line, &record) != nil {
				return false
			}
			records = append(records, record)
		}
		return len(records) == count
	})
	return records
}

func setReply(t *testing.T, dir string, reply fakeReply) {
	t.Helper()
	data, _ := json.Marshal(reply)
	if err := os.WriteFile(filepath.Join(dir, "reply.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestTranslationRequests(t *testing.T) {
	s, dir := fixture(t, true, false)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := s.Translate(ctx, JapaneseToEnglish, "hello"); err == nil {
		t.Fatal("translated before Start")
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		direction              Direction
		phrase, source, target string
	}{
		{JapaneseToEnglish, "こんにちは、元気ですか？", "ja", "en"},
		{EnglishToJapanese, "Good morning.", "en", "ja"},
		{JapaneseToEnglish, "次の文章です。", "ja", "en"},
	} {
		got, err := s.Translate(ctx, tc.direction, tc.phrase)
		if err != nil || got != "translated text" {
			t.Fatalf("Translate = %q, %v", got, err)
		}
		record := requests(t, dir, i+1)[i]
		if record.Method != "POST" || record.ContentType != "application/json" {
			t.Fatalf("HTTP request: %+v", record)
		}
		var body map[string]any
		if err := json.Unmarshal(record.Body, &body); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"model":                "plamo-3-translate",
			"messages":             []any{map[string]any{"role": "user", "content": tc.phrase}},
			"chat_template_kwargs": map[string]any{"source_lang": tc.source, "target_lang": tc.target},
			"temperature":          float64(0), "max_tokens": float64(512), "stream": false,
			"stop": []any{"<|plamo:tag|>", "<|plamo:bos|>"},
		}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("request body = %s", record.Body)
		}
	}
	if _, err := s.Translate(ctx, JapaneseToEnglish, " \n"); err == nil {
		t.Fatal("accepted empty phrase")
	}
	if _, err := s.Translate(ctx, Direction(99), "text"); err == nil {
		t.Fatal("accepted invalid direction")
	}
	old := s.run
	if err := old.child.kill(); err != nil {
		t.Fatal(err)
	}
	<-old.child.done
	if _, err := s.Translate(ctx, JapaneseToEnglish, "text"); err == nil || !strings.Contains(err.Error(), "llama-server exited") {
		t.Fatalf("request after exit: %v", err)
	}
	if s.run != old {
		t.Fatal("request relaunched server")
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Translate(ctx, EnglishToJapanese, "Another phrase."); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentTranslation(t *testing.T) {
	s, dir := fixture(t, true, false)
	data, _ := json.Marshal(fakeConfig{Directory: dir, Concurrent: true})
	if err := os.WriteFile(s.modelPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for _, direction := range []Direction{JapaneseToEnglish, EnglishToJapanese} {
		go func() { _, err := s.Translate(ctx, direction, "phrase"); done <- err }()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	requests(t, dir, 2)
}

func TestTranslationFailures(t *testing.T) {
	s, dir := fixture(t, true, false)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		reply fakeReply
		want  string
	}{
		{"HTTP", fakeReply{Status: 500, Body: "fixture server error"}, "HTTP 500"},
		{"JSON", fakeReply{Status: 200, Body: "{"}, "decode translation"},
		{"choices", fakeReply{Status: 200, Body: `{"choices":[]}`}, "0 choices"},
		{"token limit", fakeReply{Status: 200, Body: `{"choices":[{"finish_reason":"length","message":{"content":"partial translation"}}]}`}, "incomplete translation"},
		{"empty text", fakeReply{Status: 200, Body: `{"choices":[{"message":{"content":" "}}]}`}, "no text"},
		{"oversize", fakeReply{Status: 200, Body: strings.Repeat("a", (1<<20)+1)}, "exceeds 1 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setReply(t, dir, tc.reply)
			if text, err := s.Translate(ctx, JapaneseToEnglish, "phrase"); err == nil || !strings.Contains(err.Error(), tc.want) || text != "" {
				t.Fatalf("text = %q, error = %v", text, err)
			}
		})
	}
}

func TestTranslationCancellation(t *testing.T) {
	s, dir := fixture(t, true, false)
	ctx, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	setReply(t, dir, fakeReply{Block: true})
	reqCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := s.Translate(reqCtx, JapaneseToEnglish, "phrase"); done <- err }()
	requests(t, dir, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel request: %v", err)
	}
	if err := s.run.child.exitError(); err != nil {
		t.Fatalf("request cancellation stopped shared server: %v", err)
	}
	setReply(t, dir, fakeReply{Status: 200, Body: `{"choices":[{"message":{"content":"OK"}}]}`})
	if _, err := s.Translate(ctx, EnglishToJapanese, "phrase"); err != nil {
		t.Fatal(err)
	}
}

func TestTranslationRejectsRedirect(t *testing.T) {
	var forwarded atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) }))
	defer other.Close()
	s, dir := fixture(t, true, false)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	setReply(t, dir, fakeReply{Status: http.StatusTemporaryRedirect, Location: other.URL})
	if _, err := s.Translate(ctx, JapaneseToEnglish, "private phrase"); err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("redirect error: %v", err)
	}
	if forwarded.Load() != 0 {
		t.Fatal("followed redirect")
	}
}
