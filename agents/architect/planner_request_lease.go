package architect

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
)

const (
	// defaultPlannerRequestRefreshes bounds request replay when the parent
	// context has no deadline. This keeps single planner turns bounded while
	// still allowing long streams to survive transient child-deadline expiry.
	defaultPlannerRequestRefreshes = 3

	// maxPlannerRequestRefreshesCap prevents a single planner request from
	// consuming an unbounded number of full replays even when the parent
	// operation has a very large deadline budget.
	maxPlannerRequestRefreshesCap = 8
)

type plannerRequestLease struct {
	parent    context.Context
	planner   *anthropicPlanner
	stage     string
	refreshes int
}

func newPlannerRequestLease(parent context.Context, planner *anthropicPlanner, stage string) *plannerRequestLease {
	return &plannerRequestLease{
		parent:  parent,
		planner: planner,
		stage:   stage,
	}
}

func (l *plannerRequestLease) run(
	localTimeout time.Duration,
	emitRetryReset bool,
	run func(context.Context, time.Duration) error,
) error {
	return shared.RunWithContextLease(l.parent, shared.ContextLeaseConfig{
		AttemptTimeout: localTimeout,
		MaxRefreshes:   l.maxRefreshes(localTimeout),
		DeadlineGuard:  contextDeadlineGuard,
		AttemptContext: func(parent context.Context, _ time.Duration) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		},
		OnRefresh: func(info shared.ContextLeaseRefresh) {
			l.refreshes = info.RefreshCount
			l.planner.logRequestLeaseRefresh(l.parent, localTimeout, l.stage, l.refreshes, info.Error)
			if emitRetryReset {
				emitStreamRetryReset(l.parent)
			}
		},
	}, func(ctx context.Context) error {
		return run(ctx, localTimeout)
	})
}

func (l *plannerRequestLease) maxRefreshes(localTimeout time.Duration) int {
	if localTimeout <= 0 {
		return defaultPlannerRequestRefreshes
	}
	if l == nil || l.parent == nil {
		return defaultPlannerRequestRefreshes
	}
	deadline, ok := l.parent.Deadline()
	if !ok {
		return defaultPlannerRequestRefreshes
	}

	remaining := time.Until(deadline) - contextDeadlineGuard
	if remaining <= 0 {
		return 0
	}

	attempts := int(remaining / localTimeout)
	if attempts <= 1 {
		return 0
	}

	refreshes := attempts - 1
	if refreshes > maxPlannerRequestRefreshesCap {
		return maxPlannerRequestRefreshesCap
	}
	return refreshes
}
