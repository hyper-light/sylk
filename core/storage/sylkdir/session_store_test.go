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
		filepath.Join(sess.Path(), "versions", "v000001"),
		filepath.Join(sess.Path(), "versions", "v000001", "nodes"),
		filepath.Join(sess.Path(), "versions", "v000001", "edges"),
		filepath.Join(sess.Path(), "versions", "v000001", "vectors"),
		filepath.Join(sess.Path(), "versions", "v000001", "docs"),
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
		filepath.Join(sess.Path(), "versions", "v000001", "meta.json"),
		filepath.Join(sess.Path(), "versions", "v000001", "deletions.json"),
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

	// Initial state: HEAD = 1, one version
	if sess.Manifest.Head != 1 {
		t.Errorf("Initial HEAD = %d, want 1", sess.Manifest.Head)
	}
	if len(sess.Manifest.Versions) != 1 {
		t.Errorf("Initial versions = %d, want 1", len(sess.Manifest.Versions))
	}

	// Create checkpoint
	newID, err := sess.Checkpoint("test-checkpoint", "explicit")
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	if newID != 2 {
		t.Errorf("New version ID = %d, want 2", newID)
	}
	if sess.Manifest.Head != 2 {
		t.Errorf("HEAD after checkpoint = %d, want 2", sess.Manifest.Head)
	}
	if len(sess.Manifest.Versions) != 2 {
		t.Errorf("Versions after checkpoint = %d, want 2", len(sess.Manifest.Versions))
	}

	// Verify version directory created
	v2Path := sess.VersionPath(2)
	if _, err := os.Stat(v2Path); os.IsNotExist(err) {
		t.Errorf("Version 2 directory should exist at %s", v2Path)
	}

	// Verify docs directory exists in new version
	docsPath := sess.DocsPath(2)
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

	// Create several checkpoints
	sess.Checkpoint("v2", "explicit")
	sess.Checkpoint("v3", "explicit")
	sess.Checkpoint("v4", "explicit")

	if sess.Manifest.Head != 4 {
		t.Errorf("HEAD = %d, want 4", sess.Manifest.Head)
	}

	// Checkout v2
	if err := sess.Checkout(2); err != nil {
		t.Fatalf("Checkout failed: %v", err)
	}

	if sess.Manifest.Head != 2 {
		t.Errorf("HEAD after checkout = %d, want 2", sess.Manifest.Head)
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

	// Create linear history: 1 -> 2 -> 3 -> 4
	sess.Checkpoint("v2", "explicit")
	sess.Checkpoint("v3", "explicit")
	sess.Checkpoint("v4", "explicit")

	chain := sess.GetAncestorChain()
	expected := []uint32{4, 3, 2, 1}

	if len(chain) != len(expected) {
		t.Fatalf("Chain length = %d, want %d", len(chain), len(expected))
	}

	for i, v := range chain {
		if v != expected[i] {
			t.Errorf("Chain[%d] = %d, want %d", i, v, expected[i])
		}
	}

	// Checkout v2 and verify chain
	sess.Checkout(2)
	chain = sess.GetAncestorChain()
	expected = []uint32{2, 1}

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
	sess1.Checkpoint("cp1", "explicit")
	sess1.Checkpoint("cp2", "explicit")
	sess1.Save()

	// Load session fresh
	sess2, err := store.Load("ses_001")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if sess2.Manifest.Head != 3 {
		t.Errorf("HEAD after reload = %d, want 3", sess2.Manifest.Head)
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
	sess1.Checkpoint("cp1", "explicit")
	sess1.Checkpoint("cp2", "explicit")

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
	vPath := sess.VersionPath(1)
	if !filepath.IsAbs(vPath) {
		t.Errorf("VersionPath should be absolute")
	}

	headPath := sess.HeadVersionPath()
	if headPath != vPath {
		t.Errorf("HeadVersionPath = %s, want %s", headPath, vPath)
	}

	docsPath := sess.DocsPath(1)
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

	// Create: 1 -> 2 -> 3
	sess.Checkpoint("v2", "explicit")
	sess.Checkpoint("v3", "explicit")

	// Checkout v2
	sess.Checkout(2)

	// Create branch: 1 -> 2 -> 4
	//                     \-> 3 (orphaned from HEAD but still exists)
	newID, err := sess.Checkpoint("v4-branch", "explicit")
	if err != nil {
		t.Fatalf("Checkpoint after checkout failed: %v", err)
	}

	if newID != 4 {
		t.Errorf("Branch version ID = %d, want 4", newID)
	}

	// Verify v4's parent is v2
	var v4 *Version
	for i := range sess.Manifest.Versions {
		if sess.Manifest.Versions[i].ID == 4 {
			v4 = &sess.Manifest.Versions[i]
			break
		}
	}

	if v4 == nil {
		t.Fatal("Version 4 not found")
	}
	if v4.ParentID != 2 {
		t.Errorf("Version 4 parent = %d, want 2", v4.ParentID)
	}

	// Ancestor chain from HEAD (4) should be: 4, 2, 1
	chain := sess.GetAncestorChain()
	expected := []uint32{4, 2, 1}
	if len(chain) != len(expected) {
		t.Fatalf("Chain length = %d, want %d", len(chain), len(expected))
	}
	for i, v := range chain {
		if v != expected[i] {
			t.Errorf("Chain[%d] = %d, want %d", i, v, expected[i])
		}
	}
}
