package zeroleak

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuard_NoLeak_PassesCleanly verifies the happy path: a test that makes
// no disk writes during its body should pass without any diagnostic.
func TestGuard_NoLeak_PassesCleanly(t *testing.T) {
	ws := t.TempDir()
	Guard(t, ws)
	// No writes here — t.Cleanup will fire Guard's Verify, which should
	// find nothing new and produce no error.
}

// TestLeaks_DetectsShallowWrite verifies a single new file in the guarded
// directory is reported. Uses Leaks() directly rather than Guard/Verify so
// that the test itself does not fail on the simulated leak.
func TestLeaks_DetectsShallowWrite(t *testing.T) {
	ws := t.TempDir()
	snap := TakeSnapshot(t, ws)
	if err := os.WriteFile(filepath.Join(ws, "leaked.txt"), []byte("oops"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	leaks := snap.Leaks()
	if len(leaks) != 1 {
		t.Fatalf("leaks = %v, want exactly 1", leaks)
	}
	if !strings.HasSuffix(leaks[0], "/leaked.txt") {
		t.Fatalf("leak path = %q, want suffix /leaked.txt", leaks[0])
	}
}

// TestLeaks_DetectsRecursiveWrites verifies nested directories + files are
// fully enumerated, not just the top-level new entry. This mirrors a real
// leak pattern: `npm install` creating `node_modules/.pnpm/cache/foo.tgz`.
func TestLeaks_DetectsRecursiveWrites(t *testing.T) {
	ws := t.TempDir()
	snap := TakeSnapshot(t, ws)
	nested := filepath.Join(ws, "node_modules", ".pnpm", "cache")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "fake-pkg.tgz"), []byte("tar"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	leaks := snap.Leaks()
	// Expect node_modules, node_modules/.pnpm, node_modules/.pnpm/cache,
	// and node_modules/.pnpm/cache/fake-pkg.tgz — all four as distinct
	// entries so the diagnostic is specific.
	if len(leaks) != 4 {
		t.Fatalf("leaks = %v, want 4", leaks)
	}
}

// TestLeaks_MultiplePathsAreAllChecked verifies every path passed to
// TakeSnapshot is scanned, not just the first. A leak into path #2 must
// appear in the result even if path #1 is clean.
func TestLeaks_MultiplePathsAreAllChecked(t *testing.T) {
	clean := t.TempDir()
	dirty := t.TempDir()
	snap := TakeSnapshot(t, clean, dirty)
	if err := os.WriteFile(filepath.Join(dirty, "sneaky.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	leaks := snap.Leaks()
	if len(leaks) != 1 || !strings.HasSuffix(leaks[0], "/sneaky.txt") {
		t.Fatalf("leaks = %v, want one entry under %q", leaks, dirty)
	}
}

// TestLeaks_MissingPathBaselineIsEmpty verifies snapshotting a path that
// doesn't yet exist is well-defined: the baseline is empty, and any inode
// that later appears under it is a leak. This matches real usage where a
// broker mount point only materializes during the operation being tested.
func TestLeaks_MissingPathBaselineIsEmpty(t *testing.T) {
	parent := t.TempDir()
	willCreate := filepath.Join(parent, "mnt-to-be-created")
	snap := TakeSnapshot(t, willCreate)
	if err := os.MkdirAll(willCreate, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(willCreate, "inside"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	leaks := snap.Leaks()
	if len(leaks) != 1 || !strings.HasSuffix(leaks[0], "/inside") {
		t.Fatalf("leaks = %v, want one entry for /inside", leaks)
	}
}

// TestLeaks_DeletionsAreNotFlagged verifies the harness is write-only: a
// test that deletes a file it pre-populated should produce no leaks,
// because the invariant is about new inodes, not about any change.
func TestLeaks_DeletionsAreNotFlagged(t *testing.T) {
	ws := t.TempDir()
	existing := filepath.Join(ws, "existed-before-snapshot")
	if err := os.WriteFile(existing, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}
	snap := TakeSnapshot(t, ws)
	if err := os.Remove(existing); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if leaks := snap.Leaks(); len(leaks) != 0 {
		t.Fatalf("leaks = %v, want 0 (deletion is not a leak)", leaks)
	}
}

// TestLeaks_ExistingEntriesAreNotFlagged verifies that files present at
// snapshot time remain unflagged, even after the test body reads from or
// writes-in-place to them. Only brand-new inodes count.
func TestLeaks_ExistingEntriesAreNotFlagged(t *testing.T) {
	ws := t.TempDir()
	existing := filepath.Join(ws, "pre-existing.txt")
	if err := os.WriteFile(existing, []byte("before"), 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}
	snap := TakeSnapshot(t, ws)
	if err := os.WriteFile(existing, []byte("after, in-place edit"), 0o644); err != nil {
		t.Fatalf("in-place write: %v", err)
	}
	if leaks := snap.Leaks(); len(leaks) != 0 {
		t.Fatalf("leaks = %v, want 0 (in-place edits are not leaks)", leaks)
	}
}

// TestVerify_ReportsLeaksViaTestFailure verifies the production integration
// path — Verify calls t.Errorf when leaks exist. Uses a stub TB to observe
// the error without failing the outer test.
func TestVerify_ReportsLeaksViaTestFailure(t *testing.T) {
	tb := &recordingTB{TB: t}
	ws := t.TempDir()
	snap := take(tb, []string{ws})
	if err := os.WriteFile(filepath.Join(ws, "leak.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	snap.Verify()
	if !tb.failed {
		t.Fatal("Verify should have called Errorf on the TB after a real leak")
	}
	if len(tb.errorMessages) != 1 || !strings.Contains(tb.errorMessages[0], "leak.txt") {
		t.Fatalf("error messages = %v, want one mentioning leak.txt", tb.errorMessages)
	}
}

// recordingTB is a testing.TB that captures Errorf calls instead of failing
// the outer test. Used to assert Verify's integration path.
type recordingTB struct {
	testing.TB
	failed        bool
	errorMessages []string
}

func (r *recordingTB) Errorf(format string, args ...interface{}) {
	r.failed = true
	r.errorMessages = append(r.errorMessages, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Helper() {}
