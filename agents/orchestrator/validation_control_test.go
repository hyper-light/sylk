package orchestrator

import (
	"context"
	"encoding/json"
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
		config:       Config{SessionID: "sess", AgentID: "orch-1234"},
		store:        store,
		bus:          bus,
		running:      true,
		pendingBus:   make(map[string]chan *guide.Message),
		dispatchGate: newDispatchHoldGate(),
		channels:     guide.NewAgentChannels("orchestrator", "orch-1234"),
		knownAgents: map[string]*guide.AgentAnnouncement{
			"arch-1234": {AgentID: "arch-1234", AgentType: "architect"},
		},
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
		if req.TargetAgentID != "arch-1234" {
			return nil
		}
		resp := &guide.RouteResponse{
			CorrelationID:       req.CorrelationID,
			Success:             true,
			RespondingAgentID:   "arch-1234",
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

func TestOrchestratorSeedKnownAgents_PreservesSnapshot(t *testing.T) {
	orch := &Orchestrator{
		knownAgents: make(map[string]*guide.AgentAnnouncement),
	}
	orch.SeedKnownAgents([]*guide.AgentAnnouncement{
		{AgentID: "arch-1234", AgentType: "architect"},
		{AgentID: "orch-1234", AgentType: "orchestrator"},
	})

	if got := orch.knownConcreteAgentIDByType("architect"); got != "arch-1234" {
		t.Fatalf("known architect id = %q, want arch-1234", got)
	}
	if got := orch.knownConcreteAgentIDByType("orchestrator"); got != "orch-1234" {
		t.Fatalf("known orchestrator id = %q, want orch-1234", got)
	}
}

func TestResolveArchitectTargetAgentID_FallsBackToRoutableTarget(t *testing.T) {
	orch := &Orchestrator{
		knownAgents: make(map[string]*guide.AgentAnnouncement),
	}
	if got := orch.resolveArchitectTargetAgentID(nil, ""); got != "architect" {
		t.Fatalf("architect route target = %q, want architect", got)
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

func TestHandleCommandApprovalHoldForwardPausesAndResumesDAG(t *testing.T) {
	orch := &Orchestrator{
		config:       Config{SessionID: "sess", AgentID: "orchestrator"},
		running:      true,
		dispatchGate: newDispatchHoldGate(),
	}

	beginPayload := &agentshared.CommandApprovalHoldRequest{
		Action:     agentshared.CommandApprovalHoldBegin,
		HoldID:     "approval_hold_1",
		SessionID:  "sess",
		DAGID:      "dag-1",
		NodeID:     "task_1",
		TaskID:     "task_1",
		PipelineID: "task_1",
	}
	beginBody, err := encodeRouteSyncInput(beginPayload)
	if err != nil {
		t.Fatalf("marshal begin payload: %v", err)
	}
	beginFwd := &guide.ForwardedRequest{
		Input:           beginBody,
		SessionID:       "sess",
		SourceAgentID:   "tester-1",
		SourceAgentName: "tester-pipeline",
		Metadata: map[string]any{
			"control_plane_kind": agentshared.ControlPlaneKindCommandApprovalHold,
		},
	}

	resultAny, err := orch.handleCommandApprovalHoldForward(context.Background(), beginFwd)
	if err != nil {
		t.Fatalf("begin approval hold: %v", err)
	}
	result, ok := resultAny.(*agentshared.CommandApprovalHoldResult)
	if !ok {
		t.Fatalf("begin result type = %T, want *CommandApprovalHoldResult", resultAny)
	}
	if result.State != "active" {
		t.Fatalf("begin result state = %q, want active", result.State)
	}
	if !orch.dispatchGate.isHeld("sess", "dag-1") {
		t.Fatal("expected dag hold to be active")
	}

	resolvePayload := &agentshared.CommandApprovalHoldRequest{
		Action:    agentshared.CommandApprovalHoldResolve,
		HoldID:    "approval_hold_1",
		SessionID: "sess",
		DAGID:     "dag-1",
	}
	resolveBody, err := encodeRouteSyncInput(resolvePayload)
	if err != nil {
		t.Fatalf("marshal resolve payload: %v", err)
	}
	resolveFwd := &guide.ForwardedRequest{
		Input:           resolveBody,
		SessionID:       "sess",
		SourceAgentID:   "guardian",
		SourceAgentName: "guardian",
		Metadata: map[string]any{
			"control_plane_kind": agentshared.ControlPlaneKindCommandApprovalHold,
		},
	}
	if _, err := orch.handleCommandApprovalHoldForward(context.Background(), resolveFwd); err != nil {
		t.Fatalf("resolve approval hold: %v", err)
	}
	if orch.dispatchGate.isHeld("sess", "dag-1") {
		t.Fatal("expected dag hold to be resolved")
	}
}

func TestHandlePlanHandoffReceiptResyncForwardPublishesUpdate(t *testing.T) {
	store, err := OpenStore(DefaultStoreConfig(filepath.Join(t.TempDir(), "orchestrator.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	orch := &Orchestrator{
		config:     Config{SessionID: "sess", AgentID: "orch-1234"},
		store:      store,
		bus:        bus,
		running:    true,
		pendingBus: make(map[string]chan *guide.Message),
		channels:   guide.NewAgentChannels("orchestrator", "orch-1234"),
	}

	receipt, duplicate, err := store.BeginPlanHandoffReceipt("sess", "plan-1", 2, "corr-handoff", "arch-1234")
	if err != nil {
		t.Fatalf("begin receipt: %v", err)
	}
	if duplicate {
		t.Fatal("expected new receipt")
	}
	receipt, err = store.UpdatePlanHandoffReceiptStage(
		"sess",
		"plan-1",
		2,
		agentshared.PlanHandoffReceiptStatusSubmitted,
		"wf_plan-1",
		"dag-1",
		3,
		2,
		"",
	)
	if err != nil {
		t.Fatalf("update receipt: %v", err)
	}
	if err := store.InsertDAGExecution("dag-1", "plan-1", "sess", "test dag", "{}", "{}", 2, 3); err != nil {
		t.Fatalf("insert dag execution: %v", err)
	}

	updates := make(chan *agentshared.PlanHandoffReceiptUpdate, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		if req.Metadata["control_plane_kind"] != agentshared.ControlPlaneKindPlanHandoffReceiptUpdate {
			return nil
		}
		if req.TargetAgentID != "arch-1234" {
			t.Fatalf("target_agent_id = %q, want arch-1234", req.TargetAgentID)
		}
		var update agentshared.PlanHandoffReceiptUpdate
		if err := json.Unmarshal([]byte(req.Input), &update); err != nil {
			return err
		}
		updates <- &update
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe update capture: %v", err)
	}
	defer sub.Unsubscribe()

	body, err := encodeRouteSyncInput(&agentshared.PlanHandoffReceiptResyncRequest{
		SessionID:     receipt.SessionID,
		PlanID:        receipt.PlanID,
		Revision:      receipt.Revision,
		CorrelationID: receipt.CorrelationID,
		Reason:        "test_resync",
	})
	if err != nil {
		t.Fatalf("marshal resync request: %v", err)
	}

	result, err := orch.handlePlanHandoffReceiptResyncForward(context.Background(), &guide.ForwardedRequest{
		Input:         body,
		SessionID:     "sess",
		SourceAgentID: "arch-1234",
		Metadata: map[string]any{
			"control_plane_kind": agentshared.ControlPlaneKindPlanHandoffReceiptResync,
		},
	})
	if err != nil {
		t.Fatalf("handle resync: %v", err)
	}
	got, ok := result.(map[string]any)
	if !ok || got["found"] != true {
		t.Fatalf("unexpected result: %#v", result)
	}

	select {
	case update := <-updates:
		if !update.Found || update.Receipt == nil {
			t.Fatalf("expected receipt update, got %+v", update)
		}
		if update.Receipt.Status != agentshared.PlanHandoffReceiptStatusRunning {
			t.Fatalf("receipt status = %s, want running", update.Receipt.Status)
		}
		if update.CorrelationID != "corr-handoff" {
			t.Fatalf("correlation_id = %q, want corr-handoff", update.CorrelationID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for receipt update publish")
	}
}
