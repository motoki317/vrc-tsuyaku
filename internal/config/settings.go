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

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

// ErrInvalidSettings marks validation failures returned by Save.
var ErrInvalidSettings = errors.New("invalid settings")

// Settings is the complete settings.yaml document. Saving rewrites it and removes comments.
type Settings struct {
	UI               UI                `yaml:"ui"`
	Translator       string            `yaml:"translator"`
	Flows            Flows             `yaml:"flows"`
	Chatbox          Chatbox           `yaml:"chatbox"`
	LicensesAccepted map[string]string `yaml:"licenses_accepted"`
}

type UI struct {
	Language string `yaml:"language"`
}

type Flows struct {
	Self  SelfFlow  `yaml:"self"`
	World WorldFlow `yaml:"world"`
}

type SelfFlow struct {
	Enabled    bool   `yaml:"enabled"`
	Microphone string `yaml:"microphone"`
	Spoken     string `yaml:"spoken"`
	Target     string `yaml:"target"`
}

type WorldFlow struct {
	Enabled bool   `yaml:"enabled"`
	Spoken  string `yaml:"spoken"`
	Target  string `yaml:"target"`
}

func (f Flows) Languages(name string) (spoken, target string) {
	if name == "self" {
		return f.Self.Spoken, f.Self.Target
	}
	return f.World.Spoken, f.World.Target
}

type Chatbox struct {
	Address  string `yaml:"address"`
	Template string `yaml:"template"`
}

// Defaults leaves both flows stopped and uses the local VRChat address.
func Defaults() Settings {
	return Settings{
		Translator: catalog.DefaultTranslator(),
		Flows: Flows{
			Self:  SelfFlow{Spoken: catalog.Japanese, Target: catalog.English},
			World: WorldFlow{Spoken: catalog.English, Target: catalog.Japanese},
		},
		Chatbox:          Chatbox{Address: "127.0.0.1:9000", Template: chatbox.DefaultTemplate},
		LicensesAccepted: make(map[string]string),
	}
}

// Validate rejects unsupported languages, invalid destinations and invalid acceptance timestamps.
func (s Settings) Validate() error {
	if _, ok := catalog.LookupTranslator(s.Translator); !ok {
		return fmt.Errorf("translator: unsupported translator %q", s.Translator)
	}
	if s.UI.Language != "" && s.UI.Language != "en" && s.UI.Language != "ja" {
		return fmt.Errorf("ui.language: unsupported language %q", s.UI.Language)
	}
	for _, name := range []string{"self", "world"} {
		spoken, target := s.Flows.Languages(name)
		language, ok := catalog.Spoken(spoken)
		if !ok || (name == "self" && language.Auto) {
			return fmt.Errorf("flows.%s.spoken: unsupported language %q", name, spoken)
		}
		if !catalog.IsTarget(target) {
			return fmt.Errorf("flows.%s.target: unsupported language %q", name, target)
		}
	}
	if _, err := chatbox.ParseAddress(s.Chatbox.Address); err != nil {
		return err
	}
	if err := chatbox.ValidateTemplate(s.Chatbox.Template); err != nil {
		return fmt.Errorf("chatbox.template: %w", err)
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
func Save(dataDir string, s Settings) (err error) {
	if err := s.Validate(); err != nil {
		return settingsError(dataDir, fmt.Errorf("%w: %w", ErrInvalidSettings, err))
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		return settingsError(dataDir, err)
	}
	defer func() { err = errcode.With(errcode.DiskWrite, err) }()
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
