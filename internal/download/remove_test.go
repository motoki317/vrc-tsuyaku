package download

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

func TestRemoveConfinesSymlinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	marker := filepath.Join(outside, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "downloads", catalog.Haas)
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := m.Remove(catalog.Haas, "linux"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("artifact link remains", err)
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "keep" {
		t.Fatal("followed artifact link", err)
	}
	if err := os.Remove(filepath.Join(dir, "downloads")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "downloads")); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(catalog.Haas, "linux"); err == nil {
		t.Fatal("accepted downloads link outside data directory")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("removed outside file", err)
	}
	if err := m.Remove("../keep", "linux"); err == nil {
		t.Fatal("accepted path as catalog ID")
	}
	if err := os.Remove(filepath.Join(dir, "downloads")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "other")
	artifact := filepath.Join(alias, catalog.Haas)
	if err := os.MkdirAll(artifact, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alias, filepath.Join(dir, "downloads")); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(catalog.Haas, "linux"); err == nil {
		t.Fatal("accepted downloads link to another data subdirectory")
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatal("deleted artifact outside downloads", err)
	}
}
