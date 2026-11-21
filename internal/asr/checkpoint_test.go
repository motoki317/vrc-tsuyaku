package asr

import (
	"slices"
	"testing"

	ort "github.com/getcharzp/onnxruntime_purego"
)

// TestDecoderCheckpoints uses synthetic tensors with the pinned ONNX Runtime CPU
// shared library named by VRC_TSUYAKU_TEST_ORT_LIB.
//
//	nix develop -c go test ./internal/asr -run '^TestDecoderCheckpoints$' -v -count=1 -timeout=10m
func TestDecoderCheckpoints(t *testing.T) {
	e, err := NewEngine(requireEnv(t, "VRC_TSUYAKU_TEST_ORT_LIB"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	for _, model := range []string{"haas", "parakeet"} {
		t.Run(model, func(t *testing.T) {
			names := []string{"conv_state_out", "rec_state_out", "dec_out"}
			if model == "parakeet" {
				names = []string{"output_states_1", "output_states_2"}
			}
			state := make(map[string]*ort.Value)
			for _, name := range names {
				value, err := ort.NewTensor([]int64{1, 2}, []float32{1, 2})
				if err != nil {
					closeValues(state)
					t.Fatal(err)
				}
				state[name] = value
			}
			h := &haasDecoder{state: state}
			p := &parakeetDecoder{state: state, last: 7}
			var restore func() error
			if model == "haas" {
				t.Cleanup(h.close)
				restore, err = h.checkpoint()
			} else {
				t.Cleanup(p.close)
				restore, err = p.checkpoint()
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range state {
				data, err := ort.GetTensorData[float32](value)
				if err != nil {
					t.Fatal(err)
				}
				data[0] = 999
			}
			closeValues(state)
			h.state, p.state = nil, nil
			if model == "parakeet" {
				pending, err := ort.NewTensor([]int64{1}, []float32{42})
				if err != nil {
					t.Fatal(err)
				}
				p.next, p.last = map[string]*ort.Value{"pending": pending}, 99
			}
			if err := restore(); err != nil {
				t.Fatal(err)
			}
			if model == "haas" {
				state = h.state
			} else {
				state = p.state
				if p.last != 7 || p.next != nil {
					t.Fatalf("last %d, pending %v", p.last, p.next)
				}
			}
			if len(state) != len(names) {
				t.Fatalf("restored %d tensors, want %d", len(state), len(names))
			}
			for _, name := range names {
				data, shape, err := tensorData[float32](state, name, []int64{1, 2})
				if err != nil || !slices.Equal(shape, []int64{1, 2}) || !slices.Equal(data, []float32{1, 2}) {
					t.Fatalf("%s: shape %v data %v err %v", name, shape, data, err)
				}
			}
		})
	}
}
