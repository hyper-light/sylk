// Package search provides document types and search functionality for the Sylk
// Document Search System. It includes base document types for indexing source code,
// markdown, configuration files, LLM communications, web fetches, notes, and git commits.
package search

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// =============================================================================
// Document Type Enum
// =============================================================================

// DocumentType represents the category of a document in the search index.
type DocumentType string

const (
	// DocTypeSourceCode represents source code files (.go, .py, .js, etc.)
	DocTypeSourceCode DocumentType = "source_code"

	// DocTypeMarkdown represents markdown documentation files
	DocTypeMarkdown DocumentType = "markdown"

	// DocTypeConfig represents configuration files (YAML, JSON, TOML, etc.)
	DocTypeConfig DocumentType = "config"

	// DocTypeLLMPrompt represents prompts sent to LLM providers
	DocTypeLLMPrompt DocumentType = "llm_prompt"

	// DocTypeLLMResponse represents responses received from LLM providers
	DocTypeLLMResponse DocumentType = "llm_response"

	// DocTypeWebFetch represents fetched web page content
	DocTypeWebFetch DocumentType = "web_fetch"

	// DocTypeNote represents user notes and annotations
	DocTypeNote DocumentType = "note"

	// DocTypeGitCommit represents git commit messages and metadata
	DocTypeGitCommit DocumentType = "git_commit"
)

// validDocumentTypes contains all valid document types for validation.
var validDocumentTypes = map[DocumentType]struct{}{
	DocTypeSourceCode:  {},
	DocTypeMarkdown:    {},
	DocTypeConfig:      {},
	DocTypeLLMPrompt:   {},
	DocTypeLLMResponse: {},
	DocTypeWebFetch:    {},
	DocTypeNote:        {},
	DocTypeGitCommit:   {},
}

// IsValid returns true if the document type is a recognized type.
func (dt DocumentType) IsValid() bool {
	_, ok := validDocumentTypes[dt]
	return ok
}

// String returns the string representation of the document type.
func (dt DocumentType) String() string {
	return string(dt)
}

// =============================================================================
// Symbol Kind Enum
// =============================================================================

// SymbolKind represents the kind of symbol extracted from source code.
type SymbolKind string

const (
	// SymbolKindFunction represents function or method declarations
	SymbolKindFunction SymbolKind = "function"

	// SymbolKindType represents type declarations (struct, class, etc.)
	SymbolKindType SymbolKind = "type"

	// SymbolKindVariable represents variable declarations
	SymbolKindVariable SymbolKind = "variable"

	// SymbolKindConst represents constant declarations
	SymbolKindConst SymbolKind = "const"

	// SymbolKindInterface represents interface declarations
	SymbolKindInterface SymbolKind = "interface"
)

// validSymbolKinds contains all valid symbol kinds for validation.
var validSymbolKinds = map[SymbolKind]struct{}{
	SymbolKindFunction:  {},
	SymbolKindType:      {},
	SymbolKindVariable:  {},
	SymbolKindConst:     {},
	SymbolKindInterface: {},
}

// IsValid returns true if the symbol kind is a recognized kind.
func (sk SymbolKind) IsValid() bool {
	_, ok := validSymbolKinds[sk]
	return ok
}

// String returns the string representation of the symbol kind.
func (sk SymbolKind) String() string {
	return string(sk)
}

// =============================================================================
// Symbol Info
// =============================================================================

// SymbolInfo represents metadata about a symbol extracted from source code.
type SymbolInfo struct {
	// Name is the identifier of the symbol
	Name string `json:"name"`

	// Kind is the type of symbol (function, type, variable, const, interface)
	Kind SymbolKind `json:"kind"`

	// Signature is the full signature for functions or type definition for types
	Signature string `json:"signature,omitempty"`

	// Line is the line number where the symbol is defined (1-indexed)
	Line int `json:"line"`

	// Exported indicates whether the symbol is exported (public)
	Exported bool `json:"exported"`
}

// Validate checks that the SymbolInfo has valid required fields.
func (s *SymbolInfo) Validate() error {
	if s.Name == "" {
		return ErrSymbolNameEmpty
	}
	if !s.Kind.IsValid() {
		return ErrSymbolKindInvalid
	}
	if s.Line < 1 {
		return ErrSymbolLineInvalid
	}
	return nil
}

// =============================================================================
// Document
// =============================================================================

// Document represents an indexed document in the search system.
// It can represent source code, markdown, configuration, LLM communications,
// web fetches, notes, or git commits.
type Document struct {
	// ID is the content hash (SHA-256) serving as a unique identifier
	ID string `json:"id"`

	// Path is the file path or virtual path for non-file documents
	Path string `json:"path"`

	// Type is the category of document
	Type DocumentType `json:"type"`

	// Language is the programming language for source code documents
	Language string `json:"language,omitempty"`

	// Content is the full text content of the document
	Content string `json:"content"`

	// Symbols is a list of symbol names extracted from source code
	Symbols []string `json:"symbols,omitempty"`

	// Comments contains extracted comments from source code
	Comments string `json:"comments,omitempty"`

	// Imports is a list of import/dependency paths
	Imports []string `json:"imports,omitempty"`

	// Checksum is the SHA-256 hash of the content for change detection
	Checksum string `json:"checksum"`

	// ModifiedAt is when the source file was last modified
	ModifiedAt time.Time `json:"modified_at"`

	// IndexedAt is when this document was indexed
	IndexedAt time.Time `json:"indexed_at"`

	// GitCommit is the git commit hash when this version was indexed
	GitCommit string `json:"git_commit,omitempty"`
}

// Validate checks that the Document has all required fields with valid values.
func (d *Document) Validate() error {
	if d.ID == "" {
		return ErrDocumentIDEmpty
	}
	if d.Path == "" {
		return ErrDocumentPathEmpty
	}
	if !d.Type.IsValid() {
		return ErrDocumentTypeInvalid
	}
	if d.Checksum == "" {
		return ErrDocumentChecksumEmpty
	}
	return nil
}

// IsCodeDocument returns true if this document represents source code.
func (d *Document) IsCodeDocument() bool {
	return d.Type == DocTypeSourceCode
}

// HasSymbols returns true if the document has extracted symbols.
func (d *Document) HasSymbols() bool {
	return len(d.Symbols) > 0
}

// IsLLMDocument returns true if this document is an LLM prompt or response.
func (d *Document) IsLLMDocument() bool {
	return d.Type == DocTypeLLMPrompt || d.Type == DocTypeLLMResponse
}

// =============================================================================
// Document ID Generation
// =============================================================================

// GenerateDocumentID generates a document ID from content using SHA-256.
// Returns a hex-encoded string of the hash.
func GenerateDocumentID(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// GenerateChecksum generates a checksum from content using SHA-256.
// This is an alias for GenerateDocumentID but semantically represents
// a checksum rather than an identifier.
func GenerateChecksum(content []byte) string {
	return GenerateDocumentID(content)
}

// =============================================================================
// Errors
// =============================================================================

var (
	// Document validation errors
	ErrDocumentIDEmpty       = errors.New("document ID cannot be empty")
	ErrDocumentPathEmpty     = errors.New("document path cannot be empty")
	ErrDocumentTypeInvalid   = errors.New("document type is invalid")
	ErrDocumentChecksumEmpty = errors.New("document checksum cannot be empty")

	// Symbol validation errors
	ErrSymbolNameEmpty   = errors.New("symbol name cannot be empty")
	ErrSymbolKindInvalid = errors.New("symbol kind is invalid")
	ErrSymbolLineInvalid = errors.New("symbol line must be >= 1")
)
