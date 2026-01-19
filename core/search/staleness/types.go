// Package staleness provides staleness detection for the Sylk Document Search System.
// It tracks when indexed content becomes out of sync with the filesystem and uses
// multiple detection strategies for efficient staleness identification.
package staleness

import (
	"errors"
	"time"
)

// =============================================================================
// Errors
// =============================================================================

// Common errors returned by staleness detection operations.
var (
	// ErrDetectorClosed indicates the detector has been closed.
	ErrDetectorClosed = errors.New("staleness detector is closed")

	// ErrDetectionFailed indicates staleness detection failed.
	ErrDetectionFailed = errors.New("staleness detection failed")

	// ErrInvalidStrategy indicates an invalid detection strategy.
	ErrInvalidStrategy = errors.New("invalid detection strategy")

	// ErrNoStrategiesAvailable indicates no detection strategies are available.
	ErrNoStrategiesAvailable = errors.New("no detection strategies available")

	// ErrCMTUnavailable indicates CMT is not available for detection.
	ErrCMTUnavailable = errors.New("CMT is not available")

	// ErrCMTNotAvailable is an alias for ErrCMTUnavailable.
	ErrCMTNotAvailable = ErrCMTUnavailable

	// ErrGitUnavailable indicates Git is not available for detection.
	ErrGitUnavailable = errors.New("git is not available")

	// ErrContextCanceled indicates the operation was canceled.
	ErrContextCanceled = errors.New("operation canceled")

	// ErrNoFilesToSample indicates there are no files to sample.
	ErrNoFilesToSample = errors.New("no files to sample")

	// ErrSampleSizeInvalid indicates an invalid sample size.
	ErrSampleSizeInvalid = errors.New("sample size must be positive")

	// ErrBaseDirEmpty indicates the base directory is empty.
	ErrBaseDirEmpty = errors.New("base directory cannot be empty")
)

// =============================================================================
// StalenessLevel
// =============================================================================

// StalenessLevel indicates the degree of staleness detected.
// Lower values indicate fresher content.
type StalenessLevel int

const (
	// Fresh indicates the index is up-to-date with the filesystem.
	Fresh StalenessLevel = iota

	// SlightlyStale indicates minor staleness (< 5 files or recent changes).
	SlightlyStale

	// ModeratelyStale indicates moderate staleness (5-20 files or older changes).
	ModeratelyStale

	// SeverelyStale indicates severe staleness (> 20 files or very old changes).
	SeverelyStale

	// Unknown indicates staleness could not be determined.
	Unknown
)

// String returns a human-readable name for the staleness level.
func (l StalenessLevel) String() string {
	switch l {
	case Fresh:
		return "fresh"
	case SlightlyStale:
		return "slightly_stale"
	case ModeratelyStale:
		return "moderately_stale"
	case SeverelyStale:
		return "severely_stale"
	case Unknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// IsFresh returns true if the level indicates fresh content.
func (l StalenessLevel) IsFresh() bool {
	return l == Fresh
}

// IsStale returns true if any staleness is detected.
func (l StalenessLevel) IsStale() bool {
	return l == SlightlyStale || l == ModeratelyStale || l == SeverelyStale
}

// NeedsReindex returns true if staleness warrants a reindex.
func (l StalenessLevel) NeedsReindex() bool {
	return l == ModeratelyStale || l == SeverelyStale
}

// =============================================================================
// DetectionStrategy
// =============================================================================

// DetectionStrategy identifies the method used for staleness detection.
// Strategies are ordered by preference (fastest/most accurate first).
type DetectionStrategy int

const (
	// CMTRoot uses CMT root hash comparison (O(1), instant).
	CMTRoot DetectionStrategy = iota

	// GitCommit uses Git HEAD comparison (fast, catches committed changes).
	GitCommit

	// MtimeSample uses filesystem mtime sampling (periodic, uncommitted).
	MtimeSample

	// ContentHash uses content hash spot-checking (deep verification).
	ContentHash
)

// String returns a human-readable name for the detection strategy.
func (s DetectionStrategy) String() string {
	switch s {
	case CMTRoot:
		return "cmt_root"
	case GitCommit:
		return "git_commit"
	case MtimeSample:
		return "mtime_sample"
	case ContentHash:
		return "content_hash"
	default:
		return "unknown"
	}
}

// Priority returns the strategy priority (lower is higher priority).
func (s DetectionStrategy) Priority() int {
	return int(s)
}

// =============================================================================
// StaleFile
// =============================================================================

// StaleFile represents a file that has become stale.
type StaleFile struct {
	// Path is the absolute path to the stale file.
	Path string

	// Reason describes why the file is considered stale.
	Reason string

	// DetectedAt is when the staleness was detected.
	DetectedAt time.Time

	// LastIndexed is when the file was last indexed (zero if unknown).
	LastIndexed time.Time

	// CurrentMtime is the current file modification time (zero if unknown).
	CurrentMtime time.Time
}

// Age returns the duration since the file was last indexed.
// Returns zero if LastIndexed is not set.
func (f *StaleFile) Age() time.Duration {
	if f.LastIndexed.IsZero() {
		return 0
	}
	return time.Since(f.LastIndexed)
}

// =============================================================================
// DetectionResult
// =============================================================================

// DetectionResult contains the outcome of a single staleness detection check.
type DetectionResult struct {
	// Level indicates the detected staleness level.
	Level StalenessLevel

	// Strategy indicates which detection method produced this result.
	Strategy DetectionStrategy

	// StaleFiles contains paths of files detected as stale.
	StaleFiles []string

	// StaleSince is the estimated time since the index became stale.
	StaleSince time.Duration

	// Confidence is a value from 0.0 to 1.0 indicating detection confidence.
	Confidence float64

	// Timestamp is when the detection was performed.
	Timestamp time.Time

	// Details contains additional strategy-specific information.
	Details string
}

// IsFresh returns true if the result indicates freshness.
func (r *DetectionResult) IsFresh() bool {
	return r.Level == Fresh
}

// IsStale returns true if the result indicates any staleness.
func (r *DetectionResult) IsStale() bool {
	return r.Level != Fresh && r.Level != Unknown
}

// =============================================================================
// StalenessReport
// =============================================================================

// StalenessReport contains the results of staleness detection.
type StalenessReport struct {
	// Level indicates the overall staleness level.
	Level StalenessLevel

	// StaleFiles contains the list of detected stale files.
	// May be empty even when stale (e.g., if only root hash differs).
	StaleFiles []StaleFile

	// StaleSince is the earliest detected staleness time.
	// Zero if fresh or unknown.
	StaleSince time.Time

	// Confidence is the confidence level of the detection (0.0-1.0).
	// Higher values indicate more certain detection.
	Confidence float64

	// Strategy is the primary detection strategy that produced this report.
	Strategy DetectionStrategy

	// DetectedAt is when this detection was performed.
	DetectedAt time.Time

	// Duration is how long the detection took.
	Duration time.Duration

	// EarlyExit indicates detection stopped early due to confirmed freshness.
	EarlyExit bool

	// Results contains individual detection results from each strategy.
	Results []*DetectionResult
}

// StaleFileCount returns the number of stale files detected.
func (r *StalenessReport) StaleFileCount() int {
	return len(r.StaleFiles)
}

// IsFresh returns true if the report indicates fresh content.
func (r *StalenessReport) IsFresh() bool {
	return r.Level.IsFresh()
}

// NeedsReindex returns true if the report indicates reindexing is needed.
func (r *StalenessReport) NeedsReindex() bool {
	return r.Level.NeedsReindex()
}

// AddStaleFile adds a stale file to the report.
func (r *StalenessReport) AddStaleFile(file StaleFile) {
	r.StaleFiles = append(r.StaleFiles, file)
}

// AddResult adds a detection result to the report.
func (r *StalenessReport) AddResult(result *DetectionResult) {
	r.Results = append(r.Results, result)
}

// =============================================================================
// StrategyResult
// =============================================================================

// StrategyResult contains the result from a single detection strategy.
type StrategyResult struct {
	// Strategy is the detection strategy used.
	Strategy DetectionStrategy

	// Fresh indicates if this strategy detected freshness.
	Fresh bool

	// StaleFiles contains files detected as stale by this strategy.
	StaleFiles []StaleFile

	// Confidence is how confident this strategy is in its result.
	Confidence float64

	// Duration is how long this strategy took.
	Duration time.Duration

	// Error is set if the strategy failed.
	Error error
}

// Succeeded returns true if the strategy completed without error.
func (r *StrategyResult) Succeeded() bool {
	return r.Error == nil
}

// =============================================================================
// DetectorConfig
// =============================================================================

// DetectorConfig configures the hybrid staleness detector.
type DetectorConfig struct {
	// EnableCMT enables CMT root hash comparison.
	EnableCMT bool

	// EnableGit enables Git HEAD comparison.
	EnableGit bool

	// EnableMtime enables mtime sampling.
	EnableMtime bool

	// EnableContentHash enables content hash verification.
	EnableContentHash bool

	// MtimeSampleSize is the number of files to sample for mtime checks.
	MtimeSampleSize int

	// ContentHashSampleSize is the number of files to spot-check.
	ContentHashSampleSize int

	// StaleThresholdSlightly is the file count threshold for SlightlyStale.
	StaleThresholdSlightly int

	// StaleThresholdModerate is the file count threshold for ModeratelyStale.
	StaleThresholdModerate int

	// EarlyExitOnFresh enables early exit when freshness is confirmed.
	EarlyExitOnFresh bool

	// BaseDir is the base directory for file operations.
	BaseDir string
}

// DefaultConfig returns a DetectorConfig with sensible defaults.
func DefaultConfig() DetectorConfig {
	return DetectorConfig{
		EnableCMT:              true,
		EnableGit:              true,
		EnableMtime:            true,
		EnableContentHash:      true,
		MtimeSampleSize:        100,
		ContentHashSampleSize:  10,
		StaleThresholdSlightly: 5,
		StaleThresholdModerate: 20,
		EarlyExitOnFresh:       true,
	}
}

// Validate checks the configuration for validity.
func (c *DetectorConfig) Validate() error {
	if !c.EnableCMT && !c.EnableGit && !c.EnableMtime && !c.EnableContentHash {
		return ErrNoStrategiesAvailable
	}
	return nil
}

// =============================================================================
// Helper Functions
// =============================================================================

// ComputeStalenessLevel determines the staleness level based on file count.
func ComputeStalenessLevel(staleCount, thresholdSlightly, thresholdModerate int) StalenessLevel {
	if staleCount == 0 {
		return Fresh
	}
	if staleCount < thresholdSlightly {
		return SlightlyStale
	}
	if staleCount < thresholdModerate {
		return ModeratelyStale
	}
	return SeverelyStale
}

// NewFreshReport creates a fresh staleness report.
func NewFreshReport(strategy DetectionStrategy, duration time.Duration) *StalenessReport {
	return &StalenessReport{
		Level:      Fresh,
		StaleFiles: []StaleFile{},
		Confidence: 1.0,
		Strategy:   strategy,
		DetectedAt: time.Now(),
		Duration:   duration,
		EarlyExit:  false,
		Results:    []*DetectionResult{},
	}
}

// NewStaleReport creates a stale report with the given files.
func NewStaleReport(
	level StalenessLevel,
	files []StaleFile,
	staleSince time.Time,
	confidence float64,
	strategy DetectionStrategy,
	duration time.Duration,
) *StalenessReport {
	return &StalenessReport{
		Level:      level,
		StaleFiles: files,
		StaleSince: staleSince,
		Confidence: confidence,
		Strategy:   strategy,
		DetectedAt: time.Now(),
		Duration:   duration,
		EarlyExit:  false,
		Results:    []*DetectionResult{},
	}
}
