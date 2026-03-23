package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

// --- MarshalToolOutput ---

func TestMarshalToolOutput_StringPassthrough(t *testing.T) {
	input := "hello world"
	got, err := MarshalToolOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != input {
		t.Fatalf("expected %q, got %q", input, got)
	}
}

type testStringer struct{ val string }

func (s testStringer) String() string { return s.val }

func TestMarshalToolOutput_FmtStringer(t *testing.T) {
	input := testStringer{val: "stringer-output"}
	got, err := MarshalToolOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != input.val {
		t.Fatalf("expected %q, got %q", input.val, got)
	}
}

func TestMarshalToolOutput_StructJSON(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	input := payload{Name: "test", Count: 42}
	got, err := MarshalToolOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var roundtrip payload
	if err := json.Unmarshal([]byte(got), &roundtrip); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if roundtrip.Name != input.Name || roundtrip.Count != input.Count {
		t.Fatalf("roundtrip mismatch: got %+v, want %+v", roundtrip, input)
	}
}

func TestMarshalToolOutput_Nil(t *testing.T) {
	got, err := MarshalToolOutput(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "null" {
		t.Fatalf("expected \"null\" for nil input, got %q", got)
	}
}

// --- ToolErrorPayload ---

func TestToolErrorPayload_NonNilError(t *testing.T) {
	testErr := errors.New("something broke")
	got := ToolErrorPayload(testErr)
	if got == "" {
		t.Fatal("expected non-empty payload for non-nil error")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	errMsg, ok := parsed["error"].(string)
	if !ok {
		t.Fatal("payload missing 'error' key or not a string")
	}
	if errMsg != testErr.Error() {
		t.Fatalf("expected error message %q, got %q", testErr.Error(), errMsg)
	}
}

func TestToolErrorPayload_IncludesRecoveryForSingleCommandViolation(t *testing.T) {
	got := ToolErrorPayload(errors.New("run_command only accepts one plain command (detected &&)"))
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if parsed["error_kind"] != "single_command_only" {
		t.Fatalf("error_kind = %v, want %q", parsed["error_kind"], "single_command_only")
	}
	recovery, ok := parsed["recovery"].([]any)
	if !ok || len(recovery) == 0 {
		t.Fatalf("expected recovery guidance, got %#v", parsed["recovery"])
	}
}

func TestToolErrorPayload_NilError(t *testing.T) {
	got := ToolErrorPayload(nil)
	if got != "" {
		t.Fatalf("expected empty string for nil error, got %q", got)
	}
}

func TestAppendToolRecoveryMessage_DeduplicatesHints(t *testing.T) {
	req := &providers.Request{}
	AppendToolRecoveryMessage(req, []string{"adapt", "adapt", "", "switch tools"})
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 appended message, got %d", len(req.Messages))
	}
	content := req.Messages[0].Content
	if !strings.Contains(content, "adapt") || !strings.Contains(content, "switch tools") {
		t.Fatalf("unexpected recovery message content: %q", content)
	}
}

// --- CoerceMap ---

func TestCoerceMap_Nil(t *testing.T) {
	got := CoerceMap(nil)
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestCoerceMap_MapPassthrough(t *testing.T) {
	input := map[string]any{"key": "value"}
	got := CoerceMap(input)
	if got == nil {
		t.Fatal("expected non-nil map")
	}
	if got["key"] != "value" {
		t.Fatalf("expected key=value, got key=%v", got["key"])
	}
}

func TestCoerceMap_Struct(t *testing.T) {
	type sample struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	input := sample{Name: "alice", Age: 30}
	got := CoerceMap(input)
	if got == nil {
		t.Fatal("expected non-nil map from struct")
	}
	if got["name"] != "alice" {
		t.Fatalf("expected name=alice, got name=%v", got["name"])
	}
}

func TestCoerceMap_Unmarshalable(t *testing.T) {
	// A channel cannot be JSON-marshaled.
	input := make(chan int)
	got := CoerceMap(input)
	if got != nil {
		t.Fatalf("expected nil for unmarshalable value, got %v", got)
	}
}

// --- DetectToolCallDuplicate ---

func TestDetectToolCallDuplicate_AllNew(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: "readFile", Arguments: `{"path":"a.go"}`},
		{ID: "2", Name: "readFile", Arguments: `{"path":"b.go"}`},
	}
	seen := make(map[ToolCallSignature]int)
	allDup, _ := DetectToolCallDuplicate(calls, seen)
	if allDup {
		t.Fatal("expected allDup=false for all-new batch")
	}
}

func TestDetectToolCallDuplicate_OnlyBlocksThirdIdenticalBatch(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: "readFile", Arguments: `{"path":"a.go"}`},
	}
	seen := make(map[ToolCallSignature]int)
	DetectToolCallDuplicate(calls, seen)

	history := []providers.Message{
		{
			Role:      providers.RoleAssistant,
			ToolCalls: calls,
		},
		{
			Role:     providers.RoleTool,
			ToolName: "readFile",
			Content:  `{"path":"a.go","content":"package main"}`,
		},
	}

	allDup, firstDup := DetectToolCallDuplicate(calls, seen, history)
	if allDup {
		t.Fatal("expected second identical batch to be tolerated")
	}

	allDup, firstDup = DetectToolCallDuplicate(calls, seen, history)
	if !allDup {
		t.Fatal("expected third identical batch to be rejected")
	}
	if firstDup.Name != "readFile" {
		t.Fatalf("expected firstDup.Name=%q, got %q", "readFile", firstDup.Name)
	}
}

func TestDetectToolCallDuplicate_PartialDup(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: "readFile", Arguments: `{"path":"a.go"}`},
	}
	seen := make(map[ToolCallSignature]int)
	DetectToolCallDuplicate(calls, seen)

	mixed := []providers.ToolCall{
		{ID: "2", Name: "readFile", Arguments: `{"path":"a.go"}`}, // dup
		{ID: "3", Name: "readFile", Arguments: `{"path":"c.go"}`}, // new
	}
	allDup, _ := DetectToolCallDuplicate(mixed, seen)
	if allDup {
		t.Fatal("expected allDup=false when batch has a new call")
	}
}

func TestDetectToolCallDuplicate_AllowsSameCallAfterInterveningBatch(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: "read_workspace_file", Arguments: `{"path":"src/hello/greeter.py"}`},
	}
	seen := make(map[ToolCallSignature]int)
	DetectToolCallDuplicate(calls, seen)

	history := []providers.Message{
		{
			Role: providers.RoleAssistant,
			ToolCalls: []providers.ToolCall{
				{ID: "2", Name: "inspect_workspace_state", Arguments: `{"paths":["src/hello"]}`},
			},
		},
		{
			Role:     providers.RoleTool,
			ToolName: "inspect_workspace_state",
			Content:  `{"summary":"pipeline-only files exist"}`,
		},
	}

	allDup, firstDup := DetectToolCallDuplicate(calls, seen, history)
	if allDup {
		t.Fatal("expected same read call after an intervening batch to be allowed")
	}
	if firstDup.Name != "" {
		t.Fatalf("expected no duplicate signature, got %+v", firstDup)
	}
}

func TestDetectToolCallDuplicate_AllowsSecondIdenticalWriteBatch(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: "write_test", Arguments: `{"output_file":"tests/test_cli.py","content":"def test_cli():\\n    assert True\\n"}`},
	}
	seen := make(map[ToolCallSignature]int)
	DetectToolCallDuplicate(calls, seen)

	history := []providers.Message{
		{
			Role:      providers.RoleAssistant,
			ToolCalls: calls,
		},
		{
			Role:     providers.RoleTool,
			ToolName: "write_test",
			Content:  `{"output_file":"tests/test_cli.py","next_basis":"lease-2"}`,
		},
	}

	allDup, firstDup := DetectToolCallDuplicate(calls, seen, history)
	if allDup {
		t.Fatal("expected second identical write batch to be allowed")
	}
	if firstDup.Name != "" {
		t.Fatalf("expected no duplicate signature, got %+v", firstDup)
	}
}

func TestDetectToolCallDuplicate_EmptyBatch(t *testing.T) {
	seen := make(map[ToolCallSignature]int)
	allDup, firstDup := DetectToolCallDuplicate(nil, seen)
	// Empty batch: every element (vacuously) is a dup.
	if !allDup {
		t.Fatal("expected allDup=true for empty batch (vacuous truth)")
	}
	if firstDup.Name != "" {
		t.Fatalf("expected zero-value firstDup for empty batch, got %+v", firstDup)
	}
}

func TestDetectToolCallDuplicate_AllowsStaleWriteBasisPrepareRetry(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: preparePipelineWriteContextTool, Arguments: `{"path":"main.go"}`},
	}
	seen := make(map[ToolCallSignature]int)
	DetectToolCallDuplicate(calls, seen)

	history := []providers.Message{
		{
			Role:    providers.RoleAssistant,
			Content: "Trying the write.",
			ToolCalls: []providers.ToolCall{
				{ID: "write-1", Name: "write_pipeline_file", Arguments: `{"path":"main.go"}`},
			},
		},
		{
			Role:     providers.RoleTool,
			ToolName: "write_pipeline_file",
			IsError:  true,
			Content:  `{"error":"tool \"write_pipeline_file\" failed: pipeline write basis is stale: disk availability changed; rerun prepare_pipeline_write_context"}`,
		},
	}

	allDup, firstDup := DetectToolCallDuplicate(calls, seen, history)
	if allDup {
		t.Fatal("expected stale-basis retry to bypass duplicate rejection")
	}
	if firstDup.Name != "" {
		t.Fatalf("expected no duplicate signature, got %+v", firstDup)
	}
	if got := seen[ToolCallSignature{Name: preparePipelineWriteContextTool, Arguments: `{"path":"main.go"}`}]; got != 1 {
		t.Fatalf("seen count = %d, want 1 after authorized retry", got)
	}
}

func TestDetectToolCallDuplicate_RejectsPrepareRetryWithoutFreshStaleBasisError(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: preparePipelineWriteContextTool, Arguments: `{"path":"main.go"}`},
	}
	seen := make(map[ToolCallSignature]int)
	DetectToolCallDuplicate(calls, seen)

	history := []providers.Message{
		{
			Role:      providers.RoleAssistant,
			Content:   "Prepared already.",
			ToolCalls: calls,
		},
		{
			Role:     providers.RoleTool,
			ToolName: preparePipelineWriteContextTool,
			Content:  `{"path":"main.go"}`,
		},
	}

	allDup, _ := DetectToolCallDuplicate(calls, seen, history)
	if allDup {
		t.Fatal("expected second identical prepare batch to be tolerated")
	}

	allDup, firstDup := DetectToolCallDuplicate(calls, seen, history)
	if !allDup {
		t.Fatal("expected third identical prepare batch to be rejected without stale-basis guidance")
	}
	if firstDup.Name != preparePipelineWriteContextTool {
		t.Fatalf("firstDup.Name = %q, want %q", firstDup.Name, preparePipelineWriteContextTool)
	}
}

func TestDetectToolCallDuplicate_AllowsWriteRetryAfterInterveningPrerequisiteTool(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: "write_test", Arguments: `{"output_file":"tests/test_greeter.py","content":"def test_greet():\\n    assert greet() == 'Hello, World!'\\n"}`},
	}
	seen := make(map[ToolCallSignature]int)
	DetectToolCallDuplicate(calls, seen)

	history := []providers.Message{
		{
			Role: providers.RoleAssistant,
			ToolCalls: []providers.ToolCall{
				{ID: "2", Name: "analyze_risk", Arguments: `{"files":["src/hello/greeter.py"]}`},
			},
		},
		{
			Role:     providers.RoleTool,
			ToolName: "analyze_risk",
			Content:  `{"risk_areas":[{"target":"src/hello/greeter.py","kind":"default-argument"}]}`,
		},
	}

	allDup, firstDup := DetectToolCallDuplicate(calls, seen, history)
	if allDup {
		t.Fatal("expected write_test retry after analyze_risk to be allowed")
	}
	if firstDup.Name != "" {
		t.Fatalf("expected no duplicate signature, got %+v", firstDup)
	}
}

func TestDetectToolCallDuplicate_AllowsPrepareRetryAfterWriteSurfaceMutation(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: preparePipelineWriteContextTool, Arguments: `{"path":"hello_cli/__init__.py"}`},
	}
	seen := make(map[ToolCallSignature]int)
	DetectToolCallDuplicate(calls, seen)

	history := []providers.Message{
		{
			Role: providers.RoleAssistant,
			ToolCalls: []providers.ToolCall{
				{ID: "mkdir-1", Name: "create_pipeline_directory", Arguments: `{"path":"hello_cli"}`},
			},
		},
		{
			Role:     providers.RoleTool,
			ToolName: "create_pipeline_directory",
			Content:  `{"path":"hello_cli","created":true}`,
		},
	}

	allDup, firstDup := DetectToolCallDuplicate(calls, seen, history)
	if allDup {
		t.Fatal("expected prepare retry after directory mutation to bypass duplicate rejection")
	}
	if firstDup.Name != "" {
		t.Fatalf("expected no duplicate signature, got %+v", firstDup)
	}
	if got := seen[ToolCallSignature{Name: preparePipelineWriteContextTool, Arguments: `{"path":"hello_cli/__init__.py"}`}]; got != 1 {
		t.Fatalf("seen count = %d, want 1 after authorized refresh", got)
	}
}

// --- UpdateToolErrors ---

func TestUpdateToolErrors_AllErrors(t *testing.T) {
	current := 0
	totalCalls := 3
	errCount := totalCalls
	got := UpdateToolErrors(current, errCount, totalCalls)
	if got != current+1 {
		t.Fatalf("expected %d, got %d", current+1, got)
	}
}

func TestUpdateToolErrors_PartialSuccess(t *testing.T) {
	current := 5 // had a streak
	totalCalls := 3
	errCount := totalCalls - 1 // one succeeded
	got := UpdateToolErrors(current, errCount, totalCalls)
	if got != 0 {
		t.Fatalf("expected reset to 0, got %d", got)
	}
}

func TestUpdateToolErrors_ZeroTotalCalls(t *testing.T) {
	got := UpdateToolErrors(1, 0, 0)
	if got != 0 {
		t.Fatalf("expected 0 for zero total calls, got %d", got)
	}
}

// --- MaxConsecutiveToolErrors constant ---

func TestMaxConsecutiveToolErrors_Value(t *testing.T) {
	// The constant must equal 2 (two full all-error turns).
	const expected = 2
	if MaxConsecutiveToolErrors != expected {
		t.Fatalf("MaxConsecutiveToolErrors = %d, want %d", MaxConsecutiveToolErrors, expected)
	}
}

// Verify the constant is usable as an int comparison target.
func TestMaxConsecutiveToolErrors_UsableAsThreshold(t *testing.T) {
	counter := 0
	totalCalls := 1

	for range MaxConsecutiveToolErrors {
		counter = UpdateToolErrors(counter, totalCalls, totalCalls)
	}
	if counter < MaxConsecutiveToolErrors {
		t.Fatalf("counter should reach threshold after %d all-error turns, got %d",
			MaxConsecutiveToolErrors, counter)
	}
}

// Ensure MarshalToolOutput does not silently swallow marshal errors.
func TestMarshalToolOutput_UnmarshalableReturnsError(t *testing.T) {
	_, err := MarshalToolOutput(make(chan int))
	if err == nil {
		t.Fatal("expected error for unmarshalable channel type")
	}
}

// Verify ToolErrorPayload trims whitespace from error messages.
func TestToolErrorPayload_TrimsWhitespace(t *testing.T) {
	testErr := fmt.Errorf("  padded error  ")
	got := ToolErrorPayload(testErr)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	errMsg := parsed["error"].(string)
	if errMsg != "padded error" {
		t.Fatalf("expected trimmed error, got %q", errMsg)
	}
}
