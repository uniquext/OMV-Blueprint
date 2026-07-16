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
	FileByPath(ctx context.Context, path string) (repository.FileFacts, error)
	StartProcessJob(ctx context.Context, file repository.FileFacts, source repository.DiscoverySource) (repository.FileFacts, repository.JobRecord, error)
	UpsertFile(ctx context.Context, file repository.FileFacts) (repository.FileFacts, error)
	FinishProcessJob(ctx context.Context, id int64, file repository.FileFacts, result repository.JobResult, finalError string, backup *repository.BackupRecord) (repository.FileFacts, repository.JobRecord, error)
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
	ConfigProvider func() config.Config
	Repository     WorkerRepository
	BindJob        func(path string, jobID int64) bool
	Runner         CommandRunner
	Prober         WorkerProber
	Events         WorkerEventPublisher
	Backup         WorkerBackupService
	Stat           WorkerStatProvider
	Stable         WorkerStableChecker
	Temp           WorkerTempAllocator
	Source         WorkerSourceCopier
	BackupRoot     string
	WorkRoot       string
	FFmpegName     string
}

type Worker struct {
	configProvider func() config.Config
	repository     WorkerRepository
	bindJob        func(path string, jobID int64) bool
	runner         CommandRunner
	prober         WorkerProber
	events         WorkerEventPublisher
	backup         WorkerBackupService
	stat           WorkerStatProvider
	stable         WorkerStableChecker
	temp           WorkerTempAllocator
	source         WorkerSourceCopier
	backupRoot     string
	workRoot       string
	ffmpegName     string
}

type AttemptResult struct {
	FileID         int64
	JobID          int64
	FinalFile      repository.FileFacts
	Backup         *repository.BackupRecord
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
		configProvider: deps.ConfigProvider,
		repository:     deps.Repository,
		bindJob:        deps.BindJob,
		runner:         runner,
		prober:         deps.Prober,
		events:         deps.Events,
		backup:         backupService,
		stat:           stat,
		stable:         stable,
		temp:           temp,
		source:         source,
		backupRoot:     deps.BackupRoot,
		workRoot:       deps.WorkRoot,
		ffmpegName:     ffmpegName,
	}
}

type preflightResult struct {
	stat  StableStat
	probe media.ProbeData
}

func (w *Worker) ProcessAttempt(ctx context.Context, task QueueJob) (AttemptResult, error) {
	if err := w.validateDeps(); err != nil {
		return AttemptResult{}, err
	}
	if task.AttemptNumber < 1 {
		task.AttemptNumber = 1
	}

	file, err := w.repository.FileByPath(ctx, task.Path)
	fileMissing := false
	if errors.Is(err, sql.ErrNoRows) {
		fileMissing = true
		file = repository.FileFacts{Path: task.Path}
	} else if err != nil {
		return AttemptResult{JobID: task.JobID}, err
	}

	var preflight *preflightResult
	if task.JobID == 0 && !fileMissing && file.ComplianceStatus == repository.ComplianceCompliant {
		gateConfig := w.currentConfig()
		if file.AudioPolicyVersion == gateConfig.Audio.Version {
			preflightCtx, cancelPreflight := context.WithTimeout(ctx, gateConfig.Pipeline.JobTimeout())
			stable, stableErr := w.stable.Wait(preflightCtx, file.Path, gateConfig.Pipeline.StatQuietDuration())
			if stableErr == nil {
				probe, probeErr := w.prober.Probe(preflightCtx, file.Path)
				if probeErr == nil {
					if file.AudioSignature == probe.AudioSignature() && file.VideoSignature == probe.VideoSignature() {
						cancelPreflight()
						return AttemptResult{}, nil
					}
					preflight = &preflightResult{stat: stable, probe: probe}
				}
			}
			cancelPreflight()
		}
	}

	jobID := task.JobID
	if jobID == 0 {
		persisted, job, addErr := w.repository.StartProcessJob(ctx, file, discoverySourceForJob(task.Source))
		if addErr != nil {
			return AttemptResult{}, addErr
		}
		file = persisted
		jobID = job.ID
		if w.bindJob != nil && !w.bindJob(file.Path, jobID) {
			bindErr := fmt.Errorf("bind runtime task to job %d", jobID)
			return AttemptResult{
				FileID:         file.ID,
				JobID:          jobID,
				FinalFile:      file,
				LastError:      bindErr.Error(),
				FinalError:     formatFinalError(CauseFailed, bindErr),
				FailureOutcome: "failed",
			}, bindErr
		}
	}
	result := AttemptResult{FileID: file.ID, JobID: jobID, FinalFile: file}

	jobCtx, cancel := context.WithTimeout(ctx, w.currentConfig().Pipeline.JobTimeout())
	defer cancel()
	if err := w.processFile(jobCtx, ctx, &result, task.AttemptNumber, preflight); err != nil {
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

func (w *Worker) processFile(jobCtx context.Context, persistCtx context.Context, result *AttemptResult, attempt int, preflight *preflightResult) error {
	file := result.FinalFile
	jobID := result.JobID
	w.publishPhase(persistCtx, file, RuntimeChecking, attempt)
	var originalStat StableStat
	var originalProbe media.ProbeData
	if preflight != nil {
		originalStat = preflight.stat
		originalProbe = preflight.probe
		file.Size = originalStat.Size
		file.MTimeNS = originalStat.MTimeNS
		file.AudioSignature = originalProbe.AudioSignature()
		file.VideoSignature = originalProbe.VideoSignature()
		file.ComplianceStatus = repository.ComplianceUnknown
		file.AudioPolicyVersion = 0
		persisted, err := w.repository.UpsertFile(persistCtx, file)
		if err != nil {
			return err
		}
		file = persisted
		result.FinalFile = file
	} else {
		clearFileAssessment(&file)
		persisted, err := w.repository.UpsertFile(persistCtx, file)
		if err != nil {
			return err
		}
		file = persisted
		result.FinalFile = file

		analysisConfig := w.currentConfig()
		originalStat, err = w.stable.Wait(jobCtx, file.Path, analysisConfig.Pipeline.StatQuietDuration())
		if err != nil {
			return failure(err, causeForContextOr(CauseFailed, err))
		}
		file.Size = originalStat.Size
		file.MTimeNS = originalStat.MTimeNS
		result.FinalFile = file
		originalProbe, err = w.prober.Probe(jobCtx, file.Path)
		if err != nil {
			return failure(err, causeForContextOr(CauseFFProbeError, err))
		}
	}
	file.AudioSignature = originalProbe.AudioSignature()
	file.VideoSignature = originalProbe.VideoSignature()
	result.FinalFile = file

	decisionConfig := w.currentConfig()
	decision := media.Decide(file.Path, originalProbe, media.DecisionConfig{
		Extensions:         decisionConfig.Media.Extensions,
		IncompatibleCodecs: decisionConfig.Audio.IncompatibleCodecs,
	})
	switch decision.Action {
	case media.ActionUnsupported:
		clearFileAssessment(&file)
		result.FinalFile = file
		finalError := formatFinalError(CauseUnsupported, errors.New(decision.Reason))
		if err := w.completeProcessJob(persistCtx, result, repository.JobResultFailed, finalError); err != nil {
			return err
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
		file.ComplianceStatus = repository.ComplianceCompliant
		file.AudioPolicyVersion = decisionConfig.Audio.Version
		result.FinalFile = file
		if err := w.completeProcessJob(persistCtx, result, repository.JobResultCompatible, ""); err != nil {
			return err
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
	file.ComplianceStatus = repository.ComplianceNoncompliant
	file.AudioPolicyVersion = decisionConfig.Audio.Version
	persisted, err := w.repository.UpsertFile(persistCtx, file)
	if err != nil {
		return err
	}
	file = persisted
	result.FinalFile = file

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
	outputValidationConfig := w.currentConfig()
	if err := media.ValidateOutput(originalProbe, outputProbe, decision, media.ValidationRules{
		IncompatibleCodecs:       outputValidationConfig.Audio.IncompatibleCodecs,
		DurationToleranceSeconds: outputValidationConfig.Validation.DurationToleranceSec,
		MaxSizeRatio:             outputValidationConfig.Validation.MaxSizeRatio,
		MaxSizeIncreaseBytes:     outputValidationConfig.Validation.MaxSizeIncreaseMegabyte * 1024 * 1024,
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
		clearFileAssessment(&file)
		result.FinalFile = file
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
	result.Backup = &repository.BackupRecord{
		CreatedByJobID: jobID,
		BackupPath:     replace.BackupPath,
	}

	finalStat, err := w.stat.Stat(file.Path)
	if err != nil {
		clearFileAssessment(&file)
		result.FinalFile = file
		return postReplacePersistenceError{err: fmt.Errorf("stat replaced original file: %w", err)}
	}
	finalProbe, err := w.prober.Probe(jobCtx, file.Path)
	if err != nil {
		clearFileAssessment(&file)
		result.FinalFile = file
		return postReplacePersistenceError{err: fmt.Errorf("probe replaced original file: %w", err)}
	}
	finalValidationConfig := w.currentConfig()
	if err := media.ValidateOutput(originalProbe, finalProbe, decision, media.ValidationRules{
		IncompatibleCodecs:       finalValidationConfig.Audio.IncompatibleCodecs,
		DurationToleranceSeconds: finalValidationConfig.Validation.DurationToleranceSec,
		MaxSizeRatio:             finalValidationConfig.Validation.MaxSizeRatio,
		MaxSizeIncreaseBytes:     finalValidationConfig.Validation.MaxSizeIncreaseMegabyte * 1024 * 1024,
		OriginalSize:             originalStat.Size,
		OutputSize:               finalStat.Size(),
	}); err != nil {
		clearFileAssessment(&file)
		result.FinalFile = file
		return postReplacePersistenceError{err: fmt.Errorf("validate replaced original file: %w", err)}
	}
	finalDecision := media.Decide(file.Path, finalProbe, media.DecisionConfig{
		Extensions:         finalValidationConfig.Media.Extensions,
		IncompatibleCodecs: finalValidationConfig.Audio.IncompatibleCodecs,
	})
	if finalDecision.Action != media.ActionAlreadyCompatible {
		file.Size = finalStat.Size()
		file.MTimeNS = finalStat.ModTime().UnixNano()
		file.AudioSignature = finalProbe.AudioSignature()
		file.VideoSignature = finalProbe.VideoSignature()
		if finalDecision.Action == media.ActionTranscode {
			file.ComplianceStatus = repository.ComplianceNoncompliant
			file.AudioPolicyVersion = finalValidationConfig.Audio.Version
		} else {
			clearFileAssessment(&file)
		}
		result.FinalFile = file
		return postReplacePersistenceError{err: fmt.Errorf("replaced file is not compliant under audio policy version %d", finalValidationConfig.Audio.Version)}
	}

	file.Size = finalStat.Size()
	file.MTimeNS = finalStat.ModTime().UnixNano()
	file.AudioSignature = finalProbe.AudioSignature()
	file.VideoSignature = finalProbe.VideoSignature()
	file.ComplianceStatus = repository.ComplianceCompliant
	file.AudioPolicyVersion = finalValidationConfig.Audio.Version
	result.FinalFile = file
	if err := w.completeProcessJob(persistCtx, result, repository.JobResultSucceeded, ""); err != nil {
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

func (w *Worker) completeProcessJob(ctx context.Context, result *AttemptResult, jobResult repository.JobResult, finalError string) error {
	file, _, err := w.repository.FinishProcessJob(ctx, result.JobID, result.FinalFile, jobResult, finalError, result.Backup)
	if err != nil {
		return err
	}
	result.FinalFile = file
	return nil
}

func clearFileAssessment(file *repository.FileFacts) {
	file.AudioSignature = ""
	file.VideoSignature = ""
	file.ComplianceStatus = repository.ComplianceUnknown
	file.AudioPolicyVersion = 0
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

func (w *Worker) currentConfig() config.Config {
	return w.configProvider()
}

func (w *Worker) validateDeps() error {
	if w.configProvider == nil {
		return errors.New("worker config provider is required")
	}
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
