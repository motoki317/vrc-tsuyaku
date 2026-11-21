package main

import (
	"errors"
	"maps"
	"os"
	"reflect"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
)

// settingsPollInterval keeps external edits responsive without a file watcher for each OS.
const settingsPollInterval = 250 * time.Millisecond

type settingsText string

const (
	// settingsSaved means the app saved the settings.
	settingsSaved settingsText = "saved"
	// settingsReloaded means the app loaded an external edit.
	settingsReloaded settingsText = "reloaded"
	// settingsChanged means the app stopped a flow without saving because settingsError is set.
	settingsChanged settingsText = "changed"
)

type settingsFileState struct {
	modified time.Time
	size     int64
	error    string
}

func (a *app) statSettings() settingsFileState {
	root, err := os.OpenRoot(a.dir)
	if errors.Is(err, os.ErrNotExist) {
		return settingsFileState{}
	}
	if err != nil {
		return settingsFileState{error: err.Error()}
	}
	defer root.Close()
	info, err := root.Stat("settings.yaml")
	if errors.Is(err, os.ErrNotExist) {
		return settingsFileState{}
	}
	if err != nil {
		return settingsFileState{error: err.Error()}
	}
	return settingsFileState{modified: info.ModTime(), size: info.Size()}
}

// Callers must hold operations so a poll cannot mistake an app write for an external edit.
func (a *app) writeSettings(s config.Settings) error {
	if err := config.Save(a.dir, s); err != nil {
		return err
	}
	stamp := a.statSettings()
	saved, err := config.Load(a.dir)
	// An external writer can replace the file between Save and Stat.
	if err == nil && reflect.DeepEqual(saved, cloneSettings(s)) && stamp == a.statSettings() {
		a.settingsFile = stamp
	}
	a.mu.Lock()
	a.settingsError = ""
	a.mu.Unlock()
	return nil
}

func (a *app) watchSettings() {
	a.settingsWG.Go(func() {
		ticker := time.NewTicker(settingsPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				a.operations.Lock()
				a.reloadSettingsLocked()
				a.operations.Unlock()
			}
		}
	})
}

func (a *app) reloadSettingsLocked() {
	if a.ctx.Err() != nil {
		return
	}
	stamp := a.statSettings()
	if stamp == a.settingsFile {
		return
	}
	s, err := config.Load(a.dir)
	if stamp != a.statSettings() {
		return
	}
	a.settingsFile = stamp
	if err != nil {
		message := withDataDirHint(err).Error()
		a.mu.Lock()
		a.settingsError = message
		a.mu.Unlock()
		a.emit("", pipeline.KindError, message)
		return
	}
	a.applySettings(a.preserveControls(s), settingsReloaded)
}

func cloneSettings(s config.Settings) config.Settings {
	s.Glossary = append([]glossary.Entry{}, s.Glossary...)
	s.LicensesAccepted = maps.Clone(s.LicensesAccepted)
	if s.LicensesAccepted == nil {
		s.LicensesAccepted = make(map[string]string)
	}
	return s
}

func (a *app) saveSettings(s config.Settings) error {
	a.operations.Lock()
	defer a.operations.Unlock()
	if a.statSettings() != a.settingsFile {
		a.reloadSettingsLocked()
		return errcode.With(errcode.SettingsChanged, errors.New("settings changed on disk: reload and retry"))
	}
	s = a.preserveControls(s)
	if err := a.writeSettings(s); err != nil {
		return err
	}
	a.applySettings(s, settingsSaved)
	return nil
}

func (a *app) preserveControls(s config.Settings) config.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	s.LicensesAccepted = maps.Clone(a.settings.LicensesAccepted)
	for _, name := range config.FlowNames() {
		s.Flows.SetEnabled(name, a.settings.Flows.Enabled(name))
	}
	return s
}

func (a *app) applySettings(s config.Settings, text settingsText) {
	a.mu.Lock()
	old := a.settings
	translatorChanged := old.Translator != s.Translator
	var restart []config.Flow
	for _, name := range config.FlowNames() {
		oldSpoken, _ := old.Flows.Languages(name)
		newSpoken, _ := s.Flows.Languages(name)
		oldLanguage, _ := catalog.Spoken(oldSpoken)
		newLanguage, _ := catalog.Spoken(newSpoken)
		requirementsChanged := translatorChanged || oldLanguage.Recognizer != newLanguage.Recognizer
		changed := requirementsChanged
		if name == config.Self {
			changed = changed || old.Flows.Self.Microphone != s.Flows.Self.Microphone || old.Chatbox.Address != s.Chatbox.Address
		}
		status := a.flows[name].status
		waiting := requirementsChanged && s.Flows.Enabled(name) && status.Code == errcode.MissingDownloads
		if changed && (status.active() || waiting) {
			restart = append(restart, name)
		}
	}
	a.mu.Unlock()
	// Finish old phrases before publishing settings that need another model.
	for _, name := range restart {
		a.stop(name)
	}
	if translatorChanged && a.translator != nil {
		if err := a.translator.Close(); err != nil {
			a.emitError("", err)
		}
		a.translator = nil
	}
	a.mu.Lock()
	a.settings = cloneSettings(s)
	a.settingsError = ""
	a.mu.Unlock()
	for _, name := range restart {
		_ = a.start(name, s)
	}
	a.emit("", kindSettings, string(text))
}
