// Package indexer provides file scanning and indexing functionality for the Sylk
// Document Search System.
package indexer

import (
	"errors"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/parser"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrNilFileInfo indicates the FileInfo parameter was nil.
	ErrNilFileInfo = errors.New("file info cannot be nil")

	// ErrEmptyPath indicates the file path was empty.
	ErrEmptyPath = errors.New("file path cannot be empty")
)

// =============================================================================
// Document Type Detection Maps
// =============================================================================

// builderExtToDocType maps file extensions to document types.
var builderExtToDocType = map[string]search.DocumentType{
	// Source code extensions
	".go":   search.DocTypeSourceCode,
	".py":   search.DocTypeSourceCode,
	".pyi":  search.DocTypeSourceCode,
	".pyw":  search.DocTypeSourceCode,
	".js":   search.DocTypeSourceCode,
	".jsx":  search.DocTypeSourceCode,
	".ts":   search.DocTypeSourceCode,
	".tsx":  search.DocTypeSourceCode,
	".mjs":  search.DocTypeSourceCode,
	".cjs":  search.DocTypeSourceCode,
	".rs":   search.DocTypeSourceCode,
	".java": search.DocTypeSourceCode,
	".c":    search.DocTypeSourceCode,
	".cpp":  search.DocTypeSourceCode,
	".cc":   search.DocTypeSourceCode,
	".cxx":  search.DocTypeSourceCode,
	".h":    search.DocTypeSourceCode,
	".hpp":  search.DocTypeSourceCode,
	".rb":   search.DocTypeSourceCode,

	// Markdown extensions
	".md":       search.DocTypeMarkdown,
	".markdown": search.DocTypeMarkdown,
	".mdx":      search.DocTypeMarkdown,

	// Config extensions
	".json": search.DocTypeConfig,
	".yaml": search.DocTypeConfig,
	".yml":  search.DocTypeConfig,
	".toml": search.DocTypeConfig,
	".xml":  search.DocTypeConfig,
	".env":  search.DocTypeConfig,
	".ini":  search.DocTypeConfig,
}

// builderExtToLang maps file extensions to language names.
var builderExtToLang = map[string]string{
	".go":       "go",
	".py":       "python",
	".pyi":      "python",
	".pyw":      "python",
	".js":       "javascript",
	".jsx":      "javascript",
	".mjs":      "javascript",
	".cjs":      "javascript",
	".ts":       "typescript",
	".tsx":      "typescript",
	".rs":       "rust",
	".java":     "java",
	".c":        "c",
	".cpp":      "cpp",
	".cc":       "cpp",
	".cxx":      "cpp",
	".h":        "c",
	".hpp":      "cpp",
	".rb":       "ruby",
	".md":       "markdown",
	".markdown": "markdown",
	".mdx":      "markdown",
	".json":     "json",
	".yaml":     "yaml",
	".yml":      "yaml",
	".toml":     "toml",
	".xml":      "xml",
	".env":      "env",
	".ini":      "ini",
}

// =============================================================================
// ContentDocumentBuilder
// =============================================================================

// ContentDocumentBuilder transforms scanned files into indexable documents.
// Unlike the DocumentBuilder interface which reads files itself, this builder
// accepts pre-read content, making it suitable for use cases where the caller
// already has the file content (e.g., from a file watcher or in-memory source).
type ContentDocumentBuilder struct {
	registry *parser.ParserRegistry
}

// NewDocumentBuilder creates a new builder with the default parser registry.
func NewDocumentBuilder() *ContentDocumentBuilder {
	return &ContentDocumentBuilder{
		registry: parser.DefaultRegistry(),
	}
}

// NewDocumentBuilderWithRegistry creates a builder with a custom parser registry.
func NewDocumentBuilderWithRegistry(registry *parser.ParserRegistry) *ContentDocumentBuilder {
	if registry == nil {
		registry = parser.DefaultRegistry()
	}
	return &ContentDocumentBuilder{
		registry: registry,
	}
}

// =============================================================================
// Build Method
// =============================================================================

// Build creates a Document from FileInfo and file content.
// It detects the document type from extension, parses for symbols/comments/imports,
// generates ID from content hash, and populates all document fields.
func (b *ContentDocumentBuilder) Build(info *FileInfo, content []byte) (*search.Document, error) {
	if err := b.validateInput(info); err != nil {
		return nil, err
	}

	doc := b.createBaseDocument(info, content)
	b.applyParseResult(doc, info, content)

	return doc, nil
}

// =============================================================================
// Validation
// =============================================================================

// validateInput checks that the input parameters are valid.
func (b *ContentDocumentBuilder) validateInput(info *FileInfo) error {
	if info == nil {
		return ErrNilFileInfo
	}
	if info.Path == "" {
		return ErrEmptyPath
	}
	return nil
}

// =============================================================================
// Document Creation
// =============================================================================

// createBaseDocument creates a Document with basic fields populated.
func (b *ContentDocumentBuilder) createBaseDocument(info *FileInfo, content []byte) *search.Document {
	ext := builderNormalizeExt(info.Extension)

	return &search.Document{
		ID:         search.GenerateDocumentID(content),
		Path:       info.Path,
		Type:       builderDetectDocType(ext),
		Language:   builderDetectLang(ext),
		Content:    string(content),
		Checksum:   search.GenerateChecksum(content),
		ModifiedAt: info.ModTime,
		IndexedAt:  time.Now(),
	}
}

// applyParseResult runs the appropriate parser and applies results to the document.
func (b *ContentDocumentBuilder) applyParseResult(doc *search.Document, info *FileInfo, content []byte) {
	ext := builderNormalizeExt(info.Extension)
	p := b.registry.GetParser(ext)
	if p == nil {
		return
	}

	result, err := p.Parse(content, info.Path)
	if err != nil || result == nil {
		return
	}

	doc.Symbols = builderExtractSymbols(result.Symbols)
	doc.Imports = result.Imports
	doc.Comments = result.Comments

	builderApplyLangMeta(doc, result)
}

// =============================================================================
// Helper Functions
// =============================================================================

// builderNormalizeExt converts an extension to lowercase for consistent lookup.
func builderNormalizeExt(ext string) string {
	return strings.ToLower(ext)
}

// builderDetectDocType returns the document type for a file extension.
// Unknown extensions default to DocTypeSourceCode.
func builderDetectDocType(ext string) search.DocumentType {
	if docType, ok := builderExtToDocType[ext]; ok {
		return docType
	}
	return search.DocTypeSourceCode
}

// builderDetectLang returns the language name for a file extension.
// Unknown extensions return an empty string.
func builderDetectLang(ext string) string {
	if lang, ok := builderExtToLang[ext]; ok {
		return lang
	}
	return ""
}

// builderExtractSymbols converts parser.Symbol slice to a slice of symbol names.
func builderExtractSymbols(symbols []parser.Symbol) []string {
	if len(symbols) == 0 {
		return nil
	}

	names := make([]string, 0, len(symbols))
	for _, sym := range symbols {
		if sym.Name != "" {
			names = append(names, sym.Name)
		}
	}
	return names
}

// builderApplyLangMeta updates document language from parser metadata if available.
func builderApplyLangMeta(doc *search.Document, result *parser.ParseResult) {
	if result.Metadata == nil {
		return
	}
	if lang, ok := result.Metadata["language"]; ok && lang != "" {
		doc.Language = lang
	}
}
