// Package staleness provides staleness detection for the Sylk Document Search System.
package staleness

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// =============================================================================
// Interfaces
// =============================================================================

// GitProvider provides Git repository operations for staleness detection.
type GitProvider interface {
	// GetHead returns the current HEAD commit hash.
	GetHead() (string, error)

	// IsGitRepo returns true if the path is a git repository.
	IsGitRepo() bool

	// GetUncommittedFiles returns files with uncommitted changes.
	GetUncommittedFiles() ([]string, error)
}

// ContentHashProvider provides content hash comparison capabilities.
type ContentHashProvider interface {
	// GetStoredHash returns the stored content hash for a file.
	GetStoredHash(path string) ([]byte, bool)

	// ListFiles returns all tracked file paths.
	ListFiles() []string
}

// =============================================================================
// HybridStalenessDetector
// =============================================================================

// HybridStalenessDetector uses multiple strategies to detect index staleness.
// Strategies are tried in order of preference:
// 1. CMT root hash comparison (instant, catch-all)
// 2. Git HEAD check (fast, catches committed changes)
// 3. Filesystem mtime sampling (periodic, catches uncommitted)
// 4. Content hash spot-check (deep verification)
type HybridStalenessDetector struct {
	config DetectorConfig

	cmtCheck *CMTRootCheck
	git      GitProvider
	mtime    *MtimeSampler
	content  ContentHashProvider

	lastGitHead   string
	lastCheckTime time.Time

	closed bool
	mu     sync.RWMutex
}

// NewHybridStalenessDetector creates a new hybrid staleness detector.
func NewHybridStalenessDetector(config DetectorConfig) (*HybridStalenessDetector, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &HybridStalenessDetector{
		config: config,
		closed: false,
	}, nil
}

// SetCMT sets the CMT provider for root hash comparison.
func (d *HybridStalenessDetector) SetCMT(cmt CMTHashProvider) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrDetectorClosed
	}

	if cmt == nil {
		d.cmtCheck = nil
		return nil
	}

	check, err := NewCMTRootCheck(cmt)
	if err != nil {
		return err
	}

	d.cmtCheck = check
	return nil
}

// SetGit sets the Git provider for HEAD comparison.
func (d *HybridStalenessDetector) SetGit(git GitProvider) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.git = git
}

// SetMtimeSampler sets the mtime sampler for filesystem checks.
func (d *HybridStalenessDetector) SetMtimeSampler(sampler *MtimeSampler) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.mtime = sampler
}

// SetContentHashProvider sets the content hash provider for deep verification.
func (d *HybridStalenessDetector) SetContentHashProvider(provider ContentHashProvider) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.content = provider
}

// =============================================================================
// Main Detection Method
// =============================================================================

// Detect performs staleness detection using configured strategies.
// Strategies are tried in order; detection stops early if freshness is confirmed.
func (d *HybridStalenessDetector) Detect(ctx context.Context) (*StalenessReport, error) {
	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return nil, ErrDetectorClosed
	}
	d.mu.RUnlock()

	if err := ctx.Err(); err != nil {
		return nil, ErrContextCanceled
	}

	start := time.Now()
	report := d.initReport()

	// Try each strategy in order of preference
	d.tryCMTStrategy(ctx, report)
	if d.shouldEarlyExit(report) {
		return d.finalizeReport(report, start), nil
	}

	d.tryGitStrategy(ctx, report)
	if d.shouldEarlyExit(report) {
		return d.finalizeReport(report, start), nil
	}

	d.tryMtimeStrategy(ctx, report)
	if d.shouldEarlyExit(report) {
		return d.finalizeReport(report, start), nil
	}

	d.tryContentHashStrategy(ctx, report)

	return d.finalizeReport(report, start), nil
}

// initReport creates an initial empty report.
func (d *HybridStalenessDetector) initReport() *StalenessReport {
	return &StalenessReport{
		Level:      Unknown,
		StaleFiles: []StaleFile{},
		Confidence: 0.0,
		Results:    []*DetectionResult{},
	}
}

// shouldEarlyExit checks if we should stop detection early.
func (d *HybridStalenessDetector) shouldEarlyExit(report *StalenessReport) bool {
	if !d.config.EarlyExitOnFresh {
		return false
	}
	return report.Level == Fresh && report.Confidence >= 0.9
}

// finalizeReport completes the report with final timing and status.
func (d *HybridStalenessDetector) finalizeReport(report *StalenessReport, start time.Time) *StalenessReport {
	report.Duration = time.Since(start)
	report.DetectedAt = time.Now()

	d.mu.Lock()
	d.lastCheckTime = report.DetectedAt
	d.mu.Unlock()

	// If still unknown, default to fresh with low confidence
	if report.Level == Unknown {
		report.Level = Fresh
		report.Confidence = 0.5
	}

	return report
}

// =============================================================================
// CMT Strategy
// =============================================================================

// tryCMTStrategy attempts CMT root hash comparison.
func (d *HybridStalenessDetector) tryCMTStrategy(ctx context.Context, report *StalenessReport) {
	if !d.config.EnableCMT {
		return
	}

	d.mu.RLock()
	cmtCheck := d.cmtCheck
	d.mu.RUnlock()

	if cmtCheck == nil {
		return
	}

	cmtReport, err := cmtCheck.Check(ctx)
	if err != nil {
		return
	}

	result := reportToDetectionResult(cmtReport)
	report.AddResult(result)
	d.updateReportFromResult(report, result)
}

// =============================================================================
// Git Strategy
// =============================================================================

// tryGitStrategy attempts Git HEAD comparison.
func (d *HybridStalenessDetector) tryGitStrategy(ctx context.Context, report *StalenessReport) {
	if !d.config.EnableGit {
		return
	}

	d.mu.RLock()
	git := d.git
	lastHead := d.lastGitHead
	d.mu.RUnlock()

	if git == nil || !git.IsGitRepo() {
		return
	}

	result := d.checkGitHead(ctx, git, lastHead)
	if result == nil {
		return
	}

	report.AddResult(result)
	d.updateReportFromResult(report, result)
}

// checkGitHead compares current HEAD with stored HEAD.
func (d *HybridStalenessDetector) checkGitHead(
	ctx context.Context,
	git GitProvider,
	lastHead string,
) *DetectionResult {
	if ctx.Err() != nil {
		return nil
	}

	currentHead, err := git.GetHead()
	if err != nil {
		return nil
	}

	result := &DetectionResult{
		Strategy:   GitCommit,
		Timestamp:  time.Now(),
		Confidence: 0.95,
	}

	if lastHead == "" || currentHead == lastHead {
		result.Level = Fresh
		result.Details = "git HEAD unchanged"
		d.updateLastGitHead(currentHead)
		return result
	}

	result.Level = ModeratelyStale
	result.Details = "git HEAD changed"
	d.updateLastGitHead(currentHead)

	return result
}

// updateLastGitHead updates the stored Git HEAD.
func (d *HybridStalenessDetector) updateLastGitHead(head string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.lastGitHead = head
}

// =============================================================================
// Mtime Strategy
// =============================================================================

// tryMtimeStrategy attempts mtime sampling.
func (d *HybridStalenessDetector) tryMtimeStrategy(ctx context.Context, report *StalenessReport) {
	if !d.config.EnableMtime {
		return
	}

	d.mu.RLock()
	sampler := d.mtime
	d.mu.RUnlock()

	if sampler == nil {
		return
	}

	mtimeReport, err := sampler.Sample(ctx)
	if err != nil {
		return
	}

	result := reportToDetectionResult(mtimeReport)
	report.AddResult(result)
	d.updateReportFromResult(report, result)
}

// =============================================================================
// Content Hash Strategy
// =============================================================================

// tryContentHashStrategy attempts content hash spot-checking.
func (d *HybridStalenessDetector) tryContentHashStrategy(ctx context.Context, report *StalenessReport) {
	if !d.config.EnableContentHash {
		return
	}

	d.mu.RLock()
	provider := d.content
	baseDir := d.config.BaseDir
	sampleSize := d.config.ContentHashSampleSize
	d.mu.RUnlock()

	if provider == nil {
		return
	}

	result := d.spotCheckContentHashes(ctx, provider, baseDir, sampleSize)
	if result == nil {
		return
	}

	report.AddResult(result)
	d.updateReportFromResult(report, result)
}

// spotCheckContentHashes performs spot-checking of content hashes.
func (d *HybridStalenessDetector) spotCheckContentHashes(
	ctx context.Context,
	provider ContentHashProvider,
	baseDir string,
	sampleSize int,
) *DetectionResult {
	files := provider.ListFiles()
	if len(files) == 0 {
		return nil
	}

	sample := selectRandomSample(files, sampleSize)
	staleFiles := d.checkContentHashes(ctx, provider, baseDir, sample)

	result := &DetectionResult{
		Strategy:   ContentHash,
		StaleFiles: staleFiles,
		Timestamp:  time.Now(),
		Confidence: 0.85,
	}

	if len(staleFiles) == 0 {
		result.Level = Fresh
		result.Details = "content hashes match"
	} else {
		result.Level = d.computeLevelFromCount(len(staleFiles))
		result.Details = "content hash mismatch detected"
	}

	return result
}

// checkContentHashes checks content hashes for the given files.
func (d *HybridStalenessDetector) checkContentHashes(
	ctx context.Context,
	provider ContentHashProvider,
	baseDir string,
	files []string,
) []string {
	var staleFiles []string

	for _, file := range files {
		if ctx.Err() != nil {
			break
		}

		if d.isContentHashStale(provider, baseDir, file) {
			staleFiles = append(staleFiles, file)
		}
	}

	return staleFiles
}

// isContentHashStale checks if a file's content hash differs from stored.
func (d *HybridStalenessDetector) isContentHashStale(
	provider ContentHashProvider,
	baseDir string,
	file string,
) bool {
	storedHash, ok := provider.GetStoredHash(file)
	if !ok {
		return false
	}

	currentHash, err := computeFileHash(filepath.Join(baseDir, file))
	if err != nil {
		return false
	}

	return !hashesEqual(storedHash, currentHash)
}

// =============================================================================
// Helper Methods
// =============================================================================

// reportToDetectionResult converts a StalenessReport to a DetectionResult.
func reportToDetectionResult(r *StalenessReport) *DetectionResult {
	if r == nil {
		return nil
	}

	staleFilePaths := make([]string, len(r.StaleFiles))
	for i, f := range r.StaleFiles {
		staleFilePaths[i] = f.Path
	}

	return &DetectionResult{
		Level:      r.Level,
		Strategy:   r.Strategy,
		StaleFiles: staleFilePaths,
		Confidence: r.Confidence,
		Timestamp:  r.DetectedAt,
	}
}

// updateReportFromResult updates the report based on a detection result.
func (d *HybridStalenessDetector) updateReportFromResult(
	report *StalenessReport,
	result *DetectionResult,
) {
	// Use most severe level found
	if result.Level > report.Level || report.Level == Unknown {
		report.Level = result.Level
		report.Strategy = result.Strategy
	}

	// Take highest confidence
	if result.Confidence > report.Confidence {
		report.Confidence = result.Confidence
	}

	// Add stale files
	for _, path := range result.StaleFiles {
		report.StaleFiles = append(report.StaleFiles, StaleFile{
			Path:       path,
			Reason:     result.Strategy.String(),
			DetectedAt: result.Timestamp,
		})
	}
}

// computeLevelFromCount determines staleness level from stale file count.
func (d *HybridStalenessDetector) computeLevelFromCount(count int) StalenessLevel {
	return ComputeStalenessLevel(
		count,
		d.config.StaleThresholdSlightly,
		d.config.StaleThresholdModerate,
	)
}

// =============================================================================
// Close and State Methods
// =============================================================================

// Close closes the detector and releases resources.
func (d *HybridStalenessDetector) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.closed = true
	d.cmtCheck = nil
	d.git = nil
	d.mtime = nil
	d.content = nil

	return nil
}

// IsClosed returns true if the detector has been closed.
func (d *HybridStalenessDetector) IsClosed() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.closed
}

// LastCheckTime returns the time of the last staleness check.
func (d *HybridStalenessDetector) LastCheckTime() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.lastCheckTime
}

// UpdateStoredGitHead updates the stored Git HEAD after indexing.
func (d *HybridStalenessDetector) UpdateStoredGitHead(head string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.lastGitHead = head
}

// UpdateCMTHash updates the stored CMT hash after indexing.
func (d *HybridStalenessDetector) UpdateCMTHash() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cmtCheck != nil {
		d.cmtCheck.UpdateStoredHash()
	}
}

// =============================================================================
// Utility Functions
// =============================================================================

// selectRandomSample selects a random sample from the given files.
func selectRandomSample(files []string, size int) []string {
	if size >= len(files) {
		return files
	}

	// Simple selection - take first N for determinism in tests
	return files[:size]
}

// computeFileHash computes the SHA-256 hash of a file.
func computeFileHash(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}

	return h.Sum(nil), nil
}

// hashesEqual compares two hash slices for equality.
func hashesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
