package sylkdir

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// setupCommitEnv creates a fully initialised SylkDir with global stores and
// a session that has ingested data.
func setupCommitEnv(t *testing.T) (*SylkDir, *GlobalMeta, *CanonicalKeyIndex, *Session) {
	t.Helper()
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load global meta: %v", err)
	}

	canonIdx := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := canonIdx.Init(); err != nil {
		t.Fatalf("Init canonical index: %v", err)
	}

	store := NewSessionStore(sd)
	snap := &BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	}
	sess, err := store.Create(1, snap)
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	t.Cleanup(func() { sess.CloseBleve() })

	// Write some data into the session's HEAD version.
	vns := NewVersionNodeStore(sess)
	ves := NewVersionEdgeStore(sess)
	vvs := NewVersionVectorStore(sess)
	vds := NewVersionDocStore(sess)

	now := uint64(time.Now().UnixNano())

	// Assign DocRef via session DocIDMap so doc filtering works after commit.
	docRef := sess.DocIDMap.GetOrAssign("file_1")

	nodes := []*Node{
		{ID: 1, CanonicalKey: "file:main.go", Domain: 0, NodeType: 1, Name: "main.go", Path: "main.go", CreatedAt: now, SessionID: 1, DocRef: docRef},
		{ID: 2, CanonicalKey: "func:main.go:Run:func", Domain: 0, NodeType: 2, Name: "Run", Path: "main.go", Signature: "func Run()", CreatedAt: now, SessionID: 1, DocRef: docRef},
		{ID: 3, CanonicalKey: "type:main.go:Config:type", Domain: 0, NodeType: 4, Name: "Config", Path: "main.go", CreatedAt: now, SessionID: 1, DocRef: docRef},
	}
	if err := vns.WriteBatch(nodes); err != nil {
		t.Fatalf("WriteBatch nodes: %v", err)
	}

	edges := []*Edge{
		{SourceID: 1, TargetID: 2, Type: 1, Weight: 1.0, SessionID: 1, CreatedAt: now, UpdatedAt: now},
		{SourceID: 1, TargetID: 3, Type: 1, Weight: 1.0, SessionID: 1, CreatedAt: now, UpdatedAt: now},
	}
	if err := ves.WriteBatch(edges); err != nil {
		t.Fatalf("WriteBatch edges: %v", err)
	}

	vectors := []*VersionVector{
		{NodeID: 1, Vector: []float32{0.1, 0.2, 0.3, 0.4}},
		{NodeID: 2, Vector: []float32{0.5, 0.6, 0.7, 0.8}},
	}
	if err := vvs.WriteBatch(vectors); err != nil {
		t.Fatalf("WriteBatch vectors: %v", err)
	}

	docs := []*VersionDocument{
		{ID: "file_1", Path: "main.go", Type: "source_code", Content: "package main", Language: "go", IndexedAt: time.Now().UnixNano()},
	}
	if err := vds.WriteBatch(docs); err != nil {
		t.Fatalf("WriteBatch docs: %v", err)
	}

	return sd, gm, canonIdx, sess
}

func TestCommitToGlobal(t *testing.T) {
	sd, gm, canonIdx, sess := setupCommitEnv(t)

	result, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:        sess,
		SylkDir:        sd,
		GlobalMeta:     gm,
		CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("CommitToGlobal: %v", err)
	}

	// Verify result
	if result.SessionID != 1 {
		t.Errorf("SessionID = %d, want 1", result.SessionID)
	}
	if result.NodesMerged != 3 {
		t.Errorf("NodesMerged = %d, want 3", result.NodesMerged)
	}
	if result.EdgesMerged != 2 {
		t.Errorf("EdgesMerged = %d, want 2", result.EdgesMerged)
	}
	if result.VectorsStaged != 2 {
		t.Errorf("VectorsStaged = %d, want 2", result.VectorsStaged)
	}
	if result.DocsStaged != 1 {
		t.Errorf("DocsStaged = %d, want 1", result.DocsStaged)
	}

	// Pre-commit version should be minor bump from v1.0.0 → v1.1.0
	expectedPreCommit := SemanticVersion{Major: 1, Minor: 1, Patch: 0}
	if !result.PreCommitVersion.Equal(expectedPreCommit) {
		t.Errorf("PreCommitVersion = %s, want %s", result.PreCommitVersion.String(), expectedPreCommit.String())
	}

	// Global version should be major bump v1.0.0 → v2.0.0 (init creates v1.0.0)
	expectedGlobal := SemanticVersion{Major: 2, Minor: 0, Patch: 0}
	if !result.GlobalVersion.Equal(expectedGlobal) {
		t.Errorf("GlobalVersion = %s, want %s", result.GlobalVersion.String(), expectedGlobal.String())
	}

	// Staged data should be populated
	if len(result.StagedVectors) != 2 {
		t.Errorf("StagedVectors len = %d, want 2", len(result.StagedVectors))
	}
	if len(result.StagedDocs) != 1 {
		t.Errorf("StagedDocs len = %d, want 1", len(result.StagedDocs))
	}

	// ── Verify global stores on disk ───────────────────────────────────

	// Read nodes back from global version store
	globalNodeStore, err := NewGlobalVersionNodeStore(sd, result.GlobalVersion)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore: %v", err)
	}
	defer globalNodeStore.Close()
	nodes, err := globalNodeStore.ReadAll()
	if err != nil {
		t.Fatalf("read global nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Errorf("global nodes count = %d, want 3", len(nodes))
	}

	// Canonical index should have all keys
	for _, key := range []string{"file:main.go", "func:main.go:Run:func", "type:main.go:Config:type"} {
		id, err := canonIdx.Lookup(key)
		if err != nil {
			t.Errorf("canonical key %q not found: %v", key, err)
			continue
		}
		if id == 0 {
			t.Errorf("canonical key %q has ID 0", key)
		}
	}

	// ── Verify global meta.json on disk ────────────────────────────────
	gmReloaded := NewGlobalMetaFromSylkDir(sd)
	if err := gmReloaded.Load(); err != nil {
		t.Fatalf("reload global meta: %v", err)
	}
	if !gmReloaded.GetHead().Equal(expectedGlobal) {
		t.Errorf("persisted global head = %s, want %s", gmReloaded.GetHead().String(), expectedGlobal.String())
	}
	cs := gmReloaded.GetCommittedSessions()
	if len(cs) != 1 {
		t.Fatalf("committed sessions = %d, want 1", len(cs))
	}
	if cs[0].SessionID != 1 {
		t.Errorf("committed session ID = %d, want 1", cs[0].SessionID)
	}
	if !cs[0].FinalVersion.Equal(expectedPreCommit) {
		t.Errorf("committed final version = %s, want %s", cs[0].FinalVersion.String(), expectedPreCommit.String())
	}
	if !cs[0].GlobalVersion.Equal(expectedGlobal) {
		t.Errorf("committed global version = %s, want %s", cs[0].GlobalVersion.String(), expectedGlobal.String())
	}

	// ── Verify session is marked committed ─────────────────────────────
	if sess.Meta.Status != SessionCommitted {
		t.Errorf("session status = %s, want committed", sess.Meta.Status)
	}
	if sess.Meta.CommittedAt == nil {
		t.Error("session CommittedAt is nil")
	}

	// Reload session from disk and verify
	sessStore := NewSessionStore(sd)
	sessReloaded, err := sessStore.Load("ses_001")
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if sessReloaded.Meta.Status != SessionCommitted {
		t.Errorf("persisted session status = %s, want committed", sessReloaded.Meta.Status)
	}
}

func TestCommitToGlobalSupersession(t *testing.T) {
	sd, gm, canonIdx, sess := setupCommitEnv(t)

	// Pre-populate global with a node that has the same canonical key.
	// Write to the initial v1.0.0 version.
	initialVersion := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	globalNodeStore, err := NewGlobalVersionNodeStore(sd, initialVersion)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore: %v", err)
	}

	existingNode := &Node{
		ID: 100, CanonicalKey: "file:main.go",
		Domain: 0, NodeType: 1, Name: "main.go", Path: "main.go",
		CreatedAt: uint64(time.Now().UnixNano()), SessionID: 0,
	}
	if err := globalNodeStore.Write(existingNode); err != nil {
		t.Fatalf("write existing node: %v", err)
	}
	if err := globalNodeStore.SaveIndexes(); err != nil {
		t.Fatalf("save indexes: %v", err)
	}
	globalNodeStore.Close()
	canonIdx.Set("file:main.go", 100)

	result, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:        sess,
		SylkDir:        sd,
		GlobalMeta:     gm,
		CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("CommitToGlobal: %v", err)
	}

	// "file:main.go" should be superseded
	if len(result.Superseded) != 1 {
		t.Fatalf("Superseded count = %d, want 1", len(result.Superseded))
	}
	pair, ok := result.Superseded["file:main.go"]
	if !ok {
		t.Fatal("file:main.go not in superseded map")
	}
	if pair[0] != 100 {
		t.Errorf("superseded old ID = %d, want 100", pair[0])
	}
	if pair[1] != 1 {
		t.Errorf("superseded new ID = %d, want 1 (session node)", pair[1])
	}

	// Canonical index should now point to the new node
	id, err := canonIdx.Lookup("file:main.go")
	if err != nil {
		t.Fatalf("canonical lookup: %v", err)
	}
	if id != 1 {
		t.Errorf("canonical ID = %d, want 1", id)
	}

	// ReadAll should exclude the dead (superseded) node
	newVersionNodeStore, err := NewGlobalVersionNodeStore(sd, result.GlobalVersion)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore: %v", err)
	}
	defer newVersionNodeStore.Close()
	liveNodes, err := newVersionNodeStore.ReadAll()
	if err != nil {
		t.Fatalf("read new version nodes: %v", err)
	}
	for _, n := range liveNodes {
		if n.ID == 100 {
			t.Fatal("dead node 100 should not appear in ReadAll (tombstone filtering)")
		}
	}

	// Verify supersession metadata via direct ID lookup (bypasses tombstone filter)
	foundOld, err := newVersionNodeStore.ReadFromVersion(result.GlobalVersion, 100)
	if err != nil {
		t.Fatalf("ReadFromVersion old node: %v", err)
	}
	if foundOld.SupersededBy != 1 {
		t.Errorf("old node SupersededBy = %d, want 1", foundOld.SupersededBy)
	}

	foundNew, err := newVersionNodeStore.ReadFromVersion(result.GlobalVersion, 1)
	if err != nil {
		t.Fatalf("ReadFromVersion new node: %v", err)
	}
	if foundNew.Supersedes != 100 {
		t.Errorf("new node Supersedes = %d, want 100", foundNew.Supersedes)
	}

	// Verify tombstone bitmap was populated
	if result.DeadNodeCount != 1 {
		t.Errorf("DeadNodeCount = %d, want 1", result.DeadNodeCount)
	}
	if result.TombstoneRatio == 0 {
		t.Error("TombstoneRatio should be > 0 after supersession")
	}

	// Verify tombstone file exists on disk and marks the old node dead
	newVersionPath := sd.GlobalVersionPath(result.GlobalVersion)
	tb, err := LoadTombstoneBitmap(newVersionPath)
	if err != nil {
		t.Fatalf("LoadTombstoneBitmap: %v", err)
	}
	if !tb.IsDead(100) {
		t.Error("old node 100 should be marked dead in tombstone bitmap")
	}
	if tb.IsDead(1) {
		t.Error("new node 1 should NOT be marked dead in tombstone bitmap")
	}

	// Verify GlobalVersionStats includes TombstoneCount
	versionInfo := gm.GetVersionInfo(result.GlobalVersion)
	if versionInfo == nil {
		t.Fatal("version info not found for new global version")
	}
	if versionInfo.Stats.TombstoneCount != 1 {
		t.Errorf("GlobalVersionStats.TombstoneCount = %d, want 1", versionInfo.Stats.TombstoneCount)
	}
}

func TestCommitToGlobalDuplicate(t *testing.T) {
	sd, gm, canonIdx, sess := setupCommitEnv(t)

	// First commit succeeds
	_, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:        sess,
		SylkDir:        sd,
		GlobalMeta:     gm,
		CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}

	// Second commit should fail (session already committed)
	_, err = CommitToGlobal(context.Background(), CommitConfig{
		Session:        sess,
		SylkDir:        sd,
		GlobalMeta:     gm,
		CanonicalIndex: canonIdx,
	})
	if err == nil {
		t.Fatal("expected error for duplicate commit")
	}
}

func TestCommitToGlobalMultipleSessions(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	canonIdx := NewCanonicalKeyIndexFromSylkDir(sd)
	canonIdx.Init()

	store := NewSessionStore(sd)
	now := uint64(time.Now().UnixNano())

	// ── Session 1 ──────────────────────────────────────────────────────
	sess1, _ := store.Create(1, &BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	})
	vns1 := NewVersionNodeStore(sess1)
	vns1.WriteBatch([]*Node{
		{ID: 1, CanonicalKey: "file:a.go", Name: "a.go", CreatedAt: now, SessionID: 1},
	})

	r1, err := CommitToGlobal(context.Background(), CommitConfig{
		Session: sess1, SylkDir: sd, GlobalMeta: gm, CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("commit 1: %v", err)
	}
	// Global: v1.0.0 → v2.0.0 (init creates v1.0.0)
	if r1.GlobalVersion.Major != 2 {
		t.Errorf("after commit 1, global major = %d, want 2", r1.GlobalVersion.Major)
	}

	// ── Session 2 ──────────────────────────────────────────────────────
	sess2, _ := store.Create(2, &BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{1},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	})
	vns2 := NewVersionNodeStore(sess2)
	vns2.WriteBatch([]*Node{
		{ID: 10, CanonicalKey: "file:b.go", Name: "b.go", CreatedAt: now, SessionID: 2},
	})

	r2, err := CommitToGlobal(context.Background(), CommitConfig{
		Session: sess2, SylkDir: sd, GlobalMeta: gm, CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("commit 2: %v", err)
	}
	// Global: v2.0.0 → v3.0.0
	if r2.GlobalVersion.Major != 3 {
		t.Errorf("after commit 2, global major = %d, want 3", r2.GlobalVersion.Major)
	}

	// ── Session 3 (supersedes file:a.go) ───────────────────────────────
	sess3, _ := store.Create(3, &BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{1, 2},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	})
	vns3 := NewVersionNodeStore(sess3)
	vns3.WriteBatch([]*Node{
		{ID: 20, CanonicalKey: "file:a.go", Name: "a.go", CreatedAt: now, SessionID: 3},
	})

	r3, err := CommitToGlobal(context.Background(), CommitConfig{
		Session: sess3, SylkDir: sd, GlobalMeta: gm, CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("commit 3: %v", err)
	}
	// Global: v3.0.0 → v4.0.0
	if r3.GlobalVersion.Major != 4 {
		t.Errorf("after commit 3, global major = %d, want 4", r3.GlobalVersion.Major)
	}
	if len(r3.Superseded) != 1 {
		t.Errorf("commit 3 superseded = %d, want 1", len(r3.Superseded))
	}

	// ── Verify entire on-disk state ────────────────────────────────────
	raw, err := os.ReadFile(sd.MetaPath())
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var doc struct {
		Version struct {
			Major uint16 `json:"major"`
		} `json:"version"`
		CommittedSessions []struct {
			SessionID     uint32 `json:"session_id"`
			GlobalVersion struct {
				Major uint16 `json:"major"`
			} `json:"global_version"`
		} `json:"committed_sessions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Init creates v1.0.0, three commits create v2, v3, v4 → doc.Version tracks global version
	if doc.Version.Major != 4 {
		t.Errorf("on-disk global major = %d, want 4", doc.Version.Major)
	}
	if len(doc.CommittedSessions) != 3 {
		t.Fatalf("on-disk committed sessions = %d, want 3", len(doc.CommittedSessions))
	}
	for i, cs := range doc.CommittedSessions {
		expectedMajor := uint16(i + 2) // First commit creates v2.0.0
		if cs.GlobalVersion.Major != expectedMajor {
			t.Errorf("session %d global major = %d, want %d", cs.SessionID, cs.GlobalVersion.Major, expectedMajor)
		}
	}

	// Canonical index: file:a.go should point to node 20 (session 3's version)
	id, err := canonIdx.Lookup("file:a.go")
	if err != nil {
		t.Fatalf("lookup file:a.go: %v", err)
	}
	if id != 20 {
		t.Errorf("canonical file:a.go = %d, want 20", id)
	}

	// ReadAll should exclude dead (superseded) nodes
	globalNodeStore, err := NewGlobalVersionNodeStore(sd, r3.GlobalVersion)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore: %v", err)
	}
	defer globalNodeStore.Close()
	liveNodes, err := globalNodeStore.ReadAll()
	if err != nil {
		t.Fatalf("read global nodes: %v", err)
	}
	for _, n := range liveNodes {
		if n.ID == 1 {
			t.Fatal("dead node 1 should not appear in ReadAll (tombstone filtering)")
		}
	}

	// Verify supersession metadata via direct ID lookup (bypasses tombstone filter)
	node1, err := globalNodeStore.ReadFromVersion(r3.GlobalVersion, 1)
	if err != nil {
		t.Fatalf("ReadFromVersion node 1: %v", err)
	}
	if node1.SupersededBy != 20 {
		t.Errorf("node 1 SupersededBy = %d, want 20", node1.SupersededBy)
	}
}

func TestCommitToGlobalOrphanDetection(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	canonIdx := NewCanonicalKeyIndexFromSylkDir(sd)
	canonIdx.Init()

	store := NewSessionStore(sd)
	now := uint64(time.Now().UnixNano())

	// ── Commit 1: main.go with 3 symbols ─────────────────────────────
	sess1, _ := store.Create(1, &BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	})
	vns1 := NewVersionNodeStore(sess1)
	ves1 := NewVersionEdgeStore(sess1)
	vns1.WriteBatch([]*Node{
		{ID: 1, CanonicalKey: "file:main.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "main.go", Path: "main.go", CreatedAt: now, SessionID: 1},
		{ID: 2, CanonicalKey: "symbol:main.go:Run:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "Run", Path: "main.go", CreatedAt: now, SessionID: 1},
		{ID: 3, CanonicalKey: "symbol:main.go:Config:type", Domain: 0, NodeType: uint8(NodeTypeType), Name: "Config", Path: "main.go", CreatedAt: now, SessionID: 1},
		{ID: 4, CanonicalKey: "symbol:main.go:OldHelper:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "OldHelper", Path: "main.go", CreatedAt: now, SessionID: 1},
	})
	ves1.WriteBatch([]*Edge{
		{SourceID: 1, TargetID: 2, Type: uint8(EdgeTypeContains), Weight: 1.0, SessionID: 1, CreatedAt: now, UpdatedAt: now},
		{SourceID: 1, TargetID: 3, Type: uint8(EdgeTypeContains), Weight: 1.0, SessionID: 1, CreatedAt: now, UpdatedAt: now},
		{SourceID: 1, TargetID: 4, Type: uint8(EdgeTypeContains), Weight: 1.0, SessionID: 1, CreatedAt: now, UpdatedAt: now},
	})

	r1, err := CommitToGlobal(context.Background(), CommitConfig{
		Session: sess1, SylkDir: sd, GlobalMeta: gm, CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("commit 1: %v", err)
	}
	if r1.OrphanedKeys != 0 {
		t.Errorf("commit 1 OrphanedKeys = %d, want 0 (first commit)", r1.OrphanedKeys)
	}

	// Verify all 4 canonical keys exist
	for _, key := range []string{"file:main.go", "symbol:main.go:Run:function", "symbol:main.go:Config:type", "symbol:main.go:OldHelper:function"} {
		if !canonIdx.Has(key) {
			t.Errorf("after commit 1, key %q missing", key)
		}
	}

	// ── Commit 2: main.go re-indexed WITHOUT OldHelper ────────────────
	sess2, _ := store.Create(2, &BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{1},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	})
	vns2 := NewVersionNodeStore(sess2)
	vns2.WriteBatch([]*Node{
		{ID: 10, CanonicalKey: "file:main.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "main.go", Path: "main.go", CreatedAt: now, SessionID: 2},
		{ID: 11, CanonicalKey: "symbol:main.go:Run:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "Run", Path: "main.go", CreatedAt: now, SessionID: 2},
		{ID: 12, CanonicalKey: "symbol:main.go:Config:type", Domain: 0, NodeType: uint8(NodeTypeType), Name: "Config", Path: "main.go", CreatedAt: now, SessionID: 2},
		// OldHelper is NOT included — it was removed from the file
	})

	r2, err := CommitToGlobal(context.Background(), CommitConfig{
		Session: sess2, SylkDir: sd, GlobalMeta: gm, CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("commit 2: %v", err)
	}

	// OldHelper should be detected as orphaned (1 key removed)
	if r2.OrphanedKeys != 1 {
		t.Errorf("OrphanedKeys = %d, want 1", r2.OrphanedKeys)
	}

	// OldHelper's canonical key should be deleted
	if canonIdx.Has("symbol:main.go:OldHelper:function") {
		t.Error("orphaned key 'symbol:main.go:OldHelper:function' should be deleted from canonical index")
	}

	// Surviving keys should still exist
	for _, key := range []string{"file:main.go", "symbol:main.go:Run:function", "symbol:main.go:Config:type"} {
		if !canonIdx.Has(key) {
			t.Errorf("surviving key %q should still exist", key)
		}
	}

	// OldHelper's node (ID=4) should be tombstoned
	newVersionPath := sd.GlobalVersionPath(r2.GlobalVersion)
	tb, err := LoadTombstoneBitmap(newVersionPath)
	if err != nil {
		t.Fatalf("LoadTombstoneBitmap: %v", err)
	}
	if !tb.IsDead(4) {
		t.Error("orphaned node 4 (OldHelper) should be marked dead")
	}

	// Superseded nodes (1, 2, 3) should also be dead
	for _, id := range []uint32{1, 2, 3} {
		if !tb.IsDead(id) {
			t.Errorf("superseded node %d should be dead", id)
		}
	}

	// New nodes (10, 11, 12) should be alive
	for _, id := range []uint32{10, 11, 12} {
		if tb.IsDead(id) {
			t.Errorf("new node %d should NOT be dead", id)
		}
	}

	// ReadAll should return only live nodes
	globalNodeStore, err := NewGlobalVersionNodeStore(sd, r2.GlobalVersion)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore: %v", err)
	}
	defer globalNodeStore.Close()
	liveNodes, err := globalNodeStore.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	liveIDs := make(map[uint32]bool)
	for _, n := range liveNodes {
		liveIDs[n.ID] = true
	}
	for _, deadID := range []uint32{1, 2, 3, 4} {
		if liveIDs[deadID] {
			t.Errorf("dead node %d should not appear in ReadAll", deadID)
		}
	}
}

func TestCommitToGlobalFileDeletion(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	canonIdx := NewCanonicalKeyIndexFromSylkDir(sd)
	canonIdx.Init()

	store := NewSessionStore(sd)
	now := uint64(time.Now().UnixNano())

	// ── Commit 1: two files (main.go, util.go) ───────────────────────
	sess1, _ := store.Create(1, &BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	})
	vns1 := NewVersionNodeStore(sess1)
	vns1.WriteBatch([]*Node{
		{ID: 1, CanonicalKey: "file:src/main.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "main.go", Path: "src/main.go", CreatedAt: now, SessionID: 1},
		{ID: 2, CanonicalKey: "symbol:src/main.go:Run:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "Run", Path: "src/main.go", CreatedAt: now, SessionID: 1},
		{ID: 3, CanonicalKey: "file:src/util.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "util.go", Path: "src/util.go", CreatedAt: now, SessionID: 1},
		{ID: 4, CanonicalKey: "symbol:src/util.go:Helper:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "Helper", Path: "src/util.go", CreatedAt: now, SessionID: 1},
		{ID: 5, CanonicalKey: "symbol:src/util.go:Util:type", Domain: 0, NodeType: uint8(NodeTypeType), Name: "Util", Path: "src/util.go", CreatedAt: now, SessionID: 1},
	})

	_, err := CommitToGlobal(context.Background(), CommitConfig{
		Session: sess1, SylkDir: sd, GlobalMeta: gm, CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("commit 1: %v", err)
	}

	// ── Commit 2: only main.go, with ScopeRoots covering "src/" ──────
	// This simulates util.go being deleted from disk.
	sess2, _ := store.Create(2, &BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{1},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	})
	vns2 := NewVersionNodeStore(sess2)
	vns2.WriteBatch([]*Node{
		{ID: 10, CanonicalKey: "file:src/main.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "main.go", Path: "src/main.go", CreatedAt: now, SessionID: 2},
		{ID: 11, CanonicalKey: "symbol:src/main.go:Run:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "Run", Path: "src/main.go", CreatedAt: now, SessionID: 2},
	})

	r2, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:    sess2,
		SylkDir:    sd,
		GlobalMeta: gm,
		CanonicalIndex: canonIdx,
		ScopeRoots: []string{"src/"},
	})
	if err != nil {
		t.Fatalf("commit 2: %v", err)
	}

	// util.go was detected as deleted (1 file)
	if r2.DeletedFiles != 1 {
		t.Errorf("DeletedFiles = %d, want 1", r2.DeletedFiles)
	}

	// OrphanedKeys should include: file:src/util.go + symbol:src/util.go:Helper:function + symbol:src/util.go:Util:type = 3
	if r2.OrphanedKeys != 3 {
		t.Errorf("OrphanedKeys = %d, want 3", r2.OrphanedKeys)
	}

	// util.go keys should be deleted from canonical index
	for _, key := range []string{"file:src/util.go", "symbol:src/util.go:Helper:function", "symbol:src/util.go:Util:type"} {
		if canonIdx.Has(key) {
			t.Errorf("deleted file key %q should be removed from canonical index", key)
		}
	}

	// main.go keys should still exist (superseded by new session)
	for _, key := range []string{"file:src/main.go", "symbol:src/main.go:Run:function"} {
		if !canonIdx.Has(key) {
			t.Errorf("surviving key %q should still exist", key)
		}
	}

	// All old nodes (3, 4, 5 from util.go, 1, 2 from main.go supersession) should be dead
	newVersionPath := sd.GlobalVersionPath(r2.GlobalVersion)
	tb, err := LoadTombstoneBitmap(newVersionPath)
	if err != nil {
		t.Fatalf("LoadTombstoneBitmap: %v", err)
	}
	for _, id := range []uint32{1, 2, 3, 4, 5} {
		if !tb.IsDead(id) {
			t.Errorf("node %d should be dead", id)
		}
	}

	// New nodes (10, 11) should be alive
	for _, id := range []uint32{10, 11} {
		if tb.IsDead(id) {
			t.Errorf("new node %d should NOT be dead", id)
		}
	}
}

func TestCommitToGlobalScopeRootsNoFalsePositives(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	canonIdx := NewCanonicalKeyIndexFromSylkDir(sd)
	canonIdx.Init()

	store := NewSessionStore(sd)
	now := uint64(time.Now().UnixNano())

	// ── Commit 1: files in two directories ───────────────────────────
	sess1, _ := store.Create(1, &BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	})
	vns1 := NewVersionNodeStore(sess1)
	vns1.WriteBatch([]*Node{
		{ID: 1, CanonicalKey: "file:src/main.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "main.go", Path: "src/main.go", CreatedAt: now, SessionID: 1},
		{ID: 2, CanonicalKey: "file:lib/helper.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "helper.go", Path: "lib/helper.go", CreatedAt: now, SessionID: 1},
	})

	_, err := CommitToGlobal(context.Background(), CommitConfig{
		Session: sess1, SylkDir: sd, GlobalMeta: gm, CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("commit 1: %v", err)
	}

	// ── Commit 2: re-index src/ only, ScopeRoots = ["src/"] ─────────
	// lib/helper.go is NOT in scope, so it should NOT be treated as deleted.
	sess2, _ := store.Create(2, &BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{1},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	})
	vns2 := NewVersionNodeStore(sess2)
	vns2.WriteBatch([]*Node{
		{ID: 10, CanonicalKey: "file:src/main.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "main.go", Path: "src/main.go", CreatedAt: now, SessionID: 2},
	})

	r2, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:    sess2,
		SylkDir:    sd,
		GlobalMeta: gm,
		CanonicalIndex: canonIdx,
		ScopeRoots: []string{"src/"},
	})
	if err != nil {
		t.Fatalf("commit 2: %v", err)
	}

	// lib/helper.go is outside ScopeRoots — should NOT be deleted
	if r2.DeletedFiles != 0 {
		t.Errorf("DeletedFiles = %d, want 0 (lib/ is outside scope)", r2.DeletedFiles)
	}

	// lib/helper.go should still be in canonical index
	if !canonIdx.Has("file:lib/helper.go") {
		t.Error("file:lib/helper.go should survive (outside ScopeRoots)")
	}
}

// setupCommitEnvWithBleve creates a commit environment with a live GlobalVersionBleveStore.
func setupCommitEnvWithBleve(t *testing.T) (*SylkDir, *GlobalMeta, *CanonicalKeyIndex, *Session, *GlobalVersionBleveStore) {
	t.Helper()
	sd, gm, canonIdx, sess := setupCommitEnv(t)

	gvbs := NewGlobalVersionBleveStore(sd, gm.GetHead())
	if err := gvbs.OpenHead(); err != nil {
		t.Fatalf("Open GlobalVersionBleveStore: %v", err)
	}
	t.Cleanup(func() { gvbs.CloseAll() })

	return sd, gm, canonIdx, sess, gvbs
}

func TestCommitToGlobalBleveIndexing(t *testing.T) {
	sd, gm, canonIdx, sess, gvbs := setupCommitEnvWithBleve(t)

	result, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:          sess,
		SylkDir:          sd,
		GlobalMeta:       gm,
		CanonicalIndex:   canonIdx,
		GlobalBleveStore: gvbs,
	})
	if err != nil {
		t.Fatalf("CommitToGlobal: %v", err)
	}

	if result.DocsIndexed != 1 {
		t.Errorf("DocsIndexed = %d, want 1", result.DocsIndexed)
	}
	if result.DocsSuperseded != 0 {
		t.Errorf("DocsSuperseded = %d, want 0", result.DocsSuperseded)
	}

	count, err := gvbs.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount: %v", err)
	}
	if count != 1 {
		t.Errorf("Bleve document count = %d, want 1", count)
	}
}

func TestCommitToGlobalBleveSupersession(t *testing.T) {
	sd, gm, canonIdx, sess, gvbs := setupCommitEnvWithBleve(t)

	// Pre-populate global with a node.
	initialVersion := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	globalNodeStore, err := NewGlobalVersionNodeStore(sd, initialVersion)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore: %v", err)
	}

	existingNode := &Node{
		ID: 100, CanonicalKey: "file:main.go",
		Domain: 0, NodeType: 1, Name: "main.go", Path: "main.go",
		CreatedAt: uint64(time.Now().UnixNano()), SessionID: 0,
	}
	if err := globalNodeStore.Write(existingNode); err != nil {
		t.Fatalf("write existing node: %v", err)
	}
	if err := globalNodeStore.SaveIndexes(); err != nil {
		t.Fatalf("save indexes: %v", err)
	}
	globalNodeStore.Close()
	canonIdx.Set("file:main.go", 100)

	// Index the old document into Bleve.
	oldDoc := &search.Document{
		ID:       "file_100",
		Path:     "main.go",
		Type:     search.DocTypeSourceCode,
		Content:  "package old",
		Checksum: search.GenerateChecksum([]byte("package old")),
	}
	if err := gvbs.Index(context.Background(), oldDoc); err != nil {
		t.Fatalf("Index old doc: %v", err)
	}

	// Verify old doc is indexed.
	countBefore, _ := gvbs.DocumentCount()
	if countBefore != 1 {
		t.Fatalf("pre-commit Bleve count = %d, want 1", countBefore)
	}

	// Commit session — node 1 supersedes node 100 on "file:main.go".
	result, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:          sess,
		SylkDir:          sd,
		GlobalMeta:       gm,
		CanonicalIndex:   canonIdx,
		GlobalBleveStore: gvbs,
	})
	if err != nil {
		t.Fatalf("CommitToGlobal: %v", err)
	}

	if result.DocsSuperseded != 1 {
		t.Errorf("DocsSuperseded = %d, want 1", result.DocsSuperseded)
	}
	if result.DocsIndexed != 1 {
		t.Errorf("DocsIndexed = %d, want 1", result.DocsIndexed)
	}

	// The old document (file_100) should be deleted, the new one (file_1) indexed.
	// Net count stays at 1 (old deleted, new added).
	countAfter, _ := gvbs.DocumentCount()
	if countAfter != 1 {
		t.Errorf("post-commit Bleve count = %d, want 1", countAfter)
	}
}

func TestCommitToGlobalBleveNil(t *testing.T) {
	sd, gm, canonIdx, sess := setupCommitEnv(t)

	// GlobalBleveStore is nil — should not panic or error.
	result, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:          sess,
		SylkDir:          sd,
		GlobalMeta:       gm,
		CanonicalIndex:   canonIdx,
		GlobalBleveStore: nil,
	})
	if err != nil {
		t.Fatalf("CommitToGlobal: %v", err)
	}

	if result.DocsIndexed != 0 {
		t.Errorf("DocsIndexed = %d, want 0 (GlobalBleveStore nil)", result.DocsIndexed)
	}
	if result.DocsStaged != 1 {
		t.Errorf("DocsStaged = %d, want 1", result.DocsStaged)
	}
}

func TestCommitToGlobalBleveSearchable(t *testing.T) {
	sd, gm, canonIdx, sess, gvbs := setupCommitEnvWithBleve(t)

	// The session has a doc with Content: "package main" (from setupCommitEnv).
	result, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:          sess,
		SylkDir:          sd,
		GlobalMeta:       gm,
		CanonicalIndex:   canonIdx,
		GlobalBleveStore: gvbs,
	})
	if err != nil {
		t.Fatalf("CommitToGlobal: %v", err)
	}
	if result.DocsIndexed != 1 {
		t.Fatalf("DocsIndexed = %d, want 1", result.DocsIndexed)
	}

	// Search for the content.
	searchResult, err := gvbs.Search(context.Background(), &search.SearchRequest{
		Query: "package main",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searchResult.TotalHits == 0 {
		t.Error("expected search results for 'package main', got 0")
	}
}

func TestConvertToSearchDocument(t *testing.T) {
	now := time.Now().UnixNano()
	vdoc := &VersionDocument{
		ID:        "file_42",
		Path:      "cmd/main.go",
		Type:      "source_code",
		Content:   "package main\nfunc main() {}",
		Language:  "go",
		IndexedAt: now,
	}

	sdoc := ConvertToSearchDocument(vdoc)

	if sdoc.ID != "file_42" {
		t.Errorf("ID = %s, want file_42", sdoc.ID)
	}
	if sdoc.Path != "cmd/main.go" {
		t.Errorf("Path = %s, want cmd/main.go", sdoc.Path)
	}
	if sdoc.Type != search.DocTypeSourceCode {
		t.Errorf("Type = %s, want source_code", sdoc.Type)
	}
	if sdoc.Content != vdoc.Content {
		t.Errorf("Content mismatch")
	}
	if sdoc.Language != "go" {
		t.Errorf("Language = %s, want go", sdoc.Language)
	}
	if sdoc.Checksum == "" {
		t.Error("Checksum is empty")
	}
	expectedChecksum := search.GenerateChecksum([]byte(vdoc.Content))
	if sdoc.Checksum != expectedChecksum {
		t.Errorf("Checksum = %s, want %s", sdoc.Checksum, expectedChecksum)
	}
	if sdoc.IndexedAt.UnixNano() != now {
		t.Errorf("IndexedAt = %d, want %d", sdoc.IndexedAt.UnixNano(), now)
	}
}

func TestCommitToGlobalClosesSessionBleve(t *testing.T) {
	sd, gm, canonIdx, sess, gvbs := setupCommitEnvWithBleve(t)

	// Verify session has a BleveStore (from Create)
	if sess.BleveStore == nil {
		t.Fatal("Expected session to have BleveStore before commit")
	}

	// Index a document into session Bleve
	ctx := context.Background()
	doc := &search.Document{
		ID:      "session-doc",
		Path:    "/test.go",
		Content: "session bleve test content",
	}
	if err := sess.BleveStore.Index(ctx, doc); err != nil {
		t.Fatalf("Index into session Bleve: %v", err)
	}

	_, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:          sess,
		SylkDir:          sd,
		GlobalMeta:       gm,
		CanonicalIndex:   canonIdx,
		GlobalBleveStore: gvbs,
	})
	if err != nil {
		t.Fatalf("CommitToGlobal: %v", err)
	}

	// After commit, session BleveStore should be nil
	if sess.BleveStore != nil {
		t.Error("Expected session BleveStore to be nil after commit")
	}
}

func TestCommitToGlobalFileLayout(t *testing.T) {
	sd, gm, canonIdx, sess := setupCommitEnv(t)

	result, err := CommitToGlobal(context.Background(), CommitConfig{
		Session:        sess,
		SylkDir:        sd,
		GlobalMeta:     gm,
		CanonicalIndex: canonIdx,
	})
	if err != nil {
		t.Fatalf("CommitToGlobal: %v", err)
	}

	// Verify global version directory was created
	globalVersionPath := sd.GlobalVersionPath(result.GlobalVersion)
	if _, err := os.Stat(globalVersionPath); os.IsNotExist(err) {
		t.Errorf("global version path %s does not exist", globalVersionPath)
	}

	// Verify global version nodes index exists
	nodesPath := globalVersionPath + "/nodes/index.bin"
	if _, err := os.Stat(nodesPath); os.IsNotExist(err) {
		t.Error("global version nodes/index.bin does not exist")
	}

	// Verify shared global edge data directory exists
	edgesPath := sd.GlobalEdgeDataPath()
	if _, err := os.Stat(edgesPath); os.IsNotExist(err) {
		t.Error("shared global edge data directory does not exist")
	}

	// Verify canonical index is persisted
	if _, err := os.Stat(sd.GlobalCanonicalIndexPath()); os.IsNotExist(err) {
		t.Error("canonical index path does not exist")
	}

	// Verify session version directories still exist (not cleaned up)
	chain := sess.GetAncestorChain()
	for _, v := range chain {
		vPath := sess.VersionPath(v)
		if _, err := os.Stat(vPath); os.IsNotExist(err) {
			t.Errorf("session version %s missing after commit", v.String())
		}
	}
}
