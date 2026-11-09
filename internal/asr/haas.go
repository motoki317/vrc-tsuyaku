// Reimplements NeMo 2.7.3 zero-padding of clips under one second to one second,
// split across both sides with any extra sample on the right (Apache-2.0):
// https://github.com/NVIDIA/NeMo, TranscriptionTensorDataset._pad_audio in
// nemo/collections/asr/parts/mixins/transcription.py.
// Uses the Haas-ASR prediction-state design (Apache-2.0),
// https://github.com/third-intelligence/Haas-ASR at commit
// acc614f68769dcbfae9dba96b48439c4a66295af. No upstream source code is copied.
// The Python reference is tools/haas-export/haas_full_onnx.py.
// See ../../THIRD_PARTY_NOTICES.md.

package asr

import (
	"context"
	"fmt"
	"os"
	"slices"

	ort "github.com/getcharzp/onnxruntime_purego"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

const haasBlank = 3072

// Haas recognizes Japanese with the exported Haas encoder, prediction and joint
// networks.
type Haas struct {
	engine                         *Engine
	vocab                          vocabulary
	features, encoder, step, joint *ort.Session
}

func newHaas(engine *Engine, files catalog.HaasFiles) (*Haas, error) {
	f, err := os.Open(files.Vocabulary)
	if err != nil {
		return nil, fmt.Errorf("open vocabulary: %w", err)
	}
	vocab, err := readVocabulary(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if len(vocab) != haasBlank+1 || vocab[haasBlank] != "<blk>" {
		return nil, fmt.Errorf("Haas vocabulary must have %d entries ending with <blk>", haasBlank+1)
	}
	h := &Haas{engine: engine, vocab: vocab}
	for _, spec := range []struct {
		name, path string
		session    **ort.Session
	}{
		{"features", files.Features, &h.features},
		{"encoder", files.Encoder, &h.encoder},
		{"prediction", files.Decoder, &h.step},
		{"joint", files.Joint, &h.joint},
	} {
		*spec.session, err = engine.session(spec.path, 4)
		if err != nil {
			h.close()
			return nil, fmt.Errorf("load Haas %s: %w", spec.name, err)
		}
	}
	return h, nil
}

func (h *Haas) close() {
	for _, session := range []*ort.Session{h.features, h.encoder, h.step, h.joint} {
		if session != nil {
			session.Close()
		}
	}
	h.features, h.encoder, h.step, h.joint = nil, nil, nil, nil
}

// Transcribe follows the audio, concurrency and cancellation contract of [Recognizer].
func (h *Haas) Transcribe(ctx context.Context, audio []float32) (string, error) {
	tokens, err := h.tokens(ctx, audio)
	if err != nil {
		return "", err
	}
	return h.vocab.decode(tokens)
}

func (h *Haas) tokens(ctx context.Context, audio []float32) ([]int, error) {
	h.engine.mu.RLock()
	defer h.engine.mu.RUnlock()
	if err := h.engine.checkAudio(ctx, audio); err != nil {
		return nil, err
	}
	if h.encoder == nil {
		return nil, fmt.Errorf("Haas model is released")
	}
	encoded, err := h.encode(ctx, audio)
	if err != nil {
		return nil, err
	}
	d, err := h.decoder(encoded)
	if err != nil {
		return nil, err
	}
	defer d.close()
	return greedyTDT(ctx, len(encoded)/1024, tdtConfig{
		blank: haasBlank, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10,
	}, d)
}

func padAudio(audio []float32) []float32 {
	if len(audio) >= 16000 {
		return audio
	}
	padded := make([]float32, 16000)
	copy(padded[(16000-len(audio))/2:], audio)
	return padded
}

func (h *Haas) encode(ctx context.Context, audio []float32) ([]float32, error) {
	audio = padAudio(audio)
	x, err := ort.NewTensor([]int64{1, int64(len(audio))}, audio)
	if err != nil {
		return nil, err
	}
	defer x.Close()
	features, err := h.features.Run(map[string]*ort.Value{"audio": x})
	if err != nil {
		return nil, fmt.Errorf("Haas features: %w", err)
	}
	defer closeValues(features)
	mel, shape, err := tensorData[float32](features, "mel", []int64{1, 128, -1})
	if err != nil {
		return nil, err
	}
	frames := int(shape[2])
	paddedFrames := (frames + 15) / 16 * 16
	padded := make([]float32, 128*paddedFrames)
	for band := range 128 {
		copy(padded[band*paddedFrames:], mel[band*frames:(band+1)*frames])
	}
	melInput, err := ort.NewTensor([]int64{1, 128, int64(paddedFrames)}, padded)
	if err != nil {
		return nil, err
	}
	defer melInput.Close()
	length, err := ort.NewTensor([]int64{1}, []int64{int64(len(audio) / 160)})
	if err != nil {
		return nil, err
	}
	defer length.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	encoded, err := h.encoder.Run(map[string]*ort.Value{"audio_signal": melInput, "length": length})
	if err != nil {
		return nil, fmt.Errorf("Haas encoder: %w", err)
	}
	defer closeValues(encoded)
	return encoderFrames(encoded)
}

type haasDecoder struct {
	h       *Haas
	encoded []float32
	state   map[string]*ort.Value
}

func (h *Haas) decoder(encoded []float32) (*haasDecoder, error) {
	conv, err := ort.NewTensor([]int64{2, 1, 960, 4}, make([]float32, 2*960*4))
	if err != nil {
		return nil, err
	}
	rec, err := ort.NewTensor([]int64{2, 1, 5, 64, 64}, make([]float32, 2*5*64*64))
	if err != nil {
		conv.Close()
		return nil, err
	}
	d := &haasDecoder{h: h, encoded: encoded, state: map[string]*ort.Value{"conv_state_out": conv, "rec_state_out": rec}}
	if err := d.Accept(haasBlank); err != nil {
		d.close()
		return nil, err
	}
	return d, nil
}

func (d *haasDecoder) close() { closeValues(d.state) }

func (d *haasDecoder) Accept(token int) error {
	label, err := ort.NewTensor([]int64{1}, []int64{int64(token)})
	if err != nil {
		return err
	}
	defer label.Close()
	next, err := d.h.step.Run(map[string]*ort.Value{
		"label": label, "conv_state": d.state["conv_state_out"], "rec_state": d.state["rec_state_out"],
	})
	if err != nil {
		return fmt.Errorf("Haas prediction: %w", err)
	}
	closeValues(d.state)
	d.state = next
	return nil
}

func (d *haasDecoder) Scores(frame int) ([]float32, error) {
	x, err := ort.NewTensor([]int64{1, 1024}, d.encoded[frame*1024:(frame+1)*1024])
	if err != nil {
		return nil, err
	}
	defer x.Close()
	outputs, err := d.h.joint.Run(map[string]*ort.Value{"enc_frame": x, "dec_out": d.state["dec_out"]})
	if err != nil {
		return nil, fmt.Errorf("Haas joint: %w", err)
	}
	defer closeValues(outputs)
	logits, _, err := tensorData[float32](outputs, "logits", []int64{1, haasBlank + 1 + 5})
	return slices.Clone(logits), err
}
