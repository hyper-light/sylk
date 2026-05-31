package activation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	csecurity "github.com/adalundhe/sylk/core/container/security"
)

// --- Test helpers ---

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func testScope(t *testing.T) *concurrency.GoroutineScope {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return concurrency.NewGoroutineScope(ctx, "test-scope", nil)
}

func newMockContainer(t *testing.T, agentType string) *container.Container {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	scope := concurrency.NewGoroutineScope(ctx, "test-"+agentType, nil)
	secCtx := csecurity.NewSecurityContext(csecurity.SecurityContextConfig{
		ContainerID: "test-" + agentType,
		AgentID:     "agent-" + agentType,
		Role:        "worker",
	})
	agent := &mockContainerAgent{id: "agent-" + agentType, agentType: agentType}
	return container.NewContainer(container.ContainerConfig{
		ID:     container.ContainerID("test-" + agentType),
		Spec:   container.ContainerSpec{Name: agentType, AgentType: agentType},
		Scope:  scope,
		SecCtx: secCtx,
		Agent:  agent,
	})
}

type mockContainerAgent struct {
	id        string
	agentType string
}

func (m *mockContainerAgent) AgentID() string                   { return m.id }
func (m *mockContainerAgent) AgentType() string                 { return m.agentType }
func (m *mockContainerAgent) Terminate(_ context.Context) error { return nil }

// mockRuntime implements container.ContainerRuntime for testing.
type mockRuntime struct {
	mu         sync.Mutex
	created    []*container.Container
	started    int
	stopped    int
	paused     int
	resumed    int
	removed    int
	createFunc func(ctx context.Context, spec container.ContainerSpec) (*container.Container, error)
}

func newMockRuntime(t *testing.T) *mockRuntime {
	t.Helper()
	return &mockRuntime{
		createFunc: func(_ context.Context, spec container.ContainerSpec) (*container.Container, error) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			scope := concurrency.NewGoroutineScope(ctx, "rt-"+spec.AgentType, nil)
			secCtx := csecurity.NewSecurityContext(csecurity.SecurityContextConfig{
				ContainerID: "rt-" + spec.AgentType,
				Role:        "worker",
			})
			agent := &mockContainerAgent{id: "agent-" + spec.AgentType, agentType: spec.AgentType}
			c := container.NewContainer(container.ContainerConfig{
				ID:     container.ContainerID("rt-" + spec.AgentType),
				Spec:   spec,
				Scope:  scope,
				SecCtx: secCtx,
				Agent:  agent,
			})
			return c, nil
		},
	}
}

func (r *mockRuntime) CreateContainer(ctx context.Context, spec container.ContainerSpec) (*container.Container, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.createFunc(ctx, spec)
	if err != nil {
		return nil, err
	}
	r.created = append(r.created, c)
	return c, nil
}

func (r *mockRuntime) StartContainer(_ context.Context, c *container.Container) error {
	r.mu.Lock()
	r.started++
	r.mu.Unlock()
	return c.Start(context.Background())
}

func (r *mockRuntime) StopContainer(_ context.Context, c *container.Container) error {
	r.mu.Lock()
	r.stopped++
	r.mu.Unlock()
	return c.Stop(context.Background())
}

func (r *mockRuntime) PauseContainer(_ context.Context, c *container.Container) error {
	r.mu.Lock()
	r.paused++
	r.mu.Unlock()
	return c.Pause()
}

func (r *mockRuntime) ResumeContainer(_ context.Context, c *container.Container) error {
	r.mu.Lock()
	r.resumed++
	r.mu.Unlock()
	return c.Resume()
}

func (r *mockRuntime) RemoveContainer(_ context.Context, _ *container.Container) error {
	r.mu.Lock()
	r.removed++
	r.mu.Unlock()
	return nil
}

func (r *mockRuntime) ContainerStatus(c *container.Container) *container.ContainerStatus {
	return &container.ContainerStatus{ID: c.ID(), State: c.State()}
}

func (r *mockRuntime) CreateContainersForPod(ctx context.Context, podID container.PodID, specs []container.ContainerSpec) ([]*container.Container, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	containers := make([]*container.Container, 0, len(specs))
	for _, spec := range specs {
		c, err := r.createFunc(ctx, spec)
		if err != nil {
			return nil, err
		}
		c.SetPodID(podID)
		r.created = append(r.created, c)
		containers = append(containers, c)
	}
	return containers, nil
}

func (r *mockRuntime) StartContainers(ctx context.Context, containers []*container.Container) error {
	for _, ctr := range containers {
		if err := r.StartContainer(ctx, ctr); err != nil {
			return err
		}
	}
	return nil
}

func (r *mockRuntime) createdCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.created)
}

func testControllerConfig(t *testing.T) ActivationControllerConfig {
	t.Helper()
	defaults := DefaultPolicyDefaults()
	return ActivationControllerConfig{
		Runtime:      newMockRuntime(t),
		Registry:     container.NewContainerRegistry(),
		Scope:        testScope(t),
		StorageDir:   t.TempDir(),
		WarmPoolCap:  8,
		CoolStoreCap: 16,
		Policies: []*ActivationPolicy{
			DefaultPolicy("engineer", defaults),
			DefaultPolicy("architect", defaults),
			DefaultPolicy("tester", defaults),
		},
	}
}

// --- Tests ---

func TestController_EnsureActive_ColdStart(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	c, err := ac.EnsureActive(testCtx(t), "engineer")
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil container")
	}

	tier, _ := ac.TierOf("engineer")
	if tier != TierHot {
		t.Fatalf("expected TierHot, got %v", tier)
	}

	snap := ac.Metrics().Snapshot()
	if snap.ColdStarts != 1 {
		t.Fatalf("expected 1 cold start, got %d", snap.ColdStarts)
	}
}

func TestController_EnsureActive_HotHit(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	c1, _ := ac.EnsureActive(testCtx(t), "engineer")
	c2, _ := ac.EnsureActive(testCtx(t), "engineer")

	if c1 != c2 {
		t.Fatal("expected same container on hot hit")
	}

	snap := ac.Metrics().Snapshot()
	if snap.HotHits != 1 {
		t.Fatalf("expected 1 hot hit, got %d", snap.HotHits)
	}
}

func TestController_DemoteHotToWarm(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	_, err = ac.EnsureActive(testCtx(t), "engineer")
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}

	err = ac.DemoteTo(testCtx(t), "engineer", TierWarm)
	if err != nil {
		t.Fatalf("DemoteTo: %v", err)
	}

	tier, _ := ac.TierOf("engineer")
	if tier != TierWarm {
		t.Fatalf("expected TierWarm, got %v", tier)
	}

	snap := ac.Metrics().Snapshot()
	if snap.DemotionsToWarm != 1 {
		t.Fatalf("expected 1 warm demotion, got %d", snap.DemotionsToWarm)
	}
}

func TestController_DemoteAndTierOfPostActivationServiceClaims(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "activation-controller-board", SessionID: "activation-controller-session", TaskID: "task"})
	cfg := testControllerConfig(t)
	cfg.BoardProvider = func() *claims.ClaimsBoard { return board }
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	if _, err := ac.EnsureActive(testCtx(t), "engineer"); err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	waitForActivationClaims(t, board, 1)
	if err := ac.DemoteTo(testCtx(t), "engineer", TierWarm); err != nil {
		t.Fatalf("DemoteTo: %v", err)
	}
	waitForActivationClaims(t, board, 2)
	if _, err := ac.TierOf("engineer"); err != nil {
		t.Fatalf("TierOf: %v", err)
	}
	waitForActivationClaims(t, board, 3)

	assertActivationOperationClaim(t, board, "tier_transition")
	assertActivationOperationClaim(t, board, "query_tier")
}

func TestController_PromoteFromWarm(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	_, _ = ac.EnsureActive(testCtx(t), "engineer")
	_ = ac.DemoteTo(testCtx(t), "engineer", TierWarm)

	// Re-activate from Warm.
	c, err := ac.EnsureActive(testCtx(t), "engineer")
	if err != nil {
		t.Fatalf("EnsureActive from warm: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil container")
	}

	tier, _ := ac.TierOf("engineer")
	if tier != TierHot {
		t.Fatalf("expected TierHot after warm start, got %v", tier)
	}

	snap := ac.Metrics().Snapshot()
	if snap.WarmStarts != 1 {
		t.Fatalf("expected 1 warm start, got %d", snap.WarmStarts)
	}
}

func TestController_EnsureActive_UsesStartupPrewarmedContainer(t *testing.T) {
	cfg := testControllerConfig(t)
	rt := cfg.Runtime.(*mockRuntime)
	for _, policy := range cfg.Policies {
		if policy.AgentType == "architect" {
			policy.PreWarmOnStartup = true
		}
	}
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ac.warmStartupEntries()

	if rt.createdCount() != 1 {
		t.Fatalf("expected 1 container created during startup prewarm, got %d", rt.createdCount())
	}
	if rt.started != 1 {
		t.Fatalf("expected startup prewarm to start container once, got %d", rt.started)
	}
	if rt.paused != 1 {
		t.Fatalf("expected startup prewarm to pause container once, got %d", rt.paused)
	}

	c, err := ac.EnsureActive(testCtx(t), "architect")
	if err != nil {
		t.Fatalf("EnsureActive after startup prewarm: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil container")
	}
	if rt.createdCount() != 1 {
		t.Fatalf("expected no second container creation on EnsureActive, got %d", rt.createdCount())
	}
	if rt.resumed != 1 {
		t.Fatalf("expected warm container resume on activation, got %d", rt.resumed)
	}
}

func TestController_PromoteFromWarm_FallsBackToEntryContainer(t *testing.T) {
	cfg := testControllerConfig(t)
	rt := cfg.Runtime.(*mockRuntime)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	warm := newMockContainer(t, "engineer")
	if err := warm.Start(context.Background()); err != nil {
		t.Fatalf("start warm container: %v", err)
	}
	if err := warm.Pause(); err != nil {
		t.Fatalf("pause warm container: %v", err)
	}

	entry, err := ac.getEntry("engineer")
	if err != nil {
		t.Fatalf("getEntry: %v", err)
	}
	entry.Container.Store(warm)
	entry.StoreTier(TierWarm)

	c, err := ac.EnsureActive(testCtx(t), "engineer")
	if err != nil {
		t.Fatalf("EnsureActive from entry fallback warm: %v", err)
	}
	if c != warm {
		t.Fatalf("expected existing warm container, got %v want %v", c, warm)
	}
	if rt.createdCount() != 0 {
		t.Fatalf("expected no cold-start container creation, got %d", rt.createdCount())
	}
	if rt.resumed != 1 {
		t.Fatalf("expected one resume from warm fallback, got %d", rt.resumed)
	}
}

func TestController_FullCycle_ColdHotWarmCoolCold(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ctx := testCtx(t)

	// Cold -> Hot
	_, _ = ac.EnsureActive(ctx, "engineer")
	assertTier(t, ac, "engineer", TierHot)

	// Hot -> Warm
	_ = ac.DemoteTo(ctx, "engineer", TierWarm)
	assertTier(t, ac, "engineer", TierWarm)

	// Warm -> Cool
	_ = ac.DemoteTo(ctx, "engineer", TierCool)
	assertTier(t, ac, "engineer", TierCool)

	// Cool -> Cold
	_ = ac.DemoteTo(ctx, "engineer", TierCold)
	assertTier(t, ac, "engineer", TierCold)
}

func TestController_DefaultCacheCapacityDerivedFromPolicies(t *testing.T) {
	cfg := testControllerConfig(t)
	cfg.WarmPoolCap = 0
	cfg.CoolStoreCap = 0
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ctx := testCtx(t)
	for _, agentType := range []string{"engineer", "architect", "tester"} {
		if _, err := ac.EnsureActive(ctx, agentType); err != nil {
			t.Fatalf("EnsureActive(%s): %v", agentType, err)
		}
		if err := ac.DemoteTo(ctx, agentType, TierWarm); err != nil {
			t.Fatalf("DemoteTo(%s, warm): %v", agentType, err)
		}
		if err := ac.DemoteTo(ctx, agentType, TierCool); err != nil {
			t.Fatalf("DemoteTo(%s, cool): %v", agentType, err)
		}
		assertTier(t, ac, agentType, TierCool)
	}

	if got, want := ac.coolStore.Len(), len(cfg.Policies); got != want {
		t.Fatalf("cool store len = %d, want %d derived from policies", got, want)
	}
}

func TestController_ConcurrentEnsureActive_SameType(t *testing.T) {
	cfg := testControllerConfig(t)
	rt := cfg.Runtime.(*mockRuntime)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	containers := make([]*container.Container, numGoroutines)
	errors := make([]error, numGoroutines)

	for i := range numGoroutines {
		go func(idx int) {
			defer wg.Done()
			c, err := ac.EnsureActive(testCtx(t), "engineer")
			containers[idx] = c
			errors[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errors {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}

	// All should get the same container (coalesced).
	first := containers[0]
	for i := 1; i < len(containers); i++ {
		if containers[i] != first {
			t.Fatalf("goroutine %d got different container", i)
		}
	}

	// Should only create one container.
	if rt.createdCount() != 1 {
		t.Fatalf("expected 1 container created, got %d", rt.createdCount())
	}
}

func TestController_ConcurrentEnsureActive_DifferentTypes(t *testing.T) {
	cfg := testControllerConfig(t)
	rt := cfg.Runtime.(*mockRuntime)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	types := []string{"engineer", "architect", "tester"}
	var wg sync.WaitGroup
	wg.Add(len(types))

	for _, tp := range types {
		go func(agentType string) {
			defer wg.Done()
			_, _ = ac.EnsureActive(testCtx(t), agentType)
		}(tp)
	}
	wg.Wait()

	// Each type should have its own container.
	if rt.createdCount() != 3 {
		t.Fatalf("expected 3 containers created, got %d", rt.createdCount())
	}
}

func TestController_DemoteTo_RespectsMinTier(t *testing.T) {
	cfg := testControllerConfig(t)
	cfg.Policies = []*ActivationPolicy{
		{
			AgentType: "guide",
			MinTier:   TierWarm,
		},
	}
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	_, err = ac.EnsureActive(testCtx(t), "guide")
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}

	_ = ac.DemoteTo(testCtx(t), "guide", TierCold)

	// Should stop at MinTier = TierWarm.
	tier, _ := ac.TierOf("guide")
	if tier != TierWarm {
		t.Fatalf("expected TierWarm (MinTier), got %v", tier)
	}
}

func TestController_UnknownAgentType(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	_, err = ac.EnsureActive(testCtx(t), "nonexistent")
	if err != ErrUnknownAgentType {
		t.Fatalf("expected ErrUnknownAgentType, got %v", err)
	}
}

func TestController_Shutdown(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	_, _ = ac.EnsureActive(testCtx(t), "engineer")
	_, _ = ac.EnsureActive(testCtx(t), "architect")

	err = ac.Shutdown(testCtx(t))
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// All entries should be cold.
	for _, agentType := range []string{"engineer", "architect"} {
		tier, _ := ac.TierOf(agentType)
		if tier != TierCold {
			t.Fatalf("%s should be TierCold after shutdown, got %v", agentType, tier)
		}
	}

	// Further activations should fail.
	_, err = ac.EnsureActive(testCtx(t), "engineer")
	if err != ErrControllerClosed {
		t.Fatalf("expected ErrControllerClosed, got %v", err)
	}
}

func TestController_RegisterPolicy_Runtime(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ac.RegisterPolicy(&ActivationPolicy{
		AgentType: "librarian",
		MinTier:   TierCold,
	})

	if ac.EntryCount() != 4 { // 3 from config + 1 new
		t.Fatalf("expected 4 entries, got %d", ac.EntryCount())
	}
}

func TestController_ActiveAgentTypes(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	if got := ac.ActiveAgentTypes(); len(got) != 0 {
		t.Fatalf("ActiveAgentTypes() initially = %v, want empty", got)
	}

	_, _ = ac.EnsureActive(testCtx(t), "engineer")
	_, _ = ac.EnsureActive(testCtx(t), "architect")

	got := ac.ActiveAgentTypes()
	if len(got) != 2 {
		t.Fatalf("ActiveAgentTypes() len = %d, want 2 (%v)", len(got), got)
	}
}

func TestController_TouchActivity(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ac.TouchActivity("engineer")
	entry, _ := ac.getEntry("engineer")
	if entry.LastActive.Load() == 0 {
		t.Fatal("expected non-zero last active after touch")
	}
}

func TestController_Metrics(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ctx := testCtx(t)

	// Cold start.
	_, _ = ac.EnsureActive(ctx, "engineer")
	// Hot hit.
	_, _ = ac.EnsureActive(ctx, "engineer")
	// Demote.
	_ = ac.DemoteTo(ctx, "engineer", TierWarm)
	// Warm start.
	_, _ = ac.EnsureActive(ctx, "engineer")

	snap := ac.Metrics().Snapshot()
	if snap.ActivationsTotal != 3 {
		t.Fatalf("expected 3 total, got %d", snap.ActivationsTotal)
	}
	if snap.ColdStarts != 1 {
		t.Fatalf("expected 1 cold, got %d", snap.ColdStarts)
	}
	if snap.HotHits != 1 {
		t.Fatalf("expected 1 hot, got %d", snap.HotHits)
	}
	if snap.WarmStarts != 1 {
		t.Fatalf("expected 1 warm, got %d", snap.WarmStarts)
	}
}

func TestController_EvictUnderPressure(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	// Use a pressure evaluator that always reports pressure.
	ac.pressure = &alwaysPressured{}

	ctx := testCtx(t)
	_, _ = ac.EnsureActive(ctx, "engineer")
	_, _ = ac.EnsureActive(ctx, "architect")

	ac.EvictUnderPressure(ctx)

	snap := ac.Metrics().Snapshot()
	if snap.EvictionsByPressure == 0 {
		t.Fatal("expected at least one eviction by pressure")
	}
}

// alwaysPressured is a test double for PressureEvaluator.
type alwaysPressured struct {
	count atomic.Int32
}

func (a *alwaysPressured) IsUnderPressure() bool {
	// Stop after first eviction to prevent infinite loop.
	return a.count.Add(1) <= 3
}

func (a *alwaysPressured) Evaluate(entries []*ActivationEntry) []EvictionCandidate {
	pe := NewPressureEvaluator(nil, nil, PressureConfig{})
	return pe.Evaluate(entries)
}

func TestController_EnsureActive_CancelledContext(t *testing.T) {
	cfg := testControllerConfig(t)
	rt := cfg.Runtime.(*mockRuntime)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err = ac.EnsureActive(ctx, "engineer")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// No container should have been created — promote checks ctx.Err()
	// before delegating to promoteFromCold.
	if rt.createdCount() != 0 {
		t.Fatalf("expected 0 containers created, got %d", rt.createdCount())
	}
}

func TestController_PromoteFromCold_CancelBetweenCreateAndStart(t *testing.T) {
	cfg := testControllerConfig(t)
	rt := cfg.Runtime.(*mockRuntime)

	// Wrap createFunc to cancel context after container creation.
	origCreate := rt.createFunc
	cancelAfterCreate := make(chan context.CancelFunc, 1)
	rt.createFunc = func(ctx context.Context, spec container.ContainerSpec) (*container.Container, error) {
		c, err := origCreate(ctx, spec)
		if err != nil {
			return nil, err
		}
		// Signal the test to cancel the context.
		if fn, ok := ctx.Value(cancelKey{}).(context.CancelFunc); ok {
			fn()
		}
		return c, err
	}
	_ = cancelAfterCreate // suppress unused

	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, cancelKey{}, cancel)

	_, err = ac.EnsureActive(ctx, "engineer")
	if err == nil {
		t.Fatal("expected error from context cancelled between create and start")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// Container was created but should have been cleaned up (removed).
	if rt.createdCount() != 1 {
		t.Fatalf("expected 1 container created, got %d", rt.createdCount())
	}

	rt.mu.Lock()
	removed := rt.removed
	rt.mu.Unlock()
	if removed != 1 {
		t.Fatalf("expected 1 container removed (cleanup), got %d", removed)
	}
}

type cancelKey struct{}

func assertTier(t *testing.T, ac *ActivationController, agentType string, expected ActivationTier) {
	t.Helper()
	tier, err := ac.TierOf(agentType)
	if err != nil {
		t.Fatalf("TierOf(%s): %v", agentType, err)
	}
	if tier != expected {
		t.Fatalf("expected %v for %s, got %v", expected, agentType, tier)
	}
}

func waitForActivationClaims(t *testing.T, board *claims.ClaimsBoard, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(board.Projection().Claims) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("claims = %d, want at least %d", len(board.Projection().Claims), want)
}

func assertActivationOperationClaim(t *testing.T, board *claims.ClaimsBoard, operation string) {
	t.Helper()
	for _, claim := range board.Projection().Claims {
		for _, testament := range board.TestamentsByClaim(claim.ID) {
			for _, artifact := range testament.Artifacts {
				data, err := claims.ArtifactData[claims.ActivationRecordArtifactData](artifact)
				if err == nil && data.Operation == operation {
					return
				}
			}
		}
	}
	t.Fatalf("activation operation %q not found", operation)
}

// --- Request Guard Tests ---

func TestController_AcquireRequestGuard_IncrementsCounter(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	entry, _ := ac.getEntry("engineer")
	if entry.HasActiveRequests() {
		t.Fatal("expected no active requests initially")
	}

	release := ac.AcquireRequestGuard("engineer")
	if !entry.HasActiveRequests() {
		t.Fatal("expected active requests after acquire")
	}

	release()
	if entry.HasActiveRequests() {
		t.Fatal("expected no active requests after release")
	}
}

func TestController_AcquireRequestGuard_ReleaseIsIdempotent(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	release := ac.AcquireRequestGuard("engineer")
	release()
	release() // second call must not decrement below zero

	entry, _ := ac.getEntry("engineer")
	if entry.ActiveRequests.Load() != 0 {
		t.Fatalf("expected 0 active requests, got %d", entry.ActiveRequests.Load())
	}
}

func TestController_AcquireRequestGuard_UnknownAgent(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	// Must return a no-op function without panicking.
	release := ac.AcquireRequestGuard("nonexistent")
	release() // no panic
}

func TestController_DemoteWarmToCool_BlockedByActiveRequests(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ctx := testCtx(t)

	// Cold → Hot
	_, err = ac.EnsureActive(ctx, "engineer")
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}

	// Hot → Warm
	err = ac.DemoteTo(ctx, "engineer", TierWarm)
	if err != nil {
		t.Fatalf("DemoteTo Warm: %v", err)
	}
	assertTier(t, ac, "engineer", TierWarm)

	// Acquire guard while at Warm tier
	release := ac.AcquireRequestGuard("engineer")

	// Attempt Warm → Cool — should be blocked
	err = ac.DemoteTo(ctx, "engineer", TierCool)
	if err != nil {
		t.Fatalf("DemoteTo Cool: %v", err)
	}

	// Tier must remain Warm because guard is held
	assertTier(t, ac, "engineer", TierWarm)

	release()
}

func TestController_DemoteTo_StopsWhenGuardBlocksProgress(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ctx := testCtx(t)
	_, err = ac.EnsureActive(ctx, "engineer")
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	if err := ac.DemoteTo(ctx, "engineer", TierWarm); err != nil {
		t.Fatalf("DemoteTo Warm: %v", err)
	}

	release := ac.AcquireRequestGuard("engineer")
	defer release()

	done := make(chan error, 1)
	go func() {
		done <- ac.DemoteTo(ctx, "engineer", TierCool)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DemoteTo Cool: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("DemoteTo should return even when guard blocks progress")
	}

	assertTier(t, ac, "engineer", TierWarm)
}

func TestController_DemoteWarmToCool_ProceedsAfterRelease(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ctx := testCtx(t)

	// Cold → Hot → Warm
	_, err = ac.EnsureActive(ctx, "engineer")
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	err = ac.DemoteTo(ctx, "engineer", TierWarm)
	if err != nil {
		t.Fatalf("DemoteTo Warm: %v", err)
	}

	// Acquire and immediately release guard
	release := ac.AcquireRequestGuard("engineer")
	release()

	// Warm → Cool — should proceed now
	err = ac.DemoteTo(ctx, "engineer", TierCool)
	if err != nil {
		t.Fatalf("DemoteTo Cool after release: %v", err)
	}
	assertTier(t, ac, "engineer", TierCool)
}

func TestController_IdleMonitor_SkipsDemotionWithActiveRequests(t *testing.T) {
	cfg := testControllerConfig(t)
	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ctx := testCtx(t)

	// Cold → Hot
	_, err = ac.EnsureActive(ctx, "engineer")
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}

	// Acquire guard
	release := ac.AcquireRequestGuard("engineer")

	entry, _ := ac.getEntry("engineer")

	// Manually backdate LastActive so the entry looks idle
	entry.LastActive.Store(0)

	// Create idle monitor and call evaluateEntry directly
	im := NewIdleMonitor(ac, testScope(t), time.Second)
	im.evaluateEntry(ctx, entry)

	// Tier should still be Hot — evaluateEntry should have skipped demotion
	assertTier(t, ac, "engineer", TierHot)

	// LastActive should have been refreshed by the guard check
	if entry.LastActive.Load() == 0 {
		t.Fatal("expected LastActive to be refreshed by evaluateEntry guard check")
	}

	release()
}

func TestController_LifecycleCallbacks(t *testing.T) {
	cfg := testControllerConfig(t)
	activated := make(chan string, 2)
	removed := make(chan string, 1)
	cfg.OnActivated = func(c *container.Container) {
		if c == nil {
			return
		}
		activated <- c.Spec().AgentType
	}
	cfg.OnRemoved = func(c *container.Container) {
		if c == nil {
			return
		}
		removed <- c.Spec().AgentType
	}

	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	ctx := testCtx(t)
	if _, err := ac.EnsureActive(ctx, "engineer"); err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}

	select {
	case got := <-activated:
		if got != "engineer" {
			t.Fatalf("activated callback = %q, want %q", got, "engineer")
		}
	case <-time.After(time.Second):
		t.Fatal("expected activated callback")
	}

	if err := ac.DemoteTo(ctx, "engineer", TierCold); err != nil {
		t.Fatalf("DemoteTo cold: %v", err)
	}

	select {
	case got := <-removed:
		if got != "engineer" {
			t.Fatalf("removed callback = %q, want %q", got, "engineer")
		}
	case <-time.After(time.Second):
		t.Fatal("expected removed callback")
	}
}
