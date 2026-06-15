package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"omv-blueprint/compose/audiocleaner/internal/backup"
	"omv-blueprint/compose/audiocleaner/internal/config"
	"omv-blueprint/compose/audiocleaner/internal/media"
	"omv-blueprint/compose/audiocleaner/internal/repository"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args []string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}

func RunFFmpegWithFallback(ctx context.Context, runner CommandRunner, name string, primaryArgs []string, fallbackArgs []string) error {
	_, err := runFFmpegWithFallbackReport(ctx, runner, name, primaryArgs, fallbackArgs)
	return err
}

func runFFmpegWithFallbackReport(ctx context.Context, runner CommandRunner, name string, primaryArgs []string, fallbackArgs []string) (bool, error) {
	if err := runner.Run(ctx, name, primaryArgs); err != nil {
		return true, runner.Run(ctx, name, fallbackArgs)
	}
	return false, nil
}

type WorkerRepository interface {
	FileByPath(ctx context.Context, path string) (repository.FileRecord, error)
	UpsertFile(ctx context.Context, file repository.FileRecord) (repository.FileRecord, error)
	AddJobEvent(ctx context.Context, event repository.JobEvent) error
	AddBackup(ctx context.Context, backup repository.BackupRecord) error
}

type WorkerQueue interface {
	NextJob() (QueueJob, bool)
	Done(path string)
}

type WorkerProber interface {
	Probe(ctx context.Context, path string) (media.ProbeData, error)
}

type WorkerBackupService interface {
	Replace(ctx context.Context, originalPath string, outputPath string, backupRoot string, retention time.Duration) (backup.ReplaceResult, error)
}

type WorkerStatProvider interface {
	Stat(path string) (os.FileInfo, error)
}

type WorkerStableChecker interface {
	Wait(ctx context.Context, path string, quiet time.Duration) (StableStat, error)
}

type WorkerEvent struct {
	File  repository.FileRecord
	Event repository.JobEvent
}

type WorkerEventPublisher interface {
	Publish(ctx context.Context, event WorkerEvent) error
}

type WorkerRetryScheduler interface {
	Schedule(path string, delay time.Duration)
}

type WorkerTempAllocator interface {
	Allocate(path string) (string, error)
}

type WorkerSourceCopier interface {
	Copy(originalPath string, outputPath string) (string, error)
}

type WorkerDeps struct {
	Config     config.Config
	Repository WorkerRepository
	Queue      WorkerQueue
	Runner     CommandRunner
	Prober     WorkerProber
	Events     WorkerEventPublisher
	Backup     WorkerBackupService
	Stat       WorkerStatProvider
	Stable     WorkerStableChecker
	Scheduler  WorkerRetryScheduler
	Temp       WorkerTempAllocator
	Source     WorkerSourceCopier
	BackupRoot string
	WorkRoot   string
	FFmpegName string
}

type Worker struct {
	cfg        config.Config
	repository WorkerRepository
	queue      WorkerQueue
	runner     CommandRunner
	prober     WorkerProber
	events     WorkerEventPublisher
	backup     WorkerBackupService
	stat       WorkerStatProvider
	stable     WorkerStableChecker
	scheduler  WorkerRetryScheduler
	temp       WorkerTempAllocator
	source     WorkerSourceCopier
	backupRoot string
	workRoot   string
	ffmpegName string
}

func NewWorker(deps WorkerDeps) *Worker {
	runner := deps.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	backupService := deps.Backup
	if backupService == nil {
		backupService = defaultBackupService{}
	}
	stat := deps.Stat
	if stat == nil {
		stat = osStatProvider{}
	}
	stable := deps.Stable
	if stable == nil {
		stable = defaultStableChecker{}
	}
	temp := deps.Temp
	if temp == nil {
		temp = defaultTempAllocator{workRoot: deps.WorkRoot}
	}
	source := deps.Source
	if source == nil {
		source = defaultSourceCopier{}
	}
	ffmpegName := deps.FFmpegName
	if ffmpegName == "" {
		ffmpegName = "ffmpeg"
	}

	return &Worker{
		cfg:        deps.Config,
		repository: deps.Repository,
		queue:      deps.Queue,
		runner:     runner,
		prober:     deps.Prober,
		events:     deps.Events,
		backup:     backupService,
		stat:       stat,
		stable:     stable,
		scheduler:  deps.Scheduler,
		temp:       temp,
		source:     source,
		backupRoot: deps.BackupRoot,
		workRoot:   deps.WorkRoot,
		ffmpegName: ffmpegName,
	}
}

func (w *Worker) ProcessNext(ctx context.Context) error {
	if w.queue == nil {
		return errors.New("worker queue is required")
	}
	job, ok := w.queue.NextJob()
	if !ok {
		return nil
	}
	defer w.queue.Done(job.Path)
	return w.ProcessPathWithSource(ctx, job.Path, job.Source)
}

func (w *Worker) ProcessPath(ctx context.Context, path string) error {
	return w.ProcessPathWithSource(ctx, path, JobSourceDefault)
}

func (w *Worker) ProcessPathWithSource(ctx context.Context, path string, source JobSource) error {
	if err := w.validateDeps(); err != nil {
		return err
	}

	file, err := w.repository.FileByPath(ctx, path)
	fileMissing := false
	if errors.Is(err, sql.ErrNoRows) {
		fileMissing = true
		file = repository.FileRecord{Path: path}
	} else if err != nil {
		return err
	}

	if !fileMissing && shouldSkipUnchanged(file) && file.Fingerprint != "" {
		preflightCtx, cancelPreflight := context.WithTimeout(ctx, w.cfg.Pipeline.JobTimeout())
		unchanged, err := w.fileUnchanged(preflightCtx, file)
		cancelPreflight()
		if err == nil && unchanged {
			return nil
		}
	}

	file.DiscoverySource = discoverySourceForJob(source, file.DiscoverySource)
	file, err = w.transitionPhase(ctx, file, repository.PipelinePhaseQueued, "")
	if err != nil {
		return err
	}

	jobCtx, cancel := context.WithTimeout(ctx, w.cfg.Pipeline.JobTimeout())
	defer cancel()

	latest, err := w.processJob(jobCtx, ctx, file, source, fileMissing)
	if err != nil {
		var postReplaceErr postReplacePersistenceError
		if errors.As(err, &postReplaceErr) {
			return err
		}
		return w.recordFailure(ctx, latest, err)
	}
	return nil
}

func shouldSkipUnchanged(file repository.FileRecord) bool {
	if file.Status == repository.StatusCompatible ||
		file.Status == repository.StatusProcessed ||
		file.Status == repository.StatusRestored ||
		file.Status == repository.StatusIgnored {
		return true
	}
	return file.Status == repository.StatusFailed
}

func (w *Worker) fileUnchanged(ctx context.Context, file repository.FileRecord) (bool, error) {
	stat, err := w.stable.Wait(ctx, file.Path, w.cfg.Pipeline.StatQuietDuration())
	if err != nil {
		return false, err
	}
	probe, err := w.prober.Probe(ctx, file.Path)
	if err != nil {
		if canUseStatOnlyFingerprint(file) {
			fingerprint := media.Fingerprint(file.Path, stat.Size, stat.MTimeNS, "", "")
			return fingerprint == file.Fingerprint, nil
		}
		return false, err
	}
	fingerprint := media.Fingerprint(file.Path, stat.Size, stat.MTimeNS, probe.AudioSignature(), probe.VideoSignature())
	return fingerprint == file.Fingerprint, nil
}

func canUseStatOnlyFingerprint(file repository.FileRecord) bool {
	return (file.Status == repository.StatusFailed ||
		file.Status == repository.StatusIgnored ||
		file.Status == repository.StatusRestored) &&
		file.AudioSignature == "" &&
		file.VideoSignature == ""
}

func (w *Worker) processJob(jobCtx context.Context, persistCtx context.Context, file repository.FileRecord, source JobSource, fileMissing bool) (repository.FileRecord, error) {
	var err error
	file, err = w.transitionPhase(persistCtx, file, repository.PipelinePhaseChecking, "")
	if err != nil {
		return file, err
	}

	originalStat, err := w.stable.Wait(jobCtx, file.Path, w.cfg.Pipeline.StatQuietDuration())
	if err != nil {
		return file, failure(err, causeForContextOr(repository.CauseFailed, err))
	}
	file.Size = originalStat.Size
	file.MTimeNS = originalStat.MTimeNS
	file.Fingerprint = media.Fingerprint(file.Path, file.Size, file.MTimeNS, "", "")
	originalProbe, err := w.prober.Probe(jobCtx, file.Path)
	if err != nil {
		return file, failure(err, reasonForProbeError(err))
	}

	decision := media.Decide(file.Path, originalProbe, media.DecisionConfig{
		Extensions:         w.cfg.Media.Extensions,
		IncompatibleCodecs: w.cfg.Audio.IncompatibleCodecs,
	})
	file.AudioSignature = originalProbe.AudioSignature()
	file.VideoSignature = originalProbe.VideoSignature()
	file.Fingerprint = media.Fingerprint(file.Path, file.Size, file.MTimeNS, file.AudioSignature, file.VideoSignature)

	switch decision.Action {
	case media.ActionUnsupported:
		file.Status = repository.StatusFailed
		file.FailureCause = repository.CauseUnsupported
		file.PipelinePhase = repository.PipelinePhasePending
		file.LastError = decision.Reason
		file, err := w.persist(persistCtx, file, "unsupported")
		return file, err
	case media.ActionAlreadyCompatible:
		file.Status = repository.StatusCompatible
		file.FailureCause = ""
		file.PipelinePhase = repository.PipelinePhasePending
		file.LastError = ""
		file, err := w.persist(persistCtx, file, "compatible")
		return file, err
	}

	outputPath, err := w.temp.Allocate(file.Path)
	if err != nil {
		return file, failure(err, repository.CauseFailed)
	}
	sourcePath := ""
	removeOutputOnFailure := true
	defer func() {
		if removeOutputOnFailure {
			_ = os.Remove(outputPath)
		}
		if sourcePath != "" {
			_ = os.Remove(sourcePath)
		}
		cleanupEmptyWorkDir(outputPath, w.workRoot)
	}()
	sourcePath, err = w.source.Copy(file.Path, outputPath)
	if err != nil {
		return file, failure(err, repository.CauseFailed)
	}

	file, err = w.transitionPhase(persistCtx, file, repository.PipelinePhaseTranscoding, "")
	if err != nil {
		return file, err
	}
	primaryArgs := media.BuildFFmpegArgs(sourcePath, outputPath, originalProbe, decision, true)
	fallbackArgs := media.BuildFFmpegArgs(sourcePath, outputPath, originalProbe, decision, false)
	usedFallback, err := runFFmpegWithFallbackReport(jobCtx, w.runner, w.ffmpegName, primaryArgs, fallbackArgs)
	if err != nil {
		return file, failure(err, causeForContextOr(repository.CauseFailed, err))
	}
	if usedFallback {
		_ = w.repository.AddJobEvent(persistCtx, repository.JobEvent{
			FileID:    file.ID,
			EventKind: repository.EventKindDiagnostic,
			EventCode: repository.EventCodeFfmpegDataStreamFallback,
			Phase:     repository.PipelinePhaseTranscoding,
			Status:    file.Status,
			Attempt:   currentAttempt(file),
			Message:   "ffmpeg fallback without data streams succeeded",
		})
	}

	file, err = w.transitionPhase(persistCtx, file, repository.PipelinePhaseVerifying, "")
	if err != nil {
		return file, err
	}
	outputStat, err := w.stat.Stat(outputPath)
	if err != nil {
		return file, failure(err, repository.CauseVerificationFailed)
	}
	outputProbe, err := w.prober.Probe(jobCtx, outputPath)
	if err != nil {
		return file, failure(err, causeForContextOr(repository.CauseFFProbeError, err))
	}
	if err := media.ValidateOutput(originalProbe, outputProbe, decision, media.ValidationRules{
		IncompatibleCodecs:       w.cfg.Audio.IncompatibleCodecs,
		DurationToleranceSeconds: w.cfg.Validation.DurationToleranceSec,
		MaxSizeRatio:             w.cfg.Validation.MaxSizeRatio,
		MaxSizeIncreaseBytes:     w.cfg.Validation.MaxSizeIncreaseMegabyte * 1024 * 1024,
		OriginalSize:             originalStat.Size,
		OutputSize:               outputStat.Size(),
	}); err != nil {
		return file, failure(err, repository.CauseVerificationFailed)
	}

	currentOriginalStat, err := w.stat.Stat(file.Path)
	if err != nil {
		return file, failure(fmt.Errorf("stat original file before replace: %w", err), repository.CauseFailed)
	}
	if currentOriginalStat.Size() != originalStat.Size || currentOriginalStat.ModTime().UnixNano() != originalStat.MTimeNS {
		return file, failure(fmt.Errorf(
			"original file changed before replace: expected size=%d mtime_ns=%d, got size=%d mtime_ns=%d",
			originalStat.Size,
			originalStat.MTimeNS,
			currentOriginalStat.Size(),
			currentOriginalStat.ModTime().UnixNano(),
		), repository.CauseFailed)
	}

	file, err = w.transitionPhase(persistCtx, file, repository.PipelinePhaseBackingUp, "")
	if err != nil {
		return file, err
	}
	file, err = w.transitionPhase(persistCtx, file, repository.PipelinePhaseReplacing, "")
	if err != nil {
		return file, err
	}
	replace, err := w.backup.Replace(jobCtx, file.Path, outputPath, w.backupRoot, retentionDuration(w.cfg.Backup.RetentionDays))
	if err != nil {
		return file, failure(err, causeForContextOr(repository.CauseFailed, err))
	}
	removeOutputOnFailure = false
	if err := w.repository.AddBackup(persistCtx, repository.BackupRecord{
		FileID:          file.ID,
		OriginalPath:    file.Path,
		BackupPath:      replace.BackupPath,
		OriginalSize:    replace.OriginalSize,
		OriginalMTimeNS: replace.OriginalMTimeNS,
		ExpiresAt:       replace.ExpiresAt,
	}); err != nil {
		return file, postReplacePersistenceError{err: err}
	}

	finalStat, err := w.stat.Stat(file.Path)
	if err != nil {
		return file, postReplacePersistenceError{err: fmt.Errorf("stat replaced original file: %w", err)}
	}
	finalProbe, err := w.prober.Probe(jobCtx, file.Path)
	if err != nil {
		return file, postReplacePersistenceError{err: fmt.Errorf("probe replaced original file: %w", err)}
	}
	if err := media.ValidateOutput(originalProbe, finalProbe, decision, media.ValidationRules{
		IncompatibleCodecs:       w.cfg.Audio.IncompatibleCodecs,
		DurationToleranceSeconds: w.cfg.Validation.DurationToleranceSec,
		MaxSizeRatio:             w.cfg.Validation.MaxSizeRatio,
		MaxSizeIncreaseBytes:     w.cfg.Validation.MaxSizeIncreaseMegabyte * 1024 * 1024,
		OriginalSize:             originalStat.Size,
		OutputSize:               finalStat.Size(),
	}); err != nil {
		return file, postReplacePersistenceError{err: fmt.Errorf("validate replaced original file: %w", err)}
	}

	file.Status = repository.StatusProcessed
	file.FailureCause = ""
	file.PipelinePhase = repository.PipelinePhasePending
	file.LastError = ""
	file.Size = finalStat.Size()
	file.MTimeNS = finalStat.ModTime().UnixNano()
	file.AudioSignature = finalProbe.AudioSignature()
	file.VideoSignature = finalProbe.VideoSignature()
	file.Fingerprint = media.Fingerprint(file.Path, file.Size, file.MTimeNS, file.AudioSignature, file.VideoSignature)
	file, err = w.persist(persistCtx, file, "transcoded")
	if err != nil {
		return file, postReplacePersistenceError{err: err}
	}
	return file, nil
}

func (w *Worker) recordFailure(ctx context.Context, file repository.FileRecord, err error) error {
	file.Attempts++
	file.LastError = err.Error()

	cause := causeForFailure(err)
	if file.Attempts <= w.cfg.Pipeline.MaxRetries {
		file.Status = repository.StatusProcessing
		file.PipelinePhase = repository.PipelinePhaseRetryWait
		file.FailureCause = ""
	} else {
		file.Status = repository.StatusFailed
		file.PipelinePhase = repository.PipelinePhasePending
		file.FailureCause = cause
	}

	persisted, persistErr := w.persist(ctx, file, "failed")
	if persistErr != nil {
		return fmt.Errorf("%w; record failure: %v", err, persistErr)
	}
	if persisted.Status == repository.StatusProcessing && persisted.PipelinePhase == repository.PipelinePhaseRetryWait && w.scheduler != nil {
		w.scheduler.Schedule(persisted.Path, w.cfg.Pipeline.RetryDelay())
	}
	return err
}

func (w *Worker) transitionPhase(ctx context.Context, file repository.FileRecord, phase repository.PipelinePhase, message string) (repository.FileRecord, error) {
	file.Status = repository.StatusProcessing
	file.PipelinePhase = phase
	file.FailureCause = ""
	file.LastError = ""
	return w.persist(ctx, file, message)
}

func (w *Worker) persist(ctx context.Context, file repository.FileRecord, message string) (repository.FileRecord, error) {
	persisted, err := w.repository.UpsertFile(ctx, file)
	if err != nil {
		return file, err
	}

	event := eventForPersist(persisted, message)
	if err := w.repository.AddJobEvent(ctx, event); err != nil {
		return persisted, nil
	}
	if w.events != nil {
		if err := w.events.Publish(ctx, WorkerEvent{File: persisted, Event: event}); err != nil {
			return persisted, nil
		}
	}
	return persisted, nil
}

func eventForPersist(file repository.FileRecord, message string) repository.JobEvent {
	event := repository.JobEvent{
		FileID:  file.ID,
		Status:  file.Status,
		Message: message,
		Error:   file.LastError,
	}
	switch {
	case file.Status == repository.StatusCompatible:
		event.EventKind = repository.EventKindStatusChange
		event.EventCode = repository.EventCodeCompatible
	case file.Status == repository.StatusProcessed:
		event.EventKind = repository.EventKindStatusChange
		event.EventCode = repository.EventCodeProcessed
		event.Outcome = repository.OutcomeTranscoded
	case file.Status == repository.StatusRestored:
		event.EventKind = repository.EventKindStatusChange
		event.EventCode = repository.EventCodeRestore
		event.Outcome = repository.OutcomeRestored
	case file.Status == repository.StatusIgnored:
		event.EventKind = repository.EventKindStatusChange
		event.EventCode = repository.EventCodeIgnored
	case file.Status == repository.StatusFailed:
		event.EventKind = repository.EventKindStatusChange
		event.EventCode = repository.EventCodeFailed
		event.Outcome = outcomeForCause(file.FailureCause)
		event.Phase = file.PipelinePhase
	case file.PipelinePhase != repository.PipelinePhasePending:
		event.EventKind = repository.EventKindPhaseTransition
		event.EventCode = eventCodeForPipelinePhase(file.PipelinePhase)
		event.Phase = file.PipelinePhase
	}
	event.Attempt = eventAttempt(file, event)
	return event
}

func eventCodeForPipelinePhase(phase repository.PipelinePhase) repository.EventCode {
	switch phase {
	case repository.PipelinePhaseQueued:
		return repository.EventCodeQueued
	case repository.PipelinePhaseRetryWait:
		return repository.EventCodeRetryWait
	case repository.PipelinePhaseChecking:
		return repository.EventCodeChecking
	case repository.PipelinePhaseTranscoding:
		return repository.EventCodeTranscoding
	case repository.PipelinePhaseVerifying:
		return repository.EventCodeVerifying
	case repository.PipelinePhaseBackingUp:
		return repository.EventCodeBackingUp
	case repository.PipelinePhaseReplacing:
		return repository.EventCodeReplacing
	default:
		return repository.EventCode(phase)
	}
}

func outcomeForCause(cause repository.FailureCause) repository.EventOutcome {
	switch cause {
	case repository.CauseUnsupported:
		return repository.OutcomeUnsupported
	case "":
		return ""
	default:
		return repository.OutcomeFailed
	}
}

func discoverySourceForJob(source JobSource, current repository.DiscoverySource) repository.DiscoverySource {
	switch source {
	case JobSourceScan:
		return repository.DiscoveryScan
	case JobSourceWatchdog:
		return repository.DiscoveryWatchdog
	case JobSourceManual:
		return repository.DiscoveryManual
	case JobSourceDefault:
		if current != "" {
			return current
		}
		return repository.DiscoveryManual
	default:
		if current != "" {
			return current
		}
		return repository.DiscoveryManual
	}
}

func (w *Worker) validateDeps() error {
	if w.repository == nil {
		return errors.New("worker repository is required")
	}
	if w.prober == nil {
		return errors.New("worker prober is required")
	}
	if w.runner == nil {
		return errors.New("worker runner is required")
	}
	if w.backup == nil {
		return errors.New("worker backup service is required")
	}
	if w.stat == nil {
		return errors.New("worker stat provider is required")
	}
	if w.stable == nil {
		return errors.New("worker stable checker is required")
	}
	if w.temp == nil {
		return errors.New("worker temp allocator is required")
	}
	return nil
}

type defaultBackupService struct{}

func (defaultBackupService) Replace(ctx context.Context, originalPath string, outputPath string, backupRoot string, retention time.Duration) (backup.ReplaceResult, error) {
	if err := ctx.Err(); err != nil {
		return backup.ReplaceResult{}, err
	}
	return backup.ReplaceWithBackup(originalPath, outputPath, backupRoot, retention)
}

type osStatProvider struct{}

func (osStatProvider) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

type defaultStableChecker struct{}

func (defaultStableChecker) Wait(ctx context.Context, path string, quiet time.Duration) (StableStat, error) {
	return WaitForStableFile(ctx, path, quiet)
}

type defaultTempAllocator struct {
	workRoot string
}

func (a defaultTempAllocator) Allocate(path string) (string, error) {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	workRoot := a.workRoot
	if workRoot == "" {
		workRoot = os.Getenv("WORK_ROOT")
	}
	if workRoot == "" {
		workRoot = "/app/work"
	}
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return "", err
	}
	jobDir, err := os.MkdirTemp(workRoot, "job-")
	if err != nil {
		return "", err
	}
	return filepath.Join(jobDir, "output"+ext), nil
}

type defaultSourceCopier struct{}

func (defaultSourceCopier) Copy(originalPath string, outputPath string) (string, error) {
	ext := filepath.Ext(originalPath)
	sourcePath := filepath.Join(filepath.Dir(outputPath), "source"+ext)
	if err := copyFile(originalPath, sourcePath); err != nil {
		return "", err
	}
	return sourcePath, nil
}

func copyFile(src string, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	removeDst := true
	defer func() {
		if removeDst {
			_ = os.Remove(dst)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	removeDst = false
	return nil
}

func cleanupEmptyWorkDir(outputPath string, workRoot string) {
	if workRoot == "" {
		workRoot = os.Getenv("WORK_ROOT")
	}
	if workRoot == "" {
		workRoot = "/app/work"
	}
	dir := filepath.Dir(outputPath)
	rel, err := filepath.Rel(workRoot, dir)
	if err != nil || rel == "." || rel == ".." || containsParentTraversal(rel) {
		return
	}
	_ = os.Remove(dir)
}

func containsParentTraversal(rel string) bool {
	return len(rel) > 3 && rel[:3] == "../"
}

type workerFailure struct {
	err   error
	cause repository.FailureCause
}

func failure(err error, cause repository.FailureCause) error {
	return workerFailure{err: err, cause: cause}
}

func (w workerFailure) Error() string {
	return w.err.Error()
}

func (w workerFailure) Unwrap() error {
	return w.err
}

func causeForFailure(err error) repository.FailureCause {
	var workerErr workerFailure
	if errors.As(err, &workerErr) {
		return workerErr.cause
	}
	return causeForContextOr(repository.CauseFailed, err)
}

func reasonForProbeError(err error) repository.FailureCause {
	return causeForContextOr(repository.CauseFFProbeError, err)
}

func causeForContextOr(fallback repository.FailureCause, err error) repository.FailureCause {
	if errors.Is(err, context.DeadlineExceeded) {
		return repository.CauseTimeout
	}
	return fallback
}

type postReplacePersistenceError struct {
	err error
}

func (p postReplacePersistenceError) Error() string {
	return p.err.Error()
}

func (p postReplacePersistenceError) Unwrap() error {
	return p.err
}

func currentAttempt(file repository.FileRecord) int {
	return file.Attempts + 1
}

func eventAttempt(file repository.FileRecord, event repository.JobEvent) int {
	if event.Outcome == repository.OutcomeFailed || event.EventCode == repository.EventCodeRetryWait {
		return file.Attempts
	}
	return currentAttempt(file)
}

func retentionDuration(days int) time.Duration {
	return time.Duration(days) * 24 * time.Hour
}
