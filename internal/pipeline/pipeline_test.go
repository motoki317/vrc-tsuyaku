package pipeline

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/errcode"

	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/vad"
)

type sourceFunc func(context.Context, func([]float32) error) error

func (s sourceFunc) Capture(ctx context.Context, f func([]float32) error) error { return s(ctx, f) }

func fakeSource(phrases int) sourceFunc {
	return sourceFunc(func(ctx context.Context, consume func([]float32) error) error {
		for range phrases {
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
	})
}

func fakeStages() Stages {
	return Stages{
		Source:      fakeSource(3),
		Probability: func(_ context.Context, x []float32) (float32, error) { return x[0], nil },
		Recognize:   func(_ context.Context, _ []float32) (string, error) { return "text", nil },
		Settings: func() PhraseSettings {
			return PhraseSettings{SourceLang: "ja", TargetLang: "en", Template: chatbox.DefaultTemplate}
		},
		Translate: func(_ context.Context, _, _, s string) (string, error) { return "translated " + s, nil },
	}
}

func TestPhraseOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := fakeStages()
		s.Settings = func() PhraseSettings { return PhraseSettings{Template: "{{.Original}}:{{.Translation}}"} }
		var got []string
		i := 0
		s.Recognize = func(_ context.Context, x []float32) (string, error) {
			i++
			if len(x) < 512*24 {
				t.Error("phrase lost confirming silence")
			}
			return fmt.Sprint(i), nil
		}
		s.Send = func(_ context.Context, page string) error {
			got = append(got, page)
			return nil
		}
		var mu sync.Mutex
		counts := map[string]int{}
		s.Emit = func(e Event) { mu.Lock(); defer mu.Unlock(); counts[e.Kind]++ }
		if err := Run(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, []string{"1:translated 1", "1 2:translated 1 translated 2", "1 2 3:translated 1 translated 2 translated 3"}) {
			t.Fatal(got)
		}
		for _, kind := range []string{"recognized", "translated", "page", "sent"} {
			if counts[kind] != 3 {
				t.Fatal(counts)
			}
		}
	})
}

func TestSlowSendBuffersTranslations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := fakeStages()
		s.Settings = func() PhraseSettings { return PhraseSettings{Template: "{{.Original}}"} }
		s.Source = fakeSource(10)
		i := 0
		s.Recognize = func(context.Context, []float32) (string, error) {
			i++
			return fmt.Sprint(i), nil
		}
		release := make(chan struct{})
		var got []string
		s.Send = func(_ context.Context, page string) error {
			<-release
			got = append(got, page)
			return nil
		}
		translated := 0
		s.Emit = func(e Event) {
			if e.Kind == "translated" {
				translated++
			}
		}
		done := make(chan error, 1)
		go func() { done <- Run(t.Context(), s) }()
		synctest.Wait()
		if translated != 10 || len(got) != 0 {
			t.Fatalf("translated = %d, sent = %v", translated, got)
		}
		time.Sleep(time.Second)
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		want := []string{"1", "1 2", "1 2 3", "1 2 3 4", "1 2 3 4 5", "1 2 3 4 5 6", "1 2 3 4 5 6 7", "1 2 3 4 5 6 7 8", "1 2 3 4 5 6 7 8 9", "1 2 3 4 5 6 7 8 9 10"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("sent = %v, want %v", got, want)
		}
	})
}

func TestCancellationDiscardsBufferedTranslations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := fakeStages()
		s.Source = fakeSource(100)
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		sends := 0
		s.Send = func(ctx context.Context, _ string) error {
			sends++
			<-ctx.Done()
			return nil
		}
		translated := 0
		s.Emit = func(e Event) {
			if e.Kind == "translated" {
				translated++
			}
		}
		done := make(chan error, 1)
		go func() { done <- Run(ctx, s) }()
		synctest.Wait()
		if translated != 66 || sends != 1 {
			t.Fatalf("translated = %d, sends = %d, want 66 and 1: one in Send, 64 buffered and one blocked handoff", translated, sends)
		}
		want := errors.New("stop buffered send")
		cancel(want)
		if err := <-done; !errors.Is(err, want) || sends != 1 {
			t.Fatalf("Run = %v, sends = %d", err, sends)
		}
	})
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
	synctest.Test(t, func(t *testing.T) {
		seen := make(map[uint64]bool)
		for _, send := range []bool{true, false, true} {
			s := fakeStages()
			s.Translate = func(context.Context, string, string, string) (string, error) { return strings.Repeat("a", 145), nil }
			s.Settings = func() PhraseSettings {
				return PhraseSettings{SourceLang: "ja", TargetLang: "en", Template: "{{.Translation}}"}
			}
			var mu sync.Mutex
			var events []Event
			s.Emit = func(e Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) }
			if send {
				s.Send = func(context.Context, string) error { return nil }
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
				if e.Kind == "translated" && (e.Original != "text" || e.Text != strings.Repeat("a", 145)) {
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
	})
}

func TestLanguageChangesApplyToNextPhrase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		s.Send = func(context.Context, string) error { return nil }
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
			if len(events) != 4 && len(events) != 5 {
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
	})
}

func TestSpokenCodeSurvivesOmittedTranslationSource(t *testing.T) {
	for _, tc := range []struct{ spoken, source string }{
		{"ja", "ja"}, {"en", "en"}, {"de", "de"},
		{"auto", ""}, {"pt", ""}, {"sv", ""},
	} {
		t.Run(tc.spoken, func(t *testing.T) {
			s := fakeStages()
			s.Settings = func() PhraseSettings {
				return PhraseSettings{SourceLang: tc.spoken, TargetLang: "ja", Template: chatbox.DefaultTemplate}
			}
			translated, sent := 0, 0
			s.Translate = func(_ context.Context, source, target, phrase string) (string, error) {
				if source != tc.source || target != "ja" || phrase == "" {
					t.Errorf("translation request: %q, %q, %q", source, target, phrase)
				}
				translated++
				return "translation", nil
			}
			s.Send = func(_ context.Context, page string) error {
				if !strings.HasPrefix(page, "["+tc.spoken+"] ") || !strings.Contains(page, "\n[ja] ") {
					t.Errorf("chatbox page: %q", page)
				}
				sent++
				return nil
			}
			var eventsMu sync.Mutex
			var events []Event
			s.Emit = func(e Event) {
				if e.Kind == "speech" {
					return
				}
				eventsMu.Lock()
				defer eventsMu.Unlock()
				events = append(events, e)
			}
			if err := Run(t.Context(), s); err != nil {
				t.Fatal(err)
			}
			if translated != 3 || sent != 3 || len(events) != 14 {
				t.Fatal(translated, sent, events)
			}
			for _, e := range events {
				if e.SourceLang != tc.spoken || e.TargetLang != "ja" {
					t.Fatalf("event lost spoken code: %+v", e)
				}
			}
		})
	}
}

func TestSentWaitsForSuccessfulReturn(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := fakeStages()
				s.Settings = func() PhraseSettings {
					return PhraseSettings{SourceLang: "ja", TargetLang: "en", Template: "{{.Translation}}"}
				}
				s.Translate = func(context.Context, string, string, string) (string, error) { return strings.Repeat("a", 145), nil }
				var returned atomic.Bool
				var completed atomic.Int32
				var pageCount atomic.Int32
				want := errors.New("send failed after one page")
				sends := 0
				s.Send = func(context.Context, string) error {
					sends++
					returned.Store(false)
					defer returned.Store(true)
					if fail && sends == 2 {
						return want
					}
					return nil
				}
				s.Emit = func(e Event) {
					if e.Kind == "page" {
						if !returned.Load() {
							t.Error("reported page before successful Send return")
						}
						pageCount.Add(1)
					}
					if e.Kind == "sent" {
						if !returned.Load() || fail {
							t.Error("marked sent before successful Send return")
						}
						completed.Add(1)
					}
				}
				err := Run(t.Context(), s)
				if fail {
					if !errors.Is(err, want) || completed.Load() != 0 || pageCount.Load() != 1 {
						t.Fatal(err, completed.Load(), pageCount.Load())
					}
				} else if err != nil || completed.Load() != 3 || pageCount.Load() != 6 {
					t.Fatal(err, completed.Load(), pageCount.Load())
				}
			})
		})
	}
}

func TestTemplateChangesApplyToNextPhrase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		s.Send = func(_ context.Context, page string) error {
			got = append(got, page)
			return nil
		}
		if err := Run(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		want := []string{"[ja] original\n[en] translated original", "translated original", "translated original translated original"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("template changed mid-phrase: %q, want %q", got, want)
		}
	})
}

func TestTemplateErrorContinuesToNextPhrase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := fakeStages()
		s.Settings = func() PhraseSettings {
			return PhraseSettings{SourceLang: "ja", TargetLang: "en", Template: `{{if eq .Original "2"}}partial {{index .Translation 100}}{{else}}{{.Translation}}{{end}}`}
		}
		i := 0
		s.Recognize = func(context.Context, []float32) (string, error) { i++; return fmt.Sprint(i), nil }
		s.Send = func(context.Context, string) error { return nil }
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
			if events[0].Text == "3" {
				want = []string{"recognized", "translated", "page", "merged", "sent"}
			}
			if events[0].Text == "2" {
				want = []string{"recognized", "translated", "error"}
				if events[2].Code != errcode.Template || !strings.Contains(events[2].Text, "index") {
					t.Fatal("missing template error text", events[2])
				}
			}
			if !reflect.DeepEqual(kinds, want) {
				t.Fatalf("phrase %d: %v, want %v", id, kinds, want)
			}
		}
	})
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
				s.Send = func(context.Context, string) error { return want }
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

func TestRunLevelMeterAndFlushedGain(t *testing.T) {
	s := fakeStages()
	input := make([]float32, 8*vad.WindowSamples+1)
	for i := range input {
		input[i] = 0.001
	}
	s.Source = sourceFunc(func(_ context.Context, consume func([]float32) error) error {
		for start := 0; start < len(input); start += 317 {
			if err := consume(input[start:min(start+317, len(input))]); err != nil {
				return err
			}
		}
		return nil
	})
	reads, scores := 0, 0
	s.CutLevel = func() float64 { reads++; return -70 }
	var levels []float64
	s.Meter = func(db float64) { levels = append(levels, db) }
	s.Probability = func(_ context.Context, window []float32) (float32, error) {
		nearLevel(t, float64(window[0]), 0.01)
		if len(levels) != scores+1 {
			t.Error("meter readings lost window order", len(levels), scores)
		}
		scores++
		// A scorer's use of its copy must not change the segmenter's captured audio.
		clear(window)
		return 1, nil
	}
	var speech []string
	s.Emit = func(e Event) {
		if e.Kind == "speech" {
			speech = append(speech, e.Text)
		}
	}
	recognized := 0
	s.Recognize = func(_ context.Context, phrase []float32) (string, error) {
		recognized++
		if len(phrase) != 9*vad.WindowSamples {
			t.Error("flushed phrase length", len(phrase))
		}
		power, _ := measureLevel(phrase)
		nearLevel(t, powerDB(power), -25)
		gain := math.Pow(10, -25.0/20) * math.Sqrt(float64(len(phrase))/float64(len(input)))
		for i, sample := range phrase {
			want := 0.0
			if i < len(input) {
				want = gain
			}
			nearLevel(t, float64(sample), want)
		}
		return "", nil
	}
	if err := Run(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if reads != 9 || scores != 9 || len(levels) != 9 || recognized != 1 {
		t.Fatal("window or phrase counts", reads, scores, len(levels), recognized)
	}
	for _, db := range levels[:8] {
		nearLevel(t, db, -60)
	}
	nearLevel(t, levels[8], -60-10*math.Log10(vad.WindowSamples))
	if !reflect.DeepEqual(speech, []string{"start", "end"}) {
		t.Fatal("unbalanced flushed speech", speech)
	}
}

func TestRunManualGateChanges(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		loudWindows, windows   int
		initialCut, changedCut float64
		wantSpeech             []string
		wantEventWindows       []int
		wantPhraseWindows      []int
	}{
		{"closed", 0, 48, -50, -50, nil, nil, nil},
		{"lower cut", 0, 48, -50, -70, []string{"start", "end"}, []int{9, 48}, []int{48}},
		{"raise cut", 0, 48, -70, -50, []string{"start", "end"}, []int{1, 24}, []int{24}},
		{"speech stays above cut", 0, 48, -70, -65, []string{"start", "end"}, []int{1, 48}, []int{48}},
		{"hold", 8, 48, -50, -50, []string{"start", "end"}, []int{1, 40}, []int{40}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fakeStages()
			s.Source = sourceFunc(func(_ context.Context, consume func([]float32) error) error {
				for i := range tc.windows {
					db := -60.0
					if i < tc.loudWindows {
						db = -40
					}
					if err := consume(levelWindow(db)); err != nil {
						return err
					}
				}
				return nil
			})
			reads, scores := 0, 0
			s.CutLevel = func() float64 {
				reads++
				if reads <= 8 {
					return tc.initialCut
				}
				return tc.changedCut
			}
			s.Probability = func(context.Context, []float32) (float32, error) {
				scores++
				return 1, nil
			}
			var speech []string
			var eventWindows, phraseWindows []int
			s.Emit = func(e Event) {
				if e.Kind == "speech" {
					speech = append(speech, e.Text)
					eventWindows = append(eventWindows, scores)
				}
			}
			s.Recognize = func(_ context.Context, phrase []float32) (string, error) {
				phraseWindows = append(phraseWindows, len(phrase)/vad.WindowSamples)
				return "", nil
			}
			if err := Run(t.Context(), s); err != nil {
				t.Fatal(err)
			}
			if reads != tc.windows || scores != tc.windows {
				t.Fatal("gated windows still need a cut reading and a VAD score", reads, scores)
			}
			if !reflect.DeepEqual(speech, tc.wantSpeech) || !reflect.DeepEqual(eventWindows, tc.wantEventWindows) ||
				!reflect.DeepEqual(phraseWindows, tc.wantPhraseWindows) {
				t.Fatal("speech, event windows, phrase windows:", speech, eventWindows, phraseWindows)
			}
		})
	}
}

func TestRunAutomaticGainBoundaries(t *testing.T) {
	type block struct {
		windows       int
		db            float64
		probability   float32
		cut, wantGain float64
	}
	for _, tc := range []struct {
		name                 string
		blocks               []block
		phrases, speechPairs int
	}{
		{"normal end", []block{
			{8, -60, 1, 0, 20}, {16, math.Inf(-1), 0, 0, 20}, {8, -60, 1, 0, 20},
		}, 2, 2},
		{"dropped phrase clears learning", []block{
			{7, -60, 1, 0, 20}, {16, math.Inf(-1), 0, 0, 20},
			{8, -40, 1, 0, 20}, {16, math.Inf(-1), 0, 0, 20}, {8, -60, 1, 0, 0},
		}, 2, 3},
		{"forced cut clears learning", []block{
			{469, -60, 1, 0, 20}, {8, -40, 1, 0, 20},
			{16, math.Inf(-1), 0, 0, 20}, {8, -60, 1, 0, 10},
		}, 3, 3},
		{"manual mode keeps learning", []block{
			{8, -60, 1, -70, 20}, {16, math.Inf(-1), 0, -70, 20}, {8, -60, 1, 0, 20},
		}, 2, 2},
		{"only active high probability windows teach", []block{
			{8, -20, 0.4, 0, 20}, {8, -60, vad.StartProbability, 0, 20},
			{8, -20, 0.4, 0, 20}, {16, math.Inf(-1), 0, 0, 20}, {8, -60, 1, 0, 20},
		}, 2, 2},
		{"search after idle", []block{
			{8, -40, 1, 0, 20}, {16, math.Inf(-1), 0, 0, 20},
			{311, -70, 0, 0, 0}, {8, -70, 1, 0, 20},
		}, 2, 2},
		{"search after consecutive hints", []block{
			{8, -40, 1, 0, 20}, {16, math.Inf(-1), 0, 0, 20},
			{7, -70, 0.1, 0, 0}, {1, -70, 0.099, 0, 0},
			{8, -70, 0.1, 0, 0}, {8, -70, 1, 0, 20},
		}, 2, 2},
		{"closed manual gate gives no hints", []block{
			{8, -40, 1, 0, 20}, {16, math.Inf(-1), 0, 0, 20},
			{8, -70, 0.1, -50, 10}, {1, -70, 0, 0, 0},
		}, 1, 1},
		{"manual hints keep search state", []block{
			{8, -40, 1, 0, 20}, {16, math.Inf(-1), 0, 0, 20},
			{8, -70, 0.1, -80, 20}, {8, -70, 1, 0, 20},
		}, 2, 2},
		{"manual idle keeps search state", []block{
			{8, -40, 1, 0, 20}, {16, math.Inf(-1), 0, 0, 20},
			{311, -70, 0, -50, 10}, {8, -70, 1, 0, 20},
		}, 2, 2},
		{"manual gain overrides search", []block{
			{8, -40, 1, -50, 10}, {16, math.Inf(-1), 0, -50, 10}, {8, -60, 1, 0, 0},
		}, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 2 {
				s := fakeStages()
				var current block
				s.Source = sourceFunc(func(_ context.Context, consume func([]float32) error) error {
					for _, b := range tc.blocks {
						current = b
						for range b.windows {
							if err := consume(levelWindow(b.db)); err != nil {
								return err
							}
						}
					}
					return nil
				})
				reads, scores := 0, 0
				s.CutLevel = func() float64 { reads++; return current.cut }
				s.Probability = func(_ context.Context, window []float32) (float32, error) {
					scores++
					power, _ := measureLevel(window)
					nearLevel(t, powerDB(power), max(-240, current.db+current.wantGain))
					return current.probability, nil
				}
				var speech []string
				s.Emit = func(e Event) {
					if e.Kind == "speech" {
						speech = append(speech, e.Text)
					}
				}
				recognized := 0
				s.Recognize = func(context.Context, []float32) (string, error) { recognized++; return "", nil }
				if err := Run(t.Context(), s); err != nil {
					t.Fatal(err)
				}
				if reads != scores || recognized != tc.phrases || len(speech) != 2*tc.speechPairs {
					t.Fatal("cut reads, scores, recognized, speech:", reads, scores, recognized, speech)
				}
				for i, event := range speech {
					if event != []string{"start", "end"}[i%2] {
						t.Fatal("unbalanced speech", speech)
					}
				}
			}
		})
	}
}
