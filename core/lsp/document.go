package lsp

import (
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Capacity limits
// ---------------------------------------------------------------------------

// maxOpenDocuments caps the number of simultaneously open documents per client.
// Derived from: typical editing session touches < 20 files actively;
// 256 supports large refactoring scenarios.
const maxOpenDocuments = 256

// ---------------------------------------------------------------------------
// openDocument
// ---------------------------------------------------------------------------

// openDocument tracks state for a single file in an LSP session.
type openDocument struct {
	uri        string
	languageID string
	version    int
	lastText   string
	lines      []string // cached line splits, rebuilt on text change
}

// ---------------------------------------------------------------------------
// DocumentTracker
// ---------------------------------------------------------------------------

// DocumentTracker manages the set of documents open in an LSP session.
// It is safe for concurrent access.
type DocumentTracker struct {
	mu   sync.RWMutex
	docs map[string]*openDocument
}

// NewDocumentTracker returns a ready-to-use tracker.
func NewDocumentTracker() *DocumentTracker {
	return &DocumentTracker{
		docs: make(map[string]*openDocument, maxOpenDocuments),
	}
}

// Open registers a document as open. Returns the initial version (1) and
// true on success. Returns 0, false if the document is already open or
// the tracker is at capacity.
func (dt *DocumentTracker) Open(uri, languageID, text string) (int, bool) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if _, exists := dt.docs[uri]; exists {
		return 0, false
	}
	if len(dt.docs) >= maxOpenDocuments {
		return 0, false
	}
	dt.docs[uri] = &openDocument{
		uri:        uri,
		languageID: languageID,
		version:    1,
		lastText:   text,
		lines:      splitDocLines(text),
	}
	return 1, true
}

// Change records a content change and increments the version counter.
// Returns the new version and true on success, or 0, false if the
// document is not open.
func (dt *DocumentTracker) Change(uri, newText string) (int, bool) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	doc, ok := dt.docs[uri]
	if !ok {
		return 0, false
	}
	doc.version++
	doc.lastText = newText
	doc.lines = splitDocLines(newText)
	return doc.version, true
}

// Save marks a save event for a document. Returns false if not open.
func (dt *DocumentTracker) Save(uri string) bool {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	_, ok := dt.docs[uri]
	return ok
}

// Close removes a document from tracking. Returns false if not open.
func (dt *DocumentTracker) Close(uri string) bool {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if _, ok := dt.docs[uri]; !ok {
		return false
	}
	delete(dt.docs, uri)
	return true
}

// IsOpen reports whether a URI is currently tracked.
func (dt *DocumentTracker) IsOpen(uri string) bool {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	_, ok := dt.docs[uri]
	return ok
}

// Count returns the number of open documents.
func (dt *DocumentTracker) Count() int {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	return len(dt.docs)
}

// AllURIs returns all currently tracked URIs.
func (dt *DocumentTracker) AllURIs() []string {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	uris := make([]string, 0, len(dt.docs))
	for uri := range dt.docs {
		uris = append(uris, uri)
	}
	return uris
}

// Version returns the current version for a tracked URI, or 0 if not open.
func (dt *DocumentTracker) Version(uri string) int {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	doc, ok := dt.docs[uri]
	if !ok {
		return 0
	}
	return doc.version
}

// LanguageID returns the language ID for a tracked URI, or empty if not open.
func (dt *DocumentTracker) LanguageID(uri string) string {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	doc, ok := dt.docs[uri]
	if !ok {
		return ""
	}
	return doc.languageID
}

// LineText returns the text of a 0-indexed line for a tracked URI.
// Returns "" if the document is not tracked or the line is out of range.
// Uses cached line splits for O(1) lookup.
func (dt *DocumentTracker) LineText(uri string, line int) string {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	doc, ok := dt.docs[uri]
	if !ok || line < 0 || line >= len(doc.lines) {
		return ""
	}
	return doc.lines[line]
}

// splitDocLines splits text into lines, stripping trailing newlines from
// each line. Rebuilt on Open and Change so LineText is O(1).
func splitDocLines(text string) []string {
	raw := strings.SplitAfter(text, "\n")
	// SplitAfter may produce an empty trailing element if text ends with \n.
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	for i, l := range raw {
		raw[i] = strings.TrimRight(l, "\n")
	}
	return raw
}
