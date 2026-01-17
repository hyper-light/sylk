package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/treesitter"
)

func TestNewInspectorSkills(t *testing.T) {
	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewInspectorSkills(tool)
	defer skills.Close()

	if skills.tool == nil {
		t.Error("tool should not be nil")
	}
}

func TestTsComplexityAnalysis(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewInspectorSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func simple() {}
func another() {}
`)

	ctx := context.Background()
	result, err := skills.TsComplexityAnalysis(ctx, []string{path}, ComplexityThresholds{
		Cyclomatic: 10,
		Cognitive:  15,
		Nesting:    4,
	})
	if err != nil {
		t.Fatalf("TsComplexityAnalysis: %v", err)
	}

	if len(result.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(result.Files))
	}
	if len(result.Files[0].Functions) != 2 {
		t.Errorf("expected 2 functions, got %d", len(result.Files[0].Functions))
	}
}

func TestTsFindCodeSmells(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewInspectorSkills(tool)
	defer skills.Close()

	var longFunc string
	longFunc = "package main\n\nfunc longMethod() {\n"
	for i := 0; i < 55; i++ {
		longFunc += "\t_ = 1\n"
	}
	longFunc += "}\n"

	path := createTestFile(t, longFunc)

	ctx := context.Background()
	result, err := skills.TsFindCodeSmells(ctx, []string{path}, []string{"long_method"})
	if err != nil {
		t.Fatalf("TsFindCodeSmells: %v", err)
	}

	if len(result.Smells) != 1 {
		t.Errorf("expected 1 smell, got %d", len(result.Smells))
	}
	if len(result.Smells) > 0 && result.Smells[0].Type != "long_method" {
		t.Errorf("expected long_method smell, got %q", result.Smells[0].Type)
	}
}

func TestTsFindCodeSmellsNoSmells(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewInspectorSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func short() {
	x := 1
	_ = x
}
`)

	ctx := context.Background()
	result, err := skills.TsFindCodeSmells(ctx, []string{path}, []string{"long_method"})
	if err != nil {
		t.Fatalf("TsFindCodeSmells: %v", err)
	}

	if len(result.Smells) != 0 {
		t.Errorf("expected 0 smells, got %d", len(result.Smells))
	}
}

func TestTsValidateStructure(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewInspectorSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
`)

	ctx := context.Background()
	result, err := skills.TsValidateStructure(ctx, []string{path},
		[]string{`(function_declaration) @func`},
		nil,
	)
	if err != nil {
		t.Fatalf("TsValidateStructure: %v", err)
	}

	if !result.Valid {
		t.Error("expected valid result")
	}
	if len(result.Matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(result.Matches))
	}
}

func TestTsValidateStructureForbidden(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewInspectorSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
`)

	ctx := context.Background()
	result, err := skills.TsValidateStructure(ctx, []string{path},
		nil,
		[]string{`(function_declaration) @func`},
	)
	if err != nil {
		t.Fatalf("TsValidateStructure: %v", err)
	}

	if result.Valid {
		t.Error("expected invalid result due to forbidden pattern")
	}
	if len(result.Failures) != 1 {
		t.Errorf("expected 1 failure, got %d", len(result.Failures))
	}
}

func TestTsCountNodes(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewInspectorSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
func world() {}
`)

	ctx := context.Background()
	result, err := skills.TsCountNodes(ctx, []string{path}, []string{"function_declaration"})
	if err != nil {
		t.Fatalf("TsCountNodes: %v", err)
	}

	if len(result.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(result.Files))
	}
	if result.Total["function_declaration"] != 2 {
		t.Errorf("expected 2 function_declarations, got %d", result.Total["function_declaration"])
	}
}

func TestTsParseErrors(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewInspectorSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
`)

	ctx := context.Background()
	result, err := skills.TsParseErrors(ctx, []string{path})
	if err != nil {
		t.Fatalf("TsParseErrors: %v", err)
	}

	if len(result.Files) != 0 {
		t.Errorf("expected 0 files with errors, got %d", len(result.Files))
	}
}

func TestMakeSmellSet(t *testing.T) {
	tests := []struct {
		input    []string
		expected int
	}{
		{nil, 0},
		{[]string{}, 0},
		{[]string{"long_method"}, 1},
		{[]string{"long_method", "deep_nesting"}, 2},
	}

	for _, tt := range tests {
		set := makeSmellSet(tt.input)
		if len(set) != tt.expected {
			t.Errorf("makeSmellSet(%v) = %d items, want %d", tt.input, len(set), tt.expected)
		}
	}
}

func TestComputeFunctionComplexity(t *testing.T) {
	f := treesitter.FunctionInfo{
		Name:      "testFunc",
		StartLine: 10,
		EndLine:   20,
	}

	result := computeFunctionComplexity(f)

	if result.Name != "testFunc" {
		t.Errorf("Name = %q, want testFunc", result.Name)
	}
	if result.Cyclomatic != 1 {
		t.Errorf("Cyclomatic = %d, want 1", result.Cyclomatic)
	}
	if result.StartLine != 10 {
		t.Errorf("StartLine = %d, want 10", result.StartLine)
	}
}

func TestFindLongMethods(t *testing.T) {
	functions := []treesitter.FunctionInfo{
		{Name: "short", StartLine: 1, EndLine: 10},
		{Name: "long", StartLine: 20, EndLine: 100},
	}

	smells := findLongMethods(functions, "/test.go")

	if len(smells) != 1 {
		t.Errorf("expected 1 smell, got %d", len(smells))
	}
	if len(smells) > 0 && smells[0].Type != "long_method" {
		t.Errorf("expected long_method type, got %q", smells[0].Type)
	}
}

func createInspectorTestFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
