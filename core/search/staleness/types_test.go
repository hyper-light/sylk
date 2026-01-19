// Package staleness provides staleness detection for the Sylk Document Search System.
package staleness

import (
	"testing"
	"time"
)

// =============================================================================
// StalenessLevel Tests
// =============================================================================

func TestStalenessLevel_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    StalenessLevel
		expected string
	}{
		{
			name:     "fresh returns correct string",
			level:    Fresh,
			expected: "fresh",
		},
		{
			name:     "slightly stale returns correct string",
			level:    SlightlyStale,
			expected: "slightly_stale",
		},
		{
			name:     "moderately stale returns correct string",
			level:    ModeratelyStale,
			expected: "moderately_stale",
		},
		{
			name:     "severely stale returns correct string",
			level:    SeverelyStale,
			expected: "severely_stale",
		},
		{
			name:     "unknown returns correct string",
			level:    Unknown,
			expected: "unknown",
		},
		{
			name:     "invalid level returns unknown",
			level:    StalenessLevel(99),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.level.String()
			if got != tt.expected {
				t.Errorf("StalenessLevel.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestStalenessLevel_IsFresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    StalenessLevel
		expected bool
	}{
		{
			name:     "fresh returns true",
			level:    Fresh,
			expected: true,
		},
		{
			name:     "slightly stale returns false",
			level:    SlightlyStale,
			expected: false,
		},
		{
			name:     "moderately stale returns false",
			level:    ModeratelyStale,
			expected: false,
		},
		{
			name:     "severely stale returns false",
			level:    SeverelyStale,
			expected: false,
		},
		{
			name:     "unknown returns false",
			level:    Unknown,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.level.IsFresh()
			if got != tt.expected {
				t.Errorf("StalenessLevel.IsFresh() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestStalenessLevel_IsStale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    StalenessLevel
		expected bool
	}{
		{
			name:     "fresh returns false",
			level:    Fresh,
			expected: false,
		},
		{
			name:     "slightly stale returns true",
			level:    SlightlyStale,
			expected: true,
		},
		{
			name:     "moderately stale returns true",
			level:    ModeratelyStale,
			expected: true,
		},
		{
			name:     "severely stale returns true",
			level:    SeverelyStale,
			expected: true,
		},
		{
			name:     "unknown returns false",
			level:    Unknown,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.level.IsStale()
			if got != tt.expected {
				t.Errorf("StalenessLevel.IsStale() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestStalenessLevel_NeedsReindex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    StalenessLevel
		expected bool
	}{
		{
			name:     "fresh returns false",
			level:    Fresh,
			expected: false,
		},
		{
			name:     "slightly stale returns false",
			level:    SlightlyStale,
			expected: false,
		},
		{
			name:     "moderately stale returns true",
			level:    ModeratelyStale,
			expected: true,
		},
		{
			name:     "severely stale returns true",
			level:    SeverelyStale,
			expected: true,
		},
		{
			name:     "unknown returns false",
			level:    Unknown,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.level.NeedsReindex()
			if got != tt.expected {
				t.Errorf("StalenessLevel.NeedsReindex() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// DetectionStrategy Tests
// =============================================================================

func TestDetectionStrategy_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		strategy DetectionStrategy
		expected string
	}{
		{
			name:     "CMTRoot returns correct string",
			strategy: CMTRoot,
			expected: "cmt_root",
		},
		{
			name:     "GitCommit returns correct string",
			strategy: GitCommit,
			expected: "git_commit",
		},
		{
			name:     "MtimeSample returns correct string",
			strategy: MtimeSample,
			expected: "mtime_sample",
		},
		{
			name:     "ContentHash returns correct string",
			strategy: ContentHash,
			expected: "content_hash",
		},
		{
			name:     "invalid strategy returns unknown",
			strategy: DetectionStrategy(99),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.strategy.String()
			if got != tt.expected {
				t.Errorf("DetectionStrategy.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDetectionStrategy_Priority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		strategy DetectionStrategy
		expected int
	}{
		{
			name:     "CMTRoot has priority 0",
			strategy: CMTRoot,
			expected: 0,
		},
		{
			name:     "GitCommit has priority 1",
			strategy: GitCommit,
			expected: 1,
		},
		{
			name:     "MtimeSample has priority 2",
			strategy: MtimeSample,
			expected: 2,
		},
		{
			name:     "ContentHash has priority 3",
			strategy: ContentHash,
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.strategy.Priority()
			if got != tt.expected {
				t.Errorf("DetectionStrategy.Priority() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestDetectionStrategy_OrderedByPriority(t *testing.T) {
	t.Parallel()

	// Verify strategies are ordered by priority
	strategies := []DetectionStrategy{CMTRoot, GitCommit, MtimeSample, ContentHash}

	for i := 1; i < len(strategies); i++ {
		if strategies[i].Priority() <= strategies[i-1].Priority() {
			t.Errorf("Strategy %s priority not greater than %s",
				strategies[i].String(), strategies[i-1].String())
		}
	}
}

// =============================================================================
// StaleFile Tests
// =============================================================================

func TestStaleFile_Age(t *testing.T) {
	t.Parallel()

	t.Run("returns zero when LastIndexed is zero", func(t *testing.T) {
		t.Parallel()
		file := StaleFile{
			Path:        "/path/to/file.go",
			LastIndexed: time.Time{},
		}
		if file.Age() != 0 {
			t.Errorf("StaleFile.Age() = %v, want 0", file.Age())
		}
	})

	t.Run("returns positive duration when LastIndexed is set", func(t *testing.T) {
		t.Parallel()
		file := StaleFile{
			Path:        "/path/to/file.go",
			LastIndexed: time.Now().Add(-1 * time.Hour),
		}
		age := file.Age()
		if age < 59*time.Minute || age > 61*time.Minute {
			t.Errorf("StaleFile.Age() = %v, want approximately 1 hour", age)
		}
	})
}

// =============================================================================
// DetectionResult Tests
// =============================================================================

func TestDetectionResult_IsFresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    StalenessLevel
		expected bool
	}{
		{
			name:     "fresh result returns true",
			level:    Fresh,
			expected: true,
		},
		{
			name:     "stale result returns false",
			level:    ModeratelyStale,
			expected: false,
		},
		{
			name:     "unknown result returns false",
			level:    Unknown,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &DetectionResult{Level: tt.level}
			if got := result.IsFresh(); got != tt.expected {
				t.Errorf("DetectionResult.IsFresh() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetectionResult_IsStale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    StalenessLevel
		expected bool
	}{
		{
			name:     "fresh result returns false",
			level:    Fresh,
			expected: false,
		},
		{
			name:     "slightly stale returns true",
			level:    SlightlyStale,
			expected: true,
		},
		{
			name:     "moderately stale returns true",
			level:    ModeratelyStale,
			expected: true,
		},
		{
			name:     "severely stale returns true",
			level:    SeverelyStale,
			expected: true,
		},
		{
			name:     "unknown returns false",
			level:    Unknown,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &DetectionResult{Level: tt.level}
			if got := result.IsStale(); got != tt.expected {
				t.Errorf("DetectionResult.IsStale() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// StalenessReport Tests
// =============================================================================

func TestStalenessReport_StaleFileCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		files    []StaleFile
		expected int
	}{
		{
			name:     "empty report returns 0",
			files:    []StaleFile{},
			expected: 0,
		},
		{
			name:     "nil files returns 0",
			files:    nil,
			expected: 0,
		},
		{
			name: "report with files returns correct count",
			files: []StaleFile{
				{Path: "/file1.go"},
				{Path: "/file2.go"},
				{Path: "/file3.go"},
			},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			report := &StalenessReport{StaleFiles: tt.files}
			if got := report.StaleFileCount(); got != tt.expected {
				t.Errorf("StalenessReport.StaleFileCount() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestStalenessReport_IsFresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    StalenessLevel
		expected bool
	}{
		{
			name:     "fresh report returns true",
			level:    Fresh,
			expected: true,
		},
		{
			name:     "stale report returns false",
			level:    SeverelyStale,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			report := &StalenessReport{Level: tt.level}
			if got := report.IsFresh(); got != tt.expected {
				t.Errorf("StalenessReport.IsFresh() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestStalenessReport_NeedsReindex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    StalenessLevel
		expected bool
	}{
		{
			name:     "fresh does not need reindex",
			level:    Fresh,
			expected: false,
		},
		{
			name:     "slightly stale does not need reindex",
			level:    SlightlyStale,
			expected: false,
		},
		{
			name:     "moderately stale needs reindex",
			level:    ModeratelyStale,
			expected: true,
		},
		{
			name:     "severely stale needs reindex",
			level:    SeverelyStale,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			report := &StalenessReport{Level: tt.level}
			if got := report.NeedsReindex(); got != tt.expected {
				t.Errorf("StalenessReport.NeedsReindex() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestStalenessReport_AddStaleFile(t *testing.T) {
	t.Parallel()

	report := &StalenessReport{StaleFiles: []StaleFile{}}

	file1 := StaleFile{Path: "/file1.go", Reason: "mtime changed"}
	file2 := StaleFile{Path: "/file2.go", Reason: "hash mismatch"}

	report.AddStaleFile(file1)
	if len(report.StaleFiles) != 1 {
		t.Fatalf("Expected 1 file, got %d", len(report.StaleFiles))
	}

	report.AddStaleFile(file2)
	if len(report.StaleFiles) != 2 {
		t.Fatalf("Expected 2 files, got %d", len(report.StaleFiles))
	}

	if report.StaleFiles[0].Path != "/file1.go" {
		t.Errorf("First file path = %q, want %q", report.StaleFiles[0].Path, "/file1.go")
	}
	if report.StaleFiles[1].Path != "/file2.go" {
		t.Errorf("Second file path = %q, want %q", report.StaleFiles[1].Path, "/file2.go")
	}
}

func TestStalenessReport_AddResult(t *testing.T) {
	t.Parallel()

	report := &StalenessReport{Results: []*DetectionResult{}}

	result1 := &DetectionResult{Strategy: CMTRoot, Level: Fresh}
	result2 := &DetectionResult{Strategy: GitCommit, Level: ModeratelyStale}

	report.AddResult(result1)
	if len(report.Results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(report.Results))
	}

	report.AddResult(result2)
	if len(report.Results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(report.Results))
	}

	if report.Results[0].Strategy != CMTRoot {
		t.Errorf("First result strategy = %v, want CMTRoot", report.Results[0].Strategy)
	}
	if report.Results[1].Strategy != GitCommit {
		t.Errorf("Second result strategy = %v, want GitCommit", report.Results[1].Strategy)
	}
}

// =============================================================================
// StrategyResult Tests
// =============================================================================

func TestStrategyResult_Succeeded(t *testing.T) {
	t.Parallel()

	t.Run("nil error returns true", func(t *testing.T) {
		t.Parallel()
		result := &StrategyResult{Error: nil}
		if !result.Succeeded() {
			t.Error("StrategyResult.Succeeded() = false, want true")
		}
	})

	t.Run("non-nil error returns false", func(t *testing.T) {
		t.Parallel()
		result := &StrategyResult{Error: ErrDetectionFailed}
		if result.Succeeded() {
			t.Error("StrategyResult.Succeeded() = true, want false")
		}
	})
}

// =============================================================================
// DetectorConfig Tests
// =============================================================================

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()

	if !config.EnableCMT {
		t.Error("DefaultConfig().EnableCMT = false, want true")
	}
	if !config.EnableGit {
		t.Error("DefaultConfig().EnableGit = false, want true")
	}
	if !config.EnableMtime {
		t.Error("DefaultConfig().EnableMtime = false, want true")
	}
	if !config.EnableContentHash {
		t.Error("DefaultConfig().EnableContentHash = false, want true")
	}
	if config.MtimeSampleSize != 100 {
		t.Errorf("DefaultConfig().MtimeSampleSize = %d, want 100", config.MtimeSampleSize)
	}
	if config.ContentHashSampleSize != 10 {
		t.Errorf("DefaultConfig().ContentHashSampleSize = %d, want 10", config.ContentHashSampleSize)
	}
	if config.StaleThresholdSlightly != 5 {
		t.Errorf("DefaultConfig().StaleThresholdSlightly = %d, want 5", config.StaleThresholdSlightly)
	}
	if config.StaleThresholdModerate != 20 {
		t.Errorf("DefaultConfig().StaleThresholdModerate = %d, want 20", config.StaleThresholdModerate)
	}
	if !config.EarlyExitOnFresh {
		t.Error("DefaultConfig().EarlyExitOnFresh = false, want true")
	}
}

func TestDetectorConfig_Validate(t *testing.T) {
	t.Parallel()

	t.Run("valid config with all strategies returns nil", func(t *testing.T) {
		t.Parallel()
		config := DefaultConfig()
		if err := config.Validate(); err != nil {
			t.Errorf("DetectorConfig.Validate() = %v, want nil", err)
		}
	})

	t.Run("valid config with one strategy returns nil", func(t *testing.T) {
		t.Parallel()
		config := DetectorConfig{EnableCMT: true}
		if err := config.Validate(); err != nil {
			t.Errorf("DetectorConfig.Validate() = %v, want nil", err)
		}
	})

	t.Run("config with no strategies returns error", func(t *testing.T) {
		t.Parallel()
		config := DetectorConfig{}
		if err := config.Validate(); err != ErrNoStrategiesAvailable {
			t.Errorf("DetectorConfig.Validate() = %v, want ErrNoStrategiesAvailable", err)
		}
	})
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestComputeStalenessLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		count             int
		thresholdSlightly int
		thresholdModerate int
		expected          StalenessLevel
	}{
		{
			name:              "zero count returns Fresh",
			count:             0,
			thresholdSlightly: 5,
			thresholdModerate: 20,
			expected:          Fresh,
		},
		{
			name:              "count below slightly threshold returns SlightlyStale",
			count:             3,
			thresholdSlightly: 5,
			thresholdModerate: 20,
			expected:          SlightlyStale,
		},
		{
			name:              "count at slightly threshold returns ModeratelyStale",
			count:             5,
			thresholdSlightly: 5,
			thresholdModerate: 20,
			expected:          ModeratelyStale,
		},
		{
			name:              "count between thresholds returns ModeratelyStale",
			count:             10,
			thresholdSlightly: 5,
			thresholdModerate: 20,
			expected:          ModeratelyStale,
		},
		{
			name:              "count at moderate threshold returns SeverelyStale",
			count:             20,
			thresholdSlightly: 5,
			thresholdModerate: 20,
			expected:          SeverelyStale,
		},
		{
			name:              "count above moderate threshold returns SeverelyStale",
			count:             100,
			thresholdSlightly: 5,
			thresholdModerate: 20,
			expected:          SeverelyStale,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ComputeStalenessLevel(tt.count, tt.thresholdSlightly, tt.thresholdModerate)
			if got != tt.expected {
				t.Errorf("ComputeStalenessLevel() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNewFreshReport(t *testing.T) {
	t.Parallel()

	before := time.Now()
	report := NewFreshReport(CMTRoot, 100*time.Millisecond)
	after := time.Now()

	if report.Level != Fresh {
		t.Errorf("NewFreshReport().Level = %v, want Fresh", report.Level)
	}
	if len(report.StaleFiles) != 0 {
		t.Errorf("NewFreshReport().StaleFiles = %v, want empty", report.StaleFiles)
	}
	if report.Confidence != 1.0 {
		t.Errorf("NewFreshReport().Confidence = %f, want 1.0", report.Confidence)
	}
	if report.Strategy != CMTRoot {
		t.Errorf("NewFreshReport().Strategy = %v, want CMTRoot", report.Strategy)
	}
	if report.Duration != 100*time.Millisecond {
		t.Errorf("NewFreshReport().Duration = %v, want 100ms", report.Duration)
	}
	if report.EarlyExit {
		t.Error("NewFreshReport().EarlyExit = true, want false")
	}
	if report.DetectedAt.Before(before) || report.DetectedAt.After(after) {
		t.Error("NewFreshReport().DetectedAt not within expected time range")
	}
	if report.Results == nil {
		t.Error("NewFreshReport().Results = nil, want empty slice")
	}
}

func TestNewStaleReport(t *testing.T) {
	t.Parallel()

	staleSince := time.Now().Add(-1 * time.Hour)
	files := []StaleFile{
		{Path: "/file1.go"},
		{Path: "/file2.go"},
	}

	before := time.Now()
	report := NewStaleReport(SeverelyStale, files, staleSince, 0.85, GitCommit, 200*time.Millisecond)
	after := time.Now()

	if report.Level != SeverelyStale {
		t.Errorf("NewStaleReport().Level = %v, want SeverelyStale", report.Level)
	}
	if len(report.StaleFiles) != 2 {
		t.Errorf("NewStaleReport().StaleFiles len = %d, want 2", len(report.StaleFiles))
	}
	if report.StaleSince != staleSince {
		t.Errorf("NewStaleReport().StaleSince = %v, want %v", report.StaleSince, staleSince)
	}
	if report.Confidence != 0.85 {
		t.Errorf("NewStaleReport().Confidence = %f, want 0.85", report.Confidence)
	}
	if report.Strategy != GitCommit {
		t.Errorf("NewStaleReport().Strategy = %v, want GitCommit", report.Strategy)
	}
	if report.Duration != 200*time.Millisecond {
		t.Errorf("NewStaleReport().Duration = %v, want 200ms", report.Duration)
	}
	if report.EarlyExit {
		t.Error("NewStaleReport().EarlyExit = true, want false")
	}
	if report.DetectedAt.Before(before) || report.DetectedAt.After(after) {
		t.Error("NewStaleReport().DetectedAt not within expected time range")
	}
}

// =============================================================================
// Error Tests
// =============================================================================

func TestErrors_AreDistinct(t *testing.T) {
	t.Parallel()

	errors := []error{
		ErrDetectorClosed,
		ErrDetectionFailed,
		ErrInvalidStrategy,
		ErrNoStrategiesAvailable,
		ErrCMTUnavailable,
		ErrGitUnavailable,
		ErrContextCanceled,
		ErrNoFilesToSample,
		ErrSampleSizeInvalid,
		ErrBaseDirEmpty,
	}

	for i := 0; i < len(errors); i++ {
		for j := i + 1; j < len(errors); j++ {
			// Skip ErrCMTNotAvailable as it's an alias
			if errors[i] == ErrCMTNotAvailable || errors[j] == ErrCMTNotAvailable {
				continue
			}
			if errors[i] == errors[j] {
				t.Errorf("Errors at index %d and %d are not distinct", i, j)
			}
		}
	}
}

func TestErrCMTNotAvailable_IsAlias(t *testing.T) {
	t.Parallel()

	if ErrCMTNotAvailable != ErrCMTUnavailable {
		t.Error("ErrCMTNotAvailable should be alias for ErrCMTUnavailable")
	}
}
