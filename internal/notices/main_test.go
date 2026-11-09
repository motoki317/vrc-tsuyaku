package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/mod/module"
)

func writeFixture(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateNotices(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "go.mod")
	writeFixture(t, mod, []byte("module example.com/app\nrequire (\nexample.com/direct v1.0.0\nexample.com/indirect v2.0.0+incompatible // indirect\n)\n"))
	cache := filepath.Join(dir, "cache")
	texts := map[string][]byte{
		"LICENSE":     []byte("Copyright\r\nPermission\r\n"),
		"LICENSE-MIT": []byte("Second license, no final newline"),
		"NOTICE.txt":  []byte("Notice\n"),
	}
	for name, data := range texts {
		writeFixture(t, filepath.Join(cache, name), data)
	}
	writeFixture(t, filepath.Join(cache, "README.md"), []byte("not a license"))
	goText := []byte("Go copyright\nGo license\n")
	writeFixture(t, filepath.Join(dir, "go", "LICENSE"), goText)
	dest := filepath.Join(dir, "notices")
	stale := filepath.Join(dest, "modules", "example.com", "removed@v1.0.0", "LICENSE")
	writeFixture(t, stale, []byte("obsolete"))
	other := filepath.Join(dest, "other", "adapted@commit", "LICENSE")
	writeFixture(t, other, []byte("keep"))
	seen := make(map[string]bool)
	toolchain := module.Version{Path: "golang.org/toolchain", Version: "v0.0.1-go1.26.1." + runtime.GOOS + "-" + runtime.GOARCH}
	resolve := func(mod module.Version) (string, error) {
		seen[mod.String()] = true
		if mod.Path == "golang.org/toolchain" {
			if mod != toolchain {
				t.Fatalf("requested toolchain %s, want %s", mod, toolchain)
			}
			return filepath.Join(dir, "go"), nil
		}
		return cache, nil
	}
	for range 2 {
		if err := generate(mod, dest, "go1.26.1", resolve); err != nil {
			t.Fatal(err)
		}
	}
	if !seen[toolchain.String()] {
		t.Fatal("did not resolve", toolchain)
	}
	for _, identity := range []string{"example.com/direct@v1.0.0", "example.com/indirect@v2.0.0+incompatible"} {
		if !seen[identity] {
			t.Fatal("did not resolve", identity)
		}
		for name, want := range texts {
			got, err := os.ReadFile(filepath.Join(dest, "modules", filepath.FromSlash(identity), name))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("%s/%s changed bytes: %q, %v", identity, name, got, err)
			}
		}
		if _, err := os.Stat(filepath.Join(dest, "modules", filepath.FromSlash(identity), "README.md")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("copied a non-license file", err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dest, "go", "LICENSE"))
	if err != nil || !bytes.Equal(got, goText) {
		t.Fatal("Go license changed bytes", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retained obsolete module text", err)
	}
	if got, err := os.ReadFile(other); err != nil || string(got) != "keep" {
		t.Fatal("changed other material's text", err)
	}
}

func TestGenerateFailurePreservesNotices(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "go.mod")
	writeFixture(t, mod, []byte("module example.com/app\nrequire example.com/library v1.0.0\n"))
	dest := filepath.Join(dir, "notices")
	existing := filepath.Join(dest, "modules", "example.com", "library@v1.0.0", "LICENSE")
	writeFixture(t, existing, []byte("keep"))
	cache := filepath.Join(dir, "cache")
	writeFixture(t, filepath.Join(cache, "NOTICE"), []byte("notice without license"))
	err := generate(mod, dest, "go1.26.1", func(module.Version) (string, error) { return cache, nil })
	if err == nil {
		t.Fatal("accepted a module without a license")
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "keep" {
		t.Fatal("failed generation changed existing text", err)
	}
}

func TestToolchainFailurePreservesNotices(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "go.mod")
	writeFixture(t, mod, []byte("module example.com/app\n"))
	dest := filepath.Join(dir, "notices")
	existing := filepath.Join(dest, "go", "LICENSE")
	writeFixture(t, existing, []byte("keep"))
	unavailable := errors.New("toolchain download unavailable")
	err := generate(mod, dest, "go1.26.1", func(module.Version) (string, error) { return "", unavailable })
	if !errors.Is(err, unavailable) {
		t.Fatal("did not return toolchain download error", err)
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "keep" {
		t.Fatal("failed toolchain download changed existing text", err)
	}
}
