package shared

import "time"

// CollabMsgType identifies the type of a collaboration message
// exchanged between co-tenant agents within a compound node.
type CollabMsgType int

const (
	// CollabImplementation is the primary agent's implementation output.
	CollabImplementation CollabMsgType = iota
	// CollabReview is a co-agent's review of the implementation.
	CollabReview
	// CollabPushback is a co-agent's request for revision.
	CollabPushback
	// CollabAcceptance is a co-agent's approval of the implementation.
	CollabAcceptance
	// CollabRevision is the primary agent's revised implementation.
	CollabRevision
)

// String returns the string representation of a CollabMsgType.
func (t CollabMsgType) String() string {
	switch t {
	case CollabImplementation:
		return "implementation"
	case CollabReview:
		return "review"
	case CollabPushback:
		return "pushback"
	case CollabAcceptance:
		return "acceptance"
	case CollabRevision:
		return "revision"
	default:
		return "unknown"
	}
}

// CollaborationMessage is a single message exchanged between co-tenant agents
// during an adversarial or sequential collaboration round.
type CollaborationMessage struct {
	FromAgent string         `json:"from_agent"`
	ToAgent   string         `json:"to_agent"`
	Type      CollabMsgType  `json:"type"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Round     int            `json:"round"`
	Timestamp time.Time      `json:"timestamp"`
}

// CollaborationRound captures all messages and the outcome of a single
// adversarial review round.
type CollaborationRound struct {
	Round    int                    `json:"round"`
	Messages []CollaborationMessage `json:"messages"`
	Outcome  CollabMsgType          `json:"outcome"`
}

// CollaborationRecord is the full record of a multi-round collaboration
// between co-tenant agents.
type CollaborationRecord struct {
	Rounds      []CollaborationRound `json:"rounds"`
	Consensus   bool                 `json:"consensus"`
	TotalRounds int                  `json:"total_rounds"`
}
