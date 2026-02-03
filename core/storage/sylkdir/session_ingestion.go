// Package sylkdir provides session-aware ingestion that writes to version stores.
package sylkdir

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"golang.org/x/sync/errgroup"
)

// NodeType represents the type of node in the knowledge graph.
type NodeType uint8

const (
	NodeTypeUnknown NodeType = iota
	NodeTypeFile
	NodeTypeFunction
	NodeTypeMethod
	NodeTypeType
	NodeTypeInterface
	NodeTypeConst
	NodeTypeVar

	// Document/chunk node types (non-contiguous to separate from code types).
	NodeTypeDocument NodeType = 20
	NodeTypeChunk    NodeType = 21
)

// Domain represents the domain of a node.
type Domain uint8

const (
	DomainCode Domain = iota
	DomainDoc
	DomainResearch
)

// EdgeType represents the type of edge in the knowledge graph.
type EdgeType uint8

const (
	EdgeTypeImports EdgeType = iota
	EdgeTypeContains
	EdgeTypeCalls
	EdgeTypeReferences

	// Document/chunk edge types (non-contiguous to separate from code types).
	EdgeTypeContainsChunk EdgeType = 10 // Document → Chunk
	EdgeTypeChunkSequence EdgeType = 11 // Chunk[i] → Chunk[i+1]
)

// SessionIngestion handles ingestion of code into session version stores.
type SessionIngestion struct {
	session       *Session
	nodeStore     *VersionNodeStore
	edgeStore     *VersionEdgeStore
	docStore      *VersionDocStore
	vectorStore   *VersionVectorStore
	chunkRefStore *ChunkRefStore
	embedder      embedder.Embedder
	chunker       *UniversalChunker
	nextNodeID    *uint32 // shared counter from session's BaseSnapshot
}

// NewSessionIngestion creates a new session ingestion handler.
func NewSessionIngestion(sess *Session) *SessionIngestion {
	return &SessionIngestion{
		session:       sess,
		nodeStore:     NewVersionNodeStore(sess),
		edgeStore:     NewVersionEdgeStore(sess),
		docStore:      NewVersionDocStore(sess),
		vectorStore:   NewVersionVectorStore(sess),
		chunkRefStore: NewChunkRefStore(sess),
		embedder:      nil, // Set via SetEmbedder
		nextNodeID:    sess.nextAllocID(),
	}
}

// allocID atomically allocates and returns the next node ID from the shared counter.
func (s *SessionIngestion) allocID() uint32 {
	return atomic.AddUint32(s.nextNodeID, 1) - 1
}

// SetEmbedder sets the embedder for vector generation and creates the universal chunker.
func (s *SessionIngestion) SetEmbedder(e embedder.Embedder) {
	s.embedder = e
	s.chunker = NewUniversalChunker(
		embedder.NewEnhancedHybridEmbedder(),
		uint32(e.MaxInputBytes()),
	)
}

// SessionIngestionResult contains the results of a session ingestion.
type SessionIngestionResult struct {
	NodesCreated     int
	EdgesCreated     int
	DocsCreated      int
	VectorsCreated   int
	ChunkRefsCreated int
	FilesProcessed   int
	Duration         time.Duration
	EmbeddingErrors  int
}

// ingestionEntities collects all entities built during the construction phase
// before concurrent writes. Fields are grouped by entity type with parallel
// embedTexts/embedIDs slices for batch embedding.
type ingestionEntities struct {
	fileNodes   []*Node
	symbolNodes []*Node
	chunkNodes  []*Node

	containsEdges []*Edge
	importEdges   []*Edge
	chunkEdges    []*Edge

	fileDocs  []*VersionDocument
	chunkDocs []*VersionDocument
	chunkRefs []*ChunkRef

	embedTexts []string // parallel: text for each node to embed
	embedIDs   []uint32 // parallel: node ID for each embedding

	// cachedEmbeddings is parallel to chunkNodes: index i holds the merger
	// embedding for chunkNodes[i], or nil if no cached embedding exists.
	// Chunks with cached embeddings are excluded from embedTexts/embedIDs
	// and written directly in writeEntities without re-embedding.
	cachedEmbeddings [][]float32

	fileIDMap   map[uint32]uint32 // ingestion file ID -> session node ID
	symbolIDMap map[uint32]uint32 // ingestion symbol ID -> session node ID
	filePathMap map[uint32]string // ingestion file ID -> file path
	docRefMap   map[uint32]uint32 // ingestion file ID -> DocRef
}

// IngestCodeGraph converts a CodeGraph + file contents into a fully-linked entity set:
// file nodes, symbol nodes, chunk nodes with ChunkRefs, documents, edges, and vectors.
// All files with content are chunked; chunks carry the semantic content for embedding.
//
// Three-phase pipeline:
//
//	Phase 1 (sequential): buildEntities — construct all nodes, edges, docs, refs, embed texts
//	Phase 2 (concurrent):  writeEntities — errgroup: store writes, embedding, Bleve indexing
//	Phase 3 (sequential): writeVectors — write embeddings after G2 completes
func (s *SessionIngestion) IngestCodeGraph(ctx context.Context, graph *ingestion.CodeGraph, contentMap map[string]string) (*SessionIngestionResult, error) {
	start := time.Now()

	log.Printf("[ingest] buildEntities: files=%d symbols=%d content_entries=%d",
		len(graph.Files), len(graph.Symbols), len(contentMap))
	buildStart := time.Now()
	ent := s.buildEntities(ctx, graph, contentMap)
	log.Printf("[ingest] buildEntities done: %v (nodes=%d+%d+%d edges=%d+%d+%d docs=%d+%d embeds=%d)",
		time.Since(buildStart),
		len(ent.fileNodes), len(ent.symbolNodes), len(ent.chunkNodes),
		len(ent.containsEdges), len(ent.importEdges), len(ent.chunkEdges),
		len(ent.fileDocs), len(ent.chunkDocs), len(ent.embedTexts))

	writeStart := time.Now()
	result, err := s.writeEntities(ctx, ent)
	if err != nil {
		return nil, err
	}
	log.Printf("[ingest] writeEntities done: %v", time.Since(writeStart))

	result.FilesProcessed = len(ent.fileNodes)
	s.updateDeltaTracker(result)
	s.evaluateCheckpoint()

	result.Duration = time.Since(start)
	log.Printf("[ingest] IngestCodeGraph total: %v", result.Duration)
	return result, nil
}

// =========================================================================
// Phase 1: Entity Construction (sequential, allocation-heavy)
// =========================================================================

// buildEntities constructs all graph entities in a single sequential pass.
func (s *SessionIngestion) buildEntities(ctx context.Context, graph *ingestion.CodeGraph, contentMap map[string]string) *ingestionEntities {
	ent := &ingestionEntities{
		fileIDMap:   make(map[uint32]uint32, len(graph.Files)),
		symbolIDMap: make(map[uint32]uint32, len(graph.Symbols)),
		filePathMap: make(map[uint32]string, len(graph.Files)),
		docRefMap:   make(map[uint32]uint32, len(graph.Files)),
	}

	t := time.Now()
	s.buildFileEntities(graph.Files, contentMap, ent)
	log.Printf("[build] files: %v (%d)", time.Since(t), len(ent.fileNodes))

	t = time.Now()
	s.buildSymbolEntities(graph.Symbols, ent)
	log.Printf("[build] symbols: %v (%d)", time.Since(t), len(ent.symbolNodes))

	t = time.Now()
	s.buildChunkEntities(ctx, graph, contentMap, ent)
	log.Printf("[build] chunks: %v (%d nodes, %d docs, %d refs, %d edges)",
		time.Since(t), len(ent.chunkNodes), len(ent.chunkDocs), len(ent.chunkRefs), len(ent.chunkEdges))

	t = time.Now()
	s.buildEdgeEntities(graph, ent)
	log.Printf("[build] edges: %v (contains=%d imports=%d)", time.Since(t), len(ent.containsEdges), len(ent.importEdges))

	t = time.Now()
	s.buildEmbedTexts(ent)
	log.Printf("[build] embedTexts: %v (%d)", time.Since(t), len(ent.embedTexts))

	return ent
}

// buildFileEntities creates file nodes and file documents.
func (s *SessionIngestion) buildFileEntities(files []ingestion.FileNode, contentMap map[string]string, ent *ingestionEntities) {
	ent.fileNodes = make([]*Node, len(files))
	ent.fileDocs = make([]*VersionDocument, len(files))
	nowNano := time.Now().UnixNano()
	nowNode := uint64(nowNano)

	for i, file := range files {
		nodeID := s.allocID()
		ent.fileIDMap[file.ID] = nodeID
		ent.filePathMap[file.ID] = file.Path

		docID := "file_" + strconv.FormatUint(uint64(nodeID), 10)

		var docRef uint32
		if s.session.DocIDMap != nil {
			docRef = s.session.DocIDMap.GetOrAssign(docID)
		}
		ent.docRefMap[file.ID] = docRef

		ent.fileNodes[i] = &Node{
			ID:           nodeID,
			CanonicalKey: "file:" + file.Path,
			Domain:       uint8(DomainCode),
			NodeType:     uint8(NodeTypeFile),
			Name:         file.Path,
			Path:         file.Path,
			CreatedAt:    nowNode,
			SessionID:    s.session.Meta.ID,
			DocRef:       docRef,
		}

		ent.fileDocs[i] = &VersionDocument{
			ID:        docID,
			Path:      file.Path,
			Type:      "source_code",
			Content:   contentMap[file.Path],
			Language:  file.Lang,
			IndexedAt: nowNano,
		}
	}
}

// buildSymbolEntities creates symbol nodes.
func (s *SessionIngestion) buildSymbolEntities(symbols []ingestion.SymbolNode, ent *ingestionEntities) {
	ent.symbolNodes = make([]*Node, len(symbols))
	now := uint64(time.Now().UnixNano())

	for i, sym := range symbols {
		nodeID := s.allocID()
		ent.symbolIDMap[sym.ID] = nodeID
		filePath := ent.filePathMap[sym.FileID]

		ent.symbolNodes[i] = &Node{
			ID:           nodeID,
			CanonicalKey: "symbol:" + filePath + ":" + sym.Name + ":" + sym.Kind.String(),
			Domain:       uint8(DomainCode),
			NodeType:     uint8(symbolKindToNodeType(sym.Kind)),
			Name:         sym.Name,
			Path:         filePath,
			Signature:    sym.Signature,
			CreatedAt:    now,
			SessionID:    s.session.Meta.ID,
			DocRef:       ent.docRefMap[sym.FileID],
		}
	}
}

// chunkFileEntry pairs a file with its content for parallel chunking.
type chunkFileEntry struct {
	file    ingestion.FileNode
	content string
}

// chunkLocalResult holds per-worker chunk results for lock-free accumulation.
type chunkLocalResult struct {
	nodes      []*Node
	docs       []*VersionDocument
	refs       []*ChunkRef
	edges      []*Edge
	cachedEmbs [][]float32 // parallel to nodes: merger embedding or nil
}

// buildChunkEntities chunks all files in parallel across runtime.NumCPU() workers.
// Each worker processes its file subset independently, then results are merged.
func (s *SessionIngestion) buildChunkEntities(ctx context.Context, graph *ingestion.CodeGraph, contentMap map[string]string, ent *ingestionEntities) {
	if s.chunker == nil {
		return
	}

	// Filter to files with content.
	entries := make([]chunkFileEntry, 0, len(graph.Files))
	for _, file := range graph.Files {
		if c := contentMap[file.Path]; c != "" {
			entries = append(entries, chunkFileEntry{file: file, content: c})
		}
	}
	if len(entries) == 0 {
		return
	}

	symsByFile := groupSymbolsByFile(graph.Symbols)
	nowNano := time.Now().UnixNano()
	now := uint64(nowNano)

	// Split across workers.
	workers := runtime.NumCPU()
	if workers > len(entries) {
		workers = len(entries)
	}
	perWorker := (len(entries) + workers - 1) / workers

	results := make([]chunkLocalResult, workers)

	var wg sync.WaitGroup
	for w := range workers {
		lo := w * perWorker
		hi := lo + perWorker
		if hi > len(entries) {
			hi = len(entries)
		}
		if lo >= hi {
			continue
		}

		wg.Add(1)
		go func(idx int, batch []chunkFileEntry) {
			defer wg.Done()
			var local chunkLocalResult

			for _, fe := range batch {
				parentNodeID := ent.fileIDMap[fe.file.ID]
				parentDocRef := ent.docRefMap[fe.file.ID]

				cr := s.computeChunks(ctx, stringToReadOnlyBytes(fe.content), symsByFile[fe.file.ID])

				// Pre-compute the doc ID prefix once per file: "file_<parentNodeID>_c"
				docIDPrefix := "file_" + strconv.FormatUint(uint64(parentNodeID), 10) + "_c"
				// Pre-compute canonical key prefix once per file: "chunk:<path>:"
				canonPrefix := "chunk:" + fe.file.Path + ":"

				var prevChunkID uint32
				for i, b := range cr.Boundaries {
					chunkID := s.allocID()

					chunkSeq := zeroPad(i, 3)
					docID := docIDPrefix + chunkSeq

					var chunkDocRef uint32
					if s.session.DocIDMap != nil {
						chunkDocRef = s.session.DocIDMap.GetOrAssign(docID)
					}

					local.nodes = append(local.nodes, &Node{
						ID:           chunkID,
						CanonicalKey: canonPrefix + zeroPad(i, 4),
						Domain:       uint8(DomainCode),
						NodeType:     uint8(NodeTypeChunk),
						Name:         fe.content[b.ByteStart:b.ByteEnd],
						Path:         fe.file.Path,
						CreatedAt:    now,
						SessionID:    s.session.Meta.ID,
						DocRef:       chunkDocRef,
					})

					local.docs = append(local.docs, &VersionDocument{
						ID:        docID,
						Path:      fe.file.Path,
						Type:      "source_code",
						Content:   fe.content[b.ByteStart:b.ByteEnd],
						IndexedAt: nowNano,
					})

					local.refs = append(local.refs, &ChunkRef{
						NodeID: chunkID,
						Span: ChunkSpan{
							DocRef:    parentDocRef,
							ChunkSeq:  uint16(i),
							ByteStart: b.ByteStart,
							ByteEnd:   b.ByteEnd,
							LineStart: b.LineStart,
							LineEnd:   b.LineEnd,
							Flags:     PackFlags(OverlapNone, ContentClassSourceCode, b.Kind),
						},
					})

					// Parallel to nodes: cache merger embedding or nil.
					var emb []float32
					if cr.Embeddings != nil && i < len(cr.Embeddings) {
						emb = cr.Embeddings[i]
					}
					local.cachedEmbs = append(local.cachedEmbs, emb)

					local.edges = append(local.edges, &Edge{
						SourceID:  parentNodeID,
						TargetID:  chunkID,
						Type:      uint8(EdgeTypeContainsChunk),
						Weight:    1.0,
						SessionID: s.session.Meta.ID,
						CreatedAt: now,
						UpdatedAt: now,
					})

					if i > 0 {
						local.edges = append(local.edges, &Edge{
							SourceID:  prevChunkID,
							TargetID:  chunkID,
							Type:      uint8(EdgeTypeChunkSequence),
							Weight:    1.0,
							SessionID: s.session.Meta.ID,
							CreatedAt: now,
							UpdatedAt: now,
						})
					}
					prevChunkID = chunkID
				}
			}
			results[idx] = local
		}(w, entries[lo:hi])
	}
	wg.Wait()

	// Merge worker results (cachedEmbs is parallel to nodes).
	for _, r := range results {
		ent.chunkNodes = append(ent.chunkNodes, r.nodes...)
		ent.chunkDocs = append(ent.chunkDocs, r.docs...)
		ent.chunkRefs = append(ent.chunkRefs, r.refs...)
		ent.chunkEdges = append(ent.chunkEdges, r.edges...)
		ent.cachedEmbeddings = append(ent.cachedEmbeddings, r.cachedEmbs...)
	}
}

// computeChunks selects the chunking strategy based on available symbols.
// Returns chunk boundaries with their cached merger embeddings.
func (s *SessionIngestion) computeChunks(ctx context.Context, content []byte, symbols []ingestion.SymbolNode) ChunkResult {
	if len(symbols) > 0 {
		return s.chunker.ChunkWithSymbols(ctx, content, symbolsToBounds(symbols))
	}
	return s.chunker.Chunk(ctx, content)
}

// buildEdgeEntities creates structural edges (Contains, Imports).
func (s *SessionIngestion) buildEdgeEntities(graph *ingestion.CodeGraph, ent *ingestionEntities) {
	ent.containsEdges = s.convertEdges(graph.ContainsEdges, ent.fileIDMap, ent.symbolIDMap, EdgeTypeContains)
	ent.importEdges = s.convertEdges(graph.ImportEdges, ent.fileIDMap, ent.fileIDMap, EdgeTypeImports)
}

// buildEmbedTexts builds parallel embedTexts/embedIDs from all node types.
// File nodes get path-only embedding; chunks carry semantic content.
// Chunks with cached merger embeddings are excluded — they bypass G2 entirely.
func (s *SessionIngestion) buildEmbedTexts(ent *ingestionEntities) {
	totalCap := len(ent.fileNodes) + len(ent.symbolNodes) + len(ent.chunkNodes)
	ent.embedTexts = make([]string, 0, totalCap)
	ent.embedIDs = make([]uint32, 0, totalCap)

	for _, node := range ent.fileNodes {
		ent.embedTexts = append(ent.embedTexts, "file: "+node.Path)
		ent.embedIDs = append(ent.embedIDs, node.ID)
	}

	for _, node := range ent.symbolNodes {
		text := s.nodeToEmbeddingText(node)
		if text == "" {
			continue
		}
		ent.embedTexts = append(ent.embedTexts, text)
		ent.embedIDs = append(ent.embedIDs, node.ID)
	}

	for i, node := range ent.chunkNodes {
		if node.Name == "" {
			continue
		}
		// Skip chunks that have cached merger embeddings —
		// their vectors are written directly from the cache.
		if i < len(ent.cachedEmbeddings) && ent.cachedEmbeddings[i] != nil {
			continue
		}
		ent.embedTexts = append(ent.embedTexts, node.Name)
		ent.embedIDs = append(ent.embedIDs, node.ID)
	}
}

// =========================================================================
// Phase 2: Concurrent Writes (errgroup)
// =========================================================================

// writeEntities runs store writes, batch embedding, and Bleve indexing concurrently.
func (s *SessionIngestion) writeEntities(ctx context.Context, ent *ingestionEntities) (*SessionIngestionResult, error) {
	result := &SessionIngestionResult{}

	// Pre-compute concatenated slices once (shared across G1 and G3).
	allNodes := slices.Concat(ent.fileNodes, ent.symbolNodes, ent.chunkNodes)
	allEdges := slices.Concat(ent.containsEdges, ent.importEdges, ent.chunkEdges)
	allDocs := slices.Concat(ent.fileDocs, ent.chunkDocs)

	// PendingMode: stash entities in memory for direct CommitToGlobal consumption.
	// Skips G1 session store writes entirely.
	pendingMode := s.session.PendingNodes != nil
	if pendingMode {
		s.session.PendingNodes = allNodes
		s.session.PendingEdges = allEdges
		s.session.PendingDocs = allDocs
		s.session.PendingChunkRefs = ent.chunkRefs
		result.NodesCreated = len(allNodes)
		result.EdgesCreated = len(allEdges)
		result.DocsCreated = len(allDocs)
		result.ChunkRefsCreated = len(ent.chunkRefs)
	}

	g, gctx := errgroup.WithContext(ctx)

	// G1: Write all stores (nodes, edges, docs, chunk refs).
	// Skipped in PendingMode — entities go directly to CommitToGlobal.
	if !pendingMode {
		g.Go(func() error {
			t := time.Now()
			err := s.writeStores(gctx, allNodes, allEdges, allDocs, ent.chunkRefs, result)
			log.Printf("[write] G1 stores: %v (nodes=%d edges=%d docs=%d refs=%d)",
				time.Since(t), result.NodesCreated, result.EdgesCreated, result.DocsCreated, result.ChunkRefsCreated)
			return err
		})
	}

	// G2: Batch embed all texts concurrently.
	// embedDone signals G4 to start vector writes without waiting for G3 (Bleve).
	var embeddings [][]float32
	embedDone := make(chan struct{})
	g.Go(func() error {
		t := time.Now()
		var err error
		embeddings, err = s.embedAll(gctx, ent.embedTexts)
		close(embedDone)
		log.Printf("[write] G2 embed: %v (%d texts)", time.Since(t), len(ent.embedTexts))
		return err
	})

	// G3: Index all docs into Bleve concurrently.
	g.Go(func() error {
		t := time.Now()
		err := s.indexDocsIntoSessionBleve(gctx, allDocs)
		log.Printf("[write] G3 bleve: %v (%d docs)", time.Since(t), len(allDocs))
		return err
	})

	// G4: Write vectors as soon as G2 completes (overlaps with G3).
	// Merges G2 fresh embeddings with cached merger embeddings.
	g.Go(func() error {
		select {
		case <-embedDone:
		case <-gctx.Done():
			return gctx.Err()
		}
		t := time.Now()
		vectors, err := s.writeVectorsWithCache(gctx, ent, embeddings)
		if err != nil {
			return err
		}
		result.VectorsCreated = len(vectors)
		log.Printf("[write] G4 vectors: %v (%d pending)", time.Since(t), len(vectors))
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return result, nil
}

// writeStores writes all entity stores concurrently.
// Nodes, edges, docs, and chunk refs have independent SharedDataFiles
// and mutexes, so they can run in parallel.
// Receives pre-computed concatenated slices to avoid duplicate allocations.
func (s *SessionIngestion) writeStores(ctx context.Context, allNodes []*Node, allEdges []*Edge, allDocs []*VersionDocument, chunkRefs []*ChunkRef, result *SessionIngestionResult) error {
	g, _ := errgroup.WithContext(ctx)

	g.Go(func() error {
		t := time.Now()
		if err := s.nodeStore.WriteBatch(allNodes); err != nil {
			return fmt.Errorf("write nodes: %w", err)
		}
		log.Printf("[stores] nodes: %v (%d)", time.Since(t), len(allNodes))
		return nil
	})

	g.Go(func() error {
		t := time.Now()
		if err := s.edgeStore.WriteBatch(allEdges); err != nil {
			return fmt.Errorf("write edges: %w", err)
		}
		log.Printf("[stores] edges: %v (%d)", time.Since(t), len(allEdges))
		return nil
	})

	g.Go(func() error {
		t := time.Now()
		if err := s.docStore.WriteBatch(allDocs); err != nil {
			return fmt.Errorf("write docs: %w", err)
		}
		log.Printf("[stores] docs: %v (%d)", time.Since(t), len(allDocs))
		return nil
	})

	g.Go(func() error {
		if len(chunkRefs) == 0 {
			return nil
		}
		t := time.Now()
		if err := s.chunkRefStore.WriteBatch(chunkRefs); err != nil {
			return fmt.Errorf("write chunk refs: %w", err)
		}
		log.Printf("[stores] chunkRefs: %v (%d)", time.Since(t), len(chunkRefs))
		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	result.NodesCreated = len(allNodes)
	result.EdgesCreated = len(allEdges)
	result.DocsCreated = len(allDocs)
	result.ChunkRefsCreated = len(chunkRefs)
	return nil
}

// embedAll calls EmbedBatch on all texts. Returns nil, nil if no embedder.
func (s *SessionIngestion) embedAll(ctx context.Context, texts []string) ([][]float32, error) {
	if s.embedder == nil || len(texts) == 0 {
		return nil, nil
	}
	return s.embedder.EmbedBatch(ctx, texts)
}

// writeVectorsWithCache combines fresh G2 embeddings with cached merger embeddings,
// then writes all vectors in one batch. Cached embeddings are for chunk nodes
// whose embeddings were computed during agglomerative merging and don't need
// to be recomputed in G2.
func (s *SessionIngestion) writeVectorsWithCache(ctx context.Context, ent *ingestionEntities, embeddings [][]float32) ([]*VersionVector, error) {
	totalCap := len(ent.embedIDs) + len(ent.chunkNodes)
	vectors := make([]*VersionVector, 0, totalCap)

	// Fresh G2 embeddings.
	if embeddings != nil {
		for i, emb := range embeddings {
			if emb == nil {
				continue
			}
			vectors = append(vectors, &VersionVector{
				NodeID: ent.embedIDs[i],
				Vector: emb,
			})
		}
	}

	// Cached merger embeddings (parallel to chunkNodes, bypassed G2).
	for i, emb := range ent.cachedEmbeddings {
		if emb == nil {
			continue
		}
		vectors = append(vectors, &VersionVector{
			NodeID: ent.chunkNodes[i].ID,
			Vector: emb,
		})
	}

	if len(vectors) == 0 {
		return nil, nil
	}

	// Store vectors in memory for direct consumption by CommitToGlobal.
	// Eliminates the session disk round-trip (WAL + data file + offset index).
	s.session.PendingVectors = vectors
	return vectors, nil
}

// groupSymbolsByFile partitions symbols by their FileID.
func groupSymbolsByFile(symbols []ingestion.SymbolNode) map[uint32][]ingestion.SymbolNode {
	m := make(map[uint32][]ingestion.SymbolNode, len(symbols))
	for _, sym := range symbols {
		m[sym.FileID] = append(m[sym.FileID], sym)
	}
	return m
}

// symbolsToBounds converts ingestion.SymbolNode to SymbolBound for the chunker.
func symbolsToBounds(symbols []ingestion.SymbolNode) []SymbolBound {
	bounds := make([]SymbolBound, len(symbols))
	for i, sym := range symbols {
		bounds[i] = SymbolBound{
			StartLine: sym.StartLine,
			EndLine:   sym.EndLine,
			Strength:  symbolKindStrengthFromIngestion(sym.Kind),
		}
	}
	return bounds
}

// symbolKindStrengthFromIngestion maps ingestion.SymbolKind to boundary strength.
func symbolKindStrengthFromIngestion(kind ingestion.SymbolKind) uint32 {
	switch kind {
	case ingestion.SymbolKindInterface, ingestion.SymbolKindType:
		return 3
	case ingestion.SymbolKindFunction, ingestion.SymbolKindMethod:
		return 2
	default:
		return 1
	}
}

// nodeToEmbeddingText converts a node to text suitable for embedding.
func (s *SessionIngestion) nodeToEmbeddingText(node *Node) string {
	switch NodeType(node.NodeType) {
	case NodeTypeFile:
		return "file: " + node.Path
	case NodeTypeFunction, NodeTypeMethod:
		if node.Signature != "" {
			return node.Name + " " + node.Signature
		}
		return node.Name
	case NodeTypeType, NodeTypeInterface:
		return "type " + node.Name
	case NodeTypeConst, NodeTypeVar:
		return "var " + node.Name
	case NodeTypeDocument:
		return "doc: " + node.Path
	case NodeTypeChunk:
		return node.Name
	default:
		return node.Name
	}
}

// IngestDirectory runs the full ingestion pipeline on a directory.
func (s *SessionIngestion) IngestDirectory(ctx context.Context, rootPath string) (*SessionIngestionResult, error) {
	return s.IngestDirectoryWithConfig(ctx, &ingestion.Config{
		RootPath: rootPath,
		Workers:  ingestion.WorkerCount(),
	})
}

// IngestDirectoryWithConfig runs ingestion with full configuration control.
// Sets SkipPersist, SkipBleve, and RetainContent automatically.
func (s *SessionIngestion) IngestDirectoryWithConfig(ctx context.Context, config *ingestion.Config) (*SessionIngestionResult, error) {
	config.SkipPersist = true
	config.SkipBleve = true
	config.RetainContent = true

	t := time.Now()
	result, err := ingestion.IngestCodebase(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("ingest codebase: %w", err)
	}
	log.Printf("[pipeline] IngestCodebase: %v (files=%d symbols=%d lines=%d)",
		time.Since(t), result.TotalFiles, result.TotalSymbols, result.TotalLines)

	t = time.Now()
	contentMap := buildContentMap(result.MappedFiles)
	log.Printf("[pipeline] buildContentMap: %v (%d entries)", time.Since(t), len(contentMap))

	return s.IngestCodeGraph(ctx, result.Graph, contentMap)
}

// convertEdges converts ingestion Edges to sylkdir Edges.
func (s *SessionIngestion) convertEdges(edges []ingestion.Edge, sourceIDMap, targetIDMap map[uint32]uint32, edgeType EdgeType) []*Edge {
	result := make([]*Edge, 0, len(edges))
	now := uint64(time.Now().UnixNano())

	for _, e := range edges {
		sourceID, sourceOK := sourceIDMap[e.SourceID]
		targetID, targetOK := targetIDMap[e.TargetID]

		if !sourceOK || !targetOK {
			continue // Skip edges with unmapped nodes
		}

		result = append(result, &Edge{
			SourceID:  sourceID,
			TargetID:  targetID,
			Type:      uint8(edgeType),
			Weight:    1.0,
			SessionID: s.session.Meta.ID,
			AgentID:   0,
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	return result
}

// buildContentMap creates a path-to-content mapping from mapped files.
func buildContentMap(mapped []ingestion.MappedFile) map[string]string {
	contentMap := make(map[string]string, len(mapped))
	for _, m := range mapped {
		contentMap[m.Path] = string(m.Data)
	}
	return contentMap
}

// symbolKindToNodeType converts ingestion SymbolKind to sylkdir NodeType.
func symbolKindToNodeType(kind ingestion.SymbolKind) NodeType {
	switch kind {
	case ingestion.SymbolKindFunction:
		return NodeTypeFunction
	case ingestion.SymbolKindMethod:
		return NodeTypeMethod
	case ingestion.SymbolKindType:
		return NodeTypeType
	case ingestion.SymbolKindInterface:
		return NodeTypeInterface
	case ingestion.SymbolKindConst:
		return NodeTypeConst
	case ingestion.SymbolKindVar:
		return NodeTypeVar
	default:
		return NodeTypeUnknown
	}
}

// stringToReadOnlyBytes returns a []byte that shares the string's underlying memory.
// The returned slice MUST NOT be written to — strings are immutable in Go.
// Avoids the allocation and copy of []byte(s) for read-only consumers
// (boundary detection, segment splitting).
func stringToReadOnlyBytes(s string) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// zeroPad formats n as a decimal string zero-padded to width digits.
// Uses strconv.AppendInt to avoid fmt.Sprintf overhead (called ~100k+ times per boot).
func zeroPad(n, width int) string {
	var buf [20]byte // max uint64 decimal digits
	s := strconv.AppendInt(buf[:0], int64(n), 10)
	if len(s) >= width {
		return string(s)
	}
	var padded [20]byte
	pad := width - len(s)
	for i := range pad {
		padded[i] = '0'
	}
	copy(padded[pad:], s)
	return string(padded[:width])
}

// docTypeToContentClass converts a DocType string to a ContentClass.
func docTypeToContentClass(docType string) ContentClass {
	return ContentClassFromDocType(search.DocumentType(docType))
}

// IngestWithContent runs ingestion and includes file content in documents.
func (s *SessionIngestion) IngestWithContent(ctx context.Context, rootPath string) (*SessionIngestionResult, error) {
	files, err := ingestion.DiscoverFiles(ctx, rootPath, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("discover files: %w", err)
	}

	mappedFiles, err := ingestion.ReadFiles(ctx, files, ingestion.WorkerCount())
	if err != nil {
		return nil, fmt.Errorf("read files: %w", err)
	}

	pool := ingestion.NewParserPool(ingestion.WorkerCount())
	defer pool.Close()
	parsed, _ := pool.ParseAll(ctx, mappedFiles)

	graph := ingestion.Aggregate(rootPath, mappedFiles, parsed)
	contentMap := buildContentMap(mappedFiles)

	return s.IngestCodeGraph(ctx, graph, contentMap)
}

// indexDocsIntoSessionBleve indexes version documents into the session's
// per-version Bleve index for immediate full-text searchability.
// No-op if the session has no BleveStore or if HEAD is not open.
func (s *SessionIngestion) indexDocsIntoSessionBleve(ctx context.Context, docs []*VersionDocument) error {
	if s.session.BleveStore == nil || s.session.BleveStore.Head() == nil {
		return nil
	}
	if len(docs) == 0 {
		return nil
	}

	searchDocs := make([]*search.Document, len(docs))
	for i, vdoc := range docs {
		searchDocs[i] = ConvertToSearchDocument(vdoc)
	}
	return s.session.BleveStore.IndexBatch(ctx, searchDocs)
}

// GetNodeStore returns the underlying node store for direct access.
func (s *SessionIngestion) GetNodeStore() *VersionNodeStore {
	return s.nodeStore
}

// GetEdgeStore returns the underlying edge store for direct access.
func (s *SessionIngestion) GetEdgeStore() *VersionEdgeStore {
	return s.edgeStore
}

// GetDocStore returns the underlying document store for direct access.
func (s *SessionIngestion) GetDocStore() *VersionDocStore {
	return s.docStore
}

// GetVectorStore returns the underlying vector store for direct access.
func (s *SessionIngestion) GetVectorStore() *VersionVectorStore {
	return s.vectorStore
}

// GetChunkRefStore returns the underlying chunk ref store for direct access.
func (s *SessionIngestion) GetChunkRefStore() *ChunkRefStore {
	return s.chunkRefStore
}

// updateDeltaTracker records ingestion results in the session's delta tracker.
func (s *SessionIngestion) updateDeltaTracker(result *SessionIngestionResult) {
	dt := s.session.DeltaTracker
	if dt == nil {
		return
	}
	dt.IncrNodes(uint64(result.NodesCreated))
	dt.IncrEdges(uint64(result.EdgesCreated))
	dt.IncrVectors(uint64(result.VectorsCreated))
}

// evaluateCheckpoint checks the checkpoint controller and executes if triggered.
func (s *SessionIngestion) evaluateCheckpoint() {
	cc := s.session.CheckpointCtrl
	if cc == nil {
		return
	}
	decision := cc.ShouldCheckpoint()
	if !decision.ShouldCheckpoint {
		return
	}
	// Best-effort: checkpoint failure does not fail ingestion.
	_ = s.session.ExecuteCheckpoint(decision.Granularity)
}
