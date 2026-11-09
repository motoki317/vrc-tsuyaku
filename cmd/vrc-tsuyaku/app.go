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
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

type artifactState struct {
	Artifact catalog.Artifact  `json:"artifact"`
	State    string            `json:"state"`
	Progress download.Progress `json:"progress"`
	Error    string            `json:"error,omitempty"`
}

type flowState struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
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
	artifacts     map[string]artifactState
	flows         map[string]*flowRun
	events        events
	// operations serializes startEnabled, saveSettings, changeFlow,
	// startDownload and close. Callers hold it while calling start or stop.
	operations   sync.Mutex
	downloadsWG  sync.WaitGroup
	modelTurn    chan struct{}
	engine       *asr.Engine
	translator   *translate.Supervisor
	queue        *chatbox.Queue
	queueAddress string
	source       func(flowName, microphoneID string) audio.Source
	prepare      func(ctx context.Context, flowName string, settings config.Settings) (pipeline.Stages, error)
}

func newApp(ctx context.Context, dir string) (*app, error) {
	manager, err := download.New(dir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	a := &app{ctx: ctx, cancel: cancel, dir: dir, token: rand.Text(), downloads: manager,
		artifacts: make(map[string]artifactState), flows: make(map[string]*flowRun), modelTurn: make(chan struct{}, 1),
		source: flowSource,
	}
	a.prepare = a.prepareModels
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
			a.emit("", "error", state.Error)
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
		requirements[name] = required(name)
	}
	return struct {
		Settings      config.Settings          `json:"settings"`
		SettingsError string                   `json:"settingsError,omitempty"`
		Artifacts     map[string]artifactState `json:"artifacts"`
		Flows         map[string]flowState     `json:"flows"`
		Required      map[string][]string      `json:"required"`
	}{cloneSettings(a.settings), a.settingsError, maps.Clone(a.artifacts), flows, requirements}
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

func required(name string) []string {
	model := catalog.Haas
	if name == "world" {
		model = catalog.Parakeet
	}
	return []string{catalog.ONNXRuntime, catalog.Silero, model, catalog.LlamaCPP, catalog.PLaMo}
}

// Model initialization runs outside operations so stop can cancel a pending start.
func (a *app) start(name string, s config.Settings) error {
	a.mu.Lock()
	if a.flows[name].status.active() {
		a.mu.Unlock()
		return nil
	}
	if err := a.checkStart(name); err != nil {
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
			run.status = flowState{State: "error", Error: withDataDirHint(err).Error()}
		}
		state := run.status
		if ctx.Err() == nil {
			if state.Error != "" {
				a.emit(name, "error", state.Error)
			} else {
				a.emit(name, "state", state.State)
			}
		}
		a.mu.Unlock()
	}()
	return nil
}

// The caller holds mu while checking artifact state.
func (a *app) checkStart(name string) error {
	if err := a.ctx.Err(); err != nil {
		return err
	}
	var missing []string
	for _, id := range required(name) {
		if a.artifacts[id].State != "installed" {
			missing = append(missing, a.artifacts[id].Artifact.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing downloads: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (a *app) refuseStart(name string, err error) {
	a.events.publishEvent(event{Flow: name, Operation: "start", Event: pipeline.Event{Kind: "error", Text: err.Error()}})
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
	a.mu.Lock()
	old := cloneSettings(a.settings)
	a.mu.Unlock()
	s.LicensesAccepted = old.LicensesAccepted
	s.Flows.Self.Enabled = old.Flows.Self.Enabled
	s.Flows.World.Enabled = old.Flows.World.Enabled
	if err := config.Save(a.dir, s); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = cloneSettings(s)
	a.settingsError = ""
	restartSelf := a.flows["self"].status.active()
	a.mu.Unlock()
	if restartSelf && (old.Flows.Self.Microphone != s.Flows.Self.Microphone || old.Chatbox.Address != s.Chatbox.Address) {
		a.stop("self")
		_ = a.start("self", s)
	}
	a.emit("", "settings", "saved")
	return nil
}

func (a *app) changeFlow(name, action string) error {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.mu.Lock()
	s := cloneSettings(a.settings)
	settingsError := a.settingsError
	var startErr error
	if action == "start" {
		startErr = a.checkStart(name)
	}
	a.mu.Unlock()
	if settingsError != "" {
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
	if err := config.Save(a.dir, s); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = s
	a.settingsError = ""
	a.mu.Unlock()
	a.emit("", "settings", "saved")
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
			if err := config.Save(a.dir, s); err != nil {
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
	state.Progress = download.Progress{}
	a.mu.Lock()
	a.artifacts[id] = state
	a.mu.Unlock()
	a.downloadsWG.Go(func() {
		last := time.Time{}
		err := a.downloads.Download(a.ctx, id, runtime.GOOS, func(p download.Progress) {
			a.mu.Lock()
			state.Progress = p
			a.artifacts[id] = state
			a.mu.Unlock()
			if time.Since(last) > 250*time.Millisecond {
				a.emit("", "download", id)
				last = time.Now()
			}
		})
		state.State = "installed"
		if err != nil {
			state.Error = withDataDirHint(err).Error()
			if !errors.Is(err, download.ErrCleanup) {
				state.State = "missing"
			}
		}
		a.mu.Lock()
		a.artifacts[id] = state
		a.mu.Unlock()
		if err != nil {
			a.emit("", "error", state.Error)
		}
		a.emit("", "download", id)
	})
	a.emit("", "download", id)
	return nil
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
	direction := translate.JapaneseToEnglish
	if name == "self" {
		recognizer, err = a.engine.LoadHaas(catalog.HaasBundle(a.artifactDir(catalog.Haas), true))
	} else {
		recognizer, err = a.engine.LoadParakeet(catalog.ParakeetBundle(a.artifactDir(catalog.Parakeet)))
		direction = translate.EnglishToJapanese
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
		Translate: func(ctx context.Context, text string) (string, error) {
			return a.translator.Translate(ctx, direction, text)
		}}
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
		stages.Send = a.queue.SendPages
	}
	return stages, nil
}

func (a *app) close() error {
	a.cancel()
	a.operations.Lock()
	defer a.operations.Unlock()
	for _, name := range []string{"self", "world"} {
		a.stop(name)
	}
	a.downloadsWG.Wait()
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
