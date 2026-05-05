package architect

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

type testBus struct {
	published []*publishedMessage
}

type publishedMessage struct {
	topic string
	msg   *guide.Message
}

func (b *testBus) Publish(topic string, msg *guide.Message) error {
	b.published = append(b.published, &publishedMessage{topic: topic, msg: msg})
	return nil
}

func (b *testBus) Subscribe(topic string, handler guide.MessageHandler) (guide.Subscription, error) {
	return testSubscription{topic: topic}, nil
}

func (b *testBus) SubscribeAsync(topic string, handler guide.MessageHandler) (guide.Subscription, error) {
	return testSubscription{topic: topic}, nil
}

func (b *testBus) Close() error                         { return nil }
func (b *testBus) CloseWithContext(_ context.Context) error { return nil }

type testSubscription struct{ topic string }

func (s testSubscription) Topic() string      { return s.topic }
func (s testSubscription) Unsubscribe() error { return nil }
func (s testSubscription) IsActive() bool     { return true }

func testArchitectControlStore(t *testing.T) *ArchitectControlStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "architect.db")
	store, err := OpenArchitectControlStore(defaultArchitectControlStoreConfig(path))
	if err != nil {
		t.Fatalf("open architect control store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testArchitectWithControlStore(t *testing.T, plan *DesignPlan) *Architect {
	t.Helper()
	store := testPlanStore(t)
	controlStore := testArchitectControlStore(t)
	store.SetMirror(controlStore.UpsertPlan)
	if plan != nil {
		if plan.sm == nil {
			plan.sm = NewPlanStateMachine(plan.ID, plan.Status)
		}
		if err := store.Upsert(plan); err != nil {
			t.Fatalf("upsert plan: %v", err)
		}
	}
	return &Architect{
		id:           "architect",
		logger:       slog.Default(),
		planStore:    store,
		controlStore: controlStore,
		steering:     shared.NewSteeringManager(),
		pendingBus:   make(map[string]*shared.PendingSyncWait),
		knownAgents:  make(map[string]*guide.AgentAnnouncement),
	}
}

func TestArchitectSeedKnownAgents_PreservesSnapshot(t *testing.T) {
	a := testArchitectWithControlStore(t, nil)
	a.SeedKnownAgents([]*guide.AgentAnnouncement{
		{AgentID: "orch-1234", AgentType: "orchestrator"},
		{AgentID: "arch-1234", AgentType: "architect"},
	})

	if got := a.knownAgentIDByType("orchestrator", ""); got != "orch-1234" {
		t.Fatalf("known orchestrator id = %q, want orch-1234", got)
	}
	if got := a.knownAgentIDByType("architect", ""); got != "arch-1234" {
		t.Fatalf("known architect id = %q, want arch-1234", got)
	}
}



func TestGuardianControlPlane_RequestGrantQueuesGuideRoutedContinuation(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-1",
		SessionID: "sess-1",
		Query:     "build the feature",
		Status:    PlanStatusReady,
		CreatedAt: now,
		UpdatedAt: now,
	}
	a := testArchitectWithControlStore(t, plan)
	a.running = true
	a.bus = &testBus{}
	a.knownAgents = map[string]*guide.AgentAnnouncement{
		"guardian": {AgentID: "guardian-1", AgentType: "guardian"},
	}
	controlPlane := newArchitectGuardianControlPlane(a)
	policy, ok := architectToolManifest().Policy("plan_acceptance")
	if !ok {
		t.Fatal("expected route_plan_acceptance policy")
	}

	_, err := controlPlane.RequestGrant(withArchitectSessionID(context.Background(), plan.SessionID), toolruntime.GuardianControlRequest{
		AgentID:           a.id,
		CorrelationID:     "corr-1",
		CapabilityScope:   architectToolManifest().CapabilityScope,
		ToolName:          "plan_acceptance",
		ToolID:            "tool-1",
		Arguments:         `{"plan_id":"plan-1","user_response":"looks good"}`,
		Input:             map[string]any{"plan_id": "plan-1", "user_response": "looks good"},
		Policy:            policy,
		PolicyFingerprint: architectToolManifest().Fingerprint(),
		Timestamp:         now,
	})
	if !errors.Is(err, skills.ErrDelegatedRequested) {
		t.Fatalf("expected delegated sentinel, got %v", err)
	}
	if plan.PendingWork == nil || plan.PendingWork.Kind != string(continuationKindGuardianApproval) {
		t.Fatalf("expected guardian approval continuation on plan, got %+v", plan.PendingWork)
	}
	record, loadErr := a.controlStore.GetContinuationByResponseCorrelation(plan.PendingWork.CorrelationID)
	if loadErr != nil {
		t.Fatalf("load continuation: %v", loadErr)
	}
	if record == nil || record.TargetAgentID != "guardian-1" {
		t.Fatalf("expected guardian continuation record, got %+v", record)
	}
	bus := a.bus.(*testBus)
	if len(bus.published) != 1 {
		t.Fatalf("expected one published route request, got %d", len(bus.published))
	}
	if bus.published[0].topic != guide.TopicGuideRequests {
		t.Fatalf("expected publish to guide requests, got %s", bus.published[0].topic)
	}
	req, ok := bus.published[0].msg.GetRouteRequest()
	if !ok || req == nil {
		t.Fatalf("expected route request payload, got %#v", bus.published[0].msg.Payload)
	}
	if req.TargetAgentID != "guardian-1" {
		t.Fatalf("expected guardian direct target, got %q", req.TargetAgentID)
	}
	if req.Metadata["direct_skill"] != "tool_execution_control" {
		t.Fatalf("expected direct skill metadata, got %+v", req.Metadata)
	}
}

func TestSubmitRequirementsResearchHandoff_QueuesAcademicDelegation(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-clarify",
		SessionID: "sess-1",
		Query:     "Build a production-ready observability platform",
		Status:    PlanStatusConsulting,
		CreatedAt: now,
		UpdatedAt: now,
	}
	plan.sm = NewPlanStateMachine(plan.ID, plan.Status)

	a := testArchitectWithControlStore(t, plan)
	a.running = true
	a.bus = &testBus{}
	a.knownAgents = map[string]*guide.AgentAnnouncement{
		"academic": {AgentID: "academic-1", AgentType: "academic"},
	}

	_, err := a.submitRequirementsResearchHandoff(
		withArchitectSessionID(context.Background(), plan.SessionID),
		&routeRequirementsResearchParams{
			PlanID:              plan.ID,
			OriginalInput:       plan.Query,
			Reason:              "The request does not define scope, constraints, or success criteria.",
			ResearchGoal:        "Clarify scope, operational constraints, and success metrics.",
			MissingRequirements: []string{"Target services", "Retention requirements", "Success metrics"},
		},
	)
	if !errors.Is(err, skills.ErrDelegatedRequested) {
		t.Fatalf("expected delegated sentinel, got %v", err)
	}
	if plan.SM().State() != PlanStatusClarifying {
		t.Fatalf("expected plan to transition to clarifying, got %s", plan.SM().State())
	}
	if plan.PendingWork == nil || plan.PendingWork.Kind != string(continuationKindAcademicHandoff) {
		t.Fatalf("expected academic handoff continuation on plan, got %+v", plan.PendingWork)
	}
	record, loadErr := a.controlStore.GetContinuationByResponseCorrelation(plan.PendingWork.CorrelationID)
	if loadErr != nil {
		t.Fatalf("load continuation: %v", loadErr)
	}
	if record == nil || record.TargetAgentID != "academic-1" {
		t.Fatalf("expected academic continuation record, got %+v", record)
	}
	if got := plan.ClarificationQuestions; len(got) != 3 {
		t.Fatalf("expected clarification questions to be persisted, got %+v", got)
	}
	bus := a.bus.(*testBus)
	if len(bus.published) != 1 {
		t.Fatalf("expected one published route request, got %d", len(bus.published))
	}
	if bus.published[0].topic != guide.TopicGuideRequests {
		t.Fatalf("expected publish to guide requests, got %s", bus.published[0].topic)
	}
	req, ok := bus.published[0].msg.GetRouteRequest()
	if !ok || req == nil {
		t.Fatalf("expected route request payload, got %#v", bus.published[0].msg.Payload)
	}
	if req.TargetAgentID != "academic-1" {
		t.Fatalf("expected academic direct target, got %q", req.TargetAgentID)
	}
	if req.Metadata["handoff_kind"] != "requirements_clarification" {
		t.Fatalf("expected requirements handoff metadata, got %+v", req.Metadata)
	}
	if visible, _ := req.Metadata["user_facing_handoff"].(bool); !visible {
		t.Fatalf("expected user-facing handoff metadata, got %+v", req.Metadata)
	}
}

func TestHandleContinuationResponse_IgnoresStreamMessages(t *testing.T) {
	a := testArchitectWithControlStore(t, nil)
	record := &ArchitectContinuation{
		ID:                    "cont-stream",
		Kind:                  continuationKindAcademicHandoff,
		State:                 continuationStatusPending,
		SessionID:             "sess-1",
		TargetAgentID:         "academic-1",
		ResponseCorrelationID: "corr-stream",
		RequestJSON:           `{"query":"clarify"}`,
		CreatedAt:             time.Now().UTC(),
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		t.Fatalf("put continuation: %v", err)
	}

	msg := &guide.Message{
		ID:            "stream-msg",
		CorrelationID: "corr-stream",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID: "corr-stream",
		},
	}
	if err := a.handleContinuationResponse(msg); err != nil {
		t.Fatalf("handleContinuationResponse(stream): %v", err)
	}

	stored, err := a.controlStore.GetContinuationByResponseCorrelation("corr-stream")
	if err != nil {
		t.Fatalf("load continuation: %v", err)
	}
	if stored == nil {
		t.Fatal("expected continuation to remain present")
	}
	if stored.State != continuationStatusPending {
		t.Fatalf("state = %s, want pending", stored.State)
	}
}

func TestHandleContinuationResponse_ErrorMessageFailsContinuation(t *testing.T) {
	a := testArchitectWithControlStore(t, nil)
	record := &ArchitectContinuation{
		ID:                    "cont-error",
		Kind:                  continuationKindAcademicHandoff,
		State:                 continuationStatusPending,
		SessionID:             "sess-1",
		TargetAgentID:         "academic-1",
		ResponseCorrelationID: "corr-error",
		RequestJSON:           `{"query":"clarify"}`,
		CreatedAt:             time.Now().UTC(),
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		t.Fatalf("put continuation: %v", err)
	}

	msg := guide.NewErrorMessage("", "corr-error", "guide", "route failed")
	msg.CorrelationID = "corr-error"
	if err := a.handleContinuationResponse(msg); err != nil {
		t.Fatalf("handleContinuationResponse(error): %v", err)
	}

	stored, err := a.controlStore.GetContinuationByResponseCorrelation("corr-error")
	if err != nil {
		t.Fatalf("load continuation: %v", err)
	}
	if stored == nil {
		t.Fatal("expected continuation to remain present")
	}
	if stored.State != continuationStatusFailed {
		t.Fatalf("state = %s, want failed", stored.State)
	}
	if stored.ErrorText != "route failed" {
		t.Fatalf("error_text = %q, want route failed", stored.ErrorText)
	}
}

func TestReconcilePendingContinuations_DefersProcessingPlanHandoffRecovery(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-processing",
		SessionID: "sess-1",
		Query:     "build the feature",
		Status:    PlanStatusOrchestrating,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:          string(continuationKindPlanHandoff),
			Status:        string(continuationStatusPending),
			TargetAgentID: "orch-1234",
			CorrelationID: "corr-processing",
			CreatedAt:     now,
			ExpiresAt:     now.Add(time.Minute),
		},
	}
	plan.sm = NewPlanStateMachine(plan.ID, plan.Status)

	a := testArchitectWithControlStore(t, plan)
	record := &ArchitectContinuation{
		ID:                    "cont-processing",
		Kind:                  continuationKindPlanHandoff,
		State:                 continuationStatusProcessing,
		PlanID:                plan.ID,
		SessionID:             plan.SessionID,
		TargetAgentID:         "orch-1234",
		ResponseCorrelationID: "corr-processing",
		RequestJSON:           `{"plan_id":"plan-processing"}`,
		CreatedAt:             now,
		ExpiresAt:             now.Add(time.Minute),
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		t.Fatalf("put continuation: %v", err)
	}

	a.reconcilePendingContinuations(nil)

	storedPlan := a.planStore.Get(plan.ID)
	if storedPlan == nil {
		t.Fatal("expected plan to remain in store")
	}
	if storedPlan.SM().State() != PlanStatusOrchestrating {
		t.Fatalf("plan state = %s, want orchestrating", storedPlan.SM().State())
	}
	if storedPlan.PendingWork == nil || storedPlan.PendingWork.CorrelationID != "corr-processing" {
		t.Fatalf("expected pending work to remain attached, got %+v", storedPlan.PendingWork)
	}

	storedRecord, err := a.controlStore.GetContinuationByResponseCorrelation("corr-processing")
	if err != nil {
		t.Fatalf("load continuation: %v", err)
	}
	if storedRecord == nil {
		t.Fatal("expected continuation record to remain present")
	}
	if storedRecord.State != continuationStatusProcessing {
		t.Fatalf("record state = %s, want processing", storedRecord.State)
	}
}
