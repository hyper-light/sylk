// Package sylkdir provides integration between SylkDir and Bleve full-text search index.
package sylkdir

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/bleve"
)

// BleveStore wraps Bleve IndexManager to use SylkDir paths or an explicit path.
// When constructed with NewBleveStore, paths derive from the .sylk layout:
//
//	bleve/
//	└── index/  (Bleve index files)
//
// When constructed with NewBleveStoreAtPath, paths derive from the explicit path
// (used for per-session Bleve indices).
type BleveStore struct {
	sylkDir      *SylkDir // nil when using explicitPath
	explicitPath string   // non-empty when decoupled from SylkDir
	manager      *bleve.IndexManager
}

// NewBleveStore creates a Bleve store using SylkDir paths.
func NewBleveStore(sd *SylkDir) *BleveStore {
	return &BleveStore{
		sylkDir: sd,
	}
}

// NewBleveStoreAtPath creates a BleveStore at an explicit filesystem path.
// The index is stored at <blevePath>/documents.bleve.
// Use this for per-session Bleve indices that are decoupled from SylkDir.
func NewBleveStoreAtPath(blevePath string) *BleveStore {
	return &BleveStore{
		explicitPath: blevePath,
	}
}

// Open opens or creates the Bleve index.
// Creates the index directory if it doesn't exist.
func (s *BleveStore) Open() error {
	// Ensure parent directory exists
	indexPath := s.IndexPath()
	parentDir := filepath.Dir(indexPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create bleve directory: %w", err)
	}

	config := bleve.IndexConfig{
		Path:          indexPath,
		MaxConcurrent: runtime.NumCPU(),
		UnsafeBatch:   true, // Safe: WAL + data store recovery can rebuild Bleve.
	}

	s.manager = bleve.NewIndexManagerWithConfig(config)
	return s.manager.Open()
}

// OpenWithConfig opens or creates the Bleve index with custom configuration.
// The Path field in config is overridden to use this store's path.
func (s *BleveStore) OpenWithConfig(config bleve.IndexConfig) error {
	// Ensure parent directory exists
	indexPath := s.IndexPath()
	parentDir := filepath.Dir(indexPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create bleve directory: %w", err)
	}

	// Override path
	config.Path = indexPath

	s.manager = bleve.NewIndexManagerWithConfig(config)
	return s.manager.Open()
}

// Close closes the Bleve index.
func (s *BleveStore) Close() error {
	if s.manager == nil {
		return nil
	}
	return s.manager.Close()
}

// Manager returns the underlying IndexManager for direct operations.
// Returns nil if the store is not open.
func (s *BleveStore) Manager() *bleve.IndexManager {
	return s.manager
}

// IndexPath returns the path where the Bleve index database is stored.
func (s *BleveStore) IndexPath() string {
	if s.explicitPath != "" {
		return filepath.Join(s.explicitPath, "documents.bleve")
	}
	return filepath.Join(s.sylkDir.BleveIndexPath(), "documents.bleve")
}

// Path returns the base path for Bleve storage.
func (s *BleveStore) Path() string {
	if s.explicitPath != "" {
		return s.explicitPath
	}
	return s.sylkDir.BlevePath()
}

// Exists returns true if a Bleve index exists at the SylkDir path.
func (s *BleveStore) Exists() bool {
	info, err := os.Stat(s.IndexPath())
	return err == nil && info.IsDir()
}

// Delete removes all Bleve index data from the SylkDir.
// Use with caution - this is irreversible.
func (s *BleveStore) Delete() error {
	// Close first if open
	if s.manager != nil {
		if err := s.manager.Close(); err != nil {
			return fmt.Errorf("sylkdir: failed to close bleve before delete: %w", err)
		}
		s.manager = nil
	}

	// Remove index directory
	indexPath := s.IndexPath()
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		return nil
	}

	return os.RemoveAll(indexPath)
}

// Index indexes a single document.
func (s *BleveStore) Index(ctx context.Context, doc *search.Document) error {
	if s.manager == nil {
		return fmt.Errorf("sylkdir: bleve store not open")
	}
	return s.manager.Index(ctx, doc)
}

// IndexBatch indexes multiple documents in a batch.
func (s *BleveStore) IndexBatch(ctx context.Context, docs []*search.Document) error {
	if s.manager == nil {
		return fmt.Errorf("sylkdir: bleve store not open")
	}
	return s.manager.IndexBatch(ctx, docs)
}

// Search executes a search query.
func (s *BleveStore) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	if s.manager == nil {
		return nil, fmt.Errorf("sylkdir: bleve store not open")
	}
	return s.manager.Search(ctx, req)
}

// DocumentCount returns the number of indexed documents.
func (s *BleveStore) DocumentCount() (uint64, error) {
	if s.manager == nil {
		return 0, fmt.Errorf("sylkdir: bleve store not open")
	}
	return s.manager.DocumentCount()
}

// BleveStoreStats contains statistics about the Bleve store.
type BleveStoreStats struct {
	Exists        bool
	IsOpen        bool
	DocumentCount uint64
	SizeBytes     int64
	IndexHealthy  bool
}

// Stats returns statistics about the stored Bleve index.
func (s *BleveStore) Stats() BleveStoreStats {
	stats := BleveStoreStats{
		Exists: s.Exists(),
	}

	if s.manager != nil && s.manager.IsOpen() {
		stats.IsOpen = true
		stats.DocumentCount, _ = s.manager.DocumentCount()

		// Check health
		health := s.manager.CheckHealth(context.Background())
		stats.IndexHealthy = health.Health == bleve.IndexHealthy
	}

	// Calculate size
	stats.SizeBytes = dirSize(s.IndexPath())

	return stats
}
