// Package watcher provides file system and git integration for change detection
// in the Sylk Document Search System.
package watcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// =============================================================================
// Constants
// =============================================================================

// SylkHookStartMarker marks the beginning of Sylk-managed hook content.
const SylkHookStartMarker = "# SYLK_HOOK_START"

// SylkHookEndMarker marks the end of Sylk-managed hook content.
const SylkHookEndMarker = "# SYLK_HOOK_END"

// validHookTypes contains all supported git hook types.
var validHookTypes = map[string]bool{
	"post-commit":   true,
	"post-checkout": true,
	"post-merge":    true,
}

// defaultHookTypes are installed when none are specified.
var defaultHookTypes = []string{"post-commit", "post-checkout", "post-merge"}

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrRepoPathEmpty indicates the repository path was not specified.
	ErrRepoPathEmpty = errors.New("repository path cannot be empty")

	// ErrRepoPathNotExist indicates the repository path does not exist.
	ErrRepoPathNotExist = errors.New("repository path does not exist")

	// ErrNotAGitRepo indicates the path is not a git repository.
	ErrNotAGitRepo = errors.New("path is not a git repository")

	// ErrInvalidHookType indicates an unsupported hook type was specified.
	ErrInvalidHookType = errors.New("invalid hook type")

	// ErrNilEvent indicates a nil event was passed.
	ErrNilEvent = errors.New("event cannot be nil")

	// ErrInvalidArgs indicates invalid arguments for hook parsing.
	ErrInvalidArgs = errors.New("invalid arguments for hook type")
)

// =============================================================================
// GitOperation
// =============================================================================

// GitOperation represents a git operation type.
type GitOperation int

const (
	// GitCommit represents a git commit operation.
	GitCommit GitOperation = iota
	// GitCheckout represents a git checkout operation.
	GitCheckout
	// GitMerge represents a git merge operation.
	GitMerge
)

// String returns the string representation of the git operation.
func (op GitOperation) String() string {
	switch op {
	case GitCommit:
		return "commit"
	case GitCheckout:
		return "checkout"
	case GitMerge:
		return "merge"
	default:
		return "unknown"
	}
}

// =============================================================================
// GitEvent
// =============================================================================

// GitEvent represents a git hook trigger event.
type GitEvent struct {
	// Operation is the type of git operation.
	Operation GitOperation

	// CommitHash is the commit hash for commit/merge operations.
	CommitHash string

	// FromBranch is the source branch for checkout operations.
	FromBranch string

	// ToBranch is the destination branch for checkout operations.
	ToBranch string

	// Time is when the event occurred.
	Time time.Time

	// RepoPath is the path to the git repository.
	RepoPath string
}

// =============================================================================
// GitHookConfig
// =============================================================================

// GitHookConfig configures the git hook watcher.
type GitHookConfig struct {
	// RepoPath is the path to the git repository.
	RepoPath string

	// HookTypes specifies which hooks to install.
	// Valid values: "post-commit", "post-checkout", "post-merge".
	// If empty, all supported hooks are installed.
	HookTypes []string
}

// =============================================================================
// GitHookWatcher
// =============================================================================

// GitHookWatcher manages git hook integration for change detection.
type GitHookWatcher struct {
	config    GitHookConfig
	hooksDir  string
	hookTypes []string
}

// NewGitHookWatcher creates a new git hook watcher.
func NewGitHookWatcher(config GitHookConfig) (*GitHookWatcher, error) {
	if err := validateGitHookConfig(config); err != nil {
		return nil, err
	}

	hookTypes := resolveHookTypes(config.HookTypes)
	if err := validateHookTypes(hookTypes); err != nil {
		return nil, err
	}

	hooksDir := filepath.Join(config.RepoPath, ".git", "hooks")

	return &GitHookWatcher{
		config:    config,
		hooksDir:  hooksDir,
		hookTypes: hookTypes,
	}, nil
}

// =============================================================================
// Validation
// =============================================================================

// validateGitHookConfig validates the git hook configuration.
func validateGitHookConfig(config GitHookConfig) error {
	if config.RepoPath == "" {
		return ErrRepoPathEmpty
	}

	if _, err := os.Stat(config.RepoPath); os.IsNotExist(err) {
		return ErrRepoPathNotExist
	}

	gitDir := filepath.Join(config.RepoPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return ErrNotAGitRepo
	}

	return nil
}

// resolveHookTypes returns the hook types to use, defaulting if empty.
func resolveHookTypes(hookTypes []string) []string {
	if len(hookTypes) == 0 {
		return defaultHookTypes
	}
	return hookTypes
}

// validateHookTypes validates that all hook types are supported.
func validateHookTypes(hookTypes []string) error {
	for _, hookType := range hookTypes {
		if !validHookTypes[hookType] {
			return ErrInvalidHookType
		}
	}
	return nil
}

// =============================================================================
// Hook Installation
// =============================================================================

// InstallHooks installs or appends to git hooks.
// Non-invasive: preserves existing hook content by appending.
func (w *GitHookWatcher) InstallHooks() error {
	if err := w.ensureHooksDirectory(); err != nil {
		return err
	}

	for _, hookType := range w.hookTypes {
		if err := w.installSingleHook(hookType); err != nil {
			return err
		}
	}

	return nil
}

// ensureHooksDirectory creates the .git/hooks directory if it doesn't exist.
func (w *GitHookWatcher) ensureHooksDirectory() error {
	return os.MkdirAll(w.hooksDir, 0755)
}

// installSingleHook installs or updates a single hook.
func (w *GitHookWatcher) installSingleHook(hookType string) error {
	hookPath := filepath.Join(w.hooksDir, hookType)

	content, err := w.readExistingHook(hookPath)
	if err != nil {
		return err
	}

	newContent := w.buildHookContent(content, hookType)

	return w.writeHookFile(hookPath, newContent)
}

// readExistingHook reads existing hook content or returns empty string.
func (w *GitHookWatcher) readExistingHook(hookPath string) (string, error) {
	data, err := os.ReadFile(hookPath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// buildHookContent constructs the hook file content.
func (w *GitHookWatcher) buildHookContent(existing, hookType string) string {
	// Remove any existing Sylk content
	cleaned := w.removeSylkContent(existing)

	// Build Sylk hook script
	sylkScript := w.buildSylkScript(hookType)

	// Combine existing and new content
	return w.combineHookContent(cleaned, sylkScript)
}

// removeSylkContent removes existing Sylk markers and content from hook.
func (w *GitHookWatcher) removeSylkContent(content string) string {
	startIdx, endIdx := w.findSylkMarkerBounds(content)
	if startIdx == -1 || endIdx == -1 {
		return content
	}
	return content[:startIdx] + content[endIdx:]
}

// findSylkMarkerBounds locates start and end positions for Sylk content removal.
// Returns -1 for both if markers not found.
func (w *GitHookWatcher) findSylkMarkerBounds(content string) (start, end int) {
	start = strings.Index(content, SylkHookStartMarker)
	if start == -1 {
		return -1, -1
	}

	end = strings.Index(content, SylkHookEndMarker)
	if end == -1 {
		return -1, -1
	}

	end += len(SylkHookEndMarker)
	return start, w.adjustEndForNewline(content, end)
}

// adjustEndForNewline includes trailing newline in removal range if present.
func (w *GitHookWatcher) adjustEndForNewline(content string, endIdx int) int {
	if endIdx < len(content) && content[endIdx] == '\n' {
		return endIdx + 1
	}
	return endIdx
}

// buildSylkScript creates the Sylk hook script content.
func (w *GitHookWatcher) buildSylkScript(hookType string) string {
	return fmt.Sprintf(`%s
sylk index notify --hook=%s "$@"
%s
`, SylkHookStartMarker, hookType, SylkHookEndMarker)
}

// combineHookContent combines existing content with Sylk script.
func (w *GitHookWatcher) combineHookContent(existing, sylkScript string) string {
	if existing == "" {
		return "#!/bin/bash\n" + sylkScript
	}

	// Ensure existing content ends with newline
	if !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}

	return existing + sylkScript
}

// writeHookFile writes the hook content and makes it executable.
func (w *GitHookWatcher) writeHookFile(hookPath, content string) error {
	if err := os.WriteFile(hookPath, []byte(content), 0755); err != nil {
		return err
	}
	return os.Chmod(hookPath, 0755)
}

// =============================================================================
// Hook Uninstallation
// =============================================================================

// UninstallHooks removes the installed hook entries.
func (w *GitHookWatcher) UninstallHooks() error {
	for _, hookType := range w.hookTypes {
		if err := w.uninstallSingleHook(hookType); err != nil {
			return err
		}
	}
	return nil
}

// uninstallSingleHook removes Sylk content from a single hook.
func (w *GitHookWatcher) uninstallSingleHook(hookType string) error {
	hookPath := filepath.Join(w.hooksDir, hookType)

	content, err := w.readExistingHook(hookPath)
	if err != nil || content == "" {
		return nil // Hook doesn't exist, nothing to do
	}

	cleaned := w.removeSylkContent(content)

	return w.handleCleanedHook(hookPath, cleaned)
}

// handleCleanedHook decides whether to update or remove the hook file.
func (w *GitHookWatcher) handleCleanedHook(hookPath, content string) error {
	if w.isEffectivelyEmpty(content) {
		return os.Remove(hookPath)
	}
	return w.writeHookFile(hookPath, content)
}

// isEffectivelyEmpty checks if content is just shebang and whitespace.
func (w *GitHookWatcher) isEffectivelyEmpty(content string) bool {
	trimmed := strings.TrimSpace(content)
	trimmed = strings.TrimPrefix(trimmed, "#!/bin/bash")
	trimmed = strings.TrimPrefix(trimmed, "#!/bin/sh")
	return strings.TrimSpace(trimmed) == ""
}

// =============================================================================
// Hook Event Parsing
// =============================================================================

// ParseHookEvent parses hook arguments into a GitEvent.
// Args format depends on hook type:
//   - post-commit: no args
//   - post-checkout: <prev-head> <new-head> <branch-flag>
//   - post-merge: <squash-flag>
func (w *GitHookWatcher) ParseHookEvent(hookType string, args []string) (*GitEvent, error) {
	if !validHookTypes[hookType] {
		return nil, ErrInvalidHookType
	}

	event := &GitEvent{
		RepoPath: w.config.RepoPath,
		Time:     time.Now(),
	}

	return w.parseByHookType(hookType, args, event)
}

// parseByHookType dispatches to the appropriate parser.
func (w *GitHookWatcher) parseByHookType(hookType string, args []string, event *GitEvent) (*GitEvent, error) {
	switch hookType {
	case "post-commit":
		return w.parsePostCommit(event)
	case "post-checkout":
		return w.parsePostCheckout(args, event)
	case "post-merge":
		return w.parsePostMerge(event)
	default:
		return nil, ErrInvalidHookType
	}
}

// parsePostCommit handles post-commit hook events.
func (w *GitHookWatcher) parsePostCommit(event *GitEvent) (*GitEvent, error) {
	event.Operation = GitCommit

	hash, err := w.getCurrentCommitHash()
	if err != nil {
		return nil, err
	}
	event.CommitHash = hash

	return event, nil
}

// parsePostCheckout handles post-checkout hook events.
func (w *GitHookWatcher) parsePostCheckout(args []string, event *GitEvent) (*GitEvent, error) {
	if len(args) < 3 {
		return nil, ErrInvalidArgs
	}

	event.Operation = GitCheckout
	event.FromBranch = args[0]
	event.ToBranch = args[1]
	event.CommitHash = args[1]

	return event, nil
}

// parsePostMerge handles post-merge hook events.
func (w *GitHookWatcher) parsePostMerge(event *GitEvent) (*GitEvent, error) {
	event.Operation = GitMerge

	hash, err := w.getCurrentCommitHash()
	if err != nil {
		return nil, err
	}
	event.CommitHash = hash

	return event, nil
}

// getCurrentCommitHash returns the current HEAD commit hash.
func (w *GitHookWatcher) getCurrentCommitHash() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = w.config.RepoPath

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

// =============================================================================
// Affected Files
// =============================================================================

// GetAffectedFiles returns files changed by the git operation.
// Uses git diff for commits/merges, git diff-tree for checkouts.
func (w *GitHookWatcher) GetAffectedFiles(event *GitEvent) ([]string, error) {
	if event == nil {
		return nil, ErrNilEvent
	}

	switch event.Operation {
	case GitCommit, GitMerge:
		return w.getCommitAffectedFiles(event)
	case GitCheckout:
		return w.getCheckoutAffectedFiles(event)
	default:
		return nil, fmt.Errorf("unsupported operation: %v", event.Operation)
	}
}

// getCommitAffectedFiles returns files changed in a commit.
func (w *GitHookWatcher) getCommitAffectedFiles(event *GitEvent) ([]string, error) {
	cmd := exec.Command("git", "diff-tree", "--no-commit-id", "--name-only", "-r", event.CommitHash)
	cmd.Dir = event.RepoPath

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commit files: %w", err)
	}

	return w.parseFileList(event.RepoPath, out), nil
}

// getCheckoutAffectedFiles returns files changed between branches.
func (w *GitHookWatcher) getCheckoutAffectedFiles(event *GitEvent) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", event.FromBranch, event.ToBranch)
	cmd.Dir = event.RepoPath

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get checkout files: %w", err)
	}

	return w.parseFileList(event.RepoPath, out), nil
}

// parseFileList converts git output to absolute file paths.
func (w *GitHookWatcher) parseFileList(repoPath string, output []byte) []string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	files := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, filepath.Join(repoPath, line))
	}

	return files
}
