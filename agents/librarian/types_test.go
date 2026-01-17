package librarian

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSymbolKindConstants(t *testing.T) {
	kinds := []SymbolKind{
		SymbolKindFunction,
		SymbolKindType,
		SymbolKindConst,
		SymbolKindVar,
		SymbolKindMethod,
		SymbolKindField,
		SymbolKindPackage,
		SymbolKindAny,
	}

	expected := []string{
		"function", "type", "const", "var",
		"method", "field", "package", "any",
	}

	for i, kind := range kinds {
		if string(kind) != expected[i] {
			t.Errorf("SymbolKind %d = %q, want %q", i, kind, expected[i])
		}
	}
}

func TestSymbolJSON(t *testing.T) {
	symbol := Symbol{
		Name:      "TestFunc",
		Kind:      SymbolKindFunction,
		Package:   "main",
		FilePath:  "main.go",
		Line:      10,
		Column:    1,
		EndLine:   20,
		EndColumn: 1,
		Signature: "func TestFunc()",
		Doc:       "TestFunc does testing",
		Exported:  true,
		Metadata:  map[string]string{"key": "value"},
	}

	data, err := json.Marshal(symbol)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Symbol
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Name != symbol.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, symbol.Name)
	}
	if decoded.Kind != symbol.Kind {
		t.Errorf("Kind = %q, want %q", decoded.Kind, symbol.Kind)
	}
	if decoded.Exported != symbol.Exported {
		t.Errorf("Exported = %v, want %v", decoded.Exported, symbol.Exported)
	}
}

func TestFileInfoJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	info := FileInfo{
		Path:        "/path/to/file.go",
		Package:     "main",
		Imports:     []string{"fmt", "os"},
		SymbolCount: 10,
		LineCount:   100,
		ModifiedAt:  now,
		IndexedAt:   now,
		ContentHash: "abc123",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded FileInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Path != info.Path {
		t.Errorf("Path = %q, want %q", decoded.Path, info.Path)
	}
	if len(decoded.Imports) != len(info.Imports) {
		t.Errorf("Imports length = %d, want %d", len(decoded.Imports), len(info.Imports))
	}
}

func TestNewToolRegistry(t *testing.T) {
	registry := NewToolRegistry()

	if registry == nil {
		t.Fatal("NewToolRegistry returned nil")
	}
	if registry.Linters == nil {
		t.Error("Linters map is nil")
	}
	if registry.Formatters == nil {
		t.Error("Formatters map is nil")
	}
	if registry.LSPs == nil {
		t.Error("LSPs map is nil")
	}
	if registry.TypeCheckers == nil {
		t.Error("TypeCheckers map is nil")
	}
}

func TestToolConfigJSON(t *testing.T) {
	config := ToolConfig{
		Name:       "golangci-lint",
		Command:    "golangci-lint run",
		ConfigFile: ".golangci.yml",
		Available:  true,
		Version:    "1.50.0",
		Metadata:   map[string]string{"type": "linter"},
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded ToolConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Name != config.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, config.Name)
	}
	if decoded.Available != config.Available {
		t.Errorf("Available = %v, want %v", decoded.Available, config.Available)
	}
}

func TestQueryRequestJSON(t *testing.T) {
	req := QueryRequest{
		Query:       "findSymbol",
		Kind:        SymbolKindFunction,
		FilePattern: "*.go",
		Package:     "main",
		Limit:       10,
		Offset:      0,
		Filters:     map[string]string{"exported": "true"},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded QueryRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Query != req.Query {
		t.Errorf("Query = %q, want %q", decoded.Query, req.Query)
	}
	if decoded.Kind != req.Kind {
		t.Errorf("Kind = %q, want %q", decoded.Kind, req.Kind)
	}
}

func TestLibrarianIntentConstants(t *testing.T) {
	if string(IntentRecall) != "recall" {
		t.Errorf("IntentRecall = %q, want %q", IntentRecall, "recall")
	}
	if string(IntentCheck) != "check" {
		t.Errorf("IntentCheck = %q, want %q", IntentCheck, "check")
	}
}

func TestLibrarianDomainConstants(t *testing.T) {
	domains := []LibrarianDomain{
		DomainFiles,
		DomainPatterns,
		DomainCode,
		DomainTooling,
		DomainRemote,
	}

	expected := []string{"files", "patterns", "code", "tooling", "remote"}

	for i, domain := range domains {
		if string(domain) != expected[i] {
			t.Errorf("Domain %d = %q, want %q", i, domain, expected[i])
		}
	}
}

func TestLibrarianRequestJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	req := LibrarianRequest{
		ID:        "req-123",
		Intent:    IntentRecall,
		Domain:    DomainCode,
		Query:     "find function main",
		Params:    map[string]any{"limit": 10},
		SessionID: "session-456",
		Timestamp: now,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded LibrarianRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.ID != req.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, req.ID)
	}
	if decoded.Intent != req.Intent {
		t.Errorf("Intent = %q, want %q", decoded.Intent, req.Intent)
	}
	if decoded.Domain != req.Domain {
		t.Errorf("Domain = %q, want %q", decoded.Domain, req.Domain)
	}
}

func TestLibrarianResponseJSON(t *testing.T) {
	resp := LibrarianResponse{
		ID:        "resp-123",
		RequestID: "req-123",
		Success:   true,
		Data:      map[string]string{"result": "found"},
		Took:      100 * time.Millisecond,
		Timestamp: time.Now().Truncate(time.Second),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded LibrarianResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Success != resp.Success {
		t.Errorf("Success = %v, want %v", decoded.Success, resp.Success)
	}
	if decoded.RequestID != resp.RequestID {
		t.Errorf("RequestID = %q, want %q", decoded.RequestID, resp.RequestID)
	}
}

func TestCodebaseSummaryJSON(t *testing.T) {
	summary := CodebaseSummary{
		DirectoryStructure: "main/\n  pkg/\n  cmd/",
		KeyPaths:           []string{"main.go", "pkg/core"},
		CodeStyle: CodeStyleInfo{
			IndentStyle: "tabs",
			IndentSize:  4,
			LineLimit:   120,
			NamingConv:  "camelCase",
			TestPattern: "_test.go",
			DocStyle:    "godoc",
		},
		Languages:    []LanguageInfo{{Name: "Go", Extensions: []string{".go"}, Percentage: 100}},
		Dependencies: []string{"fmt", "os"},
		EntryPoints:  []string{"main.go"},
	}

	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded CodebaseSummary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.CodeStyle.IndentStyle != summary.CodeStyle.IndentStyle {
		t.Errorf("IndentStyle = %q, want %q", decoded.CodeStyle.IndentStyle, summary.CodeStyle.IndentStyle)
	}
	if len(decoded.KeyPaths) != len(summary.KeyPaths) {
		t.Errorf("KeyPaths length = %d, want %d", len(decoded.KeyPaths), len(summary.KeyPaths))
	}
}

func TestIndexProgressJSON(t *testing.T) {
	progress := IndexProgress{
		Phase:        "scanning",
		Current:      50,
		Total:        100,
		CurrentFile:  "main.go",
		StartedAt:    time.Now().Truncate(time.Second),
		EstimatedETA: 10 * time.Second,
	}

	data, err := json.Marshal(progress)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded IndexProgress
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Phase != progress.Phase {
		t.Errorf("Phase = %q, want %q", decoded.Phase, progress.Phase)
	}
	if decoded.Current != progress.Current {
		t.Errorf("Current = %d, want %d", decoded.Current, progress.Current)
	}
}

func TestAgentRoutingInfoJSON(t *testing.T) {
	info := AgentRoutingInfo{
		AgentID:   "librarian-1",
		AgentType: "librarian",
		Intents:   []LibrarianIntent{IntentRecall, IntentCheck},
		Domains:   []LibrarianDomain{DomainCode, DomainFiles},
		Keywords:  []string{"find", "search", "code"},
		Priority:  10,
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded AgentRoutingInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.AgentID != info.AgentID {
		t.Errorf("AgentID = %q, want %q", decoded.AgentID, info.AgentID)
	}
	if len(decoded.Intents) != len(info.Intents) {
		t.Errorf("Intents length = %d, want %d", len(decoded.Intents), len(info.Intents))
	}
}

func TestEmptyStructs(t *testing.T) {
	t.Run("empty Symbol", func(t *testing.T) {
		var s Symbol
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("Marshal empty Symbol failed: %v", err)
		}
		var decoded Symbol
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal empty Symbol failed: %v", err)
		}
	})

	t.Run("empty QueryRequest", func(t *testing.T) {
		var q QueryRequest
		data, err := json.Marshal(q)
		if err != nil {
			t.Fatalf("Marshal empty QueryRequest failed: %v", err)
		}
		var decoded QueryRequest
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal empty QueryRequest failed: %v", err)
		}
	})

	t.Run("empty LibrarianRequest", func(t *testing.T) {
		var r LibrarianRequest
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("Marshal empty LibrarianRequest failed: %v", err)
		}
		var decoded LibrarianRequest
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal empty LibrarianRequest failed: %v", err)
		}
	})
}

func TestNilMaps(t *testing.T) {
	symbol := Symbol{Name: "test"}
	if symbol.Metadata != nil {
		t.Error("uninitialized Metadata should be nil")
	}

	data, err := json.Marshal(symbol)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Symbol
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
}

func TestToolRegistryAddTools(t *testing.T) {
	registry := NewToolRegistry()

	registry.Linters["go"] = []ToolConfig{
		{Name: "golangci-lint", Command: "golangci-lint run"},
	}

	if len(registry.Linters["go"]) != 1 {
		t.Errorf("Linters[go] length = %d, want 1", len(registry.Linters["go"]))
	}

	registry.Formatters["go"] = []ToolConfig{
		{Name: "gofmt", Command: "gofmt -w"},
		{Name: "goimports", Command: "goimports -w"},
	}

	if len(registry.Formatters["go"]) != 2 {
		t.Errorf("Formatters[go] length = %d, want 2", len(registry.Formatters["go"]))
	}
}

func TestSearchMatchJSON(t *testing.T) {
	match := SearchMatch{
		FilePath:   "main.go",
		Line:       10,
		Column:     5,
		Content:    "func main() {",
		MatchStart: 0,
		MatchEnd:   4,
	}

	data, err := json.Marshal(match)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded SearchMatch
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.FilePath != match.FilePath {
		t.Errorf("FilePath = %q, want %q", decoded.FilePath, match.FilePath)
	}
}

func TestUsageResultJSON(t *testing.T) {
	result := UsageResult{
		Symbol: "main",
		Definition: &Symbol{
			Name: "main",
			Kind: SymbolKindFunction,
		},
		Usages: []UsageInfo{
			{Symbol: "main", FilePath: "other.go", Line: 5},
		},
		TotalCount: 1,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded UsageResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Symbol != result.Symbol {
		t.Errorf("Symbol = %q, want %q", decoded.Symbol, result.Symbol)
	}
	if decoded.Definition == nil {
		t.Error("Definition should not be nil")
	}
}

func TestContextCheckpointJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	checkpoint := ContextCheckpoint{
		ID:        "ckpt-123",
		SessionID: "session-456",
		Timestamp: now,
		Summary: &CodebaseSummary{
			DirectoryStructure: "root/",
		},
		TokensUsed:     1000,
		ThresholdLevel: 1,
	}

	data, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded ContextCheckpoint
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.ID != checkpoint.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, checkpoint.ID)
	}
	if decoded.TokensUsed != checkpoint.TokensUsed {
		t.Errorf("TokensUsed = %d, want %d", decoded.TokensUsed, checkpoint.TokensUsed)
	}
}
