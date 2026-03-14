package shared

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

type GuideRouteSyncRequest struct {
	Bus           guide.EventBus
	ResponseTopic string
	Request       *guide.RouteRequest
}

// RequestGuideRouteSync publishes a Guide route request and waits for the
// matching terminal response on the provided response topic.
func RequestGuideRouteSync(ctx context.Context, cfg GuideRouteSyncRequest) (*guide.Message, error) {
	if cfg.Bus == nil {
		return nil, fmt.Errorf("guide route sync bus is not configured")
	}
	if cfg.Request == nil {
		return nil, fmt.Errorf("guide route sync request is required")
	}
	if cfg.ResponseTopic == "" {
		return nil, fmt.Errorf("guide route sync response topic is required")
	}

	req := *cfg.Request
	if req.CorrelationID == "" {
		req.CorrelationID = "route_" + uuid.NewString()[:12]
	}
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}

	waitCh := make(chan *guide.Message, 1)
	sub, err := cfg.Bus.Subscribe(cfg.ResponseTopic, func(msg *guide.Message) error {
		if msg == nil || msg.CorrelationID != req.CorrelationID {
			return nil
		}
		switch msg.Type {
		case guide.MessageTypeResponse, guide.MessageTypeError:
		default:
			return nil
		}
		select {
		case waitCh <- msg:
		default:
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", cfg.ResponseTopic, err)
	}
	defer sub.Unsubscribe()

	msg := guide.NewRequestMessage("route_req_"+uuid.NewString()[:8], &req)
	if err := cfg.Bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		return nil, fmt.Errorf("publish guide route request: %w", err)
	}

	var response *guide.Message
	if err := RunWithContextLease(ctx, ContextLeaseConfig{
		AttemptTimeout: DefaultConsultationTimeout,
		MaxRefreshes:   DefaultConsultationLeaseRefreshes,
	}, func(waitCtx context.Context) error {
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case response = <-waitCh:
			return nil
		}
	}); err != nil {
		return nil, WrapLeaseTimeoutError(
			fmt.Sprintf("guide route to %q", req.TargetAgentID),
			DefaultConsultationTimeout,
			err,
		)
	}
	if response == nil {
		return nil, fmt.Errorf("guide route %s returned empty response", req.CorrelationID)
	}
	return response, nil
}
