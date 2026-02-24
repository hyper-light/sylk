package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

// routeSyncTimeout is the maximum wait time for a synchronous bus RPC.
const routeSyncTimeout = 60 * time.Second

// registerPendingWait creates a buffered channel for a correlation ID.
func (pi *PipelineInspector) registerPendingWait(correlationID string) <-chan *guide.Message {
	ch := make(chan *guide.Message, 1)
	pi.pendingMu.Lock()
	pi.pendingBus[correlationID] = ch
	pi.pendingMu.Unlock()
	return ch
}

// clearPendingWait removes a pending wait channel.
func (pi *PipelineInspector) clearPendingWait(correlationID string) {
	pi.pendingMu.Lock()
	delete(pi.pendingBus, correlationID)
	pi.pendingMu.Unlock()
}

// deliverPendingMessage routes a response to a waiting channel.
func (pi *PipelineInspector) deliverPendingMessage(msg *guide.Message) {
	if msg == nil {
		return
	}
	// Only deliver terminal message types.
	if msg.Type != guide.MessageTypeResponse && msg.Type != guide.MessageTypeError {
		return
	}

	pi.pendingMu.Lock()
	ch, ok := pi.pendingBus[msg.CorrelationID]
	pi.pendingMu.Unlock()

	if ok {
		select {
		case ch <- msg:
		default:
		}
	}
}

// requestRouteSync sends a request and waits for a synchronous response.
func (pi *PipelineInspector) requestRouteSync(ctx context.Context, target, payload string) (*guide.Message, error) {
	if pi.bus == nil {
		return nil, fmt.Errorf("bus not available")
	}

	correlationID := fmt.Sprintf("pi_corr_%s", uuid.New().String()[:8])
	waitCh := pi.registerPendingWait(correlationID)
	defer pi.clearPendingWait(correlationID)

	req := &guide.RouteRequest{
		Input:           payload,
		SourceAgentID:   pi.id,
		SourceAgentName: "inspector-pipeline",
		CorrelationID:   correlationID,
	}
	msg := guide.NewRequestMessage(pi.generateMessageID(), req)
	msg.CorrelationID = correlationID

	if err := pi.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		return nil, fmt.Errorf("publish correction request: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, routeSyncTimeout)
	defer cancel()

	select {
	case resp := <-waitCh:
		return resp, nil
	case <-timeoutCtx.Done():
		return nil, fmt.Errorf("correction request timed out after %v", routeSyncTimeout)
	}
}
