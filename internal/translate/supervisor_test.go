package translate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

type fakeConfig struct {
	Directory  string
	Fail       bool
	Concurrent bool
	TakePort   bool
	NoAddress  bool
}

type fakeReply struct {
	Status   int
	Body     string
	Location string
	Block    bool
}

type recordedRequest struct {
	Method, ContentType string
	Body                json.RawMessage
}

type fakeRecord struct {
	PID          int
	Address      string
	Args         []string
	TakenAddress string
}

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "-m" {
		if err := fakeServer(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	if path := os.Getenv("VRC_TSUYAKU_TEST_TRANSLATE_PARENT"); path != "" {
		binary, err := os.Executable()
		if err != nil {
			panic(err)
		}
		s, err := New(binary, path)
		if err != nil {
			panic(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := s.Start(ctx); err != nil {
			panic(err)
		}
		cancel()
		fmt.Println("ready")
		select {} // The test kills this app without running cleanup.
	}
	os.Exit(m.Run())
}

func fakeServer() error {
	data, err := os.ReadFile(os.Args[2])
	if err != nil {
		return err
	}
	var cfg fakeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if cfg.Fail {
		return errors.New("fixture: cannot load model")
	}
	args := os.Args[1:]
	if len(args) < 2 {
		return errors.New("missing server arguments")
	}
	addr := "127.0.0.1:" + args[len(args)-1]
	var takenAddress string
	if cfg.TakePort {
		taken, err := net.Listen("tcp4", addr)
		if err != nil {
			return err
		}
		defer taken.Close()
		takenAddress = "http://" + taken.Addr().String()
	}
	l, err := net.Listen("tcp4", addr)
	if err != nil {
		return err
	}
	defer l.Close()
	addr = l.Addr().String()
	if !cfg.NoAddress {
		fmt.Fprintf(os.Stderr, "srv  server_on_li: listening on http://%s\n", addr)
	}
	record, _ := json.Marshal(fakeRecord{PID: os.Getpid(), Address: "http://" + addr, Args: args, TakenAddress: takenAddress})
	if err := os.WriteFile(filepath.Join(cfg.Directory, "record.json"), record, 0600); err != nil {
		return err
	}
	mux := http.NewServeMux()
	var mu sync.Mutex
	arrivals := 0
	paired := make(chan struct{})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_ = os.WriteFile(filepath.Join(cfg.Directory, "probed"), []byte(r.Method), 0600)
		if _, err := os.Stat(filepath.Join(cfg.Directory, "ready")); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		mu.Lock()
		file, err := os.OpenFile(filepath.Join(cfg.Directory, "requests.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			err = json.NewEncoder(file).Encode(recordedRequest{r.Method, r.Header.Get("Content-Type"), data})
			file.Close()
		}
		arrivals++
		if arrivals == 2 {
			close(paired)
		}
		mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if cfg.Concurrent {
			select {
			case <-paired:
			case <-r.Context().Done():
				return
			}
		}
		reply := fakeReply{Status: http.StatusOK, Body: `{"choices":[{"message":{"content":" translated text "}}]}`}
		if data, err := os.ReadFile(filepath.Join(cfg.Directory, "reply.json")); err == nil {
			if err := json.Unmarshal(data, &reply); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		if reply.Block {
			<-r.Context().Done()
			return
		}
		if reply.Location != "" {
			w.Header().Set("Location", reply.Location)
		}
		w.WriteHeader(reply.Status)
		_, _ = w.Write([]byte(reply.Body))
	})
	return http.Serve(l, mux)
}

func fixture(t *testing.T, ready, fail bool) (*Supervisor, string) {
	t.Helper()
	dir := t.TempDir()
	if ready {
		if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	model := filepath.Join(dir, "model with spaces.gguf")
	data, _ := json.Marshal(fakeConfig{Directory: dir, Fail: fail})
	if err := os.WriteFile(model, data, 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(binary, model)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, dir
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readRecord(t *testing.T, dir string) fakeRecord {
	t.Helper()
	var record fakeRecord
	eventually(t, func() bool {
		data, err := os.ReadFile(filepath.Join(dir, "record.json"))
		return err == nil && json.Unmarshal(data, &record) == nil
	})
	return record
}

func TestPaths(t *testing.T) {
	for _, paths := range [][2]string{{"", "model"}, {"server", ""}, {"", ""}} {
		if _, err := New(paths[0], paths[1]); err == nil {
			t.Fatal("accepted empty path")
		}
	}
	s, err := New("missing-server", "model")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(s.serverPath) || !filepath.IsAbs(s.modelPath) {
		t.Fatal("paths are not absolute")
	}
	if err := s.Start(t.Context()); err == nil {
		t.Fatal("started missing executable")
	}
}

func TestSupervisorLifecycle(t *testing.T) {
	s, dir := fixture(t, false, false)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started := make(chan error, 1)
	go func() { started <- s.Start(ctx) }()
	record := readRecord(t, dir)
	eventually(t, func() bool {
		data, err := os.ReadFile(filepath.Join(dir, "probed"))
		return err == nil && string(data) == "GET"
	})
	select {
	case err := <-started:
		t.Fatalf("Start returned before health was ready: %v", err)
	default:
	}
	_, port, err := net.SplitHostPort(strings.TrimPrefix(record.Address, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if boundPort, err := strconv.Atoi(port); err != nil || boundPort == 0 {
		t.Fatalf("server did not report its bound port: %q (%v)", port, err)
	}
	want := []string{"-m", s.modelPath, "--alias", "plamo-3-translate", "--jinja", "--no-prefill-assistant", "-c", "4096", "-np", "2", "--cache-ram", "0", "-t", "8", "--no-log-jsonl", "--log-colors", "off", "--log-verbosity", "3", "--host", "127.0.0.1", "--port", "0"}
	if !reflect.DeepEqual(record.Args, want) {
		t.Fatalf("args = %q, want %q", record.Args, want)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	run := s.run
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if s.run != run {
		t.Fatal("second Start replaced the running server")
	}
	if err := run.child.kill(); err != nil {
		t.Fatal(err)
	}
	<-run.child.done
	if err := run.child.exitError(); errcode.Of(err) != errcode.TranslatorExited || !strings.Contains(err.Error(), "llama-server exited") {
		t.Fatalf("exit error: %v", err)
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if s.run == run {
		t.Fatal("did not relaunch after exit")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStartupWithTakenPort(t *testing.T) {
	s, dir := fixture(t, true, false)
	data, _ := json.Marshal(fakeConfig{Directory: dir, TakePort: true})
	if err := os.WriteFile(s.modelPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start after another listener takes the selected port: %v", err)
	}
	record := readRecord(t, dir)
	if record.TakenAddress == "" || record.TakenAddress == record.Address {
		t.Fatalf("server did not avoid the occupied port: %+v", record)
	}
	if _, err := s.Translate(ctx, "ja", "en", "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestStartupFailureAndCancellation(t *testing.T) {
	t.Run("model error", func(t *testing.T) {
		s, _ := fixture(t, true, true)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		if err := s.Start(ctx); errcode.Of(err) != errcode.TranslatorExited || !strings.Contains(err.Error(), "fixture: cannot load model") {
			t.Fatalf("startup error = %v", err)
		}
	})
	for _, noAddress := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel loading/no_address=%t", noAddress), func(t *testing.T) {
			s, dir := fixture(t, false, false)
			data, _ := json.Marshal(fakeConfig{Directory: dir, NoAddress: noAddress})
			if err := os.WriteFile(s.modelPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started := make(chan error, 1)
			go func() { started <- s.Start(ctx) }()
			readRecord(t, dir)
			if noAddress {
				if _, err := s.Translate(ctx, "ja", "en", "hello"); err == nil || !strings.Contains(err.Error(), "listening address") {
					t.Fatalf("translated before the address arrived: %v", err)
				}
			}
			cancel()
			if err := <-started; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel startup: %v", err)
			}
			select {
			case <-s.run.child.done:
			default:
				t.Fatal("cancelled startup left child running")
			}
		})
	}
}

func TestParentDeath(t *testing.T) {
	s, dir := fixture(t, true, false)
	app := exec.Command(s.serverPath)
	app.Env = append(os.Environ(), "VRC_TSUYAKU_TEST_TRANSLATE_PARENT="+s.modelPath)
	app.Stdout, app.Stderr = os.Stderr, os.Stderr
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Process.Kill(); _ = app.Wait() })
	record := readRecord(t, dir)
	assertDead := watchProcess(t, record.PID)
	if err := app.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	assertDead()
}

func TestLogTailBound(t *testing.T) {
	var log logTail
	_, _ = log.Write([]byte(strings.Repeat("a", 20000)))
	_, _ = log.Write([]byte("diagnostic"))
	if got := log.String(); len(got) != 8192 || !strings.HasSuffix(got, "diagnostic") {
		t.Fatal("log did not retain its bounded tail")
	}
}

func TestListeningAddress(t *testing.T) {
	for _, prefix := range []string{"", "srv  server_on_li: ", "srv  server_on_listening: "} {
		announcement := prefix + "listening on http://127.0.0.1:54321\n"
		for split := 0; split <= len(announcement); split++ {
			var log logTail
			_, _ = log.Write([]byte(announcement[:split]))
			if split < len(announcement) && log.URL() != "" {
				t.Fatalf("accepted an incomplete announcement at byte %d", split)
			}
			_, _ = log.Write([]byte(announcement[split:] + strings.Repeat("diagnostic\n", 2000)))
			_, _ = log.Write([]byte("srv  server_on_li: listening on http://127.0.0.1:12345\n"))
			if log.URL() != "http://127.0.0.1:54321" {
				t.Fatalf("lost the first bound address at byte %d: %q", split, log.URL())
			}
			if len(log.String()) != 8192 {
				t.Fatal("endpoint capture changed the diagnostic log bound")
			}
		}
	}
	for _, address := range []string{
		"http://evil.example:54321", "http://localhost:54321", "http://127.0.0.2:54321",
		"https://127.0.0.1:54321", "http://127.0.0.1:0", "http://127.0.0.1:-1",
		"http://127.0.0.1:65536", "http://127.0.0.1:54321/path", "http://127.0.0.1:54321@evil.example",
	} {
		var log logTail
		_, _ = log.Write([]byte("srv  server_on_listening: listening on " + address + "\n"))
		if log.URL() != "" {
			t.Fatalf("accepted invalid listening address %q", address)
		}
	}
}

func TestWaitReportsIdleExit(t *testing.T) {
	s, _ := fixture(t, true, false)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := s.Wait(ctx); err == nil {
		t.Fatal("wait before start succeeded")
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	stopped, cancelWait := context.WithCancel(ctx)
	cancelWait()
	if err := s.Wait(stopped); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait(ctx) }()
	if err := s.run.child.kill(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "llama-server exited") {
		t.Fatal(err)
	}
}

func TestWaitCancellationDuringStartup(t *testing.T) {
	s, dir := fixture(t, false, false)
	startup, cancelStart := context.WithCancel(t.Context())
	defer cancelStart()
	started := make(chan error, 1)
	go func() { started <- s.Start(startup) }()
	eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "probed"))
		return err == nil
	})
	waiting, cancelWait := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancelWait()
	done := make(chan error, 1)
	go func() { done <- s.Wait(waiting) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Error(err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("Wait ignored its cancellation during startup")
	}
	cancelStart()
	<-started
}
