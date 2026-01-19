// Package cmd provides CLI commands for the Sylk application.
// This file contains tests for the search command.
package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/search"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Search Command Tests
// =============================================================================

func TestSearchCmd_Definition(t *testing.T) {
	t.Run("command is defined", func(t *testing.T) {
		assert.NotNil(t, searchCmd)
		assert.Equal(t, "search <query>", searchCmd.Use)
		assert.Equal(t, "Search the codebase", searchCmd.Short)
	})

	t.Run("command has flags", func(t *testing.T) {
		flags := searchCmd.Flags()

		// Check fuzzy flag
		fuzzy := flags.Lookup("fuzzy")
		require.NotNil(t, fuzzy)
		assert.Equal(t, "f", fuzzy.Shorthand)
		assert.Equal(t, "false", fuzzy.DefValue)

		// Check type flag
		typeFlag := flags.Lookup("type")
		require.NotNil(t, typeFlag)
		assert.Equal(t, "t", typeFlag.Shorthand)

		// Check path flag
		pathFlag := flags.Lookup("path")
		require.NotNil(t, pathFlag)
		assert.Equal(t, "p", pathFlag.Shorthand)

		// Check limit flag
		limitFlag := flags.Lookup("limit")
		require.NotNil(t, limitFlag)
		assert.Equal(t, "l", limitFlag.Shorthand)
		assert.Equal(t, "20", limitFlag.DefValue)

		// Check interactive flag
		interactive := flags.Lookup("interactive")
		require.NotNil(t, interactive)
		assert.Equal(t, "i", interactive.Shorthand)

		// Check json flag
		jsonFlag := flags.Lookup("json")
		require.NotNil(t, jsonFlag)
		assert.Equal(t, "false", jsonFlag.DefValue)

		// Check index flag
		indexFlag := flags.Lookup("index")
		require.NotNil(t, indexFlag)
		assert.Equal(t, SearchDefaultIndexPath, indexFlag.DefValue)

		// Check highlight flag
		highlight := flags.Lookup("highlight")
		require.NotNil(t, highlight)
		assert.Equal(t, "true", highlight.DefValue)
	})

	t.Run("requires at least one argument", func(t *testing.T) {
		err := cobra.MinimumNArgs(1)(searchCmd, []string{})
		assert.Error(t, err)

		err = cobra.MinimumNArgs(1)(searchCmd, []string{"query"})
		assert.NoError(t, err)
	})
}

// =============================================================================
// Query Building Tests
// =============================================================================

func TestBuildSearchRequest(t *testing.T) {
	// Reset flags to defaults for each test
	defer func() {
		searchFuzzy = false
		searchType = ""
		searchPath = ""
		searchLimit = SearchDefaultLimit
		searchHighlight = true
		searchJSON = false
	}()

	t.Run("basic query", func(t *testing.T) {
		searchFuzzy = false
		searchType = ""
		searchPath = ""
		searchLimit = 20

		req := buildSearchRequest("test query")

		assert.Equal(t, "test query", req.Query)
		assert.Equal(t, 20, req.Limit)
		assert.True(t, req.IncludeHighlights)
	})

	t.Run("fuzzy query", func(t *testing.T) {
		searchFuzzy = true
		searchType = ""
		searchPath = ""
		searchLimit = 20

		req := buildSearchRequest("test query")

		assert.Contains(t, req.Query, "test~1")
		assert.Contains(t, req.Query, "query~1")
	})

	t.Run("type filter", func(t *testing.T) {
		searchFuzzy = false
		searchType = "source_code"
		searchPath = ""
		searchLimit = 20

		req := buildSearchRequest("test")

		assert.Contains(t, req.Query, "+type:source_code")
	})

	t.Run("path filter", func(t *testing.T) {
		searchFuzzy = false
		searchType = ""
		searchPath = "cmd/"
		searchLimit = 20

		req := buildSearchRequest("test")

		assert.Contains(t, req.Query, "+path:cmd/*")
	})

	t.Run("limit constraints", func(t *testing.T) {
		searchFuzzy = false
		searchType = ""
		searchPath = ""

		// Test zero limit
		searchLimit = 0
		req := buildSearchRequest("test")
		assert.Equal(t, SearchDefaultLimit, req.Limit)

		// Test negative limit
		searchLimit = -5
		req = buildSearchRequest("test")
		assert.Equal(t, SearchDefaultLimit, req.Limit)

		// Test over max limit
		searchLimit = 200
		req = buildSearchRequest("test")
		assert.Equal(t, SearchMaxLimit, req.Limit)

		// Test valid limit
		searchLimit = 50
		req = buildSearchRequest("test")
		assert.Equal(t, 50, req.Limit)
	})

	t.Run("no highlights in json mode", func(t *testing.T) {
		searchFuzzy = false
		searchType = ""
		searchPath = ""
		searchLimit = 20
		searchJSON = true
		searchHighlight = true

		req := buildSearchRequest("test")

		assert.False(t, req.IncludeHighlights)
	})
}

// =============================================================================
// Query Modifier Tests
// =============================================================================

func TestApplyFuzzyQuery(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single term",
			input:    "test",
			expected: []string{"test~1"},
		},
		{
			name:     "multiple terms",
			input:    "hello world",
			expected: []string{"hello~1", "world~1"},
		},
		{
			name:     "empty query",
			input:    "",
			expected: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyFuzzyQuery(tt.input)
			for _, exp := range tt.expected {
				assert.Contains(t, result, exp)
			}
		})
	}
}

func TestApplyTypeFilter(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		docType  string
		expected string
	}{
		{
			name:     "source_code",
			query:    "test",
			docType:  "source_code",
			expected: "test +type:source_code",
		},
		{
			name:     "markdown",
			query:    "readme",
			docType:  "markdown",
			expected: "readme +type:markdown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyTypeFilter(tt.query, tt.docType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestApplyPathFilter(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		pathPrefix string
		expected   string
	}{
		{
			name:       "simple path",
			query:      "test",
			pathPrefix: "cmd/",
			expected:   "test +path:cmd/*",
		},
		{
			name:       "nested path",
			query:      "search",
			pathPrefix: "core/search/",
			expected:   "search +path:core/search/*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyPathFilter(tt.query, tt.pathPrefix)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// Output Formatting Tests
// =============================================================================

func TestExtractSnippet(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxLen   int
		expected string
	}{
		{
			name:     "short content",
			content:  "short",
			maxLen:   50,
			expected: "short",
		},
		{
			name:     "exact length",
			content:  "exactly ten",
			maxLen:   11,
			expected: "exactly ten",
		},
		{
			name:     "long content truncated",
			content:  "this is a very long content that should be truncated",
			maxLen:   20,
			expected: "this is a very...",
		},
		{
			name:     "empty content",
			content:  "",
			maxLen:   50,
			expected: "",
		},
		{
			name:     "whitespace normalized",
			content:  "hello   world\n\ttest",
			maxLen:   50,
			expected: "hello world test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSnippet(tt.content, tt.maxLen)
			if tt.content != "" && len(tt.content) > tt.maxLen {
				assert.True(t, strings.HasSuffix(result, "..."))
				assert.LessOrEqual(t, len(result), tt.maxLen+3) // +3 for "..."
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGetLanguageDisplay(t *testing.T) {
	tests := []struct {
		lang     string
		expected string
	}{
		{"go", "go"},
		{"python", "python"},
		{"", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			result := getLanguageDisplay(tt.lang)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// Search Output Tests
// =============================================================================

func TestNewSearchOutput(t *testing.T) {
	t.Run("creates output from result", func(t *testing.T) {
		result := &search.SearchResult{
			Query:     "test query",
			TotalHits: 2,
			Documents: []search.ScoredDocument{
				{
					Document: search.Document{
						Path:     "/path/to/file1.go",
						Type:     search.DocTypeSourceCode,
						Language: "go",
						Content:  "package main",
					},
					Score: 0.95,
					Highlights: map[string][]string{
						"content": {"matched text"},
					},
					MatchedFields: []string{"content"},
				},
				{
					Document: search.Document{
						Path:     "/path/to/file2.md",
						Type:     search.DocTypeMarkdown,
						Language: "markdown",
						Content:  "# Title",
					},
					Score: 0.75,
				},
			},
		}

		output := newSearchOutput(result)

		assert.Equal(t, "test query", output.Query)
		assert.Equal(t, int64(2), output.TotalHits)
		assert.Len(t, output.Documents, 2)

		// Check first document
		assert.Equal(t, "/path/to/file1.go", output.Documents[0].Path)
		assert.Equal(t, "source_code", output.Documents[0].Type)
		assert.Equal(t, "go", output.Documents[0].Language)
		assert.Equal(t, 0.95, output.Documents[0].Score)
		assert.NotNil(t, output.Documents[0].Highlights)
		assert.NotNil(t, output.Documents[0].MatchedFields)

		// Check second document
		assert.Equal(t, "/path/to/file2.md", output.Documents[1].Path)
		assert.Equal(t, "markdown", output.Documents[1].Type)
	})
}

// =============================================================================
// JSON Output Tests
// =============================================================================

func TestOutputJSONResults(t *testing.T) {
	result := &search.SearchResult{
		Query:     "test",
		TotalHits: 1,
		Documents: []search.ScoredDocument{
			{
				Document: search.Document{
					Path:     "/test.go",
					Type:     search.DocTypeSourceCode,
					Language: "go",
				},
				Score: 0.9,
			},
		},
	}

	var buf bytes.Buffer
	err := outputJSONResults(&buf, result)

	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, `"query": "test"`)
	assert.Contains(t, output, `"total_hits": 1`)
	assert.Contains(t, output, `"path": "/test.go"`)
	assert.Contains(t, output, `"type": "source_code"`)
	assert.Contains(t, output, `"language": "go"`)
	assert.Contains(t, output, `"score": 0.9`)
}

// =============================================================================
// Rich Output Tests
// =============================================================================

func TestOutputRichResults(t *testing.T) {
	t.Run("no results", func(t *testing.T) {
		result := &search.SearchResult{
			Query:     "nothing",
			TotalHits: 0,
			Documents: []search.ScoredDocument{},
		}

		var buf bytes.Buffer
		err := outputRichResults(&buf, result)

		require.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "No results found")
	})

	t.Run("with results", func(t *testing.T) {
		result := &search.SearchResult{
			Query:     "test",
			TotalHits: 1,
			Documents: []search.ScoredDocument{
				{
					Document: search.Document{
						Path:     "/test.go",
						Type:     search.DocTypeSourceCode,
						Language: "go",
						Content:  "package main",
					},
					Score: 0.9,
				},
			},
		}

		var buf bytes.Buffer
		err := outputRichResults(&buf, result)

		require.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "Search Results")
		assert.Contains(t, output, "/test.go")
	})
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestSearchCmd_Execution(t *testing.T) {
	t.Run("requires query argument", func(t *testing.T) {
		cmd := &cobra.Command{
			Use:  "search <query>",
			Args: cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return nil
			},
		}

		cmd.SetArgs([]string{})
		err := cmd.Execute()

		assert.Error(t, err)
	})
}

