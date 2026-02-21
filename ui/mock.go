package ui

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/session"
	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/filetree"
)

// ---------------------------------------------------------------------------
// Mock agent constants (all derived, not magic)
// ---------------------------------------------------------------------------

// mockGuideID is the agent identifier for the guide agent shown in mock seed data.
const mockGuideID = "guide"

// mockGuideName is the display name for the mock guide agent.
const mockGuideName = "Guide"

// mockGuideType is the agent type classification for the mock guide.
const mockGuideType = "guide"

// mockArchitectID is the agent identifier for the mock architect agent.
const mockArchitectID = "mock-architect"

// mockArchitectName is the display name for the mock architect agent.
const mockArchitectName = "Architect"

// mockArchitectType is the agent type classification for the mock architect.
const mockArchitectType = "architect"

// mockChunkDelay is the delay between streaming chunks.
// Derived from: typical LLM token emission rate (~25 tokens/sec → 40ms).
const mockChunkDelay = 40 * time.Millisecond

// mockThinkDelay simulates agent "thinking" time before starting a response.
// Derived from: noticeable but not frustrating pause (~300ms).
const mockThinkDelay = 300 * time.Millisecond

// mockMaxChunks bounds the number of streaming chunks per response.
// Derived from: generous upper bound on token count for display responses.
const mockMaxChunks = 512

// mockResponseTimeout is the maximum time allowed for a single mock response.
// Derived from: mockChunkDelay × mockMaxChunks ≈ 20s, plus margin.
const mockResponseTimeout = 30 * time.Second

// mockFirstWordsCount is the number of words echoed from user input.
// Derived from: enough for context recognition without being verbose.
const mockFirstWordsCount = 8

// ---------------------------------------------------------------------------
// MockAgent
// ---------------------------------------------------------------------------

// MockAgent subscribes to its own request topic on the EventBus and generates
// fake streaming responses. The Guide routes ForwardedRequests here; the
// MockAgent responds via StreamResponse messages on its own response topic,
// which the Guide then forwards back to the TUI.
type MockAgent struct {
	bus          guide.EventBus
	activityBus  *events.ActivityEventBus
	scope        *concurrency.GoroutineScope
	subscription guide.Subscription
}

// NewMockAgent creates a mock agent with the given dependencies.
func NewMockAgent(
	bus guide.EventBus,
	activityBus *events.ActivityEventBus,
	scope *concurrency.GoroutineScope,
) *MockAgent {
	return &MockAgent{
		bus:         bus,
		activityBus: activityBus,
		scope:       scope,
	}
}

// Start subscribes to the mock agent's own request topic.
func (m *MockAgent) Start() error {
	topic := guide.TopicRequests(mockGuideID, mockGuideID)
	sub, err := m.bus.Subscribe(topic, m.onRequest)
	if err != nil {
		return err
	}
	m.subscription = sub
	return nil
}

// Stop unsubscribes from the guide bus.
func (m *MockAgent) Stop() {
	if m.subscription != nil {
		_ = m.subscription.Unsubscribe()
	}
}

// onRequest handles an incoming forwarded request by spawning a tracked goroutine.
func (m *MockAgent) onRequest(busMsg *guide.Message) error {
	fwd, ok := busMsg.GetForwardedRequest()
	if !ok {
		return nil
	}
	return m.scope.Go(
		"mock.respond."+fwd.CorrelationID,
		mockResponseTimeout,
		m.respondFunc(fwd),
	)
}

// respondFunc returns a WorkFunc that generates a mock streaming response.
func (m *MockAgent) respondFunc(fwd *guide.ForwardedRequest) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		m.respond(ctx, fwd)
		return nil
	}
}

// ---------------------------------------------------------------------------
// Response generation
// ---------------------------------------------------------------------------

// respond produces a streaming mock response for the given forwarded request.
// All stream events are published to the mock agent's response topic; the Guide
// picks them up and forwards them to the TUI.
func (m *MockAgent) respond(ctx context.Context, fwd *guide.ForwardedRequest) {
	correlationID := fwd.CorrelationID
	responseTopic := guide.TopicResponses(mockGuideID, mockGuideID)

	// Phase 1: agent starts thinking.
	m.emitActivity("", mockGuideID, mockGuideName, mockGuideType,
		events.EventTypeAgentDecision, events.OutcomePending,
		"Analyzing request...", 0.1)

	if !sleepCtx(ctx, mockThinkDelay) {
		return
	}

	// Phase 2: agent starts acting.
	m.emitActivity("", mockGuideID, mockGuideName, mockGuideType,
		events.EventTypeAgentAction, events.OutcomePending,
		"Generating response...", 0.3)

	// Phase 3: stream response chunks via the bus.
	response := mockResponse(fwd.Input)
	chunks := splitIntoChunks(response)

	m.publishStream(responseTopic, correlationID, &guide.StreamEvent{
		Type: guide.StreamEventStart, Timestamp: time.Now(),
	})

	for _, chunk := range chunks {
		if !sleepCtx(ctx, mockChunkDelay) {
			return
		}
		m.publishStream(responseTopic, correlationID, &guide.StreamEvent{
			Type: guide.StreamEventData, Text: chunk, Timestamp: time.Now(),
		})
	}

	m.publishStream(responseTopic, correlationID, &guide.StreamEvent{
		Type: guide.StreamEventComplete, Timestamp: time.Now(),
	})

	// Phase 4: agent completes.
	m.emitActivity("", mockGuideID, mockGuideName, mockGuideType,
		events.EventTypeSuccess, events.OutcomeSuccess,
		"Response complete", 0.0)
}

// publishStream publishes a StreamResponse message to the given topic.
func (m *MockAgent) publishStream(topic, correlationID string, event *guide.StreamEvent) {
	resp := &guide.StreamResponse{
		CorrelationID:     correlationID,
		RespondingAgentID: mockGuideID,
		Event:             event,
	}
	busMsg := &guide.Message{
		ID:            uuid.New().String(),
		CorrelationID: correlationID,
		Type:          guide.MessageTypeStream,
		Payload:       resp,
		SourceAgentID: mockGuideID,
		Timestamp:     time.Now(),
	}
	_ = m.bus.Publish(topic, busMsg)
}

// sleepCtx pauses for the given duration, returning false if the context
// is cancelled before the duration elapses.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// emitActivity publishes an activity event for a mock agent.
func (m *MockAgent) emitActivity(
	sessionID, agentID, agentName, agentType string,
	eventType events.EventType,
	outcome events.EventOutcome,
	content string,
	contextUsage float64,
) {
	m.activityBus.Publish(&events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: eventType,
		Timestamp: time.Now(),
		SessionID: sessionID,
		AgentID:   agentID,
		Content:   content,
		Outcome:   outcome,
		Data: map[string]any{
			"agent_type":    agentType,
			"agent_name":    agentName,
			"context_usage": contextUsage,
		},
	})
}

// ---------------------------------------------------------------------------
// Mock data seeding
// ---------------------------------------------------------------------------

// seedMockData pre-populates sessions and registers mock agents so the TUI
// has content to display on startup.
func seedMockData(deps Deps) {
	seedSessions(deps.SessionManager)
	seedAgents(deps.ActivityBus)
	seedAgentHistory(deps.ActivityBus)
}

// mockSessionConfig holds the parameters for a single mock session.
type mockSessionConfig struct {
	name   string
	branch string
}

// mockSessionConfigs returns the configurations for pre-populated sessions.
func mockSessionConfigs() []mockSessionConfig {
	return []mockSessionConfig{
		{name: "Feature Planning", branch: "feature/auth"},
		{name: "Bug Triage", branch: "main"},
		{name: "Code Review", branch: "pr/142"},
	}
}

// seedSessions creates mock sessions and switches to the first one.
func seedSessions(mgr *session.Manager) {
	configs := mockSessionConfigs()
	var firstID string
	for i, mc := range configs {
		cfg := session.DefaultConfig()
		cfg.Name = mc.name
		cfg.Branch = mc.branch
		s, err := mgr.Create(context.Background(), cfg)
		if err != nil {
			continue
		}
		if i == 0 {
			firstID = s.ID()
		}
	}
	if firstID != "" {
		_ = mgr.Switch(firstID)
	}
}

// mockAgentSeed describes an agent to register at startup.
type mockAgentSeed struct {
	id        string
	name      string
	agentType string
}

// mockAgentSeeds returns the initial mock agent registrations.
func mockAgentSeeds() []mockAgentSeed {
	return []mockAgentSeed{
		{id: mockGuideID, name: mockGuideName, agentType: mockGuideType},
		{id: mockArchitectID, name: mockArchitectName, agentType: mockArchitectType},
	}
}

// seedAgents publishes initial activity events to register mock agents
// in the agent panel.
func seedAgents(bus *events.ActivityEventBus) {
	for _, agent := range mockAgentSeeds() {
		bus.Publish(&events.ActivityEvent{
			ID:        uuid.New().String(),
			EventType: events.EventTypeLLMResponse,
			Timestamp: time.Now(),
			AgentID:   agent.id,
			Content:   "Ready",
			Outcome:   events.OutcomeSuccess,
			Data: map[string]any{
				"agent_type":    agent.agentType,
				"agent_name":    agent.name,
				"context_usage": 0.0,
			},
		})
	}
}

// ---------------------------------------------------------------------------
// Mock event history
// ---------------------------------------------------------------------------

// mockHistoryEvent describes a single event in a mock agent's history.
type mockHistoryEvent struct {
	eventType events.EventType
	outcome   events.EventOutcome
	content   string
	usage     float64
}

// mockGuideHistory returns a realistic sequence of events for the Guide agent.
func mockGuideHistory() []mockHistoryEvent {
	return []mockHistoryEvent{
		{events.EventTypeLLMRequest, events.OutcomePending, "Parsing user intent from prompt", 0.05},
		{events.EventTypeLLMResponse, events.OutcomeSuccess, "Identified task: refactor authentication module", 0.08},
		{events.EventTypeAgentDecision, events.OutcomePending, "Routing to Architect for structural analysis", 0.10},
		{events.EventTypeAgentAction, events.OutcomePending, "Delegating subtask: review session middleware", 0.12},
		{events.EventTypeToolCall, events.OutcomePending, "Reading file: core/auth/session.go", 0.15},
		{events.EventTypeToolResult, events.OutcomeSuccess, "Loaded 247 lines from core/auth/session.go", 0.18},
		{events.EventTypeToolCall, events.OutcomePending, "Reading file: core/auth/token.go", 0.20},
		{events.EventTypeToolResult, events.OutcomeSuccess, "Loaded 183 lines from core/auth/token.go", 0.23},
		{events.EventTypeLLMRequest, events.OutcomePending, "Analyzing session and token interdependencies", 0.28},
		{events.EventTypeLLMResponse, events.OutcomeSuccess, "Found 3 circular imports between auth packages", 0.32},
		{events.EventTypeAgentDecision, events.OutcomePending, "Planning refactor: extract shared types to auth/types.go", 0.35},
		{events.EventTypeToolCall, events.OutcomePending, "Searching for usages of SessionToken across codebase", 0.38},
		{events.EventTypeToolResult, events.OutcomeSuccess, "Found 14 references in 8 files", 0.40},
		{events.EventTypeAgentAction, events.OutcomeSuccess, "Compiled dependency graph for auth subsystem", 0.42},
		{events.EventTypeSuccess, events.OutcomeSuccess, "Analysis complete, ready for implementation", 0.45},
	}
}

// mockArchitectHistory returns a realistic sequence of events for the Architect agent.
func mockArchitectHistory() []mockHistoryEvent {
	return []mockHistoryEvent{
		{events.EventTypeLLMRequest, events.OutcomePending, "Evaluating architectural constraints for auth refactor", 0.05},
		{events.EventTypeLLMResponse, events.OutcomeSuccess, "Identified layered architecture with 4 tiers", 0.10},
		{events.EventTypeAgentDecision, events.OutcomePending, "Checking backward compatibility requirements", 0.12},
		{events.EventTypeToolCall, events.OutcomePending, "Reading file: api/v2/handlers.go", 0.15},
		{events.EventTypeToolResult, events.OutcomeSuccess, "Loaded 312 lines from api/v2/handlers.go", 0.18},
		{events.EventTypeToolCall, events.OutcomePending, "Reading file: api/v2/middleware.go", 0.20},
		{events.EventTypeToolResult, events.OutcomeSuccess, "Loaded 156 lines from api/v2/middleware.go", 0.22},
		{events.EventTypeAgentDecision, events.OutcomePending, "Proposing interface extraction for auth providers", 0.25},
		{events.EventTypeLLMRequest, events.OutcomePending, "Generating interface contract for AuthProvider", 0.28},
		{events.EventTypeLLMResponse, events.OutcomeSuccess, "Drafted AuthProvider interface with 5 methods", 0.32},
		{events.EventTypeToolCall, events.OutcomePending, "Validating proposed interface against existing implementations", 0.35},
		{events.EventTypeToolResult, events.OutcomeSuccess, "3/4 implementations conform, 1 needs adapter", 0.38},
		{events.EventTypeAgentAction, events.OutcomePending, "Designing adapter for legacy OAuth provider", 0.42},
		{events.EventTypeToolCall, events.OutcomePending, "Writing file: core/auth/provider.go", 0.45},
		{events.EventTypeToolResult, events.OutcomeSuccess, "Created provider interface definition (62 lines)", 0.48},
		{events.EventTypeAgentError, events.OutcomeFailure, "Conflict detected: token refresh races with session expiry", 0.50},
		{events.EventTypeLLMRequest, events.OutcomePending, "Analyzing race condition between refresh and expiry paths", 0.52},
		{events.EventTypeLLMResponse, events.OutcomeSuccess, "Solution: introduce token lease with CAS update", 0.55},
		{events.EventTypeAgentAction, events.OutcomeSuccess, "Revised design accounts for concurrent token operations", 0.58},
		{events.EventTypeSuccess, events.OutcomeSuccess, "Architecture review complete, design approved", 0.60},
	}
}

// mockEventInterval is the simulated time between mock history events.
// Derived from: realistic agent activity at ~2 events/second.
const mockEventInterval = 500 * time.Millisecond

// seedAgentHistory populates each mock agent's event stream with a
// realistic sequence of past events for testing the expanded view.
func seedAgentHistory(bus *events.ActivityEventBus) {
	type agentHistory struct {
		seed   mockAgentSeed
		events []mockHistoryEvent
	}

	histories := []agentHistory{
		{mockAgentSeeds()[0], mockGuideHistory()},
		{mockAgentSeeds()[1], mockArchitectHistory()},
	}

	baseTime := time.Now().Add(-time.Duration(20) * mockEventInterval)

	for _, ah := range histories {
		for i, evt := range ah.events {
			bus.Publish(&events.ActivityEvent{
				ID:        uuid.New().String(),
				EventType: evt.eventType,
				Timestamp: baseTime.Add(time.Duration(i) * mockEventInterval),
				AgentID:   ah.seed.id,
				Content:   evt.content,
				Outcome:   evt.outcome,
				Data: map[string]any{
					"agent_type":    ah.seed.agentType,
					"agent_name":    ah.seed.name,
					"context_usage": seedContextUsage(ah.seed.id, evt.usage),
				},
			})
		}
	}
}

func seedContextUsage(agentID string, usage float64) float64 {
	// Guide usage in mock mode is now driven by live request/response flow.
	// Keep seeded history neutral so startup does not pin to an arbitrary value.
	if agentID == mockGuideID {
		return 0
	}
	return usage
}

// ---------------------------------------------------------------------------
// Mock code panel
// ---------------------------------------------------------------------------

// mockCodeFilePath is the file path displayed in the code viewer header.
const mockCodeFilePath = "core/auth/session.go"

// mockCodeLanguage is the language identifier for syntax highlighting.
const mockCodeLanguage = "go"

// seedCodePanel populates the code viewer with a realistic Go source file.
func seedCodePanel(panel *codepkg.Model) {
	panel.SetContent(mockGoSource(), mockCodeFilePath, mockCodeLanguage)
}

// mockGoSource returns a realistic Go source file for the code viewer.
func mockGoSource() string {
	var b strings.Builder
	b.WriteString("package auth\n")
	b.WriteString("\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"crypto/rand\"\n")
	b.WriteString("\t\"encoding/hex\"\n")
	b.WriteString("\t\"errors\"\n")
	b.WriteString("\t\"sync\"\n")
	b.WriteString("\t\"time\"\n")
	b.WriteString(")\n")
	b.WriteString("\n")
	b.WriteString("// tokenLength is the byte length of generated session tokens.\n")
	b.WriteString("// Derived from: 32 bytes = 256 bits of entropy.\n")
	b.WriteString("const tokenLength = 32\n")
	b.WriteString("\n")
	b.WriteString("// defaultTTL is the default time-to-live for a session.\n")
	b.WriteString("// Derived from: 24 hours, standard web session duration.\n")
	b.WriteString("const defaultTTL = 24 * time.Hour\n")
	b.WriteString("\n")
	b.WriteString("// maxSessions is the upper bound on concurrent sessions per user.\n")
	b.WriteString("const maxSessions = 8\n")
	b.WriteString("\n")
	b.WriteString("// Errors returned by session operations.\n")
	b.WriteString("var (\n")
	b.WriteString("\tErrSessionNotFound = errors.New(\"session not found\")\n")
	b.WriteString("\tErrSessionExpired  = errors.New(\"session expired\")\n")
	b.WriteString("\tErrMaxSessions     = errors.New(\"maximum sessions reached\")\n")
	b.WriteString(")\n")
	b.WriteString("\n")
	b.WriteString("// Session represents an authenticated user session.\n")
	b.WriteString("type Session struct {\n")
	b.WriteString("\tID        string\n")
	b.WriteString("\tUserID    string\n")
	b.WriteString("\tToken     string\n")
	b.WriteString("\tCreatedAt time.Time\n")
	b.WriteString("\tExpiresAt time.Time\n")
	b.WriteString("\tMetadata  map[string]any\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("// IsExpired reports whether the session has passed its expiry time.\n")
	b.WriteString("func (s *Session) IsExpired() bool {\n")
	b.WriteString("\treturn time.Now().After(s.ExpiresAt)\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("// Remaining returns the duration until the session expires.\n")
	b.WriteString("func (s *Session) Remaining() time.Duration {\n")
	b.WriteString("\treturn time.Until(s.ExpiresAt)\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("// Store manages active sessions with bounded concurrency.\n")
	b.WriteString("type Store struct {\n")
	b.WriteString("\tmu       sync.RWMutex\n")
	b.WriteString("\tsessions map[string]*Session\n")
	b.WriteString("\tbyUser   map[string][]string\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("// NewStore creates an empty session store.\n")
	b.WriteString("func NewStore() *Store {\n")
	b.WriteString("\treturn &Store{\n")
	b.WriteString("\t\tsessions: make(map[string]*Session),\n")
	b.WriteString("\t\tbyUser:   make(map[string][]string),\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("// Create generates a new session for the given user.\n")
	b.WriteString("func (s *Store) Create(ctx context.Context, userID string) (*Session, error) {\n")
	b.WriteString("\ts.mu.Lock()\n")
	b.WriteString("\tdefer s.mu.Unlock()\n")
	b.WriteString("\n")
	b.WriteString("\tif len(s.byUser[userID]) >= maxSessions {\n")
	b.WriteString("\t\treturn nil, ErrMaxSessions\n")
	b.WriteString("\t}\n")
	b.WriteString("\n")
	b.WriteString("\ttoken, err := generateToken()\n")
	b.WriteString("\tif err != nil {\n")
	b.WriteString("\t\treturn nil, err\n")
	b.WriteString("\t}\n")
	b.WriteString("\n")
	b.WriteString("\tnow := time.Now()\n")
	b.WriteString("\tsess := &Session{\n")
	b.WriteString("\t\tID:        generateID(),\n")
	b.WriteString("\t\tUserID:    userID,\n")
	b.WriteString("\t\tToken:     token,\n")
	b.WriteString("\t\tCreatedAt: now,\n")
	b.WriteString("\t\tExpiresAt: now.Add(defaultTTL),\n")
	b.WriteString("\t\tMetadata:  make(map[string]any),\n")
	b.WriteString("\t}\n")
	b.WriteString("\n")
	b.WriteString("\ts.sessions[sess.ID] = sess\n")
	b.WriteString("\ts.byUser[userID] = append(s.byUser[userID], sess.ID)\n")
	b.WriteString("\treturn sess, nil\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("// Validate checks whether a token corresponds to a valid, unexpired session.\n")
	b.WriteString("func (s *Store) Validate(ctx context.Context, token string) (*Session, error) {\n")
	b.WriteString("\ts.mu.RLock()\n")
	b.WriteString("\tdefer s.mu.RUnlock()\n")
	b.WriteString("\n")
	b.WriteString("\tfor _, sess := range s.sessions {\n")
	b.WriteString("\t\tif sess.Token != token {\n")
	b.WriteString("\t\t\tcontinue\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tif sess.IsExpired() {\n")
	b.WriteString("\t\t\treturn nil, ErrSessionExpired\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\treturn sess, nil\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn nil, ErrSessionNotFound\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("// Revoke removes a session by ID.\n")
	b.WriteString("func (s *Store) Revoke(ctx context.Context, sessionID string) error {\n")
	b.WriteString("\ts.mu.Lock()\n")
	b.WriteString("\tdefer s.mu.Unlock()\n")
	b.WriteString("\n")
	b.WriteString("\tsess, ok := s.sessions[sessionID]\n")
	b.WriteString("\tif !ok {\n")
	b.WriteString("\t\treturn ErrSessionNotFound\n")
	b.WriteString("\t}\n")
	b.WriteString("\n")
	b.WriteString("\tdelete(s.sessions, sessionID)\n")
	b.WriteString("\ts.removeFromUser(sess.UserID, sessionID)\n")
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("// removeFromUser removes a session ID from the user's session list.\n")
	b.WriteString("func (s *Store) removeFromUser(userID, sessionID string) {\n")
	b.WriteString("\tids := s.byUser[userID]\n")
	b.WriteString("\tfor i, id := range ids {\n")
	b.WriteString("\t\tif id == sessionID {\n")
	b.WriteString("\t\t\ts.byUser[userID] = append(ids[:i], ids[i+1:]...)\n")
	b.WriteString("\t\t\treturn\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("// generateToken returns a cryptographically random hex-encoded token.\n")
	b.WriteString("func generateToken() (string, error) {\n")
	b.WriteString("\tbuf := make([]byte, tokenLength)\n")
	b.WriteString("\tif _, err := rand.Read(buf); err != nil {\n")
	b.WriteString("\t\treturn \"\", err\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn hex.EncodeToString(buf), nil\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("// generateID returns a short unique identifier for a session.\n")
	b.WriteString("func generateID() string {\n")
	b.WriteString("\tbuf := make([]byte, 8)\n")
	b.WriteString("\t_, _ = rand.Read(buf)\n")
	b.WriteString("\treturn hex.EncodeToString(buf)\n")
	b.WriteString("}\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// Mock file tree
// ---------------------------------------------------------------------------

// seedFileTree populates the file tree panel with a mock project structure.
func seedFileTree(tree *filetree.Model) {
	tree.SetEntries(mockFileTreeEntries())
}

// mockFileTreeEntries returns a realistic Go project tree for display testing.
// All directories default to collapsed; users expand them explicitly.
func mockFileTreeEntries() []filetree.Entry {
	return []filetree.Entry{
		{Name: "agents", Path: "agents", IsDir: true, Depth: 0},
		{Name: "core", Path: "core", IsDir: true, Depth: 0},
		{Name: "ui", Path: "ui", IsDir: true, Depth: 0},
		{Name: "go.mod", Path: "go.mod", IsDir: false, Depth: 0},
		{Name: "go.sum", Path: "go.sum", IsDir: false, Depth: 0},
		{Name: "main.go", Path: "main.go", IsDir: false, Depth: 0},
	}
}

// ---------------------------------------------------------------------------
// Response text generation
// ---------------------------------------------------------------------------

// mockResponse generates a markdown response that exercises chat rendering.
// It echoes part of the user's input for a contextual feel.
func mockResponse(input string) string {
	echo := firstWords(input, mockFirstWordsCount)

	var b strings.Builder
	b.WriteString("Regarding \"")
	b.WriteString(echo)
	b.WriteString("\" — here's my analysis:\n\n")
	b.WriteString("## Overview\n\n")
	b.WriteString("I've examined the relevant code and identified several key areas:\n\n")
	b.WriteString("1. **Architecture** — The current design follows clean separation of concerns\n")
	b.WriteString("2. **Performance** — There are optimization opportunities in the hot path\n")
	b.WriteString("3. **Testing** — Coverage could be improved with integration tests\n\n")
	b.WriteString("## Implementation\n\n")
	b.WriteString("```go\n")
	b.WriteString("func Process(ctx context.Context, input string) (string, error) {\n")
	b.WriteString("    result, err := analyze(ctx, input)\n")
	b.WriteString("    if err != nil {\n")
	b.WriteString("        return \"\", fmt.Errorf(\"analysis: %%w\", err)\n")
	b.WriteString("    }\n")
	b.WriteString("    return format(result), nil\n")
	b.WriteString("}\n")
	b.WriteString("```\n\n")
	b.WriteString("## Next Steps\n\n")
	b.WriteString("- Review the proposed changes above\n")
	b.WriteString("- Run the test suite to verify correctness\n")
	b.WriteString("- Consider edge cases for error handling\n\n")
	b.WriteString("Let me know if you'd like me to elaborate or proceed with the implementation.")
	return b.String()
}

// firstWords returns the first n whitespace-delimited words from s.
func firstWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) > n {
		words = words[:n]
	}
	return strings.Join(words, " ")
}

// splitIntoChunks breaks text into word-boundary chunks for streaming.
// Each chunk preserves trailing whitespace (spaces, newlines) so the
// reassembled text matches the original. Returns at most mockMaxChunks pieces.
func splitIntoChunks(text string) []string {
	if text == "" {
		return nil
	}

	// Pre-allocate with a reasonable estimate (1 chunk per ~4 chars).
	estimated := min(len(text)/4+1, mockMaxChunks)
	chunks := make([]string, 0, estimated)

	var current strings.Builder
	for _, r := range text {
		current.WriteRune(r)
		// Emit at word boundaries.
		if (r == ' ' || r == '\n') && current.Len() > 0 {
			if len(chunks) >= mockMaxChunks {
				break
			}
			chunks = append(chunks, current.String())
			current.Reset()
		}
	}

	// Flush remaining content.
	if current.Len() > 0 && len(chunks) < mockMaxChunks {
		chunks = append(chunks, current.String())
	}

	return chunks
}
