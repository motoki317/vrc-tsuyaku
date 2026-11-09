package main

import (
	"encoding/json"
	"net/http"
	"runtime/debug"
	"testing"
)

func TestAppBuildInfo(t *testing.T) {
	for _, tc := range []struct {
		name, raw, version string
		modified           bool
		vcs                bool
	}{
		{name: "tag", raw: "v1.2.3", version: "v1.2.3", vcs: true},
		{name: "dirty tag", raw: "v1.2.3+dirty", version: "v1.2.3", modified: true, vcs: true},
		{name: "prerelease", raw: "v1.2.3-rc.1", version: "v1.2.3-rc.1", vcs: true},
		{name: "pseudo-version", raw: "v1.2.4-0.20261109010203-0123456789ab", vcs: true},
		{name: "dirty pseudo-version", raw: "v1.2.4-0.20261109010203-0123456789ab+dirty", modified: true, vcs: true},
		{name: "development", raw: "(devel)"},
		{name: "invalid version", raw: "release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build := &debug.BuildInfo{Main: debug.Module{Version: tc.raw}}
			want := appInfo{Repository: repository, Version: tc.version, Modified: tc.modified, rawVersion: tc.raw}
			if tc.vcs {
				want.Revision, want.Time = "0123456789abcdef", "2026-11-09T01:02:03Z"
				modified := "false"
				if tc.modified {
					modified = "true"
				}
				build.Settings = []debug.BuildSetting{
					{Key: "vcs.revision", Value: want.Revision},
					{Key: "vcs.time", Value: want.Time},
					{Key: "vcs.modified", Value: modified},
				}
			}
			if got := appBuildInfo(build); got != want {
				t.Fatalf("build info = %+v, want %+v", got, want)
			}
			a := testApp(t)
			a.info = appBuildInfo(build)
			w := request(a, "GET", "/api/state", "", a.token)
			if w.Code != http.StatusOK {
				t.Fatal(w.Code)
			}
			var state struct {
				App map[string]any `json:"app"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if len(state.App) != 5 || state.App["repository"] != want.Repository || state.App["version"] != want.Version || state.App["revision"] != want.Revision || state.App["time"] != want.Time || state.App["modified"] != want.Modified {
				t.Fatalf("app state = %+v, want %+v", state.App, want)
			}
		})
	}
	if got := appBuildInfo(nil); got != (appInfo{Repository: repository, rawVersion: "(devel)"}) {
		t.Fatalf("missing build info = %+v", got)
	}
}
