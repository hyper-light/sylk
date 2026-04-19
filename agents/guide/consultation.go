package guide

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	corecontext "github.com/adalundhe/sylk/core/context"
)

// ConsultationObserver allows the Guide to passively log direct consultations
// to the Archivalist without adding them to its own LLM context.
type ConsultationObserver struct {
	bus       EventBus
	sessionID string

	mu   sync.Mutex
	sub  Subscription
	done bool
}

func NewConsultationObserver(bus EventBus, sessionID string) *ConsultationObserver {
	return &ConsultationObserver{
		bus:       bus,
		sessionID: sessionID,
	}
}

// Start listens to all requests via a wildcard subscription to log direct consultations.
func (c *ConsultationObserver) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.done || c.sub != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	// Subscribe to a wildcard topic pattern to capture direct consultations
	topic := BuildSessionTopic(c.sessionID, "*", "*", ChannelTypeRequests)

	// We use the SessionBusManager or TopicRouter in production to match wildcards.
	// For this standalone observer, we assume the EventBus supports pattern matching.
	sub, err := c.bus.SubscribeAsync(topic, func(msg *Message) error {
		if msg.Type == MessageTypeDirectConsultation {
			c.logToArchivalist(msg)
		}
		return nil
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.done {
		c.mu.Unlock()
		_ = sub.Unsubscribe()
		return nil
	}
	c.sub = sub
	c.mu.Unlock()

	go c.stopOnContextDone(ctx)

	return err
}

func (c *ConsultationObserver) stopOnContextDone(ctx context.Context) {
	if ctx == nil {
		return
	}
	<-ctx.Done()
	c.Stop()
}

// Stop unsubscribes the observer from the event bus.
func (c *ConsultationObserver) Stop() {
	c.mu.Lock()
	if c.done {
		c.mu.Unlock()
		return
	}
	c.done = true
	sub := c.sub
	c.sub = nil
	c.mu.Unlock()

	if sub != nil {
		_ = sub.Unsubscribe()
	}
}

func (c *ConsultationObserver) logToArchivalist(msg *Message) {
	// Extract consultation request
	consReq, ok := msg.Payload.(corecontext.ConsultationRequest)
	if !ok {
		// Also try pointer
		consPtr, okPtr := msg.Payload.(*corecontext.ConsultationRequest)
		if okPtr {
			consReq = *consPtr
		} else {
			return
		}
	}

	input, err := ArchivalistStoreRouteInput("Log direct consultation: " + consReq.Query)
	if err != nil {
		return
	}
	req := &RouteRequest{
		CorrelationID:       generateMessageID(),
		ParentCorrelationID: msg.CorrelationID,
		Input:               input,
		SourceAgentID:       firstNonEmptyConsultationSource(consReq.FromAgent),
		SourceAgentName:     firstNonEmptyConsultationSource(consReq.FromAgent),
		FireAndForget:       true,
		SessionID:           c.sessionID,
		Timestamp:           time.Now(),
		Metadata: map[string]any{
			"event_type": "direct_consultation",
			"from_agent": consReq.FromAgent,
			"to_agent":   consReq.ToAgent,
			"query":      consReq.Query,
		},
	}
	if len(msg.Metadata) > 0 {
		req.Metadata = mergeRouteMetadata(msg.Metadata, req.Metadata)
	}
	if err := c.bus.Publish(TopicGuideRequests, NewRequestMessage(generateMessageID(), req)); err != nil {
		slog.Warn("consultation_archival_publish_failed",
			"correlation_id", msg.CorrelationID,
			"from_agent", consReq.FromAgent,
			"to_agent", consReq.ToAgent,
			"error", err.Error(),
		)
	}
}

func firstNonEmptyConsultationSource(source string) string {
	if source = strings.TrimSpace(source); source != "" {
		return source
	}
	return "guide"
}

func mergeRouteMetadata(base map[string]any, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := make(map[string]any, len(base)+len(overlay))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}
