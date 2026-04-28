package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/dag"
)

// fakeBridgeForEnsure builds the minimum DAGBridge surface that
// ensureTaskPod and taskPodCacheLookup touch. Avoids spinning up the
// full DAG/scheduler stack just to exercise the per-task creation
// gate.
func fakeBridgeForEnsure() *DAGBridge {
	return &DAGBridge{
		activeDAGs: make(map[string]*ActiveDAGMeta),
		gates:      make(map[string]*dag.DecisionGate),
	}
}

func registerFakeMeta(b *DAGBridge, dagID string, factory func(string) (*shared.AgentPod, error)) *ActiveDAGMeta {
	meta := &ActiveDAGMeta{
		PlanID:         "plan-test",
		SessionID:      "sess-test",
		TaskPods:       make(map[string]*shared.AgentPod),
		TaskPodFactory: factory,
		taskPodSlots:   make(map[string]*taskPodSlot),
		subNodesByTask: make(map[string][]subNodeRegistration),
	}
	b.mu.Lock()
	b.activeDAGs[dagID] = meta
	b.mu.Unlock()
	return meta
}

// TestEnsureTaskPod_FactoryCalledOnce verifies the per-task sync.Once
// gate: even under concurrent ensure calls for the same task, the
// factory runs exactly once and all callers get the same pod.
func TestEnsureTaskPod_FactoryCalledOnce(t *testing.T) {
	b := fakeBridgeForEnsure()
	var calls atomic.Int64
	registerFakeMeta(b, "dag-1", func(taskID string) (*shared.AgentPod, error) {
		calls.Add(1)
		return &shared.AgentPod{}, nil
	})

	const concurrency = 32
	var wg sync.WaitGroup
	pods := make([]*shared.AgentPod, concurrency)
	errs := make([]error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pods[i], errs[i] = b.ensureTaskPod(context.Background(), "dag-1", "task-1")
		}(i)
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("factory call count: want 1 (sync.Once); got %d", got)
	}
	for i, p := range pods {
		if errs[i] != nil {
			t.Fatalf("ensure[%d]: unexpected error: %v", i, errs[i])
		}
		if p == nil {
			t.Fatalf("ensure[%d]: nil pod returned", i)
		}
		if p != pods[0] {
			t.Fatalf("ensure[%d]: pod identity mismatch — concurrent callers must see the same instance", i)
		}
	}
}

// TestEnsureTaskPod_FactoryErrorIsSticky verifies that a failed
// creation is not retried — repeated calls return the original error.
// This matches the design intent: a broken pod is a hard failure;
// the DAG's existing failure paths handle it.
func TestEnsureTaskPod_FactoryErrorIsSticky(t *testing.T) {
	b := fakeBridgeForEnsure()
	var calls atomic.Int64
	registerFakeMeta(b, "dag-1", func(taskID string) (*shared.AgentPod, error) {
		calls.Add(1)
		return nil, fmt.Errorf("boom")
	})

	if pod, err := b.ensureTaskPod(context.Background(), "dag-1", "task-1"); err == nil {
		t.Fatalf("first ensure: want error; got pod=%v", pod)
	}
	if pod, err := b.ensureTaskPod(context.Background(), "dag-1", "task-1"); err == nil {
		t.Fatalf("second ensure: want sticky error; got pod=%v", pod)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("factory retry: want 1 call; got %d", got)
	}
}

// TestEnsureTaskPod_DAGNotActive returns an error rather than nil
// silently. Callers must not dispatch on an unknown DAG.
func TestEnsureTaskPod_DAGNotActive(t *testing.T) {
	b := fakeBridgeForEnsure()
	if pod, err := b.ensureTaskPod(context.Background(), "dag-missing", "task-1"); err == nil {
		t.Fatalf("want error for missing dag; got pod=%v", pod)
	}
}

// TestEnsureTaskPod_NoFactory returns an error rather than nil
// silently. Misconfigured ActiveDAGMeta is a programmer error, not a
// silent skip.
func TestEnsureTaskPod_NoFactory(t *testing.T) {
	b := fakeBridgeForEnsure()
	registerFakeMeta(b, "dag-1", nil)
	if pod, err := b.ensureTaskPod(context.Background(), "dag-1", "task-1"); err == nil {
		t.Fatalf("want error for missing factory; got pod=%v", pod)
	}
}

// TestEnsureTaskPod_PublishesIntoTaskPodsCacheAfterCreation verifies
// the strict invariant: TaskPods[taskID] is set ONLY after the
// factory has returned a successful pod (and sub-node registrations
// have been applied — sub-nodes go through pod.RegisterSubNode which
// is exercised by the integration tests; here we verify the cache
// publish ordering).
func TestEnsureTaskPod_PublishesIntoTaskPodsCacheAfterCreation(t *testing.T) {
	b := fakeBridgeForEnsure()
	startedFactory := make(chan struct{})
	releaseFactory := make(chan struct{})
	registerFakeMeta(b, "dag-1", func(taskID string) (*shared.AgentPod, error) {
		close(startedFactory)
		<-releaseFactory
		return &shared.AgentPod{}, nil
	})

	// Start ensure in background.
	done := make(chan struct{})
	go func() {
		_, _ = b.ensureTaskPod(context.Background(), "dag-1", "task-1")
		close(done)
	}()

	// Wait until factory has started but not returned.
	<-startedFactory

	// At this moment the cache MUST NOT contain the pod — factory
	// hasn't returned yet, so by the strict invariant the pod isn't
	// observable.
	if pod := b.taskPodCacheLookup("dag-1", "task-1"); pod != nil {
		t.Fatal("cache should not expose pod before factory returns; saw pod=" + fmt.Sprintf("%p", pod))
	}

	// Release the factory and wait for ensure to complete.
	close(releaseFactory)
	<-done

	// Now the cache MUST contain the pod.
	if pod := b.taskPodCacheLookup("dag-1", "task-1"); pod == nil {
		t.Fatal("cache should expose pod after ensureTaskPod returns")
	}
}

// TestEnsureTaskPod_ConcurrentDifferentTasksDontBlock verifies the
// per-task gate is per-task, not global — different tasks can be
// constructed in parallel.
func TestEnsureTaskPod_ConcurrentDifferentTasksDontBlock(t *testing.T) {
	b := fakeBridgeForEnsure()
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})

	registerFakeMeta(b, "dag-1", func(taskID string) (*shared.AgentPod, error) {
		switch taskID {
		case "task-a":
			close(startedA)
			<-releaseA
		case "task-b":
			close(startedB)
			<-releaseB
		}
		return &shared.AgentPod{}, nil
	})

	go func() { _, _ = b.ensureTaskPod(context.Background(), "dag-1", "task-a") }()
	go func() { _, _ = b.ensureTaskPod(context.Background(), "dag-1", "task-b") }()

	// Both factories must enter concurrently — task-b shouldn't be
	// blocked by task-a's pending construction.
	<-startedA
	<-startedB

	// Release in reverse order: B finishes before A — verifies the
	// gate is task-local, not a global serializer.
	close(releaseB)
	close(releaseA)
}
