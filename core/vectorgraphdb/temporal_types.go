package vectorgraphdb

import (
	"encoding/json"
	"fmt"
	"time"
)

// =============================================================================
// Temporal Edge Types (KG.2.1)
// =============================================================================

// TemporalEdge extends GraphEdge with bitemporal tracking capabilities.
// ValidFrom/ValidTo represent the "valid time" - when the relationship was true
// in the real world. TxStart/TxEnd represent the "transaction time" - when the
// relationship was recorded/superseded in the database.
type TemporalEdge struct {
	GraphEdge

	// ValidFrom is when this edge relationship became valid in the real world.
	// A nil value indicates the edge was always valid (no known start).
	ValidFrom *time.Time `json:"valid_from,omitempty"`

	// ValidTo is when this edge relationship ceased to be valid.
	// A nil value indicates the edge is currently valid (no end).
	ValidTo *time.Time `json:"valid_to,omitempty"`

	// TxStart is when this edge was recorded in the database.
	TxStart *time.Time `json:"tx_start,omitempty"`

	// TxEnd is when this edge was superseded in the database.
	// A nil value indicates this is the current version.
	TxEnd *time.Time `json:"tx_end,omitempty"`
}

// NewTemporalEdge creates a new TemporalEdge from an existing GraphEdge.
// The valid time starts from now and transaction time is recorded as now.
func NewTemporalEdge(edge GraphEdge) *TemporalEdge {
	now := time.Now()
	return &TemporalEdge{
		GraphEdge: edge,
		ValidFrom: &now,
		ValidTo:   nil,
		TxStart:   &now,
		TxEnd:     nil,
	}
}

// NewTemporalEdgeWithValidity creates a new TemporalEdge with explicit valid time range.
func NewTemporalEdgeWithValidity(edge GraphEdge, validFrom, validTo *time.Time) *TemporalEdge {
	now := time.Now()
	return &TemporalEdge{
		GraphEdge: edge,
		ValidFrom: validFrom,
		ValidTo:   validTo,
		TxStart:   &now,
		TxEnd:     nil,
	}
}

// IsValidAt returns true if the edge was valid at the given time.
// An edge is valid at time t if ValidFrom <= t < ValidTo (or ValidTo is nil).
func (te *TemporalEdge) IsValidAt(t time.Time) bool {
	// Check ValidFrom: if set, t must be >= ValidFrom
	if te.ValidFrom != nil && t.Before(*te.ValidFrom) {
		return false
	}

	// Check ValidTo: if set, t must be < ValidTo
	if te.ValidTo != nil && !t.Before(*te.ValidTo) {
		return false
	}

	return true
}

// IsCurrent returns true if this edge is currently valid (ValidTo is nil).
func (te *TemporalEdge) IsCurrent() bool {
	return te.ValidTo == nil
}

// IsCurrentTransaction returns true if this is the current transaction version (TxEnd is nil).
func (te *TemporalEdge) IsCurrentTransaction() bool {
	return te.TxEnd == nil
}

// Invalidate marks this edge as no longer valid as of the given time.
// This sets ValidTo to the provided time if it is currently nil.
func (te *TemporalEdge) Invalidate(endTime time.Time) {
	if te.ValidTo == nil {
		te.ValidTo = &endTime
	}
}

// Supersede marks this transaction version as superseded.
// This sets TxEnd to the provided time if it is currently nil.
func (te *TemporalEdge) Supersede(endTime time.Time) {
	if te.TxEnd == nil {
		te.TxEnd = &endTime
	}
}

// ValidDuration returns the duration this edge was/is valid.
// Returns 0 if ValidFrom is nil (unknown start).
// If ValidTo is nil, calculates duration until now.
func (te *TemporalEdge) ValidDuration() time.Duration {
	if te.ValidFrom == nil {
		return 0
	}
	endTime := time.Now()
	if te.ValidTo != nil {
		endTime = *te.ValidTo
	}
	return endTime.Sub(*te.ValidFrom)
}

// =============================================================================
// Time Range Types
// =============================================================================

// TimeRange represents a time interval for query predicates.
// Used to specify temporal query bounds.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// NewTimeRange creates a new TimeRange with the specified start and end times.
func NewTimeRange(start, end time.Time) TimeRange {
	return TimeRange{Start: start, End: end}
}

// Contains returns true if the given time falls within this range.
// A time t is contained if Start <= t < End.
func (tr TimeRange) Contains(t time.Time) bool {
	return !t.Before(tr.Start) && t.Before(tr.End)
}

// ContainsInclusive returns true if the given time falls within this range (inclusive).
// A time t is contained if Start <= t <= End.
func (tr TimeRange) ContainsInclusive(t time.Time) bool {
	return !t.Before(tr.Start) && !t.After(tr.End)
}

// OverlapsWith returns true if this range overlaps with another range.
// Two ranges overlap if they share any common time point.
func (tr TimeRange) OverlapsWith(other TimeRange) bool {
	// Ranges overlap if: tr.Start < other.End AND other.Start < tr.End
	return tr.Start.Before(other.End) && other.Start.Before(tr.End)
}

// Duration returns the duration of this time range.
func (tr TimeRange) Duration() time.Duration {
	return tr.End.Sub(tr.Start)
}

// IsZero returns true if this is a zero-value TimeRange.
func (tr TimeRange) IsZero() bool {
	return tr.Start.IsZero() && tr.End.IsZero()
}

// Intersection returns the overlapping portion of two time ranges.
// Returns a zero TimeRange if there is no overlap.
func (tr TimeRange) Intersection(other TimeRange) TimeRange {
	if !tr.OverlapsWith(other) {
		return TimeRange{}
	}

	start := tr.Start
	if other.Start.After(start) {
		start = other.Start
	}

	end := tr.End
	if other.End.Before(end) {
		end = other.End
	}

	return TimeRange{Start: start, End: end}
}

// Union returns the smallest time range that contains both ranges.
func (tr TimeRange) Union(other TimeRange) TimeRange {
	start := tr.Start
	if other.Start.Before(start) {
		start = other.Start
	}

	end := tr.End
	if other.End.After(end) {
		end = other.End
	}

	return TimeRange{Start: start, End: end}
}

// =============================================================================
// Temporal Query Options
// =============================================================================

// TemporalQueryMode specifies how temporal queries should be evaluated.
type TemporalQueryMode int

const (
	// QueryModeCurrent returns only edges that are currently valid (ValidTo is nil).
	QueryModeCurrent TemporalQueryMode = 0

	// QueryModeAsOf returns edges that were valid at a specific point in time.
	QueryModeAsOf TemporalQueryMode = 1

	// QueryModeBetween returns edges that were valid during a time range.
	QueryModeBetween TemporalQueryMode = 2

	// QueryModeValidAt is an alias for QueryModeAsOf for semantic clarity.
	QueryModeValidAt TemporalQueryMode = 3

	// QueryModeAll returns all edges regardless of temporal validity.
	QueryModeAll TemporalQueryMode = 4
)

// String returns the string representation of the query mode.
func (m TemporalQueryMode) String() string {
	switch m {
	case QueryModeCurrent:
		return "current"
	case QueryModeAsOf:
		return "as_of"
	case QueryModeBetween:
		return "between"
	case QueryModeValidAt:
		return "valid_at"
	case QueryModeAll:
		return "all"
	default:
		return fmt.Sprintf("query_mode(%d)", m)
	}
}

// ParseTemporalQueryMode parses a string into a TemporalQueryMode.
func ParseTemporalQueryMode(value string) (TemporalQueryMode, bool) {
	switch value {
	case "current":
		return QueryModeCurrent, true
	case "as_of":
		return QueryModeAsOf, true
	case "between":
		return QueryModeBetween, true
	case "valid_at":
		return QueryModeValidAt, true
	case "all":
		return QueryModeAll, true
	default:
		return TemporalQueryMode(0), false
	}
}

// MarshalJSON implements json.Marshaler for TemporalQueryMode.
func (m TemporalQueryMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

// UnmarshalJSON implements json.Unmarshaler for TemporalQueryMode.
func (m *TemporalQueryMode) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseTemporalQueryMode(asString); ok {
			*m = parsed
			return nil
		}
		return fmt.Errorf("invalid temporal query mode: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*m = TemporalQueryMode(asInt)
		return nil
	}

	return fmt.Errorf("invalid temporal query mode")
}

// TemporalQueryOptions configures how temporal queries are executed.
type TemporalQueryOptions struct {
	// Mode specifies the temporal query mode.
	Mode TemporalQueryMode `json:"mode"`

	// AsOf specifies the point in time for QueryModeAsOf/QueryModeValidAt.
	AsOf *time.Time `json:"as_of,omitempty"`

	// Between specifies the time range for QueryModeBetween.
	Between *TimeRange `json:"between,omitempty"`

	// IncludeSuperseded includes transaction-superseded versions when true.
	IncludeSuperseded bool `json:"include_superseded,omitempty"`
}

// NewCurrentQueryOptions creates options for querying currently valid edges.
func NewCurrentQueryOptions() TemporalQueryOptions {
	return TemporalQueryOptions{
		Mode: QueryModeCurrent,
	}
}

// NewAsOfQueryOptions creates options for querying edges valid at a specific time.
func NewAsOfQueryOptions(asOf time.Time) TemporalQueryOptions {
	return TemporalQueryOptions{
		Mode: QueryModeAsOf,
		AsOf: &asOf,
	}
}

// NewBetweenQueryOptions creates options for querying edges valid during a time range.
func NewBetweenQueryOptions(start, end time.Time) TemporalQueryOptions {
	tr := NewTimeRange(start, end)
	return TemporalQueryOptions{
		Mode:    QueryModeBetween,
		Between: &tr,
	}
}

// NewValidAtQueryOptions creates options for querying edges valid at a specific time.
// This is semantically equivalent to AsOf but may have different implementation behavior.
func NewValidAtQueryOptions(validAt time.Time) TemporalQueryOptions {
	return TemporalQueryOptions{
		Mode: QueryModeValidAt,
		AsOf: &validAt,
	}
}

// NewAllQueryOptions creates options for querying all edges regardless of temporal validity.
func NewAllQueryOptions() TemporalQueryOptions {
	return TemporalQueryOptions{
		Mode: QueryModeAll,
	}
}

// Matches returns true if the given TemporalEdge matches the query options.
func (opts TemporalQueryOptions) Matches(edge *TemporalEdge) bool {
	// Check transaction validity unless we're including superseded
	if !opts.IncludeSuperseded && !edge.IsCurrentTransaction() {
		return false
	}

	switch opts.Mode {
	case QueryModeCurrent:
		return edge.IsCurrent()

	case QueryModeAsOf, QueryModeValidAt:
		if opts.AsOf == nil {
			return edge.IsCurrent()
		}
		return edge.IsValidAt(*opts.AsOf)

	case QueryModeBetween:
		if opts.Between == nil {
			return true
		}
		// Edge matches if its valid period overlaps with the query range
		edgeRange := edge.GetValidTimeRange()
		return edgeRange.OverlapsWith(*opts.Between)

	case QueryModeAll:
		return true

	default:
		return false
	}
}

// GetValidTimeRange returns the valid time range for a TemporalEdge.
// If ValidFrom is nil, uses Unix epoch. If ValidTo is nil, uses current time + 100 years.
func (te *TemporalEdge) GetValidTimeRange() TimeRange {
	start := time.Unix(0, 0)
	if te.ValidFrom != nil {
		start = *te.ValidFrom
	}

	end := time.Now().Add(100 * 365 * 24 * time.Hour) // ~100 years from now
	if te.ValidTo != nil {
		end = *te.ValidTo
	}

	return TimeRange{Start: start, End: end}
}
