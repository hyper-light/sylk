package session

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
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
	if m.closed.Load() {
		return ErrManagerClosed
	}

	// Get target session
	session, ok := m.Get(id)
	if !ok {
		return ErrSessionNotFound
	}

	// Pause current active session if any
	if currentActive, ok := m.GetActive(); ok && currentActive.ID() != id {
		if err := currentActive.Pause(); err != nil {
			// Ignore invalid state transitions when pausing
			if err != ErrInvalidStateTransition {
				return err
			}
		}
	}

	// Activate target session if not already active
	if !session.IsActive() {
		state := session.State()
		if state == StateCreated || state == StatePaused || state == StateSuspended {
			if err := session.Start(); err != nil {
				if err != ErrInvalidStateTransition {
					return err
				}
			}
		}
	}

	// Update active session
	m.activeID.Store(&id)

	// Emit event
	m.emitEvent(&Event{
		Type:      EventSwitched,
		SessionID: id,
		Timestamp: time.Now(),
	})

	return nil
}

// Pause pauses a session
func (m *Manager) Pause(id string) error {
	if m.closed.Load() {
		return ErrManagerClosed
	}

	session, ok := m.Get(id)
	if !ok {
		return ErrSessionNotFound
	}

	if err := session.Pause(); err != nil {
		return err
	}

	// Persist if enabled
	if m.persister != nil && session.Config().PersistenceEnabled {
		if err := m.persister.Save(session); err != nil {
			// Log but don't fail
		}
	}

	// Emit event
	m.emitEvent(&Event{
		Type:      EventPaused,
		SessionID: id,
		Timestamp: time.Now(),
	})

	return nil
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
	if m.closed.Load() {
		return ErrManagerClosed
	}

	session, ok := m.Get(id)
	if !ok {
		return ErrSessionNotFound
	}

	if err := session.Suspend(); err != nil {
		return err
	}

	// Persist session state
	if m.persister != nil {
		if err := m.persister.Save(session); err != nil {
			return err
		}
	}

	// Emit event
	m.emitEvent(&Event{
		Type:      EventSuspended,
		SessionID: id,
		Timestamp: time.Now(),
	})

	return nil
}

// Restore restores a suspended session
func (m *Manager) Restore(id string) error {
	if m.closed.Load() {
		return ErrManagerClosed
	}

	session, ok := m.Get(id)
	if !ok {
		// Try to load from persistence
		if m.persister != nil {
			loaded, err := m.persister.Load(id)
			if err != nil {
				return ErrSessionNotFound
			}
			session = loaded

			// Add to manager
			shard := m.getShard(id)
			shard.mu.Lock()
			shard.sessions[id] = session
			shard.mu.Unlock()
		} else {
			return ErrSessionNotFound
		}
	}

	if err := session.Resume(); err != nil {
		return err
	}

	// Emit event
	m.emitEvent(&Event{
		Type:      EventRestored,
		SessionID: id,
		Timestamp: time.Now(),
	})

	return nil
}

// Close closes and removes a session
func (m *Manager) Close(id string) error {
	if m.closed.Load() {
		return ErrManagerClosed
	}

	shard := m.getShard(id)

	shard.mu.Lock()
	session, ok := shard.sessions[id]
	if !ok {
		shard.mu.Unlock()
		return ErrSessionNotFound
	}
	delete(shard.sessions, id)
	shard.mu.Unlock()

	// Close the session
	if err := session.Close(); err != nil {
		// Ignore already closed
		if err != ErrSessionClosed {
			return err
		}
	}

	// Update active session if needed
	activeIDPtr := m.activeID.Load()
	if activeIDPtr != nil && *activeIDPtr == id {
		m.activeID.Store(nil)
	}

	// Update statistics
	if session.State() == StateCompleted {
		atomic.AddInt64(&m.totalCompleted, 1)
	} else if session.State() == StateFailed {
		atomic.AddInt64(&m.totalFailed, 1)
	}

	// Persist final state if enabled
	if m.persister != nil && session.Config().PersistenceEnabled {
		_ = m.persister.Save(session)
	}

	// Emit event
	m.emitEvent(&Event{
		Type:      EventClosed,
		SessionID: id,
		Timestamp: time.Now(),
	})

	return nil
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

	// Get all sessions before we start closing
	var sessions []*Session
	for _, shard := range m.shards {
		shard.mu.RLock()
		for _, session := range shard.sessions {
			sessions = append(sessions, session)
		}
		shard.mu.RUnlock()
	}

	// Save all sessions before closing
	if m.persister != nil {
		for _, session := range sessions {
			if session.Config().PersistenceEnabled {
				_ = m.persister.Save(session)
			}
		}
	}

	// Close all sessions directly (bypass closed check)
	var lastErr error
	for _, session := range sessions {
		shard := m.getShard(session.ID())

		shard.mu.Lock()
		delete(shard.sessions, session.ID())
		shard.mu.Unlock()

		if err := session.Close(); err != nil && err != ErrSessionClosed {
			lastErr = err
		}

		m.emitEvent(&Event{
			Type:      EventClosed,
			SessionID: session.ID(),
			Timestamp: time.Now(),
		})
	}

	// Clear active session
	m.activeID.Store(nil)

	return lastErr
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
			go handler(event)
		}
	}
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

		switch session.State() {
		case StateActive:
			stats.ActiveSessions++
		case StatePaused:
			stats.PausedSessions++
		case StateCompleted:
			stats.CompletedSessions++
		case StateFailed:
			stats.FailedSessions++
		}
	}

	return stats
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
