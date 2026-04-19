package commandapproval

import (
	"context"
	"errors"
	"fmt"

	"github.com/adalundhe/sylk/core/activity"
)

var ErrApprovalRequired = errors.New("command approval required")
var ErrApprovalDenied = errors.New("command approval denied")

type gateContextKey struct{}

func WithGate(ctx context.Context, gate Gate) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, gateContextKey{}, gate)
}

func GateFromContext(ctx context.Context) Gate {
	if ctx == nil {
		return nil
	}
	gate, _ := ctx.Value(gateContextKey{}).(Gate)
	return gate
}

// Authorize evaluates a command-approval request and emits the
// resulting decision into the Activity Fabric. Activity Fabric
// chokepoint: every guardian decision auto-publishes
// command_approved or command_denied (Fine resolution) carrying
// rationale + scope. Peers about to run similar commands see denials
// proactively in their ambient context envelope and can adapt without
// re-tripping the same guardian rule.
func Authorize(ctx context.Context, evaluator *Evaluator, req Request) (Evaluation, error) {
	eval, err := authorizeInner(ctx, evaluator, req)
	emitGuardianDecision(ctx, req, eval, err)
	return eval, err
}

func authorizeInner(ctx context.Context, evaluator *Evaluator, req Request) (Evaluation, error) {
	if gate := GateFromContext(ctx); gate != nil {
		return gate.Authorize(ctx, req)
	}
	if evaluator == nil {
		evaluator = NewEvaluator(nil)
	}
	eval, err := evaluator.Evaluate(req)
	if err != nil {
		return Evaluation{}, err
	}
	if eval.Decision == DecisionPrompt {
		return eval, fmt.Errorf("%w: %s", ErrApprovalRequired, eval.Analysis.Summary)
	}
	if eval.Decision == DecisionDeny {
		return eval, fmt.Errorf("%w: %s", ErrApprovalDenied, eval.Reason)
	}
	return eval, nil
}

func emitGuardianDecision(ctx context.Context, req Request, eval Evaluation, authErr error) {
	// Determine the kind from the resolved decision when available;
	// fall back to denied on infra failure (the operation didn't
	// proceed regardless of why).
	kind := activity.ActionCommandApproved
	if eval.Decision == DecisionDeny || (authErr != nil && !errors.Is(authErr, ErrApprovalRequired)) {
		kind = activity.ActionCommandDenied
	}
	span := activity.StartSpan(ctx, kind, activity.Subject{
		PathPrefix:     req.WorkspaceRoot,
		TargetArtifact: req.ToolName,
	})
	span.SetAttribute("command", req.Command)
	span.SetAttribute("working_dir", req.WorkingDir)
	span.SetAttribute("agent_id", req.AgentID)
	span.SetAttribute("agent_type", req.AgentType)
	span.SetAttribute("decision", string(eval.Decision))
	span.SetAttribute("source", string(eval.Source))
	if eval.Reason != "" {
		span.SetAttribute("reason", eval.Reason)
	}
	if authErr != nil {
		// Approval-required is a non-error from the fabric's
		// perspective — it's a structured outcome of the gate. Tag
		// the activity but don't mark it as a span error.
		if errors.Is(authErr, ErrApprovalRequired) {
			span.SetAttribute("requires_prompt", true)
			span.End()
			return
		}
		span.EndWithError(authErr)
		return
	}
	span.End()
}
