package steering

import (
	"encoding/json"
	"time"
)

// mailboxCapacity is the maximum pending commands in a SteeringMailbox.
// Derived from ChannelBus max concurrent handlers (32) — a user cannot
// produce more than 16 commands between two checkpoint drains.
const mailboxCapacity = 16

// maxCheckpoints is the maximum retained checkpoints per request.
// Derived from architect's MaxToolRuns (30) + headroom.
const maxCheckpoints = 32

// Pace controls tool loop execution speed.
type Pace int

const (
	// PaceAuto runs the tool loop without pauses (default).
	PaceAuto Pace = iota
	// PaceStep pauses after every checkpoint, requiring explicit resume.
	PaceStep
	// PacePaused halts the tool loop until explicitly resumed.
	PacePaused
)

func (p Pace) String() string {
	switch p {
	case PaceAuto:
		return "auto"
	case PaceStep:
		return "step"
	case PacePaused:
		return "paused"
	default:
		return "auto"
	}
}

// CommandType identifies the kind of steering command.
type CommandType int

const (
	CommandSteer   CommandType = iota // Append user message at next checkpoint.
	CommandEdit                      // Roll back, replace message, replay.
	CommandRollback                  // Roll back to specified checkpoint.
	CommandPace                      // Change execution pace.
	CommandResume                    // Unblock a paused/stepped agent.
)

func (ct CommandType) String() string {
	switch ct {
	case CommandSteer:
		return "steer"
	case CommandEdit:
		return "edit"
	case CommandRollback:
		return "rollback"
	case CommandPace:
		return "pace"
	case CommandResume:
		return "resume"
	default:
		return "steer"
	}
}

// Command is a user-issued steering instruction deposited into the mailbox.
type Command struct {
	Type         CommandType     `json:"type"`
	Text         string          `json:"text,omitempty"`          // For Steer: message content.
	CheckpointID string          `json:"checkpoint_id,omitempty"` // For Rollback/Edit: target checkpoint.
	MessageIndex int             `json:"message_index,omitempty"` // For Edit: index of message to replace.
	NewText      string          `json:"new_text,omitempty"`      // For Edit: replacement text.
	Pace         Pace            `json:"pace,omitempty"`          // For Pace: new pace value.
	Timestamp    time.Time       `json:"timestamp"`
	Data         json.RawMessage `json:"data,omitempty"` // Extension data.
}

// Checkpoint is a lightweight snapshot taken at each tool loop turn boundary.
type Checkpoint struct {
	ID           string          `json:"id"`
	Turn         int             `json:"turn"`
	MessageCount int             `json:"message_count"` // len(req.Messages) at this boundary.
	AgentState   json.RawMessage `json:"agent_state,omitempty"`
	Phase        string          `json:"phase"`
	Timestamp    time.Time       `json:"timestamp"`
}
