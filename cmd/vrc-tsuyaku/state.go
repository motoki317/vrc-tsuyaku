package main

import (
	"maps"
	"math"
	"slices"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
)

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

// levels keeps capture-window updates out of the event stream so they cannot crowd phrases out of its history.
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
