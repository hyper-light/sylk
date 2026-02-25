package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

func TestWithToolCallEmitter_RoundTrip(t *testing.T) {
	var received []ToolCallEvent
	emitter := func(ev ToolCallEvent) { received = append(received, ev) }

	ctx := WithToolCallEmitter(context.Background(), emitter)
	EmitToolCall(ctx, ToolCallEvent{ToolName: "read_file", Phase: ToolCallStart})

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].ToolName != "read_file" {
		t.Errorf("expected tool_name=read_file, got %s", received[0].ToolName)
	}
}

func TestEmitToolCall_NilEmitter(t *testing.T) {
	// Must not panic with a bare context.
	EmitToolCall(context.Background(), ToolCallEvent{ToolName: "test"})
}

func TestTimedToolCall_Success(t *testing.T) {
	var events []ToolCallEvent
	emitter := func(ev ToolCallEvent) { events = append(events, ev) }
	ctx := WithToolCallEmitter(context.Background(), emitter)

	call := providers.ToolCall{ID: "1", Name: "grep", Arguments: `{"pattern":"foo"}`}
	result, err := TimedToolCall(ctx, "engineer", call, func() (string, error) {
		time.Sleep(5 * time.Millisecond)
		return "found 3 matches", nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "found 3 matches" {
		t.Errorf("expected result 'found 3 matches', got %q", result)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (start+complete), got %d", len(events))
	}

	start := events[0]
	if start.Phase != ToolCallStart {
		t.Errorf("expected phase=start, got %d", start.Phase)
	}
	if start.ToolName != "grep" {
		t.Errorf("expected tool_name=grep, got %s", start.ToolName)
	}
	if start.AgentID != "engineer" {
		t.Errorf("expected agent_id=engineer, got %s", start.AgentID)
	}

	complete := events[1]
	if complete.Phase != ToolCallComplete {
		t.Errorf("expected phase=complete, got %d", complete.Phase)
	}
	if !complete.Success {
		t.Error("expected success=true")
	}
	if complete.Duration < 5*time.Millisecond {
		t.Errorf("expected duration >= 5ms, got %v", complete.Duration)
	}
	if complete.Output != "found 3 matches" {
		t.Errorf("expected output 'found 3 matches', got %q", complete.Output)
	}
}

func TestTimedToolCall_Error(t *testing.T) {
	var events []ToolCallEvent
	emitter := func(ev ToolCallEvent) { events = append(events, ev) }
	ctx := WithToolCallEmitter(context.Background(), emitter)

	call := providers.ToolCall{ID: "2", Name: "run_command", Arguments: `{"command":"make"}`}
	_, err := TimedToolCall(ctx, "engineer", call, func() (string, error) {
		return "", errTestToolFailed
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	complete := events[1]
	if complete.Success {
		t.Error("expected success=false on error")
	}
	if complete.ErrorMsg == "" {
		t.Error("expected non-empty error_msg")
	}
}

var errTestToolFailed = &testError{msg: "tool failed: exit code 1"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func TestTimedToolCall_NoEmitter(t *testing.T) {
	// TimedToolCall must work without panicking when no emitter is set.
	call := providers.ToolCall{ID: "3", Name: "read_file", Arguments: `{}`}
	result, err := TimedToolCall(context.Background(), "test", call, func() (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

func TestSummarizeToolArgs(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     string
		want     string
	}{
		{"path key", "read_file", `{"file_path":"core/main.go","offset":1}`, "file_path=core/main.go"},
		{"pattern key", "grep", `{"pattern":"StreamEvent"}`, "pattern=StreamEvent"},
		{"command key", "run_command", `{"command":"go build ./..."}`, "command=go build ./..."},
		{"empty args", "test", `{}`, ""},
		{"empty string", "test", "", ""},
		{"invalid json", "test", "not json", ""},
		{"no priority keys", "custom", `{"foo":"bar"}`, "foo=bar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SummarizeToolArgs(tt.toolName, tt.args)
			if got != tt.want {
				t.Errorf("SummarizeToolArgs(%q, %q) = %q, want %q", tt.toolName, tt.args, got, tt.want)
			}
		})
	}
}

func TestSummarizeToolArgs_Truncation(t *testing.T) {
	longPath := "/very/long/path/that/exceeds/sixty/characters/and/should/be/truncated/by/the/summarizer"
	got := SummarizeToolArgs("read_file", `{"path":"`+longPath+`"}`)
	if len([]rune(got)) > maxArgsSummaryLen {
		t.Errorf("summary exceeds %d chars: %d", maxArgsSummaryLen, len([]rune(got)))
	}
}

func TestTruncateOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		max      int
		expected string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncated", "hello world", 8, "hello..."},
		{"tiny max", "abcd", 3, "..."},
		{"empty", "", 10, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateOutput(tt.input, tt.max)
			if got != tt.expected {
				t.Errorf("TruncateOutput(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expected)
			}
		})
	}
}

func TestPrettyPrintArgs(t *testing.T) {
	got := PrettyPrintArgs(`{"a":1,"b":"two"}`)
	if got == `{"a":1,"b":"two"}` {
		t.Error("expected indented output, got raw")
	}
	// Should contain newlines and indentation.
	if len(got) <= len(`{"a":1,"b":"two"}`) {
		t.Errorf("expected longer formatted output, got %q", got)
	}
}

func TestPrettyPrintArgs_Invalid(t *testing.T) {
	got := PrettyPrintArgs("not json")
	if got != "not json" {
		t.Errorf("expected passthrough for invalid JSON, got %q", got)
	}
}

func TestPrettyPrintArgs_Empty(t *testing.T) {
	if got := PrettyPrintArgs(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := PrettyPrintArgs("{}"); got != "{}" {
		t.Errorf("expected '{}', got %q", got)
	}
}
