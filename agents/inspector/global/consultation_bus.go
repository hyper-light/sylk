package global

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

const routeSyncTimeout = 60 * time.Second

func (gi *GlobalInspector) registerPendingWait(correlationID string) <-chan *guide.Message {
	ch := make(chan *guide.Message, 1)
	gi.pendingMu.Lock()
	gi.pendingBus[correlationID] = ch
	gi.pendingMu.Unlock()
	return ch
}

func (gi *GlobalInspector) clearPendingWait(correlationID string) {
	gi.pendingMu.Lock()
	delete(gi.pendingBus, correlationID)
	gi.pendingMu.Unlock()
}

func (gi *GlobalInspector) deliverPendingMessage(msg *guide.Message) {
	if msg == nil {
		return
	}
	if msg.Type != guide.MessageTypeResponse && msg.Type != guide.MessageTypeError {
		return
	}

	gi.pendingMu.Lock()
	ch, ok := gi.pendingBus[msg.CorrelationID]
	gi.pendingMu.Unlock()

	if ok {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (gi *GlobalInspector) requestRouteSync(ctx context.Context, target, payload string) (*guide.Message, error) {
	if gi.bus == nil {
		return nil, fmt.Errorf("bus not available")
	}

	correlationID := fmt.Sprintf("gi_corr_%s", uuid.New().String()[:8])
	waitCh := gi.registerPendingWait(correlationID)
	defer gi.clearPendingWait(correlationID)

	req := &guide.RouteRequest{
		Input:           payload,
		SourceAgentID:   gi.id,
		SourceAgentName: "inspector",
		CorrelationID:   correlationID,
	}
	msg := guide.NewRequestMessage(gi.generateMessageID(), req)
	msg.CorrelationID = correlationID

	if err := gi.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		return nil, fmt.Errorf("publish escalation request: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, routeSyncTimeout)
	defer cancel()

	select {
	case resp := <-waitCh:
		return resp, nil
	case <-timeoutCtx.Done():
		return nil, fmt.Errorf("escalation request timed out after %v", routeSyncTimeout)
	}
}
