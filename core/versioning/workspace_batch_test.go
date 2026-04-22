package versioning

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/skills"
)

var _ = context.Background // keep context import even if indirectly used

// TestBuildWorkspaceBatchWriteOrder_MkdirBeforeChildren locks the core
// dependency invariant: when a batch contains both `mkdir X` and a file
// write inside X's tree, the mkdir must come first regardless of the
// LLM-supplied array order. Without this, the write would fail on a
// non-existent parent directory.
func TestBuildWorkspaceBatchWriteOrder_MkdirBeforeChildren(t *testing.T) {
	ops := []WorkspaceBatchWriteOp{
		{Op: "write", Path: "hello-cli/hello_cli/__init__.py", Content: ""},
		{Op: "write", Path: "hello-cli/hello_cli/cli.py", Content: "def main():\n    pass\n"},
		{Op: "mkdir", Path: "hello-cli/hello_cli"},
	}
	order, err := buildWorkspaceBatchWriteOrder(ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(order), len(ops); got != want {
		t.Fatalf("order length = %d, want %d", got, want)
	}
	// mkdir (idx=2) must come before both writes (idx=0, idx=1).
	mkdirPos := indexInOrder(order, 2)
	initPos := indexInOrder(order, 0)
	cliPos := indexInOrder(order, 1)
	if mkdirPos >= initPos {
		t.Errorf("mkdir at position %d must precede __init__.py write at %d", mkdirPos, initPos)
	}
	if mkdirPos >= cliPos {
		t.Errorf("mkdir at position %d must precede cli.py write at %d", mkdirPos, cliPos)
	}
}

// TestBuildWorkspaceBatchWriteOrder_SamePathPreservesSuppliedOrder
// covers the "multiple mutations on the same file" case: a write
// followed by an edit on the same path should execute in the
// LLM-supplied order, since the edit depends on the write's content.
func TestBuildWorkspaceBatchWriteOrder_SamePathPreservesSuppliedOrder(t *testing.T) {
	ops := []WorkspaceBatchWriteOp{
		{Op: "write", Path: "pkg/mod.py", Content: "a\nb\n"},
		{Op: "edit", Path: "pkg/mod.py", Edits: []any{map[string]string{"old_text": "a", "new_text": "A"}}},
	}
	order, err := buildWorkspaceBatchWriteOrder(ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := order, []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v — same-path ops must preserve supplied order", got, want)
	}
}

// TestBuildWorkspaceBatchWriteOrder_SiblingsOrderByDepthAndIndex
// verifies the tiebreaker for unrelated paths: shallower depth first,
// then LLM-supplied index for determinism.
func TestBuildWorkspaceBatchWriteOrder_SiblingsOrderByDepthAndIndex(t *testing.T) {
	ops := []WorkspaceBatchWriteOp{
		{Op: "write", Path: "deep/sub/nested.py", Content: ""},
		{Op: "write", Path: "top.py", Content: ""},
		{Op: "mkdir", Path: "deep/sub"},
		{Op: "mkdir", Path: "deep"},
	}
	order, err := buildWorkspaceBatchWriteOrder(ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expected: deep (idx 3) → deep/sub (idx 2) → top.py (idx 1)
	// OR:      deep (idx 3) → top.py (idx 1) → deep/sub (idx 2) — depth-tied with top.py
	// Finally: deep/sub/nested.py (idx 0) must come after its parents.
	deepPos := indexInOrder(order, 3)
	subPos := indexInOrder(order, 2)
	nestedPos := indexInOrder(order, 0)
	if deepPos >= subPos {
		t.Errorf("'deep' (pos %d) must precede 'deep/sub' (pos %d)", deepPos, subPos)
	}
	if subPos >= nestedPos {
		t.Errorf("'deep/sub' (pos %d) must precede 'deep/sub/nested.py' (pos %d)", subPos, nestedPos)
	}
}

// TestBuildWorkspaceBatchWriteOrder_EmptyPathRejected locks the
// validation gate: empty paths must be rejected at order-construction
// time so the LLM gets a top-level error before any op runs.
func TestBuildWorkspaceBatchWriteOrder_EmptyPathRejected(t *testing.T) {
	ops := []WorkspaceBatchWriteOp{
		{Op: "write", Path: "ok.py", Content: ""},
		{Op: "write", Path: "   ", Content: ""},
	}
	_, err := buildWorkspaceBatchWriteOrder(ops)
	if err == nil {
		t.Fatal("expected empty-path error")
	}
}

// TestIsWorkspaceAncestorPath covers the ancestor relationship helper.
// Mostly a smoke test of edge cases (root, trailing slashes, same
// path).
func TestIsWorkspaceAncestorPath(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"a", "a/b", true},
		{"a/b", "a/b/c", true},
		{"a/b/", "a/b/c", true},
		{"a", "a", false},
		{"a", "ab", false},
		{"a/b", "a/bc", false},
		{".", "anything", true},
		{"", "x", true},
	}
	for _, c := range cases {
		if got := isWorkspaceAncestorPath(c.a, c.b); got != c.want {
			t.Errorf("isWorkspaceAncestorPath(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestNormalizeWorkspaceBatchReadItems covers the two input-shape
// branches: flat paths list → defaults filled in per entry, vs items
// list → per-entry overrides preserved with batch-level defaults as
// fallback.
func TestNormalizeWorkspaceBatchReadItems(t *testing.T) {
	got := normalizeWorkspaceBatchReadItems(
		[]string{"a.py", " b.py "},
		[]WorkspaceBatchReadItem{
			{Path: "c.py", View: "pipeline"},
			{Path: ""}, // should be skipped
		},
		"disk", "pipeline_1", 0, 0,
		"", "",
	)
	if len(got) != 3 {
		t.Fatalf("want 3 items after normalization, got %d: %#v", len(got), got)
	}
	// First two entries inherit the defaults.
	if got[0].View != "disk" || got[0].PipelineID != "pipeline_1" {
		t.Errorf("flat-path entry did not inherit defaults: %#v", got[0])
	}
	// Trimmed path.
	if got[1].Path != "b.py" {
		t.Errorf("path not trimmed: %q", got[1].Path)
	}
	// Items-list entry keeps its override.
	if got[2].View != "pipeline" {
		t.Errorf("items override lost: %#v", got[2])
	}
}

func indexInOrder(order []int, idx int) int {
	for pos, v := range order {
		if v == idx {
			return pos
		}
	}
	return -1
}

// TestWorkspaceWriteSkill_BatchCreatesPackageEndToEnd drives the whole
// batched write path against a real SessionVFS: the LLM's "I want a
// hello-cli package" intent reduces to one tool call that produces
// mkdir + 2 file writes in the correct order with leases acquired on
// demand. The asserts mirror what the LLM would see on a real agent
// run.
func TestWorkspaceWriteSkill_BatchCreatesPackageEndToEnd(t *testing.T) {
	dir := t.TempDir()
	svfs, views, ctx := newWorkspaceWriteSkillHarness(t, dir)
	defer svfs.Close()
	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("begin pipeline: %v", err)
	}

	skill := NewWorkspaceWriteSkill(WorkspaceWriteSkillConfig{
		GetViews:          func() WorkspaceViewAccess { return views },
		GetFileAccess:     func() FileAccess { return svfs.NewPipelineFileAccess(pipe) },
		DefaultPipelineID: func() string { return "task-1" },
		Differ:            NewMyersDiffer(1),
	})

	input := map[string]any{
		"op":          "batch",
		"scope":       "pipeline",
		"pipeline_id": "task-1",
		"operations": []map[string]any{
			// Intentionally list children before the parent mkdir —
			// the DAG builder must reorder.
			{"op": "write", "path": "hello_cli/cli.py", "content": "def main():\n    print('hello world')\n"},
			{"op": "write", "path": "hello_cli/__init__.py", "content": ""},
			{"op": "mkdir", "path": "hello_cli"},
		},
	}
	raw, _ := json.Marshal(input)
	result, err := skill.Handler(ctx, raw)
	if err != nil {
		t.Fatalf("batch handler: %v", err)
	}
	response, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if response["op"] != "batch" {
		t.Errorf("op = %v, want batch", response["op"])
	}
	if response["success"] != true {
		t.Errorf("success = %v, want true; full response = %#v", response["success"], response)
	}
	opsAny, ok := response["operations"].([]WorkspaceBatchWriteResult)
	if !ok {
		t.Fatalf("operations type = %T, want []WorkspaceBatchWriteResult", response["operations"])
	}
	if got, want := len(opsAny), 3; got != want {
		t.Fatalf("operations count = %d, want %d", got, want)
	}
	for i, op := range opsAny {
		if !op.Success {
			t.Errorf("operations[%d] (op=%s, path=%s) failed: %s", i, op.Op, op.Path, op.Error)
		}
	}

	_ = pipe
	// Confirm the pipeline overlay actually contains the new files.
	cliContent, readErr := views.ReadFile(ctx, WorkspaceViewPipeline, "hello_cli/cli.py", "task-1")
	if readErr != nil {
		t.Fatalf("read cli.py: %v", readErr)
	}
	if !strings.Contains(string(cliContent), "def main") {
		t.Errorf("cli.py content = %q, expected def main(...)", string(cliContent))
	}
	initContent, readErr := views.ReadFile(ctx, WorkspaceViewPipeline, "hello_cli/__init__.py", "task-1")
	if readErr != nil {
		t.Fatalf("read __init__.py: %v", readErr)
	}
	if string(initContent) != "" {
		t.Errorf("__init__.py content = %q, expected empty", string(initContent))
	}
}

// TestWorkspaceWriteSkill_BatchAtomicAbortsOnFirstFailure locks the
// atomic=true contract: once one op fails, remaining ops are marked
// skipped_due_to_prior_failure rather than attempted. The failing op
// here is a write with an obviously-broken basis — any mismatched
// basis triggers the underlying stale-basis rejection.
func TestWorkspaceWriteSkill_BatchAtomicAbortsOnFirstFailure(t *testing.T) {
	dir := t.TempDir()
	svfs, views, ctx := newWorkspaceWriteSkillHarness(t, dir)
	defer svfs.Close()
	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("begin pipeline: %v", err)
	}

	skill := NewWorkspaceWriteSkill(WorkspaceWriteSkillConfig{
		GetViews:          func() WorkspaceViewAccess { return views },
		GetFileAccess:     func() FileAccess { return svfs.NewPipelineFileAccess(pipe) },
		DefaultPipelineID: func() string { return "task-1" },
		Differ:            NewMyersDiffer(1),
	})

	// Force a failure by supplying an unknown op name on a parent path,
	// with the dependent child op in a later layer. Atomic-mode
	// semantic: within a layer, all ops complete; BETWEEN layers, a
	// prior-layer failure skips subsequent layers. So the "should be
	// skipped" op must sit in a later layer than the failing one.
	input := map[string]any{
		"op":          "batch",
		"scope":       "pipeline",
		"pipeline_id": "task-1",
		"atomic":      true,
		"operations": []map[string]any{
			// Layer 0: fails (unknown op on dir).
			{"op": "nonsense_op", "path": "a"},
			// Layer 1: depends on layer 0 (child path); must be skipped.
			{"op": "write", "path": "a/child.py", "content": "should be skipped"},
		},
	}
	raw, _ := json.Marshal(input)
	result, err := skill.Handler(ctx, raw)
	if err != nil {
		t.Fatalf("batch handler: %v", err)
	}
	response := result.(map[string]any)
	if response["success"] != false {
		t.Errorf("success = %v, want false (atomic batch with a failure)", response["success"])
	}
	ops := response["operations"].([]WorkspaceBatchWriteResult)
	if len(ops) != 2 {
		t.Fatalf("expected 2 results, got %d", len(ops))
	}
	if ops[0].Success {
		t.Errorf("op[0] must fail for bogus op name; result = %+v", ops[0])
	}
	if !ops[1].SkippedDueToPriorFailure {
		t.Errorf("atomic: op[1].SkippedDueToPriorFailure = false; want true after op[0] failed (%s)", ops[0].Error)
	}
	if ops[1].Success {
		t.Errorf("op[1] should not have been executed when atomic abort triggered")
	}
}

// TestWorkspaceWriteSkill_BatchRejectsEmptyOperations locks the
// top-level validation: empty operations list is rejected before any
// dispatch, with a clear error message naming what's missing.
func TestWorkspaceWriteSkill_BatchRejectsEmptyOperations(t *testing.T) {
	dir := t.TempDir()
	svfs, views, ctx := newWorkspaceWriteSkillHarness(t, dir)
	defer svfs.Close()
	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("begin pipeline: %v", err)
	}

	skill := NewWorkspaceWriteSkill(WorkspaceWriteSkillConfig{
		GetViews:          func() WorkspaceViewAccess { return views },
		GetFileAccess:     func() FileAccess { return svfs.NewPipelineFileAccess(pipe) },
		DefaultPipelineID: func() string { return "task-1" },
		Differ:            NewMyersDiffer(1),
	})
	input := map[string]any{"op": "batch", "operations": []map[string]any{}}
	raw, _ := json.Marshal(input)
	_, err = skill.Handler(ctx, raw)
	if err == nil {
		t.Fatal("expected error for empty operations")
	}
	if !strings.Contains(err.Error(), "operations") {
		t.Errorf("error does not mention 'operations': %v", err)
	}
}

// TestWorkspaceBatchWriteLayers_IndependentOpsShareLayer locks the
// layering invariant that drives parallelism: ops with no path-
// dependency relationship (unrelated files, siblings under an existing
// parent, etc.) land in the same layer and can therefore dispatch
// concurrently. Ops with ancestor→descendant relationships land in
// strictly later layers.
func TestWorkspaceBatchWriteLayers_IndependentOpsShareLayer(t *testing.T) {
	ops := []WorkspaceBatchWriteOp{
		// Independent siblings — should share layer 0.
		{Op: "write", Path: "a.py", Content: ""},
		{Op: "write", Path: "b.py", Content: ""},
		// Parent dir before child; child must be in a later layer.
		{Op: "mkdir", Path: "pkg"},
		{Op: "write", Path: "pkg/mod.py", Content: ""},
		// Independent third file — also layer 0.
		{Op: "write", Path: "README.md", Content: ""},
	}
	layers, _, err := workspaceBatchWriteLayers(ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: layer 0 = {0, 1, 2, 4}; layer 1 = {3}.
	if len(layers) < 2 {
		t.Fatalf("want at least 2 layers, got %d: %v", len(layers), layers)
	}
	layer0 := map[int]bool{}
	for _, idx := range layers[0] {
		layer0[idx] = true
	}
	for _, want := range []int{0, 1, 2, 4} {
		if !layer0[want] {
			t.Errorf("expected op %d in layer 0; got layer 0 = %v", want, layers[0])
		}
	}
	pkgModLayer := -1
	for i, layer := range layers {
		for _, idx := range layer {
			if idx == 3 {
				pkgModLayer = i
			}
		}
	}
	if pkgModLayer <= 0 {
		t.Errorf("expected pkg/mod.py (op 3) in a later layer than pkg mkdir; got layer %d", pkgModLayer)
	}
}

// TestWorkspaceBatchWrite_SameLayerOpsRunConcurrently proves that when
// a GoroutineScope is stamped on context, ops within a DAG layer
// execute concurrently. The proof mechanism: each worker waits on a
// barrier channel that's only unblocked once all workers have entered
// — if execution were sequential, the first worker would block forever
// and the test would time out. Parallel execution completes because
// all workers reach the barrier together.
func TestWorkspaceBatchWrite_SameLayerOpsRunConcurrently(t *testing.T) {
	gscope := concurrency.NewGoroutineScope(context.Background(), "test", nil)
	defer func() { _ = gscope.Shutdown(1*time.Second, 2*time.Second) }()

	const concurrent = 3
	barrier := make(chan struct{})
	var entered atomic.Int32
	ran := make([]bool, concurrent)

	// Custom dispatcher: each op waits for all siblings to arrive at
	// the barrier before completing. If the runtime is sequential, the
	// first call blocks on the barrier forever and the test times out.
	dispatcher := func(ctx context.Context, op string, scope WorkspaceWriteScope, input json.RawMessage) (any, error) {
		var probe struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(input, &probe)
		idx := indexFromPath(probe.Path)
		ran[idx] = true
		if entered.Add(1) == concurrent {
			close(barrier)
		}
		select {
		case <-barrier:
			return map[string]any{"ok": true, "path": probe.Path}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
			return nil, context.DeadlineExceeded
		}
	}

	ops := []WorkspaceBatchWriteOp{
		{Op: "write", Path: "leg0.py", Basis: mockBasis("leg0.py"), Content: "x"},
		{Op: "write", Path: "leg1.py", Basis: mockBasis("leg1.py"), Content: "x"},
		{Op: "write", Path: "leg2.py", Basis: mockBasis("leg2.py"), Content: "x"},
	}
	layers, _, err := workspaceBatchWriteLayers(ops)
	if err != nil {
		t.Fatalf("workspaceBatchWriteLayers: %v", err)
	}
	if len(layers) != 1 || len(layers[0]) != concurrent {
		t.Fatalf("expected one layer with %d ops; got layers=%v", concurrent, layers)
	}

	results := make([]WorkspaceBatchWriteResult, concurrent)
	ctx := concurrency.WithScope(context.Background(), gscope)

	done := make(chan struct{})
	go func() {
		executeWorkspaceBatchWriteLayer(ctx, gscope, dispatcher, nil, nil, WorkspaceWriteScopePipeline, "", ops, layers[0], results)
		close(done)
	}()

	select {
	case <-done:
		// All three entered the barrier concurrently — parallel execution confirmed.
	case <-time.After(2 * time.Second):
		t.Fatal("layer did not complete within 2s — ops likely ran sequentially (would deadlock on barrier)")
	}
	for i, r := range results {
		if !r.Success {
			t.Errorf("op %d failed: %s", i, r.Error)
		}
	}
}

// TestWorkspaceBatchRead_ParallelFanOut proves the read-batch handler
// runs per-path reads concurrently when a scope is available. Same
// barrier pattern as the write test — if serial, timeout; if parallel,
// all reads enter before any completes.
func TestWorkspaceBatchRead_ParallelFanOut(t *testing.T) {
	gscope := concurrency.NewGoroutineScope(context.Background(), "test", nil)
	defer func() { _ = gscope.Shutdown(1*time.Second, 2*time.Second) }()

	const concurrent = 4
	barrier := make(chan struct{})
	var entered atomic.Int32

	// A minimal reader skill that blocks on the barrier so we can
	// observe concurrency. Only the Handler is used.
	reader := skills.NewSkill("test_reader").
		StringParam("path", "", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var probe struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(input, &probe)
			if entered.Add(1) == concurrent {
				close(barrier)
			}
			select {
			case <-barrier:
				return map[string]any{"path": probe.Path, "content": "ok"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(3 * time.Second):
				return nil, context.DeadlineExceeded
			}
		}).Build()

	items := []WorkspaceBatchReadItem{
		{Path: "a"}, {Path: "b"}, {Path: "c"}, {Path: "d"},
	}
	results := make([]WorkspaceBatchReadResult, concurrent)

	ctx := concurrency.WithScope(context.Background(), gscope)
	done := make(chan struct{})
	go func() {
		executeWorkspaceBatchReadLegs(ctx, reader, items, results)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reads did not complete within 2s — likely serial")
	}
	for i, r := range results {
		if r.Status != "ok" {
			t.Errorf("result %d status = %q, want ok (error=%q)", i, r.Status, r.Error)
		}
	}
}

// TestWorkspaceBatchRead_SequentialWithoutScope confirms the no-scope
// fallback: when ctx carries no GoroutineScope (tests, unwired callers),
// reads execute sequentially. No untracked goroutines are spawned.
func TestWorkspaceBatchRead_SequentialWithoutScope(t *testing.T) {
	var activeConcurrent int32
	var maxConcurrent int32
	var mu sync.Mutex

	reader := skills.NewSkill("test_reader").
		StringParam("path", "", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			cur := atomic.AddInt32(&activeConcurrent, 1)
			mu.Lock()
			if cur > maxConcurrent {
				maxConcurrent = cur
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&activeConcurrent, -1)
			return map[string]any{"content": "ok"}, nil
		}).Build()

	items := []WorkspaceBatchReadItem{{Path: "a"}, {Path: "b"}, {Path: "c"}}
	results := make([]WorkspaceBatchReadResult, len(items))
	executeWorkspaceBatchReadLegs(context.Background(), reader, items, results) // no scope on ctx

	if maxConcurrent != 1 {
		t.Errorf("without scope, reads must run sequentially; max concurrent = %d, want 1", maxConcurrent)
	}
}

// mockBasis returns a minimally-populated basis for tests that exercise
// the dispatcher path without going through a real prepare_write.
func mockBasis(path string) *WorkspaceWriteBasis {
	return &WorkspaceWriteBasis{
		Scope:          WorkspaceWriteScopePipeline,
		Path:           path,
		TargetView:     WorkspaceViewPipeline,
		PreparedAt:     time.Now().UTC(),
		LeaseExpiresAt: time.Now().UTC().Add(1 * time.Minute),
	}
}

// indexFromPath extracts the trailing integer from paths like "leg0.py"
// so the concurrency-barrier test can write results into predictable
// slots.
func indexFromPath(p string) int {
	for _, c := range p {
		if c >= '0' && c <= '9' {
			return int(c - '0')
		}
	}
	return 0
}

// TestWorkspaceWriteSkill_BatchCapEnforced locks the per-batch cap so
// one absurd batch can't blow through the lease machinery.
func TestWorkspaceWriteSkill_BatchCapEnforced(t *testing.T) {
	dir := t.TempDir()
	svfs, views, ctx := newWorkspaceWriteSkillHarness(t, dir)
	defer svfs.Close()
	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("begin pipeline: %v", err)
	}

	skill := NewWorkspaceWriteSkill(WorkspaceWriteSkillConfig{
		GetViews:          func() WorkspaceViewAccess { return views },
		GetFileAccess:     func() FileAccess { return svfs.NewPipelineFileAccess(pipe) },
		DefaultPipelineID: func() string { return "task-1" },
		Differ:            NewMyersDiffer(1),
	})
	ops := make([]map[string]any, workspaceBatchWriteMaxOps+1)
	for i := range ops {
		ops[i] = map[string]any{"op": "write", "path": "x.py", "content": ""}
	}
	input := map[string]any{"op": "batch", "operations": ops}
	raw, _ := json.Marshal(input)
	_, err = skill.Handler(ctx, raw)
	if err == nil {
		t.Fatal("expected error for over-cap batch")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("error does not mention 'cap': %v", err)
	}
}
