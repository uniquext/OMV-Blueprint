package config

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

type atomicFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

type atomicFileOps struct {
	mkdirAll   func(string, os.FileMode) error
	createTemp func(string, string) (atomicFile, error)
	remove     func(string) error
	rename     func(string, string) error
	open       func(string) (atomicFile, error)
}

var defaultAtomicFileOps = atomicFileOps{
	mkdirAll:   os.MkdirAll,
	createTemp: func(dir, pattern string) (atomicFile, error) { return os.CreateTemp(dir, pattern) },
	remove:     os.Remove,
	rename:     os.Rename,
	open:       func(path string) (atomicFile, error) { return os.Open(path) },
}

func AtomicWriteJSON(path string, value any) error {
	return atomicWriteJSON(path, value, defaultAtomicFileOps)
}

func atomicWriteJSON(path string, value any, ops atomicFileOps) error {
	dir := filepath.Dir(path)
	if err := ops.mkdirAll(dir, 0755); err != nil {
		return err
	}

	temp, err := ops.createTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = ops.remove(tempName)
		}
	}()

	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := ops.rename(tempName, path); err != nil {
		return err
	}
	removeTemp = false
	if err := syncDirWithOps(dir, ops); err != nil {
		return err
	}
	return nil
}

func syncDir(path string) error {
	return syncDirWithOps(path, defaultAtomicFileOps)
}

func syncDirWithOps(path string, ops atomicFileOps) error {
	dir, err := ops.open(path)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}
