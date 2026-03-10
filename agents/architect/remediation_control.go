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
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}

	a.publishPlanStreamChunk(ctx, "Reviewing validator findings and preparing a remediation workflow...")

	corrections := remediationCorrections(&req)
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

	a.publishPlanStreamChunk(ctx, fmt.Sprintf("Prepared a remediation workflow with %d corrective tasks.", len(tasks)))

	return &shared.RemediationResult{
		CaseID:             req.CaseID,
		SessionID:          req.SessionID,
		PlanID:             planID,
		Resolution:         shared.RemediationResolutionFixWorkflow,
		Summary:            fmt.Sprintf("Prepared a remediation workflow with %d corrective tasks.", len(tasks)),
		UserMessage:        "I prepared a corrective workflow based on the validator findings and attached it to the current plan.",
		FixWorkflowDAGJSON: string(dagJSON),
		FixTaskCount:       len(tasks),
		Corrections:        append([]shared.ValidationFinding(nil), req.Findings...),
		CreatedAt:          time.Now().UTC(),
	}, nil
}
