package guide

import (
	"context"
	"time"

	corecontext "github.com/adalundhe/sylk/core/context"
)

// ConsultationObserver allows the Guide to passively log direct consultations 
// to the Archivalist without adding them to its own LLM context.
type ConsultationObserver struct {
	bus       EventBus
	sessionID string
}

func NewConsultationObserver(bus EventBus, sessionID string) *ConsultationObserver {
	return &ConsultationObserver{
		bus:       bus,
		sessionID: sessionID,
	}
}

// Start listens to all requests via a wildcard subscription to log direct consultations.
func (c *ConsultationObserver) Start(ctx context.Context) error {
	// Subscribe to a wildcard topic pattern to capture direct consultations
	topic := BuildSessionTopic(c.sessionID, "*", ChannelTypeRequests)
	
	// We use the SessionBusManager or TopicRouter in production to match wildcards.
	// For this standalone observer, we assume the EventBus supports pattern matching.
	_, err := c.bus.SubscribeAsync(topic, func(msg *Message) error {
		if msg.Type == MessageTypeDirectConsultation {
			c.logToArchivalist(msg)
		}
		return nil
	})
	
	return err
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

	// Emit a fire-and-forget logging event to the Archivalist
	logReq := &RouteRequest{
		CorrelationID:   generateMessageID(),
		Input:           "Log direct consultation: " + consReq.Query,
		SourceAgentID:   "guide",
		TargetAgentID:   "archivalist",
		FireAndForget:   true,
		SessionID:       c.sessionID,
		Timestamp:       time.Now(),
	}

	fwdMsg := NewForwardMessage(generateMessageID(), &ForwardedRequest{
		CorrelationID: logReq.CorrelationID,
		Input:         logReq.Input,
		Intent:        IntentStore,
		Domain:        DomainHistory,
		SourceAgentID: "guide",
		FireAndForget: true,
	})

	_ = c.bus.Publish(TopicRequests("archivalist"), fwdMsg)
}
