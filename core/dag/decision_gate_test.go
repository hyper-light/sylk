package dag_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecisionGate_NoFailures(t *testing.T) {
	d := buildLinearDAG(t)
	gate := dag.NewDecisionGate(d, nil, nil)
	gateFunc := gate.Gate()

	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateSucceeded},
	}

	err := gateFunc(context.Background(), d.ID(), 0, results)
	assert.NoError(t, err)
}

func TestDecisionGate_NonBlocking(t *testing.T) {
	d := buildForkDAG(t)

	var notified []dag.FailedNodeSummary
	var mu sync.Mutex
	onNonBlocking := func(_ string, failures []dag.FailedNodeSummary) {
		mu.Lock()
		notified = failures
		mu.Unlock()
	}

	gate := dag.NewDecisionGate(d, onNonBlocking, nil)
	gateFunc := gate.Gate()

	// A succeeded, B failed — but C is still viable after layer 0
	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateSucceeded},
		"B": {NodeID: "B", State: dag.NodeStateFailed, Error: errors.New("oops")},
	}

	err := gateFunc(context.Background(), d.ID(), 0, results)
	assert.NoError(t, err)

	mu.Lock()
	assert.Len(t, notified, 1)
	assert.Equal(t, "B", notified[0].NodeID)
	mu.Unlock()
}

func TestDecisionGate_BlockingRetry(t *testing.T) {
	d := buildLinearDAG(t)

	var reqReceived dag.LayerDecisionRequest
	onDecisionReq := func(req dag.LayerDecisionRequest) {
		reqReceived = req
	}

	gate := dag.NewDecisionGate(d, nil, onDecisionReq)
	gateFunc := gate.Gate()

	// A failed → B and C blocked
	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateFailed, Error: errors.New("build failed")},
	}

	var gateErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		gateErr = gateFunc(context.Background(), d.ID(), 0, results)
	}()

	// Wait for the decision request callback
	require.Eventually(t, func() bool {
		return reqReceived.DAGID != ""
	}, time.Second, 10*time.Millisecond)

	assert.Equal(t, dag.FailureClassBlocking, reqReceived.Class)
	assert.Len(t, reqReceived.FailedNodes, 1)

	// Resolve with retry
	gate.ResolveDecision(d.ID(), dag.DecisionRetry)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("gate did not unblock after resolve")
	}

	assert.ErrorIs(t, gateErr, dag.ErrLayerRetry)
}

func TestDecisionGate_BlockingSkip(t *testing.T) {
	d := buildLinearDAG(t)

	gate := dag.NewDecisionGate(d, nil, func(_ dag.LayerDecisionRequest) {})
	gateFunc := gate.Gate()

	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateFailed},
	}

	done := make(chan error, 1)
	go func() {
		done <- gateFunc(context.Background(), d.ID(), 0, results)
	}()

	// Wait for pending
	time.Sleep(50 * time.Millisecond)
	gate.ResolveDecision(d.ID(), dag.DecisionSkip)

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("gate did not unblock")
	}
}

func TestDecisionGate_BlockingAbort(t *testing.T) {
	d := buildLinearDAG(t)

	gate := dag.NewDecisionGate(d, nil, func(_ dag.LayerDecisionRequest) {})
	gateFunc := gate.Gate()

	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateFailed},
	}

	done := make(chan error, 1)
	go func() {
		done <- gateFunc(context.Background(), d.ID(), 0, results)
	}()

	time.Sleep(50 * time.Millisecond)
	gate.ResolveDecision(d.ID(), dag.DecisionAbort)

	select {
	case err := <-done:
		assert.ErrorIs(t, err, dag.ErrDAGCancelled)
	case <-time.After(time.Second):
		t.Fatal("gate did not unblock")
	}
}

func TestDecisionGate_ContextCancellation(t *testing.T) {
	d := buildLinearDAG(t)

	gate := dag.NewDecisionGate(d, nil, func(_ dag.LayerDecisionRequest) {})
	gateFunc := gate.Gate()

	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateFailed},
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- gateFunc(ctx, d.ID(), 0, results)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("gate did not unblock on context cancel")
	}
}
