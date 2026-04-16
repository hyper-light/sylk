package shared

import "testing"

func TestDeriveInterAgentToolEvent_ConsultSingleAndPrePlanning(t *testing.T) {
	start := DeriveInterAgentToolEvent(
		"consult",
		`{"mode":"single","target":"academic","query":"Is there a cleaner approach?"}`,
		"",
		ToolCallStart,
		false,
		"",
	)
	if start == nil {
		t.Fatal("expected consult start metadata")
	}
	if got := start.Kind; got != InterAgentToolEventKindConsult {
		t.Fatalf("consult start kind = %q", got)
	}
	if len(start.AgentTypes) != 1 || start.AgentTypes[0] != "academic" {
		t.Fatalf("consult start targets = %#v", start.AgentTypes)
	}
	if got := start.Status; got != InterAgentToolEventStatusPending {
		t.Fatalf("consult start status = %q", got)
	}

	prePlanning := DeriveInterAgentToolEvent(
		"consult",
		`{"mode":"pre_planning","query":"Build the plan","include_academic":true}`,
		`{"ready":true,"consultations":{"librarian":{},"archivalist":{},"academic":{}}}`,
		ToolCallComplete,
		true,
		"",
	)
	if prePlanning == nil {
		t.Fatal("expected pre-planning consult metadata")
	}
	if got := prePlanning.AgentTypes; len(got) != 3 || got[0] != "librarian" || got[1] != "archivalist" || got[2] != "academic" {
		t.Fatalf("pre-planning targets = %#v", got)
	}
	if got := prePlanning.Summary; got != "consultation gate satisfied" {
		t.Fatalf("pre-planning summary = %q", got)
	}
	if got := prePlanning.Status; got != InterAgentToolEventStatusDone {
		t.Fatalf("pre-planning status = %q", got)
	}
}

func TestDeriveInterAgentToolEvent_RequestArchitectResearchUsesArchitectResponse(t *testing.T) {
	meta := DeriveInterAgentToolEvent(
		"request_architect_research",
		`{"description":"Assess whether the current testing scope is enough."}`,
		`{"requested":true,"target":"architect","description":"Assess whether the current testing scope is enough.","response":"The plan should add integration coverage before final sign-off."}`,
		ToolCallComplete,
		true,
		"",
	)
	if meta == nil {
		t.Fatal("expected architect research metadata")
	}
	if got := meta.Kind; got != InterAgentToolEventKindConsult {
		t.Fatalf("architect research kind = %q", got)
	}
	if len(meta.AgentTypes) != 1 || meta.AgentTypes[0] != "architect" {
		t.Fatalf("architect research targets = %#v", meta.AgentTypes)
	}
	if got := meta.Summary; got != "The plan should add integration coverage before final sign-off." {
		t.Fatalf("architect research summary = %q", got)
	}
}

func TestDeriveInterAgentToolEvent_ValidateApproachRoutesThroughLibrarian(t *testing.T) {
	meta := DeriveInterAgentToolEvent(
		"validate_approach",
		`{"approach":"Add a new error-handling helper."}`,
		`{"valid":true,"reason":"Approach validated via Librarian consultation"}`,
		ToolCallComplete,
		true,
		"",
	)
	if meta == nil {
		t.Fatal("expected validate_approach metadata")
	}
	if got := meta.Kind; got != InterAgentToolEventKindConsult {
		t.Fatalf("validate_approach kind = %q", got)
	}
	if len(meta.AgentTypes) != 1 || meta.AgentTypes[0] != "librarian" {
		t.Fatalf("validate_approach targets = %#v", meta.AgentTypes)
	}
	if got := meta.Summary; got != "Approach validated via Librarian consultation" {
		t.Fatalf("validate_approach summary = %q", got)
	}
}

func TestDeriveInterAgentToolEvent_GuardianConsultPrefersHumanizedNestedPayload(t *testing.T) {
	meta := DeriveInterAgentToolEvent(
		"consult_guardian",
		`{"query":"Check whether this task is safe to run."}`,
		`{"target":"guardian","data":{"user_message":"Safe to proceed, but approval is still required for the deploy step.","reason":"guardian-approved deterministic control-plane grant"}}`,
		ToolCallComplete,
		true,
		"",
	)
	if meta == nil {
		t.Fatal("expected guardian consult metadata")
	}
	if got := meta.Summary; got != "Safe to proceed, but approval is still required for the deploy step." {
		t.Fatalf("guardian consult summary = %q", got)
	}
}

func TestDeriveInterAgentToolEvent_GlobalReviewChallengeFlow(t *testing.T) {
	challenge := DeriveInterAgentToolEvent(
		"challenge_architect",
		`{"reason":"Need plan clarification","request":"Reassess the testing scope."}`,
		`{"selected":true,"target_agent":"architect","challenge_id":"global-review-123"}`,
		ToolCallComplete,
		true,
		"",
	)
	if challenge == nil {
		t.Fatal("expected challenge metadata")
	}
	if got := challenge.ThreadKey; got != "global_review:global-review-123" {
		t.Fatalf("challenge thread key = %q", got)
	}
	if got := challenge.Status; got != InterAgentToolEventStatusPending {
		t.Fatalf("challenge status = %q", got)
	}

	response := DeriveInterAgentToolEvent(
		"validate_global_review",
		`{"challenge_id":"global-review-123","requesting_agent":"inspector","status":"passed","summary":"Revise the plan to strengthen integration coverage."}`,
		`{"validated":true,"challenge_id":"global-review-123","requesting_agent":"inspector","responding_agent":"architect","status":"passed"}`,
		ToolCallComplete,
		true,
		"",
	)
	if response == nil || !response.UpdateOrigin {
		t.Fatal("expected origin-updating response metadata")
	}
	if len(response.AgentTypes) != 1 || response.AgentTypes[0] != "architect" {
		t.Fatalf("response agent types = %#v", response.AgentTypes)
	}
	if got := response.Status; got != InterAgentToolEventStatusDone {
		t.Fatalf("response status = %q", got)
	}
}

func TestDeriveInterAgentToolEvent_PipelineValidationFlow(t *testing.T) {
	challenge := DeriveInterAgentToolEvent(
		"challenge_agent",
		`{"target_agents":["tester-pipeline"],"request":"Audit the pipeline results."}`,
		`{"selected":true,"target_agents":["tester-pipeline"],"challenge_id":"pipeline-123"}`,
		ToolCallComplete,
		true,
		"",
	)
	if challenge == nil {
		t.Fatal("expected pipeline challenge metadata")
	}
	if got := challenge.ThreadKey; got != "pipeline:pipeline-123" {
		t.Fatalf("pipeline challenge thread key = %q", got)
	}

	response := DeriveInterAgentToolEvent(
		"validate_work",
		`{"challenge_id":"pipeline-123","requesting_agent":"inspector-pipeline","status":"passed","summary":"Validation passed with updated evidence."}`,
		`{"validated":true,"challenge_id":"pipeline-123","requesting_agent":"inspector-pipeline","responding_agent":"tester-pipeline","status":"passed"}`,
		ToolCallComplete,
		true,
		"",
	)
	if response == nil || !response.UpdateOrigin {
		t.Fatal("expected pipeline response metadata")
	}
	if len(response.AgentTypes) != 1 || response.AgentTypes[0] != "tester-pipeline" {
		t.Fatalf("pipeline response targets = %#v", response.AgentTypes)
	}

	processed := DeriveInterAgentToolEvent(
		"process_validation",
		`{"challenge_id":"pipeline-123","decision":"accept","summary":"Move toward OT handoff."}`,
		`{"processed":true,"challenge_id":"pipeline-123","decision":"accept"}`,
		ToolCallComplete,
		true,
		"",
	)
	if processed == nil || !processed.UpdateOrigin {
		t.Fatal("expected pipeline process metadata")
	}
	if got := processed.Status; got != InterAgentToolEventStatusDone {
		t.Fatalf("pipeline process status = %q", got)
	}
}

func TestNormalizeInterAgentToolEventForEmit_PipelineChallengeCanonicalizesTesterLabel(t *testing.T) {
	meta := NormalizeInterAgentToolEventForEmit(
		"challenge_agent",
		`{"target_agents":["tester"],"request":"Audit the pipeline results."}`,
		"",
		ToolCallStart,
		false,
		"",
		nil,
		map[string]any{
			"agent_type":  "inspector-pipeline",
			"pipeline_id": "task_1",
		},
	)
	if meta == nil {
		t.Fatal("expected normalized inter-agent metadata")
	}
	if got := meta.AgentTypes; len(got) != 1 || got[0] != "tester-pipeline" {
		t.Fatalf("agent types = %#v, want [tester-pipeline]", got)
	}
}

func TestCanonicalizeInterAgentToolName_GlobalReviewChallengeTargetsTester(t *testing.T) {
	got := canonicalizeInterAgentToolName(
		"challenge_agent",
		`{"target_agents":["tester"],"request":"Audit the merged state.","protocol_scope":"global_review"}`,
		"",
		map[string]any{"agent_type": "inspector"},
	)
	if got != "challenge_global_tester" {
		t.Fatalf("tool name = %q, want %q", got, "challenge_global_tester")
	}
}

func TestDeriveInterAgentToolEvent_GenericGlobalReviewChallengeUsesGlobalThreadKey(t *testing.T) {
	challenge := DeriveInterAgentToolEvent(
		"challenge_agent",
		`{"target_agents":["tester"],"request":"Audit the merged state.","protocol_scope":"global_review","thread_key":"global_review:global-review-123"}`,
		`{"selected":true,"target_agents":["tester"],"challenge_id":"global-review-123","protocol_scope":"global_review","thread_key":"global_review:global-review-123"}`,
		ToolCallComplete,
		true,
		"",
	)
	if challenge == nil {
		t.Fatal("expected global-review challenge metadata")
	}
	if got := challenge.ThreadKey; got != "global_review:global-review-123" {
		t.Fatalf("challenge thread key = %q, want %q", got, "global_review:global-review-123")
	}
}
