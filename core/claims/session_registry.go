package claims

import (
	"strings"
	"sync"
)

// SessionBoardRegistry is a process-wide registry mapping session IDs
// to their claims boards. The Guide populates it when creating session
// boards; knowledge agents and other agents read from it to access the
// session board for their current request.
//
// Thread-safe. Bounded by session count (typically 1-3 active).
type SessionBoardRegistry struct {
	mu     sync.RWMutex
	boards map[string]*ClaimsBoard
}

var defaultRegistry = &SessionBoardRegistry{
	boards: make(map[string]*ClaimsBoard),
}

// DefaultSessionBoardRegistry returns the process-wide registry.
func DefaultSessionBoardRegistry() *SessionBoardRegistry {
	return defaultRegistry
}

// Register adds a session board to the registry.
func (r *SessionBoardRegistry) Register(sessionID string, board *ClaimsBoard) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || board == nil {
		return
	}
	r.mu.Lock()
	r.boards[sessionID] = board
	r.mu.Unlock()
}

// Lookup returns the board for the given session, or nil.
func (r *SessionBoardRegistry) Lookup(sessionID string) *ClaimsBoard {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	r.mu.RLock()
	board := r.boards[sessionID]
	r.mu.RUnlock()
	return board
}

// Remove removes a session board from the registry.
func (r *SessionBoardRegistry) Remove(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	delete(r.boards, sessionID)
	r.mu.Unlock()
}
