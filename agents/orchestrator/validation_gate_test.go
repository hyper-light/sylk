package orchestrator

import (
	"context"
	"testing"
	"time"
)

func TestDispatchHoldGateBlocksUntilResolved(t *testing.T) {
	gate := newDispatchHoldGate()
	gate.activate("sess")

	done := make(chan error, 1)
	go func() {
		done <- gate.wait(context.Background(), "sess", "dag-1")
	}()

	select {
	case err := <-done:
		t.Fatalf("wait returned too early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	gate.resolve("sess")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not unblock after resolve")
	}
}

func TestDispatchHoldGateAllowsWhitelistedDAG(t *testing.T) {
	gate := newDispatchHoldGate()
	gate.activate("sess")
	gate.allowDAG("sess", "dag-remediation")

	if err := gate.wait(context.Background(), "sess", "dag-remediation"); err != nil {
		t.Fatalf("wait allowed DAG: %v", err)
	}
}

func TestDispatchHoldGateBlocksHeldDAGOnly(t *testing.T) {
	gate := newDispatchHoldGate()
	gate.activateDAG("sess", "dag-1", "hold-1")

	if err := gate.wait(context.Background(), "sess", "dag-2"); err != nil {
		t.Fatalf("wait on unrelated dag returned error: %v", err)
	}
	if !gate.isHeld("sess", "dag-1") {
		t.Fatal("expected dag-specific hold to be active")
	}

	done := make(chan error, 1)
	go func() {
		done <- gate.wait(context.Background(), "sess", "dag-1")
	}()

	select {
	case err := <-done:
		t.Fatalf("wait returned too early for held dag: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	gate.resolveHold("sess", "hold-1")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait returned error after hold resolve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not unblock after dag hold resolve")
	}
}
