package sylkdir

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/search"
)

// setupJointTestSession creates a minimal Session for JointDocIngestion testing.
func setupJointTestSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session")
	version := SemanticVersion{Major: 1, Minor: 0, Patch: 0}

	dirs := []string{
		filepath.Join(sessionPath, "data", "nodes"),
		filepath.Join(sessionPath, "data", "docs"),
		filepath.Join(sessionPath, "data", "vectors"),
		filepath.Join(sessionPath, "data", "chunks"),
		filepath.Join(sessionPath, "versions", version.DirName(), "nodes"),
		filepath.Join(sessionPath, "versions", version.DirName(), "docs"),
		filepath.Join(sessionPath, "versions", version.DirName(), "vectors"),
		filepath.Join(sessionPath, "versions", version.DirName(), "chunks"),
		filepath.Join(sessionPath, "versions", version.DirName(), "edges"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	nodeDataFile := openTestDataFile(t, filepath.Join(sessionPath, "data", "nodes", "data.bin"))
	docDataFile := openTestDataFile(t, filepath.Join(sessionPath, "data", "docs", "data.bin"))
	vectorDataFile := openTestDataFile(t, filepath.Join(sessionPath, "data", "vectors", "data.bin"))
	chunkDataFile := openTestDataFile(t, filepath.Join(sessionPath, "data", "chunks", "data.bin"))

	return &Session{
		path:           sessionPath,
		Meta:           &SessionMeta{ID: 1},
		Manifest:       &VersionManifest{Head: version},
		NodeDataFile:   nodeDataFile,
		DocDataFile:    docDataFile,
		VectorDataFile: vectorDataFile,
		ChunkDataFile:  chunkDataFile,
		DocIDMap:       NewDocIDMap(filepath.Join(sessionPath, "data", "doc_id_map.bin")),
	}
}

func openTestDataFile(t *testing.T, path string) *SharedDataFile {
	t.Helper()
	f, err := OpenSharedDataFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestJointDocInsertBasic(t *testing.T) {
	sess := setupJointTestSession(t)
	var nextID uint32 = 1
	jdi := NewJointDocIngestion(sess, &nextID, nil, 512)

	req := &JointDocRequest{
		DocID:   "test-doc",
		Path:    "/test/example.md",
		Content: []byte("# Hello\nWorld\n"),
		DocType: search.DocTypeMarkdown,
		Domain:  DomainDoc,
	}

	result, err := jdi.Insert(context.Background(), req)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if result.DocNodeID == 0 {
		t.Error("DocNodeID should be non-zero")
	}
	if result.DocsCreated < 1 {
		t.Errorf("DocsCreated: got %d, want >= 1", result.DocsCreated)
	}
}

func TestJointDocInsertCreatesChunks(t *testing.T) {
	sess := setupJointTestSession(t)
	var nextID uint32 = 1

	// Use very small config to force multiple chunks.
	jdi := NewJointDocIngestion(sess, &nextID, nil, 20)

	// Content large enough to be split into multiple chunks.
	content := "# Section 1\nParagraph about topic one.\n\n# Section 2\nParagraph about topic two.\n\n# Section 3\nParagraph about topic three.\n"
	req := &JointDocRequest{
		DocID:   "multi-chunk-doc",
		Path:    "/test/sections.md",
		Content: []byte(content),
		DocType: search.DocTypeMarkdown,
		Domain:  DomainDoc,
	}

	result, err := jdi.Insert(context.Background(), req)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if result.ChunkCount < 2 {
		t.Errorf("ChunkCount: got %d, want >= 2", result.ChunkCount)
	}
	if len(result.ChunkNodeIDs) != result.ChunkCount {
		t.Errorf("ChunkNodeIDs: got %d, want %d", len(result.ChunkNodeIDs), result.ChunkCount)
	}
	// Parent + chunks = ChunkCount + 1 docs created.
	if result.DocsCreated != result.ChunkCount+1 {
		t.Errorf("DocsCreated: got %d, want %d", result.DocsCreated, result.ChunkCount+1)
	}
	// ContainsChunk edges + ChunkSequence edges.
	expectedEdges := result.ChunkCount + (result.ChunkCount - 1)
	if result.EdgesCreated != expectedEdges {
		t.Errorf("EdgesCreated: got %d, want %d", result.EdgesCreated, expectedEdges)
	}
}

func TestJointDocInsertStoresChunkRefs(t *testing.T) {
	sess := setupJointTestSession(t)
	var nextID uint32 = 1
	jdi := NewJointDocIngestion(sess, &nextID, nil, 20)

	content := "Line 1\n\nLine 2\n\nLine 3\n\nLine 4\n\nLine 5\n\nLine 6\n\nLine 7\n\nLine 8\n\nLine 9\n\nLine 10\n"
	req := &JointDocRequest{
		DocID:   "chunk-ref-doc",
		Path:    "/test/lines.txt",
		Content: []byte(content),
		DocType: search.DocTypeNote,
		Domain:  DomainDoc,
	}

	result, err := jdi.Insert(context.Background(), req)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Verify chunk refs can be read back.
	for _, chunkID := range result.ChunkNodeIDs {
		ref, err := jdi.chunkRefStore.ReadFromVersion(sess.Manifest.Head, chunkID)
		if err != nil {
			t.Errorf("read chunk ref %d: %v", chunkID, err)
			continue
		}
		if ref.Span.ByteEnd <= ref.Span.ByteStart {
			t.Errorf("chunk %d: invalid span (start=%d, end=%d)", chunkID, ref.Span.ByteStart, ref.Span.ByteEnd)
		}
	}
}

func TestJointDocDelete(t *testing.T) {
	sess := setupJointTestSession(t)
	var nextID uint32 = 1
	jdi := NewJointDocIngestion(sess, &nextID, nil, 512)

	req := &JointDocRequest{
		DocID:   "del-doc",
		Path:    "/test/delete.md",
		Content: []byte("# To be deleted\nContent here.\n"),
		DocType: search.DocTypeMarkdown,
		Domain:  DomainDoc,
	}

	result, err := jdi.Insert(context.Background(), req)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	canonicalKey := docCanonicalKey(req.DocType, req.Path)
	if err := jdi.Delete(context.Background(), canonicalKey); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Verify the node is tombstoned.
	node, err := jdi.nodeStore.ReadFromVersion(sess.Manifest.Head, result.DocNodeID)
	if err != nil {
		t.Fatalf("read after delete: %v", err)
	}
	if node.SupersededBy != ^uint32(0) {
		t.Errorf("expected tombstone marker, got %d", node.SupersededBy)
	}
}

func TestJointDocUpdate(t *testing.T) {
	sess := setupJointTestSession(t)
	var nextID uint32 = 1
	jdi := NewJointDocIngestion(sess, &nextID, nil, 512)

	req := &JointDocRequest{
		DocID:   "update-doc",
		Path:    "/test/update.md",
		Content: []byte("# Original\nOriginal content.\n"),
		DocType: search.DocTypeMarkdown,
		Domain:  DomainDoc,
	}

	oldResult, err := jdi.Insert(context.Background(), req)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	req2 := &JointDocRequest{
		DocID:   "update-doc",
		Path:    "/test/update.md",
		Content: []byte("# Updated\nNew content here.\n"),
		DocType: search.DocTypeMarkdown,
		Domain:  DomainDoc,
	}

	canonicalKey := docCanonicalKey(req.DocType, req.Path)
	newResult, err := jdi.Update(context.Background(), canonicalKey, req2)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if newResult.DocNodeID == oldResult.DocNodeID {
		t.Error("update should create new node ID")
	}

	// Old node should be tombstoned.
	oldNode, err := jdi.nodeStore.ReadFromVersion(sess.Manifest.Head, oldResult.DocNodeID)
	if err != nil {
		t.Fatalf("read old: %v", err)
	}
	if oldNode.SupersededBy != ^uint32(0) {
		t.Errorf("old node not tombstoned: SupersededBy=%d", oldNode.SupersededBy)
	}
}
