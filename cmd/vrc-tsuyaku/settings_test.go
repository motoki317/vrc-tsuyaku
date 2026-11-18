package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
)

func pollSettings(a *app) {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.reloadSettingsLocked()
}

func TestExternalSettingsApplyLikePageSave(t *testing.T) {
	for _, change := range []string{"microphone", "address", "spoken", "target", "template", "reading time", "ui", "glossary", "glossary-empty", "glossary-missing"} {
		t.Run(change, func(t *testing.T) {
			a := testApp(t)
			a.settings.Flows.Self.Enabled, a.settings.Flows.World.Enabled = true, true
			a.settings.Flows.Self.Microphone = "synthetic-source"
			a.settings.Chatbox.Address = "127.0.0.1:47123"
			a.settings.LicensesAccepted["example"] = "2026-11-09T01:02:03Z"
			if change == "glossary-empty" || change == "glossary-missing" {
				a.settings.Glossary = []glossary.Entry{{SourceLang: "ja", Source: "マヌカ", TargetLang: "en", Target: "Manuka"}}
			}
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
			case "reading time":
				s.Chatbox.ReadingBase, s.Chatbox.ReadingFactor = 4.5, 3.1
			case "ui":
				s.UI.Language = "ja"
			case "glossary":
				s.Glossary = []glossary.Entry{{SourceLang: "ja", Source: "桔梗", TargetLang: "en", Target: "Kikyo", Reading: "ききょう"}}
			case "glossary-empty":
				s.Glossary = []glossary.Entry{}
			case "glossary-missing":
				s.Glossary = nil
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
			if !reflect.DeepEqual(got, cloneSettings(s)) {
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

func TestReadingTimeSettingsAPI(t *testing.T) {
	a := testApp(t)
	response := request(a, "GET", "/api/state", "")
	var state struct {
		Reading map[string]map[string]float64 `json:"reading"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]float64{"base": {"min": 1, "max": 10}, "factor": {"min": 1, "max": 8}}
	if !reflect.DeepEqual(state.Reading, want) {
		t.Fatalf("reading bounds = %v, want %v", state.Reading, want)
	}
	for _, tc := range []struct {
		base, factor float64
		key          string
	}{
		{0.99, 2, "reading_base_seconds"},
		{10.01, 2, "reading_base_seconds"},
		{2, 0.99, "reading_factor"},
		{2, 8.01, "reading_factor"},
	} {
		s := cloneSettings(a.settings)
		s.Chatbox.ReadingBase, s.Chatbox.ReadingFactor = tc.base, tc.factor
		body, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		w := request(a, "PUT", "/api/settings", string(body))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "chatbox."+tc.key) {
			t.Fatalf("base %g, factor %g: HTTP %d %s", tc.base, tc.factor, w.Code, w.Body.String())
		}
	}
}

func TestGlossarySettingsClone(t *testing.T) {
	s := config.Defaults()
	s.Glossary = []glossary.Entry{{SourceLang: "ja", Source: "桔梗", TargetLang: "en", Target: "Kikyo", Reading: "ききょう"}}
	cloned := cloneSettings(s)
	cloned.Glossary[0].Reading = "キキョウ"
	if s.Glossary[0].Reading != "ききょう" {
		t.Fatal("settings clone shares the glossary's backing array")
	}
	for _, entries := range [][]glossary.Entry{nil, {}} {
		s.Glossary = entries
		if cloned := cloneSettings(s); cloned.Glossary == nil || !reflect.DeepEqual(cloned, config.Defaults()) {
			t.Fatal("settings clone did not normalize an empty glossary", cloned.Glossary)
		}
	}
}

func TestEmptyGlossaryPageSave(t *testing.T) {
	for _, glossaryJSON := range []string{"", `,"Glossary":[]`, `,"Glossary":null`} {
		t.Run(glossaryJSON, func(t *testing.T) {
			a := testApp(t)
			s := cloneSettings(a.settings)
			s.Glossary = []glossary.Entry{{SourceLang: "ja", Source: "マヌカ", TargetLang: "en", Target: "Manuka"}}
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(config.Defaults())
			if err != nil {
				t.Fatal(err)
			}
			bodyText := strings.Replace(string(body), `,"Glossary":[]`, glossaryJSON, 1)
			w := request(a, "PUT", "/api/settings", bodyText)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Glossary":[]`) {
				t.Fatal("empty glossary not returned as an array", w.Code, w.Body.String())
			}
			if a.settingsFile != a.statSettings() {
				t.Fatal("normalization made an app write look like an external edit")
			}
			before, _ := a.events.after(0)
			pollSettings(a)
			after, _ := a.events.after(0)
			if len(after) != len(before) {
				t.Fatal("empty glossary caused a false reload")
			}
			if saved, err := config.Load(a.dir); err != nil || !reflect.DeepEqual(saved, config.Defaults()) {
				t.Fatal("empty glossary differed after reload", saved.Glossary, err)
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

func TestSettingsSaveRejectsUnobservedExternalChange(t *testing.T) {
	for _, tc := range []struct {
		name, external string
		invalid        bool
	}{
		{name: "valid", external: "ui: {language: ja}\nflows: {world: {target: ko}}\n"},
		{name: "invalid", external: "ui: {language: invalid}\n", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			initial := config.Defaults()
			initial.UI.Language = "en"
			body, err := json.Marshal(initial)
			if err != nil {
				t.Fatal(err)
			}
			if w := request(a, "PUT", "/api/settings", string(body)); w.Code != http.StatusOK {
				t.Fatal(w.Code, w.Body.String())
			}
			if saved, err := config.Load(a.dir); err != nil || !reflect.DeepEqual(saved, initial) {
				t.Fatal("save without external change failed", saved, err)
			}
			before := a.statSettings()
			path := filepath.Join(a.dir, "settings.yaml")
			if err := os.WriteFile(path, []byte(tc.external), 0o600); err != nil {
				t.Fatal(err)
			}
			if a.statSettings().size == before.size {
				t.Fatal("external edit did not change the file size")
			}
			stale := cloneSettings(initial)
			stale.Chatbox.Template = "{{.Translation}}"
			body, err = json.Marshal(stale)
			if err != nil {
				t.Fatal(err)
			}
			w := request(a, "PUT", "/api/settings", string(body))
			if w.Code != http.StatusConflict {
				t.Fatalf("stale save returned %d, want 409", w.Code)
			}
			var failure struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil || failure.Code != "settings_changed" {
				t.Fatal("missing settings_changed code", failure, err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != tc.external {
				t.Fatal("stale save overwrote the external edit", string(data), err)
			}
			w = request(a, "GET", "/api/state", "")
			type settingsSnapshot struct {
				Settings      config.Settings `json:"settings"`
				SettingsError string          `json:"settingsError"`
			}
			var state settingsSnapshot
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || w.Code != http.StatusOK {
				t.Fatal(w.Code, err)
			}
			want := cloneSettings(initial)
			kind, text := "error", "ui.language"
			if tc.invalid {
				if !strings.Contains(state.SettingsError, "ui.language") {
					t.Fatal("invalid external edit did not set settingsError", state.SettingsError)
				}
			} else {
				want = config.Defaults()
				want.UI.Language, want.Flows.World.Target = "ja", "ko"
				kind, text = "settings", "reloaded"
				if state.SettingsError != "" {
					t.Fatal(state.SettingsError)
				}
			}
			if !reflect.DeepEqual(state.Settings, want) {
				t.Fatalf("settings = %+v, want %+v", state.Settings, want)
			}
			history, _ := a.events.after(0)
			if len(history) != 2 || history[1].Kind != kind || !strings.Contains(history[1].Text, text) {
				t.Fatal("external edit did not emit the reload result", history)
			}
			retry := cloneSettings(state.Settings)
			retry.Chatbox.Template = stale.Chatbox.Template
			body, err = json.Marshal(retry)
			if err != nil {
				t.Fatal(err)
			}
			w = request(a, "PUT", "/api/settings", string(body))
			if w.Code != http.StatusOK {
				t.Fatal("retry failed", w.Code, w.Body.String())
			}
			if saved, err := config.Load(a.dir); err != nil || !reflect.DeepEqual(saved, retry) {
				t.Fatal("retry did not save the refreshed settings", saved, err)
			}
			state = settingsSnapshot{}
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state.SettingsError != "" || !reflect.DeepEqual(state.Settings, retry) {
				t.Fatal("retry did not apply the refreshed settings", state, err)
			}
		})
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
					if w := request(a, "POST", "/api/flows/"+flow+"/stop", ""); w.Code != http.StatusOK {
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
				if w := request(a, "POST", "/api/flows/"+flow+"/start", ""); w.Code != http.StatusConflict {
					t.Fatal(w.Code, w.Body.String())
				}
			})
		}
	}
}
