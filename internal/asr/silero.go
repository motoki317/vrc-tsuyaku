// Reimplements the OnnxWrapper state and audio-history protocol in silero-vad
// v6.2.3 (MIT), src/silero_vad/utils_vad.py at
// https://github.com/snakers4/silero-vad/tree/5cd7945676eb32225748052e2e6a0580e4686a08.
// No upstream source code is copied. See ../../THIRD_PARTY_NOTICES.md.

package asr

import (
	"context"
	"fmt"
	"math"
	"path/filepath"

	ort "github.com/getcharzp/onnxruntime_purego"
)

// Silero shares one session across independent audio streams.
// Its session lives until the last owner releases it or Engine.Close.
type Silero struct {
	engine  *Engine
	session *ort.Session
}

// LoadSilero loads the Silero VAD v6.2.3 graph.
// It converts path to an absolute path and uses it as the cache key.
// Reusing the same cache key returns the cached model.
// Release the returned reference after its streams finish. The release function
// is idempotent. The last release unloads the session.
func (e *Engine) LoadSilero(path string) (*Silero, func(), error) {
	if path == "" {
		return nil, nil, fmt.Errorf("Silero model path is empty")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve Silero path: %w", err)
	}
	return loadModel(e, e.sileros, path, func() (*Silero, error) {
		session, err := e.session(path, 1)
		if err != nil {
			return nil, fmt.Errorf("load Silero: %w", err)
		}
		return &Silero{engine: e, session: session}, nil
	})
}

func (s *Silero) close() {
	s.session.Close()
	s.session = nil
}

// SileroStream owns one stream's recurrent state and 64 preceding audio samples.
// Call its methods sequentially. Different streams can run concurrently.
type SileroStream struct {
	model   *Silero
	state   [2 * 128]float32
	context [64]float32
}

// NewStream creates a stream with no native resources of its own.
// The stream does not need closing.
func (s *Silero) NewStream() *SileroStream {
	return &SileroStream{model: s}
}

// Reset forgets the audio history, for example after restarting capture.
func (s *SileroStream) Reset() {
	clear(s.state[:])
	clear(s.context[:])
}

// Probability consumes exactly 512 finite samples of 16 kHz mono audio.
// Errors leave state and preceding audio samples unchanged. Cancellation is
// checked before inference. Probability fails after Engine.Close.
func (s *SileroStream) Probability(ctx context.Context, audio []float32) (float32, error) {
	e := s.model.engine
	e.mu.RLock()
	defer e.mu.RUnlock()
	if err := e.checkAudio(ctx, audio); err != nil {
		return 0, err
	}
	if s.model.session == nil {
		return 0, fmt.Errorf("Silero model is released")
	}
	if len(audio) != 512 {
		return 0, fmt.Errorf("Silero requires 512 samples, got %d", len(audio))
	}
	var samples [64 + 512]float32
	copy(samples[:], s.context[:])
	copy(samples[64:], audio)
	x, err := ort.NewTensor([]int64{1, 576}, samples[:])
	if err != nil {
		return 0, err
	}
	defer x.Close()
	state, err := ort.NewTensor([]int64{2, 1, 128}, s.state[:])
	if err != nil {
		return 0, err
	}
	defer state.Close()
	sr, err := ort.NewTensor([]int64{}, []int64{16000})
	if err != nil {
		return 0, err
	}
	defer sr.Close()
	outputs, err := s.model.session.Run(map[string]*ort.Value{"input": x, "state": state, "sr": sr})
	if err != nil {
		return 0, fmt.Errorf("Silero inference: %w", err)
	}
	defer closeValues(outputs)
	probability, _, err := tensorData[float32](outputs, "output", []int64{1, 1})
	if err != nil {
		return 0, err
	}
	next, _, err := tensorData[float32](outputs, "stateN", []int64{2, 1, 128})
	if err != nil {
		return 0, err
	}
	p := probability[0]
	if math.IsNaN(float64(p)) || p < 0 || p > 1 {
		return 0, fmt.Errorf("invalid Silero probability %g", p)
	}
	copy(s.state[:], next)
	copy(s.context[:], audio[len(audio)-64:])
	return p, nil
}
