package engineer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/google/uuid"
)

// routeSyncTimeout overrides the default target-aware consultation timeout in
// tests. When left at the shared default, Engineer uses the target-aware
// consultation timeout policy.
var routeSyncTimeout = shared.DefaultConsultationTimeout

func engineerConsultationTimeout(target string) time.Duration {
	if routeSyncTimeout != shared.DefaultConsultationTimeout {
		return routeSyncTimeout
	}
	return shared.ConsultationInactivityTimeout(target)
}

func (e *Engineer) registerPendingConsult(correlationID string) *shared.PendingSyncWait {
	wait := shared.NewPendingSyncWait()
	e.pendingMu.Lock()
	e.pendingConsults[correlationID] = wait
	e.pendingMu.Unlock()
	return wait
}

func (e *Engineer) clearPendingConsult(correlationID string) {
	e.pendingMu.Lock()
	delete(e.pendingConsults, correlationID)
	e.pendingMu.Unlock()
}

// deliverConsultResponse delivers terminal messages and descendant stream
// activity to synchronous consultation waiters.
func (e *Engineer) deliverConsultResponse(msg *guide.Message) {
	if msg == nil || msg.CorrelationID == "" {
		return
	}
	switch msg.Type {
	case guide.MessageTypeResponse, guide.MessageTypeError:
		e.pendingMu.Lock()
		wait := e.pendingConsults[msg.CorrelationID]
		e.pendingMu.Unlock()
		if wait == nil {
			return
		}
		select {
		case wait.Response <- msg:
		default:
		}
	case guide.MessageTypeStream:
		for _, correlationID := range shared.PendingSyncActivityCorrelations(msg) {
			e.pendingMu.Lock()
			wait := e.pendingConsults[correlationID]
			e.pendingMu.Unlock()
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

func (e *Engineer) publishConsultRequest(req *guide.RouteRequest) error {
	if req == nil {
		return fmt.Errorf("route request is required")
	}
	if req.CorrelationID == "" {
		req.CorrelationID = "corr_" + uuid.NewString()
	}
	req.SourceAgentID = e.id
	req.SourceAgentName = "engineer"
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}
	msg := guide.NewRequestMessage(e.generateMessageID(), req)
	return e.bus.Publish(guide.TopicGuideRequests, msg)
}

// requestConsultSync publishes a RouteRequest and waits synchronously for
// the response, bounded by routeSyncTimeout.
func (e *Engineer) requestConsultSync(ctx context.Context, req *guide.RouteRequest) (*guide.Message, error) {
	if e.bus == nil || !e.running {
		return nil, fmt.Errorf("engineer bus is unavailable")
	}
	if req == nil {
		return nil, fmt.Errorf("route request is required")
	}
	waitCtx, release := shared.WithoutDeadlineCancellation(ctx)
	defer release()

	branchCtx, branch := shared.BeginAutoInterAgentRouteBranch(waitCtx, req.TargetAgentID, req.Input, req.Metadata)
	req.Metadata = branch.ApplyMetadata(branchCtx, req.Metadata)
	if req.ParentCorrelationID == "" {
		if stream, ok := shared.StreamMetadataFromContext(branchCtx); ok {
			req.ParentCorrelationID = stream.CorrelationID
		}
	}
	req.Metadata = shared.RouteMetadataWithInterAgentBranch(branchCtx, req.Metadata)
	req.ExplicitTarget = strings.TrimSpace(req.TargetAgentID) != ""
	response, err := shared.RetryBusyRouteRequest(branchCtx, req.TargetAgentID, shared.DefaultBusyRetryPolicy(req.TargetAgentID), func(attemptCtx context.Context, _ int) (*guide.Message, error) {
		req.CorrelationID = "corr_" + uuid.NewString()
		wait := e.registerPendingConsult(req.CorrelationID)
		defer e.clearPendingConsult(req.CorrelationID)

		if err := e.publishConsultRequest(req); err != nil {
			return nil, err
		}

		response, err := shared.WaitForPendingSyncResponse(
			attemptCtx,
			fmt.Sprintf("consultation to %q", req.TargetAgentID),
			engineerConsultationTimeout(req.TargetAgentID),
			wait,
		)
		if err != nil {
			return nil, err
		}
		if busyErr, ok := shared.BusyRouteResponseMessage(response, req.TargetAgentID); ok {
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

// requestConsultation is the high-level consultation helper that builds a
// RouteRequest, calls requestConsultSync, and returns ConsultationEvidence.
func (e *Engineer) requestConsultation(
	ctx context.Context,
	target, query, scope, sessionID string,
) (*shared.ConsultationEvidence, error) {
	return e.requestConsultationWithMetadata(ctx, target, query, scope, sessionID, nil)
}

func (e *Engineer) requestConsultationWithMetadata(
	ctx context.Context,
	target, query, scope, sessionID string,
	metadata map[string]any,
) (*shared.ConsultationEvidence, error) {
	req := &guide.RouteRequest{
		Input:         query,
		TargetAgentID: target,
		SessionID:     sessionID,
		Metadata:      shared.CloneMetadataMap(metadata),
	}
	admission := shared.AdmitConsultation(ctx, target, query, req.Metadata)
	if !admission.Allowed {
		return failedConsultEvidence(target, query, scope, "", shared.ConsultationAdmissionError(admission)), nil
	}
	researchDepth := shared.ConsultationResearchDepth(req.Metadata)
	branchCtx, branch := shared.BeginInterAgentBranch(ctx, shared.InterAgentBranchSpec{
		Kind:       shared.InterAgentToolEventKindConsult,
		ToolName:   "consult_" + strings.ReplaceAll(strings.TrimSpace(target), "-", "_"),
		AgentTypes: []string{target},
		Summary:    query,
		Args: map[string]any{
			"target": target,
			"query":  query,
			"scope":  scope,
			"depth":  string(researchDepth),
		},
	})
	req.Metadata = branch.ApplyMetadata(branchCtx, admission.Metadata)
	response, err := e.requestConsultSync(branchCtx, req)
	shared.RecordConsultationOutcome(branchCtx, admission.AttemptID, err == nil, shared.ConsultationDataFromMessage(response), err)
	branch.CompleteFromMessage(branchCtx, response, err)
	if err != nil {
		if shared.IsAgentBusyError(err) {
			return failedConsultEvidence(target, query, scope, req.CorrelationID, err), shared.ConsultationBusyDelegatedError(target, err)
		}
		return failedConsultEvidence(target, query, scope, req.CorrelationID, err), err
	}
	return buildConsultEvidence(target, query, scope, req.CorrelationID, response), nil
}

func buildConsultEvidence(
	target, query, scope, correlationID string,
	msg *guide.Message,
) *shared.ConsultationEvidence {
	evidence := &shared.ConsultationEvidence{
		Target:      target,
		Query:       query,
		Scope:       scope,
		Correlation: correlationID,
		RequestedAt: time.Now(),
		ReceivedAt:  time.Now(),
	}
	if msg == nil {
		evidence.Success = false
		evidence.Error = "empty consultation response"
		return evidence
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		evidence.Success = resp.Success
		evidence.Data = resp.Data
		evidence.Error = resp.Error
		return evidence
	}
	if errStr, ok := msg.GetError(); ok {
		evidence.Success = false
		evidence.Error = errStr
		return evidence
	}
	evidence.Success = false
	evidence.Error = "unsupported consultation payload"
	return evidence
}

func failedConsultEvidence(target, query, scope, corr string, err error) *shared.ConsultationEvidence {
	evidence := &shared.ConsultationEvidence{
		Target:      target,
		Query:       query,
		Scope:       scope,
		Correlation: corr,
		Success:     false,
		RequestedAt: time.Now(),
		ReceivedAt:  time.Now(),
	}
	if err != nil {
		evidence.Error = err.Error()
	}
	return evidence
}
