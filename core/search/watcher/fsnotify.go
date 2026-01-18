// Package watcher provides file system monitoring for the Sylk Document Search System.
// It wraps fsnotify to provide recursive directory watching with debouncing and
// pattern-based exclusion filtering.
package watcher

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gobwas/glob"
)

// =============================================================================
// Constants
// =============================================================================

// DefaultDebounce is the default debounce interval for file events (100ms).
const DefaultDebounce = 100 * time.Millisecond

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrNoPathsConfigured indicates no watch paths were specified.
	ErrNoPathsConfigured = errors.New("no paths configured for watching")

	// ErrPathNotExist indicates a watch path does not exist.
	ErrPathNotExist = errors.New("watch path does not exist")

	// ErrPathNotDirectory indicates a watch path is not a directory.
	ErrPathNotDirectory = errors.New("watch path is not a directory")

	// ErrInvalidPattern indicates an exclude pattern could not be compiled.
	ErrInvalidPattern = errors.New("invalid exclude pattern")
)

// =============================================================================
// FileEvent
// =============================================================================

// FileEvent represents a file system change event from fsnotify.
// This is the event type emitted by FSWatcher before being converted to ChangeEvent.
type FileEvent struct {
	// Path is the absolute path to the changed file.
	Path string

	// Operation is the type of file system operation.
	Operation FileOperation

	// Time is when the event was detected.
	Time time.Time
}

// =============================================================================
// WatchConfig
// =============================================================================

// WatchConfig configures the file system watcher.
type WatchConfig struct {
	// Paths are the directories to watch recursively.
	Paths []string

	// ExcludePatterns are glob patterns for paths to ignore.
	ExcludePatterns []string

	// Debounce is the interval to wait before emitting events for the same path.
	// Default is 100ms.
	Debounce time.Duration
}

// DefaultWatchConfig returns a configuration with sensible defaults.
func DefaultWatchConfig(rootPath string) WatchConfig {
	return WatchConfig{
		Paths:           []string{rootPath},
		ExcludePatterns: []string{},
		Debounce:        DefaultDebounce,
	}
}

// =============================================================================
// pendingEvent tracks debounced events
// =============================================================================

type pendingEvent struct {
	event *FileEvent
	timer *time.Timer
}

// =============================================================================
// FSWatcher
// =============================================================================

// FSWatcher monitors file system changes using fsnotify.
type FSWatcher struct {
	config   WatchConfig
	watcher  *fsnotify.Watcher
	excludes []glob.Glob

	mu       sync.Mutex
	pending  map[string]*pendingEvent
	eventCh  chan *FileEvent
	stopOnce sync.Once
	stopped  bool
}

// NewFSWatcher creates a new file system watcher.
// Returns an error if paths are invalid or patterns cannot be compiled.
func NewFSWatcher(config WatchConfig) (*FSWatcher, error) {
	if err := validateFSWatchConfig(&config); err != nil {
		return nil, err
	}

	excludes, err := compileExcludePatterns(config.ExcludePatterns)
	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &FSWatcher{
		config:   config,
		watcher:  watcher,
		excludes: excludes,
		pending:  make(map[string]*pendingEvent),
	}, nil
}

// =============================================================================
// Validation
// =============================================================================

// validateFSWatchConfig checks that the configuration is valid.
func validateFSWatchConfig(config *WatchConfig) error {
	if err := validatePaths(config.Paths); err != nil {
		return err
	}
	applyDebounceDefault(config)
	return nil
}

// validatePaths validates that at least one path exists and all are directories.
func validatePaths(paths []string) error {
	if len(paths) == 0 {
		return ErrNoPathsConfigured
	}
	for _, path := range paths {
		if err := validateFSWatchPath(path); err != nil {
			return err
		}
	}
	return nil
}

// applyDebounceDefault sets the default debounce if not specified.
func applyDebounceDefault(config *WatchConfig) {
	if config.Debounce <= 0 {
		config.Debounce = DefaultDebounce
	}
}

// validateFSWatchPath checks that a path exists and is a directory.
func validateFSWatchPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return classifyStatError(err)
	}
	return validateIsDirectory(info)
}

// classifyStatError converts stat errors to appropriate watcher errors.
func classifyStatError(err error) error {
	if os.IsNotExist(err) {
		return ErrPathNotExist
	}
	return err
}

// validateIsDirectory checks that the path is a directory.
func validateIsDirectory(info os.FileInfo) error {
	if !info.IsDir() {
		return ErrPathNotDirectory
	}
	return nil
}

// compileExcludePatterns compiles glob patterns for exclusion matching.
func compileExcludePatterns(patterns []string) ([]glob.Glob, error) {
	excludes := make([]glob.Glob, 0, len(patterns))

	for _, pattern := range patterns {
		g, err := glob.Compile(pattern, '/')
		if err != nil {
			return nil, errors.Join(ErrInvalidPattern, err)
		}
		excludes = append(excludes, g)
	}

	return excludes, nil
}

// =============================================================================
// Start
// =============================================================================

// Start begins watching for file changes.
// Returns a channel of FileEvents that is closed when the context is cancelled
// or Stop is called.
func (w *FSWatcher) Start(ctx context.Context) (<-chan *FileEvent, error) {
	w.eventCh = make(chan *FileEvent)

	if err := w.addWatchPaths(); err != nil {
		close(w.eventCh)
		return nil, err
	}

	go w.processEvents(ctx)

	return w.eventCh, nil
}

// addWatchPaths adds all configured paths and their subdirectories to the watcher.
func (w *FSWatcher) addWatchPaths() error {
	for _, path := range w.config.Paths {
		if err := w.addDirectoryRecursive(path); err != nil {
			return err
		}
	}
	return nil
}

// addDirectoryRecursive adds a directory and all its subdirectories to the watcher.
func (w *FSWatcher) addDirectoryRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip paths with errors
		}

		if !d.IsDir() {
			return nil // Only watch directories
		}

		if w.isExcluded(path) {
			return filepath.SkipDir
		}

		return w.watcher.Add(path)
	})
}

// =============================================================================
// Event Processing
// =============================================================================

// processEvents reads from fsnotify and emits debounced events.
func (w *FSWatcher) processEvents(ctx context.Context) {
	defer w.cleanup()

	for {
		if shouldStop := w.processOnce(ctx); shouldStop {
			return
		}
	}
}

// processOnce processes one iteration of the event loop.
// Returns true if the loop should stop.
func (w *FSWatcher) processOnce(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	case event, ok := <-w.watcher.Events:
		return w.handleEventOrStop(event, ok)
	case _, ok := <-w.watcher.Errors:
		return !ok // Stop if channel closed
	}
}

// handleEventOrStop processes an event and returns true if we should stop.
func (w *FSWatcher) handleEventOrStop(event fsnotify.Event, ok bool) bool {
	if !ok {
		return true
	}
	w.handleFSEvent(event)
	return false
}

// handleFSEvent processes a single fsnotify event.
func (w *FSWatcher) handleFSEvent(event fsnotify.Event) {
	if w.isExcluded(event.Name) {
		return
	}

	// Handle new directory creation for recursive watching
	if event.Has(fsnotify.Create) {
		w.handlePossibleNewDirectory(event.Name)
	}

	op := mapFSNotifyOperation(event.Op)
	w.scheduleEvent(event.Name, op)
}

// handlePossibleNewDirectory adds a new directory to the watcher if applicable.
func (w *FSWatcher) handlePossibleNewDirectory(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}

	if info.IsDir() {
		_ = w.addDirectoryRecursive(path)
	}
}

// fsOpMappings defines the mapping from fsnotify operations to FileOperation.
// Order matters: first match wins.
var fsOpMappings = []struct {
	fsOp    fsnotify.Op
	fileOp  FileOperation
}{
	{fsnotify.Create, OpCreate},
	{fsnotify.Write, OpModify},
	{fsnotify.Remove, OpDelete},
	{fsnotify.Rename, OpRename},
	{fsnotify.Chmod, OpModify},
}

// mapFSNotifyOperation converts fsnotify.Op to FileOperation.
func mapFSNotifyOperation(op fsnotify.Op) FileOperation {
	for _, m := range fsOpMappings {
		if op.Has(m.fsOp) {
			return m.fileOp
		}
	}
	return OpModify
}

// =============================================================================
// Debouncing
// =============================================================================

// scheduleEvent schedules an event emission after the debounce interval.
func (w *FSWatcher) scheduleEvent(path string, op FileOperation) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return
	}

	event := &FileEvent{
		Path:      path,
		Operation: op,
		Time:      time.Now(),
	}

	if existing, ok := w.pending[path]; ok {
		existing.timer.Stop()
		existing.event = event
		existing.timer = w.createDebounceTimer(path, event)
		return
	}

	w.pending[path] = &pendingEvent{
		event: event,
		timer: w.createDebounceTimer(path, event),
	}
}

// createDebounceTimer creates a timer that emits the event after debounce interval.
func (w *FSWatcher) createDebounceTimer(path string, event *FileEvent) *time.Timer {
	return time.AfterFunc(w.config.Debounce, func() {
		w.emitEvent(path, event)
	})
}

// emitEvent sends an event to the output channel and removes from pending.
func (w *FSWatcher) emitEvent(path string, event *FileEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return
	}

	delete(w.pending, path)

	select {
	case w.eventCh <- event:
	default:
		// Channel full or closed, drop event
	}
}

// =============================================================================
// Exclusion
// =============================================================================

// isExcluded checks if a path matches any exclusion pattern.
func (w *FSWatcher) isExcluded(path string) bool {
	for _, pattern := range w.excludes {
		if matchesPattern(path, pattern) {
			return true
		}
	}
	return false
}

// matchesPattern checks if a path matches a single pattern.
func matchesPattern(path string, pattern glob.Glob) bool {
	// Check against full path or base name
	if pattern.Match(path) || pattern.Match(filepath.Base(path)) {
		return true
	}
	// Check path suffixes for directory patterns
	return matchesPathSuffix(path, pattern)
}

// matchesPathSuffix checks if any suffix of the path matches the pattern.
func matchesPathSuffix(path string, pattern glob.Glob) bool {
	parts := splitPath(path)
	for i := range parts {
		if pattern.Match(filepath.Join(parts[i:]...)) {
			return true
		}
	}
	return false
}

// splitPath splits a path into its components.
func splitPath(path string) []string {
	var parts []string
	for !isPathTerminal(path) {
		dir, file := filepath.Split(path)
		if file != "" {
			parts = append([]string{file}, parts...)
		}
		path = filepath.Clean(dir)
	}
	return parts
}

// isPathTerminal checks if a path is at the root or is empty/current dir.
func isPathTerminal(path string) bool {
	return path == "" || path == "/" || path == "."
}

// =============================================================================
// Stop
// =============================================================================

// Stop stops the watcher and closes the event channel.
// Safe to call multiple times.
func (w *FSWatcher) Stop() error {
	w.stopOnce.Do(func() {
		w.mu.Lock()
		w.stopped = true

		// Cancel all pending timers
		for _, p := range w.pending {
			p.timer.Stop()
		}
		w.pending = make(map[string]*pendingEvent)
		w.mu.Unlock()

		// Close the underlying watcher
		w.watcher.Close()
	})

	return nil
}

// cleanup closes the event channel when processing stops.
func (w *FSWatcher) cleanup() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.stopped {
		w.stopped = true
		for _, p := range w.pending {
			p.timer.Stop()
		}
		w.pending = make(map[string]*pendingEvent)
	}

	close(w.eventCh)
}
