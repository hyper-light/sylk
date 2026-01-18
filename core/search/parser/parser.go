// Package parser provides language-specific parsers for extracting symbols, comments,
// imports, and metadata from source files. It supports Go, TypeScript, Python,
// Markdown, and configuration files (JSON, YAML, TOML).
package parser

import (
	"errors"
	"path/filepath"
	"sync"
)

// =============================================================================
// Symbol Types
// =============================================================================

// Symbol represents a code symbol extracted from source code.
type Symbol struct {
	// Name is the identifier of the symbol
	Name string

	// Kind is the type of symbol (function, type, interface, class, variable, const)
	Kind string

	// Signature is the function signature or type definition
	Signature string

	// Line is the line number where the symbol is defined (1-indexed)
	Line int

	// Exported indicates whether the symbol is exported (public)
	Exported bool
}

// SymbolKind constants for consistent symbol categorization.
const (
	KindFunction  = "function"
	KindType      = "type"
	KindInterface = "interface"
	KindClass     = "class"
	KindVariable  = "variable"
	KindConst     = "const"
	KindHeading   = "heading"
	KindCodeBlock = "code_block"
	KindLink      = "link"
	KindKey       = "key"
)

// =============================================================================
// ParseResult
// =============================================================================

// ParseResult contains the extracted information from parsing a file.
type ParseResult struct {
	// Symbols contains all extracted symbols (functions, types, classes, etc.)
	Symbols []Symbol

	// Comments is the concatenated comments from the source file
	Comments string

	// Imports lists all import/dependency paths
	Imports []string

	// Metadata contains additional key-value pairs extracted from the file
	Metadata map[string]string
}

// NewParseResult creates a new ParseResult with initialized fields.
func NewParseResult() *ParseResult {
	return &ParseResult{
		Symbols:  make([]Symbol, 0),
		Imports:  make([]string, 0),
		Metadata: make(map[string]string),
	}
}

// =============================================================================
// Parser Interface
// =============================================================================

// Parser defines the interface for language-specific file parsers.
type Parser interface {
	// Parse extracts symbols, comments, imports, and metadata from file content.
	Parse(content []byte, path string) (*ParseResult, error)

	// Extensions returns the file extensions this parser handles (e.g., [".go"]).
	Extensions() []string
}

// =============================================================================
// Parser Registry
// =============================================================================

// ParserRegistry manages parser registration and lookup by file extension.
type ParserRegistry struct {
	mu      sync.RWMutex
	parsers map[string]Parser
}

// NewParserRegistry creates a new empty parser registry.
func NewParserRegistry() *ParserRegistry {
	return &ParserRegistry{
		parsers: make(map[string]Parser),
	}
}

// RegisterParser registers a parser for all its supported extensions.
func (r *ParserRegistry) RegisterParser(parser Parser) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, ext := range parser.Extensions() {
		r.parsers[ext] = parser
	}
}

// GetParser returns the parser for a given file extension.
// Returns nil if no parser is registered for the extension.
func (r *ParserRegistry) GetParser(extension string) Parser {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.parsers[extension]
}

// GetParserForFile returns the parser for a file based on its path.
// Returns nil if no parser is registered for the file's extension.
func (r *ParserRegistry) GetParserForFile(path string) Parser {
	ext := filepath.Ext(path)
	return r.GetParser(ext)
}

// HasParser returns true if a parser is registered for the extension.
func (r *ParserRegistry) HasParser(extension string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.parsers[extension]
	return ok
}

// Extensions returns all registered file extensions.
func (r *ParserRegistry) Extensions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	extensions := make([]string, 0, len(r.parsers))
	for ext := range r.parsers {
		extensions = append(extensions, ext)
	}
	return extensions
}

// =============================================================================
// Default Registry
// =============================================================================

// defaultRegistry is the global parser registry.
var defaultRegistry = NewParserRegistry()

// DefaultRegistry returns the default global parser registry.
func DefaultRegistry() *ParserRegistry {
	return defaultRegistry
}

// RegisterParser registers a parser in the default registry.
func RegisterParser(parser Parser) {
	defaultRegistry.RegisterParser(parser)
}

// GetParser returns a parser from the default registry.
func GetParser(extension string) Parser {
	return defaultRegistry.GetParser(extension)
}

// GetParserForFile returns a parser from the default registry for a file path.
func GetParserForFile(path string) Parser {
	return defaultRegistry.GetParserForFile(path)
}

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrEmptyContent is returned when the content to parse is empty.
	ErrEmptyContent = errors.New("content is empty")

	// ErrNoParser is returned when no parser is available for a file type.
	ErrNoParser = errors.New("no parser available for file type")
)
