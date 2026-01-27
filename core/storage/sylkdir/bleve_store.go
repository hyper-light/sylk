// Package sylkdir provides integration between SylkDir and Bleve full-text search index.
package sylkdir

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/bleve"
)

// BleveStore wraps Bleve IndexManager to use SylkDir paths.
// This ensures full-text search index storage follows the .sylk layout:
//
//	bleve/
//	└── index/  (Bleve index files)
type BleveStore struct {
	sylkDir *SylkDir
	manager *bleve.IndexManager
}

// NewBleveStore creates a Bleve store using SylkDir paths.
func NewBleveStore(sd *SylkDir) *BleveStore {
	return &BleveStore{
		sylkDir: sd,
	}
}

// Open opens or creates the Bleve index at the SylkDir path.
// Creates the index directory if it doesn't exist.
func (s *BleveStore) Open() error {
	// Ensure directory exists
	if err := os.MkdirAll(s.sylkDir.BleveIndexPath(), 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create bleve directory: %w", err)
	}

	config := bleve.IndexConfig{
		Path:          s.IndexPath(),
		BatchSize:     100,
		MaxConcurrent: 4,
		BatchTimeout:  30 * time.Second,
	}

	s.manager = bleve.NewIndexManagerWithConfig(config)
	return s.manager.Open()
}

// OpenWithConfig opens or creates the Bleve index with custom configuration.
// The Path field in config is overridden to use SylkDir paths.
func (s *BleveStore) OpenWithConfig(config bleve.IndexConfig) error {
	// Ensure directory exists
	if err := os.MkdirAll(s.sylkDir.BleveIndexPath(), 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create bleve directory: %w", err)
	}

	// Override path to use SylkDir
	config.Path = s.IndexPath()

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

// IndexPath returns the path where the Bleve index is stored.
// This is within the .sylk directory structure.
func (s *BleveStore) IndexPath() string {
	return s.sylkDir.BleveIndexPath() + "/documents.bleve"
}

// Path returns the base path for Bleve storage.
func (s *BleveStore) Path() string {
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
