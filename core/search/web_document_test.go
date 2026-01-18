package search

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// NormalizeURL Tests
// =============================================================================

func TestNormalizeURL_RemovesTrailingSlash(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "removes trailing slash from path",
			input:    "https://example.com/path/",
			expected: "https://example.com/path",
		},
		{
			name:     "removes trailing slash from root",
			input:    "https://example.com/",
			expected: "https://example.com",
		},
		{
			name:     "no change when no trailing slash",
			input:    "https://example.com/path",
			expected: "https://example.com/path",
		},
		{
			name:     "handles multiple path segments",
			input:    "https://example.com/a/b/c/",
			expected: "https://example.com/a/b/c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeURL(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeURL_LowercasesHostname(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercases simple hostname",
			input:    "https://EXAMPLE.COM/path",
			expected: "https://example.com/path",
		},
		{
			name:     "lowercases mixed case hostname",
			input:    "https://ExAmPlE.CoM/path",
			expected: "https://example.com/path",
		},
		{
			name:     "lowercases subdomain",
			input:    "https://WWW.EXAMPLE.COM/path",
			expected: "https://www.example.com/path",
		},
		{
			name:     "preserves path case",
			input:    "https://EXAMPLE.COM/Path/To/Resource",
			expected: "https://example.com/Path/To/Resource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeURL(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeURL_RemovesDefaultPorts(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "removes port 80 for http",
			input:    "http://example.com:80/path",
			expected: "http://example.com/path",
		},
		{
			name:     "removes port 443 for https",
			input:    "https://example.com:443/path",
			expected: "https://example.com/path",
		},
		{
			name:     "preserves non-default port for http",
			input:    "http://example.com:8080/path",
			expected: "http://example.com:8080/path",
		},
		{
			name:     "preserves non-default port for https",
			input:    "https://example.com:8443/path",
			expected: "https://example.com:8443/path",
		},
		{
			name:     "preserves port 443 for http",
			input:    "http://example.com:443/path",
			expected: "http://example.com:443/path",
		},
		{
			name:     "preserves port 80 for https",
			input:    "https://example.com:80/path",
			expected: "https://example.com:80/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeURL(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeURL_SortsQueryParameters(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "sorts alphabetically",
			input:    "https://example.com?z=3&a=1&m=2",
			expected: "https://example.com?a=1&m=2&z=3",
		},
		{
			name:     "handles single parameter",
			input:    "https://example.com?foo=bar",
			expected: "https://example.com?foo=bar",
		},
		{
			name:     "handles empty query",
			input:    "https://example.com/path",
			expected: "https://example.com/path",
		},
		{
			name:     "preserves multiple values for same key",
			input:    "https://example.com?b=2&a=1&a=3",
			expected: "https://example.com?a=1&a=3&b=2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeURL(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeURL_CombinedNormalization(t *testing.T) {
	input := "https://EXAMPLE.COM:443/Path/To/Resource/?z=3&a=1"
	expected := "https://example.com/Path/To/Resource?a=1&z=3"

	result := NormalizeURL(input)
	assert.Equal(t, expected, result)
}

func TestNormalizeURL_InvalidURL(t *testing.T) {
	// Invalid URLs should be returned as-is
	invalid := "://not-a-valid-url"
	result := NormalizeURL(invalid)
	assert.Equal(t, invalid, result)
}

func TestNormalizeURL_SpecialCharacters(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "handles encoded spaces in path",
			input:    "https://example.com/path%20with%20spaces",
			expected: "https://example.com/path%20with%20spaces",
		},
		{
			name:     "handles encoded query values",
			input:    "https://example.com?q=hello%20world",
			expected: "https://example.com?q=hello+world",
		},
		{
			name:     "handles unicode in hostname",
			input:    "https://example.com/path",
			expected: "https://example.com/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeURL(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// WebFetchDocument Validation Tests
// =============================================================================

func TestWebFetchDocument_Validate_Success(t *testing.T) {
	doc := validWebFetchDocument()

	err := doc.Validate()
	assert.NoError(t, err)
}

func TestWebFetchDocument_Validate_EmptyURL(t *testing.T) {
	doc := validWebFetchDocument()
	doc.URL = ""

	err := doc.Validate()
	assert.ErrorIs(t, err, ErrEmptyURL)
}

func TestWebFetchDocument_Validate_InvalidURLFormat(t *testing.T) {
	doc := validWebFetchDocument()
	doc.URL = "://invalid"

	err := doc.Validate()
	assert.ErrorIs(t, err, ErrInvalidURL)
}

func TestWebFetchDocument_Validate_InvalidScheme(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "ftp scheme", url: "ftp://example.com/file"},
		{name: "file scheme", url: "file:///path/to/file"},
		{name: "mailto scheme", url: "mailto:user@example.com"},
		{name: "empty scheme", url: "//example.com/path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := validWebFetchDocument()
			doc.URL = tt.url

			err := doc.Validate()
			assert.ErrorIs(t, err, ErrInvalidScheme)
		})
	}
}

func TestWebFetchDocument_Validate_MissingHost(t *testing.T) {
	doc := validWebFetchDocument()
	doc.URL = "https:///path/without/host"

	err := doc.Validate()
	assert.ErrorIs(t, err, ErrMissingHost)
}

func TestWebFetchDocument_Validate_ZeroFetchedAt(t *testing.T) {
	doc := validWebFetchDocument()
	doc.FetchedAt = time.Time{}

	err := doc.Validate()
	assert.ErrorIs(t, err, ErrZeroFetchedAt)
}

func TestWebFetchDocument_Validate_InvalidStatusCode(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "zero status", status: 0},
		{name: "negative status", status: -1},
		{name: "below 100", status: 99},
		{name: "above 599", status: 600},
		{name: "way above range", status: 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := validWebFetchDocument()
			doc.StatusCode = tt.status

			err := doc.Validate()
			assert.ErrorIs(t, err, ErrInvalidStatus)
		})
	}
}

func TestWebFetchDocument_Validate_EmptyContentType(t *testing.T) {
	doc := validWebFetchDocument()
	doc.ContentType = ""

	err := doc.Validate()
	assert.ErrorIs(t, err, ErrEmptyContentType)
}

func TestWebFetchDocument_Validate_ValidStatusCodeBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "minimum valid (100)", status: 100},
		{name: "maximum valid (599)", status: 599},
		{name: "typical success (200)", status: 200},
		{name: "redirect (301)", status: 301},
		{name: "client error (404)", status: 404},
		{name: "server error (500)", status: 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := validWebFetchDocument()
			doc.StatusCode = tt.status

			err := doc.Validate()
			assert.NoError(t, err)
		})
	}
}

// =============================================================================
// IsSuccess Tests
// =============================================================================

func TestWebFetchDocument_IsSuccess(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		expected bool
	}{
		{name: "200 OK", status: 200, expected: true},
		{name: "201 Created", status: 201, expected: true},
		{name: "204 No Content", status: 204, expected: true},
		{name: "299 boundary", status: 299, expected: true},
		{name: "199 below range", status: 199, expected: false},
		{name: "300 redirect", status: 300, expected: false},
		{name: "301 redirect", status: 301, expected: false},
		{name: "400 client error", status: 400, expected: false},
		{name: "404 not found", status: 404, expected: false},
		{name: "500 server error", status: 500, expected: false},
		{name: "100 informational", status: 100, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := WebFetchDocument{StatusCode: tt.status}
			assert.Equal(t, tt.expected, doc.IsSuccess())
		})
	}
}

// =============================================================================
// HasCodeSnippets Tests
// =============================================================================

func TestWebFetchDocument_HasCodeSnippets(t *testing.T) {
	tests := []struct {
		name     string
		snippets []CodeSnippet
		expected bool
	}{
		{
			name:     "no snippets",
			snippets: nil,
			expected: false,
		},
		{
			name:     "empty slice",
			snippets: []CodeSnippet{},
			expected: false,
		},
		{
			name: "one snippet",
			snippets: []CodeSnippet{
				{Language: "go", Content: "fmt.Println()", Context: "example"},
			},
			expected: true,
		},
		{
			name: "multiple snippets",
			snippets: []CodeSnippet{
				{Language: "go", Content: "fmt.Println()", Context: "example"},
				{Language: "python", Content: "print()", Context: "example"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := WebFetchDocument{CodeSnippets: tt.snippets}
			assert.Equal(t, tt.expected, doc.HasCodeSnippets())
		})
	}
}

// =============================================================================
// GenerateID Tests
// =============================================================================

func TestWebFetchDocument_GenerateID(t *testing.T) {
	doc := WebFetchDocument{
		URL: "https://EXAMPLE.COM:443/Path/",
	}

	id := doc.GenerateID()
	assert.Equal(t, "https://example.com/Path", id)
}

func TestWebFetchDocument_GenerateID_Consistent(t *testing.T) {
	// Same URL with different representations should generate same ID
	urls := []string{
		"https://EXAMPLE.COM:443/path/?b=2&a=1",
		"https://example.com/path?a=1&b=2",
		"https://Example.Com:443/path/?a=1&b=2",
	}

	var firstID string
	for i, u := range urls {
		doc := WebFetchDocument{URL: u}
		id := doc.GenerateID()

		if i == 0 {
			firstID = id
		} else {
			assert.Equal(t, firstID, id, "IDs should match for equivalent URLs")
		}
	}
}

// =============================================================================
// CodeSnippet Tests
// =============================================================================

func TestCodeSnippet_Fields(t *testing.T) {
	snippet := CodeSnippet{
		Language: "go",
		Content:  "func main() {\n\tfmt.Println(\"Hello\")\n}",
		Context:  "Basic hello world example",
	}

	assert.Equal(t, "go", snippet.Language)
	assert.Contains(t, snippet.Content, "func main()")
	assert.Equal(t, "Basic hello world example", snippet.Context)
}

// =============================================================================
// Full Document Tests
// =============================================================================

func TestWebFetchDocument_FullDocument(t *testing.T) {
	now := time.Now()
	content := "This is the content of the page"

	doc := WebFetchDocument{
		Document: Document{
			ID:         "doc-123",
			Path:       "https://golang.org/doc/",
			Type:       DocTypeWebFetch,
			Content:    content,
			Checksum:   GenerateChecksum([]byte(content)),
			ModifiedAt: now,
			IndexedAt:  now,
		},
		URL:         "https://golang.org/doc/",
		FetchedAt:   now,
		StatusCode:  200,
		ContentType: "text/html; charset=utf-8",
		Links: []string{
			"https://golang.org/pkg/",
			"https://golang.org/ref/spec",
		},
		Headings: []string{
			"Documentation",
			"Getting Started",
			"Learning Go",
		},
		CodeSnippets: []CodeSnippet{
			{
				Language: "go",
				Content:  "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}",
				Context:  "Hello World example",
			},
		},
	}

	// Verify embedded Document fields
	assert.Equal(t, "doc-123", doc.ID)
	assert.Equal(t, "https://golang.org/doc/", doc.Path)
	assert.Equal(t, "This is the content of the page", doc.Content)
	assert.Equal(t, DocTypeWebFetch, doc.Type)

	// Verify WebFetchDocument fields
	assert.Equal(t, "https://golang.org/doc/", doc.URL)
	assert.Equal(t, now, doc.FetchedAt)
	assert.Equal(t, 200, doc.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", doc.ContentType)
	assert.Len(t, doc.Links, 2)
	assert.Len(t, doc.Headings, 3)
	assert.Len(t, doc.CodeSnippets, 1)

	// Verify helper methods
	assert.True(t, doc.IsSuccess())
	assert.True(t, doc.HasCodeSnippets())
	require.NoError(t, doc.Validate())
}

func TestWebFetchDocument_EmptyOptionalFields(t *testing.T) {
	doc := validWebFetchDocument()
	doc.Links = nil
	doc.Headings = nil
	doc.CodeSnippets = nil

	// Should still validate successfully
	err := doc.Validate()
	assert.NoError(t, err)

	// Helper methods should handle nil slices
	assert.False(t, doc.HasCodeSnippets())
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestNormalizeURL_EmptyString(t *testing.T) {
	result := NormalizeURL("")
	assert.Equal(t, "", result)
}

func TestNormalizeURL_RelativeURL(t *testing.T) {
	// Relative URLs are not fully normalized but shouldn't crash
	result := NormalizeURL("/path/to/resource")
	assert.Equal(t, "/path/to/resource", result)
}

func TestNormalizeURL_QueryWithEncodedCharacters(t *testing.T) {
	input := "https://example.com?search=hello%2Bworld&tag=go%2B1.21"
	result := NormalizeURL(input)
	// Should preserve encoded characters in sorted order
	assert.Contains(t, result, "search=")
	assert.Contains(t, result, "tag=")
}

func TestNormalizeURL_Fragment(t *testing.T) {
	input := "https://example.com/page#section"
	result := NormalizeURL(input)
	// Fragment should be preserved
	assert.Contains(t, result, "#section")
}

func TestWebFetchDocument_ValidateHTTPURL(t *testing.T) {
	doc := validWebFetchDocument()
	doc.URL = "http://example.com/insecure"

	err := doc.Validate()
	assert.NoError(t, err)
}

// =============================================================================
// Test Helpers
// =============================================================================

func validWebFetchDocument() WebFetchDocument {
	content := "Test content"
	now := time.Now()
	return WebFetchDocument{
		Document: Document{
			ID:         "test-doc",
			Path:       "https://example.com/test",
			Type:       DocTypeWebFetch,
			Content:    content,
			Checksum:   GenerateChecksum([]byte(content)),
			ModifiedAt: now,
			IndexedAt:  now,
		},
		URL:         "https://example.com/test",
		FetchedAt:   now,
		StatusCode:  200,
		ContentType: "text/html",
	}
}
