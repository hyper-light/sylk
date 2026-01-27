package sylkdir

import (
	"context"
	"testing"

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

	// Create a mock CodeGraph
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
			{SourceID: 1, TargetID: 1, Kind: ingestion.EdgeKindContains}, // main.go contains main
			{SourceID: 1, TargetID: 2, Kind: ingestion.EdgeKindContains}, // main.go contains helper
			{SourceID: 2, TargetID: 3, Kind: ingestion.EdgeKindContains}, // util.go contains Util
		},
		ImportEdges: []ingestion.Edge{
			{SourceID: 1, TargetID: 2, Kind: ingestion.EdgeKindImports}, // main.go imports util.go
		},
		PathIndex:   make(map[string]uint32),
		SymbolIndex: make(map[string][]uint32),
	}

	// Ingest the graph
	si := NewSessionIngestion(sess)
	result, err := si.IngestCodeGraph(context.Background(), graph)
	if err != nil {
		t.Fatalf("IngestCodeGraph failed: %v", err)
	}

	// Verify results
	if result.FilesProcessed != 2 {
		t.Errorf("Expected 2 files, got %d", result.FilesProcessed)
	}
	if result.NodesCreated != 5 { // 2 files + 3 symbols
		t.Errorf("Expected 5 nodes, got %d", result.NodesCreated)
	}
	if result.EdgesCreated != 4 { // 3 contains + 1 import
		t.Errorf("Expected 4 edges, got %d", result.EdgesCreated)
	}
	if result.DocsCreated != 2 {
		t.Errorf("Expected 2 docs, got %d", result.DocsCreated)
	}

	// Verify we can read back the data
	nodeStore := si.GetNodeStore()
	nodes, err := nodeStore.ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAllFromAncestorChain failed: %v", err)
	}
	if len(nodes) != 5 {
		t.Errorf("Expected 5 nodes, got %d", len(nodes))
	}

	edgeStore := si.GetEdgeStore()
	edges, err := edgeStore.ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAllFromAncestorChain edges failed: %v", err)
	}
	if len(edges) != 4 {
		t.Errorf("Expected 4 edges, got %d", len(edges))
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

	// Create graph with all symbol kinds
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

	si := NewSessionIngestion(sess)
	_, err = si.IngestCodeGraph(context.Background(), graph)
	if err != nil {
		t.Fatalf("IngestCodeGraph failed: %v", err)
	}

	// Verify node types are preserved
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

func BenchmarkSessionIngestionCodeGraph(b *testing.B) {
	// Create a moderately sized CodeGraph
	files := make([]ingestion.FileNode, 100)
	symbols := make([]ingestion.SymbolNode, 0, 1000)
	containsEdges := make([]ingestion.Edge, 0, 1000)

	symbolID := uint32(1)
	for i := range files {
		files[i] = ingestion.FileNode{
			ID:        uint32(i + 1),
			Path:      "/src/file" + string(rune('0'+i%10)) + ".go",
			Lang:      "go",
			LineCount: 100,
			ByteCount: 5000,
		}
		// Add 10 symbols per file
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
		si.IngestCodeGraph(context.Background(), graph)
	}
}
