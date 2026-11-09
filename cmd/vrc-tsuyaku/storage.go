package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/motoki317/vrc-tsuyaku/internal/asr"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/download"
)

var errArtifactInUse = errors.New("artifact is in use")

func (a *app) deleteDownload(id string) error {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.mu.Lock()
	state, ok := a.artifacts[id]
	if !ok {
		a.mu.Unlock()
		return fmt.Errorf("unknown artifact %q", id)
	}
	inUse := state.State == "downloading"
	active := false
	for name, run := range a.flows {
		if run.status.active() {
			active = true
			spoken, _ := a.settings.Flows.Languages(name)
			required, _ := catalog.Required(spoken)
			inUse = inUse || slices.Contains(required, id)
		}
	}
	a.mu.Unlock()
	if inUse {
		return fmt.Errorf("%w: %s", errArtifactInUse, id)
	}
	if runtime.GOOS == "windows" && id == catalog.ONNXRuntime && asr.RuntimeResident() {
		return errors.New("restart the app before deleting ONNX Runtime")
	}
	// Idle sessions and the server can retain file handles after a flow stops.
	if !active {
		if a.engine != nil {
			a.engine.Close()
			a.engine = nil
		}
		if a.translator != nil {
			if err := a.translator.Close(); err != nil {
				return err
			}
			a.translator = nil
		}
	}
	err := a.downloads.Remove(id, runtime.GOOS)
	state.Progress = download.Progress{}
	state.Error = ""
	if err == nil {
		state.State = "missing"
	} else {
		// RemoveAll can delete some files before an OS error. Do not reuse the remainder.
		state.State = "missing"
		state.Error = withDataDirHint(err).Error()
	}
	a.mu.Lock()
	a.artifacts[id] = state
	a.mu.Unlock()
	a.emit("", "download", id)
	return err
}

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
