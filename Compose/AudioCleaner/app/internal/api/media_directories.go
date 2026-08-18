package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

const mediaMountPath = "/media"

type MediaDirectory struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	HasChildren bool   `json:"has_children"`
}

type MediaDirectoryListing struct {
	Parent      string           `json:"parent"`
	Directories []MediaDirectory `json:"directories"`
}

type mediaDirectoryError struct {
	status    int
	errorCode string
	message   string
}

func (e *mediaDirectoryError) Error() string { return e.message }

func (s *Server) handleListMediaDirectories(w http.ResponseWriter, r *http.Request) {
	listing, err := listMediaDirectories(r.Context(), s.deps.MediaRoot, r.URL.Query().Get("parent"))
	if err == nil {
		writeOK(w, listing)
		return
	}
	var directoryError *mediaDirectoryError
	if errors.As(err, &directoryError) {
		writeCodedError(w, directoryError.status, directoryError.errorCode, directoryError.message, nil)
		return
	}
	writeCodedError(w, http.StatusInternalServerError, "directory_read_failed", err.Error(), nil)
}

func listMediaDirectories(ctx context.Context, mediaRoot, parent string) (MediaDirectoryListing, error) {
	if err := ctx.Err(); err != nil {
		return MediaDirectoryListing{}, err
	}
	if parent == "" || !filepath.IsAbs(parent) || filepath.Clean(parent) != parent || (parent != mediaMountPath && !strings.HasPrefix(parent, mediaMountPath+"/")) {
		return MediaDirectoryListing{}, directoryError(http.StatusBadRequest, "directory_invalid", "parent must be /media or a normalized directory below it")
	}

	rootPath, err := filepath.EvalSymlinks(filepath.Clean(mediaRoot))
	if err != nil {
		return MediaDirectoryListing{}, err
	}
	relative := strings.TrimPrefix(parent, mediaMountPath)
	targetPath := filepath.Join(rootPath, filepath.FromSlash(strings.TrimPrefix(relative, "/")))
	if _, err := os.Lstat(targetPath); err != nil {
		if os.IsNotExist(err) {
			return MediaDirectoryListing{}, directoryError(http.StatusNotFound, "directory_not_found", "directory does not exist")
		}
		if os.IsPermission(err) {
			return MediaDirectoryListing{}, directoryError(http.StatusForbidden, "directory_forbidden", "directory is not readable")
		}
		return MediaDirectoryListing{}, err
	}
	resolvedTarget, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return MediaDirectoryListing{}, directoryError(http.StatusNotFound, "directory_not_found", "directory does not exist")
		}
		if os.IsPermission(err) {
			return MediaDirectoryListing{}, directoryError(http.StatusForbidden, "directory_forbidden", "directory is not readable")
		}
		return MediaDirectoryListing{}, err
	}
	if !pathInsideRoot(resolvedTarget, rootPath) {
		return MediaDirectoryListing{}, directoryError(http.StatusForbidden, "directory_forbidden", "directory resolves outside /media")
	}
	info, err := os.Stat(resolvedTarget)
	if err != nil {
		return MediaDirectoryListing{}, err
	}
	if !info.IsDir() {
		return MediaDirectoryListing{}, directoryError(http.StatusBadRequest, "directory_invalid", "parent is not a directory")
	}

	entries, err := os.ReadDir(resolvedTarget)
	if err != nil {
		if os.IsPermission(err) {
			return MediaDirectoryListing{}, directoryError(http.StatusForbidden, "directory_forbidden", "directory is not readable")
		}
		return MediaDirectoryListing{}, err
	}
	directories := make([]MediaDirectory, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return MediaDirectoryListing{}, err
		}
		entryPath := filepath.Join(resolvedTarget, entry.Name())
		resolvedEntry, ok := safeDirectoryPath(entryPath, rootPath)
		if !ok {
			continue
		}
		directories = append(directories, MediaDirectory{
			Name:        entry.Name(),
			Path:        pathpkg.Join(parent, entry.Name()),
			HasChildren: directoryHasChildren(ctx, resolvedEntry, rootPath),
		})
	}
	sort.SliceStable(directories, func(i, j int) bool {
		return naturalCompare(directories[i].Name, directories[j].Name) < 0
	})
	return MediaDirectoryListing{Parent: parent, Directories: directories}, nil
}

func directoryError(status int, errorCode, message string) error {
	return &mediaDirectoryError{status: status, errorCode: errorCode, message: message}
}

func safeDirectoryPath(path, root string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !pathInsideRoot(resolved, root) {
		return "", false
	}
	info, err := os.Stat(resolved)
	return resolved, err == nil && info.IsDir()
}

func directoryHasChildren(ctx context.Context, path, root string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return os.IsPermission(err)
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return false
		}
		if _, ok := safeDirectoryPath(filepath.Join(path, entry.Name()), root); ok {
			return true
		}
	}
	return false
}

func pathInsideRoot(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func naturalCompare(left, right string) int {
	a, b := strings.ToLower(left), strings.ToLower(right)
	for i, j := 0, 0; i < len(a) && j < len(b); {
		if isDigit(a[i]) && isDigit(b[j]) {
			iEnd, jEnd := i, j
			for iEnd < len(a) && isDigit(a[iEnd]) {
				iEnd++
			}
			for jEnd < len(b) && isDigit(b[jEnd]) {
				jEnd++
			}
			iStart, jStart := i, j
			for iStart < iEnd-1 && a[iStart] == '0' {
				iStart++
			}
			for jStart < jEnd-1 && b[jStart] == '0' {
				jStart++
			}
			if iEnd-iStart != jEnd-jStart {
				if iEnd-iStart < jEnd-jStart {
					return -1
				}
				return 1
			}
			if numberCompare := strings.Compare(a[iStart:iEnd], b[jStart:jEnd]); numberCompare != 0 {
				return numberCompare
			}
			i, j = iEnd, jEnd
			continue
		}
		if a[i] != b[j] {
			if a[i] < b[j] {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(left, right)
}

func isDigit(value byte) bool { return value >= '0' && value <= '9' }
