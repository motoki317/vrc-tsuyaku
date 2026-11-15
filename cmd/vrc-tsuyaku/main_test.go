package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type startupWriter struct {
	mu        sync.Mutex
	output    strings.Builder
	url       chan string
	exiting   chan struct{}
	written   chan struct{}
	blockExit <-chan struct{}
}

func (w *startupWriter) Write(p []byte) (int, error) {
	text := string(p)
	exiting := strings.HasPrefix(text, "Exiting...\n")
	if exiting {
		close(w.exiting)
		if w.blockExit != nil {
			<-w.blockExit
		}
	}
	w.mu.Lock()
	w.output.WriteString(text)
	w.mu.Unlock()
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http://127.0.0.1:") {
			select {
			case w.url <- line:
			default:
			}
		}
	}
	if exiting {
		close(w.written)
	}
	return len(p), nil
}

func (w *startupWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}

func TestConsoleStartup(t *testing.T) {
	const address = "http://127.0.0.1:54321/"
	const hint = "\n\n    " + address + "\n\n" +
		"The app exits when you close the browser tab or this window.\n" +
		"ブラウザーのタブか、このウィンドウを閉じると、終了します。\n"
	for _, version := range []string{"", "v1.2.3"} {
		for _, tc := range []struct {
			name, message string
			opened        bool
		}{
			{"browser opened", "The page is open in your browser. If you do not see it, open this address in a browser:\n" +
				"操作画面をブラウザーで開きました。表示されないときは、次のアドレスをブラウザーで開いてください。", true},
			{"browser failed", "The app could not open a browser. Open this address in a browser:\n" +
				"ブラウザーを開けませんでした。次のアドレスをブラウザーで開いてください。", false},
		} {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				var out bytes.Buffer
				printStartup(&out, version, address, tc.opened)
				name := "VRC Tsuyaku"
				if version != "" {
					name += " " + version
				}
				if want := name + "\n" + tc.message + hint; out.String() != want {
					t.Fatalf("startup output = %q, want %q", out.String(), want)
				}
			})
		}
	}
}

func TestRunExit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	for _, tc := range []struct {
		name          string
		interrupt     bool
		blockedOutput bool
	}{
		{name: "exit"},
		{name: "interrupt", interrupt: true},
		{name: "exit with blocked console", blockedOutput: true},
		{name: "interrupt with blocked console", interrupt: true, blockedOutput: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			resume := make(chan struct{})
			release := sync.OnceFunc(func() { close(resume) })
			updating, cleaning := make(chan struct{}), make(chan struct{})
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			transport.RegisterProtocol("https", updateTransport(func(r *http.Request) (*http.Response, error) {
				close(updating)
				<-r.Context().Done()
				close(cleaning)
				<-resume
				return nil, r.Context().Err()
			}))
			http.DefaultTransport = transport
			t.Cleanup(func() {
				http.DefaultTransport = original
				transport.CloseIdleConnections()
			})
			consoleResume := make(chan struct{})
			releaseConsole := sync.OnceFunc(func() { close(consoleResume) })
			out := &startupWriter{url: make(chan string, 1), exiting: make(chan struct{}), written: make(chan struct{})}
			if tc.blockedOutput {
				out.blockExit = consoleResume
			}
			done := make(chan struct{})
			var runErr error
			var errout bytes.Buffer
			dir := t.TempDir()
			go func() {
				defer close(done)
				runErr = run(ctx, []string{"-data-dir", dir}, out, &errout)
			}()
			t.Cleanup(func() {
				cancel()
				release()
				releaseConsole()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("run did not stop")
				}
			})
			var address *url.URL
			select {
			case startup := <-out.url:
				var err error
				address, err = url.Parse(startup)
				if err != nil {
					t.Fatal("invalid startup URL")
				}
			case <-done:
				t.Fatalf("run stopped before startup: %v", runErr)
			case <-time.After(5 * time.Second):
				t.Fatal("run did not report startup")
			}
			if address.Scheme != "http" || !strings.HasPrefix(address.Host, "127.0.0.1:") || address.Path != "/" || address.RawQuery != "" || address.Fragment != "" {
				t.Fatal("startup did not report a plain loopback URL")
			}
			if output := out.String(); !strings.Contains(output, "\n    "+address.String()+"\n\n") || !strings.Contains(output, "The app could not open a browser. Open this address in a browser:\nブラウザーを開けませんでした。次のアドレスをブラウザーで開いてください。\n") {
				t.Fatalf("startup output did not report the URL and browser failure: %q", output)
			}
			address.Path = "/api/exit"
			client := &http.Client{Timeout: 5 * time.Second}
			post := func(origin string, want int) {
				t.Helper()
				req, err := http.NewRequestWithContext(t.Context(), "POST", address.String(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if origin != "" {
					req.Header.Set("Origin", origin)
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("incomplete exit response: %v", err)
				}
				if resp.StatusCode != want {
					t.Fatalf("exit status = %d, want %d", resp.StatusCode, want)
				}
				if want == http.StatusAccepted && (len(body) != 0 || resp.ContentLength != 0) {
					t.Fatal("Exit did not return a complete empty response")
				}
			}
			post("http://evil.example", http.StatusForbidden)
			select {
			case <-updating:
			case <-time.After(5 * time.Second):
				t.Fatal("update request did not start")
			}
			if !tc.interrupt {
				post("http://"+address.Host, http.StatusAccepted)
			} else {
				cancel()
			}
			select {
			case <-cleaning:
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown did not cancel the update request")
			}
			select {
			case <-out.exiting:
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown did not begin the console write during cleanup")
			}
			select {
			case <-done:
				t.Fatal("run returned before cleanup finished")
			default:
			}
			post("", http.StatusAccepted)
			release()
			select {
			case <-done:
				if runErr != nil {
					t.Fatalf("run returned an error: %v", runErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("run did not stop after shutdown")
			}
			if tc.blockedOutput {
				select {
				case <-out.written:
					t.Fatal("shutdown console write did not stay blocked until run returned")
				default:
				}
			}
			releaseConsole()
			select {
			case <-out.written:
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown console write did not finish")
			}
			if output := out.String(); !strings.HasSuffix(output, "Exiting...\n終了しています...\n") {
				t.Fatalf("shutdown output did not include both languages: %q", output)
			}
			if errout.Len() != 0 {
				t.Fatalf("console messages went to standard error: %q", errout.String())
			}
		})
	}
}
