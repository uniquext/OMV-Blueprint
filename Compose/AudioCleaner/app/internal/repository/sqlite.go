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

func (r *Repository) UpsertFile(ctx context.Context, file FileRecord) (FileRecord, error) {
	now := time.Now().UTC()
	return upsertFile(ctx, r.db, file, now)
}

func upsertFile(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, file FileRecord, now time.Time) (FileRecord, error) {
	createdAt := file.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := file.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	if file.PipelinePhase == "" {
		file.PipelinePhase = PipelinePhasePending
	}
	if file.DiscoverySource == "" {
		file.DiscoverySource = DiscoveryManual
	}

	row := queryer.QueryRowContext(ctx, `
INSERT INTO files (
  file_path,
  status,
  discovery_source,
  failure_cause,
  pipeline_phase,
  fingerprint,
  size,
  mtime_ns,
  audio_signature,
  video_signature,
  attempts,
  last_error,
  created_at,
  updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(file_path) DO UPDATE SET
  status = excluded.status,
  discovery_source = excluded.discovery_source,
  failure_cause = excluded.failure_cause,
  pipeline_phase = excluded.pipeline_phase,
  fingerprint = excluded.fingerprint,
  size = excluded.size,
  mtime_ns = excluded.mtime_ns,
  audio_signature = excluded.audio_signature,
  video_signature = excluded.video_signature,
  attempts = excluded.attempts,
  last_error = excluded.last_error,
  updated_at = ?
RETURNING `+fileSelectColumns, file.Path, string(file.Status),
		string(file.DiscoverySource), nullableEnum(file.FailureCause), string(file.PipelinePhase),
		file.Fingerprint, file.Size, file.MTimeNS, file.AudioSignature,
		file.VideoSignature, file.Attempts, nullableString(file.LastError), formatTime(createdAt), formatTime(updatedAt),
		formatTime(now))

	persisted, err := scanFile(row)
	if err != nil {
		return FileRecord{}, err
	}
	return persisted, nil
}

func (r *Repository) FileByPath(ctx context.Context, path string) (FileRecord, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+fileSelectColumns+` FROM files WHERE file_path = ?`, path)
	file, err := scanFile(row)
	if err != nil {
		return FileRecord{}, err
	}
	return file, nil
}

func (r *Repository) FileByID(ctx context.Context, id int64) (FileRecord, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+fileSelectColumns+` FROM files WHERE id = ?`, id)
	file, err := scanFile(row)
	if err != nil {
		return FileRecord{}, err
	}
	return file, nil
}

func (r *Repository) Files(ctx context.Context) ([]FileRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileSelectColumns+` FROM files ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFiles(rows)
}

func (r *Repository) FilesPage(ctx context.Context, req PageRequest) (PageResult[FileRecord], error) {
	req = req.Normalize()
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&total); err != nil {
		return PageResult[FileRecord]{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileSelectColumns+` FROM files ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`, req.PageSize, req.Offset())
	if err != nil {
		return PageResult[FileRecord]{}, err
	}
	defer rows.Close()
	items, err := scanFiles(rows)
	if err != nil {
		return PageResult[FileRecord]{}, err
	}
	return PageResult[FileRecord]{Items: items, Page: req.Page, PageSize: req.PageSize, Total: total}, nil
}

func (r *Repository) ProcessingFiles(ctx context.Context) ([]FileRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileSelectColumns+` FROM files WHERE status = ? ORDER BY updated_at ASC, id ASC`, string(StatusProcessing))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFiles(rows)
}

func (r *Repository) SaveFile(ctx context.Context, file FileRecord) error {
	_, err := r.UpsertFile(ctx, file)
	return err
}

func (r *Repository) AddJobEvent(ctx context.Context, event JobEvent) error {
	return addJobEvent(ctx, r.db, event, time.Now().UTC())
}

func addJobEvent(ctx context.Context, execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, event JobEvent, now time.Time) error {
	startedAt := event.StartedAt
	if startedAt.IsZero() {
		startedAt = now
	}

	_, err := execer.ExecContext(ctx, `
INSERT INTO job_events (
  file_id,
  event_kind,
  event_code,
  phase,
  status,
  outcome,
  attempt,
  command,
  message,
  error,
  started_at,
  finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.FileID, string(event.EventKind), string(event.EventCode), nullableEnum(event.Phase),
		nullableEnum(event.Status), nullableEnum(event.Outcome), event.Attempt, nullableString(event.Command),
		nullableString(event.Message), nullableString(event.Error), formatTime(startedAt), nullableTime(event.FinishedAt))
	if err != nil {
		return fmt.Errorf("add job event: %w", err)
	}
	return nil
}

func (r *Repository) RecentJobEvents(ctx context.Context, limit int) ([]JobEvent, error) {
	if limit < 1 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id, file_id, event_kind, event_code, phase, status, outcome, attempt, command, message, error, started_at, finished_at
FROM job_events
ORDER BY started_at DESC, id DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]JobEvent, 0)
	for rows.Next() {
		var event JobEvent
		var eventKind string
		var eventCode string
		var phase sql.NullString
		var status sql.NullString
		var outcome sql.NullString
		var command sql.NullString
		var message sql.NullString
		var eventError sql.NullString
		var startedAt string
		var finishedAt sql.NullString
		if err := rows.Scan(
			&event.ID,
			&event.FileID,
			&eventKind,
			&eventCode,
			&phase,
			&status,
			&outcome,
			&event.Attempt,
			&command,
			&message,
			&eventError,
			&startedAt,
			&finishedAt,
		); err != nil {
			return nil, err
		}
		event.EventKind = EventKind(eventKind)
		event.EventCode = EventCode(eventCode)
		event.Phase = PipelinePhase(nullableStringValue(phase))
		event.Status = Status(nullableStringValue(status))
		event.Outcome = EventOutcome(nullableStringValue(outcome))
		event.Command = nullableStringValue(command)
		event.Message = nullableStringValue(message)
		event.Error = nullableStringValue(eventError)
		var parseErr error
		event.StartedAt, parseErr = parseTime(startedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("parse event started_at: %w", parseErr)
		}
		event.FinishedAt, parseErr = parseNullableTime(finishedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("parse event finished_at: %w", parseErr)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *Repository) TranscodeStats(ctx context.Context) (TranscodeStats, error) {
	var stats TranscodeStats
	err := r.db.QueryRowContext(ctx, `
	SELECT
	  COALESCE(SUM(CASE WHEN outcome = 'transcoded' THEN 1 ELSE 0 END), 0),
	  COALESCE(SUM(CASE WHEN outcome = 'failed' THEN 1 ELSE 0 END), 0)
	FROM job_events`).Scan(&stats.Succeeded, &stats.Failed)
	if err != nil {
		return TranscodeStats{}, fmt.Errorf("query transcode stats: %w", err)
	}
	return stats, nil
}

func (r *Repository) AddBackup(ctx context.Context, backup BackupRecord) error {
	createdAt := backup.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	_, err := r.db.ExecContext(ctx, `
	INSERT INTO backups (
	  file_id,
	  backup_path,
	  created_at,
	  restored_at,
	  restore_safety_path
	) VALUES (?, ?, ?, ?, ?)`,
		backup.FileID, backup.BackupPath, formatTime(createdAt),
		nullableTime(backup.RestoredAt), nullableString(backup.RestoreSafetyPath))
	if err != nil {
		return fmt.Errorf("add backup: %w", err)
	}
	return nil
}

func (r *Repository) Backups(ctx context.Context) ([]BackupView, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+backupSelectColumns+` FROM backups JOIN files ON files.id = backups.file_id ORDER BY backups.created_at DESC, backups.id DESC`)
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
	rows, err := r.db.QueryContext(ctx, `SELECT `+backupSelectColumns+` FROM backups JOIN files ON files.id = backups.file_id ORDER BY backups.created_at DESC, backups.id DESC LIMIT ? OFFSET ?`, req.PageSize, req.Offset())
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
	row := r.db.QueryRowContext(ctx, `SELECT `+backupSelectColumns+` FROM backups JOIN files ON files.id = backups.file_id WHERE backups.id = ?`, id)
	return scanBackup(row)
}

func (r *Repository) UnrestoredBackups(ctx context.Context) ([]BackupView, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT `+backupSelectColumns+`
FROM backups
JOIN files ON files.id = backups.file_id
WHERE backups.restored_at IS NULL
ORDER BY backups.created_at ASC, backups.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBackups(rows)
}

func (r *Repository) MarkBackupRestored(ctx context.Context, id int64, safetyPath string) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE backups
SET restored_at = ?, restore_safety_path = ?
WHERE id = ?`, formatTime(time.Now().UTC()), nullableString(safetyPath), id)
	if err != nil {
		return fmt.Errorf("mark backup restored: %w", err)
	}
	return nil
}

func (r *Repository) RecordRestore(ctx context.Context, backupID int64, safetyPath string, file FileRecord, event JobEvent) (FileRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return FileRecord{}, fmt.Errorf("begin restore transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
	UPDATE backups
	SET restored_at = ?, restore_safety_path = ?
	WHERE id = ?`, formatTime(now), nullableString(safetyPath), backupID)
	if err != nil {
		return FileRecord{}, fmt.Errorf("mark backup restored: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return FileRecord{}, fmt.Errorf("check restored backup update: %w", err)
	}
	if affected == 0 {
		return FileRecord{}, fmt.Errorf("mark backup restored: %w", sql.ErrNoRows)
	}

	persisted, err := upsertFile(ctx, tx, file, now)
	if err != nil {
		return FileRecord{}, err
	}
	event.FileID = persisted.ID
	if err := addJobEvent(ctx, tx, event, now); err != nil {
		return FileRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return FileRecord{}, fmt.Errorf("commit restore transaction: %w", err)
	}
	return persisted, nil
}

func (r *Repository) DeleteBackup(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete backup: %w", err)
	}
	return nil
}

func (r *Repository) DeleteUnrestoredBackup(ctx context.Context, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ? AND restored_at IS NULL`, id)
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

	for _, stmt := range schemaStatements {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("initialize sqlite schema: %w", err)
		}
	}

	return nil
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

func scanFile(row rowScanner) (FileRecord, error) {
	var file FileRecord
	var status string
	var discoverySource string
	var failureCause sql.NullString
	var pipelinePhase string
	var lastError sql.NullString
	var createdAt string
	var updatedAt string

	err := row.Scan(
		&file.ID,
		&file.Path,
		&status,
		&discoverySource,
		&failureCause,
		&pipelinePhase,
		&file.Fingerprint,
		&file.Size,
		&file.MTimeNS,
		&file.AudioSignature,
		&file.VideoSignature,
		&file.Attempts,
		&lastError,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return FileRecord{}, err
	}

	var parseErr error
	file.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return FileRecord{}, fmt.Errorf("parse file created_at: %w", parseErr)
	}
	file.UpdatedAt, parseErr = parseTime(updatedAt)
	if parseErr != nil {
		return FileRecord{}, fmt.Errorf("parse file updated_at: %w", parseErr)
	}
	file.Status = Status(status)
	file.DiscoverySource = DiscoverySource(discoverySource)
	file.FailureCause = FailureCause(nullableStringValue(failureCause))
	file.PipelinePhase = PipelinePhase(pipelinePhase)
	file.LastError = nullableStringValue(lastError)

	return file, nil
}

func scanFiles(rows *sql.Rows) ([]FileRecord, error) {
	files := make([]FileRecord, 0)
	for rows.Next() {
		file, err := scanFile(rows)
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

func scanBackup(row rowScanner) (BackupView, error) {
	var backup BackupView
	var createdAt string
	var restoredAt sql.NullString
	var restoreSafetyPath sql.NullString
	err := row.Scan(
		&backup.ID,
		&backup.FileID,
		&backup.OriginalPath,
		&backup.BackupPath,
		&createdAt,
		&restoredAt,
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
	return t.UTC().Format(time.RFC3339Nano)
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

func nullableEnum[T ~string](value T) any {
	if value == "" {
		return nil
	}
	return string(value)
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

func nullableStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

const fileSelectColumns = `
  id,
  file_path,
  status,
  discovery_source,
  failure_cause,
  pipeline_phase,
  fingerprint,
  size,
  mtime_ns,
  audio_signature,
  video_signature,
  attempts,
  last_error,
  created_at,
  updated_at`

const backupSelectColumns = `
  backups.id,
  backups.file_id,
  files.file_path,
  backups.backup_path,
  backups.created_at,
  backups.restored_at,
  backups.restore_safety_path`

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS files (
  id INTEGER PRIMARY KEY,
  file_path TEXT NOT NULL UNIQUE,
  size INTEGER CHECK(size IS NULL OR size >= 0),
  mtime_ns INTEGER CHECK(mtime_ns IS NULL OR mtime_ns >= 0),
  audio_signature TEXT,
  video_signature TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
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
