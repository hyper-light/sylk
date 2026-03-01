package sylkdir

import (
	"testing"
	"time"
)

// setupResolverSession creates a session with nodes and docs for resolver tests.
func setupResolverSession(t *testing.T) (*Session, *VersionNodeStore, *VersionDocStore) {
	t.Helper()
	sess, _ := createTestSession(t)
	nodeStore := NewVersionNodeStore(sess)
	docStore := NewVersionDocStore(sess)
	return sess, nodeStore, docStore
}

func TestDocResolverResolveMetadata(t *testing.T) {
	sess, nodeStore, _ := setupResolverSession(t)

	node := &Node{
		ID:           1,
		CanonicalKey: "file:main.go",
		Domain:       uint8(DomainCode),
		NodeType:     uint8(NodeTypeFunction),
		Name:         "HandleRequest",
		Path:         "main.go",
		SectionStart: 10,
		SectionEnd:   20,
		DocRef:       1,
		CreatedAt:    uint64(time.Now().UnixNano()),
		SessionID:    sess.Meta.ID,
	}
	if err := nodeStore.Write(node); err != nil {
		t.Fatalf("Write node: %v", err)
	}

	resolver := NewDocResolver(sess)
	metas, err := resolver.ResolveMetadata([]uint32{1, 999})
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}

	if len(metas) != 1 {
		t.Fatalf("expected 1 meta, got %d", len(metas))
	}
	m := metas[0]
	if m.NodeID != 1 {
		t.Errorf("NodeID: got %d, want 1", m.NodeID)
	}
	if m.SectionStart != 10 {
		t.Errorf("SectionStart: got %d, want 10", m.SectionStart)
	}
	if m.SectionEnd != 20 {
		t.Errorf("SectionEnd: got %d, want 20", m.SectionEnd)
	}
	if m.Path != "main.go" {
		t.Errorf("Path: got %s, want main.go", m.Path)
	}
}

func TestDocResolverResolve(t *testing.T) {
	sess, nodeStore, docStore := setupResolverSession(t)

	docID := "file_1"
	docRef := sess.DocIDMap.GetOrAssign(docID)

	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := docStore.Write(&VersionDocument{
		ID:        docID,
		Path:      "main.go",
		Type:      "source_code",
		Content:   content,
		IndexedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Write doc: %v", err)
	}

	node := &Node{
		ID:           1,
		CanonicalKey: "symbol:main.go:Foo:func",
		Domain:       uint8(DomainCode),
		NodeType:     uint8(NodeTypeFunction),
		Name:         "Foo",
		Path:         "main.go",
		SectionStart: 2,
		SectionEnd:   4,
		DocRef:       docRef,
		CreatedAt:    uint64(time.Now().UnixNano()),
		SessionID:    sess.Meta.ID,
	}
	if err := nodeStore.Write(node); err != nil {
		t.Fatalf("Write node: %v", err)
	}

	resolver := NewDocResolver(sess)
	results, err := resolver.Resolve([]uint32{1}, ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	want := "line2\nline3\nline4\n"
	if r.Content != want {
		t.Errorf("Content: got %q, want %q", r.Content, want)
	}
	if r.WindowStart != 2 {
		t.Errorf("WindowStart: got %d, want 2", r.WindowStart)
	}
	if r.WindowEnd != 4 {
		t.Errorf("WindowEnd: got %d, want 4", r.WindowEnd)
	}
}

func TestDocResolverResolveWithWindow(t *testing.T) {
	sess, nodeStore, docStore := setupResolverSession(t)

	docID := "file_1"
	docRef := sess.DocIDMap.GetOrAssign(docID)

	content := "L1\nL2\nL3\nL4\nL5\nL6\nL7\nL8\n"
	if err := docStore.Write(&VersionDocument{
		ID:        docID,
		Path:      "main.go",
		Type:      "source_code",
		Content:   content,
		IndexedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Write doc: %v", err)
	}

	node := &Node{
		ID:           1,
		CanonicalKey: "symbol:main.go:Bar:func",
		Domain:       uint8(DomainCode),
		NodeType:     uint8(NodeTypeFunction),
		Name:         "Bar",
		Path:         "main.go",
		SectionStart: 4,
		SectionEnd:   5,
		DocRef:       docRef,
		CreatedAt:    uint64(time.Now().UnixNano()),
		SessionID:    sess.Meta.ID,
	}
	if err := nodeStore.Write(node); err != nil {
		t.Fatalf("Write node: %v", err)
	}

	resolver := NewDocResolver(sess)
	results, err := resolver.Resolve([]uint32{1}, ResolveOpts{WindowLines: 2})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	want := "L2\nL3\nL4\nL5\nL6\nL7\n"
	if r.Content != want {
		t.Errorf("Content: got %q, want %q", r.Content, want)
	}
	if r.WindowStart != 2 {
		t.Errorf("WindowStart: got %d, want 2", r.WindowStart)
	}
	if r.WindowEnd != 7 {
		t.Errorf("WindowEnd: got %d, want 7", r.WindowEnd)
	}
}

func TestDocResolverBatchDedup(t *testing.T) {
	sess, nodeStore, docStore := setupResolverSession(t)

	docID := "file_1"
	docRef := sess.DocIDMap.GetOrAssign(docID)

	content := "A\nB\nC\nD\nE\n"
	if err := docStore.Write(&VersionDocument{
		ID:        docID,
		Path:      "shared.go",
		Type:      "source_code",
		Content:   content,
		IndexedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Write doc: %v", err)
	}

	now := uint64(time.Now().UnixNano())
	nodes := []*Node{
		{ID: 1, CanonicalKey: "chunk:shared.go:0000", Domain: uint8(DomainCode), NodeType: uint8(NodeTypeChunk), Name: "c0", Path: "shared.go", SectionStart: 1, SectionEnd: 2, DocRef: docRef, CreatedAt: now, SessionID: sess.Meta.ID},
		{ID: 2, CanonicalKey: "chunk:shared.go:0001", Domain: uint8(DomainCode), NodeType: uint8(NodeTypeChunk), Name: "c1", Path: "shared.go", SectionStart: 3, SectionEnd: 5, DocRef: docRef, CreatedAt: now, SessionID: sess.Meta.ID},
	}
	for _, n := range nodes {
		if err := nodeStore.Write(n); err != nil {
			t.Fatalf("Write node %d: %v", n.ID, err)
		}
	}

	resolver := NewDocResolver(sess)
	results, err := resolver.Resolve([]uint32{1, 2}, ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Content != "A\nB\n" {
		t.Errorf("results[0].Content: got %q, want %q", results[0].Content, "A\nB\n")
	}
	if results[1].Content != "C\nD\nE\n" {
		t.Errorf("results[1].Content: got %q, want %q", results[1].Content, "C\nD\nE\n")
	}
}

func TestDocResolverMissingNode(t *testing.T) {
	sess, _, _ := setupResolverSession(t)

	resolver := NewDocResolver(sess)
	results, err := resolver.Resolve([]uint32{999}, ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if results != nil {
		t.Errorf("expected nil results for missing node, got %d", len(results))
	}
}

func TestDocResolverMissingDoc(t *testing.T) {
	sess, nodeStore, _ := setupResolverSession(t)

	node := &Node{
		ID:           1,
		CanonicalKey: "file:missing.go",
		Domain:       uint8(DomainCode),
		NodeType:     uint8(NodeTypeFunction),
		Name:         "Missing",
		Path:         "missing.go",
		SectionStart: 1,
		SectionEnd:   5,
		DocRef:       9999, // no such doc
		CreatedAt:    uint64(time.Now().UnixNano()),
		SessionID:    sess.Meta.ID,
	}
	if err := nodeStore.Write(node); err != nil {
		t.Fatalf("Write node: %v", err)
	}

	resolver := NewDocResolver(sess)
	results, err := resolver.Resolve([]uint32{1}, ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Content != "" {
		t.Errorf("expected empty content for missing doc, got %q", results[0].Content)
	}
}

func TestDocResolverWholeDocSection(t *testing.T) {
	sess, nodeStore, docStore := setupResolverSession(t)

	docID := "file_1"
	docRef := sess.DocIDMap.GetOrAssign(docID)

	content := "package main\n\nfunc main() {}\n"
	if err := docStore.Write(&VersionDocument{
		ID:        docID,
		Path:      "main.go",
		Type:      "source_code",
		Content:   content,
		IndexedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Write doc: %v", err)
	}

	// SectionStart=0 means whole document
	node := &Node{
		ID:           1,
		CanonicalKey: "file:main.go",
		Domain:       uint8(DomainCode),
		NodeType:     uint8(NodeTypeFile),
		Name:         "main.go",
		Path:         "main.go",
		SectionStart: 0,
		SectionEnd:   0,
		DocRef:       docRef,
		CreatedAt:    uint64(time.Now().UnixNano()),
		SessionID:    sess.Meta.ID,
	}
	if err := nodeStore.Write(node); err != nil {
		t.Fatalf("Write node: %v", err)
	}

	resolver := NewDocResolver(sess)
	results, err := resolver.Resolve([]uint32{1}, ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Content != content {
		t.Errorf("Content: got %q, want %q", results[0].Content, content)
	}
}
