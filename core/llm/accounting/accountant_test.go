package accounting

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
)

// =============================================================================
// Test fixtures — a live identity.Factory wired with the same canonical
// shape the runtime uses. No mocking of identity values — Record takes
// real *AgentIdentity / *TaskRef and we exercise the full accounting
// surface against them.
// =============================================================================

type stubModelRegistry struct {
	models map[identity.AgentType][]identity.ModelID
	cats   map[identity.AgentType]identity.Category
	window identity.ContextWindow
}

func (r *stubModelRegistry) AllowedModels(k identity.AgentType) []identity.ModelID {
	return r.models[k]
}

func (r *stubModelRegistry) Category(k identity.AgentType) (identity.Category, bool) {
	c, ok := r.cats[k]
	return c, ok
}

func (r *stubModelRegistry) ContextWindow(_ identity.AgentType, _ identity.ModelID) identity.ContextWindow {
	return r.window
}

type stubPodRegistry struct {
	pods        map[identity.PodID]identity.PodType
	pipelinePod identity.PodID
}

func (r *stubPodRegistry) Lookup(id identity.PodID) (identity.PodType, bool) {
	pt, ok := r.pods[id]
	return pt, ok
}

func (r *stubPodRegistry) PipelinePodID() identity.PodID { return r.pipelinePod }

type uidCounter struct{ n atomic.Uint64 }

func (c *uidCounter) next() string {
	return "uid-" + strconv.FormatUint(c.n.Add(1), 10)
}

func newTestFactory(t *testing.T, ns identity.Namespace) (*identity.Factory, *uidCounter) {
	t.Helper()
	counter := &uidCounter{}
	models := &stubModelRegistry{
		models: map[identity.AgentType][]identity.ModelID{
			identity.AgentTypeGuide:             {"haiku-4.5-200k"},
			identity.AgentTypeArchitect:         {"claude-opus-4-6", "claude-sonnet-4-6"},
			identity.AgentTypeOrchestrator:      {"gemini-3-flash-preview"},
			identity.AgentTypeArchivalist:       {"sonnet-4.5-1m"},
			identity.AgentTypeLibrarian:         {"sonnet-4.5-1m"},
			identity.AgentTypeAcademic:          {"opus-4.5-200k"},
			identity.AgentTypeEngineer:          {"gpt-5.4-pro", "claude-opus-4-6"},
			identity.AgentTypeDesigner:          {"gemini-3.1-pro-preview"},
			identity.AgentTypeInspectorGlobal:   {"opus-4.6"},
			identity.AgentTypeInspectorPipeline: {"opus-4.6"},
			identity.AgentTypeTesterGlobal:      {"gpt-5.4-pro"},
			identity.AgentTypeTesterPipeline:    {"gpt-5.4-pro"},
			identity.AgentTypeGuardian:          {"gpt-5.4-pro"},
		},
		cats: map[identity.AgentType]identity.Category{
			identity.AgentTypeGuide:             identity.CategoryStandalone,
			identity.AgentTypeArchitect:         identity.CategoryKnowledge,
			identity.AgentTypeOrchestrator:      identity.CategoryStandalone,
			identity.AgentTypeArchivalist:       identity.CategoryKnowledge,
			identity.AgentTypeLibrarian:         identity.CategoryKnowledge,
			identity.AgentTypeAcademic:          identity.CategoryKnowledge,
			identity.AgentTypeEngineer:          identity.CategoryPipeline,
			identity.AgentTypeDesigner:          identity.CategoryPipeline,
			identity.AgentTypeInspectorGlobal:   identity.CategoryStandalone,
			identity.AgentTypeInspectorPipeline: identity.CategoryPipeline,
			identity.AgentTypeTesterGlobal:      identity.CategoryStandalone,
			identity.AgentTypeTesterPipeline:    identity.CategoryPipeline,
			identity.AgentTypeGuardian:          identity.CategoryStandalone,
		},
		window: 200_000,
	}
	pods := &stubPodRegistry{
		pods: map[identity.PodID]identity.PodType{
			"guide":        identity.PodTypeDaemon,
			"architect":    identity.PodTypeSingleton,
			"orchestrator": identity.PodTypeDaemon,
			"archivalist":  identity.PodTypeSingleton,
			"librarian":    identity.PodTypeSingleton,
			"academic":     identity.PodTypeSingleton,
			"inspector":    identity.PodTypeSingleton,
			"tester":       identity.PodTypeSingleton,
			"guardian":     identity.PodTypeDaemon,
			"pipeline":     identity.PodTypePipeline,
		},
		pipelinePod: "pipeline",
	}
	f, err := identity.NewFactory(identity.FactoryConfig{
		Namespace: ns,
		Models:    models,
		Pods:      pods,
		NewUID:    counter.next,
	})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	return f, counter
}

func mustMint(t *testing.T, f *identity.Factory, kind identity.AgentType, podID identity.PodID, podType identity.PodType, model identity.ModelID) *identity.AgentIdentity {
	t.Helper()
	id, err := f.Mint(identity.MintOptions{
		Kind:  kind,
		Pod:   identity.PodRef{ID: podID, Type: podType},
		Model: model,
	})
	if err != nil {
		t.Fatalf("Mint(%s/%s): %v", kind, model, err)
	}
	return id
}

func mustNewTask(t *testing.T, f *identity.Factory, corr identity.CorrelationID, pipeline *identity.PipelineRef, display string) *identity.TaskRef {
	t.Helper()
	tk, err := f.NewTask(identity.TaskOptions{
		DisplayID:   display,
		Correlation: corr,
		Pipeline:    pipeline,
	})
	if err != nil {
		t.Fatalf("NewTask: %v", err)
	}
	return tk
}

// resp builds a PhaseResponse Event with the given counts.
func resp(id *identity.AgentIdentity, tk *identity.TaskRef, in, out int, elapsed time.Duration) Event {
	return Event{
		Identity: id,
		Task:     tk,
		Usage:    UsageSample{InputTokens: in, OutputTokens: out},
		Elapsed:  elapsed,
		Phase:    PhaseResponse,
	}
}

// =============================================================================
// Construction
// =============================================================================

func TestNew_RequiresNamespace(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatalf("want error on empty namespace, got nil")
	}
}

func TestNew_DefaultClockAndLogger(t *testing.T) {
	a, err := New(Config{Namespace: "sess-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if a.Namespace() != "sess-1" {
		t.Fatalf("namespace mismatch: %s", a.Namespace())
	}
}

// =============================================================================
// Record: structural validation
// =============================================================================

func TestRecord_RejectsNilIdentity(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	tk := mustNewTask(t, f, "corr-1", nil, "")
	err := a.Record(Event{Task: tk, Phase: PhaseResponse})
	if !errors.Is(err, ErrNilIdentity) {
		t.Fatalf("want ErrNilIdentity, got %v", err)
	}
}

func TestRecord_RejectsNilTask(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	id := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	err := a.Record(Event{Identity: id, Phase: PhaseResponse})
	if !errors.Is(err, ErrNilTask) {
		t.Fatalf("want ErrNilTask, got %v", err)
	}
}

func TestRecord_RejectsNamespaceSkew(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	// Factory for different namespace.
	f, _ := newTestFactory(t, "sess-OTHER")
	id := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "corr-1", nil, "")
	err := a.Record(Event{Identity: id, Task: tk, Phase: PhaseResponse})
	if !errors.Is(err, ErrNamespaceSkew) {
		t.Fatalf("want ErrNamespaceSkew, got %v", err)
	}
}

// =============================================================================
// Per-container, per-generation, per-task, per-model aggregation
// =============================================================================

func TestByKey_AggregatesResponses(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	id := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "corr-1", nil, "")

	mustRecord(t, a, resp(id, tk, 100, 50, 10*time.Millisecond))
	mustRecord(t, a, resp(id, tk, 200, 75, 20*time.Millisecond))

	got := a.ByKey(AccountingKey{Container: id.Key(), Task: tk.UID()})
	if got.Input != 300 || got.Output != 125 {
		t.Fatalf("ByKey: input=%d output=%d, want 300/125", got.Input, got.Output)
	}
	if got.RequestCount != 2 {
		t.Fatalf("request count=%d, want 2", got.RequestCount)
	}
	if got.TotalElapsedNano != int64(30*time.Millisecond) {
		t.Fatalf("elapsed=%d, want 30ms", got.TotalElapsedNano)
	}
	if got.AverageLatency() != 15*time.Millisecond {
		t.Fatalf("avg=%v, want 15ms", got.AverageLatency())
	}
	if !got.Billable {
		t.Fatalf("want billable")
	}
}

func TestByContainer_GroupsByGenerationAndTask(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	idGen0 := mustMint(t, f, identity.AgentTypeArchitect, "architect", identity.PodTypeSingleton, "claude-opus-4-6")
	idGen1, err := f.WithNewGeneration(idGen0, "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("WithNewGeneration: %v", err)
	}

	taskA := mustNewTask(t, f, "corr-A", nil, "task-a")
	taskB := mustNewTask(t, f, "corr-B", nil, "task-b")

	mustRecord(t, a, resp(idGen0, taskA, 100, 50, 5*time.Millisecond))
	mustRecord(t, a, resp(idGen0, taskB, 40, 10, 3*time.Millisecond))
	mustRecord(t, a, resp(idGen1, taskA, 200, 100, 6*time.Millisecond))

	byContainer := a.ByContainer(idGen0.UID())
	if len(byContainer) != 2 {
		t.Fatalf("expect 2 generations, got %d", len(byContainer))
	}
	if gen0 := byContainer[0]; gen0[taskA.UID()].Input != 100 || gen0[taskB.UID()].Input != 40 {
		t.Fatalf("gen 0 breakdown wrong: %+v", gen0)
	}
	if gen1 := byContainer[1]; gen1[taskA.UID()].Input != 200 {
		t.Fatalf("gen 1 breakdown wrong: %+v", gen1)
	}
}

func TestByGeneration_AggregatesOneGeneration(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	idGen0 := mustMint(t, f, identity.AgentTypeArchitect, "architect", identity.PodTypeSingleton, "claude-opus-4-6")
	idGen1, _ := f.WithNewGeneration(idGen0, "claude-sonnet-4-6")

	taskA := mustNewTask(t, f, "corr-A", nil, "")
	taskB := mustNewTask(t, f, "corr-B", nil, "")

	mustRecord(t, a, resp(idGen0, taskA, 100, 50, 0))
	mustRecord(t, a, resp(idGen0, taskB, 30, 15, 0))
	mustRecord(t, a, resp(idGen1, taskA, 1_000, 500, 0))

	gen0 := a.ByGeneration(idGen0.UID(), 0)
	if gen0.Input != 130 || gen0.Output != 65 {
		t.Fatalf("gen 0 agg: in=%d out=%d, want 130/65", gen0.Input, gen0.Output)
	}
	gen1 := a.ByGeneration(idGen0.UID(), 1)
	if gen1.Input != 1_000 || gen1.Output != 500 {
		t.Fatalf("gen 1 agg: in=%d out=%d", gen1.Input, gen1.Output)
	}
}

func TestByKind_AggregatesAcrossContainers(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	o := mustMint(t, f, identity.AgentTypeOrchestrator, "orchestrator", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "corr", nil, "")

	mustRecord(t, a, resp(g, tk, 10, 5, 0))
	mustRecord(t, a, resp(g, tk, 20, 10, 0))
	mustRecord(t, a, resp(o, tk, 100, 50, 0))

	guide := a.ByKind(identity.AgentTypeGuide)
	if guide.Input != 30 || guide.Output != 15 {
		t.Fatalf("guide: in=%d out=%d, want 30/15", guide.Input, guide.Output)
	}
	orch := a.ByKind(identity.AgentTypeOrchestrator)
	if orch.Input != 100 || orch.Output != 50 {
		t.Fatalf("orchestrator: in=%d out=%d", orch.Input, orch.Output)
	}
}

func TestByModel_AggregatesAcrossModels(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	arch := mustMint(t, f, identity.AgentTypeArchitect, "architect", identity.PodTypeSingleton, "claude-opus-4-6")
	eng := mustMint(t, f, identity.AgentTypeEngineer, "pipeline", identity.PodTypePipeline, "claude-opus-4-6")
	gpt := mustMint(t, f, identity.AgentTypeEngineer, "pipeline", identity.PodTypePipeline, "gpt-5.4-pro")
	tk := mustNewTask(t, f, "corr", nil, "")

	mustRecord(t, a, resp(arch, tk, 100, 50, 0))
	mustRecord(t, a, resp(eng, tk, 200, 100, 0))
	mustRecord(t, a, resp(gpt, tk, 50, 25, 0))

	opus := a.ByModel("claude-opus-4-6")
	if opus.Input != 300 || opus.Output != 150 {
		t.Fatalf("opus agg: in=%d out=%d", opus.Input, opus.Output)
	}
	gpt5 := a.ByModel("gpt-5.4-pro")
	if gpt5.Input != 50 || gpt5.Output != 25 {
		t.Fatalf("gpt agg: in=%d out=%d", gpt5.Input, gpt5.Output)
	}
}

func TestByPod_AggregatesByPodHost(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	// guide (daemon) + two pipeline workers
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	e1 := mustMint(t, f, identity.AgentTypeEngineer, "pipeline", identity.PodTypePipeline, "claude-opus-4-6")
	e2 := mustMint(t, f, identity.AgentTypeDesigner, "pipeline", identity.PodTypePipeline, "")
	tk := mustNewTask(t, f, "corr", nil, "")

	mustRecord(t, a, resp(g, tk, 10, 5, 0))
	mustRecord(t, a, resp(e1, tk, 100, 50, 0))
	mustRecord(t, a, resp(e2, tk, 200, 100, 0))

	pipelineAgg := a.ByPod("pipeline")
	if pipelineAgg.Input != 300 || pipelineAgg.Output != 150 {
		t.Fatalf("pipeline pod agg: in=%d out=%d", pipelineAgg.Input, pipelineAgg.Output)
	}
	guideAgg := a.ByPod("guide")
	if guideAgg.Input != 10 {
		t.Fatalf("guide pod agg: in=%d", guideAgg.Input)
	}
}

// =============================================================================
// Per-task and per-pipeline aggregation
// =============================================================================

func TestByTask_AggregatesAcrossContainers(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	o := mustMint(t, f, identity.AgentTypeOrchestrator, "orchestrator", identity.PodTypeDaemon, "")
	sharedTask := mustNewTask(t, f, "corr-shared", nil, "shared")
	otherTask := mustNewTask(t, f, "corr-other", nil, "other")

	mustRecord(t, a, resp(g, sharedTask, 10, 5, 0))
	mustRecord(t, a, resp(o, sharedTask, 100, 50, 0))
	mustRecord(t, a, resp(g, otherTask, 99, 99, 0))

	shared := a.ByTask(sharedTask.UID())
	if shared.Input != 110 || shared.Output != 55 {
		t.Fatalf("shared task agg: in=%d out=%d", shared.Input, shared.Output)
	}
}

func TestByPipeline_FiltersByContainerLabels(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")

	// Engineer with pipeline-labeled identity (labels carry "sylk/pipeline=p1").
	// We mint a replica to attach custom labels via the replica path, but since
	// reserved labels aren't user-settable at mint time we use the task scope
	// for pipeline identity and rely on containerMeta labels. For ByPipeline
	// we explicitly need the container's labels to carry LabelPipeline, which
	// canonical mint populates only when the parent propagates it. The
	// accountant surfaces the label on containerMeta directly from the
	// identity's label map.
	eng := mustMintWithLabel(t, f, identity.AgentTypeEngineer, "pipeline", identity.PodTypePipeline, "claude-opus-4-6", identity.LabelPipeline, "p1")
	other := mustMintWithLabel(t, f, identity.AgentTypeEngineer, "pipeline", identity.PodTypePipeline, "claude-opus-4-6", identity.LabelPipeline, "p2")

	tk1 := mustNewTask(t, f, "corr-1", &identity.PipelineRef{ID: "p1", Stage: "impl"}, "t1")
	tk2 := mustNewTask(t, f, "corr-2", &identity.PipelineRef{ID: "p2", Stage: "impl"}, "t2")

	mustRecord(t, a, resp(eng, tk1, 100, 50, 0))
	mustRecord(t, a, resp(other, tk2, 200, 100, 0))

	p1 := a.ByPipeline("p1")
	if p1.Input != 100 || p1.Output != 50 {
		t.Fatalf("pipeline p1: in=%d out=%d", p1.Input, p1.Output)
	}
	p2 := a.ByPipeline("p2")
	if p2.Input != 200 || p2.Output != 100 {
		t.Fatalf("pipeline p2: in=%d out=%d", p2.Input, p2.Output)
	}
}

// =============================================================================
// Namespace + selector
// =============================================================================

func TestByNamespace_ReturnsZeroForWrongNamespace(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	if got := a.ByNamespace("sess-OTHER"); got.Input != 0 || got.Output != 0 {
		t.Fatalf("want zero, got %+v", got)
	}
}

func TestByNamespace_EqualsAllOnMatching(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "c", nil, "")
	mustRecord(t, a, resp(g, tk, 10, 5, 0))
	ns := a.ByNamespace("sess-1")
	if ns.Input != 10 || ns.Output != 5 {
		t.Fatalf("namespace agg: in=%d out=%d", ns.Input, ns.Output)
	}
}

func TestBySelector_FiltersByLabels(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	o := mustMint(t, f, identity.AgentTypeOrchestrator, "orchestrator", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "c", nil, "")

	mustRecord(t, a, resp(g, tk, 10, 5, 0))
	mustRecord(t, a, resp(o, tk, 100, 50, 0))

	sel := identity.Equals(identity.LabelKind, "guide")
	got := a.BySelector(sel)
	if got.Input != 10 {
		t.Fatalf("selector guide-only: in=%d, want 10", got.Input)
	}

	all := a.BySelector(identity.NewSelector())
	if all.Input != 110 || all.Output != 55 {
		t.Fatalf("match-all selector: in=%d out=%d", all.Input, all.Output)
	}
}

// =============================================================================
// System identity (non-billable but recorded)
// =============================================================================

func TestRecord_SystemIdentityIsNotBillable(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	sysID, err := f.SystemIdentity("oauth-refresh")
	if err != nil {
		t.Fatalf("SystemIdentity: %v", err)
	}
	sysTk, err := f.SystemTask("corr-sys")
	if err != nil {
		t.Fatalf("SystemTask: %v", err)
	}
	mustRecord(t, a, resp(sysID, sysTk, 10, 5, 0))

	// Observable in ByKey.
	byKey := a.ByKey(AccountingKey{Container: sysID.Key(), Task: sysTk.UID()})
	if byKey.Input != 10 {
		t.Fatalf("system ByKey not recorded: %+v", byKey)
	}
	if byKey.Billable {
		t.Fatalf("system key marked billable")
	}

	// Excluded from Billable().
	if b := a.Billable(); b.Input != 0 {
		t.Fatalf("billable total contaminated by system: %+v", b)
	}

	// Also mint a regular identity — Billable should include only it.
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "corr-1", nil, "")
	mustRecord(t, a, resp(g, tk, 1_000, 500, 0))

	b := a.Billable()
	if b.Input != 1_000 || b.Output != 500 {
		t.Fatalf("billable total: in=%d out=%d, want 1000/500", b.Input, b.Output)
	}
	if !b.Billable {
		t.Fatalf("aggregated billable flag should be true")
	}
}

// =============================================================================
// Phase handling — request, response, error
// =============================================================================

func TestRecord_PhaseError_IncrementsErrorCountOnly(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "c", nil, "")

	mustRecord(t, a, Event{Identity: g, Task: tk, Phase: PhaseError, Elapsed: 7 * time.Millisecond})
	mustRecord(t, a, resp(g, tk, 100, 50, 2*time.Millisecond))

	got := a.ByKey(AccountingKey{Container: g.Key(), Task: tk.UID()})
	if got.ErrorCount != 1 {
		t.Fatalf("err count=%d", got.ErrorCount)
	}
	if got.RequestCount != 1 {
		t.Fatalf("req count=%d", got.RequestCount)
	}
	if got.Input != 100 || got.Output != 50 {
		t.Fatalf("in=%d out=%d", got.Input, got.Output)
	}
}

func TestRecord_PhaseRequest_DoesNotAddTokens(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "c", nil, "")

	mustRecord(t, a, Event{Identity: g, Task: tk, Phase: PhaseRequest})
	got := a.ByKey(AccountingKey{Container: g.Key(), Task: tk.UID()})
	if got.Input != 0 || got.Output != 0 || got.RequestCount != 0 {
		t.Fatalf("request phase leaked tokens: %+v", got)
	}
}

// =============================================================================
// Replica concurrency — parallel replicas record independently
// =============================================================================

func TestRecord_ParallelReplicas_KeepSeparateBuckets(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	parent := mustMint(t, f, identity.AgentTypeArchitect, "architect", identity.PodTypeSingleton, "claude-opus-4-6")

	const replicas = 8
	const perReplica = 32

	reps := make([]*identity.AgentIdentity, replicas)
	for i := 0; i < replicas; i++ {
		r, err := f.MintReplica(identity.MintReplicaOptions{Parent: parent})
		if err != nil {
			t.Fatalf("MintReplica: %v", err)
		}
		reps[i] = r
	}
	tk := mustNewTask(t, f, "c", nil, "")

	var wg sync.WaitGroup
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func(r *identity.AgentIdentity) {
			defer wg.Done()
			for j := 0; j < perReplica; j++ {
				_ = a.Record(resp(r, tk, 1, 1, time.Microsecond))
			}
		}(reps[i])
	}
	wg.Wait()

	var total int64
	for _, r := range reps {
		got := a.ByKey(AccountingKey{Container: r.Key(), Task: tk.UID()})
		if got.RequestCount != perReplica {
			t.Fatalf("replica %s request count=%d, want %d", r.Name(), got.RequestCount, perReplica)
		}
		total += got.Input
	}
	if total != int64(replicas*perReplica) {
		t.Fatalf("total input across replicas=%d, want %d", total, replicas*perReplica)
	}
	// Parent bucket must be empty: replicas own their own keys.
	parentAgg := a.ByKey(AccountingKey{Container: parent.Key(), Task: tk.UID()})
	if parentAgg.RequestCount != 0 {
		t.Fatalf("parent bucket leaked replica records: %+v", parentAgg)
	}
}

// =============================================================================
// Subscribe
// =============================================================================

func TestSubscribe_ReceivesDeltasInOrder(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "c", nil, "")

	ch, cancel := a.Subscribe()
	t.Cleanup(cancel)

	const n = 16
	var wg sync.WaitGroup
	wg.Add(1)
	received := make([]int, 0, n)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			select {
			case d := <-ch:
				received = append(received, d.Usage.InputTokens)
			case <-time.After(2 * time.Second):
				t.Errorf("timeout waiting for delta %d", i)
				return
			}
		}
	}()

	for i := 0; i < n; i++ {
		mustRecord(t, a, resp(g, tk, i+1, 0, 0))
	}
	wg.Wait()

	for i, v := range received {
		if v != i+1 {
			t.Fatalf("out-of-order delta at index %d: got %d, want %d", i, v, i+1)
		}
	}
}

func TestSubscribe_CancelRemovesSubscriber(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "c", nil, "")

	ch, cancel := a.Subscribe()
	// Record one delta, consume it, then cancel.
	mustRecord(t, a, resp(g, tk, 1, 1, 0))
	<-ch
	cancel()

	// After cancel, ch is closed — reads return zero-value with ok=false.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("channel should be closed after cancel")
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for channel close")
	}

	// Further Record does not panic or block.
	mustRecord(t, a, resp(g, tk, 1, 1, 0))
}

func TestSubscribe_DropsOnFullBuffer(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1", SubscriberBufferSize: 2})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "c", nil, "")

	ch, cancel := a.Subscribe()
	t.Cleanup(cancel)

	// Publish 5 — buffer is 2, slow consumer never reads.
	for i := 0; i < 5; i++ {
		mustRecord(t, a, resp(g, tk, 1, 1, 0))
	}
	// Drain what made it in.
	drained := 0
	for {
		select {
		case <-ch:
			drained++
		case <-time.After(50 * time.Millisecond):
			if drained != 2 {
				t.Fatalf("drained=%d, want 2 (buffer cap)", drained)
			}
			if dropped := a.DroppedCount(); dropped != 3 {
				t.Fatalf("dropped=%d, want 3", dropped)
			}
			return
		}
	}
}

// =============================================================================
// Close: Record after close is a no-op
// =============================================================================

func TestClose_RecordAfterCloseIsNoop(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "c", nil, "")

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := a.Record(resp(g, tk, 100, 50, 0)); err != nil {
		t.Fatalf("Record after close: %v", err)
	}
	if got := a.ByKey(AccountingKey{Container: g.Key(), Task: tk.UID()}); got.Input != 0 {
		t.Fatalf("accepted record after close: %+v", got)
	}
	// Idempotent.
	if err := a.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// =============================================================================
// WAL replay
// =============================================================================

func TestFileWAL_AppendReplayRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acct.wal")
	w, err := OpenFileWAL(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	a, err := New(Config{Namespace: "sess-1", WAL: w})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	arch := mustMint(t, f, identity.AgentTypeArchitect, "architect", identity.PodTypeSingleton, "claude-opus-4-6")
	tkA := mustNewTask(t, f, "corr-A", nil, "a")
	tkB := mustNewTask(t, f, "corr-B", &identity.PipelineRef{ID: "pl", Stage: "impl"}, "b")

	mustRecord(t, a, resp(g, tkA, 100, 50, 5*time.Millisecond))
	mustRecord(t, a, resp(arch, tkB, 200, 100, 7*time.Millisecond))
	mustRecord(t, a, Event{Identity: g, Task: tkA, Phase: PhaseError})

	if err := a.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	// Reopen and verify state is restored. New() intentionally does
	// NOT replay; callers that want historical reconstruction invoke
	// ReplayFromWAL() explicitly.
	w2, err := OpenFileWAL(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	a2, err := New(Config{Namespace: "sess-1", WAL: w2})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	t.Cleanup(func() { _ = a2.Close() })

	if err := a2.ReplayFromWAL(); err != nil {
		t.Fatalf("replay: %v", err)
	}

	gotA := a2.ByKey(AccountingKey{Container: g.Key(), Task: tkA.UID()})
	if gotA.Input != 100 || gotA.Output != 50 || gotA.ErrorCount != 1 {
		t.Fatalf("replay tkA: %+v", gotA)
	}
	gotB := a2.ByKey(AccountingKey{Container: arch.Key(), Task: tkB.UID()})
	if gotB.Input != 200 || gotB.Output != 100 {
		t.Fatalf("replay tkB: %+v", gotB)
	}
	if !gotA.Billable || !gotB.Billable {
		t.Fatalf("billable lost across replay")
	}
	// Pipeline label survived replay via containerMeta.
	// arch was minted without an explicit pipeline label, so ByPipeline("pl")
	// won't match. Use ByTask instead to confirm task-level restoration.
	taskB := a2.ByTask(tkB.UID())
	if taskB.Input != 200 {
		t.Fatalf("replay ByTask: %+v", taskB)
	}
}

func TestFileWAL_SkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acct.wal")
	// Pre-write garbage + one valid-looking JSON record that our
	// schema can tolerate (missing fields just round-trip as zeros;
	// rebuildIdentity returns an error on empty UID and the line is
	// skipped).
	garbage := []byte("{not json\n{}\n{\"schema\":1}\n")
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w, err := OpenFileWAL(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	a, err := New(Config{Namespace: "sess-1", WAL: w})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// New() does not replay, so state starts empty even when the
	// backing file contains (garbage) records.
	if len(a.All()) != 0 {
		t.Fatalf("unexpected state before explicit replay: %+v", a.All())
	}
	// Explicit replay must tolerate and skip the corrupt lines.
	if err := a.ReplayFromWAL(); err != nil {
		t.Fatalf("replay tolerated corrupt lines but returned error: %v", err)
	}
	if len(a.All()) != 0 {
		t.Fatalf("corrupt-line replay produced state: %+v", a.All())
	}

	// Append a real record; a fresh accountant's ReplayFromWAL should see it.
	f, _ := newTestFactory(t, "sess-1")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "c", nil, "")
	mustRecord(t, a, resp(g, tk, 10, 5, 0))
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	w2, err := OpenFileWAL(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	a2, err := New(Config{Namespace: "sess-1", WAL: w2})
	if err != nil {
		t.Fatalf("new2: %v", err)
	}
	t.Cleanup(func() { _ = a2.Close() })
	if err := a2.ReplayFromWAL(); err != nil {
		t.Fatalf("replay2: %v", err)
	}
	if got := a2.ByKey(AccountingKey{Container: g.Key(), Task: tk.UID()}); got.Input != 10 {
		t.Fatalf("after corrupt skip: %+v", got)
	}
}

func TestFileWAL_CreatesDirectoryIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "acct")
	path := filepath.Join(dir, "wal.log")
	w, err := OpenFileWAL(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("parent not created as dir")
	}
}

func TestFileWAL_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenFileWAL(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestNoopWAL_ContractsHold(t *testing.T) {
	w := NoopWAL{}
	if err := w.Append(UsageDelta{}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Replay(func(UsageDelta) error { return nil }); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// =============================================================================
// Property-style test: conservation of totals across views
// =============================================================================

// TestProperty_ReducerTotalsAreConsistent: for any set of recorded
// events, the following must hold:
//
//	ByNamespace == sum over all buckets == ByKind(K) summed over all Ks
//
// and
//
//	ByTask(T) summed over all tasks == ByNamespace
//
// This protects against drift between the per-axis reducers and the
// canonical bucket map.
func TestProperty_ReducerTotalsAreConsistent(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-prop"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-prop")

	// Mint a mix of containers + tasks and record deterministic deltas.
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	arch := mustMint(t, f, identity.AgentTypeArchitect, "architect", identity.PodTypeSingleton, "claude-opus-4-6")
	eng1 := mustMint(t, f, identity.AgentTypeEngineer, "pipeline", identity.PodTypePipeline, "claude-opus-4-6")
	eng2 := mustMint(t, f, identity.AgentTypeEngineer, "pipeline", identity.PodTypePipeline, "gpt-5.4-pro")

	tasks := make([]*identity.TaskRef, 0, 4)
	for i := 0; i < 4; i++ {
		tasks = append(tasks, mustNewTask(t, f, identity.CorrelationID("c"+strconv.Itoa(i)), nil, "t"+strconv.Itoa(i)))
	}

	agents := []*identity.AgentIdentity{g, arch, eng1, eng2}

	var expectedInput, expectedOutput int64
	for ai, ag := range agents {
		for ti, tk := range tasks {
			in := (ai + 1) * (ti + 1) * 10
			out := in / 2
			mustRecord(t, a, resp(ag, tk, in, out, time.Duration(ti+1)*time.Millisecond))
			expectedInput += int64(in)
			expectedOutput += int64(out)
		}
	}

	ns := a.ByNamespace("sess-prop")
	if ns.Input != expectedInput || ns.Output != expectedOutput {
		t.Fatalf("ByNamespace: %+v, want in=%d out=%d", ns, expectedInput, expectedOutput)
	}

	// Sum of ByKind across all referenced kinds == namespace total.
	var kindSum AggregatedUsage
	for _, k := range []identity.AgentType{
		identity.AgentTypeGuide, identity.AgentTypeArchitect, identity.AgentTypeEngineer,
	} {
		kindSum.Merge(a.ByKind(k))
	}
	if kindSum.Input != expectedInput || kindSum.Output != expectedOutput {
		t.Fatalf("ByKind sum: %+v, want in=%d out=%d", kindSum, expectedInput, expectedOutput)
	}

	// Sum of ByTask across all tasks == namespace total.
	var taskSum AggregatedUsage
	for _, tk := range tasks {
		taskSum.Merge(a.ByTask(tk.UID()))
	}
	if taskSum.Input != expectedInput || taskSum.Output != expectedOutput {
		t.Fatalf("ByTask sum: %+v, want in=%d out=%d", taskSum, expectedInput, expectedOutput)
	}

	// Sum of ByModel across all models == namespace total.
	var modelSum AggregatedUsage
	for _, m := range []identity.ModelID{"haiku-4.5-200k", "claude-opus-4-6", "gpt-5.4-pro"} {
		modelSum.Merge(a.ByModel(m))
	}
	if modelSum.Input != expectedInput || modelSum.Output != expectedOutput {
		t.Fatalf("ByModel sum: %+v, want in=%d out=%d", modelSum, expectedInput, expectedOutput)
	}

	// Sum of All() == namespace total.
	var allSum AggregatedUsage
	for _, s := range a.All() {
		allSum.Merge(s.Usage)
	}
	if allSum.Input != expectedInput || allSum.Output != expectedOutput {
		t.Fatalf("All sum: %+v, want in=%d out=%d", allSum, expectedInput, expectedOutput)
	}
}

// =============================================================================
// Concurrent Record stress test (race detector)
// =============================================================================

func TestRecord_RaceFree_UnderConcurrentWriters(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-race"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-race")
	g := mustMint(t, f, identity.AgentTypeGuide, "guide", identity.PodTypeDaemon, "")
	tk := mustNewTask(t, f, "c", nil, "")

	const writers = 8
	const per = 256

	ch, cancel := a.Subscribe()
	t.Cleanup(cancel)
	go func() {
		for range ch {
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				_ = a.Record(resp(g, tk, 1, 1, time.Microsecond))
			}
		}()
	}
	wg.Wait()

	got := a.ByKey(AccountingKey{Container: g.Key(), Task: tk.UID()})
	if got.RequestCount != int64(writers*per) {
		t.Fatalf("concurrent request count=%d, want %d", got.RequestCount, writers*per)
	}
	if got.Input != int64(writers*per) {
		t.Fatalf("concurrent input=%d, want %d", got.Input, writers*per)
	}
}

// =============================================================================
// Pipeline tasks carry pipeline labels — ByPipeline aggregates correctly
// =============================================================================

func TestByPipeline_AggregatesSubTasks(t *testing.T) {
	a, _ := New(Config{Namespace: "sess-1"})
	t.Cleanup(func() { _ = a.Close() })
	f, _ := newTestFactory(t, "sess-1")

	eng := mustMintWithLabel(t, f, identity.AgentTypeEngineer, "pipeline", identity.PodTypePipeline, "claude-opus-4-6", identity.LabelPipeline, "pl-42")
	tkRoot := mustNewTask(t, f, "corr-root", &identity.PipelineRef{ID: "pl-42", Stage: "design"}, "root")
	tkSub, err := f.SubTask(identity.SubTaskOptions{
		Parent: tkRoot,
		Stage:  "impl",
	})
	if err != nil {
		t.Fatalf("SubTask: %v", err)
	}

	mustRecord(t, a, resp(eng, tkRoot, 100, 50, 0))
	mustRecord(t, a, resp(eng, tkSub, 200, 100, 0))

	got := a.ByPipeline("pl-42")
	if got.Input != 300 || got.Output != 150 {
		t.Fatalf("pipeline agg across subtasks: in=%d out=%d", got.Input, got.Output)
	}
}

// =============================================================================
// Helpers
// =============================================================================

func mustRecord(t *testing.T, a *Accountant, evt Event) {
	t.Helper()
	if err := a.Record(evt); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

// mustMintWithLabel mints an identity and then injects a reserved
// label into its label map by constructing via RebuildForReplay —
// this is the only path that lets tests plant a reserved-prefix label
// without going through Factory (which forbids user-supplied reserved
// labels). Mirrors how dispatch sites patch in LabelPipeline during
// request dispatch in production.
func mustMintWithLabel(t *testing.T, f *identity.Factory, kind identity.AgentType, podID identity.PodID, podType identity.PodType, model identity.ModelID, key, value string) *identity.AgentIdentity {
	t.Helper()
	id := mustMint(t, f, kind, podID, podType, model)
	// Rebuild with an additional reserved label. Copy current labels,
	// set the override, and reconstruct via RebuildForReplay.
	merged := identity.Labels{}
	for k, v := range id.Labels() {
		merged[k] = v
	}
	merged[key] = value
	return identity.RebuildForReplay(identity.ReplayAgentIdentity{
		UID:        id.UID(),
		Namespace:  id.Namespace(),
		Pod:        id.Pod(),
		Name:       id.Name(),
		Kind:       id.Kind(),
		Category:   id.Category(),
		Model:      id.Model(),
		Generation: id.Generation(),
		Window:     id.Window(),
		Labels:     merged,
		Owner:      id.Owner(),
	})
}
