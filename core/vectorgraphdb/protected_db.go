package vectorgraphdb

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// ErrSessionNotFound is returned when an operation references a non-existent session.
var ErrSessionNotFound = errors.New("session not found")

// ErrSessionAlreadyExists is returned when creating a session that already exists.
var ErrSessionAlreadyExists = errors.New("session already exists")

// ProtectedVectorDB wraps VectorGraphDB with all protection mechanisms.
// It provides snapshot isolation, optimistic concurrency control, and
// integrity validation for safe concurrent access.
type ProtectedVectorDB struct {
	db          *VectorGraphDB
	vectorIndex VectorIndex
	snapshotMgr SnapshotManager
	nodeStore   *VersionedNodeStore
	validator   *IntegrityValidator
	config      ProtectionConfig
	sessions    sync.Map // sessionID -> *SessionScopedView
	cancelFunc  context.CancelFunc
	logger      *slog.Logger
	mu          sync.RWMutex
}

// ProtectedDBDeps holds the dependencies for creating a ProtectedVectorDB.
type ProtectedDBDeps struct {
	DB          *VectorGraphDB
	VectorIndex VectorIndex
	SnapshotMgr SnapshotManager
	NodeStore   *VersionedNodeStore
}

// NewProtectedVectorDB creates a new ProtectedVectorDB with the given dependencies.
func NewProtectedVectorDB(deps ProtectedDBDeps, cfg ProtectionConfig) (*ProtectedVectorDB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	nodeStore := deps.NodeStore
	if nodeStore == nil && deps.DB != nil {
		baseNodeStore := NewNodeStore(deps.DB, nil)
		nodeStore = NewVersionedNodeStore(baseNodeStore)
	}

	validator := NewIntegrityValidator(deps.DB, cfg.IntegrityConfig, slog.Default())

	return &ProtectedVectorDB{
		db:          deps.DB,
		vectorIndex: deps.VectorIndex,
		snapshotMgr: deps.SnapshotMgr,
		nodeStore:   nodeStore,
		validator:   validator,
		config:      cfg,
		logger:      slog.Default(),
	}, nil
}

// DB returns the underlying VectorGraphDB.
func (pdb *ProtectedVectorDB) DB() *VectorGraphDB {
	return pdb.db
}

// SnapshotManager returns the snapshot manager.
func (pdb *ProtectedVectorDB) SnapshotManager() SnapshotManager {
	return pdb.snapshotMgr
}

// NodeStore returns the versioned node store.
func (pdb *ProtectedVectorDB) NodeStore() *VersionedNodeStore {
	return pdb.nodeStore
}

// Validator returns the integrity validator.
func (pdb *ProtectedVectorDB) Validator() *IntegrityValidator {
	return pdb.validator
}

// Config returns the protection configuration.
func (pdb *ProtectedVectorDB) Config() ProtectionConfig {
	return pdb.config
}

// BeginSession creates a new session with the configured default isolation.
func (pdb *ProtectedVectorDB) BeginSession(sessionID string) (*SessionScopedView, error) {
	return pdb.BeginSessionWithIsolation(sessionID, pdb.config.DefaultIsolation)
}

// BeginSessionWithIsolation creates a session with specific isolation.
func (pdb *ProtectedVectorDB) BeginSessionWithIsolation(sessionID string, isolation IsolationLevel) (*SessionScopedView, error) {
	if _, exists := pdb.sessions.Load(sessionID); exists {
		return nil, ErrSessionAlreadyExists
	}

	view := pdb.createSessionView(sessionID, isolation)
	pdb.sessions.Store(sessionID, view)
	pdb.logSessionCreated(sessionID, isolation)

	return view, nil
}

// createSessionView creates a new SessionScopedView with the given parameters.
func (pdb *ProtectedVectorDB) createSessionView(sessionID string, isolation IsolationLevel) *SessionScopedView {
	return NewSessionScopedView(SessionViewConfig{
		SessionID:      sessionID,
		DB:             pdb.db,
		SnapshotMgr:    pdb.snapshotMgr,
		LiveIndex:      pdb.vectorIndex,
		Isolation:      isolation,
		VersionedStore: pdb.nodeStore,
	})
}

// logSessionCreated logs the creation of a new session.
func (pdb *ProtectedVectorDB) logSessionCreated(sessionID string, isolation IsolationLevel) {
	pdb.logger.Debug("session created",
		"session_id", sessionID,
		"isolation", isolation.String(),
	)
}

// GetSession returns an existing session by ID.
func (pdb *ProtectedVectorDB) GetSession(sessionID string) (*SessionScopedView, error) {
	val, exists := pdb.sessions.Load(sessionID)
	if !exists {
		return nil, ErrSessionNotFound
	}
	return val.(*SessionScopedView), nil
}

// EndSession closes and removes a session.
func (pdb *ProtectedVectorDB) EndSession(sessionID string) error {
	val, exists := pdb.sessions.LoadAndDelete(sessionID)
	if !exists {
		return ErrSessionNotFound
	}

	view := val.(*SessionScopedView)
	pdb.logger.Debug("session ended", "session_id", sessionID)
	return view.Close()
}

// SessionCount returns the number of active sessions.
func (pdb *ProtectedVectorDB) SessionCount() int {
	count := 0
	pdb.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// ValidateIntegrity runs integrity validation and returns the result.
func (pdb *ProtectedVectorDB) ValidateIntegrity() (*ValidationResult, error) {
	return pdb.validator.ValidateAll()
}

// ValidateAndRepair runs validation and attempts to repair any violations.
func (pdb *ProtectedVectorDB) ValidateAndRepair() (*ValidationResult, error) {
	return pdb.validator.ValidateAndRepair()
}

// Start begins background tasks (GC, periodic validation).
func (pdb *ProtectedVectorDB) Start(ctx context.Context) {
	pdb.mu.Lock()
	defer pdb.mu.Unlock()

	if pdb.cancelFunc != nil {
		return // already started
	}

	ctx, cancel := context.WithCancel(ctx)
	pdb.cancelFunc = cancel

	go pdb.snapshotMgr.GCLoop(ctx)
	go pdb.validator.PeriodicValidation(ctx)

	pdb.logger.Info("protected db started",
		"gc_interval", pdb.config.SnapshotGCInterval,
		"integrity_interval", pdb.config.IntegrityConfig.PeriodicInterval,
	)
}

// Close shuts down all background tasks and cleans up resources.
func (pdb *ProtectedVectorDB) Close() error {
	pdb.mu.Lock()
	defer pdb.mu.Unlock()

	pdb.stopBackgroundTasks()
	pdb.closeAllSessions()
	pdb.logger.Info("protected db closed")

	return pdb.db.Close()
}

// stopBackgroundTasks cancels any running background goroutines.
func (pdb *ProtectedVectorDB) stopBackgroundTasks() {
	if pdb.cancelFunc != nil {
		pdb.cancelFunc()
		pdb.cancelFunc = nil
	}
}

// closeAllSessions closes all active sessions.
func (pdb *ProtectedVectorDB) closeAllSessions() {
	pdb.sessions.Range(func(key, val any) bool {
		view := val.(*SessionScopedView)
		_ = view.Close()
		pdb.sessions.Delete(key)
		return true
	})
}

// SetLogger sets a custom logger for the protected DB.
func (pdb *ProtectedVectorDB) SetLogger(logger *slog.Logger) {
	pdb.mu.Lock()
	defer pdb.mu.Unlock()
	pdb.logger = logger
}
