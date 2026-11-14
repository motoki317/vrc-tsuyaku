package pipeline

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

type timedEvent struct {
	Event
	at time.Duration
}

type eventLog struct {
	mu      sync.Mutex
	entries []timedEvent
	start   time.Time
}

func (l *eventLog) emit(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, timedEvent{e, time.Since(l.start)})
}

func (l *eventLog) ofKind(kind string) []timedEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	var result []timedEvent
	for _, e := range l.entries {
		if e.Kind == kind {
			result = append(result, e)
		}
	}
	return result
}

func (l *eventLog) phraseKinds(id uint64) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var kinds []string
	for _, e := range l.entries {
		if e.PhraseID == id {
			kinds = append(kinds, e.Kind)
		}
	}
	return kinds
}

func timedStages(originals []string, interval time.Duration) Stages {
	s := fakeStages()
	s.Source = fakeSource(len(originals))
	i := 0
	s.Recognize = func(context.Context, []float32) (string, error) { text := originals[i]; i++; return text, nil }
	translated := 0
	s.Translate = func(_ context.Context, _, _, text string) (string, error) {
		if translated > 0 {
			time.Sleep(interval)
		}
		translated++
		return "translated " + text, nil
	}
	return s
}

func TestMergeChainEvents(t *testing.T) {
	for _, mode := range []string{"with Send", "without Send"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := timedStages([]string{"a", "b", "c"}, time.Second)
				start := time.Now()
				r := displayRecord{start: start}
				if mode == "with Send" {
					s.Send = r.send
				}
				log := eventLog{start: start}
				s.Emit = log.emit
				if err := Run(t.Context(), s); err != nil {
					t.Fatal(err)
				}
				ids, merges := log.ofKind("recognized"), log.ofKind("merged")
				if len(ids) != 3 || len(merges) != 2 || time.Since(start) != 2*time.Second {
					t.Fatalf("recognized = %v, merged = %v, elapsed = %s", ids, merges, time.Since(start))
				}
				for i, original := range []string{"a b", "a b c"} {
					text := []string{"translated a translated b", "translated a translated b translated c"}[i]
					want := Event{PhraseID: ids[i+1].PhraseID, Into: ids[0].PhraseID, Kind: "merged", Original: original, Text: text, SourceLang: "ja", TargetLang: "en"}
					if merges[i].Event != want || merges[i].at != time.Duration(i+1)*time.Second {
						t.Fatalf("merged = %+v, want %+v at %ds", merges[i], want, i+1)
					}
				}
				for i, recognized := range ids {
					want := []string{"recognized", "translated"}
					if s.Send != nil {
						want = append(want, "page")
					}
					if i > 0 {
						want = append(want, "merged")
					}
					if s.Send != nil {
						want = append(want, "sent")
					}
					if got := log.phraseKinds(recognized.PhraseID); !reflect.DeepEqual(got, want) {
						t.Fatalf("phrase %d events = %v, want %v", i, got, want)
					}
				}
				if s.Send != nil {
					pages := []string{"[ja] a\n[en] translated a", "[ja] a b\n[en] translated a translated b", "[ja] a b c\n[en] translated a translated b translated c"}
					r.check(t, pages, []time.Duration{0, time.Second, 2 * time.Second})
					for i, e := range log.ofKind("page") {
						if e.Text != pages[i] {
							t.Fatalf("page event text = %q, want %q", e.Text, pages[i])
						}
					}
				}
			})
		})
	}
}

func TestWithoutSendRenderFailuresAndEmptyPages(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		wantMerges int
	}{
		{"NUL", "bad\x00text", 1},
		{"empty", "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := timedStages([]string{"a", "bad", "c", "d"}, 0)
				s.Settings = func() PhraseSettings {
					return PhraseSettings{SourceLang: "ja", TargetLang: "en", Template: "{{.Translation}}"}
				}
				s.Translate = func(_ context.Context, _, _, text string) (string, error) {
					if text == "bad" {
						return tc.text, nil
					}
					return text, nil
				}
				log := eventLog{start: time.Now()}
				s.Emit = log.emit
				if err := Run(t.Context(), s); err != nil {
					t.Fatal(err)
				}
				ids, merges := log.ofKind("recognized"), log.ofKind("merged")
				if len(ids) != 4 || len(merges) != tc.wantMerges || time.Since(log.start) != 0 {
					t.Fatalf("recognized = %v, merged = %v, elapsed = %s", ids, merges, time.Since(log.start))
				}
				for _, kind := range []string{"error", "page", "sent"} {
					if got := log.ofKind(kind); len(got) != 0 {
						t.Fatalf("unexpected %s: %v", kind, got)
					}
				}
				for i := range 2 {
					if kinds := log.phraseKinds(ids[i].PhraseID); !reflect.DeepEqual(kinds, []string{"recognized", "translated"}) {
						t.Fatal(kinds)
					}
				}
				want := Event{PhraseID: ids[3].PhraseID, Into: ids[2].PhraseID, Kind: "merged", Original: "c d", Text: "c d", SourceLang: "ja", TargetLang: "en"}
				if tc.wantMerges == 2 {
					first := Event{PhraseID: ids[2].PhraseID, Into: ids[0].PhraseID, Kind: "merged", Original: "a c", Text: "a c", SourceLang: "ja", TargetLang: "en"}
					if merges[0].Event != first {
						t.Fatalf("first merge = %+v, want %+v", merges[0], first)
					}
					want.Into, want.Original, want.Text = ids[0].PhraseID, "a c d", "a c d"
				}
				if got := merges[len(merges)-1].Event; got != want {
					t.Fatalf("last merge = %+v, want %+v", got, want)
				}
			})
		})
	}
}

func TestWithoutSendReadingTimeStartsAtDisplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := eventLog{start: time.Now()}
		d := display{emit: log.emit}
		if err := d.show(t.Context(), 1, "{{.Original}}", chatbox.Message{Original: "a"}, time.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		if err := d.show(t.Context(), 2, "{{.Original}}", chatbox.Message{Original: "b"}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := log.ofKind("merged"); len(got) != 1 || got[0].Into != 1 || got[0].Original != "a b" || got[0].at != time.Second {
			t.Fatal(got)
		}
	})
}

func TestWithoutSendEmptyRenderKeepsReadingTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := eventLog{start: time.Now()}
		d := display{emit: log.emit}
		for i, text := range []string{"a", " \t\n", "c"} {
			if i > 0 {
				time.Sleep(time.Second)
			}
			if err := d.show(t.Context(), uint64(i+1), "{{.Original}}", chatbox.Message{Original: text}, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		if len(log.entries) != 0 || time.Since(log.start) != 2*time.Second {
			t.Fatalf("events = %+v, elapsed = %s", log.entries, time.Since(log.start))
		}
	})
}

func TestFailedMergeWriteEmitsNoMerge(t *testing.T) {
	for _, mode := range []string{"cancel", "write error"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := timedStages([]string{"a", "b"}, 0)
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				want := errors.New("merge write stopped")
				release := make(chan struct{})
				attempts := 0
				s.Send = func(ctx context.Context, _ string) error {
					attempts++
					if attempts == 1 {
						return nil
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-release:
						return errcode.With(errcode.ChatboxSend, want)
					}
				}
				log := eventLog{start: time.Now()}
				s.Emit = log.emit
				done := make(chan error, 1)
				go func() { done <- Run(ctx, s) }()
				synctest.Wait()
				if attempts != 2 || len(log.ofKind("merged")) != 0 {
					t.Fatalf("attempts = %d, merged = %v", attempts, log.ofKind("merged"))
				}
				if mode == "cancel" {
					cancel(want)
				} else {
					close(release)
				}
				if err := <-done; !errors.Is(err, want) {
					t.Fatal(err)
				}
				ids := log.ofKind("recognized")
				if len(ids) != 2 || len(log.ofKind("merged")) != 0 || len(log.ofKind("page")) != 1 || len(log.ofKind("sent")) != 1 {
					t.Fatal(log.entries)
				}
				kinds := []string{"recognized", "translated"}
				if mode == "write error" {
					kinds = append(kinds, "error")
					if e := log.ofKind("error"); len(e) != 1 || e[0].PhraseID != ids[1].PhraseID || e[0].Code != errcode.ChatboxSend {
						t.Fatal(e)
					}
				}
				if got := log.phraseKinds(ids[1].PhraseID); !reflect.DeepEqual(got, kinds) {
					t.Fatalf("joining phrase events = %v, want %v", got, kinds)
				}
			})
		})
	}
}

func TestSuccessfulMergeEventsSurviveCancellation(t *testing.T) {
	for _, stopAt := range []string{"write", "page", "merged"} {
		t.Run(stopAt, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := timedStages([]string{"a", "b"}, 0)
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				want := errors.New("stop after successful write")
				writes := 0
				s.Send = func(context.Context, string) error {
					writes++
					if writes == 2 && stopAt == "write" {
						cancel(want)
					}
					return nil
				}
				log := eventLog{start: time.Now()}
				s.Emit = func(e Event) {
					log.emit(e)
					if e.Kind == stopAt && (e.Kind == "page" || e.Kind == "merged") && writes == 2 {
						cancel(want)
					}
				}
				if err := Run(ctx, s); !errors.Is(err, want) {
					t.Fatal(err)
				}
				ids := log.ofKind("recognized")
				if len(ids) != 2 || writes != 2 {
					t.Fatalf("recognized = %v, writes = %d", ids, writes)
				}
				if got := log.phraseKinds(ids[1].PhraseID); !reflect.DeepEqual(got, []string{"recognized", "translated", "page", "merged", "sent"}) {
					t.Fatal(got)
				}
			})
		})
	}
}
