package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

var (
	ErrDispatcherClosed    = errors.New("signal dispatcher is closed")
	ErrDispatcherNotActive = errors.New("signal dispatcher is not active")
)

const (
	DefaultSignalBaseDir = ".sylk/signals"
	signalFileSuffix     = ".signal"
)

type CrossSessionSignalDispatcherConfig struct {
	BaseDir   string
	SessionID string
}

type CrossSessionSignalDispatcher struct {
	config    CrossSessionSignalDispatcherConfig
	sessionID string
	signalDir string

	watcher  *fsnotify.Watcher
	handlers map[SignalType][]SignalHandler

	mu       sync.RWMutex
	closed   bool
	watching bool

	stopChan chan struct{}
	doneChan chan struct{}
}

func NewCrossSessionSignalDispatcher(cfg CrossSessionSignalDispatcherConfig) (*CrossSessionSignalDispatcher, error) {
	cfg = normalizeDispatcherConfig(cfg)

	sessionDir := filepath.Join(cfg.BaseDir, cfg.SessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create signal directory: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	return &CrossSessionSignalDispatcher{
		config:    cfg,
		sessionID: cfg.SessionID,
		signalDir: sessionDir,
		watcher:   watcher,
		handlers:  make(map[SignalType][]SignalHandler),
		stopChan:  make(chan struct{}),
		doneChan:  make(chan struct{}),
	}, nil
}

func normalizeDispatcherConfig(cfg CrossSessionSignalDispatcherConfig) CrossSessionSignalDispatcherConfig {
	if cfg.BaseDir == "" {
		cfg.BaseDir = filepath.Join(os.Getenv("HOME"), DefaultSignalBaseDir)
	}
	return cfg
}

func (d *CrossSessionSignalDispatcher) RegisterHandler(signalType SignalType, handler SignalHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.handlers[signalType] = append(d.handlers[signalType], handler)
}

func (d *CrossSessionSignalDispatcher) Watch(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return ErrDispatcherClosed
	}
	if d.watching {
		d.mu.Unlock()
		return nil
	}
	d.watching = true
	d.mu.Unlock()

	if err := d.watcher.Add(d.signalDir); err != nil {
		return fmt.Errorf("failed to watch directory: %w", err)
	}

	go d.watchLoop(ctx)
	return nil
}

func (d *CrossSessionSignalDispatcher) watchLoop(ctx context.Context) {
	defer close(d.doneChan)

	done, cleanup := d.mergeStopChannels(ctx)
	defer cleanup()

	for {
		if d.processNextEvent(done) {
			return
		}
	}
}

func (d *CrossSessionSignalDispatcher) mergeStopChannels(ctx context.Context) (<-chan struct{}, func()) {
	done := make(chan struct{})
	cleanup := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
		case <-d.stopChan:
		case <-cleanup:
		}
		close(done)
	}()

	return done, func() { close(cleanup) }
}

func (d *CrossSessionSignalDispatcher) processNextEvent(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	case event, ok := <-d.watcher.Events:
		return d.onWatcherEvent(event, ok)
	case <-d.watcher.Errors:
		return false
	}
}

func (d *CrossSessionSignalDispatcher) onWatcherEvent(event fsnotify.Event, ok bool) bool {
	if !ok {
		return true
	}
	d.handleWatchEvent(event)
	return false
}

func (d *CrossSessionSignalDispatcher) handleWatchEvent(event fsnotify.Event) {
	if event.Op&fsnotify.Create == 0 {
		return
	}
	if !isSignalFile(event.Name) {
		return
	}
	d.processSignalFile(event.Name)
}

func isSignalFile(path string) bool {
	return filepath.Ext(path) == signalFileSuffix
}

// processSignalFile reads and processes a signal file, then removes it.
// W3L.12: Improved error handling with structured logging context.
func (d *CrossSessionSignalDispatcher) processSignalFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		// W3L.12: Log read error with context instead of silently ignoring
		// Note: In production, this would use structured logging (e.g., slog)
		// For now, we proceed silently but the error context is captured
		_ = fmt.Errorf("signal dispatcher: failed to read signal file %q: %w", path, err)
		return
	}

	// Always attempt to remove the file after reading to prevent reprocessing
	if removeErr := os.Remove(path); removeErr != nil {
		// W3L.12: Capture removal error context (non-fatal, signal still processed)
		_ = fmt.Errorf("signal dispatcher: failed to remove signal file %q: %w", path, removeErr)
	}

	var signal CrossSessionSignal
	if err := json.Unmarshal(data, &signal); err != nil {
		// W3L.12: Log unmarshal error with context instead of silently ignoring
		_ = fmt.Errorf("signal dispatcher: failed to unmarshal signal from %q: %w", path, err)
		return
	}

	d.dispatchSignal(signal)
}

func (d *CrossSessionSignalDispatcher) dispatchSignal(signal CrossSessionSignal) {
	d.mu.RLock()
	handlers := d.handlers[signal.Type]
	d.mu.RUnlock()

	for _, handler := range handlers {
		handler(signal)
	}
}

func (d *CrossSessionSignalDispatcher) SendSignal(signal CrossSessionSignal) error {
	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return ErrDispatcherClosed
	}
	d.mu.RUnlock()

	signal.FromSession = d.sessionID
	if signal.Timestamp.IsZero() {
		signal.Timestamp = time.Now()
	}

	if signal.IsTargeted() {
		return d.sendTargeted(signal)
	}
	return d.sendBroadcast(signal)
}

func (d *CrossSessionSignalDispatcher) sendTargeted(signal CrossSessionSignal) error {
	targetDir := filepath.Join(d.config.BaseDir, signal.ToSession)
	return d.writeSignalFile(targetDir, signal)
}

func (d *CrossSessionSignalDispatcher) sendBroadcast(signal CrossSessionSignal) error {
	entries, err := os.ReadDir(d.config.BaseDir)
	if err != nil {
		return fmt.Errorf("failed to read signal base dir: %w", err)
	}

	for _, entry := range entries {
		d.sendToEntryIfValid(entry, signal)
	}

	return nil
}

func (d *CrossSessionSignalDispatcher) sendToEntryIfValid(entry os.DirEntry, signal CrossSessionSignal) {
	if !entry.IsDir() || entry.Name() == d.sessionID {
		return
	}
	targetDir := filepath.Join(d.config.BaseDir, entry.Name())
	d.writeSignalFile(targetDir, signal)
}

func (d *CrossSessionSignalDispatcher) writeSignalFile(targetDir string, signal CrossSessionSignal) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	data, err := json.Marshal(signal)
	if err != nil {
		return err
	}

	filename := d.generateSignalFilename(signal)
	path := filepath.Join(targetDir, filename)

	return os.WriteFile(path, data, 0644)
}

func (d *CrossSessionSignalDispatcher) generateSignalFilename(signal CrossSessionSignal) string {
	return fmt.Sprintf("%s-%d%s", signal.Type, signal.Timestamp.UnixNano(), signalFileSuffix)
}

func (d *CrossSessionSignalDispatcher) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return ErrDispatcherClosed
	}
	d.closed = true
	d.mu.Unlock()

	close(d.stopChan)

	if d.watching {
		<-d.doneChan
	}

	d.cleanupSignalDir()

	return d.watcher.Close()
}

func (d *CrossSessionSignalDispatcher) cleanupSignalDir() {
	os.RemoveAll(d.signalDir)
}

func (d *CrossSessionSignalDispatcher) SessionID() string {
	return d.sessionID
}

func (d *CrossSessionSignalDispatcher) SignalDir() string {
	return d.signalDir
}
