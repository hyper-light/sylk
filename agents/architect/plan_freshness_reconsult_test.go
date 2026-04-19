package architect

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/planapproval"
)

// TestPickReconsultationCandidates_NoSignalsNoCandidates pins the
// quiet path: a fresh audit with no drift signals must return zero
// candidates so the dialog publishes immediately. Without this a
// false-positive could cause the architect to thrash knowledge agents
// every time a plan is approved.
func TestPickReconsultationCandidates_NoSignalsNoCandidates(t *testing.T) {
	plan := &DesignPlan{
		ID: "p1",
		Consultations: map[string]*ConsultationEvidence{
			"librarian": {Target: "librarian", Success: true, ReceivedAt: time.Now().Add(-1 * time.Hour)},
		},
	}
	audit := &PlanFreshnessAudit{Fresh: true}
	if got := pickReconsultationCandidates(audit, plan, false); len(got) != 0 {
		t.Fatalf("expected no candidates for clean audit, got %+v", got)
	}
}

// TestPickReconsultationCandidates_CodebaseWarningTriggersLibrarian
// pins that a file-shape warning routes to the librarian — that's the
// agent whose original evidence is most likely stale when the
// codebase changes underneath the plan.
func TestPickReconsultationCandidates_CodebaseWarningTriggersLibrarian(t *testing.T) {
	plan := &DesignPlan{
		ID: "p1",
		Consultations: map[string]*ConsultationEvidence{
			"librarian": {Target: "librarian", Success: true, ReceivedAt: time.Now().Add(-1 * time.Hour)},
		},
	}
	audit := &PlanFreshnessAudit{
		Signals: []planapproval.DriftSignal{
			{Kind: "codebase", Severity: "warning", Description: "file went away"},
		},
	}
	candidates := pickReconsultationCandidates(audit, plan, false)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d (%+v)", len(candidates), candidates)
	}
	if candidates[0].Target != "librarian" {
		t.Fatalf("expected librarian, got %q", candidates[0].Target)
	}
	if candidates[0].Query == "" {
		t.Fatalf("candidate query must not be empty")
	}
}

// TestPickReconsultationCandidates_OrchestratorStateRoutesToArchivalist
// pins that execution-state divergence routes to the archivalist —
// the archivalist owns historical execution context, so a divergent
// orchestrator state means its history view may be out of date.
func TestPickReconsultationCandidates_OrchestratorStateRoutesToArchivalist(t *testing.T) {
	plan := &DesignPlan{
		ID: "p1",
		Consultations: map[string]*ConsultationEvidence{
			"archivalist": {Target: "archivalist", Success: true, ReceivedAt: time.Now().Add(-1 * time.Hour)},
		},
	}
	audit := &PlanFreshnessAudit{
		Signals: []planapproval.DriftSignal{
			{Kind: "orchestrator_state", Severity: "advisory"},
		},
	}
	candidates := pickReconsultationCandidates(audit, plan, false)
	if len(candidates) != 1 || candidates[0].Target != "archivalist" {
		t.Fatalf("expected archivalist candidate, got %+v", candidates)
	}
}

// TestPickReconsultationCandidates_DoesNotInventNewTargets pins a
// guarantee: if a drift signal recommends librarian but the plan
// never originally consulted librarian, we DO NOT add librarian as a
// candidate. The original architect chose the consultation set; the
// re-consult trigger only refreshes the chosen set, never expands it.
func TestPickReconsultationCandidates_DoesNotInventNewTargets(t *testing.T) {
	plan := &DesignPlan{
		ID: "p1",
		Consultations: map[string]*ConsultationEvidence{
			"academic": {Target: "academic", Success: true, ReceivedAt: time.Now().Add(-1 * time.Hour)},
		},
	}
	audit := &PlanFreshnessAudit{
		Signals: []planapproval.DriftSignal{
			{Kind: "codebase", Severity: "warning"},
		},
	}
	if got := pickReconsultationCandidates(audit, plan, false); len(got) != 0 {
		t.Fatalf("expected no candidates (librarian wasn't originally consulted), got %+v", got)
	}
}

// TestPickReconsultationCandidates_SkipsTooFreshConsultations pins
// the freshness floor — re-asking a librarian whose original
// consultation was 30 seconds ago is pure noise. The floor protects
// against rapid-fire approvals causing duplicated work.
func TestPickReconsultationCandidates_SkipsTooFreshConsultations(t *testing.T) {
	plan := &DesignPlan{
		ID: "p1",
		Consultations: map[string]*ConsultationEvidence{
			"librarian": {Target: "librarian", Success: true, ReceivedAt: time.Now().Add(-30 * time.Second)},
		},
	}
	audit := &PlanFreshnessAudit{
		Signals: []planapproval.DriftSignal{
			{Kind: "codebase", Severity: "warning"},
		},
	}
	if got := pickReconsultationCandidates(audit, plan, false); len(got) != 0 {
		t.Fatalf("expected no candidates (consultation too fresh), got %+v", got)
	}
}

// TestPickReconsultationCandidates_ResumeUnconditionalFanOut pins
// the resume invariant: when the plan is being resumed (re-presented
// across turns or sessions) the picker MUST refresh every
// originally-consulted target regardless of drift signals. The world
// has moved on since the plan was first shaped; the user deserves
// current evidence behind the verdict, not stale.
func TestPickReconsultationCandidates_ResumeUnconditionalFanOut(t *testing.T) {
	old := time.Now().Add(-1 * time.Hour) // past freshness floor, not yet at age warning
	plan := &DesignPlan{
		ID: "p1",
		Consultations: map[string]*ConsultationEvidence{
			"librarian":   {Target: "librarian", Success: true, ReceivedAt: old},
			"academic":    {Target: "academic", Success: true, ReceivedAt: old},
			"archivalist": {Target: "archivalist", Success: true, ReceivedAt: old},
		},
	}
	auditNoDrift := &PlanFreshnessAudit{Fresh: true}
	candidates := pickReconsultationCandidates(auditNoDrift, plan, true)
	if len(candidates) != 3 {
		t.Fatalf("resume mode should fan out to all 3 originally-consulted targets even without drift, got %d (%+v)",
			len(candidates), candidates)
	}
	for _, want := range []string{"librarian", "academic", "archivalist"} {
		found := false
		for _, c := range candidates {
			if c.Target == want {
				found = true
				if !contains(c.Reason, "resumed") {
					t.Fatalf("resume reason missing from %q candidate: %q", want, c.Reason)
				}
				break
			}
		}
		if !found {
			t.Fatalf("resume fan-out missing %q (got %+v)", want, candidates)
		}
	}
}

// TestPickReconsultationCandidates_ResumeStillRespectsFreshnessFloor
// pins that even in resume mode, consultations that are too recent
// (<5min) are skipped. Without this the chokepoint would thrash
// knowledge agents on rapid back-to-back turns.
func TestPickReconsultationCandidates_ResumeStillRespectsFreshnessFloor(t *testing.T) {
	plan := &DesignPlan{
		ID: "p1",
		Consultations: map[string]*ConsultationEvidence{
			"librarian": {Target: "librarian", Success: true, ReceivedAt: time.Now().Add(-30 * time.Second)},
		},
	}
	auditNoDrift := &PlanFreshnessAudit{Fresh: true}
	if got := pickReconsultationCandidates(auditNoDrift, plan, true); len(got) != 0 {
		t.Fatalf("resume mode must still skip too-fresh consultations, got %+v", got)
	}
}

func contains(haystack, needle string) bool {
	return haystack != "" && needle != "" && len(haystack) >= len(needle) &&
		stringIndex(haystack, needle) >= 0
}

func stringIndex(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// TestPickReconsultationCandidates_AgeWarningTouchesAllOriginal pins
// the broad-drift case: when the plan itself has aged past the
// 2-hour warning, we re-ask every originally-consulted target rather
// than picking favorites. Broad drift implies any single target may
// be stale.
func TestPickReconsultationCandidates_AgeWarningTouchesAllOriginal(t *testing.T) {
	old := time.Now().Add(-3 * time.Hour)
	plan := &DesignPlan{
		ID: "p1",
		Consultations: map[string]*ConsultationEvidence{
			"librarian":   {Target: "librarian", Success: true, ReceivedAt: old},
			"academic":    {Target: "academic", Success: true, ReceivedAt: old},
			"archivalist": {Target: "archivalist", Success: true, ReceivedAt: old},
		},
	}
	audit := &PlanFreshnessAudit{
		Signals: []planapproval.DriftSignal{
			{Kind: "consultation_age", Severity: "warning"},
		},
	}
	candidates := pickReconsultationCandidates(audit, plan, false)
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d (%+v)", len(candidates), candidates)
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		seen[c.Target] = true
	}
	for _, want := range []string{"librarian", "academic", "archivalist"} {
		if !seen[want] {
			t.Fatalf("missing candidate %q", want)
		}
	}
}

// TestPickReconsultationCandidates_ReplanRecommendationFanOut pins
// that RecommendReplan triggers a full re-consult of every
// originally-engaged knowledge agent. Severe drift means we should
// rebuild the evidence base before the user is asked to approve.
func TestPickReconsultationCandidates_ReplanRecommendationFanOut(t *testing.T) {
	old := time.Now().Add(-1 * time.Hour)
	plan := &DesignPlan{
		ID: "p1",
		Consultations: map[string]*ConsultationEvidence{
			"librarian": {Target: "librarian", Success: true, ReceivedAt: old},
			"academic":  {Target: "academic", Success: true, ReceivedAt: old},
		},
	}
	audit := &PlanFreshnessAudit{
		Recommendation: RecommendReplan,
	}
	candidates := pickReconsultationCandidates(audit, plan, false)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates for replan, got %d (%+v)", len(candidates), candidates)
	}
}

// TestPickReconsultationCandidates_DedupesReasonsForSameTarget pins
// that the same target chosen for two different drift signals shows
// up once with both reasons concatenated, not twice with one reason
// each. Duplicate consultations to the same agent waste budget.
func TestPickReconsultationCandidates_DedupesReasonsForSameTarget(t *testing.T) {
	old := time.Now().Add(-1 * time.Hour)
	plan := &DesignPlan{
		ID: "p1",
		Consultations: map[string]*ConsultationEvidence{
			"librarian": {Target: "librarian", Success: true, ReceivedAt: old},
		},
	}
	audit := &PlanFreshnessAudit{
		Signals: []planapproval.DriftSignal{
			{Kind: "codebase", Severity: "warning"},
			{Kind: "consultation_age", Severity: "warning"},
		},
	}
	candidates := pickReconsultationCandidates(audit, plan, false)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 deduped candidate, got %d (%+v)", len(candidates), candidates)
	}
	if !strings.Contains(candidates[0].Reason, "codebase") || !strings.Contains(candidates[0].Reason, "older") {
		t.Fatalf("expected combined reason text, got %q", candidates[0].Reason)
	}
}

// TestSummarizeReconsultEvidence_ExtractsCommonKeys pins that
// summarizeReconsultEvidence pulls human-readable text from the
// canonical keys ("summary", "response", "answer", "recommendation")
// when the evidence Data is a map, falling back to a generic
// acknowledgement when no keyed text is present.
func TestSummarizeReconsultEvidence_ExtractsCommonKeys(t *testing.T) {
	cases := []struct {
		name     string
		evidence *ConsultationEvidence
		want     string
	}{
		{"nil → no response", nil, "no response"},
		{"string data", &ConsultationEvidence{Data: "fresh take"}, "fresh take"},
		{"map summary key", &ConsultationEvidence{Data: map[string]any{"summary": "use new path"}}, "use new path"},
		{"map response key", &ConsultationEvidence{Data: map[string]any{"response": "still applies"}}, "still applies"},
		{"map without recognized key", &ConsultationEvidence{Data: map[string]any{"random": 42}}, "received fresh evidence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeReconsultEvidence(tc.evidence); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTruncateForDialog_CapsLongStringsWithEllipsis pins the dialog
// length cap. Without this guarantee a verbose librarian response
// could blow the dialog body's vertical budget and push the buttons
// off-screen.
func TestTruncateForDialog_CapsLongStringsWithEllipsis(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := truncateForDialog(long)
	if len(got) > 160 {
		t.Fatalf("truncation exceeded cap, got len=%d", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated string should end with ellipsis, got %q", got[len(got)-3:])
	}
}
