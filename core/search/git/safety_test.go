package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// =============================================================================
// Journal Codec Tests
// =============================================================================

func TestEncodeDecodeJournalEntry(t *testing.T) {
	original := &JournalEntry{
		Op:        OpCheckoutBranch,
		Phase:     JournalBegin,
		Timestamp: time.Now().Truncate(time.Nanosecond),
		HeadHash:  plumbing.NewHash("abc1234567890abcdef1234567890abcdef12345"),
		BranchRef: "refs/heads/main",
		Params:    "feature-branch",
		SnapRef:   "refs/sylk/snap/checkout_branch-1740067200",
		Err:       "",
	}

	data, err := EncodeJournalEntry(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DecodeJournalEntry(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.Op != original.Op {
		t.Errorf("Op: got %v, want %v", decoded.Op, original.Op)
	}
	if decoded.Phase != original.Phase {
		t.Errorf("Phase: got %v, want %v", decoded.Phase, original.Phase)
	}
	if !decoded.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp: got %v, want %v", decoded.Timestamp, original.Timestamp)
	}
	if decoded.HeadHash != original.HeadHash {
		t.Errorf("HeadHash: got %v, want %v", decoded.HeadHash, original.HeadHash)
	}
	if decoded.BranchRef != original.BranchRef {
		t.Errorf("BranchRef: got %q, want %q", decoded.BranchRef, original.BranchRef)
	}
	if decoded.Params != original.Params {
		t.Errorf("Params: got %q, want %q", decoded.Params, original.Params)
	}
	if decoded.SnapRef != original.SnapRef {
		t.Errorf("SnapRef: got %q, want %q", decoded.SnapRef, original.SnapRef)
	}
	if decoded.Err != original.Err {
		t.Errorf("Err: got %q, want %q", decoded.Err, original.Err)
	}
}

func TestDecodeJournalEntryTruncated(t *testing.T) {
	_, err := DecodeJournalEntry(make([]byte, 10))
	if err != ErrJournalCodecTruncated {
		t.Errorf("expected ErrJournalCodecTruncated, got %v", err)
	}
}

func TestDecodeJournalEntryInvalidPhase(t *testing.T) {
	data := make([]byte, journalEntryFixedSize)
	data[2] = 99 // Invalid phase.
	_, err := DecodeJournalEntry(data)
	if err != ErrJournalInvalidPhase {
		t.Errorf("expected ErrJournalInvalidPhase, got %v", err)
	}
}

func TestEncodeDecodeWithErrorField(t *testing.T) {
	original := &JournalEntry{
		Op:        OpReset,
		Phase:     JournalAbort,
		Timestamp: time.Now().Truncate(time.Nanosecond),
		Err:       "commit not found: abc123",
	}

	data, err := EncodeJournalEntry(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DecodeJournalEntry(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.Err != original.Err {
		t.Errorf("Err: got %q, want %q", decoded.Err, original.Err)
	}
}

// =============================================================================
// Journal Tests
// =============================================================================

func TestJournalBeginComplete(t *testing.T) {
	dir := t.TempDir()

	journal, err := OpenJournal(dir)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer journal.Close()

	entry := &JournalEntry{
		Op:        OpRebase,
		Timestamp: time.Now(),
		BranchRef: "refs/heads/feature",
		SnapRef:   "refs/sylk/snap/rebase-123",
	}

	if err := journal.LogBegin(entry); err != nil {
		t.Fatalf("log begin: %v", err)
	}

	if entry.Seq == 0 {
		t.Error("expected non-zero sequence after LogBegin")
	}

	if err := journal.LogComplete(entry); err != nil {
		t.Fatalf("log complete: %v", err)
	}

	// No incomplete ops should exist.
	incomplete, err := journal.FindIncompleteOps()
	if err != nil {
		t.Fatalf("find incomplete: %v", err)
	}
	if len(incomplete) != 0 {
		t.Errorf("expected 0 incomplete ops, got %d", len(incomplete))
	}
}

func TestJournalFindIncomplete(t *testing.T) {
	dir := t.TempDir()

	journal, err := OpenJournal(dir)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer journal.Close()

	entry := &JournalEntry{
		Op:        OpRebaseInteractive,
		Timestamp: time.Now(),
		BranchRef: "refs/heads/feature",
		SnapRef:   "refs/sylk/snap/rebase_interactive-456",
	}

	// Log only Begin (simulate crash).
	if err := journal.LogBegin(entry); err != nil {
		t.Fatalf("log begin: %v", err)
	}

	incomplete, err := journal.FindIncompleteOps()
	if err != nil {
		t.Fatalf("find incomplete: %v", err)
	}
	if len(incomplete) != 1 {
		t.Fatalf("expected 1 incomplete op, got %d", len(incomplete))
	}
	if incomplete[0].Op != OpRebaseInteractive {
		t.Errorf("op: got %v, want %v", incomplete[0].Op, OpRebaseInteractive)
	}
}

func TestJournalBeginAbort(t *testing.T) {
	dir := t.TempDir()

	journal, err := OpenJournal(dir)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer journal.Close()

	entry := &JournalEntry{
		Op:        OpMergeBranch,
		Timestamp: time.Now(),
		Err:       "merge conflict",
	}

	if err := journal.LogBegin(entry); err != nil {
		t.Fatalf("log begin: %v", err)
	}

	entry.Err = "merge conflict"
	if err := journal.LogAbort(entry); err != nil {
		t.Fatalf("log abort: %v", err)
	}

	incomplete, err := journal.FindIncompleteOps()
	if err != nil {
		t.Fatalf("find incomplete: %v", err)
	}
	if len(incomplete) != 0 {
		t.Errorf("expected 0 incomplete ops after abort, got %d", len(incomplete))
	}
}

func TestJournalPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()

	// Write a Begin entry.
	j1, err := OpenJournal(dir)
	if err != nil {
		t.Fatalf("open journal 1: %v", err)
	}

	entry := &JournalEntry{
		Op:        OpCherryPickSequence,
		Timestamp: time.Now(),
		SnapRef:   "refs/sylk/snap/cherry_pick_sequence-789",
	}
	if err := j1.LogBegin(entry); err != nil {
		t.Fatalf("log begin: %v", err)
	}
	j1.Close()

	// Reopen and verify the incomplete op is detected.
	j2, err := OpenJournal(dir)
	if err != nil {
		t.Fatalf("open journal 2: %v", err)
	}
	defer j2.Close()

	incomplete, err := j2.FindIncompleteOps()
	if err != nil {
		t.Fatalf("find incomplete: %v", err)
	}
	if len(incomplete) != 1 {
		t.Fatalf("expected 1 incomplete op after reopen, got %d", len(incomplete))
	}
}

func TestJournalGC(t *testing.T) {
	dir := t.TempDir()

	journal, err := OpenJournal(dir)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer journal.Close()

	entry := &JournalEntry{
		Op:        OpCommitFiles,
		Timestamp: time.Now(),
	}
	if err := journal.LogBegin(entry); err != nil {
		t.Fatalf("log begin: %v", err)
	}
	if err := journal.LogComplete(entry); err != nil {
		t.Fatalf("log complete: %v", err)
	}

	// GC with future cutoff should not error.
	if err := journal.GC(time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("gc: %v", err)
	}
}

// =============================================================================
// Snapshot Tests
// =============================================================================

func TestCreateAndListSnapshotRefs(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	client.mu.Lock()
	snap, err := client.createSnapshot(OpRebase)
	client.mu.Unlock()
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if !strings.HasPrefix(snap.Name, snapRefPrefix) {
		t.Errorf("expected snap ref prefix, got %q", snap.Name)
	}

	client.mu.RLock()
	refs, err := client.listSnapshotRefs()
	client.mu.RUnlock()
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 snapshot ref, got %d", len(refs))
	}
	if refs[0].Hash != snap.Hash {
		t.Errorf("hash mismatch: got %v, want %v", refs[0].Hash, snap.Hash)
	}
}

func TestCreateBranchBackup(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	// Create a branch.
	headHash, _ := client.GetHead()
	hash, _ := client.resolveCommitHash(headHash)
	if err := client.CreateBranch("feature", headHash); err != nil {
		t.Fatalf("create branch: %v", err)
	}

	// Back it up.
	client.mu.Lock()
	_, err = client.createBranchBackup("feature", hash)
	client.mu.Unlock()
	if err != nil {
		t.Fatalf("create branch backup: %v", err)
	}

	client.mu.RLock()
	backups, err := client.listDeletedBackups()
	client.mu.RUnlock()
	if err != nil {
		t.Fatalf("list deleted backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup ref, got %d", len(backups))
	}
}

func TestCreateDetachedBackup(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	headHash, _ := client.GetHead()
	hash, _ := client.resolveCommitHash(headHash)

	client.mu.Lock()
	_, err = client.createDetachedBackup(hash)
	client.mu.Unlock()
	if err != nil {
		t.Fatalf("create detached backup: %v", err)
	}

	client.mu.RLock()
	backups, err := client.listDetachedBackups()
	client.mu.RUnlock()
	if err != nil {
		t.Fatalf("list detached backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 detached backup, got %d", len(backups))
	}
}

func TestGCSnapshotRefs(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	// Create two snapshots.
	client.mu.Lock()
	_, _ = client.createSnapshot(OpRebase)
	time.Sleep(time.Millisecond)
	_, _ = client.createSnapshot(OpReset)
	client.mu.Unlock()

	// GC with max=1 should remove one.
	cfg := SafetyConfig{
		SnapshotRetention: time.Hour,
		MaxSnapshots:      1,
	}

	client.mu.Lock()
	removed, err := client.gcSnapshotRefs(cfg)
	client.mu.Unlock()
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	client.mu.RLock()
	refs, err := client.listSnapshotRefs()
	client.mu.RUnlock()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(refs) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(refs))
	}
}

func TestRestoreSnapshot(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	// Snapshot current state.
	client.mu.Lock()
	snap, err := client.createSnapshot(OpReset)
	client.mu.Unlock()
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	originalHash := snap.Hash.String()

	// Make a new commit to change HEAD.
	repo := client.Repository()
	wt, _ := repo.Worktree()
	testFile := filepath.Join(dir, "new.txt")
	os.WriteFile(testFile, []byte("new content"), 0o644)
	wt.Add("new.txt")
	wt.Commit("second commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})

	// Verify HEAD changed.
	newHash, _ := client.GetHead()
	if newHash == originalHash {
		t.Fatal("expected HEAD to change after commit")
	}

	// Restore snapshot.
	client.mu.Lock()
	err = client.restoreSnapshot(snap)
	client.mu.Unlock()
	if err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}

	restoredHash, _ := client.GetHead()
	if restoredHash != originalHash {
		t.Errorf("restore: got %s, want %s", restoredHash, originalHash)
	}
}

// =============================================================================
// SafetyGuard Integration Tests
// =============================================================================

func TestSafetyGuardLifecycle(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	bus := NewGitBus(client)
	sg, err := NewSafetyGuard(client, bus, DefaultSafetyConfig(), nil)
	if err != nil {
		t.Fatalf("new safety guard: %v", err)
	}

	// RecoverOnStartup should find nothing on a fresh journal.
	ops, err := sg.RecoverOnStartup()
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("expected 0 incomplete ops, got %d", len(ops))
	}

	if err := sg.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestSafetyGuardSnapshotsOnMutation(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	bus := NewGitBus(client)
	sg, err := NewSafetyGuard(client, bus, DefaultSafetyConfig(), nil)
	if err != nil {
		t.Fatalf("new safety guard: %v", err)
	}
	defer sg.Close()

	// Create a branch via the bus to trigger snapshot creation.
	headHash, _ := client.GetHead()
	if err := bus.CreateBranch("test-branch", headHash); err != nil {
		t.Fatalf("create branch: %v", err)
	}

	// Verify a snapshot was created.
	client.mu.RLock()
	refs, err := client.listSnapshotRefs()
	client.mu.RUnlock()
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(refs) == 0 {
		t.Error("expected at least one snapshot ref after mutation")
	}
}

func TestDeleteBranchCreatesBackup(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	headHash, _ := client.GetHead()
	if err := client.CreateBranch("doomed", headHash); err != nil {
		t.Fatalf("create branch: %v", err)
	}

	// Delete the branch.
	if err := client.DeleteBranch("doomed"); err != nil {
		t.Fatalf("delete branch: %v", err)
	}

	// Verify backup ref exists.
	client.mu.RLock()
	backups, err := client.listDeletedBackups()
	client.mu.RUnlock()
	if err != nil {
		t.Fatalf("list deleted backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup ref, got %d", len(backups))
	}

	// Verify the backup hash matches the original branch tip.
	hash, _ := client.resolveCommitHash(headHash)
	if backups[0].Hash != hash {
		t.Errorf("backup hash mismatch: got %v, want %v", backups[0].Hash, hash)
	}
}

// =============================================================================
// Force-Push Safety Tests
// =============================================================================

func TestCheckPushSafetyNoRemote(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	// No remote tracking ref → should return nil (safe).
	client.mu.RLock()
	err = client.checkPushSafety("main", "origin")
	client.mu.RUnlock()
	if err != nil {
		t.Errorf("expected nil error for missing remote, got %v", err)
	}
}

// =============================================================================
// Worktree Safety Tests
// =============================================================================

func TestIsDirtyWorktreeClean(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	client.mu.RLock()
	dirty := client.isDirtyWorktree()
	client.mu.RUnlock()

	if dirty {
		t.Error("expected clean worktree")
	}
}

func TestIsDirtyWorktreeModified(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	// Modify a tracked file.
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("modified"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	client.mu.RLock()
	dirty := client.isDirtyWorktree()
	client.mu.RUnlock()

	if !dirty {
		t.Error("expected dirty worktree after modification")
	}
}

func TestIsDirtyWorktreeUntrackedOnly(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	// Add an untracked file (should NOT count as dirty).
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	client.mu.RLock()
	dirty := client.isDirtyWorktree()
	client.mu.RUnlock()

	if dirty {
		t.Error("expected clean worktree with only untracked files")
	}
}

// =============================================================================
// Cached Dirty Detection Tests
// =============================================================================

func TestIsDirtyFromCachedStatusNil(t *testing.T) {
	dirty, ok := isDirtyFromCachedStatus(nil)
	if ok {
		t.Error("expected ok=false for nil status")
	}
	if dirty {
		t.Error("expected dirty=false for nil status")
	}
}

func TestIsDirtyFromCachedStatusClean(t *testing.T) {
	su := &StatusUpdate{
		StatusMap: map[string]GitFileState{
			"file.go": GitClean,
		},
	}
	dirty, ok := isDirtyFromCachedStatus(su)
	if !ok {
		t.Error("expected ok=true")
	}
	if dirty {
		t.Error("expected not dirty for clean files")
	}
}

func TestIsDirtyFromCachedStatusModified(t *testing.T) {
	su := &StatusUpdate{
		StatusMap: map[string]GitFileState{
			"file.go":      GitClean,
			"modified.go":  GitModified,
			"untracked.go": GitUntracked,
		},
	}
	dirty, ok := isDirtyFromCachedStatus(su)
	if !ok {
		t.Error("expected ok=true")
	}
	if !dirty {
		t.Error("expected dirty for modified files")
	}
}

func TestIsDirtyFromCachedStatusUntrackedOnly(t *testing.T) {
	su := &StatusUpdate{
		StatusMap: map[string]GitFileState{
			"untracked.go": GitUntracked,
			"ignored.go":   GitIgnored,
		},
	}
	dirty, ok := isDirtyFromCachedStatus(su)
	if !ok {
		t.Error("expected ok=true")
	}
	if dirty {
		t.Error("expected not dirty for untracked+ignored only")
	}
}

// =============================================================================
// Ref Index Tests
// =============================================================================

func TestRefIndexLazyLoad(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	// Create some snapshot refs directly.
	client.mu.Lock()
	_, _ = client.createSnapshot(OpRebase)
	_, _ = client.createSnapshot(OpReset)
	client.mu.Unlock()

	// Create a ref index — should lazy-load and find both snapshots.
	idx := newRefIndex(client)
	snaps := idx.listSnaps()
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots from lazy load, got %d", len(snaps))
	}

	// Verify newest-first ordering.
	if snaps[0].Timestamp.Before(snaps[1].Timestamp) {
		t.Error("expected newest-first ordering")
	}
}

func TestRefIndexAddAndRemove(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	idx := newRefIndex(client)

	// Force initial load (empty).
	snaps := idx.listSnaps()
	if len(snaps) != 0 {
		t.Fatalf("expected 0 snapshots initially, got %d", len(snaps))
	}

	// Add a snapshot to the index.
	ref := SnapshotRef{
		Name:      "refs/sylk/snap/rebase-100",
		Hash:      plumbing.NewHash("abc1234567890abcdef1234567890abcdef12345"),
		Op:        OpRebase,
		Timestamp: time.Unix(0, 100),
	}
	idx.addSnap(ref)

	snaps = idx.listSnaps()
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot after add, got %d", len(snaps))
	}

	// Remove it.
	idx.removeRef(ref.Name)
	snaps = idx.listSnaps()
	if len(snaps) != 0 {
		t.Fatalf("expected 0 snapshots after remove, got %d", len(snaps))
	}
}

func TestRefIndexCategories(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	headHash, _ := client.GetHead()
	hash, _ := client.resolveCommitHash(headHash)

	// Create refs in all three categories.
	client.mu.Lock()
	_, _ = client.createSnapshot(OpRebase)
	_, _ = client.createBranchBackup("feature", hash)
	_, _ = client.createDetachedBackup(hash)
	client.mu.Unlock()

	idx := newRefIndex(client)

	if len(idx.listSnaps()) != 1 {
		t.Errorf("expected 1 snap, got %d", len(idx.listSnaps()))
	}
	if len(idx.listDeleted()) != 1 {
		t.Errorf("expected 1 deleted, got %d", len(idx.listDeleted()))
	}
	if len(idx.listDetached()) != 1 {
		t.Errorf("expected 1 detached, got %d", len(idx.listDetached()))
	}
}

// =============================================================================
// Reverse-Scan Recovery Tests
// =============================================================================

func TestJournalReverseScanComplete(t *testing.T) {
	dir := t.TempDir()

	j, err := OpenJournal(dir)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()

	// Write multiple Begin+Complete pairs.
	for range 5 {
		entry := &JournalEntry{
			Op:        OpCommitFiles,
			Timestamp: time.Now(),
		}
		if err := j.LogBegin(entry); err != nil {
			t.Fatalf("log begin: %v", err)
		}
		if err := j.LogComplete(entry); err != nil {
			t.Fatalf("log complete: %v", err)
		}
	}

	// All entries are complete — should find 0 incomplete.
	incomplete, err := j.FindIncompleteOps()
	if err != nil {
		t.Fatalf("find incomplete: %v", err)
	}
	if len(incomplete) != 0 {
		t.Errorf("expected 0 incomplete, got %d", len(incomplete))
	}
}

func TestJournalReverseScanIncomplete(t *testing.T) {
	dir := t.TempDir()

	j, err := OpenJournal(dir)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()

	// Write 3 complete pairs, then 1 incomplete Begin.
	for range 3 {
		entry := &JournalEntry{
			Op:        OpCommitFiles,
			Timestamp: time.Now(),
		}
		_ = j.LogBegin(entry)
		_ = j.LogComplete(entry)
	}

	incomplete_entry := &JournalEntry{
		Op:        OpRebaseInteractive,
		Timestamp: time.Now(),
		SnapRef:   "refs/sylk/snap/rebase-999",
	}
	_ = j.LogBegin(incomplete_entry)

	incomplete, err := j.FindIncompleteOps()
	if err != nil {
		t.Fatalf("find incomplete: %v", err)
	}
	if len(incomplete) != 1 {
		t.Fatalf("expected 1 incomplete, got %d", len(incomplete))
	}
	if incomplete[0].Op != OpRebaseInteractive {
		t.Errorf("op: got %v, want %v", incomplete[0].Op, OpRebaseInteractive)
	}
}

// =============================================================================
// Sequencer Coalescing Tests
// =============================================================================

func TestSequencerCoalescingSkipsSnapshot(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	bus := NewGitBus(client)
	sg, err := NewSafetyGuard(client, bus, DefaultSafetyConfig(), nil)
	if err != nil {
		t.Fatalf("new safety guard: %v", err)
	}
	defer sg.Close()

	// Simulate a sequencer start event (PhasePre).
	startEvent := &GitEvent{
		Op:        OpRebaseInteractive,
		Phase:     PhasePre,
		Timestamp: time.Now(),
	}
	sg.onEvent(startEvent)

	// Count snapshots after start.
	snapsBefore := len(sg.refIdx.listSnaps())

	// Simulate a sequencer step event (PhasePre).
	stepEvent := &GitEvent{
		Op:        OpSequencerContinue,
		Phase:     PhasePre,
		Timestamp: time.Now(),
	}
	sg.onEvent(stepEvent)

	// Step should NOT create an additional snapshot (coalescing).
	snapsAfter := len(sg.refIdx.listSnaps())
	if snapsAfter != snapsBefore {
		t.Errorf("expected no new snapshots for sequencer step, got %d new",
			snapsAfter-snapsBefore)
	}
}

func TestSequencerCoalescingResetsOnAbort(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	bus := NewGitBus(client)
	sg, err := NewSafetyGuard(client, bus, DefaultSafetyConfig(), nil)
	if err != nil {
		t.Fatalf("new safety guard: %v", err)
	}
	defer sg.Close()

	// Start sequence.
	sg.onEvent(&GitEvent{
		Op:        OpRebaseInteractive,
		Phase:     PhasePre,
		Timestamp: time.Now(),
	})

	if !sg.seqActive {
		t.Error("expected seqActive after sequence start")
	}

	// Simulate abort.
	sg.onEvent(&GitEvent{
		Op:        OpSequencerAbort,
		Phase:     PhasePost,
		Timestamp: time.Now(),
	})

	if sg.seqActive {
		t.Error("expected seqActive=false after abort")
	}
}

// =============================================================================
// Guard IsDirtyWorktree Tests
// =============================================================================

func TestGuardIsDirtyWorktreeWithoutWatcher(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	bus := NewGitBus(client)
	sg, err := NewSafetyGuard(client, bus, DefaultSafetyConfig(), nil)
	if err != nil {
		t.Fatalf("new safety guard: %v", err)
	}
	defer sg.Close()

	// Clean repo without watcher should return false.
	if sg.IsDirtyWorktree() {
		t.Error("expected clean worktree")
	}
}

// =============================================================================
// Large File Guard Tests
// =============================================================================

func TestScanStagedLargeFilesEmpty(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	files, err := client.ScanStagedLargeFiles(nil, 5*1024*1024)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestScanStagedLargeFilesDetectsLargeFile(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	// Stage a file larger than threshold.
	bigFile := filepath.Join(dir, "big.dat")
	data := make([]byte, 100)
	os.WriteFile(bigFile, data, 0o644)

	repo, _ := gogit.PlainOpen(dir)
	wt, _ := repo.Worktree()
	wt.Add("big.dat")

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	// Threshold of 50 bytes should flag the file.
	files, err := client.ScanStagedLargeFiles([]string{"big.dat"}, 50)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 large file, got %d", len(files))
	}
	if files[0].Path != "big.dat" {
		t.Errorf("path: got %q, want %q", files[0].Path, "big.dat")
	}
}

func TestScanStagedLargeFilesDetectsBinary(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	binFile := filepath.Join(dir, "app.exe")
	os.WriteFile(binFile, []byte("MZ"), 0o644)

	repo, _ := gogit.PlainOpen(dir)
	wt, _ := repo.Worktree()
	wt.Add("app.exe")

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	files, err := client.ScanStagedLargeFiles([]string{"app.exe"}, 5*1024*1024)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 binary file, got %d", len(files))
	}
	if !files[0].Binary {
		t.Error("expected Binary=true for .exe file")
	}
}

// =============================================================================
// Secret Scanner Tests
// =============================================================================

func TestScanStagedSecretsClean(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	findings, err := client.ScanStagedSecrets(nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(findings))
	}
}

func TestScanStagedSecretsDetectsAWSKey(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	secretFile := filepath.Join(dir, "config.go")
	os.WriteFile(secretFile, []byte("const key = \"AKIAIOSFODNN7EXAMPLE\"\n"), 0o644)

	repo, _ := gogit.PlainOpen(dir)
	wt, _ := repo.Worktree()
	wt.Add("config.go")

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	findings, err := client.ScanStagedSecrets([]string{"config.go"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least 1 finding for AWS key")
	}
	if findings[0].PatternName != "AWS Access Key" {
		t.Errorf("pattern: got %q, want %q", findings[0].PatternName, "AWS Access Key")
	}
}

func TestScanStagedSecretsDetectsPrivateKey(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	keyFile := filepath.Join(dir, "key.pem")
	os.WriteFile(keyFile, []byte("-----BEGIN RSA PRIVATE KEY-----\nfoo\n-----END RSA PRIVATE KEY-----\n"), 0o644)

	repo, _ := gogit.PlainOpen(dir)
	wt, _ := repo.Worktree()
	wt.Add("key.pem")

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	findings, err := client.ScanStagedSecrets([]string{"key.pem"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least 1 finding for private key")
	}
	found := false
	for _, f := range findings {
		if f.PatternName == "Private Key" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Private Key pattern match")
	}
}

// =============================================================================
// Sequencer File State Tests
// =============================================================================

func TestDetectSequencerFileStateInactive(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	state := client.DetectSequencerFileState()
	if state != nil && state.Active {
		t.Error("expected inactive sequencer state")
	}
}

func TestDetectSequencerFileStateMerge(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	// Create MERGE_HEAD marker to simulate active merge.
	gitDir := filepath.Join(dir, ".git")
	mergeHead := filepath.Join(gitDir, "MERGE_HEAD")
	os.WriteFile(mergeHead, []byte("abc1234567890abcdef1234567890abcdef12345\n"), 0o644)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	state := client.DetectSequencerFileState()
	if state == nil || !state.Active {
		t.Fatal("expected active sequencer state")
	}
	if state.Type != "merge" {
		t.Errorf("type: got %q, want %q", state.Type, "merge")
	}
}

func TestDetectSequencerFileStateCherryPick(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	gitDir := filepath.Join(dir, ".git")
	cpHead := filepath.Join(gitDir, "CHERRY_PICK_HEAD")
	os.WriteFile(cpHead, []byte("abc1234567890abcdef1234567890abcdef12345\n"), 0o644)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	state := client.DetectSequencerFileState()
	if state == nil || !state.Active {
		t.Fatal("expected active sequencer state")
	}
	if state.Type != "cherry-pick" {
		t.Errorf("type: got %q, want %q", state.Type, "cherry-pick")
	}
}

func TestDetectSequencerFileStateRebase(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	gitDir := filepath.Join(dir, ".git")
	rebaseDir := filepath.Join(gitDir, "rebase-merge")
	os.MkdirAll(rebaseDir, 0o755)
	os.WriteFile(filepath.Join(rebaseDir, "msgnum"), []byte("2\n"), 0o644)
	os.WriteFile(filepath.Join(rebaseDir, "end"), []byte("5\n"), 0o644)
	os.WriteFile(filepath.Join(rebaseDir, "head-name"), []byte("refs/heads/feature\n"), 0o644)
	os.WriteFile(filepath.Join(rebaseDir, "onto"), []byte("abc1234567890abcdef1234567890abcdef12345\n"), 0o644)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	state := client.DetectSequencerFileState()
	if state == nil || !state.Active {
		t.Fatal("expected active sequencer state")
	}
	if state.Type != "rebase" {
		t.Errorf("type: got %q, want %q", state.Type, "rebase")
	}
	if state.CurrentStep != 2 {
		t.Errorf("step: got %d, want 2", state.CurrentStep)
	}
	if state.TotalSteps != 5 {
		t.Errorf("total: got %d, want 5", state.TotalSteps)
	}
	if state.OriginalRef != "feature" {
		t.Errorf("original: got %q, want %q", state.OriginalRef, "feature")
	}
}

// =============================================================================
// Conflict Preview Tests
// =============================================================================

func TestPreviewMergeClean(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	// Create a branch with a non-conflicting change.
	headHash, _ := client.GetHead()
	client.CreateBranch("feature", headHash)

	repo := client.Repository()
	wt, _ := repo.Worktree()

	// Checkout feature, add a new file.
	client.CheckoutBranch("feature")
	os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature content"), 0o644)
	wt.Add("feature.txt")
	wt.Commit("feature commit", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "t@t.com", When: time.Now()},
	})

	// Switch back to main.
	client.CheckoutBranch("master")

	result, err := client.PreviewMerge("feature", "master")
	if err != nil {
		t.Fatalf("preview merge: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Clean {
		t.Errorf("expected clean merge, got %d conflicts", len(result.Conflicts))
	}
}

func TestPreviewMergeConflict(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	headHash, _ := client.GetHead()
	client.CreateBranch("feature", headHash)

	repo := client.Repository()
	wt, _ := repo.Worktree()

	// Make conflicting changes on feature.
	client.CheckoutBranch("feature")
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("feature version"), 0o644)
	wt.Add("test.txt")
	wt.Commit("feature change", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "t@t.com", When: time.Now()},
	})

	// Make conflicting changes on master.
	client.CheckoutBranch("master")
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("master version"), 0o644)
	wt.Add("test.txt")
	wt.Commit("master change", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "t@t.com", When: time.Now()},
	})

	result, err := client.PreviewMerge("feature", "master")
	if err != nil {
		t.Fatalf("preview merge: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Clean {
		t.Error("expected conflicts")
	}
	if len(result.Conflicts) == 0 {
		t.Error("expected at least one conflict")
	}
}

// =============================================================================
// Divergence Tests
// =============================================================================

func TestComputeDivergenceNoUpstream(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	// Local-only branch with no remote → should return nil info.
	info, err := client.ComputeDivergence("master")
	if err == nil && info != nil && info.UpstreamName != "" {
		t.Error("expected no upstream for local-only repo")
	}
}

func TestComputeDivergenceBatchEmpty(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	info, err := client.ComputeDivergenceBatch(nil)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(info) != 0 {
		t.Errorf("expected 0 divergence info, got %d", len(info))
	}
}

// =============================================================================
// Integration Detection Tests
// =============================================================================

func TestDetectIntegratedCommitsNone(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	headHash, _ := client.GetHead()
	client.CreateBranch("feature", headHash)

	repo := client.Repository()
	wt, _ := repo.Worktree()

	// Add a unique commit on feature.
	client.CheckoutBranch("feature")
	os.WriteFile(filepath.Join(dir, "unique.txt"), []byte("unique"), 0o644)
	wt.Add("unique.txt")
	hash, _ := wt.Commit("unique commit", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "t@t.com", When: time.Now()},
	})

	results, err := client.DetectIntegratedCommits([]string{hash.String()}, "master")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Integrated {
		t.Error("expected Integrated=false for unique commit")
	}
}

// =============================================================================
// Abort Preservation Tests
// =============================================================================

func TestPreserveBeforeAbortClean(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	pres, err := client.PreserveBeforeAbort("test-abort")
	if err != nil {
		t.Fatalf("preserve: %v", err)
	}
	if pres == nil {
		t.Fatal("expected non-nil preservation")
	}
	if !strings.HasPrefix(pres.BackupBranch, abortRefPrefix) {
		t.Errorf("expected abort ref prefix, got %q", pres.BackupBranch)
	}
}

func TestPreserveBeforeAbortDirty(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	// Modify a tracked file.
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("modified"), 0o644)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	pres, err := client.PreserveBeforeAbort("dirty-abort")
	if err != nil {
		t.Fatalf("preserve: %v", err)
	}
	if pres == nil {
		t.Fatal("expected non-nil preservation")
	}
	if !strings.HasPrefix(pres.BackupBranch, abortRefPrefix) {
		t.Errorf("expected abort ref prefix, got %q", pres.BackupBranch)
	}
}

// =============================================================================
// Branch Stash Tests
// =============================================================================

func TestHasBranchStashEmpty(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	if client.HasBranchStash("master") {
		t.Error("expected no branch stash initially")
	}
}

func TestListBranchStashesEmpty(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	stashes, err := client.ListBranchStashes()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stashes) != 0 {
		t.Errorf("expected 0 stashes, got %d", len(stashes))
	}
}

// =============================================================================
// Ref Index Extended Category Tests
// =============================================================================

func TestRefIndexAbortAndStash(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	idx := newRefIndex(client)

	// Initially empty.
	if len(idx.listAborts()) != 0 {
		t.Errorf("expected 0 aborts initially")
	}
	if len(idx.listStashes()) != 0 {
		t.Errorf("expected 0 stashes initially")
	}

	// Add abort ref.
	abortRef := SnapshotRef{
		Name:      abortRefPrefix + "test-100",
		Hash:      plumbing.NewHash("abc1234567890abcdef1234567890abcdef12345"),
		Timestamp: time.Now(),
	}
	idx.addAbort(abortRef)
	if len(idx.listAborts()) != 1 {
		t.Fatalf("expected 1 abort after add, got %d", len(idx.listAborts()))
	}

	// Add stash ref.
	stashRef := SnapshotRef{
		Name:      stashRefPrefix + "feature-200",
		Hash:      plumbing.NewHash("def1234567890abcdef1234567890abcdef12345"),
		Timestamp: time.Now(),
	}
	idx.addStash(stashRef)
	if len(idx.listStashes()) != 1 {
		t.Fatalf("expected 1 stash after add, got %d", len(idx.listStashes()))
	}

	// Check hasStashForBranch.
	if !idx.hasStashForBranch("feature") {
		t.Error("expected hasStashForBranch=true for feature")
	}
	if idx.hasStashForBranch("nonexistent") {
		t.Error("expected hasStashForBranch=false for nonexistent")
	}

	// Remove abort ref.
	idx.removeRef(abortRef.Name)
	if len(idx.listAborts()) != 0 {
		t.Errorf("expected 0 aborts after remove, got %d", len(idx.listAborts()))
	}

	// Remove stash ref.
	idx.removeRef(stashRef.Name)
	if len(idx.listStashes()) != 0 {
		t.Errorf("expected 0 stashes after remove, got %d", len(idx.listStashes()))
	}
}

// =============================================================================
// GC Abort and Stash Refs Tests
// =============================================================================

func TestGCSnapshotsIncludesAbortAndStash(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	bus := NewGitBus(client)
	cfg := SafetyConfig{
		SnapshotRetention: 0, // Immediately expire.
		MaxSnapshots:      0,
	}
	sg, err := NewSafetyGuard(client, bus, cfg, nil)
	if err != nil {
		t.Fatalf("new safety guard: %v", err)
	}
	defer sg.Close()

	// Add old abort and stash refs to the index.
	oldTime := time.Now().Add(-time.Hour)
	hash := plumbing.NewHash("abc1234567890abcdef1234567890abcdef12345")

	abortRef := plumbing.NewHashReference(
		plumbing.ReferenceName(abortRefPrefix+"old-100"), hash)
	client.mu.Lock()
	client.repo.Storer.SetReference(abortRef)
	client.mu.Unlock()
	sg.refIdx.addAbort(SnapshotRef{
		Name: abortRefPrefix + "old-100", Hash: hash, Timestamp: oldTime,
	})

	stashRef := plumbing.NewHashReference(
		plumbing.ReferenceName(stashRefPrefix+"old-200"), hash)
	client.mu.Lock()
	client.repo.Storer.SetReference(stashRef)
	client.mu.Unlock()
	sg.refIdx.addStash(SnapshotRef{
		Name: stashRefPrefix + "old-200", Hash: hash, Timestamp: oldTime,
	})

	removed, err := sg.GCSnapshots()
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if removed < 2 {
		t.Errorf("expected at least 2 refs GC'd (abort + stash), got %d", removed)
	}
}
