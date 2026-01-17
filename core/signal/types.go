package signal

import (
	"time"

	"github.com/google/uuid"
)

// Signal represents a workflow control signal type.
type Signal string

const (
	// PauseAll pauses all pipelines and agents.
	PauseAll Signal = "pause_all"
	// ResumeAll resumes all paused pipelines and agents.
	ResumeAll Signal = "resume_all"
	// PausePipeline pauses a specific pipeline.
	PausePipeline Signal = "pause_pipeline"
	// ResumePipeline resumes a specific pipeline.
	ResumePipeline Signal = "resume_pipeline"
	// CancelTask cancels a specific task.
	CancelTask Signal = "cancel_task"
	// AbortSession aborts an entire session.
	AbortSession Signal = "abort_session"
	// QuotaWarning indicates a quota limit is approaching.
	QuotaWarning Signal = "quota_warning"
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

// ValidSignals returns all valid signal types.
func ValidSignals() []Signal {
	return []Signal{
		PauseAll,
		ResumeAll,
		PausePipeline,
		ResumePipeline,
		CancelTask,
		AbortSession,
		QuotaWarning,
	}
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
