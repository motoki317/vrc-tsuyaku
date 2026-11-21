package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync/atomic"

	"github.com/motoki317/vrc-tsuyaku/internal/asr"
	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

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

func flowSource(name config.Flow, microphoneID string) audio.Source {
	if name == config.World {
		return audio.Application("VRChat.exe", "VRChat")
	}
	return audio.Microphone(microphoneID)
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
