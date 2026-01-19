// Package skills provides skill definitions and execution for agent capabilities.
// This file defines search-related skills: search_code, git_diff, git_log, git_blame.
package skills

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/git"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrNilSearchSystem indicates the search system is nil.
	ErrNilSearchSystem = errors.New("search system is nil")

	// ErrNilGitManager indicates the git manager is nil.
	ErrNilGitManager = errors.New("git manager is nil")

	// ErrSearchNotRunning indicates the search system is not running.
	ErrSearchNotRunning = errors.New("search system is not running")

	// ErrGitNotAvailable indicates git is not available.
	ErrGitNotAvailable = errors.New("git is not available")

	// ErrEmptySearchQuery indicates an empty search query was provided.
	ErrEmptySearchQuery = errors.New("search query cannot be empty")

	// ErrEmptyFilePath indicates an empty file path was provided.
	ErrEmptyFilePath = errors.New("file path cannot be empty")

	// ErrInvalidCommitRef indicates an invalid commit reference.
	ErrInvalidCommitRef = errors.New("invalid commit reference")

	// ErrSkillExecutionFailed indicates skill execution failed.
	ErrSkillExecutionFailed = errors.New("skill execution failed")
)

// =============================================================================
// SearchSystem Interface
// =============================================================================

// SearchSystemInterface defines the interface for search operations.
// This allows for mocking in tests.
type SearchSystemInterface interface {
	IsRunning() bool
	Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error)
}

// GitManagerInterface defines the interface for git operations.
// This allows for mocking in tests.
type GitManagerInterface interface {
	IsAvailable() bool
	GetBlame(ctx context.Context, path string) (*git.BlameResult, error)
	GetDiffBetween(ctx context.Context, from, to string) ([]git.FileDiff, error)
	GetFileHistory(ctx context.Context, path string, limit int) ([]*git.CommitInfo, error)
}

// =============================================================================
// Skill Definitions
// =============================================================================

// SearchCodeSkill returns the skill definition for search_code.
func SearchCodeSkill() Skill {
	return Skill{
		Name:        "search-code",
		Description: "Search code and documentation using full-text and semantic search. Accepts a query string and returns matching files with relevance scores.",
		AllowedTools: "Read,Grep,Glob",
		Metadata: map[string]string{
			"category": "search",
			"version":  "1.0.0",
		},
	}
}

// GitDiffSkill returns the skill definition for git_diff.
func GitDiffSkill() Skill {
	return Skill{
		Name:        "git-diff",
		Description: "Show changes between two commits or between a commit and the working tree. Returns file-by-file diff information including additions, deletions, and change hunks.",
		AllowedTools: "Bash",
		Metadata: map[string]string{
			"category": "git",
			"version":  "1.0.0",
		},
	}
}

// GitLogSkill returns the skill definition for git_log.
func GitLogSkill() Skill {
	return Skill{
		Name:        "git-log",
		Description: "Show commit history for a file or the entire repository. Returns commit information including hash, author, date, and message.",
		AllowedTools: "Bash",
		Metadata: map[string]string{
			"category": "git",
			"version":  "1.0.0",
		},
	}
}

// GitBlameSkill returns the skill definition for git_blame.
func GitBlameSkill() Skill {
	return Skill{
		Name:        "git-blame",
		Description: "Show line-by-line authorship information for a file. Returns the commit hash, author, and date for each line.",
		AllowedTools: "Bash",
		Metadata: map[string]string{
			"category": "git",
			"version":  "1.0.0",
		},
	}
}

// AllSearchSkills returns all search-related skill definitions.
func AllSearchSkills() []Skill {
	return []Skill{
		SearchCodeSkill(),
		GitDiffSkill(),
		GitLogSkill(),
		GitBlameSkill(),
	}
}

// =============================================================================
// Search Code Executor
// =============================================================================

// SearchCodeParams contains parameters for the search_code skill.
type SearchCodeParams struct {
	// Query is the search query string.
	Query string

	// Limit is the maximum number of results (default: 20).
	Limit int

	// Type filters by document type (optional).
	Type search.DocumentType

	// PathFilter filters by path prefix (optional).
	PathFilter string

	// FuzzyLevel sets fuzzy matching (0-2, default: 0).
	FuzzyLevel int

	// IncludeHighlights enables result highlighting.
	IncludeHighlights bool
}

// SearchCodeResult contains results from the search_code skill.
type SearchCodeResult struct {
	// Results contains the search results.
	Results []search.ScoredDocument

	// TotalHits is the total number of matching documents.
	TotalHits int64

	// Duration is how long the search took.
	Duration time.Duration

	// Query is the original query.
	Query string
}

// SearchCodeExecutor executes the search_code skill.
type SearchCodeExecutor struct {
	searchSystem SearchSystemInterface
}

// NewSearchCodeExecutor creates a new SearchCodeExecutor.
func NewSearchCodeExecutor(searchSystem SearchSystemInterface) *SearchCodeExecutor {
	return &SearchCodeExecutor{
		searchSystem: searchSystem,
	}
}

// Execute performs a code search with the given parameters.
func (e *SearchCodeExecutor) Execute(ctx context.Context, params SearchCodeParams) (*SearchCodeResult, error) {
	if err := e.validate(params); err != nil {
		return nil, err
	}

	req := e.buildRequest(params)

	result, err := e.searchSystem.Search(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSkillExecutionFailed, err)
	}

	return &SearchCodeResult{
		Results:   result.Documents,
		TotalHits: result.TotalHits,
		Duration:  result.SearchTime,
		Query:     params.Query,
	}, nil
}

func (e *SearchCodeExecutor) validate(params SearchCodeParams) error {
	if e.searchSystem == nil {
		return ErrNilSearchSystem
	}
	if !e.searchSystem.IsRunning() {
		return ErrSearchNotRunning
	}
	if params.Query == "" {
		return ErrEmptySearchQuery
	}
	return nil
}

func (e *SearchCodeExecutor) buildRequest(params SearchCodeParams) *search.SearchRequest {
	limit := params.Limit
	if limit <= 0 {
		limit = search.DefaultLimit
	}

	return &search.SearchRequest{
		Query:             params.Query,
		Limit:             limit,
		Type:              params.Type,
		PathFilter:        params.PathFilter,
		FuzzyLevel:        params.FuzzyLevel,
		IncludeHighlights: params.IncludeHighlights,
	}
}

// =============================================================================
// Git Diff Executor
// =============================================================================

// GitDiffParams contains parameters for the git_diff skill.
type GitDiffParams struct {
	// FromCommit is the starting commit reference (e.g., commit hash, branch, HEAD~1).
	FromCommit string

	// ToCommit is the ending commit reference (default: HEAD or working tree).
	ToCommit string

	// Path filters diff to a specific file or directory (optional).
	Path string
}

// GitDiffResult contains results from the git_diff skill.
type GitDiffResult struct {
	// Files contains the diff for each changed file.
	Files []git.FileDiff

	// FromCommit is the starting commit used.
	FromCommit string

	// ToCommit is the ending commit used.
	ToCommit string

	// TotalAdditions is the total number of lines added.
	TotalAdditions int

	// TotalDeletions is the total number of lines deleted.
	TotalDeletions int
}

// GitDiffExecutor executes the git_diff skill.
type GitDiffExecutor struct {
	gitManager GitManagerInterface
}

// NewGitDiffExecutor creates a new GitDiffExecutor.
func NewGitDiffExecutor(gitManager GitManagerInterface) *GitDiffExecutor {
	return &GitDiffExecutor{
		gitManager: gitManager,
	}
}

// Execute performs a git diff with the given parameters.
func (e *GitDiffExecutor) Execute(ctx context.Context, params GitDiffParams) (*GitDiffResult, error) {
	if err := e.validate(params); err != nil {
		return nil, err
	}

	params = e.applyDefaults(params)

	diffs, err := e.gitManager.GetDiffBetween(ctx, params.FromCommit, params.ToCommit)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSkillExecutionFailed, err)
	}

	// Filter by path if specified
	if params.Path != "" {
		diffs = filterDiffsByPath(diffs, params.Path)
	}

	result := &GitDiffResult{
		Files:      diffs,
		FromCommit: params.FromCommit,
		ToCommit:   params.ToCommit,
	}

	// Calculate totals
	for _, diff := range diffs {
		result.TotalAdditions += diff.Additions
		result.TotalDeletions += diff.Deletions
	}

	return result, nil
}

func (e *GitDiffExecutor) validate(params GitDiffParams) error {
	if e.gitManager == nil {
		return ErrNilGitManager
	}
	if !e.gitManager.IsAvailable() {
		return ErrGitNotAvailable
	}
	if params.FromCommit == "" {
		return ErrInvalidCommitRef
	}
	return nil
}

func (e *GitDiffExecutor) applyDefaults(params GitDiffParams) GitDiffParams {
	if params.ToCommit == "" {
		params.ToCommit = "HEAD"
	}
	return params
}

func filterDiffsByPath(diffs []git.FileDiff, path string) []git.FileDiff {
	filtered := make([]git.FileDiff, 0, len(diffs))
	for _, diff := range diffs {
		if matchesPath(diff.Path, path) || matchesPath(diff.OldPath, path) {
			filtered = append(filtered, diff)
		}
	}
	return filtered
}

func matchesPath(filePath, filterPath string) bool {
	if filePath == "" || filterPath == "" {
		return false
	}
	// Exact match or prefix match for directory filtering
	return filePath == filterPath ||
		len(filePath) > len(filterPath) && filePath[:len(filterPath)] == filterPath && filePath[len(filterPath)] == '/'
}

// =============================================================================
// Git Log Executor
// =============================================================================

// GitLogParams contains parameters for the git_log skill.
type GitLogParams struct {
	// Path filters log to a specific file (optional).
	Path string

	// Limit is the maximum number of commits to return (default: 10).
	Limit int

	// Since filters commits after this date (optional).
	Since *time.Time

	// Until filters commits before this date (optional).
	Until *time.Time

	// Author filters by author name or email (optional).
	Author string
}

// GitLogResult contains results from the git_log skill.
type GitLogResult struct {
	// Commits contains the commit history.
	Commits []*git.CommitInfo

	// Path is the file path if filtering was applied.
	Path string

	// TotalCommits is the number of commits returned.
	TotalCommits int
}

// GitLogExecutor executes the git_log skill.
type GitLogExecutor struct {
	gitManager GitManagerInterface
}

// NewGitLogExecutor creates a new GitLogExecutor.
func NewGitLogExecutor(gitManager GitManagerInterface) *GitLogExecutor {
	return &GitLogExecutor{
		gitManager: gitManager,
	}
}

// Execute performs a git log with the given parameters.
func (e *GitLogExecutor) Execute(ctx context.Context, params GitLogParams) (*GitLogResult, error) {
	if err := e.validate(); err != nil {
		return nil, err
	}

	params = e.applyDefaults(params)

	commits, err := e.gitManager.GetFileHistory(ctx, params.Path, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSkillExecutionFailed, err)
	}

	// Apply additional filters
	commits = e.filterCommits(commits, params)

	return &GitLogResult{
		Commits:      commits,
		Path:         params.Path,
		TotalCommits: len(commits),
	}, nil
}

func (e *GitLogExecutor) validate() error {
	if e.gitManager == nil {
		return ErrNilGitManager
	}
	if !e.gitManager.IsAvailable() {
		return ErrGitNotAvailable
	}
	return nil
}

func (e *GitLogExecutor) applyDefaults(params GitLogParams) GitLogParams {
	if params.Limit <= 0 {
		params.Limit = 10
	}
	return params
}

func (e *GitLogExecutor) filterCommits(commits []*git.CommitInfo, params GitLogParams) []*git.CommitInfo {
	filtered := make([]*git.CommitInfo, 0, len(commits))

	for _, commit := range commits {
		if e.matchesFilters(commit, params) {
			filtered = append(filtered, commit)
		}
	}

	return filtered
}

func (e *GitLogExecutor) matchesFilters(commit *git.CommitInfo, params GitLogParams) bool {
	// Filter by date range
	if params.Since != nil && commit.AuthorTime.Before(*params.Since) {
		return false
	}
	if params.Until != nil && commit.AuthorTime.After(*params.Until) {
		return false
	}

	// Filter by author
	if params.Author != "" {
		if !containsIgnoreCase(commit.Author, params.Author) &&
			!containsIgnoreCase(commit.AuthorEmail, params.Author) {
			return false
		}
	}

	return true
}

func containsIgnoreCase(s, substr string) bool {
	sLower := toLower(s)
	substrLower := toLower(substr)
	return len(s) >= len(substr) && contains(sLower, substrLower)
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// =============================================================================
// Git Blame Executor
// =============================================================================

// GitBlameParams contains parameters for the git_blame skill.
type GitBlameParams struct {
	// Path is the file path to blame.
	Path string

	// StartLine is the starting line number (1-indexed, optional).
	StartLine int

	// EndLine is the ending line number (1-indexed, optional).
	EndLine int
}

// GitBlameResult contains results from the git_blame skill.
type GitBlameResult struct {
	// Path is the file path.
	Path string

	// Lines contains blame information for each line.
	Lines []git.BlameLine

	// UniqueAuthors is the count of unique authors.
	UniqueAuthors int

	// UniqueCommits is the count of unique commits.
	UniqueCommits int
}

// GitBlameExecutor executes the git_blame skill.
type GitBlameExecutor struct {
	gitManager GitManagerInterface
}

// NewGitBlameExecutor creates a new GitBlameExecutor.
func NewGitBlameExecutor(gitManager GitManagerInterface) *GitBlameExecutor {
	return &GitBlameExecutor{
		gitManager: gitManager,
	}
}

// Execute performs a git blame with the given parameters.
func (e *GitBlameExecutor) Execute(ctx context.Context, params GitBlameParams) (*GitBlameResult, error) {
	if err := e.validate(params); err != nil {
		return nil, err
	}

	blameResult, err := e.gitManager.GetBlame(ctx, params.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSkillExecutionFailed, err)
	}

	lines := blameResult.Lines

	// Apply line range filter
	if params.StartLine > 0 || params.EndLine > 0 {
		lines = filterLineRange(lines, params.StartLine, params.EndLine)
	}

	// Calculate unique authors and commits
	authors := make(map[string]bool)
	commits := make(map[string]bool)
	for _, line := range lines {
		authors[line.Author] = true
		commits[line.CommitHash] = true
	}

	return &GitBlameResult{
		Path:          params.Path,
		Lines:         lines,
		UniqueAuthors: len(authors),
		UniqueCommits: len(commits),
	}, nil
}

func (e *GitBlameExecutor) validate(params GitBlameParams) error {
	if e.gitManager == nil {
		return ErrNilGitManager
	}
	if !e.gitManager.IsAvailable() {
		return ErrGitNotAvailable
	}
	if params.Path == "" {
		return ErrEmptyFilePath
	}
	return nil
}

func filterLineRange(lines []git.BlameLine, start, end int) []git.BlameLine {
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) {
		return []git.BlameLine{}
	}

	// Convert to 0-indexed
	startIdx := start - 1
	endIdx := end

	if startIdx >= endIdx {
		return []git.BlameLine{}
	}

	return lines[startIdx:endIdx]
}

// =============================================================================
// Skill Executor Factory
// =============================================================================

// SkillExecutors holds all skill executors.
type SkillExecutors struct {
	SearchCode *SearchCodeExecutor
	GitDiff    *GitDiffExecutor
	GitLog     *GitLogExecutor
	GitBlame   *GitBlameExecutor
}

// NewSkillExecutors creates all skill executors with the given dependencies.
func NewSkillExecutors(searchSystem SearchSystemInterface, gitManager GitManagerInterface) *SkillExecutors {
	return &SkillExecutors{
		SearchCode: NewSearchCodeExecutor(searchSystem),
		GitDiff:    NewGitDiffExecutor(gitManager),
		GitLog:     NewGitLogExecutor(gitManager),
		GitBlame:   NewGitBlameExecutor(gitManager),
	}
}
