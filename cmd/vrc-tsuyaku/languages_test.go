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

func TestTargetChangesReachRunningPipeline(t *testing.T) {
	for _, flow := range []string{"self", "world"} {
		t.Run(flow, func(t *testing.T) {
			a := testApp(t)
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
			source, initialTarget := s.Flows.Languages(flow)
			for i, target := range []string{initialTarget, "zh-hans", "ja-easy"} {
				if flow == "self" {
					s.Flows.Self.Target = target
				} else {
					s.Flows.World.Target = target
				}
				body, _ := json.Marshal(s)
				if w := request(a, "PUT", "/api/settings", string(body), a.token); w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
				input <- struct{}{}
				got := <-requests
				if got != (translationRequest{source, target, "original"}) {
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
					t.Fatal("target change restarted the flow")
				}
				saved, err := config.Load(a.dir)
				if err != nil || !reflect.DeepEqual(saved, s) {
					t.Fatal(saved, err)
				}
			}
			history, _ := a.events.after(0)
			pairs := make(map[uint64]string)
			for _, e := range history {
				if e.Kind == "recognized" {
					pairs[e.PhraseID] = e.TargetLang
				}
				if e.PhraseID != 0 && (e.SourceLang != source || e.TargetLang != pairs[e.PhraseID]) {
					t.Fatal("phrase lost its language pair", e)
				}
			}
		})
	}
}

func TestSpokenChangeStopsBeforePublishingAndNeedsDownload(t *testing.T) {
	a := testApp(t)
	a.settings.Flows.Self.Microphone = "synthetic"
	a.settings.Chatbox.Address = "127.0.0.1:47123"
	for id, artifact := range a.artifacts {
		if id != catalog.Parakeet {
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
		if a.settings.Flows.Self.Spoken != s.Flows.Self.Spoken {
			t.Error("published new language before old recognizer stopped")
		}
		return pipeline.Stages{}, ctx.Err()
	}
	if err := a.changeFlow("self", "start"); err != nil {
		t.Fatal(err)
	}
	<-ready
	s := cloneSettings(a.settings)
	s.Flows.Self.Spoken = "en"
	body, _ := json.Marshal(s)
	w := request(a, "PUT", "/api/settings", string(body), a.token)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	a.mu.Lock()
	status := a.flows["self"].status
	a.mu.Unlock()
	if status.State != "error" || !strings.Contains(status.Error, a.artifacts[catalog.Parakeet].Artifact.Name) {
		t.Fatal(status)
	}
	if w := request(a, "POST", "/api/flows/self/start", "", a.token); w.Code != http.StatusConflict {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, err := config.Load(a.dir)
	if err != nil || !reflect.DeepEqual(saved, s) {
		t.Fatal(saved, err)
	}
}
