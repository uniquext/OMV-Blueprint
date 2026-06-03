package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	BackupRoot string
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
	backupRoot string
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
		temp = defaultTempAllocator{}
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
		backupRoot: deps.BackupRoot,
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

	file, err = w.transition(ctx, file, repository.StatusProcessing, repository.PhaseQueued, "")
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
	if file.Status == repository.StatusQualified {
		return true
	}
	return file.Status == repository.StatusUnqualified &&
		(file.UnqualifiedReason == repository.ReasonIgnored || file.UnqualifiedReason == repository.ReasonRestored)
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
	return file.Status == repository.StatusUnqualified &&
		(file.UnqualifiedReason == repository.ReasonIgnored || file.UnqualifiedReason == repository.ReasonRestored) &&
		file.AudioSignature == "" &&
		file.VideoSignature == ""
}

func (w *Worker) processJob(jobCtx context.Context, persistCtx context.Context, file repository.FileRecord, source JobSource, fileMissing bool) (repository.FileRecord, error) {
	var err error
	file, err = w.transition(persistCtx, file, repository.StatusProcessing, repository.PhaseChecking, "")
	if err != nil {
		return file, err
	}

	originalStat, err := w.stable.Wait(jobCtx, file.Path, w.cfg.Pipeline.StatQuietDuration())
	if err != nil {
		return file, failure(err, reasonForContextOr(repository.ReasonFailed, err))
	}
	originalProbe, err := w.prober.Probe(jobCtx, file.Path)
	if err != nil {
		return file, failure(err, reasonForProbeError(err))
	}

	decision := media.Decide(file.Path, originalProbe, media.DecisionConfig{
		Extensions:         w.cfg.Media.Extensions,
		IncompatibleCodecs: w.cfg.Audio.IncompatibleCodecs,
	})
	file.Size = originalStat.Size
	file.MTimeNS = originalStat.MTimeNS
	file.AudioSignature = originalProbe.AudioSignature()
	file.VideoSignature = originalProbe.VideoSignature()
	file.Fingerprint = media.Fingerprint(file.Path, file.Size, file.MTimeNS, file.AudioSignature, file.VideoSignature)

	switch decision.Action {
	case media.ActionUnsupported:
		file.Status = repository.StatusUnqualified
		file.UnqualifiedReason = repository.ReasonUnsupported
		file.LastError = decision.Reason
		file, err := w.persist(persistCtx, file, "unsupported")
		return file, err
	case media.ActionAlreadyCompatible:
		file.Status = repository.StatusQualified
		file.QualificationSource = alreadyCompatibleSource(source, fileMissing)
		file.UnqualifiedReason = ""
		file.LastError = ""
		file, err := w.persist(persistCtx, file, "qualified")
		return file, err
	}

	outputPath, err := w.temp.Allocate(file.Path)
	if err != nil {
		return file, failure(err, repository.ReasonFailed)
	}
	removeOutputOnFailure := true
	defer func() {
		if removeOutputOnFailure {
			_ = os.Remove(outputPath)
		}
	}()

	file, err = w.transition(persistCtx, file, repository.StatusProcessing, repository.PhaseTranscoding, "")
	if err != nil {
		return file, err
	}
	primaryArgs := media.BuildFFmpegArgs(file.Path, outputPath, originalProbe, decision, true)
	fallbackArgs := media.BuildFFmpegArgs(file.Path, outputPath, originalProbe, decision, false)
	usedFallback, err := runFFmpegWithFallbackReport(jobCtx, w.runner, w.ffmpegName, primaryArgs, fallbackArgs)
	if err != nil {
		return file, failure(err, reasonForContextOr(repository.ReasonFailed, err))
	}
	if usedFallback {
		_ = w.repository.AddJobEvent(persistCtx, repository.JobEvent{
			FileID:    file.ID,
			EventType: "job.ffmpeg_data_stream_fallback",
			Phase:     repository.PhaseTranscoding,
			Attempt:   currentAttempt(file),
			Message:   "ffmpeg fallback without data streams succeeded",
		})
	}

	file, err = w.transition(persistCtx, file, repository.StatusProcessing, repository.PhaseVerifying, "")
	if err != nil {
		return file, err
	}
	outputStat, err := w.stat.Stat(outputPath)
	if err != nil {
		return file, failure(err, repository.ReasonVerificationFailed)
	}
	outputProbe, err := w.prober.Probe(jobCtx, outputPath)
	if err != nil {
		return file, failure(err, reasonForContextOr(repository.ReasonFFProbeError, err))
	}
	if err := media.ValidateOutput(originalProbe, outputProbe, decision, media.ValidationRules{
		IncompatibleCodecs:       w.cfg.Audio.IncompatibleCodecs,
		DurationToleranceSeconds: w.cfg.Validation.DurationToleranceSec,
		MaxSizeRatio:             w.cfg.Validation.MaxSizeRatio,
		MaxSizeIncreaseBytes:     w.cfg.Validation.MaxSizeIncreaseMegabyte * 1024 * 1024,
		OriginalSize:             originalStat.Size,
		OutputSize:               outputStat.Size(),
	}); err != nil {
		return file, failure(err, repository.ReasonVerificationFailed)
	}

	currentOriginalStat, err := w.stat.Stat(file.Path)
	if err != nil {
		return file, failure(fmt.Errorf("stat original file before replace: %w", err), repository.ReasonFailed)
	}
	if currentOriginalStat.Size() != originalStat.Size || currentOriginalStat.ModTime().UnixNano() != originalStat.MTimeNS {
		return file, failure(fmt.Errorf(
			"original file changed before replace: expected size=%d mtime_ns=%d, got size=%d mtime_ns=%d",
			originalStat.Size,
			originalStat.MTimeNS,
			currentOriginalStat.Size(),
			currentOriginalStat.ModTime().UnixNano(),
		), repository.ReasonFailed)
	}

	file, err = w.transition(persistCtx, file, repository.StatusProcessing, repository.PhaseBackingUp, "")
	if err != nil {
		return file, err
	}
	file, err = w.transition(persistCtx, file, repository.StatusProcessing, repository.PhaseReplacing, "")
	if err != nil {
		return file, err
	}
	replace, err := w.backup.Replace(jobCtx, file.Path, outputPath, w.backupRoot, retentionDuration(w.cfg.Backup.RetentionDays))
	if err != nil {
		return file, failure(err, reasonForContextOr(repository.ReasonFailed, err))
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

	file.Status = repository.StatusQualified
	file.QualificationSource = repository.SourceTranscoded
	file.UnqualifiedReason = ""
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
	file.QualificationSource = ""

	reason := reasonForFailure(err)
	if file.Attempts <= w.cfg.Pipeline.MaxRetries {
		file.Status = repository.StatusProcessing
		file.Phase = repository.PhaseRetryWait
		file.UnqualifiedReason = ""
	} else {
		file.Status = repository.StatusUnqualified
		file.UnqualifiedReason = reason
	}

	persisted, persistErr := w.persist(ctx, file, "failed")
	if persistErr != nil {
		return fmt.Errorf("%w; record failure: %v", err, persistErr)
	}
	if persisted.Status == repository.StatusProcessing && persisted.Phase == repository.PhaseRetryWait && w.scheduler != nil {
		w.scheduler.Schedule(persisted.Path, w.cfg.Pipeline.RetryDelay())
	}
	return err
}

func (w *Worker) transition(ctx context.Context, file repository.FileRecord, status repository.Status, phase repository.Phase, message string) (repository.FileRecord, error) {
	file.Status = status
	file.Phase = phase
	file.UnqualifiedReason = ""
	file.LastError = ""
	return w.persist(ctx, file, message)
}

func (w *Worker) persist(ctx context.Context, file repository.FileRecord, message string) (repository.FileRecord, error) {
	persisted, err := w.repository.UpsertFile(ctx, file)
	if err != nil {
		return file, err
	}

	eventType := "job." + string(persisted.Phase)
	if persisted.Status == repository.StatusQualified {
		eventType = "job.qualified"
	} else if persisted.Status == repository.StatusUnqualified {
		eventType = "job.unqualified"
	}
	event := repository.JobEvent{
		FileID:    persisted.ID,
		EventType: eventType,
		Phase:     persisted.Phase,
		Attempt:   eventAttempt(persisted, message),
		Message:   message,
		Error:     persisted.LastError,
	}
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

func alreadyCompatibleSource(source JobSource, fileMissing bool) repository.QualificationSource {
	if source == JobSourceScan && fileMissing {
		return repository.SourceObserved
	}
	return repository.SourceAlreadyCompatible
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

type defaultTempAllocator struct{}

func (defaultTempAllocator) Allocate(path string) (string, error) {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]
	pattern := stem + ".audiocleaner-*"
	if ext != "" {
		pattern += ext
	}
	temp, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return "", err
	}
	name := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

type workerFailure struct {
	err    error
	reason repository.UnqualifiedReason
}

func failure(err error, reason repository.UnqualifiedReason) error {
	return workerFailure{err: err, reason: reason}
}

func (w workerFailure) Error() string {
	return w.err.Error()
}

func (w workerFailure) Unwrap() error {
	return w.err
}

func reasonForFailure(err error) repository.UnqualifiedReason {
	var workerErr workerFailure
	if errors.As(err, &workerErr) {
		return workerErr.reason
	}
	return reasonForContextOr(repository.ReasonFailed, err)
}

func reasonForProbeError(err error) repository.UnqualifiedReason {
	return reasonForContextOr(repository.ReasonFFProbeError, err)
}

func reasonForContextOr(fallback repository.UnqualifiedReason, err error) repository.UnqualifiedReason {
	if errors.Is(err, context.DeadlineExceeded) {
		return repository.ReasonTimeout
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

func eventAttempt(file repository.FileRecord, message string) int {
	if message == "failed" {
		return file.Attempts
	}
	return currentAttempt(file)
}

func retentionDuration(days int) time.Duration {
	return time.Duration(days) * 24 * time.Hour
}
