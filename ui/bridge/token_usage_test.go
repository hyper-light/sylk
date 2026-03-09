package bridge

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
)

func TestTokenUsageBridgeForwardHandlerAcceptsNumericVariants(t *testing.T) {
	b := NewTokenUsageBridge("test", nil)
	program := &recordingProgram{}
	handler := b.forwardHandler(program)

	evt := events.NewActivityEvent(events.EventTypeLLMResponse, "default", "ok")
	evt.AgentID = "engineer"
	evt.Outcome = events.OutcomeSuccess
	evt.Data["model"] = "gpt-5.4-pro"
	evt.Data["input_tokens"] = int64(1500)
	evt.Data["output_tokens"] = float64(275)
	evt.Data["cache_read_tokens"] = uint32(125)
	evt.Data["cache_write_tokens"] = float32(50)
	evt.Data["reasoning_tokens"] = uint16(25)

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
	if usage.AgentID != "engineer" {
		t.Fatalf("AgentID = %q, want engineer", usage.AgentID)
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
