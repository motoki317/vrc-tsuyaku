package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
)

func TestGlossaryChangesReachBothRunningFlows(t *testing.T) {
	a := testApp(t)
	for id, artifact := range a.artifacts {
		artifact.State = "installed"
		a.artifacts[id] = artifact
	}
	a.translator = testTranslator(t)
	a.settings.Flows.Self.Microphone = "synthetic"
	a.settings.Flows.World.Target = "ja-easy"
	inputs := map[string]chan struct{}{"self": make(chan struct{}), "world": make(chan struct{})}
	type call struct {
		flow, text string
		settings   pipeline.PhraseSettings
	}
	recognitions, translations := make(chan call, 1), make(chan call, 1)
	a.prepare = func(_ context.Context, name string, _ config.Settings) (pipeline.Stages, error) {
		return pipeline.Stages{
			Source: recordingSource(func(ctx context.Context, consume func([]float32) error) error {
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-inputs[name]:
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
			Recognize: func(_ context.Context, _ []float32, settings pipeline.PhraseSettings) (string, error) {
				recognitions <- call{flow: name, settings: settings}
				if name == "self" {
					return "ききょう", nil
				}
				return "LILTOON", nil
			},
			Translate: func(_ context.Context, text string, settings pipeline.PhraseSettings) (string, error) {
				translations <- call{name, text, settings}
				return "translation", nil
			},
		}, nil
	}
	receive := func(calls <-chan call) call {
		t.Helper()
		select {
		case got := <-calls:
			return got
		case <-time.After(5 * time.Second):
			t.Fatal("pipeline did not process the phrase")
			return call{}
		}
	}
	runs := make(map[string]*flowRun)
	for _, name := range []string{"self", "world"} {
		if err := a.changeFlow(name, "start"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.flows[name].status.State == "running" })
		a.mu.Lock()
		runs[name] = a.flows[name]
		a.mu.Unlock()
	}
	for _, word := range []string{"桔梗", "菊響", ""} {
		a.mu.Lock()
		s := cloneSettings(a.settings)
		a.mu.Unlock()
		s.Glossary = nil
		if word != "" {
			s.Glossary = []glossary.Entry{{SourceLang: "ja", Source: word, TargetLang: "en", Target: "lilToon", Reading: "ききょう"}}
		}
		body, _ := json.Marshal(s)
		if response := request(a, "PUT", "/api/settings", string(body)); response.Code != http.StatusOK {
			t.Fatal(response.Code, response.Body.String())
		}
		for _, name := range []string{"self", "world"} {
			inputs[name] <- struct{}{}
			source, target := s.Flows.Languages(name)
			var wantTerms []glossary.Term
			wantText := "ききょう"
			if name == "world" {
				wantText = "LILTOON"
			}
			if word != "" {
				wantText = word
				wantTerms = []glossary.Term{{Source: word, Reading: "ききょう", Target: "lilToon"}}
				if name == "world" {
					wantText = "lilToon"
					wantTerms = []glossary.Term{{Source: "lilToon", Target: word}}
				}
			}
			recognized, translated := receive(recognitions), receive(translations)
			if recognized.flow != name || translated.flow != name || translated.text != wantText ||
				translated.settings.SourceLang != source || translated.settings.TargetLang != target ||
				!reflect.DeepEqual(recognized.settings.Terms, wantTerms) || !reflect.DeepEqual(translated.settings.Terms, wantTerms) {
				t.Fatalf("%s glossary %q: recognition %+v, translation %+v, want %q with %+v", name, word, recognized, translated, wantText, wantTerms)
			}
			a.mu.Lock()
			unchanged := a.flows[name] == runs[name] && runs[name].status.State == "running"
			a.mu.Unlock()
			if !unchanged {
				t.Fatalf("glossary change restarted %s", name)
			}
		}
	}
}
