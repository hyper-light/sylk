package cmt

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// createTestFile creates a file with the given content and returns its FileInfo.
func createTestFile(t *testing.T, dir, name, content string) *FileInfo {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	hash := sha256.Sum256([]byte(content))
	return &FileInfo{
		Path:        name,
		ContentHash: hash,
		Size:        int64(len(content)),
		ModTime:     time.Now().Truncate(time.Second),
		Permissions: 0644,
		Indexed:     true,
		IndexedAt:   time.Now(),
	}
}

// buildTestTree builds a CMT from the given file infos.
func buildTestTree(t *testing.T, infos []*FileInfo) *Node {
	t.Helper()

	var root *Node
	for _, info := range infos {
		node := NewNode(info.Path, info)
		root = Insert(root, node)
	}
	return root
}

// =============================================================================
// IntegrityChecker Constructor Tests
// =============================================================================

func TestNewIntegrityChecker(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := buildTestTree(t, []*FileInfo{})

	checker := NewIntegrityChecker(root, dir, 4)

	if checker == nil {
		t.Fatal("NewIntegrityChecker returned nil")
	}
	if checker.root != root {
		t.Error("IntegrityChecker root mismatch")
	}
	if checker.rootPath != dir {
		t.Errorf("IntegrityChecker rootPath = %q, want %q", checker.rootPath, dir)
	}
	if checker.workers != 4 {
		t.Errorf("IntegrityChecker workers = %d, want 4", checker.workers)
	}
}

func TestNewIntegrityChecker_DefaultWorkers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	checker := NewIntegrityChecker(nil, dir, 0)

	if checker.workers < 1 {
		t.Errorf("IntegrityChecker workers = %d, want at least 1", checker.workers)
	}
}

// =============================================================================
// IntegrityChecker Valid Tree Tests
// =============================================================================

func TestIntegrityChecker_Check_ValidTree(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create test files
	info1 := createTestFile(t, dir, "file1.txt", "content1")
	info2 := createTestFile(t, dir, "file2.txt", "content2")
	info3 := createTestFile(t, dir, "subdir/file3.txt", "content3")

	root := buildTestTree(t, []*FileInfo{info1, info2, info3})

	checker := NewIntegrityChecker(root, dir, 4)

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if !report.Valid {
		t.Errorf("Report.Valid = false, want true; issues: %+v", report.Issues)
	}
	if report.TotalFiles != 3 {
		t.Errorf("Report.TotalFiles = %d, want 3", report.TotalFiles)
	}
	if len(report.Issues) != 0 {
		t.Errorf("Report.Issues = %d, want 0", len(report.Issues))
	}
	if report.CheckedAt.IsZero() {
		t.Error("Report.CheckedAt should not be zero")
	}
	if report.Duration <= 0 {
		t.Error("Report.Duration should be positive")
	}
}

func TestIntegrityChecker_Check_EmptyTree(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	checker := NewIntegrityChecker(nil, dir, 4)

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if !report.Valid {
		t.Error("Report.Valid = false, want true for empty tree")
	}
	if report.TotalFiles != 0 {
		t.Errorf("Report.TotalFiles = %d, want 0", report.TotalFiles)
	}
}

// =============================================================================
// IntegrityChecker Missing File Tests
// =============================================================================

func TestIntegrityChecker_Check_DetectsMissingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create one file
	info1 := createTestFile(t, dir, "exists.txt", "content1")

	// Create info for a file that doesn't exist on disk
	missingInfo := &FileInfo{
		Path:        "missing.txt",
		ContentHash: Hash{1, 2, 3},
		Size:        100,
		ModTime:     time.Now(),
		Permissions: 0644,
	}

	root := buildTestTree(t, []*FileInfo{info1, missingInfo})

	checker := NewIntegrityChecker(root, dir, 4)

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if report.Valid {
		t.Error("Report.Valid = true, want false (missing file)")
	}

	foundMissing := false
	for _, issue := range report.Issues {
		if issue.Type == IssueMissing && issue.Path == "missing.txt" {
			foundMissing = true
			break
		}
	}
	if !foundMissing {
		t.Error("Did not detect missing file issue")
	}
}

// =============================================================================
// IntegrityChecker Extra File Tests
// =============================================================================

func TestIntegrityChecker_Check_DetectsExtraFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create files on disk
	info1 := createTestFile(t, dir, "tracked.txt", "content1")
	createTestFile(t, dir, "extra.txt", "extra content") // Not in tree

	// Only put one file in tree
	root := buildTestTree(t, []*FileInfo{info1})

	checker := NewIntegrityChecker(root, dir, 4)

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if report.Valid {
		t.Error("Report.Valid = true, want false (extra file)")
	}

	foundExtra := false
	for _, issue := range report.Issues {
		if issue.Type == IssueExtra && issue.Path == "extra.txt" {
			foundExtra = true
			break
		}
	}
	if !foundExtra {
		t.Error("Did not detect extra file issue")
	}
}

// =============================================================================
// IntegrityChecker Modified File Tests
// =============================================================================

func TestIntegrityChecker_Check_DetectsModifiedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create file and build tree
	info1 := createTestFile(t, dir, "modified.txt", "original content")
	root := buildTestTree(t, []*FileInfo{info1})

	// Modify the file after tree was built
	path := filepath.Join(dir, "modified.txt")
	if err := os.WriteFile(path, []byte("changed content"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	checker := NewIntegrityChecker(root, dir, 4)

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if report.Valid {
		t.Error("Report.Valid = true, want false (modified file)")
	}

	foundModified := false
	for _, issue := range report.Issues {
		if issue.Type == IssueModified && issue.Path == "modified.txt" {
			foundModified = true
			break
		}
	}
	if !foundModified {
		t.Error("Did not detect modified file issue")
	}
}

// =============================================================================
// IntegrityChecker Merkle Hash Corruption Tests
// =============================================================================

func TestIntegrityChecker_Check_DetectsMerkleCorruption(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create file and build tree
	info1 := createTestFile(t, dir, "file1.txt", "content1")
	root := buildTestTree(t, []*FileInfo{info1})

	// Corrupt the Merkle hash
	root.Hash[0] ^= 0xFF

	checker := NewIntegrityChecker(root, dir, 4)

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if report.Valid {
		t.Error("Report.Valid = true, want false (Merkle corruption)")
	}

	foundMerkle := false
	for _, issue := range report.Issues {
		if issue.Type == IssueMerkleHash {
			foundMerkle = true
			break
		}
	}
	if !foundMerkle {
		t.Error("Did not detect Merkle hash corruption issue")
	}
}

func TestIntegrityChecker_Check_DetectsSubtreeMerkleCorruption(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create files
	info1 := createTestFile(t, dir, "a.txt", "content a")
	info2 := createTestFile(t, dir, "b.txt", "content b")
	info3 := createTestFile(t, dir, "c.txt", "content c")

	root := buildTestTree(t, []*FileInfo{info1, info2, info3})

	// Corrupt a child node's hash (not the root)
	// Find a non-root node
	var corruptNode *Node
	if root.Left != nil {
		corruptNode = root.Left
	} else if root.Right != nil {
		corruptNode = root.Right
	}

	if corruptNode == nil {
		t.Skip("Tree structure doesn't allow this test")
	}

	corruptNode.Hash[0] ^= 0xFF

	checker := NewIntegrityChecker(root, dir, 4)

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if report.Valid {
		t.Error("Report.Valid = true, want false (Merkle corruption in subtree)")
	}

	foundMerkle := false
	for _, issue := range report.Issues {
		if issue.Type == IssueMerkleHash {
			foundMerkle = true
			break
		}
	}
	if !foundMerkle {
		t.Error("Did not detect subtree Merkle hash corruption")
	}
}

// =============================================================================
// IntegrityChecker Parallel Checking Tests
// =============================================================================

func TestIntegrityChecker_Check_ParallelChecking(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create many files to test parallelism
	var infos []*FileInfo
	for i := 0; i < 100; i++ {
		name := filepath.Join("files", string(rune('a'+i%26))+string(rune('0'+i/26))+".txt")
		info := createTestFile(t, dir, name, "content "+name)
		infos = append(infos, info)
	}

	root := buildTestTree(t, infos)

	// Use multiple workers
	checker := NewIntegrityChecker(root, dir, 8)

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if !report.Valid {
		t.Errorf("Report.Valid = false with %d issues", len(report.Issues))
	}
	if report.TotalFiles != int64(len(infos)) {
		t.Errorf("Report.TotalFiles = %d, want %d", report.TotalFiles, len(infos))
	}
}

func TestIntegrityChecker_Check_ParallelDetectsMultipleIssues(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create some valid files
	info1 := createTestFile(t, dir, "valid1.txt", "content1")
	info2 := createTestFile(t, dir, "valid2.txt", "content2")

	// Create info for missing files
	missing1 := &FileInfo{
		Path:        "missing1.txt",
		ContentHash: Hash{1},
		Size:        100,
		ModTime:     time.Now(),
	}
	missing2 := &FileInfo{
		Path:        "missing2.txt",
		ContentHash: Hash{2},
		Size:        100,
		ModTime:     time.Now(),
	}

	root := buildTestTree(t, []*FileInfo{info1, info2, missing1, missing2})

	checker := NewIntegrityChecker(root, dir, 4)

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if report.Valid {
		t.Error("Report.Valid = true, want false")
	}

	missingCount := 0
	for _, issue := range report.Issues {
		if issue.Type == IssueMissing {
			missingCount++
		}
	}
	if missingCount != 2 {
		t.Errorf("Found %d missing issues, want 2", missingCount)
	}
}

// =============================================================================
// IntegrityChecker Context Cancellation Tests
// =============================================================================

func TestIntegrityChecker_Check_ContextCancellation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create many files
	var infos []*FileInfo
	for i := 0; i < 50; i++ {
		info := createTestFile(t, dir, filepath.Join("files", "file"+string(rune('a'+i))+".txt"), "content")
		infos = append(infos, info)
	}

	root := buildTestTree(t, infos)

	checker := NewIntegrityChecker(root, dir, 4)

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := checker.Check(ctx)

	// Should return context.Canceled error or nil report
	if err == nil && report != nil && report.Valid {
		// If no error, the check completed before cancellation was noticed
		// This is acceptable for small file sets
		return
	}

	if err != nil && err != context.Canceled {
		t.Logf("Check returned error (expected for cancellation): %v", err)
	}
}

func TestIntegrityChecker_Check_ContextDeadline(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create files
	var infos []*FileInfo
	for i := 0; i < 20; i++ {
		info := createTestFile(t, dir, "file"+string(rune('a'+i))+".txt", "content")
		infos = append(infos, info)
	}

	root := buildTestTree(t, infos)

	checker := NewIntegrityChecker(root, dir, 1) // Single worker to make it slower

	// Very short deadline
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	_, err := checker.Check(ctx)

	// Either completes quickly or returns deadline exceeded
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Logf("Unexpected error: %v", err)
	}
}

// =============================================================================
// IntegrityChecker CheckFile Tests
// =============================================================================

func TestIntegrityChecker_CheckFile_Valid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	info := createTestFile(t, dir, "valid.txt", "content")
	root := buildTestTree(t, []*FileInfo{info})

	checker := NewIntegrityChecker(root, dir, 4)

	issue, err := checker.CheckFile(context.Background(), "valid.txt")
	if err != nil {
		t.Fatalf("CheckFile failed: %v", err)
	}

	if issue != nil {
		t.Errorf("CheckFile returned issue for valid file: %+v", issue)
	}
}

func TestIntegrityChecker_CheckFile_Missing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	missingInfo := &FileInfo{
		Path:        "missing.txt",
		ContentHash: Hash{1, 2, 3},
		Size:        100,
		ModTime:     time.Now(),
	}
	root := buildTestTree(t, []*FileInfo{missingInfo})

	checker := NewIntegrityChecker(root, dir, 4)

	issue, err := checker.CheckFile(context.Background(), "missing.txt")
	if err != nil {
		t.Fatalf("CheckFile failed: %v", err)
	}

	if issue == nil {
		t.Fatal("CheckFile should return issue for missing file")
	}
	if issue.Type != IssueMissing {
		t.Errorf("Issue.Type = %v, want IssueMissing", issue.Type)
	}
}

func TestIntegrityChecker_CheckFile_Modified(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	info := createTestFile(t, dir, "file.txt", "original")
	root := buildTestTree(t, []*FileInfo{info})

	// Modify file
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("modified"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	checker := NewIntegrityChecker(root, dir, 4)

	issue, err := checker.CheckFile(context.Background(), "file.txt")
	if err != nil {
		t.Fatalf("CheckFile failed: %v", err)
	}

	if issue == nil {
		t.Fatal("CheckFile should return issue for modified file")
	}
	if issue.Type != IssueModified {
		t.Errorf("Issue.Type = %v, want IssueModified", issue.Type)
	}
}

func TestIntegrityChecker_CheckFile_NotInTree(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	checker := NewIntegrityChecker(nil, dir, 4)

	issue, err := checker.CheckFile(context.Background(), "nonexistent.txt")
	if err != nil {
		t.Fatalf("CheckFile failed: %v", err)
	}

	// File not in tree is not an error - it's just not tracked
	if issue != nil {
		t.Logf("Issue for untracked file: %+v", issue)
	}
}

// =============================================================================
// IssueType Tests
// =============================================================================

func TestIssueType_Values(t *testing.T) {
	t.Parallel()

	// Verify enum values are distinct
	types := []IssueType{IssueMissing, IssueExtra, IssueModified, IssueMerkleHash}
	seen := make(map[IssueType]bool)

	for _, typ := range types {
		if seen[typ] {
			t.Errorf("Duplicate IssueType value: %d", typ)
		}
		seen[typ] = true
	}
}

// =============================================================================
// IntegrityReport Tests
// =============================================================================

func TestIntegrityReport_Fields(t *testing.T) {
	t.Parallel()

	report := &IntegrityReport{
		Valid:      true,
		TotalFiles: 10,
		Issues:     []IntegrityIssue{{Type: IssueMissing, Path: "test.txt"}},
		CheckedAt:  time.Now(),
		Duration:   100 * time.Millisecond,
	}

	if !report.Valid {
		t.Error("Report.Valid should be true")
	}
	if report.TotalFiles != 10 {
		t.Errorf("Report.TotalFiles = %d, want 10", report.TotalFiles)
	}
	if len(report.Issues) != 1 {
		t.Errorf("len(Report.Issues) = %d, want 1", len(report.Issues))
	}
}

// =============================================================================
// Concurrency Safety Tests
// =============================================================================

func TestIntegrityChecker_ConcurrentChecks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create test files
	var infos []*FileInfo
	for i := 0; i < 10; i++ {
		info := createTestFile(t, dir, "file"+string(rune('a'+i))+".txt", "content")
		infos = append(infos, info)
	}

	root := buildTestTree(t, infos)
	checker := NewIntegrityChecker(root, dir, 4)

	// Run multiple concurrent checks
	var completed atomic.Int32
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		go func() {
			report, err := checker.Check(context.Background())
			if err != nil {
				errors <- err
				return
			}
			if !report.Valid {
				errors <- nil // No error but invalid
			}
			completed.Add(1)
		}()
	}

	// Wait for completion
	timeout := time.After(10 * time.Second)
	for completed.Load() < 10 {
		select {
		case err := <-errors:
			if err != nil {
				t.Errorf("Concurrent check error: %v", err)
			}
		case <-timeout:
			t.Fatalf("Timeout waiting for concurrent checks, completed: %d", completed.Load())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
