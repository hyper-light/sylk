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
	"github.com/adalundhe/sylk/core/dag"
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

func (b *testBus) Close() error { return nil }

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
	}
}

func TestDispatchPlanExecution_QueuesAsyncHandoff(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-1",
		SessionID: "sess-1",
		Query:     "build the feature",
		Status:    PlanStatusReady,
		Tasks: []*AtomicTask{
			{ID: "task-1", Name: "Task", AgentType: "engineer", Description: "Ship it"},
		},
		Workflow:  &WorkflowDAG{TotalTasks: 1, Tasks: []*AtomicTask{{ID: "task-1"}}, DAG: &dag.DAG{}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	a := testArchitectWithControlStore(t, plan)
	a.running = true
	a.bus = &testBus{}

	result, handled := a.dispatchPlanExecution(context.Background(), &ArchitectRequest{
		ID:        "req-1",
		Intent:    IntentExecute,
		SessionID: plan.SessionID,
		Timestamp: now,
	}, plan)
	if !handled {
		t.Fatal("expected dispatch to handle ready plan")
	}
	if result == nil || result.Response == "" {
		t.Fatal("expected async handoff acknowledgment")
	}
	if plan.SM().State() != PlanStatusOrchestrating {
		t.Fatalf("expected plan to transition to orchestrating, got %s", plan.SM().State())
	}
	if plan.PendingWork == nil || plan.PendingWork.Kind != string(continuationKindPlanHandoff) {
		t.Fatalf("expected pending handoff continuation, got %+v", plan.PendingWork)
	}
	record, err := a.controlStore.GetContinuationByResponseCorrelation(plan.PendingWork.CorrelationID)
	if err != nil {
		t.Fatalf("load continuation: %v", err)
	}
	if record == nil || record.Kind != continuationKindPlanHandoff {
		t.Fatalf("expected handoff continuation record, got %+v", record)
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
	if req.TargetAgentID != "orchestrator" {
		t.Fatalf("expected orchestrator target, got %q", req.TargetAgentID)
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
	policy, ok := architectToolManifest().Policy("route_plan_acceptance")
	if !ok {
		t.Fatal("expected route_plan_acceptance policy")
	}

	_, err := controlPlane.RequestGrant(withArchitectSessionID(context.Background(), plan.SessionID), toolruntime.GuardianControlRequest{
		AgentID:           a.id,
		CorrelationID:     "corr-1",
		CapabilityScope:   architectToolManifest().CapabilityScope,
		ToolName:          "route_plan_acceptance",
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
