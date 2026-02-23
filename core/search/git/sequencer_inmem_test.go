package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// testCommitFile creates or updates a file and commits it, returning the hash.
func testCommitFile(t *testing.T, dir string, repo *gogit.Repository, filename, content, msg string) plumbing.Hash {
	t.Helper()
	return testCommitFileAs(t, dir, repo, filename, content, msg, "Test Author", "test@example.com")
}

// testCommitFileAs creates or updates a file and commits it with a specified author.
func testCommitFileAs(t *testing.T, dir string, repo *gogit.Repository, filename, content, msg, authorName, authorEmail string) plumbing.Hash {
	t.Helper()

	fullPath := filepath.Join(dir, filename)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if _, err := wt.Add(filename); err != nil {
		t.Fatalf("add: %v", err)
	}

	hash, err := wt.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  authorName,
			Email: authorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return hash
}

// setupInMemRebaseRepo creates a repo with two diverging branches:
//
//	main:    A ← B (HEAD)
//	feature: A ← C ← D
//
// The feature branch modifies a different file than main, ensuring a clean
// rebase. Returns (dir, cleanup, [hashB, hashC, hashD]).
func setupInMemRebaseRepo(t *testing.T) (string, func(), [3]plumbing.Hash) {
	t.Helper()

	dir, cleanup := testRepo(t)

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		cleanup()
		t.Fatalf("open repo: %v", err)
	}

	// Commit A on main: initial file.
	testCommitFile(t, dir, repo, "shared.txt", "initial", "A: initial")

	// Create feature branch at A.
	headRef, err := repo.Head()
	if err != nil {
		cleanup()
		t.Fatalf("head: %v", err)
	}
	featureRef := plumbing.NewHashReference(
		plumbing.NewBranchReferenceName("feature"),
		headRef.Hash(),
	)
	if err := repo.Storer.SetReference(featureRef); err != nil {
		cleanup()
		t.Fatalf("create feature ref: %v", err)
	}

	// Commit B on main (different file).
	hashB := testCommitFile(t, dir, repo, "main-only.txt", "main content", "B: main-only change")

	// Switch to feature branch.
	wt, err := repo.Worktree()
	if err != nil {
		cleanup()
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
	}); err != nil {
		cleanup()
		t.Fatalf("checkout feature: %v", err)
	}

	// Commit C on feature.
	hashC := testCommitFile(t, dir, repo, "feature-only.txt", "feature v1", "C: feature file")

	// Commit D on feature.
	hashD := testCommitFile(t, dir, repo, "feature-only.txt", "feature v2", "D: update feature file")

	// Switch back to main.
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("master"),
	}); err != nil {
		cleanup()
		t.Fatalf("checkout master: %v", err)
	}

	return dir, cleanup, [3]plumbing.Hash{hashB, hashC, hashD}
}

func TestRebaseInMemory_CleanRebase(t *testing.T) {
	dir, cleanup, hashes := setupInMemRebaseRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient: %v", err)
	}
	defer client.Close()

	hashC := hashes[1]
	hashD := hashes[2]

	// Record HEAD before rebase.
	headBefore, err := client.GetHead()
	if err != nil {
		t.Fatalf("GetHead before: %v", err)
	}

	// Record worktree file content before rebase.
	mainFileContent, err := os.ReadFile(filepath.Join(dir, "main-only.txt"))
	if err != nil {
		t.Fatalf("read main-only.txt: %v", err)
	}

	// Rebase feature onto master (in-memory — feature is not checked out).
	status, err := client.RebaseInteractive("master", "feature", []RebasePlanEntry{
		{Action: RebasePick, Hash: hashC.String()},
		{Action: RebasePick, Hash: hashD.String()},
	})
	if err != nil {
		t.Fatalf("RebaseInteractive: %v", err)
	}
	if status != nil {
		t.Fatalf("expected nil status (complete), got state=%v", status.State)
	}

	// HEAD must be unchanged (still on master at the same commit).
	headAfter, err := client.GetHead()
	if err != nil {
		t.Fatalf("GetHead after: %v", err)
	}
	if headAfter != headBefore {
		t.Errorf("HEAD changed: %s → %s", headBefore, headAfter)
	}

	// Worktree files must be unchanged.
	mainFileAfter, err := os.ReadFile(filepath.Join(dir, "main-only.txt"))
	if err != nil {
		t.Fatalf("read main-only.txt after: %v", err)
	}
	if string(mainFileAfter) != string(mainFileContent) {
		t.Errorf("worktree file changed: %q → %q", mainFileContent, mainFileAfter)
	}

	// Feature branch should have no file "feature-only.txt" in the worktree
	// (we're on master), but the feature branch ref should now be rebased.
	repo := client.Repository()
	featureRefName := plumbing.NewBranchReferenceName("feature")
	featureRef, err := repo.Reference(featureRefName, true)
	if err != nil {
		t.Fatalf("resolve feature ref: %v", err)
	}

	// Walk the rebased feature branch and verify the parent chain.
	tipCommit, err := repo.CommitObject(featureRef.Hash())
	if err != nil {
		t.Fatalf("get tip commit: %v", err)
	}

	// Tip should be D' (rebased D).
	if tipCommit.Message != "D: update feature file" {
		t.Errorf("tip message = %q, want %q", tipCommit.Message, "D: update feature file")
	}

	// D' parent should be C' (rebased C).
	if len(tipCommit.ParentHashes) != 1 {
		t.Fatalf("tip has %d parents, want 1", len(tipCommit.ParentHashes))
	}
	cPrime, err := repo.CommitObject(tipCommit.ParentHashes[0])
	if err != nil {
		t.Fatalf("get C' commit: %v", err)
	}
	if cPrime.Message != "C: feature file" {
		t.Errorf("C' message = %q, want %q", cPrime.Message, "C: feature file")
	}

	// C' parent should be the master tip (hashB).
	if len(cPrime.ParentHashes) != 1 {
		t.Fatalf("C' has %d parents, want 1", len(cPrime.ParentHashes))
	}
	if cPrime.ParentHashes[0] != hashes[0] {
		t.Errorf("C' parent = %s, want master tip %s", cPrime.ParentHashes[0], hashes[0])
	}

	// The rebased commits should be new objects (different from originals).
	if featureRef.Hash() == hashD {
		t.Error("feature ref still points at original D — rebase did not create new commits")
	}
	if cPrime.Hash == hashC {
		t.Error("C' hash equals original C — rebase did not create new commits")
	}

	// Sequencer should not be active.
	if s := client.GetSequencerStatus(); s != nil {
		t.Errorf("sequencer still active: %+v", s)
	}
}

func TestRebaseInMemory_ConflictFallback(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}

	// Commit A: shared file.
	testCommitFile(t, dir, repo, "conflict.txt", "base content", "A: base")

	// Create feature branch at A.
	headRef, err := repo.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	featureRef := plumbing.NewHashReference(
		plumbing.NewBranchReferenceName("feature"),
		headRef.Hash(),
	)
	if err := repo.Storer.SetReference(featureRef); err != nil {
		t.Fatalf("create feature ref: %v", err)
	}

	// Commit B on master: modify conflict.txt.
	testCommitFile(t, dir, repo, "conflict.txt", "master version", "B: master change")

	// Switch to feature, make a conflicting change.
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
	}); err != nil {
		t.Fatalf("checkout feature: %v", err)
	}

	hashC := testCommitFile(t, dir, repo, "conflict.txt", "feature version", "C: feature change")

	// Switch back to master.
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("master"),
	}); err != nil {
		t.Fatalf("checkout master: %v", err)
	}

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient: %v", err)
	}
	defer client.Close()

	// Rebase feature onto master — should conflict.
	// The in-memory path detects the conflict and falls through to the
	// worktree-based sequencer, which pauses with SeqConflict.
	status, err := client.RebaseInteractive("master", "feature", []RebasePlanEntry{
		{Action: RebasePick, Hash: hashC.String()},
	})
	if err != nil {
		t.Fatalf("RebaseInteractive: %v", err)
	}
	if status == nil {
		t.Fatal("expected conflict status, got nil (complete)")
	}
	if status.State != SeqConflict {
		t.Errorf("state = %v, want SeqConflict", status.State)
	}

	// Sequencer should be active (worktree path took over).
	seqStatus := client.GetSequencerStatus()
	if seqStatus == nil {
		t.Fatal("sequencer should be active after conflict fallback")
	}

	// Abort to clean up.
	if err := client.SequencerAbort(); err != nil {
		t.Fatalf("abort: %v", err)
	}
}

func TestRebaseInMemory_WithReword(t *testing.T) {
	dir, cleanup, hashes := setupInMemRebaseRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient: %v", err)
	}
	defer client.Close()

	hashC := hashes[1]
	hashD := hashes[2]

	status, err := client.RebaseInteractive("master", "feature", []RebasePlanEntry{
		{Action: RebaseReword, Hash: hashC.String()},
		{Action: RebasePick, Hash: hashD.String()},
	})
	if err != nil {
		t.Fatalf("RebaseInteractive: %v", err)
	}
	if status != nil {
		t.Fatalf("expected nil status, got state=%v", status.State)
	}

	// The reword step has no message override set (sequencerStep.message is ""),
	// so the original message should be preserved.
	repo := client.Repository()
	featureRef, err := repo.Reference(plumbing.NewBranchReferenceName("feature"), true)
	if err != nil {
		t.Fatalf("resolve feature: %v", err)
	}
	tip, err := repo.CommitObject(featureRef.Hash())
	if err != nil {
		t.Fatalf("tip commit: %v", err)
	}
	parent, err := repo.CommitObject(tip.ParentHashes[0])
	if err != nil {
		t.Fatalf("parent commit: %v", err)
	}

	if parent.Message != "C: feature file" {
		t.Errorf("reworded commit message = %q, want %q", parent.Message, "C: feature file")
	}
}

func TestRebaseInMemory_WithDrop(t *testing.T) {
	// Create a repo where C and D touch independent files so dropping C is clean.
	dir, cleanup := testRepo(t)
	defer cleanup()

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Commit A on master.
	testCommitFile(t, dir, repo, "shared.txt", "initial", "A: initial")

	// Create feature at A.
	headRef, err := repo.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	fRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName("feature"), headRef.Hash())
	if err := repo.Storer.SetReference(fRef); err != nil {
		t.Fatalf("set ref: %v", err)
	}

	// Commit B on master.
	hashB := testCommitFile(t, dir, repo, "main-only.txt", "main", "B: main")

	// Switch to feature.
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
	}); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	// Commit C: adds file-c.txt (will be dropped).
	hashC := testCommitFile(t, dir, repo, "file-c.txt", "c content", "C: droppable")

	// Commit D: adds file-d.txt (independent of C).
	hashD := testCommitFile(t, dir, repo, "file-d.txt", "d content", "D: keep this")

	// Switch back to master.
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("master"),
	}); err != nil {
		t.Fatalf("checkout master: %v", err)
	}

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient: %v", err)
	}
	defer client.Close()

	_ = hashC
	status, err := client.RebaseInteractive("master", "feature", []RebasePlanEntry{
		{Action: RebaseDrop, Hash: hashC.String()},
		{Action: RebasePick, Hash: hashD.String()},
	})
	if err != nil {
		t.Fatalf("RebaseInteractive: %v", err)
	}
	if status != nil {
		t.Fatalf("expected nil status, got state=%v", status.State)
	}

	// Verify only D' exists above master (C was dropped).
	featureRef, err := repo.Reference(plumbing.NewBranchReferenceName("feature"), true)
	if err != nil {
		t.Fatalf("resolve feature: %v", err)
	}
	tip, err := repo.CommitObject(featureRef.Hash())
	if err != nil {
		t.Fatalf("tip commit: %v", err)
	}
	if tip.Message != "D: keep this" {
		t.Errorf("tip message = %q, want %q", tip.Message, "D: keep this")
	}
	if tip.ParentHashes[0] != hashB {
		t.Errorf("D' parent = %s, want master tip %s", tip.ParentHashes[0], hashB)
	}
}

func TestRebaseInMemory_HeadBranchUsesWorktree(t *testing.T) {
	dir, cleanup, hashes := setupInMemRebaseRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient: %v", err)
	}
	defer client.Close()

	hashC := hashes[1]
	hashD := hashes[2]

	// Switch to feature so it IS the HEAD branch.
	repo := client.Repository()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
	}); err != nil {
		t.Fatalf("checkout feature: %v", err)
	}

	// Rebase feature onto master with sourceBranch == HEAD branch.
	// canRebaseInMemory should return false, so the worktree path is used.
	status, err := client.RebaseInteractive("master", "feature", []RebasePlanEntry{
		{Action: RebasePick, Hash: hashC.String()},
		{Action: RebasePick, Hash: hashD.String()},
	})
	if err != nil {
		t.Fatalf("RebaseInteractive: %v", err)
	}
	// Should still complete (clean rebase via worktree path).
	if status != nil {
		t.Fatalf("expected nil status, got state=%v", status.State)
	}
}

func TestRebaseInMemory_DropBeforeSquash_Author(t *testing.T) {
	// Verifies that the squash group author comes from the preceding pick,
	// not from a dropped commit between the pick and the squash.
	//
	// Plan: [Pick A (Alice), Drop B (Bob), Squash C (Charlie)]
	// Expected squash author: Alice (not Bob).
	dir, cleanup := testRepo(t)
	defer cleanup()

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Commit base on master.
	testCommitFile(t, dir, repo, "shared.txt", "initial", "base")

	// Create feature at base.
	headRef, err := repo.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	fRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName("feature"), headRef.Hash())
	if err := repo.Storer.SetReference(fRef); err != nil {
		t.Fatalf("set ref: %v", err)
	}

	// Commit M on master (diverge).
	testCommitFile(t, dir, repo, "main-only.txt", "main", "M: master")

	// Switch to feature.
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("wt: %v", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
	}); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	// A by Alice (will be picked).
	hashA := testCommitFileAs(t, dir, repo, "file-a.txt", "a", "A: pick me", "Alice", "alice@test.com")
	// B by Bob (will be dropped — independent file).
	hashB := testCommitFileAs(t, dir, repo, "file-b.txt", "b", "B: drop me", "Bob", "bob@test.com")
	// C by Charlie (will be squashed — independent file).
	hashC := testCommitFileAs(t, dir, repo, "file-c.txt", "c", "C: squash me", "Charlie", "charlie@test.com")

	// Back to master.
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("master"),
	}); err != nil {
		t.Fatalf("checkout master: %v", err)
	}

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient: %v", err)
	}
	defer client.Close()

	status, err := client.RebaseInteractive("master", "feature", []RebasePlanEntry{
		{Action: RebasePick, Hash: hashA.String()},
		{Action: RebaseDrop, Hash: hashB.String()},
		{Action: RebaseSquash, Hash: hashC.String()},
	})
	if err != nil {
		t.Fatalf("RebaseInteractive: %v", err)
	}
	if status != nil {
		t.Fatalf("expected nil status, got state=%v", status.State)
	}

	// The squash commit's author should be Alice (from pick A), not Bob (dropped).
	featureRef, err := repo.Reference(plumbing.NewBranchReferenceName("feature"), true)
	if err != nil {
		t.Fatalf("resolve feature: %v", err)
	}
	tip, err := repo.CommitObject(featureRef.Hash())
	if err != nil {
		t.Fatalf("tip commit: %v", err)
	}

	if tip.Author.Name != "Alice" {
		t.Errorf("squash author = %q, want %q", tip.Author.Name, "Alice")
	}
}

func TestRebaseInMemory_RewordBeforeFixup_Message(t *testing.T) {
	// Verifies that a pure fixup group following a reword step uses the
	// reworded message, not the original commit message.
	//
	// Plan: [Reword A (message="Reworded!"), Fixup B]
	// Expected fixup commit message: "Reworded!" (not A's original).
	dir, cleanup := testRepo(t)
	defer cleanup()

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	testCommitFile(t, dir, repo, "shared.txt", "initial", "base")

	headRef, err := repo.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	fRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName("feature"), headRef.Hash())
	if err := repo.Storer.SetReference(fRef); err != nil {
		t.Fatalf("set ref: %v", err)
	}

	testCommitFile(t, dir, repo, "main-only.txt", "main", "M: master diverge")

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("wt: %v", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
	}); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	hashA := testCommitFile(t, dir, repo, "file-a.txt", "a", "A: original message")
	hashB := testCommitFile(t, dir, repo, "file-b.txt", "b", "B: fixup me")

	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("master"),
	}); err != nil {
		t.Fatalf("checkout master: %v", err)
	}

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient: %v", err)
	}
	defer client.Close()

	// Build plan with reword message override. RebaseInteractive takes
	// RebasePlanEntry which doesn't have a message field — the message is
	// set on the sequencerStep. We need to test via the internal path.
	// Use RebaseInteractive with RebaseReword and verify behavior.
	//
	// NOTE: RebasePlanEntry doesn't carry a message override field, so the
	// RebaseReword action uses commit.Message (no override). This test
	// verifies the internal findPickMessage correctly handles the reword
	// action by falling through to commit.Message when step.message is "".
	status, err := client.RebaseInteractive("master", "feature", []RebasePlanEntry{
		{Action: RebaseReword, Hash: hashA.String()},
		{Action: RebaseFixup, Hash: hashB.String()},
	})
	if err != nil {
		t.Fatalf("RebaseInteractive: %v", err)
	}
	if status != nil {
		t.Fatalf("expected nil status, got state=%v", status.State)
	}

	// The fixup commit's message should be A's original (since no message
	// override exists on the plan entry).
	featureRef, err := repo.Reference(plumbing.NewBranchReferenceName("feature"), true)
	if err != nil {
		t.Fatalf("resolve feature: %v", err)
	}
	tip, err := repo.CommitObject(featureRef.Hash())
	if err != nil {
		t.Fatalf("tip commit: %v", err)
	}
	if tip.Message != "A: original message" {
		t.Errorf("fixup commit message = %q, want %q", tip.Message, "A: original message")
	}
}

func TestRebaseInMemory_PickSquashFixup(t *testing.T) {
	// Full squash scenario: Pick + Squash + Fixup → one squash commit
	// with combined messages (pick creates a commit, squash+fixup create another).
	dir, cleanup := testRepo(t)
	defer cleanup()

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	testCommitFile(t, dir, repo, "shared.txt", "initial", "base")

	headRef, err := repo.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	fRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName("feature"), headRef.Hash())
	if err := repo.Storer.SetReference(fRef); err != nil {
		t.Fatalf("set ref: %v", err)
	}

	masterTip := testCommitFile(t, dir, repo, "main-only.txt", "main", "M: master diverge")

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("wt: %v", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
	}); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	hashA := testCommitFileAs(t, dir, repo, "file-a.txt", "a", "A: pick", "Alice", "alice@test.com")
	hashB := testCommitFile(t, dir, repo, "file-b.txt", "b", "B: squash msg")
	hashC := testCommitFile(t, dir, repo, "file-c.txt", "c", "C: fixup (hidden)")

	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("master"),
	}); err != nil {
		t.Fatalf("checkout master: %v", err)
	}

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient: %v", err)
	}
	defer client.Close()

	status, err := client.RebaseInteractive("master", "feature", []RebasePlanEntry{
		{Action: RebasePick, Hash: hashA.String()},
		{Action: RebaseSquash, Hash: hashB.String()},
		{Action: RebaseFixup, Hash: hashC.String()},
	})
	if err != nil {
		t.Fatalf("RebaseInteractive: %v", err)
	}
	if status != nil {
		t.Fatalf("expected nil status, got state=%v", status.State)
	}

	featureRef, err := repo.Reference(plumbing.NewBranchReferenceName("feature"), true)
	if err != nil {
		t.Fatalf("resolve feature: %v", err)
	}
	tip, err := repo.CommitObject(featureRef.Hash())
	if err != nil {
		t.Fatalf("tip commit: %v", err)
	}

	// Squash commit message should contain B's message (squash appends)
	// but NOT C's (fixup is silent).
	if tip.Message != "B: squash msg" {
		t.Errorf("squash tip message = %q, want %q", tip.Message, "B: squash msg")
	}

	// Squash commit's author should be Alice (from the preceding pick).
	if tip.Author.Name != "Alice" {
		t.Errorf("squash author = %q, want %q", tip.Author.Name, "Alice")
	}

	// The squash commit's parent should be the pick commit A'.
	pickCommit, err := repo.CommitObject(tip.ParentHashes[0])
	if err != nil {
		t.Fatalf("pick commit: %v", err)
	}
	if pickCommit.Message != "A: pick" {
		t.Errorf("pick message = %q, want %q", pickCommit.Message, "A: pick")
	}
	if pickCommit.ParentHashes[0] != masterTip {
		t.Errorf("pick parent = %s, want master tip %s", pickCommit.ParentHashes[0], masterTip)
	}
}

func TestRebaseInMemory_AllDrops(t *testing.T) {
	dir, cleanup, hashes := setupInMemRebaseRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient: %v", err)
	}
	defer client.Close()

	// Drop all commits → feature branch should point at onto tip.
	status, err := client.RebaseInteractive("master", "feature", []RebasePlanEntry{
		{Action: RebaseDrop, Hash: hashes[1].String()},
		{Action: RebaseDrop, Hash: hashes[2].String()},
	})
	if err != nil {
		t.Fatalf("RebaseInteractive: %v", err)
	}
	if status != nil {
		t.Fatalf("expected nil status, got state=%v", status.State)
	}

	repo := client.Repository()
	featureRef, err := repo.Reference(plumbing.NewBranchReferenceName("feature"), true)
	if err != nil {
		t.Fatalf("resolve feature: %v", err)
	}
	if featureRef.Hash() != hashes[0] {
		t.Errorf("feature ref = %s, want master tip %s (all commits dropped)", featureRef.Hash(), hashes[0])
	}
}

func TestRebaseInMemory_EmptyPlan(t *testing.T) {
	dir, cleanup, hashes := setupInMemRebaseRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient: %v", err)
	}
	defer client.Close()

	// Empty plan → feature branch should point at onto tip.
	status, err := client.RebaseInteractive("master", "feature", []RebasePlanEntry{})
	if err != nil {
		t.Fatalf("RebaseInteractive: %v", err)
	}
	if status != nil {
		t.Fatalf("expected nil status, got state=%v", status.State)
	}

	repo := client.Repository()
	featureRef, err := repo.Reference(plumbing.NewBranchReferenceName("feature"), true)
	if err != nil {
		t.Fatalf("resolve feature: %v", err)
	}
	if featureRef.Hash() != hashes[0] {
		t.Errorf("feature ref = %s, want master tip %s (empty plan)", featureRef.Hash(), hashes[0])
	}
}

func TestPrecedingAppliedAuthor(t *testing.T) {
	alice := object.Signature{Name: "Alice"}
	bob := object.Signature{Name: "Bob"}
	charlie := object.Signature{Name: "Charlie"}
	fallback := object.Signature{Name: "Fallback"}

	tests := []struct {
		name   string
		steps  []sequencerStep
		idx    int
		expect string
	}{
		{
			name:   "no preceding steps",
			steps:  []sequencerStep{{action: RebaseSquash, commit: &object.Commit{Author: charlie}}},
			idx:    0,
			expect: "Fallback",
		},
		{
			name: "preceding pick",
			steps: []sequencerStep{
				{action: RebasePick, commit: &object.Commit{Author: alice}},
				{action: RebaseSquash, commit: &object.Commit{Author: charlie}},
			},
			idx:    1,
			expect: "Alice",
		},
		{
			name: "skip one drop",
			steps: []sequencerStep{
				{action: RebasePick, commit: &object.Commit{Author: alice}},
				{action: RebaseDrop, commit: &object.Commit{Author: bob}},
				{action: RebaseSquash, commit: &object.Commit{Author: charlie}},
			},
			idx:    2,
			expect: "Alice",
		},
		{
			name: "skip multiple drops",
			steps: []sequencerStep{
				{action: RebasePick, commit: &object.Commit{Author: alice}},
				{action: RebaseDrop, commit: &object.Commit{Author: bob}},
				{action: RebaseDrop, commit: &object.Commit{Author: bob}},
				{action: RebaseSquash, commit: &object.Commit{Author: charlie}},
			},
			idx:    3,
			expect: "Alice",
		},
		{
			name: "all preceding are drops",
			steps: []sequencerStep{
				{action: RebaseDrop, commit: &object.Commit{Author: bob}},
				{action: RebaseDrop, commit: &object.Commit{Author: bob}},
				{action: RebaseSquash, commit: &object.Commit{Author: charlie}},
			},
			idx:    2,
			expect: "Fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := precedingAppliedAuthor(tt.steps, tt.idx, fallback)
			if got.Name != tt.expect {
				t.Errorf("precedingAppliedAuthor() = %q, want %q", got.Name, tt.expect)
			}
		})
	}
}

func TestFindPickMessage(t *testing.T) {
	tests := []struct {
		name      string
		steps     []sequencerStep
		beforeIdx int
		expect    string
	}{
		{
			name:      "no preceding steps",
			steps:     []sequencerStep{{action: RebaseFixup}},
			beforeIdx: 0,
			expect:    "squashed commits",
		},
		{
			name: "finds pick message",
			steps: []sequencerStep{
				{action: RebasePick, commit: &object.Commit{Message: "pick msg"}},
				{action: RebaseFixup},
			},
			beforeIdx: 1,
			expect:    "pick msg",
		},
		{
			name: "finds reword with override",
			steps: []sequencerStep{
				{action: RebaseReword, message: "reworded!", commit: &object.Commit{Message: "original"}},
				{action: RebaseFixup},
			},
			beforeIdx: 1,
			expect:    "reworded!",
		},
		{
			name: "reword without override falls back to commit message",
			steps: []sequencerStep{
				{action: RebaseReword, message: "", commit: &object.Commit{Message: "original"}},
				{action: RebaseFixup},
			},
			beforeIdx: 1,
			expect:    "original",
		},
		{
			name: "skips squash and fixup steps",
			steps: []sequencerStep{
				{action: RebasePick, commit: &object.Commit{Message: "the pick"}},
				{action: RebaseSquash, commit: &object.Commit{Message: "squash"}},
				{action: RebaseFixup, commit: &object.Commit{Message: "fixup"}},
			},
			beforeIdx: 2, // searching from fixup
			expect:    "the pick",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findPickMessage(tt.steps, tt.beforeIdx)
			if got != tt.expect {
				t.Errorf("findPickMessage() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestCanRebaseInMemory(t *testing.T) {
	tests := []struct {
		name   string
		seq    *sequencer
		expect bool
	}{
		{
			name:   "empty source ref",
			seq:    &sequencer{rebaseSourceRef: ""},
			expect: false,
		},
		{
			name: "has edit action",
			seq: &sequencer{
				rebaseSourceRef: "refs/heads/feature",
				steps:           []sequencerStep{{action: RebasePick}, {action: RebaseEdit}},
			},
			expect: false,
		},
		{
			name: "all pick",
			seq: &sequencer{
				rebaseSourceRef: "refs/heads/feature",
				steps:           []sequencerStep{{action: RebasePick}, {action: RebasePick}},
			},
			expect: true,
		},
		{
			name: "pick and squash",
			seq: &sequencer{
				rebaseSourceRef: "refs/heads/feature",
				steps:           []sequencerStep{{action: RebasePick}, {action: RebaseSquash}},
			},
			expect: true,
		},
		{
			name: "pick and drop",
			seq: &sequencer{
				rebaseSourceRef: "refs/heads/feature",
				steps:           []sequencerStep{{action: RebasePick}, {action: RebaseDrop}},
			},
			expect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := canRebaseInMemory(tt.seq)
			if got != tt.expect {
				t.Errorf("canRebaseInMemory() = %v, want %v", got, tt.expect)
			}
		})
	}
}
