package session

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// =============================================================================
// Session Manager
// =============================================================================

// Manager manages the lifecycle of multiple sessions
type Manager struct {
	mu sync.RWMutex

	// Sessions stored in sharded map for better concurrency
	shards    []*sessionShard
	numShards int

	// Active session tracking
	activeID atomic.Pointer[string]

	// Configuration
	maxSessions int

	// Persistence
	persister Persister

	// Event handlers
	handlersMu sync.RWMutex
	handlers   []EventHandler

	// State
	closed atomic.Bool

	scope *concurrency.GoroutineScope

	// Statistics
	totalCreated   int64
	totalCompleted int64
	totalFailed    int64
}

// sessionShard holds a subset of sessions for reduced lock contention
type sessionShard struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// ManagerConfig configures the session manager
type ManagerConfig struct {
	// MaxSessions limits concurrent sessions (default: 100)
	MaxSessions int

	// NumShards controls sharding for concurrent access (default: 16)
	NumShards int

	// Persister handles session persistence (optional)
	Persister Persister

	Scope *concurrency.GoroutineScope
}

// DefaultManagerConfig returns default manager configuration
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		MaxSessions: 100,
		NumShards:   16,
	}
}

// NewManager creates a new session manager
func NewManager(cfg ManagerConfig) *Manager {
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 100
	}
	if cfg.NumShards <= 0 {
		cfg.NumShards = 16
	}

	shards := make([]*sessionShard, cfg.NumShards)
	for i := range shards {
		shards[i] = &sessionShard{
			sessions: make(map[string]*Session),
		}
	}

	return &Manager{
		shards:      shards,
		numShards:   cfg.NumShards,
		maxSessions: cfg.MaxSessions,
		persister:   cfg.Persister,
		handlers:    make([]EventHandler, 0),
		scope:       cfg.Scope,
	}
}

// getShard returns the shard for a given session ID
func (m *Manager) getShard(id string) *sessionShard {
	hash := fnv32(id)
	return m.shards[hash%uint32(m.numShards)]
}

// fnv32 computes a simple hash for sharding
func fnv32(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// =============================================================================
// Session Lifecycle
// =============================================================================

// Create creates a new session
func (m *Manager) Create(ctx context.Context, cfg Config) (*Session, error) {
	if m.closed.Load() {
		return nil, ErrManagerClosed
	}

	// Check session limit
	if m.Count() >= m.maxSessions {
		return nil, ErrMaxSessionsReached
	}

	session := NewSession(cfg)
	shard := m.getShard(session.ID())

	shard.mu.Lock()
	shard.sessions[session.ID()] = session
	shard.mu.Unlock()

	atomic.AddInt64(&m.totalCreated, 1)

	// Emit event
	m.emitEvent(&Event{
		Type:      EventCreated,
		SessionID: session.ID(),
		Timestamp: time.Now(),
		Data: map[string]any{
			"name":   session.Name(),
			"branch": session.Branch(),
		},
	})

	return session, nil
}

// Get retrieves a session by ID
func (m *Manager) Get(id string) (*Session, bool) {
	if m.closed.Load() {
		return nil, false
	}

	shard := m.getShard(id)
	shard.mu.RLock()
	session, ok := shard.sessions[id]
	shard.mu.RUnlock()

	return session, ok
}

// GetActive returns the currently active session
func (m *Manager) GetActive() (*Session, bool) {
	if m.closed.Load() {
		return nil, false
	}

	activeIDPtr := m.activeID.Load()
	if activeIDPtr == nil {
		return nil, false
	}

	return m.Get(*activeIDPtr)
}

// List returns all sessions
func (m *Manager) List() []*Session {
	if m.closed.Load() {
		return nil
	}

	var result []*Session

	for _, shard := range m.shards {
		shard.mu.RLock()
		for _, session := range shard.sessions {
			result = append(result, session)
		}
		shard.mu.RUnlock()
	}

	return result
}

// Count returns the number of sessions
func (m *Manager) Count() int {
	if m.closed.Load() {
		return 0
	}

	count := 0
	for _, shard := range m.shards {
		shard.mu.RLock()
		count += len(shard.sessions)
		shard.mu.RUnlock()
	}
	return count
}

// =============================================================================
// Session Control
// =============================================================================

// Switch switches to a different session
func (m *Manager) Switch(id string) error {
	if err := m.ensureOpen(); err != nil {
		return err
	}

	session, err := m.getSessionOrErr(id)
	if err != nil {
		return err
	}

	if err := m.switchToSession(id, session); err != nil {
		return err
	}

	m.setActiveSession(id)
	m.emitSwitchEvent(id)

	return nil
}

func (m *Manager) ensureOpen() error {
	if m.closed.Load() {
		return ErrManagerClosed
	}
	return nil
}

func (m *Manager) getSessionOrErr(id string) (*Session, error) {
	session, ok := m.Get(id)
	if !ok {
		return nil, ErrSessionNotFound
	}
	return session, nil
}

func (m *Manager) switchToSession(id string, session *Session) error {
	if err := m.pauseActiveSession(id); err != nil {
		return err
	}
	return m.activateSession(session)
}

func (m *Manager) pauseActiveSession(targetID string) error {
	current, ok := m.GetActive()
	if !ok || current.ID() == targetID {
		return nil
	}
	if err := current.Pause(); err != nil {
		return m.ignoreInvalidTransition(err)
	}
	return nil
}

func (m *Manager) activateSession(session *Session) error {
	if session.IsActive() {
		return nil
	}
	if !m.canActivateSession(session.State()) {
		return nil
	}
	if err := session.Start(); err != nil {
		return m.ignoreInvalidTransition(err)
	}
	return nil
}

func (m *Manager) canActivateSession(state State) bool {
	switch state {
	case StateCreated, StatePaused, StateSuspended:
		return true
	default:
		return false
	}
}

func (m *Manager) ignoreInvalidTransition(err error) error {
	if err == ErrInvalidStateTransition {
		return nil
	}
	return err
}

func (m *Manager) setActiveSession(id string) {
	idCopy := id
	m.activeID.Store(&idCopy)
}

func (m *Manager) emitSwitchEvent(id string) {
	m.emitEvent(&Event{
		Type:      EventSwitched,
		SessionID: id,
		Timestamp: time.Now(),
	})
}

// Pause pauses a session
func (m *Manager) Pause(id string) error {
	if err := m.ensureOpen(); err != nil {
		return err
	}

	session, err := m.getSessionOrErr(id)
	if err != nil {
		return err
	}

	if err := session.Pause(); err != nil {
		return err
	}

	m.persistSessionIfEnabled(session)
	m.emitPauseEvent(id)

	return nil
}

func (m *Manager) persistSessionIfEnabled(session *Session) {
	if m.persister == nil || !session.Config().PersistenceEnabled {
		return
	}
	_ = m.persister.Save(session)
}

func (m *Manager) emitPauseEvent(id string) {
	m.emitEvent(&Event{
		Type:      EventPaused,
		SessionID: id,
		Timestamp: time.Now(),
	})
}

// Resume resumes a paused session
func (m *Manager) Resume(id string) error {
	if m.closed.Load() {
		return ErrManagerClosed
	}

	session, ok := m.Get(id)
	if !ok {
		return ErrSessionNotFound
	}

	if err := session.Resume(); err != nil {
		return err
	}

	// Emit event
	m.emitEvent(&Event{
		Type:      EventResumed,
		SessionID: id,
		Timestamp: time.Now(),
	})

	return nil
}

// Suspend suspends a session to disk
func (m *Manager) Suspend(id string) error {
	if err := m.ensureOpen(); err != nil {
		return err
	}

	session, err := m.getSessionOrErr(id)
	if err != nil {
		return err
	}

	return m.suspendSession(session, id)
}

func (m *Manager) suspendSession(session *Session, id string) error {
	if err := session.Suspend(); err != nil {
		return err
	}
	if err := m.persistSession(session); err != nil {
		return err
	}

	m.emitSuspendEvent(id)
	return nil
}

func (m *Manager) persistSession(session *Session) error {
	if m.persister == nil {
		return nil
	}
	return m.persister.Save(session)
}

func (m *Manager) emitSuspendEvent(id string) {
	m.emitEvent(&Event{
		Type:      EventSuspended,
		SessionID: id,
		Timestamp: time.Now(),
	})
}

// Restore restores a suspended session
func (m *Manager) Restore(id string) error {
	if err := m.ensureOpen(); err != nil {
		return err
	}

	session, err := m.getOrLoadSession(id)
	if err != nil {
		return err
	}

	if err := session.Resume(); err != nil {
		return err
	}

	m.emitRestoreEvent(id)

	return nil
}

func (m *Manager) getOrLoadSession(id string) (*Session, error) {
	session, ok := m.Get(id)
	if ok {
		return session, nil
	}
	return m.loadSession(id)
}

func (m *Manager) loadSession(id string) (*Session, error) {
	if m.persister == nil {
		return nil, ErrSessionNotFound
	}

	loaded, err := m.persister.Load(id)
	if err != nil {
		return nil, ErrSessionNotFound
	}

	m.addSession(id, loaded)
	return loaded, nil
}

func (m *Manager) addSession(id string, session *Session) {
	shard := m.getShard(id)
	shard.mu.Lock()
	shard.sessions[id] = session
	shard.mu.Unlock()
}

func (m *Manager) emitRestoreEvent(id string) {
	m.emitEvent(&Event{
		Type:      EventRestored,
		SessionID: id,
		Timestamp: time.Now(),
	})
}

// Close closes and removes a session
func (m *Manager) Close(id string) error {
	if m.closed.Load() {
		return ErrManagerClosed
	}

	session, err := m.removeSession(id)
	if err != nil {
		return err
	}
	if err := m.closeSession(session); err != nil {
		return err
	}

	m.clearActiveIfMatches(id)
	m.updateCompletionStats(session)
	m.persistFinalState(session)
	m.emitClosedEvent(id)

	return nil
}

func (m *Manager) removeSession(id string) (*Session, error) {
	shard := m.getShard(id)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	session, ok := shard.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	delete(shard.sessions, id)
	return session, nil
}

func (m *Manager) closeSession(session *Session) error {
	if err := session.Close(); err != nil {
		if err != ErrSessionClosed {
			return err
		}
	}
	return nil
}

func (m *Manager) clearActiveIfMatches(id string) {
	activeIDPtr := m.activeID.Load()
	if activeIDPtr != nil && *activeIDPtr == id {
		m.activeID.Store(nil)
	}
}

func (m *Manager) updateCompletionStats(session *Session) {
	switch session.State() {
	case StateCompleted:
		atomic.AddInt64(&m.totalCompleted, 1)
	case StateFailed:
		atomic.AddInt64(&m.totalFailed, 1)
	}
}

func (m *Manager) persistFinalState(session *Session) {
	if m.persister != nil && session.Config().PersistenceEnabled {
		_ = m.persister.Save(session)
	}
}

func (m *Manager) emitClosedEvent(id string) {
	m.emitEvent(&Event{
		Type:      EventClosed,
		SessionID: id,
		Timestamp: time.Now(),
	})
}

// CloseAll closes all sessions
func (m *Manager) CloseAll() error {
	if m.closed.Load() {
		return ErrManagerClosed
	}

	sessions := m.List()
	var lastErr error

	for _, session := range sessions {
		if err := m.Close(session.ID()); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// Shutdown shuts down the session manager
func (m *Manager) Shutdown() error {
	if m.closed.Swap(true) {
		return ErrManagerClosed
	}

	sessions := m.collectSessions()
	m.persistSessions(sessions)
	lastErr := m.closeSessions(sessions)
	m.activeID.Store(nil)

	return lastErr
}

func (m *Manager) collectSessions() []*Session {
	var sessions []*Session
	for _, shard := range m.shards {
		shard.mu.RLock()
		for _, session := range shard.sessions {
			sessions = append(sessions, session)
		}
		shard.mu.RUnlock()
	}
	return sessions
}

func (m *Manager) persistSessions(sessions []*Session) {
	if m.persister == nil {
		return
	}
	for _, session := range sessions {
		if session.Config().PersistenceEnabled {
			_ = m.persister.Save(session)
		}
	}
}

func (m *Manager) closeSessions(sessions []*Session) error {
	var lastErr error
	for _, session := range sessions {
		m.discardSession(session)
		if err := session.Close(); err != nil && err != ErrSessionClosed {
			lastErr = err
		}
		m.emitClosedEvent(session.ID())
	}
	return lastErr
}

func (m *Manager) discardSession(session *Session) {
	shard := m.getShard(session.ID())
	shard.mu.Lock()
	delete(shard.sessions, session.ID())
	shard.mu.Unlock()
}

// =============================================================================
// Event Handling
// =============================================================================

// Subscribe registers an event handler
func (m *Manager) Subscribe(handler EventHandler) func() {
	m.handlersMu.Lock()
	m.handlers = append(m.handlers, handler)
	index := len(m.handlers) - 1
	m.handlersMu.Unlock()

	// Return unsubscribe function
	return func() {
		m.handlersMu.Lock()
		defer m.handlersMu.Unlock()
		if index < len(m.handlers) {
			m.handlers[index] = nil
		}
	}
}

// emitEvent emits an event to all handlers
func (m *Manager) emitEvent(event *Event) {
	m.handlersMu.RLock()
	handlers := make([]EventHandler, len(m.handlers))
	copy(handlers, m.handlers)
	m.handlersMu.RUnlock()

	for _, handler := range handlers {
		if handler != nil {
			m.dispatchHandler(handler, event)
		}
	}
}

func (m *Manager) dispatchHandler(handler EventHandler, event *Event) {
	if m.scope == nil {
		handler(event)
		return
	}

	_ = m.scope.Go("session.manager.event_handler", 5*time.Second, func(ctx context.Context) error {
		handler(event)
		return nil
	})
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns manager statistics
func (m *Manager) Stats() ManagerStats {
	if m.closed.Load() {
		return ManagerStats{}
	}

	sessions := m.List()

	stats := ManagerStats{
		TotalSessions: len(sessions),
		MaxSessions:   m.maxSessions,
		Sessions:      make([]Stats, 0, len(sessions)),
	}

	for _, session := range sessions {
		sessionStats := session.Stats()
		stats.Sessions = append(stats.Sessions, sessionStats)
		applySessionStateStats(&stats, session.State())
	}

	return stats
}

func applySessionStateStats(stats *ManagerStats, state State) {
	updaters := map[State]func(){
		StateActive:    func() { stats.ActiveSessions++ },
		StatePaused:    func() { stats.PausedSessions++ },
		StateCompleted: func() { stats.CompletedSessions++ },
		StateFailed:    func() { stats.FailedSessions++ },
	}
	if update, ok := updaters[state]; ok {
		update()
	}
}

// =============================================================================
// Persistence
// =============================================================================

// LoadSessions loads sessions from persistence
func (m *Manager) LoadSessions() error {
	if m.persister == nil {
		return nil
	}

	sessions, err := m.persister.LoadAll()
	if err != nil {
		return err
	}

	for _, session := range sessions {
		shard := m.getShard(session.ID())
		shard.mu.Lock()
		shard.sessions[session.ID()] = session
		shard.mu.Unlock()
	}

	return nil
}

// SetPersister sets the persister for the manager
func (m *Manager) SetPersister(p Persister) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.persister = p
}
