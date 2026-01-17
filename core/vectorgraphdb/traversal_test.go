package vectorgraphdb

import (
	"sync"
	"testing"
)

func TestGraphTraverserGetNeighbors(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{3, 0.1, 0.1})

	es.InsertEdge(&GraphEdge{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{ID: "e2", FromNodeID: "A", ToNodeID: "C", EdgeType: EdgeTypeCalls})

	gt := NewGraphTraverser(db)
	neighbors, err := gt.GetNeighbors("A", nil)
	if err != nil {
		t.Fatalf("GetNeighbors: %v", err)
	}

	if len(neighbors) != 2 {
		t.Errorf("Got %d neighbors, want 2", len(neighbors))
	}
}

func TestGraphTraverserGetNeighborsOutgoing(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{3, 0.1, 0.1})

	es.InsertEdge(&GraphEdge{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{ID: "e2", FromNodeID: "C", ToNodeID: "A", EdgeType: EdgeTypeCalls})

	gt := NewGraphTraverser(db)
	neighbors, err := gt.GetNeighbors("A", &TraversalOptions{Direction: DirectionOutgoing})
	if err != nil {
		t.Fatalf("GetNeighbors: %v", err)
	}

	if len(neighbors) != 1 {
		t.Errorf("Got %d outgoing neighbors, want 1", len(neighbors))
	}
	if len(neighbors) > 0 && neighbors[0].ID != "B" {
		t.Errorf("Wrong outgoing neighbor: %s", neighbors[0].ID)
	}
}

func TestGraphTraverserGetNeighborsIncoming(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{3, 0.1, 0.1})

	es.InsertEdge(&GraphEdge{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{ID: "e2", FromNodeID: "C", ToNodeID: "A", EdgeType: EdgeTypeCalls})

	gt := NewGraphTraverser(db)
	neighbors, err := gt.GetNeighbors("A", &TraversalOptions{Direction: DirectionIncoming})
	if err != nil {
		t.Fatalf("GetNeighbors: %v", err)
	}

	if len(neighbors) != 1 {
		t.Errorf("Got %d incoming neighbors, want 1", len(neighbors))
	}
	if len(neighbors) > 0 && neighbors[0].ID != "C" {
		t.Errorf("Wrong incoming neighbor: %s", neighbors[0].ID)
	}
}

func TestGraphTraverserGetNeighborsFilterByEdgeType(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypePackage}, []float32{3, 0.1, 0.1})

	es.InsertEdge(&GraphEdge{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{ID: "e2", FromNodeID: "A", ToNodeID: "C", EdgeType: EdgeTypeImports})

	gt := NewGraphTraverser(db)
	neighbors, err := gt.GetNeighbors("A", &TraversalOptions{
		EdgeTypes: []EdgeType{EdgeTypeCalls},
		Direction: DirectionBoth,
	})
	if err != nil {
		t.Fatalf("GetNeighbors: %v", err)
	}

	if len(neighbors) != 1 {
		t.Errorf("Got %d filtered neighbors, want 1", len(neighbors))
	}
}

func TestGraphTraverserShortestPath(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{3, 0.1, 0.1})

	es.InsertEdge(&GraphEdge{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{ID: "e2", FromNodeID: "B", ToNodeID: "C", EdgeType: EdgeTypeCalls})

	gt := NewGraphTraverser(db)
	p, err := gt.ShortestPath("A", "C", nil)
	if err != nil {
		t.Fatalf("ShortestPath: %v", err)
	}

	if len(p.Nodes) != 3 {
		t.Errorf("Path has %d nodes, want 3", len(p.Nodes))
	}
	if len(p.Edges) != 2 {
		t.Errorf("Path has %d edges, want 2", len(p.Edges))
	}
}

func TestGraphTraverserShortestPathNotFound(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})

	gt := NewGraphTraverser(db)
	_, err := gt.ShortestPath("A", "B", nil)
	if err != ErrPathNotFound {
		t.Errorf("Expected ErrPathNotFound, got %v", err)
	}
}

func TestGraphTraverserShortestPathMaxDepth(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{3, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "D", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{4, 0.1, 0.1})

	es.InsertEdge(&GraphEdge{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{ID: "e2", FromNodeID: "B", ToNodeID: "C", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{ID: "e3", FromNodeID: "C", ToNodeID: "D", EdgeType: EdgeTypeCalls})

	gt := NewGraphTraverser(db)
	_, err := gt.ShortestPath("A", "D", &TraversalOptions{MaxDepth: 2})
	if err != ErrPathNotFound {
		t.Errorf("Expected ErrPathNotFound with MaxDepth=2, got %v", err)
	}
}

func TestGraphTraverserGetConnectedComponent(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{3, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "D", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{4, 0.1, 0.1})

	es.InsertEdge(&GraphEdge{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{ID: "e2", FromNodeID: "B", ToNodeID: "C", EdgeType: EdgeTypeCalls})

	gt := NewGraphTraverser(db)
	component, err := gt.GetConnectedComponent("A", nil)
	if err != nil {
		t.Fatalf("GetConnectedComponent: %v", err)
	}

	if len(component) != 3 {
		t.Errorf("Component has %d nodes, want 3", len(component))
	}
}

func TestGraphTraverserGetSubgraph(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{3, 0.1, 0.1})

	es.InsertEdge(&GraphEdge{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{ID: "e2", FromNodeID: "B", ToNodeID: "C", EdgeType: EdgeTypeCalls})

	gt := NewGraphTraverser(db)
	subgraph, err := gt.GetSubgraph("A", 2)
	if err != nil {
		t.Fatalf("GetSubgraph: %v", err)
	}

	if len(subgraph.Nodes) != 3 {
		t.Errorf("Subgraph has %d nodes, want 3", len(subgraph.Nodes))
	}
	if len(subgraph.Edges) != 2 {
		t.Errorf("Subgraph has %d edges, want 2", len(subgraph.Edges))
	}
}

func TestGraphTraverserGetSubgraphLimitedDepth(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{3, 0.1, 0.1})

	es.InsertEdge(&GraphEdge{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{ID: "e2", FromNodeID: "B", ToNodeID: "C", EdgeType: EdgeTypeCalls})

	gt := NewGraphTraverser(db)
	subgraph, err := gt.GetSubgraph("A", 1)
	if err != nil {
		t.Fatalf("GetSubgraph: %v", err)
	}

	if len(subgraph.Nodes) != 2 {
		t.Errorf("Subgraph with depth 1 has %d nodes, want 2", len(subgraph.Nodes))
	}
}

func TestGraphTraverserConcurrent(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	for i := 0; i < 10; i++ {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	for i := 0; i < 9; i++ {
		es.InsertEdge(&GraphEdge{
			ID:         "e" + nodeID(i),
			FromNodeID: nodeID(i),
			ToNodeID:   nodeID(i + 1),
			EdgeType:   EdgeTypeCalls,
		})
	}

	gt := NewGraphTraverser(db)

	var wg sync.WaitGroup
	errChan := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := gt.GetNeighbors(nodeID(0), nil)
			if err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("Concurrent traversal error: %v", err)
	}
}

func TestGraphTraverserEmptyGraph(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})

	gt := NewGraphTraverser(db)
	neighbors, err := gt.GetNeighbors("A", nil)
	if err != nil {
		t.Fatalf("GetNeighbors: %v", err)
	}

	if len(neighbors) != 0 {
		t.Errorf("Got %d neighbors, want 0", len(neighbors))
	}
}

func TestGraphTraverserDirectPath(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{2, 0.1, 0.1})

	es.InsertEdge(&GraphEdge{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: EdgeTypeCalls})

	gt := NewGraphTraverser(db)
	p, err := gt.ShortestPath("A", "B", nil)
	if err != nil {
		t.Fatalf("ShortestPath: %v", err)
	}

	if len(p.Nodes) != 2 {
		t.Errorf("Direct path has %d nodes, want 2", len(p.Nodes))
	}
}
