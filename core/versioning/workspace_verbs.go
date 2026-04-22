// Phase 2.K / CR-2 refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// 12 workspace skills collapsed into 3 verb-dispatched primitives —
// workspace_read, workspace_write, prepare_write_context. Each one
// routes to an existing per-op builder internally (no handler
// rewrites); only the LLM-facing name surface collapses.
//
// The per-op builders (NewReadWorkspaceFileSkill, NewWritePipelineFileSkill,
// etc.) remain importable so tests can continue to exercise them in
// isolation, but the LLM only sees the three polymorphic skills.
package versioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// ─── workspace_read ──────────────────────────────────────────────────

// WorkspaceReadSkillConfig wires the unified workspace_read skill onto
// an agent. getViews supplies the WorkspaceViewAccess for view-based
// reads; getFA supplies the FileAccess for list_changes.
//
// ReadSkillOverride is an optional replacement for the default `read`
// op handler. The tester uses this to install a missing-file-tolerant
// variant so test synthesis continues even when the target file does
// not exist yet (red-phase semantics). Other agents leave it nil to
// get the default error-on-missing behavior.
type WorkspaceReadSkillConfig struct {
	GetViews          WorkspaceViewAccessFunc
	GetFileAccess     FileAccessProvider
	DefaultPipelineID DefaultPipelineIDFunc
	Differ            Differ
	ReadSkillOverride *skills.Skill
}

// NewWorkspaceReadSkill returns the consolidated workspace_read skill.
// Op enum covers all seven read operations that previously had their
// own named skills: read (read_workspace_file), glob, grep, inspect
// (inspect_workspace_state), summarize (summarize_workspace_state),
// diff (diff_workspace_file), list_changes (list_{pipeline,global}_changes).
func NewWorkspaceReadSkill(cfg WorkspaceReadSkillConfig) *skills.Skill {
	// Build the underlying per-op skills once; dispatch to their
	// handlers at invocation time.
	reader := cfg.ReadSkillOverride
	if reader == nil {
		reader = NewReadWorkspaceFileSkill(cfg.GetViews, cfg.DefaultPipelineID)
	}
	globber := NewWorkspaceGlobSkill(cfg.GetViews, cfg.DefaultPipelineID)
	grepper := NewWorkspaceGrepSkill(cfg.GetViews, cfg.DefaultPipelineID)
	inspector := NewInspectWorkspaceStateSkill(cfg.GetViews, cfg.DefaultPipelineID)
	summarizer := NewSummarizeWorkspaceStateSkill(cfg.GetViews, cfg.DefaultPipelineID)
	differ := NewDiffWorkspaceFileSkill(cfg.GetViews, cfg.DefaultPipelineID, cfg.Differ)
	listPipeline := NewListPipelineChangesSkill(cfg.GetFileAccess)
	listGlobal := NewListGlobalChangesSkill(cfg.GetFileAccess)

	// prepare_write is the write-preflight that used to live in a
	// separate prepare_write_context skill. Folded in here so the
	// agent has a single read-family tool: every observation op stays
	// side-effect-free, but op=prepare_write additionally returns a
	// leased WorkspaceWriteBasis that workspace_write consumes. The
	// lease is the one side-effectful op in this skill — it's here
	// (rather than inside workspace_write) so the agent sees the
	// state + diffs before committing to a mutation.
	preparePipeline := NewPreparePipelineWriteContextSkill(cfg.GetViews, cfg.DefaultPipelineID, cfg.Differ)
	prepareGlobal := NewPrepareGlobalWriteContextSkill(cfg.GetViews, cfg.Differ)

	defaultPipelineIDFn := cfg.DefaultPipelineID

	return skills.NewSkill("workspace_read").
		Description("Read workspace state. One primitive for all read operations across disk, global session overlay, and task-local pipeline overlays. Use op to select the specific operation.\n\n" +
			"Ops:\n" +
			"- read: Fetch file contents from a view (params: view, path, pipeline_id?, offset?, limit?)\n" +
			"- batch: Fetch multiple files from a view in a single call (params: paths [required] OR items [per-path overrides], view?, pipeline_id?, budget_bytes?). Returns a per-path result array with status ∈ {ok, missing, error, skipped_budget}. Prefer this over N individual `op=read` calls when the set of paths is known upfront.\n" +
			"- glob: Find files matching a pattern in a view (params: view, pattern, path?, exclude?, pipeline_id?)\n" +
			"- grep: Search file contents in a view (params: view, pattern, path?, include?, context_lines?, max_matches?, pipeline_id?)\n" +
			"- inspect: Compare a single path across disk / global / pipeline (params: path, pipeline_id?)\n" +
			"- summarize: Summarize a bounded set of paths across views (params: paths [required], pipeline_id?)\n" +
			"- diff: Structured diff of one file between two views (params: path, base_view, target_view, pipeline_id?)\n" +
			"- list_changes: List all pipeline/global changes since base (params: scope ∈ {pipeline, global})\n" +
			"- prepare_write: Preflight for workspace_write — probes the write surface, inspects the path across views, and returns a leased WorkspaceWriteBasis that the subsequent workspace_write call must consume (params: scope ∈ {pipeline, global}, path, pipeline_id?). This is the only non-batch op that allocates state; every other observation op is pure.\n" +
			"- prepare_write_batch: Acquire write leases for a set of paths in one call (params: scope ∈ {pipeline, global}, paths [required], pipeline_id?). Atomic: if any prepare fails, the whole batch fails with no partial leases retained. Prefer this over N individual `op=prepare_write` calls, or skip it entirely by passing the paths directly to `workspace_write(op=batch)` — the write-batch handler acquires leases on demand.").
		Domain("filesystem").
		Keywords("workspace", "read", "batch", "glob", "grep", "inspect", "summarize", "diff", "list", "changes", "disk", "global", "pipeline", "prepare", "basis", "lease").
		Priority(92).
		Usage("Use whenever you need to observe workspace state. Choose op based on the question you're answering: single file → read, multiple known files → batch, filename pattern → glob, file contents → grep, multi-view state → inspect/summarize, changes between views → diff/list_changes, before a write → prepare_write to capture a basis (or prepare_write_batch for multiple paths).").
		Satisfies("Produces workspace evidence for planning, implementation, inspection, or review without forcing the LLM to learn separate skill names. op=prepare_write (and prepare_write_batch) additionally return the basis tokens workspace_write consumes to validate the reference state hasn't shifted.").
		EnumParam("op", "Read operation", []string{"read", "batch", "glob", "grep", "inspect", "summarize", "diff", "list_changes", "prepare_write", "prepare_write_batch"}, true).
		// Common workspace-view params.
		EnumParam("view", "Workspace view (read/batch/glob/grep)", []string{string(WorkspaceViewDisk), string(WorkspaceViewGlobal), string(WorkspaceViewPipeline)}, false).
		StringParam("path", "Path (read/inspect/diff/prepare_write) or base path (glob/grep)", false).
		StringParam("pipeline_id", "Task pipeline ID when the view or scope needs it", false).
		// read params.
		IntParam("offset", "Line offset (read)", false).
		IntParam("limit", "Max lines (read)", false).
		// glob/grep params.
		StringParam("pattern", "Glob pattern (glob) or regex pattern (grep)", false).
		ArrayParam("exclude", "Excluded glob patterns (glob)", "string", false).
		StringParam("include", "Included filename pattern (grep)", false).
		IntParam("context_lines", "Context lines around each match (grep)", false).
		IntParam("max_matches", "Max matches (grep, default 100)", false).
		// summarize + batch + prepare_write_batch params.
		ArrayParam("paths", "Paths (summarize, batch, prepare_write_batch)", "string", false).
		// batch-specific per-item array (optional; lets each entry override view/pipeline_id/offset/limit).
		ArrayObjectParam("items", "Per-path read overrides for op=batch (optional). Each entry: {path [required], view?, pipeline_id?, offset?, limit?}. Use when different paths need different views or slice windows; otherwise pass paths + batch-level view/pipeline_id/offset/limit.", map[string]*skills.Property{
			"path":        {Type: "string", Description: "File path (required)"},
			"view":        {Type: "string", Description: "Override view for this entry"},
			"pipeline_id": {Type: "string", Description: "Override pipeline_id for this entry"},
			"offset":      {Type: "integer", Description: "Line offset"},
			"limit":       {Type: "integer", Description: "Max lines"},
		}, []string{"path"}, false).
		IntParam("budget_bytes", "Aggregate response size cap for op=batch (default 2 MiB). Items past the cap return status=skipped_budget.", false).
		// diff params.
		EnumParam("base_view", "Baseline view (diff)", workspaceViewEnumValues(), false).
		EnumParam("target_view", "Target view (diff)", workspaceViewEnumValues(), false).
		// list_changes + prepare_write/prepare_write_batch scope param.
		EnumParam("scope", "Change scope (list_changes) or write scope (prepare_write, prepare_write_batch)", []string{string(WorkspaceWriteScopePipeline), string(WorkspaceWriteScopeGlobal)}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var probe struct {
				Op    string `json:"op"`
				Scope string `json:"scope,omitempty"`
			}
			if err := json.Unmarshal(input, &probe); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.TrimSpace(probe.Op) {
			case "read":
				return reader.Handler(ctx, input)
			case "batch":
				return handleWorkspaceReadBatch(ctx, reader, "", resolveSkillPipelineID("", defaultPipelineIDFn), input)
			case "glob":
				return globber.Handler(ctx, input)
			case "grep":
				return grepper.Handler(ctx, input)
			case "inspect":
				return inspector.Handler(ctx, input)
			case "summarize":
				return summarizer.Handler(ctx, input)
			case "diff":
				return differ.Handler(ctx, input)
			case "list_changes":
				switch strings.TrimSpace(probe.Scope) {
				case string(WorkspaceWriteScopeGlobal):
					return listGlobal.Handler(ctx, input)
				case string(WorkspaceWriteScopePipeline), "":
					return listPipeline.Handler(ctx, input)
				default:
					return nil, fmt.Errorf("unknown scope: %q (expected pipeline|global)", probe.Scope)
				}
			case "prepare_write":
				switch strings.TrimSpace(probe.Scope) {
				case string(WorkspaceWriteScopePipeline), "":
					return preparePipeline.Handler(ctx, input)
				case string(WorkspaceWriteScopeGlobal):
					return prepareGlobal.Handler(ctx, input)
				default:
					return nil, fmt.Errorf("unknown prepare_write scope: %q (expected pipeline|global)", probe.Scope)
				}
			case "prepare_write_batch":
				return handleWorkspaceReadPrepareWriteBatch(ctx, preparePipeline, prepareGlobal, WorkspaceWriteScope(strings.TrimSpace(probe.Scope)), resolveSkillPipelineID("", defaultPipelineIDFn), input)
			default:
				return nil, fmt.Errorf("unknown workspace_read op: %q (expected read|batch|glob|grep|inspect|summarize|diff|list_changes|prepare_write|prepare_write_batch)", probe.Op)
			}
		}).
		Build()
}

// ─── workspace_write ─────────────────────────────────────────────────

// NewWorkspaceWriteSkill returns the consolidated workspace_write skill.
// Op enum covers all four write operations: write, edit, delete, mkdir,
// plus op=batch for coordinated multi-op mutations with automatic DAG
// ordering and lease acquisition. Scope enum selects pipeline-overlay
// vs global-overlay target.
func NewWorkspaceWriteSkill(cfg WorkspaceWriteSkillConfig) *skills.Skill {
	// Build the underlying per-(op,scope) skills once; dispatch on op+scope.
	pipelineWrite := NewWritePipelineFileSkill(cfg)
	pipelineEdit := NewEditPipelineFileSkill(cfg)
	pipelineDelete := NewDeletePipelineFileSkill(cfg)
	pipelineMkdir := NewCreatePipelineDirectorySkill(cfg)
	globalWrite := NewWriteGlobalFileSkill(cfg)
	globalEdit := NewEditGlobalFileSkill(cfg)
	globalDelete := NewDeleteGlobalFileSkill(cfg)
	globalMkdir := NewCreateGlobalDirectorySkill(cfg)

	// Prepare-write handlers are needed for op=batch's automatic basis
	// acquisition (so the LLM can omit the `basis` field on each batch
	// entry and let the runtime lease on demand).
	var (
		preparePipeline *skills.Skill
		prepareGlobal   *skills.Skill
	)
	if cfg.GetViews != nil {
		preparePipeline = NewPreparePipelineWriteContextSkill(cfg.GetViews, cfg.DefaultPipelineID, cfg.Differ)
		prepareGlobal = NewPrepareGlobalWriteContextSkill(cfg.GetViews, cfg.Differ)
	}

	perOp := func(ctx context.Context, op string, scope WorkspaceWriteScope, input json.RawMessage) (any, error) {
		switch scope {
		case WorkspaceWriteScopePipeline:
			switch op {
			case "write":
				return pipelineWrite.Handler(ctx, input)
			case "edit":
				return pipelineEdit.Handler(ctx, input)
			case "delete":
				return pipelineDelete.Handler(ctx, input)
			case "mkdir":
				return pipelineMkdir.Handler(ctx, input)
			default:
				return nil, fmt.Errorf("unknown workspace_write op: %q (expected write|edit|delete|mkdir)", op)
			}
		case WorkspaceWriteScopeGlobal:
			switch op {
			case "write":
				return globalWrite.Handler(ctx, input)
			case "edit":
				return globalEdit.Handler(ctx, input)
			case "delete":
				return globalDelete.Handler(ctx, input)
			case "mkdir":
				return globalMkdir.Handler(ctx, input)
			default:
				return nil, fmt.Errorf("unknown workspace_write op: %q (expected write|edit|delete|mkdir)", op)
			}
		default:
			return nil, fmt.Errorf("unknown workspace_write scope: %q (expected pipeline|global)", scope)
		}
	}

	defaultPipelineIDFn := cfg.DefaultPipelineID

	return skills.NewSkill("workspace_write").
		Description("Mutate workspace state via the leased write basis pattern. One primitive for all write operations; scope selects pipeline-overlay (task-local) vs global-overlay (session shared).\n\n" +
			"Ops:\n" +
			"- write: Create or replace a file (params: path, content, basis)\n" +
			"- edit: Apply targeted string edits to a file (params: path, edits, basis)\n" +
			"- delete: Remove a file (params: path, basis)\n" +
			"- mkdir: Create a directory (params: path, basis)\n" +
			"- batch: Execute a set of write/edit/delete/mkdir operations in one call (params: operations [required], scope, pipeline_id?, atomic?). The runtime builds a path-based dependency DAG (mkdir parent before file writes in same tree, same-path ops preserve array order, siblings may run concurrently), acquires leases atomically on demand (each entry's `basis` is optional; omit it to let the runtime lease), and returns a per-op result array so the LLM sees which operations succeeded individually. Use this for any multi-file authoring flow — creating a package, scaffolding a module, applying a coordinated refactor — rather than issuing N individual write calls.").
		Domain("filesystem").
		Keywords("workspace", "write", "batch", "edit", "delete", "mkdir", "create", "directory", "file", "mutate", "basis", "lease", "pipeline", "global").
		Priority(94).
		Usage("For a single mutation, call the matching individual op (write/edit/delete/mkdir) with a basis from workspace_read(op=prepare_write,...). For multiple coordinated mutations, prefer op=batch: pass the full operations list and omit per-op basis to let the runtime acquire leases on demand.").
		Requirement("For individual ops: path and basis are required, content for write, edits for edit. For op=batch: operations (array of {op, path, content?, edits?, basis?}) is required; scope defaults to pipeline when omitted.").
		Satisfies("Applies bounded, lease-validated workspace mutations and returns refreshed next_basis tokens for chained writes. op=batch additionally handles dependency ordering and lease acquisition internally.").
		EnumParam("op", "Write operation", []string{"write", "edit", "delete", "mkdir", "batch"}, true).
		EnumParam("scope", "Write scope (pipeline overlay or global overlay)", []string{string(WorkspaceWriteScopePipeline), string(WorkspaceWriteScopeGlobal)}, false).
		StringParam("path", "Target path (individual ops only)", false).
		StringParam("content", "File content (op=write)", false).
		StringParam("pipeline_id", "Task pipeline ID (optional; falls back to ambient default)", false).
		ArrayObjectParam("edits", "Edit list (op=edit)", map[string]*skills.Property{
			"old_text": {Type: "string", Description: "Text to replace (required)"},
			"new_text": {Type: "string", Description: "Replacement text (required)"},
		}, []string{"old_text", "new_text"}, false).
		ObjectParam("basis", "Leased write basis from workspace_read(op=prepare_write) (individual ops only; op=batch acquires leases automatically when omitted)", map[string]*skills.Property{
			"scope":            {Type: "string", Description: "Must match the op scope"},
			"path":             {Type: "string", Description: "Path this basis was prepared for"},
			"pipeline_id":      {Type: "string", Description: "Active task pipeline ID (pipeline scope only)"},
			"target_view":      {Type: "string", Description: "Target view (pipeline or global)"},
			"prepared_at":      {Type: "string", Description: "When the basis was prepared"},
			"lease_expires_at": {Type: "string", Description: "When the basis lease expires"},
		}, false).
		ArrayObjectParam("operations", "Batched operations list (op=batch). Each entry specifies one mutation: {op ∈ {write, edit, delete, mkdir} [required], path [required], content? (write), edits? (edit), basis? (optional — runtime acquires a fresh lease when omitted)}. The entries' order does NOT dictate execution order — the runtime computes path-based dependency order automatically.", map[string]*skills.Property{
			"op":      {Type: "string", Description: "Operation: write | edit | delete | mkdir (required)", Enum: []string{"write", "edit", "delete", "mkdir"}},
			"path":    {Type: "string", Description: "Target path (required)"},
			"content": {Type: "string", Description: "File content (required for op=write)"},
			"edits": {
				Type:        "array",
				Description: "Edit list (required for op=edit)",
				Items: &skills.Property{
					Type: "object",
					Properties: map[string]*skills.Property{
						"old_text": {Type: "string", Description: "Text to replace"},
						"new_text": {Type: "string", Description: "Replacement text"},
					},
					Required: []string{"old_text", "new_text"},
				},
			},
			"basis": {
				Type:        "object",
				Description: "Optional leased basis; omit to let the runtime acquire one on demand",
				Properties:  workspaceWriteBasisProperties(),
			},
		}, []string{"op", "path"}, false).
		BoolParam("atomic", "When true (op=batch), the first failing op aborts the batch and remaining ops are skipped. Default false: all ops are attempted and the per-op result array reports mixed success/failure.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var probe struct {
				Op    string `json:"op"`
				Scope string `json:"scope"`
			}
			if err := json.Unmarshal(input, &probe); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			op := strings.TrimSpace(probe.Op)
			if op == "batch" {
				return handleWorkspaceWriteBatch(ctx, perOp, preparePipeline, prepareGlobal, resolveSkillPipelineID("", defaultPipelineIDFn), input)
			}
			scope := strings.TrimSpace(probe.Scope)
			if scope == "" {
				return nil, fmt.Errorf("workspace_write: scope is required for individual ops (one of pipeline|global); use op=batch for multi-op flows")
			}
			return perOp(ctx, op, WorkspaceWriteScope(scope), input)
		}).
		Build()
}

// prepare_write_context folded into workspace_read(op=prepare_write).
// The underlying NewPreparePipelineWriteContextSkill /
// NewPrepareGlobalWriteContextSkill builders stay in this package for
// direct programmatic use and for tests; the standalone LLM-facing
// skill has been removed so the agent catalog carries a single
// read-family verb that also covers the write preflight.
