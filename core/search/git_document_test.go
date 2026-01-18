package search

import (
	"strings"
	"testing"
	"time"
)

// Test fixtures for reuse across tests.
var (
	validCommitHash  = "a1b2c3d4e5f6789012345678901234567890abcd"
	validParentHash  = "1234567890abcdef1234567890abcdef12345678"
	validParentHash2 = "abcdef1234567890abcdef1234567890abcdef12"
	shortHash        = "a1b2c3d"
	invalidHashShort = "a1b2c3d"
	invalidHashChars = "g1b2c3d4e5f6789012345678901234567890abcd"
)

func newValidGitCommitDocument() *GitCommitDocument {
	return &GitCommitDocument{
		CommitHash:     validCommitHash,
		Author:         "John Doe",
		AuthorEmail:    "john@example.com",
		AuthorDate:     time.Now().Add(-time.Hour),
		Committer:      "Jane Smith",
		CommitterEmail: "jane@example.com",
		CommitDate:     time.Now(),
		Message:        "feat: add new feature\n\nThis is the commit body.",
		ParentHashes:   []string{validParentHash},
		FilesChanged: []FileChange{
			{Path: "src/main.go", Status: FileStatusModified},
			{Path: "README.md", Status: FileStatusAdded},
		},
		Diff: "diff --git a/src/main.go b/src/main.go\n...",
	}
}

// TestGitCommitDocument_Validate_HappyPath tests successful validation.
func TestGitCommitDocument_Validate_HappyPath(t *testing.T) {
	doc := newValidGitCommitDocument()
	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// TestGitCommitDocument_Validate_EmptyCommitHash tests validation with missing hash.
func TestGitCommitDocument_Validate_EmptyCommitHash(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.CommitHash = ""

	err := doc.Validate()
	if err != ErrEmptyCommitHash {
		t.Errorf("expected ErrEmptyCommitHash, got %v", err)
	}
}

// TestGitCommitDocument_Validate_InvalidCommitHash tests validation with malformed hash.
func TestGitCommitDocument_Validate_InvalidCommitHash(t *testing.T) {
	tests := []struct {
		name string
		hash string
	}{
		{"too short", invalidHashShort},
		{"invalid characters", invalidHashChars},
		{"too long", validCommitHash + "extra"},
		{"with spaces", "a1b2c3d4e5f6789012345678901234567890 bcd"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := newValidGitCommitDocument()
			doc.CommitHash = tc.hash

			err := doc.Validate()
			if err != ErrInvalidCommitHash {
				t.Errorf("expected ErrInvalidCommitHash, got %v", err)
			}
		})
	}
}

// TestGitCommitDocument_Validate_EmptyAuthor tests validation with missing author.
func TestGitCommitDocument_Validate_EmptyAuthor(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.Author = ""

	err := doc.Validate()
	if err != ErrEmptyAuthor {
		t.Errorf("expected ErrEmptyAuthor, got %v", err)
	}
}

// TestGitCommitDocument_Validate_EmptyCommitter tests validation with missing committer.
func TestGitCommitDocument_Validate_EmptyCommitter(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.Committer = ""

	err := doc.Validate()
	if err != ErrEmptyCommitter {
		t.Errorf("expected ErrEmptyCommitter, got %v", err)
	}
}

// TestGitCommitDocument_Validate_EmptyMessage tests validation with missing message.
func TestGitCommitDocument_Validate_EmptyMessage(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.Message = ""

	err := doc.Validate()
	if err != ErrEmptyMessage {
		t.Errorf("expected ErrEmptyMessage, got %v", err)
	}
}

// TestGitCommitDocument_Validate_InvalidParentHash tests validation with invalid parent.
func TestGitCommitDocument_Validate_InvalidParentHash(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.ParentHashes = []string{invalidHashShort}

	err := doc.Validate()
	if err != ErrInvalidParentHash {
		t.Errorf("expected ErrInvalidParentHash, got %v", err)
	}
}

// TestGitCommitDocument_Validate_InvalidFileChange tests file change validation.
func TestGitCommitDocument_Validate_InvalidFileChange(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.FilesChanged = []FileChange{
		{Path: "", Status: FileStatusAdded},
	}

	err := doc.Validate()
	if err != ErrInvalidFileChange {
		t.Errorf("expected ErrInvalidFileChange, got %v", err)
	}
}

// TestGitCommitDocument_Validate_MissingOldPathForRename tests rename validation.
func TestGitCommitDocument_Validate_MissingOldPathForRename(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.FilesChanged = []FileChange{
		{Path: "new_name.go", Status: FileStatusRenamed, OldPath: ""},
	}

	err := doc.Validate()
	if err != ErrMissingOldPath {
		t.Errorf("expected ErrMissingOldPath, got %v", err)
	}
}

// TestGitCommitDocument_Validate_MissingOldPathForCopy tests copy validation.
func TestGitCommitDocument_Validate_MissingOldPathForCopy(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.FilesChanged = []FileChange{
		{Path: "copied.go", Status: FileStatusCopied, OldPath: ""},
	}

	err := doc.Validate()
	if err != ErrMissingOldPath {
		t.Errorf("expected ErrMissingOldPath, got %v", err)
	}
}

// TestGitCommitDocument_Validate_ValidRename tests valid rename change.
func TestGitCommitDocument_Validate_ValidRename(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.FilesChanged = []FileChange{
		{Path: "new_name.go", Status: FileStatusRenamed, OldPath: "old_name.go"},
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error for valid rename, got %v", err)
	}
}

// TestGitCommitDocument_Validate_ValidCopy tests valid copy change.
func TestGitCommitDocument_Validate_ValidCopy(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.FilesChanged = []FileChange{
		{Path: "copied.go", Status: FileStatusCopied, OldPath: "original.go"},
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error for valid copy, got %v", err)
	}
}

// TestGitCommitDocument_ValidateCommitHash tests the method variant.
func TestGitCommitDocument_ValidateCommitHash(t *testing.T) {
	doc := newValidGitCommitDocument()
	if err := doc.ValidateCommitHash(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	doc.CommitHash = ""
	if err := doc.ValidateCommitHash(); err != ErrEmptyCommitHash {
		t.Errorf("expected ErrEmptyCommitHash, got %v", err)
	}
}

// TestValidateCommitHash_Standalone tests the standalone function.
func TestValidateCommitHash_Standalone(t *testing.T) {
	tests := []struct {
		name    string
		hash    string
		wantErr error
	}{
		{"valid lowercase", validCommitHash, nil},
		{"valid uppercase", strings.ToUpper(validCommitHash), nil},
		{"valid mixed case", "A1b2C3d4E5f6789012345678901234567890aBcD", nil},
		{"empty", "", ErrEmptyCommitHash},
		{"too short", "abc123", ErrInvalidCommitHash},
		{"too long", validCommitHash + "0", ErrInvalidCommitHash},
		{"invalid char g", "g1b2c3d4e5f6789012345678901234567890abcd", ErrInvalidCommitHash},
		{"with newline", "a1b2c3d4e5f6789012345678901234567890abc\n", ErrInvalidCommitHash},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCommitHash(tc.hash)
			if err != tc.wantErr {
				t.Errorf("ValidateCommitHash(%q) = %v, want %v", tc.hash, err, tc.wantErr)
			}
		})
	}
}

// TestGitCommitDocument_IsMergeCommit tests merge commit detection.
func TestGitCommitDocument_IsMergeCommit(t *testing.T) {
	tests := []struct {
		name         string
		parentHashes []string
		want         bool
	}{
		{"no parents (initial commit)", nil, false},
		{"empty parents", []string{}, false},
		{"one parent (normal commit)", []string{validParentHash}, false},
		{"two parents (merge commit)", []string{validParentHash, validParentHash2}, true},
		{"three parents (octopus merge)", []string{validParentHash, validParentHash2, validCommitHash}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := newValidGitCommitDocument()
			doc.ParentHashes = tc.parentHashes

			got := doc.IsMergeCommit()
			if got != tc.want {
				t.Errorf("IsMergeCommit() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestGitCommitDocument_GetShortHash tests short hash extraction.
func TestGitCommitDocument_GetShortHash(t *testing.T) {
	tests := []struct {
		name       string
		commitHash string
		want       string
	}{
		{"normal hash", validCommitHash, shortHash},
		{"empty hash", "", ""},
		{"hash exactly 7 chars", "a1b2c3d", "a1b2c3d"},
		{"hash less than 7 chars", "abc", "abc"},
		{"uppercase hash", strings.ToUpper(validCommitHash), strings.ToUpper(shortHash)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := &GitCommitDocument{CommitHash: tc.commitHash}

			got := doc.GetShortHash()
			if got != tc.want {
				t.Errorf("GetShortHash() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGitCommitDocument_GetSubject tests subject line extraction.
func TestGitCommitDocument_GetSubject(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{"subject only", "feat: add feature", "feat: add feature"},
		{"subject with body", "feat: add feature\n\nThis is the body.", "feat: add feature"},
		{"subject with multiple newlines", "fix: bug\n\n\nMultiple newlines", "fix: bug"},
		{"empty message", "", ""},
		{"just newline", "\n", ""},
		{"leading newline", "\nSecond line", ""},
		{"unicode subject", "fix: corrige le bug", "fix: corrige le bug"},
		{"emoji in subject", "feat: add emoji support :rocket:", "feat: add emoji support :rocket:"},
		{"very long subject", strings.Repeat("a", 100), strings.Repeat("a", 100)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := &GitCommitDocument{Message: tc.message}

			got := doc.GetSubject()
			if got != tc.want {
				t.Errorf("GetSubject() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFileChangeStatus_IsValid tests status validation.
func TestFileChangeStatus_IsValid(t *testing.T) {
	tests := []struct {
		status FileChangeStatus
		want   bool
	}{
		{FileStatusAdded, true},
		{FileStatusModified, true},
		{FileStatusDeleted, true},
		{FileStatusRenamed, true},
		{FileStatusCopied, true},
		{FileChangeStatus("unknown"), false},
		{FileChangeStatus(""), false},
		{FileChangeStatus("ADDED"), false}, // case sensitive
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			got := tc.status.IsValid()
			if got != tc.want {
				t.Errorf("FileChangeStatus(%q).IsValid() = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// TestFileChangeStatus_String tests string conversion.
func TestFileChangeStatus_String(t *testing.T) {
	tests := []struct {
		status FileChangeStatus
		want   string
	}{
		{FileStatusAdded, "added"},
		{FileStatusModified, "modified"},
		{FileStatusDeleted, "deleted"},
		{FileStatusRenamed, "renamed"},
		{FileStatusCopied, "copied"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			got := tc.status.String()
			if got != tc.want {
				t.Errorf("FileChangeStatus.String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGitCommitDocument_EmptyFilesChanged tests validation with no file changes.
func TestGitCommitDocument_EmptyFilesChanged(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.FilesChanged = nil

	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error for empty files changed, got %v", err)
	}

	doc.FilesChanged = []FileChange{}
	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error for empty slice, got %v", err)
	}
}

// TestGitCommitDocument_EmptyParentHashes tests initial commit (no parents).
func TestGitCommitDocument_EmptyParentHashes(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.ParentHashes = nil

	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error for nil parents, got %v", err)
	}

	doc.ParentHashes = []string{}
	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error for empty parents slice, got %v", err)
	}
}

// TestGitCommitDocument_BinaryDiff tests handling of binary diff content.
func TestGitCommitDocument_BinaryDiff(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.Diff = "Binary files a/image.png and b/image.png differ"

	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error for binary diff, got %v", err)
	}
}

// TestGitCommitDocument_LargeDiff tests handling of large diff content.
func TestGitCommitDocument_LargeDiff(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.Diff = strings.Repeat("+added line\n-removed line\n", 10000)

	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error for large diff, got %v", err)
	}
}

// TestGitCommitDocument_MultipleParentHashes tests octopus merge scenario.
func TestGitCommitDocument_MultipleParentHashes(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.ParentHashes = []string{
		validParentHash,
		validParentHash2,
		validCommitHash,
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error for multiple parents, got %v", err)
	}

	if !doc.IsMergeCommit() {
		t.Error("expected IsMergeCommit() to return true for 3 parents")
	}
}

// TestGitCommitDocument_AllFileChangeStatuses tests all file status types.
func TestGitCommitDocument_AllFileChangeStatuses(t *testing.T) {
	doc := newValidGitCommitDocument()
	doc.FilesChanged = []FileChange{
		{Path: "added.go", Status: FileStatusAdded},
		{Path: "modified.go", Status: FileStatusModified},
		{Path: "deleted.go", Status: FileStatusDeleted},
		{Path: "renamed.go", Status: FileStatusRenamed, OldPath: "old_renamed.go"},
		{Path: "copied.go", Status: FileStatusCopied, OldPath: "original.go"},
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("expected no error for all file statuses, got %v", err)
	}
}

// TestGitCommitDocument_EmbeddedDocument tests embedded Document fields.
func TestGitCommitDocument_EmbeddedDocument(t *testing.T) {
	now := time.Now()
	doc := &GitCommitDocument{
		Document: Document{
			ID:         "doc-123",
			Path:       "/commits/abc123",
			Type:       DocTypeGitCommit,
			Content:    "commit content",
			Checksum:   "abc123checksum",
			ModifiedAt: now,
			IndexedAt:  now,
		},
		CommitHash: validCommitHash,
		Author:     "John Doe",
		Committer:  "Jane Smith",
		Message:    "test commit",
	}

	// Verify embedded fields are accessible
	if doc.ID != "doc-123" {
		t.Errorf("expected ID 'doc-123', got %q", doc.ID)
	}
	if doc.Path != "/commits/abc123" {
		t.Errorf("expected Path '/commits/abc123', got %q", doc.Path)
	}
	if doc.Type != DocTypeGitCommit {
		t.Errorf("expected Type DocTypeGitCommit, got %q", doc.Type)
	}
}

// TestGitCommitDocument_CommitMessageEdgeCases tests message parsing edge cases.
func TestGitCommitDocument_CommitMessageEdgeCases(t *testing.T) {
	tests := []struct {
		name            string
		message         string
		expectedSubject string
	}{
		{
			name:            "CRLF line endings",
			message:         "subject\r\n\r\nbody",
			expectedSubject: "subject\r",
		},
		{
			name:            "tab in subject",
			message:         "feat:\tadd feature",
			expectedSubject: "feat:\tadd feature",
		},
		{
			name:            "multiline body",
			message:         "subject\n\nline1\nline2\nline3",
			expectedSubject: "subject",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := &GitCommitDocument{Message: tc.message}
			got := doc.GetSubject()
			if got != tc.expectedSubject {
				t.Errorf("GetSubject() = %q, want %q", got, tc.expectedSubject)
			}
		})
	}
}
