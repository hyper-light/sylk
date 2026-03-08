package librarian

import (
	"strings"
	"testing"
)

func TestSearchLedger_RecordAndCount(t *testing.T) {
	sl := NewSearchLedger()

	sl.Record(0, "grep", `{"pattern":"foo"}`, `{"count":3,"matches":[{"file":"a.go","line":1,"content":"foo"},{"file":"b.go","line":2,"content":"foo"},{"file":"c.go","line":3,"content":"foo"}]}`)

	if sl.SearchCount() != 1 {
		t.Fatalf("expected 1 search, got %d", sl.SearchCount())
	}
	if sl.UniqueFileCount() != 3 {
		t.Fatalf("expected 3 unique files, got %d", sl.UniqueFileCount())
	}
	if sl.TotalMatches() != 3 {
		t.Fatalf("expected 3 total matches, got %d", sl.TotalMatches())
	}
}

func TestSearchLedger_IgnoresNonSearchTools(t *testing.T) {
	sl := NewSearchLedger()

	sl.Record(0, "read_file", `{"path":"a.go"}`, `{"content":"hello"}`)
	sl.Record(0, "git", `{"command":"status"}`, `{"output":"clean"}`)

	if sl.SearchCount() != 0 {
		t.Fatalf("expected 0 searches for non-search tools, got %d", sl.SearchCount())
	}
}

func TestSearchLedger_DeduplicatesExactSameCall(t *testing.T) {
	sl := NewSearchLedger()

	result := `{"count":2,"matches":[{"file":"a.go","line":1,"content":"x"}]}`
	sl.Record(0, "grep", `{"pattern":"foo"}`, result)
	sl.Record(1, "grep", `{"pattern":"foo"}`, result)

	if sl.SearchCount() != 1 {
		t.Fatalf("expected 1 search (deduped), got %d", sl.SearchCount())
	}
}

func TestSearchLedger_SaturationDetection(t *testing.T) {
	sl := NewSearchLedger()

	// First search finds files a.go, b.go.
	sl.Record(0, "grep", `{"pattern":"foo"}`,
		`{"count":2,"matches":[{"file":"a.go","line":1,"content":"x"},{"file":"b.go","line":2,"content":"y"}]}`)

	if sl.IsSaturated() {
		t.Fatal("should not be saturated after one search")
	}

	// Second search finds only a.go (subset of already-seen files).
	sl.Record(1, "grep", `{"pattern":"bar"}`,
		`{"count":1,"matches":[{"file":"a.go","line":5,"content":"bar"}]}`)

	if sl.IsSaturated() {
		t.Fatal("should not be saturated after one saturated + one non-saturated")
	}

	// Third search also finds only b.go (already seen).
	sl.Record(2, "find_symbol", `{"name":"Baz"}`,
		`{"count":1,"symbols":[{"file":"b.go","name":"Baz","kind":"function","start_line":10,"end_line":20}]}`)

	if !sl.IsSaturated() {
		t.Fatal("should be saturated: last 2 searches returned only already-seen files")
	}
}

func TestSearchLedger_SaturationResetsOnNewFile(t *testing.T) {
	sl := NewSearchLedger()

	sl.Record(0, "grep", `{"pattern":"foo"}`,
		`{"count":1,"matches":[{"file":"a.go","line":1,"content":"x"}]}`)
	sl.Record(1, "grep", `{"pattern":"bar"}`,
		`{"count":1,"matches":[{"file":"a.go","line":5,"content":"bar"}]}`)
	sl.Record(2, "find_symbol", `{"name":"Foo"}`,
		`{"count":1,"symbols":[{"file":"a.go","name":"Foo","kind":"function","start_line":10,"end_line":20}]}`)

	if !sl.IsSaturated() {
		t.Fatal("should be saturated")
	}

	// New search finds a new file — saturation breaks.
	sl.Record(3, "grep", `{"pattern":"baz"}`,
		`{"count":1,"matches":[{"file":"new.go","line":1,"content":"baz"}]}`)

	if sl.IsSaturated() {
		t.Fatal("should not be saturated after finding a new file")
	}
}

func TestSearchLedger_EvidenceSummary(t *testing.T) {
	sl := NewSearchLedger()

	sl.Record(0, "grep", `{"pattern":"foo"}`,
		`{"count":3,"matches":[{"file":"a.go","line":1,"content":"foo"},{"file":"b.go","line":2,"content":"foo"},{"file":"c.go","line":3,"content":"foo"}]}`)
	sl.Record(1, "find_symbol", `{"name":"Bar"}`,
		`{"count":1,"symbols":[{"file":"d.go","name":"Bar","kind":"function","start_line":10,"end_line":20}]}`)

	summary := sl.EvidenceSummary()

	if !strings.Contains(summary, "[SEARCH EVIDENCE LEDGER]") {
		t.Fatal("summary should contain ledger header")
	}
	if !strings.Contains(summary, "Searches: 2") {
		t.Fatal("summary should show 2 searches")
	}
	if !strings.Contains(summary, "Unique files: 4") {
		t.Fatal("summary should show 4 unique files")
	}
	if !strings.Contains(summary, "grep") {
		t.Fatal("summary should mention grep")
	}
	if !strings.Contains(summary, "find_symbol") {
		t.Fatal("summary should mention find_symbol")
	}
}

func TestSearchLedger_EvidenceSummaryEmpty(t *testing.T) {
	sl := NewSearchLedger()
	if sl.EvidenceSummary() != "" {
		t.Fatal("empty ledger should produce empty summary")
	}
}

func TestSearchLedger_SaturatedSummaryContainsWarning(t *testing.T) {
	sl := NewSearchLedger()

	sl.Record(0, "grep", `{"pattern":"foo"}`,
		`{"count":1,"matches":[{"file":"a.go","line":1,"content":"x"}]}`)
	sl.Record(1, "grep", `{"pattern":"bar"}`,
		`{"count":1,"matches":[{"file":"a.go","line":5,"content":"bar"}]}`)
	sl.Record(2, "find_symbol", `{"name":"Foo"}`,
		`{"count":1,"symbols":[{"file":"a.go","name":"Foo","kind":"function","start_line":10,"end_line":20}]}`)

	summary := sl.EvidenceSummary()
	if !strings.Contains(summary, "SATURATED") {
		t.Fatal("saturated summary should contain SATURATED warning")
	}
}

func TestIsSearchTool(t *testing.T) {
	searchTools := []string{"grep", "glob", "find_symbol", "search_codebase", "find_pattern", "locate_symbol", "knowledge_search", "ast_grep_search"}
	for _, name := range searchTools {
		if !IsSearchTool(name) {
			t.Errorf("expected %q to be a search tool", name)
		}
	}

	nonSearchTools := []string{"read_file", "git", "lsp", "clone_repository", "reroute_request"}
	for _, name := range nonSearchTools {
		if IsSearchTool(name) {
			t.Errorf("expected %q to NOT be a search tool", name)
		}
	}
}

func TestExtractSearchMetadata_GrepResult(t *testing.T) {
	result := `{"count":2,"matches":[{"file":"a.go","line":1,"content":"hello","symbol_context":"Foo"},{"file":"b.go","line":5,"content":"world"}]}`
	count, files, symbols := extractSearchMetadata(result)

	if count != 2 {
		t.Fatalf("expected count=2, got %d", count)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if len(symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(symbols))
	}
}

func TestExtractSearchMetadata_FindSymbolResult(t *testing.T) {
	result := `{"count":1,"symbols":[{"file":"x.go","name":"MyFunc","kind":"function","start_line":10,"end_line":20}]}`
	count, files, symbols := extractSearchMetadata(result)

	if count != 1 {
		t.Fatalf("expected count=1, got %d", count)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if len(symbols) != 1 || symbols[0] != "MyFunc" {
		t.Fatalf("expected symbol MyFunc, got %v", symbols)
	}
}

func TestExtractSearchMetadata_InvalidJSON(t *testing.T) {
	count, files, symbols := extractSearchMetadata("not json")
	if count != 0 || len(files) != 0 || len(symbols) != 0 {
		t.Fatal("invalid JSON should return zeros")
	}
}

func TestExtractArgsLabel(t *testing.T) {
	label := extractArgsLabel("grep", `{"pattern":"foo.*bar","include":"**/*.go"}`)
	if !strings.Contains(label, "pattern=") {
		t.Fatalf("expected label to contain pattern=, got %q", label)
	}
	if !strings.Contains(label, "foo.*bar") {
		t.Fatalf("expected label to contain pattern value, got %q", label)
	}
}
