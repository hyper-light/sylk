package shared

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

type GuideRouteSyncRequest struct {
	Bus               guide.EventBus
	ResponseTopic     string
	Request           *guide.RouteRequest
	InactivityTimeout time.Duration
	OnMessage         func(*guide.Message)
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
	waitCtx, release := WithoutDeadlineCancellation(ctx)
	defer release()

	req.Metadata = InheritedBranchMetadata(waitCtx, req.Metadata)
	if req.ParentCorrelationID == "" {
		if stream, ok := StreamMetadataFromContext(waitCtx); ok {
			req.ParentCorrelationID = stream.CorrelationID
		}
	}
	req.Metadata = RouteMetadataWithInterAgentBranch(waitCtx, req.Metadata)
	response, err := RetryBusyRouteRequest(waitCtx, req.TargetAgentID, DefaultBusyRetryPolicy(req.TargetAgentID), func(attemptCtx context.Context, _ int) (*guide.Message, error) {
		attemptReq := req
		attemptReq.CorrelationID = "route_" + uuid.NewString()[:12]
		attemptReq.Timestamp = time.Now()

		wait := NewPendingSyncWait()
		sub, err := cfg.Bus.Subscribe(cfg.ResponseTopic, func(msg *guide.Message) error {
			if msg == nil {
				return nil
			}
			switch msg.Type {
			case guide.MessageTypeResponse, guide.MessageTypeError:
				if msg.CorrelationID != attemptReq.CorrelationID {
					return nil
				}
				if cfg.OnMessage != nil {
					cfg.OnMessage(msg)
				}
				select {
				case wait.Response <- msg:
				default:
				}
			case guide.MessageTypeStream:
				relevant := false
				for _, correlationID := range PendingSyncActivityCorrelations(msg) {
					if correlationID == attemptReq.CorrelationID {
						relevant = true
						break
					}
				}
				if !relevant {
					return nil
				}
				if cfg.OnMessage != nil {
					cfg.OnMessage(msg)
				}
				select {
				case wait.Activity <- struct{}{}:
				default:
				}
			default:
				return nil
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("subscribe %s: %w", cfg.ResponseTopic, err)
		}
		defer sub.Unsubscribe()

		msg := guide.NewRequestMessage("route_req_"+uuid.NewString()[:8], &attemptReq)
		if err := cfg.Bus.Publish(guide.TopicGuideRequests, msg); err != nil {
			return nil, fmt.Errorf("publish guide route request: %w", err)
		}

		response, err := WaitForPendingSyncResponse(
			attemptCtx,
			fmt.Sprintf("guide route to %q", attemptReq.TargetAgentID),
			inactivityTimeoutOrDefault(cfg.InactivityTimeout, attemptReq.TargetAgentID),
			wait,
		)
		if err != nil {
			cancelGuideRouteRequest(attemptCtx, cfg.Bus, &attemptReq, err)
			return nil, err
		}
		if response == nil {
			return nil, fmt.Errorf("guide route %s returned empty response", attemptReq.CorrelationID)
		}
		if busyErr, ok := BusyRouteResponseMessage(response, attemptReq.TargetAgentID); ok {
			return nil, busyErr
		}
		req.CorrelationID = attemptReq.CorrelationID
		return response, nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func cancelGuideRouteRequest(ctx context.Context, bus guide.EventBus, req *guide.RouteRequest, cause error) {
	if bus == nil || req == nil || strings.TrimSpace(req.CorrelationID) == "" || strings.TrimSpace(req.TargetAgentID) == "" {
		return
	}
	action := &guide.ActionRequest{
		CorrelationID:       req.CorrelationID,
		ParentCorrelationID: req.ParentCorrelationID,
		SourceAgentID:       firstNonEmptyString(req.SourceAgentID, "guide-route-sync"),
		SourceAgentName:     firstNonEmptyString(req.SourceAgentName, "GuideRouteSync"),
		TargetAgentID:       req.TargetAgentID,
		Action:              "cancel",
		Data: map[string]any{
			"reason":               errorString(cause),
			"sync_route_cancelled": true,
		},
		Timestamp: time.Now(),
	}
	// cancellation is best-effort by design — the sync route has already
	// returned an error to the caller. A failed cancel leaves a pending
	// route that the target agent will eventually time out, but we cannot
	// propagate an error here (the function's callers are already unwinding
	// a failure). Always log the delivery failure so stuck-cancel cases are
	// observable in telemetry.
	if err := bus.Publish(guide.TopicGuideRequests, guide.NewActionMessage("", action)); err != nil {
		if lm := LogMetaFromContext(ctx); lm.EventLogger != nil {
			LogWarning(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
				"guide_route_sync_cancel_publish_failed", map[string]any{
					"correlation_id": req.CorrelationID,
					"target_agent":   req.TargetAgentID,
					"cause":          errorString(cause),
					"error":          err.Error(),
				})
		}
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func inactivityTimeoutOrDefault(timeout time.Duration, target string) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return ConsultationInactivityTimeout(target)
}
