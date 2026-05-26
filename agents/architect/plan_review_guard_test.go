package architect

import (
	"strings"
	"testing"
)

func TestResponseMentionsPlanReview(t *testing.T) {
	if !responseMentionsPlanReview("The plan is ready for your review.") {
		t.Fatal("expected review phrase to be detected")
	}
	if responseMentionsPlanReview("I checked the repository shape and found one risk.") {
		t.Fatal("generic response should not be treated as plan review")
	}
}

func TestGuardPlanReviewResponseAllowsCurrentArtifact(t *testing.T) {
	plan := testReviewPlan()
	markdown := formatPlanForChat(plan)
	plan.PlanMarkdownArtifactID = "artifact-plan"
	plan.PlanMarkdownReplaceKey = planMarkdownReplaceKey(plan.ID)
	plan.PlanMarkdownContentHash = planMarkdownContentHash(strings.TrimSpace(markdown))
	plan.PlanMarkdownArtifactEpoch = planMarkdownArtifactEpoch(plan)

	a := newTestArchitect(t, Config{SessionID: "ses-guard"})
	response := "The plan is ready for your review."
	if got := a.guardPlanReviewResponse(nil, response, plan); got != response {
		t.Fatalf("guard response = %q, want original", got)
	}
}

func TestGuardPlanReviewResponseRewritesWithoutArtifact(t *testing.T) {
	var a *Architect
	got := a.guardPlanReviewResponse(nil, "The plan is ready for your review.", testReviewPlan())
	if got == "The plan is ready for your review." {
		t.Fatal("expected missing artifact response to be rewritten")
	}
}
