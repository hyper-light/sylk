package scribe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
)

type mockProvider struct {
	mu        sync.Mutex
	requests  []*providers.Request
	responses []*providers.Response
	errors    []error
}

func (p *mockProvider) Complete(_ context.Context, req *providers.Request) (*providers.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.requests = append(p.requests, cloneProviderRequest(req))
	idx := len(p.requests) - 1
	if idx < len(p.errors) && p.errors[idx] != nil {
		return nil, p.errors[idx]
	}
	if idx >= len(p.responses) {
		return nil, fmt.Errorf("unexpected provider call %d", idx+1)
	}
	return cloneProviderResponse(p.responses[idx]), nil
}

func (p *mockProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *mockProvider) requestAt(idx int) *providers.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.requests) {
		return nil
	}
	return cloneProviderRequest(p.requests[idx])
}

type mockBus struct {
	mu        sync.Mutex
	published []*guide.Message
	topics    []string
	subs      map[string]guide.MessageHandler
}

func newMockBus() *mockBus {
	return &mockBus{subs: make(map[string]guide.MessageHandler)}
}

func (b *mockBus) Publish(topic string, msg *guide.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, msg)
	b.topics = append(b.topics, topic)
	return nil
}

func (b *mockBus) Subscribe(topic string, handler guide.MessageHandler) (guide.Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = handler
	return &mockSub{topic: topic}, nil
}

func (b *mockBus) SubscribeAsync(topic string, handler guide.MessageHandler) (guide.Subscription, error) {
	return b.Subscribe(topic, handler)
}

func (b *mockBus) Close() error { return nil }

func (b *mockBus) publishCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

func (b *mockBus) lastTopic() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.topics) == 0 {
		return ""
	}
	return b.topics[len(b.topics)-1]
}

func (b *mockBus) lastMessage() *guide.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.published) == 0 {
		return nil
	}
	return b.published[len(b.published)-1]
}

type mockSub struct {
	topic        string
	unsubscribed atomic.Bool
}

func (s *mockSub) Topic() string { return s.topic }

func (s *mockSub) Unsubscribe() error {
	s.unsubscribed.Store(true)
	return nil
}

func (s *mockSub) IsActive() bool {
	return !s.unsubscribed.Load()
}

type mockForest struct {
	mu             sync.Mutex
	resolveResult  *forest.IntentResolution
	retrieveResult []*forest.BranchPacket
	predictResult  []*forest.BranchPacket
	outcomes       []forest.OutcomeRecord
}

func (m *mockForest) ResolveIntent(_ context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resolveResult != nil {
		cloned := *m.resolveResult
		return &cloned, nil
	}
	return &forest.IntentResolution{Query: input.Query}, nil
}

func (m *mockForest) Retrieve(_ context.Context, _ forest.Query) ([]*forest.BranchPacket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneBranchPackets(m.retrieveResult), nil
}

func (m *mockForest) PredictNextBranches(_ context.Context, _ forest.Query) ([]*forest.BranchPacket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneBranchPackets(m.predictResult), nil
}

func (m *mockForest) RecordOutcome(_ context.Context, record forest.OutcomeRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outcomes = append(m.outcomes, record)
	return nil
}

// MEM-01: MemoryForestService widened with family projections. Scribe
// doesn't exercise these; minimal no-op stubs keep the stub a valid
// interface implementation.
func (m *mockForest) ProjectIntent(context.Context, forest.ProjectionInput) (*forest.IntentProjection, error) {
	return nil, nil
}
func (m *mockForest) ProjectConstraints(context.Context, forest.ProjectionInput) (*forest.ConstraintProjection, error) {
	return nil, nil
}
func (m *mockForest) ProjectEvidence(context.Context, forest.ProjectionInput) (*forest.EvidenceProjection, error) {
	return nil, nil
}
func (m *mockForest) ProjectDecisions(context.Context, forest.ProjectionInput) (*forest.DecisionProjection, error) {
	return nil, nil
}
func (m *mockForest) ProjectOutcomes(context.Context, forest.ProjectionInput) (*forest.OutcomeProjection, error) {
	return nil, nil
}
func (m *mockForest) ProjectPreferences(context.Context, forest.ProjectionInput) (*forest.PreferenceProjection, error) {
	return nil, nil
}
func (m *mockForest) ProjectCapabilities(context.Context, forest.ProjectionInput) (*forest.CapabilityProjection, error) {
	return nil, nil
}
func (m *mockForest) ProjectOpportunities(context.Context, forest.ProjectionInput) (*forest.OpportunityProjection, error) {
	return nil, nil
}

func (m *mockForest) recordedOutcomes() []forest.OutcomeRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]forest.OutcomeRecord, len(m.outcomes))
	copy(out, m.outcomes)
	return out
}

func newTestScope(t *testing.T) *concurrency.GoroutineScope {
	t.Helper()
	scope := concurrency.NewGoroutineScope(context.Background(), "test-scribe", nil)
	t.Cleanup(func() { _ = scope.Shutdown(50*time.Millisecond, 100*time.Millisecond) })
	return scope
}

func TestScribe_New(t *testing.T) {
	s := New(Config{
		ParentAgentType: "engineer",
		Provider: &mockProvider{responses: []*providers.Response{
			storeToolResponse("init"),
		}},
		Model: "test-model",
		Bus:   newMockBus(),
		Scope: newTestScope(t),
	})

	if s.id == "" {
		t.Fatal("expected non-empty ID")
	}
	if s.parentAgentType != "engineer" {
		t.Fatalf("parentAgentType = %q, want engineer", s.parentAgentType)
	}
	if s.maxMessages != defaultMaxMessages {
		t.Fatalf("maxMessages = %d, want %d", s.maxMessages, defaultMaxMessages)
	}
	if s.maxToolRuns != defaultMaxToolRuns {
		t.Fatalf("maxToolRuns = %d, want %d", s.maxToolRuns, defaultMaxToolRuns)
	}
}

func TestScribe_StartStop(t *testing.T) {
	s := New(Config{
		ParentAgentType: "architect",
		Provider: &mockProvider{responses: []*providers.Response{
			storeToolResponse("noop"),
		}},
		Model: "test-model",
		Bus:   newMockBus(),
		Scope: newTestScope(t),
	})

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !s.running.Load() {
		t.Fatal("expected running=true after Start")
	}
	if err := s.Start(); err != nil {
		t.Fatalf("double Start: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s.running.Load() {
		t.Fatal("expected running=false after Stop")
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("double Stop: %v", err)
	}
}

func TestScribe_Feed_ProcessesAndForwards(t *testing.T) {
	bus := newMockBus()
	provider := &mockProvider{
		responses: []*providers.Response{
			storeToolResponse("engineer built feature X"),
		},
	}
	s := New(Config{
		ParentAgentType: "engineer",
		Provider:        provider,
		Model:           "test-model",
		Bus:             bus,
		Scope:           newTestScope(t),
	})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Stop() }()

	s.Feed(shared.ScribeFeed{
		UserRequest:         "build feature X",
		AgentResponse:       "built feature X",
		ParentCorrelationID: "corr-1",
		Timestamp:           time.Now(),
	})

	waitFor(t, "scribe provider call", func() bool { return provider.callCount() == 1 })
	waitFor(t, "archivalist store publish", func() bool { return bus.publishCount() == 1 })

	req := provider.requestAt(0)
	if req == nil {
		t.Fatal("expected first provider request")
	}
	if req.ToolChoice != "any" {
		t.Fatalf("toolChoice = %q, want any", req.ToolChoice)
	}
	if !containsTool(req.Tools, scribeStoreArchivalistSkillName) {
		t.Fatalf("provider request tools missing %q", scribeStoreArchivalistSkillName)
	}
	if containsTool(req.Tools, "scribe_forest_consult") {
		t.Fatal("forest skills should not be exposed when forest is disabled")
	}

	if bus.lastTopic() != guide.TopicGuideRequests {
		t.Fatalf("last topic = %q, want %q", bus.lastTopic(), guide.TopicGuideRequests)
	}
	msg := bus.lastMessage()
	if msg == nil {
		t.Fatal("expected published message")
	}
	route, ok := msg.GetRouteRequest()
	if !ok || route == nil {
		t.Fatalf("expected route request payload, got %#v", msg.Payload)
	}
	if route.SourceAgentID != s.id {
		t.Fatalf("source_agent_id = %q, want %q", route.SourceAgentID, s.id)
	}
	if !route.FireAndForget {
		t.Fatal("expected archivalist store route to be fire-and-forget")
	}
	if route.CorrelationID == "corr-1" {
		t.Fatal("scribe reused parent correlation ID for archival store route")
	}
	if route.ParentCorrelationID != "corr-1" {
		t.Fatalf("parent_correlation_id = %q, want corr-1", route.ParentCorrelationID)
	}
	if !strings.HasPrefix(route.Input, "@archivalist:store:history") {
		t.Fatalf("input = %q, want archivalist store DSL", route.Input)
	}
	if got, _ := route.Metadata["chat_nested_branch"].(bool); !got {
		t.Fatalf("chat_nested_branch = %#v, want true", route.Metadata["chat_nested_branch"])
	}
	if got, _ := route.Metadata["chat_inter_agent_kind"].(string); got != shared.InterAgentToolEventKindStore {
		t.Fatalf("chat_inter_agent_kind = %q, want %q", got, shared.InterAgentToolEventKindStore)
	}
}

func TestScribe_GenerateCommentary_UsesForestSkillAndRecordsOutcome(t *testing.T) {
	bus := newMockBus()
	forestSvc := &mockForest{
		resolveResult: &forest.IntentResolution{
			Query:         "preserve handoff context",
			PrimaryIntent: "preserve history",
		},
		retrieveResult: []*forest.BranchPacket{
			newBranchPacket("branch-1"),
		},
	}
	provider := &mockProvider{
		responses: []*providers.Response{
			{
				ToolCalls: []providers.ToolCall{{
					ID:        "call-1",
					Name:      "scribe_forest_consult",
					Arguments: `{"purpose":"get_capture_targets","query":"preserve handoff context"}`,
				}},
				StopReason: providers.StopReasonToolUse,
			},
			storeToolResponse("captured with forest"),
		},
	}
	s := New(Config{
		ParentAgentType: "architect",
		Provider:        provider,
		Model:           "test-model",
		Bus:             bus,
		Scope:           newTestScope(t),
		Forest:          forestSvc,
	})

	feed := shared.ScribeFeed{
		UserRequest:         "plan the rollout",
		AgentResponse:       "prepared the rollout plan",
		ParentCorrelationID: "corr-forest",
		Timestamp:           time.Now(),
	}
	ctx := s.contextForFeed(context.Background(), feed)
	commentary, err := s.generateCommentary(ctx, []providers.Message{s.buildTurnMessage(feed)}, feed)
	if err != nil {
		t.Fatalf("generateCommentary: %v", err)
	}
	if !strings.Contains(commentary, `"summary":"captured with forest"`) {
		t.Fatalf("commentary = %q, want stored JSON summary", commentary)
	}
	if provider.callCount() != 2 {
		t.Fatalf("provider call count = %d, want 2", provider.callCount())
	}

	firstReq := provider.requestAt(0)
	if firstReq == nil {
		t.Fatal("expected first provider request")
	}
	if !containsTool(firstReq.Tools, "scribe_forest_consult") {
		t.Fatal("forest capture tool was not exposed to scribe")
	}
	if !containsTool(firstReq.Tools, scribeStoreArchivalistSkillName) {
		t.Fatal("store_archivalist tool was not exposed to scribe")
	}

	outcomes := forestSvc.recordedOutcomes()
	if len(outcomes) != 1 {
		t.Fatalf("forest outcomes = %d, want 1", len(outcomes))
	}
	if outcomes[0].BranchID != "branch-1" {
		t.Fatalf("outcome branch_id = %q, want branch-1", outcomes[0].BranchID)
	}
	if outcomes[0].Status != forest.OutcomeStatusSucceeded {
		t.Fatalf("outcome status = %q, want %q", outcomes[0].Status, forest.OutcomeStatusSucceeded)
	}
	if bus.publishCount() != 1 {
		t.Fatalf("archivalist store publishes = %d, want 1", bus.publishCount())
	}
}

func TestScribe_WorkstreamsIsolateByCorrelation(t *testing.T) {
	bus := newMockBus()
	provider := &mockProvider{
		responses: []*providers.Response{
			storeToolResponse("first summary"),
			storeToolResponse("second summary"),
		},
	}
	s := New(Config{
		ParentAgentType: "engineer",
		Provider:        provider,
		Model:           "test-model",
		Bus:             bus,
		Scope:           newTestScope(t),
	})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Stop() }()

	s.Feed(shared.ScribeFeed{
		UserRequest:         "feature-one",
		AgentResponse:       "implemented feature-one",
		ParentCorrelationID: "corr-a",
		Timestamp:           time.Now(),
	})
	waitFor(t, "first workstream call", func() bool { return provider.callCount() >= 1 })

	s.Feed(shared.ScribeFeed{
		UserRequest:         "feature-two",
		AgentResponse:       "implemented feature-two",
		ParentCorrelationID: "corr-b",
		Timestamp:           time.Now(),
	})
	waitFor(t, "second workstream call", func() bool { return provider.callCount() >= 2 })

	secondReq := provider.requestAt(1)
	if secondReq == nil {
		t.Fatal("expected second provider request")
	}
	if joined := joinMessageContents(secondReq.Messages); strings.Contains(joined, "feature-one") || strings.Contains(joined, "first summary") {
		t.Fatalf("second workstream leaked first workstream history: %s", joined)
	}
	if joined := joinMessageContents(secondReq.Messages); !strings.Contains(joined, "feature-two") {
		t.Fatalf("second workstream missing current turn: %s", joined)
	}
}

func TestScribe_WorkstreamHistoryPersistsWithinCorrelation(t *testing.T) {
	bus := newMockBus()
	provider := &mockProvider{
		responses: []*providers.Response{
			storeToolResponse("first summary"),
			storeToolResponse("second summary"),
		},
	}
	s := New(Config{
		ParentAgentType: "architect",
		Provider:        provider,
		Model:           "test-model",
		Bus:             bus,
		Scope:           newTestScope(t),
	})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Stop() }()

	s.Feed(shared.ScribeFeed{
		UserRequest:         "step one",
		AgentResponse:       "planned step one",
		ParentCorrelationID: "corr-shared",
		Timestamp:           time.Now(),
	})
	waitFor(t, "first same-correlation call", func() bool { return provider.callCount() >= 1 })

	s.Feed(shared.ScribeFeed{
		UserRequest:         "step two",
		AgentResponse:       "planned step two",
		ParentCorrelationID: "corr-shared",
		Timestamp:           time.Now(),
	})
	waitFor(t, "second same-correlation call", func() bool { return provider.callCount() >= 2 })

	secondReq := provider.requestAt(1)
	if secondReq == nil {
		t.Fatal("expected second provider request")
	}
	joined := joinMessageContents(secondReq.Messages)
	if !strings.Contains(joined, "first summary") {
		t.Fatalf("second request missing prior stored commentary: %s", joined)
	}
	if !strings.Contains(joined, "step two") {
		t.Fatalf("second request missing current turn: %s", joined)
	}
}

func TestScribe_InjectPreparedContext_SeedsMatchingWorkstream(t *testing.T) {
	bus := newMockBus()
	provider := &mockProvider{
		responses: []*providers.Response{
			storeToolResponse("post handoff summary"),
		},
	}
	s := New(Config{
		ParentAgentType: "architect",
		Provider:        provider,
		Model:           "test-model",
		Bus:             bus,
		Scope:           newTestScope(t),
	})
	if err := s.InjectPreparedContext(testPreparedContextBrief(t, "corr-handoff", handoff.ContextBrief{
		TaskSummary:  "previous scribe preserved plan state",
		KeyDecisions: "kept the staged rollout",
		ActiveState:  "working through rollout plan",
		NextSteps:    "validate the remaining deployment steps",
		Blockers:     "deployment risk still open",
		GeneratedAt:  time.Now().UTC(),
	})); err != nil {
		t.Fatalf("InjectPreparedContext: %v", err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Stop() }()

	s.Feed(shared.ScribeFeed{
		UserRequest:         "finish the rollout plan",
		AgentResponse:       "completed the rollout plan updates",
		ParentCorrelationID: "corr-handoff",
		Timestamp:           time.Now(),
	})

	waitFor(t, "handoff-seeded provider call", func() bool { return provider.callCount() >= 1 })

	req := provider.requestAt(0)
	if req == nil {
		t.Fatal("expected provider request after handoff feed")
	}
	joined := joinMessageContents(req.Messages)
	if !strings.Contains(joined, "[scribe handoff]") {
		t.Fatalf("request missing handoff seed: %s", joined)
	}
	if !strings.Contains(joined, "previous scribe preserved plan state") {
		t.Fatalf("request missing handoff summary: %s", joined)
	}
	if !strings.Contains(joined, "validate the remaining deployment steps") {
		t.Fatalf("request missing handoff next steps: %s", joined)
	}
	if !strings.Contains(joined, "finish the rollout plan") {
		t.Fatalf("request missing current turn after handoff seed: %s", joined)
	}
}

func TestScribe_InjectPreparedContext_WaitsForMatchingCorrelation(t *testing.T) {
	bus := newMockBus()
	provider := &mockProvider{
		responses: []*providers.Response{
			storeToolResponse("first unrelated summary"),
			storeToolResponse("matching handoff summary"),
		},
	}
	s := New(Config{
		ParentAgentType: "engineer",
		Provider:        provider,
		Model:           "test-model",
		Bus:             bus,
		Scope:           newTestScope(t),
	})
	if err := s.InjectPreparedContext(testPreparedContextBrief(t, "corr-target", handoff.ContextBrief{
		TaskSummary: "carry over implementation context",
		NextSteps:   "resume the pending file change",
		GeneratedAt: time.Now().UTC(),
	})); err != nil {
		t.Fatalf("InjectPreparedContext: %v", err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Stop() }()

	s.Feed(shared.ScribeFeed{
		UserRequest:         "unrelated task",
		AgentResponse:       "handled unrelated task",
		ParentCorrelationID: "corr-other",
		Timestamp:           time.Now(),
	})
	waitFor(t, "non-matching workstream call", func() bool { return provider.callCount() >= 1 })

	firstReq := provider.requestAt(0)
	if firstReq == nil {
		t.Fatal("expected first provider request")
	}
	if joined := joinMessageContents(firstReq.Messages); strings.Contains(joined, "[scribe handoff]") || strings.Contains(joined, "carry over implementation context") {
		t.Fatalf("handoff seed leaked into non-matching workstream: %s", joined)
	}

	s.Feed(shared.ScribeFeed{
		UserRequest:         "resume target task",
		AgentResponse:       "resumed target task",
		ParentCorrelationID: "corr-target",
		Timestamp:           time.Now(),
	})
	waitFor(t, "matching workstream call", func() bool { return provider.callCount() >= 2 })

	secondReq := provider.requestAt(1)
	if secondReq == nil {
		t.Fatal("expected second provider request")
	}
	joined := joinMessageContents(secondReq.Messages)
	if !strings.Contains(joined, "[scribe handoff]") {
		t.Fatalf("matching workstream missing handoff seed: %s", joined)
	}
	if !strings.Contains(joined, "carry over implementation context") {
		t.Fatalf("matching workstream missing preserved summary: %s", joined)
	}
	if !strings.Contains(joined, "resume target task") {
		t.Fatalf("matching workstream missing current turn: %s", joined)
	}
}

func TestScribeSystemPrompt_IsSpecializedByAgent(t *testing.T) {
	engineerPrompt := scribeSystemPrompt("engineer", true)
	if !strings.Contains(engineerPrompt, `"files"`) || !strings.Contains(engineerPrompt, `"tests"`) {
		t.Fatalf("engineer prompt missing implementation fields: %s", engineerPrompt)
	}
	if !strings.Contains(engineerPrompt, scribeStoreArchivalistSkillName) {
		t.Fatalf("engineer prompt missing store skill guidance: %s", engineerPrompt)
	}
	if !strings.Contains(engineerPrompt, "scribe_forest_consult(purpose=get_capture_targets") {
		t.Fatalf("engineer prompt missing forest guidance when enabled: %s", engineerPrompt)
	}

	academicPrompt := scribeSystemPrompt("academic", false)
	if !strings.Contains(academicPrompt, `"thesis"`) || !strings.Contains(academicPrompt, `"handoff_delta"`) {
		t.Fatalf("academic prompt missing research fields: %s", academicPrompt)
	}
	if strings.Contains(academicPrompt, "scribe_forest_consult") {
		t.Fatalf("academic prompt should not mention forest skill when forest is disabled: %s", academicPrompt)
	}
}

func TestScribe_HandoffableAgent(t *testing.T) {
	s := New(Config{
		ParentAgentType: "designer",
		Provider: &mockProvider{responses: []*providers.Response{
			storeToolResponse("noop"),
		}},
		Model: "gemini-3-flash-preview",
		Bus:   newMockBus(),
		Scope: newTestScope(t),
	})

	if s.AgentType() != "scribe-designer" {
		t.Fatalf("AgentType = %q, want scribe-designer", s.AgentType())
	}
	desc := s.Descriptor()
	if desc.AgentType != "scribe-designer" {
		t.Fatalf("descriptor AgentType = %q, want scribe-designer", desc.AgentType)
	}
	if desc.ModelID != "gemini-3-flash-preview" {
		t.Fatalf("descriptor ModelID = %q, want gemini-3-flash-preview", desc.ModelID)
	}
	if desc.ContextWindow != 1_000_000 {
		t.Fatalf("descriptor ContextWindow = %d, want 1000000", desc.ContextWindow)
	}
	state := s.ExtractArchivableState()
	if state.AgentType != "scribe-designer" {
		t.Fatalf("archivable AgentType = %q, want scribe-designer", state.AgentType)
	}
}

func TestScribe_Feed_DropsWhenBufferFull(t *testing.T) {
	s := New(Config{
		ParentAgentType: "test",
		Provider: &mockProvider{responses: []*providers.Response{
			storeToolResponse("noop"),
		}},
		Model: "test-model",
		Bus:   newMockBus(),
		Scope: newTestScope(t),
	})

	for range feedBufferSize + 5 {
		s.Feed(shared.ScribeFeed{
			UserRequest:         "req",
			AgentResponse:       "resp",
			ParentCorrelationID: "c",
			Timestamp:           time.Now(),
		})
	}

	if got := len(s.feedCh); got > feedBufferSize {
		t.Fatalf("feedCh length = %d, want <= %d", got, feedBufferSize)
	}
}

func TestScribe_WorkstreamForFeed_PrunesIdleAndExcessWorkstreams(t *testing.T) {
	s := New(Config{
		ParentAgentType: "architect",
		Provider: &mockProvider{responses: []*providers.Response{
			storeToolResponse("noop"),
		}},
		Model: "test-model",
		Bus:   newMockBus(),
		Scope: newTestScope(t),
	})
	now := time.Now()
	for i := 0; i < scribeMaxWorkstreams+10; i++ {
		s.workstreams[fmt.Sprintf("stale-%d", i)] = &scribeWorkstream{
			messages:  []providers.Message{{Role: providers.RoleUser, Content: "old"}},
			updatedAt: now.Add(-scribeWorkstreamTTL - time.Minute),
		}
	}
	for i := 0; i < scribeMaxWorkstreams+10; i++ {
		s.workstreams[fmt.Sprintf("fresh-%d", i)] = &scribeWorkstream{
			messages:  []providers.Message{{Role: providers.RoleUser, Content: "fresh"}},
			updatedAt: now.Add(-time.Duration(i) * time.Second),
		}
	}

	workstream := s.workstreamForFeed(shared.ScribeFeed{ParentCorrelationID: "corr-new"})
	if workstream == nil {
		t.Fatal("expected workstream")
	}
	if got := len(s.workstreams); got > scribeMaxWorkstreams {
		t.Fatalf("workstreams = %d, want <= %d", got, scribeMaxWorkstreams)
	}
	if _, ok := s.workstreams["corr-new"]; !ok {
		t.Fatal("expected new workstream to be retained")
	}
	for key := range s.workstreams {
		if strings.HasPrefix(key, "stale-") {
			t.Fatalf("expected stale workstream %q to be pruned", key)
		}
	}
}

func waitFor(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func storeToolResponse(summary string) *providers.Response {
	return &providers.Response{
		ToolCalls: []providers.ToolCall{{
			ID:        "store-1",
			Name:      scribeStoreArchivalistSkillName,
			Arguments: commentaryArgs(summary),
		}},
		StopReason: providers.StopReasonToolUse,
	}
}

func commentaryArgs(summary string) string {
	payload := map[string]any{
		"commentary": map[string]any{
			"summary":         summary,
			"progress":        "in progress",
			"decisions":       "made a decision",
			"state":           "working",
			"risk":            "none",
			"handoff_context": "compact handoff",
			"details": map[string]any{
				"notes": "high signal only",
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func containsTool(tools []providers.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func joinMessageContents(messages []providers.Message) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if content := strings.TrimSpace(msg.Content); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n")
}

func cloneProviderRequest(req *providers.Request) *providers.Request {
	if req == nil {
		return nil
	}
	cloned := *req
	cloned.Messages = cloneProviderMessages(req.Messages)
	cloned.Tools = cloneProviderTools(req.Tools)
	if req.Metadata != nil {
		cloned.Metadata = cloneAnyMap(req.Metadata)
	}
	return &cloned
}

func cloneProviderResponse(resp *providers.Response) *providers.Response {
	if resp == nil {
		return nil
	}
	cloned := *resp
	if len(resp.ToolCalls) > 0 {
		cloned.ToolCalls = append([]providers.ToolCall(nil), resp.ToolCalls...)
	}
	if resp.ProviderMetadata != nil {
		cloned.ProviderMetadata = cloneAnyMap(resp.ProviderMetadata)
	}
	return &cloned
}

func cloneProviderMessages(messages []providers.Message) []providers.Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]providers.Message, len(messages))
	for i, msg := range messages {
		cloned[i] = msg
		if len(msg.ToolCalls) > 0 {
			cloned[i].ToolCalls = append([]providers.ToolCall(nil), msg.ToolCalls...)
		}
		if msg.Metadata != nil {
			cloned[i].Metadata = cloneAnyMap(msg.Metadata)
		}
	}
	return cloned
}

func cloneProviderTools(tools []providers.Tool) []providers.Tool {
	if len(tools) == 0 {
		return nil
	}
	cloned := make([]providers.Tool, len(tools))
	for i, tool := range tools {
		cloned[i] = tool.Clone()
	}
	return cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func newBranchPacket(branchID string) *forest.BranchPacket {
	return &forest.BranchPacket{
		Branch: &forest.Branch{
			ID:      branchID,
			Title:   "Branch " + branchID,
			Summary: "summary for " + branchID,
		},
	}
}

func cloneBranchPackets(packets []*forest.BranchPacket) []*forest.BranchPacket {
	if len(packets) == 0 {
		return nil
	}
	cloned := make([]*forest.BranchPacket, 0, len(packets))
	for _, packet := range packets {
		if packet == nil {
			continue
		}
		next := *packet
		if packet.Branch != nil {
			branch := *packet.Branch
			next.Branch = &branch
		}
		cloned = append(cloned, &next)
	}
	return cloned
}

func testPreparedContextBrief(t *testing.T, correlationID string, brief handoff.ContextBrief) *handoff.PreparedContext {
	t.Helper()
	pc := handoff.NewPreparedContextDefault()
	raw, err := json.Marshal(brief)
	if err != nil {
		t.Fatalf("marshal brief: %v", err)
	}
	pc.SetMetadata("context_brief", string(raw))
	if trimmed := strings.TrimSpace(correlationID); trimmed != "" {
		pc.SetMetadata("correlation_id", trimmed)
	}
	return pc
}
