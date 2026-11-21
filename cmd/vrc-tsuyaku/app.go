package main

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/asr"
	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/download"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/instance"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

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
