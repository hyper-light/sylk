package archivalist

import (
	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.4.1 DualWriteStrategy Type
// =============================================================================

// DualWriteStrategy determines how an event is stored in dual-write architecture.
// It specifies which stores to write to, what fields to index, and whether to aggregate.
type DualWriteStrategy struct {
	WriteBleve    bool     // Write to Bleve document database
	WriteVector   bool     // Write to Vector database (HNSW)
	BleveFields   []string // Which ActivityEvent fields to index in Bleve
	VectorContent string   // What to embed: "full", "summary", "keywords"
	Aggregate     bool     // Aggregate with similar recent events
}

// =============================================================================
// AE.4.2 EventClassifier
// =============================================================================

// EventClassifier determines dual-write strategy per event type.
// It maintains a mapping from EventType to DualWriteStrategy.
type EventClassifier struct {
	strategies map[events.EventType]*DualWriteStrategy
}

// NewEventClassifier creates a new EventClassifier with default strategies
// per GRAPH.md specification. Strategies are chosen based on:
//   - High-value events (decisions, failures): both stores, full content
//   - Tool events: Bleve only, aggregated (high volume, structured)
//   - Index events: Bleve only, structured metadata
//   - Context events: Bleve only, metadata
func NewEventClassifier() *EventClassifier {
	return &EventClassifier{
		strategies: map[events.EventType]*DualWriteStrategy{
			// High-value events: both stores, full content
			events.EventTypeAgentDecision: {
				WriteBleve:    true,
				WriteVector:   true,
				BleveFields:   []string{"content", "keywords", "category", "agent_id"},
				VectorContent: "full",
				Aggregate:     false,
			},
			events.EventTypeFailure: {
				WriteBleve:    true,
				WriteVector:   true,
				BleveFields:   []string{"content", "keywords", "category"},
				VectorContent: "full",
				Aggregate:     false,
			},
			events.EventTypeAgentError: {
				WriteBleve:    true,
				WriteVector:   true,
				BleveFields:   []string{"content", "category"},
				VectorContent: "full",
				Aggregate:     false,
			},

			// User events: semantic search important
			events.EventTypeUserPrompt: {
				WriteBleve:    true,
				WriteVector:   true,
				BleveFields:   []string{"content", "keywords"},
				VectorContent: "full",
				Aggregate:     false,
			},
			events.EventTypeUserClarification: {
				WriteBleve:    true,
				WriteVector:   true,
				BleveFields:   []string{"content"},
				VectorContent: "full",
				Aggregate:     false,
			},

			// Success events: semantic value moderate
			events.EventTypeSuccess: {
				WriteBleve:    true,
				WriteVector:   true,
				BleveFields:   []string{"summary", "category"},
				VectorContent: "summary",
				Aggregate:     false,
			},

			// Tool events: structured only (high volume, low semantic value)
			events.EventTypeToolCall: {
				WriteBleve:    true,
				WriteVector:   false,
				BleveFields:   []string{"agent_id", "data"},
				VectorContent: "",
				Aggregate:     true,
			},
			events.EventTypeToolResult: {
				WriteBleve:    true,
				WriteVector:   false,
				BleveFields:   []string{"agent_id", "data"},
				VectorContent: "",
				Aggregate:     true,
			},
			events.EventTypeToolTimeout: {
				WriteBleve:    true,
				WriteVector:   true,
				BleveFields:   []string{"content", "agent_id"},
				VectorContent: "full",
				Aggregate:     false,
			},

			// Agent actions: metadata important
			events.EventTypeAgentAction: {
				WriteBleve:    true,
				WriteVector:   false,
				BleveFields:   []string{"summary", "category", "agent_id"},
				VectorContent: "",
				Aggregate:     false,
			},

			// LLM events: structured metadata
			events.EventTypeLLMRequest: {
				WriteBleve:    true,
				WriteVector:   false,
				BleveFields:   []string{"agent_id", "data"},
				VectorContent: "",
				Aggregate:     true,
			},
			events.EventTypeLLMResponse: {
				WriteBleve:    true,
				WriteVector:   false,
				BleveFields:   []string{"agent_id", "data"},
				VectorContent: "",
				Aggregate:     true,
			},

			// Index events: structured metadata only
			events.EventTypeIndexStart: {
				WriteBleve:    true,
				WriteVector:   false,
				BleveFields:   []string{"session_id", "data"},
				VectorContent: "",
				Aggregate:     false,
			},
			events.EventTypeIndexComplete: {
				WriteBleve:    true,
				WriteVector:   false,
				BleveFields:   []string{"session_id", "data"},
				VectorContent: "",
				Aggregate:     false,
			},
			events.EventTypeIndexFileAdded: {
				WriteBleve:    true,
				WriteVector:   false,
				BleveFields:   []string{"content", "data"},
				VectorContent: "",
				Aggregate:     false,
			},

			// Context events: metadata only
			events.EventTypeContextEviction: {
				WriteBleve:    true,
				WriteVector:   false,
				BleveFields:   []string{"data"},
				VectorContent: "",
				Aggregate:     false,
			},
			events.EventTypeContextRestore: {
				WriteBleve:    true,
				WriteVector:   false,
				BleveFields:   []string{"data"},
				VectorContent: "",
				Aggregate:     false,
			},
		},
	}
}

// GetStrategy returns the dual-write strategy for a given event type.
// Returns a default strategy if the event type is not found.
func (c *EventClassifier) GetStrategy(eventType events.EventType) *DualWriteStrategy {
	if strategy, ok := c.strategies[eventType]; ok {
		return strategy
	}

	// Default strategy: write to both stores, full content
	return &DualWriteStrategy{
		WriteBleve:    true,
		WriteVector:   true,
		BleveFields:   []string{"content"},
		VectorContent: "full",
		Aggregate:     false,
	}
}
