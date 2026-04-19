package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/google/uuid"
)

// routeSyncTimeout is the default wait budget used by other pipeline-inspector
// code paths. Academic-targeted waits override this through the shared
// consultation timeout helper at the actual blocking call site.
const routeSyncTimeout = shared.DefaultConsultationTimeout

// registerPendingWait creates a buffered channel for a correlation ID.
func (pi *PipelineInspector) registerPendingWait(correlationID string) *shared.PendingSyncWait {
	wait := shared.NewPendingSyncWait()
	pi.pendingMu.Lock()
	pi.pendingBus[correlationID] = wait
	pi.pendingMu.Unlock()
	return wait
}

// clearPendingWait removes a pending wait channel.
func (pi *PipelineInspector) clearPendingWait(correlationID string) {
	pi.pendingMu.Lock()
	delete(pi.pendingBus, correlationID)
	pi.pendingMu.Unlock()
}

// deliverPendingMessage routes terminal responses and descendant child-stream
// activity to synchronous waiters.
func (pi *PipelineInspector) deliverPendingMessage(msg *guide.Message) {
	if msg == nil {
		return
	}
	switch msg.Type {
	case guide.MessageTypeResponse, guide.MessageTypeError:
		pi.pendingMu.Lock()
		wait, ok := pi.pendingBus[msg.CorrelationID]
		pi.pendingMu.Unlock()
		if !ok || wait == nil {
			return
		}
		select {
		case wait.Response <- msg:
		default:
		}
	case guide.MessageTypeStream:
		for _, correlationID := range shared.PendingSyncActivityCorrelations(msg) {
			pi.pendingMu.Lock()
			wait := pi.pendingBus[correlationID]
			pi.pendingMu.Unlock()
			if wait == nil {
				continue
			}
			select {
			case wait.Activity <- struct{}{}:
			default:
			}
		}
	default:
		return
	}
}

// requestRouteSync sends a request and waits for a synchronous response.
func (pi *PipelineInspector) requestRouteSync(ctx context.Context, target, payload string) (*guide.Message, error) {
	if pi.bus == nil {
		return nil, fmt.Errorf("bus not available")
	}
	waitCtx, release := shared.WithoutDeadlineCancellation(ctx)
	defer release()

	req := &guide.RouteRequest{
		Input:           payload,
		SourceAgentID:   pi.id,
		SourceAgentName: "inspector-pipeline",
		TargetAgentID:   target,
		ExplicitTarget:  strings.TrimSpace(target) != "",
	}
	if req.ParentCorrelationID == "" {
		if stream, ok := shared.StreamMetadataFromContext(waitCtx); ok {
			req.ParentCorrelationID = stream.CorrelationID
		}
	}
	req.Metadata = shared.InheritedBranchMetadata(waitCtx, req.Metadata)
	req.Metadata = shared.RouteMetadataWithInterAgentBranch(waitCtx, req.Metadata)
	response, err := shared.RetryBusyRouteRequest(waitCtx, target, shared.DefaultBusyRetryPolicy(target), func(attemptCtx context.Context, _ int) (*guide.Message, error) {
		req.CorrelationID = fmt.Sprintf("pi_corr_%s", uuid.New().String()[:8])
		wait := pi.registerPendingWait(req.CorrelationID)
		defer pi.clearPendingWait(req.CorrelationID)

		msg := guide.NewRequestMessage(pi.generateMessageID(), req)
		msg.CorrelationID = req.CorrelationID

		if err := pi.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
			return nil, fmt.Errorf("publish correction request: %w", err)
		}

		response, err := shared.WaitForPendingSyncResponse(
			attemptCtx,
			"correction request",
			shared.ConsultationInactivityTimeout(target),
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
		return nil, err
	}
	return response, nil
}
