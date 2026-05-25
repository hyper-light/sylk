package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

func TestCompleteYieldedToolFromContinuationRecordsPairedCompletion(t *testing.T) {
	startedAt := time.Now().Add(-2 * time.Second).UTC()
	started := &claims.Artifact{
		ID:      "started-1",
		AgentID: "architect",
		Kind:    "tool_started",
		Metadata: map[string]any{
			"call_id":    "call-1",
			"tool_name":  "consult_peer",
			"started_at": startedAt.Format(time.RFC3339Nano),
		},
		Created: startedAt,
	}
	acc := claims.RestoreAccumulator(
		"architect",
		"sess-1",
		"claim-1",
		startedAt.Add(-time.Second),
		[]*claims.Artifact{started},
		nil,
	)
	snapshot := &TurnSnapshot{
		AwaitToolCallID: "call-1",
		AwaitToolName:   "consult_peer",
		AccumulatorState: AccumulatorSnapshot{
			AgentID:   "architect",
			SessionID: "sess-1",
			ClaimID:   "claim-1",
			Started:   startedAt.Add(-time.Second),
			Artifacts: []*claims.Artifact{started},
		},
		Request: &providers.Request{Messages: []providers.Message{{
			Role: providers.RoleAssistant,
			ToolCalls: []providers.ToolCall{{
				ID:        "call-1",
				Name:      "consult_peer",
				Arguments: `{"target_agent_type":"librarian","query":"what exists?"}`,
			}},
		}}},
	}
	results := map[string]*claims.ConsultResolvedDelta{
		"consult-1": {
			ConsultID:        "consult-1",
			Status:           claims.ConsultStatusCompleted,
			ResponseSummary:  "Found existing project structure.",
			ResponderAgentID: "librarian",
		},
	}

	var events []ToolCallEvent
	ctx := claims.WithTestamentAccumulator(context.Background(), acc)
	ctx = WithToolCallEmitter(ctx, func(event ToolCallEvent) error {
		events = append(events, event)
		return nil
	})

	output := `{"results":{"consult-1":{"status":"completed","summary":"Found existing project structure."}}}`
	if !CompleteYieldedToolFromContinuation(ctx, snapshot, results, output) {
		t.Fatal("expected yielded tool completion to be recorded")
	}
	if CompleteYieldedToolFromContinuation(ctx, snapshot, results, output) {
		t.Fatal("expected duplicate yielded tool completion to be ignored")
	}

	completions := yieldedCompletionArtifacts(acc.Artifacts())
	if len(completions) != 1 {
		t.Fatalf("completion artifacts = %d, want 1", len(completions))
	}
	completed := completions[0]
	if got := claims.CompletesArtifactID(completed.Relations); got != "started-1" {
		t.Fatalf("completion relation = %q, want started-1", got)
	}
	if got := yieldedToolMetadataString(completed.Metadata, "outcome"); got != "success" {
		t.Fatalf("outcome = %q, want success", got)
	}
	if got := yieldedToolMetadataString(completed.Metadata, "call_id"); got != "call-1" {
		t.Fatalf("call_id = %q, want call-1", got)
	}
	if got := yieldedToolMetadataString(completed.Metadata, "tool_name"); got != "consult_peer" {
		t.Fatalf("tool_name = %q, want consult_peer", got)
	}

	if len(events) != 1 {
		t.Fatalf("tool events = %d, want 1", len(events))
	}
	event := events[0]
	if event.ToolCallKey != "call-1" {
		t.Fatalf("event.ToolCallKey = %q, want call-1", event.ToolCallKey)
	}
	if event.Phase != ToolCallComplete {
		t.Fatalf("event.Phase = %d, want ToolCallComplete", event.Phase)
	}
	if !event.Success {
		t.Fatalf("event.Success = false, want true: error=%q", event.ErrorMsg)
	}
	if event.InterAgent == nil {
		t.Fatal("event.InterAgent = nil, want consult completion metadata")
	}
	if event.InterAgent.Status != InterAgentToolEventStatusDone {
		t.Fatalf("event.InterAgent.Status = %q, want done", event.InterAgent.Status)
	}
}

func yieldedCompletionArtifacts(artifacts []*claims.Artifact) []*claims.Artifact {
	var out []*claims.Artifact
	for _, art := range artifacts {
		if art != nil && art.Kind == "tool_completed" {
			out = append(out, art)
		}
	}
	return out
}
