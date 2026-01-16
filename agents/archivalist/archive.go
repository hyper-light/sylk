package archivalist

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// DefaultArchivePath is the default location for the SQLite database
	DefaultArchivePath = ".sylk/archive.db"

	// Schema version for migrations
	schemaVersion = 2
)

// Archive provides SQLite-based persistent storage for chronicle entries
type Archive struct {
	db   *sql.DB
	path string
}

// ArchiveConfig configures the archive storage
type ArchiveConfig struct {
	Path string // Path to SQLite database file
}

// NewArchive creates a new archive storage instance
func NewArchive(cfg ArchiveConfig) (*Archive, error) {
	path := cfg.Path
	if path == "" {
		path = DefaultArchivePath
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create archive directory: %w", err)
	}

	// Open database
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open archive database: %w", err)
	}

	// Enable WAL mode for better concurrent performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	archive := &Archive{
		db:   db,
		path: path,
	}

	// Initialize schema
	if err := archive.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return archive, nil
}

// initSchema creates the database schema if it doesn't exist
func (a *Archive) initSchema() error {
	schema := `
	-- Schema version tracking
	CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY
	);

	-- Sessions table
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		started_at TIMESTAMP NOT NULL,
		ended_at TIMESTAMP,
		summary TEXT,
		primary_focus TEXT,
		entry_count INTEGER DEFAULT 0
	);

	-- Main entries table
	CREATE TABLE IF NOT EXISTS entries (
		id TEXT PRIMARY KEY,
		category TEXT NOT NULL,
		title TEXT,
		content TEXT NOT NULL,
		source TEXT NOT NULL,
		session_id TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL,
		archived_at TIMESTAMP,
		tokens_estimate INTEGER DEFAULT 0,
		metadata JSON,
		related_ids JSON,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	-- Full-text search index
	CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
		id,
		title,
		content,
		category,
		content='entries',
		content_rowid='rowid'
	);

	-- Triggers to keep FTS index in sync
	CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
		INSERT INTO entries_fts(id, title, content, category)
		VALUES (new.id, new.title, new.content, new.category);
	END;

	CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
		INSERT INTO entries_fts(entries_fts, id, title, content, category)
		VALUES ('delete', old.id, old.title, old.content, old.category);
	END;

	CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE ON entries BEGIN
		INSERT INTO entries_fts(entries_fts, id, title, content, category)
		VALUES ('delete', old.id, old.title, old.content, old.category);
		INSERT INTO entries_fts(id, title, content, category)
		VALUES (new.id, new.title, new.content, new.category);
	END;

	-- Entry links table for relationships
	CREATE TABLE IF NOT EXISTS entry_links (
		from_id TEXT NOT NULL,
		to_id TEXT NOT NULL,
		relationship TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (from_id, to_id, relationship),
		FOREIGN KEY (from_id) REFERENCES entries(id),
		FOREIGN KEY (to_id) REFERENCES entries(id)
	);

	-- Indexes for efficient querying
	CREATE INDEX IF NOT EXISTS idx_entries_category ON entries(category);
	CREATE INDEX IF NOT EXISTS idx_entries_session ON entries(session_id);
	CREATE INDEX IF NOT EXISTS idx_entries_source ON entries(source);
	CREATE INDEX IF NOT EXISTS idx_entries_created ON entries(created_at);
	CREATE INDEX IF NOT EXISTS idx_entries_archived ON entries(archived_at);

	-- ==========================================================================
	-- Facts tables - structured extraction from entries
	-- ==========================================================================

	-- Extracted decisions
	CREATE TABLE IF NOT EXISTS facts_decisions (
		id TEXT PRIMARY KEY,
		choice TEXT NOT NULL,
		rationale TEXT,
		context TEXT,
		alternatives JSON,
		confidence REAL DEFAULT 1.0,
		source_entry_ids JSON NOT NULL,
		session_id TEXT NOT NULL,
		extracted_at TIMESTAMP NOT NULL,
		superseded_by TEXT,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	-- Extracted patterns
	CREATE TABLE IF NOT EXISTS facts_patterns (
		id TEXT PRIMARY KEY,
		category TEXT NOT NULL,
		name TEXT NOT NULL,
		pattern TEXT NOT NULL,
		example TEXT,
		rationale TEXT,
		usage_count INTEGER DEFAULT 1,
		source_entry_ids JSON NOT NULL,
		session_id TEXT NOT NULL,
		extracted_at TIMESTAMP NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	-- Extracted failures/learnings
	CREATE TABLE IF NOT EXISTS facts_failures (
		id TEXT PRIMARY KEY,
		approach TEXT NOT NULL,
		reason TEXT NOT NULL,
		context TEXT,
		resolution TEXT,
		resolution_entry_id TEXT,
		source_entry_ids JSON NOT NULL,
		session_id TEXT NOT NULL,
		extracted_at TIMESTAMP NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	-- Extracted file changes
	CREATE TABLE IF NOT EXISTS facts_file_changes (
		id TEXT PRIMARY KEY,
		path TEXT NOT NULL,
		change_type TEXT NOT NULL,
		description TEXT,
		line_start INTEGER,
		line_end INTEGER,
		source_entry_ids JSON NOT NULL,
		session_id TEXT NOT NULL,
		extracted_at TIMESTAMP NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	-- Indexes for facts tables
	CREATE INDEX IF NOT EXISTS idx_facts_decisions_session ON facts_decisions(session_id);
	CREATE INDEX IF NOT EXISTS idx_facts_patterns_category ON facts_patterns(category);
	CREATE INDEX IF NOT EXISTS idx_facts_patterns_session ON facts_patterns(session_id);
	CREATE INDEX IF NOT EXISTS idx_facts_failures_session ON facts_failures(session_id);
	CREATE INDEX IF NOT EXISTS idx_facts_file_changes_path ON facts_file_changes(path);
	CREATE INDEX IF NOT EXISTS idx_facts_file_changes_session ON facts_file_changes(session_id);

	-- ==========================================================================
	-- Summaries table - hierarchical compression of entries
	-- ==========================================================================

	CREATE TABLE IF NOT EXISTS summaries (
		id TEXT PRIMARY KEY,
		level TEXT NOT NULL,
		scope TEXT NOT NULL,
		scope_id TEXT,
		content TEXT NOT NULL,
		key_points JSON,
		tokens_estimate INTEGER DEFAULT 0,
		source_entry_ids JSON NOT NULL,
		source_summary_ids JSON,
		session_id TEXT,
		time_start TIMESTAMP,
		time_end TIMESTAMP,
		created_at TIMESTAMP NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	-- Full-text search on summaries
	CREATE VIRTUAL TABLE IF NOT EXISTS summaries_fts USING fts5(
		id,
		content,
		scope,
		level,
		content='summaries',
		content_rowid='rowid'
	);

	-- Triggers to keep summaries FTS in sync
	CREATE TRIGGER IF NOT EXISTS summaries_ai AFTER INSERT ON summaries BEGIN
		INSERT INTO summaries_fts(id, content, scope, level)
		VALUES (new.id, new.content, new.scope, new.level);
	END;

	CREATE TRIGGER IF NOT EXISTS summaries_ad AFTER DELETE ON summaries BEGIN
		INSERT INTO summaries_fts(summaries_fts, id, content, scope, level)
		VALUES ('delete', old.id, old.content, old.scope, old.level);
	END;

	CREATE TRIGGER IF NOT EXISTS summaries_au AFTER UPDATE ON summaries BEGIN
		INSERT INTO summaries_fts(summaries_fts, id, content, scope, level)
		VALUES ('delete', old.id, old.content, old.scope, old.level);
		INSERT INTO summaries_fts(id, content, scope, level)
		VALUES (new.id, new.content, new.scope, new.level);
	END;

	-- Indexes for summaries
	CREATE INDEX IF NOT EXISTS idx_summaries_level ON summaries(level);
	CREATE INDEX IF NOT EXISTS idx_summaries_scope ON summaries(scope);
	CREATE INDEX IF NOT EXISTS idx_summaries_session ON summaries(session_id);
	CREATE INDEX IF NOT EXISTS idx_summaries_time ON summaries(time_start, time_end);
	`

	_, err := a.db.Exec(schema)
	return err
}

// Close closes the archive database connection
func (a *Archive) Close() error {
	return a.db.Close()
}

// ArchiveEntry stores an entry in the archive
func (a *Archive) ArchiveEntry(entry *Entry) error {
	now := time.Now()
	if entry.ArchivedAt == nil {
		entry.ArchivedAt = &now
	}

	metadata, err := json.Marshal(entry.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	relatedIDs, err := json.Marshal(entry.RelatedIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal related IDs: %w", err)
	}

	_, err = a.db.Exec(`
		INSERT OR REPLACE INTO entries
		(id, category, title, content, source, session_id, created_at, updated_at, archived_at, tokens_estimate, metadata, related_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		entry.ID, entry.Category, entry.Title, entry.Content, entry.Source,
		entry.SessionID, entry.CreatedAt, entry.UpdatedAt, entry.ArchivedAt,
		entry.TokensEstimate, metadata, relatedIDs,
	)

	return err
}

// ArchiveEntries stores multiple entries in a transaction
func (a *Archive) ArchiveEntries(entries []*Entry) error {
	tx, stmt, err := a.prepareArchiveEntriesTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	defer stmt.Close()

	now := time.Now()
	for _, entry := range entries {
		if err := a.archiveEntry(stmt, entry, now); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (a *Archive) prepareArchiveEntriesTx() (*sql.Tx, *sql.Stmt, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO entries
		(id, category, title, content, source, session_id, created_at, updated_at, archived_at, tokens_estimate, metadata, related_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	return tx, stmt, nil
}

func (a *Archive) archiveEntry(stmt *sql.Stmt, entry *Entry, now time.Time) error {
	if entry.ArchivedAt == nil {
		entry.ArchivedAt = &now
	}

	metadata, relatedIDs, err := marshalEntryFields(entry)
	if err != nil {
		return err
	}

	_, err = stmt.Exec(
		entry.ID, entry.Category, entry.Title, entry.Content, entry.Source,
		entry.SessionID, entry.CreatedAt, entry.UpdatedAt, entry.ArchivedAt,
		entry.TokensEstimate, metadata, relatedIDs,
	)
	if err != nil {
		return fmt.Errorf("failed to archive entry %s: %w", entry.ID, err)
	}
	return nil
}

func marshalEntryFields(entry *Entry) ([]byte, []byte, error) {
	metadata, err := json.Marshal(entry.Metadata)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	relatedIDs, err := json.Marshal(entry.RelatedIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal related IDs: %w", err)
	}

	return metadata, relatedIDs, nil
}

// SaveSession saves or updates a session record
func (a *Archive) SaveSession(session *Session) error {
	_, err := a.db.Exec(`
		INSERT OR REPLACE INTO sessions (id, started_at, ended_at, summary, primary_focus, entry_count)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		session.ID, session.StartedAt, session.EndedAt, session.Summary,
		session.PrimaryFocus, session.EntryCount,
	)
	return err
}

// queryBuilder helps construct SQL queries with conditions
type queryBuilder struct {
	conditions []string
	args       []interface{}
}

func (qb *queryBuilder) addInFilter(column string, values []string) {
	if len(values) == 0 {
		return
	}
	placeholders := make([]string, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		qb.args = append(qb.args, v)
	}
	qb.conditions = append(qb.conditions, fmt.Sprintf("%s IN (%s)", column, joinStrings(placeholders, ",")))
}

func (qb *queryBuilder) addDateFilter(column string, value *time.Time, op string) {
	if value == nil {
		return
	}
	qb.conditions = append(qb.conditions, fmt.Sprintf("%s %s ?", column, op))
	qb.args = append(qb.args, value)
}

// Query searches the archive based on the query parameters
func (a *Archive) Query(q ArchiveQuery) ([]*Entry, error) {
	qb := &queryBuilder{}

	qb.addInFilter("category", categoriesToStrings(q.Categories))
	qb.addInFilter("source", sourcesToStrings(q.Sources))
	qb.addInFilter("session_id", q.SessionIDs)
	qb.addInFilter("id", q.IDs)
	qb.addDateFilter("created_at", q.Since, ">=")
	qb.addDateFilter("created_at", q.Until, "<=")

	query := a.buildSelectQuery(qb.conditions, q.Limit)
	return a.executeQuery(query, qb.args)
}

func (a *Archive) buildSelectQuery(conditions []string, limit int) string {
	query := baseEntrySelectQuery()
	query = addEntryConditions(query, conditions)
	query = addEntryOrder(query)
	query = addEntryLimit(query, limit)
	return query
}

func baseEntrySelectQuery() string {
	return "SELECT id, category, title, content, source, session_id, created_at, updated_at, archived_at, tokens_estimate, metadata, related_ids FROM entries WHERE 1=1"
}

func addEntryConditions(query string, conditions []string) string {
	for _, condition := range conditions {
		query += " AND " + condition
	}
	return query
}

func addEntryOrder(query string) string {
	return query + " ORDER BY created_at DESC"
}

func addEntryLimit(query string, limit int) string {
	if limit <= 0 {
		return query
	}
	return query + fmt.Sprintf(" LIMIT %d", limit)
}

func (a *Archive) executeQuery(query string, args []interface{}) ([]*Entry, error) {
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query entries: %w", err)
	}
	defer rows.Close()

	entries, err := scanEntryRows(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to scan entry: %w", err)
	}
	return entries, nil
}

// SearchText performs full-text search on archived entries
func (a *Archive) SearchText(searchText string, limit int) ([]*Entry, error) {
	query := entrySearchQuery(limit)
	rows, err := a.db.Query(query, searchText)
	if err != nil {
		return nil, fmt.Errorf("failed to search entries: %w", err)
	}
	defer rows.Close()

	entries, err := scanEntryRows(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to scan entry: %w", err)
	}
	return entries, nil
}

func entrySearchQuery(limit int) string {
	query := `
		SELECT e.id, e.category, e.title, e.content, e.source, e.session_id,
		       e.created_at, e.updated_at, e.archived_at, e.tokens_estimate, e.metadata, e.related_ids
		FROM entries e
		JOIN entries_fts fts ON e.id = fts.id
		WHERE entries_fts MATCH ?
		ORDER BY rank
	`
	if limit <= 0 {
		return query
	}
	return query + fmt.Sprintf(" LIMIT %d", limit)
}

func scanEntryRows(rows *sql.Rows) ([]*Entry, error) {
	var entries []*Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func categoriesToStrings(cats []Category) []string {
	result := make([]string, len(cats))
	for i, c := range cats {
		result[i] = string(c)
	}
	return result
}

func sourcesToStrings(sources []SourceModel) []string {
	result := make([]string, len(sources))
	for i, s := range sources {
		result[i] = string(s)
	}
	return result
}

// GetEntry retrieves a single entry by ID
func (a *Archive) GetEntry(id string) (*Entry, error) {
	row := a.db.QueryRow(`
		SELECT id, category, title, content, source, session_id, created_at, updated_at,
		       archived_at, tokens_estimate, metadata, related_ids
		FROM entries WHERE id = ?
	`, id)

	return scanEntryRow(row)
}

// GetSession retrieves a session by ID
func (a *Archive) GetSession(id string) (*Session, error) {
	row := a.db.QueryRow(`
		SELECT id, started_at, ended_at, summary, primary_focus, entry_count
		FROM sessions WHERE id = ?
	`, id)

	session, data, err := scanSessionRow(row)
	if err != nil {
		return nil, err
	}
	applySessionRow(session, data)
	return session, nil
}

// GetRecentSessions retrieves the most recent sessions
func (a *Archive) GetRecentSessions(limit int) ([]*Session, error) {
	query := recentSessionsQuery(limit)
	rows, err := a.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		session, data, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		applySessionRow(session, data)
		sessions = append(sessions, session)
	}

	return sessions, rows.Err()
}

func recentSessionsQuery(limit int) string {
	query := `
		SELECT id, started_at, ended_at, summary, primary_focus, entry_count
		FROM sessions ORDER BY started_at DESC
	`
	if limit <= 0 {
		return query
	}
	return query + fmt.Sprintf(" LIMIT %d", limit)
}

type sessionRowData struct {
	endedAt      sql.NullTime
	summary      sql.NullString
	primaryFocus sql.NullString
}

func scanSessionRow(scanner interface {
	Scan(dest ...interface{}) error
}) (*Session, sessionRowData, error) {
	var session Session
	var data sessionRowData

	err := scanner.Scan(&session.ID, &session.StartedAt, &data.endedAt, &data.summary, &data.primaryFocus, &session.EntryCount)
	if err != nil {
		return nil, sessionRowData{}, err
	}
	return &session, data, nil
}

func applySessionRow(session *Session, data sessionRowData) {
	if data.endedAt.Valid {
		session.EndedAt = &data.endedAt.Time
	}
	if data.summary.Valid {
		session.Summary = data.summary.String
	}
	if data.primaryFocus.Valid {
		session.PrimaryFocus = data.primaryFocus.String
	}
}

// SaveLink saves a relationship between entries
func (a *Archive) SaveLink(link EntryLink) error {
	_, err := a.db.Exec(`
		INSERT OR IGNORE INTO entry_links (from_id, to_id, relationship)
		VALUES (?, ?, ?)
	`, link.FromID, link.ToID, link.Relationship)
	return err
}

// GetRelatedEntries retrieves entries related to the given entry ID
func (a *Archive) GetRelatedEntries(entryID string) ([]*Entry, error) {
	rows, err := a.db.Query(`
		SELECT e.id, e.category, e.title, e.content, e.source, e.session_id,
		       e.created_at, e.updated_at, e.archived_at, e.tokens_estimate, e.metadata, e.related_ids
		FROM entries e
		JOIN entry_links l ON (e.id = l.to_id OR e.id = l.from_id)
		WHERE (l.from_id = ? OR l.to_id = ?) AND e.id != ?
	`, entryID, entryID, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

// Stats returns statistics about the archive
func (a *Archive) Stats() (map[string]int, error) {
	stats := make(map[string]int)
	if err := a.addTotalEntries(stats); err != nil {
		return nil, err
	}
	if err := a.addTotalSessions(stats); err != nil {
		return nil, err
	}
	if err := a.addCategoryCounts(stats); err != nil {
		return nil, err
	}
	return stats, nil
}

func (a *Archive) addTotalEntries(stats map[string]int) error {
	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM entries").Scan(&total); err != nil {
		return err
	}
	stats["total_entries"] = total
	return nil
}

func (a *Archive) addTotalSessions(stats map[string]int) error {
	var sessions int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		return err
	}
	stats["total_sessions"] = sessions
	return nil
}

func (a *Archive) addCategoryCounts(stats map[string]int) error {
	rows, err := a.db.Query("SELECT category, COUNT(*) FROM entries GROUP BY category")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		category, count, err := scanCategoryCount(rows)
		if err != nil {
			return err
		}
		stats["category_"+category] = count
	}
	return rows.Err()
}

func scanCategoryCount(scanner interface {
	Scan(dest ...interface{}) error
}) (string, int, error) {
	var category string
	var count int
	if err := scanner.Scan(&category, &count); err != nil {
		return "", 0, err
	}
	return category, count, nil
}

// Helper functions

type entryRowData struct {
	title      sql.NullString
	archivedAt sql.NullTime
	metadata   []byte
	relatedIDs []byte
}

func scanEntry(rows *sql.Rows) (*Entry, error) {
	entry, data, err := scanEntryData(rows)
	if err != nil {
		return nil, err
	}
	applyEntryRow(entry, data)
	return entry, nil
}

func scanEntryRow(row *sql.Row) (*Entry, error) {
	entry, data, err := scanEntryData(row)
	if err != nil {
		return nil, err
	}
	applyEntryRow(entry, data)
	return entry, nil
}

func scanEntryData(scanner interface {
	Scan(dest ...interface{}) error
}) (*Entry, entryRowData, error) {
	var entry Entry
	var data entryRowData

	err := scanner.Scan(
		&entry.ID, &entry.Category, &data.title, &entry.Content, &entry.Source,
		&entry.SessionID, &entry.CreatedAt, &entry.UpdatedAt, &data.archivedAt,
		&entry.TokensEstimate, &data.metadata, &data.relatedIDs,
	)
	if err != nil {
		return nil, entryRowData{}, err
	}

	return &entry, data, nil
}

func applyEntryRow(entry *Entry, data entryRowData) {
	if data.title.Valid {
		entry.Title = data.title.String
	}
	if data.archivedAt.Valid {
		entry.ArchivedAt = &data.archivedAt.Time
	}
	if len(data.metadata) > 0 {
		json.Unmarshal(data.metadata, &entry.Metadata)
	}
	if len(data.relatedIDs) > 0 {
		json.Unmarshal(data.relatedIDs, &entry.RelatedIDs)
	}
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

// =============================================================================
// Facts Storage Methods
// =============================================================================

// SaveFactDecision stores a decision fact
func (a *Archive) SaveFactDecision(fact *FactDecision) error {
	alternatives, err := json.Marshal(fact.Alternatives)
	if err != nil {
		return fmt.Errorf("failed to marshal alternatives: %w", err)
	}

	sourceEntryIDs, err := json.Marshal(fact.SourceEntryIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal source entry IDs: %w", err)
	}

	_, err = a.db.Exec(`
		INSERT OR REPLACE INTO facts_decisions
		(id, choice, rationale, context, alternatives, confidence, source_entry_ids, session_id, extracted_at, superseded_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		fact.ID, fact.Choice, fact.Rationale, fact.Context, alternatives,
		fact.Confidence, sourceEntryIDs, fact.SessionID, fact.ExtractedAt, fact.SupersededBy,
	)
	return err
}

// SaveFactPattern stores a pattern fact
func (a *Archive) SaveFactPattern(fact *FactPattern) error {
	sourceEntryIDs, err := json.Marshal(fact.SourceEntryIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal source entry IDs: %w", err)
	}

	_, err = a.db.Exec(`
		INSERT OR REPLACE INTO facts_patterns
		(id, category, name, pattern, example, rationale, usage_count, source_entry_ids, session_id, extracted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		fact.ID, fact.Category, fact.Name, fact.Pattern, fact.Example,
		fact.Rationale, fact.UsageCount, sourceEntryIDs, fact.SessionID, fact.ExtractedAt,
	)
	return err
}

// SaveFactFailure stores a failure fact
func (a *Archive) SaveFactFailure(fact *FactFailure) error {
	sourceEntryIDs, err := json.Marshal(fact.SourceEntryIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal source entry IDs: %w", err)
	}

	_, err = a.db.Exec(`
		INSERT OR REPLACE INTO facts_failures
		(id, approach, reason, context, resolution, resolution_entry_id, source_entry_ids, session_id, extracted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		fact.ID, fact.Approach, fact.Reason, fact.Context, fact.Resolution,
		fact.ResolutionEntryID, sourceEntryIDs, fact.SessionID, fact.ExtractedAt,
	)
	return err
}

// SaveFactFileChange stores a file change fact
func (a *Archive) SaveFactFileChange(fact *FactFileChange) error {
	sourceEntryIDs, err := json.Marshal(fact.SourceEntryIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal source entry IDs: %w", err)
	}

	_, err = a.db.Exec(`
		INSERT OR REPLACE INTO facts_file_changes
		(id, path, change_type, description, line_start, line_end, source_entry_ids, session_id, extracted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		fact.ID, fact.Path, fact.ChangeType, fact.Description,
		fact.LineStart, fact.LineEnd, sourceEntryIDs, fact.SessionID, fact.ExtractedAt,
	)
	return err
}

// QueryFactDecisions retrieves decision facts
func (a *Archive) QueryFactDecisions(sessionID string, limit int) ([]*FactDecision, error) {
	query, args := decisionFactsQuery(sessionID, limit)
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanDecisionFacts(rows)
}

func decisionFactsQuery(sessionID string, limit int) (string, []interface{}) {
	query := `SELECT id, choice, rationale, context, alternatives, confidence,
	                 source_entry_ids, session_id, extracted_at, superseded_by
	          FROM facts_decisions WHERE 1=1`
	args := []interface{}{}
	if sessionID != "" {
		query += " AND session_id = ?"
		args = append(args, sessionID)
	}
	query += " ORDER BY extracted_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	return query, args
}

type factDecisionRow struct {
	fact           FactDecision
	alternatives   []byte
	sourceEntryIDs []byte
	supersededBy   sql.NullString
}

func scanDecisionFacts(rows *sql.Rows) ([]*FactDecision, error) {
	var facts []*FactDecision
	for rows.Next() {
		row, err := scanDecisionFactRow(rows)
		if err != nil {
			return nil, err
		}
		applyDecisionFactRow(&row.fact, row)
		facts = append(facts, &row.fact)
	}
	return facts, rows.Err()
}

func scanDecisionFactRow(scanner interface {
	Scan(dest ...interface{}) error
}) (factDecisionRow, error) {
	var row factDecisionRow
	err := scanner.Scan(&row.fact.ID, &row.fact.Choice, &row.fact.Rationale, &row.fact.Context,
		&row.alternatives, &row.fact.Confidence, &row.sourceEntryIDs, &row.fact.SessionID,
		&row.fact.ExtractedAt, &row.supersededBy)
	if err != nil {
		return factDecisionRow{}, err
	}
	return row, nil
}

func applyDecisionFactRow(fact *FactDecision, row factDecisionRow) {
	if len(row.alternatives) > 0 {
		json.Unmarshal(row.alternatives, &fact.Alternatives)
	}
	if len(row.sourceEntryIDs) > 0 {
		json.Unmarshal(row.sourceEntryIDs, &fact.SourceEntryIDs)
	}
	if row.supersededBy.Valid {
		fact.SupersededBy = row.supersededBy.String
	}
}

// QueryFactPatterns retrieves pattern facts
func (a *Archive) QueryFactPatterns(category string, limit int) ([]*FactPattern, error) {
	query, args := patternFactsQuery(category, limit)
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPatternFacts(rows)
}

func patternFactsQuery(category string, limit int) (string, []interface{}) {
	query := `SELECT id, category, name, pattern, example, rationale, usage_count,
	                 source_entry_ids, session_id, extracted_at
	          FROM facts_patterns WHERE 1=1`
	args := []interface{}{}
	if category != "" {
		query += " AND category = ?"
		args = append(args, category)
	}
	query += " ORDER BY usage_count DESC, extracted_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	return query, args
}

type factPatternRow struct {
	fact           FactPattern
	sourceEntryIDs []byte
}

func scanPatternFacts(rows *sql.Rows) ([]*FactPattern, error) {
	var facts []*FactPattern
	for rows.Next() {
		row, err := scanPatternFactRow(rows)
		if err != nil {
			return nil, err
		}
		applyPatternFactRow(&row.fact, row)
		facts = append(facts, &row.fact)
	}
	return facts, rows.Err()
}

func scanPatternFactRow(scanner interface {
	Scan(dest ...interface{}) error
}) (factPatternRow, error) {
	var row factPatternRow
	err := scanner.Scan(&row.fact.ID, &row.fact.Category, &row.fact.Name, &row.fact.Pattern,
		&row.fact.Example, &row.fact.Rationale, &row.fact.UsageCount, &row.sourceEntryIDs,
		&row.fact.SessionID, &row.fact.ExtractedAt)
	if err != nil {
		return factPatternRow{}, err
	}
	return row, nil
}

func applyPatternFactRow(fact *FactPattern, row factPatternRow) {
	if len(row.sourceEntryIDs) > 0 {
		json.Unmarshal(row.sourceEntryIDs, &fact.SourceEntryIDs)
	}
}

// QueryFactFailures retrieves failure facts
func (a *Archive) QueryFactFailures(sessionID string, limit int) ([]*FactFailure, error) {
	query, args := failureFactsQuery(sessionID, limit)
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanFailureFacts(rows)
}

func failureFactsQuery(sessionID string, limit int) (string, []interface{}) {
	query := `SELECT id, approach, reason, context, resolution, resolution_entry_id,
	                 source_entry_ids, session_id, extracted_at
	          FROM facts_failures WHERE 1=1`
	args := []interface{}{}
	if sessionID != "" {
		query += " AND session_id = ?"
		args = append(args, sessionID)
	}
	query += " ORDER BY extracted_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	return query, args
}

type factFailureRow struct {
	fact              FactFailure
	sourceEntryIDs    []byte
	resolutionEntryID sql.NullString
}

func scanFailureFacts(rows *sql.Rows) ([]*FactFailure, error) {
	var facts []*FactFailure
	for rows.Next() {
		row, err := scanFailureFactRow(rows)
		if err != nil {
			return nil, err
		}
		applyFailureFactRow(&row.fact, row)
		facts = append(facts, &row.fact)
	}
	return facts, rows.Err()
}

func scanFailureFactRow(scanner interface {
	Scan(dest ...interface{}) error
}) (factFailureRow, error) {
	var row factFailureRow
	err := scanner.Scan(&row.fact.ID, &row.fact.Approach, &row.fact.Reason, &row.fact.Context,
		&row.fact.Resolution, &row.resolutionEntryID, &row.sourceEntryIDs,
		&row.fact.SessionID, &row.fact.ExtractedAt)
	if err != nil {
		return factFailureRow{}, err
	}
	return row, nil
}

func applyFailureFactRow(fact *FactFailure, row factFailureRow) {
	if len(row.sourceEntryIDs) > 0 {
		json.Unmarshal(row.sourceEntryIDs, &fact.SourceEntryIDs)
	}
	if row.resolutionEntryID.Valid {
		fact.ResolutionEntryID = row.resolutionEntryID.String
	}
}

// QueryFactFileChanges retrieves file change facts
func (a *Archive) QueryFactFileChanges(path string, limit int) ([]*FactFileChange, error) {
	query, args := fileChangeFactsQuery(path, limit)
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanFileChangeFacts(rows)
}

func fileChangeFactsQuery(path string, limit int) (string, []interface{}) {
	query := `SELECT id, path, change_type, description, line_start, line_end,
	                 source_entry_ids, session_id, extracted_at
	          FROM facts_file_changes WHERE 1=1`
	args := []interface{}{}
	if path != "" {
		query += " AND path = ?"
		args = append(args, path)
	}
	query += " ORDER BY extracted_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	return query, args
}

type fileChangeRow struct {
	fact           FactFileChange
	sourceEntryIDs []byte
}

func scanFileChangeFacts(rows *sql.Rows) ([]*FactFileChange, error) {
	var facts []*FactFileChange
	for rows.Next() {
		row, err := scanFileChangeRow(rows)
		if err != nil {
			return nil, err
		}
		applyFileChangeRow(&row.fact, row)
		facts = append(facts, &row.fact)
	}
	return facts, rows.Err()
}

func scanFileChangeRow(scanner interface {
	Scan(dest ...interface{}) error
}) (fileChangeRow, error) {
	var row fileChangeRow
	err := scanner.Scan(&row.fact.ID, &row.fact.Path, &row.fact.ChangeType, &row.fact.Description,
		&row.fact.LineStart, &row.fact.LineEnd, &row.sourceEntryIDs,
		&row.fact.SessionID, &row.fact.ExtractedAt)
	if err != nil {
		return fileChangeRow{}, err
	}
	return row, nil
}

func applyFileChangeRow(fact *FactFileChange, row fileChangeRow) {
	if len(row.sourceEntryIDs) > 0 {
		json.Unmarshal(row.sourceEntryIDs, &fact.SourceEntryIDs)
	}
}

// =============================================================================
// Summary Storage Methods
// =============================================================================

// SaveSummary stores a summary
func (a *Archive) SaveSummary(summary *CompactedSummary) error {
	keyPoints, err := json.Marshal(summary.KeyPoints)
	if err != nil {
		return fmt.Errorf("failed to marshal key points: %w", err)
	}

	sourceEntryIDs, err := json.Marshal(summary.SourceEntryIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal source entry IDs: %w", err)
	}

	sourceSummaryIDs, err := json.Marshal(summary.SourceSummaryIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal source summary IDs: %w", err)
	}

	_, err = a.db.Exec(`
		INSERT OR REPLACE INTO summaries
		(id, level, scope, scope_id, content, key_points, tokens_estimate,
		 source_entry_ids, source_summary_ids, session_id, time_start, time_end, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		summary.ID, summary.Level, summary.Scope, summary.ScopeID, summary.Content,
		keyPoints, summary.TokensEstimate, sourceEntryIDs, sourceSummaryIDs,
		summary.SessionID, summary.TimeStart, summary.TimeEnd, summary.CreatedAt,
	)
	return err
}

// QuerySummaries retrieves summaries matching the query
func (a *Archive) QuerySummaries(q SummaryQuery) ([]*CompactedSummary, error) {
	query, args := buildSummaryQuery(q)
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSummaries(rows)
}

func buildSummaryQuery(q SummaryQuery) (string, []interface{}) {
	query := baseSummaryQuery()
	args := []interface{}{}
	query, args = addSummaryLevels(query, args, q.Levels)
	query, args = addSummaryScopes(query, args, q.Scopes)
	query, args = addSummarySessions(query, args, q.SessionIDs)
	query, args = addSummarySince(query, args, q.Since)
	query, args = addSummaryUntil(query, args, q.Until)
	query = addSummaryOrder(query)
	query = addSummaryLimit(query, q.Limit)
	return query, args
}

func baseSummaryQuery() string {
	return `SELECT id, level, scope, scope_id, content, key_points, tokens_estimate,
	                 source_entry_ids, source_summary_ids, session_id, time_start, time_end, created_at
	          FROM summaries WHERE 1=1`
}

func addSummaryLevels(query string, args []interface{}, levels []SummaryLevel) (string, []interface{}) {
	if len(levels) == 0 {
		return query, args
	}
	placeholders := summaryPlaceholders(len(levels))
	for _, level := range levels {
		args = append(args, level)
	}
	query += fmt.Sprintf(" AND level IN (%s)", joinStrings(placeholders, ","))
	return query, args
}

func addSummaryScopes(query string, args []interface{}, scopes []SummaryScope) (string, []interface{}) {
	if len(scopes) == 0 {
		return query, args
	}
	placeholders := summaryPlaceholders(len(scopes))
	for _, scope := range scopes {
		args = append(args, scope)
	}
	query += fmt.Sprintf(" AND scope IN (%s)", joinStrings(placeholders, ","))
	return query, args
}

func addSummarySessions(query string, args []interface{}, sessionIDs []string) (string, []interface{}) {
	if len(sessionIDs) == 0 {
		return query, args
	}
	placeholders := summaryPlaceholders(len(sessionIDs))
	for _, sessionID := range sessionIDs {
		args = append(args, sessionID)
	}
	query += fmt.Sprintf(" AND session_id IN (%s)", joinStrings(placeholders, ","))
	return query, args
}

func addSummarySince(query string, args []interface{}, since *time.Time) (string, []interface{}) {
	if since == nil {
		return query, args
	}
	query += " AND time_start >= ?"
	args = append(args, since)
	return query, args
}

func addSummaryUntil(query string, args []interface{}, until *time.Time) (string, []interface{}) {
	if until == nil {
		return query, args
	}
	query += " AND time_end <= ?"
	args = append(args, until)
	return query, args
}

func addSummaryOrder(query string) string {
	return query + " ORDER BY created_at DESC"
}

func addSummaryLimit(query string, limit int) string {
	if limit <= 0 {
		return query
	}
	return query + fmt.Sprintf(" LIMIT %d", limit)
}

func summaryPlaceholders(count int) []string {
	placeholders := make([]string, count)
	for i := range placeholders {
		placeholders[i] = "?"
	}
	return placeholders
}

// SearchSummaries performs full-text search on summaries
func (a *Archive) SearchSummaries(searchText string, limit int) ([]*CompactedSummary, error) {
	query := summarySearchQuery(limit)
	rows, err := a.db.Query(query, searchText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSummaries(rows)
}

func summarySearchQuery(limit int) string {
	query := `
		SELECT s.id, s.level, s.scope, s.scope_id, s.content, s.key_points, s.tokens_estimate,
		       s.source_entry_ids, s.source_summary_ids, s.session_id, s.time_start, s.time_end, s.created_at
		FROM summaries s
		JOIN summaries_fts fts ON s.id = fts.id
		WHERE summaries_fts MATCH ?
		ORDER BY rank
	`
	if limit <= 0 {
		return query
	}
	return query + fmt.Sprintf(" LIMIT %d", limit)
}

func scanSummaries(rows *sql.Rows) ([]*CompactedSummary, error) {
	var summaries []*CompactedSummary
	for rows.Next() {
		summary, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

// GetSummary retrieves a single summary by ID
func (a *Archive) GetSummary(id string) (*CompactedSummary, error) {
	row := a.db.QueryRow(`
		SELECT id, level, scope, scope_id, content, key_points, tokens_estimate,
		       source_entry_ids, source_summary_ids, session_id, time_start, time_end, created_at
		FROM summaries WHERE id = ?
	`, id)

	summary, data, err := scanSummaryRow(row)
	if err != nil {
		return nil, err
	}
	applySummaryRowData(summary, data)
	return summary, nil
}

func scanSummary(rows *sql.Rows) (*CompactedSummary, error) {
	summary, data, err := scanSummaryRow(rows)
	if err != nil {
		return nil, err
	}
	applySummaryRowData(summary, data)
	return summary, nil
}

type summaryRowData struct {
	scopeID          sql.NullString
	sessionID        sql.NullString
	timeStart        sql.NullTime
	timeEnd          sql.NullTime
	keyPoints        []byte
	sourceEntryIDs   []byte
	sourceSummaryIDs []byte
}

func scanSummaryRow(scanner interface {
	Scan(dest ...interface{}) error
}) (*CompactedSummary, summaryRowData, error) {
	var summary CompactedSummary
	var data summaryRowData

	err := scanner.Scan(&summary.ID, &summary.Level, &summary.Scope, &data.scopeID, &summary.Content,
		&data.keyPoints, &summary.TokensEstimate, &data.sourceEntryIDs, &data.sourceSummaryIDs,
		&data.sessionID, &data.timeStart, &data.timeEnd, &summary.CreatedAt)
	if err != nil {
		return nil, summaryRowData{}, err
	}

	return &summary, data, nil
}

func applySummaryRowData(summary *CompactedSummary, data summaryRowData) {
	applySummaryNulls(summary, data)
	applySummaryLists(summary, data)
}

func applySummaryNulls(summary *CompactedSummary, data summaryRowData) {
	if data.scopeID.Valid {
		summary.ScopeID = data.scopeID.String
	}
	if data.sessionID.Valid {
		summary.SessionID = data.sessionID.String
	}
	if data.timeStart.Valid {
		summary.TimeStart = &data.timeStart.Time
	}
	if data.timeEnd.Valid {
		summary.TimeEnd = &data.timeEnd.Time
	}
}

func applySummaryLists(summary *CompactedSummary, data summaryRowData) {
	if len(data.keyPoints) > 0 {
		json.Unmarshal(data.keyPoints, &summary.KeyPoints)
	}
	if len(data.sourceEntryIDs) > 0 {
		json.Unmarshal(data.sourceEntryIDs, &summary.SourceEntryIDs)
	}
	if len(data.sourceSummaryIDs) > 0 {
		json.Unmarshal(data.sourceSummaryIDs, &summary.SourceSummaryIDs)
	}
}

// GetLatestSummary retrieves the most recent summary for a given level and scope
func (a *Archive) GetLatestSummary(level SummaryLevel, scope SummaryScope, scopeID string) (*CompactedSummary, error) {
	query := `SELECT id, level, scope, scope_id, content, key_points, tokens_estimate,
	                 source_entry_ids, source_summary_ids, session_id, time_start, time_end, created_at
	          FROM summaries WHERE level = ? AND scope = ?`
	args := []interface{}{level, scope}

	if scopeID != "" {
		query += " AND scope_id = ?"
		args = append(args, scopeID)
	}

	query += " ORDER BY created_at DESC LIMIT 1"

	row := a.db.QueryRow(query, args...)

	summary, data, err := scanSummaryRow(row)
	if err != nil {
		return nil, err
	}
	applySummaryRowData(summary, data)
	return summary, nil
}
