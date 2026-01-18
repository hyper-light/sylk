// Package watcher provides file change detection functionality for the Sylk
// Document Search System. It includes periodic scanning, fsnotify integration,
// git hook integration, and change coordination.
package watcher

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrInvalidInterval indicates the scan interval is invalid.
	ErrInvalidInterval = errors.New("interval must be positive")

	// ErrInvalidSampleRate indicates the sample rate is out of range.
	ErrInvalidSampleRate = errors.New("sample rate must be between 0.0 and 1.0")

	// ErrEmptyRootPath indicates the root path was not specified.
	ErrEmptyRootPath = errors.New("root path cannot be empty")

	// ErrRootNotExist indicates the root path does not exist.
	ErrRootNotExist = errors.New("root path does not exist")

	// ErrAlreadyRunning indicates the scanner is already running.
	ErrAlreadyRunning = errors.New("scanner is already running")
)

// Note: The PeriodicScanner uses the ChecksumStore interface defined in
// checksum.go for accessing stored checksums.

// =============================================================================
// Configuration
// =============================================================================

// PeriodicScanConfig configures the periodic scanner.
type PeriodicScanConfig struct {
	// Interval is the time between scan cycles (required, must be positive)
	Interval time.Duration

	// SampleRate is the fraction of files to check per cycle (0.0-1.0)
	// Default: 0.1 (10% of files per cycle)
	SampleRate float64

	// RootPath is the directory to scan (required)
	RootPath string
}

// Validate checks that the configuration is valid.
func (c *PeriodicScanConfig) Validate() error {
	if c.Interval <= 0 {
		return ErrInvalidInterval
	}

	if c.SampleRate < 0 || c.SampleRate > 1.0 {
		return ErrInvalidSampleRate
	}

	if c.RootPath == "" {
		return ErrEmptyRootPath
	}

	return nil
}

// DefaultSampleRate is the default fraction of files to scan per cycle.
const DefaultSampleRate = 0.1

// =============================================================================
// PeriodicScanner
// =============================================================================

// PeriodicScanner performs scheduled file integrity checks.
// It scans files at configurable intervals and compares checksums against
// a checksum store to detect changes. The scanner supports pause/resume for
// yielding to higher-priority operations.
type PeriodicScanner struct {
	config PeriodicScanConfig
	store  ChecksumStore

	paused  atomic.Bool
	running atomic.Bool

	mu     sync.Mutex
	stopCh chan struct{}
}

// NewPeriodicScanner creates a new periodic scanner.
// The store parameter may be nil, in which case all files are reported
// as changed (useful for initial indexing).
func NewPeriodicScanner(config PeriodicScanConfig, store ChecksumStore) *PeriodicScanner {
	if config.SampleRate == 0 {
		config.SampleRate = DefaultSampleRate
	}

	return &PeriodicScanner{
		config: config,
		store:  store,
	}
}

// =============================================================================
// Start/Stop
// =============================================================================

// Start begins periodic scanning.
// Returns a channel of changed file paths. The channel is closed when
// the context is cancelled or the scanner is stopped.
func (s *PeriodicScanner) Start(ctx context.Context) (<-chan string, error) {
	if err := s.validateAndInit(); err != nil {
		return nil, err
	}

	changes := make(chan string)
	go s.scanLoop(ctx, changes)

	return changes, nil
}

// validateAndInit validates the configuration and initializes the scanner.
func (s *PeriodicScanner) validateAndInit() error {
	if err := s.config.Validate(); err != nil {
		return err
	}

	if _, err := os.Stat(s.config.RootPath); os.IsNotExist(err) {
		return ErrRootNotExist
	}

	if !s.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}

	s.mu.Lock()
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	return nil
}

// scanLoop runs the periodic scanning cycle.
func (s *PeriodicScanner) scanLoop(ctx context.Context, changes chan<- string) {
	defer close(changes)
	defer s.running.Store(false)

	ticker := time.NewTicker(s.config.Interval)
	defer ticker.Stop()

	// Perform initial scan
	s.performScan(ctx, changes)

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.getStopCh():
			return
		case <-ticker.C:
			s.performScan(ctx, changes)
		}
	}
}

// getStopCh returns the stop channel under lock.
func (s *PeriodicScanner) getStopCh() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopCh
}

// performScan executes a single scan cycle.
func (s *PeriodicScanner) performScan(ctx context.Context, changes chan<- string) {
	if s.paused.Load() {
		return
	}

	files := s.collectFiles()
	sampled := s.sampleFiles(files)

	for _, path := range sampled {
		if s.shouldStop(ctx) {
			return
		}

		if s.paused.Load() {
			return
		}

		if s.hasChanged(path) {
			s.sendChange(ctx, changes, path)
		}
	}
}

// shouldStop checks if scanning should stop.
func (s *PeriodicScanner) shouldStop(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// =============================================================================
// File Collection
// =============================================================================

// collectFiles gathers all files in the root path.
func (s *PeriodicScanner) collectFiles() []string {
	var files []string

	_ = filepath.WalkDir(s.config.RootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if d.IsDir() {
			if isExcludedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		files = append(files, path)
		return nil
	})

	return files
}

// isExcludedDir checks if a directory should be skipped.
func isExcludedDir(name string) bool {
	excluded := map[string]struct{}{
		".git":         {},
		"node_modules": {},
		"vendor":       {},
		"__pycache__":  {},
		".next":        {},
		"dist":         {},
		"build":        {},
		".cache":       {},
		"target":       {},
	}
	_, ok := excluded[name]
	return ok
}

// sampleFiles selects a random sample of files based on the sample rate.
func (s *PeriodicScanner) sampleFiles(files []string) []string {
	if s.config.SampleRate >= 1.0 {
		return files
	}

	sampleSize := int(float64(len(files)) * s.config.SampleRate)
	if sampleSize == 0 && len(files) > 0 {
		sampleSize = 1 // Always check at least one file
	}

	// Fisher-Yates shuffle for random sample
	shuffled := make([]string, len(files))
	copy(shuffled, files)

	for i := len(shuffled) - 1; i > 0; i-- {
		j := rand.IntN(i + 1)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	return shuffled[:sampleSize]
}

// =============================================================================
// Change Detection
// =============================================================================

// hasChanged determines if a file has changed compared to the checksum store.
func (s *PeriodicScanner) hasChanged(path string) bool {
	if s.store == nil {
		return true // No store means everything is "new"
	}

	storedChecksum, exists := s.store.GetChecksum(path)
	if !exists {
		return true // File not in store
	}

	currentChecksum, err := ComputeChecksum(path)
	if err != nil {
		return false // Can't read file, don't report as changed
	}

	return currentChecksum != storedChecksum
}

// sendChange sends a change notification to the channel.
func (s *PeriodicScanner) sendChange(ctx context.Context, changes chan<- string, path string) {
	select {
	case <-ctx.Done():
		return
	case changes <- path:
	}
}

// =============================================================================
// Pause/Resume
// =============================================================================

// Pause temporarily stops scanning.
// This is used to yield to higher-priority active operations.
func (s *PeriodicScanner) Pause() {
	s.paused.Store(true)
}

// Resume continues scanning after a pause.
func (s *PeriodicScanner) Resume() {
	s.paused.Store(false)
}

// IsPaused returns whether the scanner is currently paused.
func (s *PeriodicScanner) IsPaused() bool {
	return s.paused.Load()
}
