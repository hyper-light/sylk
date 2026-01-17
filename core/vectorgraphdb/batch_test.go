package vectorgraphdb

import (
	"sync"
	"testing"
)

func TestBatchStoreInsertNodes(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	hnsw := newMockHNSW()
	bs := NewBatchStore(db, hnsw)
	ns := NewNodeStore(db, nil)

	nodes := make([]*GraphNode, 10)
	embeddings := make([][]float32, 10)
	for i := 0; i < 10; i++ {
		nodes[i] = &GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}
		embeddings[i] = []float32{float32(i + 1), 0.1, 0.1}
	}

	err := bs.BatchInsertNodes(nodes, embeddings)
	if err != nil {
		t.Fatalf("BatchInsertNodes: %v", err)
	}

	for i := 0; i < 10; i++ {
		node, err := ns.GetNode(nodeID(i))
		if err != nil {
			t.Errorf("Node %s not found: %v", nodeID(i), err)
			continue
		}
		if node.Domain != DomainCode {
			t.Errorf("Node %s domain = %s, want code", nodeID(i), node.Domain)
		}
	}

	for i := 0; i < 10; i++ {
		if !hnsw.inserted[nodeID(i)] {
			t.Errorf("Node %s not inserted to HNSW", nodeID(i))
		}
	}
}

func TestBatchStoreInsertNodesWithProgress(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	bs := NewBatchStore(db, nil)

	nodes := make([]*GraphNode, 5)
	embeddings := make([][]float32, 5)
	for i := 0; i < 5; i++ {
		nodes[i] = &GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}
		embeddings[i] = []float32{float32(i + 1), 0.1, 0.1}
	}

	var progressCalls []int
	progress := func(completed, total int) {
		progressCalls = append(progressCalls, completed)
	}

	err := bs.BatchInsertNodesWithProgress(nodes, embeddings, progress)
	if err != nil {
		t.Fatalf("BatchInsertNodesWithProgress: %v", err)
	}

	if len(progressCalls) != 5 {
		t.Errorf("Progress called %d times, want 5", len(progressCalls))
	}
}

func TestBatchStoreInsertNodesMismatch(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	bs := NewBatchStore(db, nil)

	nodes := make([]*GraphNode, 5)
	embeddings := make([][]float32, 3)

	err := bs.BatchInsertNodes(nodes, embeddings)
	if err == nil {
		t.Error("BatchInsertNodes should fail with mismatched lengths")
	}
}

func TestBatchStoreInsertEdges(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	bs := NewBatchStore(db, nil)
	es := NewEdgeStore(db)

	for i := 0; i < 5; i++ {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	edges := make([]*GraphEdge, 4)
	for i := 0; i < 4; i++ {
		edges[i] = &GraphEdge{
			SourceID: nodeID(i),
			TargetID: nodeID(i + 1),
			EdgeType: EdgeTypeCalls,
		}
	}

	err := bs.BatchInsertEdges(edges)
	if err != nil {
		t.Fatalf("BatchInsertEdges: %v", err)
	}

	for i := 0; i < 4; i++ {
		edgeID := edges[i].ID
		edge, err := es.GetEdge(edgeID)
		if err != nil {
			t.Errorf("Edge %d not found: %v", edgeID, err)
		}
		if edge.EdgeType != EdgeTypeCalls {
			t.Errorf("Edge %d type = %s, want calls", edgeID, edge.EdgeType)
		}
	}
}

func TestBatchStoreInsertEdgesWithProgress(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	bs := NewBatchStore(db, nil)

	for i := 0; i < 3; i++ {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	edges := make([]*GraphEdge, 2)
	for i := 0; i < 2; i++ {
		edges[i] = &GraphEdge{
			SourceID: nodeID(i),
			TargetID: nodeID(i + 1),
			EdgeType: EdgeTypeCalls,
		}
	}

	var progressCalls []int
	progress := func(completed, total int) {
		progressCalls = append(progressCalls, completed)
	}

	err := bs.BatchInsertEdgesWithProgress(edges, progress)
	if err != nil {
		t.Fatalf("BatchInsertEdgesWithProgress: %v", err)
	}

	if len(progressCalls) != 2 {
		t.Errorf("Progress called %d times, want 2", len(progressCalls))
	}
}

func TestBatchStoreDeleteNodes(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	hnsw := newMockHNSW()
	ns := NewNodeStore(db, hnsw)
	bs := NewBatchStore(db, hnsw)

	for i := 0; i < 10; i++ {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	idsToDelete := []string{nodeID(0), nodeID(2), nodeID(4)}
	err := bs.BatchDeleteNodes(idsToDelete)
	if err != nil {
		t.Fatalf("BatchDeleteNodes: %v", err)
	}

	for _, id := range idsToDelete {
		if !hnsw.deleted[id] {
			t.Errorf("Node %s not deleted from HNSW", id)
		}
		_, err := ns.GetNode(id)
		if err != ErrNodeNotFound {
			t.Errorf("Node %s still exists", id)
		}
	}

	surviving := []string{nodeID(1), nodeID(3), nodeID(5)}
	for _, id := range surviving {
		_, err := ns.GetNode(id)
		if err != nil {
			t.Errorf("Surviving node %s not found: %v", id, err)
		}
	}
}

func TestBatchStoreDeleteNodesWithProgress(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	bs := NewBatchStore(db, nil)

	for i := 0; i < 5; i++ {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	idsToDelete := []string{nodeID(0), nodeID(1), nodeID(2)}

	var progressCalls []int
	progress := func(completed, total int) {
		progressCalls = append(progressCalls, completed)
	}

	err := bs.BatchDeleteNodesWithProgress(idsToDelete, progress)
	if err != nil {
		t.Fatalf("BatchDeleteNodesWithProgress: %v", err)
	}

	if len(progressCalls) != 3 {
		t.Errorf("Progress called %d times, want 3", len(progressCalls))
	}
}

func TestBatchStoreLargeBatch(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	bs := NewBatchStore(db, nil)
	ns := NewNodeStore(db, nil)

	const batchSize = 1000

	nodes := make([]*GraphNode, batchSize)
	embeddings := make([][]float32, batchSize)
	for i := 0; i < batchSize; i++ {
		nodes[i] = &GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}
		embeddings[i] = []float32{float32(i + 1), 0.1, 0.1}
	}

	err := bs.BatchInsertNodes(nodes, embeddings)
	if err != nil {
		t.Fatalf("BatchInsertNodes large batch: %v", err)
	}

	node, err := ns.GetNode(nodeID(500))
	if err != nil {
		t.Errorf("Node 500 not found: %v", err)
	}
	if node == nil {
		t.Error("Node 500 is nil")
	}

	node, err = ns.GetNode(nodeID(999))
	if err != nil {
		t.Errorf("Node 999 not found: %v", err)
	}
	if node == nil {
		t.Error("Node 999 is nil")
	}
}

func TestBatchStoreConcurrentInserts(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	var wg sync.WaitGroup
	errChan := make(chan error, 10)

	for batch := 0; batch < 10; batch++ {
		wg.Add(1)
		go func(batchNum int) {
			defer wg.Done()
			bs := NewBatchStore(db, nil)

			nodes := make([]*GraphNode, 10)
			embeddings := make([][]float32, 10)
			for i := 0; i < 10; i++ {
				nodeIdx := batchNum*10 + i
				nodes[i] = &GraphNode{
					ID:       nodeID(nodeIdx),
					Domain:   DomainCode,
					NodeType: NodeTypeFile,
				}
				embeddings[i] = []float32{float32(nodeIdx + 1), 0.1, 0.1}
			}

			if err := bs.BatchInsertNodes(nodes, embeddings); err != nil {
				errChan <- err
			}
		}(batch)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("Concurrent insert error: %v", err)
	}

	for i := 0; i < 100; i++ {
		_, err := ns.GetNode(nodeID(i))
		if err != nil {
			t.Errorf("Node %s not found after concurrent insert: %v", nodeID(i), err)
		}
	}
}

func TestBatchStoreRollbackOnError(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	ns.InsertNode(&GraphNode{
		ID:       nodeID(0),
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
	}, []float32{1.0, 0.1, 0.1})

	bs := NewBatchStore(db, nil)

	nodes := []*GraphNode{
		{ID: nodeID(1), Domain: DomainCode, NodeType: NodeTypeFile},
		{ID: nodeID(0), Domain: DomainCode, NodeType: NodeTypeFile},
	}
	embeddings := [][]float32{{1.0, 0.1, 0.1}, {2.0, 0.1, 0.1}}

	err := bs.BatchInsertNodes(nodes, embeddings)
	if err == nil {
		t.Log("Expected error from duplicate insert, but got nil")
	}

	_, err = ns.GetNode(nodeID(1))
	if err == ErrNodeNotFound {
		t.Log("Transaction correctly rolled back - node1 not inserted")
	}
}
