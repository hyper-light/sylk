// Package sylkdir provides integration between SylkDir and IVF vector index storage.
package sylkdir

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/ivf"
)

// IVFStore wraps IVF persistence to use SylkDir paths.
// This ensures vector index storage follows the .sylk layout:
//
//	knowledge/vectors/
//	├── metadata.yaml
//	├── centroids.bin
//	├── partitions.bin
//	├── bbq_means.bin
//	├── vectors/  (sharded vector data)
//	├── graph/    (Vamana adjacency lists)
//	├── norms/    (vector norms)
//	└── bbq/      (BBQ codes)
type IVFStore struct {
	sylkDir    *SylkDir
	vectorPath string
}

// NewIVFStore creates an IVF store using SylkDir paths.
func NewIVFStore(sd *SylkDir) *IVFStore {
	return &IVFStore{
		sylkDir:    sd,
		vectorPath: sd.VectorsPath(),
	}
}

// Init ensures the vector storage directory structure exists.
func (s *IVFStore) Init() error {
	// Create subdirectories for IVF components
	dirs := []string{
		s.vectorPath,
		filepath.Join(s.vectorPath, "vectors"),
		filepath.Join(s.vectorPath, "graph"),
		filepath.Join(s.vectorPath, "norms"),
		filepath.Join(s.vectorPath, "bbq"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("sylkdir: failed to create IVF directory %s: %w", dir, err)
		}
	}

	return nil
}

// Save persists an IVF index to the SylkDir vector storage path.
func (s *IVFStore) Save(idx *ivf.Index) error {
	if err := s.Init(); err != nil {
		return err
	}
	return idx.Save(s.vectorPath)
}

// Load loads an IVF index from the SylkDir vector storage path.
// Returns a PersistentIndex for disk-based access.
func (s *IVFStore) Load() (*ivf.PersistentIndex, error) {
	return ivf.LoadIndex(s.vectorPath)
}

// LoadInMemory loads an IVF index entirely into memory.
// This provides faster search but uses more RAM.
func (s *IVFStore) LoadInMemory() (*ivf.Index, error) {
	return ivf.LoadIndexInMemory(s.vectorPath)
}

// Exists returns true if an IVF index exists at the SylkDir path.
func (s *IVFStore) Exists() bool {
	metaPath := filepath.Join(s.vectorPath, "metadata.yaml")
	_, err := os.Stat(metaPath)
	return err == nil
}

// Path returns the base path where IVF data is stored.
func (s *IVFStore) Path() string {
	return s.vectorPath
}

// VectorsSubpath returns the path to the vectors subdirectory.
func (s *IVFStore) VectorsSubpath() string {
	return filepath.Join(s.vectorPath, "vectors")
}

// GraphSubpath returns the path to the graph subdirectory.
func (s *IVFStore) GraphSubpath() string {
	return filepath.Join(s.vectorPath, "graph")
}

// NormsSubpath returns the path to the norms subdirectory.
func (s *IVFStore) NormsSubpath() string {
	return filepath.Join(s.vectorPath, "norms")
}

// BBQSubpath returns the path to the BBQ subdirectory.
func (s *IVFStore) BBQSubpath() string {
	return filepath.Join(s.vectorPath, "bbq")
}

// CentroidsPath returns the path to the centroids file.
func (s *IVFStore) CentroidsPath() string {
	return filepath.Join(s.vectorPath, "centroids.bin")
}

// PartitionsPath returns the path to the partitions file.
func (s *IVFStore) PartitionsPath() string {
	return filepath.Join(s.vectorPath, "partitions.bin")
}

// MetadataPath returns the path to the metadata file.
func (s *IVFStore) MetadataPath() string {
	return filepath.Join(s.vectorPath, "metadata.yaml")
}

// Delete removes all IVF data from the SylkDir.
// Use with caution - this is irreversible.
func (s *IVFStore) Delete() error {
	// Only delete contents, not the vectors directory itself
	entries, err := os.ReadDir(s.vectorPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sylkdir: failed to read vectors directory: %w", err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(s.vectorPath, entry.Name())
		if err := os.RemoveAll(entryPath); err != nil {
			return fmt.Errorf("sylkdir: failed to delete %s: %w", entryPath, err)
		}
	}

	return nil
}

// Stats returns statistics about the stored IVF index.
type IVFStoreStats struct {
	Exists      bool
	NumVectors  int
	Dim         int
	SizeBytes   int64
	MetadataOK  bool
	VectorsOK   bool
	GraphOK     bool
	NormsOK     bool
	BBQOK       bool
	CentroidsOK bool
}

func (s *IVFStore) Stats() IVFStoreStats {
	stats := IVFStoreStats{}

	// Check metadata
	if _, err := os.Stat(s.MetadataPath()); err == nil {
		stats.MetadataOK = true
		stats.Exists = true
	}

	// Check subdirectories
	if _, err := os.Stat(s.VectorsSubpath()); err == nil {
		stats.VectorsOK = true
	}
	if _, err := os.Stat(s.GraphSubpath()); err == nil {
		stats.GraphOK = true
	}
	if _, err := os.Stat(s.NormsSubpath()); err == nil {
		stats.NormsOK = true
	}
	if _, err := os.Stat(s.BBQSubpath()); err == nil {
		stats.BBQOK = true
	}
	if _, err := os.Stat(s.CentroidsPath()); err == nil {
		stats.CentroidsOK = true
	}

	// Calculate total size
	stats.SizeBytes = dirSize(s.vectorPath)

	// Try to read metadata for vector count and dim
	if stats.Exists {
		if pi, err := s.Load(); err == nil {
			stats.NumVectors = pi.NumVectors()
			stats.Dim = pi.Dim()
			pi.Close()
		}
	}

	return stats
}

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}
