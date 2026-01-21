package quantization

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// =============================================================================
// Remove-Birth Centroid Adaptation Types
// =============================================================================
//
// Remove-Birth enables continuous codebook evolution without full retraining.
// From "A survey on indexing methods for nearest neighbor search in high
// dimensional data" (arXiv 2306.12574).
//
// Mechanism:
//   - Track utilization (assignment count) per centroid
//   - Death: Remove underutilized centroids (below threshold)
//   - Birth: Split overutilized centroids (above threshold)
//   - Maintains codebook quality under distribution shift

// =============================================================================
// CentroidID Type
// =============================================================================

// CentroidID uniquely identifies a centroid within a codebook.
type CentroidID struct {
	// CodebookType identifies which codebook (e.g., "global", "lopq_123").
	CodebookType string

	// SubspaceIndex is the subspace this centroid belongs to (0 to M-1).
	SubspaceIndex int

	// CentroidIndex is the index within the subspace (0 to K-1).
	CentroidIndex int
}

// String returns a string representation.
func (id CentroidID) String() string {
	return fmt.Sprintf("%s:sub%d:c%d", id.CodebookType, id.SubspaceIndex, id.CentroidIndex)
}

// Equal returns true if both IDs are identical.
func (id CentroidID) Equal(other CentroidID) bool {
	return id.CodebookType == other.CodebookType &&
		id.SubspaceIndex == other.SubspaceIndex &&
		id.CentroidIndex == other.CentroidIndex
}

// =============================================================================
// CentroidUtilization Type
// =============================================================================

// CentroidUtilization tracks how many vectors are assigned to a centroid.
type CentroidUtilization struct {
	// ID uniquely identifies the centroid.
	ID CentroidID

	// Count is the number of vectors currently assigned to this centroid.
	Count int64

	// LastUpdated is when the count was last modified.
	LastUpdated time.Time
}

// IsDead returns true if utilization is below the death threshold.
func (u CentroidUtilization) IsDead(threshold int64) bool {
	return u.Count < threshold
}

// IsOverloaded returns true if utilization is above the birth threshold.
func (u CentroidUtilization) IsOverloaded(threshold int64) bool {
	return u.Count > threshold
}

// String returns a string representation.
func (u CentroidUtilization) String() string {
	return fmt.Sprintf("Util{%s, Count:%d, Updated:%s}",
		u.ID, u.Count, u.LastUpdated.Format(time.RFC3339))
}

// =============================================================================
// AdaptationEvent Types
// =============================================================================

// AdaptationEventType identifies the type of centroid lifecycle event.
type AdaptationEventType int

const (
	// AdaptationEventDeath indicates a centroid was removed due to low utilization.
	AdaptationEventDeath AdaptationEventType = iota

	// AdaptationEventBirth indicates a centroid was created by splitting.
	AdaptationEventBirth

	// AdaptationEventMerge indicates two centroids were merged.
	AdaptationEventMerge
)

// String returns a string representation.
func (t AdaptationEventType) String() string {
	switch t {
	case AdaptationEventDeath:
		return "Death"
	case AdaptationEventBirth:
		return "Birth"
	case AdaptationEventMerge:
		return "Merge"
	default:
		return fmt.Sprintf("Unknown(%d)", t)
	}
}

// =============================================================================
// DeathEvent
// =============================================================================

// DeathEvent records when a centroid was removed due to low utilization.
type DeathEvent struct {
	// Centroid is the ID of the removed centroid.
	Centroid CentroidID

	// FinalCount is the utilization count when removed.
	FinalCount int64

	// Threshold is the death threshold that triggered removal.
	Threshold int64

	// OccurredAt is when the death occurred.
	OccurredAt time.Time

	// ReplacementCentroid is the ID of the centroid that absorbed its vectors.
	// Empty if vectors were redistributed across multiple centroids.
	ReplacementCentroid CentroidID
}

// String returns a string representation.
func (e DeathEvent) String() string {
	return fmt.Sprintf("Death{%s, Count:%d, Thresh:%d, At:%s}",
		e.Centroid, e.FinalCount, e.Threshold, e.OccurredAt.Format(time.RFC3339))
}

// =============================================================================
// BirthEvent
// =============================================================================

// BirthEvent records when a centroid was created by splitting an overloaded one.
type BirthEvent struct {
	// ParentCentroid is the ID of the centroid that was split.
	ParentCentroid CentroidID

	// ChildCentroid is the ID of the newly created centroid.
	ChildCentroid CentroidID

	// ParentCountBefore is the parent's utilization before splitting.
	ParentCountBefore int64

	// Threshold is the birth threshold that triggered splitting.
	Threshold int64

	// OccurredAt is when the birth occurred.
	OccurredAt time.Time
}

// String returns a string representation.
func (e BirthEvent) String() string {
	return fmt.Sprintf("Birth{Parent:%s, Child:%s, ParentCount:%d, At:%s}",
		e.ParentCentroid, e.ChildCentroid, e.ParentCountBefore, e.OccurredAt.Format(time.RFC3339))
}

// =============================================================================
// AdaptationConfig
// =============================================================================

// AdaptationConfig configures the Remove-Birth centroid adaptation system.
type AdaptationConfig struct {
	// DeathThreshold is the minimum utilization before a centroid is removed.
	// Centroids with count < DeathThreshold are candidates for removal.
	// Default: 5 (very low utilization indicates poor fit).
	DeathThreshold int64

	// BirthThreshold is the maximum utilization before a centroid is split.
	// Centroids with count > BirthThreshold are candidates for splitting.
	// Default: 10000 (indicates too many vectors in one cluster).
	BirthThreshold int64

	// CheckInterval is how often to scan for adaptation candidates.
	// Default: 1 minute.
	CheckInterval time.Duration

	// MinCentroidsPerSubspace prevents subspaces from becoming too sparse.
	// Default: 16 (minimum centroids even after deaths).
	MinCentroidsPerSubspace int

	// MaxCentroidsPerSubspace prevents subspaces from growing too large.
	// Default: 256 (uint8 encoding limit).
	MaxCentroidsPerSubspace int

	// Enabled controls whether adaptation is active.
	// Default: true.
	Enabled bool
}

// DefaultAdaptationConfig returns an AdaptationConfig with conservative default values.
//
// This function provides a fallback configuration for when no cluster data is available
// to derive thresholds from actual utilization statistics. The default values are
// intentionally conservative:
//
//   - DeathThreshold (5): Low enough to only remove truly unused centroids
//   - BirthThreshold (10000): High enough to prevent excessive splitting
//   - MinCentroidsPerSubspace (16): Maintains reasonable cluster granularity
//   - MaxCentroidsPerSubspace (256): Matches uint8 encoding limit
//
// For production use with existing data, prefer DeriveAdaptationConfig() which
// computes thresholds from actual cluster size statistics.
func DefaultAdaptationConfig() AdaptationConfig {
	return AdaptationConfig{
		DeathThreshold:          5,
		BirthThreshold:          10000,
		CheckInterval:           time.Minute,
		MinCentroidsPerSubspace: 16,
		MaxCentroidsPerSubspace: 256,
		Enabled:                 true,
	}
}

// Validate checks that the configuration is valid.
func (c AdaptationConfig) Validate() error {
	if c.DeathThreshold < 0 {
		return ErrAdaptationInvalidDeathThreshold
	}
	if c.BirthThreshold <= c.DeathThreshold {
		return ErrAdaptationInvalidBirthThreshold
	}
	if c.CheckInterval < time.Second {
		return ErrAdaptationInvalidCheckInterval
	}
	if c.MinCentroidsPerSubspace <= 0 {
		return ErrAdaptationInvalidMinCentroids
	}
	if c.MaxCentroidsPerSubspace < c.MinCentroidsPerSubspace {
		return ErrAdaptationInvalidMaxCentroids
	}
	return nil
}

// percentile computes the p-th percentile of the given data using linear interpolation.
// Parameter p must be in the range [0.0, 1.0] where 0.0 is the minimum and 1.0 is the maximum.
// Returns 0 if data is empty. The input slice is not modified.
func percentile(data []int64, p float64) int64 {
	n := len(data)
	if n == 0 {
		return 0
	}

	sorted := make([]int64, n)
	copy(sorted, data)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	if n == 1 {
		return sorted[0]
	}

	// Compute the rank using the "nearest rank" method
	// rank = p * (n - 1), giving index into sorted array
	rank := p * float64(n-1)
	lower := int(rank)
	upper := lower + 1

	if upper >= n {
		return sorted[n-1]
	}

	// Linear interpolation between lower and upper ranks
	fraction := rank - float64(lower)
	return sorted[lower] + int64(fraction*float64(sorted[upper]-sorted[lower]))
}

// DeriveAdaptationConfig computes an AdaptationConfig from actual cluster size statistics.
//
// This function derives thresholds from the distribution of cluster sizes, providing
// data-driven values rather than hardcoded defaults. The derivation logic:
//
//   - DeathThreshold: 5th percentile of cluster sizes (minimum 1)
//     Clusters smaller than this are statistical outliers on the low end.
//
//   - BirthThreshold: 2x the 95th percentile of cluster sizes
//     Clusters larger than this are significantly oversized and should split.
//
//   - MinCentroidsPerSubspace: Count of non-empty clusters (minimum 8)
//     Preserves the current active cluster count as a floor.
//
// Edge cases:
//   - Empty slice: Returns DefaultAdaptationConfig()
//   - Single value: DeathThreshold=1, BirthThreshold=2*value
//   - All same values: DeathThreshold=value (min 1), BirthThreshold=2*value
//
// The function is deterministic: identical input produces identical output.
func DeriveAdaptationConfig(clusterSizes []int64) AdaptationConfig {
	if len(clusterSizes) == 0 {
		return DefaultAdaptationConfig()
	}

	// Count non-empty clusters for MinCentroidsPerSubspace
	nonEmptyCount := 0
	for _, size := range clusterSizes {
		if size > 0 {
			nonEmptyCount++
		}
	}

	// Compute percentile-based thresholds
	p5 := percentile(clusterSizes, 0.05)
	p95 := percentile(clusterSizes, 0.95)

	// DeathThreshold: 5th percentile, minimum 1
	deathThreshold := p5
	if deathThreshold < 1 {
		deathThreshold = 1
	}

	// BirthThreshold: 2x the 95th percentile
	// Ensure it's always greater than death threshold
	birthThreshold := p95 * 2
	if birthThreshold <= deathThreshold {
		birthThreshold = deathThreshold + 1
	}

	// MinCentroidsPerSubspace: non-empty count with floor of 8
	minCentroids := nonEmptyCount
	if minCentroids < 8 {
		minCentroids = 8
	}

	return AdaptationConfig{
		DeathThreshold:          deathThreshold,
		BirthThreshold:          birthThreshold,
		CheckInterval:           time.Minute,
		MinCentroidsPerSubspace: minCentroids,
		MaxCentroidsPerSubspace: 256,
		Enabled:                 true,
	}
}

// String returns a string representation.
func (c AdaptationConfig) String() string {
	return fmt.Sprintf("AdaptationConfig{Death:%d, Birth:%d, Interval:%s, Enabled:%t}",
		c.DeathThreshold, c.BirthThreshold, c.CheckInterval, c.Enabled)
}

// =============================================================================
// AdaptationTracker Interface
// =============================================================================

// AdaptationTracker manages centroid utilization and lifecycle events.
// Implementations are safe for concurrent use.
type AdaptationTracker interface {
	// Increment increases utilization count for a centroid.
	Increment(id CentroidID) error

	// Decrement decreases utilization count for a centroid.
	Decrement(id CentroidID) error

	// GetUtilization returns current utilization for a centroid.
	GetUtilization(id CentroidID) (CentroidUtilization, error)

	// GetDeadCentroids returns centroids below the death threshold.
	GetDeadCentroids(codebookType string) ([]CentroidID, error)

	// GetOverloadedCentroids returns centroids above the birth threshold.
	GetOverloadedCentroids(codebookType string) ([]CentroidID, error)

	// RecordDeath records a centroid death event.
	RecordDeath(event DeathEvent) error

	// RecordBirth records a centroid birth event.
	RecordBirth(event BirthEvent) error

	// GetRecentEvents returns adaptation events within the time window.
	GetRecentEvents(since time.Time) ([]DeathEvent, []BirthEvent, error)

	// GetStats returns adaptation statistics.
	GetStats() AdaptationStats
}

// =============================================================================
// AdaptationStats
// =============================================================================

// AdaptationStats contains statistics about centroid adaptation.
type AdaptationStats struct {
	// TotalCentroids is the total number of tracked centroids.
	TotalCentroids int

	// DeadCentroids is the number currently below death threshold.
	DeadCentroids int

	// OverloadedCentroids is the number currently above birth threshold.
	OverloadedCentroids int

	// TotalDeaths is the cumulative number of centroid deaths.
	TotalDeaths int64

	// TotalBirths is the cumulative number of centroid births.
	TotalBirths int64

	// LastCheckAt is when the last adaptation check ran.
	LastCheckAt time.Time
}

// String returns a string representation.
func (s AdaptationStats) String() string {
	return fmt.Sprintf("AdaptationStats{Total:%d, Dead:%d, Overloaded:%d, Deaths:%d, Births:%d}",
		s.TotalCentroids, s.DeadCentroids, s.OverloadedCentroids, s.TotalDeaths, s.TotalBirths)
}

// =============================================================================
// Adaptation Errors
// =============================================================================

var (
	// ErrAdaptationInvalidDeathThreshold indicates death threshold is negative.
	ErrAdaptationInvalidDeathThreshold = errors.New("adaptation: death threshold cannot be negative")

	// ErrAdaptationInvalidBirthThreshold indicates birth threshold <= death threshold.
	ErrAdaptationInvalidBirthThreshold = errors.New("adaptation: birth threshold must be greater than death threshold")

	// ErrAdaptationInvalidCheckInterval indicates check interval is too short.
	ErrAdaptationInvalidCheckInterval = errors.New("adaptation: check interval must be at least 1 second")

	// ErrAdaptationInvalidMinCentroids indicates min centroids is not positive.
	ErrAdaptationInvalidMinCentroids = errors.New("adaptation: minimum centroids must be positive")

	// ErrAdaptationInvalidMaxCentroids indicates max centroids < min centroids.
	ErrAdaptationInvalidMaxCentroids = errors.New("adaptation: maximum centroids must be >= minimum")

	// ErrAdaptationCentroidNotFound indicates centroid ID doesn't exist.
	ErrAdaptationCentroidNotFound = errors.New("adaptation: centroid not found")

	// ErrAdaptationDisabled indicates adaptation is disabled.
	ErrAdaptationDisabled = errors.New("adaptation: feature is disabled")
)
