package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
)

func TestFlowErrorCodeInStateAndEvents(t *testing.T) {
	for _, code := range []errcode.Code{errcode.MicrophoneUnavailable, errcode.Unknown} {
		t.Run(string(code), func(t *testing.T) {
			a := testApp(t)
			s := cloneSettings(a.settings)
			s.Flows.Self.Microphone = "synthetic-source"
			s.Chatbox.Address = "127.0.0.1:47123"
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			for id, state := range a.artifacts {
				state.State = "installed"
				a.artifacts[id] = state
			}
			failure := errors.New("source diagnostic")
			a.prepare = func(context.Context, string, config.Settings) (pipeline.Stages, error) {
				if code == errcode.Unknown {
					return pipeline.Stages{}, failure
				}
				return pipeline.Stages{}, errcode.With(code, failure)
			}
			if w := request(a, "POST", "/api/flows/self/start", ""); w.Code != http.StatusOK {
				t.Fatal(w.Code, w.Body.String())
			}
			waitFor(t, func() bool {
				a.mu.Lock()
				defer a.mu.Unlock()
				return a.flows["self"].status.State == "error"
			})
			var state struct{ Flows map[string]flowState }
			w := request(a, "GET", "/api/state", "")
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if got := state.Flows["self"]; got.Code != code || got.Error != failure.Error() {
				t.Fatal(got)
			}
			history, _ := a.events.after(0)
			last := history[len(history)-1]
			if last.Kind != "error" || last.Code != code || last.Text != failure.Error() {
				t.Fatal(last)
			}
		})
	}
}

func TestUntypedEventErrorCode(t *testing.T) {
	var events events
	events.publish("", pipeline.Event{Kind: "error", Text: "unclassified"})
	events.publish("self", pipeline.Event{Kind: "recognized", Text: "phrase"})
	history, _ := events.after(0)
	if history[0].Code != errcode.Unknown || history[1].Code != "" {
		t.Fatal(history)
	}
}
