package chatbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

type recordedWrite struct {
	packet []byte
	at     time.Time
}

type recordingSocket struct {
	delay time.Duration
	err   error
	short bool

	// mu guards the record, which a Send on another goroutine can append to while the test reads it.
	mu     sync.Mutex
	writes []recordedWrite
	closed int
}

func (s *recordingSocket) Write(p []byte) (int, error) {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.writes = append(s.writes, recordedWrite{bytes.Clone(p), time.Now()})
	s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	if s.short {
		return len(p) - 1, nil
	}
	return len(p), nil
}

func (s *recordingSocket) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	return nil
}

func (s *recordingSocket) recorded() ([]recordedWrite, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.writes), s.closed
}

func TestQueueSpacing(t *testing.T) {
	for _, delay := range []time.Duration{0, 2 * time.Second} {
		t.Run(delay.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				socket := &recordingSocket{delay: delay}
				q := newQueue(socket)
				defer q.Close()
				for _, page := range []string{"first", "second", "third"} {
					if err := q.Send(t.Context(), page); err != nil {
						t.Fatal(err)
					}
				}
				writes, _ := socket.recorded()
				if len(writes) != 3 {
					t.Fatal(writes)
				}
				for i := 1; i < len(writes); i++ {
					if gap := writes[i].at.Sub(writes[i-1].at); gap != delay+1500*time.Millisecond {
						t.Fatalf("write gap = %s, want %s", gap, delay+1500*time.Millisecond)
					}
				}
				time.Sleep(5 * time.Second)
				start := time.Now()
				if err := q.Send(t.Context(), "after idle"); err != nil {
					t.Fatal(err)
				}
				writes, _ = socket.recorded()
				if got := writes[3].at.Sub(start); got != delay {
					t.Fatalf("idle send took %s, want %s", got, delay)
				}
			})
		})
	}
}

func TestQueueConcurrentPages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := &recordingSocket{delay: 2 * time.Second}
		q := newQueue(socket)
		defer q.Close()
		start := time.Now()
		done := make(chan error, 3)
		for _, page := range []string{"a", "b", "c"} {
			go func() { done <- q.Send(t.Context(), page) }()
			synctest.Wait()
		}
		for range 3 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		writes, _ := socket.recorded()
		var pages []string
		for i, w := range writes {
			if got, want := w.at.Sub(start), 2*time.Second+time.Duration(i)*3500*time.Millisecond; got != want {
				t.Fatalf("write %d at %s, want %s", i, got, want)
			}
			pages = append(pages, decodePacket(t, w.packet))
		}
		slices.Sort(pages)
		if !slices.Equal(pages, []string{"a", "b", "c"}) {
			t.Fatal(pages)
		}
	})
}

func TestQueueCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := &recordingSocket{}
		q := newQueue(socket)
		defer q.Close()
		if err := q.Send(t.Context(), "first"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- q.Send(ctx, "spacing wait") }()
		synctest.Wait()
		waiting, cancelWaiting := context.WithCancel(t.Context())
		other := make(chan error, 1)
		go func() { other <- q.Send(waiting, "waiting for turn") }()
		synctest.Wait()
		cancelWaiting()
		if err := <-other; !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancellation = %v", err)
		}
		time.Sleep(time.Second)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("spacing wait cancellation = %v", err)
		}
		if err := q.Send(ctx, "already canceled"); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if writes, _ := socket.recorded(); len(writes) != 1 {
			t.Fatalf("sent %d pages after cancellation", len(writes))
		}
		if err := q.Send(t.Context(), "new"); err != nil {
			t.Fatal(err)
		}
		writes, _ := socket.recorded()
		if gap := writes[1].at.Sub(writes[0].at); gap != 1500*time.Millisecond {
			t.Fatalf("cancellation reset the rate limit: %s", gap)
		}
	})
}

func TestQueueClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := &recordingSocket{}
		q := newQueue(socket)
		if err := q.Send(t.Context(), "first"); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 2)
		for range 2 {
			go func() { done <- q.Send(t.Context(), "queued") }()
			synctest.Wait()
		}
		for range 2 {
			if err := q.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for range 2 {
			if err := <-done; !errors.Is(err, net.ErrClosed) || errcode.Of(err) != errcode.ChatboxSend {
				t.Fatalf("close during Send = %v", err)
			}
		}
		if err := q.Send(t.Context(), "later"); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Send after Close = %v", err)
		}
		if writes, closed := socket.recorded(); len(writes) != 1 || closed != 1 {
			t.Fatalf("writes = %d, closes = %d", len(writes), closed)
		}
	})
}

func TestQueueCloseActiveWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writer, reader := net.Pipe()
		defer reader.Close()
		q := newQueue(writer)
		done := make(chan error, 1)
		go func() { done <- q.Send(t.Context(), "blocked write") }()
		synctest.Wait()
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, io.ErrClosedPipe) || errcode.Of(err) != errcode.ChatboxSend {
			t.Fatalf("closed write = %v", err)
		}
	})
}

func TestQueueWriteErrors(t *testing.T) {
	for _, short := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			wantErr := errors.New("write failed")
			socket := &recordingSocket{}
			q := newQueue(socket)
			defer q.Close()
			if err := q.Send(t.Context(), "first"); err != nil {
				t.Fatal(err)
			}
			socket.err = wantErr
			if short {
				socket.err, socket.short, wantErr = nil, true, io.ErrShortWrite
			}
			if err := q.Send(t.Context(), "failed"); !errors.Is(err, wantErr) || errcode.Of(err) != errcode.ChatboxSend {
				t.Fatalf("write error = %v, want %v", err, wantErr)
			}
			if writes, _ := socket.recorded(); len(writes) != 2 {
				t.Fatalf("retried after a write error: %d writes", len(writes))
			}
			socket.err, socket.short = nil, false
			if err := q.Send(t.Context(), "later"); err != nil {
				t.Fatal(err)
			}
			writes, _ := socket.recorded()
			if gap := writes[2].at.Sub(writes[1].at); gap != 1500*time.Millisecond {
				t.Fatalf("failed write reset the rate limit: %s", gap)
			}
		})
	}
}

func TestQueueAddress(t *testing.T) {
	for _, address := range []string{"", "localhost:9000", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:65536"} {
		if q, err := New(address); err == nil {
			q.Close()
			t.Fatalf("accepted address %q", address)
		}
	}
}

func decodePacket(t *testing.T, packet []byte) string {
	t.Helper()
	if len(packet)%4 != 0 {
		t.Fatalf("unaligned packet length %d", len(packet))
	}
	readString := func() string {
		t.Helper()
		n := bytes.IndexByte(packet, 0)
		if n < 0 {
			t.Fatal("OSC string has no terminator")
		}
		text := string(packet[:n])
		size := (n + 4) &^ 3
		if size > len(packet) {
			t.Fatal("truncated OSC padding")
		}
		for _, b := range packet[n:size] {
			if b != 0 {
				t.Fatal("nonzero OSC padding")
			}
		}
		packet = packet[size:]
		return text
	}
	if got := readString(); got != "/chatbox/input" {
		t.Fatalf("OSC address = %q", got)
	}
	if got := readString(); got != ",sTF" {
		t.Fatalf("OSC type tags = %q", got)
	}
	text := readString()
	if len(packet) != 0 {
		t.Fatalf("unexpected OSC argument data: % x", packet)
	}
	return text
}

func TestQueueUDP(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	q, err := New(listener.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	want := "[ja] こんにちは\n[en] Hello"
	if err := q.Send(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 2048)
	n, _, err := listener.ReadFromUDP(packet)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodePacket(t, packet[:n]); got != want {
		t.Fatalf("received %q, want %q", got, want)
	}
}
