package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/asr"
	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/download"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

type artifactState struct {
	Artifact catalog.Artifact  `json:"artifact"`
	State    string            `json:"state"`
	Progress download.Progress `json:"progress"`
	Error    string            `json:"error,omitempty"`
	Code     errcode.Code      `json:"code,omitempty"`
}

type flowState struct {
	State string       `json:"state"`
	Error string       `json:"error,omitempty"`
	Code  errcode.Code `json:"code,omitempty"`
}

func flowError(err error) flowState {
	return flowState{State: "error", Error: withDataDirHint(err).Error(), Code: errcode.Of(err)}
}

func (s flowState) active() bool {
	return s.State == "starting" || s.State == "running"
}

type flowRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	status flowState
}

type app struct {
	ctx           context.Context
	cancel        context.CancelFunc
	dir, token    string
	downloads     *download.Manager
	mu            sync.Mutex
	settings      config.Settings
	settingsError string
	settingsFile  settingsFileState
	artifacts     map[string]artifactState
	flows         map[string]*flowRun
	events        events
	// operations serializes settings, flow and storage changes, including close.
	// Callers hold it while calling start or stop.
	operations       sync.Mutex
	settingsWG       sync.WaitGroup
	downloadsWG      sync.WaitGroup
	modelTurn        chan struct{}
	engine           *asr.Engine
	translator       *translate.Supervisor
	queue            *chatbox.Queue
	queueAddress     string
	source           func(flowName, microphoneID string) audio.Source
	prepare          func(ctx context.Context, flowName string, settings config.Settings) (pipeline.Stages, error)
	downloadArtifact func(context.Context, string, string, func(download.Progress)) error
	open             func(string) error
}

func newApp(ctx context.Context, dir string) (*app, error) {
	manager, err := download.New(dir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	a := &app{ctx: ctx, cancel: cancel, dir: dir, token: rand.Text(), downloads: manager,
		artifacts: make(map[string]artifactState), flows: make(map[string]*flowRun), modelTurn: make(chan struct{}, 1),
		source: flowSource, open: openFile,
	}
	a.prepare = a.prepareModels
	a.downloadArtifact = manager.Download
	a.settingsFile = a.statSettings()
	settings, loadErr := config.Load(dir)
	if loadErr != nil {
		settings = config.Defaults()
		a.settingsError = withDataDirHint(loadErr).Error()
		a.emit("", "error", a.settingsError)
	}
	a.settings = settings
	artifacts, err := catalog.List(runtime.GOOS)
	if err != nil {
		cancel()
		return nil, err
	}
	for _, artifact := range artifacts {
		installed, err := manager.CheckInstalled(ctx, artifact.ID, runtime.GOOS)
		state := artifactState{Artifact: artifact, State: "missing"}
		if installed {
			state.State = "installed"
		}
		if err != nil {
			state.Error = withDataDirHint(err).Error()
			state.Code = errcode.Of(err)
			a.emitError("", err)
		}
		a.artifacts[artifact.ID] = state
	}
	for _, name := range []string{"self", "world"} {
		a.flows[name] = &flowRun{status: flowState{State: "stopped"}}
	}
	return a, nil
}

func flowSource(name, microphoneID string) audio.Source {
	if name == "world" {
		return audio.Application("VRChat.exe", "VRChat")
	}
	return audio.Microphone(microphoneID)
}

func (a *app) emit(flow, kind, text string) {
	a.events.publish(flow, pipeline.Event{Kind: kind, Text: text})
}

func (a *app) emitError(flow string, err error) {
	a.events.publish(flow, pipeline.Event{Kind: "error", Code: errcode.Of(err), Text: withDataDirHint(err).Error()})
}

func cloneSettings(s config.Settings) config.Settings {
	s.LicensesAccepted = maps.Clone(s.LicensesAccepted)
	if s.LicensesAccepted == nil {
		s.LicensesAccepted = make(map[string]string)
	}
	return s
}

func (a *app) snapshot() any {
	a.mu.Lock()
	defer a.mu.Unlock()
	flows := make(map[string]flowState)
	requirements := make(map[string][]string)
	for name, run := range a.flows {
		flows[name] = run.status
		spoken, _ := a.settings.Flows.Languages(name)
		requirements[name], _ = catalog.Required(spoken)
	}
	return struct {
		Settings      config.Settings          `json:"settings"`
		SettingsError string                   `json:"settingsError,omitempty"`
		Artifacts     map[string]artifactState `json:"artifacts"`
		Flows         map[string]flowState     `json:"flows"`
		Required      map[string][]string      `json:"required"`
		Languages     catalog.LanguageCatalog  `json:"languages"`
	}{cloneSettings(a.settings), a.settingsError, maps.Clone(a.artifacts), flows, requirements, catalog.Languages()}
}

func (a *app) startEnabled() {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.mu.Lock()
	s := a.settings
	valid := a.settingsError == ""
	a.mu.Unlock()
	if !valid {
		return
	}
	if s.Flows.Self.Enabled {
		_ = a.start("self", s)
	}
	if s.Flows.World.Enabled {
		_ = a.start("world", s)
	}
}

// Model initialization runs outside operations so stop can cancel a pending start.
func (a *app) start(name string, s config.Settings) error {
	a.mu.Lock()
	if a.flows[name].status.active() {
		a.mu.Unlock()
		return nil
	}
	if err := a.checkStart(name, s); err != nil {
		a.mu.Unlock()
		a.refuseStart(name, err)
		return err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	run := &flowRun{cancel: cancel, done: make(chan struct{}), status: flowState{State: "starting"}}
	a.flows[name] = run
	a.mu.Unlock()
	a.emit(name, "state", "starting")
	go func() {
		defer close(run.done)
		defer cancel()
		stages, err := a.prepare(ctx, name, s)
		if err == nil && ctx.Err() == nil {
			stages.Settings = func() pipeline.PhraseSettings {
				a.mu.Lock()
				defer a.mu.Unlock()
				source, target := a.settings.Flows.Languages(name)
				return pipeline.PhraseSettings{SourceLang: source, TargetLang: target, Template: a.settings.Chatbox.Template}
			}
			a.mu.Lock()
			run.status = flowState{State: "running"}
			a.mu.Unlock()
			a.emit(name, "state", "running")
			stages.Emit = func(e pipeline.Event) { a.events.publish(name, e) }
			err = a.runStages(ctx, stages)
		}
		a.mu.Lock()
		run.status = flowState{State: "stopped"}
		if err != nil && ctx.Err() == nil {
			run.status = flowError(err)
		}
		state := run.status
		if ctx.Err() == nil {
			if state.Error != "" {
				a.events.publish(name, pipeline.Event{Kind: "error", Code: state.Code, Text: state.Error})
			} else {
				a.emit(name, "state", state.State)
			}
		}
		a.mu.Unlock()
	}()
	return nil
}

// The caller holds mu while checking artifact state.
func (a *app) checkStart(name string, s config.Settings) error {
	if err := a.ctx.Err(); err != nil {
		return err
	}
	var missing []string
	spoken, _ := s.Flows.Languages(name)
	required, err := catalog.Required(spoken)
	if err != nil {
		return err
	}
	for _, id := range required {
		if a.artifacts[id].State != "installed" {
			missing = append(missing, a.artifacts[id].Artifact.Name)
		}
	}
	if len(missing) > 0 {
		return errcode.With(errcode.MissingDownloads, fmt.Errorf("missing downloads: %s", strings.Join(missing, ", ")))
	}
	return nil
}

func (a *app) refuseStart(name string, err error) {
	a.mu.Lock()
	if !a.flows[name].status.active() {
		a.flows[name].status = flowError(err)
	}
	a.mu.Unlock()
	a.events.publishEvent(event{Flow: name, Operation: "start", Event: pipeline.Event{Kind: "error", Code: errcode.Of(err), Text: err.Error()}})
}

func (a *app) stop(name string) {
	a.mu.Lock()
	run := a.flows[name]
	active := run.status.active()
	if run.cancel != nil {
		run.cancel()
	}
	a.mu.Unlock()
	if run.cancel != nil {
		<-run.done
	}
	a.mu.Lock()
	run.status = flowState{State: "stopped"}
	a.mu.Unlock()
	if active {
		a.emit(name, "state", "stopped")
	}
}

func (a *app) saveSettings(s config.Settings) error {
	a.operations.Lock()
	defer a.operations.Unlock()
	s = a.preserveControls(s)
	if err := a.writeSettings(s); err != nil {
		return err
	}
	a.applySettings(s, "saved")
	return nil
}

func (a *app) preserveControls(s config.Settings) config.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	s.LicensesAccepted = maps.Clone(a.settings.LicensesAccepted)
	s.Flows.Self.Enabled = a.settings.Flows.Self.Enabled
	s.Flows.World.Enabled = a.settings.Flows.World.Enabled
	return s
}

func (a *app) applySettings(s config.Settings, event string) {
	a.mu.Lock()
	old := a.settings
	var restart []string
	for _, name := range []string{"self", "world"} {
		oldSpoken, _ := old.Flows.Languages(name)
		newSpoken, _ := s.Flows.Languages(name)
		oldLanguage, _ := catalog.Spoken(oldSpoken)
		newLanguage, _ := catalog.Spoken(newSpoken)
		changed := oldLanguage.Recognizer != newLanguage.Recognizer
		if name == "self" {
			changed = changed || old.Flows.Self.Microphone != s.Flows.Self.Microphone || old.Chatbox.Address != s.Chatbox.Address
		}
		if changed && a.flows[name].status.active() {
			restart = append(restart, name)
		}
	}
	a.mu.Unlock()
	// Finish old recognition before publishing a language that needs another model.
	for _, name := range restart {
		a.stop(name)
	}
	a.mu.Lock()
	a.settings = cloneSettings(s)
	a.settingsError = ""
	a.mu.Unlock()
	for _, name := range restart {
		_ = a.start(name, s)
	}
	a.emit("", "settings", event)
}

func (a *app) changeFlow(name, action string) error {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.reloadSettingsLocked()
	a.mu.Lock()
	s := cloneSettings(a.settings)
	settingsError := a.settingsError
	var startErr error
	if action == "start" {
		startErr = a.checkStart(name, s)
	}
	a.mu.Unlock()
	if settingsError != "" && action != "stop" {
		return fmt.Errorf("save valid settings before changing a flow: %s", settingsError)
	}
	if startErr != nil {
		a.refuseStart(name, startErr)
		return startErr
	}
	if name == "self" {
		s.Flows.Self.Enabled = action == "start"
	} else {
		s.Flows.World.Enabled = action == "start"
	}
	if settingsError == "" {
		if err := a.writeSettings(s); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	text := "saved"
	if settingsError != "" {
		text = "changed"
	}
	a.emit("", "settings", text)
	if action == "stop" {
		a.stop(name)
		return nil
	}
	return a.start(name, s)
}

func (a *app) startDownload(id string, accept bool) error {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.mu.Lock()
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
	if state.State != "missing" {
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
	state.State = "downloading"
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
			if time.Since(last) > 250*time.Millisecond {
				a.emit("", "download", id)
				last = time.Now()
			}
		})
		a.operations.Lock()
		defer a.operations.Unlock()
		state.State = "installed"
		if err != nil {
			state.Error = withDataDirHint(err).Error()
			state.Code = errcode.Of(err)
			if !errors.Is(err, download.ErrCleanup) {
				state.State = "missing"
			}
		}
		a.mu.Lock()
		a.artifacts[id] = state
		a.mu.Unlock()
		if err != nil {
			a.emitError("", err)
		}
		a.emit("", "download", id)
		if state.State == "installed" {
			a.startWaitingFlows()
		}
	})
	a.emit("", "download", id)
	return nil
}

// Callers hold operations to serialize retries with stop and settings changes.
func (a *app) startWaitingFlows() {
	a.reloadSettingsLocked()
	a.mu.Lock()
	s := cloneSettings(a.settings)
	var waiting []string
	if a.settingsError == "" && a.ctx.Err() == nil {
		for _, name := range []string{"self", "world"} {
			enabled := s.Flows.Self.Enabled
			if name == "world" {
				enabled = s.Flows.World.Enabled
			}
			status := a.flows[name].status
			if enabled && !status.active() && status.Code == errcode.MissingDownloads && a.checkStart(name, s) == nil {
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

func (a *app) prepareModels(ctx context.Context, name string, s config.Settings) (pipeline.Stages, error) {
	select {
	case a.modelTurn <- struct{}{}:
	case <-ctx.Done():
		return pipeline.Stages{}, ctx.Err()
	}
	defer func() { <-a.modelTurn }()
	if err := ctx.Err(); err != nil {
		return pipeline.Stages{}, err
	}
	var err error
	if a.engine == nil {
		a.engine, err = asr.NewEngine(catalog.ORTLibrary(a.artifactDir(catalog.ONNXRuntime)))
		if err != nil {
			return pipeline.Stages{}, err
		}
	}
	silero, err := a.engine.LoadSilero(catalog.SileroModel(a.artifactDir(catalog.Silero)))
	if err != nil {
		return pipeline.Stages{}, err
	}
	var recognizer asr.Recognizer
	spoken, _ := s.Flows.Languages(name)
	language, ok := catalog.Spoken(spoken)
	if !ok {
		return pipeline.Stages{}, fmt.Errorf("unsupported spoken language %q", spoken)
	}
	switch language.Recognizer {
	case catalog.Haas:
		recognizer, err = a.engine.LoadHaas(catalog.HaasBundle(a.artifactDir(catalog.Haas), true))
	case catalog.Parakeet:
		recognizer, err = a.engine.LoadParakeet(catalog.ParakeetBundle(catalog.Parakeet, a.artifactDir(catalog.Parakeet)))
	default:
		return pipeline.Stages{}, fmt.Errorf("unsupported recognizer %q", language.Recognizer)
	}
	if err != nil {
		return pipeline.Stages{}, err
	}
	if a.translator == nil {
		a.translator, err = translate.New(catalog.LlamaServer(a.artifactDir(catalog.LlamaCPP)), catalog.PLaMoModel(a.artifactDir(catalog.PLaMo)))
		if err != nil {
			return pipeline.Stages{}, err
		}
	}
	if err = a.translator.Start(ctx); err != nil {
		return pipeline.Stages{}, err
	}
	stages := pipeline.Stages{Source: a.source(name, s.Flows.Self.Microphone), Probability: silero.NewStream().Probability, Recognize: recognizer.Transcribe,
		Translate: a.translator.Translate}
	if name == "self" {
		if a.queue == nil || a.queueAddress != s.Chatbox.Address {
			if a.queue != nil {
				_ = a.queue.Close()
				a.queue = nil
			}
			a.queue, err = chatbox.New(s.Chatbox.Address)
			if err != nil {
				return pipeline.Stages{}, err
			}
			a.queueAddress = s.Chatbox.Address
		}
		stages.Send = a.queue.Send
	}
	return stages, nil
}

func (a *app) close() error {
	a.cancel()
	a.settingsWG.Wait()
	a.operations.Lock()
	for _, name := range []string{"self", "world"} {
		a.stop(name)
	}
	// Download completion takes operations; cancellation and this barrier prevent new downloads.
	a.operations.Unlock()
	a.downloadsWG.Wait()
	a.operations.Lock()
	defer a.operations.Unlock()
	if a.engine != nil {
		a.engine.Close()
	}
	var err error
	if a.translator != nil {
		err = a.translator.Close()
	}
	if a.queue != nil {
		err = errors.Join(err, a.queue.Close())
	}
	return err
}

func (a *app) runStages(ctx context.Context, stages pipeline.Stages) error {
	ctx, cancel := context.WithCancelCause(ctx)
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		if err := a.translator.Wait(ctx); err != nil {
			cancel(err)
		}
	}()
	err := pipeline.Run(ctx, stages)
	cancel(nil)
	<-watched
	return err
}
