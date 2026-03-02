package steering

import "encoding/json"

// StateSnapshotter is an optional interface for agents with significant
// derived state beyond req.Messages. Agents that don't implement it get
// message-only rollback (truncate req.Messages). The LLM re-derives
// agent state from the restored message history.
type StateSnapshotter interface {
	// SnapshotState serializes agent-specific state for checkpoint storage.
	// Returns nil if the agent has no meaningful state to snapshot.
	SnapshotState() (json.RawMessage, error)

	// RestoreState restores agent-specific state from a previous snapshot.
	RestoreState(json.RawMessage) error
}
