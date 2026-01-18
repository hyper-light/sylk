package parser

import (
	"sync"
	"testing"
)

// =============================================================================
// ParseResult Tests
// =============================================================================

func TestNewParseResult(t *testing.T) {
	t.Parallel()

	result := NewParseResult()

	if result == nil {
		t.Fatal("NewParseResult returned nil")
	}
	if result.Symbols == nil {
		t.Error("Symbols slice should be initialized")
	}
	if result.Imports == nil {
		t.Error("Imports slice should be initialized")
	}
	if result.Metadata == nil {
		t.Error("Metadata map should be initialized")
	}
	if len(result.Symbols) != 0 {
		t.Error("Symbols should be empty initially")
	}
}

// =============================================================================
// ParserRegistry Tests
// =============================================================================

func TestNewParserRegistry(t *testing.T) {
	t.Parallel()

	registry := NewParserRegistry()

	if registry == nil {
		t.Fatal("NewParserRegistry returned nil")
	}
	if registry.parsers == nil {
		t.Error("parsers map should be initialized")
	}
}

func TestParserRegistry_RegisterParser(t *testing.T) {
	t.Parallel()

	registry := NewParserRegistry()
	parser := NewGoParser()

	registry.RegisterParser(parser)

	got := registry.GetParser(".go")
	if got == nil {
		t.Error("GetParser should return registered parser")
	}
}

func TestParserRegistry_GetParser_NotFound(t *testing.T) {
	t.Parallel()

	registry := NewParserRegistry()

	got := registry.GetParser(".unknown")
	if got != nil {
		t.Error("GetParser should return nil for unknown extension")
	}
}

func TestParserRegistry_GetParserForFile(t *testing.T) {
	t.Parallel()

	registry := NewParserRegistry()
	registry.RegisterParser(NewGoParser())

	tests := []struct {
		name     string
		path     string
		wantNil  bool
	}{
		{"go file", "main.go", false},
		{"nested go file", "/path/to/file.go", false},
		{"unknown file", "file.unknown", true},
		{"no extension", "README", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := registry.GetParserForFile(tt.path)
			if (got == nil) != tt.wantNil {
				t.Errorf("GetParserForFile(%q) nil=%v, want nil=%v", tt.path, got == nil, tt.wantNil)
			}
		})
	}
}

func TestParserRegistry_HasParser(t *testing.T) {
	t.Parallel()

	registry := NewParserRegistry()
	registry.RegisterParser(NewGoParser())

	if !registry.HasParser(".go") {
		t.Error("HasParser should return true for registered extension")
	}
	if registry.HasParser(".unknown") {
		t.Error("HasParser should return false for unknown extension")
	}
}

func TestParserRegistry_Extensions(t *testing.T) {
	t.Parallel()

	registry := NewParserRegistry()
	registry.RegisterParser(NewGoParser())
	registry.RegisterParser(NewPythonParser())

	extensions := registry.Extensions()
	if len(extensions) == 0 {
		t.Error("Extensions should return registered extensions")
	}

	// Check that .go and .py are present
	found := make(map[string]bool)
	for _, ext := range extensions {
		found[ext] = true
	}

	if !found[".go"] {
		t.Error("Extensions should include .go")
	}
	if !found[".py"] {
		t.Error("Extensions should include .py")
	}
}

func TestParserRegistry_ThreadSafety(t *testing.T) {
	t.Parallel()

	registry := NewParserRegistry()
	var wg sync.WaitGroup

	// Concurrent registrations and lookups
	for i := 0; i < 100; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			registry.RegisterParser(NewGoParser())
		}()

		go func() {
			defer wg.Done()
			_ = registry.GetParser(".go")
		}()
	}

	wg.Wait()
}

// =============================================================================
// Default Registry Tests
// =============================================================================

func TestDefaultRegistry(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	if registry == nil {
		t.Fatal("DefaultRegistry should not be nil")
	}

	// Default registry should have parsers registered via init()
	goParser := registry.GetParser(".go")
	if goParser == nil {
		t.Error("Default registry should have Go parser")
	}

	pyParser := registry.GetParser(".py")
	if pyParser == nil {
		t.Error("Default registry should have Python parser")
	}

	tsParser := registry.GetParser(".ts")
	if tsParser == nil {
		t.Error("Default registry should have TypeScript parser")
	}

	mdParser := registry.GetParser(".md")
	if mdParser == nil {
		t.Error("Default registry should have Markdown parser")
	}

	jsonParser := registry.GetParser(".json")
	if jsonParser == nil {
		t.Error("Default registry should have JSON parser")
	}
}

func TestGetParserForFile_Global(t *testing.T) {
	t.Parallel()

	parser := GetParserForFile("test.go")
	if parser == nil {
		t.Error("GetParserForFile should return Go parser for .go file")
	}
}

// =============================================================================
// Symbol Kind Constants Tests
// =============================================================================

func TestSymbolKindConstants(t *testing.T) {
	t.Parallel()

	constants := []string{
		KindFunction,
		KindType,
		KindInterface,
		KindClass,
		KindVariable,
		KindConst,
		KindHeading,
		KindCodeBlock,
		KindLink,
		KindKey,
	}

	seen := make(map[string]bool)
	for _, c := range constants {
		if c == "" {
			t.Error("Symbol kind constant should not be empty")
		}
		if seen[c] {
			t.Errorf("Duplicate symbol kind: %s", c)
		}
		seen[c] = true
	}
}
