package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type CreateRequest struct {
	OriginalPath string
	BackupRoot   string
	FileID       int64
	JobID        int64
}

type CreateResult struct {
	BackupPath string `json:"backup_path"`
}

type RestoreRequest struct {
	OriginalPath string
	BackupPath   string
	BackupRoot   string
	MediaRoots   []string
}

type RestoreResult struct {
	BackupPath string `json:"backup_path"`
}

func Create(req CreateRequest) (CreateResult, error) {
	originalPath := filepath.Clean(req.OriginalPath)
	backupRoot := filepath.Clean(req.BackupRoot)
	if !filepath.IsAbs(originalPath) {
		return CreateResult{}, errors.New("original path must be absolute")
	}
	if !filepath.IsAbs(backupRoot) {
		return CreateResult{}, errors.New("backup root must be absolute")
	}
	if req.FileID < 1 || req.JobID < 1 {
		return CreateResult{}, errors.New("file_id and job_id must be positive")
	}
	if _, err := regularFileInfo("original path", originalPath); err != nil {
		return CreateResult{}, err
	}
	backupPath, _ := TargetPath(req)
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		return CreateResult{}, fmt.Errorf("create backup directory: %w", err)
	}
	if err := copyFileExclusive(originalPath, backupPath); err != nil {
		return CreateResult{}, fmt.Errorf("create backup: %w", err)
	}
	return CreateResult{BackupPath: backupPath}, nil
}

func TargetPath(req CreateRequest) (string, error) {
	originalPath := filepath.Clean(req.OriginalPath)
	backupRoot := filepath.Clean(req.BackupRoot)
	if !filepath.IsAbs(originalPath) {
		return "", errors.New("original path must be absolute")
	}
	if !filepath.IsAbs(backupRoot) {
		return "", errors.New("backup root must be absolute")
	}
	if req.FileID < 1 || req.JobID < 1 {
		return "", errors.New("file_id and job_id must be positive")
	}
	backupPath := filepath.Join(
		backupRoot,
		strconv.FormatInt(req.FileID, 10),
		strconv.FormatInt(req.JobID, 10),
		filepath.Base(originalPath),
	)
	return backupPath, nil
}

func Restore(req RestoreRequest) (RestoreResult, error) {
	originalPath := filepath.Clean(req.OriginalPath)
	backupPath := filepath.Clean(req.BackupPath)
	backupRoot := filepath.Clean(req.BackupRoot)
	if !filepath.IsAbs(originalPath) {
		return RestoreResult{}, errors.New("original path must be absolute")
	}
	if !filepath.IsAbs(backupPath) {
		return RestoreResult{}, errors.New("backup path must be absolute")
	}
	if !insideAnyRoot(originalPath, req.MediaRoots) {
		return RestoreResult{}, errors.New("original path is outside configured media roots")
	}
	if !filepath.IsAbs(backupRoot) || !insideRoot(backupPath, backupRoot) {
		return RestoreResult{}, errors.New("backup path is outside configured backup root")
	}
	if err := Install(backupPath, originalPath); err != nil {
		return RestoreResult{}, fmt.Errorf("restore backup: %w", err)
	}
	return RestoreResult{BackupPath: backupPath}, nil
}

func Install(sourcePath string, destinationPath string) error {
	sourcePath = filepath.Clean(sourcePath)
	destinationPath = filepath.Clean(destinationPath)
	if !filepath.IsAbs(sourcePath) || !filepath.IsAbs(destinationPath) {
		return errors.New("source and destination paths must be absolute")
	}
	info, err := regularFileInfo("source path", sourcePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destinationPath), ".audiocleaner-install-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := copyIntoFile(sourcePath, temporary, info); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		return err
	}
	removeTemporary = false
	return syncDirectory(filepath.Dir(destinationPath))
}

func Remove(path string, backupRoot string) error {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(backupRoot)
	if !filepath.IsAbs(cleanRoot) || !insideRoot(cleanPath, cleanRoot) {
		return errors.New("backup path is outside configured backup root")
	}
	if err := os.Remove(cleanPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	removeEmptyParents(filepath.Dir(cleanPath), cleanRoot)
	return nil
}

func copyFileExclusive(sourcePath string, destinationPath string) error {
	info, err := regularFileInfo("source path", sourcePath)
	if err != nil {
		return err
	}
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	removeDestination := true
	defer func() {
		_ = destination.Close()
		if removeDestination {
			_ = os.Remove(destinationPath)
		}
	}()
	if err := copyIntoFile(sourcePath, destination, info); err != nil {
		return err
	}
	if err := destination.Close(); err != nil {
		return err
	}
	removeDestination = false
	return syncDirectory(filepath.Dir(destinationPath))
}

func copyIntoFile(sourcePath string, destination *os.File, info os.FileInfo) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	if _, err := io.Copy(destination, source); err != nil {
		return err
	}
	if err := destination.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err := destination.Sync(); err != nil {
		return err
	}
	if err := os.Chtimes(destination.Name(), info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	return destination.Sync()
}

func regularFileInfo(label string, path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file: %s", label, path)
	}
	return info, nil
}

func insideAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		if insideRoot(path, root) {
			return true
		}
	}
	return false
}

func insideRoot(path string, root string) bool {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	relative, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func removeEmptyParents(path string, root string) {
	for insideRoot(path, root) && filepath.Clean(path) != filepath.Clean(root) {
		if err := os.Remove(path); err != nil {
			return
		}
		path = filepath.Dir(path)
	}
}
