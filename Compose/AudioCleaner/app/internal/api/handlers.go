package api

import (
	"context"
	"database/sql"
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
	"omv-blueprint/compose/audiocleaner/internal/settings"
)

var (
	ErrProcessing         = errors.New("file is currently processing")
	ErrFileNotProcessable = errors.New("file is not eligible for processing")
	ErrJobNotActionable   = errors.New("job is not eligible for this operation")
)

const (
	scanRequestBodyLimit   int64 = 1024
	configRequestBodyLimit int64 = 1024 * 1024
)

type Deps struct {
	Config     config.Config
	ConfigPath string
	WebDir     string
	MediaRoot  string
	Logf       func(string, ...any)
	Events     *eventbus.Bus
	EventBus   *eventbus.Bus

	ScanAll          func(context.Context) error
	RequestRestart   func(config.Config, config.Config) error
	ConfigApplicator settings.Applicator
	Scans            ScanController
	Discovery        DiscoveryStatusProvider

	Status       StatusProvider
	RuntimeTasks RuntimeTaskProvider
	History      HistoryStore
	Files        FilesStore
	RuntimeLogs  RuntimeLogStore
}

type StatusProvider interface {
	Status(ctx context.Context) (any, error)
}

type RuntimeTaskProvider interface {
	RuntimeTasks(ctx context.Context) (any, error)
}

type ScanController interface {
	StartScan(context.Context) (any, error)
	CurrentScan(context.Context) (any, error)
	CancelScan(context.Context, string) (any, error)
}

type DiscoveryStatusProvider interface {
	DiscoveryStatus(context.Context) (any, error)
}

type HistoryStore interface {
	ListHistory(ctx context.Context, page *repository.PageRequest) (any, error)
	RetryHistoryJob(ctx context.Context, id int64) (any, error)
	IgnoreHistoryJob(ctx context.Context, id int64) (any, error)
}

type FilesStore interface {
	ListFiles(ctx context.Context, page *repository.PageRequest) (any, error)
	ProcessFile(ctx context.Context, id int64) (any, error)
	RestoreFileBackup(ctx context.Context, id int64) (any, error)
	DeleteFileBackup(ctx context.Context, id int64) (any, error)
}

type RuntimeLogStore interface {
	RuntimeLogs(ctx context.Context, lines int) (any, error)
}

type RuntimeLogResult struct {
	Content string `json:"content"`
	Lines   int    `json:"lines"`
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

func (s *Server) handleRuntimeTasks(w http.ResponseWriter, r *http.Request) {
	if s.deps.RuntimeTasks == nil {
		writeOK(w, map[string]any{"waiting_tasks": []any{}, "active_tasks": []any{}})
		return
	}
	tasks, err := s.deps.RuntimeTasks.RuntimeTasks(r.Context())
	writeDependencyResult(w, tasks, err)
}

func (s *Server) handleListHistory(w http.ResponseWriter, r *http.Request) {
	if s.deps.History == nil {
		writeOK(w, []repository.JobHistoryRecord{})
		return
	}
	page, err := parsePageRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	history, err := s.deps.History.ListHistory(r.Context(), page)
	writeDependencyResult(w, history, err)
}

func (s *Server) handleRetryHistoryJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "job_id")
	if !ok {
		return
	}
	if s.deps.History == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	result, err := s.deps.History.RetryHistoryJob(r.Context(), id)
	writeDependencyResult(w, result, err)
}

func (s *Server) handleIgnoreHistoryJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "job_id")
	if !ok {
		return
	}
	if s.deps.History == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	result, err := s.deps.History.IgnoreHistoryJob(r.Context(), id)
	writeDependencyResult(w, result, err)
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

func (s *Server) handleCreateScan(w http.ResponseWriter, r *http.Request) {
	if !scanPayloadAllowed(w, r) {
		return
	}
	if s.deps.Scans == nil {
		writeOK(w, map[string]any{"scan": map[string]string{"status": "idle"}, "reused": false})
		return
	}
	result, err := s.deps.Scans.StartScan(r.Context())
	writeDependencyResult(w, result, err)
}

func (s *Server) handleCurrentScan(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scans == nil {
		writeOK(w, map[string]string{"status": "idle"})
		return
	}
	result, err := s.deps.Scans.CurrentScan(r.Context())
	writeDependencyResult(w, result, err)
}

func (s *Server) handleCancelScan(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scans == nil {
		writeError(w, http.StatusNotFound, "scan not found")
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "scan_id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "scan id is required")
		return
	}
	result, err := s.deps.Scans.CancelScan(r.Context(), id)
	writeDependencyResult(w, result, err)
}

func (s *Server) handleDiscoveryStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.Discovery == nil {
		writeOK(w, map[string]any{"status": "limited", "watcher_status": "disabled"})
		return
	}
	result, err := s.deps.Discovery.DiscoveryStatus(r.Context())
	writeDependencyResult(w, result, err)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeOK(w, s.settings.View())
}

func (s *Server) handleGetConfigDefaults(w http.ResponseWriter, r *http.Request) {
	writeOK(w, s.settings.Defaults())
}

func (s *Server) handlePatchConfig(module settings.Module) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		expectedRevision := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
		if expectedRevision == "" {
			writeCodedError(w, http.StatusPreconditionRequired, "revision_required", "If-Match module revision is required", map[string]any{"module": module})
			return
		}
		body := http.MaxBytesReader(w, r.Body, configRequestBodyLimit)
		payload, err := io.ReadAll(body)
		if err != nil {
			writeCodedError(w, http.StatusBadRequest, "invalid_json", "invalid configuration JSON", map[string]any{"module": module})
			return
		}
		result, err := s.settings.Update(r.Context(), module, expectedRevision, payload)
		if err != nil {
			var conflict *settings.ConflictError
			var validation *settings.ValidationError
			var apply *settings.ApplyError
			switch {
			case errors.As(err, &conflict):
				writeCodedError(w, http.StatusConflict, "config_changed", conflict.Error(), conflict)
			case errors.As(err, &validation):
				writeCodedError(w, http.StatusBadRequest, "validation_failed", validation.Error(), validation)
			case errors.As(err, &apply):
				writeCodedError(w, http.StatusInternalServerError, "apply_failed", apply.Error(), map[string]any{"module": module, "status": "apply_failed"})
			default:
				writeCodedError(w, http.StatusInternalServerError, "config_update_failed", err.Error(), map[string]any{"module": module})
			}
			return
		}
		message := "configuration applied"
		if result.Status == settings.Restarting {
			message = "restarting"
		}
		writeJSON(w, http.StatusOK, Response{Code: 0, Message: message, Data: result})
		_ = http.NewResponseController(w).Flush()
		if result.Changed && result.ApplyMode == settings.ServiceRestart && s.deps.RequestRestart != nil {
			go func() {
				if err := s.deps.RequestRestart(result.BeforeConfig, result.FullConfig); err != nil {
					if restoreErr := s.settings.Restore(result.BeforeConfig); restoreErr != nil && s.deps.Logf != nil {
						s.deps.Logf("restore settings snapshot after restart failure: %v", restoreErr)
					}
				}
			}()
		}
	}
}

func (s *Server) handleRuntimeLogs(w http.ResponseWriter, r *http.Request) {
	if s.deps.RuntimeLogs == nil {
		writeOK(w, RuntimeLogResult{})
		return
	}
	lines, err := parseRuntimeLogLines(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	logs, err := s.deps.RuntimeLogs.RuntimeLogs(r.Context(), lines)
	writeDependencyResult(w, logs, err)
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	if s.deps.Files == nil {
		writeOK(w, []repository.FileFacts{})
		return
	}
	page, err := parsePageRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	files, err := s.deps.Files.ListFiles(r.Context(), page)
	writeDependencyResult(w, files, err)
}

func (s *Server) handleProcessFile(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "file_id")
	if !ok {
		return
	}
	if s.deps.Files == nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	result, err := s.deps.Files.ProcessFile(r.Context(), id)
	writeDependencyResult(w, result, err)
}

func (s *Server) handleRestoreFileBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "file_id")
	if !ok {
		return
	}
	if s.deps.Files == nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	result, err := s.deps.Files.RestoreFileBackup(r.Context(), id)
	writeDependencyResult(w, result, err)
}

func (s *Server) handleDeleteFileBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "file_id")
	if !ok {
		return
	}
	if s.deps.Files == nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	result, err := s.deps.Files.DeleteFileBackup(r.Context(), id)
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
	if errors.Is(err, ErrFileNotProcessable) {
		writeError(w, http.StatusConflict, ErrFileNotProcessable.Error())
		return
	}
	if errors.Is(err, ErrJobNotActionable) {
		writeError(w, http.StatusConflict, ErrJobNotActionable.Error())
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	return parsePathID(w, r, "id")
}

func parsePathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	value := chi.URLParam(r, name)
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

func parseRuntimeLogLines(r *http.Request) (int, error) {
	value := r.URL.Query().Get("lines")
	if value == "" {
		return 100, nil
	}
	lines, err := strconv.Atoi(value)
	if err != nil || lines < 1 {
		return 0, fmt.Errorf("invalid lines %q", value)
	}
	if lines > 2000 {
		lines = 2000
	}
	return lines, nil
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
