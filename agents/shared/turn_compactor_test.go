package shared

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func newCounter() providers.TokenCounter {
	return providers.NewCharacterBasedCounter(providers.DefaultTokenCounterConfig())
}

func TestIdentifyTurnGroups_Basic(t *testing.T) {
	messages := []providers.Message{
		{Role: providers.RoleUser, Content: "initial prompt"},
		{Role: providers.RoleAssistant, Content: "thinking", ToolCalls: []providers.ToolCall{{ID: "1", Name: "read_file"}}},
		{Role: providers.RoleTool, ToolCallID: "1", ToolName: "read_file", Content: "file content here"},
		{Role: providers.RoleAssistant, Content: "more thinking", ToolCalls: []providers.ToolCall{{ID: "2", Name: "grep"}}},
		{Role: providers.RoleTool, ToolCallID: "2", ToolName: "grep", Content: "grep results"},
	}
	groups := IdentifyTurnGroups(messages, newCounter())
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].AssistantIdx != 1 {
		t.Fatalf("group[0] assistant at %d, expected 1", groups[0].AssistantIdx)
	}
	if len(groups[0].ToolIdxs) != 1 || groups[0].ToolIdxs[0] != 2 {
		t.Fatalf("group[0] tool indices: %v", groups[0].ToolIdxs)
	}
	if groups[1].AssistantIdx != 3 {
		t.Fatalf("group[1] assistant at %d, expected 3", groups[1].AssistantIdx)
	}
}

func TestIdentifyTurnGroups_MultipleToolResults(t *testing.T) {
	messages := []providers.Message{
		{Role: providers.RoleUser, Content: "do something"},
		{Role: providers.RoleAssistant, Content: "ok", ToolCalls: []providers.ToolCall{
			{ID: "1", Name: "read_file"}, {ID: "2", Name: "grep"},
		}},
		{Role: providers.RoleTool, ToolCallID: "1", ToolName: "read_file", Content: "file1"},
		{Role: providers.RoleTool, ToolCallID: "2", ToolName: "grep", Content: "results"},
	}
	groups := IdentifyTurnGroups(messages, newCounter())
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups[0].ToolIdxs) != 2 {
		t.Fatalf("expected 2 tool results, got %d", len(groups[0].ToolIdxs))
	}
}

func TestIdentifyTurnGroups_SkipsAssistantWithoutToolCalls(t *testing.T) {
	messages := []providers.Message{
		{Role: providers.RoleUser, Content: "hello"},
		{Role: providers.RoleAssistant, Content: "just a reply, no tools"},
	}
	groups := IdentifyTurnGroups(messages, newCounter())
	if len(groups) != 0 {
		t.Fatalf("expected 0 groups for assistant without tool calls, got %d", len(groups))
	}
}

func TestCompactTurnGroups_PreservesRecent(t *testing.T) {
	messages := []providers.Message{
		{Role: providers.RoleUser, Content: "prompt"},
		{Role: providers.RoleAssistant, Content: "t1", ToolCalls: []providers.ToolCall{{ID: "1", Name: "exec"}}},
		{Role: providers.RoleTool, ToolCallID: "1", ToolName: "exec", Content: "very long output " + makeString(500)},
		{Role: providers.RoleAssistant, Content: "t2", ToolCalls: []providers.ToolCall{{ID: "2", Name: "exec"}}},
		{Role: providers.RoleTool, ToolCallID: "2", ToolName: "exec", Content: "another long output " + makeString(500)},
		{Role: providers.RoleAssistant, Content: "t3", ToolCalls: []providers.ToolCall{{ID: "3", Name: "exec"}}},
		{Role: providers.RoleTool, ToolCallID: "3", ToolName: "exec", Content: "recent output"},
	}
	counter := newCounter()
	groups := IdentifyTurnGroups(messages, counter)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}

	freed := CompactTurnGroups(messages, groups, 2, counter)
	if freed <= 0 {
		t.Fatal("expected positive tokens freed")
	}
	// First group should be compacted.
	if !strings.HasPrefix(messages[2].Content, "[") {
		t.Fatalf("expected compacted content, got: %s", messages[2].Content[:50])
	}
	// Last two groups should be untouched.
	if strings.HasPrefix(messages[4].Content, "[") {
		t.Fatal("second group should not be compacted (preserveRecent=2)")
	}
	if messages[6].Content != "recent output" {
		t.Fatal("third group should not be compacted")
	}
}

func TestCompactTurnGroups_NeverModifiesAssistant(t *testing.T) {
	messages := []providers.Message{
		{Role: providers.RoleUser, Content: "prompt"},
		{Role: providers.RoleAssistant, Content: "original assistant content",
			Metadata:  map[string]any{"thinking": "deep thoughts"},
			ToolCalls: []providers.ToolCall{{ID: "1", Name: "exec"}}},
		{Role: providers.RoleTool, ToolCallID: "1", ToolName: "exec", Content: makeString(1000)},
	}
	counter := newCounter()
	groups := IdentifyTurnGroups(messages, counter)
	CompactTurnGroups(messages, groups, 0, counter)

	if messages[1].Content != "original assistant content" {
		t.Fatal("assistant content was modified")
	}
	if messages[1].Metadata["thinking"] != "deep thoughts" {
		t.Fatal("assistant metadata was modified")
	}
}

func TestCompactTurnGroups_SkipsAlreadyCompacted(t *testing.T) {
	messages := []providers.Message{
		{Role: providers.RoleUser, Content: "prompt"},
		{Role: providers.RoleAssistant, Content: "t1", ToolCalls: []providers.ToolCall{{ID: "1", Name: "exec"}}},
		{Role: providers.RoleTool, ToolCallID: "1", ToolName: "exec", Content: "[exec: 50 lines]"},
	}
	counter := newCounter()
	groups := IdentifyTurnGroups(messages, counter)
	freed := CompactTurnGroups(messages, groups, 0, counter)
	if freed != 0 {
		t.Fatalf("expected 0 tokens freed for already-compacted content, got %d", freed)
	}
}

func TestEvictTurnGroup(t *testing.T) {
	messages := []providers.Message{
		{Role: providers.RoleUser, Content: "prompt"},
		{Role: providers.RoleAssistant, Content: "t1", ToolCalls: []providers.ToolCall{{ID: "1", Name: "exec"}}},
		{Role: providers.RoleTool, ToolCallID: "1", ToolName: "exec", Content: "output1"},
		{Role: providers.RoleAssistant, Content: "t2", ToolCalls: []providers.ToolCall{{ID: "2", Name: "grep"}}},
		{Role: providers.RoleTool, ToolCallID: "2", ToolName: "grep", Content: "output2"},
	}
	groups := IdentifyTurnGroups(messages, newCounter())
	result := EvictTurnGroup(messages, groups[0])
	if len(result) != 3 {
		t.Fatalf("expected 3 messages after eviction, got %d", len(result))
	}
	if result[0].Content != "prompt" {
		t.Fatal("user prompt should survive eviction")
	}
	if result[1].Content != "t2" {
		t.Fatalf("expected t2 assistant, got %s", result[1].Content)
	}
}

func TestSummarizeToolResult_Empty(t *testing.T) {
	s := SummarizeToolResult("read_file", "")
	if s != "[read_file: empty]" {
		t.Fatalf("unexpected: %q", s)
	}
}

func TestSummarizeToolResult_WithErrors(t *testing.T) {
	content := "line1\nline2\nError: something failed\nline4\nWarning: deprecated"
	s := SummarizeToolResult("exec", content)
	if !strings.HasPrefix(s, "[exec: 5 lines") {
		t.Fatalf("unexpected header: %q", s)
	}
	if !strings.Contains(s, "1 errors") {
		t.Fatalf("expected error count: %q", s)
	}
	if !strings.Contains(s, "1 warnings") {
		t.Fatalf("expected warning count: %q", s)
	}
	if !strings.Contains(s, "Error: something failed") {
		t.Fatal("expected important line preserved")
	}
}

func TestSummarizeToolResult_FileLinePatterns(t *testing.T) {
	content := "ok\nmain.go:42:5: undefined: foo\nfine"
	s := SummarizeToolResult("exec", content)
	if !strings.Contains(s, "main.go:42:5") {
		t.Fatalf("expected file:line preserved: %q", s)
	}
}

func TestSummarizeToolResult_NoImportantLines(t *testing.T) {
	content := "all good\nnothing special\njust data"
	s := SummarizeToolResult("read_file", content)
	if !strings.HasPrefix(s, "[read_file: 3 lines]") {
		t.Fatalf("unexpected: %q", s)
	}
	// No important lines → no newlines after header.
	if strings.Contains(s, "\n") {
		t.Fatalf("should not have preserved lines: %q", s)
	}
}
