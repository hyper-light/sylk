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

	return skills.NewSkill("workspace_read").
		Description("Read workspace state. One primitive for all read operations across disk, global session overlay, and task-local pipeline overlays. Use op to select the specific operation.\n\n" +
			"Ops:\n" +
			"- read: Fetch file contents from a view (params: view, path, pipeline_id?, offset?, limit?)\n" +
			"- glob: Find files matching a pattern in a view (params: view, pattern, path?, exclude?, pipeline_id?)\n" +
			"- grep: Search file contents in a view (params: view, pattern, path?, include?, context_lines?, max_matches?, pipeline_id?)\n" +
			"- inspect: Compare a single path across disk / global / pipeline (params: path, pipeline_id?)\n" +
			"- summarize: Summarize a bounded set of paths across views (params: paths [required], pipeline_id?)\n" +
			"- diff: Structured diff of one file between two views (params: path, base_view, target_view, pipeline_id?)\n" +
			"- list_changes: List all pipeline/global changes since base (params: scope ∈ {pipeline, global})\n" +
			"- prepare_write: Preflight for workspace_write — probes the write surface, inspects the path across views, and returns a leased WorkspaceWriteBasis that the subsequent workspace_write call must consume (params: scope ∈ {pipeline, global}, path, pipeline_id?). This is the only op that allocates state; every other op is a pure observation.").
		Domain("filesystem").
		Keywords("workspace", "read", "glob", "grep", "inspect", "summarize", "diff", "list", "changes", "disk", "global", "pipeline", "prepare", "basis", "lease").
		Priority(92).
		Usage("Use whenever you need to observe workspace state. Choose op based on the question you're answering: single file → read, filename pattern → glob, file contents → grep, multi-view state → inspect/summarize, changes between views → diff/list_changes, before a write → prepare_write to capture a basis.").
		Satisfies("Produces workspace evidence for planning, implementation, inspection, or review without forcing the LLM to learn separate skill names. op=prepare_write additionally returns the basis token workspace_write consumes to validate the reference state hasn't shifted.").
		EnumParam("op", "Read operation", []string{"read", "glob", "grep", "inspect", "summarize", "diff", "list_changes", "prepare_write"}, true).
		// Common workspace-view params.
		EnumParam("view", "Workspace view (read/glob/grep)", []string{string(WorkspaceViewDisk), string(WorkspaceViewGlobal), string(WorkspaceViewPipeline)}, false).
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
		// summarize params.
		ArrayParam("paths", "Paths to summarize (summarize)", "string", false).
		// diff params.
		EnumParam("base_view", "Baseline view (diff)", workspaceViewEnumValues(), false).
		EnumParam("target_view", "Target view (diff)", workspaceViewEnumValues(), false).
		// list_changes + prepare_write scope param.
		EnumParam("scope", "Change scope (list_changes) or write scope (prepare_write)", []string{string(WorkspaceWriteScopePipeline), string(WorkspaceWriteScopeGlobal)}, false).
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
			default:
				return nil, fmt.Errorf("unknown workspace_read op: %q (expected read|glob|grep|inspect|summarize|diff|list_changes|prepare_write)", probe.Op)
			}
		}).
		Build()
}

// ─── workspace_write ─────────────────────────────────────────────────

// NewWorkspaceWriteSkill returns the consolidated workspace_write skill.
// Op enum covers all four write operations: write, edit, delete, mkdir.
// Scope enum selects pipeline-overlay vs global-overlay target.
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

	return skills.NewSkill("workspace_write").
		Description("Mutate workspace state via the leased write basis pattern. One primitive for all write operations; scope selects pipeline-overlay (task-local) vs global-overlay (session shared).\n\n" +
			"Ops:\n" +
			"- write: Create or replace a file (params: path, content, basis)\n" +
			"- edit: Apply targeted string edits to a file (params: path, edits, basis)\n" +
			"- delete: Remove a file (params: path, basis)\n" +
			"- mkdir: Create a directory (params: path, basis)").
		Domain("filesystem").
		Keywords("workspace", "write", "edit", "delete", "mkdir", "create", "directory", "file", "mutate", "basis", "lease", "pipeline", "global").
		Priority(94).
		Usage("Always call prepare_write_context first to obtain a leased basis for the target path, then pass that basis into workspace_write. Scope must match the basis.scope returned by prepare_write_context.").
		Requirement("For every op: path and basis are required. For write: content. For edit: edits (array of {old_text, new_text}).").
		Satisfies("Applies a bounded, lease-validated workspace mutation and returns the refreshed next_basis for chained writes.").
		EnumParam("op", "Write operation", []string{"write", "edit", "delete", "mkdir"}, true).
		EnumParam("scope", "Write scope (pipeline overlay or global overlay)", []string{string(WorkspaceWriteScopePipeline), string(WorkspaceWriteScopeGlobal)}, true).
		StringParam("path", "Target path", true).
		StringParam("content", "File content (op=write)", false).
		ArrayObjectParam("edits", "Edit list (op=edit)", map[string]*skills.Property{
			"old_text": {Type: "string", Description: "Text to replace (required)"},
			"new_text": {Type: "string", Description: "Replacement text (required)"},
		}, []string{"old_text", "new_text"}, false).
		ObjectParam("basis", "Leased write basis from prepare_write_context", map[string]*skills.Property{
			"scope":            {Type: "string", Description: "Must match the op scope"},
			"path":             {Type: "string", Description: "Path this basis was prepared for"},
			"pipeline_id":      {Type: "string", Description: "Active task pipeline ID (pipeline scope only)"},
			"target_view":      {Type: "string", Description: "Target view (pipeline or global)"},
			"prepared_at":      {Type: "string", Description: "When the basis was prepared"},
			"lease_expires_at": {Type: "string", Description: "When the basis lease expires"},
		}, true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var probe struct {
				Op    string `json:"op"`
				Scope string `json:"scope"`
			}
			if err := json.Unmarshal(input, &probe); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			op := strings.TrimSpace(probe.Op)
			scope := strings.TrimSpace(probe.Scope)
			switch scope {
			case string(WorkspaceWriteScopePipeline):
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
			case string(WorkspaceWriteScopeGlobal):
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
		}).
		Build()
}

// prepare_write_context folded into workspace_read(op=prepare_write).
// The underlying NewPreparePipelineWriteContextSkill /
// NewPrepareGlobalWriteContextSkill builders stay in this package for
// direct programmatic use and for tests; the standalone LLM-facing
// skill has been removed so the agent catalog carries a single
// read-family verb that also covers the write preflight.
