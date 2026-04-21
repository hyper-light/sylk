package agentlog

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestToolRecorder_StartAndCompletePair(t *testing.T) {
	dir := t.TempDir()
	w, err := NewStreamWriter(dir, "tools", StreamWriterConfig{
		Location: time.UTC,
		Now:      func() time.Time { return time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC) },
	}, nil)
	if err != nil {
		t.Fatalf("writer: %v", err)
	}
	r := NewToolRecorder(w)
	err = r.Start(ToolStartRecord{
		ToolCallKey:   "tc-1",
		CorrelationID: "corr-1",
		ToolName:      "consult_peer",
		Args:          json.RawMessage(`{"target_agent_type":"librarian"}`),
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	err = r.Complete(ToolCompleteRecord{
		ToolCallKey:   "tc-1",
		CorrelationID: "corr-1",
		ToolName:      "consult_peer",
		DurationMs:    184,
		Success:       true,
		Output:        json.RawMessage(`{"status":"completed"}`),
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	_ = w.Close()

	entries := mustReadJSONL(t, filepath.Join(dir, "2026-04-21", "tools.000.jsonl"))
	if len(entries) != 2 {
		t.Fatalf("expected 2 records, got %d", len(entries))
	}
	if entries[0]["phase"] != "start" {
		t.Fatalf("first record not start: %#v", entries[0])
	}
	if entries[1]["phase"] != "complete" {
		t.Fatalf("second record not complete: %#v", entries[1])
	}
	if entries[0]["tool_call_key"] != entries[1]["tool_call_key"] {
		t.Fatalf("key mismatch between phases: %v / %v", entries[0]["tool_call_key"], entries[1]["tool_call_key"])
	}
	if entries[1]["success"] != true {
		t.Fatalf("expected success=true: %#v", entries[1])
	}
}

func TestToolRecorder_NilReceiverIsNoop(t *testing.T) {
	var r *ToolRecorder
	if err := r.Start(ToolStartRecord{}); err != nil {
		t.Fatalf("nil start: %v", err)
	}
	if err := r.Complete(ToolCompleteRecord{}); err != nil {
		t.Fatalf("nil complete: %v", err)
	}
}

func TestLLMRecorder_ContentAddressesPromptAndToolDefs(t *testing.T) {
	dir := t.TempDir()
	corpusDir := filepath.Join(dir, "prompts")

	w, err := NewStreamWriter(dir, "llm", StreamWriterConfig{
		Location: time.UTC,
		Now:      func() time.Time { return time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC) },
	}, nil)
	if err != nil {
		t.Fatalf("writer: %v", err)
	}
	corpus := NewPromptCorpus(corpusDir, nil)
	r := NewLLMRecorder(w, corpus, LLMRecorderConfig{
		ContentAddressPrompts: true,
	})

	systemPrompt := "You are the engineer agent."
	toolDefs := `[{"name":"read_file"},{"name":"write_file"}]`

	err = r.Start(LLMStartRecord{
		LLMCallID:    "llm-1",
		Model:        "claude-opus-4-7",
		SystemPrompt: systemPrompt,
		Messages: []LLMMessage{
			{Role: "user", Content: "hello"},
		},
	}, toolDefs)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = r.Complete(LLMCompleteRecord{
		LLMCallID:    "llm-1",
		ResponseText: "hi",
		DurationMs:   50,
	})
	_ = w.Close()

	entries := mustReadJSONL(t, filepath.Join(dir, "2026-04-21", "llm.000.jsonl"))
	if len(entries) != 2 {
		t.Fatalf("expected start+complete, got %d", len(entries))
	}
	start := entries[0]
	if start["event"] != "llm_start" {
		t.Fatalf("first record not llm_start: %#v", start)
	}
	if start["system_prompt"] != nil && start["system_prompt"] != "" {
		t.Fatalf("system_prompt should be stripped, got %v", start["system_prompt"])
	}
	if start["system_prompt_sha256"] == nil {
		t.Fatal("expected system_prompt_sha256 reference")
	}
	if start["tool_definitions_sha256"] == nil {
		t.Fatal("expected tool_definitions_sha256 reference")
	}

	// The corpus file must exist and round-trip.
	systemHash, _ := start["system_prompt_sha256"].(string)
	got, ok := corpus.Get(systemHash)
	if !ok || got != systemPrompt {
		t.Fatalf("corpus did not store system prompt: ok=%v got=%q", ok, got)
	}
}

func TestLLMRecorder_MessageTruncation(t *testing.T) {
	dir := t.TempDir()
	w, err := NewStreamWriter(dir, "llm", StreamWriterConfig{
		Location: time.UTC,
		Now:      func() time.Time { return time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC) },
	}, nil)
	if err != nil {
		t.Fatalf("writer: %v", err)
	}
	r := NewLLMRecorder(w, nil, LLMRecorderConfig{
		TruncateMessageAt:     256,
		ContentAddressPrompts: false,
	})
	longContent := make([]byte, 4096)
	for i := range longContent {
		longContent[i] = 'A'
	}
	err = r.Start(LLMStartRecord{
		LLMCallID: "llm-2",
		Messages: []LLMMessage{
			{Role: "user", Content: string(longContent)},
		},
	}, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = w.Close()

	entries := mustReadJSONL(t, filepath.Join(dir, "2026-04-21", "llm.000.jsonl"))
	if len(entries) != 1 {
		t.Fatalf("expected 1 record")
	}
	msgs := entries[0]["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message")
	}
	content := msgs[0].(map[string]any)["content"].(string)
	if len(content) >= len(longContent) {
		t.Fatalf("content not truncated: %d chars", len(content))
	}
	if len(content) > 512 {
		t.Fatalf("truncation cap too loose: %d chars", len(content))
	}
}
