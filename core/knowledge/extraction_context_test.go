package knowledge

import (
	"testing"
)

// =============================================================================
// NewExtractionContext Tests
// =============================================================================

func TestNewExtractionContext(t *testing.T) {
	filePath := "/path/to/file.go"
	language := "go"

	ctx := NewExtractionContext(filePath, language)

	if ctx.FilePath != filePath {
		t.Errorf("FilePath = %v, want %v", ctx.FilePath, filePath)
	}
	if ctx.Language != language {
		t.Errorf("Language = %v, want %v", ctx.Language, language)
	}
	if ctx.SymbolTable == nil {
		t.Error("SymbolTable should not be nil")
	}
	if len(ctx.SymbolTable) != 0 {
		t.Errorf("SymbolTable length = %v, want 0", len(ctx.SymbolTable))
	}
	if ctx.ParentScope != "" {
		t.Errorf("ParentScope = %v, want empty string", ctx.ParentScope)
	}
	if ctx.ImportMap == nil {
		t.Error("ImportMap should not be nil")
	}
	if len(ctx.ImportMap) != 0 {
		t.Errorf("ImportMap length = %v, want 0", len(ctx.ImportMap))
	}
}

// =============================================================================
// WithScope Tests
// =============================================================================

func TestExtractionContext_WithScope(t *testing.T) {
	original := NewExtractionContext("/path/to/file.go", "go")
	original.SymbolTable["test"] = &ExtractedEntity{Name: "test"}
	original.ImportMap["fmt"] = "fmt"

	scopeName := "MyFunction"
	scoped := original.WithScope(scopeName)

	if scoped.ParentScope != scopeName {
		t.Errorf("ParentScope = %v, want %v", scoped.ParentScope, scopeName)
	}
	if scoped.FilePath != original.FilePath {
		t.Errorf("FilePath = %v, want %v", scoped.FilePath, original.FilePath)
	}
	if scoped.Language != original.Language {
		t.Errorf("Language = %v, want %v", scoped.Language, original.Language)
	}
	// Verify SymbolTable is shared by checking mutations are visible
	if _, exists := scoped.SymbolTable["test"]; !exists {
		t.Error("SymbolTable should be shared reference")
	}
	// Verify ImportMap is shared by checking mutations are visible
	if _, exists := scoped.ImportMap["fmt"]; !exists {
		t.Error("ImportMap should be shared reference")
	}
}

func TestExtractionContext_WithScope_Chainable(t *testing.T) {
	ctx := NewExtractionContext("/path/to/file.go", "go")

	level1 := ctx.WithScope("Level1")
	level2 := level1.WithScope("Level2")
	level3 := level2.WithScope("Level3")

	if level1.ParentScope != "Level1" {
		t.Errorf("level1.ParentScope = %v, want Level1", level1.ParentScope)
	}
	if level2.ParentScope != "Level2" {
		t.Errorf("level2.ParentScope = %v, want Level2", level2.ParentScope)
	}
	if level3.ParentScope != "Level3" {
		t.Errorf("level3.ParentScope = %v, want Level3", level3.ParentScope)
	}

	// Verify SymbolTable is shared by adding to one and checking in another
	level1.SymbolTable["test"] = &ExtractedEntity{Name: "test"}
	if _, exists := level3.SymbolTable["test"]; !exists {
		t.Error("SymbolTable should be shared across scopes")
	}
}

// =============================================================================
// SymbolTable Tests
// =============================================================================

func TestExtractionContext_SymbolTable_SharedAcrossScopes(t *testing.T) {
	ctx := NewExtractionContext("/path/to/file.go", "go")

	entity1 := &ExtractedEntity{Name: "Entity1"}
	ctx.SymbolTable["Entity1"] = entity1

	scoped := ctx.WithScope("SomeScope")
	entity2 := &ExtractedEntity{Name: "Entity2"}
	scoped.SymbolTable["Entity2"] = entity2

	if len(ctx.SymbolTable) != 2 {
		t.Errorf("ctx.SymbolTable length = %v, want 2", len(ctx.SymbolTable))
	}
	if len(scoped.SymbolTable) != 2 {
		t.Errorf("scoped.SymbolTable length = %v, want 2", len(scoped.SymbolTable))
	}

	if ctx.SymbolTable["Entity2"] != entity2 {
		t.Error("Entity2 should be visible in original context")
	}
	if scoped.SymbolTable["Entity1"] != entity1 {
		t.Error("Entity1 should be visible in scoped context")
	}
}

// =============================================================================
// ImportMap Tests
// =============================================================================

func TestExtractionContext_ImportMap_SharedAcrossScopes(t *testing.T) {
	ctx := NewExtractionContext("/path/to/file.go", "go")

	ctx.ImportMap["fmt"] = "fmt"

	scoped := ctx.WithScope("SomeScope")
	scoped.ImportMap["os"] = "os"

	if len(ctx.ImportMap) != 2 {
		t.Errorf("ctx.ImportMap length = %v, want 2", len(ctx.ImportMap))
	}
	if len(scoped.ImportMap) != 2 {
		t.Errorf("scoped.ImportMap length = %v, want 2", len(scoped.ImportMap))
	}

	if ctx.ImportMap["os"] != "os" {
		t.Error("os import should be visible in original context")
	}
	if scoped.ImportMap["fmt"] != "fmt" {
		t.Error("fmt import should be visible in scoped context")
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestExtractionContext_CompleteWorkflow(t *testing.T) {
	ctx := NewExtractionContext("/src/main.go", "go")

	ctx.ImportMap["fmt"] = "fmt"
	ctx.ImportMap["strings"] = "strings"

	globalEntity := &ExtractedEntity{
		Name: "GlobalVar",
		Kind: EntityKindVariable,
	}
	ctx.SymbolTable["GlobalVar"] = globalEntity

	funcScope := ctx.WithScope("MyFunction")
	localEntity := &ExtractedEntity{
		Name: "localVar",
		Kind: EntityKindVariable,
	}
	funcScope.SymbolTable["localVar"] = localEntity

	blockScope := funcScope.WithScope("IfBlock")

	if blockScope.ParentScope != "IfBlock" {
		t.Errorf("blockScope.ParentScope = %v, want IfBlock", blockScope.ParentScope)
	}
	if len(blockScope.SymbolTable) != 2 {
		t.Errorf("blockScope.SymbolTable length = %v, want 2", len(blockScope.SymbolTable))
	}
	if blockScope.SymbolTable["GlobalVar"] != globalEntity {
		t.Error("GlobalVar should be accessible in nested scope")
	}
	if blockScope.SymbolTable["localVar"] != localEntity {
		t.Error("localVar should be accessible in nested scope")
	}
	if len(blockScope.ImportMap) != 2 {
		t.Errorf("blockScope.ImportMap length = %v, want 2", len(blockScope.ImportMap))
	}
}
