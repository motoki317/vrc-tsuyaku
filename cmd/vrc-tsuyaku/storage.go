package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type openTargetName string

const (
	// openData selects the data directory.
	openData openTargetName = "data"
	// openSettings selects settings.yaml in the data directory.
	openSettings openTargetName = "settings"
)

var errUnknownOpenTarget = errors.New("unknown open target")

func (a *app) openTarget(target openTargetName) error {
	if target != openData && target != openSettings {
		return errUnknownOpenTarget
	}
	a.operations.Lock()
	defer a.operations.Unlock()
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return err
	}
	path := a.dir
	if target == openSettings {
		root, err := os.OpenRoot(a.dir)
		if err != nil {
			return err
		}
		_, err = root.Stat("settings.yaml")
		_ = root.Close()
		if errors.Is(err, os.ErrNotExist) {
			a.mu.Lock()
			s := cloneSettings(a.settings)
			a.mu.Unlock()
			err = a.writeSettings(s)
		}
		if err != nil {
			return err
		}
		path = filepath.Join(a.dir, "settings.yaml")
	}
	return a.open(path)
}

func fileCommand(goos, path string) *exec.Cmd {
	if goos == "windows" {
		return exec.Command("explorer.exe", path)
	}
	return exec.Command("xdg-open", path)
}

func openFile(path string) error {
	cmd := fileCommand(runtime.GOOS, path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
