package session

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Session
// =============================================================================

// Session represents an isolated execution context for workflows
type Session struct {
	mu sync.RWMutex

	// Identity
	id          string
	name        string
	description string
	branch      string

	// Configuration
	config Config

	// State
	state     State
	createdAt time.Time
	updatedAt time.Time
	startedAt time.Time
	pausedAt  time.Time

	// Statistics (atomic for concurrent access)
	tasksCompleted   int64
	tasksFailed      int64
	messagesRouted   int64
	activeDuration   int64 // nanoseconds
	engineersSpawned int32

	// Metadata
	metadata map[string]any

	// Context data for serialization
	contextData map[string]any

	// Workflow state
	currentDAGID string
	currentPhase string
	progress     float64

	// Closed flag
	closed atomic.Bool
}

// sessionSnapshot is used for serialization
type sessionSnapshot struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	Branch           string         `json:"branch"`
	State            State          `json:"state"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	StartedAt        time.Time      `json:"started_at"`
	PausedAt         time.Time      `json:"paused_at"`
	TasksCompleted   int64          `json:"tasks_completed"`
	TasksFailed      int64          `json:"tasks_failed"`
	MessagesRouted   int64          `json:"messages_routed"`
	ActiveDuration   int64          `json:"active_duration_ns"`
	EngineersSpawned int32          `json:"engineers_spawned"`
	Metadata         map[string]any `json:"metadata"`
	ContextData      map[string]any `json:"context_data"`
	CurrentDAGID     string         `json:"current_dag_id"`
	CurrentPhase     string         `json:"current_phase"`
	Progress         float64        `json:"progress"`
	Config           Config         `json:"config"`
}

// NewSession creates a new session with the given configuration
func NewSession(cfg Config) *Session {
	now := time.Now()
	id := uuid.New().String()

	if cfg.Name == "" {
		cfg.Name = "session-" + id[:8]
	}

	metadata := cfg.Metadata
	if metadata == nil {
		metadata = make(map[string]any)
	}

	return &Session{
		id:          id,
		name:        cfg.Name,
		description: cfg.Description,
		branch:      cfg.Branch,
		config:      cfg,
		state:       StateCreated,
		createdAt:   now,
		updatedAt:   now,
		metadata:    metadata,
		contextData: make(map[string]any),
	}
}

// =============================================================================
// Identity
// =============================================================================

// ID returns the unique identifier for this session
func (s *Session) ID() string {
	return s.id
}

// Name returns the human-readable name
func (s *Session) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.name
}

// Description returns the session description
func (s *Session) Description() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.description
}

// Branch returns the git branch associated with this session
func (s *Session) Branch() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.branch
}

// =============================================================================
// State Management
// =============================================================================

// State returns the current session state
func (s *Session) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// SetState transitions the session to a new state
func (s *Session) SetState(newState State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed.Load() {
		return ErrSessionClosed
	}

	if !s.state.CanTransitionTo(newState) {
		return ErrInvalidStateTransition
	}

	oldState := s.state
	s.state = newState
	s.updatedAt = time.Now()

	s.trackActiveDuration(oldState, newState)
	s.recordStateTiming(oldState, newState)

	return nil
}

func (s *Session) trackActiveDuration(oldState, newState State) {
	if oldState == StateActive && newState != StateActive {
		duration := time.Since(s.startedAt)
		atomic.AddInt64(&s.activeDuration, int64(duration))
	}
}

func (s *Session) recordStateTiming(oldState, newState State) {
	switch newState {
	case StateActive:
		if oldState == StateCreated || oldState == StatePaused || oldState == StateSuspended {
			s.startedAt = time.Now()
		}
	case StatePaused:
		s.pausedAt = time.Now()
	}
}

// Start activates the session
func (s *Session) Start() error {
	return s.SetState(StateActive)
}

// Pause pauses the session
func (s *Session) Pause() error {
	return s.SetState(StatePaused)
}

// Resume resumes a paused session
func (s *Session) Resume() error {
	return s.SetState(StateActive)
}

// Suspend suspends the session to disk
func (s *Session) Suspend() error {
	return s.SetState(StateSuspended)
}

// Complete marks the session as completed
func (s *Session) Complete() error {
	return s.SetState(StateCompleted)
}

// Fail marks the session as failed
func (s *Session) Fail() error {
	return s.SetState(StateFailed)
}

// IsActive returns true if the session is active
func (s *Session) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state == StateActive
}

// IsClosed returns true if the session is closed
func (s *Session) IsClosed() bool {
	return s.closed.Load()
}

// Close closes the session
func (s *Session) Close() error {
	if s.closed.Swap(true) {
		return ErrSessionClosed
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Update active duration if currently active
	if s.state == StateActive {
		duration := time.Since(s.startedAt)
		atomic.AddInt64(&s.activeDuration, int64(duration))
	}

	s.updatedAt = time.Now()
	return nil
}

// =============================================================================
// Metadata
// =============================================================================

// Metadata returns a copy of the session metadata
func (s *Session) Metadata() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]any, len(s.metadata))
	for k, v := range s.metadata {
		result[k] = v
	}
	return result
}

// GetMetadata returns a single metadata value
func (s *Session) GetMetadata(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.metadata[key]
	return v, ok
}

// SetMetadata sets a metadata value
func (s *Session) SetMetadata(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metadata[key] = value
	s.updatedAt = time.Now()
}

// DeleteMetadata removes a metadata value
func (s *Session) DeleteMetadata(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.metadata, key)
	s.updatedAt = time.Now()
}

// =============================================================================
// Context Data
// =============================================================================

// GetContextData returns a context data value
func (s *Session) GetContextData(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.contextData[key]
	return v, ok
}

// SetContextData sets a context data value
func (s *Session) SetContextData(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contextData[key] = value
	s.updatedAt = time.Now()
}

// =============================================================================
// Workflow State
// =============================================================================

// SetWorkflowState updates the current workflow state
func (s *Session) SetWorkflowState(dagID, phase string, progress float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentDAGID = dagID
	s.currentPhase = phase
	s.progress = progress
	s.updatedAt = time.Now()
}

// WorkflowState returns the current workflow state
func (s *Session) WorkflowState() (dagID, phase string, progress float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentDAGID, s.currentPhase, s.progress
}

// =============================================================================
// Statistics
// =============================================================================

// IncrementTasksCompleted increments the completed tasks counter
func (s *Session) IncrementTasksCompleted() {
	atomic.AddInt64(&s.tasksCompleted, 1)
}

// IncrementTasksFailed increments the failed tasks counter
func (s *Session) IncrementTasksFailed() {
	atomic.AddInt64(&s.tasksFailed, 1)
}

// IncrementMessagesRouted increments the routed messages counter
func (s *Session) IncrementMessagesRouted() {
	atomic.AddInt64(&s.messagesRouted, 1)
}

// IncrementEngineersSpawned increments the spawned engineers counter
func (s *Session) IncrementEngineersSpawned() {
	atomic.AddInt32(&s.engineersSpawned, 1)
}

// Stats returns the session statistics
func (s *Session) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	activeDuration := time.Duration(atomic.LoadInt64(&s.activeDuration))
	if s.state == StateActive {
		activeDuration += time.Since(s.startedAt)
	}

	return Stats{
		ID:               s.id,
		Name:             s.name,
		State:            s.state.String(),
		CreatedAt:        s.createdAt,
		UpdatedAt:        s.updatedAt,
		ActiveDuration:   activeDuration,
		TasksCompleted:   atomic.LoadInt64(&s.tasksCompleted),
		TasksFailed:      atomic.LoadInt64(&s.tasksFailed),
		MessagesRouted:   atomic.LoadInt64(&s.messagesRouted),
		EngineersSpawned: int(atomic.LoadInt32(&s.engineersSpawned)),
	}
}

// Config returns the session configuration
func (s *Session) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

// CreatedAt returns the session creation time
func (s *Session) CreatedAt() time.Time {
	return s.createdAt
}

// UpdatedAt returns the last update time
func (s *Session) UpdatedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}

// =============================================================================
// Serialization
// =============================================================================

// Serialize converts the session to bytes for persistence
func (s *Session) Serialize() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := sessionSnapshot{
		ID:               s.id,
		Name:             s.name,
		Description:      s.description,
		Branch:           s.branch,
		State:            s.state,
		CreatedAt:        s.createdAt,
		UpdatedAt:        s.updatedAt,
		StartedAt:        s.startedAt,
		PausedAt:         s.pausedAt,
		TasksCompleted:   atomic.LoadInt64(&s.tasksCompleted),
		TasksFailed:      atomic.LoadInt64(&s.tasksFailed),
		MessagesRouted:   atomic.LoadInt64(&s.messagesRouted),
		ActiveDuration:   atomic.LoadInt64(&s.activeDuration),
		EngineersSpawned: atomic.LoadInt32(&s.engineersSpawned),
		Metadata:         s.metadata,
		ContextData:      s.contextData,
		CurrentDAGID:     s.currentDAGID,
		CurrentPhase:     s.currentPhase,
		Progress:         s.progress,
		Config:           s.config,
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, ErrSerializationFailed
	}
	return data, nil
}

// Restore restores session state from serialized data
func (s *Session) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var snapshot sessionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ErrDeserializationFailed
	}

	// Restore identity (ID is immutable, but others can be restored)
	s.name = snapshot.Name
	s.description = snapshot.Description
	s.branch = snapshot.Branch

	// Restore state
	s.state = snapshot.State
	s.createdAt = snapshot.CreatedAt
	s.updatedAt = snapshot.UpdatedAt
	s.startedAt = snapshot.StartedAt
	s.pausedAt = snapshot.PausedAt

	// Restore statistics
	atomic.StoreInt64(&s.tasksCompleted, snapshot.TasksCompleted)
	atomic.StoreInt64(&s.tasksFailed, snapshot.TasksFailed)
	atomic.StoreInt64(&s.messagesRouted, snapshot.MessagesRouted)
	atomic.StoreInt64(&s.activeDuration, snapshot.ActiveDuration)
	atomic.StoreInt32(&s.engineersSpawned, snapshot.EngineersSpawned)

	// Restore metadata and context
	s.metadata = snapshot.Metadata
	if s.metadata == nil {
		s.metadata = make(map[string]any)
	}
	s.contextData = snapshot.ContextData
	if s.contextData == nil {
		s.contextData = make(map[string]any)
	}

	// Restore workflow state
	s.currentDAGID = snapshot.CurrentDAGID
	s.currentPhase = snapshot.CurrentPhase
	s.progress = snapshot.Progress

	// Restore config
	s.config = snapshot.Config

	return nil
}

// DeserializeSession creates a new session from serialized data
func DeserializeSession(data []byte) (*Session, error) {
	var snapshot sessionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, ErrDeserializationFailed
	}

	metadata := snapshot.Metadata
	if metadata == nil {
		metadata = make(map[string]any)
	}

	contextData := snapshot.ContextData
	if contextData == nil {
		contextData = make(map[string]any)
	}

	session := &Session{
		id:               snapshot.ID,
		name:             snapshot.Name,
		description:      snapshot.Description,
		branch:           snapshot.Branch,
		config:           snapshot.Config,
		state:            snapshot.State,
		createdAt:        snapshot.CreatedAt,
		updatedAt:        snapshot.UpdatedAt,
		startedAt:        snapshot.StartedAt,
		pausedAt:         snapshot.PausedAt,
		tasksCompleted:   snapshot.TasksCompleted,
		tasksFailed:      snapshot.TasksFailed,
		messagesRouted:   snapshot.MessagesRouted,
		activeDuration:   snapshot.ActiveDuration,
		engineersSpawned: snapshot.EngineersSpawned,
		metadata:         metadata,
		contextData:      contextData,
		currentDAGID:     snapshot.CurrentDAGID,
		currentPhase:     snapshot.CurrentPhase,
		progress:         snapshot.Progress,
	}

	return session, nil
}
