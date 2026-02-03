package sylkdir

import (
	"testing"
)

func TestGlobalVersionNodeStoreWrite(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	head := gm.GetHead()
	store, err := NewGlobalVersionNodeStore(sd, head)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore: %v", err)
	}
	defer store.Close()

	// Write a node
	node := &Node{
		ID:           1,
		CanonicalKey: "file:test.go",
		Name:         "test.go",
		NodeType:     1,
	}
	if err := store.Write(node); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read it back
	nodes, err := store.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	if nodes[0].ID != 1 {
		t.Errorf("node ID = %d, want 1", nodes[0].ID)
	}
	if nodes[0].Name != "test.go" {
		t.Errorf("node Name = %s, want test.go", nodes[0].Name)
	}
}

func TestGlobalVersionNodeStoreWriteBatch(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	head := gm.GetHead()
	store, err := NewGlobalVersionNodeStore(sd, head)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore: %v", err)
	}
	defer store.Close()

	// Write batch
	nodes := []*Node{
		{ID: 1, Name: "a.go", NodeType: 1},
		{ID: 2, Name: "b.go", NodeType: 1},
		{ID: 3, Name: "c.go", NodeType: 1},
	}
	if err := store.WriteBatch(nodes); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	// Read all
	readNodes, err := store.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(readNodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(readNodes))
	}
}

func TestGlobalVersionEdgeStoreWrite(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	head := gm.GetHead()
	store, err := NewGlobalVersionEdgeStore(sd, head)
	if err != nil {
		t.Fatalf("NewGlobalVersionEdgeStore: %v", err)
	}
	defer store.Close()

	// Write an edge
	edge := &Edge{
		SourceID: 1,
		TargetID: 2,
		Type:     1,
		Weight:   1.0,
	}
	if err := store.Write(edge); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read it back
	edges, err := store.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("got %d edges, want 1", len(edges))
	}
	if edges[0].SourceID != 1 || edges[0].TargetID != 2 {
		t.Errorf("edge = %d->%d, want 1->2", edges[0].SourceID, edges[0].TargetID)
	}
}

func TestGlobalVersionDocStoreWrite(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	head := gm.GetHead()
	store, err := NewGlobalVersionDocStore(sd, head)
	if err != nil {
		t.Fatalf("NewGlobalVersionDocStore: %v", err)
	}
	defer store.Close()

	// Write a document
	doc := &VersionDocument{
		ID:      "file_1",
		Path:    "test.go",
		Type:    "source_code",
		Content: "package main",
	}
	if err := store.Write(doc); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read it back
	docs, err := store.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}
	if docs[0].ID != "file_1" {
		t.Errorf("doc ID = %s, want file_1", docs[0].ID)
	}
}

func TestGlobalVersionVectorStoreWrite(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	head := gm.GetHead()
	store, err := NewGlobalVersionVectorStore(sd, head)
	if err != nil {
		t.Fatalf("NewGlobalVersionVectorStore: %v", err)
	}
	defer store.Close()

	// Write a vector
	vec := &VersionVector{
		NodeID: 1,
		Vector: []float32{0.1, 0.2, 0.3, 0.4},
	}
	if err := store.Write(vec); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read it back
	vecs, err := store.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(vecs) != 1 {
		t.Fatalf("got %d vectors, want 1", len(vecs))
	}
	if vecs[0].NodeID != 1 {
		t.Errorf("vector NodeID = %d, want 1", vecs[0].NodeID)
	}
	if len(vecs[0].Vector) != 4 {
		t.Errorf("vector dim = %d, want 4", len(vecs[0].Vector))
	}
}

func TestGlobalVersionStoreVersionIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Write to v1.0.0
	v1 := gm.GetHead()
	nodeStore1, err := NewGlobalVersionNodeStore(sd, v1)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore v1: %v", err)
	}
	defer nodeStore1.Close()
	nodeStore1.Write(&Node{ID: 1, Name: "v1-node"})
	nodeStore1.SaveIndexes()

	// Create v2.0.0 and write to it
	v2 := v1.BumpMajor()
	if err := sd.CreateGlobalVersion(v2); err != nil {
		t.Fatalf("CreateGlobalVersion: %v", err)
	}
	nodeStore2, err := NewGlobalVersionNodeStore(sd, v2)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore v2: %v", err)
	}
	defer nodeStore2.Close()
	nodeStore2.Write(&Node{ID: 2, Name: "v2-node"})
	nodeStore2.SaveIndexes()

	// v1 should only have node 1
	nodes1, _ := nodeStore1.ReadAllFromVersion(v1)
	if len(nodes1) != 1 {
		t.Errorf("v1 nodes = %d, want 1", len(nodes1))
	}

	// v2 should only have node 2
	nodes2, _ := nodeStore2.ReadAllFromVersion(v2)
	if len(nodes2) != 1 {
		t.Errorf("v2 nodes = %d, want 1", len(nodes2))
	}
}

func TestGlobalCheckout(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Initial HEAD is v1.0.0
	v1 := gm.GetHead()
	if v1.Major != 1 || v1.Minor != 0 || v1.Patch != 0 {
		t.Fatalf("initial HEAD = %s, want v1.0.0", v1.String())
	}

	// Create v2.0.0
	v2 := v1.BumpMajor()
	if err := sd.CreateGlobalVersion(v2); err != nil {
		t.Fatalf("CreateGlobalVersion: %v", err)
	}
	gv := GlobalVersion{
		ID:        v2,
		ParentID:  v1,
		SessionID: 1,
	}
	if err := gm.AddVersion(gv); err != nil {
		t.Fatalf("AddVersion: %v", err)
	}
	if err := gm.SetHead(v2); err != nil {
		t.Fatalf("SetHead: %v", err)
	}

	// HEAD should be v2.0.0
	if !gm.GetHead().Equal(v2) {
		t.Errorf("HEAD = %s, want %s", gm.GetHead().String(), v2.String())
	}

	// Checkout back to v1.0.0
	if err := gm.Checkout(v1); err != nil {
		t.Fatalf("Checkout v1: %v", err)
	}

	// HEAD should be v1.0.0
	if !gm.GetHead().Equal(v1) {
		t.Errorf("after checkout, HEAD = %s, want %s", gm.GetHead().String(), v1.String())
	}

	// Checkout to non-existent version should fail
	v3 := SemanticVersion{Major: 3, Minor: 0, Patch: 0}
	if err := gm.Checkout(v3); err != ErrVersionNotFound {
		t.Errorf("checkout to non-existent version error = %v, want ErrVersionNotFound", err)
	}
}
