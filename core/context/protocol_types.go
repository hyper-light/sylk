// Package context provides types and utilities for context virtualization,
// inter-agent communication protocols, and context sharing mechanisms.
package context

import (
	"time"
)

// =============================================================================
// ContextPriority Enum
// =============================================================================

// ContextPriority represents the priority level for context-related requests.
// Higher values indicate more urgent requests that should be processed first.
type ContextPriority int

const (
	// ContextPriorityLow indicates background requests that can wait.
	ContextPriorityLow ContextPriority = 0

	// ContextPriorityNormal indicates standard priority requests.
	ContextPriorityNormal ContextPriority = 50

	// ContextPriorityHigh indicates time-sensitive requests.
	ContextPriorityHigh ContextPriority = 75

	// ContextPriorityCritical indicates blocking operations that need immediate attention.
	ContextPriorityCritical ContextPriority = 100
)

// contextPriorityNames maps ContextPriority values to their string representations.
var contextPriorityNames = map[ContextPriority]string{
	ContextPriorityLow:      "low",
	ContextPriorityNormal:   "normal",
	ContextPriorityHigh:     "high",
	ContextPriorityCritical: "critical",
}

// String returns the string representation of the priority level.
func (p ContextPriority) String() string {
	if name, ok := contextPriorityNames[p]; ok {
		return name
	}
	return "unknown"
}

// =============================================================================
// Context Share Protocol Types
// =============================================================================

// ContextShareRequest represents a request from one agent to share context with another.
type ContextShareRequest struct {
	// RequestID is a unique identifier for this request.
	RequestID string `json:"request_id"`

	// FromAgent is the ID of the requesting agent.
	FromAgent string `json:"from_agent"`

	// ToAgent is the ID of the target agent.
	ToAgent string `json:"to_agent"`

	// SessionID is the session scope for this request.
	SessionID string `json:"session_id"`

	// Query is the search query for retrieving relevant context.
	Query string `json:"query"`

	// MaxTokens is the maximum number of tokens to return.
	MaxTokens int `json:"max_tokens"`

	// Priority indicates the urgency of this request.
	Priority ContextPriority `json:"priority"`

	// ContentTypes filters the types of content to include.
	ContentTypes []ContentType `json:"content_types,omitempty"`

	// Deadline is the absolute time by which the response is needed.
	Deadline time.Time `json:"deadline"`
}

// ContextShareResponse represents the response to a context share request.
type ContextShareResponse struct {
	// RequestID matches the corresponding request.
	RequestID string `json:"request_id"`

	// Entries are the retrieved content entries.
	Entries []*ContentEntry `json:"entries"`

	// TotalTokens is the total token count of all entries.
	TotalTokens int `json:"total_tokens"`

	// Truncated indicates if the response was truncated to fit MaxTokens.
	Truncated bool `json:"truncated"`

	// Duration is how long the retrieval took.
	Duration time.Duration `json:"duration"`

	// Error contains any error message if the request failed.
	Error string `json:"error,omitempty"`
}

// =============================================================================
// Consultation Protocol Types
// =============================================================================

// ConsultationRequest represents a request from one agent to consult another.
// This bypasses the Guide for direct agent-to-agent communication.
type ConsultationRequest struct {
	// RequestID is a unique identifier for this consultation.
	RequestID string `json:"request_id"`

	// FromAgent is the ID of the requesting agent.
	FromAgent string `json:"from_agent"`

	// ToAgent is the ID of the target agent.
	ToAgent string `json:"to_agent"`

	// SessionID is the session scope for this consultation.
	SessionID string `json:"session_id"`

	// Query is the question or topic to consult about.
	Query string `json:"query"`

	// MaxTokens is the maximum number of tokens for the response.
	MaxTokens int `json:"max_tokens"`

	// Priority indicates the urgency of this consultation.
	Priority ContextPriority `json:"priority"`

	// Timeout is the maximum duration to wait for a response.
	Timeout time.Duration `json:"timeout"`
}

// ConsultationResponse represents the response to a consultation request.
type ConsultationResponse struct {
	// RequestID matches the corresponding request.
	RequestID string `json:"request_id"`

	// Answer is the response from the consulted agent.
	Answer string `json:"answer"`

	// Sources are the content IDs that were referenced for the answer.
	Sources []string `json:"sources"`

	// Confidence is the agent's confidence in the answer (0-1).
	Confidence float64 `json:"confidence"`

	// Duration is how long the consultation took.
	Duration time.Duration `json:"duration"`

	// Error contains any error message if the consultation failed.
	Error string `json:"error,omitempty"`
}

// =============================================================================
// Prefetch Share Protocol Types
// =============================================================================

// PrefetchSharePayload contains prefetched content from Guide to target agent.
type PrefetchSharePayload struct {
	// SessionID is the session this prefetch belongs to.
	SessionID string `json:"session_id"`

	// Query is the originating query that triggered the prefetch.
	Query string `json:"query"`

	// Excerpts are the full content excerpts (Tier A).
	Excerpts []Excerpt `json:"excerpts"`

	// Summaries are the one-line hints (Tier B).
	Summaries []Summary `json:"summaries"`

	// TokensUsed is the total tokens consumed by excerpts and summaries.
	TokensUsed int `json:"tokens_used"`

	// TierSource indicates which search tier produced the results.
	TierSource SearchTier `json:"tier_source"`

	// Timestamp is when this prefetch was generated.
	Timestamp time.Time `json:"timestamp"`
}

// =============================================================================
// Handoff Context Protocol Types
// =============================================================================

// ConversationTurn represents a single turn in a conversation history.
type ConversationTurn struct {
	// TurnNumber is the sequential turn number.
	TurnNumber int `json:"turn_number"`

	// Role is the participant role (e.g., "user", "assistant", "system").
	Role string `json:"role"`

	// Content is the message content for this turn.
	Content string `json:"content"`

	// TokenCount is the number of tokens in this turn.
	TokenCount int `json:"token_count"`

	// Timestamp is when this turn occurred.
	Timestamp time.Time `json:"timestamp"`
}

// FileState represents the state of a file being tracked during a session.
type FileState struct {
	// Path is the file path.
	Path string `json:"path"`

	// LastModified is the last modification time of the file.
	LastModified time.Time `json:"last_modified"`

	// TokenCount is the estimated token count for this file's content.
	TokenCount int `json:"token_count"`

	// Relevance is the relevance score to the current task (0-1).
	Relevance float64 `json:"relevance"`
}

// TaskState represents the current state of an in-progress task.
type TaskState struct {
	// TaskID is the unique identifier for this task.
	TaskID string `json:"task_id"`

	// Status is the current task status (e.g., "pending", "in_progress", "completed").
	Status string `json:"status"`

	// Progress is the completion percentage (0-100).
	Progress float64 `json:"progress"`

	// CurrentStep describes the current step being executed.
	CurrentStep string `json:"current_step"`

	// Dependencies are the IDs of tasks this task depends on.
	Dependencies []string `json:"dependencies"`
}

// Decision represents a key decision made during a session.
type Decision struct {
	// ID is the unique identifier for this decision.
	ID string `json:"id"`

	// Description is a brief description of what was decided.
	Description string `json:"description"`

	// Rationale explains why this decision was made.
	Rationale string `json:"rationale"`

	// Timestamp is when this decision was made.
	Timestamp time.Time `json:"timestamp"`

	// AgentID is the ID of the agent that made this decision.
	AgentID string `json:"agent_id"`
}

// HandoffContextPayload contains the context to transfer during agent handoff.
type HandoffContextPayload struct {
	// FromAgentID is the ID of the agent handing off.
	FromAgentID string `json:"from_agent_id"`

	// ToAgentID is the ID of the agent receiving the handoff.
	ToAgentID string `json:"to_agent_id"`

	// AgentType is the type of the agents involved in the handoff.
	AgentType string `json:"agent_type"`

	// SessionID is the session being handed off.
	SessionID string `json:"session_id"`

	// PreparedContextSize is the size in tokens of the prepared context.
	PreparedContextSize int `json:"prepared_context_size"`

	// Summary is a brief summary of the session state.
	Summary string `json:"summary"`

	// RecentTurns are the most recent conversation turns to preserve.
	RecentTurns []ConversationTurn `json:"recent_turns"`

	// ActiveFiles maps file paths to their current state.
	ActiveFiles map[string]FileState `json:"active_files"`

	// TaskState is the current state of any in-progress task.
	TaskState *TaskState `json:"task_state,omitempty"`

	// KeyDecisions are important decisions made during the session.
	KeyDecisions []Decision `json:"key_decisions"`

	// Timestamp is when this handoff payload was created.
	Timestamp time.Time `json:"timestamp"`
}
