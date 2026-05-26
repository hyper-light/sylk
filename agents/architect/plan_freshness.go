// Plan freshness audit — Phase 2 of the agentic plan acceptance flow.
//
// Treats every plan re-presentation (whether fresh-out-of-planning or
// resumed across architect demotion/restart) as a re-evaluation rather
// than a re-dispatch. The audit composes cheap drift signals (workspace
// fingerprint, plan age, file existence/modification) into a structured
// PlanFreshnessAudit. The approval payload carries the audit's
// FreshnessSummary + DriftSignals as structured decision context; user-visible
// plan and audit text belongs in chat, not in the input-panel dialog.
//
// Cheap by design: no LLM calls, no consultations, no orchestrator
// queries inline. Each signal is a stat() call or a string compare.
// Phase 2.5 will add selective consultation triggers when drift
// exceeds threshold; Phase 3 wires the audit into requestPlanApprovalDialog
// so the decision payload remains fresh.
package architect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/planapproval"
)

// PlanFreshnessAudit is the structured output of auditPlanFreshness.
// Drives both the dialog's user-facing context (Summary + Signals) and
// the architect's internal routing decisions (Recommendation).
type PlanFreshnessAudit struct {
	// PlanID echoes back so the audit can be paired with its plan in
	// async paths (e.g. logged + dispatched separately).
	PlanID string

	// Fresh is true when no drift was detected. The dialog still
	// surfaces the audit but with a "no changes since drafting"
	// summary — explicit verification beats silent assumption.
	Fresh bool

	// Signals are the per-source drift observations. Each carries a
	// kind, severity, human-readable description, and optional path
	// for the source file or evidence. Empty when Fresh is true.
	Signals []planapproval.DriftSignal

	// Summary is a one-line description of the audit suitable for the
	// dialog header. Explicit positive ("plan still applies; checked
	// X files") or negative ("3 files changed since drafting") so the
	// user always sees evidence the audit ran.
	Summary string

	// Recommendation tells the architect (or the LLM-driven verdict
	// handler) what action the audit thinks is appropriate. Pure
	// advisory; the user's clicked verdict still wins.
	Recommendation FreshnessRecommendation

	// OrchestratorStateHint is the human-readable execution state
	// summary returned by the orchestrator's plan_state_query. Empty
	// when the orchestrator is unreachable or has no record of the
	// plan. Surfaced in the dialog so the user sees both file-drift
	// and execution-state context before clicking a verdict.
	OrchestratorStateHint string
}

// FreshnessRecommendation is the audit's suggested next step. The
// dialog still shows three options regardless; this drives default
// selection and informs the architect's narration ("I'd recommend
// approving as-is" vs "I'd recommend reviewing the changes first").
type FreshnessRecommendation string

const (
	// RecommendProceed: plan still applies, dispatch as-is is safe.
	RecommendProceed FreshnessRecommendation = "proceed"

	// RecommendRevise: drift detected but salvageable — apply
	// modifications before dispatching. UI defaults selection to the
	// Modify button.
	RecommendRevise FreshnessRecommendation = "revise"

	// RecommendReplan: drift severe enough that a fresh plan is
	// likely better than patching this one. UI defaults to Reject.
	RecommendReplan FreshnessRecommendation = "replan"
)

// planFreshnessMaxAge bounds how long a plan can sit between drafting
// and re-presentation before age alone triggers a drift signal. 30
// minutes matches ReadyPlanMaxAge — past that the plan is considered
// stale enough that something might have changed even if no concrete
// signal fires.
const planFreshnessMaxAge = 30 * time.Minute

// planAuditCacheTTL is how long a cached presentation audit is reused
// before falling through to a fresh run. Long enough that the dialog
// publish (which fires after compose) reuses the same audit the LLM
// saw; short enough that a follow-up turn after a real wait gets
// fresh evidence rather than stale data.
const planAuditCacheTTL = 90 * time.Second

// preparePlanPresentationAudit is the single chokepoint for plan
// presentation auditing. Both consumers (LLM context enrichment in
// applyFreshnessAuditToConversation AND dialog publish in
// requestPlanApprovalDialog) call this so the audit runs ONCE per
// presentation rather than once per code path.
//
// isResume distinguishes "freshly drafted plan" (no point re-asking
// the librarian — the plan was just shaped against current state)
// from "plan being resumed across turns or sessions" (the world has
// moved on; we should refresh evidence regardless of file-drift
// signals). Resume mode forces unconditional reconsult of every
// originally-engaged knowledge agent (subject to the existing
// freshness floor inside pickReconsultationCandidates).
//
// Returns the cached audit when the plan was presented within the
// TTL. Returns nil for nil plans or audit failures (caller treats
// nil as "no audit context available" rather than blocking).
func (a *Architect) preparePlanPresentationAudit(
	ctx context.Context,
	plan *DesignPlan,
	isResume bool,
) *PlanFreshnessAudit {
	if a == nil || plan == nil {
		return nil
	}
	a.planAuditCacheMu.Lock()
	if a.planAuditCache != nil {
		if entry, ok := a.planAuditCache[plan.ID]; ok && time.Since(entry.timestamp) < planAuditCacheTTL {
			a.planAuditCacheMu.Unlock()
			return entry.audit
		}
	}
	a.planAuditCacheMu.Unlock()

	audit := a.auditPlanFreshnessWithContext(ctx, plan)
	if audit == nil {
		return nil
	}
	a.runReconsultationsForAudit(ctx, plan, audit, isResume)

	a.planAuditCacheMu.Lock()
	if a.planAuditCache == nil {
		a.planAuditCache = make(map[string]*planAuditCacheEntry)
	}
	a.planAuditCache[plan.ID] = &planAuditCacheEntry{
		audit:     audit,
		timestamp: time.Now(),
	}
	a.planAuditCacheMu.Unlock()
	return audit
}

// invalidatePlanAuditCache drops a plan's cached audit. Called when
// the plan transitions state in a way that makes its audit stale
// (e.g. the user's Modify verdict triggers a revision; the prior
// audit no longer applies to the revised plan).
func (a *Architect) invalidatePlanAuditCache(planID string) {
	if a == nil || planID == "" {
		return
	}
	a.planAuditCacheMu.Lock()
	delete(a.planAuditCache, planID)
	a.planAuditCacheMu.Unlock()
}

// applyFreshnessAuditToConversation runs (or reuses) the audit via
// preparePlanPresentationAudit and copies the human-readable findings
// into the planner conversation request so the LLM weaves them into
// its narrative response. The dialog renders only buttons; the chat
// is where the user sees the audit context.
//
// Best-effort: a nil plan or nil audit is silently skipped so the
// conversation flow never breaks because the audit had a hiccup.
func (a *Architect) applyFreshnessAuditToConversation(
	ctx context.Context,
	plan *DesignPlan,
	isResume bool,
	apply func(summary string, signals []string, hint string, recommendation string),
) {
	if a == nil || plan == nil || apply == nil {
		return
	}
	audit := a.preparePlanPresentationAudit(ctx, plan, isResume)
	if audit == nil {
		return
	}
	apply(audit.Summary, driftSignalNarrativeMessages(audit.Signals), audit.OrchestratorStateHint, string(audit.Recommendation))
}

// driftSignalNarrativeMessages renders the per-signal drift list as
// short narrative lines suitable for embedding in the architect's
// prompt context. Each line is "[SEVERITY] description" so the LLM can
// quote the strongest signals verbatim into its chat response.
func driftSignalNarrativeMessages(signals []planapproval.DriftSignal) []string {
	if len(signals) == 0 {
		return nil
	}
	out := make([]string, 0, len(signals))
	for _, sig := range signals {
		desc := strings.TrimSpace(sig.Description)
		if desc == "" {
			continue
		}
		sev := strings.TrimSpace(sig.Severity)
		if sev == "" {
			out = append(out, desc)
			continue
		}
		out = append(out, "["+strings.ToUpper(sev)+"] "+desc)
	}
	return out
}

// auditPlanFreshness inspects the plan against the current workspace
// state and returns a structured audit. Cheap operations only: stat()
// calls on each AffectedFile referenced by tasks, age check against
// plan.UpdatedAt, plus a bus-routed plan_state_query to the
// orchestrator (3s timeout, best-effort).
//
// The architect calls this inside applyFreshnessAuditToConversation
// (which feeds the chat narrative) and inside requestPlanApprovalDialog
// (which used to feed the dialog body — now superseded by the chat
// narrative path). Kept around for tests that exercise the audit
// directly.
func (a *Architect) auditPlanFreshness(plan *DesignPlan) *PlanFreshnessAudit {
	return a.auditPlanFreshnessWithContext(context.Background(), plan)
}

// auditPlanFreshnessWithContext is the ctx-aware variant. Callers in
// active request handlers should use this so the orchestrator query
// propagates the request's deadline; the no-ctx variant exists for
// tests and best-effort pre-publish hooks.
func (a *Architect) auditPlanFreshnessWithContext(ctx context.Context, plan *DesignPlan) *PlanFreshnessAudit {
	if plan == nil {
		return nil
	}
	audit := &PlanFreshnessAudit{
		PlanID:         plan.ID,
		Fresh:          true,
		Recommendation: RecommendProceed,
	}

	workDir := a.workingDirectory()

	now := time.Now()
	age := now.Sub(plan.UpdatedAt)

	// File-existence / size signals. Cheap: stat() per AffectedFile.
	// Each TaskFileTarget tells us what the plan EXPECTS to operate
	// on; if a "modify" target is now missing, or a "create" target
	// already exists, that's drift the user should know about before
	// re-confirming.
	checked, fileSignals := a.driftSignalsForAffectedFiles(plan, workDir)
	audit.Signals = append(audit.Signals, fileSignals...)

	// Age signal: even with no file drift, surface the elapsed time
	// since drafting so the user can decide whether to trust the plan
	// in context. The signal is informational at <30min, advisory at
	// 30min-2h, warning past 2h.
	if ageSignal := planAgeDriftSignal(age); ageSignal != nil {
		audit.Signals = append(audit.Signals, *ageSignal)
	}

	// Orchestrator state hint (best-effort). The dialog body shows
	// this alongside drift signals so the user sees both
	// file-shape drift and execution-state context. Failure to query
	// (orchestrator unreachable, query timeout) is silent — the
	// freshness audit is informational, not blocking.
	if state := a.queryOrchestratorPlanState(ctx, plan.ID); state != nil {
		audit.OrchestratorStateHint = orchestratorStateHint(state)
		// Promote unexpected execution states into drift signals so
		// they're surfaced in the dialog body too. "running" /
		// "completed" while we're presenting a Ready plan signals
		// state divergence the user should know about.
		if signal := orchestratorStateDriftSignal(state, plan); signal != nil {
			audit.Signals = append(audit.Signals, *signal)
		}
	}
	if len(audit.Signals) > 0 {
		audit.Fresh = false
	}
	audit.Summary = composeFreshnessSummary(audit, checked, age)
	audit.Recommendation = pickFreshnessRecommendation(audit.Signals)

	// Freshness audit testament.
	a.architectSubmitTestament(ctx, a.architectTestament(
		fmt.Sprintf("Plan %s freshness: %s", plan.ID, audit.Recommendation),
		"committed",
		[]*claims.Artifact{
			a.architectArtifact("recommendation", string(audit.Recommendation)),
			a.architectJSONArtifact("drift_signals", audit.Signals),
			a.architectArtifact("orchestrator_state", audit.OrchestratorStateHint),
			a.architectArtifact("files_checked", fmt.Sprintf("%d", checked)),
			a.architectArtifact("fresh", fmt.Sprintf("%t", audit.Fresh)),
		},
	))

	return audit
}

// orchestratorStateDriftSignal escalates an unexpected orchestrator
// state into a drift signal. Specifically: when the architect is
// presenting a Ready plan but the orchestrator reports the same plan
// as already running / stalled / completed, that's state divergence
// the user should see explicitly before clicking Approve.
func orchestratorStateDriftSignal(state *orchestratorPlanState, plan *DesignPlan) *planapproval.DriftSignal {
	if state == nil || plan == nil {
		return nil
	}
	switch state.Status {
	case "running":
		return &planapproval.DriftSignal{
			Kind:        "orchestrator_state",
			Severity:    "advisory",
			Description: fmt.Sprintf("Orchestrator reports this plan is already running (%d/%d nodes complete). Re-approving will redispatch.", state.NodesSucceeded, state.NodesTotal),
		}
	case "stalled":
		return &planapproval.DriftSignal{
			Kind:        "orchestrator_state",
			Severity:    "warning",
			Description: fmt.Sprintf("Orchestrator reports this plan is stalled (no progress for %ds). Resuming may dispatch fresh execution.", state.StalledForSeconds),
		}
	case "completed":
		return &planapproval.DriftSignal{
			Kind:        "orchestrator_state",
			Severity:    "warning",
			Description: "Orchestrator reports this plan already completed in a prior run. Approving again will start a new execution.",
		}
	case "failed":
		msg := "Orchestrator reports this plan failed in a prior run."
		if state.Error != "" {
			msg = msg + " Last error: " + state.Error
		}
		return &planapproval.DriftSignal{
			Kind:        "orchestrator_state",
			Severity:    "warning",
			Description: msg,
		}
	default:
		return nil
	}
}

// driftSignalsForAffectedFiles stats every AffectedFile across all plan
// tasks. Returns the count of files actually checked plus one drift
// signal per observed mismatch:
//   - "modify" target that no longer exists → warning (user expected it
//     to be there)
//   - "create" target that already exists → advisory (user may want to
//     verify they're not about to clobber)
//   - "delete" target that no longer exists → info (no-op delete)
func (a *Architect) driftSignalsForAffectedFiles(plan *DesignPlan, workDir string) (int, []planapproval.DriftSignal) {
	if plan == nil {
		return 0, nil
	}
	seen := make(map[string]struct{})
	var signals []planapproval.DriftSignal
	checked := 0
	for _, task := range plan.Tasks {
		if task == nil {
			continue
		}
		for _, file := range task.AffectedFiles {
			path := strings.TrimSpace(file.Path)
			if path == "" {
				continue
			}
			if _, dup := seen[path]; dup {
				continue
			}
			seen[path] = struct{}{}
			absPath := path
			if workDir != "" && !filepath.IsAbs(path) {
				absPath = filepath.Join(workDir, path)
			}
			info, err := os.Stat(absPath)
			checked++
			op := strings.ToLower(strings.TrimSpace(file.Operation))
			switch op {
			case "modify":
				if err != nil {
					signals = append(signals, planapproval.DriftSignal{
						Kind:        "codebase",
						Severity:    "warning",
						Description: fmt.Sprintf("Task expects to modify `%s` but it no longer exists.", path),
						SourcePath:  path,
					})
				}
			case "create":
				if err == nil && info != nil && !info.IsDir() {
					signals = append(signals, planapproval.DriftSignal{
						Kind:        "codebase",
						Severity:    "advisory",
						Description: fmt.Sprintf("Task expects to create `%s` but a file already exists at that path.", path),
						SourcePath:  path,
					})
				}
			case "delete":
				if err != nil {
					signals = append(signals, planapproval.DriftSignal{
						Kind:        "codebase",
						Severity:    "info",
						Description: fmt.Sprintf("Task expects to delete `%s` but it no longer exists (no-op).", path),
						SourcePath:  path,
					})
				}
			}
		}
	}
	sort.SliceStable(signals, func(i, j int) bool {
		// Warnings first, then advisories, then info.
		return driftSeverityRank(signals[i].Severity) < driftSeverityRank(signals[j].Severity)
	})
	return checked, signals
}

func driftSeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "warning":
		return 0
	case "advisory":
		return 1
	default:
		return 2
	}
}

// planAgeDriftSignal returns an age-based drift signal when the plan
// has been sitting long enough that some external state may have
// changed independently of the plan. Returns nil for fresh plans
// (under planFreshnessMaxAge).
func planAgeDriftSignal(age time.Duration) *planapproval.DriftSignal {
	if age <= planFreshnessMaxAge {
		return nil
	}
	severity := "advisory"
	if age >= 2*time.Hour {
		severity = "warning"
	}
	return &planapproval.DriftSignal{
		Kind:        "consultation_age",
		Severity:    severity,
		Description: fmt.Sprintf("Plan was drafted %s ago. The codebase or your priorities may have shifted since then.", roundedDurationDescription(age)),
	}
}

// roundedDurationDescription renders a duration as a human-friendly
// approximation. Avoids the precise but ugly "3h17m42s" formatting in
// favor of "about 3 hours" / "about 45 minutes" since users care about
// scale, not seconds.
func roundedDurationDescription(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "about a minute"
		}
		return fmt.Sprintf("about %d minutes", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "about an hour"
		}
		return fmt.Sprintf("about %d hours", hours)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "about a day"
		}
		return fmt.Sprintf("about %d days", days)
	}
}

// composeFreshnessSummary builds the one-line summary the dialog uses
// in its header context. Always present — explicit "no drift detected"
// is more reassuring than a blank header.
func composeFreshnessSummary(audit *PlanFreshnessAudit, filesChecked int, age time.Duration) string {
	if audit == nil {
		return ""
	}
	if audit.Fresh {
		if filesChecked > 0 {
			return fmt.Sprintf("Re-checked %d referenced files (%s old) — no drift detected.", filesChecked, roundedDurationDescription(age))
		}
		return fmt.Sprintf("Plan is %s old; no drift detected.", roundedDurationDescription(age))
	}
	warnings := 0
	advisories := 0
	infos := 0
	for _, sig := range audit.Signals {
		switch strings.ToLower(strings.TrimSpace(sig.Severity)) {
		case "warning":
			warnings++
		case "advisory":
			advisories++
		default:
			infos++
		}
	}
	parts := []string{}
	if warnings > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s)", warnings))
	}
	if advisories > 0 {
		parts = append(parts, fmt.Sprintf("%d advisory", advisories))
	}
	if infos > 0 {
		parts = append(parts, fmt.Sprintf("%d info", infos))
	}
	return fmt.Sprintf("Re-check found drift since drafting: %s.", strings.Join(parts, ", "))
}

// pickFreshnessRecommendation maps the strongest signal severity to an
// architect-level recommendation. Pure heuristic — the user's clicked
// verdict still wins; this only drives default selection in the dialog
// and the architect's narration.
func pickFreshnessRecommendation(signals []planapproval.DriftSignal) FreshnessRecommendation {
	if len(signals) == 0 {
		return RecommendProceed
	}
	hasWarning := false
	advisoryCount := 0
	for _, sig := range signals {
		switch strings.ToLower(strings.TrimSpace(sig.Severity)) {
		case "warning":
			hasWarning = true
		case "advisory":
			advisoryCount++
		}
	}
	switch {
	case hasWarning && advisoryCount >= 2:
		// Warning plus multiple advisories → likely outdated enough to replan.
		return RecommendReplan
	case hasWarning:
		// Single warning → revise rather than replan.
		return RecommendRevise
	case advisoryCount > 0:
		return RecommendRevise
	default:
		return RecommendProceed
	}
}

// workingDirectory returns the architect's configured working directory
// (used as the root for AffectedFile path resolution). Falls back to
// the process CWD if the architect was constructed without an explicit
// WorkingDirectory.
func (a *Architect) workingDirectory() string {
	if a == nil {
		return ""
	}
	if dir := strings.TrimSpace(a.config.WorkingDirectory); dir != "" {
		return dir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}
