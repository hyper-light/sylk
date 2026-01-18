package signal

import (
	"time"

	"github.com/google/uuid"
)

// Signal represents a workflow control signal type.
type Signal string

const (
	PauseAll       Signal = "pause_all"
	ResumeAll      Signal = "resume_all"
	PausePipeline  Signal = "pause_pipeline"
	ResumePipeline Signal = "resume_pipeline"
	CancelTask     Signal = "cancel_task"
	AbortSession   Signal = "abort_session"
	QuotaWarning   Signal = "quota_warning"
	StateChanged   Signal = "state_changed"

	// Memory pressure signals
	EvictCaches           Signal = "evict_caches"
	CompactContexts       Signal = "compact_contexts"
	MemoryPressureChanged Signal = "memory_pressure_changed"
)

// SignalMessage represents a signal sent through the bus.
type SignalMessage struct {
	ID          string
	Signal      Signal
	TargetID    string
	Reason      string
	Payload     any
	RequiresAck bool
	Timeout     time.Duration
	SentAt      time.Time
}

// PausePayload contains additional data for pause signals.
type PausePayload struct {
	Provider string
	ResumeAt *time.Time
	Attempt  int
	Message  string
}

// SignalAck represents an acknowledgment from a subscriber.
type SignalAck struct {
	SignalID     string
	SubscriberID string
	AgentID      string
	ReceivedAt   time.Time
	State        string
	Checkpoint   any
}

// SignalSubscriber represents an entity subscribed to signals.
type SignalSubscriber struct {
	ID      string
	AgentID string
	Signals []Signal
	Channel chan SignalMessage
}

func ValidSignals() []Signal {
	return []Signal{
		PauseAll,
		ResumeAll,
		PausePipeline,
		ResumePipeline,
		CancelTask,
		AbortSession,
		QuotaWarning,
		StateChanged,
		EvictCaches,
		CompactContexts,
		MemoryPressureChanged,
	}
}

type StateChangePayload struct {
	RunnerID  string
	FromState string
	ToState   string
	Error     error
}

type EvictCachesPayload struct {
	Percent     float64
	TargetBytes int64
	Reason      string
}

type CompactContextsPayload struct {
	TargetID string
	All      bool
	Reason   string
}

type MemoryPressurePayload struct {
	From      string
	To        string
	Usage     float64
	Timestamp time.Time
}

// NewSignalMessage creates a new signal message with defaults.
func NewSignalMessage(signal Signal, targetID string, requiresAck bool) *SignalMessage {
	return &SignalMessage{
		ID:          uuid.New().String(),
		Signal:      signal,
		TargetID:    targetID,
		RequiresAck: requiresAck,
		Timeout:     5 * time.Second,
		SentAt:      time.Now(),
	}
}
