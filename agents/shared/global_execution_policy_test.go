package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestGlobalReviewHandoffNext_RequiresConcreteTesterEvidence(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	ctx := WithGlobalReviewState(context.Background(), NewGlobalReviewState(&GlobalReviewSnapshot{
		ReviewID:       "review-evidence",
		CurrentRequest: "Audit the merged batch.",
	}, GlobalReviewMetadata(map[string]any{
		"global_review_stage": "checkpoint",
	}, nil)))
	ctx = WithStreamContext(ctx, "corr-review", "tui")
	ctx = WithGlobalExecutionState(ctx, NewGlobalExecutionState(&GlobalExecutionContract{
		Role:   "tester-global",
		Mode:   GlobalExecutionModeExecute,
		Intent: guide.IntentCheck,
	}))

	protocolSkills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
		AgentType: func() string { return GlobalReviewAgentTester },
		AgentID:   func() string { return "tester-global-1" },
		ResolveTarget: func(agentType string) string {
			if agentType == GlobalReviewAgentInspector {
				return "inspector-global-1"
			}
			return agentType
		},
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	})

	_, err := invokeGlobalReviewSkill(t, ctx, protocolSkills, "handoff_next", map[string]any{
		"target_agents": []string{GlobalReviewAgentInspector},
		"reason":        "Returning the tester verdict.",
		"request":       "Audit the returned tester evidence.",
	})
	if err == nil {
		t.Fatal("expected tester handoff to be blocked without evidence")
	}
	if !strings.Contains(err.Error(), "run_test_suite") &&
		!strings.Contains(err.Error(), "read the relevant existing tests") &&
		!strings.Contains(err.Error(), "inspect the relevant implementation") {
		t.Fatalf("error = %v, want concrete evidence guidance", err)
	}

	RecordGlobalExecutionSuccess(ctx, "read_workspace_file", map[string]any{"path": "pkg/service/service.go"}, "")
	RecordGlobalExecutionSuccess(ctx, "read_workspace_file", map[string]any{"path": "pkg/service/service_test.go"}, "")
	RecordGlobalExecutionSuccess(ctx, "analyze_risk", map[string]any{"files": []string{"pkg/service/service.go"}}, `{"risk_count":1,"risk_areas":[{"file":"pkg/service/service.go","category":"logic","level":"medium","description":"Check merged behavior"}]}`)
	RecordGlobalExecutionSuccess(ctx, "run_test_suite", map[string]any{"files": []string{"pkg/service/service.go"}}, `{"passed":1,"failed":0}`)

	result, err := invokeGlobalReviewSkill(t, ctx, protocolSkills, "handoff_next", map[string]any{
		"target_agents": []string{GlobalReviewAgentInspector},
		"reason":        "Returning the tester verdict with concrete execution evidence.",
		"request":       "Audit the returned tester evidence.",
	})
	if err != nil {
		t.Fatalf("handoff_next with evidence: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if forwarded, _ := resultMap["forwarded"].(bool); !forwarded {
		t.Fatalf("forwarded = %#v, want true", resultMap["forwarded"])
	}
}
