package ui

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/planapproval"
)

func TestBuildPlanApprovalMarkdownUsesPlanTextBody(t *testing.T) {
	proposal := &planapproval.Proposal{
		PlanName:    "Short name",
		PlanSummary: "Summary only",
		PlanText:    "### Plan\n\n1. Do the work.",
	}
	got := buildPlanApprovalMarkdown(proposal)
	if got != proposal.PlanText {
		t.Fatalf("markdown = %q, want plan text body", got)
	}
}

func TestBuildPlanApprovalMarkdownWarnsOnArtifactHashMismatch(t *testing.T) {
	proposal := &planapproval.Proposal{
		PlanText:       "### Plan\n\n1. Do the work.",
		PlanArtifactID: "artifact-plan",
		Metadata: map[string]any{
			"plan_artifact_content_hash": "sha256:not-the-body",
		},
	}
	got := buildPlanApprovalMarkdown(proposal)
	if !strings.Contains(got, "hash mismatch") {
		t.Fatalf("markdown = %q, want hash mismatch warning", got)
	}
	if !strings.Contains(got, proposal.PlanText) {
		t.Fatalf("markdown = %q, want fallback plan text", got)
	}
}
