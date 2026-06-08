package api

import (
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
)

type Server struct {
	deps          Deps
	router        http.Handler
	configMu      sync.RWMutex
	configWriteMu sync.Mutex
}

func NewServer(deps Deps) *Server {
	server := &Server{deps: deps}
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
	r.Get("/api/jobs", s.handleListJobs)
	r.Get("/api/jobs/{id}", s.handleGetJob)
	r.Post("/api/scan", s.handleScan)
	r.Post("/api/jobs/{id}/retry", s.handleRetryJob)
	r.Post("/api/jobs/{id}/ignore", s.handleIgnoreJob)
	r.Get("/api/config", s.handleGetConfig)
	r.Put("/api/config", s.handlePutConfig)
	r.Patch("/api/config/ui", s.handlePatchUIConfig)
	r.Patch("/api/config/backup", s.handlePatchBackupConfig)
	r.Get("/api/logs/recent", s.handleRecentLogs)
	r.Get("/api/backups", s.handleListBackups)
	r.Post("/api/backups/{id}/restore", s.handleRestoreBackup)
	r.Post("/api/backups/cleanup", s.handleCleanupBackups)
	r.Get("/api/events", s.handleEvents)

	return r
}
