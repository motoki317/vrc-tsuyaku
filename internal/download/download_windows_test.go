package download

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestInstallWithOpenDirectory(t *testing.T) {
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
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if handle != windows.InvalidHandle {
			_ = windows.CloseHandle(handle)
		}
	}()
	if err = m.install(context.Background(), a, nil); err == nil {
		t.Fatal("renamed open directory without delete sharing")
	}
	if string(installedBytes(t, m)) != "old" {
		t.Fatal("old installation changed")
	}
	assertClean(t, m)
	if err = windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	handle = windows.InvalidHandle
	if err = m.install(context.Background(), a, nil); err != nil {
		t.Fatal(err)
	}
	assertClean(t, m)
}

func TestInstallWithOpenChild(t *testing.T) {
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
	path, err := windows.UTF16PtrFromString(filepath.Join(dir, "model"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	installed := false
	err = m.install(context.Background(), a, func(p Progress) { installed = installed || p.Phase == "installed" })
	if err == nil || errors.Is(err, ErrCleanup) || installed {
		t.Fatalf("open child should prevent publication: %v, event %v", err, installed)
	}
	if string(installedBytes(t, m)) != "old" {
		t.Fatal("old installation changed")
	}
	assertClean(t, m)
}
