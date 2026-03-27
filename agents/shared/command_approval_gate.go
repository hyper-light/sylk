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

func (g *GuardianCommandGate) Authorize(ctx context.Context, req commandapproval.Request) (commandapproval.Evaluation, error) {
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
	sub, err := bus.SubscribeAsync(responseTopic, func(msg *guide.Message) error {
		if !isCommandApprovalTerminalMessage(msg, correlationID) {
			return nil
		}
		select {
		case waitCh <- msg:
		default:
		}
		return nil
	})
	if err != nil {
		return commandapproval.Evaluation{}, fmt.Errorf("subscribe command approval response: %w", err)
	}
	defer sub.Unsubscribe()

	payload, err := json.Marshal(req)
	if err != nil {
		return commandapproval.Evaluation{}, fmt.Errorf("encode command approval request: %w", err)
	}
	targetAgentID := "guardian"
	if g.cfg.GuardianTargetAgentID != nil {
		if resolved := strings.TrimSpace(g.cfg.GuardianTargetAgentID()); resolved != "" {
			targetAgentID = resolved
		}
	}
	branchCtx, branch := beginGuardianApprovalBranch(ctx, targetAgentID, req)
	if strings.TrimSpace(branch.branch.ParentCorrelationID) == "" {
		branchCtx, branch = BeginAutoInterAgentRouteBranch(ctx, targetAgentID, payload, map[string]any{
			"direct_skill": "command_execution_control",
			"tool_name":    req.ToolName,
			"summary":      req.Command,
		})
	}
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
		Metadata: branch.ApplyMetadata(branchCtx, map[string]any{
			"direct_skill": "command_execution_control",
			"tool_name":    req.ToolName,
		}),
	}
	if routeReq.ParentCorrelationID == "" {
		if stream, ok := StreamMetadataFromContext(branchCtx); ok {
			routeReq.ParentCorrelationID = stream.CorrelationID
		}
	}
	routeReq.Metadata = RouteMetadataWithInterAgentBranch(branchCtx, routeReq.Metadata)
	requestMsg := guide.NewRequestMessage("", routeReq).WithReplyTo(responseTopic)
	if err := bus.Publish(guide.TopicGuideRequests, requestMsg); err != nil {
		branch.Complete(branchCtx, "", "", err)
		return commandapproval.Evaluation{}, fmt.Errorf("publish command approval request: %w", err)
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
			branch.Complete(branchCtx, "", "", ctx.Err())
			return commandapproval.Evaluation{}, ctx.Err()
		}
	}
	publishGuardianApprovalResolvedProgress(branchCtx, req)
	branch.CompleteFromMessage(branchCtx, msg, nil)
	return decodeCommandApprovalMessage(msg)
}

func beginGuardianApprovalBranch(
	ctx context.Context,
	targetAgentID string,
	req commandapproval.Request,
) (context.Context, InterAgentBranchHandle) {
	branchCtx, branch := BeginAutoInterAgentRouteBranch(ctx, targetAgentID, nil, nil)
	if strings.TrimSpace(branch.branch.ParentCorrelationID) != "" {
		return branchCtx, branch
	}
	summary := guardianApprovalBranchSummary(req)
	return BeginInterAgentBranch(ctx, InterAgentBranchSpec{
		Kind:          InterAgentToolEventKindApproval,
		ToolName:      "approval_guardian",
		AgentTypes:    []string{firstNonEmptyApprovalTarget(targetAgentID)},
		Summary:       summary,
		SuccessStatus: InterAgentToolEventStatusDone,
		Args: map[string]any{
			"target":    firstNonEmptyApprovalTarget(targetAgentID),
			"tool_name": strings.TrimSpace(req.ToolName),
			"command":   strings.TrimSpace(req.Command),
			"domain":    strings.TrimSpace(req.Domain),
			"summary":   summary,
		},
	})
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

func decodeCommandApprovalMessage(msg *guide.Message) (commandapproval.Evaluation, error) {
	if msg == nil {
		return commandapproval.Evaluation{}, fmt.Errorf("command approval response is missing")
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		if !resp.Success {
			return commandapproval.Evaluation{}, fmt.Errorf("%s", strings.TrimSpace(resp.Error))
		}
		return decodeCommandApprovalEvaluation(resp.Data)
	}
	if errText, ok := msg.GetError(); ok {
		return commandapproval.Evaluation{}, fmt.Errorf("%s", strings.TrimSpace(errText))
	}
	return commandapproval.Evaluation{}, fmt.Errorf("unsupported command approval response payload")
}

func decodeCommandApprovalEvaluation(data any) (commandapproval.Evaluation, error) {
	if typed, ok := data.(commandapproval.Evaluation); ok {
		if typed.Decision == commandapproval.DecisionDeny {
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
	if eval.Decision == commandapproval.DecisionDeny {
		return eval, fmt.Errorf("%w: %s", commandapproval.ErrApprovalDenied, strings.TrimSpace(eval.Reason))
	}
	return eval, nil
}
