package app

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
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
	"omv-blueprint/compose/audiocleaner/internal/compatibility"
	"omv-blueprint/compose/audiocleaner/internal/config"
	"omv-blueprint/compose/audiocleaner/internal/eventbus"
	"omv-blueprint/compose/audiocleaner/internal/failure"
	"omv-blueprint/compose/audiocleaner/internal/media"
	"omv-blueprint/compose/audiocleaner/internal/observability"
	"omv-blueprint/compose/audiocleaner/internal/pipeline"
	"omv-blueprint/compose/audiocleaner/internal/replacement"
	"omv-blueprint/compose/audiocleaner/internal/repository"
	"omv-blueprint/compose/audiocleaner/internal/settings"
)

const (
	defaultConfigPath  = "/app/config/config.json"
	defaultDBPath      = "/app/data/audiocleaner.db"
	defaultBackupRoot  = "/app/backups"
	defaultHTTPAddr    = ":9830"
	defaultLogPath     = "/app/logs/audiocleaner.log"
	defaultWorkRoot    = "/app/work"
	defaultWebDir      = "/app/web/dist"
	defaultLogMaxBytes = int64(10 * 1024 * 1024)
	defaultLogBackups  = 5
	defaultLogMaxAge   = 30 * 24 * time.Hour

	startupCriticalRecoveryPrefix = "startup recovered interrupted critical phase "
)

var defaultRetentionPolicy = repository.RetentionPolicy{
	HistoryMaxAge: 90 * 24 * time.Hour, HistoryMaxCount: 10_000,
	MissingFileMaxAge: 30 * 24 * time.Hour, MissingFileMaxCount: 5_000,
}

const retentionLoopInterval = 24 * time.Hour

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
}

type runtimeWatcher interface {
	Close() error
	Errors() <-chan error
}

type Service struct {
	cfg             config.Config
	cfgMu           sync.RWMutex
	configPath      string
	db              *repository.Repository
	events          *eventbus.Bus
	queue           *pipeline.Queue
	watcher         runtimeWatcher
	watcherMu       sync.Mutex
	server          *http.Server
	logger          *log.Logger
	logSink         io.Closer
	logPath         string
	space           observability.SpaceProvider
	retentionPolicy repository.RetentionPolicy

	backupRoot  string
	workRoot    string
	webDir      string
	httpAddr    string
	ffmpegName  string
	ffprobeName string
	prober      pipeline.WorkerProber

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

	scanMu                 sync.RWMutex
	scanCurrent            ScanSession
	scanHasCurrent         bool
	scanCancel             context.CancelFunc
	scanSequence           uint64
	lastScanVisited        int64
	pendingScanSource      ScanSource
	discoveryWatcherStatus string
	discoveryWatcherError  string
	discoveryNextAt        time.Time
	discoveryLastResult    *ReconciliationResult
	discoveryRecovery      *RecoveryResult
}

type workerJob struct {
	task   pipeline.QueueJob
	ctx    context.Context
	cancel context.CancelFunc
}

type enqueueDisposition uint8

const (
	enqueueRejected enqueueDisposition = iota
	enqueueAccepted
	enqueueDuplicate
)

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
	logger, logSink, err := newFileLogger(opts.LogPath, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	cfg, err := config.LoadOrCreate(opts.ConfigPath)
	if err != nil {
		_ = logSink.Close()
		return nil, fmt.Errorf("load config: %w", err)
	}
	repo, err := repository.Open(ctx, opts.DBPath)
	if err != nil {
		_ = logSink.Close()
		return nil, fmt.Errorf("open repository: %w", err)
	}

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	service := &Service{
		cfg:               cfg,
		configPath:        opts.ConfigPath,
		db:                repo,
		events:            eventbus.New(),
		queue:             pipeline.NewQueue(),
		logger:            logger,
		logSink:           logSink,
		logPath:           opts.LogPath,
		space:             diskSpaceProvider{},
		retentionPolicy:   defaultRetentionPolicy,
		backupRoot:        opts.BackupRoot,
		workRoot:          opts.WorkRoot,
		webDir:            opts.WebDir,
		httpAddr:          opts.HTTPAddr,
		ffmpegName:        "ffmpeg",
		ffprobeName:       "ffprobe",
		prober:            ffprobeProber{name: "ffprobe"},
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
		_ = logSink.Close()
		cancelWorker()
		return nil, fmt.Errorf("start http server: %w", err)
	}
	if err := service.recoverReplacementJournals(ctx); err != nil {
		_ = listener.Close()
		_ = repo.Close()
		_ = logSink.Close()
		cancelWorker()
		return nil, fmt.Errorf("recover interrupted replacements: %w", err)
	}
	if err := RecoverStartup(ctx, repo); err != nil {
		_ = listener.Close()
		_ = repo.Close()
		_ = logSink.Close()
		cancelWorker()
		return nil, fmt.Errorf("recover startup jobs: %w", err)
	}
	if err := service.cleanupOrphanTempOutputs(ctx); err != nil {
		service.logf("startup temp cleanup failed: %v", err)
	}
	service.runRetention(ctx)
	if cfg.Scan.WatchdogEnabled {
		if err := service.startWatcher(); err != nil {
			service.logf("watcher start failed: %v", err)
			service.markWatcherError(err)
		}
	} else {
		service.markWatcherDisabled()
	}
	service.startWorkers()
	service.startReconciliationLoop()
	service.startRetentionLoop()
	service.serveHTTP(listener)
	if cfg.Scan.StartupScanEnabled {
		if _, _, err := service.startManagedScan(ScanSourceStartup, false); err != nil {
			service.logf("startup scan failed: %v", err)
		}
	}
	return service, nil
}

func (s *Service) recoverReplacementJournals(ctx context.Context) error {
	journals, err := s.db.ReplacementJournals(ctx)
	if err != nil {
		return err
	}
	for _, journal := range journals {
		criticalCtx, cancel := context.WithTimeout(context.Background(), defaultStartupCriticalTimeout)
		err := s.recoverReplacementJournal(criticalCtx, journal)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

const defaultStartupCriticalTimeout = 5 * time.Minute

type recoveryObservation struct {
	replacement.Observation
	info  os.FileInfo
	probe media.ProbeData
}

func (s *Service) recoverReplacementJournal(ctx context.Context, journal repository.ReplacementJournal) error {
	if journal.Phase == repository.ReplacementCommitted {
		return s.cleanupReplacementJournal(ctx, journal)
	}
	file, err := s.db.FileByID(ctx, journal.FileID)
	if err != nil {
		return err
	}
	if journal.Phase == repository.ReplacementManualRecovery {
		if err := s.setRecoveryFailureDetails(ctx, journal.JobID, "manual_recovery", journal.LastError); err != nil {
			return err
		}
		if file.BackupFile == journal.TemporaryBackupPath {
			return nil
		}
		return s.db.SetFileBackup(ctx, file.ID, journal.TemporaryBackupPath)
	}
	if journal.Phase == repository.ReplacementPrepared || journal.Phase == repository.ReplacementBackupReady {
		return s.finishInterruptedReplacement(ctx, journal, file, recoveryObservation{}, "installation did not start")
	}

	observed, observeErr := s.observeRecoveryPath(ctx, journal.OriginalPath)
	if observeErr != nil {
		return s.preserveManualRecovery(ctx, journal, file, recoveryObservation{}, "observe original path: "+observeErr.Error())
	}
	action := replacement.DecideRecovery(journal.Phase, observed.Observation, journal.OriginalEvidence(), journal.OutputEvidence())
	switch action {
	case replacement.RecoveryCommitInstalled:
		return s.commitRecoveredInstallation(ctx, journal, file, observed)
	case replacement.RecoveryAbort, replacement.RecoveryRestored:
		return s.finishInterruptedReplacement(ctx, journal, file, observed, string(action))
	case replacement.RecoveryRestore:
		return s.restoreInterruptedReplacement(ctx, journal, file)
	default:
		return s.preserveManualRecovery(ctx, journal, file, observed, "replacement evidence is ambiguous")
	}
}

func (s *Service) observeRecoveryPath(ctx context.Context, path string) (recoveryObservation, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return recoveryObservation{}, nil
	}
	if err != nil {
		return recoveryObservation{}, err
	}
	prober := s.prober
	if prober == nil {
		prober = ffprobeProber{name: s.ffprobeName}
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.restoreProbeDeadline())
	defer cancel()
	probe, err := prober.Probe(probeCtx, path)
	if err != nil {
		return recoveryObservation{}, err
	}
	evidence := replacement.Evidence{
		Size: info.Size(), MTimeNS: info.ModTime().UnixNano(),
		AudioSignature: probe.AudioSignature(), VideoSignature: probe.VideoSignature(),
	}
	return recoveryObservation{Observation: replacement.Observation{Exists: true, Evidence: evidence}, info: info, probe: probe}, nil
}

func (s *Service) commitRecoveredInstallation(ctx context.Context, journal repository.ReplacementJournal, file repository.FileFacts, observed recoveryObservation) error {
	if journal.QualityAssessment == nil || !journal.QualityAssessment.Passed {
		return s.preserveManualRecovery(ctx, journal, file, observed, "installed output has no passed quality assessment")
	}
	s.applyRecoveryObservation(&file, observed)
	if file.ComplianceStatus != repository.ComplianceCompliant {
		return s.preserveManualRecovery(ctx, journal, file, observed, "installed output is not compatible")
	}
	file.BackupFile = ""
	if _, _, err := s.db.FinishProcessJob(ctx, journal.JobID, file, repository.JobResultSucceeded, ""); err != nil {
		return err
	}
	return s.cleanupReplacementJournal(ctx, journal)
}

func (s *Service) restoreInterruptedReplacement(ctx context.Context, journal repository.ReplacementJournal, file repository.FileFacts) error {
	if err := s.db.UpdateReplacementPhase(ctx, journal.JobID, repository.ReplacementRestoring, "startup automatic restore"); err != nil {
		return err
	}
	cfg := s.config()
	if _, err := backup.Restore(backup.RestoreRequest{
		OriginalPath: journal.OriginalPath, BackupPath: journal.TemporaryBackupPath,
		BackupRoot: s.backupRoot, MediaRoots: cfg.Media.Roots,
	}); err != nil {
		return s.preserveManualRecovery(ctx, journal, file, recoveryObservation{}, "automatic restore failed: "+err.Error())
	}
	observed, err := s.observeRecoveryPath(ctx, journal.OriginalPath)
	if err != nil || !journal.OriginalEvidence().Matches(observed.Evidence) {
		reason := "restored file does not match original evidence"
		if err != nil {
			reason = "observe restored file: " + err.Error()
		}
		return s.preserveManualRecovery(ctx, journal, file, observed, reason)
	}
	return s.finishInterruptedReplacement(ctx, journal, file, observed, "automatic restore completed")
}

func (s *Service) finishInterruptedReplacement(ctx context.Context, journal repository.ReplacementJournal, file repository.FileFacts, observed recoveryObservation, reason string) error {
	if observed.Exists {
		s.applyRecoveryObservation(&file, observed)
	}
	file.BackupFile = ""
	finalError := "interrupted_replacement: " + reason
	if err := s.setRecoveryFailureDetails(ctx, journal.JobID, "interrupted_replacement", finalError); err != nil {
		return err
	}
	if _, _, err := s.db.FinishProcessJob(ctx, journal.JobID, file, repository.JobResultFailed, finalError); err != nil {
		return err
	}
	return s.cleanupReplacementJournal(ctx, journal)
}

func (s *Service) preserveManualRecovery(ctx context.Context, journal repository.ReplacementJournal, file repository.FileFacts, observed recoveryObservation, reason string) error {
	if observed.Exists {
		s.applyRecoveryObservation(&file, observed)
	}
	file.BackupFile = journal.TemporaryBackupPath
	finalError := "manual_recovery: " + reason
	if err := s.setRecoveryFailureDetails(ctx, journal.JobID, "manual_recovery", finalError); err != nil {
		return err
	}
	if _, _, err := s.db.FinishProcessJob(ctx, journal.JobID, file, repository.JobResultFailed, finalError); err != nil {
		return err
	}
	s.logf("startup preserved replacement candidates file_id=%d path=%s backup=%s output=%s reason=%s", file.ID, file.Path, journal.TemporaryBackupPath, journal.OutputPath, reason)
	return nil
}

func (s *Service) setRecoveryFailureDetails(ctx context.Context, jobID int64, code, detail string) error {
	classification := failure.Classify(code, detail)
	return s.db.SetJobFailureDetails(ctx, jobID, repository.FailureDetails{
		Stage: classification.Stage, Category: string(classification.Category), Code: classification.Code,
		Summary: classification.Summary, Advice: classification.Advice,
		RetryStrategy: string(classification.Strategy), UnlockCondition: classification.UnlockCondition,
	})
}

func (s *Service) applyRecoveryObservation(file *repository.FileFacts, observed recoveryObservation) {
	file.Size = observed.info.Size()
	file.MTimeNS = observed.info.ModTime().UnixNano()
	file.AudioSignature = observed.probe.AudioSignature()
	file.VideoSignature = observed.probe.VideoSignature()
	cfg := s.config()
	decision := media.Decide(file.Path, observed.probe, media.DecisionConfig{
		Extensions: cfg.Media.Extensions, IncompatibleCodecs: cfg.Audio.IncompatibleCodecs,
	})
	file.AudioPolicyVersion = cfg.Audio.Version
	switch decision.Action {
	case media.ActionAlreadyCompatible:
		file.ComplianceStatus = repository.ComplianceCompliant
	case media.ActionTranscode:
		file.ComplianceStatus = repository.ComplianceNoncompliant
	default:
		file.ComplianceStatus = repository.ComplianceUnknown
		file.AudioPolicyVersion = 0
	}
	assessment := compatibility.BuildAssessment(observed.probe, decision, compatibility.Policy{
		Version: cfg.Audio.Version, IncompatibleCodecs: cfg.Audio.IncompatibleCodecs,
	}, time.Now())
	file.CompatibilityAssessment = &assessment
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
	return opts
}

type nopLogSink struct{}

func (nopLogSink) Close() error { return nil }

func newFileLogger(path string, stderr io.Writer) (*log.Logger, io.Closer, error) {
	if stderr == nil {
		stderr = io.Discard
	}
	if path == "" {
		return log.New(stderr, "audiocleaner: ", log.LstdFlags), nopLogSink{}, nil
	}
	file, err := observability.NewRotatingFileWriterWithAge(path, defaultLogMaxBytes, defaultLogBackups, defaultLogMaxAge)
	if err != nil {
		return nil, nil, err
	}
	writer := io.MultiWriter(stderr, file)
	return log.New(writer, "audiocleaner: ", log.LstdFlags), file, nil
}

func (s *Service) startRetentionLoop() {
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		ticker := time.NewTicker(retentionLoopInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.workerCtx.Done():
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				s.runRetention(ctx)
				cancel()
			}
		}
	}()
}

func (s *Service) runRetention(ctx context.Context) {
	if s.db == nil {
		return
	}
	result, err := s.db.PruneRetention(ctx, s.retentionPolicy, time.Now())
	if err != nil {
		s.logf("retention cleanup failed: %v", err)
		return
	}
	if result.DeletedJobs > 0 || result.DeletedFiles > 0 {
		s.logf("retention cleanup completed deleted_jobs=%d deleted_files=%d", result.DeletedJobs, result.DeletedFiles)
	}
}

func (s *Service) ScanAll(ctx context.Context) error {
	_, _, err := s.startManagedScan(ScanSourceManual, false)
	return err
}

func (s *Service) startWatcher() error {
	return s.reloadWatcher(s.config())
}

func (s *Service) reloadWatcher(cfg config.Config) error {
	if !cfg.Scan.WatchdogEnabled {
		s.setConfig(cfg)
		s.closeWatcher()
		s.markWatcherDisabled()
		return nil
	}
	mediaConfig := cfg.Media
	candidate, err := pipeline.NewFilteredWatcher(pipeline.ScanConfig{
		Roots:           mediaConfig.Roots,
		Extensions:      mediaConfig.Extensions,
		ExcludeDirs:     mediaConfig.ExcludeDirs,
		ExcludePatterns: mediaConfig.ExcludePatterns,
	}, func(path string) {
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
	s.markWatcherRunning()
	if old != nil {
		_ = old.Close()
	}
	s.workers.Add(1)
	go s.monitorWatcher(candidate)
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
			s.processActiveJob(job)
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

func (s *Service) processActiveJob(job workerJob) {
	snapshot := s.config()
	worker := s.newWorker(snapshot)
	var result pipeline.AttemptResult
	var processErr error
	if job.ctx.Err() == nil {
		result, processErr = worker.ProcessAttempt(job.ctx, job.task)
	}
	job.cancel()
	s.processAttemptResult(job.task, result, processErr)
}

func (s *Service) processAttemptResult(task pipeline.QueueJob, result pipeline.AttemptResult, processErr error) {
	if task.JobID == 0 && result.JobID != 0 && !s.queue.BindJob(task.Path, result.JobID) {
		s.logf("worker failed to bind job id=%d path=%s", result.JobID, task.Path)
	}
	retryScheduled := false
	classification := failure.Classify(failureCode(result.FinalError), result.LastError)
	if processErr != nil && shouldRetryAttempt(classification, result) {
		pipelineConfig := s.config().Pipeline
		maxRetries := pipelineConfig.MaxRetries
		if classification.Category == failure.SourceChanged && maxRetries < task.AttemptNumber {
			// A source-version conflict is condition-gated reanalysis, not a retry of
			// the discarded output. Keep waiting for each newly stable version.
			maxRetries = task.AttemptNumber
		}
		retryScheduled = s.scheduleRetryFor(
			task.Path,
			failure.RetryDelay(pipelineConfig.RetryDelay(), task.AttemptNumber),
			result.LastError,
			maxRetries,
			classification,
			result.FinalFile.Size,
		)
	}
	if retryScheduled {
		recordCtx, cancelRecord := context.WithTimeout(context.Background(), 30*time.Second)
		s.recordFailureIssue(recordCtx, result, task, classification, true)
		cancelRecord()
		waitPhase := pipeline.RuntimeRetryWait
		if classification.Category == failure.Capacity {
			waitPhase = pipeline.RuntimeCapacityWait
		}
		s.publishWorkerEvent(pipeline.WorkerEvent{FileID: result.FileID, Path: task.Path, Kind: "phase_transition", Code: string(waitPhase), Phase: waitPhase, Attempt: task.AttemptNumber, Error: result.LastError})
	} else {
		if processErr != nil && result.JobID != 0 {
			finalError := result.FinalError
			if finalError == "" {
				finalError = processErr.Error()
			}
			finishCtx, cancelFinish := context.WithTimeout(context.Background(), 30*time.Second)
			_, _, finishErr := s.db.FinishProcessJob(finishCtx, result.JobID, result.FinalFile, repository.JobResultFailed, finalError)
			if finishErr == nil {
				s.recordFailureIssue(finishCtx, result, task, classification, false)
				s.completeRetryAudit(finishCtx, result.JobID, "failed", finalError)
			}
			cancelFinish()
			if finishErr != nil {
				s.logf("finish failed job id=%d: %v", result.JobID, finishErr)
			} else if result.FinalFile.BackupFile != "" {
				journalCtx, cancelJournal := context.WithTimeout(context.Background(), 30*time.Second)
				err := s.db.DeleteReplacementJournal(journalCtx, result.JobID)
				cancelJournal()
				if err != nil {
					s.logf("delete unresolved backup journal job_id=%d: %v", result.JobID, err)
				}
				if s.events != nil {
					s.events.Publish("file.backup_created", map[string]any{"file_id": result.FinalFile.ID, "path": result.FinalFile.Path, "backup_file": result.FinalFile.BackupFile})
				}
			}
			outcome := result.FailureOutcome
			if outcome == "" {
				outcome = "failed"
			}
			s.publishWorkerEvent(pipeline.WorkerEvent{FileID: result.FileID, Path: task.Path, Kind: "status_change", Code: "failed", Status: "failed", Outcome: outcome, Attempt: task.AttemptNumber, Error: result.LastError})
		} else if result.FailureOutcome != "" && result.JobID != 0 {
			recordCtx, cancelRecord := context.WithTimeout(context.Background(), 30*time.Second)
			s.recordFailureIssue(recordCtx, result, task, classification, false)
			s.completeRetryAudit(recordCtx, result.JobID, "failed", result.FinalError)
			cancelRecord()
		} else if processErr == nil && result.FileID != 0 {
			recordCtx, cancelRecord := context.WithTimeout(context.Background(), 30*time.Second)
			if err := s.db.ResolveFailureIssue(recordCtx, result.FileID); err != nil {
				s.logf("resolve successful failure issue file_id=%d: %v", result.FileID, err)
			}
			if result.JobID != 0 {
				s.completeRetryAudit(recordCtx, result.JobID, "succeeded", "analysis completed")
			}
			cancelRecord()
		}
		s.queue.Done(task.Path)
	}
	if processErr != nil {
		s.logf("worker error: %v", processErr)
	}
}

func shouldRetryAttempt(classification failure.Classification, result pipeline.AttemptResult) bool {
	if !classification.AutoRetry {
		return false
	}
	return classification.Category != failure.SourceChanged || result.Retryable
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
		Space:          s.space,
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
		Scans:            s,
		Discovery:        s,
		RequestRestart:   s.RequestRestart,
		ConfigApplicator: s,
		Status:           s,
		RuntimeTasks:     s,
		History:          s,
		Files:            s,
		RuntimeLogs:      s,
		Recovery:         s,
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
	s.cancelManagedScanForShutdown()
	s.closeWatcher()
	s.cancelNonCriticalActiveJobs()
	s.waitForCriticalPhaseClear()
	s.cancelNonCriticalActiveJobs()
	s.events.Publish("service.restarting", map[string]any{"status": "restarting"})
	for attempt := 1; attempt <= 3; attempt++ {
		if err := execSelf(); err != nil {
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
	processStatus := "running"
	switch s.restartState.Load() {
	case restartPending:
		processStatus = "restarting"
	case restartFailed:
		processStatus = "restart_failed"
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
	issues, err := s.db.ActiveFailureIssues(ctx)
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
	cfg := s.config()
	capacity := serviceCapacitySnapshot(s.space, s.backupRoot, s.workRoot, cfg.Media.Roots, 0, 0)
	manualFiles := make(map[int64]struct{}, len(filesWithBackup))
	for _, file := range filesWithBackup {
		manualFiles[file.ID] = struct{}{}
	}
	for _, issue := range issues {
		if issue.Category == string(failure.Recovery) {
			manualFiles[issue.FileID] = struct{}{}
		}
	}
	s.scanMu.RLock()
	watcherStatus := s.discoveryWatcherStatus
	watcherError := s.discoveryWatcherError
	s.scanMu.RUnlock()
	health := observability.EvaluateHealth(observability.HealthInput{
		ProcessStatus: processStatus, CapacityReady: capacity.Ready, CapacityReasons: capacity.Blocking,
		WatcherStatus: watcherStatus, WatcherError: watcherError, ManualRecoveryCount: len(manualFiles),
	})
	intakeAccepting := s.accepting.Load() && health.Status != observability.StatusIntakeStopped && health.Status != observability.StatusManualRecovery
	return map[string]any{
		"status":                  health.Status,
		"process_status":          health.ProcessStatus,
		"health_reasons":          health.Reasons,
		"capacity":                capacity,
		"intake_accepting":        intakeAccepting,
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
		_, retried, err = s.db.StartManualRetryJob(ctx, file, job.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return api.ErrJobNotActionable
			}
			return err
		}
		if !s.enqueueExistingJob(file.Path, pipeline.JobSourceManual, retried.ID) {
			_, _ = s.db.FinishJob(ctx, retried.ID, repository.JobResultFailed, "queue_error: unable to enqueue manual retry")
			return api.ErrProcessing
		}
		path = file.Path
		_, err = s.db.CreateRecoveryAudit(ctx, repository.RecoveryAudit{
			FileID: retried.FileID, JobID: retried.ID, OriginalJobID: job.ID,
			OriginalFailureCode: job.FailureCode, OriginalFailure: job.FinalError, Actor: "local_operator",
			Action: "retry", Source: "manual_history", Result: "queued", Detail: "failure suppression bypassed once",
		})
		if err != nil {
			s.queue.Done(file.Path)
			_, _ = s.db.FinishJob(ctx, retried.ID, repository.JobResultFailed, "audit_error: "+err.Error())
			return err
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}
	s.logf("history job retry queued job_id=%d file_id=%d path=%s", retried.ID, retried.FileID, path)
	if err := s.db.ConfirmFailureAttempt(ctx, retried.FileID, "manual_history"); err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.logf("confirm history retry issue job_id=%d: %v", retried.ID, err)
	}
	if s.events != nil {
		s.events.Publish("job.retry_queued", map[string]any{
			"job_id": retried.ID, "file_id": retried.FileID, "path": path,
		})
	}
	return map[string]any{"status": "queued", "job_id": retried.ID, "file_id": retried.FileID, "path": path}, nil
}

func (s *Service) enqueueExistingJob(path string, source pipeline.JobSource, jobID int64) bool {
	return s.queue.EnqueueExistingJob(path, source, jobID)
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
	files, err := s.db.Files(ctx)
	if err != nil {
		return nil, err
	}
	mediaConfig := s.config().Media
	filtered := make([]repository.FileFacts, 0, len(files))
	for _, file := range files {
		if file.MissingAt == nil && isCandidatePathForMedia(file.Path, mediaConfig) {
			filtered = append(filtered, file)
		}
	}
	if page == nil {
		return filtered, nil
	}

	request := page.Normalize()
	start := request.Offset()
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + request.PageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	items := append(make([]repository.FileFacts, 0, end-start), filtered[start:end]...)
	return repository.PageResult[repository.FileFacts]{
		Items: items, Page: request.Page, PageSize: request.PageSize, Total: len(filtered),
	}, nil
}

func (s *Service) ProcessFile(ctx context.Context, id int64) (any, error) {
	file, err := s.db.FileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	issue, issueErr := s.db.ActiveFailureIssueByFile(ctx, id)
	if issueErr != nil && !errors.Is(issueErr, sql.ErrNoRows) {
		return nil, issueErr
	}
	if (file.ComplianceStatus != repository.ComplianceNoncompliant && errors.Is(issueErr, sql.ErrNoRows)) || file.BackupFile != "" {
		return nil, api.ErrFileNotProcessable
	}
	var jobID int64
	if issueErr == nil {
		jobID, err = s.retryFailedFile(ctx, file, issue, "manual_file")
		if err != nil {
			return nil, err
		}
	} else if !s.enqueueWithSource(file.Path, pipeline.JobSourceManual) {
		return nil, api.ErrProcessing
	}
	s.logf("manual process queued file_id=%d path=%s", file.ID, file.Path)
	response := map[string]any{
		"status":  "queued",
		"file_id": file.ID,
		"path":    file.Path,
	}
	if jobID != 0 {
		response["job_id"] = jobID
	}
	return response, nil
}

func (s *Service) RestoreFileBackup(ctx context.Context, id int64) (any, error) {
	file, err := s.loadFileByID(ctx, id)
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

	file, err = s.loadFileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if file.BackupFile == "" {
		return nil, sql.ErrNoRows
	}
	backupPath := file.BackupFile
	journal, journalErr := s.db.ReplacementJournalByFile(ctx, file.ID)
	if journalErr != nil && !errors.Is(journalErr, sql.ErrNoRows) {
		return nil, journalErr
	}
	var originalJob repository.JobRecord
	if journalErr == nil {
		originalJob, err = s.db.JobByID(ctx, journal.JobID)
		if err != nil {
			return nil, err
		}
	}
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
	var expectedEvidence *replacement.Evidence
	if journalErr == nil {
		evidence := journal.OriginalEvidence()
		expectedEvidence = &evidence
	}
	if err := s.persistRestoredFileWithEvidence(file, backupPath, expectedEvidence); err != nil {
		return nil, err
	}
	if journalErr == nil {
		if err := s.cleanupReplacementJournal(ctx, journal); err != nil {
			return nil, err
		}
	}
	if s.events != nil {
		s.events.Publish("file.backup_restored", map[string]any{"file_id": file.ID, "path": file.Path})
	}
	_ = s.db.ResolveFailureIssue(ctx, file.ID)
	audit := repository.RecoveryAudit{
		FileID: file.ID, JobID: journal.JobID, Actor: "local_operator",
		Action: "restore", Source: "recovery_center", Result: "succeeded", Detail: backupPath,
	}
	if journalErr == nil {
		audit.OriginalJobID = originalJob.ID
		audit.OriginalFailureCode = originalJob.FailureCode
		audit.OriginalFailure = originalJob.FinalError
	}
	if err := s.db.AddRecoveryAudit(ctx, audit); err != nil {
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

func (s *Service) DeleteFileBackup(ctx context.Context, id int64) (any, error) {
	file, err := s.loadFileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !s.beginPathMutation(file.Path) {
		return nil, api.ErrProcessing
	}
	defer s.finishPathMutation(file.Path)

	file, err = s.loadFileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, journalErr := s.db.ReplacementJournalByFile(ctx, file.ID); journalErr == nil {
		return nil, api.ErrFileNotProcessable
	} else if !errors.Is(journalErr, sql.ErrNoRows) {
		return nil, journalErr
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
	_ = s.db.AddRecoveryAudit(ctx, repository.RecoveryAudit{FileID: file.ID, Action: "delete", Source: "recovery_center", Result: "deleted", Detail: file.BackupFile})
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

func (s *Service) loadFileByID(ctx context.Context, id int64) (repository.FileFacts, error) {
	return s.db.FileByID(ctx, id)
}

func (s *Service) loadJobByID(ctx context.Context, id int64) (repository.JobRecord, error) {
	return s.db.JobByID(ctx, id)
}

func (s *Service) RuntimeLogs(ctx context.Context, lines int) (any, error) {
	content, count, err := tailLogFile(ctx, s.logPath, lines)
	if err != nil {
		return nil, err
	}
	return api.RuntimeLogResult{Content: content, Lines: count}, nil
}

func (s *Service) enqueueWithSource(path string, source pipeline.JobSource) bool {
	disposition, _ := s.enqueueWithSourceResult(path, source)
	return disposition == enqueueAccepted
}

func (s *Service) enqueueWithSourceResult(path string, source pipeline.JobSource) (enqueueDisposition, error) {
	s.workerIntakeMu.Lock()
	defer s.workerIntakeMu.Unlock()
	if !s.canEnqueueLocked(path) {
		return enqueueRejected, nil
	}
	file, err := s.db.FileByPath(context.Background(), path)
	if err == nil && file.BackupFile != "" {
		s.logf("skip unresolved backup path=%s backup_file=%s source=%s", path, file.BackupFile, source)
		return enqueueRejected, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.logf("skip enqueue because file backup state could not be read path=%s source=%s error=%v", path, source, err)
		return enqueueRejected, err
	}
	if !s.queue.EnqueueWithSource(path, source) {
		return enqueueDuplicate, nil
	}
	return enqueueAccepted, nil
}

func (s *Service) scheduleRetry(path string, delay time.Duration, lastError string, maxRetries int) bool {
	return s.scheduleRetryFor(path, delay, lastError, maxRetries, failure.Classification{}, 0)
}

func (s *Service) scheduleRetryFor(path string, delay time.Duration, lastError string, maxRetries int, classification failure.Classification, fileSize int64) bool {
	s.workerIntakeMu.Lock()
	defer s.workerIntakeMu.Unlock()
	if s.queue == nil || !s.isCandidatePath(path) {
		return false
	}
	checkInterval := delay
	if checkInterval > 30*time.Second {
		checkInterval = 30 * time.Second
	}
	if checkInterval < time.Second {
		checkInterval = time.Second
	}
	switch classification.Category {
	case failure.Capacity:
		return s.queue.ScheduleRetryWithObservation(path, delay, checkInterval, lastError, maxRetries, pipeline.RuntimeCapacityWait, lastError, "三处容量均达到安全余量", func() bool {
			cfg := s.config()
			maxOutput := workerOutputEstimate(fileSize, cfg.Validation.MaxSizeRatio, cfg.Validation.MaxSizeIncreaseMegabyte)
			return serviceCapacitySnapshot(s.space, s.backupRoot, s.workRoot, []string{path}, fileSize, maxOutput).Ready
		})
	case failure.SourceChanged:
		quiet := s.config().Pipeline.StatQuietDuration()
		return s.queue.ScheduleRetryWithCondition(path, delay, checkInterval, lastError, maxRetries, func() bool {
			return retrySourceStable(path, quiet, time.Now())
		})
	}
	return s.queue.ScheduleRetry(path, delay, lastError, maxRetries)
}

func retryCapacityAvailable(path string, fileSize int64) bool {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(path), &stats); err != nil {
		return false
	}
	available := int64(stats.Bavail) * int64(stats.Bsize)
	if fileSize < 0 {
		fileSize = 0
	}
	const reserve = int64(64 * 1024 * 1024)
	if fileSize > (math.MaxInt64-reserve)/2 {
		return false
	}
	required := fileSize*2 + reserve
	return available >= required
}

func workerOutputEstimate(sourceBytes int64, ratio float64, maxIncreaseMB int64) int64 {
	maxOutput := math.Max(float64(sourceBytes)*ratio, float64(sourceBytes)+float64(maxIncreaseMB*1024*1024))
	if math.IsNaN(maxOutput) || maxOutput < 0 {
		return -1
	}
	if maxOutput > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(maxOutput)
}

func serviceCapacitySnapshot(space observability.SpaceProvider, backupRoot, workRoot string, mediaRoots []string, sourceBytes, maxOutputBytes int64) observability.CapacityResult {
	mediaPath := ""
	if len(mediaRoots) > 0 {
		mediaPath = mediaRoots[0]
	}
	result := observability.EvaluateCapacity(space, observability.CapacityRequest{
		SourceBytes: sourceBytes, MaxOutputBytes: maxOutputBytes, ReserveBytes: observability.DefaultReserveBytes,
		BackupPath: backupRoot, WorkPath: workRoot, MediaPath: mediaPath,
	})
	for _, root := range mediaRoots[1:] {
		extra := observability.EvaluateCapacity(space, observability.CapacityRequest{
			SourceBytes: sourceBytes, MaxOutputBytes: maxOutputBytes, ReserveBytes: observability.DefaultReserveBytes,
			BackupPath: backupRoot, WorkPath: workRoot, MediaPath: root,
		})
		media := extra.Volumes[2]
		result.Volumes = append(result.Volumes, media)
		if !media.Ready {
			result.Ready = false
			result.Blocking = append(result.Blocking, extra.Blocking[len(extra.Blocking)-1])
		}
	}
	return result
}

type diskSpaceProvider struct{}

const (
	accessExecute uint32 = 1
	accessWrite   uint32 = 2
	accessRead    uint32 = 4
)

func (provider diskSpaceProvider) AvailableBytes(path string) (int64, error) {
	current := filepath.Clean(path)
	for {
		if _, err := os.Stat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return 0, fmt.Errorf("no existing parent for %s", path)
		}
		current = parent
	}
	var stats syscall.Statfs_t
	if err := syscall.Statfs(current, &stats); err != nil {
		return 0, err
	}
	return int64(stats.Bavail) * int64(stats.Bsize), nil
}

func (provider diskSpaceProvider) CheckCapability(path, capability string) error {
	current := filepath.Clean(path)
	info, err := os.Stat(current)
	if err != nil {
		if capability == "media" || !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check %s path %s: %w", capability, path, err)
		}
		for errors.Is(err, os.ErrNotExist) {
			parent := filepath.Dir(current)
			if parent == current {
				return fmt.Errorf("no existing parent for %s", path)
			}
			current = parent
			info, err = os.Stat(current)
		}
		if err != nil {
			return fmt.Errorf("check %s path %s: %w", capability, path, err)
		}
	}

	switch capability {
	case "backup", "work":
		if !info.IsDir() {
			return fmt.Errorf("%s path is not a directory: %s", capability, current)
		}
		if err := syscall.Access(current, accessWrite|accessExecute); err != nil {
			return fmt.Errorf("%s path is not writable: %w", capability, err)
		}
	case "media":
		if info.IsDir() {
			if err := syscall.Access(current, accessRead|accessWrite|accessExecute); err != nil {
				return fmt.Errorf("media directory is not readable and writable: %w", err)
			}
			return nil
		}
		if err := syscall.Access(current, accessRead); err != nil {
			return fmt.Errorf("media file is not readable: %w", err)
		}
		parent := filepath.Dir(current)
		parentInfo, err := os.Stat(parent)
		if err != nil {
			return fmt.Errorf("check media parent %s: %w", parent, err)
		}
		if !parentInfo.IsDir() {
			return fmt.Errorf("media parent is not a directory: %s", parent)
		}
		if err := syscall.Access(parent, accessWrite|accessExecute); err != nil {
			return fmt.Errorf("media directory is not writable: %w", err)
		}
	default:
		return fmt.Errorf("unknown storage capability %q", capability)
	}
	return nil
}

func retrySourceStable(path string, quiet time.Duration, now time.Time) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if quiet < 0 {
		quiet = 0
	}
	return !info.ModTime().After(now) && now.Sub(info.ModTime()) >= quiet
}

func failureCode(finalError string) string {
	code, _, found := strings.Cut(finalError, ":")
	if !found {
		return finalError
	}
	return code
}

func (s *Service) recordFailureIssue(ctx context.Context, result pipeline.AttemptResult, task pipeline.QueueJob, classification failure.Classification, retryScheduled ...bool) {
	if result.FileID < 1 {
		return
	}
	var nextRetryAt *time.Time
	willRetry := classification.AutoRetry && (len(retryScheduled) == 0 || retryScheduled[0])
	if willRetry {
		delay := failure.RetryDelay(s.config().Pipeline.RetryDelay(), task.AttemptNumber)
		next := time.Now().Add(delay)
		nextRetryAt = &next
	}
	policyVersion := result.FinalFile.AudioPolicyVersion
	if policyVersion < 1 {
		policyVersion = s.config().Audio.Version
	}
	if result.JobID != 0 {
		err := s.db.SetJobFailureDetails(ctx, result.JobID, repository.FailureDetails{
			Stage: classification.Stage, Category: string(classification.Category), Code: classification.Code,
			Summary: classification.Summary, Advice: classification.Advice,
			RetryStrategy: string(classification.Strategy), UnlockCondition: classification.UnlockCondition,
		})
		if err != nil {
			s.logf("record job failure details job_id=%d: %v", result.JobID, err)
		}
	}
	maxAttempts := s.config().Pipeline.MaxRetries + 1
	if classification.Category == failure.SourceChanged {
		maxAttempts = 0
	}
	_, err := s.db.RecordFailureIssue(ctx, repository.FailureIssue{
		FileID: result.FileID, OriginJobID: result.JobID, Stage: classification.Stage,
		Category: string(classification.Category), Code: classification.Code,
		Summary: classification.Summary, Advice: classification.Advice,
		RetryStrategy: string(classification.Strategy), UnlockCondition: classification.UnlockCondition,
		NextRetryAt: nextRetryAt, FileSize: result.FinalFile.Size, FileMTimeNS: result.FinalFile.MTimeNS,
		PolicyVersion: policyVersion, LastAttemptSource: string(task.Source), AttemptNumber: task.AttemptNumber,
		MaxAttempts: maxAttempts,
	})
	if err != nil {
		s.logf("record failure governance job_id=%d: %v", result.JobID, err)
	}
}

func (s *Service) completeRetryAudit(ctx context.Context, jobID int64, result, detail string) {
	if err := s.db.CompleteRecoveryAuditForJob(ctx, jobID, result, detail); err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.logf("complete retry audit job_id=%d: %v", jobID, err)
	}
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
	return s.persistRestoredFileWithEvidence(file, backupPath, nil)
}

func (s *Service) persistRestoredFileWithEvidence(file repository.FileFacts, backupPath string, expected *replacement.Evidence) error {
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
	file.CompatibilityAssessment = nil

	if statErr != nil {
		return fmt.Errorf("%s: stat restored file: %w", pipeline.CauseRestoreStatError, statErr)
	}
	if probeErr != nil {
		return fmt.Errorf("%s: probe restored file: %w", pipeline.CauseRestoreProbeError, probeErr)
	}
	file.AudioSignature = probe.AudioSignature()
	file.VideoSignature = probe.VideoSignature()
	if expected != nil {
		observed := replacement.Evidence{
			Size: info.Size(), MTimeNS: info.ModTime().UnixNano(),
			AudioSignature: file.AudioSignature, VideoSignature: file.VideoSignature,
		}
		if !expected.Matches(observed) {
			return errors.New("manual recovery result does not match original evidence")
		}
	}
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
	assessment := compatibility.BuildAssessment(probe, decision, compatibility.Policy{
		Version: analysisConfig.Audio.Version, IncompatibleCodecs: analysisConfig.Audio.IncompatibleCodecs,
	}, time.Now())
	file.CompatibilityAssessment = &assessment
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
	if !pathWithinRoots(path, cfg.Roots) {
		return false
	}
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
	var removedWaiting []pipeline.RemovedWaitingTask
	switch module {
	case settings.Media:
		var err error
		removedWaiting, err = s.applyMediaConfig(after)
		if err != nil {
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
	for _, task := range removedWaiting {
		s.finishRemovedWaitingTask(task)
		s.logf("removed waiting task excluded by media rules path=%s phase=%s source=%s", task.Path, task.Phase, task.Source)
	}
	if s.events != nil {
		data := map[string]any{"module": module, "apply_mode": settings.ApplyModeFor(module)}
		if module == settings.Media {
			data["removed_waiting_tasks"] = len(removedWaiting)
		}
		s.events.Publish("config.applied", data)
	}
	return nil
}

func (s *Service) applyMediaConfig(after config.Config) ([]pipeline.RemovedWaitingTask, error) {
	s.workerIntakeMu.Lock()
	defer s.workerIntakeMu.Unlock()
	if err := s.reloadWatcher(after); err != nil {
		return nil, err
	}
	if s.queue == nil {
		return nil, nil
	}
	return s.queue.RemoveWaitingIf(func(task pipeline.RuntimeTask) bool {
		return !isCandidatePathForMedia(task.Path, after.Media)
	}), nil
}

func (s *Service) finishRemovedWaitingTask(task pipeline.RemovedWaitingTask) {
	if task.JobID == 0 || s.db == nil {
		return
	}
	finalError := task.LastRetryError
	if finalError == "" {
		finalError = "removed from waiting queue because the path is excluded by current media rules"
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := s.db.FailProcessingJob(finishCtx, task.JobID, finalError); err != nil {
		s.logf("finish removed waiting job failed job_id=%d path=%s error=%v", task.JobID, task.Path, err)
		return
	}
	if s.events != nil {
		s.events.Publish("failed", map[string]any{
			"job_id": task.JobID, "path": task.Path, "status": "failed", "error": finalError,
		})
	}
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
	if s.queue != nil && event.Kind == "progress" {
		s.queue.UpdateProgress(event.Path, pipeline.RuntimeProgress{
			PositionSeconds: event.PositionSeconds, Speed: event.Speed, OutputBytes: event.OutputBytes,
			ETASeconds: event.ETASeconds, LastProgressAt: event.LastProgressAt,
		})
	}
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
			"file_id":          event.FileID,
			"path":             event.Path,
			"status":           event.Status,
			"phase":            event.Phase,
			"kind":             event.Kind,
			"code":             event.Code,
			"outcome":          event.Outcome,
			"attempt":          event.Attempt,
			"message":          event.Message,
			"error":            event.Error,
			"position_seconds": event.PositionSeconds,
			"speed":            event.Speed,
			"output_bytes":     event.OutputBytes,
			"eta_seconds":      event.ETASeconds,
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
