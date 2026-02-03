package sylkdir

import (
	"fmt"
)

// ChunkLocation describes the exact position and content of a chunk
// within its parent document. Returned by ChunkLocator.Locate.
type ChunkLocation struct {
	ChunkNodeID  uint32 // The chunk node that was queried
	ParentDocRef uint32 // uint32 ref of the parent document
	ParentPath   string // File path or URL of the parent document
	ChunkSeq     uint16 // 0-indexed sequence position within the document
	ByteStart    uint32 // Byte offset of chunk start in parent content
	ByteEnd      uint32 // Byte offset of chunk end (exclusive)
	LineStart    uint32 // 1-indexed line number of chunk start
	LineEnd      uint32 // 1-indexed line number of chunk end (inclusive)
	ChunkContent string // The parent document content at [ByteStart:ByteEnd]
}

// ChunkLocator resolves chunk node IDs to their parent document location.
// It reads from the session's HEAD version stores. All reads are lock-free.
type ChunkLocator struct {
	chunkRefStore *ChunkRefStore
	docStore      *VersionDocStore
	docIDMap      *DocIDMap
	version       SemanticVersion
}

// NewChunkLocator creates a locator bound to the session's HEAD version.
// It reuses the session's registered stores to avoid double-registration.
func NewChunkLocator(sess *Session) *ChunkLocator {
	return &ChunkLocator{
		chunkRefStore: sess.chunkRefStore,
		docStore:      sess.docStore,
		docIDMap:      sess.DocIDMap,
		version:       sess.Manifest.Head,
	}
}

// Locate resolves a chunk node ID to its full location within the parent
// document, including the content at the chunk's byte range.
func (cl *ChunkLocator) Locate(chunkNodeID uint32) (*ChunkLocation, error) {
	ref, err := cl.chunkRefStore.ReadFromVersion(cl.version, chunkNodeID)
	if err != nil {
		return nil, fmt.Errorf("read chunk ref %d: %w", chunkNodeID, err)
	}
	return cl.resolveRef(chunkNodeID, ref)
}

// LocateBatch resolves multiple chunk node IDs, deduplicating parent doc
// reads by DocRef. Returns locations in the same order as the input IDs.
func (cl *ChunkLocator) LocateBatch(chunkNodeIDs []uint32) ([]*ChunkLocation, error) {
	refs, err := cl.loadRefs(chunkNodeIDs)
	if err != nil {
		return nil, err
	}

	parentDocs, err := cl.loadParentDocs(refs)
	if err != nil {
		return nil, err
	}

	return cl.buildLocations(chunkNodeIDs, refs, parentDocs), nil
}

// resolveRef loads the parent doc and builds a ChunkLocation for a single ref.
func (cl *ChunkLocator) resolveRef(chunkNodeID uint32, ref *ChunkRef) (*ChunkLocation, error) {
	parentDoc, err := cl.docStore.ReadByDocRef(cl.version, ref.Span.DocRef)
	if err != nil {
		return nil, fmt.Errorf("read parent doc ref %d: %w", ref.Span.DocRef, err)
	}
	return buildChunkLocation(chunkNodeID, ref, parentDoc), nil
}

// loadRefs reads ChunkRefs for all node IDs.
func (cl *ChunkLocator) loadRefs(ids []uint32) ([]*ChunkRef, error) {
	refs := make([]*ChunkRef, len(ids))
	for i, id := range ids {
		ref, err := cl.chunkRefStore.ReadFromVersion(cl.version, id)
		if err != nil {
			return nil, fmt.Errorf("read chunk ref %d: %w", id, err)
		}
		refs[i] = ref
	}
	return refs, nil
}

// loadParentDocs reads unique parent documents, deduplicating by DocRef.
func (cl *ChunkLocator) loadParentDocs(refs []*ChunkRef) (map[uint32]*VersionDocument, error) {
	docs := make(map[uint32]*VersionDocument, len(refs))
	for _, ref := range refs {
		if _, ok := docs[ref.Span.DocRef]; ok {
			continue
		}
		doc, err := cl.docStore.ReadByDocRef(cl.version, ref.Span.DocRef)
		if err != nil {
			return nil, fmt.Errorf("read parent doc ref %d: %w", ref.Span.DocRef, err)
		}
		docs[ref.Span.DocRef] = doc
	}
	return docs, nil
}

// buildLocations assembles ChunkLocation records from refs and parent docs.
func (cl *ChunkLocator) buildLocations(ids []uint32, refs []*ChunkRef, parentDocs map[uint32]*VersionDocument) []*ChunkLocation {
	locs := make([]*ChunkLocation, len(ids))
	for i, ref := range refs {
		locs[i] = buildChunkLocation(ids[i], ref, parentDocs[ref.Span.DocRef])
	}
	return locs
}

// buildChunkLocation constructs a ChunkLocation from a ref and parent doc.
func buildChunkLocation(chunkNodeID uint32, ref *ChunkRef, parentDoc *VersionDocument) *ChunkLocation {
	return &ChunkLocation{
		ChunkNodeID:  chunkNodeID,
		ParentDocRef: ref.Span.DocRef,
		ParentPath:   parentDoc.Path,
		ChunkSeq:     ref.Span.ChunkSeq,
		ByteStart:    ref.Span.ByteStart,
		ByteEnd:      ref.Span.ByteEnd,
		LineStart:    ref.Span.LineStart,
		LineEnd:      ref.Span.LineEnd,
		ChunkContent: safeSlice(parentDoc.Content, ref.Span.ByteStart, ref.Span.ByteEnd),
	}
}

// safeSlice extracts a substring by byte offsets, clamping to content bounds.
func safeSlice(content string, start, end uint32) string {
	contentLen := uint32(len(content))
	if start >= contentLen {
		return ""
	}
	if end > contentLen {
		end = contentLen
	}
	return content[start:end]
}
