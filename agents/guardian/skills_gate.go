package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/skills"
)

// GuardianPreflightClassifierVersion identifies the active classifier
// rule set. Bumped whenever classifyPreflightPath / classifyRiskFactor
// or evaluateTaskPreflight semantics change. Orchestrator and architect
// both witness this in the attestation; a stale verdict (older version)
// is invalidated by the architect's plan freshness flow on regeneration.
const GuardianPreflightClassifierVersion = "guardian-preflight-v1"

// ---------------------------------------------------------------------------
// review_gate — Domain: gate, Priority: 90
// ---------------------------------------------------------------------------

type reviewGateInput struct {
	Action              string   `json:"action"`
	DiffContent         string   `json:"diff_content,omitempty"`
	Paths               []string `json:"paths,omitempty"`
	PlanID              string   `json:"plan_id,omitempty"`
	TaskID              string   `json:"task_id,omitempty"`
	AgentType           string   `json:"agent_type,omitempty"`
	Complexity          string   `json:"complexity,omitempty"`
	AffectedFiles       []string `json:"affected_files,omitempty"`
	RiskFactors         []string `json:"risk_factors,omitempty"`
	TestRequirements    []string `json:"test_requirements,omitempty"`
	Guidelines          []string `json:"guidelines,omitempty"`
	ImplementationGuide string   `json:"implementation_guide,omitempty"`

	// Batched preflight (action=preflight_plan) — architect issues a
	// single call covering every task in the plan, receives a unified
	// attestation back. Replaces the previous per-task RPC loop the
	// orchestrator ran post-approval.
	PlanRevision    int                      `json:"plan_revision,omitempty"`
	PlanContentHash string                   `json:"plan_content_hash,omitempty"`
	Tasks           []preflightPlanTaskInput `json:"tasks,omitempty"`
}

// preflightPlanTaskInput carries one task's preflight-relevant fields.
// Mirrors the per-task fields of reviewGateInput so the same
// classification logic applies unchanged.
type preflightPlanTaskInput struct {
	TaskID              string   `json:"task_id"`
	AgentType           string   `json:"agent_type,omitempty"`
	Complexity          string   `json:"complexity,omitempty"`
	AffectedFiles       []string `json:"affected_files,omitempty"`
	RiskFactors         []string `json:"risk_factors,omitempty"`
	TestRequirements    []string `json:"test_requirements,omitempty"`
	Guidelines          []string `json:"guidelines,omitempty"`
	ImplementationGuide string   `json:"implementation_guide,omitempty"`
}

// PlanPreflightAttestation, PlanPreflightTaskVerdict types moved to
// agents/shared/preflight_attestation.go so architect and orchestrator
// can consume them without import cycles. Guardian is still the
// canonical issuer; only the type definition migrated.

func reviewGateSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *reviewGateInput) (any, error)
	dispatch := map[string]handler{
		"review_diff": func(ctx context.Context, p *reviewGateInput) (any, error) {
			if p.DiffContent == "" {
				return nil, fmt.Errorf("diff_content is required for review_diff")
			}
			if board := g.guardianBoard(); board != nil {
				data, err := g.invokeGuardianDecisionService(ctx, board, claims.GuardianToolDiffReview, map[string]any{
					"policy_rule":      "diff.review",
					"approval_subject": p.TaskID,
					"diff":             p.DiffContent,
				})
				return guardianDiffServiceResult(data), err
			}
			result := g.diffGate.ReviewDiff(p.DiffContent)
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				verdict := "clean"
				if !result.Clean {
					verdict = "suspicious"
				}
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventDiffReviewed,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.DiffPayload{Verdict: verdict, Reason: fmt.Sprintf("%d findings", len(result.Findings))})
			}
			return map[string]any{
				"clean":    result.Clean,
				"findings": result.Findings,
				"stats":    result.Stats,
			}, nil
		},
		"pre_commit_check": func(ctx context.Context, p *reviewGateInput) (any, error) {
			if p.DiffContent == "" {
				return nil, fmt.Errorf("diff_content is required for pre_commit_check")
			}
			if board := g.guardianBoard(); board != nil {
				data, err := g.invokeGuardianDecisionService(ctx, board, claims.GuardianToolDiffReview, map[string]any{
					"policy_rule":      "diff.pre_commit",
					"approval_subject": p.TaskID,
					"diff":             p.DiffContent,
				})
				return guardianDiffServiceResult(data), err
			}
			readFile := func(path string) (string, error) {
				if g.fileAccess == nil {
					return "", fmt.Errorf("no file access configured")
				}
				ctx := context.Background()
				data, err := g.fileAccess.ReadFile(ctx, path)
				if err != nil {
					return "", err
				}
				return string(data), nil
			}
			result := g.diffGate.PreCommitCheck(p.DiffContent, p.Paths, readFile)
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				evt := agentlog.EventDiffApproved
				if !result.Clean {
					evt = agentlog.EventDiffRejected
				}
				shared.LogAgentEvent(lm.EventLogger, evt,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.DiffPayload{Verdict: fmt.Sprintf("%d findings", len(result.Findings))})
			}
			return map[string]any{
				"clean":         result.Clean,
				"findings":      result.Findings,
				"finding_count": len(result.Findings),
				"stats":         result.Stats,
			}, nil
		},
		"detect_anomaly": func(_ context.Context, _ *reviewGateInput) (any, error) {
			anomalies := g.healthMon.DetectAnomalies()
			return map[string]any{
				"anomalies":     anomalies,
				"anomaly_count": len(anomalies),
				"clean":         len(anomalies) == 0,
			}, nil
		},
		"preflight_task": func(_ context.Context, p *reviewGateInput) (any, error) {
			return evaluateTaskPreflight(p), nil
		},
		"preflight_plan": func(_ context.Context, p *reviewGateInput) (any, error) {
			return evaluatePlanPreflight(g, p), nil
		},
	}

	return skills.NewSkill("review_gate").
		Description("Diff review and pre-commit gating: review diffs for suspicious changes, run composite pre-commit checks.\n\n"+
			"Actions:\n"+
			"- review_diff: Review a diff for suspicious patterns (params: diff_content [required])\n"+
			"- pre_commit_check: Full pre-commit review including credential scan (params: diff_content [required], paths)\n"+
			"- detect_anomaly: Detect operational anomalies across the system\n"+
			"- preflight_task: Deterministically assess a planned task before mutation begins\n"+
			"- preflight_plan: Batched per-task preflight for an entire plan; returns a Guardian-issued PlanPreflightAttestation that the orchestrator verifies at ingest (replaces N sequential post-approval RPCs).").
		Domain("gate").
		Keywords("review", "diff", "pre-commit", "gate", "check", "anomaly").
		Priority(90).
		TokenEstimate(500).
		EnumParam("action", "Gate action", []string{
			"review_diff", "pre_commit_check", "detect_anomaly", "preflight_task", "preflight_plan",
		}, true).
		StringParam("diff_content", "Diff content to review (for review_diff, pre_commit_check)", false).
		ArrayParam("paths", "Staged file paths (for pre_commit_check)", "string", false).
		StringParam("plan_id", "Plan identifier for preflight_task", false).
		StringParam("task_id", "Task identifier for preflight_task", false).
		StringParam("agent_type", "Planned worker type for preflight_task", false).
		StringParam("complexity", "Architect-estimated complexity for preflight_task", false).
		ArrayParam("affected_files", "Files expected to be touched by preflight_task", "string", false).
		ArrayParam("risk_factors", "Architect-identified risk factors for preflight_task", "string", false).
		ArrayParam("test_requirements", "Required tests for preflight_task", "string", false).
		ArrayParam("guidelines", "Implementation guidelines for preflight_task", "string", false).
		StringParam("implementation_guide", "Detailed implementation guide for preflight_task", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params reviewGateInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown review_gate action: %q", params.Action)
			}
			if board := g.guardianBoard(); board != nil && reviewGateActionUsesGuardianService(params.Action) {
				if err := validateReviewGateServiceInput(params); err != nil {
					return nil, err
				}
				data, err := g.invokeGuardianDecisionService(ctx, board, claims.GuardianToolDiffReview, map[string]any{
					"policy_rule":      "diff." + params.Action,
					"approval_subject": params.TaskID,
					"diff":             params.DiffContent,
				})
				return guardianDiffServiceResult(data), err
			}

			// Post claim: review gate evaluation.
			sessionID := g.activeSessionID
			g.guardianPostClaim(ctx,
				guardianClaimAction(claims.ActionTypeTask),
				guardianClaim(
					"Review gate: "+params.Action,
					"Diff review or pre-commit gating evaluation",
					"guardian",
					[]claims.ClaimScopeEntry{{Kind: "gate", Key: params.Action}},
					claims.ActionTypeTask,
					[]*claims.Validation{
						guardianValidation(claims.ValidationTypeInspection, true, "No suspicious patterns in staged changes", "Zero findings in diff review"),
					},
				),
			)

			result, err := fn(ctx, &params)

			// Submit testament with results.
			if err != nil {
				g.guardianSubmitTestament(ctx, guardianTestamentAction(), guardianTestament(
					sessionID, "Review gate "+params.Action+" failed: "+err.Error(), "committed", "",
					[]*claims.Artifact{guardianArtifact(sessionID, "error", err.Error())},
				))
			} else {
				g.guardianSubmitTestament(ctx, guardianTestamentAction(), guardianTestament(
					sessionID, "Review gate "+params.Action+" complete", "committed", "",
					[]*claims.Artifact{guardianJSONArtifact(sessionID, "gate_results", result)},
				))
			}
			return result, err
		}).
		Build()
}

func validateReviewGateServiceInput(params reviewGateInput) error {
	switch params.Action {
	case "review_diff", "pre_commit_check":
		if params.DiffContent == "" {
			return fmt.Errorf("diff_content is required for %s", params.Action)
		}
	}
	return nil
}

type taskPreflightResult struct {
	PlanID           string   `json:"plan_id,omitempty"`
	TaskID           string   `json:"task_id,omitempty"`
	AgentType        string   `json:"agent_type,omitempty"`
	Clean            bool     `json:"clean"`
	RequiresApproval bool     `json:"requires_approval"`
	Warnings         []string `json:"warnings,omitempty"`
	BlockingFindings []string `json:"blocking_findings,omitempty"`
	UserMessage      string   `json:"user_message"`
}

// evaluatePlanPreflight runs the deterministic classifier across every
// task in the plan and assembles a single attestation. This is the
// architect-facing batched entry point that replaces the orchestrator's
// post-approval N-sequential-RPC loop.
//
// Strict pass-through of classifier results — same logic as
// evaluateTaskPreflight, but applied once per task in a single call.
// The architect attaches the attestation to PlanHandoff; the
// orchestrator verifies (plan_content_hash, classifier_version,
// per_task coverage) at ingest and does not re-run the classifier.
func evaluatePlanPreflight(g *Guardian, input *reviewGateInput) *shared.PlanPreflightAttestation {
	att := &shared.PlanPreflightAttestation{
		PlanID:            strings.TrimSpace(input.PlanID),
		PlanRevision:      input.PlanRevision,
		PlanContentHash:   strings.TrimSpace(input.PlanContentHash),
		ClassifierVersion: GuardianPreflightClassifierVersion,
		IssuedAt:          time.Now().UTC(),
		IssuerAgentID:     guardianIssuerID(g),
		PerTask:           make(map[string]*shared.PlanPreflightTaskVerdict, len(input.Tasks)),
	}
	for _, task := range input.Tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			continue
		}
		// Reuse the per-task classifier by adapting to its input shape.
		taskInput := &reviewGateInput{
			PlanID:              att.PlanID,
			TaskID:              taskID,
			AgentType:           task.AgentType,
			Complexity:          task.Complexity,
			AffectedFiles:       task.AffectedFiles,
			RiskFactors:         task.RiskFactors,
			TestRequirements:    task.TestRequirements,
			Guidelines:          task.Guidelines,
			ImplementationGuide: task.ImplementationGuide,
		}
		raw := evaluateTaskPreflight(taskInput)
		verdict := taskVerdictFromRaw(taskID, task.AgentType, raw)
		att.PerTask[taskID] = verdict
		if len(verdict.BlockingFindings) > 0 {
			att.HasBlockingFindings = true
		}
		if len(verdict.Warnings) > 0 {
			att.HasWarnings = true
		}
		if verdict.RequiresApproval {
			att.RequiresApproval = true
		}
	}
	return att
}

// taskVerdictFromRaw decodes the map[string]any returned by
// evaluateTaskPreflight into a typed PlanPreflightTaskVerdict. The
// underlying classifier emits an untyped map; rather than restructure
// it (and risk silently changing existing per-task callers), we
// project into the typed verdict here.
func taskVerdictFromRaw(taskID, agentType string, raw map[string]any) *shared.PlanPreflightTaskVerdict {
	v := &shared.PlanPreflightTaskVerdict{
		TaskID:    taskID,
		AgentType: agentType,
	}
	if c, ok := raw["clean"].(bool); ok {
		v.Clean = c
	}
	if r, ok := raw["requires_approval"].(bool); ok {
		v.RequiresApproval = r
	}
	if w, ok := raw["warnings"].([]string); ok {
		v.Warnings = w
	}
	if b, ok := raw["blocking_findings"].([]string); ok {
		v.BlockingFindings = b
	}
	if msg, ok := raw["user_message"].(string); ok {
		v.UserMessage = msg
	}
	return v
}

// guardianIssuerID returns the canonical agent ID for attestation
// provenance. Falls back to the agent type when the runtime ID
// hasn't been minted yet (test setups).
func guardianIssuerID(g *Guardian) string {
	if g == nil {
		return "guardian"
	}
	if id := strings.TrimSpace(g.id); id != "" {
		return id
	}
	return "guardian"
}

func evaluateTaskPreflight(input *reviewGateInput) map[string]any {
	result := taskPreflightResult{
		PlanID:    strings.TrimSpace(input.PlanID),
		TaskID:    strings.TrimSpace(input.TaskID),
		AgentType: strings.TrimSpace(input.AgentType),
	}

	for _, path := range uniqueStrings(input.AffectedFiles) {
		if finding, blocking, approval := classifyPreflightPath(path); finding != "" {
			if blocking {
				result.BlockingFindings = append(result.BlockingFindings, finding)
			} else {
				result.Warnings = append(result.Warnings, finding)
			}
			if approval {
				result.RequiresApproval = true
			}
		}
	}

	for _, risk := range uniqueStrings(input.RiskFactors) {
		if warning, blocking, approval := classifyRiskFactor(risk); warning != "" {
			if blocking {
				result.BlockingFindings = append(result.BlockingFindings, warning)
			} else {
				result.Warnings = append(result.Warnings, warning)
			}
			if approval {
				result.RequiresApproval = true
			}
		}
	}

	if strings.EqualFold(strings.TrimSpace(input.AgentType), "engineer") && len(input.AffectedFiles) == 0 {
		result.Warnings = append(result.Warnings, "Engineer task has no affected_files declaration; mutation scope is underspecified.")
		result.RequiresApproval = true
	}
	if len(input.TestRequirements) == 0 {
		result.Warnings = append(result.Warnings, "No explicit test requirements declared for this task.")
	}
	if strings.Contains(strings.ToLower(input.ImplementationGuide), "manual") {
		result.Warnings = append(result.Warnings, "Implementation guide includes manual steps; verify automation and rollback plan before dispatch.")
	}
	if strings.EqualFold(strings.TrimSpace(input.Complexity), "high") || strings.EqualFold(strings.TrimSpace(input.Complexity), "complex") {
		result.Warnings = append(result.Warnings, "Task complexity is high; monitor closely and checkpoint early.")
	}

	result.Warnings = uniqueStrings(result.Warnings)
	result.BlockingFindings = uniqueStrings(result.BlockingFindings)
	result.Clean = len(result.Warnings) == 0 && len(result.BlockingFindings) == 0
	result.UserMessage = buildPreflightUserMessage(result)

	return map[string]any{
		"plan_id":           result.PlanID,
		"task_id":           result.TaskID,
		"agent_type":        result.AgentType,
		"clean":             result.Clean,
		"requires_approval": result.RequiresApproval,
		"warnings":          result.Warnings,
		"blocking_findings": result.BlockingFindings,
		"user_message":      result.UserMessage,
	}
}

func classifyPreflightPath(path string) (finding string, blocking bool, approval bool) {
	clean := filepath.ToSlash(strings.TrimSpace(path))
	if clean == "" {
		return "", false, false
	}
	lower := strings.ToLower(clean)

	switch {
	case strings.HasSuffix(lower, ".env"), strings.Contains(lower, "secret"), strings.Contains(lower, "credential"):
		return fmt.Sprintf("Sensitive file path requires explicit approval: %s", clean), true, true
	case strings.HasPrefix(lower, ".github/workflows/"), strings.HasPrefix(lower, "deploy/"), strings.HasPrefix(lower, "infra/"), strings.HasPrefix(lower, "terraform/"), strings.HasPrefix(lower, "k8s/"):
		return fmt.Sprintf("Infrastructure or CI path requires explicit review: %s", clean), false, true
	case strings.Contains(lower, "migration"), strings.Contains(lower, "/schema"), strings.Contains(lower, "database/"):
		return fmt.Sprintf("Database-affecting path should be reviewed for rollout and rollback: %s", clean), false, true
	default:
		return "", false, false
	}
}

func classifyRiskFactor(risk string) (finding string, blocking bool, approval bool) {
	trimmed := strings.TrimSpace(risk)
	if trimmed == "" {
		return "", false, false
	}
	lower := strings.ToLower(trimmed)

	switch {
	case strings.Contains(lower, "secret"), strings.Contains(lower, "credential"), strings.Contains(lower, "permission"), strings.Contains(lower, "auth"), strings.Contains(lower, "payment"):
		return "Security-sensitive task requires early user visibility and explicit approval.", false, true
	case strings.Contains(lower, "delete"), strings.Contains(lower, "destructive"), strings.Contains(lower, "drop"):
		return "Destructive change identified; require explicit approval before execution.", true, true
	case strings.Contains(lower, "migration"), strings.Contains(lower, "schema"), strings.Contains(lower, "rollout"), strings.Contains(lower, "backfill"):
		return "Migration-style risk detected; validate rollout, rollback, and test coverage before execution.", false, true
	case strings.Contains(lower, "concurrency"), strings.Contains(lower, "race"), strings.Contains(lower, "deadlock"):
		return "Concurrency risk detected; require race/deadlock validation before completion.", false, false
	case strings.Contains(lower, "performance"), strings.Contains(lower, "latency"), strings.Contains(lower, "memory"):
		return "Performance-sensitive task detected; benchmark requirements should be explicit.", false, false
	default:
		return "", false, false
	}
}

func buildPreflightUserMessage(result taskPreflightResult) string {
	if result.Clean {
		return "Guardian preflight found no early safety issues."
	}

	var parts []string
	if len(result.BlockingFindings) > 0 {
		parts = append(parts, fmt.Sprintf("%d blocking findings", len(result.BlockingFindings)))
	}
	if len(result.Warnings) > 0 {
		parts = append(parts, fmt.Sprintf("%d warnings", len(result.Warnings)))
	}
	if result.RequiresApproval {
		parts = append(parts, "explicit approval required")
	}
	return "Guardian preflight: " + strings.Join(parts, ", ") + "."
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
