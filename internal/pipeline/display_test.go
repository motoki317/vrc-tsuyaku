package pipeline

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
)

type displayRecord struct {
	pages []string
	times []time.Duration
	start time.Time
}

func (r *displayRecord) send(_ context.Context, page string) error {
	r.pages = append(r.pages, page)
	r.times = append(r.times, time.Since(r.start))
	return nil
}

func (r *displayRecord) check(t *testing.T, pages []string, times []time.Duration) {
	t.Helper()
	if !reflect.DeepEqual(r.pages, pages) || !reflect.DeepEqual(r.times, times) {
		t.Fatalf("writes = %q at %v, want %q at %v", r.pages, r.times, pages, times)
	}
}

func TestDisplayReadingTime(t *testing.T) {
	for _, tc := range []struct {
		name, first, second string
		pages               []string
		times               []time.Duration
	}{
		{"long page", strings.Repeat("猫", 100), strings.Repeat("b", 100), []string{strings.Repeat("猫", 100), strings.Repeat("b", 100)}, []time.Duration{0, 12 * time.Second}},
		{"two pages", strings.Repeat("猫", 145), "", []string{strings.Repeat("猫", 144), "猫"}, []time.Duration{0, 17280 * time.Millisecond}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := displayRecord{start: time.Now()}
				d := display{send: r.send, emit: func(Event) {}}
				for _, text := range []string{tc.first, tc.second} {
					if err := d.show(t.Context(), 1, "{{.Original}}", chatbox.Message{Original: text}, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				r.check(t, tc.pages, tc.times)
			})
		})
	}
}

func TestDisplayMergeAndChain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := displayRecord{start: time.Now()}
		d := display{send: r.send, emit: func(Event) {}}
		a, last := strings.Repeat("猫", 100), strings.Repeat("d", 100)
		for i, text := range []string{a, "hm", "c", last} {
			if i == 1 || i == 2 {
				time.Sleep(time.Second)
			}
			if err := d.show(t.Context(), 1, "{{.Original}}", chatbox.Message{Original: text}, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		r.check(t, []string{a, a + " hm", a + " hm c", last}, []time.Duration{0, time.Second, 2 * time.Second, 12600 * time.Millisecond})
	})
}

func TestDisplayDoesNotMerge(t *testing.T) {
	a := strings.Repeat("a", 100)
	for _, tc := range []struct {
		name, first, second, format string
		firstLang, secondLang       chatbox.Message
		idle, want                  time.Duration
	}{
		{name: "reading ended", first: a, second: "b", idle: 12 * time.Second, want: 12 * time.Second},
		{name: "template differs", first: a, second: "b", format: "{{.Translation}}", want: 12 * time.Second},
		{name: "source differs", first: a, second: "b", firstLang: chatbox.Message{SourceLang: "ja"}, secondLang: chatbox.Message{SourceLang: "en"}, want: 12 * time.Second},
		{name: "target differs", first: a, second: "b", firstLang: chatbox.Message{TargetLang: "ja"}, secondLang: chatbox.Message{TargetLang: "en"}, want: 12 * time.Second},
		{name: "joined needs two pages", first: a, second: a, want: 12 * time.Second},
		{name: "previous has two pages", first: strings.Repeat("a", 145), second: "b", want: 18780 * time.Millisecond},
	} {
		for _, mode := range []string{"with Send", "without Send"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					r := displayRecord{start: time.Now()}
					log := eventLog{start: r.start}
					d := display{emit: log.emit}
					if mode == "with Send" {
						d.send = r.send
					}
					first, second := tc.firstLang, tc.secondLang
					first.Original = tc.first
					second.Original, second.Translation = tc.second, tc.second
					if err := d.show(t.Context(), 1, "{{.Original}}", first, time.Now()); err != nil {
						t.Fatal(err)
					}
					time.Sleep(tc.idle)
					format := tc.format
					if format == "" {
						format = "{{.Original}}"
					}
					if err := d.show(t.Context(), 2, format, second, time.Now()); err != nil {
						t.Fatal(err)
					}
					if merges := log.ofKind("merged"); len(merges) != 0 {
						t.Fatal(merges)
					}
					if d.send == nil {
						if time.Since(r.start) != tc.idle {
							t.Fatalf("flow without Send waited %s, want %s", time.Since(r.start), tc.idle)
						}
						return
					}
					if got, at := r.pages[len(r.pages)-1], r.times[len(r.times)-1]; got != tc.second || at != tc.want {
						t.Fatalf("last page = %q at %s, want %q at %s", got, at, tc.second, tc.want)
					}
				})
			})
		}
	}
}

func TestDisplayFailedJoinedRender(t *testing.T) {
	for _, joined := range []string{`{{index .Translation 100}}`, " \t\n"} {
		t.Run(joined, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := displayRecord{start: time.Now()}
				d := display{send: r.send, emit: func(Event) {}}
				a := strings.Repeat("a", 100)
				format := `{{if gt (len .Original) 100}}` + joined + `{{else}}{{.Original}}{{end}}`
				for _, text := range []string{a, "b"} {
					if err := d.show(t.Context(), 1, format, chatbox.Message{Original: text}, time.Now()); err != nil {
						t.Fatalf("joined render failure became a template error: %v", err)
					}
				}
				r.check(t, []string{a, "b"}, []time.Duration{0, 12 * time.Second})
			})
		})
	}
}

func TestDisplayMaximumWait(t *testing.T) {
	for _, tc := range []struct {
		name   string
		second string
		pages  []string
		times  []time.Duration
	}{
		{"one page", strings.Repeat("b", 100), []string{strings.Repeat("a", 144), strings.Repeat("b", 100)}, []time.Duration{0, 0}},
		{"two pages", strings.Repeat("b", 154), []string{strings.Repeat("a", 144), strings.Repeat("b", 144), strings.Repeat("b", 10)}, []time.Duration{0, 0, 17280 * time.Millisecond}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := displayRecord{start: time.Now()}
				d := display{send: r.send, emit: func(Event) {}}
				if err := d.show(t.Context(), 1, "{{.Original}}", chatbox.Message{Original: strings.Repeat("a", 144)}, time.Now()); err != nil {
					t.Fatal(err)
				}
				if err := d.show(t.Context(), 1, "{{.Original}}", chatbox.Message{Original: tc.second}, r.start.Add(-17280*time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				r.check(t, tc.pages, tc.times)
			})
		})
	}
}

func TestDisplayBacklog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := fakeStages()
		s.Source = fakeSource(20)
		s.Settings = func() PhraseSettings { return PhraseSettings{Template: "{{.Translation}}"} }
		s.Translate = func(context.Context, string, string, string) (string, error) {
			time.Sleep(8 * time.Second)
			return strings.Repeat("a", 154), nil
		}
		r := displayRecord{start: time.Now().Add(8 * time.Second)}
		s.Send = r.send
		if err := Run(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		if len(r.pages) != 40 {
			t.Fatalf("pages = %d, want 40", len(r.pages))
		}
		for i := range 20 {
			if delay := r.times[2*i] - time.Duration(i)*8*time.Second; delay < 0 || delay > 26560*time.Millisecond {
				t.Fatalf("message %d delayed %s", i, delay)
			}
		}
	})
}

func TestDisplaySkippedPhrasePreservesMessage(t *testing.T) {
	for _, skipped := range []string{"bad", " \t\n"} {
		t.Run(skipped, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a, last := strings.Repeat("a", 100), strings.Repeat("z", 100)
				originals := []string{a, skipped, "b", last}
				s := timedStages(originals, 0)
				s.Settings = func() PhraseSettings {
					return PhraseSettings{Template: `{{if eq .Original "bad"}}{{index .Translation 1000}}{{else}}{{.Original}}{{end}}`}
				}
				r := displayRecord{start: time.Now()}
				s.Send = r.send
				log := eventLog{start: r.start}
				s.Emit = log.emit
				if err := Run(t.Context(), s); err != nil {
					t.Fatal(err)
				}
				r.check(t, []string{a, a + " b", last}, []time.Duration{0, 0, 12240 * time.Millisecond})
				ids := log.ofKind("recognized")
				if len(ids) != len(originals) {
					t.Fatal(ids)
				}
				want := []string{"recognized", "translated", "sent"}
				if skipped == "bad" {
					want[2] = "error"
				}
				if kinds := log.phraseKinds(ids[1].PhraseID); !reflect.DeepEqual(kinds, want) {
					t.Fatalf("skipped phrase events = %v, want %v", kinds, want)
				}
			})
		})
	}
}

func TestDisplayCancellationDuringReadingTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := fakeStages()
		s.Source = fakeSource(2)
		s.Settings = func() PhraseSettings { return PhraseSettings{Template: "{{.Translation}}"} }
		s.Translate = func(context.Context, string, string, string) (string, error) { return strings.Repeat("a", 100), nil }
		r := displayRecord{start: time.Now()}
		s.Send = r.send
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		log := eventLog{start: r.start}
		s.Emit = log.emit
		done := make(chan error, 1)
		go func() { done <- Run(ctx, s) }()
		synctest.Wait()
		time.Sleep(time.Second)
		want := errors.New("stop reading wait")
		cancel(want)
		if err := <-done; !errors.Is(err, want) {
			t.Fatal(err)
		}
		r.check(t, []string{strings.Repeat("a", 100)}, []time.Duration{0})
		ids := log.ofKind("recognized")
		if len(ids) != 2 {
			t.Fatal(ids)
		}
		for i, id := range ids {
			want := []string{"recognized", "translated"}
			if i == 0 {
				want = append(want, "page", "sent")
			}
			if kinds := log.phraseKinds(id.PhraseID); !reflect.DeepEqual(kinds, want) {
				t.Fatalf("phrase %d events = %v, want %v", i, kinds, want)
			}
		}
	})
}

func TestDisplayStateEndsWithRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := displayRecord{start: time.Now()}
		for _, text := range []string{strings.Repeat("a", 100), "b"} {
			s := fakeStages()
			s.Source = fakeSource(1)
			s.Settings = func() PhraseSettings { return PhraseSettings{Template: "{{.Translation}}"} }
			s.Translate = func(context.Context, string, string, string) (string, error) { return text, nil }
			s.Send = r.send
			if err := Run(t.Context(), s); err != nil {
				t.Fatal(err)
			}
		}
		r.check(t, []string{strings.Repeat("a", 100), "b"}, []time.Duration{0, 0})
	})
}
