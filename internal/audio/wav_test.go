package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"slices"
	"testing"
	"time"
)

type sampleSource [][]float32

func (s sampleSource) Capture(_ context.Context, consume func([]float32) error) error {
	for _, chunk := range s {
		if err := consume(chunk); err != nil {
			return err
		}
	}
	return nil
}

func TestRecordWAV(t *testing.T) {
	var b bytes.Buffer
	source := sampleSource{{-2, -1, -0.5}, {0, 0.5, 1, 2, 0.25}, {0.75}}
	if err := RecordWAV(t.Context(), source, 7*time.Second/SampleRate, &b); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	wantHeader := []byte{
		'R', 'I', 'F', 'F', 50, 0, 0, 0, 'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ', 16, 0, 0, 0, 1, 0, 1, 0,
		0x80, 0x3e, 0, 0, 0, 0x7d, 0, 0, 2, 0, 16, 0,
		'd', 'a', 't', 'a', 14, 0, 0, 0,
	}
	if len(data) != 58 || !bytes.Equal(data[:44], wantHeader) {
		t.Fatalf("noncanonical WAV: % x", data)
	}
	var got []int16
	for i := 44; i < len(data); i += 2 {
		got = append(got, int16(binary.LittleEndian.Uint16(data[i:])))
	}
	if !slices.Equal(got, []int16{-32768, -32768, -16384, 0, 16384, 32767, 32767}) {
		t.Fatalf("PCM16 = %v", got)
	}
}

func TestRecordWAVErrors(t *testing.T) {
	for _, duration := range []time.Duration{0, -time.Second, time.Nanosecond, 48 * time.Hour} {
		var b bytes.Buffer
		if err := RecordWAV(t.Context(), nil, duration, &b); err == nil || b.Len() != 0 {
			t.Fatalf("duration %v: error=%v, bytes=%d", duration, err, b.Len())
		}
	}
	for _, value := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		if err := RecordWAV(t.Context(), sampleSource{{value}}, time.Second/SampleRate, io.Discard); err == nil {
			t.Fatalf("accepted %v", value)
		}
	}
	if err := RecordWAV(t.Context(), sampleSource{{0}}, time.Second, io.Discard); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short capture = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var b bytes.Buffer
	if err := RecordWAV(ctx, nil, time.Second, &b); !errors.Is(err, context.Canceled) || b.Len() != 0 {
		t.Fatalf("cancelled recording: error=%v, bytes=%d", err, b.Len())
	}
	for _, limit := range []int{0, 44} {
		w := &limitedWriter{remaining: limit}
		if err := RecordWAV(t.Context(), sampleSource{{0}}, time.Second/SampleRate, w); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("write failure at %d = %v", limit, err)
		}
	}
}

type limitedWriter struct{ remaining int }

func (w *limitedWriter) Write(b []byte) (int, error) {
	n := min(len(b), w.remaining)
	w.remaining -= n
	return n, nil
}
