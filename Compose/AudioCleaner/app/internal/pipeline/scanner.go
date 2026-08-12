package pipeline

import (
	"context"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"strings"
)

type ScanConfig struct {
	Roots           []string
	Extensions      []string
	ExcludeDirs     []string
	ExcludePatterns []string
}

type ScanCallbacks struct {
	OnEntry     func(root, path string, entry fs.DirEntry)
	OnDirectory func(root, path string)
	OnCandidate func(root, path string, info fs.FileInfo)
	OnError     func(path string, err error)
}

var readDirEntryInfo = func(entry fs.DirEntry) (fs.FileInfo, error) {
	return entry.Info()
}

// StreamScan walks configured roots once and reports candidates immediately.
// Per-path filesystem errors are reported and skipped so one unreadable branch
// does not hide the rest of a media library.
func StreamScan(ctx context.Context, cfg ScanConfig, callbacks ScanCallbacks) error {
	extensions := extensionSet(cfg.Extensions)
	excludeDirs := nameSet(cfg.ExcludeDirs)

	for _, root := range cfg.Roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				if callbacks.OnError != nil {
					callbacks.OnError(path, walkErr)
				}
				return nil
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if entry.IsDir() {
				if path != root {
					if _, ok := excludeDirs[entry.Name()]; ok || IsPathExcluded(path, cfg.ExcludeDirs, cfg.ExcludePatterns) {
						return filepath.SkipDir
					}
				}
				if callbacks.OnEntry != nil {
					callbacks.OnEntry(root, path, entry)
				}
				if callbacks.OnDirectory != nil {
					callbacks.OnDirectory(root, path)
				}
				return nil
			}
			if callbacks.OnEntry != nil {
				callbacks.OnEntry(root, path, entry)
			}
			if _, ok := extensions[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
				return nil
			}
			if IsPathExcluded(path, cfg.ExcludeDirs, cfg.ExcludePatterns) || IsAudioCleanerTempOutputPath(entry.Name()) {
				return nil
			}
			info, err := readDirEntryInfo(entry)
			if err != nil {
				if callbacks.OnError != nil {
					callbacks.OnError(path, err)
				}
				return nil
			}
			if info.Size() == 0 {
				return nil
			}
			if callbacks.OnCandidate != nil {
				callbacks.OnCandidate(root, path, info)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func ScanRoots(cfg ScanConfig) ([]string, error) {
	extensions := extensionSet(cfg.Extensions)
	excludeDirs := nameSet(cfg.ExcludeDirs)
	var paths []string

	for _, root := range cfg.Roots {
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if entry.IsDir() {
				if _, ok := excludeDirs[entry.Name()]; ok {
					return filepath.SkipDir
				}
				return nil
			}
			if _, ok := extensions[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
				return nil
			}
			if IsPathExcluded(path, cfg.ExcludeDirs, cfg.ExcludePatterns) {
				return nil
			}
			if IsAudioCleanerTempOutputPath(entry.Name()) {
				return nil
			}
			info, err := readDirEntryInfo(entry)
			if err != nil {
				return err
			}
			if info.Size() == 0 {
				return nil
			}
			paths = append(paths, path)
			return nil
		}); err != nil {
			return nil, err
		}
	}

	return paths, nil
}

func IsPathExcluded(path string, excludeDirs []string, excludePatterns []string) bool {
	if hasExcludedDir(path, excludeDirs) {
		return true
	}
	return matchesExcludePattern(path, excludePatterns)
}

func extensionSet(extensions []string) map[string]struct{} {
	set := make(map[string]struct{}, len(extensions))
	for _, extension := range extensions {
		extension = strings.TrimSpace(extension)
		if extension == "" {
			continue
		}
		if !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		set[strings.ToLower(extension)] = struct{}{}
	}
	return set
}

func nameSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		set[name] = struct{}{}
	}
	return set
}

func hasExcludedDir(path string, excludeDirs []string) bool {
	excluded := nameSet(excludeDirs)
	if len(excluded) == 0 {
		return false
	}
	for _, part := range strings.FieldsFunc(filepath.Clean(path), func(r rune) bool {
		return r == filepath.Separator || r == '/'
	}) {
		if _, ok := excluded[part]; ok {
			return true
		}
	}
	return false
}

func matchesExcludePattern(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	candidates := patternCandidates(path)
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		pattern = filepath.ToSlash(pattern)
		for _, candidate := range candidates {
			matched, err := pathpkg.Match(pattern, candidate)
			if err == nil && matched {
				return true
			}
		}
	}
	return false
}

func patternCandidates(path string) []string {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	base := pathpkg.Base(cleaned)
	parts := strings.Split(strings.Trim(cleaned, "/"), "/")

	candidates := []string{base, cleaned}
	for i := range parts {
		suffix := strings.Join(parts[i:], "/")
		if suffix != "" {
			candidates = append(candidates, suffix)
		}
	}
	return candidates
}
