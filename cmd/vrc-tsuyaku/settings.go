package main

import (
	"errors"
	"os"
	"reflect"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/config"
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

// Callers hold operations so a poll cannot mistake an app write for an external edit.
func (a *app) writeSettings(s config.Settings) error {
	if err := config.Save(a.dir, s); err != nil {
		return err
	}
	stamp := a.statSettings()
	saved, err := config.Load(a.dir)
	// An external writer can replace the file between Save and Stat.
	if err == nil && reflect.DeepEqual(saved, s) && stamp == a.statSettings() {
		a.settingsFile = stamp
	}
	a.mu.Lock()
	a.settingsError = ""
	a.mu.Unlock()
	return nil
}

func (a *app) watchSettings() {
	a.settingsWG.Go(func() {
		ticker := time.NewTicker(250 * time.Millisecond)
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
		a.emit("", "error", message)
		return
	}
	a.applySettings(a.preserveControls(s), "reloaded")
}
