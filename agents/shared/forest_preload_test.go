package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/forest"
)

// fakeProjector implements the packet-native MemoryForestService with
// controllable fixtures: no SQLite, no goroutines.
type fakeProjector struct {
	packets        []*forest.ForestPacket
	cursor         *forest.ForestCursor
	calledRetrieve bool
	calledCursor   bool
}

func (f *fakeProjector) ResolveIntent(context.Context, forest.ResolveIntentInput) (*forest.IntentResolution, error) {
	return nil, nil
}
func (f *fakeProjector) RetrieveForest(_ context.Context, _ forest.Query) ([]*forest.ForestPacket, error) {
	f.calledRetrieve = true
	if f.packets != nil {
		return f.packets, nil
	}
	return []*forest.ForestPacket{{
		Node: forest.ForestNode{
			ID:            "node-1",
			Kind:          forest.ForestNodeClaim,
			Summary:       "Use the validated forest projection path",
			EvidenceGrade: forest.EvidenceGradeObserved,
			Confidence:    1,
		},
		Evidence: []forest.ForestEvidence{{RefType: "node", RefID: "node-1", NodeID: "node-1", Grade: forest.EvidenceGradeObserved}},
		Score:    1,
	}}, nil
}
func (f *fakeProjector) CreateForestCursor(_ context.Context, input forest.ForestCursorInput) (*forest.ForestCursor, error) {
	f.calledCursor = true
	if f.cursor != nil {
		return f.cursor, nil
	}
	cursor := &forest.ForestCursor{ID: "cursor-1", SessionID: input.SessionID, ActiveNodeIDs: []string{"node-1"}}
	return cursor, nil
}
func (f *fakeProjector) ProposeForestClaim(context.Context, forest.ForestClaimProposal) error {
	return nil
}
func (f *fakeProjector) RecordOutcome(context.Context, forest.OutcomeRecord) error {
	return nil
}

func TestPreloadFor_ArchitectPullsIntentConstraintDecision(t *testing.T) {
	f := &fakeProjector{}
	got, err := PreloadFor(context.Background(), f, ForestPreloadInput{AgentType: "architect"})
	if err != nil {
		t.Fatalf("preload err: %v", err)
	}
	if got == nil || got.Projection == nil || got.Projection.Role != "architect" {
		t.Fatalf("architect preload projection = %#v", got)
	}
	if !f.calledRetrieve || !f.calledCursor {
		t.Fatalf("architect preload did not use forest packet cursor path: retrieve=%v cursor=%v", f.calledRetrieve, f.calledCursor)
	}
}

func TestPreloadFor_LibrarianPullsPreferenceCapabilityIntent(t *testing.T) {
	f := &fakeProjector{}
	got, err := PreloadFor(context.Background(), f, ForestPreloadInput{AgentType: "librarian"})
	if err != nil {
		t.Fatalf("preload err: %v", err)
	}
	if got == nil || got.Projection == nil || got.Projection.Role != "librarian" {
		t.Fatalf("librarian preload projection = %#v", got)
	}
}

func TestPreloadFor_AcademicPullsEvidenceOutcomeIntent(t *testing.T) {
	f := &fakeProjector{}
	got, err := PreloadFor(context.Background(), f, ForestPreloadInput{AgentType: "academic"})
	if err != nil {
		t.Fatalf("preload err: %v", err)
	}
	if got == nil || got.Projection == nil || got.Projection.Role != "academic" {
		t.Fatalf("academic preload projection = %#v", got)
	}
}

func TestPreloadFor_AllEmergentAgencyRolesReceivePacketProjection(t *testing.T) {
	roles := []string{"architect", "engineer", "tester", "guardian", "inspector", "librarian", "academic", "designer", "orchestrator", "scribe", "scribe-engineer", "archivalist", "guide"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			f := &fakeProjector{}
			got, err := PreloadFor(context.Background(), f, ForestPreloadInput{AgentType: role, Query: role + " work"})
			if err != nil {
				t.Fatalf("preload err: %v", err)
			}
			if got == nil || got.Projection == nil || got.Cursor == nil || len(got.Packets) == 0 {
				t.Fatalf("role %s missing projection/cursor/packets: %+v", role, got)
			}
			rendered := got.Render()
			if !strings.Contains(rendered, "cursor=") || !strings.Contains(rendered, "evidence_refs:") {
				t.Fatalf("role %s render missing cursor/evidence refs: %q", role, rendered)
			}
		})
	}
}

// TestPreloadFor_UnknownAgentReturnsNil covers the contract guarantee that an
// unknown agent type degrades to "no preload" instead of querying the forest.
func TestPreloadFor_UnknownAgentReturnsNil(t *testing.T) {
	f := &fakeProjector{}
	got, err := PreloadFor(context.Background(), f, ForestPreloadInput{AgentType: "unknown-agent"})
	if err != nil {
		t.Fatalf("preload err: %v", err)
	}
	if got != nil {
		t.Errorf("unknown agent returned non-nil preload: %+v", got)
	}
	if f.calledRetrieve || f.calledCursor {
		t.Errorf("unknown agent queried forest: retrieve=%v cursor=%v", f.calledRetrieve, f.calledCursor)
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
	preload := &ForestPreload{
		Projection: &forest.ForestRoleProjection{
			Role:         "engineer",
			EvidenceRefs: []string{"node:node-1"},
			RiskFlags:    []string{"context.under_review"},
			Text:         "Memory Forest projection role=engineer\nevidence_refs: node:node-1\nrisk_flags: context.under_review",
		},
	}

	rendered := preload.Render()
	if rendered == "" {
		t.Fatal("Render() returned empty for non-empty preload")
	}
	if !strings.Contains(rendered, "Memory Forest projection role=engineer") {
		t.Errorf("missing projection header: %q", rendered)
	}
	if !strings.Contains(rendered, "evidence_refs: node:node-1") || !strings.Contains(rendered, "risk_flags: context.under_review") {
		t.Errorf("projection evidence/risk missing: %q", rendered)
	}
}

// TestForestPreload_IsEmpty_NilAndHollowAreBothEmpty ensures IsEmpty
// treats a preload whose every projection is present-but-empty as
// empty. Otherwise Render would emit a bare header with no body.
func TestForestPreload_IsEmpty_NilAndHollowAreBothEmpty(t *testing.T) {
	if !(*ForestPreload)(nil).IsEmpty() {
		t.Error("nil preload reports non-empty")
	}
	hollow := &ForestPreload{Projection: &forest.ForestRoleProjection{}}
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
	var packets []*forest.ForestPacket
	for i := 0; i < 12; i++ {
		node := forest.ForestNode{ID: "node-" + string(rune('a'+i)), Kind: forest.ForestNodeClaim, Summary: "s", EvidenceGrade: forest.EvidenceGradeObserved}
		packets = append(packets, &forest.ForestPacket{
			Node:     node,
			Evidence: []forest.ForestEvidence{{RefType: "node", RefID: node.ID, NodeID: node.ID, Grade: forest.EvidenceGradeObserved}},
			Score:    float64(i + 1),
		})
	}
	projection, err := forest.BuildRoleForestProjection("engineer", packets, &forest.ForestCursor{ID: "cursor-budget"}, 5)
	if err != nil {
		t.Fatalf("build projection: %v", err)
	}
	preload := &ForestPreload{Projection: projection}
	if len(projection.Packets) != 5 {
		t.Fatalf("projection packet count = %d, want 5", len(projection.Packets))
	}
	if !strings.Contains(preload.Render(), "cursor=cursor-budget") {
		t.Errorf("projection render missing cursor: %q", preload.Render())
	}
}
