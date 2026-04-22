package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	shared "github.com/adalundhe/sylk/agents/shared"
)

func architectControlPlaneKind(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	if kind, ok := metadata["control_plane_kind"].(string); ok {
		return strings.TrimSpace(kind)
	}
	return ""
}

func (a *Architect) handleControlPlaneForward(ctx context.Context, fwd *guide.ForwardedRequest) (any, bool, error) {
	switch architectControlPlaneKind(fwd.Metadata) {
	case shared.ControlPlaneKindRemediationRequest:
		result, err := a.handleRemediationRequest(ctx, fwd)
		return result, true, err
	case shared.ControlPlaneKindPlanHandoffReceiptUpdate:
		result, err := a.handlePlanHandoffReceiptUpdate(ctx, fwd)
		return result, true, err
	default:
		return nil, false, nil
	}
}

func remediationCorrections(req *shared.RemediationRequest) []any {
	corrections := make([]any, 0, len(req.Findings)+len(req.SuggestedFixes)+1)
	for _, finding := range req.Findings {
		corrections = append(corrections, map[string]any{
			"description": firstNonEmptyString(finding.Recommendation, finding.Detail, finding.Summary),
			"message":     finding.Summary,
			"issue":       finding.Detail,
			"file":        finding.File,
			"line":        finding.Line,
			"severity":    string(finding.Severity),
		})
	}
	for _, fix := range req.SuggestedFixes {
		fix = strings.TrimSpace(fix)
		if fix == "" {
			continue
		}
		corrections = append(corrections, map[string]any{
			"description": fix,
			"message":     fix,
		})
	}
	if len(corrections) == 0 && strings.TrimSpace(req.Summary) != "" {
		corrections = append(corrections, map[string]any{
			"description": req.Summary,
			"message":     req.Summary,
		})
	}
	return corrections
}

func (a *Architect) handleRemediationRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	var req shared.RemediationRequest
	if err := json.Unmarshal([]byte(fwd.Input), &req); err != nil {
		return nil, fmt.Errorf("decode remediation request: %w", err)
	}
	req.SessionID = normalizeSessionID(firstNonEmpty(req.SessionID, fwd.SessionID))
	return a.processRemediationRequest(ctx, &req)
}

// processRemediationRequest is the shared entry point for every source
// of remediation (orchestrator-forwarded validator verdicts AND audit
// rejections received directly via the audit-rejection bridge). The
// two paths converge here so fix-workflow construction, task
// metadata, and result shaping stay identical regardless of how the
// request originated.
//
// Per docs/PARALLEL_GLOBAL_VFS.md §3.8, when req.RemediatesVersion is
// non-zero each fix task's Context carries "remediates_version" so the
// orchestrator's pipeline dispatch translates it into
// BeginPipelineConfig.BaseCopyVersion (byte-for-byte materialization
// from the rejected merge's Copy).
func (a *Architect) processRemediationRequest(ctx context.Context, req *shared.RemediationRequest) (*shared.RemediationResult, error) {
	if req == nil {
		return nil, fmt.Errorf("remediation request is nil")
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}

	// Surface the ACTUAL rejection reason to the user before any
	// workflow preparation work runs. The prior version emitted a
	// generic "Reviewing validator findings..." placeholder which left
	// users staring at a spinner with zero context about WHY their
	// work was rejected. The validator already supplied req.Summary
	// (headline) and req.Findings (itemized reasons); render both.
	a.publishPlanStreamChunk(ctx, formatRemediationRejectionPreamble(req))

	corrections := remediationCorrections(req)
	if len(corrections) == 0 {
		return &shared.RemediationResult{
			CaseID:         req.CaseID,
			SessionID:      req.SessionID,
			Resolution:     shared.RemediationResolutionNeedsUserInput,
			Summary:        "No actionable remediation items were provided.",
			UserMessage:    "I need more specific findings before I can correct the workflow.",
			NeedsUserInput: true,
			CreatedAt:      time.Now().UTC(),
		}, nil
	}

	var plan *DesignPlan
	if selected, err := a.selectPlan(req.PlanID); err == nil {
		plan = selected
	}

	tasks := buildFixTasks(corrections)
	annotateFixTasksForRemediation(tasks, req)
	workflow, err := a.createWorkflowDAG(ctx, tasks)
	if err != nil {
		return &shared.RemediationResult{
			CaseID:      req.CaseID,
			SessionID:   req.SessionID,
			PlanID:      req.PlanID,
			Resolution:  shared.RemediationResolutionUnrecoverable,
			Summary:     "I could not build a remediation workflow from the validator findings.",
			UserMessage: err.Error(),
			CreatedAt:   time.Now().UTC(),
		}, nil
	}

	planID := strings.TrimSpace(req.PlanID)
	if plan != nil {
		planID = firstNonEmpty(a.attachFixWorkflow(plan.ID, workflow, tasks), plan.ID)
	}

	dagJSON, err := workflow.DAG.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal remediation dag: %w", err)
	}

	// Stream the concrete plan: list each prepared task by name so
	// users see the mapping from finding → corrective task without
	// having to click into the pipelines.
	a.publishPlanStreamChunk(ctx, formatRemediationWorkflowSummary(tasks))

	return &shared.RemediationResult{
		CaseID:             req.CaseID,
		SessionID:          req.SessionID,
		PlanID:             planID,
		Resolution:         shared.RemediationResolutionFixWorkflow,
		Summary:            formatRemediationWorkflowSummary(tasks),
		UserMessage:        formatRemediationUserMessage(req, tasks),
		FixWorkflowDAGJSON: string(dagJSON),
		FixTaskCount:       len(tasks),
		Corrections:        append([]shared.ValidationFinding(nil), req.Findings...),
		CreatedAt:          time.Now().UTC(),
	}, nil
}

// annotateFixTasksForRemediation writes req's audit-rejection metadata
// (RemediatesVersion, RejectedReplicaID) onto each fix task's Context.
// The orchestrator's pipeline dispatch reads these keys to set
// BeginPipelineConfig.BaseCopyVersion so the fix pipeline materializes
// from the rejected merge's Copy (docs/PARALLEL_GLOBAL_VFS.md §3.8).
//
// Skipped when the request carries a zero RemediatesVersion — a
// non-audit validator rejection has no specific Copy to target.
func annotateFixTasksForRemediation(tasks []*AtomicTask, req *shared.RemediationRequest) {
	if req == nil || req.RemediatesVersion.IsZero() {
		return
	}
	versionStr := req.RemediatesVersion.String()
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if task.Context == nil {
			task.Context = make(map[string]any)
		}
		task.Context["remediates_version"] = versionStr
		if rid := strings.TrimSpace(req.RejectedReplicaID); rid != "" {
			task.Context["rejected_replica_id"] = rid
		}
	}
}

// formatRemediationRejectionPreamble renders the rejection reason as a
// multi-line chat chunk: a headline sentence naming who rejected the
// work, the validator's summary, and a bulleted list of findings with
// file/line context when present. This is the first thing the user
// sees after a rejection — it must answer "why" at a glance.
func formatRemediationRejectionPreamble(req *shared.RemediationRequest) string {
	var b strings.Builder
	b.WriteString(rejectionHeadline(req))
	summary := strings.TrimSpace(req.Summary)
	if summary != "" {
		b.WriteString("\n")
		b.WriteString(summary)
	}
	if details := strings.TrimSpace(req.Details); details != "" && details != summary {
		b.WriteString("\n")
		b.WriteString(details)
	}
	if len(req.Findings) > 0 {
		b.WriteString("\n\nFindings:")
		for _, finding := range req.Findings {
			b.WriteString("\n")
			b.WriteString(formatFindingLine(finding))
		}
	}
	if len(req.RecommendedActions) > 0 {
		b.WriteString("\n\nRecommended actions:")
		for _, action := range req.RecommendedActions {
			if a := strings.TrimSpace(action); a != "" {
				b.WriteString("\n • ")
				b.WriteString(a)
			}
		}
	}
	return b.String()
}

// rejectionHeadline names the source of the rejection so the user knows
// which validator weighed in. Falls back to a neutral phrasing when the
// request lacks a validator identity (should be rare in production but
// keeps the message readable in tests and edge cases).
func rejectionHeadline(req *shared.RemediationRequest) string {
	// The RemediationRequest doesn't carry the validator's agent type
	// explicitly — it comes in via the control-plane forwarding layer.
	// We can't reliably attribute the rejection to "global inspector"
	// vs "global tester" from the payload alone, but we can still
	// frame the message as a rejection with actionable context.
	return "Validator rejected the work — preparing corrective tasks."
}

// formatFindingLine renders a single finding as a bulleted line with
// severity, file/line locator, and the summary text. Example:
//
//	 • [high] tests/test_init.py:12 — invalid merged test artifact
//
// Designed to be scan-readable so a user can take in a 3-finding
// rejection at a glance.
func formatFindingLine(f shared.ValidationFinding) string {
	var parts []string
	if severity := strings.TrimSpace(string(f.Severity)); severity != "" {
		parts = append(parts, "["+severity+"]")
	}
	if loc := formatFindingLocator(f); loc != "" {
		parts = append(parts, loc)
	}
	summary := strings.TrimSpace(firstNonEmpty(f.Summary, f.Detail, f.Recommendation))
	if summary == "" {
		summary = "(no summary provided)"
	}
	prefix := strings.Join(parts, " ")
	if prefix == "" {
		return " • " + summary
	}
	return " • " + prefix + " — " + summary
}

// formatFindingLocator builds a compact file:line string when a finding
// includes filesystem anchor fields. Empty string when absent so the
// line stays clean.
func formatFindingLocator(f shared.ValidationFinding) string {
	file := strings.TrimSpace(f.File)
	if file == "" {
		return ""
	}
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d", file, f.Line)
	}
	return file
}

// formatRemediationWorkflowSummary renders the list of corrective tasks
// so the user sees what each fix pipeline will address. Used both as
// the chat stream chunk after workflow construction and as the
// RemediationResult.Summary field (consumed by downstream UI code).
func formatRemediationWorkflowSummary(tasks []*AtomicTask) string {
	if len(tasks) == 0 {
		return "No corrective tasks were needed."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Prepared %d corrective task", len(tasks))
	if len(tasks) != 1 {
		b.WriteString("s")
	}
	b.WriteString(":")
	for _, task := range tasks {
		b.WriteString("\n • ")
		b.WriteString(strings.TrimSpace(task.Name))
	}
	return b.String()
}

// formatRemediationUserMessage is the UserMessage field on the result —
// a concise single-paragraph form for callers (like the orchestrator's
// follow-up request flow) that want a brief explanation rather than the
// full stream-chunk preamble. Includes the leading rejection reason so
// the UserMessage is self-contained.
func formatRemediationUserMessage(req *shared.RemediationRequest, tasks []*AtomicTask) string {
	reason := strings.TrimSpace(firstNonEmpty(req.Summary, req.Details))
	if reason == "" && len(req.Findings) > 0 {
		reason = strings.TrimSpace(req.Findings[0].Summary)
	}
	if reason == "" {
		reason = "validator reported blocking issues"
	}
	count := len(tasks)
	if count == 0 {
		return fmt.Sprintf("I received a rejection (%s) but could not prepare corrective work from the findings.", reason)
	}
	verb := "a corrective task"
	if count != 1 {
		verb = fmt.Sprintf("%d corrective tasks", count)
	}
	return fmt.Sprintf("I prepared %s to address the rejection: %s.", verb, reason)
}
