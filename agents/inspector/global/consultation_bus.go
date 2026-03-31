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

type pendingWait struct {
	response chan *guide.Message
	activity chan struct{}
}

func (gi *GlobalInspector) registerPendingWait(correlationID string) *pendingWait {
	wait := &pendingWait{
		response: make(chan *guide.Message, 1),
		activity: make(chan struct{}, 1),
	}
	gi.pendingMu.Lock()
	gi.pendingBus[correlationID] = wait
	gi.pendingMu.Unlock()
	return wait
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

	switch msg.Type {
	case guide.MessageTypeResponse, guide.MessageTypeError:
		gi.pendingMu.Lock()
		wait, ok := gi.pendingBus[msg.CorrelationID]
		gi.pendingMu.Unlock()
		if !ok || wait == nil {
			return
		}
		select {
		case wait.response <- msg:
		default:
		}
	case guide.MessageTypeStream:
		for _, correlationID := range shared.PendingSyncActivityCorrelations(msg) {
			gi.pendingMu.Lock()
			activityWait := gi.pendingBus[correlationID]
			gi.pendingMu.Unlock()
			if activityWait == nil {
				continue
			}
			select {
			case activityWait.activity <- struct{}{}:
			default:
			}
		}
	default:
		return
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
	routeCtx, release := shared.WithoutDeadlineCancellation(ctx)
	defer release()

	encoded, err := encodeConsultationPayload(payload)
	if err != nil {
		return nil, err
	}
	branchCtx, branch := shared.BeginAutoInterAgentRouteBranch(routeCtx, target, encoded, metadata)
	metadata = branch.ApplyMetadata(branchCtx, metadata)
	sessionID := strings.TrimSpace(versioning.SessionIDFromContext(routeCtx))
	if sessionID == "" {
		sessionID = strings.TrimSpace(gi.config.SessionID)
	}
	req := &guide.RouteRequest{
		Input:           encoded,
		SourceAgentID:   gi.id,
		SourceAgentName: "inspector",
		TargetAgentID:   target,
		ExplicitTarget:  true,
		SessionID:       sessionID,
		Metadata:        metadata,
	}
	if req.ParentCorrelationID == "" {
		if stream, ok := shared.StreamMetadataFromContext(branchCtx); ok {
			req.ParentCorrelationID = stream.CorrelationID
		}
	}
	req.Metadata = shared.RouteMetadataWithInterAgentBranch(branchCtx, req.Metadata)
	response, err := shared.RetryBusyRouteRequest(branchCtx, target, shared.DefaultBusyRetryPolicy(target), func(attemptCtx context.Context, _ int) (*guide.Message, error) {
		req.CorrelationID = fmt.Sprintf("gi_corr_%s", uuid.New().String()[:8])
		req.Timestamp = time.Now().UTC()
		wait := gi.registerPendingWait(req.CorrelationID)
		defer gi.clearPendingWait(req.CorrelationID)

		msg := guide.NewRequestMessage(gi.generateMessageID(), req)
		msg.CorrelationID = req.CorrelationID

		if err := gi.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
			return nil, fmt.Errorf("publish consultation request: %w", err)
		}

		response, err := waitForPendingResponse(attemptCtx, consultationWaitSubject(target), consultationInactivityTimeout(target), wait)
		if err != nil {
			return nil, err
		}
		if busyErr, ok := shared.BusyRouteResponseMessage(response, target); ok {
			return nil, busyErr
		}
		return response, nil
	})
	if err != nil {
		branch.Complete(branchCtx, "", "", err)
		return nil, err
	}
	branch.CompleteFromMessage(branchCtx, response, nil)
	return response, nil
}

func consultationInactivityTimeout(target string) time.Duration {
	return shared.ConsultationInactivityTimeout(target)
}

func consultationWaitSubject(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return "consultation request"
	}
	return fmt.Sprintf("consultation to %q", target)
}

func waitForPendingResponse(ctx context.Context, subject string, inactivityTimeout time.Duration, wait *pendingWait) (*guide.Message, error) {
	return shared.WaitForPendingSyncResponse(ctx, subject, inactivityTimeout, &shared.PendingSyncWait{
		Response: wait.response,
		Activity: wait.activity,
	})
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
