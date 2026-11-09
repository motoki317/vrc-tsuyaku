package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/download"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

func seedSafeFlowSettings(t *testing.T, a *app) {
	t.Helper()
	s := cloneSettings(a.settings)
	s.Flows.Self.Microphone = "synthetic-source"
	s.Chatbox.Address = "127.0.0.1:47123"
	if err := a.writeSettings(s); err != nil {
		t.Fatal(err)
	}
	a.settings = s
}

func pendingDownloadApp(t *testing.T, missing ...string) (*app, *atomic.Int32) {
	t.Helper()
	a := testApp(t)
	seedSafeFlowSettings(t, a)
	for id, state := range a.artifacts {
		state.State = "installed"
		a.artifacts[id] = state
	}
	for _, id := range missing {
		state := a.artifacts[id]
		state.State = "missing"
		a.artifacts[id] = state
	}
	a.settings.Flows.Self.Enabled = true
	a.settings.Flows.World.Enabled = true
	if err := a.writeSettings(a.settings); err != nil {
		t.Fatal(err)
	}
	starts := new(atomic.Int32)
	a.prepare = func(ctx context.Context, _ string, _ config.Settings) (pipeline.Stages, error) {
		starts.Add(1)
		<-ctx.Done()
		return pipeline.Stages{}, ctx.Err()
	}
	a.downloadArtifact = func(context.Context, string, string, func(download.Progress)) error { return nil }
	return a, starts
}

func TestDownloadAcceptancePreservesExternalSettings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		invalid bool
	}{
		{name: "valid"},
		{name: "invalid", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			seedSafeFlowSettings(t, a)
			var fetches atomic.Int32
			a.downloadArtifact = func(context.Context, string, string, func(download.Progress)) error {
				fetches.Add(1)
				return nil
			}
			key := a.artifacts[catalog.PLaMo].Artifact.AcceptanceKey
			if key == "" {
				t.Fatal("test artifact does not require license acceptance")
			}
			before := a.statSettings()
			path := filepath.Join(a.dir, "settings.yaml")
			external := cloneSettings(a.settings)
			external.UI.Language = "ja"
			external.Chatbox.Template = "{{.Translation}}"
			if tc.invalid {
				if err := os.WriteFile(path, []byte("ui: {language: invalid}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := config.Save(a.dir, external); err != nil {
				t.Fatal(err)
			}
			if a.statSettings().size == before.size {
				t.Fatal("external edit did not change the file size")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			w := request(a, "POST", "/api/downloads/"+catalog.PLaMo, `{"acceptLicense":true}`)
			a.downloadsWG.Wait()
			if tc.invalid {
				if w.Code != http.StatusConflict || fetches.Load() != 0 {
					t.Fatalf("invalid settings: status %d, fetches %d", w.Code, fetches.Load())
				}
				if after, err := os.ReadFile(path); err != nil || string(after) != string(data) {
					t.Fatal("acceptance overwrote invalid external settings", err)
				}
				if a.settingsError == "" || a.settings.LicensesAccepted[key] != "" {
					t.Fatal("invalid external settings did not prevent acceptance", a.settingsError)
				}
				return
			}
			if w.Code != http.StatusAccepted || fetches.Load() != 1 {
				t.Fatalf("valid settings: status %d, fetches %d", w.Code, fetches.Load())
			}
			saved, err := config.Load(a.dir)
			if err != nil {
				t.Fatal(err)
			}
			if saved.LicensesAccepted[key] == "" {
				t.Fatal("explicit license acceptance was not saved")
			}
			external.LicensesAccepted[key] = saved.LicensesAccepted[key]
			if !reflect.DeepEqual(saved, external) || !reflect.DeepEqual(a.settings, external) {
				t.Fatalf("acceptance lost external settings: saved %+v, live %+v, want %+v", saved, a.settings, external)
			}
		})
	}
}

func TestLanguageChangeStartsAfterRecognizerDownload(t *testing.T) {
	for _, flow := range []string{"self", "world"} {
		t.Run(flow, func(t *testing.T) {
			missing, spoken := catalog.Parakeet, "en"
			if flow == "world" {
				missing, spoken = catalog.Haas, "ja"
			}
			a, starts := pendingDownloadApp(t, missing)
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
			a.prepare = func(ctx context.Context, name string, s config.Settings) (pipeline.Stages, error) {
				if name != flow {
					t.Errorf("started unrelated flow %s", name)
				}
				if starts.Add(1) == 2 {
					if got, _ := s.Flows.Languages(flow); got != spoken {
						t.Errorf("resumed old recognizer: %s, want %s", got, spoken)
					}
				}
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
			waitFor(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.flows[flow].status.State == "running" })
			s := cloneSettings(a.settings)
			if flow == "self" {
				s.Flows.Self.Spoken = spoken
			} else {
				s.Flows.World.Spoken = spoken
			}
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			if got := a.flows[flow].status; got.Code != errcode.MissingDownloads || got.State != "error" {
				t.Fatal("language change did not wait for recognizer", got)
			}
			if err := a.startDownload(missing, false); err != nil {
				t.Fatal(err)
			}
			a.downloadsWG.Wait()
			waitFor(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.flows[flow].status.State == "running" })
			if starts.Load() != 2 {
				t.Fatal("flow did not resume once", starts.Load())
			}
			if err := a.startDownload(missing, false); err == nil || starts.Load() != 2 {
				t.Fatal("duplicate download restarted flow", err, starts.Load())
			}
		})
	}
}

func TestDownloadCompletionWaitsForAllRequirements(t *testing.T) {
	a, starts := pendingDownloadApp(t, catalog.Silero, catalog.Haas)
	a.startEnabled()
	for _, id := range []string{catalog.Haas, catalog.Silero, catalog.Silero} {
		var failure error
		if id == catalog.Silero && a.artifacts[id].Error == "" {
			failure = errcode.With(errcode.DownloadHTTP, errors.New("synthetic connection failure"))
		}
		a.downloadArtifact = func(context.Context, string, string, func(download.Progress)) error { return failure }
		if err := a.startDownload(id, false); err != nil {
			t.Fatal(err)
		}
		a.downloadsWG.Wait()
		if failure != nil {
			var state struct{ Artifacts map[string]artifactState }
			w := request(a, "GET", "/api/state", "")
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if got := state.Artifacts[id]; got.State != "missing" || got.Code != errcode.DownloadHTTP || got.Error != failure.Error() {
				t.Fatal(got)
			}
		}
		if a.artifacts[catalog.Silero].State != "installed" && starts.Load() != 0 {
			t.Fatal("started before all artifacts installed")
		}
	}
	waitFor(t, func() bool { return starts.Load() == 2 })
	for _, flow := range []string{"self", "world"} {
		a.mu.Lock()
		status := a.flows[flow].status
		a.mu.Unlock()
		if !status.active() || status.Code != "" || status.Error != "" {
			t.Fatal("retry retained error", status)
		}
	}
}

func TestDownloadCompletionLeavesOtherFlowsAlone(t *testing.T) {
	for _, state := range []string{"off", "never-started", "other-error", "active", "stopped"} {
		t.Run(state, func(t *testing.T) {
			a, starts := pendingDownloadApp(t, catalog.Parakeet)
			a.settings.Flows.World.Enabled = false
			if state == "off" {
				a.settings.Flows.Self.Enabled = false
				a.refuseStart("self", errcode.With(errcode.MissingDownloads, errors.New("missing")))
			}
			if state == "other-error" {
				a.refuseStart("self", errcode.With(errcode.MicrophoneUnavailable, errors.New("missing microphone")))
			}
			if state == "active" || state == "stopped" {
				if err := a.start("self", a.settings); err != nil {
					t.Fatal(err)
				}
				waitFor(t, func() bool { return starts.Load() == 1 })
				if state == "stopped" {
					a.stop("self")
				}
			}
			if err := a.writeSettings(a.settings); err != nil {
				t.Fatal(err)
			}
			before, count := a.flows["self"].status, starts.Load()
			if err := a.startDownload(catalog.Parakeet, false); err != nil {
				t.Fatal(err)
			}
			a.downloadsWG.Wait()
			a.mu.Lock()
			after := a.flows["self"].status
			a.mu.Unlock()
			if after != before || starts.Load() != count {
				t.Fatal("unrelated completion restarted flow", before, after, starts.Load())
			}
		})
	}
}

func TestDownloadCompletionObservesStopAndInvalidSettings(t *testing.T) {
	for _, action := range []string{"stop", "invalid-settings"} {
		t.Run(action, func(t *testing.T) {
			a, starts := pendingDownloadApp(t, catalog.Silero)
			a.startEnabled()
			release := make(chan struct{})
			a.downloadArtifact = func(ctx context.Context, _, _ string, _ func(download.Progress)) error {
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if err := a.startDownload(catalog.Silero, false); err != nil {
				t.Fatal(err)
			}
			if action == "stop" {
				for _, flow := range []string{"self", "world"} {
					if err := a.changeFlow(flow, "stop"); err != nil {
						t.Fatal(err)
					}
				}
			} else if err := os.WriteFile(filepath.Join(a.dir, "settings.yaml"), []byte("broken: ["), 0o600); err != nil {
				t.Fatal(err)
			}
			close(release)
			a.downloadsWG.Wait()
			if starts.Load() != 0 {
				t.Fatal("download completion ignored intervening change")
			}
			if action == "invalid-settings" && a.settingsError == "" {
				t.Fatal("completion did not reload invalid external settings")
			}
		})
	}
}

func TestShutdownDuringDownloadCompletion(t *testing.T) {
	a, starts := pendingDownloadApp(t, catalog.Silero)
	a.startEnabled()
	a.downloadArtifact = func(ctx context.Context, _, _ string, _ func(download.Progress)) error {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := a.startDownload(catalog.Silero, false); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- a.close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown deadlocked with download completion")
	}
	if starts.Load() != 0 {
		t.Fatal("shutdown started a waiting flow")
	}
	if w := request(a, "POST", "/api/downloads/"+catalog.Silero, "{}"); w.Code != http.StatusConflict {
		t.Fatal("download started after shutdown", w.Code)
	}
}
