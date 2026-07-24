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
	"omv-blueprint/compose/audiocleaner/internal/settings"
)

const (
	defaultConfigPath = "/app/config/config.json"
	defaultDBPath     = "/app/data/audiocleaner.db"
	defaultBackupRoot = "/app/backups"
	defaultHTTPAddr   = ":9830"
	defaultLogPath    = "/app/logs/audiocleaner.log"
	defaultWorkRoot   = "/app/work"
	defaultWebDir     = "/app/web/dist"

	startupCriticalRecoveryPrefix = "startup recovered interrupted critical phase "
)

const (
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

type runtimeWatcher interface {
	Close() error
	Errors() <-chan error
}

type watcherFactory func([]string, func(string)) (runtimeWatcher, error)

type Service struct {
	cfg        config.Config
	cfgMu      sync.RWMutex
	configPath string
	db         *repository.Repository
	events     *eventbus.Bus
	queue      *pipeline.Queue
	watcher    runtimeWatcher
	watcherMu  sync.Mutex
	newWatcher watcherFactory
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

	workerCtx       context.Context
	cancelWorker    context.CancelFunc
	workers         sync.WaitGroup
	workerIntakeMu  sync.Mutex
	workerControlMu sync.Mutex
	workerStops     []chan struct{}

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
		cfg:               cfg,
		configPath:        opts.ConfigPath,
		db:                repo,
		events:            eventbus.New(),
		queue:             pipeline.NewQueue(),
		logger:            opts.Logger,
		logCloser:         opts.logCloser,
		logPath:           opts.LogPath,
		backupRoot:        opts.BackupRoot,
		workRoot:          opts.WorkRoot,
		webDir:            opts.WebDir,
		httpAddr:          opts.HTTPAddr,
		ffmpegName:        opts.FFmpegName,
		ffprobeName:       opts.FFprobeName,
		prober:            opts.Prober,
		restart:           opts.Restart,
		workerCtx:         workerCtx,
		cancelWorker:      cancelWorker,
		pathMutationPaths: make(map[string]struct{}),
	}
	service.accepting.Store(true)
	service.restartState.Store(restartRunning)
	service.logf("service started pid=%d", os.Getpid())

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
	if err := service.recoverReplacementJournals(ctx); err != nil {
		_ = listener.Close()
		_ = repo.Close()
		closeOptionsLog(opts)
		cancelWorker()
		return nil, fmt.Errorf("recover interrupted replacements: %w", err)
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
	service.startWorkers()
	service.serveHTTP(listener)
	return service, nil
}

func (s *Service) recoverReplacementJournals(ctx context.Context) error {
	journals, err := s.db.ReplacementJournals(ctx)
	if err != nil {
		return err
	}
	for _, journal := range journals {
		if journal.Phase == repository.ReplacementCommitted {
			file, fileErr := s.db.FileByID(ctx, journal.FileID)
			if fileErr == nil && file.BackupFile == journal.TemporaryBackupPath {
				if err := s.db.DeleteReplacementJournal(ctx, journal.JobID); err != nil {
					return err
				}
				continue
			}
			if fileErr != nil && !errors.Is(fileErr, sql.ErrNoRows) {
				return fileErr
			}
			if err := s.cleanupReplacementJournal(ctx, journal); err != nil {
				return err
			}
			continue
		}
		file, err := s.db.FileByID(ctx, journal.FileID)
		if err != nil {
			return err
		}
		finalError := startupCriticalRecoveryPrefix + string(journal.Phase)
		if journal.Phase == repository.ReplacementOutputInstalled || journal.Phase == repository.ReplacementRestoring {
			restoreErr := s.restoreJournalBackup(ctx, journal, &file)
			if restoreErr != nil {
				file.BackupFile = journal.TemporaryBackupPath
				finalError += "; automatic restore failed: " + restoreErr.Error()
				if _, _, err := s.db.FinishProcessJob(ctx, journal.JobID, file, repository.JobResultFailed, finalError); err != nil {
					return err
				}
				if err := s.db.DeleteReplacementJournal(ctx, journal.JobID); err != nil {
					return err
				}
				s.logf("startup preserved unresolved backup file_id=%d path=%s backup_file=%s error=%v", file.ID, file.Path, file.BackupFile, restoreErr)
				continue
			}
		}
		if _, _, err := s.db.FinishProcessJob(ctx, journal.JobID, file, repository.JobResultFailed, finalError); err != nil {
			return err
		}
		if err := s.cleanupReplacementJournal(ctx, journal); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) restoreJournalBackup(ctx context.Context, journal repository.ReplacementJournal, file *repository.FileFacts) error {
	cfg := s.config()
	if _, err := backup.Restore(backup.RestoreRequest{
		OriginalPath: journal.OriginalPath,
		BackupPath:   journal.TemporaryBackupPath,
		BackupRoot:   s.backupRoot,
		MediaRoots:   cfg.Media.Roots,
	}); err != nil {
		return err
	}
	info, err := os.Stat(journal.OriginalPath)
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.restoreProbeDeadline())
	defer cancel()
	probe, err := s.prober.Probe(probeCtx, journal.OriginalPath)
	if err != nil {
		return err
	}
	if info.Size() != journal.OriginalSize ||
		(journal.OriginalAudioSignature != "" && probe.AudioSignature() != journal.OriginalAudioSignature) ||
		(journal.OriginalVideoSignature != "" && probe.VideoSignature() != journal.OriginalVideoSignature) {
		return errors.New("restored file does not match replacement journal")
	}
	file.Size = info.Size()
	file.MTimeNS = info.ModTime().UnixNano()
	file.AudioSignature = probe.AudioSignature()
	file.VideoSignature = probe.VideoSignature()
	return nil
}

func (s *Service) cleanupReplacementJournal(ctx context.Context, journal repository.ReplacementJournal) error {
	if journal.OutputPath != "" {
		if err := os.Remove(journal.OutputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := backup.Remove(journal.TemporaryBackupPath, s.backupRoot); err != nil {
		return err
	}
	return s.db.DeleteReplacementJournal(ctx, journal.JobID)
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
	return s.reloadWatcher(s.config())
}

func (s *Service) reloadWatcher(cfg config.Config) error {
	if !cfg.Scan.WatchdogEnabled {
		s.setConfig(cfg)
		s.closeWatcher()
		return nil
	}
	mediaConfig := cfg.Media
	createWatcher := s.newWatcher
	if createWatcher == nil {
		createWatcher = func(roots []string, onPath func(string)) (runtimeWatcher, error) {
			return pipeline.NewWatcher(roots, onPath)
		}
	}
	candidate, err := createWatcher(mediaConfig.Roots, func(path string) {
		if isCandidatePathForMedia(path, mediaConfig) {
			s.enqueueWithSource(path, pipeline.JobSourceWatchdog)
		}
	})
	if err != nil {
		return err
	}
	s.watcherMu.Lock()
	old := s.watcher
	s.setConfig(cfg)
	s.watcher = candidate
	s.watcherMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		for err := range candidate.Errors() {
			s.logf("watcher error: %v", err)
		}
	}()
	return nil
}

func (s *Service) startWorkers() {
	s.resizeWorkers(s.config().Pipeline.Workers)
}

func (s *Service) resizeWorkers(target int) {
	s.workerControlMu.Lock()
	defer s.workerControlMu.Unlock()
	for len(s.workerStops) < target {
		stop := make(chan struct{})
		s.workerStops = append(s.workerStops, stop)
		s.workers.Add(1)
		go s.workerLoop(stop)
	}
	for len(s.workerStops) > target {
		last := len(s.workerStops) - 1
		close(s.workerStops[last])
		s.workerStops = s.workerStops[:last]
	}
}

func (s *Service) workerCount() int {
	s.workerControlMu.Lock()
	defer s.workerControlMu.Unlock()
	return len(s.workerStops)
}

func (s *Service) workerLoop(stop <-chan struct{}) {
	defer s.workers.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.workerCtx.Done():
			return
		case <-stop:
			return
		default:
		}
		job, ok := s.nextActiveJob()
		if ok {
			snapshot := s.config()
			worker := s.newWorker(snapshot)
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
					)
					cancelFinish()
					if finishErr != nil {
						s.logf("finish failed job id=%d: %v", result.JobID, finishErr)
					} else {
						if result.FinalFile.BackupFile != "" {
							journalCtx, cancelJournal := context.WithTimeout(context.Background(), 30*time.Second)
							err := s.db.DeleteReplacementJournal(journalCtx, result.JobID)
							cancelJournal()
							if err != nil {
								s.logf("delete unresolved backup journal job_id=%d: %v", result.JobID, err)
							}
							if s.events != nil {
								s.events.Publish("file.backup_created", map[string]any{
									"file_id": result.FinalFile.ID, "path": result.FinalFile.Path,
									"backup_file": result.FinalFile.BackupFile,
								})
							}
						}
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
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) newWorker(snapshot config.Config) *pipeline.Worker {
	return pipeline.NewWorker(pipeline.WorkerDeps{
		ConfigProvider: func() config.Config { return snapshot },
		Repository:     s.db,
		BindJob:        s.queue.BindJob,
		Runner:         pipeline.ExecRunner{},
		Prober:         s.prober,
		Events:         workerEvents{service: s},
		BackupRoot:     s.backupRoot,
		WorkRoot:       s.workRoot,
		FFmpegName:     s.ffmpegName,
	})
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
		Config:           s.cfg,
		ConfigPath:       s.configPath,
		WebDir:           s.webDir,
		MediaRoot:        "/media",
		Logf:             s.logf,
		Events:           s.events,
		ScanAll:          s.ScanAll,
		RequestRestart:   s.RequestRestart,
		ConfigApplicator: s,
		Status:           s,
		RuntimeTasks:     s,
		History:          s,
		Files:            s,
		RuntimeLogs:      s,
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

func (s *Service) RequestRestart(before, after config.Config) error {
	if !s.restartState.CompareAndSwap(restartRunning, restartPending) &&
		!s.restartState.CompareAndSwap(restartFailed, restartPending) {
		return errors.New("service restart is already pending")
	}
	s.setConfig(after)
	s.logf("service restart requested")
	s.accepting.Store(false)
	s.stopWorkerIntake()
	s.closeWatcher()
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
		return nil
	}
	s.restartState.Store(restartFailed)
	s.setConfig(before)
	s.accepting.Store(true)
	s.stoppingWorkers.Store(false)
	if before.Scan.WatchdogEnabled {
		if err := s.startWatcher(); err != nil {
			s.logf("watcher restart after process replacement failure failed: %v", err)
		}
	}
	s.logf("critical: process replacement failed after 3 attempts")
	s.events.Publish("service.restart_failed", map[string]any{"status": "restart_failed"})
	return errors.New("process replacement failed after 3 attempts")
}

func (s *Service) waitForCriticalPhaseClear() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for s.hasCriticalPhase() {
		<-ticker.C
	}
}

func (s *Service) Status(ctx context.Context) (any, error) {
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
	filesWithBackup, err := s.db.FilesWithBackup(ctx)
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
	}
	var backupUsage int64
	for _, file := range filesWithBackup {
		info, err := os.Stat(file.BackupFile)
		if err != nil {
			continue
		}
		backupUsage += info.Size()
	}
	return map[string]any{
		"status":                  status,
		"counts":                  counts,
		"backup_usage_bytes":      backupUsage,
		"unresolved_backup_count": len(filesWithBackup),
		"transcode_success_rate":  transcodeSuccessRate(transcodeStats),
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

func (s *Service) RetryHistoryJob(ctx context.Context, id int64) (any, error) {
	var retried repository.JobRecord
	var path string
	err := func() error {
		s.workerIntakeMu.Lock()
		defer s.workerIntakeMu.Unlock()

		job, err := s.db.JobByID(ctx, id)
		if err != nil {
			return err
		}
		if job.Ignored || job.Result != repository.JobResultFailed {
			return api.ErrJobNotActionable
		}
		file, err := s.db.FileByID(ctx, job.FileID)
		if err != nil {
			return err
		}
		if file.BackupFile != "" {
			return api.ErrJobNotActionable
		}
		if _, err := s.db.ReplacementJournalByJob(ctx, job.ID); err == nil {
			return api.ErrJobNotActionable
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !s.canEnqueueLocked(file.Path) || s.queue.Contains(file.Path) {
			return api.ErrProcessing
		}
		if !s.queue.EnqueueExistingJob(file.Path, pipeline.JobSourceManual, job.ID) {
			return api.ErrProcessing
		}
		retried, err = s.db.RestartJob(ctx, job.ID)
		if err != nil {
			s.queue.Done(file.Path)
			if errors.Is(err, sql.ErrNoRows) {
				return api.ErrJobNotActionable
			}
			return err
		}
		path = file.Path
		return nil
	}()
	if err != nil {
		return nil, err
	}
	s.logf("history job retry queued job_id=%d file_id=%d path=%s", retried.ID, retried.FileID, path)
	if s.events != nil {
		s.events.Publish("job.retry_queued", map[string]any{
			"job_id": retried.ID, "file_id": retried.FileID, "path": path,
		})
	}
	return map[string]any{"status": "queued", "job_id": retried.ID, "file_id": retried.FileID, "path": path}, nil
}

func (s *Service) IgnoreHistoryJob(ctx context.Context, id int64) (any, error) {
	job, err := s.db.JobByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if job.Ignored || job.Result != repository.JobResultFailed {
		return nil, api.ErrJobNotActionable
	}
	ignored, err := s.db.IgnoreJob(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, api.ErrJobNotActionable
	}
	if err != nil {
		return nil, err
	}
	s.logf("history job ignored job_id=%d file_id=%d", ignored.ID, ignored.FileID)
	if s.events != nil {
		s.events.Publish("job.ignored", map[string]any{"job_id": ignored.ID, "file_id": ignored.FileID})
	}
	return map[string]any{"status": "ignored", "job_id": ignored.ID, "file_id": ignored.FileID}, nil
}

func (s *Service) ListFiles(ctx context.Context, page *repository.PageRequest) (any, error) {
	if page != nil {
		return s.db.FilesPage(ctx, *page)
	}
	return s.db.Files(ctx)
}

func (s *Service) ProcessFile(ctx context.Context, id int64) (any, error) {
	file, err := s.db.FileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if file.ComplianceStatus != repository.ComplianceNoncompliant || file.BackupFile != "" {
		return nil, api.ErrFileNotProcessable
	}
	if !s.enqueueWithSource(file.Path, pipeline.JobSourceManual) {
		return nil, api.ErrProcessing
	}
	s.logf("manual process queued file_id=%d path=%s", file.ID, file.Path)
	return map[string]any{
		"status":  "queued",
		"file_id": file.ID,
		"path":    file.Path,
	}, nil
}

func (s *Service) RestoreFileBackup(ctx context.Context, id int64) (any, error) {
	file, err := s.db.FileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if file.BackupFile == "" {
		return nil, sql.ErrNoRows
	}
	if !s.beginPathMutation(file.Path) {
		return nil, api.ErrProcessing
	}
	defer s.finishPathMutation(file.Path)

	file, err = s.db.FileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if file.BackupFile == "" {
		return nil, sql.ErrNoRows
	}
	backupPath := file.BackupFile
	cfg := s.config()
	result, err := backup.Restore(backup.RestoreRequest{
		OriginalPath: file.Path,
		BackupPath:   backupPath,
		BackupRoot:   s.backupRoot,
		MediaRoots:   cfg.Media.Roots,
	})
	if err != nil {
		return nil, err
	}
	if err := s.persistRestoredFile(file, backupPath); err != nil {
		return nil, err
	}
	if s.events != nil {
		s.events.Publish("file.backup_restored", map[string]any{"file_id": file.ID, "path": file.Path})
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

func (s *Service) DeleteFileBackup(ctx context.Context, id int64) (any, error) {
	file, err := s.db.FileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !s.beginPathMutation(file.Path) {
		return nil, api.ErrProcessing
	}
	defer s.finishPathMutation(file.Path)

	file, err = s.db.FileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if file.BackupFile != "" {
		if err := backup.Remove(file.BackupFile, s.backupRoot); err != nil {
			return nil, err
		}
		if err := s.db.ClearFileBackup(ctx, file.ID); err != nil {
			return nil, err
		}
	}
	s.logf("backup cleared file_id=%d path=%s backup_file=%s", file.ID, file.Path, file.BackupFile)
	if s.events != nil {
		s.events.Publish("file.backup_deleted", map[string]any{"file_id": file.ID, "path": file.Path})
	}
	return map[string]string{"status": "deleted"}, nil
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
	file, err := s.db.FileByPath(context.Background(), path)
	if err == nil && file.BackupFile != "" {
		s.logf("skip unresolved backup path=%s backup_file=%s source=%s", path, file.BackupFile, source)
		return false
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.logf("skip enqueue because file backup state could not be read path=%s source=%s error=%v", path, source, err)
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

func (s *Service) persistRestoredFile(file repository.FileFacts, backupPath string) error {
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

	if statErr != nil {
		return fmt.Errorf("%s: stat restored file: %w", pipeline.CauseRestoreStatError, statErr)
	}
	if probeErr != nil {
		return fmt.Errorf("%s: probe restored file: %w", pipeline.CauseRestoreProbeError, probeErr)
	}
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
		return fmt.Errorf("%s: assess restored file: %s", pipeline.CauseUnsupported, decision.Reason)
	}
	if err := backup.Remove(backupPath, s.backupRoot); err != nil {
		return fmt.Errorf("delete restored backup: %w", err)
	}
	file.BackupFile = ""

	persistCtx, cancelPersist := context.WithTimeout(context.Background(), s.restorePersistDeadline())
	defer cancelPersist()
	_, err := s.db.PersistRestoredFile(persistCtx, file)
	if err != nil {
		return err
	}
	return nil
}

func (s *Service) isCandidatePath(path string) bool {
	return isCandidatePathForMedia(path, s.config().Media)
}

func isCandidatePathForMedia(path string, cfg config.MediaConfig) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if pipeline.IsPathExcluded(path, cfg.ExcludeDirs, cfg.ExcludePatterns) {
		return false
	}
	for _, candidate := range cfg.Extensions {
		if strings.ToLower(candidate) == ext {
			return !pipeline.IsAudioCleanerTempOutputPath(path)
		}
	}
	return false
}

func (s *Service) closeWatcher() {
	s.watcherMu.Lock()
	watcher := s.watcher
	s.watcher = nil
	s.watcherMu.Unlock()
	if watcher != nil {
		if err := watcher.Close(); err != nil {
			s.logf("watcher close failed: %v", err)
		}
	}
}

func (s *Service) Apply(_ context.Context, module settings.Module, before, after config.Config) error {
	switch module {
	case settings.Media:
		if err := s.reloadWatcher(after); err != nil {
			return err
		}
	case settings.Pipeline:
		s.setConfig(after)
		s.resizeWorkers(after.Pipeline.Workers)
	case settings.Audio, settings.Validation, settings.Scan, settings.UI:
		s.setConfig(after)
	default:
		return fmt.Errorf("unsupported config module %q", module)
	}
	if s.events != nil {
		s.events.Publish("config.applied", map[string]any{"module": module, "apply_mode": settings.ApplyModeFor(module)})
	}
	return nil
}

func (s *Service) Rollback(ctx context.Context, module settings.Module, before config.Config) error {
	switch module {
	case settings.Media:
		return s.reloadWatcher(before)
	case settings.Pipeline:
		s.setConfig(before)
		s.resizeWorkers(before.Pipeline.Workers)
	default:
		s.setConfig(before)
	}
	if s.events != nil {
		s.events.Publish("config.rolled_back", map[string]any{"module": module})
	}
	return nil
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
