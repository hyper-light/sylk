package session

import (
	"errors"
	"time"
)

// =============================================================================
// Session State
// =============================================================================

// State represents the lifecycle state of a session
type State int

const (
	// StateCreated indicates the session has been created but not started
	StateCreated State = iota
	// StateActive indicates the session is actively processing
	StateActive
	// StatePaused indicates the session is paused and can be resumed
	StatePaused
	// StateSuspended indicates the session is suspended to disk
	StateSuspended
	// StateCompleted indicates the session completed successfully
	StateCompleted
	// StateFailed indicates the session failed
	StateFailed
)

// String returns the string representation of a session state
func (s State) String() string {
	if name, ok := stateStrings()[s]; ok {
		return name
	}
	return "unknown"
}

type stateStringMap map[State]string

func stateStrings() stateStringMap {
	return stateStringMap{
		StateCreated:   "created",
		StateActive:    "active",
		StatePaused:    "paused",
		StateSuspended: "suspended",
		StateCompleted: "completed",
		StateFailed:    "failed",
	}
}

// IsTerminal returns true if the state is a terminal state
func (s State) IsTerminal() bool {
	return s == StateCompleted || s == StateFailed
}

// CanTransitionTo returns true if transitioning to the target state is valid
func (s State) CanTransitionTo(target State) bool {
	allowed := transitionRules()[s]
	return allowsTransition(allowed, target)
}

type stateTransitionTable map[State][]State

type stateSet map[State]struct{}

func transitionRules() stateTransitionTable {
	return stateTransitionTable{
		StateCreated:   {StateActive, StateFailed},
		StateActive:    {StatePaused, StateSuspended, StateCompleted, StateFailed},
		StatePaused:    {StateActive, StateSuspended, StateCompleted, StateFailed},
		StateSuspended: {StateActive, StateFailed},
		StateCompleted: {},
		StateFailed:    {},
	}
}

func allowsTransition(allowed []State, target State) bool {
	if len(allowed) == 0 {
		return false
	}
	return stateSetFromStates(allowed).has(target)
}

func stateSetFromStates(states []State) stateSet {
	set := make(stateSet, len(states))
	for _, state := range states {
		set[state] = struct{}{}
	}
	return set
}

func (s stateSet) has(state State) bool {
	_, ok := s[state]
	return ok
}

// =============================================================================
// Session Configuration
// =============================================================================

// Config holds configuration for creating a new session
type Config struct {
	// Name is a human-readable name for the session
	Name string

	// Description provides additional context about the session
	Description string

	// Branch is the git branch this session is associated with
	Branch string

	// MaxConcurrentTasks limits concurrent task execution
	MaxConcurrentTasks int

	// MaxEngineers limits the number of Engineer agents
	MaxEngineers int

	// Metadata holds arbitrary key-value pairs
	Metadata map[string]any

	// PersistenceEnabled enables saving session state to disk
	PersistenceEnabled bool

	// PersistencePath is the directory for session persistence
	PersistencePath string
}

// DefaultConfig returns a Config with sensible defaults
func DefaultConfig() Config {
	return Config{
		Name:               "default",
		MaxConcurrentTasks: 50,
		MaxEngineers:       20,
		Metadata:           make(map[string]any),
		PersistenceEnabled: true,
		PersistencePath:    ".sylk/sessions",
	}
}

// =============================================================================
// Session Statistics
// =============================================================================

// Stats contains statistics for a single session
type Stats struct {
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	State            string        `json:"state"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	ActiveDuration   time.Duration `json:"active_duration"`
	TasksCompleted   int64         `json:"tasks_completed"`
	TasksFailed      int64         `json:"tasks_failed"`
	MessagesRouted   int64         `json:"messages_routed"`
	EngineersSpawned int           `json:"engineers_spawned"`
}

// ManagerStats contains statistics for the session manager
type ManagerStats struct {
	TotalSessions     int     `json:"total_sessions"`
	ActiveSessions    int     `json:"active_sessions"`
	PausedSessions    int     `json:"paused_sessions"`
	CompletedSessions int     `json:"completed_sessions"`
	FailedSessions    int     `json:"failed_sessions"`
	MaxSessions       int     `json:"max_sessions"`
	Sessions          []Stats `json:"sessions,omitempty"`
}

// =============================================================================
// Events
// =============================================================================

// EventType represents the type of session event
type EventType int

const (
	EventCreated EventType = iota
	EventStarted
	EventPaused
	EventResumed
	EventSuspended
	EventRestored
	EventCompleted
	EventFailed
	EventClosed
	EventSwitched
)

// String returns the string representation of an event type
func (e EventType) String() string {
	return eventTypeStrings().name(e)
}

type eventTypeStringMap map[EventType]string

func eventTypeStrings() eventTypeStringMap {
	return eventTypeStringMap{
		EventCreated:   "created",
		EventStarted:   "started",
		EventPaused:    "paused",
		EventResumed:   "resumed",
		EventSuspended: "suspended",
		EventRestored:  "restored",
		EventCompleted: "completed",
		EventFailed:    "failed",
		EventClosed:    "closed",
		EventSwitched:  "switched",
	}
}

func (m eventTypeStringMap) name(event EventType) string {
	if name, ok := m[event]; ok {
		return name
	}
	return "unknown"
}

// Event represents a session lifecycle event
type Event struct {
	Type      EventType
	SessionID string
	Timestamp time.Time
	Data      map[string]any
}

// EventHandler is a callback for session events
type EventHandler func(event *Event)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrSessionNotFound indicates the session was not found
	ErrSessionNotFound = errors.New("session not found")

	// ErrSessionExists indicates a session with the same ID already exists
	ErrSessionExists = errors.New("session already exists")

	// ErrSessionClosed indicates the session is closed
	ErrSessionClosed = errors.New("session is closed")

	// ErrSessionNotActive indicates the session is not in active state
	ErrSessionNotActive = errors.New("session is not active")

	// ErrInvalidStateTransition indicates an invalid state transition
	ErrInvalidStateTransition = errors.New("invalid state transition")

	// ErrManagerClosed indicates the session manager is closed
	ErrManagerClosed = errors.New("session manager is closed")

	// ErrMaxSessionsReached indicates the maximum number of sessions is reached
	ErrMaxSessionsReached = errors.New("maximum sessions reached")

	// ErrNoActiveSession indicates there is no active session
	ErrNoActiveSession = errors.New("no active session")

	// ErrSerializationFailed indicates session serialization failed
	ErrSerializationFailed = errors.New("session serialization failed")

	// ErrDeserializationFailed indicates session deserialization failed
	ErrDeserializationFailed = errors.New("session deserialization failed")
)
