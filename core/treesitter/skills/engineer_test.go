package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/treesitter"
)

func TestNewEngineerSkills(t *testing.T) {
	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewEngineerSkills(tool)
	defer skills.Close()

	if skills.tool == nil {
		t.Error("tool should not be nil")
	}
}

func TestTsRenameSymbol(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewEngineerSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {
	foo := 1
	bar := foo + foo
	_ = bar
}
`)

	ctx := context.Background()
	result, err := skills.TsRenameSymbol(ctx, path, "foo", "baz", RenameOptions{DryRun: true})
	if err != nil {
		t.Fatalf("TsRenameSymbol: %v", err)
	}

	if result.OldName != "foo" {
		t.Errorf("OldName = %q, want foo", result.OldName)
	}
	if result.NewName != "baz" {
		t.Errorf("NewName = %q, want baz", result.NewName)
	}
	if len(result.Occurrences) < 2 {
		t.Errorf("expected at least 2 occurrences, got %d", len(result.Occurrences))
	}
}

func TestTsRenameSymbolNoMatch(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewEngineerSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
`)

	ctx := context.Background()
	result, err := skills.TsRenameSymbol(ctx, path, "nonexistent", "new", RenameOptions{})
	if err != nil {
		t.Fatalf("TsRenameSymbol: %v", err)
	}

	if len(result.Occurrences) != 0 {
		t.Errorf("expected 0 occurrences, got %d", len(result.Occurrences))
	}
}

func TestTsExtractFunction(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewEngineerSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {
	x := 1
	y := 2
	z := x + y
	_ = z
}
`)

	ctx := context.Background()
	result, err := skills.TsExtractFunction(ctx, path, 4, 6, "add", ExtractFunctionOptions{DryRun: true})
	if err != nil {
		t.Fatalf("TsExtractFunction: %v", err)
	}

	if result.FunctionName != "add" {
		t.Errorf("FunctionName = %q, want add", result.FunctionName)
	}
	if result.StartLine != 4 || result.EndLine != 6 {
		t.Errorf("range = [%d, %d], want [4, 6]", result.StartLine, result.EndLine)
	}
}

func TestTsFindEditTargets(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewEngineerSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
func world() {}
`)

	ctx := context.Background()
	result, err := skills.TsFindEditTargets(ctx, path, "function_declaration", FindEditTargetsOptions{})
	if err != nil {
		t.Fatalf("TsFindEditTargets: %v", err)
	}

	if len(result.Targets) != 2 {
		t.Errorf("expected 2 targets, got %d", len(result.Targets))
	}
}

func TestTsFindEditTargetsWithPattern(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewEngineerSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
func world() {}
`)

	ctx := context.Background()
	result, err := skills.TsFindEditTargets(ctx, path, "function_declaration", FindEditTargetsOptions{
		NamePattern: "hello",
	})
	if err != nil {
		t.Fatalf("TsFindEditTargets: %v", err)
	}

	found := false
	for _, target := range result.Targets {
		if target.NodeType == "function_declaration" {
			found = true
		}
	}
	if !found && len(result.Targets) > 0 {
		t.Error("expected function_declaration targets")
	}
}

func TestTsGetNodeAt(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewEngineerSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
`)

	ctx := context.Background()
	result, err := skills.TsGetNodeAt(ctx, path, 3, 5, GetNodeAtOptions{})
	if err != nil {
		t.Fatalf("TsGetNodeAt: %v", err)
	}

	if result.Node == nil {
		t.Error("Node should not be nil")
	}
}

func TestTsRenameSymbolFileNotFound(t *testing.T) {
	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewEngineerSkills(tool)
	defer skills.Close()

	ctx := context.Background()
	_, err := skills.TsRenameSymbol(ctx, "/nonexistent/file.go", "old", "new", RenameOptions{})
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestCollectRenameLocations(t *testing.T) {
	captures := []treesitter.ToolCapture{
		{Content: "foo", Node: &treesitter.NodeInfo{StartLine: 1, StartCol: 5}},
		{Content: "bar", Node: &treesitter.NodeInfo{StartLine: 2, StartCol: 5}},
		{Content: "foo", Node: &treesitter.NodeInfo{StartLine: 3, StartCol: 10}},
	}

	var locs []RenameLocation
	collectRenameLocations(captures, "foo", &locs)

	if len(locs) != 2 {
		t.Errorf("expected 2 locations, got %d", len(locs))
	}
}

func TestIsInRange(t *testing.T) {
	node := &treesitter.NodeInfo{StartLine: 5, EndLine: 10}

	tests := []struct {
		start, end int
		expected   bool
	}{
		{1, 15, true},
		{5, 10, true},
		{1, 4, false},
		{11, 15, false},
	}

	for _, tt := range tests {
		got := isInRange(node, tt.start, tt.end)
		if got != tt.expected {
			t.Errorf("isInRange(%d, %d) = %v, want %v", tt.start, tt.end, got, tt.expected)
		}
	}
}

func createEngineerTestFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
