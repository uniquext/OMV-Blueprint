package pipeline

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"omv-blueprint/compose/audiocleaner/internal/backup"
	"omv-blueprint/compose/audiocleaner/internal/compatibility"
	"omv-blueprint/compose/audiocleaner/internal/config"
	"omv-blueprint/compose/audiocleaner/internal/media"
	"omv-blueprint/compose/audiocleaner/internal/observability"
	"omv-blueprint/compose/audiocleaner/internal/replacement"
	"omv-blueprint/compose/audiocleaner/internal/repository"
)

var errSourceChangedDuringProbe = errors.New("source file changed during compatibility probe")
var ErrCapacityBlocked = errors.New("capacity gate blocked new transcode")

type FailureCause string

const (
	CauseFailed             FailureCause = "failed"
	CauseUnsupported        FailureCause = "unsupported"
	CauseFFProbeError       FailureCause = "ffprobe_error"
	CauseVerificationFailed FailureCause = "verification_failed"
	CauseTimeout            FailureCause = "timeout"
	CauseRestoreStatError   FailureCause = "restore_stat_error"
	CauseRestoreProbeError  FailureCause = "restore_probe_error"
	CauseSourceChanged      FailureCause = "source_changed"
	CauseManualRecovery     FailureCause = "manual_recovery"
	CauseCapacity           FailureCause = "capacity_exhausted"
)

const defaultCriticalOperationTimeout = 5 * time.Minute
const MaxDiagnosticBytes = 4096

type CommandRunner interface {
	Run(ctx context.Context, name string, args []string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string) error {
	cmd := newProcessGroupCommand(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return waitForCommand(ctx, cmd)
}

type ProgressCommandRunner interface {
	RunWithProgress(ctx context.Context, name string, args []string, accept func(string)) error
}

func (ExecRunner) RunWithProgress(ctx context.Context, name string, args []string, accept func(string)) error {
	cmd := newProcessGroupCommand(name, args...)
	stdout, _ := cmd.StdoutPipe()
	stderr := &boundedWriter{max: MaxDiagnosticBytes}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	scanDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if accept != nil {
				accept(scanner.Text())
			}
		}
		scanDone <- scanner.Err()
	}()
	var scanErr error
	select {
	case scanErr = <-scanDone:
	case <-ctx.Done():
		terminateProcessGroup(cmd.Process.Pid)
		scanErr = <-scanDone
		_ = cmd.Wait()
		return ctx.Err()
	}
	waitErr := cmd.Wait()
	if scanErr != nil {
		return fmt.Errorf("read ffmpeg progress: %w", scanErr)
	}
	if waitErr != nil && stderr.String() != "" {
		return fmt.Errorf("%w: %s", waitErr, stderr.String())
	}
	return waitErr
}

func newProcessGroupCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func waitForCommand(ctx context.Context, cmd *exec.Cmd) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		terminateProcessGroup(cmd.Process.Pid)
		<-done
		return ctx.Err()
	}
}

func terminateProcessGroup(pid int) {
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

type boundedWriter struct {
	data []byte
	max  int
}

func (w *boundedWriter) Write(value []byte) (int, error) {
	original := len(value)
	remaining := w.max - len(w.data)
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		w.data = append(w.data, value...)
	}
	return original, nil
}

func (w *boundedWriter) String() string { return strings.TrimSpace(string(w.data)) }

func withFFmpegProgressArgs(args []string) []string {
	progressArgs := []string{"-progress", "pipe:1", "-nostats"}
	if len(args) == 0 {
		return progressArgs
	}
	result := make([]string, 0, len(args)+len(progressArgs))
	result = append(result, args[:len(args)-1]...)
	result = append(result, progressArgs...)
	result = append(result, args[len(args)-1])
	return result
}

func RunFFmpegWithFallback(ctx context.Context, runner CommandRunner, name string, primaryArgs []string, fallbackArgs []string) error {
	_, err := runFFmpegWithFallbackReport(ctx, runner, name, primaryArgs, fallbackArgs)
	return err
}

func runFFmpegWithFallbackReport(ctx context.Context, runner CommandRunner, name string, primaryArgs []string, fallbackArgs []string) (bool, error) {
	return runFFmpegWithFallbackProgress(ctx, runner, name, primaryArgs, fallbackArgs, nil)
}

func runFFmpegWithFallbackProgress(ctx context.Context, runner CommandRunner, name string, primaryArgs []string, fallbackArgs []string, accept func(string)) (bool, error) {
	run := func(args []string) error {
		if progressRunner, ok := runner.(ProgressCommandRunner); ok {
			return progressRunner.RunWithProgress(ctx, name, withFFmpegProgressArgs(args), accept)
		}
		return runner.Run(ctx, name, args)
	}
	if err := run(primaryArgs); err != nil {
		return true, run(fallbackArgs)
	}
	return false, nil
}

type WorkerRepository interface {
	FileByPath(ctx context.Context, path string) (repository.FileFacts, error)
	StartProcessJob(ctx context.Context, file repository.FileFacts, source repository.DiscoverySource) (repository.FileFacts, repository.JobRecord, error)
	UpsertFile(ctx context.Context, file repository.FileFacts) (repository.FileFacts, error)
	FinishProcessJob(ctx context.Context, id int64, file repository.FileFacts, result repository.JobResult, finalError string) (repository.FileFacts, repository.JobRecord, error)
	AddReplacementJournal(ctx context.Context, journal repository.ReplacementJournal) error
	UpdateReplacementPhase(ctx context.Context, jobID int64, phase repository.ReplacementPhase, lastError string) error
	MarkReplacementBackupReady(ctx context.Context, jobID int64, size int64, mtimeNS int64, audioSignature string, videoSignature string) error
	RecordReplacementQuality(ctx context.Context, jobID int64, output replacement.Evidence, report media.QualityAssessment) error
	MarkReplacementInstalling(ctx context.Context, jobID int64) error
	DeleteReplacementJournal(ctx context.Context, jobID int64) error
}

type WorkerProber interface {
	Probe(ctx context.Context, path string) (media.ProbeData, error)
}

type WorkerBackupService interface {
	TargetPath(req backup.CreateRequest) (string, error)
	Create(ctx context.Context, req backup.CreateRequest) (backup.CreateResult, error)
	Install(ctx context.Context, sourcePath string, destinationPath string) error
	Restore(ctx context.Context, req backup.RestoreRequest) (backup.RestoreResult, error)
	Remove(path string, backupRoot string) error
}

type WorkerStatProvider interface {
	Stat(path string) (os.FileInfo, error)
}

type WorkerStableChecker interface {
	Wait(ctx context.Context, path string, quiet time.Duration) (StableStat, error)
}

type WorkerEvent struct {
	FileID          int64        `json:"file_id"`
	Path            string       `json:"path"`
	Kind            string       `json:"kind"`
	Code            string       `json:"code"`
	Status          string       `json:"status,omitempty"`
	Phase           RuntimePhase `json:"phase,omitempty"`
	Outcome         string       `json:"outcome,omitempty"`
	Attempt         int          `json:"attempt"`
	Message         string       `json:"message,omitempty"`
	Error           string       `json:"error,omitempty"`
	PositionSeconds float64      `json:"position_seconds,omitempty"`
	Speed           float64      `json:"speed,omitempty"`
	OutputBytes     int64        `json:"output_bytes,omitempty"`
	ETASeconds      float64      `json:"eta_seconds,omitempty"`
	LastProgressAt  time.Time    `json:"last_progress_at,omitempty"`
}

type WorkerEventPublisher interface {
	Publish(ctx context.Context, event WorkerEvent) error
}

type WorkerTempAllocator interface {
	Allocate(path string) (string, error)
}

type WorkerDeps struct {
	ConfigProvider  func() config.Config
	Repository      WorkerRepository
	BindJob         func(path string, jobID int64) bool
	Runner          CommandRunner
	Prober          WorkerProber
	Events          WorkerEventPublisher
	Backup          WorkerBackupService
	Stat            WorkerStatProvider
	Stable          WorkerStableChecker
	Temp            WorkerTempAllocator
	BackupRoot      string
	WorkRoot        string
	FFmpegName      string
	CriticalTimeout time.Duration
	Space           observability.SpaceProvider
}

type Worker struct {
	configProvider  func() config.Config
	repository      WorkerRepository
	bindJob         func(path string, jobID int64) bool
	runner          CommandRunner
	prober          WorkerProber
	events          WorkerEventPublisher
	backup          WorkerBackupService
	stat            WorkerStatProvider
	stable          WorkerStableChecker
	temp            WorkerTempAllocator
	backupRoot      string
	workRoot        string
	ffmpegName      string
	criticalTimeout time.Duration
	space           observability.SpaceProvider
}

type AttemptResult struct {
	FileID         int64
	JobID          int64
	FinalFile      repository.FileFacts
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
	ffmpegName := deps.FFmpegName
	if ffmpegName == "" {
		ffmpegName = "ffmpeg"
	}
	criticalTimeout := deps.CriticalTimeout
	if criticalTimeout <= 0 {
		criticalTimeout = defaultCriticalOperationTimeout
	}
	return &Worker{
		configProvider:  deps.ConfigProvider,
		repository:      deps.Repository,
		bindJob:         deps.BindJob,
		runner:          runner,
		prober:          deps.Prober,
		events:          deps.Events,
		backup:          backupService,
		stat:            stat,
		stable:          stable,
		temp:            temp,
		backupRoot:      deps.BackupRoot,
		workRoot:        deps.WorkRoot,
		ffmpegName:      ffmpegName,
		criticalTimeout: criticalTimeout,
		space:           deps.Space,
	}
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
	if file.BackupFile != "" {
		w.publish(ctx, WorkerEvent{
			FileID: file.ID, Path: file.Path, Kind: "diagnostic", Code: "backup_blocked",
			Attempt: task.AttemptNumber, Message: "skipped file with unresolved backup: " + file.BackupFile,
		})
		return AttemptResult{}, nil
	}

	if task.JobID == 0 && !fileMissing && file.ComplianceStatus == repository.ComplianceCompliant {
		cacheResult, reused, cacheErr := w.tryReuseCompatibilityCache(ctx, file, task.AttemptNumber)
		if cacheErr != nil {
			return cacheResult, cacheErr
		}
		if reused {
			return cacheResult, nil
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
	if err := w.processFile(jobCtx, ctx, &result, task.AttemptNumber); err != nil {
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

func (w *Worker) tryReuseCompatibilityCache(ctx context.Context, file repository.FileFacts, attempt int) (AttemptResult, bool, error) {
	currentInfo, err := w.stat.Stat(file.Path)
	if err != nil {
		return AttemptResult{}, false, nil
	}
	gateConfig := w.currentConfig()
	assessment := compatibility.Assessment{}
	if file.CompatibilityAssessment != nil {
		assessment = *file.CompatibilityAssessment
	}
	cache := compatibility.EvaluateCache(compatibility.CacheInput{
		KnownCompatible:  true,
		HasRecoveryIssue: file.BackupFile != "",
		StoredSize:       file.Size, StoredMTimeNS: file.MTimeNS,
		CurrentSize: currentInfo.Size(), CurrentMTimeNS: currentInfo.ModTime().UnixNano(),
		Policy:     compatibility.Policy{Version: gateConfig.Audio.Version, IncompatibleCodecs: gateConfig.Audio.IncompatibleCodecs},
		Assessment: assessment, Now: time.Now(),
	})
	if !cache.Reuse {
		return AttemptResult{}, false, nil
	}
	file.AudioPolicyVersion = gateConfig.Audio.Version
	file.CompatibilityAssessment = &cache.Assessment
	persisted, persistErr := w.repository.UpsertFile(ctx, file)
	if persistErr != nil {
		return AttemptResult{FileID: file.ID, FinalFile: file}, false, persistErr
	}
	w.publish(ctx, WorkerEvent{FileID: persisted.ID, Path: persisted.Path, Kind: "diagnostic", Code: "compatibility_cache_reused", Attempt: attempt, Message: cache.Reason})
	return AttemptResult{FileID: persisted.ID, FinalFile: persisted}, true, nil
}

type stableSourceAssessment struct {
	stat     StableStat
	probe    media.ProbeData
	decision media.Decision
	config   config.Config
}

func (w *Worker) assessStableSource(jobCtx context.Context, result *AttemptResult) (stableSourceAssessment, error) {
	file := result.FinalFile
	analysisConfig := w.currentConfig()
	originalStat, err := w.stable.Wait(jobCtx, file.Path, analysisConfig.Pipeline.StatQuietDuration())
	if err != nil {
		return stableSourceAssessment{}, failure(err, causeForContextOr(CauseFailed, err))
	}
	file.Size = originalStat.Size
	file.MTimeNS = originalStat.MTimeNS
	clearFileAssessment(&file)
	result.FinalFile = file

	originalProbe, err := w.prober.Probe(jobCtx, file.Path)
	if err != nil {
		return stableSourceAssessment{}, failure(fmt.Errorf("probe source: %w", err), causeForContextOr(CauseFFProbeError, err))
	}
	postProbeInfo, err := w.stat.Stat(file.Path)
	if err != nil {
		return stableSourceAssessment{}, failure(fmt.Errorf("stat source after probe: %w", err), CauseFailed)
	}
	if postProbeInfo.Size() != originalStat.Size || postProbeInfo.ModTime().UnixNano() != originalStat.MTimeNS {
		return stableSourceAssessment{}, failure(fmt.Errorf("%w: expected size=%d mtime_ns=%d, got size=%d mtime_ns=%d", errSourceChangedDuringProbe, originalStat.Size, originalStat.MTimeNS, postProbeInfo.Size(), postProbeInfo.ModTime().UnixNano()), CauseSourceChanged)
	}

	decisionConfig := w.currentConfig()
	decision := media.Decide(file.Path, originalProbe, media.DecisionConfig{
		Extensions: decisionConfig.Media.Extensions, IncompatibleCodecs: decisionConfig.Audio.IncompatibleCodecs,
	})
	assessment := compatibility.BuildAssessment(originalProbe, decision, compatibility.Policy{
		Version: decisionConfig.Audio.Version, IncompatibleCodecs: decisionConfig.Audio.IncompatibleCodecs,
	}, time.Now())
	file.AudioSignature = originalProbe.AudioSignature()
	file.VideoSignature = originalProbe.VideoSignature()
	file.CompatibilityAssessment = &assessment
	result.FinalFile = file
	return stableSourceAssessment{stat: originalStat, probe: originalProbe, decision: decision, config: decisionConfig}, nil
}

func (w *Worker) processFile(jobCtx context.Context, persistCtx context.Context, result *AttemptResult, attempt int) error {
	file := result.FinalFile
	jobID := result.JobID
	w.publishPhase(persistCtx, file, RuntimeChecking, attempt)

	source, err := w.assessStableSource(jobCtx, result)
	if err != nil {
		return err
	}
	file = result.FinalFile
	originalStat := source.stat
	originalProbe := source.probe
	decision := source.decision
	decisionConfig := source.config

	switch decision.Action {
	case media.ActionUnsupported:
		file.ComplianceStatus = repository.ComplianceUnknown
		file.AudioPolicyVersion = 0
		result.FinalFile = file
		finalError := formatFinalError(CauseUnsupported, errors.New(decision.Reason))
		if err := w.completeProcessJob(persistCtx, result, repository.JobResultFailed, finalError); err != nil {
			return err
		}
		result.LastError = decision.Reason
		result.FinalError = finalError
		result.FailureOutcome = "unsupported"
		w.publish(persistCtx, WorkerEvent{FileID: file.ID, Path: file.Path, Kind: "status_change", Code: "failed", Status: "failed", Outcome: "unsupported", Attempt: attempt, Error: decision.Reason})
		return nil
	case media.ActionAlreadyCompatible:
		file.ComplianceStatus = repository.ComplianceCompliant
		file.AudioPolicyVersion = decisionConfig.Audio.Version
		result.FinalFile = file
		if err := w.completeProcessJob(persistCtx, result, repository.JobResultCompatible, ""); err != nil {
			return err
		}
		w.publish(persistCtx, WorkerEvent{FileID: file.ID, Path: file.Path, Kind: "status_change", Code: "compatible", Status: "compatible", Attempt: attempt})
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
	if w.space != nil {
		capacity := observability.EvaluateCapacity(w.space, workerCapacityRequest(file.Path, originalStat.Size, decisionConfig, w.backupRoot, w.workRoot))
		if !capacity.Ready {
			return failure(capacityError(capacity), CauseCapacity)
		}
	}

	outputPath, err := w.temp.Allocate(file.Path)
	if err != nil {
		return failure(err, CauseFailed)
	}
	defer cleanupEmptyWorkDir(outputPath, w.workRoot)
	createRequest := backup.CreateRequest{
		OriginalPath: file.Path,
		BackupRoot:   w.backupRoot,
		FileID:       file.ID,
		JobID:        jobID,
	}
	backupPath, err := w.backup.TargetPath(createRequest)
	if err != nil {
		return failure(err, CauseFailed)
	}
	journal := repository.ReplacementJournal{
		JobID: jobID, FileID: file.ID, OriginalPath: file.Path,
		TemporaryBackupPath: backupPath, OutputPath: outputPath,
		Phase: repository.ReplacementPrepared, OriginalSize: originalStat.Size,
		OriginalMTimeNS:        originalStat.MTimeNS,
		OriginalAudioSignature: file.AudioSignature,
		OriginalVideoSignature: file.VideoSignature,
	}
	if err := w.repository.AddReplacementJournal(persistCtx, journal); err != nil {
		return failure(err, CauseFailed)
	}
	preserveBackup := false
	defer func() {
		if preserveBackup {
			return
		}
		if cleanupErr := w.cleanupAttempt(persistCtx, jobID, backupPath, outputPath); cleanupErr != nil {
			w.publish(persistCtx, WorkerEvent{
				FileID: file.ID, Path: file.Path, Kind: "diagnostic", Code: "backup_cleanup_failed",
				Attempt: attempt, Error: cleanupErr.Error(),
			})
		}
	}()

	w.publishPhase(persistCtx, file, RuntimeBackingUp, attempt)
	created, err := w.backup.Create(jobCtx, createRequest)
	if err != nil {
		return failure(err, causeForContextOr(CauseFailed, err))
	}
	if created.BackupPath != backupPath {
		return failure(fmt.Errorf("backup path changed: prepared=%s created=%s", backupPath, created.BackupPath), CauseFailed)
	}
	backupStat, err := w.stat.Stat(backupPath)
	if err != nil {
		return failure(fmt.Errorf("stat backup: %w", err), CauseVerificationFailed)
	}
	if backupStat.Size() != originalStat.Size {
		return failure(fmt.Errorf("backup size mismatch: got %d want %d", backupStat.Size(), originalStat.Size), CauseVerificationFailed)
	}
	backupProbe, err := w.prober.Probe(jobCtx, backupPath)
	if err != nil {
		return failure(fmt.Errorf("probe backup: %w", err), causeForContextOr(CauseFFProbeError, err))
	}
	if backupProbe.AudioSignature() != originalProbe.AudioSignature() || backupProbe.VideoSignature() != originalProbe.VideoSignature() {
		return failure(errors.New("backup signature differs from probed source"), CauseVerificationFailed)
	}
	if err := w.repository.MarkReplacementBackupReady(
		persistCtx, jobID, originalStat.Size, originalStat.MTimeNS,
		originalProbe.AudioSignature(), originalProbe.VideoSignature(),
	); err != nil {
		return err
	}

	w.publishPhase(persistCtx, file, RuntimeTranscoding, attempt)
	primaryArgs := media.BuildFFmpegArgs(backupPath, outputPath, originalProbe, decision, true)
	fallbackArgs := media.BuildFFmpegArgs(backupPath, outputPath, originalProbe, decision, false)
	progressParser := observability.NewProgressParser(originalProbe.DurationSeconds())
	usedFallback, err := runFFmpegWithFallbackProgress(jobCtx, w.runner, w.ffmpegName, primaryArgs, fallbackArgs, func(line string) {
		observedAt := time.Now()
		progressParser.Accept(line, observedAt)
		progress := progressParser.Snapshot()
		w.publish(persistCtx, WorkerEvent{
			FileID: file.ID, Path: file.Path, Kind: "progress", Code: "job_progress", Phase: RuntimeTranscoding, Attempt: attempt,
			PositionSeconds: progress.PositionSeconds, Speed: progress.Speed, OutputBytes: progress.OutputBytes,
			ETASeconds: progress.ETASeconds, LastProgressAt: progress.LastProgressAt,
		})
	})
	if err != nil {
		return failure(err, causeForContextOr(CauseFailed, err))
	}
	if usedFallback {
		w.publish(persistCtx, WorkerEvent{FileID: file.ID, Path: file.Path, Kind: "diagnostic", Code: "ffmpeg_data_stream_fallback", Phase: RuntimeTranscoding, Attempt: attempt, Message: "ffmpeg fallback without data streams succeeded"})
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
	qualityReport := media.EvaluateOutputQuality(originalProbe, outputProbe, decision, validationRules(outputValidationConfig, originalStat.Size, outputStat.Size()))
	outputEvidence := replacement.Evidence{
		Size: outputStat.Size(), MTimeNS: outputStat.ModTime().UnixNano(),
		AudioSignature: outputProbe.AudioSignature(), VideoSignature: outputProbe.VideoSignature(),
	}
	if err := w.repository.RecordReplacementQuality(persistCtx, jobID, outputEvidence, qualityReport); err != nil {
		return failure(err, CauseFailed)
	}
	if !qualityReport.Passed {
		return failure(qualityAssessmentError(qualityReport), CauseVerificationFailed)
	}

	currentOriginalStat, err := w.stat.Stat(file.Path)
	if err != nil {
		return failure(fmt.Errorf("stat original file before replace: %w", err), CauseFailed)
	}
	if currentOriginalStat.Size() != originalStat.Size || currentOriginalStat.ModTime().UnixNano() != originalStat.MTimeNS {
		return failure(fmt.Errorf("original file changed before replace: expected size=%d mtime_ns=%d, got size=%d mtime_ns=%d", originalStat.Size, originalStat.MTimeNS, currentOriginalStat.Size(), currentOriginalStat.ModTime().UnixNano()), CauseSourceChanged)
	}

	w.publishPhase(persistCtx, file, RuntimeReplacing, attempt)
	criticalCtx, cancelCritical := context.WithTimeout(context.Background(), w.criticalTimeout)
	defer cancelCritical()
	if err := w.repository.MarkReplacementInstalling(criticalCtx, jobID); err != nil {
		return failure(err, CauseFailed)
	}
	if err := w.backup.Install(criticalCtx, outputPath, file.Path); err != nil {
		return w.handleInstalledFailure(criticalCtx, result, attempt, originalStat, originalProbe, decisionConfig.Audio.Version, backupPath, err, &preserveBackup)
	}
	if err := w.repository.UpdateReplacementPhase(criticalCtx, jobID, repository.ReplacementOutputInstalled, ""); err != nil {
		return w.handleInstalledFailure(criticalCtx, result, attempt, originalStat, originalProbe, decisionConfig.Audio.Version, backupPath, err, &preserveBackup)
	}

	finalStat, finalProbe, finalErr := w.validateInstalled(criticalCtx, file.Path, originalStat, originalProbe, decision)
	if finalErr != nil {
		return w.handleInstalledFailure(criticalCtx, result, attempt, originalStat, originalProbe, decisionConfig.Audio.Version, backupPath, finalErr, &preserveBackup)
	}
	finalValidationConfig := w.currentConfig()
	finalDecision := media.Decide(file.Path, finalProbe, media.DecisionConfig{
		Extensions: finalValidationConfig.Media.Extensions, IncompatibleCodecs: finalValidationConfig.Audio.IncompatibleCodecs,
	})
	if finalDecision.Action != media.ActionAlreadyCompatible {
		err := fmt.Errorf("replaced file is not compliant under audio policy version %d", finalValidationConfig.Audio.Version)
		return w.handleInstalledFailure(criticalCtx, result, attempt, originalStat, originalProbe, decisionConfig.Audio.Version, backupPath, err, &preserveBackup)
	}

	file.Size = finalStat.Size()
	file.MTimeNS = finalStat.ModTime().UnixNano()
	file.AudioSignature = finalProbe.AudioSignature()
	file.VideoSignature = finalProbe.VideoSignature()
	file.ComplianceStatus = repository.ComplianceCompliant
	file.AudioPolicyVersion = finalValidationConfig.Audio.Version
	finalAssessment := compatibility.BuildAssessment(finalProbe, finalDecision, compatibility.Policy{
		Version: finalValidationConfig.Audio.Version, IncompatibleCodecs: finalValidationConfig.Audio.IncompatibleCodecs,
	}, time.Now())
	if file.CompatibilityAssessment != nil {
		finalAssessment.LastProcessing = compatibility.ProcessingEvidenceFrom(*file.CompatibilityAssessment)
	}
	file.CompatibilityAssessment = &finalAssessment
	result.FinalFile = file
	if err := w.completeProcessJob(criticalCtx, result, repository.JobResultSucceeded, ""); err != nil {
		return w.handleInstalledFailure(criticalCtx, result, attempt, originalStat, originalProbe, decisionConfig.Audio.Version, backupPath, err, &preserveBackup)
	}
	w.publish(persistCtx, WorkerEvent{FileID: file.ID, Path: file.Path, Kind: "status_change", Code: "processed", Status: "processed", Outcome: "transcoded", Attempt: attempt})
	return nil
}

func qualityAssessmentError(report media.QualityAssessment) error {
	for _, check := range report.Checks {
		if check.Status == media.QualityFailed {
			return errors.New(check.Detail)
		}
	}
	return errors.New("output quality assessment did not pass")
}

func (w *Worker) validateInstalled(ctx context.Context, path string, originalStat StableStat, originalProbe media.ProbeData, decision media.Decision) (os.FileInfo, media.ProbeData, error) {
	finalStat, err := w.stat.Stat(path)
	if err != nil {
		return nil, media.ProbeData{}, failure(fmt.Errorf("stat installed file: %w", err), CauseVerificationFailed)
	}
	finalProbe, err := w.prober.Probe(ctx, path)
	if err != nil {
		return nil, media.ProbeData{}, failure(fmt.Errorf("probe installed file: %w", err), causeForContextOr(CauseFFProbeError, err))
	}
	validationConfig := w.currentConfig()
	if err := media.ValidateOutput(originalProbe, finalProbe, decision, validationRules(validationConfig, originalStat.Size, finalStat.Size())); err != nil {
		return nil, media.ProbeData{}, failure(fmt.Errorf("validate installed file: %w", err), CauseVerificationFailed)
	}
	return finalStat, finalProbe, nil
}

func (w *Worker) handleInstalledFailure(
	_ context.Context,
	result *AttemptResult,
	attempt int,
	originalStat StableStat,
	originalProbe media.ProbeData,
	audioPolicyVersion int,
	backupPath string,
	processingErr error,
	preserveBackup *bool,
) error {
	recoveryCtx, cancelRecovery := context.WithTimeout(context.Background(), w.criticalTimeout)
	defer cancelRecovery()
	file := result.FinalFile
	phaseErr := w.repository.UpdateReplacementPhase(recoveryCtx, result.JobID, repository.ReplacementRestoring, processingErr.Error())
	restoreConfig := w.currentConfig()
	_, restoreErr := w.backup.Restore(recoveryCtx, backup.RestoreRequest{
		OriginalPath: file.Path,
		BackupPath:   backupPath,
		BackupRoot:   w.backupRoot,
		MediaRoots:   restoreConfig.Media.Roots,
	})
	var restoredStat os.FileInfo
	var restoredProbe media.ProbeData
	if restoreErr == nil {
		restoredStat, restoreErr = w.stat.Stat(file.Path)
		if restoreErr != nil {
			restoreErr = fmt.Errorf("stat automatically restored file: %w", restoreErr)
		}
	}
	if restoreErr == nil {
		restoredProbe, restoreErr = w.prober.Probe(recoveryCtx, file.Path)
		if restoreErr != nil {
			restoreErr = fmt.Errorf("probe automatically restored file: %w", restoreErr)
		}
	}
	if restoreErr == nil && (restoredStat.Size() != originalStat.Size ||
		restoredProbe.AudioSignature() != originalProbe.AudioSignature() ||
		restoredProbe.VideoSignature() != originalProbe.VideoSignature()) {
		restoreErr = errors.New("automatically restored file does not match the temporary backup")
	}
	if phaseErr != nil {
		if restoreErr == nil {
			restoreErr = fmt.Errorf("record restoring phase: %w", phaseErr)
		} else {
			restoreErr = fmt.Errorf("%v; record restoring phase: %w", restoreErr, phaseErr)
		}
	}
	if restoreErr == nil {
		file.Size = restoredStat.Size()
		file.MTimeNS = restoredStat.ModTime().UnixNano()
		file.AudioSignature = restoredProbe.AudioSignature()
		file.VideoSignature = restoredProbe.VideoSignature()
		file.ComplianceStatus = repository.ComplianceNoncompliant
		file.AudioPolicyVersion = audioPolicyVersion
		file.BackupFile = ""
		persisted, err := w.repository.UpsertFile(recoveryCtx, file)
		if err != nil {
			return err
		}
		result.FinalFile = persisted
		return processingErr
	}

	*preserveBackup = true
	file.BackupFile = backupPath
	result.FinalFile = file
	finalError := formatFinalError(CauseManualRecovery, fmt.Errorf("%v; automatic restore failed: %w", processingErr, restoreErr))
	if err := w.completeProcessJob(recoveryCtx, result, repository.JobResultFailed, finalError); err != nil {
		return postReplacePersistenceError{err: fmt.Errorf("persist unresolved backup %s: %w", backupPath, err)}
	}
	result.Retryable = false
	result.LastError = finalError
	result.FinalError = finalError
	result.FailureOutcome = "restore_failed"
	w.publish(recoveryCtx, WorkerEvent{
		FileID: file.ID, Path: file.Path, Kind: "status_change", Code: "file.backup_created",
		Status: "blocked", Outcome: "restore_failed", Attempt: attempt, Error: finalError,
	})
	w.publish(recoveryCtx, WorkerEvent{
		FileID: file.ID, Path: file.Path, Kind: "status_change", Code: "failed", Status: "failed",
		Outcome: "restore_failed", Attempt: attempt, Error: finalError,
	})
	return nil
}

func validationRules(cfg config.Config, originalSize int64, outputSize int64) media.ValidationRules {
	return media.ValidationRules{
		IncompatibleCodecs:       cfg.Audio.IncompatibleCodecs,
		DurationToleranceSeconds: cfg.Validation.DurationToleranceSec,
		MaxSizeRatio:             cfg.Validation.MaxSizeRatio,
		MaxSizeIncreaseBytes:     cfg.Validation.MaxSizeIncreaseMegabyte * 1024 * 1024,
		OriginalSize:             originalSize,
		OutputSize:               outputSize,
	}
}

func (w *Worker) cleanupAttempt(ctx context.Context, jobID int64, backupPath string, outputPath string) error {
	var cleanupErr error
	if outputPath != "" {
		if err := os.Remove(outputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = fmt.Errorf("remove output: %w", err)
		}
	}
	if err := w.backup.Remove(backupPath, w.backupRoot); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove temporary backup: %w", err))
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	return w.repository.DeleteReplacementJournal(ctx, jobID)
}

func (w *Worker) completeProcessJob(ctx context.Context, result *AttemptResult, jobResult repository.JobResult, finalError string) error {
	file, _, err := w.repository.FinishProcessJob(ctx, result.JobID, result.FinalFile, jobResult, finalError)
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
	file.CompatibilityAssessment = nil
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
	if cause == CauseSourceChanged {
		return "source_changed"
	}
	return "failed"
}

func formatFinalError(cause FailureCause, err error) string {
	if err == nil || err.Error() == "" {
		return string(cause)
	}
	return observability.LimitDiagnostic(string(cause)+": "+err.Error(), MaxDiagnosticBytes)
}

func workerCapacityRequest(mediaPath string, sourceBytes int64, cfg config.Config, backupPath, workPath string) observability.CapacityRequest {
	maxByRatio := float64(sourceBytes) * cfg.Validation.MaxSizeRatio
	maxByIncrease := float64(sourceBytes) + float64(cfg.Validation.MaxSizeIncreaseMegabyte*1024*1024)
	maxOutput := math.Max(maxByRatio, maxByIncrease)
	maxOutputBytes := int64(maxOutput)
	if math.IsNaN(maxOutput) || maxOutput < 0 {
		maxOutputBytes = -1
	} else if maxOutput > math.MaxInt64 {
		maxOutputBytes = math.MaxInt64
	}
	return observability.CapacityRequest{
		SourceBytes: sourceBytes, MaxOutputBytes: maxOutputBytes, ReserveBytes: observability.DefaultReserveBytes,
		BackupPath: backupPath, WorkPath: workPath, MediaPath: mediaPath,
	}
}

func capacityError(result observability.CapacityResult) error {
	if len(result.Blocking) == 0 {
		return ErrCapacityBlocked
	}
	first := result.Blocking[0]
	path := ""
	for _, volume := range result.Volumes {
		if volume.Code == first.Code {
			path = volume.Path
			break
		}
	}
	return fmt.Errorf("%w: %s path=%s: %s", ErrCapacityBlocked, first.Code, path, first.Summary)
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

func (defaultBackupService) TargetPath(req backup.CreateRequest) (string, error) {
	return backup.TargetPath(req)
}

func (defaultBackupService) Create(ctx context.Context, req backup.CreateRequest) (backup.CreateResult, error) {
	if err := ctx.Err(); err != nil {
		return backup.CreateResult{}, err
	}
	return backup.Create(req)
}

func (defaultBackupService) Install(ctx context.Context, sourcePath string, destinationPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return backup.Install(sourcePath, destinationPath)
}

func (defaultBackupService) Restore(ctx context.Context, req backup.RestoreRequest) (backup.RestoreResult, error) {
	if err := ctx.Err(); err != nil {
		return backup.RestoreResult{}, err
	}
	return backup.Restore(req)
}

func (defaultBackupService) Remove(path string, backupRoot string) error {
	return backup.Remove(path, backupRoot)
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
