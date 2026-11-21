package main

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/asr"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/download"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

// downloadProgressInterval limits progress refreshes and leaves room for phrase events in the stream history.
const downloadProgressInterval = 250 * time.Millisecond

type artifactStatus string

const (
	// artifactMissing means the artifact's files are unavailable.
	artifactMissing artifactStatus = "missing"
	// artifactDownloading means a download for the artifact is in progress.
	artifactDownloading artifactStatus = "downloading"
	// artifactInstalled means the artifact's files are available to flows.
	artifactInstalled artifactStatus = "installed"
)

type artifactState struct {
	Artifact catalog.Artifact  `json:"artifact"`
	State    artifactStatus    `json:"state"`
	Progress download.Progress `json:"progress"`
	Error    string            `json:"error,omitempty"`
	Code     errcode.Code      `json:"code,omitempty"`
}

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
	inUse := state.State == artifactDownloading
	active := false
	for name, run := range a.flows {
		if run.status.active() {
			active = true
			spoken, _ := a.settings.Flows.Languages(name)
			required, _ := catalog.Required(spoken, a.settings.Translator)
			inUse = inUse || slices.Contains(required, id)
		}
	}
	a.mu.Unlock()
	if inUse {
		return fmt.Errorf("%w: %s", errArtifactInUse, id)
	}
	// Windows holds ONNX Runtime's DLL open for the life of the process.
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
	state.Code = ""
	if err == nil {
		state.State = artifactMissing
	} else {
		// RemoveAll can delete some files before an OS error. Do not reuse the remainder.
		state.State = artifactMissing
		state.Error = withDataDirHint(err).Error()
		state.Code = errcode.Of(err)
	}
	a.mu.Lock()
	a.artifacts[id] = state
	a.mu.Unlock()
	a.emit("", kindDownload, id)
	return err
}

// startDownload requires the caller to set accept only after the user accepts the artifact's license.
func (a *app) startDownload(id string, accept bool) error {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.reloadSettingsLocked()
	a.mu.Lock()
	if a.instance.State == instanceWaiting {
		a.mu.Unlock()
		return errInstancePending
	}
	state, ok := a.artifacts[id]
	s := cloneSettings(a.settings)
	settingsError := a.settingsError
	a.mu.Unlock()
	if settingsError != "" {
		return fmt.Errorf("save valid settings before downloading: %s", settingsError)
	}
	if !ok {
		return fmt.Errorf("unknown artifact %q", id)
	}
	if state.State != artifactMissing {
		return fmt.Errorf("artifact %s is %s", id, state.State)
	}
	if key := state.Artifact.AcceptanceKey; key != "" {
		if accept {
			s.LicensesAccepted[key] = time.Now().UTC().Format(time.RFC3339)
			if err := a.writeSettings(s); err != nil {
				return err
			}
			a.mu.Lock()
			a.settings = s
			a.mu.Unlock()
		}
		if s.LicensesAccepted[key] == "" {
			return download.ErrLicenseRequired
		}
	}
	if err := a.ctx.Err(); err != nil {
		return err
	}
	state.State = artifactDownloading
	state.Error = ""
	state.Code = ""
	state.Progress = download.Progress{}
	a.mu.Lock()
	a.artifacts[id] = state
	a.mu.Unlock()
	a.downloadsWG.Go(func() {
		last := time.Time{}
		err := a.downloadArtifact(a.ctx, id, runtime.GOOS, func(p download.Progress) {
			a.mu.Lock()
			state.Progress = p
			a.artifacts[id] = state
			a.mu.Unlock()
			if time.Since(last) > downloadProgressInterval {
				a.emit("", kindDownload, id)
				last = time.Now()
			}
		})
		a.operations.Lock()
		defer a.operations.Unlock()
		state.State = artifactInstalled
		if err != nil {
			state.Error = withDataDirHint(err).Error()
			state.Code = errcode.Of(err)
			if !errors.Is(err, download.ErrCleanup) {
				state.State = artifactMissing
			}
		}
		a.mu.Lock()
		a.artifacts[id] = state
		a.mu.Unlock()
		if err != nil {
			a.emitError("", err)
		}
		a.emit("", kindDownload, id)
		if state.State == artifactInstalled {
			a.startWaitingFlows()
		}
	})
	a.emit("", kindDownload, id)
	return nil
}

// Callers must hold operations so retries cannot overlap stop or settings changes.
func (a *app) startWaitingFlows() {
	a.reloadSettingsLocked()
	a.mu.Lock()
	s := cloneSettings(a.settings)
	var waiting []config.Flow
	if a.settingsError == "" && a.ctx.Err() == nil {
		for _, name := range config.FlowNames() {
			status := a.flows[name].status
			if s.Flows.Enabled(name) && !status.active() && status.Code == errcode.MissingDownloads && a.checkStart(name, s) == nil {
				waiting = append(waiting, name)
			}
		}
	}
	a.mu.Unlock()
	for _, name := range waiting {
		_ = a.start(name, s)
	}
}

func (a *app) artifactDir(id string) string {
	dir, _ := a.downloads.Directory(id, runtime.GOOS)
	return dir
}
