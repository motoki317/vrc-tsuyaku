package asr

import (
	"fmt"
	"slices"

	ort "github.com/getcharzp/onnxruntime_purego"
)

type floatTensorSnapshot struct {
	shape []int64
	data  []float32
}

func snapshotFloats(values map[string]*ort.Value, names ...string) (map[string]floatTensorSnapshot, error) {
	state := make(map[string]floatTensorSnapshot, len(names))
	for _, name := range names {
		value := values[name]
		if value == nil {
			return nil, fmt.Errorf("checkpoint tensor %q is missing", name)
		}
		shape, err := value.GetShape()
		if err != nil {
			return nil, err
		}
		data, err := ort.GetTensorData[float32](value)
		if err != nil {
			return nil, err
		}
		// Own the shape and data because the decoder's Accept method closes the source tensors.
		state[name] = floatTensorSnapshot{slices.Clone(shape), slices.Clone(data)}
	}
	return state, nil
}

func restoreFloats(state map[string]floatTensorSnapshot) (map[string]*ort.Value, error) {
	values := make(map[string]*ort.Value, len(state))
	for name, tensor := range state {
		// NewTensor aliases tensor.data. Use the checkpoint only once because decoding can mutate that buffer.
		value, err := ort.NewTensor(tensor.shape, tensor.data)
		if err != nil {
			closeValues(values)
			return nil, err
		}
		values[name] = value
	}
	return values, nil
}

// encoderFrames trims and transposes [1,1024,paddedFrames] to [validFrames,1024].
func encoderFrames(encoded map[string]*ort.Value) ([]float32, error) {
	data, encShape, err := tensorData[float32](encoded, "outputs", []int64{1, 1024, -1})
	if err != nil {
		return nil, err
	}
	lengths, _, err := tensorData[int64](encoded, "encoded_lengths", []int64{1})
	if err != nil {
		return nil, err
	}
	if lengths[0] < 0 || lengths[0] > encShape[2] {
		return nil, fmt.Errorf("encoder length %d outside [0, %d]", lengths[0], encShape[2])
	}
	frames, stride := int(lengths[0]), int(encShape[2])
	transposed := make([]float32, frames*1024)
	for frame := range frames {
		for channel := range 1024 {
			transposed[frame*1024+channel] = data[channel*stride+frame]
		}
	}
	return transposed, nil
}

func closeValues(values map[string]*ort.Value) {
	for _, value := range values {
		value.Close()
	}
}

func tensorData[T ort.TensorElement](values map[string]*ort.Value, name string, wantShape []int64) ([]T, []int64, error) {
	value := values[name]
	if value == nil {
		return nil, nil, fmt.Errorf("model output %q is missing", name)
	}
	shape, err := value.GetShape()
	if err != nil {
		return nil, nil, err
	}
	if len(shape) != len(wantShape) {
		return nil, nil, fmt.Errorf("output %q shape %v, want %v", name, shape, wantShape)
	}
	for i, dim := range shape {
		if dim < 0 || (wantShape[i] >= 0 && dim != wantShape[i]) {
			return nil, nil, fmt.Errorf("output %q shape %v, want %v", name, shape, wantShape)
		}
	}
	data, err := ort.GetTensorData[T](value)
	if err != nil {
		return nil, nil, fmt.Errorf("output %q: %w", name, err)
	}
	return data, shape, nil
}
