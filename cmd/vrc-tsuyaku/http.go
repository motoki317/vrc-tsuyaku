package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"runtime"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
)

//go:embed ui/*
var ui embed.FS

func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.snapshot()) })
	mux.HandleFunc("GET /api/microphones", func(w http.ResponseWriter, r *http.Request) {
		devices, err := audio.Microphones(r.Context())
		if err != nil {
			apiError(w, err, http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, devices)
	})
	mux.HandleFunc("POST /api/chatbox/preview", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Template string `json:"template"`
			chatbox.Message
		}
		if err := readJSON(w, r, &body); err != nil {
			apiError(w, err, http.StatusBadRequest)
			return
		}
		pages, err := chatbox.Render(body.Template, body.Message)
		if err != nil {
			apiError(w, err, http.StatusBadRequest)
			return
		}
		writeJSON(w, struct {
			Pages []string `json:"pages"`
		}{pages})
	})
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var s config.Settings
		if err := readJSON(w, r, &s); err != nil {
			apiError(w, err, http.StatusBadRequest)
			return
		}
		if err := a.saveSettings(s); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, config.ErrInvalidSettings) {
				status = http.StatusBadRequest
			}
			apiError(w, err, status)
			return
		}
		writeJSON(w, a.snapshot())
	})
	mux.HandleFunc("POST /api/flows/{name}/{action}", func(w http.ResponseWriter, r *http.Request) {
		name, action := r.PathValue("name"), r.PathValue("action")
		if (name != "self" && name != "world") || (action != "start" && action != "stop") {
			http.NotFound(w, r)
			return
		}
		if err := a.changeFlow(name, action); err != nil {
			apiError(w, err, http.StatusConflict)
			return
		}
		writeJSON(w, a.snapshot())
	})
	mux.HandleFunc("POST /api/downloads/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AcceptLicense bool `json:"acceptLicense"`
		}
		if err := readJSON(w, r, &body); err != nil {
			apiError(w, err, http.StatusBadRequest)
			return
		}
		if err := a.startDownload(r.PathValue("id"), body.AcceptLicense); err != nil {
			apiError(w, err, http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("DELETE /api/downloads/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := catalog.Lookup(id, runtime.GOOS); err != nil {
			http.NotFound(w, r)
			return
		}
		if err := a.deleteDownload(id); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errArtifactInUse) {
				status = http.StatusConflict
			}
			apiError(w, err, status)
			return
		}
		writeJSON(w, a.snapshot())
	})
	mux.HandleFunc("GET /api/events", a.streamEvents)
	mux.HandleFunc("POST /api/open/{target}", func(w http.ResponseWriter, r *http.Request) {
		target := r.PathValue("target")
		if target != "data" && target != "settings" {
			http.NotFound(w, r)
			return
		}
		if err := a.openTarget(target); err != nil {
			apiError(w, err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	files, _ := fs.Sub(ui, "ui")
	mux.Handle("/", http.FileServerFS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, err error, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": withDataDirHint(err).Error()})
}

func (a *app) streamEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	rc := http.NewResponseController(w)
	var id uint64
	for {
		records, changed := a.events.after(id)
		if err := rc.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return
		}
		for _, e := range records {
			if err := json.NewEncoder(w).Encode(e); err != nil {
				return
			}
			id = e.ID
		}
		if len(records) == 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-a.ctx.Done():
			return
		case <-changed:
		case <-time.After(5 * time.Second):
		}
	}
}
