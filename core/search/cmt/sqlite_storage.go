// Package cmt provides a Cartesian Merkle Tree implementation for the Sylk
// Document Search System. This file implements SQLite-based persistent storage
// for the CMT.
package cmt

import (
	"database/sql"
	"errors"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrStoragePathEmpty indicates the storage path was not provided.
	ErrStoragePathEmpty = errors.New("storage path cannot be empty")

	// ErrStorageNotOpen indicates the storage is not open.
	ErrStorageNotOpen = errors.New("storage is not open")

	// ErrStorageAlreadyOpen indicates the storage is already open.
	ErrStorageAlreadyOpen = errors.New("storage is already open")

	// ErrStorageNilNode indicates the node was nil.
	ErrStorageNilNode = errors.New("node cannot be nil")

	// ErrStorageEmptyKey indicates the key was empty.
	ErrStorageEmptyKey = errors.New("key cannot be empty")
)

// =============================================================================
// SQL Statements
// =============================================================================

const createTablesSQL = `
CREATE TABLE IF NOT EXISTS cmt_nodes (
    key TEXT PRIMARY KEY,
    priority INTEGER NOT NULL,
    hash BLOB NOT NULL,
    content_hash BLOB,
    file_size INTEGER,
    mod_time INTEGER,
    permissions INTEGER,
    indexed INTEGER,
    indexed_at INTEGER
);

CREATE TABLE IF NOT EXISTS cmt_metadata (
    key TEXT PRIMARY KEY,
    value TEXT
);
`

const insertNodeSQL = `
INSERT OR REPLACE INTO cmt_nodes (key, priority, hash, content_hash, file_size, mod_time, permissions, indexed, indexed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`

const deleteNodeSQL = `DELETE FROM cmt_nodes WHERE key = ?`

const deleteAllNodesSQL = `DELETE FROM cmt_nodes`

const selectAllNodesSQL = `SELECT key, priority, hash, content_hash, file_size, mod_time, permissions, indexed, indexed_at FROM cmt_nodes`

const countNodesSQL = `SELECT COUNT(*) FROM cmt_nodes`

// =============================================================================
// SQLiteStorage
// =============================================================================

// SQLiteStorage persists CMT to SQLite.
type SQLiteStorage struct {
	db   *sql.DB
	path string
	mu   sync.RWMutex
}

// NewSQLiteStorage creates a new SQLite storage.
func NewSQLiteStorage(path string) (*SQLiteStorage, error) {
	if path == "" {
		return nil, ErrStoragePathEmpty
	}

	return &SQLiteStorage{
		path: path,
	}, nil
}

// Open opens or creates the database.
func (s *SQLiteStorage) Open() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		return ErrStorageAlreadyOpen
	}

	db, err := sql.Open("sqlite3", s.path+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return err
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return err
	}

	if _, err := db.Exec(createTablesSQL); err != nil {
		db.Close()
		return err
	}

	s.db = db
	return nil
}

// Close closes the database connection.
func (s *SQLiteStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}

	err := s.db.Close()
	s.db = nil
	return err
}

// =============================================================================
// Save Operations
// =============================================================================

// Save persists the entire tree to SQLite using a transaction for atomicity.
func (s *SQLiteStorage) Save(root *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return ErrStorageNotOpen
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}

	if err := s.deleteAllNodesTx(tx); err != nil {
		tx.Rollback()
		return err
	}

	if err := s.saveTreeTx(tx, root); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// deleteAllNodesTx deletes all nodes within a transaction.
func (s *SQLiteStorage) deleteAllNodesTx(tx *sql.Tx) error {
	_, err := tx.Exec(deleteAllNodesSQL)
	return err
}

// saveTreeTx walks the tree and saves each node within a transaction.
func (s *SQLiteStorage) saveTreeTx(tx *sql.Tx, node *Node) error {
	if node == nil {
		return nil
	}

	if err := s.saveNodeTx(tx, node); err != nil {
		return err
	}

	if err := s.saveTreeTx(tx, node.Left); err != nil {
		return err
	}

	return s.saveTreeTx(tx, node.Right)
}

// saveNodeTx saves a single node within a transaction.
func (s *SQLiteStorage) saveNodeTx(tx *sql.Tx, node *Node) error {
	var contentHash []byte
	var fileSize, modTime, permissions sql.NullInt64
	var indexed, indexedAt sql.NullInt64

	if node.Info != nil {
		contentHash = node.Info.ContentHash[:]
		fileSize = sql.NullInt64{Int64: node.Info.Size, Valid: true}
		modTime = sql.NullInt64{Int64: node.Info.ModTime.UnixNano(), Valid: true}
		permissions = sql.NullInt64{Int64: int64(node.Info.Permissions), Valid: true}
		indexed = sql.NullInt64{Int64: boolToInt64(node.Info.Indexed), Valid: true}
		indexedAt = sql.NullInt64{Int64: node.Info.IndexedAt.UnixNano(), Valid: true}
	}

	_, err := tx.Exec(insertNodeSQL,
		node.Key,
		int64(node.Priority),
		node.Hash[:],
		contentHash,
		fileSize,
		modTime,
		permissions,
		indexed,
		indexedAt,
	)
	return err
}

// SaveNode saves a single node (for incremental updates).
func (s *SQLiteStorage) SaveNode(node *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return ErrStorageNotOpen
	}

	if node == nil {
		return ErrStorageNilNode
	}

	return s.saveNodeDirect(node)
}

// saveNodeDirect saves a node directly without locking.
func (s *SQLiteStorage) saveNodeDirect(node *Node) error {
	var contentHash []byte
	var fileSize, modTime, permissions sql.NullInt64
	var indexed, indexedAt sql.NullInt64

	if node.Info != nil {
		contentHash = node.Info.ContentHash[:]
		fileSize = sql.NullInt64{Int64: node.Info.Size, Valid: true}
		modTime = sql.NullInt64{Int64: node.Info.ModTime.UnixNano(), Valid: true}
		permissions = sql.NullInt64{Int64: int64(node.Info.Permissions), Valid: true}
		indexed = sql.NullInt64{Int64: boolToInt64(node.Info.Indexed), Valid: true}
		indexedAt = sql.NullInt64{Int64: node.Info.IndexedAt.UnixNano(), Valid: true}
	}

	_, err := s.db.Exec(insertNodeSQL,
		node.Key,
		int64(node.Priority),
		node.Hash[:],
		contentHash,
		fileSize,
		modTime,
		permissions,
		indexed,
		indexedAt,
	)
	return err
}

// =============================================================================
// Load Operations
// =============================================================================

// Load reconstructs the tree from SQLite.
func (s *SQLiteStorage) Load() (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, ErrStorageNotOpen
	}

	rows, err := s.db.Query(selectAllNodesSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var root *Node
	for rows.Next() {
		node, err := s.scanNode(rows)
		if err != nil {
			return nil, err
		}
		root = Insert(root, node)
	}

	return root, rows.Err()
}

// scanNode scans a single row into a Node.
func (s *SQLiteStorage) scanNode(rows *sql.Rows) (*Node, error) {
	var key string
	var priority int64
	var hashBytes []byte
	var contentHash []byte
	var fileSize, modTime, permissions sql.NullInt64
	var indexed, indexedAt sql.NullInt64

	err := rows.Scan(
		&key,
		&priority,
		&hashBytes,
		&contentHash,
		&fileSize,
		&modTime,
		&permissions,
		&indexed,
		&indexedAt,
	)
	if err != nil {
		return nil, err
	}

	node := &Node{
		Key:      key,
		Priority: uint64(priority),
	}

	if len(hashBytes) == HashSize {
		copy(node.Hash[:], hashBytes)
	}

	if contentHash != nil {
		node.Info = s.buildFileInfo(key, contentHash, fileSize, modTime, permissions, indexed, indexedAt)
	}

	return node, nil
}

// buildFileInfo constructs a FileInfo from scanned values.
func (s *SQLiteStorage) buildFileInfo(key string, contentHash []byte, fileSize, modTime, permissions, indexed, indexedAt sql.NullInt64) *FileInfo {
	info := &FileInfo{Path: key}

	if len(contentHash) == HashSize {
		copy(info.ContentHash[:], contentHash)
	}

	if fileSize.Valid {
		info.Size = fileSize.Int64
	}

	if modTime.Valid {
		info.ModTime = time.Unix(0, modTime.Int64)
	}

	if permissions.Valid {
		info.Permissions = uint32(permissions.Int64)
	}

	if indexed.Valid {
		info.Indexed = indexed.Int64 != 0
	}

	if indexedAt.Valid {
		info.IndexedAt = time.Unix(0, indexedAt.Int64)
	}

	return info
}

// =============================================================================
// Delete Operations
// =============================================================================

// DeleteNode removes a node by key.
func (s *SQLiteStorage) DeleteNode(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return ErrStorageNotOpen
	}

	if key == "" {
		return ErrStorageEmptyKey
	}

	_, err := s.db.Exec(deleteNodeSQL, key)
	return err
}

// =============================================================================
// Count Operations
// =============================================================================

// GetNodeCount returns the number of stored nodes.
func (s *SQLiteStorage) GetNodeCount() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, ErrStorageNotOpen
	}

	var count int64
	err := s.db.QueryRow(countNodesSQL).Scan(&count)
	return count, err
}

// =============================================================================
// Helpers
// =============================================================================

// boolToInt64 converts a bool to int64 for SQLite storage.
func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
