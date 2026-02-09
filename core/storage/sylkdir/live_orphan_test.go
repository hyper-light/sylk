package sylkdir

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
)

// TestLiveScopeAwareOrphan exercises scope-aware orphan detection against
// real Go source files from this repository. Source files are copied to a
// temp workspace (read-only access to repo); all .sylk storage goes to
// t.TempDir(). The repo is never modified.
//
// Stages:
//
//	1. Initial ingest of 3 real .go files — verify zero orphans
//	2. Remove MarkDead method from tombstone.go copy — verify orphan detected
//	3. Delete canonical_index.go copy + ScopeRoots — verify file deletion detected
//	4. Verify cumulative state: canonical index ↔ live nodes consistency
func TestLiveScopeAwareOrphan(t *testing.T) {
	// Source files from this package (test runs from core/storage/sylkdir/)
	sourceFiles := []string{"tombstone.go", "canonical_index.go", "commit.go"}
	for _, f := range sourceFiles {
		if _, err := os.Stat(f); err != nil {
			t.Skipf("source file %s not found (not running from package dir?), skipping", f)
		}
	}

	// Copy to temp workspace — repo files are read-only
	workspace := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	for _, f := range sourceFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if err := os.WriteFile(filepath.Join(workspace, f), data, 0644); err != nil {
			t.Fatalf("copy %s: %v", f, err)
		}
	}

	// .sylk storage in a separate temp dir — nothing touches the repo
	sylkRoot := t.TempDir()
	sd := New(sylkRoot)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("GlobalMeta load: %v", err)
	}

	canonIdx := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := canonIdx.Init(); err != nil {
		t.Fatalf("CanonicalIndex init: %v", err)
	}

	sessStore := NewSessionStore(sd)
	ctx := context.Background()

	// ingestAndCommit: parse workspace with real tree-sitter pipeline,
	// create a session, ingest, and commit to global.
	ingestAndCommit := func(t *testing.T, scopeRoots []string) (*CommitResult, *ingestion.CodeGraph) {
		t.Helper()

		graph, err := ingestion.QuickIngest(ctx, workspace)
		if err != nil {
			t.Fatalf("QuickIngest: %v", err)
		}

		contentMap := make(map[string]string, len(graph.Files))
		for _, f := range graph.Files {
			data, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatalf("read %s for contentMap: %v", f.Path, err)
			}
			contentMap[f.Path] = string(data)
		}

		sessID, err := gm.AllocateSessionID()
		if err != nil {
			t.Fatalf("AllocateSessionID: %v", err)
		}

		// Allocate enough IDs for all nodes the pipeline will create
		// (files + symbols + chunks — chunks can outnumber symbols)
		nodeCount := uint32(len(graph.Files)+len(graph.Symbols)) * 5
		startID, err := gm.AllocateNodeIDs(nodeCount)
		if err != nil {
			t.Fatalf("AllocateNodeIDs: %v", err)
		}

		sess, err := sessStore.Create(sessID, &BaseSnapshot{
			GlobalVersion:     gm.GetHead(),
			CommittedSessions: []uint32{},
			SnapshotAt:        time.Now(),
			NextNodeID:        gm.GetCurrentNodeID(),
		})
		if err != nil {
			t.Fatalf("Create session: %v", err)
		}
		t.Cleanup(func() { sess.CloseBleve() })

		si := NewSessionIngestion(sess)
		*si.nextNodeID = startID

		if _, err := si.IngestCodeGraph(ctx, graph, contentMap); err != nil {
			t.Fatalf("IngestCodeGraph: %v", err)
		}

		result, err := CommitToGlobal(CommitConfig{
			Session:        sess,
			SylkDir:        sd,
			GlobalMeta:     gm,
			CanonicalIndex: canonIdx,
			ScopeRoots:     scopeRoots,
		})
		if err != nil {
			t.Fatalf("CommitToGlobal: %v", err)
		}

		return result, graph
	}

	// ═══════════════════════════════════════════════════════════════════
	// Stage 1: Initial ingest — parse 3 real Go files, zero orphans
	// ═══════════════════════════════════════════════════════════════════
	var tombstoneSymbols []string // names of symbols in tombstone.go
	var tombstonePath string      // absolute path as reported by ingestion
	var canonicalIndexPath string
	var commitPath string

	t.Run("Stage1_InitialIngest", func(t *testing.T) {
		r, graph := ingestAndCommit(t, nil)

		t.Logf("Parsed %d files, %d symbols from real Go source", len(graph.Files), len(graph.Symbols))

		// Build file path lookup and log all parsed symbols
		filePathByID := make(map[uint32]string)
		for _, f := range graph.Files {
			filePathByID[f.ID] = f.Path
			switch filepath.Base(f.Path) {
			case "tombstone.go":
				tombstonePath = f.Path
			case "canonical_index.go":
				canonicalIndexPath = f.Path
			case "commit.go":
				commitPath = f.Path
			}
		}
		for _, s := range graph.Symbols {
			path := filePathByID[s.FileID]
			t.Logf("  %s :: %s (%s) [lines %d-%d]", path, s.Name, s.Kind.String(), s.StartLine, s.EndLine)
			if strings.HasSuffix(path, "tombstone.go") {
				tombstoneSymbols = append(tombstoneSymbols, s.Name)
			}
		}

		if len(graph.Files) != 3 {
			t.Errorf("parsed %d files, want 3", len(graph.Files))
		}
		if len(graph.Symbols) == 0 {
			t.Fatal("tree-sitter parsed 0 symbols — grammars may not be available")
		}

		if r.OrphanedKeys != 0 {
			t.Errorf("OrphanedKeys = %d, want 0 (first commit)", r.OrphanedKeys)
		}
		if r.DeletedFiles != 0 {
			t.Errorf("DeletedFiles = %d, want 0 (first commit)", r.DeletedFiles)
		}

		// Every file should have a canonical key
		for _, f := range graph.Files {
			if !canonIdx.Has("file:" + f.Path) {
				t.Errorf("missing canonical key file:%s", f.Path)
			}
		}

		t.Logf("Commit: v%s, %d nodes merged, %d canonical keys total",
			r.GlobalVersion.String(), r.NodesMerged, canonIdx.Count())
	})

	// ═══════════════════════════════════════════════════════════════════
	// Stage 2: Remove MarkDead method — orphan detected
	// ═══════════════════════════════════════════════════════════════════
	t.Run("Stage2_RemoveFunction", func(t *testing.T) {
		// Precondition: MarkDead exists in the canonical index.
		// The exact key depends on tree-sitter classification (method vs function).
		markDeadKeys := canonIdx.LookupPrefix("symbol:" + tombstonePath + ":MarkDead:")
		if len(markDeadKeys) == 0 {
			t.Fatal("precondition: MarkDead should have a canonical key")
		}
		var markDeadKey string
		var markDeadNodeID uint32
		for k, id := range markDeadKeys {
			markDeadKey = k
			markDeadNodeID = id
		}
		t.Logf("MarkDead key before removal: %s → node %d", markDeadKey, markDeadNodeID)

		preKeyCount := canonIdx.Count()

		// Surgically remove MarkDead from the workspace copy
		src, err := os.ReadFile(filepath.Join(workspace, "tombstone.go"))
		if err != nil {
			t.Fatalf("read tombstone.go: %v", err)
		}
		modified := removeFuncFromSource(src, "func (tb *TombstoneBitmap) MarkDead(")
		if len(modified) >= len(src) {
			t.Fatal("removeFuncFromSource did not remove anything")
		}
		if err := os.WriteFile(filepath.Join(workspace, "tombstone.go"), modified, 0644); err != nil {
			t.Fatalf("write tombstone.go: %v", err)
		}

		r, graph := ingestAndCommit(t, nil)

		// Verify MarkDead is no longer in the parsed symbols
		for _, s := range graph.Symbols {
			if s.Name == "MarkDead" {
				t.Error("MarkDead should not appear in parsed symbols after removal")
			}
		}

		// Orphan detection should have caught it
		if r.OrphanedKeys < 1 {
			t.Errorf("OrphanedKeys = %d, want >= 1 (MarkDead removed)", r.OrphanedKeys)
		}

		// The key should be deleted from the canonical index
		if canonIdx.Has(markDeadKey) {
			t.Errorf("orphaned key %q should be deleted", markDeadKey)
		}
		t.Logf("Canonical keys: %d → %d (lost MarkDead + supersession churn)", preKeyCount, canonIdx.Count())

		// The node should be tombstoned
		versionPath := sd.GlobalVersionPath(r.GlobalVersion)
		tb, err := LoadTombstoneBitmap(versionPath)
		if err != nil {
			t.Fatalf("LoadTombstoneBitmap: %v", err)
		}
		if !tb.IsDead(markDeadNodeID) {
			t.Errorf("MarkDead node %d should be dead in tombstone bitmap", markDeadNodeID)
		}

		// Other tombstone.go symbols should survive (superseded → new IDs, but keys exist)
		for _, name := range tombstoneSymbols {
			if name == "MarkDead" {
				continue
			}
			found := canonIdx.LookupPrefix("symbol:" + tombstonePath + ":" + name + ":")
			if len(found) == 0 {
				t.Errorf("surviving symbol %s should still have a canonical key", name)
			}
		}

		t.Logf("Commit: v%s, %d orphaned, %d dead total",
			r.GlobalVersion.String(), r.OrphanedKeys, r.DeadNodeCount)
	})

	// ═══════════════════════════════════════════════════════════════════
	// Stage 3: Delete canonical_index.go — file deletion via ScopeRoots
	// ═══════════════════════════════════════════════════════════════════
	t.Run("Stage3_DeleteFile", func(t *testing.T) {
		ciFileKey := "file:" + canonicalIndexPath
		if !canonIdx.Has(ciFileKey) {
			t.Fatalf("precondition: %q should exist", ciFileKey)
		}
		ciSymbolKeys := canonIdx.LookupPrefix("symbol:" + canonicalIndexPath + ":")
		t.Logf("canonical_index.go: 1 file key + %d symbol keys before deletion", len(ciSymbolKeys))

		// Delete the file from the workspace
		if err := os.Remove(filepath.Join(workspace, "canonical_index.go")); err != nil {
			t.Fatalf("remove: %v", err)
		}

		// Commit with ScopeRoots covering the workspace directory
		r, _ := ingestAndCommit(t, []string{workspace})

		if r.DeletedFiles != 1 {
			t.Errorf("DeletedFiles = %d, want 1", r.DeletedFiles)
		}

		// All canonical_index.go keys should be gone
		if canonIdx.Has(ciFileKey) {
			t.Error("file:canonical_index.go should be deleted from canonical index")
		}
		remaining := canonIdx.LookupPrefix("symbol:" + canonicalIndexPath + ":")
		if len(remaining) != 0 {
			for k := range remaining {
				t.Errorf("  stale key: %s", k)
			}
			t.Errorf("%d canonical_index.go symbol keys remain, want 0", len(remaining))
		}

		// Other files should survive
		if !canonIdx.Has("file:" + tombstonePath) {
			t.Error("file:tombstone.go should survive")
		}
		if !canonIdx.Has("file:" + commitPath) {
			t.Error("file:commit.go should survive")
		}

		// Verify all CI nodes are tombstoned
		versionPath := sd.GlobalVersionPath(r.GlobalVersion)
		tb, err := LoadTombstoneBitmap(versionPath)
		if err != nil {
			t.Fatalf("LoadTombstoneBitmap: %v", err)
		}
		for key, nodeID := range ciSymbolKeys {
			if !tb.IsDead(nodeID) {
				t.Errorf("deleted symbol %s (node %d) should be dead", key, nodeID)
			}
		}

		t.Logf("Commit: v%s, %d orphaned keys, %d deleted files, %d dead total",
			r.GlobalVersion.String(), r.OrphanedKeys, r.DeletedFiles, r.DeadNodeCount)
	})

	// ═══════════════════════════════════════════════════════════════════
	// Stage 4: Verify cumulative state — consistency check
	// ═══════════════════════════════════════════════════════════════════
	t.Run("Stage4_CumulativeVerify", func(t *testing.T) {
		headVersion := gm.GetHead()
		nodeStore, nsErr := NewGlobalVersionNodeStore(sd, headVersion)
		if nsErr != nil {
			t.Fatalf("NewGlobalVersionNodeStore: %v", nsErr)
		}
		defer nodeStore.Close()
		liveNodes, err := nodeStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}

		liveIDs := make(map[uint32]bool, len(liveNodes))
		for _, n := range liveNodes {
			liveIDs[n.ID] = true
		}

		// Every canonical key must point to a live node
		stale := 0
		for _, key := range canonIdx.Keys() {
			id, _ := canonIdx.Lookup(key)
			if !liveIDs[id] {
				t.Errorf("canonical key %q → node %d which is NOT live", key, id)
				stale++
			}
		}

		// Deleted entities must be absent
		if canonIdx.Has("file:" + canonicalIndexPath) {
			t.Error("deleted file key persists")
		}
		markDeadKeys := canonIdx.LookupPrefix("symbol:" + tombstonePath + ":MarkDead:")
		if len(markDeadKeys) > 0 {
			t.Error("orphaned MarkDead key persists")
		}

		// Verify edges are consistent (no dead endpoints)
		edgeStore, esErr := NewGlobalVersionEdgeStore(sd, headVersion)
		if esErr != nil {
			t.Fatalf("NewGlobalVersionEdgeStore: %v", esErr)
		}
		defer edgeStore.Close()
		liveEdges, err := edgeStore.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll edges: %v", err)
		}
		for _, e := range liveEdges {
			if !liveIDs[e.SourceID] {
				t.Errorf("edge source %d is not a live node", e.SourceID)
			}
			if !liveIDs[e.TargetID] {
				t.Errorf("edge target %d is not a live node", e.TargetID)
			}
		}

		// Summary
		versionPath := sd.GlobalVersionPath(headVersion)
		tb, err := LoadTombstoneBitmap(versionPath)
		if err != nil {
			t.Fatalf("LoadTombstoneBitmap: %v", err)
		}

		t.Logf("Final state: %d canonical keys, %d live nodes, %d live edges, %d dead (tombstoned), %d stale keys",
			canonIdx.Count(), len(liveNodes), len(liveEdges), tb.Count(), stale)

		if stale > 0 {
			t.Errorf("%d stale canonical keys point to dead nodes", stale)
		}
	})
}

// removeFuncFromSource removes a function/method from Go source code by
// finding the line containing the given signature, removing any preceding
// doc comment, and tracking brace nesting to find the closing brace.
func removeFuncFromSource(src []byte, signature string) []byte {
	lines := strings.Split(string(src), "\n")

	sigIdx := -1
	for i, line := range lines {
		if strings.Contains(line, signature) {
			sigIdx = i
			break
		}
	}
	if sigIdx < 0 {
		return src
	}

	// Walk backwards to include the doc comment
	startIdx := sigIdx
	for startIdx > 0 && strings.HasPrefix(strings.TrimSpace(lines[startIdx-1]), "//") {
		startIdx--
	}

	// Walk forward tracking brace nesting to find the closing brace
	endIdx := sigIdx
	braces := 0
	entered := false
	for i := sigIdx; i < len(lines); i++ {
		braces += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
		if braces > 0 {
			entered = true
		}
		if entered && braces <= 0 {
			endIdx = i
			break
		}
	}

	var result []string
	result = append(result, lines[:startIdx]...)
	result = append(result, lines[endIdx+1:]...)
	return []byte(strings.Join(result, "\n"))
}
