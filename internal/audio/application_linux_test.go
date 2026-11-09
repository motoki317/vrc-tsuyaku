package audio

import (
	"context"
	"testing"

	"github.com/jfreymuth/pulse/proto"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

func TestSelectApplication(t *testing.T) {
	input := func(index uint32, name, binary string) *proto.GetSinkInputInfoReply {
		return &proto.GetSinkInputInfoReply{SinkInputIndex: index, Properties: proto.PropList{"application.name": proto.PropListString(name), "application.process.binary": proto.PropListString(binary)}}
	}
	unrelated := input(1, "Music", "music")
	for _, match := range []*proto.GetSinkInputInfoReply{input(2, "Player.exe", "wine64-preloader"), input(3, "player product", "wine64-preloader"), input(4, "Player", "Player.exe")} {
		got, err := selectApplication(proto.GetSinkInputInfoListReply{unrelated, match}, "Player.exe", "Player Product")
		if err != nil || got != match {
			t.Fatalf("match %+v: %v, %v", match, got, err)
		}
		if _, err = selectApplication(proto.GetSinkInputInfoListReply{match, match}, "Player.exe", "Player Product"); errcode.Of(err) != errcode.MultipleApplications {
			t.Fatal("accepted ambiguous streams")
		}
	}
	if _, err := selectApplication(proto.GetSinkInputInfoListReply{unrelated, input(3, "Player Product", "wine64-preloader")}, "Player.exe"); errcode.Of(err) != errcode.ApplicationNotRunning {
		t.Fatal("selected unrelated stream")
	}
}

func TestApplicationStreamEnded(t *testing.T) {
	p := newPulsePackets(applicationStreamEnded("Player.exe"))
	p.receive(&proto.RecordStreamKilled{})
	if err := p.consume(context.Background(), func([]float32) error { t.Fatal("unexpected audio"); return nil }); errcode.Of(err) != errcode.ApplicationEnded || err.Error() != "Player.exe playback stream ended or moved to another output" {
		t.Fatalf("stream end: %v", err)
	}
}
