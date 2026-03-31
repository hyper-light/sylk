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

func (o *Orchestrator) registerPendingWait(correlationID string) *shared.PendingSyncWait {
	wait := shared.NewPendingSyncWait()
	o.pendingMu.Lock()
	o.pendingBus[correlationID] = wait
	o.pendingMu.Unlock()
	return wait
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
	switch msg.Type {
	case guide.MessageTypeResponse, guide.MessageTypeError:
		o.pendingMu.Lock()
		wait, ok := o.pendingBus[msg.CorrelationID]
		o.pendingMu.Unlock()
		if !ok || wait == nil {
			return false
		}

		select {
		case wait.Response <- msg:
		default:
		}
		return true
	case guide.MessageTypeStream:
		delivered := false
		for _, correlationID := range shared.PendingSyncActivityCorrelations(msg) {
			o.pendingMu.Lock()
			wait := o.pendingBus[correlationID]
			o.pendingMu.Unlock()
			if wait == nil {
				continue
			}
			select {
			case wait.Activity <- struct{}{}:
			default:
			}
			delivered = true
		}
		return delivered
	default:
		return false
	}
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
	waitCtx, release := shared.WithoutDeadlineCancellation(ctx)
	defer release()

	branchCtx, branch := shared.BeginAutoInterAgentRouteBranch(waitCtx, target, payload, metadata)
	metadata = branch.ApplyMetadata(branchCtx, metadata)
	req := &guide.RouteRequest{
		Input:           payload,
		SourceAgentID:   o.config.AgentID,
		SourceAgentName: "orchestrator",
		TargetAgentID:   target,
		ExplicitTarget:  true,
		SessionID:       o.config.SessionID,
		Metadata:        metadata,
	}
	if req.ParentCorrelationID == "" {
		if stream, ok := shared.StreamMetadataFromContext(branchCtx); ok {
			req.ParentCorrelationID = stream.CorrelationID
		}
	}
	req.Metadata = shared.RouteMetadataWithInterAgentBranch(branchCtx, req.Metadata)

	response, err := shared.RetryBusyRouteRequest(branchCtx, target, shared.DefaultBusyRetryPolicy(target), func(attemptCtx context.Context, _ int) (*guide.Message, error) {
		req.CorrelationID = "orchestrator_corr_" + uuid.NewString()[:8]
		req.Timestamp = time.Now()
		wait := o.registerPendingWait(req.CorrelationID)
		defer o.clearPendingWait(req.CorrelationID)

		msg := guide.NewRequestMessage(generateMessageID(), req)
		msg.CorrelationID = req.CorrelationID

		if err := o.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
			return nil, fmt.Errorf("publish route request: %w", err)
		}

		response, err := shared.WaitForPendingSyncResponse(
			attemptCtx,
			fmt.Sprintf("route request to %s", target),
			orchestratorRouteSyncTimeout,
			wait,
		)
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
