package main

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

//go:embed ui/*
var ui embed.FS

func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /notices.txt", serveNotices)
	mux.HandleFunc("POST /api/exit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusAccepted)
		_ = http.NewResponseController(w).Flush()
		a.cancel()
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.snapshot()) })
	mux.HandleFunc("GET /api/levels", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.levels()) })
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
			if errcode.Of(err) == errcode.SettingsChanged {
				status = http.StatusConflict
			} else if errors.Is(err, config.ErrInvalidSettings) {
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
		local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
		if !ok {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		_, port, err := net.SplitHostPort(local.String())
		if err != nil {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Host != local.String() && !strings.EqualFold(r.Host, "localhost:"+port) {
			http.Error(w, "forbidden host: open http://"+local.String()+"/", http.StatusForbidden)
			return
		}
		// http.CrossOriginProtection checks only methods other than GET, HEAD and OPTIONS,
		// so it cannot refuse a cross-site GET. This check covers every method.
		switch r.Header.Get("Sec-Fetch-Site") {
		case "cross-site", "same-site":
			http.Error(w, "forbidden fetch site: type http://"+local.String()+"/ into the address bar", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origins := r.Header.Values("Origin"); len(origins) > 0 && (len(origins) != 1 || origins[0] != "http://"+r.Host) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
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
	_ = json.NewEncoder(w).Encode(map[string]string{"error": withDataDirHint(err).Error(), "code": string(errcode.Of(err))})
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
