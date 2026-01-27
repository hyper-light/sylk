package sylkdir

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStoreCreate(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)

	baseSnapshot := &BaseSnapshot{
		CommittedSessions: []uint32{},
		SnapshotAt:        time.Now(),
		NextNodeID:        1,
	}

	sess, err := store.Create(1, baseSnapshot)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify session directory structure
	expectedDirs := []string{
		filepath.Join(sess.Path(), "base"),
		filepath.Join(sess.Path(), "versions"),
		filepath.Join(sess.Path(), "delta"),
		filepath.Join(sess.Path(), "state"),
		filepath.Join(sess.Path(), "agents"),
		filepath.Join(sess.Path(), "messages"),
		filepath.Join(sess.Path(), "versions", "v1.0.0"),
		filepath.Join(sess.Path(), "versions", "v1.0.0", "nodes"),
		filepath.Join(sess.Path(), "versions", "v1.0.0", "edges"),
		filepath.Join(sess.Path(), "versions", "v1.0.0", "vectors"),
		filepath.Join(sess.Path(), "versions", "v1.0.0", "docs"),
	}

	for _, dir := range expectedDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("Expected directory %s to exist", dir)
		}
	}

	// Verify files
	expectedFiles := []string{
		filepath.Join(sess.Path(), "meta.json"),
		filepath.Join(sess.Path(), "base", "snapshot.json"),
		filepath.Join(sess.Path(), "versions", "manifest.json"),
		filepath.Join(sess.Path(), "versions", "v1.0.0", "meta.json"),
		filepath.Join(sess.Path(), "versions", "v1.0.0", "deletions.json"),
		filepath.Join(sess.Path(), "delta", "tracker.json"),
	}

	for _, file := range expectedFiles {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			t.Errorf("Expected file %s to exist", file)
		}
	}
}

func TestSessionStoreCreateMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)

	// Create multiple sessions
	for i := uint32(1); i <= 3; i++ {
		_, err := store.Create(i, nil)
		if err != nil {
			t.Fatalf("Create session %d failed: %v", i, err)
		}
	}

	// List sessions
	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(sessions) != 3 {
		t.Errorf("Expected 3 sessions, got %d", len(sessions))
	}
}

func TestSessionStoreLoadSession(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)

	baseSnapshot := &BaseSnapshot{
		CommittedSessions: []uint32{1, 2},
		SnapshotAt:        time.Now(),
		NextNodeID:        1000,
	}

	_, err := store.Create(5, baseSnapshot)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Load session
	sess, err := store.Load("ses_005")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if sess.Meta.ID != 5 {
		t.Errorf("Meta.ID = %d, want 5", sess.Meta.ID)
	}
	if sess.Meta.StringID != "ses_005" {
		t.Errorf("Meta.StringID = %s, want ses_005", sess.Meta.StringID)
	}
	if sess.Meta.Status != SessionActive {
		t.Errorf("Meta.Status = %s, want active", sess.Meta.Status)
	}
	if len(sess.BaseSnapshot.CommittedSessions) != 2 {
		t.Errorf("BaseSnapshot.CommittedSessions len = %d, want 2", len(sess.BaseSnapshot.CommittedSessions))
	}
	if sess.BaseSnapshot.NextNodeID != 1000 {
		t.Errorf("BaseSnapshot.NextNodeID = %d, want 1000", sess.BaseSnapshot.NextNodeID)
	}
}

func TestSessionCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Initial state: HEAD = v1.0.0, one version
	v100 := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	if !sess.Manifest.Head.Equal(v100) {
		t.Errorf("Initial HEAD = %s, want v1.0.0", sess.Manifest.Head.String())
	}
	if len(sess.Manifest.Versions) != 1 {
		t.Errorf("Initial versions = %d, want 1", len(sess.Manifest.Versions))
	}

	// Create checkpoint (patch bump: v1.0.0 -> v1.0.1)
	newID, err := sess.Checkpoint("test-checkpoint", CheckpointPatch)
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	v101 := SemanticVersion{Major: 1, Minor: 0, Patch: 1}
	if !newID.Equal(v101) {
		t.Errorf("New version ID = %s, want v1.0.1", newID.String())
	}
	if !sess.Manifest.Head.Equal(v101) {
		t.Errorf("HEAD after checkpoint = %s, want v1.0.1", sess.Manifest.Head.String())
	}
	if len(sess.Manifest.Versions) != 2 {
		t.Errorf("Versions after checkpoint = %d, want 2", len(sess.Manifest.Versions))
	}

	// Verify version directory created
	v2Path := sess.VersionPath(v101)
	if _, err := os.Stat(v2Path); os.IsNotExist(err) {
		t.Errorf("Version v1.0.1 directory should exist at %s", v2Path)
	}

	// Verify docs directory exists in new version
	docsPath := sess.DocsPath(v101)
	if _, err := os.Stat(docsPath); os.IsNotExist(err) {
		t.Errorf("Docs directory should exist at %s", docsPath)
	}
}

func TestSessionCheckout(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Create several checkpoints: v1.0.0 -> v1.0.1 -> v1.0.2 -> v1.0.3
	sess.Checkpoint("v1.0.1", CheckpointPatch)
	sess.Checkpoint("v1.0.2", CheckpointPatch)
	sess.Checkpoint("v1.0.3", CheckpointPatch)

	v103 := SemanticVersion{Major: 1, Minor: 0, Patch: 3}
	if !sess.Manifest.Head.Equal(v103) {
		t.Errorf("HEAD = %s, want v1.0.3", sess.Manifest.Head.String())
	}

	// Checkout v1.0.1
	v101 := SemanticVersion{Major: 1, Minor: 0, Patch: 1}
	if err := sess.Checkout(v101); err != nil {
		t.Fatalf("Checkout failed: %v", err)
	}

	if !sess.Manifest.Head.Equal(v101) {
		t.Errorf("HEAD after checkout = %s, want v1.0.1", sess.Manifest.Head.String())
	}

	// All versions still exist
	if len(sess.Manifest.Versions) != 4 {
		t.Errorf("Versions = %d, want 4 (checkout should not delete)", len(sess.Manifest.Versions))
	}
}

func TestSessionGetAncestorChain(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Create linear history: v1.0.0 -> v1.0.1 -> v1.0.2 -> v1.0.3
	sess.Checkpoint("v1.0.1", CheckpointPatch)
	sess.Checkpoint("v1.0.2", CheckpointPatch)
	sess.Checkpoint("v1.0.3", CheckpointPatch)

	chain := sess.GetAncestorChain()
	expected := []SemanticVersion{
		{Major: 1, Minor: 0, Patch: 3},
		{Major: 1, Minor: 0, Patch: 2},
		{Major: 1, Minor: 0, Patch: 1},
		{Major: 1, Minor: 0, Patch: 0},
	}

	if len(chain) != len(expected) {
		t.Fatalf("Chain length = %d, want %d", len(chain), len(expected))
	}

	for i, v := range chain {
		if !v.Equal(expected[i]) {
			t.Errorf("Chain[%d] = %s, want %s", i, v.String(), expected[i].String())
		}
	}

	// Checkout v1.0.1 and verify chain
	v101 := SemanticVersion{Major: 1, Minor: 0, Patch: 1}
	sess.Checkout(v101)
	chain = sess.GetAncestorChain()
	expected = []SemanticVersion{
		{Major: 1, Minor: 0, Patch: 1},
		{Major: 1, Minor: 0, Patch: 0},
	}

	if len(chain) != len(expected) {
		t.Fatalf("Chain length after checkout = %d, want %d", len(chain), len(expected))
	}
}

func TestSessionSetActive(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	store.Create(1, nil)
	store.Create(2, nil)

	// Set session 1 as active
	if err := store.SetActive("ses_001"); err != nil {
		t.Fatalf("SetActive failed: %v", err)
	}

	active, err := store.GetActive()
	if err != nil {
		t.Fatalf("GetActive failed: %v", err)
	}
	if active != "ses_001" {
		t.Errorf("Active = %s, want ses_001", active)
	}

	// Switch to session 2
	if err := store.SetActive("ses_002"); err != nil {
		t.Fatalf("SetActive failed: %v", err)
	}

	active, _ = store.GetActive()
	if active != "ses_002" {
		t.Errorf("Active = %s, want ses_002", active)
	}
}

func TestSessionPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)

	// Create session and add checkpoints
	sess1, _ := store.Create(1, nil)
	sess1.Checkpoint("cp1", CheckpointPatch)
	sess1.Checkpoint("cp2", CheckpointPatch)
	sess1.Save()

	// Load session fresh
	sess2, err := store.Load("ses_001")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	v102 := SemanticVersion{Major: 1, Minor: 0, Patch: 2}
	if !sess2.Manifest.Head.Equal(v102) {
		t.Errorf("HEAD after reload = %s, want v1.0.2", sess2.Manifest.Head.String())
	}
	if len(sess2.Manifest.Versions) != 3 {
		t.Errorf("Versions after reload = %d, want 3", len(sess2.Manifest.Versions))
	}
}

func TestSessionStoreStats(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)

	// Create sessions with varying checkpoints
	sess1, _ := store.Create(1, nil)
	sess1.Checkpoint("cp1", CheckpointPatch)
	sess1.Checkpoint("cp2", CheckpointPatch)

	store.Create(2, nil) // sess2 has only initial version

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats.TotalSessions != 2 {
		t.Errorf("TotalSessions = %d, want 2", stats.TotalSessions)
	}
	if stats.ActiveSessions != 2 {
		t.Errorf("ActiveSessions = %d, want 2", stats.ActiveSessions)
	}
	if stats.TotalVersions != 4 { // 3 + 1
		t.Errorf("TotalVersions = %d, want 4", stats.TotalVersions)
	}
}

func TestSessionVersionPaths(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, _ := store.Create(1, nil)

	// Test path methods
	v100 := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	vPath := sess.VersionPath(v100)
	if !filepath.IsAbs(vPath) {
		t.Errorf("VersionPath should be absolute")
	}

	headPath := sess.HeadVersionPath()
	if headPath != vPath {
		t.Errorf("HeadVersionPath = %s, want %s", headPath, vPath)
	}

	docsPath := sess.DocsPath(v100)
	expected := filepath.Join(vPath, "docs")
	if docsPath != expected {
		t.Errorf("DocsPath = %s, want %s", docsPath, expected)
	}
}

func TestSessionBranchOnCheckout(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, _ := store.Create(1, nil)

	// Create: v1.0.0 -> v1.0.1 -> v1.0.2
	sess.Checkpoint("v1.0.1", CheckpointPatch)
	sess.Checkpoint("v1.0.2", CheckpointPatch)

	// Checkout v1.0.1
	v101 := SemanticVersion{Major: 1, Minor: 0, Patch: 1}
	sess.Checkout(v101)

	// Create branch: v1.0.0 -> v1.0.1 -> v1.0.3
	//                              \-> v1.0.2 (orphaned from HEAD but still exists)
	newID, err := sess.Checkpoint("v1.0.3-branch", CheckpointPatch)
	if err != nil {
		t.Fatalf("Checkpoint after checkout failed: %v", err)
	}

	// The new version is v1.0.2 because patch bumps from v1.0.1
	// Note: This creates a different v1.0.2 from the orphaned one
	v102 := SemanticVersion{Major: 1, Minor: 0, Patch: 2}
	if !newID.Equal(v102) {
		t.Errorf("Branch version ID = %s, want v1.0.2", newID.String())
	}

	// Verify the new v1.0.2's parent is v1.0.1
	var branchVersion *Version
	for i := range sess.Manifest.Versions {
		v := &sess.Manifest.Versions[i]
		if v.ID.Equal(v102) && v.Name == "v1.0.3-branch" {
			branchVersion = v
			break
		}
	}

	if branchVersion == nil {
		t.Fatal("Branch version not found")
	}
	if !branchVersion.ParentID.Equal(v101) {
		t.Errorf("Branch version parent = %s, want v1.0.1", branchVersion.ParentID.String())
	}

	// Ancestor chain from HEAD (v1.0.2-branch) should be: v1.0.2, v1.0.1, v1.0.0
	chain := sess.GetAncestorChain()
	expected := []SemanticVersion{
		{Major: 1, Minor: 0, Patch: 2},
		{Major: 1, Minor: 0, Patch: 1},
		{Major: 1, Minor: 0, Patch: 0},
	}
	if len(chain) != len(expected) {
		t.Fatalf("Chain length = %d, want %d", len(chain), len(expected))
	}
	for i, v := range chain {
		if !v.Equal(expected[i]) {
			t.Errorf("Chain[%d] = %s, want %s", i, v.String(), expected[i].String())
		}
	}
}

func TestSemanticVersionBumps(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, _ := store.Create(1, nil)

	// Test patch bump: v1.0.0 -> v1.0.1
	v, _ := sess.Checkpoint("patch", CheckpointPatch)
	if v.Major != 1 || v.Minor != 0 || v.Patch != 1 {
		t.Errorf("Patch bump = %s, want v1.0.1", v.String())
	}

	// Test minor bump: v1.0.1 -> v1.1.0
	v, _ = sess.Checkpoint("minor", CheckpointMinor)
	if v.Major != 1 || v.Minor != 1 || v.Patch != 0 {
		t.Errorf("Minor bump = %s, want v1.1.0", v.String())
	}

	// Test major bump: v1.1.0 -> v2.0.0
	v, _ = sess.Checkpoint("major", CheckpointMajor)
	if v.Major != 2 || v.Minor != 0 || v.Patch != 0 {
		t.Errorf("Major bump = %s, want v2.0.0", v.String())
	}
}
