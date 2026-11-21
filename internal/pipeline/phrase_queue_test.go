package pipeline

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/vad"
)

// Phrase 1 uses the fewest whole windows that meet the VAD's 250 ms speech minimum.
const phraseNumberWindowOffset = (audio.SampleRate*250/1000 - 1) / vad.WindowSamples

func numberedPhrase(number, silence int) []float32 {
	return phraseAudio((phraseNumberWindowOffset+number)*vad.WindowSamples, silence)
}

func phraseNumber(samples []float32) int {
	voiced := 0
	for _, sample := range samples {
		if sample != 0 {
			voiced++
		}
	}
	return voiced/vad.WindowSamples - phraseNumberWindowOffset
}

func TestCaptureDropsOldestWaitingPhrases(t *testing.T) {
	for _, mode := range []struct {
		name        string
		send        bool
		punctuation bool
	}{
		{name: "with Send", send: true},
		{name: "without Send"},
		{name: "punctuation after skip", punctuation: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := fakeStages()
				log := eventLog{start: time.Now()}
				started := make(chan struct{})
				captured, release := make(chan struct{}), make(chan struct{})
				s.Source = sourceFunc(func(_ context.Context, consume func([]float32) error) error {
					defer close(captured)
					for i := 1; i <= 7; i++ {
						if err := consume(numberedPhrase(i, 16*vad.WindowSamples)); err != nil {
							return err
						}
						if i == 1 {
							<-started
						}
					}
					return nil
				})
				s.Recognize = func(_ context.Context, samples []float32, _ PhraseSettings) (string, error) {
					number := phraseNumber(samples)
					if number == 1 {
						close(started)
						<-release
					}
					if mode.punctuation && number == 6 {
						return "…", nil
					}
					return strconv.Itoa(number), nil
				}
				s.Translate = func(_ context.Context, text string, _ PhraseSettings) (string, error) {
					return text, nil
				}
				s.Settings = func() PhraseSettings {
					return PhraseSettings{Template: "{{.Original}}", SourceLang: "ja", TargetLang: "en"}
				}
				if mode.send {
					s.Send = func(context.Context, string) error { return nil }
				}
				s.Emit = log.emit
				done := make(chan error, 1)
				go func() { done <- Run(t.Context(), s) }()
				synctest.Wait()
				select {
				case <-captured:
				default:
					close(release)
					<-done
					t.Fatal("capture blocked while recognition waited")
				}
				if skips := log.ofKind("skipped"); len(skips) != 0 {
					t.Errorf("skips while phrase 1 was in recognition = %v, want none", skips)
				}
				close(release)
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				var order []string
				var lastID uint64
				for _, e := range log.entries {
					switch e.Kind {
					case KindRecognized:
						order = append(order, e.Text)
					case KindSkipped:
						order = append(order, "skipped")
						if e.Event != (Event{Kind: KindSkipped, PhraseID: e.PhraseID}) {
							t.Errorf("skip payload = %+v", e.Event)
						}
						if kinds := log.phraseKinds(e.PhraseID); !slices.Equal(kinds, []string{"skipped"}) {
							t.Errorf("events for skipped phrase %d = %v", e.PhraseID, kinds)
						}
					default:
						continue
					}
					if e.PhraseID <= lastID {
						t.Errorf("phrase ID %d did not advance after %d", e.PhraseID, lastID)
					}
					lastID = e.PhraseID
				}
				want := []string{"1", "skipped", "skipped", "skipped", "skipped"}
				if !mode.punctuation {
					want = append(want, "6")
				}
				want = append(want, "7")
				if !slices.Equal(order, want) {
					t.Errorf("recognized and skipped = %v, want %v", order, want)
				}
				merges := log.ofKind("merged")
				if mode.punctuation {
					if len(merges) != 0 {
						t.Errorf("merge crossed the skip after punctuation: %v", merges)
					}
				} else {
					recognized := log.ofKind("recognized")
					if len(recognized) != 3 || len(merges) != 1 || merges[0].Into != recognized[1].PhraseID || merges[0].Original != "6 7" || merges[0].Text != "6 7" {
						t.Errorf("only phrases 6 and 7 should join: recognized = %v, merged = %v", recognized, merges)
					}
				}
				if mode.send {
					pages := log.ofKind("page")
					if len(pages) != 3 || pages[1].Text != "6" {
						t.Errorf("phrase 6 should start a new page: %v", pages)
					}
				}
			})
		})
	}
}

func TestWaitingPhrasesAtEndOfInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		silence int
		cancel  bool
	}{
		{name: "flush active phrase"},
		{name: "pad confirming silence", silence: 16*vad.WindowSamples - 1},
		{name: "cancel before flush", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				started, captured, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				s := fakeStages()
				finished := 3
				if tc.cancel {
					finished++
				}
				s.Source = sourceFunc(func(_ context.Context, consume func([]float32) error) error {
					defer close(captured)
					for i := 1; i <= finished; i++ {
						if err := consume(numberedPhrase(i, 16*vad.WindowSamples)); err != nil {
							return err
						}
						if i == 1 {
							<-started
						}
					}
					return consume(numberedPhrase(finished+1, tc.silence))
				})
				paddedWindows := 0
				s.Meter = func(float64) {
					select {
					case <-captured:
						paddedWindows++
					default:
					}
				}
				var recognized []int
				s.Recognize = func(_ context.Context, samples []float32, _ PhraseSettings) (string, error) {
					number := phraseNumber(samples)
					recognized = append(recognized, number)
					if len(recognized) == 1 {
						close(started)
						<-release
						if tc.cancel {
							// An empty transcript lets recognition reach the next take even after cancellation.
							return "", nil
						}
					}
					return strconv.Itoa(number), nil
				}
				log := eventLog{start: time.Now()}
				s.Emit = log.emit
				done := make(chan error, 1)
				go func() { done <- Run(ctx, s) }()
				<-captured
				synctest.Wait()
				if tc.silence != 0 && paddedWindows != 1 {
					t.Errorf("windows scored after capture while the queue was full = %d, want 1", paddedWindows)
				}
				var wantErr error
				want := []int{1, 2, 3, 4}
				if tc.cancel {
					wantErr = errors.New("stop with waiting phrases")
					want = []int{1}
					cancel(wantErr)
				}
				close(release)
				if err := <-done; !errors.Is(err, wantErr) {
					t.Fatalf("Run = %v, want %v", err, wantErr)
				}
				if !slices.Equal(recognized, want) || len(log.ofKind("skipped")) != 0 || len(log.ofKind("speech")) != 2*(finished+1) {
					t.Fatalf("recognitions = %v, want %v, events = %v", recognized, want, log.entries)
				}
			})
		})
	}
}
