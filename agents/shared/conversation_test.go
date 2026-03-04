package shared

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

// ---------------------------------------------------------------------------
// ConversationTurnsToMessages — raw conversion (for Guardian local seeding)
// ---------------------------------------------------------------------------

func TestConversationTurnsToMessages_NilHistory(t *testing.T) {
	msgs := ConversationTurnsToMessages(nil)
	if msgs != nil {
		t.Fatalf("expected nil, got %v", msgs)
	}
}

func TestConversationTurnsToMessages_EmptyHistory(t *testing.T) {
	msgs := ConversationTurnsToMessages([]guide.ConversationTurn{})
	if msgs != nil {
		t.Fatalf("expected nil, got %v", msgs)
	}
}

func TestConversationTurnsToMessages_SingleTurn(t *testing.T) {
	history := []guide.ConversationTurn{
		{UserInput: "hello", AgentReply: "hi there", AgentID: "agent-1", Timestamp: time.Now()},
	}
	msgs := ConversationTurnsToMessages(history)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	assertMsg(t, msgs[0], providers.RoleUser, "hello")
	assertMsg(t, msgs[1], providers.RoleAssistant, "hi there")
}

func TestConversationTurnsToMessages_EmptyAgentReply(t *testing.T) {
	history := []guide.ConversationTurn{
		{UserInput: "hello", AgentReply: "", AgentID: "agent-1", Timestamp: time.Now()},
	}
	msgs := ConversationTurnsToMessages(history)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (user only), got %d", len(msgs))
	}
	assertMsg(t, msgs[0], providers.RoleUser, "hello")
}

func TestConversationTurnsToMessages_MultipleTurns(t *testing.T) {
	history := []guide.ConversationTurn{
		{UserInput: "q1", AgentReply: "a1", AgentID: "agent-1", Timestamp: time.Now()},
		{UserInput: "q2", AgentReply: "a2", AgentID: "agent-1", Timestamp: time.Now()},
		{UserInput: "q3", AgentReply: "", AgentID: "agent-1", Timestamp: time.Now()},
	}
	msgs := ConversationTurnsToMessages(history)
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}
	assertMsg(t, msgs[0], providers.RoleUser, "q1")
	assertMsg(t, msgs[1], providers.RoleAssistant, "a1")
	assertMsg(t, msgs[2], providers.RoleUser, "q2")
	assertMsg(t, msgs[3], providers.RoleAssistant, "a2")
	assertMsg(t, msgs[4], providers.RoleUser, "q3")
}

// ---------------------------------------------------------------------------
// PrependHistoryMessages — recalled context in system prompt
// ---------------------------------------------------------------------------

func TestPrependHistoryMessages_EmptyHistory(t *testing.T) {
	req := &providers.Request{
		SystemPrompt: "base system",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "current"},
		},
	}
	PrependHistoryMessages(req, nil)

	// System prompt untouched.
	if req.SystemPrompt != "base system" {
		t.Errorf("system prompt modified on empty history: %q", req.SystemPrompt)
	}
	// Messages untouched.
	if len(req.Messages) != 1 || req.Messages[0].Content != "current" {
		t.Errorf("messages modified on empty history")
	}
}

func TestPrependHistoryMessages_AppendsToSystemPrompt(t *testing.T) {
	req := &providers.Request{
		SystemPrompt: "You are a helpful agent.",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "current task"},
		},
	}
	history := []guide.ConversationTurn{
		{UserInput: "build auth", AgentReply: "I'll analyze the requirements", AgentID: "a", Timestamp: time.Now()},
	}
	PrependHistoryMessages(req, history)

	// System prompt starts with original.
	if !strings.HasPrefix(req.SystemPrompt, "You are a helpful agent.") {
		t.Error("original system prompt not preserved as prefix")
	}

	// Contains recalled context header.
	if !strings.Contains(req.SystemPrompt, "## Prior Conversation Context") {
		t.Error("missing context header in system prompt")
	}

	// Second-person attribution.
	if !strings.Contains(req.SystemPrompt, "Turn 1 — User: build auth") {
		t.Error("missing user turn in recalled context")
	}
	if !strings.Contains(req.SystemPrompt, "Your response: I'll analyze the requirements") {
		t.Error("missing second-person response attribution")
	}

	// Independent evaluation cue.
	if !strings.Contains(req.SystemPrompt, "Evaluate the current request on its own merits") {
		t.Error("missing independent evaluation cue")
	}

	// Messages completely untouched.
	if len(req.Messages) != 1 || req.Messages[0].Content != "current task" {
		t.Error("messages were modified")
	}
}

func TestPrependHistoryMessages_MultipleTurns(t *testing.T) {
	req := &providers.Request{
		SystemPrompt: "base",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "now"},
		},
	}
	history := []guide.ConversationTurn{
		{UserInput: "q1", AgentReply: "a1", AgentID: "a", Timestamp: time.Now()},
		{UserInput: "q2", AgentReply: "a2", AgentID: "a", Timestamp: time.Now()},
	}
	PrependHistoryMessages(req, history)

	if !strings.Contains(req.SystemPrompt, "Turn 1 — User: q1") {
		t.Error("missing turn 1 user")
	}
	if !strings.Contains(req.SystemPrompt, "Turn 2 — User: q2") {
		t.Error("missing turn 2 user")
	}
	if !strings.Contains(req.SystemPrompt, "Your response: a1") {
		t.Error("missing turn 1 response")
	}
	if !strings.Contains(req.SystemPrompt, "Your response: a2") {
		t.Error("missing turn 2 response")
	}
	// Messages untouched.
	if len(req.Messages) != 1 || req.Messages[0].Content != "now" {
		t.Error("messages were modified")
	}
}

func TestPrependHistoryMessages_EmptyReplyTurn(t *testing.T) {
	req := &providers.Request{
		SystemPrompt: "base",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "current"},
		},
	}
	history := []guide.ConversationTurn{
		{UserInput: "unanswered", AgentReply: "", AgentID: "a", Timestamp: time.Now()},
	}
	PrependHistoryMessages(req, history)

	if !strings.Contains(req.SystemPrompt, "Turn 1 — User: unanswered") {
		t.Error("missing unanswered user turn")
	}
	// No "Your response:" line for empty reply.
	if strings.Contains(req.SystemPrompt, "Your response:") {
		t.Error("should not contain response line for empty reply")
	}
}

func TestPrependHistoryMessages_PreservesMultipleCurrentMessages(t *testing.T) {
	req := &providers.Request{
		SystemPrompt: "base",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "original prompt"},
			{Role: providers.RoleAssistant, Content: "prefill"},
			{Role: providers.RoleUser, Content: "continuation prompt"},
		},
	}
	history := []guide.ConversationTurn{
		{UserInput: "q1", AgentReply: "a1", AgentID: "a", Timestamp: time.Now()},
	}
	PrependHistoryMessages(req, history)

	// All 3 original messages preserved, untouched, in order.
	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(req.Messages))
	}
	assertMsg(t, req.Messages[0], providers.RoleUser, "original prompt")
	assertMsg(t, req.Messages[1], providers.RoleAssistant, "prefill")
	assertMsg(t, req.Messages[2], providers.RoleUser, "continuation prompt")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func assertMsg(t *testing.T, msg providers.Message, wantRole providers.Role, wantContent string) {
	t.Helper()
	if msg.Role != wantRole || msg.Content != wantContent {
		t.Errorf("got {%s, %q}, want {%s, %q}", msg.Role, msg.Content, wantRole, wantContent)
	}
}
