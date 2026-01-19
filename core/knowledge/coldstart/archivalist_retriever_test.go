package coldstart

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/knowledge/memory"
	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// Mock SessionSummaryLoader
// =============================================================================

type mockSessionLoader struct {
	sessions []SessionSummary
	events   map[string][]*events.ActivityEvent
	loadErr  error
}

func (m *mockSessionLoader) LoadRecentSessions(ctx context.Context, projectID string, limit int) ([]SessionSummary, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	if limit > len(m.sessions) {
		return m.sessions, nil
	}
	return m.sessions[:limit], nil
}

func (m *mockSessionLoader) LoadSessionEvents(ctx context.Context, sessionID string, eventIDs []string) ([]*events.ActivityEvent, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	sessionEvents, ok := m.events[sessionID]
	if !ok {
		return nil, nil
	}

	// Filter by requested IDs
	result := make([]*events.ActivityEvent, 0)
	for _, e := range sessionEvents {
		for _, id := range eventIDs {
			if e.ID == id {
				result = append(result, e)
				break
			}
		}
	}
	return result, nil
}

// =============================================================================
// Test Helpers
// =============================================================================

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	// Create required tables
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER DEFAULT 0,
			memory_activation REAL,
			last_accessed_at INTEGER,
			access_count INTEGER DEFAULT 0,
			base_offset REAL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS node_access_traces (
			node_id TEXT,
			accessed_at INTEGER,
			access_type TEXT,
			context TEXT
		);
		CREATE TABLE IF NOT EXISTS decay_parameters (
			domain INTEGER PRIMARY KEY,
			decay_exponent_alpha REAL,
			decay_exponent_beta REAL,
			base_offset_mean REAL,
			base_offset_variance REAL,
			effective_samples REAL,
			updated_at TEXT
		);
	`)
	if err != nil {
		t.Fatalf("failed to create tables: %v", err)
	}

	return db
}

func createTestRetriever(t *testing.T, loader SessionSummaryLoader) (*ArchivalistRetriever, *sql.DB) {
	db := setupTestDB(t)
	memStore := memory.NewMemoryStore(db, 100, time.Minute)
	retriever := NewArchivalistRetriever(nil, memStore, loader)
	return retriever, db
}

// =============================================================================
// AE.5.7 Tests - LoadCrossSessionState
// =============================================================================

func TestNewArchivalistRetriever(t *testing.T) {
	retriever := NewArchivalistRetriever(nil, nil, nil)

	if retriever == nil {
		t.Fatal("expected non-nil retriever")
	}

	if retriever.actionPrior == nil {
		t.Error("expected default action prior to be set")
	}

	if retriever.crossSessionDecayDays != DefaultCrossSessionDecayDays {
		t.Errorf("expected default decay days %v, got %v",
			DefaultCrossSessionDecayDays, retriever.crossSessionDecayDays)
	}
}

func TestLoadCrossSessionStateNoLoader(t *testing.T) {
	retriever := NewArchivalistRetriever(nil, nil, nil)

	err := retriever.LoadCrossSessionState(context.Background(), "project-1", 5.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !retriever.HasCrossSessionState() {
		t.Error("expected cross-session state to be set")
	}

	count := retriever.GetLoadedSessionCount()
	if count != 0 {
		t.Errorf("expected 0 sessions, got %d", count)
	}
}

func TestLoadCrossSessionStateWithSessions(t *testing.T) {
	loader := &mockSessionLoader{
		sessions: []SessionSummary{
			{
				SessionID:   "session-1",
				ProjectID:   "project-1",
				StartedAt:   time.Now().Add(-48 * time.Hour),
				EndedAt:     time.Now().Add(-47 * time.Hour),
				EventCount:  10,
				TopEventIDs: []string{"event-1", "event-2"},
				TopEventTypes: map[events.EventType]int{
					events.EventTypeAgentDecision: 3,
					events.EventTypeToolCall:      7,
				},
			},
			{
				SessionID:   "session-2",
				ProjectID:   "project-1",
				StartedAt:   time.Now().Add(-24 * time.Hour),
				EndedAt:     time.Now().Add(-23 * time.Hour),
				EventCount:  5,
				TopEventIDs: []string{"event-3"},
				TopEventTypes: map[events.EventType]int{
					events.EventTypeSuccess: 5,
				},
			},
		},
		events: map[string][]*events.ActivityEvent{
			"session-1": {
				{
					ID:         "event-1",
					EventType:  events.EventTypeAgentDecision,
					Timestamp:  time.Now().Add(-48 * time.Hour),
					Importance: 0.9,
				},
				{
					ID:         "event-2",
					EventType:  events.EventTypeToolCall,
					Timestamp:  time.Now().Add(-47 * time.Hour),
					Importance: 0.5,
				},
			},
			"session-2": {
				{
					ID:         "event-3",
					EventType:  events.EventTypeSuccess,
					Timestamp:  time.Now().Add(-24 * time.Hour),
					Importance: 0.75,
				},
			},
		},
	}

	retriever := NewArchivalistRetriever(nil, nil, loader)

	err := retriever.LoadCrossSessionState(context.Background(), "project-1", 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !retriever.HasCrossSessionState() {
		t.Error("expected cross-session state to be set")
	}

	count := retriever.GetLoadedSessionCount()
	if count != 2 {
		t.Errorf("expected 2 sessions, got %d", count)
	}

	state := retriever.GetCrossSessionState()
	if state == nil {
		t.Fatal("expected non-nil state")
	}

	if state.ProjectID != "project-1" {
		t.Errorf("expected project-1, got %s", state.ProjectID)
	}

	if len(state.InheritedTraces) != 3 {
		t.Errorf("expected 3 inherited traces, got %d", len(state.InheritedTraces))
	}
}

func TestCrossSessionDecayFactorCalculation(t *testing.T) {
	loader := &mockSessionLoader{
		sessions: []SessionSummary{},
	}

	retriever := NewArchivalistRetriever(nil, nil, loader)

	testCases := []struct {
		name             string
		daysSinceSession float64
		decayDays        float64
		expectedFactor   float64
	}{
		{
			name:             "zero days - no decay",
			daysSinceSession: 0.0,
			decayDays:        7.0,
			expectedFactor:   1.0,
		},
		{
			name:             "one time constant - 37% remaining",
			daysSinceSession: 7.0,
			decayDays:        7.0,
			expectedFactor:   math.Exp(-1.0), // ~0.368
		},
		{
			name:             "two time constants - 13.5% remaining",
			daysSinceSession: 14.0,
			decayDays:        7.0,
			expectedFactor:   math.Exp(-2.0), // ~0.135
		},
		{
			name:             "half time constant",
			daysSinceSession: 3.5,
			decayDays:        7.0,
			expectedFactor:   math.Exp(-0.5), // ~0.606
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			retriever.SetCrossSessionDecayDays(tc.decayDays)

			err := retriever.LoadCrossSessionState(context.Background(), "project-1", tc.daysSinceSession)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			decayFactor := retriever.GetDecayFactor()
			if math.Abs(decayFactor-tc.expectedFactor) > 0.001 {
				t.Errorf("expected decay factor %v, got %v", tc.expectedFactor, decayFactor)
			}
		})
	}
}

func TestInheritedActivationDecay(t *testing.T) {
	loader := &mockSessionLoader{
		sessions: []SessionSummary{
			{
				SessionID:   "session-1",
				ProjectID:   "project-1",
				TopEventIDs: []string{"event-1"},
				TopEventTypes: map[events.EventType]int{
					events.EventTypeAgentDecision: 1,
				},
			},
		},
		events: map[string][]*events.ActivityEvent{
			"session-1": {
				{
					ID:         "event-1",
					EventType:  events.EventTypeAgentDecision,
					Timestamp:  time.Now().Add(-7 * 24 * time.Hour),
					Importance: 0.9,
				},
			},
		},
	}

	retriever := NewArchivalistRetriever(nil, nil, loader)

	// Load with exactly one decay time constant elapsed
	err := retriever.LoadCrossSessionState(context.Background(), "project-1", 7.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Original activation is 0.9, with e^(-1) decay factor
	expectedActivation := 0.9 * math.Exp(-1.0)

	actualActivation := retriever.GetInheritedActivation("event-1")
	if math.Abs(actualActivation-expectedActivation) > 0.001 {
		t.Errorf("expected activation %v, got %v", expectedActivation, actualActivation)
	}

	// Non-existent event should return 0
	if retriever.GetInheritedActivation("non-existent") != 0 {
		t.Error("expected 0 for non-existent event")
	}
}

func TestCrossSessionStateAggregatesEventTypes(t *testing.T) {
	loader := &mockSessionLoader{
		sessions: []SessionSummary{
			{
				SessionID: "session-1",
				ProjectID: "project-1",
				TopEventTypes: map[events.EventType]int{
					events.EventTypeAgentDecision: 5,
					events.EventTypeToolCall:      3,
				},
			},
			{
				SessionID: "session-2",
				ProjectID: "project-1",
				TopEventTypes: map[events.EventType]int{
					events.EventTypeAgentDecision: 2,
					events.EventTypeSuccess:       4,
				},
			},
		},
	}

	retriever := NewArchivalistRetriever(nil, nil, loader)

	err := retriever.LoadCrossSessionState(context.Background(), "project-1", 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state := retriever.GetCrossSessionState()

	// Check aggregated event types
	if state.TopEventTypes[events.EventTypeAgentDecision] != 7 {
		t.Errorf("expected 7 agent decisions, got %d", state.TopEventTypes[events.EventTypeAgentDecision])
	}
	if state.TopEventTypes[events.EventTypeToolCall] != 3 {
		t.Errorf("expected 3 tool calls, got %d", state.TopEventTypes[events.EventTypeToolCall])
	}
	if state.TopEventTypes[events.EventTypeSuccess] != 4 {
		t.Errorf("expected 4 successes, got %d", state.TopEventTypes[events.EventTypeSuccess])
	}
}

func TestLowActivationTracesFiltered(t *testing.T) {
	loader := &mockSessionLoader{
		sessions: []SessionSummary{
			{
				SessionID:   "session-1",
				ProjectID:   "project-1",
				TopEventIDs: []string{"event-high", "event-low"},
				TopEventTypes: map[events.EventType]int{
					events.EventTypeAgentDecision: 2,
				},
			},
		},
		events: map[string][]*events.ActivityEvent{
			"session-1": {
				{
					ID:         "event-high",
					EventType:  events.EventTypeAgentDecision,
					Timestamp:  time.Now().Add(-7 * 24 * time.Hour),
					Importance: 0.5, // Will decay to ~0.18, above threshold
				},
				{
					ID:         "event-low",
					EventType:  events.EventTypeAgentDecision,
					Timestamp:  time.Now().Add(-7 * 24 * time.Hour),
					Importance: 0.02, // Will decay to ~0.007, below 0.01 threshold
				},
			},
		},
	}

	retriever := NewArchivalistRetriever(nil, nil, loader)

	// Load with decay
	err := retriever.LoadCrossSessionState(context.Background(), "project-1", 7.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state := retriever.GetCrossSessionState()

	// Only high-importance event should be inherited
	if len(state.InheritedTraces) != 1 {
		t.Errorf("expected 1 inherited trace, got %d", len(state.InheritedTraces))
	}

	if state.InheritedTraces[0].EventID != "event-high" {
		t.Error("expected event-high to be inherited")
	}
}

// =============================================================================
// AE.5.8 Tests - RecordRetrieval
// =============================================================================

func TestRecordRetrievalDelegation(t *testing.T) {
	retriever, db := createTestRetriever(t, nil)
	defer db.Close()

	// Insert a test node
	_, err := db.Exec(`INSERT INTO nodes (id, domain) VALUES ('event-1', 0)`)
	if err != nil {
		t.Fatalf("failed to insert test node: %v", err)
	}

	// Record a useful retrieval
	err = retriever.RecordRetrieval(context.Background(), "event-1", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify trace was recorded
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM node_access_traces WHERE node_id = 'event-1'`).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query traces: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 trace, got %d", count)
	}

	// Verify access type and context
	var accessType, contextStr string
	err = db.QueryRow(`SELECT access_type, context FROM node_access_traces WHERE node_id = 'event-1'`).Scan(&accessType, &contextStr)
	if err != nil {
		t.Fatalf("failed to query trace details: %v", err)
	}

	if accessType != "retrieval" {
		t.Errorf("expected access type 'retrieval', got %s", accessType)
	}

	if contextStr != "retrieval:useful" {
		t.Errorf("expected context 'retrieval:useful', got %s", contextStr)
	}
}

func TestRecordRetrievalNotUseful(t *testing.T) {
	retriever, db := createTestRetriever(t, nil)
	defer db.Close()

	// Insert a test node
	_, err := db.Exec(`INSERT INTO nodes (id, domain) VALUES ('event-2', 0)`)
	if err != nil {
		t.Fatalf("failed to insert test node: %v", err)
	}

	// Record a not-useful retrieval
	err = retriever.RecordRetrieval(context.Background(), "event-2", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify context indicates not useful
	var contextStr string
	err = db.QueryRow(`SELECT context FROM node_access_traces WHERE node_id = 'event-2'`).Scan(&contextStr)
	if err != nil {
		t.Fatalf("failed to query context: %v", err)
	}

	if contextStr != "retrieval:not_useful" {
		t.Errorf("expected context 'retrieval:not_useful', got %s", contextStr)
	}
}

func TestRecordRetrievalNoMemoryStore(t *testing.T) {
	retriever := NewArchivalistRetriever(nil, nil, nil)

	// Should not error when no memory store
	err := retriever.RecordRetrieval(context.Background(), "event-1", true)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// =============================================================================
// Helper Method Tests
// =============================================================================

func TestSetGetCrossSessionDecayDays(t *testing.T) {
	retriever := NewArchivalistRetriever(nil, nil, nil)

	// Default value
	if retriever.GetCrossSessionDecayDays() != DefaultCrossSessionDecayDays {
		t.Errorf("expected default %v", DefaultCrossSessionDecayDays)
	}

	// Set new value
	retriever.SetCrossSessionDecayDays(14.0)
	if retriever.GetCrossSessionDecayDays() != 14.0 {
		t.Errorf("expected 14.0, got %v", retriever.GetCrossSessionDecayDays())
	}

	// Invalid value should not change
	retriever.SetCrossSessionDecayDays(-1.0)
	if retriever.GetCrossSessionDecayDays() != 14.0 {
		t.Errorf("expected 14.0 unchanged, got %v", retriever.GetCrossSessionDecayDays())
	}

	retriever.SetCrossSessionDecayDays(0)
	if retriever.GetCrossSessionDecayDays() != 14.0 {
		t.Errorf("expected 14.0 unchanged, got %v", retriever.GetCrossSessionDecayDays())
	}
}

func TestClearCrossSessionState(t *testing.T) {
	loader := &mockSessionLoader{
		sessions: []SessionSummary{
			{SessionID: "session-1", ProjectID: "project-1"},
		},
	}

	retriever := NewArchivalistRetriever(nil, nil, loader)

	// Load state
	err := retriever.LoadCrossSessionState(context.Background(), "project-1", 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !retriever.HasCrossSessionState() {
		t.Error("expected state to be loaded")
	}

	// Clear state
	retriever.ClearCrossSessionState()

	if retriever.HasCrossSessionState() {
		t.Error("expected state to be cleared")
	}

	if retriever.GetLoadedSessionCount() != 0 {
		t.Error("expected 0 sessions after clear")
	}

	if retriever.GetCrossSessionState() != nil {
		t.Error("expected nil state after clear")
	}
}

func TestGetDecayFactorNoState(t *testing.T) {
	retriever := NewArchivalistRetriever(nil, nil, nil)

	if retriever.GetDecayFactor() != 0 {
		t.Error("expected 0 decay factor when no state loaded")
	}
}

func TestGetInheritedActivationNoState(t *testing.T) {
	retriever := NewArchivalistRetriever(nil, nil, nil)

	if retriever.GetInheritedActivation("any-event") != 0 {
		t.Error("expected 0 activation when no state loaded")
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestConcurrentStateAccess(t *testing.T) {
	loader := &mockSessionLoader{
		sessions: []SessionSummary{
			{
				SessionID:   "session-1",
				ProjectID:   "project-1",
				TopEventIDs: []string{"event-1"},
				TopEventTypes: map[events.EventType]int{
					events.EventTypeAgentDecision: 1,
				},
			},
		},
		events: map[string][]*events.ActivityEvent{
			"session-1": {
				{
					ID:         "event-1",
					EventType:  events.EventTypeAgentDecision,
					Importance: 0.9,
				},
			},
		},
	}

	retriever := NewArchivalistRetriever(nil, nil, loader)

	// Load initial state
	err := retriever.LoadCrossSessionState(context.Background(), "project-1", 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Concurrent reads and writes
	done := make(chan bool, 100)

	// Readers
	for i := 0; i < 50; i++ {
		go func() {
			retriever.GetCrossSessionState()
			retriever.GetLoadedSessionCount()
			retriever.GetInheritedActivation("event-1")
			retriever.GetCrossSessionDecayDays()
			retriever.HasCrossSessionState()
			done <- true
		}()
	}

	// Writers
	for i := 0; i < 50; i++ {
		go func(i int) {
			if i%2 == 0 {
				retriever.SetCrossSessionDecayDays(float64(i + 1))
			} else {
				retriever.LoadCrossSessionState(context.Background(), "project-1", float64(i))
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}
}

// =============================================================================
// AE.5.4-5.6 Mock Implementations
// =============================================================================

// mockMemoryStoreInterface implements MemoryStoreInterface for testing.
type mockMemoryStoreInterface struct {
	memories map[string]*memory.ACTRMemory
	accesses []mockAccessRecord
}

type mockAccessRecord struct {
	nodeID     string
	accessType memory.AccessType
	context    string
}

func newMockMemoryStoreInterface() *mockMemoryStoreInterface {
	return &mockMemoryStoreInterface{
		memories: make(map[string]*memory.ACTRMemory),
		accesses: make([]mockAccessRecord, 0),
	}
}

func (m *mockMemoryStoreInterface) GetMemory(ctx context.Context, nodeID string, d domain.Domain) (*memory.ACTRMemory, error) {
	mem, ok := m.memories[nodeID]
	if !ok {
		return nil, nil
	}
	return mem, nil
}

func (m *mockMemoryStoreInterface) RecordAccess(ctx context.Context, nodeID string, accessType memory.AccessType, contextStr string) error {
	m.accesses = append(m.accesses, mockAccessRecord{
		nodeID:     nodeID,
		accessType: accessType,
		context:    contextStr,
	})
	return nil
}

func (m *mockMemoryStoreInterface) SetMemory(nodeID string, mem *memory.ACTRMemory) {
	m.memories[nodeID] = mem
}

// mockBleveSearcherInterface implements BleveSearcherInterface for testing.
type mockBleveSearcherInterface struct {
	results      []*events.ActivityEvent
	err          error
	searchCalled bool
	query        string
}

func (m *mockBleveSearcherInterface) Search(ctx context.Context, query string, limit int) ([]*events.ActivityEvent, error) {
	m.searchCalled = true
	m.query = query
	if m.err != nil {
		return nil, m.err
	}
	if limit < len(m.results) {
		return m.results[:limit], nil
	}
	return m.results, nil
}

// mockVectorSearcherInterface implements VectorSearcherInterface for testing.
type mockVectorSearcherInterface struct {
	results      []*events.ActivityEvent
	err          error
	searchCalled bool
	query        string
}

func (m *mockVectorSearcherInterface) Search(ctx context.Context, queryText string, limit int) ([]*events.ActivityEvent, error) {
	m.searchCalled = true
	m.query = queryText
	if m.err != nil {
		return nil, m.err
	}
	if limit < len(m.results) {
		return m.results[:limit], nil
	}
	return m.results, nil
}

// =============================================================================
// AE.5.4-5.6 Helper Functions
// =============================================================================

func createSearchTestEvent(id string, eventType events.EventType, timestamp time.Time) *events.ActivityEvent {
	return &events.ActivityEvent{
		ID:        id,
		EventType: eventType,
		Timestamp: timestamp,
		SessionID: "test-session",
		Content:   "test content",
		Data:      make(map[string]any),
	}
}

func createSearchTestEventWithEmbed(id string, eventType events.EventType, timestamp time.Time, embed []float32) *events.ActivityEvent {
	event := createSearchTestEvent(id, eventType, timestamp)
	event.Data["embedding"] = embed
	return event
}

func createSearchTestMemory(nodeID string, traceCount int, now time.Time) *memory.ACTRMemory {
	traces := make([]memory.AccessTrace, traceCount)
	for i := 0; i < traceCount; i++ {
		traces[i] = memory.AccessTrace{
			AccessedAt: now.Add(-time.Duration(i) * time.Minute),
			AccessType: memory.AccessRetrieval,
			Context:    "test",
		}
	}

	return &memory.ACTRMemory{
		NodeID:             nodeID,
		Domain:             int(domain.DomainArchivalist),
		Traces:             traces,
		MaxTraces:          100,
		DecayAlpha:         5.0,
		DecayBeta:          5.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		CreatedAt:          now.Add(-time.Hour),
		AccessCount:        traceCount,
	}
}

// =============================================================================
// AE.5.4 Tests - ArchivalistRetriever Type and Configuration
// =============================================================================

func TestNewArchivalistRetrieverWithInterfaces(t *testing.T) {
	memStore := newMockMemoryStoreInterface()
	bleve := &mockBleveSearcherInterface{}
	vector := &mockVectorSearcherInterface{}

	retriever := NewArchivalistRetrieverWithInterfaces(memStore, bleve, vector)

	if retriever == nil {
		t.Fatal("expected non-nil retriever")
	}
	if retriever.actionPrior == nil {
		t.Error("expected non-nil actionPrior")
	}
	if retriever.minTracesForWarm != DefaultMinTracesForWarm {
		t.Errorf("expected minTracesForWarm=%d, got %d", DefaultMinTracesForWarm, retriever.minTracesForWarm)
	}
	if retriever.fullWarmTraces != DefaultFullWarmTraces {
		t.Errorf("expected fullWarmTraces=%d, got %d", DefaultFullWarmTraces, retriever.fullWarmTraces)
	}
	if retriever.crossSessionDecayDays != DefaultCrossSessionDecayDays {
		t.Errorf("expected crossSessionDecayDays=%f, got %f", DefaultCrossSessionDecayDays, retriever.crossSessionDecayDays)
	}
}

func TestRetrieverOptions(t *testing.T) {
	customPrior := &ActionTypePrior{
		TypeImportance: map[events.EventType]float64{
			events.EventTypeAgentDecision: 1.0,
		},
	}

	retriever := NewArchivalistRetrieverWithInterfaces(
		nil, nil, nil,
		WithMinTracesForWarm(5),
		WithFullWarmTraces(20),
		WithCrossSessionDecayDaysOpt(14.0),
		WithColdPrior(customPrior),
	)

	if retriever.minTracesForWarm != 5 {
		t.Errorf("expected minTracesForWarm=5, got %d", retriever.minTracesForWarm)
	}
	if retriever.fullWarmTraces != 20 {
		t.Errorf("expected fullWarmTraces=20, got %d", retriever.fullWarmTraces)
	}
	if retriever.crossSessionDecayDays != 14.0 {
		t.Errorf("expected crossSessionDecayDays=14.0, got %f", retriever.crossSessionDecayDays)
	}
	if retriever.actionPrior != customPrior {
		t.Error("expected custom actionPrior to be set")
	}
}

func TestRetrieverOptionsInvalidValues(t *testing.T) {
	retriever := NewArchivalistRetrieverWithInterfaces(
		nil, nil, nil,
		WithMinTracesForWarm(-1),          // Should be ignored
		WithFullWarmTraces(0),             // Should be ignored
		WithCrossSessionDecayDaysOpt(-5.0), // Should be ignored
	)

	// Defaults should be preserved
	if retriever.minTracesForWarm != DefaultMinTracesForWarm {
		t.Errorf("invalid minTracesForWarm should be ignored, got %d", retriever.minTracesForWarm)
	}
	if retriever.fullWarmTraces != DefaultFullWarmTraces {
		t.Errorf("invalid fullWarmTraces should be ignored, got %d", retriever.fullWarmTraces)
	}
	if retriever.crossSessionDecayDays != DefaultCrossSessionDecayDays {
		t.Errorf("invalid crossSessionDecayDays should be ignored, got %f", retriever.crossSessionDecayDays)
	}
}

func TestRetrieverAccessors(t *testing.T) {
	retriever := NewArchivalistRetrieverWithInterfaces(nil, nil, nil)

	if retriever.ColdPrior() == nil {
		t.Error("ColdPrior() should return non-nil")
	}
	if retriever.MinTracesForWarm() != DefaultMinTracesForWarm {
		t.Errorf("MinTracesForWarm() = %d, want %d", retriever.MinTracesForWarm(), DefaultMinTracesForWarm)
	}
	if retriever.FullWarmTraces() != DefaultFullWarmTraces {
		t.Errorf("FullWarmTraces() = %d, want %d", retriever.FullWarmTraces(), DefaultFullWarmTraces)
	}
}

// =============================================================================
// AE.5.5 Tests - Search Method
// =============================================================================

func TestSearchBasic(t *testing.T) {
	now := time.Now()
	event1 := createSearchTestEvent("event-1", events.EventTypeAgentDecision, now)
	event2 := createSearchTestEvent("event-2", events.EventTypeToolResult, now)

	bleve := &mockBleveSearcherInterface{results: []*events.ActivityEvent{event1}}
	vector := &mockVectorSearcherInterface{results: []*events.ActivityEvent{event2}}

	retriever := NewArchivalistRetrieverWithInterfaces(nil, bleve, vector)

	results, err := retriever.Search(context.Background(), "test query", nil, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if !bleve.searchCalled {
		t.Error("bleve search should have been called")
	}
	if !vector.searchCalled {
		t.Error("vector search should have been called")
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestSearchDeduplication(t *testing.T) {
	now := time.Now()
	event1 := createSearchTestEvent("event-1", events.EventTypeAgentDecision, now)
	event2 := createSearchTestEvent("event-1", events.EventTypeAgentDecision, now) // Same ID

	bleve := &mockBleveSearcherInterface{results: []*events.ActivityEvent{event1}}
	vector := &mockVectorSearcherInterface{results: []*events.ActivityEvent{event2}}

	retriever := NewArchivalistRetrieverWithInterfaces(nil, bleve, vector)

	results, err := retriever.Search(context.Background(), "test", nil, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Should be deduplicated to 1 result
	if len(results) != 1 {
		t.Errorf("expected 1 deduplicated result, got %d", len(results))
	}
}

func TestSearchWithEventTypeFilter(t *testing.T) {
	now := time.Now()
	event1 := createSearchTestEvent("event-1", events.EventTypeAgentDecision, now)
	event2 := createSearchTestEvent("event-2", events.EventTypeToolResult, now)
	event3 := createSearchTestEvent("event-3", events.EventTypeToolCall, now)

	bleve := &mockBleveSearcherInterface{results: []*events.ActivityEvent{event1, event2, event3}}

	retriever := NewArchivalistRetrieverWithInterfaces(nil, bleve, nil)

	opts := &SearchOptions{
		Limit:      10,
		EventTypes: []events.EventType{events.EventTypeAgentDecision, events.EventTypeToolCall},
	}

	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Should only include filtered types
	if len(results) != 2 {
		t.Errorf("expected 2 filtered results, got %d", len(results))
	}

	for _, r := range results {
		if r.Event.EventType != events.EventTypeAgentDecision && r.Event.EventType != events.EventTypeToolCall {
			t.Errorf("unexpected event type: %v", r.Event.EventType)
		}
	}
}

func TestSearchWithMinScoreFilter(t *testing.T) {
	now := time.Now()
	// High importance event
	event1 := createSearchTestEventWithEmbed("event-1", events.EventTypeAgentDecision, now, []float32{1, 0, 0})
	// Low importance event (old with poor semantic match)
	event2 := createSearchTestEventWithEmbed("event-2", events.EventTypeIndexStart, now.Add(-24*time.Hour*30), []float32{0, 1, 0})

	bleve := &mockBleveSearcherInterface{results: []*events.ActivityEvent{event1, event2}}

	retriever := NewArchivalistRetrieverWithInterfaces(nil, bleve, nil)

	opts := &SearchOptions{
		Limit:    10,
		MinScore: 0.3,
	}

	results, err := retriever.Search(context.Background(), "test", []float32{1, 0, 0}, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// All results should meet minimum score
	for _, r := range results {
		if r.Score < opts.MinScore {
			t.Errorf("result score %f is below MinScore %f", r.Score, opts.MinScore)
		}
	}
}

func TestSearchWithLimit(t *testing.T) {
	now := time.Now()
	evts := make([]*events.ActivityEvent, 30)
	for i := 0; i < 30; i++ {
		evts[i] = createSearchTestEvent("event-"+string(rune('a'+i)), events.EventTypeAgentDecision, now)
	}

	bleve := &mockBleveSearcherInterface{results: evts}

	retriever := NewArchivalistRetrieverWithInterfaces(nil, bleve, nil)

	opts := &SearchOptions{
		Limit: 5,
	}

	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 5 {
		t.Errorf("expected 5 results (limit), got %d", len(results))
	}
}

func TestSearchResultsSortedByScore(t *testing.T) {
	now := time.Now()
	// Create events with different importance levels
	event1 := createSearchTestEventWithEmbed("event-1", events.EventTypeIndexStart, now.Add(-48*time.Hour), []float32{0, 1, 0})
	event2 := createSearchTestEventWithEmbed("event-2", events.EventTypeAgentDecision, now, []float32{1, 0, 0})
	event3 := createSearchTestEventWithEmbed("event-3", events.EventTypeToolResult, now.Add(-1*time.Hour), []float32{0.5, 0.5, 0})

	bleve := &mockBleveSearcherInterface{results: []*events.ActivityEvent{event1, event2, event3}}

	retriever := NewArchivalistRetrieverWithInterfaces(nil, bleve, nil)

	results, err := retriever.Search(context.Background(), "test", []float32{1, 0, 0}, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Results should be sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: score[%d]=%f > score[%d]=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestSearchBothSearchersFail(t *testing.T) {
	bleve := &mockBleveSearcherInterface{err: errors.New("bleve error")}
	vector := &mockVectorSearcherInterface{err: errors.New("vector error")}

	retriever := NewArchivalistRetrieverWithInterfaces(nil, bleve, vector)

	_, err := retriever.Search(context.Background(), "test", nil, nil)
	if err == nil {
		t.Error("expected error when both searchers fail")
	}
}

func TestSearchBleveFailsVectorSucceeds(t *testing.T) {
	now := time.Now()
	event := createSearchTestEvent("event-1", events.EventTypeAgentDecision, now)

	bleve := &mockBleveSearcherInterface{err: errors.New("bleve error")}
	vector := &mockVectorSearcherInterface{results: []*events.ActivityEvent{event}}

	retriever := NewArchivalistRetrieverWithInterfaces(nil, bleve, vector)

	results, err := retriever.Search(context.Background(), "test", nil, nil)
	if err != nil {
		t.Fatalf("Search should succeed when one searcher works: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result from vector search, got %d", len(results))
	}
}

func TestSearchNilSearchers(t *testing.T) {
	retriever := NewArchivalistRetrieverWithInterfaces(nil, nil, nil)

	results, err := retriever.Search(context.Background(), "test", nil, nil)
	if err != nil {
		t.Fatalf("Search with nil searchers should not error: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results with nil searchers, got %d", len(results))
	}
}

func TestSearchParallelExecution(t *testing.T) {
	now := time.Now()
	event1 := createSearchTestEvent("event-1", events.EventTypeAgentDecision, now)
	event2 := createSearchTestEvent("event-2", events.EventTypeToolResult, now)

	bleve := &mockBleveSearcherInterface{results: []*events.ActivityEvent{event1}}
	vector := &mockVectorSearcherInterface{results: []*events.ActivityEvent{event2}}

	retriever := NewArchivalistRetrieverWithInterfaces(nil, bleve, vector)

	_, err := retriever.Search(context.Background(), "test", nil, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Both searches should have been called
	if !bleve.searchCalled || !vector.searchCalled {
		t.Error("both searches should have been called in parallel")
	}
}

// =============================================================================
// AE.5.6 Tests - scoreEvent Method
// =============================================================================

func TestScoreEventPureCold(t *testing.T) {
	now := time.Now()
	event := createSearchTestEventWithEmbed("event-1", events.EventTypeAgentDecision, now, []float32{1, 0, 0})

	memStore := newMockMemoryStoreInterface()
	// No memory for this event (traceCount = 0)

	retriever := NewArchivalistRetrieverWithInterfaces(memStore, nil, nil)

	ranked, err := retriever.ScoreEventDirect(context.Background(), event, []float32{1, 0, 0}, now)
	if err != nil {
		t.Fatalf("scoreEvent failed: %v", err)
	}

	// With 0 traces, should be pure cold score
	if ranked.BlendRatio != 0 {
		t.Errorf("expected BlendRatio=0 for pure cold, got %f", ranked.BlendRatio)
	}
	if ranked.Score != ranked.ColdScore {
		t.Errorf("expected Score=ColdScore for pure cold, got Score=%f, ColdScore=%f",
			ranked.Score, ranked.ColdScore)
	}
}

func TestScoreEventPureWarm(t *testing.T) {
	now := time.Now()
	event := createSearchTestEvent("event-1", events.EventTypeAgentDecision, now)

	memStore := newMockMemoryStoreInterface()
	// Create memory with many traces (>= fullWarmTraces)
	mem := createSearchTestMemory(event.ID, DefaultFullWarmTraces+5, now)
	memStore.SetMemory(event.ID, mem)

	retriever := NewArchivalistRetrieverWithInterfaces(memStore, nil, nil)

	ranked, err := retriever.ScoreEventDirect(context.Background(), event, nil, now)
	if err != nil {
		t.Fatalf("scoreEvent failed: %v", err)
	}

	// With many traces, should be pure warm score
	if ranked.BlendRatio != 1.0 {
		t.Errorf("expected BlendRatio=1.0 for pure warm, got %f", ranked.BlendRatio)
	}
	if ranked.Score != ranked.WarmScore {
		t.Errorf("expected Score=WarmScore for pure warm, got Score=%f, WarmScore=%f",
			ranked.Score, ranked.WarmScore)
	}
}

func TestScoreEventBlended(t *testing.T) {
	now := time.Now()
	event := createSearchTestEvent("event-1", events.EventTypeAgentDecision, now)

	memStore := newMockMemoryStoreInterface()
	// Create memory with traces between min and full (blended region)
	traceCount := (DefaultMinTracesForWarm + DefaultFullWarmTraces) / 2
	mem := createSearchTestMemory(event.ID, traceCount, now)
	memStore.SetMemory(event.ID, mem)

	retriever := NewArchivalistRetrieverWithInterfaces(memStore, nil, nil)

	ranked, err := retriever.ScoreEventDirect(context.Background(), event, nil, now)
	if err != nil {
		t.Fatalf("scoreEvent failed: %v", err)
	}

	// Should be blended
	if ranked.BlendRatio <= 0 || ranked.BlendRatio >= 1.0 {
		t.Errorf("expected 0 < BlendRatio < 1.0 for blended, got %f", ranked.BlendRatio)
	}

	// Verify blended score formula
	expectedScore := (1.0-ranked.BlendRatio)*ranked.ColdScore + ranked.BlendRatio*ranked.WarmScore
	if math.Abs(ranked.Score-expectedScore) > 0.001 {
		t.Errorf("blended score mismatch: expected %f, got %f", expectedScore, ranked.Score)
	}
}

func TestComputeBlendRatio(t *testing.T) {
	retriever := NewArchivalistRetrieverWithInterfaces(nil, nil, nil)

	testCases := []struct {
		name       string
		traceCount int
		expected   float64
	}{
		{"zero traces", 0, 0.0},
		{"below min", DefaultMinTracesForWarm - 1, 0.0},
		{"at min", DefaultMinTracesForWarm, 0.0},
		{"above min", DefaultMinTracesForWarm + 1, float64(1) / float64(DefaultFullWarmTraces-DefaultMinTracesForWarm)},
		{"at full", DefaultFullWarmTraces, 1.0},
		{"above full", DefaultFullWarmTraces + 10, 1.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ratio := retriever.ComputeBlendRatioDirect(tc.traceCount)
			if math.Abs(ratio-tc.expected) > 0.001 {
				t.Errorf("expected ratio=%f, got %f", tc.expected, ratio)
			}
		})
	}
}

func TestActivationToScore(t *testing.T) {
	retriever := NewArchivalistRetrieverWithInterfaces(nil, nil, nil)

	testCases := []struct {
		name       string
		activation float64
		expectHigh bool
		expectLow  bool
	}{
		{"very negative", -150, false, true},
		{"negative", -5, false, false},
		{"zero", 0, false, false},
		{"positive", 5, true, false},
		{"very positive", 25, true, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			score := retriever.ActivationToScoreDirect(tc.activation)

			if score < 0 || score > 1 {
				t.Errorf("score should be in [0,1], got %f", score)
			}
			if tc.expectHigh && score < 0.9 {
				t.Errorf("expected high score, got %f", score)
			}
			if tc.expectLow && score > 0.1 {
				t.Errorf("expected low score, got %f", score)
			}
		})
	}
}

func TestApplyCrossSessionDecayScoreEvent(t *testing.T) {
	retriever := NewArchivalistRetrieverWithInterfaces(nil, nil, nil)

	now := time.Now()
	baseScore := 1.0

	testCases := []struct {
		name      string
		daysSince float64
	}{
		{"same time", 0},
		{"1 day", 1},
		{"7 days (half-life)", 7},
		{"14 days", 14},
		{"30 days", 30},
	}

	var prevScore float64 = 2.0 // Initialize higher than possible
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			eventTime := now.Add(-time.Duration(tc.daysSince*24) * time.Hour)
			decayedScore := retriever.ApplyCrossSessionDecayDirect(baseScore, eventTime, now)

			if decayedScore > 1.0 || decayedScore < 0 {
				t.Errorf("decayed score should be in [0,1], got %f", decayedScore)
			}
			if tc.daysSince > 0 && decayedScore >= prevScore {
				t.Errorf("older events should have lower scores: %f >= %f", decayedScore, prevScore)
			}
			prevScore = decayedScore
		})
	}
}

func TestScoreEventExplanation(t *testing.T) {
	now := time.Now()
	event := createSearchTestEvent("event-1", events.EventTypeAgentDecision, now)

	memStore := newMockMemoryStoreInterface()

	testCases := []struct {
		name       string
		traceCount int
	}{
		{"pure cold", 0},
		{"pure warm", DefaultFullWarmTraces + 5},
		{"blended", (DefaultMinTracesForWarm + DefaultFullWarmTraces) / 2},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.traceCount > 0 {
				mem := createSearchTestMemory(event.ID, tc.traceCount, now)
				memStore.SetMemory(event.ID, mem)
			} else {
				delete(memStore.memories, event.ID)
			}

			retriever := NewArchivalistRetrieverWithInterfaces(memStore, nil, nil)
			ranked, err := retriever.ScoreEventDirect(context.Background(), event, nil, now)
			if err != nil {
				t.Fatalf("scoreEvent failed: %v", err)
			}

			if ranked.Explanation == "" {
				t.Error("expected non-empty explanation")
			}
		})
	}
}

func TestGetEventEmbedding(t *testing.T) {
	now := time.Now()

	testCases := []struct {
		name     string
		event    *events.ActivityEvent
		expected int // expected length
	}{
		{
			name:     "with float32 embedding",
			event:    createSearchTestEventWithEmbed("e1", events.EventTypeAgentDecision, now, []float32{1, 2, 3}),
			expected: 3,
		},
		{
			name:     "without embedding",
			event:    createSearchTestEvent("e2", events.EventTypeAgentDecision, now),
			expected: 0,
		},
		{
			name: "with any slice embedding",
			event: func() *events.ActivityEvent {
				e := createSearchTestEvent("e3", events.EventTypeAgentDecision, now)
				e.Data["embedding"] = []any{float64(1.0), float64(2.0)}
				return e
			}(),
			expected: 2,
		},
	}

	retriever := NewArchivalistRetrieverWithInterfaces(nil, nil, nil)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			embed := retriever.getEventEmbedding(tc.event)
			if len(embed) != tc.expected {
				t.Errorf("expected embedding length %d, got %d", tc.expected, len(embed))
			}
		})
	}
}

// =============================================================================
// RankedActivityEvent Tests
// =============================================================================

func TestRankedActivityEventFields(t *testing.T) {
	now := time.Now()
	event := createSearchTestEvent("event-1", events.EventTypeAgentDecision, now)

	ranked := &RankedActivityEvent{
		Event:       event,
		Score:       0.85,
		ColdScore:   0.90,
		WarmScore:   0.80,
		BlendRatio:  0.5,
		Explanation: "test explanation",
	}

	if ranked.Event != event {
		t.Error("Event field mismatch")
	}
	if ranked.Score != 0.85 {
		t.Errorf("Score=%f, want 0.85", ranked.Score)
	}
	if ranked.ColdScore != 0.90 {
		t.Errorf("ColdScore=%f, want 0.90", ranked.ColdScore)
	}
	if ranked.WarmScore != 0.80 {
		t.Errorf("WarmScore=%f, want 0.80", ranked.WarmScore)
	}
	if ranked.BlendRatio != 0.5 {
		t.Errorf("BlendRatio=%f, want 0.5", ranked.BlendRatio)
	}
	if ranked.Explanation != "test explanation" {
		t.Errorf("Explanation=%s, want 'test explanation'", ranked.Explanation)
	}
}

// =============================================================================
// SearchOptions Tests
// =============================================================================

func TestDefaultSearchOptions(t *testing.T) {
	opts := DefaultSearchOptions()

	if opts.Limit != 20 {
		t.Errorf("Limit=%d, want 20", opts.Limit)
	}
	if opts.MinScore != 0.0 {
		t.Errorf("MinScore=%f, want 0.0", opts.MinScore)
	}
	if opts.EventTypes != nil {
		t.Error("EventTypes should be nil by default")
	}
	if opts.SessionID != "" {
		t.Error("SessionID should be empty by default")
	}
}

// =============================================================================
// AE.5.4-5.6 Integration Tests
// =============================================================================

func TestFullSearchPipeline(t *testing.T) {
	now := time.Now()

	// Create events with varying characteristics
	evts := []*events.ActivityEvent{
		createSearchTestEventWithEmbed("e1", events.EventTypeAgentDecision, now, []float32{1, 0, 0}),
		createSearchTestEventWithEmbed("e2", events.EventTypeToolResult, now.Add(-1*time.Hour), []float32{0.8, 0.2, 0}),
		createSearchTestEventWithEmbed("e3", events.EventTypeIndexStart, now.Add(-24*time.Hour), []float32{0, 1, 0}),
	}

	// Setup memory for one event (e2) to test blending
	memStore := newMockMemoryStoreInterface()
	mem := createSearchTestMemory("e2", 10, now)
	memStore.SetMemory("e2", mem)

	bleve := &mockBleveSearcherInterface{results: evts[:2]}
	vector := &mockVectorSearcherInterface{results: evts[1:]}

	retriever := NewArchivalistRetrieverWithInterfaces(memStore, bleve, vector)

	results, err := retriever.Search(
		context.Background(),
		"test query",
		[]float32{1, 0, 0},
		&SearchOptions{
			Limit:    10,
			MinScore: 0.1,
		},
	)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Verify results are present and sorted
	if len(results) == 0 {
		t.Error("expected at least one result")
	}

	// Verify sorted by score
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Error("results not sorted by score descending")
		}
	}
}

func TestSearchWithMixedWarmColdEvents(t *testing.T) {
	now := time.Now()

	// Create events
	coldEvent := createSearchTestEvent("cold-event", events.EventTypeAgentDecision, now)
	warmEvent := createSearchTestEvent("warm-event", events.EventTypeAgentDecision, now)

	// Setup memory only for warm event
	memStore := newMockMemoryStoreInterface()
	mem := createSearchTestMemory("warm-event", DefaultFullWarmTraces+5, now)
	memStore.SetMemory("warm-event", mem)

	bleve := &mockBleveSearcherInterface{results: []*events.ActivityEvent{coldEvent, warmEvent}}

	retriever := NewArchivalistRetrieverWithInterfaces(memStore, bleve, nil)

	results, err := retriever.Search(context.Background(), "test", nil, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Verify we got both events
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Find each event's result
	var coldResult, warmResult *RankedActivityEvent
	for _, r := range results {
		if r.Event.ID == "cold-event" {
			coldResult = r
		} else if r.Event.ID == "warm-event" {
			warmResult = r
		}
	}

	if coldResult == nil || warmResult == nil {
		t.Fatal("expected both events in results")
	}

	// Cold event should have BlendRatio = 0
	if coldResult.BlendRatio != 0 {
		t.Errorf("cold event should have BlendRatio=0, got %f", coldResult.BlendRatio)
	}

	// Warm event should have BlendRatio = 1
	if warmResult.BlendRatio != 1.0 {
		t.Errorf("warm event should have BlendRatio=1.0, got %f", warmResult.BlendRatio)
	}
}

// =============================================================================
// Backward Compatibility Tests for AE.5.4-5.6
// =============================================================================

func TestLegacyConstructorWithNewOptions(t *testing.T) {
	bleve := &mockBleveSearcherInterface{}
	vector := &mockVectorSearcherInterface{}

	retriever := NewArchivalistRetriever(
		nil, nil, nil,
		WithBleveSearcher(bleve),
		WithVectorSearcher(vector),
		WithMinTracesForWarm(10),
	)

	if retriever.bleveSearcher != bleve {
		t.Error("bleve searcher not set via option")
	}
	if retriever.vectorSearcher != vector {
		t.Error("vector searcher not set via option")
	}
	if retriever.minTracesForWarm != 10 {
		t.Errorf("minTracesForWarm=%d, want 10", retriever.minTracesForWarm)
	}
}
