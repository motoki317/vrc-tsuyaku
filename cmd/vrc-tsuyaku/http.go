package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/pipeline"
	"github.com/motoki317/vrc-tsuyaku/web"
)

func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	// Return the plain-text license notices for the app and its dependencies.
	mux.HandleFunc("GET /notices.txt", serveNotices)
	// Return 202 and exit the app as Ctrl+C does.
	mux.HandleFunc("POST /api/exit", a.handleExit)
	// Signal that a page is closing. The app exits 5 s after the latest signal if no event stream is open then.
	mux.HandleFunc("POST /api/closing", a.handleClosing)
	// Return the app snapshot used by the web page.
	mux.HandleFunc("GET /api/state", a.handleState)
	// Start this waiting instance beside the others and return its state.
	mux.HandleFunc("POST /api/instance/start", a.handleStartInstance)
	// Request a stop for one listed instance by slot.
	mux.HandleFunc("POST /api/instance/stop/{slot}", a.handleStopInstance)
	// Return each running flow's input level in dBFS after its first capture window.
	mux.HandleFunc("GET /api/levels", a.handleLevels)
	// Return microphone IDs and names.
	mux.HandleFunc("GET /api/microphones", a.handleMicrophones)
	// Render the message as chatbox pages and return their reading times and reading metrics.
	mux.HandleFunc("POST /api/chatbox/preview", a.handleChatboxPreview)
	// Save a complete settings object. Return 409 with settings_changed if settings.yaml changed on disk.
	mux.HandleFunc("PUT /api/settings", a.handleSaveSettings)
	// Save the self or world flow's enabled state and start or stop it. A start with missing downloads returns 409.
	mux.HandleFunc("POST /api/flows/{name}/{action}", a.handleChangeFlow)
	// Start a missing artifact's download. Read license acceptance from the required JSON body.
	mux.HandleFunc("POST /api/downloads/{id}", a.handleStartDownload)
	// Delete an installed artifact's files. Return 409 while a download or an active flow uses them.
	mux.HandleFunc("DELETE /api/downloads/{id}", a.handleDeleteDownload)
	// Stream the latest 256 events and new events as newline-delimited JSON, with an empty line every 5 s while idle.
	mux.HandleFunc("GET /api/events", a.streamEvents)
	// Open the data folder ("data") or settings.yaml ("settings"). Write the current settings first if the file is missing.
	mux.HandleFunc("POST /api/open/{target}", a.handleOpenTarget)
	// Serve the embedded web page and its assets.
	mux.Handle("/", http.FileServerFS(web.Files))
	return guardLocalRequests(mux)
}

// guardLocalRequests accepts only the listener's address or localhost with its port as Host.
// It refuses cross-site and same-site Sec-Fetch-Site values on every method.
// For methods other than GET and HEAD, a supplied Origin must be a single value equal to http:// followed by Host.
// With a valid Host, a request that omits both headers passes because the app trusts programs on the same PC.
// http.CrossOriginProtection exempts GET, HEAD and OPTIONS and leaves listener Host validation to the caller.
// Its Origin fallback compares only the host.
func guardLocalRequests(next http.Handler) http.Handler {
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
		next.ServeHTTP(w, r)
	})
}

func (a *app) handleExit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusAccepted)
	_ = http.NewResponseController(w).Flush()
	a.cancel()
}

func (a *app) handleClosing(w http.ResponseWriter, r *http.Request) {
	a.pageClosing()
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.snapshot())
}

func (a *app) handleStartInstance(w http.ResponseWriter, r *http.Request) {
	if !a.startInstance() {
		apiError(w, errors.New("this instance is not waiting"), http.StatusConflict)
		return
	}
	writeJSON(w, a.snapshot())
}

func (a *app) handleStopInstance(w http.ResponseWriter, r *http.Request) {
	slot, err := strconv.Atoi(r.PathValue("slot"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.stopInstance(slot); err != nil {
		if errors.Is(err, errInstanceNotFound) {
			http.NotFound(w, r)
			return
		}
		apiError(w, err, http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (a *app) handleLevels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.levels())
}

func (a *app) handleMicrophones(w http.ResponseWriter, r *http.Request) {
	devices, err := audio.Microphones(r.Context())
	if err != nil {
		apiError(w, err, http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, devices)
}

func (a *app) handleChatboxPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Template      string  `json:"template"`
		ReadingBase   float64 `json:"readingBase"`
		ReadingFactor float64 `json:"readingFactor"`
		chatbox.Message
	}
	if err := readJSON(w, r, &body); err != nil {
		apiError(w, err, http.StatusBadRequest)
		return
	}
	if err := config.ValidateReadingTime(body.ReadingBase, body.ReadingFactor, "readingBase", "readingFactor"); err != nil {
		apiError(w, err, http.StatusBadRequest)
		return
	}
	pages, err := chatbox.Render(body.Template, body.Message)
	if err != nil {
		apiError(w, err, http.StatusBadRequest)
		return
	}
	var preview struct {
		Pages   []string  `json:"pages"`
		Seconds []float64 `json:"seconds"`
		Reading struct {
			Characters          int     `json:"characters"`
			SecondsPerCharacter float64 `json:"secondsPerCharacter"`
		} `json:"reading"`
	}
	preview.Pages = pages
	preview.Seconds = make([]float64, len(pages))
	timing := chatbox.NewReadingTime(body.ReadingBase, body.ReadingFactor)
	for i, duration := range timing.PageDurations(body.Message, pages) {
		preview.Seconds[i] = duration.Seconds()
	}
	reading := chatbox.SlowerReading(body.Message)
	preview.Reading.Characters = reading.Characters
	preview.Reading.SecondsPerCharacter = reading.PerCharacter.Seconds()
	writeJSON(w, preview)
}

func (a *app) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var s config.Settings
	if err := readLimitedJSON(w, r, &s, config.MaxSettingsJSONBytes); err != nil {
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
}

func (a *app) handleChangeFlow(w http.ResponseWriter, r *http.Request) {
	name, action := config.Flow(r.PathValue("name")), flowAction(r.PathValue("action"))
	if !slices.Contains(config.FlowNames(), name) || (action != flowStart && action != flowStop) {
		http.NotFound(w, r)
		return
	}
	if err := a.changeFlow(name, action); err != nil {
		apiError(w, err, http.StatusConflict)
		return
	}
	writeJSON(w, a.snapshot())
}

func (a *app) handleStartDownload(w http.ResponseWriter, r *http.Request) {
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
}

func (a *app) handleDeleteDownload(w http.ResponseWriter, r *http.Request) {
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
}

func (a *app) handleOpenTarget(w http.ResponseWriter, r *http.Request) {
	target := openTargetName(r.PathValue("target"))
	if err := a.openTarget(target); err != nil {
		if errors.Is(err, errUnknownOpenTarget) {
			http.NotFound(w, r)
			return
		}
		apiError(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	return readLimitedJSON(w, r, v, 64<<10)
}

func readLimitedJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
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
	_ = json.NewEncoder(w).Encode(struct {
		Error    string            `json:"error"`
		Code     errcode.Code      `json:"code"`
		Location *errcode.Location `json:"location,omitempty"`
	}{withDataDirHint(err).Error(), errcode.Of(err), errcode.LocationOf(err)})
}

func (a *app) streamEvents(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.eventStreams++
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.eventStreams--
		a.mu.Unlock()
	}()
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
			if err := rc.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return
			}
			exit := struct {
				Kind pipeline.Kind `json:"kind"`
			}{Kind: kindExit}
			if err := json.NewEncoder(w).Encode(exit); err == nil {
				_ = rc.Flush()
			}
			return
		case <-changed:
		case <-time.After(5 * time.Second):
		}
	}
}
