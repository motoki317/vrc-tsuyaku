package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
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
		w := request(a, "POST", "/api/chatbox/preview", string(data), a.token)
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
	for _, token := range []string{"", "wrong"} {
		if w := request(a, "POST", "/api/chatbox/preview", `{}`, token); w.Code != http.StatusUnauthorized {
			t.Fatal(w.Code)
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
		w := request(a, "POST", "/api/chatbox/preview", tc.body, a.token)
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
		w := request(a, "PUT", "/api/settings", string(body), a.token)
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
		w := request(a, "PUT", "/api/settings", string(body), a.token)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "chatbox.template") {
			t.Fatal(w.Code, w.Body.String())
		}
		if a.settings.Chatbox.Template != "{{.Translation}}" {
			t.Fatal("invalid save changed live template")
		}
	}
}
