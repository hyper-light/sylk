// Package sylkdir provides session-aware ingestion that writes to version stores.
package sylkdir

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
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
	session     *Session
	nodeStore   *VersionNodeStore
	edgeStore   *VersionEdgeStore
	docStore    *VersionDocStore
	vectorStore *VersionVectorStore
	embedder    embedder.Embedder

	// ID allocation
	nextNodeID uint32
}

// NewSessionIngestion creates a new session ingestion handler.
func NewSessionIngestion(sess *Session) *SessionIngestion {
	return &SessionIngestion{
		session:     sess,
		nodeStore:   NewVersionNodeStore(sess),
		edgeStore:   NewVersionEdgeStore(sess),
		docStore:    NewVersionDocStore(sess),
		vectorStore: NewVersionVectorStore(sess),
		embedder:    nil, // Set via SetEmbedder
		nextNodeID:  1,
	}
}

// SetEmbedder sets the embedder for vector generation.
func (s *SessionIngestion) SetEmbedder(e embedder.Embedder) {
	s.embedder = e
}

// SessionIngestionResult contains the results of a session ingestion.
type SessionIngestionResult struct {
	NodesCreated    int
	EdgesCreated    int
	DocsCreated     int
	VectorsCreated  int
	FilesProcessed  int
	Duration        time.Duration
	EmbeddingErrors int
}

// IngestCodeGraph converts and writes a CodeGraph to the session version stores.
func (s *SessionIngestion) IngestCodeGraph(ctx context.Context, graph *ingestion.CodeGraph) (*SessionIngestionResult, error) {
	start := time.Now()
	result := &SessionIngestionResult{}

	// Convert and write file nodes
	fileNodes, fileIDMap, filePathMap, docRefMap, err := s.convertFileNodes(graph.Files)
	if err != nil {
		return nil, fmt.Errorf("convert file nodes: %w", err)
	}
	if err := s.nodeStore.WriteBatch(fileNodes); err != nil {
		return nil, fmt.Errorf("write file nodes: %w", err)
	}
	result.NodesCreated += len(fileNodes)
	result.FilesProcessed = len(fileNodes)

	// Convert and write symbol nodes
	symbolNodes, symbolIDMap, err := s.convertSymbolNodes(graph.Symbols, fileIDMap, filePathMap, docRefMap)
	if err != nil {
		return nil, fmt.Errorf("convert symbol nodes: %w", err)
	}
	if err := s.nodeStore.WriteBatch(symbolNodes); err != nil {
		return nil, fmt.Errorf("write symbol nodes: %w", err)
	}
	result.NodesCreated += len(symbolNodes)

	// Generate and write vectors for all nodes (if embedder is set)
	if s.embedder != nil {
		allNodes := append(fileNodes, symbolNodes...)
		vectors, embeddingErrors := s.generateVectors(ctx, allNodes)
		if len(vectors) > 0 {
			if err := s.vectorStore.WriteBatch(vectors); err != nil {
				return nil, fmt.Errorf("write vectors: %w", err)
			}
		}
		result.VectorsCreated = len(vectors)
		result.EmbeddingErrors = embeddingErrors
	}

	// Convert and write contains edges (file -> symbol)
	containsEdges := s.convertEdges(graph.ContainsEdges, fileIDMap, symbolIDMap, EdgeTypeContains)
	if err := s.edgeStore.WriteBatch(containsEdges); err != nil {
		return nil, fmt.Errorf("write contains edges: %w", err)
	}
	result.EdgesCreated += len(containsEdges)

	// Convert and write import edges (file -> file)
	importEdges := s.convertEdges(graph.ImportEdges, fileIDMap, fileIDMap, EdgeTypeImports)
	if err := s.edgeStore.WriteBatch(importEdges); err != nil {
		return nil, fmt.Errorf("write import edges: %w", err)
	}
	result.EdgesCreated += len(importEdges)

	// Create documents for files
	docs := s.createDocuments(graph.Files, fileIDMap)
	if err := s.docStore.WriteBatch(docs); err != nil {
		return nil, fmt.Errorf("write documents: %w", err)
	}
	result.DocsCreated = len(docs)

	// Index into session Bleve for immediate searchability
	if err := s.indexDocsIntoSessionBleve(ctx, docs); err != nil {
		return nil, fmt.Errorf("index documents in session bleve: %w", err)
	}

	// Update delta tracker and evaluate checkpoint.
	s.updateDeltaTracker(result)
	s.evaluateCheckpoint()

	result.Duration = time.Since(start)
	return result, nil
}

// generateVectors creates embeddings for nodes using the configured embedder.
func (s *SessionIngestion) generateVectors(ctx context.Context, nodes []*Node) ([]*VersionVector, int) {
	vectors := make([]*VersionVector, 0, len(nodes))
	errors := 0

	for _, node := range nodes {
		// Create embedding text from node metadata
		text := s.nodeToEmbeddingText(node)
		if text == "" {
			continue
		}

		vec, err := s.embedder.Embed(ctx, text)
		if err != nil {
			errors++
			continue
		}

		vectors = append(vectors, &VersionVector{
			NodeID: node.ID,
			Vector: vec,
		})
	}

	return vectors, errors
}

// nodeToEmbeddingText converts a node to text suitable for embedding.
func (s *SessionIngestion) nodeToEmbeddingText(node *Node) string {
	switch NodeType(node.NodeType) {
	case NodeTypeFile:
		return fmt.Sprintf("file: %s", node.Path)
	case NodeTypeFunction, NodeTypeMethod:
		if node.Signature != "" {
			return fmt.Sprintf("%s %s", node.Name, node.Signature)
		}
		return node.Name
	case NodeTypeType, NodeTypeInterface:
		return fmt.Sprintf("type %s", node.Name)
	case NodeTypeConst, NodeTypeVar:
		return fmt.Sprintf("var %s", node.Name)
	case NodeTypeDocument:
		return fmt.Sprintf("doc: %s", node.Path)
	case NodeTypeChunk:
		return node.Name
	default:
		return node.Name
	}
}

// IngestDirectory runs the full ingestion pipeline on a directory.
func (s *SessionIngestion) IngestDirectory(ctx context.Context, rootPath string) (*SessionIngestionResult, error) {
	// Run the ingestion pipeline
	config := &ingestion.Config{
		RootPath:    rootPath,
		Workers:     ingestion.WorkerCount(),
		SkipPersist: true, // We handle persistence via version stores
		SkipBleve:   true, // We handle doc indexing via version stores
	}

	ingestionResult, err := ingestion.IngestCodebase(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("ingest codebase: %w", err)
	}

	// Convert and write to version stores
	return s.IngestCodeGraph(ctx, ingestionResult.Graph)
}

// convertFileNodes converts ingestion FileNodes to sylkdir Nodes.
// Returns: nodes, idMap (ingestion ID -> session node ID), pathMap (ingestion ID -> file path),
// docRefMap (ingestion ID -> DocRef uint32 for symbol node linking).
func (s *SessionIngestion) convertFileNodes(files []ingestion.FileNode) ([]*Node, map[uint32]uint32, map[uint32]string, map[uint32]uint32, error) {
	nodes := make([]*Node, len(files))
	idMap := make(map[uint32]uint32, len(files))      // old ID -> new ID
	pathMap := make(map[uint32]string, len(files))     // old ID -> file path
	docRefMap := make(map[uint32]uint32, len(files))   // old ID -> DocRef

	now := uint64(time.Now().UnixNano())

	for i, file := range files {
		newID := s.nextNodeID
		s.nextNodeID++
		idMap[file.ID] = newID
		pathMap[file.ID] = file.Path

		// Assign DocRef via DocIDMap. The doc string ID matches what
		// createDocuments produces, so the OffsetIndex key will match.
		var docRef uint32
		if s.session.DocIDMap != nil {
			docRef = s.session.DocIDMap.GetOrAssign(fmt.Sprintf("file_%d", newID))
		}
		docRefMap[file.ID] = docRef

		nodes[i] = &Node{
			ID:           newID,
			CanonicalKey: fmt.Sprintf("file:%s", file.Path),
			Domain:       uint8(DomainCode),
			NodeType:     uint8(NodeTypeFile),
			Name:         file.Path,
			Path:         file.Path,
			Package:      "",
			Signature:    "",
			CreatedAt:    now,
			SessionID:    s.session.Meta.ID,
			CreatedBy:    0,
			DocRef:       docRef,
		}
	}

	return nodes, idMap, pathMap, docRefMap, nil
}

// convertSymbolNodes converts ingestion SymbolNodes to sylkdir Nodes.
// filePathMap maps ingestion file IDs to file paths for canonical key construction.
// docRefMap maps ingestion file IDs to DocRef values so symbols inherit their parent file's document.
func (s *SessionIngestion) convertSymbolNodes(symbols []ingestion.SymbolNode, fileIDMap map[uint32]uint32, filePathMap map[uint32]string, docRefMap map[uint32]uint32) ([]*Node, map[uint32]uint32, error) {
	nodes := make([]*Node, len(symbols))
	idMap := make(map[uint32]uint32, len(symbols))

	now := uint64(time.Now().UnixNano())

	for i, sym := range symbols {
		newID := s.nextNodeID
		s.nextNodeID++
		idMap[sym.ID] = newID

		nodeType := symbolKindToNodeType(sym.Kind)
		filePath := filePathMap[sym.FileID]

		nodes[i] = &Node{
			ID:           newID,
			CanonicalKey: fmt.Sprintf("symbol:%s:%s:%s", filePath, sym.Name, sym.Kind.String()),
			Domain:       uint8(DomainCode),
			NodeType:     uint8(nodeType),
			Name:         sym.Name,
			Path:         filePath,
			Package:      "",
			Signature:    sym.Signature,
			CreatedAt:    now,
			SessionID:    s.session.Meta.ID,
			CreatedBy:    0,
			DocRef:       docRefMap[sym.FileID],
		}
	}

	return nodes, idMap, nil
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

// createDocuments creates VersionDocuments from FileNodes.
func (s *SessionIngestion) createDocuments(files []ingestion.FileNode, fileIDMap map[uint32]uint32) []*VersionDocument {
	docs := make([]*VersionDocument, len(files))
	now := time.Now().UnixNano()

	for i, file := range files {
		docs[i] = &VersionDocument{
			ID:        fmt.Sprintf("file_%d", fileIDMap[file.ID]),
			Path:      file.Path,
			Type:      "source_code",
			Content:   "", // Content would come from MappedFile, not FileNode
			Language:  file.Lang,
			IndexedAt: now,
		}
	}

	return docs
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

// IngestWithContent runs ingestion and includes file content in documents.
func (s *SessionIngestion) IngestWithContent(ctx context.Context, rootPath string) (*SessionIngestionResult, error) {
	start := time.Now()

	// Phase 1: Discovery
	files, err := ingestion.DiscoverFiles(ctx, rootPath, nil)
	if err != nil {
		return nil, fmt.Errorf("discover files: %w", err)
	}

	// Phase 2: Read files
	mappedFiles, err := ingestion.ReadFiles(ctx, files, ingestion.WorkerCount())
	if err != nil {
		return nil, fmt.Errorf("read files: %w", err)
	}

	// Phase 3: Parse files
	pool := ingestion.NewParserPool(ingestion.WorkerCount())
	parsed, parseErrors := pool.ParseAll(ctx, mappedFiles)
	_ = parseErrors // Log or handle parse errors

	// Phase 4: Aggregate into CodeGraph
	graph := ingestion.Aggregate(rootPath, mappedFiles, parsed)

	// Phase 5: Convert and write to version stores
	result := &SessionIngestionResult{}

	// Convert and write file nodes
	fileNodes, fileIDMap, filePathMap, docRefMap, err := s.convertFileNodes(graph.Files)
	if err != nil {
		return nil, fmt.Errorf("convert file nodes: %w", err)
	}
	if err := s.nodeStore.WriteBatch(fileNodes); err != nil {
		return nil, fmt.Errorf("write file nodes: %w", err)
	}
	result.NodesCreated += len(fileNodes)
	result.FilesProcessed = len(fileNodes)

	// Convert and write symbol nodes
	symbolNodes, symbolIDMap, err := s.convertSymbolNodes(graph.Symbols, fileIDMap, filePathMap, docRefMap)
	if err != nil {
		return nil, fmt.Errorf("convert symbol nodes: %w", err)
	}
	if err := s.nodeStore.WriteBatch(symbolNodes); err != nil {
		return nil, fmt.Errorf("write symbol nodes: %w", err)
	}
	result.NodesCreated += len(symbolNodes)

	// Generate and write vectors for all nodes (if embedder is set)
	if s.embedder != nil {
		allNodes := append(fileNodes, symbolNodes...)
		vectors, embeddingErrors := s.generateVectors(ctx, allNodes)
		if len(vectors) > 0 {
			if err := s.vectorStore.WriteBatch(vectors); err != nil {
				return nil, fmt.Errorf("write vectors: %w", err)
			}
		}
		result.VectorsCreated = len(vectors)
		result.EmbeddingErrors = embeddingErrors
	}

	// Convert and write edges
	containsEdges := s.convertEdges(graph.ContainsEdges, fileIDMap, symbolIDMap, EdgeTypeContains)
	if err := s.edgeStore.WriteBatch(containsEdges); err != nil {
		return nil, fmt.Errorf("write contains edges: %w", err)
	}
	result.EdgesCreated += len(containsEdges)

	importEdges := s.convertEdges(graph.ImportEdges, fileIDMap, fileIDMap, EdgeTypeImports)
	if err := s.edgeStore.WriteBatch(importEdges); err != nil {
		return nil, fmt.Errorf("write import edges: %w", err)
	}
	result.EdgesCreated += len(importEdges)

	// Create documents WITH content from mapped files
	docs := s.createDocumentsWithContent(mappedFiles, graph.Files, fileIDMap)
	if err := s.docStore.WriteBatch(docs); err != nil {
		return nil, fmt.Errorf("write documents: %w", err)
	}
	result.DocsCreated = len(docs)

	// Index into session Bleve for immediate searchability
	if err := s.indexDocsIntoSessionBleve(ctx, docs); err != nil {
		return nil, fmt.Errorf("index documents in session bleve: %w", err)
	}

	// Update delta tracker and evaluate checkpoint.
	s.updateDeltaTracker(result)
	s.evaluateCheckpoint()

	result.Duration = time.Since(start)
	return result, nil
}

// createDocumentsWithContent creates VersionDocuments with file content.
func (s *SessionIngestion) createDocumentsWithContent(mapped []ingestion.MappedFile, files []ingestion.FileNode, fileIDMap map[uint32]uint32) []*VersionDocument {
	// Build path -> content map
	contentMap := make(map[string]string, len(mapped))
	for _, m := range mapped {
		contentMap[m.Path] = string(m.Data)
	}

	docs := make([]*VersionDocument, len(files))
	now := time.Now().UnixNano()

	for i, file := range files {
		content := contentMap[file.Path]
		docs[i] = &VersionDocument{
			ID:        fmt.Sprintf("file_%d", fileIDMap[file.ID]),
			Path:      file.Path,
			Type:      "source_code",
			Content:   content,
			Language:  file.Lang,
			IndexedAt: now,
		}
	}

	return docs
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
