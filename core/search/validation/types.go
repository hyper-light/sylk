// Package validation provides cross-source validation for the Sylk Document Search System.
// It compares filesystem, CMT (Cartesian Merkle Tree), and git sources to detect
// discrepancies and ensure data consistency across the document index.
package validation

import (
	"errors"
	"time"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrInvalidSource indicates an invalid validation source.
	ErrInvalidSource = errors.New("invalid validation source")

	// ErrPathEmpty indicates an empty path.
	ErrPathEmpty = errors.New("path cannot be empty")

	// ErrNoSources indicates no sources were provided for validation.
	ErrNoSources = errors.New("no sources provided for validation")

	// ErrDiscrepancyNoStates indicates a discrepancy without states.
	ErrDiscrepancyNoStates = errors.New("discrepancy must have at least one state")

	// ErrFileStateInvalid indicates an invalid file state.
	ErrFileStateInvalid = errors.New("file state is invalid")
)

// =============================================================================
// ValidationSource
// =============================================================================

// ValidationSource represents a source of truth for file state.
type ValidationSource int

const (
	// SourceUnknown represents an unknown or unset source.
	SourceUnknown ValidationSource = iota

	// SourceFilesystem represents the actual filesystem.
	SourceFilesystem

	// SourceCMT represents the Cartesian Merkle Tree manifest.
	SourceCMT

	// SourceGit represents the git repository.
	SourceGit
)

// String returns the string representation of the validation source.
func (s ValidationSource) String() string {
	switch s {
	case SourceFilesystem:
		return "FILESYSTEM"
	case SourceCMT:
		return "CMT"
	case SourceGit:
		return "GIT"
	default:
		return "UNKNOWN"
	}
}

// IsValid returns true if the source is a known valid source.
func (s ValidationSource) IsValid() bool {
	return s >= SourceFilesystem && s <= SourceGit
}

// ParseValidationSource parses a string into a ValidationSource.
func ParseValidationSource(s string) ValidationSource {
	switch s {
	case "FILESYSTEM":
		return SourceFilesystem
	case "CMT":
		return SourceCMT
	case "GIT":
		return SourceGit
	default:
		return SourceUnknown
	}
}

// =============================================================================
// DiscrepancyType
// =============================================================================

// DiscrepancyType represents the type of discrepancy detected.
type DiscrepancyType int

const (
	// DiscrepancyUnknown represents an unknown discrepancy type.
	DiscrepancyUnknown DiscrepancyType = iota

	// DiscrepancyMissing indicates a file exists in some sources but not others.
	DiscrepancyMissing

	// DiscrepancyContentMismatch indicates content hashes differ between sources.
	DiscrepancyContentMismatch

	// DiscrepancySizeMismatch indicates file sizes differ between sources.
	DiscrepancySizeMismatch

	// DiscrepancyModTimeMismatch indicates modification times differ between sources.
	DiscrepancyModTimeMismatch
)

// String returns the string representation of the discrepancy type.
func (d DiscrepancyType) String() string {
	switch d {
	case DiscrepancyMissing:
		return "MISSING"
	case DiscrepancyContentMismatch:
		return "CONTENT_MISMATCH"
	case DiscrepancySizeMismatch:
		return "SIZE_MISMATCH"
	case DiscrepancyModTimeMismatch:
		return "MODTIME_MISMATCH"
	default:
		return "UNKNOWN"
	}
}

// =============================================================================
// FileState
// =============================================================================

// FileState represents the state of a file from a specific source.
type FileState struct {
	// Path is the file path.
	Path string

	// Source indicates which source this state came from.
	Source ValidationSource

	// Exists indicates whether the file exists in this source.
	Exists bool

	// ContentHash is the SHA-256 hash of file content (hex-encoded).
	ContentHash string

	// ModTime is the last modification time.
	ModTime time.Time

	// Size is the file size in bytes.
	Size int64
}

// IsZero returns true if the FileState is the zero value.
func (f *FileState) IsZero() bool {
	return f.Path == "" && f.Source == SourceUnknown && !f.Exists
}

// Validate validates the FileState.
func (f *FileState) Validate() error {
	if f.Path == "" {
		return ErrPathEmpty
	}
	if !f.Source.IsValid() {
		return ErrInvalidSource
	}
	return nil
}

// Matches returns true if two FileStates represent the same file content.
// It compares existence, content hash, and size.
func (f *FileState) Matches(other *FileState) bool {
	if f.Exists != other.Exists {
		return false
	}
	if !f.Exists {
		return true // Both don't exist, so they match
	}
	return f.ContentHash == other.ContentHash && f.Size == other.Size
}

// Clone creates a deep copy of the FileState.
func (f *FileState) Clone() *FileState {
	if f == nil {
		return nil
	}
	return &FileState{
		Path:        f.Path,
		Source:      f.Source,
		Exists:      f.Exists,
		ContentHash: f.ContentHash,
		ModTime:     f.ModTime,
		Size:        f.Size,
	}
}

// =============================================================================
// Discrepancy
// =============================================================================

// Discrepancy represents a detected inconsistency between sources.
type Discrepancy struct {
	// Path is the file path with the discrepancy.
	Path string

	// Sources lists which sources were compared.
	Sources []ValidationSource

	// States contains the file state from each source.
	States []*FileState

	// Type indicates the nature of the discrepancy.
	Type DiscrepancyType
}

// Validate validates the Discrepancy.
func (d *Discrepancy) Validate() error {
	if d.Path == "" {
		return ErrPathEmpty
	}
	if len(d.States) == 0 {
		return ErrDiscrepancyNoStates
	}
	return nil
}

// HasSource returns true if the discrepancy involves the given source.
func (d *Discrepancy) HasSource(source ValidationSource) bool {
	for _, s := range d.Sources {
		if s == source {
			return true
		}
	}
	return false
}

// GetState returns the FileState for the given source, or nil if not present.
func (d *Discrepancy) GetState(source ValidationSource) *FileState {
	for _, state := range d.States {
		if state.Source == source {
			return state
		}
	}
	return nil
}

// Clone creates a deep copy of the Discrepancy.
func (d *Discrepancy) Clone() *Discrepancy {
	if d == nil {
		return nil
	}

	sources := make([]ValidationSource, len(d.Sources))
	copy(sources, d.Sources)

	states := make([]*FileState, len(d.States))
	for i, state := range d.States {
		states[i] = state.Clone()
	}

	return &Discrepancy{
		Path:    d.Path,
		Sources: sources,
		States:  states,
		Type:    d.Type,
	}
}

// =============================================================================
// ValidationReport
// =============================================================================

// ValidationReport contains the results of a cross-source validation.
type ValidationReport struct {
	// CheckedAt is when the validation was performed.
	CheckedAt time.Time

	// TotalFiles is the number of files checked.
	TotalFiles int

	// Discrepancies contains all detected discrepancies.
	Discrepancies []*Discrepancy

	// Duration is how long the validation took.
	Duration time.Duration

	// SourcesChecked lists which sources were compared.
	SourcesChecked []ValidationSource

	// PathsChecked lists the paths that were validated (if subset was specified).
	PathsChecked []string
}

// HasDiscrepancies returns true if any discrepancies were detected.
func (r *ValidationReport) HasDiscrepancies() bool {
	return len(r.Discrepancies) > 0
}

// DiscrepancyCount returns the number of discrepancies.
func (r *ValidationReport) DiscrepancyCount() int {
	return len(r.Discrepancies)
}

// GetDiscrepanciesByType returns discrepancies of the specified type.
func (r *ValidationReport) GetDiscrepanciesByType(t DiscrepancyType) []*Discrepancy {
	var result []*Discrepancy
	for _, d := range r.Discrepancies {
		if d.Type == t {
			result = append(result, d)
		}
	}
	return result
}

// GetDiscrepanciesBySource returns discrepancies involving the specified source.
func (r *ValidationReport) GetDiscrepanciesBySource(source ValidationSource) []*Discrepancy {
	var result []*Discrepancy
	for _, d := range r.Discrepancies {
		if d.HasSource(source) {
			result = append(result, d)
		}
	}
	return result
}

// Clone creates a deep copy of the ValidationReport.
func (r *ValidationReport) Clone() *ValidationReport {
	if r == nil {
		return nil
	}

	sourcesChecked := make([]ValidationSource, len(r.SourcesChecked))
	copy(sourcesChecked, r.SourcesChecked)

	pathsChecked := make([]string, len(r.PathsChecked))
	copy(pathsChecked, r.PathsChecked)

	discrepancies := make([]*Discrepancy, len(r.Discrepancies))
	for i, d := range r.Discrepancies {
		discrepancies[i] = d.Clone()
	}

	return &ValidationReport{
		CheckedAt:      r.CheckedAt,
		TotalFiles:     r.TotalFiles,
		Discrepancies:  discrepancies,
		Duration:       r.Duration,
		SourcesChecked: sourcesChecked,
		PathsChecked:   pathsChecked,
	}
}

// =============================================================================
// ValidationConfig
// =============================================================================

// ValidationConfig configures the cross-source validation.
type ValidationConfig struct {
	// Paths specifies a subset of paths to validate. Empty means all paths.
	Paths []string

	// IncludeMissing controls whether to report files missing from some sources.
	IncludeMissing bool

	// IncludeContentMismatch controls whether to report content hash mismatches.
	IncludeContentMismatch bool

	// IncludeSizeMismatch controls whether to report size mismatches.
	IncludeSizeMismatch bool

	// IncludeModTimeMismatch controls whether to report modification time mismatches.
	IncludeModTimeMismatch bool

	// MaxConcurrent limits parallel validation operations.
	MaxConcurrent int
}

// DefaultValidationConfig returns a ValidationConfig with sensible defaults.
func DefaultValidationConfig() ValidationConfig {
	return ValidationConfig{
		Paths:                  nil,
		IncludeMissing:         true,
		IncludeContentMismatch: true,
		IncludeSizeMismatch:    true,
		IncludeModTimeMismatch: false, // Often legitimately different
		MaxConcurrent:          4,
	}
}

// HasPathSubset returns true if a path subset is configured.
func (c *ValidationConfig) HasPathSubset() bool {
	return len(c.Paths) > 0
}

// ShouldReportType returns true if the given discrepancy type should be reported.
func (c *ValidationConfig) ShouldReportType(t DiscrepancyType) bool {
	switch t {
	case DiscrepancyMissing:
		return c.IncludeMissing
	case DiscrepancyContentMismatch:
		return c.IncludeContentMismatch
	case DiscrepancySizeMismatch:
		return c.IncludeSizeMismatch
	case DiscrepancyModTimeMismatch:
		return c.IncludeModTimeMismatch
	default:
		return false
	}
}
