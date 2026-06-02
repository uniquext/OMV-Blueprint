package pipeline

import (
	"io/fs"
	"path/filepath"
	"strings"
)

type ScanConfig struct {
	Roots       []string
	Extensions  []string
	ExcludeDirs []string
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
			info, err := entry.Info()
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
