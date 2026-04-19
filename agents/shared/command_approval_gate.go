package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

type GuardianCommandGateConfig struct {
	BusProvider            func() guide.EventBus
	SourceAgentID          func() string
	SourceAgentType        string
	SourceAgentName        string
	GuardianTargetAgentID  func() string
	ApprovalRequestTimeout time.Duration
}

type GuardianCommandGate struct {
	cfg GuardianCommandGateConfig
}

var guardianApprovalKeepaliveInterval = 10 * time.Second

func NewGuardianCommandGate(cfg GuardianCommandGateConfig) *GuardianCommandGate {
	return &GuardianCommandGate{cfg: cfg}
}

func WithGuardianCommandGate(ctx context.Context, cfg GuardianCommandGateConfig) context.Context {
	return commandapproval.WithGate(ctx, NewGuardianCommandGate(cfg))
}

func (g *GuardianCommandGate) Authorize(ctx context.Context, req commandapproval.Request) (eval commandapproval.Evaluation, err error) {
	if g == nil || g.cfg.BusProvider == nil {
		return commandapproval.Evaluation{}, fmt.Errorf("guardian command gate is not configured")
	}
	bus := g.cfg.BusProvider()
	if bus == nil {
		return commandapproval.Evaluation{}, fmt.Errorf("guide bus is unavailable for command approval")
	}
	sourceAgentID := ""
	if g.cfg.SourceAgentID != nil {
		sourceAgentID = strings.TrimSpace(g.cfg.SourceAgentID())
	}
	if sourceAgentID == "" {
		return commandapproval.Evaluation{}, fmt.Errorf("source agent id is required for command approval")
	}
	sourceAgentType := strings.TrimSpace(g.cfg.SourceAgentType)
	if sourceAgentType == "" {
		sourceAgentType = sourceAgentID
	}
	if strings.TrimSpace(req.SessionID) == "" {
		req.SessionID = versioning.SessionIDFromContext(ctx)
	}
	if strings.TrimSpace(req.AgentID) == "" {
		req.AgentID = sourceAgentID
	}
	if strings.TrimSpace(req.AgentType) == "" {
		req.AgentType = sourceAgentType
	}

	correlationID := "cmd_approval_" + uuid.NewString()
	responseTopic := guide.TopicResponses(sourceAgentType, sourceAgentID)
	waitCh := make(chan *guide.Message, 1)
	sub, subErr := bus.SubscribeAsync(responseTopic, func(msg *guide.Message) error {
		if !isCommandApprovalTerminalMessage(msg, correlationID) {
			return nil
		}
		select {
		case waitCh <- msg:
		default:
		}
		return nil
	})
	if subErr != nil {
		return commandapproval.Evaluation{}, fmt.Errorf("subscribe command approval response: %w", subErr)
	}
	defer sub.Unsubscribe()

	payload, payloadErr := json.Marshal(req)
	if payloadErr != nil {
		return commandapproval.Evaluation{}, fmt.Errorf("encode command approval request: %w", payloadErr)
	}
	targetAgentID := "guardian"
	if g.cfg.GuardianTargetAgentID != nil {
		if resolved := strings.TrimSpace(g.cfg.GuardianTargetAgentID()); resolved != "" {
			targetAgentID = resolved
		}
	}
	branchCtx, branch := beginGuardianApprovalBranch(ctx, targetAgentID, req)
	// The branch is the canonical owner of the approval row's lifecycle. A
	// deferred safety-net Complete guarantees the row is closed on every exit
	// path (early error return, context cancel after the inline handler
	// already closed it, panic propagating through the goroutine). The
	// handle's atomic-once guard makes this idempotent with any earlier
	// explicit Complete on the success path. We do NOT recover panics —
	// invariant violations must surface to the goroutine boundary so they
	// appear in logs.
	//
	// abnormalExit is a sentinel error initialized non-nil and cleared at the
	// natural success point (right after the explicit CompleteFromMessage
	// fires). When the deferred Complete runs from a normal error return,
	// terminalErr or err is set and wins via firstNonNilError. When the
	// deferred Complete runs during panic propagation (no error variables
	// set, success point never reached), abnormalExit wins so the row is
	// stamped as failed instead of misleadingly successful.
	var terminalErr error
	abnormalExit := fmt.Errorf("command approval gate unwound abnormally without returning (tool %q)", req.ToolName)
	defer func() {
		branch.Complete(branchCtx, "", "", firstNonNilError(terminalErr, err, abnormalExit))
	}()

	routeMetadata := InheritedBranchMetadata(branchCtx, map[string]any{
		"direct_skill": "command_execution_control",
		"tool_name":    req.ToolName,
		"summary":      req.Command,
	})
	routeReq := &guide.RouteRequest{
		CorrelationID: correlationID,
		Input:         string(payload),
		SourceAgentID: sourceAgentID,
		SourceAgentName: strings.TrimSpace(
			g.cfg.SourceAgentName,
		),
		TargetAgentID: targetAgentID,
		SessionID:     req.SessionID,
		Timestamp:     time.Now(),
		Metadata:      branch.ApplyMetadata(branchCtx, routeMetadata),
	}
	if routeReq.ParentCorrelationID == "" {
		if stream, ok := StreamMetadataFromContext(branchCtx); ok {
			routeReq.ParentCorrelationID = stream.CorrelationID
		}
	}
	routeReq.Metadata = RouteMetadataWithInterAgentBranch(branchCtx, routeReq.Metadata)
	requestMsg := guide.NewRequestMessage("", routeReq).WithReplyTo(responseTopic)
	if publishErr := bus.Publish(guide.TopicGuideRequests, requestMsg); publishErr != nil {
		terminalErr = publishErr
		return commandapproval.Evaluation{}, fmt.Errorf("publish command approval request: %w", publishErr)
	}

	publishGuardianApprovalKeepalive(branchCtx, req)
	interval := guardianApprovalKeepaliveInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var msg *guide.Message
	for msg == nil {
		select {
		case msg = <-waitCh:
		case <-ticker.C:
			publishGuardianApprovalKeepalive(branchCtx, req)
		case <-ctx.Done():
			terminalErr = ctx.Err()
			return commandapproval.Evaluation{}, ctx.Err()
		}
	}
	publishGuardianApprovalResolvedProgress(branchCtx, req)
	branch.CompleteFromMessage(branchCtx, msg, nil)
	// We reached the natural success point — Complete fired authoritatively
	// via CompleteFromMessage. Clear the abnormal-exit sentinel so the
	// deferred safety-net Complete (which is now a no-op via the atomic-once
	// guard) does not also leak a misleading abnormal-exit error if anything
	// downstream were to ever observe it.
	abnormalExit = nil
	return decodeCommandApprovalMessage(msg, req)
}

// firstNonNilError returns the first non-nil error from the list. Used to
// resolve which error the deferred branch.Complete should reflect when the
// function has multiple error sources (explicit return err, sentinel
// abnormal-exit error, context cancellation).
func firstNonNilError(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func beginGuardianApprovalBranch(
	ctx context.Context,
	targetAgentID string,
	req commandapproval.Request,
) (context.Context, InterAgentBranchHandle) {
	summary := guardianApprovalBranchSummary(req)
	// Stamp a stable ThreadKey on the approval branch so the TUI can
	// consolidate the Start (emitted here) with the Complete (emitted later
	// by CompleteFromMessage) even when they route through different scopes
	// — and so any intermediate status update can target the same row by
	// identity rather than creating a parallel one. The key is built from
	// the approval correlation id in the upstream dispatcher; we fall back
	// to a request-derived signature when that's unavailable so the branch
	// always carries a non-empty identity.
	return BeginInterAgentBranch(ctx, InterAgentBranchSpec{
		Kind:          InterAgentToolEventKindApproval,
		ToolName:      "approval_guardian",
		AgentTypes:    []string{firstNonEmptyApprovalTarget(targetAgentID)},
		Summary:       summary,
		SuccessStatus: InterAgentToolEventStatusDone,
		ThreadKey:     guardianApprovalThreadKey(req),
		Args: map[string]any{
			"target":    firstNonEmptyApprovalTarget(targetAgentID),
			"tool_name": strings.TrimSpace(req.ToolName),
			"command":   strings.TrimSpace(req.Command),
			"domain":    strings.TrimSpace(req.Domain),
			"summary":   summary,
		},
	})
}

// guardianApprovalThreadKey produces a deterministic thread identity for a
// single approval lifecycle. The ToolCallKey already identifies Start/Complete
// across the bus, but the UI's InterAgent matcher consults ThreadKey when an
// origin-update arrives from a different scope or the Start/Complete drift
// routing (e.g. guardian re-publishes a status mid-flight). A non-empty
// ThreadKey makes consolidation identity-based rather than dependent on the
// Start row surviving at the same list position until Complete arrives.
func guardianApprovalThreadKey(req commandapproval.Request) string {
	parts := []string{
		strings.TrimSpace(req.SessionID),
		strings.TrimSpace(req.AgentID),
		strings.TrimSpace(req.ToolName),
		strings.TrimSpace(req.Command),
	}
	base := strings.Join(parts, "|")
	if strings.TrimSpace(base) == "" {
		return ""
	}
	return "guardian_approval:" + base
}

func guardianApprovalBranchSummary(req commandapproval.Request) string {
	switch {
	case strings.TrimSpace(req.Domain) != "":
		return "Requesting Guardian approval for " + strings.TrimSpace(req.Domain)
	case strings.TrimSpace(req.ToolName) != "":
		return "Requesting Guardian approval for " + strings.TrimSpace(req.ToolName)
	case strings.TrimSpace(req.Command) != "":
		return "Requesting Guardian approval"
	default:
		return "Requesting Guardian approval"
	}
}

func firstNonEmptyApprovalTarget(value string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return "guardian"
}

func publishGuardianApprovalKeepalive(ctx context.Context, req commandapproval.Request) {
	pp := ProgressPublisherFromContext(ctx)
	if pp == nil {
		return
	}
	message := "Waiting for Guardian approval"
	switch {
	case strings.TrimSpace(req.Domain) != "":
		message += " for " + strings.TrimSpace(req.Domain)
	case strings.TrimSpace(req.ToolName) != "":
		message += " for " + strings.TrimSpace(req.ToolName)
	}
	pp.PublishState(events.AgentUIStateValidating, message)
}

func publishGuardianApprovalResolvedProgress(ctx context.Context, req commandapproval.Request) {
	pp := ProgressPublisherFromContext(ctx)
	if pp == nil {
		return
	}
	message := "Guardian approval received"
	switch {
	case strings.TrimSpace(req.Domain) != "":
		message += " for " + strings.TrimSpace(req.Domain)
	case strings.TrimSpace(req.ToolName) != "":
		message += " for " + strings.TrimSpace(req.ToolName)
	}
	pp.Publish(message)
}

func isCommandApprovalTerminalMessage(msg *guide.Message, correlationID string) bool {
	if msg == nil || strings.TrimSpace(msg.CorrelationID) != correlationID {
		return false
	}
	if _, ok := msg.GetRouteResponse(); ok {
		return true
	}
	_, ok := msg.GetError()
	return ok
}

func decodeCommandApprovalMessage(msg *guide.Message, req commandapproval.Request) (commandapproval.Evaluation, error) {
	if msg == nil {
		return commandapproval.Evaluation{}, fmt.Errorf("command approval response is missing")
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		if !resp.Success {
			return commandapproval.Evaluation{}, fmt.Errorf("%s", strings.TrimSpace(resp.Error))
		}
		return decodeCommandApprovalEvaluation(resp.Data, req)
	}
	if errText, ok := msg.GetError(); ok {
		return commandapproval.Evaluation{}, fmt.Errorf("%s", strings.TrimSpace(errText))
	}
	return commandapproval.Evaluation{}, fmt.Errorf("unsupported command approval response payload")
}

func decodeCommandApprovalEvaluation(data any, req commandapproval.Request) (commandapproval.Evaluation, error) {
	if typed, ok := data.(commandapproval.Evaluation); ok {
		if commandApprovalEvaluationShouldError(req, typed) {
			return typed, fmt.Errorf("%w: %s", commandapproval.ErrApprovalDenied, strings.TrimSpace(typed.Reason))
		}
		return typed, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return commandapproval.Evaluation{}, fmt.Errorf("marshal command approval response: %w", err)
	}
	var eval commandapproval.Evaluation
	if err := json.Unmarshal(raw, &eval); err != nil {
		return commandapproval.Evaluation{}, fmt.Errorf("decode command approval response: %w", err)
	}
	if commandApprovalEvaluationShouldError(req, eval) {
		return eval, fmt.Errorf("%w: %s", commandapproval.ErrApprovalDenied, strings.TrimSpace(eval.Reason))
	}
	return eval, nil
}

func commandApprovalEvaluationShouldError(_ commandapproval.Request, eval commandapproval.Evaluation) bool {
	return eval.Decision == commandapproval.DecisionDeny
}
