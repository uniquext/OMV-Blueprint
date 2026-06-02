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
	createdAt := file.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := file.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}

	row := r.db.QueryRowContext(ctx, `
INSERT INTO files (
  file_path,
  status,
  qualification_source,
  phase,
  unqualified_reason,
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
  qualification_source = excluded.qualification_source,
  phase = excluded.phase,
  unqualified_reason = excluded.unqualified_reason,
  fingerprint = excluded.fingerprint,
  size = excluded.size,
  mtime_ns = excluded.mtime_ns,
  audio_signature = excluded.audio_signature,
  video_signature = excluded.video_signature,
  attempts = excluded.attempts,
  last_error = excluded.last_error,
  updated_at = ?
RETURNING `+fileSelectColumns, file.Path, string(file.Status), string(file.QualificationSource), string(file.Phase),
		string(file.UnqualifiedReason), file.Fingerprint, file.Size, file.MTimeNS, file.AudioSignature,
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

func (r *Repository) AddJobEvent(ctx context.Context, event JobEvent) error {
	startedAt := event.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}

	_, err := r.db.ExecContext(ctx, `
INSERT INTO job_events (
  file_id,
  event_type,
  phase,
  attempt,
  command,
  message,
  error,
  started_at,
  finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.FileID, event.EventType, string(event.Phase), event.Attempt, event.Command,
		event.Message, event.Error, formatTime(startedAt), formatTime(event.FinishedAt))
	if err != nil {
		return fmt.Errorf("add job event: %w", err)
	}
	return nil
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
	var qualificationSource string
	var phase string
	var unqualifiedReason string
	var createdAt string
	var updatedAt string

	err := row.Scan(
		&file.ID,
		&file.Path,
		&status,
		&qualificationSource,
		&phase,
		&unqualifiedReason,
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
	file.QualificationSource = QualificationSource(qualificationSource)
	file.Phase = Phase(phase)
	file.UnqualifiedReason = UnqualifiedReason(unqualifiedReason)

	return file, nil
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
  qualification_source,
  phase,
  unqualified_reason,
  fingerprint,
  size,
  mtime_ns,
  audio_signature,
  video_signature,
  attempts,
  last_error,
  created_at,
  updated_at`

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS system_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
	`CREATE TABLE IF NOT EXISTS files (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  file_path TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  qualification_source TEXT NOT NULL DEFAULT '',
  phase TEXT NOT NULL DEFAULT '',
  unqualified_reason TEXT NOT NULL DEFAULT '',
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
  event_type TEXT NOT NULL,
  phase TEXT NOT NULL DEFAULT '',
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
