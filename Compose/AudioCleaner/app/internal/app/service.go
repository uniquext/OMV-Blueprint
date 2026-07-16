package app

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"omv-blueprint/compose/audiocleaner/internal/api"
	"omv-blueprint/compose/audiocleaner/internal/backup"
	"omv-blueprint/compose/audiocleaner/internal/config"
	"omv-blueprint/compose/audiocleaner/internal/eventbus"
	"omv-blueprint/compose/audiocleaner/internal/media"
	"omv-blueprint/compose/audiocleaner/internal/pipeline"
	"omv-blueprint/compose/audiocleaner/internal/repository"
)

const (
	defaultConfigPath = "/app/config/config.json"
	defaultDBPath     = "/app/data/audiocleaner.db"
	defaultBackupRoot = "/app/backups"
	defaultHTTPAddr   = ":9830"
	defaultLogPath    = "/app/logs/audiocleaner.log"
	defaultWorkRoot   = "/app/work"
	defaultWebDir     = "/app/web/dist"

	defaultBackupCleanupInterval  = time.Hour
	startupCriticalRecoveryPrefix = "startup recovered interrupted critical phase "

	restartRunning int32 = iota
	restartPending
	restartFailed
)

type RecoveryRepository interface {
	ProcessingJobs(ctx context.Context) ([]repository.JobRecord, error)
	FailProcessingJob(ctx context.Context, id int64, finalError string) (repository.FileFacts, repository.JobRecord, error)
}

type Options struct {
	ConfigPath string
	DBPath     string
	LogPath    string
	BackupRoot string
	WorkRoot   string
	WebDir     string
	HTTPAddr   string
	Logger     *log.Logger

	FFmpegName  string
	FFprobeName string
	Prober      pipeline.WorkerProber
	Restart     func() error

	afterHTTPBind func()
	logCloser     func() error
}

type Service struct {
	cfg        config.Config
	cfgMu      sync.RWMutex
	configPath string
	db         *repository.Repository
	events     *eventbus.Bus
	queue      *pipeline.Queue
	watcher    *pipeline.Watcher
	server     *http.Server
	logger     *log.Logger
	logCloser  func() error
	logPath    string

	backupRoot  string
	workRoot    string
	webDir      string
	httpAddr    string
	ffmpegName  string
	ffprobeName string
	prober      pipeline.WorkerProber
	restart     func() error

	restoreProbeTimeout   time.Duration
	restorePersistTimeout time.Duration
	backupCleanupInterval time.Duration

	workerCtx      context.Context
	cancelWorker   context.CancelFunc
	workers        sync.WaitGroup
	workerIntakeMu sync.Mutex

	accepting       atomic.Bool
	stoppingWorkers atomic.Bool
	restartState    atomic.Int32

	pathMutationMu    sync.Mutex
	pathMutationPaths map[string]struct{}
}

type workerJob struct {
	task   pipeline.QueueJob
	ctx    context.Context
	cancel context.CancelFunc
}

func RecoverStartup(ctx context.Context, repo RecoveryRepository) error {
	jobs, err := repo.ProcessingJobs(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if _, _, err := repo.FailProcessingJob(ctx, job.ID, startupCriticalRecoveryPrefix+"processing; manual review required"); err != nil {
			return err
		}
	}
	return nil
}

func Run(ctx context.Context, opts Options) error {
	service, err := Start(ctx, opts)
	if err != nil {
		return err
	}
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return ctx.Err()
}

func Start(ctx context.Context, opts Options) (*Service, error) {
	opts = withDefaults(opts)
	if opts.Logger == nil {
		logger, closeLogger, err := newFileLogger(opts.LogPath, os.Stderr)
		if err != nil {
			return nil, fmt.Errorf("open log file: %w", err)
		}
		opts.Logger = logger
		opts.logCloser = closeLogger
	}
	cfg, err := config.LoadOrCreate(opts.ConfigPath)
	if err != nil {
		closeOptionsLog(opts)
		return nil, fmt.Errorf("load config: %w", err)
	}
	repo, err := repository.Open(ctx, opts.DBPath)
	if err != nil {
		closeOptionsLog(opts)
		return nil, fmt.Errorf("open repository: %w", err)
	}

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	service := &Service{
		cfg:                   cfg,
		configPath:            opts.ConfigPath,
		db:                    repo,
		events:                eventbus.New(),
		queue:                 pipeline.NewQueue(),
		logger:                opts.Logger,
		logCloser:             opts.logCloser,
		logPath:               opts.LogPath,
		backupRoot:            opts.BackupRoot,
		workRoot:              opts.WorkRoot,
		webDir:                opts.WebDir,
		httpAddr:              opts.HTTPAddr,
		ffmpegName:            opts.FFmpegName,
		ffprobeName:           opts.FFprobeName,
		prober:                opts.Prober,
		restart:               opts.Restart,
		backupCleanupInterval: defaultBackupCleanupInterval,
		workerCtx:             workerCtx,
		cancelWorker:          cancelWorker,
		pathMutationPaths:     make(map[string]struct{}),
	}
	service.accepting.Store(true)
	service.restartState.Store(restartRunning)

	listener, err := service.prepareHTTPServer()
	if err != nil {
		_ = repo.Close()
		closeOptionsLog(opts)
		cancelWorker()
		return nil, fmt.Errorf("start http server: %w", err)
	}
	if opts.afterHTTPBind != nil {
		opts.afterHTTPBind()
	}
	if err := RecoverStartup(ctx, repo); err != nil {
		_ = listener.Close()
		_ = repo.Close()
		closeOptionsLog(opts)
		cancelWorker()
		return nil, fmt.Errorf("recover startup jobs: %w", err)
	}
	if err := service.cleanupOrphanTempOutputs(ctx); err != nil {
		service.logf("startup temp cleanup failed: %v", err)
	}
	if cfg.Scan.StartupScanEnabled {
		if err := service.ScanAll(ctx); err != nil {
			service.logf("startup scan failed: %v", err)
		}
	}
	if cfg.Scan.WatchdogEnabled {
		if err := service.startWatcher(); err != nil {
			service.logf("watcher start failed: %v", err)
		}
	}
	service.startBackupCleanup()
	service.startWorkers()
	service.serveHTTP(listener)
	return service, nil
}

func withDefaults(opts Options) Options {
	if opts.ConfigPath == "" {
		opts.ConfigPath = getenv("CONFIG_PATH", defaultConfigPath)
	}
	if opts.DBPath == "" {
		opts.DBPath = getenv("DB_PATH", defaultDBPath)
	}
	if opts.LogPath == "" {
		opts.LogPath = getenv("LOG_PATH", defaultLogPath)
	}
	if opts.BackupRoot == "" {
		opts.BackupRoot = getenv("BACKUP_ROOT", defaultBackupRoot)
	}
	if opts.WorkRoot == "" {
		opts.WorkRoot = getenv("WORK_ROOT", defaultWorkRoot)
	}
	if opts.WebDir == "" {
		opts.WebDir = getenv("WEB_DIR", defaultWebDir)
	}
	if opts.HTTPAddr == "" {
		opts.HTTPAddr = getenv("HTTP_ADDR", defaultHTTPAddr)
	}
	if opts.FFmpegName == "" {
		opts.FFmpegName = getenv("FFMPEG_BIN", "ffmpeg")
	}
	if opts.FFprobeName == "" {
		opts.FFprobeName = getenv("FFPROBE_BIN", "ffprobe")
	}
	if opts.Prober == nil {
		opts.Prober = ffprobeProber{name: opts.FFprobeName}
	}
	if opts.Restart == nil {
		opts.Restart = execSelf
	}
	return opts
}

func newFileLogger(path string, stderr io.Writer) (*log.Logger, func() error, error) {
	if stderr == nil {
		stderr = io.Discard
	}
	if path == "" {
		return log.New(stderr, "audiocleaner: ", log.LstdFlags), func() error { return nil }, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	writer := io.MultiWriter(stderr, file)
	return log.New(writer, "audiocleaner: ", log.LstdFlags), file.Close, nil
}

func closeOptionsLog(opts Options) {
	if opts.logCloser != nil {
		_ = opts.logCloser()
	}
}

func (s *Service) ScanAll(ctx context.Context) error {
	cfg := s.config()
	paths, err := pipeline.ScanRoots(pipeline.ScanConfig{
		Roots:           cfg.Media.Roots,
		Extensions:      cfg.Media.Extensions,
		ExcludeDirs:     cfg.Media.ExcludeDirs,
		ExcludePatterns: cfg.Media.ExcludePatterns,
	})
	if err != nil {
		return err
	}
	for _, path := range paths {
		s.enqueueWithSource(path, pipeline.JobSourceScan)
	}
	return nil
}

func (s *Service) startWatcher() error {
	watcher, err := pipeline.NewWatcher(s.cfg.Media.Roots, func(path string) {
		if s.isCandidatePath(path) {
			s.enqueueWithSource(path, pipeline.JobSourceWatchdog)
		}
	})
	if err != nil {
		return err
	}
	s.watcher = watcher
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		for err := range watcher.Errors() {
			s.logf("watcher error: %v", err)
		}
	}()
	return nil
}

func (s *Service) startWorkers() {
	for i := 0; i < s.cfg.Pipeline.Workers; i++ {
		worker := pipeline.NewWorker(pipeline.WorkerDeps{
			ConfigProvider: s.config,
			Repository:     s.db,
			BindJob:        s.queue.BindJob,
			Runner:         pipeline.ExecRunner{},
			Prober:         s.prober,
			Events:         workerEvents{service: s},
			BackupRoot:     s.backupRoot,
			WorkRoot:       s.workRoot,
			FFmpegName:     s.ffmpegName,
		})
		s.workers.Add(1)
		go s.workerLoop(worker)
	}
}

func (s *Service) workerLoop(worker *pipeline.Worker) {
	defer s.workers.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.workerCtx.Done():
			return
		default:
		}
		job, ok := s.nextActiveJob()
		if ok {
			var result pipeline.AttemptResult
			var processErr error
			if job.ctx.Err() == nil {
				result, processErr = worker.ProcessAttempt(job.ctx, job.task)
			}
			job.cancel()
			if job.task.JobID == 0 && result.JobID != 0 && !s.queue.BindJob(job.task.Path, result.JobID) {
				s.logf("worker failed to bind job id=%d path=%s", result.JobID, job.task.Path)
			}
			retryScheduled := false
			if processErr != nil && result.Retryable {
				pipelineConfig := s.config().Pipeline
				retryScheduled = s.queue.ScheduleRetry(
					job.task.Path,
					pipelineConfig.RetryDelay(),
					result.LastError,
					pipelineConfig.MaxRetries,
				)
			}
			if retryScheduled {
				s.publishWorkerEvent(pipeline.WorkerEvent{
					FileID:  result.FileID,
					Path:    job.task.Path,
					Kind:    "phase_transition",
					Code:    string(pipeline.RuntimeRetryWait),
					Phase:   pipeline.RuntimeRetryWait,
					Attempt: job.task.AttemptNumber,
					Error:   result.LastError,
				})
			} else {
				if processErr != nil && result.JobID != 0 {
					finalError := result.FinalError
					if finalError == "" {
						finalError = processErr.Error()
					}
					finishCtx, cancelFinish := context.WithTimeout(context.Background(), 30*time.Second)
					_, _, finishErr := s.db.FinishProcessJob(
						finishCtx,
						result.JobID,
						result.FinalFile,
						repository.JobResultFailed,
						finalError,
						result.Backup,
					)
					cancelFinish()
					if finishErr != nil {
						s.logf("finish failed job id=%d: %v", result.JobID, finishErr)
					} else {
						outcome := result.FailureOutcome
						if outcome == "" {
							outcome = "failed"
						}
						s.publishWorkerEvent(pipeline.WorkerEvent{
							FileID:  result.FileID,
							Path:    job.task.Path,
							Kind:    "status_change",
							Code:    "failed",
							Status:  "failed",
							Outcome: outcome,
							Attempt: job.task.AttemptNumber,
							Error:   result.LastError,
						})
					}
				}
				s.queue.Done(job.task.Path)
			}
			if processErr != nil {
				s.logf("worker error: %v", processErr)
			}
		}
		select {
		case <-s.workerCtx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) nextActiveJob() (workerJob, bool) {
	s.workerIntakeMu.Lock()
	defer s.workerIntakeMu.Unlock()
	if s.stoppingWorkers.Load() {
		return workerJob{}, false
	}
	jobCtx, cancel := context.WithCancel(s.workerCtx)
	job, ok := s.queue.ClaimNext(cancel)
	if !ok {
		cancel()
		return workerJob{}, false
	}
	return workerJob{task: job, ctx: jobCtx, cancel: cancel}, true
}

func (s *Service) stopWorkerIntake() {
	s.workerIntakeMu.Lock()
	defer s.workerIntakeMu.Unlock()
	s.stoppingWorkers.Store(true)
}

func (s *Service) prepareHTTPServer() (net.Listener, error) {
	server := api.NewServer(api.Deps{
		Config:         s.cfg,
		ConfigPath:     s.configPath,
		WebDir:         s.webDir,
		Logf:           s.logf,
		Events:         s.events,
		ScanAll:        s.ScanAll,
		RequestRestart: s.RequestRestart,
		Status:         s,
		RuntimeTasks:   s,
		History:        s,
		Backups:        s,
		RuntimeLogs:    s,
	})
	s.server = &http.Server{
		Addr:              s.httpAddr,
		Handler:           server.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", s.httpAddr)
	if err != nil {
		return nil, err
	}
	return listener, nil
}

func (s *Service) serveHTTP(listener net.Listener) {
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logf("http server failed: %v", err)
		}
	}()
}

func (s *Service) RequestRestart(cfg config.Config) {
	s.setConfig(cfg)
	if !s.restartState.CompareAndSwap(restartRunning, restartPending) &&
		!s.restartState.CompareAndSwap(restartFailed, restartPending) {
		return
	}
	s.accepting.Store(false)
	s.stopWorkerIntake()
	if s.watcher != nil {
		if err := s.watcher.Close(); err != nil {
			s.logf("watcher close before restart failed: %v", err)
		}
	}
	s.cancelNonCriticalActiveJobs()
	s.waitForCriticalPhaseClear()
	s.cancelNonCriticalActiveJobs()
	s.events.Publish("service.restarting", map[string]any{"status": "restarting"})
	for attempt := 1; attempt <= 3; attempt++ {
		if err := s.restart(); err != nil {
			s.logf("restart attempt %d failed: %v", attempt, err)
			time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
			continue
		}
		return
	}
	s.restartState.Store(restartFailed)
	s.accepting.Store(true)
	s.stoppingWorkers.Store(false)
	if s.watcher != nil {
		if err := s.startWatcher(); err != nil {
			s.logf("watcher restart after process replacement failure failed: %v", err)
		}
	}
	s.logf("critical: process replacement failed after 3 attempts")
	s.events.Publish("service.restart_failed", map[string]any{"status": "restart_failed"})
}

func (s *Service) waitForCriticalPhaseClear() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for s.hasCriticalPhase() {
		<-ticker.C
	}
}

func (s *Service) Status(ctx context.Context) (any, error) {
	cfg := s.config()
	status := "running"
	switch s.restartState.Load() {
	case restartPending:
		status = "restarting"
	case restartFailed:
		status = "restart_failed"
	}
	outcomeCounts, err := s.db.JobOutcomeCounts(ctx)
	if err != nil {
		return nil, err
	}
	backups, err := s.db.Backups(ctx)
	if err != nil {
		return nil, err
	}
	transcodeStats, err := s.db.ProcessJobStats(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{
		"compatible": outcomeCounts.Compatible,
		"processed":  outcomeCounts.Processed,
		"failed":     outcomeCounts.Failed,
		"restored":   outcomeCounts.Restored,
	}
	var backupUsage int64
	now := time.Now()
	retentionDays := cfg.Backup.RetentionDays
	for _, record := range backups {
		if backupAvailability(record, now, retentionDays) != repository.BackupAvailable {
			continue
		}
		info, err := os.Stat(record.BackupPath)
		if err != nil {
			continue
		}
		backupUsage += info.Size()
	}
	return map[string]any{
		"status":                 status,
		"counts":                 counts,
		"backup_usage_bytes":     backupUsage,
		"transcode_success_rate": transcodeSuccessRate(transcodeStats),
	}, nil
}

func (s *Service) RuntimeTasks(_ context.Context) (any, error) {
	if s.queue == nil {
		return pipeline.RuntimeTaskSnapshot{
			Waiting: []pipeline.RuntimeTask{},
			Active:  []pipeline.RuntimeTask{},
		}, nil
	}
	return s.queue.Snapshot(), nil
}

func transcodeSuccessRate(stats repository.ProcessJobStats) map[string]any {
	total := stats.Succeeded + stats.Failed
	rate := 0.0
	if total > 0 {
		rate = float64(stats.Succeeded) / float64(total)
	}
	return map[string]any{
		"succeeded": stats.Succeeded,
		"failed":    stats.Failed,
		"rate":      rate,
	}
}

func (s *Service) ListHistory(ctx context.Context, page *repository.PageRequest) (any, error) {
	if page != nil {
		return s.db.HistoryPage(ctx, *page)
	}
	return s.db.History(ctx)
}

func (s *Service) ListBackups(ctx context.Context, page *repository.PageRequest) (any, error) {
	now := time.Now()
	retentionDays := s.config().Backup.RetentionDays
	if page != nil {
		result, err := s.db.BackupsPage(ctx, *page)
		if err != nil {
			return nil, err
		}
		result.Items = backupsWithAvailability(result.Items, now, retentionDays)
		return result, nil
	}
	records, err := s.db.Backups(ctx)
	if err != nil {
		return nil, err
	}
	return backupsWithAvailability(records, now, retentionDays), nil
}

func backupsWithAvailability(records []repository.BackupView, now time.Time, retentionDays int) []repository.BackupView {
	for i := range records {
		records[i].Availability = backupAvailability(records[i], now, retentionDays)
	}
	return records
}

func backupAvailability(record repository.BackupView, now time.Time, retentionDays int) repository.BackupAvailability {
	if _, err := os.Stat(record.BackupPath); errors.Is(err, os.ErrNotExist) {
		return repository.BackupMissing
	} else if err != nil {
		return repository.BackupMissing
	}
	if !record.RestoredAt.IsZero() {
		return repository.BackupRestored
	}
	if backupExpired(record, now, retentionDays) {
		return repository.BackupExpired
	}
	return repository.BackupAvailable
}

func backupExpired(record repository.BackupView, now time.Time, retentionDays int) bool {
	if record.CreatedAt.IsZero() {
		return false
	}
	expiresAt := record.CreatedAt.AddDate(0, 0, retentionDays)
	return !expiresAt.After(now)
}

func (s *Service) RestoreBackup(ctx context.Context, id int64) (any, error) {
	record, err := s.db.BackupByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !s.beginPathMutation(record.OriginalPath) {
		return nil, api.ErrProcessing
	}
	defer s.finishPathMutation(record.OriginalPath)

	file, err := s.db.FileByPath(ctx, record.OriginalPath)
	if err != nil {
		return nil, err
	}
	cfg := s.config()
	result, err := backup.RestoreBackup(backup.RestoreRequest{
		OriginalPath: record.OriginalPath,
		BackupPath:   record.BackupPath,
		MediaRoots:   cfg.Media.Roots,
	})
	if err != nil {
		return nil, err
	}

	if err := s.persistRestoredFile(record.ID, result.SafetyPath, file); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) restoreProbeDeadline() time.Duration {
	if s.restoreProbeTimeout > 0 {
		return s.restoreProbeTimeout
	}
	return 30 * time.Second
}

func (s *Service) restorePersistDeadline() time.Duration {
	if s.restorePersistTimeout > 0 {
		return s.restorePersistTimeout
	}
	return 30 * time.Second
}

func (s *Service) CleanupBackups(ctx context.Context) (any, error) {
	records, err := s.db.UnrestoredBackups(ctx)
	if err != nil {
		return nil, err
	}
	removed := 0
	now := time.Now()
	retentionDays := s.config().Backup.RetentionDays
	for _, record := range records {
		if !backupExpired(record, now, retentionDays) {
			continue
		}
		didRemove, err := s.cleanupBackupRecord(ctx, record)
		if err != nil {
			return nil, err
		}
		if didRemove {
			removed++
		}
	}
	return map[string]int{"removed": removed}, nil
}

func (s *Service) startBackupCleanup() {
	interval := s.backupCleanupInterval
	if interval <= 0 || s.db == nil || s.workerCtx == nil {
		return
	}

	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.workerCtx.Done():
				return
			case <-ticker.C:
				if _, err := s.CleanupBackups(context.Background()); err != nil {
					s.logf("backup cleanup failed: %v", err)
				}
			}
		}
	}()
}

func (s *Service) cleanupOrphanTempOutputs(ctx context.Context) error {
	cfg := s.config()
	for _, root := range cfg.Media.Roots {
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if !pipeline.IsAudioCleanerTempOutputPath(entry.Name()) {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Service) cleanupBackupRecord(ctx context.Context, record repository.BackupView) (bool, error) {
	if !s.beginPathMutation(record.OriginalPath) {
		return false, nil
	}
	defer s.finishPathMutation(record.OriginalPath)

	current, err := s.db.BackupByID(ctx, record.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !current.RestoredAt.IsZero() {
		return false, nil
	}
	if err := os.Remove(current.BackupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return s.db.DeleteUnrestoredBackup(ctx, current.ID)
}

func (s *Service) RuntimeLogs(ctx context.Context, lines int) (any, error) {
	content, count, err := tailLogFile(ctx, s.logPath, lines)
	if err != nil {
		return nil, err
	}
	return api.RuntimeLogResult{Content: content, Lines: count}, nil
}

func (s *Service) enqueueWithSource(path string, source pipeline.JobSource) bool {
	s.workerIntakeMu.Lock()
	defer s.workerIntakeMu.Unlock()
	if !s.canEnqueueLocked(path) {
		return false
	}
	return s.queue.EnqueueWithSource(path, source)
}

func (s *Service) canEnqueueLocked(path string) bool {
	if !s.accepting.Load() {
		return false
	}
	if !s.isCandidatePath(path) {
		return false
	}
	if s.isPathMutating(path) {
		return false
	}
	if s.queue == nil {
		return false
	}
	return true
}

func (s *Service) isProcessingPath(path string) bool {
	return s.queue != nil && s.queue.Contains(path)
}

func (s *Service) beginPathMutation(path string) bool {
	s.workerIntakeMu.Lock()
	defer s.workerIntakeMu.Unlock()
	if s.isProcessingPath(path) {
		return false
	}
	s.pathMutationMu.Lock()
	defer s.pathMutationMu.Unlock()
	if s.pathMutationPaths == nil {
		s.pathMutationPaths = make(map[string]struct{})
	}
	if _, ok := s.pathMutationPaths[path]; ok {
		return false
	}
	s.pathMutationPaths[path] = struct{}{}
	return true
}

func (s *Service) finishPathMutation(path string) {
	s.pathMutationMu.Lock()
	defer s.pathMutationMu.Unlock()
	delete(s.pathMutationPaths, path)
}

func (s *Service) isPathMutating(path string) bool {
	s.pathMutationMu.Lock()
	defer s.pathMutationMu.Unlock()
	_, ok := s.pathMutationPaths[path]
	return ok
}

func (s *Service) persistRestoredFile(backupID int64, safetyPath string, file repository.FileFacts) error {
	info, statErr := os.Stat(file.Path)
	prober := s.prober
	if prober == nil {
		prober = ffprobeProber{name: s.ffprobeName}
	}
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), s.restoreProbeDeadline())
	probe, probeErr := prober.Probe(probeCtx, file.Path)
	cancelProbe()

	if statErr == nil {
		file.Size = info.Size()
		file.MTimeNS = info.ModTime().UnixNano()
	}
	file.AudioSignature = ""
	file.VideoSignature = ""
	file.ComplianceStatus = repository.ComplianceUnknown
	file.AudioPolicyVersion = 0

	jobResult := repository.JobResultSucceeded
	finalError := ""
	status := "restored"
	if statErr != nil {
		jobResult = repository.JobResultFailed
		status = "failed"
		finalError = fmt.Sprintf("%s: stat restored file: %v", pipeline.CauseRestoreStatError, statErr)
	} else if probeErr != nil {
		jobResult = repository.JobResultFailed
		status = "failed"
		finalError = fmt.Sprintf("%s: probe restored file: %v", pipeline.CauseRestoreProbeError, probeErr)
	} else {
		file.AudioSignature = probe.AudioSignature()
		file.VideoSignature = probe.VideoSignature()
		analysisConfig := s.config()
		decision := media.Decide(file.Path, probe, media.DecisionConfig{
			Extensions:         analysisConfig.Media.Extensions,
			IncompatibleCodecs: analysisConfig.Audio.IncompatibleCodecs,
		})
		switch decision.Action {
		case media.ActionAlreadyCompatible:
			file.ComplianceStatus = repository.ComplianceCompliant
			file.AudioPolicyVersion = analysisConfig.Audio.Version
		case media.ActionTranscode:
			file.ComplianceStatus = repository.ComplianceNoncompliant
			file.AudioPolicyVersion = analysisConfig.Audio.Version
		default:
			file.AudioSignature = ""
			file.VideoSignature = ""
			jobResult = repository.JobResultFailed
			status = "failed"
			finalError = fmt.Sprintf("%s: assess restored file: %s", pipeline.CauseUnsupported, decision.Reason)
		}
	}

	persistCtx, cancelPersist := context.WithTimeout(context.Background(), s.restorePersistDeadline())
	defer cancelPersist()
	persisted, _, err := s.db.RecordRestore(persistCtx, backupID, safetyPath, file, jobResult, finalError)
	if err != nil {
		return err
	}
	if s.events != nil {
		s.events.Publish("job.restored", map[string]any{
			"file_id": persisted.ID,
			"path":    persisted.Path,
			"status":  status,
			"error":   finalError,
		})
	}
	return nil
}

func (s *Service) isCandidatePath(path string) bool {
	cfg := s.config()
	ext := strings.ToLower(filepath.Ext(path))
	if pipeline.IsPathExcluded(path, cfg.Media.ExcludeDirs, cfg.Media.ExcludePatterns) {
		return false
	}
	for _, candidate := range cfg.Media.Extensions {
		if strings.ToLower(candidate) == ext {
			return !pipeline.IsAudioCleanerTempOutputPath(path)
		}
	}
	return false
}

func (s *Service) config() config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

func (s *Service) setConfig(cfg config.Config) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	s.cfg = cfg
}

func (s *Service) publishWorkerEvent(event pipeline.WorkerEvent) {
	if s.queue != nil && event.Phase != pipeline.RuntimeRetryWait {
		s.queue.UpdatePhase(event.Path, event.Phase)
	}
	s.logf(
		"job event kind=%s code=%s file_id=%d path=%s status=%s phase=%s outcome=%s message=%s error=%s",
		event.Kind,
		event.Code,
		event.FileID,
		event.Path,
		event.Status,
		event.Phase,
		event.Outcome,
		event.Message,
		event.Error,
	)
	if s.events != nil {
		s.events.Publish(event.Code, map[string]any{
			"file_id": event.FileID,
			"path":    event.Path,
			"status":  event.Status,
			"phase":   event.Phase,
			"kind":    event.Kind,
			"code":    event.Code,
			"outcome": event.Outcome,
			"attempt": event.Attempt,
			"message": event.Message,
			"error":   event.Error,
		})
	}
}

func (s *Service) hasCriticalPhase() bool {
	return s.queue != nil && s.queue.HasCriticalActive()
}

func (s *Service) cancelNonCriticalActiveJobs() {
	s.cancelActiveJobs(false)
}

func (s *Service) cancelAllActiveJobs() {
	s.cancelActiveJobs(true)
}

func (s *Service) cancelActiveJobs(includeCritical bool) {
	if s.queue != nil {
		s.queue.CancelActive(includeCritical)
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

type workerEvents struct {
	service *Service
}

func (w workerEvents) Publish(ctx context.Context, event pipeline.WorkerEvent) error {
	w.service.publishWorkerEvent(event)
	return nil
}

type ffprobeProber struct {
	name string
}

func (p ffprobeProber) Probe(ctx context.Context, path string) (media.ProbeData, error) {
	cmd := exec.CommandContext(ctx, p.name,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	output, err := cmd.Output()
	if err != nil {
		return media.ProbeData{}, err
	}
	return media.ParseProbe(output)
}

func execSelf() error {
	return syscall.Exec(os.Args[0], os.Args, os.Environ())
}

func tailLogFile(ctx context.Context, path string, lineLimit int) (string, int, error) {
	if lineLimit < 1 {
		lineLimit = 100
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	lines := make([]string, 0, lineLimit)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		line := scanner.Text()
		if len(lines) == lineLimit {
			copy(lines, lines[1:])
			lines[lineLimit-1] = line
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return "", 0, err
	}
	if len(lines) == 0 {
		return "", 0, nil
	}
	return strings.Join(lines, "\n") + "\n", len(lines), nil
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
