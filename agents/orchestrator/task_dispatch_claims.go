package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
)

// dispatchPipelineTaskAsClaim posts an action_type=task claim onto the
// session's claims board with the target pipeline worker as the
// claim's subject. This is the substrate-native replacement for the
// legacy ForwardedRequest RPC dispatch: PostAction itself fires the
// InboxDelta to the subject's already-subscribed ClaimsInbox, the
// inbox's OnResolved invokes the worker's processClaimsEntry on a
// tracked goroutine, and the cycle closes when the worker submits a
// testament that the inspector evaluates.
//
// The claim's Description carries the full task prompt; Validations
// carry acceptance criteria so the inspector evaluates against them
// directly. Scope entries are derived from the dispatch context's
// affected_files and workspace.{read,write,test}_set keys.
//
// Errors fall into two classes:
//   - board not yet wired (session has no claims board) — returned so
//     the caller can fail loudly; this is a programming error if it
//     happens after eager pipeline activation.
//   - PostAction failure — returned, recorded as a notification error
//     on the board for operator visibility.
//
// Always tracked, never sync: the actual board write goes through
// the orchestrator's scope via orchestratorPostClaim semantics, but
// here we want the deterministic claim_id back so we can correlate
// with the dispatch acknowledgement, so we call PostAction on the
// caller's already-tracked goroutine and let the amplifier emissions
// dispatch async on the board's wired scope.
func (o *Orchestrator) dispatchPipelineTaskAsClaim(
	ctx context.Context,
	dispatch *taskDispatchContext,
) error {
	if dispatch == nil {
		return fmt.Errorf("dispatchPipelineTaskAsClaim: dispatch is nil")
	}
	board := o.orchestratorBoardOrNil()
	if board == nil {
		return fmt.Errorf("dispatchPipelineTaskAsClaim: session %q has no claims board (eager activation likely missing)", o.SessionID())
	}

	subjectID := strings.TrimSpace(dispatch.targetID)
	if subjectID == "" {
		subjectID = pipelineWorkerTargetAgentID(o.SessionID(), dispatch.pipelineTaskID, dispatch.agentType)
	}
	if subjectID == "" {
		subjectID = strings.TrimSpace(dispatch.agentType)
	}

	title := buildPipelineClaimTitle(dispatch)
	description := buildPipelineClaimDescription(dispatch)
	validations := buildPipelineClaimValidations(dispatch)
	scope := buildPipelineClaimScope(dispatch)

	relations := []claims.Relation{
		{Related: "orchestrator", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		{Related: subjectID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
	}
	// Pipeline-protocol-eligible tasks (engineer / designer / tester
	// pipelines) are evaluated by the pipeline inspector. Wiring the
	// inspector as the evaluator surfaces TestamentDeltas to its
	// inbox automatically. The inspector is itself a task-scoped pipeline
	// worker, so its evaluator identity must be the canonical UUID that
	// matches its ClaimsInbox subscription — never the slug-form routing
	// target.
	if pipelineProtocolEligible(dispatch) || isPipelineWorkerType(dispatch.agentType) {
		evaluatorID := pipelineWorkerTargetAgentID(o.SessionID(), dispatch.pipelineTaskID, agentshared.PipelineAgentInspector)
		if strings.TrimSpace(evaluatorID) == "" {
			evaluatorID = agentshared.PipelineAgentInspector
		}
		relations = append(relations, claims.Relation{
			Related:      evaluatorID,
			RelatedType:  claims.RelatedTypeAgent,
			Relationship: claims.RelationshipEvaluator,
		})
	}
	// Cycle attribution (UI_DESIGN.md §4.5): nest this dispatched task
	// claim under the orchestrator's currently-executing claim so the
	// bridge's cycle resolver renders it as a child of that cycle.
	relations = agentshared.AttachCausedByFromContext(ctx, relations)

	claim := claims.Claim{
		Title:       title,
		Description: description,
		ActionType:  claims.ActionTypeTask,
		Scope:       scope,
		Relations:   relations,
		Validations: validations,
	}
	action := claims.Action{
		AgentID: "orchestrator",
		Type:    claims.ActionTypeTask,
	}

	expectedInboxTopic := claims.InboxTopic(o.SessionID(), subjectID, claims.RelationshipSubject, claims.ActionTypeTask)
	slog.Info("orchestrator_dispatch_claim_begin",
		"session_id", o.SessionID(),
		"task_id", dispatch.pipelineTaskID,
		"agent_type", dispatch.agentType,
		"subject_id", subjectID,
		"expected_inbox_topic", expectedInboxTopic,
		"validations", len(validations),
		"scope_entries", len(scope),
		"board_id", board.BoardID(),
	)

	if err := board.PostAction(ctx, action, []claims.Claim{claim}); err != nil {
		slog.Error("orchestrator_dispatch_claim_failed",
			"session_id", o.SessionID(),
			"task_id", dispatch.pipelineTaskID,
			"agent_type", dispatch.agentType,
			"subject_id", subjectID,
			"error", err.Error(),
		)
		board.RecordNotificationError(fmt.Sprintf(
			"orchestrator dispatch claim failed: task=%s subject=%s error=%s",
			dispatch.pipelineTaskID, subjectID, err.Error()))
		return fmt.Errorf("post pipeline task claim: %w", err)
	}
	slog.Info("orchestrator_dispatch_claim_posted",
		"session_id", o.SessionID(),
		"task_id", dispatch.pipelineTaskID,
		"agent_type", dispatch.agentType,
		"subject_id", subjectID,
		"expected_inbox_topic", expectedInboxTopic,
	)
	return nil
}

// buildPipelineClaimTitle returns a short human-readable title for
// the dispatched task claim. Falls back to the dispatch name then
// task slug then a generic placeholder.
func buildPipelineClaimTitle(d *taskDispatchContext) string {
	if name := strings.TrimSpace(d.name); name != "" {
		return name
	}
	if slug := strings.TrimSpace(d.pipelineTaskSlug); slug != "" {
		return slug
	}
	return fmt.Sprintf("Pipeline task: %s/%s", d.dagID, d.nodeID)
}

// buildPipelineClaimDescription serializes the full task prompt and
// auxiliary execution context into the claim's Description so the
// engineer's ComposeClaimsEntryPrompt surfaces it directly. Mirrors
// the prior ComposePipelineTaskUserPrompt shape so workers see the
// same task framing they did under the legacy ForwardedRequest path.
func buildPipelineClaimDescription(d *taskDispatchContext) string {
	var b strings.Builder
	if prompt := strings.TrimSpace(d.prompt); prompt != "" {
		b.WriteString(prompt)
		b.WriteString("\n\n")
	}
	if d.dagID != "" {
		b.WriteString("DAG: " + d.dagID + "\n")
	}
	if d.nodeID != "" {
		b.WriteString("Node: " + d.nodeID + "\n")
	}
	if d.pipelineTaskID != "" {
		b.WriteString("Task: " + d.pipelineTaskID + "\n")
	}
	if d.pipelineStage != "" {
		b.WriteString("Stage: " + d.pipelineStage + "\n")
	}
	if len(d.parentResults) > 0 {
		if encoded, err := json.MarshalIndent(d.parentResults, "", "  "); err == nil {
			b.WriteString("\n## Parent Results\n```json\n")
			b.Write(encoded)
			b.WriteString("\n```\n")
		}
	}
	if d.nodeCtx != nil {
		if encoded, err := json.MarshalIndent(extractContextSummary(d.nodeCtx), "", "  "); err == nil && string(encoded) != "{}" {
			b.WriteString("\n## Execution Context\n```json\n")
			b.Write(encoded)
			b.WriteString("\n```\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// extractContextSummary picks the keys from the dispatch nodeCtx that
// are relevant for the worker's local decisions (big_picture,
// task_intent, acceptance_criteria, success_criteria, test_requirements,
// guidelines, risk_factors, implementation_guide, workspace, affected_files,
// worker_packets). Drops orchestration plumbing keys that workers
// don't need to reason about.
func extractContextSummary(ctx map[string]any) map[string]any {
	if len(ctx) == 0 {
		return nil
	}
	keep := map[string]struct{}{
		"big_picture":          {},
		"task_intent":          {},
		"task_name":            {},
		"task_slug":            {},
		"acceptance_criteria":  {},
		"success_criteria":     {},
		"test_requirements":    {},
		"guidelines":           {},
		"risk_factors":         {},
		"implementation_guide": {},
		"workspace":            {},
		"affected_files":       {},
		"worker_packets":       {},
		"pipeline_stage":       {},
	}
	out := make(map[string]any, len(keep))
	for k, v := range ctx {
		if _, want := keep[k]; want {
			out[k] = v
		}
	}
	return out
}

// buildPipelineClaimValidations converts the dispatch's acceptance
// criteria into Validation records the inspector will evaluate. Each
// criterion becomes a single required Validation; the criterion's
// description is the validation description; quality_bar combines the
// criterion's "given/when/then" structure into a single criterion
// string.
func buildPipelineClaimValidations(d *taskDispatchContext) []*claims.Validation {
	rawCriteria := decodeAcceptanceCriteriaList(d.nodeCtx)
	if len(rawCriteria) == 0 {
		// Always require a Receipt validation so the inspector has at
		// least one thing to evaluate; testament must be submitted.
		return []*claims.Validation{
			{
				Type:        claims.ValidationTypeReceipt,
				Description: "Worker submits a testament with concrete artifacts demonstrating task completion",
				QualityBar:  "Testament present; artifacts ≥ 1; no kind=error_* artifacts",
				Status:      claims.ValidationStatusPending,
				Required:    true,
			},
		}
	}
	out := make([]*claims.Validation, 0, len(rawCriteria))
	for _, crit := range rawCriteria {
		given := strings.TrimSpace(crit["given"])
		when := strings.TrimSpace(crit["when"])
		thenText := strings.TrimSpace(crit["then"])
		desc := thenText
		if desc == "" {
			desc = strings.TrimSpace(crit["description"])
		}
		if desc == "" {
			continue
		}
		bar := fmt.Sprintf("Given %s; when %s; then %s", given, when, thenText)
		out = append(out, &claims.Validation{
			Type:        validationTypeForCriterion(crit),
			Description: desc,
			QualityBar:  strings.TrimSpace(bar),
			Status:      claims.ValidationStatusPending,
			Required:    true,
		})
	}
	if len(out) == 0 {
		out = append(out, &claims.Validation{
			Type:        claims.ValidationTypeReceipt,
			Description: "Worker submits a testament with concrete artifacts demonstrating task completion",
			QualityBar:  "Testament present; artifacts ≥ 1; no kind=error_* artifacts",
			Status:      claims.ValidationStatusPending,
			Required:    true,
		})
	}
	return out
}

// validationTypeForCriterion maps a criterion category to the
// substrate's ValidationType taxonomy. Unknown categories default
// to Inspection.
func validationTypeForCriterion(crit map[string]string) claims.ValidationType {
	switch strings.ToLower(strings.TrimSpace(crit["category"])) {
	case "test", "tests":
		return claims.ValidationTypeTest
	case "integration":
		return claims.ValidationTypeIntegration
	case "contract":
		return claims.ValidationTypeContract
	case "design":
		return claims.ValidationTypeDesign
	case "regression":
		return claims.ValidationTypeRegression
	case "receipt":
		return claims.ValidationTypeReceipt
	}
	return claims.ValidationTypeInspection
}

// decodeAcceptanceCriteriaList extracts the dispatch context's
// acceptance_criteria field, normalizing each entry to a flat
// string→string map. Tolerates entries that are already strings
// (treated as a single description) or maps with given/when/then keys.
func decodeAcceptanceCriteriaList(ctx map[string]any) []map[string]string {
	if ctx == nil {
		return nil
	}
	raw, ok := ctx["acceptance_criteria"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]string, 0, len(list))
	for _, entry := range list {
		switch typed := entry.(type) {
		case string:
			text := strings.TrimSpace(typed)
			if text == "" {
				continue
			}
			out = append(out, map[string]string{"description": text})
		case map[string]any:
			flat := make(map[string]string, len(typed))
			for k, v := range typed {
				if s, ok := v.(string); ok {
					flat[k] = s
				}
			}
			if len(flat) > 0 {
				out = append(out, flat)
			}
		}
	}
	return out
}

// buildPipelineClaimScope extracts file/scope entries from the
// dispatch's affected_files and workspace paths so the inspector and
// peer workers see what the task touches.
func buildPipelineClaimScope(d *taskDispatchContext) []claims.ClaimScopeEntry {
	if d.nodeCtx == nil {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]claims.ClaimScopeEntry, 0, 8)
	add := func(kind, key string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		dedupKey := kind + ":" + key
		if _, already := seen[dedupKey]; already {
			return
		}
		seen[dedupKey] = struct{}{}
		out = append(out, claims.ClaimScopeEntry{Kind: kind, Key: key})
	}
	for _, p := range affectedFilesFromContext(d.nodeCtx) {
		add("file", p)
	}
	for _, key := range []string{"read_set", "write_set", "test_surface", "prefetch_paths"} {
		for _, p := range workspacePathsFromContext(d.nodeCtx, key) {
			add("file", p)
		}
	}
	return out
}

// affectedFilesFromContext reads ctx["affected_files"] tolerantly:
// either a slice of strings or a slice of {path: string} maps.
func affectedFilesFromContext(ctx map[string]any) []string {
	raw, ok := ctx["affected_files"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, entry := range typed {
			switch e := entry.(type) {
			case string:
				if t := strings.TrimSpace(e); t != "" {
					out = append(out, t)
				}
			case map[string]any:
				if path, _ := e["path"].(string); strings.TrimSpace(path) != "" {
					out = append(out, strings.TrimSpace(path))
				}
			}
		}
		return out
	}
	return nil
}

// workspacePathsFromContext reads ctx["workspace"][key] as a slice
// of strings.
func workspacePathsFromContext(ctx map[string]any, key string) []string {
	workspace, _ := ctx["workspace"].(map[string]any)
	if len(workspace) == 0 {
		return nil
	}
	raw, ok := workspace[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, entry := range typed {
			if s, ok := entry.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}

// isPipelineWorkerType reports whether the given agent type is a
// pipeline worker (engineer / designer / tester / inspector pipeline).
// The PipelineAgent* constants from agents/shared expand to
// "engineer" / "designer" / "tester-pipeline" / "inspector-pipeline";
// listing them once each.
func isPipelineWorkerType(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case agentshared.PipelineAgentEngineer,
		agentshared.PipelineAgentDesigner,
		agentshared.PipelineAgentTester,
		agentshared.PipelineAgentInspector:
		return true
	}
	return false
}

// dispatchClaimTimeout bounds the synchronous claim post. PostAction
// is normally fast (in-process WAL append + amplifier dispatch goes
// async on the board's tracked scope). 5s gives ample headroom for
// SQLite write contention without leaving the dispatcher pinned.
const dispatchClaimTimeout = 5 * time.Second
