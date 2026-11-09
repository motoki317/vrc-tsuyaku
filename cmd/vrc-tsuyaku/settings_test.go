package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
)

func pollSettings(a *app) {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.reloadSettingsLocked()
}

func TestExternalSettingsApplyLikePageSave(t *testing.T) {
	for _, change := range []string{"microphone", "address", "spoken", "target", "template", "ui"} {
		t.Run(change, func(t *testing.T) {
			a := testApp(t)
			a.settings.Flows.Self.Enabled, a.settings.Flows.World.Enabled = true, true
			a.settings.Flows.Self.Microphone = "synthetic-source"
			a.settings.Chatbox.Address = "127.0.0.1:47123"
			a.settings.LicensesAccepted["example"] = "2026-11-09T01:02:03Z"
			for id, state := range a.artifacts {
				state.State = "installed"
				a.artifacts[id] = state
			}
			var self, world atomic.Int32
			a.prepare = func(ctx context.Context, name string, _ config.Settings) (pipeline.Stages, error) {
				if name == "self" {
					self.Add(1)
				} else {
					world.Add(1)
				}
				<-ctx.Done()
				return pipeline.Stages{}, ctx.Err()
			}
			a.startEnabled()
			waitFor(t, func() bool { return self.Load() == 1 && world.Load() == 1 })
			s := cloneSettings(a.settings)
			s.Flows.Self.Enabled, s.Flows.World.Enabled = false, false
			s.LicensesAccepted = map[string]string{}
			switch change {
			case "microphone":
				s.Flows.Self.Microphone = "other-synthetic-source"
			case "address":
				s.Chatbox.Address = "127.0.0.1:47124"
			case "spoken":
				s.Flows.Self.Spoken, s.Flows.World.Spoken = "en", "ja"
			case "target":
				s.Flows.Self.Target, s.Flows.World.Target = "ja-easy", "ko"
			case "template":
				s.Chatbox.Template = "{{.Translation}}"
			case "ui":
				s.UI.Language = "ja"
			}
			if err := config.Save(a.dir, s); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(a.dir, "settings.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			pollSettings(a)
			wantSelf, wantWorld := int32(1), int32(1)
			if change == "microphone" || change == "address" || change == "spoken" {
				wantSelf++
			}
			if change == "spoken" {
				wantWorld++
			}
			waitFor(t, func() bool { return self.Load() == wantSelf && world.Load() == wantWorld })
			a.mu.Lock()
			got := cloneSettings(a.settings)
			a.mu.Unlock()
			s.Flows.Self.Enabled, s.Flows.World.Enabled = true, true
			s.LicensesAccepted["example"] = "2026-11-09T01:02:03Z"
			if !reflect.DeepEqual(got, s) {
				t.Fatalf("reload differs from page save: %+v, want %+v", got, s)
			}
			if after, err := os.ReadFile(filepath.Join(a.dir, "settings.yaml")); err != nil || string(after) != string(before) {
				t.Fatal("reload rewrote external edit", err)
			}
			beforeEvents, _ := a.events.after(0)
			pollSettings(a)
			afterEvents, _ := a.events.after(0)
			if len(afterEvents) != len(beforeEvents) {
				t.Fatal("unchanged file reloaded twice")
			}
		})
	}
}

func TestSettingsPollerAndRecovery(t *testing.T) {
	a := testApp(t)
	a.watchSettings()
	path := filepath.Join(a.dir, "settings.yaml")
	if err := os.WriteFile(path, []byte("ui: {language: ja}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.settings.UI.Language == "ja" && a.settingsError == ""
	})
	if err := os.WriteFile(path, []byte("ui: {language: invalid}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return strings.Contains(a.settingsError, "ui.language") && a.settings.UI.Language == "ja"
	})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.settingsError == "" && a.settings.UI.Language == ""
	})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("reload recreated deleted settings", err)
	}
}

func TestSettingsPollDetectsTimeAndSize(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(a.dir, "settings.yaml")
	for i, tc := range []struct{ body, language string }{
		{"ui: {language: en}\n", "en"},
		{"ui: {language: ja}\n", "ja"},
		{"ui: {language: ''}\n# size changed\n", ""},
	} {
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		modified := time.Unix(1000, 0)
		if i > 0 {
			modified = time.Unix(1001, 0)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
		pollSettings(a)
		if a.settings.UI.Language != tc.language {
			t.Fatal("missed edit", i, a.settings.UI)
		}
	}
}

func TestAppSettingsWritesDoNotReload(t *testing.T) {
	a := testApp(t)
	a.open = func(string) error { return nil }
	if err := a.openTarget("settings"); err != nil {
		t.Fatal(err)
	}
	s := cloneSettings(a.settings)
	s.UI.Language = "ja"
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.changeFlow("self", "stop"); err != nil {
		t.Fatal(err)
	}
	before, _ := a.events.after(0)
	pollSettings(a)
	after, _ := a.events.after(0)
	if len(after) != len(before) {
		t.Fatal("app write counted as an external edit", after)
	}
}

func TestStopWithInvalidExternalSettings(t *testing.T) {
	for _, flow := range []string{"self", "world"} {
		for _, observed := range []bool{false, true} {
			t.Run(flow+"/observed="+map[bool]string{false: "false", true: "true"}[observed], func(t *testing.T) {
				a := testApp(t)
				a.settings.Flows.Self.Microphone = "synthetic-source"
				a.settings.Chatbox.Address = "127.0.0.1:47123"
				for id, state := range a.artifacts {
					state.State = "installed"
					a.artifacts[id] = state
				}
				started := make(chan struct{})
				a.prepare = func(ctx context.Context, _ string, _ config.Settings) (pipeline.Stages, error) {
					close(started)
					<-ctx.Done()
					return pipeline.Stages{}, ctx.Err()
				}
				if err := a.changeFlow(flow, "start"); err != nil {
					t.Fatal(err)
				}
				<-started
				path := filepath.Join(a.dir, "settings.yaml")
				invalid := "chatbox: {template: '{{.Orignal}}'}\n"
				if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
					t.Fatal(err)
				}
				if observed {
					pollSettings(a)
				}
				for range 2 {
					if w := request(a, "POST", "/api/flows/"+flow+"/stop", "", a.token); w.Code != http.StatusOK {
						t.Fatal(w.Code, w.Body.String())
					}
				}
				if a.flows[flow].status.State != "stopped" || a.settingsError == "" {
					t.Fatal(a.flows[flow].status, a.settingsError)
				}
				if (flow == "self" && a.settings.Flows.Self.Enabled) || (flow == "world" && a.settings.Flows.World.Enabled) {
					t.Fatal("stop retained enabled state", a.settings.Flows)
				}
				if body, err := os.ReadFile(path); err != nil || string(body) != invalid {
					t.Fatal("stop overwrote invalid settings", string(body), err)
				}
				if w := request(a, "POST", "/api/flows/"+flow+"/start", "", a.token); w.Code != http.StatusConflict {
					t.Fatal(w.Code, w.Body.String())
				}
			})
		}
	}
}
