package archivalist

import (
	"testing"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.4.1 DualWriteStrategy Tests
// =============================================================================

func TestDualWriteStrategy_Structure(t *testing.T) {
	strategy := &DualWriteStrategy{
		WriteBleve:    true,
		WriteVector:   true,
		BleveFields:   []string{"content", "keywords"},
		VectorContent: "full",
		Aggregate:     false,
	}

	if !strategy.WriteBleve {
		t.Error("Expected WriteBleve to be true")
	}
	if !strategy.WriteVector {
		t.Error("Expected WriteVector to be true")
	}
	if len(strategy.BleveFields) != 2 {
		t.Errorf("Expected 2 BleveFields, got %d", len(strategy.BleveFields))
	}
	if strategy.VectorContent != "full" {
		t.Errorf("Expected VectorContent 'full', got '%s'", strategy.VectorContent)
	}
	if strategy.Aggregate {
		t.Error("Expected Aggregate to be false")
	}
}

// =============================================================================
// AE.4.2 EventClassifier Tests
// =============================================================================

func TestNewEventClassifier_Initialization(t *testing.T) {
	classifier := NewEventClassifier()

	if classifier == nil {
		t.Fatal("Expected non-nil EventClassifier")
	}

	if classifier.strategies == nil {
		t.Fatal("Expected strategies map to be initialized")
	}

	// Verify we have strategies for all expected event types
	expectedCount := 17 // Total event types in events package
	if len(classifier.strategies) != expectedCount {
		t.Errorf("Expected %d strategies, got %d", expectedCount, len(classifier.strategies))
	}
}

func TestEventClassifier_GetStrategy_HighValueEvents(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name      string
		eventType events.EventType
		wantBleve bool
		wantVec   bool
		wantAgg   bool
	}{
		{
			name:      "AgentDecision - both stores",
			eventType: events.EventTypeAgentDecision,
			wantBleve: true,
			wantVec:   true,
			wantAgg:   false,
		},
		{
			name:      "Failure - both stores",
			eventType: events.EventTypeFailure,
			wantBleve: true,
			wantVec:   true,
			wantAgg:   false,
		},
		{
			name:      "AgentError - both stores",
			eventType: events.EventTypeAgentError,
			wantBleve: true,
			wantVec:   true,
			wantAgg:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			if strategy.WriteBleve != tt.wantBleve {
				t.Errorf("WriteBleve = %v, want %v", strategy.WriteBleve, tt.wantBleve)
			}
			if strategy.WriteVector != tt.wantVec {
				t.Errorf("WriteVector = %v, want %v", strategy.WriteVector, tt.wantVec)
			}
			if strategy.Aggregate != tt.wantAgg {
				t.Errorf("Aggregate = %v, want %v", strategy.Aggregate, tt.wantAgg)
			}
			if strategy.VectorContent != "full" {
				t.Errorf("VectorContent = %s, want 'full'", strategy.VectorContent)
			}
		})
	}
}

func TestEventClassifier_GetStrategy_ToolEvents(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name      string
		eventType events.EventType
		wantBleve bool
		wantVec   bool
		wantAgg   bool
	}{
		{
			name:      "ToolCall - Bleve only, aggregated",
			eventType: events.EventTypeToolCall,
			wantBleve: true,
			wantVec:   false,
			wantAgg:   true,
		},
		{
			name:      "ToolResult - Bleve only, aggregated",
			eventType: events.EventTypeToolResult,
			wantBleve: true,
			wantVec:   false,
			wantAgg:   true,
		},
		{
			name:      "ToolTimeout - both stores (error condition)",
			eventType: events.EventTypeToolTimeout,
			wantBleve: true,
			wantVec:   true,
			wantAgg:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			if strategy.WriteBleve != tt.wantBleve {
				t.Errorf("WriteBleve = %v, want %v", strategy.WriteBleve, tt.wantBleve)
			}
			if strategy.WriteVector != tt.wantVec {
				t.Errorf("WriteVector = %v, want %v", strategy.WriteVector, tt.wantVec)
			}
			if strategy.Aggregate != tt.wantAgg {
				t.Errorf("Aggregate = %v, want %v", strategy.Aggregate, tt.wantAgg)
			}
		})
	}
}

func TestEventClassifier_GetStrategy_IndexEvents(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name      string
		eventType events.EventType
		wantBleve bool
		wantVec   bool
	}{
		{
			name:      "IndexStart - Bleve only",
			eventType: events.EventTypeIndexStart,
			wantBleve: true,
			wantVec:   false,
		},
		{
			name:      "IndexComplete - Bleve only",
			eventType: events.EventTypeIndexComplete,
			wantBleve: true,
			wantVec:   false,
		},
		{
			name:      "IndexFileAdded - Bleve only",
			eventType: events.EventTypeIndexFileAdded,
			wantBleve: true,
			wantVec:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			if strategy.WriteBleve != tt.wantBleve {
				t.Errorf("WriteBleve = %v, want %v", strategy.WriteBleve, tt.wantBleve)
			}
			if strategy.WriteVector != tt.wantVec {
				t.Errorf("WriteVector = %v, want %v", strategy.WriteVector, tt.wantVec)
			}
			if strategy.Aggregate {
				t.Error("Index events should not aggregate")
			}
		})
	}
}

func TestEventClassifier_GetStrategy_UserEvents(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name      string
		eventType events.EventType
	}{
		{
			name:      "UserPrompt",
			eventType: events.EventTypeUserPrompt,
		},
		{
			name:      "UserClarification",
			eventType: events.EventTypeUserClarification,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			// User events should go to both stores for semantic search
			if !strategy.WriteBleve {
				t.Error("Expected WriteBleve to be true for user events")
			}
			if !strategy.WriteVector {
				t.Error("Expected WriteVector to be true for user events")
			}
			if strategy.Aggregate {
				t.Error("User events should not aggregate")
			}
		})
	}
}

func TestEventClassifier_GetStrategy_ContextEvents(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name      string
		eventType events.EventType
	}{
		{
			name:      "ContextEviction",
			eventType: events.EventTypeContextEviction,
		},
		{
			name:      "ContextRestore",
			eventType: events.EventTypeContextRestore,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			// Context events: metadata only, Bleve only
			if !strategy.WriteBleve {
				t.Error("Expected WriteBleve to be true")
			}
			if strategy.WriteVector {
				t.Error("Expected WriteVector to be false for context events")
			}
			if strategy.VectorContent != "" {
				t.Error("Context events should have empty VectorContent")
			}
		})
	}
}

func TestEventClassifier_GetStrategy_BleveFields(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name       string
		eventType  events.EventType
		wantFields []string
	}{
		{
			name:       "AgentDecision - all fields",
			eventType:  events.EventTypeAgentDecision,
			wantFields: []string{"content", "keywords", "category", "agent_id"},
		},
		{
			name:       "ToolCall - structured fields",
			eventType:  events.EventTypeToolCall,
			wantFields: []string{"agent_id", "data"},
		},
		{
			name:       "IndexStart - minimal fields",
			eventType:  events.EventTypeIndexStart,
			wantFields: []string{"session_id", "data"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			if len(strategy.BleveFields) != len(tt.wantFields) {
				t.Errorf("Expected %d fields, got %d", len(tt.wantFields), len(strategy.BleveFields))
				return
			}

			fieldMap := make(map[string]bool)
			for _, f := range strategy.BleveFields {
				fieldMap[f] = true
			}

			for _, wantField := range tt.wantFields {
				if !fieldMap[wantField] {
					t.Errorf("Missing expected field: %s", wantField)
				}
			}
		})
	}
}

func TestEventClassifier_GetStrategy_DefaultFallback(t *testing.T) {
	classifier := NewEventClassifier()

	// Use an invalid event type to test default fallback
	invalidType := events.EventType(999)

	strategy := classifier.GetStrategy(invalidType)

	if strategy == nil {
		t.Fatal("Expected non-nil default strategy")
	}

	// Default should write to both stores
	if !strategy.WriteBleve {
		t.Error("Default strategy should write to Bleve")
	}
	if !strategy.WriteVector {
		t.Error("Default strategy should write to Vector")
	}

	// Default should have "full" content
	if strategy.VectorContent != "full" {
		t.Errorf("Default VectorContent = %s, want 'full'", strategy.VectorContent)
	}

	// Default should not aggregate
	if strategy.Aggregate {
		t.Error("Default strategy should not aggregate")
	}
}

func TestEventClassifier_GetStrategy_AllEventTypes(t *testing.T) {
	classifier := NewEventClassifier()

	// Verify all valid event types have strategies
	for _, eventType := range events.ValidEventTypes() {
		t.Run(eventType.String(), func(t *testing.T) {
			strategy := classifier.GetStrategy(eventType)

			if strategy == nil {
				t.Fatal("Expected non-nil strategy")
			}

			// Verify at least one store is enabled
			if !strategy.WriteBleve && !strategy.WriteVector {
				t.Error("At least one store should be enabled")
			}

			// If WriteVector is true, VectorContent should not be empty
			if strategy.WriteVector && strategy.VectorContent == "" {
				t.Error("WriteVector is true but VectorContent is empty")
			}
		})
	}
}
