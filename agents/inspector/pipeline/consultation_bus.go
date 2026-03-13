package pipeline

import (
	"context"
	"fmt"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/google/uuid"
)

// routeSyncTimeout is the maximum wait time for a synchronous bus RPC.
const routeSyncTimeout = shared.DefaultConsultationTimeout

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

	var response *guide.Message
	err := shared.RunWithContextLease(ctx, shared.ContextLeaseConfig{
		AttemptTimeout: routeSyncTimeout,
		MaxRefreshes:   shared.DefaultConsultationLeaseRefreshes,
		OnRefresh: func(info shared.ContextLeaseRefresh) {
			if pi.logger != nil {
				pi.logger.Info("correction wait lease refreshed",
					"target", target,
					"correlation_id", correlationID,
					"refresh_count", info.RefreshCount,
					"attempt_timeout", info.AttemptTimeout.String(),
					"error", info.Error)
			}
		},
	}, func(waitCtx context.Context) error {
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case response = <-waitCh:
			return nil
		}
	})
	if err != nil {
		return nil, shared.WrapLeaseTimeoutError("correction request", routeSyncTimeout, err)
	}
	return response, nil
}
