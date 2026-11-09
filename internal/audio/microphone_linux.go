// connectPulse reimplements Unix socket setup from jfreymuth/pulse's
// proto/connect.go (MIT). It accepts only Unix sockets; upstream also accepts TCP.
// https://github.com/jfreymuth/pulse/blob/v0.1.1/proto/connect.go
// No upstream source code is copied. See ../../THIRD_PARTY_NOTICES.md.

package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jfreymuth/pulse/proto"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

// Microphones lists input devices, excluding playback monitors.
func Microphones(ctx context.Context) ([]Device, error) {
	p, closeConnection, err := connectPulse(ctx, func(any) {})
	if err != nil {
		return nil, err
	}
	defer closeConnection()
	var reply proto.GetSourceInfoListReply
	if err := p.Request(&proto.GetSourceInfoList{}, &reply); err != nil {
		return nil, fmt.Errorf("list microphones: %w", err)
	}
	var devices []Device
	for _, source := range reply {
		if source.MonitorSourceIndex == proto.Undefined {
			devices = append(devices, Device{ID: source.SourceName, Name: source.Device})
		}
	}
	return devices, nil
}

func (m microphone) Capture(ctx context.Context, consume func([]float32) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	packets := newPulsePackets(errcode.With(errcode.MicrophoneUnavailable, errors.New("PulseAudio capture stream was removed")))
	p, closeConnection, err := connectPulse(ctx, packets.receive)
	if err != nil {
		return errcode.With(errcode.MicrophoneUnavailable, err)
	}
	defer closeConnection()
	return m.capturePulse(ctx, p.Request, packets, consume)
}

func (m microphone) capturePulse(ctx context.Context, request func(proto.RequestArgs, proto.Reply) error, packets *pulsePackets, consume func([]float32) error) error {
	var source proto.GetSourceInfoReply
	if err := request(&proto.GetSourceInfo{SourceIndex: proto.Undefined, SourceName: m.id}, &source); err != nil {
		return errcode.With(errcode.MicrophoneUnavailable, fmt.Errorf("find microphone: %w", err))
	}
	if source.MonitorSourceIndex != proto.Undefined {
		return errcode.With(errcode.MicrophoneUnavailable, errors.New("selected source is a playback monitor, not a microphone"))
	}
	if err := startPulseCapture(request, source.SourceIndex, proto.Undefined); err != nil {
		return errcode.With(errcode.MicrophoneUnavailable, err)
	}
	return packets.consume(ctx, consume)
}

func startPulseCapture(request func(proto.RequestArgs, proto.Reply) error, sourceIndex, inputIndex uint32) error {
	// Use proto directly because pulse.RecordStream hides start errors and
	// stream-killed events. FixFormat/Rate/Channels stay false so the server
	// converts to the requested sample spec.
	// https://www.freedesktop.org/software/pulseaudio/doxygen/sample.html
	const fragmentBytes = SampleRate * 4 / 50 // 20 ms of mono float32.
	// Fragment size alone leaves an otherwise idle source at its highest latency.
	// https://www.freedesktop.org/wiki/Software/PulseAudio/Documentation/Developer/Clients/LatencyControl/
	args := proto.CreateRecordStream{
		SampleSpec: proto.SampleSpec{Format: proto.FormatFloat32LE, Channels: 1, Rate: SampleRate},
		ChannelMap: proto.ChannelMap{proto.ChannelMono}, SourceIndex: sourceIndex,
		BufferMaxLength: SampleRate * 4, BufferFragSize: fragmentBytes,
		Corked: true, NoMove: true, AdjustLatency: true, DirectOnInputIndex: inputIndex,
		ChannelVolumes: proto.ChannelVolumes{uint32(proto.VolumeNorm)},
		Properties:     proto.PropList{"media.name": proto.PropListString("vrc-tsuyaku capture")},
	}
	var reply proto.CreateRecordStreamReply
	if err := request(&args, &reply); err != nil {
		return fmt.Errorf("open audio capture: %w", err)
	}
	if reply.SampleSpec != args.SampleSpec {
		return fmt.Errorf("PulseAudio returned an unexpected capture format: %+v", reply.SampleSpec)
	}
	if err := request(&proto.CorkRecordStream{StreamIndex: reply.StreamIndex, Corked: false}, nil); err != nil {
		return fmt.Errorf("start audio capture: %w", err)
	}
	return nil
}

type pulsePackets struct {
	*captureQueue
	streamEnded error
}

func newPulsePackets(streamEnded error) *pulsePackets {
	return &pulsePackets{captureQueue: newCaptureQueue(), streamEnded: streamEnded}
}

// Never wait for the consumer on proto.Client's read goroutine, which also
// delivers request replies.
func (p *pulsePackets) receive(message any) {
	switch message := message.(type) {
	case *proto.DataPacket:
		if len(message.Data) == 0 {
			return
		}
		if len(message.Data)%4 != 0 {
			p.fail(errors.New("PulseAudio sent an incomplete float32 sample"))
			return
		}
		samples := make([]float32, len(message.Data)/4)
		for i := range samples {
			samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(message.Data[4*i:]))
		}
		if err := p.push(samples); err != nil {
			p.fail(err)
		}
	case *proto.RecordStreamKilled:
		p.fail(p.streamEnded)
	case *proto.Overflow:
		p.fail(fmt.Errorf("PulseAudio server buffer overflow: %w", errCaptureOverflow))
	case error:
		p.fail(fmt.Errorf("PulseAudio connection: %w", message))
	}
}

func connectPulse(ctx context.Context, callback func(any)) (*proto.Client, func(), error) {
	server := os.Getenv("PULSE_SERVER")
	if server == "" {
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if runtimeDir == "" {
			return nil, nil, errors.New("set XDG_RUNTIME_DIR or PULSE_SERVER to a local PulseAudio socket")
		}
		server = filepath.Join(runtimeDir, "pulse", "native")
	}
	server = strings.TrimPrefix(server, "unix:")
	if !filepath.IsAbs(server) {
		return nil, nil, errors.New("PULSE_SERVER must name an absolute local Unix socket path")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", server)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to PulseAudio: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	closeConnection := func() { stop(); _ = conn.Close() }
	p := &proto.Client{Callback: callback}
	p.SetTimeout(5 * time.Second)
	p.Open(&pulseConn{Conn: conn, fail: func(err error) { callback(err) }})
	cookiePath := os.Getenv("PULSE_COOKIE")
	if cookiePath == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			closeConnection()
			return nil, nil, err
		}
		cookiePath = filepath.Join(config, "pulse", "cookie")
	}
	cookie, err := os.ReadFile(cookiePath)
	if errors.Is(err, os.ErrNotExist) {
		cookie, err = make([]byte, 256), nil
	}
	if err != nil || len(cookie) != 256 {
		closeConnection()
		return nil, nil, fmt.Errorf("read PulseAudio cookie (must be 256 bytes): %v", err)
	}
	var auth proto.AuthReply
	if err := p.Request(&proto.Auth{Version: p.Version(), Cookie: cookie}, &auth); err != nil {
		closeConnection()
		return nil, nil, fmt.Errorf("authenticate to PulseAudio: %w", err)
	}
	p.SetVersion(auth.Version)
	if err := p.Request(&proto.SetClientName{Props: proto.PropList{
		"application.name": proto.PropListString("vrc-tsuyaku"),
	}}, &proto.SetClientNameReply{}); err != nil {
		closeConnection()
		return nil, nil, fmt.Errorf("name PulseAudio client: %w", err)
	}
	return p, closeConnection, nil
}

// Deliver transport errors, including EOF, directly to receive. proto.Client
// wraps EOF as *proto.ConnectionClosed and omits other transport errors.
type pulseConn struct {
	net.Conn
	fail func(error)
}

func (c *pulseConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil {
		c.fail(err)
	}
	return n, err
}

func (c *pulseConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err != nil {
		c.fail(err)
		_ = c.Close()
	}
	return n, err
}
