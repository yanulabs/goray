// Package watch monitors a Go project for index-relevant changes.
package watch

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watcher debounces nothing itself; it emits a plain change notification.
type Watcher struct {
	w         *fsnotify.Watcher
	root      string
	log       *slog.Logger
	onChange  func([]string)
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// New recursively watches root and invokes onChange with the absolute paths of
// relevant changes. Callers may debounce and deduplicate the batches.
func New(root string, logger *slog.Logger, onChange func([]string)) (*Watcher, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	watcher := &Watcher{w: w, root: absRoot, log: logger, onChange: onChange, done: make(chan struct{})}
	if err := watcher.addTree(absRoot); err != nil {
		_ = w.Close()
		return nil, err
	}
	watcher.wg.Add(1)
	go watcher.loop()
	return watcher, nil
}

func (w *Watcher) loop() {
	defer w.wg.Done()
	for {
		select {
		case event, ok := <-w.w.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if err := w.addTree(event.Name); err != nil {
						w.log.Warn("watch directory", "path", event.Name, "err", err)
					}
				}
			}
			if relevant(event) {
				w.onChange([]string{event.Name})
			} else if directoryEvent(event) {
				// Directory creation/removal changes package discovery; force a
				// full rebuild by reporting the directory path.
				w.onChange([]string{filepath.Join(event.Name, "go.mod")})
			}
		case err, ok := <-w.w.Errors:
			if !ok {
				return
			}
			w.log.Warn("watch error", "err", err)
		case <-w.done:
			return
		}
	}
}

func (w *Watcher) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if path != w.root && ignoredDir(entry.Name()) {
			return filepath.SkipDir
		}
		if err := w.w.Add(path); err != nil {
			return err
		}
		return nil
	})
}

// Close stops watching and waits for the event loop.
func (w *Watcher) Close() error {
	w.closeOnce.Do(func() {
		close(w.done)
		w.closeErr = w.w.Close()
		w.wg.Wait()
	})
	return w.closeErr
}

func relevant(event fsnotify.Event) bool {
	if ignoredName(filepath.Base(event.Name)) {
		return false
	}
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
		return false
	}
	base := filepath.Base(event.Name)
	return base == "go.mod" || base == "go.sum" || strings.HasSuffix(base, ".go")
}

func ignoredDir(name string) bool {
	return name == "vendor" || name == "node_modules" || name == ".git" || strings.HasPrefix(name, ".")
}

func directoryEvent(event fsnotify.Event) bool {
	if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
		return false
	}
	base := filepath.Base(event.Name)
	if ignoredName(base) || ignoredDir(base) {
		return false
	}
	if info, err := os.Stat(event.Name); err == nil {
		return info.IsDir()
	}
	return filepath.Ext(base) == ""
}

func ignoredName(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
		return true
	}
	switch filepath.Ext(name) {
	case ".swp", ".tmp", ".orig", ".rej":
		return true
	}
	return strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#")
}
