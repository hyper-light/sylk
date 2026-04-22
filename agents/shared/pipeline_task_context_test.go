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

func TestBuildPipelineSystemContext_IncludesPipelineProtocol(t *testing.T) {
	task := &PipelineTaskInput{
		TaskID:    "task-2",
		AgentType: "engineer",
		Context: map[string]any{
			"pipeline_protocol": map[string]any{
				"requested_by":    "inspector-pipeline",
				"mode":            "single",
				"current_request": "Implement the change against the authored tests.",
				"active_agents":   []any{"engineer"},
				"audit_lock": map[string]any{
					"owner_agent": "inspector-pipeline",
					"phase":       "finalize_pipeline",
					"reason":      "Inspector is conducting terminal audit review.",
				},
				"roster": []any{
					map[string]any{"agent_type": "inspector-pipeline", "role": "entrypoint"},
					map[string]any{"agent_type": "tester-pipeline", "role": "testing"},
					map[string]any{"agent_type": "engineer", "role": "implementation"},
				},
				"pending_challenge": map[string]any{
					"id":               "challenge-1",
					"requesting_agent": "inspector-pipeline",
					"target_agents":    []any{"engineer"},
					"request":          "Implement the requested behavior.",
				},
				"pending_validation": map[string]any{
					"challenge_id":     "challenge-0",
					"requesting_agent": "inspector-pipeline",
					"responding_agent": "tester-pipeline",
					"status":           "passed",
					"summary":          "The tests are in place.",
				},
				"recent_events": []any{
					map[string]any{"type": "handoff", "agent_type": "inspector-pipeline", "summary": "Routed to tester"},
				},
			},
		},
	}

	contextText := BuildPipelineSystemContext(task)
	for _, want := range []string{
		"## Pipeline Protocol",
		"Pipeline Members",
		"Active Agents",
		"Audit Lock",
		"Pending Challenge",
		"Pending Validation",
		"Inspector is the only pipeline entrypoint and the ultimate pipeline exit point",
		"The normal top-level flow is Inspector -> Tester for initial tests -> Engineer/Designer for implementation -> Inspector for audit",
		"A first challenge on a directed pipeline-agent pair is allowed.",
		"Repeated challenges require new pipeline VFS evidence on the directed pair",
		"Use handoff_next for ordinary top-level phase progression. Use validate_work only when you are answering an active challenge.",
		"After Tester hands back the initial tests, Inspector audits those tests and either challenges Tester or hands implementation to Engineer/Designer.",
		"Engineer and Designer should hand completed implementation turns back to Inspector",
		"After Engineer or Designer hand work back, Inspector audits the implementation first.",
		"Use finalize_pipeline only after the current inspector audit is complete",
		"Inspector must immediately invoke handoff_to_green instead of starting another audit loop",
		"Challenges to Inspector are refused while this lock is active.",
		"stop work until Inspector challenges you directly or your preceding agent hands off to you",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context missing %q\n%s", want, contextText)
		}
	}
}

func TestComposePipelineTaskUserPrompt_PrioritizesCurrentTurnRequest(t *testing.T) {
	task := &PipelineTaskInput{
		TaskID:    "task-cli",
		AgentType: "tester-pipeline",
		Prompt:    "Implement the CLI module and package entrypoint.",
		Context: map[string]any{
			"pipeline_stage": "test",
			"pipeline_protocol": map[string]any{
				"current_request": "Author the red-phase tests for the CLI contract.",
				"requested_by":    "inspector-pipeline",
			},
		},
	}

	prompt := ComposePipelineTaskUserPrompt(task)
	if !strings.Contains(prompt, "### Current Turn Request\nAuthor the red-phase tests for the CLI contract.") {
		t.Fatalf("prompt missing current turn request:\n%s", prompt)
	}
	if !strings.Contains(prompt, "### Original Task Objective\nImplement the CLI module and package entrypoint.") {
		t.Fatalf("prompt missing original task objective:\n%s", prompt)
	}
}
