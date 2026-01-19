package librarian

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockSearchSystem is a test double for SearchSystem.
type mockSearchSystem struct {
	result *CodeSearchResult
	err    error
	opts   SearchOptions
	query  string
}

func (m *mockSearchSystem) Search(_ context.Context, query string, opts SearchOptions) (*CodeSearchResult, error) {
	m.query = query
	m.opts = opts
	return m.result, m.err
}

func TestSearchHandler_Handle_HappyPath(t *testing.T) {
	docs := []ScoredDocument{
		{
			ID:      "doc1",
			Path:    "/path/to/main.go",
			Type:    "source_code",
			Content: "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n",
			Score:   0.95,
			Line:    3,
		},
		{
			ID:      "doc2",
			Path:    "/path/to/utils.py",
			Type:    "source_code",
			Content: "def helper():\n    return True\n",
			Score:   0.85,
			Line:    1,
		},
	}

	mock := &mockSearchSystem{
		result: &CodeSearchResult{
			Documents: docs,
			TotalHits: 2,
			Took:      10 * time.Millisecond,
		},
	}

	handler := NewSearchHandler(mock)

	req := &LibrarianRequest{
		ID:        "req-123",
		Intent:    IntentRecall,
		Domain:    DomainCode,
		Query:     "main function",
		Params:    map[string]any{"limit": 10},
		SessionID: "session-456",
		Timestamp: time.Now(),
	}

	resp, err := handler.Handle(context.Background(), req)

	if err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}
	if !resp.Success {
		t.Errorf("Success = false, want true")
	}
	if resp.RequestID != req.ID {
		t.Errorf("RequestID = %q, want %q", resp.RequestID, req.ID)
	}
	if resp.ID == "" {
		t.Error("Response ID should not be empty")
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("Data is not map[string]any")
	}

	results, ok := data["results"].([]EnrichedResult)
	if !ok {
		t.Fatalf("results is not []EnrichedResult")
	}
	if len(results) != 2 {
		t.Errorf("len(results) = %d, want 2", len(results))
	}

	if results[0].Language != "go" {
		t.Errorf("results[0].Language = %q, want %q", results[0].Language, "go")
	}
	if results[1].Language != "python" {
		t.Errorf("results[1].Language = %q, want %q", results[1].Language, "python")
	}
}

func TestSearchHandler_Handle_EmptyResults(t *testing.T) {
	mock := &mockSearchSystem{
		result: &CodeSearchResult{
			Documents: nil,
			TotalHits: 0,
			Took:      5 * time.Millisecond,
		},
	}

	handler := NewSearchHandler(mock)

	req := &LibrarianRequest{
		ID:     "req-empty",
		Domain: DomainCode,
		Query:  "nonexistent",
	}

	resp, err := handler.Handle(context.Background(), req)

	if err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}
	if !resp.Success {
		t.Errorf("Success = false, want true")
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("Data is not map[string]any")
	}

	results := data["results"]
	if results != nil {
		enriched, ok := results.([]EnrichedResult)
		if ok && len(enriched) != 0 {
			t.Errorf("results should be nil or empty, got %d items", len(enriched))
		}
	}

	totalHits, ok := data["total_hits"].(int)
	if !ok || totalHits != 0 {
		t.Errorf("total_hits = %v, want 0", data["total_hits"])
	}
}

func TestSearchHandler_Handle_InvalidDomain(t *testing.T) {
	mock := &mockSearchSystem{}
	handler := NewSearchHandler(mock)

	testCases := []struct {
		name   string
		domain LibrarianDomain
	}{
		{"files domain", DomainFiles},
		{"patterns domain", DomainPatterns},
		{"tooling domain", DomainTooling},
		{"remote domain", DomainRemote},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &LibrarianRequest{
				ID:     "req-invalid",
				Domain: tc.domain,
				Query:  "test",
			}

			resp, err := handler.Handle(context.Background(), req)

			if !errors.Is(err, ErrInvalidDomain) {
				t.Errorf("expected ErrInvalidDomain, got %v", err)
			}
			if resp.Success {
				t.Error("Success = true, want false")
			}
			if resp.Error == "" {
				t.Error("Error message should not be empty")
			}
		})
	}
}

func TestSearchHandler_Handle_NilRequest(t *testing.T) {
	mock := &mockSearchSystem{}
	handler := NewSearchHandler(mock)

	resp, err := handler.Handle(context.Background(), nil)

	if !errors.Is(err, ErrNilRequest) {
		t.Errorf("expected ErrNilRequest, got %v", err)
	}
	if resp.Success {
		t.Error("Success = true, want false")
	}
}

func TestSearchHandler_Handle_SearchError(t *testing.T) {
	searchErr := errors.New("connection timeout")
	mock := &mockSearchSystem{
		err: searchErr,
	}

	handler := NewSearchHandler(mock)

	req := &LibrarianRequest{
		ID:     "req-error",
		Domain: DomainCode,
		Query:  "test",
	}

	resp, err := handler.Handle(context.Background(), req)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !resp.Success {
		// This is expected behavior
	} else {
		t.Error("Success = true, want false")
	}
	if resp.Error == "" {
		t.Error("Error message should not be empty")
	}
}

func TestSearchHandler_extractOptions_Defaults(t *testing.T) {
	handler := NewSearchHandler(nil)

	req := &LibrarianRequest{
		Query:  "test",
		Params: nil,
	}

	opts := handler.extractOptions(req)

	if opts.Limit != DefaultSearchLimit {
		t.Errorf("Limit = %d, want %d", opts.Limit, DefaultSearchLimit)
	}
	if opts.Fuzzy {
		t.Error("Fuzzy = true, want false")
	}
	if opts.PathPrefix != "" {
		t.Errorf("PathPrefix = %q, want empty", opts.PathPrefix)
	}
}

func TestSearchHandler_extractOptions_WithParams(t *testing.T) {
	handler := NewSearchHandler(nil)

	req := &LibrarianRequest{
		Query: "test",
		Params: map[string]any{
			"limit":       50,
			"path_prefix": "/src/",
			"fuzzy":       true,
			"types":       []string{"source_code", "config"},
		},
	}

	opts := handler.extractOptions(req)

	if opts.Limit != 50 {
		t.Errorf("Limit = %d, want 50", opts.Limit)
	}
	if opts.PathPrefix != "/src/" {
		t.Errorf("PathPrefix = %q, want %q", opts.PathPrefix, "/src/")
	}
	if !opts.Fuzzy {
		t.Error("Fuzzy = false, want true")
	}
	if len(opts.Types) != 2 {
		t.Errorf("len(Types) = %d, want 2", len(opts.Types))
	}
}

func TestSearchHandler_extractOptions_LimitClamping(t *testing.T) {
	handler := NewSearchHandler(nil)

	req := &LibrarianRequest{
		Query: "test",
		Params: map[string]any{
			"limit": 500, // exceeds MaxSearchLimit
		},
	}

	opts := handler.extractOptions(req)

	if opts.Limit != MaxSearchLimit {
		t.Errorf("Limit = %d, want %d (clamped)", opts.Limit, MaxSearchLimit)
	}
}

func TestSearchHandler_extractOptions_FloatLimit(t *testing.T) {
	handler := NewSearchHandler(nil)

	req := &LibrarianRequest{
		Query: "test",
		Params: map[string]any{
			"limit": float64(25), // JSON numbers often come as float64
		},
	}

	opts := handler.extractOptions(req)

	if opts.Limit != 25 {
		t.Errorf("Limit = %d, want 25", opts.Limit)
	}
}

func TestSearchHandler_extractOptions_TypesAnySlice(t *testing.T) {
	handler := NewSearchHandler(nil)

	req := &LibrarianRequest{
		Query: "test",
		Params: map[string]any{
			"types": []any{"source_code", "markdown", 123}, // mixed types
		},
	}

	opts := handler.extractOptions(req)

	if len(opts.Types) != 2 {
		t.Errorf("len(Types) = %d, want 2 (non-strings filtered)", len(opts.Types))
	}
}

func TestSearchHandler_EnrichResults_Empty(t *testing.T) {
	handler := NewSearchHandler(nil)

	result := handler.EnrichResults(nil)

	if result != nil {
		t.Errorf("EnrichResults(nil) = %v, want nil", result)
	}

	result = handler.EnrichResults([]ScoredDocument{})

	if result != nil {
		t.Errorf("EnrichResults([]) = %v, want nil", result)
	}
}

func TestSearchHandler_EnrichResults_WithContent(t *testing.T) {
	handler := NewSearchHandler(nil)

	docs := []ScoredDocument{
		{
			ID:      "doc1",
			Path:    "/test/main.go",
			Content: "line1\nline2\nline3\nline4\nline5\nline6\nline7\n",
			Line:    4,
			Score:   0.9,
		},
	}

	results := handler.EnrichResults(docs)

	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}

	r := results[0]
	if r.Language != "go" {
		t.Errorf("Language = %q, want %q", r.Language, "go")
	}
	if r.Context == "" {
		t.Error("Context should not be empty")
	}
	if len(r.LineContext) == 0 {
		t.Error("LineContext should not be empty")
	}
}

func TestDetectLanguageFromPath(t *testing.T) {
	testCases := []struct {
		path     string
		expected string
	}{
		{"/src/main.go", "go"},
		{"/src/script.py", "python"},
		{"/src/app.js", "javascript"},
		{"/src/app.ts", "typescript"},
		{"/src/lib.rs", "rust"},
		{"/src/Main.java", "java"},
		{"/src/code.c", "c"},
		{"/src/code.cpp", "cpp"},
		{"/docs/README.md", "markdown"},
		{"/config/settings.yaml", ""},
		{"", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			result := detectLanguageFromPath(tc.path)
			if result != tc.expected {
				t.Errorf("detectLanguageFromPath(%q) = %q, want %q", tc.path, result, tc.expected)
			}
		})
	}
}

func TestExtractLineContext(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10"

	testCases := []struct {
		name         string
		content      string
		matchLine    int
		contextLines int
		wantLen      int
	}{
		{"middle of file", content, 5, 2, 5},
		{"beginning of file", content, 1, 2, 3},
		{"end of file", content, 10, 2, 3},
		{"empty content", "", 5, 2, 0},
		{"invalid line number", content, 0, 2, 0},
		{"negative line number", content, -1, 2, 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractLineContext(tc.content, tc.matchLine, tc.contextLines)
			if len(result) != tc.wantLen {
				t.Errorf("len(extractLineContext) = %d, want %d", len(result), tc.wantLen)
			}
		})
	}
}

func TestSplitLines(t *testing.T) {
	testCases := []struct {
		name    string
		content string
		wantLen int
	}{
		{"simple", "a\nb\nc", 3},
		{"trailing newline", "a\nb\nc\n", 3},
		{"empty", "", 0},
		{"single line", "hello", 1},
		{"only newlines", "\n\n\n", 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := splitLines(tc.content)
			if len(result) != tc.wantLen {
				t.Errorf("len(splitLines(%q)) = %d, want %d", tc.content, len(result), tc.wantLen)
			}
		})
	}
}

func TestBuildContextString(t *testing.T) {
	testCases := []struct {
		name     string
		lines    []string
		expected string
	}{
		{"multiple lines", []string{"a", "b", "c"}, "a\nb\nc"},
		{"single line", []string{"hello"}, "hello"},
		{"empty", nil, ""},
		{"empty slice", []string{}, ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := buildContextString(tc.lines)
			if result != tc.expected {
				t.Errorf("buildContextString(%v) = %q, want %q", tc.lines, result, tc.expected)
			}
		})
	}
}

func TestConvertToStringSlice(t *testing.T) {
	testCases := []struct {
		name     string
		input    []any
		expected int
	}{
		{"all strings", []any{"a", "b", "c"}, 3},
		{"mixed types", []any{"a", 1, "b", true}, 2},
		{"no strings", []any{1, 2, 3}, 0},
		{"empty", []any{}, 0},
		{"nil values", []any{nil, "a", nil}, 1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := convertToStringSlice(tc.input)
			if len(result) != tc.expected {
				t.Errorf("len(convertToStringSlice) = %d, want %d", len(result), tc.expected)
			}
		})
	}
}

func TestSearchHandler_TokenBudget(t *testing.T) {
	handler := NewSearchHandler(nil)

	// Create many documents with substantial content
	docs := make([]ScoredDocument, 100)
	longContent := ""
	for i := 0; i < 500; i++ {
		longContent += "This is a very long line of content for testing token budget limits.\n"
	}

	for i := range docs {
		docs[i] = ScoredDocument{
			ID:      "doc",
			Path:    "/test/file.go",
			Content: longContent,
			Line:    250,
			Score:   0.5,
		}
	}

	results := handler.EnrichResults(docs)

	// Should stop before processing all 100 documents due to token budget
	if len(results) >= 100 {
		t.Error("Token budget should limit the number of enriched results")
	}
	if len(results) == 0 {
		t.Error("Should have at least one enriched result")
	}
}

func TestNewSearchHandler(t *testing.T) {
	mock := &mockSearchSystem{}
	handler := NewSearchHandler(mock)

	if handler == nil {
		t.Fatal("NewSearchHandler returned nil")
	}
	if handler.search != mock {
		t.Error("search system not properly assigned")
	}
}

func TestSearchHandler_ResponseTimestamps(t *testing.T) {
	mock := &mockSearchSystem{
		result: &CodeSearchResult{
			Documents: []ScoredDocument{},
			TotalHits: 0,
		},
	}

	handler := NewSearchHandler(mock)
	req := &LibrarianRequest{
		ID:     "req-time",
		Domain: DomainCode,
		Query:  "test",
	}

	before := time.Now()
	resp, _ := handler.Handle(context.Background(), req)
	after := time.Now()

	if resp.Timestamp.Before(before) || resp.Timestamp.After(after) {
		t.Error("Response timestamp should be between before and after Handle call")
	}
	if resp.Took <= 0 {
		t.Error("Took duration should be positive")
	}
}
