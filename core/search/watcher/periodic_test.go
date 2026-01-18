package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// testManifest is a simple in-memory manifest for testing.
type testManifest struct {
	mu       sync.RWMutex
	files    map[string]string // path -> checksum
	modTimes map[string]time.Time
}

func newTestManifest() *testManifest {
	return &testManifest{
		files:    make(map[string]string),
		modTimes: make(map[string]time.Time),
	}
}

func (m *testManifest) GetChecksum(path string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	checksum, ok := m.files[path]
	return checksum, ok
}

func (m *testManifest) SetChecksum(path, checksum string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = checksum
}

func (m *testManifest) GetModTime(path string) (time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.modTimes[path]
	return t, ok
}

func (m *testManifest) SetModTime(path string, t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modTimes[path] = t
}

func (m *testManifest) ListPaths() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	paths := make([]string, 0, len(m.files))
	for p := range m.files {
		paths = append(paths, p)
	}
	return paths
}

// setupTestDir creates a temp directory with test files.
func setupTestDir(t *testing.T, fileCount int) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "periodic_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	for i := 0; i < fileCount; i++ {
		name := filepath.Join(dir, "file"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(name, []byte("content"+string(rune('0'+i))), 0644); err != nil {
			os.RemoveAll(dir)
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	return dir, func() { os.RemoveAll(dir) }
}

// =============================================================================
// PeriodicScanConfig Tests
// =============================================================================

func TestPeriodicScanConfig_Defaults(t *testing.T) {
	config := PeriodicScanConfig{}

	if config.Interval != 0 {
		t.Errorf("expected zero interval, got %v", config.Interval)
	}
	if config.SampleRate != 0 {
		t.Errorf("expected zero sample rate, got %v", config.SampleRate)
	}
}

func TestPeriodicScanConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		config  PeriodicScanConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: PeriodicScanConfig{
				Interval:   time.Second,
				SampleRate: 0.5,
				RootPath:   "/tmp",
			},
			wantErr: false,
		},
		{
			name: "sample rate too low",
			config: PeriodicScanConfig{
				Interval:   time.Second,
				SampleRate: -0.1,
				RootPath:   "/tmp",
			},
			wantErr: true,
		},
		{
			name: "sample rate too high",
			config: PeriodicScanConfig{
				Interval:   time.Second,
				SampleRate: 1.5,
				RootPath:   "/tmp",
			},
			wantErr: true,
		},
		{
			name: "empty root path",
			config: PeriodicScanConfig{
				Interval:   time.Second,
				SampleRate: 0.5,
				RootPath:   "",
			},
			wantErr: true,
		},
		{
			name: "zero interval",
			config: PeriodicScanConfig{
				Interval:   0,
				SampleRate: 0.5,
				RootPath:   "/tmp",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// =============================================================================
// PeriodicScanner Creation Tests
// =============================================================================

func TestNewPeriodicScanner(t *testing.T) {
	manifest := newTestManifest()

	config := PeriodicScanConfig{
		Interval:   5 * time.Minute,
		SampleRate: 0.1,
		RootPath:   "/tmp",
	}

	scanner := NewPeriodicScanner(config, manifest)

	if scanner == nil {
		t.Fatal("expected non-nil scanner")
	}
	if scanner.config.Interval != config.Interval {
		t.Errorf("interval = %v, want %v", scanner.config.Interval, config.Interval)
	}
	if scanner.config.SampleRate != config.SampleRate {
		t.Errorf("sample rate = %v, want %v", scanner.config.SampleRate, config.SampleRate)
	}
}

func TestNewPeriodicScanner_NilManifest(t *testing.T) {
	config := PeriodicScanConfig{
		Interval:   time.Second,
		SampleRate: 0.5,
		RootPath:   "/tmp",
	}

	scanner := NewPeriodicScanner(config, nil)
	if scanner == nil {
		t.Fatal("expected non-nil scanner even with nil manifest")
	}
}

// =============================================================================
// Periodic Scanner Start/Stop Tests
// =============================================================================

func TestPeriodicScanner_Start_InvalidConfig(t *testing.T) {
	manifest := newTestManifest()
	config := PeriodicScanConfig{
		Interval:   0, // Invalid
		SampleRate: 0.5,
		RootPath:   "/tmp",
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx := context.Background()

	_, err := scanner.Start(ctx)
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

func TestPeriodicScanner_Start_ContextCancellation(t *testing.T) {
	dir, cleanup := setupTestDir(t, 5)
	defer cleanup()

	manifest := newTestManifest()
	config := PeriodicScanConfig{
		Interval:   50 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithCancel(context.Background())

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Cancel context quickly
	cancel()

	// Drain channel to ensure it closes
	drained := false
	timeout := time.After(500 * time.Millisecond)
	for !drained {
		select {
		case _, ok := <-changes:
			if !ok {
				drained = true
			}
		case <-timeout:
			t.Fatal("channel did not close after context cancellation")
		}
	}
}

func TestPeriodicScanner_RunsAtInterval(t *testing.T) {
	dir, cleanup := setupTestDir(t, 3)
	defer cleanup()

	// Add files to manifest with stale checksums
	manifest := newTestManifest()
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		manifest.SetChecksum(filepath.Join(dir, f.Name()), "stale-checksum")
	}

	config := PeriodicScanConfig{
		Interval:   30 * time.Millisecond,
		SampleRate: 1.0, // Check all files
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Count change events
	var changeCount int
	for range changes {
		changeCount++
	}

	// With 30ms interval and 200ms timeout, expect at least 2 scan cycles
	// Each cycle should find 3 changed files (due to stale checksums)
	if changeCount < 3 {
		t.Errorf("expected at least 3 changes, got %d", changeCount)
	}
}

// =============================================================================
// Sample Rate Tests
// =============================================================================

func TestPeriodicScanner_SampleRate_LimitsFilesChecked(t *testing.T) {
	dir, cleanup := setupTestDir(t, 10)
	defer cleanup()

	manifest := newTestManifest()
	// Mark all files as stale
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		manifest.SetChecksum(filepath.Join(dir, f.Name()), "stale")
	}

	config := PeriodicScanConfig{
		Interval:   100 * time.Millisecond, // Long interval to ensure only 1 scan
		SampleRate: 0.3,                    // Only check 30% of files
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	// Short timeout to catch only the initial scan
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var changeCount int
	for range changes {
		changeCount++
	}

	// With 10 files and 0.3 sample rate, should check ~3 files in initial scan
	// Allow some variance (1-5 files)
	if changeCount < 1 || changeCount > 5 {
		t.Errorf("sample rate not working: got %d changes, expected 1-5", changeCount)
	}
}

func TestPeriodicScanner_SampleRate_FullCoverage(t *testing.T) {
	dir, cleanup := setupTestDir(t, 5)
	defer cleanup()

	manifest := newTestManifest()
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		manifest.SetChecksum(filepath.Join(dir, f.Name()), "stale")
	}

	config := PeriodicScanConfig{
		Interval:   100 * time.Millisecond, // Long interval to ensure only 1 scan
		SampleRate: 1.0,                    // Check all files
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	// Short timeout to catch only the initial scan
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var changeCount int
	for range changes {
		changeCount++
	}

	// With 100% sample rate, should find all 5 files in initial scan
	if changeCount != 5 {
		t.Errorf("expected 5 changes with 100%% sample rate, got %d", changeCount)
	}
}

// =============================================================================
// Pause/Resume Tests
// =============================================================================

func TestPeriodicScanner_Pause_StopsScanning(t *testing.T) {
	dir, cleanup := setupTestDir(t, 5)
	defer cleanup()

	manifest := newTestManifest()
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		manifest.SetChecksum(filepath.Join(dir, f.Name()), "stale")
	}

	config := PeriodicScanConfig{
		Interval:   20 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Let one scan complete
	time.Sleep(30 * time.Millisecond)

	// Pause
	scanner.Pause()

	// Count changes during pause
	var countDuringPause int
	pauseTimeout := time.After(60 * time.Millisecond)
pauseLoop:
	for {
		select {
		case _, ok := <-changes:
			if !ok {
				break pauseLoop
			}
			countDuringPause++
		case <-pauseTimeout:
			break pauseLoop
		}
	}

	// During 60ms pause with 20ms interval, should see 0 or very few new changes
	// (some may trickle in from just-started scans)
	if countDuringPause > 10 {
		t.Errorf("too many changes during pause: %d", countDuringPause)
	}
}

func TestPeriodicScanner_Resume_ContinuesScanning(t *testing.T) {
	dir, cleanup := setupTestDir(t, 3)
	defer cleanup()

	manifest := newTestManifest()
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		manifest.SetChecksum(filepath.Join(dir, f.Name()), "stale")
	}

	config := PeriodicScanConfig{
		Interval:   20 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Pause immediately
	scanner.Pause()
	time.Sleep(50 * time.Millisecond)

	// Resume
	scanner.Resume()

	// Count changes after resume
	var countAfterResume int
	resumeTimeout := time.After(100 * time.Millisecond)
resumeLoop:
	for {
		select {
		case _, ok := <-changes:
			if !ok {
				break resumeLoop
			}
			countAfterResume++
		case <-resumeTimeout:
			break resumeLoop
		}
	}

	// After resume with 20ms interval and 100ms window, expect at least 3 changes
	if countAfterResume < 3 {
		t.Errorf("expected at least 3 changes after resume, got %d", countAfterResume)
	}
}

func TestPeriodicScanner_PauseResume_MultipleTimes(t *testing.T) {
	dir, cleanup := setupTestDir(t, 2)
	defer cleanup()

	manifest := newTestManifest()
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		manifest.SetChecksum(filepath.Join(dir, f.Name()), "stale")
	}

	config := PeriodicScanConfig{
		Interval:   15 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Pause/Resume cycle multiple times
	for i := 0; i < 3; i++ {
		scanner.Pause()
		time.Sleep(30 * time.Millisecond)
		scanner.Resume()
		time.Sleep(30 * time.Millisecond)
	}

	// Drain and count
	var count int
	for range changes {
		count++
	}

	// Should still have received some changes
	if count < 2 {
		t.Errorf("expected at least 2 changes through pause/resume cycles, got %d", count)
	}
}

// =============================================================================
// Change Detection Tests
// =============================================================================

func TestPeriodicScanner_DetectsNewFiles(t *testing.T) {
	dir, cleanup := setupTestDir(t, 2)
	defer cleanup()

	manifest := newTestManifest()
	// Only add one file to manifest - the other is "new"
	files, _ := os.ReadDir(dir)
	if len(files) > 0 {
		manifest.SetChecksum(filepath.Join(dir, files[0].Name()), "checksum1")
	}

	config := PeriodicScanConfig{
		Interval:   30 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should detect at least one "new" file
	var foundNew bool
	for range changes {
		foundNew = true
	}

	if !foundNew {
		t.Error("expected to detect new file not in manifest")
	}
}

func TestPeriodicScanner_DetectsModifiedFiles(t *testing.T) {
	dir, cleanup := setupTestDir(t, 1)
	defer cleanup()

	manifest := newTestManifest()
	files, _ := os.ReadDir(dir)
	filePath := filepath.Join(dir, files[0].Name())

	// Store old checksum
	manifest.SetChecksum(filePath, "old-checksum-that-doesnt-match")

	config := PeriodicScanConfig{
		Interval:   30 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should detect the modified file
	var foundModified bool
	for range changes {
		foundModified = true
	}

	if !foundModified {
		t.Error("expected to detect modified file")
	}
}

func TestPeriodicScanner_IgnoresUnchangedFiles(t *testing.T) {
	dir, cleanup := setupTestDir(t, 1)
	defer cleanup()

	files, _ := os.ReadDir(dir)
	filePath := filepath.Join(dir, files[0].Name())

	// Compute the actual SHA-256 checksum that the scanner will compute
	actualChecksum, err := ComputeChecksum(filePath)
	if err != nil {
		t.Fatalf("failed to compute checksum: %v", err)
	}

	manifest := newTestManifest()
	// Store correct checksum (simulating no change)
	manifest.SetChecksum(filePath, actualChecksum)

	config := PeriodicScanConfig{
		Interval:   30 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var changeCount int
	for range changes {
		changeCount++
	}

	if changeCount > 0 {
		t.Errorf("expected 0 changes for unchanged file, got %d", changeCount)
	}
}

// Note: Tests use the ComputeChecksum function from checksum.go which produces
// SHA-256 hashes. The testManifest implements ChecksumStore interface.

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestPeriodicScanner_ConcurrentPauseResume(t *testing.T) {
	dir, cleanup := setupTestDir(t, 5)
	defer cleanup()

	manifest := newTestManifest()
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		manifest.SetChecksum(filepath.Join(dir, f.Name()), "stale")
	}

	config := PeriodicScanConfig{
		Interval:   10 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Concurrent pause/resume from multiple goroutines
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				scanner.Pause()
				time.Sleep(time.Millisecond)
				scanner.Resume()
			}
		}()
	}

	// Drain channel
	go func() {
		for range changes {
		}
	}()

	wg.Wait()
}

func TestPeriodicScanner_RaceCondition(t *testing.T) {
	dir, cleanup := setupTestDir(t, 3)
	defer cleanup()

	manifest := newTestManifest()
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		manifest.SetChecksum(filepath.Join(dir, f.Name()), "stale")
	}

	config := PeriodicScanConfig{
		Interval:   5 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithCancel(context.Background())

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var wg sync.WaitGroup
	var ops atomic.Int64

	// Reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range changes {
			ops.Add(1)
		}
	}()

	// Pause/Resume
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if i%2 == 0 {
				scanner.Pause()
			} else {
				scanner.Resume()
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Run for a bit then cancel
	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()
}

// =============================================================================
// IsPaused Tests
// =============================================================================

func TestPeriodicScanner_IsPaused(t *testing.T) {
	config := PeriodicScanConfig{
		Interval:   time.Minute,
		SampleRate: 0.5,
		RootPath:   "/tmp",
	}

	scanner := NewPeriodicScanner(config, newTestManifest())

	if scanner.IsPaused() {
		t.Error("expected not paused initially")
	}

	scanner.Pause()
	if !scanner.IsPaused() {
		t.Error("expected paused after Pause()")
	}

	scanner.Resume()
	if scanner.IsPaused() {
		t.Error("expected not paused after Resume()")
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestPeriodicScanner_EmptyDirectory(t *testing.T) {
	dir, err := os.MkdirTemp("", "empty_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	manifest := newTestManifest()
	config := PeriodicScanConfig{
		Interval:   30 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	changes, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var changeCount int
	for range changes {
		changeCount++
	}

	if changeCount != 0 {
		t.Errorf("expected 0 changes for empty directory, got %d", changeCount)
	}
}

func TestPeriodicScanner_NonExistentDirectory(t *testing.T) {
	manifest := newTestManifest()
	config := PeriodicScanConfig{
		Interval:   time.Second,
		SampleRate: 0.5,
		RootPath:   "/nonexistent/path/that/should/not/exist",
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx := context.Background()

	_, err := scanner.Start(ctx)
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

func TestPeriodicScanner_MultipleStartCalls(t *testing.T) {
	dir, cleanup := setupTestDir(t, 2)
	defer cleanup()

	manifest := newTestManifest()
	config := PeriodicScanConfig{
		Interval:   50 * time.Millisecond,
		SampleRate: 1.0,
		RootPath:   dir,
	}

	scanner := NewPeriodicScanner(config, manifest)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// First start
	changes1, err := scanner.Start(ctx)
	if err != nil {
		t.Fatalf("First Start() error = %v", err)
	}

	// Second start should return error (already running)
	_, err = scanner.Start(ctx)
	if err == nil {
		t.Error("expected error on second Start() call")
	}

	// Drain first channel
	for range changes1 {
	}
}
