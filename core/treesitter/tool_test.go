package treesitter

import (
	"context"
	"testing"
)

func TestNewTreeSitterTool(t *testing.T) {
	tool := NewTreeSitterTool()
	defer tool.Close()

	if tool.manager == nil {
		t.Error("manager should not be nil")
	}
	if tool.loader == nil {
		t.Error("loader should not be nil")
	}
}

func TestToolDetectLanguage(t *testing.T) {
	tool := NewTreeSitterTool()
	defer tool.Close()

	tests := []struct {
		path     string
		expected string
		found    bool
	}{
		{"main.go", "go", true},
		{"script.py", "python", true},
		{"app.js", "javascript", true},
		{"unknown.xyz", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			lang, found := tool.DetectLanguage(tt.path)
			if lang != tt.expected || found != tt.found {
				t.Errorf("DetectLanguage(%q) = (%q, %v), want (%q, %v)",
					tt.path, lang, found, tt.expected, tt.found)
			}
		})
	}
}

func TestToolListAvailableGrammars(t *testing.T) {
	tool := NewTreeSitterTool()
	defer tool.Close()

	grammars := tool.ListAvailableGrammars()
	if len(grammars) == 0 {
		t.Error("expected at least one available grammar")
	}

	hasGo := false
	for _, g := range grammars {
		if g == "go" {
			hasGo = true
			break
		}
	}
	if !hasGo {
		t.Error("expected 'go' in available grammars")
	}
}

func skipIfNoGrammarTool(t *testing.T, name string) {
	t.Helper()
	if !IsGrammarAvailable(name) {
		t.Skipf("grammar %q not available, skipping", name)
	}
}

func TestToolParse(t *testing.T) {
	skipIfNoGrammarTool(t, "go")

	tool := NewTreeSitterTool()
	defer tool.Close()

	content := []byte(`package main

import "fmt"

func hello() {
	fmt.Println("hello")
}

func world(x int) string {
	return "world"
}
`)

	ctx := context.Background()
	result, err := tool.Parse(ctx, "test.go", content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if result.Language != "go" {
		t.Errorf("Language = %q, want go", result.Language)
	}
	if result.FilePath != "test.go" {
		t.Errorf("FilePath = %q, want test.go", result.FilePath)
	}
	if result.RootNode == nil {
		t.Error("RootNode should not be nil")
	}
	if result.RootNode.Type != "source_file" {
		t.Errorf("RootNode.Type = %q, want source_file", result.RootNode.Type)
	}
	if result.ParseTimeMs < 0 {
		t.Error("ParseTimeMs should be non-negative")
	}
}

func TestToolParseFunctions(t *testing.T) {
	skipIfNoGrammarTool(t, "go")

	tool := NewTreeSitterTool()
	defer tool.Close()

	content := []byte(`package main

func hello() {}

func world(x int) string {
	return "world"
}
`)

	ctx := context.Background()
	result, err := tool.Parse(ctx, "test.go", content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Functions) != 2 {
		t.Errorf("expected 2 functions, got %d", len(result.Functions))
	}

	var hello, world *FunctionInfo
	for i := range result.Functions {
		switch result.Functions[i].Name {
		case "hello":
			hello = &result.Functions[i]
		case "world":
			world = &result.Functions[i]
		}
	}

	if hello == nil {
		t.Error("expected to find function 'hello'")
	}
	if world == nil {
		t.Error("expected to find function 'world'")
	}

	if world != nil && world.Parameters == "" {
		t.Error("world should have parameters")
	}
}

func TestToolParseTypes(t *testing.T) {
	skipIfNoGrammarTool(t, "go")

	tool := NewTreeSitterTool()
	defer tool.Close()

	content := []byte(`package main

type MyStruct struct {
	Name string
}

type MyInterface interface {
	Do()
}
`)

	ctx := context.Background()
	result, err := tool.Parse(ctx, "test.go", content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Types) != 2 {
		t.Errorf("expected 2 types, got %d", len(result.Types))
	}
}

func TestToolParseImports(t *testing.T) {
	skipIfNoGrammarTool(t, "go")

	tool := NewTreeSitterTool()
	defer tool.Close()

	content := []byte(`package main

import (
	"fmt"
	alias "strings"
)
`)

	ctx := context.Background()
	result, err := tool.Parse(ctx, "test.go", content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Imports) != 2 {
		t.Errorf("expected 2 imports, got %d", len(result.Imports))
	}
}

func TestToolParseErrorsRecovery(t *testing.T) {
	skipIfNoGrammarTool(t, "go")

	tool := NewTreeSitterTool()
	defer tool.Close()

	content := []byte(`package main

func broken( {
}
`)

	ctx := context.Background()
	result, err := tool.Parse(ctx, "test.go", content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if result.RootNode == nil {
		t.Error("RootNode should not be nil even with syntax errors")
	}
}

func TestToolParseUnknownLanguage(t *testing.T) {
	tool := NewTreeSitterTool()
	defer tool.Close()

	ctx := context.Background()
	_, err := tool.Parse(ctx, "test.unknown", []byte("content"))
	if err != ErrGrammarNotFound {
		t.Errorf("expected ErrGrammarNotFound, got %v", err)
	}
}

func TestToolQuery(t *testing.T) {
	skipIfNoGrammarTool(t, "go")

	tool := NewTreeSitterTool()
	defer tool.Close()

	content := []byte(`package main

func hello() {}
func world() {}
`)

	ctx := context.Background()
	pattern := `(function_declaration name: (identifier) @func_name)`
	matches, err := tool.Query(ctx, "test.go", content, pattern)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}

	for _, m := range matches {
		if len(m.Captures) == 0 {
			t.Error("expected captures in match")
		}
	}
}

func TestToolQueryUnknownLanguage(t *testing.T) {
	tool := NewTreeSitterTool()
	defer tool.Close()

	ctx := context.Background()
	_, err := tool.Query(ctx, "test.unknown", []byte("content"), "(identifier)")
	if err != ErrGrammarNotFound {
		t.Errorf("expected ErrGrammarNotFound, got %v", err)
	}
}

func TestNodeToInfo(t *testing.T) {
	t.Run("nil node", func(t *testing.T) {
		info := nodeToInfo(nil, 0)
		if info != nil {
			t.Error("expected nil for nil node")
		}
	})
}

func TestDeduplicateStrings(t *testing.T) {
	tests := []struct {
		input    []string
		expected int
	}{
		{[]string{"a", "b", "c"}, 3},
		{[]string{"a", "a", "a"}, 1},
		{[]string{"a", "b", "a", "c", "b"}, 3},
		{[]string{}, 0},
	}

	for _, tt := range tests {
		result := deduplicateStrings(tt.input)
		if len(result) != tt.expected {
			t.Errorf("deduplicateStrings(%v) len = %d, want %d",
				tt.input, len(result), tt.expected)
		}
	}
}

func TestGetFieldContent(t *testing.T) {
	skipIfNoGrammarTool(t, "go")

	tool := NewTreeSitterTool()
	defer tool.Close()

	content := []byte(`package main

func hello() {}
`)

	ctx := context.Background()
	result, err := tool.Parse(ctx, "test.go", content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Functions) == 0 {
		t.Skip("no functions found, skipping")
	}

	if result.Functions[0].Name != "hello" {
		t.Errorf("expected function name 'hello', got %q", result.Functions[0].Name)
	}
}
