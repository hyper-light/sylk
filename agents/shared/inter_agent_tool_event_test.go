package shared

import "testing"

func TestDeriveInterAgentToolEvent_ConsultPeerResolvesTargetFromTargetAgentType(t *testing.T) {
	// consult_peer names its addressee via `target_agent_type`. Prior to
	// this classifier fix the derivation returned nil (the switch fell
	// through firstKnownAgentInName("peer") with no match), the tool call
	// carried no InterAgent metadata, and the UI rendered it as a plain
	// tool-call row without nested children — visible in the chat panel
	// as a 1–2 ms dispatch with no stitching path.
	start := DeriveInterAgentToolEvent(
		"consult_peer",
		`{"target_agent_type":"librarian","query":"Any prior art for quota enforcement in this codebase?"}`,
		"",
		ToolCallStart,
		false,
		"",
	)
	if start == nil {
		t.Fatal("expected consult_peer start metadata")
	}
	if start.Kind != InterAgentToolEventKindConsult {
		t.Fatalf("consult_peer start kind = %q", start.Kind)
	}
	if len(start.AgentTypes) != 1 || start.AgentTypes[0] != "librarian" {
		t.Fatalf("consult_peer start targets = %#v", start.AgentTypes)
	}
	if start.Status != InterAgentToolEventStatusPending {
		t.Fatalf("consult_peer start status = %q", start.Status)
	}
	if start.Summary != "Any prior art for quota enforcement in this codebase?" {
		t.Fatalf("consult_peer start summary = %q", start.Summary)
	}

	done := DeriveInterAgentToolEvent(
		"consult_peer",
		`{"target_agent_type":"librarian","query":"Any prior art?"}`,
		`{"consult_id":"act-123","status":"completed","response":{"user_message":"Found two related modules."}}`,
		ToolCallComplete,
		true,
		"",
	)
	if done == nil {
		t.Fatal("expected consult_peer completion metadata")
	}
	if len(done.AgentTypes) != 1 || done.AgentTypes[0] != "librarian" {
		t.Fatalf("consult_peer done targets = %#v", done.AgentTypes)
	}
	if done.Status != InterAgentToolEventStatusDone {
		t.Fatalf("consult_peer done status = %q", done.Status)
	}
}

func TestDeriveInterAgentToolEvent_ConsultPeerCrossPipelineTargetAgentType(t *testing.T) {
	// target_pipeline_id is an addressing parameter, not an agent type;
	// AgentTypes should still resolve to target_agent_type alone.
	meta := DeriveInterAgentToolEvent(
		"consult_peer",
		`{"target_agent_type":"engineer","target_pipeline_id":"pipe-42","query":"How are you handling retry semantics?"}`,
		"",
		ToolCallStart,
		false,
		"",
	)
	if meta == nil {
		t.Fatal("expected consult_peer cross-pipeline metadata")
	}
	if len(meta.AgentTypes) != 1 || meta.AgentTypes[0] != "engineer" {
		t.Fatalf("cross-pipeline targets = %#v", meta.AgentTypes)
	}
}

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
	// Producers (global_review_protocol.go) now stamp protocol_scope in both
	// challenge-dispatch outputs and validate_global_review outputs. The UI
	// reads ThreadKey directly from that explicit scope — no tool-name
	// guessing. Tests that simulate producer emission must include the
	// scope the same way real producers do.
	challenge := DeriveInterAgentToolEvent(
		"challenge_architect",
		`{"reason":"Need plan clarification","request":"Reassess the testing scope."}`,
		`{"selected":true,"target_agent":"architect","challenge_id":"global-review-123","protocol_scope":"global_review"}`,
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
		`{"challenge_id":"global-review-123","requesting_agent":"inspector","status":"passed","summary":"Revise the plan to strengthen integration coverage.","protocol_scope":"global_review"}`,
		`{"validated":true,"challenge_id":"global-review-123","requesting_agent":"inspector","responding_agent":"architect","status":"passed","protocol_scope":"global_review"}`,
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

// TestNormalizeInterAgentToolEventForEmit_PipelineChallengeRejectsOrchestratorTarget
// reproduces the "challenges escape the pipeline" hang: a pipeline-inspector
// challenge_agent call with target_agents=["orchestrator"] must NOT produce a
// publishable inter-agent event, because the pipeline protocol rejects such
// targets at the handler boundary. Without this filter the UI commits a
// Challenge-kind row before the handler runs; the Challenge-kind status
// lifecycle then keeps it rendering as pending forever.
func TestNormalizeInterAgentToolEventForEmit_PipelineChallengeRejectsOrchestratorTarget(t *testing.T) {
	meta := NormalizeInterAgentToolEventForEmit(
		"challenge_agent",
		`{"target_agents":["orchestrator"],"request":"Escalate workflow."}`,
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
	if meta != nil {
		t.Fatalf("expected nil inter-agent metadata for invalid pipeline target, got %#v", meta)
	}
}

// TestNormalizeInterAgentToolEventForEmit_PipelineChallengeFiltersInvalidAmongValid
// ensures that a mixed target list (e.g. LLM asks for both orchestrator and
// tester) drops orchestrator without emitting an "orchestrator" row — only
// valid pipeline peers remain in AgentTypes.
func TestNormalizeInterAgentToolEventForEmit_PipelineChallengeFiltersInvalidAmongValid(t *testing.T) {
	meta := NormalizeInterAgentToolEventForEmit(
		"challenge_agent",
		`{"target_agents":["orchestrator","tester"],"request":"Audit plus escalate."}`,
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
		t.Fatal("expected inter-agent metadata for the valid subset of targets")
	}
	if got := meta.AgentTypes; len(got) != 1 || got[0] != "tester-pipeline" {
		t.Fatalf("agent types = %#v, want [tester-pipeline] (orchestrator filtered)", got)
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
