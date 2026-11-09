// Package config reads and saves settings in the caller's data directory.
package config

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
)

// ErrInvalidSettings marks validation failures returned by Save.
var ErrInvalidSettings = errors.New("invalid settings")

// Settings is the complete settings.yaml document. Saving rewrites it and removes comments.
type Settings struct {
	Flows            Flows             `yaml:"flows"`
	Chatbox          Chatbox           `yaml:"chatbox"`
	LicensesAccepted map[string]string `yaml:"licenses_accepted"`
}

type Flows struct {
	Self  SelfFlow  `yaml:"self"`
	World WorldFlow `yaml:"world"`
}

type SelfFlow struct {
	Enabled    bool   `yaml:"enabled"`
	Microphone string `yaml:"microphone"`
}

type WorldFlow struct {
	Enabled bool `yaml:"enabled"`
}

type Chatbox struct {
	Address string `yaml:"address"`
}

// Defaults leaves both flows stopped and uses the local VRChat address.
func Defaults() Settings {
	return Settings{
		Chatbox:          Chatbox{Address: "127.0.0.1:9000"},
		LicensesAccepted: make(map[string]string),
	}
}

// Validate checks the chatbox destination and RFC3339 license acceptance timestamps.
func (s Settings) Validate() error {
	if _, err := chatbox.ParseAddress(s.Chatbox.Address); err != nil {
		return err
	}
	for key, value := range s.LicensesAccepted {
		if key == "" {
			return errors.New("license acceptance key must not be empty")
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("licenses_accepted[%q]: %w", key, err)
		}
	}
	return nil
}

// Load uses defaults for a missing file or field. Unknown keys fail with their YAML line number.
// It does not create the data directory.
func Load(dataDir string) (Settings, error) {
	s := Defaults()
	root, err := os.OpenRoot(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, settingsError(dataDir, err)
	}
	defer root.Close()
	b, err := root.ReadFile("settings.yaml")
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, settingsError(dataDir, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err = dec.Decode(&s); err != nil && !errors.Is(err, io.EOF) {
		return s, settingsError(dataDir, err)
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("expected one YAML document")
		}
		return s, settingsError(dataDir, err)
	}
	if s.LicensesAccepted == nil {
		s.LicensesAccepted = make(map[string]string)
	}
	if err = s.Validate(); err != nil {
		return s, settingsError(dataDir, err)
	}
	return s, nil
}

// Save validates settings and atomically replaces settings.yaml.
// On failure the old settings remain in place.
func Save(dataDir string, s Settings) error {
	if err := s.Validate(); err != nil {
		return settingsError(dataDir, fmt.Errorf("%w: %w", ErrInvalidSettings, err))
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		return settingsError(dataDir, err)
	}
	if err = os.MkdirAll(dataDir, 0o755); err != nil {
		return settingsError(dataDir, err)
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return settingsError(dataDir, err)
	}
	defer root.Close()
	tmp := ".settings-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return settingsError(dataDir, err)
	}
	defer func() { _ = root.Remove(tmp) }()
	_, writeErr := f.Write(b)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return settingsError(dataDir, err)
	}
	if err = root.Rename(tmp, "settings.yaml"); err != nil {
		return settingsError(dataDir, err)
	}
	return nil
}

func settingsError(dir string, err error) error {
	return fmt.Errorf("settings %s: %w", filepath.Join(dir, "settings.yaml"), err)
}
