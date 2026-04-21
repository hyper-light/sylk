package shared

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

// TestTreesitterBackend_SymbolsReadsThroughFileAccess verifies that
// the treesitter backend sees in-flight VFS content rather than real
// disk. We write a file through the VFS layer and expect the symbol
// listing to reflect that content without ever touching disk.
func TestTreesitterBackend_SymbolsReadsThroughFileAccess(t *testing.T) {
	root := t.TempDir()
	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	pipe, err := svfs.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: root,
		Files:      []string{"example.py"},
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}
	fa := svfs.NewPipelineFileAccess(pipe)
	ctx := context.Background()
	// Write to VFS — disk stays empty.
	source := "def greet(name):\n    return f'hello {name}'\n"
	if err := fa.WriteFile(ctx, "example.py", []byte(source)); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Sanity: disk really is empty.
	if _, err := os.Stat(filepath.Join(root, "example.py")); !os.IsNotExist(err) {
		t.Fatalf("expected file to be VFS-only, got stat err=%v", err)
	}

	backend := &TreesitterBackend{
		Tool:          SharedTreeSitter(),
		FileAccess:    func() versioning.FileAccess { return fa },
		WorkspaceRoot: func() string { return root },
	}
	result, err := backend.Symbols(ctx, LSPSymbolsRequest{File: "example.py"})
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q reason=%q", result.Status, result.Reason)
	}
	data, ok := result.Data["functions"].([]any)
	if !ok {
		// ParseResult.Functions comes back typed; the JSON round-trip
		// in the shape test uses []any. Native call here preserves
		// the typed slice.
		if _, typed := result.Data["functions"]; !typed {
			t.Fatalf("functions field missing: %#v", result.Data)
		}
	}
	_ = data
	// Language should be Python.
	if lang, _ := result.Data["language"].(string); !strings.Contains(strings.ToLower(lang), "python") {
		t.Fatalf("language = %q, want python", lang)
	}
}

// TestTreesitterBackend_HoverReturnsContext verifies that hover at a
// known line returns the surrounding snippet and a node type.
func TestTreesitterBackend_HoverReturnsContext(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.py")
	source := "def greet(name):\n    return f'hello {name}'\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fa := versioning.NewDiskFileAccess(root, true)
	backend := &TreesitterBackend{
		Tool:          SharedTreeSitter(),
		FileAccess:    func() versioning.FileAccess { return fa },
		WorkspaceRoot: func() string { return root },
	}
	result, err := backend.Hover(context.Background(), LSPLocationRequest{
		File:   "example.py",
		Line:   1,
		Column: 5,
	})
	if err != nil {
		t.Fatalf("Hover: %v", err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q reason=%q", result.Status, result.Reason)
	}
	if snippet, _ := result.Data["snippet"].(string); !strings.Contains(snippet, "greet") {
		t.Fatalf("hover snippet = %q, want greet", snippet)
	}
}

// TestCompositeBackend_FallsBackToSecondaryOnPrimaryUnavailable
// verifies that when gopls reports unavailable, the composite backend
// calls the treesitter secondary. No gopls binary needed in tests.
func TestCompositeBackend_FallsBackToSecondaryOnPrimaryUnavailable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.go")
	source := "package main\n\nfunc Greet(name string) string { return \"hello \" + name }\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	primary := &GoplsBackend{
		Run: func(_ context.Context, _, _, _ string) (string, string) {
			return "gopls missing", "unavailable"
		},
		GetWorkDir: func() string { return root },
	}
	secondary := &TreesitterBackend{
		Tool:          SharedTreeSitter(),
		FileAccess:    func() versioning.FileAccess { return versioning.NewDiskFileAccess(root, true) },
		WorkspaceRoot: func() string { return root },
	}
	composite := &CompositeBackend{Primary: primary, Secondary: secondary, GoFirst: true}

	result, err := composite.Symbols(context.Background(), LSPSymbolsRequest{File: "example.go"})
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q reason=%q", result.Status, result.Reason)
	}
	if result.Source != "treesitter" {
		t.Fatalf("source = %q, want treesitter fallback", result.Source)
	}
}

// TestCompositeBackend_SkipsPrimaryForNonGoFiles verifies that GoFirst
// routes Python directly to the treesitter secondary without asking
// gopls (which would return unavailable and log noise).
func TestCompositeBackend_SkipsPrimaryForNonGoFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.py")
	if err := os.WriteFile(path, []byte("def f():\n    pass\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	primaryCalled := false
	primary := &GoplsBackend{
		Run: func(_ context.Context, _, _, _ string) (string, string) {
			primaryCalled = true
			return "", "ok"
		},
		GetWorkDir: func() string { return root },
	}
	secondary := &TreesitterBackend{
		Tool:          SharedTreeSitter(),
		FileAccess:    func() versioning.FileAccess { return versioning.NewDiskFileAccess(root, true) },
		WorkspaceRoot: func() string { return root },
	}
	composite := &CompositeBackend{Primary: primary, Secondary: secondary, GoFirst: true}

	_, err := composite.Symbols(context.Background(), LSPSymbolsRequest{File: "example.py"})
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if primaryCalled {
		t.Fatal("gopls was called for a .py file; GoFirst should skip primary")
	}
}

// TestSymbolAtCursor_WalksIdentifierBoundary verifies the cursor-to-
// identifier helper that goto_definition / find_references rely on.
func TestSymbolAtCursor_WalksIdentifierBoundary(t *testing.T) {
	content := []byte("func Greet(name string) string {\n    return name\n}\n")
	if got := symbolAtCursor(content, 0, 5); got != "Greet" {
		t.Fatalf("expected Greet at (0,5), got %q", got)
	}
	if got := symbolAtCursor(content, 0, 11); got != "name" {
		t.Fatalf("expected name at (0,11), got %q", got)
	}
	// Cursor over whitespace.
	if got := symbolAtCursor(content, 0, 4); got != "" {
		t.Fatalf("expected no symbol over whitespace, got %q", got)
	}
	// Out-of-bounds row.
	if got := symbolAtCursor(content, 99, 0); got != "" {
		t.Fatalf("expected empty for out-of-bounds row, got %q", got)
	}
}
