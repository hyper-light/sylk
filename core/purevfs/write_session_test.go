package purevfs

import (
	"errors"
	"io/fs"
	"testing"
)

// TestWriteSession_WritesUntilSeal pins the basic happy path: a session
// writes freely while live, Seal returns the head snapshot, and the
// session itself rejects further writes after Seal.
func TestWriteSession_WritesUntilSeal(t *testing.T) {
	w := NewWorkspace(NewChunkStore(0), nil)
	defer w.Close()

	sess := w.BeginWriteSession("main")

	if _, err := sess.WriteFile("/a.txt", []byte("hello"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	handoff, err := sess.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if handoff.Branch != "main" {
		t.Fatalf("handoff branch = %q, want main", handoff.Branch)
	}
	if handoff.Generation != 1 {
		t.Fatalf("handoff gen = %d, want 1", handoff.Generation)
	}
	if handoff.SnapshotID == 0 {
		t.Fatal("handoff snapshot id is zero")
	}

	// Post-seal writes through the same session reject.
	_, err = sess.WriteFile("/b.txt", []byte("nope"), 0o644)
	if !errors.Is(err, ErrSessionSealed) {
		t.Fatalf("post-seal write err = %v, want ErrSessionSealed", err)
	}

	// Second seal also rejects (idempotent in spirit, errors to caller).
	_, err = sess.Seal()
	if !errors.Is(err, ErrSessionSealed) {
		t.Fatalf("second seal err = %v, want ErrSessionSealed", err)
	}
}

// TestWriteSession_PredecessorFencedBySuccessor is the protocol's whole
// point: predecessor opens a session, successor opens (still at gen 0
// — same as predecessor since no Seal yet), but as soon as successor
// Seals, predecessor's writes are stale.
//
// Realistic handoff: predecessor Seals first, then successor opens at
// gen 1; both stages must work.
func TestWriteSession_PredecessorFencedBySuccessor(t *testing.T) {
	w := NewWorkspace(NewChunkStore(0), nil)
	defer w.Close()

	pred := w.BeginWriteSession("main")
	if _, err := pred.WriteFile("/pred.txt", []byte("by predecessor"), 0o644); err != nil {
		t.Fatalf("pred initial write: %v", err)
	}

	handoff, err := pred.Seal()
	if err != nil {
		t.Fatalf("pred seal: %v", err)
	}

	succ := w.BeginWriteSession("main")
	if got := succ.Generation(); got != handoff.Generation {
		t.Fatalf("successor gen = %d, want %d", got, handoff.Generation)
	}

	// A laggard write attempt by predecessor (already sealed itself,
	// but hypothetically a long-running tool call).
	_, err = pred.WriteFile("/late.txt", []byte("too late"), 0o644)
	if !errors.Is(err, ErrSessionSealed) {
		t.Fatalf("post-handoff pred write err = %v, want ErrSessionSealed", err)
	}

	// Successor writes succeed.
	if _, err := succ.WriteFile("/succ.txt", []byte("by successor"), 0o644); err != nil {
		t.Fatalf("succ write: %v", err)
	}
}

// TestWriteSession_StaleSessionRejectedAfterPeerSeal exercises the case
// where two WriteSessions are opened at the same generation, one Seals,
// and the other discovers it is fenced (ErrStaleGeneration, not
// ErrSessionSealed — the difference matters: predecessor knows it was
// overtaken, not that it sealed itself).
func TestWriteSession_StaleSessionRejectedAfterPeerSeal(t *testing.T) {
	w := NewWorkspace(NewChunkStore(0), nil)
	defer w.Close()

	a := w.BeginWriteSession("main")
	b := w.BeginWriteSession("main")
	if a.Generation() != b.Generation() {
		t.Fatalf("opened at different gens: %d vs %d", a.Generation(), b.Generation())
	}

	// b seals first; a should now be fenced.
	if _, err := b.Seal(); err != nil {
		t.Fatalf("b seal: %v", err)
	}
	if !a.IsFenced() {
		t.Fatal("expected a to report IsFenced after b sealed")
	}
	_, err := a.WriteFile("/a.txt", []byte("nope"), 0o644)
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("fenced write err = %v, want ErrStaleGeneration", err)
	}
}

// TestWriteSession_SeparateBranchesAreIndependent ensures fences are
// per-branch: sealing main does not affect a session on dev.
func TestWriteSession_SeparateBranchesAreIndependent(t *testing.T) {
	w := NewWorkspace(NewChunkStore(0), nil)
	defer w.Close()

	if err := w.CreateBranch("dev", 1); err != nil {
		t.Fatalf("create dev: %v", err)
	}

	mainSess := w.BeginWriteSession("main")
	devSess := w.BeginWriteSession("dev")

	if _, err := mainSess.Seal(); err != nil {
		t.Fatalf("main seal: %v", err)
	}
	if devSess.IsFenced() {
		t.Fatal("dev session should not be fenced by main seal")
	}
	if _, err := devSess.WriteFile("/dev.txt", []byte("ok"), 0o644); err != nil {
		t.Fatalf("dev write after main seal: %v", err)
	}
}

// TestWorkspace_GenerationLazyInit verifies the branchGens machinery
// is allocated lazily — workspaces that never use seal pay no cost
// for the feature.
func TestWorkspace_GenerationLazyInit(t *testing.T) {
	w := NewWorkspace(NewChunkStore(0), nil)
	defer w.Close()

	// No seal use: generation reads return 0 without allocating.
	if got := w.Generation("main"); got != 0 {
		t.Fatalf("unused branch gen = %d, want 0", got)
	}
	if w.branchGens != nil {
		t.Fatal("branchGens should be lazily nil before first session")
	}

	// First session creates the generations map.
	_ = w.BeginWriteSession("main")
	if w.branchGens == nil {
		t.Fatal("branchGens should be initialized after BeginWriteSession")
	}
	if got := w.Generation("main"); got != 0 {
		t.Fatalf("fresh branch gen = %d, want 0", got)
	}
	_ = fs.ModePerm // keep io/fs import grouping consistent
}
