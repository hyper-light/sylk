package container

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// ContainerAwareHook is optionally implemented by HookHandler implementations
// that need access to the owning container at execution time. The HookRunner
// calls BindContainer before Execute, providing the real agent ID, type, and
// container reference — avoiding the spec-time vs runtime ID mismatch.
type ContainerAwareHook interface {
	BindContainer(c *Container)
}

// HookRunner executes lifecycle hooks for a container.
// All hook goroutines are tracked via GoroutineScope.
type HookRunner struct {
	scope     *concurrency.GoroutineScope
	spec      LifecycleHookSpec
	container *Container
}

// NewHookRunner creates a hook runner backed by the given scope.
func NewHookRunner(scope *concurrency.GoroutineScope, spec LifecycleHookSpec) *HookRunner {
	return &HookRunner{
		scope: scope,
		spec:  spec,
	}
}

// SetContainer associates the hook runner with its owning container.
// Must be called after NewContainer but before Start. Hooks that
// implement ContainerAwareHook will receive this reference.
func (hr *HookRunner) SetContainer(c *Container) {
	hr.container = c
}

// RunPreStart executes the pre-start hook synchronously. Returns error
// if the hook fails — the caller should abort container start.
func (hr *HookRunner) RunPreStart(ctx context.Context) error {
	return hr.runHookSync(ctx, HookPreStart, hr.spec.PreStart)
}

// RunPostStart executes the post-start hook asynchronously via GoroutineScope.
// Post-start failure does not prevent the container from running.
func (hr *HookRunner) RunPostStart() error {
	return hr.runHookAsync(HookPostStart, hr.spec.PostStart)
}

// RunPreStop executes the pre-stop hook synchronously. Should run during
// graceful shutdown before the agent is terminated.
func (hr *HookRunner) RunPreStop(ctx context.Context) error {
	return hr.runHookSync(ctx, HookPreStop, hr.spec.PreStop)
}

// RunPostStop executes the post-stop hook synchronously. Always runs
// after the container has stopped, even on failure.
func (hr *HookRunner) RunPostStop(ctx context.Context) error {
	return hr.runHookSync(ctx, HookPostStop, hr.spec.PostStop)
}

func (hr *HookRunner) runHookSync(ctx context.Context, _ HookPhase, action HookAction) error {
	if action.Handler == nil {
		return nil
	}
	hr.bindContainer(action.Handler)
	timeout := hr.resolveTimeout(action)
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return action.Handler.Execute(hookCtx)
}

func (hr *HookRunner) runHookAsync(phase HookPhase, action HookAction) error {
	if action.Handler == nil {
		return nil
	}
	hr.bindContainer(action.Handler)
	timeout := hr.resolveTimeout(action)
	return hr.scope.Go(
		fmt.Sprintf("hook-%s", hookPhaseNames[phase]),
		timeout,
		func(ctx context.Context) error {
			return action.Handler.Execute(ctx)
		},
	)
}

// bindContainer injects the container reference into hooks that implement
// ContainerAwareHook. Called before every hook execution.
func (hr *HookRunner) bindContainer(handler HookHandler) {
	if hr.container == nil {
		return
	}
	if aware, ok := handler.(ContainerAwareHook); ok {
		aware.BindContainer(hr.container)
	}
}

func (hr *HookRunner) resolveTimeout(action HookAction) time.Duration {
	if action.Timeout > 0 {
		return action.Timeout
	}
	return defaultHookTimeout
}

const defaultHookTimeout = 30 * time.Second

// hookPhaseNames maps hook phases to string names for goroutine descriptions.
var hookPhaseNames = map[HookPhase]string{
	HookPreStart:  "pre-start",
	HookPostStart: "post-start",
	HookPreStop:   "pre-stop",
	HookPostStop:  "post-stop",
}
