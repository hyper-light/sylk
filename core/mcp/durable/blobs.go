package durable

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BlobStore is a content-addressed file store for out-of-band MCP payloads
// (large resource bodies, image content, etc). Files are placed under
// <session>/mcp/blobs/<sha256>. Read-after-write returns the stable digest
// the runtime records on the operation.
type BlobStore struct {
	dir string
}

// NewBlobStore opens (or creates) a per-session blob directory.
func NewBlobStore(sessionDir string) (*BlobStore, error) {
	dir := filepath.Join(sessionDir, "mcp", "blobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mcp blobs: ensure dir: %w", err)
	}
	return &BlobStore{dir: dir}, nil
}

// Put writes content under its sha256 digest. Returns the digest as a hex
// string. Existing content is left untouched (write is idempotent).
func (b *BlobStore) Put(content []byte) (string, error) {
	if b == nil {
		return "", errors.New("mcp blobs: store not initialized")
	}
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	path := filepath.Join(b.dir, digest)
	if _, err := os.Stat(path); err == nil {
		return digest, nil
	}
	tmp, err := os.CreateTemp(b.dir, "blob-*.tmp")
	if err != nil {
		return "", fmt.Errorf("mcp blobs: temp: %w", err)
	}
	tmpPath := tmp.Name()
	if err := writeAndClose(tmp, content); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("mcp blobs: rename: %w", err)
	}
	return digest, nil
}

func writeAndClose(f *os.File, content []byte) error {
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("mcp blobs: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("mcp blobs: fsync: %w", err)
	}
	return f.Close()
}

// Open returns an io.ReadCloser for a stored blob.
func (b *BlobStore) Open(digest string) (io.ReadCloser, error) {
	if b == nil {
		return nil, errors.New("mcp blobs: store not initialized")
	}
	return os.Open(filepath.Join(b.dir, digest))
}

// Path returns the absolute path of a blob.
func (b *BlobStore) Path(digest string) string {
	if b == nil {
		return ""
	}
	return filepath.Join(b.dir, digest)
}
