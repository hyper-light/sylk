package handoff

import (
	"context"
	"time"

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
	AgentType       string        `json:"agent_type"`
	ModelID         string        `json:"model_id"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	ContextWindow   int           `json:"context_window"`
	Category        AgentCategory `json:"category"`
}

// ArchivableState captures agent state for handoff persistence.
type ArchivableState struct {
	AgentID   string            `json:"agent_id"`
	AgentType string            `json:"agent_type"`
	State     map[string]string `json:"state,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
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

// ContextEvictable is implemented by knowledge agents that support
// tiered eviction. Instead of a full handoff, these agents can free
// context by evicting low-value entries from their working set.
type ContextEvictable interface {
	HandoffableAgent
	EvictEntries(candidates []EvictionCandidate) (freedTokens int, err error)
}

// TurnRecord captures metrics from a single agent turn.
type TurnRecord struct {
	InputTokens   int           `json:"input_tokens"`
	OutputTokens  int           `json:"output_tokens"`
	ContextSize   int           `json:"context_size"`
	ToolCalls     int           `json:"tool_calls"`
	ToolSuccesses int           `json:"tool_successes"`
	TurnNumber    int           `json:"turn_number"`
	Duration      time.Duration `json:"duration"`
	Timestamp     time.Time     `json:"timestamp"`

	// Provider response signals (omitempty for backward compat).
	StopReason       providers.StopReason    `json:"stop_reason,omitempty"`
	CacheReadTokens  int                     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int                     `json:"cache_write_tokens,omitempty"`

	// Stream metrics snapshot (nil when not available).
	StreamMetrics *providers.StreamMetrics `json:"stream_metrics,omitempty"`
}

// QualitySignal captures external quality feedback for an agent.
type QualitySignal struct {
	Source       string       `json:"source"`
	Score        float64      `json:"score"`
	FeedbackType FeedbackType `json:"feedback_type"`
	Timestamp    time.Time    `json:"timestamp"`
}
