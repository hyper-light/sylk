// Package context provides CV.1 UniversalContentStore for the Context Virtualization System.
// It wraps VectorGraphDB + Bleve for hybrid search with WAVE 4 compliant resource management.
package context

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/resources"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/bleve"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// ContentStoreConfig holds configuration for the UniversalContentStore.
type ContentStoreConfig struct {
	BlevePath        string
	VectorDB         *vectorgraphdb.VectorGraphDB
	IndexQueueSize   int
	IndexWorkers     int
	GoroutineBudget  *concurrency.GoroutineBudget
	FileHandleBudget *resources.FileHandleBudget
	Logger           *slog.Logger
}

// DefaultContentStoreConfig returns sensible defaults.
func DefaultContentStoreConfig(basePath string) ContentStoreConfig {
	return ContentStoreConfig{
		BlevePath:      basePath + "/documents.bleve",
		IndexQueueSize: 1000,
		IndexWorkers:   4,
	}
}

// UniversalContentStore provides centralized storage for all content,
// wrapping VectorGraphDB (SQLite-based) and Bleve (file-based) for hybrid search.
type UniversalContentStore struct {
	bleveIndex       *bleve.IndexManager
	vectorDB         *vectorgraphdb.VectorGraphDB
	nodeStore        *vectorgraphdb.NodeStore
	indexQueue       chan *ContentEntry
	workers          int
	goroutineBudget  *concurrency.GoroutineBudget
	fileHandleBudget *resources.FileHandleBudget
	logger           *slog.Logger
	mu               sync.RWMutex
	closed           bool
	wg               sync.WaitGroup
	cancelFunc       context.CancelFunc
}

var (
	ErrStoreClosed   = errors.New("content store is closed")
	ErrNilEntry      = errors.New("content entry cannot be nil")
	ErrEmptyContent  = errors.New("content cannot be empty")
	ErrInvalidConfig = errors.New("invalid configuration")
)

// NewUniversalContentStore creates a new content store with the given configuration.
func NewUniversalContentStore(cfg ContentStoreConfig) (*UniversalContentStore, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	store := &UniversalContentStore{
		vectorDB:         cfg.VectorDB,
		nodeStore:        vectorgraphdb.NewNodeStore(cfg.VectorDB, nil),
		workers:          normalizeWorkers(cfg.IndexWorkers),
		goroutineBudget:  cfg.GoroutineBudget,
		fileHandleBudget: cfg.FileHandleBudget,
		logger:           normalizeLogger(cfg.Logger),
	}

	if err := store.initBleveIndex(cfg.BlevePath); err != nil {
		return nil, fmt.Errorf("init bleve index: %w", err)
	}

	store.indexQueue = make(chan *ContentEntry, normalizeQueueSize(cfg.IndexQueueSize))

	ctx, cancel := context.WithCancel(context.Background())
	store.cancelFunc = cancel
	store.startWorkers(ctx)

	if err := store.ensureSchema(); err != nil {
		return nil, fmt.Errorf("ensure schema: %w", err)
	}

	return store, nil
}

func validateConfig(cfg ContentStoreConfig) error {
	if cfg.VectorDB == nil {
		return fmt.Errorf("%w: VectorDB is required", ErrInvalidConfig)
	}
	if cfg.BlevePath == "" {
		return fmt.Errorf("%w: BlevePath is required", ErrInvalidConfig)
	}
	return nil
}

func normalizeWorkers(workers int) int {
	if workers <= 0 {
		return 4
	}
	return workers
}

func normalizeQueueSize(size int) int {
	if size <= 0 {
		return 1000
	}
	return size
}

func normalizeLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}

func (s *UniversalContentStore) initBleveIndex(path string) error {
	s.bleveIndex = bleve.NewIndexManager(path)
	return s.bleveIndex.Open()
}

func (s *UniversalContentStore) ensureSchema() error {
	db := s.vectorDB.DB()
	schema := `
		CREATE TABLE IF NOT EXISTS content_entries (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			agent_type TEXT NOT NULL,
			content_type TEXT NOT NULL,
			content TEXT NOT NULL,
			token_count INTEGER NOT NULL,
			timestamp INTEGER NOT NULL,
			turn_number INTEGER NOT NULL,
			keywords TEXT,
			entities TEXT,
			parent_id TEXT,
			related_files TEXT,
			metadata TEXT,
			created_at INTEGER DEFAULT (unixepoch())
		);

		CREATE INDEX IF NOT EXISTS idx_content_session ON content_entries(session_id);
		CREATE INDEX IF NOT EXISTS idx_content_agent ON content_entries(agent_id);
		CREATE INDEX IF NOT EXISTS idx_content_type ON content_entries(content_type);
		CREATE INDEX IF NOT EXISTS idx_content_turn ON content_entries(session_id, turn_number);
		CREATE INDEX IF NOT EXISTS idx_content_timestamp ON content_entries(timestamp);

		CREATE TABLE IF NOT EXISTS context_references (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			content_ids TEXT NOT NULL,
			summary TEXT NOT NULL,
			tokens_saved INTEGER NOT NULL,
			turn_range_start INTEGER NOT NULL,
			turn_range_end INTEGER NOT NULL,
			topics TEXT,
			entities TEXT,
			query_hints TEXT,
			ref_type TEXT NOT NULL,
			timestamp INTEGER NOT NULL,
			created_at INTEGER DEFAULT (unixepoch())
		);

		CREATE INDEX IF NOT EXISTS idx_ref_session ON context_references(session_id);
		CREATE INDEX IF NOT EXISTS idx_ref_agent ON context_references(agent_id);
	`
	_, err := db.Exec(schema)
	return err
}

func (s *UniversalContentStore) startWorkers(ctx context.Context) {
	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.indexWorker(ctx)
	}
}

// IndexContent indexes a content entry asynchronously.
func (s *UniversalContentStore) IndexContent(entry *ContentEntry) error {
	if err := s.validateEntry(entry); err != nil {
		return err
	}
	if err := s.checkClosed(); err != nil {
		return err
	}

	s.ensureEntryID(entry)
	s.tryIndexBleve(entry)
	return s.tryQueueOrSync(entry)
}

func (s *UniversalContentStore) checkClosed() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrStoreClosed
	}
	return nil
}

func (s *UniversalContentStore) tryIndexBleve(entry *ContentEntry) {
	if err := s.indexBleve(entry); err != nil {
		s.logger.Warn("bleve index failed", "id", entry.ID, "error", err)
	}
}

func (s *UniversalContentStore) tryQueueOrSync(entry *ContentEntry) error {
	if err := s.queueForVectorIndex(entry); err != nil {
		return s.indexSync(entry)
	}
	return nil
}

func (s *UniversalContentStore) validateEntry(entry *ContentEntry) error {
	if entry == nil {
		return ErrNilEntry
	}
	if entry.Content == "" {
		return ErrEmptyContent
	}
	return nil
}

func (s *UniversalContentStore) ensureEntryID(entry *ContentEntry) {
	if entry.ID == "" {
		entry.ID = GenerateContentID([]byte(entry.Content))
	}
}

// GenerateContentID generates a deterministic ID from content.
func GenerateContentID(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

func (s *UniversalContentStore) indexBleve(entry *ContentEntry) error {
	doc := s.entryToDocument(entry)
	return s.bleveIndex.Index(context.Background(), doc)
}

func (s *UniversalContentStore) entryToDocument(entry *ContentEntry) *search.Document {
	return &search.Document{
		ID:       entry.ID,
		Path:     s.getEntryPath(entry),
		Type:     s.contentTypeToDocType(entry.ContentType),
		Content:  entry.Content,
		Language: s.getLanguageFromMetadata(entry),
		Symbols:  entry.Keywords,
	}
}

func (s *UniversalContentStore) getEntryPath(entry *ContentEntry) string {
	if len(entry.RelatedFiles) > 0 {
		return entry.RelatedFiles[0]
	}
	return entry.ID
}

func (s *UniversalContentStore) contentTypeToDocType(ct ContentType) search.DocumentType {
	switch ct {
	case ContentTypeCodeFile:
		return search.DocTypeSourceCode
	default:
		return search.DocTypeSourceCode
	}
}

func (s *UniversalContentStore) getLanguageFromMetadata(entry *ContentEntry) string {
	if entry.Metadata != nil {
		if lang, ok := entry.Metadata["language"]; ok {
			return lang
		}
	}
	return ""
}

func (s *UniversalContentStore) queueForVectorIndex(entry *ContentEntry) error {
	select {
	case s.indexQueue <- entry:
		return nil
	default:
		return errors.New("index queue full")
	}
}

func (s *UniversalContentStore) indexSync(entry *ContentEntry) error {
	return s.storeEntryInDB(entry)
}

func (s *UniversalContentStore) indexWorker(ctx context.Context) {
	defer s.wg.Done()

	for {
		if s.processNextEntry(ctx) {
			return
		}
	}
}

func (s *UniversalContentStore) processNextEntry(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	case entry, ok := <-s.indexQueue:
		if !ok {
			return true
		}
		s.storeEntryWithLogging(entry)
		return false
	}
}

func (s *UniversalContentStore) storeEntryWithLogging(entry *ContentEntry) {
	if err := s.storeEntryInDB(entry); err != nil {
		s.logger.Warn("store entry failed", "id", entry.ID, "error", err)
	}
}

func (s *UniversalContentStore) storeEntryInDB(entry *ContentEntry) error {
	db := s.vectorDB.DB()

	keywords, _ := json.Marshal(entry.Keywords)
	entities, _ := json.Marshal(entry.Entities)
	relatedFiles, _ := json.Marshal(entry.RelatedFiles)
	metadata, _ := json.Marshal(entry.Metadata)

	_, err := db.Exec(`
		INSERT OR REPLACE INTO content_entries
		(id, session_id, agent_id, agent_type, content_type, content, token_count,
		 timestamp, turn_number, keywords, entities, parent_id, related_files, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, entry.ID, entry.SessionID, entry.AgentID, entry.AgentType,
		string(entry.ContentType), entry.Content, entry.TokenCount,
		entry.Timestamp.Unix(), entry.TurnNumber,
		string(keywords), string(entities), entry.ParentID,
		string(relatedFiles), string(metadata))

	return err
}

// Search performs hybrid search combining Bleve and DB results.
func (s *UniversalContentStore) Search(
	query string,
	filters *SearchFilters,
	limit int,
) ([]*ContentEntry, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrStoreClosed
	}
	s.mu.RUnlock()

	return s.hybridSearch(context.Background(), query, filters, limit)
}

func (s *UniversalContentStore) hybridSearch(
	ctx context.Context,
	query string,
	filters *SearchFilters,
	limit int,
) ([]*ContentEntry, error) {
	var wg sync.WaitGroup
	var bleveResults, dbResults []*ContentEntry
	var bleveErr, dbErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		bleveResults, bleveErr = s.searchBleve(ctx, query, limit*2)
	}()
	go func() {
		defer wg.Done()
		dbResults, dbErr = s.searchDB(ctx, query, filters, limit*2)
	}()
	wg.Wait()

	if bleveErr != nil && dbErr != nil {
		return nil, fmt.Errorf("both searches failed: bleve=%v, db=%v", bleveErr, dbErr)
	}

	return s.fuseResults(bleveResults, dbResults, limit), nil
}

func (s *UniversalContentStore) searchBleve(
	ctx context.Context,
	query string,
	limit int,
) ([]*ContentEntry, error) {
	req := &search.SearchRequest{
		Query: query,
		Limit: limit,
	}

	result, err := s.bleveIndex.Search(ctx, req)
	if err != nil {
		return nil, err
	}

	return s.bleveResultsToEntries(result), nil
}

func (s *UniversalContentStore) bleveResultsToEntries(result *search.SearchResult) []*ContentEntry {
	entries := make([]*ContentEntry, 0, len(result.Documents))
	for _, doc := range result.Documents {
		entry := s.documentToEntry(&doc.Document, doc.Score)
		entries = append(entries, entry)
	}
	return entries
}

func (s *UniversalContentStore) documentToEntry(doc *search.Document, score float64) *ContentEntry {
	return &ContentEntry{
		ID:          doc.ID,
		Content:     doc.Content,
		ContentType: ContentTypeCodeFile,
		Keywords:    doc.Symbols,
		Metadata:    map[string]string{"score": fmt.Sprintf("%.4f", score)},
	}
}

func (s *UniversalContentStore) searchDB(
	ctx context.Context,
	query string,
	filters *SearchFilters,
	limit int,
) ([]*ContentEntry, error) {
	db := s.vectorDB.DB()

	sqlQuery := `
		SELECT id, session_id, agent_id, agent_type, content_type, content,
		       token_count, timestamp, turn_number, keywords, entities,
		       parent_id, related_files, metadata
		FROM content_entries
		WHERE content LIKE ?
	`
	args := []any{"%" + query + "%"}

	sqlQuery, args = s.applyFilters(sqlQuery, args, filters)
	sqlQuery += " LIMIT ?"
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanEntries(rows)
}

func (s *UniversalContentStore) applyFilters(
	query string,
	args []any,
	filters *SearchFilters,
) (string, []any) {
	if filters == nil {
		return query, args
	}

	query, args = applySessionFilter(query, args, filters)
	query, args = applyAgentFilter(query, args, filters)
	query, args = applyTurnRangeFilter(query, args, filters)
	return query, args
}

func applySessionFilter(query string, args []any, filters *SearchFilters) (string, []any) {
	if filters.HasSessionFilter() {
		query += " AND session_id = ?"
		args = append(args, filters.SessionID)
	}
	return query, args
}

func applyAgentFilter(query string, args []any, filters *SearchFilters) (string, []any) {
	if filters.HasAgentFilter() {
		query += " AND agent_id = ?"
		args = append(args, filters.AgentID)
	}
	return query, args
}

func applyTurnRangeFilter(query string, args []any, filters *SearchFilters) (string, []any) {
	if filters.HasTurnRange() {
		query += " AND turn_number >= ? AND turn_number <= ?"
		args = append(args, filters.TurnRange[0], filters.TurnRange[1])
	}
	return query, args
}

func (s *UniversalContentStore) scanEntries(rows *sql.Rows) ([]*ContentEntry, error) {
	var entries []*ContentEntry
	for rows.Next() {
		entry, err := s.scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *UniversalContentStore) scanEntry(rows *sql.Rows) (*ContentEntry, error) {
	var entry ContentEntry
	var timestamp int64
	var contentType string
	var keywords, entities, relatedFiles, metadata sql.NullString
	var parentID sql.NullString

	err := rows.Scan(
		&entry.ID, &entry.SessionID, &entry.AgentID, &entry.AgentType,
		&contentType, &entry.Content, &entry.TokenCount,
		&timestamp, &entry.TurnNumber, &keywords, &entities,
		&parentID, &relatedFiles, &metadata,
	)
	if err != nil {
		return nil, err
	}

	entry.ContentType = ContentType(contentType)
	entry.Timestamp = time.Unix(timestamp, 0)
	entry.ParentID = parentID.String

	s.parseJSONFields(&entry, keywords, entities, relatedFiles, metadata)

	return &entry, nil
}

func (s *UniversalContentStore) parseJSONFields(
	entry *ContentEntry,
	keywords, entities, relatedFiles, metadata sql.NullString,
) {
	unmarshalIfValid(keywords, &entry.Keywords)
	unmarshalIfValid(entities, &entry.Entities)
	unmarshalIfValid(relatedFiles, &entry.RelatedFiles)
	unmarshalIfValid(metadata, &entry.Metadata)
}

func unmarshalIfValid[T any](ns sql.NullString, target *T) {
	if ns.Valid {
		_ = json.Unmarshal([]byte(ns.String), target)
	}
}

// fuseResults combines results using Reciprocal Rank Fusion (RRF).
func (s *UniversalContentStore) fuseResults(
	bleveResults, dbResults []*ContentEntry,
	limit int,
) []*ContentEntry {
	scoreMap := make(map[string]float64)
	entryMap := make(map[string]*ContentEntry)

	const k = 60.0 // RRF constant

	s.addRRFScores(bleveResults, scoreMap, entryMap, k)
	s.addRRFScores(dbResults, scoreMap, entryMap, k)

	return s.sortAndLimitByScore(entryMap, scoreMap, limit)
}

func (s *UniversalContentStore) addRRFScores(
	entries []*ContentEntry,
	scoreMap map[string]float64,
	entryMap map[string]*ContentEntry,
	k float64,
) {
	for rank, entry := range entries {
		scoreMap[entry.ID] += 1.0 / (k + float64(rank+1))
		if _, exists := entryMap[entry.ID]; !exists {
			entryMap[entry.ID] = entry
		}
	}
}

type scoredEntry struct {
	entry *ContentEntry
	score float64
}

func (s *UniversalContentStore) sortAndLimitByScore(
	entryMap map[string]*ContentEntry,
	scoreMap map[string]float64,
	limit int,
) []*ContentEntry {
	scored := make([]scoredEntry, 0, len(entryMap))
	for id, entry := range entryMap {
		scored = append(scored, scoredEntry{entry: entry, score: scoreMap[id]})
	}

	s.sortByScoreDesc(scored)

	if len(scored) > limit {
		scored = scored[:limit]
	}

	result := make([]*ContentEntry, len(scored))
	for i, se := range scored {
		result[i] = se.entry
	}
	return result
}

func (s *UniversalContentStore) sortByScoreDesc(scored []scoredEntry) {
	for i := 0; i < len(scored)-1; i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}
}

// GetByIDs retrieves entries by their IDs.
func (s *UniversalContentStore) GetByIDs(ids []string) ([]*ContentEntry, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrStoreClosed
	}
	s.mu.RUnlock()

	if len(ids) == 0 {
		return nil, nil
	}

	return s.queryEntriesByIDs(ids)
}

func (s *UniversalContentStore) queryEntriesByIDs(ids []string) ([]*ContentEntry, error) {
	db := s.vectorDB.DB()

	placeholders := ""
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = id
	}

	query := `
		SELECT id, session_id, agent_id, agent_type, content_type, content,
		       token_count, timestamp, turn_number, keywords, entities,
		       parent_id, related_files, metadata
		FROM content_entries
		WHERE id IN (` + placeholders + `)
	`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanEntries(rows)
}

// GetByTurnRange retrieves entries within a turn range for a session.
func (s *UniversalContentStore) GetByTurnRange(
	sessionID string,
	fromTurn, toTurn int,
) ([]*ContentEntry, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrStoreClosed
	}
	s.mu.RUnlock()

	return s.queryEntriesByTurnRange(sessionID, fromTurn, toTurn)
}

func (s *UniversalContentStore) queryEntriesByTurnRange(
	sessionID string,
	fromTurn, toTurn int,
) ([]*ContentEntry, error) {
	db := s.vectorDB.DB()

	query := `
		SELECT id, session_id, agent_id, agent_type, content_type, content,
		       token_count, timestamp, turn_number, keywords, entities,
		       parent_id, related_files, metadata
		FROM content_entries
		WHERE session_id = ? AND turn_number >= ? AND turn_number <= ?
		ORDER BY turn_number ASC
	`

	rows, err := db.Query(query, sessionID, fromTurn, toTurn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanEntries(rows)
}

// StoreReference stores a context reference for evicted content.
func (s *UniversalContentStore) StoreReference(ref *ContextReference) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStoreClosed
	}
	s.mu.RUnlock()

	return s.insertReference(ref)
}

func (s *UniversalContentStore) insertReference(ref *ContextReference) error {
	db := s.vectorDB.DB()

	contentIDs, _ := json.Marshal(ref.ContentIDs)
	topics, _ := json.Marshal(ref.Topics)
	entities, _ := json.Marshal(ref.Entities)
	queryHints, _ := json.Marshal(ref.QueryHints)

	_, err := db.Exec(`
		INSERT OR REPLACE INTO context_references
		(id, session_id, agent_id, content_ids, summary, tokens_saved,
		 turn_range_start, turn_range_end, topics, entities, query_hints,
		 ref_type, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ref.ID, ref.SessionID, ref.AgentID, string(contentIDs), ref.Summary,
		ref.TokensSaved, ref.TurnRange[0], ref.TurnRange[1],
		string(topics), string(entities), string(queryHints),
		string(ref.Type), ref.Timestamp.Unix())

	return err
}

// GetReference retrieves a context reference by ID.
func (s *UniversalContentStore) GetReference(refID string) (*ContextReference, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrStoreClosed
	}
	s.mu.RUnlock()

	return s.queryReference(refID)
}

func (s *UniversalContentStore) queryReference(refID string) (*ContextReference, error) {
	db := s.vectorDB.DB()

	row := db.QueryRow(`
		SELECT id, session_id, agent_id, content_ids, summary, tokens_saved,
		       turn_range_start, turn_range_end, topics, entities, query_hints,
		       ref_type, timestamp
		FROM context_references
		WHERE id = ?
	`, refID)

	return s.scanReference(row)
}

func (s *UniversalContentStore) scanReference(row *sql.Row) (*ContextReference, error) {
	var ref ContextReference
	var contentIDs, topics, entities, queryHints sql.NullString
	var refType string
	var timestamp int64
	var turnStart, turnEnd int

	err := row.Scan(
		&ref.ID, &ref.SessionID, &ref.AgentID, &contentIDs, &ref.Summary,
		&ref.TokensSaved, &turnStart, &turnEnd,
		&topics, &entities, &queryHints, &refType, &timestamp,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("reference not found: %s", ref.ID)
	}
	if err != nil {
		return nil, err
	}

	ref.Type = ReferenceType(refType)
	ref.TurnRange = [2]int{turnStart, turnEnd}
	ref.Timestamp = time.Unix(timestamp, 0)

	s.parseRefJSONFields(&ref, contentIDs, topics, entities, queryHints)

	return &ref, nil
}

func (s *UniversalContentStore) parseRefJSONFields(
	ref *ContextReference,
	contentIDs, topics, entities, queryHints sql.NullString,
) {
	unmarshalIfValid(contentIDs, &ref.ContentIDs)
	unmarshalIfValid(topics, &ref.Topics)
	unmarshalIfValid(entities, &ref.Entities)
	unmarshalIfValid(queryHints, &ref.QueryHints)
}

// Close shuts down the content store and releases resources.
func (s *UniversalContentStore) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.cancelFunc()
	close(s.indexQueue)
	s.wg.Wait()

	if err := s.bleveIndex.Close(); err != nil {
		return fmt.Errorf("close bleve: %w", err)
	}

	return nil
}

// IsClosed returns true if the store has been closed.
func (s *UniversalContentStore) IsClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// DocumentCount returns the number of indexed documents.
func (s *UniversalContentStore) DocumentCount() (uint64, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return 0, ErrStoreClosed
	}
	s.mu.RUnlock()

	return s.bleveIndex.DocumentCount()
}

// EntryCount returns the number of content entries in the database.
func (s *UniversalContentStore) EntryCount() (int64, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return 0, ErrStoreClosed
	}
	s.mu.RUnlock()

	var count int64
	err := s.vectorDB.DB().QueryRow("SELECT COUNT(*) FROM content_entries").Scan(&count)
	return count, err
}

// QueueLength returns the current length of the index queue.
func (s *UniversalContentStore) QueueLength() int {
	return len(s.indexQueue)
}

// BleveIndex returns the underlying Bleve index manager.
func (s *UniversalContentStore) BleveIndex() *bleve.IndexManager {
	return s.bleveIndex
}

// VectorDB returns the underlying VectorGraphDB.
func (s *UniversalContentStore) VectorDB() *vectorgraphdb.VectorGraphDB {
	return s.vectorDB
}
