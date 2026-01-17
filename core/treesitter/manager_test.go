package treesitter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/versioning"
)

func TestNewTreeSitterManager(t *testing.T) {
	t.Run("creates manager with caches", func(t *testing.T) {
		loader := &GrammarLoader{}
		m := NewTreeSitterManager(loader, 100, 50)
		defer m.Close()

		if m.loader != loader {
			t.Error("loader not set")
		}
		if m.treeCache == nil {
			t.Error("treeCache not initialized")
		}
		if m.queryCache == nil {
			t.Error("queryCache not initialized")
		}
		if m.treeCache.maxSize != 100 {
			t.Errorf("expected tree cache size 100, got %d", m.treeCache.maxSize)
		}
		if m.queryCache.maxSize != 50 {
			t.Errorf("expected query cache size 50, got %d", m.queryCache.maxSize)
		}
	})
}

func TestManagerOptions(t *testing.T) {
	t.Run("WithTreeCacheSize", func(t *testing.T) {
		loader := &GrammarLoader{}
		m := NewTreeSitterManager(loader, 10, 10)
		defer m.Close()

		WithTreeCacheSize(200)(m)
		if m.treeCache.maxSize != 200 {
			t.Errorf("expected 200, got %d", m.treeCache.maxSize)
		}
	})

	t.Run("WithQueryCacheSize", func(t *testing.T) {
		loader := &GrammarLoader{}
		m := NewTreeSitterManager(loader, 10, 10)
		defer m.Close()

		WithQueryCacheSize(300)(m)
		if m.queryCache.maxSize != 300 {
			t.Errorf("expected 300, got %d", m.queryCache.maxSize)
		}
	})
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"main.go", "go"},
		{"/path/to/file.go", "go"},
		{"lib.rs", "rust"},
		{"script.py", "python"},
		{"types.pyi", "python"},
		{"app.js", "javascript"},
		{"module.mjs", "javascript"},
		{"index.ts", "typescript"},
		{"component.tsx", "tsx"},
		{"App.jsx", "javascript"},
		{"Main.java", "java"},
		{"code.c", "c"},
		{"header.h", "c"},
		{"code.cpp", "cpp"},
		{"code.cc", "cpp"},
		{"header.hpp", "cpp"},
		{"script.rb", "ruby"},
		{"Rakefile.rake", "ruby"},
		{"app.swift", "swift"},
		{"Main.kt", "kotlin"},
		{"build.kts", "kotlin"},
		{"unknown.xyz", ""},
		{"noextension", ""},
		{"/path/no.ext/file", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := detectLanguage(tt.path)
			if got != tt.expected {
				t.Errorf("detectLanguage(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestExtractExtension(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"file.go", ".go"},
		{"/a/b/c.txt", ".txt"},
		{"noext", ""},
		{"/path/noext", ""},
		{"a.b.c.d", ".d"},
		{".hidden", ".hidden"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := extractExtension(tt.path)
			if got != tt.expected {
				t.Errorf("extractExtension(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestIsNodeType(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"source_file", true},
		{"function_declaration", true},
		{"identifier", true},
		{"a", true},
		{"", false},
		{"UpperCase", false},
		{"with-dash", false},
		{"with123", false},
		{"func_name", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isNodeType(tt.input)
			if got != tt.expected {
				t.Errorf("isNodeType(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestUintToStr(t *testing.T) {
	tests := []struct {
		input    uint
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{123, "123"},
		{1000000, "1000000"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := uintToStr(tt.input)
			if got != tt.expected {
				t.Errorf("uintToStr(%d) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTreeCacheOperations(t *testing.T) {
	t.Run("get returns nil for missing", func(t *testing.T) {
		loader := &GrammarLoader{}
		m := NewTreeSitterManager(loader, 10, 10)
		defer m.Close()

		var zeroID versioning.VersionID
		cached := m.getCachedTree(zeroID)
		if cached != nil {
			t.Error("expected nil for missing cache entry")
		}
	})

	t.Run("concurrent cache access", func(t *testing.T) {
		loader := &GrammarLoader{}
		m := NewTreeSitterManager(loader, 100, 100)
		defer m.Close()

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				var id versioning.VersionID
				id[0] = byte(idx)
				_ = m.getCachedTree(id)
			}(i)
		}
		wg.Wait()
	})
}

func TestManagerErrors(t *testing.T) {
	t.Run("ErrInvalidPath", func(t *testing.T) {
		if ErrInvalidPath.Error() != "invalid path" {
			t.Errorf("unexpected error message: %s", ErrInvalidPath.Error())
		}
	})

	t.Run("ErrPathNotFound", func(t *testing.T) {
		if ErrPathNotFound.Error() != "path not found in tree" {
			t.Errorf("unexpected error message: %s", ErrPathNotFound.Error())
		}
	})
}

func TestComputeNodePathNilTree(t *testing.T) {
	loader := &GrammarLoader{}
	m := NewTreeSitterManager(loader, 10, 10)
	defer m.Close()

	path := m.ComputeNodePath(nil, 0)
	if path != nil {
		t.Errorf("expected nil path for nil tree, got %v", path)
	}
}

func TestResolveNodePathErrors(t *testing.T) {
	loader := &GrammarLoader{}
	m := NewTreeSitterManager(loader, 10, 10)
	defer m.Close()

	t.Run("nil tree", func(t *testing.T) {
		_, err := m.ResolveNodePath(nil, []string{"source_file"})
		if err != ErrInvalidPath {
			t.Errorf("expected ErrInvalidPath, got %v", err)
		}
	})

	t.Run("empty path", func(t *testing.T) {
		_, err := m.ResolveNodePath(&Tree{}, []string{})
		if err != ErrInvalidPath {
			t.Errorf("expected ErrInvalidPath, got %v", err)
		}
	})
}

func TestInvalidateCacheNonExistent(t *testing.T) {
	loader := &GrammarLoader{}
	m := NewTreeSitterManager(loader, 10, 10)
	defer m.Close()

	var id versioning.VersionID
	id[0] = 99
	m.InvalidateCache(id)
}

func TestManagerClose(t *testing.T) {
	loader := &GrammarLoader{}
	m := NewTreeSitterManager(loader, 10, 10)

	m.Close()

	if len(m.treeCache.entries) != 0 {
		t.Error("tree cache should be empty after close")
	}
	if len(m.queryCache.entries) != 0 {
		t.Error("query cache should be empty after close")
	}
}

func TestEvictOldestIfNeeded(t *testing.T) {
	loader := &GrammarLoader{}
	m := NewTreeSitterManager(loader, 2, 10)

	id1 := versioning.VersionID{1}
	id2 := versioning.VersionID{2}
	id3 := versioning.VersionID{3}

	m.treeCache.entries[id1] = &CachedTree{ParsedAt: time.Now().Add(-2 * time.Hour)}
	m.treeCache.entries[id2] = &CachedTree{ParsedAt: time.Now().Add(-1 * time.Hour)}

	m.treeCache.mu.Lock()
	m.evictOldestIfNeeded()
	m.treeCache.entries[id3] = &CachedTree{ParsedAt: time.Now()}
	m.treeCache.mu.Unlock()

	if _, ok := m.treeCache.entries[id1]; ok {
		t.Error("oldest entry should have been evicted")
	}

	// Cleanup without Close() since we have nil Trees
	m.treeCache.entries = make(map[versioning.VersionID]*CachedTree)
	m.queryCache.entries = make(map[queryCacheKey]*Query)
}

func TestEvictQueryIfNeededNoEviction(t *testing.T) {
	loader := &GrammarLoader{}
	m := NewTreeSitterManager(loader, 10, 10)
	defer m.Close()

	if len(m.queryCache.entries) != 0 {
		t.Error("cache should start empty")
	}

	m.queryCache.mu.Lock()
	m.evictQueryIfNeeded()
	m.queryCache.mu.Unlock()
}

func TestFindOldestEntryEmpty(t *testing.T) {
	loader := &GrammarLoader{}
	m := NewTreeSitterManager(loader, 10, 10)
	defer m.Close()

	oldest := m.findOldestEntry()
	if oldest != nil {
		t.Error("expected nil for empty cache")
	}
}

func skipIfNoGrammarManager(t *testing.T, name string) {
	t.Helper()
	if !IsGrammarAvailable(name) {
		t.Skipf("grammar %q not available, skipping", name)
	}
}

func TestParseVersionWithGrammar(t *testing.T) {
	skipIfNoGrammarManager(t, "go")

	loader := NewGrammarLoader()
	m := NewTreeSitterManager(loader, 100, 50)
	defer m.Close()

	content := []byte(`package main

func main() {
	println("hello")
}
`)
	fv := versioning.NewFileVersion(
		"test.go",
		content,
		nil,
		nil,
		"pipeline-1",
		"session-1",
		versioning.VectorClock{},
	)

	ctx := context.Background()
	tree, err := m.ParseVersion(ctx, &fv, content)
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}

	root := tree.RootNode()
	if root.Type() != "source_file" {
		t.Errorf("expected source_file, got %s", root.Type())
	}

	tree2, err := m.ParseVersion(ctx, &fv, content)
	if err != nil {
		t.Fatalf("second ParseVersion: %v", err)
	}
	if tree2 != tree {
		t.Error("expected cached tree")
	}
}

func TestParseVersionUnknownLanguage(t *testing.T) {
	loader := NewGrammarLoader()
	m := NewTreeSitterManager(loader, 100, 50)
	defer m.Close()

	content := []byte("some content")
	fv := versioning.NewFileVersion(
		"test.unknown",
		content,
		nil,
		nil,
		"pipeline-1",
		"session-1",
		versioning.VectorClock{},
	)

	ctx := context.Background()
	_, err := m.ParseVersion(ctx, &fv, content)
	if err != ErrGrammarNotFound {
		t.Errorf("expected ErrGrammarNotFound, got %v", err)
	}
}

func TestParseIncrementalWithGrammar(t *testing.T) {
	skipIfNoGrammarManager(t, "go")

	loader := NewGrammarLoader()
	m := NewTreeSitterManager(loader, 100, 50)
	defer m.Close()

	content1 := []byte(`package main`)
	fv1 := versioning.NewFileVersion(
		"test.go",
		content1,
		nil,
		nil,
		"pipeline-1",
		"session-1",
		versioning.VectorClock{},
	)

	ctx := context.Background()
	tree1, err := m.ParseVersion(ctx, &fv1, content1)
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}

	content2 := []byte(`package main

import "fmt"
`)
	tree2, err := m.ParseIncremental(ctx, &fv1, content2)
	if err != nil {
		t.Fatalf("ParseIncremental: %v", err)
	}
	defer tree2.Close()

	_ = tree1
	if tree2.RootNode().Type() != "source_file" {
		t.Errorf("expected source_file, got %s", tree2.RootNode().Type())
	}
}

func TestParseIncrementalNoCachedTree(t *testing.T) {
	skipIfNoGrammarManager(t, "go")

	loader := NewGrammarLoader()
	m := NewTreeSitterManager(loader, 100, 50)
	defer m.Close()

	content := []byte(`package main`)
	fv := versioning.NewFileVersion(
		"test.go",
		content,
		nil,
		nil,
		"pipeline-1",
		"session-1",
		versioning.VectorClock{},
	)

	ctx := context.Background()
	tree, err := m.ParseIncremental(ctx, &fv, content)
	if err != nil {
		t.Fatalf("ParseIncremental without cache: %v", err)
	}
	defer tree.Close()

	if tree.RootNode().Type() != "source_file" {
		t.Errorf("expected source_file, got %s", tree.RootNode().Type())
	}
}

func TestComputeAndResolveNodePath(t *testing.T) {
	skipIfNoGrammarManager(t, "go")

	loader := NewGrammarLoader()
	m := NewTreeSitterManager(loader, 100, 50)
	defer m.Close()

	content := []byte(`package main

func hello() {
	println("world")
}
`)
	fv := versioning.NewFileVersion(
		"test.go",
		content,
		nil,
		nil,
		"pipeline-1",
		"session-1",
		versioning.VectorClock{},
	)

	ctx := context.Background()
	tree, err := m.ParseVersion(ctx, &fv, content)
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}

	funcOffset := uint(14)
	path := m.ComputeNodePath(tree, funcOffset)
	if len(path) == 0 {
		t.Fatal("expected non-empty path")
	}

	if path[0] != "source_file" {
		t.Errorf("expected path to start with source_file, got %s", path[0])
	}
}

func TestQueryWithGrammar(t *testing.T) {
	skipIfNoGrammarManager(t, "go")

	loader := NewGrammarLoader()
	m := NewTreeSitterManager(loader, 100, 50)
	defer m.Close()

	content := []byte(`package main

func hello() {}
func world() {}
`)
	fv := versioning.NewFileVersion(
		"test.go",
		content,
		nil,
		nil,
		"pipeline-1",
		"session-1",
		versioning.VectorClock{},
	)

	ctx := context.Background()
	tree, err := m.ParseVersion(ctx, &fv, content)
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}

	pattern := `(function_declaration name: (identifier) @func_name)`
	matches, err := m.Query(ctx, tree, "go", pattern)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}

	matches2, err := m.Query(ctx, tree, "go", pattern)
	if err != nil {
		t.Fatalf("second Query: %v", err)
	}
	if len(matches2) != 2 {
		t.Error("cached query should return same results")
	}
}

func TestCreateTargetFromNode(t *testing.T) {
	skipIfNoGrammarManager(t, "go")

	loader := NewGrammarLoader()
	m := NewTreeSitterManager(loader, 100, 50)
	defer m.Close()

	content := []byte(`package main

func hello() {}
`)
	fv := versioning.NewFileVersion(
		"test.go",
		content,
		nil,
		nil,
		"pipeline-1",
		"session-1",
		versioning.VectorClock{},
	)

	ctx := context.Background()
	tree, err := m.ParseVersion(ctx, &fv, content)
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}

	root := tree.RootNode()
	funcNode := root.NamedChild(1)
	if funcNode == nil {
		t.Fatal("expected function node")
	}

	target := m.CreateTargetFromNode(funcNode, tree, "go")

	if !target.IsAST() {
		t.Error("expected AST target")
	}
	if target.Language != "go" {
		t.Errorf("expected language go, got %s", target.Language)
	}
	if target.NodeType != "function_declaration" {
		t.Errorf("expected function_declaration, got %s", target.NodeType)
	}
	if target.NodeID == "" {
		t.Error("expected non-empty NodeID")
	}
}

func TestInvalidateCacheWithTree(t *testing.T) {
	skipIfNoGrammarManager(t, "go")

	loader := NewGrammarLoader()
	m := NewTreeSitterManager(loader, 100, 50)
	defer m.Close()

	content := []byte(`package main`)
	fv := versioning.NewFileVersion(
		"test.go",
		content,
		nil,
		nil,
		"pipeline-1",
		"session-1",
		versioning.VectorClock{},
	)

	ctx := context.Background()
	_, err := m.ParseVersion(ctx, &fv, content)
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}

	if m.getCachedTree(fv.ID) == nil {
		t.Error("expected tree to be cached")
	}

	m.InvalidateCache(fv.ID)

	if m.getCachedTree(fv.ID) != nil {
		t.Error("expected tree to be invalidated")
	}
}

func TestConcurrentParseVersion(t *testing.T) {
	skipIfNoGrammarManager(t, "go")

	loader := NewGrammarLoader()
	m := NewTreeSitterManager(loader, 100, 50)
	defer m.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := []byte(`package main`)
			fv := versioning.NewFileVersion(
				"test.go",
				content,
				nil,
				nil,
				"pipeline-1",
				versioning.SessionID("session-"+uintToStr(uint(idx))),
				versioning.VectorClock{},
			)
			tree, err := m.ParseVersion(ctx, &fv, content)
			if err != nil {
				t.Errorf("ParseVersion[%d]: %v", idx, err)
				return
			}
			_ = tree
		}(i)
	}

	wg.Wait()
}

func TestConcurrentQuery(t *testing.T) {
	skipIfNoGrammarManager(t, "go")

	loader := NewGrammarLoader()
	m := NewTreeSitterManager(loader, 100, 50)
	defer m.Close()

	content := []byte(`package main
func foo() {}
`)
	fv := versioning.NewFileVersion(
		"test.go",
		content,
		nil,
		nil,
		"pipeline-1",
		"session-1",
		versioning.VectorClock{},
	)

	ctx := context.Background()
	tree, err := m.ParseVersion(ctx, &fv, content)
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pattern := `(function_declaration name: (identifier) @func_name)`
			_, err := m.Query(ctx, tree, "go", pattern)
			if err != nil {
				t.Errorf("Query: %v", err)
			}
		}()
	}

	wg.Wait()
}
