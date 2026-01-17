package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrKnowledgeManagerClosed = errors.New("knowledge manager is closed")
	ErrUnauthorizedWrite      = errors.New("unauthorized write to project knowledge")
	ErrUnknownScope           = errors.New("unknown knowledge scope")
	ErrKnowledgeNotFound      = errors.New("knowledge entry not found")
)

type KnowledgeScope string

const (
	ScopeProject KnowledgeScope = "project"
	ScopeSession KnowledgeScope = "session"
)

type KnowledgeEntry struct {
	ID        string            `json:"id"`
	Scope     KnowledgeScope    `json:"scope"`
	SessionID string            `json:"session_id"`
	Type      string            `json:"type"`
	Key       string            `json:"key"`
	Value     string            `json:"value"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type QueryOpts struct {
	Scope                KnowledgeScope
	Type                 string
	IncludeOtherSessions bool
	Limit                int
}

type KnowledgeManagerConfig struct {
	ProjectID     string
	SessionID     string
	BaseDir       string
	IsArchivalist bool
}

type SessionKnowledgeManager struct {
	config    KnowledgeManagerConfig
	projectDB *sql.DB
	sessionDB *sql.DB

	otherSessionViews map[string]*sql.DB

	mu     sync.RWMutex
	closed bool
}

func NewSessionKnowledgeManager(cfg KnowledgeManagerConfig) (*SessionKnowledgeManager, error) {
	cfg = normalizeKnowledgeConfig(cfg)

	projectDB, err := openKnowledgeDB(cfg.BaseDir, cfg.ProjectID, "project")
	if err != nil {
		return nil, err
	}

	sessionDB, err := openKnowledgeDB(cfg.BaseDir, cfg.ProjectID, cfg.SessionID)
	if err != nil {
		projectDB.Close()
		return nil, err
	}

	return &SessionKnowledgeManager{
		config:            cfg,
		projectDB:         projectDB,
		sessionDB:         sessionDB,
		otherSessionViews: make(map[string]*sql.DB),
	}, nil
}

func normalizeKnowledgeConfig(cfg KnowledgeManagerConfig) KnowledgeManagerConfig {
	if cfg.BaseDir == "" {
		homeDir, _ := os.UserHomeDir()
		cfg.BaseDir = filepath.Join(homeDir, ".sylk", "knowledge")
	}
	if cfg.ProjectID == "" {
		cfg.ProjectID = "default"
	}
	return cfg
}

func openKnowledgeDB(baseDir, projectID, dbName string) (*sql.DB, error) {
	dir := filepath.Join(baseDir, projectID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create knowledge dir: %w", err)
	}

	dbPath := filepath.Join(dir, dbName+".db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open knowledge db: %w", err)
	}

	if err := createKnowledgeSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func createKnowledgeSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS knowledge (
			id TEXT PRIMARY KEY,
			scope TEXT NOT NULL,
			session_id TEXT NOT NULL,
			type TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			metadata TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_knowledge_type ON knowledge(type);
		CREATE INDEX IF NOT EXISTS idx_knowledge_key ON knowledge(key);
		CREATE INDEX IF NOT EXISTS idx_knowledge_session ON knowledge(session_id);
	`)
	return err
}

func (m *SessionKnowledgeManager) StoreKnowledge(entry KnowledgeEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrKnowledgeManagerClosed
	}

	return m.storeEntry(entry)
}

func (m *SessionKnowledgeManager) storeEntry(entry KnowledgeEntry) error {
	switch entry.Scope {
	case ScopeProject:
		return m.storeProjectEntry(entry)
	case ScopeSession:
		return m.storeSessionEntry(entry)
	default:
		return ErrUnknownScope
	}
}

func (m *SessionKnowledgeManager) storeProjectEntry(entry KnowledgeEntry) error {
	if !m.config.IsArchivalist {
		return ErrUnauthorizedWrite
	}

	entry.SessionID = m.config.SessionID
	return m.insertEntry(m.projectDB, entry)
}

func (m *SessionKnowledgeManager) storeSessionEntry(entry KnowledgeEntry) error {
	entry.SessionID = m.config.SessionID
	return m.insertEntry(m.sessionDB, entry)
}

func (m *SessionKnowledgeManager) insertEntry(db *sql.DB, entry KnowledgeEntry) error {
	if entry.ID == "" {
		entry.ID = generateKnowledgeID()
	}

	metadata, _ := json.Marshal(entry.Metadata)
	now := time.Now()

	_, err := db.Exec(`
		INSERT INTO knowledge (id, scope, session_id, type, key, value, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			value = ?, metadata = ?, updated_at = ?
	`,
		entry.ID, entry.Scope, entry.SessionID, entry.Type, entry.Key, entry.Value, string(metadata), now, now,
		entry.Value, string(metadata), now,
	)
	return err
}

func generateKnowledgeID() string {
	return fmt.Sprintf("k_%d", time.Now().UnixNano())
}

func (m *SessionKnowledgeManager) QueryKnowledge(query string, opts QueryOpts) ([]KnowledgeEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrKnowledgeManagerClosed
	}

	var results []KnowledgeEntry

	projectResults := m.queryDB(m.projectDB, query, opts)
	results = append(results, projectResults...)

	sessionResults := m.queryDB(m.sessionDB, query, opts)
	results = append(results, sessionResults...)

	if opts.IncludeOtherSessions {
		results = m.appendOtherSessionResults(results, query, opts)
	}

	return m.deduplicateResults(results), nil
}

func (m *SessionKnowledgeManager) queryDB(db *sql.DB, query string, opts QueryOpts) []KnowledgeEntry {
	sqlQuery, args := m.buildQuery(query, opts)

	rows, err := db.Query(sqlQuery, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	return m.scanEntries(rows)
}

func (m *SessionKnowledgeManager) buildQuery(query string, opts QueryOpts) (string, []interface{}) {
	sql := `SELECT id, scope, session_id, type, key, value, metadata, created_at, updated_at
		FROM knowledge WHERE (key LIKE ? OR value LIKE ?)`
	args := []interface{}{"%" + query + "%", "%" + query + "%"}

	if opts.Type != "" {
		sql += " AND type = ?"
		args = append(args, opts.Type)
	}

	sql += " ORDER BY updated_at DESC"

	if opts.Limit > 0 {
		sql += " LIMIT ?"
		args = append(args, opts.Limit)
	}

	return sql, args
}

func (m *SessionKnowledgeManager) scanEntries(rows *sql.Rows) []KnowledgeEntry {
	var entries []KnowledgeEntry

	for rows.Next() {
		entry := m.scanSingleEntry(rows)
		if entry != nil {
			entries = append(entries, *entry)
		}
	}

	return entries
}

func (m *SessionKnowledgeManager) scanSingleEntry(rows *sql.Rows) *KnowledgeEntry {
	var entry KnowledgeEntry
	var metadata string

	err := rows.Scan(
		&entry.ID,
		&entry.Scope,
		&entry.SessionID,
		&entry.Type,
		&entry.Key,
		&entry.Value,
		&metadata,
		&entry.CreatedAt,
		&entry.UpdatedAt,
	)
	if err != nil {
		return nil
	}

	json.Unmarshal([]byte(metadata), &entry.Metadata)
	return &entry
}

func (m *SessionKnowledgeManager) appendOtherSessionResults(results []KnowledgeEntry, query string, opts QueryOpts) []KnowledgeEntry {
	for _, view := range m.otherSessionViews {
		otherResults := m.queryDB(view, query, opts)
		results = append(results, otherResults...)
	}
	return results
}

func (m *SessionKnowledgeManager) deduplicateResults(entries []KnowledgeEntry) []KnowledgeEntry {
	seen := make(map[string]bool)
	var result []KnowledgeEntry

	for _, entry := range entries {
		if !seen[entry.ID] {
			seen[entry.ID] = true
			result = append(result, entry)
		}
	}

	return result
}

func (m *SessionKnowledgeManager) GetEntry(id string) (*KnowledgeEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrKnowledgeManagerClosed
	}

	entry := m.findEntry(m.projectDB, id)
	if entry != nil {
		return entry, nil
	}

	entry = m.findEntry(m.sessionDB, id)
	if entry != nil {
		return entry, nil
	}

	return nil, ErrKnowledgeNotFound
}

func (m *SessionKnowledgeManager) findEntry(db *sql.DB, id string) *KnowledgeEntry {
	var entry KnowledgeEntry
	var metadata string

	err := db.QueryRow(`
		SELECT id, scope, session_id, type, key, value, metadata, created_at, updated_at
		FROM knowledge WHERE id = ?
	`, id).Scan(
		&entry.ID,
		&entry.Scope,
		&entry.SessionID,
		&entry.Type,
		&entry.Key,
		&entry.Value,
		&metadata,
		&entry.CreatedAt,
		&entry.UpdatedAt,
	)

	if err != nil {
		return nil
	}

	json.Unmarshal([]byte(metadata), &entry.Metadata)
	return &entry
}

func (m *SessionKnowledgeManager) DeleteEntry(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrKnowledgeManagerClosed
	}

	m.sessionDB.Exec(`DELETE FROM knowledge WHERE id = ?`, id)

	if m.config.IsArchivalist {
		m.projectDB.Exec(`DELETE FROM knowledge WHERE id = ?`, id)
	}

	return nil
}

func (m *SessionKnowledgeManager) AddOtherSessionView(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrKnowledgeManagerClosed
	}

	if _, exists := m.otherSessionViews[sessionID]; exists {
		return nil
	}

	db, err := openKnowledgeDB(m.config.BaseDir, m.config.ProjectID, sessionID)
	if err != nil {
		return err
	}

	m.otherSessionViews[sessionID] = db
	return nil
}

func (m *SessionKnowledgeManager) RemoveOtherSessionView(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if db, exists := m.otherSessionViews[sessionID]; exists {
		db.Close()
		delete(m.otherSessionViews, sessionID)
	}
}

func (m *SessionKnowledgeManager) GetProjectStats() *KnowledgeStats {
	return m.getDBStats(m.projectDB, "project")
}

func (m *SessionKnowledgeManager) GetSessionStats() *KnowledgeStats {
	return m.getDBStats(m.sessionDB, "session")
}

type KnowledgeStats struct {
	Scope      string
	EntryCount int
	TypeCounts map[string]int
}

func (m *SessionKnowledgeManager) getDBStats(db *sql.DB, scope string) *KnowledgeStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := &KnowledgeStats{
		Scope:      scope,
		TypeCounts: make(map[string]int),
	}

	db.QueryRow(`SELECT COUNT(*) FROM knowledge`).Scan(&stats.EntryCount)

	rows, err := db.Query(`SELECT type, COUNT(*) FROM knowledge GROUP BY type`)
	if err != nil {
		return stats
	}
	defer rows.Close()

	for rows.Next() {
		var typ string
		var count int
		rows.Scan(&typ, &count)
		stats.TypeCounts[typ] = count
	}

	return stats
}

func (m *SessionKnowledgeManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrKnowledgeManagerClosed
	}
	m.closed = true

	for _, db := range m.otherSessionViews {
		db.Close()
	}
	m.otherSessionViews = nil

	m.sessionDB.Close()
	m.projectDB.Close()

	return nil
}
