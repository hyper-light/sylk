package identity

import (
	"context"
	"errors"
)

// Context keys. Private to the package — callers must use the
// typed accessors below.
type identityCtxKey struct{}
type taskCtxKey struct{}

// Errors surfaced by RequireIdentity / RequireTask.
var (
	ErrNoIdentityInContext = errors.New("identity: no AgentIdentity on ctx")
	ErrNoTaskInContext     = errors.New("identity: no TaskRef on ctx")
)

// WithIdentity returns a context carrying id. Panics on nil id —
// the propagation contract requires non-nil at every dispatch
// boundary.
func WithIdentity(ctx context.Context, id *AgentIdentity) context.Context {
	if id == nil {
		panic("identity.WithIdentity: nil identity")
	}
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// IdentityFromContext returns the identity, or (nil, false) when
// absent. Intended for non-critical observers (logging, metrics)
// where missing identity is recoverable.
func IdentityFromContext(ctx context.Context) (*AgentIdentity, bool) {
	if ctx == nil {
		return nil, false
	}
	id, ok := ctx.Value(identityCtxKey{}).(*AgentIdentity)
	return id, ok && id != nil
}

// RequireIdentity returns the identity or an error. Dispatch paths
// use this — missing identity aborts the call loudly. Replaces the
// silent-success behavior of the old stampRequestIdentity.
func RequireIdentity(ctx context.Context) (*AgentIdentity, error) {
	id, ok := IdentityFromContext(ctx)
	if !ok {
		return nil, ErrNoIdentityInContext
	}
	return id, nil
}

// WithTask returns a context carrying task. Panics on nil task.
func WithTask(ctx context.Context, task *TaskRef) context.Context {
	if task == nil {
		panic("identity.WithTask: nil task")
	}
	return context.WithValue(ctx, taskCtxKey{}, task)
}

// TaskFromContext returns the task, or (nil, false) when absent.
func TaskFromContext(ctx context.Context) (*TaskRef, bool) {
	if ctx == nil {
		return nil, false
	}
	t, ok := ctx.Value(taskCtxKey{}).(*TaskRef)
	return t, ok && t != nil
}

// RequireTask returns the task or an error. Dispatch paths use this;
// CategorySystem requests are expected to carry a SystemTaskUID
// sentinel rather than omit the task entirely.
func RequireTask(ctx context.Context) (*TaskRef, error) {
	t, ok := TaskFromContext(ctx)
	if !ok {
		return nil, ErrNoTaskInContext
	}
	return t, nil
}

// RequireDispatch returns both identity and task, failing fast if
// either is missing. This is the canonical pre-LLM-call check.
func RequireDispatch(ctx context.Context) (*AgentIdentity, *TaskRef, error) {
	id, err := RequireIdentity(ctx)
	if err != nil {
		return nil, nil, err
	}
	task, err := RequireTask(ctx)
	if err != nil {
		return nil, nil, err
	}
	return id, task, nil
}
