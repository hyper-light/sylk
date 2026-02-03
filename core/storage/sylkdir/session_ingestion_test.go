package sylkdir

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
)

func TestSessionIngestionIngestCodeGraph(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	graph := &ingestion.CodeGraph{
		Files: []ingestion.FileNode{
			{ID: 1, Path: "/src/main.go", Lang: "go", LineCount: 100, ByteCount: 5000},
			{ID: 2, Path: "/src/util.go", Lang: "go", LineCount: 50, ByteCount: 2500},
		},
		Symbols: []ingestion.SymbolNode{
			{ID: 1, FileID: 1, Name: "main", Kind: ingestion.SymbolKindFunction, StartLine: 10, EndLine: 20, Signature: "func main()"},
			{ID: 2, FileID: 1, Name: "helper", Kind: ingestion.SymbolKindFunction, StartLine: 22, EndLine: 30, Signature: "func helper() error"},
			{ID: 3, FileID: 2, Name: "Util", Kind: ingestion.SymbolKindType, StartLine: 5, EndLine: 15, Signature: "type Util struct"},
		},
		ContainsEdges: []ingestion.Edge{
			{SourceID: 1, TargetID: 1, Kind: ingestion.EdgeKindContains},
			{SourceID: 1, TargetID: 2, Kind: ingestion.EdgeKindContains},
			{SourceID: 2, TargetID: 3, Kind: ingestion.EdgeKindContains},
		},
		ImportEdges: []ingestion.Edge{
			{SourceID: 1, TargetID: 2, Kind: ingestion.EdgeKindImports},
		},
		PathIndex:   make(map[string]uint32),
		SymbolIndex: make(map[string][]uint32),
	}

	contentMap := map[string]string{
		"/src/main.go": "package main\n\nfunc main() {}\n\nfunc helper() error { return nil }\n",
		"/src/util.go": "package util\n\ntype Util struct{}\n",
	}

	si := NewSessionIngestion(sess)
	result, err := si.IngestCodeGraph(context.Background(), graph, contentMap)
	if err != nil {
		t.Fatalf("IngestCodeGraph failed: %v", err)
	}

	if result.FilesProcessed != 2 {
		t.Errorf("Expected 2 files, got %d", result.FilesProcessed)
	}
	// 2 files + 3 symbols (no chunks without embedder)
	if result.NodesCreated < 5 {
		t.Errorf("Expected >= 5 nodes, got %d", result.NodesCreated)
	}
	if result.EdgesCreated < 4 { // 3 contains + 1 import (+ chunk edges)
		t.Errorf("Expected >= 4 edges, got %d", result.EdgesCreated)
	}
	if result.DocsCreated < 2 {
		t.Errorf("Expected >= 2 docs, got %d", result.DocsCreated)
	}

	nodeStore := si.GetNodeStore()
	nodes, err := nodeStore.ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAllFromAncestorChain failed: %v", err)
	}
	if len(nodes) < 5 {
		t.Errorf("Expected >= 5 nodes, got %d", len(nodes))
	}

	edgeStore := si.GetEdgeStore()
	edges, err := edgeStore.ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAllFromAncestorChain edges failed: %v", err)
	}
	if len(edges) < 4 {
		t.Errorf("Expected >= 4 edges, got %d", len(edges))
	}
}

func TestSessionIngestionNodeTypes(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	graph := &ingestion.CodeGraph{
		Files: []ingestion.FileNode{
			{ID: 1, Path: "/src/types.go", Lang: "go"},
		},
		Symbols: []ingestion.SymbolNode{
			{ID: 1, FileID: 1, Name: "Func", Kind: ingestion.SymbolKindFunction},
			{ID: 2, FileID: 1, Name: "Method", Kind: ingestion.SymbolKindMethod},
			{ID: 3, FileID: 1, Name: "Type", Kind: ingestion.SymbolKindType},
			{ID: 4, FileID: 1, Name: "Interface", Kind: ingestion.SymbolKindInterface},
			{ID: 5, FileID: 1, Name: "Const", Kind: ingestion.SymbolKindConst},
			{ID: 6, FileID: 1, Name: "Var", Kind: ingestion.SymbolKindVar},
		},
		ContainsEdges: make([]ingestion.Edge, 0),
		ImportEdges:   make([]ingestion.Edge, 0),
		PathIndex:     make(map[string]uint32),
		SymbolIndex:   make(map[string][]uint32),
	}

	contentMap := map[string]string{
		"/src/types.go": "package types\nfunc Func() {}\nfunc (t T) Method() {}\ntype Type struct{}\ntype Interface interface{}\nconst Const = 1\nvar Var int\n",
	}

	si := NewSessionIngestion(sess)
	_, err = si.IngestCodeGraph(context.Background(), graph, contentMap)
	if err != nil {
		t.Fatalf("IngestCodeGraph failed: %v", err)
	}

	nodeStore := si.GetNodeStore()
	nodes, _ := nodeStore.ReadAllFromAncestorChain()

	typeCount := make(map[NodeType]int)
	for _, n := range nodes {
		typeCount[NodeType(n.NodeType)]++
	}

	if typeCount[NodeTypeFile] != 1 {
		t.Errorf("Expected 1 file node, got %d", typeCount[NodeTypeFile])
	}
	if typeCount[NodeTypeFunction] != 1 {
		t.Errorf("Expected 1 function node, got %d", typeCount[NodeTypeFunction])
	}
	if typeCount[NodeTypeMethod] != 1 {
		t.Errorf("Expected 1 method node, got %d", typeCount[NodeTypeMethod])
	}
	if typeCount[NodeTypeType] != 1 {
		t.Errorf("Expected 1 type node, got %d", typeCount[NodeTypeType])
	}
	if typeCount[NodeTypeInterface] != 1 {
		t.Errorf("Expected 1 interface node, got %d", typeCount[NodeTypeInterface])
	}
	if typeCount[NodeTypeConst] != 1 {
		t.Errorf("Expected 1 const node, got %d", typeCount[NodeTypeConst])
	}
	if typeCount[NodeTypeVar] != 1 {
		t.Errorf("Expected 1 var node, got %d", typeCount[NodeTypeVar])
	}
}

func TestSessionIngestionIndexesIntoBleve(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create session failed: %v", err)
	}
	defer sess.CloseBleve()

	if sess.BleveStore == nil {
		t.Fatal("Expected session to have BleveStore after Create")
	}

	graph := &ingestion.CodeGraph{
		Files: []ingestion.FileNode{
			{ID: 1, Path: "/src/main.go", Lang: "go", LineCount: 100, ByteCount: 5000},
			{ID: 2, Path: "/src/util.go", Lang: "go", LineCount: 50, ByteCount: 2500},
		},
		Symbols:       []ingestion.SymbolNode{},
		ContainsEdges: []ingestion.Edge{},
		ImportEdges:   []ingestion.Edge{},
		PathIndex:     make(map[string]uint32),
		SymbolIndex:   make(map[string][]uint32),
	}

	contentMap := map[string]string{
		"/src/main.go": "package main\nfunc main() {}\n",
		"/src/util.go": "package util\nfunc Help() {}\n",
	}

	si := NewSessionIngestion(sess)
	result, err := si.IngestCodeGraph(context.Background(), graph, contentMap)
	if err != nil {
		t.Fatalf("IngestCodeGraph failed: %v", err)
	}

	if result.DocsCreated < 2 {
		t.Errorf("DocsCreated = %d, want >= 2", result.DocsCreated)
	}

	time.Sleep(100 * time.Millisecond)

	count, err := sess.BleveStore.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount failed: %v", err)
	}
	if count < 2 {
		t.Errorf("Session Bleve DocumentCount = %d, want >= 2", count)
	}
}

func TestSessionIngestionBleveNilSafe(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	sess.CloseBleve()

	graph := &ingestion.CodeGraph{
		Files: []ingestion.FileNode{
			{ID: 1, Path: "/src/main.go", Lang: "go"},
		},
		Symbols:       []ingestion.SymbolNode{},
		ContainsEdges: []ingestion.Edge{},
		ImportEdges:   []ingestion.Edge{},
		PathIndex:     make(map[string]uint32),
		SymbolIndex:   make(map[string][]uint32),
	}

	contentMap := map[string]string{
		"/src/main.go": "package main\n",
	}

	si := NewSessionIngestion(sess)
	result, err := si.IngestCodeGraph(context.Background(), graph, contentMap)
	if err != nil {
		t.Fatalf("IngestCodeGraph failed: %v", err)
	}
	if result.DocsCreated < 1 {
		t.Errorf("DocsCreated = %d, want >= 1", result.DocsCreated)
	}
}

func TestSessionIngestionSymbolCanonicalKeys(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	graph := &ingestion.CodeGraph{
		Files: []ingestion.FileNode{
			{ID: 1, Path: "src/main.go", Lang: "go"},
			{ID: 2, Path: "src/util.go", Lang: "go"},
		},
		Symbols: []ingestion.SymbolNode{
			{ID: 1, FileID: 1, Name: "main", Kind: ingestion.SymbolKindFunction},
			{ID: 2, FileID: 2, Name: "Helper", Kind: ingestion.SymbolKindType},
		},
		ContainsEdges: []ingestion.Edge{},
		ImportEdges:   []ingestion.Edge{},
		PathIndex:     make(map[string]uint32),
		SymbolIndex:   make(map[string][]uint32),
	}

	contentMap := map[string]string{
		"src/main.go": "package main\nfunc main() {}\n",
		"src/util.go": "package util\ntype Helper struct{}\n",
	}

	si := NewSessionIngestion(sess)
	if _, err := si.IngestCodeGraph(context.Background(), graph, contentMap); err != nil {
		t.Fatalf("IngestCodeGraph failed: %v", err)
	}

	nodes, err := si.GetNodeStore().ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	symbolKeys := make(map[string]string)
	for _, n := range nodes {
		if NodeType(n.NodeType) != NodeTypeFile && NodeType(n.NodeType) != NodeTypeChunk {
			symbolKeys[n.Name] = n.CanonicalKey
			if n.Path != "src/main.go" && n.Path != "src/util.go" {
				t.Errorf("symbol %q has Path=%q, want actual file path", n.Name, n.Path)
			}
		}
	}

	if key, ok := symbolKeys["main"]; !ok {
		t.Error("missing symbol node for 'main'")
	} else if key != "symbol:src/main.go:main:function" {
		t.Errorf("main canonical key = %q, want %q", key, "symbol:src/main.go:main:function")
	}

	if key, ok := symbolKeys["Helper"]; !ok {
		t.Error("missing symbol node for 'Helper'")
	} else if key != "symbol:src/util.go:Helper:type" {
		t.Errorf("Helper canonical key = %q, want %q", key, "symbol:src/util.go:Helper:type")
	}
}

func TestSessionIngestionChunksCodeFiles(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create session failed: %v", err)
	}
	defer sess.Close()

	graph := &ingestion.CodeGraph{
		Files: []ingestion.FileNode{
			{ID: 1, Path: "src/main.go", Lang: "go"},
			{ID: 2, Path: "src/util.go", Lang: "go"},
		},
		Symbols: []ingestion.SymbolNode{
			{ID: 1, FileID: 1, Name: "main", Kind: ingestion.SymbolKindFunction, StartLine: 1, EndLine: 5},
			{ID: 2, FileID: 1, Name: "helper", Kind: ingestion.SymbolKindFunction, StartLine: 7, EndLine: 12},
		},
		ContainsEdges: []ingestion.Edge{
			{SourceID: 1, TargetID: 1, Kind: ingestion.EdgeKindContains},
			{SourceID: 1, TargetID: 2, Kind: ingestion.EdgeKindContains},
		},
		ImportEdges: []ingestion.Edge{},
		PathIndex:   make(map[string]uint32),
		SymbolIndex: make(map[string][]uint32),
	}

	contentMap := map[string]string{
		"src/main.go": "func main() {\n\tfmt.Println(\"hello\")\n}\n\nfunc helper() error {\n\treturn nil\n}\n",
		"src/util.go": "package util\n\n// Utility functions for the project.\nfunc DoSomething() {}\n",
	}

	si := NewSessionIngestion(sess)
	mock := &mockBatchEmbedder{dim: 4, maxBytes: 200}
	si.SetEmbedder(mock)

	result, err := si.IngestCodeGraph(context.Background(), graph, contentMap)
	if err != nil {
		t.Fatalf("IngestCodeGraph: %v", err)
	}

	if result.NodesCreated < 4 {
		t.Errorf("NodesCreated = %d, want >= 4 (2 files + 2 symbols + chunks)", result.NodesCreated)
	}
	if result.ChunkRefsCreated == 0 {
		t.Error("ChunkRefsCreated = 0, want > 0")
	}

	nodes, err := si.GetNodeStore().ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAllFromAncestorChain: %v", err)
	}

	var chunkCount int
	for _, n := range nodes {
		if NodeType(n.NodeType) == NodeTypeChunk {
			chunkCount++
			if n.DocRef == 0 {
				t.Errorf("chunk node %d has DocRef=0", n.ID)
			}
		}
	}
	if chunkCount == 0 {
		t.Error("no chunk nodes found")
	}

	refs, err := si.GetChunkRefStore().ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAllFromAncestorChain chunk refs: %v", err)
	}
	if len(refs) != result.ChunkRefsCreated {
		t.Errorf("chunk refs on disk = %d, ChunkRefsCreated = %d", len(refs), result.ChunkRefsCreated)
	}

	edges, err := si.GetEdgeStore().ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAllFromAncestorChain edges: %v", err)
	}
	var containsChunkEdges int
	for _, e := range edges {
		if EdgeType(e.Type) == EdgeTypeContainsChunk {
			containsChunkEdges++
		}
	}
	if containsChunkEdges == 0 {
		t.Error("no ContainsChunk edges found")
	}
	if result.VectorsCreated == 0 {
		t.Error("VectorsCreated = 0, want > 0")
	}
	if result.DocsCreated < 2 {
		t.Errorf("DocsCreated = %d, want >= 2", result.DocsCreated)
	}
}

func TestSessionIngestionChunkRefsValid(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create session failed: %v", err)
	}
	defer sess.Close()

	graph := &ingestion.CodeGraph{
		Files: []ingestion.FileNode{
			{ID: 1, Path: "src/big.go", Lang: "go"},
		},
		Symbols:       []ingestion.SymbolNode{},
		ContainsEdges: []ingestion.Edge{},
		ImportEdges:   []ingestion.Edge{},
		PathIndex:     make(map[string]uint32),
		SymbolIndex:   make(map[string][]uint32),
	}

	content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"
	contentMap := map[string]string{"src/big.go": content}

	si := NewSessionIngestion(sess)
	mock := &mockBatchEmbedder{dim: 4, maxBytes: 20}
	si.SetEmbedder(mock)

	result, err := si.IngestCodeGraph(context.Background(), graph, contentMap)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	refs, err := si.GetChunkRefStore().ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("read refs: %v", err)
	}
	if len(refs) != result.ChunkRefsCreated {
		t.Fatalf("refs count %d != result.ChunkRefsCreated %d", len(refs), result.ChunkRefsCreated)
	}

	for _, ref := range refs {
		if ref.Span.ByteEnd <= ref.Span.ByteStart {
			t.Errorf("chunk %d: ByteEnd (%d) <= ByteStart (%d)", ref.NodeID, ref.Span.ByteEnd, ref.Span.ByteStart)
		}
		if ref.Span.ByteEnd > uint32(len(content)) {
			t.Errorf("chunk %d: ByteEnd (%d) > content len (%d)", ref.NodeID, ref.Span.ByteEnd, len(content))
		}
		slice := content[ref.Span.ByteStart:ref.Span.ByteEnd]
		if len(slice) == 0 {
			t.Errorf("chunk %d: empty byte range", ref.NodeID)
		}
	}
}

func TestSessionIngestionNodeIDsFromSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, &BaseSnapshot{
		CommittedSessions: []uint32{},
		SnapshotAt:        time.Now(),
		NextNodeID:        1000,
	})
	if err != nil {
		t.Fatalf("Create session failed: %v", err)
	}
	defer sess.Close()

	graph := &ingestion.CodeGraph{
		Files: []ingestion.FileNode{
			{ID: 1, Path: "src/main.go", Lang: "go"},
		},
		Symbols: []ingestion.SymbolNode{
			{ID: 1, FileID: 1, Name: "main", Kind: ingestion.SymbolKindFunction},
		},
		ContainsEdges: []ingestion.Edge{},
		ImportEdges:   []ingestion.Edge{},
		PathIndex:     make(map[string]uint32),
		SymbolIndex:   make(map[string][]uint32),
	}

	contentMap := map[string]string{
		"src/main.go": "package main\nfunc main() {}\n",
	}

	si := NewSessionIngestion(sess)
	_, err = si.IngestCodeGraph(context.Background(), graph, contentMap)
	if err != nil {
		t.Fatalf("IngestCodeGraph: %v", err)
	}

	nodes, err := si.GetNodeStore().ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	for _, n := range nodes {
		if n.ID < 1000 {
			t.Errorf("node %q has ID %d, want >= 1000", n.Name, n.ID)
		}
	}
}

func TestSessionIngestionChunkDocsInBleve(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		t.Fatalf("Create session failed: %v", err)
	}
	defer sess.Close()

	if sess.BleveStore == nil {
		t.Fatal("Expected BleveStore after Create")
	}

	graph := &ingestion.CodeGraph{
		Files: []ingestion.FileNode{
			{ID: 1, Path: "src/main.go", Lang: "go"},
		},
		Symbols:       []ingestion.SymbolNode{},
		ContainsEdges: []ingestion.Edge{},
		ImportEdges:   []ingestion.Edge{},
		PathIndex:     make(map[string]uint32),
		SymbolIndex:   make(map[string][]uint32),
	}

	content := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello world\")\n}\n"
	contentMap := map[string]string{"src/main.go": content}

	si := NewSessionIngestion(sess)
	mock := &mockBatchEmbedder{dim: 4, maxBytes: 200}
	si.SetEmbedder(mock)

	result, err := si.IngestCodeGraph(context.Background(), graph, contentMap)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	count, err := sess.BleveStore.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount: %v", err)
	}
	if count < uint64(result.DocsCreated) {
		t.Errorf("Bleve doc count = %d, want >= %d", count, result.DocsCreated)
	}
}

func BenchmarkSessionIngestionCodeGraph(b *testing.B) {
	files := make([]ingestion.FileNode, 100)
	symbols := make([]ingestion.SymbolNode, 0, 1000)
	containsEdges := make([]ingestion.Edge, 0, 1000)
	contentMap := make(map[string]string, 100)

	symbolID := uint32(1)
	for i := range files {
		path := fmt.Sprintf("/src/file%d.go", i)
		files[i] = ingestion.FileNode{
			ID:        uint32(i + 1),
			Path:      path,
			Lang:      "go",
			LineCount: 100,
			ByteCount: 5000,
		}
		contentMap[path] = fmt.Sprintf("package file%d\nfunc init() {}\n", i)

		for j := 0; j < 10; j++ {
			symbols = append(symbols, ingestion.SymbolNode{
				ID:        symbolID,
				FileID:    uint32(i + 1),
				Name:      "func" + string(rune('A'+j)),
				Kind:      ingestion.SymbolKindFunction,
				StartLine: uint32(j * 10),
				EndLine:   uint32(j*10 + 8),
				Signature: "func() error",
			})
			containsEdges = append(containsEdges, ingestion.Edge{
				SourceID: uint32(i + 1),
				TargetID: symbolID,
				Kind:     ingestion.EdgeKindContains,
			})
			symbolID++
		}
	}

	graph := &ingestion.CodeGraph{
		Files:         files,
		Symbols:       symbols,
		ContainsEdges: containsEdges,
		ImportEdges:   make([]ingestion.Edge, 0),
		PathIndex:     make(map[string]uint32),
		SymbolIndex:   make(map[string][]uint32),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		sd := New(tmpDir)
		sd.Init()

		store := NewSessionStore(sd)
		sess, _ := store.Create(uint32(i+1), nil)

		si := NewSessionIngestion(sess)
		si.IngestCodeGraph(context.Background(), graph, contentMap)
	}
}
