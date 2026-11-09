package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "-m" {
		for i, arg := range os.Args {
			if arg == "--port" && i+1 < len(os.Args) {
				server := &http.Server{
					Addr: "127.0.0.1:" + os.Args[i+1], ReadHeaderTimeout: time.Second,
					Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != "/health" {
							http.NotFound(w, r)
						}
					}),
				}
				if err := server.ListenAndServe(); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			}
		}
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func testApp(t *testing.T) *app {
	t.Helper()
	a, err := newApp(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.close(); err != nil {
			t.Error(err)
		}
	})
	return a
}

func request(a *app, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}

func TestRequiredArtifactsInState(t *testing.T) {
	a := testApp(t)
	var state struct {
		Required  map[string][]string     `json:"required"`
		Languages catalog.LanguageCatalog `json:"languages"`
	}
	w := request(a, "GET", "/api/state", "", a.token)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	for _, flow := range []string{"self", "world"} {
		spoken, _ := a.settings.Flows.Languages(flow)
		want, _ := catalog.Required(spoken)
		if !reflect.DeepEqual(state.Required[flow], want) {
			t.Fatalf("%s: %v, want %v", flow, state.Required[flow], want)
		}
	}
	if !reflect.DeepEqual(state.Languages, catalog.Languages()) {
		t.Fatal("language choices missing from state", state.Languages)
	}
	s := cloneSettings(a.settings)
	s.Flows.Self.Spoken, s.Flows.World.Spoken = "en", "ja"
	s.Flows.Self.Target, s.Flows.World.Target = "ko", "ja-easy"
	body, _ := json.Marshal(s)
	w = request(a, "PUT", "/api/settings", string(body), a.token)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Required["self"], wantRequirements(t, "en")) || !reflect.DeepEqual(state.Required["world"], wantRequirements(t, "ja")) {
		t.Fatal("requirements did not follow spoken language", state.Required)
	}
}

func wantRequirements(t *testing.T, spoken string) []string {
	t.Helper()
	required, err := catalog.Required(spoken)
	if err != nil {
		t.Fatal(err)
	}
	return required
}

func TestMissingDownloadsLeaveFlowStopped(t *testing.T) {
	for _, flow := range []string{"self", "world"} {
		t.Run(flow, func(t *testing.T) {
			a := testApp(t)
			before := cloneSettings(a.settings)
			response := request(a, "POST", "/api/flows/"+flow+"/start", "", a.token)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "missing downloads:") {
				t.Fatal(response.Code, response.Body.String())
			}
			if got := a.flows[flow].status; got != (flowState{State: "stopped"}) {
				t.Fatal(got)
			}
			history, _ := a.events.after(0)
			if len(history) != 1 || history[0].Flow != flow || history[0].Kind != "error" || history[0].Operation != "start" ||
				!strings.HasPrefix(history[0].Text, "missing downloads:") {
				t.Fatal(history)
			}
			if !reflect.DeepEqual(a.settings, before) {
				t.Fatal("refused start changed settings", a.settings)
			}
			if _, err := os.Stat(filepath.Join(a.dir, "settings.yaml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refused start wrote settings", err)
			}
			for id, state := range a.artifacts {
				state.State = "installed"
				a.artifacts[id] = state
			}
			if got := a.flows[flow].status; got != (flowState{State: "stopped"}) || a.flows[flow].cancel != nil {
				t.Fatal("artifact availability changed the flow", got)
			}
		})
	}
}

func TestStartKeepsActiveRun(t *testing.T) {
	for _, phase := range []string{"starting", "running"} {
		t.Run(phase, func(t *testing.T) {
			a := testApp(t)
			for id, artifact := range a.artifacts {
				artifact.State = "installed"
				a.artifacts[id] = artifact
			}
			var starts atomic.Int32
			a.prepare = func(ctx context.Context, _ string, _ config.Settings) (pipeline.Stages, error) {
				starts.Add(1)
				<-ctx.Done()
				return pipeline.Stages{}, ctx.Err()
			}
			if err := a.start("self", a.settings); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return starts.Load() == 1 })
			a.mu.Lock()
			run := a.flows["self"]
			run.status.State = phase
			a.mu.Unlock()
			before, _ := a.events.after(0)
			results := make(chan *httptest.ResponseRecorder, 2)
			for range 2 {
				go func() { results <- request(a, "POST", "/api/flows/self/start", "", a.token) }()
			}
			for range 2 {
				if response := <-results; response.Code != http.StatusOK {
					t.Fatal(response.Code, response.Body.String())
				}
			}
			a.mu.Lock()
			unchanged := a.flows["self"] == run && run.status.State == phase
			a.mu.Unlock()
			if !unchanged || starts.Load() != 1 {
				t.Fatal("Start replaced an active run")
			}
			saved, err := config.Load(a.dir)
			if err != nil || !saved.Flows.Self.Enabled {
				t.Fatal("Start did not save Enabled", saved, err)
			}
			after, _ := a.events.after(0)
			if len(after) != len(before)+2 {
				t.Fatal("Start emitted run events", after)
			}
			for _, e := range after[len(before):] {
				if e.Kind != "settings" || e.Text != "saved" {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestFailedStartReleasesContext(t *testing.T) {
	a := testApp(t)
	for id, artifact := range a.artifacts {
		artifact.State = "installed"
		a.artifacts[id] = artifact
	}
	contexts := make(chan context.Context, 1)
	a.prepare = func(ctx context.Context, _ string, _ config.Settings) (pipeline.Stages, error) {
		contexts <- ctx
		return pipeline.Stages{}, errors.New("model load failed")
	}
	for range 2 {
		if err := a.changeFlow("self", "start"); err != nil {
			t.Fatal(err)
		}
		ctx := <-contexts
		a.mu.Lock()
		run := a.flows["self"]
		a.mu.Unlock()
		<-run.done
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("failed start retained its context")
		}
	}
}

func TestStopEmitsOnce(t *testing.T) {
	for _, phase := range []string{"stopped", "starting", "running", "error"} {
		t.Run(phase, func(t *testing.T) {
			a := testApp(t)
			a.settings.Flows.World.Enabled = true
			for id, artifact := range a.artifacts {
				artifact.State = "installed"
				a.artifacts[id] = artifact
			}
			ready := make(chan struct{})
			a.prepare = func(ctx context.Context, _ string, _ config.Settings) (pipeline.Stages, error) {
				close(ready)
				<-ctx.Done()
				return pipeline.Stages{}, ctx.Err()
			}
			if phase == "starting" || phase == "running" {
				if err := a.changeFlow("world", "start"); err != nil {
					t.Fatal(err)
				}
				<-ready
			}
			a.mu.Lock()
			a.flows["world"].status.State = phase
			if phase == "error" {
				a.flows["world"].status.Error = "capture failed"
			}
			a.mu.Unlock()
			for attempt := range 2 {
				before, _ := a.events.after(0)
				if err := a.changeFlow("world", "stop"); err != nil {
					t.Fatal(err)
				}
				after, _ := a.events.after(0)
				want := 0
				if attempt == 0 && (phase == "starting" || phase == "running") {
					want = 1
				}
				if len(after) != len(before)+want+1 {
					t.Fatalf("Stop published %d events: %+v", len(after)-len(before), after)
				}
				settings := after[len(before)]
				if settings.Flow != "" || settings.Kind != "settings" || settings.Text != "saved" {
					t.Fatalf("missing settings notification: %+v", settings)
				}
				if want == 1 {
					e := after[len(after)-1]
					if e.Flow != "world" || e.Kind != "state" || e.Text != "stopped" {
						t.Fatalf("Stop event: %+v", e)
					}
				}
				saved, err := config.Load(a.dir)
				if err != nil || saved.Flows.World.Enabled || a.settings.Flows.World.Enabled || a.flows["world"].status != (flowState{State: "stopped"}) {
					t.Fatal("Stop did not clear the saved launch setting and flow status", saved, a.flows["world"].status, err)
				}
			}
		})
	}
}

func TestStopRacesNormalCompletion(t *testing.T) {
	a := testApp(t)
	for id, artifact := range a.artifacts {
		artifact.State = "installed"
		a.artifacts[id] = artifact
	}
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
	for range 100 {
		ready, release := make(chan struct{}), make(chan struct{})
		a.prepare = func(context.Context, string, config.Settings) (pipeline.Stages, error) {
			return pipeline.Stages{
				Source: recordingSource(func(ctx context.Context, _ func([]float32) error) error {
					close(ready)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}),
				Probability: func(context.Context, []float32) (float32, error) { return 0, nil },
				Recognize:   func(context.Context, []float32) (string, error) { return "", nil },
				Translate:   func(context.Context, string, string, string) (string, error) { return "", nil },
			}, nil
		}
		if err := a.changeFlow("world", "start"); err != nil {
			t.Fatal(err)
		}
		<-ready
		history, _ := a.events.after(0)
		lastID := history[len(history)-1].ID
		done := make(chan struct{})
		go func() {
			a.operations.Lock()
			a.stop("world")
			a.operations.Unlock()
			close(done)
		}()
		close(release)
		<-done
		history, _ = a.events.after(lastID)
		if len(history) != 1 || history[0].Kind != "state" || history[0].Text != "stopped" {
			t.Fatalf("completion racing Stop: %+v", history)
		}
	}
}

func TestTokenOnEveryAPI(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"/api/state", "/api/events", "/api/settings", "/api/microphones", "/api/downloads/unknown", "/api/flows/self/start", "/api/unknown"} {
		for _, token := range []string{"", "wrong", a.token + "x"} {
			if got := request(a, "GET", path+"?token="+a.token, "", token).Code; got != http.StatusUnauthorized {
				t.Fatalf("%s: %d", path, got)
			}
		}
	}
	if got := request(a, "GET", "/api/state", "", a.token).Code; got != 200 {
		t.Fatal(got)
	}
	for _, path := range []string{"/", "/app.js", "/style.css"} {
		w := request(a, "GET", path, "", "")
		if w.Code != 200 || strings.Contains(w.Body.String(), a.token) {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestNoNetworkUntilDownload(t *testing.T) {
	var calls atomic.Int32
	old := http.DefaultTransport
	transport := old.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("network disabled by test")
	}
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = old; transport.CloseIdleConnections() }()
	a := testApp(t)
	a.startEnabled()
	for _, path := range []string{"/", "/app.js", "/style.css", "/api/state"} {
		if w := request(a, "GET", path, "", a.token); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	w := request(a, "POST", "/api/flows/self/start", "", a.token)
	if w.Code != 409 || !strings.Contains(w.Body.String(), a.artifacts[catalog.Haas].Artifact.Name) || strings.Contains(w.Body.String(), catalog.Haas) {
		t.Fatal(w.Code, w.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatal("startup or UI made a request")
	}
	w = request(a, "POST", "/api/downloads/"+catalog.PLaMo, `{}`, a.token)
	if w.Code != 409 || calls.Load() != 0 {
		t.Fatal("license gate did not reject before network")
	}
	w = request(a, "POST", "/api/downloads/"+catalog.Silero, `{}`, a.token)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.downloadsWG.Wait()
	if calls.Load() == 0 {
		t.Fatal("explicit download made no request")
	}
	a.mu.Lock()
	state := a.artifacts[catalog.Silero]
	state.State = "installed"
	a.artifacts[catalog.Silero] = state
	a.mu.Unlock()
	count := calls.Load()
	if w = request(a, "POST", "/api/downloads/"+catalog.Silero, `{}`, a.token); w.Code != 409 {
		t.Fatal("installed artifact can be replaced")
	}
	if calls.Load() != count {
		t.Fatal("installed download made network request")
	}
}

func TestSettingsAndFlowLifecycle(t *testing.T) {
	a := testApp(t)
	a.settings.Flows.Self.Enabled = true
	var starts atomic.Int32
	a.mu.Lock()
	for id, state := range a.artifacts {
		state.State = "installed"
		a.artifacts[id] = state
	}
	a.mu.Unlock()
	a.prepare = func(ctx context.Context, name string, s config.Settings) (pipeline.Stages, error) {
		starts.Add(1)
		<-ctx.Done()
		return pipeline.Stages{}, ctx.Err()
	}
	a.startEnabled()
	waitFor(t, func() bool { return starts.Load() == 1 })
	s := cloneSettings(a.settings)
	s.Flows.Self.Microphone = "other"
	body, _ := json.Marshal(s)
	if w := request(a, "PUT", "/api/settings", string(body), a.token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	waitFor(t, func() bool { return starts.Load() == 2 })
	if w := request(a, "POST", "/api/flows/self/stop", "", a.token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	saved, err := config.Load(a.dir)
	if err != nil || saved.Flows.Self.Enabled || saved.Flows.Self.Microphone != "other" {
		t.Fatal(saved, err)
	}
	for _, body := range []string{`{"unknown":true}`, `{} {}`, `{"Chatbox":{"Address":"hostname:9000"}}`} {
		if w := request(a, "PUT", "/api/settings", body, a.token); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}

func TestSettingsValidationAndWriteStatus(t *testing.T) {
	a := testApp(t)
	before := cloneSettings(a.settings)
	s := cloneSettings(before)
	s.Chatbox.Address = "invalid"
	body, _ := json.Marshal(s)
	if response := request(a, "PUT", "/api/settings", string(body), a.token); response.Code != http.StatusBadRequest {
		t.Fatal(response.Code, response.Body.String())
	}
	// A directory at the destination prevents replacement even for a privileged test process.
	if err := os.Mkdir(filepath.Join(a.dir, "settings.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Chatbox.Address = "127.0.0.1:47123"
	body, _ = json.Marshal(s)
	response := request(a, "PUT", "/api/settings", string(body), a.token)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "settings.yaml") {
		t.Fatal(response.Code, response.Body.String())
	}
	if !reflect.DeepEqual(a.settings, before) {
		t.Fatal("failed save changed live settings")
	}
}

func TestSettingsOnlyRestartActiveFlows(t *testing.T) {
	for _, flow := range []string{"self", "world"} {
		for _, phase := range []string{"stopped", "error", "starting", "running"} {
			for _, change := range []string{"enabled", "microphone", "address", "spoken", "target"} {
				t.Run(flow+"/"+phase+"/"+change, func(t *testing.T) {
					a := testApp(t)
					for id, artifact := range a.artifacts {
						artifact.State = "installed"
						a.artifacts[id] = artifact
					}
					s := cloneSettings(a.settings)
					s.Flows.Self.Enabled = flow == "self"
					s.Flows.World.Enabled = flow == "world"
					a.settings = s
					var starts atomic.Int32
					a.prepare = func(ctx context.Context, name string, _ config.Settings) (pipeline.Stages, error) {
						if name != flow {
							t.Errorf("settings started the other flow: %s", name)
						}
						starts.Add(1)
						<-ctx.Done()
						return pipeline.Stages{}, ctx.Err()
					}
					active := phase == "starting" || phase == "running"
					if active {
						if err := a.start(flow, s); err != nil {
							t.Fatal(err)
						}
						waitFor(t, func() bool { return starts.Load() == 1 })
					}
					initial := flowState{State: phase}
					if phase == "error" {
						initial.Error = "capture failed"
					}
					a.mu.Lock()
					a.flows[flow].status = initial
					before := a.flows[flow]
					a.mu.Unlock()
					s.Flows.Self.Enabled = !s.Flows.Self.Enabled
					s.Flows.World.Enabled = !s.Flows.World.Enabled
					switch change {
					case "microphone":
						s.Flows.Self.Microphone = "other"
					case "address":
						s.Chatbox.Address = "127.0.0.1:47123"
					case "spoken":
						if flow == "self" {
							s.Flows.Self.Spoken = "en"
						} else {
							s.Flows.World.Spoken = "ja"
						}
					case "target":
						if flow == "self" {
							s.Flows.Self.Target = "ja-easy"
						} else {
							s.Flows.World.Target = "zh-hant"
						}
					}
					body, _ := json.Marshal(s)
					if response := request(a, "PUT", "/api/settings", string(body), a.token); response.Code != http.StatusOK {
						t.Fatal(response.Code, response.Body.String())
					}
					restart := active && (change == "spoken" || (flow == "self" && (change == "microphone" || change == "address")))
					if restart {
						waitFor(t, func() bool { return starts.Load() == 2 })
					}
					a.mu.Lock()
					after := a.flows[flow]
					status := after.status
					a.mu.Unlock()
					if (before != after) != restart || (!restart && status != initial) {
						t.Fatalf("unexpected restart or state change: restart=%v, status=%+v", before != after, status)
					}
					saved, err := config.Load(a.dir)
					if err != nil || saved.Flows.Self.Enabled != (flow == "self") || saved.Flows.World.Enabled != (flow == "world") {
						t.Fatal("settings changed saved Enabled values", saved, err)
					}
					other := "self"
					if flow == other {
						other = "world"
					}
					if a.flows[other].status.State != "stopped" {
						t.Fatal("settings started the other flow")
					}
				})
			}
		}
	}
}

func TestSettingsCannotUndoStop(t *testing.T) {
	a := testApp(t)
	a.settings.Flows.Self.Enabled = true
	a.settings.Flows.World.Enabled = true
	stale := cloneSettings(a.settings)
	for _, flow := range []string{"self", "world"} {
		if response := request(a, "POST", "/api/flows/"+flow+"/stop", "", a.token); response.Code != http.StatusOK {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	stale.Flows.Self.Microphone = "other"
	body, _ := json.Marshal(stale)
	if response := request(a, "PUT", "/api/settings", string(body), a.token); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	saved, err := config.Load(a.dir)
	if err != nil || saved.Flows.Self.Enabled || saved.Flows.World.Enabled || saved.Flows.Self.Microphone != "other" {
		t.Fatal("stale settings undid Stop", saved, err)
	}
	if a.settings.Flows.Self.Enabled || a.settings.Flows.World.Enabled {
		t.Fatal("stale settings changed the in-memory launch state")
	}
}

func waitFor(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEventStream(t *testing.T) {
	a := testApp(t)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/events", nil)
	r.Header.Set("Authorization", "Bearer "+a.token)
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	a.events.publish("self", pipeline.Event{PhraseID: 42, Kind: "recognized", Text: "hello"})
	var e event
	if err = json.NewDecoder(response.Body).Decode(&e); err != nil || e.Text != "hello" || e.Flow != "self" || e.PhraseID != 42 || e.ID != 1 {
		t.Fatal(e, err)
	}
	cancel()
	for i := range 300 {
		a.emit("self", "page", fmt.Sprint(i))
	}
	history, _ := a.events.after(0)
	if len(history) != 256 || history[len(history)-1].Text != "299" {
		t.Fatal("history not bounded")
	}
}

func TestDirectoryAndErrors(t *testing.T) {
	dir, err := dataDirectory(".")
	if err != nil || !filepath.IsAbs(dir) {
		t.Fatal(dir, err)
	}
	executable, _ := os.Executable()
	resolved, _ := filepath.EvalSymlinks(executable)
	dir, err = dataDirectory("")
	if err != nil || dir != filepath.Dir(resolved) {
		t.Fatal(dir, err)
	}
	for _, cause := range []error{os.ErrPermission, syscall.EROFS} {
		wrapped := fmt.Errorf("settings: %w", &os.PathError{Op: "open", Path: "settings.yaml", Err: cause})
		if !strings.Contains(withDataDirHint(wrapped).Error(), "-data-dir") {
			t.Fatal(wrapped)
		}
	}
	for _, cause := range []error{errors.New("invalid settings"), context.Canceled, &net.OpError{Op: "dial", Err: os.ErrPermission}} {
		if strings.Contains(withDataDirHint(cause).Error(), "-data-dir") {
			t.Fatal(cause)
		}
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-ort", "file"}, &out, &out); err == nil {
		t.Fatal("accepted ORT override")
	}
}

func TestLicenseAcceptanceAfterNullSettingsMap(t *testing.T) {
	a := testApp(t)
	s := config.Defaults()
	s.Flows.Self.Enabled = false
	s.LicensesAccepted = nil
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	a.cancel()
	if err := a.startDownload(catalog.PLaMo, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	saved, err := config.Load(a.dir)
	if err != nil || saved.LicensesAccepted[catalog.PLaMoAcceptanceKey] == "" {
		t.Fatal(saved, err)
	}
}

func TestSettingsPreserveLicenseAcceptance(t *testing.T) {
	a := testApp(t)
	s := config.Defaults()
	s.Flows.Self.Enabled = false
	save := func(acceptances map[string]string) config.Settings {
		t.Helper()
		s.LicensesAccepted = acceptances
		body, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if response := request(a, "PUT", "/api/settings", string(body), a.token); response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		saved, err := config.Load(a.dir)
		if err != nil {
			t.Fatal(err)
		}
		if !maps.Equal(saved.LicensesAccepted, a.settings.LicensesAccepted) {
			t.Fatal("saved and in-memory acceptances differ")
		}
		return saved
	}
	if saved := save(map[string]string{catalog.PLaMoAcceptanceKey: "2020-01-01T00:00:00Z"}); len(saved.LicensesAccepted) != 0 {
		t.Fatal("settings recorded an acceptance")
	}
	a.cancel() // Record explicit acceptance without starting a network request.
	if err := a.startDownload(catalog.PLaMo, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	accepted, err := config.Load(a.dir)
	if err != nil || accepted.LicensesAccepted[catalog.PLaMoAcceptanceKey] == "" {
		t.Fatal(accepted, err)
	}
	for _, submitted := range []map[string]string{nil, {}, {catalog.PLaMoAcceptanceKey: "2020-01-01T00:00:00Z", "other-license": "2020-01-01T00:00:00Z"}} {
		if saved := save(submitted); !maps.Equal(saved.LicensesAccepted, accepted.LicensesAccepted) {
			t.Fatalf("settings changed acceptance: %v", saved.LicensesAccepted)
		}
	}
}

func TestStopQueuedModelStartup(t *testing.T) {
	a := testApp(t)
	a.mu.Lock()
	for id, state := range a.artifacts {
		state.State = "installed"
		a.artifacts[id] = state
	}
	a.mu.Unlock()
	a.modelTurn <- struct{}{} // Another flow is still initializing its models.
	defer func() { <-a.modelTurn }()
	if err := a.changeFlow("world", "start"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.changeFlow("world", "stop") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued start blocked Stop")
	}
}

func TestInvalidSettingsAreNotReplacedByOtherActions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yaml")
	original := []byte("unknown_option: true\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := newApp(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	for _, path := range []string{"/api/flows/self/start", "/api/downloads/" + catalog.PLaMo} {
		if response := request(a, "POST", path, `{"acceptLicense":true}`, a.token); response.Code != 409 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("settings changed: %s, %v", got, err)
	}
}
