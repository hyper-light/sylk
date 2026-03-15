package pipeline

import (
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

func (b *testerCoordinationBus) Close() error { return nil }

func TestReportToEngineerSkillPublishesVerificationArtifact(t *testing.T) {
	pt, ctx, _ := newGoPipelineTesterWithVFS(t)
	pt.config.SessionID = "sess-1"
	pt.pipelineName = "Implement CLI Module"
	pt.currentTask = &agentshared.PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "tester-pipeline",
		Prompt:    "Implement the CLI module and package entrypoint.",
		Context: map[string]any{
			"acceptance_criteria": []map[string]any{
				{"id": "SC-01", "description": "CLI prints greeting"},
			},
			"success_criteria":     []string{"Tests fail before implementation and pass after."},
			"test_requirements":    []string{"Write and execute red-phase tests."},
			"implementation_guide": "Author CLI module, then execute tests.",
			"pipeline_protocol": map[string]any{
				"current_request": "Author and execute the red-phase tests, then prepare the verification handoff.",
			},
			"coordination_packet": map[string]any{
				"relevant_artifacts": []any{
					map[string]any{
						"id":            "criteria-1",
						"kind":          "invariant_set",
						"summary":       "Inspector criteria",
						"producer_type": "inspector-pipeline",
					},
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
			{Name: "TestAdd", Package: "pkg/service", Status: tester.StatusFailed, ErrorMessage: "got 4, want 5", Output: "assertion failed"},
			{Name: "TestSub", Package: "pkg/service", Status: tester.StatusPassed},
		},
	})
	pt.storeDiagnosis(&testershared.DiagnosisReport{
		ID:           "diag-1",
		TestName:     "TestAdd",
		Confidence:   0.9,
		IsProductBug: true,
		RootCauses: []testershared.RootCause{
			{File: "pkg/service/service.go", Line: 3, Description: "Add returns the wrong value"},
		},
	})

	bus := &testerCoordinationBus{pending: make(map[string]chan *guide.Message)}
	pt.bus = bus
	pt.pendingBus = bus.pending
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
		if input.Kind != "verification_result" {
			t.Fatalf("artifact kind = %q, want verification_result", input.Kind)
		}
		if input.IdempotencyKey != "tester-verification-handoff:task-1:suite-1" {
			t.Fatalf("idempotency key = %q", input.IdempotencyKey)
		}
		if got := input.UpstreamArtifactIDs; len(got) != 1 || got[0] != "criteria-1" {
			t.Fatalf("upstream artifact ids = %#v, want [criteria-1]", got)
		}
		if got := input.Payload["current_request"]; got != "Author and execute the red-phase tests, then prepare the verification handoff." {
			t.Fatalf("current_request = %#v", got)
		}
		if got := input.Payload["authored_test_files"].([]any); len(got) != 1 || got[0] != "pkg/service/service_test.go" {
			t.Fatalf("authored_test_files = %#v", got)
		}
		failures, _ := input.Payload["failures"].([]any)
		if len(failures) != 1 {
			t.Fatalf("failures len = %d, want 1", len(failures))
		}
		diagnoses, _ := input.Payload["diagnoses"].([]any)
		if len(diagnoses) != 1 {
			t.Fatalf("diagnoses len = %d, want 1", len(diagnoses))
		}
		return map[string]any{
			"id":                    "artifact-1",
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

	skill := reportToEngineerSkill(pt)
	output, err := skill.Handler(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("report_to_engineer error = %v", err)
	}
	result, ok := output.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", output)
	}
	if got := result["artifact_id"]; got != "artifact-1" {
		t.Fatalf("artifact_id = %#v, want artifact-1", got)
	}
	refs, _ := result["handoff_references"].([]string)
	if len(refs) != 1 || refs[0] != "artifact:artifact-1" {
		t.Fatalf("handoff_references = %#v", result["handoff_references"])
	}
}

func TestReportToDesignerSkillRequiresSuiteExecution(t *testing.T) {
	pt, ctx, _ := newGoPipelineTesterWithVFS(t)
	pt.currentTask = &agentshared.PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "tester-pipeline",
		Prompt:    "Implement the CLI module and package entrypoint.",
		Context: map[string]any{
			"pipeline_protocol": map[string]any{
				"current_request": "Author and execute the red-phase tests, then prepare the verification handoff.",
			},
		},
	}

	_, err := reportToDesignerSkill(pt).Handler(ctx, []byte(`{}`))
	if err == nil {
		t.Fatal("expected suite execution requirement error")
	}
	if !strings.Contains(err.Error(), "run_test_suite") {
		t.Fatalf("error = %v, want run_test_suite guidance", err)
	}
}
