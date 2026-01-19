package git

import (
	"strconv"
	"strings"
	"time"
)

// =============================================================================
// ListModifiedFiles
// =============================================================================

// ListModifiedFiles returns files modified since the given time.
// Returns file paths relative to the repository root.
func (c *GitClient) ListModifiedFiles(since time.Time) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getModifiedFilesSince(since)
}

// getModifiedFilesSince retrieves files changed since a timestamp.
func (c *GitClient) getModifiedFilesSince(since time.Time) ([]string, error) {
	commits, err := c.getCommitsSinceInternal(since)
	if err != nil {
		return nil, err
	}

	return c.collectFilesFromCommits(commits), nil
}

// collectFilesFromCommits extracts unique file paths from commits.
func (c *GitClient) collectFilesFromCommits(commits []*CommitInfo) []string {
	seen := make(map[string]bool)
	files := make([]string, 0)

	for _, commit := range commits {
		for _, file := range commit.FilesChanged {
			if !seen[file] {
				seen[file] = true
				files = append(files, file)
			}
		}
	}

	return files
}

// =============================================================================
// ListModifiedFilesSinceCommit
// =============================================================================

// ListModifiedFilesSinceCommit returns files changed since a commit.
// The commit hash can be full or abbreviated.
func (c *GitClient) ListModifiedFilesSinceCommit(commitHash string) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	if commitHash == "" {
		return nil, ErrInvalidCommit
	}

	return c.getModifiedSinceCommit(commitHash)
}

// getModifiedSinceCommit uses git diff to find changed files.
func (c *GitClient) getModifiedSinceCommit(commitHash string) ([]string, error) {
	output, err := c.runGitCommand("diff", "--name-only", commitHash+"..HEAD")
	if err != nil {
		return nil, err
	}

	return c.parseFileList(output), nil
}

// parseFileList parses newline-separated file list from git output.
func (c *GitClient) parseFileList(output string) []string {
	output = strings.TrimSpace(output)
	if output == "" {
		return []string{}
	}

	lines := strings.Split(output, "\n")
	files := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}

	return files
}

// =============================================================================
// GetCommitsSince
// =============================================================================

// GetCommitsSince returns commits since a time.
// Results are ordered from newest to oldest.
func (c *GitClient) GetCommitsSince(since time.Time) ([]*CommitInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getCommitsSinceInternal(since)
}

// getCommitsSinceInternal retrieves commits since a time without locking.
func (c *GitClient) getCommitsSinceInternal(since time.Time) ([]*CommitInfo, error) {
	// Check if HEAD exists (repo has commits)
	if !c.hasHead() {
		return []*CommitInfo{}, nil
	}

	sinceStr := since.Format(time.RFC3339)

	output, err := c.runGitCommand(
		"log",
		"--format=%H|%h|%an|%ae|%ai|%cn|%ce|%ci|%s|%P",
		"--since="+sinceStr,
	)
	if err != nil {
		return nil, err
	}

	return c.parseCommitLog(output)
}

// hasHead checks if the repository has any commits.
func (c *GitClient) hasHead() bool {
	_, err := c.runGitCommand("rev-parse", "--verify", "HEAD")
	return err == nil
}

// parseCommitLog parses the git log output into CommitInfo structs.
func (c *GitClient) parseCommitLog(output string) ([]*CommitInfo, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return []*CommitInfo{}, nil
	}

	lines := strings.Split(output, "\n")
	commits := make([]*CommitInfo, 0, len(lines))

	for _, line := range lines {
		commit, err := c.parseCommitLine(line)
		if err != nil {
			continue // Skip malformed lines
		}
		commits = append(commits, commit)
	}

	return commits, nil
}

// parseCommitLine parses a single log line into CommitInfo.
func (c *GitClient) parseCommitLine(line string) (*CommitInfo, error) {
	parts := strings.SplitN(line, "|", 10)
	if len(parts) < 10 {
		return nil, ErrInvalidCommit
	}

	commit := &CommitInfo{
		Hash:           parts[0],
		ShortHash:      parts[1],
		Author:         parts[2],
		AuthorEmail:    parts[3],
		Committer:      parts[5],
		CommitterEmail: parts[6],
		Subject:        parts[8],
	}

	if err := c.parseCommitTimes(commit, parts[4], parts[7]); err != nil {
		return nil, err
	}

	c.parseParentHashes(commit, parts[9])
	c.addFilesToCommit(commit)

	return commit, nil
}

// parseCommitTimes parses author and commit times.
func (c *GitClient) parseCommitTimes(commit *CommitInfo, authorTime, commitTime string) error {
	at, err := time.Parse("2006-01-02 15:04:05 -0700", authorTime)
	if err == nil {
		commit.AuthorTime = at
	}

	ct, err := time.Parse("2006-01-02 15:04:05 -0700", commitTime)
	if err == nil {
		commit.CommitTime = ct
	}

	return nil
}

// parseParentHashes extracts parent commit hashes.
func (c *GitClient) parseParentHashes(commit *CommitInfo, parents string) {
	parents = strings.TrimSpace(parents)
	if parents == "" {
		commit.ParentHashes = []string{}
		return
	}

	commit.ParentHashes = strings.Split(parents, " ")
}

// addFilesToCommit populates FilesChanged for a commit.
func (c *GitClient) addFilesToCommit(commit *CommitInfo) {
	files, err := c.getFilesInCommitInternal(commit.Hash)
	if err == nil {
		commit.FilesChanged = files
	}
}

// =============================================================================
// GetFilesInCommit
// =============================================================================

// GetFilesInCommit returns files changed in a specific commit.
func (c *GitClient) GetFilesInCommit(commitHash string) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	if commitHash == "" {
		return nil, ErrInvalidCommit
	}

	return c.getFilesInCommitInternal(commitHash)
}

// getFilesInCommitInternal gets files without locking.
// Uses --root to handle initial commits without parents.
func (c *GitClient) getFilesInCommitInternal(commitHash string) ([]string, error) {
	output, err := c.runGitCommand(
		"diff-tree",
		"--no-commit-id",
		"--name-only",
		"-r",
		"--root",
		commitHash,
	)
	if err != nil {
		return nil, err
	}

	return c.parseFileList(output), nil
}

// =============================================================================
// GetAllCommits
// =============================================================================

// GetAllCommits returns all commits (limited by count).
// If limit is 0 or negative, returns all commits.
// Results are ordered from newest to oldest.
func (c *GitClient) GetAllCommits(limit int) ([]*CommitInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getAllCommitsInternal(limit)
}

// getAllCommitsInternal retrieves all commits without locking.
func (c *GitClient) getAllCommitsInternal(limit int) ([]*CommitInfo, error) {
	// Check if HEAD exists (repo has commits)
	if !c.hasHead() {
		return []*CommitInfo{}, nil
	}

	args := []string{
		"log",
		"--format=%H|%h|%an|%ae|%ai|%cn|%ce|%ci|%s|%P",
	}

	if limit > 0 {
		args = append(args, "-n", formatInt(limit))
	}

	output, err := c.runGitCommand(args...)
	if err != nil {
		return nil, err
	}

	return c.parseCommitLogNoFiles(output)
}

// formatInt converts an int to string.
func formatInt(n int) string {
	return strconv.Itoa(n)
}

// parseCommitLogNoFiles parses log without adding files.
func (c *GitClient) parseCommitLogNoFiles(output string) ([]*CommitInfo, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return []*CommitInfo{}, nil
	}

	lines := strings.Split(output, "\n")
	commits := make([]*CommitInfo, 0, len(lines))

	for _, line := range lines {
		commit, err := c.parseCommitLineNoFiles(line)
		if err != nil {
			continue
		}
		commits = append(commits, commit)
	}

	return commits, nil
}

// parseCommitLineNoFiles parses a commit line without files.
func (c *GitClient) parseCommitLineNoFiles(line string) (*CommitInfo, error) {
	parts := strings.SplitN(line, "|", 10)
	if len(parts) < 10 {
		return nil, ErrInvalidCommit
	}

	commit := &CommitInfo{
		Hash:           parts[0],
		ShortHash:      parts[1],
		Author:         parts[2],
		AuthorEmail:    parts[3],
		Committer:      parts[5],
		CommitterEmail: parts[6],
		Subject:        parts[8],
	}

	_ = c.parseCommitTimes(commit, parts[4], parts[7])
	c.parseParentHashes(commit, parts[9])

	return commit, nil
}

// =============================================================================
// GetUncommittedFiles
// =============================================================================

// GetUncommittedFiles returns files with uncommitted changes.
// This includes both staged and unstaged modifications.
func (c *GitClient) GetUncommittedFiles() ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getUncommittedFilesInternal()
}

// getUncommittedFilesInternal gets uncommitted files without locking.
func (c *GitClient) getUncommittedFilesInternal() ([]string, error) {
	output, err := c.runGitCommand("status", "--porcelain")
	if err != nil {
		return nil, err
	}

	return c.parseStatusOutput(output, false), nil
}

// parseStatusOutput parses git status --porcelain output.
// If untrackedOnly is true, only returns untracked files.
func (c *GitClient) parseStatusOutput(output string, untrackedOnly bool) []string {
	// Only trim trailing whitespace to preserve leading status characters
	output = strings.TrimRight(output, " \t\n\r")
	if output == "" {
		return []string{}
	}

	lines := strings.Split(output, "\n")
	files := make([]string, 0, len(lines))

	for _, line := range lines {
		file := c.parseStatusLine(line, untrackedOnly)
		if file != "" {
			files = append(files, file)
		}
	}

	return files
}

// parseStatusLine extracts file path from a status line.
// Git porcelain format: "XY filename" where X is index status, Y is work tree status.
func (c *GitClient) parseStatusLine(line string, untrackedOnly bool) string {
	if len(line) < 4 {
		return ""
	}

	// Status is first 2 characters
	status := line[:2]

	// Path starts after "XY " (index 3)
	// Handle renamed files which have format "XY oldname -> newname"
	pathPart := line[3:]
	path := strings.TrimSpace(pathPart)

	// For renamed files, extract the new name
	if idx := strings.Index(path, " -> "); idx >= 0 {
		path = path[idx+4:]
	}

	if untrackedOnly {
		if status == "??" {
			return path
		}
		return ""
	}

	// Return any file with changes
	return path
}

// =============================================================================
// GetUntrackedFiles
// =============================================================================

// GetUntrackedFiles returns untracked files.
// Ignored files are excluded.
func (c *GitClient) GetUntrackedFiles() ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getUntrackedFilesInternal()
}

// getUntrackedFilesInternal gets untracked files without locking.
// Uses -u to show individual untracked files in directories.
func (c *GitClient) getUntrackedFilesInternal() ([]string, error) {
	output, err := c.runGitCommand("status", "--porcelain", "-u")
	if err != nil {
		return nil, err
	}

	return c.parseStatusOutput(output, true), nil
}
