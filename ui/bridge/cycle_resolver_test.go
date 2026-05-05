package bridge

import (
	"testing"
)

// Cycle resolver behavior — UI_DESIGN.md §5 + §7 P4.

func TestCycleResolver_OpenOnFreshTopLevelClaim(t *testing.T) {
	r := newCycleResolver()
	out := r.onClaimCreated("c1", "architect", "engineer", "", "")
	if out.CycleOpened == nil || out.CycleOpened.CycleID != "c1" || out.CycleOpened.OwnerAgentID != "architect" {
		t.Fatalf("unexpected outcome: %+v", out)
	}
	if out.PredecessorClosed != nil {
		t.Fatalf("unparented claim must not close anything, got predecessor=%+v", out.PredecessorClosed)
	}
	if out.AttachedToCycle != nil {
		t.Fatalf("unparented claim must not attach to a cycle, got %+v", out.AttachedToCycle)
	}
	if got := r.CycleForClaim("c1"); got != "c1" {
		t.Fatalf("CycleForClaim(c1)=%q, want c1", got)
	}
}

func TestCycleResolver_AttachesChildToParentCycle(t *testing.T) {
	r := newCycleResolver()
	r.onClaimCreated("root", "architect", "engineer", "", "")
	out := r.onClaimCreated("child", "engineer", "designer", "root", "")
	if out.CycleOpened != nil {
		t.Fatalf("expected no new cycle, got %+v", out.CycleOpened)
	}
	if out.AttachedToCycle == nil || out.AttachedToCycle.CycleID != "root" {
		t.Fatalf("expected attachment to root cycle, got %+v", out.AttachedToCycle)
	}
	if got := r.CycleForClaim("child"); got != "root" {
		t.Fatalf("CycleForClaim(child)=%q, want root", got)
	}
}

func TestCycleResolver_HandoffClosesPredecessorAndOpensSuccessor(t *testing.T) {
	r := newCycleResolver()
	r.onClaimCreated("predecessor-root", "guardian", "guardian", "", "")
	out := r.onClaimCreated("successor-root", "inspector", "inspector", "", "predecessor-root")
	if out.CycleOpened == nil || out.CycleOpened.CycleID != "successor-root" || out.CycleOpened.OwnerAgentID != "inspector" {
		t.Fatalf("unexpected successor cycle: %+v", out.CycleOpened)
	}
	if out.PredecessorClosed == nil {
		t.Fatal("expected predecessor cycle to close immediately on handoff")
	}
	if out.PredecessorClosed.CycleID != "predecessor-root" {
		t.Fatalf("PredecessorClosed.CycleID = %q, want predecessor-root", out.PredecessorClosed.CycleID)
	}
	if out.PredecessorClosed.OwnerAgentID != "guardian" {
		t.Fatalf("PredecessorClosed.OwnerAgentID = %q, want guardian", out.PredecessorClosed.OwnerAgentID)
	}
	// Predecessor cycle state torn down.
	if r.CycleForClaim("predecessor-root") != "" {
		t.Fatal("predecessor cycle should be torn down after handoff")
	}
}

func TestCycleResolver_HandoffWithUnknownPredecessor_StillOpensSuccessor(t *testing.T) {
	r := newCycleResolver()
	// No predecessor registered.
	out := r.onClaimCreated("successor-root", "inspector", "inspector", "", "ghost-predecessor")
	if out.CycleOpened == nil {
		t.Fatal("expected successor cycle to open even with unknown predecessor")
	}
	if out.PredecessorClosed != nil {
		t.Fatalf("expected nil PredecessorClosed for unknown predecessor, got %+v", out.PredecessorClosed)
	}
}

func TestCycleResolver_DrainsOnClaimAndArtifactClosed(t *testing.T) {
	r := newCycleResolver()
	r.onClaimCreated("root", "architect", "engineer", "", "")
	r.onArtifactStarted("art-1", "root")
	// Closing the claim doesn't drain — the artifact is still in-flight.
	if drained := r.onClaimClosed("root"); drained != nil {
		t.Fatal("cycle should not drain while artifact is in-flight")
	}
	// Pairing the completion artifact drains the cycle.
	cycle, drained := r.onArtifactCompleted("art-1")
	if !drained {
		t.Fatal("expected drain after final artifact paired")
	}
	if cycle == nil || cycle.CycleID != "root" {
		t.Fatalf("unexpected drained cycle: %+v", cycle)
	}
	// Cycle state torn down.
	if r.CycleForClaim("root") != "" {
		t.Fatal("expected claim mapping to be cleared after drain")
	}
}

func TestCycleResolver_OutOfOrderChildAttachesAfterParent(t *testing.T) {
	r := newCycleResolver()
	// Child arrives before parent — should defer.
	out := r.onClaimCreated("child", "engineer", "designer", "missing-parent", "")
	if out.CycleOpened != nil || out.AttachedToCycle != nil || out.PredecessorClosed != nil {
		t.Fatalf("out-of-order child should yield empty outcome, got %+v", out)
	}
	if r.CycleForClaim("child") != "" {
		t.Fatal("child should not be attached yet")
	}
	// Parent arrives — child should now be attached to parent's cycle.
	r.onClaimCreated("missing-parent", "architect", "engineer", "", "")
	if got := r.CycleForClaim("child"); got != "missing-parent" {
		t.Fatalf("CycleForClaim(child)=%q, want missing-parent after parent arrival", got)
	}
}

func TestCycleResolver_OrphanCompletion_SafelyDropped(t *testing.T) {
	r := newCycleResolver()
	// Completion arrives without a matching start — must not panic, no drain.
	cycle, drained := r.onArtifactCompleted("never-started")
	if cycle != nil || drained {
		t.Fatalf("orphan completion should produce nil/false, got %+v drained=%v", cycle, drained)
	}
}

func TestCycleResolver_DuplicateCompletionDeduplicated(t *testing.T) {
	r := newCycleResolver()
	r.onClaimCreated("root", "architect", "engineer", "", "")
	r.onArtifactStarted("art-1", "root")
	// Close the claim first; cycle still has the in-flight artifact.
	if drained := r.onClaimClosed("root"); drained != nil {
		t.Fatal("cycle should not drain while artifact in flight")
	}
	// First completion drains the cycle (now both maps empty).
	if cycle, drained := r.onArtifactCompleted("art-1"); !drained || cycle == nil {
		t.Fatalf("first completion should drain cycle (got drained=%v cycle=%+v)", drained, cycle)
	}
	// Second completion of the same started artifact must not re-drain
	// or panic. Cycle state has already been torn down.
	cycle, drained := r.onArtifactCompleted("art-1")
	if drained {
		t.Fatal("duplicate completion should not drain again")
	}
	if cycle != nil {
		t.Fatalf("duplicate completion returned cycle %+v", cycle)
	}
}

func TestCycleResolver_DeepCausedByChain(t *testing.T) {
	r := newCycleResolver()
	r.onClaimCreated("root", "architect", "engineer", "", "")
	r.onClaimCreated("level1", "engineer", "designer", "root", "")
	r.onClaimCreated("level2", "designer", "tester", "level1", "")
	r.onClaimCreated("level3", "tester", "guardian", "level2", "")
	for _, id := range []string{"root", "level1", "level2", "level3"} {
		if got := r.CycleForClaim(id); got != "root" {
			t.Fatalf("CycleForClaim(%s)=%q, want root", id, got)
		}
	}
}

func TestCycleResolver_Reset_ClearsAllState(t *testing.T) {
	r := newCycleResolver()
	r.onClaimCreated("root", "architect", "engineer", "", "")
	r.onArtifactStarted("art-1", "root")
	r.Reset()
	if r.CycleForClaim("root") != "" {
		t.Fatal("Reset should clear claim cycle map")
	}
	if r.CycleForArtifact("art-1") != "" {
		t.Fatal("Reset should clear started artifact map")
	}
}

func TestCycleResolver_AgentReplacementOnHandoff(t *testing.T) {
	r := newCycleResolver()
	r.onClaimCreated("c1", "guardian", "guardian", "", "")
	r.onClaimCreated("c2", "inspector", "inspector", "", "c1")
	r.mu.Lock()
	if r.agentCycle["inspector"] != "c2" {
		t.Fatalf("agentCycle[inspector]=%q, want c2", r.agentCycle["inspector"])
	}
	r.mu.Unlock()
}

// Race test: many concurrent claim/artifact operations should not
// trigger -race violations and should leave the resolver in a
// consistent state. Run under -race.
func TestCycleResolver_ConcurrentAccess(t *testing.T) {
	r := newCycleResolver()
	const N = 64
	done := make(chan struct{}, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			rootID := "r" + itoa(i)
			r.onClaimCreated(rootID, "agent", "subj", "", "")
			r.onArtifactStarted("a"+itoa(i), rootID)
			r.onArtifactCompleted("a" + itoa(i))
			r.onClaimClosed(rootID)
		}(i)
	}
	for i := 0; i < N; i++ {
		<-done
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
