package orchestrator

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/events"
)

// --- test helpers ---

type testPipelineAgentPodConfig struct {
	DAGID         string
	PodID         string
	SessionID     string
	Activator     guide.PodActivator
	Registrar     PipelineRegistrar
	ActivityPub   events.ActivityPublisher
	ScribeFactory shared.ScribeFactory
}

func newTestPipelineAgentPod(cfg testPipelineAgentPodConfig) *shared.AgentPod {
	podID := cfg.PodID
	if podID == "" {
		podID = cfg.DAGID
	}
	return shared.NewAgentPod(shared.AgentPodConfig{
		PodID:     podID,
		SessionID: cfg.SessionID,
		Activator: cfg.Activator,
		Registrar: func(ctx context.Context, agentType string) error {
			if cfg.Registrar == nil {
				return nil
			}
			return cfg.Registrar(ctx, podID, agentType)
		},
		ActivityPub:   cfg.ActivityPub,
		MemberTypes:   PipelineAgentTypes[:],
		DisplayNames:  PipelineAgentDisplayNames,
		ScribeFactory: cfg.ScribeFactory,
	})
}

// trackingActivator records HoldPodActive calls and returns configurable
// release functions and errors.
type trackingActivator struct {
	mu       sync.Mutex
	calls    []string
	releases int
	failOn   string // agent type that should fail
}

func (a *trackingActivator) EnsurePodActive(_ context.Context, _ string) error {
	return nil
}

func (a *trackingActivator) TouchPodActivity(string) {}

func (a *trackingActivator) PodForAgent(agentType string) string { return agentType }

func (a *trackingActivator) HoldPodActive(_ context.Context, agentType string) (func(), error) {
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

func (r *trackingRegistrar) register(_ context.Context, _ string, agentType string) error {
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

func (r *trackingRegistrar) calledTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
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

// --- PipelinePod per-node activation tests ---

func TestPipelinePod_HoldForNode_SingleAgent(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
	})

	if err := pod.HoldForNode(context.Background(), "n1", NodeAgentTypes(node)); err != nil {
		t.Fatalf("HoldForNode: %v", err)
	}

	if got := act.holdCount(); got != 1 {
		t.Errorf("expected 1 HoldPodActive call, got %d", got)
	}
	if got := reg.callCount(); got != len(PipelineAgentTypes) {
		t.Errorf("expected %d registrar calls, got %d", len(PipelineAgentTypes), got)
	}
	if got := pod.ActiveGuardCount(); got != 1 {
		t.Errorf("expected 1 active guard, got %d", got)
	}
}

func TestPipelinePod_HoldForNode_WithCoAgents(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
		CoAgents:  []string{"designer", "inspector-pipeline"},
	})

	if err := pod.HoldForNode(context.Background(), "n1", NodeAgentTypes(node)); err != nil {
		t.Fatalf("HoldForNode: %v", err)
	}

	// engineer + designer + inspector-pipeline = 3
	if got := act.holdCount(); got != 3 {
		t.Errorf("expected 3 HoldPodActive calls, got %d: %v", got, act.calledTypes())
	}
	if got := reg.callCount(); got != len(PipelineAgentTypes) {
		t.Errorf("expected %d registrar calls, got %d", len(PipelineAgentTypes), got)
	}
}

func TestPipelinePod_HoldForNode_RegistrarDedup(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	// Two nodes using the same agent type.
	for _, id := range []string{"n1", "n2"} {
		node := dag.NewNode(dag.NodeConfig{
			ID:        id,
			AgentType: "engineer",
			Prompt:    "task",
		})
		if err := pod.HoldForNode(context.Background(), id, NodeAgentTypes(node)); err != nil {
			t.Fatalf("HoldForNode(%s): %v", id, err)
		}
	}

	// Each node gets its own HoldPodActive guard.
	if got := act.holdCount(); got != 2 {
		t.Errorf("expected 2 HoldPodActive calls, got %d", got)
	}
	// Registrar is deduped for the full pipeline roster across nodes.
	if got := reg.callCount(); got != len(PipelineAgentTypes) {
		t.Errorf("expected %d registrar calls (deduped roster), got %d: %v", len(PipelineAgentTypes), got, reg.calledTypes())
	}
	if got := pod.ActiveGuardCount(); got != 2 {
		t.Errorf("expected 2 active guards, got %d", got)
	}
}

func TestPipelinePod_ReleaseForNode(t *testing.T) {
	act := &trackingActivator{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
	})

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "task",
	})

	if err := pod.HoldForNode(context.Background(), "n1", NodeAgentTypes(node)); err != nil {
		t.Fatalf("HoldForNode: %v", err)
	}

	pod.ReleaseForNode("n1")

	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 release, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 0 {
		t.Errorf("expected 0 active guards after release, got %d", got)
	}

	// Second release is a no-op.
	pod.ReleaseForNode("n1")
	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected still 1 release after double-call, got %d", got)
	}
}

func TestPipelinePod_HoldForNode_ActivationFailureRollback(t *testing.T) {
	act := &trackingActivator{failOn: "designer"}
	reg := &trackingRegistrar{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "task",
		CoAgents:  []string{"designer"},
	})

	err := pod.HoldForNode(context.Background(), "n1", NodeAgentTypes(node))
	if err == nil {
		t.Fatal("expected error from failed activation")
	}

	// engineer guard acquired then released during rollback.
	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 rollback release, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 0 {
		t.Errorf("expected 0 active guards after failed activation, got %d", got)
	}
}

func TestPipelinePod_HoldForNode_RegistrarFailureRollback(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{failOn: "designer"}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "task",
		CoAgents:  []string{"designer"},
	})

	err := pod.HoldForNode(context.Background(), "n1", NodeAgentTypes(node))
	if err == nil {
		t.Fatal("expected error from failed registrar")
	}

	// engineer + designer guards acquired, both released during rollback.
	if got := act.releaseCount(); got != 2 {
		t.Errorf("expected 2 rollback releases, got %d", got)
	}
}

func TestPipelinePod_Release_AllRemainingGuards(t *testing.T) {
	act := &trackingActivator{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
	})

	for _, id := range []string{"n1", "n2", "n3"} {
		node := dag.NewNode(dag.NodeConfig{
			ID:        id,
			AgentType: "engineer",
			Prompt:    "task",
		})
		if err := pod.HoldForNode(context.Background(), id, NodeAgentTypes(node)); err != nil {
			t.Fatalf("HoldForNode(%s): %v", id, err)
		}
	}

	pod.Release()

	if got := act.releaseCount(); got != 3 {
		t.Errorf("expected 3 releases from Release(), got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 0 {
		t.Errorf("expected 0 active guards after Release(), got %d", got)
	}
}

func TestPipelinePod_Release_Idempotent(t *testing.T) {
	act := &trackingActivator{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
	})

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "task",
	})

	if err := pod.HoldForNode(context.Background(), "n1", NodeAgentTypes(node)); err != nil {
		t.Fatalf("HoldForNode: %v", err)
	}

	pod.Release()
	pod.Release() // second call is a no-op

	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 release (not double), got %d", got)
	}
}

func TestPipelinePod_Release_ConcurrentSafe(t *testing.T) {
	act := &trackingActivator{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
	})

	for _, id := range []string{"n1", "n2", "n3"} {
		node := dag.NewNode(dag.NodeConfig{
			ID:        id,
			AgentType: "engineer",
			Prompt:    "task",
		})
		if err := pod.HoldForNode(context.Background(), id, NodeAgentTypes(node)); err != nil {
			t.Fatalf("HoldForNode(%s): %v", id, err)
		}
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

func TestPipelinePod_HoldAfterRelease_Rejected(t *testing.T) {
	act := &trackingActivator{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
	})

	pod.Release()

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "task",
	})

	err := pod.HoldForNode(context.Background(), "n1", NodeAgentTypes(node))
	if err == nil {
		t.Fatal("expected error from HoldForNode after Release")
	}
}

func TestPipelinePod_NilActivator(t *testing.T) {
	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID: "dag-1",
		// Activator is nil — should be a no-op
	})

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "task",
	})

	if err := pod.HoldForNode(context.Background(), "n1", NodeAgentTypes(node)); err != nil {
		t.Fatalf("expected nil activator to be a no-op, got: %v", err)
	}
}

func TestPipelinePod_NilRegistrar(t *testing.T) {
	act := &trackingActivator{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
		// Registrar is nil — should not panic
	})

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "task",
	})

	if err := pod.HoldForNode(context.Background(), "n1", NodeAgentTypes(node)); err != nil {
		t.Fatalf("HoldForNode with nil registrar: %v", err)
	}

	if got := act.holdCount(); got != 1 {
		t.Errorf("expected 1 HoldPodActive call, got %d", got)
	}

	pod.Release()
}

func TestPipelinePod_RegisteredAgentTypes(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	for i, cfg := range []dag.NodeConfig{
		{ID: "n1", AgentType: "engineer", Prompt: "task1"},
		{ID: "n2", AgentType: "designer", Prompt: "task2"},
		{ID: "n3", AgentType: "engineer", Prompt: "task3"}, // dedup
	} {
		node := dag.NewNode(cfg)
		if err := pod.HoldForNode(context.Background(), cfg.ID, NodeAgentTypes(node)); err != nil {
			t.Fatalf("HoldForNode(%d): %v", i, err)
		}
	}

	registered := pod.RegisteredAgentTypes()
	seen := make(map[string]struct{}, len(registered))
	for _, typ := range registered {
		seen[typ] = struct{}{}
	}

	for _, expected := range PipelineAgentTypes {
		if _, ok := seen[expected]; !ok {
			t.Errorf("missing registered type %q in %v", expected, registered)
		}
	}
	if len(registered) != len(PipelineAgentTypes) {
		t.Errorf("expected %d registered types, got %d: %v", len(PipelineAgentTypes), len(registered), registered)
	}

	pod.Release()
}

func TestPipelinePod_ActiveNodeIDs(t *testing.T) {
	act := &trackingActivator{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "dag-1",
		Activator: act,
	})

	for _, id := range []string{"n1", "n2"} {
		node := dag.NewNode(dag.NodeConfig{
			ID:        id,
			AgentType: "engineer",
			Prompt:    "task",
		})
		if err := pod.HoldForNode(context.Background(), id, NodeAgentTypes(node)); err != nil {
			t.Fatalf("HoldForNode(%s): %v", id, err)
		}
	}

	ids := pod.ActiveNodeIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 active node IDs, got %d: %v", len(ids), ids)
	}

	pod.ReleaseForNode("n1")

	ids = pod.ActiveNodeIDs()
	if len(ids) != 1 {
		t.Errorf("expected 1 active node ID after partial release, got %d: %v", len(ids), ids)
	}

	pod.Release()
}

// --- collectAgentTypes tests ---

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
	for _, typ := range types {
		seen[typ]++
	}

	for agentType, count := range seen {
		if count != 1 {
			t.Errorf("agent type %q appears %d times, expected 1", agentType, count)
		}
	}

	for _, expected := range []string{"engineer", "designer", "inspector-pipeline", "tester-pipeline"} {
		if seen[expected] == 0 {
			t.Errorf("missing expected agent type %q in %v", expected, types)
		}
	}
}

func TestCollectAgentTypes_EmptyDAG(t *testing.T) {
	d := dag.NewDAG("empty", dag.ExecutionPolicy{})
	types := collectAgentTypes(d)

	if len(types) != len(PipelineAgentTypes) {
		t.Errorf("expected %d types for empty DAG, got %d: %v", len(PipelineAgentTypes), len(types), types)
	}
	for _, expected := range PipelineAgentTypes {
		if !slices.Contains(types, expected) {
			t.Errorf("missing expected pipeline agent type %q in %v", expected, types)
		}
	}
}

// --- PipelinePod sub-node tracking tests ---

func TestPipelinePod_RegisterSubNode(t *testing.T) {
	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{DAGID: "dag-1"})

	pod.RegisterSubNode("n1:inspect", "n1", "inspect", "inspector-pipeline")
	pod.RegisterSubNode("n1:execute", "n1", "execute", "engineer")

	if got := pod.SubNodeCount(); got != 2 {
		t.Fatalf("expected 2 sub-nodes, got %d", got)
	}

	meta := pod.GetSubNode("n1:inspect")
	if meta == nil {
		t.Fatal("expected sub-node metadata for n1:inspect")
	}
	if meta.ParentNodeID != "n1" {
		t.Errorf("expected parent n1, got %s", meta.ParentNodeID)
	}
	if meta.Stage != "inspect" {
		t.Errorf("expected stage inspect, got %s", meta.Stage)
	}
	if meta.AgentType != "inspector-pipeline" {
		t.Errorf("expected agent type inspector-pipeline, got %s", meta.AgentType)
	}
}

func TestPipelinePod_RecordSubNodeACK(t *testing.T) {
	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{DAGID: "dag-1"})
	pod.RegisterSubNode("n1:execute", "n1", "execute", "engineer")

	ackedAt := time.Now()
	pod.RecordSubNodeACK("n1:execute", "agent-42", ackedAt)

	meta := pod.GetSubNode("n1:execute")
	if meta == nil {
		t.Fatal("expected sub-node metadata")
	}
	if meta.AgentID != "agent-42" {
		t.Errorf("expected agent-42, got %s", meta.AgentID)
	}
	if !meta.AckedAt.Equal(ackedAt) {
		t.Errorf("expected acked_at %v, got %v", ackedAt, meta.AckedAt)
	}
}

func TestPipelinePod_GetSubNode_Unknown(t *testing.T) {
	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{DAGID: "dag-1"})
	if meta := pod.GetSubNode("nonexistent"); meta != nil {
		t.Errorf("expected nil for unknown sub-node, got %+v", meta)
	}
}

func TestPipelinePod_RecordSubNodeACK_Unknown(t *testing.T) {
	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{DAGID: "dag-1"})
	// Should not panic for unknown sub-node.
	pod.RecordSubNodeACK("nonexistent", "agent-1", time.Now())
}

// --- PreActivate tests ---

// trackingActivityPub captures published activity events for assertions.
type trackingActivityPub struct {
	mu     sync.Mutex
	events []*events.ActivityEvent
}

func (p *trackingActivityPub) PublishActivity(evt *events.ActivityEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, evt)
}

func (p *trackingActivityPub) collected() []*events.ActivityEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*events.ActivityEvent, len(p.events))
	copy(out, p.events)
	return out
}

func TestPipelinePod_PreActivate_PublishesRegisteredEvents(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{}
	pub := &trackingActivityPub{}

	pod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:       "dag-1",
		SessionID:   "sess-1",
		Activator:   act,
		Registrar:   reg.register,
		ActivityPub: pub,
	})

	pod.PreActivate(context.Background())

	collected := pub.collected()
	if len(collected) != len(PipelineAgentTypes) {
		t.Fatalf("expected %d events, got %d", len(PipelineAgentTypes), len(collected))
	}

	for _, evt := range collected {
		if evt.EventType != events.EventTypeAgentRegistered {
			t.Errorf("expected EventTypeAgentRegistered, got %v for agent %s",
				evt.EventType, evt.AgentID)
		}
	}
}

// --- NodeAgentTypes tests ---

func TestNodeAgentTypes_SingleAgent(t *testing.T) {
	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "task",
	})
	types := NodeAgentTypes(node)
	if len(types) != 1 || types[0] != "engineer" {
		t.Errorf("expected [engineer], got %v", types)
	}
}

func TestNodeAgentTypes_WithCoAgents(t *testing.T) {
	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "task",
		CoAgents:  []string{"designer", "inspector-pipeline"},
	})
	types := NodeAgentTypes(node)
	if len(types) != 3 {
		t.Errorf("expected 3 types, got %d: %v", len(types), types)
	}
}

// Keep shared import referenced for SubNodeMeta type assertions.
var _ = shared.SubNodeMeta{}
