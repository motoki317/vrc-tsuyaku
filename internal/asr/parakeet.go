// Reimplements the combined prediction/joint step in onnx-asr 0.12.0 (MIT),
// https://github.com/istupakov/onnx-asr, models/nemo.py.
// No upstream source code is copied.
// See ../../THIRD_PARTY_NOTICES.md.

package asr

import (
	"context"
	"fmt"
	"os"

	ort "github.com/getcharzp/onnxruntime_purego"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

// Parakeet recognizes speech with the int8 Parakeet TDT 0.6B v2 or v3 networks.
type Parakeet struct {
	engine                          *Engine
	vocab                           vocabulary
	blank                           int
	features, encoder, decoderJoint *ort.Session
}

func newParakeet(engine *Engine, files catalog.ParakeetFiles) (*Parakeet, error) {
	f, err := os.Open(files.Vocabulary)
	if err != nil {
		return nil, fmt.Errorf("open vocabulary: %w", err)
	}
	vocab, err := readVocabulary(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if (len(vocab) != 1025 && len(vocab) != 8193) || vocab[len(vocab)-1] != "<blk>" {
		return nil, fmt.Errorf("Parakeet vocabulary must have 1025 (v2) or 8193 (v3) entries ending with <blk>")
	}
	p := &Parakeet{engine: engine, vocab: vocab, blank: len(vocab) - 1}
	for _, spec := range []struct {
		name, path string
		session    **ort.Session
	}{
		{"features", files.Features, &p.features},
		{"encoder", files.Encoder, &p.encoder},
		{"decoder/joint", files.DecoderJoint, &p.decoderJoint},
	} {
		*spec.session, err = engine.session(spec.path, 4)
		if err != nil {
			p.close()
			return nil, fmt.Errorf("load Parakeet %s: %w", spec.name, err)
		}
	}
	return p, nil
}

func (p *Parakeet) close() {
	for _, session := range []*ort.Session{p.features, p.encoder, p.decoderJoint} {
		if session != nil {
			session.Close()
		}
	}
}

// Transcribe follows the audio, concurrency and cancellation contract of [Recognizer].
func (p *Parakeet) Transcribe(ctx context.Context, audio []float32) (string, error) {
	tokens, err := p.tokens(ctx, audio)
	if err != nil {
		return "", err
	}
	return p.vocab.decode(tokens)
}

func (p *Parakeet) tokens(ctx context.Context, audio []float32) ([]int, error) {
	p.engine.mu.RLock()
	defer p.engine.mu.RUnlock()
	if err := p.engine.checkAudio(ctx, audio); err != nil {
		return nil, err
	}
	encoded, err := p.encode(ctx, audio)
	if err != nil {
		return nil, err
	}
	d, err := p.decoder(encoded)
	if err != nil {
		return nil, err
	}
	defer d.close()
	return greedyTDT(ctx, len(encoded)/1024, tdtConfig{
		blank: p.blank, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10,
	}, d)
}

func (p *Parakeet) encode(ctx context.Context, audio []float32) ([]float32, error) {
	x, err := ort.NewTensor([]int64{1, int64(len(audio))}, audio)
	if err != nil {
		return nil, err
	}
	defer x.Close()
	length, err := ort.NewTensor([]int64{1}, []int64{int64(len(audio))})
	if err != nil {
		return nil, err
	}
	defer length.Close()
	features, err := p.features.Run(map[string]*ort.Value{"waveforms": x, "waveforms_lens": length})
	if err != nil {
		return nil, fmt.Errorf("Parakeet features: %w", err)
	}
	defer closeValues(features)
	if _, _, err := tensorData[float32](features, "features", []int64{1, 128, -1}); err != nil {
		return nil, err
	}
	if _, _, err := tensorData[int64](features, "features_lens", []int64{1}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	encoded, err := p.encoder.Run(map[string]*ort.Value{"audio_signal": features["features"], "length": features["features_lens"]})
	if err != nil {
		return nil, fmt.Errorf("Parakeet encoder: %w", err)
	}
	defer closeValues(encoded)
	return encoderFrames(encoded)
}

type parakeetDecoder struct {
	p           *Parakeet
	encoded     []float32
	last        int32
	state, next map[string]*ort.Value
}

func (p *Parakeet) decoder(encoded []float32) (*parakeetDecoder, error) {
	d := &parakeetDecoder{p: p, encoded: encoded, last: int32(p.blank), state: make(map[string]*ort.Value)}
	for _, name := range []string{"output_states_1", "output_states_2"} {
		value, err := ort.NewTensor([]int64{2, 1, 640}, make([]float32, 2*640))
		if err != nil {
			d.close()
			return nil, err
		}
		d.state[name] = value
	}
	return d, nil
}

func (d *parakeetDecoder) close() {
	closeValues(d.state)
	closeValues(d.next)
}

func (d *parakeetDecoder) Scores(frame int) ([]float32, error) {
	// A blank makes greedyTDT skip Accept. Discard the uncommitted candidate
	// from the previous Scores call before computing one from the last token.
	closeValues(d.next)
	d.next = nil
	x, err := ort.NewTensor([]int64{1, 1024, 1}, d.encoded[frame*1024:(frame+1)*1024])
	if err != nil {
		return nil, err
	}
	defer x.Close()
	target, err := ort.NewTensor([]int64{1, 1}, []int32{d.last})
	if err != nil {
		return nil, err
	}
	defer target.Close()
	length, err := ort.NewTensor([]int64{1}, []int32{1})
	if err != nil {
		return nil, err
	}
	defer length.Close()
	d.next, err = d.p.decoderJoint.Run(map[string]*ort.Value{
		"encoder_outputs": x, "targets": target, "target_length": length,
		"input_states_1": d.state["output_states_1"], "input_states_2": d.state["output_states_2"],
	})
	if err != nil {
		return nil, fmt.Errorf("Parakeet decoder/joint: %w", err)
	}
	for _, name := range []string{"output_states_1", "output_states_2"} {
		if _, _, err := tensorData[float32](d.next, name, []int64{2, 1, 640}); err != nil {
			return nil, err
		}
	}
	logits, _, err := tensorData[float32](d.next, "outputs", []int64{1, 1, 1, int64(d.p.blank + 1 + 5)})
	return logits, err
}

func (d *parakeetDecoder) Accept(token int) error {
	// Commit the candidate prediction state from the previous Scores call.
	closeValues(d.state)
	d.state, d.next = d.next, nil
	d.last = int32(token)
	return nil
}
