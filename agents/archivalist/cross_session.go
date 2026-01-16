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
	var sessions []*Session

	for _, item := range results {
		if seen[item.SessionID] {
			continue
		}
		seen[item.SessionID] = true

		if c.archive != nil {
			session, err := c.archive.GetSession(item.SessionID)
			if err == nil && session != nil {
				sessions = append(sessions, session)
				continue
			}
		}

		if c.store != nil {
			current := c.store.GetCurrentSession()
			if current != nil && current.ID == item.SessionID {
				sessions = append(sessions, current)
			}
		}
	}

	return sessions, nil
}

func (c *CrossSessionIndex) GetSessionHistory(sessionID string, limit int) []*Event {
	if c.eventLog == nil {
		return nil
	}
	return c.eventLog.GetBySession(sessionID, limit)
}
