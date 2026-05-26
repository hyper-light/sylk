package claims

import (
	"fmt"
	"sort"
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

// ErrSessionBoardAlreadyRegistered is returned by Register when a
// board is already present for the given session ID. Callers that
// legitimately replace a board (e.g. replaying after a session
// restart) must use ReplaceForReason with an explicit reason
// string — silent overwrites previously masked a bug where the
// Guide's lazy board-create path overwrote the session manager's
// properly-wired board, dropping every InboxDelta on the floor.
var ErrSessionBoardAlreadyRegistered = fmt.Errorf("claims: session board already registered for this session")

// Register adds a session board to the registry. Returns
// ErrSessionBoardAlreadyRegistered if a board already exists for the
// given session ID — Register is strictly first-write-wins, and any
// caller that wants to replace must go through ReplaceForReason. This
// guard prevents the silent-overwrite class of bug where two
// independent code paths construct boards with the same ID and the
// later one shadows the earlier one's wiring.
//
// Empty session ID or nil board is a programming error and panics —
// these inputs cannot produce a usable registration and silently
// dropping them would mask the caller's mistake.
func (r *SessionBoardRegistry) Register(sessionID string, board *ClaimsBoard) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		panic("claims.SessionBoardRegistry.Register: empty session ID")
	}
	if board == nil {
		panic("claims.SessionBoardRegistry.Register: nil board")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.boards[sessionID]; exists {
		return ErrSessionBoardAlreadyRegistered
	}
	r.boards[sessionID] = board
	return nil
}

// ReplaceForReason atomically replaces an existing registration. The
// reason argument is required and surfaced to telemetry — it documents
// why a replacement is legitimate (e.g. session restart, manual
// intervention) so silent overwrites stay impossible by construction.
//
// Empty session ID, nil board, or empty reason are all programming
// errors and panic.
func (r *SessionBoardRegistry) ReplaceForReason(sessionID string, board *ClaimsBoard, reason string) {
	sessionID = strings.TrimSpace(sessionID)
	reason = strings.TrimSpace(reason)
	if sessionID == "" {
		panic("claims.SessionBoardRegistry.ReplaceForReason: empty session ID")
	}
	if board == nil {
		panic("claims.SessionBoardRegistry.ReplaceForReason: nil board")
	}
	if reason == "" {
		panic("claims.SessionBoardRegistry.ReplaceForReason: empty reason")
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

func (r *SessionBoardRegistry) Snapshot() map[string]*ClaimsBoard {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*ClaimsBoard, len(r.boards))
	for sessionID, board := range r.boards {
		out[sessionID] = board
	}
	return out
}

func (r *SessionBoardRegistry) SessionIDs() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.boards))
	for sessionID := range r.boards {
		ids = append(ids, sessionID)
	}
	sort.Strings(ids)
	return ids
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
