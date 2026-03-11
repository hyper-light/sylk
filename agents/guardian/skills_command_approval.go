package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

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
		return eval, fmt.Errorf("%s", strings.TrimSpace(eval.Reason))
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
		return eval, fmt.Errorf("%s", eval.Reason)
	}
	eval.Decision = commandapproval.DecisionAllow
	eval.Source = commandapproval.MatchSourceInteractive
	eval.Reason = firstNonEmptyApprovalReason(result.Reason, "command approved by user") + persistNote
	return eval, nil
}

func (g *Guardian) commandApprovalProposal(req commandapproval.Request, analysis commandapproval.Analysis) *commandapproval.Proposal {
	return &commandapproval.Proposal{
		AgentID:       req.AgentID,
		AgentType:     req.AgentType,
		ToolName:      req.ToolName,
		Command:       req.Command,
		WorkingDir:    req.WorkingDir,
		WorkspaceRoot: req.WorkspaceRoot,
		TemplateKey:   analysis.TemplateKey,
		PersistKey:    analysis.PersistKey,
		PersistLabel:  analysis.PersistLabel,
		RuleLabel:     analysis.RuleLabel,
		Summary:       analysis.Summary,
		Risk:          analysis.Risk,
		Timestamp:     time.Now(),
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

	timer := time.NewTimer(DefaultApprovalTTL)
	defer timer.Stop()

	select {
	case result := <-ch:
		return result, nil
	case <-timer.C:
		return ApprovalResult{}, fmt.Errorf("command approval timed out after %v", DefaultApprovalTTL)
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
