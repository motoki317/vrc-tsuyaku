package audio

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jfreymuth/pulse/proto"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

func (a application) Capture(ctx context.Context, consume func([]float32) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(a.executable) == "" {
		return errors.New("application executable name is empty")
	}
	packets := newPulsePackets(applicationStreamEnded(a.executable))
	p, closeConnection, err := connectPulse(ctx, packets.receive)
	if err != nil {
		return err
	}
	defer closeConnection()
	var inputs proto.GetSinkInputInfoListReply
	if err := p.Request(&proto.GetSinkInputInfoList{}, &inputs); err != nil {
		return fmt.Errorf("list application playback: %w", err)
	}
	input, err := selectApplication(inputs, a.executable, a.names...)
	if err != nil {
		return err
	}
	var sink proto.GetSinkInfoReply
	if err := p.Request(&proto.GetSinkInfo{SinkIndex: input.SinkIndex}, &sink); err != nil {
		return fmt.Errorf("find application playback device: %w", err)
	}
	if err := startPulseCapture(p.Request, sink.MonitorSourceIndex, input.SinkInputIndex); err != nil {
		return err
	}
	return packets.consume(ctx, consume)
}

func applicationStreamEnded(executable string) error {
	return errcode.With(errcode.ApplicationEnded, fmt.Errorf("%s playback stream ended or moved to another output", executable))
}

func selectApplication(inputs proto.GetSinkInputInfoListReply, executable string, names ...string) (*proto.GetSinkInputInfoReply, error) {
	var selected *proto.GetSinkInputInfoReply
	for _, input := range inputs {
		// Wine uses the product name, or executable basename, as application.name.
		// The caller supplies candidate product names. Native clients expose application.process.binary.
		// https://github.com/ValveSoftware/wine/blob/b8fdff8e1f855b5276ec4ddca0f31b2792554322/dlls/mmdevapi/client.c#L277
		// https://www.freedesktop.org/software/pulseaudio/doxygen/proplist_8h.html
		name := strings.TrimRight(string(input.Properties["application.name"]), "\x00")
		binary := strings.TrimRight(string(input.Properties["application.process.binary"]), "\x00")
		match := strings.EqualFold(name, executable) || strings.EqualFold(binary, executable)
		for _, candidate := range names {
			match = match || strings.EqualFold(name, candidate)
		}
		if !match {
			continue
		}
		if selected != nil {
			return nil, errcode.With(errcode.MultipleApplications, fmt.Errorf("multiple playback streams for %s are available", executable))
		}
		selected = input
	}
	if selected == nil {
		return nil, errcode.With(errcode.ApplicationNotRunning, fmt.Errorf("%s is not running or has no PulseAudio playback stream", executable))
	}
	return selected, nil
}
