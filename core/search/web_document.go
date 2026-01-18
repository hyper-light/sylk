package search

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// =============================================================================
// CodeSnippet
// =============================================================================

// CodeSnippet represents a code block extracted from a web document
type CodeSnippet struct {
	// Language is the programming language of the snippet (e.g., "go", "python")
	Language string `json:"language"`

	// Content is the actual code content
	Content string `json:"content"`

	// Context describes where this snippet was found or what it demonstrates
	Context string `json:"context"`
}

// =============================================================================
// WebFetchDocument
// =============================================================================

// WebFetchDocument represents a document fetched from the web
// It embeds Document and adds web-specific metadata
type WebFetchDocument struct {
	Document

	// URL is the source URL of the document
	URL string `json:"url"`

	// FetchedAt is the timestamp when the document was fetched
	FetchedAt time.Time `json:"fetched_at"`

	// StatusCode is the HTTP status code from the fetch response
	StatusCode int `json:"status_code"`

	// ContentType is the MIME type of the document content
	ContentType string `json:"content_type"`

	// Links contains URLs extracted from the document
	Links []string `json:"links,omitempty"`

	// Headings contains heading text extracted from the document
	Headings []string `json:"headings,omitempty"`

	// CodeSnippets contains code blocks extracted from the document
	CodeSnippets []CodeSnippet `json:"code_snippets,omitempty"`
}

// =============================================================================
// URL Normalization
// =============================================================================

// NormalizeURL normalizes a URL string for consistent ID generation.
// It performs the following transformations:
//   - Lowercases the hostname
//   - Removes default ports (80 for http, 443 for https)
//   - Removes trailing slashes from the path
//   - Sorts query parameters alphabetically
func NormalizeURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	normalizeHost(parsed)
	normalizePath(parsed)
	normalizeQuery(parsed)

	return parsed.String()
}

// normalizeHost lowercases the hostname and removes default ports
func normalizeHost(u *url.URL) {
	host := strings.ToLower(u.Hostname())
	port := u.Port()

	if shouldRemovePort(u.Scheme, port) {
		u.Host = host
		return
	}

	if port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}
}

// shouldRemovePort returns true if the port is the default for the scheme
func shouldRemovePort(scheme, port string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}

// normalizePath removes trailing slashes from the path
func normalizePath(u *url.URL) {
	if u.Path == "" || u.Path == "/" {
		u.Path = ""
		return
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
}

// normalizeQuery sorts query parameters alphabetically
func normalizeQuery(u *url.URL) {
	if u.RawQuery == "" {
		return
	}

	values := u.Query()
	sortedKeys := sortQueryKeys(values)
	u.RawQuery = buildSortedQuery(values, sortedKeys)
}

// sortQueryKeys returns sorted query parameter keys
func sortQueryKeys(values url.Values) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// buildSortedQuery builds a query string with sorted parameters
func buildSortedQuery(values url.Values, sortedKeys []string) string {
	var parts []string
	for _, key := range sortedKeys {
		for _, val := range values[key] {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(val))
		}
	}
	return strings.Join(parts, "&")
}

// =============================================================================
// Validation
// =============================================================================

// Web document validation errors
var (
	ErrEmptyURL         = errors.New("URL cannot be empty")
	ErrInvalidURL       = errors.New("URL is invalid")
	ErrInvalidScheme    = errors.New("URL scheme must be http or https")
	ErrMissingHost      = errors.New("URL must have a host")
	ErrZeroFetchedAt    = errors.New("FetchedAt cannot be zero")
	ErrInvalidStatus    = errors.New("StatusCode must be between 100 and 599")
	ErrEmptyContentType = errors.New("ContentType cannot be empty")
)

// Validate checks that the WebFetchDocument has valid required fields
func (w *WebFetchDocument) Validate() error {
	if err := w.validateURL(); err != nil {
		return err
	}
	return w.validateMetadata()
}

// validateURL validates the URL field
func (w *WebFetchDocument) validateURL() error {
	if w.URL == "" {
		return ErrEmptyURL
	}

	parsed, err := url.Parse(w.URL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	if !isValidScheme(parsed.Scheme) {
		return ErrInvalidScheme
	}

	if parsed.Host == "" {
		return ErrMissingHost
	}

	return nil
}

// isValidScheme checks if the URL scheme is http or https
func isValidScheme(scheme string) bool {
	return scheme == "http" || scheme == "https"
}

// validateMetadata validates non-URL metadata fields
func (w *WebFetchDocument) validateMetadata() error {
	if w.FetchedAt.IsZero() {
		return ErrZeroFetchedAt
	}

	if !isValidStatusCode(w.StatusCode) {
		return ErrInvalidStatus
	}

	if w.ContentType == "" {
		return ErrEmptyContentType
	}

	return nil
}

// isValidStatusCode checks if status code is in valid HTTP range
func isValidStatusCode(code int) bool {
	return code >= 100 && code <= 599
}

// =============================================================================
// Helper Methods
// =============================================================================

// IsSuccess returns true if the HTTP status code indicates success (2xx)
func (w *WebFetchDocument) IsSuccess() bool {
	return w.StatusCode >= 200 && w.StatusCode < 300
}

// HasCodeSnippets returns true if the document contains code snippets
func (w *WebFetchDocument) HasCodeSnippets() bool {
	return len(w.CodeSnippets) > 0
}

// GenerateID generates a document ID from the normalized URL
func (w *WebFetchDocument) GenerateID() string {
	return NormalizeURL(w.URL)
}
