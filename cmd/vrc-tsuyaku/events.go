package main

import (
	"sync"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
)

const (
	// kindState carries the new flow or instance state, with empty text when the instance starts.
	kindState pipeline.Kind = "state"
	// kindSettings carries whether settings were saved, reloaded, or changed only in memory.
	kindSettings pipeline.Kind = "settings"
	// kindDownload carries the ID of the artifact whose state or progress changed.
	kindDownload pipeline.Kind = "download"
	// kindUpdate carries the result of the update check.
	kindUpdate pipeline.Kind = "update"
	// kindExit omits text and ends the stream when the app begins shutdown.
	kindExit pipeline.Kind = "exit"
)

type event struct {
	ID        uint64      `json:"id"`
	Time      time.Time   `json:"time"`
	Flow      config.Flow `json:"flow,omitempty"`
	Operation flowAction  `json:"operation,omitempty"`
	pipeline.Event
}

// A bounded history lets browsers reconnect without holding up audio processing.
type events struct {
	mu      sync.Mutex
	history []event
	next    uint64
	changed chan struct{}
}

func (b *events) publish(flow config.Flow, e pipeline.Event) {
	b.publishEvent(event{Flow: flow, Event: e})
}

func (b *events) publishEvent(e event) {
	if e.Kind == pipeline.KindError && e.Code == "" {
		e.Code = errcode.Unknown
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	e.ID, e.Time = b.next, time.Now()
	b.history = append(b.history, e)
	if len(b.history) > 256 {
		copy(b.history, b.history[len(b.history)-256:])
		b.history = b.history[:256]
	}
	if b.changed != nil {
		close(b.changed)
	}
	b.changed = make(chan struct{})
}

func (b *events) after(id uint64) ([]event, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.changed == nil {
		b.changed = make(chan struct{})
	}
	var result []event
	for _, e := range b.history {
		if e.ID > id {
			result = append(result, e)
		}
	}
	return result, b.changed
}

func (a *app) emit(flow config.Flow, kind pipeline.Kind, text string) {
	a.events.publish(flow, pipeline.Event{Kind: kind, Text: text})
}

func (a *app) emitError(flow config.Flow, err error) {
	a.events.publish(flow, pipeline.Event{Kind: pipeline.KindError, Code: errcode.Of(err), Text: withDataDirHint(err).Error()})
}
