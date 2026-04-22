package versioning

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/skills"
)

// workspaceBatchWorkerTimeout is the per-op goroutine timeout
// propagated to tracked workers. Workspace I/O is typically sub-second;
// the 60s ceiling keeps a pathologically-stuck worker from holding a
// scope slot forever without making healthy ops feel bounded.
const workspaceBatchWorkerTimeout = 60 * time.Second

// ─── Batched workspace operations ────────────────────────────────────
//
// Batched variants collapse N individual read/prepare/write tool calls
// into a single tool call so the LLM can express the full set of
// intended operations at once. The runtime handles ordering, lease
// acquisition, and per-op result composition — the LLM sees one
// request-response round trip instead of N.
//
// Three batched ops are wired into the existing unified workspace
// skills (no new top-level tool; the catalog stays compact):
//
//   - workspace_read(op=batch, paths=[...]) — parallel reads
//   - workspace_read(op=prepare_write_batch, paths=[...]) — bulk lease
//   - workspace_write(op=batch, operations=[...]) — DAG-ordered writes
//
// Partial failure is reported as a structured per-op result array
// rather than as a single "batch failed" error, so the LLM can see
// which operations succeeded and recover only the ones that didn't.
// Atomicity is opt-in via atomic=true on the write batch.
//
// Size-cap: reads default to a 2 MiB aggregate response budget. When
// exceeded, the response returns success=false plus the set of paths
// that overflowed the budget so the LLM can re-batch at a smaller
// granularity. Write batches have a per-op cap (64 operations) so a
// runaway plan fails fast.

// workspaceBatchReadDefaultBudgetBytes is the aggregate response
// budget for workspace_read(op=batch) before the handler truncates.
// Sized to fit in roughly one LLM turn's context for Claude models
// (1 MiB ≈ 250k tokens at 4 chars/token, with headroom).
const workspaceBatchReadDefaultBudgetBytes = 2 * 1024 * 1024

// workspaceBatchWriteMaxOps caps the number of operations per
// write batch. Above this, the LLM should split the batch; trying
// to acquire 100+ leases in one shot is an anti-pattern because
// the failure blast radius grows linearly with batch size.
const workspaceBatchWriteMaxOps = 64

// WorkspaceBatchReadItem is a single entry in a workspace_read(op=batch)
// request. Only path is required; the per-item view/pipeline_id/offset/
// limit override the batch-level defaults when set.
type WorkspaceBatchReadItem struct {
	Path       string `json:"path"`
	View       string `json:"view,omitempty"`
	PipelineID string `json:"pipeline_id,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// WorkspaceBatchReadResult is a single entry in the response array.
// Exactly one of Content, Missing, or Error is populated; the LLM
// switches on the Status field to decide which to read.
type WorkspaceBatchReadResult struct {
	Path    string `json:"path"`
	View    string `json:"view,omitempty"`
	Status  string `json:"status"` // ok | missing | error | skipped_budget
	Content any    `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
}

// WorkspaceBatchWriteOp is a single entry in a workspace_write(op=batch)
// request. The op field is the per-entry verb — a batch may mix
// write/edit/delete/mkdir freely; the DAG builder infers ordering from
// paths alone. Basis is optional: when absent, the handler acquires a
// fresh lease internally via the same prepare_write path the LLM would
// have invoked itself.
type WorkspaceBatchWriteOp struct {
	Op      string               `json:"op"`
	Path    string               `json:"path"`
	Content string               `json:"content,omitempty"`
	Edits   []any                `json:"edits,omitempty"`
	Basis   *WorkspaceWriteBasis `json:"basis,omitempty"`
}

// WorkspaceBatchWriteResult is a single entry in the response array.
// Success ops carry NextBasis so the LLM can chain further ops on the
// same path without re-acquiring a lease. Failed ops carry Error and
// optionally SkippedDueToPriorFailure when atomic=true and an earlier
// op aborted the batch.
type WorkspaceBatchWriteResult struct {
	Op                      string               `json:"op"`
	Path                    string               `json:"path"`
	Success                 bool                 `json:"success"`
	NextBasis               *WorkspaceWriteBasis `json:"next_basis,omitempty"`
	Error                   string               `json:"error,omitempty"`
	SkippedDueToPriorFailure bool                `json:"skipped_due_to_prior_failure,omitempty"`
	Result                  any                  `json:"result,omitempty"`
}

// ─── Batch handlers ──────────────────────────────────────────────────

// handleWorkspaceReadBatch fans out the batch to the individual read
// handler. When the context carries a GoroutineScope, all reads dispatch
// concurrently through scope.Go for tracked parallelism — per-op workers
// are registered with the scope so leak detection, budgeting, and
// lifetime enforcement still apply. The aggregate response budget is
// computed after every item returns; items past the cap are rewritten
// to status=skipped_budget so the LLM knows which paths to re-batch.
//
// Without a scope (tests, unwired callers), falls back to a sequential
// loop. No untracked goroutine is ever spawned.
func handleWorkspaceReadBatch(
	ctx context.Context,
	reader *skills.Skill,
	defaultView string,
	defaultPipelineID string,
	input json.RawMessage,
) (any, error) {
	if reader == nil {
		return nil, fmt.Errorf("workspace_read batch: underlying reader is not configured")
	}
	var params struct {
		Paths      []string                 `json:"paths,omitempty"`
		Items      []WorkspaceBatchReadItem `json:"items,omitempty"`
		View       string                   `json:"view,omitempty"`
		PipelineID string                   `json:"pipeline_id,omitempty"`
		Offset     int                      `json:"offset,omitempty"`
		Limit      int                      `json:"limit,omitempty"`
		Budget     int                      `json:"budget_bytes,omitempty"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	items := normalizeWorkspaceBatchReadItems(params.Paths, params.Items, params.View, params.PipelineID, params.Offset, params.Limit, defaultView, defaultPipelineID)
	if len(items) == 0 {
		return nil, fmt.Errorf("workspace_read(op=batch): at least one path (or items entry) is required")
	}
	budget := params.Budget
	if budget <= 0 {
		budget = workspaceBatchReadDefaultBudgetBytes
	}

	results := make([]WorkspaceBatchReadResult, len(items))
	executeWorkspaceBatchReadLegs(ctx, reader, items, results)
	used := applyWorkspaceBatchReadBudget(items, results, budget)

	return map[string]any{
		"op":           "batch",
		"results":      results,
		"count":        len(results),
		"budget_bytes": budget,
		"budget_used":  used,
	}, nil
}

// executeWorkspaceBatchReadLegs runs each read leg and writes its
// result into results[i]. When ctx carries a GoroutineScope, legs
// dispatch concurrently through scope.Go; otherwise the function
// executes sequentially.
//
// Result-ordering invariant: results[i] always corresponds to items[i]
// regardless of execution order, because each worker writes only into
// its own index slot — no shared mutable state across workers.
func executeWorkspaceBatchReadLegs(
	ctx context.Context,
	reader *skills.Skill,
	items []WorkspaceBatchReadItem,
	results []WorkspaceBatchReadResult,
) {
	scope := concurrency.ScopeFromContext(ctx)
	if scope == nil || len(items) <= 1 {
		for i := range items {
			results[i] = executeWorkspaceBatchReadLeg(ctx, reader, items[i])
		}
		return
	}
	var wg sync.WaitGroup
	for i := range items {
		idx := i
		item := items[idx]
		wg.Add(1)
		goErr := scope.Go(
			"workspace_read_batch_leg",
			workspaceBatchWorkerTimeout,
			func(workerCtx context.Context) error {
				defer wg.Done()
				results[idx] = executeWorkspaceBatchReadLeg(workerCtx, reader, item)
				return nil
			},
		)
		if goErr != nil {
			// Scope is shutting down or budget exhausted — fall back
			// to synchronous execution for this leg so we never drop a
			// result. The WaitGroup counter balances via the sync leg.
			results[idx] = executeWorkspaceBatchReadLeg(ctx, reader, item)
			wg.Done()
		}
	}
	wg.Wait()
}

// executeWorkspaceBatchReadLeg runs a single read leg and constructs
// the result row. Pure function: no shared state, no side effects on
// anything outside the returned value. Safe to call from concurrent
// scope workers.
func executeWorkspaceBatchReadLeg(
	ctx context.Context,
	reader *skills.Skill,
	item WorkspaceBatchReadItem,
) WorkspaceBatchReadResult {
	perItemInput, err := json.Marshal(item)
	if err != nil {
		return WorkspaceBatchReadResult{Path: item.Path, View: item.View, Status: "error", Error: err.Error()}
	}
	result, err := reader.Handler(ctx, perItemInput)
	if err != nil {
		return WorkspaceBatchReadResult{Path: item.Path, View: item.View, Status: "error", Error: err.Error()}
	}
	entry := WorkspaceBatchReadResult{Path: item.Path, View: item.View, Status: "ok", Content: result}
	entry.Status, entry.Bytes = classifyWorkspaceBatchReadResult(result)
	return entry
}

// applyWorkspaceBatchReadBudget walks the results in input order,
// zeros-out entries whose cumulative byte count exceeds the budget
// (rewriting them to status=skipped_budget with an explanation), and
// returns the used-byte total. Budget enforcement is deterministic
// against input order — not execution order — so re-running the same
// batch twice produces the same cut point regardless of how the
// parallel workers interleaved.
func applyWorkspaceBatchReadBudget(items []WorkspaceBatchReadItem, results []WorkspaceBatchReadResult, budget int) int {
	used := 0
	for i := range results {
		if used >= budget {
			results[i] = WorkspaceBatchReadResult{
				Path:   items[i].Path,
				View:   items[i].View,
				Status: "skipped_budget",
				Error:  fmt.Sprintf("response budget %d bytes exhausted before reading this path; re-batch with fewer paths or raise budget_bytes", budget),
			}
			continue
		}
		used += results[i].Bytes
	}
	return used
}

// handleWorkspaceReadPrepareWriteBatch acquires write leases for every
// path in the batch. Acquisition is atomic: if any prepare fails, the
// entire batch fails and the handler returns an error naming the first
// offending path. Partial-success is intentionally not supported — a
// half-acquired basis set is useless to the LLM.
//
// When ctx carries a GoroutineScope, all prepares dispatch concurrently
// through scope.Go. Results are collected by input index so the
// returned `prepared` array still matches the `paths` array one-to-one.
// Fail-fast: after all workers complete, the handler inspects errors
// in input order and returns the first one found. Later ones are
// discarded (the LLM only needs to know the batch failed + why). All
// goroutines are tracked — leak detection, budgeting, and lifetime
// enforcement apply as they would to any agent-owned work.
func handleWorkspaceReadPrepareWriteBatch(
	ctx context.Context,
	preparePipeline, prepareGlobal *skills.Skill,
	defaultScope WorkspaceWriteScope,
	defaultPipelineID string,
	input json.RawMessage,
) (any, error) {
	var params struct {
		Paths      []string `json:"paths,omitempty"`
		Scope      string   `json:"scope,omitempty"`
		PipelineID string   `json:"pipeline_id,omitempty"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if len(params.Paths) == 0 {
		return nil, fmt.Errorf("workspace_read(op=prepare_write_batch): at least one path is required")
	}
	scope := WorkspaceWriteScope(strings.TrimSpace(params.Scope))
	if scope == "" {
		scope = defaultScope
	}
	if scope == "" {
		scope = WorkspaceWriteScopePipeline
	}
	prepare := preparePipeline
	if scope == WorkspaceWriteScopeGlobal {
		prepare = prepareGlobal
	}
	if prepare == nil {
		return nil, fmt.Errorf("workspace_read(op=prepare_write_batch): %s scope is not configured", scope)
	}

	// Build per-leg inputs up-front (validates all paths before any
	// goroutine is spawned).
	pipelineID := strings.TrimSpace(params.PipelineID)
	if pipelineID == "" {
		pipelineID = strings.TrimSpace(defaultPipelineID)
	}
	legs := make([][]byte, len(params.Paths))
	for i, p := range params.Paths {
		target := strings.TrimSpace(p)
		if target == "" {
			return nil, fmt.Errorf("workspace_read(op=prepare_write_batch): paths[%d] is empty", i)
		}
		legPayload := map[string]any{"path": target}
		if pipelineID != "" {
			legPayload["pipeline_id"] = pipelineID
		}
		raw, err := json.Marshal(legPayload)
		if err != nil {
			return nil, fmt.Errorf("workspace_read(op=prepare_write_batch): encode leg for %q: %w", target, err)
		}
		legs[i] = raw
	}

	prepared := make([]any, len(params.Paths))
	errs := make([]error, len(params.Paths))
	gscope := concurrency.ScopeFromContext(ctx)
	if gscope == nil || len(legs) <= 1 {
		for i, raw := range legs {
			prepared[i], errs[i] = prepare.Handler(ctx, raw)
		}
	} else {
		var wg sync.WaitGroup
		for i := range legs {
			idx := i
			raw := legs[idx]
			wg.Add(1)
			goErr := gscope.Go(
				"workspace_prepare_write_batch_leg",
				workspaceBatchWorkerTimeout,
				func(workerCtx context.Context) error {
					defer wg.Done()
					prepared[idx], errs[idx] = prepare.Handler(workerCtx, raw)
					return nil
				},
			)
			if goErr != nil {
				prepared[idx], errs[idx] = prepare.Handler(ctx, raw)
				wg.Done()
			}
		}
		wg.Wait()
	}

	// Fail-fast in input order so "the first offending path" is
	// deterministic, independent of which worker failed first.
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("workspace_read(op=prepare_write_batch): prepare for %q failed: %w (batch aborted; no partial leases retained)", strings.TrimSpace(params.Paths[i]), err)
		}
	}
	return map[string]any{
		"op":       "prepare_write_batch",
		"scope":    string(scope),
		"count":    len(prepared),
		"prepared": prepared,
	}, nil
}

// handleWorkspaceWriteBatch executes a set of write/edit/delete/mkdir
// ops in DAG order, acquiring leases as needed. Ordering rules are
// purely path-based (see buildWorkspaceBatchWriteOrder) so the LLM
// never has to specify a sequence.
//
// Failure semantics:
//   - atomic=false (default): every op is attempted; per-op results
//     carry success/error individually. The handler does NOT roll back
//     earlier successes — the LLM reads the result array and decides.
//   - atomic=true: on first failure, remaining ops are marked
//     skipped_due_to_prior_failure and the handler returns success=false
//     at the top level. No automatic rollback of prior ops (the existing
//     write handlers don't expose a revert primitive); the LLM is
//     expected to emit a follow-up batch that undoes what was written.
//     True transactional rollback is a future enhancement.
func handleWorkspaceWriteBatch(
	ctx context.Context,
	perOp workspaceBatchWriteDispatcher,
	preparePipeline, prepareGlobal *skills.Skill,
	defaultPipelineID string,
	input json.RawMessage,
) (any, error) {
	var params struct {
		Scope      string                  `json:"scope,omitempty"`
		PipelineID string                  `json:"pipeline_id,omitempty"`
		Operations []WorkspaceBatchWriteOp `json:"operations"`
		Atomic     bool                    `json:"atomic,omitempty"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if len(params.Operations) == 0 {
		return nil, fmt.Errorf("workspace_write(op=batch): operations is required and must contain at least one entry")
	}
	if len(params.Operations) > workspaceBatchWriteMaxOps {
		return nil, fmt.Errorf("workspace_write(op=batch): %d operations exceeds the per-batch cap of %d — split into smaller batches", len(params.Operations), workspaceBatchWriteMaxOps)
	}
	scope := WorkspaceWriteScope(strings.TrimSpace(params.Scope))
	if scope == "" {
		scope = WorkspaceWriteScopePipeline
	}
	pipelineID := strings.TrimSpace(params.PipelineID)
	if pipelineID == "" {
		pipelineID = strings.TrimSpace(defaultPipelineID)
	}

	layers, _, err := workspaceBatchWriteLayers(params.Operations)
	if err != nil {
		return nil, err
	}

	results := make([]WorkspaceBatchWriteResult, len(params.Operations))
	gscope := concurrency.ScopeFromContext(ctx)
	success, _ := executeWorkspaceBatchWriteLayers(
		ctx,
		gscope,
		perOp,
		preparePipeline, prepareGlobal,
		scope, pipelineID,
		params.Operations, layers, params.Atomic,
		results,
	)
	return map[string]any{
		"op":         "batch",
		"scope":      string(scope),
		"success":    success,
		"atomic":     params.Atomic,
		"count":      len(params.Operations),
		"operations": results,
	}, nil
}

// executeWorkspaceBatchWriteLayers executes the layered DAG. Each layer
// runs concurrently when a GoroutineScope is available; ops within a
// layer are independent by construction of the layer assignment. Layers
// themselves are strictly sequential — layer L+1 starts only after
// every op in layer L finishes, so a mkdir in layer L that a write in
// layer L+1 depends on is always visible.
//
// Atomic mode: if any op in a layer fails, remaining layers are skipped
// (their ops filled with SkippedDueToPriorFailure=true). Ops that had
// already been dispatched in the failing layer complete normally; the
// goroutine scope is not cancelled, so workers are never abandoned mid-
// flight.
//
// Non-atomic mode: every layer runs to completion and per-op failures
// are simply recorded. Subsequent layers still execute — the LLM sees
// a complete per-op success map and decides how to recover.
//
// Returns (batchSuccess, firstLayerWithFailure). The second value is
// used by tests; production callers only look at batchSuccess.
func executeWorkspaceBatchWriteLayers(
	ctx context.Context,
	gscope *concurrency.GoroutineScope,
	perOp workspaceBatchWriteDispatcher,
	preparePipeline, prepareGlobal *skills.Skill,
	scope WorkspaceWriteScope,
	pipelineID string,
	ops []WorkspaceBatchWriteOp,
	layers [][]int,
	atomic bool,
	results []WorkspaceBatchWriteResult,
) (bool, int) {
	batchSuccess := true
	firstFailureLayer := -1
	for layerIdx, layerOps := range layers {
		if atomic && firstFailureLayer >= 0 {
			// Prior layer failed in atomic mode — mark remaining ops
			// as skipped without attempting them.
			for _, idx := range layerOps {
				results[idx] = WorkspaceBatchWriteResult{
					Op:                       ops[idx].Op,
					Path:                     ops[idx].Path,
					Success:                  false,
					Error:                    "skipped: a prior op in this atomic batch failed",
					SkippedDueToPriorFailure: true,
				}
			}
			continue
		}
		executeWorkspaceBatchWriteLayer(ctx, gscope, perOp, preparePipeline, prepareGlobal, scope, pipelineID, ops, layerOps, results)
		// Record per-layer outcome.
		for _, idx := range layerOps {
			if !results[idx].Success {
				batchSuccess = false
				if firstFailureLayer < 0 {
					firstFailureLayer = layerIdx
				}
			}
		}
	}
	return batchSuccess, firstFailureLayer
}

// executeWorkspaceBatchWriteLayer dispatches one layer's ops, either
// concurrently (scope available) or sequentially (scope nil or single
// op). All workers write their result into their own index slot; no
// shared mutable state crosses goroutines.
func executeWorkspaceBatchWriteLayer(
	ctx context.Context,
	gscope *concurrency.GoroutineScope,
	perOp workspaceBatchWriteDispatcher,
	preparePipeline, prepareGlobal *skills.Skill,
	scope WorkspaceWriteScope,
	pipelineID string,
	ops []WorkspaceBatchWriteOp,
	layerOps []int,
	results []WorkspaceBatchWriteResult,
) {
	if gscope == nil || len(layerOps) <= 1 {
		for _, idx := range layerOps {
			results[idx], _ = executeWorkspaceBatchWriteOp(ctx, perOp, preparePipeline, prepareGlobal, scope, pipelineID, ops[idx])
		}
		return
	}
	var wg sync.WaitGroup
	for _, idx := range layerOps {
		i := idx
		op := ops[i]
		wg.Add(1)
		goErr := gscope.Go(
			"workspace_write_batch_leg",
			workspaceBatchWorkerTimeout,
			func(workerCtx context.Context) error {
				defer wg.Done()
				results[i], _ = executeWorkspaceBatchWriteOp(workerCtx, perOp, preparePipeline, prepareGlobal, scope, pipelineID, op)
				return nil
			},
		)
		if goErr != nil {
			results[i], _ = executeWorkspaceBatchWriteOp(ctx, perOp, preparePipeline, prepareGlobal, scope, pipelineID, op)
			wg.Done()
		}
	}
	wg.Wait()
}

// workspaceBatchWriteDispatcher is the minimal surface handleWorkspace-
// WriteBatch needs from the unified workspace_write skill: given an op
// + scope, invoke the right underlying per-op handler with the input
// payload. Defined as a type so the skill builder can inject a closure
// that reuses the already-built per-op handlers instead of re-building
// them here.
type workspaceBatchWriteDispatcher func(ctx context.Context, op string, scope WorkspaceWriteScope, input json.RawMessage) (any, error)

// executeWorkspaceBatchWriteOp runs one op within a batch: resolves a
// basis if the op didn't supply one, invokes the per-op dispatcher, and
// populates the result row. The returned error is logged only; the
// batch as a whole reports per-op errors via the result array rather
// than propagating a single error up.
func executeWorkspaceBatchWriteOp(
	ctx context.Context,
	dispatcher workspaceBatchWriteDispatcher,
	preparePipeline, prepareGlobal *skills.Skill,
	scope WorkspaceWriteScope,
	pipelineID string,
	op WorkspaceBatchWriteOp,
) (WorkspaceBatchWriteResult, error) {
	trimmedPath := strings.TrimSpace(op.Path)
	if trimmedPath == "" {
		return WorkspaceBatchWriteResult{Op: op.Op, Success: false, Error: "operations[*].path is required"}, nil
	}
	basis := op.Basis
	if basis == nil {
		acquired, prepErr := acquireWorkspaceBatchBasis(ctx, preparePipeline, prepareGlobal, scope, trimmedPath, pipelineID)
		if prepErr != nil {
			return WorkspaceBatchWriteResult{Op: op.Op, Path: trimmedPath, Success: false, Error: fmt.Sprintf("auto-prepare basis failed: %s", prepErr.Error())}, prepErr
		}
		basis = acquired
	}
	opInput := map[string]any{
		"op":    op.Op,
		"scope": string(scope),
		"path":  trimmedPath,
		"basis": basis,
	}
	if strings.TrimSpace(pipelineID) != "" {
		opInput["pipeline_id"] = strings.TrimSpace(pipelineID)
	}
	if op.Content != "" {
		opInput["content"] = op.Content
	}
	if len(op.Edits) > 0 {
		opInput["edits"] = op.Edits
	}
	raw, encErr := json.Marshal(opInput)
	if encErr != nil {
		return WorkspaceBatchWriteResult{Op: op.Op, Path: trimmedPath, Success: false, Error: fmt.Sprintf("encode op: %s", encErr.Error())}, encErr
	}
	result, dispatchErr := dispatcher(ctx, op.Op, scope, raw)
	if dispatchErr != nil {
		return WorkspaceBatchWriteResult{Op: op.Op, Path: trimmedPath, Success: false, Error: dispatchErr.Error()}, dispatchErr
	}
	return WorkspaceBatchWriteResult{
		Op:        op.Op,
		Path:      trimmedPath,
		Success:   true,
		NextBasis: extractNextBasis(result),
		Result:    result,
	}, nil
}

// acquireWorkspaceBatchBasis invokes the matching prepare_write handler
// to obtain a fresh basis for a single path in a batch. Extracted so
// both the write handler and the bulk prepare helper can share it.
func acquireWorkspaceBatchBasis(
	ctx context.Context,
	preparePipeline, prepareGlobal *skills.Skill,
	scope WorkspaceWriteScope,
	target string,
	pipelineID string,
) (*WorkspaceWriteBasis, error) {
	prepare := preparePipeline
	if scope == WorkspaceWriteScopeGlobal {
		prepare = prepareGlobal
	}
	if prepare == nil {
		return nil, fmt.Errorf("no prepare handler configured for scope %q", scope)
	}
	leg := map[string]any{"path": target}
	if strings.TrimSpace(pipelineID) != "" {
		leg["pipeline_id"] = strings.TrimSpace(pipelineID)
	}
	raw, err := json.Marshal(leg)
	if err != nil {
		return nil, fmt.Errorf("encode prepare leg: %w", err)
	}
	result, err := prepare.Handler(ctx, raw)
	if err != nil {
		return nil, err
	}
	return extractBasisFromPrepareResult(result)
}

// extractBasisFromPrepareResult pulls the WorkspaceWriteBasis out of a
// PreparedWorkspaceWriteContext result. Both typed and map[string]any
// shapes are tolerated so the helper works against handlers that marshal
// through json.RawMessage intermediaries.
func extractBasisFromPrepareResult(result any) (*WorkspaceWriteBasis, error) {
	if typed, ok := result.(PreparedWorkspaceWriteContext); ok {
		clone := typed.Basis
		return &clone, nil
	}
	if typed, ok := result.(*PreparedWorkspaceWriteContext); ok && typed != nil {
		clone := typed.Basis
		return &clone, nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal prepare result: %w", err)
	}
	var decoded struct {
		Basis WorkspaceWriteBasis `json:"basis"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode prepare result: %w", err)
	}
	if strings.TrimSpace(string(decoded.Basis.Scope)) == "" {
		return nil, fmt.Errorf("prepare result did not contain a basis")
	}
	return &decoded.Basis, nil
}

// extractNextBasis pulls a NextBasis out of a per-op write result so
// the batch response can surface it for chained calls. Returns nil when
// the underlying handler didn't emit one (delete/mkdir may legitimately
// skip it).
func extractNextBasis(result any) *WorkspaceWriteBasis {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	var decoded struct {
		NextBasis *WorkspaceWriteBasis `json:"next_basis,omitempty"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	return decoded.NextBasis
}

// ─── DAG layering ────────────────────────────────────────────────────
//
// Write-batch ops are layered by path dependency rather than by LLM-
// supplied sequence. Rules:
//
//   - mkdir X (or any op on X) must precede any op whose path is a
//     strict descendant of X.
//   - For any two ops on the same path, the LLM-supplied order within
//     the batch is preserved (e.g. write then edit on the same file).
//   - Ops that share no dependency relationship may execute
//     concurrently. That's the whole point of layering: ops in the
//     same layer are independent, so a tracked GoroutineScope can
//     dispatch them in parallel.
//
// Layer assignment: for each op, its layer is 1 + max(layer of any op
// it depends on), or 0 if it has no dependencies. Cycles are
// impossible by construction — every dependency edge points from a
// path that is either strictly ancestral to, or identical to at an
// earlier input index, another path. Both relations are well-founded.

// workspaceBatchWriteLayers returns (layers, flatOrder, err).
//
// `layers[i]` is the set of op indices in layer i; ops within a layer
// are concurrently runnable. `flatOrder` is a deterministic linear
// flattening of the layers that respects dependencies — used by
// sequential execution paths and by regression tests that assert a
// stable ordering. When any operation has an empty path, the function
// returns an error with the offending index.
//
// The caller decides whether to iterate the flat order (no concurrency)
// or dispatch each layer in parallel.
func workspaceBatchWriteLayers(ops []WorkspaceBatchWriteOp) ([][]int, []int, error) {
	n := len(ops)
	if n == 0 {
		return nil, nil, nil
	}
	cleanPaths := make([]string, n)
	for i, op := range ops {
		trimmed := strings.TrimSpace(op.Path)
		if trimmed == "" {
			return nil, nil, fmt.Errorf("workspace_write(op=batch): operations[%d].path is required", i)
		}
		cleanPaths[i] = path.Clean(trimmed)
	}

	// Dependency edges: for each op i, the set of j that must precede it.
	deps := make([][]int, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			if isWorkspaceAncestorPath(cleanPaths[j], cleanPaths[i]) {
				deps[i] = append(deps[i], j)
				continue
			}
			if cleanPaths[j] == cleanPaths[i] && j < i {
				deps[i] = append(deps[i], j)
			}
		}
	}

	// Compute each op's layer: memoized DFS so depth of each op is
	// 1 + max over its dependencies. Deterministic.
	layer := make([]int, n)
	for i := range layer {
		layer[i] = -1
	}
	var compute func(int) int
	compute = func(i int) int {
		if layer[i] >= 0 {
			return layer[i]
		}
		max := -1
		for _, j := range deps[i] {
			if d := compute(j); d > max {
				max = d
			}
		}
		layer[i] = max + 1
		return layer[i]
	}
	maxLayer := -1
	for i := 0; i < n; i++ {
		l := compute(i)
		if l > maxLayer {
			maxLayer = l
		}
	}

	// Group indices by layer. Within a layer, preserve LLM-supplied
	// index order so flat-order flattening is deterministic.
	layers := make([][]int, maxLayer+1)
	for i := 0; i < n; i++ {
		layers[layer[i]] = append(layers[layer[i]], i)
	}
	for _, bucket := range layers {
		sort.Ints(bucket)
	}
	flat := make([]int, 0, n)
	for _, bucket := range layers {
		flat = append(flat, bucket...)
	}
	return layers, flat, nil
}

// buildWorkspaceBatchWriteOrder returns a flat permutation of
// [0, len(ops)) that respects the dependency rules. Retained as the
// single-shot entry point for sequential execution paths and for
// regression tests that predate the layer-aware executor. Internally
// delegates to workspaceBatchWriteLayers.
func buildWorkspaceBatchWriteOrder(ops []WorkspaceBatchWriteOp) ([]int, error) {
	_, flat, err := workspaceBatchWriteLayers(ops)
	return flat, err
}

// isWorkspaceAncestorPath reports whether a is a strict ancestor of b,
// with both paths already cleaned. "/" (root) and "" are treated as the
// root and are ancestors of every path except themselves.
func isWorkspaceAncestorPath(a, b string) bool {
	a = strings.TrimSuffix(path.Clean(a), "/")
	b = strings.TrimSuffix(path.Clean(b), "/")
	if a == "" || a == "." {
		return b != "" && b != "."
	}
	if a == b {
		return false
	}
	return strings.HasPrefix(b, a+"/")
}

// workspacePathDepth returns the number of segments in a cleaned path,
// treating "" and "." as depth 0.
func workspacePathDepth(p string) int {
	clean := strings.TrimSuffix(path.Clean(p), "/")
	if clean == "" || clean == "." {
		return 0
	}
	return strings.Count(clean, "/") + 1
}

// ─── Helpers ─────────────────────────────────────────────────────────

// normalizeWorkspaceBatchReadItems merges the two input shapes
// (flat paths list + batch-level defaults, or explicit items array with
// per-item overrides) into a canonical item list the fan-out loop can
// consume uniformly.
func normalizeWorkspaceBatchReadItems(
	paths []string,
	items []WorkspaceBatchReadItem,
	defaultView, defaultPipelineID string,
	defaultOffset, defaultLimit int,
	ambientView, ambientPipelineID string,
) []WorkspaceBatchReadItem {
	view := strings.TrimSpace(defaultView)
	if view == "" {
		view = strings.TrimSpace(ambientView)
	}
	pipelineID := strings.TrimSpace(defaultPipelineID)
	if pipelineID == "" {
		pipelineID = strings.TrimSpace(ambientPipelineID)
	}

	out := make([]WorkspaceBatchReadItem, 0, len(paths)+len(items))
	for _, p := range paths {
		target := strings.TrimSpace(p)
		if target == "" {
			continue
		}
		out = append(out, WorkspaceBatchReadItem{
			Path:       target,
			View:       view,
			PipelineID: pipelineID,
			Offset:     defaultOffset,
			Limit:      defaultLimit,
		})
	}
	for _, item := range items {
		target := strings.TrimSpace(item.Path)
		if target == "" {
			continue
		}
		entry := item
		entry.Path = target
		if strings.TrimSpace(entry.View) == "" {
			entry.View = view
		}
		if strings.TrimSpace(entry.PipelineID) == "" {
			entry.PipelineID = pipelineID
		}
		if entry.Offset == 0 {
			entry.Offset = defaultOffset
		}
		if entry.Limit == 0 {
			entry.Limit = defaultLimit
		}
		out = append(out, entry)
	}
	return out
}

// classifyWorkspaceBatchReadResult inspects a per-item read result,
// returns its canonical status string (ok | missing | error) and its
// approximate byte size for budget accounting. The sizing is rough —
// Marshal + len(bytes) — but accurate enough to prevent a single huge
// file from blowing out the whole response.
func classifyWorkspaceBatchReadResult(result any) (string, int) {
	raw, err := json.Marshal(result)
	if err != nil {
		return "error", 0
	}
	bytes := len(raw)
	var probe struct {
		Missing     *bool `json:"missing,omitempty"`
		Unavailable *bool `json:"unavailable,omitempty"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "ok", bytes
	}
	if probe.Missing != nil && *probe.Missing {
		return "missing", bytes
	}
	if probe.Unavailable != nil && *probe.Unavailable {
		return "error", bytes
	}
	return "ok", bytes
}
