package architect

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	shared "github.com/adalundhe/sylk/agents/shared"
)

func TestHandleRemediationRequestBuildsFixWorkflow(t *testing.T) {
	a := newTestArchitect(t, Config{AllowPlanningWithoutConsultation: true})

	plan := &DesignPlan{
		ID:        "plan-1",
		SessionID: "sess",
		Status:    PlanStatusReady,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := a.planStore.Upsert(plan); err != nil {
		t.Fatalf("upsert plan: %v", err)
	}

	req := &shared.RemediationRequest{
		CaseID:    "case-1",
		SessionID: "sess",
		PlanID:    "plan-1",
		Summary:   "Fix failing integration and tighten validation.",
		Findings: []shared.ValidationFinding{
			{
				ID:             "f1",
				Severity:       shared.ValidationSeverityHigh,
				File:           "pkg/auth/service.go",
				Line:           42,
				Summary:        "Shared auth flow is failing under integration load.",
				Recommendation: "Add corrective validation and repair the auth flow.",
			},
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal remediation request: %v", err)
	}
	fwd := &guide.ForwardedRequest{
		Input:     string(data),
		SessionID: "sess",
		Metadata: map[string]any{
			"control_plane_kind": shared.ControlPlaneKindRemediationRequest,
		},
	}

	resultAny, handled, err := a.handleControlPlaneForward(context.Background(), fwd)
	if err != nil {
		t.Fatalf("handle remediation request: %v", err)
	}
	if !handled {
		t.Fatal("expected remediation request to be handled")
	}
	result, ok := resultAny.(*shared.RemediationResult)
	if !ok {
		t.Fatalf("expected remediation result, got %T", resultAny)
	}
	if result.Resolution != shared.RemediationResolutionFixWorkflow {
		t.Fatalf("expected fix_workflow resolution, got %s", result.Resolution)
	}
	if result.FixTaskCount == 0 {
		t.Fatal("expected remediation tasks")
	}
	if result.FixWorkflowDAGJSON == "" {
		t.Fatal("expected remediation dag json")
	}

	stored, ok := a.GetActivePlan("plan-1")
	if !ok || stored == nil {
		t.Fatal("expected stored plan")
	}
	if stored.Workflow == nil {
		t.Fatal("expected remediation workflow attached to plan")
	}

	// Fix task names must carry descriptive content, not ordinals. The
	// chat panel's pipeline header renders task.Name, so a purely
	// ordinal "Apply fix N" leaves the user guessing which rejection
	// maps to which pipeline. With a recommendation authored by the
	// validator, the task name must surface it.
	if len(stored.Tasks) == 0 {
		t.Fatal("expected remediation tasks on stored plan")
	}
	firstFixName := stored.Tasks[0].Name
	if strings.HasPrefix(firstFixName, "Apply fix ") {
		t.Errorf("task name %q is the ordinal fallback; validator supplied a recommendation so it should be descriptive", firstFixName)
	}
	if !strings.HasPrefix(firstFixName, "Fix: ") {
		t.Errorf("task name %q does not start with 'Fix: ' — descriptive-title invariant broken", firstFixName)
	}

	// RemediationResult.UserMessage must include the reason, not a
	// generic "prepared corrective workflow" string.
	if !strings.Contains(result.UserMessage, "integration") &&
		!strings.Contains(result.UserMessage, "auth flow") &&
		!strings.Contains(result.UserMessage, "validation") {
		t.Errorf("UserMessage %q does not surface the validator reason (expected phrasing from req.Summary or req.Findings)", result.UserMessage)
	}
	if !strings.Contains(result.Summary, "corrective task") {
		t.Errorf("Summary %q does not describe the prepared workflow", result.Summary)
	}
}

// TestBuildFixTaskName_PrefersRecommendationOverOrdinal covers the
// name-generation policy: when the correction payload includes a
// description/recommendation, the resulting Name must be a short
// description-derived title, not "Apply fix N".
func TestBuildFixTaskName_PrefersRecommendationOverOrdinal(t *testing.T) {
	correction := map[string]any{
		"description": "Add missing __init__.py to hello_cli so the package is importable.",
		"severity":    "high",
	}
	name := buildFixTaskName(correction, "Add missing __init__.py to hello_cli so the package is importable.", 0)
	if !strings.HasPrefix(name, "Fix: ") {
		t.Errorf("name = %q, want 'Fix: ' prefix", name)
	}
	if strings.HasPrefix(name, "Apply fix ") {
		t.Errorf("name = %q fell through to ordinal fallback despite having description", name)
	}
	if !strings.Contains(name, "missing __init__.py") {
		t.Errorf("name = %q does not carry description headline", name)
	}
}

// TestBuildFixTaskName_FallsBackToLocatorWhenNoProse tests the
// middle tier of the name-generation priority: when a finding has no
// prose but has a file/line locator, the locator becomes the name.
func TestBuildFixTaskName_FallsBackToLocatorWhenNoProse(t *testing.T) {
	correction := map[string]any{
		"file":     "tests/test_init.py",
		"line":     12,
		"severity": "high",
	}
	// Simulate the fallback description from extractCorrectionText.
	name := buildFixTaskName(correction, "Resolve correction item 1", 0)
	if !strings.Contains(name, "tests/test_init.py") {
		t.Errorf("name = %q does not include file locator from correction payload", name)
	}
	if !strings.Contains(name, ":12") {
		t.Errorf("name = %q missing line number", name)
	}
}

// TestBuildFixTaskName_OrdinalFallback_OnlyWhenNoSignal is the
// anti-regression for the one legitimate ordinal use: when the
// correction is completely empty, we still return a name rather than
// blank.
func TestBuildFixTaskName_OrdinalFallback_OnlyWhenNoSignal(t *testing.T) {
	name := buildFixTaskName(map[string]any{}, "", 3)
	if name != "Apply fix 4" {
		t.Errorf("empty correction name = %q, want 'Apply fix 4'", name)
	}
}

// TestTruncateFixTitle_CapsLongTitles verifies the header-row width
// cap so a pathological multi-paragraph finding doesn't blow out the
// chat-panel layout.
func TestTruncateFixTitle_CapsLongTitles(t *testing.T) {
	long := "Fix: " + strings.Repeat("very long ", 40)
	got := truncateFixTitle(long)
	// Rune count is what the chat panel column budget is measured in,
	// not byte count (the ellipsis "…" is 3 bytes but 1 rune).
	runeCount := len([]rune(got))
	if runeCount > fixTitleMaxLen {
		t.Errorf("truncated rune count = %d, want ≤ %d", runeCount, fixTitleMaxLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated title must end with ellipsis; got %q", got)
	}
}

// TestFormatRemediationRejectionPreamble_IncludesFindings verifies
// the rejection-preamble composition: it must carry the validator's
// summary AND an itemized list of findings with severity + locator.
// This is what lands in the chat as the explanation of WHY the work
// was rejected.
func TestFormatRemediationRejectionPreamble_IncludesFindings(t *testing.T) {
	req := &shared.RemediationRequest{
		Summary: "Invalid merged test artifact in hello_cli.",
		Findings: []shared.ValidationFinding{
			{
				Severity: shared.ValidationSeverityHigh,
				File:     "tests/test_init.py",
				Line:     12,
				Summary:  "Syntax error at line 12 — merge conflict marker not resolved.",
			},
			{
				Severity: shared.ValidationSeverityWarning,
				File:     "hello_cli/__init__.py",
				Summary:  "Empty __init__.py is missing the version string.",
			},
		},
		RecommendedActions: []string{
			"Re-run test synthesis against the merged source",
		},
	}
	got := formatRemediationRejectionPreamble(req)
	for _, want := range []string{
		"Invalid merged test artifact",
		"[high] tests/test_init.py:12",
		"Syntax error",
		"[warning] hello_cli/__init__.py",
		"Empty __init__.py",
		"Recommended actions:",
		"Re-run test synthesis",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("preamble missing required substring %q; got:\n%s", want, got)
		}
	}
}

// TestFormatRemediationWorkflowSummary_ListsEachTask ensures the
// post-construction stream chunk enumerates every corrective task by
// name — so the user can scan "what's going to happen" without opening
// pipelines.
func TestFormatRemediationWorkflowSummary_ListsEachTask(t *testing.T) {
	tasks := []*AtomicTask{
		{Name: "Fix: invalid merged test artifact"},
		{Name: "Fix: add missing __init__.py"},
	}
	got := formatRemediationWorkflowSummary(tasks)
	if !strings.Contains(got, "Prepared 2 corrective tasks:") {
		t.Errorf("summary count phrasing wrong; got: %s", got)
	}
	if !strings.Contains(got, "Fix: invalid merged test artifact") {
		t.Errorf("summary missing first task name; got: %s", got)
	}
	if !strings.Contains(got, "Fix: add missing __init__.py") {
		t.Errorf("summary missing second task name; got: %s", got)
	}
}

// TestFormatRemediationUserMessage_CarriesReason pins the UserMessage
// contract: the reason must be embedded so downstream callers have a
// self-contained explanation.
func TestFormatRemediationUserMessage_CarriesReason(t *testing.T) {
	req := &shared.RemediationRequest{
		Summary: "Invalid merged test artifact in hello_cli.",
	}
	tasks := []*AtomicTask{{Name: "Fix: invalid merged test artifact"}}
	got := formatRemediationUserMessage(req, tasks)
	if !strings.Contains(got, "Invalid merged test artifact") {
		t.Errorf("user message %q does not include the validator summary", got)
	}
	if !strings.Contains(got, "corrective task") {
		t.Errorf("user message %q does not describe the prepared work", got)
	}
}
