package sylkdir

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/search"
)

func TestChunkLocatorBasic(t *testing.T) {
	sess := setupJointTestSession(t)
	var nextID uint32 = 1
	mock := &mockBatchEmbedder{dim: 4, maxBytes: 50}
	jdi := NewJointDocIngestion(sess, &nextID, mock)

	content := "# Section 1\nParagraph about topic one.\n\n# Section 2\nParagraph about topic two.\n\n# Section 3\nParagraph about topic three.\n"
	req := &JointDocRequest{
		DocID:   "locator-test",
		Path:    "/test/locator.md",
		Content: []byte(content),
		DocType: search.DocTypeMarkdown,
		Domain:  DomainDoc,
	}

	result, err := jdi.Insert(context.Background(), req)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if result.ChunkCount < 2 {
		t.Fatalf("expected >= 2 chunks, got %d", result.ChunkCount)
	}

	locator := NewChunkLocator(sess)
	for _, chunkID := range result.ChunkNodeIDs {
		loc, err := locator.Locate(chunkID)
		if err != nil {
			t.Errorf("locate chunk %d: %v", chunkID, err)
			continue
		}
		if loc.ParentPath != "/test/locator.md" {
			t.Errorf("chunk %d: parent path = %q, want /test/locator.md", chunkID, loc.ParentPath)
		}
		if loc.ChunkContent == "" {
			t.Errorf("chunk %d: empty chunk content", chunkID)
		}
		// Verify content matches the parent doc at the byte range.
		expected := content[loc.ByteStart:loc.ByteEnd]
		if loc.ChunkContent != expected {
			t.Errorf("chunk %d: content mismatch:\n  got  %q\n  want %q", chunkID, loc.ChunkContent, expected)
		}
	}
}

func TestChunkLocatorBatch(t *testing.T) {
	sess := setupJointTestSession(t)
	var nextID uint32 = 1
	mock := &mockBatchEmbedder{dim: 4, maxBytes: 20}
	jdi := NewJointDocIngestion(sess, &nextID, mock)

	content := "Line 1\n\nLine 2\n\nLine 3\n\nLine 4\n\nLine 5\n\nLine 6\n\nLine 7\n\nLine 8\n\nLine 9\n\nLine 10\n"
	req := &JointDocRequest{
		DocID:   "batch-locator",
		Path:    "/test/batch.txt",
		Content: []byte(content),
		DocType: search.DocTypeNote,
		Domain:  DomainDoc,
	}

	result, err := jdi.Insert(context.Background(), req)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if result.ChunkCount < 2 {
		t.Fatalf("expected >= 2 chunks, got %d", result.ChunkCount)
	}

	locator := NewChunkLocator(sess)
	locs, err := locator.LocateBatch(result.ChunkNodeIDs)
	if err != nil {
		t.Fatalf("LocateBatch: %v", err)
	}
	if len(locs) != len(result.ChunkNodeIDs) {
		t.Fatalf("LocateBatch: got %d locations, want %d", len(locs), len(result.ChunkNodeIDs))
	}
	for i, loc := range locs {
		if loc.ChunkNodeID != result.ChunkNodeIDs[i] {
			t.Errorf("loc[%d]: ChunkNodeID = %d, want %d", i, loc.ChunkNodeID, result.ChunkNodeIDs[i])
		}
		if loc.ChunkContent == "" {
			t.Errorf("loc[%d]: empty content", i)
		}
	}
}

func TestChunkLocatorNotFound(t *testing.T) {
	sess := setupJointTestSession(t)
	// Ensure stores are registered.
	_ = NewJointDocIngestion(sess, new(uint32), nil)

	locator := NewChunkLocator(sess)
	_, err := locator.Locate(9999)
	if err == nil {
		t.Error("expected error for non-existent chunk ID")
	}
}
