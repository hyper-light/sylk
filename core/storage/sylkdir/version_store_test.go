package sylkdir

import (
	"encoding/binary"
	"testing"
	"time"
)

func createTestSession(t *testing.T) (*Session, *SessionStore) {
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
	t.Cleanup(func() { sess.CloseBleve() })

	return sess, store
}

func TestVersionNodeStoreWrite(t *testing.T) {
	sess, _ := createTestSession(t)
	nodeStore := NewVersionNodeStore(sess)

	node := &Node{
		ID:           1,
		Domain:       1,
		NodeType:     2,
		SessionID:    1,
		CreatedBy:    1,
		CreatedAt:    uint64(time.Now().UnixNano()),
		Name:         "TestFunction",
		Path:         "/src/main.go",
		Package:      "main",
		Signature:    "func TestFunction() error",
		CanonicalKey: "repo:/src/main.go:TestFunction:func",
	}

	if err := nodeStore.Write(node); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read back
	readNode, err := nodeStore.ReadFromVersion(sess.Manifest.Head, 1)
	if err != nil {
		t.Fatalf("ReadFromVersion failed: %v", err)
	}

	if readNode.ID != node.ID {
		t.Errorf("ID mismatch: got %d, want %d", readNode.ID, node.ID)
	}
	if readNode.Name != node.Name {
		t.Errorf("Name mismatch: got %s, want %s", readNode.Name, node.Name)
	}
	if readNode.CanonicalKey != node.CanonicalKey {
		t.Errorf("CanonicalKey mismatch: got %s, want %s", readNode.CanonicalKey, node.CanonicalKey)
	}
}

func TestVersionNodeStoreWriteBatch(t *testing.T) {
	sess, _ := createTestSession(t)
	nodeStore := NewVersionNodeStore(sess)

	nodes := make([]*Node, 100)
	for i := range nodes {
		nodes[i] = &Node{
			ID:           uint32(i + 1),
			Name:         "Function" + string(rune('A'+i%26)),
			Path:         "/src/file.go",
			CanonicalKey: "repo:/src/file.go:Function:func",
			CreatedAt:    uint64(time.Now().UnixNano()),
		}
	}

	if err := nodeStore.WriteBatch(nodes); err != nil {
		t.Fatalf("WriteBatch failed: %v", err)
	}

	// Read all back
	readNodes, err := nodeStore.ReadAllFromVersion(sess.Manifest.Head)
	if err != nil {
		t.Fatalf("ReadAllFromVersion failed: %v", err)
	}

	if len(readNodes) != 100 {
		t.Errorf("Expected 100 nodes, got %d", len(readNodes))
	}
}

func TestVersionNodeStoreAncestorChain(t *testing.T) {
	sess, _ := createTestSession(t)
	nodeStore := NewVersionNodeStore(sess)

	// Write node to v1.0.0
	node1 := &Node{ID: 1, Name: "v1-node", CreatedAt: uint64(time.Now().UnixNano())}
	nodeStore.Write(node1)

	// Checkpoint to v1.0.1
	sess.Checkpoint("v1.0.1", CheckpointPatch)

	// Write different node to v1.0.1
	node2 := &Node{ID: 2, Name: "v1.0.1-node", CreatedAt: uint64(time.Now().UnixNano())}
	nodeStore.Write(node2)

	// Checkpoint to v1.0.2
	sess.Checkpoint("v1.0.2", CheckpointPatch)

	// Write updated version of node1 to v1.0.2
	node1Updated := &Node{ID: 1, Name: "v1.0.2-node1-updated", CreatedAt: uint64(time.Now().UnixNano())}
	nodeStore.Write(node1Updated)

	// Read from ancestor chain - should get newest version of node1
	readNode, err := nodeStore.ReadFromAncestorChain(1)
	if err != nil {
		t.Fatalf("ReadFromAncestorChain failed: %v", err)
	}

	if readNode.Name != "v1.0.2-node1-updated" {
		t.Errorf("Expected newest version, got %s", readNode.Name)
	}

	// Read all from ancestor chain - should get 2 unique nodes
	allNodes, err := nodeStore.ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAllFromAncestorChain failed: %v", err)
	}

	if len(allNodes) != 2 {
		t.Errorf("Expected 2 unique nodes, got %d", len(allNodes))
	}
}

func TestVersionEdgeStoreWrite(t *testing.T) {
	sess, _ := createTestSession(t)
	edgeStore := NewVersionEdgeStore(sess)

	edge := &Edge{
		SourceID:  1,
		TargetID:  2,
		Type:      1,
		Weight:    0.75,
		SessionID: 1,
		AgentID:   1,
		CreatedAt: uint64(time.Now().UnixNano()),
		UpdatedAt: uint64(time.Now().UnixNano()),
	}

	if err := edgeStore.Write(edge); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read back via outgoing
	edges, err := edgeStore.GetOutgoingFromVersion(sess.Manifest.Head, 1)
	if err != nil {
		t.Fatalf("GetOutgoingFromVersion failed: %v", err)
	}

	if len(edges) != 1 {
		t.Fatalf("Expected 1 edge, got %d", len(edges))
	}

	if edges[0].TargetID != 2 {
		t.Errorf("TargetID mismatch: got %d, want 2", edges[0].TargetID)
	}
}

func TestVersionEdgeStoreWriteBatch(t *testing.T) {
	sess, _ := createTestSession(t)
	edgeStore := NewVersionEdgeStore(sess)

	edges := make([]*Edge, 100)
	for i := range edges {
		edges[i] = &Edge{
			SourceID:  uint32(i / 10),
			TargetID:  uint32(i%10 + 100),
			Type:      uint8(i % 5),
			Weight:    0.5,
			CreatedAt: uint64(time.Now().UnixNano()),
			UpdatedAt: uint64(time.Now().UnixNano()),
		}
	}

	if err := edgeStore.WriteBatch(edges); err != nil {
		t.Fatalf("WriteBatch failed: %v", err)
	}

	// Read all back
	readEdges, err := edgeStore.ReadAllFromVersion(sess.Manifest.Head)
	if err != nil {
		t.Fatalf("ReadAllFromVersion failed: %v", err)
	}

	if len(readEdges) != 100 {
		t.Errorf("Expected 100 edges, got %d", len(readEdges))
	}
}

func TestVersionEdgeStoreAncestorChain(t *testing.T) {
	sess, _ := createTestSession(t)
	edgeStore := NewVersionEdgeStore(sess)

	// Write edge to v1.0.0
	edge1 := &Edge{SourceID: 1, TargetID: 2, Type: 1, CreatedAt: uint64(time.Now().UnixNano())}
	edgeStore.Write(edge1)

	// Checkpoint
	sess.Checkpoint("v1.0.1", CheckpointPatch)

	// Write another edge from same source
	edge2 := &Edge{SourceID: 1, TargetID: 3, Type: 1, CreatedAt: uint64(time.Now().UnixNano())}
	edgeStore.Write(edge2)

	// Get outgoing from ancestor chain
	edges, err := edgeStore.GetOutgoingFromAncestorChain(1)
	if err != nil {
		t.Fatalf("GetOutgoingFromAncestorChain failed: %v", err)
	}

	if len(edges) != 2 {
		t.Errorf("Expected 2 edges, got %d", len(edges))
	}
}

func TestVersionDocStoreWrite(t *testing.T) {
	sess, _ := createTestSession(t)
	docStore := NewVersionDocStore(sess)

	doc := &VersionDocument{
		ID:        "doc1",
		Path:      "/src/main.go",
		Type:      "source_code",
		Content:   "package main\n\nfunc main() {}",
		Language:  "go",
		IndexedAt: time.Now().UnixNano(),
	}

	if err := docStore.Write(doc); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read back
	docs, err := docStore.ReadFromVersion(sess.Manifest.Head)
	if err != nil {
		t.Fatalf("ReadFromVersion failed: %v", err)
	}

	if len(docs) != 1 {
		t.Fatalf("Expected 1 doc, got %d", len(docs))
	}

	if docs[0].Path != "/src/main.go" {
		t.Errorf("Path mismatch: got %s", docs[0].Path)
	}
}

func TestVersionDocStoreWriteBatch(t *testing.T) {
	sess, _ := createTestSession(t)
	docStore := NewVersionDocStore(sess)

	docs := make([]*VersionDocument, 50)
	for i := range docs {
		docs[i] = &VersionDocument{
			ID:        "doc" + string(rune('0'+i)),
			Path:      "/src/file" + string(rune('0'+i)) + ".go",
			Type:      "source_code",
			Content:   "package main",
			IndexedAt: time.Now().UnixNano(),
		}
	}

	if err := docStore.WriteBatch(docs); err != nil {
		t.Fatalf("WriteBatch failed: %v", err)
	}

	count, err := docStore.Count()
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}

	if count != 50 {
		t.Errorf("Expected 50 docs, got %d", count)
	}
}

func TestVersionDocStoreAncestorChain(t *testing.T) {
	sess, _ := createTestSession(t)
	docStore := NewVersionDocStore(sess)

	// Write doc to v1.0.0
	doc1 := &VersionDocument{ID: "doc1", Path: "/v1.go", Content: "v1 content", IndexedAt: time.Now().UnixNano()}
	docStore.Write(doc1)

	// Checkpoint
	sess.Checkpoint("v1.0.1", CheckpointPatch)

	// Write updated doc1 and new doc2 to v1.0.1
	doc1Updated := &VersionDocument{ID: "doc1", Path: "/v1.go", Content: "v1.0.1 updated content", IndexedAt: time.Now().UnixNano()}
	doc2 := &VersionDocument{ID: "doc2", Path: "/v2.go", Content: "doc2 content", IndexedAt: time.Now().UnixNano()}
	docStore.Write(doc1Updated)
	docStore.Write(doc2)

	// Read from ancestor chain
	docs, err := docStore.ReadFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadFromAncestorChain failed: %v", err)
	}

	if len(docs) != 2 {
		t.Errorf("Expected 2 unique docs, got %d", len(docs))
	}

	// doc1 should have v1.0.1 content (newest wins)
	for _, doc := range docs {
		if doc.ID == "doc1" && doc.Content != "v1.0.1 updated content" {
			t.Errorf("Expected newest doc1 content, got %s", doc.Content)
		}
	}
}

func TestVersionStoresIntegration(t *testing.T) {
	sess, _ := createTestSession(t)

	nodeStore := NewVersionNodeStore(sess)
	edgeStore := NewVersionEdgeStore(sess)
	docStore := NewVersionDocStore(sess)

	// Write initial data to v1.0.0
	nodeStore.Write(&Node{ID: 1, Name: "func1", CreatedAt: uint64(time.Now().UnixNano())})
	nodeStore.Write(&Node{ID: 2, Name: "func2", CreatedAt: uint64(time.Now().UnixNano())})
	edgeStore.Write(&Edge{SourceID: 1, TargetID: 2, Type: 1, CreatedAt: uint64(time.Now().UnixNano())})
	docStore.Write(&VersionDocument{ID: "doc1", Path: "/main.go", IndexedAt: time.Now().UnixNano()})

	// Checkpoint
	sess.Checkpoint("after-initial", CheckpointPatch)

	// Write more data to v1.0.1
	nodeStore.Write(&Node{ID: 3, Name: "func3", CreatedAt: uint64(time.Now().UnixNano())})
	edgeStore.Write(&Edge{SourceID: 2, TargetID: 3, Type: 1, CreatedAt: uint64(time.Now().UnixNano())})
	docStore.Write(&VersionDocument{ID: "doc2", Path: "/util.go", IndexedAt: time.Now().UnixNano()})

	// Verify counts from ancestor chain
	allNodes, _ := nodeStore.ReadAllFromAncestorChain()
	if len(allNodes) != 3 {
		t.Errorf("Expected 3 nodes, got %d", len(allNodes))
	}

	allEdges, _ := edgeStore.ReadAllFromAncestorChain()
	if len(allEdges) != 2 {
		t.Errorf("Expected 2 edges, got %d", len(allEdges))
	}

	allDocs, _ := docStore.ReadFromAncestorChain()
	if len(allDocs) != 2 {
		t.Errorf("Expected 2 docs, got %d", len(allDocs))
	}

	// Checkout v1.0.0 and verify we only see v1.0.0 data
	v100 := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	sess.Checkout(v100)

	v1Nodes, _ := nodeStore.ReadAllFromAncestorChain()
	if len(v1Nodes) != 2 {
		t.Errorf("After checkout v1.0.0: expected 2 nodes, got %d", len(v1Nodes))
	}

	v1Edges, _ := edgeStore.ReadAllFromAncestorChain()
	if len(v1Edges) != 1 {
		t.Errorf("After checkout v1.0.0: expected 1 edge, got %d", len(v1Edges))
	}

	v1Docs, _ := docStore.ReadFromAncestorChain()
	if len(v1Docs) != 1 {
		t.Errorf("After checkout v1.0.0: expected 1 doc, got %d", len(v1Docs))
	}
}

func TestVersionStoresBranchIsolation(t *testing.T) {
	sess, _ := createTestSession(t)

	nodeStore := NewVersionNodeStore(sess)

	// v1.0.0: write node 1
	nodeStore.Write(&Node{ID: 1, Name: "node1-v1", CreatedAt: uint64(time.Now().UnixNano())})

	// v1.0.1: checkpoint and write node 2
	sess.Checkpoint("v1.0.1", CheckpointPatch)
	nodeStore.Write(&Node{ID: 2, Name: "node2-v1.0.1", CreatedAt: uint64(time.Now().UnixNano())})

	// v1.0.2: checkpoint and write node 3
	sess.Checkpoint("v1.0.2", CheckpointPatch)
	nodeStore.Write(&Node{ID: 3, Name: "node3-v1.0.2", CreatedAt: uint64(time.Now().UnixNano())})

	// Checkout v1.0.0 and create branch v1.1.0
	v100 := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	sess.Checkout(v100)
	sess.Checkpoint("v1.1.0-branch", CheckpointMinor)
	nodeStore.Write(&Node{ID: 4, Name: "node4-v1.1.0-branch", CreatedAt: uint64(time.Now().UnixNano())})

	// From v1.1.0: should see nodes 1 and 4, NOT 2 and 3
	branchNodes, _ := nodeStore.ReadAllFromAncestorChain()

	nodeIDs := make(map[uint32]bool)
	for _, n := range branchNodes {
		nodeIDs[n.ID] = true
	}

	if !nodeIDs[1] || !nodeIDs[4] {
		t.Error("Branch should contain nodes 1 and 4")
	}
	if nodeIDs[2] || nodeIDs[3] {
		t.Error("Branch should NOT contain nodes 2 and 3 (different branch)")
	}
}

func TestVersionVectorStoreWrite(t *testing.T) {
	sess, _ := createTestSession(t)

	store := NewVersionVectorStore(sess)

	vec := &VersionVector{
		NodeID: 1,
		Vector: []float32{0.1, 0.2, 0.3, 0.4, 0.5},
	}

	if err := store.Write(vec); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read back
	got, err := store.GetFromVersion(sess.Manifest.Head, 1)
	if err != nil {
		t.Fatalf("GetFromVersion failed: %v", err)
	}
	if got == nil {
		t.Fatal("Expected vector, got nil")
	}
	if got.NodeID != 1 {
		t.Errorf("NodeID = %d, want 1", got.NodeID)
	}
	if len(got.Vector) != 5 {
		t.Errorf("Vector dim = %d, want 5", len(got.Vector))
	}
	for i, v := range got.Vector {
		if v != vec.Vector[i] {
			t.Errorf("Vector[%d] = %f, want %f", i, v, vec.Vector[i])
		}
	}
}

func TestVersionVectorStoreWriteBatch(t *testing.T) {
	sess, _ := createTestSession(t)

	store := NewVersionVectorStore(sess)

	vecs := []*VersionVector{
		{NodeID: 1, Vector: []float32{1.0, 2.0, 3.0}},
		{NodeID: 2, Vector: []float32{4.0, 5.0, 6.0}},
		{NodeID: 3, Vector: []float32{7.0, 8.0, 9.0}},
	}

	if err := store.WriteBatch(vecs); err != nil {
		t.Fatalf("WriteBatch failed: %v", err)
	}

	// Read all
	all, err := store.ReadAllFromVersion(sess.Manifest.Head)
	if err != nil {
		t.Fatalf("ReadAllFromVersion failed: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("Expected 3 vectors, got %d", len(all))
	}
}

func TestVersionVectorStoreAncestorChain(t *testing.T) {
	sess, _ := createTestSession(t)

	store := NewVersionVectorStore(sess)

	// Write to v1.0.0
	store.Write(&VersionVector{NodeID: 1, Vector: []float32{1.0, 1.0}})

	// Checkpoint to v1.0.1
	sess.Checkpoint("v1.0.1", CheckpointPatch)

	// Write to v1.0.1
	store.Write(&VersionVector{NodeID: 2, Vector: []float32{2.0, 2.0}})

	// Read from ancestor chain (should get both)
	all, err := store.ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAllFromAncestorChain failed: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("Expected 2 vectors from chain, got %d", len(all))
	}

	// Checkout v1.0.0 (should only see 1 vector)
	v100 := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	sess.Checkout(v100)

	v1Vecs, err := store.ReadAllFromAncestorChain()
	if err != nil {
		t.Fatalf("ReadAllFromAncestorChain after checkout failed: %v", err)
	}
	if len(v1Vecs) != 1 {
		t.Errorf("Expected 1 vector after checkout v1.0.0, got %d", len(v1Vecs))
	}
}

func TestVersionVectorStoreHighDim(t *testing.T) {
	sess, _ := createTestSession(t)

	store := NewVersionVectorStore(sess)

	// Test with 128-dimensional vector (common embedding size)
	dim := 128
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(i) / float32(dim)
	}

	if err := store.Write(&VersionVector{NodeID: 1, Vector: vec}); err != nil {
		t.Fatalf("Write high-dim vector failed: %v", err)
	}

	got, err := store.GetFromVersion(sess.Manifest.Head, 1)
	if err != nil {
		t.Fatalf("GetFromVersion failed: %v", err)
	}
	if len(got.Vector) != dim {
		t.Errorf("Vector dim = %d, want %d", len(got.Vector), dim)
	}

	// Verify values
	for i := range got.Vector {
		expected := float32(i) / float32(dim)
		if got.Vector[i] != expected {
			t.Errorf("Vector[%d] = %f, want %f", i, got.Vector[i], expected)
		}
	}
}

func TestSemanticVersionParsing(t *testing.T) {
	tests := []struct {
		input    string
		expected SemanticVersion
	}{
		{"v1.0.0", SemanticVersion{1, 0, 0}},
		{"v2.1.3", SemanticVersion{2, 1, 3}},
		{"1.0.0", SemanticVersion{1, 0, 0}},
	}

	for _, tt := range tests {
		v, err := ParseSemanticVersion(tt.input)
		if err != nil {
			t.Errorf("ParseSemanticVersion(%s) error: %v", tt.input, err)
			continue
		}
		if !v.Equal(tt.expected) {
			t.Errorf("ParseSemanticVersion(%s) = %s, want %s", tt.input, v.String(), tt.expected.String())
		}
	}
}

func TestSemanticVersionBumping(t *testing.T) {
	v := SemanticVersion{1, 2, 3}

	if patch := v.BumpPatch(); !patch.Equal(SemanticVersion{1, 2, 4}) {
		t.Errorf("BumpPatch: got %s, want v1.2.4", patch.String())
	}

	if minor := v.BumpMinor(); !minor.Equal(SemanticVersion{1, 3, 0}) {
		t.Errorf("BumpMinor: got %s, want v1.3.0", minor.String())
	}

	if major := v.BumpMajor(); !major.Equal(SemanticVersion{2, 0, 0}) {
		t.Errorf("BumpMajor: got %s, want v2.0.0", major.String())
	}
}

func TestMarshalDocRecordBinaryRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		doc  VersionDocument
	}{
		{
			name: "full fields",
			doc: VersionDocument{
				ID:        "doc_123",
				Path:      "/src/main.go",
				Type:      "source_code",
				Content:   "package main\n\nfunc main() {}",
				Language:  "go",
				IndexedAt: 1700000000000000000,
			},
		},
		{
			name: "empty content",
			doc: VersionDocument{
				ID:        "empty",
				Path:      "/empty.txt",
				Type:      "text",
				Content:   "",
				Language:  "",
				IndexedAt: 0,
			},
		},
		{
			name: "large content",
			doc: VersionDocument{
				ID:        "big_file",
				Path:      "/src/generated.go",
				Type:      "source_code",
				Content:   string(make([]byte, 100000)),
				Language:  "go",
				IndexedAt: 9999999999,
			},
		},
		{
			name: "unicode content",
			doc: VersionDocument{
				ID:        "i18n",
				Path:      "/docs/readme.md",
				Type:      "markdown",
				Content:   "Hello \xe4\xb8\x96\xe7\x95\x8c \xf0\x9f\x8c\x8d",
				Language:  "markdown",
				IndexedAt: 42,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, err := marshalDocRecord(&tt.doc)
			if err != nil {
				t.Fatalf("marshalDocRecord: %v", err)
			}

			// Decode: skip the 4-byte size prefix, pass payload to unmarshal.
			size := binary.LittleEndian.Uint32(record[0:4])
			if int(size) != len(record)-4 {
				t.Fatalf("size prefix = %d, record payload = %d", size, len(record)-4)
			}

			got, decErr := unmarshalDocPayload(record[4:])
			if decErr != nil {
				t.Fatalf("unmarshalDocPayload: %v", decErr)
			}

			if got.ID != tt.doc.ID {
				t.Errorf("ID: got %q, want %q", got.ID, tt.doc.ID)
			}
			if got.Path != tt.doc.Path {
				t.Errorf("Path: got %q, want %q", got.Path, tt.doc.Path)
			}
			if got.Type != tt.doc.Type {
				t.Errorf("Type: got %q, want %q", got.Type, tt.doc.Type)
			}
			if got.Content != tt.doc.Content {
				t.Errorf("Content length: got %d, want %d", len(got.Content), len(tt.doc.Content))
			}
			if got.Language != tt.doc.Language {
				t.Errorf("Language: got %q, want %q", got.Language, tt.doc.Language)
			}
			if got.IndexedAt != tt.doc.IndexedAt {
				t.Errorf("IndexedAt: got %d, want %d", got.IndexedAt, tt.doc.IndexedAt)
			}
		})
	}
}
