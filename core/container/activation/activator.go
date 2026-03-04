package activation

import (
	"context"
	"sync"
)

// ControllerPodActivator adapts ActivationController to the
// guide.PodActivator interface. It maps agent types to pod IDs
// and delegates activation to the controller using pod IDs as keys.
type ControllerPodActivator struct {
	ctrl       *ActivationController
	agentToPod sync.Map // agentType → podID
}

// NewControllerPodActivator creates a pod activator backed by the given controller.
func NewControllerPodActivator(ctrl *ActivationController) *ControllerPodActivator {
	return &ControllerPodActivator{ctrl: ctrl}
}

// EnsurePodActive guarantees the pod is in TierHot.
func (a *ControllerPodActivator) EnsurePodActive(ctx context.Context, podID string) error {
	_, err := a.ctrl.EnsureActive(ctx, podID)
	return err
}

// TouchPodActivity resets the idle timer for the given pod.
func (a *ControllerPodActivator) TouchPodActivity(podID string) {
	a.ctrl.TouchActivity(podID)
}

// HoldPodActive activates the pod and acquires a request guard that prevents
// idle demotion while held. Returns an idempotent release function.
func (a *ControllerPodActivator) HoldPodActive(ctx context.Context, podID string) (func(), error) {
	if _, err := a.ctrl.EnsureActive(ctx, podID); err != nil {
		return func() {}, err
	}
	return a.ctrl.AcquireRequestGuard(podID), nil
}

// PodForAgent resolves an agent type to its owning pod ID.
// Returns the agentType itself for singleton pods (no mapping registered).
func (a *ControllerPodActivator) PodForAgent(agentType string) string {
	if v, ok := a.agentToPod.Load(agentType); ok {
		return v.(string)
	}
	return agentType
}

// RegisterMapping associates an agent type with a pod ID.
func (a *ControllerPodActivator) RegisterMapping(agentType, podID string) {
	a.agentToPod.Store(agentType, podID)
}

// NewControllerActivator is the legacy constructor. Deprecated: use NewControllerPodActivator.
func NewControllerActivator(ctrl *ActivationController) *ControllerPodActivator {
	return NewControllerPodActivator(ctrl)
}
