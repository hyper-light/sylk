package sylkdir

import (
	"os"
	"path/filepath"
	"testing"
)

// setupChunkRefTestSession creates a minimal Session for ChunkRefStore testing.
func setupChunkRefTestSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()

	sessionPath := filepath.Join(dir, "session")
	version := SemanticVersion{Major: 1, Minor: 0, Patch: 0}

	// Create directory structure.
	dirs := []string{
		filepath.Join(sessionPath, "data", "chunks"),
		filepath.Join(sessionPath, "versions", version.DirName(), "chunks"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	chunkDataFile, err := OpenSharedDataFile(filepath.Join(sessionPath, "data", "chunks", "data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { chunkDataFile.Close() })

	return &Session{
		path:          sessionPath,
		Manifest:      &VersionManifest{Head: version},
		ChunkDataFile: chunkDataFile,
	}
}

func TestChunkRefStoreWriteRead(t *testing.T) {
	sess := setupChunkRefTestSession(t)
	store := NewChunkRefStore(sess)

	ref := &ChunkRef{
		NodeID: 10,
		Span: ChunkSpan{
			DocRef:    42,
			ChunkSeq:  0,
			ByteStart: 0,
			ByteEnd:   1024,
			LineStart: 1,
			LineEnd:   20,
			Flags:     PackFlags(OverlapNone, ContentClassSourceCode, BoundaryStructural),
		},
	}

	if err := store.Write(ref); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := store.ReadFromVersion(sess.Manifest.Head, 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if got.NodeID != ref.NodeID {
		t.Errorf("NodeID: got %d, want %d", got.NodeID, ref.NodeID)
	}
	if got.Span != ref.Span {
		t.Errorf("Span mismatch:\n  got:  %+v\n  want: %+v", got.Span, ref.Span)
	}
}

func TestChunkRefStoreWriteBatch(t *testing.T) {
	sess := setupChunkRefTestSession(t)
	store := NewChunkRefStore(sess)

	refs := []*ChunkRef{
		{
			NodeID: 10,
			Span: ChunkSpan{
				DocRef: 42, ChunkSeq: 0,
				ByteStart: 0, ByteEnd: 500,
				LineStart: 1, LineEnd: 10,
				Flags: PackFlags(OverlapSuffix, ContentClassMarkdown, BoundaryHeading),
			},
		},
		{
			NodeID: 11,
			Span: ChunkSpan{
				DocRef: 42, ChunkSeq: 1,
				ByteStart: 450, ByteEnd: 1000,
				LineStart: 9, LineEnd: 20,
				Flags: PackFlags(OverlapBoth, ContentClassMarkdown, BoundaryHeading),
			},
		},
		{
			NodeID: 12,
			Span: ChunkSpan{
				DocRef: 42, ChunkSeq: 2,
				ByteStart: 950, ByteEnd: 1200,
				LineStart: 19, LineEnd: 30,
				Flags: PackFlags(OverlapPrefix, ContentClassMarkdown, BoundarySentence),
			},
		},
	}

	if err := store.WriteBatch(refs); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	if got := store.Count(); got != 3 {
		t.Fatalf("count: got %d, want 3", got)
	}

	// Read each back.
	for _, ref := range refs {
		got, err := store.ReadFromVersion(sess.Manifest.Head, ref.NodeID)
		if err != nil {
			t.Fatalf("read %d: %v", ref.NodeID, err)
		}
		if got.Span != ref.Span {
			t.Errorf("node %d span mismatch", ref.NodeID)
		}
	}
}

func TestChunkRefStoreReadAllFromVersion(t *testing.T) {
	sess := setupChunkRefTestSession(t)
	store := NewChunkRefStore(sess)

	refs := []*ChunkRef{
		{NodeID: 1, Span: ChunkSpan{DocRef: 10, ChunkSeq: 0, ByteEnd: 100, LineStart: 1, LineEnd: 5}},
		{NodeID: 2, Span: ChunkSpan{DocRef: 10, ChunkSeq: 1, ByteStart: 100, ByteEnd: 200, LineStart: 5, LineEnd: 10}},
	}

	if err := store.WriteBatch(refs); err != nil {
		t.Fatal(err)
	}

	all, err := store.ReadAllFromVersion(sess.Manifest.Head)
	if err != nil {
		t.Fatal(err)
	}

	if len(all) != 2 {
		t.Fatalf("read all: got %d, want 2", len(all))
	}
}

func TestChunkRefStoreNotFound(t *testing.T) {
	sess := setupChunkRefTestSession(t)
	store := NewChunkRefStore(sess)

	_, err := store.ReadFromVersion(sess.Manifest.Head, 999)
	if err != ErrChunkRefNotFound {
		t.Fatalf("expected ErrChunkRefNotFound, got %v", err)
	}
}

func TestChunkRefStoreSaveAndLoadIndex(t *testing.T) {
	sess := setupChunkRefTestSession(t)
	store := NewChunkRefStore(sess)

	ref := &ChunkRef{
		NodeID: 5,
		Span: ChunkSpan{
			DocRef: 99, ChunkSeq: 0,
			ByteEnd: 2048, LineStart: 1, LineEnd: 50,
		},
	}

	if err := store.Write(ref); err != nil {
		t.Fatal(err)
	}

	// Save index to disk.
	if err := store.SaveIndexes(); err != nil {
		t.Fatal(err)
	}

	// Create a new store and verify it can load.
	store2 := &ChunkRefStore{session: sess}
	got, err := store2.ReadFromVersion(sess.Manifest.Head, 5)
	if err != nil {
		t.Fatalf("read after reload: %v", err)
	}

	if got.Span.DocRef != 99 {
		t.Errorf("DocRef: got %d, want 99", got.Span.DocRef)
	}
}

func TestChunkRefStoreEmptyBatch(t *testing.T) {
	sess := setupChunkRefTestSession(t)
	store := NewChunkRefStore(sess)

	if err := store.WriteBatch(nil); err != nil {
		t.Fatal(err)
	}

	if got := store.Count(); got != 0 {
		t.Fatalf("count after empty batch: got %d, want 0", got)
	}
}
