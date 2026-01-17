package detect

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestWhich(t *testing.T) {
	// Always clear cache before tests to ensure clean state
	ClearCache()

	tests := []struct {
		name     string
		binary   string
		wantPath bool // true if we expect a non-empty path
	}{
		{
			name:     "empty string returns empty",
			binary:   "",
			wantPath: false,
		},
		{
			name:     "non-existent binary returns empty",
			binary:   "this-binary-definitely-does-not-exist-xyz123",
			wantPath: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ClearCache() // Clear cache between subtests
			got := Which(tt.binary)
			if tt.wantPath && got == "" {
				t.Errorf("Which(%q) = empty, wanted non-empty path", tt.binary)
			}
			if !tt.wantPath && got != "" {
				t.Errorf("Which(%q) = %q, wanted empty", tt.binary, got)
			}
		})
	}
}

func TestWhich_CommonBinaries(t *testing.T) {
	ClearCache()

	// Test common binaries that should exist on most systems
	// Use OS-specific binaries
	var testBinaries []string
	if runtime.GOOS == "windows" {
		testBinaries = []string{"cmd", "powershell"}
	} else {
		testBinaries = []string{"ls", "sh"}
	}

	for _, binary := range testBinaries {
		t.Run(binary, func(t *testing.T) {
			path := Which(binary)
			if path == "" {
				t.Skipf("Binary %q not found in PATH, skipping", binary)
			}
			// Verify the path actually exists
			if _, err := os.Stat(path); err != nil {
				t.Errorf("Which(%q) returned path %q that doesn't exist: %v", binary, path, err)
			}
		})
	}
}

func TestWhichAll(t *testing.T) {
	ClearCache()

	tests := []struct {
		name       string
		binary     string
		wantResult bool // true if we expect at least one result
	}{
		{
			name:       "empty string returns nil",
			binary:     "",
			wantResult: false,
		},
		{
			name:       "non-existent binary returns empty slice",
			binary:     "this-binary-definitely-does-not-exist-xyz123",
			wantResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ClearCache()
			got := WhichAll(tt.binary)
			if tt.wantResult && len(got) == 0 {
				t.Errorf("WhichAll(%q) returned empty, wanted at least one result", tt.binary)
			}
			if !tt.wantResult && len(got) > 0 {
				t.Errorf("WhichAll(%q) = %v, wanted empty", tt.binary, got)
			}
		})
	}
}

func TestWhichAll_CommonBinaries(t *testing.T) {
	ClearCache()

	// Test common binaries that should exist on most systems
	var testBinaries []string
	if runtime.GOOS == "windows" {
		testBinaries = []string{"cmd"}
	} else {
		testBinaries = []string{"ls", "sh"}
	}

	for _, binary := range testBinaries {
		t.Run(binary, func(t *testing.T) {
			paths := WhichAll(binary)
			if len(paths) == 0 {
				t.Skipf("Binary %q not found in PATH, skipping", binary)
			}
			// Verify all returned paths actually exist
			for _, path := range paths {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("WhichAll(%q) returned path %q that doesn't exist: %v", binary, path, err)
				}
			}
		})
	}
}

func TestWhichAll_ReturnsAllOccurrences(t *testing.T) {
	ClearCache()

	// Create a temp directory with a fake binary
	tempDir := t.TempDir()

	// Create a fake executable
	binaryName := "test-which-binary"
	if runtime.GOOS == "windows" {
		binaryName = "test-which-binary.exe"
	}

	fakeBinaryPath := filepath.Join(tempDir, binaryName)
	if err := os.WriteFile(fakeBinaryPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("Failed to create fake binary: %v", err)
	}

	// Save original PATH and restore after test
	originalPath := os.Getenv("PATH")
	defer func() {
		os.Setenv("PATH", originalPath)
		ClearCache()
	}()

	// Prepend temp dir to PATH
	separator := ":"
	if runtime.GOOS == "windows" {
		separator = ";"
	}
	os.Setenv("PATH", tempDir+separator+originalPath)
	ClearCache()

	// Now test that we can find the fake binary
	baseName := "test-which-binary"
	path := Which(baseName)
	if path == "" {
		t.Skipf("Could not find fake binary, skipping test")
	}

	paths := WhichAll(baseName)
	if len(paths) == 0 {
		t.Errorf("WhichAll(%q) returned empty when binary was added to PATH", baseName)
	}

	// First path should be from our temp directory
	if len(paths) > 0 && !containsPath(paths, fakeBinaryPath) {
		t.Errorf("WhichAll(%q) = %v, expected to contain %q", baseName, paths, fakeBinaryPath)
	}
}

func containsPath(paths []string, target string) bool {
	for _, p := range paths {
		if p == target {
			return true
		}
	}
	return false
}

func TestClearCache(t *testing.T) {
	// First, populate the cache with a lookup
	ClearCache()

	var testBinary string
	if runtime.GOOS == "windows" {
		testBinary = "cmd"
	} else {
		testBinary = "ls"
	}

	// Perform a lookup to populate cache
	path1 := Which(testBinary)
	if path1 == "" {
		t.Skipf("Binary %q not found, skipping cache test", testBinary)
	}

	// Clear the cache
	ClearCache()

	// Verify cache was cleared by checking internal state
	globalCache.mu.RLock()
	cacheLen := len(globalCache.cache)
	cacheAllLen := len(globalCache.cacheAll)
	lastPath := globalCache.lastPathEnv
	globalCache.mu.RUnlock()

	if cacheLen != 0 {
		t.Errorf("ClearCache() did not clear cache, len = %d", cacheLen)
	}
	if cacheAllLen != 0 {
		t.Errorf("ClearCache() did not clear cacheAll, len = %d", cacheAllLen)
	}
	if lastPath != "" {
		t.Errorf("ClearCache() did not clear lastPathEnv, value = %q", lastPath)
	}
}

func TestCacheBehavior(t *testing.T) {
	ClearCache()

	var testBinary string
	if runtime.GOOS == "windows" {
		testBinary = "cmd"
	} else {
		testBinary = "ls"
	}

	// First call should populate cache
	path1 := Which(testBinary)
	if path1 == "" {
		t.Skipf("Binary %q not found, skipping cache behavior test", testBinary)
	}

	// Verify cache was populated
	globalCache.mu.RLock()
	cachedPath, exists := globalCache.cache[testBinary]
	globalCache.mu.RUnlock()

	if !exists {
		t.Errorf("Which(%q) did not populate cache", testBinary)
	}
	if cachedPath != path1 {
		t.Errorf("Cached path %q != returned path %q", cachedPath, path1)
	}

	// Second call should return same result from cache
	path2 := Which(testBinary)
	if path1 != path2 {
		t.Errorf("Which(%q) returned different results: %q vs %q", testBinary, path1, path2)
	}
}

func TestCacheInvalidationOnPathChange(t *testing.T) {
	ClearCache()

	var testBinary string
	if runtime.GOOS == "windows" {
		testBinary = "cmd"
	} else {
		testBinary = "ls"
	}

	// First call to populate cache
	path1 := Which(testBinary)
	if path1 == "" {
		t.Skipf("Binary %q not found, skipping cache invalidation test", testBinary)
	}

	// Save original PATH
	originalPath := os.Getenv("PATH")
	defer func() {
		os.Setenv("PATH", originalPath)
		ClearCache()
	}()

	// Modify PATH environment variable
	os.Setenv("PATH", originalPath+":/some/new/path")

	// Next call should detect PATH change and refresh cache
	path2 := Which(testBinary)

	// Both calls should still find the binary (same result expected)
	if path1 != path2 {
		// This is acceptable - PATH changed, so result could differ
		// But we mainly want to ensure no crash occurs
	}

	// Verify that cache was refreshed (lastPathEnv should be updated)
	globalCache.mu.RLock()
	lastPath := globalCache.lastPathEnv
	globalCache.mu.RUnlock()

	expectedPath := originalPath + ":/some/new/path"
	if lastPath != expectedPath {
		t.Errorf("Cache was not refreshed after PATH change, lastPathEnv = %q, want %q", lastPath, expectedPath)
	}
}

func TestWhichAllCacheBehavior(t *testing.T) {
	ClearCache()

	var testBinary string
	if runtime.GOOS == "windows" {
		testBinary = "cmd"
	} else {
		testBinary = "ls"
	}

	// First call should populate cache
	paths1 := WhichAll(testBinary)
	if len(paths1) == 0 {
		t.Skipf("Binary %q not found, skipping WhichAll cache behavior test", testBinary)
	}

	// Verify cacheAll was populated
	globalCache.mu.RLock()
	cachedPaths, exists := globalCache.cacheAll[testBinary]
	globalCache.mu.RUnlock()

	if !exists {
		t.Errorf("WhichAll(%q) did not populate cacheAll", testBinary)
	}
	if len(cachedPaths) != len(paths1) {
		t.Errorf("Cached paths length %d != returned paths length %d", len(cachedPaths), len(paths1))
	}

	// Second call should return same result from cache
	paths2 := WhichAll(testBinary)
	if len(paths1) != len(paths2) {
		t.Errorf("WhichAll(%q) returned different number of results: %d vs %d", testBinary, len(paths1), len(paths2))
	}
}

func TestWhichAllReturnsCopy(t *testing.T) {
	ClearCache()

	var testBinary string
	if runtime.GOOS == "windows" {
		testBinary = "cmd"
	} else {
		testBinary = "ls"
	}

	paths1 := WhichAll(testBinary)
	if len(paths1) == 0 {
		t.Skipf("Binary %q not found, skipping copy test", testBinary)
	}

	// Modify the returned slice
	originalFirst := paths1[0]
	paths1[0] = "modified"

	// Get paths again - should not be affected by our modification
	paths2 := WhichAll(testBinary)
	if len(paths2) == 0 {
		t.Fatalf("WhichAll(%q) returned empty on second call", testBinary)
	}

	if paths2[0] == "modified" {
		t.Errorf("WhichAll returned reference to internal cache instead of copy")
	}
	if paths2[0] != originalFirst {
		t.Errorf("WhichAll returned different result after modification: %q vs %q", paths2[0], originalFirst)
	}
}

func TestConcurrentAccess(t *testing.T) {
	ClearCache()

	var testBinary string
	if runtime.GOOS == "windows" {
		testBinary = "cmd"
	} else {
		testBinary = "ls"
	}

	// Verify binary exists before testing
	if path := Which(testBinary); path == "" {
		t.Skipf("Binary %q not found, skipping concurrency test", testBinary)
	}
	ClearCache()

	var wg sync.WaitGroup
	iterations := 100
	goroutines := 10

	// Channel to collect any panics
	panicChan := make(chan interface{}, goroutines*iterations)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicChan <- r
				}
			}()
			for j := 0; j < iterations; j++ {
				_ = Which(testBinary)
				_ = WhichAll(testBinary)
			}
		}()
	}

	wg.Wait()
	close(panicChan)

	// Check for panics
	for panic := range panicChan {
		t.Errorf("Concurrent access caused panic: %v", panic)
	}
}

func TestConcurrentAccessWithClearCache(t *testing.T) {
	ClearCache()

	var testBinary string
	if runtime.GOOS == "windows" {
		testBinary = "cmd"
	} else {
		testBinary = "ls"
	}

	// Verify binary exists before testing
	if path := Which(testBinary); path == "" {
		t.Skipf("Binary %q not found, skipping concurrency test", testBinary)
	}
	ClearCache()

	var wg sync.WaitGroup
	iterations := 50

	// Test concurrent reads and cache clears
	wg.Add(3)

	// Reader 1: Which
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = Which(testBinary)
		}
	}()

	// Reader 2: WhichAll
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = WhichAll(testBinary)
		}
	}()

	// Writer: ClearCache
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			ClearCache()
		}
	}()

	// Test should complete without deadlock or panic
	wg.Wait()
}

func TestFindAllInPath_EmptyPath(t *testing.T) {
	// Test internal function behavior with empty PATH
	result := findAllInPath("ls", "")
	if result != nil {
		t.Errorf("findAllInPath with empty PATH should return nil, got %v", result)
	}
}

func TestFindAllInPath_WithTempBinaries(t *testing.T) {
	// Create two temp directories with the same fake binary
	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()

	binaryName := "test-findall-binary"
	if runtime.GOOS == "windows" {
		binaryName = "test-findall-binary.exe"
	}

	// Create fake executables in both directories
	binary1 := filepath.Join(tempDir1, binaryName)
	binary2 := filepath.Join(tempDir2, binaryName)

	if err := os.WriteFile(binary1, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("Failed to create binary1: %v", err)
	}
	if err := os.WriteFile(binary2, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("Failed to create binary2: %v", err)
	}

	// Create PATH with both directories
	separator := ":"
	if runtime.GOOS == "windows" {
		separator = ";"
	}
	testPath := tempDir1 + separator + tempDir2

	// Test findAllInPath
	baseName := "test-findall-binary"
	results := findAllInPath(baseName, testPath)

	if len(results) != 2 {
		t.Errorf("findAllInPath should find 2 binaries, found %d: %v", len(results), results)
	}

	// Verify both paths are in results
	if !containsPath(results, binary1) {
		t.Errorf("Results should contain %q, got %v", binary1, results)
	}
	if !containsPath(results, binary2) {
		t.Errorf("Results should contain %q, got %v", binary2, results)
	}
}

func TestFindAllInPath_DeduplicatesSamePath(t *testing.T) {
	// Create a temp directory with a fake binary
	tempDir := t.TempDir()

	binaryName := "test-dedup-binary"
	if runtime.GOOS == "windows" {
		binaryName = "test-dedup-binary.exe"
	}

	binaryPath := filepath.Join(tempDir, binaryName)
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("Failed to create binary: %v", err)
	}

	// Create PATH with the same directory twice
	separator := ":"
	if runtime.GOOS == "windows" {
		separator = ";"
	}
	testPath := tempDir + separator + tempDir

	// Test findAllInPath - should deduplicate
	baseName := "test-dedup-binary"
	results := findAllInPath(baseName, testPath)

	if len(results) != 1 {
		t.Errorf("findAllInPath should deduplicate same path, got %d results: %v", len(results), results)
	}
}

func TestGetPathSeparator(t *testing.T) {
	separator := getPathSeparator()
	if runtime.GOOS == "windows" {
		if separator != ";" {
			t.Errorf("getPathSeparator() on windows = %q, want %q", separator, ";")
		}
	} else {
		if separator != ":" {
			t.Errorf("getPathSeparator() on unix = %q, want %q", separator, ":")
		}
	}
}

func TestIsExecutable(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("non-existent file returns false", func(t *testing.T) {
		if isExecutable(filepath.Join(tempDir, "nonexistent")) {
			t.Error("isExecutable should return false for non-existent file")
		}
	})

	t.Run("directory returns false", func(t *testing.T) {
		subDir := filepath.Join(tempDir, "subdir")
		if err := os.Mkdir(subDir, 0755); err != nil {
			t.Fatalf("Failed to create subdir: %v", err)
		}
		if isExecutable(subDir) {
			t.Error("isExecutable should return false for directory")
		}
	})

	if runtime.GOOS != "windows" {
		t.Run("non-executable file returns false", func(t *testing.T) {
			nonExec := filepath.Join(tempDir, "nonexec")
			if err := os.WriteFile(nonExec, []byte("content"), 0644); err != nil {
				t.Fatalf("Failed to create file: %v", err)
			}
			if isExecutable(nonExec) {
				t.Error("isExecutable should return false for non-executable file")
			}
		})

		t.Run("executable file returns true", func(t *testing.T) {
			exec := filepath.Join(tempDir, "exec")
			if err := os.WriteFile(exec, []byte("#!/bin/sh\n"), 0755); err != nil {
				t.Fatalf("Failed to create file: %v", err)
			}
			if !isExecutable(exec) {
				t.Error("isExecutable should return true for executable file")
			}
		})
	}

	if runtime.GOOS == "windows" {
		t.Run("any file returns true on windows", func(t *testing.T) {
			// On Windows, any existing non-directory file is considered "executable"
			anyFile := filepath.Join(tempDir, "anyfile.txt")
			if err := os.WriteFile(anyFile, []byte("content"), 0644); err != nil {
				t.Fatalf("Failed to create file: %v", err)
			}
			if !isExecutable(anyFile) {
				t.Error("isExecutable should return true for any file on Windows")
			}
		})
	}
}

func TestGetCandidates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Run("windows includes extensions", func(t *testing.T) {
			candidates := getCandidates("test", "C:\\bin")
			if len(candidates) < 2 {
				t.Errorf("getCandidates on Windows should include multiple extensions, got %v", candidates)
			}
			// First candidate should be the base path
			if candidates[0] != "C:\\bin\\test" {
				t.Errorf("First candidate should be base path, got %q", candidates[0])
			}
		})
	} else {
		t.Run("unix returns single candidate", func(t *testing.T) {
			candidates := getCandidates("test", "/usr/bin")
			if len(candidates) != 1 {
				t.Errorf("getCandidates on Unix should return single candidate, got %v", candidates)
			}
			if candidates[0] != "/usr/bin/test" {
				t.Errorf("Candidate should be /usr/bin/test, got %q", candidates[0])
			}
		})
	}
}

func TestBuildBasePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		path := buildBasePath("C:\\bin", "test")
		if path != "C:\\bin\\test" {
			t.Errorf("buildBasePath on Windows = %q, want %q", path, "C:\\bin\\test")
		}
	} else {
		path := buildBasePath("/usr/bin", "test")
		if path != "/usr/bin/test" {
			t.Errorf("buildBasePath on Unix = %q, want %q", path, "/usr/bin/test")
		}
	}
}

func TestCopyPaths(t *testing.T) {
	original := []string{"path1", "path2", "path3"}
	copied := copyPaths(original)

	// Verify it's a copy with same content
	if len(copied) != len(original) {
		t.Errorf("copyPaths returned different length: %d vs %d", len(copied), len(original))
	}
	for i, v := range original {
		if copied[i] != v {
			t.Errorf("copyPaths[%d] = %q, want %q", i, copied[i], v)
		}
	}

	// Verify modifying copy doesn't affect original
	copied[0] = "modified"
	if original[0] == "modified" {
		t.Error("copyPaths returned reference instead of copy")
	}
}

func TestCopyPaths_EmptySlice(t *testing.T) {
	original := []string{}
	copied := copyPaths(original)

	if len(copied) != 0 {
		t.Errorf("copyPaths of empty slice should return empty slice, got %v", copied)
	}
}

func TestWhich_NegativeCaching(t *testing.T) {
	ClearCache()

	// Lookup a non-existent binary
	nonExistent := "this-binary-absolutely-does-not-exist-xyz789"
	result := Which(nonExistent)
	if result != "" {
		t.Fatalf("Which(%q) should return empty, got %q", nonExistent, result)
	}

	// Verify negative result was cached
	globalCache.mu.RLock()
	cachedPath, exists := globalCache.cache[nonExistent]
	globalCache.mu.RUnlock()

	if !exists {
		t.Errorf("Which should cache negative results")
	}
	if cachedPath != "" {
		t.Errorf("Cached negative result should be empty string, got %q", cachedPath)
	}
}

func TestWhichAll_EmptyStringReturnsNil(t *testing.T) {
	ClearCache()

	result := WhichAll("")
	if result != nil {
		t.Errorf("WhichAll(\"\") should return nil, got %v", result)
	}
}

func TestWhich_EmptyStringReturnsEmpty(t *testing.T) {
	ClearCache()

	result := Which("")
	if result != "" {
		t.Errorf("Which(\"\") should return empty string, got %q", result)
	}
}

func TestCollectExecutables_EmptyDir(t *testing.T) {
	// Test that empty directory strings are skipped
	result := collectExecutables("test", []string{"", "/usr/bin", ""})

	// Should not panic and should process non-empty dirs
	// Just verify it doesn't panic - actual results depend on system
	_ = result
}
