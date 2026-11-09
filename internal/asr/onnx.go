package asr

import (
	"fmt"

	ort "github.com/getcharzp/onnxruntime_purego"
)

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
