package archivalist

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/adalundhe/sylk/core/knowledge/memory"
)

// MemoryIntegrationConfig configures ACT-R memory scoring wiring.
type MemoryIntegrationConfig struct {
	// ArchivePath is the base path for persistent storage.
	// The memory DB will be created at {ArchivePath}_memory.db.
	ArchivePath string
}

// memoryIntegration holds ACT-R memory subsystem components.
type memoryIntegration struct {
	store  *memory.MemoryStore
	scorer *memory.MemoryWeightedScorer
	hybrid *memory.HybridQueryWithMemory
	db     *sql.DB
}

// buildMemoryIntegration creates the ACT-R memory scoring pipeline.
// Returns nil components (no error) if ArchivePath is empty.
func buildMemoryIntegration(cfg MemoryIntegrationConfig) (*memoryIntegration, error) {
	if cfg.ArchivePath == "" {
		return &memoryIntegration{}, nil
	}

	dbPath := deriveMemoryDBPath(cfg.ArchivePath)
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open memory db: %w", err)
	}

	store := memory.NewMemoryStore(db, 0, 0)
	scorer := memory.NewMemoryWeightedScorer(store)
	hybrid := memory.NewHybridQueryWithMemory(scorer)

	return &memoryIntegration{
		store:  store,
		scorer: scorer,
		hybrid: hybrid,
		db:     db,
	}, nil
}

func deriveMemoryDBPath(archivePath string) string {
	ext := filepath.Ext(archivePath)
	base := archivePath[:len(archivePath)-len(ext)]
	return base + "_memory" + ext
}

// Close releases memory integration resources.
func (m *memoryIntegration) Close() error {
	if m.scorer != nil {
		m.scorer.Stop()
	}
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}
