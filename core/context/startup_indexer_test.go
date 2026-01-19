package context

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/resources"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// Test Helpers
// =============================================================================

func setupTestIndexer(t *testing.T) (*StartupIndexer, *UniversalContentStore, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "startup_indexer_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := vectorgraphdb.Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to open vectorgraphdb: %v", err)
	}

	cfg := ContentStoreConfig{
		BlevePath:      filepath.Join(tmpDir, "documents.bleve"),
		VectorDB:       db,
		IndexQueueSize: 100,
		IndexWorkers:   2,
	}

	store, err := NewUniversalContentStore(cfg)
	if err != nil {
		db.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create content store: %v", err)
	}

	pressureLevel := &atomic.Int32{}
	goroutineBudget := concurrency.NewGoroutineBudget(pressureLevel)
	fileHandleBudget := resources.NewFileHandleBudget(resources.DefaultFileHandleBudgetConfig())

	indexer, err := NewStartupIndexer(StartupIndexerConfig{
		ContentStore:     store,
		GoroutineBudget:  goroutineBudget,
		FileHandleBudget: fileHandleBudget,
		AgentID:          "test-indexer",
	})
	if err != nil {
		store.Close()
		db.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create startup indexer: %v", err)
	}

	cleanup := func() {
		store.Close()
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return indexer, store, cleanup
}

func createTestDirectory(t *testing.T) (string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "test_project")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Create directory structure
	dirs := []string{
		"cmd/app",
		"internal/service",
		"pkg/util",
		"vendor/lib",
		".git/objects",
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, dir), 0755); err != nil {
			os.RemoveAll(tmpDir)
			t.Fatalf("failed to create dir %s: %v", dir, err)
		}
	}

	// Create test files
	files := map[string]string{
		"cmd/app/main.go":           "package main\n\nfunc main() {}\n",
		"internal/service/svc.go":   "package service\n\ntype Service struct{}\n",
		"pkg/util/helpers.go":       "package util\n\nfunc Helper() {}\n",
		"README.md":                 "# Test Project\n",
		".env":                      "SECRET_KEY=abc123",
		"vendor/lib/lib.go":         "package lib\n",
		".git/objects/test":         "git object",
		"large_file.txt":            "",
		"test.min.js":               "minified js",
	}

	for path, content := range files {
		fullPath := filepath.Join(tmpDir, path)
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			os.RemoveAll(tmpDir)
			t.Fatalf("failed to write file %s: %v", path, err)
		}
	}

	// Create a large file that exceeds MaxFileSize
	largeFile := filepath.Join(tmpDir, "large_file.txt")
	largeContent := make([]byte, 2*1024*1024) // 2MB
	if err := os.WriteFile(largeFile, largeContent, 0644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create large file: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return tmpDir, cleanup
}

// =============================================================================
// NewStartupIndexer Tests
// =============================================================================

func TestNewStartupIndexer_HappyPath(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	if indexer == nil {
		t.Fatal("expected non-nil indexer")
	}
}

func TestNewStartupIndexer_NilContentStore(t *testing.T) {
	pressureLevel := &atomic.Int32{}
	goroutineBudget := concurrency.NewGoroutineBudget(pressureLevel)
	fileHandleBudget := resources.NewFileHandleBudget(resources.DefaultFileHandleBudgetConfig())

	_, err := NewStartupIndexer(StartupIndexerConfig{
		ContentStore:     nil,
		GoroutineBudget:  goroutineBudget,
		FileHandleBudget: fileHandleBudget,
	})

	if err != ErrNilContentStore {
		t.Errorf("expected ErrNilContentStore, got: %v", err)
	}
}

func TestNewStartupIndexer_NilGoroutineBudget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := vectorgraphdb.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := NewUniversalContentStore(ContentStoreConfig{
		BlevePath: filepath.Join(tmpDir, "test.bleve"),
		VectorDB:  db,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	fileHandleBudget := resources.NewFileHandleBudget(resources.DefaultFileHandleBudgetConfig())

	_, err = NewStartupIndexer(StartupIndexerConfig{
		ContentStore:     store,
		GoroutineBudget:  nil,
		FileHandleBudget: fileHandleBudget,
	})

	if err != ErrNilGoroutineBudget {
		t.Errorf("expected ErrNilGoroutineBudget, got: %v", err)
	}
}

func TestNewStartupIndexer_NilFileHandleBudget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := vectorgraphdb.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := NewUniversalContentStore(ContentStoreConfig{
		BlevePath: filepath.Join(tmpDir, "test.bleve"),
		VectorDB:  db,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pressureLevel := &atomic.Int32{}
	goroutineBudget := concurrency.NewGoroutineBudget(pressureLevel)

	_, err = NewStartupIndexer(StartupIndexerConfig{
		ContentStore:     store,
		GoroutineBudget:  goroutineBudget,
		FileHandleBudget: nil,
	})

	if err != ErrNilFileHandleBudget {
		t.Errorf("expected ErrNilFileHandleBudget, got: %v", err)
	}
}

// =============================================================================
// IndexProject Tests
// =============================================================================

func TestIndexProject_HappyPath(t *testing.T) {
	indexer, store, cleanup := setupTestIndexer(t)
	defer cleanup()

	projectDir, projectCleanup := createTestDirectory(t)
	defer projectCleanup()

	var progressCalls []string
	var mu sync.Mutex
	indexer.SetProgressCallback(func(phase string, indexed, total int) {
		mu.Lock()
		progressCalls = append(progressCalls, phase)
		mu.Unlock()
	})

	ctx := context.Background()
	err := indexer.IndexProject(ctx, projectDir, nil)
	if err != nil {
		t.Fatalf("IndexProject failed: %v", err)
	}

	// Wait for async indexing
	time.Sleep(500 * time.Millisecond)

	// Verify progress callbacks were called
	mu.Lock()
	defer mu.Unlock()

	if len(progressCalls) == 0 {
		t.Error("expected progress callbacks to be called")
	}

	// Verify some files were indexed
	count, err := store.EntryCount()
	if err != nil {
		t.Fatalf("EntryCount failed: %v", err)
	}

	if count == 0 {
		t.Error("expected some files to be indexed")
	}
}

func TestIndexProject_NonExistentPath(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	ctx := context.Background()
	err := indexer.IndexProject(ctx, "/nonexistent/path/that/does/not/exist", nil)

	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestIndexProject_FileInsteadOfDirectory(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Create a file instead of directory
	tmpFile, err := os.CreateTemp("", "test_file")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	ctx := context.Background()
	err = indexer.IndexProject(ctx, tmpFile.Name(), nil)

	if err != ErrRootNotDirectory {
		t.Errorf("expected ErrRootNotDirectory, got: %v", err)
	}
}

func TestIndexProject_EmptyDirectory(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Create empty directory
	tmpDir, err := os.MkdirTemp("", "empty_project")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	err = indexer.IndexProject(ctx, tmpDir, nil)

	if err != nil {
		t.Errorf("expected no error for empty directory, got: %v", err)
	}
}

func TestIndexProject_ContextCancellation(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	projectDir, projectCleanup := createTestDirectory(t)
	defer projectCleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := indexer.IndexProject(ctx, projectDir, nil)

	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestIndexProject_SecurityFilters(t *testing.T) {
	indexer, store, cleanup := setupTestIndexer(t)
	defer cleanup()

	projectDir, projectCleanup := createTestDirectory(t)
	defer projectCleanup()

	ctx := context.Background()
	err := indexer.IndexProject(ctx, projectDir, nil)
	if err != nil {
		t.Fatalf("IndexProject failed: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Verify .env file was NOT indexed
	results, err := store.Search(".env", nil, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	for _, r := range results {
		if r.RelatedFiles != nil && len(r.RelatedFiles) > 0 {
			if r.RelatedFiles[0] == ".env" {
				t.Error(".env file should not be indexed")
			}
		}
	}
}

func TestIndexProject_ExcludedDirectories(t *testing.T) {
	indexer, store, cleanup := setupTestIndexer(t)
	defer cleanup()

	projectDir, projectCleanup := createTestDirectory(t)
	defer projectCleanup()

	ctx := context.Background()
	err := indexer.IndexProject(ctx, projectDir, nil)
	if err != nil {
		t.Fatalf("IndexProject failed: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Verify vendor directory was NOT indexed
	results, err := store.Search("vendor", nil, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	for _, r := range results {
		if r.RelatedFiles != nil && len(r.RelatedFiles) > 0 {
			path := r.RelatedFiles[0]
			if filepath.HasPrefix(path, "vendor/") {
				t.Errorf("vendor file should not be indexed: %s", path)
			}
		}
	}
}

func TestIndexProject_CustomConfig(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	projectDir, projectCleanup := createTestDirectory(t)
	defer projectCleanup()

	config := &StartupIndexConfig{
		MaxConcurrency:  2,
		BatchSize:       10,
		PriorityPaths:   []string{"cmd/"},
		ExcludePaths:    []string{"internal/"},
		ExcludePatterns: []string{"*.md"},
		MaxFileSize:     100 * 1024, // 100KB
		SecurityFilters: []string{".env"},
	}

	ctx := context.Background()
	err := indexer.IndexProject(ctx, projectDir, config)

	if err != nil {
		t.Errorf("IndexProject with custom config failed: %v", err)
	}
}

// =============================================================================
// IncrementalIndex Tests
// =============================================================================

func TestIncrementalIndex_HappyPath(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	projectDir, projectCleanup := createTestDirectory(t)
	defer projectCleanup()

	// Set since time to 1 second ago
	since := time.Now().Add(-time.Second)

	ctx := context.Background()
	err := indexer.IncrementalIndex(ctx, projectDir, since, nil)

	if err != nil {
		t.Errorf("IncrementalIndex failed: %v", err)
	}
}

func TestIncrementalIndex_NoModifiedFiles(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	projectDir, projectCleanup := createTestDirectory(t)
	defer projectCleanup()

	// Set since time to future
	since := time.Now().Add(time.Hour)

	var progressPhases []string
	var mu sync.Mutex
	indexer.SetProgressCallback(func(phase string, indexed, total int) {
		mu.Lock()
		progressPhases = append(progressPhases, phase)
		mu.Unlock()
	})

	ctx := context.Background()
	err := indexer.IncrementalIndex(ctx, projectDir, since, nil)

	if err != nil {
		t.Errorf("IncrementalIndex failed: %v", err)
	}

	// Should complete without indexing anything
	mu.Lock()
	defer mu.Unlock()

	hasComplete := false
	for _, phase := range progressPhases {
		if phase == "complete" {
			hasComplete = true
		}
	}

	if !hasComplete {
		t.Error("expected complete phase in progress")
	}
}

// =============================================================================
// GetIndexedCount Tests
// =============================================================================

func TestGetIndexedCount_HappyPath(t *testing.T) {
	indexer, store, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Index a test entry directly
	entry := &ContentEntry{
		ID:          "test-entry",
		SessionID:   "session1",
		AgentID:     "agent1",
		AgentType:   "engineer",
		ContentType: ContentTypeCodeFile,
		Content:     "test content",
		TokenCount:  10,
		Timestamp:   time.Now(),
	}
	store.IndexContent(entry)
	time.Sleep(200 * time.Millisecond)

	count, err := indexer.GetIndexedCount()
	if err != nil {
		t.Fatalf("GetIndexedCount failed: %v", err)
	}

	if count == 0 {
		t.Error("expected positive count")
	}
}

// =============================================================================
// IsIndexed Tests
// =============================================================================

func TestIsIndexed_True(t *testing.T) {
	indexer, store, cleanup := setupTestIndexer(t)
	defer cleanup()

	entry := &ContentEntry{
		ID:           "test-entry",
		SessionID:    "session1",
		AgentID:      "agent1",
		AgentType:    "engineer",
		ContentType:  ContentTypeCodeFile,
		Content:      "unique test content for is indexed check",
		TokenCount:   10,
		Timestamp:    time.Now(),
		RelatedFiles: []string{"test/path.go"},
	}
	store.IndexContent(entry)
	time.Sleep(200 * time.Millisecond)

	ctx := context.Background()
	indexed, err := indexer.IsIndexed(ctx, "unique test content")
	if err != nil {
		t.Fatalf("IsIndexed failed: %v", err)
	}

	if !indexed {
		t.Error("expected file to be indexed")
	}
}

func TestIsIndexed_False(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	ctx := context.Background()
	indexed, err := indexer.IsIndexed(ctx, "nonexistent_file_xyz123.go")
	if err != nil {
		t.Fatalf("IsIndexed failed: %v", err)
	}

	if indexed {
		t.Error("expected file to not be indexed")
	}
}

// =============================================================================
// DefaultStartupIndexConfig Tests
// =============================================================================

func TestDefaultStartupIndexConfig(t *testing.T) {
	config := DefaultStartupIndexConfig()

	if config.MaxConcurrency <= 0 {
		t.Error("expected positive MaxConcurrency")
	}

	if config.BatchSize <= 0 {
		t.Error("expected positive BatchSize")
	}

	if len(config.PriorityPaths) == 0 {
		t.Error("expected non-empty PriorityPaths")
	}

	if len(config.ExcludePaths) == 0 {
		t.Error("expected non-empty ExcludePaths")
	}

	if len(config.ExcludePatterns) == 0 {
		t.Error("expected non-empty ExcludePatterns")
	}

	if config.MaxFileSize <= 0 {
		t.Error("expected positive MaxFileSize")
	}

	if len(config.SecurityFilters) == 0 {
		t.Error("expected non-empty SecurityFilters")
	}
}

// =============================================================================
// Priority Calculation Tests
// =============================================================================

func TestCalculatePriority_RecentFile(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	recentTime := time.Now()
	oldTime := time.Now().Add(-365 * 24 * time.Hour)

	recentPriority := indexer.calculatePriority("src/main.go", recentTime, []string{"src/"})
	oldPriority := indexer.calculatePriority("src/main.go", oldTime, []string{"src/"})

	if recentPriority <= oldPriority {
		t.Error("recent file should have higher priority")
	}
}

func TestCalculatePriority_PriorityPath(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	modTime := time.Now()

	cmdPriority := indexer.calculatePriority("cmd/main.go", modTime, []string{"cmd/"})
	otherPriority := indexer.calculatePriority("other/main.go", modTime, []string{"cmd/"})

	if cmdPriority <= otherPriority {
		t.Error("priority path file should have higher priority")
	}
}

func TestCalculatePriority_HighValueExtension(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	modTime := time.Now()

	goPriority := indexer.calculatePriority("file.go", modTime, nil)
	txtPriority := indexer.calculatePriority("file.txt", modTime, nil)

	if goPriority <= txtPriority {
		t.Error(".go file should have higher priority than .txt")
	}
}

// =============================================================================
// Security Pattern Matching Tests
// =============================================================================

func TestMatchesSecurityPattern(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		pattern string
		want    bool
	}{
		{"exact match", ".env", ".env", true},
		{"no match", "main.go", ".env", false},
		{"wildcard prefix", "secrets.pem", "*.pem", true},
		{"wildcard prefix no match", "secrets.txt", "*.pem", false},
		{"wildcard pattern", ".env.local", ".env.*", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesSecurityPattern(tt.file, tt.pattern)
			if got != tt.want {
				t.Errorf("matchesSecurityPattern(%q, %q) = %v, want %v",
					tt.file, tt.pattern, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Language Detection Tests
// =============================================================================

func TestDetectLanguageFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"app.ts", "typescript"},
		{"component.tsx", "typescript"},
		{"script.js", "javascript"},
		{"app.jsx", "javascript"},
		{"main.py", "python"},
		{"lib.rs", "rust"},
		{"App.java", "java"},
		{"main.cpp", "cpp"},
		{"main.c", "c"},
		{"script.rb", "ruby"},
		{"index.php", "php"},
		{"README.md", "markdown"},
		{"config.yaml", "yaml"},
		{"config.yml", "yaml"},
		{"package.json", "json"},
		{"config.toml", "toml"},
		{"unknown.xyz", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := detectLanguageFromPath(tt.path)
			if got != tt.want {
				t.Errorf("detectLanguageFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Token Count Estimation Tests
// =============================================================================

func TestEstimateTokenCount(t *testing.T) {
	content := []byte("This is a test content with some words")
	tokens := estimateTokenCount(content)

	// Should be roughly len(content) / 4
	expected := len(content) / 4
	if tokens != expected {
		t.Errorf("estimateTokenCount() = %d, want %d", tokens, expected)
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestStartupIndexerConcurrentIndexing(t *testing.T) {
	indexer, store, cleanup := setupTestIndexer(t)
	defer cleanup()

	projectDir, projectCleanup := createTestDirectory(t)
	defer projectCleanup()

	// Run multiple index operations concurrently
	var wg sync.WaitGroup
	errCh := make(chan error, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			if err := indexer.IndexProject(ctx, projectDir, nil); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent indexing error: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	count, err := store.EntryCount()
	if err != nil {
		t.Fatalf("EntryCount failed: %v", err)
	}

	if count == 0 {
		t.Error("expected files to be indexed")
	}
}

// =============================================================================
// Progress Callback Tests
// =============================================================================

func TestProgressCallback_Phases(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	projectDir, projectCleanup := createTestDirectory(t)
	defer projectCleanup()

	var phases []string
	var mu sync.Mutex
	indexer.SetProgressCallback(func(phase string, indexed, total int) {
		mu.Lock()
		phases = append(phases, phase)
		mu.Unlock()
	})

	ctx := context.Background()
	indexer.IndexProject(ctx, projectDir, nil)

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should have scanning, indexing (multiple times), and complete phases
	hasScanning := false
	hasIndexing := false
	hasComplete := false

	for _, phase := range phases {
		switch phase {
		case "scanning":
			hasScanning = true
		case "indexing":
			hasIndexing = true
		case "complete":
			hasComplete = true
		}
	}

	if !hasScanning {
		t.Error("expected scanning phase")
	}
	if !hasIndexing {
		t.Error("expected indexing phase")
	}
	if !hasComplete {
		t.Error("expected complete phase")
	}
}

// =============================================================================
// WalkDir Helper Tests
// =============================================================================

func TestWalkDir_ContextCancellation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "walkdir_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create some files
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("test"), 0644)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err = WalkDir(ctx, tmpDir, func(path string, d os.DirEntry, err error) error {
		return nil
	})

	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestIndexProject_WithSymlinks(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skipping symlink test in CI")
	}

	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Create directory with symlink
	tmpDir, err := os.MkdirTemp("", "symlink_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a real file
	realFile := filepath.Join(tmpDir, "real.go")
	os.WriteFile(realFile, []byte("package main"), 0644)

	// Create a symlink
	linkPath := filepath.Join(tmpDir, "link.go")
	os.Symlink(realFile, linkPath)

	ctx := context.Background()
	err = indexer.IndexProject(ctx, tmpDir, nil)

	// Should not fail due to symlinks
	if err != nil {
		t.Errorf("IndexProject with symlinks failed: %v", err)
	}
}

func TestIndexProject_WithPermissionErrors(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping permission test as root")
	}

	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	tmpDir, err := os.MkdirTemp("", "permission_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a readable file
	readableFile := filepath.Join(tmpDir, "readable.go")
	os.WriteFile(readableFile, []byte("package main"), 0644)

	// Create an unreadable directory
	unreadableDir := filepath.Join(tmpDir, "unreadable")
	os.Mkdir(unreadableDir, 0000)
	defer os.Chmod(unreadableDir, 0755) // Restore for cleanup

	ctx := context.Background()
	err = indexer.IndexProject(ctx, tmpDir, nil)

	// Should succeed despite permission errors (skipping unreadable)
	if err != nil {
		t.Errorf("IndexProject should handle permission errors gracefully: %v", err)
	}
}

func TestIndexProject_LargeFileSkipped(t *testing.T) {
	indexer, store, cleanup := setupTestIndexer(t)
	defer cleanup()

	projectDir, projectCleanup := createTestDirectory(t)
	defer projectCleanup()

	ctx := context.Background()
	err := indexer.IndexProject(ctx, projectDir, nil)
	if err != nil {
		t.Fatalf("IndexProject failed: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Search for the large file content - it should not be found
	results, err := store.Search("large_file", nil, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// The large file should have been skipped
	for _, r := range results {
		if r.RelatedFiles != nil && len(r.RelatedFiles) > 0 {
			if r.RelatedFiles[0] == "large_file.txt" {
				t.Error("large file should have been skipped")
			}
		}
	}
}
