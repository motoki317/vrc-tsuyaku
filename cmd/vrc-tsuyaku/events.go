package main

import (
	"sync"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
)

type event struct {
	ID        uint64    `json:"id"`
	Time      time.Time `json:"time"`
	Flow      string    `json:"flow,omitempty"`
	Operation string    `json:"operation,omitempty"`
	pipeline.Event
}

// A bounded history lets browsers reconnect without holding up audio processing.
type events struct {
	mu      sync.Mutex
	history []event
	next    uint64
	changed chan struct{}
}

func (b *events) publish(flow string, e pipeline.Event) {
	b.publishEvent(event{Flow: flow, Event: e})
}

func (b *events) publishEvent(e event) {
	if e.Kind == "error" && e.Code == "" {
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
