package asr

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
)

type scriptedScore struct{ frame, token, duration int }

type scriptedDecoder struct {
	t        *testing.T
	scores   []scriptedScore
	accepted []int
}

func (d *scriptedDecoder) Scores(frame int) ([]float32, error) {
	d.t.Helper()
	if len(d.scores) == 0 {
		d.t.Fatal("unexpected score call")
	}
	s := d.scores[0]
	d.scores = d.scores[1:]
	if frame != s.frame {
		d.t.Fatalf("frame = %d, want %d", frame, s.frame)
	}
	logits := make([]float32, 8) // Three tokens including blank, five durations.
	logits[s.token], logits[3+s.duration] = 1, 1
	return logits, nil
}

func (d *scriptedDecoder) Accept(token int) error {
	d.accepted = append(d.accepted, token)
	return nil
}

func TestGreedyTDT(t *testing.T) {
	cfg := tdtConfig{blank: 2, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10}
	t.Run("blanks durations and final emission", func(t *testing.T) {
		d := &scriptedDecoder{t: t, scores: []scriptedScore{{0, 2, 0}, {1, 2, 2}, {3, 0, 0}, {3, 1, 1}, {4, 0, 4}}}
		tokens, err := greedyTDT(t.Context(), 5, cfg, d)
		if err != nil || !slices.Equal(tokens, []int{0, 1, 0}) || !slices.Equal(tokens, d.accepted) || len(d.scores) != 0 {
			t.Fatalf("tokens %v, accepted %v, remaining %v, error %v", tokens, d.accepted, d.scores, err)
		}
	})
	t.Run("ten symbols then advance", func(t *testing.T) {
		d := &scriptedDecoder{t: t}
		for range 10 {
			d.scores = append(d.scores, scriptedScore{0, 0, 0})
		}
		d.scores = append(d.scores, scriptedScore{1, 2, 0})
		tokens, err := greedyTDT(t.Context(), 2, cfg, d)
		if err != nil || len(tokens) != 10 || len(d.scores) != 0 {
			t.Fatalf("tokens %v, remaining %v, error %v", tokens, d.scores, err)
		}
	})
	t.Run("empty and cancelled", func(t *testing.T) {
		d := &scriptedDecoder{t: t}
		if tokens, err := greedyTDT(t.Context(), 0, cfg, d); err != nil || len(tokens) != 0 {
			t.Fatalf("empty: %v, %v", tokens, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := greedyTDT(ctx, 1, cfg, d); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled: %v", err)
		}
	})
}

func TestArgmax(t *testing.T) {
	if got, err := argmax([]float32{-2, 3, 3}); err != nil || got != 1 {
		t.Fatalf("ties: %d, %v", got, err)
	}
	for _, bad := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		if _, err := argmax([]float32{0, bad}); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}
