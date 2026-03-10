package orchestrator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
)

func TestHandleValidationVerdictForwardCreatesHoldAndRemediationCase(t *testing.T) {
	store, err := OpenStore(DefaultStoreConfig(filepath.Join(t.TempDir(), "orchestrator.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	orch := &Orchestrator{
		config:       Config{SessionID: "sess", AgentID: "orchestrator"},
		store:        store,
		bus:          bus,
		running:      true,
		pendingBus:   make(map[string]chan *guide.Message),
		dispatchGate: newDispatchHoldGate(),
		channels:     guide.NewAgentChannels("orchestrator", "orchestrator"),
	}

	sub, err := bus.SubscribeAsync(orch.channels.Responses, orch.handleBusResponse)
	if err != nil {
		t.Fatalf("subscribe orchestrator responses: %v", err)
	}
	defer sub.Unsubscribe()

	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		if req.TargetAgentID != "architect" {
			return nil
		}
		resp := &guide.RouteResponse{
			CorrelationID:       req.CorrelationID,
			Success:             true,
			RespondingAgentID:   "architect",
			RespondingAgentName: "architect",
			Data: &agentshared.RemediationResult{
				CaseID:         "ignored",
				SessionID:      "sess",
				Resolution:     agentshared.RemediationResolutionNeedsUserInput,
				Summary:        "Need more detail",
				NeedsUserInput: true,
				CreatedAt:      time.Now().UTC(),
			},
		}
		return bus.Publish(orch.channels.Responses, guide.NewResponseMessage("resp_1", resp))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	payload := &agentshared.ValidationVerdictPayload{
		Kind:             agentshared.ValidationVerdictNeedsArchitectRemediation,
		Severity:         agentshared.ValidationSeverityCritical,
		ValidatorAgentID: "tester-1",
		ValidatorType:    "global-tester",
		SessionID:        "sess",
		Summary:          "Blocking cross-pipeline failure",
		ShouldPause:      true,
		AffectedTasks:    []string{"task-1"},
	}
	body, err := encodeRouteSyncInput(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	fwd := &guide.ForwardedRequest{
		Input:           body,
		SessionID:       "sess",
		SourceAgentID:   "tester-1",
		SourceAgentName: "tester",
		Metadata: map[string]any{
			"control_plane_kind": agentshared.ControlPlaneKindValidationVerdict,
		},
	}

	result, err := orch.handleValidationVerdictForward(context.Background(), fwd)
	if err != nil {
		t.Fatalf("handle validation verdict: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	hold, err := store.GetActiveExecutionHold("sess")
	if err != nil {
		t.Fatalf("get active hold: %v", err)
	}
	if hold == nil {
		t.Fatal("expected active execution hold")
	}
	if !orch.dispatchGate.isActive("sess") {
		t.Fatal("expected dispatch gate to be active")
	}
	caseRecord, err := store.GetRemediationCase(hold.RemediationCaseID)
	if err != nil {
		t.Fatalf("get remediation case: %v", err)
	}
	if caseRecord == nil {
		t.Fatal("expected remediation case")
	}
	if caseRecord.Status != RemediationCaseStatusNeedsUserInput {
		t.Fatalf("expected needs_user_input case status, got %s", caseRecord.Status)
	}
}

func TestHandleValidationVerdictForwardPassDoesNotReleaseActiveHold(t *testing.T) {
	store, err := OpenStore(DefaultStoreConfig(filepath.Join(t.TempDir(), "orchestrator.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	now := time.Now().UTC()
	hold := &ExecutionHoldRecord{
		HoldID:             "hold_existing",
		SessionID:          "sess",
		EpochID:            "epoch_existing",
		Status:             ExecutionHoldStatusActive,
		Reason:             "blocking_failure",
		Summary:            "Existing blocking validation failure",
		CreatedByAgentID:   "tester-1",
		CreatedByAgentType: "global-tester",
		CreatedAt:          now,
	}
	if err := store.CreateExecutionHold(hold); err != nil {
		t.Fatalf("create hold: %v", err)
	}

	orch := &Orchestrator{
		config:       Config{SessionID: "sess", AgentID: "orchestrator"},
		store:        store,
		running:      true,
		dispatchGate: newDispatchHoldGate(),
	}
	orch.dispatchGate.activate("sess")

	payload := &agentshared.ValidationVerdictPayload{
		Kind:             agentshared.ValidationVerdictPass,
		Severity:         agentshared.ValidationSeverityInfo,
		ValidatorAgentID: "inspector-1",
		ValidatorType:    "global-inspector",
		SessionID:        "sess",
		Summary:          "No issues found in this validation pass",
	}
	body, err := encodeRouteSyncInput(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	fwd := &guide.ForwardedRequest{
		Input:           body,
		SessionID:       "sess",
		SourceAgentID:   "inspector-1",
		SourceAgentName: "inspector",
		Metadata: map[string]any{
			"control_plane_kind": agentshared.ControlPlaneKindValidationVerdict,
		},
	}

	result, err := orch.handleValidationVerdictForward(context.Background(), fwd)
	if err != nil {
		t.Fatalf("handle validation verdict: %v", err)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if got["hold_state"] != string(ExecutionHoldStatusActive) {
		t.Fatalf("expected active hold state, got %#v", got["hold_state"])
	}
	storedHold, err := store.GetActiveExecutionHold("sess")
	if err != nil {
		t.Fatalf("get active hold: %v", err)
	}
	if storedHold == nil {
		t.Fatal("expected hold to remain active")
	}
	if !orch.dispatchGate.isActive("sess") {
		t.Fatal("expected dispatch gate to remain active")
	}
}
