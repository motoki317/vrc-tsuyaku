package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// RecordWAV writes a canonical 44-byte-header PCM16 WAV from source.
// It records duration rounded down to a sample, clips samples to [-1, 1], and
// rejects non-finite samples. RecordWAV does not close w.
// The caller must discard w's contents on error.
func RecordWAV(ctx context.Context, source Source, duration time.Duration, w io.Writer) error {
	count := int64(duration/time.Second)*SampleRate + int64(duration%time.Second)*SampleRate/int64(time.Second)
	if count <= 0 || count > (math.MaxUint32-36)/2 {
		return errors.New("recording duration must fit at least one sample and a PCM16 RIFF file")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var header [44]byte
	copy(header[:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+2*count))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], SampleRate)
	binary.LittleEndian.PutUint32(header[28:], SampleRate*2)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(2*count))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	done := errors.New("recording complete")
	err := source.Capture(ctx, func(samples []float32) error {
		samples = samples[:min(int64(len(samples)), count)]
		data := make([]byte, len(samples)*2)
		for i, sample := range samples {
			v := float64(sample)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return errors.New("microphone returned a non-finite audio sample")
			}
			pcm := int16(max(-32768, min(32767, math.Round(v*32768))))
			binary.LittleEndian.PutUint16(data[2*i:], uint16(pcm))
		}
		if err := writeAll(w, data); err != nil {
			return err
		}
		count -= int64(len(samples))
		if count == 0 {
			return done
		}
		return nil
	})
	if errors.Is(err, done) && count == 0 {
		return nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return fmt.Errorf("record audio: %w", err)
}

func writeAll(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}
