// Package mvcc provides Multi-Version Concurrency Control (MVCC) primitives
// for the vector graph database. It enables concurrent read/write access
// to documents by maintaining version chains where each document can have
// multiple versions with different visibility based on transaction timestamps.
package mvcc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Common errors for version chain operations
var (
	ErrVersionNotFound      = errors.New("version not found in chain")
	ErrVersionCommitted     = errors.New("version already committed")
	ErrVersionUncommitted   = errors.New("version is uncommitted")
	ErrTxnNotFound          = errors.New("transaction not found")
	ErrTxnNotActive         = errors.New("transaction is not active")
	ErrWriteConflict        = errors.New("write-write conflict detected")
	ErrTransactionClosed    = errors.New("transaction is closed (committed or aborted)")
	ErrTransactionAborted   = errors.New("transaction has been aborted")
	ErrTransactionCommitted = errors.New("transaction has been committed")
)

// writeIntent represents a pending write in a transaction's write set.
// It tracks both the value to be written and whether this is a delete operation.
type writeIntent struct {
	value    []byte // The value to write (nil for deletes)
	isDelete bool   // True if this is a delete operation
}

// TransactionState represents the current state of a transaction
type TransactionState int

const (
	TxnStateActive TransactionState = iota
	TxnStateCommitting
	TxnStateCommitted
	TxnStateAborted
)

// String returns a human-readable representation of the transaction state
func (s TransactionState) String() string {
	switch s {
	case TxnStateActive:
		return "Active"
	case TxnStateCommitting:
		return "Committing"
	case TxnStateCommitted:
		return "Committed"
	case TxnStateAborted:
		return "Aborted"
	default:
		return "Unknown"
	}
}

// Transaction represents an active MVCC transaction.
// It tracks the read and write sets for optimistic concurrency control,
// and maintains state throughout the transaction lifecycle.
type Transaction struct {
	ID        uint64                  // Unique transaction ID
	StartTS   uint64                  // Read timestamp (snapshot point)
	State     TransactionState        // Current state
	ReadSet   map[string]uint64       // docID -> version txnID read
	WriteSet  map[string]*Version     // docID -> uncommitted version
	mu        sync.Mutex              // Protects state changes
	createdAt time.Time               // Wall clock time for debugging/monitoring

	// Fields for new Transaction API (MVCC.8-10)
	store     *MVCCStore              // Reference to the store for chain access
	writes    map[string]*writeIntent // key -> pending write intent
	commitTS  uint64                  // Commit timestamp (set during commit)
	committed atomic.Bool             // True if transaction has committed
	aborted   atomic.Bool             // True if transaction has aborted
}

// NewTransaction creates a new active transaction with the given ID and start timestamp.
// The transaction is initialized with empty read and write sets.
func NewTransaction(id uint64, startTS uint64) *Transaction {
	return &Transaction{
		ID:        id,
		StartTS:   startTS,
		State:     TxnStateActive,
		ReadSet:   make(map[string]uint64),
		WriteSet:  make(map[string]*Version),
		writes:    make(map[string]*writeIntent),
		createdAt: time.Now(),
	}
}

// NewTransactionWithStore creates a new active transaction with a reference to the MVCC store.
// This enables the use of the Get/Put/Delete/Commit/Rollback API (MVCC.8-10).
func NewTransactionWithStore(id uint64, startTS uint64, store *MVCCStore) *Transaction {
	return &Transaction{
		ID:        id,
		StartTS:   startTS,
		State:     TxnStateActive,
		ReadSet:   make(map[string]uint64),
		WriteSet:  make(map[string]*Version),
		writes:    make(map[string]*writeIntent),
		store:     store,
		createdAt: time.Now(),
	}
}

// AddToReadSet records that this transaction read a specific version of a document.
// The versionTxnID is the transaction ID of the version that was read.
func (t *Transaction) AddToReadSet(docID string, versionTxnID uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ReadSet[docID] = versionTxnID
}

// AddToWriteSet records that this transaction created a new version for a document.
// The version should be uncommitted at this point.
func (t *Transaction) AddToWriteSet(docID string, v *Version) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.WriteSet[docID] = v
}

// GetFromWriteSet returns the uncommitted version for a document in this transaction's
// write set. Returns nil if the document has not been written by this transaction.
func (t *Transaction) GetFromWriteSet(docID string) *Version {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.WriteSet[docID]
}

// CanCommit checks if this transaction can safely commit without conflicts.
// It verifies that none of the versions read by this transaction have been
// overwritten by other committed transactions. This implements optimistic
// concurrency control validation.
//
// Returns false if any read version has been superseded by a newer committed version.
func (t *Transaction) CanCommit(chains map[string]*VersionChain) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	for docID, readVersionTxnID := range t.ReadSet {
		chain, exists := chains[docID]
		if !exists {
			// Chain was deleted - conflict
			continue
		}

		// Get the latest committed version in the chain
		latestCommitted := chain.GetLatestCommitted()
		if latestCommitted == nil {
			// No committed version exists
			// If we read a version (readVersionTxnID != 0), there's a conflict
			if readVersionTxnID != 0 {
				return false
			}
			continue
		}

		// Check if a newer version was committed after our read
		// Our read was valid if the latest committed version's txnID matches what we read
		if latestCommitted.TxnID != readVersionTxnID {
			// Someone else committed a newer version - conflict
			return false
		}
	}

	return true
}

// IsActive returns true if the transaction is still in the active state.
func (t *Transaction) IsActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.State == TxnStateActive
}

// SetState atomically updates the transaction state.
func (t *Transaction) SetState(state TransactionState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.State = state
}

// GetState returns the current transaction state.
func (t *Transaction) GetState() TransactionState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.State
}

// CreatedAt returns the wall clock time when the transaction was created.
func (t *Transaction) CreatedAt() time.Time {
	return t.createdAt
}

// =============================================================================
// Transaction API (MVCC.8-10): Get/Put/Delete/Commit/Rollback
// =============================================================================

// Get retrieves a value at the transaction's snapshot timestamp.
// Returns the value and true if found, nil and false otherwise.
// If a pending write exists in this transaction, it returns that value.
func (tx *Transaction) Get(key string) ([]byte, bool) {
	// Check if we have a pending write in this transaction
	tx.mu.Lock()
	pending, ok := tx.writes[key]
	tx.mu.Unlock()

	if ok {
		if pending.isDelete {
			return nil, false
		}
		// Return a copy of the pending value
		valueCopy := make([]byte, len(pending.value))
		copy(valueCopy, pending.value)
		return valueCopy, true
	}

	// Read from the version chain at our snapshot timestamp
	if tx.store == nil {
		return nil, false
	}

	chain := tx.store.getChain(key)
	if chain == nil {
		return nil, false
	}

	// Use the existing Read method which handles visibility correctly
	data, err := chain.Read(tx)
	if err != nil || data == nil {
		return nil, false
	}
	return data, true
}

// Put stores a value in the transaction's write set.
// The value is not visible to other transactions until Commit.
func (tx *Transaction) Put(key string, value []byte) error {
	if tx.committed.Load() || tx.aborted.Load() {
		return ErrTransactionClosed
	}

	// Make a copy of the value
	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)

	// Record the write intent
	tx.mu.Lock()
	tx.writes[key] = &writeIntent{
		value:    valueCopy,
		isDelete: false,
	}
	tx.mu.Unlock()

	return nil
}

// Delete marks a key for deletion in the transaction's write set.
// The deletion is not visible to other transactions until Commit.
func (tx *Transaction) Delete(key string) error {
	if tx.committed.Load() || tx.aborted.Load() {
		return ErrTransactionClosed
	}

	tx.mu.Lock()
	tx.writes[key] = &writeIntent{
		value:    nil,
		isDelete: true,
	}
	tx.mu.Unlock()

	return nil
}

// Commit atomically applies all writes to the version chains.
// Returns ErrWriteConflict if any write conflicts with concurrent transactions.
// Returns ErrTransactionAborted if the transaction was already aborted.
func (tx *Transaction) Commit() error {
	if tx.committed.Load() {
		return nil // Already committed
	}
	if tx.aborted.Load() {
		return ErrTransactionAborted
	}

	if tx.store == nil {
		return ErrTransactionClosed
	}

	// Get snapshot of writes to process
	tx.mu.Lock()
	writesCopy := make(map[string]*writeIntent, len(tx.writes))
	for k, v := range tx.writes {
		writesCopy[k] = v
	}
	tx.mu.Unlock()

	// Phase 1: Apply all writes to version chains (checks for conflicts)
	for key, intent := range writesCopy {
		chain := tx.store.getOrCreateChain(key)

		if intent.isDelete {
			if err := chain.Delete(tx); err != nil {
				tx.Rollback()
				return err
			}
		} else {
			if err := chain.Write(tx, intent.value); err != nil {
				tx.Rollback()
				return err
			}
		}
	}

	// Phase 2: Mark committed (makes writes visible)
	tx.commitTS = tx.store.getNextTimestamp()
	tx.committed.Store(true)
	tx.SetState(TxnStateCommitted)

	// Phase 3: Finalize versions with commit timestamp
	for key := range writesCopy {
		chain := tx.store.getChain(key)
		if chain != nil {
			chain.CommitPending(tx)
		}
	}

	return nil
}

// Rollback discards all pending writes and aborts the transaction.
// Returns ErrTransactionCommitted if the transaction was already committed.
func (tx *Transaction) Rollback() error {
	if tx.committed.Load() {
		return ErrTransactionCommitted
	}

	if tx.aborted.Swap(true) {
		return nil // Already aborted
	}

	tx.SetState(TxnStateAborted)

	// Remove any pending versions from chains
	if tx.store != nil {
		tx.mu.Lock()
		writesCopy := make(map[string]*writeIntent, len(tx.writes))
		for k, v := range tx.writes {
			writesCopy[k] = v
		}
		tx.mu.Unlock()

		for key := range writesCopy {
			chain := tx.store.getChain(key)
			if chain != nil {
				chain.AbortPending(tx)
			}
		}
	}

	// Clear the write set
	tx.mu.Lock()
	tx.writes = nil
	tx.mu.Unlock()

	return nil
}

// Version represents a single version of a document in the version chain.
// Each version is created by a transaction and becomes visible to other
// transactions only after the creating transaction commits.
type Version struct {
	TxnID        uint64    // Transaction that created this version
	CommitTS     uint64    // Commit timestamp (0 if uncommitted)
	Data         []byte    // Serialized document data
	DeleteMarker bool      // True if this version represents a deletion
	Next         *Version  // Pointer to older version (nil = oldest)
	CreatedAt    time.Time // Wall clock time for debugging/GC
}

// NewVersion creates a new uncommitted version with the given transaction ID,
// data, and deletion flag. The commit timestamp is initially 0, indicating
// the version has not yet been committed.
func NewVersion(txnID uint64, data []byte, isDelete bool) *Version {
	return &Version{
		TxnID:        txnID,
		CommitTS:     0,
		Data:         data,
		DeleteMarker: isDelete,
		Next:         nil,
		CreatedAt:    time.Now(),
	}
}

// IsCommitted returns true if this version has been committed.
// A version is committed when its CommitTS is greater than 0.
func (v *Version) IsCommitted() bool {
	return v.CommitTS > 0
}

// IsVisible returns true if this version is visible to a transaction
// with the given read timestamp. A version is visible if:
//   - It has been committed (CommitTS > 0), AND
//   - Its commit timestamp is less than or equal to the read timestamp
func (v *Version) IsVisible(readTS uint64) bool {
	return v.IsCommitted() && v.CommitTS <= readTS
}

// Clone creates a deep copy of the version. The Next pointer is NOT copied,
// so the cloned version is disconnected from the chain. This is useful for
// returning version data without exposing internal chain structure.
func (v *Version) Clone() *Version {
	var dataCopy []byte
	if v.Data != nil {
		dataCopy = make([]byte, len(v.Data))
		copy(dataCopy, v.Data)
	}

	return &Version{
		TxnID:        v.TxnID,
		CommitTS:     v.CommitTS,
		Data:         dataCopy,
		DeleteMarker: v.DeleteMarker,
		Next:         nil, // Intentionally not copying the chain
		CreatedAt:    v.CreatedAt,
	}
}

// VersionChain manages the linked list of versions for a single document.
// Newer versions are at the head of the chain, with older versions linked
// via Next pointers. The chain supports concurrent access through a RWMutex.
type VersionChain struct {
	mu     sync.RWMutex
	DocID  string   // Document identifier
	Head   *Version // Most recent version (may be uncommitted)
	Length int      // Number of versions in chain
}

// NewVersionChain creates an empty version chain for a document.
func NewVersionChain(docID string) *VersionChain {
	return &VersionChain{
		DocID:  docID,
		Head:   nil,
		Length: 0,
	}
}

// GetLatest returns the head version of the chain, which may be uncommitted.
// Returns nil if the chain is empty.
func (vc *VersionChain) GetLatest() *Version {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	return vc.Head
}

// GetLatestCommitted returns the most recent committed version in the chain.
// Returns nil if no committed version exists.
func (vc *VersionChain) GetLatestCommitted() *Version {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	current := vc.Head
	for current != nil {
		if current.IsCommitted() {
			return current
		}
		current = current.Next
	}
	return nil
}

// GetVisibleVersion returns the version visible to a transaction with the
// given read timestamp. It walks the chain from newest to oldest and returns
// the first version that satisfies the visibility criteria:
//   - Version is committed (CommitTS > 0)
//   - Version's commit timestamp <= readTS
//
// Returns nil if no version is visible to the given read timestamp.
func (vc *VersionChain) GetVisibleVersion(readTS uint64) *Version {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	return vc.getVisibleVersionLocked(readTS)
}

// getVisibleVersionLocked is the internal implementation of GetVisibleVersion.
// It assumes the caller already holds at least a read lock on vc.mu.
func (vc *VersionChain) getVisibleVersionLocked(readTS uint64) *Version {
	current := vc.Head
	for current != nil {
		if current.IsVisible(readTS) {
			return current
		}
		current = current.Next
	}
	return nil
}

// Read returns the visible version data for a transaction.
// Returns nil, nil if no visible version exists or if the visible version is deleted.
// The transaction's read set is updated to record this read for conflict detection.
func (vc *VersionChain) Read(txn *Transaction) ([]byte, error) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	// Find visible version
	version := vc.getVisibleVersionLocked(txn.StartTS)
	if version == nil {
		return nil, nil // No visible version
	}

	if version.DeleteMarker {
		return nil, nil // Deleted
	}

	// Record in transaction's read set for conflict detection
	txn.AddToReadSet(vc.DocID, version.TxnID)

	// Return copy of data (immutable)
	data := make([]byte, len(version.Data))
	copy(data, version.Data)
	return data, nil
}

// ReadForUpdate reads with write intent (checks for conflicts).
// It returns the visible version data and checks if there's an uncommitted
// version from another transaction, returning ErrWriteConflict if so.
// Returns nil, nil if no visible version exists or if the visible version is deleted.
func (vc *VersionChain) ReadForUpdate(txn *Transaction) ([]byte, error) {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	// Check if there's an uncommitted version from another transaction
	if vc.Head != nil && !vc.Head.IsCommitted() && vc.Head.TxnID != txn.ID {
		return nil, ErrWriteConflict
	}

	version := vc.getVisibleVersionLocked(txn.StartTS)
	if version == nil {
		return nil, nil
	}

	if version.DeleteMarker {
		return nil, nil
	}

	txn.AddToReadSet(vc.DocID, version.TxnID)

	data := make([]byte, len(version.Data))
	copy(data, version.Data)
	return data, nil
}

// Write creates a new uncommitted version at the head of the chain.
// It returns ErrWriteConflict if:
//   - There's an uncommitted version from another transaction, OR
//   - There's a committed version with commitTS > txn.StartTS (another txn committed after we started)
//
// If this transaction already wrote to this chain, it updates the existing version.
func (vc *VersionChain) Write(txn *Transaction, data []byte) error {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	// Check for write-write conflict with uncommitted version
	if vc.Head != nil && !vc.Head.IsCommitted() && vc.Head.TxnID != txn.ID {
		return ErrWriteConflict
	}

	// Check for write-write conflict with recently committed version
	// If there's a committed version with commitTS > our startTS, another transaction
	// committed after we started, which is a conflict
	if vc.Head != nil && vc.Head.IsCommitted() && vc.Head.CommitTS > txn.StartTS {
		return ErrWriteConflict
	}

	// If this transaction already wrote, update its version
	if vc.Head != nil && vc.Head.TxnID == txn.ID && !vc.Head.IsCommitted() {
		vc.Head.Data = make([]byte, len(data))
		copy(vc.Head.Data, data)
		return nil
	}

	// Create new version with a copy of the data
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	version := NewVersion(txn.ID, dataCopy, false)
	version.Next = vc.Head
	vc.Head = version
	vc.Length++

	// Track in transaction write set
	txn.AddToWriteSet(vc.DocID, version)

	return nil
}

// Delete marks the document as deleted for this transaction.
// It returns ErrWriteConflict if:
//   - There's an uncommitted version from another transaction, OR
//   - There's a committed version with commitTS > txn.StartTS (another txn committed after we started)
//
// If this transaction already has an uncommitted version, it converts that version to a delete marker.
func (vc *VersionChain) Delete(txn *Transaction) error {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	// Check for write conflict with uncommitted version
	if vc.Head != nil && !vc.Head.IsCommitted() && vc.Head.TxnID != txn.ID {
		return ErrWriteConflict
	}

	// Check for write conflict with recently committed version
	if vc.Head != nil && vc.Head.IsCommitted() && vc.Head.CommitTS > txn.StartTS {
		return ErrWriteConflict
	}

	// If this transaction already has an uncommitted version, convert to delete
	if vc.Head != nil && vc.Head.TxnID == txn.ID && !vc.Head.IsCommitted() {
		vc.Head.DeleteMarker = true
		vc.Head.Data = nil
		return nil
	}

	// Create delete marker version
	version := NewVersion(txn.ID, nil, true)
	version.Next = vc.Head
	vc.Head = version
	vc.Length++

	txn.AddToWriteSet(vc.DocID, version)

	return nil
}

// Prepend adds a new version at the head of the chain.
// The new version's Next pointer is set to the previous head.
func (vc *VersionChain) Prepend(v *Version) {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	v.Next = vc.Head
	vc.Head = v
	vc.Length++
}

// CommitVersion marks a version as committed with the given timestamp.
// It searches for the version by transaction ID and sets its CommitTS.
// Returns ErrVersionNotFound if no version with the given txnID exists.
// Returns ErrVersionCommitted if the version is already committed.
func (vc *VersionChain) CommitVersion(txnID uint64, commitTS uint64) error {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	current := vc.Head
	for current != nil {
		if current.TxnID == txnID {
			if current.IsCommitted() {
				return ErrVersionCommitted
			}
			current.CommitTS = commitTS
			return nil
		}
		current = current.Next
	}
	return ErrVersionNotFound
}

// AbortVersion removes an uncommitted version from the chain.
// It searches for the version by transaction ID and removes it from the chain.
// Returns ErrVersionNotFound if no version with the given txnID exists.
// Returns ErrVersionCommitted if the version is already committed (cannot abort).
func (vc *VersionChain) AbortVersion(txnID uint64) error {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	// Special case: head is the version to abort
	if vc.Head != nil && vc.Head.TxnID == txnID {
		if vc.Head.IsCommitted() {
			return ErrVersionCommitted
		}
		vc.Head = vc.Head.Next
		vc.Length--
		return nil
	}

	// Search through the chain
	prev := vc.Head
	for prev != nil && prev.Next != nil {
		if prev.Next.TxnID == txnID {
			if prev.Next.IsCommitted() {
				return ErrVersionCommitted
			}
			prev.Next = prev.Next.Next
			vc.Length--
			return nil
		}
		prev = prev.Next
	}

	return ErrVersionNotFound
}

// CommitPending finalizes a pending version by setting its commit timestamp.
// This is called during Transaction.Commit to make writes visible.
// If no uncommitted version exists for the transaction, this is a no-op.
func (vc *VersionChain) CommitPending(tx *Transaction) {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	current := vc.Head
	for current != nil {
		if current.TxnID == tx.ID && !current.IsCommitted() {
			current.CommitTS = tx.commitTS
			return
		}
		current = current.Next
	}
}

// AbortPending removes any uncommitted version created by the given transaction.
// This is called during Transaction.Rollback to discard pending writes.
// If no uncommitted version exists for the transaction, this is a no-op.
func (vc *VersionChain) AbortPending(tx *Transaction) {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	// Special case: head is the version to abort
	if vc.Head != nil && vc.Head.TxnID == tx.ID && !vc.Head.IsCommitted() {
		vc.Head = vc.Head.Next
		vc.Length--
		return
	}

	// Search through the chain
	prev := vc.Head
	for prev != nil && prev.Next != nil {
		if prev.Next.TxnID == tx.ID && !prev.Next.IsCommitted() {
			prev.Next = prev.Next.Next
			vc.Length--
			return
		}
		prev = prev.Next
	}
}

// GarbageCollect removes versions older than minTS that are no longer needed.
// A version is eligible for garbage collection if:
//   - It is committed
//   - Its commit timestamp is less than minTS
//   - There is at least one newer committed version to preserve
//
// This ensures that at least one committed version remains in the chain
// for queries that might still need it. Returns the number of versions removed.
func (vc *VersionChain) GarbageCollect(minTS uint64) int {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	if vc.Head == nil {
		return 0
	}

	removed := 0

	// Find the first committed version that should be kept
	// We need to keep at least one committed version
	var lastKeptCommitted *Version
	current := vc.Head

	// First pass: find where to cut the chain
	// We keep all versions until we find a committed version with commitTS < minTS
	// AND we already have a committed version to keep
	var prev *Version
	for current != nil {
		if current.IsCommitted() {
			if lastKeptCommitted == nil {
				// This is the first committed version - always keep it
				lastKeptCommitted = current
				prev = current
				current = current.Next
				continue
			}

			// We have a committed version to keep, check if this one can be removed
			if current.CommitTS < minTS {
				// Cut the chain here - remove this and all older versions
				chainLength := 0
				for c := current; c != nil; c = c.Next {
					chainLength++
				}
				removed = chainLength
				if prev != nil {
					prev.Next = nil
				}
				vc.Length -= removed
				return removed
			}
		}
		prev = current
		current = current.Next
	}

	return removed
}

// MVCCStore provides MVCC operations over a collection of version chains.
// It manages transactions and their interaction with document version chains,
// providing snapshot isolation for concurrent access. The store includes
// built-in garbage collection capabilities.
type MVCCStore struct {
	chains   map[string]*VersionChain // docID -> chain
	chainsMu sync.RWMutex

	txns   map[uint64]*Transaction // txnID -> transaction
	txnsMu sync.RWMutex

	nextTxnID atomic.Uint64 // Transaction ID generator
	currentTS atomic.Uint64 // Timestamp generator

	gcInterval     time.Duration // How often to run GC
	minRetentionTS uint64        // Minimum timestamp to retain
	gcMetrics      *GCMetrics    // GC statistics
}

// DefaultGCInterval is the default garbage collection interval.
const DefaultGCInterval = 5 * time.Minute

// NewMVCCStore creates a new MVCC store with default settings.
// The store starts with transaction ID 1 and timestamp 1 to reserve
// 0 as a sentinel value for uncommitted/uninitialized states.
func NewMVCCStore() *MVCCStore {
	store := &MVCCStore{
		chains:         make(map[string]*VersionChain),
		txns:           make(map[uint64]*Transaction),
		gcInterval:     DefaultGCInterval,
		minRetentionTS: 0,
		gcMetrics:      &GCMetrics{},
	}
	// Start IDs at 1 to reserve 0 as sentinel
	store.nextTxnID.Store(1)
	store.currentTS.Store(1)
	return store
}

// Begin starts a new transaction with a unique ID and the current timestamp.
// The transaction is registered in the store's transaction map and returned
// in the active state. The transaction has a reference to the store for
// the Get/Put/Delete/Commit/Rollback API.
func (s *MVCCStore) Begin() *Transaction {
	txnID := s.nextTxnID.Add(1) - 1 // Atomically get-and-increment
	startTS := s.currentTS.Load()

	txn := NewTransactionWithStore(txnID, startTS, s)

	s.txnsMu.Lock()
	s.txns[txnID] = txn
	s.txnsMu.Unlock()

	return txn
}

// GetChain returns the version chain for a document, creating one if it doesn't exist.
// This method is safe for concurrent access.
func (s *MVCCStore) GetChain(docID string) *VersionChain {
	// First, try read lock
	s.chainsMu.RLock()
	chain, exists := s.chains[docID]
	s.chainsMu.RUnlock()

	if exists {
		return chain
	}

	// Need to create - acquire write lock
	s.chainsMu.Lock()
	defer s.chainsMu.Unlock()

	// Double-check after acquiring write lock
	if chain, exists = s.chains[docID]; exists {
		return chain
	}

	chain = NewVersionChain(docID)
	s.chains[docID] = chain
	return chain
}

// GetTransaction returns an active transaction by ID.
// Returns nil if the transaction does not exist or is not in the active state.
func (s *MVCCStore) GetTransaction(txnID uint64) *Transaction {
	s.txnsMu.RLock()
	defer s.txnsMu.RUnlock()

	txn, exists := s.txns[txnID]
	if !exists {
		return nil
	}
	return txn
}

// NextTimestamp generates a monotonically increasing timestamp.
// Each call returns a unique timestamp greater than all previous timestamps.
func (s *MVCCStore) NextTimestamp() uint64 {
	return s.currentTS.Add(1)
}

// GetChains returns a copy of the chains map for read-only inspection.
// This is primarily used for transaction validation (CanCommit).
func (s *MVCCStore) GetChains() map[string]*VersionChain {
	s.chainsMu.RLock()
	defer s.chainsMu.RUnlock()

	// Return a shallow copy to prevent external modification of the map
	chainsCopy := make(map[string]*VersionChain, len(s.chains))
	for k, v := range s.chains {
		chainsCopy[k] = v
	}
	return chainsCopy
}

// RemoveTransaction removes a transaction from the store.
// This is called after a transaction is committed or aborted.
func (s *MVCCStore) RemoveTransaction(txnID uint64) {
	s.txnsMu.Lock()
	defer s.txnsMu.Unlock()
	delete(s.txns, txnID)
}

// TransactionCount returns the number of active transactions.
func (s *MVCCStore) TransactionCount() int {
	s.txnsMu.RLock()
	defer s.txnsMu.RUnlock()
	return len(s.txns)
}

// ChainCount returns the number of document chains in the store.
func (s *MVCCStore) ChainCount() int {
	s.chainsMu.RLock()
	defer s.chainsMu.RUnlock()
	return len(s.chains)
}

// SetGCInterval sets the garbage collection interval.
func (s *MVCCStore) SetGCInterval(interval time.Duration) {
	s.gcInterval = interval
}

// SetMinRetentionTS sets the minimum timestamp for version retention.
// Versions older than this timestamp may be garbage collected.
func (s *MVCCStore) SetMinRetentionTS(ts uint64) {
	s.minRetentionTS = ts
}

// getChain returns the version chain for a key, or nil if it doesn't exist.
// This is an internal method used by Transaction.Get.
func (s *MVCCStore) getChain(key string) *VersionChain {
	s.chainsMu.RLock()
	defer s.chainsMu.RUnlock()
	return s.chains[key]
}

// getOrCreateChain returns the version chain for a key, creating one if needed.
// This is an internal method used by Transaction.Commit.
func (s *MVCCStore) getOrCreateChain(key string) *VersionChain {
	return s.GetChain(key) // Reuse existing implementation
}

// getNextTimestamp returns a new monotonically increasing timestamp.
// This is an internal method used by Transaction.Commit.
func (s *MVCCStore) getNextTimestamp() uint64 {
	return s.NextTimestamp() // Reuse existing implementation
}

// =============================================================================
// Garbage Collection (MVCC.11)
// =============================================================================

// GCConfig configures garbage collection behavior.
type GCConfig struct {
	MinVersionsToKeep int           // Minimum versions to retain per key
	MaxVersionAge     time.Duration // Maximum age before GC eligible
	BatchSize         int           // Versions to process per GC cycle
	Interval          time.Duration // How often to run GC
}

// DefaultGCConfig returns a reasonable default configuration for garbage collection.
func DefaultGCConfig() GCConfig {
	return GCConfig{
		MinVersionsToKeep: 1,
		MaxVersionAge:     24 * time.Hour,
		BatchSize:         1000,
		Interval:          5 * time.Minute,
	}
}

// GCMetrics tracks garbage collection statistics.
type GCMetrics struct {
	CyclesRun        atomic.Uint64 // Total GC cycles executed
	VersionsCollected atomic.Uint64 // Total versions garbage collected
	ChainsScanned    atomic.Uint64 // Total chains scanned
	LastRunTime      atomic.Int64  // Unix timestamp of last GC run
	LastRunDuration  atomic.Int64  // Duration of last run in nanoseconds
	SkippedActive    atomic.Uint64 // Versions skipped due to active readers
}

// Reset clears all GC metrics.
func (m *GCMetrics) Reset() {
	m.CyclesRun.Store(0)
	m.VersionsCollected.Store(0)
	m.ChainsScanned.Store(0)
	m.LastRunTime.Store(0)
	m.LastRunDuration.Store(0)
	m.SkippedActive.Store(0)
}

// Snapshot returns a copy of the current metrics values.
func (m *GCMetrics) Snapshot() GCMetricsSnapshot {
	return GCMetricsSnapshot{
		CyclesRun:         m.CyclesRun.Load(),
		VersionsCollected: m.VersionsCollected.Load(),
		ChainsScanned:     m.ChainsScanned.Load(),
		LastRunTime:       time.Unix(0, m.LastRunTime.Load()),
		LastRunDuration:   time.Duration(m.LastRunDuration.Load()),
		SkippedActive:     m.SkippedActive.Load(),
	}
}

// GCMetricsSnapshot is a point-in-time snapshot of GC metrics.
type GCMetricsSnapshot struct {
	CyclesRun         uint64
	VersionsCollected uint64
	ChainsScanned     uint64
	LastRunTime       time.Time
	LastRunDuration   time.Duration
	SkippedActive     uint64
}

// GCResult contains the results of a single GC cycle.
type GCResult struct {
	ChainsScanned     int
	VersionsCollected int
	SkippedActive     int
	Duration          time.Duration
}

// StartGC begins background garbage collection.
// The GC goroutine will run until the context is canceled.
func (s *MVCCStore) StartGC(ctx context.Context, config GCConfig) {
	go s.gcLoop(ctx, config)
}

// gcLoop runs the garbage collection loop.
func (s *MVCCStore) gcLoop(ctx context.Context, config GCConfig) {
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunGCCycle(config)
		}
	}
}

// RunGCCycle collects old versions that are no longer needed.
// It returns the result of the GC cycle including metrics.
func (s *MVCCStore) RunGCCycle(config GCConfig) GCResult {
	startTime := time.Now()
	result := GCResult{}

	// Calculate cutoff timestamp based on max version age
	cutoff := startTime.Add(-config.MaxVersionAge)
	cutoffTS := s.timeToTimestamp(cutoff)

	// Get the oldest active transaction timestamp to protect versions
	// that active readers might still need
	oldestActiveTS := s.getOldestActiveTransactionTS()

	// Use the more conservative of the two cutoffs
	// This ensures we don't GC versions that active transactions need
	effectiveCutoff := cutoffTS
	if oldestActiveTS > 0 && oldestActiveTS < effectiveCutoff {
		effectiveCutoff = oldestActiveTS
	}

	// Collect all chain keys to process
	s.chainsMu.RLock()
	chainKeys := make([]string, 0, len(s.chains))
	for key := range s.chains {
		chainKeys = append(chainKeys, key)
	}
	s.chainsMu.RUnlock()

	versionsProcessed := 0

	// Process each chain
	for _, key := range chainKeys {
		if config.BatchSize > 0 && versionsProcessed >= config.BatchSize {
			break
		}

		s.chainsMu.RLock()
		chain, exists := s.chains[key]
		s.chainsMu.RUnlock()

		if !exists {
			continue
		}

		collected, skipped := s.gcChain(chain, effectiveCutoff, oldestActiveTS, config.MinVersionsToKeep)
		result.VersionsCollected += collected
		result.SkippedActive += skipped
		result.ChainsScanned++
		versionsProcessed += collected
	}

	result.Duration = time.Since(startTime)

	// Update metrics
	s.gcMetrics.CyclesRun.Add(1)
	s.gcMetrics.VersionsCollected.Add(uint64(result.VersionsCollected))
	s.gcMetrics.ChainsScanned.Add(uint64(result.ChainsScanned))
	s.gcMetrics.LastRunTime.Store(startTime.UnixNano())
	s.gcMetrics.LastRunDuration.Store(int64(result.Duration))
	s.gcMetrics.SkippedActive.Add(uint64(result.SkippedActive))

	return result
}

// gcChain garbage collects old versions from a single chain.
// Returns the number of versions collected and the number skipped due to active readers.
func (s *MVCCStore) gcChain(chain *VersionChain, cutoffTS uint64, oldestActiveTS uint64, minKeep int) (int, int) {
	chain.mu.Lock()
	defer chain.mu.Unlock()

	if chain.Head == nil {
		return 0, 0
	}

	// Count committed versions to ensure we keep minimum
	committedCount := 0
	curr := chain.Head
	for curr != nil {
		if curr.IsCommitted() {
			committedCount++
		}
		curr = curr.Next
	}

	if committedCount <= minKeep {
		// Not enough committed versions to GC any
		return 0, 0
	}

	collected := 0
	skipped := 0
	keptCommitted := 0

	// Walk the chain and remove eligible versions
	var prev *Version
	curr = chain.Head

	for curr != nil {
		next := curr.Next

		// Never remove uncommitted versions
		if !curr.IsCommitted() {
			prev = curr
			curr = next
			continue
		}

		// Track committed versions
		keptCommitted++

		// Check if this version is eligible for GC
		isEligible := curr.CommitTS < cutoffTS && keptCommitted > minKeep

		// Skip if needed by active readers
		if isEligible && oldestActiveTS > 0 && curr.CommitTS >= oldestActiveTS {
			skipped++
			prev = curr
			curr = next
			continue
		}

		// Remove if eligible
		if isEligible {
			if prev != nil {
				prev.Next = next
			} else {
				chain.Head = next
			}
			chain.Length--
			collected++
			keptCommitted--
			// Don't update prev, as we removed curr
			curr = next
			continue
		}

		prev = curr
		curr = next
	}

	return collected, skipped
}

// timeToTimestamp converts a wall clock time to a MVCC timestamp.
// This uses a simple approximation based on the current timestamp
// and the time difference from now.
func (s *MVCCStore) timeToTimestamp(t time.Time) uint64 {
	// Get current state
	currentTS := s.currentTS.Load()
	now := time.Now()

	// Calculate time difference
	diff := now.Sub(t)

	// Approximate: assume 1000 transactions per second
	// This is a rough heuristic; in production you might track actual rates
	txnsPerSecond := uint64(1000)
	tsAge := uint64(diff.Seconds()) * txnsPerSecond

	if tsAge >= currentTS {
		return 0
	}
	return currentTS - tsAge
}

// getOldestActiveTransactionTS returns the start timestamp of the oldest active transaction.
// This is used to ensure GC doesn't remove versions that active readers need.
// Returns 0 if there are no active transactions.
func (s *MVCCStore) getOldestActiveTransactionTS() uint64 {
	s.txnsMu.RLock()
	defer s.txnsMu.RUnlock()

	var oldest uint64 = 0
	for _, txn := range s.txns {
		if txn.GetState() == TxnStateActive {
			if oldest == 0 || txn.StartTS < oldest {
				oldest = txn.StartTS
			}
		}
	}
	return oldest
}

// GetGCMetrics returns the current GC metrics.
func (s *MVCCStore) GetGCMetrics() *GCMetrics {
	return s.gcMetrics
}

// GCChainWithMinTS performs garbage collection on a specific chain using
// a minimum timestamp threshold. This is useful for testing and manual GC.
func (s *MVCCStore) GCChainWithMinTS(docID string, minTS uint64, minKeep int) int {
	s.chainsMu.RLock()
	chain, exists := s.chains[docID]
	s.chainsMu.RUnlock()

	if !exists {
		return 0
	}

	collected, _ := s.gcChain(chain, minTS, 0, minKeep)
	return collected
}
