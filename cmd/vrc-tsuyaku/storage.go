package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/motoki317/vrc-tsuyaku/internal/config"
)

func (a *app) openTarget(target string) error {
	a.operations.Lock()
	defer a.operations.Unlock()
	if target != "data" && target != "settings" {
		return errors.New("unknown open target")
	}
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return err
	}
	path := a.dir
	if target == "settings" {
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
			err = config.Save(a.dir, s)
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
