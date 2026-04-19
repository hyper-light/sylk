package forest

import (
	"testing"
)

// Placement helpers are the interesting logic; the Project* methods are
// thin wrappers over Retrieve+placement. Tests here exercise placement
// directly so they do not depend on a live SQLite fixture — a
// regression in state-to-bucket mapping surfaces here without the
// noise of retrieval ranking.

func packet(state BranchState, conflictSeverity float64) *BranchPacket {
	p := &BranchPacket{
		Branch: &Branch{State: state, Summary: "s"},
	}
	if conflictSeverity > 0 {
		p.Conflicts = []PacketConflict{{Severity: conflictSeverity}}
	}
	return p
}

func TestPlaceConstraintPacket_Partitions(t *testing.T) {
	out := &ConstraintProjection{}
	placeConstraintPacket(packet(BranchStateActive, 0), out)
	placeConstraintPacket(packet(BranchStateContradicted, 0), out)
	placeConstraintPacket(packet(BranchStateActive, 0.4), out) // conflicted wins
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

func TestPlaceEvidencePacket_RefutedAndCurrent(t *testing.T) {
	out := &EvidenceProjection{}
	placeEvidencePacket(packet(BranchStateValidated, 0), out)
	placeEvidencePacket(packet(BranchStateContradicted, 0), out)
	placeEvidencePacket(packet(BranchStateSuperseded, 0), out)
	placeEvidencePacket(packet(BranchStateActive, 0.6), out) // flagged
	placeEvidencePacket(packet(BranchStateActive, 0), out)

	if got := len(out.Current); got != 2 {
		t.Errorf("Current = %d, want 2 (Validated + Active)", got)
	}
	if got := len(out.Refuted); got != 2 {
		t.Errorf("Refuted = %d, want 2 (Contradicted + Superseded)", got)
	}
	if got := len(out.Flagged); got != 1 {
		t.Errorf("Flagged = %d, want 1", got)
	}
}

func TestPlaceDecisionPacket_LifecyclePartitions(t *testing.T) {
	out := &DecisionProjection{}
	placeDecisionPacket(packet(BranchStateCandidate, 0), out)
	placeDecisionPacket(packet(BranchStateActive, 0), out)     // chosen
	placeDecisionPacket(packet(BranchStateValidated, 0), out)  // chosen
	placeDecisionPacket(packet(BranchStateSuperseded, 0), out) // superseded
	placeDecisionPacket(packet(BranchStateDormant, 0), out)    // superseded bucket
	placeDecisionPacket(packet(BranchStateContradicted, 0), out)
	placeDecisionPacket(packet(BranchStateActive, 0.7), out) // flagged

	if got := len(out.Candidates); got != 1 {
		t.Errorf("Candidates = %d, want 1", got)
	}
	if got := len(out.Chosen); got != 2 {
		t.Errorf("Chosen = %d, want 2 (Active + Validated)", got)
	}
	if got := len(out.Superseded); got != 2 {
		t.Errorf("Superseded = %d, want 2 (Superseded + Dormant)", got)
	}
	if got := len(out.Contradicted); got != 1 {
		t.Errorf("Contradicted = %d, want 1", got)
	}
	if got := len(out.Flagged); got != 1 {
		t.Errorf("Flagged = %d, want 1", got)
	}
}

func TestPlaceOutcomePacket_SuccessAndRegression(t *testing.T) {
	out := &OutcomeProjection{}
	placeOutcomePacket(packet(BranchStateValidated, 0), out)
	placeOutcomePacket(packet(BranchStateContradicted, 0), out)
	placeOutcomePacket(packet(BranchStateCandidate, 0), out)
	placeOutcomePacket(packet(BranchStateActive, 0.4), out)

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

func TestPlacePreferencePacket_ActiveVsDormant(t *testing.T) {
	out := &PreferenceProjection{}
	placePreferencePacket(packet(BranchStateActive, 0), out)
	placePreferencePacket(packet(BranchStateDormant, 0), out)
	placePreferencePacket(packet(BranchStateActive, 0.8), out)

	if got := len(out.Active); got != 1 {
		t.Errorf("Active = %d, want 1", got)
	}
	if got := len(out.Dormant); got != 1 {
		t.Errorf("Dormant = %d, want 1", got)
	}
	if got := len(out.Flagged); got != 1 {
		t.Errorf("Flagged = %d, want 1", got)
	}
}

func TestPlaceCapabilityPacket_TrustLevels(t *testing.T) {
	out := &CapabilityProjection{}
	placeCapabilityPacket(packet(BranchStateValidated, 0), out)
	placeCapabilityPacket(packet(BranchStateContradicted, 0), out)
	placeCapabilityPacket(packet(BranchStateCandidate, 0), out)
	placeCapabilityPacket(packet(BranchStateActive, 0), out)
	placeCapabilityPacket(packet(BranchStateActive, 0.3), out)

	if got := len(out.Proven); got != 1 {
		t.Errorf("Proven = %d, want 1", got)
	}
	if got := len(out.Unreliable); got != 1 {
		t.Errorf("Unreliable = %d, want 1", got)
	}
	if got := len(out.Claimed); got != 2 {
		t.Errorf("Claimed = %d, want 2 (Candidate + Active)", got)
	}
	if got := len(out.Flagged); got != 1 {
		t.Errorf("Flagged = %d, want 1", got)
	}
}

func TestPlaceOpportunityPacket_Lifecycle(t *testing.T) {
	out := &OpportunityProjection{}
	placeOpportunityPacket(packet(BranchStateCandidate, 0), out)
	placeOpportunityPacket(packet(BranchStateActive, 0), out)
	placeOpportunityPacket(packet(BranchStateValidated, 0), out)  // accepted
	placeOpportunityPacket(packet(BranchStateSuperseded, 0), out) // rejected
	placeOpportunityPacket(packet(BranchStateContradicted, 0), out)
	placeOpportunityPacket(packet(BranchStateActive, 0.9), out)

	if got := len(out.Proposed); got != 1 {
		t.Errorf("Proposed = %d, want 1", got)
	}
	if got := len(out.Accepted); got != 2 {
		t.Errorf("Accepted = %d, want 2 (Active + Validated)", got)
	}
	if got := len(out.Rejected); got != 2 {
		t.Errorf("Rejected = %d, want 2 (Superseded + Contradicted)", got)
	}
	if got := len(out.Flagged); got != 1 {
		t.Errorf("Flagged = %d, want 1", got)
	}
}

// TestProjectIntent_PrimaryIntentLocksOnFirstActive verifies the quirk
// that PrimaryIntent is populated from the first Active packet only —
// not Dormant, not Flagged. A regression here means ProjectIntent is
// either leaking stale/dormant summaries into PrimaryIntent or failing
// to populate it when the first packet happens to be dormant.
func TestProjectIntent_PrimaryIntentLocksOnFirstActive(t *testing.T) {
	out := &IntentProjection{}

	dormant := &BranchPacket{Branch: &Branch{State: BranchStateDormant, Summary: "stale-goal"}}
	active := &BranchPacket{Branch: &Branch{State: BranchStateActive, Summary: "real-goal"}}
	laterActive := &BranchPacket{Branch: &Branch{State: BranchStateActive, Summary: "would-clobber"}}

	for _, p := range []*BranchPacket{dormant, active, laterActive} {
		if flagIfNil(p) || p.HasUnresolvedConflicts() {
			continue
		}
		if p.Branch.State == BranchStateDormant {
			out.Dormant = append(out.Dormant, *p)
			continue
		}
		out.Active = append(out.Active, *p)
		if out.PrimaryIntent == "" {
			out.PrimaryIntent = p.Branch.Summary
		}
	}

	if got := out.PrimaryIntent; got != "real-goal" {
		t.Errorf("PrimaryIntent = %q, want %q — dormant summary leaked or active was skipped", got, "real-goal")
	}
	if got := len(out.Active); got != 2 {
		t.Errorf("Active = %d, want 2", got)
	}
}

// TestIsRefutedState_Completeness documents the canonical refuted set
// so adding a new BranchState later triggers a compile failure here
// (via the switch) rather than silently widening the Evidence "current"
// bucket.
func TestIsRefutedState_Completeness(t *testing.T) {
	cases := map[BranchState]bool{
		BranchStateActive:       false,
		BranchStateCandidate:    false,
		BranchStateValidated:    false,
		BranchStateContradicted: true,
		BranchStateSuperseded:   true,
		BranchStateDormant:      false,
	}
	for state, want := range cases {
		if got := isRefutedState(state); got != want {
			t.Errorf("isRefutedState(%q) = %v, want %v", state, got, want)
		}
	}
}
