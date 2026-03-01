package sylkdir

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"golang.org/x/sync/errgroup"
)

// JointDocIngestion provides atomic Insert/Update/Delete for documents
// that jointly operate on document DB, knowledge graph, vectors, and Bleve.
// Each document is chunked using content-type-aware strategies.
type JointDocIngestion struct {
	session       *Session
	nodeStore     *VersionNodeStore
	edgeStore     *VersionEdgeStore
	docStore      *VersionDocStore
	vectorStore   *VersionVectorStore
	chunkRefStore *ChunkRefStore
	embedder   embedder.Embedder
	chunker    *UniversalChunker
	nextNodeID *uint32 // shared counter (may be shared with SessionIngestion)
}

// JointDocRequest describes a document to ingest jointly.
type JointDocRequest struct {
	DocID    string              // Unique document identifier
	Path     string              // File path or URL
	Content  []byte              // Raw content bytes
	DocType  search.DocumentType // For chunker selection
	Language string              // Programming language (if applicable)
	Domain   Domain              // Knowledge graph domain
	Symbols  []search.SymbolInfo // Optional structural boundaries for source code
	Metadata map[string]string   // Additional metadata
}

// JointDocResult summarizes what was created or modified.
type JointDocResult struct {
	DocNodeID      uint32
	ChunkNodeIDs   []uint32
	ChunkCount     int
	EdgesCreated   int
	VectorsCreated int
	DocsCreated    int
}

// NewJointDocIngestion creates a joint ingestion handler for the session.
func NewJointDocIngestion(sess *Session, nextNodeID *uint32, e embedder.Embedder) *JointDocIngestion {
	similarity := embedder.NewEnhancedHybridEmbedder()
	var ceiling uint32
	if e != nil {
		ceiling = uint32(e.MaxInputBytes())
	} else {
		ceiling = uint32(similarity.MaxInputBytes())
	}
	return &JointDocIngestion{
		session:       sess,
		nodeStore:     NewVersionNodeStore(sess),
		edgeStore:     NewVersionEdgeStore(sess),
		docStore:      NewVersionDocStore(sess),
		vectorStore:   NewVersionVectorStore(sess),
		chunkRefStore: NewChunkRefStore(sess),
		embedder:      e,
		chunker:       NewUniversalChunker(similarity, ceiling),
		nextNodeID:    nextNodeID,
	}
}

// allocID atomically allocates a node ID.
func (j *JointDocIngestion) allocID() uint32 {
	id := *j.nextNodeID
	*j.nextNodeID++
	return id
}

// Insert creates a document with its chunks, nodes, edges, vectors, and
// Bleve index entries. Store writes, batch embedding, and Bleve indexing
// run concurrently via errgroup for throughput.
func (j *JointDocIngestion) Insert(ctx context.Context, req *JointDocRequest) (*JointDocResult, error) {
	cc := ContentClassFromDocType(req.DocType)
	boundaries := j.chunkBoundaries(ctx, req)
	result := &JointDocResult{ChunkCount: len(boundaries)}

	parentNode, parentDocRef := j.createParentNode(req, cc)
	result.DocNodeID = parentNode.ID

	parentDoc := j.createParentDoc(req, parentNode.ID)
	chunkNodes, chunkDocs, chunkRefs := j.createChunks(req, boundaries, parentDocRef, cc)
	result.ChunkNodeIDs = extractNodeIDs(chunkNodes)

	edges := j.createChunkEdges(parentNode.ID, chunkNodes)
	result.EdgesCreated = len(edges)

	allDocs := make([]*VersionDocument, 0, 1+len(chunkDocs))
	allDocs = append(allDocs, parentDoc)
	allDocs = append(allDocs, chunkDocs...)
	result.DocsCreated = len(allDocs)

	chunkTexts := extractChunkTexts(chunkDocs)

	g, gctx := errgroup.WithContext(ctx)

	// Store writes: nodes, docs, chunk refs, edges.
	g.Go(func() error {
		return j.writeAll(gctx, parentNode, parentDoc, chunkNodes, chunkDocs, chunkRefs, edges)
	})

	// Batch embed all chunk texts concurrently with store writes.
	var embeddings [][]float32
	g.Go(func() error {
		var err error
		embeddings, err = j.embedBatch(gctx, chunkTexts)
		return err
	})

	// Bleve index concurrently with store writes and embedding.
	g.Go(func() error {
		return j.indexBleve(gctx, allDocs)
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Write vectors after embeddings are ready.
	vectors, err := j.writeVectors(ctx, chunkNodes, chunkDocs, embeddings)
	if err == nil {
		result.VectorsCreated = len(vectors)
	}

	j.trackAndCheckpoint(result)
	return result, nil
}

// Update supersedes an old document and inserts the new version.
func (j *JointDocIngestion) Update(ctx context.Context, canonicalKey string, req *JointDocRequest) (*JointDocResult, error) {
	if err := j.tombstoneByKey(ctx, canonicalKey); err != nil {
		return nil, fmt.Errorf("tombstone old: %w", err)
	}
	return j.Insert(ctx, req)
}

// Delete tombstones a document and all its chunks by canonical key.
func (j *JointDocIngestion) Delete(ctx context.Context, canonicalKey string) error {
	return j.tombstoneByKey(ctx, canonicalKey)
}

// ---------------------------------------------------------------------------
// Chunking
// ---------------------------------------------------------------------------

func (j *JointDocIngestion) chunkBoundaries(ctx context.Context, req *JointDocRequest) []ChunkBoundary {
	if len(req.Symbols) > 0 {
		bounds := make([]SymbolBound, len(req.Symbols))
		for i, sym := range req.Symbols {
			bounds[i] = SymbolBound{
				StartLine: uint32(sym.Line),
				EndLine:   0,
				Strength:  SearchKindToStrength(string(sym.Kind)),
			}
		}
		return j.chunker.ChunkWithSymbols(ctx, req.Content, bounds).Boundaries
	}
	return j.chunker.Chunk(ctx, req.Content).Boundaries
}

// ---------------------------------------------------------------------------
// Node + document construction
// ---------------------------------------------------------------------------

func (j *JointDocIngestion) createParentNode(req *JointDocRequest, cc ContentClass) (*Node, uint32) {
	id := j.allocID()
	var docRef uint32
	if j.session.DocIDMap != nil {
		docRef = j.session.DocIDMap.GetOrAssign(fmt.Sprintf("doc_%d", id))
	}
	node := &Node{
		ID:           id,
		CanonicalKey: docCanonicalKey(req.DocType, req.Path),
		Domain:       uint8(req.Domain),
		NodeType:     uint8(NodeTypeDocument),
		Name:         req.Path,
		Path:         req.Path,
		CreatedAt:    uint64(time.Now().UnixNano()),
		SessionID:    j.session.Meta.ID,
		DocRef:       docRef,
	}
	return node, docRef
}

func (j *JointDocIngestion) createParentDoc(req *JointDocRequest, nodeID uint32) *VersionDocument {
	return &VersionDocument{
		ID:        fmt.Sprintf("doc_%d", nodeID),
		Path:      req.Path,
		Type:      string(req.DocType),
		Content:   string(req.Content),
		Language:  req.Language,
		IndexedAt: time.Now().UnixNano(),
	}
}

func (j *JointDocIngestion) createChunks(req *JointDocRequest, boundaries []ChunkBoundary, parentDocRef uint32, cc ContentClass) ([]*Node, []*VersionDocument, []*ChunkRef) {
	nodes := make([]*Node, len(boundaries))
	docs := make([]*VersionDocument, len(boundaries))
	refs := make([]*ChunkRef, len(boundaries))
	now := uint64(time.Now().UnixNano())

	for i, b := range boundaries {
		id := j.allocID()
		nodes[i] = j.chunkNode(id, req, i, now, b)
		docs[i] = j.chunkDoc(id, req, b)
		refs[i] = j.chunkRef(id, b, parentDocRef, uint16(i), cc)
	}
	return nodes, docs, refs
}

func (j *JointDocIngestion) chunkNode(id uint32, req *JointDocRequest, seq int, now uint64, b ChunkBoundary) *Node {
	var docRef uint32
	if j.session.DocIDMap != nil {
		docRef = j.session.DocIDMap.GetOrAssign(fmt.Sprintf("doc_%d_c%03d", id, seq))
	}
	return &Node{
		ID:           id,
		CanonicalKey: fmt.Sprintf("chunk:%s:%04d", req.DocID, seq),
		Domain:       uint8(req.Domain),
		NodeType:     uint8(NodeTypeChunk),
		Name:         fmt.Sprintf("%s#%d", req.Path, seq),
		Path:         req.Path,
		CreatedAt:    now,
		SessionID:    j.session.Meta.ID,
		DocRef:       docRef,
		SectionStart: b.LineStart,
		SectionEnd:   b.LineEnd,
	}
}

func (j *JointDocIngestion) chunkDoc(nodeID uint32, req *JointDocRequest, b ChunkBoundary) *VersionDocument {
	return &VersionDocument{
		ID:        fmt.Sprintf("doc_%d_c%03d", nodeID, 0),
		Path:      req.Path,
		Type:      string(req.DocType),
		Content:   string(req.Content[b.ByteStart:b.ByteEnd]),
		Language:  req.Language,
		IndexedAt: time.Now().UnixNano(),
	}
}

func (j *JointDocIngestion) chunkRef(nodeID uint32, b ChunkBoundary, parentDocRef uint32, seq uint16, cc ContentClass) *ChunkRef {
	overlap := overlapKindFromSeq(seq, seq) // placeholder; computed from neighbors
	return &ChunkRef{
		NodeID: nodeID,
		Span: ChunkSpan{
			DocRef:    parentDocRef,
			ChunkSeq:  seq,
			ByteStart: b.ByteStart,
			ByteEnd:   b.ByteEnd,
			LineStart: b.LineStart,
			LineEnd:   b.LineEnd,
			Flags:     PackFlags(overlap, cc, b.Kind),
		},
	}
}

func overlapKindFromSeq(_, _ uint16) OverlapKind {
	return OverlapNone // Overlap computed post-hoc when needed
}

// ---------------------------------------------------------------------------
// Edge construction
// ---------------------------------------------------------------------------

func (j *JointDocIngestion) createChunkEdges(parentID uint32, chunks []*Node) []*Edge {
	edges := make([]*Edge, 0, len(chunks)*2)
	now := uint64(time.Now().UnixNano())

	for i, chunk := range chunks {
		edges = append(edges, j.containsEdge(parentID, chunk.ID, now))
		if i > 0 {
			edges = append(edges, j.sequenceEdge(chunks[i-1].ID, chunk.ID, now))
		}
	}
	return edges
}

func (j *JointDocIngestion) containsEdge(parentID, chunkID uint32, now uint64) *Edge {
	return &Edge{
		SourceID:  parentID,
		TargetID:  chunkID,
		Type:      uint8(EdgeTypeContainsChunk),
		Weight:    1.0,
		SessionID: j.session.Meta.ID,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (j *JointDocIngestion) sequenceEdge(prevID, nextID uint32, now uint64) *Edge {
	return &Edge{
		SourceID:  prevID,
		TargetID:  nextID,
		Type:      uint8(EdgeTypeChunkSequence),
		Weight:    1.0,
		SessionID: j.session.Meta.ID,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

func (j *JointDocIngestion) writeAll(ctx context.Context, parentNode *Node, parentDoc *VersionDocument, chunkNodes []*Node, chunkDocs []*VersionDocument, chunkRefs []*ChunkRef, edges []*Edge) error {
	allNodes := make([]*Node, 0, 1+len(chunkNodes))
	allNodes = append(allNodes, parentNode)
	allNodes = append(allNodes, chunkNodes...)

	if err := j.nodeStore.WriteBatch(allNodes); err != nil {
		return fmt.Errorf("write nodes: %w", err)
	}

	allDocs := make([]*VersionDocument, 0, 1+len(chunkDocs))
	allDocs = append(allDocs, parentDoc)
	allDocs = append(allDocs, chunkDocs...)

	if err := j.docStore.WriteBatch(allDocs); err != nil {
		return fmt.Errorf("write docs: %w", err)
	}
	if err := j.chunkRefStore.WriteBatch(chunkRefs); err != nil {
		return fmt.Errorf("write chunk refs: %w", err)
	}
	if err := j.edgeStore.WriteBatch(edges); err != nil {
		return fmt.Errorf("write edges: %w", err)
	}
	return nil
}

// embedBatch calls EmbedBatch on all chunk texts. Returns nil, nil if no
// embedder is configured. The embedder handles internal concurrency,
// batching, and rate limiting.
func (j *JointDocIngestion) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if j.embedder == nil {
		return nil, nil
	}
	if len(texts) == 0 {
		return nil, nil
	}
	return j.embedder.EmbedBatch(ctx, texts)
}

// writeVectors constructs VersionVector records from batch embeddings and
// writes them to the vector store. Skips positions where embedding is nil
// or content was empty.
func (j *JointDocIngestion) writeVectors(_ context.Context, nodes []*Node, docs []*VersionDocument, embeddings [][]float32) ([]*VersionVector, error) {
	if embeddings == nil {
		return nil, nil
	}
	vectors := make([]*VersionVector, 0, len(nodes))
	for i, emb := range embeddings {
		if emb == nil || docs[i].Content == "" {
			continue
		}
		vectors = append(vectors, &VersionVector{
			NodeID: nodes[i].ID,
			Vector: emb,
		})
	}
	if len(vectors) == 0 {
		return nil, nil
	}
	if err := j.vectorStore.WriteBatch(vectors); err != nil {
		return nil, fmt.Errorf("write vectors: %w", err)
	}
	return vectors, nil
}

// extractChunkTexts collects content strings from chunk documents.
// Returns a parallel slice suitable for EmbedBatch.
func extractChunkTexts(docs []*VersionDocument) []string {
	texts := make([]string, len(docs))
	for i, doc := range docs {
		texts[i] = doc.Content
	}
	return texts
}

func (j *JointDocIngestion) indexBleve(ctx context.Context, docs []*VersionDocument) error {
	if j.session.BleveStore == nil {
		return nil
	}
	if j.session.BleveStore.Head() == nil {
		return nil
	}
	searchDocs := make([]*search.Document, len(docs))
	for i, vdoc := range docs {
		searchDocs[i] = ConvertToSearchDocument(vdoc)
	}
	return j.session.BleveStore.IndexBatch(ctx, searchDocs)
}

// ---------------------------------------------------------------------------
// Tombstoning
// ---------------------------------------------------------------------------

func (j *JointDocIngestion) tombstoneByKey(_ context.Context, canonicalKey string) error {
	node, err := j.findNodeByCanonicalKey(canonicalKey)
	if err != nil {
		return fmt.Errorf("find node: %w", err)
	}
	node.SupersededBy = ^uint32(0) // tombstone marker
	return j.nodeStore.Write(node)
}

func (j *JointDocIngestion) findNodeByCanonicalKey(key string) (*Node, error) {
	head := j.session.Manifest.Head
	all, err := j.nodeStore.ReadAllFromVersion(head)
	if err != nil {
		return nil, err
	}
	for _, n := range all {
		if n.CanonicalKey == key {
			return n, nil
		}
	}
	return nil, fmt.Errorf("sylkdir: node not found for key %q", key)
}

// ---------------------------------------------------------------------------
// Delta tracking
// ---------------------------------------------------------------------------

func (j *JointDocIngestion) trackAndCheckpoint(result *JointDocResult) {
	dt := j.session.DeltaTracker
	if dt != nil {
		dt.IncrNodes(uint64(1 + result.ChunkCount))
		dt.IncrEdges(uint64(result.EdgesCreated))
		dt.IncrVectors(uint64(result.VectorsCreated))
	}
	j.evalCheckpoint()
}

func (j *JointDocIngestion) evalCheckpoint() {
	cc := j.session.CheckpointCtrl
	if cc == nil {
		return
	}
	decision := cc.ShouldCheckpoint()
	if !decision.ShouldCheckpoint {
		return
	}
	_ = j.session.ExecuteCheckpoint(decision.Granularity)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func docCanonicalKey(dt search.DocumentType, path string) string {
	return fmt.Sprintf("doc:%s:%s", dt, path)
}

func extractNodeIDs(nodes []*Node) []uint32 {
	ids := make([]uint32, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}
