package download

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnwritableExistingDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses write permissions")
	}
	for _, where := range []string{"data", "downloads"} {
		t.Run(where, func(t *testing.T) {
			m, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := m.dataDir
			if where == "downloads" {
				dir = filepath.Join(dir, "downloads")
				if err = os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.Chmod(dir, 0o555); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chmod(dir, 0o755) }()
			err = m.install(context.Background(), fixture([]byte("bytes")), nil)
			if err == nil || !strings.Contains(err.Error(), m.dataDir) || strings.Contains(err.Error(), "-data-dir") {
				t.Fatalf("missing directory/remedy: %v", err)
			}
		})
	}
}

func TestCleanupFailureAfterPublication(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	body := []byte("new bytes")
	m, s := testManager(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
	a := fixture(body)
	a.Sources[0].URL = s.URL
	dir := filepath.Join(m.dataDir, "downloads", a.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		entries, _ := os.ReadDir(filepath.Dir(dir))
		for _, e := range entries {
			_ = os.Chmod(filepath.Join(filepath.Dir(dir), e.Name()), 0o755)
		}
	}()
	installed := false
	err := m.install(context.Background(), a, func(p Progress) { installed = installed || p.Phase == "installed" })
	if !errors.Is(err, ErrCleanup) || !strings.Contains(err.Error(), m.dataDir) || !installed || strings.Contains(err.Error(), "-data-dir") {
		t.Fatalf("committed state not reported: %v, event %v", err, installed)
	}
	if ok, err := m.check(context.Background(), a); !ok || err != nil {
		t.Fatalf("new install: %v %v", ok, err)
	}
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected complete new installation and old backup, got %v", entries)
	}
}

func TestInstalledExecutablePermission(t *testing.T) {
	body := []byte("executable bytes")
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := fixture(body)
	a.Sources[0].Files[0].Executable = true
	dir := filepath.Join(m.dataDir, "downloads", a.ID)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "model")
	if err = os.WriteFile(file, body, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode os.FileMode
		want bool
	}{{0o644, false}, {0o755, true}, {0o644, false}} {
		if err = os.Chmod(file, tc.mode); err != nil {
			t.Fatal(err)
		}
		if got, err := m.check(context.Background(), a); err != nil || got != tc.want {
			t.Fatalf("mode %o: installed %v, want %v: %v", tc.mode, got, tc.want, err)
		}
	}
	a.Sources[0].Files[0].Executable = false
	if ok, err := m.check(context.Background(), a); !ok || err != nil {
		t.Fatalf("non-executable data: installed %v: %v", ok, err)
	}
}
