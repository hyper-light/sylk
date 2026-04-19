package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/forest"
)

// fakeProjector implements MemoryForestProjector + MemoryForestService.
// The preload helper sniffs via type-assert, so satisfying both lets us
// exercise the real PreloadFor() dispatch path with a controllable
// fixture — no SQLite, no goroutines.
type fakeProjector struct {
	intent       *forest.IntentProjection
	constraint   *forest.ConstraintProjection
	evidence     *forest.EvidenceProjection
	decisions    *forest.DecisionProjection
	outcomes     *forest.OutcomeProjection
	preferences  *forest.PreferenceProjection
	capabilities *forest.CapabilityProjection
	opportunity  *forest.OpportunityProjection

	calledIntent       bool
	calledConstraint   bool
	calledEvidence     bool
	calledDecisions    bool
	calledOutcomes     bool
	calledPreferences  bool
	calledCapabilities bool
}

func (f *fakeProjector) ResolveIntent(context.Context, forest.ResolveIntentInput) (*forest.IntentResolution, error) {
	return nil, nil
}
func (f *fakeProjector) Retrieve(context.Context, forest.Query) ([]*forest.BranchPacket, error) {
	return nil, nil
}
func (f *fakeProjector) PredictNextBranches(context.Context, forest.Query) ([]*forest.BranchPacket, error) {
	return nil, nil
}
func (f *fakeProjector) RecordOutcome(context.Context, forest.OutcomeRecord) error {
	return nil
}

func (f *fakeProjector) ProjectIntent(_ context.Context, _ forest.ProjectionInput) (*forest.IntentProjection, error) {
	f.calledIntent = true
	return f.intent, nil
}
func (f *fakeProjector) ProjectConstraints(_ context.Context, _ forest.ProjectionInput) (*forest.ConstraintProjection, error) {
	f.calledConstraint = true
	return f.constraint, nil
}
func (f *fakeProjector) ProjectEvidence(_ context.Context, _ forest.ProjectionInput) (*forest.EvidenceProjection, error) {
	f.calledEvidence = true
	return f.evidence, nil
}
func (f *fakeProjector) ProjectDecisions(_ context.Context, _ forest.ProjectionInput) (*forest.DecisionProjection, error) {
	f.calledDecisions = true
	return f.decisions, nil
}
func (f *fakeProjector) ProjectOutcomes(_ context.Context, _ forest.ProjectionInput) (*forest.OutcomeProjection, error) {
	f.calledOutcomes = true
	return f.outcomes, nil
}
func (f *fakeProjector) ProjectPreferences(_ context.Context, _ forest.ProjectionInput) (*forest.PreferenceProjection, error) {
	f.calledPreferences = true
	return f.preferences, nil
}
func (f *fakeProjector) ProjectCapabilities(_ context.Context, _ forest.ProjectionInput) (*forest.CapabilityProjection, error) {
	f.calledCapabilities = true
	return f.capabilities, nil
}
func (f *fakeProjector) ProjectOpportunities(_ context.Context, _ forest.ProjectionInput) (*forest.OpportunityProjection, error) {
	return f.opportunity, nil
}

// TestPreloadFor_ArchitectPullsIntentConstraintDecision locks the
// architect's projection lane: memory context should carry the intent
// envelope, the enforced constraints, and chosen decisions — nothing
// else. A regression here (e.g. Architect suddenly pulling Evidence)
// would bloat the planner prompt with Academic-lane content and
// muddle ranking.
func TestPreloadFor_ArchitectPullsIntentConstraintDecision(t *testing.T) {
	f := &fakeProjector{}
	_, err := PreloadFor(context.Background(), f, ForestPreloadInput{AgentType: "architect"})
	if err != nil {
		t.Fatalf("preload err: %v", err)
	}
	if !f.calledIntent || !f.calledConstraint || !f.calledDecisions {
		t.Errorf("architect lane missed a projection: intent=%v constraint=%v decisions=%v",
			f.calledIntent, f.calledConstraint, f.calledDecisions)
	}
	if f.calledEvidence || f.calledOutcomes || f.calledPreferences || f.calledCapabilities {
		t.Errorf("architect lane leaked: evidence=%v outcomes=%v prefs=%v caps=%v",
			f.calledEvidence, f.calledOutcomes, f.calledPreferences, f.calledCapabilities)
	}
}

// TestPreloadFor_LibrarianPullsPreferenceCapabilityIntent — preference
// and capability are the two librarian-lane signals, intent grounds the
// retrieval.
func TestPreloadFor_LibrarianPullsPreferenceCapabilityIntent(t *testing.T) {
	f := &fakeProjector{}
	_, err := PreloadFor(context.Background(), f, ForestPreloadInput{AgentType: "librarian"})
	if err != nil {
		t.Fatalf("preload err: %v", err)
	}
	if !f.calledPreferences || !f.calledCapabilities || !f.calledIntent {
		t.Errorf("librarian lane missed: prefs=%v caps=%v intent=%v",
			f.calledPreferences, f.calledCapabilities, f.calledIntent)
	}
	if f.calledConstraint || f.calledDecisions || f.calledEvidence || f.calledOutcomes {
		t.Errorf("librarian lane leaked: constraint=%v decisions=%v evidence=%v outcomes=%v",
			f.calledConstraint, f.calledDecisions, f.calledEvidence, f.calledOutcomes)
	}
}

// TestPreloadFor_AcademicPullsEvidenceOutcomeIntent — academic owns
// the epistemic lane.
func TestPreloadFor_AcademicPullsEvidenceOutcomeIntent(t *testing.T) {
	f := &fakeProjector{}
	_, err := PreloadFor(context.Background(), f, ForestPreloadInput{AgentType: "academic"})
	if err != nil {
		t.Fatalf("preload err: %v", err)
	}
	if !f.calledEvidence || !f.calledOutcomes || !f.calledIntent {
		t.Errorf("academic lane missed: evidence=%v outcomes=%v intent=%v",
			f.calledEvidence, f.calledOutcomes, f.calledIntent)
	}
	if f.calledConstraint || f.calledDecisions || f.calledPreferences || f.calledCapabilities {
		t.Errorf("academic lane leaked: constraint=%v decisions=%v prefs=%v caps=%v",
			f.calledConstraint, f.calledDecisions, f.calledPreferences, f.calledCapabilities)
	}
}

// TestPreloadFor_UnknownAgentReturnsNil covers the contract guarantee
// that an unknown agent type does not fire projection queries.
// Callers depend on this: a misconfigured agent name must degrade to
// "no preload" rather than dispatching every family.
func TestPreloadFor_UnknownAgentReturnsNil(t *testing.T) {
	f := &fakeProjector{}
	got, err := PreloadFor(context.Background(), f, ForestPreloadInput{AgentType: "scribe"})
	if err != nil {
		t.Fatalf("preload err: %v", err)
	}
	if got != nil {
		t.Errorf("unknown agent returned non-nil preload: %+v", got)
	}
	if f.calledIntent || f.calledConstraint || f.calledEvidence {
		t.Errorf("unknown agent fired a projection")
	}
}

// TestPreloadFor_NilServiceReturnsNil — a nil forest service must
// degrade silently. Memory preload is assist, not gate.
func TestPreloadFor_NilServiceReturnsNil(t *testing.T) {
	got, err := PreloadFor(context.Background(), nil, ForestPreloadInput{AgentType: "architect"})
	if err != nil {
		t.Fatalf("preload err: %v", err)
	}
	if got != nil {
		t.Errorf("nil service returned non-nil preload: %+v", got)
	}
}

// TestForestPreload_RenderIncludesOnlyPopulatedBuckets exercises the
// rendering contract: each populated family produces a header line and
// up to five summaries; empty families are suppressed. Regressions
// here mean the system prompt carries noise ("- Chosen decisions:
// (none)") instead of the intended terse preload.
func TestForestPreload_RenderIncludesOnlyPopulatedBuckets(t *testing.T) {
	packet := func(title, summary string) forest.BranchPacket {
		return forest.BranchPacket{Branch: &forest.Branch{Title: title, Summary: summary}}
	}
	preload := &ForestPreload{
		Intents: &forest.IntentProjection{
			PrimaryIntent: "ship the thing",
			Active:        []forest.BranchPacket{packet("t1", "goal one"), packet("t2", "goal two")},
		},
		Constraints: &forest.ConstraintProjection{
			Enforced: []forest.BranchPacket{packet("c1", "no panics")},
		},
		// Evidence present but empty — must not render.
		Evidence: &forest.EvidenceProjection{},
	}

	rendered := preload.Render()
	if rendered == "" {
		t.Fatal("Render() returned empty for non-empty preload")
	}
	if !strings.Contains(rendered, "MEMORY CONTEXT") {
		t.Errorf("missing header: %q", rendered)
	}
	if !strings.Contains(rendered, `primary="ship the thing"`) {
		t.Errorf("primary intent missing from render: %q", rendered)
	}
	if !strings.Contains(rendered, "t1 — goal one") {
		t.Errorf("active intent summary missing: %q", rendered)
	}
	if !strings.Contains(rendered, "c1 — no panics") {
		t.Errorf("enforced constraint missing: %q", rendered)
	}
	if strings.Contains(rendered, "Current evidence") {
		t.Errorf("empty Evidence bucket rendered anyway: %q", rendered)
	}
}

// TestForestPreload_IsEmpty_NilAndHollowAreBothEmpty ensures IsEmpty
// treats a preload whose every projection is present-but-empty as
// empty. Otherwise Render would emit a bare header with no body.
func TestForestPreload_IsEmpty_NilAndHollowAreBothEmpty(t *testing.T) {
	if !(*ForestPreload)(nil).IsEmpty() {
		t.Error("nil preload reports non-empty")
	}
	hollow := &ForestPreload{
		Intents:      &forest.IntentProjection{},
		Constraints:  &forest.ConstraintProjection{},
		Decisions:    &forest.DecisionProjection{},
		Evidence:     &forest.EvidenceProjection{},
		Outcomes:     &forest.OutcomeProjection{},
		Preferences:  &forest.PreferenceProjection{},
		Capabilities: &forest.CapabilityProjection{},
	}
	if !hollow.IsEmpty() {
		t.Error("hollow preload reports non-empty")
	}
	if hollow.Render() != "" {
		t.Errorf("hollow preload rendered non-empty: %q", hollow.Render())
	}
}

// TestForestPreload_RenderTruncatesLongLists checks the per-bucket cap
// of preloadBucketMax. A preload with 12 active intents should emit 5
// plus a "+N more" footer, not the full 12 — otherwise long memory
// tails crowd out real instructions.
func TestForestPreload_RenderTruncatesLongLists(t *testing.T) {
	var active []forest.BranchPacket
	for i := 0; i < 12; i++ {
		active = append(active, forest.BranchPacket{Branch: &forest.Branch{Title: "t", Summary: "s"}})
	}
	preload := &ForestPreload{Intents: &forest.IntentProjection{Active: active}}
	rendered := preload.Render()
	if !strings.Contains(rendered, "…+7 more") {
		t.Errorf("truncation footer missing for 12-item list: %q", rendered)
	}
}
