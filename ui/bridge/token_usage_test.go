package bridge

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
)

// TestTokenUsageBridge_StopIsIdempotent locks the UI-12 invariant: Stop
// can be called multiple times without a panic. The atomic stopped flag
// replaces the former sync.Once; a regression to naive close(done) would
// double-close and crash.
func TestTokenUsageBridge_StopIsIdempotent(t *testing.T) {
	b := NewTokenUsageBridge("test", nil, nil)
	b.Stop()
	b.Stop()
}

func TestTokenUsageBridgeForwardHandlerAcceptsNumericVariants(t *testing.T) {
	b := NewTokenUsageBridge("test", nil, nil)
	program := &recordingProgram{}
	handler := b.forwardHandler(program)

	evt := events.NewActivityEvent(events.EventTypeLLMResponse, "default", "ok")
	evt.AgentID = "engineer"
	evt.CorrelationID = "corr-1"
	evt.Outcome = events.OutcomeSuccess
	evt.Data["model"] = "gpt-5.4-pro"
	evt.Data["input_tokens"] = int64(1500)
	evt.Data["output_tokens"] = float64(275)
	evt.Data["cache_read_tokens"] = uint32(125)
	evt.Data["cache_write_tokens"] = float32(50)
	evt.Data["reasoning_tokens"] = uint16(25)
	evt.Data["runtime_agent_id"] = "engineer-runtime-1"
	evt.Data["agent_type"] = "engineer"
	evt.Data["pipeline_id"] = "task_auth_checkout"
	evt.Data["task_id"] = "task_auth_checkout"

	if err := handler(guide.NewActivityMessage("engineer", evt)); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if len(program.messages) != 1 {
		t.Fatalf("sent %d messages, want 1", len(program.messages))
	}

	usage, ok := program.messages[0].(msg.TokenUsageMsg)
	if !ok {
		t.Fatalf("sent message type %T, want msg.TokenUsageMsg", program.messages[0])
	}
	if usage.AgentID != "task_auth_checkout:engineer" {
		t.Fatalf("AgentID = %q, want task_auth_checkout:engineer", usage.AgentID)
	}
	if usage.RuntimeAgentID != "engineer-runtime-1" {
		t.Fatalf("RuntimeAgentID = %q, want engineer-runtime-1", usage.RuntimeAgentID)
	}
	if usage.AgentType != "engineer" {
		t.Fatalf("AgentType = %q, want engineer", usage.AgentType)
	}
	if usage.PipelineID != "task_auth_checkout" {
		t.Fatalf("PipelineID = %q, want task_auth_checkout", usage.PipelineID)
	}
	if usage.TaskID != "task_auth_checkout" {
		t.Fatalf("TaskID = %q, want task_auth_checkout", usage.TaskID)
	}
	if usage.CorrelationID != "corr-1" {
		t.Fatalf("CorrelationID = %q, want corr-1", usage.CorrelationID)
	}
	if usage.Model != "gpt-5.4-pro" {
		t.Fatalf("Model = %q, want gpt-5.4-pro", usage.Model)
	}
	if usage.InputTokens != 1500 {
		t.Fatalf("InputTokens = %d, want 1500", usage.InputTokens)
	}
	if usage.OutputTokens != 275 {
		t.Fatalf("OutputTokens = %d, want 275", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 125 {
		t.Fatalf("CacheReadTokens = %d, want 125", usage.CacheReadTokens)
	}
	if usage.CacheWriteTokens != 50 {
		t.Fatalf("CacheWriteTokens = %d, want 50", usage.CacheWriteTokens)
	}
	if usage.ReasoningTokens != 25 {
		t.Fatalf("ReasoningTokens = %d, want 25", usage.ReasoningTokens)
	}
}

func TestTokenUsageBridgeForwardHandler_CanonicalizesReplicaAgentIdentity(t *testing.T) {
	b := NewTokenUsageBridge("test", nil, nil)
	program := &recordingProgram{}
	handler := b.forwardHandler(program)

	evt := events.NewActivityEvent(events.EventTypeLLMResponse, "default", "ok")
	evt.AgentID = "academic#replica-corr-1"
	evt.CorrelationID = "corr-2"
	evt.Outcome = events.OutcomeSuccess
	evt.Data["input_tokens"] = 300
	evt.Data["output_tokens"] = 120
	evt.Data["runtime_agent_id"] = "academic#replica-corr-1"
	evt.Data["canonical_agent_id"] = "academic"
	evt.Data["agent_type"] = "academic"

	if err := handler(guide.NewActivityMessage("academic#replica-corr-1", evt)); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if len(program.messages) != 1 {
		t.Fatalf("sent %d messages, want 1", len(program.messages))
	}

	usage, ok := program.messages[0].(msg.TokenUsageMsg)
	if !ok {
		t.Fatalf("sent message type %T, want msg.TokenUsageMsg", program.messages[0])
	}
	if usage.AgentID != "academic" {
		t.Fatalf("AgentID = %q, want academic", usage.AgentID)
	}
	if usage.RuntimeAgentID != "academic#replica-corr-1" {
		t.Fatalf("RuntimeAgentID = %q, want academic#replica-corr-1", usage.RuntimeAgentID)
	}
}
