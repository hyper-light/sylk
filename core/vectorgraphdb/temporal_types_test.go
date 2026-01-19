package vectorgraphdb

import (
	"encoding/json"
	"testing"
	"time"
)

// =============================================================================
// TemporalEdge Tests
// =============================================================================

func TestNewTemporalEdge(t *testing.T) {
	edge := GraphEdge{
		ID:       1,
		SourceID: "source-1",
		TargetID: "target-1",
		EdgeType: EdgeTypeCalls,
		Weight:   0.9,
	}

	temporal := NewTemporalEdge(edge)

	if temporal.ID != edge.ID {
		t.Errorf("expected ID %d, got %d", edge.ID, temporal.ID)
	}
	if temporal.SourceID != edge.SourceID {
		t.Errorf("expected SourceID %s, got %s", edge.SourceID, temporal.SourceID)
	}
	if temporal.TargetID != edge.TargetID {
		t.Errorf("expected TargetID %s, got %s", edge.TargetID, temporal.TargetID)
	}
	if temporal.ValidFrom == nil {
		t.Error("expected ValidFrom to be set")
	}
	if temporal.ValidTo != nil {
		t.Error("expected ValidTo to be nil")
	}
	if temporal.TxStart == nil {
		t.Error("expected TxStart to be set")
	}
	if temporal.TxEnd != nil {
		t.Error("expected TxEnd to be nil")
	}
}

func TestNewTemporalEdgeWithValidity(t *testing.T) {
	edge := GraphEdge{
		ID:       1,
		SourceID: "source-1",
		TargetID: "target-1",
		EdgeType: EdgeTypeCalls,
	}

	validFrom := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)

	temporal := NewTemporalEdgeWithValidity(edge, &validFrom, &validTo)

	if temporal.ValidFrom == nil || !temporal.ValidFrom.Equal(validFrom) {
		t.Errorf("expected ValidFrom %v, got %v", validFrom, temporal.ValidFrom)
	}
	if temporal.ValidTo == nil || !temporal.ValidTo.Equal(validTo) {
		t.Errorf("expected ValidTo %v, got %v", validTo, temporal.ValidTo)
	}
	if temporal.TxStart == nil {
		t.Error("expected TxStart to be set")
	}
}

func TestTemporalEdge_IsValidAt(t *testing.T) {
	validFrom := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)

	edge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 1},
		ValidFrom: &validFrom,
		ValidTo:   &validTo,
	}

	tests := []struct {
		name     string
		time     time.Time
		expected bool
	}{
		{
			name:     "before valid range",
			time:     time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC),
			expected: false,
		},
		{
			name:     "at valid from",
			time:     validFrom,
			expected: true,
		},
		{
			name:     "within valid range",
			time:     time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
			expected: true,
		},
		{
			name:     "at valid to (exclusive)",
			time:     validTo,
			expected: false,
		},
		{
			name:     "after valid range",
			time:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := edge.IsValidAt(tt.time)
			if result != tt.expected {
				t.Errorf("IsValidAt(%v) = %v, expected %v", tt.time, result, tt.expected)
			}
		})
	}
}

func TestTemporalEdge_IsValidAt_NilBounds(t *testing.T) {
	// Test with nil ValidFrom (always valid from beginning)
	edge1 := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 1},
		ValidFrom: nil,
		ValidTo:   nil,
	}
	if !edge1.IsValidAt(time.Now()) {
		t.Error("expected nil bounds to be valid at any time")
	}

	// Test with only ValidTo set
	validTo := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	edge2 := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 2},
		ValidFrom: nil,
		ValidTo:   &validTo,
	}
	if !edge2.IsValidAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("expected edge to be valid before ValidTo")
	}
	if edge2.IsValidAt(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("expected edge to be invalid after ValidTo")
	}
}

func TestTemporalEdge_IsCurrent(t *testing.T) {
	validFrom := time.Now()

	currentEdge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 1},
		ValidFrom: &validFrom,
		ValidTo:   nil,
	}
	if !currentEdge.IsCurrent() {
		t.Error("expected edge with nil ValidTo to be current")
	}

	validTo := time.Now().Add(-time.Hour)
	expiredEdge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 2},
		ValidFrom: &validFrom,
		ValidTo:   &validTo,
	}
	if expiredEdge.IsCurrent() {
		t.Error("expected edge with ValidTo to not be current")
	}
}

func TestTemporalEdge_Invalidate(t *testing.T) {
	edge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 1},
		ValidTo:   nil,
	}

	endTime := time.Now()
	edge.Invalidate(endTime)

	if edge.ValidTo == nil {
		t.Error("expected ValidTo to be set after Invalidate")
	}
	if !edge.ValidTo.Equal(endTime) {
		t.Errorf("expected ValidTo %v, got %v", endTime, edge.ValidTo)
	}

	// Should not change if already set
	newEndTime := time.Now().Add(time.Hour)
	edge.Invalidate(newEndTime)
	if !edge.ValidTo.Equal(endTime) {
		t.Error("Invalidate should not change already set ValidTo")
	}
}

func TestTemporalEdge_Supersede(t *testing.T) {
	txStart := time.Now()
	edge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 1},
		TxStart:   &txStart,
		TxEnd:     nil,
	}

	endTime := time.Now()
	edge.Supersede(endTime)

	if edge.TxEnd == nil {
		t.Error("expected TxEnd to be set after Supersede")
	}
	if !edge.TxEnd.Equal(endTime) {
		t.Errorf("expected TxEnd %v, got %v", endTime, edge.TxEnd)
	}
}

func TestTemporalEdge_ValidDuration(t *testing.T) {
	validFrom := time.Now().Add(-24 * time.Hour)
	validTo := time.Now()

	edge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 1},
		ValidFrom: &validFrom,
		ValidTo:   &validTo,
	}

	duration := edge.ValidDuration()
	expected := 24 * time.Hour

	// Allow 1 second tolerance for test execution time
	if duration < expected-time.Second || duration > expected+time.Second {
		t.Errorf("expected duration ~%v, got %v", expected, duration)
	}

	// Test with nil ValidFrom
	edge2 := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 2},
		ValidFrom: nil,
	}
	if edge2.ValidDuration() != 0 {
		t.Error("expected 0 duration for nil ValidFrom")
	}
}

// =============================================================================
// TimeRange Tests
// =============================================================================

func TestNewTimeRange(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)

	tr := NewTimeRange(start, end)

	if !tr.Start.Equal(start) {
		t.Errorf("expected Start %v, got %v", start, tr.Start)
	}
	if !tr.End.Equal(end) {
		t.Errorf("expected End %v, got %v", end, tr.End)
	}
}

func TestTimeRange_Contains(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	tr := NewTimeRange(start, end)

	tests := []struct {
		name     string
		time     time.Time
		expected bool
	}{
		{"before range", time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC), false},
		{"at start", start, true},
		{"within range", time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC), true},
		{"at end (exclusive)", end, false},
		{"after range", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.Contains(tt.time)
			if result != tt.expected {
				t.Errorf("Contains(%v) = %v, expected %v", tt.time, result, tt.expected)
			}
		})
	}
}

func TestTimeRange_ContainsInclusive(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	tr := NewTimeRange(start, end)

	// End should be included in inclusive check
	if !tr.ContainsInclusive(end) {
		t.Error("ContainsInclusive should include end time")
	}
}

func TestTimeRange_OverlapsWith(t *testing.T) {
	range1 := NewTimeRange(
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC),
	)

	tests := []struct {
		name     string
		other    TimeRange
		expected bool
	}{
		{
			name: "overlapping ranges",
			other: NewTimeRange(
				time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2024, 9, 30, 0, 0, 0, 0, time.UTC),
			),
			expected: true,
		},
		{
			name: "contained range",
			other: NewTimeRange(
				time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2024, 5, 31, 0, 0, 0, 0, time.UTC),
			),
			expected: true,
		},
		{
			name: "adjacent (no overlap)",
			other: NewTimeRange(
				time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC),
				time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
			),
			expected: false,
		},
		{
			name: "completely before",
			other: NewTimeRange(
				time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
			),
			expected: false,
		},
		{
			name: "completely after",
			other: NewTimeRange(
				time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := range1.OverlapsWith(tt.other)
			if result != tt.expected {
				t.Errorf("OverlapsWith() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestTimeRange_Duration(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	tr := NewTimeRange(start, end)

	expected := 24 * time.Hour
	if tr.Duration() != expected {
		t.Errorf("expected duration %v, got %v", expected, tr.Duration())
	}
}

func TestTimeRange_IsZero(t *testing.T) {
	var zeroRange TimeRange
	if !zeroRange.IsZero() {
		t.Error("expected zero range to return true for IsZero")
	}

	nonZeroRange := NewTimeRange(time.Now(), time.Now().Add(time.Hour))
	if nonZeroRange.IsZero() {
		t.Error("expected non-zero range to return false for IsZero")
	}
}

func TestTimeRange_Intersection(t *testing.T) {
	range1 := NewTimeRange(
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC),
	)

	range2 := NewTimeRange(
		time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 30, 0, 0, 0, 0, time.UTC),
	)

	intersection := range1.Intersection(range2)

	expectedStart := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC)

	if !intersection.Start.Equal(expectedStart) {
		t.Errorf("expected intersection Start %v, got %v", expectedStart, intersection.Start)
	}
	if !intersection.End.Equal(expectedEnd) {
		t.Errorf("expected intersection End %v, got %v", expectedEnd, intersection.End)
	}

	// Test non-overlapping ranges
	range3 := NewTimeRange(
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC),
	)
	noIntersection := range1.Intersection(range3)
	if !noIntersection.IsZero() {
		t.Error("expected zero intersection for non-overlapping ranges")
	}
}

func TestTimeRange_Union(t *testing.T) {
	range1 := NewTimeRange(
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC),
	)

	range2 := NewTimeRange(
		time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
	)

	union := range1.Union(range2)

	expectedStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)

	if !union.Start.Equal(expectedStart) {
		t.Errorf("expected union Start %v, got %v", expectedStart, union.Start)
	}
	if !union.End.Equal(expectedEnd) {
		t.Errorf("expected union End %v, got %v", expectedEnd, union.End)
	}
}

// =============================================================================
// TemporalQueryMode Tests
// =============================================================================

func TestTemporalQueryMode_String(t *testing.T) {
	tests := []struct {
		mode     TemporalQueryMode
		expected string
	}{
		{QueryModeCurrent, "current"},
		{QueryModeAsOf, "as_of"},
		{QueryModeBetween, "between"},
		{QueryModeValidAt, "valid_at"},
		{QueryModeAll, "all"},
		{TemporalQueryMode(99), "query_mode(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.mode.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.mode.String())
			}
		})
	}
}

func TestParseTemporalQueryMode(t *testing.T) {
	tests := []struct {
		input    string
		expected TemporalQueryMode
		ok       bool
	}{
		{"current", QueryModeCurrent, true},
		{"as_of", QueryModeAsOf, true},
		{"between", QueryModeBetween, true},
		{"valid_at", QueryModeValidAt, true},
		{"all", QueryModeAll, true},
		{"invalid", TemporalQueryMode(0), false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, ok := ParseTemporalQueryMode(tt.input)
			if ok != tt.ok {
				t.Errorf("expected ok=%v, got %v", tt.ok, ok)
			}
			if ok && result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestTemporalQueryMode_JSON(t *testing.T) {
	mode := QueryModeAsOf

	data, err := json.Marshal(mode)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	expected := `"as_of"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}

	var unmarshaled TemporalQueryMode
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled != mode {
		t.Errorf("expected %v, got %v", mode, unmarshaled)
	}

	// Test integer unmarshaling
	var intUnmarshaled TemporalQueryMode
	if err := json.Unmarshal([]byte("1"), &intUnmarshaled); err != nil {
		t.Fatalf("failed to unmarshal int: %v", err)
	}
	if intUnmarshaled != QueryModeAsOf {
		t.Errorf("expected QueryModeAsOf, got %v", intUnmarshaled)
	}
}

// =============================================================================
// TemporalQueryOptions Tests
// =============================================================================

func TestNewCurrentQueryOptions(t *testing.T) {
	opts := NewCurrentQueryOptions()
	if opts.Mode != QueryModeCurrent {
		t.Errorf("expected QueryModeCurrent, got %v", opts.Mode)
	}
}

func TestNewAsOfQueryOptions(t *testing.T) {
	asOf := time.Now()
	opts := NewAsOfQueryOptions(asOf)

	if opts.Mode != QueryModeAsOf {
		t.Errorf("expected QueryModeAsOf, got %v", opts.Mode)
	}
	if opts.AsOf == nil || !opts.AsOf.Equal(asOf) {
		t.Errorf("expected AsOf %v, got %v", asOf, opts.AsOf)
	}
}

func TestNewBetweenQueryOptions(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	opts := NewBetweenQueryOptions(start, end)

	if opts.Mode != QueryModeBetween {
		t.Errorf("expected QueryModeBetween, got %v", opts.Mode)
	}
	if opts.Between == nil {
		t.Fatal("expected Between to be set")
	}
	if !opts.Between.Start.Equal(start) {
		t.Errorf("expected Between.Start %v, got %v", start, opts.Between.Start)
	}
	if !opts.Between.End.Equal(end) {
		t.Errorf("expected Between.End %v, got %v", end, opts.Between.End)
	}
}

func TestTemporalQueryOptions_Matches(t *testing.T) {
	now := time.Now()
	pastStart := now.Add(-48 * time.Hour)
	pastEnd := now.Add(-24 * time.Hour)
	futureEnd := now.Add(24 * time.Hour)

	// Current edge (no ValidTo)
	currentEdge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 1},
		ValidFrom: &pastStart,
		ValidTo:   nil,
		TxStart:   &now,
		TxEnd:     nil,
	}

	// Past edge (has ValidTo in the past)
	pastEdge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 2},
		ValidFrom: &pastStart,
		ValidTo:   &pastEnd,
		TxStart:   &now,
		TxEnd:     nil,
	}

	// Future-bounded edge
	futureBoundedEdge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 3},
		ValidFrom: &pastStart,
		ValidTo:   &futureEnd,
		TxStart:   &now,
		TxEnd:     nil,
	}

	t.Run("QueryModeCurrent", func(t *testing.T) {
		opts := NewCurrentQueryOptions()
		if !opts.Matches(currentEdge) {
			t.Error("expected current edge to match")
		}
		if opts.Matches(pastEdge) {
			t.Error("expected past edge to not match")
		}
	})

	t.Run("QueryModeAsOf", func(t *testing.T) {
		queryTime := pastStart.Add(12 * time.Hour) // Between pastStart and pastEnd
		opts := NewAsOfQueryOptions(queryTime)

		if !opts.Matches(pastEdge) {
			t.Error("expected past edge to match at query time")
		}
		if !opts.Matches(currentEdge) {
			t.Error("expected current edge to match at query time")
		}
	})

	t.Run("QueryModeBetween", func(t *testing.T) {
		opts := NewBetweenQueryOptions(pastStart, now)

		if !opts.Matches(pastEdge) {
			t.Error("expected past edge to match between range")
		}
		if !opts.Matches(currentEdge) {
			t.Error("expected current edge to match between range")
		}
		if !opts.Matches(futureBoundedEdge) {
			t.Error("expected future-bounded edge to match between range")
		}
	})

	t.Run("QueryModeAll", func(t *testing.T) {
		opts := NewAllQueryOptions()
		if !opts.Matches(currentEdge) {
			t.Error("expected all mode to match current edge")
		}
		if !opts.Matches(pastEdge) {
			t.Error("expected all mode to match past edge")
		}
	})
}

func TestTemporalQueryOptions_SupersededEdges(t *testing.T) {
	now := time.Now()
	txEnd := now.Add(-time.Hour)

	supersededEdge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 1},
		TxStart:   &now,
		TxEnd:     &txEnd,
	}

	// Default options should not match superseded edges
	opts := NewCurrentQueryOptions()
	if opts.Matches(supersededEdge) {
		t.Error("expected superseded edge to not match without IncludeSuperseded")
	}

	// With IncludeSuperseded, should match
	opts.IncludeSuperseded = true
	if !opts.Matches(supersededEdge) {
		t.Error("expected superseded edge to match with IncludeSuperseded")
	}
}

func TestTemporalEdge_GetValidTimeRange(t *testing.T) {
	validFrom := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)

	edge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 1},
		ValidFrom: &validFrom,
		ValidTo:   &validTo,
	}

	tr := edge.GetValidTimeRange()

	if !tr.Start.Equal(validFrom) {
		t.Errorf("expected Start %v, got %v", validFrom, tr.Start)
	}
	if !tr.End.Equal(validTo) {
		t.Errorf("expected End %v, got %v", validTo, tr.End)
	}
}

func TestTemporalEdge_GetValidTimeRange_NilBounds(t *testing.T) {
	edge := &TemporalEdge{
		GraphEdge: GraphEdge{ID: 1},
		ValidFrom: nil,
		ValidTo:   nil,
	}

	tr := edge.GetValidTimeRange()

	// Start should be Unix epoch
	if !tr.Start.Equal(time.Unix(0, 0)) {
		t.Errorf("expected Start to be Unix epoch, got %v", tr.Start)
	}

	// End should be far in the future (approximately now + 100 years)
	if tr.End.Before(time.Now().Add(99 * 365 * 24 * time.Hour)) {
		t.Error("expected End to be far in the future")
	}
}

// =============================================================================
// JSON Serialization Tests
// =============================================================================

func TestTemporalEdge_JSON(t *testing.T) {
	validFrom := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	edge := &TemporalEdge{
		GraphEdge: GraphEdge{
			ID:       1,
			SourceID: "source-1",
			TargetID: "target-1",
			EdgeType: EdgeTypeCalls,
			Weight:   0.9,
		},
		ValidFrom: &validFrom,
		ValidTo:   nil,
	}

	data, err := json.Marshal(edge)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var unmarshaled TemporalEdge
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled.ID != edge.ID {
		t.Errorf("expected ID %d, got %d", edge.ID, unmarshaled.ID)
	}
	if unmarshaled.SourceID != edge.SourceID {
		t.Errorf("expected SourceID %s, got %s", edge.SourceID, unmarshaled.SourceID)
	}
	if unmarshaled.ValidFrom == nil || !unmarshaled.ValidFrom.Equal(validFrom) {
		t.Errorf("expected ValidFrom %v, got %v", validFrom, unmarshaled.ValidFrom)
	}
	if unmarshaled.ValidTo != nil {
		t.Error("expected ValidTo to be nil")
	}
}

func TestTimeRange_JSON(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	tr := NewTimeRange(start, end)

	data, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var unmarshaled TimeRange
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !unmarshaled.Start.Equal(start) {
		t.Errorf("expected Start %v, got %v", start, unmarshaled.Start)
	}
	if !unmarshaled.End.Equal(end) {
		t.Errorf("expected End %v, got %v", end, unmarshaled.End)
	}
}

func TestTemporalQueryOptions_JSON(t *testing.T) {
	asOf := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	opts := TemporalQueryOptions{
		Mode:              QueryModeAsOf,
		AsOf:              &asOf,
		IncludeSuperseded: true,
	}

	data, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var unmarshaled TemporalQueryOptions
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled.Mode != QueryModeAsOf {
		t.Errorf("expected Mode QueryModeAsOf, got %v", unmarshaled.Mode)
	}
	if unmarshaled.AsOf == nil || !unmarshaled.AsOf.Equal(asOf) {
		t.Errorf("expected AsOf %v, got %v", asOf, unmarshaled.AsOf)
	}
	if !unmarshaled.IncludeSuperseded {
		t.Error("expected IncludeSuperseded to be true")
	}
}
