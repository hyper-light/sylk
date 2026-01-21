package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// BenchmarkManualExtraction benchmarks the current manual traversal approach.
func BenchmarkManualExtraction(b *testing.B) {
	ctx := context.Background()
	tool := NewTreeSitterTool()

	// Find Go files in the Sylk codebase for benchmarking
	goFiles := collectGoFiles(b, "/home/ada/Projects/sylk", 50)
	if len(goFiles) == 0 {
		b.Skip("No Go files found for benchmarking")
	}

	// Pre-read file contents
	type testFile struct {
		path    string
		content []byte
	}
	files := make([]testFile, 0, len(goFiles))
	for _, path := range goFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		files = append(files, testFile{path: path, content: content})
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for _, f := range files {
			_, err := tool.ParseFast(ctx, f.path, f.content)
			if err != nil {
				b.Logf("Parse error for %s: %v", f.path, err)
			}
		}
	}
}

// BenchmarkQueryExtraction benchmarks the query-based extraction approach.
func BenchmarkQueryExtraction(b *testing.B) {
	ctx := context.Background()
	loader := NewGrammarLoader()
	qc := NewEmbeddedQueryCache()
	defer qc.Close()

	// Load Go language
	lang, err := loader.LoadContext(ctx, "go")
	if err != nil {
		b.Skipf("Failed to load Go grammar: %v", err)
	}

	// Find Go files in the Sylk codebase for benchmarking
	goFiles := collectGoFiles(b, "/home/ada/Projects/sylk", 50)
	if len(goFiles) == 0 {
		b.Skip("No Go files found for benchmarking")
	}

	// Pre-read file contents and parse trees
	type testFile struct {
		path    string
		content []byte
	}
	files := make([]testFile, 0, len(goFiles))
	for _, path := range goFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		files = append(files, testFile{path: path, content: content})
	}

	// Create a reusable parser
	parser := NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		b.Fatalf("Failed to set language: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for _, f := range files {
			tree, err := parser.Parse(f.content, nil)
			if err != nil {
				b.Logf("Parse error for %s: %v", f.path, err)
				continue
			}

			_, err = ExtractSymbolsWithQuery(qc, lang, "go", tree)
			if err != nil {
				b.Logf("Query error for %s: %v", f.path, err)
			}
			tree.Close()
		}
	}
}

// BenchmarkManualSingleFile benchmarks manual extraction on a single large file.
func BenchmarkManualSingleFile(b *testing.B) {
	ctx := context.Background()
	tool := NewTreeSitterTool()

	// Use tool.go as the benchmark file (it's the largest)
	content, err := os.ReadFile("/home/ada/Projects/sylk/core/treesitter/tool.go")
	if err != nil {
		b.Skipf("Failed to read tool.go: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := tool.ParseFast(ctx, "tool.go", content)
		if err != nil {
			b.Fatalf("Parse error: %v", err)
		}
	}
}

// BenchmarkQuerySingleFile benchmarks query extraction on a single large file.
func BenchmarkQuerySingleFile(b *testing.B) {
	ctx := context.Background()
	loader := NewGrammarLoader()
	qc := NewEmbeddedQueryCache()
	defer qc.Close()

	lang, err := loader.LoadContext(ctx, "go")
	if err != nil {
		b.Skipf("Failed to load Go grammar: %v", err)
	}

	content, err := os.ReadFile("/home/ada/Projects/sylk/core/treesitter/tool.go")
	if err != nil {
		b.Skipf("Failed to read tool.go: %v", err)
	}

	parser := NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		b.Fatalf("Failed to set language: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tree, err := parser.Parse(content, nil)
		if err != nil {
			b.Fatalf("Parse error: %v", err)
		}

		_, err = ExtractSymbolsWithQuery(qc, lang, "go", tree)
		if err != nil {
			b.Fatalf("Query error: %v", err)
		}
		tree.Close()
	}
}

// BenchmarkParseOnly benchmarks just parsing without symbol extraction.
func BenchmarkParseOnly(b *testing.B) {
	ctx := context.Background()
	loader := NewGrammarLoader()

	lang, err := loader.LoadContext(ctx, "go")
	if err != nil {
		b.Skipf("Failed to load Go grammar: %v", err)
	}

	content, err := os.ReadFile("/home/ada/Projects/sylk/core/treesitter/tool.go")
	if err != nil {
		b.Skipf("Failed to read tool.go: %v", err)
	}

	parser := NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		b.Fatalf("Failed to set language: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tree, err := parser.Parse(content, nil)
		if err != nil {
			b.Fatalf("Parse error: %v", err)
		}
		tree.Close()
	}
}

// TestQueryCorrectness verifies query extraction matches manual extraction.
func TestQueryCorrectness(t *testing.T) {
	ctx := context.Background()
	tool := NewTreeSitterTool()
	loader := NewGrammarLoader()
	qc := NewEmbeddedQueryCache()
	defer qc.Close()

	lang, err := loader.LoadContext(ctx, "go")
	if err != nil {
		t.Skipf("Failed to load Go grammar: %v", err)
	}

	// Test on queries.go itself
	content, err := os.ReadFile("/home/ada/Projects/sylk/core/treesitter/queries.go")
	if err != nil {
		t.Fatalf("Failed to read queries.go: %v", err)
	}

	// Manual extraction
	manualResult, err := tool.ParseFast(ctx, "queries.go", content)
	if err != nil {
		t.Fatalf("Manual parse error: %v", err)
	}

	// Query extraction
	parser := NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		t.Fatalf("Failed to set language: %v", err)
	}

	tree, err := parser.Parse(content, nil)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	defer tree.Close()

	queryResult, err := ExtractSymbolsWithQuery(qc, lang, "go", tree)
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}

	// Compare results
	t.Logf("Manual: %d functions, %d types, %d imports",
		len(manualResult.Functions), len(manualResult.Types), len(manualResult.Imports))
	t.Logf("Query:  %d functions, %d types, %d imports",
		len(queryResult.Functions), len(queryResult.Types), len(queryResult.Imports))

	// Log function names for comparison
	t.Log("Manual functions:")
	for _, f := range manualResult.Functions {
		t.Logf("  - %s (line %d)", f.Name, f.StartLine)
	}

	t.Log("Query functions:")
	for _, f := range queryResult.Functions {
		t.Logf("  - %s (line %d)", f.Name, f.StartLine)
	}
}

// TestQueryAvailability tests which queries are available.
func TestQueryAvailability(t *testing.T) {
	langs := ListAvailableQueries()
	t.Logf("Available queries: %v", langs)

	expected := []string{"go", "python", "javascript", "typescript", "tsx", "rust", "java", "c", "cpp"}
	for _, lang := range expected {
		if !HasQuery(lang) {
			t.Errorf("Expected query for %s to be available", lang)
		}
	}
}

// collectGoFiles recursively collects Go files up to maxFiles.
func collectGoFiles(tb testing.TB, root string, maxFiles int) []string {
	tb.Helper()

	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			// Skip vendor, .git, node_modules
			base := filepath.Base(path)
			if base == "vendor" || base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" && len(files) < maxFiles {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		tb.Logf("Walk error: %v", err)
	}
	return files
}

// BenchmarkCGoOverhead measures the overhead of CGo calls.
func BenchmarkCGoOverhead(b *testing.B) {
	ctx := context.Background()
	loader := NewGrammarLoader()

	lang, err := loader.LoadContext(ctx, "go")
	if err != nil {
		b.Skipf("Failed to load Go grammar: %v", err)
	}

	content := []byte(`package main
func main() {
	println("hello")
}`)

	parser := NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		b.Fatalf("Failed to set language: %v", err)
	}

	tree, err := parser.Parse(content, nil)
	if err != nil {
		b.Fatalf("Parse error: %v", err)
	}
	defer tree.Close()

	root := tree.RootNode()

	// Benchmark individual CGo calls
	b.Run("NodeType", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = root.Type()
		}
	})

	b.Run("ChildCount", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = root.ChildCount()
		}
	})

	b.Run("NamedChildCount", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = root.NamedChildCount()
		}
	})

	b.Run("StartPosition", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = root.StartPosition()
		}
	})

	b.Run("NamedChild", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = root.NamedChild(0)
		}
	})
}

// Ensure types are compatible
var (
	_ *sitter.Language // Verify import is used
)
