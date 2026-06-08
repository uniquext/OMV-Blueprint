package api

import (
	"net/http"
	"os"
	"path/filepath"
)

const defaultWebDir = "/app/web/dist"

func (s *Server) webDir() string {
	if s.deps.WebDir != "" {
		return s.deps.WebDir
	}
	return defaultWebDir
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	indexPath := filepath.Join(s.webDir(), "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!doctype html><title>AudioCleaner</title><h1>AudioCleaner</h1><p>WebUI dist is missing. Build the frontend and restart the container.</p>`))
		return
	}
	http.ServeFile(w, r, indexPath)
}

func (s *Server) handleAssets() http.Handler {
	return http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(s.webDir(), "assets"))))
}
