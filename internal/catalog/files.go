package catalog

import (
	"path/filepath"
	"runtime"
)

// HaasFiles names the local files consumed by a Haas recognizer.
type HaasFiles struct {
	Features, Encoder, Decoder, Joint, Vocabulary string
}

// HaasBundle names files under the directory from download.Manager.Directory.
// Use true for the downloadable int8 encoder or false for the fp32 parity reference.
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

// ParakeetFiles names the local files consumed by a Parakeet int8 recognizer.
type ParakeetFiles struct {
	Features, Encoder, DecoderJoint, Vocabulary string
}

// ParakeetBundle names the recognizer artifact's int8 files under the directory
// from download.Manager.Directory.
func ParakeetBundle(id, dir string) ParakeetFiles {
	return ParakeetFiles{
		Features: modelPath(id, dir, "features"), Encoder: modelPath(id, dir, "encoder"),
		DecoderJoint: modelPath(id, dir, "decoder_joint"), Vocabulary: modelPath(id, dir, "vocabulary"),
	}
}

// SileroModel names the local Silero VAD graph.
func SileroModel(dir string) string { return modelPath(Silero, dir, "model") }

// PLaMoModel names the local translation weights.
func PLaMoModel(id, dir string) string { return modelPath(id, dir, "model") }

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
