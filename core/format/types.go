// Package format provides formatter detection, registration, and execution
// for code formatting tools like gofmt, prettier, black, rustfmt, etc.
package format

import (
	"sync"
	"time"
)

// FormatterID is a unique string identifier for a formatter.
// Examples: "gofmt", "prettier", "black", "rustfmt"
type FormatterID string

// FormatterDefinition describes a code formatter and how to detect/invoke it.
type FormatterDefinition struct {
	// ID is the unique identifier for this formatter
	ID FormatterID

	// Name is the human-readable display name
	Name string

	// Command is the executable name or path
	Command string

	// Args are the default command-line arguments
	Args []string

	// Extensions lists file extensions this formatter handles (e.g., ".go", ".ts")
	Extensions []string

	// ConfigFiles lists configuration files that indicate this formatter is used
	// (e.g., ".prettierrc", "pyproject.toml")
	ConfigFiles []string

	// Priority determines order when multiple formatters match (higher = preferred)
	Priority int

	// Enabled is an optional function that checks if the formatter is available
	// in the given project root. Returns true if the formatter should be used.
	Enabled func(root string) bool
}

// FormatterRegistry provides thread-safe storage and lookup of formatter definitions.
type FormatterRegistry struct {
	mu          sync.RWMutex
	formatters  map[FormatterID]*FormatterDefinition
	byExtension map[string][]*FormatterDefinition
}

// NewFormatterRegistry creates an initialized FormatterRegistry.
func NewFormatterRegistry() *FormatterRegistry {
	return &FormatterRegistry{
		formatters:  make(map[FormatterID]*FormatterDefinition),
		byExtension: make(map[string][]*FormatterDefinition),
	}
}

// Register adds a formatter definition to the registry.
// Returns an error if a formatter with the same ID is already registered.
func (r *FormatterRegistry) Register(def *FormatterDefinition) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.formatters[def.ID]; exists {
		return &DuplicateFormatterError{ID: def.ID}
	}

	r.formatters[def.ID] = def

	// Index by extension for fast lookup
	for _, ext := range def.Extensions {
		r.byExtension[ext] = append(r.byExtension[ext], def)
	}

	return nil
}

// Get retrieves a formatter definition by ID.
// Returns the definition and true if found, nil and false otherwise.
func (r *FormatterRegistry) Get(id FormatterID) (*FormatterDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	def, ok := r.formatters[id]
	return def, ok
}

// List returns all registered formatter definitions.
func (r *FormatterRegistry) List() []*FormatterDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*FormatterDefinition, 0, len(r.formatters))
	for _, def := range r.formatters {
		result = append(result, def)
	}
	return result
}

// GetByExtension returns all formatters that handle the given file extension.
// The extension should include the leading dot (e.g., ".go").
func (r *FormatterRegistry) GetByExtension(ext string) []*FormatterDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := r.byExtension[ext]
	if defs == nil {
		return nil
	}

	// Return a copy to prevent external modification
	result := make([]*FormatterDefinition, len(defs))
	copy(result, defs)
	return result
}

// FormatterResult represents the outcome of running a formatter on a file.
type FormatterResult struct {
	// FormatterID identifies which formatter was used
	FormatterID FormatterID

	// FilePath is the absolute path to the formatted file
	FilePath string

	// Success indicates whether the formatter completed without error
	Success bool

	// Changed indicates whether the file content was modified
	Changed bool

	// Error contains any error that occurred during formatting
	Error error

	// Duration is how long the formatter took to run
	Duration time.Duration

	// Stdout contains the formatter's standard output
	Stdout string

	// Stderr contains the formatter's standard error output
	Stderr string
}

// FormatterConfig allows user overrides for a specific formatter.
type FormatterConfig struct {
	// Disabled prevents this formatter from being used
	Disabled bool

	// Command overrides the default executable
	Command string

	// Args overrides the default command-line arguments
	Args []string

	// Extensions overrides or extends the file extensions
	Extensions []string
}

// DetectionResult represents a formatter detection match.
type DetectionResult struct {
	// FormatterID is the detected formatter
	FormatterID FormatterID

	// Confidence indicates how certain we are about this detection (0.0 to 1.0)
	Confidence float64

	// Reason explains why this formatter was detected
	Reason string
}

// DuplicateFormatterError is returned when attempting to register
// a formatter with an ID that already exists.
type DuplicateFormatterError struct {
	ID FormatterID
}

func (e *DuplicateFormatterError) Error() string {
	return "formatter already registered: " + string(e.ID)
}
