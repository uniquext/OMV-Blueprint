package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omv-blueprint/compose/audiocleaner/internal/config"
	"omv-blueprint/compose/audiocleaner/internal/pipeline"
	"omv-blueprint/compose/audiocleaner/internal/repository"
)

type ScanSource string
type ScanStatus string

const (
	ScanSourceManual         ScanSource = "manual"
	ScanSourceStartup        ScanSource = "startup"
	ScanSourceReconciliation ScanSource = "reconciliation"
	ScanSourceRecovery       ScanSource = "recovery"

	ScanStatusIdle        ScanStatus = "idle"
	ScanStatusRunning     ScanStatus = "running"
	ScanStatusReconciling ScanStatus = "reconciling"
	ScanStatusCancelling  ScanStatus = "cancelling"
	ScanStatusCompleted   ScanStatus = "completed"
	ScanStatusCancelled   ScanStatus = "cancelled"
	ScanStatusFailed      ScanStatus = "failed"
)

type ScanSession struct {
	ID               string     `json:"scan_id"`
	Source           ScanSource `json:"source"`
	Status           ScanStatus `json:"status"`
	StartedAt        time.Time  `json:"started_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	CurrentRoot      string     `json:"current_root"`
	CurrentDirectory string     `json:"current_directory"`
	ProgressPercent  *float64   `json:"progress_percent,omitempty"`
	Estimating       bool       `json:"estimating"`
	RatePerSecond    float64    `json:"rate_per_second"`
	ETASeconds       *int64     `json:"eta_seconds,omitempty"`
	Visited          int64      `json:"visited"`
	Discovered       int64      `json:"discovered"`
	Skipped          int64      `json:"skipped"`
	Enqueued         int64      `json:"enqueued"`
	Failed           int64      `json:"failed"`
	Merged           int64      `json:"merged"`
	Missing          int64      `json:"missing"`
	Added            int64      `json:"added"`
	Modified         int64      `json:"modified"`
	LastMergedPath   string     `json:"last_merged_path,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	estimatedTotal   int64
}

type ScanStartResult struct {
	Scan   ScanSession `json:"scan"`
	Reused bool        `json:"reused"`
}

type ReconciliationResult struct {
	CompletedAt time.Time `json:"completed_at"`
	Added       int64     `json:"added"`
	Modified    int64     `json:"modified"`
	Deleted     int64     `json:"deleted"`
}

type RecoveryResult struct {
	DetectedAt  time.Time  `json:"detected_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Reason      string     `json:"reason"`
	Status      string     `json:"status"`
}

type DiscoveryStatus struct {
	Status                        string                `json:"status"`
	WatcherEnabled                bool                  `json:"watcher_enabled"`
	WatcherStatus                 string                `json:"watcher_status"`
	WatcherError                  string                `json:"watcher_error,omitempty"`
	WatchedDirectories            int                   `json:"watched_directories"`
	ReconciliationIntervalMinutes int                   `json:"reconciliation_interval_minutes"`
	NextReconciliationAt          *time.Time            `json:"next_reconciliation_at,omitempty"`
	LastReconciliation            *ReconciliationResult `json:"last_reconciliation,omitempty"`
	Recovery                      *RecoveryResult       `json:"recovery,omitempty"`
}

type watcherRecoverySource interface {
	RecoveryRequests() <-chan string
}

type watcherDirectoryCounter interface {
	DirectoryCount() int
}

func (s *Service) StartScan(_ context.Context) (any, error) {
	scan, created, err := s.startManagedScan(ScanSourceManual, false)
	if err != nil {
		return nil, err
	}
	return ScanStartResult{Scan: scan, Reused: !created}, nil
}

func (s *Service) CurrentScan(_ context.Context) (any, error) {
	s.scanMu.RLock()
	defer s.scanMu.RUnlock()
	if s.scanHasCurrent {
		return cloneScanSession(s.scanCurrent), nil
	}
	return ScanSession{Status: ScanStatusIdle}, nil
}

func (s *Service) CancelScan(_ context.Context, id string) (any, error) {
	s.scanMu.Lock()
	if !s.scanHasCurrent || s.scanCurrent.ID != id || !scanStatusActive(s.scanCurrent.Status) {
		s.scanMu.Unlock()
		return nil, sql.ErrNoRows
	}
	if s.scanCurrent.Status != ScanStatusCancelling {
		s.scanCurrent.Status = ScanStatusCancelling
		s.scanCurrent.UpdatedAt = time.Now()
		s.scanCurrent.ETASeconds = nil
	}
	cancel := s.scanCancel
	snapshot := cloneScanSession(s.scanCurrent)
	s.scanMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.logf("scan cancellation requested scan_id=%s source=%s", snapshot.ID, snapshot.Source)
	s.publishScanEvent("scan.cancel_requested", snapshot)
	return snapshot, nil
}

func (s *Service) cancelManagedScanForShutdown() {
	s.scanMu.Lock()
	cancel := s.scanCancel
	if s.scanHasCurrent && scanStatusActive(s.scanCurrent.Status) {
		s.scanCurrent.Status = ScanStatusCancelling
		s.scanCurrent.UpdatedAt = time.Now()
	}
	s.scanMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) DiscoveryStatus(_ context.Context) (any, error) {
	cfg := s.config()
	s.scanMu.RLock()
	watcherStatus := s.discoveryWatcherStatus
	watcherError := s.discoveryWatcherError
	next := s.discoveryNextAt
	last := cloneReconciliationResult(s.discoveryLastResult)
	recovery := cloneRecoveryResult(s.discoveryRecovery)
	s.scanMu.RUnlock()

	watchedDirectories := 0
	s.watcherMu.Lock()
	watcher := s.watcher
	s.watcherMu.Unlock()
	if counter, ok := watcher.(watcherDirectoryCounter); ok {
		watchedDirectories = counter.DirectoryCount()
	}
	status := "normal"
	if watcherStatus == "error" {
		status = "degraded"
	} else if !cfg.Scan.WatchdogEnabled {
		status = "limited"
	}
	var nextPtr *time.Time
	if !next.IsZero() {
		value := next
		nextPtr = &value
	}
	return DiscoveryStatus{
		Status:                        status,
		WatcherEnabled:                cfg.Scan.WatchdogEnabled,
		WatcherStatus:                 watcherStatus,
		WatcherError:                  watcherError,
		WatchedDirectories:            watchedDirectories,
		ReconciliationIntervalMinutes: cfg.Scan.ReconciliationIntervalMins,
		NextReconciliationAt:          nextPtr,
		LastReconciliation:            last,
		Recovery:                      recovery,
	}, nil
}

func (s *Service) startManagedScan(source ScanSource, queueIfBusy bool) (ScanSession, bool, error) {
	now := time.Now()
	s.scanMu.Lock()
	if s.scanHasCurrent && scanStatusActive(s.scanCurrent.Status) {
		if queueIfBusy {
			s.queuePendingScanLocked(source)
		}
		snapshot := cloneScanSession(s.scanCurrent)
		s.scanMu.Unlock()
		s.logf("scan reused scan_id=%s source=%s requested_source=%s", snapshot.ID, snapshot.Source, source)
		return snapshot, false, nil
	}
	if !s.accepting.Load() || s.workerCtx.Err() != nil {
		s.scanMu.Unlock()
		return ScanSession{}, false, errors.New("scan service is not accepting new work")
	}
	s.scanSequence++
	status := ScanStatusRunning
	prefix := "SCAN"
	if source == ScanSourceReconciliation || source == ScanSourceRecovery {
		status = ScanStatusReconciling
		prefix = "RECON"
	}
	if source == ScanSourceRecovery {
		prefix = "RECOV"
	}
	cfg := s.config()
	currentRoot := ""
	if len(cfg.Media.Roots) > 0 {
		currentRoot = cfg.Media.Roots[0]
	}
	ctx, cancel := context.WithCancel(s.workerCtx)
	s.scanCurrent = ScanSession{
		ID:             fmt.Sprintf("%s-%s-%04d", prefix, now.Format("20060102"), s.scanSequence),
		Source:         source,
		Status:         status,
		StartedAt:      now,
		UpdatedAt:      now,
		CurrentRoot:    currentRoot,
		Estimating:     s.lastScanVisited == 0,
		estimatedTotal: s.lastScanVisited,
	}
	s.scanHasCurrent = true
	s.scanCancel = cancel
	if source == ScanSourceRecovery {
		if s.discoveryRecovery == nil {
			s.discoveryRecovery = &RecoveryResult{DetectedAt: now, Reason: "watcher_error"}
		}
		s.discoveryRecovery.Status = "running"
	}
	snapshot := cloneScanSession(s.scanCurrent)
	s.scanMu.Unlock()

	s.logf("scan started scan_id=%s source=%s", snapshot.ID, snapshot.Source)
	s.publishScanEvent("scan.started", snapshot)
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		s.runManagedScan(ctx, snapshot.ID, cfg)
	}()
	return snapshot, true, nil
}

func (s *Service) runManagedScan(ctx context.Context, id string, cfg config.Config) {
	s.runScanWithConfig(ctx, id, pipeline.ScanConfig{
		Roots:           cfg.Media.Roots,
		Extensions:      cfg.Media.Extensions,
		ExcludeDirs:     cfg.Media.ExcludeDirs,
		ExcludePatterns: cfg.Media.ExcludePatterns,
	}, cfg.Audio.Version)
}

func (s *Service) runScanWithConfig(ctx context.Context, id string, cfg pipeline.ScanConfig, audioPolicyVersion int) {
	knownFiles, err := s.db.Files(ctx)
	if err != nil {
		s.finishManagedScan(id, err, 0, 0, 0)
		return
	}
	known := make(map[string]repository.FileFacts, len(knownFiles))
	for _, file := range knownFiles {
		known[file.Path] = file
	}
	seen := make(map[string]struct{}, len(knownFiles))
	var added int64
	var modified int64

	err = pipeline.StreamScan(ctx, cfg, pipeline.ScanCallbacks{
		OnEntry: func(root, path string, entry fs.DirEntry) {
			directory := path
			if !entry.IsDir() {
				directory = filepath.Dir(path)
			}
			s.updateScan(id, func(scan *ScanSession) {
				scan.Visited++
				scan.CurrentRoot = root
				scan.CurrentDirectory = directory
			})
		},
		OnCandidate: func(_ string, path string, info fs.FileInfo) {
			s.updateScan(id, func(scan *ScanSession) { scan.Discovered++ })
			if _, duplicate := seen[path]; duplicate {
				s.updateScan(id, func(scan *ScanSession) {
					scan.Merged++
					scan.LastMergedPath = path
				})
				return
			}
			seen[path] = struct{}{}
			persisted, exists := known[path]
			if exists && persisted.BackupFile != "" {
				s.updateScan(id, func(scan *ScanSession) { scan.Skipped++ })
				return
			}
			if exists && persisted.Size == info.Size() && persisted.MTimeNS == info.ModTime().UnixNano() &&
				persisted.AudioPolicyVersion == audioPolicyVersion && persisted.ComplianceStatus != repository.ComplianceUnknown {
				s.updateScan(id, func(scan *ScanSession) { scan.Skipped++ })
				return
			}
			if !exists {
				added++
			} else if persisted.Size != info.Size() || persisted.MTimeNS != info.ModTime().UnixNano() {
				modified++
			}
			if s.queue != nil && s.queue.Contains(path) {
				s.updateScan(id, func(scan *ScanSession) {
					scan.Merged++
					scan.LastMergedPath = path
				})
				return
			}
			if s.enqueueWithSource(path, pipeline.JobSourceScan) {
				s.updateScan(id, func(scan *ScanSession) { scan.Enqueued++ })
				return
			}
			if ctx.Err() == nil {
				s.updateScan(id, func(scan *ScanSession) {
					scan.Merged++
					scan.LastMergedPath = path
				})
			}
		},
		OnError: func(path string, scanErr error) {
			s.updateScan(id, func(scan *ScanSession) {
				scan.Failed++
				scan.LastError = fmt.Sprintf("%s: %v", path, scanErr)
			})
		},
	})

	missing := int64(0)
	if err == nil && ctx.Err() == nil {
		for _, file := range knownFiles {
			if _, ok := seen[file.Path]; ok || !pathWithinRoots(file.Path, cfg.Roots) ||
				!isCandidatePathForMedia(file.Path, mediaConfigFromScan(cfg)) {
				continue
			}
			if _, statErr := os.Stat(file.Path); errors.Is(statErr, os.ErrNotExist) {
				missing++
			}
		}
	}
	s.finishManagedScan(id, err, added, modified, missing)
}

func (s *Service) finishManagedScan(id string, scanErr error, added, modified, missing int64) {
	now := time.Now()
	s.scanMu.Lock()
	if !s.scanHasCurrent || s.scanCurrent.ID != id {
		s.scanMu.Unlock()
		return
	}
	s.scanCurrent.Added = added
	s.scanCurrent.Modified = modified
	s.scanCurrent.Missing = missing
	s.scanCurrent.UpdatedAt = now
	s.scanCurrent.FinishedAt = &now
	s.scanCurrent.RatePerSecond = scanRate(s.scanCurrent, now)
	s.scanCurrent.ETASeconds = int64Pointer(0)
	s.scanCurrent.Estimating = false
	progress := 100.0
	s.scanCurrent.ProgressPercent = &progress
	if errors.Is(scanErr, context.Canceled) || s.scanCurrent.Status == ScanStatusCancelling {
		s.scanCurrent.Status = ScanStatusCancelled
		s.scanCurrent.ProgressPercent = activeProgress(s.scanCurrent)
		s.scanCurrent.ETASeconds = nil
	} else if scanErr != nil {
		s.scanCurrent.Status = ScanStatusFailed
		s.scanCurrent.LastError = scanErr.Error()
		s.scanCurrent.ProgressPercent = activeProgress(s.scanCurrent)
		s.scanCurrent.ETASeconds = nil
	} else {
		s.scanCurrent.Status = ScanStatusCompleted
		if s.scanCurrent.Visited > 0 {
			s.lastScanVisited = s.scanCurrent.Visited
		}
	}
	if (s.scanCurrent.Source == ScanSourceReconciliation || s.scanCurrent.Source == ScanSourceRecovery) && scanErr == nil {
		result := &ReconciliationResult{CompletedAt: now, Added: added, Modified: modified, Deleted: missing}
		s.discoveryLastResult = result
		if s.scanCurrent.Source == ScanSourceRecovery && s.discoveryRecovery != nil {
			s.discoveryRecovery.Status = "completed"
			s.discoveryRecovery.CompletedAt = &now
			s.discoveryWatcherStatus = "running"
			s.discoveryWatcherError = ""
		}
	}
	s.scanCancel = nil
	snapshot := cloneScanSession(s.scanCurrent)
	pending := s.pendingScanSource
	s.pendingScanSource = ""
	s.scanMu.Unlock()

	s.logf(
		"scan finished scan_id=%s source=%s status=%s visited=%d discovered=%d skipped=%d enqueued=%d failed=%d merged=%d missing=%d",
		snapshot.ID, snapshot.Source, snapshot.Status, snapshot.Visited, snapshot.Discovered, snapshot.Skipped,
		snapshot.Enqueued, snapshot.Failed, snapshot.Merged, snapshot.Missing,
	)
	eventType := "scan.completed"
	if snapshot.Status == ScanStatusCancelled {
		eventType = "scan.cancelled"
	} else if snapshot.Status == ScanStatusFailed {
		eventType = "scan.failed"
	}
	s.publishScanEvent(eventType, snapshot)
	if pending != "" && s.accepting.Load() && s.workerCtx.Err() == nil {
		_, _, _ = s.startManagedScan(pending, true)
	}
}

func (s *Service) updateScan(id string, update func(*ScanSession)) {
	now := time.Now()
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if !s.scanHasCurrent || s.scanCurrent.ID != id || !scanStatusActive(s.scanCurrent.Status) {
		return
	}
	update(&s.scanCurrent)
	s.scanCurrent.UpdatedAt = now
	s.scanCurrent.RatePerSecond = scanRate(s.scanCurrent, now)
	s.scanCurrent.ProgressPercent = activeProgress(s.scanCurrent)
	s.scanCurrent.Estimating = s.scanCurrent.estimatedTotal == 0
	if s.scanCurrent.estimatedTotal > s.scanCurrent.Visited && s.scanCurrent.RatePerSecond > 0 {
		remaining := int64(float64(s.scanCurrent.estimatedTotal-s.scanCurrent.Visited) / s.scanCurrent.RatePerSecond)
		s.scanCurrent.ETASeconds = &remaining
	} else {
		s.scanCurrent.ETASeconds = nil
	}
}

func (s *Service) startReconciliationLoop() {
	interval := time.Duration(s.config().Scan.ReconciliationIntervalMins) * time.Minute
	if interval <= 0 {
		return
	}
	s.scanMu.Lock()
	s.discoveryNextAt = time.Now().Add(interval)
	s.scanMu.Unlock()
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-s.workerCtx.Done():
				return
			case <-timer.C:
				_, _, _ = s.startManagedScan(ScanSourceReconciliation, true)
				next := time.Now().Add(interval)
				s.scanMu.Lock()
				s.discoveryNextAt = next
				s.scanMu.Unlock()
				timer.Reset(interval)
			}
		}
	}()
}

func (s *Service) monitorWatcher(watcher runtimeWatcher) {
	defer s.workers.Done()
	errorsCh := watcher.Errors()
	var recoveriesCh <-chan string
	if source, ok := watcher.(watcherRecoverySource); ok {
		recoveriesCh = source.RecoveryRequests()
	}
	for errorsCh != nil || recoveriesCh != nil {
		select {
		case <-s.workerCtx.Done():
			return
		case err, ok := <-errorsCh:
			if !ok {
				errorsCh = nil
				continue
			}
			s.logf("watcher error: %v", err)
			s.markWatcherError(err)
			s.requestRecoveryScan(err.Error())
		case _, ok := <-recoveriesCh:
			if !ok {
				recoveriesCh = nil
				continue
			}
			_, _, _ = s.startManagedScan(ScanSourceReconciliation, true)
		}
	}
}

func (s *Service) requestRecoveryScan(reason string) {
	now := time.Now()
	s.scanMu.Lock()
	s.discoveryRecovery = &RecoveryResult{DetectedAt: now, Reason: reason, Status: "pending"}
	s.scanMu.Unlock()
	_, _, _ = s.startManagedScan(ScanSourceRecovery, true)
}

func (s *Service) markWatcherRunning() {
	s.scanMu.Lock()
	s.discoveryWatcherStatus = "running"
	s.discoveryWatcherError = ""
	s.scanMu.Unlock()
}

func (s *Service) markWatcherDisabled() {
	s.scanMu.Lock()
	s.discoveryWatcherStatus = "disabled"
	s.discoveryWatcherError = ""
	s.scanMu.Unlock()
}

func (s *Service) markWatcherError(err error) {
	s.scanMu.Lock()
	s.discoveryWatcherStatus = "error"
	if err != nil {
		s.discoveryWatcherError = err.Error()
	}
	s.scanMu.Unlock()
}

func (s *Service) queuePendingScanLocked(source ScanSource) {
	if source == ScanSourceRecovery || s.pendingScanSource == "" {
		s.pendingScanSource = source
	}
}

func (s *Service) publishScanEvent(eventType string, scan ScanSession) {
	if s.events != nil {
		s.events.Publish(eventType, map[string]any{
			"scan_id": scan.ID,
			"source":  scan.Source,
			"status":  scan.Status,
		})
	}
}

func scanStatusActive(status ScanStatus) bool {
	return status == ScanStatusRunning || status == ScanStatusReconciling || status == ScanStatusCancelling
}

func scanRate(scan ScanSession, now time.Time) float64 {
	elapsed := now.Sub(scan.StartedAt).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(scan.Visited) / elapsed
}

func activeProgress(scan ScanSession) *float64 {
	if scan.estimatedTotal <= 0 {
		return nil
	}
	value := float64(scan.Visited) / float64(scan.estimatedTotal) * 100
	if value > 99 {
		value = 99
	}
	if value < 0 {
		value = 0
	}
	return &value
}

func cloneScanSession(scan ScanSession) ScanSession {
	clone := scan
	if scan.ProgressPercent != nil {
		value := *scan.ProgressPercent
		clone.ProgressPercent = &value
	}
	if scan.ETASeconds != nil {
		value := *scan.ETASeconds
		clone.ETASeconds = &value
	}
	if scan.FinishedAt != nil {
		value := *scan.FinishedAt
		clone.FinishedAt = &value
	}
	return clone
}

func cloneReconciliationResult(result *ReconciliationResult) *ReconciliationResult {
	if result == nil {
		return nil
	}
	clone := *result
	return &clone
}

func cloneRecoveryResult(result *RecoveryResult) *RecoveryResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.CompletedAt != nil {
		value := *result.CompletedAt
		clone.CompletedAt = &value
	}
	return &clone
}

func int64Pointer(value int64) *int64 {
	return &value
}

func pathWithinRoots(path string, roots []string) bool {
	cleanPath := filepath.Clean(path)
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		relative, err := filepath.Rel(cleanRoot, cleanPath)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func mediaConfigFromScan(cfg pipeline.ScanConfig) config.MediaConfig {
	return config.MediaConfig{
		Roots:           cfg.Roots,
		Extensions:      cfg.Extensions,
		ExcludeDirs:     cfg.ExcludeDirs,
		ExcludePatterns: cfg.ExcludePatterns,
	}
}
