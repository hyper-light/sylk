package archivalist

import (
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.4.3 EventAggregator
// =============================================================================

// AggregatedEvent represents a collection of similar events aggregated over a time window.
type AggregatedEvent struct {
	EventType      events.EventType        `json:"event_type"`
	AgentID        string                  `json:"agent_id"`
	SessionID      string                  `json:"session_id"`
	Events         []*events.ActivityEvent `json:"events"`
	Count          int                     `json:"count"`
	FirstTimestamp time.Time               `json:"first_timestamp"`
	LastTimestamp  time.Time               `json:"last_timestamp"`
}

// EventAggregator aggregates high-volume events over a time window.
// Events with the same type, agent ID, and session ID are grouped together.
type EventAggregator struct {
	window  time.Duration
	pending map[string]*AggregatedEvent
	mu      sync.Mutex
}

// NewEventAggregator creates a new EventAggregator with the specified time window.
// The window determines how long events are buffered before being flushed.
// Default window is 5 seconds per GRAPH.md specification.
func NewEventAggregator(window time.Duration) *EventAggregator {
	return &EventAggregator{
		window:  window,
		pending: make(map[string]*AggregatedEvent),
	}
}

// Add adds an event to the aggregation buffer.
// Returns nil if the event is buffered (window not expired).
// Returns an AggregatedEvent if the window has expired and events should be flushed.
func (a *EventAggregator) Add(event *events.ActivityEvent) *events.ActivityEvent {
	a.mu.Lock()
	defer a.mu.Unlock()

	key := a.aggregationKey(event)
	now := time.Now()

	// Check if we have a pending aggregate for this key
	if agg, exists := a.pending[key]; exists {
		// Check if window has expired
		if now.Sub(agg.FirstTimestamp) >= a.window {
			// Flush this aggregate
			flushedEvent := a.createAggregatedEvent(agg)
			delete(a.pending, key)

			// Start new aggregate with current event
			a.pending[key] = &AggregatedEvent{
				EventType:      event.EventType,
				AgentID:        event.AgentID,
				SessionID:      event.SessionID,
				Events:         []*events.ActivityEvent{event},
				Count:          1,
				FirstTimestamp: event.Timestamp,
				LastTimestamp:  event.Timestamp,
			}

			return flushedEvent
		}

		// Window not expired, add to existing aggregate
		agg.Events = append(agg.Events, event)
		agg.Count++
		agg.LastTimestamp = event.Timestamp
		return nil
	}

	// No existing aggregate, create new one
	a.pending[key] = &AggregatedEvent{
		EventType:      event.EventType,
		AgentID:        event.AgentID,
		SessionID:      event.SessionID,
		Events:         []*events.ActivityEvent{event},
		Count:          1,
		FirstTimestamp: event.Timestamp,
		LastTimestamp:  event.Timestamp,
	}

	return nil
}

// Flush returns all pending aggregates and clears the buffer.
// This is useful for shutdown or forced flush scenarios.
func (a *EventAggregator) Flush() []*events.ActivityEvent {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make([]*events.ActivityEvent, 0, len(a.pending))

	for key, agg := range a.pending {
		result = append(result, a.createAggregatedEvent(agg))
		delete(a.pending, key)
	}

	return result
}

// aggregationKey creates a unique key for aggregating events.
// Events with the same type, agent ID, and session ID are aggregated together.
func (a *EventAggregator) aggregationKey(event *events.ActivityEvent) string {
	return fmt.Sprintf("%s:%s:%s", event.EventType.String(), event.AgentID, event.SessionID)
}

// createAggregatedEvent converts an AggregatedEvent into an ActivityEvent
// suitable for storage. The aggregated event contains summary information
// about all events in the aggregate.
func (a *EventAggregator) createAggregatedEvent(agg *AggregatedEvent) *events.ActivityEvent {
	// Create summary content
	content := fmt.Sprintf("Aggregated %d %s events from %s to %s",
		agg.Count,
		agg.EventType.String(),
		agg.FirstTimestamp.Format(time.RFC3339),
		agg.LastTimestamp.Format(time.RFC3339),
	)

	// Use the first event as a template
	firstEvent := agg.Events[0]

	return &events.ActivityEvent{
		ID:         firstEvent.ID,
		EventType:  agg.EventType,
		Timestamp:  agg.FirstTimestamp,
		SessionID:  agg.SessionID,
		AgentID:    agg.AgentID,
		Content:    content,
		Summary:    fmt.Sprintf("Aggregated %d events", agg.Count),
		Category:   firstEvent.Category,
		FilePaths:  firstEvent.FilePaths,
		Keywords:   firstEvent.Keywords,
		RelatedIDs: a.collectRelatedIDs(agg),
		Outcome:    firstEvent.Outcome,
		Importance: firstEvent.Importance,
		Data: map[string]any{
			"aggregated":      true,
			"event_count":     agg.Count,
			"first_timestamp": agg.FirstTimestamp,
			"last_timestamp":  agg.LastTimestamp,
			"original_ids":    a.collectEventIDs(agg),
		},
	}
}

// collectRelatedIDs gathers all related IDs from aggregated events.
func (a *EventAggregator) collectRelatedIDs(agg *AggregatedEvent) []string {
	idsMap := make(map[string]bool)

	for _, event := range agg.Events {
		for _, id := range event.RelatedIDs {
			idsMap[id] = true
		}
	}

	ids := make([]string, 0, len(idsMap))
	for id := range idsMap {
		ids = append(ids, id)
	}

	return ids
}

// collectEventIDs gathers all event IDs from aggregated events.
func (a *EventAggregator) collectEventIDs(agg *AggregatedEvent) []string {
	ids := make([]string, 0, len(agg.Events))
	for _, event := range agg.Events {
		ids = append(ids, event.ID)
	}
	return ids
}
