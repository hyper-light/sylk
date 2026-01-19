package archivalist

import (
	"context"
	"sync"
	"time"

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

// =============================================================================
// AE.4.6 DualWriter
// =============================================================================

// BleveEventIndexer is the interface for Bleve event indexing operations.
// This interface allows for mocking in tests.
type BleveEventIndexer interface {
	IndexEvent(ctx context.Context, event *events.ActivityEvent, fields []string) error
	Close() error
}

// VectorEventStorer is the interface for vector event storage operations.
// This interface allows for mocking in tests.
type VectorEventStorer interface {
	EmbedEvent(ctx context.Context, event *events.ActivityEvent, contentField string) error
}

// DualWriter writes events to both Bleve and Vector stores based on the
// dual-write strategy determined by the event classifier.
type DualWriter struct {
	bleveIndex  BleveEventIndexer
	vectorStore VectorEventStorer
	classifier  *EventClassifier
	aggregator  *EventAggregator
	mu          sync.Mutex
}

// NewDualWriter creates a new DualWriter with the specified stores.
// It initializes an EventClassifier with default strategies and an
// EventAggregator with a 5-second window.
func NewDualWriter(bleveIndex BleveEventIndexer, vectorStore VectorEventStorer) *DualWriter {
	return &DualWriter{
		bleveIndex:  bleveIndex,
		vectorStore: vectorStore,
		classifier:  NewEventClassifier(),
		aggregator:  NewEventAggregator(5 * time.Second),
	}
}

// Write writes an event to the appropriate stores based on the dual-write strategy.
// For events requiring aggregation, the event is buffered and written when the
// aggregation window expires. Writes to both stores are performed in parallel.
func (w *DualWriter) Write(ctx context.Context, event *events.ActivityEvent) error {
	strategy := w.classifier.GetStrategy(event.EventType)

	// Handle aggregation for high-volume events
	if strategy.Aggregate {
		return w.writeAggregated(ctx, event, strategy)
	}

	// Direct write for non-aggregated events
	return w.writeEvent(ctx, event, strategy)
}

// writeAggregated handles aggregated event writing.
// Returns any flushed aggregate if the window has expired.
func (w *DualWriter) writeAggregated(ctx context.Context, event *events.ActivityEvent, strategy *DualWriteStrategy) error {
	w.mu.Lock()
	flushedEvent := w.aggregator.Add(event)
	w.mu.Unlock()

	// If an aggregate was flushed, write it
	if flushedEvent != nil {
		return w.writeEvent(ctx, flushedEvent, strategy)
	}

	return nil
}

// writeEvent writes an event to the stores based on strategy, in parallel.
func (w *DualWriter) writeEvent(ctx context.Context, event *events.ActivityEvent, strategy *DualWriteStrategy) error {
	var wg sync.WaitGroup
	errChan := make(chan error, 2)

	// Write to Bleve if enabled
	if strategy.WriteBleve && w.bleveIndex != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.bleveIndex.IndexEvent(ctx, event, strategy.BleveFields); err != nil {
				errChan <- err
			}
		}()
	}

	// Write to Vector store if enabled
	if strategy.WriteVector && w.vectorStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.vectorStore.EmbedEvent(ctx, event, strategy.VectorContent); err != nil {
				errChan <- err
			}
		}()
	}

	// Wait for both writes to complete
	wg.Wait()
	close(errChan)

	// Return first error if any
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

// Flush flushes all pending aggregated events to their respective stores.
// This should be called during shutdown to ensure no events are lost.
func (w *DualWriter) Flush(ctx context.Context) error {
	w.mu.Lock()
	flushedEvents := w.aggregator.Flush()
	w.mu.Unlock()

	for _, event := range flushedEvents {
		strategy := w.classifier.GetStrategy(event.EventType)
		if err := w.writeEvent(ctx, event, strategy); err != nil {
			return err
		}
	}

	return nil
}

// Close closes the DualWriter and its underlying stores.
// It first flushes any pending aggregated events.
func (w *DualWriter) Close() error {
	// Flush pending events with background context
	if err := w.Flush(context.Background()); err != nil {
		return err
	}

	// Close Bleve index
	if w.bleveIndex != nil {
		if err := w.bleveIndex.Close(); err != nil {
			return err
		}
	}

	return nil
}
