package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

func TestLanguageChangesReachRunningPipeline(t *testing.T) {
	type pair struct{ source, target string }
	for _, tc := range []struct {
		name, flow, spoken string
		changes            []pair
	}{
		{"self targets", "self", "ja", []pair{{"ja", "en"}, {"ja", "zh-hans"}, {"ja", "ja-easy"}}},
		{"world targets", "world", "en", []pair{{"en", "ja"}, {"en", "zh-hans"}, {"en", "ja-easy"}}},
		{"world v3 sources", "world", "de", []pair{{"de", "ja"}, {"fr", "ja"}, {"auto", "ja"}, {"pt", "ja"}, {"sv", "ja"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flow := tc.flow
			a := testApp(t)
			if flow == "world" {
				a.settings.Flows.World.Spoken = tc.spoken
			}
			for id, artifact := range a.artifacts {
				artifact.State = "installed"
				a.artifacts[id] = artifact
			}
			a.settings.Flows.Self.Microphone = "synthetic"
			a.settings.Chatbox.Address = "127.0.0.1:47123"
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			a.translator, err = translate.New(executable, "unused-model")
			if err != nil {
				t.Fatal(err)
			}
			if err := a.translator.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			input := make(chan struct{})
			type translationRequest struct{ source, target, phrase string }
			requests := make(chan translationRequest, 1)
			a.prepare = func(context.Context, string, config.Settings) (pipeline.Stages, error) {
				return pipeline.Stages{
					Source: recordingSource(func(ctx context.Context, consume func([]float32) error) error {
						for {
							select {
							case <-ctx.Done():
								return ctx.Err()
							case <-input:
								samples := make([]float32, 512*24)
								for i := range 512 * 8 {
									samples[i] = 1
								}
								if err := consume(samples); err != nil {
									return err
								}
							}
						}
					}),
					Probability: func(_ context.Context, samples []float32) (float32, error) { return samples[0], nil },
					Recognize:   func(context.Context, []float32) (string, error) { return "original", nil },
					Translate: func(_ context.Context, source, target, phrase string) (string, error) {
						requests <- translationRequest{source, target, phrase}
						return "translation", nil
					},
				}, nil
			}
			if err := a.changeFlow(flow, "start"); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.flows[flow].status.State == "running" })
			a.mu.Lock()
			run := a.flows[flow]
			s := cloneSettings(a.settings)
			a.mu.Unlock()
			for i, change := range tc.changes {
				source, target := change.source, change.target
				if flow == "self" {
					s.Flows.Self.Spoken, s.Flows.Self.Target = source, target
				} else {
					s.Flows.World.Spoken, s.Flows.World.Target = source, target
				}
				body, _ := json.Marshal(s)
				if w := request(a, "PUT", "/api/settings", string(body), a.token); w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
				input <- struct{}{}
				got := <-requests
				if got != (translationRequest{catalog.TranslatorSource(source), target, "original"}) {
					t.Fatal(got)
				}
				waitFor(t, func() bool {
					history, _ := a.events.after(0)
					count := 0
					for _, e := range history {
						if e.Kind == "translated" {
							count++
						}
					}
					return count == i+1
				})
				a.mu.Lock()
				unchanged := a.flows[flow] == run && run.status.State == "running"
				a.mu.Unlock()
				if !unchanged {
					t.Fatal("change within a recognizer restarted the flow")
				}
				saved, err := config.Load(a.dir)
				if err != nil || !reflect.DeepEqual(saved, s) {
					t.Fatal(saved, err)
				}
			}
			history, _ := a.events.after(0)
			pairs := make(map[uint64]pair)
			for _, e := range history {
				if e.Kind == "recognized" {
					pairs[e.PhraseID] = pair{e.SourceLang, e.TargetLang}
				}
				if e.PhraseID != 0 && (pair{e.SourceLang, e.TargetLang} != pairs[e.PhraseID]) {
					t.Fatal("phrase lost its language pair", e)
				}
			}
		})
	}
}

func TestSpokenChangeStopsBeforePublishingAndNeedsDownload(t *testing.T) {
	for _, tc := range []struct{ flow, initial, spoken, missing string }{
		{"self", "ja", "en", catalog.Parakeet},
		{"world", "en", "de", catalog.ParakeetV3},
		{"world", "de", "en", catalog.Parakeet},
	} {
		t.Run(tc.flow+"/"+tc.initial+"/"+tc.spoken, func(t *testing.T) {
			a := testApp(t)
			if tc.flow == "world" {
				a.settings.Flows.World.Spoken = tc.initial
			}
			a.settings.Flows.Self.Microphone = "synthetic"
			a.settings.Chatbox.Address = "127.0.0.1:47123"
			for id, artifact := range a.artifacts {
				if id != tc.missing {
					artifact.State = "installed"
				}
				a.artifacts[id] = artifact
			}
			ready := make(chan struct{})
			a.prepare = func(ctx context.Context, name string, s config.Settings) (pipeline.Stages, error) {
				close(ready)
				<-ctx.Done()
				a.mu.Lock()
				defer a.mu.Unlock()
				current, _ := a.settings.Flows.Languages(tc.flow)
				before, _ := s.Flows.Languages(tc.flow)
				if current != before {
					t.Error("published new language before old recognizer stopped")
				}
				return pipeline.Stages{}, ctx.Err()
			}
			if err := a.changeFlow(tc.flow, "start"); err != nil {
				t.Fatal(err)
			}
			<-ready
			s := cloneSettings(a.settings)
			if tc.flow == "self" {
				s.Flows.Self.Spoken = tc.spoken
			} else {
				s.Flows.World.Spoken = tc.spoken
			}
			body, _ := json.Marshal(s)
			w := request(a, "PUT", "/api/settings", string(body), a.token)
			if w.Code != http.StatusOK {
				t.Fatal(w.Code, w.Body.String())
			}
			a.mu.Lock()
			status := a.flows[tc.flow].status
			a.mu.Unlock()
			if status.State != "error" || !strings.Contains(status.Error, a.artifacts[tc.missing].Artifact.Name) {
				t.Fatal(status)
			}
			if w := request(a, "POST", "/api/flows/"+tc.flow+"/start", "", a.token); w.Code != http.StatusConflict {
				t.Fatal(w.Code, w.Body.String())
			}
			saved, err := config.Load(a.dir)
			if err != nil || !reflect.DeepEqual(saved, s) {
				t.Fatal(saved, err)
			}
		})
	}
}
