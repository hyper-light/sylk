package vectorgraphdb

import (
	"fmt"
	"time"
)

// =============================================================================
// Integrity Configuration
// =============================================================================

// IntegrityConfig controls how integrity validation is performed.
// It balances thoroughness against performance overhead.
type IntegrityConfig struct {
	// ValidateOnRead checks hash on every read operation.
	// Disabled by default as it's too expensive for every read.
	ValidateOnRead bool

	// PeriodicInterval is the interval for background integrity scans.
	PeriodicInterval time.Duration

	// AutoRepair enables automatic fixing of minor issues.
	AutoRepair bool

	// SampleSize is the number of nodes to check per scan.
	SampleSize int
}

// DefaultIntegrityConfig returns the default integrity configuration.
func DefaultIntegrityConfig() IntegrityConfig {
	return IntegrityConfig{
		ValidateOnRead:   false,      // too expensive for every read
		PeriodicInterval: time.Hour,  // background scan interval
		AutoRepair:       true,       // fix minor issues automatically
		SampleSize:       100,        // nodes to check per scan
	}
}

// =============================================================================
// Severity Levels
// =============================================================================

// Severity represents the severity level of an integrity violation.
type Severity int

const (
	// SeverityWarning indicates a minor issue that is logged and continued.
	SeverityWarning Severity = iota

	// SeverityError indicates an issue that should be logged and repaired.
	SeverityError

	// SeverityCritical indicates a severe issue that should halt operation.
	SeverityCritical
)

// String returns the string representation of the severity.
func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	case SeverityCritical:
		return "critical"
	default:
		return fmt.Sprintf("severity(%d)", s)
	}
}

// ParseSeverity parses a string into a Severity value.
func ParseSeverity(value string) (Severity, bool) {
	switch value {
	case "warning":
		return SeverityWarning, true
	case "error":
		return SeverityError, true
	case "critical":
		return SeverityCritical, true
	default:
		return Severity(0), false
	}
}

// IsValid returns true if the severity is a recognized value.
func (s Severity) IsValid() bool {
	switch s {
	case SeverityWarning, SeverityError, SeverityCritical:
		return true
	default:
		return false
	}
}

// =============================================================================
// Invariant Check Definition
// =============================================================================

// InvariantCheck defines a consistency rule for integrity validation.
// Each check has a query to detect violations and an optional repair query.
type InvariantCheck struct {
	// Name is the unique identifier for this check.
	Name string

	// Description explains what this check validates.
	Description string

	// Query is the SQL that returns rows that violate this invariant.
	Query string

	// Repair is the SQL to fix violations (optional, empty if not repairable).
	Repair string

	// Severity indicates how serious violations of this check are.
	Severity Severity
}

// =============================================================================
// Violation Result
// =============================================================================

// Violation represents a detected integrity issue.
type Violation struct {
	// Check is the name of the invariant check that was violated.
	Check string

	// EntityID is the ID of the entity that violated the invariant.
	EntityID string

	// Description provides details about the specific violation.
	Description string

	// Severity indicates how serious this violation is.
	Severity Severity

	// Repaired indicates whether this violation was automatically repaired.
	Repaired bool
}

// =============================================================================
// Validation Result
// =============================================================================

// ValidationResult contains the results of an integrity validation run.
type ValidationResult struct {
	// Violations is the list of detected integrity issues.
	Violations []Violation

	// ChecksRun is the number of invariant checks that were executed.
	ChecksRun int

	// TotalChecked is the number of entities that were checked.
	TotalChecked int

	// RepairedCount is the number of violations that were auto-repaired.
	RepairedCount int

	// Duration is how long the validation took.
	Duration time.Duration

	// Timestamp is when the validation was performed.
	Timestamp time.Time
}

// HasCritical returns true if any critical violations were detected.
func (vr *ValidationResult) HasCritical() bool {
	for _, v := range vr.Violations {
		if v.Severity == SeverityCritical {
			return true
		}
	}
	return false
}

// HasErrors returns true if any error-level violations were detected.
func (vr *ValidationResult) HasErrors() bool {
	for _, v := range vr.Violations {
		if v.Severity == SeverityError {
			return true
		}
	}
	return false
}

// UnrepairedCount returns the number of violations that were not repaired.
func (vr *ValidationResult) UnrepairedCount() int {
	count := 0
	for _, v := range vr.Violations {
		if !v.Repaired {
			count++
		}
	}
	return count
}

// =============================================================================
// Standard Invariant Checks
// =============================================================================

// StandardChecks returns the standard set of invariant checks for VectorGraphDB.
// These checks validate referential integrity and data consistency.
func StandardChecks() []InvariantCheck {
	return []InvariantCheck{
		{
			Name:        "orphaned_vectors",
			Description: "Vectors without corresponding nodes",
			Query:       "SELECT node_id FROM vectors WHERE node_id NOT IN (SELECT id FROM nodes)",
			Repair:      "DELETE FROM vectors WHERE node_id NOT IN (SELECT id FROM nodes)",
			Severity:    SeverityError,
		},
		{
			Name:        "orphaned_edges_source",
			Description: "Edges with non-existent source nodes",
			Query:       "SELECT id FROM edges WHERE source_id NOT IN (SELECT id FROM nodes)",
			Repair:      "DELETE FROM edges WHERE source_id NOT IN (SELECT id FROM nodes)",
			Severity:    SeverityError,
		},
		{
			Name:        "orphaned_edges_target",
			Description: "Edges with non-existent target nodes",
			Query:       "SELECT id FROM edges WHERE target_id NOT IN (SELECT id FROM nodes)",
			Repair:      "DELETE FROM edges WHERE target_id NOT IN (SELECT id FROM nodes)",
			Severity:    SeverityError,
		},
		{
			Name:        "invalid_hnsw_entry",
			Description: "HNSW index entries pointing to non-existent nodes",
			Query:       "SELECT node_id FROM hnsw_nodes WHERE node_id NOT IN (SELECT id FROM nodes)",
			Repair:      "DELETE FROM hnsw_nodes WHERE node_id NOT IN (SELECT id FROM nodes)",
			Severity:    SeverityCritical,
		},
		{
			Name:        "dimension_mismatch",
			Description: "Vectors with incorrect embedding dimensions",
			Query:       fmt.Sprintf("SELECT node_id FROM vectors WHERE dimensions != %d", EmbeddingDimension),
			Repair:      "", // Cannot auto-repair dimension mismatches
			Severity:    SeverityCritical,
		},
		{
			Name:        "superseded_cycle",
			Description: "Nodes involved in supersession cycles",
			Query: `WITH RECURSIVE supersession_chain(id, path, has_cycle) AS (
				SELECT id, ARRAY[id], false FROM nodes WHERE superseded_by IS NOT NULL
				UNION ALL
				SELECT n.superseded_by, sc.path || n.superseded_by,
					n.superseded_by = ANY(sc.path)
				FROM nodes n
				JOIN supersession_chain sc ON n.id = sc.id
				WHERE n.superseded_by IS NOT NULL AND NOT sc.has_cycle
			)
			SELECT DISTINCT id FROM supersession_chain WHERE has_cycle = true`,
			Repair:   "", // Cannot auto-repair cycles - requires manual resolution
			Severity: SeverityError,
		},
		{
			Name:        "orphaned_provenance",
			Description: "Provenance records without corresponding nodes",
			Query:       "SELECT id FROM provenance WHERE node_id NOT IN (SELECT id FROM nodes)",
			Repair:      "DELETE FROM provenance WHERE node_id NOT IN (SELECT id FROM nodes)",
			Severity:    SeverityWarning,
		},
	}
}

// CheckNames returns the names of all standard checks.
func CheckNames() []string {
	checks := StandardChecks()
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = c.Name
	}
	return names
}

// GetCheckByName returns the invariant check with the given name.
func GetCheckByName(name string) (InvariantCheck, bool) {
	for _, c := range StandardChecks() {
		if c.Name == name {
			return c, true
		}
	}
	return InvariantCheck{}, false
}
