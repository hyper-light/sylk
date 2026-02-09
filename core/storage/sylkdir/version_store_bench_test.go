package sylkdir

import (
	"testing"
	"time"
)

func BenchmarkVersionNodeStoreWrite(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		b.Fatalf("Create session failed: %v", err)
	}

	nodeStore := NewVersionNodeStore(sess)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		node := &Node{
			ID:           uint32(i + 1),
			Name:         "TestFunction",
			Path:         "/src/main.go",
			CanonicalKey: "repo:/src/main.go:TestFunction:func",
			CreatedAt:    uint64(time.Now().UnixNano()),
		}
		if err := nodeStore.Write(node); err != nil {
			b.Fatalf("Write failed: %v", err)
		}
	}
}

func BenchmarkVersionNodeStoreBatchWrite(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		b.Fatalf("Create session failed: %v", err)
	}

	nodeStore := NewVersionNodeStore(sess)
	nodes := make([]*Node, 1000)
	for i := range nodes {
		nodes[i] = &Node{
			ID:           uint32(i + 1),
			Name:         "TestFunction",
			Path:         "/src/main.go",
			CanonicalKey: "repo:/src/main.go:TestFunction:func",
			CreatedAt:    uint64(time.Now().UnixNano()),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset session for each iteration
		sess, _ = store.Create(uint32(i+2), nil)
		nodeStore = NewVersionNodeStore(sess)
		if err := nodeStore.WriteBatch(nodes); err != nil {
			b.Fatalf("WriteBatch failed: %v", err)
		}
	}
}

func BenchmarkVersionEdgeStoreBatchWrite(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		b.Fatalf("Create session failed: %v", err)
	}

	edgeStore := NewVersionEdgeStore(sess)
	edges := make([]*Edge, 5000)
	for i := range edges {
		edges[i] = &Edge{
			SourceID:  uint32(i / 50),
			TargetID:  uint32(i%50 + 1000),
			Type:      uint8(i % 5),
			Weight:    0.5,
			CreatedAt: uint64(time.Now().UnixNano()),
			UpdatedAt: uint64(time.Now().UnixNano()),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess, _ = store.Create(uint32(i+2), nil)
		edgeStore = NewVersionEdgeStore(sess)
		if err := edgeStore.WriteBatch(edges); err != nil {
			b.Fatalf("WriteBatch failed: %v", err)
		}
	}
}

func BenchmarkVersionDocStoreBatchWrite(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)
	sess, err := store.Create(1, nil)
	if err != nil {
		b.Fatalf("Create session failed: %v", err)
	}

	docStore := NewVersionDocStore(sess)
	docs := make([]*VersionDocument, 100)
	for i := range docs {
		docs[i] = &VersionDocument{
			ID:        "doc" + string(rune('0'+i)),
			Path:      "/src/file.go",
			Type:      "source_code",
			Content:   "package main\n\nfunc main() {}",
			Language:  "go",
			IndexedAt: time.Now().UnixNano(),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess, _ = store.Create(uint32(i+2), nil)
		docStore = NewVersionDocStore(sess)
		if err := docStore.WriteBatch(docs); err != nil {
			b.Fatalf("WriteBatch failed: %v", err)
		}
	}
}

func BenchmarkVersionStoresCombinedWorkload(b *testing.B) {
	// This benchmark verifies the acceptance criteria:
	// "Write 1000 nodes + 5000 edges + 100 docs to session < 500ms"
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewSessionStore(sd)

	// Prepare test data
	nodes := make([]*Node, 1000)
	for i := range nodes {
		nodes[i] = &Node{
			ID:           uint32(i + 1),
			Name:         "TestFunction",
			Path:         "/src/main.go",
			CanonicalKey: "repo:/src/main.go:TestFunction:func",
			CreatedAt:    uint64(time.Now().UnixNano()),
		}
	}

	edges := make([]*Edge, 5000)
	for i := range edges {
		edges[i] = &Edge{
			SourceID:  uint32(i / 50),
			TargetID:  uint32(i%50 + 1000),
			Type:      uint8(i % 5),
			Weight:    0.5,
			CreatedAt: uint64(time.Now().UnixNano()),
			UpdatedAt: uint64(time.Now().UnixNano()),
		}
	}

	docs := make([]*VersionDocument, 100)
	for i := range docs {
		docs[i] = &VersionDocument{
			ID:        "doc" + string(rune('0'+i)),
			Path:      "/src/file.go",
			Type:      "source_code",
			Content:   "package main\n\nfunc main() {}",
			Language:  "go",
			IndexedAt: time.Now().UnixNano(),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess, err := store.Create(uint32(i+1), nil)
		if err != nil {
			b.Fatalf("Create session failed: %v", err)
		}

		nodeStore := NewVersionNodeStore(sess)
		edgeStore := NewVersionEdgeStore(sess)
		docStore := NewVersionDocStore(sess)

		if err := nodeStore.WriteBatch(nodes); err != nil {
			b.Fatalf("WriteBatch nodes failed: %v", err)
		}
		if err := edgeStore.WriteBatch(edges); err != nil {
			b.Fatalf("WriteBatch edges failed: %v", err)
		}
		if err := docStore.WriteBatch(docs); err != nil {
			b.Fatalf("WriteBatch docs failed: %v", err)
		}
	}
}
