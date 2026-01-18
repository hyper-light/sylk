//go:build fsnotify
// +build fsnotify

// Package watcher provides file system monitoring for the Sylk Document Search System.
package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers (prefixed with fs to avoid collision with other test files)
// =============================================================================

// fsTestDir creates a temporary directory for fsnotify testing.
func fsTestDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// fsCreateFile creates a file with the given content for fsnotify tests.
func fsCreateFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)

	parentDir := filepath.Dir(path)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatalf("failed to create parent directory: %v", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create file %s: %v", path, err)
	}
	return path
}

// fsCreateDir creates a subdirectory for fsnotify tests.
func fsCreateDir(t *testing.T, parent, name string) string {
	t.Helper()
	path := filepath.Join(parent, name)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("failed to create directory %s: %v", path, err)
	}
	return path
}

// fsCollectEvents collects events from a channel with a timeout.
func fsCollectEvents(ch <-chan *FileEvent, timeout time.Duration) []*FileEvent {
	var events []*FileEvent
	deadline := time.After(timeout)

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, event)
		case <-deadline:
			return events
		}
	}
}

// fsWaitForEvent waits for a specific event with timeout.
func fsWaitForEvent(t *testing.T, ch <-chan *FileEvent, timeout time.Duration) *FileEvent {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(timeout):
		t.Fatal("timeout waiting for event")
		return nil
	}
}

// =============================================================================
// WatchConfig Tests
// =============================================================================

func TestDefaultWatchConfig(t *testing.T) {
	t.Parallel()

	config := DefaultWatchConfig("/some/path")

	if len(config.Paths) != 1 {
		t.Errorf("expected 1 path, got %d", len(config.Paths))
	}

	if config.Paths[0] != "/some/path" {
		t.Errorf("Paths[0] = %q, want %q", config.Paths[0], "/some/path")
	}

	if config.Debounce != 100*time.Millisecond {
		t.Errorf("Debounce = %v, want %v", config.Debounce, 100*time.Millisecond)
	}

	if len(config.ExcludePatterns) != 0 {
		t.Error("expected empty ExcludePatterns by default")
	}
}

func TestWatchConfig_EmptyPaths(t *testing.T) {
	t.Parallel()

	config := WatchConfig{
		Paths:    []string{},
		Debounce: 100 * time.Millisecond,
	}

	_, err := NewFSWatcher(config)
	if err != ErrNoPathsConfigured {
		t.Errorf("NewFSWatcher() error = %v, want %v", err, ErrNoPathsConfigured)
	}
}

// =============================================================================
// NewFSWatcher Tests
// =============================================================================

func TestNewFSWatcher(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	config := WatchConfig{
		Paths:           []string{dir},
		ExcludePatterns: []string{"*.tmp"},
		Debounce:        50 * time.Millisecond,
	}

	watcher, err := NewFSWatcher(config)
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	if watcher == nil {
		t.Fatal("NewFSWatcher returned nil")
	}
}

func TestNewFSWatcher_DefaultDebounce(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	config := WatchConfig{
		Paths: []string{dir},
	}

	watcher, err := NewFSWatcher(config)
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	if watcher.config.Debounce != DefaultDebounce {
		t.Errorf("Debounce = %v, want default %v", watcher.config.Debounce, DefaultDebounce)
	}
}

func TestNewFSWatcher_InvalidExcludePattern(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	config := WatchConfig{
		Paths:           []string{dir},
		ExcludePatterns: []string{"[invalid"},
	}

	_, err := NewFSWatcher(config)
	if err == nil {
		t.Error("expected error for invalid pattern, got nil")
	}
}

func TestNewFSWatcher_NonExistentPath(t *testing.T) {
	t.Parallel()

	config := WatchConfig{
		Paths: []string{"/nonexistent/path/that/does/not/exist"},
	}

	_, err := NewFSWatcher(config)
	if err != ErrPathNotExist {
		t.Errorf("NewFSWatcher() error = %v, want %v", err, ErrPathNotExist)
	}
}

func TestNewFSWatcher_PathIsFile(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)
	filePath := fsCreateFile(t, dir, "file.txt", "content")

	config := WatchConfig{
		Paths: []string{filePath},
	}

	_, err := NewFSWatcher(config)
	if err != ErrPathNotDirectory {
		t.Errorf("NewFSWatcher() error = %v, want %v", err, ErrPathNotDirectory)
	}
}

// =============================================================================
// File Operation Detection Tests
// =============================================================================

func TestFSWatcher_DetectsFileCreate(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Give watcher time to start
	time.Sleep(50 * time.Millisecond)

	// Create a file
	fsCreateFile(t, dir, "new_file.txt", "hello")

	event := fsWaitForEvent(t, eventCh, 500*time.Millisecond)

	if event.Operation != OpCreate {
		t.Errorf("Operation = %v, want %v", event.Operation, OpCreate)
	}

	if filepath.Base(event.Path) != "new_file.txt" {
		t.Errorf("Path = %q, want file named %q", event.Path, "new_file.txt")
	}
}

func TestFSWatcher_DetectsFileWrite(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)
	filePath := fsCreateFile(t, dir, "existing.txt", "initial")

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Write to the file
	if err := os.WriteFile(filePath, []byte("modified"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	event := fsWaitForEvent(t, eventCh, 500*time.Millisecond)

	// fsnotify Write maps to coordinator's OpModify
	if event.Operation != OpModify {
		t.Errorf("Operation = %v, want %v", event.Operation, OpModify)
	}
}

func TestFSWatcher_DetectsFileRemove(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)
	filePath := fsCreateFile(t, dir, "to_delete.txt", "content")

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Remove the file
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("Remove error: %v", err)
	}

	event := fsWaitForEvent(t, eventCh, 500*time.Millisecond)

	// fsnotify Remove maps to coordinator's OpDelete
	if event.Operation != OpDelete {
		t.Errorf("Operation = %v, want %v", event.Operation, OpDelete)
	}
}

func TestFSWatcher_DetectsFileRename(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)
	oldPath := fsCreateFile(t, dir, "old_name.txt", "content")
	newPath := filepath.Join(dir, "new_name.txt")

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Rename the file
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("Rename error: %v", err)
	}

	// Collect events - rename typically generates multiple events
	events := fsCollectEvents(eventCh, 300*time.Millisecond)

	if len(events) == 0 {
		t.Fatal("expected at least one event for rename")
	}

	// At least one event should be rename-related
	hasRenameOrCreate := false
	for _, e := range events {
		if e.Operation == OpRename || e.Operation == OpCreate {
			hasRenameOrCreate = true
			break
		}
	}

	if !hasRenameOrCreate {
		t.Error("expected rename or create event")
	}
}

// =============================================================================
// Debouncing Tests
// =============================================================================

func TestFSWatcher_Debounces_RapidChanges(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)
	filePath := fsCreateFile(t, dir, "debounce_test.txt", "initial")

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Make rapid changes (faster than debounce interval)
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filePath, []byte("change"+string(rune('0'+i))), 0644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}
		time.Sleep(20 * time.Millisecond) // 20ms between writes, less than 100ms debounce
	}

	// Wait for debounce to settle
	events := fsCollectEvents(eventCh, 300*time.Millisecond)

	// Should get fewer events than the number of writes due to debouncing
	// Typically just 1 or 2 events instead of 5
	if len(events) >= 5 {
		t.Errorf("expected fewer than 5 events due to debouncing, got %d", len(events))
	}

	if len(events) == 0 {
		t.Error("expected at least 1 event")
	}
}

func TestFSWatcher_Debounces_SameFile_MultipleOps(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create and write rapidly
	filePath := filepath.Join(dir, "rapid.txt")
	if err := os.WriteFile(filePath, []byte("1"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filePath, []byte("12"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filePath, []byte("123"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	events := fsCollectEvents(eventCh, 300*time.Millisecond)

	// Should be debounced to fewer events
	if len(events) > 3 {
		t.Errorf("expected 3 or fewer events due to debouncing, got %d", len(events))
	}
}

// =============================================================================
// Recursive Watching Tests
// =============================================================================

func TestFSWatcher_WatchesSubdirectories(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)
	subDir := fsCreateDir(t, dir, "subdir")

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create file in subdirectory
	fsCreateFile(t, subDir, "nested.txt", "nested content")

	event := fsWaitForEvent(t, eventCh, 500*time.Millisecond)

	if filepath.Base(event.Path) != "nested.txt" {
		t.Errorf("expected event for nested.txt, got %s", event.Path)
	}
}

func TestFSWatcher_WatchesDeeplyNestedDirectories(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)
	level1 := fsCreateDir(t, dir, "level1")
	level2 := fsCreateDir(t, level1, "level2")
	level3 := fsCreateDir(t, level2, "level3")

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create file in deeply nested directory
	fsCreateFile(t, level3, "deep.txt", "deep content")

	event := fsWaitForEvent(t, eventCh, 500*time.Millisecond)

	if filepath.Base(event.Path) != "deep.txt" {
		t.Errorf("expected event for deep.txt, got %s", event.Path)
	}
}

func TestFSWatcher_NewDirectoryAutoWatched(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create a new subdirectory
	newSubDir := fsCreateDir(t, dir, "new_subdir")

	// Wait for directory creation event
	time.Sleep(100 * time.Millisecond)

	// Create file in the new subdirectory
	fsCreateFile(t, newSubDir, "in_new_dir.txt", "content")

	events := fsCollectEvents(eventCh, 500*time.Millisecond)

	// Should have received event for file in new directory
	foundFileEvent := false
	for _, e := range events {
		if filepath.Base(e.Path) == "in_new_dir.txt" {
			foundFileEvent = true
			break
		}
	}

	if !foundFileEvent {
		t.Error("expected event for file in newly created directory")
	}
}

// =============================================================================
// Exclude Pattern Tests
// =============================================================================

func TestFSWatcher_ExcludesMatchingFiles(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:           []string{dir},
		ExcludePatterns: []string{"*.tmp", "*.log"},
		Debounce:        20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create excluded files
	fsCreateFile(t, dir, "test.tmp", "temp content")
	fsCreateFile(t, dir, "debug.log", "log content")

	// Create non-excluded file
	fsCreateFile(t, dir, "important.txt", "important")

	events := fsCollectEvents(eventCh, 300*time.Millisecond)

	// Should only get event for important.txt
	for _, e := range events {
		name := filepath.Base(e.Path)
		if name == "test.tmp" || name == "debug.log" {
			t.Errorf("received event for excluded file: %s", name)
		}
	}

	foundImportant := false
	for _, e := range events {
		if filepath.Base(e.Path) == "important.txt" {
			foundImportant = true
			break
		}
	}

	if !foundImportant {
		t.Error("expected event for non-excluded file important.txt")
	}
}

func TestFSWatcher_ExcludesDirectoryPattern(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)
	nodeModules := fsCreateDir(t, dir, "node_modules")

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:           []string{dir},
		ExcludePatterns: []string{"node_modules/**"},
		Debounce:        20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create file in node_modules
	fsCreateFile(t, nodeModules, "package.json", "{}")

	// Create file in root
	fsCreateFile(t, dir, "index.js", "// code")

	events := fsCollectEvents(eventCh, 300*time.Millisecond)

	// Should not have event for node_modules file
	for _, e := range events {
		if filepath.Base(e.Path) == "package.json" {
			t.Error("received event for file in excluded directory")
		}
	}
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

func TestFSWatcher_ContextCancellation(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Cancel the context
	cancel()

	// Channel should be closed soon
	select {
	case _, ok := <-eventCh:
		if ok {
			// Might receive final events, wait for close
			for range eventCh {
			}
		}
	case <-time.After(time.Second):
		t.Error("event channel was not closed after context cancellation")
	}
}

func TestFSWatcher_ContextAlreadyCancelled(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should close quickly
	select {
	case <-eventCh:
		// Channel closed or event received (both acceptable)
	case <-time.After(500 * time.Millisecond):
		t.Error("channel should close quickly with cancelled context")
	}
}

// =============================================================================
// Multiple Paths Tests
// =============================================================================

func TestFSWatcher_MultiplePaths(t *testing.T) {
	t.Parallel()

	dir1 := fsTestDir(t)
	dir2 := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir1, dir2},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create files in both directories
	fsCreateFile(t, dir1, "file1.txt", "content1")
	fsCreateFile(t, dir2, "file2.txt", "content2")

	events := fsCollectEvents(eventCh, 500*time.Millisecond)

	foundFile1 := false
	foundFile2 := false
	for _, e := range events {
		name := filepath.Base(e.Path)
		if name == "file1.txt" {
			foundFile1 = true
		}
		if name == "file2.txt" {
			foundFile2 = true
		}
	}

	if !foundFile1 {
		t.Error("expected event for file in dir1")
	}
	if !foundFile2 {
		t.Error("expected event for file in dir2")
	}
}

// =============================================================================
// Stop Tests
// =============================================================================

func TestFSWatcher_Stop(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Stop the watcher
	if err := watcher.Stop(); err != nil {
		t.Errorf("Stop() error = %v", err)
	}

	// Channel should be closed
	select {
	case _, ok := <-eventCh:
		if ok {
			// Drain any remaining events
			for range eventCh {
			}
		}
	case <-time.After(time.Second):
		t.Error("channel was not closed after Stop()")
	}
}

func TestFSWatcher_StopWithoutStart(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}

	// Stop without Start should not panic
	if err := watcher.Stop(); err != nil {
		t.Errorf("Stop() without Start error = %v", err)
	}
}

func TestFSWatcher_DoubleStop(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Double stop should not panic
	_ = watcher.Stop()
	err = watcher.Stop()
	if err != nil {
		t.Errorf("second Stop() error = %v", err)
	}
}

// =============================================================================
// FileEvent Tests
// =============================================================================

func TestFileEvent_Fields(t *testing.T) {
	t.Parallel()

	now := time.Now()
	event := &FileEvent{
		Path:      "/path/to/file.txt",
		Operation: OpCreate,
		Time:      now,
	}

	if event.Path != "/path/to/file.txt" {
		t.Errorf("Path = %q, want %q", event.Path, "/path/to/file.txt")
	}

	if event.Operation != OpCreate {
		t.Errorf("Operation = %v, want %v", event.Operation, OpCreate)
	}

	if !event.Time.Equal(now) {
		t.Errorf("Time = %v, want %v", event.Time, now)
	}
}

// TestFSNotifyFileOperation_String tests the operation string mapping for fsnotify-mapped operations.
// Note: FileOperation is defined in coordinator.go, this tests the mapping.
func TestFSNotifyFileOperation_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		op   FileOperation
		want string
	}{
		{OpCreate, "create"},
		{OpModify, "modify"},
		{OpDelete, "delete"},
		{OpRename, "rename"},
		{FileOperation(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.op.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// =============================================================================
// FSNotify Error Tests
// =============================================================================

func TestFSNotifyErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err     error
		wantMsg string
	}{
		{ErrNoPathsConfigured, "no paths configured for watching"},
		{ErrPathNotExist, "watch path does not exist"},
		{ErrPathNotDirectory, "watch path is not a directory"},
		{ErrInvalidPattern, "invalid exclude pattern"},
	}

	for _, tt := range tests {
		t.Run(tt.wantMsg, func(t *testing.T) {
			t.Parallel()
			if tt.err.Error() != tt.wantMsg {
				t.Errorf("error = %q, want %q", tt.err.Error(), tt.wantMsg)
			}
		})
	}
}

// =============================================================================
// Race Condition Tests
// =============================================================================

func TestFSWatcher_ConcurrentEvents(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create multiple files concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			fsCreateFile(t, dir, "concurrent_"+string(rune('a'+idx))+".txt", "content")
		}(i)
	}

	wg.Wait()

	// Collect events
	events := fsCollectEvents(eventCh, time.Second)

	// Should have received some events (may be debounced)
	if len(events) == 0 {
		t.Error("expected at least some events from concurrent file creation")
	}
}

func TestFSWatcher_ConcurrentStopAndEvents(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	var wg sync.WaitGroup

	// Consumer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range eventCh {
		}
	}()

	// Producer - create files
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			fsCreateFile(t, dir, "race_"+string(rune('a'+i))+".txt", "content")
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Stop watcher while events happening
	time.Sleep(20 * time.Millisecond)
	_ = watcher.Stop()

	// Wait for goroutines - should not deadlock
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for goroutines - possible deadlock")
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestFSWatcher_EmptyDirectoryInitially(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)
	// Don't create any files initially

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Now create a file
	fsCreateFile(t, dir, "first_file.txt", "content")

	event := fsWaitForEvent(t, eventCh, 500*time.Millisecond)

	if filepath.Base(event.Path) != "first_file.txt" {
		t.Errorf("expected event for first_file.txt, got %s", event.Path)
	}
}

func TestFSWatcher_HiddenFiles(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create hidden file
	fsCreateFile(t, dir, ".hidden", "secret")

	event := fsWaitForEvent(t, eventCh, 500*time.Millisecond)

	if filepath.Base(event.Path) != ".hidden" {
		t.Errorf("expected event for .hidden, got %s", event.Path)
	}
}

func TestFSWatcher_SpecialCharactersInFilename(t *testing.T) {
	t.Parallel()

	dir := fsTestDir(t)

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create file with spaces
	fsCreateFile(t, dir, "file with spaces.txt", "content")

	event := fsWaitForEvent(t, eventCh, 500*time.Millisecond)

	if filepath.Base(event.Path) != "file with spaces.txt" {
		t.Errorf("expected event for 'file with spaces.txt', got %s", event.Path)
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkFSWatcher_EventProcessing(b *testing.B) {
	dir := b.TempDir()

	watcher, err := NewFSWatcher(WatchConfig{
		Paths:    []string{dir},
		Debounce: 1 * time.Millisecond, // Minimal debounce for benchmark
	})
	if err != nil {
		b.Fatalf("NewFSWatcher() error = %v", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := watcher.Start(ctx)
	if err != nil {
		b.Fatalf("Start() error = %v", err)
	}

	// Consume events
	go func() {
		for range eventCh {
		}
	}()

	time.Sleep(50 * time.Millisecond)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		path := filepath.Join(dir, "bench_"+string(rune('a'+i%26))+".txt")
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			b.Fatalf("WriteFile error: %v", err)
		}
	}
}
