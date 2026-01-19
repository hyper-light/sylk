package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock Searcher Tests
// =============================================================================

func TestMockSearcher_Search(t *testing.T) {
	docs := []search.ScoredDocument{
		{
			Document: search.Document{
				ID:      "1",
				Path:    "test/file1.go",
				Content: "package test",
			},
			Score: 0.9,
		},
		{
			Document: search.Document{
				ID:      "2",
				Path:    "other/file2.go",
				Content: "package other",
			},
			Score: 0.8,
		},
	}

	searcher := &MockSearcher{Results: docs}

	t.Run("filters by query", func(t *testing.T) {
		req := search.SearchRequest{Query: "test"}
		result, err := searcher.Search(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, int64(1), result.TotalHits)
		assert.Equal(t, "test/file1.go", result.Documents[0].Path)
	})

	t.Run("returns all on broad query", func(t *testing.T) {
		req := search.SearchRequest{Query: "file"}
		result, err := searcher.Search(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, int64(2), result.TotalHits)
	})

	t.Run("returns empty on no match", func(t *testing.T) {
		req := search.SearchRequest{Query: "nonexistent"}
		result, err := searcher.Search(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, int64(0), result.TotalHits)
	})

	t.Run("case insensitive", func(t *testing.T) {
		req := search.SearchRequest{Query: "TEST"}
		result, err := searcher.Search(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, int64(1), result.TotalHits)
	})
}

func TestMockSearcher_Error(t *testing.T) {
	searcher := &MockSearcher{
		Err: errors.New("search error"),
	}

	req := search.SearchRequest{Query: "test"}
	result, err := searcher.Search(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// =============================================================================
// InteractiveSearcher Constructor Tests
// =============================================================================

func TestNewInteractiveSearcher(t *testing.T) {
	searcher := &MockSearcher{}
	stdin := &bytes.Buffer{}
	stdout := &bytes.Buffer{}

	is := NewInteractiveSearcher(searcher, stdin, stdout)

	assert.NotNil(t, is)
	assert.Equal(t, searcher, is.searcher)
	assert.Equal(t, stdin, is.stdin)
	assert.Equal(t, stdout, is.stdout)
	assert.Empty(t, is.results)
	assert.Equal(t, 0, is.selected)
	assert.Equal(t, "", is.query)
	assert.False(t, is.running)
	assert.True(t, is.showPreview)
}

// =============================================================================
// Navigation Tests
// =============================================================================

func TestInteractiveSearcher_MoveUp(t *testing.T) {
	is := createTestSearcher()
	is.results = createTestResults(5)

	// Start at 0, can't go up
	is.selected = 0
	is.moveUp()
	assert.Equal(t, 0, is.selected)

	// Start at 3, can go up
	is.selected = 3
	is.moveUp()
	assert.Equal(t, 2, is.selected)

	// Start at 1, can go up to 0
	is.selected = 1
	is.moveUp()
	assert.Equal(t, 0, is.selected)
}

func TestInteractiveSearcher_MoveDown(t *testing.T) {
	is := createTestSearcher()
	is.results = createTestResults(5)

	// Start at 0, can go down
	is.selected = 0
	is.moveDown()
	assert.Equal(t, 1, is.selected)

	// Start at 4, can't go down (at end of results)
	is.selected = 4
	is.moveDown()
	assert.Equal(t, 4, is.selected)

	// With fewer results than maxResults
	is.results = createTestResults(3)
	is.selected = 2
	is.moveDown()
	assert.Equal(t, 2, is.selected) // Can't go beyond results
}

func TestInteractiveSearcher_MoveDown_MaxResults(t *testing.T) {
	is := createTestSearcher()
	is.results = createTestResults(15) // More than maxResults

	// Can't go beyond maxResults - 1
	is.selected = maxResults - 2
	is.moveDown()
	assert.Equal(t, maxResults-1, is.selected)

	is.moveDown() // Try to go beyond
	assert.Equal(t, maxResults-1, is.selected)
}

// =============================================================================
// Search Tests
// =============================================================================

func TestInteractiveSearcher_PerformSearch(t *testing.T) {
	docs := []search.ScoredDocument{
		{Document: search.Document{Path: "file1.go"}, Score: 0.9},
		{Document: search.Document{Path: "file2.go"}, Score: 0.8},
	}
	is := createTestSearcher()
	is.searcher = &MockSearcher{Results: docs}

	t.Run("empty query clears results", func(t *testing.T) {
		is.query = ""
		is.results = docs
		is.selected = 1
		is.performSearch()
		assert.Nil(t, is.results)
		assert.Equal(t, 0, is.selected)
	})

	t.Run("populates results on query", func(t *testing.T) {
		is.query = "file"
		is.lastSearch = time.Time{} // Reset debounce
		is.performSearch()
		assert.NotEmpty(t, is.results)
		assert.Equal(t, 0, is.selected)
	})
}

func TestInteractiveSearcher_PerformSearch_Debounce(t *testing.T) {
	is := createTestSearcher()
	is.query = "test"
	is.lastSearch = time.Now() // Recent search

	// Should be debounced
	is.performSearch()
	// Results won't change due to debounce
}

// =============================================================================
// Key Handling Tests
// =============================================================================

func TestInteractiveSearcher_HandleBackspace(t *testing.T) {
	is := createTestSearcher()

	// With content
	is.query = "test"
	is.handleBackspace()
	assert.Equal(t, "tes", is.query)

	// Down to empty
	is.query = "t"
	is.handleBackspace()
	assert.Equal(t, "", is.query)

	// Already empty
	is.query = ""
	is.handleBackspace()
	assert.Equal(t, "", is.query)
}

func TestInteractiveSearcher_HandleCharacter(t *testing.T) {
	is := createTestSearcher()
	is.query = ""

	is.handleCharacter('a')
	assert.Equal(t, "a", is.query)

	is.handleCharacter('b')
	assert.Equal(t, "ab", is.query)

	is.handleCharacter('c')
	assert.Equal(t, "abc", is.query)
}

// =============================================================================
// Rendering Tests
// =============================================================================

func TestInteractiveSearcher_Render(t *testing.T) {
	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)
	is.termWidth = 80
	is.termHeight = 24
	is.query = "test"
	is.results = createTestResults(3)

	is.render()

	output := stdout.String()

	// Check header is rendered
	assert.Contains(t, output, "Sylk Interactive Search")

	// Check prompt is rendered
	assert.Contains(t, output, ">")
	assert.Contains(t, output, "test")

	// Check footer is rendered
	assert.Contains(t, output, "Navigate")
	assert.Contains(t, output, "Quit")
}

func TestInteractiveSearcher_RenderResults_Empty(t *testing.T) {
	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)
	is.termWidth = 80
	is.query = ""
	is.results = nil

	is.renderResults()
	output := stdout.String()
	assert.Contains(t, output, "Type to search")
}

func TestInteractiveSearcher_RenderResults_NoMatch(t *testing.T) {
	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)
	is.termWidth = 80
	is.query = "nonexistent"
	is.results = []search.ScoredDocument{}

	is.renderResults()
	output := stdout.String()
	assert.Contains(t, output, "No results found")
}

func TestInteractiveSearcher_RenderResultItem(t *testing.T) {
	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)
	is.termWidth = 80
	is.results = createTestResults(3)
	is.selected = 1

	// Render selected item
	is.renderResultItem(1)
	output := stdout.String()
	assert.Contains(t, output, ">") // Selection indicator

	// Render non-selected item
	stdout.Reset()
	is.renderResultItem(0)
	output = stdout.String()
	assert.NotContains(t, output, ">")
}

func TestInteractiveSearcher_RenderPreview(t *testing.T) {
	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)
	is.termWidth = 80
	is.results = []search.ScoredDocument{
		{
			Document: search.Document{
				Path:    "test.go",
				Content: "line 1\nline 2\nline 3",
			},
			Score: 0.9,
		},
	}
	is.selected = 0
	is.showPreview = true

	is.renderPreview()
	output := stdout.String()
	assert.Contains(t, output, "Preview")
	assert.Contains(t, output, "test.go")
	assert.Contains(t, output, "1:")
	assert.Contains(t, output, "line 1")
}

func TestInteractiveSearcher_RenderPreview_NoResults(t *testing.T) {
	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)
	is.results = []search.ScoredDocument{}
	is.selected = 0

	is.renderPreview()
	output := stdout.String()
	assert.Empty(t, output)
}

func TestInteractiveSearcher_RenderPreview_OutOfBounds(t *testing.T) {
	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)
	is.results = createTestResults(2)
	is.selected = 5 // Out of bounds

	is.renderPreview()
	output := stdout.String()
	assert.Empty(t, output)
}

// =============================================================================
// Terminal Control Tests
// =============================================================================

func TestInteractiveSearcher_TerminalControls(t *testing.T) {
	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)

	t.Run("clearScreen", func(t *testing.T) {
		stdout.Reset()
		is.clearScreen()
		assert.Contains(t, stdout.String(), escClearScreen)
	})

	t.Run("clearLine", func(t *testing.T) {
		stdout.Reset()
		is.clearLine()
		assert.Contains(t, stdout.String(), escClearLine)
	})

	t.Run("moveCursor", func(t *testing.T) {
		stdout.Reset()
		is.moveCursor(5, 10)
		assert.Contains(t, stdout.String(), "\033[5;10H")
	})

	t.Run("hideCursor", func(t *testing.T) {
		stdout.Reset()
		is.hideCursor()
		assert.Contains(t, stdout.String(), escHideCursor)
	})

	t.Run("showCursor", func(t *testing.T) {
		stdout.Reset()
		is.showCursor()
		assert.Contains(t, stdout.String(), escShowCursor)
	})
}

// =============================================================================
// Select Result Tests
// =============================================================================

func TestInteractiveSearcher_SelectResult(t *testing.T) {
	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)
	is.results = []search.ScoredDocument{
		{Document: search.Document{Path: "/path/to/file.go"}},
	}
	is.selected = 0
	is.running = true

	is.selectResult()

	assert.False(t, is.running)
	assert.Contains(t, stdout.String(), "/path/to/file.go")
}

func TestInteractiveSearcher_SelectResult_NoResults(t *testing.T) {
	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)
	is.results = []search.ScoredDocument{}
	is.selected = 0
	is.running = true

	is.selectResult()

	assert.False(t, is.running)
	// Should not output anything for empty results
	output := stdout.String()
	assert.NotContains(t, output, "/path/to/file.go")
}

// =============================================================================
// Command Tests
// =============================================================================

func TestInteractiveSearchCommand(t *testing.T) {
	assert.NotNil(t, interactiveSearchCmd)
	assert.Equal(t, "isearch", interactiveSearchCmd.Use)
	assert.NotNil(t, interactiveSearchCmd.RunE)
}

func TestInteractiveSearchPreviewFlag(t *testing.T) {
	previewFlag := interactiveSearchCmd.Flags().Lookup("preview")
	assert.NotNil(t, previewFlag)
	assert.Equal(t, "p", previewFlag.Shorthand)
	assert.Equal(t, "true", previewFlag.DefValue)
}

// =============================================================================
// Utility Tests
// =============================================================================

func TestMin(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, 10, 0},
		{-1, 1, -1},
	}

	for _, tt := range tests {
		result := min(tt.a, tt.b)
		assert.Equal(t, tt.expected, result)
	}
}

// =============================================================================
// Mock Results Creation Tests
// =============================================================================

func TestCreateMockResults(t *testing.T) {
	results := createMockResults()

	assert.NotEmpty(t, results)
	assert.GreaterOrEqual(t, len(results), 1)

	// Verify structure of results
	for _, doc := range results {
		assert.NotEmpty(t, doc.Path)
		assert.NotEmpty(t, doc.Content)
		assert.Greater(t, doc.Score, 0.0)
	}
}

// =============================================================================
// Constants Tests
// =============================================================================

func TestConstants(t *testing.T) {
	// Verify ANSI codes are defined
	assert.NotEmpty(t, escClearLine)
	assert.NotEmpty(t, escClearScreen)
	assert.NotEmpty(t, escHideCursor)
	assert.NotEmpty(t, escShowCursor)

	// Verify key codes
	assert.Equal(t, byte(13), byte(keyEnter))
	assert.Equal(t, byte(27), byte(keyEscape))
	assert.Equal(t, byte(127), byte(keyBackspace))

	// Verify display settings
	assert.Greater(t, maxResults, 0)
	assert.Greater(t, maxPreviewLines, 0)
	assert.Greater(t, maxPreviewWidth, 0)
}

// =============================================================================
// Integration-like Tests
// =============================================================================

func TestInteractiveSearcher_QueryAndNavigation(t *testing.T) {
	docs := []search.ScoredDocument{
		{Document: search.Document{Path: "file1.go", Content: "test content 1"}, Score: 0.9},
		{Document: search.Document{Path: "file2.go", Content: "test content 2"}, Score: 0.8},
		{Document: search.Document{Path: "file3.go", Content: "test content 3"}, Score: 0.7},
	}

	stdout := &bytes.Buffer{}
	is := createTestSearcherWithOutput(stdout)
	is.searcher = &MockSearcher{Results: docs}
	is.termWidth = 80
	is.termHeight = 24

	// Type query
	is.handleCharacter('t')
	is.handleCharacter('e')
	is.handleCharacter('s')
	is.handleCharacter('t')

	assert.Equal(t, "test", is.query)

	// Search should populate results
	is.lastSearch = time.Time{} // Reset debounce
	is.performSearch()
	assert.Len(t, is.results, 3)

	// Navigate down
	is.moveDown()
	assert.Equal(t, 1, is.selected)

	is.moveDown()
	assert.Equal(t, 2, is.selected)

	// Navigate up
	is.moveUp()
	assert.Equal(t, 1, is.selected)

	// Delete character
	is.handleBackspace()
	assert.Equal(t, "tes", is.query)
}

// =============================================================================
// Helper Functions
// =============================================================================

func createTestSearcher() *InteractiveSearcher {
	return &InteractiveSearcher{
		searcher:    &MockSearcher{},
		results:     make([]search.ScoredDocument, 0),
		selected:    0,
		query:       "",
		running:     false,
		stdin:       &bytes.Buffer{},
		stdout:      &bytes.Buffer{},
		showPreview: true,
	}
}

func createTestSearcherWithOutput(stdout *bytes.Buffer) *InteractiveSearcher {
	return &InteractiveSearcher{
		searcher:    &MockSearcher{},
		results:     make([]search.ScoredDocument, 0),
		selected:    0,
		query:       "",
		running:     false,
		stdin:       &bytes.Buffer{},
		stdout:      stdout,
		showPreview: true,
		termWidth:   80,
		termHeight:  24,
	}
}

func createTestResults(count int) []search.ScoredDocument {
	results := make([]search.ScoredDocument, count)
	for i := 0; i < count; i++ {
		results[i] = search.ScoredDocument{
			Document: search.Document{
				ID:      string(rune('1' + i)),
				Path:    strings.Repeat("file", i+1) + ".go",
				Type:    search.DocTypeSourceCode,
				Content: "test content",
			},
			Score: 1.0 - float64(i)*0.1,
		}
	}
	return results
}
