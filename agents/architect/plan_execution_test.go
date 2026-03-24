package architect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/providers"
)

func testArchitectWithStore(t *testing.T, plans ...*DesignPlan) *Architect {
	t.Helper()
	store := testPlanStore(t)
	for _, p := range plans {
		if p.sm == nil {
			p.sm = NewPlanStateMachine(p.ID, p.Status)
		}
		_ = store.Upsert(p)
	}
	return &Architect{
		planStore:   store,
		logger:      slog.Default(),
		pendingBus:  make(map[string]chan *guide.Message),
		knownAgents: make(map[string]*guide.AgentAnnouncement),
	}
}

type captureConversationPlanner struct {
	lastRequest plannerConversationRequest
	response    string
}

func (p *captureConversationPlanner) AnalyzeRequirements(context.Context, string, map[string]any) (*Requirements, error) {
	return nil, errors.New("unexpected AnalyzeRequirements call")
}

func (p *captureConversationPlanner) DesignArchitecture(context.Context, *Requirements, *CodebasePatterns) (*SolutionArchitecture, error) {
	return nil, errors.New("unexpected DesignArchitecture call")
}

func (p *captureConversationPlanner) GenerateTasks(context.Context, *SolutionArchitecture, *PlanConstraints) ([]*AtomicTask, error) {
	return nil, errors.New("unexpected GenerateTasks call")
}

func (p *captureConversationPlanner) ComposeUserResponse(_ context.Context, request plannerConversationRequest) (string, error) {
	p.lastRequest = request
	if strings.TrimSpace(p.response) != "" {
		return p.response, nil
	}
	return "planner response", nil
}

func (p *captureConversationPlanner) CompleteForToolLoop(context.Context, *providers.Request, string, func(string)) (*providers.Response, error) {
	return nil, errors.New("unexpected CompleteForToolLoop call")
}

func (p *captureConversationPlanner) ConversationSystemPrompt() string {
	return "test planner prompt"
}

func unwrapConversationResult(t *testing.T, result any) *ConversationResult {
	t.Helper()
	conv, ok := unwrapArchitectResult(result).(*ConversationResult)
	if !ok {
		t.Fatalf("conversation result type = %T, want *ConversationResult", unwrapArchitectResult(result))
	}
	return conv
}

func TestLatestReadyPlan_NoPlans(t *testing.T) {
	a := testArchitectWithStore(t)
	if a.latestReadyPlan("sess1") != nil {
		t.Error("expected nil with no plans")
	}
}

func TestLatestReadyPlan_EmptySessionID(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "a", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now},
	)
	if a.latestReadyPlan("") != nil {
		t.Error("expected nil with empty session ID")
	}
}

func TestLatestReadyPlan_NoneReady(t *testing.T) {
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "a", SessionID: "sess1", Status: PlanStatusAnalyzing, UpdatedAt: time.Now()},
		&DesignPlan{ID: "b", SessionID: "sess1", Status: PlanStatusDesigning, UpdatedAt: time.Now()},
	)
	if a.latestReadyPlan("sess1") != nil {
		t.Error("expected nil with no ready plans")
	}
}

func TestLatestReadyPlan_FiltersSession(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "other", SessionID: "sess-old", Status: PlanStatusReady, UpdatedAt: now},
		&DesignPlan{ID: "mine", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now},
	)

	plan := a.latestReadyPlan("sess1")
	if plan == nil || plan.ID != "mine" {
		t.Errorf("expected 'mine', got %v", plan)
	}

	plan = a.latestReadyPlan("sess-old")
	if plan == nil || plan.ID != "other" {
		t.Errorf("expected 'other', got %v", plan)
	}

	if a.latestReadyPlan("sess-unknown") != nil {
		t.Error("expected nil for unknown session")
	}
}

func TestLatestReadyPlan_SelectsMostRecent(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "old", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now.Add(-5 * time.Minute)},
		&DesignPlan{ID: "new", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now.Add(-1 * time.Minute)},
	)

	plan := a.latestReadyPlan("sess1")
	if plan == nil || plan.ID != "new" {
		t.Errorf("expected 'new', got %v", plan)
	}
}

func TestLatestStalledPlan_FindsConsultingPlan(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "stalled", SessionID: "sess1", Status: PlanStatusConsulting, UpdatedAt: now},
	)
	plan := a.latestStalledPlan("sess1")
	if plan == nil || plan.ID != "stalled" {
		t.Errorf("expected stalled plan, got %v", plan)
	}
}

func TestLatestStalledPlan_IgnoresReadyPlan(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "ready", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now},
	)
	if a.latestStalledPlan("sess1") != nil {
		t.Error("expected nil for ready plan")
	}
}

func TestLatestStalledPlan_IgnoresClarifyingPlan(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "clarifying", SessionID: "sess1", Status: PlanStatusClarifying, UpdatedAt: now},
	)
	if a.latestStalledPlan("sess1") != nil {
		t.Error("expected nil for clarifying plan")
	}
}

func TestLatestStalledPlan_IgnoresOldPlan(t *testing.T) {
	old := time.Now().Add(-10 * time.Minute)
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "old", SessionID: "sess1", Status: PlanStatusConsulting, UpdatedAt: old},
	)
	if a.latestStalledPlan("sess1") != nil {
		t.Error("expected nil for plan older than stalledPlanMaxAge")
	}
}

func TestLatestStalledPlanForRequest_FiltersByCorrelation(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{
			ID:                   "wanted",
			SessionID:            "sess1",
			Status:               PlanStatusConsulting,
			RequestCorrelationID: "corr-1",
			UpdatedAt:            now,
		},
		&DesignPlan{
			ID:                   "other",
			SessionID:            "sess1",
			Status:               PlanStatusGenerating,
			RequestCorrelationID: "corr-2",
			UpdatedAt:            now.Add(time.Second),
		},
	)
	plan := a.latestStalledPlanForRequest("sess1", "corr-1")
	if plan == nil || plan.ID != "wanted" {
		t.Fatalf("latestStalledPlanForRequest = %v, want wanted", plan)
	}
}

func TestLatestActivePendingPlan_SelectsMostRecent(t *testing.T) {
	now := time.Now().UTC()
	a := testArchitectWithStore(t,
		&DesignPlan{
			ID:        "older",
			SessionID: "sess1",
			Status:    PlanStatusReady,
			UpdatedAt: now.Add(-time.Minute),
			PendingWork: &PendingContinuation{
				Kind:      string(continuationKindGuardianApproval),
				Status:    string(continuationStatusPending),
				Message:   "older pending",
				ExpiresAt: now.Add(time.Minute),
			},
		},
		&DesignPlan{
			ID:        "newer",
			SessionID: "sess1",
			Status:    PlanStatusReady,
			UpdatedAt: now,
			PendingWork: &PendingContinuation{
				Kind:      string(continuationKindAcceptanceEval),
				Status:    string(continuationStatusPending),
				Message:   "newer pending",
				ExpiresAt: now.Add(time.Minute),
			},
		},
	)

	plan := a.latestActivePendingPlan("sess1")
	if plan == nil || plan.ID != "newer" {
		t.Fatalf("latestActivePendingPlan = %v, want newer", plan)
	}
}

func TestLatestActivePendingPlan_IgnoresExpiredPendingWork(t *testing.T) {
	now := time.Now().UTC()
	a := testArchitectWithStore(t,
		&DesignPlan{
			ID:        "expired",
			SessionID: "sess1",
			Status:    PlanStatusReady,
			UpdatedAt: now,
			PendingWork: &PendingContinuation{
				Kind:      string(continuationKindGuardianApproval),
				Status:    string(continuationStatusPending),
				Message:   "expired pending",
				ExpiresAt: now.Add(-time.Second),
			},
		},
	)

	if plan := a.latestActivePendingPlan("sess1"); plan != nil {
		t.Fatalf("latestActivePendingPlan = %v, want nil", plan)
	}
}

func TestHandleExecute_ReturnsPendingMessageWhenPlanWorkIsInFlight(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-1",
		SessionID: "sess1",
		Status:    PlanStatusReady,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:      string(continuationKindGuardianApproval),
			Status:    string(continuationStatusPending),
			Message:   "Guardian is reviewing the plan response.",
			ExpiresAt: now.Add(time.Minute),
		},
	}
	a := testArchitectWithStore(t, plan)

	result, err := a.handleExecute(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: "sess1",
	})
	if err != nil {
		t.Fatalf("handleExecute error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleExecute result type = %T, want *ConversationResult", result)
	}
	if conv.Response != "Guardian is reviewing the plan response." {
		t.Fatalf("response = %q, want pending message", conv.Response)
	}
	if conv.Intent != IntentExecute {
		t.Fatalf("intent = %s, want execute", conv.Intent)
	}
}

func TestHandleConversation_UsesMetadataPlanIDAcrossSessionDrift(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-convo",
		SessionID: "sess-plan",
		Status:    PlanStatusReady,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:      string(continuationKindGuardianApproval),
			Status:    string(continuationStatusPending),
			Message:   "Guardian is still reviewing the plan.",
			ExpiresAt: now.Add(time.Minute),
		},
	}
	a := testArchitectWithStore(t, plan)

	result, err := a.handleConversation(context.Background(), &guide.ForwardedRequest{
		Input:     "any updates?",
		SessionID: "sess-drifted",
		Metadata:  map[string]any{"plan_id": plan.ID},
		Intent:    guide.IntentChat,
	})
	if err != nil {
		t.Fatalf("handleConversation error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleConversation result type = %T, want *ConversationResult", result)
	}
	if conv.Response != "Guardian is still reviewing the plan." {
		t.Fatalf("response = %q, want pending message", conv.Response)
	}
}

func TestHandleConversation_ClearsExpiredPendingWorkAndCarriesReadyPlanIntoPlanner(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-expired-pending",
		SessionID: "sess-plan",
		Query:     "build the feature",
		Status:    PlanStatusReady,
		UpdatedAt: now,
		Tasks: []*AtomicTask{
			{Name: "Wire the endpoint", AgentType: "engineer", Description: "Implement the initial HTTP surface."},
		},
		PendingWork: &PendingContinuation{
			Kind:      string(continuationKindAcceptanceEval),
			Status:    string(continuationStatusPending),
			Message:   "I'm reviewing your response against the current plan now. I'll update you shortly.",
			ExpiresAt: now.Add(-time.Second),
		},
	}
	a := testArchitectWithStore(t, plan)
	planner := &captureConversationPlanner{response: "planner saw the recovered plan"}
	a.config.EnableLLM = true
	a.planner = planner

	result, err := a.handleConversation(context.Background(), &guide.ForwardedRequest{
		Input:     "can you remind me what we planned?",
		SessionID: plan.SessionID,
		Intent:    guide.IntentChat,
	})
	if err != nil {
		t.Fatalf("handleConversation error = %v", err)
	}
	conv := unwrapConversationResult(t, result)
	if conv.Response != "planner saw the recovered plan" {
		t.Fatalf("response = %q, want planner response", conv.Response)
	}
	if plan.PendingWork != nil {
		t.Fatalf("expected stale pending work to clear, got %+v", plan.PendingWork)
	}
	if planner.lastRequest.Mode != plannerConversationModeExistingReady {
		t.Fatalf("mode = %q, want %q", planner.lastRequest.Mode, plannerConversationModeExistingReady)
	}
	if planner.lastRequest.PlanID != plan.ID {
		t.Fatalf("plan_id = %q, want %q", planner.lastRequest.PlanID, plan.ID)
	}
	if planner.lastRequest.PriorQuery != plan.Query {
		t.Fatalf("prior_query = %q, want %q", planner.lastRequest.PriorQuery, plan.Query)
	}
	if !strings.Contains(planner.lastRequest.PlanSummary, "Wire the endpoint") {
		t.Fatalf("plan_summary = %q, want task summary from ready plan", planner.lastRequest.PlanSummary)
	}
}

func TestHandleConversation_ClearsExpiredMetadataPendingWorkAcrossSessionDrift(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-expired-metadata",
		SessionID: "sess-plan",
		Status:    PlanStatusReady,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:      string(continuationKindAcceptanceEval),
			Status:    string(continuationStatusPending),
			Message:   "I'm reviewing your response against the current plan now. I'll update you shortly.",
			ExpiresAt: now.Add(-time.Second),
		},
	}
	a := testArchitectWithStore(t, plan)

	result, err := a.handleConversation(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: "sess-drifted",
		Metadata:  map[string]any{"plan_id": plan.ID},
		Intent:    guide.IntentChat,
	})
	if err != nil {
		t.Fatalf("handleConversation error = %v", err)
	}
	conv := unwrapConversationResult(t, result)
	if strings.Contains(conv.Response, "reviewing your response against the current plan") {
		t.Fatalf("response = %q, want stale pending message to be cleared", conv.Response)
	}
	if !strings.Contains(conv.Response, "tool runtime is not configured") {
		t.Fatalf("response = %q, want route_plan_acceptance fallback after clearing stale pending", conv.Response)
	}
	if plan.PendingWork != nil {
		t.Fatalf("expected stale pending work to clear, got %+v", plan.PendingWork)
	}
}

func TestHandleConversation_UsesPlannerForUniqueRecoveredReadyPlanAcrossSessionDrift(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-unique-recovered",
		SessionID: "sess-original",
		Query:     "build the feature",
		Status:    PlanStatusReady,
		UpdatedAt: now,
		Tasks: []*AtomicTask{
			{Name: "Add orchestration", AgentType: "orchestrator", Description: "Coordinate the build workflow."},
		},
	}
	a := testArchitectWithStore(t, plan)
	planner := &captureConversationPlanner{response: "planner kept continuity"}
	a.config.EnableLLM = true
	a.planner = planner

	result, err := a.handleConversation(context.Background(), &guide.ForwardedRequest{
		Input:     "what changed since that plan?",
		SessionID: "sess-drifted",
		Intent:    guide.IntentChat,
	})
	if err != nil {
		t.Fatalf("handleConversation error = %v", err)
	}
	conv := unwrapConversationResult(t, result)
	if conv.Response != "planner kept continuity" {
		t.Fatalf("response = %q, want planner response", conv.Response)
	}
	if conv.Directive != nil {
		t.Fatalf("directive = %+v, want nil so the old plan is not silently re-armed", conv.Directive)
	}
	if planner.lastRequest.Mode != plannerConversationModeExistingReady {
		t.Fatalf("mode = %q, want %q", planner.lastRequest.Mode, plannerConversationModeExistingReady)
	}
	if planner.lastRequest.PlanID != plan.ID {
		t.Fatalf("plan_id = %q, want %q", planner.lastRequest.PlanID, plan.ID)
	}
	if planner.lastRequest.PriorQuery != plan.Query {
		t.Fatalf("prior_query = %q, want %q", planner.lastRequest.PriorQuery, plan.Query)
	}
	if !strings.Contains(planner.lastRequest.PlanSummary, "Add orchestration") {
		t.Fatalf("plan_summary = %q, want recovered plan summary", planner.lastRequest.PlanSummary)
	}
}

func TestHandleConversation_ExplicitResumeSignalDispatchesRecoveredReadyPlan(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-resume",
		SessionID: "sess-plan",
		Query:     "build the feature",
		Status:    PlanStatusReady,
		UpdatedAt: now,
	}
	a := testArchitectWithStore(t, plan)

	result, err := a.handleConversation(context.Background(), &guide.ForwardedRequest{
		Input:     "resume that plan",
		SessionID: "sess-plan",
		Intent:    guide.IntentChat,
	})
	if err != nil {
		t.Fatalf("handleConversation error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleConversation result type = %T, want *ConversationResult", result)
	}
	if conv.Intent != IntentExecute {
		t.Fatalf("intent = %s, want execute", conv.Intent)
	}
	if conv.Response != "I have a plan ready, but I can't dispatch it right now — the orchestration bus isn't available." {
		t.Fatalf("response = %q, want execute fallback", conv.Response)
	}
}

func TestHandleExecute_RehydratesOrphanedPendingContinuationFromControlStore(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-orphan",
		SessionID: "sess1",
		Status:    PlanStatusReady,
		UpdatedAt: now,
	}
	a := testArchitectWithControlStore(t, plan)
	record := &ArchitectContinuation{
		ID:                    "cont-1",
		Kind:                  continuationKindGuardianApproval,
		State:                 continuationStatusPending,
		PlanID:                plan.ID,
		SessionID:             plan.SessionID,
		TargetAgentID:         "guardian",
		ResponseCorrelationID: "corr-1",
		CreatedAt:             now.Add(5 * time.Second),
		ExpiresAt:             now.Add(time.Minute),
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		t.Fatalf("put continuation: %v", err)
	}

	result, err := a.handleExecute(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: "sess1",
	})
	if err != nil {
		t.Fatalf("handleExecute error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleExecute result type = %T, want *ConversationResult", result)
	}
	if conv.Response != "The latest plan response is still going through Guardian approval. I'll update you shortly." {
		t.Fatalf("response = %q, want guardian pending message", conv.Response)
	}
	stored := a.planStore.Get(plan.ID)
	if stored == nil || stored.PendingWork == nil {
		t.Fatal("expected orphaned continuation to be reattached to plan")
	}
	if stored.PendingWork.CorrelationID != "corr-1" {
		t.Fatalf("correlation_id = %q, want corr-1", stored.PendingWork.CorrelationID)
	}
}

func TestHandleExecute_UsesMetadataPlanIDAcrossSessionDrift(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-metadata",
		SessionID: "sess-plan",
		Query:     "build the feature",
		Status:    PlanStatusReady,
		CreatedAt: now,
		UpdatedAt: now,
	}
	a := testArchitectWithStore(t, plan)

	result, err := a.handleExecute(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: "sess-drifted",
		Metadata:  map[string]any{"plan_id": plan.ID},
	})
	if err != nil {
		t.Fatalf("handleExecute error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleExecute result type = %T, want *ConversationResult", result)
	}
	if !strings.Contains(conv.Response, "can't dispatch it right now") {
		t.Fatalf("response = %q, want dispatch fallback proving plan recovery", conv.Response)
	}
	if conv.Intent != IntentExecute {
		t.Fatalf("intent = %s, want execute", conv.Intent)
	}
}

func TestHandleExecute_RecoversUniqueRecentReadyPlanAcrossSessionDrift(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-unique",
		SessionID: "sess-plan",
		Query:     "build the feature",
		Status:    PlanStatusReady,
		CreatedAt: now,
		UpdatedAt: now,
	}
	a := testArchitectWithStore(t, plan)

	result, err := a.handleExecute(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: "sess-drifted",
	})
	if err != nil {
		t.Fatalf("handleExecute error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleExecute result type = %T, want *ConversationResult", result)
	}
	if !strings.Contains(conv.Response, "can't dispatch it right now") {
		t.Fatalf("response = %q, want dispatch fallback proving unique-plan recovery", conv.Response)
	}
}

func TestHandleExecute_DoesNotGuessAcrossSessionDriftWhenMultipleRecentPlansExist(t *testing.T) {
	now := time.Now().UTC()
	a := testArchitectWithStore(t,
		&DesignPlan{
			ID:        "plan-a",
			SessionID: "sess-a",
			Query:     "build feature a",
			Status:    PlanStatusReady,
			CreatedAt: now,
			UpdatedAt: now,
		},
		&DesignPlan{
			ID:        "plan-b",
			SessionID: "sess-b",
			Query:     "build feature b",
			Status:    PlanStatusReady,
			CreatedAt: now,
			UpdatedAt: now.Add(time.Second),
		},
	)

	result, err := a.handleExecute(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: "sess-drifted",
	})
	if err != nil {
		t.Fatalf("handleExecute error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleExecute result type = %T, want *ConversationResult", result)
	}
	if conv.Response != "There's no ready plan to execute. Describe what you'd like to build and I'll create one." {
		t.Fatalf("response = %q, want no-ready message when recovery is ambiguous", conv.Response)
	}
}

func TestHandleExecute_RecoversExpiredPlanHandoffAndRedispatches(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-1",
		SessionID: "sess1",
		Query:     "build the feature",
		Status:    PlanStatusOrchestrating,
		Tasks: []*AtomicTask{
			{ID: "task-1", Name: "Task", AgentType: "engineer", Description: "Ship it"},
		},
		Workflow:  &WorkflowDAG{TotalTasks: 1, Tasks: []*AtomicTask{{ID: "task-1"}}, DAG: &dag.DAG{}},
		CreatedAt: now,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:          string(continuationKindPlanHandoff),
			Status:        string(continuationStatusPending),
			TargetAgentID: "orch-1234",
			CorrelationID: "corr-expired",
			Message:       "handoff queued",
			CreatedAt:     now.Add(-2 * time.Minute),
			ExpiresAt:     now.Add(-time.Minute),
		},
	}
	plan.sm = NewPlanStateMachine(plan.ID, plan.Status)

	a := testArchitectWithControlStore(t, plan)
	a.running = true
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })
	a.bus = bus
	a.channels = guide.NewAgentChannels("architect", a.id)
	a.knownAgents = map[string]*guide.AgentAnnouncement{
		"orch-1234": {AgentID: "orch-1234", AgentType: "orchestrator"},
	}

	respSub, err := bus.SubscribeAsync(a.channels.Responses, a.handleBusResponse)
	if err != nil {
		t.Fatalf("subscribe architect responses: %v", err)
	}
	defer respSub.Unsubscribe()

	requestKinds := make(chan string, 4)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		if req.Metadata["control_plane_kind"] != agentshared.ControlPlaneKindPlanHandoffStatus {
			requestKinds <- "dispatch"
			return nil
		}
		requestKinds <- "lookup"
		resp := &guide.RouteResponse{
			CorrelationID:       req.CorrelationID,
			Success:             true,
			RespondingAgentID:   "orchestrator",
			RespondingAgentName: "orchestrator",
			Data: &agentshared.PlanHandoffStatusResponse{
				Found: false,
			},
		}
		return bus.Publish(a.channels.Responses, guide.NewResponseMessage("resp_lookup", resp))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	record := &ArchitectContinuation{
		ID:                    "cont-expired",
		Kind:                  continuationKindPlanHandoff,
		State:                 continuationStatusPending,
		PlanID:                plan.ID,
		SessionID:             plan.SessionID,
		TargetAgentID:         "orch-1234",
		ResponseCorrelationID: "corr-expired",
		RequestJSON:           `{"plan_id":"plan-1"}`,
		CreatedAt:             now.Add(-2 * time.Minute),
		ExpiresAt:             now.Add(-time.Minute),
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		t.Fatalf("put continuation: %v", err)
	}

	result, err := a.handleExecute(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: plan.SessionID,
	})
	if err != nil {
		t.Fatalf("handleExecute error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleExecute result type = %T, want *ConversationResult", result)
	}
	if !strings.Contains(conv.Response, "Plan handoff queued") {
		t.Fatalf("response = %q, want redispatch acknowledgment", conv.Response)
	}

	stored := a.planStore.Get(plan.ID)
	if stored == nil {
		t.Fatal("expected plan to remain present")
	}
	if stored.SM().State() != PlanStatusOrchestrating {
		t.Fatalf("plan state = %s, want orchestrating after redispatch", stored.SM().State())
	}
	if stored.PendingWork == nil || stored.PendingWork.CorrelationID == "corr-expired" {
		t.Fatalf("expected new pending handoff continuation, got %+v", stored.PendingWork)
	}

	expiredRecord, err := a.controlStore.GetContinuationByResponseCorrelation("corr-expired")
	if err != nil {
		t.Fatalf("load expired continuation: %v", err)
	}
	if expiredRecord == nil || expiredRecord.State != continuationStatusFailed {
		t.Fatalf("expired continuation = %+v, want failed", expiredRecord)
	}

	seen := map[string]int{}
	deadline := time.After(time.Second)
	for len(seen) < 2 {
		select {
		case kind := <-requestKinds:
			seen[kind]++
		case <-deadline:
			t.Fatalf("expected lookup + redispatch route requests, saw %+v", seen)
		}
	}
}

func TestHandleExecute_PromotesExecutingFromDurableHandoffReceipt(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-1",
		SessionID: "sess1",
		Query:     "build the feature",
		Revision:  2,
		Status:    PlanStatusOrchestrating,
		CreatedAt: now,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:          string(continuationKindPlanHandoff),
			Status:        string(continuationStatusPending),
			TargetAgentID: "orch-1234",
			CorrelationID: "corr-stale",
			Message:       "handoff queued",
			CreatedAt:     now.Add(-2 * time.Minute),
			ExpiresAt:     now.Add(-time.Minute),
		},
	}
	plan.sm = NewPlanStateMachine(plan.ID, plan.Status)

	a := testArchitectWithControlStore(t, plan)
	a.running = true
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })
	a.bus = bus
	a.channels = guide.NewAgentChannels("architect", a.id)
	a.knownAgents = map[string]*guide.AgentAnnouncement{
		"orch-1234": {AgentID: "orch-1234", AgentType: "orchestrator"},
	}

	respSub, err := bus.SubscribeAsync(a.channels.Responses, a.handleBusResponse)
	if err != nil {
		t.Fatalf("subscribe architect responses: %v", err)
	}
	defer respSub.Unsubscribe()

	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		if req.TargetAgentID != "orch-1234" {
			return nil
		}
		resp := &guide.RouteResponse{
			CorrelationID:       req.CorrelationID,
			Success:             true,
			RespondingAgentID:   "orchestrator",
			RespondingAgentName: "orchestrator",
			Data: &agentshared.PlanHandoffStatusResponse{
				Found: true,
				Receipt: &agentshared.PlanHandoffReceipt{
					ReceiptID:   "handoff_1",
					SessionID:   plan.SessionID,
					PlanID:      plan.ID,
					Revision:    plan.Revision,
					Attempt:     1,
					Status:      agentshared.PlanHandoffReceiptStatusRunning,
					DAGID:       "dag-1",
					WorkflowID:  "wf_plan-1",
					SubmittedAt: now,
					RunningAt:   now,
				},
			},
		}
		return bus.Publish(a.channels.Responses, guide.NewResponseMessage("resp_1", resp))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	result, err := a.handleExecute(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: plan.SessionID,
	})
	if err != nil {
		t.Fatalf("handleExecute error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleExecute result type = %T, want *ConversationResult", result)
	}
	if conv.Response != "That plan is already executing in the orchestrator." {
		t.Fatalf("response = %q, want executing message", conv.Response)
	}

	stored := a.planStore.Get(plan.ID)
	if stored == nil {
		t.Fatal("expected plan to remain present")
	}
	if stored.SM().State() != PlanStatusExecuting {
		t.Fatalf("plan state = %s, want executing", stored.SM().State())
	}
	if stored.PendingWork != nil {
		t.Fatalf("expected pending work to clear, got %+v", stored.PendingWork)
	}
}

func TestHandlePlanHandoffReceiptUpdate_PromotesExecutingPlan(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-update",
		SessionID: "sess1",
		Query:     "build the feature",
		Revision:  3,
		Status:    PlanStatusReady,
		CreatedAt: now,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:          string(continuationKindPlanHandoff),
			Status:        string(continuationStatusPending),
			TargetAgentID: "orch-1234",
			CorrelationID: "corr-update",
			Message:       "handoff queued",
		},
	}
	plan.sm = NewPlanStateMachine(plan.ID, plan.Status)

	a := testArchitectWithControlStore(t, plan)
	record := &ArchitectContinuation{
		ID:                    "cont-update",
		Kind:                  continuationKindPlanHandoff,
		State:                 continuationStatusPending,
		PlanID:                plan.ID,
		SessionID:             plan.SessionID,
		TargetAgentID:         "orch-1234",
		ResponseCorrelationID: "corr-update",
		RequestJSON:           `{"plan_id":"plan-update"}`,
		CreatedAt:             now,
		ExpiresAt:             now.Add(time.Minute),
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		t.Fatalf("put continuation: %v", err)
	}

	body, err := json.Marshal(&agentshared.PlanHandoffReceiptUpdate{
		SessionID:     plan.SessionID,
		PlanID:        plan.ID,
		Revision:      plan.Revision,
		CorrelationID: "corr-update",
		Found:         true,
		Receipt: &agentshared.PlanHandoffReceipt{
			ReceiptID:   "handoff-1",
			SessionID:   plan.SessionID,
			PlanID:      plan.ID,
			Revision:    plan.Revision,
			Status:      agentshared.PlanHandoffReceiptStatusRunning,
			DAGID:       "dag-1",
			WorkflowID:  "wf_plan-update",
			SubmittedAt: now,
			RunningAt:   now,
		},
		Reason: "running",
	})
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}

	resultAny, handled, err := a.handleControlPlaneForward(context.Background(), &guide.ForwardedRequest{
		Input:     string(body),
		SessionID: plan.SessionID,
		Metadata: map[string]any{
			"control_plane_kind": agentshared.ControlPlaneKindPlanHandoffReceiptUpdate,
		},
	})
	if err != nil {
		t.Fatalf("handle control plane: %v", err)
	}
	if !handled {
		t.Fatal("expected update to be handled")
	}
	result, ok := resultAny.(map[string]any)
	if !ok || result["applied"] != true {
		t.Fatalf("unexpected result: %#v", resultAny)
	}

	stored := a.planStore.Get(plan.ID)
	if stored == nil {
		t.Fatal("expected plan to remain present")
	}
	if stored.SM().State() != PlanStatusExecuting {
		t.Fatalf("plan state = %s, want executing", stored.SM().State())
	}
	if stored.PendingWork != nil {
		t.Fatalf("expected pending work to clear, got %+v", stored.PendingWork)
	}

	storedRecord, err := a.controlStore.GetContinuationByResponseCorrelation("corr-update")
	if err != nil {
		t.Fatalf("load continuation: %v", err)
	}
	if storedRecord == nil || storedRecord.State != continuationStatusCompleted {
		t.Fatalf("continuation = %+v, want completed", storedRecord)
	}
}

func TestApplyFoundPlanHandoffReceiptUpdate_PublishesExecutingNotificationWhileContinuationPending(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-update-pending",
		SessionID: "sess1",
		Query:     "build the feature",
		Revision:  3,
		Status:    PlanStatusReady,
		CreatedAt: now,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:          string(continuationKindPlanHandoff),
			Status:        string(continuationStatusPending),
			TargetAgentID: "orch-1234",
			CorrelationID: "corr-pending",
			Message:       "handoff queued",
		},
	}
	plan.sm = NewPlanStateMachine(plan.ID, plan.Status)

	a := testArchitectWithControlStore(t, plan)
	a.bus = guide.NewChannelBus(guide.DefaultChannelBusConfig())
	a.channels = guide.NewAgentChannels("architect", "architect")
	notifications := make(chan string, 1)
	sub, err := a.bus.Subscribe(a.channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventPush {
			return nil
		}
		push, ok := stream.Event.Data.(*guide.AgentPush)
		if !ok || push == nil {
			return nil
		}
		notifications <- push.Content
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	record := &ArchitectContinuation{
		ID:                    "cont-pending",
		Kind:                  continuationKindPlanHandoff,
		State:                 continuationStatusPending,
		PlanID:                plan.ID,
		SessionID:             plan.SessionID,
		TargetAgentID:         "orch-1234",
		ResponseCorrelationID: "corr-pending",
		RequestJSON:           `{"plan_id":"plan-update-pending"}`,
		CreatedAt:             now,
		ExpiresAt:             now.Add(time.Minute),
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		t.Fatalf("put continuation: %v", err)
	}

	a.applyFoundPlanHandoffReceiptUpdate(plan, "corr-pending", &agentshared.PlanHandoffReceiptUpdate{
		SessionID:     plan.SessionID,
		PlanID:        plan.ID,
		Revision:      plan.Revision,
		CorrelationID: "corr-pending",
		Found:         true,
		Receipt: &agentshared.PlanHandoffReceipt{
			ReceiptID:   "handoff-pending",
			SessionID:   plan.SessionID,
			PlanID:      plan.ID,
			Revision:    plan.Revision,
			Status:      agentshared.PlanHandoffReceiptStatusRunning,
			DAGID:       "dag-pending",
			WorkflowID:  "wf_plan-update-pending",
			SubmittedAt: now,
			RunningAt:   now,
		},
		Reason: "running",
	})

	select {
	case got := <-notifications:
		want := "Plan dispatched to the orchestrator. DAG dag-pending is now running."
		if got != want {
			t.Fatalf("notification = %q, want %q", got, want)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected executing notification")
	}
}

func TestPlanHandoffContinuationThenReceiptUpdate_PublishesExecutingNotificationOnce(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-update-completed",
		SessionID: "sess1",
		Query:     "build the feature",
		Revision:  3,
		Status:    PlanStatusOrchestrating,
		CreatedAt: now,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:          string(continuationKindPlanHandoff),
			Status:        string(continuationStatusPending),
			TargetAgentID: "orch-1234",
			CorrelationID: "corr-completed",
			Message:       "handoff queued",
		},
	}
	plan.sm = NewPlanStateMachine(plan.ID, plan.Status)

	a := testArchitectWithControlStore(t, plan)
	a.bus = guide.NewChannelBus(guide.DefaultChannelBusConfig())
	a.channels = guide.NewAgentChannels("architect", "architect")
	notifications := make(chan string, 1)
	sub, err := a.bus.Subscribe(a.channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventPush {
			return nil
		}
		push, ok := stream.Event.Data.(*guide.AgentPush)
		if !ok || push == nil {
			return nil
		}
		notifications <- push.Content
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	record := &ArchitectContinuation{
		ID:                    "cont-completed",
		Kind:                  continuationKindPlanHandoff,
		State:                 continuationStatusProcessing,
		PlanID:                plan.ID,
		SessionID:             plan.SessionID,
		TargetAgentID:         "orch-1234",
		ResponseCorrelationID: "corr-completed",
		RequestJSON:           `{"plan_id":"plan-update-completed"}`,
		CreatedAt:             now,
		ExpiresAt:             now.Add(time.Minute),
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		t.Fatalf("put continuation: %v", err)
	}

	msg := guide.NewResponseMessage("resp-completed", &guide.RouteResponse{
		CorrelationID:       "corr-completed",
		Success:             true,
		RespondingAgentID:   "orch-1234",
		RespondingAgentName: "orchestrator",
		Data: map[string]any{
			"response": "DAG dag-completed is now running.",
		},
	})
	if err := a.handlePlanHandoffContinuation(msg, record); err != nil {
		t.Fatalf("handlePlanHandoffContinuation: %v", err)
	}

	a.applyFoundPlanHandoffReceiptUpdate(plan, "corr-completed", &agentshared.PlanHandoffReceiptUpdate{
		SessionID:     plan.SessionID,
		PlanID:        plan.ID,
		Revision:      plan.Revision,
		CorrelationID: "corr-completed",
		Found:         true,
		Receipt: &agentshared.PlanHandoffReceipt{
			ReceiptID:   "handoff-completed",
			SessionID:   plan.SessionID,
			PlanID:      plan.ID,
			Revision:    plan.Revision,
			Status:      agentshared.PlanHandoffReceiptStatusRunning,
			DAGID:       "dag-completed",
			WorkflowID:  "wf_plan-update-completed",
			SubmittedAt: now,
			RunningAt:   now,
		},
		Reason: "running",
	})

	select {
	case got := <-notifications:
		want := "Plan dispatched to the orchestrator. DAG dag-completed is now running."
		if got != want {
			t.Fatalf("notification = %q, want %q", got, want)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected initial executing notification")
	}

	select {
	case got := <-notifications:
		t.Fatalf("unexpected duplicate notification: %q", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRequestOpenPlanHandoffReceiptResyncs_PublishesRequests(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-resync",
		SessionID: "sess1",
		Revision:  1,
		Status:    PlanStatusOrchestrating,
		CreatedAt: now,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:          string(continuationKindPlanHandoff),
			Status:        string(continuationStatusPending),
			TargetAgentID: "orch-1234",
			CorrelationID: "corr-resync",
		},
	}
	plan.sm = NewPlanStateMachine(plan.ID, plan.Status)

	a := testArchitectWithControlStore(t, plan)
	a.running = true
	a.bus = &testBus{}
	a.knownAgents = map[string]*guide.AgentAnnouncement{
		"orch-1234": {AgentID: "orch-1234", AgentType: "orchestrator"},
	}

	a.requestOpenPlanHandoffReceiptResyncs("architect startup")

	bus := a.bus.(*testBus)
	if len(bus.published) != 1 {
		t.Fatalf("expected one published resync request, got %d", len(bus.published))
	}
	req, ok := bus.published[0].msg.GetRouteRequest()
	if !ok || req == nil {
		t.Fatalf("expected route request payload, got %#v", bus.published[0].msg.Payload)
	}
	if req.TargetAgentID != "orch-1234" || !req.FireAndForget {
		t.Fatalf("unexpected route request: %+v", req)
	}
	if req.Metadata["control_plane_kind"] != agentshared.ControlPlaneKindPlanHandoffReceiptResync {
		t.Fatalf("unexpected control plane kind: %+v", req.Metadata)
	}
}

func TestIsStalledState(t *testing.T) {
	stalled := []PlanStatus{
		PlanStatusPending, PlanStatusAnalyzing, PlanStatusConsulting,
		PlanStatusDesigning, PlanStatusGenerating, PlanStatusOrchestrating,
	}
	for _, s := range stalled {
		if !isStalledState(s) {
			t.Errorf("expected %s to be stalled", s)
		}
	}
	notStalled := []PlanStatus{
		PlanStatusReady, PlanStatusExecuting, PlanStatusCompleted,
		PlanStatusFailed, PlanStatusClarifying,
	}
	for _, s := range notStalled {
		if isStalledState(s) {
			t.Errorf("expected %s to NOT be stalled", s)
		}
	}
}
