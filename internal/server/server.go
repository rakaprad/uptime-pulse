// Package server exposes the status page, JSON API, health and metrics endpoints.
package server

import (
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakaprad/uptime-pulse/internal/checker"
)

//go:embed templates/index.html
var templateFS embed.FS

var indexTmpl = template.Must(template.New("index.html").Funcs(template.FuncMap{
	"ago": func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		return time.Since(t).Round(time.Second).String() + " ago"
	},
}).ParseFS(templateFS, "templates/index.html"))

// StatusSource provides the current status of all targets.
type StatusSource interface {
	Snapshot() []checker.Status
}

type pageData struct {
	Version  string
	Now      time.Time
	Up       int
	Total    int
	Statuses []checker.Status
}

// Handler builds the HTTP routes. metrics is typically promhttp's handler.
func Handler(src StatusSource, metrics http.Handler, version string, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		statuses := src.Snapshot()
		data := pageData{Version: version, Now: time.Now(), Total: len(statuses), Statuses: statuses}
		for _, s := range statuses {
			if s.State == checker.StateUp {
				data.Up++
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := indexTmpl.Execute(w, data); err != nil {
			log.Error("render status page", "error", err)
		}
	})

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"targets": src.Snapshot()}); err != nil {
			log.Error("encode status", "error", err)
		}
	})

	// Liveness only: the process is serving HTTP. Target health is deliberately
	// not part of it, otherwise an outage elsewhere would restart the monitor.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.Handle("GET /metrics", metrics)
	return mux
}
