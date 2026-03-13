package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/google/uuid"
)

const orchestratorRouteSyncTimeout = 45 * time.Second

func (o *Orchestrator) registerPendingWait(correlationID string) <-chan *guide.Message {
	ch := make(chan *guide.Message, 1)
	o.pendingMu.Lock()
	o.pendingBus[correlationID] = ch
	o.pendingMu.Unlock()
	return ch
}

func (o *Orchestrator) clearPendingWait(correlationID string) {
	o.pendingMu.Lock()
	delete(o.pendingBus, correlationID)
	o.pendingMu.Unlock()
}

func (o *Orchestrator) deliverPendingMessage(msg *guide.Message) bool {
	if msg == nil {
		return false
	}
	if msg.Type != guide.MessageTypeResponse && msg.Type != guide.MessageTypeError {
		return false
	}

	o.pendingMu.Lock()
	ch, ok := o.pendingBus[msg.CorrelationID]
	o.pendingMu.Unlock()
	if !ok {
		return false
	}

	select {
	case ch <- msg:
	default:
	}
	return true
}

func (o *Orchestrator) requestRouteSync(
	ctx context.Context,
	target string,
	input any,
	metadata map[string]any,
) (*guide.Message, error) {
	if o.bus == nil || !o.running {
		return nil, fmt.Errorf("orchestrator not running")
	}

	payload, err := encodeRouteSyncInput(input)
	if err != nil {
		return nil, err
	}

	correlationID := "orchestrator_corr_" + uuid.NewString()[:8]
	waitCh := o.registerPendingWait(correlationID)
	defer o.clearPendingWait(correlationID)

	req := &guide.RouteRequest{
		Input:           payload,
		SourceAgentID:   o.config.AgentID,
		SourceAgentName: "orchestrator",
		TargetAgentID:   target,
		ExplicitTarget:  true,
		CorrelationID:   correlationID,
		SessionID:       o.config.SessionID,
		Timestamp:       time.Now(),
		Metadata:        metadata,
	}
	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.CorrelationID = correlationID

	if err := o.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		return nil, fmt.Errorf("publish route request: %w", err)
	}

	var response *guide.Message
	err = shared.RunWithContextLease(ctx, shared.ContextLeaseConfig{
		AttemptTimeout: orchestratorRouteSyncTimeout,
		MaxRefreshes:   shared.DefaultConsultationLeaseRefreshes,
		OnRefresh: func(info shared.ContextLeaseRefresh) {
			o.logInfo("orchestrator route wait lease refreshed",
				"target", target,
				"correlation_id", correlationID,
				"refresh_count", info.RefreshCount,
				"attempt_timeout", info.AttemptTimeout.String(),
				"error", info.Error)
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
		return nil, shared.WrapLeaseTimeoutError(
			fmt.Sprintf("route request to %s", target),
			orchestratorRouteSyncTimeout,
			err,
		)
	}
	return response, nil
}

func encodeRouteSyncInput(input any) (string, error) {
	switch v := input.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal route input: %w", err)
		}
		return string(data), nil
	}
}
