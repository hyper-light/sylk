package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/planapproval"
	"github.com/adalundhe/sylk/ui/bridge"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestPlanApprovalLayoutIsDecisionOnly(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 40}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	app.planApproval = &planApprovalState{
		proposal: &planapproval.Proposal{
			PlanName:    "This should not render",
			PlanSummary: "This summary should not render",
			PlanText:    "### Plan\n\n- this task body belongs in the chat artifact",
		},
		selected:  0,
		activated: -1,
	}

	layout := app.planApprovalLayout(max(app.width-2, 1))
	if got := len(layout.lines); got > planApprovalMaxInnerLines {
		t.Fatalf("layout inner lines = %d, want <= %d:\n%s", got, planApprovalMaxInnerLines, strings.Join(layout.lines, "\n"))
	}
	if got, want := app.planApprovalHeight(), planApprovalMaxInnerLines+inputBorderSize; got > want {
		t.Fatalf("planApprovalHeight() = %d, want <= %d", got, want)
	}
	rendered := app.renderPlanApprovalView()
	if got, want := strings.Count(rendered, "\n")+1, planApprovalMaxInnerLines+inputBorderSize; got > want {
		t.Fatalf("rendered lines = %d, want <= %d:\n%s", got, want, rendered)
	}
	if layout.codeVisibleLines != 0 || layout.codeTotalLines != 0 {
		t.Fatalf("approval dialog rendered a scrollable body: visible=%d total=%d", layout.codeVisibleLines, layout.codeTotalLines)
	}

	joined := ansi.Strip(strings.Join(layout.lines, "\n"))
	for _, want := range []string{
		"Approve, modify, or reject this plan:",
		"Status: ready",
		"• Approve (launch the plan)",
		"• Modify (ask for changes)",
		"• Reject (scrap, choose different direction)",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("plan approval layout missing %q:\n%s", want, joined)
		}
	}
	for _, notWant := range []string{"this task body belongs", "This should not render", "This summary should not render"} {
		if strings.Contains(joined, notWant) {
			t.Fatalf("approval dialog rendered plan content %q:\n%s", notWant, joined)
		}
	}

	if app.scrollPlanApprovalBody(layout, 1) {
		t.Fatal("plan approval dialog should not scroll plan body")
	}
}

func TestHydratePlanApprovalProposalUsesClaimsArtifact(t *testing.T) {
	sessionID := "ses-plan-approval-artifact"
	artifactBody := "### Plan\n\n1. Use the board artifact."
	artifact := &claims.Artifact{
		ID:        "artifact-plan-md",
		Kind:      claims.ArtifactKindPlanMarkdown,
		Reference: artifactBody,
		Presentation: &claims.Presentation{
			Audiences:  []claims.PresentationAudience{claims.PresentationAudienceUser},
			Surfaces:   []claims.PresentationSurface{claims.PresentationSurfaceApproval},
			Format:     claims.PresentationFormatMarkdown,
			ReplaceKey: "plan:plan-1:review",
		},
		Metadata: map[string]any{
			"plan_id":      "plan-1",
			"epoch":        uint64(3),
			"content_hash": planApprovalMarkdownHash(artifactBody),
		},
	}
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-" + sessionID,
		SessionID: sessionID,
	})
	if err := board.SubmitTestaments(context.Background(), claims.Action{AgentID: "architect", Type: claims.ActionTypeTestament}, []claims.Testament{{
		AgentID:    "architect",
		Summary:    "Plan ready for review.",
		Confidence: "committed",
		Artifacts:  []*claims.Artifact{artifact},
	}}); err != nil {
		t.Fatalf("submit testament: %v", err)
	}
	registry := claims.DefaultSessionBoardRegistry()
	registry.ReplaceForReason(sessionID, board, "test")
	t.Cleanup(func() { registry.Remove(sessionID) })
	br := bridge.NewClaimsBridge("test.claims", registry, nil)
	br.SwitchSession(sessionID)

	model := &AppModel{claimsBridge: br}
	proposal := &planapproval.Proposal{
		PlanID:                 "plan-1",
		SessionID:              sessionID,
		PlanText:               "fallback text that should not render",
		PlanArtifactID:         artifact.ID,
		PlanArtifactReplaceKey: "stale-replace-key",
	}
	got := model.hydratePlanApprovalProposal(proposal)
	if got.PlanText != artifactBody {
		t.Fatalf("PlanText = %q, want artifact body", got.PlanText)
	}
	if got.PlanArtifactReplaceKey != "plan:plan-1:review" {
		t.Fatalf("PlanArtifactReplaceKey = %q", got.PlanArtifactReplaceKey)
	}
	if resolved, _ := planApprovalMetadataBool(got.Metadata, "plan_artifact_resolved"); !resolved {
		t.Fatalf("plan_artifact_resolved = %#v, want true", got.Metadata["plan_artifact_resolved"])
	}
}
