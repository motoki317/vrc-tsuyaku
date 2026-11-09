package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestLoadDefaultsAndPartial(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	got, err := Load(dir)
	if err != nil || !reflect.DeepEqual(got, Defaults()) {
		t.Fatalf("missing: %+v, %v", got, err)
	}
	if got.Flows.Self.Enabled || got.Flows.World.Enabled {
		t.Fatal("default settings must not start capture")
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("Load created directory: %v", err)
	}
	if err = os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"", "flows:\n  self:\n    microphone: endpoint\n  world:\n    enabled: true\n"} {
		if err = os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err = Load(dir)
		if err != nil || got.Flows.Self.Enabled || got.Chatbox.Address != "127.0.0.1:9000" {
			t.Fatalf("partial: %+v, %v", got, err)
		}
		if body != "" && (got.Flows.Self.Microphone != "endpoint" || !got.Flows.World.Enabled) {
			t.Fatalf("fields missing: %+v", got)
		}
	}
}

func TestInvalidSettings(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"flows:\n  self:\n    enable: true\n", "line 3"},
		{"chatbox: {address: localhost:9000}", "numeric IP:port"},
		{"chatbox: {address: '127.0.0.1:0'}", "nonzero"},
		{"chatbox: {address: ''}", "numeric IP:port"},
		{"licenses_accepted: {license: yesterday}", "licenses_accepted"},
		{"flows: {}\n---\nflows: {}", "one YAML document"},
		{"flows: {self: {enabled: true, enabled: false}}", "already defined"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "-data-dir") {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSaveReplaceAndFailure(t *testing.T) {
	dir := t.TempDir()
	s := Defaults()
	s.Flows.Self.Enabled = true
	s.Chatbox.Address = "[::1]:9000"
	s.LicensesAccepted["example@revision"] = "2026-11-09T01:02:03Z"
	for range 2 {
		if err := Save(dir, s); err != nil {
			t.Fatal(err)
		}
		got, err := Load(dir)
		if err != nil || !reflect.DeepEqual(got, s) {
			t.Fatalf("round trip: %+v, %v", got, err)
		}
	}
	bad := s
	bad.Chatbox.Address = "host:9"
	badLicense := Defaults()
	badLicense.LicensesAccepted["example@revision"] = "invalid"
	for _, invalid := range []Settings{bad, badLicense} {
		if err := Save(dir, invalid); !errors.Is(err, ErrInvalidSettings) || !strings.Contains(err.Error(), filepath.Join(dir, "settings.yaml")) || strings.Contains(err.Error(), "-data-dir") {
			t.Fatalf("invalid save error = %v", err)
		}
	}
	got, err := Load(dir)
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("failed save changed settings: %+v, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "settings.yaml" {
		t.Fatalf("temporary files: %v, %v", entries, err)
	}
}

func TestSaveRenameFailureCleansTemp(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "settings.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, Defaults()); err == nil || errors.Is(err, ErrInvalidSettings) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("got %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files: %v, %v", entries, err)
	}
}

func TestSettingsSymlinkEscape(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "settings.yaml")
	if err := os.WriteFile(target, []byte("chatbox: {address: '[::1]:9'}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "settings.yaml")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("read outside data directory")
	}
	if err := Save(dir, Defaults()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "chatbox: {address: '[::1]:9'}" {
		t.Fatalf("outside file changed: %q, %v", b, err)
	}
}

func TestSettingsErrorsKeepPathWithoutFlags(t *testing.T) {
	for _, cause := range []error{os.ErrPermission, syscall.EROFS} {
		err := settingsError("data", &os.PathError{Op: "open", Path: "settings.yaml", Err: cause})
		if !errors.Is(err, cause) || strings.Contains(err.Error(), "-data-dir") || !strings.Contains(err.Error(), "settings.yaml") {
			t.Fatalf("package error contains a flag or loses its path: %v", err)
		}
	}
}
