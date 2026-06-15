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

func (r *Repository) Meta(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM system_meta WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
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
		string(file.DiscoverySource), string(file.FailureCause), string(file.PipelinePhase),
		file.Fingerprint, file.Size, file.MTimeNS, file.AudioSignature,
		file.VideoSignature, file.Attempts, file.LastError, formatTime(createdAt), formatTime(updatedAt),
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
		event.FileID, string(event.EventKind), string(event.EventCode), string(event.Phase),
		string(event.Status), string(event.Outcome), event.Attempt, event.Command,
		event.Message, event.Error, formatTime(startedAt), formatTime(event.FinishedAt))
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
		var phase string
		var status string
		var outcome string
		var startedAt string
		var finishedAt string
		if err := rows.Scan(
			&event.ID,
			&event.FileID,
			&eventKind,
			&eventCode,
			&phase,
			&status,
			&outcome,
			&event.Attempt,
			&event.Command,
			&event.Message,
			&event.Error,
			&startedAt,
			&finishedAt,
		); err != nil {
			return nil, err
		}
		event.EventKind = EventKind(eventKind)
		event.EventCode = EventCode(eventCode)
		event.Phase = PipelinePhase(phase)
		event.Status = Status(status)
		event.Outcome = EventOutcome(outcome)
		var parseErr error
		event.StartedAt, parseErr = parseTime(startedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("parse event started_at: %w", parseErr)
		}
		event.FinishedAt, parseErr = parseTime(finishedAt)
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
  original_path,
  backup_path,
  original_size,
  original_mtime_ns,
  created_at,
  expires_at,
  restored_at,
  restore_safety_path,
  missing
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		backup.FileID, backup.OriginalPath, backup.BackupPath, backup.OriginalSize,
		backup.OriginalMTimeNS, formatTime(createdAt), formatTime(backup.ExpiresAt),
		formatTime(backup.RestoredAt), backup.RestoreSafetyPath, boolInt(backup.Missing))
	if err != nil {
		return fmt.Errorf("add backup: %w", err)
	}
	return nil
}

func (r *Repository) Backups(ctx context.Context) ([]BackupRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+backupSelectColumns+` FROM backups ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBackups(rows)
}

func (r *Repository) BackupsPage(ctx context.Context, req PageRequest) (PageResult[BackupRecord], error) {
	req = req.Normalize()
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backups`).Scan(&total); err != nil {
		return PageResult[BackupRecord]{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+backupSelectColumns+` FROM backups ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, req.PageSize, req.Offset())
	if err != nil {
		return PageResult[BackupRecord]{}, err
	}
	defer rows.Close()
	items, err := scanBackups(rows)
	if err != nil {
		return PageResult[BackupRecord]{}, err
	}
	return PageResult[BackupRecord]{Items: items, Page: req.Page, PageSize: req.PageSize, Total: total}, nil
}

func (r *Repository) BackupByID(ctx context.Context, id int64) (BackupRecord, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+backupSelectColumns+` FROM backups WHERE id = ?`, id)
	return scanBackup(row)
}

func (r *Repository) ExpiredBackups(ctx context.Context, now time.Time) ([]BackupRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT `+backupSelectColumns+`
FROM backups
WHERE expires_at != '' AND expires_at <= ? AND restored_at = ''
ORDER BY expires_at ASC, id ASC`, formatTime(now))
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
WHERE id = ?`, formatTime(time.Now().UTC()), safetyPath, id)
	if err != nil {
		return fmt.Errorf("mark backup restored: %w", err)
	}
	return nil
}

func (r *Repository) MarkBackupMissing(ctx context.Context, id int64, missing bool) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE backups
SET missing = ?
WHERE id = ?`, boolInt(missing), id)
	if err != nil {
		return fmt.Errorf("mark backup missing: %w", err)
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
	WHERE id = ?`, formatTime(now), safetyPath, backupID)
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
	result, err := r.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ? AND restored_at = ''`, id)
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

	_, err := r.db.ExecContext(ctx, `
INSERT OR IGNORE INTO system_meta (key, value) VALUES (?, ?)`,
		"db_initialized_at", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("initialize sqlite metadata: %w", err)
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
	var failureCause string
	var pipelinePhase string
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
		&file.LastError,
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
	file.FailureCause = FailureCause(failureCause)
	file.PipelinePhase = PipelinePhase(pipelinePhase)

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

func scanBackup(row rowScanner) (BackupRecord, error) {
	var backup BackupRecord
	var createdAt string
	var expiresAt string
	var restoredAt string
	var missing int
	err := row.Scan(
		&backup.ID,
		&backup.FileID,
		&backup.OriginalPath,
		&backup.BackupPath,
		&backup.OriginalSize,
		&backup.OriginalMTimeNS,
		&createdAt,
		&expiresAt,
		&restoredAt,
		&backup.RestoreSafetyPath,
		&missing,
	)
	if err != nil {
		return BackupRecord{}, err
	}

	var parseErr error
	backup.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return BackupRecord{}, fmt.Errorf("parse backup created_at: %w", parseErr)
	}
	backup.ExpiresAt, parseErr = parseTime(expiresAt)
	if parseErr != nil {
		return BackupRecord{}, fmt.Errorf("parse backup expires_at: %w", parseErr)
	}
	backup.RestoredAt, parseErr = parseTime(restoredAt)
	if parseErr != nil {
		return BackupRecord{}, fmt.Errorf("parse backup restored_at: %w", parseErr)
	}
	backup.Missing = missing != 0
	return backup, nil
}

func scanBackups(rows *sql.Rows) ([]BackupRecord, error) {
	backups := make([]BackupRecord, 0)
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
  id,
  file_id,
  original_path,
  backup_path,
  original_size,
  original_mtime_ns,
  created_at,
  expires_at,
  restored_at,
  restore_safety_path,
  missing`

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS system_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
	`CREATE TABLE IF NOT EXISTS files (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  file_path TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  discovery_source TEXT NOT NULL DEFAULT '',
  failure_cause TEXT NOT NULL DEFAULT '',
  pipeline_phase TEXT NOT NULL DEFAULT 'pending',
  fingerprint TEXT NOT NULL DEFAULT '',
  size INTEGER NOT NULL DEFAULT 0,
  mtime_ns INTEGER NOT NULL DEFAULT 0,
  audio_signature TEXT NOT NULL DEFAULT '',
  video_signature TEXT NOT NULL DEFAULT '',
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);`,
	`CREATE TABLE IF NOT EXISTS job_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  file_id INTEGER NOT NULL,
  event_kind TEXT NOT NULL DEFAULT '',
  event_code TEXT NOT NULL DEFAULT '',
  phase TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  outcome TEXT NOT NULL DEFAULT '',
  attempt INTEGER NOT NULL DEFAULT 0,
  command TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(file_id) REFERENCES files(id)
);`,
	`CREATE TABLE IF NOT EXISTS backups (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  file_id INTEGER NOT NULL,
  original_path TEXT NOT NULL,
  backup_path TEXT NOT NULL,
  original_size INTEGER NOT NULL DEFAULT 0,
  original_mtime_ns INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL DEFAULT '',
  restored_at TEXT NOT NULL DEFAULT '',
  restore_safety_path TEXT NOT NULL DEFAULT '',
  missing INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY(file_id) REFERENCES files(id)
);`,
}
