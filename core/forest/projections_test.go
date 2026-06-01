package forest

import "testing"

func projectionPacket(status string, grade EvidenceGrade, flagged bool) *ForestPacket {
	packet := &ForestPacket{
		Node: ForestNode{
			ID:            stableID("projection_packet", status, string(grade)),
			Status:        status,
			EvidenceGrade: grade,
			Title:         "title",
			Summary:       "summary",
			Confidence:    1,
		},
		Score: 1,
	}
	if flagged {
		packet.CounterEvidence = []ForestEvidence{{RefType: "edge", RefID: "counter", Counter: true}}
	}
	return packet
}

func TestPlaceConstraintPacket_PartitionsForestPackets(t *testing.T) {
	out := &ConstraintProjection{}
	placeConstraintPacket(projectionPacket(string(BranchStateActive), EvidenceGradeValidated, false), out)
	placeConstraintPacket(projectionPacket(string(BranchStateContradicted), EvidenceGradeContradicted, false), out)
	placeConstraintPacket(projectionPacket(string(BranchStateActive), EvidenceGradeValidated, true), out)
	placeConstraintPacket(nil, out)

	if got := len(out.Enforced); got != 1 {
		t.Errorf("Enforced = %d, want 1", got)
	}
	if got := len(out.Disputed); got != 1 {
		t.Errorf("Disputed = %d, want 1", got)
	}
	if got := len(out.Flagged); got != 1 {
		t.Errorf("Flagged = %d, want 1", got)
	}
}

func TestPlaceEvidencePacket_RefutedAndCurrentForestPackets(t *testing.T) {
	out := &EvidenceProjection{}
	placeEvidencePacket(projectionPacket(string(BranchStateValidated), EvidenceGradeValidated, false), out)
	placeEvidencePacket(projectionPacket(string(BranchStateContradicted), EvidenceGradeContradicted, false), out)
	placeEvidencePacket(projectionPacket(string(BranchStateSuperseded), EvidenceGradeObserved, false), out)
	placeEvidencePacket(projectionPacket(string(BranchStateActive), EvidenceGradeObserved, true), out)
	placeEvidencePacket(projectionPacket(string(BranchStateActive), EvidenceGradeObserved, false), out)

	if got := len(out.Current); got != 2 {
		t.Errorf("Current = %d, want 2", got)
	}
	if got := len(out.Refuted); got != 2 {
		t.Errorf("Refuted = %d, want 2", got)
	}
	if got := len(out.Flagged); got != 1 {
		t.Errorf("Flagged = %d, want 1", got)
	}
}

func TestPlaceDecisionPacket_LifecyclePartitionsForestPackets(t *testing.T) {
	out := &DecisionProjection{}
	placeDecisionPacket(projectionPacket(string(BranchStateCandidate), EvidenceGradeObserved, false), out)
	placeDecisionPacket(projectionPacket(string(BranchStateActive), EvidenceGradeObserved, false), out)
	placeDecisionPacket(projectionPacket(string(BranchStateValidated), EvidenceGradeValidated, false), out)
	placeDecisionPacket(projectionPacket(string(BranchStateSuperseded), EvidenceGradeObserved, false), out)
	placeDecisionPacket(projectionPacket(string(BranchStateDormant), EvidenceGradeObserved, false), out)
	placeDecisionPacket(projectionPacket(string(BranchStateContradicted), EvidenceGradeContradicted, false), out)
	placeDecisionPacket(projectionPacket(string(BranchStateActive), EvidenceGradeObserved, true), out)

	if got := len(out.Candidates); got != 1 {
		t.Errorf("Candidates = %d, want 1", got)
	}
	if got := len(out.Chosen); got != 2 {
		t.Errorf("Chosen = %d, want 2", got)
	}
	if got := len(out.Superseded); got != 2 {
		t.Errorf("Superseded = %d, want 2", got)
	}
	if got := len(out.Contradicted); got != 1 {
		t.Errorf("Contradicted = %d, want 1", got)
	}
	if got := len(out.Flagged); got != 1 {
		t.Errorf("Flagged = %d, want 1", got)
	}
}

func TestPlaceOutcomePacket_SuccessAndRegressionForestPackets(t *testing.T) {
	out := &OutcomeProjection{}
	placeOutcomePacket(projectionPacket("", EvidenceGradeValidated, false), out)
	placeOutcomePacket(projectionPacket("", EvidenceGradeFailed, false), out)
	placeOutcomePacket(projectionPacket("", EvidenceGradeObserved, false), out)
	placeOutcomePacket(projectionPacket("", EvidenceGradeObserved, true), out)

	if got := len(out.Successes); got != 1 {
		t.Errorf("Successes = %d, want 1", got)
	}
	if got := len(out.Regressions); got != 1 {
		t.Errorf("Regressions = %d, want 1", got)
	}
	if got := len(out.Pending); got != 1 {
		t.Errorf("Pending = %d, want 1", got)
	}
	if got := len(out.Flagged); got != 1 {
		t.Errorf("Flagged = %d, want 1", got)
	}
}

func TestPlaceOpportunityPacket_LifecycleForestPackets(t *testing.T) {
	out := &OpportunityProjection{}
	placeOpportunityPacket(projectionPacket(ForestProposalStatusProposed, EvidenceGradeGenerated, false), out)
	placeOpportunityPacket(projectionPacket(ForestProposalStatusAccepted, EvidenceGradeValidated, false), out)
	placeOpportunityPacket(projectionPacket(ForestProposalStatusRejected, EvidenceGradeFailed, false), out)
	placeOpportunityPacket(projectionPacket(ForestProposalStatusAccepted, EvidenceGradeValidated, true), out)

	if got := len(out.Proposed); got != 1 {
		t.Errorf("Proposed = %d, want 1", got)
	}
	if got := len(out.Accepted); got != 1 {
		t.Errorf("Accepted = %d, want 1", got)
	}
	if got := len(out.Rejected); got != 1 {
		t.Errorf("Rejected = %d, want 1", got)
	}
	if got := len(out.Flagged); got != 1 {
		t.Errorf("Flagged = %d, want 1", got)
	}
}

func TestForestPacketRefuted(t *testing.T) {
	cases := []struct {
		packet *ForestPacket
		want   bool
	}{
		{projectionPacket(string(BranchStateActive), EvidenceGradeObserved, false), false},
		{projectionPacket(string(BranchStateContradicted), EvidenceGradeObserved, false), true},
		{projectionPacket(string(BranchStateSuperseded), EvidenceGradeObserved, false), true},
		{projectionPacket("", EvidenceGradeFailed, false), true},
		{projectionPacket("", EvidenceGradeContradicted, false), true},
	}
	for _, tc := range cases {
		if got := forestPacketRefuted(tc.packet); got != tc.want {
			t.Errorf("forestPacketRefuted(%+v) = %v, want %v", tc.packet.Node, got, tc.want)
		}
	}
}
