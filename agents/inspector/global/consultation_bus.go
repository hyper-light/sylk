package global

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

const routeSyncTimeout = shared.DefaultConsultationTimeout

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

func (gi *GlobalInspector) requestRouteSync(
	ctx context.Context,
	target string,
	payload any,
	metadata map[string]any,
) (*guide.Message, error) {
	if gi.bus == nil {
		return nil, fmt.Errorf("bus not available")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("target agent is required")
	}
	encoded, err := encodeConsultationPayload(payload)
	if err != nil {
		return nil, err
	}

	correlationID := fmt.Sprintf("gi_corr_%s", uuid.New().String()[:8])
	waitCh := gi.registerPendingWait(correlationID)
	defer gi.clearPendingWait(correlationID)

	sessionID := strings.TrimSpace(versioning.SessionIDFromContext(ctx))
	if sessionID == "" {
		sessionID = strings.TrimSpace(gi.config.SessionID)
	}
	req := &guide.RouteRequest{
		Input:           encoded,
		SourceAgentID:   gi.id,
		SourceAgentName: "inspector",
		TargetAgentID:   target,
		ExplicitTarget:  true,
		CorrelationID:   correlationID,
		SessionID:       sessionID,
		Timestamp:       time.Now().UTC(),
		Metadata:        metadata,
	}
	msg := guide.NewRequestMessage(gi.generateMessageID(), req)
	msg.CorrelationID = correlationID

	if err := gi.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		return nil, fmt.Errorf("publish escalation request: %w", err)
	}

	var response *guide.Message
	err = shared.RunWithContextLease(ctx, shared.ContextLeaseConfig{
		AttemptTimeout: routeSyncTimeout,
		MaxRefreshes:   shared.DefaultConsultationLeaseRefreshes,
		OnRefresh: func(info shared.ContextLeaseRefresh) {
			if gi.logger != nil {
				gi.logger.Info("escalation wait lease refreshed",
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
		return nil, shared.WrapLeaseTimeoutError("escalation request", routeSyncTimeout, err)
	}
	return response, nil
}

func encodeConsultationPayload(payload any) (string, error) {
	switch typed := payload.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("marshal consultation payload: %w", err)
		}
		return string(raw), nil
	}
}
