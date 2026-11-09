package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
)

type recordingSource func(context.Context, func([]float32) error) error

func (s recordingSource) Capture(ctx context.Context, consume func([]float32) error) error {
	return s(ctx, consume)
}

func TestRecordFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.wav")
	source := recordingSource(func(_ context.Context, consume func([]float32) error) error {
		return consume([]float32{0.5})
	})
	if err := recordFile(t.Context(), source, time.Second/16000, path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil || len(before) != 46 {
		t.Fatalf("recording = %d bytes, error %v", len(before), err)
	}
	if err := recordFile(t.Context(), nil, time.Second, path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite = %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("existing recording changed")
	}
	failure := errors.New("capture failed")
	badSource := recordingSource(func(context.Context, func([]float32) error) error { return failure })
	badPath := filepath.Join(t.TempDir(), "incomplete.wav")
	if err := recordFile(t.Context(), badSource, time.Second, badPath); !errors.Is(err, failure) {
		t.Fatalf("capture failure = %v", err)
	}
	if _, err := os.Stat(badPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete recording remains: %v", err)
	}
}

func TestRecordArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.wav")
	for _, args := range [][]string{{}, {"a", "b"}, {"-seconds", "0", path}, {"-seconds", "-1", path},
		{"-seconds", "NaN", path}, {"-seconds", "+Inf", path}, {"-seconds", "999999999", path},
		{"-seconds", "1m", path}, {"-seconds", "1m2", path}, {"-bad", path}, {"-list", path}, {"-app", "player", "-microphone", "mic", path}, {"-app", "player", "-list"}} {
		if err := record(t.Context(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted arguments %q", args)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid arguments created a recording: %v", err)
	}
}

func TestRecordList(t *testing.T) {
	if os.Getenv("VRC_TSUYAKU_TEST_AUDIO") == "" {
		t.Skip("set VRC_TSUYAKU_TEST_AUDIO to list microphones")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	devices, err := audio.Microphones(ctx)
	if err != nil || len(devices) == 0 {
		t.Fatalf("Microphones = %v, %v", devices, err)
	}
	var out bytes.Buffer
	if err := record(ctx, []string{"-list"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, device := range devices {
		if !strings.Contains(out.String(), device.ID+"\t"+device.Name+"\n") {
			t.Fatalf("listing %q omits %+v", out.String(), device)
		}
	}
}
