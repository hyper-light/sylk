package indexer

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// createTestDir creates a temporary directory structure for testing.
// Returns the root path and a cleanup function.
func createTestDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// createFile creates a file with the given content in the directory.
func createFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)

	// Ensure parent directory exists
	parentDir := filepath.Dir(path)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatalf("failed to create parent directory: %v", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create file %s: %v", path, err)
	}
	return path
}

// createDir creates a directory.
func createDir(t *testing.T, parent, name string) string {
	t.Helper()
	path := filepath.Join(parent, name)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("failed to create directory %s: %v", path, err)
	}
	return path
}

// collectFiles collects all files from a channel into a slice.
func collectFiles(ch <-chan *FileInfo) []*FileInfo {
	var files []*FileInfo
	for f := range ch {
		files = append(files, f)
	}
	return files
}

// sortFilesByPath sorts files by path for deterministic comparison.
func sortFilesByPath(files []*FileInfo) {
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
}

// findFile finds a file by name in the slice.
func findFile(files []*FileInfo, name string) *FileInfo {
	for _, f := range files {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// =============================================================================
// ScanConfig Tests
// =============================================================================

func TestScanConfig_Defaults(t *testing.T) {
	t.Parallel()

	config := ScanConfig{
		RootPath: "/some/path",
	}

	if config.MaxFileSize != 0 {
		t.Error("expected MaxFileSize to be zero before NewScanner")
	}

	if config.FollowSymlinks {
		t.Error("expected FollowSymlinks to be false by default")
	}

	if len(config.IncludePatterns) != 0 {
		t.Error("expected IncludePatterns to be empty by default")
	}

	if len(config.ExcludePatterns) != 0 {
		t.Error("expected ExcludePatterns to be empty by default")
	}
}

// =============================================================================
// NewScanner Tests
// =============================================================================

func TestNewScanner(t *testing.T) {
	t.Parallel()

	config := ScanConfig{
		RootPath:        "/test/path",
		IncludePatterns: []string{"*.go"},
		ExcludePatterns: []string{"*_test.go"},
		MaxFileSize:     5 * 1024 * 1024,
		FollowSymlinks:  true,
	}

	scanner := NewScanner(config)

	if scanner == nil {
		t.Fatal("NewScanner returned nil")
	}

	if scanner.config.RootPath != "/test/path" {
		t.Errorf("RootPath = %q, want %q", scanner.config.RootPath, "/test/path")
	}

	if scanner.maxFileSize != 5*1024*1024 {
		t.Errorf("maxFileSize = %d, want %d", scanner.maxFileSize, 5*1024*1024)
	}
}

func TestNewScanner_DefaultMaxFileSize(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(ScanConfig{RootPath: "/test"})

	if scanner.maxFileSize != DefaultMaxFileSize {
		t.Errorf("maxFileSize = %d, want default %d", scanner.maxFileSize, DefaultMaxFileSize)
	}
}

func TestNewScanner_ZeroMaxFileSize(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(ScanConfig{
		RootPath:    "/test",
		MaxFileSize: 0,
	})

	if scanner.maxFileSize != DefaultMaxFileSize {
		t.Errorf("maxFileSize = %d, want default %d", scanner.maxFileSize, DefaultMaxFileSize)
	}
}

func TestNewScanner_NegativeMaxFileSize(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(ScanConfig{
		RootPath:    "/test",
		MaxFileSize: -100,
	})

	if scanner.maxFileSize != DefaultMaxFileSize {
		t.Errorf("maxFileSize = %d, want default %d", scanner.maxFileSize, DefaultMaxFileSize)
	}
}

// =============================================================================
// Scan Validation Tests
// =============================================================================

func TestScanner_Scan_EmptyRootPath(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(ScanConfig{})

	_, err := scanner.Scan(context.Background())
	if err != ErrRootPathEmpty {
		t.Errorf("Scan() error = %v, want %v", err, ErrRootPathEmpty)
	}
}

func TestScanner_Scan_NonExistentPath(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(ScanConfig{
		RootPath: "/nonexistent/path/that/does/not/exist",
	})

	_, err := scanner.Scan(context.Background())
	if err != ErrRootPathNotExist {
		t.Errorf("Scan() error = %v, want %v", err, ErrRootPathNotExist)
	}
}

func TestScanner_Scan_RootPathIsFile(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	filePath := createFile(t, dir, "file.txt", "content")

	scanner := NewScanner(ScanConfig{
		RootPath: filePath,
	})

	_, err := scanner.Scan(context.Background())
	if err != ErrRootPathNotDir {
		t.Errorf("Scan() error = %v, want %v", err, ErrRootPathNotDir)
	}
}

func TestScanner_Scan_InvalidIncludePattern(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)

	scanner := NewScanner(ScanConfig{
		RootPath:        dir,
		IncludePatterns: []string{"[invalid"},
	})

	_, err := scanner.Scan(context.Background())
	if err == nil {
		t.Error("Scan() expected error for invalid pattern, got nil")
	}
}

func TestScanner_Scan_InvalidExcludePattern(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)

	scanner := NewScanner(ScanConfig{
		RootPath:        dir,
		ExcludePatterns: []string{"[invalid"},
	})

	_, err := scanner.Scan(context.Background())
	if err == nil {
		t.Error("Scan() expected error for invalid pattern, got nil")
	}
}

// =============================================================================
// Basic Scanning Tests
// =============================================================================

func TestScanner_Scan_EmptyDirectory(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestScanner_Scan_SingleFile(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	f := files[0]
	if f.Name != "main.go" {
		t.Errorf("Name = %q, want %q", f.Name, "main.go")
	}
	if f.Extension != ".go" {
		t.Errorf("Extension = %q, want %q", f.Extension, ".go")
	}
	if f.IsDir {
		t.Error("IsDir = true, want false")
	}
}

func TestScanner_Scan_MultipleFiles(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")
	createFile(t, dir, "handler.go", "package main")
	createFile(t, dir, "config.yaml", "key: value")

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}
}

func TestScanner_Scan_NestedDirectories(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "root.go", "package root")
	createDir(t, dir, "pkg")
	createFile(t, dir, "pkg/lib.go", "package pkg")
	createDir(t, dir, "pkg/sub")
	createFile(t, dir, "pkg/sub/deep.go", "package sub")

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}

	// Verify nested file was found
	found := false
	for _, f := range files {
		if f.Name == "deep.go" {
			found = true
			break
		}
	}
	if !found {
		t.Error("nested file 'deep.go' not found")
	}
}

// =============================================================================
// Include Pattern Tests
// =============================================================================

func TestScanner_Scan_IncludePattern_SingleExtension(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")
	createFile(t, dir, "app.ts", "const x = 1")
	createFile(t, dir, "config.yaml", "key: value")

	scanner := NewScanner(ScanConfig{
		RootPath:        dir,
		IncludePatterns: []string{"*.go"},
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	if files[0].Name != "main.go" {
		t.Errorf("Name = %q, want %q", files[0].Name, "main.go")
	}
}

func TestScanner_Scan_IncludePattern_MultipleExtensions(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")
	createFile(t, dir, "app.ts", "const x = 1")
	createFile(t, dir, "script.py", "x = 1")
	createFile(t, dir, "config.yaml", "key: value")

	scanner := NewScanner(ScanConfig{
		RootPath:        dir,
		IncludePatterns: []string{"*.go", "*.ts", "*.py"},
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}
}

func TestScanner_Scan_IncludePattern_RecursiveMatch(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "root.go", "package root")
	createDir(t, dir, "pkg")
	createFile(t, dir, "pkg/lib.go", "package pkg")
	createFile(t, dir, "pkg/data.json", "{}")

	scanner := NewScanner(ScanConfig{
		RootPath:        dir,
		IncludePatterns: []string{"**/*.go", "*.go"},
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 2 {
		t.Errorf("expected 2 Go files, got %d", len(files))
	}
}

// =============================================================================
// Exclude Pattern Tests
// =============================================================================

func TestScanner_Scan_ExcludePattern_TestFiles(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")
	createFile(t, dir, "main_test.go", "package main")
	createFile(t, dir, "handler.go", "package main")
	createFile(t, dir, "handler_test.go", "package main")

	scanner := NewScanner(ScanConfig{
		RootPath:        dir,
		ExcludePatterns: []string{"*_test.go"},
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 2 {
		t.Errorf("expected 2 files (excluding test files), got %d", len(files))
	}

	for _, f := range files {
		if f.Name == "main_test.go" || f.Name == "handler_test.go" {
			t.Errorf("test file %q should have been excluded", f.Name)
		}
	}
}

func TestScanner_Scan_ExcludePattern_Generated(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")
	createFile(t, dir, "types.generated.go", "package main")
	createFile(t, dir, "mock.generated.go", "package main")

	scanner := NewScanner(ScanConfig{
		RootPath:        dir,
		ExcludePatterns: []string{"*.generated.go"},
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d", len(files))
	}
}

func TestScanner_Scan_IncludeAndExclude(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")
	createFile(t, dir, "main_test.go", "package main")
	createFile(t, dir, "app.ts", "const x = 1")
	createFile(t, dir, "config.yaml", "key: value")

	scanner := NewScanner(ScanConfig{
		RootPath:        dir,
		IncludePatterns: []string{"*.go"},
		ExcludePatterns: []string{"*_test.go"},
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	if files[0].Name != "main.go" {
		t.Errorf("Name = %q, want %q", files[0].Name, "main.go")
	}
}

// =============================================================================
// Default Directory Exclusion Tests
// =============================================================================

func TestScanner_Scan_ExcludesGitDir(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")
	createDir(t, dir, ".git")
	createFile(t, dir, ".git/config", "git config")

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)

	for _, f := range files {
		if f.Name == "config" && filepath.Dir(f.Path) == filepath.Join(dir, ".git") {
			t.Error(".git directory should have been excluded")
		}
	}
}

func TestScanner_Scan_ExcludesNodeModules(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "index.js", "module.exports = {}")
	createDir(t, dir, "node_modules")
	createFile(t, dir, "node_modules/lodash/index.js", "module.exports = {}")

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 1 {
		t.Errorf("expected 1 file (node_modules excluded), got %d", len(files))
	}
}

func TestScanner_Scan_ExcludesAllDefaultDirs(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")

	// Create all default excluded directories with files
	excludedDirs := []string{
		".git", "node_modules", "vendor", "__pycache__",
		".next", "dist", "build", ".cache",
		"target", "bin", "obj", ".idea", ".vscode",
	}

	for _, excDir := range excludedDirs {
		createDir(t, dir, excDir)
		createFile(t, dir, excDir+"/file.txt", "content")
	}

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 1 {
		t.Errorf("expected 1 file (all excluded dirs), got %d", len(files))
	}

	if files[0].Name != "main.go" {
		t.Errorf("expected main.go, got %s", files[0].Name)
	}
}

// =============================================================================
// MaxFileSize Tests
// =============================================================================

func TestScanner_Scan_MaxFileSize(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)

	// Create a small file
	createFile(t, dir, "small.txt", "small content")

	// Create a large file (1KB for this test)
	largeContent := make([]byte, 2000)
	for i := range largeContent {
		largeContent[i] = 'x'
	}
	createFile(t, dir, "large.txt", string(largeContent))

	scanner := NewScanner(ScanConfig{
		RootPath:    dir,
		MaxFileSize: 1000, // 1KB limit
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 1 {
		t.Fatalf("expected 1 file (small only), got %d", len(files))
	}

	if files[0].Name != "small.txt" {
		t.Errorf("expected small.txt, got %s", files[0].Name)
	}
}

func TestScanner_Scan_MaxFileSize_ExactLimit(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)

	// Create file exactly at limit
	exactContent := make([]byte, 100)
	for i := range exactContent {
		exactContent[i] = 'x'
	}
	createFile(t, dir, "exact.txt", string(exactContent))

	// Create file just over limit
	overContent := make([]byte, 101)
	for i := range overContent {
		overContent[i] = 'y'
	}
	createFile(t, dir, "over.txt", string(overContent))

	scanner := NewScanner(ScanConfig{
		RootPath:    dir,
		MaxFileSize: 100,
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 1 {
		t.Fatalf("expected 1 file (at limit), got %d", len(files))
	}

	if files[0].Name != "exact.txt" {
		t.Errorf("expected exact.txt, got %s", files[0].Name)
	}
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

func TestScanner_Scan_ContextCancellation(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)

	// Create many files
	for i := 0; i < 100; i++ {
		createFile(t, dir, filepath.Join("subdir", "file"+string(rune('a'+i%26))+".go"), "package main")
	}

	ctx, cancel := context.WithCancel(context.Background())

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	// Read a few files then cancel
	count := 0
	for range ch {
		count++
		if count >= 5 {
			cancel()
			break
		}
	}

	// Drain remaining items
	for range ch {
		count++
	}

	// We should have gotten at least some files but potentially not all
	if count < 5 {
		t.Errorf("expected at least 5 files before cancel, got %d", count)
	}
}

func TestScanner_Scan_ContextAlreadyCancelled(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	// May get some files or none depending on timing
	// The important thing is that it doesn't hang
	_ = files
}

func TestScanner_Scan_ContextTimeout(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	// Let timeout occur
	time.Sleep(20 * time.Millisecond)

	// Drain channel - should complete quickly
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()

	select {
	case <-done:
		// Good, channel was closed
	case <-time.After(time.Second):
		t.Error("channel was not closed after timeout")
	}
}

// =============================================================================
// Symlink Tests
// =============================================================================

func TestScanner_Scan_SymlinkDir_NotFollowed(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")

	// Create a subdirectory with a file
	subDir := createDir(t, dir, "realdir")
	createFile(t, subDir, "lib.go", "package lib")

	// Create symlink to the subdirectory
	symlinkPath := filepath.Join(dir, "linkeddir")
	if err := os.Symlink(subDir, symlinkPath); err != nil {
		t.Skip("symlink creation not supported on this platform")
	}

	scanner := NewScanner(ScanConfig{
		RootPath:       dir,
		FollowSymlinks: false,
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)

	// Should find main.go and lib.go from realdir, but not from linkeddir
	names := make(map[string]int)
	for _, f := range files {
		names[f.Name]++
	}

	if names["lib.go"] != 1 {
		t.Errorf("expected lib.go once (symlink not followed), found %d times", names["lib.go"])
	}
}

func TestScanner_Scan_SymlinkDir_Followed(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "main.go", "package main")

	// Create external directory (outside root)
	extDir := t.TempDir()
	createFile(t, extDir, "external.go", "package external")

	// Create symlink to external directory
	symlinkPath := filepath.Join(dir, "external")
	if err := os.Symlink(extDir, symlinkPath); err != nil {
		t.Skip("symlink creation not supported on this platform")
	}

	scanner := NewScanner(ScanConfig{
		RootPath:       dir,
		FollowSymlinks: true,
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)

	// Should find both main.go and external.go (via symlink)
	foundMain := false
	foundExternal := false
	for _, f := range files {
		if f.Name == "main.go" {
			foundMain = true
		}
		if f.Name == "external.go" {
			foundExternal = true
		}
	}

	if !foundMain {
		t.Error("main.go not found")
	}
	if !foundExternal {
		t.Error("external.go not found when following symlinks")
	}
}

// =============================================================================
// FileInfo Tests
// =============================================================================

func TestScanner_Scan_FileInfo_Fields(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	content := "package main\nfunc main() {}"
	filePath := createFile(t, dir, "main.go", content)

	// Get expected mod time
	stat, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat error: %v", err)
	}

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	f := files[0]

	if f.Path != filePath {
		t.Errorf("Path = %q, want %q", f.Path, filePath)
	}

	if f.Name != "main.go" {
		t.Errorf("Name = %q, want %q", f.Name, "main.go")
	}

	if f.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", f.Size, len(content))
	}

	if f.Extension != ".go" {
		t.Errorf("Extension = %q, want %q", f.Extension, ".go")
	}

	if f.IsDir {
		t.Error("IsDir = true, want false")
	}

	// ModTime should match (within a second for filesystem precision)
	if f.ModTime.Sub(stat.ModTime()) > time.Second {
		t.Errorf("ModTime = %v, want close to %v", f.ModTime, stat.ModTime())
	}
}

func TestScanner_Scan_FileInfo_NoExtension(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "Makefile", "all: build")
	createFile(t, dir, "LICENSE", "MIT License")

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)

	for _, f := range files {
		if f.Extension != "" {
			t.Errorf("file %q has extension %q, want empty", f.Name, f.Extension)
		}
	}
}

func TestScanner_Scan_FileInfo_ExtensionLowercase(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "README.MD", "# Title")
	createFile(t, dir, "image.PNG", "fake image")

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)

	for _, f := range files {
		if f.Extension != ".md" && f.Extension != ".png" {
			t.Errorf("file %q has extension %q, want lowercase", f.Name, f.Extension)
		}
	}
}

// =============================================================================
// Permission Error Tests
// =============================================================================

func TestScanner_Scan_PermissionDenied_File(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test cannot run as root")
	}

	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "readable.go", "package main")
	noReadPath := createFile(t, dir, "noread.go", "package main")

	// Remove read permission
	if err := os.Chmod(noReadPath, 0000); err != nil {
		t.Fatalf("chmod error: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(noReadPath, 0644) // Restore for cleanup
	})

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)

	// Should still find the readable file
	found := false
	for _, f := range files {
		if f.Name == "readable.go" {
			found = true
		}
	}

	if !found {
		t.Error("readable.go not found")
	}
}

func TestScanner_Scan_PermissionDenied_Dir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test cannot run as root")
	}

	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "root.go", "package main")

	noAccessDir := createDir(t, dir, "noaccess")
	createFile(t, noAccessDir, "hidden.go", "package hidden")

	// Remove execute permission (needed to traverse)
	if err := os.Chmod(noAccessDir, 0000); err != nil {
		t.Fatalf("chmod error: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(noAccessDir, 0755) // Restore for cleanup
	})

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)

	// Should find root.go but not hidden.go
	foundRoot := false
	foundHidden := false
	for _, f := range files {
		if f.Name == "root.go" {
			foundRoot = true
		}
		if f.Name == "hidden.go" {
			foundHidden = true
		}
	}

	if !foundRoot {
		t.Error("root.go not found")
	}
	if foundHidden {
		t.Error("hidden.go should not be found (permission denied)")
	}
}

// =============================================================================
// Error Tests
// =============================================================================

func TestErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err     error
		wantMsg string
	}{
		{ErrRootPathEmpty, "root path cannot be empty"},
		{ErrRootPathNotExist, "root path does not exist"},
		{ErrRootPathNotDir, "root path is not a directory"},
		{ErrInvalidPattern, "invalid glob pattern"},
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
// Edge Case Tests
// =============================================================================

func TestScanner_Scan_DotFiles(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, ".env", "SECRET=value")
	createFile(t, dir, ".gitignore", "*.log")
	createFile(t, dir, "main.go", "package main")

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 3 {
		t.Errorf("expected 3 files (including dot files), got %d", len(files))
	}
}

func TestScanner_Scan_SpecialCharactersInFilename(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "file with spaces.go", "package main")
	createFile(t, dir, "file-with-dashes.go", "package main")
	createFile(t, dir, "file_with_underscores.go", "package main")

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}
}

func TestScanner_Scan_EmptyFile(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)
	createFile(t, dir, "empty.go", "")

	scanner := NewScanner(ScanConfig{RootPath: dir})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	if files[0].Size != 0 {
		t.Errorf("Size = %d, want 0", files[0].Size)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestScanner_Scan_RealWorldScenario(t *testing.T) {
	t.Parallel()

	dir := createTestDir(t)

	// Simulate a Go project structure
	createFile(t, dir, "go.mod", "module example.com/myproject")
	createFile(t, dir, "go.sum", "")
	createFile(t, dir, "main.go", "package main\nfunc main() {}")
	createFile(t, dir, "main_test.go", "package main\nfunc TestMain(t *testing.T) {}")
	createFile(t, dir, "README.md", "# My Project")

	// cmd directory
	createDir(t, dir, "cmd")
	createFile(t, dir, "cmd/cli.go", "package cmd")
	createFile(t, dir, "cmd/cli_test.go", "package cmd")

	// internal directory
	createDir(t, dir, "internal")
	createFile(t, dir, "internal/handler.go", "package internal")
	createFile(t, dir, "internal/handler_test.go", "package internal")

	// Excluded directories
	createDir(t, dir, ".git")
	createFile(t, dir, ".git/config", "git config")
	createDir(t, dir, "vendor")
	createFile(t, dir, "vendor/dep/lib.go", "package dep")

	scanner := NewScanner(ScanConfig{
		RootPath:        dir,
		IncludePatterns: []string{"*.go"},
		ExcludePatterns: []string{"*_test.go"},
	})

	ch, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	files := collectFiles(ch)
	sortFilesByPath(files)

	// Should find: main.go, cmd/cli.go, internal/handler.go
	// Should NOT find: *_test.go files, .git/*, vendor/*
	expectedNames := map[string]bool{
		"main.go":    false,
		"cli.go":     false,
		"handler.go": false,
	}

	for _, f := range files {
		if _, expected := expectedNames[f.Name]; expected {
			expectedNames[f.Name] = true
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected file %q not found", name)
		}
	}

	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
		for _, f := range files {
			t.Logf("found: %s", f.Path)
		}
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkScanner_Scan(b *testing.B) {
	dir := b.TempDir()

	// Create a moderately complex directory structure
	for i := 0; i < 10; i++ {
		subDir := filepath.Join(dir, "pkg"+string(rune('a'+i)))
		if err := os.MkdirAll(subDir, 0755); err != nil {
			b.Fatalf("mkdir error: %v", err)
		}
		for j := 0; j < 10; j++ {
			filePath := filepath.Join(subDir, "file"+string(rune('a'+j))+".go")
			if err := os.WriteFile(filePath, []byte("package pkg"), 0644); err != nil {
				b.Fatalf("write error: %v", err)
			}
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		scanner := NewScanner(ScanConfig{RootPath: dir})
		ch, err := scanner.Scan(context.Background())
		if err != nil {
			b.Fatalf("Scan() error = %v", err)
		}
		for range ch {
		}
	}
}

func BenchmarkScanner_Scan_WithPatterns(b *testing.B) {
	dir := b.TempDir()

	// Create mixed file types
	for i := 0; i < 10; i++ {
		subDir := filepath.Join(dir, "pkg"+string(rune('a'+i)))
		if err := os.MkdirAll(subDir, 0755); err != nil {
			b.Fatalf("mkdir error: %v", err)
		}
		for j := 0; j < 10; j++ {
			goPath := filepath.Join(subDir, "file"+string(rune('a'+j))+".go")
			if err := os.WriteFile(goPath, []byte("package pkg"), 0644); err != nil {
				b.Fatalf("write error: %v", err)
			}
			testPath := filepath.Join(subDir, "file"+string(rune('a'+j))+"_test.go")
			if err := os.WriteFile(testPath, []byte("package pkg"), 0644); err != nil {
				b.Fatalf("write error: %v", err)
			}
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		scanner := NewScanner(ScanConfig{
			RootPath:        dir,
			IncludePatterns: []string{"*.go"},
			ExcludePatterns: []string{"*_test.go"},
		})
		ch, err := scanner.Scan(context.Background())
		if err != nil {
			b.Fatalf("Scan() error = %v", err)
		}
		for range ch {
		}
	}
}
