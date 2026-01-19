package staleness

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// =============================================================================
// Sample Strategy
// =============================================================================

// SampleStrategy determines how files are selected for mtime sampling.
type SampleStrategy int

const (
	// RandomSample selects files randomly from the file list.
	RandomSample SampleStrategy = iota

	// RecentFirstSample prioritizes recently modified files.
	RecentFirstSample

	// LargestFirstSample prioritizes largest files.
	LargestFirstSample
)

// String returns a human-readable name for the sample strategy.
func (s SampleStrategy) String() string {
	switch s {
	case RandomSample:
		return "random"
	case RecentFirstSample:
		return "recent_first"
	case LargestFirstSample:
		return "largest_first"
	default:
		return "unknown"
	}
}

// =============================================================================
// Sample Config
// =============================================================================

// SampleConfig configures the mtime sampler.
type SampleConfig struct {
	// SampleSize is the number of files to sample.
	SampleSize int

	// Strategy determines how files are selected.
	Strategy SampleStrategy

	// MaxAge is the maximum age for a file to be considered fresh.
	MaxAge time.Duration

	// RandSource provides randomness for sampling (optional, for testing).
	RandSource rand.Source
}

// DefaultSampleConfig returns sensible default configuration.
func DefaultSampleConfig() SampleConfig {
	return SampleConfig{
		SampleSize: 100,
		Strategy:   RandomSample,
		MaxAge:     5 * time.Minute,
	}
}

// Validate checks that the configuration is valid.
func (c *SampleConfig) Validate() error {
	if c.SampleSize <= 0 {
		return ErrSampleSizeInvalid
	}
	return nil
}

// =============================================================================
// File Info Provider
// =============================================================================

// FileInfoProvider provides file metadata for sampling.
// This interface allows for dependency injection and testing.
type FileInfoProvider interface {
	// ListFiles returns paths of all tracked files.
	ListFiles() []string

	// GetModTime returns the stored modification time for a file.
	GetModTime(path string) (time.Time, bool)
}

// =============================================================================
// MtimeSampler
// =============================================================================

// MtimeSampler samples file modification times to detect staleness.
// It stats files without reading their content, making it efficient.
type MtimeSampler struct {
	config   SampleConfig
	baseDir  string
	provider FileInfoProvider
	rng      *rand.Rand
	mu       sync.RWMutex
}

// NewMtimeSampler creates a new mtime sampler.
// Returns an error if baseDir is empty or config is invalid.
func NewMtimeSampler(baseDir string, config SampleConfig, provider FileInfoProvider) (*MtimeSampler, error) {
	if baseDir == "" {
		return nil, ErrBaseDirEmpty
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	rngSource := config.RandSource
	if rngSource == nil {
		rngSource = rand.NewSource(time.Now().UnixNano())
	}

	return &MtimeSampler{
		config:   config,
		baseDir:  baseDir,
		provider: provider,
		rng:      rand.New(rngSource),
	}, nil
}

// Sample performs mtime sampling and returns a staleness report.
// It stats a sample of files to check if mtimes have changed.
func (m *MtimeSampler) Sample(ctx context.Context) (*StalenessReport, error) {
	start := time.Now()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	files := m.getFilesToSample()
	if len(files) == 0 {
		return m.buildEmptyResult(time.Since(start)), nil
	}

	sample := m.selectSample(files)
	staleFiles := m.checkMtimes(ctx, sample)
	duration := time.Since(start)

	return m.buildResult(staleFiles, len(sample), duration), nil
}

// getFilesToSample returns all files available for sampling.
func (m *MtimeSampler) getFilesToSample() []string {
	if m.provider == nil {
		return nil
	}
	return m.provider.ListFiles()
}

// selectSample selects files according to the configured strategy.
func (m *MtimeSampler) selectSample(files []string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	sampleSize := m.config.SampleSize
	if sampleSize > len(files) {
		sampleSize = len(files)
	}

	switch m.config.Strategy {
	case RandomSample:
		return m.randomSelect(files, sampleSize)
	default:
		return m.randomSelect(files, sampleSize)
	}
}

// randomSelect randomly selects n files from the list.
func (m *MtimeSampler) randomSelect(files []string, n int) []string {
	if n >= len(files) {
		return files
	}

	// Fisher-Yates shuffle (partial)
	shuffled := make([]string, len(files))
	copy(shuffled, files)

	for i := 0; i < n; i++ {
		j := i + m.rng.Intn(len(shuffled)-i)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	return shuffled[:n]
}

// checkMtimes checks if any sampled files have modified mtimes.
func (m *MtimeSampler) checkMtimes(ctx context.Context, sample []string) []StaleFile {
	var staleFiles []StaleFile

	for _, path := range sample {
		if ctx.Err() != nil {
			break
		}

		if staleFile, isStale := m.checkFileMtime(path); isStale {
			staleFiles = append(staleFiles, staleFile)
		}
	}

	return staleFiles
}

// checkFileMtime checks if a single file's mtime has changed.
func (m *MtimeSampler) checkFileMtime(path string) (StaleFile, bool) {
	storedMtime, ok := m.getStoredMtime(path)
	if !ok {
		return StaleFile{}, false // Unknown file, not considered stale
	}

	currentMtime, err := m.getCurrentMtime(path)
	if err != nil {
		return StaleFile{}, false // Cannot stat, not considered stale
	}

	if storedMtime.Equal(currentMtime) {
		return StaleFile{}, false
	}

	return StaleFile{
		Path:         path,
		Reason:       "mtime changed",
		DetectedAt:   time.Now(),
		LastIndexed:  storedMtime,
		CurrentMtime: currentMtime,
	}, true
}

// getStoredMtime retrieves the stored modification time for a file.
func (m *MtimeSampler) getStoredMtime(path string) (time.Time, bool) {
	if m.provider == nil {
		return time.Time{}, false
	}
	return m.provider.GetModTime(path)
}

// getCurrentMtime stats the file and returns its current mtime.
func (m *MtimeSampler) getCurrentMtime(path string) (time.Time, error) {
	fullPath := filepath.Join(m.baseDir, path)
	info, err := os.Stat(fullPath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// buildEmptyResult builds a result when there are no files to sample.
func (m *MtimeSampler) buildEmptyResult(duration time.Duration) *StalenessReport {
	return &StalenessReport{
		Level:      Fresh,
		Strategy:   MtimeSample,
		DetectedAt: time.Now(),
		Duration:   duration,
		Confidence: 0.0, // No confidence when no files sampled
		StaleFiles: []StaleFile{},
	}
}

// buildResult builds a result from the stale file check.
func (m *MtimeSampler) buildResult(staleFiles []StaleFile, sampleSize int, duration time.Duration) *StalenessReport {
	level := m.calculateStalenessLevel(staleFiles, sampleSize)
	confidence := m.calculateConfidence(sampleSize)

	if level == Fresh {
		report := NewFreshReport(MtimeSample, duration)
		report.Confidence = confidence
		return report
	}

	staleSince := m.findEarliestStaleness(staleFiles)

	return NewStaleReport(level, staleFiles, staleSince, confidence, MtimeSample, duration)
}

// findEarliestStaleness finds the earliest staleness time from stale files.
func (m *MtimeSampler) findEarliestStaleness(staleFiles []StaleFile) time.Time {
	if len(staleFiles) == 0 {
		return time.Time{}
	}

	earliest := staleFiles[0].LastIndexed
	for _, f := range staleFiles[1:] {
		if !f.LastIndexed.IsZero() && f.LastIndexed.Before(earliest) {
			earliest = f.LastIndexed
		}
	}
	return earliest
}

// calculateStalenessLevel determines staleness based on stale file ratio.
func (m *MtimeSampler) calculateStalenessLevel(staleFiles []StaleFile, sampleSize int) StalenessLevel {
	if len(staleFiles) == 0 {
		return Fresh
	}

	ratio := float64(len(staleFiles)) / float64(sampleSize)

	if ratio < 0.1 {
		return SlightlyStale
	}
	if ratio < 0.3 {
		return ModeratelyStale
	}
	return SeverelyStale
}

// calculateConfidence returns confidence based on sample size.
func (m *MtimeSampler) calculateConfidence(sampleSize int) float64 {
	if sampleSize == 0 {
		return 0.0
	}
	if sampleSize >= m.config.SampleSize {
		return 0.9 // High confidence when full sample achieved
	}
	return 0.9 * float64(sampleSize) / float64(m.config.SampleSize)
}

// Config returns the sampler configuration.
func (m *MtimeSampler) Config() SampleConfig {
	return m.config
}

// BaseDir returns the base directory for file paths.
func (m *MtimeSampler) BaseDir() string {
	return m.baseDir
}
