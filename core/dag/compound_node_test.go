package dag

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// CollaborationMode.String
// ---------------------------------------------------------------------------

func TestCollaborationModeString(t *testing.T) {
	tests := []struct {
		name string
		mode CollaborationMode
		want string
	}{
		{name: "sequential", mode: CollaborationSequential, want: "sequential"},
		{name: "adversarial", mode: CollaborationAdversarial, want: "adversarial"},
		{name: "unknown", mode: CollaborationMode(99), want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.mode.String()
			if got != tt.want {
				t.Errorf("CollaborationMode(%d).String() = %q, want %q", tt.mode, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsCompoundNode
// ---------------------------------------------------------------------------

func TestIsCompoundNode(t *testing.T) {
	tests := []struct {
		name     string
		coAgents []string
		want     bool
	}{
		{name: "nil co-agents", coAgents: nil, want: false},
		{name: "empty co-agents", coAgents: []string{}, want: false},
		{name: "one co-agent", coAgents: []string{"reviewer"}, want: true},
		{name: "multiple co-agents", coAgents: []string{"reviewer", "tester"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &NodeConfig{CoAgents: tt.coAgents}
			got := IsCompoundNode(cfg)
			if got != tt.want {
				t.Errorf("IsCompoundNode() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// CoAgentTypes
// ---------------------------------------------------------------------------

func TestCoAgentTypes_NilReturnsNil(t *testing.T) {
	cfg := &NodeConfig{CoAgents: nil}
	got := CoAgentTypes(cfg)
	if got != nil {
		t.Errorf("CoAgentTypes(nil CoAgents) = %v, want nil", got)
	}
}

func TestCoAgentTypes_EmptyReturnsNil(t *testing.T) {
	cfg := &NodeConfig{CoAgents: []string{}}
	got := CoAgentTypes(cfg)
	if got != nil {
		t.Errorf("CoAgentTypes(empty CoAgents) = %v, want nil", got)
	}
}

func TestCoAgentTypes_ReturnsCopy(t *testing.T) {
	original := []string{"reviewer", "tester"}
	cfg := &NodeConfig{CoAgents: original}
	got := CoAgentTypes(cfg)

	if len(got) != len(original) {
		t.Fatalf("CoAgentTypes() length = %d, want %d", len(got), len(original))
	}
	for i := range original {
		if got[i] != original[i] {
			t.Errorf("CoAgentTypes()[%d] = %q, want %q", i, got[i], original[i])
		}
	}

	// Verify it is a distinct slice (not aliased backing array).
	got[0] = "mutated"
	if cfg.CoAgents[0] == "mutated" {
		t.Error("CoAgentTypes() returned aliased slice; mutation leaked to original")
	}
}

// ---------------------------------------------------------------------------
// ValidateCompoundNode
// ---------------------------------------------------------------------------

func TestValidateCompoundNode_EmptyCoAgentsReturnsNil(t *testing.T) {
	cfg := &NodeConfig{
		ID:        "n1",
		AgentType: "coder",
		CoAgents:  nil,
	}
	if err := ValidateCompoundNode(cfg); err != nil {
		t.Errorf("ValidateCompoundNode(empty CoAgents) = %v, want nil", err)
	}
}

func TestValidateCompoundNode_SelfReferentialCoAgent(t *testing.T) {
	cfg := &NodeConfig{
		ID:        "n1",
		AgentType: "coder",
		CoAgents:  []string{"coder"},
	}
	err := ValidateCompoundNode(cfg)
	if err == nil {
		t.Fatal("ValidateCompoundNode(self-referential) = nil, want error")
	}
	if !strings.Contains(err.Error(), "cannot be its own co-agent") {
		t.Errorf("error message = %q, want substring %q", err.Error(), "cannot be its own co-agent")
	}
}

func TestValidateCompoundNode_AdversarialZeroRounds(t *testing.T) {
	cfg := &NodeConfig{
		ID:                "n1",
		AgentType:         "coder",
		CoAgents:          []string{"reviewer"},
		CollaborationMode: CollaborationAdversarial,
		MaxReviewRounds:   0,
	}
	err := ValidateCompoundNode(cfg)
	if err == nil {
		t.Fatal("ValidateCompoundNode(adversarial, 0 rounds) = nil, want error")
	}
	if !strings.Contains(err.Error(), "MaxReviewRounds > 0") {
		t.Errorf("error message = %q, want substring %q", err.Error(), "MaxReviewRounds > 0")
	}
}

func TestValidateCompoundNode_AdversarialValid(t *testing.T) {
	cfg := &NodeConfig{
		ID:                "n1",
		AgentType:         "coder",
		CoAgents:          []string{"reviewer"},
		CollaborationMode: CollaborationAdversarial,
		MaxReviewRounds:   3,
	}
	if err := ValidateCompoundNode(cfg); err != nil {
		t.Errorf("ValidateCompoundNode(valid adversarial) = %v, want nil", err)
	}
}

func TestValidateCompoundNode_SequentialZeroRoundsOK(t *testing.T) {
	cfg := &NodeConfig{
		ID:                "n1",
		AgentType:         "coder",
		CoAgents:          []string{"reviewer"},
		CollaborationMode: CollaborationSequential,
		MaxReviewRounds:   0,
	}
	if err := ValidateCompoundNode(cfg); err != nil {
		t.Errorf("ValidateCompoundNode(sequential, 0 rounds) = %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// CompoundNodeResult.ToNodeResult
// ---------------------------------------------------------------------------

func TestToNodeResult_NilPrimaryResult(t *testing.T) {
	cr := &CompoundNodeResult{
		PrimaryResult: nil,
	}
	got := cr.ToNodeResult()

	if got.State != NodeStateFailed {
		t.Errorf("ToNodeResult().State = %v, want %v", got.State, NodeStateFailed)
	}
	if got.Error == nil {
		t.Fatal("ToNodeResult().Error = nil, want non-nil")
	}
	if !strings.Contains(got.Error.Error(), "no primary result") {
		t.Errorf("ToNodeResult().Error = %q, want substring %q", got.Error.Error(), "no primary result")
	}
	if got.EndTime.IsZero() {
		t.Error("ToNodeResult().EndTime is zero, want non-zero")
	}
}

func TestToNodeResult_ValidPrimary(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Second)

	cr := &CompoundNodeResult{
		PrimaryResult: &NodeResult{
			NodeID:    "n1",
			State:     NodeStateSucceeded,
			Output:    "done",
			StartTime: start,
			EndTime:   end,
			Duration:  end.Sub(start),
		},
		Consensus:        true,
		ReviewRoundsUsed: 2,
	}
	got := cr.ToNodeResult()

	if got.State != NodeStateSucceeded {
		t.Errorf("State = %v, want %v", got.State, NodeStateSucceeded)
	}
	if got.NodeID != "n1" {
		t.Errorf("NodeID = %q, want %q", got.NodeID, "n1")
	}

	consensus, ok := got.Metadata["consensus"].(bool)
	if !ok || !consensus {
		t.Errorf("Metadata[consensus] = %v, want true", got.Metadata["consensus"])
	}

	rounds, ok := got.Metadata["review_rounds_used"].(int)
	if !ok || rounds != 2 {
		t.Errorf("Metadata[review_rounds_used] = %v, want 2", got.Metadata["review_rounds_used"])
	}

	if _, exists := got.Metadata["disputes_count"]; exists {
		t.Error("Metadata[disputes_count] should not exist when there are no disputes")
	}
}

func TestToNodeResult_WithDisputes(t *testing.T) {
	cr := &CompoundNodeResult{
		PrimaryResult: &NodeResult{
			NodeID: "n1",
			State:  NodeStateSucceeded,
		},
		Consensus:        false,
		ReviewRoundsUsed: 3,
		Disputes: []Dispute{
			{Challenger: "reviewer", Subject: "error handling", Round: 1},
			{Challenger: "reviewer", Subject: "naming", Round: 2},
		},
	}
	got := cr.ToNodeResult()

	disputeCount, ok := got.Metadata["disputes_count"].(int)
	if !ok {
		t.Fatalf("Metadata[disputes_count] type = %T, want int", got.Metadata["disputes_count"])
	}
	if disputeCount != len(cr.Disputes) {
		t.Errorf("Metadata[disputes_count] = %d, want %d", disputeCount, len(cr.Disputes))
	}
}

func TestToNodeResult_PrimaryWithExistingMetadata(t *testing.T) {
	cr := &CompoundNodeResult{
		PrimaryResult: &NodeResult{
			NodeID: "n1",
			State:  NodeStateSucceeded,
			Metadata: map[string]any{
				"existing_key": "existing_value",
			},
		},
		Consensus:        true,
		ReviewRoundsUsed: 1,
	}
	got := cr.ToNodeResult()

	// Existing metadata preserved.
	if got.Metadata["existing_key"] != "existing_value" {
		t.Errorf("Metadata[existing_key] = %v, want %q", got.Metadata["existing_key"], "existing_value")
	}
	// Compound metadata added.
	if got.Metadata["consensus"] != true {
		t.Errorf("Metadata[consensus] = %v, want true", got.Metadata["consensus"])
	}
}

func TestToNodeResult_DoesNotMutateOriginal(t *testing.T) {
	primary := &NodeResult{
		NodeID: "n1",
		State:  NodeStateSucceeded,
	}
	cr := &CompoundNodeResult{
		PrimaryResult:    primary,
		Consensus:        true,
		ReviewRoundsUsed: 1,
	}
	got := cr.ToNodeResult()

	// The returned result should be a separate copy.
	got.State = NodeStateFailed
	if primary.State == NodeStateFailed {
		t.Error("ToNodeResult() returned pointer to original; mutation leaked")
	}

	// Original should not have gained metadata.
	if primary.Metadata != nil {
		t.Error("ToNodeResult() mutated original PrimaryResult.Metadata")
	}
}
