package vectorgraphdb

import (
	"testing"
)

func TestEdgeStoreInsertAndGet(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	node1 := &GraphNode{ID: "node1", Domain: DomainCode, NodeType: NodeTypeFile}
	node2 := &GraphNode{ID: "node2", Domain: DomainCode, NodeType: NodeTypeFunction}
	ns.InsertNode(node1, []float32{1, 0, 0})
	ns.InsertNode(node2, []float32{0, 1, 0})

	edge := &GraphEdge{
		SourceID: "node1",
		TargetID: "node2",
		EdgeType: EdgeTypeDefines,
		Weight:   0.9,
		Metadata: map[string]any{"line": 42},
	}

	err := es.InsertEdge(edge)
	if err != nil {
		t.Fatalf("InsertEdge: %v", err)
	}

	retrieved, err := es.GetEdge(edge.ID)
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}

	if retrieved.ID != edge.ID {
		t.Errorf("ID = %d, want %d", retrieved.ID, edge.ID)
	}
	if retrieved.SourceID != edge.SourceID {
		t.Errorf("SourceID = %s, want %s", retrieved.SourceID, edge.SourceID)
	}
	if retrieved.EdgeType != edge.EdgeType {
		t.Errorf("EdgeType = %s, want %s", retrieved.EdgeType, edge.EdgeType)
	}
	if retrieved.Weight != edge.Weight {
		t.Errorf("Weight = %f, want %f", retrieved.Weight, edge.Weight)
	}
}

func TestEdgeStoreInsertAutoID(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "n2", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0, 1, 0})

	edge := &GraphEdge{
		SourceID: "n1",
		TargetID: "n2",
		EdgeType: EdgeTypeCalls,
	}

	err := es.InsertEdge(edge)
	if err != nil {
		t.Fatalf("InsertEdge: %v", err)
	}

	if edge.ID == 0 {
		t.Error("Auto-generated ID should not be empty")
	}
}

func TestEdgeStoreInsertInvalidEdgeType(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	es := NewEdgeStore(db)

	edge := &GraphEdge{
		SourceID: "n1",
		TargetID: "n2",
		EdgeType: EdgeType(-1),
	}

	err := es.InsertEdge(edge)
	if err != ErrInvalidEdgeType {
		t.Errorf("InsertEdge: got %v, want ErrInvalidEdgeType", err)
	}
}

func TestEdgeStoreInsertMissingFromNode(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n2", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0, 1, 0})

	edge := &GraphEdge{
		SourceID: "nonexistent",
		TargetID: "n2",
		EdgeType: EdgeTypeCalls,
	}

	err := es.InsertEdge(edge)
	if err == nil {
		t.Error("InsertEdge should fail with missing from node")
	}
}

func TestEdgeStoreInsertMissingToNode(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})

	edge := &GraphEdge{
		SourceID: "n1",
		TargetID: "nonexistent",
		EdgeType: EdgeTypeCalls,
	}

	err := es.InsertEdge(edge)
	if err == nil {
		t.Error("InsertEdge should fail with missing to node")
	}
}

func TestEdgeStoreInsertValidCrossDomain(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "code1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "academic1", Domain: DomainAcademic, NodeType: NodeTypeDocumentation}, []float32{0, 1, 0})

	edge := &GraphEdge{
		SourceID: "code1",
		TargetID: "academic1",
		EdgeType: EdgeTypeReferences,
	}

	err := es.InsertEdge(edge)
	if err != nil {
		t.Fatalf("InsertEdge valid cross-domain: %v", err)
	}
}

func TestEdgeStoreInsertInvalidCrossDomain(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "code1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "academic1", Domain: DomainAcademic, NodeType: NodeTypeDocumentation}, []float32{0, 1, 0})

	edge := &GraphEdge{
		SourceID: "code1",
		TargetID: "academic1",
		EdgeType: EdgeTypeCalls,
	}

	err := es.InsertEdge(edge)
	if err != ErrInvalidCrossDomain {
		t.Errorf("InsertEdge: got %v, want ErrInvalidCrossDomain", err)
	}
}

func TestEdgeStoreGetNotFound(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	es := NewEdgeStore(db)

	_, err := es.GetEdge(0)
	if err != ErrEdgeNotFound {
		t.Errorf("GetEdge: got %v, want ErrEdgeNotFound", err)
	}
}

func TestEdgeStoreGetEdgesBetween(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "n2", Domain: DomainCode, NodeType: NodeTypeFunction}, []float32{0, 1, 0})

	es.InsertEdge(&GraphEdge{SourceID: "n1", TargetID: "n2", EdgeType: EdgeTypeDefines})
	es.InsertEdge(&GraphEdge{SourceID: "n1", TargetID: "n2", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{SourceID: "n2", TargetID: "n1", EdgeType: EdgeTypeCalls})

	edges, err := es.GetEdgesBetween("n1", "n2")
	if err != nil {
		t.Fatalf("GetEdgesBetween: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("Got %d edges, want 2", len(edges))
	}
}

func TestEdgeStoreGetOutgoingEdges(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "n2", Domain: DomainCode, NodeType: NodeTypeFunction}, []float32{0, 1, 0})
	ns.InsertNode(&GraphNode{ID: "n3", Domain: DomainCode, NodeType: NodeTypeFunction}, []float32{0, 0, 1})

	es.InsertEdge(&GraphEdge{SourceID: "n1", TargetID: "n2", EdgeType: EdgeTypeDefines})
	es.InsertEdge(&GraphEdge{SourceID: "n1", TargetID: "n3", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{SourceID: "n2", TargetID: "n3", EdgeType: EdgeTypeCalls})

	edges, err := es.GetOutgoingEdges("n1")
	if err != nil {
		t.Fatalf("GetOutgoingEdges: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("Got %d outgoing edges, want 2", len(edges))
	}
}

func TestEdgeStoreGetOutgoingEdgesFiltered(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "n2", Domain: DomainCode, NodeType: NodeTypeFunction}, []float32{0, 1, 0})
	ns.InsertNode(&GraphNode{ID: "n3", Domain: DomainCode, NodeType: NodeTypeFunction}, []float32{0, 0, 1})

	es.InsertEdge(&GraphEdge{SourceID: "n1", TargetID: "n2", EdgeType: EdgeTypeDefines})
	es.InsertEdge(&GraphEdge{SourceID: "n1", TargetID: "n3", EdgeType: EdgeTypeCalls})

	edges, err := es.GetOutgoingEdges("n1", EdgeTypeDefines)
	if err != nil {
		t.Fatalf("GetOutgoingEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("Got %d outgoing edges, want 1", len(edges))
	}
	if edges[0].EdgeType != EdgeTypeDefines {
		t.Errorf("EdgeType = %s, want defines", edges[0].EdgeType)
	}
}

func TestEdgeStoreGetIncomingEdges(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "n2", Domain: DomainCode, NodeType: NodeTypeFunction}, []float32{0, 1, 0})
	ns.InsertNode(&GraphNode{ID: "n3", Domain: DomainCode, NodeType: NodeTypeFunction}, []float32{0, 0, 1})

	es.InsertEdge(&GraphEdge{SourceID: "n1", TargetID: "n3", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{SourceID: "n2", TargetID: "n3", EdgeType: EdgeTypeCalls})

	edges, err := es.GetIncomingEdges("n3")
	if err != nil {
		t.Fatalf("GetIncomingEdges: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("Got %d incoming edges, want 2", len(edges))
	}
}

func TestEdgeStoreGetIncomingEdgesFiltered(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "n2", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0, 1, 0})
	ns.InsertNode(&GraphNode{ID: "n3", Domain: DomainCode, NodeType: NodeTypeFunction}, []float32{0, 0, 1})

	es.InsertEdge(&GraphEdge{SourceID: "n1", TargetID: "n3", EdgeType: EdgeTypeDefines})
	es.InsertEdge(&GraphEdge{SourceID: "n2", TargetID: "n3", EdgeType: EdgeTypeCalls})

	edges, err := es.GetIncomingEdges("n3", EdgeTypeDefines)
	if err != nil {
		t.Fatalf("GetIncomingEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("Got %d incoming edges, want 1", len(edges))
	}
}

func TestEdgeStoreDeleteEdge(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "n2", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0, 1, 0})

	edge := &GraphEdge{SourceID: "n1", TargetID: "n2", EdgeType: EdgeTypeCalls}
	es.InsertEdge(edge)

	err := es.DeleteEdge(edge.ID)
	if err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}

	_, err = es.GetEdge(edge.ID)
	if err != ErrEdgeNotFound {
		t.Error("Edge still exists after delete")
	}
}

func TestEdgeStoreDeleteEdgeNotFound(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	es := NewEdgeStore(db)

	err := es.DeleteEdge(0)
	if err != ErrEdgeNotFound {
		t.Errorf("DeleteEdge: got %v, want ErrEdgeNotFound", err)
	}
}

func TestEdgeStoreDeleteEdgesBetween(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "n2", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0, 1, 0})

	es.InsertEdge(&GraphEdge{SourceID: "n1", TargetID: "n2", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{SourceID: "n1", TargetID: "n2", EdgeType: EdgeTypeDefines})

	err := es.DeleteEdgesBetween("n1", "n2")
	if err != nil {
		t.Fatalf("DeleteEdgesBetween: %v", err)
	}

	edges, _ := es.GetEdgesBetween("n1", "n2")
	if len(edges) != 0 {
		t.Errorf("Got %d edges after delete, want 0", len(edges))
	}
}

func TestEdgeStoreCascadeDelete(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "n1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0, 0})
	ns.InsertNode(&GraphNode{ID: "n2", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0, 1, 0})

	edge := &GraphEdge{SourceID: "n1", TargetID: "n2", EdgeType: EdgeTypeCalls}
	es.InsertEdge(edge)

	ns.DeleteNode("n1")

	_, err := es.GetEdge(edge.ID)
	if err != ErrEdgeNotFound {
		t.Error("Edge should be cascade deleted when node is deleted")
	}
}
