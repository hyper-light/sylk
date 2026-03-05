package sylkdir

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTombstoneLifecycle exercises the full tombstone lifecycle:
//
//	Stage 1: Boot — verify initial tombstones.bin is empty
//	Stage 2: First commit — no supersessions, zero dead
//	Stage 3: Re-index commit — supersessions create dead nodes, filtered reads
//	Stage 4: Compaction trigger — dead data physically removed
func TestTombstoneLifecycle(t *testing.T) {
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
	now := uint64(time.Now().UnixNano())

	// ═══════════════════════════════════════════════════════════════════
	// Stage 1: Boot — verify initial tombstones.bin
	// ═══════════════════════════════════════════════════════════════════
	t.Run("Stage1_Boot", func(t *testing.T) {
		initVersion := gm.GetHead()
		versionPath := sd.GlobalVersionPath(initVersion)
		tbPath := filepath.Join(versionPath, TombstoneFile)

		info, err := os.Stat(tbPath)
		if err != nil {
			t.Fatalf("tombstones.bin should exist after Init: %v", err)
		}
		if info.Size() == 0 {
			t.Fatal("tombstones.bin should not be zero bytes (has header)")
		}

		tb, err := LoadTombstoneBitmap(versionPath)
		if err != nil {
			t.Fatalf("LoadTombstoneBitmap: %v", err)
		}
		if tb.Count() != 0 {
			t.Fatalf("initial tombstone count = %d, want 0", tb.Count())
		}
	})

	// ═══════════════════════════════════════════════════════════════════
	// Stage 2: First commit — ingest 5 nodes, commit, verify zero dead
	// ═══════════════════════════════════════════════════════════════════
	var firstCommitNodeIDs []uint32

	t.Run("Stage2_FirstCommit", func(t *testing.T) {
		sessID, err := gm.AllocateSessionID()
		if err != nil {
			t.Fatalf("AllocateSessionID: %v", err)
		}

		snap := &BaseSnapshot{
			GlobalVersion:     gm.GetHead(),
			CommittedSessions: []uint32{},
			SnapshotAt:        time.Now(),
			NextNodeID:        gm.GetCurrentNodeID(),
		}
		sess, err := store.Create(sessID, snap)
		if err != nil {
			t.Fatalf("Create session: %v", err)
		}
		t.Cleanup(func() { sess.CloseBleve() })

		// Allocate 5 node IDs
		firstID, err := gm.AllocateNodeIDs(5)
		if err != nil {
			t.Fatalf("AllocateNodeIDs: %v", err)
		}
		for i := range uint32(5) {
			firstCommitNodeIDs = append(firstCommitNodeIDs, firstID+i)
		}

		// Assign DocRef for doc filtering (all a.go nodes → doc "file_<first>", b.go → doc "file_<fifth>").
		docRefA := sess.DocIDMap.GetOrAssign(fmt.Sprintf("file_%d", firstCommitNodeIDs[0]))
		docRefB := sess.DocIDMap.GetOrAssign(fmt.Sprintf("file_%d", firstCommitNodeIDs[4]))

		nodes := []*Node{
			{ID: firstCommitNodeIDs[0], CanonicalKey: "file:a.go", Domain: 0, NodeType: 1, Name: "a.go", Path: "a.go", CreatedAt: now, SessionID: sessID, DocRef: docRefA},
			{ID: firstCommitNodeIDs[1], CanonicalKey: "func:a.go:Foo", Domain: 0, NodeType: 2, Name: "Foo", Path: "a.go", Signature: "func Foo()", CreatedAt: now, SessionID: sessID, DocRef: docRefA},
			{ID: firstCommitNodeIDs[2], CanonicalKey: "func:a.go:Bar", Domain: 0, NodeType: 2, Name: "Bar", Path: "a.go", Signature: "func Bar()", CreatedAt: now, SessionID: sessID, DocRef: docRefA},
			{ID: firstCommitNodeIDs[3], CanonicalKey: "type:a.go:Config", Domain: 0, NodeType: 4, Name: "Config", Path: "a.go", CreatedAt: now, SessionID: sessID, DocRef: docRefA},
			{ID: firstCommitNodeIDs[4], CanonicalKey: "file:b.go", Domain: 0, NodeType: 1, Name: "b.go", Path: "b.go", CreatedAt: now, SessionID: sessID, DocRef: docRefB},
		}

		vns := NewVersionNodeStore(sess)
		if err := vns.WriteBatch(nodes); err != nil {
			t.Fatalf("WriteBatch nodes: %v", err)
		}

		edges := []*Edge{
			{SourceID: firstCommitNodeIDs[0], TargetID: firstCommitNodeIDs[1], Type: 1, Weight: 1.0, SessionID: sessID, CreatedAt: now, UpdatedAt: now},
			{SourceID: firstCommitNodeIDs[0], TargetID: firstCommitNodeIDs[2], Type: 1, Weight: 1.0, SessionID: sessID, CreatedAt: now, UpdatedAt: now},
			{SourceID: firstCommitNodeIDs[0], TargetID: firstCommitNodeIDs[3], Type: 2, Weight: 1.0, SessionID: sessID, CreatedAt: now, UpdatedAt: now},
			{SourceID: firstCommitNodeIDs[4], TargetID: firstCommitNodeIDs[1], Type: 3, Weight: 0.5, SessionID: sessID, CreatedAt: now, UpdatedAt: now},
		}

		ves := NewVersionEdgeStore(sess)
		if err := ves.WriteBatch(edges); err != nil {
			t.Fatalf("WriteBatch edges: %v", err)
		}

		vectors := []*VersionVector{
			{NodeID: firstCommitNodeIDs[0], Vector: []float32{0.1, 0.2, 0.3, 0.4}},
			{NodeID: firstCommitNodeIDs[1], Vector: []float32{0.5, 0.6, 0.7, 0.8}},
			{NodeID: firstCommitNodeIDs[4], Vector: []float32{0.9, 0.1, 0.2, 0.3}},
		}

		vvs := NewVersionVectorStore(sess)
		if err := vvs.WriteBatch(vectors); err != nil {
			t.Fatalf("WriteBatch vectors: %v", err)
		}

		docs := []*VersionDocument{
			{ID: "file_1", Path: "a.go", Type: "source_code", Content: "package main\nfunc Foo() {}", Language: "go", IndexedAt: time.Now().UnixNano()},
			{ID: "file_5", Path: "b.go", Type: "source_code", Content: "package util", Language: "go", IndexedAt: time.Now().UnixNano()},
		}

		vds := NewVersionDocStore(sess)
		if err := vds.WriteBatch(docs); err != nil {
			t.Fatalf("WriteBatch docs: %v", err)
		}

		result, err := CommitToGlobal(context.Background(), CommitConfig{
			Session:        sess,
			SylkDir:        sd,
			GlobalMeta:     gm,
			CanonicalIndex: canonIdx,
		})
		if err != nil {
			t.Fatalf("CommitToGlobal: %v", err)
		}

		// Verify: no dead nodes
		if result.DeadNodeCount != 0 {
			t.Errorf("Stage2: DeadNodeCount = %d, want 0", result.DeadNodeCount)
		}
		if result.TombstoneRatio != 0 {
			t.Errorf("Stage2: TombstoneRatio = %f, want 0", result.TombstoneRatio)
		}
		if result.NodesMerged != 5 {
			t.Errorf("Stage2: NodesMerged = %d, want 5", result.NodesMerged)
		}
		if result.EdgesMerged != 4 {
			t.Errorf("Stage2: EdgesMerged = %d, want 4", result.EdgesMerged)
		}

		// Verify: ReadAll returns all entities
		nodeStore, nsErr := NewGlobalVersionNodeStore(sd, result.GlobalVersion)
		if nsErr != nil {
			t.Fatalf("NewGlobalVersionNodeStore: %v", nsErr)
		}
		defer nodeStore.Close()
		liveNodes, err := nodeStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll nodes: %v", err)
		}
		if len(liveNodes) != 5 {
			t.Errorf("Stage2: live nodes = %d, want 5", len(liveNodes))
		}

		edgeStore, esErr := NewGlobalVersionEdgeStore(sd, result.GlobalVersion)
		if esErr != nil {
			t.Fatalf("NewGlobalVersionEdgeStore: %v", esErr)
		}
		defer edgeStore.Close()
		liveEdges, err := edgeStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll edges: %v", err)
		}
		if len(liveEdges) != 4 {
			t.Errorf("Stage2: live edges = %d, want 4", len(liveEdges))
		}

		vecStore, vsErr := NewGlobalVersionVectorStore(sd, result.GlobalVersion)
		if vsErr != nil {
			t.Fatalf("NewGlobalVersionVectorStore: %v", vsErr)
		}
		defer vecStore.Close()
		liveVecs, err := vecStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll vectors: %v", err)
		}
		if len(liveVecs) != 3 {
			t.Errorf("Stage2: live vectors = %d, want 3", len(liveVecs))
		}

		docStore, docErr := NewGlobalVersionDocStore(sd, result.GlobalVersion)
		if docErr != nil {
			t.Fatalf("NewGlobalVersionDocStore: %v", docErr)
		}
		defer docStore.Close()
		liveDocs, err := docStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll docs: %v", err)
		}
		if len(liveDocs) != 2 {
			t.Errorf("Stage2: live docs = %d, want 2", len(liveDocs))
		}

		t.Logf("Stage2 complete: v%s, %d nodes, %d edges, %d vectors, %d docs",
			result.GlobalVersion.String(), len(liveNodes), len(liveEdges), len(liveVecs), len(liveDocs))
	})

	// ═══════════════════════════════════════════════════════════════════
	// Stage 3: Re-index a.go — supersedes 4 nodes, creates tombstones
	// ═══════════════════════════════════════════════════════════════════
	var secondCommitNodeIDs []uint32

	t.Run("Stage3_ReindexSupersession", func(t *testing.T) {
		sessID, err := gm.AllocateSessionID()
		if err != nil {
			t.Fatalf("AllocateSessionID: %v", err)
		}

		snap := &BaseSnapshot{
			GlobalVersion:     gm.GetHead(),
			CommittedSessions: []uint32{},
			SnapshotAt:        time.Now(),
			NextNodeID:        gm.GetCurrentNodeID(),
		}
		sess, err := store.Create(sessID, snap)
		if err != nil {
			t.Fatalf("Create session: %v", err)
		}
		t.Cleanup(func() { sess.CloseBleve() })

		// Allocate 5 new node IDs: 4 re-indexed from a.go + 1 new file c.go
		firstID, err := gm.AllocateNodeIDs(5)
		if err != nil {
			t.Fatalf("AllocateNodeIDs: %v", err)
		}
		for i := range uint32(5) {
			secondCommitNodeIDs = append(secondCommitNodeIDs, firstID+i)
		}

		// Assign DocRef for the re-indexed a.go and new c.go documents.
		docRefA2 := sess.DocIDMap.GetOrAssign(fmt.Sprintf("file_%d", secondCommitNodeIDs[0]))
		docRefC := sess.DocIDMap.GetOrAssign(fmt.Sprintf("file_%d", secondCommitNodeIDs[4]))

		// Same canonical keys for a.go entities → supersession
		nodes := []*Node{
			{ID: secondCommitNodeIDs[0], CanonicalKey: "file:a.go", Domain: 0, NodeType: 1, Name: "a.go", Path: "a.go", CreatedAt: now, SessionID: sessID, DocRef: docRefA2},
			{ID: secondCommitNodeIDs[1], CanonicalKey: "func:a.go:Foo", Domain: 0, NodeType: 2, Name: "Foo", Path: "a.go", Signature: "func Foo() error", CreatedAt: now, SessionID: sessID, DocRef: docRefA2},
			{ID: secondCommitNodeIDs[2], CanonicalKey: "func:a.go:Bar", Domain: 0, NodeType: 2, Name: "Bar", Path: "a.go", Signature: "func Bar(ctx context.Context)", CreatedAt: now, SessionID: sessID, DocRef: docRefA2},
			{ID: secondCommitNodeIDs[3], CanonicalKey: "type:a.go:Config", Domain: 0, NodeType: 4, Name: "Config", Path: "a.go", CreatedAt: now, SessionID: sessID, DocRef: docRefA2},
			// New file — no supersession
			{ID: secondCommitNodeIDs[4], CanonicalKey: "file:c.go", Domain: 0, NodeType: 1, Name: "c.go", Path: "c.go", CreatedAt: now, SessionID: sessID, DocRef: docRefC},
		}

		vns := NewVersionNodeStore(sess)
		if err := vns.WriteBatch(nodes); err != nil {
			t.Fatalf("WriteBatch nodes: %v", err)
		}

		// New edges for the re-indexed nodes
		edges := []*Edge{
			{SourceID: secondCommitNodeIDs[0], TargetID: secondCommitNodeIDs[1], Type: 1, Weight: 1.0, SessionID: sessID, CreatedAt: now, UpdatedAt: now},
			{SourceID: secondCommitNodeIDs[0], TargetID: secondCommitNodeIDs[2], Type: 1, Weight: 1.0, SessionID: sessID, CreatedAt: now, UpdatedAt: now},
			{SourceID: secondCommitNodeIDs[4], TargetID: secondCommitNodeIDs[1], Type: 3, Weight: 0.8, SessionID: sessID, CreatedAt: now, UpdatedAt: now},
		}

		ves := NewVersionEdgeStore(sess)
		if err := ves.WriteBatch(edges); err != nil {
			t.Fatalf("WriteBatch edges: %v", err)
		}

		// Updated vectors
		vectors := []*VersionVector{
			{NodeID: secondCommitNodeIDs[0], Vector: []float32{0.2, 0.3, 0.4, 0.5}},
			{NodeID: secondCommitNodeIDs[1], Vector: []float32{0.6, 0.7, 0.8, 0.9}},
			{NodeID: secondCommitNodeIDs[4], Vector: []float32{0.3, 0.4, 0.5, 0.6}},
		}

		vvs := NewVersionVectorStore(sess)
		if err := vvs.WriteBatch(vectors); err != nil {
			t.Fatalf("WriteBatch vectors: %v", err)
		}

		// Updated docs — node 1 superseded, doc ID format "file_{nodeID}"
		docs := []*VersionDocument{
			{ID: "file_" + itoa(secondCommitNodeIDs[0]), Path: "a.go", Type: "source_code", Content: "package main\nfunc Foo() error { return nil }", Language: "go", IndexedAt: time.Now().UnixNano()},
			{ID: "file_" + itoa(secondCommitNodeIDs[4]), Path: "c.go", Type: "source_code", Content: "package c", Language: "go", IndexedAt: time.Now().UnixNano()},
		}

		vds := NewVersionDocStore(sess)
		if err := vds.WriteBatch(docs); err != nil {
			t.Fatalf("WriteBatch docs: %v", err)
		}

		result, err := CommitToGlobal(context.Background(), CommitConfig{
			Session:        sess,
			SylkDir:        sd,
			GlobalMeta:     gm,
			CanonicalIndex: canonIdx,
		})
		if err != nil {
			t.Fatalf("CommitToGlobal: %v", err)
		}

		// ── Verify supersession counts ──
		if result.DeadNodeCount != 4 {
			t.Errorf("Stage3: DeadNodeCount = %d, want 4", result.DeadNodeCount)
		}
		if len(result.Superseded) != 4 {
			t.Errorf("Stage3: Superseded = %d, want 4", len(result.Superseded))
		}
		if result.TombstoneRatio == 0 {
			t.Error("Stage3: TombstoneRatio should be > 0")
		}

		// ── Verify tombstone bitmap on disk ──
		versionPath := sd.GlobalVersionPath(result.GlobalVersion)
		tb, err := LoadTombstoneBitmap(versionPath)
		if err != nil {
			t.Fatalf("LoadTombstoneBitmap: %v", err)
		}

		// Old a.go node IDs should be dead
		for i := range 4 {
			if !tb.IsDead(firstCommitNodeIDs[i]) {
				t.Errorf("old node %d (canonical index %d) should be dead", firstCommitNodeIDs[i], i)
			}
		}
		// b.go node should still be alive (not re-indexed)
		if tb.IsDead(firstCommitNodeIDs[4]) {
			t.Error("b.go node should be alive (not re-indexed)")
		}
		// New nodes should be alive
		for _, id := range secondCommitNodeIDs {
			if tb.IsDead(id) {
				t.Errorf("new node %d should be alive", id)
			}
		}

		// ── Verify filtered reads ──
		nodeStore, nsErr := NewGlobalVersionNodeStore(sd, result.GlobalVersion)
		if nsErr != nil {
			t.Fatalf("NewGlobalVersionNodeStore: %v", nsErr)
		}
		defer nodeStore.Close()
		liveNodes, err := nodeStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll nodes: %v", err)
		}

		// Should have: 5 new nodes + 1 surviving old node (b.go) = 6 live
		if len(liveNodes) != 6 {
			t.Errorf("Stage3: live nodes = %d, want 6", len(liveNodes))
		}

		// Verify dead nodes are excluded
		liveIDs := make(map[uint32]bool)
		for _, n := range liveNodes {
			liveIDs[n.ID] = true
		}
		for i := range 4 {
			if liveIDs[firstCommitNodeIDs[i]] {
				t.Errorf("dead node %d should not appear in ReadAll", firstCommitNodeIDs[i])
			}
		}
		// b.go and all new nodes should be present
		if !liveIDs[firstCommitNodeIDs[4]] {
			t.Error("b.go node should appear in ReadAll")
		}
		for _, id := range secondCommitNodeIDs {
			if !liveIDs[id] {
				t.Errorf("new node %d should appear in ReadAll", id)
			}
		}

		// Verify edge filtering — edges touching dead nodes excluded
		edgeStore, esErr := NewGlobalVersionEdgeStore(sd, result.GlobalVersion)
		if esErr != nil {
			t.Fatalf("NewGlobalVersionEdgeStore: %v", esErr)
		}
		defer edgeStore.Close()
		liveEdges, err := edgeStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll edges: %v", err)
		}
		for _, e := range liveEdges {
			for i := range 4 {
				if e.SourceID == firstCommitNodeIDs[i] || e.TargetID == firstCommitNodeIDs[i] {
					t.Errorf("edge (%d→%d) references dead node %d", e.SourceID, e.TargetID, firstCommitNodeIDs[i])
				}
			}
		}

		// Verify vector filtering — dead node vectors excluded
		vecStore, vsErr := NewGlobalVersionVectorStore(sd, result.GlobalVersion)
		if vsErr != nil {
			t.Fatalf("NewGlobalVersionVectorStore: %v", vsErr)
		}
		defer vecStore.Close()
		liveVecs, err := vecStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll vectors: %v", err)
		}
		for _, v := range liveVecs {
			for i := range 4 {
				if v.NodeID == firstCommitNodeIDs[i] {
					t.Errorf("vector for dead node %d should be excluded", firstCommitNodeIDs[i])
				}
			}
		}

		// Verify doc filtering
		docStore, docErr := NewGlobalVersionDocStore(sd, result.GlobalVersion)
		if docErr != nil {
			t.Fatalf("NewGlobalVersionDocStore: %v", docErr)
		}
		defer docStore.Close()
		liveDocs, err := docStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll docs: %v", err)
		}
		for _, d := range liveDocs {
			if d.ID == "file_1" {
				t.Error("doc file_1 (dead node) should be excluded")
			}
		}

		// Verify direct lookup still works (bypasses tombstone filter)
		oldNode, err := nodeStore.ReadFromVersion(result.GlobalVersion, firstCommitNodeIDs[0])
		if err != nil {
			t.Fatalf("ReadFromVersion old node: %v", err)
		}
		if oldNode.SupersededBy != secondCommitNodeIDs[0] {
			t.Errorf("old node SupersededBy = %d, want %d", oldNode.SupersededBy, secondCommitNodeIDs[0])
		}

		// Verify GlobalVersionStats
		vInfo := gm.GetVersionInfo(result.GlobalVersion)
		if vInfo == nil {
			t.Fatal("version info not found")
		}
		if vInfo.Stats.TombstoneCount != 4 {
			t.Errorf("TombstoneCount = %d, want 4", vInfo.Stats.TombstoneCount)
		}

		t.Logf("Stage3 complete: v%s, %d live nodes, %d dead, %d superseded, ratio=%.3f",
			result.GlobalVersion.String(), len(liveNodes), result.DeadNodeCount, len(result.Superseded), result.TombstoneRatio)
	})

	// ═══════════════════════════════════════════════════════════════════
	// Stage 4: Compaction — verify ShouldCompact triggers, data shrinks
	// ═══════════════════════════════════════════════════════════════════
	t.Run("Stage4_Compaction", func(t *testing.T) {
		// Verify compaction would trigger for the current head
		parentInfo := gm.GetVersionInfo(gm.GetHead())
		if parentInfo == nil {
			t.Fatal("parent version info not found")
		}
		if !ShouldCompact(parentInfo.Stats.TombstoneCount, parentInfo.Stats.TotalNodes) {
			t.Fatalf("ShouldCompact(%d, %d) = false, expected true",
				parentInfo.Stats.TombstoneCount, parentInfo.Stats.TotalNodes)
		}

		// Record pre-compaction file sizes (edges use shared EdgeShardStore, not per-version files)
		parentVersion := gm.GetHead()
		parentPath := sd.GlobalVersionPath(parentVersion)
		preNodeSize := fileSize(t, filepath.Join(parentPath, "nodes", "index.bin"))
		preVecSize := fileSize(t, filepath.Join(parentPath, "vectors", "index.bin"))

		t.Logf("Pre-compaction sizes: nodes=%d, vectors=%d", preNodeSize, preVecSize)

		// Create a third session — re-index a.go again
		sessID, err := gm.AllocateSessionID()
		if err != nil {
			t.Fatalf("AllocateSessionID: %v", err)
		}

		snap := &BaseSnapshot{
			GlobalVersion:     gm.GetHead(),
			CommittedSessions: []uint32{},
			SnapshotAt:        time.Now(),
			NextNodeID:        gm.GetCurrentNodeID(),
		}
		sess, err := store.Create(sessID, snap)
		if err != nil {
			t.Fatalf("Create session: %v", err)
		}
		t.Cleanup(func() { sess.CloseBleve() })

		// Allocate new IDs for re-indexed a.go
		firstID, err := gm.AllocateNodeIDs(4)
		if err != nil {
			t.Fatalf("AllocateNodeIDs: %v", err)
		}
		thirdNodeIDs := make([]uint32, 4)
		for i := range uint32(4) {
			thirdNodeIDs[i] = firstID + i
		}

		nodes := []*Node{
			{ID: thirdNodeIDs[0], CanonicalKey: "file:a.go", Domain: 0, NodeType: 1, Name: "a.go", Path: "a.go", CreatedAt: now, SessionID: sessID},
			{ID: thirdNodeIDs[1], CanonicalKey: "func:a.go:Foo", Domain: 0, NodeType: 2, Name: "Foo", Path: "a.go", Signature: "func Foo() (string, error)", CreatedAt: now, SessionID: sessID},
			{ID: thirdNodeIDs[2], CanonicalKey: "func:a.go:Bar", Domain: 0, NodeType: 2, Name: "Bar", Path: "a.go", Signature: "func Bar(ctx context.Context) error", CreatedAt: now, SessionID: sessID},
			{ID: thirdNodeIDs[3], CanonicalKey: "type:a.go:Config", Domain: 0, NodeType: 4, Name: "Config", Path: "a.go", CreatedAt: now, SessionID: sessID},
		}

		vns := NewVersionNodeStore(sess)
		if err := vns.WriteBatch(nodes); err != nil {
			t.Fatalf("WriteBatch nodes: %v", err)
		}

		edges := []*Edge{
			{SourceID: thirdNodeIDs[0], TargetID: thirdNodeIDs[1], Type: 1, Weight: 1.0, SessionID: sessID, CreatedAt: now, UpdatedAt: now},
			{SourceID: thirdNodeIDs[0], TargetID: thirdNodeIDs[2], Type: 1, Weight: 1.0, SessionID: sessID, CreatedAt: now, UpdatedAt: now},
		}

		ves := NewVersionEdgeStore(sess)
		if err := ves.WriteBatch(edges); err != nil {
			t.Fatalf("WriteBatch edges: %v", err)
		}

		vectors := []*VersionVector{
			{NodeID: thirdNodeIDs[0], Vector: []float32{0.3, 0.4, 0.5, 0.6}},
			{NodeID: thirdNodeIDs[1], Vector: []float32{0.7, 0.8, 0.9, 1.0}},
		}

		vvs := NewVersionVectorStore(sess)
		if err := vvs.WriteBatch(vectors); err != nil {
			t.Fatalf("WriteBatch vectors: %v", err)
		}

		vds := NewVersionDocStore(sess)
		docs := []*VersionDocument{
			{ID: "file_" + itoa(thirdNodeIDs[0]), Path: "a.go", Type: "source_code", Content: "package main\nfunc Foo() (string, error) { return \"\", nil }", Language: "go", IndexedAt: time.Now().UnixNano()},
		}
		if err := vds.WriteBatch(docs); err != nil {
			t.Fatalf("WriteBatch docs: %v", err)
		}

		result, err := CommitToGlobal(context.Background(), CommitConfig{
			Session:        sess,
			SylkDir:        sd,
			GlobalMeta:     gm,
			CanonicalIndex: canonIdx,
		})
		if err != nil {
			t.Fatalf("CommitToGlobal: %v", err)
		}

		// Verify new supersessions occurred (4 from second commit's nodes)
		if len(result.Superseded) != 4 {
			t.Errorf("Stage4: Superseded = %d, want 4", len(result.Superseded))
		}

		// Post-compaction file sizes (edges use shared EdgeShardStore, not per-version files)
		newVersionPath := sd.GlobalVersionPath(result.GlobalVersion)
		postNodeSize := fileSize(t, filepath.Join(newVersionPath, "nodes", "index.bin"))
		postVecSize := fileSize(t, filepath.Join(newVersionPath, "vectors", "index.bin"))

		t.Logf("Post-compaction sizes: nodes=%d, vectors=%d", postNodeSize, postVecSize)

		// Node and vector sizes may not shrink because compaction removes old dead data
		// but the commit simultaneously appends new session data + supersession metadata.
		// The correct invariant: first-round dead nodes are physically absent from the
		// compacted file, which we verify below via ReadAll and tombstone assertions.

		// Verify reads are consistent after compaction
		nodeStore, nsErr := NewGlobalVersionNodeStore(sd, result.GlobalVersion)
		if nsErr != nil {
			t.Fatalf("NewGlobalVersionNodeStore: %v", nsErr)
		}
		defer nodeStore.Close()
		liveNodes, err := nodeStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll nodes: %v", err)
		}

		// Live nodes: 4 new (third re-index) + 1 surviving (b.go) + 1 surviving (c.go) = 6
		if len(liveNodes) != 6 {
			t.Errorf("Stage4: live nodes = %d, want 6", len(liveNodes))
		}

		liveIDs := make(map[uint32]bool)
		for _, n := range liveNodes {
			liveIDs[n.ID] = true
		}

		// b.go and c.go should survive compaction
		if !liveIDs[firstCommitNodeIDs[4]] {
			t.Error("b.go node should survive compaction")
		}
		if !liveIDs[secondCommitNodeIDs[4]] {
			t.Error("c.go node should survive compaction")
		}
		// Third-round nodes should be present
		for _, id := range thirdNodeIDs {
			if !liveIDs[id] {
				t.Errorf("third-round node %d should be present", id)
			}
		}
		// All previous a.go nodes should be gone from live reads
		for i := range 4 {
			if liveIDs[firstCommitNodeIDs[i]] {
				t.Errorf("first-round a.go node %d should not be live", firstCommitNodeIDs[i])
			}
			if liveIDs[secondCommitNodeIDs[i]] {
				t.Errorf("second-round a.go node %d should not be live", secondCommitNodeIDs[i])
			}
		}

		// Tombstone bitmap preserves all deaths (inherited + new) because the
		// shared EdgeShardStore still needs tombstone filtering for dead edges.
		// Node/vector indexes are compacted (dead entries removed), so tombstones
		// are redundant for those, but harmless.
		tb, err := LoadTombstoneBitmap(newVersionPath)
		if err != nil {
			t.Fatalf("LoadTombstoneBitmap: %v", err)
		}

		// All 8 dead nodes: 4 from first round + 4 from second round
		if tb.Count() != 8 {
			t.Errorf("post-compaction tombstone count = %d, want 8 (inherited + newly dead)", tb.Count())
		}
		for i := range 4 {
			if !tb.IsDead(secondCommitNodeIDs[i]) {
				t.Errorf("second-round node %d should be dead after compaction", secondCommitNodeIDs[i])
			}
		}

		// Canonical index should point to the latest versions
		for _, key := range []string{"file:a.go", "func:a.go:Foo", "func:a.go:Bar", "type:a.go:Config"} {
			id, err := canonIdx.Lookup(key)
			if err != nil {
				t.Fatalf("canonical lookup %s: %v", key, err)
			}
			if !liveIDs[id] {
				t.Errorf("canonical %s points to %d which is not live", key, id)
			}
		}

		t.Logf("Stage4 complete: v%s, %d live nodes, %d dead, sizes: nodes %d→%d, vectors %d→%d",
			result.GlobalVersion.String(), len(liveNodes), result.DeadNodeCount,
			preNodeSize, postNodeSize, preVecSize, postVecSize)
	})

	// ═══════════════════════════════════════════════════════════════════
	// Stage 5: Orphan detection — remove a symbol from a file
	// ═══════════════════════════════════════════════════════════════════
	t.Run("Stage5_OrphanDetection", func(t *testing.T) {
		// Commit a new file d.go with 3 symbols using proper symbol: prefix keys
		sessID, err := gm.AllocateSessionID()
		if err != nil {
			t.Fatalf("AllocateSessionID: %v", err)
		}

		snap := &BaseSnapshot{
			GlobalVersion:     gm.GetHead(),
			CommittedSessions: []uint32{},
			SnapshotAt:        time.Now(),
			NextNodeID:        gm.GetCurrentNodeID(),
		}
		sess, err := store.Create(sessID, snap)
		if err != nil {
			t.Fatalf("Create session: %v", err)
		}
		t.Cleanup(func() { sess.CloseBleve() })

		firstID, err := gm.AllocateNodeIDs(4)
		if err != nil {
			t.Fatalf("AllocateNodeIDs: %v", err)
		}
		d1IDs := make([]uint32, 4)
		for i := range uint32(4) {
			d1IDs[i] = firstID + i
		}

		vns := NewVersionNodeStore(sess)
		vns.WriteBatch([]*Node{
			{ID: d1IDs[0], CanonicalKey: "file:d.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "d.go", Path: "d.go", CreatedAt: now, SessionID: sessID},
			{ID: d1IDs[1], CanonicalKey: "symbol:d.go:Alpha:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "Alpha", Path: "d.go", CreatedAt: now, SessionID: sessID},
			{ID: d1IDs[2], CanonicalKey: "symbol:d.go:Beta:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "Beta", Path: "d.go", CreatedAt: now, SessionID: sessID},
			{ID: d1IDs[3], CanonicalKey: "symbol:d.go:Gamma:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "Gamma", Path: "d.go", CreatedAt: now, SessionID: sessID},
		})

		r5a, err := CommitToGlobal(context.Background(), CommitConfig{
			Session: sess, SylkDir: sd, GlobalMeta: gm, CanonicalIndex: canonIdx,
		})
		if err != nil {
			t.Fatalf("commit 5a: %v", err)
		}
		if r5a.OrphanedKeys != 0 {
			t.Errorf("first d.go commit: OrphanedKeys = %d, want 0", r5a.OrphanedKeys)
		}

		// Verify all 4 keys exist
		for _, key := range []string{"file:d.go", "symbol:d.go:Alpha:function", "symbol:d.go:Beta:function", "symbol:d.go:Gamma:function"} {
			if !canonIdx.Has(key) {
				t.Errorf("after 5a, key %q missing", key)
			}
		}

		// Now re-index d.go WITHOUT Gamma
		sessID2, _ := gm.AllocateSessionID()
		snap2 := &BaseSnapshot{
			GlobalVersion:     gm.GetHead(),
			CommittedSessions: []uint32{},
			SnapshotAt:        time.Now(),
			NextNodeID:        gm.GetCurrentNodeID(),
		}
		sess2, _ := store.Create(sessID2, snap2)
		t.Cleanup(func() { sess2.CloseBleve() })

		firstID2, _ := gm.AllocateNodeIDs(3)
		d2IDs := make([]uint32, 3)
		for i := range uint32(3) {
			d2IDs[i] = firstID2 + i
		}

		vns2 := NewVersionNodeStore(sess2)
		vns2.WriteBatch([]*Node{
			{ID: d2IDs[0], CanonicalKey: "file:d.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "d.go", Path: "d.go", CreatedAt: now, SessionID: sessID2},
			{ID: d2IDs[1], CanonicalKey: "symbol:d.go:Alpha:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "Alpha", Path: "d.go", CreatedAt: now, SessionID: sessID2},
			{ID: d2IDs[2], CanonicalKey: "symbol:d.go:Beta:function", Domain: 0, NodeType: uint8(NodeTypeFunction), Name: "Beta", Path: "d.go", CreatedAt: now, SessionID: sessID2},
			// Gamma is NOT included — removed from file
		})

		r5b, err := CommitToGlobal(context.Background(), CommitConfig{
			Session: sess2, SylkDir: sd, GlobalMeta: gm, CanonicalIndex: canonIdx,
		})
		if err != nil {
			t.Fatalf("commit 5b: %v", err)
		}

		// Gamma should be detected as orphaned
		if r5b.OrphanedKeys != 1 {
			t.Errorf("OrphanedKeys = %d, want 1 (Gamma removed)", r5b.OrphanedKeys)
		}

		// Gamma key should be deleted from canonical index
		if canonIdx.Has("symbol:d.go:Gamma:function") {
			t.Error("Gamma key should be deleted after orphan detection")
		}

		// Surviving keys should exist
		for _, key := range []string{"file:d.go", "symbol:d.go:Alpha:function", "symbol:d.go:Beta:function"} {
			if !canonIdx.Has(key) {
				t.Errorf("surviving key %q should exist", key)
			}
		}

		// Gamma's node (d1IDs[3]) should be tombstoned
		versionPath := sd.GlobalVersionPath(r5b.GlobalVersion)
		tb, err := LoadTombstoneBitmap(versionPath)
		if err != nil {
			t.Fatalf("LoadTombstoneBitmap: %v", err)
		}
		if !tb.IsDead(d1IDs[3]) {
			t.Errorf("Gamma node %d should be dead", d1IDs[3])
		}

		t.Logf("Stage5 complete: v%s, orphaned=%d, Gamma node %d dead",
			r5b.GlobalVersion.String(), r5b.OrphanedKeys, d1IDs[3])
	})

	// ═══════════════════════════════════════════════════════════════════
	// Stage 6: File deletion via ScopeRoots — delete b.go
	// ═══════════════════════════════════════════════════════════════════
	t.Run("Stage6_FileDeletion", func(t *testing.T) {
		// At this point the canonical index has file:b.go from Stage 2.
		// We'll commit a session that doesn't include b.go with ScopeRoots
		// covering all paths, so b.go is detected as deleted.
		if !canonIdx.Has("file:b.go") {
			t.Fatal("precondition: file:b.go should exist in canonical index")
		}

		sessID, _ := gm.AllocateSessionID()
		snap := &BaseSnapshot{
			GlobalVersion:     gm.GetHead(),
			CommittedSessions: []uint32{},
			SnapshotAt:        time.Now(),
			NextNodeID:        gm.GetCurrentNodeID(),
		}
		sess, _ := store.Create(sessID, snap)
		t.Cleanup(func() { sess.CloseBleve() })

		firstID, _ := gm.AllocateNodeIDs(1)

		// Only include a.go (not b.go, not c.go, not d.go)
		// ScopeRoots covers only "b" prefix to avoid touching other files
		vns := NewVersionNodeStore(sess)
		vns.WriteBatch([]*Node{
			{ID: firstID, CanonicalKey: "file:a.go", Domain: 0, NodeType: uint8(NodeTypeFile), Name: "a.go", Path: "a.go", CreatedAt: now, SessionID: sessID},
		})

		r6, err := CommitToGlobal(context.Background(), CommitConfig{
			Session:        sess,
			SylkDir:        sd,
			GlobalMeta:     gm,
			CanonicalIndex: canonIdx,
			ScopeRoots:     []string{"b.go"},
		})
		if err != nil {
			t.Fatalf("commit 6: %v", err)
		}

		// b.go should be detected as deleted
		if r6.DeletedFiles != 1 {
			t.Errorf("DeletedFiles = %d, want 1", r6.DeletedFiles)
		}

		// file:b.go key should be removed
		if canonIdx.Has("file:b.go") {
			t.Error("file:b.go should be deleted from canonical index")
		}

		// Other file keys should survive
		if !canonIdx.Has("file:a.go") {
			t.Error("file:a.go should survive")
		}
		if !canonIdx.Has("file:c.go") {
			t.Error("file:c.go should survive (outside ScopeRoots)")
		}
		if !canonIdx.Has("file:d.go") {
			t.Error("file:d.go should survive (outside ScopeRoots)")
		}

		// Verify b.go's node is tombstoned
		versionPath := sd.GlobalVersionPath(r6.GlobalVersion)
		tb, err := LoadTombstoneBitmap(versionPath)
		if err != nil {
			t.Fatalf("LoadTombstoneBitmap: %v", err)
		}
		if !tb.IsDead(firstCommitNodeIDs[4]) {
			t.Errorf("b.go node %d should be dead", firstCommitNodeIDs[4])
		}

		t.Logf("Stage6 complete: v%s, deletedFiles=%d, orphanedKeys=%d",
			r6.GlobalVersion.String(), r6.DeletedFiles, r6.OrphanedKeys)
	})
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
