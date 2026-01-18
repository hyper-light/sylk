// Package search provides document types for the Sylk search system.
package search

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// FileChangeStatus represents the type of change made to a file in a commit.
type FileChangeStatus string

const (
	// FileStatusAdded indicates a newly created file.
	FileStatusAdded FileChangeStatus = "added"
	// FileStatusModified indicates a modified existing file.
	FileStatusModified FileChangeStatus = "modified"
	// FileStatusDeleted indicates a removed file.
	FileStatusDeleted FileChangeStatus = "deleted"
	// FileStatusRenamed indicates a file that was renamed (with optional content changes).
	FileStatusRenamed FileChangeStatus = "renamed"
	// FileStatusCopied indicates a file that was copied from another location.
	FileStatusCopied FileChangeStatus = "copied"
)

// FileChange represents a single file modification within a commit.
type FileChange struct {
	// Path is the current path of the file (after any rename/copy).
	Path string `json:"path"`
	// Status indicates the type of change (added, modified, deleted, renamed, copied).
	Status FileChangeStatus `json:"status"`
	// OldPath is the previous path for renamed or copied files.
	OldPath string `json:"old_path,omitempty"`
}

// GitCommitDocument represents an indexed git commit for search.
// It embeds the base Document type and adds git-specific fields.
type GitCommitDocument struct {
	Document

	// CommitHash is the full 40-character hexadecimal SHA-1 hash.
	CommitHash string `json:"commit_hash"`

	// Author information
	Author      string    `json:"author"`
	AuthorEmail string    `json:"author_email"`
	AuthorDate  time.Time `json:"author_date"`

	// Committer information (may differ from author in rebases, cherry-picks)
	Committer      string    `json:"committer"`
	CommitterEmail string    `json:"committer_email"`
	CommitDate     time.Time `json:"commit_date"`

	// Message is the full commit message including subject and body.
	Message string `json:"message"`

	// FilesChanged lists all files modified in this commit.
	FilesChanged []FileChange `json:"files_changed,omitempty"`

	// Diff contains the full diff output for this commit.
	Diff string `json:"diff,omitempty"`

	// ParentHashes contains the commit hashes of parent commits.
	// A normal commit has one parent; a merge commit has two or more.
	ParentHashes []string `json:"parent_hashes,omitempty"`
}

// Validation errors for GitCommitDocument.
var (
	ErrEmptyCommitHash    = errors.New("commit hash is required")
	ErrInvalidCommitHash  = errors.New("commit hash must be 40 hexadecimal characters")
	ErrEmptyAuthor        = errors.New("author is required")
	ErrEmptyCommitter     = errors.New("committer is required")
	ErrEmptyMessage       = errors.New("commit message is required")
	ErrInvalidParentHash  = errors.New("parent hash must be 40 hexadecimal characters")
	ErrInvalidFileChange  = errors.New("file change path is required")
	ErrMissingOldPath     = errors.New("old_path is required for renamed or copied files")
)

// commitHashRegex validates a 40-character hexadecimal commit hash.
var commitHashRegex = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// ValidateCommitHash checks if a commit hash is valid.
// Returns nil if valid, ErrEmptyCommitHash if empty, or ErrInvalidCommitHash if malformed.
func ValidateCommitHash(hash string) error {
	if hash == "" {
		return ErrEmptyCommitHash
	}
	if !commitHashRegex.MatchString(hash) {
		return ErrInvalidCommitHash
	}
	return nil
}

// Validate performs comprehensive validation of the GitCommitDocument.
// It checks all required fields and validates commit hash formats.
func (g *GitCommitDocument) Validate() error {
	if err := g.validateRequiredFields(); err != nil {
		return err
	}
	if err := g.validateHashes(); err != nil {
		return err
	}
	return g.validateFileChanges()
}

// validateRequiredFields checks that all required fields are present.
func (g *GitCommitDocument) validateRequiredFields() error {
	if g.Author == "" {
		return ErrEmptyAuthor
	}
	if g.Committer == "" {
		return ErrEmptyCommitter
	}
	if g.Message == "" {
		return ErrEmptyMessage
	}
	return nil
}

// validateHashes validates the commit hash and all parent hashes.
func (g *GitCommitDocument) validateHashes() error {
	if err := ValidateCommitHash(g.CommitHash); err != nil {
		return err
	}
	for _, parentHash := range g.ParentHashes {
		if !commitHashRegex.MatchString(parentHash) {
			return ErrInvalidParentHash
		}
	}
	return nil
}

// validateFileChanges validates that all file changes have required fields.
func (g *GitCommitDocument) validateFileChanges() error {
	for _, fc := range g.FilesChanged {
		if fc.Path == "" {
			return ErrInvalidFileChange
		}
		if (fc.Status == FileStatusRenamed || fc.Status == FileStatusCopied) && fc.OldPath == "" {
			return ErrMissingOldPath
		}
	}
	return nil
}

// ValidateCommitHash validates the CommitHash field of this document.
func (g *GitCommitDocument) ValidateCommitHash() error {
	return ValidateCommitHash(g.CommitHash)
}

// IsMergeCommit returns true if this commit has more than one parent.
func (g *GitCommitDocument) IsMergeCommit() bool {
	return len(g.ParentHashes) > 1
}

// GetShortHash returns the first 7 characters of the commit hash.
// Returns an empty string if the commit hash is too short.
func (g *GitCommitDocument) GetShortHash() string {
	if len(g.CommitHash) < 7 {
		return g.CommitHash
	}
	return g.CommitHash[:7]
}

// GetSubject returns the first line of the commit message.
// This is typically used as the commit subject/title.
func (g *GitCommitDocument) GetSubject() string {
	if g.Message == "" {
		return ""
	}
	idx := strings.Index(g.Message, "\n")
	if idx == -1 {
		return g.Message
	}
	return g.Message[:idx]
}

// IsValid returns true if FileChangeStatus is a known valid status.
func (s FileChangeStatus) IsValid() bool {
	switch s {
	case FileStatusAdded, FileStatusModified, FileStatusDeleted, FileStatusRenamed, FileStatusCopied:
		return true
	default:
		return false
	}
}

// String returns the string representation of FileChangeStatus.
func (s FileChangeStatus) String() string {
	return string(s)
}
