package msg

import (
	"encoding/json"
	"testing"
	"time"
)

func TestClaimPresentationMsgZeroValueIsSafe(t *testing.T) {
	var m ClaimPresentationMsg
	if _, err := json.Marshal(m); err != nil {
		t.Fatalf("marshal zero value: %v", err)
	}
}

func TestClaimPresentationMsgJSONRoundTrip(t *testing.T) {
	original := ClaimPresentationMsg{
		SessionID:   "session-1",
		CycleID:     "claim-root",
		ClaimID:     "claim-1",
		SourceType:  "artifact",
		SourceID:    "artifact-plan",
		TestamentID: "testament-1",
		AgentID:     "architect",
		Title:       "Plan",
		Content:     "### Plan",
		Format:      "markdown",
		Placement:   "before_response",
		ReplaceKey:  "plan:p1:review",
		Metadata:    map[string]any{"plan_id": "p1"},
		CreatedAt:   time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
		Sequence:    42,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded ClaimPresentationMsg
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.SourceID != original.SourceID {
		t.Fatalf("SourceID = %q, want %q", decoded.SourceID, original.SourceID)
	}
	if decoded.Format != "markdown" || decoded.Placement != "before_response" {
		t.Fatalf("format/placement = %q/%q", decoded.Format, decoded.Placement)
	}
	if decoded.ReplaceKey != original.ReplaceKey {
		t.Fatalf("ReplaceKey = %q", decoded.ReplaceKey)
	}
	if decoded.Sequence != original.Sequence {
		t.Fatalf("Sequence = %d", decoded.Sequence)
	}
}
