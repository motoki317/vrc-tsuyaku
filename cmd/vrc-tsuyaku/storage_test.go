package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
)

func TestUILanguageAPI(t *testing.T) {
	a := testApp(t)
	s := cloneSettings(a.settings)
	s.UI.Language = "ja"
	body, _ := json.Marshal(s)
	w := request(a, "PUT", "/api/settings", string(body), a.token)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"UI":{"Language":"ja"}`) {
		t.Fatal(w.Code, w.Body.String())
	}
	loaded, err := config.Load(a.dir)
	if err != nil || loaded.UI.Language != "ja" {
		t.Fatal(loaded, err)
	}
	s.UI.Language = "fr"
	body, _ = json.Marshal(s)
	w = request(a, "PUT", "/api/settings", string(body), a.token)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "ui.language") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDeleteDownload(t *testing.T) {
	a := testApp(t)
	for _, id := range []string{catalog.Haas, catalog.Parakeet} {
		dir := a.artifactDir(id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "fixture"), []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
		state := a.artifacts[id]
		state.State = "installed"
		a.artifacts[id] = state
	}
	path := "/api/downloads/" + catalog.Haas
	for _, token := range []string{"", "wrong"} {
		if w := request(a, "DELETE", path, "", token); w.Code != http.StatusUnauthorized {
			t.Fatal(w.Code)
		}
	}
	for range 2 {
		w := request(a, "DELETE", path, "", a.token)
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
		if _, err := os.Stat(a.artifactDir(catalog.Haas)); !os.IsNotExist(err) || a.artifacts[catalog.Haas].State != "missing" {
			t.Fatal("deleted artifact remains installed", err)
		}
		if body, err := os.ReadFile(filepath.Join(a.artifactDir(catalog.Parakeet), "fixture")); err != nil || string(body) != catalog.Parakeet {
			t.Fatal("neighbor changed", string(body), err)
		}
	}
	for _, id := range []string{"unknown", "..%2Fsettings.yaml"} {
		if w := request(a, "DELETE", "/api/downloads/"+id, "", a.token); w.Code != http.StatusNotFound {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestDeleteDownloadInUse(t *testing.T) {
	for _, flow := range []string{"self", "world"} {
		for _, phase := range []string{"starting", "running"} {
			t.Run(flow+"/"+phase, func(t *testing.T) {
				a := testApp(t)
				a.flows[flow].status.State = phase
				spoken, _ := a.settings.Flows.Languages(flow)
				for _, id := range wantRequirements(t, spoken) {
					if w := request(a, "DELETE", "/api/downloads/"+id, "", a.token); w.Code != http.StatusConflict {
						t.Fatal(id, w.Code, w.Body.String())
					}
				}
				unused := catalog.Haas
				if spoken == "ja" {
					unused = catalog.Parakeet
				}
				if w := request(a, "DELETE", "/api/downloads/"+unused, "", a.token); w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
			})
		}
	}
	a := testApp(t)
	state := a.artifacts[catalog.Haas]
	state.State = "downloading"
	a.artifacts[catalog.Haas] = state
	if w := request(a, "DELETE", "/api/downloads/"+catalog.Haas, "", a.token); w.Code != http.StatusConflict {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(a, "DELETE", "/api/downloads/"+catalog.Parakeet, "", a.token); w.Code != http.StatusOK {
		t.Fatal("unrelated download blocked deletion", w.Code, w.Body.String())
	}
}

func TestOpenTargets(t *testing.T) {
	a := testApp(t)
	var opened []string
	a.open = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	for _, target := range []string{"data", "settings", "settings"} {
		w := request(a, "POST", "/api/open/"+target, "", a.token)
		if w.Code != http.StatusNoContent {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if !reflect.DeepEqual(opened, []string{a.dir, filepath.Join(a.dir, "settings.yaml"), filepath.Join(a.dir, "settings.yaml")}) {
		t.Fatal(opened)
	}
	if got, err := config.Load(a.dir); err != nil || !reflect.DeepEqual(got, a.settings) {
		t.Fatal(got, err)
	}
	for _, target := range []string{"elsewhere", "settings.yaml", "%2Ftmp%2Ffile"} {
		w := request(a, "POST", "/api/open/"+target, "", a.token)
		if w.Code != http.StatusNotFound {
			t.Fatal(target, w.Code, w.Body.String())
		}
	}
	for _, token := range []string{"", "wrong"} {
		if w := request(a, "POST", "/api/open/data", "", token); w.Code != http.StatusUnauthorized {
			t.Fatal(w.Code)
		}
	}
	if len(opened) != 3 {
		t.Fatal("invalid request reached opener", opened)
	}
	path := filepath.Join(a.dir, "settings.yaml")
	invalid := []byte("unknown: true\n")
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if w := request(a, "POST", "/api/open/settings", "", a.token); w.Code != http.StatusNoContent {
		t.Fatal(w.Code, w.Body.String())
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != string(invalid) {
		t.Fatal("opening existing file rewrote it", string(body), err)
	}
	a.open = func(string) error { return errors.New("opener unavailable") }
	if w := request(a, "POST", "/api/open/data", "", a.token); w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "opener unavailable") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestFileCommand(t *testing.T) {
	for _, tc := range []struct{ goos, executable string }{{"windows", "explorer.exe"}, {"linux", "xdg-open"}} {
		path := filepath.Join(t.TempDir(), "a file.yaml")
		if got := fileCommand(tc.goos, path).Args; !reflect.DeepEqual(got, []string{tc.executable, path}) {
			t.Fatal(got)
		}
	}
}
