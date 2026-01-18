package vectorgraphdb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrAlreadyCommitted is returned when Commit is called on an already-committed transaction.
var ErrAlreadyCommitted = errors.New("transaction already committed")

// ErrAlreadyRolledBack is returned when operations are attempted on a rolled-back transaction.
var ErrAlreadyRolledBack = errors.New("transaction already rolled back")

// OptimisticTx implements optimistic concurrency control transactions.
// It buffers reads and writes, validating at commit time that no concurrent
// modifications have occurred.
type OptimisticTx struct {
	store      *VersionedNodeStore
	sessionID  string
	readSet    map[string]uint64     // nodeID -> version at read time
	writeSet   map[string]*GraphNode // buffered writes
	committed  bool
	rolledBack bool
	mu         sync.Mutex
}

// BeginOptimistic starts a new optimistic transaction.
func (store *VersionedNodeStore) BeginOptimistic(sessionID string) *OptimisticTx {
	return &OptimisticTx{
		store:     store,
		sessionID: sessionID,
		readSet:   make(map[string]uint64),
		writeSet:  make(map[string]*GraphNode),
	}
}

// Read fetches a node and records its version for later validation.
// If the node was already read in this transaction, returns the same version.
// If the node was written in this transaction, returns the buffered write.
func (tx *OptimisticTx) Read(nodeID string) (*GraphNode, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if err := tx.checkTxState(); err != nil {
		return nil, err
	}

	return tx.readNodeLocked(nodeID)
}

// readNodeLocked performs the actual read operation (must hold lock).
func (tx *OptimisticTx) readNodeLocked(nodeID string) (*GraphNode, error) {
	// Check if we have a buffered write for this node
	if node, ok := tx.writeSet[nodeID]; ok {
		return node, nil
	}

	return tx.fetchAndRecordVersion(nodeID)
}

// fetchAndRecordVersion fetches node from store and records its version.
func (tx *OptimisticTx) fetchAndRecordVersion(nodeID string) (*GraphNode, error) {
	node, err := tx.store.GetNodeWithVersion(nodeID)
	if err != nil {
		return nil, err
	}

	// Record version if not already recorded
	if _, ok := tx.readSet[nodeID]; !ok {
		tx.readSet[nodeID] = node.Version
	}

	return node, nil
}

// Write buffers a node modification for later commit.
// The write is not applied until Commit is called.
func (tx *OptimisticTx) Write(node *GraphNode) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if err := tx.checkTxState(); err != nil {
		return err
	}

	tx.writeSet[node.ID] = node
	return nil
}

// checkTxState verifies the transaction is in a valid state for operations.
func (tx *OptimisticTx) checkTxState() error {
	if tx.committed {
		return ErrAlreadyCommitted
	}
	if tx.rolledBack {
		return ErrAlreadyRolledBack
	}
	return nil
}

// Commit validates all reads and applies writes atomically.
// Phase 1: Validate all reads still have the same version.
// Phase 2: Apply writes with CAS (version check).
// Returns ConflictError if validation fails.
func (tx *OptimisticTx) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if err := tx.checkTxState(); err != nil {
		return err
	}

	return tx.executeCommit()
}

// executeCommit performs the actual commit (must hold lock).
func (tx *OptimisticTx) executeCommit() error {
	// Phase 1: Validate all reads
	if err := tx.validateReadSet(); err != nil {
		return err
	}

	// Phase 2: Apply writes with CAS
	if err := tx.applyWrites(); err != nil {
		return err
	}

	tx.committed = true
	return nil
}

// validateReadSet checks that all read nodes still have their expected versions.
func (tx *OptimisticTx) validateReadSet() error {
	for nodeID, readVersion := range tx.readSet {
		if err := tx.validateSingleRead(nodeID, readVersion); err != nil {
			return err
		}
	}
	return nil
}

// validateSingleRead validates a single node's version.
func (tx *OptimisticTx) validateSingleRead(nodeID string, readVersion uint64) error {
	currentVersion, err := tx.store.GetNodeVersion(nodeID)
	if err == ErrNodeNotFound {
		return NewDeletedConflictError(nodeID, tx.sessionID)
	}
	if err != nil {
		return fmt.Errorf("validate read: %w", err)
	}
	if currentVersion != readVersion {
		return NewStaleConflictError(nodeID, readVersion, currentVersion, tx.sessionID)
	}
	return nil
}

// applyWrites applies all buffered writes within a SQL transaction.
func (tx *OptimisticTx) applyWrites() error {
	if len(tx.writeSet) == 0 {
		return nil
	}

	return tx.executeWritesInTx()
}

// executeWritesInTx executes all writes within a SQL transaction.
func (tx *OptimisticTx) executeWritesInTx() error {
	sqlTx, err := tx.store.db.BeginTx()
	if err != nil {
		return fmt.Errorf("begin sql tx: %w", err)
	}
	defer sqlTx.Rollback()

	if err := tx.applyAllWrites(sqlTx); err != nil {
		return err
	}

	return sqlTx.Commit()
}

// applyAllWrites applies each write in the write set.
func (tx *OptimisticTx) applyAllWrites(sqlTx *sql.Tx) error {
	for _, node := range tx.writeSet {
		if err := tx.applySingleWrite(sqlTx, node); err != nil {
			return err
		}
	}
	return nil
}

// applySingleWrite applies a single node write with CAS.
func (tx *OptimisticTx) applySingleWrite(sqlTx *sql.Tx, node *GraphNode) error {
	expectedVersion, hasRead := tx.readSet[node.ID]
	if hasRead {
		return tx.updateNodeWithCAS(sqlTx, node, expectedVersion)
	}

	return tx.writeUnreadNode(sqlTx, node)
}

// writeUnreadNode handles writes for nodes that weren't read in this transaction.
func (tx *OptimisticTx) writeUnreadNode(sqlTx *sql.Tx, node *GraphNode) error {
	currentVersion, err := tx.store.GetNodeVersion(node.ID)
	if err == ErrNodeNotFound {
		return tx.insertNode(sqlTx, node)
	}
	if err != nil {
		return fmt.Errorf("get version for write: %w", err)
	}

	return tx.updateNodeWithCAS(sqlTx, node, currentVersion)
}

// insertNode inserts a new node within the transaction.
func (tx *OptimisticTx) insertNode(sqlTx *sql.Tx, node *GraphNode) error {
	metadata, _ := json.Marshal(node.Metadata)
	now := time.Now()
	node.CreatedAt = now
	node.UpdatedAt = now
	node.Version = 1

	_, err := sqlTx.Exec(`
		INSERT INTO nodes (id, domain, node_type, name, path, package, line_start, line_end, signature,
			session_id, timestamp, category, url, source, authors, published_at,
			content, content_hash, metadata, verified, verification_type, confidence, trust_level,
			created_at, updated_at, expires_at, superseded_by, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, node.ID, node.Domain, node.NodeType, node.Name, node.Path, node.Package,
		nullInt(node.LineStart), nullInt(node.LineEnd), node.Signature,
		nullString(node.SessionID), nullTime(node.Timestamp), nullString(node.Category),
		nullString(node.URL), nullString(node.Source), nullJSON(node.Authors), nullTime(node.PublishedAt),
		nullString(node.Content), node.ContentHash, string(metadata),
		node.Verified, nullInt(int(node.VerificationType)), nullFloat(node.Confidence), nullInt(int(node.TrustLevel)),
		node.CreatedAt.Format(time.RFC3339), node.UpdatedAt.Format(time.RFC3339),
		nullTime(node.ExpiresAt), nullString(node.SupersededBy), node.Version)
	if err != nil {
		return fmt.Errorf("insert node: %w", err)
	}

	return nil
}

// updateNodeWithCAS updates a node only if its version matches.
func (tx *OptimisticTx) updateNodeWithCAS(sqlTx *sql.Tx, node *GraphNode, expectedVersion uint64) error {
	metadata, _ := json.Marshal(node.Metadata)
	node.UpdatedAt = time.Now()
	newVersion := expectedVersion + 1

	result, err := sqlTx.Exec(`
		UPDATE nodes SET domain = ?, node_type = ?, name = ?, path = ?, package = ?,
			line_start = ?, line_end = ?, signature = ?,
			session_id = ?, timestamp = ?, category = ?, url = ?, source = ?, authors = ?, published_at = ?,
			content = ?, content_hash = ?, metadata = ?, verified = ?, verification_type = ?,
			confidence = ?, trust_level = ?, updated_at = ?, expires_at = ?, superseded_by = ?,
			version = ?
		WHERE id = ? AND version = ?
	`, node.Domain, node.NodeType, node.Name, node.Path, node.Package,
		nullInt(node.LineStart), nullInt(node.LineEnd), node.Signature,
		nullString(node.SessionID), nullTime(node.Timestamp), nullString(node.Category),
		nullString(node.URL), nullString(node.Source), nullJSON(node.Authors), nullTime(node.PublishedAt),
		nullString(node.Content), node.ContentHash, string(metadata),
		node.Verified, nullInt(int(node.VerificationType)), nullFloat(node.Confidence), nullInt(int(node.TrustLevel)),
		node.UpdatedAt.Format(time.RFC3339), nullTime(node.ExpiresAt), nullString(node.SupersededBy),
		newVersion, node.ID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update node: %w", err)
	}

	return tx.checkRowsAffected(result, node.ID)
}

// checkRowsAffected verifies exactly one row was affected by the CAS update.
func (tx *OptimisticTx) checkRowsAffected(result sql.Result, nodeID string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return NewConcurrentConflictError(nodeID, tx.sessionID)
	}

	// Invalidate version cache since we updated the node
	tx.store.InvalidateVersionCache(nodeID)
	return nil
}

// Rollback discards the transaction, clearing all buffered reads and writes.
func (tx *OptimisticTx) Rollback() {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	tx.readSet = make(map[string]uint64)
	tx.writeSet = make(map[string]*GraphNode)
	tx.rolledBack = true
}

// IsCommitted returns true if the transaction has been committed.
func (tx *OptimisticTx) IsCommitted() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.committed
}

// IsRolledBack returns true if the transaction has been rolled back.
func (tx *OptimisticTx) IsRolledBack() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.rolledBack
}

// ReadSetSize returns the number of nodes in the read set.
func (tx *OptimisticTx) ReadSetSize() int {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return len(tx.readSet)
}

// WriteSetSize returns the number of nodes in the write set.
func (tx *OptimisticTx) WriteSetSize() int {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return len(tx.writeSet)
}

// SessionID returns the session ID associated with this transaction.
func (tx *OptimisticTx) SessionID() string {
	return tx.sessionID
}
