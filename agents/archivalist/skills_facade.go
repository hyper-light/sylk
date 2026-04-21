package archivalist

// skills_facade consolidates the archivalist's verbose per-verb tools into
// polymorphic façade skills that dispatch by a kind/action enum.
//
// The per-verb builders in skills_legacy.go (queryPatternsToolSkill,
// recordPatternToolSkill, declareIntentToolSkill, …) remain registered
// for internal programmatic callers and handler plumbing. The façade
// skills are the sole entries exposed to the LLM — the catalog carries
// three polymorphic verbs instead of ten look-alike tool names, which
// sharply reduces tool-name selection noise without removing any
// underlying capability.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// ─── archivalist_query ───────────────────────────────────────────────────

// ToolArchivalistQuery is the façade name for the archivalist query family.
// It dispatches on `kind` across briefing, patterns, failures, context,
// file_state, and conflicts — every read-only query path an archivalist
// caller used to pick by tool name.
const ToolArchivalistQuery = "archivalist_query"

func archivalistQueryFacadeSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolArchivalistQuery).
		Description("Query archivalist-held session, file, and coordination state. One primitive for every read operation; kind selects the specific query.\n\n" +
			"Kinds:\n" +
			"- briefing: Handoff briefing for continuing work (params: tier ∈ {micro, standard, full})\n" +
			"- patterns: Coding patterns by category (params: category, scope, limit)\n" +
			"- failures: Past failures and their resolutions (params: error_type, file_pattern, limit)\n" +
			"- context: Free-form RAG query over session/global/all scope (params: query [required], scope ∈ {session, global, all})\n" +
			"- file_state: File state across the current handoff context (params: path [required], include_history)\n" +
			"- conflicts: Overlapping active intents or unresolved conflicts (params: paths, check_intents)").
		Domain("memory").
		Keywords("archivalist", "query", "briefing", "patterns", "failures", "context", "file", "state", "conflicts", "recall").
		Priority(100).
		EnumParam("kind", "Which archivalist read to perform", []string{
			"briefing", "patterns", "failures", "context", "file_state", "conflicts",
		}, true).
		// briefing.
		EnumParam("tier", "Briefing detail level (kind=briefing)", []string{"micro", "standard", "full"}, false).
		// patterns + failures shared.
		StringParam("category", "Hierarchical pattern category (kind=patterns)", false).
		ArrayParam("scope", "Path scopes for pattern filter (kind=patterns)", "string", false).
		StringParam("error_type", "Error-type or keyword filter (kind=failures)", false).
		StringParam("file_pattern", "File path filter (kind=failures)", false).
		IntParam("limit", "Maximum number of results (kind=patterns|failures)", false).
		// context.
		StringParam("query", "Natural-language query (kind=context)", false).
		EnumParam("context_scope", "Search scope (kind=context)", []string{"session", "global", "all"}, false).
		// file_state.
		StringParam("path", "File path or glob pattern (kind=file_state)", false).
		BoolParam("include_history", "Include modification history (kind=file_state)", false).
		// conflicts.
		ArrayParam("paths", "Paths to check (kind=conflicts)", "string", false).
		BoolParam("check_intents", "Include active declared intents (kind=conflicts)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var probe struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(input, &probe); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.TrimSpace(probe.Kind) {
			case "briefing":
				var params GetBriefingInput
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
				return a.getBriefingToolData(params.Tier)
			case "patterns":
				var params QueryPatternsInput
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
				return a.queryPatternsToolData(ctx, params), nil
			case "failures":
				var params QueryFailuresInput
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
				return a.queryFailuresToolData(ctx, params), nil
			case "context":
				// context uses a custom payload shape — the facade's
				// `context_scope` avoids colliding with the kind=conflicts
				// `scope` param, so remap it here before dispatching.
				var body struct {
					Query        string `json:"query"`
					ContextScope string `json:"context_scope"`
				}
				if err := json.Unmarshal(input, &body); err != nil {
					return nil, err
				}
				return a.queryContextToolData(ctx, QueryContextInput{
					Query: body.Query,
					Scope: body.ContextScope,
				})
			case "file_state":
				var params QueryFileStateInput
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
				return a.queryFileStateToolData(params), nil
			case "conflicts":
				var params GetConflictsInput
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
				return a.getConflictsToolData(params), nil
			default:
				return nil, fmt.Errorf("unknown archivalist_query kind: %q (expected briefing|patterns|failures|context|file_state|conflicts)", probe.Kind)
			}
		}).
		Build()
}

// ─── archivalist_record ──────────────────────────────────────────────────

// ToolArchivalistRecord is the façade name for the archivalist record family.
// It dispatches on `kind` across pattern and failure records — both are
// write-side chronicle entries, so they share a façade.
const ToolArchivalistRecord = "archivalist_record"

func archivalistRecordFacadeSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolArchivalistRecord).
		Description("Record new archivalist knowledge. One primitive for every chronicle-write operation; kind selects the record type.\n\n" +
			"Kinds:\n" +
			"- pattern: Record a new coding pattern (params: pattern [required], category [required], scope, supersedes, reason)\n" +
			"- failure: Record a failure and its resolution (params: error [required], context [required], approach [required], resolution, outcome ∈ {success, partial, failed})").
		Domain("chronicle").
		Keywords("archivalist", "record", "pattern", "failure", "chronicle", "remember", "standard").
		Priority(85).
		EnumParam("kind", "What to record", []string{"pattern", "failure"}, true).
		// pattern fields.
		StringParam("pattern", "Pattern description (kind=pattern)", false).
		StringParam("category", "Pattern category (kind=pattern)", false).
		ArrayParam("scope", "Path scopes for the pattern (kind=pattern)", "string", false).
		ArrayParam("supersedes", "Pattern IDs superseded by this one (kind=pattern)", "string", false).
		StringParam("reason", "Reason for superseding or adopting the pattern (kind=pattern)", false).
		// failure fields.
		StringParam("error", "Observed error or failure reason (kind=failure)", false).
		StringParam("context", "Task context (kind=failure)", false).
		StringParam("approach", "Failed approach (kind=failure)", false).
		StringParam("resolution", "Working resolution (kind=failure)", false).
		EnumParam("outcome", "Outcome quality (kind=failure)", []string{"success", "partial", "failed"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var probe struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(input, &probe); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.TrimSpace(probe.Kind) {
			case "pattern":
				var params RecordPatternInput
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
				return a.recordPatternToolData(params), nil
			case "failure":
				var params RecordFailureInput
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
				return a.recordFailureToolData(params), nil
			default:
				return nil, fmt.Errorf("unknown archivalist_record kind: %q (expected pattern|failure)", probe.Kind)
			}
		}).
		Build()
}

// ─── archivalist_intent ──────────────────────────────────────────────────

// ToolArchivalistIntent is the façade name for the archivalist intent
// lifecycle. It dispatches on `action` across declare and complete — the
// two verbs that bracket a cross-cutting work declaration.
const ToolArchivalistIntent = "archivalist_intent"

func archivalistIntentFacadeSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolArchivalistIntent).
		Description("Manage the cross-cutting work-intent lifecycle. One primitive for both endpoints; action selects declare vs complete.\n\n" +
			"Actions:\n" +
			"- declare: Declare intent for cross-cutting work (params: type ∈ {refactor, rename, api_change, breaking_change}, description, affected_paths [required], affected_apis, priority ∈ {low, medium, high, critical})\n" +
			"- complete: Mark a declared intent as completed (params: intent_id [required], success [required], files_changed)").
		Domain("control").
		Keywords("archivalist", "intent", "declare", "complete", "coordination", "cross-cutting", "conflict").
		Priority(85).
		EnumParam("action", "Intent lifecycle action", []string{"declare", "complete"}, true).
		// declare fields.
		EnumParam("type", "Intent type (action=declare)", []string{"refactor", "rename", "api_change", "breaking_change"}, false).
		StringParam("description", "Intent description (action=declare)", false).
		ArrayParam("affected_paths", "Paths affected (action=declare)", "string", false).
		ArrayParam("affected_apis", "APIs affected (action=declare)", "string", false).
		EnumParam("priority", "Priority (action=declare)", []string{"low", "medium", "high", "critical"}, false).
		// complete fields.
		StringParam("intent_id", "Intent ID to complete (action=complete)", false).
		BoolParam("success", "Whether the intent succeeded (action=complete)", false).
		ArrayParam("files_changed", "Files changed (action=complete)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var probe struct {
				Action string `json:"action"`
			}
			if err := json.Unmarshal(input, &probe); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.TrimSpace(probe.Action) {
			case "declare":
				var params DeclareIntentInput
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
				return a.declareWorkIntent(params, a.id), nil
			case "complete":
				var params CompleteIntentInput
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
				return a.completeWorkIntent(params), nil
			default:
				return nil, fmt.Errorf("unknown archivalist_intent action: %q (expected declare|complete)", probe.Action)
			}
		}).
		Build()
}
