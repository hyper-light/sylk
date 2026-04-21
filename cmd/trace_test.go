package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeJSONLines writes each record as one line of JSON to path, creating
// parent directories as needed. Used by the trace tests to seed a fake
// session log tree.
func writeJSONLines(t *testing.T, path string, records []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	for _, rec := range records {
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		f.Write(data)
		f.Write([]byte{'\n'})
	}
}

func TestTraceCollectAndFilterByCorrelation(t *testing.T) {
	root := t.TempDir()
	// Seed a single-session, two-agent structure:
	//   {root}/{sid}/agents/engineer/logs/2026-04-21/events.000.jsonl
	//   {root}/{sid}/agents/engineer/logs/2026-04-21/tools.000.jsonl
	//   {root}/{sid}/agents/librarian/logs/2026-04-21/events.000.jsonl
	sid := "sess-abc"
	base := filepath.Join(root, sid, "agents")
	day := "2026-04-21"

	writeJSONLines(t, filepath.Join(base, "engineer", "logs", day, "events.000.jsonl"), []map[string]any{
		{"ts": "2026-04-21T14:32:00.000Z", "level": "info", "agent": "engineer", "agent_id": "engineer-0", "session_id": sid, "event": "turn_start", "msg": "turn begins", "correlation_id": "corr-1"},
		{"ts": "2026-04-21T14:32:05.000Z", "level": "info", "agent": "engineer", "session_id": sid, "event": "unrelated", "correlation_id": "corr-other"},
	})
	writeJSONLines(t, filepath.Join(base, "engineer", "logs", day, "tools.000.jsonl"), []map[string]any{
		{"ts": "2026-04-21T14:32:01.000Z", "phase": "start", "tool_call_key": "tc-1", "correlation_id": "corr-1", "agent_type": "engineer", "tool_name": "consult_peer"},
		{"ts": "2026-04-21T14:32:01.200Z", "phase": "complete", "tool_call_key": "tc-1", "correlation_id": "corr-1", "agent_type": "engineer", "tool_name": "consult_peer", "success": true, "duration_ms": 200},
	})
	writeJSONLines(t, filepath.Join(base, "librarian", "logs", day, "events.000.jsonl"), []map[string]any{
		{"ts": "2026-04-21T14:32:01.100Z", "level": "info", "agent": "librarian", "session_id": sid, "event": "consult_received", "correlation_id": "corr-1", "tool_call_key": "tc-1"},
	})

	// Point the CLI at the fake tree.
	orig := traceSessionDir
	traceSessionDir = root
	defer func() { traceSessionDir = orig }()

	entries, err := collectEntries(traceSessionDirResolved())
	if err != nil {
		t.Fatalf("collectEntries: %v", err)
	}
	if len(entries) < 4 {
		t.Fatalf("expected >=4 entries, got %d", len(entries))
	}

	// Render filtered: corr-1 should produce three rows (engineer event + two tool phases + librarian event = 4, minus the corr-other event).
	var buf bytes.Buffer
	filter := func(e *traceEntry) bool { return e.CorrelationID == "corr-1" }
	if err := renderFiltered(&buf, entries, filter); err != nil {
		t.Fatalf("renderFiltered: %v", err)
	}
	out := buf.String()
	// Sanity: every line should mention 14:32, and none should
	// contain the unrelated correlation's event name.
	if strings.Contains(out, "unrelated") {
		t.Fatalf("filter leaked unrelated event: %q", out)
	}
	if !strings.Contains(out, "consult_peer") {
		t.Fatalf("expected consult_peer in output: %q", out)
	}
	if !strings.Contains(out, "librarian") {
		t.Fatalf("expected librarian in output: %q", out)
	}
}

func TestTraceFilterByTool(t *testing.T) {
	root := t.TempDir()
	sid := "sess-xyz"
	base := filepath.Join(root, sid, "agents")
	day := "2026-04-21"

	writeJSONLines(t, filepath.Join(base, "engineer", "logs", day, "tools.000.jsonl"), []map[string]any{
		{"ts": "2026-04-21T10:00:00.000Z", "phase": "start", "tool_call_key": "parent", "correlation_id": "cid", "agent_type": "engineer", "tool_name": "consult_peer"},
		{"ts": "2026-04-21T10:00:00.500Z", "phase": "start", "tool_call_key": "child", "parent_tool_call_key": "parent", "correlation_id": "cid", "agent_type": "librarian", "tool_name": "read_file"},
		{"ts": "2026-04-21T10:00:01.000Z", "phase": "complete", "tool_call_key": "child", "correlation_id": "cid", "agent_type": "librarian", "tool_name": "read_file", "success": true, "duration_ms": 500},
		{"ts": "2026-04-21T10:00:02.000Z", "phase": "complete", "tool_call_key": "parent", "correlation_id": "cid", "agent_type": "engineer", "tool_name": "consult_peer", "success": true, "duration_ms": 2000},
		{"ts": "2026-04-21T10:01:00.000Z", "phase": "start", "tool_call_key": "unrelated", "correlation_id": "other", "agent_type": "engineer", "tool_name": "read_file"},
	})

	orig := traceSessionDir
	traceSessionDir = root
	defer func() { traceSessionDir = orig }()

	entries, err := collectEntries(traceSessionDirResolved())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	var buf bytes.Buffer
	filter := func(e *traceEntry) bool {
		return e.ToolCallKey == "parent" || e.ParentToolCallKey == "parent"
	}
	if err := renderFiltered(&buf, entries, filter); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "unrelated") {
		t.Fatalf("tool filter leaked unrelated entry: %q", out)
	}
	if !strings.Contains(out, "parent") || !strings.Contains(out, "child") {
		t.Fatalf("expected parent+child in output: %q", out)
	}
}

func TestTraceStreamClassification(t *testing.T) {
	if s, ok := streamForFilename("events.000.jsonl"); !ok || s != "events" {
		t.Fatalf("events classification: %q ok=%v", s, ok)
	}
	if s, ok := streamForFilename("tools.012.jsonl"); !ok || s != "tools" {
		t.Fatalf("tools classification: %q ok=%v", s, ok)
	}
	if s, ok := streamForFilename("llm.000.jsonl"); !ok || s != "llm" {
		t.Fatalf("llm classification: %q ok=%v", s, ok)
	}
	if _, ok := streamForFilename("activity.jsonl"); ok {
		t.Fatal("activity.jsonl (legacy) should not classify as a new stream")
	}
	if _, ok := streamForFilename("random.txt"); ok {
		t.Fatal("non-jsonl files should not classify")
	}
}
