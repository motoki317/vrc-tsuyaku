package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/internal/translate"
)

func TestChatboxPreview(t *testing.T) {
	a := testApp(t)
	before := cloneSettings(a.settings)
	for _, format := range []string{chatbox.DefaultTemplate, "{{.Translation}}", "", "{{.Original}}\n{{.Translation}}"} {
		body := struct {
			Template string `json:"template"`
			chatbox.Message
		}{format, chatbox.Message{Original: "こんにちは", Translation: strings.Repeat("Hello ", 50), SourceLang: "ja", TargetLang: "en"}}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		w := request(a, "POST", "/api/chatbox/preview", string(data))
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
		var preview struct {
			Pages []string `json:"pages"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
			t.Fatal(err)
		}
		want, err := chatbox.Render(format, body.Message)
		if err != nil || !reflect.DeepEqual(preview.Pages, want) {
			t.Fatal(preview, err)
		}
		if !reflect.DeepEqual(a.settings, before) {
			t.Fatal("preview changed settings")
		}
	}
}

func TestChatboxPreviewErrors(t *testing.T) {
	a := testApp(t)
	for _, tc := range []struct{ body, want string }{
		{`{"template":"{{"}`, "chatbox template"},
		{`{"template":"{{.Unknown}}"}`, "Unknown"},
		{`{"template":"{{index .Translation 3}}","translation":"a"}`, "index"},
		{`{"template":"{{.Original}}","original":"bad\u0000"}`, "NUL"},
		{`{"template":"{{.Original}}","extra":true}`, "unknown field"},
		{`{} {}`, "one JSON object"},
		{`{`, "EOF"},
		{`{"template":"` + strings.Repeat("a", 64<<10) + `"}`, "too large"},
	} {
		w := request(a, "POST", "/api/chatbox/preview", tc.body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestTemplateSettingsAPI(t *testing.T) {
	a := testApp(t)
	s := cloneSettings(a.settings)
	s.Chatbox.Template = "{{.Translation}}"
	body, _ := json.Marshal(s)
	for range 2 {
		w := request(a, "PUT", "/api/settings", string(body))
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
		saved, err := config.Load(a.dir)
		if err != nil || !reflect.DeepEqual(saved, s) {
			t.Fatal(saved, err)
		}
	}
	for _, format := range []string{"{{", "[{{.SourceLang}}] {{.Orignal}}"} {
		s.Chatbox.Template = format
		body, _ = json.Marshal(s)
		w := request(a, "PUT", "/api/settings", string(body))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "chatbox.template") {
			t.Fatal(w.Code, w.Body.String())
		}
		if a.settings.Chatbox.Template != "{{.Translation}}" {
			t.Fatal("invalid save changed live template")
		}
	}
}

func TestWorldFlowUsesDefaultTemplate(t *testing.T) {
	a := testApp(t)
	for id, artifact := range a.artifacts {
		artifact.State = "installed"
		a.artifacts[id] = artifact
	}
	a.translator = testTranslator(t)
	a.settings.Chatbox.Template = "{{.Translation}}"
	originals := []string{strings.Repeat("a", 80), strings.Repeat("b", 80), "c"}
	i := 0
	a.prepare = func(context.Context, string, config.Settings) (pipeline.Stages, error) {
		return pipeline.Stages{
			Source: recordingSource(func(_ context.Context, consume func([]float32) error) error {
				for range originals {
					samples := make([]float32, 512*24)
					for i := range 512 * 8 {
						samples[i] = 1
					}
					if err := consume(samples); err != nil {
						return err
					}
				}
				return nil
			}),
			Probability: func(_ context.Context, samples []float32) (float32, error) { return samples[0], nil },
			Recognize:   func(context.Context, []float32) (string, error) { text := originals[i]; i++; return text, nil },
			Translate:   func(_ context.Context, _, _, text string) (string, error) { return text[:1], nil },
		}, nil
	}
	if err := a.changeFlow("world", "start"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.flows["world"].status.State == "stopped"
	})
	history, _ := a.events.after(0)
	var ids []uint64
	var merged []pipeline.Event
	for _, e := range history {
		switch e.Kind {
		case "recognized":
			ids = append(ids, e.PhraseID)
		case "merged":
			merged = append(merged, e.Event)
		case "error", "page", "sent":
			t.Fatalf("unexpected world event: %+v", e)
		}
	}
	if len(ids) != 3 || len(merged) != 1 {
		t.Fatalf("recognized = %v, merged = %+v", ids, merged)
	}
	source, target := a.settings.Flows.Languages("world")
	want := pipeline.Event{PhraseID: ids[2], Into: ids[1], Kind: "merged", Original: originals[1] + " c", Text: "b c", SourceLang: source, TargetLang: target}
	if merged[0] != want {
		t.Fatalf("world merge = %+v, want %+v with the default template", merged[0], want)
	}
}

func TestTemplateEditsAndErrorsKeepFlowRunning(t *testing.T) {
	a := testApp(t)
	for id, artifact := range a.artifacts {
		artifact.State = "installed"
		a.artifacts[id] = artifact
	}
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	a.settings.Flows.Self.Microphone = "synthetic"
	a.settings.Chatbox.Address = listener.LocalAddr().String()
	a.settings.Chatbox.Template = `{{if eq .Original "1"}}partial {{index .Translation 100}}{{else}}` + chatbox.DefaultTemplate + `{{end}}`
	a.queue, err = chatbox.New(a.settings.Chatbox.Address)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.translator, err = translate.New(executable, "unused-model")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.translator.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	input := make(chan struct{}, 1)
	translating, release := make(chan struct{}), make(chan struct{})
	a.prepare = func(context.Context, string, config.Settings) (pipeline.Stages, error) {
		i := 0
		return pipeline.Stages{
			Source: recordingSource(func(ctx context.Context, consume func([]float32) error) error {
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-input:
						samples := make([]float32, 512*24)
						for i := range 512 * 8 {
							samples[i] = 1
						}
						if err := consume(samples); err != nil {
							return err
						}
					}
				}
			}),
			Probability: func(_ context.Context, samples []float32) (float32, error) { return samples[0], nil },
			Recognize: func(context.Context, []float32) (string, error) {
				i++
				return string(rune('0' + i)), nil
			},
			Translate: func(ctx context.Context, _, _, phrase string) (string, error) {
				if phrase == "2" {
					close(translating)
					select {
					case <-ctx.Done():
						return "", ctx.Err()
					case <-release:
					}
				}
				return "translation " + phrase, nil
			},
			Send: a.queue.Send,
		}, nil
	}
	if err := a.changeFlow("self", "start"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.flows["self"].status.State == "running" })
	a.mu.Lock()
	run := a.flows["self"]
	s := cloneSettings(a.settings)
	a.mu.Unlock()
	input <- struct{}{}
	waitFor(t, func() bool {
		history, _ := a.events.after(0)
		for _, e := range history {
			if e.Kind == "error" {
				if e.Flow != "self" || e.PhraseID == 0 || !strings.Contains(e.Text, "index") {
					t.Error("template error missing phrase or text", e)
				}
				return true
			}
		}
		return false
	})
	input <- struct{}{}
	select {
	case <-translating:
	case <-time.After(5 * time.Second):
		t.Fatal("flow did not continue after the template error")
	}
	s.Chatbox.Template = "{{.Translation}}!"
	body, _ := json.Marshal(s)
	if w := request(a, "PUT", "/api/settings", string(body)); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	close(release)
	for i, want := range []string{"[ja] 2\n[en] translation 2", "translation 3!"} {
		if i == 1 {
			input <- struct{}{}
		}
		if err := listener.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		packet := make([]byte, 2048)
		n, _, err := listener.ReadFromUDP(packet)
		if err != nil {
			t.Fatal(err)
		}
		if got := decodeTestOSC(t, packet[:n]); got != want {
			t.Fatalf("received %q, want %q", got, want)
		}
	}
	waitFor(t, func() bool {
		history, _ := a.events.after(0)
		count := 0
		for _, e := range history {
			if e.Kind == "sent" {
				count++
			}
		}
		return count == 2
	})
	a.mu.Lock()
	unchanged := a.flows["self"] == run && run.status.State == "running"
	a.mu.Unlock()
	if !unchanged {
		t.Fatal("template edit or error restarted the flow")
	}
}
