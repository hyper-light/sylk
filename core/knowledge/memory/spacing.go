// Package memory provides ACT-R based memory management for adaptive retrieval.
// MD.5.1 SpacingAnalyzer implementation for spaced repetition analysis.
package memory

import (
	"context"
	"math"
	"sort"
	"time"
)

// =============================================================================
// MD.5.1 SpacingAnalyzer
// =============================================================================

const (
	// DefaultTargetRetention is the default target retention rate (90%).
	DefaultTargetRetention = 0.9

	// DefaultMinInterval is the minimum review interval.
	DefaultMinInterval = 1 * time.Hour

	// DefaultMaxInterval is the maximum review interval.
	DefaultMaxInterval = 30 * 24 * time.Hour // 30 days
)

// Pimsleur graduated intervals for code context (modified from original).
// These intervals are used as base values before adjustment.
var pimsleurIntervals = []time.Duration{
	1 * time.Hour,      // Initial
	24 * time.Hour,     // After 1 review (1 day)
	3 * 24 * time.Hour, // After 2 reviews (3 days)
	7 * 24 * time.Hour, // After 3 reviews (1 week)
	14 * 24 * time.Hour, // After 4 reviews (2 weeks)
	30 * 24 * time.Hour, // After 5+ reviews (1 month)
}

// SpacingAnalyzer analyzes memory access patterns and recommends optimal
// review intervals based on Pimsleur graduated interval recall principles.
// It considers activation levels, spacing stability, and target retention
// to compute personalized review schedules.
type SpacingAnalyzer struct {
	targetRetention float64
	minInterval     time.Duration
	maxInterval     time.Duration
}

// SpacingReport contains the results of spacing pattern analysis for a memory.
type SpacingReport struct {
	// Stability measures how well-spaced the access history is (0-1).
	// Higher values indicate more optimal spacing patterns.
	Stability float64

	// AverageInterval is the average time between consecutive accesses.
	AverageInterval time.Duration

	// LongestGap is the longest time between any two consecutive accesses.
	LongestGap time.Duration

	// ShortestGap is the shortest time between any two consecutive accesses.
	ShortestGap time.Duration

	// IsWellSpaced indicates whether the memory has good spacing patterns.
	IsWellSpaced bool

	// RecommendedNextReview is the suggested time for the next review.
	RecommendedNextReview time.Time
}

// NewSpacingAnalyzer creates a new SpacingAnalyzer with the specified target retention.
// If targetRetention is <= 0 or > 1, DefaultTargetRetention (0.9) is used.
func NewSpacingAnalyzer(targetRetention float64) *SpacingAnalyzer {
	if targetRetention <= 0 || targetRetention > 1 {
		targetRetention = DefaultTargetRetention
	}

	return &SpacingAnalyzer{
		targetRetention: targetRetention,
		minInterval:     DefaultMinInterval,
		maxInterval:     DefaultMaxInterval,
	}
}

// NewSpacingAnalyzerWithIntervals creates a SpacingAnalyzer with custom interval bounds.
func NewSpacingAnalyzerWithIntervals(targetRetention float64, minInterval, maxInterval time.Duration) *SpacingAnalyzer {
	analyzer := NewSpacingAnalyzer(targetRetention)

	if minInterval > 0 {
		analyzer.minInterval = minInterval
	}
	if maxInterval > minInterval {
		analyzer.maxInterval = maxInterval
	}

	return analyzer
}

// OptimalReviewTime calculates when this memory should next be accessed
// to maintain the target retention rate. The interval is based on:
// 1. Pimsleur graduated intervals (base)
// 2. Current activation level (higher activation -> longer interval)
// 3. Spacing stability (better spacing -> longer interval)
// 4. Target retention (higher target -> shorter interval)
//
// Formula:
//   baseInterval = pimsleurInterval(reviewCount)
//   stabilityFactor = 0.5 + 0.5 * stability  // 0.5 to 1.0
//   activationFactor = sigmoid(activation)    // 0 to 1
//   retentionFactor = 1 / targetRetention     // e.g., 1.11 for 90%
//   optimalInterval = baseInterval * stabilityFactor * activationFactor * retentionFactor
func (s *SpacingAnalyzer) OptimalReviewTime(memory *ACTRMemory) time.Duration {
	if memory == nil || len(memory.Traces) == 0 {
		return s.minInterval
	}

	// Get base interval from Pimsleur schedule based on review count
	reviewCount := len(memory.Traces)
	baseInterval := s.pimsleurInterval(reviewCount)

	// Compute stability factor (0.5 to 1.0)
	stability := s.computeStability(memory)
	stabilityFactor := 0.5 + 0.5*stability

	// Compute activation factor using sigmoid
	now := time.Now().UTC()
	activation := memory.Activation(now)
	activationFactor := s.sigmoid(activation)

	// Compute retention factor (inverse of target retention)
	retentionFactor := 1.0 / s.targetRetention

	// Calculate optimal interval
	optimalNanos := float64(baseInterval.Nanoseconds()) * stabilityFactor * activationFactor * retentionFactor
	optimalInterval := time.Duration(optimalNanos)

	// Clamp to min/max bounds
	if optimalInterval < s.minInterval {
		optimalInterval = s.minInterval
	}
	if optimalInterval > s.maxInterval {
		optimalInterval = s.maxInterval
	}

	return optimalInterval
}

// pimsleurInterval returns the base interval for the given review count.
func (s *SpacingAnalyzer) pimsleurInterval(reviewCount int) time.Duration {
	if reviewCount <= 0 {
		return pimsleurIntervals[0]
	}

	// reviewCount is 1-indexed for interval lookup
	// After 1 trace, we're looking for interval to 2nd review
	idx := reviewCount - 1
	if idx >= len(pimsleurIntervals) {
		idx = len(pimsleurIntervals) - 1
	}

	return pimsleurIntervals[idx]
}

// sigmoid computes a sigmoid function to normalize activation to [0, 1].
// Uses a scaled sigmoid: 1 / (1 + exp(-activation/2))
func (s *SpacingAnalyzer) sigmoid(activation float64) float64 {
	// Clamp extreme values to avoid overflow
	if activation > 20 {
		return 1.0
	}
	if activation < -20 {
		return 0.0
	}

	// Scale activation for better sensitivity
	return 1.0 / (1.0 + math.Exp(-activation/2.0))
}

// computeStability measures how well-spaced the access history is.
// Well-spaced accesses (1h, 1d, 1w intervals) produce higher stability.
// Massed accesses (all within 1 hour) produce lower stability.
//
// Stability is computed by comparing actual intervals to ideal Pimsleur intervals.
// A perfect match gives stability of 1.0.
func (s *SpacingAnalyzer) computeStability(memory *ACTRMemory) float64 {
	if memory == nil || len(memory.Traces) < 2 {
		return 0.5 // Default stability for insufficient data
	}

	// Compute actual intervals between accesses
	intervals := make([]time.Duration, 0, len(memory.Traces)-1)
	for i := 1; i < len(memory.Traces); i++ {
		interval := memory.Traces[i].AccessedAt.Sub(memory.Traces[i-1].AccessedAt)
		if interval < 0 {
			interval = 0 // Handle out-of-order traces
		}
		intervals = append(intervals, interval)
	}

	if len(intervals) == 0 {
		return 0.5
	}

	// Compare each interval to the expected Pimsleur interval
	totalRatio := 0.0
	for i, interval := range intervals {
		expected := s.pimsleurInterval(i + 1) // i+1 because we're looking at interval after i-th access
		if expected == 0 {
			continue
		}

		// Compute ratio of actual to expected (capped at 2.0 for very long gaps)
		ratio := float64(interval) / float64(expected)
		if ratio > 2.0 {
			ratio = 2.0
		}

		// Convert ratio to similarity score
		// ratio = 1.0 is perfect, deviations reduce score
		// Use exp(-|ln(ratio)|) for symmetric penalty around 1.0
		if ratio <= 0 {
			totalRatio += 0 // Zero interval is bad
		} else {
			similarity := math.Exp(-math.Abs(math.Log(ratio)))
			totalRatio += similarity
		}
	}

	stability := totalRatio / float64(len(intervals))

	// Bonus for longer histories (more data = more confidence in stability)
	historyBonus := 1.0 - math.Exp(-float64(len(intervals))/10.0)
	stability = stability*0.8 + historyBonus*0.2

	// Clamp to [0, 1]
	if stability < 0 {
		stability = 0
	}
	if stability > 1 {
		stability = 1
	}

	return stability
}

// SuggestReviewList returns memories that should be reviewed soon, sorted by urgency.
// Urgency is determined by how overdue a memory is for its optimal review time.
// Memories whose optimal review time has passed are prioritized.
func (s *SpacingAnalyzer) SuggestReviewList(ctx context.Context, memories []*ACTRMemory, now time.Time, limit int) []*ACTRMemory {
	if len(memories) == 0 {
		return nil
	}

	// Compute urgency scores for each memory
	type urgencyItem struct {
		memory  *ACTRMemory
		urgency float64 // Higher = more urgent
	}

	items := make([]urgencyItem, 0, len(memories))
	for _, memory := range memories {
		if memory == nil || len(memory.Traces) == 0 {
			continue
		}

		// Get last access time
		lastAccess := memory.Traces[len(memory.Traces)-1].AccessedAt

		// Calculate optimal interval from last access
		optimalInterval := s.OptimalReviewTime(memory)

		// Compute when the review should happen
		shouldReviewAt := lastAccess.Add(optimalInterval)

		// Compute urgency based on how far past the review time we are
		// Positive urgency = overdue, negative = not yet due
		timeSinceShould := now.Sub(shouldReviewAt)
		urgency := timeSinceShould.Hours() / optimalInterval.Hours()

		items = append(items, urgencyItem{
			memory:  memory,
			urgency: urgency,
		})
	}

	// Sort by urgency (highest first = most overdue)
	sort.Slice(items, func(i, j int) bool {
		return items[i].urgency > items[j].urgency
	})

	// Take top 'limit' items
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}

	result := make([]*ACTRMemory, limit)
	for i := 0; i < limit; i++ {
		result[i] = items[i].memory
	}

	return result
}

// AnalyzeSpacingPattern generates a comprehensive spacing report for a memory.
// The report includes stability metrics, interval statistics, and recommendations.
func (s *SpacingAnalyzer) AnalyzeSpacingPattern(memory *ACTRMemory) *SpacingReport {
	if memory == nil {
		return &SpacingReport{
			Stability:             0,
			AverageInterval:       0,
			LongestGap:            0,
			ShortestGap:           0,
			IsWellSpaced:          false,
			RecommendedNextReview: time.Now().UTC().Add(s.minInterval),
		}
	}

	if len(memory.Traces) == 0 {
		return &SpacingReport{
			Stability:             0,
			AverageInterval:       0,
			LongestGap:            0,
			ShortestGap:           0,
			IsWellSpaced:          false,
			RecommendedNextReview: time.Now().UTC().Add(s.minInterval),
		}
	}

	// Compute stability
	stability := s.computeStability(memory)

	// Compute interval statistics
	var totalInterval time.Duration
	var longestGap time.Duration
	shortestGap := time.Duration(math.MaxInt64)
	intervalCount := 0

	for i := 1; i < len(memory.Traces); i++ {
		interval := memory.Traces[i].AccessedAt.Sub(memory.Traces[i-1].AccessedAt)
		if interval < 0 {
			interval = 0
		}

		totalInterval += interval
		intervalCount++

		if interval > longestGap {
			longestGap = interval
		}
		if interval < shortestGap {
			shortestGap = interval
		}
	}

	var averageInterval time.Duration
	if intervalCount > 0 {
		averageInterval = time.Duration(int64(totalInterval) / int64(intervalCount))
	}

	// Handle case with only one trace (no intervals)
	if shortestGap == time.Duration(math.MaxInt64) {
		shortestGap = 0
	}

	// Determine if well-spaced
	// Well-spaced if stability >= 0.6 and has at least 2 accesses
	isWellSpaced := stability >= 0.6 && len(memory.Traces) >= 2

	// Compute recommended next review time
	lastAccess := memory.Traces[len(memory.Traces)-1].AccessedAt
	optimalInterval := s.OptimalReviewTime(memory)
	recommendedNextReview := lastAccess.Add(optimalInterval)

	return &SpacingReport{
		Stability:             stability,
		AverageInterval:       averageInterval,
		LongestGap:            longestGap,
		ShortestGap:           shortestGap,
		IsWellSpaced:          isWellSpaced,
		RecommendedNextReview: recommendedNextReview,
	}
}

// TargetRetention returns the current target retention rate.
func (s *SpacingAnalyzer) TargetRetention() float64 {
	return s.targetRetention
}

// MinInterval returns the minimum review interval.
func (s *SpacingAnalyzer) MinInterval() time.Duration {
	return s.minInterval
}

// MaxInterval returns the maximum review interval.
func (s *SpacingAnalyzer) MaxInterval() time.Duration {
	return s.maxInterval
}

// SetTargetRetention updates the target retention rate.
// Values <= 0 or > 1 are ignored.
func (s *SpacingAnalyzer) SetTargetRetention(retention float64) {
	if retention > 0 && retention <= 1 {
		s.targetRetention = retention
	}
}

// SetMinInterval updates the minimum review interval.
// Values <= 0 are ignored.
func (s *SpacingAnalyzer) SetMinInterval(interval time.Duration) {
	if interval > 0 {
		s.minInterval = interval
	}
}

// SetMaxInterval updates the maximum review interval.
// Values <= minInterval are ignored.
func (s *SpacingAnalyzer) SetMaxInterval(interval time.Duration) {
	if interval > s.minInterval {
		s.maxInterval = interval
	}
}
