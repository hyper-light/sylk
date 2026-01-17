package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/treesitter"
)

func skipIfNoGrammar(t *testing.T, name string) {
	t.Helper()
	if !treesitter.IsGrammarAvailable(name) {
		t.Skipf("grammar %q not available, skipping", name)
	}
}

func createTestFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewLibrarianSkills(t *testing.T) {
	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	if skills.tool == nil {
		t.Error("tool should not be nil")
	}
}

func TestTsParse(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
`)

	ctx := context.Background()
	output, err := skills.TsParse(ctx, path, ParseOptions{})
	if err != nil {
		t.Fatalf("TsParse: %v", err)
	}

	if output.Language != "go" {
		t.Errorf("Language = %q, want go", output.Language)
	}
	if output.RootNode == nil {
		t.Error("RootNode should not be nil")
	}
	if len(output.Functions) != 1 {
		t.Errorf("expected 1 function, got %d", len(output.Functions))
	}
}

func TestTsParseFileNotFound(t *testing.T) {
	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	ctx := context.Background()
	_, err := skills.TsParse(ctx, "/nonexistent/file.go", ParseOptions{})
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestTsQuery(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
func world() {}
`)

	ctx := context.Background()
	output, err := skills.TsQuery(ctx, path, `(function_declaration name: (identifier) @name)`, QueryOptions{})
	if err != nil {
		t.Fatalf("TsQuery: %v", err)
	}

	if len(output.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(output.Matches))
	}
}

func TestTsQueryWithCaptureName(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
`)

	ctx := context.Background()
	output, err := skills.TsQuery(ctx, path, `(function_declaration name: (identifier) @name)`, QueryOptions{
		CaptureName: "name",
	})
	if err != nil {
		t.Fatalf("TsQuery: %v", err)
	}

	if len(output.Matches) == 0 {
		t.Error("expected at least one match")
	}

	for _, m := range output.Matches {
		for _, c := range m.Captures {
			if c.Name != "name" {
				t.Errorf("expected capture name 'name', got %q", c.Name)
			}
		}
	}
}

func TestTsFindFunctions(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
func world(x int) string { return "" }
`)

	ctx := context.Background()
	output, err := skills.TsFindFunctions(ctx, path, FunctionOptions{})
	if err != nil {
		t.Fatalf("TsFindFunctions: %v", err)
	}

	if len(output.Functions) != 2 {
		t.Errorf("expected 2 functions, got %d", len(output.Functions))
	}
}

func TestTsFindTypes(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

type MyStruct struct {
	Name string
}
`)

	ctx := context.Background()
	output, err := skills.TsFindTypes(ctx, path, TypeOptions{IncludeFields: true})
	if err != nil {
		t.Fatalf("TsFindTypes: %v", err)
	}

	if len(output.Types) != 1 {
		t.Errorf("expected 1 type, got %d", len(output.Types))
	}

	if output.Types[0].Name != "MyStruct" {
		t.Errorf("expected type name 'MyStruct', got %q", output.Types[0].Name)
	}
}

func TestTsFindImports(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

import (
	"fmt"
	alias "strings"
)
`)

	ctx := context.Background()
	output, err := skills.TsFindImports(ctx, path)
	if err != nil {
		t.Fatalf("TsFindImports: %v", err)
	}

	if len(output.Imports) != 2 {
		t.Errorf("expected 2 imports, got %d", len(output.Imports))
	}
}

func TestTsFindReferences(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {
	x := 1
	y := x + x
	_ = y
}
`)

	ctx := context.Background()
	output, err := skills.TsFindReferences(ctx, path, "x", ReferenceOptions{})
	if err != nil {
		t.Fatalf("TsFindReferences: %v", err)
	}

	if output.Symbol != "x" {
		t.Errorf("expected symbol 'x', got %q", output.Symbol)
	}

	if len(output.References) < 2 {
		t.Errorf("expected at least 2 references to 'x', got %d", len(output.References))
	}
}

func TestTsExtractSymbols(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

type MyType struct{}

func hello() {}
func world() {}
`)

	ctx := context.Background()
	output, err := skills.TsExtractSymbols(ctx, path, ExtractSymbolsOptions{})
	if err != nil {
		t.Fatalf("TsExtractSymbols: %v", err)
	}

	if len(output.Symbols) < 3 {
		t.Errorf("expected at least 3 symbols, got %d", len(output.Symbols))
	}
}

func TestTsExtractSymbolsFiltered(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewLibrarianSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

type MyType struct{}

func hello() {}
`)

	ctx := context.Background()
	output, err := skills.TsExtractSymbols(ctx, path, ExtractSymbolsOptions{
		Types: []string{"function"},
	})
	if err != nil {
		t.Fatalf("TsExtractSymbols: %v", err)
	}

	for _, s := range output.Symbols {
		if s.Kind != "function" {
			t.Errorf("expected only functions, got kind %q", s.Kind)
		}
	}
}

func TestMakeTypeSet(t *testing.T) {
	tests := []struct {
		input    []string
		expected int
	}{
		{nil, 0},
		{[]string{}, 0},
		{[]string{"a"}, 1},
		{[]string{"a", "b"}, 2},
	}

	for _, tt := range tests {
		set := makeTypeSet(tt.input)
		if tt.expected == 0 && set != nil {
			t.Error("expected nil for empty input")
		}
		if tt.expected > 0 && len(set) != tt.expected {
			t.Errorf("expected %d, got %d", tt.expected, len(set))
		}
	}
}

func TestShouldInclude(t *testing.T) {
	tests := []struct {
		typeSet  map[string]bool
		kind     string
		expected bool
	}{
		{nil, "function", true},
		{nil, "type", true},
		{map[string]bool{"function": true}, "function", true},
		{map[string]bool{"function": true}, "type", false},
	}

	for _, tt := range tests {
		got := shouldInclude(tt.typeSet, tt.kind)
		if got != tt.expected {
			t.Errorf("shouldInclude(%v, %q) = %v, want %v",
				tt.typeSet, tt.kind, got, tt.expected)
		}
	}
}
