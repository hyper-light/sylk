package shared

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- test helpers ---

type podTrackingActivator struct {
	mu       sync.Mutex
	calls    []string
	releases int
	failOn   string
}

func (a *podTrackingActivator) EnsurePodActive(_ context.Context, _ string) error { return nil }
func (a *podTrackingActivator) TouchPodActivity(string)                           {}
func (a *podTrackingActivator) PodForAgent(agentType string) string               { return agentType }

func (a *podTrackingActivator) HoldPodActive(_ context.Context, agentType string) (func(), error) {
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

func (a *podTrackingActivator) holdCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func (a *podTrackingActivator) releaseCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.releases
}

func (a *podTrackingActivator) calledTypes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.calls))
	copy(out, a.calls)
	return out
}

type podTrackingRegistrar struct {
	mu     sync.Mutex
	calls  []string
	failOn string
}

func (r *podTrackingRegistrar) register(_ context.Context, agentType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, agentType)
	if r.failOn == agentType {
		return errors.New("register failed: " + agentType)
	}
	return nil
}

func (r *podTrackingRegistrar) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *podTrackingRegistrar) calledTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// mockScribe implements the Scribe interface for testing.
type mockScribe struct {
	started atomic.Bool
	stopped atomic.Bool
	feeds   []ScribeFeed
	feedMu  sync.Mutex
}

func (s *mockScribe) Start() error {
	s.started.Store(true)
	return nil
}

func (s *mockScribe) Stop() error {
	s.stopped.Store(true)
	return nil
}

func (s *mockScribe) Feed(feed ScribeFeed) {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	s.feeds = append(s.feeds, feed)
}

func (s *mockScribe) feedCount() int {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	return len(s.feeds)
}

// --- AgentPod tests ---

func TestAgentPod_HoldForNode_SingleAgent(t *testing.T) {
	act := &podTrackingActivator{}
	reg := &podTrackingRegistrar{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
		Registrar: reg.register,
	})

	if err := pod.HoldForNode(context.Background(), "n1", []string{"engineer"}); err != nil {
		t.Fatalf("HoldForNode: %v", err)
	}

	if got := act.holdCount(); got != 1 {
		t.Errorf("expected 1 HoldPodActive call, got %d", got)
	}
	if got := reg.callCount(); got != 1 {
		t.Errorf("expected 1 registrar call, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 1 {
		t.Errorf("expected 1 active guard, got %d", got)
	}

	pod.Release()
}

func TestAgentPod_HoldForNode_WithCoAgents(t *testing.T) {
	act := &podTrackingActivator{}
	reg := &podTrackingRegistrar{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
		Registrar: reg.register,
	})

	agentTypes := []string{"engineer", "designer", "inspector-pipeline"}
	if err := pod.HoldForNode(context.Background(), "n1", agentTypes); err != nil {
		t.Fatalf("HoldForNode: %v", err)
	}

	if got := act.holdCount(); got != 3 {
		t.Errorf("expected 3 HoldPodActive calls, got %d: %v", got, act.calledTypes())
	}
	if got := reg.callCount(); got != 3 {
		t.Errorf("expected 3 registrar calls, got %d", got)
	}

	pod.Release()
}

func TestAgentPod_HoldForNode_RegistrarDedup(t *testing.T) {
	act := &podTrackingActivator{}
	reg := &podTrackingRegistrar{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
		Registrar: reg.register,
	})

	for _, id := range []string{"n1", "n2"} {
		if err := pod.HoldForNode(context.Background(), id, []string{"engineer"}); err != nil {
			t.Fatalf("HoldForNode(%s): %v", id, err)
		}
	}

	if got := act.holdCount(); got != 2 {
		t.Errorf("expected 2 HoldPodActive calls, got %d", got)
	}
	if got := reg.callCount(); got != 1 {
		t.Errorf("expected 1 registrar call (deduped), got %d: %v", got, reg.calledTypes())
	}
	if got := pod.ActiveGuardCount(); got != 2 {
		t.Errorf("expected 2 active guards, got %d", got)
	}

	pod.Release()
}

func TestAgentPod_ReleaseForNode(t *testing.T) {
	act := &podTrackingActivator{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
	})

	if err := pod.HoldForNode(context.Background(), "n1", []string{"engineer"}); err != nil {
		t.Fatalf("HoldForNode: %v", err)
	}

	pod.ReleaseForNode("n1")

	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 release, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 0 {
		t.Errorf("expected 0 active guards, got %d", got)
	}

	// Second release is a no-op.
	pod.ReleaseForNode("n1")
	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected still 1 release, got %d", got)
	}
}

func TestAgentPod_HoldForNode_ActivationFailureRollback(t *testing.T) {
	act := &podTrackingActivator{failOn: "designer"}
	reg := &podTrackingRegistrar{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
		Registrar: reg.register,
	})

	err := pod.HoldForNode(context.Background(), "n1", []string{"engineer", "designer"})
	if err == nil {
		t.Fatal("expected error from failed activation")
	}

	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 rollback release, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 0 {
		t.Errorf("expected 0 active guards, got %d", got)
	}
}

func TestAgentPod_HoldForNode_RegistrarFailureRollback(t *testing.T) {
	act := &podTrackingActivator{}
	reg := &podTrackingRegistrar{failOn: "designer"}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
		Registrar: reg.register,
	})

	err := pod.HoldForNode(context.Background(), "n1", []string{"engineer", "designer"})
	if err == nil {
		t.Fatal("expected error from failed registrar")
	}

	if got := act.releaseCount(); got != 2 {
		t.Errorf("expected 2 rollback releases, got %d", got)
	}
}

func TestAgentPod_Release_AllRemainingGuards(t *testing.T) {
	act := &podTrackingActivator{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
	})

	for _, id := range []string{"n1", "n2", "n3"} {
		if err := pod.HoldForNode(context.Background(), id, []string{"engineer"}); err != nil {
			t.Fatalf("HoldForNode(%s): %v", id, err)
		}
	}

	pod.Release()

	if got := act.releaseCount(); got != 3 {
		t.Errorf("expected 3 releases, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 0 {
		t.Errorf("expected 0 active guards, got %d", got)
	}
}

func TestAgentPod_Release_Idempotent(t *testing.T) {
	act := &podTrackingActivator{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
	})

	if err := pod.HoldForNode(context.Background(), "n1", []string{"engineer"}); err != nil {
		t.Fatalf("HoldForNode: %v", err)
	}

	pod.Release()
	pod.Release()

	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 release (not double), got %d", got)
	}
}

func TestAgentPod_Release_ConcurrentSafe(t *testing.T) {
	act := &podTrackingActivator{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
	})

	for _, id := range []string{"n1", "n2", "n3"} {
		if err := pod.HoldForNode(context.Background(), id, []string{"engineer"}); err != nil {
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
		t.Errorf("expected 3 releases, got %d", got)
	}
}

func TestAgentPod_HoldAfterRelease_Rejected(t *testing.T) {
	act := &podTrackingActivator{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
	})

	pod.Release()

	err := pod.HoldForNode(context.Background(), "n1", []string{"engineer"})
	if err == nil {
		t.Fatal("expected error from HoldForNode after Release")
	}
}

func TestAgentPod_NilActivator(t *testing.T) {
	pod := NewAgentPod(AgentPodConfig{PodID: "pod-1"})

	if err := pod.HoldForNode(context.Background(), "n1", []string{"engineer"}); err != nil {
		t.Fatalf("expected nil activator to be a no-op, got: %v", err)
	}
}

func TestAgentPod_NilRegistrar(t *testing.T) {
	act := &podTrackingActivator{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
	})

	if err := pod.HoldForNode(context.Background(), "n1", []string{"engineer"}); err != nil {
		t.Fatalf("HoldForNode with nil registrar: %v", err)
	}

	if got := act.holdCount(); got != 1 {
		t.Errorf("expected 1 HoldPodActive call, got %d", got)
	}

	pod.Release()
}

func TestAgentPod_RegisteredAgentTypes(t *testing.T) {
	act := &podTrackingActivator{}
	reg := &podTrackingRegistrar{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
		Registrar: reg.register,
	})

	for i, agentTypes := range [][]string{
		{"engineer"},
		{"designer"},
		{"engineer"}, // dedup
	} {
		id := []string{"n1", "n2", "n3"}[i]
		if err := pod.HoldForNode(context.Background(), id, agentTypes); err != nil {
			t.Fatalf("HoldForNode(%d): %v", i, err)
		}
	}

	registered := pod.RegisteredAgentTypes()
	seen := make(map[string]struct{}, len(registered))
	for _, typ := range registered {
		seen[typ] = struct{}{}
	}

	for _, expected := range []string{"engineer", "designer"} {
		if _, ok := seen[expected]; !ok {
			t.Errorf("missing registered type %q in %v", expected, registered)
		}
	}
	if len(registered) != 2 {
		t.Errorf("expected 2 registered types, got %d: %v", len(registered), registered)
	}

	pod.Release()
}

func TestAgentPod_ActiveNodeIDs(t *testing.T) {
	act := &podTrackingActivator{}

	pod := NewAgentPod(AgentPodConfig{
		PodID:     "pod-1",
		Activator: act,
	})

	for _, id := range []string{"n1", "n2"} {
		if err := pod.HoldForNode(context.Background(), id, []string{"engineer"}); err != nil {
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
		t.Errorf("expected 1 active node ID, got %d: %v", len(ids), ids)
	}

	pod.Release()
}

// --- Sub-node tracking tests ---

func TestAgentPod_RegisterSubNode(t *testing.T) {
	pod := NewAgentPod(AgentPodConfig{PodID: "pod-1"})

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

func TestAgentPod_RecordSubNodeACK(t *testing.T) {
	pod := NewAgentPod(AgentPodConfig{PodID: "pod-1"})
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

func TestAgentPod_GetSubNode_Unknown(t *testing.T) {
	pod := NewAgentPod(AgentPodConfig{PodID: "pod-1"})
	if meta := pod.GetSubNode("nonexistent"); meta != nil {
		t.Errorf("expected nil for unknown sub-node, got %+v", meta)
	}
}

// --- Scribe management tests ---

func TestAgentPod_ScribeLifecycle(t *testing.T) {
	scribes := make(map[string]*mockScribe)
	factory := func(parentAgentType string, _ *slog.Logger) (Scribe, error) {
		s := &mockScribe{}
		scribes[parentAgentType] = s
		return s, nil
	}

	act := &podTrackingActivator{}
	pod := NewAgentPod(AgentPodConfig{
		PodID:         "pod-1",
		Activator:     act,
		MemberTypes:   []string{"engineer", "designer"},
		ScribeFactory: factory,
	})

	pod.PreActivate(context.Background())

	// Verify Scribes were started.
	if len(scribes) != 2 {
		t.Fatalf("expected 2 scribes, got %d", len(scribes))
	}
	for _, s := range scribes {
		if !s.started.Load() {
			t.Error("expected scribe to be started")
		}
	}

	// Feed a Scribe.
	pod.FeedScribe("engineer", "build X", "done building X", "corr-1")

	engScribe := scribes["engineer"]
	if engScribe.feedCount() != 1 {
		t.Errorf("expected 1 feed to engineer scribe, got %d", engScribe.feedCount())
	}

	// Feed non-existent Scribe — should be no-op.
	pod.FeedScribe("nonexistent", "a", "b", "c")

	// Release stops Scribes.
	pod.Release()

	for agentType, s := range scribes {
		if !s.stopped.Load() {
			t.Errorf("expected scribe for %s to be stopped", agentType)
		}
	}
}

func TestAgentPod_GetScribe(t *testing.T) {
	factory := func(parentAgentType string, _ *slog.Logger) (Scribe, error) {
		return &mockScribe{}, nil
	}

	act := &podTrackingActivator{}
	pod := NewAgentPod(AgentPodConfig{
		PodID:         "pod-1",
		Activator:     act,
		MemberTypes:   []string{"architect"},
		ScribeFactory: factory,
	})

	pod.PreActivate(context.Background())

	if got := pod.GetScribe("architect"); got == nil {
		t.Error("expected non-nil scribe for architect")
	}
	if got := pod.GetScribe("nonexistent"); got != nil {
		t.Error("expected nil for nonexistent scribe")
	}

	pod.Release()
}

func TestAgentPod_NoScribeFactory(t *testing.T) {
	act := &podTrackingActivator{}
	pod := NewAgentPod(AgentPodConfig{
		PodID:       "pod-1",
		Activator:   act,
		MemberTypes: []string{"engineer"},
	})

	pod.PreActivate(context.Background())

	// No panic, no Scribes.
	if got := pod.GetScribe("engineer"); got != nil {
		t.Error("expected nil scribe when no factory")
	}

	pod.Release()
}
