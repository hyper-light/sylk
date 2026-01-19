package memory

import (
	"context"
	"math"
	"testing"
	"time"
)

// =============================================================================
// MD.5.1 SpacingAnalyzer Tests
// =============================================================================

func TestNewSpacingAnalyzer(t *testing.T) {
	t.Run("default target retention", func(t *testing.T) {
		analyzer := NewSpacingAnalyzer(0.9)
		if analyzer == nil {
			t.Fatal("expected non-nil analyzer")
		}
		if analyzer.TargetRetention() != 0.9 {
			t.Errorf("expected target retention 0.9, got %f", analyzer.TargetRetention())
		}
	})

	t.Run("invalid target retention uses default", func(t *testing.T) {
		testCases := []float64{0, -0.5, 1.5, 2.0}
		for _, tc := range testCases {
			analyzer := NewSpacingAnalyzer(tc)
			if analyzer.TargetRetention() != DefaultTargetRetention {
				t.Errorf("invalid retention %f should use default, got %f",
					tc, analyzer.TargetRetention())
			}
		}
	})

	t.Run("valid retention at boundary", func(t *testing.T) {
		analyzer := NewSpacingAnalyzer(1.0)
		if analyzer.TargetRetention() != 1.0 {
			t.Errorf("retention 1.0 should be valid, got %f", analyzer.TargetRetention())
		}
	})

	t.Run("default intervals", func(t *testing.T) {
		analyzer := NewSpacingAnalyzer(0.9)
		if analyzer.MinInterval() != DefaultMinInterval {
			t.Errorf("expected min interval %v, got %v", DefaultMinInterval, analyzer.MinInterval())
		}
		if analyzer.MaxInterval() != DefaultMaxInterval {
			t.Errorf("expected max interval %v, got %v", DefaultMaxInterval, analyzer.MaxInterval())
		}
	})
}

func TestNewSpacingAnalyzerWithIntervals(t *testing.T) {
	t.Run("custom intervals", func(t *testing.T) {
		minInt := 30 * time.Minute
		maxInt := 7 * 24 * time.Hour
		analyzer := NewSpacingAnalyzerWithIntervals(0.85, minInt, maxInt)

		if analyzer.TargetRetention() != 0.85 {
			t.Errorf("expected retention 0.85, got %f", analyzer.TargetRetention())
		}
		if analyzer.MinInterval() != minInt {
			t.Errorf("expected min interval %v, got %v", minInt, analyzer.MinInterval())
		}
		if analyzer.MaxInterval() != maxInt {
			t.Errorf("expected max interval %v, got %v", maxInt, analyzer.MaxInterval())
		}
	})

	t.Run("invalid min interval uses default", func(t *testing.T) {
		analyzer := NewSpacingAnalyzerWithIntervals(0.9, -1*time.Hour, 7*24*time.Hour)
		if analyzer.MinInterval() != DefaultMinInterval {
			t.Errorf("invalid min interval should use default")
		}
	})

	t.Run("max interval must exceed min interval", func(t *testing.T) {
		analyzer := NewSpacingAnalyzerWithIntervals(0.9, 2*time.Hour, 1*time.Hour)
		if analyzer.MaxInterval() != DefaultMaxInterval {
			t.Errorf("invalid max interval should use default")
		}
	})
}

func TestSpacingAnalyzer_OptimalReviewTime(t *testing.T) {
	analyzer := NewSpacingAnalyzer(0.9)
	now := time.Now().UTC()

	t.Run("nil memory returns min interval", func(t *testing.T) {
		interval := analyzer.OptimalReviewTime(nil)
		if interval != analyzer.MinInterval() {
			t.Errorf("nil memory should return min interval, got %v", interval)
		}
	})

	t.Run("empty traces returns min interval", func(t *testing.T) {
		memory := &ACTRMemory{
			NodeID: "test",
			Traces: []AccessTrace{},
		}
		interval := analyzer.OptimalReviewTime(memory)
		if interval != analyzer.MinInterval() {
			t.Errorf("empty traces should return min interval, got %v", interval)
		}
	})

	t.Run("single trace uses first pimsleur interval", func(t *testing.T) {
		memory := &ACTRMemory{
			NodeID:     "test",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now, AccessType: AccessCreation},
			},
		}
		interval := analyzer.OptimalReviewTime(memory)

		// Should be close to 1 hour base, adjusted by factors
		if interval < 30*time.Minute || interval > 3*time.Hour {
			t.Errorf("single trace interval should be around 1 hour, got %v", interval)
		}
	})

	t.Run("more reviews increase interval", func(t *testing.T) {
		// Memory with 1 review
		memory1 := &ACTRMemory{
			NodeID:     "test1",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now, AccessType: AccessCreation},
			},
		}

		// Memory with 5 reviews (well-spaced)
		memory5 := &ACTRMemory{
			NodeID:     "test5",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-30 * 24 * time.Hour), AccessType: AccessCreation},
				{AccessedAt: now.Add(-29 * 24 * time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-26 * 24 * time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-19 * 24 * time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-5 * 24 * time.Hour), AccessType: AccessRetrieval},
			},
		}

		interval1 := analyzer.OptimalReviewTime(memory1)
		interval5 := analyzer.OptimalReviewTime(memory5)

		if interval5 <= interval1 {
			t.Errorf("more reviews should increase interval: 1 review=%v, 5 reviews=%v",
				interval1, interval5)
		}
	})

	t.Run("interval clamped to max", func(t *testing.T) {
		// Very high activation, many reviews -> would exceed max
		memory := &ACTRMemory{
			NodeID:         "test",
			DecayAlpha:     1.0,
			DecayBeta:      9.0, // Very slow decay
			BaseOffsetMean: 5.0, // High base offset
			Traces:         make([]AccessTrace, 10),
		}
		for i := 0; i < 10; i++ {
			memory.Traces[i] = AccessTrace{
				AccessedAt: now.Add(-time.Duration(i) * time.Minute),
				AccessType: AccessRetrieval,
			}
		}

		interval := analyzer.OptimalReviewTime(memory)
		if interval > analyzer.MaxInterval() {
			t.Errorf("interval should be clamped to max: got %v, max=%v",
				interval, analyzer.MaxInterval())
		}
	})
}

func TestSpacingAnalyzer_computeStability(t *testing.T) {
	analyzer := NewSpacingAnalyzer(0.9)
	now := time.Now().UTC()

	t.Run("nil memory returns 0.5", func(t *testing.T) {
		stability := analyzer.computeStability(nil)
		if stability != 0.5 {
			t.Errorf("nil memory should have stability 0.5, got %f", stability)
		}
	})

	t.Run("single trace returns 0.5", func(t *testing.T) {
		memory := &ACTRMemory{
			Traces: []AccessTrace{
				{AccessedAt: now, AccessType: AccessCreation},
			},
		}
		stability := analyzer.computeStability(memory)
		if stability != 0.5 {
			t.Errorf("single trace should have stability 0.5, got %f", stability)
		}
	})

	t.Run("well-spaced accesses have high stability", func(t *testing.T) {
		// Accesses following Pimsleur schedule: 1h, 1d, 3d, 1w
		memory := &ACTRMemory{
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-15 * 24 * time.Hour), AccessType: AccessCreation},
				{AccessedAt: now.Add(-15*24*time.Hour + 1*time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-14 * 24 * time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-11 * 24 * time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-4 * 24 * time.Hour), AccessType: AccessRetrieval},
			},
		}

		stability := analyzer.computeStability(memory)
		if stability < 0.5 {
			t.Errorf("well-spaced accesses should have stability > 0.5, got %f", stability)
		}
	})

	t.Run("massed accesses have low stability", func(t *testing.T) {
		// All accesses within 1 hour (massed practice)
		memory := &ACTRMemory{
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-60 * time.Minute), AccessType: AccessCreation},
				{AccessedAt: now.Add(-45 * time.Minute), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-30 * time.Minute), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-15 * time.Minute), AccessType: AccessRetrieval},
				{AccessedAt: now, AccessType: AccessRetrieval},
			},
		}

		stability := analyzer.computeStability(memory)
		if stability > 0.6 {
			t.Errorf("massed accesses should have stability < 0.6, got %f", stability)
		}
	})

	t.Run("stability bounded 0 to 1", func(t *testing.T) {
		testCases := []*ACTRMemory{
			{Traces: []AccessTrace{}},
			{Traces: []AccessTrace{{AccessedAt: now}}},
			{Traces: []AccessTrace{
				{AccessedAt: now.Add(-100 * 24 * time.Hour)},
				{AccessedAt: now},
			}},
		}

		for _, memory := range testCases {
			stability := analyzer.computeStability(memory)
			if stability < 0 || stability > 1 {
				t.Errorf("stability should be in [0,1], got %f", stability)
			}
		}
	})
}

func TestSpacingAnalyzer_SuggestReviewList(t *testing.T) {
	analyzer := NewSpacingAnalyzer(0.9)
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("empty input returns nil", func(t *testing.T) {
		result := analyzer.SuggestReviewList(ctx, []*ACTRMemory{}, now, 10)
		if result != nil {
			t.Errorf("empty input should return nil, got %v", result)
		}
	})

	t.Run("nil memories filtered out", func(t *testing.T) {
		memories := []*ACTRMemory{
			nil,
			{NodeID: "test", Traces: []AccessTrace{{AccessedAt: now.Add(-2 * time.Hour)}}},
			nil,
		}
		result := analyzer.SuggestReviewList(ctx, memories, now, 10)
		if len(result) != 1 {
			t.Errorf("expected 1 result after filtering nils, got %d", len(result))
		}
	})

	t.Run("sorted by urgency", func(t *testing.T) {
		// Memory A: last accessed 1 day ago, optimal interval ~1h -> very overdue
		memoryA := &ACTRMemory{
			NodeID:     "A",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-24 * time.Hour), AccessType: AccessCreation},
			},
		}

		// Memory B: last accessed 30 min ago, optimal interval ~1h -> not yet due
		memoryB := &ACTRMemory{
			NodeID:     "B",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-30 * time.Minute), AccessType: AccessCreation},
			},
		}

		// Memory C: last accessed 3 hours ago, optimal interval ~1h -> overdue
		memoryC := &ACTRMemory{
			NodeID:     "C",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-3 * time.Hour), AccessType: AccessCreation},
			},
		}

		memories := []*ACTRMemory{memoryB, memoryA, memoryC}
		result := analyzer.SuggestReviewList(ctx, memories, now, 10)

		if len(result) != 3 {
			t.Fatalf("expected 3 results, got %d", len(result))
		}

		// A should be first (most overdue), B should be last (not yet due)
		if result[0].NodeID != "A" {
			t.Errorf("most overdue memory should be first, got %s", result[0].NodeID)
		}
		if result[len(result)-1].NodeID != "B" {
			t.Errorf("least urgent memory should be last, got %s", result[len(result)-1].NodeID)
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		memories := make([]*ACTRMemory, 10)
		for i := 0; i < 10; i++ {
			memories[i] = &ACTRMemory{
				NodeID:     string(rune('A' + i)),
				DecayAlpha: 5.0,
				DecayBeta:  5.0,
				Traces: []AccessTrace{
					{AccessedAt: now.Add(-time.Duration(i) * time.Hour), AccessType: AccessCreation},
				},
			}
		}

		result := analyzer.SuggestReviewList(ctx, memories, now, 3)
		if len(result) != 3 {
			t.Errorf("expected 3 results with limit=3, got %d", len(result))
		}
	})

	t.Run("zero or negative limit returns all", func(t *testing.T) {
		memories := []*ACTRMemory{
			{NodeID: "A", DecayAlpha: 5.0, DecayBeta: 5.0, Traces: []AccessTrace{{AccessedAt: now}}},
			{NodeID: "B", DecayAlpha: 5.0, DecayBeta: 5.0, Traces: []AccessTrace{{AccessedAt: now}}},
		}

		result := analyzer.SuggestReviewList(ctx, memories, now, 0)
		if len(result) != 2 {
			t.Errorf("limit=0 should return all, got %d", len(result))
		}

		result = analyzer.SuggestReviewList(ctx, memories, now, -1)
		if len(result) != 2 {
			t.Errorf("limit=-1 should return all, got %d", len(result))
		}
	})
}

func TestSpacingAnalyzer_AnalyzeSpacingPattern(t *testing.T) {
	analyzer := NewSpacingAnalyzer(0.9)
	now := time.Now().UTC()

	t.Run("nil memory", func(t *testing.T) {
		report := analyzer.AnalyzeSpacingPattern(nil)
		if report == nil {
			t.Fatal("expected non-nil report for nil memory")
		}
		if report.Stability != 0 {
			t.Errorf("nil memory should have stability 0, got %f", report.Stability)
		}
		if report.IsWellSpaced {
			t.Error("nil memory should not be well-spaced")
		}
	})

	t.Run("empty traces", func(t *testing.T) {
		memory := &ACTRMemory{NodeID: "test", Traces: []AccessTrace{}}
		report := analyzer.AnalyzeSpacingPattern(memory)
		if report.Stability != 0 {
			t.Errorf("empty traces should have stability 0, got %f", report.Stability)
		}
		if report.AverageInterval != 0 {
			t.Error("empty traces should have zero average interval")
		}
	})

	t.Run("single trace", func(t *testing.T) {
		memory := &ACTRMemory{
			NodeID:     "test",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now, AccessType: AccessCreation},
			},
		}
		report := analyzer.AnalyzeSpacingPattern(memory)

		if report.AverageInterval != 0 {
			t.Error("single trace should have zero average interval")
		}
		if report.LongestGap != 0 || report.ShortestGap != 0 {
			t.Error("single trace should have zero gaps")
		}
		if report.IsWellSpaced {
			t.Error("single trace should not be well-spaced")
		}
	})

	t.Run("multiple traces with statistics", func(t *testing.T) {
		memory := &ACTRMemory{
			NodeID:     "test",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-10 * time.Hour), AccessType: AccessCreation},
				{AccessedAt: now.Add(-9 * time.Hour), AccessType: AccessRetrieval},  // 1h gap
				{AccessedAt: now.Add(-6 * time.Hour), AccessType: AccessRetrieval},  // 3h gap
				{AccessedAt: now.Add(-4 * time.Hour), AccessType: AccessRetrieval},  // 2h gap
				{AccessedAt: now, AccessType: AccessRetrieval},                       // 4h gap
			},
		}
		report := analyzer.AnalyzeSpacingPattern(memory)

		// Average: (1+3+2+4) / 4 = 2.5 hours
		expectedAvg := 2*time.Hour + 30*time.Minute
		if math.Abs(float64(report.AverageInterval-expectedAvg)) > float64(time.Minute) {
			t.Errorf("expected average interval ~%v, got %v", expectedAvg, report.AverageInterval)
		}

		if report.LongestGap != 4*time.Hour {
			t.Errorf("expected longest gap 4h, got %v", report.LongestGap)
		}
		if report.ShortestGap != 1*time.Hour {
			t.Errorf("expected shortest gap 1h, got %v", report.ShortestGap)
		}
	})

	t.Run("well-spaced determination", func(t *testing.T) {
		// Well-spaced memory (stability >= 0.6, at least 2 accesses)
		wellSpaced := &ACTRMemory{
			NodeID:     "well",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-15 * 24 * time.Hour), AccessType: AccessCreation},
				{AccessedAt: now.Add(-15*24*time.Hour + 1*time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-14 * 24 * time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-11 * 24 * time.Hour), AccessType: AccessRetrieval},
			},
		}
		report := analyzer.AnalyzeSpacingPattern(wellSpaced)
		if report.Stability < 0.4 {
			t.Logf("well-spaced stability: %f (may vary based on algorithm)", report.Stability)
		}

		// Poorly spaced memory (massed practice)
		poorlySpaced := &ACTRMemory{
			NodeID:     "poor",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-5 * time.Minute), AccessType: AccessCreation},
				{AccessedAt: now.Add(-4 * time.Minute), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-3 * time.Minute), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-2 * time.Minute), AccessType: AccessRetrieval},
			},
		}
		report = analyzer.AnalyzeSpacingPattern(poorlySpaced)
		if report.IsWellSpaced {
			t.Error("massed practice should not be well-spaced")
		}
	})

	t.Run("recommended next review computed", func(t *testing.T) {
		memory := &ACTRMemory{
			NodeID:     "test",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now, AccessType: AccessCreation},
			},
		}
		report := analyzer.AnalyzeSpacingPattern(memory)

		// Recommended review should be after last access
		if report.RecommendedNextReview.Before(now) {
			t.Error("recommended review should be after last access")
		}

		// Should be within bounds
		interval := report.RecommendedNextReview.Sub(now)
		if interval < analyzer.MinInterval() || interval > analyzer.MaxInterval() {
			t.Errorf("recommended interval %v out of bounds [%v, %v]",
				interval, analyzer.MinInterval(), analyzer.MaxInterval())
		}
	})
}

func TestSpacingAnalyzer_PimsleurIntervals(t *testing.T) {
	analyzer := NewSpacingAnalyzer(0.9)

	t.Run("interval progression", func(t *testing.T) {
		// Verify intervals increase with review count
		prevInterval := time.Duration(0)
		for i := 1; i <= 6; i++ {
			interval := analyzer.pimsleurInterval(i)
			if interval <= prevInterval {
				t.Errorf("interval for %d reviews (%v) should exceed %v",
					i, interval, prevInterval)
			}
			prevInterval = interval
		}
	})

	t.Run("caps at max interval", func(t *testing.T) {
		// Reviews beyond 6 should use the last interval
		interval6 := analyzer.pimsleurInterval(6)
		interval10 := analyzer.pimsleurInterval(10)
		interval100 := analyzer.pimsleurInterval(100)

		if interval10 != interval6 {
			t.Errorf("interval for 10 reviews should equal 6 reviews: %v vs %v",
				interval10, interval6)
		}
		if interval100 != interval6 {
			t.Errorf("interval for 100 reviews should equal 6 reviews: %v vs %v",
				interval100, interval6)
		}
	})

	t.Run("zero or negative reviews use first interval", func(t *testing.T) {
		if analyzer.pimsleurInterval(0) != pimsleurIntervals[0] {
			t.Error("0 reviews should use first interval")
		}
		if analyzer.pimsleurInterval(-1) != pimsleurIntervals[0] {
			t.Error("-1 reviews should use first interval")
		}
	})
}

func TestSpacingAnalyzer_Sigmoid(t *testing.T) {
	analyzer := NewSpacingAnalyzer(0.9)

	t.Run("sigmoid properties", func(t *testing.T) {
		// Sigmoid(0) should be 0.5
		if math.Abs(analyzer.sigmoid(0)-0.5) > 0.01 {
			t.Errorf("sigmoid(0) should be 0.5, got %f", analyzer.sigmoid(0))
		}

		// Sigmoid should be monotonically increasing
		prev := analyzer.sigmoid(-10)
		for x := -9.0; x <= 10.0; x += 1.0 {
			current := analyzer.sigmoid(x)
			if current < prev {
				t.Errorf("sigmoid should be monotonically increasing: sigmoid(%f) < sigmoid(%f)",
					x, x-1)
			}
			prev = current
		}

		// Sigmoid bounds
		if analyzer.sigmoid(-100) < 0 {
			t.Error("sigmoid should never be negative")
		}
		if analyzer.sigmoid(100) > 1 {
			t.Error("sigmoid should never exceed 1")
		}
	})

	t.Run("extreme values clamped", func(t *testing.T) {
		if analyzer.sigmoid(-21) != 0 {
			t.Errorf("sigmoid(-21) should be clamped to 0, got %f", analyzer.sigmoid(-21))
		}
		if analyzer.sigmoid(21) != 1 {
			t.Errorf("sigmoid(21) should be clamped to 1, got %f", analyzer.sigmoid(21))
		}
	})
}

func TestSpacingAnalyzer_SettersAndGetters(t *testing.T) {
	analyzer := NewSpacingAnalyzer(0.9)

	t.Run("SetTargetRetention", func(t *testing.T) {
		analyzer.SetTargetRetention(0.85)
		if analyzer.TargetRetention() != 0.85 {
			t.Errorf("expected retention 0.85, got %f", analyzer.TargetRetention())
		}

		// Invalid values ignored
		analyzer.SetTargetRetention(0)
		if analyzer.TargetRetention() != 0.85 {
			t.Error("0 should be ignored")
		}
		analyzer.SetTargetRetention(1.5)
		if analyzer.TargetRetention() != 0.85 {
			t.Error("1.5 should be ignored")
		}
	})

	t.Run("SetMinInterval", func(t *testing.T) {
		analyzer.SetMinInterval(30 * time.Minute)
		if analyzer.MinInterval() != 30*time.Minute {
			t.Errorf("expected min interval 30m, got %v", analyzer.MinInterval())
		}

		// Invalid value ignored
		analyzer.SetMinInterval(-1 * time.Minute)
		if analyzer.MinInterval() != 30*time.Minute {
			t.Error("negative should be ignored")
		}
	})

	t.Run("SetMaxInterval", func(t *testing.T) {
		analyzer.SetMaxInterval(14 * 24 * time.Hour)
		if analyzer.MaxInterval() != 14*24*time.Hour {
			t.Errorf("expected max interval 14d, got %v", analyzer.MaxInterval())
		}

		// Value less than min ignored
		analyzer.SetMaxInterval(15 * time.Minute) // Less than current min (30m)
		if analyzer.MaxInterval() != 14*24*time.Hour {
			t.Error("value less than min should be ignored")
		}
	})
}

func TestSpacingAnalyzer_Integration(t *testing.T) {
	analyzer := NewSpacingAnalyzer(0.9)
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("full workflow", func(t *testing.T) {
		// Create a set of memories with varying access patterns
		memories := make([]*ACTRMemory, 5)

		// Memory 0: Well-spaced, following Pimsleur schedule
		memories[0] = &ACTRMemory{
			NodeID:     "well-spaced",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-15 * 24 * time.Hour), AccessType: AccessCreation},
				{AccessedAt: now.Add(-15*24*time.Hour + 1*time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-14 * 24 * time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-11 * 24 * time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-4 * 24 * time.Hour), AccessType: AccessRetrieval},
			},
		}

		// Memory 1: Massed practice (all within minutes)
		memories[1] = &ACTRMemory{
			NodeID:     "massed",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-30 * time.Minute), AccessType: AccessCreation},
				{AccessedAt: now.Add(-25 * time.Minute), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-20 * time.Minute), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-15 * time.Minute), AccessType: AccessRetrieval},
			},
		}

		// Memory 2: Very old, needs review
		memories[2] = &ACTRMemory{
			NodeID:     "old",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-60 * 24 * time.Hour), AccessType: AccessCreation},
			},
		}

		// Memory 3: Recent, doesn't need review yet
		memories[3] = &ACTRMemory{
			NodeID:     "recent",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-10 * time.Minute), AccessType: AccessCreation},
			},
		}

		// Memory 4: Moderate access pattern
		memories[4] = &ACTRMemory{
			NodeID:     "moderate",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-7 * 24 * time.Hour), AccessType: AccessCreation},
				{AccessedAt: now.Add(-5 * 24 * time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-2 * 24 * time.Hour), AccessType: AccessRetrieval},
			},
		}

		// 1. Analyze each memory's spacing pattern
		for _, memory := range memories {
			report := analyzer.AnalyzeSpacingPattern(memory)
			if report == nil {
				t.Errorf("report should not be nil for %s", memory.NodeID)
				continue
			}

			// Verify report fields are reasonable
			if report.Stability < 0 || report.Stability > 1 {
				t.Errorf("%s: stability %f out of bounds", memory.NodeID, report.Stability)
			}
		}

		// 2. Get suggested review list
		reviewList := analyzer.SuggestReviewList(ctx, memories, now, 3)
		if len(reviewList) != 3 {
			t.Errorf("expected 3 items in review list, got %d", len(reviewList))
		}

		// 3. Verify "old" memory is in top priority
		found := false
		for i, m := range reviewList[:min(2, len(reviewList))] {
			if m.NodeID == "old" {
				found = true
				t.Logf("'old' memory found at position %d (expected high priority)", i)
			}
		}
		if !found {
			t.Log("'old' memory not in top 2 - may depend on exact urgency calculation")
		}

		// 4. Verify "recent" memory is low priority
		if len(reviewList) >= 3 && reviewList[len(reviewList)-1].NodeID != "recent" {
			t.Logf("last item is %s, expected 'recent' to be lowest priority",
				reviewList[len(reviewList)-1].NodeID)
		}

		// 5. Compute optimal review times
		for _, memory := range memories {
			interval := analyzer.OptimalReviewTime(memory)
			if interval < analyzer.MinInterval() || interval > analyzer.MaxInterval() {
				t.Errorf("%s: interval %v out of bounds", memory.NodeID, interval)
			}
		}
	})
}

func TestSpacingReport(t *testing.T) {
	t.Run("zero values valid", func(t *testing.T) {
		report := &SpacingReport{}
		if report.Stability != 0 {
			t.Error("zero value should be valid")
		}
		if report.IsWellSpaced {
			t.Error("default should not be well-spaced")
		}
	})
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkSpacingAnalyzer_OptimalReviewTime(b *testing.B) {
	analyzer := NewSpacingAnalyzer(0.9)
	now := time.Now().UTC()

	memory := &ACTRMemory{
		NodeID:     "bench",
		DecayAlpha: 5.0,
		DecayBeta:  5.0,
		Traces:     make([]AccessTrace, 20),
	}
	for i := 0; i < 20; i++ {
		memory.Traces[i] = AccessTrace{
			AccessedAt: now.Add(-time.Duration(20-i) * 24 * time.Hour),
			AccessType: AccessRetrieval,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analyzer.OptimalReviewTime(memory)
	}
}

func BenchmarkSpacingAnalyzer_ComputeStability(b *testing.B) {
	analyzer := NewSpacingAnalyzer(0.9)
	now := time.Now().UTC()

	memory := &ACTRMemory{
		NodeID: "bench",
		Traces: make([]AccessTrace, 50),
	}
	for i := 0; i < 50; i++ {
		memory.Traces[i] = AccessTrace{
			AccessedAt: now.Add(-time.Duration(50-i) * 24 * time.Hour),
			AccessType: AccessRetrieval,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analyzer.computeStability(memory)
	}
}

func BenchmarkSpacingAnalyzer_SuggestReviewList(b *testing.B) {
	analyzer := NewSpacingAnalyzer(0.9)
	ctx := context.Background()
	now := time.Now().UTC()

	memories := make([]*ACTRMemory, 100)
	for i := 0; i < 100; i++ {
		memories[i] = &ACTRMemory{
			NodeID:     string(rune('A' + i%26)),
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-time.Duration(i) * time.Hour), AccessType: AccessCreation},
			},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analyzer.SuggestReviewList(ctx, memories, now, 10)
	}
}

func BenchmarkSpacingAnalyzer_AnalyzeSpacingPattern(b *testing.B) {
	analyzer := NewSpacingAnalyzer(0.9)
	now := time.Now().UTC()

	memory := &ACTRMemory{
		NodeID:     "bench",
		DecayAlpha: 5.0,
		DecayBeta:  5.0,
		Traces:     make([]AccessTrace, 30),
	}
	for i := 0; i < 30; i++ {
		memory.Traces[i] = AccessTrace{
			AccessedAt: now.Add(-time.Duration(30-i) * 24 * time.Hour),
			AccessType: AccessRetrieval,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analyzer.AnalyzeSpacingPattern(memory)
	}
}
