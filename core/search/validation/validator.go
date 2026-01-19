// Package validation provides cross-source validation for the Sylk Document Search System.
package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/search/cmt"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrValidatorClosed indicates the validator has been closed.
	ErrValidatorClosed = errors.New("validator is closed")

	// ErrRepoPathEmpty indicates an empty repository path.
	ErrRepoPathEmpty = errors.New("repository path cannot be empty")

	// ErrCMTRequired indicates CMT is required but not provided.
	ErrCMTRequired = errors.New("CMT is required for validation")
)

// =============================================================================
// Interfaces
// =============================================================================

// CMTReader provides read-only access to CMT file manifest.
type CMTReader interface {
	Get(key string) *cmt.FileInfo
	Walk(fn func(key string, info *cmt.FileInfo) bool)
}

// GitSource provides read-only access to git file information.
// This interface allows for flexible implementation of git integration.
type GitSource interface {
	// IsAvailable returns true if git source is available.
	IsAvailable() bool

	// GetTrackedPaths returns all git-tracked file paths.
	GetTrackedPaths() ([]string, error)

	// GetFileState returns file state from git for a path.
	// Returns nil if the file is not tracked.
	GetFileState(path string) *FileState
}

// =============================================================================
// CrossValidatorConfig
// =============================================================================

// CrossValidatorConfig configures the cross-source validator.
type CrossValidatorConfig struct {
	// RepoPath is the path to the repository root.
	RepoPath string

	// CMT is the Cartesian Merkle Tree for manifest data.
	CMT CMTReader

	// Git is the git source for git data.
	Git GitSource

	// Config contains validation configuration options.
	Config ValidationConfig
}

// =============================================================================
// CrossValidator
// =============================================================================

// CrossValidator compares filesystem, CMT, and git sources.
// It detects discrepancies where sources disagree on file state.
type CrossValidator struct {
	repoPath string
	cmt      CMTReader
	git      GitSource
	config   ValidationConfig
	mu       sync.RWMutex
	closed   bool
}

// NewCrossValidator creates a new cross-source validator.
func NewCrossValidator(cfg CrossValidatorConfig) (*CrossValidator, error) {
	if cfg.RepoPath == "" {
		return nil, ErrRepoPathEmpty
	}

	config := cfg.Config
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 4
	}

	return &CrossValidator{
		repoPath: cfg.RepoPath,
		cmt:      cfg.CMT,
		git:      cfg.Git,
		config:   config,
	}, nil
}

// =============================================================================
// Validation
// =============================================================================

// Validate performs cross-source validation and returns a report.
func (v *CrossValidator) Validate(ctx context.Context) (*ValidationReport, error) {
	if err := v.checkClosed(); err != nil {
		return nil, err
	}

	startTime := time.Now()

	paths, err := v.collectPaths(ctx)
	if err != nil {
		return nil, err
	}

	discrepancies := v.validatePathsParallel(ctx, paths)

	return v.buildReport(startTime, paths, discrepancies), nil
}

// collectPaths gathers all paths to validate from configured sources.
func (v *CrossValidator) collectPaths(ctx context.Context) ([]string, error) {
	if v.config.HasPathSubset() {
		return v.config.Paths, nil
	}
	return v.collectAllPaths(ctx)
}

// collectAllPaths gathers paths from all available sources.
func (v *CrossValidator) collectAllPaths(ctx context.Context) ([]string, error) {
	pathSet := make(map[string]struct{})

	v.collectCMTPaths(pathSet)
	v.collectGitPaths(pathSet)

	return mapKeysToSlice(pathSet), nil
}

// collectCMTPaths adds CMT paths to the path set.
func (v *CrossValidator) collectCMTPaths(pathSet map[string]struct{}) {
	if v.cmt == nil {
		return
	}
	v.cmt.Walk(func(key string, _ *cmt.FileInfo) bool {
		pathSet[key] = struct{}{}
		return true
	})
}

// collectGitPaths adds git-tracked paths to the path set.
func (v *CrossValidator) collectGitPaths(pathSet map[string]struct{}) {
	if v.git == nil || !v.git.IsAvailable() {
		return
	}

	files, err := v.git.GetTrackedPaths()
	if err != nil {
		return
	}

	for _, f := range files {
		pathSet[f] = struct{}{}
	}
}

// =============================================================================
// Parallel Validation
// =============================================================================

// validatePathsParallel validates paths using worker pool pattern.
func (v *CrossValidator) validatePathsParallel(ctx context.Context, paths []string) []*Discrepancy {
	if len(paths) == 0 {
		return nil
	}

	results := make(chan *Discrepancy, len(paths))
	pathChan := make(chan string, len(paths))

	var wg sync.WaitGroup
	workers := v.config.MaxConcurrent

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go v.validationWorker(ctx, &wg, pathChan, results)
	}

	for _, p := range paths {
		pathChan <- p
	}
	close(pathChan)

	wg.Wait()
	close(results)

	return collectDiscrepancies(results)
}

// validationWorker processes paths from the channel.
func (v *CrossValidator) validationWorker(
	ctx context.Context,
	wg *sync.WaitGroup,
	paths <-chan string,
	results chan<- *Discrepancy,
) {
	defer wg.Done()

	for path := range paths {
		if ctx.Err() != nil {
			return
		}
		if d := v.validatePath(path); d != nil {
			results <- d
		}
	}
}

// validatePath validates a single path across all sources.
func (v *CrossValidator) validatePath(path string) *Discrepancy {
	states := v.collectStates(path)
	return v.detectDiscrepancy(path, states)
}

// =============================================================================
// State Collection
// =============================================================================

// collectStates gathers file state from all sources.
func (v *CrossValidator) collectStates(path string) []*FileState {
	var states []*FileState

	if fs := v.getFilesystemState(path); fs != nil {
		states = append(states, fs)
	}

	if cs := v.getCMTState(path); cs != nil {
		states = append(states, cs)
	}

	if gs := v.getGitState(path); gs != nil {
		states = append(states, gs)
	}

	return states
}

// getFilesystemState gets the file state from the filesystem.
func (v *CrossValidator) getFilesystemState(path string) *FileState {
	fullPath := v.fullPath(path)
	info, err := os.Stat(fullPath)

	state := &FileState{
		Path:   path,
		Source: SourceFilesystem,
		Exists: err == nil,
	}

	if !state.Exists {
		return state
	}

	state.Size = info.Size()
	state.ModTime = info.ModTime()
	state.ContentHash = v.computeFileHash(fullPath)

	return state
}

// getCMTState gets the file state from CMT.
func (v *CrossValidator) getCMTState(path string) *FileState {
	if v.cmt == nil {
		return nil
	}

	info := v.cmt.Get(path)
	state := &FileState{
		Path:   path,
		Source: SourceCMT,
		Exists: info != nil,
	}

	if !state.Exists {
		return state
	}

	state.Size = info.Size
	state.ModTime = info.ModTime
	state.ContentHash = info.ContentHash.String()

	return state
}

// getGitState gets the file state from git.
func (v *CrossValidator) getGitState(path string) *FileState {
	if v.git == nil || !v.git.IsAvailable() {
		return nil
	}

	return v.git.GetFileState(path)
}

// =============================================================================
// Discrepancy Detection
// =============================================================================

// detectDiscrepancy checks states for inconsistencies.
func (v *CrossValidator) detectDiscrepancy(path string, states []*FileState) *Discrepancy {
	if len(states) < 2 {
		return nil
	}

	discType := v.determineEnabledDiscrepancyType(states)
	if discType == DiscrepancyUnknown {
		return nil
	}

	return &Discrepancy{
		Path:    path,
		Sources: extractSources(states),
		States:  states,
		Type:    discType,
	}
}

// determineEnabledDiscrepancyType identifies the first enabled discrepancy type.
// It only returns types that are configured to be reported.
func (v *CrossValidator) determineEnabledDiscrepancyType(states []*FileState) DiscrepancyType {
	if v.config.IncludeMissing && hasMissingDiscrepancy(states) {
		return DiscrepancyMissing
	}

	if v.config.IncludeContentMismatch && hasContentMismatch(states) {
		return DiscrepancyContentMismatch
	}

	if v.config.IncludeSizeMismatch && hasSizeMismatch(states) {
		return DiscrepancySizeMismatch
	}

	if v.config.IncludeModTimeMismatch && hasModTimeMismatch(states) {
		return DiscrepancyModTimeMismatch
	}

	return DiscrepancyUnknown
}

// hasMissingDiscrepancy checks if any source is missing the file.
func hasMissingDiscrepancy(states []*FileState) bool {
	var hasExisting, hasMissing bool
	for _, s := range states {
		if s.Exists {
			hasExisting = true
		} else {
			hasMissing = true
		}
	}
	return hasExisting && hasMissing
}

// hasContentMismatch checks if content hashes differ.
func hasContentMismatch(states []*FileState) bool {
	var refHash string
	for _, s := range states {
		if !s.Exists || s.ContentHash == "" {
			continue
		}
		if refHash == "" {
			refHash = s.ContentHash
			continue
		}
		if s.ContentHash != refHash {
			return true
		}
	}
	return false
}

// hasSizeMismatch checks if sizes differ.
func hasSizeMismatch(states []*FileState) bool {
	var refSize int64
	var hasRef bool
	for _, s := range states {
		if !s.Exists {
			continue
		}
		if !hasRef {
			refSize = s.Size
			hasRef = true
			continue
		}
		if s.Size != refSize {
			return true
		}
	}
	return false
}

// hasModTimeMismatch checks if modification times differ.
func hasModTimeMismatch(states []*FileState) bool {
	var refTime time.Time
	var hasRef bool
	for _, s := range states {
		if !s.Exists || s.ModTime.IsZero() {
			continue
		}
		if !hasRef {
			refTime = s.ModTime
			hasRef = true
			continue
		}
		if !s.ModTime.Equal(refTime) {
			return true
		}
	}
	return false
}

// =============================================================================
// Report Building
// =============================================================================

// buildReport constructs the validation report.
func (v *CrossValidator) buildReport(
	startTime time.Time,
	paths []string,
	discrepancies []*Discrepancy,
) *ValidationReport {
	return &ValidationReport{
		CheckedAt:      startTime,
		TotalFiles:     len(paths),
		Discrepancies:  discrepancies,
		Duration:       time.Since(startTime),
		SourcesChecked: v.getActiveSources(),
		PathsChecked:   paths,
	}
}

// getActiveSources returns the list of sources being validated.
func (v *CrossValidator) getActiveSources() []ValidationSource {
	sources := []ValidationSource{SourceFilesystem}

	if v.cmt != nil {
		sources = append(sources, SourceCMT)
	}

	if v.git != nil && v.git.IsAvailable() {
		sources = append(sources, SourceGit)
	}

	return sources
}

// =============================================================================
// Helper Methods
// =============================================================================

// fullPath returns the absolute path for a relative path.
func (v *CrossValidator) fullPath(path string) string {
	return filepath.Join(v.repoPath, path)
}

// computeFileHash computes SHA-256 hash of a file.
func (v *CrossValidator) computeFileHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}

	return hex.EncodeToString(h.Sum(nil))
}

// checkClosed returns an error if the validator is closed.
func (v *CrossValidator) checkClosed() error {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return ErrValidatorClosed
	}
	return nil
}

// Close releases resources held by the validator.
func (v *CrossValidator) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.closed = true
	return nil
}

// =============================================================================
// Utility Functions
// =============================================================================

// mapKeysToSlice extracts keys from a map into a slice.
func mapKeysToSlice(m map[string]struct{}) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	return result
}

// extractSources extracts sources from a slice of states.
func extractSources(states []*FileState) []ValidationSource {
	sources := make([]ValidationSource, len(states))
	for i, s := range states {
		sources[i] = s.Source
	}
	return sources
}

// collectDiscrepancies drains a channel into a slice.
func collectDiscrepancies(ch <-chan *Discrepancy) []*Discrepancy {
	var result []*Discrepancy
	for d := range ch {
		result = append(result, d)
	}
	return result
}
