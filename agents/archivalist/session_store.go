package archivalist

import (
	"context"
)

type SessionStore struct {
	store     *Store
	archive   *Archive
	cache     *QueryCache
	sessionID string
}

type SessionSummary struct {
	Session *Session     `json:"session"`
	Stats   StorageStats `json:"stats"`
}

func NewSessionStore(store *Store, archive *Archive, cache *QueryCache, sessionID string) *SessionStore {
	return &SessionStore{
		store:     store,
		archive:   archive,
		cache:     cache,
		sessionID: sessionID,
	}
}

func (s *SessionStore) SessionID() string {
	return s.sessionID
}

func (s *SessionStore) StoreEntry(entry *Entry) (string, error) {
	return s.store.InsertEntryInSession(s.sessionID, entry)
}

func (s *SessionStore) Query(query ArchiveQuery) ([]*Entry, error) {
	query.SessionIDs = []string{s.sessionID}
	return s.store.Query(query)
}

func (s *SessionStore) SearchText(ctx context.Context, text string, includeArchived bool, limit int) ([]*Entry, error) {
	query := ArchiveQuery{
		SearchText:      text,
		Limit:           limit,
		IncludeArchived: includeArchived,
		SessionIDs:      []string{s.sessionID},
	}
	return s.store.Query(query)
}

func (s *SessionStore) EndSession(summary string, primaryFocus string) error {
	return s.store.EndSession(summary, primaryFocus)
}

func (s *SessionStore) Snapshot() *SessionSummary {
	stats := s.store.Stats()
	stats.CurrentSessionID = s.sessionID
	return &SessionSummary{
		Session: s.store.GetCurrentSession(),
		Stats:   stats,
	}
}

func (s *SessionStore) InvalidateCache() {
	if s.cache == nil {
		return
	}
	s.cache.InvalidateBySession(s.sessionID)
}

func (s *SessionStore) CacheStats() QueryCacheStats {
	if s.cache == nil {
		return QueryCacheStats{}
	}
	return s.cache.StatsBySession(s.sessionID)
}
