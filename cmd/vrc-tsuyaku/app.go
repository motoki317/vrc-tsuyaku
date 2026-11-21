package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"math"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/asr"
	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/download"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
	"github.com/motoki317/vrc-tsuyaku/internal/instance"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

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

type flowStatus string

const (
	// flowStopped means the flow has no active run.
	flowStopped flowStatus = "stopped"
	// flowStarting means the flow is preparing its pipeline.
	flowStarting flowStatus = "starting"
	// flowRunning means the flow has started its pipeline.
	flowRunning flowStatus = "running"
	// flowFailed means startup or the flow's pipeline failed.
	flowFailed flowStatus = "error"
)

type flowState struct {
	State flowStatus   `json:"state"`
	Error string       `json:"error,omitempty"`
	Code  errcode.Code `json:"code,omitempty"`
}

func flowError(err error) flowState {
	return flowState{State: flowFailed, Error: withDataDirHint(err).Error(), Code: errcode.Of(err)}
}

func (s flowState) active() bool {
	return s.State == flowStarting || s.State == flowRunning
}

type flowRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	status flowState
	// Holds the bits of a float64 level in dBFS.
	// NaN means no reading because the zero value reads as 0 dBFS.
	level atomic.Uint64
}

// At 1.5 dB per 32 ms window, a sound between two of the page's 100 ms polls
// reads at most about 5 dB low.
const levelFallDB = 1.5

func (r *flowRun) meter(level float64) {
	if previous := math.Float64frombits(r.level.Load()); !math.IsNaN(previous) {
		level = max(level, previous-levelFallDB)
	}
	r.level.Store(math.Float64bits(level))
}

type app struct {
	ctx           context.Context
	cancel        context.CancelFunc
	dir           string
	launch        string
	info          appInfo
	update        updateState
	downloads     *download.Manager
	mu            sync.Mutex
	settings      config.Settings
	settingsError string
	settingsFile  settingsFileState
	artifacts     map[string]artifactState
	flows         map[config.Flow]*flowRun
	events        events
	eventStreams  int
	closeGrace    time.Duration
	closeTimer    *time.Timer
	instance      instanceState
	membership    *instance.Membership
	instanceHost  string
	instancePoll  time.Duration
	stopGrace     time.Duration
	killGrace     time.Duration
	kill          func(int) error
	// operations serializes settings, flow and storage changes, including close.
	// Callers hold it while calling start or stop.
	operations   sync.Mutex
	settingsWG   sync.WaitGroup
	downloadsWG  sync.WaitGroup
	updateWG     sync.WaitGroup
	instanceWG   sync.WaitGroup
	modelTurn    chan struct{}
	engine       *asr.Engine
	translator   *translate.Supervisor
	queue        *chatbox.Queue
	queueAddress string
	source       func(flowName config.Flow, microphoneID string) audio.Source
	// start must call any returned Stages.Close, even on preparation error or cancellation.
	prepare          func(ctx context.Context, flowName config.Flow, settings config.Settings) (pipeline.Stages, error)
	downloadArtifact func(context.Context, string, string, func(download.Progress)) error
	open             func(string) error
}

func newApp(ctx context.Context, dir string) (*app, error) {
	manager, err := download.New(dir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	build, _ := debug.ReadBuildInfo()
	a := &app{ctx: ctx, cancel: cancel, dir: dir, downloads: manager,
		launch:       rand.Text(),
		info:         appBuildInfo(build),
		update:       updateState{State: updateChecking},
		closeGrace:   5 * time.Second,
		instancePoll: time.Second, stopGrace: 10 * time.Second, killGrace: 5 * time.Second,
		kill:      killProcess,
		artifacts: make(map[string]artifactState), flows: make(map[config.Flow]*flowRun), modelTurn: make(chan struct{}, 1),
		source: flowSource, open: openFile,
	}
	a.prepare = a.prepareModels
	a.downloadArtifact = manager.Download
	a.settingsFile = a.statSettings()
	settings, loadErr := config.Load(dir)
	if loadErr != nil {
		settings = config.Defaults()
		a.settingsError = withDataDirHint(loadErr).Error()
		a.emit("", pipeline.KindError, a.settingsError)
	}
	a.settings = settings
	artifacts, err := catalog.List(runtime.GOOS)
	if err != nil {
		cancel()
		return nil, err
	}
	for _, artifact := range artifacts {
		installed, err := manager.CheckInstalled(ctx, artifact.ID, runtime.GOOS)
		state := artifactState{Artifact: artifact, State: artifactMissing}
		if installed {
			state.State = artifactInstalled
		}
		if err != nil {
			state.Error = withDataDirHint(err).Error()
			state.Code = errcode.Of(err)
			a.emitError("", err)
		}
		a.artifacts[artifact.ID] = state
	}
	for _, name := range config.FlowNames() {
		run := &flowRun{status: flowState{State: flowStopped}}
		run.level.Store(math.Float64bits(math.NaN()))
		a.flows[name] = run
	}
	return a, nil
}

func flowSource(name config.Flow, microphoneID string) audio.Source {
	if name == config.World {
		return audio.Application("VRChat.exe", "VRChat")
	}
	return audio.Microphone(microphoneID)
}

func (a *app) emit(flow config.Flow, kind pipeline.Kind, text string) {
	a.events.publish(flow, pipeline.Event{Kind: kind, Text: text})
}

func (a *app) emitError(flow config.Flow, err error) {
	a.events.publish(flow, pipeline.Event{Kind: pipeline.KindError, Code: errcode.Of(err), Text: withDataDirHint(err).Error()})
}

func cloneSettings(s config.Settings) config.Settings {
	s.Glossary = append([]glossary.Entry{}, s.Glossary...)
	s.LicensesAccepted = maps.Clone(s.LicensesAccepted)
	if s.LicensesAccepted == nil {
		s.LicensesAccepted = make(map[string]string)
	}
	return s
}

func (a *app) snapshot() any {
	a.mu.Lock()
	defer a.mu.Unlock()
	flows := make(map[config.Flow]flowState)
	for name, run := range a.flows {
		flows[name] = run.status
	}
	type bounds struct {
		Min float64 `json:"min"`
		Max float64 `json:"max"`
	}
	reading := map[string]bounds{
		"base":   {chatbox.MinReadingBaseSeconds, chatbox.MaxReadingBaseSeconds},
		"factor": {chatbox.MinReadingFactor, chatbox.MaxReadingFactor},
	}
	return struct {
		Launch          string                    `json:"launch"`
		App             appInfo                   `json:"app"`
		Update          updateState               `json:"update"`
		Settings        config.Settings           `json:"settings"`
		SettingsError   string                    `json:"settingsError,omitempty"`
		Artifacts       map[string]artifactState  `json:"artifacts"`
		Flows           map[config.Flow]flowState `json:"flows"`
		Languages       catalog.LanguageCatalog   `json:"languages"`
		GlossaryLimit   int                       `json:"glossaryLimit"`
		DefaultTemplate string                    `json:"defaultTemplate"`
		Reading         map[string]bounds         `json:"reading"`
		Instance        instanceState             `json:"instance"`
	}{a.launch, a.info, a.update, cloneSettings(a.settings), a.settingsError, maps.Clone(a.artifacts), flows, catalog.Languages(), glossary.MaxEntries, chatbox.DefaultTemplate, reading,
		instanceState{State: a.instance.State, Others: slices.Clone(a.instance.Others)}}
}

func (a *app) levels() map[config.Flow]float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	levels := make(map[config.Flow]float64)
	for name, run := range a.flows {
		if level := math.Float64frombits(run.level.Load()); !math.IsNaN(level) {
			levels[name] = level
		}
	}
	return levels
}

func (a *app) startEnabled() {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.mu.Lock()
	s := a.settings
	valid := a.settingsError == "" && a.instance.State != instanceWaiting
	a.mu.Unlock()
	if !valid {
		return
	}
	for _, name := range config.FlowNames() {
		if s.Flows.Enabled(name) {
			_ = a.start(name, s)
		}
	}
}

// Model initialization runs outside operations so stop can cancel a pending start.
func (a *app) start(name config.Flow, s config.Settings) error {
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
	run := &flowRun{cancel: cancel, done: make(chan struct{}), status: flowState{State: flowStarting}}
	run.level.Store(math.Float64bits(math.NaN()))
	a.flows[name] = run
	a.mu.Unlock()
	a.emit(name, kindState, string(flowStarting))
	go func() {
		defer close(run.done)
		defer cancel()
		stages, err := a.prepare(ctx, name, s)
		if err == nil && ctx.Err() == nil {
			stages.Settings = func() pipeline.PhraseSettings {
				a.mu.Lock()
				defer a.mu.Unlock()
				source, target := a.settings.Flows.Languages(name)
				box := a.settings.Chatbox
				format := box.Template
				if name == config.World {
					format = chatbox.DefaultTemplate
				}
				terms := glossary.Terms(a.settings.Glossary, source, target)
				return pipeline.PhraseSettings{
					SourceLang: source, TargetLang: target, Template: format,
					ReadingTime: chatbox.NewReadingTime(box.ReadingBase, box.ReadingFactor),
					Terms:       terms,
				}
			}
			stages.CutLevel = func() float64 {
				a.mu.Lock()
				defer a.mu.Unlock()
				return a.settings.Flows.CutLevel(name)
			}
			stages.Meter = run.meter
			a.mu.Lock()
			run.status = flowState{State: flowRunning}
			a.mu.Unlock()
			a.emit(name, kindState, string(flowRunning))
			stages.Emit = func(e pipeline.Event) { a.events.publish(name, e) }
			err = a.runStages(ctx, stages)
			run.level.Store(math.Float64bits(math.NaN()))
		}
		if stages.Close != nil {
			stages.Close()
		}
		a.mu.Lock()
		run.status = flowState{State: flowStopped}
		if err != nil && ctx.Err() == nil {
			run.status = flowError(err)
		}
		state := run.status
		if ctx.Err() == nil {
			if state.Error != "" {
				a.events.publish(name, pipeline.Event{Kind: pipeline.KindError, Code: state.Code, Text: state.Error})
			} else {
				a.emit(name, kindState, string(state.State))
			}
		}
		a.mu.Unlock()
	}()
	return nil
}

// The caller holds mu while checking artifact state.
func (a *app) checkStart(name config.Flow, s config.Settings) error {
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if a.instance.State == instanceWaiting {
		return errInstancePending
	}
	var missing []string
	spoken, _ := s.Flows.Languages(name)
	required, err := catalog.Required(spoken, s.Translator)
	if err != nil {
		return err
	}
	for _, id := range required {
		if a.artifacts[id].State != artifactInstalled {
			missing = append(missing, a.artifacts[id].Artifact.Name)
		}
	}
	if len(missing) > 0 {
		return errcode.With(errcode.MissingDownloads, fmt.Errorf("missing downloads: %s", strings.Join(missing, ", ")))
	}
	return nil
}

func (a *app) refuseStart(name config.Flow, err error) {
	a.mu.Lock()
	if !a.flows[name].status.active() {
		a.flows[name].status = flowError(err)
	}
	a.mu.Unlock()
	a.events.publishEvent(event{Flow: name, Operation: flowStart, Event: pipeline.Event{Kind: pipeline.KindError, Code: errcode.Of(err), Text: err.Error()}})
}

func (a *app) stop(name config.Flow) {
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
	run.status = flowState{State: flowStopped}
	a.mu.Unlock()
	if active {
		a.emit(name, kindState, string(flowStopped))
	}
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

type flowAction string

const (
	// flowStart enables a flow and requests a run.
	flowStart flowAction = "start"
	// flowStop disables a flow and stops its run.
	flowStop flowAction = "stop"
)

func (a *app) changeFlow(name config.Flow, action flowAction) error {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.reloadSettingsLocked()
	a.mu.Lock()
	s := cloneSettings(a.settings)
	settingsError := a.settingsError
	var startErr error
	if action == flowStart {
		startErr = a.checkStart(name, s)
	}
	a.mu.Unlock()
	if settingsError != "" && action != flowStop && errcode.Of(startErr) != errcode.InstancePending {
		return fmt.Errorf("save valid settings before changing a flow: %s", settingsError)
	}
	if startErr != nil {
		a.refuseStart(name, startErr)
		return startErr
	}
	s.Flows.SetEnabled(name, action == flowStart)
	if settingsError == "" {
		if err := a.writeSettings(s); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	text := settingsSaved
	if settingsError != "" {
		text = settingsChanged
	}
	a.emit("", kindSettings, string(text))
	if action == flowStop {
		a.stop(name)
		return nil
	}
	return a.start(name, s)
}

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
			if time.Since(last) > 250*time.Millisecond {
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

// Callers hold operations to serialize retries with stop and settings changes.
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

func (a *app) prepareModels(ctx context.Context, name config.Flow, s config.Settings) (stages pipeline.Stages, err error) {
	select {
	case a.modelTurn <- struct{}{}:
	case <-ctx.Done():
		return pipeline.Stages{}, ctx.Err()
	}
	defer func() { <-a.modelTurn }()
	if err := ctx.Err(); err != nil {
		return pipeline.Stages{}, err
	}
	if a.engine == nil {
		a.engine, err = asr.NewEngine(catalog.ORTLibrary(a.artifactDir(catalog.ONNXRuntime)))
		if err != nil {
			return pipeline.Stages{}, err
		}
	}
	silero, releaseSilero, err := a.engine.LoadSilero(catalog.SileroModel(a.artifactDir(catalog.Silero)))
	if err != nil {
		return pipeline.Stages{}, err
	}
	var releaseRecognizer func()
	release := func() {
		if releaseRecognizer != nil {
			releaseRecognizer()
		}
		releaseSilero()
	}
	stages = pipeline.Stages{Close: release}
	var recognizer asr.Recognizer
	spoken, _ := s.Flows.Languages(name)
	language, ok := catalog.Spoken(spoken)
	if !ok {
		return stages, fmt.Errorf("unsupported spoken language %q", spoken)
	}
	switch language.Recognizer {
	case catalog.Haas:
		recognizer, releaseRecognizer, err = a.engine.LoadHaas(catalog.HaasBundle(a.artifactDir(catalog.Haas), true))
	case catalog.Parakeet, catalog.ParakeetV3:
		recognizer, releaseRecognizer, err = a.engine.LoadParakeet(catalog.ParakeetBundle(language.Recognizer, a.artifactDir(language.Recognizer)))
	default:
		return stages, fmt.Errorf("unsupported recognizer %q", language.Recognizer)
	}
	if err != nil {
		return stages, err
	}
	if a.translator == nil {
		translator, ok := catalog.LookupTranslator(s.Translator)
		if !ok {
			return stages, fmt.Errorf("unsupported translator %q", s.Translator)
		}
		a.translator, err = translate.New(catalog.LlamaServer(a.artifactDir(catalog.LlamaCPP)), catalog.PLaMoModel(translator.Artifact, a.artifactDir(translator.Artifact)))
		if err != nil {
			return stages, err
		}
	}
	if err = a.translator.Start(ctx); err != nil {
		return stages, err
	}
	if name == config.Self {
		if a.queue == nil || a.queueAddress != s.Chatbox.Address {
			if a.queue != nil {
				_ = a.queue.Close()
				a.queue = nil
			}
			a.queue, err = chatbox.New(s.Chatbox.Address)
			if err != nil {
				return stages, err
			}
			a.queueAddress = s.Chatbox.Address
		}
		stages.Send = a.queue.Send
	}
	stages.Source = a.source(name, s.Flows.Self.Microphone)
	stages.Probability = silero.NewStream().Probability
	stages.Recognize = func(ctx context.Context, samples []float32, settings pipeline.PhraseSettings) (string, error) {
		return recognizer.Transcribe(ctx, samples, glossary.Words(settings.Terms))
	}
	var history translate.History
	stages.Translate = func(ctx context.Context, phrase string, settings pipeline.PhraseSettings) (string, error) {
		return a.translator.Translate(ctx, settings.SourceLang, settings.TargetLang, phrase, settings.Terms, &history)
	}
	return stages, nil
}

func (a *app) pageClosing() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx.Err() != nil {
		return
	}
	if a.closeTimer != nil {
		a.closeTimer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(a.closeGrace, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		// Stop cannot prevent a callback that already waits for the lock.
		if a.closeTimer != timer {
			return
		}
		a.closeTimer = nil
		if a.eventStreams == 0 {
			a.cancel()
		}
	})
	a.closeTimer = timer
}

func (a *app) close() error {
	a.cancel()
	a.mu.Lock()
	if a.closeTimer != nil {
		a.closeTimer.Stop()
		a.closeTimer = nil
	}
	a.mu.Unlock()
	a.updateWG.Wait()
	a.settingsWG.Wait()
	a.instanceWG.Wait()
	a.operations.Lock()
	for _, name := range config.FlowNames() {
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
