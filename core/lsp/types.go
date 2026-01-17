// Package lsp provides types and utilities for Language Server Protocol integration.
// It supports automatic detection, lifecycle management, and communication with
// language servers like gopls, typescript-language-server, pyright, and rust-analyzer.
package lsp

import (
	"os"
	"sync"
)

// ServerID uniquely identifies a language server.
// Examples: "gopls", "typescript-language-server", "pyright", "rust-analyzer"
type ServerID string

// Common server IDs for well-known language servers.
const (
	ServerGopls      ServerID = "gopls"
	ServerTypeScript ServerID = "typescript-language-server"
	ServerPyright    ServerID = "pyright"
	ServerRustAna    ServerID = "rust-analyzer"
	ServerClangd     ServerID = "clangd"
	ServerLuaLS      ServerID = "lua-language-server"
	ServerZLS        ServerID = "zls"
	ServerOCamlLSP   ServerID = "ocamllsp"
)

// LanguageServerDefinition defines a language server's configuration and detection criteria.
// Each definition specifies how to start the server and what files/languages it supports.
type LanguageServerDefinition struct {
	// ID is the unique identifier for this server.
	ID ServerID

	// Name is the human-readable name of the language server.
	Name string

	// Command is the executable name or path to run.
	Command string

	// Args are the default command-line arguments passed to the server.
	Args []string

	// Extensions lists file extensions this server handles (e.g., ".go", ".ts", ".py").
	Extensions []string

	// LanguageIDs lists LSP language identifiers (e.g., "go", "typescript", "python").
	LanguageIDs []string

	// RootMarkers are files/directories that indicate a project root for this server.
	// Examples: "go.mod", "package.json", "pyproject.toml", "Cargo.toml"
	RootMarkers []string

	// Enabled is an optional function that returns whether the server should be used.
	// If nil, the server is always considered enabled when detected.
	Enabled func() bool

	// AutoDownload specifies how to automatically install the server if not found.
	// If nil, auto-download is not supported.
	AutoDownload *AutoDownloadConfig
}

// AutoDownloadConfig specifies how to automatically download and install a language server.
type AutoDownloadConfig struct {
	// Source indicates the package manager or download source.
	// Supported values: "npm", "go", "github", "pip", "cargo"
	Source string

	// Package is the package name to install (e.g., "typescript-language-server", "golang.org/x/tools/gopls").
	Package string

	// Binary is the name of the binary after installation, if different from the package name.
	Binary string
}

// AutoDownload sources.
const (
	SourceNPM    = "npm"
	SourceGo     = "go"
	SourceGitHub = "github"
	SourcePip    = "pip"
	SourceCargo  = "cargo"
)

// LSPClient represents an active connection to a language server instance.
// Each client manages communication with a single server process for a specific project.
type LSPClient struct {
	// ID is a unique identifier for this client instance.
	ID string

	// ServerID identifies which language server this client connects to.
	ServerID ServerID

	// ProjectRoot is the root directory of the project this client serves.
	ProjectRoot string

	// Process is the underlying server process.
	Process *os.Process

	// Capabilities holds the server's declared capabilities after initialization.
	Capabilities ServerCapabilities

	// Status indicates the current state of the client connection.
	Status ClientStatus

	// mu protects concurrent access to client state.
	mu sync.RWMutex
}

// ServerCapabilities represents the capabilities reported by a language server.
// This is a subset of the full LSP ServerCapabilities.
type ServerCapabilities struct {
	// HoverProvider indicates the server supports hover information.
	HoverProvider bool

	// DefinitionProvider indicates the server supports go-to-definition.
	DefinitionProvider bool

	// ReferencesProvider indicates the server supports find-references.
	ReferencesProvider bool

	// CompletionProvider indicates the server supports code completion.
	CompletionProvider bool

	// DiagnosticProvider indicates the server supports pull-based diagnostics.
	DiagnosticProvider bool

	// DocumentSymbolProvider indicates the server supports document symbols.
	DocumentSymbolProvider bool

	// WorkspaceSymbolProvider indicates the server supports workspace symbols.
	WorkspaceSymbolProvider bool

	// CodeActionProvider indicates the server supports code actions.
	CodeActionProvider bool

	// RenameProvider indicates the server supports rename refactoring.
	RenameProvider bool
}

// ClientStatus represents the current state of an LSP client connection.
type ClientStatus int

const (
	// StatusStarting indicates the client is initializing the server connection.
	StatusStarting ClientStatus = iota

	// StatusReady indicates the client is connected and ready for requests.
	StatusReady

	// StatusError indicates the client encountered an error.
	StatusError

	// StatusStopped indicates the client has been stopped.
	StatusStopped
)

var clientStatusNames = map[ClientStatus]string{
	StatusStarting: "starting",
	StatusReady:    "ready",
	StatusError:    "error",
	StatusStopped:  "stopped",
}

// String returns the string representation of ClientStatus.
func (s ClientStatus) String() string {
	if name, ok := clientStatusNames[s]; ok {
		return name
	}
	return "unknown"
}

// DiagnosticResult holds diagnostics returned from a language server for a file.
type DiagnosticResult struct {
	// ServerID identifies which server produced these diagnostics.
	ServerID ServerID

	// FilePath is the absolute path to the file these diagnostics apply to.
	FilePath string

	// Diagnostics is the list of diagnostic messages for the file.
	Diagnostics []Diagnostic
}

// Diagnostic represents a single diagnostic message from a language server.
// This follows the LSP Diagnostic structure.
type Diagnostic struct {
	// Range indicates the text range this diagnostic applies to.
	Range Range

	// Severity indicates the severity level of the diagnostic.
	Severity DiagnosticSeverity

	// Code is an optional diagnostic code (can be string or number in LSP).
	Code string

	// Source identifies the tool that produced this diagnostic (e.g., "gopls", "typescript").
	Source string

	// Message is the human-readable diagnostic message.
	Message string

	// RelatedInformation provides additional locations related to this diagnostic.
	RelatedInformation []DiagnosticRelatedInformation
}

// DiagnosticRelatedInformation represents additional context for a diagnostic.
type DiagnosticRelatedInformation struct {
	// Location is the related location.
	Location Location

	// Message describes the relationship to the primary diagnostic.
	Message string
}

// Location represents a location in a document.
type Location struct {
	// URI is the document URI (typically file:// URI).
	URI string

	// Range is the text range within the document.
	Range Range
}

// DiagnosticSeverity indicates the severity level of a diagnostic.
type DiagnosticSeverity int

const (
	// SeverityError indicates an error that prevents compilation or execution.
	SeverityError DiagnosticSeverity = 1

	// SeverityWarning indicates a potential problem that should be addressed.
	SeverityWarning DiagnosticSeverity = 2

	// SeverityInformation indicates informational messages.
	SeverityInformation DiagnosticSeverity = 3

	// SeverityHint indicates hints or suggestions for improvement.
	SeverityHint DiagnosticSeverity = 4
)

var severityNames = map[DiagnosticSeverity]string{
	SeverityError:       "error",
	SeverityWarning:     "warning",
	SeverityInformation: "information",
	SeverityHint:        "hint",
}

// String returns the string representation of DiagnosticSeverity.
func (s DiagnosticSeverity) String() string {
	if name, ok := severityNames[s]; ok {
		return name
	}
	return "unknown"
}

// Range represents a text range in a document with start and end positions.
type Range struct {
	// Start is the starting position (inclusive).
	Start Position

	// End is the ending position (exclusive).
	End Position
}

// Position represents a position in a text document.
// Line and character are 0-based, following LSP conventions.
type Position struct {
	// Line is the 0-based line number.
	Line int

	// Character is the 0-based character offset within the line.
	Character int
}

// LSPConfig provides user-configurable overrides for language server settings.
// This allows users to customize or disable specific servers.
type LSPConfig struct {
	// Disabled indicates whether this server should be disabled.
	Disabled bool

	// Command overrides the default command to run.
	Command string

	// Args overrides the default command arguments.
	Args []string

	// AutoDownload indicates whether to automatically download the server if missing.
	AutoDownload bool

	// RootMarkers allows users to specify additional root markers.
	RootMarkers []string

	// InitializationOptions are custom options passed during LSP initialization.
	InitializationOptions map[string]interface{}
}

// DetectionResult represents the outcome of attempting to detect a language server for a project.
type DetectionResult struct {
	// ServerID is the detected server identifier.
	ServerID ServerID

	// Confidence indicates how confident the detection is (0.0 to 1.0).
	// 1.0 means certain (e.g., explicit config), 0.5 means probable (e.g., file extension match).
	Confidence float64

	// Reason describes why this server was detected.
	Reason string

	// RootPath is the detected project root for this server.
	RootPath string
}

// LSPRegistry maintains a thread-safe registry of language server definitions.
// It provides lookup and iteration capabilities for server discovery.
type LSPRegistry struct {
	// servers maps ServerID to LanguageServerDefinition.
	servers map[ServerID]*LanguageServerDefinition

	// byExtension maps file extensions to server IDs for quick lookup.
	byExtension map[string][]ServerID

	// byLanguageID maps LSP language IDs to server IDs.
	byLanguageID map[string][]ServerID

	// mu protects concurrent access to the registry.
	mu sync.RWMutex
}

// NewLSPRegistry creates a new empty LSP registry.
func NewLSPRegistry() *LSPRegistry {
	return &LSPRegistry{
		servers:      make(map[ServerID]*LanguageServerDefinition),
		byExtension:  make(map[string][]ServerID),
		byLanguageID: make(map[string][]ServerID),
	}
}

// Register adds a language server definition to the registry.
// If a server with the same ID already exists, it will be replaced.
func (r *LSPRegistry) Register(def *LanguageServerDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.servers[def.ID] = def

	// Index by extension
	for _, ext := range def.Extensions {
		r.byExtension[ext] = appendUnique(r.byExtension[ext], def.ID)
	}

	// Index by language ID
	for _, langID := range def.LanguageIDs {
		r.byLanguageID[langID] = appendUnique(r.byLanguageID[langID], def.ID)
	}
}

// Get retrieves a language server definition by ID.
// Returns nil if the server is not found.
func (r *LSPRegistry) Get(id ServerID) *LanguageServerDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.servers[id]
}

// GetByExtension returns all server IDs that support the given file extension.
func (r *LSPRegistry) GetByExtension(ext string) []ServerID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byExtension[ext]
}

// GetByLanguageID returns all server IDs that support the given LSP language ID.
func (r *LSPRegistry) GetByLanguageID(langID string) []ServerID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byLanguageID[langID]
}

// All returns all registered server definitions.
func (r *LSPRegistry) All() []*LanguageServerDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*LanguageServerDefinition, 0, len(r.servers))
	for _, def := range r.servers {
		result = append(result, def)
	}
	return result
}

// IDs returns all registered server IDs.
func (r *LSPRegistry) IDs() []ServerID {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ServerID, 0, len(r.servers))
	for id := range r.servers {
		result = append(result, id)
	}
	return result
}

// Has checks if a server with the given ID is registered.
func (r *LSPRegistry) Has(id ServerID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.servers[id]
	return exists
}

// appendUnique appends id to slice if not already present.
func appendUnique(slice []ServerID, id ServerID) []ServerID {
	for _, existing := range slice {
		if existing == id {
			return slice
		}
	}
	return append(slice, id)
}

// DefaultRegistry is the global registry instance pre-populated with common servers.
var DefaultRegistry = NewLSPRegistry()
