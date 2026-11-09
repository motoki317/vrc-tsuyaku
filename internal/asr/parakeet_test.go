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
	p, err := e.LoadParakeet(catalog.ParakeetBundle(catalog.Parakeet, bundle))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

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
		tokens, err := p.tokens(t.Context(), readTestWAV(t, wavDir, ref.ID))
		if err != nil {
			t.Fatalf("%s: %v", ref.ID, err)
		}
		if got := onnxASRText(p.vocab, tokens); got != ref.Hyp {
			t.Fatalf("%s: got %q, want %q, tokens %v", ref.ID, got, ref.Hyp, tokens)
		}
		if (i+1)%50 == 0 {
			t.Logf("%d/%d identical", i+1, len(refs))
		}
	}
	t.Logf("Parakeet v2 int8, four threads: %d/%d identical", len(refs), len(refs))
}

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
	p, err := e.LoadParakeet(catalog.ParakeetBundle(catalog.ParakeetV3, bundle))
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, ref := range refs {
		if ref.Lang == "" || len(ref.Tokens) == 0 {
			t.Fatalf("%s: reference must include language and token IDs", ref.ID)
		}
		tokens, err := p.tokens(t.Context(), readTestWAV(t, wavDir, ref.ID))
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
	if _, err := p.Transcribe(ctx, []float32{0}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	for _, audio := range [][]float32{nil, {float32(math.NaN())}, {float32(math.Inf(1))}} {
		if _, err := p.Transcribe(t.Context(), audio); err == nil {
			t.Fatalf("accepted audio %v", audio)
		}
	}
	audio := readTestWAV(t, wavDir, refs[0].ID)
	before := slices.Clone(audio)
	tokens, err := p.tokens(t.Context(), audio)
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
			text, err := p.Transcribe(t.Context(), audio)
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
	if _, err := p.Transcribe(t.Context(), audio); err == nil {
		t.Fatal("transcription succeeded after close")
	}
	if _, err := e.LoadParakeet(catalog.ParakeetBundle(catalog.Parakeet, bundle)); err == nil {
		t.Fatal("load succeeded after close")
	}
}
