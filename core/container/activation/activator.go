package activation

import "context"

// ControllerActivator adapts ActivationController to the
// guide.AgentActivator interface, discarding the *Container return
// value from EnsureActive.
type ControllerActivator struct {
	ctrl *ActivationController
}

// NewControllerActivator creates an activator backed by the given controller.
func NewControllerActivator(ctrl *ActivationController) *ControllerActivator {
	return &ControllerActivator{ctrl: ctrl}
}

// EnsureActive guarantees the agent container is in TierHot.
func (a *ControllerActivator) EnsureActive(ctx context.Context, agentType string) error {
	_, err := a.ctrl.EnsureActive(ctx, agentType)
	return err
}

// TouchActivity resets the idle timer for the given agent type.
func (a *ControllerActivator) TouchActivity(agentType string) {
	a.ctrl.TouchActivity(agentType)
}
