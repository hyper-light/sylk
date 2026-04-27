package handoff

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// AgentCategory classifies how an agent handles context exhaustion.
type AgentCategory int

const (
	// CategoryKnowledge agents use tiered eviction within the same instance.
	// They can free context by pruning low-value entries without requiring
	// a full handoff to a new agent instance.
	CategoryKnowledge AgentCategory = iota

	// CategoryStandalone agents perform a full handoff to a new instance.
	// The current agent is terminated and replaced with a fresh instance
	// that receives a prepared context snapshot.
	CategoryStandalone

	// CategoryPipeline agents perform handoff within their pipeline goroutine.
	// Similar to standalone but the lifecycle is managed by the pipeline
	// rather than by the supervisor directly.
	CategoryPipeline
)

// String returns the human-readable name of the category.
func (c AgentCategory) String() string {
	switch c {
	case CategoryKnowledge:
		return "knowledge"
	case CategoryStandalone:
		return "standalone"
	case CategoryPipeline:
		return "pipeline"
	default:
		return "unknown"
	}
}

// AgentDescriptor is immutable metadata about an agent type.
// It describes the agent's model, context window, and handoff category.
type AgentDescriptor struct {
	AgentType             string                    `json:"agent_type"`
	ModelID               string                    `json:"model_id"`
	ReasoningEffort       string                    `json:"reasoning_effort,omitempty"`
	ContextWindow         int                       `json:"context_window"`
	Category              AgentCategory             `json:"category"`
	RuntimeProfiles       []llmruntime.StageProfile `json:"runtime_profiles,omitempty"`
	DefaultRuntimeProfile string                    `json:"default_runtime_profile,omitempty"`
}

// ArchivableState captures agent state for handoff persistence.
//
// Identity and Task carry the typed primary key when the source
// agent was constructed through the identity.Factory (Step 5
// per-agent migration). Not JSON-serialized — persistence uses the
// flat AgentID + AgentType fields alongside a per-agent WAL that
// records the typed form (see core/llm/accounting/wal.go). Archivalist
// consumers key on Identity.UID() + Task.UID() when both are set.
type ArchivableState struct {
	AgentID   string                  `json:"agent_id"`
	AgentType string                  `json:"agent_type"`
	State     map[string]string       `json:"state,omitempty"`
	Timestamp time.Time               `json:"timestamp"`
	Identity  *identity.AgentIdentity `json:"-"`
	Task      *identity.TaskRef       `json:"-"`
}

// HandoffableAgent is the base interface for handoff participation.
// Every agent that participates in the handoff system must implement this.
type HandoffableAgent interface {
	AgentID() string
	AgentType() string
	Descriptor() AgentDescriptor
	ExtractArchivableState() *ArchivableState
	Terminate(ctx context.Context) error
}

// HandoffInjectable is implemented by standalone and pipeline agents
// that accept prepared context from a handoff. After handoff, the new
// agent instance receives the prepared context via InjectPreparedContext.
type HandoffInjectable interface {
	HandoffableAgent
	InjectPreparedContext(pc *PreparedContext) error
}

// HandoffBoardInjectable extends HandoffInjectable for agents that can
// hydrate their context from the claims board instead of PreparedContext.
type HandoffBoardInjectable interface {
	HandoffableAgent
	InjectFromBoard(board *claims.ClaimsBoard, claimIDs []string) error
}

// ContextEvictable is implemented by knowledge agents that support
// tiered eviction. Instead of a full handoff, these agents can free
// context by evicting low-value entries from their working set.
type ContextEvictable interface {
	HandoffableAgent
	EvictEntries(candidates []EvictionCandidate) (freedTokens int, err error)
}

// TurnRecord captures metrics from a single agent turn.
type TurnRecord struct {
	InputTokens    int           `json:"input_tokens"`
	OutputTokens   int           `json:"output_tokens"`
	ContextSize    int           `json:"context_size"`
	ToolCalls      int           `json:"tool_calls"`
	ToolSuccesses  int           `json:"tool_successes"`
	TurnNumber     int           `json:"turn_number"`
	Duration       time.Duration `json:"duration"`
	Timestamp      time.Time     `json:"timestamp"`
	Stage          string        `json:"stage,omitempty"`
	RuntimeProfile string        `json:"runtime_profile,omitempty"`

	// Provider response signals (omitempty for backward compat).
	StopReason       providers.StopReason `json:"stop_reason,omitempty"`
	CacheReadTokens  int                  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int                  `json:"cache_write_tokens,omitempty"`

	// Stream metrics snapshot (nil when not available).
	StreamMetrics *providers.StreamMetrics `json:"stream_metrics,omitempty"`

	// Per-turn logging metadata for bridge-side trace emission.
	CorrelationID string                       `json:"correlation_id,omitempty"`
	SessionID     string                       `json:"session_id,omitempty"`
	EventLogger   *agentlog.SessionEventLogger `json:"-"`
}

// QualitySignal captures external quality feedback for an agent.
type QualitySignal struct {
	Source       string       `json:"source"`
	Score        float64      `json:"score"`
	FeedbackType FeedbackType `json:"feedback_type"`
	Timestamp    time.Time    `json:"timestamp"`
}
