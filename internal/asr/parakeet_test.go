// Reimplements onnx-asr 0.12.0's text rule for parity comparisons (MIT),
// https://github.com/istupakov/onnx-asr, asr.py DECODE_SPACE_PATTERN.
// No upstream source code is copied. See ../../THIRD_PARTY_NOTICES.md.

package asr

import (
	"context"
	"errors"
	"math"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

// onnxASRText matches Python's Unicode-aware space and word classes without
// applying its regex with Go's ASCII-only word boundaries.
func onnxASRText(v vocabulary, tokens []int) string {
	var joined strings.Builder
	for _, id := range tokens {
		joined.WriteString(strings.ReplaceAll(v[id], "▁", " "))
	}
	runes := []rune(joined.String())
	var text strings.Builder
	for i, r := range runes {
		if unicode.IsSpace(r) || (r >= '\u001c' && r <= '\u001f') {
			if i > 0 && i+1 < len(runes) {
				next := runes[i+1]
				if unicode.IsLetter(next) || unicode.IsNumber(next) || next == '_' {
					text.WriteByte(' ')
				}
			}
		} else {
			text.WriteRune(r)
		}
	}
	return text.String()
}

func TestOnnxASRText(t *testing.T) {
	for _, tc := range []struct{ pieces, want, sentencePiece string }{
		{"▁Hello▁world!", "Hello world!", "Hello world!"},
		{"▁Hello▁,▁world▁!▁", "Hello, world!", "Hello , world !"},
		{"▁▁hello▁▁world", " hello world", "hello  world"},
		{"▁Été▁猫▁²▁_▁-", "Été 猫 ² _-", "Été 猫 ² _ -"},
	} {
		t.Run(tc.pieces, func(t *testing.T) {
			v := vocabulary{tc.pieces}
			if got := onnxASRText(v, []int{0}); got != tc.want {
				t.Fatalf("onnx-asr text %q, want %q", got, tc.want)
			}
			if got, err := v.decode([]int{0}); err != nil || got != tc.sentencePiece {
				t.Fatalf("SentencePiece text %q, %v, want %q", got, err, tc.sentencePiece)
			}
		})
	}
}

func loadTestParakeet(t *testing.T, e *Engine, bundle string) *Parakeet {
	t.Helper()
	p, release, err := e.LoadParakeet(catalog.ParakeetBundle(catalog.Parakeet, bundle))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return p
}

// TestParakeetCPUParity checks transcripts on Linux with onnx-asr's text rule.
// The app keeps SentencePiece spacing, so punctuation spacing can differ.
//
//   - VRC_TSUYAKU_TEST_ORT_LIB names the pinned Linux ONNX Runtime CPU shared library.
//   - VRC_TSUYAKU_TEST_PARAKEET_BUNDLE names the pinned Parakeet v2 int8 bundle from internal/catalog.
//   - VRC_TSUYAKU_TEST_PARAKEET_WAV_DIR contains <id>.wav clips in canonical 44-byte-header, 16 kHz mono PCM16 format.
//   - VRC_TSUYAKU_TEST_PARAKEET_REF names a nonempty JSONL reference. Each row has a unique filename stem id,
//     expected transcript hyp and nonempty human transcript ref.
//
// Use the feature graph from the pinned onnx-asr wheel. The older graph in
// the v2 conversion repository produces different features.
// Generate hyp outside this repository with onnx-asr 0.12.0. Set so to ONNX
// Runtime session options with four intra-op threads and one inter-op thread.
// Set path to the bundle directory.
//
//	providers = ["CPUExecutionProvider"]
//	model = load_model("nemo-parakeet-tdt-0.6b-v2", path, quantization="int8",
//	    sess_options=so, providers=providers,
//	    preprocessor_config={"use_numpy_preprocessors": False, "use_conv_preprocessors": False,
//	                         "sess_options": so, "providers": providers})
//	model.recognize(audio, sample_rate=16000)
//
//	nix develop -c go test ./internal/asr -run 'TestParakeet(CPUParity|Lifecycle)$' -v -count=1 -timeout=20m
func TestParakeetCPUParity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the reference uses the Linux ONNX Runtime build")
	}
	lib := requireEnv(t, "VRC_TSUYAKU_TEST_ORT_LIB")
	bundle := requireEnv(t, "VRC_TSUYAKU_TEST_PARAKEET_BUNDLE")
	wavDir := requireEnv(t, "VRC_TSUYAKU_TEST_PARAKEET_WAV_DIR")
	refs, err := readReferences(requireEnv(t, "VRC_TSUYAKU_TEST_PARAKEET_REF"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(lib)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	p := loadTestParakeet(t, e, bundle)
	for i, ref := range refs {
		tokens, _, err := p.tokens(t.Context(), readTestWAV(t, wavDir, ref.ID), nil)
		if err != nil {
			t.Fatalf("%s: %v", ref.ID, err)
		}
		if got := onnxASRText(p.vocab, tokens); got != ref.Hyp {
			t.Fatalf("%s: got %q, want %q, tokens %v", ref.ID, got, ref.Hyp, tokens)
		}
		if ref.Tokens != nil && !slices.Equal(tokens, ref.Tokens) {
			t.Fatalf("%s: tokens %v, want %v", ref.ID, tokens, ref.Tokens)
		}
		if (i+1)%50 == 0 {
			t.Logf("%d/%d identical", i+1, len(refs))
		}
	}
	t.Logf("Parakeet v2 int8, four threads: %d/%d identical", len(refs), len(refs))
}

// TestParakeetV3CPUParity compares token IDs and both renderings of the transcript on Linux.
//
//   - VRC_TSUYAKU_TEST_ORT_LIB names the pinned Linux ONNX Runtime CPU shared library.
//   - VRC_TSUYAKU_TEST_PARAKEET_V3_BUNDLE names the pinned Parakeet v3 int8 bundle from internal/catalog.
//   - VRC_TSUYAKU_TEST_PARAKEET_V3_WAV_DIR contains <id>.wav clips in canonical 44-byte-header, 16 kHz mono PCM16 format.
//   - VRC_TSUYAKU_TEST_PARAKEET_V3_REF names a nonempty JSONL reference. Each row has a unique filename stem id,
//     expected transcript hyp, nonempty human transcript ref, language code lang and a nonempty integer array tokens.
//     The reference must include at least one clip each for en, de, fr, es and ru.
//
// Generate the reference outside this repository with onnx-asr 0.12.0's
// NemoConformerTdt. Use the four pinned bundle files, the v3 conversion
// repository's config.json, four intra-op threads and one inter-op thread.
// Take token IDs from _decoding and hyp from _decode_tokens.
//
//	nix develop -c go test ./internal/asr -run '^TestParakeetV3CPUParity$' -v -count=1 -timeout=20m
func TestParakeetV3CPUParity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the reference uses the Linux ONNX Runtime build")
	}
	lib := requireEnv(t, "VRC_TSUYAKU_TEST_ORT_LIB")
	bundle := requireEnv(t, "VRC_TSUYAKU_TEST_PARAKEET_V3_BUNDLE")
	wavDir := requireEnv(t, "VRC_TSUYAKU_TEST_PARAKEET_V3_WAV_DIR")
	refs, err := readReferences(requireEnv(t, "VRC_TSUYAKU_TEST_PARAKEET_V3_REF"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(lib)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	p, release, err := e.LoadParakeet(catalog.ParakeetBundle(catalog.ParakeetV3, bundle))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	seen := make(map[string]bool)
	for _, ref := range refs {
		if ref.Lang == "" || len(ref.Tokens) == 0 {
			t.Fatalf("%s: reference must include language and token IDs", ref.ID)
		}
		tokens, _, err := p.tokens(t.Context(), readTestWAV(t, wavDir, ref.ID), nil)
		if err != nil {
			t.Fatalf("%s: %v", ref.ID, err)
		}
		if !slices.Equal(tokens, ref.Tokens) {
			t.Fatalf("%s: tokens %v, want %v", ref.ID, tokens, ref.Tokens)
		}
		if got := onnxASRText(p.vocab, tokens); got != ref.Hyp {
			t.Fatalf("%s: text %q, want %q", ref.ID, got, ref.Hyp)
		}
		if got, err := p.vocab.decode(tokens); err != nil || got != ref.Hyp {
			t.Fatalf("%s: transcript %q, %v, want %q", ref.ID, got, err, ref.Hyp)
		}
		seen[ref.Lang] = true
		t.Logf("%s: identical tokens and text: %s", ref.ID, ref.Hyp)
	}
	for _, language := range []string{"en", "de", "fr", "es", "ru"} {
		if !seen[language] {
			t.Fatalf("reference has no %s clip", language)
		}
	}
}

func TestParakeetConfidenceCut(t *testing.T) {
	lib := requireEnv(t, "VRC_TSUYAKU_TEST_ORT_LIB")
	for _, tc := range []struct {
		name, env string
		model     string
		samples   int
		hz        []float64
	}{
		{"v2", "VRC_TSUYAKU_TEST_PARAKEET_BUNDLE", catalog.Parakeet, 9600, []float64{440}},
		{"v3", "VRC_TSUYAKU_TEST_PARAKEET_V3_BUNDLE", catalog.ParakeetV3, 16000, []float64{50, 150}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := requireEnv(t, tc.env)
			e, err := NewEngine(lib)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			p, release, err := e.LoadParakeet(catalog.ParakeetBundle(tc.model, bundle))
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			checkConfidenceCut(t, p, testTone(tc.samples, tc.hz...), parakeetMinConfidence)
		})
	}
}

// TestParakeetLifecycle checks session sharing, concurrent inference and engine closure with a v2 int8 clip.
//
//   - VRC_TSUYAKU_TEST_ORT_LIB names the pinned ONNX Runtime CPU shared library.
//   - VRC_TSUYAKU_TEST_PARAKEET_BUNDLE names the pinned Parakeet v2 int8 bundle from internal/catalog.
//   - VRC_TSUYAKU_TEST_PARAKEET_WAV_DIR contains the test WAV clips.
//   - VRC_TSUYAKU_TEST_PARAKEET_REF names the onnx-asr JSONL reference.
//
// See [TestParakeetCPUParity] for clip and reference formats and reference generation.
//
//	nix develop -c go test ./internal/asr -run '^TestParakeetLifecycle$' -v -count=1 -timeout=20m
func TestParakeetLifecycle(t *testing.T) {
	lib := requireEnv(t, "VRC_TSUYAKU_TEST_ORT_LIB")
	bundle := requireEnv(t, "VRC_TSUYAKU_TEST_PARAKEET_BUNDLE")
	wavDir := requireEnv(t, "VRC_TSUYAKU_TEST_PARAKEET_WAV_DIR")
	refs, err := readReferences(requireEnv(t, "VRC_TSUYAKU_TEST_PARAKEET_REF"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(lib)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	p := loadTestParakeet(t, e, bundle)
	if again := loadTestParakeet(t, e, bundle); again != p {
		t.Fatal("model sessions were not shared")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Transcribe(ctx, []float32{0}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	for _, audio := range [][]float32{nil, {float32(math.NaN())}, {float32(math.Inf(1))}} {
		if _, err := p.Transcribe(t.Context(), audio, nil); err == nil {
			t.Fatalf("accepted audio %v", audio)
		}
	}
	audio := readTestWAV(t, wavDir, refs[0].ID)
	before := slices.Clone(audio)
	tokens, _, err := p.tokens(t.Context(), audio, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := p.vocab.decode(tokens)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			text, err := p.Transcribe(t.Context(), audio, nil)
			if err == nil && text != want {
				err = errors.New("concurrent transcription differs: " + text)
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(audio, before) {
		t.Fatal("transcription changed the input audio")
	}
	e.Close()
	if _, err := p.Transcribe(t.Context(), audio, nil); err == nil {
		t.Fatal("transcription succeeded after close")
	}
	if _, _, err := e.LoadParakeet(catalog.ParakeetBundle(catalog.Parakeet, bundle)); err == nil {
		t.Fatal("load succeeded after close")
	}
}
