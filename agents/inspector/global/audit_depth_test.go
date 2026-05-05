package global

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

func TestDetermineAuditDepthDecision_UsesAcademicCompatibleDepth(t *testing.T) {
	ctx := withGlobalAuditRequestState(
		agentShared.WithAuditDepthContext(context.Background(), nil),
		"Run a progressive checkpoint audit over this small stdlib-only Python CLI package.",
		&agentShared.GlobalExecutionContract{Mode: agentShared.GlobalExecutionModeAudit},
		map[string]any{
			"global_review_stage": "checkpoint",
			"affected_files":      []string{"pyproject.toml", "hello.py"},
		},
	)

	decision := determineAuditDepthDecision(ctx, "")
	if decision.Depth != agentShared.ResearchDepthQuick {
		t.Fatalf("depth = %q, want %q", decision.Depth, agentShared.ResearchDepthQuick)
	}
	if !containsString(agentShared.ResearchDepthEnumValues(), string(decision.Depth)) {
		t.Fatalf("depth %q is not Academic-compatible", decision.Depth)
	}
	if strings.TrimSpace(decision.AssessmentGuidance) == "" {
		t.Fatal("expected assessment guidance")
	}
}

func TestDetermineAuditDepthSkill_PersistsContractIntoGlobalReviewState(t *testing.T) {
	state := agentShared.NewGlobalReviewState(nil, map[string]any{
		"global_review":       true,
		"global_review_stage": "checkpoint",
		"affected_files":      []string{"pyproject.toml", "hello.py"},
	})
	ctx := agentShared.WithGlobalReviewState(context.Background(), state)
	ctx = agentShared.WithAuditDepthContext(ctx, nil)
	ctx = withGlobalAuditRequestState(ctx,
		"Run a progressive checkpoint audit over this small stdlib-only Python CLI package.",
		&agentShared.GlobalExecutionContract{Mode: agentShared.GlobalExecutionModeAudit},
		map[string]any{
			"global_review_stage": "checkpoint",
			"affected_files":      []string{"pyproject.toml", "hello.py"},
		},
	)

	skill := determineAuditDepthSkill(nil)
	if skill == nil || skill.Handler == nil {
		t.Fatal("expected determine_audit_depth skill handler")
	}
	raw, err := skill.Handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("determine_audit_depth: %v", err)
	}
	result, _ := raw.(map[string]any)
	if got := strings.TrimSpace(result["depth"].(string)); got != string(agentShared.ResearchDepthQuick) {
		t.Fatalf("result depth = %q, want %q", got, agentShared.ResearchDepthQuick)
	}

	contract, ok := agentShared.AuditDepthContractFromContext(ctx)
	if !ok {
		t.Fatal("expected persisted audit depth contract in context")
	}
	if contract.Depth != string(agentShared.ResearchDepthQuick) {
		t.Fatalf("context depth = %q, want %q", contract.Depth, agentShared.ResearchDepthQuick)
	}
	persisted, ok := agentShared.AuditDepthContractFromMetadata(state.BaseMetadata())
	if !ok {
		t.Fatal("expected audit depth contract in global review base metadata")
	}
	if persisted.Depth != string(agentShared.ResearchDepthQuick) {
		t.Fatalf("metadata depth = %q, want %q", persisted.Depth, agentShared.ResearchDepthQuick)
	}
}

func TestApplyToolCalls_AuditDepthGateRequiresDetermineFirst(t *testing.T) {
	gi := &GlobalInspector{}
	ctx := agentShared.WithAuditDepthContext(context.Background(), nil)
	req := &providers.Request{}
	resp := &providers.Response{
		ToolCalls: []providers.ToolCall{
			{ID: "call-1", Name: "read_workspace_file", Arguments: `{}`},
			{ID: "call-2", Name: "consult_academic_approach", Arguments: `{}`},
		},
	}

	errCount, rerouted, delegated, delegatedMessage, _ := gi.applyToolCalls(ctx, req, resp)
	if errCount != 1 {
		t.Fatalf("errCount = %d, want 1", errCount)
	}
	if rerouted || delegated || delegatedMessage != "" {
		t.Fatalf("unexpected reroute/delegation state: rerouted=%v delegated=%v msg=%q", rerouted, delegated, delegatedMessage)
	}
	if len(req.Messages) < 2 {
		t.Fatalf("messages = %d, want assistant + tool result", len(req.Messages))
	}
	toolMsg := req.Messages[1]
	if !toolMsg.IsError {
		t.Fatal("expected first tool result to be an error")
	}
	if !strings.Contains(toolMsg.Content, determineAuditDepthToolName) {
		t.Fatalf("tool error = %q, want determine_audit_depth guidance", toolMsg.Content)
	}
}

func TestConsultationMetadataWithAuditDepth_InheritsBranchDepth(t *testing.T) {
	ctx := agentShared.WithAuditDepthContext(context.Background(), agentShared.MetadataWithAuditDepthContract(nil, agentShared.AuditDepthContract{
		Depth:     string(agentShared.ResearchDepthDeep),
		Rationale: "deep audit branch",
	}))
	metadata := consultationMetadataWithAuditDepth(ctx, map[string]any{"consultation_kind": "academic_approach"}, "")

	if got := agentShared.ConsultationResearchDepth(metadata); got != agentShared.ResearchDepthDeep {
		t.Fatalf("research depth = %q, want %q", got, agentShared.ResearchDepthDeep)
	}
	contract, ok := agentShared.AuditDepthContractFromMetadata(metadata)
	if !ok {
		t.Fatal("expected audit depth contract to be copied into consult metadata")
	}
	if contract.Depth != string(agentShared.ResearchDepthDeep) {
		t.Fatalf("contract depth = %q, want %q", contract.Depth, agentShared.ResearchDepthDeep)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
