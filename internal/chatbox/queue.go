// Package chatbox sends original text and its translation to VRChat.
package chatbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

const pageInterval = 1500 * time.Millisecond

// Queue sends messages to one UDP destination.
// A message is the original text and translation supplied to one Send call.
// Reuse the Queue when stopping and resuming sends to preserve the 1.5 s page spacing.
// This spacing keeps sends under VRChat's chatbox spam timeout.
type Queue struct {
	conn      io.WriteCloser
	turn      chan struct{}
	closed    chan struct{}
	next      time.Time
	closeOnce sync.Once
	closeErr  error
}

// New opens a UDP socket for a numeric IP:port with a nonzero port.
// Opening the socket sends no DNS requests.
func New(address string) (*Queue, error) {
	addr, err := ParseAddress(address)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		return nil, errcode.With(errcode.ChatboxSend, fmt.Errorf("open chatbox socket: %w", err))
	}
	return newQueue(conn), nil
}

// ParseAddress validates a numeric IP:port with a nonzero port without opening a socket.
func ParseAddress(address string) (netip.AddrPort, error) {
	addr, err := netip.ParseAddrPort(address)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("chatbox address must be a numeric IP:port: %w", err)
	}
	if addr.Port() == 0 {
		return netip.AddrPort{}, errors.New("chatbox port must be nonzero")
	}
	return addr, nil
}

func newQueue(conn io.WriteCloser) *Queue {
	return &Queue{conn: conn, turn: make(chan struct{}, 1), closed: make(chan struct{})}
}

// Send renders format with message, then splits the result into pages.
// Send splits each message into pages of at most 144 runes and nine newline-delimited lines.
// Splits prefer sentence endings, then whitespace, then rune boundaries.
// Whitespace at page edges is omitted. Whitespace-only input sends nothing.
// A template error or invalid rendered text fails before any page is sent.
// sent runs synchronously after each successful page write and must not call Send on this queue.
//
// Send blocks until every page is written or an error occurs. Concurrent calls
// cannot interleave pages. Each write attempt is at least 1.5 seconds after the
// previous attempt finishes, including attempts from earlier Send calls.
// Cancellation stops pending pages, including while waiting behind another Send.
// Cancellation cannot interrupt an active UDP write, but Close can. Errors are not retried.
func (q *Queue) Send(ctx context.Context, format string, message Message, sent func(string)) (err error) {
	defer func() {
		if err != nil && !errors.Is(err, ErrTemplate) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			err = errcode.With(errcode.ChatboxSend, err)
		}
	}()
	messagePages, err := Render(format, message)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-q.closed:
		return net.ErrClosed
	case q.turn <- struct{}{}:
	}
	defer func() { <-q.turn }()
	if err := q.check(ctx); err != nil {
		return err
	}
	for i, page := range messagePages {
		if delay := time.Until(q.next); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-q.closed:
				timer.Stop()
				return net.ErrClosed
			case <-timer.C:
			}
		}
		if err := q.check(ctx); err != nil {
			return err
		}
		packet := encodePage(page)
		n, err := q.conn.Write(packet)
		q.next = time.Now().Add(pageInterval)
		if err == nil && n != len(packet) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return fmt.Errorf("send chatbox page %d: %w", i+1, err)
		}
		if sent != nil {
			sent(page)
		}
	}
	return nil
}

func (q *Queue) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-q.closed:
		return net.ErrClosed
	default:
		return nil
	}
}

// Close interrupts waiting Send calls and closes the socket to abort active UDP writes.
// Later Send calls fail. Repeated Close calls are safe.
func (q *Queue) Close() error {
	q.closeOnce.Do(func() {
		close(q.closed)
		q.closeErr = q.conn.Close()
	})
	return q.closeErr
}
