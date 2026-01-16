package archivalist

type CrossSessionResult struct {
	SessionID string `json:"session_id"`
	Entry     *Entry `json:"entry"`
}

type CrossSessionIndex struct {
	store    *Store
	archive  *Archive
	eventLog *EventLog
}

func NewCrossSessionIndex(store *Store, archive *Archive, eventLog *EventLog) *CrossSessionIndex {
	return &CrossSessionIndex{
		store:    store,
		archive:  archive,
		eventLog: eventLog,
	}
}

func (c *CrossSessionIndex) QueryCrossSession(query ArchiveQuery) ([]CrossSessionResult, error) {
	results, err := c.store.Query(query)
	if err != nil {
		return nil, err
	}

	cross := make([]CrossSessionResult, 0, len(results))
	for _, entry := range results {
		cross = append(cross, CrossSessionResult{
			SessionID: entry.SessionID,
			Entry:     entry,
		})
	}

	return cross, nil
}

func (c *CrossSessionIndex) QuerySessions(query ArchiveQuery) ([]*Session, error) {
	results, err := c.QueryCrossSession(query)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	sessions := make([]*Session, 0, len(results))
	for _, item := range results {
		if seen[item.SessionID] {
			continue
		}
		seen[item.SessionID] = true

		session := c.sessionFromCrossResult(item)
		if session != nil {
			sessions = append(sessions, session)
		}
	}

	return sessions, nil
}

func (c *CrossSessionIndex) sessionFromCrossResult(item CrossSessionResult) *Session {
	if session := c.sessionFromArchive(item.SessionID); session != nil {
		return session
	}
	if session := c.sessionFromStore(item); session != nil {
		return session
	}
	return nil
}

func (c *CrossSessionIndex) sessionFromArchive(sessionID string) *Session {
	if c.archive == nil {
		return nil
	}
	session, err := c.archive.GetSession(sessionID)
	if err != nil {
		return nil
	}
	return session
}

func (c *CrossSessionIndex) sessionFromStore(item CrossSessionResult) *Session {
	if c.store == nil {
		return nil
	}
	if current := c.store.GetCurrentSession(); current != nil && current.ID == item.SessionID {
		return current
	}
	if entry, ok := c.store.GetEntry(item.Entry.ID); ok && entry != nil {
		return &Session{ID: item.SessionID}
	}
	return nil
}

func (c *CrossSessionIndex) GetSessionHistory(sessionID string, limit int) []*Event {
	if c.eventLog == nil {
		return nil
	}
	return c.eventLog.GetBySession(sessionID, limit)
}
