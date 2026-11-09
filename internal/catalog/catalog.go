// Package catalog pins the files and licenses used by the application.
package catalog

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"slices"
)

// These constants identify the downloadable artifacts.
const (
	Haas          = "haas-ja-v1.0-int8"
	Parakeet      = "parakeet-tdt-0.6b-v2-int8"
	ParakeetV3    = "parakeet-tdt-0.6b-v3-int8"
	Silero        = "silero-vad-v6.2.3"
	PLaMo         = "plamo-3-translate-2b-q4_k_m"
	LlamaCPP      = "llama.cpp-b12201-cpu"
	ONNXRuntime   = "onnxruntime-1.31.1-cpu"
	plamoRevision = "4f672106b847200bfdc0f6af6c321cd94eba1aed"
)

// PLaMoAcceptanceKey names the license text so models can share saved consent.
const PLaMoAcceptanceKey = "plamo-community-license@2026-11-09"

//go:embed licenses/plamo-community.txt
var plamoLicense string

// License names a license and identifies the material it covers in Covers.
// Text, when nonempty, holds the complete license for display before a download starts.
type License struct {
	Name, URL, Covers, Text, Credit string
}

// File is one installed regular file. Member is its exact archive path, or empty
// for a plain download. Role identifies files consumed by the app, including the
// server executable and runtime library. The Role fields in pins.go list the valid roles.
type File struct {
	Path, Role, Member, SHA256 string
	Size                       int64
	Executable                 bool
}

// Source pins the bytes fetched from URL. Format is "file", "zip", or "tar.gz".
// Archive sources also pin every selected member in Files; unselected members are ignored.
type Source struct {
	URL, SHA256, Format string
	Size                int64
	Files               []File
}

// Artifact describes a complete installation. A nonempty AcceptanceKey must be
// present in saved settings before download. Languages uses the codes in translation.go.
type Artifact struct {
	ID, Name, AcceptanceKey string
	Languages               []string
	Licenses                []License
	Sources                 []Source
}

type entry struct {
	Artifact
	platforms        map[string]platform
	referenceEncoder string
}

type platform struct {
	Sources  []Source
	Licenses []License
}

// Lookup returns an independent copy of an Artifact for linux or windows on x64.
func Lookup(id, goos string) (Artifact, error) {
	if goos != "linux" && goos != "windows" {
		return Artifact{}, fmt.Errorf("unsupported OS %q", goos)
	}
	e, ok := artifacts[id]
	if !ok {
		return Artifact{}, fmt.Errorf("unknown artifact %q", id)
	}
	a := e.Artifact
	a.ID = id
	a.Languages = slices.Clone(a.Languages)
	a.Licenses = slices.Clone(a.Licenses)
	if e.platforms != nil {
		a.Sources = e.platforms[goos].Sources
		a.Licenses = append(a.Licenses, e.platforms[goos].Licenses...)
	}
	a.Sources = slices.Clone(a.Sources)
	for i := range a.Sources {
		a.Sources[i].Files = slices.Clone(a.Sources[i].Files)
	}
	return a, nil
}

// List returns artifacts in ID order without opening files or making network requests.
func List(goos string) ([]Artifact, error) {
	ids := make([]string, 0, len(artifacts))
	for id := range artifacts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := make([]Artifact, 0, len(ids))
	for _, id := range ids {
		a, err := Lookup(id, goos)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, nil
}

// Path joins dir with the installed file for role. dir is the artifact directory,
// not the data directory. This does not check that the file is installed.
func (a Artifact) Path(dir, role string) (string, error) {
	for _, source := range a.Sources {
		for _, f := range source.Files {
			if f.Role == role && role != "" {
				return filepath.Join(dir, f.Path), nil
			}
		}
	}
	return "", fmt.Errorf("artifact %s has no file for role %q", a.ID, role)
}

func plain(base, path, role string, size int64, hash string) Source {
	return Source{URL: base + path, Size: size, SHA256: hash, Format: "file", Files: []File{{Path: path, Role: role, Size: size, SHA256: hash}}}
}
