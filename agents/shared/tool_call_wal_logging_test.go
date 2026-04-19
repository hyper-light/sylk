package shared

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
)

// TestToolCallSession_LogsStartAndCompleteToWAL pins the single-point
// instrumentation contract: every ToolCallSession Start emits a
// tool_call_started event to the agent's WAL/JSONL log, and every
// Complete emits a tool_call_completed event with duration, success,
// and output size. This is the canonical hook all 10 agents inherit
// without per-agent wiring — if it regresses, the WAL stops carrying
// tool-call lifecycle records and observability collapses.
func TestToolCallSession_LogsStartAndCompleteToWAL(t *testing.T) {
	dir := t.TempDir()
	logger := agentlog.NewSessionEventLogger("engineer", "events")
	if err := logger.BindSession(dir, "ses_test"); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	ctx := WithLogMeta(context.Background(), LogMeta{
		EventLogger: logger,
		AgentID:     "engineer",
		SessionID:   "ses_test",
		CorrID:      "corr-tcwal-1",
	})

	call := providers.ToolCall{ID: "call-1", Name: "read_file", Arguments: `{"path":"/tmp/x"}`}
	session := acquireToolCallSession(ctx, "engineer", call)
	if !session.Start(call, time.Now(), nil) {
		t.Fatal("expected Start to fire")
	}
	if !session.Complete(call, "file contents here", nil) {
		t.Fatal("expected Complete to fire")
	}

	entries := logger.RecentEntries(8)
	var sawStart, sawComplete bool
	for _, e := range entries {
		switch e.EventCode {
		case agentlog.EventToolCallStarted:
			sawStart = true
			data, _ := e.Data.(map[string]any)
			if data["tool_name"] != "read_file" {
				t.Fatalf("start tool_name = %v, want read_file", data["tool_name"])
			}
			if data["tool_call_id"] != "call-1" {
				t.Fatalf("start tool_call_id = %v, want call-1", data["tool_call_id"])
			}
		case agentlog.EventToolCallCompleted:
			sawComplete = true
			data, _ := e.Data.(map[string]any)
			if data["success"] != true {
				t.Fatalf("complete success = %v, want true", data["success"])
			}
			if data["output_size"].(int) <= 0 {
				t.Fatalf("complete output_size = %v, want >0", data["output_size"])
			}
			if _, ok := data["duration_ms"]; !ok {
				t.Fatal("complete missing duration_ms")
			}
		}
	}
	if !sawStart {
		t.Fatal("EventToolCallStarted not found in log entries")
	}
	if !sawComplete {
		t.Fatal("EventToolCallCompleted not found in log entries")
	}
}

// TestToolCallSession_LogsCompleteWithErrorAtWarnLevel pins the
// error-path contract: a failed tool call still emits a completion
// event, but at warn level with the error string attached. Without
// this, error tool calls would silently disappear from the log.
func TestToolCallSession_LogsCompleteWithErrorAtWarnLevel(t *testing.T) {
	dir := t.TempDir()
	logger := agentlog.NewSessionEventLogger("designer", "events")
	if err := logger.BindSession(dir, "ses_test"); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	ctx := WithLogMeta(context.Background(), LogMeta{
		EventLogger: logger,
		AgentID:     "designer",
		SessionID:   "ses_test",
		CorrID:      "corr-tcwal-2",
	})

	call := providers.ToolCall{ID: "call-2", Name: "edit_file", Arguments: `{"path":"/x"}`}
	session := acquireToolCallSession(ctx, "designer", call)
	session.Start(call, time.Now(), nil)
	session.Complete(call, "", errors.New("permission denied"))

	var sawErrorComplete bool
	for _, e := range logger.RecentEntries(8) {
		if e.EventCode != agentlog.EventToolCallCompleted {
			continue
		}
		if e.Level != "warn" {
			t.Fatalf("error completion level = %q, want warn", e.Level)
		}
		data, _ := e.Data.(map[string]any)
		if data["success"] != false {
			t.Fatalf("error completion success = %v, want false", data["success"])
		}
		if data["error"] != "permission denied" {
			t.Fatalf("error completion error = %v, want permission denied", data["error"])
		}
		sawErrorComplete = true
	}
	if !sawErrorComplete {
		t.Fatal("error completion event not found")
	}
}

// TestToolCallSession_NoEventLoggerIsSafe pins that the WAL hook is
// nil-safe — agents that haven't bound a session yet must not panic
// when tool calls fire. The hook should silently skip and let the
// existing UI emission path proceed normally.
func TestToolCallSession_NoEventLoggerIsSafe(t *testing.T) {
	ctx := context.Background() // no LogMeta
	call := providers.ToolCall{ID: "call-3", Name: "glob", Arguments: `{}`}
	session := acquireToolCallSession(ctx, "librarian", call)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Start/Complete panicked without logger: %v", r)
		}
	}()
	session.Start(call, time.Now(), nil)
	session.Complete(call, "matched 0 files", nil)
}
