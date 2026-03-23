package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/google/uuid"
)

func TestActivateAgents_WithPod(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, pod)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
	})

	if err := d.activateAgents(context.Background(), node); err != nil {
		t.Fatalf("activateAgents: %v", err)
	}

	if got := act.holdCount(); got != 1 {
		t.Errorf("expected 1 HoldPodActive call, got %d", got)
	}
	if got := reg.callCount(); got != len(PipelineAgentTypes) {
		t.Errorf("expected %d registrar calls, got %d", len(PipelineAgentTypes), got)
	}
	if got := pod.ActiveGuardCount(); got != 1 {
		t.Errorf("expected 1 active guard in pod, got %d", got)
	}
}

func TestActivateAgents_WithPod_CoAgents(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, pod)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
		CoAgents:  []string{"designer", "inspector-pipeline"},
	})

	if err := d.activateAgents(context.Background(), node); err != nil {
		t.Fatalf("activateAgents: %v", err)
	}

	// engineer + designer + inspector-pipeline = 3
	if got := act.holdCount(); got != 3 {
		t.Errorf("expected 3 HoldPodActive calls, got %d: %v", got, act.calledTypes())
	}
	if got := reg.callCount(); got != len(PipelineAgentTypes) {
		t.Errorf("expected %d registrar calls, got %d", len(PipelineAgentTypes), got)
	}
}

func TestActivateAgents_WithPod_GuardReleasedOnNodeComplete(t *testing.T) {
	act := &trackingActivator{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
	})

	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, pod)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
	})

	if err := d.activateAgents(context.Background(), node); err != nil {
		t.Fatalf("activateAgents: %v", err)
	}

	if got := pod.ActiveGuardCount(); got != 1 {
		t.Fatalf("expected 1 active guard, got %d", got)
	}

	// OnNodeComplete releases the guard (even without a pending dispatch
	// channel — logs a warning but still releases).
	d.OnNodeComplete("n1", &dag.NodeResult{State: dag.NodeStateSucceeded})

	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 release from OnNodeComplete, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 0 {
		t.Errorf("expected 0 active guards after OnNodeComplete, got %d", got)
	}
}

func TestActivateAgents_WithPod_FailureIsolation(t *testing.T) {
	act := &trackingActivator{failOn: "designer"}
	reg := &trackingRegistrar{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, pod)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
		CoAgents:  []string{"designer"},
	})

	err := d.activateAgents(context.Background(), node)
	if err == nil {
		t.Fatal("expected error from failed activation")
	}

	// engineer guard was acquired then released during rollback.
	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 rollback release, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 0 {
		t.Errorf("expected 0 active guards after failed activation, got %d", got)
	}
}

func TestReleaseAllGuards_DelegatesToPod(t *testing.T) {
	act := &trackingActivator{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
	})

	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, pod)

	for _, id := range []string{"n1", "n2", "n3"} {
		node := dag.NewNode(dag.NodeConfig{
			ID:        id,
			AgentType: "engineer",
			Prompt:    "task",
		})
		if err := d.activateAgents(context.Background(), node); err != nil {
			t.Fatalf("activateAgents(%s): %v", id, err)
		}
	}

	d.ReleaseAllGuards()

	if got := act.releaseCount(); got != 3 {
		t.Errorf("expected 3 releases from ReleaseAllGuards, got %d", got)
	}

	// Idempotent.
	d.ReleaseAllGuards()
	if got := act.releaseCount(); got != 3 {
		t.Errorf("expected still 3 releases after second ReleaseAllGuards, got %d", got)
	}
}

func TestActivateAgents_FallbackWithoutPod(t *testing.T) {
	act := &trackingActivator{}

	// No pod — falls back to activator.EnsurePodActive.
	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, nil)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
		CoAgents:  []string{"designer"},
	})

	if err := d.activateAgents(context.Background(), node); err != nil {
		t.Fatalf("activateAgents with nil pod: %v", err)
	}

	// Fallback uses EnsurePodActive, not HoldPodActive.
	if got := act.holdCount(); got != 0 {
		t.Errorf("expected 0 HoldPodActive calls (fallback uses EnsurePodActive), got %d", got)
	}
}

func TestActivateAgents_NilPodNilActivator(t *testing.T) {
	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, nil, nil)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
	})

	if err := d.activateAgents(context.Background(), node); err != nil {
		t.Fatalf("expected double-nil to be a no-op, got: %v", err)
	}
}

func TestReleaseGuard_NilPod(t *testing.T) {
	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, nil, nil)
	// Should not panic.
	d.ReleaseGuard("n1")
	d.ReleaseAllGuards()
}

func TestBuildDispatchMessage_UsesStableTaskIdentityFromContext(t *testing.T) {
	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, nil, nil)
	node := dag.NewNode(dag.NodeConfig{
		ID:        "task_1:inspect",
		AgentType: "inspector-pipeline",
		Prompt:    "inspect",
		Context: map[string]any{
			"task_id":   "task_1",
			"task_slug": "hello-cli",
		},
	})

	msg := d.buildDispatchMessage(node, nil, "ack.topic")
	payload, ok := msg.Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", msg.Payload)
	}

	if got, _ := payload["task_id"].(string); got != "task_1" {
		t.Fatalf("task_id = %q, want task_1", got)
	}
	if got, _ := payload["task_slug"].(string); got != "hello-cli" {
		t.Fatalf("task_slug = %q, want hello-cli", got)
	}
}

func TestBuildDispatchMessage_FallsBackToNodeIDWhenTaskIdentityMissing(t *testing.T) {
	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, nil, nil)
	node := dag.NewNode(dag.NodeConfig{
		ID:        "task_1:execute",
		AgentType: "engineer",
		Prompt:    "implement",
	})

	msg := d.buildDispatchMessage(node, nil, "ack.topic")
	payload, ok := msg.Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", msg.Payload)
	}

	if got, _ := payload["task_id"].(string); got != "task_1:execute" {
		t.Fatalf("task_id = %q, want task_1:execute", got)
	}
}

func TestBuildDispatchMessage_DoesNotInventUUIDTaskIDs(t *testing.T) {
	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, nil, nil)
	node := dag.NewNode(dag.NodeConfig{
		ID:        "task_1",
		AgentType: "engineer",
		Prompt:    "implement",
		Context: map[string]any{
			"task_id": "task_1",
		},
	})

	msg := d.buildDispatchMessage(node, nil, "ack.topic")
	payload, ok := msg.Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", msg.Payload)
	}

	if got, _ := payload["task_id"].(string); got == "" {
		t.Fatal("task_id should not be empty")
	} else if _, err := uuid.Parse(got); err == nil {
		t.Fatalf("task_id = %q, unexpectedly looks like a generated UUID", got)
	}
}

func TestAcknowledge_UnblocksDispatchWithoutAgentAckTopic(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	dispatcher := NewBusNodeDispatcher(bus, "orch", "sess", "dag-1", nil, nil, nil)
	node := dag.NewNode(dag.NodeConfig{
		ID:        "task_1:inspect",
		AgentType: "inspector-pipeline",
		Prompt:    "inspect",
	})

	resultCh := make(chan *dag.NodeResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := dispatcher.Dispatch(context.Background(), node, nil)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	deadline := time.After(2 * time.Second)
	for dispatcher.DispatchDone(node.ID()) == nil {
		select {
		case <-deadline:
			t.Fatal("dispatch did not start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if ok := dispatcher.Acknowledge(node.ID(), &ACKResult{
		AgentID:   "inspector-pipeline",
		AgentType: "inspector-pipeline",
		AckedAt:   time.Now(),
	}); !ok {
		t.Fatal("expected synthetic ACK to be accepted")
	}

	dispatcher.OnNodeComplete(node.ID(), &dag.NodeResult{
		NodeID: node.ID(),
		State:  dag.NodeStateSucceeded,
		Output: "ok",
	})

	select {
	case err := <-errCh:
		t.Fatalf("dispatch returned error: %v", err)
	case result := <-resultCh:
		if result == nil || result.State != dag.NodeStateSucceeded {
			t.Fatalf("result = %#v, want succeeded", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatch result")
	}
}

func TestDispatch_KeepsWaitingWhileActivityContinuesAfterDeadline(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	dispatcher := NewBusNodeDispatcher(bus, "orch", "sess", "dag-1", nil, nil, nil)
	dispatcher.activityGrace = 120 * time.Millisecond

	node := dag.NewNode(dag.NodeConfig{
		ID:        "task_1",
		AgentType: "engineer",
		Prompt:    "implement",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	resultCh := make(chan *dag.NodeResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := dispatcher.Dispatch(ctx, node, nil)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	deadline := time.After(2 * time.Second)
	for dispatcher.DispatchDone(node.ID()) == nil {
		select {
		case <-deadline:
			t.Fatal("dispatch did not start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if ok := dispatcher.Acknowledge(node.ID(), &ACKResult{
		AgentID:   "engineer-1",
		AgentType: "engineer",
		AckedAt:   time.Now(),
	}); !ok {
		t.Fatal("expected synthetic ACK to be accepted")
	}

	stopActivity := make(chan struct{})
	go func() {
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopActivity:
				return
			case <-ticker.C:
				dispatcher.RecordActivity(node.ID())
			}
		}
	}()

	time.Sleep(2 * dispatcher.activityGrace)
	dispatcher.OnNodeComplete(node.ID(), &dag.NodeResult{
		NodeID: node.ID(),
		State:  dag.NodeStateSucceeded,
		Output: "ok",
	})
	close(stopActivity)

	select {
	case err := <-errCh:
		t.Fatalf("dispatch returned error: %v", err)
	case result := <-resultCh:
		if result == nil || result.State != dag.NodeStateSucceeded {
			t.Fatalf("result = %#v, want succeeded", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatch result")
	}
}

func TestDispatch_UsesNodeTimeoutAsSlidingActivityWindow(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	dispatcher := NewBusNodeDispatcher(bus, "orch", "sess", "dag-1", nil, nil, nil)
	dispatcher.activityGrace = 30 * time.Millisecond

	node := dag.NewNode(dag.NodeConfig{
		ID:        "task_1",
		AgentType: "engineer",
		Prompt:    "implement",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	resultCh := make(chan *dag.NodeResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := dispatcher.Dispatch(ctx, node, nil)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	deadline := time.After(2 * time.Second)
	for dispatcher.DispatchDone(node.ID()) == nil {
		select {
		case <-deadline:
			t.Fatal("dispatch did not start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if ok := dispatcher.Acknowledge(node.ID(), &ACKResult{
		AgentID:   "engineer-1",
		AgentType: "engineer",
		AckedAt:   time.Now(),
	}); !ok {
		t.Fatal("expected synthetic ACK to be accepted")
	}

	time.Sleep(90 * time.Millisecond)
	dispatcher.RecordActivity(node.ID())

	time.Sleep(70 * time.Millisecond)
	dispatcher.OnNodeComplete(node.ID(), &dag.NodeResult{
		NodeID: node.ID(),
		State:  dag.NodeStateSucceeded,
		Output: "ok",
	})

	select {
	case err := <-errCh:
		t.Fatalf("dispatch returned error: %v", err)
	case result := <-resultCh:
		if result == nil || result.State != dag.NodeStateSucceeded {
			t.Fatalf("result = %#v, want succeeded", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatch result")
	}
}

func TestDispatch_NoDeadlineTimesOutOnInactivity(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	dispatcher := NewBusNodeDispatcher(bus, "orch", "sess", "dag-1", nil, nil, nil)
	dispatcher.activityGrace = 80 * time.Millisecond

	node := dag.NewNode(dag.NodeConfig{
		ID:        "task_1",
		AgentType: "engineer",
		Prompt:    "implement",
	})

	errCh := make(chan error, 1)
	go func() {
		_, err := dispatcher.Dispatch(context.Background(), node, nil)
		errCh <- err
	}()

	deadline := time.After(2 * time.Second)
	for dispatcher.DispatchDone(node.ID()) == nil {
		select {
		case <-deadline:
			t.Fatal("dispatch did not start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if ok := dispatcher.Acknowledge(node.ID(), &ACKResult{
		AgentID:   "engineer-1",
		AgentType: "engineer",
		AckedAt:   time.Now(),
	}); !ok {
		t.Fatal("expected synthetic ACK to be accepted")
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected inactivity timeout error")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inactivity timeout")
	}
}

func TestDispatch_NoDeadlineKeepsWaitingWhileActivityContinues(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	dispatcher := NewBusNodeDispatcher(bus, "orch", "sess", "dag-1", nil, nil, nil)
	dispatcher.activityGrace = 80 * time.Millisecond

	node := dag.NewNode(dag.NodeConfig{
		ID:        "task_1",
		AgentType: "engineer",
		Prompt:    "implement",
	})

	resultCh := make(chan *dag.NodeResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := dispatcher.Dispatch(context.Background(), node, nil)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	deadline := time.After(2 * time.Second)
	for dispatcher.DispatchDone(node.ID()) == nil {
		select {
		case <-deadline:
			t.Fatal("dispatch did not start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if ok := dispatcher.Acknowledge(node.ID(), &ACKResult{
		AgentID:   "engineer-1",
		AgentType: "engineer",
		AckedAt:   time.Now(),
	}); !ok {
		t.Fatal("expected synthetic ACK to be accepted")
	}

	stopActivity := make(chan struct{})
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopActivity:
				return
			case <-ticker.C:
				dispatcher.RecordActivity(node.ID())
			}
		}
	}()

	time.Sleep(2 * dispatcher.activityGrace)
	dispatcher.OnNodeComplete(node.ID(), &dag.NodeResult{
		NodeID: node.ID(),
		State:  dag.NodeStateSucceeded,
		Output: "ok",
	})
	close(stopActivity)

	select {
	case err := <-errCh:
		t.Fatalf("dispatch returned error: %v", err)
	case result := <-resultCh:
		if result == nil || result.State != dag.NodeStateSucceeded {
			t.Fatalf("result = %#v, want succeeded", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatch result")
	}
}
