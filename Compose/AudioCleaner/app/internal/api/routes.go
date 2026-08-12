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
	if deps.MediaRoot == "" {
		deps.MediaRoot = mediaMountPath
	}
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
	r.Get("/api/history/{job_id}/retry-context", s.handleRetryHistoryContext)
	r.Post("/api/history/{job_id}/ignore", s.handleIgnoreHistoryJob)
	r.Post("/api/scan", s.handleScan)
	r.Post("/api/scans", s.handleCreateScan)
	r.Get("/api/scans/current", s.handleCurrentScan)
	r.Delete("/api/scans/{scan_id}", s.handleCancelScan)
	r.Get("/api/discovery-status", s.handleDiscoveryStatus)
	r.Get("/api/config", s.handleGetConfig)
	r.Get("/api/config/defaults", s.handleGetConfigDefaults)
	r.Get("/api/media/directories", s.handleListMediaDirectories)
	for _, module := range settings.Modules() {
		module := module
		r.Patch("/api/config/"+string(module), s.handlePatchConfig(module))
	}
	r.Get("/api/logs", s.handleRuntimeLogs)
	r.Get("/api/files", s.handleListFiles)
	r.Post("/api/files/{file_id}/process", s.handleProcessFile)
	r.Get("/api/files/{file_id}/retry-context", s.handleRetryFileContext)
	r.Post("/api/files/{file_id}/restore-backup", s.handleRestoreFileBackup)
	r.Delete("/api/files/{file_id}/backup", s.handleDeleteFileBackup)
	r.Get("/api/recovery", s.handleListRecovery)
	r.Get("/api/failures", s.handleListFailures)
	r.Get("/api/recovery/{file_id}/retry-context", s.handleRetryRecoveryContext)
	r.Post("/api/recovery/{file_id}/retry", s.handleRetryRecovery)
	r.Post("/api/recovery/{file_id}/retain", s.handleRetainRecovery)
	r.Post("/api/recovery/{file_id}/restore", s.handleRestoreRecovery)
	r.Delete("/api/recovery/{file_id}/backup", s.handleDeleteRecoveryBackup)
	r.Get("/api/events", s.handleEvents)

	return r
}
