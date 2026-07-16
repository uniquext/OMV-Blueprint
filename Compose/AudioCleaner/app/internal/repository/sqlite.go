package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
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
  created_at,
  updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(file_path) DO UPDATE SET
  size = excluded.size,
  mtime_ns = excluded.mtime_ns,
  audio_signature = excluded.audio_signature,
  video_signature = excluded.video_signature,
  compliance_status = excluded.compliance_status,
  audio_policy_version = excluded.audio_policy_version,
  updated_at = ?
RETURNING `+fileSelectColumns, file.Path,
		file.Size, file.MTimeNS, nullableString(file.AudioSignature),
		nullableString(file.VideoSignature), nullableString(string(file.ComplianceStatus)),
		nullableInt64(int64(file.AudioPolicyVersion)), formatTime(createdAt), formatTime(updatedAt),
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

func (r *Repository) History(ctx context.Context) ([]JobHistoryRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+historySelectColumns+`
	FROM jobs
	JOIN files ON files.id = jobs.file_id
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
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`).Scan(&total); err != nil {
		return PageResult[JobHistoryRecord]{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+historySelectColumns+`
	FROM jobs
	JOIN files ON files.id = jobs.file_id
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
	  COALESCE(SUM(CASE WHEN kind = 'process' AND result = 'compatible' THEN 1 ELSE 0 END), 0),
	  COALESCE(SUM(CASE WHEN kind = 'process' AND result = 'succeeded' THEN 1 ELSE 0 END), 0),
	  COALESCE(SUM(CASE WHEN result = 'failed' THEN 1 ELSE 0 END), 0),
	  COALESCE(SUM(CASE WHEN kind = 'restore' AND result = 'succeeded' THEN 1 ELSE 0 END), 0)
	FROM jobs`).Scan(&counts.Compatible, &counts.Processed, &counts.Failed, &counts.Restored)
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
	WHERE kind = 'process'`).Scan(&stats.Succeeded, &stats.Failed)
	if err != nil {
		return ProcessJobStats{}, fmt.Errorf("query process job stats: %w", err)
	}
	return stats, nil
}

func (r *Repository) AddJob(ctx context.Context, job JobRecord) (JobRecord, error) {
	return addJob(ctx, r.db, job, time.Now())
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
		Kind:          JobKindProcess,
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
	if job.Kind == "" {
		job.Kind = JobKindProcess
	}
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
  kind,
  trigger_source,
  result,
  final_error,
  started_at,
  finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING `+jobSelectColumns,
		job.FileID,
		string(job.Kind),
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

func (r *Repository) FinishProcessJob(ctx context.Context, id int64, file FileFacts, result JobResult, finalError string, backup *BackupRecord) (FileFacts, JobRecord, error) {
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
	if backup != nil {
		record := *backup
		record.CreatedByJobID = id
		if err := addBackup(ctx, tx, record, now); err != nil {
			return FileFacts{}, JobRecord{}, err
		}
	}
	job, err := finishJob(ctx, tx, id, result, finalError, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
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
	rows, err := r.db.QueryContext(ctx, `SELECT `+jobSelectColumns+` FROM jobs WHERE result = ? ORDER BY started_at ASC, id ASC`, string(JobResultProcessing))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (r *Repository) AddBackup(ctx context.Context, backup BackupRecord) error {
	return addBackup(ctx, r.db, backup, time.Now())
}

func addBackup(ctx context.Context, execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, backup BackupRecord, now time.Time) error {
	createdAt := backup.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	if backup.CreatedByJobID == 0 {
		return errors.New("created_by_job_id is required")
	}

	_, err := execer.ExecContext(ctx, `
	INSERT INTO backups (
	  created_by_job_id,
	  backup_path,
	  created_at,
	  restored_by_job_id,
	  restore_safety_path
	) VALUES (?, ?, ?, ?, ?)`,
		backup.CreatedByJobID, backup.BackupPath, formatTime(createdAt),
		nullableInt64(backup.RestoredByJobID), nullableString(backup.RestoreSafetyPath))
	if err != nil {
		return fmt.Errorf("add backup: %w", err)
	}
	return nil
}

func (r *Repository) Backups(ctx context.Context) ([]BackupView, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+backupSelectColumns+` FROM `+backupSelectFrom+` ORDER BY backups.created_at DESC, backups.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBackups(rows)
}

func (r *Repository) BackupsPage(ctx context.Context, req PageRequest) (PageResult[BackupView], error) {
	req = req.Normalize()
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backups`).Scan(&total); err != nil {
		return PageResult[BackupView]{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+backupSelectColumns+` FROM `+backupSelectFrom+` ORDER BY backups.created_at DESC, backups.id DESC LIMIT ? OFFSET ?`, req.PageSize, req.Offset())
	if err != nil {
		return PageResult[BackupView]{}, err
	}
	defer rows.Close()
	items, err := scanBackups(rows)
	if err != nil {
		return PageResult[BackupView]{}, err
	}
	return PageResult[BackupView]{Items: items, Page: req.Page, PageSize: req.PageSize, Total: total}, nil
}

func (r *Repository) BackupByID(ctx context.Context, id int64) (BackupView, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+backupSelectColumns+` FROM `+backupSelectFrom+` WHERE backups.id = ?`, id)
	return scanBackup(row)
}

func (r *Repository) UnrestoredBackups(ctx context.Context) ([]BackupView, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT `+backupSelectColumns+`
FROM `+backupSelectFrom+`
WHERE backups.restored_by_job_id IS NULL
ORDER BY backups.created_at ASC, backups.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBackups(rows)
}

func (r *Repository) RecordRestore(ctx context.Context, backupID int64, safetyPath string, file FileFacts, jobResult JobResult, finalError string) (FileFacts, JobRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("begin restore transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	persisted, err := upsertFile(ctx, tx, file, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, err
	}
	restoreJob, err := addJob(ctx, tx, JobRecord{
		FileID:        persisted.ID,
		Kind:          JobKindRestore,
		TriggerSource: DiscoveryManual,
		Result:        jobResult,
		FinalError:    finalError,
		StartedAt:     now,
		FinishedAt:    now,
	}, now)
	if err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("add restore job: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
	UPDATE backups
	SET restored_by_job_id = ?, restore_safety_path = ?
	WHERE id = ?`, restoreJob.ID, nullableString(safetyPath), backupID)
	if err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("mark backup restored: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("check restored backup update: %w", err)
	}
	if affected == 0 {
		return FileFacts{}, JobRecord{}, fmt.Errorf("mark backup restored: %w", sql.ErrNoRows)
	}

	if err := tx.Commit(); err != nil {
		return FileFacts{}, JobRecord{}, fmt.Errorf("commit restore transaction: %w", err)
	}
	return persisted, restoreJob, nil
}

func (r *Repository) DeleteUnrestoredBackup(ctx context.Context, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ? AND restored_by_job_id IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("delete unrestored backup: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check unrestored backup delete: %w", err)
	}
	return affected > 0, nil
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
		if _, err := r.db.ExecContext(ctx, `PRAGMA user_version = 2`); err != nil {
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
	case 2:
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
	table  string
	column string
}

func (r *Repository) normalizeStoredTimes(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin stored time normalization: %w", err)
	}
	defer tx.Rollback()

	columns := []storedTimeColumn{
		{table: "files", column: "created_at"},
		{table: "files", column: "updated_at"},
		{table: "jobs", column: "started_at"},
		{table: "jobs", column: "finished_at"},
		{table: "backups", column: "created_at"},
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
	query := fmt.Sprintf("SELECT id, %s FROM %s", target.column, target.table)
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
		statement := fmt.Sprintf("UPDATE %s SET %s = ? WHERE id = ?", target.table, target.column)
		if _, err := tx.ExecContext(ctx, statement, update.value, update.id); err != nil {
			return fmt.Errorf("normalize %s.%s timestamp for id %d: %w", target.table, target.column, update.id, err)
		}
	}
	return nil
}

func (r *Repository) validateSchema(ctx context.Context) error {
	return r.validateSchemaColumns(ctx, schemaV2Columns)
}

func (r *Repository) validateSchemaColumns(ctx context.Context, expected map[string][]string) error {
	for _, table := range []string{"files", "jobs", "backups"} {
		want := expected[table]
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
	return file, nil
}

func scanHistoryRecord(row rowScanner) (JobHistoryRecord, error) {
	var record JobHistoryRecord
	var kind string
	var triggerSource string
	var result string
	var finalError sql.NullString
	var startedAt string
	var finishedAt sql.NullString

	err := row.Scan(
		&record.ID,
		&record.FileID,
		&record.Path,
		&kind,
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
	record.Kind = JobKind(kind)
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
	var kind string
	var triggerSource string
	var result string
	var finalError sql.NullString
	var startedAt string
	var finishedAt sql.NullString
	if err := row.Scan(
		&job.ID,
		&job.FileID,
		&kind,
		&triggerSource,
		&result,
		&finalError,
		&startedAt,
		&finishedAt,
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
	job.Kind = JobKind(kind)
	job.TriggerSource = DiscoverySource(triggerSource)
	job.Result = JobResult(result)
	job.FinalError = nullableStringValue(finalError)
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

func scanBackup(row rowScanner) (BackupView, error) {
	var backup BackupView
	var createdAt string
	var restoredAt sql.NullString
	var restoredByJobID sql.NullInt64
	var restoreSafetyPath sql.NullString
	err := row.Scan(
		&backup.ID,
		&backup.FileID,
		&backup.CreatedByJobID,
		&backup.OriginalPath,
		&backup.BackupPath,
		&createdAt,
		&restoredAt,
		&restoredByJobID,
		&restoreSafetyPath,
	)
	if err != nil {
		return BackupView{}, err
	}

	var parseErr error
	backup.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return BackupView{}, fmt.Errorf("parse backup created_at: %w", parseErr)
	}
	backup.RestoredAt, parseErr = parseNullableTime(restoredAt)
	if parseErr != nil {
		return BackupView{}, fmt.Errorf("parse backup restored_at: %w", parseErr)
	}
	backup.RestoredByJobID = nullableInt64Value(restoredByJobID)
	backup.RestoreSafetyPath = nullableStringValue(restoreSafetyPath)
	return backup, nil
}

func scanBackups(rows *sql.Rows) ([]BackupView, error) {
	backups := make([]BackupView, 0)
	for rows.Next() {
		backup, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		backups = append(backups, backup)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return backups, nil
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
  files.created_at,
  files.updated_at`

const historySelectColumns = `
	  jobs.id,
	  jobs.file_id,
	  files.file_path,
	  jobs.kind,
	  jobs.trigger_source,
	  jobs.result,
	  jobs.final_error,
	  jobs.started_at,
	  jobs.finished_at`

const jobSelectColumns = `
  id,
  file_id,
  kind,
  trigger_source,
  result,
  final_error,
  started_at,
  finished_at`

const backupSelectColumns = `
  backups.id,
  created_jobs.file_id,
  backups.created_by_job_id,
  files.file_path,
  backups.backup_path,
  backups.created_at,
  restored_jobs.finished_at,
  backups.restored_by_job_id,
  backups.restore_safety_path`

const backupSelectFrom = `backups
JOIN jobs AS created_jobs ON created_jobs.id = backups.created_by_job_id
JOIN files ON files.id = created_jobs.file_id
LEFT JOIN jobs AS restored_jobs ON restored_jobs.id = backups.restored_by_job_id`

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
  )
);`,
	`CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY,
  file_id INTEGER NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('process','restore')),
  trigger_source TEXT NOT NULL CHECK(trigger_source IN ('scan','watchdog','manual')),
  result TEXT NOT NULL CHECK(result IN ('processing','compatible','succeeded','failed')),
  final_error TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  FOREIGN KEY(file_id) REFERENCES files(id) ON DELETE RESTRICT,
  CHECK((result = 'failed' AND final_error IS NOT NULL) OR (result <> 'failed' AND final_error IS NULL)),
  CHECK((result = 'processing' AND finished_at IS NULL) OR (result <> 'processing' AND finished_at IS NOT NULL)),
  CHECK(kind = 'process' OR result <> 'compatible')
);`,
	`CREATE TABLE IF NOT EXISTS backups (
  id INTEGER PRIMARY KEY,
  created_by_job_id INTEGER NOT NULL,
  backup_path TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  restored_by_job_id INTEGER,
  restore_safety_path TEXT,
  FOREIGN KEY(created_by_job_id) REFERENCES jobs(id) ON DELETE RESTRICT,
  FOREIGN KEY(restored_by_job_id) REFERENCES jobs(id) ON DELETE RESTRICT,
  CHECK(restore_safety_path IS NULL OR restored_by_job_id IS NOT NULL)
);`,
	`CREATE INDEX IF NOT EXISTS idx_files_updated_at_id ON files(updated_at DESC, id DESC);`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_result_finished_at ON jobs(result, finished_at DESC, id DESC);`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_file_id_finished_at ON jobs(file_id, finished_at DESC, id DESC);`,
	`CREATE INDEX IF NOT EXISTS idx_backups_created_at_id ON backups(created_at DESC, id DESC);`,
}
