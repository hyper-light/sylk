package archivalist

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// Embedder is the interface for generating text embeddings
type Embedder interface {
	// Embed generates an embedding for the given text
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embeddings for multiple texts
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimension returns the embedding dimension
	Dimension() int
}

// EmbeddingEntry stores an embedding with metadata
type EmbeddingEntry struct {
	ID        string         `json:"id"`
	Text      string         `json:"text,omitempty"` // Original text (optional)
	Embedding []float32      `json:"-"`              // Not serialized directly
	Category  string         `json:"category"`
	SessionID string         `json:"session_id"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// EmbeddingStoreConfig configures the embedding store
type EmbeddingStoreConfig struct {
	// SQLite database path (empty for in-memory only)
	DBPath string

	// Maximum embeddings in memory
	MaxInMemory int

	// Embedding dimension
	Dimension int
}

// DefaultEmbeddingStoreConfig returns sensible defaults
func DefaultEmbeddingStoreConfig() EmbeddingStoreConfig {
	return EmbeddingStoreConfig{
		MaxInMemory: 10000,
		Dimension:   1536,
	}
}

// EmbeddingStore provides vector storage for semantic search
type EmbeddingStore struct {
	mu sync.RWMutex

	// In-memory storage
	entries    map[string]*EmbeddingEntry
	embeddings map[string][]float32

	// Indexes for efficient filtering
	byCategory map[string][]string
	bySession  map[string][]string

	// SQLite persistence (optional)
	db *sql.DB

	// Configuration
	config EmbeddingStoreConfig
}

// NewEmbeddingStore creates a new embedding store
func NewEmbeddingStore(cfg EmbeddingStoreConfig) (*EmbeddingStore, error) {
	if cfg.MaxInMemory == 0 {
		cfg = DefaultEmbeddingStoreConfig()
	}

	store := &EmbeddingStore{
		entries:    make(map[string]*EmbeddingEntry),
		embeddings: make(map[string][]float32),
		byCategory: make(map[string][]string),
		bySession:  make(map[string][]string),
		config:     cfg,
	}

	// Initialize SQLite if path provided
	if cfg.DBPath != "" {
		db, err := sql.Open("sqlite", cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open embedding database: %w", err)
		}

		if err := store.initDB(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to initialize embedding database: %w", err)
		}

		store.db = db
	}

	return store, nil
}

// initDB initializes the SQLite schema
func (es *EmbeddingStore) initDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS embeddings (
			id TEXT PRIMARY KEY,
			text TEXT,
			embedding BLOB,
			category TEXT,
			session_id TEXT,
			metadata TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_embeddings_category ON embeddings(category);
		CREATE INDEX IF NOT EXISTS idx_embeddings_session ON embeddings(session_id);
	`)
	return err
}

// Close closes the embedding store
func (es *EmbeddingStore) Close() error {
	if es.db != nil {
		return es.db.Close()
	}
	return nil
}

// Store stores an embedding
func (es *EmbeddingStore) Store(entry *EmbeddingEntry, embedding []float32) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	// Store in memory
	es.entries[entry.ID] = entry
	es.embeddings[entry.ID] = embedding
	es.byCategory[entry.Category] = append(es.byCategory[entry.Category], entry.ID)
	es.bySession[entry.SessionID] = append(es.bySession[entry.SessionID], entry.ID)

	// Persist to SQLite if available
	if es.db != nil {
		embBytes := encodeEmbedding(embedding)
		metaBytes, _ := json.Marshal(entry.Metadata)

		_, err := es.db.Exec(`
			INSERT OR REPLACE INTO embeddings (id, text, embedding, category, session_id, metadata, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, entry.ID, entry.Text, embBytes, entry.Category, entry.SessionID, metaBytes, entry.CreatedAt)
		if err != nil {
			return fmt.Errorf("failed to persist embedding: %w", err)
		}
	}

	// Evict if over capacity
	if len(es.entries) > es.config.MaxInMemory {
		es.evictOldestLocked()
	}

	return nil
}

// Get retrieves an embedding entry by ID
func (es *EmbeddingStore) Get(id string) (*EmbeddingEntry, []float32, bool) {
	es.mu.RLock()
	entry, ok := es.entries[id]
	embedding := es.embeddings[id]
	es.mu.RUnlock()

	if ok {
		return entry, embedding, true
	}

	// Try SQLite if not in memory
	if es.db != nil {
		entry, embedding, err := es.loadFromDB(id)
		if err == nil && entry != nil {
			return entry, embedding, true
		}
	}

	return nil, nil, false
}

// scoredCandidate holds id and similarity score
type scoredCandidate struct {
	id    string
	score float64
}

// SearchSimilar finds entries most similar to the query embedding
func (es *EmbeddingStore) SearchSimilar(query []float32, opts SearchOptions) ([]*ScoredEntry, error) {
	es.mu.RLock()
	defer es.mu.RUnlock()

	candidates := es.findCandidates(query, opts)
	es.sortCandidates(candidates)
	candidates = es.limitCandidates(candidates, opts.Limit)
	return es.buildResults(candidates), nil
}

func (es *EmbeddingStore) findCandidates(query []float32, opts SearchOptions) []scoredCandidate {
	var candidates []scoredCandidate
	for id, embedding := range es.embeddings {
		if !es.matchesFilters(id, opts) {
			continue
		}
		sim := cosineSimilarity(query, embedding)
		if sim >= opts.MinSimilarity {
			candidates = append(candidates, scoredCandidate{id, sim})
		}
	}
	return candidates
}

func (es *EmbeddingStore) matchesFilters(id string, opts SearchOptions) bool {
	entry := es.entries[id]
	if opts.Category != "" && entry.Category != opts.Category {
		return false
	}
	if opts.SessionID != "" && entry.SessionID != opts.SessionID {
		return false
	}
	return true
}

func (es *EmbeddingStore) sortCandidates(candidates []scoredCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})
}

func (es *EmbeddingStore) limitCandidates(candidates []scoredCandidate, limit int) []scoredCandidate {
	if limit <= 0 {
		limit = 10
	}
	if len(candidates) > limit {
		return candidates[:limit]
	}
	return candidates
}

func (es *EmbeddingStore) buildResults(candidates []scoredCandidate) []*ScoredEntry {
	results := make([]*ScoredEntry, 0, len(candidates))
	for _, c := range candidates {
		results = append(results, &ScoredEntry{Entry: es.entries[c.id], Score: c.score})
	}
	return results
}

// SearchOptions configures semantic search
type SearchOptions struct {
	Limit         int     // Max results to return
	Category      string  // Filter by category
	SessionID     string  // Filter by session
	MinSimilarity float64 // Minimum similarity threshold (0-1)
}

// ScoredEntry is an entry with its similarity score
type ScoredEntry struct {
	Entry *EmbeddingEntry `json:"entry"`
	Score float64         `json:"score"`
}

// DeleteBySession removes all embeddings for a session
func (es *EmbeddingStore) DeleteBySession(sessionID string) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	ids := es.bySession[sessionID]
	for _, id := range ids {
		es.deleteEntryLocked(id)
	}
	delete(es.bySession, sessionID)

	// Also delete from SQLite
	if es.db != nil {
		_, err := es.db.Exec("DELETE FROM embeddings WHERE session_id = ?", sessionID)
		if err != nil {
			return fmt.Errorf("failed to delete embeddings from database: %w", err)
		}
	}

	return nil
}

// DeleteByCategory removes all embeddings for a category
func (es *EmbeddingStore) DeleteByCategory(category string) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	ids := es.byCategory[category]
	for _, id := range ids {
		es.deleteEntryLocked(id)
	}
	delete(es.byCategory, category)

	// Also delete from SQLite
	if es.db != nil {
		_, err := es.db.Exec("DELETE FROM embeddings WHERE category = ?", category)
		if err != nil {
			return fmt.Errorf("failed to delete embeddings from database: %w", err)
		}
	}

	return nil
}

// Stats returns storage statistics
func (es *EmbeddingStore) Stats() EmbeddingStoreStats {
	es.mu.RLock()
	defer es.mu.RUnlock()

	byCategory := make(map[string]int)
	for cat, ids := range es.byCategory {
		byCategory[cat] = len(ids)
	}

	return EmbeddingStoreStats{
		TotalEntries:    len(es.entries),
		ByCategory:      byCategory,
		TotalSessions:   len(es.bySession),
		InMemoryEntries: len(es.entries),
	}
}

// EmbeddingStoreStats contains storage statistics
type EmbeddingStoreStats struct {
	TotalEntries    int            `json:"total_entries"`
	ByCategory      map[string]int `json:"by_category"`
	TotalSessions   int            `json:"total_sessions"`
	InMemoryEntries int            `json:"in_memory_entries"`
}

// loadFromDB loads an embedding from SQLite
func (es *EmbeddingStore) loadFromDB(id string) (*EmbeddingEntry, []float32, error) {
	if es.db == nil {
		return nil, nil, fmt.Errorf("no database configured")
	}

	var entry EmbeddingEntry
	var embBytes, metaBytes []byte

	err := es.db.QueryRow(`
		SELECT id, text, embedding, category, session_id, metadata, created_at
		FROM embeddings WHERE id = ?
	`, id).Scan(&entry.ID, &entry.Text, &embBytes, &entry.Category, &entry.SessionID, &metaBytes, &entry.CreatedAt)

	if err != nil {
		return nil, nil, err
	}

	embedding := decodeEmbedding(embBytes)
	if metaBytes != nil {
		json.Unmarshal(metaBytes, &entry.Metadata)
	}

	return &entry, embedding, nil
}

// deleteEntryLocked removes an entry (must be called with lock held)
func (es *EmbeddingStore) deleteEntryLocked(id string) {
	entry := es.entries[id]
	if entry == nil {
		return
	}

	delete(es.entries, id)
	delete(es.embeddings, id)

	// Remove from category index
	if catIDs, ok := es.byCategory[entry.Category]; ok {
		es.byCategory[entry.Category] = removeFromSlice(catIDs, id)
	}

	// Remove from session index
	if sessIDs, ok := es.bySession[entry.SessionID]; ok {
		es.bySession[entry.SessionID] = removeFromSlice(sessIDs, id)
	}
}

// evictOldestLocked removes oldest entries to make room (must be called with lock held)
func (es *EmbeddingStore) evictOldestLocked() {
	// Evict 10% of entries
	toEvict := len(es.entries) / 10
	if toEvict < 1 {
		toEvict = 1
	}

	type entry struct {
		id        string
		createdAt time.Time
	}

	entries := make([]entry, 0, len(es.entries))
	for id, e := range es.entries {
		entries = append(entries, entry{id, e.CreatedAt})
	}

	// Sort by creation time (oldest first)
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].createdAt.Before(entries[i].createdAt) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// Remove oldest entries
	for i := 0; i < toEvict && i < len(entries); i++ {
		es.deleteEntryLocked(entries[i].id)
	}
}

// encodeEmbedding converts a float32 slice to bytes
func encodeEmbedding(embedding []float32) []byte {
	buf := make([]byte, len(embedding)*4)
	for i, v := range embedding {
		bits := math.Float32bits(v)
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf
}

// decodeEmbedding converts bytes back to a float32 slice
func decodeEmbedding(buf []byte) []float32 {
	embedding := make([]float32, len(buf)/4)
	for i := range embedding {
		bits := binary.LittleEndian.Uint32(buf[i*4:])
		embedding[i] = math.Float32frombits(bits)
	}
	return embedding
}

// ===============================================================================
// Mock Embedder for testing
// ===============================================================================

// MockEmbedder provides a simple embedder for testing
type MockEmbedder struct {
	dimension int
}

// NewMockEmbedder creates a mock embedder
func NewMockEmbedder(dimension int) *MockEmbedder {
	return &MockEmbedder{dimension: dimension}
}

// Embed generates a deterministic embedding based on the text hash
func (m *MockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	// Generate deterministic embedding from text
	hash := hashQuery(text)
	embedding := make([]float32, m.dimension)

	for i := 0; i < m.dimension; i++ {
		// Use hash bytes to seed embedding values
		idx := i % len(hash)
		embedding[i] = float32(hash[idx]) / 255.0
	}

	// Normalize
	var norm float64
	for _, v := range embedding {
		norm += float64(v * v)
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range embedding {
			embedding[i] = float32(float64(embedding[i]) / norm)
		}
	}

	return embedding, nil
}

// EmbedBatch embeds multiple texts
func (m *MockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = emb
	}
	return results, nil
}

// Dimension returns the embedding dimension
func (m *MockEmbedder) Dimension() int {
	return m.dimension
}
