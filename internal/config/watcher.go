package config

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceDuration = 500 * time.Millisecond

// Watcher watches a config file for changes and calls onChange with the new
// parsed config. Changes are debounced so rapid writes (e.g. atomic saves via
// rename) only trigger one reload.
type Watcher struct {
	path     string
	onChange func(*Config)
	fsw      *fsnotify.Watcher
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewWatcher creates and starts a config file watcher. The onChange callback
// is invoked in a dedicated goroutine each time a valid reload occurs.
// Call Close() to shut down the watcher.
func NewWatcher(path string, onChange func(*Config)) (*Watcher, error) {
	path = filepath.Clean(path)
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("config watcher: create fsnotify watcher: %w", err)
	}

	// Watch the directory so renames (atomic saves) are captured too.
	dir := filepath.Dir(path)
	if err := fsw.Add(dir); err != nil {
		fsw.Close()
		return nil, fmt.Errorf("config watcher: watch %s: %w", dir, err)
	}

	w := &Watcher{
		path:     path,
		onChange: onChange,
		fsw:      fsw,
		stopCh:   make(chan struct{}),
	}

	w.wg.Add(1)
	go w.run()

	slog.Info("config watcher started", "path", path)
	return w, nil
}

func (w *Watcher) run() {
	defer w.wg.Done()
	var debounce *time.Timer

	for {
		select {
		case <-w.stopCh:
			if debounce != nil {
				debounce.Stop()
			}
			return

		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			// Only care about our specific file.
			if filepath.Clean(event.Name) != w.path {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}

			slog.Debug("config file event", "op", event.Op.String(), "path", event.Name)

			// Reset debounce timer.
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(debounceDuration, func() {
				w.reload()
			})

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			slog.Error("config watcher fsnotify error", "err", err)
		}
	}
}

func (w *Watcher) reload() {
	cfg, err := Load(w.path)
	if err != nil {
		slog.Error("config hot-reload failed — keeping previous config", "err", err, "path", w.path)
		return
	}
	slog.Info("config reloaded successfully", "path", w.path)
	w.onChange(cfg)
}

// Close stops the watcher and releases resources. It blocks until the
// background goroutine exits.
func (w *Watcher) Close() error {
	close(w.stopCh)
	w.wg.Wait()
	return w.fsw.Close()
}
