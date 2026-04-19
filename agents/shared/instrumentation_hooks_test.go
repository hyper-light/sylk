package shared

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
)

// TestLogLLMCallStartedFromContext_EmitsRequestSentEvent pins the LLM
// start hook: every LLM call now produces a paired
// LLMRequestSent → LLMResponseReceived (or LLMError) record so hung calls
// remain observable in the WAL until the response arrives. Captures
// model + message + tool counts.
func TestLogLLMCallStartedFromContext_EmitsRequestSentEvent(t *testing.T) {
	dir := t.TempDir()
	logger := agentlog.NewSessionEventLogger("engineer", "events")
	if err := logger.BindSession(dir, "ses_test"); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	ctx := WithLogMeta(context.Background(), LogMeta{
		EventLogger: logger,
		AgentID:     "engineer",
		SessionID:   "ses_test",
		CorrID:      "corr-llm-start",
	})

	req := &providers.Request{
		Model:    "claude-opus-4-7",
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
		Tools:    []providers.Tool{{Name: "read_file"}, {Name: "write_file"}},
	}
	LogLLMCallStartedFromContext(ctx, req)

	var sawStart bool
	for _, e := range logger.RecentEntries(8) {
		if e.EventCode == agentlog.EventLLMRequestSent {
			sawStart = true
			data, _ := e.Data.(map[string]any)
			if data["model"] != "claude-opus-4-7" {
				t.Fatalf("model = %v, want claude-opus-4-7", data["model"])
			}
			if data["messages_count"].(int) != 1 {
				t.Fatalf("messages_count = %v, want 1", data["messages_count"])
			}
			if data["tools_offered"].(int) != 2 {
				t.Fatalf("tools_offered = %v, want 2", data["tools_offered"])
			}
		}
	}
	if !sawStart {
		t.Fatal("EventLLMRequestSent not found")
	}
}

// TestLogLLMCallStartedFromContext_NilSafe pins the no-op contract: a
// nil request or missing LogMeta must not panic. Required because every
// agent will fan out this call and a single panic would crash a
// goroutine boundary.
func TestLogLLMCallStartedFromContext_NilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LogLLMCallStartedFromContext panicked: %v", r)
		}
	}()
	LogLLMCallStartedFromContext(context.Background(), nil)
	LogLLMCallStartedFromContext(context.Background(), &providers.Request{Model: "x"})
}

// TestLogThinkingChunk_RecordsSampledChunk pins the thinking-stream
// instrumentation: one log entry per sentence-boundary emission with
// byte size + a short preview. Without this, long-running thinking
// streams were invisible until the LLM call returned.
func TestLogThinkingChunk_RecordsSampledChunk(t *testing.T) {
	dir := t.TempDir()
	logger := agentlog.NewSessionEventLogger("architect", "events")
	if err := logger.BindSession(dir, "ses_test"); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	ctx := WithLogMeta(context.Background(), LogMeta{
		EventLogger: logger,
		AgentID:     "architect",
		SessionID:   "ses_test",
		CorrID:      "corr-think",
	})

	thought := "I am evaluating the structural shape of this refactor proposal."
	LogThinkingChunk(ctx, thought)
	LogThinkingChunk(ctx, "") // no-op

	var sawChunk bool
	for _, e := range logger.RecentEntries(8) {
		if e.EventCode == agentlog.EventLLMStreamChunk {
			sawChunk = true
			data, _ := e.Data.(map[string]any)
			if data["thinking_size"].(int) != len(thought) {
				t.Fatalf("thinking_size = %v, want %d", data["thinking_size"], len(thought))
			}
			if data["preview"] == nil {
				t.Fatal("preview missing")
			}
		}
	}
	if !sawChunk {
		t.Fatal("EventLLMStreamChunk not found")
	}
}

// TestPublishAgentState_MirrorsToWAL pins the agent-state WAL hook: every
// AgentStateEvent published to the bus is also recorded in the WAL so
// the durable history mirrors the chat activity indicator. Without this
// hook the only authoritative state record is in transient bus traffic.
func TestPublishAgentState_MirrorsToWAL(t *testing.T) {
	dir := t.TempDir()
	logger := agentlog.NewSessionEventLogger("designer", "events")
	if err := logger.BindSession(dir, "ses_test"); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	ctx := WithLogMeta(context.Background(), LogMeta{
		EventLogger: logger,
		AgentID:     "designer",
		SessionID:   "ses_test",
		CorrID:      "corr-state",
	})

	// Bus + channels are nil — PublishStreamEvent will short-circuit, but
	// the WAL hook fires unconditionally on every PublishAgentState call.
	_ = PublishAgentState(nil, nil, ctx, "designer", "designer",
		guide.AgentStateReasoning, "thinking through layout", nil)

	var sawState bool
	for _, e := range logger.RecentEntries(8) {
		if e.EventCode == agentlog.EventAgentStateTransition {
			sawState = true
			data, _ := e.Data.(map[string]any)
			if data["state"] != string(guide.AgentStateReasoning) {
				t.Fatalf("state = %v, want %v", data["state"], guide.AgentStateReasoning)
			}
			if data["detail"] != "thinking through layout" {
				t.Fatalf("detail = %v, want 'thinking through layout'", data["detail"])
			}
		}
	}
	if !sawState {
		t.Fatal("EventAgentStateTransition not found")
	}
}
