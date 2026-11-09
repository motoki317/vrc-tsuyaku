package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/jfreymuth/pulse/proto"
)

func TestPulsePackets(t *testing.T) {
	p := newPulsePackets(errors.New("capture stream ended"))
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b, math.Float32bits(-0.5))
	binary.LittleEndian.PutUint32(b[4:], math.Float32bits(0.25))
	p.receive(&proto.DataPacket{Data: b})
	clear(b)
	samples := p.take()
	if len(samples) != 2 || samples[0] != -0.5 || samples[1] != 0.25 {
		t.Fatalf("samples must own a decoded copy: %v", samples)
	}
	for _, message := range []any{&proto.RecordStreamKilled{}, &proto.Overflow{}, &proto.DataPacket{Data: []byte{1}}, io.ErrUnexpectedEOF} {
		p := newPulsePackets(errors.New("capture stream ended"))
		p.receive(message)
		select {
		case <-p.failures:
		default:
			t.Fatalf("no error for %T", message)
		}
	}
}

func TestPulseTransportFailure(t *testing.T) {
	for _, write := range []bool{false, true} {
		a, b := net.Pipe()
		_ = b.Close()
		p := newPulsePackets(errors.New("capture stream ended"))
		conn := &pulseConn{Conn: a, fail: p.fail}
		var err error
		if write {
			_, err = conn.Write([]byte{1})
		} else {
			_, err = conn.Read(make([]byte, 1))
		}
		_ = a.Close()
		select {
		case got := <-p.failures:
			if err == nil || !errors.Is(got, err) {
				t.Fatalf("transport error = %v, returned %v", got, err)
			}
		default:
			t.Fatal("transport failure was not reported")
		}
	}
}

func TestPulseCancellationDuringSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulse.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("PULSE_SERVER", "unix:"+path)
	t.Setenv("PULSE_COOKIE", filepath.Join(t.TempDir(), "absent"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Wait for the authentication request, then cancel without replying.
		_, _ = conn.Read(make([]byte, 1024))
		cancel()
		_, _ = io.Copy(io.Discard, conn)
	}()
	_, closeConnection, err := connectPulse(ctx, func(any) {})
	if err == nil {
		closeConnection()
		t.Fatal("setup succeeded without an authentication reply")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancelled setup left its connection open")
	}
}

func TestPulseRejectsRemoteServer(t *testing.T) {
	t.Setenv("PULSE_SERVER", "tcp:example.com:4713")
	if _, _, err := connectPulse(t.Context(), func(any) {}); err == nil {
		t.Fatal("accepted a remote audio server")
	}
}
