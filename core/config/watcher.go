package config

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/adalundhe/sylk/core/storage"
	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	manager    *Manager
	fsWatcher  *fsnotify.Watcher
	debounceMs int
	stopCh     chan struct{}
	stoppedCh  chan struct{}
	mu         sync.Mutex
	lastReload time.Time
	running    bool
}

func NewWatcher(manager *Manager, debounceMs int) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if debounceMs <= 0 {
		debounceMs = 500
	}

	return &Watcher{
		manager:    manager,
		fsWatcher:  fsWatcher,
		debounceMs: debounceMs,
		stopCh:     make(chan struct{}),
		stoppedCh:  make(chan struct{}),
	}, nil
}

func (w *Watcher) Start() error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return nil
	}
	w.running = true
	w.mu.Unlock()

	if err := w.addConfigPaths(); err != nil {
		return err
	}

	go w.watchLoop()
	go w.signalLoop()

	return nil
}

func (w *Watcher) addConfigPaths() error {
	projectDirs := storage.ResolveProjectDirs(".")

	paths := []string{
		projectDirs.Config,
		w.manager.dirs.ConfigDir("config.yaml"),
	}

	localConfig := projectDirs.Local + "/config.yaml"
	paths = append(paths, localConfig)

	for _, path := range paths {
		dir := dirOf(path)
		if _, err := os.Stat(dir); err == nil {
			_ = w.fsWatcher.Add(dir)
		}
	}

	return nil
}

func (w *Watcher) watchLoop() {
	defer close(w.stoppedCh)

	var debounceTimer *time.Timer
	var timerCh <-chan time.Time

	for {
		select {
		case <-w.stopCh:
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			if isConfigFile(event.Name) && isWriteEvent(event) {
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.NewTimer(time.Duration(w.debounceMs) * time.Millisecond)
				timerCh = debounceTimer.C
			}

		case <-timerCh:
			w.reload()
			timerCh = nil

		case <-w.fsWatcher.Errors:
			continue
		}
	}
}

func (w *Watcher) signalLoop() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)

	for {
		select {
		case <-w.stopCh:
			signal.Stop(sigCh)
			return
		case <-sigCh:
			w.reload()
		}
	}
}

func (w *Watcher) reload() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if time.Since(w.lastReload) < time.Duration(w.debounceMs)*time.Millisecond {
		return
	}

	_ = w.manager.Reload()
	w.lastReload = time.Now()
}

func (w *Watcher) Stop() error {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return nil
	}
	w.running = false
	w.mu.Unlock()

	close(w.stopCh)
	<-w.stoppedCh

	return w.fsWatcher.Close()
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}

func isConfigFile(path string) bool {
	return len(path) > 5 && path[len(path)-5:] == ".yaml"
}

func isWriteEvent(event fsnotify.Event) bool {
	return event.Op&fsnotify.Write == fsnotify.Write ||
		event.Op&fsnotify.Create == fsnotify.Create
}
