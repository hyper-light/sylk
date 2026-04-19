package shared

import (
	"strings"
	"testing"
)

func TestValidateAgentReportShape_AcceptsConformingTesterReport(t *testing.T) {
	report := `## Tester Turn Report

**Status:** Verified · 4/4 success criteria met

### Summary

Authored ` + "`tests/test_metadata.py`" + ` with 4 cases covering PEP 517 metadata, Ruff lint enforcement, build target shape, and module import surface. All four passed against the merged checkpoint.

### Criteria Reviewed

1. **C-1 PEP 517 metadata** ✓
   - Evidence: ` + "`tests/test_metadata.py::test_pep517_compliance`" + ` passed
2. **C-2 Ruff lint clean** ✓
   - Evidence: ` + "`make lint`" + ` exit 0

### Findings

- No blocking defects on the merged checkpoint surface.
- Engineer should still verify ` + "`Makefile`" + ` lint target wires through.

### Next

Hand off to **engineer** for implementation pass on the build target.`

	if err := ValidateAgentReportShape(report, TesterTurnReportShapeProfile); err != nil {
		t.Fatalf("ValidateAgentReportShape on conforming report = %v, want nil", err)
	}
}

func TestValidateAgentReportShape_RejectsLeadingProseFromScreenshot(t *testing.T) {
	// This is the actual paragraph from the user's screenshot of the bug.
	// The validator must reject it: it starts with a sentence rather than
	// a heading, and the entire response is one ~300-word paragraph.
	prose := `I'm grounding on the merged checkpoint surface first so the strategy ` +
		`is tied to the actual global state, not just the handoff summary. The ` +
		`workspace summary already shows a key risk: the merged artifact ` +
		`referenced by the handoff is not present in the currently accessible ` +
		`workspace view. I'm reading the concrete files next to separate a real ` +
		`product regression from a visibility/state mismatch. I've confirmed the ` +
		`accessible workspace currently lacks the very files the checkpoint says ` +
		`were merged, so I'm pulling precedent to keep the validation strategy ` +
		`grounded and avoid over-narrowing on one symptom. I've hit a state ` +
		`mismatch between the handoff and the accessible workspace, so I'm ` +
		`switching to a specification-driven kick pass. That lets me frame what ` +
		`must be validated now versus what is merely a later-checkpoint concern. ` +
		`I've established the current cross-component surface: the packaging file ` +
		`and its metadata target are not visible in the accessible workspace, ` +
		`while the Go-centric Makefile is. I'm converting that into an explicit ` +
		`checkpoint validation plan so the next review step has concrete coverage ` +
		`targets and failure hypotheses. I've completed the planning pass: the ` +
		`highest-risk issue is not the accepted metadata contract itself, but ` +
		`that the currently accessible workspace does not expose pyproject.toml ` +
		`or tests/test_metadata.py at all. I'm returning a concrete checkpoint ` +
		`validation strategy to the inspector with the required follow-up gates ` +
		`and non-blocking judgments scoped to this checkpoint.`

	err := ValidateAgentReportShape(prose, TesterTurnReportShapeProfile)
	if err == nil {
		t.Fatal("ValidateAgentReportShape on bare prose = nil, want heading violation")
	}
	if !strings.Contains(err.Error(), "must start with") {
		t.Fatalf("error = %q, want heading-prefix violation", err.Error())
	}
}

func TestValidateAgentReportShape_RejectsMissingRequiredSection(t *testing.T) {
	report := `## Tester Turn Report

**Status:** Verified

### Summary

Done.

### Findings

- All clear.`

	err := ValidateAgentReportShape(report, TesterTurnReportShapeProfile)
	if err == nil {
		t.Fatal("ValidateAgentReportShape with missing 'Next' section = nil, want missing-section error")
	}
	if !strings.Contains(err.Error(), "next") {
		t.Fatalf("error = %q, want 'next' as missing section", err.Error())
	}
}

func TestValidateAgentReportShape_RejectsLongUnstructuredParagraphInsideSection(t *testing.T) {
	bigParagraph := strings.Repeat("word ", 120)
	report := `## Tester Turn Report

**Status:** Verified

### Summary

` + bigParagraph + `

### Findings

- All clear.

### Next

Hand off.`

	err := ValidateAgentReportShape(report, TesterTurnReportShapeProfile)
	if err == nil {
		t.Fatal("ValidateAgentReportShape with 120-word paragraph = nil, want paragraph-length error")
	}
	if !strings.Contains(err.Error(), "unstructured paragraph") {
		t.Fatalf("error = %q, want unstructured-paragraph error", err.Error())
	}
}

func TestValidateAgentReportShape_AcceptsEmptyText(t *testing.T) {
	// Terminal turns that ended via tool calls (handoff_next, validate_work)
	// typically carry no assistant prose. The validator must be a no-op
	// for empty text.
	if err := ValidateAgentReportShape("", TesterTurnReportShapeProfile); err != nil {
		t.Fatalf("ValidateAgentReportShape on empty text = %v, want nil", err)
	}
	if err := ValidateAgentReportShape("   \n  \n", TesterTurnReportShapeProfile); err != nil {
		t.Fatalf("ValidateAgentReportShape on whitespace-only = %v, want nil", err)
	}
}

func TestValidateAgentReportShape_TolaratesCodeBlocksAndBlockquotes(t *testing.T) {
	report := `## Tester Turn Report

**Status:** Verified

### Summary

Below shows the failing assertion captured during the suite run.

### Findings

` + "```" + `python
def test_pep517_compliance():
    assert metadata.is_compliant()
` + "```" + `

> The assertion fires because pyproject.toml is missing the build-system table.

### Next

Engineer must add the build-system declaration.`

	if err := ValidateAgentReportShape(report, TesterTurnReportShapeProfile); err != nil {
		t.Fatalf("ValidateAgentReportShape on report with code block + blockquote = %v, want nil", err)
	}
}
