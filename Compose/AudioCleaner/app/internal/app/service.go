package app

import (
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
	ProcessingFiles(ctx context.Context) ([]repository.FileRecord, error)
	SaveFile(ctx context.Context, record repository.FileRecord) error
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

	activeMu   sync.Mutex
	activeJobs map[string]activeJob

	pathMutationMu    sync.Mutex
	pathMutationPaths map[string]struct{}
}

type activeJob struct {
	cancel context.CancelFunc
	phase  repository.Phase
}

type workerJob struct {
	path   string
	source pipeline.JobSource
	ctx    context.Context
	cancel context.CancelFunc
}

func RecoverStartup(ctx context.Context, repo RecoveryRepository) error {
	files, err := repo.ProcessingFiles(ctx)
	if err != nil {
		return err
	}
	for _, file := range files {
		if isCriticalPhase(file.Phase) {
			file.Status = repository.StatusUnqualified
			file.Phase = repository.PhaseDeferred
			file.UnqualifiedReason = repository.ReasonFailed
			file.LastError = fmt.Sprintf("%s%s; manual review required", startupCriticalRecoveryPrefix, file.Phase)
		} else {
			file.Status = repository.StatusProcessing
			file.Phase = repository.PhaseQueued
			file.LastError = ""
			file.UnqualifiedReason = ""
		}
		if err := repo.SaveFile(ctx, file); err != nil {
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
		activeJobs:            make(map[string]activeJob),
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
	if err := service.markMissingBackups(ctx); err != nil {
		service.logf("startup backup presence check failed: %v", err)
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
		opts.DBPath = getenv("DB_PATH", getenv("DATA_PATH", defaultDBPath))
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
		skip, err := s.shouldSkipStartupRecoveredCritical(ctx, path)
		if err != nil {
			return err
		}
		if skip {
			continue
		}
		s.enqueueWithSource(path, pipeline.JobSourceScan)
	}
	return nil
}

func (s *Service) shouldSkipStartupRecoveredCritical(ctx context.Context, path string) (bool, error) {
	if s.db == nil {
		return false, nil
	}
	file, err := s.db.FileByPath(ctx, path)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return file.Status == repository.StatusUnqualified &&
		file.Phase == repository.PhaseDeferred &&
		file.UnqualifiedReason == repository.ReasonFailed &&
		strings.HasPrefix(file.LastError, startupCriticalRecoveryPrefix), nil
}

func (s *Service) startWatcher() error {
	watcher, err := pipeline.NewWatcher(s.cfg.Media.Roots, func(path string) {
		if s.isCandidatePath(path) {
			s.enqueue(path)
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
			Config:     s.cfg,
			Repository: s.db,
			Queue:      s.queue,
			Runner:     pipeline.ExecRunner{},
			Prober:     s.prober,
			Events:     workerEvents{service: s},
			Scheduler:  retryScheduler{service: s},
			BackupRoot: s.backupRoot,
			WorkRoot:   s.workRoot,
			FFmpegName: s.ffmpegName,
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
			if job.ctx.Err() == nil {
				if err := worker.ProcessPathWithSource(job.ctx, job.path, job.source); err != nil {
					s.logf("worker error: %v", err)
				}
			}
			job.cancel()
			s.queue.Done(job.path)
			s.untrackActiveJob(job.path)
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
	job, ok := s.queue.NextJob()
	if !ok {
		return workerJob{}, false
	}
	jobCtx, cancel := context.WithCancel(s.workerCtx)
	s.trackActiveJob(job.Path, cancel)
	return workerJob{path: job.Path, source: job.Source, ctx: jobCtx, cancel: cancel}, true
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
		Events:         s.events,
		ScanAll:        s.ScanAll,
		RequestRestart: s.RequestRestart,
		Status:         s,
		Jobs:           s,
		Backups:        s,
		Logs:           s,
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
	files, err := s.db.Files(ctx)
	if err != nil {
		return nil, err
	}
	backups, err := s.db.Backups(ctx)
	if err != nil {
		return nil, err
	}
	transcodeStats, err := s.db.TranscodeStats(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{
		string(repository.StatusQualified):   0,
		string(repository.StatusProcessing):  0,
		string(repository.StatusUnqualified): 0,
	}
	recentFailed := make([]repository.FileRecord, 0)
	for _, file := range files {
		counts[string(file.Status)]++
		if file.Status == repository.StatusUnqualified &&
			file.UnqualifiedReason != repository.ReasonIgnored &&
			file.UnqualifiedReason != repository.ReasonRestored &&
			file.LastError != "" &&
			len(recentFailed) < 5 {
			recentFailed = append(recentFailed, file)
		}
	}
	var backupUsage int64
	for _, record := range backups {
		if record.RestoredAt.IsZero() && !record.Missing {
			backupUsage += record.OriginalSize
		}
	}
	queueCount := 0
	if s.queue != nil {
		queueCount = s.queue.Len()
	}
	return map[string]any{
		"status":                 status,
		"workers":                cfg.Pipeline.Workers,
		"media_roots":            cfg.Media.Roots,
		"snapshot_id":            s.snapshotID(),
		"queue_count":            queueCount,
		"current_processing":     s.currentProcessing(),
		"counts":                 counts,
		"recent_failed":          recentFailed,
		"backup_usage_bytes":     backupUsage,
		"transcode_success_rate": transcodeSuccessRate(transcodeStats),
	}, nil
}

func (s *Service) snapshotID() uint64 {
	if s.events == nil {
		return 0
	}
	return s.events.SnapshotID()
}

func (s *Service) currentProcessing() []map[string]any {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()

	current := make([]map[string]any, 0, len(s.activeJobs))
	for path, job := range s.activeJobs {
		current = append(current, map[string]any{
			"path":  path,
			"phase": job.phase,
		})
	}
	return current
}

func transcodeSuccessRate(stats repository.TranscodeStats) map[string]any {
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

func (s *Service) ListJobs(ctx context.Context, page *repository.PageRequest) (any, error) {
	if page != nil {
		return s.db.FilesPage(ctx, *page)
	}
	return s.db.Files(ctx)
}

func (s *Service) JobByID(ctx context.Context, id int64) (any, error) {
	file, err := s.db.FileByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("job not found")
	}
	return file, err
}

func (s *Service) RetryJob(ctx context.Context, id int64) error {
	file, err := s.db.FileByID(ctx, id)
	if err != nil {
		return err
	}
	if file.Status == repository.StatusProcessing {
		return api.ErrProcessing
	}
	if file.Status != repository.StatusUnqualified {
		return api.ErrInvalidJobState
	}
	s.workerIntakeMu.Lock()
	defer s.workerIntakeMu.Unlock()
	if !s.canEnqueueLocked(file.Path) {
		return api.ErrProcessing
	}
	file.Status = repository.StatusProcessing
	file.Phase = repository.PhaseQueued
	file.UnqualifiedReason = ""
	file.LastError = ""
	if err := s.db.SaveFile(ctx, file); err != nil {
		return err
	}
	s.queue.Enqueue(file.Path)
	return nil
}

func (s *Service) IgnoreJob(ctx context.Context, id int64) error {
	file, err := s.db.FileByID(ctx, id)
	if err != nil {
		return err
	}
	if file.Status == repository.StatusProcessing {
		return api.ErrProcessing
	}
	if file.Status != repository.StatusUnqualified {
		return api.ErrInvalidJobState
	}
	if s.isProcessingPath(file.Path) || s.isPathMutating(file.Path) {
		return api.ErrProcessing
	}
	if err := s.updateCurrentFingerprint(ctx, &file); err != nil {
		return err
	}
	file.Status = repository.StatusUnqualified
	file.Phase = repository.PhaseDeferred
	file.UnqualifiedReason = repository.ReasonIgnored
	file.LastError = ""
	return s.db.SaveFile(ctx, file)
}

func (s *Service) updateCurrentFingerprint(ctx context.Context, file *repository.FileRecord) error {
	info, err := os.Stat(file.Path)
	if err != nil {
		return err
	}
	file.Size = info.Size()
	file.MTimeNS = info.ModTime().UnixNano()

	prober := s.prober
	if prober == nil {
		prober = ffprobeProber{name: s.ffprobeName}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	probe, probeErr := prober.Probe(probeCtx, file.Path)
	cancel()
	if probeErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(probeErr, context.Canceled) || errors.Is(probeErr, context.DeadlineExceeded) {
			return probeErr
		}
		file.AudioSignature = ""
		file.VideoSignature = ""
	} else {
		file.AudioSignature = probe.AudioSignature()
		file.VideoSignature = probe.VideoSignature()
	}
	file.Fingerprint = media.Fingerprint(file.Path, file.Size, file.MTimeNS, file.AudioSignature, file.VideoSignature)
	return nil
}

func (s *Service) ListBackups(ctx context.Context, page *repository.PageRequest) (any, error) {
	if page != nil {
		return s.db.BackupsPage(ctx, *page)
	}
	return s.db.Backups(ctx)
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
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && file.Status == repository.StatusProcessing {
		return nil, api.ErrProcessing
	}
	if errors.Is(err, sql.ErrNoRows) {
		file = repository.FileRecord{Path: record.OriginalPath}
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
	records, err := s.db.ExpiredBackups(ctx, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	removed := 0
	for _, record := range records {
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

func (s *Service) markMissingBackups(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	records, err := s.db.Backups(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		_, statErr := os.Stat(record.BackupPath)
		missing := errors.Is(statErr, os.ErrNotExist)
		if statErr != nil && !missing {
			return statErr
		}
		if record.Missing != missing {
			if err := s.db.MarkBackupMissing(ctx, record.ID, missing); err != nil {
				return err
			}
		}
	}
	return nil
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

func (s *Service) cleanupBackupRecord(ctx context.Context, record repository.BackupRecord) (bool, error) {
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

func (s *Service) RecentLogs(ctx context.Context) (any, error) {
	return s.db.RecentJobEvents(ctx, 50)
}

func (s *Service) enqueue(path string) bool {
	return s.enqueueWithSource(path, pipeline.JobSourceDefault)
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
	if s.isActivePath(path) {
		return true
	}
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

func (s *Service) persistRestoredFile(backupID int64, safetyPath string, file repository.FileRecord) error {
	cfg := s.config()
	info, statErr := os.Stat(file.Path)
	prober := s.prober
	if prober == nil {
		prober = ffprobeProber{name: s.ffprobeName}
	}
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), s.restoreProbeDeadline())
	probe, probeErr := prober.Probe(probeCtx, file.Path)
	cancelProbe()

	file.QualificationSource = repository.SourceRestored
	file.Phase = ""
	file.Attempts = 0
	if statErr == nil {
		file.Size = info.Size()
		file.MTimeNS = info.ModTime().UnixNano()
	}
	file.AudioSignature = probe.AudioSignature()
	file.VideoSignature = probe.VideoSignature()
	file.Fingerprint = media.Fingerprint(file.Path, file.Size, file.MTimeNS, file.AudioSignature, file.VideoSignature)

	if statErr != nil {
		file.Status = repository.StatusUnqualified
		file.UnqualifiedReason = repository.ReasonRestored
		file.LastError = fmt.Sprintf("stat restored file: %v", statErr)
	} else if probeErr != nil {
		file.Status = repository.StatusUnqualified
		file.UnqualifiedReason = repository.ReasonRestored
		file.LastError = fmt.Sprintf("probe restored file: %v", probeErr)
	} else {
		decision := media.Decide(file.Path, probe, media.DecisionConfig{
			Extensions:         cfg.Media.Extensions,
			IncompatibleCodecs: cfg.Audio.IncompatibleCodecs,
		})
		switch decision.Action {
		case media.ActionAlreadyCompatible:
			file.Status = repository.StatusQualified
			file.UnqualifiedReason = ""
			file.LastError = ""
		case media.ActionUnsupported:
			file.Status = repository.StatusUnqualified
			file.UnqualifiedReason = repository.ReasonRestored
			file.LastError = decision.Reason
		default:
			file.Status = repository.StatusUnqualified
			file.UnqualifiedReason = repository.ReasonRestored
			file.LastError = "restored file still requires audio transcoding"
		}
	}

	event := repository.JobEvent{
		EventType: "job.restored",
		Phase:     file.Phase,
		Message:   "restored",
		Error:     file.LastError,
	}
	persistCtx, cancelPersist := context.WithTimeout(context.Background(), s.restorePersistDeadline())
	defer cancelPersist()
	persisted, err := s.db.RecordRestore(persistCtx, backupID, safetyPath, file, event)
	if err != nil {
		return err
	}
	if s.events != nil {
		s.events.Publish("job.restored", map[string]any{
			"file_id": persisted.ID,
			"path":    persisted.Path,
			"status":  persisted.Status,
			"error":   persisted.LastError,
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
	s.updateActiveJobPhase(event.File.Path, event.Event.Phase)
	s.logf(
		"job event type=%s file_id=%d path=%s status=%s phase=%s message=%s error=%s",
		event.Event.EventType,
		event.File.ID,
		event.File.Path,
		event.File.Status,
		event.Event.Phase,
		event.Event.Message,
		event.Event.Error,
	)
	s.events.Publish(event.Event.EventType, map[string]any{
		"file_id": event.File.ID,
		"path":    event.File.Path,
		"status":  event.File.Status,
		"phase":   event.Event.Phase,
		"message": event.Event.Message,
		"error":   event.Event.Error,
	})
}

func (s *Service) hasCriticalPhase() bool {
	return s.hasCriticalActiveJob()
}

func (s *Service) trackActiveJob(path string, cancel context.CancelFunc) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.activeJobs == nil {
		s.activeJobs = make(map[string]activeJob)
	}
	s.activeJobs[path] = activeJob{cancel: cancel, phase: repository.PhaseQueued}
}

func (s *Service) updateActiveJobPhase(path string, phase repository.Phase) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	job, ok := s.activeJobs[path]
	if !ok {
		return
	}
	job.phase = phase
	s.activeJobs[path] = job
}

func (s *Service) untrackActiveJob(path string) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	delete(s.activeJobs, path)
}

func (s *Service) isActivePath(path string) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	_, ok := s.activeJobs[path]
	return ok
}

func (s *Service) cancelNonCriticalActiveJobs() {
	s.cancelActiveJobs(false)
}

func (s *Service) cancelAllActiveJobs() {
	s.cancelActiveJobs(true)
}

func (s *Service) cancelActiveJobs(includeCritical bool) {
	s.activeMu.Lock()
	jobs := make([]activeJob, 0, len(s.activeJobs))
	for _, job := range s.activeJobs {
		if includeCritical || !isCriticalPhase(job.phase) {
			jobs = append(jobs, job)
		}
	}
	s.activeMu.Unlock()

	for _, job := range jobs {
		if job.cancel != nil {
			job.cancel()
		}
	}
}

func (s *Service) hasCriticalActiveJob() bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	for _, job := range s.activeJobs {
		if isCriticalPhase(job.phase) {
			return true
		}
	}
	return false
}

func isCriticalPhase(phase repository.Phase) bool {
	return phase == repository.PhaseBackingUp || phase == repository.PhaseReplacing
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

type retryScheduler struct {
	service *Service
}

func (r retryScheduler) Schedule(path string, delay time.Duration) {
	time.AfterFunc(delay, func() {
		r.service.enqueue(path)
	})
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

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
