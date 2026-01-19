package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Format Parsing Tests
// =============================================================================

func TestParseGitFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected GitOutputFormat
	}{
		{
			name:     "json format",
			input:    "json",
			expected: GitOutputJSON,
		},
		{
			name:     "JSON uppercase",
			input:    "JSON",
			expected: GitOutputJSON,
		},
		{
			name:     "plain format",
			input:    "plain",
			expected: GitOutputPlain,
		},
		{
			name:     "table format",
			input:    "table",
			expected: GitOutputTable,
		},
		{
			name:     "default for unknown",
			input:    "unknown",
			expected: GitOutputTable,
		},
		{
			name:     "empty string",
			input:    "",
			expected: GitOutputTable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseGitFormat(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// Duration Parsing Tests
// =============================================================================

func TestParseGitDuration(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		input       string
		shouldError bool
		checkFunc   func(t *testing.T, result time.Time)
	}{
		{
			name:        "hours format",
			input:       "24h",
			shouldError: false,
			checkFunc: func(t *testing.T, result time.Time) {
				expected := now.Add(-24 * time.Hour)
				// Allow 1 second tolerance
				assert.WithinDuration(t, expected, result, time.Second)
			},
		},
		{
			name:        "days format",
			input:       "7d",
			shouldError: false,
			checkFunc: func(t *testing.T, result time.Time) {
				expected := now.AddDate(0, 0, -7)
				assert.WithinDuration(t, expected, result, time.Second)
			},
		},
		{
			name:        "weeks format",
			input:       "2w",
			shouldError: false,
			checkFunc: func(t *testing.T, result time.Time) {
				expected := now.AddDate(0, 0, -14)
				assert.WithinDuration(t, expected, result, time.Second)
			},
		},
		{
			name:        "months format",
			input:       "3m",
			shouldError: false,
			checkFunc: func(t *testing.T, result time.Time) {
				expected := now.AddDate(0, -3, 0)
				assert.WithinDuration(t, expected, result, time.Second)
			},
		},
		{
			name:        "years format",
			input:       "1y",
			shouldError: false,
			checkFunc: func(t *testing.T, result time.Time) {
				expected := now.AddDate(-1, 0, 0)
				assert.WithinDuration(t, expected, result, time.Second)
			},
		},
		{
			name:        "absolute date YYYY-MM-DD",
			input:       "2024-01-15",
			shouldError: false,
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
				assert.Equal(t, expected.Year(), result.Year())
				assert.Equal(t, expected.Month(), result.Month())
				assert.Equal(t, expected.Day(), result.Day())
			},
		},
		{
			name:        "invalid format",
			input:       "invalid",
			shouldError: true,
		},
		{
			name:        "empty string",
			input:       "",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseGitDuration(tt.input)
			if tt.shouldError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.checkFunc != nil {
					tt.checkFunc(t, result)
				}
			}
		})
	}
}

func TestParseGitRelativeDuration(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		input       string
		shouldError bool
		checkDelta  func(result time.Time) time.Duration
	}{
		{
			name:        "48 hours",
			input:       "48h",
			shouldError: false,
			checkDelta: func(result time.Time) time.Duration {
				return now.Sub(result)
			},
		},
		{
			name:        "invalid suffix",
			input:       "48x",
			shouldError: true,
		},
		{
			name:        "no number",
			input:       "h",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseGitRelativeDuration(tt.input)
			if tt.shouldError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.checkDelta != nil {
					delta := tt.checkDelta(result)
					assert.Greater(t, delta, time.Duration(0))
				}
			}
		})
	}
}

// =============================================================================
// Diff Output Formatting Tests
// =============================================================================

func TestFormatDiffStatus(t *testing.T) {
	tests := []struct {
		status   string
		expected string
	}{
		{"A", "Added"},
		{"M", "Modified"},
		{"D", "Deleted"},
		{"R", "Renamed"},
		{"C", "Copied"},
		{"X", "X"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := formatDiffStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOutputDiffTable(t *testing.T) {
	diffs := []git.FileDiff{
		{
			Path:      "file1.go",
			Status:    "M",
			Additions: 10,
			Deletions: 5,
		},
		{
			Path:      "file2.go",
			OldPath:   "old_file2.go",
			Status:    "R",
			Additions: 0,
			Deletions: 0,
		},
	}

	err := outputDiffTable(diffs)
	require.NoError(t, err)
	// The output goes to os.Stdout, but we verify the function doesn't error
}

func TestOutputDiffJSON(t *testing.T) {
	diffs := []git.FileDiff{
		{
			Path:      "file1.go",
			Status:    "A",
			Additions: 100,
		},
	}

	// Capture output would require redirecting stdout
	// For now, just verify no error
	err := outputDiffJSON(diffs)
	require.NoError(t, err)
}

func TestOutputDiffPlain(t *testing.T) {
	diffs := []git.FileDiff{
		{
			Path:   "file1.go",
			Status: "M",
			Hunks: []git.DiffHunk{
				{
					Lines: []string{"+added line", "-removed line"},
				},
			},
		},
	}

	err := outputDiffPlain(diffs)
	require.NoError(t, err)
}

// =============================================================================
// Log Output Formatting Tests
// =============================================================================

func TestFormatLogOutput_Empty(t *testing.T) {
	commits := []*git.CommitInfo{}

	// Test all formats with empty results
	err := formatLogOutput(commits, GitOutputTable)
	require.NoError(t, err)

	err = formatLogOutput(commits, GitOutputJSON)
	require.NoError(t, err)

	err = formatLogOutput(commits, GitOutputPlain)
	require.NoError(t, err)
}

func TestOutputLogTable(t *testing.T) {
	now := time.Now()
	commits := []*git.CommitInfo{
		{
			Hash:       "abc123456789",
			ShortHash:  "abc1234",
			Author:     "Test Author",
			AuthorTime: now,
			Subject:    "Test commit message",
		},
	}

	err := outputLogTable(commits)
	require.NoError(t, err)
}

func TestOutputLogPlain(t *testing.T) {
	now := time.Now()
	commits := []*git.CommitInfo{
		{
			Hash:        "abc123456789",
			Author:      "Test Author",
			AuthorEmail: "test@example.com",
			AuthorTime:  now,
			Subject:     "Test commit message",
		},
	}

	err := outputLogPlain(commits)
	require.NoError(t, err)
}

func TestOutputLogJSON(t *testing.T) {
	now := time.Now()
	commits := []*git.CommitInfo{
		{
			Hash:       "abc123456789",
			Author:     "Test Author",
			AuthorTime: now,
			Subject:    "Test commit",
		},
	}

	err := outputLogJSON(commits)
	require.NoError(t, err)
}

// =============================================================================
// Blame Output Formatting Tests
// =============================================================================

func TestFormatBlameOutput_Empty(t *testing.T) {
	result := &git.BlameResult{
		Path:  "empty.go",
		Lines: []git.BlameLine{},
	}

	err := formatBlameOutput(result, GitOutputTable)
	require.NoError(t, err)
}

func TestFormatBlameOutput_Nil(t *testing.T) {
	err := formatBlameOutput(nil, GitOutputTable)
	require.NoError(t, err)

	err = formatBlameOutput(nil, GitOutputJSON)
	require.NoError(t, err)
}

func TestOutputBlameTable(t *testing.T) {
	now := time.Now()
	result := &git.BlameResult{
		Path: "test.go",
		Lines: []git.BlameLine{
			{
				LineNumber: 1,
				CommitHash: "abc123456789",
				Author:     "Test Author",
				AuthorTime: now,
				Content:    "package main",
			},
			{
				LineNumber: 2,
				CommitHash: "def456789012",
				Author:     "Another Author",
				AuthorTime: now.Add(-time.Hour * 24),
				Content:    "func main() {}",
			},
		},
	}

	err := outputBlameTable(result)
	require.NoError(t, err)
}

func TestOutputBlamePlain(t *testing.T) {
	now := time.Now()
	result := &git.BlameResult{
		Path: "test.go",
		Lines: []git.BlameLine{
			{
				LineNumber: 1,
				CommitHash: "abc123456789",
				Author:     "Test Author",
				AuthorTime: now,
				Content:    "package main",
			},
		},
	}

	err := outputBlamePlain(result)
	require.NoError(t, err)
}

func TestOutputBlameJSON(t *testing.T) {
	now := time.Now()
	result := &git.BlameResult{
		Path: "test.go",
		Lines: []git.BlameLine{
			{
				LineNumber: 1,
				CommitHash: "abc123456789",
				Author:     "Test Author",
				AuthorTime: now,
				Content:    "package main",
			},
		},
	}

	err := outputBlameJSON(result)
	require.NoError(t, err)
}

// =============================================================================
// Filter Tests
// =============================================================================

func TestFilterCommitsByAuthor(t *testing.T) {
	now := time.Now()
	commits := []*git.CommitInfo{
		{
			Hash:        "1",
			Author:      "Alice",
			AuthorEmail: "alice@example.com",
			AuthorTime:  now,
		},
		{
			Hash:        "2",
			Author:      "Bob",
			AuthorEmail: "bob@example.com",
			AuthorTime:  now,
		},
		{
			Hash:        "3",
			Author:      "Charlie",
			AuthorEmail: "charlie@alice.org",
			AuthorTime:  now,
		},
	}

	// Filter by name
	filtered := filterCommitsByAuthor(commits, "alice")
	assert.Len(t, filtered, 2) // Alice and charlie@alice.org

	// Filter by exact name
	filtered = filterCommitsByAuthor(commits, "Bob")
	assert.Len(t, filtered, 1)
	assert.Equal(t, "Bob", filtered[0].Author)

	// Filter by email
	filtered = filterCommitsByAuthor(commits, "bob@example.com")
	assert.Len(t, filtered, 1)

	// No match
	filtered = filterCommitsByAuthor(commits, "nonexistent")
	assert.Len(t, filtered, 0)

	// Case insensitive
	filtered = filterCommitsByAuthor(commits, "ALICE")
	assert.Len(t, filtered, 2)
}

func TestFilterCommitsByTime(t *testing.T) {
	now := time.Now()
	since := now.Add(-48 * time.Hour)
	until := now.Add(-12 * time.Hour)

	commits := []*git.CommitInfo{
		{
			Hash:       "1",
			AuthorTime: now.Add(-72 * time.Hour), // Before since
		},
		{
			Hash:       "2",
			AuthorTime: now.Add(-36 * time.Hour), // In range
		},
		{
			Hash:       "3",
			AuthorTime: now.Add(-24 * time.Hour), // In range
		},
		{
			Hash:       "4",
			AuthorTime: now.Add(-6 * time.Hour), // After until
		},
	}

	filtered := filterCommitsByTime(commits, since, until)
	assert.Len(t, filtered, 2)
	assert.Equal(t, "2", filtered[0].Hash)
	assert.Equal(t, "3", filtered[1].Hash)
}

// =============================================================================
// Utility Tests
// =============================================================================

func TestTruncateString(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{
			input:    "short",
			maxLen:   10,
			expected: "short",
		},
		{
			input:    "this is a long string",
			maxLen:   10,
			expected: "this is...",
		},
		{
			input:    "exact",
			maxLen:   5,
			expected: "exact",
		},
		{
			input:    "",
			maxLen:   10,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
			assert.LessOrEqual(t, len(result), tt.maxLen)
		})
	}
}

// =============================================================================
// Command Structure Tests
// =============================================================================

func TestGitCommandStructure(t *testing.T) {
	// Verify git command exists
	assert.NotNil(t, gitCmd)
	assert.Equal(t, "git", gitCmd.Use)

	// Verify subcommands exist
	subCmds := gitCmd.Commands()
	cmdNames := make([]string, len(subCmds))
	for i, cmd := range subCmds {
		cmdNames[i] = cmd.Use
	}

	assert.Contains(t, strings.Join(cmdNames, " "), "diff")
	assert.Contains(t, strings.Join(cmdNames, " "), "log")
	assert.Contains(t, strings.Join(cmdNames, " "), "blame")
}

func TestGitDiffCommand(t *testing.T) {
	assert.NotNil(t, gitDiffCmd)
	assert.Equal(t, "diff [from] [to]", gitDiffCmd.Use)
	assert.NotNil(t, gitDiffCmd.RunE)
}

func TestGitLogCommand(t *testing.T) {
	assert.NotNil(t, gitLogCmd)
	assert.Equal(t, "log [path]", gitLogCmd.Use)
	assert.NotNil(t, gitLogCmd.RunE)
}

func TestGitBlameCommand(t *testing.T) {
	assert.NotNil(t, gitBlameCmd)
	assert.Equal(t, "blame <file>", gitBlameCmd.Use)
	assert.NotNil(t, gitBlameCmd.RunE)
}

// =============================================================================
// Flag Tests
// =============================================================================

func TestGitCommandFlags(t *testing.T) {
	// Check persistent flags on git command
	formatFlag := gitCmd.PersistentFlags().Lookup("format")
	assert.NotNil(t, formatFlag)
	assert.Equal(t, "f", formatFlag.Shorthand)
	assert.Equal(t, "table", formatFlag.DefValue)

	repoFlag := gitCmd.PersistentFlags().Lookup("repo")
	assert.NotNil(t, repoFlag)
	assert.Equal(t, ".", repoFlag.DefValue)
}

func TestGitLogFlags(t *testing.T) {
	sinceFlag := gitLogCmd.Flags().Lookup("since")
	assert.NotNil(t, sinceFlag)

	untilFlag := gitLogCmd.Flags().Lookup("until")
	assert.NotNil(t, untilFlag)

	authorFlag := gitLogCmd.Flags().Lookup("author")
	assert.NotNil(t, authorFlag)

	limitFlag := gitLogCmd.Flags().Lookup("limit")
	assert.NotNil(t, limitFlag)
	assert.Equal(t, "n", limitFlag.Shorthand)
	assert.Equal(t, "10", limitFlag.DefValue)
}

// =============================================================================
// Build File History Options Tests
// =============================================================================

func TestBuildFileHistoryOptions(t *testing.T) {
	// Reset global flags
	gitLimit = 10
	gitSince = ""
	gitUntil = ""

	opts := buildFileHistoryOptions()
	assert.Equal(t, 10, opts.Limit)
	assert.True(t, opts.Since.IsZero())
	assert.True(t, opts.Until.IsZero())

	// With since
	gitSince = "7d"
	opts = buildFileHistoryOptions()
	assert.False(t, opts.Since.IsZero())

	// With until
	gitUntil = "1d"
	opts = buildFileHistoryOptions()
	assert.False(t, opts.Until.IsZero())

	// Reset
	gitSince = ""
	gitUntil = ""
}
