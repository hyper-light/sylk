package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

const commandApprovalControlTimeout = 5 * time.Second

func commandExecutionControlSkill(g *Guardian) *skills.Skill {
	return skills.NewSkill("command_execution_control").
		Description("Evaluate and gate agent command execution requests through Guardian approval policy.").
		Domain("control").
		Keywords("guardian", "command", "approval", "policy", "execution").
		Priority(100).
		Usage("Direct-skill only. Accepts a CommandApproval request payload and returns the final approval evaluation.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var req commandapproval.Request
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid command approval request payload: %w", err)
			}
			return g.evaluateCommandApproval(ctx, &req)
		}).
		Build()
}

func (g *Guardian) evaluateCommandApproval(ctx context.Context, req *commandapproval.Request) (commandapproval.Evaluation, error) {
	if req == nil {
		return commandapproval.Evaluation{}, fmt.Errorf("command approval request is required")
	}
	evaluator := commandapproval.NewEvaluator(g.commandRules)
	eval, err := evaluator.Evaluate(*req)
	if err != nil {
		return commandapproval.Evaluation{}, err
	}
	switch eval.Decision {
	case commandapproval.DecisionAllow:
		return eval, nil
	case commandapproval.DecisionDeny:
		return eval, nil
	default:
	}

	result, err := g.requestCommandApproval(ctx, g.commandApprovalProposal(*req, eval.Analysis))
	if err != nil {
		return commandapproval.Evaluation{}, err
	}
	persistNote := ""
	if result.Decision == ApprovalAllowAlways || result.Decision == ApprovalDenyAlways {
		action := commandapproval.RuleActionAllow
		if result.Decision == ApprovalDenyAlways {
			action = commandapproval.RuleActionDeny
		}
		if recordErr := g.commandRules.Record(commandapproval.Rule{
			MatchKey:  eval.Analysis.PersistKey,
			Action:    action,
			RuleLabel: eval.Analysis.PersistLabel,
			Summary:   eval.Analysis.Summary,
			CreatedAt: time.Now().UTC(),
		}); recordErr != nil {
			persistNote = " Rule persistence failed: " + recordErr.Error()
		}
	}
	if !result.Approved {
		eval.Decision = commandapproval.DecisionDeny
		eval.Source = commandapproval.MatchSourceInteractive
		eval.Reason = firstNonEmptyApprovalReason(result.Reason, "command approval denied") + persistNote
		return eval, nil
	}
	eval.Decision = commandapproval.DecisionAllow
	eval.Source = commandapproval.MatchSourceInteractive
	eval.Reason = firstNonEmptyApprovalReason(result.Reason, "command approved by user") + persistNote
	return eval, nil
}

func (g *Guardian) commandApprovalProposal(req commandapproval.Request, analysis commandapproval.Analysis) *commandapproval.Proposal {
	return &commandapproval.Proposal{
		AgentID:        req.AgentID,
		AgentType:      req.AgentType,
		SessionID:      req.SessionID,
		DAGID:          req.DAGID,
		NodeID:         req.NodeID,
		TaskID:         req.TaskID,
		PipelineID:     req.PipelineID,
		ToolName:       req.ToolName,
		Command:        req.Command,
		WorkingDir:     req.WorkingDir,
		WorkspaceRoot:  req.WorkspaceRoot,
		TemplateKey:    analysis.TemplateKey,
		PersistKey:     analysis.PersistKey,
		PersistLabel:   analysis.PersistLabel,
		RuleLabel:      analysis.RuleLabel,
		Summary:        analysis.Summary,
		Risk:           analysis.Risk,
		Timestamp:      time.Now(),
		ApprovalPolicy: analysis.ApprovalPolicy,
	}
}

func (g *Guardian) requestCommandApproval(ctx context.Context, proposal *commandapproval.Proposal) (ApprovalResult, error) {
	if g == nil || g.bus == nil {
		return ApprovalResult{}, fmt.Errorf("guardian bus is unavailable for command approval")
	}
	if proposal == nil {
		return ApprovalResult{}, fmt.Errorf("command approval proposal is required")
	}
	correlationID := uuid.New().String()
	proposal.CorrelationID = correlationID
	proposal.TargetAgentID = g.id
	holdID, err := g.beginCommandApprovalHold(ctx, proposal)
	if err != nil {
		return ApprovalResult{}, err
	}
	if holdID != "" {
		defer func() {
			resolveCtx, cancel := context.WithTimeout(context.Background(), commandApprovalControlTimeout)
			defer cancel()
			if err := g.resolveCommandApprovalHold(resolveCtx, proposal, holdID); err != nil && g.logger != nil {
				g.logger.Warn("command approval hold resolve failed",
					"hold_id", holdID,
					"session_id", proposal.SessionID,
					"dag_id", proposal.DAGID,
					"error", err)
			}
		}()
	}

	ch := make(chan ApprovalResult, 1)
	g.pendingMu.Lock()
	g.pendingApprovals[correlationID] = ch
	g.pendingMu.Unlock()
	defer func() {
		g.pendingMu.Lock()
		delete(g.pendingApprovals, correlationID)
		g.pendingMu.Unlock()
	}()

	msg := &guide.Message{
		ID:            uuid.New().String(),
		CorrelationID: correlationID,
		Type:          guide.MessageTypeProposal,
		SourceAgentID: g.id,
		Payload:       proposal,
		Timestamp:     time.Now(),
	}
	if err := g.bus.Publish(guide.TopicResponses("tui", "tui"), msg); err != nil {
		return ApprovalResult{}, fmt.Errorf("publish command approval proposal: %w", err)
	}

	var result ApprovalResult
	select {
	case result = <-ch:
		return result, nil
	case <-ctx.Done():
		return ApprovalResult{}, ctx.Err()
	}
}

func firstNonEmptyApprovalReason(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmptyApprovalValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (g *Guardian) beginCommandApprovalHold(ctx context.Context, proposal *commandapproval.Proposal) (string, error) {
	if proposal == nil {
		return "", nil
	}
	sessionID := strings.TrimSpace(proposal.SessionID)
	dagID := strings.TrimSpace(proposal.DAGID)
	if sessionID == "" || dagID == "" {
		return "", nil
	}
	req := &shared.CommandApprovalHoldRequest{
		Action:              shared.CommandApprovalHoldBegin,
		HoldID:              "approval_hold_" + uuid.NewString(),
		SessionID:           sessionID,
		DAGID:               dagID,
		NodeID:              strings.TrimSpace(proposal.NodeID),
		TaskID:              strings.TrimSpace(proposal.TaskID),
		PipelineID:          firstNonEmptyApprovalValue(strings.TrimSpace(proposal.PipelineID), strings.TrimSpace(proposal.TaskID)),
		SourceAgentID:       strings.TrimSpace(proposal.AgentID),
		SourceAgentType:     strings.TrimSpace(proposal.AgentType),
		SourceAgentName:     strings.TrimSpace(proposal.AgentType),
		ApprovalCorrelation: strings.TrimSpace(proposal.CorrelationID),
		ToolName:            strings.TrimSpace(proposal.ToolName),
		Command:             strings.TrimSpace(proposal.Command),
		RequestedAt:         time.Now().UTC(),
	}
	result, err := g.requestCommandApprovalHold(ctx, req)
	if err != nil {
		return "", err
	}
	return firstNonEmptyApprovalValue(strings.TrimSpace(result.HoldID), req.HoldID), nil
}

func (g *Guardian) resolveCommandApprovalHold(ctx context.Context, proposal *commandapproval.Proposal, holdID string) error {
	if proposal == nil || strings.TrimSpace(holdID) == "" {
		return nil
	}
	sessionID := strings.TrimSpace(proposal.SessionID)
	dagID := strings.TrimSpace(proposal.DAGID)
	if sessionID == "" || dagID == "" {
		return nil
	}
	_, err := g.requestCommandApprovalHold(ctx, &shared.CommandApprovalHoldRequest{
		Action:              shared.CommandApprovalHoldResolve,
		HoldID:              strings.TrimSpace(holdID),
		SessionID:           sessionID,
		DAGID:               dagID,
		NodeID:              strings.TrimSpace(proposal.NodeID),
		TaskID:              strings.TrimSpace(proposal.TaskID),
		PipelineID:          firstNonEmptyApprovalValue(strings.TrimSpace(proposal.PipelineID), strings.TrimSpace(proposal.TaskID)),
		SourceAgentID:       strings.TrimSpace(proposal.AgentID),
		SourceAgentType:     strings.TrimSpace(proposal.AgentType),
		SourceAgentName:     strings.TrimSpace(proposal.AgentType),
		ApprovalCorrelation: strings.TrimSpace(proposal.CorrelationID),
		ToolName:            strings.TrimSpace(proposal.ToolName),
		Command:             strings.TrimSpace(proposal.Command),
		RequestedAt:         time.Now().UTC(),
	})
	return err
}

func (g *Guardian) requestCommandApprovalHold(ctx context.Context, req *shared.CommandApprovalHoldRequest) (*shared.CommandApprovalHoldResult, error) {
	if g == nil || g.bus == nil {
		return nil, fmt.Errorf("guardian bus is unavailable for command approval hold")
	}
	if req == nil {
		return nil, fmt.Errorf("command approval hold request is required")
	}
	correlationID := "cmd_approval_hold_" + uuid.NewString()
	responseTopic := guide.TopicResponses("guardian", g.id)
	waitCh := make(chan *guide.Message, 1)
	sub, err := g.bus.SubscribeAsync(responseTopic, func(msg *guide.Message) error {
		if !isCommandApprovalHoldTerminalMessage(msg, correlationID) {
			return nil
		}
		select {
		case waitCh <- msg:
		default:
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe command approval hold response: %w", err)
	}
	defer sub.Unsubscribe()

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode command approval hold request: %w", err)
	}
	routeReq := &guide.RouteRequest{
		CorrelationID:   correlationID,
		Input:           string(payload),
		SourceAgentID:   g.id,
		SourceAgentName: "guardian",
		TargetAgentID:   "orchestrator",
		ExplicitTarget:  true,
		SessionID:       strings.TrimSpace(req.SessionID),
		Timestamp:       time.Now(),
		Metadata: map[string]any{
			"control_plane_kind": shared.ControlPlaneKindCommandApprovalHold,
		},
	}
	if err := g.bus.Publish(guide.TopicGuideRequests, guide.NewRequestMessage("", routeReq)); err != nil {
		return nil, fmt.Errorf("publish command approval hold request: %w", err)
	}

	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(waitCtx, commandApprovalControlTimeout)
	defer cancel()

	select {
	case msg := <-waitCh:
		return decodeCommandApprovalHoldMessage(msg)
	case <-waitCtx.Done():
		return nil, fmt.Errorf("command approval hold request timed out: %w", waitCtx.Err())
	}
}

func isCommandApprovalHoldTerminalMessage(msg *guide.Message, correlationID string) bool {
	if msg == nil || strings.TrimSpace(msg.CorrelationID) != correlationID {
		return false
	}
	if _, ok := msg.GetRouteResponse(); ok {
		return true
	}
	_, ok := msg.GetError()
	return ok
}

func decodeCommandApprovalHoldMessage(msg *guide.Message) (*shared.CommandApprovalHoldResult, error) {
	if msg == nil {
		return nil, fmt.Errorf("command approval hold response is missing")
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		if !resp.Success {
			return nil, fmt.Errorf("%s", strings.TrimSpace(resp.Error))
		}
		return decodeCommandApprovalHoldResult(resp.Data)
	}
	if errText, ok := msg.GetError(); ok {
		return nil, fmt.Errorf("%s", strings.TrimSpace(errText))
	}
	return nil, fmt.Errorf("unsupported command approval hold response payload")
}

func decodeCommandApprovalHoldResult(data any) (*shared.CommandApprovalHoldResult, error) {
	switch typed := data.(type) {
	case *shared.CommandApprovalHoldResult:
		return typed, nil
	case shared.CommandApprovalHoldResult:
		copy := typed
		return &copy, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal command approval hold response: %w", err)
	}
	var result shared.CommandApprovalHoldResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode command approval hold response: %w", err)
	}
	return &result, nil
}
