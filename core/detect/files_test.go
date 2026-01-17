package detect

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileExists(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T) (string, []string)
		expected bool
	}{
		{
			name: "existing file returns true",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				testFile := filepath.Join(dir, "test.txt")
				if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return dir, []string{"test.txt"}
			},
			expected: true,
		},
		{
			name: "non-existing file returns false",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				return dir, []string{"nonexistent.txt"}
			},
			expected: false,
		},
		{
			name: "empty root returns false",
			setup: func(t *testing.T) (string, []string) {
				return "", []string{"test.txt"}
			},
			expected: false,
		},
		{
			name: "empty files list returns false",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				return dir, []string{}
			},
			expected: false,
		},
		{
			name: "directory instead of file returns false",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				subDir := filepath.Join(dir, "subdir")
				if err := os.Mkdir(subDir, 0755); err != nil {
					t.Fatalf("failed to create subdirectory: %v", err)
				}
				return dir, []string{"subdir"}
			},
			expected: false,
		},
		{
			name: "one of multiple files exists returns true",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				testFile := filepath.Join(dir, "exists.txt")
				if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return dir, []string{"missing1.txt", "exists.txt", "missing2.txt"}
			},
			expected: true,
		},
		{
			name: "none of multiple files exist returns false",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				return dir, []string{"missing1.txt", "missing2.txt", "missing3.txt"}
			},
			expected: false,
		},
		{
			name: "absolute file path works",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				testFile := filepath.Join(dir, "absolute.txt")
				if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return dir, []string{testFile}
			},
			expected: true,
		},
		{
			name: "file in subdirectory",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				subDir := filepath.Join(dir, "subdir")
				if err := os.Mkdir(subDir, 0755); err != nil {
					t.Fatalf("failed to create subdirectory: %v", err)
				}
				testFile := filepath.Join(subDir, "nested.txt")
				if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return dir, []string{"subdir/nested.txt"}
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, files := tt.setup(t)
			result := FileExists(root, files...)
			if result != tt.expected {
				t.Errorf("FileExists() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFindUp(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T) (startDir string, filename string)
		expectErr   error
		expectFound bool
	}{
		{
			name: "finds file in current directory",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				testFile := filepath.Join(dir, "target.txt")
				if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return dir, "target.txt"
			},
			expectErr:   nil,
			expectFound: true,
		},
		{
			name: "finds file in parent directory",
			setup: func(t *testing.T) (string, string) {
				parentDir := t.TempDir()
				childDir := filepath.Join(parentDir, "child")
				if err := os.Mkdir(childDir, 0755); err != nil {
					t.Fatalf("failed to create child directory: %v", err)
				}
				testFile := filepath.Join(parentDir, "parent-file.txt")
				if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return childDir, "parent-file.txt"
			},
			expectErr:   nil,
			expectFound: true,
		},
		{
			name: "finds file in grandparent directory",
			setup: func(t *testing.T) (string, string) {
				grandparentDir := t.TempDir()
				parentDir := filepath.Join(grandparentDir, "parent")
				childDir := filepath.Join(parentDir, "child")
				if err := os.MkdirAll(childDir, 0755); err != nil {
					t.Fatalf("failed to create directories: %v", err)
				}
				testFile := filepath.Join(grandparentDir, "grandparent-file.txt")
				if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return childDir, "grandparent-file.txt"
			},
			expectErr:   nil,
			expectFound: true,
		},
		{
			name: "returns ErrFileNotFound when file does not exist",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				return dir, "nonexistent.txt"
			},
			expectErr:   ErrFileNotFound,
			expectFound: false,
		},
		{
			name: "returns ErrInvalidPath for empty startDir",
			setup: func(t *testing.T) (string, string) {
				return "", "test.txt"
			},
			expectErr:   ErrInvalidPath,
			expectFound: false,
		},
		{
			name: "returns ErrNoFilesSpecified for empty filename",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				return dir, ""
			},
			expectErr:   ErrNoFilesSpecified,
			expectFound: false,
		},
		{
			name: "startDir is a file resolves to parent directory",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				startFile := filepath.Join(dir, "start.txt")
				if err := os.WriteFile(startFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create start file: %v", err)
				}
				targetFile := filepath.Join(dir, "target.txt")
				if err := os.WriteFile(targetFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create target file: %v", err)
				}
				return startFile, "target.txt"
			},
			expectErr:   nil,
			expectFound: true,
		},
		{
			name: "returns ErrInvalidPath for non-existent startDir",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				return filepath.Join(dir, "nonexistent", "path"), "test.txt"
			},
			expectErr:   ErrInvalidPath,
			expectFound: false,
		},
		{
			name: "ignores directory with same name as target file",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				// Create a directory with the target name
				targetDir := filepath.Join(dir, "target")
				if err := os.Mkdir(targetDir, 0755); err != nil {
					t.Fatalf("failed to create target directory: %v", err)
				}
				return dir, "target"
			},
			expectErr:   ErrFileNotFound,
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			startDir, filename := tt.setup(t)
			path, err := FindUp(startDir, filename)

			if tt.expectErr != nil {
				if !errors.Is(err, tt.expectErr) {
					t.Errorf("FindUp() error = %v, want %v", err, tt.expectErr)
				}
				return
			}

			if err != nil {
				t.Errorf("FindUp() unexpected error = %v", err)
				return
			}

			if tt.expectFound {
				if path == "" {
					t.Error("FindUp() returned empty path when expecting found file")
				}
				if filepath.Base(path) != filename {
					t.Errorf("FindUp() returned path with base = %v, want %v", filepath.Base(path), filename)
				}
				// Verify the file actually exists at the returned path
				if _, err := os.Stat(path); err != nil {
					t.Errorf("FindUp() returned path that does not exist: %v", path)
				}
			}
		})
	}
}

func TestFindUpAny(t *testing.T) {
	tests := []struct {
		name             string
		setup            func(t *testing.T) (startDir string, filenames []string)
		expectErr        error
		expectFound      bool
		expectedFilename string
	}{
		{
			name: "finds first of multiple files",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				testFile := filepath.Join(dir, "first.txt")
				if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return dir, []string{"first.txt", "second.txt"}
			},
			expectErr:        nil,
			expectFound:      true,
			expectedFilename: "first.txt",
		},
		{
			name: "finds second file when first doesn't exist",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				testFile := filepath.Join(dir, "second.txt")
				if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return dir, []string{"first.txt", "second.txt"}
			},
			expectErr:        nil,
			expectFound:      true,
			expectedFilename: "second.txt",
		},
		{
			name: "finds file in parent when not in current",
			setup: func(t *testing.T) (string, []string) {
				parentDir := t.TempDir()
				childDir := filepath.Join(parentDir, "child")
				if err := os.Mkdir(childDir, 0755); err != nil {
					t.Fatalf("failed to create child directory: %v", err)
				}
				testFile := filepath.Join(parentDir, "parent-target.txt")
				if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return childDir, []string{"nonexistent.txt", "parent-target.txt"}
			},
			expectErr:        nil,
			expectFound:      true,
			expectedFilename: "parent-target.txt",
		},
		{
			name: "returns ErrFileNotFound when no files exist",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				return dir, []string{"missing1.txt", "missing2.txt"}
			},
			expectErr:   ErrFileNotFound,
			expectFound: false,
		},
		{
			name: "returns ErrInvalidPath for empty startDir",
			setup: func(t *testing.T) (string, []string) {
				return "", []string{"test.txt"}
			},
			expectErr:   ErrInvalidPath,
			expectFound: false,
		},
		{
			name: "returns ErrNoFilesSpecified for empty filenames",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				return dir, []string{}
			},
			expectErr:   ErrNoFilesSpecified,
			expectFound: false,
		},
		{
			name: "returns ErrNoFilesSpecified for nil filenames",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				return dir, nil
			},
			expectErr:   ErrNoFilesSpecified,
			expectFound: false,
		},
		{
			name: "returns correct filename that was found",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				testFile := filepath.Join(dir, "package.json")
				if err := os.WriteFile(testFile, []byte("{}"), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return dir, []string{"Cargo.toml", "package.json", "go.mod"}
			},
			expectErr:        nil,
			expectFound:      true,
			expectedFilename: "package.json",
		},
		{
			name: "startDir is a file resolves to parent directory",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				startFile := filepath.Join(dir, "start.txt")
				if err := os.WriteFile(startFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create start file: %v", err)
				}
				targetFile := filepath.Join(dir, "target.txt")
				if err := os.WriteFile(targetFile, []byte("content"), 0644); err != nil {
					t.Fatalf("failed to create target file: %v", err)
				}
				return startFile, []string{"target.txt"}
			},
			expectErr:        nil,
			expectFound:      true,
			expectedFilename: "target.txt",
		},
		{
			name: "returns ErrInvalidPath for non-existent startDir",
			setup: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				return filepath.Join(dir, "nonexistent", "path"), []string{"test.txt"}
			},
			expectErr:   ErrInvalidPath,
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			startDir, filenames := tt.setup(t)
			path, foundName, err := FindUpAny(startDir, filenames...)

			if tt.expectErr != nil {
				if !errors.Is(err, tt.expectErr) {
					t.Errorf("FindUpAny() error = %v, want %v", err, tt.expectErr)
				}
				if path != "" {
					t.Errorf("FindUpAny() path = %v, want empty on error", path)
				}
				if foundName != "" {
					t.Errorf("FindUpAny() foundName = %v, want empty on error", foundName)
				}
				return
			}

			if err != nil {
				t.Errorf("FindUpAny() unexpected error = %v", err)
				return
			}

			if tt.expectFound {
				if path == "" {
					t.Error("FindUpAny() returned empty path when expecting found file")
				}
				if foundName != tt.expectedFilename {
					t.Errorf("FindUpAny() foundName = %v, want %v", foundName, tt.expectedFilename)
				}
				if filepath.Base(path) != tt.expectedFilename {
					t.Errorf("FindUpAny() path base = %v, want %v", filepath.Base(path), tt.expectedFilename)
				}
				// Verify the file actually exists at the returned path
				if _, err := os.Stat(path); err != nil {
					t.Errorf("FindUpAny() returned path that does not exist: %v", path)
				}
			}
		})
	}
}

func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		msg  string
	}{
		{
			name: "ErrInvalidPath message",
			err:  ErrInvalidPath,
			msg:  "invalid path provided",
		},
		{
			name: "ErrFileNotFound message",
			err:  ErrFileNotFound,
			msg:  "file not found",
		},
		{
			name: "ErrNoFilesSpecified message",
			err:  ErrNoFilesSpecified,
			msg:  "no files specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.msg {
				t.Errorf("%s.Error() = %v, want %v", tt.name, tt.err.Error(), tt.msg)
			}
		})
	}
}

func TestFindUp_PreferCloserFile(t *testing.T) {
	// Tests that FindUp prefers files in closer directories
	grandparentDir := t.TempDir()
	parentDir := filepath.Join(grandparentDir, "parent")
	childDir := filepath.Join(parentDir, "child")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatalf("failed to create directories: %v", err)
	}

	// Create same file in both grandparent and parent
	grandparentFile := filepath.Join(grandparentDir, "config.txt")
	parentFile := filepath.Join(parentDir, "config.txt")

	if err := os.WriteFile(grandparentFile, []byte("grandparent"), 0644); err != nil {
		t.Fatalf("failed to create grandparent file: %v", err)
	}
	if err := os.WriteFile(parentFile, []byte("parent"), 0644); err != nil {
		t.Fatalf("failed to create parent file: %v", err)
	}

	// Search from child should find parent's config first
	path, err := FindUp(childDir, "config.txt")
	if err != nil {
		t.Fatalf("FindUp() error = %v", err)
	}

	// Read content to verify we got the closer one
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read found file: %v", err)
	}

	if string(content) != "parent" {
		t.Errorf("FindUp() found file with content = %v, want 'parent' (closer file)", string(content))
	}
}

func TestFindUpAny_PreferFirstInList(t *testing.T) {
	// Tests that FindUpAny prefers files in the order they appear in the list
	dir := t.TempDir()

	// Create both files
	if err := os.WriteFile(filepath.Join(dir, "first.txt"), []byte("first"), 0644); err != nil {
		t.Fatalf("failed to create first file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "second.txt"), []byte("second"), 0644); err != nil {
		t.Fatalf("failed to create second file: %v", err)
	}

	// Both files exist, should prefer "first.txt" as it appears first in the list
	_, foundName, err := FindUpAny(dir, "first.txt", "second.txt")
	if err != nil {
		t.Fatalf("FindUpAny() error = %v", err)
	}

	if foundName != "first.txt" {
		t.Errorf("FindUpAny() foundName = %v, want 'first.txt' (first in list)", foundName)
	}
}

func TestFileExists_WithSymlinks(t *testing.T) {
	dir := t.TempDir()

	// Create a real file
	realFile := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(realFile, []byte("content"), 0644); err != nil {
		t.Fatalf("failed to create real file: %v", err)
	}

	// Create a symlink to the file
	symlink := filepath.Join(dir, "link.txt")
	if err := os.Symlink(realFile, symlink); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	// FileExists should follow symlinks
	if !FileExists(dir, "link.txt") {
		t.Error("FileExists() should return true for valid symlink")
	}

	// Create a broken symlink
	brokenLink := filepath.Join(dir, "broken.txt")
	if err := os.Symlink(filepath.Join(dir, "nonexistent.txt"), brokenLink); err != nil {
		t.Fatalf("failed to create broken symlink: %v", err)
	}

	// FileExists should return false for broken symlinks
	if FileExists(dir, "broken.txt") {
		t.Error("FileExists() should return false for broken symlink")
	}
}

func TestFindUp_WithSymlinks(t *testing.T) {
	parentDir := t.TempDir()
	childDir := filepath.Join(parentDir, "child")
	if err := os.Mkdir(childDir, 0755); err != nil {
		t.Fatalf("failed to create child directory: %v", err)
	}

	// Create a real file in parent
	realFile := filepath.Join(parentDir, "config.txt")
	if err := os.WriteFile(realFile, []byte("content"), 0644); err != nil {
		t.Fatalf("failed to create real file: %v", err)
	}

	// Create a symlink in child pointing to parent's file with different name
	symlink := filepath.Join(childDir, "local-config.txt")
	if err := os.Symlink(realFile, symlink); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	// FindUp should find the symlink in child directory
	path, err := FindUp(childDir, "local-config.txt")
	if err != nil {
		t.Fatalf("FindUp() error = %v", err)
	}

	if filepath.Base(path) != "local-config.txt" {
		t.Errorf("FindUp() found wrong file: %v", path)
	}
}

func TestFileExists_SpecialCharacters(t *testing.T) {
	dir := t.TempDir()

	// Test files with special characters
	specialFiles := []string{
		"file with spaces.txt",
		"file-with-dashes.txt",
		"file_with_underscores.txt",
		"file.multiple.dots.txt",
	}

	for _, filename := range specialFiles {
		fullPath := filepath.Join(dir, filename)
		if err := os.WriteFile(fullPath, []byte("content"), 0644); err != nil {
			t.Fatalf("failed to create file %q: %v", filename, err)
		}

		if !FileExists(dir, filename) {
			t.Errorf("FileExists() = false for %q, want true", filename)
		}
	}
}

func TestFindUp_DeepNesting(t *testing.T) {
	// Test that FindUp can handle deeply nested directories
	baseDir := t.TempDir()

	// Create the target file at the root
	targetFile := filepath.Join(baseDir, "root-config.txt")
	if err := os.WriteFile(targetFile, []byte("root"), 0644); err != nil {
		t.Fatalf("failed to create target file: %v", err)
	}

	// Create a deeply nested directory structure
	currentDir := baseDir
	for i := 0; i < 10; i++ {
		currentDir = filepath.Join(currentDir, "level")
	}
	if err := os.MkdirAll(currentDir, 0755); err != nil {
		t.Fatalf("failed to create nested directories: %v", err)
	}

	// FindUp from the deepest level should still find the root file
	path, err := FindUp(currentDir, "root-config.txt")
	if err != nil {
		t.Fatalf("FindUp() error = %v", err)
	}

	if path != targetFile {
		t.Errorf("FindUp() = %v, want %v", path, targetFile)
	}
}
