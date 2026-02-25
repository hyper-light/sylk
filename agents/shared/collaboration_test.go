package shared

import (
	"encoding/json"
	"testing"
	"time"
)

// --- CollabMsgType.String ---

func TestCollabMsgType_String_AllValues(t *testing.T) {
	cases := []struct {
		input CollabMsgType
		want  string
	}{
		{CollabImplementation, "implementation"},
		{CollabReview, "review"},
		{CollabPushback, "pushback"},
		{CollabAcceptance, "acceptance"},
		{CollabRevision, "revision"},
	}
	for _, tc := range cases {
		got := tc.input.String()
		if got != tc.want {
			t.Fatalf("CollabMsgType(%d).String() = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCollabMsgType_String_Unknown(t *testing.T) {
	unknown := CollabMsgType(999)
	got := unknown.String()
	if got != "unknown" {
		t.Fatalf("expected \"unknown\" for out-of-range value, got %q", got)
	}
}

// --- CollaborationRecord zero value ---

func TestCollaborationRecord_ZeroValue(t *testing.T) {
	var rec CollaborationRecord
	if rec.Consensus {
		t.Fatal("zero-value Consensus should be false")
	}
	if rec.TotalRounds != 0 {
		t.Fatalf("zero-value TotalRounds should be 0, got %d", rec.TotalRounds)
	}
}

// --- JSON serialization ---

func TestCollaborationMessage_JSONRoundtrip(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	original := CollaborationMessage{
		FromAgent: "engineer",
		ToAgent:   "reviewer",
		Type:      CollabReview,
		Content:   "looks good",
		Metadata:  map[string]any{"score": 0.95},
		Round:     1,
		Timestamp: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var restored CollaborationMessage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if restored.FromAgent != original.FromAgent {
		t.Fatalf("FromAgent mismatch: got %q, want %q", restored.FromAgent, original.FromAgent)
	}
	if restored.Type != original.Type {
		t.Fatalf("Type mismatch: got %d, want %d", restored.Type, original.Type)
	}
	if restored.Content != original.Content {
		t.Fatalf("Content mismatch: got %q, want %q", restored.Content, original.Content)
	}
}

func TestCollaborationRound_JSONRoundtrip(t *testing.T) {
	original := CollaborationRound{
		Round: 2,
		Messages: []CollaborationMessage{
			{FromAgent: "a", ToAgent: "b", Type: CollabPushback, Content: "needs work"},
		},
		Outcome: CollabPushback,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var restored CollaborationRound
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if restored.Round != original.Round {
		t.Fatalf("Round mismatch: got %d, want %d", restored.Round, original.Round)
	}
	if len(restored.Messages) != len(original.Messages) {
		t.Fatalf("Messages count mismatch: got %d, want %d",
			len(restored.Messages), len(original.Messages))
	}
	if restored.Outcome != original.Outcome {
		t.Fatalf("Outcome mismatch: got %d, want %d", restored.Outcome, original.Outcome)
	}
}

func TestCollaborationRecord_JSONRoundtrip(t *testing.T) {
	original := CollaborationRecord{
		Rounds: []CollaborationRound{
			{Round: 1, Outcome: CollabAcceptance},
		},
		Consensus:   true,
		TotalRounds: 1,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var restored CollaborationRecord
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if restored.Consensus != original.Consensus {
		t.Fatalf("Consensus mismatch: got %v, want %v", restored.Consensus, original.Consensus)
	}
	if restored.TotalRounds != original.TotalRounds {
		t.Fatalf("TotalRounds mismatch: got %d, want %d",
			restored.TotalRounds, original.TotalRounds)
	}
}
