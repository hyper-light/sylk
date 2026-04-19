// Selective re-consultation trigger — Phase 2.5 of the agentic plan
// acceptance flow. When the freshness audit detects drift severe enough
// that the original consultation evidence may no longer apply, the
// architect re-asks the relevant knowledge agents (librarian / academic /
// archivalist) BEFORE the dialog publishes. The new evidence is folded
// back into the audit so the user sees both the drift signal AND a
// fresh take from the agent that originally reasoned about the affected
// surface.
//
// Why selective rather than always-on: re-consultation costs real time
// and bus traffic. The audit's drift signals are precisely the evidence
// that warrants re-asking — file-shape drift implies the librarian's
// codebase model is stale; orchestrator state divergence implies the
// archivalist's history is stale; severe age implies all of them are.
// We re-ask ONLY the targets the audit's signals point at, AND only if
// the original consultation is old enough that re-asking might return
// new information.
//
// Best-effort and time-bounded. If re-consultation times out or fails
// the dialog publishes with the original audit context — better to give
// the user some evidence than to block on a flaky consultation path.
package architect

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/planapproval"
)

// reconsultationBudget bounds total time spent re-consulting across all
// candidates. Conservative — the dialog should appear within a few
// seconds of plan ready; if re-consult exceeds this we proceed without
// fresh evidence and the original audit context still shows.
const reconsultationBudget = 8 * time.Second

// reconsultationMinElapsed is the floor on how recent the original
// consultation can be before re-asking is worthwhile. Under this value
// the agent's answer almost certainly hasn't changed and re-asking is
// just noise; the freshness audit still surfaces the drift signal so
// the user sees evidence even without a fresh consultation.
const reconsultationMinElapsed = 5 * time.Minute

// reconsultationCandidate identifies a knowledge-agent target the
// freshness audit recommends re-consulting, plus the human-readable
// reason that drove the recommendation. The reason is surfaced in the
// dialog as supporting context for why the re-consult ran.
type reconsultationCandidate struct {
	Target string
	Reason string
	Query  string
}

// pickReconsultationCandidates inspects the audit's drift signals and
// the plan's original consultation evidence, returning the targets
// worth re-asking. Empty slice means "original evidence still applies;
// no re-consult needed". Each candidate carries a short reason so the
// dialog can show "Re-asked X because Y."
//
// Selection rules:
//   - isResume=true → ALL originally-consulted targets, unconditional
//     (the world has moved on since the plan was first shaped; refresh
//     evidence even without explicit drift signals). Subject to the
//     min-elapsed freshness floor inside add() so back-to-back
//     re-presentations don't thrash the knowledge agents.
//   - Codebase warning signals → librarian (if originally consulted)
//   - Orchestrator state advisory/warning → archivalist (if originally
//     consulted) — execution history changed since the plan drafted
//   - Age warning (≥2h) → all originally-consulted targets — broad
//     drift means ANY consultation may be stale
//   - RecommendReplan → all originally-consulted targets — severe
//     enough that re-evaluation from scratch is more honest than
//     patching the existing plan
//
// Results are de-duplicated; the same target chosen for multiple
// reasons gets a combined reason string.
func pickReconsultationCandidates(audit *PlanFreshnessAudit, plan *DesignPlan, isResume bool) []reconsultationCandidate {
	if audit == nil || plan == nil || len(plan.Consultations) == 0 {
		return nil
	}
	now := time.Now()
	type pendingReason struct {
		Reasons []string
	}
	pending := map[string]*pendingReason{}
	add := func(target, reason string) {
		target = strings.ToLower(strings.TrimSpace(target))
		reason = strings.TrimSpace(reason)
		if target == "" || reason == "" {
			return
		}
		ev, ok := plan.Consultations[target]
		if !ok || ev == nil {
			// Don't introduce a target that wasn't originally
			// consulted — the user's plan was built with whatever
			// targets the architect chose; we re-ask those, not new
			// ones.
			return
		}
		if !ev.ReceivedAt.IsZero() && now.Sub(ev.ReceivedAt) < reconsultationMinElapsed {
			// Too fresh to be worth re-asking.
			return
		}
		entry, exists := pending[target]
		if !exists {
			entry = &pendingReason{}
			pending[target] = entry
		}
		entry.Reasons = append(entry.Reasons, reason)
	}

	if isResume {
		// Plan is being re-presented (resumed across turns or sessions).
		// Refresh ALL originally-engaged knowledge agents — the world
		// has moved on since the plan was first shaped, and the user
		// deserves current evidence behind the verdict, not stale.
		// Subject to the min-elapsed floor inside add() so back-to-back
		// re-presentations don't thrash the agents.
		for target := range plan.Consultations {
			add(target, "plan resumed — refreshing evidence")
		}
	}

	for _, signal := range audit.Signals {
		kind := strings.ToLower(strings.TrimSpace(signal.Kind))
		severity := strings.ToLower(strings.TrimSpace(signal.Severity))
		switch kind {
		case "codebase":
			if severity == "warning" {
				add("librarian", "codebase shape changed since plan drafted")
			}
		case "orchestrator_state":
			if severity == "advisory" || severity == "warning" {
				add("archivalist", "orchestrator reports execution state diverged from plan")
			}
		case "consultation_age":
			if severity == "warning" {
				for target := range plan.Consultations {
					add(target, "plan is significantly older than re-consult window")
				}
			}
		}
	}

	if audit.Recommendation == RecommendReplan {
		for target := range plan.Consultations {
			add(target, "freshness audit recommends re-evaluation")
		}
	}

	if len(pending) == 0 {
		return nil
	}
	candidates := make([]reconsultationCandidate, 0, len(pending))
	for target, entry := range pending {
		original := plan.Consultations[target]
		query := reconsultationQueryForTarget(target, plan, entry.Reasons)
		_ = original
		candidates = append(candidates, reconsultationCandidate{
			Target: target,
			Reason: strings.Join(uniqueOrderedStrings(entry.Reasons), "; "),
			Query:  query,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Target < candidates[j].Target
	})
	return candidates
}

// uniqueOrderedStrings preserves first-seen order while deduping.
// Tiny helper but avoids the dialog showing "X; X; X" when multiple
// signals point at the same target for the same reason.
func uniqueOrderedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

// reconsultationQueryForTarget builds the prompt sent to the
// knowledge agent during re-consultation. Includes the original plan
// query for context plus the drift reasons so the agent knows what's
// being verified — "is your prior answer still applicable?" rather
// than "answer this question from scratch."
func reconsultationQueryForTarget(target string, plan *DesignPlan, reasons []string) string {
	planQuery := strings.TrimSpace(plan.Query)
	if planQuery == "" {
		planQuery = "(plan query unavailable)"
	}
	reasonsLine := strings.Join(reasons, "; ")
	return fmt.Sprintf(
		"Plan freshness audit re-consultation. Original plan goal: %s. "+
			"Drift observed since you last weighed in: %s. "+
			"Please confirm whether your prior guidance still applies, "+
			"or flag what's changed.",
		planQuery, reasonsLine,
	)
}

// runReconsultationsForAudit issues the candidate consultations under
// a bounded total budget and folds the results into the audit as
// "reconsult_evidence" drift signals so the dialog body shows the
// fresh take alongside the original drift signal that triggered it.
//
// Best-effort: failures are recorded as info-severity signals so the
// user sees "we tried to re-consult X but it didn't respond" rather
// than silent omission.
func (a *Architect) runReconsultationsForAudit(ctx context.Context, plan *DesignPlan, audit *PlanFreshnessAudit, isResume bool) {
	if a == nil || audit == nil || plan == nil {
		return
	}
	candidates := pickReconsultationCandidates(audit, plan, isResume)
	if len(candidates) == 0 {
		return
	}
	budgetCtx, cancel := context.WithTimeout(ctx, reconsultationBudget)
	defer cancel()

	architectDebugLog().Info("runReconsultationsForAudit: START",
		"plan_id", plan.ID,
		"candidates", len(candidates),
		"is_resume", isResume,
		"budget", reconsultationBudget.String())

	for _, candidate := range candidates {
		if budgetCtx.Err() != nil {
			audit.Signals = append(audit.Signals, planapproval.DriftSignal{
				Kind:        "reconsult_evidence",
				Severity:    "info",
				Description: fmt.Sprintf("Skipped re-consult of %s — freshness audit budget exhausted.", candidate.Target),
			})
			continue
		}
		evidence, err := a.requestConsultation(budgetCtx, candidate.Target, candidate.Query, "", plan.SessionID)
		if err != nil || evidence == nil || !evidence.Success {
			audit.Signals = append(audit.Signals, planapproval.DriftSignal{
				Kind:        "reconsult_evidence",
				Severity:    "info",
				Description: fmt.Sprintf("Re-consulted %s (reason: %s): no usable response.", candidate.Target, candidate.Reason),
			})
			continue
		}
		audit.Signals = append(audit.Signals, planapproval.DriftSignal{
			Kind:        "reconsult_evidence",
			Severity:    "advisory",
			Description: fmt.Sprintf("Re-consulted %s (reason: %s): %s", candidate.Target, candidate.Reason, summarizeReconsultEvidence(evidence)),
		})
	}
	if len(candidates) > 0 {
		// Re-evaluation may have added signals; keep summary aligned.
		audit.Summary = composeFreshnessSummary(audit, len(plan.Tasks), time.Since(plan.UpdatedAt))
		audit.Recommendation = pickFreshnessRecommendation(audit.Signals)
		audit.Fresh = len(audit.Signals) == 0
	}
	architectDebugLog().Info("runReconsultationsForAudit: END",
		"plan_id", plan.ID,
		"final_signal_count", len(audit.Signals),
		"final_recommendation", string(audit.Recommendation))
}

// summarizeReconsultEvidence renders a one-line description of a
// consultation response for the dialog. We prefer the agent's own
// summary text when available; otherwise a generic acknowledgement.
// The dialog body has limited room so we cap aggressively.
func summarizeReconsultEvidence(evidence *ConsultationEvidence) string {
	if evidence == nil {
		return "no response"
	}
	switch typed := evidence.Data.(type) {
	case string:
		return truncateForDialog(typed)
	case map[string]any:
		for _, key := range []string{"summary", "response", "answer", "recommendation"} {
			if value, ok := typed[key]; ok {
				if asString, ok := value.(string); ok {
					return truncateForDialog(asString)
				}
			}
		}
	}
	return "received fresh evidence"
}

// truncateForDialog caps a string for inclusion in the dialog body.
// The dialog has finite vertical space; aggressive truncation keeps
// the freshness section readable even when an agent returns a long
// response. Byte-budget aware so the multi-byte ellipsis fits within
// the cap rather than overflowing it.
func truncateForDialog(s string) string {
	const maxBytes = 160
	const ellipsis = "…"
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes-len(ellipsis)] + ellipsis
}
