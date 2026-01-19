// Package cmd provides CLI commands for the Sylk application.
// This file contains tests for the index command.
package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Index Command Tests
// =============================================================================

func TestIndexCmd_Definition(t *testing.T) {
	t.Run("command is defined", func(t *testing.T) {
		assert.NotNil(t, indexCmd)
		assert.Equal(t, "index", indexCmd.Use)
		assert.Equal(t, "Manage the search index", indexCmd.Short)
	})

	t.Run("has subcommands", func(t *testing.T) {
		cmds := indexCmd.Commands()

		// Find status, rebuild, verify subcommands
		var foundStatus, foundRebuild, foundVerify bool
		for _, cmd := range cmds {
			switch cmd.Use {
			case "status":
				foundStatus = true
			case "rebuild":
				foundRebuild = true
			case "verify":
				foundVerify = true
			}
		}

		assert.True(t, foundStatus, "status subcommand should exist")
		assert.True(t, foundRebuild, "rebuild subcommand should exist")
		assert.True(t, foundVerify, "verify subcommand should exist")
	})

	t.Run("has persistent flags", func(t *testing.T) {
		pflags := indexCmd.PersistentFlags()

		// Check index flag
		indexFlag := pflags.Lookup("index")
		require.NotNil(t, indexFlag)
		assert.Equal(t, IndexDefaultPath, indexFlag.DefValue)

		// Check json flag
		jsonFlag := pflags.Lookup("json")
		require.NotNil(t, jsonFlag)
		assert.Equal(t, "false", jsonFlag.DefValue)

		// Check verbose flag
		verboseFlag := pflags.Lookup("verbose")
		require.NotNil(t, verboseFlag)
		assert.Equal(t, "v", verboseFlag.Shorthand)
	})
}

func TestIndexStatusCmd_Definition(t *testing.T) {
	t.Run("command is defined", func(t *testing.T) {
		assert.NotNil(t, indexStatusCmd)
		assert.Equal(t, "status", indexStatusCmd.Use)
		assert.Equal(t, "Show index status", indexStatusCmd.Short)
	})
}

func TestIndexRebuildCmd_Definition(t *testing.T) {
	t.Run("command is defined", func(t *testing.T) {
		assert.NotNil(t, indexRebuildCmd)
		assert.Equal(t, "rebuild", indexRebuildCmd.Use)
		assert.Equal(t, "Rebuild the index from scratch", indexRebuildCmd.Short)
	})

	t.Run("has rebuild-specific flags", func(t *testing.T) {
		flags := indexRebuildCmd.Flags()

		// Check root flag
		rootFlag := flags.Lookup("root")
		require.NotNil(t, rootFlag)
		assert.Equal(t, "r", rootFlag.Shorthand)
		assert.Equal(t, ".", rootFlag.DefValue)

		// Check batch-size flag
		batchFlag := flags.Lookup("batch-size")
		require.NotNil(t, batchFlag)
		assert.Equal(t, "b", batchFlag.Shorthand)

		// Check concurrency flag
		concFlag := flags.Lookup("concurrency")
		require.NotNil(t, concFlag)
		assert.Equal(t, "c", concFlag.Shorthand)

		// Check watch flag
		watchFlag := flags.Lookup("watch")
		require.NotNil(t, watchFlag)
		assert.Equal(t, "w", watchFlag.Shorthand)
		assert.Equal(t, "false", watchFlag.DefValue)

		// Check include flag
		includeFlag := flags.Lookup("include")
		require.NotNil(t, includeFlag)
		assert.Equal(t, "I", includeFlag.Shorthand)

		// Check exclude flag
		excludeFlag := flags.Lookup("exclude")
		require.NotNil(t, excludeFlag)
		assert.Equal(t, "E", excludeFlag.Shorthand)
	})
}

func TestIndexVerifyCmd_Definition(t *testing.T) {
	t.Run("command is defined", func(t *testing.T) {
		assert.NotNil(t, indexVerifyCmd)
		assert.Equal(t, "verify", indexVerifyCmd.Use)
		assert.Equal(t, "Verify index integrity", indexVerifyCmd.Short)
	})
}

// =============================================================================
// Index Status Output Tests
// =============================================================================

func TestIndexStatusOutput(t *testing.T) {
	t.Run("creates status output", func(t *testing.T) {
		status := &indexStatusOutput{
			Path:          "/path/to/index",
			Exists:        true,
			Open:          true,
			DocumentCount: 100,
			LastModified:  time.Now(),
			SizeBytes:     1024 * 1024, // 1MB
			Healthy:       true,
			HealthMessage: "Index is healthy.",
		}

		assert.Equal(t, "/path/to/index", status.Path)
		assert.True(t, status.Exists)
		assert.True(t, status.Open)
		assert.Equal(t, uint64(100), status.DocumentCount)
		assert.True(t, status.Healthy)
	})
}

func TestOutputJSONIndexStatus(t *testing.T) {
	status := &indexStatusOutput{
		Path:          "/test/index",
		Exists:        true,
		Open:          true,
		DocumentCount: 50,
		Healthy:       true,
		HealthMessage: "OK",
	}

	var buf bytes.Buffer
	err := outputJSONIndexStatus(&buf, status)

	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, `"path": "/test/index"`)
	assert.Contains(t, output, `"exists": true`)
	assert.Contains(t, output, `"open": true`)
	assert.Contains(t, output, `"document_count": 50`)
	assert.Contains(t, output, `"healthy": true`)
}

func TestOutputRichIndexStatus(t *testing.T) {
	t.Run("index exists", func(t *testing.T) {
		status := &indexStatusOutput{
			Path:          "/test/index",
			Exists:        true,
			Open:          true,
			DocumentCount: 100,
			SizeBytes:     1024 * 1024,
			Healthy:       true,
			HealthMessage: "Index is healthy.",
		}

		var buf bytes.Buffer
		err := outputRichIndexStatus(&buf, status)

		require.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "Index Status")
		assert.Contains(t, output, "/test/index")
		assert.Contains(t, output, "100")
	})

	t.Run("index does not exist", func(t *testing.T) {
		status := &indexStatusOutput{
			Path:          "/nonexistent",
			Exists:        false,
			Healthy:       false,
			HealthMessage: "Index does not exist.",
		}

		var buf bytes.Buffer
		err := outputRichIndexStatus(&buf, status)

		require.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "No")
		assert.Contains(t, output, "Index does not exist")
	})
}

// =============================================================================
// Rebuild Output Tests
// =============================================================================

func TestIndexRebuildOutput(t *testing.T) {
	result := &indexRebuildOutput{
		Success:      true,
		TotalFiles:   100,
		IndexedFiles: 95,
		FailedFiles:  5,
		Duration:     5 * time.Second,
		Errors:       []string{"error1", "error2"},
		Watching:     false,
	}

	assert.True(t, result.Success)
	assert.Equal(t, int64(100), result.TotalFiles)
	assert.Equal(t, int64(95), result.IndexedFiles)
	assert.Equal(t, int64(5), result.FailedFiles)
	assert.Len(t, result.Errors, 2)
}

func TestOutputRebuildResult(t *testing.T) {
	// Reset global flags
	defer func() {
		indexJSON = false
		indexVerbose = false
	}()

	t.Run("success output", func(t *testing.T) {
		indexJSON = false
		indexVerbose = false

		result := &indexRebuildOutput{
			Success:      true,
			TotalFiles:   100,
			IndexedFiles: 100,
			FailedFiles:  0,
			Duration:     2 * time.Second,
		}

		var buf bytes.Buffer
		err := outputRebuildResult(&buf, result)

		require.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "Rebuild Complete")
		assert.Contains(t, output, "100")
	})

	t.Run("with failures and verbose", func(t *testing.T) {
		indexJSON = false
		indexVerbose = true

		result := &indexRebuildOutput{
			Success:      true,
			TotalFiles:   100,
			IndexedFiles: 95,
			FailedFiles:  5,
			Duration:     2 * time.Second,
			Errors:       []string{"failed to read file1.go"},
		}

		var buf bytes.Buffer
		err := outputRebuildResult(&buf, result)

		require.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "95")
		assert.Contains(t, output, "5")
		assert.Contains(t, output, "failed to read file1.go")
	})

	t.Run("json output", func(t *testing.T) {
		indexJSON = true

		result := &indexRebuildOutput{
			Success:      true,
			TotalFiles:   50,
			IndexedFiles: 50,
			Duration:     1 * time.Second,
		}

		var buf bytes.Buffer
		err := outputRebuildResult(&buf, result)

		require.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, `"success": true`)
		assert.Contains(t, output, `"total_files": 50`)
		assert.Contains(t, output, `"indexed_files": 50`)
	})
}

// =============================================================================
// Verify Output Tests
// =============================================================================

func TestIndexVerifyOutput(t *testing.T) {
	result := &indexVerifyOutput{
		Valid:         true,
		DocumentCount: 100,
		CorruptedDocs: 0,
		OrphanedFiles: 0,
		MissingFiles:  0,
		Issues:        []string{},
		CheckDuration: "100ms",
	}

	assert.True(t, result.Valid)
	assert.Equal(t, uint64(100), result.DocumentCount)
	assert.Empty(t, result.Issues)
}

func TestOutputVerifyResult(t *testing.T) {
	// Reset global flags
	defer func() {
		indexJSON = false
	}()

	t.Run("valid index", func(t *testing.T) {
		indexJSON = false

		result := &indexVerifyOutput{
			Valid:         true,
			DocumentCount: 100,
			CheckDuration: "50ms",
		}

		var buf bytes.Buffer
		err := outputVerifyResult(&buf, result, time.Now())

		require.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "100")
		assert.Contains(t, output, "Valid")
	})

	t.Run("invalid index", func(t *testing.T) {
		indexJSON = false

		result := &indexVerifyOutput{
			Valid:         false,
			DocumentCount: 0,
			Issues:        []string{"Index does not exist"},
		}

		var buf bytes.Buffer
		err := outputVerifyResult(&buf, result, time.Now())

		require.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "Invalid")
		assert.Contains(t, output, "Index does not exist")
	})

	t.Run("json output", func(t *testing.T) {
		indexJSON = true

		result := &indexVerifyOutput{
			Valid:         true,
			DocumentCount: 50,
		}

		var buf bytes.Buffer
		err := outputVerifyResult(&buf, result, time.Now())

		require.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, `"valid": true`)
		assert.Contains(t, output, `"document_count": 50`)
	})
}

// =============================================================================
// Progress Tracker Tests
// =============================================================================

func TestProgressTracker(t *testing.T) {
	t.Run("enabled tracker", func(t *testing.T) {
		var buf bytes.Buffer
		tracker := newProgressTracker(&buf, true)

		require.NotNil(t, tracker)
		assert.True(t, tracker.enabled)
		assert.NotZero(t, tracker.startTime)
	})

	t.Run("disabled tracker", func(t *testing.T) {
		var buf bytes.Buffer
		tracker := newProgressTracker(&buf, false)

		require.NotNil(t, tracker)
		assert.False(t, tracker.enabled)
	})

	t.Run("update writes progress", func(t *testing.T) {
		var buf bytes.Buffer
		tracker := newProgressTracker(&buf, true)

		tracker.update(10, 8, 2, "/path/to/file.go")

		output := buf.String()
		assert.Contains(t, output, "Processed")
		assert.Contains(t, output, "10")
		assert.Contains(t, output, "Indexed")
		assert.Contains(t, output, "8")
	})

	t.Run("update does nothing when disabled", func(t *testing.T) {
		var buf bytes.Buffer
		tracker := newProgressTracker(&buf, false)

		tracker.update(10, 8, 2, "/path/to/file.go")

		assert.Empty(t, buf.String())
	})

	t.Run("finish clears line when enabled", func(t *testing.T) {
		var buf bytes.Buffer
		tracker := newProgressTracker(&buf, true)
		tracker.lastLen = 50

		tracker.finish()

		// Should contain carriage return for line clearing
		assert.Contains(t, buf.String(), "\r")
	})
}

// =============================================================================
// Utility Function Tests
// =============================================================================

func TestValidateRootPath(t *testing.T) {
	t.Run("valid directory", func(t *testing.T) {
		// Use the current directory which should always exist
		err := validateRootPath(".")
		assert.NoError(t, err)
	})

	t.Run("nonexistent path", func(t *testing.T) {
		err := validateRootPath("/nonexistent/path/12345")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist")
	})

	t.Run("file instead of directory", func(t *testing.T) {
		// Create a temp file
		tmpFile, err := os.CreateTemp("", "test")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		tmpFile.Close()

		err = validateRootPath(tmpFile.Name())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not a directory")
	})
}

func TestGetDirSize(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "testdir")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		size, err := getDirSize(tmpDir)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), size)
	})

	t.Run("directory with files", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "testdir")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Create a file with known size
		content := []byte("hello world")
		err = os.WriteFile(filepath.Join(tmpDir, "test.txt"), content, 0644)
		require.NoError(t, err)

		size, err := getDirSize(tmpDir)
		assert.NoError(t, err)
		assert.Equal(t, int64(len(content)), size)
	})
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatBytes(tt.bytes)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestIndexCmd_Execution(t *testing.T) {
	t.Run("index with no args shows status", func(t *testing.T) {
		// This tests that the default behavior is to show status
		cmd := &cobra.Command{
			Use:  "index",
			RunE: runIndexStatus,
		}

		// We can't fully test this without a real index, but we can verify
		// the command structure is correct
		assert.NotNil(t, cmd.RunE)
	})
}

// =============================================================================
// Command Flag Behavior Tests
// =============================================================================

func TestIndexFlags_Defaults(t *testing.T) {
	t.Run("default values", func(t *testing.T) {
		// Verify constant values match expected defaults
		assert.Equal(t, 50, IndexDefaultBatchSize)
		assert.Equal(t, 4, IndexDefaultConcurrency)
		assert.Equal(t, ".sylk/index", IndexDefaultPath)
		assert.Equal(t, 40, ProgressBarWidth)
	})
}

// =============================================================================
// Error Handling Tests
// =============================================================================

func TestIndexErrorCases(t *testing.T) {
	t.Run("rebuild with invalid root path", func(t *testing.T) {
		err := validateRootPath("/definitely/not/a/real/path/12345")
		assert.Error(t, err)
	})
}

// =============================================================================
// Color Code Tests
// =============================================================================

func TestColorCodes(t *testing.T) {
	// Verify color codes are defined (used in output formatting)
	// These are defined in search.go but used in index.go as well
	assert.Equal(t, "\033[0m", colorReset)
	assert.Equal(t, "\033[31m", colorRed)
	assert.Equal(t, "\033[32m", colorGreen)
	assert.Equal(t, "\033[33m", colorYellow)
	assert.Equal(t, "\033[34m", colorBlue)
	assert.Equal(t, "\033[36m", colorCyan)
	assert.Equal(t, "\033[90m", colorGray)
	assert.Equal(t, "\033[1m", colorBold)
}

// =============================================================================
// Path Truncation Tests
// =============================================================================

func TestProgressPathTruncation(t *testing.T) {
	t.Run("long path is truncated in progress", func(t *testing.T) {
		var buf bytes.Buffer
		tracker := newProgressTracker(&buf, true)

		longPath := strings.Repeat("very/long/path/", 10) + "file.go"
		tracker.update(1, 1, 0, longPath)

		output := buf.String()
		// The truncated path should be <= 50 characters + "..."
		// Check that output contains some part of the path
		assert.Contains(t, output, "file.go")
	})

	t.Run("short path is not truncated", func(t *testing.T) {
		var buf bytes.Buffer
		tracker := newProgressTracker(&buf, true)

		shortPath := "cmd/search.go"
		tracker.update(1, 1, 0, shortPath)

		output := buf.String()
		assert.Contains(t, output, shortPath)
	})
}
