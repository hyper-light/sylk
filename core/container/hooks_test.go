package container

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
)

type recordingHook struct {
	called atomic.Bool
	err    error
}

func (h *recordingHook) Execute(_ context.Context) error {
	h.called.Store(true)
	return h.err
}

func testHookScope(t *testing.T) (*concurrency.GoroutineScope, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	scope := concurrency.NewGoroutineScope(ctx, "hook-test", nil)
	return scope, cancel
}

func TestHookRunner_NilHandlerNoOp(t *testing.T) {
	scope, cancel := testHookScope(t)
	defer cancel()

	hr := NewHookRunner(scope, LifecycleHookSpec{})
	if err := hr.RunPreStart(context.Background()); err != nil {
		t.Fatalf("nil handler should be no-op, got %v", err)
	}
	if err := hr.RunPostStart(); err != nil {
		t.Fatalf("nil handler should be no-op, got %v", err)
	}
	if err := hr.RunPreStop(context.Background()); err != nil {
		t.Fatalf("nil handler should be no-op, got %v", err)
	}
	if err := hr.RunPostStop(context.Background()); err != nil {
		t.Fatalf("nil handler should be no-op, got %v", err)
	}
}

func TestHookRunner_PreStartExecutes(t *testing.T) {
	scope, cancel := testHookScope(t)
	defer cancel()

	hook := &recordingHook{}
	hr := NewHookRunner(scope, LifecycleHookSpec{
		PreStart: HookAction{Handler: hook},
	})

	if err := hr.RunPreStart(context.Background()); err != nil {
		t.Fatalf("RunPreStart failed: %v", err)
	}
	if !hook.called.Load() {
		t.Fatal("pre-start hook should have been called")
	}
}

func TestHookRunner_PreStartErrorPropagates(t *testing.T) {
	scope, cancel := testHookScope(t)
	defer cancel()

	hookErr := errors.New("pre-start failed")
	hook := &recordingHook{err: hookErr}
	hr := NewHookRunner(scope, LifecycleHookSpec{
		PreStart: HookAction{Handler: hook},
	})

	err := hr.RunPreStart(context.Background())
	if !errors.Is(err, hookErr) {
		t.Fatalf("expected %v, got %v", hookErr, err)
	}
}

func TestHookRunner_PostStopExecutes(t *testing.T) {
	scope, cancel := testHookScope(t)
	defer cancel()

	hook := &recordingHook{}
	hr := NewHookRunner(scope, LifecycleHookSpec{
		PostStop: HookAction{Handler: hook},
	})

	if err := hr.RunPostStop(context.Background()); err != nil {
		t.Fatalf("RunPostStop failed: %v", err)
	}
	if !hook.called.Load() {
		t.Fatal("post-stop hook should have been called")
	}
}

func TestHookRunner_PreStopExecutes(t *testing.T) {
	scope, cancel := testHookScope(t)
	defer cancel()

	hook := &recordingHook{}
	hr := NewHookRunner(scope, LifecycleHookSpec{
		PreStop: HookAction{Handler: hook},
	})

	if err := hr.RunPreStop(context.Background()); err != nil {
		t.Fatalf("RunPreStop failed: %v", err)
	}
	if !hook.called.Load() {
		t.Fatal("pre-stop hook should have been called")
	}
}
