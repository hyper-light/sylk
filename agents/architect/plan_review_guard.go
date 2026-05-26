package architect

import (
	"context"
	"fmt"
	"strings"
)

var planReviewResponsePhrases = []string{
	"review the plan",
	"take a look at the plan",
	"plan is ready",
	"ready for your review",
	"approve the plan",
}

func responseMentionsPlanReview(response string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(response), " "))
	if normalized == "" {
		return false
	}
	for _, phrase := range planReviewResponsePhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func (a *Architect) guardPlanReviewResponse(ctx context.Context, response string, plan *DesignPlan) string {
	if !responseMentionsPlanReview(response) || plan == nil {
		return response
	}
	if planHasCurrentMarkdownArtifact(plan) {
		return response
	}
	if a != nil {
		if err := a.ensurePlanMarkdownArtifact(ctx, plan); err == nil && planHasCurrentMarkdownArtifact(plan) {
			return response
		}
	}
	return planReviewArtifactUnavailableResponse(plan)
}

func planReviewArtifactUnavailableResponse(plan *DesignPlan) string {
	if plan == nil {
		return "I prepared the plan data, but I could not publish the review artifact yet, so I cannot ask you to approve it until that is fixed."
	}
	taskCount := len(plan.Tasks)
	if taskCount > 0 {
		return fmt.Sprintf("I prepared a %d-task plan, but I could not publish the review artifact yet, so I cannot ask you to approve it until that is fixed.", taskCount)
	}
	return "I prepared the plan data, but I could not publish the review artifact yet, so I cannot ask you to approve it until that is fixed."
}
