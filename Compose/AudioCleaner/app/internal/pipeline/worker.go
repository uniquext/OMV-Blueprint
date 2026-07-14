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

type FailureCause string

const (
	CauseFailed             FailureCause = "failed"
	CauseUnsupported        FailureCause = "unsupported"
	CauseFFProbeError       FailureCause = "ffprobe_error"
	CauseVerificationFailed FailureCause = "verification_failed"
	CauseTimeout            FailureCause = "timeout"
	CauseRestoreStatError   FailureCause = "restore_stat_error"
	CauseRestoreProbeError  FailureCause = "restore_probe_error"
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
	FileBaselineByPath(ctx context.Context, path string) (repository.FileBaseline, error)
	UpsertFile(ctx context.Context, file repository.FileFacts) (repository.FileFacts, error)
	AddJob(ctx context.Context, job repository.JobRecord) (repository.JobRecord, error)
	FinishJob(ctx context.Context, id int64, result repository.JobResult, finalError string) (repository.JobRecord, error)
	AddBackup(ctx context.Context, backup repository.BackupRecord) error
}

type WorkerProber interface {
	Probe(ctx context.Context, path string) (media.ProbeData, error)
}

type WorkerBackupService interface {
	Replace(ctx context.Context, originalPath string, outputPath string, backupRoot string) (backup.ReplaceResult, error)
}

type WorkerStatProvider interface {
	Stat(path string) (os.FileInfo, error)
}

type WorkerStableChecker interface {
	Wait(ctx context.Context, path string, quiet time.Duration) (StableStat, error)
}

type WorkerEvent struct {
	FileID  int64        `json:"file_id"`
	Path    string       `json:"path"`
	Kind    string       `json:"kind"`
	Code    string       `json:"code"`
	Status  string       `json:"status,omitempty"`
	Phase   RuntimePhase `json:"phase,omitempty"`
	Outcome string       `json:"outcome,omitempty"`
	Attempt int          `json:"attempt"`
	Message string       `json:"message,omitempty"`
	Error   string       `json:"error,omitempty"`
}

type WorkerEventPublisher interface {
	Publish(ctx context.Context, event WorkerEvent) error
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
	BindJob    func(path string, jobID int64) bool
	Runner     CommandRunner
	Prober     WorkerProber
	Events     WorkerEventPublisher
	Backup     WorkerBackupService
	Stat       WorkerStatProvider
	Stable     WorkerStableChecker
	Temp       WorkerTempAllocator
	Source     WorkerSourceCopier
	BackupRoot string
	WorkRoot   string
	FFmpegName string
}

type Worker struct {
	cfg        config.Config
	repository WorkerRepository
	bindJob    func(path string, jobID int64) bool
	runner     CommandRunner
	prober     WorkerProber
	events     WorkerEventPublisher
	backup     WorkerBackupService
	stat       WorkerStatProvider
	stable     WorkerStableChecker
	temp       WorkerTempAllocator
	source     WorkerSourceCopier
	backupRoot string
	workRoot   string
	ffmpegName string
}

type AttemptResult struct {
	FileID         int64
	JobID          int64
	Retryable      bool
	LastError      string
	FinalError     string
	FailureOutcome string
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
		bindJob:    deps.BindJob,
		runner:     runner,
		prober:     deps.Prober,
		events:     deps.Events,
		backup:     backupService,
		stat:       stat,
		stable:     stable,
		temp:       temp,
		source:     source,
		backupRoot: deps.BackupRoot,
		workRoot:   deps.WorkRoot,
		ffmpegName: ffmpegName,
	}
}

func (w *Worker) ProcessAttempt(ctx context.Context, task QueueJob) (AttemptResult, error) {
	if err := w.validateDeps(); err != nil {
		return AttemptResult{}, err
	}
	if task.AttemptNumber < 1 {
		task.AttemptNumber = 1
	}

	baseline, err := w.repository.FileBaselineByPath(ctx, task.Path)
	fileMissing := false
	if errors.Is(err, sql.ErrNoRows) {
		fileMissing = true
		baseline.File = repository.FileFacts{Path: task.Path}
	} else if err != nil {
		return AttemptResult{JobID: task.JobID}, err
	}

	if !fileMissing && shouldSkipUnchanged(baseline.LatestJob) {
		preflightCtx, cancelPreflight := context.WithTimeout(ctx, w.cfg.Pipeline.JobTimeout())
		unchanged, unchangedErr := w.fileUnchanged(preflightCtx, baseline)
		cancelPreflight()
		if unchangedErr == nil && unchanged {
			return AttemptResult{JobID: task.JobID}, nil
		}
	}

	file := baseline.File
	if fileMissing {
		file, err = w.repository.UpsertFile(ctx, file)
		if err != nil {
			return AttemptResult{JobID: task.JobID}, err
		}
	}

	jobID := task.JobID
	if jobID == 0 {
		job, addErr := w.repository.AddJob(ctx, repository.JobRecord{
			FileID:        file.ID,
			Kind:          repository.JobKindProcess,
			TriggerSource: discoverySourceForJob(task.Source),
			Result:        repository.JobResultProcessing,
		})
		if addErr != nil {
			return AttemptResult{}, addErr
		}
		jobID = job.ID
		if w.bindJob != nil && !w.bindJob(file.Path, jobID) {
			bindErr := fmt.Errorf("bind runtime task to job %d", jobID)
			return AttemptResult{
				FileID:         file.ID,
				JobID:          jobID,
				LastError:      bindErr.Error(),
				FinalError:     formatFinalError(CauseFailed, bindErr),
				FailureOutcome: "failed",
			}, bindErr
		}
	}
	result := AttemptResult{FileID: file.ID, JobID: jobID}

	jobCtx, cancel := context.WithTimeout(ctx, w.cfg.Pipeline.JobTimeout())
	defer cancel()
	if err := w.processFile(jobCtx, ctx, file, jobID, task.AttemptNumber); err != nil {
		cause := causeForFailure(err)
		var postReplaceErr postReplacePersistenceError
		result.Retryable = !errors.As(err, &postReplaceErr)
		result.LastError = err.Error()
		result.FinalError = formatFinalError(cause, err)
		result.FailureOutcome = outcomeForCause(cause)
		return result, err
	}
	return result, nil
}

func shouldSkipUnchanged(job *repository.JobRecord) bool {
	if job == nil {
		return false
	}
	switch job.Result {
	case repository.JobResultCompatible, repository.JobResultSucceeded, repository.JobResultFailed:
		return true
	default:
		return false
	}
}

func (w *Worker) fileUnchanged(ctx context.Context, baseline repository.FileBaseline) (bool, error) {
	file := baseline.File
	stat, err := w.stable.Wait(ctx, file.Path, w.cfg.Pipeline.StatQuietDuration())
	if err != nil {
		return false, err
	}
	stored := media.Fingerprint(file.Path, file.Size, file.MTimeNS, file.AudioSignature, file.VideoSignature)
	probe, err := w.prober.Probe(ctx, file.Path)
	if err != nil {
		if canUseStatOnlyFingerprint(baseline) {
			current := media.Fingerprint(file.Path, stat.Size, stat.MTimeNS, "", "")
			return current == stored, nil
		}
		return false, err
	}
	current := media.Fingerprint(file.Path, stat.Size, stat.MTimeNS, probe.AudioSignature(), probe.VideoSignature())
	return current == stored, nil
}

func canUseStatOnlyFingerprint(baseline repository.FileBaseline) bool {
	if baseline.File.AudioSignature != "" || baseline.File.VideoSignature != "" || baseline.LatestJob == nil {
		return false
	}
	return baseline.LatestJob.Result == repository.JobResultFailed || baseline.LatestJob.Kind == repository.JobKindRestore
}

func (w *Worker) processFile(jobCtx context.Context, persistCtx context.Context, file repository.FileFacts, jobID int64, attempt int) error {
	w.publishPhase(persistCtx, file, RuntimeChecking, attempt)
	originalStat, err := w.stable.Wait(jobCtx, file.Path, w.cfg.Pipeline.StatQuietDuration())
	if err != nil {
		return failure(err, causeForContextOr(CauseFailed, err))
	}
	file.Size = originalStat.Size
	file.MTimeNS = originalStat.MTimeNS
	file.AudioSignature = ""
	file.VideoSignature = ""
	file, err = w.repository.UpsertFile(persistCtx, file)
	if err != nil {
		return err
	}

	originalProbe, err := w.prober.Probe(jobCtx, file.Path)
	if err != nil {
		return failure(err, causeForContextOr(CauseFFProbeError, err))
	}
	file.AudioSignature = originalProbe.AudioSignature()
	file.VideoSignature = originalProbe.VideoSignature()
	file, err = w.repository.UpsertFile(persistCtx, file)
	if err != nil {
		return err
	}

	decision := media.Decide(file.Path, originalProbe, media.DecisionConfig{
		Extensions:         w.cfg.Media.Extensions,
		IncompatibleCodecs: w.cfg.Audio.IncompatibleCodecs,
	})
	switch decision.Action {
	case media.ActionUnsupported:
		finalError := formatFinalError(CauseUnsupported, errors.New(decision.Reason))
		if _, err := w.repository.FinishJob(persistCtx, jobID, repository.JobResultFailed, finalError); err != nil {
			return postReplacePersistenceError{err: err}
		}
		w.publish(persistCtx, WorkerEvent{
			FileID:  file.ID,
			Path:    file.Path,
			Kind:    "status_change",
			Code:    "failed",
			Status:  "failed",
			Outcome: "unsupported",
			Attempt: attempt,
			Error:   decision.Reason,
		})
		return nil
	case media.ActionAlreadyCompatible:
		if _, err := w.repository.FinishJob(persistCtx, jobID, repository.JobResultCompatible, ""); err != nil {
			return postReplacePersistenceError{err: err}
		}
		w.publish(persistCtx, WorkerEvent{
			FileID:  file.ID,
			Path:    file.Path,
			Kind:    "status_change",
			Code:    "compatible",
			Status:  "compatible",
			Attempt: attempt,
		})
		return nil
	}

	outputPath, err := w.temp.Allocate(file.Path)
	if err != nil {
		return failure(err, CauseFailed)
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
		return failure(err, CauseFailed)
	}

	w.publishPhase(persistCtx, file, RuntimeTranscoding, attempt)
	primaryArgs := media.BuildFFmpegArgs(sourcePath, outputPath, originalProbe, decision, true)
	fallbackArgs := media.BuildFFmpegArgs(sourcePath, outputPath, originalProbe, decision, false)
	usedFallback, err := runFFmpegWithFallbackReport(jobCtx, w.runner, w.ffmpegName, primaryArgs, fallbackArgs)
	if err != nil {
		return failure(err, causeForContextOr(CauseFailed, err))
	}
	if usedFallback {
		w.publish(persistCtx, WorkerEvent{
			FileID:  file.ID,
			Path:    file.Path,
			Kind:    "diagnostic",
			Code:    "ffmpeg_data_stream_fallback",
			Phase:   RuntimeTranscoding,
			Attempt: attempt,
			Message: "ffmpeg fallback without data streams succeeded",
		})
	}

	w.publishPhase(persistCtx, file, RuntimeVerifying, attempt)
	outputStat, err := w.stat.Stat(outputPath)
	if err != nil {
		return failure(err, CauseVerificationFailed)
	}
	outputProbe, err := w.prober.Probe(jobCtx, outputPath)
	if err != nil {
		return failure(err, causeForContextOr(CauseFFProbeError, err))
	}
	if err := media.ValidateOutput(originalProbe, outputProbe, decision, media.ValidationRules{
		IncompatibleCodecs:       w.cfg.Audio.IncompatibleCodecs,
		DurationToleranceSeconds: w.cfg.Validation.DurationToleranceSec,
		MaxSizeRatio:             w.cfg.Validation.MaxSizeRatio,
		MaxSizeIncreaseBytes:     w.cfg.Validation.MaxSizeIncreaseMegabyte * 1024 * 1024,
		OriginalSize:             originalStat.Size,
		OutputSize:               outputStat.Size(),
	}); err != nil {
		return failure(err, CauseVerificationFailed)
	}

	currentOriginalStat, err := w.stat.Stat(file.Path)
	if err != nil {
		return failure(fmt.Errorf("stat original file before replace: %w", err), CauseFailed)
	}
	if currentOriginalStat.Size() != originalStat.Size || currentOriginalStat.ModTime().UnixNano() != originalStat.MTimeNS {
		return failure(fmt.Errorf(
			"original file changed before replace: expected size=%d mtime_ns=%d, got size=%d mtime_ns=%d",
			originalStat.Size,
			originalStat.MTimeNS,
			currentOriginalStat.Size(),
			currentOriginalStat.ModTime().UnixNano(),
		), CauseFailed)
	}

	w.publishPhase(persistCtx, file, RuntimeBackingUp, attempt)
	w.publishPhase(persistCtx, file, RuntimeReplacing, attempt)
	replace, err := w.backup.Replace(jobCtx, file.Path, outputPath, w.backupRoot)
	if err != nil {
		return failure(err, causeForContextOr(CauseFailed, err))
	}
	removeOutputOnFailure = false
	if err := w.repository.AddBackup(persistCtx, repository.BackupRecord{
		CreatedByJobID: jobID,
		BackupPath:     replace.BackupPath,
	}); err != nil {
		return postReplacePersistenceError{err: err}
	}

	finalStat, err := w.stat.Stat(file.Path)
	if err != nil {
		return postReplacePersistenceError{err: fmt.Errorf("stat replaced original file: %w", err)}
	}
	finalProbe, err := w.prober.Probe(jobCtx, file.Path)
	if err != nil {
		return postReplacePersistenceError{err: fmt.Errorf("probe replaced original file: %w", err)}
	}
	if err := media.ValidateOutput(originalProbe, finalProbe, decision, media.ValidationRules{
		IncompatibleCodecs:       w.cfg.Audio.IncompatibleCodecs,
		DurationToleranceSeconds: w.cfg.Validation.DurationToleranceSec,
		MaxSizeRatio:             w.cfg.Validation.MaxSizeRatio,
		MaxSizeIncreaseBytes:     w.cfg.Validation.MaxSizeIncreaseMegabyte * 1024 * 1024,
		OriginalSize:             originalStat.Size,
		OutputSize:               finalStat.Size(),
	}); err != nil {
		return postReplacePersistenceError{err: fmt.Errorf("validate replaced original file: %w", err)}
	}

	file.Size = finalStat.Size()
	file.MTimeNS = finalStat.ModTime().UnixNano()
	file.AudioSignature = finalProbe.AudioSignature()
	file.VideoSignature = finalProbe.VideoSignature()
	if _, err := w.repository.UpsertFile(persistCtx, file); err != nil {
		return postReplacePersistenceError{err: err}
	}
	if _, err := w.repository.FinishJob(persistCtx, jobID, repository.JobResultSucceeded, ""); err != nil {
		return postReplacePersistenceError{err: err}
	}
	w.publish(persistCtx, WorkerEvent{
		FileID:  file.ID,
		Path:    file.Path,
		Kind:    "status_change",
		Code:    "processed",
		Status:  "processed",
		Outcome: "transcoded",
		Attempt: attempt,
	})
	return nil
}

func (w *Worker) publishPhase(ctx context.Context, file repository.FileFacts, phase RuntimePhase, attempt int) {
	w.publish(ctx, WorkerEvent{
		FileID:  file.ID,
		Path:    file.Path,
		Kind:    "phase_transition",
		Code:    string(phase),
		Phase:   phase,
		Attempt: attempt,
	})
}

func (w *Worker) publish(ctx context.Context, event WorkerEvent) {
	if w.events != nil {
		_ = w.events.Publish(ctx, event)
	}
}

func outcomeForCause(cause FailureCause) string {
	if cause == CauseUnsupported {
		return "unsupported"
	}
	return "failed"
}

func formatFinalError(cause FailureCause, err error) string {
	if err == nil || err.Error() == "" {
		return string(cause)
	}
	return string(cause) + ": " + err.Error()
}

func discoverySourceForJob(source JobSource) repository.DiscoverySource {
	switch source {
	case JobSourceScan:
		return repository.DiscoveryScan
	case JobSourceWatchdog:
		return repository.DiscoveryWatchdog
	default:
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

func (defaultBackupService) Replace(ctx context.Context, originalPath string, outputPath string, backupRoot string) (backup.ReplaceResult, error) {
	if err := ctx.Err(); err != nil {
		return backup.ReplaceResult{}, err
	}
	return backup.ReplaceWithBackup(originalPath, outputPath, backupRoot)
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
	cause FailureCause
}

func failure(err error, cause FailureCause) error {
	return workerFailure{err: err, cause: cause}
}

func (w workerFailure) Error() string {
	return w.err.Error()
}

func (w workerFailure) Unwrap() error {
	return w.err
}

func causeForFailure(err error) FailureCause {
	var workerErr workerFailure
	if errors.As(err, &workerErr) {
		return workerErr.cause
	}
	return causeForContextOr(CauseFailed, err)
}

func causeForContextOr(fallback FailureCause, err error) FailureCause {
	if errors.Is(err, context.DeadlineExceeded) {
		return CauseTimeout
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
