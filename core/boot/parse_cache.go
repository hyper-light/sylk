package boot

import (
	"encoding/gob"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"

	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
)

// ParseCache provides content-hash-keyed caching for parse and chunk results.
// On-disk storage: one gob-encoded file per content hash in cacheDir.
// Thread-safe for concurrent reads/writes via a sharded mutex.
//
// Implements both ingestion.ParseCacheLookup and sylkdir.ChunkCacheLookup.
type ParseCache struct {
	cacheDir string
	shards   [cacheShards]sync.Mutex
}

const cacheShards = 16

// NewParseCache creates a new ParseCache rooted at cacheDir.
// Creates the directory if it doesn't exist.
func NewParseCache(cacheDir string) *ParseCache {
	_ = os.MkdirAll(cacheDir, 0755)
	return &ParseCache{cacheDir: cacheDir}
}

// cacheEntry is the on-disk format for a combined parse + chunk cache entry.
type cacheEntry struct {
	// Parse results.
	Symbols []ingestion.Symbol
	Imports []ingestion.Import
	Lines   int

	// Chunk results (may be empty if not yet cached).
	HasChunks  bool
	Boundaries []sylkdir.ChunkBoundary
	Embeddings [][]float32
}

// LookupParse returns cached parse results for the given content hash, or nil on miss.
func (pc *ParseCache) LookupParse(contentHash [32]byte) *ingestion.ParseCacheEntry {
	entry := pc.load(contentHash)
	if entry == nil {
		return nil
	}
	return &ingestion.ParseCacheEntry{
		Symbols: entry.Symbols,
		Imports: entry.Imports,
		Lines:   entry.Lines,
	}
}

// StoreParse stores parse results keyed by content hash.
// Preserves existing chunk data if present.
func (pc *ParseCache) StoreParse(contentHash [32]byte, pe *ingestion.ParseCacheEntry) {
	shard := pc.shardFor(contentHash)
	pc.shards[shard].Lock()
	defer pc.shards[shard].Unlock()

	existing := pc.loadUnlocked(contentHash)
	entry := &cacheEntry{
		Symbols: pe.Symbols,
		Imports: pe.Imports,
		Lines:   pe.Lines,
	}
	if existing != nil && existing.HasChunks {
		entry.HasChunks = true
		entry.Boundaries = existing.Boundaries
		entry.Embeddings = existing.Embeddings
	}
	pc.storeUnlocked(contentHash, entry)
}

// LookupChunk returns cached chunk results for the given content hash, or nil on miss.
func (pc *ParseCache) LookupChunk(contentHash [32]byte) *sylkdir.ChunkCacheEntry {
	entry := pc.load(contentHash)
	if entry == nil || !entry.HasChunks {
		return nil
	}
	return &sylkdir.ChunkCacheEntry{
		Boundaries: entry.Boundaries,
		Embeddings: entry.Embeddings,
	}
}

// StoreChunk stores chunk results keyed by content hash.
// Preserves existing parse data if present.
func (pc *ParseCache) StoreChunk(contentHash [32]byte, ce *sylkdir.ChunkCacheEntry) {
	shard := pc.shardFor(contentHash)
	pc.shards[shard].Lock()
	defer pc.shards[shard].Unlock()

	existing := pc.loadUnlocked(contentHash)
	entry := &cacheEntry{
		HasChunks:  true,
		Boundaries: ce.Boundaries,
		Embeddings: ce.Embeddings,
	}
	if existing != nil {
		entry.Symbols = existing.Symbols
		entry.Imports = existing.Imports
		entry.Lines = existing.Lines
	}
	pc.storeUnlocked(contentHash, entry)
}

// load reads a cache entry from disk. Thread-safe.
func (pc *ParseCache) load(contentHash [32]byte) *cacheEntry {
	shard := pc.shardFor(contentHash)
	pc.shards[shard].Lock()
	defer pc.shards[shard].Unlock()
	return pc.loadUnlocked(contentHash)
}

// loadUnlocked reads a cache entry from disk. Caller must hold the shard lock.
func (pc *ParseCache) loadUnlocked(contentHash [32]byte) *cacheEntry {
	path := pc.entryPath(contentHash)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entry cacheEntry
	if err := gob.NewDecoder(f).Decode(&entry); err != nil {
		return nil // Corrupt cache entry — treat as miss.
	}
	return &entry
}

// storeUnlocked writes a cache entry to disk. Caller must hold the shard lock.
func (pc *ParseCache) storeUnlocked(contentHash [32]byte, entry *cacheEntry) {
	path := pc.entryPath(contentHash)
	_ = os.MkdirAll(filepath.Dir(path), 0755)

	// Write to temp file then rename for atomic replacement.
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}

	if err := gob.NewEncoder(f).Encode(entry); err != nil {
		f.Close()
		os.Remove(tmp)
		return
	}
	f.Close()
	os.Rename(tmp, path)
}

// entryPath returns the file path for a content hash.
// Uses first 2 hex chars as subdirectory for filesystem fan-out.
func (pc *ParseCache) entryPath(contentHash [32]byte) string {
	h := hex.EncodeToString(contentHash[:])
	return filepath.Join(pc.cacheDir, h[:2], h[2:]+".gob")
}

// shardFor returns the shard index for a content hash.
func (pc *ParseCache) shardFor(contentHash [32]byte) int {
	return int(contentHash[0]) % cacheShards
}
