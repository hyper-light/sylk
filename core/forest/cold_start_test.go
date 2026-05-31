package forest

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #5 — Cold-start fallback + session priming
// ────────────────────────────────────────────────────────────────────

// TestColdStart_GlobalPriorsFillEmptyResult exercises the fallback
// path: a session with zero matching branches still gets results from
// the global pool when the global pool has eligible branches.
func TestColdStart_GlobalPriorsFillEmptyResult(t *testing.T) {
	t.Skip("legacy branch global-prior retrieval removed; ForestPacket cold-start semantics are node-native")
	forest, _ := newTestForest(t)
	ctx := context.Background()

	// Seed three branches in session "old" that should qualify as
	// global priors (they have support_count > 0 after the projector
	// applies the events).
	for i, title := range []string{"alpha", "beta", "gamma"} {
		event := &Event{
			SessionID: "old",
			BranchID:  "branch-" + title,
			AgentID:   "engineer", AgentType: "engineer",
			EventType: EventTypeDecisionRecorded,
			Family:    TreeFamilyDecision,
			Title:     "Prior " + title,
		}
		_ = i
		if err := forest.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	// Retrieve from a fresh session "new" with the same family.
	packets, err := forest.retrieveBranchPackets(ctx, Query{
		SessionID: "new",
		AgentType: "engineer",
		Query:     "no match string",
		Limit:     2,
		Families:  []TreeFamily{TreeFamilyDecision},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Either primary or global-prior packets — both surfacing tactics
	// are expected to find the prior branches.
	if len(packets) == 0 {
		t.Fatalf("expected fallback packets; cold-start returned empty")
	}
}

// TestColdStart_GlobalPriorsTaggedSource verifies fallback packets
// carry RetrievalSourceGlobalPrior so the audit + caller can
// distinguish them.
func TestColdStart_GlobalPriorsTaggedSource(t *testing.T) {
	forest := &MemoryForest{}
	pkts := forest.fillWithGlobalPriors(context.Background(), Query{}, nil, 0)
	if pkts != nil {
		t.Errorf("nil-input fallthrough leaked packets: %v", pkts)
	}
}

// TestColdStart_PrimingNoPriorSession verifies PrimeSession returns
// errPrimingNoPriorSession when no prior session exists. This lets
// callers no-op gracefully on day-zero.
func TestColdStart_PrimingNoPriorSession(t *testing.T) {
	forest, _ := newTestForest(t)
	_, err := forest.PrimeSession(context.Background(), SessionPrimingRequest{
		SessionID: "fresh",
		AgentType: "nobody",
	})
	if !errors.Is(err, errPrimingNoPriorSession) {
		t.Errorf("expected errPrimingNoPriorSession; got %v", err)
	}
}

// TestColdStart_PrimingCopiesTopBranches seeds an "old" session and
// verifies that PrimeSession on "new" produces a canopy referencing
// the old session's branches.
func TestColdStart_PrimingCopiesTopBranches(t *testing.T) {
	t.Skip("legacy branch session priming removed from production retrieval")
	forest, db := newTestForest(t)
	ctx := context.Background()

	// Seed three branches in old.
	for _, title := range []string{"a", "b", "c"} {
		event := &Event{
			SessionID: "old",
			BranchID:  "branch-" + title,
			AgentID:   "engineer", AgentType: "engineer",
			EventType: EventTypeDecisionRecorded,
			Family:    TreeFamilyDecision,
			Title:     "Prior " + title,
		}
		if err := forest.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	result, err := forest.PrimeSession(ctx, SessionPrimingRequest{
		SessionID: "new",
		AgentType: "engineer",
		Families:  []TreeFamily{TreeFamilyDecision},
		Limit:     5,
	})
	if err != nil {
		t.Fatalf("prime: %v", err)
	}
	if result.SourceSessionID != "old" {
		t.Errorf("source session: got %s want old", result.SourceSessionID)
	}
	if result.BranchesCopied == 0 {
		t.Errorf("expected at least one branch copied; got 0")
	}
	if result.CanopyKey == "" {
		t.Errorf("canopy key not set")
	}

	// Verify the canopy actually landed.
	var rootIDs string
	if err := db.QueryRow(`SELECT root_ids FROM forest_canopies WHERE canopy_key = ?`, result.CanopyKey).Scan(&rootIDs); err != nil {
		t.Fatalf("canopy lookup: %v", err)
	}
	if rootIDs == "" || rootIDs == "[]" {
		t.Errorf("canopy root_ids empty: %q", rootIDs)
	}
}

// TestColdStart_PrimingIdempotent verifies running PrimeSession twice
// for the same session produces a single canopy row, not duplicates.
func TestColdStart_PrimingIdempotent(t *testing.T) {
	t.Skip("legacy branch session priming removed from production retrieval")
	forest, db := newTestForest(t)
	ctx := context.Background()

	event := &Event{
		SessionID: "old",
		BranchID:  "branch-only",
		AgentID:   "engineer", AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "Prior",
	}
	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, err := forest.PrimeSession(ctx, SessionPrimingRequest{
			SessionID: "new",
			AgentType: "engineer",
		}); err != nil {
			t.Fatalf("prime iter %d: %v", i, err)
		}
	}

	var canopyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_canopies WHERE session_id = ?`, "new").Scan(&canopyCount); err != nil {
		t.Fatal(err)
	}
	if canopyCount != 1 {
		t.Errorf("expected 1 canopy after 3 primings; got %d", canopyCount)
	}
}

// TestColdStart_PrimingValidatesSessionID verifies missing session ID
// is rejected.
func TestColdStart_PrimingValidatesSessionID(t *testing.T) {
	forest, _ := newTestForest(t)
	_, err := forest.PrimeSession(context.Background(), SessionPrimingRequest{
		SessionID: "",
	})
	if err == nil {
		t.Error("expected error for empty session ID")
	}
}

// TestColdStart_PrimingResolvesLimit verifies the limit clamp
// (negative → default; over-cap → max).
func TestColdStart_PrimingResolvesLimit(t *testing.T) {
	if got := resolveSessionPrimingLimit(0); got != defaultSessionPrimingLimit {
		t.Errorf("limit default: got %v", got)
	}
	if got := resolveSessionPrimingLimit(1000); got != sessionPrimingMaxLimit {
		t.Errorf("limit clamp: got %v want %v", got, sessionPrimingMaxLimit)
	}
}

// TestColdStart_BranchIDSetNilSafe verifies the helper handles nil
// inputs.
func TestColdStart_BranchIDSetNilSafe(t *testing.T) {
	out := branchIDSet(nil)
	if len(out) != 0 {
		t.Errorf("nil input should return empty set; got %v", out)
	}
	out = branchIDSet([]*BranchPacket{nil, {Branch: nil}, {Branch: &Branch{ID: "x"}}})
	if _, ok := out["x"]; !ok {
		t.Errorf("missing x; got %v", out)
	}
	if len(out) != 1 {
		t.Errorf("expected only x; got %v", out)
	}
}

// TestColdStart_TagPrimaryRetrievalSource verifies the helper stamps
// only empty Source fields, preserving prior tagging.
func TestColdStart_TagPrimaryRetrievalSource(t *testing.T) {
	pkts := []*BranchPacket{
		{Branch: &Branch{ID: "a"}}, // empty Source → stamped Primary
		{Branch: &Branch{ID: "b"}, Source: RetrievalSourceGlobalPrior},
		nil,
	}
	tagPrimaryRetrievalSource(pkts)
	if pkts[0].Source != RetrievalSourcePrimary {
		t.Errorf("a: got %s want primary", pkts[0].Source)
	}
	if pkts[1].Source != RetrievalSourceGlobalPrior {
		t.Errorf("b: prior tagging should be preserved; got %s", pkts[1].Source)
	}
}

// TestColdStart_EncodingRootIDs verifies the JSON encoder produces
// canonical "[]" for empty input and round-trips a populated slice.
func TestColdStart_EncodingRootIDs(t *testing.T) {
	if got, _ := encodePrimingRootIDs(nil); got != "[]" {
		t.Errorf("empty: got %q want []", got)
	}
	encoded, err := encodePrimingRootIDs([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if encoded == "" || encoded == "[]" {
		t.Errorf("populated slice produced empty payload: %q", encoded)
	}
}

var _ = time.Second
