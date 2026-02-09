// Package sylkdir provides per-version Bleve full-text search indices.
package sylkdir

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/adalundhe/sylk/core/search"
)

// VersionBleveStore manages per-version Bleve indices within a session.
// Each version has its own Bleve index at versions/<version>/bleve/documents.bleve.
// Only the HEAD version's Bleve is kept open for writes.
// Versions are cumulative: each version's Bleve contains ALL documents reachable
// from that version (its own docs plus all ancestor docs).
type VersionBleveStore struct {
	session *Session
	mu      sync.RWMutex
	head    *BleveStore // Currently open HEAD Bleve (nil if not open)
}

// NewVersionBleveStore creates a new VersionBleveStore for the given session.
func NewVersionBleveStore(sess *Session) *VersionBleveStore {
	return &VersionBleveStore{
		session: sess,
	}
}

// BlevePath returns the path to the Bleve directory for a specific version.
func (vbs *VersionBleveStore) BlevePath(version SemanticVersion) string {
	return filepath.Join(vbs.session.VersionPath(version), "bleve")
}

// HeadBlevePath returns the path to the HEAD version's Bleve directory.
func (vbs *VersionBleveStore) HeadBlevePath() string {
	return vbs.BlevePath(vbs.session.Manifest.Head)
}

// OpenHead opens the HEAD version's Bleve index.
// If the Bleve directory is missing (lazy snapshot), rebuilds from doc data.
func (vbs *VersionBleveStore) OpenHead() error {
	vbs.mu.Lock()
	defer vbs.mu.Unlock()

	if vbs.head != nil && vbs.head.Manager() != nil {
		return nil // Already open
	}

	return vbs.openOrRebuildLocked(vbs.session.Manifest.Head)
}

// openOrRebuildLocked opens or rebuilds the Bleve for a version.
// Must be called with vbs.mu held.
func (vbs *VersionBleveStore) openOrRebuildLocked(version SemanticVersion) error {
	blevePath := vbs.BlevePath(version)
	bleveDBPath := filepath.Join(blevePath, "documents.bleve")

	// Try opening existing Bleve first.
	if _, err := os.Stat(bleveDBPath); err == nil {
		vbs.head = NewBleveStoreAtPath(blevePath)
		if err := vbs.head.Open(); err == nil {
			return nil
		}
		// Open failed — fall through to rebuild.
		vbs.head = nil
	}

	// Bleve missing or corrupt — rebuild from doc shared data file.
	return vbs.rebuildLocked()
}

// CloseHead closes the HEAD version's Bleve index.
// Safe to call if no Bleve is open.
func (vbs *VersionBleveStore) CloseHead() error {
	vbs.mu.Lock()
	defer vbs.mu.Unlock()

	if vbs.head == nil {
		return nil
	}
	err := vbs.head.Close()
	vbs.head = nil
	return err
}

// CloseAll closes any open Bleve instances.
// Currently only HEAD is kept open, so this is equivalent to CloseHead.
func (vbs *VersionBleveStore) CloseAll() error {
	return vbs.CloseHead()
}

// Index indexes a single document into the HEAD version's Bleve.
func (vbs *VersionBleveStore) Index(ctx context.Context, doc *search.Document) error {
	vbs.mu.RLock()
	defer vbs.mu.RUnlock()

	if vbs.head == nil {
		return fmt.Errorf("version bleve store: head not open")
	}
	return vbs.head.Index(ctx, doc)
}

// IndexBatch indexes multiple documents into the HEAD version's Bleve.
func (vbs *VersionBleveStore) IndexBatch(ctx context.Context, docs []*search.Document) error {
	vbs.mu.RLock()
	defer vbs.mu.RUnlock()

	if vbs.head == nil {
		return fmt.Errorf("version bleve store: head not open")
	}
	return vbs.head.IndexBatch(ctx, docs)
}

// Search searches the HEAD version's Bleve.
// Since versions are cumulative, HEAD contains all documents reachable from HEAD.
func (vbs *VersionBleveStore) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	vbs.mu.RLock()
	defer vbs.mu.RUnlock()

	if vbs.head == nil {
		return nil, nil
	}
	return vbs.head.Search(ctx, req)
}

// DocumentCount returns the number of documents in the HEAD version's Bleve.
func (vbs *VersionBleveStore) DocumentCount() (uint64, error) {
	vbs.mu.RLock()
	defer vbs.mu.RUnlock()

	if vbs.head == nil {
		return 0, nil
	}
	return vbs.head.DocumentCount()
}

// SnapshotBleve closes the parent HEAD Bleve and defers target Bleve creation.
// The target version's Bleve will be rebuilt from doc data on first OpenHead call.
// This keeps checkpoint latency O(1) for the Bleve layer; indexDocsIntoSessionBleve
// safely no-ops while head is nil.
func (vbs *VersionBleveStore) SnapshotBleve(parent, target SemanticVersion) error {
	vbs.mu.Lock()
	defer vbs.mu.Unlock()

	if vbs.head != nil {
		if err := vbs.head.Close(); err != nil {
			return fmt.Errorf("close head bleve before snapshot: %w", err)
		}
		vbs.head = nil
	}

	return nil
}

// RebuildHead rebuilds the HEAD version's Bleve from the HEAD version's doc data.
// This is used for recovery when the Bleve database is missing or corrupt.
func (vbs *VersionBleveStore) RebuildHead() error {
	vbs.mu.Lock()
	defer vbs.mu.Unlock()
	return vbs.rebuildLocked()
}

// rebuildLocked rebuilds the HEAD Bleve from doc data. Must be called with vbs.mu held.
func (vbs *VersionBleveStore) rebuildLocked() error {
	// Close and delete existing HEAD Bleve if present
	if vbs.head != nil {
		if err := vbs.head.Delete(); err != nil {
			return fmt.Errorf("delete existing head bleve: %w", err)
		}
		vbs.head = nil
	}

	// Read docs from HEAD version's shared data file (which is cumulative).
	// Use registered doc store if available (has in-memory indexes).
	var docs []*VersionDocument
	var err error
	if vbs.session.docStore != nil {
		docs, err = vbs.session.docStore.ReadFromVersion(vbs.session.Manifest.Head)
	} else {
		tempStore := NewVersionDocStore(vbs.session)
		docs, err = tempStore.ReadFromVersion(vbs.session.Manifest.Head)
	}
	if err != nil {
		return fmt.Errorf("read docs for bleve rebuild: %w", err)
	}

	// Open fresh Bleve
	blevePath := vbs.HeadBlevePath()
	vbs.head = NewBleveStoreAtPath(blevePath)
	if err := vbs.head.Open(); err != nil {
		vbs.head = nil
		return fmt.Errorf("open bleve for rebuild: %w", err)
	}

	// Index all docs
	if len(docs) > 0 {
		searchDocs := make([]*search.Document, len(docs))
		for i, vdoc := range docs {
			searchDocs[i] = ConvertToSearchDocument(vdoc)
		}
		if err := vbs.head.IndexBatch(context.Background(), searchDocs); err != nil {
			return fmt.Errorf("reindex docs into bleve: %w", err)
		}
	}

	return nil
}

// Head returns the underlying BleveStore for HEAD (for advanced operations).
// Returns nil if HEAD is not open.
func (vbs *VersionBleveStore) Head() *BleveStore {
	vbs.mu.RLock()
	defer vbs.mu.RUnlock()
	return vbs.head
}

// copyDir recursively copies a directory from src to dst.
func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// copyFileIfExists copies a file if it exists, skips if not.
func copyFileIfExists(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	return copyFile(src, dst)
}
