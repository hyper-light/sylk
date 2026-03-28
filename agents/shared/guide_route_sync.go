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

	branchCtx, branch := BeginAutoInterAgentRouteBranch(waitCtx, req.TargetAgentID, req.Input, req.Metadata)
	req.Metadata = branch.ApplyMetadata(branchCtx, req.Metadata)
	if req.CorrelationID == "" {
		req.CorrelationID = "route_" + uuid.NewString()[:12]
	}
	if req.ParentCorrelationID == "" {
		if stream, ok := StreamMetadataFromContext(branchCtx); ok {
			req.ParentCorrelationID = stream.CorrelationID
		}
	}
	req.Metadata = RouteMetadataWithInterAgentBranch(branchCtx, req.Metadata)
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}

	wait := NewPendingSyncWait()
	sub, err := cfg.Bus.Subscribe(cfg.ResponseTopic, func(msg *guide.Message) error {
		if msg == nil {
			return nil
		}
		switch msg.Type {
		case guide.MessageTypeResponse, guide.MessageTypeError:
			if msg.CorrelationID != req.CorrelationID {
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
				if correlationID == req.CorrelationID {
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

	msg := guide.NewRequestMessage("route_req_"+uuid.NewString()[:8], &req)
	if err := cfg.Bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		branch.Complete(branchCtx, "", "", err)
		return nil, fmt.Errorf("publish guide route request: %w", err)
	}

	response, err := WaitForPendingSyncResponse(
		branchCtx,
		fmt.Sprintf("guide route to %q", req.TargetAgentID),
		inactivityTimeoutOrDefault(cfg.InactivityTimeout, req.TargetAgentID),
		wait,
	)
	if err != nil {
		cancelGuideRouteRequest(cfg.Bus, &req, err)
		branch.Complete(branchCtx, "", "", err)
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("guide route %s returned empty response", req.CorrelationID)
	}
	branch.CompleteFromMessage(branchCtx, response, nil)
	return response, nil
}

func cancelGuideRouteRequest(bus guide.EventBus, req *guide.RouteRequest, cause error) {
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
	_ = bus.Publish(guide.TopicGuideRequests, guide.NewActionMessage("", action))
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
