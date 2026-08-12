package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"omv-blueprint/compose/audiocleaner/internal/compatibility"
	"omv-blueprint/compose/audiocleaner/internal/media"
	"omv-blueprint/compose/audiocleaner/internal/observability"
	"omv-blueprint/compose/audiocleaner/internal/replacement"
)

type Repository struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Repository, error) {
	return openRepository(ctx, path, sql.Open)
}

func openRepository(ctx context.Context, path string, opener func(string, string) (*sql.DB, error)) (*Repository, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}

	db, err := opener("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite repository: %w", err)
	}

	repo := &Repository{db: db}
	if err := repo.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return repo, nil
}

func (r *Repository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *Repository) UpsertFile(ctx context.Context, file FileFacts) (FileFacts, error) {
	now := time.Now()
	return upsertFile(ctx, r.db, file, now)
}

func upsertFile(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, file FileFacts, now time.Time) (FileFacts, error) {
	if err := validateFileCompliance(file); err != nil {
		return FileFacts{}, err
	}
	assessmentJSON, err := marshalCompatibilityAssessment(file.CompatibilityAssessment)
	if err != nil {
		return FileFacts{}, err
	}
	createdAt := file.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := file.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	row := queryer.QueryRowContext(ctx, `
INSERT INTO files (
  file_path,
  size,
  mtime_ns,
  audio_signature,
  video_signature,
  compliance_status,
	  audio_policy_version,
	  backup_file,
	  compatibility_assessment,
	  created_at,
	  updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(file_path) DO UPDATE SET
  size = excluded.size,
  mtime_ns = excluded.mtime_ns,
  audio_signature = excluded.audio_signature,
  video_signature = excluded.video_signature,
  compliance_status = excluded.compliance_status,
	  audio_policy_version = excluded.audio_policy_version,
	  backup_file = excluded.backup_file,
	  compatibility_assessment = excluded.compatibility_assessment,
	  missing_at = NULL,
	  updated_at = ?
RETURNING `+fileSelectColumns, file.Path,
		file.Size, file.MTimeNS, nullableString(file.AudioSignature),
		nullableString(file.VideoSignature), nullableString(string(file.ComplianceStatus)),
		nullableInt64(int64(file.AudioPolicyVersion)), nullableString(file.BackupFile),
		assessmentJSON,
		formatTime(createdAt), formatTime(updatedAt),
		formatTime(now))

	persisted, err := scanFileFacts(row)
	if err != nil {
		return FileFacts{}, err
	}
	return persisted, nil
}

func validateFileCompliance(file FileFacts) error {
	switch file.ComplianceStatus {
	case ComplianceUnknown:
		if file.AudioPolicyVersion != 0 {
			return errors.New("unknown compliance status requires an unknown audio policy version")
		}
	case ComplianceCompliant, ComplianceNoncompliant:
		if file.AudioPolicyVersion < 1 {
			return errors.New("known compliance status requires a positive audio policy version")
		}
	default:
		return fmt.Errorf("invalid compliance status %q", file.ComplianceStatus)
	}
	return nil
}

func (r *Repository) FileByPath(ctx context.Context, path string) (FileFacts, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+fileSelectColumns+` FROM files WHERE file_path = ?`, path)
	return scanFileFacts(row)
}

func (r *Repository) FileByID(ctx context.Context, id int64) (FileFacts, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+fileSelectColumns+` FROM files WHERE id = ?`, id)
	return scanFileFacts(row)
}

func (r *Repository) Files(ctx context.Context) ([]FileFacts, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileSelectColumns+` FROM files ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFileFactsRows(rows)
}

func (r *Repository) FilesPage(ctx context.Context, req PageRequest) (PageResult[FileFacts], error) {
	req = req.Normalize()
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&total); err != nil {
		return PageResult[FileFacts]{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileSelectColumns+` FROM files ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`, req.PageSize, req.Offset())
	if err != nil {
		return PageResult[FileFacts]{}, err
	}
	defer rows.Close()
	items, err := scanFileFactsRows(rows)
	if err != nil {
		return PageResult[FileFacts]{}, err
	}
	return PageResult[FileFacts]{Items: items, Page: req.Page, PageSize: req.PageSize, Total: total}, nil
}

func (r *Repository) FilesWithBackup(ctx context.Context) ([]FileFacts, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileSelectColumns+` FROM files WHERE backup_file IS NOT NULL ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFileFactsRows(rows)
}

func (r *Repository) ReconcileFilePresence(ctx context.Context, missingIDs, presentIDs []int64, observedAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin file presence reconciliation: %w", err)
	}
	defer tx.Rollback()

	for _, id := range missingIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE files SET missing_at = ?, updated_at = ? WHERE id = ? AND missing_at IS NULL`,
			formatTime(observedAt), formatTime(observedAt), id); err != nil {
			return fmt.Errorf("mark file %d missing: %w", id, err)
		}
	}
	for _, id := range presentIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE files SET missing_at = NULL, updated_at = ? WHERE id = ? AND missing_at IS NOT NULL`,
			formatTime(observedAt), id); err != nil {
			return fmt.Errorf("mark file %d present: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit file presence reconciliation: %w", err)
	}
	return nil
}

func (r *Repository) SetFileBackup(ctx context.Context, fileID int64, path string) error {
	if path == "" {
		return errors.New("backup file path is required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE files SET backup_file = ?, updated_at = ? WHERE id = ?`, path, formatTime(time.Now()), fileID)
	if err != nil {
		return fmt.Errorf("set file backup: %w", err)
	}
	return requireAffectedRow(result, "set file backup")
}

func (r *Repository) ClearFileBackup(ctx context.Context, fileID int64) error {
	result, err := r.db.ExecContext(ctx, `UPDATE files SET backup_file = NULL, updated_at = ? WHERE id = ?`, formatTime(time.Now()), fileID)
	if err != nil {
		return fmt.Errorf("clear file backup: %w", err)
	}
	return requireAffectedRow(result, "clear file backup")
}

func (r *Repository) History(ctx context.Context) ([]JobHistoryRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+historySelectColumns+`
	FROM jobs
	JOIN files ON files.id = jobs.file_id
	WHERE jobs.ignored = 0
	ORDER BY COALESCE(jobs.finished_at, jobs.started_at) DESC, jobs.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHistoryRecords(rows)
}

func (r *Repository) HistoryPage(ctx context.Context, req PageRequest) (PageResult[JobHistoryRecord], error) {
	req = req.Normalize()
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE ignored = 0`).Scan(&total); err != nil {
		return PageResult[JobHistoryRecord]{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+historySelectColumns+`
	FROM jobs
	JOIN files ON files.id = jobs.file_id
	WHERE jobs.ignored = 0
	ORDER BY COALESCE(jobs.finished_at, jobs.started_at) DESC, jobs.id DESC
	LIMIT ? OFFSET ?`, req.PageSize, req.Offset())
	if err != nil {
		return PageResult[JobHistoryRecord]{}, err
	}
	defer rows.Close()
	items, err := scanHistoryRecords(rows)
	if err != nil {
		return PageResult[JobHistoryRecord]{}, err
	}
	return PageResult[JobHistoryRecord]{Items: items, Page: req.Page, PageSize: req.PageSize, Total: total}, nil
}

func (r *Repository) JobOutcomeCounts(ctx context.Context) (OutcomeCounts, error) {
	var counts OutcomeCounts
	err := r.db.QueryRowContext(ctx, `
	SELECT
	  COALESCE(SUM(CASE WHEN result = 'compatible' THEN 1 ELSE 0 END), 0),
	  COALESCE(SUM(CASE WHEN result = 'succeeded' THEN 1 ELSE 0 END), 0),
	  COALESCE(SUM(CASE WHEN result = 'failed' THEN 1 ELSE 0 END), 0)
	FROM jobs
	WHERE ignored = 0`).Scan(&counts.Compatible, &counts.Processed, &counts.Failed)
	if err != nil {
		return OutcomeCounts{}, fmt.Errorf("query job outcome counts: %w", err)
	}
	return counts, nil
}

func (r *Repository) ProcessJobStats(ctx context.Context) (ProcessJobStats, error) {
	var stats ProcessJobStats
	err := r.db.QueryRowContext(ctx, `
	SELECT
	  COALESCE(SUM(CASE WHEN result = 'succeeded' THEN 1 ELSE 0 END), 0),
	  COALESCE(SUM(CASE WHEN result = 'failed' THEN 1 ELSE 0 END), 0)
	FROM jobs
	WHERE ignored = 0`).Scan(&stats.Succeeded, &stats.Failed)
	if err != nil {
		return ProcessJobStats{}, fmt.Errorf("query process job stats: %w", err)
	}
	return stats, nil
}

func (r *Repository) PruneRetention(ctx context.Context, policy RetentionPolicy, now time.Time) (RetentionResult, error) {
	if policy.HistoryMaxAge <= 0 && policy.HistoryMaxCount <= 0 && policy.MissingFileMaxAge <= 0 && policy.MissingFileMaxCount <= 0 {
		return RetentionResult{}, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return RetentionResult{}, fmt.Errorf("begin retention transaction: %w", err)
	}
	defer tx.Rollback()

	jobItems, err := retentionJobItems(ctx, tx)
	if err != nil {
		return RetentionResult{}, err
	}
	jobCutoff := time.Time{}
	if policy.HistoryMaxAge > 0 {
		jobCutoff = now.Add(-policy.HistoryMaxAge)
	}
	jobIDs := observability.SelectExpired(jobItems, jobCutoff, policy.HistoryMaxCount)
	result := RetentionResult{}
	for _, id := range jobIDs {
		deleted, deleteErr := tx.ExecContext(ctx, `DELETE FROM jobs WHERE id=?`, id)
		if deleteErr != nil {
			return RetentionResult{}, fmt.Errorf("delete expired job %d: %w", id, deleteErr)
		}
		count, countErr := deleted.RowsAffected()
		if countErr != nil {
			return RetentionResult{}, countErr
		}
		result.DeletedJobs += int(count)
	}

	fileItems, err := retentionFileItems(ctx, tx)
	if err != nil {
		return RetentionResult{}, err
	}
	fileCutoff := time.Time{}
	if policy.MissingFileMaxAge > 0 {
		fileCutoff = now.Add(-policy.MissingFileMaxAge)
	}
	fileIDs := observability.SelectExpired(fileItems, fileCutoff, policy.MissingFileMaxCount)
	for _, id := range fileIDs {
		deleted, deleteErr := tx.ExecContext(ctx, `DELETE FROM files WHERE id=?`, id)
		if deleteErr != nil {
			return RetentionResult{}, fmt.Errorf("delete expired missing file %d: %w", id, deleteErr)
		}
		count, countErr := deleted.RowsAffected()
		if countErr != nil {
			return RetentionResult{}, countErr
		}
		result.DeletedFiles += int(count)
	}
	if err := tx.Commit(); err != nil {
		return RetentionResult{}, fmt.Errorf("commit retention transaction: %w", err)
	}
	return result, nil
}

func retentionJobItems(ctx context.Context, tx *sql.Tx) ([]observability.RetentionItem, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT jobs.id,COALESCE(jobs.finished_at,jobs.started_at),
 CASE WHEN jobs.result='processing'
   OR EXISTS(SELECT 1 FROM replacement_journal WHERE replacement_journal.job_id=jobs.id)
   OR EXISTS(SELECT 1 FROM failure_issues WHERE failure_issues.resolved_at IS NULL AND (failure_issues.origin_job_id=jobs.id OR failure_issues.file_id=jobs.file_id))
   OR EXISTS(SELECT 1 FROM recovery_audit WHERE recovery_audit.job_id=jobs.id OR recovery_audit.original_job_id=jobs.id)
   OR EXISTS(SELECT 1 FROM files WHERE files.id=jobs.file_id AND files.backup_file IS NOT NULL)
 THEN 1 ELSE 0 END
FROM jobs`)
	if err != nil {
		return nil, fmt.Errorf("load retention jobs: %w", err)
	}
	defer rows.Close()
	items := make([]observability.RetentionItem, 0)
	for rows.Next() {
		var item observability.RetentionItem
		var raw string
		var protected int
		if err := rows.Scan(&item.ID, &raw, &protected); err != nil {
			return nil, fmt.Errorf("scan retention job: %w", err)
		}
		item.Timestamp, err = parseTime(raw)
		if err != nil {
			return nil, fmt.Errorf("parse retention job time: %w", err)
		}
		item.Protected = protected != 0
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retention jobs: %w", err)
	}
	return items, nil
}

func retentionFileItems(ctx context.Context, tx *sql.Tx) ([]observability.RetentionItem, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT files.id,files.missing_at,
 CASE WHEN files.backup_file IS NOT NULL
   OR EXISTS(SELECT 1 FROM jobs WHERE jobs.file_id=files.id)
   OR EXISTS(SELECT 1 FROM replacement_journal WHERE replacement_journal.file_id=files.id)
   OR EXISTS(SELECT 1 FROM failure_issues WHERE failure_issues.file_id=files.id AND failure_issues.resolved_at IS NULL)
   OR EXISTS(SELECT 1 FROM recovery_audit WHERE recovery_audit.file_id=files.id)
 THEN 1 ELSE 0 END
FROM files WHERE files.missing_at IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("load retention files: %w", err)
	}
	defer rows.Close()
	items := make([]observability.RetentionItem, 0)
	for rows.Next() {
		var item observability.RetentionItem
		var raw string
		var protected int
		if err := rows.Scan(&item.ID, &raw, &protected); err != nil {
			return nil, fmt.Errorf("scan retention file: %w", err)
		}
		item.Timestamp, err = parseTime(raw)
		if err != nil {
			return nil, fmt.Errorf("parse retention file time: %w", err)
		}
		item.Protected = protected != 0
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retention files: %w", err)
	}
	return items, nil
}

func (r *Repository) AddJob(ctx context.Context, job JobRecord) (JobRecord, error) {
	return addJob(ctx, r.db, job, time.Now())
}

func (r *Repository) JobByID(ctx context.Context, id int64) (JobRecord, error) {
	return scanJob(r.db.QueryRowContext(ctx, `SELECT `+jobSelectColumns+` FROM jobs WHERE id = ?`, id))
}

func (r *Repository) RestartJob(ctx context.Context, id int64) (JobRecord, error) {
	now := time.Now()
	return scanJob(r.db.QueryRowContext(ctx, `
UPDATE jobs
SET trigger_source = ?, result = ?, final_error = NULL, failure_code = NULL,
    quality_assessment = NULL, started_at = ?, finished_at = NULL
WHERE id = ? AND ignored = 0 AND result = ?
  AND COALESCE(failure_code, '') <> 'manual_recovery'
RETURNING `+jobSelectColumns,
		string(DiscoveryManual), string(JobResultProcessing), formatTime(now), id, string(JobResultFailed)))
}

func (r *Repository) StartManualRetryJob(ctx context.Context, file FileFacts, parentJobID int64) (FileFacts, JobRecord, error) {
	original, err := r.JobByID(ctx, parentJobID)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
	}
	if original.FileID != file.ID || original.Result != JobResultFailed || original.FailureCode == "manual_recovery" {
		return FileFacts{}, JobRecord{}, sql.ErrNoRows
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("begin manual retry transaction: %w", err)
	}
	defer tx.Rollback()
	now := time.Now()
	persisted, err := upsertFile(ctx, tx, file, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
	}
	retry, err := addJob(ctx, tx, JobRecord{FileID: persisted.ID, ParentJobID: parentJobID, TriggerSource: DiscoveryManual, Result: JobResultProcessing}, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("commit manual retry transaction: %w", err)
	}
	return persisted, retry, nil
}

func (r *Repository) IgnoreJob(ctx context.Context, id int64) (JobRecord, error) {
	return scanJob(r.db.QueryRowContext(ctx, `
UPDATE jobs
SET ignored = 1
WHERE id = ? AND ignored = 0 AND result = ?
RETURNING `+jobSelectColumns, id, string(JobResultFailed)))
}

func (r *Repository) StartProcessJob(ctx context.Context, file FileFacts, source DiscoverySource) (FileFacts, JobRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("begin process job transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	persisted, err := upsertFile(ctx, tx, file, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
	}
	job, err := addJob(ctx, tx, JobRecord{
		FileID:        persisted.ID,
		TriggerSource: source,
		Result:        JobResultProcessing,
	}, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("commit process job transaction: %w", err)
	}
	return persisted, job, nil
}

func addJob(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, job JobRecord, now time.Time) (JobRecord, error) {
	if job.TriggerSource == "" {
		job.TriggerSource = DiscoveryManual
	}
	if job.Result == "" {
		job.Result = JobResultProcessing
	}
	startedAt := job.StartedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	finishedAt := job.FinishedAt
	if job.Result != JobResultProcessing && finishedAt.IsZero() {
		finishedAt = now
	}
	qualityAssessment, err := marshalQualityAssessment(job.QualityAssessment)
	if err != nil {
		return JobRecord{}, err
	}
	row := queryer.QueryRowContext(ctx, `
INSERT INTO jobs (
  file_id,
	parent_job_id,
  trigger_source,
  result,
  final_error,
	  failure_code,
	  quality_assessment, failure_stage, failure_category, failure_summary, failure_advice, retry_strategy, unlock_condition,
  started_at,
  finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING `+jobSelectColumns,
		job.FileID,
		nullableInt64(job.ParentJobID),
		string(job.TriggerSource),
		string(job.Result),
		nullableString(job.FinalError),
		nullableString(job.FailureCode),
		qualityAssessment,
		nullableString(job.FailureStage), nullableString(job.FailureCategory), nullableString(job.FailureSummary),
		nullableString(job.FailureAdvice), nullableString(job.RetryStrategy), nullableString(job.UnlockCondition),
		formatTime(startedAt),
		nullableTime(finishedAt))
	return scanJob(row)
}

func (r *Repository) SetJobFailureDetails(ctx context.Context, jobID int64, details FailureDetails) error {
	result, err := r.db.ExecContext(ctx, `UPDATE jobs SET failure_code=?,failure_stage=?,failure_category=?,failure_summary=?,failure_advice=?,retry_strategy=?,unlock_condition=? WHERE id=?`,
		nullableString(details.Code), nullableString(details.Stage), nullableString(details.Category), nullableString(details.Summary), nullableString(details.Advice), nullableString(details.RetryStrategy), nullableString(details.UnlockCondition), jobID)
	if err != nil {
		return err
	}
	return requireAffectedRow(result, "set job failure details")
}

func (r *Repository) FinishJob(ctx context.Context, id int64, result JobResult, finalError string) (JobRecord, error) {
	return finishJob(ctx, r.db, id, result, finalError, time.Now())
}

func finishJob(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64, result JobResult, finalError string, now time.Time) (JobRecord, error) {
	row := queryer.QueryRowContext(ctx, `
UPDATE jobs
SET result = ?, final_error = ?, failure_code = ?, finished_at = ?
WHERE id = ?
RETURNING `+jobSelectColumns,
		string(result),
		nullableString(finalError),
		nullableStringForResult(result, failureCodeFromFinalError(finalError)),
		formatTime(now),
		id)
	return scanJob(row)
}

func (r *Repository) FinishProcessJob(ctx context.Context, id int64, file FileFacts, result JobResult, finalError string) (FileFacts, JobRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("begin finish process job transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	persisted, err := upsertFile(ctx, tx, file, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
	}
	var jobFileID int64
	if err := tx.QueryRowContext(ctx, `SELECT file_id FROM jobs WHERE id = ?`, id).Scan(&jobFileID); err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("load process job file: %w", err)
	}
	if jobFileID != persisted.ID {
		return FileFacts{}, JobRecord{}, fmt.Errorf("process job %d belongs to file %d, not %d", id, jobFileID, persisted.ID)
	}
	job, err := finishJob(ctx, tx, id, result, finalError, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
	}
	journalPhase := ReplacementCommitted
	if result == JobResultFailed && file.BackupFile != "" {
		journalPhase = ReplacementManualRecovery
	}
	if _, err := tx.ExecContext(ctx, `UPDATE replacement_journal SET phase = ?, updated_at = ? WHERE job_id = ?`,
		string(journalPhase), formatTime(now), id); err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("commit replacement journal: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("commit finish process job transaction: %w", err)
	}
	return persisted, job, nil
}

func (r *Repository) FailProcessingJob(ctx context.Context, id int64, finalError string) (FileFacts, JobRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("begin fail processing job transaction: %w", err)
	}
	defer tx.Rollback()

	file, err := scanFileFacts(tx.QueryRowContext(ctx, `SELECT `+qualifiedFileSelectColumns+`
FROM files
JOIN jobs ON jobs.file_id = files.id
WHERE jobs.id = ? AND jobs.result = ?`, id, string(JobResultProcessing)))
	if err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("load processing job file: %w", err)
	}
	file.AudioSignature = ""
	file.VideoSignature = ""
	file.ComplianceStatus = ComplianceUnknown
	file.AudioPolicyVersion = 0
	file.CompatibilityAssessment = nil
	now := time.Now()
	persisted, err := upsertFile(ctx, tx, file, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
	}
	job, err := finishJob(ctx, tx, id, JobResultFailed, finalError, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("commit fail processing job transaction: %w", err)
	}
	return persisted, job, nil
}

func (r *Repository) ProcessingJobs(ctx context.Context) ([]JobRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+jobSelectColumns+` FROM jobs WHERE result = ? AND ignored = 0 ORDER BY started_at ASC, id ASC`, string(JobResultProcessing))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (r *Repository) AddReplacementJournal(ctx context.Context, journal ReplacementJournal) error {
	if journal.JobID < 1 || journal.FileID < 1 {
		return errors.New("replacement journal requires job_id and file_id")
	}
	if journal.OriginalPath == "" || journal.TemporaryBackupPath == "" {
		return errors.New("replacement journal requires original and backup paths")
	}
	if journal.Phase == "" {
		journal.Phase = ReplacementPrepared
	}
	now := time.Now()
	_, err := r.db.ExecContext(ctx, `
INSERT INTO replacement_journal (
  job_id, file_id, original_path, temporary_backup_path, output_path, phase,
  original_size, original_mtime_ns, original_audio_signature, original_video_signature,
  last_error, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		journal.JobID, journal.FileID, journal.OriginalPath, journal.TemporaryBackupPath,
		nullableString(journal.OutputPath), string(journal.Phase), journal.OriginalSize,
		journal.OriginalMTimeNS, nullableString(journal.OriginalAudioSignature),
		nullableString(journal.OriginalVideoSignature), nullableString(journal.LastError),
		formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("add replacement journal: %w", err)
	}
	return nil
}

func (r *Repository) UpdateReplacementPhase(ctx context.Context, jobID int64, phase ReplacementPhase, lastError string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE replacement_journal SET phase = ?, last_error = ?, updated_at = ? WHERE job_id = ?`,
		string(phase), nullableString(lastError), formatTime(time.Now()), jobID)
	if err != nil {
		return fmt.Errorf("update replacement journal: %w", err)
	}
	return requireAffectedRow(result, "update replacement journal")
}

func (r *Repository) MarkReplacementBackupReady(ctx context.Context, jobID int64, size int64, mtimeNS int64, audioSignature string, videoSignature string) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE replacement_journal
SET phase = ?, original_size = ?, original_mtime_ns = ?,
    original_audio_signature = ?, original_video_signature = ?, updated_at = ?
WHERE job_id = ?`, string(ReplacementBackupReady), size, mtimeNS,
		nullableString(audioSignature), nullableString(videoSignature), formatTime(time.Now()), jobID)
	if err != nil {
		return fmt.Errorf("mark replacement backup ready: %w", err)
	}
	return requireAffectedRow(result, "mark replacement backup ready")
}

func (r *Repository) RecordReplacementQuality(ctx context.Context, jobID int64, output replacement.Evidence, report media.QualityAssessment) error {
	if jobID < 1 {
		return errors.New("replacement quality requires job_id")
	}
	if report.SchemaVersion != media.QualitySchemaVersion || len(report.Checks) == 0 {
		return errors.New("replacement quality requires a current-schema assessment with checks")
	}
	if report.Passed && !output.Matches(output) {
		return errors.New("passed replacement quality requires complete output evidence")
	}
	encoded, err := marshalQualityAssessment(&report)
	if err != nil {
		return err
	}
	return recordReplacementQualityTransaction(ctx, sqlMigrationBeginner{db: r.db}, jobID, output, encoded, time.Now())
}

func recordReplacementQualityTransaction(ctx context.Context, beginner sqliteMigrationBeginner, jobID int64, output replacement.Evidence, encoded any, now time.Time) error {
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replacement quality transaction: %w", err)
	}
	defer tx.Rollback()
	journalResult, err := tx.ExecContext(ctx, `
UPDATE replacement_journal
SET output_size = ?, output_mtime_ns = ?, output_audio_signature = ?,
    output_video_signature = ?, quality_assessment = ?, updated_at = ?
WHERE job_id = ? AND phase = ?`,
		output.Size, output.MTimeNS, output.AudioSignature, output.VideoSignature,
		encoded, formatTime(now), jobID, string(ReplacementBackupReady))
	if err != nil {
		return fmt.Errorf("record replacement journal quality: %w", err)
	}
	if err := requireAffectedRow(journalResult, "record replacement journal quality"); err != nil {
		return err
	}
	jobResult, err := tx.ExecContext(ctx, `UPDATE jobs SET quality_assessment = ? WHERE id = ? AND result = ?`,
		encoded, jobID, string(JobResultProcessing))
	if err != nil {
		return fmt.Errorf("record process job quality: %w", err)
	}
	if err := requireAffectedRow(jobResult, "record process job quality"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replacement quality: %w", err)
	}
	return nil
}

func (r *Repository) MarkReplacementInstalling(ctx context.Context, jobID int64) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE replacement_journal
SET phase = ?, updated_at = ?
WHERE job_id = ? AND phase = ? AND quality_assessment IS NOT NULL
  AND output_size > 0 AND output_mtime_ns > 0
  AND output_audio_signature IS NOT NULL AND output_video_signature IS NOT NULL`,
		string(ReplacementInstalling), formatTime(time.Now()), jobID, string(ReplacementBackupReady))
	if err != nil {
		return fmt.Errorf("mark replacement installing: %w", err)
	}
	return requireAffectedRow(result, "mark replacement installing")
}

func (r *Repository) ReplacementJournalByJob(ctx context.Context, jobID int64) (ReplacementJournal, error) {
	return scanReplacementJournal(r.db.QueryRowContext(ctx, `SELECT `+replacementJournalSelectColumns+` FROM replacement_journal WHERE job_id = ?`, jobID))
}

func (r *Repository) ReplacementJournalByFile(ctx context.Context, fileID int64) (ReplacementJournal, error) {
	return scanReplacementJournal(r.db.QueryRowContext(ctx, `SELECT `+replacementJournalSelectColumns+` FROM replacement_journal WHERE file_id = ?`, fileID))
}

func (r *Repository) ReplacementJournals(ctx context.Context) ([]ReplacementJournal, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+replacementJournalSelectColumns+` FROM replacement_journal ORDER BY created_at, job_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]ReplacementJournal, 0)
	for rows.Next() {
		record, err := scanReplacementJournal(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r *Repository) RecordFailureIssue(ctx context.Context, issue FailureIssue) (FailureIssue, error) {
	now := time.Now()
	if issue.OccurrenceCount < 1 {
		issue.OccurrenceCount = 1
	}
	return scanFailureIssue(r.db.QueryRowContext(ctx, `
INSERT INTO failure_issues (file_id, origin_job_id, stage, category, code, summary, advice,
 retry_strategy, unlock_condition, next_retry_at, file_size, file_mtime_ns, policy_version,
	 occurrence_count, last_attempt_source, confirmed_at, created_at, last_seen_at, resolved_at, attempt_number, max_attempts)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)
ON CONFLICT(file_id) WHERE resolved_at IS NULL DO UPDATE SET
 origin_job_id=excluded.origin_job_id, stage=excluded.stage, category=excluded.category,
 code=excluded.code, summary=excluded.summary, advice=excluded.advice,
 retry_strategy=excluded.retry_strategy, unlock_condition=excluded.unlock_condition,
 next_retry_at=excluded.next_retry_at, file_size=excluded.file_size,
 file_mtime_ns=excluded.file_mtime_ns, policy_version=excluded.policy_version,
 occurrence_count=failure_issues.occurrence_count+1,
 last_attempt_source=excluded.last_attempt_source, confirmed_at=excluded.confirmed_at,
	 attempt_number=excluded.attempt_number, max_attempts=excluded.max_attempts,
 last_seen_at=excluded.last_seen_at
RETURNING `+failureIssueReturningColumns,
		issue.FileID, nullableInt64(issue.OriginJobID), issue.Stage, issue.Category, issue.Code,
		issue.Summary, issue.Advice, issue.RetryStrategy, issue.UnlockCondition,
		nullableTimePtr(issue.NextRetryAt), issue.FileSize, issue.FileMTimeNS, issue.PolicyVersion,
		issue.OccurrenceCount, nullableString(issue.LastAttemptSource), nullableTimePtr(issue.ConfirmedAt),
		formatTime(now), formatTime(now), issue.AttemptNumber, issue.MaxAttempts))
}

func (r *Repository) ActiveFailureIssueByFile(ctx context.Context, fileID int64) (FailureIssue, error) {
	return scanFailureIssue(r.db.QueryRowContext(ctx, `SELECT `+failureIssueSelectColumns+` FROM failure_issues JOIN files ON files.id=failure_issues.file_id WHERE failure_issues.file_id=? AND resolved_at IS NULL`, fileID))
}

func (r *Repository) ActiveFailureIssues(ctx context.Context) ([]FailureIssue, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+failureIssueSelectColumns+` FROM failure_issues JOIN files ON files.id=failure_issues.file_id WHERE resolved_at IS NULL ORDER BY last_seen_at DESC, failure_issues.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	issues := make([]FailureIssue, 0)
	for rows.Next() {
		issue, scanErr := scanFailureIssue(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

// EvaluateFailureSuppression atomically confirms an unchanged suppressed issue or resolves it after change.
func (r *Repository) EvaluateFailureSuppression(ctx context.Context, fileID, size, mtimeNS int64, policyVersion int) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var issueID, storedSize, storedMTime int64
	var storedPolicy int
	var strategy string
	err = tx.QueryRowContext(ctx, `SELECT id,file_size,file_mtime_ns,policy_version,retry_strategy FROM failure_issues WHERE file_id=? AND resolved_at IS NULL`, fileID).Scan(&issueID, &storedSize, &storedMTime, &storedPolicy, &strategy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := formatTime(time.Now())
	unchanged := storedSize == size && storedMTime == mtimeNS && storedPolicy == policyVersion
	if unchanged && strategy == "suppress" {
		_, err = tx.ExecContext(ctx, `UPDATE failure_issues SET occurrence_count=occurrence_count+1,last_seen_at=? WHERE id=?`, now, issueID)
		if err != nil {
			return false, err
		}
		if err = tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	if !unchanged {
		_, err = tx.ExecContext(ctx, `UPDATE failure_issues SET resolved_at=?,last_seen_at=? WHERE id=?`, now, now, issueID)
		if err != nil {
			return false, err
		}
		if err = tx.Commit(); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (r *Repository) ResolveFailureIssue(ctx context.Context, fileID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE failure_issues SET resolved_at=?,last_seen_at=? WHERE file_id=? AND resolved_at IS NULL`, formatTime(time.Now()), formatTime(time.Now()), fileID)
	return err
}

func (r *Repository) ConfirmFailureAttempt(ctx context.Context, fileID int64, source string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE failure_issues SET last_attempt_source=?,confirmed_at=?,last_seen_at=? WHERE file_id=? AND resolved_at IS NULL`, source, formatTime(time.Now()), formatTime(time.Now()), fileID)
	if err != nil {
		return err
	}
	return requireAffectedRow(result, "confirm failure attempt")
}

func (r *Repository) AddRecoveryAudit(ctx context.Context, audit RecoveryAudit) error {
	_, err := r.CreateRecoveryAudit(ctx, audit)
	return err
}

func (r *Repository) CreateRecoveryAudit(ctx context.Context, audit RecoveryAudit) (RecoveryAudit, error) {
	if audit.Actor == "" {
		audit.Actor = "system"
	}
	var raw string
	err := r.db.QueryRowContext(ctx, `INSERT INTO recovery_audit(file_id,job_id,original_job_id,original_failure_code,original_failure,actor,action,source,result,detail,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING id,created_at`,
		audit.FileID, nullableInt64(audit.JobID), nullableInt64(audit.OriginalJobID), nullableString(audit.OriginalFailureCode), nullableString(audit.OriginalFailure), audit.Actor, audit.Action, audit.Source, audit.Result, nullableString(audit.Detail), formatTime(time.Now())).Scan(&audit.ID, &raw)
	if err != nil {
		return RecoveryAudit{}, err
	}
	audit.CreatedAt, err = parseTime(raw)
	return audit, err
}

func (r *Repository) CompleteRecoveryAuditForJob(ctx context.Context, jobID int64, result, detail string) error {
	updated, err := r.db.ExecContext(ctx, `UPDATE recovery_audit SET result=?,detail=? WHERE job_id=? AND action='retry' AND result='queued'`, result, nullableString(detail), jobID)
	if err != nil {
		return err
	}
	return requireAffectedRow(updated, "complete recovery audit")
}

func (r *Repository) RecoveryAudits(ctx context.Context, fileID int64) ([]RecoveryAudit, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,file_id,COALESCE(job_id,0),COALESCE(original_job_id,0),COALESCE(original_failure_code,''),COALESCE(original_failure,''),actor,action,source,result,COALESCE(detail,''),created_at FROM recovery_audit WHERE file_id=? ORDER BY created_at,id`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]RecoveryAudit, 0)
	for rows.Next() {
		var item RecoveryAudit
		var raw string
		if err := rows.Scan(&item.ID, &item.FileID, &item.JobID, &item.OriginalJobID, &item.OriginalFailureCode, &item.OriginalFailure, &item.Actor, &item.Action, &item.Source, &item.Result, &item.Detail, &raw); err != nil {
			return nil, err
		}
		item.CreatedAt, err = parseTime(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) DeleteReplacementJournal(ctx context.Context, jobID int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM replacement_journal WHERE job_id = ?`, jobID)
	if err != nil {
		return fmt.Errorf("delete replacement journal: %w", err)
	}
	return nil
}

func requireAffectedRow(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if affected == 0 {
		return fmt.Errorf("%s: %w", operation, sql.ErrNoRows)
	}
	return nil
}

func (r *Repository) PersistRestoredFile(ctx context.Context, file FileFacts) (FileFacts, error) {
	file.BackupFile = ""
	return r.UpsertFile(ctx, file)
}

func (r *Repository) init(ctx context.Context) error {
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA busy_timeout=5000;`,
	} {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("run sqlite pragma: %w", err)
		}
	}

	var version int
	if err := r.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	hasFiles, err := tableExists(ctx, r.db, "files")
	if err != nil {
		return err
	}
	if !hasFiles {
		if version != 0 {
			return fmt.Errorf("unsupported sqlite schema version %d without files table", version)
		}
		for _, stmt := range schemaStatements {
			if _, err := r.db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("initialize sqlite schema: %w", err)
			}
		}
		if err := r.validateSchema(ctx); err != nil {
			return err
		}
		if _, err := r.db.ExecContext(ctx, `PRAGMA user_version = 10`); err != nil {
			return fmt.Errorf("set sqlite schema version: %w", err)
		}
		return r.normalizeStoredTimes(ctx)
	}

	versionSchemas := map[int]map[string][]string{
		1: schemaV1Columns, 2: schemaV2Columns, 3: schemaV3Columns, 4: schemaV4Columns,
		5: schemaV5Columns, 6: schemaV6Columns, 7: schemaV7Columns, 8: schemaV8Columns,
		9: schemaV9Columns, 10: schemaV10Columns,
	}
	expected, supported := versionSchemas[version]
	if !supported {
		if err := r.validateSchema(ctx); err != nil {
			return err
		}
		return fmt.Errorf("unsupported sqlite schema version %d", version)
	}
	if err := r.validateSchemaColumns(ctx, expected); err != nil {
		return err
	}
	migrations := map[int]func(context.Context) error{
		1: r.migrateV1ToV2, 2: r.migrateV2ToV3, 3: r.migrateV3ToV4,
		4: r.migrateV4ToV5, 5: r.migrateV5ToV6, 6: r.migrateV6ToV7,
		7: r.migrateV7ToV8, 8: r.migrateV8ToV9, 9: r.migrateV9ToV10,
	}
	for current := version; current < 10; current++ {
		if err := migrations[current](ctx); err != nil {
			return err
		}
	}
	if err := r.validateSchema(ctx); err != nil {
		return err
	}
	return r.normalizeStoredTimes(ctx)
}

type storedTimeColumn struct {
	table    string
	idColumn string
	column   string
}

func (r *Repository) normalizeStoredTimes(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin stored time normalization: %w", err)
	}
	defer tx.Rollback()

	columns := []storedTimeColumn{
		{table: "files", idColumn: "id", column: "created_at"},
		{table: "files", idColumn: "id", column: "updated_at"},
		{table: "files", idColumn: "id", column: "missing_at"},
		{table: "jobs", idColumn: "id", column: "started_at"},
		{table: "jobs", idColumn: "id", column: "finished_at"},
		{table: "replacement_journal", idColumn: "job_id", column: "created_at"},
		{table: "replacement_journal", idColumn: "job_id", column: "updated_at"},
		{table: "failure_issues", idColumn: "id", column: "next_retry_at"},
		{table: "failure_issues", idColumn: "id", column: "confirmed_at"},
		{table: "failure_issues", idColumn: "id", column: "created_at"},
		{table: "failure_issues", idColumn: "id", column: "last_seen_at"},
		{table: "failure_issues", idColumn: "id", column: "resolved_at"},
		{table: "recovery_audit", idColumn: "id", column: "created_at"},
	}
	for _, target := range columns {
		if err := normalizeStoredTimeColumn(ctx, tx, target); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit stored time normalization: %w", err)
	}
	return nil
}

func normalizeStoredTimeColumn(ctx context.Context, tx *sql.Tx, target storedTimeColumn) error {
	query := fmt.Sprintf("SELECT %s, %s FROM %s", target.idColumn, target.column, target.table)
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("load %s.%s timestamps: %w", target.table, target.column, err)
	}
	type storedTimeUpdate struct {
		id    int64
		value string
	}
	updates := make([]storedTimeUpdate, 0)
	for rows.Next() {
		var id int64
		var raw sql.NullString
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return fmt.Errorf("scan %s.%s timestamp: %w", target.table, target.column, err)
		}
		if !raw.Valid {
			continue
		}
		parsed, err := parseTime(raw.String)
		if err != nil {
			rows.Close()
			return fmt.Errorf("parse %s.%s timestamp for id %d: %w", target.table, target.column, id, err)
		}
		formatted := formatTime(parsed)
		if formatted != raw.String {
			updates = append(updates, storedTimeUpdate{id: id, value: formatted})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate %s.%s timestamps: %w", target.table, target.column, err)
	}
	rows.Close()
	for _, update := range updates {
		statement := fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", target.table, target.column, target.idColumn)
		if _, err := tx.ExecContext(ctx, statement, update.value, update.id); err != nil {
			return fmt.Errorf("normalize %s.%s timestamp for id %d: %w", target.table, target.column, update.id, err)
		}
	}
	return nil
}

func (r *Repository) validateSchema(ctx context.Context) error {
	return r.validateSchemaColumns(ctx, schemaV10Columns)
}

func (r *Repository) validateSchemaColumns(ctx context.Context, expected map[string][]string) error {
	for _, table := range []string{"files", "jobs", "backups", "replacement_journal", "failure_issues", "recovery_audit"} {
		want, exists := expected[table]
		if !exists {
			continue
		}
		rows, err := r.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
		if err != nil {
			return fmt.Errorf("inspect %s schema: %w", table, err)
		}
		columns := make([]string, 0, len(want))
		for rows.Next() {
			var cid int
			var name string
			var columnType string
			var notNull int
			var defaultValue any
			var primaryKey int
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				return fmt.Errorf("scan %s schema: %w", table, err)
			}
			columns = append(columns, name)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate %s schema rows: %w", table, err)
		}
		rows.Close()
		if len(columns) != len(want) {
			return fmt.Errorf("unsupported %s schema columns: got %v, want %v", table, columns, want)
		}
		for index := range want {
			if columns[index] != want[index] {
				return fmt.Errorf("unsupported %s schema columns: got %v, want %v", table, columns, want)
			}
		}
	}
	return nil
}

func (r *Repository) migrateV1ToV2(ctx context.Context) error {
	return runSQLiteMigration(ctx, sqlMigrationBeginner{db: r.db}, "v1 to v2", []string{
		`ALTER TABLE files ADD COLUMN compliance_status TEXT NULL
CHECK (compliance_status IS NULL OR compliance_status IN ('compliant', 'noncompliant'))`,
		`ALTER TABLE files ADD COLUMN audio_policy_version INTEGER NULL
CHECK (
  (compliance_status IS NULL AND audio_policy_version IS NULL)
  OR (
    compliance_status IS NOT NULL
    AND
    compliance_status IN ('compliant', 'noncompliant')
    AND typeof(audio_policy_version) = 'integer'
    AND audio_policy_version >= 1
  )
)`,
		`PRAGMA user_version = 2`,
	})
}

func (r *Repository) migrateV2ToV3(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite v2 to v3 migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `ALTER TABLE files ADD COLUMN backup_file TEXT NULL`); err != nil {
		return fmt.Errorf("add files.backup_file: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
SELECT jobs.file_id, backups.backup_path
FROM backups
JOIN jobs ON jobs.id = backups.created_by_job_id
WHERE backups.restored_by_job_id IS NULL
ORDER BY backups.created_at DESC, backups.id DESC`)
	if err != nil {
		return fmt.Errorf("load legacy backups: %w", err)
	}
	usable := make(map[int64][]string)
	for rows.Next() {
		var fileID int64
		var path string
		if err := rows.Scan(&fileID, &path); err != nil {
			rows.Close()
			return fmt.Errorf("scan legacy backup: %w", err)
		}
		info, statErr := os.Stat(path)
		if statErr == nil && info.Mode().IsRegular() {
			usable[fileID] = append(usable[fileID], path)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate legacy backup rows: %w", err)
	}
	rows.Close()
	for fileID, paths := range usable {
		if len(paths) > 1 {
			return fmt.Errorf("file %d has %d usable legacy backups; resolve them before migration", fileID, len(paths))
		}
		if _, err := tx.ExecContext(ctx, `UPDATE files SET backup_file = ? WHERE id = ?`, paths[0], fileID); err != nil {
			return fmt.Errorf("migrate legacy backup for file %d: %w", fileID, err)
		}
	}

	statements := []string{
		`DROP TABLE backups`,
		`CREATE UNIQUE INDEX idx_files_backup_file ON files(backup_file) WHERE backup_file IS NOT NULL`,
		`CREATE TABLE replacement_journal (
  job_id INTEGER PRIMARY KEY,
  file_id INTEGER NOT NULL UNIQUE,
  original_path TEXT NOT NULL UNIQUE,
  temporary_backup_path TEXT NOT NULL UNIQUE,
  output_path TEXT,
  phase TEXT NOT NULL CHECK(phase IN ('prepared','backup_ready','output_installed','restoring','committed')),
  original_size INTEGER NOT NULL CHECK(original_size >= 0),
  original_mtime_ns INTEGER NOT NULL CHECK(original_mtime_ns >= 0),
  original_audio_signature TEXT,
  original_video_signature TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE RESTRICT,
  FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT
)`,
		`CREATE INDEX idx_replacement_journal_phase ON replacement_journal(phase, updated_at)`,
		`PRAGMA user_version = 3`,
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate sqlite v2 to v3: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite v2 to v3 migration: %w", err)
	}
	return nil
}

func (r *Repository) migrateV3ToV4(ctx context.Context) error {
	return runSQLiteMigration(ctx, sqlMigrationBeginner{db: r.db}, "v3 to v4", []string{
		`CREATE TABLE jobs_v4 (
  id INTEGER PRIMARY KEY,
  file_id INTEGER NOT NULL,
  trigger_source TEXT NOT NULL CHECK(trigger_source IN ('scan','watchdog','manual')),
  result TEXT NOT NULL CHECK(result IN ('processing','compatible','succeeded','failed')),
  final_error TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT,
  CHECK((result = 'failed' AND final_error IS NOT NULL) OR (result <> 'failed' AND final_error IS NULL)),
  CHECK((result = 'processing' AND finished_at IS NULL) OR (result <> 'processing' AND finished_at IS NOT NULL))
)`,
		`INSERT INTO jobs_v4 (
  id, file_id, trigger_source, result, final_error, started_at, finished_at
)
SELECT id, file_id, trigger_source, result,
  CASE
    WHEN result = 'failed' THEN COALESCE(final_error, 'legacy job failed')
    ELSE NULL
  END,
  started_at,
  CASE
    WHEN result = 'processing' THEN NULL
    ELSE COALESCE(finished_at, started_at)
  END
FROM jobs
WHERE kind = 'process'`,
		`CREATE TABLE replacement_journal_v4 (
  job_id INTEGER PRIMARY KEY,
  file_id INTEGER NOT NULL UNIQUE,
  original_path TEXT NOT NULL UNIQUE,
  temporary_backup_path TEXT NOT NULL UNIQUE,
  output_path TEXT,
  phase TEXT NOT NULL CHECK(phase IN ('prepared','backup_ready','output_installed','restoring','committed')),
  original_size INTEGER NOT NULL CHECK(original_size >= 0),
  original_mtime_ns INTEGER NOT NULL CHECK(original_mtime_ns >= 0),
  original_audio_signature TEXT,
  original_video_signature TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(job_id) REFERENCES jobs_v4(id) ON DELETE RESTRICT,
  FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT
)`,
		`INSERT INTO replacement_journal_v4 (
  job_id, file_id, original_path, temporary_backup_path, output_path, phase,
  original_size, original_mtime_ns, original_audio_signature, original_video_signature,
  last_error, created_at, updated_at
)
SELECT replacement_journal.job_id, replacement_journal.file_id,
  replacement_journal.original_path, replacement_journal.temporary_backup_path,
  replacement_journal.output_path, replacement_journal.phase,
  replacement_journal.original_size, replacement_journal.original_mtime_ns,
  replacement_journal.original_audio_signature, replacement_journal.original_video_signature,
  replacement_journal.last_error, replacement_journal.created_at, replacement_journal.updated_at
FROM replacement_journal
JOIN jobs ON jobs.id = replacement_journal.job_id
WHERE jobs.kind = 'process'`,
		`DROP TABLE replacement_journal`,
		`DROP TABLE jobs`,
		`ALTER TABLE jobs_v4 RENAME TO jobs`,
		`ALTER TABLE replacement_journal_v4 RENAME TO replacement_journal`,
		`CREATE INDEX idx_jobs_result_finished_at ON jobs(result, finished_at DESC, id DESC)`,
		`CREATE INDEX idx_jobs_file_id_finished_at ON jobs(file_id, finished_at DESC, id DESC)`,
		`CREATE INDEX idx_replacement_journal_phase ON replacement_journal(phase, updated_at)`,
		`PRAGMA user_version = 4`,
	})
}

func (r *Repository) migrateV4ToV5(ctx context.Context) error {
	return runSQLiteMigration(ctx, sqlMigrationBeginner{db: r.db}, "v4 to v5", []string{
		`ALTER TABLE jobs ADD COLUMN ignored INTEGER NOT NULL DEFAULT 0 CHECK(ignored IN (0,1))`,
		`PRAGMA user_version = 5`,
	})
}

func (r *Repository) migrateV5ToV6(ctx context.Context) error {
	return runSQLiteMigration(ctx, sqlMigrationBeginner{db: r.db}, "v5 to v6", []string{
		`ALTER TABLE files ADD COLUMN missing_at TEXT NULL`,
		`PRAGMA user_version = 6`,
	})
}

func (r *Repository) migrateV6ToV7(ctx context.Context) error {
	return runSQLiteMigration(ctx, sqlMigrationBeginner{db: r.db}, "v6 to v7", []string{
		`ALTER TABLE files ADD COLUMN compatibility_assessment TEXT NULL`,
		`PRAGMA user_version = 7`,
	})
}

func (r *Repository) migrateV7ToV8(ctx context.Context) error {
	return runSQLiteMigration(ctx, sqlMigrationBeginner{db: r.db}, "v7 to v8", []string{
		`ALTER TABLE jobs ADD COLUMN failure_code TEXT NULL`,
		`ALTER TABLE jobs ADD COLUMN quality_assessment TEXT NULL`,
		`CREATE TABLE replacement_journal_v8 (
  job_id INTEGER PRIMARY KEY,
  file_id INTEGER NOT NULL UNIQUE,
  original_path TEXT NOT NULL UNIQUE,
  temporary_backup_path TEXT NOT NULL UNIQUE,
  output_path TEXT,
  phase TEXT NOT NULL CHECK(phase IN ('prepared','backup_ready','installing','output_installed','restoring','manual_recovery','committed')),
  original_size INTEGER NOT NULL CHECK(original_size >= 0),
  original_mtime_ns INTEGER NOT NULL CHECK(original_mtime_ns >= 0),
  original_audio_signature TEXT,
  original_video_signature TEXT,
  output_size INTEGER NULL CHECK(output_size IS NULL OR output_size >= 0),
  output_mtime_ns INTEGER NULL CHECK(output_mtime_ns IS NULL OR output_mtime_ns >= 0),
  output_audio_signature TEXT,
  output_video_signature TEXT,
  quality_assessment TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE RESTRICT,
  FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT
)`,
		`INSERT INTO replacement_journal_v8 (
  job_id, file_id, original_path, temporary_backup_path, output_path, phase,
  original_size, original_mtime_ns, original_audio_signature, original_video_signature,
  last_error, created_at, updated_at
)
SELECT job_id, file_id, original_path, temporary_backup_path, output_path, phase,
  original_size, original_mtime_ns, original_audio_signature, original_video_signature,
  last_error, created_at, updated_at
FROM replacement_journal`,
		`DROP TABLE replacement_journal`,
		`ALTER TABLE replacement_journal_v8 RENAME TO replacement_journal`,
		`CREATE INDEX idx_replacement_journal_phase ON replacement_journal(phase, updated_at)`,
		`PRAGMA user_version = 8`,
	})
}

func (r *Repository) migrateV8ToV9(ctx context.Context) error {
	return runSQLiteMigration(ctx, sqlMigrationBeginner{db: r.db}, "v8 to v9", failureGovernanceV9Schema())
}

func failureGovernanceV9Schema() []string {
	return []string{
		`CREATE TABLE failure_issues (id INTEGER PRIMARY KEY,file_id INTEGER NOT NULL,origin_job_id INTEGER,stage TEXT NOT NULL,category TEXT NOT NULL,code TEXT NOT NULL,summary TEXT NOT NULL,advice TEXT NOT NULL,retry_strategy TEXT NOT NULL CHECK(retry_strategy IN ('suppress','backoff','conditional','manual_recovery')),unlock_condition TEXT NOT NULL,next_retry_at TEXT,file_size INTEGER NOT NULL,file_mtime_ns INTEGER NOT NULL,policy_version INTEGER NOT NULL,occurrence_count INTEGER NOT NULL DEFAULT 1 CHECK(occurrence_count>=1),last_attempt_source TEXT,confirmed_at TEXT,created_at TEXT NOT NULL,last_seen_at TEXT NOT NULL,resolved_at TEXT,FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT,FOREIGN KEY(origin_job_id) REFERENCES jobs(id) ON DELETE SET NULL)`,
		`CREATE UNIQUE INDEX idx_failure_issues_active_file ON failure_issues(file_id) WHERE resolved_at IS NULL`,
		`CREATE INDEX idx_failure_issues_active ON failure_issues(resolved_at,last_seen_at)`,
		`CREATE TABLE recovery_audit (id INTEGER PRIMARY KEY,file_id INTEGER NOT NULL,job_id INTEGER,action TEXT NOT NULL,source TEXT NOT NULL,result TEXT NOT NULL,detail TEXT,created_at TEXT NOT NULL,FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT,FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE SET NULL)`,
		`CREATE INDEX idx_recovery_audit_file ON recovery_audit(file_id,created_at,id)`,
		`PRAGMA user_version = 9`,
	}
}

func (r *Repository) migrateV9ToV10(ctx context.Context) error {
	return runSQLiteMigration(ctx, sqlMigrationBeginner{db: r.db}, "v9 to v10", []string{
		`ALTER TABLE jobs ADD COLUMN parent_job_id INTEGER NULL REFERENCES jobs(id) ON DELETE SET NULL`,
		`ALTER TABLE jobs ADD COLUMN failure_stage TEXT NULL`,
		`ALTER TABLE jobs ADD COLUMN failure_category TEXT NULL`,
		`ALTER TABLE jobs ADD COLUMN failure_summary TEXT NULL`,
		`ALTER TABLE jobs ADD COLUMN failure_advice TEXT NULL`,
		`ALTER TABLE jobs ADD COLUMN retry_strategy TEXT NULL`,
		`ALTER TABLE jobs ADD COLUMN unlock_condition TEXT NULL`,
		`ALTER TABLE failure_issues ADD COLUMN attempt_number INTEGER NOT NULL DEFAULT 0 CHECK(attempt_number>=0)`,
		`ALTER TABLE failure_issues ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 0 CHECK(max_attempts>=0)`,
		`ALTER TABLE recovery_audit ADD COLUMN actor TEXT NOT NULL DEFAULT 'system'`,
		`ALTER TABLE recovery_audit ADD COLUMN original_job_id INTEGER NULL REFERENCES jobs(id) ON DELETE SET NULL`,
		`ALTER TABLE recovery_audit ADD COLUMN original_failure_code TEXT NULL`,
		`ALTER TABLE recovery_audit ADD COLUMN original_failure TEXT NULL`,
		`PRAGMA user_version = 10`,
	})
}

func failureGovernanceSchema(ifNotExists, versionPrefix string) []string {
	return []string{
		`CREATE TABLE ` + ifNotExists + `failure_issues (
 id INTEGER PRIMARY KEY, file_id INTEGER NOT NULL, origin_job_id INTEGER,
 stage TEXT NOT NULL, category TEXT NOT NULL, code TEXT NOT NULL, summary TEXT NOT NULL, advice TEXT NOT NULL,
 retry_strategy TEXT NOT NULL CHECK(retry_strategy IN ('suppress','backoff','conditional','manual_recovery')),
 unlock_condition TEXT NOT NULL, next_retry_at TEXT, file_size INTEGER NOT NULL, file_mtime_ns INTEGER NOT NULL,
	 policy_version INTEGER NOT NULL, occurrence_count INTEGER NOT NULL DEFAULT 1 CHECK(occurrence_count>=1),
 last_attempt_source TEXT, confirmed_at TEXT, created_at TEXT NOT NULL, last_seen_at TEXT NOT NULL, resolved_at TEXT,
	 attempt_number INTEGER NOT NULL DEFAULT 0 CHECK(attempt_number>=0), max_attempts INTEGER NOT NULL DEFAULT 0 CHECK(max_attempts>=0),
 FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT, FOREIGN KEY(origin_job_id) REFERENCES jobs(id) ON DELETE SET NULL)`,
		`CREATE UNIQUE INDEX ` + ifNotExists + `idx_failure_issues_active_file ON failure_issues(file_id) WHERE resolved_at IS NULL`,
		`CREATE INDEX ` + ifNotExists + `idx_failure_issues_active ON failure_issues(resolved_at,last_seen_at)`,
		`CREATE TABLE ` + ifNotExists + `recovery_audit (id INTEGER PRIMARY KEY,file_id INTEGER NOT NULL,job_id INTEGER,action TEXT NOT NULL,source TEXT NOT NULL,result TEXT NOT NULL,detail TEXT,created_at TEXT NOT NULL,actor TEXT NOT NULL DEFAULT 'system',original_job_id INTEGER,original_failure_code TEXT,original_failure TEXT,FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT,FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE SET NULL,FOREIGN KEY(original_job_id) REFERENCES jobs(id) ON DELETE SET NULL)`,
		`CREATE INDEX ` + ifNotExists + `idx_recovery_audit_file ON recovery_audit(file_id,created_at,id)`,
		versionPrefix + `PRAGMA user_version = 10`,
	}
}

type sqliteMigrationBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (sqliteMigrationTx, error)
}

type sqliteMigrationTx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	Commit() error
	Rollback() error
}

type sqlMigrationBeginner struct {
	db *sql.DB
}

func (b sqlMigrationBeginner) BeginTx(ctx context.Context, options *sql.TxOptions) (sqliteMigrationTx, error) {
	return b.db.BeginTx(ctx, options)
}

func runSQLiteMigration(ctx context.Context, beginner sqliteMigrationBeginner, name string, statements []string) error {
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite %s migration: %w", name, err)
	}
	defer tx.Rollback()
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate sqlite %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite %s: %w", name, err)
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect sqlite table %s: %w", table, err)
	}
	return count == 1, nil
}

var schemaV1Columns = map[string][]string{
	"files":   {"id", "file_path", "size", "mtime_ns", "audio_signature", "video_signature", "created_at", "updated_at"},
	"jobs":    {"id", "file_id", "kind", "trigger_source", "result", "final_error", "started_at", "finished_at"},
	"backups": {"id", "created_by_job_id", "backup_path", "created_at", "restored_by_job_id", "restore_safety_path"},
}

var schemaV2Columns = map[string][]string{
	"files": {
		"id", "file_path", "size", "mtime_ns", "audio_signature", "video_signature",
		"created_at", "updated_at", "compliance_status", "audio_policy_version",
	},
	"jobs":    {"id", "file_id", "kind", "trigger_source", "result", "final_error", "started_at", "finished_at"},
	"backups": {"id", "created_by_job_id", "backup_path", "created_at", "restored_by_job_id", "restore_safety_path"},
}

var schemaV3Columns = map[string][]string{
	"files": {
		"id", "file_path", "size", "mtime_ns", "audio_signature", "video_signature",
		"created_at", "updated_at", "compliance_status", "audio_policy_version", "backup_file",
	},
	"jobs": {"id", "file_id", "kind", "trigger_source", "result", "final_error", "started_at", "finished_at"},
	"replacement_journal": {
		"job_id", "file_id", "original_path", "temporary_backup_path", "output_path", "phase",
		"original_size", "original_mtime_ns", "original_audio_signature", "original_video_signature",
		"last_error", "created_at", "updated_at",
	},
}

var schemaV4Columns = map[string][]string{
	"files": {
		"id", "file_path", "size", "mtime_ns", "audio_signature", "video_signature",
		"created_at", "updated_at", "compliance_status", "audio_policy_version", "backup_file",
	},
	"jobs": {"id", "file_id", "trigger_source", "result", "final_error", "started_at", "finished_at"},
	"replacement_journal": {
		"job_id", "file_id", "original_path", "temporary_backup_path", "output_path", "phase",
		"original_size", "original_mtime_ns", "original_audio_signature", "original_video_signature",
		"last_error", "created_at", "updated_at",
	},
}

var schemaV5Columns = map[string][]string{
	"files": {
		"id", "file_path", "size", "mtime_ns", "audio_signature", "video_signature",
		"created_at", "updated_at", "compliance_status", "audio_policy_version", "backup_file",
	},
	"jobs": {"id", "file_id", "trigger_source", "result", "final_error", "started_at", "finished_at", "ignored"},
	"replacement_journal": {
		"job_id", "file_id", "original_path", "temporary_backup_path", "output_path", "phase",
		"original_size", "original_mtime_ns", "original_audio_signature", "original_video_signature",
		"last_error", "created_at", "updated_at",
	},
}

var schemaV6Columns = map[string][]string{
	"files": {
		"id", "file_path", "size", "mtime_ns", "audio_signature", "video_signature",
		"created_at", "updated_at", "compliance_status", "audio_policy_version", "backup_file", "missing_at",
	},
	"jobs": {"id", "file_id", "trigger_source", "result", "final_error", "started_at", "finished_at", "ignored"},
	"replacement_journal": {
		"job_id", "file_id", "original_path", "temporary_backup_path", "output_path", "phase",
		"original_size", "original_mtime_ns", "original_audio_signature", "original_video_signature",
		"last_error", "created_at", "updated_at",
	},
}

var schemaV7Columns = map[string][]string{
	"files": {
		"id", "file_path", "size", "mtime_ns", "audio_signature", "video_signature",
		"created_at", "updated_at", "compliance_status", "audio_policy_version", "backup_file", "missing_at",
		"compatibility_assessment",
	},
	"jobs": {"id", "file_id", "trigger_source", "result", "final_error", "started_at", "finished_at", "ignored"},
	"replacement_journal": {
		"job_id", "file_id", "original_path", "temporary_backup_path", "output_path", "phase",
		"original_size", "original_mtime_ns", "original_audio_signature", "original_video_signature",
		"last_error", "created_at", "updated_at",
	},
}

var schemaV8Columns = map[string][]string{
	"files": schemaV7Columns["files"],
	"jobs": {
		"id", "file_id", "trigger_source", "result", "final_error", "started_at", "finished_at", "ignored",
		"failure_code", "quality_assessment",
	},
	"replacement_journal": {
		"job_id", "file_id", "original_path", "temporary_backup_path", "output_path", "phase",
		"original_size", "original_mtime_ns", "original_audio_signature", "original_video_signature",
		"output_size", "output_mtime_ns", "output_audio_signature", "output_video_signature", "quality_assessment",
		"last_error", "created_at", "updated_at",
	},
}

var schemaV9Columns = map[string][]string{
	"files": schemaV8Columns["files"], "jobs": schemaV8Columns["jobs"], "replacement_journal": schemaV8Columns["replacement_journal"],
	"failure_issues": {"id", "file_id", "origin_job_id", "stage", "category", "code", "summary", "advice", "retry_strategy", "unlock_condition", "next_retry_at", "file_size", "file_mtime_ns", "policy_version", "occurrence_count", "last_attempt_source", "confirmed_at", "created_at", "last_seen_at", "resolved_at"},
	"recovery_audit": {"id", "file_id", "job_id", "action", "source", "result", "detail", "created_at"},
}

var schemaV10Columns = map[string][]string{
	"files":               schemaV9Columns["files"],
	"jobs":                append(append([]string{}, schemaV9Columns["jobs"]...), "parent_job_id", "failure_stage", "failure_category", "failure_summary", "failure_advice", "retry_strategy", "unlock_condition"),
	"replacement_journal": schemaV9Columns["replacement_journal"],
	"failure_issues":      append(append([]string{}, schemaV9Columns["failure_issues"]...), "attempt_number", "max_attempts"),
	"recovery_audit":      append(append([]string{}, schemaV9Columns["recovery_audit"]...), "actor", "original_job_id", "original_failure_code", "original_failure"),
}

func ensureParentDir(path string) error {
	if path == "" {
		return errors.New("sqlite repository path is empty")
	}
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}

	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create sqlite repository directory: %w", err)
	}
	return nil
}

func sqliteDSN(path string) string {
	if path == ":memory:" {
		return addSQLitePragmas(&url.URL{Scheme: "file", Opaque: ":memory:"})
	}
	if strings.HasPrefix(path, "file:") {
		u, err := url.Parse(path)
		if err == nil {
			return addSQLitePragmas(u)
		}
	}

	return addSQLitePragmas(&url.URL{Scheme: "file", Path: path})
}

func addSQLitePragmas(u *url.URL) string {
	q := u.Query()
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	return u.String()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFileFacts(row rowScanner) (FileFacts, error) {
	var file FileFacts
	var size sql.NullInt64
	var mtimeNS sql.NullInt64
	var audioSignature sql.NullString
	var videoSignature sql.NullString
	var complianceStatus sql.NullString
	var audioPolicyVersion sql.NullInt64
	var backupFile sql.NullString
	var missingAt sql.NullString
	var compatibilityAssessment sql.NullString
	var createdAt string
	var updatedAt string

	err := row.Scan(
		&file.ID,
		&file.Path,
		&size,
		&mtimeNS,
		&audioSignature,
		&videoSignature,
		&complianceStatus,
		&audioPolicyVersion,
		&backupFile,
		&missingAt,
		&compatibilityAssessment,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return FileFacts{}, err
	}

	var parseErr error
	file.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return FileFacts{}, fmt.Errorf("parse file created_at: %w", parseErr)
	}
	file.UpdatedAt, parseErr = parseTime(updatedAt)
	if parseErr != nil {
		return FileFacts{}, fmt.Errorf("parse file updated_at: %w", parseErr)
	}
	file.Size = nullableInt64Value(size)
	file.MTimeNS = nullableInt64Value(mtimeNS)
	file.AudioSignature = nullableStringValue(audioSignature)
	file.VideoSignature = nullableStringValue(videoSignature)
	file.ComplianceStatus = ComplianceStatus(nullableStringValue(complianceStatus))
	file.AudioPolicyVersion = int(nullableInt64Value(audioPolicyVersion))
	file.BackupFile = nullableStringValue(backupFile)
	if missingAt.Valid {
		value, err := parseTime(missingAt.String)
		if err != nil {
			return FileFacts{}, fmt.Errorf("parse file missing_at: %w", err)
		}
		file.MissingAt = &value
	}
	if compatibilityAssessment.Valid {
		file.CompatibilityAssessment = &compatibility.Assessment{}
		if err := json.Unmarshal([]byte(compatibilityAssessment.String), file.CompatibilityAssessment); err != nil {
			return FileFacts{}, fmt.Errorf("parse file compatibility_assessment: %w", err)
		}
	}
	return file, nil
}

func marshalCompatibilityAssessment(assessment *compatibility.Assessment) (any, error) {
	if assessment == nil {
		return nil, nil
	}
	data, err := json.Marshal(assessment)
	if err != nil {
		return nil, fmt.Errorf("marshal file compatibility_assessment: %w", err)
	}
	return string(data), nil
}

func marshalQualityAssessment(assessment *media.QualityAssessment) (any, error) {
	if assessment == nil {
		return nil, nil
	}
	data, err := json.Marshal(assessment)
	if err != nil {
		return nil, fmt.Errorf("marshal quality_assessment: %w", err)
	}
	return string(data), nil
}

func nullableStringForResult(result JobResult, value string) any {
	if result != JobResultFailed {
		return nil
	}
	return nullableString(value)
}

func failureCodeFromFinalError(finalError string) string {
	prefix, _, found := strings.Cut(finalError, ":")
	if !found {
		return "failed"
	}
	switch prefix {
	case "source_changed", "manual_recovery", "verification_failed", "timeout", "ffprobe_error", "restore_stat_error", "restore_probe_error", "unsupported", "interrupted_replacement":
		return prefix
	default:
		return "failed"
	}
}

func scanFileFactsRows(rows *sql.Rows) ([]FileFacts, error) {
	files := make([]FileFacts, 0)
	for rows.Next() {
		file, err := scanFileFacts(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return files, nil
}

func scanReplacementJournal(row rowScanner) (ReplacementJournal, error) {
	var journal ReplacementJournal
	var outputPath sql.NullString
	var phase string
	var audioSignature sql.NullString
	var videoSignature sql.NullString
	var outputSize sql.NullInt64
	var outputMTimeNS sql.NullInt64
	var outputAudioSignature sql.NullString
	var outputVideoSignature sql.NullString
	var qualityAssessment sql.NullString
	var lastError sql.NullString
	var createdAt string
	var updatedAt string
	if err := row.Scan(
		&journal.JobID,
		&journal.FileID,
		&journal.OriginalPath,
		&journal.TemporaryBackupPath,
		&outputPath,
		&phase,
		&journal.OriginalSize,
		&journal.OriginalMTimeNS,
		&audioSignature,
		&videoSignature,
		&outputSize,
		&outputMTimeNS,
		&outputAudioSignature,
		&outputVideoSignature,
		&qualityAssessment,
		&lastError,
		&createdAt,
		&updatedAt,
	); err != nil {
		return ReplacementJournal{}, err
	}
	journal.OutputPath = nullableStringValue(outputPath)
	journal.Phase = ReplacementPhase(phase)
	journal.OriginalAudioSignature = nullableStringValue(audioSignature)
	journal.OriginalVideoSignature = nullableStringValue(videoSignature)
	journal.OutputSize = nullableInt64Value(outputSize)
	journal.OutputMTimeNS = nullableInt64Value(outputMTimeNS)
	journal.OutputAudioSignature = nullableStringValue(outputAudioSignature)
	journal.OutputVideoSignature = nullableStringValue(outputVideoSignature)
	if qualityAssessment.Valid {
		journal.QualityAssessment = &media.QualityAssessment{}
		if err := json.Unmarshal([]byte(qualityAssessment.String), journal.QualityAssessment); err != nil {
			return ReplacementJournal{}, fmt.Errorf("parse replacement journal quality_assessment: %w", err)
		}
	}
	journal.LastError = nullableStringValue(lastError)
	var err error
	journal.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return ReplacementJournal{}, fmt.Errorf("parse replacement journal created_at: %w", err)
	}
	journal.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return ReplacementJournal{}, fmt.Errorf("parse replacement journal updated_at: %w", err)
	}
	return journal, nil
}

func scanHistoryRecord(row rowScanner) (JobHistoryRecord, error) {
	var record JobHistoryRecord
	var triggerSource string
	var result string
	var finalError sql.NullString
	var failureCode sql.NullString
	var qualityAssessment sql.NullString
	var parentJobID sql.NullInt64
	var failureStage, failureCategory, failureSummary, failureAdvice, retryStrategy, unlockCondition sql.NullString
	var startedAt string
	var finishedAt sql.NullString

	err := row.Scan(
		&record.ID,
		&record.FileID,
		&record.Path,
		&triggerSource,
		&result,
		&finalError,
		&failureCode,
		&qualityAssessment,
		&parentJobID,
		&failureStage,
		&failureCategory,
		&failureSummary,
		&failureAdvice,
		&retryStrategy,
		&unlockCondition,
		&startedAt,
		&finishedAt,
	)
	if err != nil {
		return JobHistoryRecord{}, err
	}

	var parseErr error
	record.StartedAt, parseErr = parseTime(startedAt)
	if parseErr != nil {
		return JobHistoryRecord{}, fmt.Errorf("parse history started_at: %w", parseErr)
	}
	record.FinishedAt, parseErr = parseNullableTime(finishedAt)
	if parseErr != nil {
		return JobHistoryRecord{}, fmt.Errorf("parse history finished_at: %w", parseErr)
	}
	record.TriggerSource = DiscoverySource(triggerSource)
	record.Result = JobResult(result)
	record.FinalError = nullableStringValue(finalError)
	record.FailureCode = nullableStringValue(failureCode)
	record.ParentJobID = nullableInt64Value(parentJobID)
	record.FailureStage = nullableStringValue(failureStage)
	record.FailureCategory = nullableStringValue(failureCategory)
	record.FailureSummary = nullableStringValue(failureSummary)
	record.FailureAdvice = nullableStringValue(failureAdvice)
	record.RetryStrategy = nullableStringValue(retryStrategy)
	record.UnlockCondition = nullableStringValue(unlockCondition)
	if qualityAssessment.Valid {
		record.QualityAssessment = &media.QualityAssessment{}
		if err := json.Unmarshal([]byte(qualityAssessment.String), record.QualityAssessment); err != nil {
			return JobHistoryRecord{}, fmt.Errorf("parse history quality_assessment: %w", err)
		}
	}
	return record, nil
}

func scanHistoryRecords(rows *sql.Rows) ([]JobHistoryRecord, error) {
	records := make([]JobHistoryRecord, 0)
	for rows.Next() {
		record, err := scanHistoryRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func scanJob(row rowScanner) (JobRecord, error) {
	var job JobRecord
	var triggerSource string
	var result string
	var finalError sql.NullString
	var failureCode sql.NullString
	var qualityAssessment sql.NullString
	var parentJobID sql.NullInt64
	var failureStage, failureCategory, failureSummary, failureAdvice, retryStrategy, unlockCondition sql.NullString
	var startedAt string
	var finishedAt sql.NullString
	var ignored int
	if err := row.Scan(
		&job.ID,
		&job.FileID,
		&triggerSource,
		&result,
		&finalError,
		&startedAt,
		&finishedAt,
		&ignored,
		&failureCode,
		&qualityAssessment,
		&parentJobID,
		&failureStage,
		&failureCategory,
		&failureSummary,
		&failureAdvice,
		&retryStrategy,
		&unlockCondition,
	); err != nil {
		return JobRecord{}, err
	}
	var parseErr error
	job.StartedAt, parseErr = parseTime(startedAt)
	if parseErr != nil {
		return JobRecord{}, fmt.Errorf("parse job started_at: %w", parseErr)
	}
	job.FinishedAt, parseErr = parseNullableTime(finishedAt)
	if parseErr != nil {
		return JobRecord{}, fmt.Errorf("parse job finished_at: %w", parseErr)
	}
	job.TriggerSource = DiscoverySource(triggerSource)
	job.Result = JobResult(result)
	job.FinalError = nullableStringValue(finalError)
	job.FailureCode = nullableStringValue(failureCode)
	job.ParentJobID = nullableInt64Value(parentJobID)
	job.FailureStage = nullableStringValue(failureStage)
	job.FailureCategory = nullableStringValue(failureCategory)
	job.FailureSummary = nullableStringValue(failureSummary)
	job.FailureAdvice = nullableStringValue(failureAdvice)
	job.RetryStrategy = nullableStringValue(retryStrategy)
	job.UnlockCondition = nullableStringValue(unlockCondition)
	if qualityAssessment.Valid {
		job.QualityAssessment = &media.QualityAssessment{}
		if err := json.Unmarshal([]byte(qualityAssessment.String), job.QualityAssessment); err != nil {
			return JobRecord{}, fmt.Errorf("parse job quality_assessment: %w", err)
		}
	}
	job.Ignored = ignored != 0
	return job, nil
}

func scanJobs(rows *sql.Rows) ([]JobRecord, error) {
	jobs := make([]JobRecord, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(time.Local).Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func parseNullableTime(value sql.NullString) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, nil
	}
	return parseTime(value.String)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTime(value)
}

func nullableTimePtr(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return formatTime(*value)
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullableInt64Value(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

const fileSelectColumns = `
  id,
  file_path,
  size,
  mtime_ns,
  audio_signature,
  video_signature,
  compliance_status,
  audio_policy_version,
	  backup_file,
	  missing_at,
	  compatibility_assessment,
	  created_at,
  updated_at`

const qualifiedFileSelectColumns = `
  files.id,
  files.file_path,
  files.size,
  files.mtime_ns,
  files.audio_signature,
  files.video_signature,
  files.compliance_status,
  files.audio_policy_version,
	  files.backup_file,
	  files.missing_at,
	  files.compatibility_assessment,
	  files.created_at,
  files.updated_at`

const historySelectColumns = `
	  jobs.id,
	  jobs.file_id,
	  files.file_path,
	  jobs.trigger_source,
	  jobs.result,
	  jobs.final_error,
	  jobs.failure_code,
	  jobs.quality_assessment,
	  jobs.parent_job_id,
	  jobs.failure_stage,
	  jobs.failure_category,
	  jobs.failure_summary,
	  jobs.failure_advice,
	  jobs.retry_strategy,
	  jobs.unlock_condition,
	  jobs.started_at,
	  jobs.finished_at`

const jobSelectColumns = `
  id,
  file_id,
  trigger_source,
  result,
  final_error,
	  started_at,
	  finished_at,
	  ignored,
	  failure_code,
	  quality_assessment,
	  parent_job_id,
	  failure_stage,
	  failure_category,
	  failure_summary,
	  failure_advice,
	  retry_strategy,
	  unlock_condition`

const replacementJournalSelectColumns = `
  job_id,
  file_id,
  original_path,
  temporary_backup_path,
  output_path,
  phase,
  original_size,
  original_mtime_ns,
  original_audio_signature,
	  original_video_signature,
	  output_size,
	  output_mtime_ns,
	  output_audio_signature,
	  output_video_signature,
	  quality_assessment,
  last_error,
  created_at,
  updated_at`

const failureIssueSelectColumns = `
  failure_issues.id, failure_issues.file_id, files.file_path, COALESCE(failure_issues.origin_job_id, 0),
  failure_issues.stage, failure_issues.category, failure_issues.code, failure_issues.summary, failure_issues.advice,
  failure_issues.retry_strategy, failure_issues.unlock_condition, failure_issues.next_retry_at,
  failure_issues.file_size, failure_issues.file_mtime_ns, failure_issues.policy_version,
	  failure_issues.occurrence_count, COALESCE(failure_issues.last_attempt_source, ''), failure_issues.confirmed_at,
	  failure_issues.created_at, failure_issues.last_seen_at, failure_issues.resolved_at,
	  failure_issues.attempt_number, failure_issues.max_attempts`

const failureIssueReturningColumns = `
  id, file_id, (SELECT file_path FROM files WHERE files.id = failure_issues.file_id), COALESCE(origin_job_id, 0),
  stage, category, code, summary, advice, retry_strategy, unlock_condition, next_retry_at,
	  file_size, file_mtime_ns, policy_version, occurrence_count, COALESCE(last_attempt_source, ''), confirmed_at,
	  created_at, last_seen_at, resolved_at, attempt_number, max_attempts`

func scanFailureIssue(row rowScanner) (FailureIssue, error) {
	var issue FailureIssue
	var next, confirmed, resolved sql.NullString
	var created, lastSeen string
	if err := row.Scan(&issue.ID, &issue.FileID, &issue.Path, &issue.OriginJobID, &issue.Stage, &issue.Category, &issue.Code, &issue.Summary, &issue.Advice, &issue.RetryStrategy, &issue.UnlockCondition, &next, &issue.FileSize, &issue.FileMTimeNS, &issue.PolicyVersion, &issue.OccurrenceCount, &issue.LastAttemptSource, &confirmed, &created, &lastSeen, &resolved, &issue.AttemptNumber, &issue.MaxAttempts); err != nil {
		return FailureIssue{}, err
	}
	var err error
	if issue.NextRetryAt, err = parseNullableTimePtr(next); err != nil {
		return FailureIssue{}, err
	}
	if issue.ConfirmedAt, err = parseNullableTimePtr(confirmed); err != nil {
		return FailureIssue{}, err
	}
	if issue.ResolvedAt, err = parseNullableTimePtr(resolved); err != nil {
		return FailureIssue{}, err
	}
	if issue.CreatedAt, err = parseTime(created); err != nil {
		return FailureIssue{}, err
	}
	if issue.LastSeenAt, err = parseTime(lastSeen); err != nil {
		return FailureIssue{}, err
	}
	return issue, nil
}

func parseNullableTimePtr(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS files (
  id INTEGER PRIMARY KEY,
  file_path TEXT NOT NULL UNIQUE,
  size INTEGER CHECK(size IS NULL OR size >= 0),
  mtime_ns INTEGER CHECK(mtime_ns IS NULL OR mtime_ns >= 0),
  audio_signature TEXT,
  video_signature TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  compliance_status TEXT NULL CHECK (
    compliance_status IS NULL
    OR compliance_status IN ('compliant', 'noncompliant')
  ),
	  audio_policy_version INTEGER NULL CHECK (
    (compliance_status IS NULL AND audio_policy_version IS NULL)
    OR (
      compliance_status IS NOT NULL
      AND compliance_status IN ('compliant', 'noncompliant')
      AND typeof(audio_policy_version) = 'integer'
      AND audio_policy_version >= 1
    )
	  ),
	  backup_file TEXT NULL,
	  missing_at TEXT NULL,
	  compatibility_assessment TEXT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY,
  file_id INTEGER NOT NULL,
  trigger_source TEXT NOT NULL CHECK(trigger_source IN ('scan','watchdog','manual')),
  result TEXT NOT NULL CHECK(result IN ('processing','compatible','succeeded','failed')),
  final_error TEXT,
  started_at TEXT NOT NULL,
	finished_at TEXT,
	  ignored INTEGER NOT NULL DEFAULT 0 CHECK(ignored IN (0,1)),
	  failure_code TEXT NULL,
	  quality_assessment TEXT NULL,
	  parent_job_id INTEGER NULL,
	  failure_stage TEXT NULL,
	  failure_category TEXT NULL,
	  failure_summary TEXT NULL,
	  failure_advice TEXT NULL,
	  retry_strategy TEXT NULL,
	  unlock_condition TEXT NULL,
  FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT,
	FOREIGN KEY(parent_job_id) REFERENCES jobs(id) ON DELETE SET NULL,
  CHECK((result = 'failed' AND final_error IS NOT NULL) OR (result <> 'failed' AND final_error IS NULL)),
  CHECK((result = 'processing' AND finished_at IS NULL) OR (result <> 'processing' AND finished_at IS NOT NULL))
);`,
	`CREATE TABLE IF NOT EXISTS replacement_journal (
	  job_id INTEGER PRIMARY KEY,
	  file_id INTEGER NOT NULL UNIQUE,
	  original_path TEXT NOT NULL UNIQUE,
	  temporary_backup_path TEXT NOT NULL UNIQUE,
	  output_path TEXT,
	  phase TEXT NOT NULL CHECK(phase IN ('prepared','backup_ready','installing','output_installed','restoring','manual_recovery','committed')),
	  original_size INTEGER NOT NULL CHECK(original_size >= 0),
	  original_mtime_ns INTEGER NOT NULL CHECK(original_mtime_ns >= 0),
	  original_audio_signature TEXT,
	  original_video_signature TEXT,
	  output_size INTEGER NULL CHECK(output_size IS NULL OR output_size >= 0),
	  output_mtime_ns INTEGER NULL CHECK(output_mtime_ns IS NULL OR output_mtime_ns >= 0),
	  output_audio_signature TEXT,
	  output_video_signature TEXT,
	  quality_assessment TEXT,
	  last_error TEXT,
	  created_at TEXT NOT NULL,
	  updated_at TEXT NOT NULL,
	  FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE RESTRICT,
	  FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT
	);`,
	`CREATE INDEX IF NOT EXISTS idx_files_updated_at_id ON files(updated_at DESC, id DESC);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_files_backup_file ON files(backup_file) WHERE backup_file IS NOT NULL;`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_result_finished_at ON jobs(result, finished_at DESC, id DESC);`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_file_id_finished_at ON jobs(file_id, finished_at DESC, id DESC);`,
	`CREATE INDEX IF NOT EXISTS idx_replacement_journal_phase ON replacement_journal(phase, updated_at);`,
}

func init() {
	governance := failureGovernanceSchema("IF NOT EXISTS ", "")
	schemaStatements = append(schemaStatements, governance[:len(governance)-1]...)
}
