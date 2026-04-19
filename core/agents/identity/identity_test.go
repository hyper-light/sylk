package identity

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// =============================================================================
// Test fixtures
// =============================================================================

// stubModelRegistry implements ModelRegistry for tests. Simple
// hand-keyed map per agent type.
type stubModelRegistry struct {
	models map[AgentType][]ModelID
	cats   map[AgentType]Category
	window ContextWindow
}

func (r *stubModelRegistry) AllowedModels(kind AgentType) []ModelID {
	return r.models[kind]
}

func (r *stubModelRegistry) Category(kind AgentType) (Category, bool) {
	c, ok := r.cats[kind]
	return c, ok
}

func (r *stubModelRegistry) ContextWindow(_ AgentType, _ ModelID) ContextWindow {
	return r.window
}

// stubPodRegistry implements PodRegistry for tests.
type stubPodRegistry struct {
	pods        map[PodID]PodType
	pipelinePod PodID
}

func (r *stubPodRegistry) Lookup(id PodID) (PodType, bool) {
	pt, ok := r.pods[id]
	return pt, ok
}

func (r *stubPodRegistry) PipelinePodID() PodID { return r.pipelinePod }

// newTestFactory builds a fully wired factory with canonical
// fixtures covering the agent types used across tests. Uses a
// counter-based UID generator so tests observe deterministic UIDs.
func newTestFactory(t *testing.T) (*Factory, *uidCounter) {
	t.Helper()
	counter := &uidCounter{}
	models := &stubModelRegistry{
		models: map[AgentType][]ModelID{
			AgentTypeGuide:             {"haiku-4.5-200k"},
			AgentTypeArchitect:         {"claude-opus-4-6"},
			AgentTypeOrchestrator:      {"gemini-3-flash-preview"},
			AgentTypeArchivalist:       {"sonnet-4.5-1m"},
			AgentTypeLibrarian:         {"sonnet-4.5-1m"},
			AgentTypeAcademic:          {"opus-4.5-200k"},
			AgentTypeEngineer:          {"gpt-5.4-pro", "claude-opus-4-6"},
			AgentTypeDesigner:          {"gemini-3.1-pro-preview"},
			AgentTypeInspectorGlobal:   {"opus-4.6"},
			AgentTypeInspectorPipeline: {"opus-4.6"},
			AgentTypeTesterGlobal:      {"gpt-5.4-pro"},
			AgentTypeTesterPipeline:    {"gpt-5.4-pro"},
			AgentTypeGuardian:          {"gpt-5.4-pro"},
		},
		cats: map[AgentType]Category{
			AgentTypeGuide:             CategoryStandalone,
			AgentTypeArchitect:         CategoryKnowledge,
			AgentTypeOrchestrator:      CategoryStandalone,
			AgentTypeArchivalist:       CategoryKnowledge,
			AgentTypeLibrarian:         CategoryKnowledge,
			AgentTypeAcademic:          CategoryKnowledge,
			AgentTypeEngineer:          CategoryPipeline,
			AgentTypeDesigner:          CategoryPipeline,
			AgentTypeInspectorGlobal:   CategoryStandalone,
			AgentTypeInspectorPipeline: CategoryPipeline,
			AgentTypeTesterGlobal:      CategoryStandalone,
			AgentTypeTesterPipeline:    CategoryPipeline,
			AgentTypeGuardian:          CategoryStandalone,
		},
		window: 200_000,
	}
	pods := &stubPodRegistry{
		pods: map[PodID]PodType{
			"guide":        PodTypeDaemon,
			"architect":    PodTypeSingleton,
			"orchestrator": PodTypeDaemon,
			"archivalist":  PodTypeSingleton,
			"librarian":    PodTypeSingleton,
			"academic":     PodTypeSingleton,
			"inspector":    PodTypeSingleton,
			"tester":       PodTypeSingleton,
			"guardian":     PodTypeDaemon,
			"pipeline":     PodTypePipeline,
		},
		pipelinePod: "pipeline",
	}
	f, err := NewFactory(FactoryConfig{
		Namespace: "sess-test-001",
		Models:    models,
		Pods:      pods,
		NewUID:    counter.next,
	})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	return f, counter
}

// uidCounter produces deterministic UIDs for test assertions.
type uidCounter struct {
	n atomic.Uint64
}

func (c *uidCounter) next() string {
	n := c.n.Add(1)
	return "uid-" + strconv.FormatUint(n, 10)
}

// =============================================================================
// Factory validation tests
// =============================================================================

func TestNewFactory_RejectsMissingNamespace(t *testing.T) {
	_, err := NewFactory(FactoryConfig{
		Models: &stubModelRegistry{},
		Pods:   &stubPodRegistry{},
	})
	if !errors.Is(err, ErrFactoryNotBound) {
		t.Fatalf("want ErrFactoryNotBound, got %v", err)
	}
}

func TestNewFactory_RejectsMissingModels(t *testing.T) {
	_, err := NewFactory(FactoryConfig{
		Namespace: "n",
		Pods:      &stubPodRegistry{},
	})
	if !errors.Is(err, ErrNilModelRegistry) {
		t.Fatalf("want ErrNilModelRegistry, got %v", err)
	}
}

func TestNewFactory_RejectsMissingPods(t *testing.T) {
	_, err := NewFactory(FactoryConfig{
		Namespace: "n",
		Models:    &stubModelRegistry{},
	})
	if !errors.Is(err, ErrNilPodRegistry) {
		t.Fatalf("want ErrNilPodRegistry, got %v", err)
	}
}

// =============================================================================
// Mint tests
// =============================================================================

func TestMint_RequiresKind(t *testing.T) {
	f, _ := newTestFactory(t)
	_, err := f.Mint(MintOptions{Pod: PodRef{ID: "guide", Type: PodTypeDaemon}})
	if !errors.Is(err, ErrMissingKind) {
		t.Fatalf("want ErrMissingKind, got %v", err)
	}
}

func TestMint_RequiresPodID(t *testing.T) {
	f, _ := newTestFactory(t)
	_, err := f.Mint(MintOptions{Kind: AgentTypeGuide})
	if !errors.Is(err, ErrMissingPod) {
		t.Fatalf("want ErrMissingPod, got %v", err)
	}
}

func TestMint_UnknownPod(t *testing.T) {
	f, _ := newTestFactory(t)
	_, err := f.Mint(MintOptions{
		Kind: AgentTypeGuide,
		Pod:  PodRef{ID: "nope", Type: PodTypeDaemon},
	})
	if !errors.Is(err, ErrUnknownPod) {
		t.Fatalf("want ErrUnknownPod, got %v", err)
	}
}

func TestMint_PodTypeMismatch(t *testing.T) {
	f, _ := newTestFactory(t)
	_, err := f.Mint(MintOptions{
		Kind: AgentTypeGuide,
		Pod:  PodRef{ID: "guide", Type: PodTypeSingleton},
	})
	if !errors.Is(err, ErrPodTypeMismatch) {
		t.Fatalf("want ErrPodTypeMismatch, got %v", err)
	}
}

func TestMint_UnknownModel(t *testing.T) {
	f, _ := newTestFactory(t)
	_, err := f.Mint(MintOptions{
		Kind:  AgentTypeGuide,
		Pod:   PodRef{ID: "guide", Type: PodTypeDaemon},
		Model: "not-a-real-model",
	})
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("want ErrUnknownModel, got %v", err)
	}
}

func TestMint_ReservedLabelForbidden(t *testing.T) {
	f, _ := newTestFactory(t)
	_, err := f.Mint(MintOptions{
		Kind:   AgentTypeGuide,
		Pod:    PodRef{ID: "guide", Type: PodTypeDaemon},
		Labels: Labels{"sylk/custom": "no"},
	})
	if !errors.Is(err, ErrReservedLabel) {
		t.Fatalf("want ErrReservedLabel, got %v", err)
	}
}

func TestMint_CanonicalInvariants(t *testing.T) {
	f, counter := newTestFactory(t)
	id, err := f.Mint(MintOptions{
		Kind:   AgentTypeGuide,
		Pod:    PodRef{ID: "guide", Type: PodTypeDaemon},
		Labels: Labels{"team": "platform"},
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id.UID() != UID("uid-1") {
		t.Fatalf("UID = %s, want uid-1", id.UID())
	}
	if id.Namespace() != "sess-test-001" {
		t.Fatalf("Namespace = %s", id.Namespace())
	}
	if id.Pod().ID != "guide" || id.Pod().Type != PodTypeDaemon {
		t.Fatalf("Pod = %s", id.Pod())
	}
	if id.Kind() != AgentTypeGuide {
		t.Fatalf("Kind = %s", id.Kind())
	}
	if id.Category() != CategoryStandalone {
		t.Fatalf("Category = %s", id.Category())
	}
	if id.Model() != "haiku-4.5-200k" {
		t.Fatalf("Model = %s", id.Model())
	}
	if id.Generation() != 0 {
		t.Fatalf("Generation = %d", id.Generation())
	}
	if id.IsReplica() {
		t.Fatal("canonical agent should not be a replica")
	}
	// Reserved labels are auto-populated.
	if id.Label(LabelKind) != "guide" {
		t.Fatalf("LabelKind = %s", id.Label(LabelKind))
	}
	if id.Label(LabelModel) != "" {
		// model is not auto-labeled at Mint time (only on generation
		// bump) — but LabelPod / LabelCategory / LabelNamespace are.
		t.Logf("LabelModel at mint: %q (not expected to be auto-set until swap)", id.Label(LabelModel))
	}
	if id.Label(LabelPod) != "guide" {
		t.Fatalf("LabelPod = %s", id.Label(LabelPod))
	}
	if id.Label(LabelCategory) != "standalone" {
		t.Fatalf("LabelCategory = %s", id.Label(LabelCategory))
	}
	if id.Label(LabelNamespace) != "sess-test-001" {
		t.Fatalf("LabelNamespace = %s", id.Label(LabelNamespace))
	}
	if id.Label("team") != "platform" {
		t.Fatalf("user label 'team' lost: %q", id.Label("team"))
	}
	if counter.n.Load() != 1 {
		t.Fatalf("UID counter should be 1, is %d", counter.n.Load())
	}
}

func TestMint_NameOrdinalsUnique(t *testing.T) {
	f, _ := newTestFactory(t)
	ids := make([]*AgentIdentity, 5)
	for i := range ids {
		id, err := f.Mint(MintOptions{
			Kind: AgentTypeArchitect,
			Pod:  PodRef{ID: "architect", Type: PodTypeSingleton},
		})
		if err != nil {
			t.Fatalf("Mint %d: %v", i, err)
		}
		ids[i] = id
	}
	seen := make(map[Name]bool)
	for _, id := range ids {
		if seen[id.Name()] {
			t.Fatalf("duplicate name %s", id.Name())
		}
		seen[id.Name()] = true
	}
	// Expected shape: architect, architect-1, architect-2, ...
	if ids[0].Name() != "architect" {
		t.Fatalf("first name should be bare kind: %s", ids[0].Name())
	}
	if ids[1].Name() != "architect-1" {
		t.Fatalf("second name should have ordinal: %s", ids[1].Name())
	}
}

// =============================================================================
// Replica tests
// =============================================================================

func TestMintReplica_CanonicalParent(t *testing.T) {
	f, _ := newTestFactory(t)
	parent, err := f.Mint(MintOptions{
		Kind: AgentTypeLibrarian,
		Pod:  PodRef{ID: "librarian", Type: PodTypeSingleton},
	})
	if err != nil {
		t.Fatalf("parent Mint: %v", err)
	}
	replica, err := f.MintReplica(MintReplicaOptions{
		Parent: parent,
		Labels: Labels{"sylk-user/correlation": "corr-abc"},
	})
	if err != nil {
		t.Fatalf("MintReplica: %v", err)
	}
	if !replica.IsReplica() {
		t.Fatal("replica should report IsReplica=true")
	}
	owner := replica.Owner()
	if owner == nil {
		t.Fatal("replica missing owner")
	}
	if owner.UID != parent.UID() {
		t.Fatalf("owner.UID = %s, want %s", owner.UID, parent.UID())
	}
	if owner.Kind != parent.Kind() {
		t.Fatalf("owner.Kind = %s, want %s", owner.Kind, parent.Kind())
	}
	if replica.Kind() != parent.Kind() {
		t.Fatalf("replica should inherit kind, got %s vs %s", replica.Kind(), parent.Kind())
	}
	if replica.UID() == parent.UID() {
		t.Fatal("replica must have a distinct UID from parent")
	}
	if replica.Label(LabelOwnerUID) != string(parent.UID()) {
		t.Fatalf("LabelOwnerUID = %s, want %s", replica.Label(LabelOwnerUID), parent.UID())
	}
}

func TestMintReplica_RequiresParent(t *testing.T) {
	f, _ := newTestFactory(t)
	_, err := f.MintReplica(MintReplicaOptions{})
	if !errors.Is(err, ErrNilParent) {
		t.Fatalf("want ErrNilParent, got %v", err)
	}
}

func TestMintReplica_NameOrdinals(t *testing.T) {
	f, _ := newTestFactory(t)
	parent, _ := f.Mint(MintOptions{
		Kind: AgentTypeArchivalist,
		Pod:  PodRef{ID: "archivalist", Type: PodTypeSingleton},
	})
	var replicas []*AgentIdentity
	for i := 0; i < 3; i++ {
		r, err := f.MintReplica(MintReplicaOptions{Parent: parent})
		if err != nil {
			t.Fatalf("MintReplica %d: %v", i, err)
		}
		replicas = append(replicas, r)
	}
	// Replica names should be of the form "<parent-name>-r<ordinal>".
	for i, r := range replicas {
		expected := fmt.Sprintf("%s-r%d", parent.Name(), i)
		if string(r.Name()) != expected {
			t.Fatalf("replica %d name = %s, want %s", i, r.Name(), expected)
		}
	}
}

// =============================================================================
// Generation / SwapModel tests
// =============================================================================

func TestWithNewGeneration_IncrementsAndRebaselinesModel(t *testing.T) {
	f, _ := newTestFactory(t)
	v0, err := f.Mint(MintOptions{
		Kind: AgentTypeEngineer,
		Pod:  PodRef{ID: "pipeline", Type: PodTypePipeline},
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	v1, err := f.WithNewGeneration(v0, "claude-opus-4-6")
	if err != nil {
		t.Fatalf("WithNewGeneration: %v", err)
	}
	if v1.UID() != v0.UID() {
		t.Fatal("UID must persist across generations")
	}
	if v1.Generation() != v0.Generation()+1 {
		t.Fatalf("Generation = %d, want %d", v1.Generation(), v0.Generation()+1)
	}
	if v1.Model() != "claude-opus-4-6" {
		t.Fatalf("Model = %s", v1.Model())
	}
	if v1.Label(LabelModel) != "claude-opus-4-6" {
		t.Fatalf("LabelModel must update on swap: %s", v1.Label(LabelModel))
	}
	// Prior identity should be untouched.
	if v0.Model() != "gpt-5.4-pro" {
		t.Fatalf("prior model drifted: %s", v0.Model())
	}
	if v0.Generation() != 0 {
		t.Fatalf("prior generation drifted: %d", v0.Generation())
	}
}

func TestWithNewGeneration_RejectsUnknownModel(t *testing.T) {
	f, _ := newTestFactory(t)
	v0, _ := f.Mint(MintOptions{
		Kind: AgentTypeEngineer,
		Pod:  PodRef{ID: "pipeline", Type: PodTypePipeline},
	})
	_, err := f.WithNewGeneration(v0, "nonexistent")
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("want ErrUnknownModel, got %v", err)
	}
}

// =============================================================================
// System identity tests
// =============================================================================

func TestSystemIdentity_IsNonBillable(t *testing.T) {
	f, _ := newTestFactory(t)
	sys, err := f.SystemIdentity("oauth-refresh")
	if err != nil {
		t.Fatalf("SystemIdentity: %v", err)
	}
	if !IsSystem(sys) {
		t.Fatal("SystemIdentity must return IsSystem=true")
	}
	if sys.Category().IsBillable() {
		t.Fatal("system identity must be non-billable")
	}
	if SystemPurpose(sys) != "oauth-refresh" {
		t.Fatalf("SystemPurpose = %s", SystemPurpose(sys))
	}
}

// =============================================================================
// Task tests
// =============================================================================

func TestNewTask_TopLevel(t *testing.T) {
	f, _ := newTestFactory(t)
	task, err := f.NewTask(TaskOptions{
		DisplayID:   "user-hello",
		Correlation: "corr-1",
	})
	if err != nil {
		t.Fatalf("NewTask: %v", err)
	}
	if task.IsPipelineStep() {
		t.Fatal("top-level task should not be a pipeline step")
	}
	if task.Correlation() != "corr-1" {
		t.Fatalf("correlation = %s", task.Correlation())
	}
	if task.Namespace() != "sess-test-001" {
		t.Fatalf("namespace = %s", task.Namespace())
	}
	if task.DisplayID() != "user-hello" {
		t.Fatalf("displayID = %s", task.DisplayID())
	}
}

func TestNewTask_PipelineStep(t *testing.T) {
	f, _ := newTestFactory(t)
	task, err := f.NewTask(TaskOptions{
		Correlation: "corr-p1",
		Pipeline:    &PipelineRef{ID: "pipe-a", Stage: "plan"},
	})
	if err != nil {
		t.Fatalf("NewTask: %v", err)
	}
	if !task.IsPipelineStep() {
		t.Fatal("task should be a pipeline step")
	}
	if task.PipelineID() != "pipe-a" {
		t.Fatalf("pipelineID = %s", task.PipelineID())
	}
	if task.Stage() != "plan" {
		t.Fatalf("stage = %s", task.Stage())
	}
	if task.Label(LabelPipeline) != "pipe-a" {
		t.Fatalf("LabelPipeline = %s", task.Label(LabelPipeline))
	}
}

func TestSubTask_InheritsPipelineAndCorrelation(t *testing.T) {
	f, _ := newTestFactory(t)
	parent, _ := f.NewTask(TaskOptions{
		Correlation: "corr-root",
		Pipeline:    &PipelineRef{ID: "pipe-b", Stage: "analyze"},
	})
	sub, err := f.SubTask(SubTaskOptions{
		Parent: parent,
		Stage:  "synth",
	})
	if err != nil {
		t.Fatalf("SubTask: %v", err)
	}
	if sub.Correlation() != "corr-root" {
		t.Fatalf("sub correlation should inherit: %s", sub.Correlation())
	}
	if sub.PipelineID() != "pipe-b" {
		t.Fatalf("sub pipelineID should inherit: %s", sub.PipelineID())
	}
	if sub.Stage() != "synth" {
		t.Fatalf("sub stage override: %s", sub.Stage())
	}
	if sub.ParentTask() != parent.UID() {
		t.Fatalf("sub parent = %s, want %s", sub.ParentTask(), parent.UID())
	}
}

func TestNewTask_RequiresCorrelation(t *testing.T) {
	f, _ := newTestFactory(t)
	_, err := f.NewTask(TaskOptions{})
	if !errors.Is(err, ErrMissingTaskCorr) {
		t.Fatalf("want ErrMissingTaskCorr, got %v", err)
	}
}

// =============================================================================
// Context propagation tests
// =============================================================================

func TestContextPropagation_IdentityAndTask(t *testing.T) {
	f, _ := newTestFactory(t)
	id, _ := f.Mint(MintOptions{
		Kind: AgentTypeGuide,
		Pod:  PodRef{ID: "guide", Type: PodTypeDaemon},
	})
	task, _ := f.NewTask(TaskOptions{Correlation: "corr-x"})

	ctx := context.Background()
	ctx = WithIdentity(ctx, id)
	ctx = WithTask(ctx, task)

	got, err := RequireIdentity(ctx)
	if err != nil {
		t.Fatalf("RequireIdentity: %v", err)
	}
	if got != id {
		t.Fatal("identity pointer should round-trip")
	}
	gotTask, err := RequireTask(ctx)
	if err != nil {
		t.Fatalf("RequireTask: %v", err)
	}
	if gotTask != task {
		t.Fatal("task pointer should round-trip")
	}

	idOut, taskOut, err := RequireDispatch(ctx)
	if err != nil {
		t.Fatalf("RequireDispatch: %v", err)
	}
	if idOut != id || taskOut != task {
		t.Fatal("RequireDispatch lost a pointer")
	}
}

func TestRequireIdentity_ErrorsWhenAbsent(t *testing.T) {
	_, err := RequireIdentity(context.Background())
	if !errors.Is(err, ErrNoIdentityInContext) {
		t.Fatalf("want ErrNoIdentityInContext, got %v", err)
	}
}

func TestRequireTask_ErrorsWhenAbsent(t *testing.T) {
	_, err := RequireTask(context.Background())
	if !errors.Is(err, ErrNoTaskInContext) {
		t.Fatalf("want ErrNoTaskInContext, got %v", err)
	}
}

func TestWithIdentity_PanicsOnNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("WithIdentity(nil) should panic")
		}
	}()
	WithIdentity(context.Background(), nil)
}

// =============================================================================
// Selector tests
// =============================================================================

func TestSelector_Match(t *testing.T) {
	labels := Labels{
		"sylk/kind":     "engineer",
		"sylk/model":    "gpt-5.4-pro",
		"sylk/pipeline": "pipe-a",
	}
	cases := []struct {
		name string
		sel  *Selector
		want bool
	}{
		{"equals true", Equals("sylk/kind", "engineer"), true},
		{"equals false", Equals("sylk/kind", "librarian"), false},
		{"exists true", Exists("sylk/pipeline"), true},
		{"exists false", Exists("nonexistent"), false},
		{"notexists true", NewSelector().NotExists("nonexistent"), true},
		{"notexists false", NewSelector().NotExists("sylk/kind"), false},
		{"in true", In("sylk/kind", "engineer", "designer"), true},
		{"in false", In("sylk/kind", "librarian", "academic"), false},
		{"notin true", NewSelector().NotIn("sylk/kind", "a", "b"), true},
		{"notin false", NewSelector().NotIn("sylk/kind", "engineer"), false},
		{"empty selector matches anything", NewSelector(), true},
		{"chained both pass", NewSelector().Equals("sylk/kind", "engineer").Exists("sylk/pipeline"), true},
		{"chained one fails", NewSelector().Equals("sylk/kind", "engineer").NotExists("sylk/pipeline"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sel.Matches(labels); got != tc.want {
				t.Fatalf("selector %s on %v: got %v, want %v", tc.sel, labels, got, tc.want)
			}
		})
	}
}

// =============================================================================
// Concurrency tests
// =============================================================================

func TestFactory_ConcurrentMintIsSafe(t *testing.T) {
	f, _ := newTestFactory(t)
	const goroutines = 32
	const perRoutine = 20
	total := goroutines * perRoutine
	ids := make(chan *AgentIdentity, total)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perRoutine; i++ {
				id, err := f.Mint(MintOptions{
					Kind: AgentTypeArchitect,
					Pod:  PodRef{ID: "architect", Type: PodTypeSingleton},
				})
				if err != nil {
					t.Errorf("Mint: %v", err)
					return
				}
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)

	seenUIDs := make(map[UID]bool, total)
	seenNames := make(map[Name]bool, total)
	for id := range ids {
		if seenUIDs[id.UID()] {
			t.Errorf("duplicate UID under concurrent mint: %s", id.UID())
		}
		seenUIDs[id.UID()] = true
		if seenNames[id.Name()] {
			t.Errorf("duplicate Name under concurrent mint: %s", id.Name())
		}
		seenNames[id.Name()] = true
	}
	if len(seenUIDs) != total {
		t.Fatalf("expected %d unique UIDs, got %d", total, len(seenUIDs))
	}
}

// =============================================================================
// Equality tests
// =============================================================================

func TestIdentity_EqualStructural(t *testing.T) {
	f, _ := newTestFactory(t)
	a, _ := f.Mint(MintOptions{
		Kind: AgentTypeArchitect,
		Pod:  PodRef{ID: "architect", Type: PodTypeSingleton},
	})
	b := *a
	if !a.Equal(&b) {
		t.Fatal("value-copy should be Equal")
	}
	// Distinct UID → not equal.
	c, _ := f.Mint(MintOptions{
		Kind: AgentTypeArchitect,
		Pod:  PodRef{ID: "architect", Type: PodTypeSingleton},
	})
	if a.Equal(c) {
		t.Fatal("different UID should not be Equal")
	}
}
