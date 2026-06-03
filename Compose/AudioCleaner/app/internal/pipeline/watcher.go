package pipeline

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	watcher      *fsnotify.Watcher
	onPath       func(string)
	errors       chan error
	once         sync.Once
	errorsMu     sync.Mutex
	errorsClosed bool
}

func NewWatcher(roots []string, onPath func(string)) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		watcher: fsWatcher,
		onPath:  onPath,
		errors:  make(chan error, 64),
	}
	for _, root := range roots {
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
			}
		}
		return
	}
	if w.onPath != nil {
		w.onPath(event.Name)
	}
}

func (w *Watcher) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		return w.watcher.Add(path)
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

func (w *Watcher) closeErrors() {
	w.errorsMu.Lock()
	defer w.errorsMu.Unlock()
	if !w.errorsClosed {
		close(w.errors)
		w.errorsClosed = true
	}
}
