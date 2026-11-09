package asr

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

type reference struct {
	ID  string `json:"id"`
	Hyp string `json:"hyp"`
	Ref string `json:"ref"`
}

func requireEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Skipf("set %s to run this gated test", name)
	}
	return value
}

func readReferences(path string) ([]reference, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []reference
	seen := make(map[string]bool)
	s := bufio.NewScanner(f)
	for s.Scan() {
		var row reference
		if err := json.Unmarshal(s.Bytes(), &row); err != nil {
			return nil, err
		}
		if row.ID == "" || row.ID == "." || row.ID == ".." || strings.ContainsAny(row.ID, `/\`) || row.Ref == "" || seen[row.ID] {
			return nil, fmt.Errorf("invalid or duplicate reference row: %+v", row)
		}
		seen[row.ID] = true
		rows = append(rows, row)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("reference contains no clips")
	}
	return rows, nil
}

func readTestWAV(t *testing.T, dir, id string) []float32 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, id+".wav"))
	if err != nil {
		t.Fatal(err)
	}
	// The parity fixture uses canonical PCM16 WAVs with a 44-byte header.
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:16]) != "WAVEfmt " ||
		binary.LittleEndian.Uint32(data[16:20]) != 16 || binary.LittleEndian.Uint16(data[20:22]) != 1 ||
		binary.LittleEndian.Uint16(data[22:24]) != 1 || binary.LittleEndian.Uint32(data[24:28]) != 16000 ||
		binary.LittleEndian.Uint16(data[34:36]) != 16 || string(data[36:40]) != "data" ||
		int(binary.LittleEndian.Uint32(data[40:44])) != len(data)-44 || (len(data)-44)%2 != 0 {
		t.Fatalf("%s: expected canonical mono 16 kHz PCM16 WAV", id)
	}
	audio := make([]float32, (len(data)-44)/2)
	for i := range audio {
		audio[i] = float32(int16(binary.LittleEndian.Uint16(data[44+2*i:]))) / 32768
	}
	return audio
}

func loadTestHaas(t *testing.T, e *Engine, bundle string, int8Encoder bool) *Haas {
	t.Helper()
	h, err := e.LoadHaas(catalog.HaasBundle(bundle, int8Encoder))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHaasCPUParity(t *testing.T) {
	lib := requireEnv(t, "VRC_TSUYAKU_TEST_ORT_LIB")
	bundle := requireEnv(t, "VRC_TSUYAKU_TEST_HAAS_BUNDLE")
	wavDir := requireEnv(t, "VRC_TSUYAKU_TEST_HAAS_WAV_DIR")
	e, err := NewEngine(lib)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, variant := range []struct {
		name, env string
		int8      bool
	}{
		{"fp32", "VRC_TSUYAKU_TEST_HAAS_REF_FP32", false},
		{"int8", "VRC_TSUYAKU_TEST_HAAS_REF_INT8", true},
	} {
		t.Run(variant.name, func(t *testing.T) {
			refs, err := readReferences(requireEnv(t, variant.env))
			if err != nil {
				t.Fatal(err)
			}
			h := loadTestHaas(t, e, bundle, variant.int8)
			var errors, characters int
			for i, ref := range refs {
				audio := readTestWAV(t, wavDir, ref.ID)
				tokens, err := h.tokens(t.Context(), audio)
				if err != nil {
					t.Fatalf("%s: %v", ref.ID, err)
				}
				text, err := h.vocab.decode(tokens)
				if err != nil || text != ref.Hyp {
					t.Fatalf("%s: got %q, want %q, tokens %v, error %v", ref.ID, text, ref.Hyp, tokens, err)
				}
				errors += characterErrors(ref.Ref, text)
				characters += len([]rune(ref.Ref))
				if (i+1)%50 == 0 {
					t.Logf("%d/%d identical", i+1, len(refs))
				}
			}
			t.Logf("%s: %d/%d identical, strict CER %.4f%% (%d/%d)", variant.name, len(refs), len(refs), 100*float64(errors)/float64(characters), errors, characters)
		})
	}
}

// characterErrors computes rune edit distance without text normalization.
func characterErrors(reference, hypothesis string) int {
	a, b := []rune(reference), []rune(hypothesis)
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i, want := range a {
		diagonal := row[0]
		row[0] = i + 1
		for j, got := range b {
			cost := 0
			if want != got {
				cost = 1
			}
			next := min(row[j+1]+1, row[j]+1, diagonal+cost)
			diagonal, row[j+1] = row[j+1], next
		}
	}
	return row[len(b)]
}

func TestReadReferences(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       int
	}{
		{"empty", "", 0},
		{"single", `{"id":"a","hyp":"","ref":"a"}` + "\n", 1},
		{"multiple", `{"id":"b","hyp":"b","ref":"b"}` + "\n" + `{"id":"a","hyp":"a","ref":"a"}` + "\n", 2},
		{"duplicate", `{"id":"a","ref":"a"}` + "\n" + `{"id":"a","ref":"a"}` + "\n", 0},
		{"invalid", `{"id":"../a","ref":"a"}`, 0},
		{"malformed", "{", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "reference.jsonl")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			refs, err := readReferences(path)
			if (err != nil) != (tc.want == 0) || len(refs) != tc.want {
				t.Fatalf("readReferences = %v, %v, want %d rows", refs, err, tc.want)
			}
		})
	}
}

func TestCharacterErrors(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"", "", 0}, {"", "あ😀", 2}, {"abc", "", 3}, {"猫です", "犬です", 1}, {"猫", "猫。", 1}, {"a b", "ab", 1}, {"abc", "bca", 2}} {
		t.Run(fmt.Sprintf("%s/%s", tc.a, tc.b), func(t *testing.T) {
			if got := characterErrors(tc.a, tc.b); got != tc.want {
				t.Fatalf("distance = %d, want %d", got, tc.want)
			}
		})
	}
}
