package catalog

import (
	"path/filepath"
	"runtime"
)

// HaasFiles names the local files consumed by a Haas recognizer.
type HaasFiles struct {
	Features, Encoder, Decoder, Joint, Vocabulary string
}

// HaasBundle names the files in the artifact directory returned by Manager.Directory
// in internal/download. int8Encoder selects the downloadable encoder. False selects
// the fp32 encoder used by offline parity tests.
func HaasBundle(dir string, int8Encoder bool) HaasFiles {
	encoder := filepath.Join(dir, artifacts[Haas].referenceEncoder)
	if int8Encoder {
		encoder = modelPath(Haas, dir, "encoder")
	}
	return HaasFiles{
		Features: modelPath(Haas, dir, "features"), Encoder: encoder,
		Decoder: modelPath(Haas, dir, "decoder"), Joint: modelPath(Haas, dir, "joint"),
		Vocabulary: modelPath(Haas, dir, "vocabulary"),
	}
}

// ParakeetFiles names the local files consumed by a Parakeet v2 int8 recognizer.
type ParakeetFiles struct {
	Features, Encoder, DecoderJoint, Vocabulary string
}

// ParakeetBundle names the v2 int8 files in the artifact directory returned by
// Manager.Directory in internal/download.
func ParakeetBundle(dir string) ParakeetFiles {
	return ParakeetFiles{
		Features: modelPath(Parakeet, dir, "features"), Encoder: modelPath(Parakeet, dir, "encoder"),
		DecoderJoint: modelPath(Parakeet, dir, "decoder_joint"), Vocabulary: modelPath(Parakeet, dir, "vocabulary"),
	}
}

// SileroModel names the local Silero VAD graph.
func SileroModel(dir string) string { return modelPath(Silero, dir, "model") }

// PLaMoModel names the local translation weights.
func PLaMoModel(dir string) string { return modelPath(PLaMo, dir, "model") }

// LlamaServer names the server executable for the current OS.
func LlamaServer(dir string) string { return modelPath(LlamaCPP, dir, "server") }

// ORTLibrary names the runtime library for the current OS.
func ORTLibrary(dir string) string { return modelPath(ONNXRuntime, dir, "runtime") }

func modelPath(id, dir, role string) string {
	a, err := Lookup(id, runtime.GOOS)
	if err != nil {
		panic(err)
	}
	path, err := a.Path(dir, role)
	if err != nil {
		panic(err)
	}
	return path
}
