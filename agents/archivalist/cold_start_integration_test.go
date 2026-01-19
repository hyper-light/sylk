package archivalist

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/knowledge/coldstart"
)

// =============================================================================
// AE.7.3 Cold-Start Integration Tests
// =============================================================================

// TestColdStartIntegration_ZeroHistory tests that ArchivalistRetriever
// works correctly with zero historical data (pure cold-start scenario).
func TestColdStartIntegration_ZeroHistory(t *testing.T) {
	// Create retriever with no memory store, no searchers (zero history)
	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, nil, nil)

	// Verify default configuration
	if retriever.MinTracesForWarm() != coldstart.DefaultMinTracesForWarm {
		t.Errorf("Expected MinTracesForWarm=%d, got %d",
			coldstart.DefaultMinTracesForWarm, retriever.MinTracesForWarm())
	}

	if retriever.FullWarmTraces() != coldstart.DefaultFullWarmTraces {
		t.Errorf("Expected FullWarmTraces=%d, got %d",
			coldstart.DefaultFullWarmTraces, retriever.FullWarmTraces())
	}

	// With no searchers, search should return empty results (no errors)
	opts := coldstart.DefaultSearchOptions()
	results, err := retriever.Search(context.Background(), "test query", nil, opts)

	// Both searchers are nil, so this should fail with appropriate error
	// or return empty results
	if err != nil {
		// Expected - both searchers nil
		t.Logf("Expected error with no searchers: %v", err)
	} else {
		// If no error, results should be empty
		if len(results) != 0 {
			t.Errorf("Expected 0 results with no searchers, got %d", len(results))
		}
	}
}

// TestColdStartIntegration_ColdPriorsUsed tests that cold priors are used
// when there is no user history (trace count < minTracesForWarm).
func TestColdStartIntegration_ColdPriorsUsed(t *testing.T) {
	// Set up mock searchers
	mockBleve := newColdStartMockBleveSearcher()
	mockVector := newColdStartMockVectorSearcher()

	// Create retriever with mocks but no memory store
	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	// Add test events to mock searchers
	testEvents := []*events.ActivityEvent{
		createColdStartTestEvent("evt-1", events.EventTypeAgentDecision, "Decision content"),
		createColdStartTestEvent("evt-2", events.EventTypeUserPrompt, "User prompt content"),
		createColdStartTestEvent("evt-3", events.EventTypeToolCall, "Tool call content"),
	}
	mockBleve.SetResults(testEvents)
	mockVector.SetResults(testEvents)

	// Search without any user history
	opts := coldstart.DefaultSearchOptions()
	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Verify results use cold scoring
	for _, result := range results {
		// With no traces, BlendRatio should be 0 (pure cold scoring)
		if result.BlendRatio != 0.0 {
			t.Errorf("Expected BlendRatio=0.0 (pure cold), got %f for %s",
				result.BlendRatio, result.Event.ID)
		}

		// ColdScore should be non-zero (based on event type importance)
		if result.ColdScore <= 0 {
			t.Errorf("Expected ColdScore > 0, got %f for %s",
				result.ColdScore, result.Event.ID)
		}

		// Score should equal ColdScore when BlendRatio is 0
		if result.Score != result.ColdScore {
			t.Errorf("Expected Score=ColdScore when BlendRatio=0, got Score=%f, ColdScore=%f",
				result.Score, result.ColdScore)
		}

		// Explanation should indicate pure cold scoring
		if result.Explanation == "" {
			t.Error("Expected non-empty explanation")
		}
	}
}

// TestColdStartIntegration_TransitionToWarm tests the transition from
// cold-start to warm blending as traces accumulate.
func TestColdStartIntegration_TransitionToWarm(t *testing.T) {
	// Create retriever without memory store - test blend ratio calculation directly
	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, nil, nil)

	// Test progression from cold to warm based on trace count
	// Default: minTracesForWarm=3, fullWarmTraces=15
	traceProgression := []struct {
		traceCount     int
		expectedRatio  float64
		description    string
	}{
		{0, 0.0, "pure cold (no traces)"},
		{1, 0.0, "pure cold (below threshold)"},
		{2, 0.0, "pure cold (at threshold - 1)"},
		{3, 0.0, "transition start (at minTracesForWarm)"},
		{9, 0.5, "mid transition"},
		{15, 1.0, "pure warm (at fullWarmTraces)"},
		{20, 1.0, "pure warm (above fullWarmTraces)"},
	}

	for _, tc := range traceProgression {
		blendRatio := retriever.ComputeBlendRatioDirect(tc.traceCount)

		if blendRatio != tc.expectedRatio {
			t.Errorf("%s (traces=%d): expected ratio=%f, got %f",
				tc.description, tc.traceCount, tc.expectedRatio, blendRatio)
		}
	}
}

// TestColdStartIntegration_ColdPriorScoring tests that cold prior scoring
// correctly weights event types by importance.
func TestColdStartIntegration_ColdPriorScoring(t *testing.T) {
	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, nil, nil)
	prior := retriever.ColdPrior()

	// Test that high-value event types have higher importance
	highValueTypes := []events.EventType{
		events.EventTypeAgentDecision,
		events.EventTypeFailure,
		events.EventTypeSuccess,
	}

	lowValueTypes := []events.EventType{
		events.EventTypeIndexFileAdded,
		events.EventTypeLLMRequest,
		events.EventTypeContextEviction,
	}

	for _, hvt := range highValueTypes {
		for _, lvt := range lowValueTypes {
			hvScore := prior.TypeImportance[hvt]
			lvScore := prior.TypeImportance[lvt]

			if hvScore <= lvScore {
				t.Errorf("Expected %s (%.2f) > %s (%.2f)",
					hvt.String(), hvScore, lvt.String(), lvScore)
			}
		}
	}
}

// TestColdStartIntegration_SemanticSimilarityAffectsColdScore tests that
// semantic similarity affects cold-start scoring.
func TestColdStartIntegration_SemanticSimilarityAffectsColdScore(t *testing.T) {
	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, nil, nil)
	prior := retriever.ColdPrior()

	// Create mock embeddings (high similarity vs low similarity)
	queryEmbed := []float32{1.0, 0.0, 0.0}
	similarEmbed := []float32{0.9, 0.1, 0.0}   // High cosine similarity
	dissimilarEmbed := []float32{0.0, 1.0, 0.0} // Low cosine similarity

	// Same event type and turn for both
	eventType := events.EventTypeAgentDecision
	currentTurn := 100
	eventTurn := 99

	scoreSimilar := prior.ComputeColdScore(eventType, queryEmbed, similarEmbed, currentTurn, eventTurn)
	scoreDissimilar := prior.ComputeColdScore(eventType, queryEmbed, dissimilarEmbed, currentTurn, eventTurn)

	if scoreSimilar <= scoreDissimilar {
		t.Errorf("Expected similar embedding to score higher: similar=%f, dissimilar=%f",
			scoreSimilar, scoreDissimilar)
	}
}

// TestColdStartIntegration_RecencyAffectsColdScore tests that temporal
// recency affects cold-start scoring.
func TestColdStartIntegration_RecencyAffectsColdScore(t *testing.T) {
	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, nil, nil)
	prior := retriever.ColdPrior()

	// Same event type and embeddings
	eventType := events.EventTypeAgentDecision
	queryEmbed := []float32{1.0, 0.0, 0.0}
	eventEmbed := []float32{1.0, 0.0, 0.0}
	currentTurn := 100

	scoreRecent := prior.ComputeColdScore(eventType, queryEmbed, eventEmbed, currentTurn, 99)
	scoreOld := prior.ComputeColdScore(eventType, queryEmbed, eventEmbed, currentTurn, 50)

	if scoreRecent <= scoreOld {
		t.Errorf("Expected recent event to score higher: recent=%f, old=%f",
			scoreRecent, scoreOld)
	}
}

// TestColdStartIntegration_CrossSessionDecay tests that cross-session
// decay is applied correctly to scores.
func TestColdStartIntegration_CrossSessionDecay(t *testing.T) {
	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, nil, nil)

	now := time.Now()
	baseScore := 1.0

	// Test decay over time
	// Decay formula: score * exp(-daysSince / (crossSessionDecayDays * ln(2)))
	// With default crossSessionDecayDays=7:
	// - 7 days: exp(-7 / (7 * ln(2))) = exp(-1/ln(2)) ~ 0.236
	// - 14 days: exp(-2/ln(2)) ~ 0.056
	// - 30 days: exp(-30 / (7 * ln(2))) ~ 0.002
	testCases := []struct {
		daysAgo        int
		minExpected    float64
		maxExpected    float64
	}{
		{0, 0.99, 1.01},      // Same day - minimal decay
		{7, 0.20, 0.30},      // One week - significant decay (~0.236)
		{14, 0.04, 0.08},     // Two weeks - heavy decay (~0.056)
		{30, 0.00, 0.01},     // One month - near zero
	}

	for _, tc := range testCases {
		eventTime := now.AddDate(0, 0, -tc.daysAgo)
		decayedScore := retriever.ApplyCrossSessionDecayDirect(baseScore, eventTime, now)

		if decayedScore < tc.minExpected || decayedScore > tc.maxExpected {
			t.Errorf("%d days ago: expected score in [%f, %f], got %f",
				tc.daysAgo, tc.minExpected, tc.maxExpected, decayedScore)
		}
	}
}

// TestColdStartIntegration_LoadCrossSessionState tests loading of
// cross-session state from prior sessions.
func TestColdStartIntegration_LoadCrossSessionState(t *testing.T) {
	mockLoader := newColdStartMockSessionLoader()
	retriever := coldstart.NewArchivalistRetriever(nil, nil, mockLoader)

	// Set up mock session data
	mockLoader.SetSessions([]coldstart.SessionSummary{
		{
			SessionID:     "session-1",
			ProjectID:     "project-1",
			StartedAt:     time.Now().AddDate(0, 0, -2),
			EndedAt:       time.Now().AddDate(0, 0, -1),
			EventCount:    10,
			TopEventIDs:   []string{"evt-1", "evt-2"},
			TopEventTypes: map[events.EventType]int{events.EventTypeAgentDecision: 5},
		},
	})

	// Load cross-session state
	err := retriever.LoadCrossSessionState(context.Background(), "project-1", 1.0)
	if err != nil {
		t.Fatalf("LoadCrossSessionState failed: %v", err)
	}

	// Verify state was loaded
	if !retriever.HasCrossSessionState() {
		t.Error("Expected cross-session state to be loaded")
	}

	if retriever.GetLoadedSessionCount() != 1 {
		t.Errorf("Expected 1 loaded session, got %d", retriever.GetLoadedSessionCount())
	}

	// Verify decay factor is reasonable
	decayFactor := retriever.GetDecayFactor()
	if decayFactor <= 0 || decayFactor > 1 {
		t.Errorf("Expected decay factor in (0,1], got %f", decayFactor)
	}
}

// TestColdStartIntegration_NilSessionLoader tests behavior when no
// session loader is configured.
func TestColdStartIntegration_NilSessionLoader(t *testing.T) {
	// Create retriever without session loader
	retriever := coldstart.NewArchivalistRetriever(nil, nil, nil)

	// Should not error, but create empty state
	err := retriever.LoadCrossSessionState(context.Background(), "project-1", 1.0)
	if err != nil {
		t.Fatalf("Expected no error with nil loader, got: %v", err)
	}

	// State should be created but empty
	if !retriever.HasCrossSessionState() {
		t.Error("Expected empty cross-session state to be created")
	}

	if retriever.GetLoadedSessionCount() != 0 {
		t.Errorf("Expected 0 loaded sessions, got %d", retriever.GetLoadedSessionCount())
	}
}

// TestColdStartIntegration_ClearCrossSessionState tests clearing of
// cross-session state.
func TestColdStartIntegration_ClearCrossSessionState(t *testing.T) {
	mockLoader := newColdStartMockSessionLoader()
	retriever := coldstart.NewArchivalistRetriever(nil, nil, mockLoader)

	mockLoader.SetSessions([]coldstart.SessionSummary{{SessionID: "session-1"}})
	_ = retriever.LoadCrossSessionState(context.Background(), "project-1", 1.0)

	if !retriever.HasCrossSessionState() {
		t.Fatal("Expected state to be loaded")
	}

	retriever.ClearCrossSessionState()

	if retriever.HasCrossSessionState() {
		t.Error("Expected state to be cleared")
	}
}

// TestColdStartIntegration_ActivationToScore tests the sigmoid transformation
// from ACT-R activation to normalized score.
func TestColdStartIntegration_ActivationToScore(t *testing.T) {
	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, nil, nil)

	testCases := []struct {
		activation float64
		minScore   float64
		maxScore   float64
	}{
		{-100, 0.0, 0.01},      // Very negative - near 0
		{-10, 0.0, 0.01},       // Negative - very low
		{-2, 0.40, 0.60},       // Around center
		{0, 0.85, 0.95},        // Neutral - high
		{5, 0.99, 1.0},         // Positive - near 1
		{20, 0.99, 1.0},        // Very positive - saturated at 1
	}

	for _, tc := range testCases {
		score := retriever.ActivationToScoreDirect(tc.activation)
		if score < tc.minScore || score > tc.maxScore {
			t.Errorf("Activation %f: expected score in [%f, %f], got %f",
				tc.activation, tc.minScore, tc.maxScore, score)
		}
	}
}

// TestColdStartIntegration_ConfigurableThresholds tests that trace
// thresholds can be configured via options.
func TestColdStartIntegration_ConfigurableThresholds(t *testing.T) {
	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(
		nil, nil, nil,
		coldstart.WithMinTracesForWarm(5),
		coldstart.WithFullWarmTraces(20),
	)

	if retriever.MinTracesForWarm() != 5 {
		t.Errorf("Expected MinTracesForWarm=5, got %d", retriever.MinTracesForWarm())
	}

	if retriever.FullWarmTraces() != 20 {
		t.Errorf("Expected FullWarmTraces=20, got %d", retriever.FullWarmTraces())
	}

	// Test blend ratio with custom thresholds
	ratio := retriever.ComputeBlendRatioDirect(12) // Midpoint between 5 and 20
	expectedRatio := 7.0 / 15.0 // (12-5) / (20-5)

	if ratio < expectedRatio-0.01 || ratio > expectedRatio+0.01 {
		t.Errorf("Expected ratio ~%f at trace count 12, got %f", expectedRatio, ratio)
	}
}

// =============================================================================
// Mock Implementations for Cold-Start Tests
// =============================================================================

// coldStartMockBleveSearcher implements BleveSearcherInterface for testing.
type coldStartMockBleveSearcher struct {
	results []*events.ActivityEvent
	err     error
}

func newColdStartMockBleveSearcher() *coldStartMockBleveSearcher {
	return &coldStartMockBleveSearcher{
		results: make([]*events.ActivityEvent, 0),
	}
}

func (m *coldStartMockBleveSearcher) Search(ctx context.Context, query string, limit int) ([]*events.ActivityEvent, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.results) > limit {
		return m.results[:limit], nil
	}
	return m.results, nil
}

func (m *coldStartMockBleveSearcher) SetResults(results []*events.ActivityEvent) {
	m.results = results
}

// coldStartMockVectorSearcher implements VectorSearcherInterface for testing.
type coldStartMockVectorSearcher struct {
	results []*events.ActivityEvent
	err     error
}

func newColdStartMockVectorSearcher() *coldStartMockVectorSearcher {
	return &coldStartMockVectorSearcher{
		results: make([]*events.ActivityEvent, 0),
	}
}

func (m *coldStartMockVectorSearcher) Search(ctx context.Context, queryText string, limit int) ([]*events.ActivityEvent, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.results) > limit {
		return m.results[:limit], nil
	}
	return m.results, nil
}

func (m *coldStartMockVectorSearcher) SetResults(results []*events.ActivityEvent) {
	m.results = results
}

// coldStartMockSessionLoader implements SessionSummaryLoader for testing.
type coldStartMockSessionLoader struct {
	sessions []coldstart.SessionSummary
	events   map[string][]*events.ActivityEvent
	loadErr  error
}

func newColdStartMockSessionLoader() *coldStartMockSessionLoader {
	return &coldStartMockSessionLoader{
		sessions: make([]coldstart.SessionSummary, 0),
		events:   make(map[string][]*events.ActivityEvent),
	}
}

func (m *coldStartMockSessionLoader) LoadRecentSessions(ctx context.Context, projectID string, limit int) ([]coldstart.SessionSummary, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	if len(m.sessions) > limit {
		return m.sessions[:limit], nil
	}
	return m.sessions, nil
}

func (m *coldStartMockSessionLoader) LoadSessionEvents(ctx context.Context, sessionID string, eventIDs []string) ([]*events.ActivityEvent, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.events[sessionID], nil
}

func (m *coldStartMockSessionLoader) SetSessions(sessions []coldstart.SessionSummary) {
	m.sessions = sessions
}

func (m *coldStartMockSessionLoader) SetSessionEvents(sessionID string, events []*events.ActivityEvent) {
	m.events[sessionID] = events
}

// =============================================================================
// Helper Functions
// =============================================================================

func createColdStartTestEvent(id string, eventType events.EventType, content string) *events.ActivityEvent {
	event := events.NewActivityEvent(eventType, "test-session-cold", content)
	event.ID = id // Override the generated UUID with the specified ID
	event.AgentID = "test-agent-cold"
	return event
}
