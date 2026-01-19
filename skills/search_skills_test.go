package skills

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock Implementations
// =============================================================================

// MockSearchSystem is a mock implementation of SearchSystemInterface.
type MockSearchSystem struct {
	mock.Mock
}

func (m *MockSearchSystem) IsRunning() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockSearchSystem) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*search.SearchResult), args.Error(1)
}

// MockGitManager is a mock implementation of GitManagerInterface.
type MockGitManager struct {
	mock.Mock
}

func (m *MockGitManager) IsAvailable() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockGitManager) GetBlame(ctx context.Context, path string) (*git.BlameResult, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*git.BlameResult), args.Error(1)
}

func (m *MockGitManager) GetDiffBetween(ctx context.Context, from, to string) ([]git.FileDiff, error) {
	args := m.Called(ctx, from, to)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]git.FileDiff), args.Error(1)
}

func (m *MockGitManager) GetFileHistory(ctx context.Context, path string, limit int) ([]*git.CommitInfo, error) {
	args := m.Called(ctx, path, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*git.CommitInfo), args.Error(1)
}

// =============================================================================
// Skill Definition Tests
// =============================================================================

func TestSearchCodeSkill(t *testing.T) {
	skill := SearchCodeSkill()

	assert.Equal(t, "search-code", skill.Name)
	assert.NotEmpty(t, skill.Description)
	assert.Contains(t, skill.Description, "search")
	assert.Equal(t, "Read,Grep,Glob", skill.AllowedTools)
	assert.Equal(t, "search", skill.Metadata["category"])
	assert.Equal(t, "1.0.0", skill.Metadata["version"])
}

func TestGitDiffSkill(t *testing.T) {
	skill := GitDiffSkill()

	assert.Equal(t, "git-diff", skill.Name)
	assert.NotEmpty(t, skill.Description)
	assert.Contains(t, skill.Description, "diff")
	assert.Equal(t, "Bash", skill.AllowedTools)
	assert.Equal(t, "git", skill.Metadata["category"])
}

func TestGitLogSkill(t *testing.T) {
	skill := GitLogSkill()

	assert.Equal(t, "git-log", skill.Name)
	assert.NotEmpty(t, skill.Description)
	assert.Contains(t, skill.Description, "commit")
	assert.Equal(t, "Bash", skill.AllowedTools)
	assert.Equal(t, "git", skill.Metadata["category"])
}

func TestGitBlameSkill(t *testing.T) {
	skill := GitBlameSkill()

	assert.Equal(t, "git-blame", skill.Name)
	assert.NotEmpty(t, skill.Description)
	assert.Contains(t, skill.Description, "authorship")
	assert.Equal(t, "Bash", skill.AllowedTools)
	assert.Equal(t, "git", skill.Metadata["category"])
}

func TestAllSearchSkills(t *testing.T) {
	skills := AllSearchSkills()

	assert.Len(t, skills, 4)

	names := make(map[string]bool)
	for _, s := range skills {
		names[s.Name] = true
	}

	assert.True(t, names["search-code"])
	assert.True(t, names["git-diff"])
	assert.True(t, names["git-log"])
	assert.True(t, names["git-blame"])
}

// =============================================================================
// SearchCodeExecutor Tests
// =============================================================================

func TestSearchCodeExecutor_Execute_Success(t *testing.T) {
	mockSearch := new(MockSearchSystem)
	executor := NewSearchCodeExecutor(mockSearch)

	expectedDocs := []search.ScoredDocument{
		{Document: search.Document{ID: "doc-1", Path: "/file1.go"}, Score: 0.95},
		{Document: search.Document{ID: "doc-2", Path: "/file2.go"}, Score: 0.85},
	}

	mockSearch.On("IsRunning").Return(true)
	mockSearch.On("Search", mock.Anything, mock.MatchedBy(func(req *search.SearchRequest) bool {
		return req.Query == "test query" && req.Limit == 20
	})).Return(&search.SearchResult{
		Documents:  expectedDocs,
		TotalHits:  100,
		SearchTime: 50 * time.Millisecond,
	}, nil)

	result, err := executor.Execute(context.Background(), SearchCodeParams{
		Query: "test query",
		Limit: 20,
	})

	require.NoError(t, err)
	assert.Equal(t, expectedDocs, result.Results)
	assert.Equal(t, int64(100), result.TotalHits)
	assert.Equal(t, 50*time.Millisecond, result.Duration)
	assert.Equal(t, "test query", result.Query)

	mockSearch.AssertExpectations(t)
}

func TestSearchCodeExecutor_Execute_DefaultLimit(t *testing.T) {
	mockSearch := new(MockSearchSystem)
	executor := NewSearchCodeExecutor(mockSearch)

	mockSearch.On("IsRunning").Return(true)
	mockSearch.On("Search", mock.Anything, mock.MatchedBy(func(req *search.SearchRequest) bool {
		return req.Limit == search.DefaultLimit
	})).Return(&search.SearchResult{}, nil)

	_, err := executor.Execute(context.Background(), SearchCodeParams{
		Query: "test",
		Limit: 0, // Should use default
	})

	require.NoError(t, err)
	mockSearch.AssertExpectations(t)
}

func TestSearchCodeExecutor_Execute_WithFilters(t *testing.T) {
	mockSearch := new(MockSearchSystem)
	executor := NewSearchCodeExecutor(mockSearch)

	mockSearch.On("IsRunning").Return(true)
	mockSearch.On("Search", mock.Anything, mock.MatchedBy(func(req *search.SearchRequest) bool {
		return req.Type == search.DocTypeSourceCode &&
			req.PathFilter == "/src" &&
			req.FuzzyLevel == 1 &&
			req.IncludeHighlights == true
	})).Return(&search.SearchResult{}, nil)

	_, err := executor.Execute(context.Background(), SearchCodeParams{
		Query:             "test",
		Type:              search.DocTypeSourceCode,
		PathFilter:        "/src",
		FuzzyLevel:        1,
		IncludeHighlights: true,
	})

	require.NoError(t, err)
	mockSearch.AssertExpectations(t)
}

func TestSearchCodeExecutor_Execute_NilSearchSystem(t *testing.T) {
	executor := NewSearchCodeExecutor(nil)

	_, err := executor.Execute(context.Background(), SearchCodeParams{
		Query: "test",
	})

	assert.ErrorIs(t, err, ErrNilSearchSystem)
}

func TestSearchCodeExecutor_Execute_SystemNotRunning(t *testing.T) {
	mockSearch := new(MockSearchSystem)
	executor := NewSearchCodeExecutor(mockSearch)

	mockSearch.On("IsRunning").Return(false)

	_, err := executor.Execute(context.Background(), SearchCodeParams{
		Query: "test",
	})

	assert.ErrorIs(t, err, ErrSearchNotRunning)
}

func TestSearchCodeExecutor_Execute_EmptyQuery(t *testing.T) {
	mockSearch := new(MockSearchSystem)
	executor := NewSearchCodeExecutor(mockSearch)

	mockSearch.On("IsRunning").Return(true)

	_, err := executor.Execute(context.Background(), SearchCodeParams{
		Query: "",
	})

	assert.ErrorIs(t, err, ErrEmptySearchQuery)
}

func TestSearchCodeExecutor_Execute_SearchError(t *testing.T) {
	mockSearch := new(MockSearchSystem)
	executor := NewSearchCodeExecutor(mockSearch)

	mockSearch.On("IsRunning").Return(true)
	mockSearch.On("Search", mock.Anything, mock.Anything).Return(nil, errors.New("search failed"))

	_, err := executor.Execute(context.Background(), SearchCodeParams{
		Query: "test",
	})

	assert.ErrorIs(t, err, ErrSkillExecutionFailed)
	assert.Contains(t, err.Error(), "search failed")
}

// =============================================================================
// GitDiffExecutor Tests
// =============================================================================

func TestGitDiffExecutor_Execute_Success(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitDiffExecutor(mockGit)

	expectedDiffs := []git.FileDiff{
		{Path: "/file1.go", Status: "M", Additions: 10, Deletions: 5},
		{Path: "/file2.go", Status: "A", Additions: 50, Deletions: 0},
	}

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetDiffBetween", mock.Anything, "HEAD~1", "HEAD").Return(expectedDiffs, nil)

	result, err := executor.Execute(context.Background(), GitDiffParams{
		FromCommit: "HEAD~1",
		ToCommit:   "HEAD",
	})

	require.NoError(t, err)
	assert.Equal(t, expectedDiffs, result.Files)
	assert.Equal(t, "HEAD~1", result.FromCommit)
	assert.Equal(t, "HEAD", result.ToCommit)
	assert.Equal(t, 60, result.TotalAdditions)
	assert.Equal(t, 5, result.TotalDeletions)

	mockGit.AssertExpectations(t)
}

func TestGitDiffExecutor_Execute_DefaultToCommit(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitDiffExecutor(mockGit)

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetDiffBetween", mock.Anything, "abc123", "HEAD").Return([]git.FileDiff{}, nil)

	result, err := executor.Execute(context.Background(), GitDiffParams{
		FromCommit: "abc123",
		// ToCommit not set, should default to HEAD
	})

	require.NoError(t, err)
	assert.Equal(t, "HEAD", result.ToCommit)
	mockGit.AssertExpectations(t)
}

func TestGitDiffExecutor_Execute_WithPathFilter(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitDiffExecutor(mockGit)

	allDiffs := []git.FileDiff{
		{Path: "/src/main.go", Status: "M", Additions: 10, Deletions: 5},
		{Path: "/tests/test.go", Status: "M", Additions: 20, Deletions: 10},
		{Path: "/src/util.go", Status: "A", Additions: 30, Deletions: 0},
	}

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetDiffBetween", mock.Anything, "HEAD~1", "HEAD").Return(allDiffs, nil)

	result, err := executor.Execute(context.Background(), GitDiffParams{
		FromCommit: "HEAD~1",
		ToCommit:   "HEAD",
		Path:       "/src",
	})

	require.NoError(t, err)
	assert.Len(t, result.Files, 2)
	assert.Equal(t, 40, result.TotalAdditions)
	assert.Equal(t, 5, result.TotalDeletions)
}

func TestGitDiffExecutor_Execute_NilGitManager(t *testing.T) {
	executor := NewGitDiffExecutor(nil)

	_, err := executor.Execute(context.Background(), GitDiffParams{
		FromCommit: "HEAD~1",
	})

	assert.ErrorIs(t, err, ErrNilGitManager)
}

func TestGitDiffExecutor_Execute_GitNotAvailable(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitDiffExecutor(mockGit)

	mockGit.On("IsAvailable").Return(false)

	_, err := executor.Execute(context.Background(), GitDiffParams{
		FromCommit: "HEAD~1",
	})

	assert.ErrorIs(t, err, ErrGitNotAvailable)
}

func TestGitDiffExecutor_Execute_EmptyFromCommit(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitDiffExecutor(mockGit)

	mockGit.On("IsAvailable").Return(true)

	_, err := executor.Execute(context.Background(), GitDiffParams{
		FromCommit: "",
	})

	assert.ErrorIs(t, err, ErrInvalidCommitRef)
}

func TestGitDiffExecutor_Execute_Error(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitDiffExecutor(mockGit)

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetDiffBetween", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("git error"))

	_, err := executor.Execute(context.Background(), GitDiffParams{
		FromCommit: "HEAD~1",
	})

	assert.ErrorIs(t, err, ErrSkillExecutionFailed)
}

// =============================================================================
// GitLogExecutor Tests
// =============================================================================

func TestGitLogExecutor_Execute_Success(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitLogExecutor(mockGit)

	expectedCommits := []*git.CommitInfo{
		{Hash: "abc123", Author: "Alice", Subject: "Fix bug", AuthorTime: time.Now()},
		{Hash: "def456", Author: "Bob", Subject: "Add feature", AuthorTime: time.Now().Add(-24 * time.Hour)},
	}

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetFileHistory", mock.Anything, "", 10).Return(expectedCommits, nil)

	result, err := executor.Execute(context.Background(), GitLogParams{})

	require.NoError(t, err)
	assert.Equal(t, expectedCommits, result.Commits)
	assert.Equal(t, 2, result.TotalCommits)

	mockGit.AssertExpectations(t)
}

func TestGitLogExecutor_Execute_WithPath(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitLogExecutor(mockGit)

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetFileHistory", mock.Anything, "/main.go", 10).Return([]*git.CommitInfo{}, nil)

	result, err := executor.Execute(context.Background(), GitLogParams{
		Path: "/main.go",
	})

	require.NoError(t, err)
	assert.Equal(t, "/main.go", result.Path)
	mockGit.AssertExpectations(t)
}

func TestGitLogExecutor_Execute_WithLimit(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitLogExecutor(mockGit)

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetFileHistory", mock.Anything, "", 5).Return([]*git.CommitInfo{}, nil)

	_, err := executor.Execute(context.Background(), GitLogParams{
		Limit: 5,
	})

	require.NoError(t, err)
	mockGit.AssertExpectations(t)
}

func TestGitLogExecutor_Execute_WithAuthorFilter(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitLogExecutor(mockGit)

	commits := []*git.CommitInfo{
		{Hash: "abc123", Author: "Alice Smith", AuthorEmail: "alice@example.com", AuthorTime: time.Now()},
		{Hash: "def456", Author: "Bob Jones", AuthorEmail: "bob@example.com", AuthorTime: time.Now()},
		{Hash: "ghi789", Author: "Alice Brown", AuthorEmail: "alice.brown@example.com", AuthorTime: time.Now()},
	}

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetFileHistory", mock.Anything, "", 10).Return(commits, nil)

	result, err := executor.Execute(context.Background(), GitLogParams{
		Author: "alice",
	})

	require.NoError(t, err)
	assert.Equal(t, 2, result.TotalCommits)
}

func TestGitLogExecutor_Execute_WithDateFilter(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitLogExecutor(mockGit)

	now := time.Now()
	since := now.Add(-7 * 24 * time.Hour)
	until := now.Add(-1 * 24 * time.Hour)

	commits := []*git.CommitInfo{
		{Hash: "abc123", Author: "Alice", AuthorTime: now.Add(-3 * 24 * time.Hour)},   // Within range
		{Hash: "def456", Author: "Bob", AuthorTime: now.Add(-10 * 24 * time.Hour)},    // Before since
		{Hash: "ghi789", Author: "Charlie", AuthorTime: now.Add(-12 * time.Hour)},     // After until
		{Hash: "jkl012", Author: "Dave", AuthorTime: now.Add(-5 * 24 * time.Hour)},    // Within range
	}

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetFileHistory", mock.Anything, "", 10).Return(commits, nil)

	result, err := executor.Execute(context.Background(), GitLogParams{
		Since: &since,
		Until: &until,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, result.TotalCommits)
}

func TestGitLogExecutor_Execute_NilGitManager(t *testing.T) {
	executor := NewGitLogExecutor(nil)

	_, err := executor.Execute(context.Background(), GitLogParams{})

	assert.ErrorIs(t, err, ErrNilGitManager)
}

func TestGitLogExecutor_Execute_GitNotAvailable(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitLogExecutor(mockGit)

	mockGit.On("IsAvailable").Return(false)

	_, err := executor.Execute(context.Background(), GitLogParams{})

	assert.ErrorIs(t, err, ErrGitNotAvailable)
}

func TestGitLogExecutor_Execute_Error(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitLogExecutor(mockGit)

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetFileHistory", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("git error"))

	_, err := executor.Execute(context.Background(), GitLogParams{})

	assert.ErrorIs(t, err, ErrSkillExecutionFailed)
}

// =============================================================================
// GitBlameExecutor Tests
// =============================================================================

func TestGitBlameExecutor_Execute_Success(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitBlameExecutor(mockGit)

	expectedLines := []git.BlameLine{
		{LineNumber: 1, CommitHash: "abc123", Author: "Alice", Content: "package main"},
		{LineNumber: 2, CommitHash: "abc123", Author: "Alice", Content: ""},
		{LineNumber: 3, CommitHash: "def456", Author: "Bob", Content: "func main() {}"},
	}

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetBlame", mock.Anything, "/main.go").Return(&git.BlameResult{
		Path:  "/main.go",
		Lines: expectedLines,
	}, nil)

	result, err := executor.Execute(context.Background(), GitBlameParams{
		Path: "/main.go",
	})

	require.NoError(t, err)
	assert.Equal(t, "/main.go", result.Path)
	assert.Equal(t, expectedLines, result.Lines)
	assert.Equal(t, 2, result.UniqueAuthors)
	assert.Equal(t, 2, result.UniqueCommits)

	mockGit.AssertExpectations(t)
}

func TestGitBlameExecutor_Execute_WithLineRange(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitBlameExecutor(mockGit)

	allLines := []git.BlameLine{
		{LineNumber: 1, CommitHash: "abc123", Author: "Alice"},
		{LineNumber: 2, CommitHash: "def456", Author: "Bob"},
		{LineNumber: 3, CommitHash: "ghi789", Author: "Charlie"},
		{LineNumber: 4, CommitHash: "jkl012", Author: "Dave"},
		{LineNumber: 5, CommitHash: "mno345", Author: "Eve"},
	}

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetBlame", mock.Anything, "/main.go").Return(&git.BlameResult{
		Path:  "/main.go",
		Lines: allLines,
	}, nil)

	result, err := executor.Execute(context.Background(), GitBlameParams{
		Path:      "/main.go",
		StartLine: 2,
		EndLine:   4,
	})

	require.NoError(t, err)
	assert.Len(t, result.Lines, 3)
	assert.Equal(t, 2, result.Lines[0].LineNumber)
	assert.Equal(t, 4, result.Lines[2].LineNumber)
}

func TestGitBlameExecutor_Execute_LineRangeOutOfBounds(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitBlameExecutor(mockGit)

	lines := []git.BlameLine{
		{LineNumber: 1, CommitHash: "abc123", Author: "Alice"},
		{LineNumber: 2, CommitHash: "def456", Author: "Bob"},
	}

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetBlame", mock.Anything, "/main.go").Return(&git.BlameResult{
		Path:  "/main.go",
		Lines: lines,
	}, nil)

	// Start beyond file length
	result, err := executor.Execute(context.Background(), GitBlameParams{
		Path:      "/main.go",
		StartLine: 10,
		EndLine:   20,
	})

	require.NoError(t, err)
	assert.Empty(t, result.Lines)
}

func TestGitBlameExecutor_Execute_NilGitManager(t *testing.T) {
	executor := NewGitBlameExecutor(nil)

	_, err := executor.Execute(context.Background(), GitBlameParams{
		Path: "/main.go",
	})

	assert.ErrorIs(t, err, ErrNilGitManager)
}

func TestGitBlameExecutor_Execute_GitNotAvailable(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitBlameExecutor(mockGit)

	mockGit.On("IsAvailable").Return(false)

	_, err := executor.Execute(context.Background(), GitBlameParams{
		Path: "/main.go",
	})

	assert.ErrorIs(t, err, ErrGitNotAvailable)
}

func TestGitBlameExecutor_Execute_EmptyPath(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitBlameExecutor(mockGit)

	mockGit.On("IsAvailable").Return(true)

	_, err := executor.Execute(context.Background(), GitBlameParams{
		Path: "",
	})

	assert.ErrorIs(t, err, ErrEmptyFilePath)
}

func TestGitBlameExecutor_Execute_Error(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitBlameExecutor(mockGit)

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetBlame", mock.Anything, mock.Anything).Return(nil, errors.New("git error"))

	_, err := executor.Execute(context.Background(), GitBlameParams{
		Path: "/main.go",
	})

	assert.ErrorIs(t, err, ErrSkillExecutionFailed)
}

// =============================================================================
// SkillExecutors Factory Tests
// =============================================================================

func TestNewSkillExecutors(t *testing.T) {
	mockSearch := new(MockSearchSystem)
	mockGit := new(MockGitManager)

	executors := NewSkillExecutors(mockSearch, mockGit)

	assert.NotNil(t, executors)
	assert.NotNil(t, executors.SearchCode)
	assert.NotNil(t, executors.GitDiff)
	assert.NotNil(t, executors.GitLog)
	assert.NotNil(t, executors.GitBlame)
}

func TestNewSkillExecutors_NilDependencies(t *testing.T) {
	// Should not panic with nil dependencies
	executors := NewSkillExecutors(nil, nil)

	assert.NotNil(t, executors)
	assert.NotNil(t, executors.SearchCode)
	assert.NotNil(t, executors.GitDiff)
	assert.NotNil(t, executors.GitLog)
	assert.NotNil(t, executors.GitBlame)
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestMatchesPath(t *testing.T) {
	tests := []struct {
		name       string
		filePath   string
		filterPath string
		expected   bool
	}{
		{"exact match", "/src/main.go", "/src/main.go", true},
		{"directory prefix", "/src/sub/file.go", "/src", true},
		{"no match", "/tests/test.go", "/src", false},
		{"partial match not a prefix", "/src-old/file.go", "/src", false},
		{"empty file path", "", "/src", false},
		{"empty filter path", "/src/main.go", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesPath(tt.filePath, tt.filterPath)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFilterLineRange(t *testing.T) {
	lines := []git.BlameLine{
		{LineNumber: 1}, {LineNumber: 2}, {LineNumber: 3},
		{LineNumber: 4}, {LineNumber: 5},
	}

	tests := []struct {
		name     string
		start    int
		end      int
		expected int
	}{
		{"full range", 1, 5, 5},
		{"partial range", 2, 4, 3},
		{"start only", 3, 0, 3},
		{"end only", 0, 3, 3},
		{"start beyond end", 4, 2, 0},
		{"start beyond file", 10, 0, 0},
		{"negative start", -1, 3, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterLineRange(lines, tt.start, tt.end)
			assert.Len(t, result, tt.expected)
		})
	}
}

func TestContainsIgnoreCase(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{"exact match", "Hello", "Hello", true},
		{"case insensitive", "Hello", "hello", true},
		{"partial match", "Hello World", "world", true},
		{"no match", "Hello", "Goodbye", false},
		{"empty substr", "Hello", "", true},
		{"empty s", "", "Hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsIgnoreCase(tt.s, tt.substr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// Integration-style Tests
// =============================================================================

func TestSearchCodeWorkflow(t *testing.T) {
	mockSearch := new(MockSearchSystem)
	executor := NewSearchCodeExecutor(mockSearch)

	// Simulate a full search workflow
	mockSearch.On("IsRunning").Return(true)
	mockSearch.On("Search", mock.Anything, mock.Anything).Return(&search.SearchResult{
		Documents: []search.ScoredDocument{
			{Document: search.Document{ID: "1", Path: "/main.go", Content: "func main() {}"}, Score: 0.95},
		},
		TotalHits:  1,
		SearchTime: 10 * time.Millisecond,
	}, nil)

	result, err := executor.Execute(context.Background(), SearchCodeParams{
		Query:             "main",
		Limit:             10,
		Type:              search.DocTypeSourceCode,
		IncludeHighlights: true,
	})

	require.NoError(t, err)
	assert.True(t, result.TotalHits > 0)
	assert.Len(t, result.Results, 1)
}

func TestGitWorkflow(t *testing.T) {
	mockGit := new(MockGitManager)

	// Setup mock responses
	mockGit.On("IsAvailable").Return(true)

	// Diff
	mockGit.On("GetDiffBetween", mock.Anything, "HEAD~1", "HEAD").Return([]git.FileDiff{
		{Path: "/main.go", Additions: 5, Deletions: 2},
	}, nil)

	// Log
	mockGit.On("GetFileHistory", mock.Anything, "/main.go", 5).Return([]*git.CommitInfo{
		{Hash: "abc123", Subject: "Update main", Author: "Alice", AuthorTime: time.Now()},
	}, nil)

	// Blame
	mockGit.On("GetBlame", mock.Anything, "/main.go").Return(&git.BlameResult{
		Path:  "/main.go",
		Lines: []git.BlameLine{{LineNumber: 1, Author: "Alice"}},
	}, nil)

	executors := NewSkillExecutors(nil, mockGit)

	// Test diff
	diffResult, err := executors.GitDiff.Execute(context.Background(), GitDiffParams{
		FromCommit: "HEAD~1",
		ToCommit:   "HEAD",
	})
	require.NoError(t, err)
	assert.Len(t, diffResult.Files, 1)

	// Test log
	logResult, err := executors.GitLog.Execute(context.Background(), GitLogParams{
		Path:  "/main.go",
		Limit: 5,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, logResult.TotalCommits)

	// Test blame
	blameResult, err := executors.GitBlame.Execute(context.Background(), GitBlameParams{
		Path: "/main.go",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, blameResult.UniqueAuthors)

	mockGit.AssertExpectations(t)
}

// =============================================================================
// Error Message Tests
// =============================================================================

func TestErrorMessages(t *testing.T) {
	assert.Equal(t, "search system is nil", ErrNilSearchSystem.Error())
	assert.Equal(t, "git manager is nil", ErrNilGitManager.Error())
	assert.Equal(t, "search system is not running", ErrSearchNotRunning.Error())
	assert.Equal(t, "git is not available", ErrGitNotAvailable.Error())
	assert.Equal(t, "search query cannot be empty", ErrEmptySearchQuery.Error())
	assert.Equal(t, "file path cannot be empty", ErrEmptyFilePath.Error())
	assert.Equal(t, "invalid commit reference", ErrInvalidCommitRef.Error())
	assert.Equal(t, "skill execution failed", ErrSkillExecutionFailed.Error())
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

func TestSearchCodeExecutor_ContextCancellation(t *testing.T) {
	mockSearch := new(MockSearchSystem)
	executor := NewSearchCodeExecutor(mockSearch)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockSearch.On("IsRunning").Return(true)
	mockSearch.On("Search", ctx, mock.Anything).Return(nil, context.Canceled)

	_, err := executor.Execute(ctx, SearchCodeParams{
		Query: "test",
	})

	assert.Error(t, err)
}

func TestGitDiffExecutor_ContextCancellation(t *testing.T) {
	mockGit := new(MockGitManager)
	executor := NewGitDiffExecutor(mockGit)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockGit.On("IsAvailable").Return(true)
	mockGit.On("GetDiffBetween", ctx, mock.Anything, mock.Anything).Return(nil, context.Canceled)

	_, err := executor.Execute(ctx, GitDiffParams{
		FromCommit: "HEAD~1",
	})

	assert.Error(t, err)
}
