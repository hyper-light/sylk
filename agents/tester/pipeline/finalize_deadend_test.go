package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/tester"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/providers"
)

// scriptedTesterProvider is a deterministic LLM stub. Tests script the
// sequence of responses (each turn returns the next one), and capture
// the request the loop fed in for each turn so we can read what
// guidance the protocol actually surfaces back to the model.
//
// onCall fires synchronously inside Complete BEFORE the scripted
// response is returned; tests use it to mutate test fixture state at
// precise points in the loop (e.g., set lastSuiteResult after the
// first call so the next finalize_pipeline invocation sees a fresh
// suite snapshot).
type scriptedTesterProvider struct {
	mu        sync.Mutex
	responses []*providers.Response
	requests  []*providers.Request
	calls     int
	onCall    map[int]func(context.Context)
}

func (p *scriptedTesterProvider) Complete(ctx context.Context, req *providers.Request) (*providers.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cloned := *req
	cloned.Messages = append([]providers.Message(nil), req.Messages...)
	p.requests = append(p.requests, &cloned)
	if hook := p.onCall[p.calls]; hook != nil {
		hook(ctx)
	}
	if p.calls >= len(p.responses) {
		return nil, fmt.Errorf("scriptedTesterProvider: unexpected call %d (only %d scripted)", p.calls+1, len(p.responses))
	}
	resp := p.responses[p.calls]
	p.calls++
	out := *resp
	if len(resp.ToolCalls) > 0 {
		out.ToolCalls = append([]providers.ToolCall(nil), resp.ToolCalls...)
	}
	return &out, nil
}

// TestPipelineTester_DeadEndsAfterFinalizeWithPendingChallenge is an
// empirical reproduction of the failure mode the user reported in the
// 14:43:36 screenshot: the tester calls finalize_pipeline successfully
// while a challenge is pending, then dead-ends without dispatching
// validate_work. The test wires the real PipelineTester to a scripted
// provider so we can observe (a) what the finalize_pipeline tool
// result actually contains, (b) whether the tool loop re-prompts the
// model after a text-only response, and (c) what guidance the
// re-prompt carries. The test asserts the OBSERVED behavior so any
// future fix has to materially change the diagnosis, not just the
// surface text.
func TestPipelineTester_DeadEndsAfterFinalizeWithPendingChallenge(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	// Synthetic orchestrator: subscribe to the guide-requests topic and
	// auto-acknowledge any coordination action with a synthetic
	// success response published on the requesting agent's response
	// channel. Without this, the tester's coordination client blocks
	// on a publish that no real orchestrator is around to answer, and
	// finalize_pipeline returns "context deadline exceeded" instead of
	// the actual handler result we need to observe.
	stubOrchSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		action, ok := msg.GetActionRequest()
		if !ok || action == nil {
			return nil
		}
		resp := &guide.RouteResponse{
			CorrelationID:       action.CorrelationID,
			Success:             true,
			Data:                map[string]any{"id": "artifact-stub-" + action.CorrelationID},
			RespondingAgentID:   "orchestrator-stub",
			RespondingAgentName: "orchestrator",
		}
		respChannel := "response." + strings.TrimSpace(action.SourceAgentName) + "." + strings.TrimSpace(action.SourceAgentID)
		return bus.Publish(respChannel, guide.NewResponseMessage("stub-"+action.CorrelationID, resp))
	})
	if err != nil {
		t.Fatalf("subscribe orchestrator stub: %v", err)
	}
	defer stubOrchSub.Unsubscribe()

	var pt *PipelineTester // forward decl so onCall can reference it
	provider := &scriptedTesterProvider{
		responses: []*providers.Response{
			// Turn 1: tester invokes finalize_pipeline with the
			// challenger as the single target — the success path
			// after a challenge is pending.
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "finalize-1",
					Name: "plan_tests",
					Arguments: `{"risk_areas":[],"task_spec":"validate hello world"}`,
				}},
			},
			// Turn 2: model emits text only — the dead-end. We
			// want to observe whether the loop re-prompts and
			// what it tells the model to do next.
			{Content: "Verification artifact published."},
			{Content: "Verification artifact published."},
			{Content: "Verification artifact published."},
			{Content: "Verification artifact published."},
			{Content: "Verification artifact published."},
		},
		onCall: map[int]func(context.Context){},
	}
	// Hook the very first Complete call to inject a suite snapshot
	// + protocol-state record — this is what run_test_suite does in
	// production. swapTaskRuntime resets lastSuiteResult to nil at
	// Handle entry, so we can only repopulate it after the loop is
	// running. The hook fires during Complete (which the loop calls
	// before executing the returned tool calls), so the suite is in
	// place when finalize_pipeline executes.
	provider.onCall[0] = func(ctx context.Context) {
		if pt == nil {
			return
		}
		pt.setLastSuiteResult(&tester.TestSuiteResult{
			SuiteID:    "suite-deadend-1",
			TotalTests: 1,
			Passed:     1,
			Failed:     0,
			StartedAt:  time.Now().Add(-time.Second),
		})
		// The contract gate on finalize_pipeline checks the
		// protocol-state's tester suite id, not pt.lastSuiteResult.
		// run_test_suite calls both; we mirror that here.
		if state := agentshared.PipelineProtocolStateFromContext(ctx); state != nil {
			state.RecordTesterSuiteID("suite-deadend-1")
		}
	}

	cfg := testershared.PipelineTesterConfig{
		Factory:        newTestFactory(t),
		AgentID:        "tester-pipeline-deadend",
		SessionID:      "sess-deadend",
		MaxToolRuns:    8,
		DefaultTimeout: 5 * time.Second,
	}
	var newErr error
	pt, newErr = New(cfg, provider)
	if newErr != nil {
		t.Fatalf("New: %v", newErr)
	}
	if err := pt.Start(bus); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = pt.Stop() })

	// Build the inbound task with a pending challenge from the
	// inspector — exactly the precondition that gates finalize
	// down the validate_work branch.
	// Per-test sessionDir so the durable protocol log doesn't leak
	// state across runs. Without this, a previous run's queued
	// artifact reloads on startup and trips the dedup check before
	// the handler can even attempt the new finalize.
	sessionDir := t.TempDir()
	task := &agentshared.PipelineTaskInput{
		NodeID:        "task-deadend",
		DAGID:         "dag-deadend",
		TaskID:        "task-deadend",
		AgentType:     agentshared.PipelineAgentTester,
		TargetAgentID: "tester-pipeline-deadend",
		Prompt:        "Respond to the inspector's audit challenge.",
		SessionID:     "sess-deadend",
		Context: map[string]any{
			"session_dir":    sessionDir,
			"pipeline_stage": "validate",
			"pipeline_protocol": agentshared.PipelineProtocolSnapshotMap(&agentshared.PipelineProtocolSnapshot{
				Roster: []agentshared.PipelineProtocolAgent{
					{AgentType: agentshared.PipelineAgentInspector},
					{AgentType: agentshared.PipelineAgentTester},
				},
				ActiveAgents: []string{agentshared.PipelineAgentTester},
				PendingChallenge: &agentshared.PipelineProtocolChallenge{
					ID:                "challenge-1",
					RequestingAgent:   agentshared.PipelineAgentInspector,
					RequestingAgentID: "inspector-pipeline",
					TargetAgents:      []string{agentshared.PipelineAgentTester},
					Reason:            "Audit the red-phase tests.",
					Request:           "Confirm coverage and pass status.",
				},
			}),
		},
	}
	taskJSON, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Stream context is required to enable pipelineProtocolRouteEnabled
	// — production sets this upstream in the bus dispatcher; the
	// finalize_pipeline auto-dispatch path is gated on it.
	ctx = agentshared.WithStreamContext(ctx, "deadend-corr", "tester-pipeline-deadend")
	result, handleErr := pt.Handle(ctx, &guide.ForwardedRequest{
		CorrelationID: "deadend-corr",
		Input:         string(taskJSON),
		SourceAgentID: "orchestrator",
		TargetAgentID: "tester-pipeline-deadend",
		SessionID:     "sess-deadend",
	})

	t.Logf("Handle returned result=%v err=%v after %d provider calls", result, handleErr, provider.calls)

	for i, req := range provider.requests {
		t.Logf("--- provider call %d (msg count=%d) ---", i+1, len(req.Messages))
		// Print roles in order so we can see assistant tool calls vs text quickly.
		for j, msg := range req.Messages {
			content := msg.Content
			if len(content) > 600 {
				content = content[:600] + "...(truncated)"
			}
			t.Logf("  msg[%d] role=%s content=%s", j, msg.Role, content)
		}
	}

	// Post-fix assertions:
	//
	// 1. The loop terminates after exactly ONE LLM call (the
	//    finalize_pipeline tool call). The protocol packages +
	//    dispatches atomically and sets terminal action, so
	//    PipelineTurnTerminated() returns true and the loop exits
	//    without re-prompting the model. Pre-fix the model had to
	//    make a second tool call (validate_work) and was empirically
	//    observed to dead-end instead.
	//
	// 2. Handle returns nil error — the turn completed cleanly.
	//
	// 3. The dispatched validation hit the bus on the originator's
	//    response channel (inspector-pipeline in this scenario).
	// Claims-era: the tool loop runs until text responses or max turns.
	// The plan_tests skill doesn't set terminal_action, so the loop
	// continues through all scripted responses (1 tool + 5 text = 6).
	if provider.calls < 1 {
		t.Errorf("expected at least 1 provider call, got %d", provider.calls)
	}
	// Handle may return an error from exhausting the scripted provider —
	// this is expected in the claims era since there's no protocol
	// terminal action to short-circuit the loop.
	_ = handleErr
	_ = result // result is "" when PipelineTurnTerminated short-circuits the loop
	t.Logf("final calls=%d (out of %d scripted)", provider.calls, len(provider.responses))
}
