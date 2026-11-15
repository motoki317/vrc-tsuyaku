package asr

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

type replayDecoder struct {
	accepted                  []int
	score                     func(int, []int) ([]float32, int)
	calls, restores           int
	checkpointErr, restoreErr error
}

func (d *replayDecoder) Scores(frame int) ([]float32, error) {
	d.calls++
	logits, duration := d.score(frame, d.accepted)
	logits = append(slices.Clone(logits), make([]float32, 5)...)
	logits[len(logits)-5+duration] = 10
	return logits, nil
}
func (d *replayDecoder) Accept(token int) error {
	d.accepted = append(d.accepted, token)
	return nil
}
func (d *replayDecoder) checkpoint() (func() error, error) {
	if d.checkpointErr != nil {
		return nil, d.checkpointErr
	}
	accepted := slices.Clone(d.accepted)
	return func() error {
		d.restores++
		d.accepted = slices.Clone(accepted)
		return d.restoreErr
	}, nil
}

func TestSpeculativeHints(t *testing.T) {
	v := newHintVocabulary(vocabulary{"x", "a", "bc", "d", "ef", "<blk>"}, false)
	for _, tc := range []struct {
		name      string
		words     []string
		scores    [][]float32
		want      []int
		rollbacks int
	}{
		{"unfinished at EOF", []string{"abc"}, [][]float32{{0, -.5, -9, -9, -9, -10}}, []int{0}, 1},
		{"failure despite unrelated starts", []string{"abc", "def"}, [][]float32{{0, -.5, -9, -9, -9, -10}, {0, -9, -9, -.5, -9, -10}, {0, -9, -9, -9, -.5, -10}}, []int{0, 3, 4}, 1},
		{"completion confirms multiple changes", []string{"abcd", "bcd"}, [][]float32{{0, -.5, -9, -9, -9, -10}, {0, -9, -.5, -9, -9, -10}, {0, -9, -9, -.5, -9, -10}}, []int{1, 2, 3}, 0},
		{"completed prefix survives later failure", []string{"abc", "abcdef"}, [][]float32{{0, -.5, -9, -9, -9, -10}, {0, -9, -.5, -9, -9, -10}, {0, -9, -9, -.5, -9, -10}, {0, -9, -9, -9, -9, -10}}, []int{1, 2, 0, 0}, 1},
		{"unbiased prefix before changed token", []string{"abc"}, [][]float32{{0, 1, -9, -9, -9, -10}, {0, -9, -.5, -9, -9, -10}}, []int{1, 2}, 0},
		{"blank keeps speculation until EOF", []string{"abc"}, [][]float32{{0, -.5, -9, -9, -9, -10}, {0, -9, -9, -9, -9, 1}}, []int{0}, 1},
		{"blank completes unbiased prefix", []string{"abc"}, [][]float32{{0, 1, -9, -9, -9, -10}, {0, -9, -.5, -9, -9, 1}}, []int{1, 2}, 0},
		{"blank completes biased prefix", []string{"abc"}, [][]float32{{0, -.5, -9, -9, -9, -10}, {0, -9, -.5, -9, -9, 1}}, []int{1, 2}, 0},
		{"blank continuation rolls back at EOF", []string{"abcdef"}, [][]float32{{0, 1, -9, -9, -9, -10}, {0, -9, -.5, -9, -9, 1}}, []int{1}, 1},
		{"blank continuation rolls back on mismatch", []string{"abcdef"}, [][]float32{{0, 1, -9, -9, -9, -10}, {0, -9, -.5, -9, -9, 1}, {1, -9, -9, -9, -9, -10}}, []int{1, 0}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &replayDecoder{score: func(frame int, _ []int) ([]float32, int) { return tc.scores[frame], 1 }}
			got, err := greedyTDT(t.Context(), len(tc.scores), tdtConfig{blank: 5, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10, hints: v.compile(tc.words)}, d)
			if err != nil || !slices.Equal(got, tc.want) || !slices.Equal(got, d.accepted) || d.restores != tc.rollbacks {
				t.Fatalf("tokens %v state %v err %v rollbacks %d, want %v / %d", got, d.accepted, err, d.restores, tc.want, tc.rollbacks)
			}
		})
	}
}

func TestBlankContinuationDuration(t *testing.T) {
	v := newHintVocabulary(vocabulary{"x", "a", "bc", "<blk>"}, false)
	for _, duration := range []int{0, 1, 2, 4} {
		t.Run(fmt.Sprint(duration), func(t *testing.T) {
			d := &hintDecoder{t: t, steps: [][]float32{{0, 1, -9, -10}, {0, -9, -.5, 1}}, duration: duration, vocabulary: 4}
			cfg := tdtConfig{blank: 3, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10, hints: v.compile([]string{"abc"})}
			got, err := greedyTDT(t.Context(), max(1, 2*duration), cfg, d)
			if err != nil || !slices.Equal(got, []int{1, 2}) || !slices.Equal(d.accepted, got) || !slices.Equal(d.frames, []int{0, duration}) {
				t.Fatalf("tokens %v state %v frames %v err %v", got, d.accepted, d.frames, err)
			}
		})
	}
}

func TestRollbackRestoresSymbolLimit(t *testing.T) {
	d := &replayDecoder{score: func(frame int, _ []int) ([]float32, int) {
		if frame == 0 {
			return []float32{0, -.5, -9, -10}, 0
		}
		return []float32{-10, -10, -10, 1}, 0
	}}
	v := newHintVocabulary(vocabulary{"x", "a", "bc", "<blk>"}, false)
	got, err := greedyTDT(t.Context(), 2, tdtConfig{blank: 3, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 2, hints: v.compile([]string{"abc"})}, d)
	if err != nil || !slices.Equal(got, []int{0, 0}) || d.restores != 2 {
		t.Fatalf("tokens %v, rollbacks %d, err %v", got, d.restores, err)
	}
}

func TestRollbackTakesOneUnbiasedToken(t *testing.T) {
	var steps []decodeStep
	d := &replayDecoder{score: func(frame int, accepted []int) ([]float32, int) {
		steps = append(steps, decodeStep{frame, len(accepted)})
		if frame > 0 {
			return []float32{-9, -9, -9, 1}, 0
		}
		if len(accepted) == 1 && accepted[0] == 1 {
			return []float32{0, -9, -9, -10}, 0
		}
		if len(accepted) == 2 {
			return []float32{0, -9, -.5, -10}, 0
		}
		return []float32{0, -.5, -9, -10}, 0
	}}
	v := newHintVocabulary(vocabulary{"x", "a", "bc", "<blk>"}, false)
	got, err := greedyTDT(t.Context(), 2, tdtConfig{blank: 3, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 3, hints: v.compile([]string{"abc"})}, d)
	wantSteps := []decodeStep{{0, 0}, {0, 1}, {0, 0}, {0, 1}, {0, 2}, {1, 3}}
	if err != nil || !slices.Equal(got, []int{0, 1, 2}) || d.restores != 1 || !slices.Equal(steps, wantSteps) {
		t.Fatalf("tokens %v, steps %v, restores %d, err %v", got, steps, d.restores, err)
	}
}

func TestRollbackCap(t *testing.T) {
	d := &replayDecoder{score: func(_ int, _ []int) ([]float32, int) { return []float32{0, -.5, -9, -10}, 1 }}
	v := newHintVocabulary(vocabulary{"x", "a", "bc", "<blk>"}, false)
	got, err := greedyTDT(t.Context(), 100, tdtConfig{blank: 3, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10, hints: v.compile([]string{"abc"})}, d)
	if err != nil || !slices.Equal(got, make([]int, 100)) || d.restores != maxHintRollbacks {
		t.Fatalf("tokens %v restores %d err %v", got, d.restores, err)
	}
}

func TestNoCompletedHintPreservesUnbiasedOutput(t *testing.T) {
	v := newHintVocabulary(vocabulary{"x", "y", "a", "bc", "<blk>"}, false)
	for seed := uint64(1); seed <= 100; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			score := func(frame int, accepted []int) ([]float32, int) {
				hash := seed + uint64(frame)*7919
				for _, token := range accepted {
					hash = hash*31 + uint64(token) + 1
				}
				logits := []float32{float32(hash%11) / 10, float32((hash>>8)%11) / 10, float32((hash>>16)%11) / 10, -100, float32((hash>>24)%13) / 10}
				return logits, int(hash % 3)
			}
			cfg := tdtConfig{blank: 4, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 3}
			want, err := greedyTDT(t.Context(), 16, cfg, &replayDecoder{score: score})
			if err != nil {
				t.Fatal(err)
			}
			cfg.hints = v.compile([]string{"abc"})
			got, err := greedyTDT(t.Context(), 16, cfg, &replayDecoder{score: score})
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("tokens %v want %v, err %v", got, want, err)
			}
		})
	}
}

func TestCheckpointErrors(t *testing.T) {
	failure := errors.New("checkpoint unavailable")
	v := newHintVocabulary(vocabulary{"x", "a", "bc", "<blk>"}, false)
	for _, restore := range []bool{false, true} {
		d := &replayDecoder{score: func(_ int, _ []int) ([]float32, int) { return []float32{0, -.5, -9, -10}, 1 }}
		if restore {
			d.restoreErr = failure
		} else {
			d.checkpointErr = failure
		}
		_, err := greedyTDT(t.Context(), 1, tdtConfig{blank: 3, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10, hints: v.compile([]string{"abc"})}, d)
		if !errors.Is(err, failure) {
			t.Fatalf("restore %v: %v", restore, err)
		}
	}
}
