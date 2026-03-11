package sylkdir

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunRecovery_RollsBackBrokenHeadAndRebuildsCanonicalIndex(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load meta: %v", err)
	}
	canonIdx := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := canonIdx.Init(); err != nil {
		t.Fatalf("Init canonical index: %v", err)
	}

	v1 := gm.GetHead()
	nodeStore, err := NewGlobalVersionNodeStore(sd, v1)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore: %v", err)
	}
	if err := nodeStore.Write(&Node{ID: 1, CanonicalKey: "file:/repo/a.go", Name: "a.go"}); err != nil {
		t.Fatalf("Write node: %v", err)
	}
	if err := nodeStore.SaveIndexes(); err != nil {
		t.Fatalf("SaveIndexes: %v", err)
	}
	if err := nodeStore.Close(); err != nil {
		t.Fatalf("Close node store: %v", err)
	}

	canonIdx.Set("file:/repo/b.go", 2)
	if err := canonIdx.Save(); err != nil {
		t.Fatalf("Save bogus canonical index: %v", err)
	}

	v2 := v1.BumpMajor()
	if err := sd.CreateGlobalVersion(v2); err != nil {
		t.Fatalf("CreateGlobalVersion: %v", err)
	}
	if err := sd.SnapshotGlobalData(v1, v2); err != nil {
		t.Fatalf("SnapshotGlobalData: %v", err)
	}
	gv := GlobalVersion{ID: v2, ParentID: v1, SessionID: 7}
	if _, err := gm.PublishCommit(gv, 7, SemanticVersion{Major: 1, Minor: 0, Patch: 1}); err != nil {
		t.Fatalf("PublishCommit: %v", err)
	}
	if err := gm.SetLastBleveIndexed(v2); err != nil {
		t.Fatalf("SetLastBleveIndexed: %v", err)
	}

	if err := os.Remove(filepath.Join(sd.GlobalVersionPath(v2), "nodes", "index.bin")); err != nil {
		t.Fatalf("Remove corrupted index: %v", err)
	}

	result, err := RunRecovery(RecoveryConfig{
		SylkDir:        sd,
		GlobalMeta:     gm,
		CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("RunRecovery: %v", err)
	}
	if !result.ManifestRepaired {
		t.Fatal("expected manifest repair")
	}
	if !result.CanonicalRebuilt {
		t.Fatal("expected canonical index rebuild")
	}
	if got := gm.GetHead(); !got.Equal(v1) {
		t.Fatalf("HEAD = %s, want %s", got.String(), v1.String())
	}
	if info := gm.GetVersionInfo(v2); info != nil {
		t.Fatalf("expected v2 to be pruned from manifest")
	}
	if _, err := canonIdx.Lookup("file:/repo/a.go"); err != nil {
		t.Fatalf("expected rebuilt canonical key for a.go: %v", err)
	}
	if _, err := canonIdx.Lookup("file:/repo/b.go"); err == nil {
		t.Fatal("expected bogus canonical key to be removed")
	}
}

func TestRunRecovery_KeepsPublishedIncompleteCommit(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load meta: %v", err)
	}

	v1 := gm.GetHead()
	v2 := v1.BumpMajor()
	if err := sd.CreateGlobalVersion(v2); err != nil {
		t.Fatalf("CreateGlobalVersion: %v", err)
	}
	if err := sd.SnapshotGlobalData(v1, v2); err != nil {
		t.Fatalf("SnapshotGlobalData: %v", err)
	}
	gv := GlobalVersion{ID: v2, ParentID: v1, SessionID: 9}
	if _, err := gm.PublishCommit(gv, 9, SemanticVersion{Major: 1, Minor: 0, Patch: 1}); err != nil {
		t.Fatalf("PublishCommit: %v", err)
	}

	cw, err := OpenCommitWAL(CommitWALConfig{
		Dir:         sd.CommitWALPath(),
		SyncOnWrite: true,
	})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	if err := logCommitBegin(cw, 9, v2); err != nil {
		t.Fatalf("logCommitBegin: %v", err)
	}

	result, err := RunRecovery(RecoveryConfig{
		SylkDir:    sd,
		GlobalMeta: gm,
		CommitWAL:  cw,
	})
	if err != nil {
		t.Fatalf("RunRecovery: %v", err)
	}
	if len(result.VersionsRemoved) != 0 {
		t.Fatalf("expected published version to be kept, removed=%v", result.VersionsRemoved)
	}
	if got := gm.GetHead(); !got.Equal(v2) {
		t.Fatalf("HEAD = %s, want %s", got.String(), v2.String())
	}
	if _, err := os.Stat(sd.GlobalVersionPath(v2)); err != nil {
		t.Fatalf("expected v2 directory to remain: %v", err)
	}
}

func TestRunRecovery_PreservesBranchesWhenDetachedHeadBreaks(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load meta: %v", err)
	}

	v1 := gm.GetHead()
	v2, err := gm.AllocateGlobalVersion()
	if err != nil {
		t.Fatalf("AllocateGlobalVersion v2: %v", err)
	}
	if err := sd.CreateGlobalVersion(v2); err != nil {
		t.Fatalf("CreateGlobalVersion v2: %v", err)
	}
	if err := sd.SnapshotGlobalData(v1, v2); err != nil {
		t.Fatalf("SnapshotGlobalData v2: %v", err)
	}
	v2Commit, err := gm.PublishCommit(GlobalVersion{ID: v2, ParentID: v1, SessionID: 1, CreatedAt: time.Now().UTC()}, 1, SemanticVersion{Major: 1, Minor: 0, Patch: 1})
	if err != nil {
		t.Fatalf("PublishCommit v2: %v", err)
	}
	if err := gm.CreateBranch("feature"); err != nil {
		t.Fatalf("CreateBranch feature: %v", err)
	}

	if err := gm.Checkout(v1); err != nil {
		t.Fatalf("Checkout v1: %v", err)
	}
	v3, err := gm.AllocateGlobalVersion()
	if err != nil {
		t.Fatalf("AllocateGlobalVersion v3: %v", err)
	}
	if err := sd.CreateGlobalVersion(v3); err != nil {
		t.Fatalf("CreateGlobalVersion v3: %v", err)
	}
	if err := sd.SnapshotGlobalData(v1, v3); err != nil {
		t.Fatalf("SnapshotGlobalData v3: %v", err)
	}
	if _, err := gm.PublishCommit(GlobalVersion{ID: v3, ParentID: v1, SessionID: 2, CreatedAt: time.Now().UTC()}, 2, SemanticVersion{Major: 1, Minor: 0, Patch: 2}); err != nil {
		t.Fatalf("PublishCommit v3: %v", err)
	}

	if err := os.Remove(filepath.Join(sd.GlobalVersionPath(v3), "nodes", "index.bin")); err != nil {
		t.Fatalf("Remove broken v3 index: %v", err)
	}

	result, err := RunRecovery(RecoveryConfig{
		SylkDir:    sd,
		GlobalMeta: gm,
	})
	if err != nil {
		t.Fatalf("RunRecovery: %v", err)
	}
	if !result.ManifestRepaired {
		t.Fatal("expected manifest repair")
	}
	if got := gm.GetHead(); !got.Equal(v2) {
		t.Fatalf("HEAD = %s, want %s", got.String(), v2.String())
	}
	if got := gm.GetActiveRef(); got != "main" {
		t.Fatalf("active ref = %q, want main", got)
	}
	graph := gm.GetCommitGraph()
	if graph.Refs["main"] != v2Commit {
		t.Fatalf("main ref = %q, want %q", graph.Refs["main"], v2Commit)
	}
	if graph.Refs["feature"] != v2Commit {
		t.Fatalf("feature ref = %q, want %q", graph.Refs["feature"], v2Commit)
	}
}
