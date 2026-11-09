// Package translate runs a local llama-server for the self and world flows.
package translate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

// Supervisor owns one server shared by both flows. Start and Close serialize
// lifecycle changes.
type Supervisor struct {
	mu                    sync.Mutex
	serverPath, modelPath string
	stateMu               sync.RWMutex
	run                   *serverRun
}

type serverRun struct {
	child  *child
	client *http.Client
}

// New requires non-empty executable and GGUF paths and makes them absolute.
// It starts no process.
func New(serverPath, modelPath string) (*Supervisor, error) {
	if serverPath == "" || modelPath == "" {
		return nil, errors.New("llama-server and GGUF paths must be non-empty")
	}
	serverPath, err := filepath.Abs(serverPath)
	if err != nil {
		return nil, fmt.Errorf("resolve llama-server path: %w", err)
	}
	modelPath, err = filepath.Abs(modelPath)
	if err != nil {
		return nil, fmt.Errorf("resolve GGUF path: %w", err)
	}
	return &Supervisor{serverPath: serverPath, modelPath: modelPath}, nil
}

// Start waits for /health before returning. It reuses a running server or
// relaunches one that exited. ctx limits startup, not the server's lifetime.
// A failed or cancelled startup stops its child before returning.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if run := s.currentRun(); run != nil && run.child.exitError() == nil {
		return nil
	}
	// Let the child bind its own port so no other process can take it during a handoff.
	// Endpoint discovery needs plain INFO logging regardless of inherited logging settings.
	p, err := launch(s.serverPath, []string{
		"-m", s.modelPath, "--alias", catalog.TranslationAlias,
		"--jinja", "--no-prefill-assistant", "-c", "4096", "-np", "2",
		"--cache-ram", "0", "-t", "8",
		"--no-log-jsonl", "--log-colors", "off", "--log-verbosity", "3",
		"--host", "127.0.0.1", "--port", "0",
	})
	if err != nil {
		return fmt.Errorf("start llama-server: %w", err)
	}
	// Neither proxy settings nor redirects may send a phrase off this address.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	run := &serverRun{child: p, client: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	s.stateMu.Lock()
	s.run = run
	s.stateMu.Unlock()
	go func() { <-p.done; transport.CloseIdleConnections() }()
	if err := run.waitHealthy(ctx); err != nil {
		return errors.Join(err, p.stop())
	}
	return nil
}

func (r *serverRun) waitHealthy(ctx context.Context) error {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.child.exitError(); err != nil {
			return err
		}
		if address := r.child.log.URL(); address != "" {
			probeCtx, cancel := context.WithTimeout(ctx, time.Second)
			req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, address+"/health", nil)
			if err != nil {
				cancel()
				return err
			}
			resp, err := r.client.Do(req)
			ready := false
			if err == nil {
				ready = resp.StatusCode == http.StatusOK
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
			}
			cancel()
			if ready {
				return r.child.exitError()
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.child.done:
			return r.child.exitError()
		case <-tick.C:
		}
	}
}

// Close stops and reaps the server at app shutdown and interrupts active requests.
// Keep the server running while the flows are stopped.
// Close waits for any pending Start, which holds the lifecycle lock during the
// health wait. The OS also kills the child if the app dies without Close.
func (s *Supervisor) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.currentRun()
	if run == nil {
		return nil
	}
	return run.child.stop()
}

// Reading the current child must not wait for Start's health check, so a flow
// can cancel Translate and Wait while another flow starts the server.
func (s *Supervisor) currentRun() *serverRun {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.run
}

type child struct {
	done chan struct{}
	err  error // Read only after done closes.
	kill func() error
	log  logTail
}

func (p *child) stop() error {
	select {
	case <-p.done:
		return nil
	default:
	}
	if err := p.kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		select {
		case <-p.done:
			return nil
		default:
			return fmt.Errorf("stop llama-server: %w", err)
		}
	}
	<-p.done
	return nil
}

func (p *child) exitError() error {
	select {
	case <-p.done:
		return errcode.With(errcode.TranslatorExited, fmt.Errorf("llama-server exited (%v): %s", p.err, p.log.String()))
	default:
		return nil
	}
}

// Startup failures need diagnostics, but a long-running server cannot keep an
// unbounded log in memory.
type logTail struct {
	mu   sync.Mutex
	data []byte
	url  string
}

func (b *logTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if b.url == "" {
		// Capture the endpoint before truncation, even when a write splits the announcement.
		remaining := string(append(b.data, p...))
		for {
			line, rest, complete := strings.Cut(remaining, "\n")
			if !complete {
				break
			}
			remaining = rest
			_, value, found := strings.Cut(line, "listening on http://127.0.0.1:")
			if port, err := strconv.Atoi(strings.TrimSpace(value)); found && err == nil && port > 0 && port <= 65535 {
				b.url = "http://127.0.0.1:" + strconv.Itoa(port)
				break
			}
		}
	}
	const limit = 8192
	if len(p) >= limit {
		b.data = append(b.data[:0], p[len(p)-limit:]...)
	} else {
		if overflow := len(b.data) + len(p) - limit; overflow > 0 {
			b.data = b.data[overflow:]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func (b *logTail) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func (b *logTail) URL() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.url
}

// Wait reports the current server's exit, even while no translation is pending.
// Call it after Start. Cancelling ctx stops waiting without stopping the server.
func (s *Supervisor) Wait(ctx context.Context) error {
	run := s.currentRun()
	if run == nil {
		return errors.New("llama-server is not started")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-run.child.done:
		return run.child.exitError()
	}
}
