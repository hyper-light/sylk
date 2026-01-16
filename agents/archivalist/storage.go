package archivalist

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultTokenThreshold is the default threshold for triggering archival (750K tokens)
	DefaultTokenThreshold = 750_000
)

// Store provides thread-safe in-memory storage (L1 hot memory)
type Store struct {
	mu sync.RWMutex

	// Current session
	currentSession *Session

	// Entry storage by ID
	entries map[string]*Entry

	// Indexes for efficient querying
	byCategory map[Category][]string
	bySource   map[SourceModel][]string
	bySession  map[string][]string

	// Chronological order
	chronological []string

	// Token tracking
	totalTokens    int
	tokenThreshold int

	// Archive reference (L2 cold storage)
	archive *Archive
}

// StoreConfig configures the in-memory store
type StoreConfig struct {
	TokenThreshold int
	Archive        *Archive
}

// NewStore creates a new in-memory store
func NewStore(cfg StoreConfig) *Store {
	threshold := cfg.TokenThreshold
	if threshold == 0 {
		threshold = DefaultTokenThreshold
	}

	store := &Store{
		entries:        make(map[string]*Entry),
		byCategory:     make(map[Category][]string),
		bySource:       make(map[SourceModel][]string),
		bySession:      make(map[string][]string),
		chronological:  make([]string, 0),
		tokenThreshold: threshold,
		archive:        cfg.Archive,
	}

	// Start a new session
	store.startNewSession()

	return store
}

// startNewSession creates a new session
func (s *Store) startNewSession() {
	s.currentSession = &Session{
		ID:        s.generateID("sess"),
		StartedAt: time.Now(),
	}
}

// GetCurrentSession returns the current session
func (s *Store) GetCurrentSession() *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentSession
}

// EndSession ends the current session and archives its entries
func (s *Store) EndSession(summary string, primaryFocus string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentSession == nil {
		return fmt.Errorf("no active session")
	}

	now := time.Now()
	s.currentSession.EndedAt = &now
	s.currentSession.Summary = summary
	s.currentSession.PrimaryFocus = primaryFocus
	s.currentSession.EntryCount = len(s.bySession[s.currentSession.ID])

	// Archive session entries if archive is available
	if s.archive != nil {
		if err := s.archiveSessionEntries(s.currentSession.ID); err != nil {
			return fmt.Errorf("failed to archive session entries: %w", err)
		}
		if err := s.archive.SaveSession(s.currentSession); err != nil {
			return fmt.Errorf("failed to save session: %w", err)
		}
	}

	// Start new session
	s.startNewSession()

	return nil
}

// InsertEntry stores an entry and returns the assigned ID
func (s *Store) InsertEntry(entry *Entry) (string, error) {
	return s.InsertEntryInSession(s.currentSession.ID, entry)
}

func (s *Store) InsertEntryInSession(sessionID string, entry *Entry) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.ID == "" {
		entry.ID = s.generateID("ent")
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	entry.UpdatedAt = time.Now()
	entry.SessionID = sessionID
	entry.TokensEstimate = EstimateTokens(entry.Content + entry.Title)

	if s.totalTokens+entry.TokensEstimate > s.tokenThreshold {
		if err := s.archiveOldestEntries(); err != nil {
			return "", fmt.Errorf("failed to archive entries: %w", err)
		}
	}

	s.entries[entry.ID] = entry
	s.byCategory[entry.Category] = append(s.byCategory[entry.Category], entry.ID)
	s.bySource[entry.Source] = append(s.bySource[entry.Source], entry.ID)
	s.bySession[entry.SessionID] = append(s.bySession[entry.SessionID], entry.ID)
	s.chronological = append(s.chronological, entry.ID)
	s.totalTokens += entry.TokensEstimate

	return entry.ID, nil
}

// UpdateEntry updates an existing entry
func (s *Store) UpdateEntry(id string, updates func(*Entry)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[id]
	if !ok {
		return fmt.Errorf("entry not found: %s", id)
	}

	oldTokens := entry.TokensEstimate
	updates(entry)
	entry.UpdatedAt = time.Now()
	entry.TokensEstimate = EstimateTokens(entry.Content + entry.Title)
	s.totalTokens += entry.TokensEstimate - oldTokens

	return nil
}

// GetEntry retrieves an entry by ID, checking L1 first then L2
func (s *Store) GetEntry(id string) (*Entry, bool) {
	s.mu.RLock()
	entry, ok := s.entries[id]
	s.mu.RUnlock()

	if ok {
		return entry, true
	}

	// Check archive if not in hot memory
	if s.archive != nil {
		archived, err := s.archive.GetEntry(id)
		if err == nil && archived != nil {
			return archived, true
		}
	}

	return nil, false
}

// Query retrieves entries matching the query parameters
func (s *Store) Query(q ArchiveQuery) ([]*Entry, error) {
	results := s.queryHotMemory(q)
	results, err := s.mergeArchivedQuery(results, q)
	if err != nil {
		return nil, err
	}
	s.sortByCreatedDesc(results)
	return s.applyResultLimit(results, q.Limit), nil
}

func (s *Store) queryHotMemory(q ArchiveQuery) []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*Entry
	for _, id := range s.chronological {
		if entry := s.entries[id]; s.matchesQuery(entry, q) {
			results = append(results, entry)
		}
	}
	return results
}

func (s *Store) mergeArchivedQuery(results []*Entry, q ArchiveQuery) ([]*Entry, error) {
	if !q.IncludeArchived || s.archive == nil {
		return results, nil
	}
	archived, err := s.archive.Query(q)
	if err != nil {
		return nil, fmt.Errorf("failed to query archive: %w", err)
	}
	return s.mergeUniqueEntries(results, archived), nil
}

func (s *Store) sortByCreatedDesc(results []*Entry) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
}

// QueryByCategory retrieves entries in a specific category
func (s *Store) QueryByCategory(category Category, limit int) []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.byCategory[category]
	var results []*Entry

	// Iterate in reverse for most recent first
	for i := len(ids) - 1; i >= 0; i-- {
		results = append(results, s.entries[ids[i]])
		if limit > 0 && len(results) >= limit {
			break
		}
	}

	return results
}

// SearchText performs text search across hot memory and optionally archive
func (s *Store) SearchText(text string, includeArchived bool, limit int) ([]*Entry, error) {
	results := s.searchHotMemory(text)
	results, err := s.mergeArchivedResults(results, text, includeArchived, limit)
	if err != nil {
		return nil, err
	}
	return s.applyResultLimit(results, limit), nil
}

func (s *Store) searchHotMemory(text string) []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*Entry
	for _, entry := range s.entries {
		if s.entryMatchesText(entry, text) {
			results = append(results, entry)
		}
	}
	return results
}

func (s *Store) entryMatchesText(entry *Entry, text string) bool {
	return containsIgnoreCase(entry.Content, text) || containsIgnoreCase(entry.Title, text)
}

func (s *Store) mergeArchivedResults(results []*Entry, text string, includeArchived bool, limit int) ([]*Entry, error) {
	if !includeArchived || s.archive == nil {
		return results, nil
	}
	archived, err := s.archive.SearchText(text, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search archive: %w", err)
	}
	return s.mergeUniqueEntries(results, archived), nil
}

func (s *Store) mergeUniqueEntries(base, additional []*Entry) []*Entry {
	seen := make(map[string]bool)
	for _, e := range base {
		seen[e.ID] = true
	}
	for _, e := range additional {
		if !seen[e.ID] {
			base = append(base, e)
		}
	}
	return base
}

func (s *Store) applyResultLimit(results []*Entry, limit int) []*Entry {
	if limit > 0 && len(results) > limit {
		return results[:limit]
	}
	return results
}

// RestoreFromArchive pulls entries from archive back into hot memory
func (s *Store) RestoreFromArchive(ids []string) error {
	if s.archive == nil {
		return fmt.Errorf("no archive configured")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range ids {
		// Skip if already in hot memory
		if _, ok := s.entries[id]; ok {
			continue
		}

		entry, err := s.archive.GetEntry(id)
		if err != nil {
			return fmt.Errorf("failed to retrieve entry %s: %w", id, err)
		}
		if entry == nil {
			continue
		}

		// Check if we need to archive to make room
		if s.totalTokens+entry.TokensEstimate > s.tokenThreshold {
			if err := s.archiveOldestEntries(); err != nil {
				return fmt.Errorf("failed to make room: %w", err)
			}
		}

		// Clear archived timestamp since it's now hot
		entry.ArchivedAt = nil

		s.entries[entry.ID] = entry
		s.byCategory[entry.Category] = append(s.byCategory[entry.Category], entry.ID)
		s.bySource[entry.Source] = append(s.bySource[entry.Source], entry.ID)
		s.bySession[entry.SessionID] = append(s.bySession[entry.SessionID], entry.ID)
		s.chronological = append(s.chronological, entry.ID)
		s.totalTokens += entry.TokensEstimate
	}

	return nil
}

// Stats returns current storage statistics
func (s *Store) Stats() StorageStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byCategory := make(map[Category]int)
	for cat, ids := range s.byCategory {
		byCategory[cat] = len(ids)
	}

	bySource := make(map[SourceModel]int)
	for src, ids := range s.bySource {
		bySource[src] = len(ids)
	}

	var archivedCount int
	if s.archive != nil {
		stats, _ := s.archive.Stats()
		archivedCount = stats["total_entries"]
	}

	var sessionID string
	if s.currentSession != nil {
		sessionID = s.currentSession.ID
	}

	return StorageStats{
		TotalEntries:      len(s.entries),
		EntriesByCategory: byCategory,
		EntriesBySource:   bySource,
		HotMemoryTokens:   s.totalTokens,
		ArchivedEntries:   archivedCount,
		CurrentSessionID:  sessionID,
	}
}

// archiveOldestEntries moves the oldest entries to archive to free up space
func (s *Store) archiveOldestEntries() error {
	if s.archive == nil {
		// No archive, just remove oldest entries
		s.removeOldestEntries(s.tokenThreshold / 4)
		return nil
	}

	// Archive oldest 25% of entries by tokens
	targetTokens := s.tokenThreshold / 4
	var toArchive []*Entry
	var removedTokens int

	for _, id := range s.chronological {
		entry := s.entries[id]
		toArchive = append(toArchive, entry)
		removedTokens += entry.TokensEstimate
		if removedTokens >= targetTokens {
			break
		}
	}

	if err := s.archive.ArchiveEntries(toArchive); err != nil {
		return err
	}

	// Remove archived entries from hot memory
	for _, entry := range toArchive {
		s.removeEntry(entry.ID)
	}

	return nil
}

// archiveSessionEntries archives all entries from a session
func (s *Store) archiveSessionEntries(sessionID string) error {
	ids := s.bySession[sessionID]
	var entries []*Entry

	for _, id := range ids {
		if entry, ok := s.entries[id]; ok {
			entries = append(entries, entry)
		}
	}

	if len(entries) > 0 {
		return s.archive.ArchiveEntries(entries)
	}
	return nil
}

// removeOldestEntries removes entries without archiving
func (s *Store) removeOldestEntries(targetTokens int) {
	var removedTokens int
	var toRemove []string

	for _, id := range s.chronological {
		entry := s.entries[id]
		toRemove = append(toRemove, id)
		removedTokens += entry.TokensEstimate
		if removedTokens >= targetTokens {
			break
		}
	}

	for _, id := range toRemove {
		s.removeEntry(id)
	}
}

// MarkArchived marks an entry as archived and removes it from hot memory
func (s *Store) MarkArchived(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry, ok := s.entries[id]; ok {
		now := time.Now()
		entry.ArchivedAt = &now
	}

	s.removeEntry(id)
}

// removeEntry removes an entry from all indexes
func (s *Store) removeEntry(id string) {
	entry, ok := s.entries[id]
	if !ok {
		return
	}

	s.totalTokens -= entry.TokensEstimate
	delete(s.entries, id)

	// Remove from indexes
	s.byCategory[entry.Category] = removeFromSlice(s.byCategory[entry.Category], id)
	s.bySource[entry.Source] = removeFromSlice(s.bySource[entry.Source], id)
	s.bySession[entry.SessionID] = removeFromSlice(s.bySession[entry.SessionID], id)
	s.chronological = removeFromSlice(s.chronological, id)
}

// matchesQuery checks if an entry matches the query parameters
func (s *Store) matchesQuery(entry *Entry, q ArchiveQuery) bool {
	return s.matchesCategoryFilter(entry, q.Categories) &&
		s.matchesSourceFilter(entry, q.Sources) &&
		s.matchesSessionFilter(entry, q.SessionIDs) &&
		s.matchesIDFilter(entry, q.IDs) &&
		s.matchesDateRange(entry, q.Since, q.Until)
}

func (s *Store) matchesCategoryFilter(entry *Entry, categories []Category) bool {
	if len(categories) == 0 {
		return true
	}
	return containsCategory(categories, entry.Category)
}

func (s *Store) matchesSourceFilter(entry *Entry, sources []SourceModel) bool {
	if len(sources) == 0 {
		return true
	}
	return containsSource(sources, entry.Source)
}

func (s *Store) matchesSessionFilter(entry *Entry, sessionIDs []string) bool {
	if len(sessionIDs) == 0 {
		return true
	}
	return containsString(sessionIDs, entry.SessionID)
}

func (s *Store) matchesIDFilter(entry *Entry, ids []string) bool {
	if len(ids) == 0 {
		return true
	}
	return containsString(ids, entry.ID)
}

func (s *Store) matchesDateRange(entry *Entry, since, until *time.Time) bool {
	if since != nil && entry.CreatedAt.Before(*since) {
		return false
	}
	if until != nil && entry.CreatedAt.After(*until) {
		return false
	}
	return true
}

func containsCategory(slice []Category, item Category) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func containsSource(slice []SourceModel, item SourceModel) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// generateID creates a unique identifier for stored items
func (s *Store) generateID(prefix string) string {
	return fmt.Sprintf("%s_%s", prefix, uuid.New().String()[:8])
}

// Helper functions

func removeFromSlice(slice []string, item string) []string {
	for i, s := range slice {
		if s == item {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
