package pipeline

import "math"

const (
	// The automatic gain targets this level for a phrase's speech windows.
	// Silero stops detecting speech under about -50 dBFS, so the target leaves 10 dB of margin.
	vadTargetDB = -40
	// The gain also applies in pauses between phrases.
	// This cap bounds how much background sound it amplifies.
	vadMaxGainDB = 20
	// A quarter per learning phrase recovered from a wrong gain in fewer phrases than an eighth,
	// with the same errors on steady input.
	autoGainShare = 0.25
	// A phrase with fewer speech windows gives too little evidence of a speech level.
	// Eight windows cover the segmenter's 250 ms minimum phrase.
	minimumSpeechWindows = 8
	// A search applies the maximum gain until a learning phrase sets the learned gain.
	// After this many windows outside a phrase (10 s), a search starts.
	// Ten seconds exceeds the pauses inside a conversation.
	searchIdleWindows = 312
	// A hint is a window outside a phrase at searchHintProbability or more.
	// Typing and white noise give runs of at most three hints.
	// Speech at -50 to -55 dBFS gives sustained runs, so eight hints distinguish it.
	searchHintWindows     = 8
	searchHintProbability = 0.1
	// A window counts as above the cut level for this many windows after the last
	// window at or above it. This keeps word endings inside a phrase.
	gateHoldWindows = 16
	// The recognizer gets phrases near the level of ordinary recordings.
	// Recognition worsened on quieter phrases.
	phraseTargetDB = -25
	// Very quiet phrases need more gain than vadMaxGainDB.
	phraseMaxGainDB = 40
	// JSON cannot carry -Inf. The meter shows digital silence at -240 dBFS.
	levelFloorPower = 1e-24
)

// leveler holds the learned gain and count of learning phrases since the last search.
// It also holds the search state, the current phrase's speech power, and a ring
// of recent window levels for the cut level.
type leveler struct {
	gainDB        float64
	phrases       int
	searching     bool
	idleWindows   int
	hintWindows   int
	speechPower   float64
	speechWindows int
	recent        [gateHoldWindows + 1]float64
	next, count   int
}

func (l *leveler) process(window, out []float32, cutLevel float64) (power, level float64, open bool) {
	power, peak := measureLevel(window)
	level = powerDB(power)
	l.recent[l.next] = level
	l.next = (l.next + 1) % len(l.recent)
	l.count = min(l.count+1, len(l.recent))
	gainDB := l.gainDB
	if l.searching {
		gainDB = vadMaxGainDB
	}
	open = cutLevel == 0
	if cutLevel != 0 {
		gainDB = min(vadMaxGainDB, max(0, vadTargetDB-cutLevel))
		for _, previous := range l.recent[:l.count] {
			open = open || previous >= cutLevel
		}
	}
	applyGain(window, out, gainDB, peak)
	return power, level, open
}

func (l *leveler) after(active bool, probability float32) {
	if active {
		l.idleWindows, l.hintWindows = 0, 0
		return
	}
	l.idleWindows = min(l.idleWindows+1, searchIdleWindows)
	if probability >= searchHintProbability {
		l.hintWindows = min(l.hintWindows+1, searchHintWindows)
	} else {
		l.hintWindows = 0
	}
	if l.idleWindows == searchIdleWindows || l.hintWindows == searchHintWindows {
		l.searching = true
	}
}

func (l *leveler) heard(power float64) {
	l.speechPower += power
	l.speechWindows++
}

func (l *leveler) ended() {
	if l.speechWindows >= minimumSpeechWindows {
		need := min(vadMaxGainDB, max(0, vadTargetDB-powerDB(l.speechPower/float64(l.speechWindows))))
		if l.searching {
			l.gainDB, l.phrases, l.searching = need, 1, false
		} else {
			l.phrases++
			l.gainDB += (need - l.gainDB) * max(1/float64(l.phrases), autoGainShare)
		}
	}
	l.speechPower, l.speechWindows = 0, 0
}

func amplifyPhrase(phrase []float32) {
	if len(phrase) == 0 {
		return
	}
	power, peak := measureLevel(phrase)
	gainDB := min(phraseMaxGainDB, max(0, phraseTargetDB-powerDB(power)))
	applyGain(phrase, phrase, gainDB, peak)
}

func measureLevel(samples []float32) (power, peak float64) {
	for _, sample := range samples {
		x := float64(sample)
		power += x * x
		peak = max(peak, math.Abs(x))
	}
	return power / float64(len(samples)), peak
}

func powerDB(power float64) float64 {
	return 10 * math.Log10(max(power, levelFloorPower))
}

func applyGain(input, out []float32, gainDB, peak float64) {
	gain := max(1, min(math.Pow(10, gainDB/20), 1/peak))
	for i, sample := range input {
		out[i] = float32(float64(sample) * gain)
	}
}
