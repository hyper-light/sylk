package commandapproval

import (
	"context"
	"errors"
	"fmt"
)

var ErrApprovalRequired = errors.New("command approval required")

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

func Authorize(ctx context.Context, evaluator *Evaluator, req Request) (Evaluation, error) {
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
		return eval, fmt.Errorf("%s", eval.Reason)
	}
	return eval, nil
}
