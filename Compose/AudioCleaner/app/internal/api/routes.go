package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"omv-blueprint/compose/audiocleaner/internal/settings"
)

type Server struct {
	deps     Deps
	router   http.Handler
	settings *settings.Store
}

func NewServer(deps Deps) *Server {
	server := &Server{deps: deps, settings: settings.NewStore(deps.Config, deps.ConfigPath, deps.ConfigApplicator)}
	server.router = server.routes()
	return server
}

func (s *Server) Router() http.Handler {
	return s.router
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	})

	r.Get("/", s.handleRoot)
	r.Handle("/assets/*", s.handleAssets())
	r.Get("/api/health", s.handleHealth)
	r.Get("/api/status", s.handleStatus)
	r.Get("/api/runtime-tasks", s.handleRuntimeTasks)
	r.Get("/api/history", s.handleListHistory)
	r.Post("/api/history/{job_id}/retry", s.handleRetryHistoryJob)
	r.Post("/api/history/{job_id}/ignore", s.handleIgnoreHistoryJob)
	r.Post("/api/scan", s.handleScan)
	r.Get("/api/config", s.handleGetConfig)
	r.Get("/api/config/defaults", s.handleGetConfigDefaults)
	for _, module := range settings.Modules() {
		module := module
		r.Patch("/api/config/"+string(module), s.handlePatchConfig(module))
	}
	r.Get("/api/logs", s.handleRuntimeLogs)
	r.Get("/api/files", s.handleListFiles)
	r.Post("/api/files/{file_id}/process", s.handleProcessFile)
	r.Post("/api/files/{file_id}/restore-backup", s.handleRestoreFileBackup)
	r.Delete("/api/files/{file_id}/backup", s.handleDeleteFileBackup)
	r.Get("/api/events", s.handleEvents)

	return r
}
