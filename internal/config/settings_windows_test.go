package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSaveWithOpenFile(t *testing.T) {
	dir := t.TempDir()
	old := Defaults()
	if err := Save(dir, old); err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(filepath.Join(dir, "settings.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if handle != windows.InvalidHandle {
			_ = windows.CloseHandle(handle)
		}
	}()
	next := Defaults()
	next.Flows.Self.Enabled = false
	if err = Save(dir, next); err == nil {
		t.Fatal("replaced file held without delete sharing")
	}
	got, err := Load(dir)
	if err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("old settings lost: %+v %v", got, err)
	}
	if err = windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	handle = windows.InvalidHandle
	if err = Save(dir, next); err != nil {
		t.Fatal(err)
	}
}
