// Package watcher provides file watching and validation functionality for the
// Sylk Document Search System. It supports file integrity verification through
// content hashing.
package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"
)

// =============================================================================
// Constants
// =============================================================================

// bufferSize is the buffer size for streaming file reads (32KB).
const bufferSize = 32 * 1024

// maxConcurrentValidations limits concurrent file validations to prevent
// file handle exhaustion.
const maxConcurrentValidations = 16

// =============================================================================
// ValidationStatus
// =============================================================================

// ValidationStatus represents the outcome of a file validation.
type ValidationStatus int

const (
	// StatusValid indicates the file checksum matches the stored value.
	StatusValid ValidationStatus = iota

	// StatusChanged indicates the file content has changed.
	StatusChanged

	// StatusMissing indicates the file is not in the checksum store (not indexed).
	StatusMissing

	// StatusError indicates an error occurred during validation.
	StatusError
)

// =============================================================================
// ValidationResult
// =============================================================================

// ValidationResult holds the result of validating a single file.
type ValidationResult struct {
	// Path is the file path that was validated.
	Path string

	// Status is the validation outcome.
	Status ValidationStatus

	// ExpectedHash is the checksum from the store (empty if not found).
	ExpectedHash string

	// ActualHash is the computed checksum (empty if error occurred).
	ActualHash string

	// Error contains any error that occurred during validation.
	Error error
}

// IsValid returns true if the file checksum matches the stored value.
func (r *ValidationResult) IsValid() bool {
	return r.Status == StatusValid
}

// =============================================================================
// ChecksumStore Interface
// =============================================================================

// ChecksumStore provides access to stored checksums for comparison.
type ChecksumStore interface {
	// GetChecksum returns the stored checksum for a path.
	// Returns empty string and false if the path is not in the store.
	GetChecksum(path string) (string, bool)
}

// =============================================================================
// ChecksumValidator
// =============================================================================

// ChecksumValidator verifies file integrity via content hashing.
type ChecksumValidator struct {
	store ChecksumStore
}

// NewChecksumValidator creates a new validator with the given checksum store.
func NewChecksumValidator(store ChecksumStore) *ChecksumValidator {
	return &ChecksumValidator{store: store}
}

// =============================================================================
// Single File Validation
// =============================================================================

// Validate checks a single file against its stored checksum.
// Uses streaming SHA-256 to handle large files efficiently.
func (v *ChecksumValidator) Validate(path string) *ValidationResult {
	result := &ValidationResult{Path: path}

	expected, found := v.store.GetChecksum(path)
	if !found {
		result.Status = StatusMissing
		return result
	}
	result.ExpectedHash = expected

	actual, err := ComputeChecksum(path)
	if err != nil {
		result.Status = StatusError
		result.Error = err
		return result
	}
	result.ActualHash = actual

	result.Status = determineStatus(expected, actual)
	return result
}

// determineStatus compares hashes and returns the appropriate status.
func determineStatus(expected, actual string) ValidationStatus {
	if expected == actual {
		return StatusValid
	}
	return StatusChanged
}

// =============================================================================
// Batch Validation
// =============================================================================

// ValidateBatch validates multiple files concurrently.
// Returns results in the same order as input paths.
// Respects context cancellation for early termination.
func (v *ChecksumValidator) ValidateBatch(ctx context.Context, paths []string) []*ValidationResult {
	if len(paths) == 0 {
		return nil
	}

	results := make([]*ValidationResult, len(paths))
	jobs := make(chan validationJob, len(paths))
	var wg sync.WaitGroup

	workerCount := min(maxConcurrentValidations, len(paths))
	wg.Add(workerCount)

	for i := 0; i < workerCount; i++ {
		go v.validationWorker(ctx, jobs, results, &wg)
	}

	v.enqueueJobs(ctx, paths, jobs)

	wg.Wait()

	return results
}

// validationJob represents a single validation task.
type validationJob struct {
	index int
	path  string
}

// validationWorker processes validation jobs from the jobs channel.
func (v *ChecksumValidator) validationWorker(
	ctx context.Context,
	jobs <-chan validationJob,
	results []*ValidationResult,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	for job := range jobs {
		if ctx.Err() != nil {
			continue // Drain remaining jobs but skip processing
		}
		results[job.index] = v.Validate(job.path)
	}
}

// enqueueJobs sends validation jobs to the worker pool.
func (v *ChecksumValidator) enqueueJobs(ctx context.Context, paths []string, jobs chan<- validationJob) {
	defer close(jobs)

	for i, path := range paths {
		if ctx.Err() != nil {
			return
		}
		jobs <- validationJob{index: i, path: path}
	}
}

// =============================================================================
// Checksum Computation
// =============================================================================

// ComputeChecksum computes SHA-256 of a file using streaming IO.
// Memory-efficient: uses 32KB buffer, doesn't load entire file.
func ComputeChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	return computeHashFromReader(file)
}

// computeHashFromReader computes SHA-256 hash from a reader.
func computeHashFromReader(r io.Reader) (string, error) {
	hasher := sha256.New()
	buffer := make([]byte, bufferSize)

	if _, err := io.CopyBuffer(hasher, r, buffer); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// =============================================================================
// Utility Functions
// =============================================================================

// min returns the smaller of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
