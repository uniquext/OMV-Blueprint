package observability

import (
	"errors"
	"fmt"
	"io"
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
	file       rotatingFile
	size       int64
	modifiedAt time.Time
	closed     bool
	ops        rotatingFileOps
	now        func() time.Time
}

type rotatingFile interface {
	io.Writer
	Close() error
	Stat() (os.FileInfo, error)
}

type rotatingFileOps struct {
	mkdirAll func(string, os.FileMode) error
	openFile func(string, int, os.FileMode) (rotatingFile, error)
	remove   func(string) error
	rename   func(string, string) error
	stat     func(string) (os.FileInfo, error)
}

var osRotatingFileOps = rotatingFileOps{
	mkdirAll: os.MkdirAll,
	openFile: func(path string, flag int, mode os.FileMode) (rotatingFile, error) {
		return os.OpenFile(path, flag, mode)
	},
	remove: os.Remove,
	rename: os.Rename,
	stat:   os.Stat,
}

func NewRotatingFileWriter(path string, maxBytes int64, maxBackups int) (*RotatingFileWriter, error) {
	return newRotatingFileWriter(path, maxBytes, maxBackups, osRotatingFileOps)
}

func NewRotatingFileWriterWithAge(path string, maxBytes int64, maxBackups int, maxAge time.Duration) (*RotatingFileWriter, error) {
	return newRotatingFileWriterWithAge(path, maxBytes, maxBackups, maxAge, time.Now, osRotatingFileOps)
}

func newRotatingFileWriter(path string, maxBytes int64, maxBackups int, ops rotatingFileOps) (*RotatingFileWriter, error) {
	return newRotatingFileWriterWithAge(path, maxBytes, maxBackups, 0, time.Now, ops)
}

func newRotatingFileWriterWithAge(path string, maxBytes int64, maxBackups int, maxAge time.Duration, now func() time.Time, ops rotatingFileOps) (*RotatingFileWriter, error) {
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
	if now == nil {
		now = time.Now
	}
	if err := ops.mkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, size, modifiedAt, err := openAppendFile(path, ops.openFile)
	if err != nil {
		return nil, err
	}
	writer := &RotatingFileWriter{
		path: path, maxBytes: maxBytes, maxBackups: maxBackups, maxAge: maxAge,
		file: file, size: size, modifiedAt: modifiedAt, ops: ops, now: now,
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
		if err := w.ops.remove(w.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		oldest := rotatedPath(w.path, w.maxBackups)
		if err := w.ops.remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for index := w.maxBackups - 1; index >= 1; index-- {
			from := rotatedPath(w.path, index)
			to := rotatedPath(w.path, index+1)
			if err := w.ops.rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := w.ops.rename(w.path, rotatedPath(w.path, 1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	file, size, modifiedAt, err := openAppendFile(w.path, w.ops.openFile)
	if err != nil {
		return err
	}
	w.file = file
	w.size = size
	w.modifiedAt = modifiedAt
	return nil
}

func (w *RotatingFileWriter) expired(modifiedAt time.Time) bool {
	return w.maxAge > 0 && !modifiedAt.IsZero() && !modifiedAt.Add(w.maxAge).After(w.now())
}

func (w *RotatingFileWriter) pruneExpiredBackups() error {
	if w.maxAge <= 0 {
		return nil
	}
	for index := 1; index <= w.maxBackups; index++ {
		path := rotatedPath(w.path, index)
		info, err := w.ops.stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if w.expired(info.ModTime()) {
			if err := w.ops.remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func openAppendFile(path string, openFile func(string, int, os.FileMode) (rotatingFile, error)) (rotatingFile, int64, time.Time, error) {
	file, err := openFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
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
