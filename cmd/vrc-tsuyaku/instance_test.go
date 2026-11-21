package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/download"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/instance"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
)

func testMembership(t *testing.T, dir, url string, pid int) *instance.Membership {
	t.Helper()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := instance.Join(dir, instance.Info{URL: url, PID: pid, Host: host, Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m
}

func waitingApp(t *testing.T, url string, pid int) (*app, *instance.Membership) {
	t.Helper()
	dir := t.TempDir()
	other := testMembership(t, dir, url, pid)
	a, err := newApp(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.stopGrace != 10*time.Second || a.killGrace != 5*time.Second || a.instancePoll != time.Second {
		t.Fatalf("instance defaults: stopGrace = %v, killGrace = %v, instancePoll = %v", a.stopGrace, a.killGrace, a.instancePoll)
	}
	a.instancePoll, a.stopGrace, a.killGrace = time.Millisecond, 10*time.Millisecond, 10*time.Millisecond
	a.kill = func(pid int) error {
		t.Errorf("unexpected kill of PID %d", pid)
		return errors.New("kill refused by test")
	}
	if err := a.joinInstance("http://127.0.0.1:50001/"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.close(); err != nil {
			t.Error(err)
		}
		if err := a.membership.Close(); err != nil {
			t.Error(err)
		}
	})
	return a, other
}

func instanceSnapshot(t *testing.T, a *app) instanceState {
	t.Helper()
	w := request(a, "GET", "/api/state", "")
	var state struct {
		Instance instanceState `json:"instance"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || w.Code != http.StatusOK {
		t.Fatalf("state = %d %s, err = %v", w.Code, w.Body.String(), err)
	}
	return state.Instance
}

func instanceRequest(t *testing.T, a *app, path string, status int) *httptest.ResponseRecorder {
	t.Helper()
	w := request(a, "POST", path, "")
	if w.Code != status {
		t.Fatalf("POST %s = %d %s, want %d", path, w.Code, w.Body.String(), status)
	}
	return w
}

func TestInstanceGateAndAutomaticStart(t *testing.T) {
	a, other := waitingApp(t, "http://127.0.0.1:50000/", 4242)
	for id, state := range a.artifacts {
		state.State = "installed"
		a.artifacts[id] = state
	}
	a.settings.Flows.Self.Enabled, a.settings.Flows.World.Enabled = true, true
	if err := a.writeSettings(a.settings); err != nil {
		t.Fatal(err)
	}
	var prepared, downloaded atomic.Int32
	a.prepare = func(ctx context.Context, _ config.Flow, _ config.Settings) (pipeline.Stages, error) {
		prepared.Add(1)
		<-ctx.Done()
		return pipeline.Stages{}, ctx.Err()
	}
	a.downloadArtifact = func(context.Context, string, string, func(download.Progress)) error {
		downloaded.Add(1)
		return errors.New("unexpected download")
	}
	a.startEnabled()
	initial := instanceSnapshot(t, a)
	if initial.State != "waiting" || len(initial.Others) != 1 || initial.Others[0].Slot != 0 || initial.Others[0].PID != 4242 || initial.Others[0].Host != a.instanceHost || initial.Others[0].URL != "http://127.0.0.1:50000/" || initial.Others[0].Started.IsZero() {
		t.Fatalf("waiting snapshot = %+v", initial)
	}
	assertPending := func() {
		t.Helper()
		for _, call := range []struct{ path, body string }{
			{"/api/flows/self/start", ""}, {"/api/flows/world/start", ""},
			{"/api/downloads/" + catalog.ONNXRuntime, `{"acceptLicense":true}`},
		} {
			w := request(a, "POST", call.path, call.body)
			var failure struct{ Code errcode.Code }
			if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil || w.Code != http.StatusConflict || failure.Code != errcode.InstancePending {
				t.Fatalf("waiting gate %s = %d %s, err = %v", call.path, w.Code, w.Body.String(), err)
			}
		}
	}
	assertPending()
	// A missing-download retry after a settings reload also goes through the gate.
	a.flows["self"].status = flowState{State: "error", Code: errcode.MissingDownloads}
	s := cloneSettings(a.settings)
	s.Flows.Self.Spoken, s.Flows.Self.Target = "en", "ja"
	if err := config.Save(a.dir, s); err != nil {
		t.Fatal(err)
	}
	pollSettings(a)
	assertPending()
	a.operations.Lock()
	a.startWaitingFlows()
	a.operations.Unlock()
	if prepared.Load() != 0 || downloaded.Load() != 0 || a.engine != nil || a.translator != nil {
		t.Fatal("waiting instance prepared a model or started a download")
	}
	a.mu.Lock()
	a.settingsError = "synthetic invalid settings"
	a.mu.Unlock()
	assertPending()
	a.mu.Lock()
	a.settingsError = ""
	a.mu.Unlock()
	a.watchInstances()
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return prepared.Load() == 2 })
	if got := instanceSnapshot(t, a); got.State != "" || got.Others != nil {
		t.Fatalf("automatic start snapshot = %+v", got)
	}
	if rows, err := a.membership.Others(); err != nil || len(rows) != 0 {
		t.Fatalf("normal membership = %+v, %v", rows, err)
	}
	history, _ := a.events.after(0)
	if !slices.ContainsFunc(history, func(e event) bool { return e.Flow == "" && e.Kind == "state" && e.Text == "" }) {
		t.Fatal("automatic start did not publish a state event")
	}
}

func TestInstanceStartBeside(t *testing.T) {
	a, other := waitingApp(t, "http://127.0.0.1:50000/", 4242)
	a.settings.Flows.Self.Enabled = true
	for id, state := range a.artifacts {
		state.State = "installed"
		a.artifacts[id] = state
	}
	var starts atomic.Int32
	a.prepare = func(ctx context.Context, _ config.Flow, _ config.Settings) (pipeline.Stages, error) {
		starts.Add(1)
		<-ctx.Done()
		return pipeline.Stages{}, ctx.Err()
	}
	w := instanceRequest(t, a, "/api/instance/start", http.StatusOK)
	if !strings.Contains(w.Body.String(), `"instance":{"state":""}`) {
		t.Fatal("normal snapshot includes waiting rows", w.Body.String())
	}
	waitFor(t, func() bool { return starts.Load() == 1 })
	instanceRequest(t, a, "/api/instance/start", http.StatusConflict)
	instanceRequest(t, a, "/api/instance/stop/0", http.StatusConflict)
	if rows, err := other.Others(); err != nil || len(rows) != 1 || rows[0].PID != os.Getpid() || rows[0].Host != a.instanceHost {
		t.Fatalf("starting beside released the membership: %+v, %v", rows, err)
	}
}

func TestInstanceStopGraceful(t *testing.T) {
	requested, respond := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/exit" {
			t.Errorf("exit request = %s %s", r.Method, r.URL)
		}
		close(requested)
		select {
		case <-respond:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	a, other := waitingApp(t, server.URL+"/", 4242)
	instanceRequest(t, a, "/api/instance/stop/99", http.StatusNotFound)
	instanceRequest(t, a, "/api/instance/stop/bad", http.StatusNotFound)
	instanceRequest(t, a, "/api/instance/stop/0", http.StatusAccepted)
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not post to the listed instance")
	}
	if err := a.refreshInstances(); err != nil {
		t.Fatal(err)
	}
	if row := instanceSnapshot(t, a).Others[0]; row.State != "stopping" || row.Error != "" || row.Code != "" {
		t.Fatalf("rescan lost stop progress: %+v", row)
	}
	instanceRequest(t, a, "/api/instance/stop/0", http.StatusConflict)
	a.watchInstances()
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	close(respond)
	waitFor(t, func() bool { return instanceSnapshot(t, a).State == "" })
}

func TestInstanceStopRequiresSameNonemptyHost(t *testing.T) {
	for _, hosts := range []struct{ self, other string }{
		{"local", "remote"}, {"", "remote"}, {"local", ""}, {"", ""},
	} {
		t.Run(hosts.self+"/"+hosts.other, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a, _ := waitingApp(t, "http://127.0.0.1:50000/", 4242)
				a.instanceHost = hosts.self
				row := a.instance.Others[0]
				row.Host = hosts.other
				data, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(a.dir, "instances", "0.lock"), data, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := a.refreshInstances(); err != nil {
					t.Fatal(err)
				}
				original := http.DefaultTransport
				http.DefaultTransport = updateTransport(func(*http.Request) (*http.Response, error) {
					t.Error("sent an exit request for another or unknown host")
					return nil, errors.New("unexpected request")
				})
				t.Cleanup(func() { http.DefaultTransport = original })
				started := time.Now()
				instanceRequest(t, a, "/api/instance/stop/0", http.StatusAccepted)
				a.instanceWG.Wait()
				if got := instanceSnapshot(t, a).Others[0]; got.State != "" || got.Code != errcode.InstanceNotStopped || got.Error == "" || got.Host != hosts.other {
					t.Fatalf("stop on another or unknown host = %+v", got)
				}
				if elapsed := time.Since(started); elapsed != 0 {
					t.Fatalf("stop on another host waited %v", elapsed)
				}
			})
		})
	}
}

func TestInstanceStopGraceIncludesExitRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, other := waitingApp(t, "http://127.0.0.1:50000/", 4242)
		original := http.DefaultTransport
		http.DefaultTransport = updateTransport(func(*http.Request) (*http.Response, error) {
			time.Sleep(4 * time.Second)
			return nil, errors.New("slow exit request failed")
		})
		t.Cleanup(func() { http.DefaultTransport = original })
		a.stopGrace, a.killGrace, a.instancePoll = 10*time.Second, 5*time.Second, time.Second
		started := time.Now()
		var killedAfter time.Duration
		a.kill = func(int) error {
			killedAfter = time.Since(started)
			return other.Close()
		}
		instanceRequest(t, a, "/api/instance/stop/0", http.StatusAccepted)
		a.instanceWG.Wait()
		if killedAfter != 10*time.Second {
			t.Fatalf("kill after %v, want 10 s including the exit request", killedAfter)
		}
		if got := instanceSnapshot(t, a); got.State != "" {
			t.Fatalf("stop did not release the slot: %+v", got)
		}
	})
}

func TestInstanceRescanReportsOnlyChangedErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _ := waitingApp(t, "http://127.0.0.1:50000/", 4242)
		path := filepath.Join(a.dir, "instances", "0.lock")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		a.watchInstances()
		check := func(data []byte, want int) {
			t.Helper()
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(3 * a.instancePoll)
			synctest.Wait()
			history, _ := a.events.after(0)
			var errors []string
			for _, event := range history {
				if event.Kind == "error" {
					errors = append(errors, event.Text)
				}
			}
			if len(errors) != want {
				t.Fatalf("rescan error events = %v, want %d", errors, want)
			}
		}
		check([]byte("{"), 1)
		check([]byte("!"), 2)
		check(original, 2)
		check([]byte("!"), 3)
	})
}

func TestInstanceStopFailureAndRetry(t *testing.T) {
	a, _ := waitingApp(t, "unusable URL", 4242)
	var kills atomic.Int32
	a.kill = func(pid int) error {
		if pid != 4242 {
			t.Errorf("kill PID = %d, want 4242", pid)
		}
		others, err := a.membership.Others()
		if err != nil || !slices.ContainsFunc(others, func(info instance.Info) bool { return info.Slot == 0 && info.PID == pid }) {
			t.Errorf("kill target no longer owns its slot: %+v, %v", others, err)
		}
		kills.Add(1)
		return errors.New("synthetic kill failure")
	}
	for attempt := int32(1); attempt <= 2; attempt++ {
		instanceRequest(t, a, "/api/instance/stop/0", http.StatusAccepted)
		waitFor(t, func() bool {
			row := instanceSnapshot(t, a).Others[0]
			return kills.Load() == attempt && row.State == "" && row.Code == errcode.InstanceNotStopped
		})
		before := instanceSnapshot(t, a).Others[0]
		if !strings.Contains(before.Error, "synthetic kill failure") {
			t.Fatal("stop failure has no diagnostic", before)
		}
		if err := a.refreshInstances(); err != nil {
			t.Fatal(err)
		}
		if got := instanceSnapshot(t, a).Others[0]; got != before {
			t.Fatalf("rescan lost stop error: %+v, want %+v", got, before)
		}
	}
}

func TestInstanceStopKillReleasesSlot(t *testing.T) {
	a, other := waitingApp(t, "unusable URL", 4242)
	called := make(chan int, 1)
	a.kill = func(pid int) error {
		called <- pid
		if err := other.Close(); err != nil {
			t.Error(err)
		}
		return errors.New("process already exited")
	}
	w := request(a, "POST", "/api/instance/stop/0", `{"pid":9999,"url":"http://evil.example/"}`)
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	waitFor(t, func() bool { return instanceSnapshot(t, a).State == "" })
	if pid := <-called; pid != 4242 {
		t.Fatalf("kill trusted request data: %d", pid)
	}
}

func TestInstanceStopNeverKillsSelf(t *testing.T) {
	a, _ := waitingApp(t, "unusable URL", os.Getpid())
	instanceRequest(t, a, "/api/instance/stop/0", http.StatusAccepted)
	waitFor(t, func() bool { return instanceSnapshot(t, a).Others[0].Code == errcode.InstanceNotStopped })
}

func TestInstanceStopSlotReplacement(t *testing.T) {
	requested, respond := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requested)
		select {
		case <-respond:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	a, other := waitingApp(t, server.URL+"/", 4242)
	instanceRequest(t, a, "/api/instance/stop/0", http.StatusAccepted)
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not post to the original instance")
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	testMembership(t, a.dir, "invalid replacement URL", 4243)
	if err := a.refreshInstances(); err != nil {
		t.Fatal(err)
	}
	close(respond)
	a.instanceWG.Wait()
	rows := instanceSnapshot(t, a).Others
	if len(rows) != 1 || rows[0].Slot != 0 || rows[0].PID != 4243 || rows[0].State != "" || rows[0].Error != "" || rows[0].Code != "" {
		t.Fatalf("replacement inherited old row state: %+v", rows)
	}
}

func TestInstanceStopCancellation(t *testing.T) {
	a, _ := waitingApp(t, "unusable URL", 4242)
	a.stopGrace = time.Hour
	instanceRequest(t, a, "/api/instance/stop/0", http.StatusAccepted)
	a.cancel()
	done := make(chan struct{})
	go func() { a.instanceWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the stop wait")
	}
}

func TestInstanceURL(t *testing.T) {
	for _, url := range []string{"http://127.0.0.1:1/", "http://127.0.0.1:65535/", "http://127.0.0.1:0123/"} {
		if !instanceURL(url) {
			t.Errorf("refused loopback URL %q", url)
		}
	}
	for _, url := range []string{
		"", "http://localhost:80/", "http://127.0.0.2:80/", "http://[::1]:80/", "https://127.0.0.1:80/",
		"http://user@127.0.0.1:80/", "http://127.0.0.1:80@evil.example/", "http://127.0.0.1:80",
		"http://127.0.0.1:80//", "http://127.0.0.1:80/api/", "http://127.0.0.1:80/?q=1", "http://127.0.0.1:80/#f",
		"http://127.0.0.1:0/", "http://127.0.0.1:65536/", "http://127.0.0.1:-1/", "http://127.0.0.1:+80/", "http://127.0.0.1:８０/",
	} {
		if instanceURL(url) {
			t.Errorf("accepted non-loopback URL %q", url)
		}
	}
}

func TestInstanceExitRequestTimeoutAndFailure(t *testing.T) {
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	var posts atomic.Int32
	transport.RegisterProtocol("http", updateTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
			t.Errorf("exit request has no 5 s timeout: %v, %t", deadline, ok)
		}
		if r.Method != "POST" || r.URL.String() != "http://127.0.0.1:50000/api/exit" {
			t.Errorf("exit request = %s %s", r.Method, r.URL)
		}
		posts.Add(1)
		return nil, errors.New("synthetic request failure")
	}))
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original; transport.CloseIdleConnections() })
	a, other := waitingApp(t, "http://127.0.0.1:50000/", 4242)
	a.kill = func(int) error { return other.Close() }
	instanceRequest(t, a, "/api/instance/stop/0", http.StatusAccepted)
	waitFor(t, func() bool { return instanceSnapshot(t, a).State == "" })
	if posts.Load() != 1 {
		t.Fatal("exit request did not run exactly once")
	}
}

func TestInstanceExitRequestDoesNotFollowRedirect(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	t.Cleanup(target.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	a, other := waitingApp(t, server.URL+"/", 4242)
	a.kill = func(int) error { return other.Close() }
	instanceRequest(t, a, "/api/instance/stop/0", http.StatusAccepted)
	waitFor(t, func() bool { return instanceSnapshot(t, a).State == "" })
	if redirected.Load() != 0 {
		t.Fatal("exit request followed a redirect")
	}
}

func TestInstanceInvalidURLRemainsListedWithoutRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	t.Cleanup(server.Close)
	a, other := waitingApp(t, server.URL+"/untrusted/", 4242)
	if rows := instanceSnapshot(t, a).Others; len(rows) != 1 || rows[0].URL != server.URL+"/untrusted/" {
		t.Fatalf("invalid URL silently disappeared: %+v", rows)
	}
	a.kill = func(int) error { return other.Close() }
	instanceRequest(t, a, "/api/instance/stop/0", http.StatusAccepted)
	waitFor(t, func() bool { return instanceSnapshot(t, a).State == "" })
	if requests.Load() != 0 {
		t.Fatal("stop sent a request to an invalid slot URL")
	}
}
