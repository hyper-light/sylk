package recovery

import (
	"fmt"
	"log/slog"
)

type ResourceAcquirer interface {
	Acquire(agentID, resourceID string) error
}

type ResourceReacquisition struct {
	resourceMgr ResourceAcquirer
	notifier    RecoveryNotifier
	logger      *slog.Logger
}

func NewResourceReacquisition(
	resourceMgr ResourceAcquirer,
	notifier RecoveryNotifier,
	logger *slog.Logger,
) *ResourceReacquisition {
	return &ResourceReacquisition{
		resourceMgr: resourceMgr,
		notifier:    notifier,
		logger:      logger,
	}
}

func (rr *ResourceReacquisition) ReacquireResources(agentID string, resources []string) error {
	failed := rr.attemptReacquisitions(agentID, resources)

	if len(failed) > 0 {
		return fmt.Errorf("failed to re-acquire %d resources: %v", len(failed), failed)
	}

	rr.logger.Info("agent re-acquired all resources",
		"agent", agentID,
		"count", len(resources),
	)
	return nil
}

func (rr *ResourceReacquisition) attemptReacquisitions(agentID string, resources []string) []string {
	var failed []string

	for _, resourceID := range resources {
		if err := rr.resourceMgr.Acquire(agentID, resourceID); err != nil {
			failed = append(failed, resourceID)
			rr.logger.Warn("failed to re-acquire resource",
				"agent", agentID,
				"resource", resourceID,
				"error", err,
			)
		}
	}
	return failed
}

func (rr *ResourceReacquisition) NotifyAndReacquire(agentID string, resources []string) error {
	rr.notifier.NotifyReacquireResources(agentID, resources)
	return rr.ReacquireResources(agentID, resources)
}
