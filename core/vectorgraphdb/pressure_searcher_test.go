package vectorgraphdb

import (
	"sync"
	"testing"
)

func TestPressureLevel_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level    PressureLevel
		expected string
	}{
		{PressureLevelLow, "low"},
		{PressureLevelMedium, "medium"},
		{PressureLevelHigh, "high"},
		{PressureLevelCritical, "critical"},
		{PressureLevel(99), "unknown"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.expected, func(t *testing.T) {
			t.Parallel()
			if tc.level.String() != tc.expected {
				t.Errorf("%v.String() = %s, want %s", tc.level, tc.level.String(), tc.expected)
			}
		})
	}
}

func TestTierForPressure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level    PressureLevel
		expected MaxTier
	}{
		{PressureLevelLow, MaxTierFull},
		{PressureLevelMedium, MaxTierWarm},
		{PressureLevelHigh, MaxTierHot},
		{PressureLevelCritical, MaxTierNone},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.level.String(), func(t *testing.T) {
			t.Parallel()
			result := TierForPressure(tc.level)
			if result != tc.expected {
				t.Errorf("TierForPressure(%v) = %v, want %v", tc.level, result, tc.expected)
			}
		})
	}
}

func TestNewPressureAwareSearcher(t *testing.T) {
	t.Parallel()

	engine := &QueryEngine{}
	pas := NewPressureAwareSearcher(engine)

	if pas.Engine() != engine {
		t.Error("Engine() should return the provided engine")
	}
	if pas.MaxTier() != MaxTierFull {
		t.Errorf("initial MaxTier = %v, want MaxTierFull", pas.MaxTier())
	}
}

func TestPressureAwareSearcher_OnPressureChange(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcher(&QueryEngine{})

	// Initial state
	if pas.MaxTier() != MaxTierFull {
		t.Fatal("should start at MaxTierFull")
	}

	// Change to medium
	pas.OnPressureChange(PressureLevelMedium)
	if pas.MaxTier() != MaxTierWarm {
		t.Errorf("after medium pressure, MaxTier = %v, want MaxTierWarm", pas.MaxTier())
	}

	// Change to high
	pas.OnPressureChange(PressureLevelHigh)
	if pas.MaxTier() != MaxTierHot {
		t.Errorf("after high pressure, MaxTier = %v, want MaxTierHot", pas.MaxTier())
	}

	// Change to critical
	pas.OnPressureChange(PressureLevelCritical)
	if pas.MaxTier() != MaxTierNone {
		t.Errorf("after critical pressure, MaxTier = %v, want MaxTierNone", pas.MaxTier())
	}

	// Change back to low
	pas.OnPressureChange(PressureLevelLow)
	if pas.MaxTier() != MaxTierFull {
		t.Errorf("after low pressure, MaxTier = %v, want MaxTierFull", pas.MaxTier())
	}
}

func TestPressureAwareSearcher_AddTierChangeListener(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcher(&QueryEngine{})

	var called bool
	var capturedOld, capturedNew MaxTier

	pas.AddTierChangeListener(func(old, new MaxTier) {
		called = true
		capturedOld = old
		capturedNew = new
	})

	pas.OnPressureChange(PressureLevelHigh)

	if !called {
		t.Error("listener should be called on tier change")
	}
	if capturedOld != MaxTierFull {
		t.Errorf("old tier = %v, want MaxTierFull", capturedOld)
	}
	if capturedNew != MaxTierHot {
		t.Errorf("new tier = %v, want MaxTierHot", capturedNew)
	}
}

func TestPressureAwareSearcher_ListenerNotCalledOnSameTier(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcher(&QueryEngine{})

	callCount := 0
	pas.AddTierChangeListener(func(_, _ MaxTier) {
		callCount++
	})

	// Set to low (same as initial)
	pas.OnPressureChange(PressureLevelLow)

	if callCount != 0 {
		t.Errorf("listener called %d times, want 0 (no change)", callCount)
	}
}

func TestPressureAwareSearcher_CanSearch(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcher(&QueryEngine{})

	// Should be able to search at all levels except critical
	if !pas.CanSearch() {
		t.Error("should be able to search at low pressure")
	}

	pas.OnPressureChange(PressureLevelMedium)
	if !pas.CanSearch() {
		t.Error("should be able to search at medium pressure")
	}

	pas.OnPressureChange(PressureLevelHigh)
	if !pas.CanSearch() {
		t.Error("should be able to search at high pressure")
	}

	pas.OnPressureChange(PressureLevelCritical)
	if pas.CanSearch() {
		t.Error("should not be able to search at critical pressure")
	}
}

func TestPressureAwareSearcher_AdjustOptionsForTier_Hot(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcher(&QueryEngine{})

	opts := &HybridQueryOptions{
		VectorLimit: 50,
		GraphDepth:  5,
	}

	adjusted := pas.adjustOptionsForTier(opts, MaxTierHot)

	if adjusted.VectorLimit > 10 {
		t.Errorf("VectorLimit = %d, want <= 10 for hot tier", adjusted.VectorLimit)
	}
	if adjusted.GraphDepth > 1 {
		t.Errorf("GraphDepth = %d, want <= 1 for hot tier", adjusted.GraphDepth)
	}
}

func TestPressureAwareSearcher_AdjustOptionsForTier_Warm(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcher(&QueryEngine{})

	opts := &HybridQueryOptions{
		VectorLimit: 50,
		GraphDepth:  5,
	}

	adjusted := pas.adjustOptionsForTier(opts, MaxTierWarm)

	if adjusted.VectorLimit > 20 {
		t.Errorf("VectorLimit = %d, want <= 20 for warm tier", adjusted.VectorLimit)
	}
	if adjusted.GraphDepth > 2 {
		t.Errorf("GraphDepth = %d, want <= 2 for warm tier", adjusted.GraphDepth)
	}
}

func TestPressureAwareSearcher_AdjustOptionsForTier_Full(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcher(&QueryEngine{})

	opts := &HybridQueryOptions{
		VectorLimit: 50,
		GraphDepth:  5,
	}

	adjusted := pas.adjustOptionsForTier(opts, MaxTierFull)

	// Full tier should preserve original values
	if adjusted.VectorLimit != 50 {
		t.Errorf("VectorLimit = %d, want 50 for full tier", adjusted.VectorLimit)
	}
	if adjusted.GraphDepth != 5 {
		t.Errorf("GraphDepth = %d, want 5 for full tier", adjusted.GraphDepth)
	}
}

func TestPressureAwareSearcher_AdjustOptionsForTier_NilOpts(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcher(&QueryEngine{})

	adjusted := pas.adjustOptionsForTier(nil, MaxTierHot)

	if adjusted == nil {
		t.Error("should handle nil options")
	}
}

func TestPressureAwareSearcher_ConcurrentPressureChanges(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcher(&QueryEngine{})

	var wg sync.WaitGroup
	levels := []PressureLevel{
		PressureLevelLow,
		PressureLevelMedium,
		PressureLevelHigh,
		PressureLevelCritical,
	}

	// Concurrent pressure changes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pas.OnPressureChange(levels[idx%len(levels)])
		}(i)
	}

	wg.Wait()

	// Should be in a valid state
	tier := pas.MaxTier()
	if tier < MaxTierFull || tier > MaxTierNone {
		t.Errorf("invalid tier after concurrent changes: %v", tier)
	}
}

// =============================================================================
// PressureAwareSearcherWithStats Tests
// =============================================================================

func TestNewPressureAwareSearcherWithStats(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcherWithStats(&QueryEngine{})

	if pas.PressureAwareSearcher == nil {
		t.Error("should embed PressureAwareSearcher")
	}

	stats := pas.Stats()
	if stats.CurrentTier != MaxTierFull {
		t.Errorf("initial tier = %v, want MaxTierFull", stats.CurrentTier)
	}
}

func TestPressureAwareSearcherWithStats_TracksTierChanges(t *testing.T) {
	t.Parallel()

	pas := NewPressureAwareSearcherWithStats(&QueryEngine{})

	pas.OnPressureChange(PressureLevelMedium)
	pas.OnPressureChange(PressureLevelHigh)
	pas.OnPressureChange(PressureLevelLow)

	stats := pas.Stats()
	if stats.TierChanges != 3 {
		t.Errorf("TierChanges = %d, want 3", stats.TierChanges)
	}
}

func TestPressureAwareSearcherWithStats_Stats(t *testing.T) {
	t.Parallel()

	// Use nil engine - HybridQuery handles nil gracefully at critical level
	pas := NewPressureAwareSearcherWithStats(nil)

	// At critical level, queries are rejected without calling engine
	pas.OnPressureChange(PressureLevelCritical)
	pas.HybridQuery(nil, nil, nil) // This should just increment rejected count

	stats := pas.Stats()

	if stats.QueriesRejected != 1 {
		t.Errorf("QueriesRejected = %d, want 1", stats.QueriesRejected)
	}
	if stats.CurrentTier != MaxTierNone {
		t.Errorf("CurrentTier = %v, want MaxTierNone", stats.CurrentTier)
	}
}

func TestMaxTierConstants(t *testing.T) {
	t.Parallel()

	// Verify ordering
	if MaxTierFull >= MaxTierWarm {
		t.Error("MaxTierFull should be less than MaxTierWarm")
	}
	if MaxTierWarm >= MaxTierHot {
		t.Error("MaxTierWarm should be less than MaxTierHot")
	}
	if MaxTierHot >= MaxTierNone {
		t.Error("MaxTierHot should be less than MaxTierNone")
	}
}
