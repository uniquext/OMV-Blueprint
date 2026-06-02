package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type ReplaceResult struct {
	BackupPath      string
	OriginalSize    int64
	OriginalMTimeNS int64
	ExpiresAt       time.Time
}

type RestoreRequest struct {
	OriginalPath string
	BackupPath   string
	MediaRoots   []string
}

type RestoreResult struct {
	SafetyPath string
}

var (
	linkFile   = os.Link
	removeFile = os.Remove
	nowUTC     = func() time.Time {
		return time.Now().UTC()
	}
)

func ReplaceWithBackup(originalPath string, outputPath string, backupRoot string, retention time.Duration) (ReplaceResult, error) {
	originalPath = filepath.Clean(originalPath)
	outputPath = filepath.Clean(outputPath)
	if !filepath.IsAbs(originalPath) {
		return ReplaceResult{}, errors.New("original path must be absolute")
	}
	if !filepath.IsAbs(outputPath) {
		return ReplaceResult{}, errors.New("output path must be absolute")
	}
	if backupRoot == "" {
		return ReplaceResult{}, errors.New("backup root must not be empty")
	}
	if !filepath.IsAbs(backupRoot) {
		return ReplaceResult{}, errors.New("backup root must be absolute")
	}
	if originalPath == outputPath {
		return ReplaceResult{}, errors.New("original path and output path must differ")
	}
	info, err := regularFileInfo("original path", originalPath)
	if err != nil {
		return ReplaceResult{}, err
	}
	if _, err := regularFileInfo("output path", outputPath); err != nil {
		return ReplaceResult{}, err
	}
	now := nowUTC()
	backupPath, err := backupPathForOriginal(backupRoot, originalPath, now)
	if err != nil {
		return ReplaceResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		return ReplaceResult{}, err
	}
	if err := moveFileNoOverwrite(originalPath, backupPath); err != nil {
		return ReplaceResult{}, err
	}
	if err := moveFileNoOverwrite(outputPath, originalPath); err != nil {
		if rollbackErr := moveFileNoOverwrite(backupPath, originalPath); rollbackErr != nil {
			return ReplaceResult{}, fmt.Errorf("replace output failed: %w; rollback failed: %v", err, rollbackErr)
		}
		return ReplaceResult{}, fmt.Errorf("replace output failed: %w", err)
	}
	return ReplaceResult{
		BackupPath:      backupPath,
		OriginalSize:    info.Size(),
		OriginalMTimeNS: info.ModTime().UnixNano(),
		ExpiresAt:       now.Add(retention),
	}, nil
}

func RestoreBackup(req RestoreRequest) (RestoreResult, error) {
	originalPath := filepath.Clean(req.OriginalPath)
	backupPath := filepath.Clean(req.BackupPath)
	if !filepath.IsAbs(originalPath) {
		return RestoreResult{}, errors.New("original path must be absolute")
	}
	if !filepath.IsAbs(backupPath) {
		return RestoreResult{}, errors.New("backup path must be absolute")
	}
	if !insideAnyRoot(originalPath, req.MediaRoots) {
		return RestoreResult{}, errors.New("original path is outside configured media roots")
	}
	if _, err := regularFileInfo("backup path", backupPath); err != nil {
		return RestoreResult{}, err
	}
	safetyPath := ""
	if info, err := os.Lstat(originalPath); err == nil {
		if !info.Mode().IsRegular() {
			return RestoreResult{}, fmt.Errorf("original path is not a regular file: %s", originalPath)
		}
		var pathErr error
		safetyPath, pathErr = uniquePath(originalPath + ".restore-safety." + nowUTC().Format("20060102T150405Z"))
		if pathErr != nil {
			return RestoreResult{}, pathErr
		}
		if err := moveFileNoOverwrite(originalPath, safetyPath); err != nil {
			return RestoreResult{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return RestoreResult{}, fmt.Errorf("original path: %w", err)
	}
	if err := moveFileNoOverwrite(backupPath, originalPath); err != nil {
		if safetyPath != "" {
			if rollbackErr := moveFileNoOverwrite(safetyPath, originalPath); rollbackErr != nil {
				return RestoreResult{}, fmt.Errorf("restore backup failed: %w; rollback failed: %v", err, rollbackErr)
			}
		}
		return RestoreResult{}, fmt.Errorf("restore backup failed: %w", err)
	}
	return RestoreResult{SafetyPath: safetyPath}, nil
}

func insideAnyRoot(path string, roots []string) bool {
	cleanPath := filepath.Clean(path)
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		rel, err := filepath.Rel(cleanRoot, cleanPath)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

func backupPathForOriginal(backupRoot string, originalPath string, timestamp time.Time) (string, error) {
	if !filepath.IsAbs(originalPath) {
		return "", errors.New("original path must be absolute")
	}
	cleanOriginal := filepath.Clean(originalPath)
	relativeOriginal := strings.TrimPrefix(cleanOriginal, filepath.VolumeName(cleanOriginal))
	relativeOriginal = strings.TrimPrefix(relativeOriginal, string(filepath.Separator))
	return uniquePath(filepath.Join(backupRoot, timestamp.UTC().Format("20060102T150405Z"), relativeOriginal))
}

func uniquePath(base string) (string, error) {
	for suffix := 0; ; suffix++ {
		path := base
		if suffix > 0 {
			path = fmt.Sprintf("%s.%d", base, suffix)
		}
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		} else if err != nil {
			return "", err
		}
	}
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

func moveFileNoOverwrite(src string, dst string) error {
	if err := linkFile(src, dst); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("destination already exists: %s: %w", dst, os.ErrExist)
		}
		if !canFallbackFromLinkError(err) {
			return err
		}
		return copyFileThenRemove(src, dst)
	}
	if err := removeFile(src); err != nil {
		_ = removeFile(dst)
		return fmt.Errorf("remove source after linking %s: %w", src, err)
	}
	return nil
}

func canFallbackFromLinkError(err error) bool {
	return errors.Is(err, syscall.EXDEV) ||
		errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP) ||
		errors.Is(err, syscall.EPERM)
}

func copyFileThenRemove(src string, dst string) error {
	info, err := regularFileInfo("source path", src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	removeDst := true
	defer func() {
		if removeDst {
			_ = removeFile(dst)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Chmod(info.Mode().Perm()); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := removeFile(src); err != nil {
		return err
	}
	removeDst = false
	return nil
}
