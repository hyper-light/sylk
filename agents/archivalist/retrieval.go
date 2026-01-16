package archivalist

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RetrievalResult represents a single retrieved item
type RetrievalResult struct {
	ID       string         `json:"id"`
	Content  string         `json:"content"`
	Score    float64        `json:"score"`     // Combined relevance score
	Source   string         `json:"source"`    // "fts", "embedding", "exact"
	Category string         `json:"category"`
	Type     string         `json:"type"`      // "pattern", "failure", "file", etc.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// RetrievalOptions configures the retrieval
type RetrievalOptions struct {
	// FTS options
	FTSLimit int  // Max results from full-text search
	UseFTS   bool // Enable full-text search

	// Embedding options
	EmbeddingLimit int     // Max results from embedding search
	UseEmbeddings  bool    // Enable embedding search
	MinSimilarity  float64 // Minimum embedding similarity

	// Filtering
	Categories []string // Filter by categories
	SessionID  string   // Filter by session
	Types      []string // Filter by content types

	// Output
	TopK       int  // Final number of results to return
	UseReranker bool // Use re-ranking for precision
}

// DefaultRetrievalOptions returns sensible defaults
func DefaultRetrievalOptions() RetrievalOptions {
	return RetrievalOptions{
		FTSLimit:       50,
		UseFTS:         true,
		EmbeddingLimit: 50,
		UseEmbeddings:  true,
		MinSimilarity:  0.5,
		TopK:           10,
		UseReranker:    true,
	}
}

// SemanticRetriever provides multi-source retrieval for RAG
type SemanticRetriever struct {
	// SQLite with FTS5 for keyword search
	db *sql.DB

	// Embedding store for semantic search
	embeddings *EmbeddingStore

	// Embedder for generating query embeddings
	embedder Embedder

	// Agent context for in-memory data
	agentContext *AgentContext
}

// SemanticRetrieverConfig configures the retriever
type SemanticRetrieverConfig struct {
	DBPath       string
	Embeddings   *EmbeddingStore
	Embedder     Embedder
	AgentContext *AgentContext
}

// NewSemanticRetriever creates a new semantic retriever
func NewSemanticRetriever(cfg SemanticRetrieverConfig) (*SemanticRetriever, error) {
	var db *sql.DB
	var err error

	if cfg.DBPath != "" {
		db, err = sql.Open("sqlite", cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open database: %w", err)
		}

		// Enable FTS5
		if err := initFTS(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to initialize FTS: %w", err)
		}
	}

	return &SemanticRetriever{
		db:           db,
		embeddings:   cfg.Embeddings,
		embedder:     cfg.Embedder,
		agentContext: cfg.AgentContext,
	}, nil
}

// initFTS initializes FTS5 tables
func initFTS(db *sql.DB) error {
	_, err := db.Exec(`
		-- Main content table
		CREATE TABLE IF NOT EXISTS retrieval_content (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			category TEXT,
			type TEXT,
			session_id TEXT,
			metadata TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		-- FTS5 virtual table for full-text search
		CREATE VIRTUAL TABLE IF NOT EXISTS content_fts USING fts5(
			content,
			category,
			type,
			content='retrieval_content',
			content_rowid='rowid'
		);

		-- Triggers to keep FTS in sync
		CREATE TRIGGER IF NOT EXISTS content_ai AFTER INSERT ON retrieval_content BEGIN
			INSERT INTO content_fts(rowid, content, category, type)
			VALUES (new.rowid, new.content, new.category, new.type);
		END;

		CREATE TRIGGER IF NOT EXISTS content_ad AFTER DELETE ON retrieval_content BEGIN
			INSERT INTO content_fts(content_fts, rowid, content, category, type)
			VALUES ('delete', old.rowid, old.content, old.category, old.type);
		END;

		CREATE TRIGGER IF NOT EXISTS content_au AFTER UPDATE ON retrieval_content BEGIN
			INSERT INTO content_fts(content_fts, rowid, content, category, type)
			VALUES ('delete', old.rowid, old.content, old.category, old.type);
			INSERT INTO content_fts(rowid, content, category, type)
			VALUES (new.rowid, new.content, new.category, new.type);
		END;

		-- Indexes
		CREATE INDEX IF NOT EXISTS idx_content_category ON retrieval_content(category);
		CREATE INDEX IF NOT EXISTS idx_content_type ON retrieval_content(type);
		CREATE INDEX IF NOT EXISTS idx_content_session ON retrieval_content(session_id);
	`)
	return err
}

// Close closes the retriever
func (sr *SemanticRetriever) Close() error {
	if sr.db != nil {
		return sr.db.Close()
	}
	return nil
}

// Retrieve performs multi-source retrieval
func (sr *SemanticRetriever) Retrieve(ctx context.Context, query string, opts RetrievalOptions) ([]*RetrievalResult, error) {
	results := sr.gatherResults(ctx, query, opts)
	results, err := sr.applyFTSSearch(ctx, results, query, opts)
	if err != nil {
		return nil, err
	}
	results, err = sr.applyEmbeddingSearch(ctx, results, query, opts)
	if err != nil {
		return nil, err
	}
	results = deduplicateResults(results)
	return sr.finalizeResults(query, results, opts), nil
}

func (sr *SemanticRetriever) gatherResults(ctx context.Context, query string, opts RetrievalOptions) []*RetrievalResult {
	if sr.agentContext == nil {
		return nil
	}
	return sr.searchAgentContext(query, opts)
}

func (sr *SemanticRetriever) applyFTSSearch(ctx context.Context, results []*RetrievalResult, query string, opts RetrievalOptions) ([]*RetrievalResult, error) {
	if !opts.UseFTS || sr.db == nil {
		return results, nil
	}
	ftsResults, err := sr.searchFTS(ctx, query, opts)
	if err != nil {
		return nil, fmt.Errorf("FTS search failed: %w", err)
	}
	return append(results, ftsResults...), nil
}

func (sr *SemanticRetriever) applyEmbeddingSearch(ctx context.Context, results []*RetrievalResult, query string, opts RetrievalOptions) ([]*RetrievalResult, error) {
	if !opts.UseEmbeddings || sr.embeddings == nil || sr.embedder == nil {
		return results, nil
	}
	embResults, err := sr.searchEmbeddings(ctx, query, opts)
	if err != nil {
		return nil, fmt.Errorf("embedding search failed: %w", err)
	}
	return append(results, embResults...), nil
}

func (sr *SemanticRetriever) finalizeResults(query string, results []*RetrievalResult, opts RetrievalOptions) []*RetrievalResult {
	if opts.UseReranker && len(results) > opts.TopK {
		return sr.rerank(query, results, opts.TopK)
	}
	if len(results) > opts.TopK {
		sort.Slice(results, func(i, j int) bool {
			return results[i].Score > results[j].Score
		})
		return results[:opts.TopK]
	}
	return results
}

// searchAgentContext searches the in-memory agent context
func (sr *SemanticRetriever) searchAgentContext(query string, opts RetrievalOptions) []*RetrievalResult {
	queryLower := strings.ToLower(query)
	var results []*RetrievalResult

	results = append(results, sr.searchPatterns(queryLower, opts)...)
	results = append(results, sr.searchFailures(queryLower, opts)...)
	results = append(results, sr.searchFiles(queryLower, opts)...)

	return results
}

func (sr *SemanticRetriever) searchPatterns(query string, opts RetrievalOptions) []*RetrievalResult {
	if !containsType(opts.Types, "pattern") && len(opts.Types) > 0 {
		return nil
	}
	var results []*RetrievalResult
	for _, pattern := range sr.agentContext.GetAllPatterns() {
		if result := sr.matchPattern(query, pattern); result != nil {
			results = append(results, result)
		}
	}
	return results
}

func (sr *SemanticRetriever) matchPattern(query string, pattern *Pattern) *RetrievalResult {
	score := sr.textMatchScore(query, pattern.Description, pattern.Name, pattern.Category)
	if score <= 0.3 {
		return nil
	}
	return &RetrievalResult{
		ID: pattern.ID, Content: formatPatternContent(pattern),
		Score: score, Source: "memory", Category: pattern.Category, Type: "pattern",
	}
}

func (sr *SemanticRetriever) searchFailures(query string, opts RetrievalOptions) []*RetrievalResult {
	if !containsType(opts.Types, "failure") && len(opts.Types) > 0 {
		return nil
	}
	var results []*RetrievalResult
	for _, failure := range sr.agentContext.GetRecentFailures(50) {
		if result := sr.matchFailure(query, failure); result != nil {
			results = append(results, result)
		}
	}
	return results
}

func (sr *SemanticRetriever) matchFailure(query string, failure *Failure) *RetrievalResult {
	score := sr.textMatchScore(query, failure.Approach, failure.Reason, failure.Resolution)
	if score <= 0.3 {
		return nil
	}
	return &RetrievalResult{
		ID: failure.ID, Content: formatFailureContent(failure),
		Score: score, Source: "memory", Category: "failure", Type: "failure",
	}
}

func (sr *SemanticRetriever) searchFiles(query string, opts RetrievalOptions) []*RetrievalResult {
	if !containsType(opts.Types, "file") && len(opts.Types) > 0 {
		return nil
	}
	var results []*RetrievalResult
	for _, file := range sr.agentContext.GetModifiedFiles() {
		if result := sr.matchFile(query, file); result != nil {
			results = append(results, result)
		}
	}
	return results
}

func (sr *SemanticRetriever) matchFile(query string, file *FileState) *RetrievalResult {
	score := sr.textMatchScore(query, file.Path, file.Summary)
	if score <= 0.3 {
		return nil
	}
	return &RetrievalResult{
		ID: file.Path, Content: formatFileContent(file),
		Score: score, Source: "memory", Category: "file", Type: "file",
	}
}

// ftsQueryBuilder constructs FTS5 queries
type ftsQueryBuilder struct {
	sql  string
	args []any
}

func newFTSQueryBuilder(ftsQuery string) *ftsQueryBuilder {
	return &ftsQueryBuilder{
		sql: `SELECT rc.id, rc.content, rc.category, rc.type, rc.metadata, bm25(content_fts) as score
			FROM content_fts JOIN retrieval_content rc ON content_fts.rowid = rc.rowid
			WHERE content_fts MATCH ?`,
		args: []any{ftsQuery},
	}
}

func (b *ftsQueryBuilder) addCategoryFilter(categories []string) {
	if len(categories) == 0 {
		return
	}
	b.addInFilter("rc.category", categories)
}

func (b *ftsQueryBuilder) addTypeFilter(types []string) {
	if len(types) == 0 {
		return
	}
	b.addInFilter("rc.type", types)
}

func (b *ftsQueryBuilder) addInFilter(column string, values []string) {
	placeholders := make([]string, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		b.args = append(b.args, v)
	}
	b.sql += fmt.Sprintf(" AND %s IN (%s)", column, strings.Join(placeholders, ","))
}

func (b *ftsQueryBuilder) addSessionFilter(sessionID string) {
	if sessionID == "" {
		return
	}
	b.sql += " AND rc.session_id = ?"
	b.args = append(b.args, sessionID)
}

func (b *ftsQueryBuilder) finalize(limit int) (string, []any) {
	b.sql += " ORDER BY score LIMIT ?"
	b.args = append(b.args, limit)
	return b.sql, b.args
}

// searchFTS performs FTS5 search
func (sr *SemanticRetriever) searchFTS(ctx context.Context, query string, opts RetrievalOptions) ([]*RetrievalResult, error) {
	if sr.db == nil {
		return nil, nil
	}

	builder := newFTSQueryBuilder(buildFTSQuery(query))
	builder.addCategoryFilter(opts.Categories)
	builder.addTypeFilter(opts.Types)
	builder.addSessionFilter(opts.SessionID)
	sqlQuery, args := builder.finalize(opts.FTSLimit)

	return sr.executeFTSQuery(ctx, sqlQuery, args)
}

func (sr *SemanticRetriever) executeFTSQuery(ctx context.Context, query string, args []any) ([]*RetrievalResult, error) {
	rows, err := sr.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*RetrievalResult
	for rows.Next() {
		if r, err := sr.scanFTSRow(rows); err != nil {
			return nil, err
		} else {
			results = append(results, r)
		}
	}
	return results, nil
}

func (sr *SemanticRetriever) scanFTSRow(rows *sql.Rows) (*RetrievalResult, error) {
	r := &RetrievalResult{Source: "fts"}
	var metadataJSON sql.NullString
	if err := rows.Scan(&r.ID, &r.Content, &r.Category, &r.Type, &metadataJSON, &r.Score); err != nil {
		return nil, err
	}
	if metadataJSON.Valid {
		json.Unmarshal([]byte(metadataJSON.String), &r.Metadata)
	}
	r.Score = 1.0 / (1.0 - r.Score)
	return r, nil
}

// searchEmbeddings performs semantic search via embeddings
func (sr *SemanticRetriever) searchEmbeddings(ctx context.Context, query string, opts RetrievalOptions) ([]*RetrievalResult, error) {
	// Embed the query
	queryEmbed, err := sr.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	// Search embeddings
	searchOpts := SearchOptions{
		Limit:         opts.EmbeddingLimit,
		MinSimilarity: opts.MinSimilarity,
	}
	if opts.SessionID != "" {
		searchOpts.SessionID = opts.SessionID
	}
	if len(opts.Categories) == 1 {
		searchOpts.Category = opts.Categories[0]
	}

	scored, err := sr.embeddings.SearchSimilar(queryEmbed, searchOpts)
	if err != nil {
		return nil, err
	}

	// Convert to results
	results := make([]*RetrievalResult, 0, len(scored))
	for _, s := range scored {
		results = append(results, &RetrievalResult{
			ID:       s.Entry.ID,
			Content:  s.Entry.Text,
			Score:    s.Score,
			Source:   "embedding",
			Category: s.Entry.Category,
			Type:     getTypeFromCategory(s.Entry.Category),
			Metadata: s.Entry.Metadata,
		})
	}

	return results, nil
}

// rerank re-ranks results for precision using simple heuristics
func (sr *SemanticRetriever) rerank(query string, results []*RetrievalResult, topK int) []*RetrievalResult {
	queryLower := strings.ToLower(query)
	queryTerms := strings.Fields(queryLower)

	// Score each result
	type scoredResult struct {
		result *RetrievalResult
		score  float64
	}
	scored := make([]scoredResult, len(results))

	for i, r := range results {
		// Base score from retrieval
		score := r.Score

		// Boost for exact query term matches
		contentLower := strings.ToLower(r.Content)
		for _, term := range queryTerms {
			if strings.Contains(contentLower, term) {
				score += 0.1
			}
		}

		// Boost for category match if query mentions it
		if strings.Contains(queryLower, strings.ToLower(r.Category)) {
			score += 0.2
		}

		// Boost for source diversity (embedding matches often more relevant)
		if r.Source == "embedding" {
			score += 0.05
		}

		scored[i] = scoredResult{r, score}
	}

	// Sort by score
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take top K
	if len(scored) > topK {
		scored = scored[:topK]
	}

	// Extract results
	reranked := make([]*RetrievalResult, len(scored))
	for i, s := range scored {
		s.result.Score = s.score
		reranked[i] = s.result
	}

	return reranked
}

// textMatchScore computes a simple text match score
func (sr *SemanticRetriever) textMatchScore(query string, texts ...string) float64 {
	queryTerms := strings.Fields(query)
	if len(queryTerms) == 0 {
		return 0
	}

	matches := 0
	for _, text := range texts {
		textLower := strings.ToLower(text)
		for _, term := range queryTerms {
			if strings.Contains(textLower, term) {
				matches++
			}
		}
	}

	return float64(matches) / float64(len(queryTerms)*len(texts))
}

// Index adds content to the retrieval index
func (sr *SemanticRetriever) Index(ctx context.Context, id, content, category, contentType, sessionID string, metadata map[string]any) error {
	// Add to FTS
	if sr.db != nil {
		metaJSON, _ := json.Marshal(metadata)
		_, err := sr.db.ExecContext(ctx, `
			INSERT OR REPLACE INTO retrieval_content (id, content, category, type, session_id, metadata)
			VALUES (?, ?, ?, ?, ?, ?)
		`, id, content, category, contentType, sessionID, metaJSON)
		if err != nil {
			return fmt.Errorf("failed to index in FTS: %w", err)
		}
	}

	// Add to embeddings
	if sr.embeddings != nil && sr.embedder != nil {
		embedding, err := sr.embedder.Embed(ctx, content)
		if err != nil {
			return fmt.Errorf("failed to generate embedding: %w", err)
		}

		err = sr.embeddings.Store(&EmbeddingEntry{
			ID:        id,
			Text:      content,
			Category:  category,
			SessionID: sessionID,
			Metadata:  metadata,
			CreatedAt: time.Now(),
		}, embedding)
		if err != nil {
			return fmt.Errorf("failed to store embedding: %w", err)
		}
	}

	return nil
}

// Delete removes content from the index
func (sr *SemanticRetriever) Delete(ctx context.Context, id string) error {
	if sr.db != nil {
		_, err := sr.db.ExecContext(ctx, "DELETE FROM retrieval_content WHERE id = ?", id)
		if err != nil {
			return err
		}
	}
	return nil
}

// Helper functions

func buildFTSQuery(query string) string {
	// Tokenize and build OR query with prefix matching
	tokens := strings.Fields(query)
	var parts []string
	for _, t := range tokens {
		// Escape special characters
		t = strings.ReplaceAll(t, "\"", "")
		t = strings.ReplaceAll(t, "'", "")
		if len(t) > 0 {
			parts = append(parts, fmt.Sprintf("%s*", t)) // Prefix matching
		}
	}
	return strings.Join(parts, " OR ")
}

func deduplicateResults(results []*RetrievalResult) []*RetrievalResult {
	seen := make(map[string]bool)
	deduplicated := make([]*RetrievalResult, 0, len(results))

	for _, r := range results {
		if !seen[r.ID] {
			seen[r.ID] = true
			deduplicated = append(deduplicated, r)
		}
	}

	return deduplicated
}

func containsType(types []string, t string) bool {
	for _, typ := range types {
		if typ == t {
			return true
		}
	}
	return false
}

func getTypeFromCategory(category string) string {
	if strings.HasPrefix(category, "pattern") || strings.Contains(category, ".") {
		return "pattern"
	}
	if strings.HasPrefix(category, "failure") {
		return "failure"
	}
	if strings.HasPrefix(category, "file") {
		return "file"
	}
	return "general"
}

func formatPatternContent(p *Pattern) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Pattern: %s\n", p.Name))
	sb.WriteString(fmt.Sprintf("Category: %s\n", p.Category))
	sb.WriteString(fmt.Sprintf("Description: %s\n", p.Description))
	if p.Example != "" {
		sb.WriteString(fmt.Sprintf("Example: %s\n", p.Example))
	}
	return sb.String()
}

func formatFailureContent(f *Failure) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Failed Approach: %s\n", f.Approach))
	sb.WriteString(fmt.Sprintf("Reason: %s\n", f.Reason))
	if f.Context != "" {
		sb.WriteString(fmt.Sprintf("Context: %s\n", f.Context))
	}
	if f.Resolution != "" {
		sb.WriteString(fmt.Sprintf("Resolution: %s\n", f.Resolution))
	}
	return sb.String()
}

func formatFileContent(f *FileState) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("File: %s\n", f.Path))
	sb.WriteString(fmt.Sprintf("Status: %s\n", f.Status))
	if f.Summary != "" {
		sb.WriteString(fmt.Sprintf("Summary: %s\n", f.Summary))
	}
	return sb.String()
}
