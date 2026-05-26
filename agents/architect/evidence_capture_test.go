package architect

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

func architectEvidenceTestContext(sessionID, corrID string) context.Context {
	ctx := withArchitectSessionID(context.Background(), sessionID)
	ctx = withArchitectStreamContext(ctx, corrID, "user")
	return shared.WithLogMeta(ctx, shared.LogMeta{CorrID: corrID, SessionID: sessionID, AgentID: "architect"})
}

func TestConsultPeerEvidenceBeforePlanStartDrainsIntoCreatedPlan(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "sess-evidence", AllowPlanningWithoutConsultation: true})
	ctx := architectEvidenceTestContext("sess-evidence", "corr-evidence")

	err := a.captureConsultPeerEvidence(ctx, &skills.ToolCallHookData{
		ToolName: "consult_peer",
		Input: map[string]any{
			"target_agent_type": "librarian",
			"query":             "Find Python CLI patterns.",
			"scope":             ".",
		},
		Output: map[string]any{
			"consult_id": "consult-before-start",
			"status":     "completed",
			"response": map[string]any{
				"summary": "Use cli.py and keep argparse parsing isolated.",
			},
		},
	})
	if err != nil {
		t.Fatalf("captureConsultPeerEvidence returned error: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"action": "start",
		"query":  "Create a toy Python hello world CLI application.",
	})
	result := a.InvokeSkill(ctx, "plan", payload)
	if result == nil || !result.Success {
		t.Fatalf("plan start failed: %+v", result)
	}
	data := result.Data.(map[string]any)
	plan := a.planStore.Get(data["plan_id"].(string))
	if plan == nil {
		t.Fatal("expected created plan")
	}
	if len(plan.EvidenceTrail) != 1 {
		t.Fatalf("EvidenceTrail len = %d, want 1", len(plan.EvidenceTrail))
	}
	if plan.Consultations["librarian"] == nil {
		t.Fatal("expected librarian consultation index")
	}
	if plan.CodebasePatterns == nil || len(plan.CodebasePatterns.Patterns) == 0 {
		t.Fatalf("expected normalized librarian patterns, got %+v", plan.CodebasePatterns)
	}
}

func TestConsultPeerEvidenceBeforePlanStartDrainsAcrossUserConfirmationCorrelation(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "sess-evidence-confirm", AllowPlanningWithoutConsultation: true})
	consultCtx := architectEvidenceTestContext("sess-evidence-confirm", "corr-consult")

	err := a.captureConsultPeerEvidence(consultCtx, &skills.ToolCallHookData{
		ToolName: "consult_peer",
		Input: map[string]any{
			"target_agent_type": "librarian",
			"query":             "Find Python CLI patterns.",
			"scope":             ".",
		},
		Output: map[string]any{
			"consult_id": "consult-before-confirmation",
			"status":     "completed",
			"response": map[string]any{
				"summary": "No existing Python package; create a minimal pyproject.toml.",
			},
		},
	})
	if err != nil {
		t.Fatalf("captureConsultPeerEvidence returned error: %v", err)
	}

	planCtx := architectEvidenceTestContext("sess-evidence-confirm", "corr-plan-it")
	payload, _ := json.Marshal(map[string]any{
		"action": "start",
		"query":  "Create a toy Python hello world CLI application.",
	})
	result := a.InvokeSkill(planCtx, "plan", payload)
	if result == nil || !result.Success {
		t.Fatalf("plan start failed: %+v", result)
	}
	data := result.Data.(map[string]any)
	plan := a.planStore.Get(data["plan_id"].(string))
	if plan == nil {
		t.Fatal("expected created plan")
	}
	if len(plan.EvidenceTrail) != 1 {
		t.Fatalf("EvidenceTrail len = %d, want 1", len(plan.EvidenceTrail))
	}
	if got := plan.EvidenceTrail[0].SourceArtifactID; got != "consult-before-confirmation" {
		t.Fatalf("SourceArtifactID = %q, want consult-before-confirmation", got)
	}
	if !planHasFreshPlanningEvidence(plan, a.config.ConsultationMaxAge) {
		t.Fatal("expected drained discussion-time evidence to satisfy planning evidence gate")
	}
}

func TestConsultPeerEvidenceCaptureDoesNotAutoCarryForwardContinuity(t *testing.T) {
	sessionID := "sess-no-auto-carry-consult"
	registry := claims.DefaultSessionBoardRegistry()
	registry.Remove(sessionID)
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-no-auto-carry-consult",
		SessionID: sessionID,
		TaskID:    "task-no-auto-carry-consult",
	})
	if err := registry.Register(sessionID, board); err != nil {
		t.Fatalf("register board: %v", err)
	}
	t.Cleanup(func() { registry.Remove(sessionID) })

	if err := board.SubmitTestaments(context.Background(),
		claims.Action{AgentID: "librarian", Type: claims.ActionTypeTestament},
		[]claims.Testament{{
			AgentID: "librarian",
			Summary: "No existing Python project structure was found.",
			Artifacts: []*claims.Artifact{{
				AgentID:   "librarian",
				Kind:      claims.ArtifactKindResponseText,
				Reference: "No pyproject.toml or setup.py exists; create a minimal Python CLI package.",
			}},
		}},
	); err != nil {
		t.Fatalf("submit librarian testament: %v", err)
	}

	a := newTestArchitect(t, Config{SessionID: sessionID, AllowPlanningWithoutConsultation: true})
	ctx := architectEvidenceTestContext(sessionID, "corr-auto-carry")
	query := "Is there an existing Python project structure or CLI convention?"
	err := a.captureConsultPeerEvidence(ctx, &skills.ToolCallHookData{
		ToolName: "consult_peer",
		Input: map[string]any{
			"target_agent_type": "librarian",
			"query":             query,
			"scope":             ".",
		},
		Output: map[string]any{
			"consult_id": "consult-auto-carry",
			"status":     "completed",
			"response": map[string]any{
				"summary": "No pyproject.toml or setup.py exists; create a minimal Python CLI package.",
			},
		},
	})
	if err != nil {
		t.Fatalf("captureConsultPeerEvidence returned error: %v", err)
	}

	for _, testament := range board.Projection().Testaments {
		if testament.AgentID != "architect" {
			continue
		}
		for _, artifact := range testament.Artifacts {
			if artifact != nil && artifact.Kind == claims.ArtifactKindContinuityCursor {
				t.Fatalf("consult evidence capture must not auto-publish carry_forward continuity: %+v", testament)
			}
		}
	}
}

func TestConsultPeerEvidenceCaptureIsIdempotentByConsultID(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "sess-idem", AllowPlanningWithoutConsultation: true})
	ctx := architectEvidenceTestContext("sess-idem", "corr-idem")
	plan := newProtocolPlan(&ArchitectRequest{SessionID: "sess-idem", Query: "Plan", Timestamp: time.Now()}, "corr-idem")
	if err := a.persistPlanState(plan); err != nil {
		t.Fatal(err)
	}
	data := &skills.ToolCallHookData{
		ToolName: "consult_peer",
		Input: map[string]any{
			"target_agent_type": "librarian",
			"query":             "Find CLI patterns.",
		},
		Output: map[string]any{
			"consult_id": "same-consult",
			"status":     "completed",
			"response":   map[string]any{"summary": "Use cli.py."},
		},
	}
	if err := a.captureConsultPeerEvidence(ctx, data); err != nil {
		t.Fatal(err)
	}
	if err := a.captureConsultPeerEvidence(ctx, data); err != nil {
		t.Fatal(err)
	}
	if got := len(plan.EvidenceTrail); got != 1 {
		t.Fatalf("EvidenceTrail len after replay = %d, want 1", got)
	}
}

func TestRecallForwardEvidenceCaptureAttachesContinuityToPlan(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "sess-recall", AllowPlanningWithoutConsultation: true})
	ctx := architectEvidenceTestContext("sess-recall", "corr-recall")
	plan := newProtocolPlan(&ArchitectRequest{SessionID: "sess-recall", Query: "Plan", Timestamp: time.Now()}, "corr-recall")
	if err := a.persistPlanState(plan); err != nil {
		t.Fatal(err)
	}

	err := a.captureRecallForwardEvidence(ctx, &skills.ToolCallHookData{
		ToolName: "recall_forward",
		Input: map[string]any{
			"topic":           "python cli plan",
			"include_sources": "source_index",
		},
		Output: map[string]any{
			"topic":               "python cli plan",
			"status":              "usable",
			"usable":              true,
			"working_context":     "Earlier Librarian evidence found no existing Python package; use a minimal click CLI.",
			"evidence_digest":     "submit_testaments carried the repository scan and CLI recommendation.",
			"include_sources":     "source_index",
			"source_index_count":  1,
			"hydration_available": true,
			"lookback_sessions":   0,
			"items": []any{map[string]any{
				"testament_id":       "testament-continuity",
				"working_context":    "Earlier Librarian evidence found no existing Python package.",
				"evidence_digest":    "Use a minimal click CLI.",
				"through_sequence":   float64(12),
				"source_index_count": float64(1),
			}},
		},
	})
	if err != nil {
		t.Fatalf("captureRecallForwardEvidence returned error: %v", err)
	}
	if got := len(plan.EvidenceTrail); got != 1 {
		t.Fatalf("EvidenceTrail len = %d, want 1", got)
	}
	ev := plan.EvidenceTrail[0]
	if ev.Kind != EvidenceKindContinuity {
		t.Fatalf("evidence kind = %q, want %q", ev.Kind, EvidenceKindContinuity)
	}
	if !ev.Success {
		t.Fatalf("expected successful continuity evidence: %+v", ev)
	}
	if !strings.Contains(ev.Summary, "Earlier Librarian evidence") {
		t.Fatalf("summary did not use recall context: %q", ev.Summary)
	}
	if plan.Consultations["continuity"] != nil {
		t.Fatalf("continuity evidence must not masquerade as consultation index: %+v", plan.Consultations)
	}
}

func TestRecallForwardEvidenceCaptureIgnoresWeakContinuity(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "sess-recall-weak", AllowPlanningWithoutConsultation: true})
	ctx := architectEvidenceTestContext("sess-recall-weak", "corr-recall-weak")
	plan := newProtocolPlan(&ArchitectRequest{SessionID: "sess-recall-weak", Query: "Plan", Timestamp: time.Now()}, "corr-recall-weak")
	if err := a.persistPlanState(plan); err != nil {
		t.Fatal(err)
	}

	err := a.captureRecallForwardEvidence(ctx, &skills.ToolCallHookData{
		ToolName: "recall_forward",
		Input: map[string]any{
			"topic": "python cli plan",
		},
		Output: map[string]any{
			"topic":                   "python cli plan",
			"status":                  "insufficient",
			"usable":                  false,
			"reason":                  "related enrichment exists, but no source-indexed carry_forward continuity was found for this agent/topic",
			"recommended_next_action": "perform the narrowest necessary evidence-gathering step, then call carry_forward(topic=..., mode=advance)",
			"working_context":         "A related legacy note mentioned Click.",
			"evidence_digest":         "Reconstructed continuity candidate from archivalist_scribe_entries.",
			"items": []any{map[string]any{
				"reconstructed":      true,
				"working_context":    "A related legacy note mentioned Click.",
				"source_index_count": float64(0),
			}},
		},
	})
	if err != nil {
		t.Fatalf("captureRecallForwardEvidence returned error: %v", err)
	}
	if got := len(plan.EvidenceTrail); got != 0 {
		t.Fatalf("weak recall evidence must not attach to plan, got %d item(s): %+v", got, plan.EvidenceTrail)
	}
	if planHasFreshPlanningEvidence(plan, a.config.ConsultationMaxAge) {
		t.Fatal("weak recall evidence must not satisfy the planning evidence gate")
	}
}

func TestParallelConsultPeerEvidenceWritesBothTargets(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "sess-parallel", AllowPlanningWithoutConsultation: true})
	ctx := architectEvidenceTestContext("sess-parallel", "corr-parallel")
	plan := newProtocolPlan(&ArchitectRequest{SessionID: "sess-parallel", Query: "Plan", Timestamp: time.Now()}, "corr-parallel")
	if err := a.persistPlanState(plan); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, target := range []string{"librarian", "academic"} {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = a.captureConsultPeerEvidence(ctx, &skills.ToolCallHookData{
				ToolName: "consult_peer",
				Input: map[string]any{
					"target_agent_type": target,
					"query":             "Question for " + target,
				},
				Output: map[string]any{
					"consult_id": "consult-" + target,
					"status":     "completed",
					"response":   map[string]any{"summary": "Answer from " + target},
				},
			})
		}()
	}
	wg.Wait()
	if got := len(plan.EvidenceTrail); got != 2 {
		t.Fatalf("EvidenceTrail len = %d, want 2", got)
	}
	if plan.Consultations["librarian"] == nil || plan.Consultations["academic"] == nil {
		t.Fatalf("missing consultation index entries: %+v", plan.Consultations)
	}
}

func TestContinuationPostToolHooksCaptureYieldedConsultResult(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "sess-resume", AllowPlanningWithoutConsultation: true})
	ctx := architectEvidenceTestContext("sess-resume", "corr-resume")
	plan := newProtocolPlan(&ArchitectRequest{SessionID: "sess-resume", Query: "Plan", Timestamp: time.Now()}, "corr-resume")
	if err := a.persistPlanState(plan); err != nil {
		t.Fatal(err)
	}
	args := `{"target_agent_type":"librarian","query":"Find CLI patterns.","scope":"."}`
	snapshot := &shared.TurnSnapshot{
		AgentID:           "architect",
		SessionID:         "sess-resume",
		CorrelationID:     "corr-resume",
		AwaitToolCallID:   "call-consult",
		AwaitToolName:     "consult_peer",
		AwaitedConsultIDs: []string{"yielded-consult"},
		Request: &providers.Request{Messages: []providers.Message{{
			Role: providers.RoleAssistant,
			ToolCalls: []providers.ToolCall{{
				ID:        "call-consult",
				Name:      "consult_peer",
				Arguments: args,
			}},
		}}},
	}
	output := `{"results":{"yielded-consult":{"status":"completed","response":{"summary":"Use cli.py."},"summary":"Use cli.py.","responder":"librarian"}}}`
	a.runContinuationPostToolHooks(ctx, snapshot, output)
	if len(plan.EvidenceTrail) != 1 {
		t.Fatalf("EvidenceTrail len = %d, want 1", len(plan.EvidenceTrail))
	}
	if plan.Consultations["librarian"] == nil {
		t.Fatal("expected yielded librarian evidence in consultation index")
	}
}

func TestPlanningEvidenceDigestPrefersRecentOnTopicWithinBudget(t *testing.T) {
	now := time.Now()
	plan := &DesignPlan{
		ID:        "plan-digest",
		SessionID: "sess-digest",
		EvidenceTrail: []*PlanEvidence{
			{
				ID:         "consult:old-academic",
				Kind:       EvidenceKindConsult,
				Target:     "academic",
				Query:      "General architecture research",
				Success:    true,
				Summary:    "A broad and stale discussion about unrelated infrastructure tradeoffs " + stringsRepeat("x", 900),
				ReceivedAt: now.Add(-2 * time.Hour),
			},
			{
				ID:         "consult:new-librarian",
				Kind:       EvidenceKindConsult,
				Target:     "librarian",
				Query:      "Python CLI hello world pattern",
				Success:    true,
				Summary:    "Use cli.py for the Python CLI entrypoint.",
				ReceivedAt: now,
			},
		},
	}
	digest := planningEvidenceDigestWithBudget(plan, "Create a Python hello world CLI", 420)
	if len(digest) != 1 {
		t.Fatalf("digest len = %d, want 1: %+v", len(digest), digest)
	}
	if digest[0]["target"] != "librarian" {
		t.Fatalf("digest target = %v, want librarian", digest[0]["target"])
	}
}

func stringsRepeat(s string, count int) string {
	var b strings.Builder
	for i := 0; i < count; i++ {
		b.WriteString(s)
	}
	return b.String()
}
