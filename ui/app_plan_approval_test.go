package ui

import (
	"testing"

	"github.com/adalundhe/sylk/core/planapproval"
)

func TestPlanApprovalResponseTargetUsesProposalMetadata(t *testing.T) {
	proposal := &planapproval.Proposal{
		Metadata: map[string]any{
			"target_agent_id": "guardian-session",
		},
	}
	if got := planApprovalResponseTarget(proposal); got != "guardian-session" {
		t.Fatalf("target = %q, want guardian-session", got)
	}
}

func TestPlanApprovalResponseTargetDefaultsToGuardian(t *testing.T) {
	if got := planApprovalResponseTarget(&planapproval.Proposal{}); got != "guardian" {
		t.Fatalf("target = %q, want guardian", got)
	}
}
