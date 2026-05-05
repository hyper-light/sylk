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
	"github.com/adalundhe/sylk/core/pipeline/coordination"
)

type testerCoordinationBus struct {
	mu      sync.Mutex
	pending map[string]chan *guide.Message
	handle  func(*guide.ActionRequest) (map[string]any, error)
}

func (b *testerCoordinationBus) Publish(topic string, msg *guide.Message) error {
	if topic != guide.TopicGuideRequests {
		return fmt.Errorf("unexpected topic %q", topic)
	}
	req, ok := msg.GetActionRequest()
	if !ok || req == nil {
		return fmt.Errorf("expected action request message")
	}
	payload := map[string]any{"ok": true}
	if b.handle != nil {
		data, err := b.handle(req)
		if err != nil {
			resp := guide.NewResponseMessage("", &guide.RouteResponse{
				CorrelationID: req.CorrelationID,
				Success:       false,
				Error:         err.Error(),
			})
			b.mu.Lock()
			ch := b.pending[req.CorrelationID]
			b.mu.Unlock()
			if ch != nil {
				ch <- resp
				return nil
			}
			return err
		}
		if data != nil {
			payload = data
		}
	}
	b.mu.Lock()
	ch := b.pending[req.CorrelationID]
	b.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("no pending waiter for correlation %q", req.CorrelationID)
	}
	resp := guide.NewResponseMessage("", &guide.RouteResponse{
		CorrelationID: req.CorrelationID,
		Success:       true,
		Data:          payload,
	})
	ch <- resp
	return nil
}

func (b *testerCoordinationBus) Subscribe(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, fmt.Errorf("not implemented")
}

func (b *testerCoordinationBus) SubscribeAsync(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, fmt.Errorf("not implemented")
}

func (b *testerCoordinationBus) Close() error                            { return nil }
func (b *testerCoordinationBus) CloseWithContext(_ context.Context) error { return nil }

func TestPublishVerificationArtifact_PerRecipientPayload(t *testing.T) {
	pt, ctx, _ := newGoPipelineTesterWithVFS(t)
	pt.config.SessionID = "sess-1"
	pt.pipelineName = "Implement CLI Module"
	pt.currentTask = &agentshared.PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "tester-pipeline",
		Prompt:    "Implement the CLI module and package entrypoint.",
		Context: map[string]any{
			"acceptance_criteria":  []map[string]any{{"id": "SC-01", "description": "CLI prints greeting"}},
			"success_criteria":     []string{"Tests fail before implementation and pass after."},
			"test_requirements":    []string{"Write and execute red-phase tests."},
			"implementation_guide": "Author CLI module, then execute tests.",
			"pipeline_protocol":    map[string]any{"current_request": "Author and execute the red-phase tests."},
			"coordination_packet": map[string]any{
				"relevant_artifacts": []any{
					map[string]any{"id": "criteria-1", "kind": "invariant_set", "summary": "Inspector criteria", "producer_type": "inspector-pipeline"},
				},
			},
		},
	}
	pt.setHarnessState(&testHarnessState{
		FrameworkID:       "gotest",
		FrameworkName:     "Go Test",
		RunCommand:        "go test ./...",
		CreatedFiles:      []string{"pkg/service/service_test.go"},
		ExistingTestFiles: []string{"pkg/service/service_test.go"},
	})
	pt.mu.Lock()
	pt.currentPlan = &testershared.TestPlan{
		ID:        "plan-1",
		Rationale: "Validate the CLI greeting contract.",
		PlannedCase: []testershared.PlannedTestCase{
			{Name: "TestAdd", TargetFile: "pkg/service/service.go", ExpectedBehavior: "returns the sum"},
		},
	}
	pt.mu.Unlock()
	pt.setLastSuiteResult(&tester.TestSuiteResult{
		SuiteID:    "suite-1",
		Name:       "go test",
		TotalTests: 2,
		Passed:     1,
		Failed:     1,
		Results: []tester.TestResult{
			{Name: "TestAdd", Package: "pkg/service", Status: tester.StatusFailed, ErrorMessage: "got 4, want 5"},
			{Name: "TestSub", Package: "pkg/service", Status: tester.StatusPassed},
		},
	})
	pt.storeDiagnosis(&testershared.DiagnosisReport{
		ID: "diag-1", TestName: "TestAdd", Confidence: 0.9, IsProductBug: true,
		RootCauses: []testershared.RootCause{{File: "pkg/service/service.go", Line: 3, Description: "Add returns the wrong value"}},
	})

	bus := &testerCoordinationBus{pending: make(map[string]chan *guide.Message)}
	pt.bus = bus
	pt.pendingBus = bus.pending
	captured := make([]coordination.PublishArtifactInput, 0, 2)
	bus.handle = func(req *guide.ActionRequest) (map[string]any, error) {
		if req.Action != coordination.ActionPublishArtifact {
			return nil, fmt.Errorf("unexpected action %q", req.Action)
		}
		raw, err := json.Marshal(req.Data)
		if err != nil {
			return nil, err
		}
		var input coordination.PublishArtifactInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		captured = append(captured, input)
		return map[string]any{
			"id":                    fmt.Sprintf("artifact-%d", len(captured)),
			"task_id":               input.TaskID,
			"kind":                  input.Kind,
			"summary":               input.Summary,
			"producer_agent_id":     "tester-1",
			"producer_type":         "tester-pipeline",
			"idempotency_key":       input.IdempotencyKey,
			"payload":               input.Payload,
			"evidence":              input.Evidence,
			"upstream_artifact_ids": input.UpstreamArtifactIDs,
			"created_at":            time.Now(),
			"updated_at":            time.Now(),
			"status":                string(coordination.ArtifactStatusPublished),
		}, nil
	}

	engineerSpec := agentshared.PipelineTesterFinalizeTargetSpec{
		Target:       agentshared.PipelineAgentEngineer,
		Summary:      "Engineer-relevant: TestAdd asserts wrong value in Add().",
		EvidenceRefs: []string{"pkg/service/service.go"},
		FailureFocus: []string{"TestAdd"},
	}
	designerSpec := agentshared.PipelineTesterFinalizeTargetSpec{
		Target:       agentshared.PipelineAgentDesigner,
		Summary:      "Designer-relevant: contract for Add() needs success-criterion update.",
		EvidenceRefs: []string{"design/contracts/add.md"},
	}

	engineerArt, err := pt.publishVerificationArtifact(ctx, engineerSpec)
	if err != nil {
		t.Fatalf("publish engineer artifact: %v", err)
	}
	designerArt, err := pt.publishVerificationArtifact(ctx, designerSpec)
	if err != nil {
		t.Fatalf("publish designer artifact: %v", err)
	}
	if engineerArt.ID == designerArt.ID {
		t.Fatalf("expected per-target artifact IDs to differ; both = %q", engineerArt.ID)
	}

	if len(captured) != 2 {
		t.Fatalf("captured %d publish calls, want 2", len(captured))
	}
	if want := "tester-verification-handoff:task-1:suite-1:engineer"; captured[0].IdempotencyKey != want {
		t.Fatalf("engineer idempotency key = %q, want %q", captured[0].IdempotencyKey, want)
	}
	if want := "tester-verification-handoff:task-1:suite-1:designer"; captured[1].IdempotencyKey != want {
		t.Fatalf("designer idempotency key = %q, want %q", captured[1].IdempotencyKey, want)
	}
	if !strings.Contains(captured[0].Title, "engineer") {
		t.Fatalf("engineer artifact title = %q, want contains engineer", captured[0].Title)
	}
	if !strings.Contains(captured[1].Title, "designer") {
		t.Fatalf("designer artifact title = %q, want contains designer", captured[1].Title)
	}

	engineerView, _ := captured[0].Payload["recipient_view"].(map[string]any)
	if engineerView == nil {
		t.Fatal("engineer payload missing recipient_view")
	}
	if engineerView["target"] != "engineer" {
		t.Fatalf("engineer recipient_view target = %#v", engineerView["target"])
	}
	focused, _ := engineerView["focused_failures"].([]any)
	if len(focused) != 1 {
		t.Fatalf("engineer focused_failures = %#v, want single TestAdd entry", engineerView["focused_failures"])
	}
	first, _ := focused[0].(map[string]any)
	if first["name"] != "TestAdd" {
		t.Fatalf("engineer focused_failures[0].name = %#v, want TestAdd", first["name"])
	}

	designerView, _ := captured[1].Payload["recipient_view"].(map[string]any)
	if designerView == nil {
		t.Fatal("designer payload missing recipient_view")
	}
	if designerView["target"] != "designer" {
		t.Fatalf("designer recipient_view target = %#v", designerView["target"])
	}
	if _, hasFocused := designerView["focused_failures"]; hasFocused {
		t.Fatalf("designer recipient_view should not have focused_failures (no failure_focus given)")
	}
}

func TestPublishVerificationArtifact_RequiresSuiteSnapshot(t *testing.T) {
	pt, ctx, _ := newGoPipelineTesterWithVFS(t)
	pt.currentTask = &agentshared.PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "tester-pipeline",
		Prompt:    "Implement the CLI module and package entrypoint.",
		Context: map[string]any{
			"pipeline_protocol": map[string]any{"current_request": "Author and execute the red-phase tests."},
		},
	}

	_, err := pt.publishVerificationArtifact(ctx, agentshared.PipelineTesterFinalizeTargetSpec{
		Target:  agentshared.PipelineAgentDesigner,
		Summary: "premature publish",
	})
	if err == nil {
		t.Fatal("expected suite execution requirement error")
	}
	if !strings.Contains(err.Error(), "run_test_suite") {
		t.Fatalf("error = %v, want run_test_suite guidance", err)
	}
}

func TestCurrentSuiteID_EmptyBeforeSuite_PopulatedAfter(t *testing.T) {
	pt, _, _ := newGoPipelineTesterWithVFS(t)
	if got := pt.currentSuiteID(); got != "" {
		t.Fatalf("currentSuiteID before suite = %q, want empty", got)
	}
	pt.setLastSuiteResult(&tester.TestSuiteResult{SuiteID: "suite-xyz"})
	if got := pt.currentSuiteID(); got != "suite-xyz" {
		t.Fatalf("currentSuiteID after suite = %q, want suite-xyz", got)
	}
}
