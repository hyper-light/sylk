// Package git provides Git integration for the Sylk Document Search System.
// It offers functionality for tracking modified files, blame information,
// file history, and coordinated git operations.
package git

import (
	"errors"
	"time"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrRepoNotFound indicates the git repository was not found.
	ErrRepoNotFound = errors.New("git repository not found")

	// ErrRepoPathEmpty indicates an empty repository path.
	ErrRepoPathEmpty = errors.New("repository path cannot be empty")

	// ErrCommitNotFound indicates the specified commit was not found.
	ErrCommitNotFound = errors.New("commit not found")

	// ErrFileNotFound indicates the specified file was not found.
	ErrFileNotFound = errors.New("file not found")

	// ErrInvalidCommitHash indicates an invalid commit hash format.
	ErrInvalidCommitHash = errors.New("invalid commit hash")

	// ErrGitNotAvailable indicates git is not available in the environment.
	ErrGitNotAvailable = errors.New("git is not available")

	// ErrContextCanceled indicates the operation was canceled.
	ErrContextCanceled = errors.New("operation canceled")

	// ErrOperationTimeout indicates the operation timed out.
	ErrOperationTimeout = errors.New("operation timed out")

	// ErrManagerClosed indicates the manager has been closed.
	ErrManagerClosed = errors.New("git manager is closed")

	// ErrCacheDisabled indicates caching is disabled.
	ErrCacheDisabled = errors.New("cache is disabled")
)

// =============================================================================
// CommitInfo
// =============================================================================

// CommitInfo represents metadata about a git commit.
type CommitInfo struct {
	// Hash is the full 40-character commit hash.
	Hash string

	// ShortHash is the abbreviated commit hash (7 characters).
	ShortHash string

	// Author is the name of the commit author.
	Author string

	// AuthorEmail is the email of the commit author.
	AuthorEmail string

	// AuthorTime is when the commit was authored.
	AuthorTime time.Time

	// Committer is the name of the person who committed.
	Committer string

	// CommitterEmail is the email of the committer.
	CommitterEmail string

	// CommitTime is when the commit was made.
	CommitTime time.Time

	// Message is the full commit message.
	Message string

	// Subject is the first line of the commit message.
	Subject string

	// ParentHashes contains the hashes of parent commits.
	ParentHashes []string

	// FilesChanged contains paths of files changed in this commit.
	FilesChanged []string
}

// IsMergeCommit returns true if the commit has multiple parents.
func (c *CommitInfo) IsMergeCommit() bool {
	return len(c.ParentHashes) > 1
}

// Date returns the author time (alias for AuthorTime).
// This is the conventional "date" shown in git log.
func (c *CommitInfo) Date() time.Time {
	return c.AuthorTime
}

// =============================================================================
// BlameLine and BlameResult
// =============================================================================

// BlameLine represents blame information for a single line.
type BlameLine struct {
	// LineNumber is the 1-indexed line number.
	LineNumber int

	// CommitHash is the commit that last modified this line.
	CommitHash string

	// Author is the author of the commit.
	Author string

	// AuthorEmail is the author's email.
	AuthorEmail string

	// AuthorTime is when the line was last modified.
	AuthorTime time.Time

	// Content is the content of the line.
	Content string
}

// Line returns the line number (alias for LineNumber).
func (b *BlameLine) Line() int {
	return b.LineNumber
}

// Date returns the author time (alias for AuthorTime).
func (b *BlameLine) Date() time.Time {
	return b.AuthorTime
}

// BlameResult contains blame for an entire file.
type BlameResult struct {
	// Path is the file path.
	Path string

	// Lines contains blame info for each line.
	Lines []BlameLine
}

// Entries returns the lines (alias for Lines for compatibility).
func (r *BlameResult) Entries() []BlameLine {
	return r.Lines
}

// BlameEntry is an alias for BlameLine for compatibility.
type BlameEntry = BlameLine

// =============================================================================
// FileDiff
// =============================================================================

// FileDiff represents a diff for a single file.
type FileDiff struct {
	// Path is the file path.
	Path string

	// OldPath is the previous path if the file was renamed.
	OldPath string

	// Status indicates the type of change (A, M, D, R, C).
	Status string

	// Additions is the number of lines added.
	Additions int

	// Deletions is the number of lines deleted.
	Deletions int

	// Hunks contains the diff hunks.
	Hunks []DiffHunk

	// Binary indicates if this is a binary file.
	Binary bool
}

// DiffHunk represents a section of changes in a diff.
type DiffHunk struct {
	// OldStart is the starting line in the old version.
	OldStart int

	// OldLines is the number of lines from old version.
	OldLines int

	// NewStart is the starting line in the new version.
	NewStart int

	// NewLines is the number of lines in new version.
	NewLines int

	// Lines contains the actual diff lines.
	Lines []string
}

// =============================================================================
// FileStatus
// =============================================================================

// FileStatus represents the status of a file in git.
type FileStatus struct {
	// Path is the file path.
	Path string

	// Status is the git status code (M, A, D, R, C, U, ?).
	Status string

	// Staged indicates if changes are staged.
	Staged bool

	// OldPath is the previous path for renamed files.
	OldPath string
}

// IsModified returns true if the file is modified.
func (f *FileStatus) IsModified() bool {
	return f.Status == "M"
}

// IsAdded returns true if the file is newly added.
func (f *FileStatus) IsAdded() bool {
	return f.Status == "A"
}

// IsDeleted returns true if the file is deleted.
func (f *FileStatus) IsDeleted() bool {
	return f.Status == "D"
}

// IsRenamed returns true if the file is renamed.
func (f *FileStatus) IsRenamed() bool {
	return f.Status == "R"
}

// IsUntracked returns true if the file is untracked.
func (f *FileStatus) IsUntracked() bool {
	return f.Status == "?"
}
