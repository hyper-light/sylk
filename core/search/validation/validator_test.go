// Package validation provides cross-source validation for the Sylk Document Search System.
package validation

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search/cmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock Types
// =============================================================================

// mockCMTReader implements CMTReader for testing.
type mockCMTReader struct {
	files map[string]*cmt.FileInfo
	mu    sync.RWMutex
}

func newMockCMTReader() *mockCMTReader {
	return &mockCMTReader{
		files: make(map[string]*cmt.FileInfo),
	}
}

func (m *mockCMTReader) AddFile(path string, info *cmt.FileInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = info
}

func (m *mockCMTReader) Get(key string) *cmt.FileInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.files[key]
}

func (m *mockCMTReader) Walk(fn func(key string, info *cmt.FileInfo) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k, v := range m.files {
		if !fn(k, v) {
			return
		}
	}
}

// mockGitSource implements GitSource for testing.
type mockGitSource struct {
	available bool
	paths     []string
	states    map[string]*FileState
	mu        sync.RWMutex
}

func newMockGitSource(available bool) *mockGitSource {
	return &mockGitSource{
		available: available,
		paths:     []string{},
		states:    make(map[string]*FileState),
	}
}

func (m *mockGitSource) SetAvailable(available bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.available = available
}

func (m *mockGitSource) AddPath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paths = append(m.paths, path)
}

func (m *mockGitSource) AddState(path string, state *FileState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[path] = state
}

func (m *mockGitSource) IsAvailable() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.available
}

func (m *mockGitSource) GetTrackedPaths() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]string, len(m.paths))
	copy(result, m.paths)
	return result, nil
}

func (m *mockGitSource) GetFileState(path string) *FileState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.states[path]
}

// =============================================================================
// Test Helpers
// =============================================================================

func setupTestDir(t *testing.T) (string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "validation-test-*")
	require.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return tmpDir, cleanup
}

func createTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)

	return name
}

func createTestHash(content string) cmt.Hash {
	var h cmt.Hash
	// Simple mock hash based on content length
	h[0] = byte(len(content) % 256)
	return h
}

// =============================================================================
// NewCrossValidator Tests
// =============================================================================

func TestNewCrossValidator_ValidConfig(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		Config:   DefaultValidationConfig(),
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	assert.NotNil(t, validator)
	defer validator.Close()
}

func TestNewCrossValidator_EmptyRepoPath(t *testing.T) {
	config := CrossValidatorConfig{
		RepoPath: "",
		Config:   DefaultValidationConfig(),
	}

	_, err := NewCrossValidator(config)
	assert.Error(t, err)
	assert.Equal(t, ErrRepoPathEmpty, err)
}

func TestNewCrossValidator_DefaultMaxConcurrent(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		Config: ValidationConfig{
			MaxConcurrent: 0, // Should default to 4
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	assert.NotNil(t, validator)
	defer validator.Close()
}

func TestNewCrossValidator_WithCMT(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	mockCMT := newMockCMTReader()

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Config:   DefaultValidationConfig(),
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	assert.NotNil(t, validator)
	defer validator.Close()
}

func TestNewCrossValidator_WithGit(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	mockGit := newMockGitSource(true)

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		Git:      mockGit,
		Config:   DefaultValidationConfig(),
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	assert.NotNil(t, validator)
	defer validator.Close()
}

// =============================================================================
// Validate Tests - Happy Path
// =============================================================================

func TestValidate_NoDiscrepancies(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create a test file
	content := "test content"
	createTestFile(t, tmpDir, "file.txt", content)

	// Create matching CMT entry
	mockCMT := newMockCMTReader()
	info, err := os.Stat(filepath.Join(tmpDir, "file.txt"))
	require.NoError(t, err)

	mockCMT.AddFile("file.txt", &cmt.FileInfo{
		Path:        "file.txt",
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		ContentHash: createTestHash(content),
	})

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Config: ValidationConfig{
			Paths:                  []string{"file.txt"},
			IncludeMissing:         true,
			IncludeContentMismatch: false, // Disable to avoid hash comparison issues
			IncludeSizeMismatch:    true,
			MaxConcurrent:          4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	assert.NotNil(t, report)
	assert.Equal(t, 1, report.TotalFiles)
	assert.False(t, report.HasDiscrepancies())
}

func TestValidate_EmptyPaths(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		Config: ValidationConfig{
			Paths:         []string{},
			MaxConcurrent: 4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	assert.NotNil(t, report)
	assert.Equal(t, 0, report.TotalFiles)
	assert.False(t, report.HasDiscrepancies())
}

// =============================================================================
// Validate Tests - Discrepancy Detection
// =============================================================================

func TestValidate_DetectsMissingFile(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create CMT entry for file that doesn't exist on filesystem
	mockCMT := newMockCMTReader()
	mockCMT.AddFile("missing.txt", &cmt.FileInfo{
		Path:        "missing.txt",
		Size:        100,
		ModTime:     time.Now(),
		ContentHash: createTestHash("content"),
	})

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Config: ValidationConfig{
			Paths:          []string{"missing.txt"},
			IncludeMissing: true,
			MaxConcurrent:  4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	assert.True(t, report.HasDiscrepancies())
	assert.Equal(t, 1, report.DiscrepancyCount())

	missing := report.GetDiscrepanciesByType(DiscrepancyMissing)
	assert.Len(t, missing, 1)
	assert.Equal(t, "missing.txt", missing[0].Path)
}

func TestValidate_DetectsSizeMismatch(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create test file
	content := "short"
	createTestFile(t, tmpDir, "file.txt", content)

	// Create CMT entry with different size but same content hash
	// This isolates the size mismatch detection
	mockCMT := newMockCMTReader()
	mockCMT.AddFile("file.txt", &cmt.FileInfo{
		Path:        "file.txt",
		Size:        1000, // Different from actual (5 bytes)
		ModTime:     time.Now(),
		ContentHash: cmt.Hash{}, // Empty hash won't trigger content mismatch
	})

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Config: ValidationConfig{
			Paths:                  []string{"file.txt"},
			IncludeMissing:         false,
			IncludeContentMismatch: false, // Disabled so content mismatch is ignored
			IncludeSizeMismatch:    true,
			MaxConcurrent:          4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	assert.True(t, report.HasDiscrepancies())

	size := report.GetDiscrepanciesByType(DiscrepancySizeMismatch)
	assert.Len(t, size, 1)
}

func TestValidate_SkipsDisabledDiscrepancyTypes(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create CMT entry for missing file
	mockCMT := newMockCMTReader()
	mockCMT.AddFile("missing.txt", &cmt.FileInfo{
		Path:        "missing.txt",
		Size:        100,
		ModTime:     time.Now(),
		ContentHash: createTestHash("content"),
	})

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Config: ValidationConfig{
			Paths:          []string{"missing.txt"},
			IncludeMissing: false, // Disabled
			MaxConcurrent:  4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	// Should not report missing discrepancy because it's disabled
	assert.False(t, report.HasDiscrepancies())
}

// =============================================================================
// Validate Tests - Context Cancellation
// =============================================================================

func TestValidate_ContextCancellation(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create many files to process
	mockCMT := newMockCMTReader()
	for i := 0; i < 100; i++ {
		path := filepath.Join("file", string(rune('0'+i%10))+".txt")
		mockCMT.AddFile(path, &cmt.FileInfo{
			Path:        path,
			Size:        100,
			ModTime:     time.Now(),
			ContentHash: createTestHash("content"),
		})
	}

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Config: ValidationConfig{
			IncludeMissing: true,
			MaxConcurrent:  1, // Single worker to make cancellation more predictable
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	report, err := validator.Validate(ctx)
	// Should still return a report, possibly incomplete
	assert.NoError(t, err)
	assert.NotNil(t, report)
}

// =============================================================================
// Validate Tests - Parallel Validation
// =============================================================================

func TestValidate_ParallelValidation(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create multiple test files
	for i := 0; i < 10; i++ {
		name := string(rune('a'+i)) + ".txt"
		createTestFile(t, tmpDir, name, "content"+name)
	}

	// Create matching CMT entries
	mockCMT := newMockCMTReader()
	for i := 0; i < 10; i++ {
		name := string(rune('a'+i)) + ".txt"
		info, _ := os.Stat(filepath.Join(tmpDir, name))
		mockCMT.AddFile(name, &cmt.FileInfo{
			Path:        name,
			Size:        info.Size(),
			ModTime:     info.ModTime(),
			ContentHash: createTestHash("content" + name),
		})
	}

	paths := make([]string, 10)
	for i := 0; i < 10; i++ {
		paths[i] = string(rune('a'+i)) + ".txt"
	}

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Config: ValidationConfig{
			Paths:               paths,
			IncludeSizeMismatch: true,
			MaxConcurrent:       4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	assert.Equal(t, 10, report.TotalFiles)
	assert.False(t, report.HasDiscrepancies())
}

// =============================================================================
// Validate Tests - Three Sources
// =============================================================================

func TestValidate_ThreeSourceValidation(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create test file
	content := "test content"
	createTestFile(t, tmpDir, "file.txt", content)
	info, err := os.Stat(filepath.Join(tmpDir, "file.txt"))
	require.NoError(t, err)

	// Create matching CMT entry
	mockCMT := newMockCMTReader()
	mockCMT.AddFile("file.txt", &cmt.FileInfo{
		Path:        "file.txt",
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		ContentHash: createTestHash(content),
	})

	// Create matching Git entry
	mockGit := newMockGitSource(true)
	mockGit.AddPath("file.txt")
	mockGit.AddState("file.txt", &FileState{
		Path:        "file.txt",
		Source:      SourceGit,
		Exists:      true,
		Size:        info.Size(),
		ContentHash: createTestHash(content).String(),
	})

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Git:      mockGit,
		Config: ValidationConfig{
			Paths:               []string{"file.txt"},
			IncludeMissing:      true,
			IncludeSizeMismatch: true,
			MaxConcurrent:       4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	// All three sources should be checked
	assert.Contains(t, report.SourcesChecked, SourceFilesystem)
	assert.Contains(t, report.SourcesChecked, SourceCMT)
	assert.Contains(t, report.SourcesChecked, SourceGit)
}

// =============================================================================
// Validate Tests - Path Collection
// =============================================================================

func TestValidate_CollectsPathsFromCMT(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create files that exist in CMT
	createTestFile(t, tmpDir, "cmt1.txt", "content1")
	createTestFile(t, tmpDir, "cmt2.txt", "content2")

	mockCMT := newMockCMTReader()
	mockCMT.AddFile("cmt1.txt", &cmt.FileInfo{Path: "cmt1.txt", Size: 8, ModTime: time.Now()})
	mockCMT.AddFile("cmt2.txt", &cmt.FileInfo{Path: "cmt2.txt", Size: 8, ModTime: time.Now()})

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Config: ValidationConfig{
			// No explicit paths - should collect from CMT
			IncludeSizeMismatch: true,
			MaxConcurrent:       4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	assert.Equal(t, 2, report.TotalFiles)
}

func TestValidate_CollectsPathsFromGit(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create files that exist in Git
	createTestFile(t, tmpDir, "git1.txt", "content1")
	createTestFile(t, tmpDir, "git2.txt", "content2")

	mockGit := newMockGitSource(true)
	mockGit.AddPath("git1.txt")
	mockGit.AddPath("git2.txt")

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		Git:      mockGit,
		Config: ValidationConfig{
			// No explicit paths - should collect from Git
			MaxConcurrent: 4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	assert.Equal(t, 2, report.TotalFiles)
}

func TestValidate_MergesPathsFromAllSources(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create files
	createTestFile(t, tmpDir, "both.txt", "content")
	createTestFile(t, tmpDir, "cmt_only.txt", "content")
	createTestFile(t, tmpDir, "git_only.txt", "content")

	mockCMT := newMockCMTReader()
	mockCMT.AddFile("both.txt", &cmt.FileInfo{Path: "both.txt", Size: 7, ModTime: time.Now()})
	mockCMT.AddFile("cmt_only.txt", &cmt.FileInfo{Path: "cmt_only.txt", Size: 7, ModTime: time.Now()})

	mockGit := newMockGitSource(true)
	mockGit.AddPath("both.txt")
	mockGit.AddPath("git_only.txt")

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Git:      mockGit,
		Config: ValidationConfig{
			// No explicit paths - should merge from both sources
			MaxConcurrent: 4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	// Should have 3 unique paths
	assert.Equal(t, 3, report.TotalFiles)
}

// =============================================================================
// Close Tests
// =============================================================================

func TestClose_Success(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		Config:   DefaultValidationConfig(),
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)

	err = validator.Close()
	assert.NoError(t, err)
}

func TestClose_DoubleClose(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		Config:   DefaultValidationConfig(),
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)

	err = validator.Close()
	assert.NoError(t, err)

	err = validator.Close()
	assert.NoError(t, err)
}

func TestValidate_AfterClose(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		Config:   DefaultValidationConfig(),
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)

	validator.Close()

	ctx := context.Background()
	_, err = validator.Validate(ctx)
	assert.Error(t, err)
	assert.Equal(t, ErrValidatorClosed, err)
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestValidate_ConcurrentAccess(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	createTestFile(t, tmpDir, "file.txt", "content")

	mockCMT := newMockCMTReader()
	mockCMT.AddFile("file.txt", &cmt.FileInfo{
		Path:    "file.txt",
		Size:    7,
		ModTime: time.Now(),
	})

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		CMT:      mockCMT,
		Config: ValidationConfig{
			Paths:         []string{"file.txt"},
			MaxConcurrent: 4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := validator.Validate(ctx)
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent validation error: %v", err)
	}
}

// =============================================================================
// Report Structure Tests
// =============================================================================

func TestValidate_ReportStructure(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	createTestFile(t, tmpDir, "file.txt", "content")

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		Config: ValidationConfig{
			Paths:         []string{"file.txt"},
			MaxConcurrent: 4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	beforeValidation := time.Now()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	// Check report fields
	assert.False(t, report.CheckedAt.IsZero())
	assert.True(t, report.CheckedAt.After(beforeValidation) || report.CheckedAt.Equal(beforeValidation))
	assert.Equal(t, 1, report.TotalFiles)
	assert.True(t, report.Duration > 0)
	assert.Contains(t, report.SourcesChecked, SourceFilesystem)
	assert.Equal(t, []string{"file.txt"}, report.PathsChecked)
}

// =============================================================================
// Git Source Unavailable Tests
// =============================================================================

func TestValidate_GitUnavailable(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	createTestFile(t, tmpDir, "file.txt", "content")

	mockGit := newMockGitSource(false) // Unavailable

	config := CrossValidatorConfig{
		RepoPath: tmpDir,
		Git:      mockGit,
		Config: ValidationConfig{
			Paths:         []string{"file.txt"},
			MaxConcurrent: 4,
		},
	}

	validator, err := NewCrossValidator(config)
	require.NoError(t, err)
	defer validator.Close()

	ctx := context.Background()
	report, err := validator.Validate(ctx)
	require.NoError(t, err)

	// Git should not be in sources checked
	assert.NotContains(t, report.SourcesChecked, SourceGit)
	assert.Contains(t, report.SourcesChecked, SourceFilesystem)
}

// =============================================================================
// Utility Function Tests
// =============================================================================

func TestMapKeysToSlice(t *testing.T) {
	t.Parallel()

	m := map[string]struct{}{
		"a": {},
		"b": {},
		"c": {},
	}

	result := mapKeysToSlice(m)
	assert.Len(t, result, 3)
	assert.Contains(t, result, "a")
	assert.Contains(t, result, "b")
	assert.Contains(t, result, "c")
}

func TestMapKeysToSlice_Empty(t *testing.T) {
	t.Parallel()

	m := map[string]struct{}{}
	result := mapKeysToSlice(m)
	assert.Len(t, result, 0)
}

func TestExtractSources(t *testing.T) {
	t.Parallel()

	states := []*FileState{
		{Source: SourceFilesystem},
		{Source: SourceCMT},
		{Source: SourceGit},
	}

	sources := extractSources(states)
	assert.Len(t, sources, 3)
	assert.Equal(t, SourceFilesystem, sources[0])
	assert.Equal(t, SourceCMT, sources[1])
	assert.Equal(t, SourceGit, sources[2])
}

func TestCollectDiscrepancies(t *testing.T) {
	t.Parallel()

	ch := make(chan *Discrepancy, 3)
	ch <- &Discrepancy{Path: "a.txt"}
	ch <- &Discrepancy{Path: "b.txt"}
	ch <- &Discrepancy{Path: "c.txt"}
	close(ch)

	result := collectDiscrepancies(ch)
	assert.Len(t, result, 3)
}

// =============================================================================
// Discrepancy Detection Function Tests
// =============================================================================

func TestHasMissingDiscrepancy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		states   []*FileState
		expected bool
	}{
		{
			name: "all exist returns false",
			states: []*FileState{
				{Exists: true},
				{Exists: true},
			},
			expected: false,
		},
		{
			name: "all missing returns false",
			states: []*FileState{
				{Exists: false},
				{Exists: false},
			},
			expected: false,
		},
		{
			name: "mixed returns true",
			states: []*FileState{
				{Exists: true},
				{Exists: false},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hasMissingDiscrepancy(tt.states)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestHasContentMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		states   []*FileState
		expected bool
	}{
		{
			name: "same hash returns false",
			states: []*FileState{
				{Exists: true, ContentHash: "abc"},
				{Exists: true, ContentHash: "abc"},
			},
			expected: false,
		},
		{
			name: "different hash returns true",
			states: []*FileState{
				{Exists: true, ContentHash: "abc"},
				{Exists: true, ContentHash: "def"},
			},
			expected: true,
		},
		{
			name: "non-existent skipped",
			states: []*FileState{
				{Exists: true, ContentHash: "abc"},
				{Exists: false, ContentHash: "def"},
			},
			expected: false,
		},
		{
			name: "empty hash skipped",
			states: []*FileState{
				{Exists: true, ContentHash: "abc"},
				{Exists: true, ContentHash: ""},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hasContentMismatch(tt.states)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestHasSizeMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		states   []*FileState
		expected bool
	}{
		{
			name: "same size returns false",
			states: []*FileState{
				{Exists: true, Size: 100},
				{Exists: true, Size: 100},
			},
			expected: false,
		},
		{
			name: "different size returns true",
			states: []*FileState{
				{Exists: true, Size: 100},
				{Exists: true, Size: 200},
			},
			expected: true,
		},
		{
			name: "non-existent skipped",
			states: []*FileState{
				{Exists: true, Size: 100},
				{Exists: false, Size: 200},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hasSizeMismatch(tt.states)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestHasModTimeMismatch(t *testing.T) {
	t.Parallel()

	now := time.Now()
	later := now.Add(time.Hour)

	tests := []struct {
		name     string
		states   []*FileState
		expected bool
	}{
		{
			name: "same time returns false",
			states: []*FileState{
				{Exists: true, ModTime: now},
				{Exists: true, ModTime: now},
			},
			expected: false,
		},
		{
			name: "different time returns true",
			states: []*FileState{
				{Exists: true, ModTime: now},
				{Exists: true, ModTime: later},
			},
			expected: true,
		},
		{
			name: "zero time skipped",
			states: []*FileState{
				{Exists: true, ModTime: now},
				{Exists: true, ModTime: time.Time{}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hasModTimeMismatch(tt.states)
			assert.Equal(t, tt.expected, got)
		})
	}
}
