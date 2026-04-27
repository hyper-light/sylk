package forest

import (
	"testing"
)

// ────────────────────────────────────────────────────────────────────
// Issue #10 Phase C — substrate edge diff tests
// ────────────────────────────────────────────────────────────────────

func TestSubstrateEdgeDiff_NoChangeNoOp(t *testing.T) {
	current := map[edgeKey]substrateEdgeRow{
		{source: "a", target: "b"}: {conductance: 0.5, flux: 0.3, redundancy: 0.1, inhibition: 0.0},
	}
	desired := map[edgeKey]substrateEdgeRow{
		{source: "a", target: "b"}: {conductance: 0.5, flux: 0.3, redundancy: 0.1, inhibition: 0.0},
	}
	upsert, del := classifySubstrateEdgeDiff(current, desired)
	if len(upsert) != 0 {
		t.Errorf("no-change should produce 0 upserts; got %d", len(upsert))
	}
	if len(del) != 0 {
		t.Errorf("no-change should produce 0 deletes; got %d", len(del))
	}
}

func TestSubstrateEdgeDiff_AddedEdge(t *testing.T) {
	current := map[edgeKey]substrateEdgeRow{}
	desired := map[edgeKey]substrateEdgeRow{
		{source: "a", target: "b"}: {conductance: 0.5},
	}
	upsert, del := classifySubstrateEdgeDiff(current, desired)
	if len(upsert) != 1 {
		t.Errorf("added edge: got %d upserts want 1", len(upsert))
	}
	if len(del) != 0 {
		t.Errorf("added edge: got %d deletes want 0", len(del))
	}
}

func TestSubstrateEdgeDiff_RemovedEdge(t *testing.T) {
	current := map[edgeKey]substrateEdgeRow{
		{source: "a", target: "b"}: {conductance: 0.5},
	}
	desired := map[edgeKey]substrateEdgeRow{}
	upsert, del := classifySubstrateEdgeDiff(current, desired)
	if len(upsert) != 0 {
		t.Errorf("removed edge: got %d upserts want 0", len(upsert))
	}
	if len(del) != 1 || del[0] != (edgeKey{source: "a", target: "b"}) {
		t.Errorf("removed edge: got deletes %v", del)
	}
}

func TestSubstrateEdgeDiff_ChangedEdge(t *testing.T) {
	current := map[edgeKey]substrateEdgeRow{
		{source: "a", target: "b"}: {conductance: 0.5, flux: 0.3, redundancy: 0.1, inhibition: 0.0},
	}
	desired := map[edgeKey]substrateEdgeRow{
		{source: "a", target: "b"}: {conductance: 0.7, flux: 0.3, redundancy: 0.1, inhibition: 0.0},
	}
	upsert, del := classifySubstrateEdgeDiff(current, desired)
	if len(upsert) != 1 {
		t.Errorf("changed edge: got %d upserts want 1", len(upsert))
	}
	if len(del) != 0 {
		t.Errorf("changed edge: got %d deletes want 0", len(del))
	}
}

func TestSubstrateEdgeDiff_EpsilonTolerance(t *testing.T) {
	// Difference smaller than epsilon → no upsert.
	current := map[edgeKey]substrateEdgeRow{
		{source: "a", target: "b"}: {conductance: 0.5, flux: 0.3, redundancy: 0.1, inhibition: 0.0},
	}
	desired := map[edgeKey]substrateEdgeRow{
		{source: "a", target: "b"}: {conductance: 0.5 + substrateEdgeEpsilon/10, flux: 0.3, redundancy: 0.1, inhibition: 0.0},
	}
	upsert, _ := classifySubstrateEdgeDiff(current, desired)
	if len(upsert) != 0 {
		t.Errorf("sub-epsilon change should not upsert; got %d", len(upsert))
	}
}

func TestSubstrateEdgeDiff_MixedChange(t *testing.T) {
	// 1 unchanged, 1 added, 1 removed, 1 changed → expect 2 upserts (added + changed) + 1 delete.
	current := map[edgeKey]substrateEdgeRow{
		{source: "a", target: "b"}: {conductance: 0.5},
		{source: "a", target: "c"}: {conductance: 0.4}, // unchanged
		{source: "c", target: "d"}: {conductance: 0.2}, // removed
	}
	desired := map[edgeKey]substrateEdgeRow{
		{source: "a", target: "b"}: {conductance: 0.7}, // changed
		{source: "a", target: "c"}: {conductance: 0.4}, // unchanged
		{source: "x", target: "y"}: {conductance: 0.1}, // added
	}
	upsert, del := classifySubstrateEdgeDiff(current, desired)
	if len(upsert) != 2 {
		t.Errorf("mixed: got %d upserts want 2", len(upsert))
	}
	if len(del) != 1 {
		t.Errorf("mixed: got %d deletes want 1", len(del))
	}
}

func TestSubstrateEdgeDiff_FloatsCloseEdgeCases(t *testing.T) {
	if !floatsClose(0.5, 0.5) {
		t.Error("identical floats should be close")
	}
	if !floatsClose(0.5, 0.5+substrateEdgeEpsilon/2) {
		t.Error("sub-epsilon difference should be close")
	}
	if floatsClose(0.5, 0.5+substrateEdgeEpsilon*10) {
		t.Error("super-epsilon difference should NOT be close")
	}
	// Negative vs positive diff symmetry.
	if !floatsClose(0.5+substrateEdgeEpsilon/2, 0.5) {
		t.Error("symmetric sub-epsilon should be close")
	}
}

func TestSubstrateEdgeDiff_SubstrateEdgesToMap(t *testing.T) {
	edges := []substrateEdge{
		{SourceID: "a", TargetID: "b", Conductance: 0.5, Flux: 0.3},
		{SourceID: "c", TargetID: "d", Conductance: 0.4},
	}
	m := substrateEdgesToMap(edges)
	if len(m) != 2 {
		t.Errorf("got %d entries want 2", len(m))
	}
	if m[edgeKey{source: "a", target: "b"}].conductance != 0.5 {
		t.Errorf("a→b conductance: got %v", m[edgeKey{source: "a", target: "b"}].conductance)
	}
}
