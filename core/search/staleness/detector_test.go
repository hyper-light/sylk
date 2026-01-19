// Package staleness provides staleness detection for the Sylk Document Search System.
package staleness

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Mock Implementations
// =============================================================================

// mockCMTHashProvider implements CMTHashProvider for testing.
type mockCMTHashProvider struct {
	hash Hash
	size int64
	mu   sync.RWMutex
}

func newMockCMTHashProvider(hash Hash, size int64) *mockCMTHashProvider {
	return &mockCMTHashProvider{hash: hash, size: size}
}

func (m *mockCMTHashProvider) RootHash() Hash {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hash
}

func (m *mockCMTHashProvider) Size() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size
}

func (m *mockCMTHashProvider) SetHash(hash Hash) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hash = hash
}

// mockGitProvider implements GitProvider for testing.
type mockGitProvider struct {
	head             string
	isRepo           bool
	uncommittedFiles []string
	headErr          error
	mu               sync.RWMutex
}

func newMockGitProvider(head string, isRepo bool) *mockGitProvider {
	return &mockGitProvider{head: head, isRepo: isRepo}
}

func (m *mockGitProvider) GetHead() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.headErr != nil {
		return "", m.headErr
	}
	return m.head, nil
}

func (m *mockGitProvider) IsGitRepo() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isRepo
}

func (m *mockGitProvider) GetUncommittedFiles() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.uncommittedFiles, nil
}

func (m *mockGitProvider) SetHead(head string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.head = head
}

// mockContentHashProvider implements ContentHashProvider for testing.
type mockContentHashProvider struct {
	hashes map[string][]byte
	files  []string
	mu     sync.RWMutex
}

func newMockContentHashProvider() *mockContentHashProvider {
	return &mockContentHashProvider{
		hashes: make(map[string][]byte),
		files:  []string{},
	}
}

func (m *mockContentHashProvider) GetStoredHash(path string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	hash, ok := m.hashes[path]
	return hash, ok
}

func (m *mockContentHashProvider) ListFiles() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.files
}

func (m *mockContentHashProvider) AddFile(path string, hash []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hashes[path] = hash
	m.files = append(m.files, path)
}

// detectorTestFileInfoProvider implements FileInfoProvider for testing.
type detectorTestFileInfoProvider struct {
	files    []string
	modTimes map[string]time.Time
	mu       sync.RWMutex
}

func newDetectorTestFileInfoProvider() *detectorTestFileInfoProvider {
	return &detectorTestFileInfoProvider{
		files:    []string{},
		modTimes: make(map[string]time.Time),
	}
}

func (m *detectorTestFileInfoProvider) ListFiles() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.files
}

func (m *detectorTestFileInfoProvider) GetModTime(path string) (time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mtime, ok := m.modTimes[path]
	return mtime, ok
}

func (m *detectorTestFileInfoProvider) AddFile(path string, mtime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files = append(m.files, path)
	m.modTimes[path] = mtime
}

// =============================================================================
// HybridStalenessDetector Tests
// =============================================================================

func TestNewHybridStalenessDetector(t *testing.T) {
	t.Parallel()

	t.Run("valid config creates detector", func(t *testing.T) {
		t.Parallel()
		config := DefaultConfig()
		detector, err := NewHybridStalenessDetector(config)
		if err != nil {
			t.Fatalf("NewHybridStalenessDetector() error = %v", err)
		}
		if detector == nil {
			t.Fatal("NewHybridStalenessDetector() returned nil")
		}
		defer detector.Close()
	})

	t.Run("invalid config returns error", func(t *testing.T) {
		t.Parallel()
		config := DetectorConfig{} // All strategies disabled
		_, err := NewHybridStalenessDetector(config)
		if err != ErrNoStrategiesAvailable {
			t.Errorf("NewHybridStalenessDetector() error = %v, want ErrNoStrategiesAvailable", err)
		}
	})
}

func TestHybridStalenessDetector_SetCMT(t *testing.T) {
	t.Parallel()

	t.Run("sets CMT provider successfully", func(t *testing.T) {
		t.Parallel()
		detector, _ := NewHybridStalenessDetector(DefaultConfig())
		defer detector.Close()

		cmt := newMockCMTHashProvider(Hash{1, 2, 3}, 100)
		err := detector.SetCMT(cmt)
		if err != nil {
			t.Errorf("SetCMT() error = %v", err)
		}
	})

	t.Run("nil CMT clears provider", func(t *testing.T) {
		t.Parallel()
		detector, _ := NewHybridStalenessDetector(DefaultConfig())
		defer detector.Close()

		err := detector.SetCMT(nil)
		if err != nil {
			t.Errorf("SetCMT(nil) error = %v", err)
		}
	})

	t.Run("closed detector returns error", func(t *testing.T) {
		t.Parallel()
		detector, _ := NewHybridStalenessDetector(DefaultConfig())
		detector.Close()

		cmt := newMockCMTHashProvider(Hash{1, 2, 3}, 100)
		err := detector.SetCMT(cmt)
		if err != ErrDetectorClosed {
			t.Errorf("SetCMT() on closed detector error = %v, want ErrDetectorClosed", err)
		}
	})
}

func TestHybridStalenessDetector_Detect_Fresh(t *testing.T) {
	t.Parallel()

	t.Run("returns fresh when CMT hash unchanged", func(t *testing.T) {
		t.Parallel()

		config := DefaultConfig()
		config.EnableGit = false
		config.EnableMtime = false
		config.EnableContentHash = false

		detector, _ := NewHybridStalenessDetector(config)
		defer detector.Close()

		cmt := newMockCMTHashProvider(Hash{1, 2, 3}, 100)
		detector.SetCMT(cmt)

		report, err := detector.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		if !report.IsFresh() {
			t.Errorf("Detect() Level = %v, want Fresh", report.Level)
		}
		if report.Confidence < 0.9 {
			t.Errorf("Detect() Confidence = %f, want >= 0.9", report.Confidence)
		}
	})

	t.Run("returns fresh when git HEAD unchanged", func(t *testing.T) {
		t.Parallel()

		config := DefaultConfig()
		config.EnableCMT = false
		config.EnableMtime = false
		config.EnableContentHash = false

		detector, _ := NewHybridStalenessDetector(config)
		defer detector.Close()

		git := newMockGitProvider("abc123", true)
		detector.SetGit(git)

		report, err := detector.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		if !report.IsFresh() {
			t.Errorf("Detect() Level = %v, want Fresh", report.Level)
		}
	})
}

func TestHybridStalenessDetector_Detect_Stale(t *testing.T) {
	t.Parallel()

	t.Run("detects staleness when CMT hash changed", func(t *testing.T) {
		t.Parallel()

		config := DefaultConfig()
		config.EnableGit = false
		config.EnableMtime = false
		config.EnableContentHash = false

		detector, _ := NewHybridStalenessDetector(config)
		defer detector.Close()

		cmt := newMockCMTHashProvider(Hash{1, 2, 3}, 100)
		detector.SetCMT(cmt)

		// First detection establishes baseline
		detector.Detect(context.Background())

		// Change the hash
		cmt.SetHash(Hash{4, 5, 6})

		report, err := detector.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		if report.IsFresh() {
			t.Error("Detect() should detect staleness after hash change")
		}
	})

	t.Run("detects staleness when git HEAD changed", func(t *testing.T) {
		t.Parallel()

		config := DefaultConfig()
		config.EnableCMT = false
		config.EnableMtime = false
		config.EnableContentHash = false

		detector, _ := NewHybridStalenessDetector(config)
		defer detector.Close()

		git := newMockGitProvider("abc123", true)
		detector.SetGit(git)

		// First detection establishes baseline
		detector.Detect(context.Background())

		// Change the HEAD
		git.SetHead("def456")

		report, err := detector.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		if report.IsFresh() {
			t.Error("Detect() should detect staleness after HEAD change")
		}
		if report.Level != ModeratelyStale {
			t.Errorf("Detect() Level = %v, want ModeratelyStale", report.Level)
		}
	})
}

func TestHybridStalenessDetector_Detect_EarlyExit(t *testing.T) {
	t.Parallel()

	t.Run("exits early on freshness when enabled", func(t *testing.T) {
		t.Parallel()

		config := DefaultConfig()
		config.EarlyExitOnFresh = true

		detector, _ := NewHybridStalenessDetector(config)
		defer detector.Close()

		cmt := newMockCMTHashProvider(Hash{1, 2, 3}, 100)
		detector.SetCMT(cmt)

		report, err := detector.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		if !report.IsFresh() {
			t.Errorf("Detect() Level = %v, want Fresh", report.Level)
		}
		// Should only have CMT result due to early exit
		if len(report.Results) > 1 {
			t.Errorf("Expected early exit with 1 result, got %d", len(report.Results))
		}
	})

	t.Run("continues checking when early exit disabled", func(t *testing.T) {
		t.Parallel()

		config := DefaultConfig()
		config.EarlyExitOnFresh = false
		config.EnableContentHash = false // Simplify test

		detector, _ := NewHybridStalenessDetector(config)
		defer detector.Close()

		cmt := newMockCMTHashProvider(Hash{1, 2, 3}, 100)
		detector.SetCMT(cmt)

		git := newMockGitProvider("abc123", true)
		detector.SetGit(git)

		report, err := detector.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		// Should have multiple results when not exiting early
		if len(report.Results) < 2 {
			t.Errorf("Expected multiple results without early exit, got %d", len(report.Results))
		}
	})
}

func TestHybridStalenessDetector_Detect_ContextCanceled(t *testing.T) {
	t.Parallel()

	detector, _ := NewHybridStalenessDetector(DefaultConfig())
	defer detector.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := detector.Detect(ctx)
	if err != ErrContextCanceled {
		t.Errorf("Detect() with canceled context error = %v, want ErrContextCanceled", err)
	}
}

func TestHybridStalenessDetector_Detect_ClosedDetector(t *testing.T) {
	t.Parallel()

	detector, _ := NewHybridStalenessDetector(DefaultConfig())
	detector.Close()

	_, err := detector.Detect(context.Background())
	if err != ErrDetectorClosed {
		t.Errorf("Detect() on closed detector error = %v, want ErrDetectorClosed", err)
	}
}

func TestHybridStalenessDetector_Close(t *testing.T) {
	t.Parallel()

	t.Run("close is idempotent", func(t *testing.T) {
		t.Parallel()
		detector, _ := NewHybridStalenessDetector(DefaultConfig())

		err1 := detector.Close()
		err2 := detector.Close()

		if err1 != nil {
			t.Errorf("First Close() error = %v", err1)
		}
		if err2 != nil {
			t.Errorf("Second Close() error = %v", err2)
		}
	})

	t.Run("IsClosed returns true after close", func(t *testing.T) {
		t.Parallel()
		detector, _ := NewHybridStalenessDetector(DefaultConfig())

		if detector.IsClosed() {
			t.Error("IsClosed() before close = true, want false")
		}

		detector.Close()

		if !detector.IsClosed() {
			t.Error("IsClosed() after close = false, want true")
		}
	})
}

func TestHybridStalenessDetector_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	detector, _ := NewHybridStalenessDetector(DefaultConfig())
	defer detector.Close()

	cmt := newMockCMTHashProvider(Hash{1, 2, 3}, 100)
	detector.SetCMT(cmt)

	var wg sync.WaitGroup
	const numGoroutines = 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = detector.Detect(context.Background())
		}()
	}

	wg.Wait()
}

func TestHybridStalenessDetector_UpdateStoredGitHead(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.EnableCMT = false
	config.EnableMtime = false
	config.EnableContentHash = false

	detector, _ := NewHybridStalenessDetector(config)
	defer detector.Close()

	git := newMockGitProvider("abc123", true)
	detector.SetGit(git)

	// First detection
	detector.Detect(context.Background())

	// Update stored head manually
	detector.UpdateStoredGitHead("xyz789")

	// Change git head to match stored
	git.SetHead("xyz789")

	report, _ := detector.Detect(context.Background())
	if !report.IsFresh() {
		t.Error("Should be fresh when HEAD matches stored value")
	}
}

func TestHybridStalenessDetector_UpdateCMTHash(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.EnableGit = false
	config.EnableMtime = false
	config.EnableContentHash = false

	detector, _ := NewHybridStalenessDetector(config)
	defer detector.Close()

	cmt := newMockCMTHashProvider(Hash{1, 2, 3}, 100)
	detector.SetCMT(cmt)

	// First detection
	detector.Detect(context.Background())

	// Change hash
	cmt.SetHash(Hash{4, 5, 6})

	// Update stored hash
	detector.UpdateCMTHash()

	// Should be fresh now
	report, _ := detector.Detect(context.Background())
	if !report.IsFresh() {
		t.Error("Should be fresh after UpdateCMTHash()")
	}
}

func TestHybridStalenessDetector_LastCheckTime(t *testing.T) {
	t.Parallel()

	detector, _ := NewHybridStalenessDetector(DefaultConfig())
	defer detector.Close()

	cmt := newMockCMTHashProvider(Hash{1, 2, 3}, 100)
	detector.SetCMT(cmt)

	if !detector.LastCheckTime().IsZero() {
		t.Error("LastCheckTime() before detection should be zero")
	}

	before := time.Now()
	detector.Detect(context.Background())
	after := time.Now()

	lastCheck := detector.LastCheckTime()
	if lastCheck.Before(before) || lastCheck.After(after) {
		t.Error("LastCheckTime() not within expected range")
	}
}

// =============================================================================
// Utility Function Tests
// =============================================================================

func TestSelectRandomSample(t *testing.T) {
	t.Parallel()

	t.Run("returns all files when size exceeds length", func(t *testing.T) {
		t.Parallel()
		files := []string{"a", "b", "c"}
		sample := selectRandomSample(files, 10)
		if len(sample) != 3 {
			t.Errorf("selectRandomSample() len = %d, want 3", len(sample))
		}
	})

	t.Run("returns requested size when size is smaller", func(t *testing.T) {
		t.Parallel()
		files := []string{"a", "b", "c", "d", "e"}
		sample := selectRandomSample(files, 3)
		if len(sample) != 3 {
			t.Errorf("selectRandomSample() len = %d, want 3", len(sample))
		}
	})

	t.Run("returns empty for empty input", func(t *testing.T) {
		t.Parallel()
		files := []string{}
		sample := selectRandomSample(files, 5)
		if len(sample) != 0 {
			t.Errorf("selectRandomSample() len = %d, want 0", len(sample))
		}
	})
}

func TestComputeFileHash(t *testing.T) {
	t.Parallel()

	t.Run("computes hash for existing file", func(t *testing.T) {
		t.Parallel()

		// Create temp file
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.txt")
		if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		hash, err := computeFileHash(testFile)
		if err != nil {
			t.Fatalf("computeFileHash() error = %v", err)
		}
		if len(hash) != 32 { // SHA-256 produces 32 bytes
			t.Errorf("computeFileHash() hash len = %d, want 32", len(hash))
		}
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		t.Parallel()

		_, err := computeFileHash("/nonexistent/path/file.txt")
		if err == nil {
			t.Error("computeFileHash() should return error for non-existent file")
		}
	})

	t.Run("same content produces same hash", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		content := []byte("identical content")

		file1 := filepath.Join(tmpDir, "file1.txt")
		file2 := filepath.Join(tmpDir, "file2.txt")

		os.WriteFile(file1, content, 0644)
		os.WriteFile(file2, content, 0644)

		hash1, _ := computeFileHash(file1)
		hash2, _ := computeFileHash(file2)

		if !hashesEqual(hash1, hash2) {
			t.Error("Same content should produce same hash")
		}
	})

	t.Run("different content produces different hash", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()

		file1 := filepath.Join(tmpDir, "file1.txt")
		file2 := filepath.Join(tmpDir, "file2.txt")

		os.WriteFile(file1, []byte("content 1"), 0644)
		os.WriteFile(file2, []byte("content 2"), 0644)

		hash1, _ := computeFileHash(file1)
		hash2, _ := computeFileHash(file2)

		if hashesEqual(hash1, hash2) {
			t.Error("Different content should produce different hash")
		}
	})
}

func TestHashesEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a        []byte
		b        []byte
		expected bool
	}{
		{
			name:     "equal hashes",
			a:        []byte{1, 2, 3, 4},
			b:        []byte{1, 2, 3, 4},
			expected: true,
		},
		{
			name:     "different hashes",
			a:        []byte{1, 2, 3, 4},
			b:        []byte{1, 2, 3, 5},
			expected: false,
		},
		{
			name:     "different lengths",
			a:        []byte{1, 2, 3},
			b:        []byte{1, 2, 3, 4},
			expected: false,
		},
		{
			name:     "both empty",
			a:        []byte{},
			b:        []byte{},
			expected: true,
		},
		{
			name:     "one empty",
			a:        []byte{1},
			b:        []byte{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hashesEqual(tt.a, tt.b); got != tt.expected {
				t.Errorf("hashesEqual() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// Strategy Order Tests
// =============================================================================

func TestHybridStalenessDetector_StrategyOrder(t *testing.T) {
	t.Parallel()

	t.Run("strategies are tried in order", func(t *testing.T) {
		t.Parallel()

		config := DefaultConfig()
		config.EarlyExitOnFresh = false
		config.EnableContentHash = false // Simplify

		detector, _ := NewHybridStalenessDetector(config)
		defer detector.Close()

		cmt := newMockCMTHashProvider(Hash{1, 2, 3}, 100)
		detector.SetCMT(cmt)

		git := newMockGitProvider("abc123", true)
		detector.SetGit(git)

		report, _ := detector.Detect(context.Background())

		// Verify order: CMT should come before Git
		if len(report.Results) >= 2 {
			if report.Results[0].Strategy != CMTRoot {
				t.Error("First strategy should be CMTRoot")
			}
			if report.Results[1].Strategy != GitCommit {
				t.Error("Second strategy should be GitCommit")
			}
		}
	})
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestHybridStalenessDetector_NoProvidersConfigured(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	detector, _ := NewHybridStalenessDetector(config)
	defer detector.Close()

	// Don't set any providers
	report, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	// Should return with default values
	if report.Level != Fresh {
		t.Errorf("Detect() Level = %v, want Fresh (default)", report.Level)
	}
}

func TestHybridStalenessDetector_GitNotRepo(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.EnableCMT = false
	config.EnableMtime = false
	config.EnableContentHash = false

	detector, _ := NewHybridStalenessDetector(config)
	defer detector.Close()

	git := newMockGitProvider("abc123", false) // Not a repo
	detector.SetGit(git)

	report, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	// Should skip git and use default
	if len(report.Results) != 0 {
		t.Errorf("Expected no results when git is not a repo, got %d", len(report.Results))
	}
}
