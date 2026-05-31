// Package sylkdir provides per-version Bleve full-text search indices for the global KG.
package sylkdir

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/adalundhe/sylk/core/search"
	"github.com/blevesearch/bleve/v2"
)

// ErrBleveHeadUnavailable is returned by read-only HEAD open paths when the
// committed HEAD Bleve index is missing or cannot be opened without recovery.
var ErrBleveHeadUnavailable = errors.New("global version bleve store: head bleve unavailable")

// GlobalVersionBleveStore manages per-version Bleve indices for the global knowledge graph.
// Each version has its own Bleve index at knowledge/versions/<version>/bleve/documents.bleve.
// Only the HEAD version's Bleve is kept open for writes.
// Versions are cumulative: each version's Bleve contains ALL documents reachable
// from that version (its own docs plus all ancestor docs).
type GlobalVersionBleveStore struct {
	sylkDir *SylkDir
	head    SemanticVersion
	mu      sync.RWMutex
	store   *BleveStore // Currently open HEAD Bleve (nil if not open)
}

// NewGlobalVersionBleveStore creates a new GlobalVersionBleveStore for the given SylkDir.
func NewGlobalVersionBleveStore(sd *SylkDir, head SemanticVersion) *GlobalVersionBleveStore {
	return &GlobalVersionBleveStore{
		sylkDir: sd,
		head:    head,
	}
}

// SetHead updates the HEAD version without opening it.
// Call OpenHead after this to open the new HEAD.
func (gvbs *GlobalVersionBleveStore) SetHead(head SemanticVersion) {
	gvbs.mu.Lock()
	defer gvbs.mu.Unlock()
	gvbs.head = head
}

// Head returns the current HEAD version.
func (gvbs *GlobalVersionBleveStore) Head() SemanticVersion {
	gvbs.mu.RLock()
	defer gvbs.mu.RUnlock()
	return gvbs.head
}

// BlevePath returns the path to the Bleve directory for a specific version.
func (gvbs *GlobalVersionBleveStore) BlevePath(version SemanticVersion) string {
	return filepath.Join(gvbs.sylkDir.GlobalVersionPath(version), "bleve")
}

// HeadBlevePath returns the path to the HEAD version's Bleve directory.
func (gvbs *GlobalVersionBleveStore) HeadBlevePath() string {
	gvbs.mu.RLock()
	defer gvbs.mu.RUnlock()
	return gvbs.BlevePath(gvbs.head)
}

// OpenHead opens the HEAD version's Bleve index.
// If the Bleve directory is missing (lazy snapshot), rebuilds from doc data.
func (gvbs *GlobalVersionBleveStore) OpenHead() error {
	gvbs.mu.Lock()
	defer gvbs.mu.Unlock()

	if gvbs.store != nil && gvbs.store.Manager() != nil {
		return nil // Already open
	}

	return gvbs.openOrRebuildLocked()
}

// OpenExistingHead opens the HEAD version's Bleve index only if it already
// exists and can be opened as-is. It never rebuilds, creates, deletes, or
// otherwise mutates the Bleve directory, so startup readers can use it without
// turning a missing/corrupt derived index into boot-time write work.
func (gvbs *GlobalVersionBleveStore) OpenExistingHead() error {
	gvbs.mu.Lock()
	defer gvbs.mu.Unlock()

	if gvbs.store != nil && gvbs.store.Manager() != nil {
		return nil
	}

	return gvbs.openExistingLocked()
}

// openExistingLocked opens an existing HEAD Bleve without repair.
// Must be called with gvbs.mu held.
func (gvbs *GlobalVersionBleveStore) openExistingLocked() error {
	blevePath := gvbs.BlevePath(gvbs.head)
	bleveDBPath := filepath.Join(blevePath, "documents.bleve")

	if _, err := os.Stat(bleveDBPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrBleveHeadUnavailable
		}
		return fmt.Errorf("%w: stat %s: %v", ErrBleveHeadUnavailable, bleveDBPath, err)
	}

	store := NewBleveStoreAtPath(blevePath)
	if err := store.OpenExisting(); err != nil {
		_ = store.Close()
		return fmt.Errorf("%w: open %s: %v", ErrBleveHeadUnavailable, blevePath, err)
	}
	gvbs.store = store
	return nil
}

// openOrRebuildLocked opens or rebuilds the HEAD Bleve.
// Must be called with gvbs.mu held.
func (gvbs *GlobalVersionBleveStore) openOrRebuildLocked() error {
	blevePath := gvbs.BlevePath(gvbs.head)
	bleveDBPath := filepath.Join(blevePath, "documents.bleve")

	// Try opening existing Bleve first.
	if _, err := os.Stat(bleveDBPath); err == nil {
		gvbs.store = NewBleveStoreAtPath(blevePath)
		if err := gvbs.store.Open(); err == nil {
			return nil
		}
		// Open failed — fall through to rebuild.
		gvbs.store = nil
	}

	// Bleve missing or corrupt — rebuild from doc shared data file.
	return gvbs.rebuildLocked()
}

// CloseHead closes the HEAD version's Bleve index.
// Safe to call if no Bleve is open.
func (gvbs *GlobalVersionBleveStore) CloseHead() error {
	gvbs.mu.Lock()
	defer gvbs.mu.Unlock()

	if gvbs.store == nil {
		return nil
	}
	err := gvbs.store.Close()
	gvbs.store = nil
	return err
}

// CloseAll closes any open Bleve instances.
// Currently only HEAD is kept open, so this is equivalent to CloseHead.
func (gvbs *GlobalVersionBleveStore) CloseAll() error {
	return gvbs.CloseHead()
}

// Index indexes a single document into the HEAD version's Bleve.
func (gvbs *GlobalVersionBleveStore) Index(ctx context.Context, doc *search.Document) error {
	gvbs.mu.RLock()
	defer gvbs.mu.RUnlock()

	if gvbs.store == nil {
		return fmt.Errorf("global version bleve store: head not open")
	}
	return gvbs.store.Index(ctx, doc)
}

// IndexBatch indexes multiple documents into the HEAD version's Bleve.
func (gvbs *GlobalVersionBleveStore) IndexBatch(ctx context.Context, docs []*search.Document) error {
	gvbs.mu.RLock()
	defer gvbs.mu.RUnlock()

	if gvbs.store == nil {
		return fmt.Errorf("global version bleve store: head not open")
	}
	return gvbs.store.IndexBatch(ctx, docs)
}

// Search searches the HEAD version's Bleve.
// Since versions are cumulative, HEAD contains all documents reachable from HEAD.
func (gvbs *GlobalVersionBleveStore) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	gvbs.mu.RLock()
	defer gvbs.mu.RUnlock()

	if gvbs.store == nil {
		return nil, nil
	}
	return gvbs.store.Search(ctx, req)
}

// DocumentCount returns the number of documents in the HEAD version's Bleve.
func (gvbs *GlobalVersionBleveStore) DocumentCount() (uint64, error) {
	gvbs.mu.RLock()
	defer gvbs.mu.RUnlock()

	if gvbs.store == nil {
		return 0, nil
	}
	return gvbs.store.DocumentCount()
}

// SnapshotBleve prepares the target version's Bleve by rebuilding from the
// doc store. Bleve indices use UnsafeBatch (no WAL) so segment data is
// volatile across close/reopen — directory copies produce empty indices.
// Rebuilding from the cumulative doc store is the only reliable path.
func (gvbs *GlobalVersionBleveStore) SnapshotBleve(parent, target SemanticVersion) error {
	gvbs.mu.Lock()
	defer gvbs.mu.Unlock()

	if gvbs.store != nil {
		if err := gvbs.store.Close(); err != nil {
			return fmt.Errorf("close global head bleve before snapshot: %w", err)
		}
		gvbs.store = nil
	}

	gvbs.head = target
	return gvbs.rebuildLocked()
}

// SnapshotOrPromote prepares the target version's Bleve using the best available source.
// On incremental boot, copies the parent global Bleve (contains all prior docs).
// On cold start (no parent Bleve), promotes the session Bleve (contains all ingested docs).
// Returns true if the session Bleve was promoted (all docs already indexed).
func (gvbs *GlobalVersionBleveStore) SnapshotOrPromote(parent, target SemanticVersion, sessionBlevePath string) (bool, error) {
	gvbs.mu.Lock()
	defer gvbs.mu.Unlock()

	if err := gvbs.closeStoreLocked(); err != nil {
		return false, err
	}
	promoted := gvbs.selectAndCopySource(parent, target, sessionBlevePath)
	gvbs.head = target
	return promoted, gvbs.openOrCreateLocked()
}

// closeStoreLocked closes the current open Bleve. Caller holds gvbs.mu.
func (gvbs *GlobalVersionBleveStore) closeStoreLocked() error {
	if gvbs.store == nil {
		return nil
	}
	err := gvbs.store.Close()
	gvbs.store = nil
	return err
}

// selectAndCopySource picks the best Bleve source for the target version.
// Returns true if the session Bleve was promoted (caller can skip indexing).
func (gvbs *GlobalVersionBleveStore) selectAndCopySource(parent, target SemanticVersion, sessionBlevePath string) bool {
	parentDB := filepath.Join(gvbs.BlevePath(parent), "documents.bleve")
	if _, err := os.Stat(parentDB); err == nil {
		_ = copyDir(gvbs.BlevePath(parent), gvbs.BlevePath(target))
		return false
	}
	if sessionBlevePath != "" {
		_ = copyDir(sessionBlevePath, gvbs.BlevePath(target))
		return true
	}
	return false
}

// openOrCreateLocked opens existing Bleve or creates a fresh empty one.
// Unlike openOrRebuildLocked, does NOT rebuild from doc data.
// Must be called with gvbs.mu held.
func (gvbs *GlobalVersionBleveStore) openOrCreateLocked() error {
	blevePath := gvbs.BlevePath(gvbs.head)
	bleveDBPath := filepath.Join(blevePath, "documents.bleve")

	if _, err := os.Stat(bleveDBPath); err == nil {
		gvbs.store = NewBleveStoreAtPath(blevePath)
		if err := gvbs.store.Open(); err == nil {
			return nil
		}
		gvbs.store = nil
	}

	gvbs.store = NewBleveStoreAtPath(blevePath)
	return gvbs.store.Open()
}

// RebuildHead rebuilds the HEAD version's Bleve from the HEAD version's doc data.
// This is used for recovery when the Bleve database is missing or corrupt.
func (gvbs *GlobalVersionBleveStore) RebuildHead() error {
	gvbs.mu.Lock()
	defer gvbs.mu.Unlock()
	return gvbs.rebuildLocked()
}

// rebuildLocked rebuilds the HEAD Bleve from doc data. Must be called with gvbs.mu held.
func (gvbs *GlobalVersionBleveStore) rebuildLocked() error {
	// Close and delete existing HEAD Bleve if present
	if gvbs.store != nil {
		if err := gvbs.store.Delete(); err != nil {
			return fmt.Errorf("delete existing global head bleve: %w", err)
		}
		gvbs.store = nil
	}

	// Read docs from HEAD version's shared data file (which is cumulative)
	docStore, err := NewGlobalVersionDocStore(gvbs.sylkDir, gvbs.head)
	if err != nil {
		return fmt.Errorf("open global doc store for rebuild: %w", err)
	}
	defer docStore.Close()
	docs, err := docStore.ReadAll()
	if err != nil {
		return fmt.Errorf("read docs for global bleve rebuild: %w", err)
	}

	// Open fresh Bleve
	blevePath := gvbs.BlevePath(gvbs.head)
	gvbs.store = NewBleveStoreAtPath(blevePath)
	if err := gvbs.store.Open(); err != nil {
		gvbs.store = nil
		return fmt.Errorf("open global bleve for rebuild: %w", err)
	}

	// Index all docs
	if len(docs) > 0 {
		searchDocs := make([]*search.Document, len(docs))
		for i, vdoc := range docs {
			searchDocs[i] = ConvertToSearchDocument(vdoc)
		}
		if err := gvbs.store.IndexBatch(context.Background(), searchDocs); err != nil {
			return fmt.Errorf("reindex docs into global bleve: %w", err)
		}
	}

	return nil
}

// Store returns the underlying BleveStore for HEAD (for advanced operations).
// Returns nil if HEAD is not open.
func (gvbs *GlobalVersionBleveStore) Store() *BleveStore {
	gvbs.mu.RLock()
	defer gvbs.mu.RUnlock()
	return gvbs.store
}

// RawIndex returns the underlying bleve.Index for direct query access.
// Returns nil if HEAD is not open. The caller must not close it.
func (gvbs *GlobalVersionBleveStore) RawIndex() bleve.Index {
	gvbs.mu.RLock()
	defer gvbs.mu.RUnlock()
	if gvbs.store == nil || gvbs.store.Manager() == nil {
		return nil
	}
	return gvbs.store.Manager().RawIndex()
}
