package versioning

import (
	"testing"
)

// retain/release/advance test helpers — panic on WAL errors so test
// bodies stay focused on retention semantics. Production callers
// propagate the errors.
func retain(t *testing.T, r *CopyRetention, ver SemanticVersion, holderID string) {
	t.Helper()
	if err := r.Retain(ver, holderID); err != nil {
		t.Fatalf("retain: %v", err)
	}
}
func release(t *testing.T, r *CopyRetention, holderID string) {
	t.Helper()
	if err := r.Release(holderID); err != nil {
		t.Fatalf("release: %v", err)
	}
}
func advance(t *testing.T, r *CopyRetention, newLine SemanticVersion, known []SemanticVersion) []SemanticVersion {
	t.Helper()
	rel, err := r.AdvanceWaterLine(newLine, known)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	return rel
}

func TestCopyRetention_RetainReleaseRefCount(t *testing.T) {
	r := NewCopyRetention(CopyRetentionConfig{})
	v1 := SemanticVersion{Minor: 1}

	retain(t, r, v1, "replica-1")
	if got := r.RefCount(v1); got != 1 {
		t.Fatalf("refcount = %d, want 1", got)
	}
	retain(t, r, v1, "replica-2")
	if got := r.RefCount(v1); got != 2 {
		t.Fatalf("refcount = %d, want 2", got)
	}
	release(t, r, "replica-1")
	if got := r.RefCount(v1); got != 1 {
		t.Fatalf("refcount after release = %d, want 1", got)
	}
	release(t, r, "replica-2")
	if got := r.RefCount(v1); got != 0 {
		t.Fatalf("refcount after both released = %d, want 0", got)
	}
}

func TestCopyRetention_RetainMoveReleasesOld(t *testing.T) {
	r := NewCopyRetention(CopyRetentionConfig{})
	v1 := SemanticVersion{Minor: 1}
	v2 := SemanticVersion{Minor: 2}

	retain(t, r, v1, "h")
	retain(t, r, v2, "h")
	if got := r.RefCount(v1); got != 0 {
		t.Fatalf("v1 ref after move = %d, want 0", got)
	}
	if got := r.RefCount(v2); got != 1 {
		t.Fatalf("v2 ref after move = %d, want 1", got)
	}
}

func TestCopyRetention_AdvanceWaterLineReleasesBelow(t *testing.T) {
	var released []SemanticVersion
	r := NewCopyRetention(CopyRetentionConfig{
		OnRelease: func(rv []SemanticVersion) { released = append(released, rv...) },
	})
	v1 := SemanticVersion{Minor: 1}
	v2 := SemanticVersion{Minor: 2}
	v3 := SemanticVersion{Minor: 3}

	retain(t, r, v1, "replica-1")

	known := []SemanticVersion{v1, v2}
	gotReleased := advance(t, r, v3, known)
	if len(gotReleased) != 1 || gotReleased[0] != v2 {
		t.Fatalf("released = %v, want [v2]", gotReleased)
	}
	if len(released) != 1 || released[0] != v2 {
		t.Fatalf("callback release = %v, want [v2]", released)
	}

	lower := SemanticVersion{Minor: 1}
	gotReleased = advance(t, r, lower, known)
	if len(gotReleased) != 0 {
		t.Fatalf("released on lower water = %v, want none", gotReleased)
	}
}

// TestCopyRetention_DurableAcrossReopen pins the retention-durability
// invariant: every Retain, Release, and AdvanceWaterLine is persisted
// to the ControlWAL before in-memory state mutates, so the retention
// topology rebuilds identically on restart.
func TestCopyRetention_DurableAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	r1 := NewCopyRetention(CopyRetentionConfig{WAL: wal})

	v1 := SemanticVersion{Minor: 1}
	v2 := SemanticVersion{Minor: 2}
	v3 := SemanticVersion{Minor: 3}

	retain(t, r1, v1, "replica-A")
	retain(t, r1, v2, "replica-B")
	retain(t, r1, v3, "replica-C")
	release(t, r1, "replica-A")
	if _, err := r1.AdvanceWaterLine(v2, []SemanticVersion{v1, v2, v3}); err != nil {
		t.Fatalf("advance: %v", err)
	}

	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}

	wal2, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	defer wal2.Close()

	r2 := NewCopyRetention(CopyRetentionConfig{WAL: wal2})
	if err := wal2.Replay(func(e *ControlEntry) error {
		r2.ApplyControlEntry(e)
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}

	if got := r2.RefCount(v1); got != 0 {
		t.Errorf("v1 refcount after replay = %d, want 0 (A released)", got)
	}
	if got := r2.RefCount(v2); got != 1 {
		t.Errorf("v2 refcount after replay = %d, want 1", got)
	}
	if got := r2.RefCount(v3); got != 1 {
		t.Errorf("v3 refcount after replay = %d, want 1", got)
	}
	if got := r2.WaterLine(); got != v2 {
		t.Errorf("water line after replay = %v, want %v", got, v2)
	}
}
