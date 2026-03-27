package archivalist

import (
	"context"
	"encoding/json"
	"testing"
)

func TestArchivalistBuildToolDefinitions_UsesLegacyFacade(t *testing.T) {
	a := newTestArchivalist(t)

	tools := a.buildToolDefinitions()
	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		names[tool.Name] = struct{}{}
	}

	for _, expected := range archivalistVisibleSkillNames() {
		if _, ok := names[expected]; !ok {
			t.Fatalf("expected legacy public tool %q to be active", expected)
		}
	}

	for _, hidden := range []string{"store", "query", "briefing"} {
		if _, ok := names[hidden]; ok {
			t.Fatalf("generic archivalist alias %q should not be in the default public surface", hidden)
		}
	}
}

func TestArchivalistLegacyFacade_RecordFailureAndQueryFailures(t *testing.T) {
	a := newTestArchivalist(t)

	output, err := a.HandleToolCall(context.Background(), ToolRecordFailure, []byte(`{
		"error":"ModuleNotFoundError",
		"context":"tester install bootstrap",
		"approach":"installed the wrong package",
		"resolution":"install the project dependency set",
		"outcome":"success"
	}`))
	if err != nil {
		t.Fatalf("record failure returned error: %v", err)
	}

	result := decodeLegacyToolResult(t, output)
	if got := result["status"]; got != "recorded" {
		t.Fatalf("record failure status = %v, want recorded", got)
	}

	output, err = a.HandleToolCall(context.Background(), ToolQueryFailures, []byte(`{"error_type":"ModuleNotFound","limit":5}`))
	if err != nil {
		t.Fatalf("query failures returned error: %v", err)
	}

	queryResult := decodeLegacyToolResult(t, output)
	failures, ok := queryResult["failures"].([]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("query failures returned %v, want one failure", queryResult["failures"])
	}
}

func TestArchivalistLegacyFacade_IntentLifecycleSurfacesInBriefingAndConflicts(t *testing.T) {
	a := newTestArchivalist(t)

	declareOutput, err := a.HandleToolCall(context.Background(), ToolDeclareIntent, []byte(`{
		"type":"refactor",
		"description":"Rename User to Account",
		"affected_paths":["src/models/","src/views/"],
		"affected_apis":["User","get_user"],
		"priority":"high"
	}`))
	if err != nil {
		t.Fatalf("declare intent returned error: %v", err)
	}

	declareResult := decodeLegacyToolResult(t, declareOutput)
	if got := declareResult["status"]; got != "declared" {
		t.Fatalf("declare intent status = %v, want declared", got)
	}

	intent, ok := declareResult["intent"].(map[string]any)
	if !ok {
		t.Fatalf("declare intent payload missing intent object: %v", declareResult)
	}
	intentID, _ := intent["id"].(string)
	if intentID == "" {
		t.Fatalf("declare intent did not return an id: %v", intent)
	}

	briefingOutput, err := a.HandleToolCall(context.Background(), ToolGetBriefing, []byte(`{"tier":"standard"}`))
	if err != nil {
		t.Fatalf("get briefing returned error: %v", err)
	}
	briefing := decodeLegacyToolResult(t, briefingOutput)
	declared, ok := briefing["declared_intents"].([]any)
	if !ok || len(declared) != 1 {
		t.Fatalf("briefing declared_intents = %v, want one active intent", briefing["declared_intents"])
	}

	conflictOutput, err := a.HandleToolCall(context.Background(), ToolDeclareIntent, []byte(`{
		"type":"breaking_change",
		"description":"Split User into Profile and Account",
		"affected_paths":["src/models/"],
		"priority":"critical"
	}`))
	if err != nil {
		t.Fatalf("conflicting declare intent returned error: %v", err)
	}
	conflictResult := decodeLegacyToolResult(t, conflictOutput)
	if got := conflictResult["status"]; got != "intent_conflict" {
		t.Fatalf("conflicting declare status = %v, want intent_conflict", got)
	}

	conflictsOutput, err := a.HandleToolCall(context.Background(), ToolGetConflicts, []byte(`{
		"paths":["src/models/"],
		"check_intents":true
	}`))
	if err != nil {
		t.Fatalf("get conflicts returned error: %v", err)
	}
	conflictsResult := decodeLegacyToolResult(t, conflictsOutput)
	if got := int(conflictsResult["count"].(float64)); got == 0 {
		t.Fatalf("conflicts count = %d, want at least one unresolved conflict", got)
	}
	activeIntents, ok := conflictsResult["active_intents"].([]any)
	if !ok || len(activeIntents) != 1 {
		t.Fatalf("active_intents = %v, want one active overlapping intent", conflictsResult["active_intents"])
	}

	completeOutput, err := a.HandleToolCall(context.Background(), ToolCompleteIntent, []byte(`{
		"intent_id":"`+intentID+`",
		"success":true,
		"files_changed":["src/models/account.go"]
	}`))
	if err != nil {
		t.Fatalf("complete intent returned error: %v", err)
	}
	completeResult := decodeLegacyToolResult(t, completeOutput)
	if got := completeResult["status"]; got != "completed" {
		t.Fatalf("complete intent status = %v, want completed", got)
	}

	briefingOutput, err = a.HandleToolCall(context.Background(), ToolGetBriefing, []byte(`{"tier":"standard"}`))
	if err != nil {
		t.Fatalf("get briefing after completion returned error: %v", err)
	}
	briefing = decodeLegacyToolResult(t, briefingOutput)
	if raw, exists := briefing["declared_intents"]; exists {
		declared, ok = raw.([]any)
		if !ok || len(declared) != 0 {
			t.Fatalf("briefing declared_intents after completion = %v, want none", briefing["declared_intents"])
		}
	}
}

func decodeLegacyToolResult(t *testing.T, output string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode tool output %q: %v", output, err)
	}
	return result
}
