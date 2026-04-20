package shared

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

// stubBus captures the last published message for assertion. Only the
// Publish path is exercised by BuildAskUserClarificationSkill; the
// other EventBus methods return zero-value sentinels so the stub
// implements the full interface without doing real work.
type stubBus struct {
	mu        sync.Mutex
	lastTopic string
	lastMsg   *guide.Message
	publishErr error
}

func (s *stubBus) Publish(topic string, msg *guide.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTopic = topic
	s.lastMsg = msg
	return s.publishErr
}

func (s *stubBus) Subscribe(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, errors.New("not implemented")
}
func (s *stubBus) SubscribeAsync(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, errors.New("not implemented")
}
func (s *stubBus) Close() error { return nil }

// TestAskUserClarificationSkill_PublishesClarificationRequest locks
// the publish path: when an agent invokes ask_user_clarification, a
// fire-and-forget request must hit guide.TopicGuideRequests with the
// clarification metadata shape consumers expect (type,
// question, context, options, from_agent). Engineer and tester gain
// this skill via the shared builder; a regression here would break
// their direct path to the user when task preferences are unclear.
func TestAskUserClarificationSkill_PublishesClarificationRequest(t *testing.T) {
	bus := &stubBus{}
	skill := BuildAskUserClarificationSkill(AskUserClarificationConfig{
		Bus:          bus,
		AgentID:      "agent_42",
		AgentName:    "engineer",
		SessionID:    "sess_xyz",
		NewMessageID: func() string { return "msg_1" },
	})
	if skill.Name != "ask_user_clarification" {
		t.Fatalf("skill.Name = %q, want %q", skill.Name, "ask_user_clarification")
	}

	input, _ := json.Marshal(map[string]any{
		"question": "Should the new module be Python or Go?",
		"context":  "Task spec is silent on language; codebase is Go but task wording is Python-flavored.",
		"options":  []string{"python", "go"},
	})
	if _, err := skill.Handler(context.Background(), input); err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}

	if bus.lastTopic != guide.TopicGuideRequests {
		t.Fatalf("publish topic = %q, want %q", bus.lastTopic, guide.TopicGuideRequests)
	}
	if bus.lastMsg == nil {
		t.Fatal("no message published")
	}
	meta := bus.lastMsg.Metadata
	if meta["type"] != "clarification_request" {
		t.Errorf("metadata.type = %v, want clarification_request", meta["type"])
	}
	if meta["question"] != "Should the new module be Python or Go?" {
		t.Errorf("metadata.question = %v, want question text", meta["question"])
	}
	if meta["from_agent"] != "agent_42" {
		t.Errorf("metadata.from_agent = %v, want agent_42", meta["from_agent"])
	}
}

// TestAskUserClarificationSkill_RejectsBlankQuestion guards the
// invariant that the skill cannot publish a request with no question.
// A blank ask would surface to the user as a meaningless prompt and
// waste a clarification turn.
func TestAskUserClarificationSkill_RejectsBlankQuestion(t *testing.T) {
	bus := &stubBus{}
	skill := BuildAskUserClarificationSkill(AskUserClarificationConfig{
		Bus:          bus,
		AgentID:      "agent_1",
		AgentName:    "tester-pipeline",
		SessionID:    "sess_a",
		NewMessageID: func() string { return "msg_1" },
	})
	input, _ := json.Marshal(map[string]any{
		"question": "   ",
		"context":  "still ambiguous",
	})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for blank question, got nil")
	}
	if bus.lastMsg != nil {
		t.Fatal("blank question should not publish")
	}
}

// TestAskUserClarificationSkill_RequiresBus guards the nil-bus case
// — a misconfigured agent must surface the missing bus rather than
// silently dropping the user's clarification request.
func TestAskUserClarificationSkill_RequiresBus(t *testing.T) {
	skill := BuildAskUserClarificationSkill(AskUserClarificationConfig{
		AgentID:   "agent_1",
		AgentName: "tester-pipeline",
		SessionID: "sess_a",
	})
	input, _ := json.Marshal(map[string]any{
		"question": "real question",
		"context":  "real context",
	})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for nil bus, got nil")
	}
}
