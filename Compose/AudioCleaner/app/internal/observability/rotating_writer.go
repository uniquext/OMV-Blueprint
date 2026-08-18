package observability

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type RotatingFileWriter struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	maxAge     time.Duration
	file       *os.File
	size       int64
	modifiedAt time.Time
	closed     bool
}

func NewRotatingFileWriter(path string, maxBytes int64, maxBackups int) (*RotatingFileWriter, error) {
	return newRotatingFileWriterWithAge(path, maxBytes, maxBackups, 0)
}

func NewRotatingFileWriterWithAge(path string, maxBytes int64, maxBackups int, maxAge time.Duration) (*RotatingFileWriter, error) {
	return newRotatingFileWriterWithAge(path, maxBytes, maxBackups, maxAge)
}

func newRotatingFileWriterWithAge(path string, maxBytes int64, maxBackups int, maxAge time.Duration) (*RotatingFileWriter, error) {
	if path == "" {
		return nil, errors.New("log path is required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("log max bytes must be positive")
	}
	if maxBackups < 0 {
		return nil, errors.New("log max backups must not be negative")
	}
	if maxAge < 0 {
		return nil, errors.New("log max age must not be negative")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, size, modifiedAt, err := openAppendFile(path)
	if err != nil {
		return nil, err
	}
	writer := &RotatingFileWriter{
		path: path, maxBytes: maxBytes, maxBackups: maxBackups, maxAge: maxAge,
		file: file, size: size, modifiedAt: modifiedAt,
	}
	if writer.size > 0 && writer.expired(writer.modifiedAt) {
		if err := writer.rotate(); err != nil {
			return nil, err
		}
	}
	if err := writer.pruneExpiredBackups(); err != nil {
		_ = writer.file.Close()
		return nil, err
	}
	return writer, nil
}

func (w *RotatingFileWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, errors.New("write closed log")
	}
	if w.size > 0 && w.expired(w.modifiedAt) {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	if err := w.pruneExpiredBackups(); err != nil {
		return 0, err
	}
	if w.size > 0 && int64(len(value)) > w.maxBytes-w.size {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	written, err := w.file.Write(value)
	w.size += int64(written)
	return written, err
}

func (w *RotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.file.Close()
}

func (w *RotatingFileWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	if w.maxBackups == 0 {
		if err := os.Remove(w.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		oldest := rotatedPath(w.path, w.maxBackups)
		if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for index := w.maxBackups - 1; index >= 1; index-- {
			from := rotatedPath(w.path, index)
			to := rotatedPath(w.path, index+1)
			if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := os.Rename(w.path, rotatedPath(w.path, 1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	file, size, modifiedAt, err := openAppendFile(w.path)
	if err != nil {
		return err
	}
	w.file = file
	w.size = size
	w.modifiedAt = modifiedAt
	return nil
}

func (w *RotatingFileWriter) expired(modifiedAt time.Time) bool {
	return w.maxAge > 0 && !modifiedAt.IsZero() && !modifiedAt.Add(w.maxAge).After(time.Now())
}

func (w *RotatingFileWriter) pruneExpiredBackups() error {
	if w.maxAge <= 0 {
		return nil
	}
	for index := 1; index <= w.maxBackups; index++ {
		path := rotatedPath(w.path, index)
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if w.expired(info.ModTime()) {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func openAppendFile(path string) (*os.File, int64, time.Time, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, time.Time{}, err
	}
	return file, info.Size(), info.ModTime(), nil
}

func rotatedPath(path string, index int) string {
	return fmt.Sprintf("%s.%s", path, strconv.Itoa(index))
}
