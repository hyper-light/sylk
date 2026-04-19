// Package zeroleak is the runtime correctness oracle for sylk's strict-disk
// invariant: no command dispatched by an agent (or by broker-routed support
// code) may produce a write to any real filesystem path. The FUSE-projected
// workspace is the only durable surface for side effects.
//
// A leak that slips past the nodirectexec analyzer (for instance, through a
// reflection-driven exec, a newly-added dependency that shells out, or a
// subtle broker misconfiguration) shows up as new inodes appearing in a
// directory that the test was not expected to write to. This package lets
// any test pin that invariant at the test boundary:
//
//	func TestInstallRoutesThroughBroker(t *testing.T) {
//	    workspace := t.TempDir()
//	    zeroleak.Guard(t, workspace)      // snapshot + auto-verify on cleanup
//	    // ... exercise the code under test ...
//	    // cleanup fires: if anything wrote to workspace, the test fails.
//	}
//
// The harness is intentionally minimal: a pre/post directory-walk diff. It is
// not trying to prove anything about kernel-level syscall capture — that is
// the broker's job. It is instead an independent observer that catches
// anything the broker missed, from a position outside the broker's own trust
// boundary. Belt to the analyzer's braces.
package zeroleak

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Snapshot captures the inodes present at one or more filesystem paths at
// the moment it is taken. Verify fails the underlying test if any new
// inodes appear in those paths after the snapshot.
//
// Snapshot does NOT record deletions — the strict-disk invariant is about
// writes leaking out of the VFS, not about reads or local cleanup. Callers
// that need full change detection should compose multiple snapshots.
type Snapshot struct {
	t      testing.TB
	paths  []string
	before map[string]map[string]struct{}
}

// Guard takes a snapshot of the supplied paths and registers a t.Cleanup
// hook that verifies no new inodes appeared during the test. Guard is the
// normal entrypoint.
//
// Passing no paths guards the calling test's t.TempDir by default, which is
// useful when a test creates a scratch workspace and wants to prove the code
// under test did not shell out to host disk while operating in it. Prefer
// passing explicit paths when the test's workspace is known ahead of time —
// it's clearer to the next reader what invariant is being asserted.
func Guard(t testing.TB, paths ...string) *Snapshot {
	t.Helper()
	if len(paths) == 0 {
		paths = []string{t.TempDir()}
	}
	s := take(t, paths)
	t.Cleanup(s.Verify)
	return s
}

// TakeSnapshot returns a manually-verified snapshot. Use this when the test
// needs to assert no-leak at a specific point mid-test rather than only at
// cleanup time.
func TakeSnapshot(t testing.TB, paths ...string) *Snapshot {
	t.Helper()
	if len(paths) == 0 {
		t.Fatal("zeroleak.TakeSnapshot: at least one path is required; pass t.TempDir() or an explicit workspace root")
	}
	return take(t, paths)
}

// Verify fails the test if new inodes appeared under any snapshotted path.
// Safe to call multiple times; each call compares against the original
// snapshot, not the previous Verify call's state.
func (s *Snapshot) Verify() {
	s.t.Helper()
	if leaks := s.Leaks(); len(leaks) > 0 {
		s.t.Errorf("strict-disk invariant violated: %d new inode(s) appeared during test:\n  %s",
			len(leaks), strings.Join(leaks, "\n  "))
	}
}

// Leaks returns the absolute paths of inodes that appeared in any
// snapshotted path after the snapshot was taken. Returns an empty slice if
// no leaks are detected. Exposed separately from Verify so tests of the
// harness itself can assert the leak set without tripping t.Errorf.
func (s *Snapshot) Leaks() []string {
	var leaks []string
	for _, root := range s.paths {
		after := scan(root)
		for rel := range after {
			if _, existed := s.before[root][rel]; !existed {
				leaks = append(leaks, filepath.Join(root, rel))
			}
		}
	}
	if len(leaks) == 0 {
		return nil
	}
	sort.Strings(leaks)
	return leaks
}

// Paths returns the absolute paths the snapshot is watching. Useful for
// debugging flaky tests where the snapshot boundary might be wrong.
func (s *Snapshot) Paths() []string {
	out := make([]string, len(s.paths))
	copy(out, s.paths)
	return out
}

func take(t testing.TB, paths []string) *Snapshot {
	t.Helper()
	s := &Snapshot{
		t:      t,
		paths:  append([]string(nil), paths...),
		before: make(map[string]map[string]struct{}, len(paths)),
	}
	for _, p := range paths {
		s.before[p] = scan(p)
	}
	return s
}

// scan walks root and returns the set of inode paths it contains, keyed by
// their path relative to root. A missing root returns an empty set — callers
// legitimately snapshot paths that may not yet exist (e.g. a subdirectory
// that the test will only create if something goes wrong).
func scan(root string) map[string]struct{} {
	out := make(map[string]struct{})
	// WalkDir visits the root itself first; skip that so the set only
	// contains children. Symlinks are not followed, which matches the
	// invariant we care about: if a command creates a new symlink in the
	// workspace, that's a leak even though the target might be elsewhere.
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == root {
				return filepath.SkipAll
			}
			// Unreadable subtree (permissions, vanished file): skip but
			// don't fail the walk. The worst that happens is we miss a
			// leak inside an unreadable dir — which is rare, and the test
			// itself would not be able to cause such writes unless it
			// escalated privilege.
			return nil
		}
		if p == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		out[rel] = struct{}{}
		return nil
	})
	return out
}
