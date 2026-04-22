package versioning

import (
	"testing"
)

func TestCopyRetention_RetainReleaseRefCount(t *testing.T) {
	r := NewCopyRetention(CopyRetentionConfig{})
	v1 := SemanticVersion{Minor: 1}

	r.Retain(v1, "replica-1")
	if got := r.RefCount(v1); got != 1 {
		t.Fatalf("refcount = %d, want 1", got)
	}
	r.Retain(v1, "replica-2")
	if got := r.RefCount(v1); got != 2 {
		t.Fatalf("refcount = %d, want 2", got)
	}
	r.Release("replica-1")
	if got := r.RefCount(v1); got != 1 {
		t.Fatalf("refcount after release = %d, want 1", got)
	}
	r.Release("replica-2")
	if got := r.RefCount(v1); got != 0 {
		t.Fatalf("refcount after both released = %d, want 0", got)
	}
}

func TestCopyRetention_RetainMoveReleasesOld(t *testing.T) {
	r := NewCopyRetention(CopyRetentionConfig{})
	v1 := SemanticVersion{Minor: 1}
	v2 := SemanticVersion{Minor: 2}

	r.Retain(v1, "h")
	r.Retain(v2, "h") // same holder, different version
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

	// v1 is held by replica, v2 is not held.
	r.Retain(v1, "replica-1")

	known := []SemanticVersion{v1, v2}
	gotReleased := r.AdvanceWaterLine(v3, known)
	// v2 below water line, no refs → released.
	// v1 below water line but still held → retained.
	if len(gotReleased) != 1 || gotReleased[0] != v2 {
		t.Fatalf("released = %v, want [v2]", gotReleased)
	}
	if len(released) != 1 || released[0] != v2 {
		t.Fatalf("callback release = %v, want [v2]", released)
	}

	// Advancing to a lower water line is a no-op.
	lower := SemanticVersion{Minor: 1}
	gotReleased = r.AdvanceWaterLine(lower, known)
	if len(gotReleased) != 0 {
		t.Fatalf("released on lower water = %v, want none", gotReleased)
	}
}
