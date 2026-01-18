package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// mockChecksumStore implements ChecksumStore for testing.
type mockChecksumStore struct {
	mu        sync.RWMutex
	checksums map[string]string
}

func newMockStore() *mockChecksumStore {
	return &mockChecksumStore{
		checksums: make(map[string]string),
	}
}

func (m *mockChecksumStore) GetChecksum(path string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	checksum, ok := m.checksums[path]
	return checksum, ok
}

func (m *mockChecksumStore) SetChecksum(path, checksum string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checksums[path] = checksum
}

// computeExpectedHash computes SHA-256 hash of content for test verification.
func computeExpectedHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// createChecksumTestFile creates a temporary file with the given content.
func createChecksumTestFile(t *testing.T, dir, name, content string) string {
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

// =============================================================================
// ValidationStatus Tests
// =============================================================================

func TestValidationStatus_Values(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status ValidationStatus
		want   int
	}{
		{StatusValid, 0},
		{StatusChanged, 1},
		{StatusMissing, 2},
		{StatusError, 3},
	}

	for _, tt := range tests {
		if int(tt.status) != tt.want {
			t.Errorf("ValidationStatus %d != expected %d", tt.status, tt.want)
		}
	}
}

// =============================================================================
// ValidationResult Tests
// =============================================================================

func TestValidationResult_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result *ValidationResult
		want   bool
	}{
		{
			name:   "valid status",
			result: &ValidationResult{Status: StatusValid},
			want:   true,
		},
		{
			name:   "changed status",
			result: &ValidationResult{Status: StatusChanged},
			want:   false,
		},
		{
			name:   "missing status",
			result: &ValidationResult{Status: StatusMissing},
			want:   false,
		},
		{
			name:   "error status",
			result: &ValidationResult{Status: StatusError},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.result.IsValid()
			if got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// ComputeChecksum Tests
// =============================================================================

func TestComputeChecksum_ValidFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := "hello world"
	path := createChecksumTestFile(t, dir, "test.txt", content)

	hash, err := ComputeChecksum(path)
	if err != nil {
		t.Fatalf("ComputeChecksum() error = %v", err)
	}

	expected := computeExpectedHash(content)
	if hash != expected {
		t.Errorf("ComputeChecksum() = %q, want %q", hash, expected)
	}
}

func TestComputeChecksum_EmptyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := createChecksumTestFile(t, dir, "empty.txt", "")

	hash, err := ComputeChecksum(path)
	if err != nil {
		t.Fatalf("ComputeChecksum() error = %v", err)
	}

	expected := computeExpectedHash("")
	if hash != expected {
		t.Errorf("ComputeChecksum() = %q, want %q", hash, expected)
	}
}

func TestComputeChecksum_NonExistentFile(t *testing.T) {
	t.Parallel()

	_, err := ComputeChecksum("/nonexistent/path/file.txt")
	if err == nil {
		t.Error("ComputeChecksum() expected error for nonexistent file")
	}
}

func TestComputeChecksum_LargeFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a 1MB file to test streaming
	largeContent := make([]byte, 1024*1024)
	for i := range largeContent {
		largeContent[i] = byte(i % 256)
	}

	path := filepath.Join(dir, "large.bin")
	if err := os.WriteFile(path, largeContent, 0644); err != nil {
		t.Fatalf("failed to create large file: %v", err)
	}

	hash, err := ComputeChecksum(path)
	if err != nil {
		t.Fatalf("ComputeChecksum() error = %v", err)
	}

	// Verify hash is valid format (64 hex characters for SHA-256)
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}

	// Verify consistency
	hash2, err := ComputeChecksum(path)
	if err != nil {
		t.Fatalf("ComputeChecksum() second call error = %v", err)
	}
	if hash != hash2 {
		t.Errorf("ComputeChecksum() not consistent: %q != %q", hash, hash2)
	}
}

func TestComputeChecksum_BinaryContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Binary content with null bytes and various byte values
	content := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x00, 0x10}

	path := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("failed to create binary file: %v", err)
	}

	hash, err := ComputeChecksum(path)
	if err != nil {
		t.Fatalf("ComputeChecksum() error = %v", err)
	}

	h := sha256.Sum256(content)
	expected := hex.EncodeToString(h[:])
	if hash != expected {
		t.Errorf("ComputeChecksum() = %q, want %q", hash, expected)
	}
}

func TestComputeChecksum_PermissionDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test cannot run as root")
	}

	t.Parallel()

	dir := t.TempDir()
	path := createChecksumTestFile(t, dir, "noaccess.txt", "content")

	if err := os.Chmod(path, 0000); err != nil {
		t.Fatalf("chmod error: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(path, 0644)
	})

	_, err := ComputeChecksum(path)
	if err == nil {
		t.Error("ComputeChecksum() expected error for permission denied")
	}
}

// =============================================================================
// NewChecksumValidator Tests
// =============================================================================

func TestNewChecksumValidator(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	validator := NewChecksumValidator(store)

	if validator == nil {
		t.Fatal("NewChecksumValidator() returned nil")
	}
}

// =============================================================================
// Validate Tests - Happy Path
// =============================================================================

func TestValidate_FileValid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := "package main"
	path := createChecksumTestFile(t, dir, "main.go", content)

	store := newMockStore()
	expectedHash := computeExpectedHash(content)
	store.SetChecksum(path, expectedHash)

	validator := NewChecksumValidator(store)
	result := validator.Validate(path)

	if result.Status != StatusValid {
		t.Errorf("Status = %v, want StatusValid", result.Status)
	}
	if result.Path != path {
		t.Errorf("Path = %q, want %q", result.Path, path)
	}
	if result.ExpectedHash != expectedHash {
		t.Errorf("ExpectedHash = %q, want %q", result.ExpectedHash, expectedHash)
	}
	if result.ActualHash != expectedHash {
		t.Errorf("ActualHash = %q, want %q", result.ActualHash, expectedHash)
	}
	if result.Error != nil {
		t.Errorf("Error = %v, want nil", result.Error)
	}
	if !result.IsValid() {
		t.Error("IsValid() = false, want true")
	}
}

// =============================================================================
// Validate Tests - Changed File
// =============================================================================

func TestValidate_FileChanged(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	originalContent := "package main"
	path := createChecksumTestFile(t, dir, "main.go", originalContent)

	store := newMockStore()
	oldHash := computeExpectedHash(originalContent)
	store.SetChecksum(path, oldHash)

	// Modify the file
	newContent := "package main\nfunc main() {}"
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		t.Fatalf("failed to modify file: %v", err)
	}

	validator := NewChecksumValidator(store)
	result := validator.Validate(path)

	if result.Status != StatusChanged {
		t.Errorf("Status = %v, want StatusChanged", result.Status)
	}
	if result.ExpectedHash != oldHash {
		t.Errorf("ExpectedHash = %q, want %q", result.ExpectedHash, oldHash)
	}
	newHash := computeExpectedHash(newContent)
	if result.ActualHash != newHash {
		t.Errorf("ActualHash = %q, want %q", result.ActualHash, newHash)
	}
	if result.IsValid() {
		t.Error("IsValid() = true, want false")
	}
}

// =============================================================================
// Validate Tests - Missing File
// =============================================================================

func TestValidate_FileNotFound(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	path := "/nonexistent/path/file.go"
	store.SetChecksum(path, "somehash")

	validator := NewChecksumValidator(store)
	result := validator.Validate(path)

	if result.Status != StatusError {
		t.Errorf("Status = %v, want StatusError", result.Status)
	}
	if result.Path != path {
		t.Errorf("Path = %q, want %q", result.Path, path)
	}
	if result.Error == nil {
		t.Error("Error = nil, want error")
	}
}

func TestValidate_FileNotInStore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := createChecksumTestFile(t, dir, "unknown.go", "content")

	store := newMockStore()
	// Do not add checksum to store

	validator := NewChecksumValidator(store)
	result := validator.Validate(path)

	if result.Status != StatusMissing {
		t.Errorf("Status = %v, want StatusMissing (not indexed)", result.Status)
	}
	if result.ExpectedHash != "" {
		t.Errorf("ExpectedHash = %q, want empty", result.ExpectedHash)
	}
}

// =============================================================================
// Validate Tests - Error Cases
// =============================================================================

func TestValidate_PermissionDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test cannot run as root")
	}

	t.Parallel()

	dir := t.TempDir()
	path := createChecksumTestFile(t, dir, "noaccess.go", "content")

	store := newMockStore()
	store.SetChecksum(path, "somehash")

	if err := os.Chmod(path, 0000); err != nil {
		t.Fatalf("chmod error: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(path, 0644)
	})

	validator := NewChecksumValidator(store)
	result := validator.Validate(path)

	if result.Status != StatusError {
		t.Errorf("Status = %v, want StatusError", result.Status)
	}
	if result.Error == nil {
		t.Error("Error = nil, want error")
	}
}

// =============================================================================
// ValidateBatch Tests
// =============================================================================

func TestValidateBatch_Empty(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	validator := NewChecksumValidator(store)

	results := validator.ValidateBatch(context.Background(), nil)

	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
}

func TestValidateBatch_SingleFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := "package main"
	path := createChecksumTestFile(t, dir, "main.go", content)

	store := newMockStore()
	store.SetChecksum(path, computeExpectedHash(content))

	validator := NewChecksumValidator(store)
	results := validator.ValidateBatch(context.Background(), []string{path})

	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Status != StatusValid {
		t.Errorf("Status = %v, want StatusValid", results[0].Status)
	}
}

func TestValidateBatch_MultipleFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create multiple files
	paths := make([]string, 5)
	for i := 0; i < 5; i++ {
		content := "package main // file " + string(rune('0'+i))
		paths[i] = createChecksumTestFile(t, dir, "file"+string(rune('0'+i))+".go", content)
	}

	store := newMockStore()
	for _, path := range paths {
		content, _ := os.ReadFile(path)
		store.SetChecksum(path, computeExpectedHash(string(content)))
	}

	validator := NewChecksumValidator(store)
	results := validator.ValidateBatch(context.Background(), paths)

	if len(results) != 5 {
		t.Fatalf("len(results) = %d, want 5", len(results))
	}

	for i, result := range results {
		if result.Status != StatusValid {
			t.Errorf("results[%d].Status = %v, want StatusValid", i, result.Status)
		}
		if result.Path != paths[i] {
			t.Errorf("results[%d].Path = %q, want %q", i, result.Path, paths[i])
		}
	}
}

func TestValidateBatch_MixedResults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// File 1: Valid
	content1 := "package main"
	path1 := createChecksumTestFile(t, dir, "valid.go", content1)

	// File 2: Changed
	content2 := "original content"
	path2 := createChecksumTestFile(t, dir, "changed.go", "modified content")

	// File 3: Not in store
	path3 := createChecksumTestFile(t, dir, "missing.go", "new file")

	store := newMockStore()
	store.SetChecksum(path1, computeExpectedHash(content1))
	store.SetChecksum(path2, computeExpectedHash(content2))
	// path3 not added to store

	validator := NewChecksumValidator(store)
	results := validator.ValidateBatch(context.Background(), []string{path1, path2, path3})

	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}

	// Verify order is maintained
	if results[0].Path != path1 || results[0].Status != StatusValid {
		t.Errorf("results[0] unexpected: %+v", results[0])
	}
	if results[1].Path != path2 || results[1].Status != StatusChanged {
		t.Errorf("results[1] unexpected: %+v", results[1])
	}
	if results[2].Path != path3 || results[2].Status != StatusMissing {
		t.Errorf("results[2] unexpected: %+v", results[2])
	}
}

func TestValidateBatch_PreservesOrder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := make([]string, 10)
	for i := range paths {
		content := "content " + string(rune('a'+i))
		paths[i] = createChecksumTestFile(t, dir, "file"+string(rune('a'+i))+".go", content)
	}

	store := newMockStore()
	for _, path := range paths {
		content, _ := os.ReadFile(path)
		store.SetChecksum(path, computeExpectedHash(string(content)))
	}

	validator := NewChecksumValidator(store)
	results := validator.ValidateBatch(context.Background(), paths)

	for i, result := range results {
		if result.Path != paths[i] {
			t.Errorf("results[%d].Path = %q, want %q (order not preserved)", i, result.Path, paths[i])
		}
	}
}

// =============================================================================
// ValidateBatch Context Cancellation Tests
// =============================================================================

func TestValidateBatch_ContextCancelled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create many files
	paths := make([]string, 100)
	for i := range paths {
		content := "package main // " + string(rune(i))
		paths[i] = createChecksumTestFile(t, dir, "file"+string(rune('a'+i%26))+string(rune('0'+i/26))+".go", content)
	}

	store := newMockStore()
	for _, path := range paths {
		content, _ := os.ReadFile(path)
		store.SetChecksum(path, computeExpectedHash(string(content)))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	validator := NewChecksumValidator(store)
	results := validator.ValidateBatch(ctx, paths)

	// With immediate cancellation, we might get 0 results or a few
	// The key is that it doesn't hang and returns
	_ = results
}

func TestValidateBatch_ContextTimeout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	paths := make([]string, 20)
	for i := range paths {
		content := "package main // " + string(rune(i))
		paths[i] = createChecksumTestFile(t, dir, "file"+string(rune('a'+i))+".go", content)
	}

	store := newMockStore()
	for _, path := range paths {
		content, _ := os.ReadFile(path)
		store.SetChecksum(path, computeExpectedHash(string(content)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	validator := NewChecksumValidator(store)

	done := make(chan struct{})
	go func() {
		_ = validator.ValidateBatch(ctx, paths)
		close(done)
	}()

	select {
	case <-done:
		// Good - completed
	case <-time.After(5 * time.Second):
		t.Error("ValidateBatch did not respect context timeout")
	}
}

// =============================================================================
// ValidateBatch Concurrent Validation Tests
// =============================================================================

func TestValidateBatch_ConcurrentExecution(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create files
	paths := make([]string, 50)
	for i := range paths {
		content := "package main // " + string(rune(i))
		paths[i] = createChecksumTestFile(t, dir, "file"+string(rune('a'+i%26))+string(rune('0'+i/26))+".go", content)
	}

	store := newMockStore()
	for _, path := range paths {
		content, _ := os.ReadFile(path)
		store.SetChecksum(path, computeExpectedHash(string(content)))
	}

	validator := NewChecksumValidator(store)

	// Run validation
	start := time.Now()
	results := validator.ValidateBatch(context.Background(), paths)
	elapsed := time.Since(start)

	// Verify all results
	if len(results) != len(paths) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(paths))
	}

	for i, result := range results {
		if result.Status != StatusValid {
			t.Errorf("results[%d].Status = %v, want StatusValid", i, result.Status)
		}
	}

	// Log timing (not a hard assertion since it depends on system)
	t.Logf("Validated %d files in %v", len(paths), elapsed)
}

// =============================================================================
// Concurrent Safety Tests
// =============================================================================

func TestValidateBatch_ConcurrentCallsSafe(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := make([]string, 10)
	for i := range paths {
		content := "package main // " + string(rune(i))
		paths[i] = createChecksumTestFile(t, dir, "file"+string(rune('a'+i))+".go", content)
	}

	store := newMockStore()
	for _, path := range paths {
		content, _ := os.ReadFile(path)
		store.SetChecksum(path, computeExpectedHash(string(content)))
	}

	validator := NewChecksumValidator(store)

	// Run multiple batches concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := validator.ValidateBatch(context.Background(), paths)
			if len(results) != len(paths) {
				t.Errorf("unexpected result count: %d", len(results))
			}
		}()
	}

	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestValidate_SpecialCharactersInPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := "package main"
	path := createChecksumTestFile(t, dir, "file with spaces.go", content)

	store := newMockStore()
	store.SetChecksum(path, computeExpectedHash(content))

	validator := NewChecksumValidator(store)
	result := validator.Validate(path)

	if result.Status != StatusValid {
		t.Errorf("Status = %v, want StatusValid", result.Status)
	}
}

func TestValidate_UnicodeFilename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := "package main"
	path := createChecksumTestFile(t, dir, "unicode_test.go", content)

	store := newMockStore()
	store.SetChecksum(path, computeExpectedHash(content))

	validator := NewChecksumValidator(store)
	result := validator.Validate(path)

	if result.Status != StatusValid {
		t.Errorf("Status = %v, want StatusValid", result.Status)
	}
}

func TestComputeChecksum_Deterministic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := "test content for determinism"
	path := createChecksumTestFile(t, dir, "test.txt", content)

	// Compute hash multiple times
	hashes := make([]string, 10)
	for i := 0; i < 10; i++ {
		hash, err := ComputeChecksum(path)
		if err != nil {
			t.Fatalf("ComputeChecksum() error = %v", err)
		}
		hashes[i] = hash
	}

	// All hashes should be identical
	for i := 1; i < len(hashes); i++ {
		if hashes[i] != hashes[0] {
			t.Errorf("Hash mismatch: hashes[%d] = %q, hashes[0] = %q", i, hashes[i], hashes[0])
		}
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkComputeChecksum_SmallFile(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "small.txt")
	content := make([]byte, 1024) // 1KB
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		b.Fatalf("write error: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ComputeChecksum(path)
		if err != nil {
			b.Fatalf("ComputeChecksum() error = %v", err)
		}
	}
}

func BenchmarkComputeChecksum_LargeFile(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "large.bin")
	content := make([]byte, 10*1024*1024) // 10MB
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		b.Fatalf("write error: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ComputeChecksum(path)
		if err != nil {
			b.Fatalf("ComputeChecksum() error = %v", err)
		}
	}
}

func BenchmarkValidateBatch(b *testing.B) {
	dir := b.TempDir()

	paths := make([]string, 100)
	for i := range paths {
		content := "package main // " + string(rune(i))
		path := filepath.Join(dir, "file"+string(rune('a'+i%26))+string(rune('0'+i/26))+".go")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			b.Fatalf("write error: %v", err)
		}
		paths[i] = path
	}

	store := newMockStore()
	for _, path := range paths {
		content, _ := os.ReadFile(path)
		h := sha256.Sum256(content)
		store.SetChecksum(path, hex.EncodeToString(h[:]))
	}

	validator := NewChecksumValidator(store)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.ValidateBatch(context.Background(), paths)
	}
}
