package ingestion

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/bleve"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// Persister
// =============================================================================

// Persister handles parallel persistence to SQLite and Bleve.
type Persister struct {
	sqlite *vectorgraphdb.VectorGraphDB
	bleve  *bleve.IndexManager
}

// NewPersister creates a persister with the given database connections.
func NewPersister(sqlite *vectorgraphdb.VectorGraphDB, bleveManager *bleve.IndexManager) *Persister {
	return &Persister{
		sqlite: sqlite,
		bleve:  bleveManager,
	}
}

// Persist saves the CodeGraph to both SQLite and Bleve in parallel.
// Returns errors from either persistence path.
func (p *Persister) Persist(ctx context.Context, graph *CodeGraph) error {
	var wg sync.WaitGroup
	errChan := make(chan error, 2)

	// Run SQLite and Bleve persistence in parallel
	wg.Add(2)
	go p.persistSQLite(ctx, graph, &wg, errChan)
	go p.persistBleve(ctx, graph, &wg, errChan)

	wg.Wait()
	close(errChan)

	return collectErrors(errChan)
}

// PersistSQLiteOnly persists only to SQLite (for testing or when Bleve is disabled).
func (p *Persister) PersistSQLiteOnly(ctx context.Context, graph *CodeGraph) error {
	return p.persistSQLiteSync(ctx, graph)
}

// PersistBleveOnly persists only to Bleve (for testing).
func (p *Persister) PersistBleveOnly(ctx context.Context, graph *CodeGraph) error {
	return p.persistBleveSync(ctx, graph)
}

// =============================================================================
// SQLite Persistence
// =============================================================================

// persistSQLite runs SQLite persistence in a goroutine.
func (p *Persister) persistSQLite(ctx context.Context, graph *CodeGraph, wg *sync.WaitGroup, errChan chan<- error) {
	defer wg.Done()

	if p.sqlite == nil {
		return
	}

	if err := p.persistSQLiteSync(ctx, graph); err != nil {
		select {
		case errChan <- fmt.Errorf("sqlite: %w", err):
		default:
		}
	}
}

// persistSQLiteSync performs synchronous SQLite persistence.
func (p *Persister) persistSQLiteSync(ctx context.Context, graph *CodeGraph) error {
	tx, err := p.sqlite.BeginTx()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	// Rollback on error
	var committed bool
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	// Apply optimizations
	if err := applyBulkOptimizations(tx); err != nil {
		return fmt.Errorf("apply optimizations: %w", err)
	}

	// Drop indexes before bulk insert
	if err := dropIndexes(tx); err != nil {
		return fmt.Errorf("drop indexes: %w", err)
	}

	// Insert files
	if err := insertFiles(ctx, tx, graph.Files); err != nil {
		return fmt.Errorf("insert files: %w", err)
	}

	// Insert symbols
	if err := insertSymbols(ctx, tx, graph.Symbols); err != nil {
		return fmt.Errorf("insert symbols: %w", err)
	}

	// Insert edges (imports + contains)
	if err := insertEdges(ctx, tx, graph); err != nil {
		return fmt.Errorf("insert edges: %w", err)
	}

	// Rebuild indexes
	if err := rebuildIndexes(tx); err != nil {
		return fmt.Errorf("rebuild indexes: %w", err)
	}

	// Commit
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true

	return nil
}

// applyBulkOptimizations applies SQLite optimizations for bulk insert.
func applyBulkOptimizations(tx *sql.Tx) error {
	optimizations := []string{
		fmt.Sprintf("PRAGMA cache_size = -%d", SQLiteCacheSizeKB),
		"PRAGMA temp_store = MEMORY",
		"PRAGMA synchronous = NORMAL",
	}

	for _, opt := range optimizations {
		if _, err := tx.Exec(opt); err != nil {
			return fmt.Errorf("optimization %q: %w", opt, err)
		}
	}

	return nil
}

// dropIndexes drops indexes before bulk insert for performance.
func dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"DROP INDEX IF EXISTS idx_ingested_files_path",
		"DROP INDEX IF EXISTS idx_ingested_symbols_name",
		"DROP INDEX IF EXISTS idx_ingested_symbols_file",
		"DROP INDEX IF EXISTS idx_ingested_edges_source",
		"DROP INDEX IF EXISTS idx_ingested_edges_target",
	}

	for _, stmt := range indexes {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	return nil
}

// rebuildIndexes creates indexes after bulk insert.
func rebuildIndexes(tx *sql.Tx) error {
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_ingested_files_path ON ingested_files(path)",
		"CREATE INDEX IF NOT EXISTS idx_ingested_symbols_name ON ingested_symbols(name)",
		"CREATE INDEX IF NOT EXISTS idx_ingested_symbols_file ON ingested_symbols(file_id)",
		"CREATE INDEX IF NOT EXISTS idx_ingested_edges_source ON ingested_edges(source_id)",
		"CREATE INDEX IF NOT EXISTS idx_ingested_edges_target ON ingested_edges(target_id)",
	}

	for _, stmt := range indexes {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	return nil
}

// insertFiles bulk inserts file nodes.
func insertFiles(ctx context.Context, tx *sql.Tx, files []FileNode) error {
	// Create table if not exists
	createTable := `
		CREATE TABLE IF NOT EXISTS ingested_files (
			id INTEGER PRIMARY KEY,
			path TEXT NOT NULL,
			lang TEXT NOT NULL,
			line_count INTEGER NOT NULL,
			byte_count INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`
	if _, err := tx.ExecContext(ctx, createTable); err != nil {
		return err
	}

	// Clear existing data
	if _, err := tx.ExecContext(ctx, "DELETE FROM ingested_files"); err != nil {
		return err
	}

	// Prepare statement
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO ingested_files (id, path, lang, line_count, byte_count)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	// Insert in batches
	for i := 0; i < len(files); i += SQLiteBatchSize {
		end := i + SQLiteBatchSize
		if end > len(files) {
			end = len(files)
		}

		for _, f := range files[i:end] {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if _, err := stmt.ExecContext(ctx, f.ID, f.Path, f.Lang, f.LineCount, f.ByteCount); err != nil {
				return err
			}
		}
	}

	return nil
}

// insertSymbols bulk inserts symbol nodes.
func insertSymbols(ctx context.Context, tx *sql.Tx, symbols []SymbolNode) error {
	// Create table if not exists
	createTable := `
		CREATE TABLE IF NOT EXISTS ingested_symbols (
			id INTEGER PRIMARY KEY,
			file_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			kind INTEGER NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			signature TEXT,
			FOREIGN KEY (file_id) REFERENCES ingested_files(id)
		)
	`
	if _, err := tx.ExecContext(ctx, createTable); err != nil {
		return err
	}

	// Clear existing data
	if _, err := tx.ExecContext(ctx, "DELETE FROM ingested_symbols"); err != nil {
		return err
	}

	// Prepare statement
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO ingested_symbols (id, file_id, name, kind, start_line, end_line, signature)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	// Insert in batches
	for i := 0; i < len(symbols); i += SQLiteBatchSize {
		end := i + SQLiteBatchSize
		if end > len(symbols) {
			end = len(symbols)
		}

		for _, s := range symbols[i:end] {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if _, err := stmt.ExecContext(ctx, s.ID, s.FileID, s.Name, s.Kind, s.StartLine, s.EndLine, s.Signature); err != nil {
				return err
			}
		}
	}

	return nil
}

// insertEdges bulk inserts edges.
func insertEdges(ctx context.Context, tx *sql.Tx, graph *CodeGraph) error {
	// Create table if not exists
	createTable := `
		CREATE TABLE IF NOT EXISTS ingested_edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			target_id INTEGER NOT NULL,
			kind INTEGER NOT NULL
		)
	`
	if _, err := tx.ExecContext(ctx, createTable); err != nil {
		return err
	}

	// Clear existing data
	if _, err := tx.ExecContext(ctx, "DELETE FROM ingested_edges"); err != nil {
		return err
	}

	// Prepare statement
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO ingested_edges (source_id, target_id, kind)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	// Insert import edges
	for _, e := range graph.ImportEdges {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if _, err := stmt.ExecContext(ctx, e.SourceID, e.TargetID, e.Kind); err != nil {
			return err
		}
	}

	// Insert contains edges
	for _, e := range graph.ContainsEdges {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if _, err := stmt.ExecContext(ctx, e.SourceID, e.TargetID, e.Kind); err != nil {
			return err
		}
	}

	return nil
}

// =============================================================================
// Bleve Persistence
// =============================================================================

// persistBleve runs Bleve persistence in a goroutine.
func (p *Persister) persistBleve(ctx context.Context, graph *CodeGraph, wg *sync.WaitGroup, errChan chan<- error) {
	defer wg.Done()

	if p.bleve == nil {
		return
	}

	if err := p.persistBleveSync(ctx, graph); err != nil {
		select {
		case errChan <- fmt.Errorf("bleve: %w", err):
		default:
		}
	}
}

// persistBleveSync performs synchronous Bleve persistence.
func (p *Persister) persistBleveSync(ctx context.Context, graph *CodeGraph) error {
	// Convert files to search documents
	docs := make([]*search.Document, 0, len(graph.Files))
	symbolsByFile := buildSymbolsByFileIndex(graph)

	for _, f := range graph.Files {
		doc := fileToDocument(f, graph.RootPath, symbolsByFile[f.ID])
		docs = append(docs, doc)
	}

	// Batch index documents
	return p.bleve.IndexBatch(ctx, docs)
}

// buildSymbolsByFileIndex creates a file ID -> symbols index.
func buildSymbolsByFileIndex(graph *CodeGraph) map[uint32][]SymbolNode {
	index := make(map[uint32][]SymbolNode, len(graph.Files))
	for _, s := range graph.Symbols {
		index[s.FileID] = append(index[s.FileID], s)
	}
	return index
}

// fileToDocument converts a FileNode to a search.Document.
func fileToDocument(f FileNode, rootPath string, symbols []SymbolNode) *search.Document {
	// Extract symbol names
	symbolNames := make([]string, len(symbols))
	for i, s := range symbols {
		symbolNames[i] = s.Name
	}

	// Generate content hash for ID
	contentHash := search.GenerateDocumentID([]byte(f.Path))

	return &search.Document{
		ID:         contentHash,
		Path:       f.Path,
		Type:       determineDocType(f.Lang),
		Language:   f.Lang,
		Symbols:    symbolNames,
		Checksum:   contentHash,
		ModifiedAt: time.Now(),
		IndexedAt:  time.Now(),
	}
}

// determineDocType maps language to document type.
func determineDocType(lang string) search.DocumentType {
	switch lang {
	case "markdown":
		return search.DocTypeMarkdown
	case "json", "yaml", "toml":
		return search.DocTypeConfig
	default:
		return search.DocTypeSourceCode
	}
}

// =============================================================================
// Helpers
// =============================================================================

// collectErrors collects all errors from the channel into a single error.
func collectErrors(errChan <-chan error) error {
	var errs []string
	for err := range errChan {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("persistence errors: %s", strings.Join(errs, "; "))
}
