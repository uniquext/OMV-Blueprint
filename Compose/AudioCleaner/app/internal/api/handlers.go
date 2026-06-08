package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"omv-blueprint/compose/audiocleaner/internal/config"
	"omv-blueprint/compose/audiocleaner/internal/eventbus"
	"omv-blueprint/compose/audiocleaner/internal/repository"
)

var (
	ErrProcessing            = errors.New("file is currently processing")
	ErrInvalidJobState       = errors.New("invalid job state")
	ErrConfigWriteInProgress = errors.New("config write already in progress")
)

type invalidConfigError struct {
	err error
}

func (e invalidConfigError) Error() string {
	return e.err.Error()
}

func (e invalidConfigError) Unwrap() error {
	return e.err
}

const (
	scanRequestBodyLimit   int64 = 1024
	configRequestBodyLimit int64 = 1024 * 1024
)

type Deps struct {
	Config     config.Config
	ConfigPath string
	WebDir     string
	Events     *eventbus.Bus
	EventBus   *eventbus.Bus

	ScanAll        func(context.Context) error
	RequestRestart func(config.Config)

	Status  StatusProvider
	Jobs    JobStore
	Backups BackupStore
	Logs    LogStore
}

type StatusProvider interface {
	Status(ctx context.Context) (any, error)
}

type JobStore interface {
	ListJobs(ctx context.Context, page *repository.PageRequest) (any, error)
	JobByID(ctx context.Context, id int64) (any, error)
	RetryJob(ctx context.Context, id int64) error
	IgnoreJob(ctx context.Context, id int64) error
}

type BackupStore interface {
	ListBackups(ctx context.Context, page *repository.PageRequest) (any, error)
	RestoreBackup(ctx context.Context, id int64) (any, error)
	CleanupBackups(ctx context.Context) (any, error)
}

type LogStore interface {
	RecentLogs(ctx context.Context) (any, error)
}

type patchUIConfigRequest struct {
	Language *string `json:"language"`
}

type patchBackupConfigRequest struct {
	RetentionDays *int `json:"retention_days"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeOK(w, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.Status == nil {
		writeOK(w, map[string]string{"status": "ok"})
		return
	}
	status, err := s.deps.Status.Status(r.Context())
	writeDependencyResult(w, status, err)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if s.deps.Jobs == nil {
		writeOK(w, []repository.FileRecord{})
		return
	}
	page, err := parsePageRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	jobs, err := s.deps.Jobs.ListJobs(r.Context(), page)
	writeDependencyResult(w, jobs, err)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if s.deps.Jobs == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	job, err := s.deps.Jobs.JobByID(r.Context(), id)
	writeDependencyResult(w, job, err)
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if !scanPayloadAllowed(w, r) {
		return
	}

	if s.deps.ScanAll != nil {
		if err := s.deps.ScanAll(r.Context()); err != nil {
			writeDependencyResult(w, nil, err)
			return
		}
		writeOK(w, map[string]string{"status": "queued"})
		return
	}

	writeOK(w, map[string]string{"status": "queued"})
}

func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if s.deps.Jobs == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	err := s.deps.Jobs.RetryJob(r.Context(), id)
	writeDependencyResult(w, map[string]int64{"id": id}, err)
}

func (s *Server) handleIgnoreJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if s.deps.Jobs == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	err := s.deps.Jobs.IgnoreJob(r.Context(), id)
	writeDependencyResult(w, map[string]int64{"id": id}, err)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	if len(cfg.Media.Roots) == 0 {
		cfg = config.Default()
	}
	writeOK(w, cfg)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	cfg := config.Default()
	body := http.MaxBytesReader(w, r.Body, configRequestBodyLimit)
	if err := json.NewDecoder(body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid config JSON")
		return
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.updateConfig(cfg); err != nil {
		writeDependencyResult(w, nil, err)
		return
	}
	if s.deps.RequestRestart != nil {
		writeJSON(w, http.StatusOK, Response{
			Code:    0,
			Message: "restarting",
			Data:    map[string]string{"status": "restarting"},
		})
		_ = http.NewResponseController(w).Flush()
		go s.deps.RequestRestart(cfg)
		return
	}
	writeOK(w, cfg)
}

func (s *Server) handlePatchUIConfig(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload patchUIConfigRequest
	body := http.MaxBytesReader(w, r.Body, configRequestBodyLimit)
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid config JSON")
		return
	}
	if payload.Language == nil {
		writeError(w, http.StatusBadRequest, "ui language is required")
		return
	}
	if *payload.Language == "" {
		writeError(w, http.StatusBadRequest, "ui language must not be empty")
		return
	}
	cfg, err := s.updateConfigBlock(func(cfg *config.Config) error {
		cfg.UI.Language = *payload.Language
		return nil
	})
	if err != nil {
		writeDependencyResult(w, nil, err)
		return
	}
	writeOK(w, cfg.UI)
}

func (s *Server) handlePatchBackupConfig(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload patchBackupConfigRequest
	body := http.MaxBytesReader(w, r.Body, configRequestBodyLimit)
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid config JSON")
		return
	}
	if payload.RetentionDays == nil {
		writeError(w, http.StatusBadRequest, "backup retention days is required")
		return
	}
	cfg, err := s.updateConfigBlock(func(cfg *config.Config) error {
		cfg.Backup.RetentionDays = *payload.RetentionDays
		return nil
	})
	if err != nil {
		writeDependencyResult(w, nil, err)
		return
	}
	if s.deps.RequestRestart != nil {
		writeJSON(w, http.StatusOK, Response{
			Code:    0,
			Message: "restarting",
			Data:    map[string]string{"status": "restarting"},
		})
		_ = http.NewResponseController(w).Flush()
		go s.deps.RequestRestart(cfg)
		return
	}
	writeOK(w, cfg.Backup)
}

func (s *Server) handleRecentLogs(w http.ResponseWriter, r *http.Request) {
	if s.deps.Logs == nil {
		writeOK(w, []string{})
		return
	}
	logs, err := s.deps.Logs.RecentLogs(r.Context())
	writeDependencyResult(w, logs, err)
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	if s.deps.Backups == nil {
		writeOK(w, []repository.BackupRecord{})
		return
	}
	page, err := parsePageRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	backups, err := s.deps.Backups.ListBackups(r.Context(), page)
	writeDependencyResult(w, backups, err)
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if s.deps.Backups == nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	result, err := s.deps.Backups.RestoreBackup(r.Context(), id)
	writeDependencyResult(w, result, err)
}

func (s *Server) handleCleanupBackups(w http.ResponseWriter, r *http.Request) {
	if s.deps.Backups == nil {
		writeOK(w, map[string]int{"removed": 0})
		return
	}
	result, err := s.deps.Backups.CleanupBackups(r.Context())
	writeDependencyResult(w, result, err)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	bus := s.deps.Events
	if bus == nil {
		bus = s.deps.EventBus
	}
	StreamEvents(w, r, bus)
}

func writeDependencyResult(w http.ResponseWriter, data any, err error) {
	if err == nil {
		writeOK(w, data)
		return
	}
	if errors.Is(err, ErrProcessing) {
		writeError(w, http.StatusConflict, ErrProcessing.Error())
		return
	}
	if errors.Is(err, ErrConfigWriteInProgress) {
		writeError(w, http.StatusConflict, ErrConfigWriteInProgress.Error())
		return
	}
	if errors.Is(err, ErrInvalidJobState) {
		writeError(w, http.StatusBadRequest, ErrInvalidJobState.Error())
		return
	}
	var invalidConfig invalidConfigError
	if errors.As(err, &invalidConfig) {
		writeError(w, http.StatusBadRequest, invalidConfig.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid id %q", value))
		return 0, false
	}
	return id, true
}

func parsePageRequest(r *http.Request) (*repository.PageRequest, error) {
	query := r.URL.Query()
	hasPage := query.Has("page")
	hasPageSize := query.Has("page_size")
	if !hasPage && !hasPageSize {
		return nil, nil
	}
	page := 1
	pageSize := 20
	if hasPage {
		parsed, err := strconv.Atoi(query.Get("page"))
		if err != nil {
			return nil, fmt.Errorf("invalid page %q", query.Get("page"))
		}
		page = parsed
	}
	if hasPageSize {
		parsed, err := strconv.Atoi(query.Get("page_size"))
		if err != nil {
			return nil, fmt.Errorf("invalid page_size %q", query.Get("page_size"))
		}
		pageSize = parsed
	}
	req := repository.PageRequest{Page: page, PageSize: pageSize}.Normalize()
	return &req, nil
}

func scanPayloadAllowed(w http.ResponseWriter, r *http.Request) bool {
	defer r.Body.Close()

	body := http.MaxBytesReader(w, r.Body, scanRequestBodyLimit)
	data, err := io.ReadAll(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	if strings.TrimSpace(string(data)) == "" {
		return true
	}

	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	if containsPathLikeField(payload) {
		writeError(w, http.StatusBadRequest, "scan path overrides are not allowed")
		return false
	}
	return true
}

func containsPathLikeField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if isPathLikeKey(key) || containsPathLikeField(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsPathLikeField(nested) {
				return true
			}
		}
	}
	return false
}

func isPathLikeKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
	switch normalized {
	case "path", "paths", "root", "roots", "mediaroot", "mediaroots":
		return true
	default:
		return strings.HasSuffix(normalized, "path") ||
			strings.HasSuffix(normalized, "paths") ||
			strings.HasSuffix(normalized, "root") ||
			strings.HasSuffix(normalized, "roots")
	}
}

func (s *Server) config() config.Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.deps.Config
}

func (s *Server) updateConfig(cfg config.Config) error {
	if !s.configWriteMu.TryLock() {
		return ErrConfigWriteInProgress
	}
	defer s.configWriteMu.Unlock()

	s.configMu.Lock()
	defer s.configMu.Unlock()

	if s.deps.ConfigPath != "" {
		if err := config.AtomicWriteJSON(s.deps.ConfigPath, cfg); err != nil {
			return err
		}
	}
	s.deps.Config = cfg
	return nil
}

func (s *Server) updateConfigBlock(mutate func(*config.Config) error) (config.Config, error) {
	if !s.configWriteMu.TryLock() {
		return config.Config{}, ErrConfigWriteInProgress
	}
	defer s.configWriteMu.Unlock()

	s.configMu.Lock()
	defer s.configMu.Unlock()

	cfg := s.deps.Config
	if err := mutate(&cfg); err != nil {
		return config.Config{}, err
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return config.Config{}, invalidConfigError{err: err}
	}
	if s.deps.ConfigPath != "" {
		if err := config.AtomicWriteJSON(s.deps.ConfigPath, cfg); err != nil {
			return config.Config{}, err
		}
	}
	s.deps.Config = cfg
	return cfg, nil
}
