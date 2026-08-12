package pipeline

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	watcher      *fsnotify.Watcher
	onPath       func(string)
	scanConfig   ScanConfig
	errors       chan error
	recoveries   chan string
	once         sync.Once
	errorsMu     sync.Mutex
	errorsClosed bool
	watchedMu    sync.RWMutex
	watched      map[string]struct{}
}

var newFSNotifyWatcher = fsnotify.NewWatcher

func NewWatcher(roots []string, onPath func(string)) (*Watcher, error) {
	return NewFilteredWatcher(ScanConfig{Roots: roots}, onPath)
}

func NewFilteredWatcher(cfg ScanConfig, onPath func(string)) (*Watcher, error) {
	fsWatcher, err := newFSNotifyWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		watcher:    fsWatcher,
		onPath:     onPath,
		scanConfig: cfg,
		errors:     make(chan error, 64),
		recoveries: make(chan string, 16),
		watched:    make(map[string]struct{}),
	}
	for _, root := range cfg.Roots {
		if err := w.addTree(root); err != nil {
			_ = fsWatcher.Close()
			return nil, err
		}
	}
	go w.run()
	return w, nil
}

func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		err = w.watcher.Close()
		w.closeErrors()
	})
	return err
}

func (w *Watcher) Errors() <-chan error {
	return w.errors
}

func (w *Watcher) RecoveryRequests() <-chan string {
	return w.recoveries
}

func (w *Watcher) DirectoryCount() int {
	w.watchedMu.RLock()
	defer w.watchedMu.RUnlock()
	return len(w.watched)
}

func (w *Watcher) run() {
	defer w.closeErrors()
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.reportError(err)
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		w.removeWatchedTree(event.Name)
	}
	if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
		return
	}
	info, err := os.Stat(event.Name)
	if err != nil {
		return
	}
	if info.IsDir() {
		if event.Op&fsnotify.Create == fsnotify.Create {
			if err := w.addTree(event.Name); err != nil {
				w.reportError(err)
			} else {
				w.reportRecovery("directory_registered")
			}
		}
		return
	}
	if w.onPath != nil {
		w.onPath(event.Name)
	}
}

func (w *Watcher) removeWatchedTree(root string) {
	root = filepath.Clean(root)
	prefix := root + string(filepath.Separator)
	w.watchedMu.Lock()
	defer w.watchedMu.Unlock()
	for path := range w.watched {
		if path == root || strings.HasPrefix(path, prefix) {
			delete(w.watched, path)
		}
	}
}

func (w *Watcher) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && IsPathExcluded(path, w.scanConfig.ExcludeDirs, w.scanConfig.ExcludePatterns) {
			return filepath.SkipDir
		}
		w.watchedMu.RLock()
		_, exists := w.watched[path]
		w.watchedMu.RUnlock()
		if exists {
			return nil
		}
		if err := w.watcher.Add(path); err != nil {
			return err
		}
		w.watchedMu.Lock()
		w.watched[path] = struct{}{}
		w.watchedMu.Unlock()
		return nil
	})
}

func (w *Watcher) reportError(err error) {
	if err == nil {
		return
	}
	w.errorsMu.Lock()
	defer w.errorsMu.Unlock()
	if w.errorsClosed {
		return
	}
	select {
	case w.errors <- err:
	default:
	}
}

func (w *Watcher) reportRecovery(reason string) {
	w.errorsMu.Lock()
	defer w.errorsMu.Unlock()
	if w.errorsClosed {
		return
	}
	select {
	case w.recoveries <- reason:
	default:
	}
}

func (w *Watcher) closeErrors() {
	w.errorsMu.Lock()
	defer w.errorsMu.Unlock()
	if !w.errorsClosed {
		close(w.errors)
		close(w.recoveries)
		w.errorsClosed = true
	}
}
