// Package search provides document types and search functionality for the Sylk system.
package search

import (
	"errors"
	"strings"
)

// StopReason represents why an LLM response ended.
type StopReason string

const (
	// StopReasonEndTurn indicates the model naturally completed its response.
	StopReasonEndTurn StopReason = "end_turn"
	// StopReasonMaxTokens indicates the response hit the token limit.
	StopReasonMaxTokens StopReason = "max_tokens"
	// StopReasonStopSequence indicates a stop sequence was encountered.
	StopReasonStopSequence StopReason = "stop_sequence"
	// StopReasonToolUse indicates the model requested to use a tool.
	StopReasonToolUse StopReason = "tool_use"
)

// validStopReasons contains all valid StopReason values for validation.
var validStopReasons = map[StopReason]bool{
	StopReasonEndTurn:      true,
	StopReasonMaxTokens:    true,
	StopReasonStopSequence: true,
	StopReasonToolUse:      true,
}

// IsValid returns true if the StopReason is a recognized value.
func (s StopReason) IsValid() bool {
	return validStopReasons[s]
}

// String returns the string representation of the StopReason.
func (s StopReason) String() string {
	return string(s)
}

// CodeBlock represents extracted code from an LLM response.
type CodeBlock struct {
	// Language is the programming language of the code block (e.g., "go", "python").
	Language string `json:"language"`
	// Content is the actual code content.
	Content string `json:"content"`
	// LineNum is the line number in the response where the block starts.
	LineNum int `json:"line_num"`
}

// LLMPromptDocument represents an indexed LLM prompt for search.
type LLMPromptDocument struct {
	Document

	// SessionID identifies the session this prompt belongs to.
	SessionID string `json:"session_id"`
	// AgentID identifies the agent that generated this prompt.
	AgentID string `json:"agent_id"`
	// AgentType categorizes the agent (e.g., "Engineer", "Designer", "Reviewer").
	AgentType string `json:"agent_type"`
	// Model is the LLM model used (e.g., "claude-3-opus").
	Model string `json:"model"`
	// TokenCount is the number of tokens in the prompt.
	TokenCount int `json:"token_count"`
	// TurnNumber is the conversation turn number.
	TurnNumber int `json:"turn_number"`

	// PrecedingFiles lists files referenced before this prompt.
	PrecedingFiles []string `json:"preceding_files,omitempty"`
	// FollowingFiles lists files referenced after this prompt.
	FollowingFiles []string `json:"following_files,omitempty"`
}

// Validate checks that the LLMPromptDocument has all required fields.
func (d *LLMPromptDocument) Validate() error {
	if d.SessionID == "" {
		return errors.New("session_id is required")
	}
	if d.AgentID == "" {
		return errors.New("agent_id is required")
	}
	if d.Model == "" {
		return errors.New("model is required")
	}
	if d.TokenCount < 0 {
		return errors.New("token_count must be non-negative")
	}
	if d.TurnNumber < 0 {
		return errors.New("turn_number must be non-negative")
	}
	return nil
}

// LLMResponseDocument represents an indexed LLM response for search.
type LLMResponseDocument struct {
	Document

	// PromptID links this response to its prompt.
	PromptID string `json:"prompt_id"`
	// SessionID identifies the session this response belongs to.
	SessionID string `json:"session_id"`
	// AgentID identifies the agent that received this response.
	AgentID string `json:"agent_id"`
	// Model is the LLM model that generated the response.
	Model string `json:"model"`
	// TokenCount is the number of tokens in the response.
	TokenCount int `json:"token_count"`
	// LatencyMs is the response latency in milliseconds.
	LatencyMs int64 `json:"latency_ms"`
	// StopReason indicates why the response ended.
	StopReason StopReason `json:"stop_reason"`

	// CodeBlocks contains code snippets extracted from the response.
	CodeBlocks []CodeBlock `json:"code_blocks,omitempty"`
	// FilesModified lists files that were modified as a result of this response.
	FilesModified []string `json:"files_modified,omitempty"`
	// ToolsUsed lists tools that were invoked during this response.
	ToolsUsed []string `json:"tools_used,omitempty"`
}

// Validate checks that the LLMResponseDocument has all required fields.
func (d *LLMResponseDocument) Validate() error {
	if d.PromptID == "" {
		return errors.New("prompt_id is required")
	}
	if d.SessionID == "" {
		return errors.New("session_id is required")
	}
	if d.AgentID == "" {
		return errors.New("agent_id is required")
	}
	if d.Model == "" {
		return errors.New("model is required")
	}
	if d.TokenCount < 0 {
		return errors.New("token_count must be non-negative")
	}
	if d.LatencyMs < 0 {
		return errors.New("latency_ms must be non-negative")
	}
	if d.StopReason != "" && !d.StopReason.IsValid() {
		return errors.New("invalid stop_reason")
	}
	return nil
}

// HasCodeBlocks returns true if the response contains any code blocks.
func (d *LLMResponseDocument) HasCodeBlocks() bool {
	return len(d.CodeBlocks) > 0
}

// GetCodeBlocksByLanguage returns all code blocks matching the specified language.
// The language comparison is case-insensitive.
func (d *LLMResponseDocument) GetCodeBlocksByLanguage(lang string) []CodeBlock {
	if len(d.CodeBlocks) == 0 {
		return nil
	}

	langLower := strings.ToLower(lang)
	var result []CodeBlock
	for _, block := range d.CodeBlocks {
		if strings.ToLower(block.Language) == langLower {
			result = append(result, block)
		}
	}
	return result
}
