package vad_test

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/vad"
)

// Distinct samples expose lost, duplicated or overwritten audio at boundaries.
func window(frame int) []float32 {
	audio := make([]float32, vad.WindowSamples)
	for i := range audio {
		audio[i] = float32(frame*vad.WindowSamples + i)
	}
	return audio
}

func push(t *testing.T, s *vad.Segmenter, frame int, probability float32) []float32 {
	t.Helper()
	phrase, err := s.Push(window(frame), probability)
	if err != nil {
		t.Fatal(err)
	}
	return phrase
}

func wantAudio(t *testing.T, audio []float32, start, end int) {
	t.Helper()
	if len(audio) != end-start {
		t.Fatalf("got %d samples, want %d (%d..%d)", len(audio), end-start, start, end)
	}
	for i, got := range audio {
		if got != float32(start+i) {
			t.Fatalf("sample %d = %g, want %d", i, got, start+i)
		}
	}
}

func TestPreRollAndMinimum(t *testing.T) {
	for _, before := range []int{0, 3, 10} {
		for _, speech := range []int{7, 8} {
			t.Run(fmt.Sprintf("history%d/speech%d", before, speech), func(t *testing.T) {
				var s vad.Segmenter
				end := before + speech + 16
				for frame := range end {
					probability := float32(0.1)
					if frame >= before && frame < before+speech {
						probability = 0.5
					} else if frame > before+speech {
						probability = 0.4
					}
					phrase := push(t, &s, frame, probability)
					if frame == end-1 && speech == 8 {
						wantAudio(t, phrase, max(0, before*512-4800), end*512)
					} else if phrase != nil {
						t.Fatalf("unexpected phrase at frame %d", frame)
					}
				}
				if s.Flush() != nil {
					t.Fatal("confirmed phrase left pending audio")
				}
			})
		}
	}
}

func TestThresholdsAndEndSilence(t *testing.T) {
	belowEnd := math.Nextafter32(0.35, 0)
	aboveEnd := math.Nextafter32(0.35, 1)
	belowStart := math.Nextafter32(0.5, 0)
	aboveStart := math.Nextafter32(0.5, 1)
	for _, probability := range []float32{belowStart, 0.5, aboveStart} {
		t.Run(fmt.Sprintf("start/%.9g", probability), func(t *testing.T) {
			var s vad.Segmenter
			for frame := range 8 {
				push(t, &s, frame, probability)
			}
			phrase := s.Flush()
			if probability >= 0.5 {
				wantAudio(t, phrase, 0, 8*512)
			} else if phrase != nil {
				t.Fatal("started below 0.5")
			}
		})
	}
	for _, probability := range []float32{belowEnd, 0.35, aboveEnd, belowStart, 0.5, aboveStart} {
		t.Run(fmt.Sprintf("pending/%.9g", probability), func(t *testing.T) {
			var s vad.Segmenter
			for frame := range 24 {
				p := float32(0.5)
				if frame >= 8 {
					// Middle probabilities cannot start the end silence.
					p = []float32{0.35, aboveEnd, belowStart}[frame%3]
				}
				if push(t, &s, frame, p) != nil {
					t.Fatalf("ended before the first low window: %d", frame)
				}
			}
			for frame := 24; frame < 39; frame++ {
				if push(t, &s, frame, belowEnd) != nil {
					t.Fatal("ended before 500 ms of end silence")
				}
			}
			phrase := push(t, &s, 39, probability)
			if probability < 0.5 {
				wantAudio(t, phrase, 0, 40*512)
				for frame := 40; frame < 56; frame++ {
					if push(t, &s, frame, belowStart) != nil {
						t.Fatal("restarted below 0.5 after an end")
					}
				}
				if s.Flush() != nil {
					t.Fatal("idle audio became a phrase")
				}
				return
			}
			if phrase != nil {
				t.Fatal("did not cancel the end silence at or above 0.5")
			}
			for frame := 40; frame < 56; frame++ {
				phrase = push(t, &s, frame, belowEnd)
				if frame == 55 {
					wantAudio(t, phrase, 0, 56*512)
				} else if phrase != nil {
					t.Fatal("did not restart the full end-silence timer")
				}
			}
		})
	}
}

func TestCutAndContinuation(t *testing.T) {
	for _, before := range []int{0, 10} {
		for _, speech := range []int{7, 8} {
			t.Run(fmt.Sprintf("history%d/continuation%d", before, speech), func(t *testing.T) {
				var s vad.Segmenter
				for frame := range before {
					push(t, &s, frame, 0)
				}
				// 469 windows without pre-roll, or 460 after 4,800 pre-roll samples,
				// are the first whole-window boundaries at or beyond 15 seconds.
				cut := before + 469
				if before != 0 {
					cut = before + 460
				}
				start := max(0, before*512-4800)
				var first []float32
				for frame := before; frame < cut; frame++ {
					first = push(t, &s, frame, 0.5)
					if frame < cut-1 && first != nil {
						t.Fatalf("cut early at frame %d", frame)
					}
				}
				wantAudio(t, first, start, cut*512)
				if len(first) < 240000 || len(first) >= 240000+512 {
					t.Fatalf("cut length %d outside whole-window bound", len(first))
				}
				end := cut + speech + 16
				for frame := cut; frame < end; frame++ {
					probability := float32(0.4)
					if frame >= cut+speech {
						probability = 0
					}
					phrase := push(t, &s, frame, probability)
					if frame == end-1 && speech == 8 {
						wantAudio(t, phrase, cut*512, end*512)
					} else if phrase != nil {
						t.Fatalf("unexpected continuation at frame %d", frame)
					}
				}
				wantAudio(t, first, start, cut*512)
			})
		}
	}
}

func TestSilenceAcrossCut(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume%t", resume), func(t *testing.T) {
			var s vad.Segmenter
			for frame := range 469 {
				probability := float32(0.5)
				if frame >= 461 {
					probability = 0
				}
				phrase := push(t, &s, frame, probability)
				if frame == 468 {
					wantAudio(t, phrase, 0, 469*512)
				} else if phrase != nil {
					t.Fatal("emitted before cut")
				}
			}
			lowWindows := 8
			if resume {
				lowWindows = 2
			}
			for frame := 469; frame < 469+lowWindows; frame++ {
				if push(t, &s, frame, 0.4) != nil {
					t.Fatal("emitted an all-silence continuation")
				}
			}
			end := 469 + lowWindows + 8 + 16
			for frame := 469 + lowWindows; frame < end; frame++ {
				probability := float32(0.4)
				if resume && frame == 469+lowWindows {
					probability = 0.5
				}
				if frame >= end-16 {
					probability = 0
				}
				phrase := push(t, &s, frame, probability)
				if resume && frame == end-1 {
					wantAudio(t, phrase, 469*512, end*512)
				} else if phrase != nil {
					t.Fatalf("unexpected phrase at frame %d", frame)
				}
			}
		})
	}
}

func TestEndAtCut(t *testing.T) {
	var s vad.Segmenter
	for frame := range 469 {
		probability := float32(0.5)
		if frame >= 453 {
			probability = 0
		}
		phrase := push(t, &s, frame, probability)
		if frame == 468 {
			wantAudio(t, phrase, 0, 469*512)
		} else if phrase != nil {
			t.Fatal("emitted before simultaneous cut and end")
		}
	}
	for frame := 469; frame < 477; frame++ {
		push(t, &s, frame, 0.4)
	}
	if s.Flush() != nil {
		t.Fatal("continued speech after confirmed end at cut")
	}
}

func TestFlushResetAndOwnership(t *testing.T) {
	for _, speech := range []int{7, 8} {
		var s vad.Segmenter
		for frame := range speech + 2 {
			audio := window(frame)
			probability := float32(0.5)
			if frame >= speech {
				probability = 0
			}
			if _, err := s.Push(audio, probability); err != nil {
				t.Fatal(err)
			}
			clear(audio)
		}
		phrase := s.Flush()
		if speech == 8 {
			wantAudio(t, phrase, 0, (speech+2)*512)
		} else if phrase != nil {
			t.Fatal("flush counted final silence toward minimum")
		}
		if s.Flush() != nil {
			t.Fatal("repeated flush returned audio")
		}
		for frame := range 8 {
			push(t, &s, frame, 0.5)
		}
		s.Reset()
		if s.Flush() != nil {
			t.Fatal("reset left pending audio")
		}
		for frame := range 8 {
			push(t, &s, frame, 0.5)
		}
		wantAudio(t, s.Flush(), 0, 8*512)
		if speech == 8 {
			wantAudio(t, phrase, 0, (speech+2)*512)
		}
	}
}

func TestInvalidInputDoesNotAdvance(t *testing.T) {
	badSample := window(0)
	badSample[511] = float32(math.NaN())
	for _, tc := range []struct {
		name        string
		audio       []float32
		probability float32
	}{
		{"empty", nil, 0}, {"short", make([]float32, 511), 0},
		{"long", make([]float32, 513), 0}, {"nan sample", badSample, 0},
		{"infinite sample", append(make([]float32, 511), float32(math.Inf(-1))), 0},
		{"nan probability", window(0), float32(math.NaN())},
		{"infinite probability", window(0), float32(math.Inf(1))},
		{"negative probability", window(0), -0.01}, {"large probability", window(0), 1.01},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s, control vad.Segmenter
			for frame := range 23 {
				probability := float32(0.5)
				if frame >= 8 {
					probability = 0
				}
				push(t, &s, frame, probability)
				push(t, &control, frame, probability)
			}
			if phrase, err := s.Push(tc.audio, tc.probability); err == nil || phrase != nil {
				t.Fatalf("invalid input: %d samples returned, error %v", len(phrase), err)
			}
			got := push(t, &s, 23, 0)
			want := push(t, &control, 23, 0)
			if len(got) == 0 || !slices.Equal(got, want) {
				t.Fatal("invalid input altered the pending phrase or endpoint")
			}
		})
	}
}
