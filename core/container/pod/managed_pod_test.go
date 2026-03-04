package pod

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
)

// --- test helpers ---

type mockAgent struct {
	id        string
	agentType string
}

func (a *mockAgent) AgentID() string                        { return a.id }
func (a *mockAgent) AgentType() string                      { return a.agentType }
func (a *mockAgent) Terminate(_ context.Context) error      { return nil }

func testRuntime(t *testing.T) *container.DefaultRuntime {
	t.Helper()
	budget := concurrency.NewGoroutineBudget(new(atomic.Int32))
	reg := container.NewContainerRegistry()
	return container.NewDefaultRuntime(container.DefaultRuntimeConfig{
		Budget:   budget,
		Registry: reg,
		CreateAgent: func(_ context.Context, agentType string) (container.ContainerAgent, error) {
			return &mockAgent{id: agentType + "-1", agentType: agentType}, nil
		},
	})
}

func testPolicy(podID string, members []string, podType PodType) *PodPolicy {
	return &PodPolicy{
		PodID:       podID,
		PodType:     podType,
		MemberTypes: members,
		IdleToWarm:  30 * time.Second,
		IdleToCool:  5 * time.Minute,
		IdleToCold:  30 * time.Minute,
	}
}

func testSpecs(members []string) []container.ContainerSpec {
	specs := make([]container.ContainerSpec, len(members))
	for i, m := range members {
		specs[i] = container.ContainerSpec{
			Name:      m,
			AgentType: m,
			Resources: container.ResourceSpec{GoroutineLimit: 2},
		}
	}
	return specs
}

// --- tests ---

func TestManagedPod_PromoteFromCold(t *testing.T) {
	rt := testRuntime(t)
	members := []string{"engineer", "designer"}
	p := NewManagedPod(ManagedPodConfig{
		ID:      "test-pod",
		Policy:  testPolicy("test-pod", members, PodTypePipeline),
		Runtime: rt,
	})

	if p.LoadTier() != TierCold {
		t.Fatalf("expected TierCold, got %v", p.LoadTier())
	}

	specs := testSpecs(members)
	if err := p.Promote(context.Background(), specs); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	if p.LoadTier() != TierHot {
		t.Fatalf("expected TierHot after promote, got %v", p.LoadTier())
	}
	if p.ContainerCount() != 2 {
		t.Fatalf("expected 2 containers, got %d", p.ContainerCount())
	}
	if p.ContainerFor("engineer") == nil {
		t.Fatal("expected engineer container")
	}
	if p.ContainerFor("designer") == nil {
		t.Fatal("expected designer container")
	}

	snap := p.Metrics().Snapshot()
	if snap.ColdStarts != 1 {
		t.Errorf("expected 1 cold start, got %d", snap.ColdStarts)
	}
	if snap.PromotionsTotal != 1 {
		t.Errorf("expected 1 promotion, got %d", snap.PromotionsTotal)
	}
}

func TestManagedPod_HotHit(t *testing.T) {
	rt := testRuntime(t)
	members := []string{"architect"}
	p := NewManagedPod(ManagedPodConfig{
		ID:      "singleton",
		Policy:  testPolicy("singleton", members, PodTypeSingleton),
		Runtime: rt,
	})

	if err := p.Promote(context.Background(), testSpecs(members)); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// Second promote should be a hot hit.
	if err := p.Promote(context.Background(), nil); err != nil {
		t.Fatalf("hot promote: %v", err)
	}

	snap := p.Metrics().Snapshot()
	if snap.HotHits != 1 {
		t.Errorf("expected 1 hot hit, got %d", snap.HotHits)
	}
}

func TestManagedPod_DemoteHotToWarm(t *testing.T) {
	rt := testRuntime(t)
	members := []string{"engineer"}
	p := NewManagedPod(ManagedPodConfig{
		ID:      "demo",
		Policy:  testPolicy("demo", members, PodTypePipeline),
		Runtime: rt,
	})

	if err := p.Promote(context.Background(), testSpecs(members)); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	if err := p.Demote(context.Background(), TierWarm); err != nil {
		t.Fatalf("Demote: %v", err)
	}

	if p.LoadTier() != TierWarm {
		t.Fatalf("expected TierWarm, got %v", p.LoadTier())
	}

	snap := p.Metrics().Snapshot()
	if snap.DemotionsToWarm != 1 {
		t.Errorf("expected 1 demotion to warm, got %d", snap.DemotionsToWarm)
	}
}

func TestManagedPod_DemoteHotToCold(t *testing.T) {
	rt := testRuntime(t)
	members := []string{"engineer"}
	p := NewManagedPod(ManagedPodConfig{
		ID:      "full-demo",
		Policy:  testPolicy("full-demo", members, PodTypePipeline),
		Runtime: rt,
	})

	if err := p.Promote(context.Background(), testSpecs(members)); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	if err := p.Demote(context.Background(), TierCold); err != nil {
		t.Fatalf("Demote to cold: %v", err)
	}

	if p.LoadTier() != TierCold {
		t.Fatalf("expected TierCold, got %v", p.LoadTier())
	}
	if p.ContainerCount() != 0 {
		t.Fatalf("expected 0 containers after full demotion, got %d", p.ContainerCount())
	}

	snap := p.Metrics().Snapshot()
	if snap.DemotionsToWarm != 1 {
		t.Errorf("warm demotions: got %d, want 1", snap.DemotionsToWarm)
	}
	if snap.DemotionsToCool != 1 {
		t.Errorf("cool demotions: got %d, want 1", snap.DemotionsToCool)
	}
	if snap.DemotionsToCold != 1 {
		t.Errorf("cold demotions: got %d, want 1", snap.DemotionsToCold)
	}
}

func TestManagedPod_PromoteFromWarm(t *testing.T) {
	rt := testRuntime(t)
	members := []string{"engineer"}
	p := NewManagedPod(ManagedPodConfig{
		ID:      "warm-test",
		Policy:  testPolicy("warm-test", members, PodTypePipeline),
		Runtime: rt,
	})

	if err := p.Promote(context.Background(), testSpecs(members)); err != nil {
		t.Fatalf("cold promote: %v", err)
	}
	if err := p.Demote(context.Background(), TierWarm); err != nil {
		t.Fatalf("demote to warm: %v", err)
	}
	if err := p.Promote(context.Background(), nil); err != nil {
		t.Fatalf("warm promote: %v", err)
	}

	if p.LoadTier() != TierHot {
		t.Fatalf("expected TierHot, got %v", p.LoadTier())
	}

	snap := p.Metrics().Snapshot()
	if snap.WarmStarts != 1 {
		t.Errorf("warm starts: got %d, want 1", snap.WarmStarts)
	}
}

func TestManagedPod_GuardManagement(t *testing.T) {
	rt := testRuntime(t)
	p := NewManagedPod(ManagedPodConfig{
		ID:      "guard-test",
		Policy:  testPolicy("guard-test", []string{"engineer"}, PodTypePipeline),
		Runtime: rt,
	})

	release := p.AcquireRequestGuard()
	if !p.HasActiveRequests() {
		t.Fatal("expected active requests after acquire")
	}
	if p.ActiveRequestCount() != 1 {
		t.Fatalf("expected 1 active request, got %d", p.ActiveRequestCount())
	}

	release()
	if p.HasActiveRequests() {
		t.Fatal("expected no active requests after release")
	}

	// Idempotent release.
	release()
	if p.ActiveRequestCount() != 0 {
		t.Fatalf("expected 0 after double release, got %d", p.ActiveRequestCount())
	}
}

func TestManagedPod_HoldForNode(t *testing.T) {
	rt := testRuntime(t)
	p := NewManagedPod(ManagedPodConfig{
		ID:      "node-test",
		Policy:  testPolicy("node-test", []string{"engineer", "designer"}, PodTypePipeline),
		Runtime: rt,
	})

	if err := p.HoldForNode(context.Background(), "n1", []string{"engineer", "designer"}); err != nil {
		t.Fatalf("HoldForNode: %v", err)
	}
	if p.ActiveGuardCount() != 1 {
		t.Fatalf("expected 1 guard entry, got %d", p.ActiveGuardCount())
	}
	if p.ActiveRequestCount() != 2 {
		t.Fatalf("expected 2 active requests, got %d", p.ActiveRequestCount())
	}

	p.ReleaseForNode("n1")
	if p.ActiveGuardCount() != 0 {
		t.Fatalf("expected 0 guard entries after release, got %d", p.ActiveGuardCount())
	}
	if p.ActiveRequestCount() != 0 {
		t.Fatalf("expected 0 active requests after release, got %d", p.ActiveRequestCount())
	}
}

func TestManagedPod_Release(t *testing.T) {
	rt := testRuntime(t)
	p := NewManagedPod(ManagedPodConfig{
		ID:      "release-test",
		Policy:  testPolicy("release-test", []string{"engineer"}, PodTypePipeline),
		Runtime: rt,
	})

	_ = p.HoldForNode(context.Background(), "n1", []string{"engineer"})
	_ = p.HoldForNode(context.Background(), "n2", []string{"designer"})

	p.Release()
	if !p.IsReleased() {
		t.Fatal("expected released after Release()")
	}
	if p.ActiveGuardCount() != 0 {
		t.Fatalf("expected 0 guards after release, got %d", p.ActiveGuardCount())
	}

	// HoldForNode after release should fail.
	err := p.HoldForNode(context.Background(), "n3", []string{"engineer"})
	if err != ErrPodReleased {
		t.Fatalf("expected ErrPodReleased, got %v", err)
	}
}

func TestManagedPod_ConcurrentGuards(t *testing.T) {
	rt := testRuntime(t)
	p := NewManagedPod(ManagedPodConfig{
		ID:      "concurrent-test",
		Policy:  testPolicy("concurrent-test", []string{"engineer"}, PodTypePipeline),
		Runtime: rt,
	})

	const n = 100
	done := make(chan struct{})
	for i := range n {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			release := p.AcquireRequestGuard()
			time.Sleep(time.Millisecond)
			release()
		}(i)
	}

	for range n {
		<-done
	}

	if p.ActiveRequestCount() != 0 {
		t.Fatalf("expected 0 active requests after concurrent test, got %d", p.ActiveRequestCount())
	}

	snap := p.Metrics().Snapshot()
	if snap.GuardAcquisitions != n {
		t.Errorf("acquisitions: got %d, want %d", snap.GuardAcquisitions, n)
	}
	if snap.GuardReleases != n {
		t.Errorf("releases: got %d, want %d", snap.GuardReleases, n)
	}
}
