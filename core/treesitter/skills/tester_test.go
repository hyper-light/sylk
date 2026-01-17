package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/treesitter"
)

func TestNewTesterSkills(t *testing.T) {
	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewTesterSkills(tool)
	defer skills.Close()

	if skills.tool == nil {
		t.Error("tool should not be nil")
	}
}

func TestTsDiscoverTests(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewTesterSkills(tool)
	defer skills.Close()

	path := createTesterTestFile(t, "example_test.go", `package main

func TestHello(t *testing.T) {}
func TestWorld(t *testing.T) {}
`)

	ctx := context.Background()
	result, err := skills.TsDiscoverTests(ctx, []string{path}, DiscoverTestsOptions{})
	if err != nil {
		t.Fatalf("TsDiscoverTests: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("expected 2 tests, got %d", result.Total)
	}
	if len(result.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(result.Files))
	}
}

func TestTsDiscoverTestsNoTests(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewTesterSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func hello() {}
`)

	ctx := context.Background()
	result, err := skills.TsDiscoverTests(ctx, []string{path}, DiscoverTestsOptions{})
	if err != nil {
		t.Fatalf("TsDiscoverTests: %v", err)
	}

	if result.Total != 0 {
		t.Errorf("expected 0 tests, got %d", result.Total)
	}
}

func TestTsFindTestableFunctions(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewTesterSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func PublicFunc() {}
func privateFunc() {}
`)

	ctx := context.Background()
	result, err := skills.TsFindTestableFunctions(ctx, []string{path}, TestableFunctionsOptions{})
	if err != nil {
		t.Fatalf("TsFindTestableFunctions: %v", err)
	}

	if len(result.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(result.Files))
	}
	if len(result.Files[0].Functions) != 2 {
		t.Errorf("expected 2 functions, got %d", len(result.Files[0].Functions))
	}
}

func TestTsFindTestableFunctionsExcludePrivate(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewTesterSkills(tool)
	defer skills.Close()

	path := createTestFile(t, `package main

func PublicFunc() {}
func privateFunc() {}
`)

	ctx := context.Background()
	result, err := skills.TsFindTestableFunctions(ctx, []string{path}, TestableFunctionsOptions{
		ExcludePrivate: true,
	})
	if err != nil {
		t.Fatalf("TsFindTestableFunctions: %v", err)
	}

	if len(result.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(result.Files))
	}
	for _, file := range result.Files {
		for _, f := range file.Functions {
			if !f.IsPublic {
				t.Errorf("found private function %q when excludePrivate=true", f.Name)
			}
		}
	}
}

func TestTsFindTestableFunctionsSkipsTestFiles(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewTesterSkills(tool)
	defer skills.Close()

	path := createTesterTestFile(t, "example_test.go", `package main

func TestSomething(t *testing.T) {}
`)

	ctx := context.Background()
	result, err := skills.TsFindTestableFunctions(ctx, []string{path}, TestableFunctionsOptions{})
	if err != nil {
		t.Fatalf("TsFindTestableFunctions: %v", err)
	}

	if len(result.Files) != 0 {
		t.Errorf("expected 0 files (test file should be skipped), got %d", len(result.Files))
	}
}

func TestTsAnalyzeTestStructure(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewTesterSkills(tool)
	defer skills.Close()

	path := createTesterTestFile(t, "example_test.go", `package main

func TestOne(t *testing.T) {}
func TestTwo(t *testing.T) {}
func setupTest() {}
`)

	ctx := context.Background()
	result, err := skills.TsAnalyzeTestStructure(ctx, path)
	if err != nil {
		t.Fatalf("TsAnalyzeTestStructure: %v", err)
	}

	if result.TestCount != 2 {
		t.Errorf("expected 2 tests, got %d", result.TestCount)
	}
	if len(result.SetupFuncs) != 1 {
		t.Errorf("expected 1 setup function, got %d", len(result.SetupFuncs))
	}
}

func TestTsFindAssertions(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewTesterSkills(tool)
	defer skills.Close()

	path := createTesterTestFile(t, "example_test.go", `package main

import "testing"

func TestExample(t *testing.T) {
	t.Error("failed")
	t.Fatal("really failed")
}
`)

	ctx := context.Background()
	result, err := skills.TsFindAssertions(ctx, []string{path})
	if err != nil {
		t.Fatalf("TsFindAssertions: %v", err)
	}

	if result.Total < 2 {
		t.Errorf("expected at least 2 assertions, got %d", result.Total)
	}
}

func TestTsMatchTestsToFunctions(t *testing.T) {
	skipIfNoGrammar(t, "go")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewTesterSkills(tool)
	defer skills.Close()

	dir := t.TempDir()

	sourcePath := filepath.Join(dir, "example.go")
	if err := os.WriteFile(sourcePath, []byte(`package main

func Hello() {}
func World() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	testPath := filepath.Join(dir, "example_test.go")
	if err := os.WriteFile(testPath, []byte(`package main

func TestHello(t *testing.T) {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	result, err := skills.TsMatchTestsToFunctions(ctx, sourcePath, testPath)
	if err != nil {
		t.Fatalf("TsMatchTestsToFunctions: %v", err)
	}

	if len(result.Mappings) != 1 {
		t.Errorf("expected 1 mapping, got %d", len(result.Mappings))
	}
	if len(result.Untested) != 1 {
		t.Errorf("expected 1 untested function, got %d", len(result.Untested))
	}
}

func TestIsTestFunction(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"TestSomething", true},
		{"test_something", true},
		{"TestA", true},
		{"test_a", true},
		{"NotATest", false},
		{"testLowerCase", false},
		{"setupTest", false},
	}

	for _, tt := range tests {
		got := isTestFunction(tt.name)
		if got != tt.expected {
			t.Errorf("isTestFunction(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestIsPublicFunction(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"PublicFunc", true},
		{"privateFunc", false},
		{"A", true},
		{"a", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isPublicFunction(tt.name)
		if got != tt.expected {
			t.Errorf("isPublicFunction(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestIsHelperFunction(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"testHelper", true},
		{"helperFunc", true},
		{"createHelper", true},
		{"regular", false},
	}

	for _, tt := range tests {
		got := isHelperFunction(tt.name)
		if got != tt.expected {
			t.Errorf("isHelperFunction(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestIsSetupFunction(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"setup", true},
		{"SetupTest", true},
		{"teardown", true},
		{"TearDown", true},
		{"regular", false},
	}

	for _, tt := range tests {
		got := isSetupFunction(tt.name)
		if got != tt.expected {
			t.Errorf("isSetupFunction(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestIsAssertionMethod(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"Error", true},
		{"Fatal", true},
		{"Equal", true},
		{"NotEqual", true},
		{"regular", false},
	}

	for _, tt := range tests {
		got := isAssertionMethod(tt.name)
		if got != tt.expected {
			t.Errorf("isAssertionMethod(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func createTesterTestFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
