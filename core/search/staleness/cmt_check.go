package staleness

import (
	"context"
	"sync"
	"time"
)

// =============================================================================
// CMT Interface (to avoid circular imports)
// =============================================================================

// HashSize is the size of SHA-256 hashes in bytes.
const HashSize = 32

// Hash represents a SHA-256 hash.
type Hash [HashSize]byte

// IsZero returns true if the hash is all zeros.
func (h Hash) IsZero() bool {
	for _, b := range h {
		if b != 0 {
			return false
		}
	}
	return true
}

// CMTHashProvider provides access to CMT root hash.
// This interface allows for dependency injection and avoids circular imports.
type CMTHashProvider interface {
	// RootHash returns the current root Merkle hash.
	RootHash() Hash

	// Size returns the number of entries in the tree.
	Size() int64
}

// =============================================================================
// CMTRootCheck
// =============================================================================

// CMTRootCheck compares stored vs computed CMT root hash.
// This is an O(1) operation that instantly detects any changes.
type CMTRootCheck struct {
	cmt        CMTHashProvider
	storedHash Hash
	mu         sync.RWMutex
}

// NewCMTRootCheck creates a new CMT root hash checker.
// Returns an error if cmt is nil.
func NewCMTRootCheck(cmt CMTHashProvider) (*CMTRootCheck, error) {
	if cmt == nil {
		return nil, ErrCMTUnavailable
	}

	return &CMTRootCheck{
		cmt:        cmt,
		storedHash: cmt.RootHash(),
	}, nil
}

// Check compares the stored root hash with the current CMT root hash.
// Returns a StalenessReport indicating if the index is stale.
// This is an O(1) operation.
func (c *CMTRootCheck) Check(ctx context.Context) (*StalenessReport, error) {
	start := time.Now()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.mu.RLock()
	storedHash := c.storedHash
	c.mu.RUnlock()

	currentHash := c.cmt.RootHash()
	duration := time.Since(start)

	return c.buildResult(storedHash, currentHash, duration), nil
}

// buildResult constructs a StalenessReport from hash comparison.
func (c *CMTRootCheck) buildResult(stored, current Hash, duration time.Duration) *StalenessReport {
	if stored == current {
		return NewFreshReport(CMTRoot, duration)
	}

	return c.buildStaleResult(duration)
}

// buildStaleResult creates a stale report when hashes differ.
func (c *CMTRootCheck) buildStaleResult(duration time.Duration) *StalenessReport {
	return NewStaleReport(
		ModeratelyStale,
		[]StaleFile{},
		time.Time{}, // Unknown when staleness began
		1.0,         // Hash comparison is definitive
		CMTRoot,
		duration,
	)
}

// UpdateStoredHash updates the stored hash to the current CMT root hash.
// Call this after the index has been refreshed.
func (c *CMTRootCheck) UpdateStoredHash() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.storedHash = c.cmt.RootHash()
}

// SetStoredHash sets the stored hash to a specific value.
// Useful for restoring state from persistent storage.
func (c *CMTRootCheck) SetStoredHash(hash Hash) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.storedHash = hash
}

// StoredHash returns the currently stored hash.
func (c *CMTRootCheck) StoredHash() Hash {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.storedHash
}

// CurrentHash returns the current CMT root hash.
func (c *CMTRootCheck) CurrentHash() Hash {
	return c.cmt.RootHash()
}

// IsEmpty returns true if the CMT has no entries.
func (c *CMTRootCheck) IsEmpty() bool {
	return c.cmt.Size() == 0
}
