package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/dag"
)

// --- test helpers ---

// trackingActivator records HoldActive calls and returns configurable
// release functions and errors.
type trackingActivator struct {
	mu       sync.Mutex
	calls    []string
	releases int
	failOn   string // agent type that should fail
}

func (a *trackingActivator) EnsureActive(_ context.Context, agentType string) error {
	return nil
}

func (a *trackingActivator) TouchActivity(string) {}

func (a *trackingActivator) HoldActive(_ context.Context, agentType string) (func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.calls = append(a.calls, agentType)
	if a.failOn == agentType {
		return func() {}, errors.New("activation failed: " + agentType)
	}

	released := false
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if !released {
			released = true
			a.releases++
		}
	}, nil
}

func (a *trackingActivator) holdCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func (a *trackingActivator) releaseCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.releases
}

func (a *trackingActivator) calledTypes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.calls))
	copy(out, a.calls)
	return out
}

// trackingRegistrar records registrar calls.
type trackingRegistrar struct {
	mu     sync.Mutex
	calls  []string
	failOn string
}

func (r *trackingRegistrar) register(_ context.Context, agentType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, agentType)
	if r.failOn == agentType {
		return errors.New("register failed: " + agentType)
	}
	return nil
}

func (r *trackingRegistrar) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// testDAG builds a simple DAG with the given node configs.
func testDAG(t *testing.T, nodes ...dag.NodeConfig) *dag.DAG {
	t.Helper()
	d := dag.NewDAG("test-dag", dag.ExecutionPolicy{})
	for _, n := range nodes {
		if err := d.AddNode(n); err != nil {
			t.Fatalf("add node: %v", err)
		}
	}
	return d
}

// --- tests ---

func TestPipelinePod_Activate_AllAgentsHeld(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{}

	d := testDAG(t,
		dag.NodeConfig{ID: "n1", AgentType: "engineer", Prompt: "task1"},
		dag.NodeConfig{ID: "n2", AgentType: "designer", Prompt: "task2"},
	)

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     d.ID(),
		Activator: act,
		Registrar: reg.register,
	})

	if err := pod.Activate(context.Background(), d); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	// engineer, designer, inspector-pipeline, tester-pipeline
	if got := act.holdCount(); got != 4 {
		t.Errorf("expected 4 HoldActive calls, got %d: %v", got, act.calledTypes())
	}
	if got := reg.callCount(); got != 4 {
		t.Errorf("expected 4 registrar calls, got %d", got)
	}

	pod.Release()
	if got := act.releaseCount(); got != 4 {
		t.Errorf("expected 4 releases, got %d", got)
	}
}

func TestPipelinePod_Activate_RollbackOnFailure(t *testing.T) {
	act := &trackingActivator{failOn: "inspector-pipeline"}
	reg := &trackingRegistrar{}

	d := testDAG(t,
		dag.NodeConfig{ID: "n1", AgentType: "engineer", Prompt: "task1"},
	)

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     d.ID(),
		Activator: act,
		Registrar: reg.register,
	})

	err := pod.Activate(context.Background(), d)
	if err == nil {
		t.Fatal("expected error from failed activation")
	}

	// engineer succeeded → its guard should have been released during rollback
	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 rollback release, got %d", got)
	}
}

func TestPipelinePod_Activate_RollbackOnRegistrarFailure(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{failOn: "designer"}

	d := testDAG(t,
		dag.NodeConfig{ID: "n1", AgentType: "engineer", Prompt: "task1"},
		dag.NodeConfig{ID: "n2", AgentType: "designer", Prompt: "task2"},
	)

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     d.ID(),
		Activator: act,
		Registrar: reg.register,
	})

	err := pod.Activate(context.Background(), d)
	if err == nil {
		t.Fatal("expected error from failed registrar")
	}

	// engineer + designer guards acquired, both released during rollback
	if got := act.releaseCount(); got != 2 {
		t.Errorf("expected 2 rollback releases, got %d", got)
	}
}

func TestPipelinePod_Release_Idempotent(t *testing.T) {
	act := &trackingActivator{}

	d := testDAG(t,
		dag.NodeConfig{ID: "n1", AgentType: "engineer", Prompt: "task1"},
	)

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     d.ID(),
		Activator: act,
	})

	if err := pod.Activate(context.Background(), d); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	pod.Release()
	pod.Release() // second call should be a no-op

	// 3 agents: engineer, inspector-pipeline, tester-pipeline
	if got := act.releaseCount(); got != 3 {
		t.Errorf("expected 3 releases (not double), got %d", got)
	}
}

func TestPipelinePod_Release_ConcurrentSafe(t *testing.T) {
	act := &trackingActivator{}

	d := testDAG(t,
		dag.NodeConfig{ID: "n1", AgentType: "engineer", Prompt: "task1"},
	)

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     d.ID(),
		Activator: act,
	})

	if err := pod.Activate(context.Background(), d); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pod.Release()
		}()
	}
	wg.Wait()

	if got := act.releaseCount(); got != 3 {
		t.Errorf("expected 3 releases from concurrent Release(), got %d", got)
	}
}

func TestCollectAgentTypes_Dedup(t *testing.T) {
	d := testDAG(t,
		dag.NodeConfig{ID: "n1", AgentType: "engineer", Prompt: "task1"},
		dag.NodeConfig{ID: "n2", AgentType: "engineer", Prompt: "task2"},
		dag.NodeConfig{ID: "n3", AgentType: "designer", Prompt: "task3",
			CoAgents: []string{"engineer", "inspector-pipeline"},
		},
	)

	types := collectAgentTypes(d)

	seen := make(map[string]int)
	for _, t := range types {
		seen[t]++
	}

	for agentType, count := range seen {
		if count != 1 {
			t.Errorf("agent type %q appears %d times, expected 1", agentType, count)
		}
	}

	// Must contain: engineer, designer, inspector-pipeline, tester-pipeline
	for _, expected := range []string{"engineer", "designer", "inspector-pipeline", "tester-pipeline"} {
		if seen[expected] == 0 {
			t.Errorf("missing expected agent type %q in %v", expected, types)
		}
	}
}

func TestCollectAgentTypes_EmptyDAG(t *testing.T) {
	d := dag.NewDAG("empty", dag.ExecutionPolicy{})
	types := collectAgentTypes(d)

	// Even an empty DAG requires inspector-pipeline and tester-pipeline
	if len(types) != 2 {
		t.Errorf("expected 2 types for empty DAG, got %d: %v", len(types), types)
	}
}

func TestPipelinePod_NilRegistrar(t *testing.T) {
	act := &trackingActivator{}

	d := testDAG(t,
		dag.NodeConfig{ID: "n1", AgentType: "engineer", Prompt: "task1"},
	)

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     d.ID(),
		Activator: act,
		// Registrar is nil — should not panic
	})

	if err := pod.Activate(context.Background(), d); err != nil {
		t.Fatalf("Activate with nil registrar: %v", err)
	}

	// 3 agents: engineer, inspector-pipeline, tester-pipeline
	if got := act.holdCount(); got != 3 {
		t.Errorf("expected 3 HoldActive calls, got %d", got)
	}

	pod.Release()
}
