package shared

import (
	"strings"
	"testing"
)

func TestBuildPipelineSystemContext_IncludesCoordinationPacket(t *testing.T) {
	task := &PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "engineer",
		Context: map[string]any{
			"coordination_packet": map[string]any{
				"summary": "2 relevant artifact(s) already exist.",
				"contract": map[string]any{
					"summary":                  "Claim the concrete test surface before duplicating work.",
					"minimum_claims":           1.0,
					"minimum_artifacts":        1.0,
					"preferred_artifact_kinds": []any{"verification_result"},
				},
				"my_claims": []any{
					map[string]any{"scope_kind": "file", "scope_key": "pkg/auth/middleware.go", "purpose": "implement auth guard"},
				},
				"peer_claims": []any{
					map[string]any{"scope_kind": "test_surface", "scope_key": "auth-failure-path", "owner_type": "tester-pipeline"},
				},
				"relevant_artifacts": []any{
					map[string]any{"kind": "risk_map", "producer_type": "inspector-pipeline", "summary": "guard unauthenticated writes"},
				},
				"pending_reviews": []any{
					map[string]any{"artifact_id": "art-1", "summary": "confirm implementation coverage"},
				},
				"historical_precedents": []any{
					map[string]any{"category": "decision", "summary": "Past auth tasks converged faster when engineer reused tester repros.", "session_id": "sess-9"},
				},
			},
		},
	}

	contextText := BuildPipelineSystemContext(task)
	for _, want := range []string{
		"## Coordination State",
		"Required Coordination Protocol",
		"Active Claims You Already Hold",
		"Relevant Existing Artifacts",
		"Pending Review Requests",
		"Historical Coordination Precedents",
		"Query and reuse the current coordination state before rediscovering work.",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context missing %q\n%s", want, contextText)
		}
	}
}
