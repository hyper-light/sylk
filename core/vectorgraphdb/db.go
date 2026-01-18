package vectorgraphdb

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaSQL string

type VectorGraphDB struct {
	db   *sql.DB
	path string
	mu   sync.RWMutex
}

type DBConfig struct {
	Path            string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

func DefaultDBConfig(path string) DBConfig {
	return DBConfig{
		Path:            path,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
	}
}

func Open(path string) (*VectorGraphDB, error) {
	return OpenWithConfig(DefaultDBConfig(path))
}

func OpenWithConfig(config DBConfig) (*VectorGraphDB, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_synchronous=normal", config.Path)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	vgdb := &VectorGraphDB{
		db:   db,
		path: config.Path,
	}

	if err := vgdb.Migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return vgdb, nil
}

func (v *VectorGraphDB) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.db == nil {
		return nil
	}

	return v.db.Close()
}

func (v *VectorGraphDB) Migrate() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	_, err := v.db.Exec(schemaSQL)
	if err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}

	return nil
}

func (v *VectorGraphDB) Vacuum() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	_, err := v.db.Exec("VACUUM")
	if err != nil {
		return fmt.Errorf("failed to vacuum database: %w", err)
	}

	return nil
}

func (v *VectorGraphDB) Stats() (*DBStats, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	stats := &DBStats{
		NodesByDomain: make(map[Domain]int64),
		NodesByType:   make(map[NodeType]int64),
		EdgesByType:   make(map[EdgeType]int64),
	}

	if err := v.collectNodeStats(stats); err != nil {
		return nil, err
	}

	if err := v.collectEdgeStats(stats); err != nil {
		return nil, err
	}

	if err := v.collectMiscStats(stats); err != nil {
		return nil, err
	}

	return stats, nil
}

func (v *VectorGraphDB) collectNodeStats(stats *DBStats) error {
	if err := v.db.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&stats.TotalNodes); err != nil {
		return fmt.Errorf("failed to count nodes: %w", err)
	}

	if err := v.scanGroupedCounts("SELECT domain, COUNT(*) FROM nodes GROUP BY domain", func(key int64, count int64) {
		stats.NodesByDomain[Domain(key)] = count
	}); err != nil {
		return fmt.Errorf("failed to count nodes by domain: %w", err)
	}

	if err := v.scanGroupedCounts("SELECT node_type, COUNT(*) FROM nodes GROUP BY node_type", func(key int64, count int64) {
		stats.NodesByType[NodeType(key)] = count
	}); err != nil {
		return fmt.Errorf("failed to count nodes by type: %w", err)
	}

	return nil
}

func (v *VectorGraphDB) collectEdgeStats(stats *DBStats) error {
	if err := v.db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&stats.TotalEdges); err != nil {
		return fmt.Errorf("failed to count edges: %w", err)
	}

	if err := v.scanGroupedCounts("SELECT edge_type, COUNT(*) FROM edges GROUP BY edge_type", func(key int64, count int64) {
		stats.EdgesByType[EdgeType(key)] = count
	}); err != nil {
		return fmt.Errorf("failed to count edges by type: %w", err)
	}

	return nil
}

func (v *VectorGraphDB) collectMiscStats(stats *DBStats) error {
	if err := v.db.QueryRow("SELECT COUNT(*) FROM vectors").Scan(&stats.TotalVectors); err != nil {
		return fmt.Errorf("failed to count vectors: %w", err)
	}

	if err := v.db.QueryRow("SELECT COUNT(*) FROM conflicts WHERE resolved_at IS NULL").Scan(&stats.UnresolvedConflicts); err != nil {
		return fmt.Errorf("failed to count unresolved conflicts: %w", err)
	}

	if err := v.collectStaleNodeCount(stats); err != nil {
		return err
	}

	v.collectDBSize(stats)
	return nil
}

func (v *VectorGraphDB) collectStaleNodeCount(stats *DBStats) error {
	staleThreshold := time.Now().Add(-DefaultStaleThreshold).Format(time.RFC3339)
	if err := v.db.QueryRow("SELECT COUNT(*) FROM nodes WHERE updated_at < ?", staleThreshold).Scan(&stats.StaleNodes); err != nil {
		return fmt.Errorf("failed to count stale nodes: %w", err)
	}
	return nil
}

func (v *VectorGraphDB) collectDBSize(stats *DBStats) {
	fileInfo, err := os.Stat(v.path)
	if err == nil {
		stats.DBSizeBytes = fileInfo.Size()
	}
}

func (v *VectorGraphDB) scanGroupedCounts(query string, handler func(key int64, count int64)) error {
	rows, err := v.db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var key int64
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		handler(key, count)
	}

	return rows.Err()
}

func (v *VectorGraphDB) DB() *sql.DB {
	return v.db
}

func (v *VectorGraphDB) Path() string {
	return v.path
}

func (v *VectorGraphDB) BeginTx() (*sql.Tx, error) {
	return v.db.Begin()
}

func (v *VectorGraphDB) GetSchemaVersion() (int, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	var version int
	err := v.db.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("failed to get schema version: %w", err)
	}
	return version, nil
}

// WithProtection wraps VectorGraphDB with full protection mechanisms.
// It creates a ProtectedVectorDB with the provided vector index, snapshot manager,
// and protection configuration. The caller is responsible for providing properly
// initialized dependencies to avoid circular imports with the hnsw package.
func (v *VectorGraphDB) WithProtection(
	vectorIndex VectorIndex,
	snapshotMgr SnapshotManager,
	cfg ProtectionConfig,
) (*ProtectedVectorDB, error) {
	return NewProtectedVectorDB(ProtectedDBDeps{
		DB:          v,
		VectorIndex: vectorIndex,
		SnapshotMgr: snapshotMgr,
	}, cfg)
}

// WithDefaultProtection creates a protected DB with default configuration.
// The caller must provide the vector index and snapshot manager implementations.
func (v *VectorGraphDB) WithDefaultProtection(
	vectorIndex VectorIndex,
	snapshotMgr SnapshotManager,
) (*ProtectedVectorDB, error) {
	return v.WithProtection(vectorIndex, snapshotMgr, DefaultProtectionConfig())
}
