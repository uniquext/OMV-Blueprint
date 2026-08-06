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
)

type Repository struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Repository, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", sqliteDSN(path))
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
SET trigger_source = ?, result = ?, final_error = NULL, started_at = ?, finished_at = NULL
WHERE id = ? AND ignored = 0 AND result = ?
RETURNING `+jobSelectColumns,
		string(DiscoveryManual), string(JobResultProcessing), formatTime(now), id, string(JobResultFailed)))
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
	row := queryer.QueryRowContext(ctx, `
INSERT INTO jobs (
  file_id,
  trigger_source,
  result,
  final_error,
  started_at,
  finished_at
) VALUES (?, ?, ?, ?, ?, ?)
RETURNING `+jobSelectColumns,
		job.FileID,
		string(job.TriggerSource),
		string(job.Result),
		nullableString(job.FinalError),
		formatTime(startedAt),
		nullableTime(finishedAt))
	return scanJob(row)
}

func (r *Repository) FinishJob(ctx context.Context, id int64, result JobResult, finalError string) (JobRecord, error) {
	return finishJob(ctx, r.db, id, result, finalError, time.Now())
}

func finishJob(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64, result JobResult, finalError string, now time.Time) (JobRecord, error) {
	row := queryer.QueryRowContext(ctx, `
UPDATE jobs
SET result = ?, final_error = ?, finished_at = ?
WHERE id = ?
RETURNING `+jobSelectColumns,
		string(result),
		nullableString(finalError),
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
	if _, err := tx.ExecContext(ctx, `UPDATE replacement_journal SET phase = ?, updated_at = ? WHERE job_id = ?`,
		string(ReplacementCommitted), formatTime(now), id); err != nil {
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

func (r *Repository) ReplacementJournalByJob(ctx context.Context, jobID int64) (ReplacementJournal, error) {
	return scanReplacementJournal(r.db.QueryRowContext(ctx, `SELECT `+replacementJournalSelectColumns+` FROM replacement_journal WHERE job_id = ?`, jobID))
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
		if _, err := r.db.ExecContext(ctx, `PRAGMA user_version = 7`); err != nil {
			return fmt.Errorf("set sqlite schema version: %w", err)
		}
		return r.normalizeStoredTimes(ctx)
	}

	switch version {
	case 1:
		if err := r.validateSchemaColumns(ctx, schemaV1Columns); err != nil {
			return err
		}
		if err := r.migrateV1ToV2(ctx); err != nil {
			return err
		}
		if err := r.migrateV2ToV3(ctx); err != nil {
			return err
		}
		if err := r.migrateV3ToV4(ctx); err != nil {
			return err
		}
		if err := r.migrateV4ToV5(ctx); err != nil {
			return err
		}
		if err := r.migrateV5ToV6(ctx); err != nil {
			return err
		}
		if err := r.migrateV6ToV7(ctx); err != nil {
			return err
		}
	case 2:
		if err := r.validateSchemaColumns(ctx, schemaV2Columns); err != nil {
			return err
		}
		if err := r.migrateV2ToV3(ctx); err != nil {
			return err
		}
		if err := r.migrateV3ToV4(ctx); err != nil {
			return err
		}
		if err := r.migrateV4ToV5(ctx); err != nil {
			return err
		}
		if err := r.migrateV5ToV6(ctx); err != nil {
			return err
		}
		if err := r.migrateV6ToV7(ctx); err != nil {
			return err
		}
	case 3:
		if err := r.validateSchemaColumns(ctx, schemaV3Columns); err != nil {
			return err
		}
		if err := r.migrateV3ToV4(ctx); err != nil {
			return err
		}
		if err := r.migrateV4ToV5(ctx); err != nil {
			return err
		}
		if err := r.migrateV5ToV6(ctx); err != nil {
			return err
		}
		if err := r.migrateV6ToV7(ctx); err != nil {
			return err
		}
	case 4:
		if err := r.validateSchemaColumns(ctx, schemaV4Columns); err != nil {
			return err
		}
		if err := r.migrateV4ToV5(ctx); err != nil {
			return err
		}
		if err := r.migrateV5ToV6(ctx); err != nil {
			return err
		}
		if err := r.migrateV6ToV7(ctx); err != nil {
			return err
		}
	case 5:
		if err := r.validateSchemaColumns(ctx, schemaV5Columns); err != nil {
			return err
		}
		if err := r.migrateV5ToV6(ctx); err != nil {
			return err
		}
		if err := r.migrateV6ToV7(ctx); err != nil {
			return err
		}
	case 6:
		if err := r.validateSchemaColumns(ctx, schemaV6Columns); err != nil {
			return err
		}
		if err := r.migrateV6ToV7(ctx); err != nil {
			return err
		}
	case 7:
		// Already current.
	default:
		if err := r.validateSchema(ctx); err != nil {
			return err
		}
		return fmt.Errorf("unsupported sqlite schema version %d", version)
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
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close %s.%s timestamps: %w", target.table, target.column, err)
	}
	for _, update := range updates {
		statement := fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", target.table, target.column, target.idColumn)
		if _, err := tx.ExecContext(ctx, statement, update.value, update.id); err != nil {
			return fmt.Errorf("normalize %s.%s timestamp for id %d: %w", target.table, target.column, update.id, err)
		}
	}
	return nil
}

func (r *Repository) validateSchema(ctx context.Context) error {
	return r.validateSchemaColumns(ctx, schemaV7Columns)
}

func (r *Repository) validateSchemaColumns(ctx context.Context, expected map[string][]string) error {
	for _, table := range []string{"files", "jobs", "backups", "replacement_journal"} {
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
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close %s schema rows: %w", table, err)
		}
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite v1 to v2 migration: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
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
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate sqlite v1 to v2: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite v1 to v2 migration: %w", err)
	}
	return nil
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
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy backup rows: %w", err)
	}
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite v3 to v4 migration: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
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
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate sqlite v3 to v4: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite v3 to v4 migration: %w", err)
	}
	return nil
}

func (r *Repository) migrateV4ToV5(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite v4 to v5 migration: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`ALTER TABLE jobs ADD COLUMN ignored INTEGER NOT NULL DEFAULT 0 CHECK(ignored IN (0,1))`,
		`PRAGMA user_version = 5`,
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate sqlite v4 to v5: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite v4 to v5 migration: %w", err)
	}
	return nil
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
	var startedAt string
	var finishedAt sql.NullString

	err := row.Scan(
		&record.ID,
		&record.FileID,
		&record.Path,
		&triggerSource,
		&result,
		&finalError,
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
  ignored`

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
  last_error,
  created_at,
  updated_at`

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
  FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT,
  CHECK((result = 'failed' AND final_error IS NOT NULL) OR (result <> 'failed' AND final_error IS NULL)),
  CHECK((result = 'processing' AND finished_at IS NULL) OR (result <> 'processing' AND finished_at IS NOT NULL))
);`,
	`CREATE TABLE IF NOT EXISTS replacement_journal (
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
	);`,
	`CREATE INDEX IF NOT EXISTS idx_files_updated_at_id ON files(updated_at DESC, id DESC);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_files_backup_file ON files(backup_file) WHERE backup_file IS NOT NULL;`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_result_finished_at ON jobs(result, finished_at DESC, id DESC);`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_file_id_finished_at ON jobs(file_id, finished_at DESC, id DESC);`,
	`CREATE INDEX IF NOT EXISTS idx_replacement_journal_phase ON replacement_journal(phase, updated_at);`,
}
