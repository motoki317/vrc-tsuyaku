package pipeline

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/vad"
)

func levelWindow(db float64) []float32 {
	window := make([]float32, vad.WindowSamples)
	for i := range window {
		window[i] = float32(math.Pow(10, db/20))
	}
	return window
}

func nearLevel(t *testing.T, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > 1e-5 {
		t.Fatalf("got %g, want %g", got, want)
	}
}

func TestAutomaticGainLearning(t *testing.T) {
	var l leveler
	nearLevel(t, l.gainDB, 0)
	for range 7 {
		l.heard(1e-8)
	}
	l.ended()
	nearLevel(t, l.gainDB, 0)
	if l.phrases != 0 {
		t.Fatal("short phrase counted toward learning", l.phrases)
	}
	for i, tc := range []struct{ level, want float64 }{
		{-80, 20},
		{-45, 12.5},
		{-20, 25.0 / 3},
		{-50, 8.75},
		{-60, 11.5625},
	} {
		before := l.gainDB
		for range 8 {
			l.heard(math.Pow(10, tc.level/10))
			nearLevel(t, l.gainDB, before)
		}
		l.ended()
		nearLevel(t, l.gainDB, tc.want)
		if l.phrases != i+1 {
			t.Fatal("phrase count", l.phrases, i+1)
		}
		l.ended()
		nearLevel(t, l.gainDB, tc.want)
	}
}

func TestAutomaticGainAlternatingNeeds(t *testing.T) {
	var l leveler
	for phrase := range 100 {
		power := 1e-4
		if phrase%2 != 0 {
			power = 1e-6
		}
		for range 8 {
			l.heard(power)
		}
		l.ended()
		if phrase >= 3 && (l.gainDB < 7 || l.gainDB > 12) {
			t.Fatalf("phrase %d: gain %g outside 7..12 dB", phrase+1, l.gainDB)
		}
	}
}

func TestSearchGain(t *testing.T) {
	for _, tc := range []struct {
		name             string
		level, cut, want float64
	}{
		{"quiet", -80, 0, -60},
		{"limited by peak", -10, 0, 0},
		{"manual overrides search", -80, -50, -70},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := leveler{searching: true}
			input := levelWindow(tc.level)
			original := slices.Clone(input)
			var out [vad.WindowSamples]float32
			l.process(input, out[:], tc.cut)
			power, peak := measureLevel(out[:])
			nearLevel(t, powerDB(power), tc.want)
			nearLevel(t, l.gainDB, 0)
			if peak > 1 || !slices.Equal(input, original) {
				t.Fatal("search clipped or changed captured audio", peak)
			}
		})
	}
}

func teachLevel(l *leveler, db float64, windows int) {
	power := math.Pow(10, db/10)
	for range windows {
		l.heard(power)
		l.after(true, vad.StartProbability)
	}
	l.ended()
}

func TestSearchLearnsFirstPhrase(t *testing.T) {
	l := leveler{searching: true}
	teachLevel(&l, -60, 7)
	if !l.searching || l.phrases != 0 {
		t.Fatal("short phrase stopped the search", l)
	}
	nearLevel(t, l.gainDB, 0)
	teachLevel(&l, -50, 8)
	if l.searching || l.phrases != 1 {
		t.Fatal("first qualifying phrase did not stop the search", l)
	}
	nearLevel(t, l.gainDB, 10)
	var out [vad.WindowSamples]float32
	l.process(levelWindow(-70), out[:], 0)
	power, _ := measureLevel(out[:])
	nearLevel(t, powerDB(power), -60)
}

func TestSearchTriggers(t *testing.T) {
	type step struct {
		windows     int
		active      bool
		probability float32
		searching   bool
	}
	for _, tc := range []struct {
		name  string
		steps []step
	}{
		{"idle", []step{{311, false, 0, false}, {1, false, 0, true}}},
		{"hints", []step{{7, false, 0.1, false}, {1, false, 0.1, true}}},
		{"broken hints", []step{
			{7, false, 0.1, false}, {1, false, 0.099, false},
			{7, false, 0.1, false}, {1, false, 0.1, true},
		}},
		{"active resets idle", []step{
			{311, false, 0, false}, {1, true, 0, false},
			{311, false, 0, false}, {1, false, 0, true},
		}},
		{"active resets hints", []step{
			{7, false, 0.1, false}, {1, true, vad.StartProbability, false},
			{7, false, 0.1, false}, {1, false, 0.1, true},
		}},
		{"inside phrase", []step{{400, true, 0.4, false}, {1, false, 0, false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := leveler{searching: true}
			for range 10 {
				teachLevel(&l, -40, 8)
			}
			for i, s := range tc.steps {
				for range s.windows {
					l.after(s.active, s.probability)
				}
				if l.searching != s.searching {
					t.Fatalf("step %d: searching %v, want %v", i, l.searching, s.searching)
				}
				nearLevel(t, l.gainDB, 0)
				if l.phrases != 10 {
					t.Fatal("search changed the learned phrase count", l.phrases)
				}
			}
		})
	}
}

func TestSearchReplacesLearnedGain(t *testing.T) {
	l := leveler{searching: true}
	for range 10 {
		teachLevel(&l, -40, 8)
	}
	for range 312 {
		l.after(false, 0)
	}
	teachLevel(&l, -60, 7)
	if !l.searching || l.phrases != 10 {
		t.Fatal("short phrase changed the search or learned count", l)
	}
	nearLevel(t, l.gainDB, 0)
	teachLevel(&l, -60, 8)
	if l.searching || l.phrases != 1 {
		t.Fatal("search did not restart learning at the qualifying phrase", l)
	}
	nearLevel(t, l.gainDB, 20)
	teachLevel(&l, -40, 8)
	nearLevel(t, l.gainDB, 10)
	if l.phrases != 2 {
		t.Fatal("next phrase did not use the restarted mean", l.phrases)
	}
}

func TestSpeechLevelUsesMeanPower(t *testing.T) {
	var l leveler
	for range 6 {
		l.heard(1e-5)
	}
	for range 2 {
		l.heard(0)
	}
	l.ended()
	if l.gainDB < 10 || l.gainDB > 11.5 {
		t.Fatal("digital silence pulled the speech level down", l.gainDB)
	}
}

func TestVADGainCopy(t *testing.T) {
	for _, tc := range []struct {
		name             string
		level, cut, want float64
	}{
		{"automatic", -80, 0, -60},
		{"manual cap", -80, -80, -60},
		{"manual target", -80, -50, -70},
		{"manual no attenuation", -80, -10, -80},
		{"limited by peak", -10, 0, 0},
		{"full scale", 0, 0, 0},
		{"silence", math.Inf(-1), 0, -240},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := leveler{gainDB: 20}
			input := levelWindow(tc.level)
			input[0] = -input[0]
			original := slices.Clone(input)
			var out [vad.WindowSamples]float32
			power, db, _ := l.process(input, out[:], tc.cut)
			nearLevel(t, db, max(-240, tc.level))
			nearLevel(t, power, math.Pow(10, tc.level/10))
			got, peak := measureLevel(out[:])
			nearLevel(t, powerDB(got), tc.want)
			if peak > 1 || !slices.Equal(input, original) {
				t.Fatal("clipped or changed captured audio", peak, input[0])
			}
		})
	}
}

func TestGainUsesOneScaleForTheWholeWindow(t *testing.T) {
	input := levelWindow(-60)
	input[0] = -0.5
	var out [vad.WindowSamples]float32
	l := leveler{gainDB: 20}
	l.process(input, out[:], 0)
	for i := range input {
		nearLevel(t, float64(out[i]), 2*float64(input[i]))
	}
}

func TestPhraseGain(t *testing.T) {
	for _, tc := range []struct{ input, want float64 }{
		{0, 0}, {-20, -20}, {-45, -25}, {-80, -40}, {math.Inf(-1), -240},
	} {
		t.Run(fmt.Sprint(tc.input), func(t *testing.T) {
			phrase := levelWindow(tc.input)
			amplifyPhrase(phrase)
			power, peak := measureLevel(phrase)
			nearLevel(t, powerDB(power), tc.want)
			if peak > 1 {
				t.Fatal("clipped phrase", peak)
			}
		})
	}
	phrase := levelWindow(-60)
	phrase[0] = 0.5
	original := slices.Clone(phrase)
	amplifyPhrase(phrase)
	for i := range phrase {
		nearLevel(t, float64(phrase[i]), 2*float64(original[i]))
	}
	amplifyPhrase(nil)
}

func TestManualGateHistory(t *testing.T) {
	var l leveler
	var out [vad.WindowSamples]float32
	quiet, loud := levelWindow(-70), levelWindow(-40)
	if _, _, open := l.process(quiet, out[:], -50); open {
		t.Fatal("gate open without earlier loud windows")
	}
	if _, _, open := l.process(loud, out[:], -50); !open {
		t.Fatal("loud window did not open gate")
	}
	for i := range 16 {
		if _, _, open := l.process(quiet, out[:], -50); !open {
			t.Fatal("hold ended early", i)
		}
	}
	if _, _, open := l.process(quiet, out[:], -50); open {
		t.Fatal("hold lasted more than 16 windows")
	}
	_, level, _ := l.process(loud, out[:], 0)
	if _, _, open := l.process(quiet, out[:], level); !open {
		t.Fatal("automatic mode lost history or gate excluded equality")
	}
	if _, _, open := l.process(quiet, out[:], -30); open {
		t.Fatal("higher cut level did not close gate against earlier levels")
	}
	if _, _, open := l.process(quiet, out[:], -50); !open {
		t.Fatal("lower cut level did not reopen gate against earlier levels")
	}
}

func TestLevelWindowDoesNotAllocate(t *testing.T) {
	var l leveler
	var input, out [vad.WindowSamples]float32
	allocations := testing.AllocsPerRun(100, func() {
		power, _, _ := l.process(input[:], out[:], -50)
		l.after(false, 0.1)
		l.heard(power)
		l.ended()
	})
	if allocations != 0 {
		t.Fatal("allocations per window", allocations)
	}
}
