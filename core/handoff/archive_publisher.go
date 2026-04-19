package handoff

import (
	"context"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
)

// HAND-05: async archival via bus.
//
// When a handoff completes, the outgoing agent's state is too
// expensive to persist inline on the handoff's critical path —
// serialization, WAL fsyncs, archive DB writes, and summary LLM calls
// can add seconds to what should be a sub-second transition. Instead,
// the handoff bridge hands the state to an ArchivePublisher, which
// publishes it on the bus for the Archivalist to consume at its own
// pace. The handoff returns immediately; archival happens in the
// background.
//
// The publisher is an interface so production code can use a
// ChannelBus-backed implementation while tests can use an in-memory
// stub that records calls.

// ArchiveReason names the lifecycle transition that triggered the
// archival request. Archivalist filters / routes on this.
type ArchiveReason string

const (
	// ArchiveReasonHandoffComplete fires after a successful standalone
	// or pipeline handoff: the outgoing agent's final state should be
	// recorded before the new instance takes over.
	ArchiveReasonHandoffComplete ArchiveReason = "handoff_complete"

	// ArchiveReasonTerminate fires when an agent is being terminated
	// without a handoff (session close, explicit shutdown). The state
	// still deserves archival — this is often the last chance.
	ArchiveReasonTerminate ArchiveReason = "terminate"

	// ArchiveReasonEvictionComplete fires when a knowledge agent
	// completes a tiered eviction pass — the evicted content should
	// be archived rather than simply dropped.
	ArchiveReasonEvictionComplete ArchiveReason = "eviction_complete"
)

// String returns the reason as a plain string for bus serialization.
func (r ArchiveReason) String() string {
	return string(r)
}

// ArchiveRequest is the typed payload the handoff bridge sends to the
// archivalist when an agent lifecycle transition warrants persistence.
//
// Identity and Task are typed primary keys — HAND-05 per
// docs/FIX_ID_AND_TOKENS.md §44 requires the archivalist to file by
// UID + TaskUID rather than free-form strings. When the source agent
// was not constructed via identity.Factory (legacy path), Identity
// may be nil; the archivalist falls back to AgentID + AgentType on
// State in that case.
type ArchiveRequest struct {
	Identity  *identity.AgentIdentity `json:"-"`
	Task      *identity.TaskRef       `json:"-"`
	State     *ArchivableState        `json:"state"`
	Reason    ArchiveReason           `json:"reason"`
	Timestamp time.Time               `json:"timestamp"`

	// SourceAgentID and AgentType are the flat identifiers lifted off
	// Identity and State for serialization. Duplicated here so consumers
	// that do not use identity.Factory still have stable string keys.
	SourceAgentID string `json:"source_agent_id"`
	AgentType     string `json:"agent_type,omitempty"`

	// SessionID enables the archivalist to route the state into the
	// correct per-session archive. Taken from the bridge's trace
	// metadata at publication time.
	SessionID string `json:"session_id,omitempty"`

	// CorrelationID threads the archive request back to the originating
	// user turn, so downstream recall can group all observations from a
	// single interaction. Optional — absent when the handoff happened
	// outside a tracked correlation scope.
	CorrelationID string `json:"correlation_id,omitempty"`
}

// ArchivePublisher is implemented by bus adapters that deliver
// ArchiveRequest instances to the Archivalist asynchronously. The
// contract is fire-and-forget: the publisher must return quickly and
// must not block the caller waiting for the Archivalist's response.
// A publisher may choose to buffer, drop-oldest, or fail-fast under
// backpressure, but it must not panic or leak goroutines.
type ArchivePublisher interface {
	PublishArchive(ctx context.Context, req *ArchiveRequest) error
}

// newArchiveRequestFromBridge builds an ArchiveRequest from the
// current bridge state. Returns nil when there is nothing worth
// archiving — specifically when the agent has no extractable state
// and no typed identity, which means the archivalist would have no
// key to file under. Pure: safe to call with any lock held because
// it only reads bridge fields through their own concurrency-safe
// accessors.
func newArchiveRequestFromBridge(
	agent HandoffableAgent,
	reason ArchiveReason,
	sessionID, correlationID string,
) *ArchiveRequest {
	if agent == nil {
		return nil
	}
	state := agent.ExtractArchivableState()
	if state == nil {
		return nil
	}
	return &ArchiveRequest{
		State:         state,
		Reason:        reason,
		Timestamp:     time.Now(),
		SourceAgentID: strings.TrimSpace(state.AgentID),
		AgentType:     strings.TrimSpace(state.AgentType),
		Identity:      state.Identity,
		Task:          state.Task,
		SessionID:     strings.TrimSpace(sessionID),
		CorrelationID: strings.TrimSpace(correlationID),
	}
}
