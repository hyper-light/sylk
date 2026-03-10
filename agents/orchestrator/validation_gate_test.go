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
