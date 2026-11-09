package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/download"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

func testTranslator(t *testing.T) *translate.Supervisor {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	supervisor, err := translate.New(executable, "unused-model")
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return supervisor
}

func TestTranslatorChangeRestartsBothFlows(t *testing.T) {
	for _, initial := range catalog.Translators() {
		for _, phase := range []string{"starting", "running"} {
			t.Run(initial.ID+"/"+phase, func(t *testing.T) {
				a, _ := pendingDownloadApp(t)
				a.settings.Translator = initial.ID
				if err := a.writeSettings(a.settings); err != nil {
					t.Fatal(err)
				}
				old := testTranslator(t)
				a.translator = old
				type preparation struct{ flow, translator string }
				prepared := make(chan preparation, 4)
				a.prepare = func(ctx context.Context, flow string, s config.Settings) (pipeline.Stages, error) {
					if s.Translator != initial.ID {
						if code := errcode.Of(old.Wait(ctx)); code != errcode.TranslatorExited {
							t.Errorf("old supervisor survived switch: %s", code)
						}
						select {
						case a.modelTurn <- struct{}{}:
						case <-ctx.Done():
							return pipeline.Stages{}, ctx.Err()
						}
						if a.translator == nil {
							a.translator = testTranslator(t)
						}
						<-a.modelTurn
					}
					prepared <- preparation{flow, s.Translator}
					stopped := func() {
						a.mu.Lock()
						defer a.mu.Unlock()
						if a.settings.Translator != s.Translator {
							t.Error("published new translator before old flow stopped")
						}
					}
					if phase == "starting" {
						<-ctx.Done()
						stopped()
						return pipeline.Stages{}, ctx.Err()
					}
					return pipeline.Stages{Source: recordingSource(func(ctx context.Context, _ func([]float32) error) error {
						<-ctx.Done()
						stopped()
						return ctx.Err()
					}),
						Probability: func(context.Context, []float32) (float32, error) { return 0, nil },
						Recognize:   func(context.Context, []float32) (string, error) { return "", nil },
						Translate:   func(context.Context, string, string, string) (string, error) { return "", nil },
					}, nil
				}
				a.startEnabled()
				for range 2 {
					<-prepared
				}
				waitFor(t, func() bool {
					a.mu.Lock()
					defer a.mu.Unlock()
					return a.flows["self"].status.State == phase && a.flows["world"].status.State == phase
				})
				a.mu.Lock()
				oldSelf, oldWorld := a.flows["self"], a.flows["world"]
				s := cloneSettings(a.settings)
				a.mu.Unlock()
				s.Translator = catalog.Translator08B
				if initial.ID == s.Translator {
					s.Translator = catalog.Translator2B
				}
				if err := a.saveSettings(s); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if got := <-prepared; got.translator != s.Translator {
						t.Fatal("wrong restarted translator", got)
					}
				}
				a.mu.Lock()
				replaced := a.flows["self"] != oldSelf && a.flows["world"] != oldWorld
				a.mu.Unlock()
				if !replaced || a.translator == old || a.translator == nil {
					t.Fatal("switch did not replace both flows and their supervisor")
				}
				saved, err := config.Load(a.dir)
				if err != nil || !reflect.DeepEqual(saved, s) {
					t.Fatal("translator choice was not saved", saved.Translator, err)
				}
			})
		}
	}
}

func TestTranslatorChangeClosesIdleSupervisor(t *testing.T) {
	a := testApp(t)
	old := testTranslator(t)
	a.translator = old
	s := cloneSettings(a.settings)
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if a.translator != old {
		t.Fatal("unchanged translator closed the idle server")
	}
	s.Translator = catalog.Translator08B
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if a.translator != nil || errcode.Of(old.Wait(t.Context())) != errcode.TranslatorExited {
		t.Fatal("idle server survived translator change")
	}
}

func TestMissingTranslatorWaitsForExplicitDownload(t *testing.T) {
	a, starts := pendingDownloadApp(t, catalog.PLaMo08B)
	a.settings.LicensesAccepted[catalog.PLaMoAcceptanceKey] = "2026-11-09T00:00:00Z"
	if err := a.writeSettings(a.settings); err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	a.downloadArtifact = func(_ context.Context, id, _ string, _ func(download.Progress)) error {
		if id != catalog.PLaMo08B {
			t.Errorf("unexpected download %s", id)
		}
		downloads.Add(1)
		return nil
	}
	a.startEnabled()
	waitFor(t, func() bool { return starts.Load() == 2 })
	s := cloneSettings(a.settings)
	s.Translator = catalog.Translator08B
	body, _ := json.Marshal(s)
	if w := request(a, "PUT", "/api/settings", string(body)); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, flow := range []string{"self", "world"} {
		if got := a.flows[flow].status; got.State != "error" || got.Code != errcode.MissingDownloads {
			t.Fatal("flow did not wait for translator", flow, got)
		}
	}
	if starts.Load() != 2 || downloads.Load() != 0 || !a.settings.Flows.Self.Enabled || !a.settings.Flows.World.Enabled {
		t.Fatal("switch started work without the selected download")
	}
	s.Translator = catalog.Translator2B
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return starts.Load() == 4 })
	s.Translator = catalog.Translator08B
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 4 || downloads.Load() != 0 {
		t.Fatal("switching back to missing translator started work")
	}
	if w := request(a, "POST", "/api/downloads/"+catalog.PLaMo08B, `{"acceptLicense":false}`); w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	waitFor(t, func() bool { return starts.Load() == 6 })
	if downloads.Load() != 1 {
		t.Fatal("explicit download did not start once")
	}
}

func TestLanguageChangeResumesWaitingFlow(t *testing.T) {
	for _, flow := range []string{"self", "world"} {
		t.Run(flow, func(t *testing.T) {
			a, starts := pendingDownloadApp(t, catalog.ParakeetV3)
			a.translator = testTranslator(t)
			a.settings.Flows.World.Spoken = catalog.Japanese
			if err := a.writeSettings(a.settings); err != nil {
				t.Fatal(err)
			}
			a.prepare = func(_ context.Context, name string, s config.Settings) (pipeline.Stages, error) {
				spoken, _ := s.Flows.Languages(name)
				if name != flow || spoken != catalog.Japanese {
					t.Errorf("prepared %s with language %s, want %s with Japanese", name, spoken, flow)
				}
				starts.Add(1)
				return pipeline.Stages{
					Source:      recordingSource(func(ctx context.Context, _ func([]float32) error) error { <-ctx.Done(); return ctx.Err() }),
					Probability: func(context.Context, []float32) (float32, error) { return 0, nil },
					Recognize:   func(context.Context, []float32) (string, error) { return "", nil },
					Translate:   func(context.Context, string, string, string) (string, error) { return "", nil },
				}, nil
			}
			if err := a.changeFlow(flow, "start"); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool {
				a.mu.Lock()
				defer a.mu.Unlock()
				return a.flows[flow].status.State == "running"
			})
			s := cloneSettings(a.settings)
			for _, spoken := range []string{"fr", catalog.Japanese} {
				if flow == "self" {
					s.Flows.Self.Spoken = spoken
				} else {
					s.Flows.World.Spoken = spoken
				}
				if err := a.saveSettings(s); err != nil {
					t.Fatal(err)
				}
				if spoken == "fr" {
					a.mu.Lock()
					status := a.flows[flow].status
					a.mu.Unlock()
					if status.State != "error" || status.Code != errcode.MissingDownloads || starts.Load() != 1 {
						t.Fatal("flow did not wait for the missing recognizer", status, starts.Load())
					}
				}
			}
			waitFor(t, func() bool {
				a.mu.Lock()
				defer a.mu.Unlock()
				return a.flows[flow].status == (flowState{State: "running"})
			})
			if starts.Load() != 2 {
				t.Fatal("returning to Japanese did not start the flow once", starts.Load())
			}
			saved, err := config.Load(a.dir)
			if err != nil || !reflect.DeepEqual(saved, s) {
				t.Fatal("language changes did not preserve the enabled settings", saved, err)
			}
		})
	}
}

func TestTranslatorStateAndDeletion(t *testing.T) {
	for _, choice := range catalog.Translators() {
		for _, flow := range []string{"self", "world"} {
			for _, phase := range []string{"starting", "running"} {
				t.Run(choice.ID+"/"+flow+"/"+phase, func(t *testing.T) {
					a := testApp(t)
					a.settings.Translator = choice.ID
					a.flows[flow].status.State = phase
					var state struct {
						Settings  config.Settings         `json:"settings"`
						Required  map[string][]string     `json:"required"`
						Languages catalog.LanguageCatalog `json:"languages"`
					}
					w := request(a, "GET", "/api/state", "")
					if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
						t.Fatal(err)
					}
					if state.Settings.Translator != choice.ID || !reflect.DeepEqual(state.Languages.Translators, catalog.Translators()) {
						t.Fatal("translator choices missing from state")
					}
					for _, required := range state.Required {
						if !slices.Contains(required, choice.Artifact) {
							t.Fatal("selected translator not required", required)
						}
					}
					for _, candidate := range catalog.Translators() {
						dir := a.artifactDir(candidate.Artifact)
						if err := os.MkdirAll(dir, 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, "fixture"), []byte("fixture"), 0o600); err != nil {
							t.Fatal(err)
						}
						w := request(a, "DELETE", "/api/downloads/"+candidate.Artifact, "")
						if candidate.ID == choice.ID {
							if w.Code != http.StatusConflict {
								t.Fatal(w.Code, w.Body.String())
							}
							if _, err := os.Stat(dir); err != nil {
								t.Fatal("selected translator removed", err)
							}
						} else {
							for _, required := range state.Required {
								if slices.Contains(required, candidate.Artifact) {
									t.Fatal("unused translator required", required)
								}
							}
							if w.Code != http.StatusOK {
								t.Fatal(w.Code, w.Body.String())
							}
							if _, err := os.Stat(dir); !os.IsNotExist(err) {
								t.Fatal("unused translator remains", err)
							}
						}
					}
				})
			}
		}
	}
}

func TestTranslatorChangeKeepsUnrelatedInactiveFlowsStopped(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		status  flowState
	}{
		{"stopped", false, flowState{State: "stopped"}},
		{"enabled but stopped", true, flowState{State: "stopped"}},
		{"disabled waiting", false, flowState{State: "error", Code: errcode.MissingDownloads}},
		{"unrelated error", true, flowState{State: "error", Code: errcode.MicrophoneUnavailable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := pendingDownloadApp(t)
			a.settings.Flows.Self.Enabled = tc.enabled
			a.settings.Flows.World.Enabled = tc.enabled
			for _, run := range a.flows {
				run.status = tc.status
			}
			s := cloneSettings(a.settings)
			s.Translator = catalog.Translator08B
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			for name, run := range a.flows {
				if run.status != tc.status {
					t.Fatal("translator restarted inactive flow", name, run.status)
				}
			}
		})
	}
}
