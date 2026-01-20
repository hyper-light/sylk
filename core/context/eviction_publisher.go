// Package context provides AE.3.5 EvictionEventPublisher for VirtualContextManager.
// The EvictionEventPublisher integrates with ActivityEventBus to publish eviction events
// with configurable detail levels and non-blocking event publishing.
package context

import (
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.3.5.1 EvictionReason Type
// =============================================================================

// EvictionReason describes why content was evicted.
type EvictionReason string

const (
	// EvictionReasonMemoryPressure indicates eviction due to memory limits.
	EvictionReasonMemoryPressure EvictionReason = "memory_pressure"

	// EvictionReasonTokenLimit indicates eviction due to token count limits.
	EvictionReasonTokenLimit EvictionReason = "token_limit"

	// EvictionReasonAge indicates eviction due to content age.
	EvictionReasonAge EvictionReason = "age"

	// EvictionReasonLowRelevance indicates eviction due to low relevance score.
	EvictionReasonLowRelevance EvictionReason = "low_relevance"

	// EvictionReasonStrategyBased indicates eviction based on strategy selection.
	EvictionReasonStrategyBased EvictionReason = "strategy_based"

	// EvictionReasonManual indicates manual/forced eviction.
	EvictionReasonManual EvictionReason = "manual"
)

// String returns the string representation of the eviction reason.
func (r EvictionReason) String() string {
	return string(r)
}

// =============================================================================
// AE.3.5.2 EvictionEvent Types
// =============================================================================

// EvictionEvent represents an individual item eviction event.
type EvictionEvent struct {
	// ItemID is the unique identifier of the evicted item.
	ItemID string `json:"item_id"`

	// ItemType is the content type of the evicted item.
	ItemType ContentType `json:"item_type"`

	// Reason describes why the item was evicted.
	Reason EvictionReason `json:"reason"`

	// Timestamp is when the eviction occurred.
	Timestamp time.Time `json:"timestamp"`

	// TokenCount is the number of tokens freed by this eviction.
	TokenCount int `json:"token_count,omitempty"`

	// AgentID is the agent whose context was affected.
	AgentID string `json:"agent_id,omitempty"`

	// SessionID is the session context.
	SessionID string `json:"session_id,omitempty"`

	// Metadata contains additional event-specific data.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// EvictionBatchEvent represents a batch of eviction events.
type EvictionBatchEvent struct {
	// BatchID is a unique identifier for this eviction batch.
	BatchID string `json:"batch_id"`

	// AgentID is the agent whose context was affected.
	AgentID string `json:"agent_id"`

	// SessionID is the session context.
	SessionID string `json:"session_id"`

	// StartTime is when the eviction batch started.
	StartTime time.Time `json:"start_time"`

	// EndTime is when the eviction batch completed.
	EndTime time.Time `json:"end_time,omitempty"`

	// TotalItems is the total number of items in the batch.
	TotalItems int `json:"total_items"`

	// TotalTokens is the total tokens freed by the batch.
	TotalTokens int `json:"total_tokens"`

	// Reason is the primary reason for this eviction batch.
	Reason EvictionReason `json:"reason"`

	// Items contains individual eviction events (if detail level allows).
	Items []EvictionEvent `json:"items,omitempty"`
}

// =============================================================================
// AE.3.5.3 EventDetailLevel Configuration
// =============================================================================

// EventDetailLevel controls the verbosity of eviction events.
type EventDetailLevel int

const (
	// EventDetailNone disables eviction event publishing.
	EventDetailNone EventDetailLevel = 0

	// EventDetailSummary publishes only batch start/complete events.
	EventDetailSummary EventDetailLevel = 1

	// EventDetailStandard publishes batch events plus item counts.
	EventDetailStandard EventDetailLevel = 2

	// EventDetailVerbose publishes all events including individual items.
	EventDetailVerbose EventDetailLevel = 3
)

// String returns the string representation of the detail level.
func (d EventDetailLevel) String() string {
	switch d {
	case EventDetailNone:
		return "none"
	case EventDetailSummary:
		return "summary"
	case EventDetailStandard:
		return "standard"
	case EventDetailVerbose:
		return "verbose"
	default:
		return fmt.Sprintf("level(%d)", d)
	}
}

// =============================================================================
// AE.3.5.4 EvictionPublisherConfig
// =============================================================================

// EvictionPublisherConfig configures the EvictionEventPublisher.
type EvictionPublisherConfig struct {
	// DetailLevel controls event verbosity.
	DetailLevel EventDetailLevel

	// BufferSize is the size of the async event buffer.
	BufferSize int

	// MaxItemsPerEvent limits items in verbose events to prevent overflow.
	MaxItemsPerEvent int

	// IncludeMetadata determines if item metadata is included.
	IncludeMetadata bool
}

// DefaultEvictionPublisherConfig returns the default configuration.
func DefaultEvictionPublisherConfig() *EvictionPublisherConfig {
	return &EvictionPublisherConfig{
		DetailLevel:      EventDetailStandard,
		BufferSize:       100,
		MaxItemsPerEvent: 50,
		IncludeMetadata:  false,
	}
}

// =============================================================================
// AE.3.5.5 EvictionEventPublisher
// =============================================================================

// EvictionEventPublisher publishes eviction events to the ActivityEventBus.
// It provides non-blocking event publishing with configurable detail levels.
type EvictionEventPublisher struct {
	// bus is the activity event bus to publish to.
	bus *events.ActivityEventBus

	// sessionID is the current session identifier.
	sessionID string

	// agentID is the current agent identifier.
	agentID string

	// config holds the publisher configuration.
	config *EvictionPublisherConfig

	// buffer is the async event buffer.
	buffer chan *events.ActivityEvent

	// mu protects configuration updates.
	mu sync.RWMutex

	// closed indicates if the publisher is closed.
	closed bool

	// wg tracks pending async operations.
	wg sync.WaitGroup
}

// NewEvictionEventPublisher creates a new EvictionEventPublisher.
func NewEvictionEventPublisher(
	bus *events.ActivityEventBus,
	sessionID, agentID string,
	config *EvictionPublisherConfig,
) *EvictionEventPublisher {
	if config == nil {
		config = DefaultEvictionPublisherConfig()
	}

	bufferSize := config.BufferSize
	if bufferSize <= 0 {
		bufferSize = 100
	}

	p := &EvictionEventPublisher{
		bus:       bus,
		sessionID: sessionID,
		agentID:   agentID,
		config:    config,
		buffer:    make(chan *events.ActivityEvent, bufferSize),
	}

	// Start async publisher goroutine
	p.wg.Add(1)
	go p.processBuffer()

	return p
}

// processBuffer handles async event publishing.
func (p *EvictionEventPublisher) processBuffer() {
	defer p.wg.Done()

	for event := range p.buffer {
		if p.bus != nil {
			p.bus.Publish(event)
		}
	}
}

// =============================================================================
// AE.3.5.6 Publishing Methods
// =============================================================================

// PublishEvictionStarted publishes an eviction started event.
func (p *EvictionEventPublisher) PublishEvictionStarted(
	agentID string,
	reason EvictionReason,
	targetTokens int,
) error {
	p.mu.RLock()
	if p.closed || p.config.DetailLevel < EventDetailSummary {
		p.mu.RUnlock()
		return nil
	}
	p.mu.RUnlock()

	content := fmt.Sprintf("Eviction started for agent %s: reason=%s, target_tokens=%d",
		agentID, reason, targetTokens)

	event := events.NewActivityEvent(events.EventTypeContextEviction, p.sessionID, content)
	event.AgentID = agentID
	event.Category = "eviction"
	event.Keywords = []string{"eviction", "started", reason.String()}

	event.Data["phase"] = "started"
	event.Data["reason"] = reason.String()
	event.Data["target_tokens"] = targetTokens

	return p.publishAsync(event)
}

// PublishItemEvicted publishes an individual item eviction event.
func (p *EvictionEventPublisher) PublishItemEvicted(evt *EvictionEvent) error {
	p.mu.RLock()
	if p.closed || p.config.DetailLevel < EventDetailVerbose {
		p.mu.RUnlock()
		return nil
	}
	includeMetadata := p.config.IncludeMetadata
	p.mu.RUnlock()

	if evt == nil {
		return nil
	}

	content := fmt.Sprintf("Item evicted: id=%s, type=%s, reason=%s, tokens=%d",
		evt.ItemID, evt.ItemType, evt.Reason, evt.TokenCount)

	event := events.NewActivityEvent(events.EventTypeContextEviction, p.sessionID, content)
	event.AgentID = evt.AgentID
	event.Category = "eviction"
	event.Keywords = []string{"eviction", "item", evt.Reason.String(), string(evt.ItemType)}

	event.Data["phase"] = "item_evicted"
	event.Data["item_id"] = evt.ItemID
	event.Data["item_type"] = string(evt.ItemType)
	event.Data["reason"] = evt.Reason.String()
	event.Data["token_count"] = evt.TokenCount

	if includeMetadata && evt.Metadata != nil {
		event.Data["metadata"] = evt.Metadata
	}

	return p.publishAsync(event)
}

// PublishEvictionCompleted publishes an eviction completed event.
func (p *EvictionEventPublisher) PublishEvictionCompleted(batch *EvictionBatchEvent) error {
	p.mu.RLock()
	if p.closed || p.config.DetailLevel < EventDetailSummary {
		p.mu.RUnlock()
		return nil
	}
	detailLevel := p.config.DetailLevel
	maxItems := p.config.MaxItemsPerEvent
	p.mu.RUnlock()

	if batch == nil {
		return nil
	}

	duration := batch.EndTime.Sub(batch.StartTime)
	content := fmt.Sprintf("Eviction completed for agent %s: items=%d, tokens=%d, duration=%v",
		batch.AgentID, batch.TotalItems, batch.TotalTokens, duration)

	event := events.NewActivityEvent(events.EventTypeContextEviction, p.sessionID, content)
	event.AgentID = batch.AgentID
	event.Category = "eviction"
	event.Keywords = []string{"eviction", "completed", batch.Reason.String()}
	event.Outcome = events.OutcomeSuccess

	event.Data["phase"] = "completed"
	event.Data["batch_id"] = batch.BatchID
	event.Data["reason"] = batch.Reason.String()
	event.Data["total_items"] = batch.TotalItems
	event.Data["total_tokens"] = batch.TotalTokens
	event.Data["duration_ms"] = duration.Milliseconds()
	event.Data["start_time"] = batch.StartTime.Format(time.RFC3339)
	event.Data["end_time"] = batch.EndTime.Format(time.RFC3339)

	// Include item details at verbose level
	if detailLevel >= EventDetailVerbose && len(batch.Items) > 0 {
		items := batch.Items
		if len(items) > maxItems {
			items = items[:maxItems]
			event.Data["items_truncated"] = true
		}
		event.Data["items"] = formatEvictionItems(items)
	}

	return p.publishAsync(event)
}

// formatEvictionItems converts eviction events to a serializable format.
func formatEvictionItems(items []EvictionEvent) []map[string]any {
	result := make([]map[string]any, len(items))
	for i, item := range items {
		result[i] = map[string]any{
			"item_id":     item.ItemID,
			"item_type":   string(item.ItemType),
			"reason":      item.Reason.String(),
			"token_count": item.TokenCount,
		}
	}
	return result
}

// PublishEvictionFailed publishes an eviction failure event.
func (p *EvictionEventPublisher) PublishEvictionFailed(
	agentID string,
	reason EvictionReason,
	err error,
) error {
	p.mu.RLock()
	if p.closed || p.config.DetailLevel < EventDetailSummary {
		p.mu.RUnlock()
		return nil
	}
	p.mu.RUnlock()

	var errMsg string
	if err != nil {
		errMsg = err.Error()
	}

	content := fmt.Sprintf("Eviction failed for agent %s: reason=%s, error=%s",
		agentID, reason, errMsg)

	event := events.NewActivityEvent(events.EventTypeContextEviction, p.sessionID, content)
	event.AgentID = agentID
	event.Category = "eviction"
	event.Keywords = []string{"eviction", "failed", reason.String()}
	event.Outcome = events.OutcomeFailure

	event.Data["phase"] = "failed"
	event.Data["reason"] = reason.String()
	event.Data["error"] = errMsg

	return p.publishAsync(event)
}

// =============================================================================
// AE.3.5.7 Batch Helper Methods
// =============================================================================

// PublishBatch is a convenience method that publishes start, items, and complete.
func (p *EvictionEventPublisher) PublishBatch(
	agentID string,
	reason EvictionReason,
	entries []*ContentEntry,
	duration time.Duration,
) error {
	if len(entries) == 0 {
		return nil
	}

	now := time.Now()
	batchID := generateBatchID(agentID, now)

	// Calculate totals
	totalTokens := 0
	for _, e := range entries {
		totalTokens += e.TokenCount
	}

	// Publish start
	if err := p.PublishEvictionStarted(agentID, reason, totalTokens); err != nil {
		return err
	}

	// Publish individual items at verbose level
	p.mu.RLock()
	detailLevel := p.config.DetailLevel
	p.mu.RUnlock()

	items := make([]EvictionEvent, 0, len(entries))
	for _, entry := range entries {
		evt := EvictionEvent{
			ItemID:     entry.ID,
			ItemType:   entry.ContentType,
			Reason:     reason,
			Timestamp:  now,
			TokenCount: entry.TokenCount,
			AgentID:    agentID,
			SessionID:  p.sessionID,
		}
		items = append(items, evt)

		if detailLevel >= EventDetailVerbose {
			if err := p.PublishItemEvicted(&evt); err != nil {
				// Continue on error - don't fail the batch
				continue
			}
		}
	}

	// Publish complete
	batch := &EvictionBatchEvent{
		BatchID:     batchID,
		AgentID:     agentID,
		SessionID:   p.sessionID,
		StartTime:   now.Add(-duration),
		EndTime:     now,
		TotalItems:  len(entries),
		TotalTokens: totalTokens,
		Reason:      reason,
		Items:       items,
	}

	return p.PublishEvictionCompleted(batch)
}

// generateBatchID creates a unique batch identifier.
func generateBatchID(agentID string, t time.Time) string {
	return fmt.Sprintf("evict-%s-%d", agentID, t.UnixNano())
}

// =============================================================================
// AE.3.5.8 Configuration and Lifecycle
// =============================================================================

// SetDetailLevel updates the event detail level.
func (p *EvictionEventPublisher) SetDetailLevel(level EventDetailLevel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config.DetailLevel = level
}

// GetDetailLevel returns the current detail level.
func (p *EvictionEventPublisher) GetDetailLevel() EventDetailLevel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config.DetailLevel
}

// SetIncludeMetadata updates the metadata inclusion setting.
func (p *EvictionEventPublisher) SetIncludeMetadata(include bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config.IncludeMetadata = include
}

// UpdateSession updates the session and agent identifiers.
func (p *EvictionEventPublisher) UpdateSession(sessionID, agentID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessionID = sessionID
	p.agentID = agentID
}

// publishAsync sends an event to the buffer for async publishing.
// This is non-blocking - if the buffer is full, the event is dropped.
func (p *EvictionEventPublisher) publishAsync(event *events.ActivityEvent) error {
	select {
	case p.buffer <- event:
		return nil
	default:
		// Buffer full, drop event (non-blocking behavior)
		return nil
	}
}

// Flush waits for all pending events to be published.
func (p *EvictionEventPublisher) Flush() {
	// Drain the buffer by waiting for it to empty
	for len(p.buffer) > 0 {
		time.Sleep(time.Millisecond)
	}
}

// Close closes the publisher and waits for pending events.
func (p *EvictionEventPublisher) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	close(p.buffer)
	p.wg.Wait()
}

// =============================================================================
// AE.3.5.9 Hook Interface for VirtualContextManager Integration
// =============================================================================

// EvictionHook defines the interface for receiving eviction notifications.
// This allows the EvictionEventPublisher to be used as a hook in VirtualContextManager.
type EvictionHook interface {
	// OnEvictionStarted is called when eviction begins.
	OnEvictionStarted(agentID string, reason EvictionReason, targetTokens int)

	// OnItemEvicted is called for each evicted item.
	OnItemEvicted(event *EvictionEvent)

	// OnEvictionCompleted is called when eviction finishes.
	OnEvictionCompleted(batch *EvictionBatchEvent)

	// OnEvictionFailed is called if eviction fails.
	OnEvictionFailed(agentID string, reason EvictionReason, err error)
}

// Ensure EvictionEventPublisher implements EvictionHook.
var _ EvictionHook = (*EvictionEventPublisher)(nil)

// OnEvictionStarted implements EvictionHook.
func (p *EvictionEventPublisher) OnEvictionStarted(agentID string, reason EvictionReason, targetTokens int) {
	_ = p.PublishEvictionStarted(agentID, reason, targetTokens)
}

// OnItemEvicted implements EvictionHook.
func (p *EvictionEventPublisher) OnItemEvicted(event *EvictionEvent) {
	_ = p.PublishItemEvicted(event)
}

// OnEvictionCompleted implements EvictionHook.
func (p *EvictionEventPublisher) OnEvictionCompleted(batch *EvictionBatchEvent) {
	_ = p.PublishEvictionCompleted(batch)
}

// OnEvictionFailed implements EvictionHook.
func (p *EvictionEventPublisher) OnEvictionFailed(agentID string, reason EvictionReason, err error) {
	_ = p.PublishEvictionFailed(agentID, reason, err)
}

// =============================================================================
// AE.3.5.10 NoOp Implementation
// =============================================================================

// NoOpEvictionPublisher is a no-operation implementation of EvictionHook.
// Use this when eviction event publishing is not needed.
type NoOpEvictionPublisher struct{}

// NewNoOpEvictionPublisher creates a new NoOpEvictionPublisher.
func NewNoOpEvictionPublisher() *NoOpEvictionPublisher {
	return &NoOpEvictionPublisher{}
}

// OnEvictionStarted implements EvictionHook (no-op).
func (p *NoOpEvictionPublisher) OnEvictionStarted(agentID string, reason EvictionReason, targetTokens int) {
}

// OnItemEvicted implements EvictionHook (no-op).
func (p *NoOpEvictionPublisher) OnItemEvicted(event *EvictionEvent) {}

// OnEvictionCompleted implements EvictionHook (no-op).
func (p *NoOpEvictionPublisher) OnEvictionCompleted(batch *EvictionBatchEvent) {}

// OnEvictionFailed implements EvictionHook (no-op).
func (p *NoOpEvictionPublisher) OnEvictionFailed(agentID string, reason EvictionReason, err error) {}

// Ensure NoOpEvictionPublisher implements EvictionHook.
var _ EvictionHook = (*NoOpEvictionPublisher)(nil)
