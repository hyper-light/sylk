package signal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type OperationState string

const (
	OperationPending    OperationState = "pending"
	OperationInProgress OperationState = "in_progress"
	OperationCompleted  OperationState = "completed"
)

type Operation struct {
	ID   string         `json:"id"`
	Type string         `json:"type"`
	Data map[string]any `json:"data,omitempty"`
}

type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	StartedAt time.Time      `json:"started_at"`
}

type Action struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Priority int            `json:"priority"`
	Data     map[string]any `json:"data,omitempty"`
}

type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type AgentCheckpoint struct {
	ID              string         `json:"id"`
	AgentID         string         `json:"agent_id"`
	AgentType       string         `json:"agent_type"`
	SessionID       string         `json:"session_id"`
	PipelineID      string         `json:"pipeline_id"`
	TaskID          string         `json:"task_id"`
	CreatedAt       time.Time      `json:"created_at"`
	LastOperation   *Operation     `json:"last_operation,omitempty"`
	OperationState  OperationState `json:"operation_state"`
	MessagesHash    string         `json:"messages_hash"`
	MessageCount    int            `json:"message_count"`
	ToolsInProgress []ToolCall     `json:"tools_in_progress,omitempty"`
	PendingActions  []Action       `json:"pending_actions,omitempty"`
	RecentMessages  []Message      `json:"recent_messages,omitempty"`
}

type CheckpointBuilder struct {
	checkpoint *AgentCheckpoint
}

func NewCheckpointBuilder(id, agentID, agentType string) *CheckpointBuilder {
	return &CheckpointBuilder{
		checkpoint: &AgentCheckpoint{
			ID:             id,
			AgentID:        agentID,
			AgentType:      agentType,
			CreatedAt:      time.Now(),
			OperationState: OperationPending,
		},
	}
}

func (b *CheckpointBuilder) WithSession(sessionID string) *CheckpointBuilder {
	b.checkpoint.SessionID = sessionID
	return b
}

func (b *CheckpointBuilder) WithPipeline(pipelineID string) *CheckpointBuilder {
	b.checkpoint.PipelineID = pipelineID
	return b
}

func (b *CheckpointBuilder) WithTask(taskID string) *CheckpointBuilder {
	b.checkpoint.TaskID = taskID
	return b
}

func (b *CheckpointBuilder) WithOperation(op *Operation, state OperationState) *CheckpointBuilder {
	b.checkpoint.LastOperation = op
	b.checkpoint.OperationState = state
	return b
}

func (b *CheckpointBuilder) WithTools(tools []ToolCall) *CheckpointBuilder {
	b.checkpoint.ToolsInProgress = tools
	return b
}

func (b *CheckpointBuilder) WithPendingActions(actions []Action) *CheckpointBuilder {
	b.checkpoint.PendingActions = actions
	return b
}

func (b *CheckpointBuilder) WithMessages(messages []Message) *CheckpointBuilder {
	b.checkpoint.RecentMessages = limitMessages(messages, 10)
	b.checkpoint.MessageCount = len(messages)
	b.checkpoint.MessagesHash = computeMessagesHash(messages)
	return b
}

func limitMessages(messages []Message, limit int) []Message {
	if len(messages) <= limit {
		return messages
	}
	return messages[len(messages)-limit:]
}

func computeMessagesHash(messages []Message) string {
	data, _ := json.Marshal(messages)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:8])
}

func (b *CheckpointBuilder) Build() *AgentCheckpoint {
	return b.checkpoint
}

func (c *AgentCheckpoint) HasInProgressTools() bool {
	return len(c.ToolsInProgress) > 0
}

func (c *AgentCheckpoint) HasPendingActions() bool {
	return len(c.PendingActions) > 0
}

func (c *AgentCheckpoint) IsOperationInProgress() bool {
	return c.OperationState == OperationInProgress
}

func (c *AgentCheckpoint) VerifyMessagesHash(currentMessages []Message) bool {
	currentHash := computeMessagesHash(currentMessages)
	return c.MessagesHash == currentHash
}
