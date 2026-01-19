// Package validation provides cross-source validation for the Sylk Document Search System.
package validation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// ValidationSource Tests
// =============================================================================

func TestValidationSource_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   ValidationSource
		expected string
	}{
		{
			name:     "unknown returns UNKNOWN",
			source:   SourceUnknown,
			expected: "UNKNOWN",
		},
		{
			name:     "filesystem returns FILESYSTEM",
			source:   SourceFilesystem,
			expected: "FILESYSTEM",
		},
		{
			name:     "CMT returns CMT",
			source:   SourceCMT,
			expected: "CMT",
		},
		{
			name:     "git returns GIT",
			source:   SourceGit,
			expected: "GIT",
		},
		{
			name:     "invalid value returns UNKNOWN",
			source:   ValidationSource(99),
			expected: "UNKNOWN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.source.String()
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestValidationSource_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   ValidationSource
		expected bool
	}{
		{
			name:     "unknown is invalid",
			source:   SourceUnknown,
			expected: false,
		},
		{
			name:     "filesystem is valid",
			source:   SourceFilesystem,
			expected: true,
		},
		{
			name:     "CMT is valid",
			source:   SourceCMT,
			expected: true,
		},
		{
			name:     "git is valid",
			source:   SourceGit,
			expected: true,
		},
		{
			name:     "negative value is invalid",
			source:   ValidationSource(-1),
			expected: false,
		},
		{
			name:     "large value is invalid",
			source:   ValidationSource(100),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.source.IsValid()
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestParseValidationSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected ValidationSource
	}{
		{
			name:     "FILESYSTEM parses correctly",
			input:    "FILESYSTEM",
			expected: SourceFilesystem,
		},
		{
			name:     "CMT parses correctly",
			input:    "CMT",
			expected: SourceCMT,
		},
		{
			name:     "GIT parses correctly",
			input:    "GIT",
			expected: SourceGit,
		},
		{
			name:     "lowercase returns unknown",
			input:    "filesystem",
			expected: SourceUnknown,
		},
		{
			name:     "empty string returns unknown",
			input:    "",
			expected: SourceUnknown,
		},
		{
			name:     "invalid string returns unknown",
			input:    "INVALID",
			expected: SourceUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseValidationSource(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// =============================================================================
// DiscrepancyType Tests
// =============================================================================

func TestDiscrepancyType_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dt       DiscrepancyType
		expected string
	}{
		{
			name:     "unknown returns UNKNOWN",
			dt:       DiscrepancyUnknown,
			expected: "UNKNOWN",
		},
		{
			name:     "missing returns MISSING",
			dt:       DiscrepancyMissing,
			expected: "MISSING",
		},
		{
			name:     "content mismatch returns CONTENT_MISMATCH",
			dt:       DiscrepancyContentMismatch,
			expected: "CONTENT_MISMATCH",
		},
		{
			name:     "size mismatch returns SIZE_MISMATCH",
			dt:       DiscrepancySizeMismatch,
			expected: "SIZE_MISMATCH",
		},
		{
			name:     "modtime mismatch returns MODTIME_MISMATCH",
			dt:       DiscrepancyModTimeMismatch,
			expected: "MODTIME_MISMATCH",
		},
		{
			name:     "invalid value returns UNKNOWN",
			dt:       DiscrepancyType(99),
			expected: "UNKNOWN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.dt.String()
			assert.Equal(t, tt.expected, got)
		})
	}
}

// =============================================================================
// FileState Tests
// =============================================================================

func TestFileState_IsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		state    FileState
		expected bool
	}{
		{
			name:     "zero value returns true",
			state:    FileState{},
			expected: true,
		},
		{
			name: "non-zero path returns false",
			state: FileState{
				Path: "file.txt",
			},
			expected: false,
		},
		{
			name: "non-zero source returns false",
			state: FileState{
				Source: SourceFilesystem,
			},
			expected: false,
		},
		{
			name: "exists true returns false",
			state: FileState{
				Exists: true,
			},
			expected: false,
		},
		{
			name: "fully populated returns false",
			state: FileState{
				Path:        "file.txt",
				Source:      SourceFilesystem,
				Exists:      true,
				ContentHash: "abc123",
				Size:        100,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.state.IsZero()
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestFileState_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		state       FileState
		expectError bool
	}{
		{
			name: "valid state passes",
			state: FileState{
				Path:   "file.txt",
				Source: SourceFilesystem,
				Exists: true,
			},
			expectError: false,
		},
		{
			name: "empty path fails",
			state: FileState{
				Path:   "",
				Source: SourceFilesystem,
			},
			expectError: true,
		},
		{
			name: "invalid source fails",
			state: FileState{
				Path:   "file.txt",
				Source: SourceUnknown,
			},
			expectError: true,
		},
		{
			name: "CMT source is valid",
			state: FileState{
				Path:   "file.txt",
				Source: SourceCMT,
			},
			expectError: false,
		},
		{
			name: "git source is valid",
			state: FileState{
				Path:   "file.txt",
				Source: SourceGit,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.state.Validate()
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFileState_Matches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		state1   FileState
		state2   FileState
		expected bool
	}{
		{
			name: "both non-existent match",
			state1: FileState{
				Path:   "file.txt",
				Exists: false,
			},
			state2: FileState{
				Path:   "file.txt",
				Exists: false,
			},
			expected: true,
		},
		{
			name: "one exists one not does not match",
			state1: FileState{
				Path:   "file.txt",
				Exists: true,
			},
			state2: FileState{
				Path:   "file.txt",
				Exists: false,
			},
			expected: false,
		},
		{
			name: "same content hash and size match",
			state1: FileState{
				Path:        "file.txt",
				Exists:      true,
				ContentHash: "abc123",
				Size:        100,
			},
			state2: FileState{
				Path:        "file.txt",
				Exists:      true,
				ContentHash: "abc123",
				Size:        100,
			},
			expected: true,
		},
		{
			name: "different content hash does not match",
			state1: FileState{
				Path:        "file.txt",
				Exists:      true,
				ContentHash: "abc123",
				Size:        100,
			},
			state2: FileState{
				Path:        "file.txt",
				Exists:      true,
				ContentHash: "def456",
				Size:        100,
			},
			expected: false,
		},
		{
			name: "different size does not match",
			state1: FileState{
				Path:        "file.txt",
				Exists:      true,
				ContentHash: "abc123",
				Size:        100,
			},
			state2: FileState{
				Path:        "file.txt",
				Exists:      true,
				ContentHash: "abc123",
				Size:        200,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.state1.Matches(&tt.state2)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestFileState_Clone(t *testing.T) {
	t.Parallel()

	now := time.Now()
	original := &FileState{
		Path:        "file.txt",
		Source:      SourceFilesystem,
		Exists:      true,
		ContentHash: "abc123",
		ModTime:     now,
		Size:        100,
	}

	cloned := original.Clone()

	require.NotNil(t, cloned)
	assert.Equal(t, original.Path, cloned.Path)
	assert.Equal(t, original.Source, cloned.Source)
	assert.Equal(t, original.Exists, cloned.Exists)
	assert.Equal(t, original.ContentHash, cloned.ContentHash)
	assert.Equal(t, original.ModTime, cloned.ModTime)
	assert.Equal(t, original.Size, cloned.Size)

	// Verify independence
	cloned.Path = "modified.txt"
	assert.NotEqual(t, original.Path, cloned.Path)
}

func TestFileState_Clone_Nil(t *testing.T) {
	t.Parallel()

	var state *FileState
	cloned := state.Clone()
	assert.Nil(t, cloned)
}

// =============================================================================
// Discrepancy Tests
// =============================================================================

func TestDiscrepancy_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		discrepancy Discrepancy
		expectError bool
	}{
		{
			name: "valid discrepancy passes",
			discrepancy: Discrepancy{
				Path:    "file.txt",
				Sources: []ValidationSource{SourceFilesystem, SourceCMT},
				States: []*FileState{
					{Path: "file.txt", Source: SourceFilesystem},
				},
				Type: DiscrepancyMissing,
			},
			expectError: false,
		},
		{
			name: "empty path fails",
			discrepancy: Discrepancy{
				Path:   "",
				States: []*FileState{{Path: "file.txt"}},
			},
			expectError: true,
		},
		{
			name: "no states fails",
			discrepancy: Discrepancy{
				Path:   "file.txt",
				States: []*FileState{},
			},
			expectError: true,
		},
		{
			name: "nil states slice fails",
			discrepancy: Discrepancy{
				Path:   "file.txt",
				States: nil,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.discrepancy.Validate()
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDiscrepancy_HasSource(t *testing.T) {
	t.Parallel()

	d := Discrepancy{
		Path:    "file.txt",
		Sources: []ValidationSource{SourceFilesystem, SourceCMT},
	}

	assert.True(t, d.HasSource(SourceFilesystem))
	assert.True(t, d.HasSource(SourceCMT))
	assert.False(t, d.HasSource(SourceGit))
}

func TestDiscrepancy_GetState(t *testing.T) {
	t.Parallel()

	fsState := &FileState{Path: "file.txt", Source: SourceFilesystem}
	cmtState := &FileState{Path: "file.txt", Source: SourceCMT}

	d := Discrepancy{
		Path:   "file.txt",
		States: []*FileState{fsState, cmtState},
	}

	assert.Equal(t, fsState, d.GetState(SourceFilesystem))
	assert.Equal(t, cmtState, d.GetState(SourceCMT))
	assert.Nil(t, d.GetState(SourceGit))
}

func TestDiscrepancy_Clone(t *testing.T) {
	t.Parallel()

	original := &Discrepancy{
		Path:    "file.txt",
		Sources: []ValidationSource{SourceFilesystem, SourceCMT},
		States: []*FileState{
			{Path: "file.txt", Source: SourceFilesystem, Exists: true},
			{Path: "file.txt", Source: SourceCMT, Exists: false},
		},
		Type: DiscrepancyMissing,
	}

	cloned := original.Clone()

	require.NotNil(t, cloned)
	assert.Equal(t, original.Path, cloned.Path)
	assert.Equal(t, original.Type, cloned.Type)
	assert.Equal(t, len(original.Sources), len(cloned.Sources))
	assert.Equal(t, len(original.States), len(cloned.States))

	// Verify deep copy of sources
	cloned.Sources[0] = SourceGit
	assert.NotEqual(t, original.Sources[0], cloned.Sources[0])

	// Verify deep copy of states
	cloned.States[0].Path = "modified.txt"
	assert.NotEqual(t, original.States[0].Path, cloned.States[0].Path)
}

func TestDiscrepancy_Clone_Nil(t *testing.T) {
	t.Parallel()

	var d *Discrepancy
	cloned := d.Clone()
	assert.Nil(t, cloned)
}

// =============================================================================
// ValidationReport Tests
// =============================================================================

func TestValidationReport_HasDiscrepancies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		report   ValidationReport
		expected bool
	}{
		{
			name: "no discrepancies returns false",
			report: ValidationReport{
				Discrepancies: nil,
			},
			expected: false,
		},
		{
			name: "empty slice returns false",
			report: ValidationReport{
				Discrepancies: []*Discrepancy{},
			},
			expected: false,
		},
		{
			name: "with discrepancies returns true",
			report: ValidationReport{
				Discrepancies: []*Discrepancy{
					{Path: "file.txt"},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.report.HasDiscrepancies()
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestValidationReport_DiscrepancyCount(t *testing.T) {
	t.Parallel()

	report := ValidationReport{
		Discrepancies: []*Discrepancy{
			{Path: "file1.txt"},
			{Path: "file2.txt"},
			{Path: "file3.txt"},
		},
	}

	assert.Equal(t, 3, report.DiscrepancyCount())
}

func TestValidationReport_GetDiscrepanciesByType(t *testing.T) {
	t.Parallel()

	report := ValidationReport{
		Discrepancies: []*Discrepancy{
			{Path: "file1.txt", Type: DiscrepancyMissing},
			{Path: "file2.txt", Type: DiscrepancyContentMismatch},
			{Path: "file3.txt", Type: DiscrepancyMissing},
			{Path: "file4.txt", Type: DiscrepancySizeMismatch},
		},
	}

	missing := report.GetDiscrepanciesByType(DiscrepancyMissing)
	assert.Len(t, missing, 2)

	content := report.GetDiscrepanciesByType(DiscrepancyContentMismatch)
	assert.Len(t, content, 1)

	modtime := report.GetDiscrepanciesByType(DiscrepancyModTimeMismatch)
	assert.Len(t, modtime, 0)
}

func TestValidationReport_GetDiscrepanciesBySource(t *testing.T) {
	t.Parallel()

	report := ValidationReport{
		Discrepancies: []*Discrepancy{
			{Path: "file1.txt", Sources: []ValidationSource{SourceFilesystem, SourceCMT}},
			{Path: "file2.txt", Sources: []ValidationSource{SourceFilesystem, SourceGit}},
			{Path: "file3.txt", Sources: []ValidationSource{SourceCMT, SourceGit}},
		},
	}

	fsDisc := report.GetDiscrepanciesBySource(SourceFilesystem)
	assert.Len(t, fsDisc, 2)

	cmtDisc := report.GetDiscrepanciesBySource(SourceCMT)
	assert.Len(t, cmtDisc, 2)

	gitDisc := report.GetDiscrepanciesBySource(SourceGit)
	assert.Len(t, gitDisc, 2)
}

func TestValidationReport_Clone(t *testing.T) {
	t.Parallel()

	now := time.Now()
	original := &ValidationReport{
		CheckedAt:  now,
		TotalFiles: 100,
		Discrepancies: []*Discrepancy{
			{Path: "file.txt", Type: DiscrepancyMissing},
		},
		Duration:       5 * time.Second,
		SourcesChecked: []ValidationSource{SourceFilesystem, SourceCMT},
		PathsChecked:   []string{"path1", "path2"},
	}

	cloned := original.Clone()

	require.NotNil(t, cloned)
	assert.Equal(t, original.CheckedAt, cloned.CheckedAt)
	assert.Equal(t, original.TotalFiles, cloned.TotalFiles)
	assert.Equal(t, original.Duration, cloned.Duration)
	assert.Equal(t, len(original.Discrepancies), len(cloned.Discrepancies))
	assert.Equal(t, len(original.SourcesChecked), len(cloned.SourcesChecked))
	assert.Equal(t, len(original.PathsChecked), len(cloned.PathsChecked))

	// Verify deep copy
	cloned.Discrepancies[0].Path = "modified.txt"
	assert.NotEqual(t, original.Discrepancies[0].Path, cloned.Discrepancies[0].Path)
}

func TestValidationReport_Clone_Nil(t *testing.T) {
	t.Parallel()

	var report *ValidationReport
	cloned := report.Clone()
	assert.Nil(t, cloned)
}

// =============================================================================
// ValidationConfig Tests
// =============================================================================

func TestDefaultValidationConfig(t *testing.T) {
	t.Parallel()

	config := DefaultValidationConfig()

	assert.Nil(t, config.Paths)
	assert.True(t, config.IncludeMissing)
	assert.True(t, config.IncludeContentMismatch)
	assert.True(t, config.IncludeSizeMismatch)
	assert.False(t, config.IncludeModTimeMismatch)
	assert.Equal(t, 4, config.MaxConcurrent)
}

func TestValidationConfig_HasPathSubset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   ValidationConfig
		expected bool
	}{
		{
			name:     "nil paths returns false",
			config:   ValidationConfig{Paths: nil},
			expected: false,
		},
		{
			name:     "empty paths returns false",
			config:   ValidationConfig{Paths: []string{}},
			expected: false,
		},
		{
			name:     "with paths returns true",
			config:   ValidationConfig{Paths: []string{"file.txt"}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.config.HasPathSubset()
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestValidationConfig_ShouldReportType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   ValidationConfig
		dt       DiscrepancyType
		expected bool
	}{
		{
			name:     "missing enabled reports missing",
			config:   ValidationConfig{IncludeMissing: true},
			dt:       DiscrepancyMissing,
			expected: true,
		},
		{
			name:     "missing disabled does not report missing",
			config:   ValidationConfig{IncludeMissing: false},
			dt:       DiscrepancyMissing,
			expected: false,
		},
		{
			name:     "content mismatch enabled reports content mismatch",
			config:   ValidationConfig{IncludeContentMismatch: true},
			dt:       DiscrepancyContentMismatch,
			expected: true,
		},
		{
			name:     "size mismatch enabled reports size mismatch",
			config:   ValidationConfig{IncludeSizeMismatch: true},
			dt:       DiscrepancySizeMismatch,
			expected: true,
		},
		{
			name:     "modtime mismatch enabled reports modtime mismatch",
			config:   ValidationConfig{IncludeModTimeMismatch: true},
			dt:       DiscrepancyModTimeMismatch,
			expected: true,
		},
		{
			name:     "unknown type is not reported",
			config:   ValidationConfig{IncludeMissing: true},
			dt:       DiscrepancyUnknown,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.config.ShouldReportType(tt.dt)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// =============================================================================
// Error Tests
// =============================================================================

func TestErrors_AreDistinct(t *testing.T) {
	t.Parallel()

	errors := []error{
		ErrInvalidSource,
		ErrPathEmpty,
		ErrNoSources,
		ErrDiscrepancyNoStates,
		ErrFileStateInvalid,
	}

	seen := make(map[string]bool)
	for _, err := range errors {
		msg := err.Error()
		assert.False(t, seen[msg], "duplicate error message: %s", msg)
		seen[msg] = true
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestFileState_MatchesWithEmptyHashes(t *testing.T) {
	t.Parallel()

	state1 := FileState{
		Path:        "file.txt",
		Exists:      true,
		ContentHash: "",
		Size:        100,
	}
	state2 := FileState{
		Path:        "file.txt",
		Exists:      true,
		ContentHash: "",
		Size:        100,
	}

	// Empty hashes should still match if both are empty
	assert.True(t, state1.Matches(&state2))
}

func TestDiscrepancy_EmptySources(t *testing.T) {
	t.Parallel()

	d := Discrepancy{
		Path:    "file.txt",
		Sources: []ValidationSource{},
	}

	assert.False(t, d.HasSource(SourceFilesystem))
}

func TestValidationReport_EmptyReport(t *testing.T) {
	t.Parallel()

	report := ValidationReport{}

	assert.False(t, report.HasDiscrepancies())
	assert.Equal(t, 0, report.DiscrepancyCount())
	assert.Len(t, report.GetDiscrepanciesByType(DiscrepancyMissing), 0)
	assert.Len(t, report.GetDiscrepanciesBySource(SourceFilesystem), 0)
}

func TestValidationSource_Constants(t *testing.T) {
	t.Parallel()

	// Verify the enum values are as expected
	assert.Equal(t, ValidationSource(0), SourceUnknown)
	assert.Equal(t, ValidationSource(1), SourceFilesystem)
	assert.Equal(t, ValidationSource(2), SourceCMT)
	assert.Equal(t, ValidationSource(3), SourceGit)
}

func TestDiscrepancyType_Constants(t *testing.T) {
	t.Parallel()

	// Verify the enum values are as expected
	assert.Equal(t, DiscrepancyType(0), DiscrepancyUnknown)
	assert.Equal(t, DiscrepancyType(1), DiscrepancyMissing)
	assert.Equal(t, DiscrepancyType(2), DiscrepancyContentMismatch)
	assert.Equal(t, DiscrepancyType(3), DiscrepancySizeMismatch)
	assert.Equal(t, DiscrepancyType(4), DiscrepancyModTimeMismatch)
}
