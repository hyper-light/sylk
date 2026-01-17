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

type SignalDispatcherConfig struct {
	BaseDir   string
	SessionID string
}

type SignalDispatcher struct {
	config    SignalDispatcherConfig
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

func NewSignalDispatcher(cfg SignalDispatcherConfig) (*SignalDispatcher, error) {
	cfg = normalizeDispatcherConfig(cfg)

	sessionDir := filepath.Join(cfg.BaseDir, cfg.SessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create signal directory: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	return &SignalDispatcher{
		config:    cfg,
		sessionID: cfg.SessionID,
		signalDir: sessionDir,
		watcher:   watcher,
		handlers:  make(map[SignalType][]SignalHandler),
		stopChan:  make(chan struct{}),
		doneChan:  make(chan struct{}),
	}, nil
}

func normalizeDispatcherConfig(cfg SignalDispatcherConfig) SignalDispatcherConfig {
	if cfg.BaseDir == "" {
		cfg.BaseDir = filepath.Join(os.Getenv("HOME"), DefaultSignalBaseDir)
	}
	return cfg
}

func (d *SignalDispatcher) RegisterHandler(signalType SignalType, handler SignalHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.handlers[signalType] = append(d.handlers[signalType], handler)
}

func (d *SignalDispatcher) Watch(ctx context.Context) error {
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

func (d *SignalDispatcher) watchLoop(ctx context.Context) {
	defer close(d.doneChan)

	done := d.mergeStopChannels(ctx)
	for {
		if d.processNextEvent(done) {
			return
		}
	}
}

func (d *SignalDispatcher) mergeStopChannels(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-d.stopChan:
		}
		close(done)
	}()
	return done
}

func (d *SignalDispatcher) processNextEvent(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	case event, ok := <-d.watcher.Events:
		return d.onWatcherEvent(event, ok)
	case <-d.watcher.Errors:
		return false
	}
}

func (d *SignalDispatcher) onWatcherEvent(event fsnotify.Event, ok bool) bool {
	if !ok {
		return true
	}
	d.handleWatchEvent(event)
	return false
}

func (d *SignalDispatcher) handleWatchEvent(event fsnotify.Event) {
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

func (d *SignalDispatcher) processSignalFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	os.Remove(path)

	var signal CrossSessionSignal
	if err := json.Unmarshal(data, &signal); err != nil {
		return
	}

	d.dispatchSignal(signal)
}

func (d *SignalDispatcher) dispatchSignal(signal CrossSessionSignal) {
	d.mu.RLock()
	handlers := d.handlers[signal.Type]
	d.mu.RUnlock()

	for _, handler := range handlers {
		handler(signal)
	}
}

func (d *SignalDispatcher) SendSignal(signal CrossSessionSignal) error {
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

func (d *SignalDispatcher) sendTargeted(signal CrossSessionSignal) error {
	targetDir := filepath.Join(d.config.BaseDir, signal.ToSession)
	return d.writeSignalFile(targetDir, signal)
}

func (d *SignalDispatcher) sendBroadcast(signal CrossSessionSignal) error {
	entries, err := os.ReadDir(d.config.BaseDir)
	if err != nil {
		return fmt.Errorf("failed to read signal base dir: %w", err)
	}

	for _, entry := range entries {
		d.sendToEntryIfValid(entry, signal)
	}

	return nil
}

func (d *SignalDispatcher) sendToEntryIfValid(entry os.DirEntry, signal CrossSessionSignal) {
	if !entry.IsDir() || entry.Name() == d.sessionID {
		return
	}
	targetDir := filepath.Join(d.config.BaseDir, entry.Name())
	d.writeSignalFile(targetDir, signal)
}

func (d *SignalDispatcher) writeSignalFile(targetDir string, signal CrossSessionSignal) error {
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

func (d *SignalDispatcher) generateSignalFilename(signal CrossSessionSignal) string {
	return fmt.Sprintf("%s-%d%s", signal.Type, signal.Timestamp.UnixNano(), signalFileSuffix)
}

func (d *SignalDispatcher) Close() error {
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

func (d *SignalDispatcher) cleanupSignalDir() {
	os.RemoveAll(d.signalDir)
}

func (d *SignalDispatcher) SessionID() string {
	return d.sessionID
}

func (d *SignalDispatcher) SignalDir() string {
	return d.signalDir
}
