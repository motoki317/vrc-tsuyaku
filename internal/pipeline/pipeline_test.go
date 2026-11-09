package pipeline

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
)

type sourceFunc func(context.Context, func([]float32) error) error

func (s sourceFunc) Capture(ctx context.Context, f func([]float32) error) error { return s(ctx, f) }

func fakeStages() Stages {
	return Stages{
		Source: sourceFunc(func(ctx context.Context, consume func([]float32) error) error {
			for range 3 {
				samples := make([]float32, 512*24)
				for i := range 512 * 8 {
					samples[i] = 1
				}
				for len(samples) > 0 {
					n := min(317, len(samples))
					if err := consume(samples[:n]); err != nil {
						return err
					}
					samples = samples[n:]
				}
			}
			return nil
		}),
		Probability: func(_ context.Context, x []float32) (float32, error) { return x[0], nil },
		Recognize:   func(_ context.Context, _ []float32) (string, error) { return "text", nil },
		Settings: func() PhraseSettings {
			return PhraseSettings{SourceLang: "ja", TargetLang: "en", Template: chatbox.DefaultTemplate}
		},
		Translate: func(_ context.Context, _, _, s string) (string, error) { return "translated " + s, nil },
	}
}

func TestPhraseOrder(t *testing.T) {
	s := fakeStages()
	var got []string
	i := 0
	s.Recognize = func(_ context.Context, x []float32) (string, error) {
		i++
		if len(x) < 512*24 {
			t.Error("phrase lost confirming silence")
		}
		return fmt.Sprint(i), nil
	}
	s.Send = func(_ context.Context, _ string, message chatbox.Message, sent func(string)) error {
		got = append(got, message.Original+":"+message.Translation)
		sent(message.Translation)
		return nil
	}
	var mu sync.Mutex
	counts := map[string]int{}
	s.Emit = func(e Event) { mu.Lock(); defer mu.Unlock(); counts[e.Kind]++ }
	if err := Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"1:translated 1", "2:translated 2", "3:translated 3"}) {
		t.Fatal(got)
	}
	for _, kind := range []string{"recognized", "translated", "page", "sent"} {
		if counts[kind] != 3 {
			t.Fatal(counts)
		}
	}
}

func TestSpeechEvents(t *testing.T) {
	for _, tc := range []struct {
		name          string
		voice, silent int
		emptyText     bool
		want          []string
		recognized    int
	}{
		{"silence", 0, 20, false, nil, 0},
		{"phrase", 8, 16, false, []string{"start", "end"}, 1},
		{"too short", 1, 16, false, []string{"start", "end"}, 0},
		{"empty recognition", 8, 16, true, []string{"start", "end"}, 0},
		{"flush", 8, 0, false, []string{"start", "end"}, 1},
		{"cut", 477, 0, false, []string{"start", "end", "start", "end"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fakeStages()
			s.Source = sourceFunc(func(_ context.Context, consume func([]float32) error) error {
				samples := make([]float32, 512*(tc.voice+tc.silent))
				for i := range 512 * tc.voice {
					samples[i] = 1
				}
				return consume(samples)
			})
			if tc.emptyText {
				s.Recognize = func(context.Context, []float32) (string, error) { return "", nil }
			}
			var mu sync.Mutex
			var speech []string
			ends, recognized := 0, 0
			s.Emit = func(e Event) {
				mu.Lock()
				defer mu.Unlock()
				if e.Kind == "speech" {
					speech = append(speech, e.Text)
					if e.Text == "end" {
						ends++
					}
				}
				if e.Kind == "recognized" {
					recognized++
					if ends < recognized {
						t.Error("recognized preceded its speech end")
					}
				}
			}
			if err := Run(t.Context(), s); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(speech, tc.want) || recognized != tc.recognized {
				t.Fatal(speech, recognized)
			}
		})
	}
}

func TestCancellationEndsSpeech(t *testing.T) {
	s := fakeStages()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.Source = sourceFunc(func(ctx context.Context, consume func([]float32) error) error {
		x := make([]float32, 512)
		for i := range x {
			x[i] = 1
		}
		if err := consume(x); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
	var got []string
	s.Emit = func(e Event) {
		if e.Kind == "speech" {
			got = append(got, e.Text)
			if e.Text == "start" {
				cancel()
			}
		}
	}
	if err := Run(ctx, s); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, []string{"start", "end"}) {
		t.Fatal(err, got)
	}
}

func TestPhraseIdentityAcrossRuns(t *testing.T) {
	seen := make(map[uint64]bool)
	for _, send := range []bool{true, false, true} {
		s := fakeStages()
		var mu sync.Mutex
		var events []Event
		s.Emit = func(e Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) }
		if send {
			s.Send = func(_ context.Context, _ string, _ chatbox.Message, sent func(string)) error {
				sent("a page independent of text matching")
				sent("another page\u0085with whitespace")
				return nil
			}
		}
		if err := Run(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		phrases := make(map[uint64][]string)
		for _, e := range events {
			if e.Kind == "speech" {
				continue
			}
			if e.SourceLang != "ja" || e.TargetLang != "en" {
				t.Fatalf("missing phrase languages: %+v", e)
			}
			if e.PhraseID == 0 {
				t.Fatalf("missing phrase ID: %+v", e)
			}
			if e.Kind == "recognized" {
				if seen[e.PhraseID] || e.Text != "text" {
					t.Fatalf("reused identity or changed repeated input: %+v", e)
				}
				seen[e.PhraseID] = true
			}
			if e.Kind == "translated" && (e.Original != "text" || e.Text != "translated text") {
				t.Fatalf("translation: %+v", e)
			}
			phrases[e.PhraseID] = append(phrases[e.PhraseID], e.Kind)
		}
		if len(phrases) != 3 {
			t.Fatal(phrases)
		}
		want := []string{"recognized", "translated"}
		if send {
			want = append(want, "page", "page", "sent")
		}
		for id, kinds := range phrases {
			if !reflect.DeepEqual(kinds, want) {
				t.Fatalf("phrase %d: %v, want %v", id, kinds, want)
			}
		}
	}
}

func TestLanguageChangesApplyToNextPhrase(t *testing.T) {
	s := fakeStages()
	var target atomic.Value
	target.Store("en")
	s.Settings = func() PhraseSettings {
		return PhraseSettings{SourceLang: "ja", TargetLang: target.Load().(string), Template: chatbox.DefaultTemplate}
	}
	first := true
	s.Recognize = func(context.Context, []float32) (string, error) {
		if first {
			first = false
			target.Store("ja-easy")
		}
		return "text", nil
	}
	var translatedTargets []string
	s.Translate = func(_ context.Context, source, target, phrase string) (string, error) {
		if source != "ja" || phrase != "text" {
			t.Errorf("unexpected request: %s, %s", source, phrase)
		}
		translatedTargets = append(translatedTargets, target)
		return "translation", nil
	}
	s.Send = func(_ context.Context, _ string, _ chatbox.Message, sent func(string)) error {
		sent("page")
		return nil
	}
	var mu sync.Mutex
	phrases := make(map[uint64][]Event)
	s.Emit = func(e Event) {
		if e.Kind != "speech" {
			mu.Lock()
			defer mu.Unlock()
			phrases[e.PhraseID] = append(phrases[e.PhraseID], e)
		}
	}
	if err := Run(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(translatedTargets, []string{"en", "ja-easy", "ja-easy"}) {
		t.Fatal("language change lost its phrase boundary", translatedTargets)
	}
	old, updated := 0, 0
	for _, events := range phrases {
		if len(events) != 4 {
			t.Fatal(events)
		}
		want := events[0].TargetLang
		switch want {
		case "en":
			old++
		case "ja-easy":
			updated++
		default:
			t.Fatal(events)
		}
		for _, e := range events {
			if e.SourceLang != "ja" || e.TargetLang != want {
				t.Fatalf("phrase languages changed mid-flight: %+v", events)
			}
		}
	}
	if old != 1 || updated != 2 {
		t.Fatal(old, updated)
	}
}

func TestSentWaitsForSuccessfulReturn(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s := fakeStages()
			var returned atomic.Bool
			var completed atomic.Int32
			want := errors.New("send failed after one page")
			s.Send = func(_ context.Context, _ string, _ chatbox.Message, sent func(string)) error {
				returned.Store(false)
				defer returned.Store(true)
				sent("partial page")
				if fail {
					return want
				}
				return nil
			}
			s.Emit = func(e Event) {
				if e.Kind == "sent" {
					if !returned.Load() || fail {
						t.Error("marked sent before successful Send return")
					}
					completed.Add(1)
				}
			}
			err := Run(t.Context(), s)
			if fail {
				if !errors.Is(err, want) || completed.Load() != 0 {
					t.Fatal(err, completed.Load())
				}
			} else if err != nil || completed.Load() != 3 {
				t.Fatal(err, completed.Load())
			}
		})
	}
}

func TestTemplateChangesApplyToNextPhrase(t *testing.T) {
	s := fakeStages()
	var format atomic.Value
	format.Store(chatbox.DefaultTemplate)
	s.Settings = func() PhraseSettings {
		return PhraseSettings{SourceLang: "ja", TargetLang: "en", Template: format.Load().(string)}
	}
	s.Recognize = func(context.Context, []float32) (string, error) {
		format.Store("{{.Translation}}")
		return "original", nil
	}
	var got []string
	s.Send = func(_ context.Context, format string, message chatbox.Message, sent func(string)) error {
		pages, err := chatbox.Render(format, message)
		for _, page := range pages {
			got = append(got, page)
			sent(page)
		}
		return err
	}
	if err := Run(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	want := []string{"[ja] original\n[en] translated original", "translated original", "translated original"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("template changed mid-phrase: %q, want %q", got, want)
	}
}

func TestTemplateErrorContinuesToNextPhrase(t *testing.T) {
	s := fakeStages()
	s.Settings = func() PhraseSettings {
		return PhraseSettings{SourceLang: "ja", TargetLang: "en", Template: `{{if eq .Original "2"}}partial {{index .Translation 100}}{{else}}{{.Translation}}{{end}}`}
	}
	i := 0
	s.Recognize = func(context.Context, []float32) (string, error) { i++; return fmt.Sprint(i), nil }
	s.Send = func(_ context.Context, format string, message chatbox.Message, sent func(string)) error {
		pages, err := chatbox.Render(format, message)
		for _, page := range pages {
			sent(page)
		}
		return err
	}
	var mu sync.Mutex
	phrases := make(map[uint64][]Event)
	s.Emit = func(e Event) {
		if e.Kind != "speech" {
			mu.Lock()
			defer mu.Unlock()
			phrases[e.PhraseID] = append(phrases[e.PhraseID], e)
		}
	}
	if err := Run(t.Context(), s); err != nil {
		t.Fatal("template error stopped the flow", err)
	}
	if len(phrases) != 3 {
		t.Fatal(phrases)
	}
	for id, events := range phrases {
		var kinds []string
		for _, e := range events {
			kinds = append(kinds, e.Kind)
			if e.PhraseID != id || e.SourceLang != "ja" || e.TargetLang != "en" {
				t.Fatal("error lost phrase identity or languages", e)
			}
		}
		want := []string{"recognized", "translated", "page", "sent"}
		if events[0].Text == "2" {
			want = []string{"recognized", "translated", "error"}
			if !strings.Contains(events[2].Text, "index") {
				t.Fatal("missing template error text", events[2])
			}
		}
		if !reflect.DeepEqual(kinds, want) {
			t.Fatalf("phrase %d: %v, want %v", id, kinds, want)
		}
	}
}

func TestFailureCancelsCapture(t *testing.T) {
	for _, stage := range []string{"vad", "recognize", "translate", "send"} {
		t.Run(stage, func(t *testing.T) {
			s := fakeStages()
			want := errors.New(stage)
			switch stage {
			case "vad":
				s.Probability = func(context.Context, []float32) (float32, error) { return 0, want }
			case "recognize":
				s.Recognize = func(context.Context, []float32) (string, error) { return "", want }
			case "translate":
				s.Translate = func(context.Context, string, string, string) (string, error) { return "", want }
			case "send":
				s.Send = func(context.Context, string, chatbox.Message, func(string)) error { return want }
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := Run(ctx, s); !errors.Is(err, want) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestCancellationAndFinalWindow(t *testing.T) {
	s := fakeStages()
	padded := false
	recognized := false
	s.Source = sourceFunc(func(_ context.Context, f func([]float32) error) error {
		x := make([]float32, 512*8+1)
		for i := range x {
			x[i] = 1
		}
		return f(x)
	})
	s.Probability = func(_ context.Context, x []float32) (float32, error) {
		if x[0] == 1 && x[1] == 0 {
			padded = true
		}
		return 1, nil
	}
	s.Recognize = func(context.Context, []float32) (string, error) { recognized = true; return "", nil }
	if err := Run(context.Background(), s); err != nil || !padded || !recognized {
		t.Fatalf("%v padded=%v recognized=%v", err, padded, recognized)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, s); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
