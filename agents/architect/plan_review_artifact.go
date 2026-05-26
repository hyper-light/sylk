package architect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/google/uuid"
)

const (
	planReviewArtifactRole = "primary_review_artifact"
)

func planMarkdownReplaceKey(planID string) string {
	return "plan:" + strings.TrimSpace(planID) + ":review"
}

func planMarkdownContentHash(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func planMarkdownArtifactEpoch(plan *DesignPlan) uint64 {
	if plan == nil {
		return 0
	}
	if plan.Epoch > 0 {
		return plan.Epoch
	}
	if plan.Revision > 0 {
		return uint64(plan.Revision)
	}
	return 1
}

func (a *Architect) buildPlanMarkdownArtifact(plan *DesignPlan, priorArtifactID string) (*claims.Artifact, string, string, uint64, error) {
	if a == nil {
		return nil, "", "", 0, fmt.Errorf("architect is nil")
	}
	if plan == nil {
		return nil, "", "", 0, fmt.Errorf("plan is required")
	}
	markdown := strings.TrimSpace(formatPlanForChat(plan))
	if markdown == "" {
		return nil, "", "", 0, fmt.Errorf("plan %s has no reviewable markdown", plan.ID)
	}
	replaceKey := planMarkdownReplaceKey(plan.ID)
	contentHash := planMarkdownContentHash(markdown)
	epoch := planMarkdownArtifactEpoch(plan)
	artifact := a.architectArtifact(claims.ArtifactKindPlanMarkdown, markdown)
	artifact.ID = uuid.NewString()
	artifact.Presentation = &claims.Presentation{
		Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
		Surfaces: []claims.PresentationSurface{
			claims.PresentationSurfaceChat,
			claims.PresentationSurfaceApproval,
		},
		Format:     claims.PresentationFormatMarkdown,
		Title:      "Plan",
		Placement:  claims.PresentationPlacementBeforeResponse,
		ReplaceKey: replaceKey,
	}
	artifact.Metadata = map[string]any{
		"plan_id":      plan.ID,
		"epoch":        epoch,
		"revision":     plan.Revision,
		"task_count":   len(plan.Tasks),
		"content_hash": contentHash,
		"role":         planReviewArtifactRole,
	}
	if priorArtifactID = strings.TrimSpace(priorArtifactID); priorArtifactID != "" {
		artifact.Relations = append(artifact.Relations, claims.Relation{
			Related:      priorArtifactID,
			RelatedType:  claims.RelatedTypeArtifact,
			Relationship: claims.RelationshipSupersedes,
		})
	}
	return artifact, replaceKey, contentHash, epoch, nil
}

func planHasCurrentMarkdownArtifact(plan *DesignPlan) bool {
	if plan == nil {
		return false
	}
	markdown := strings.TrimSpace(formatPlanForChat(plan))
	if markdown == "" {
		return false
	}
	return strings.TrimSpace(plan.PlanMarkdownArtifactID) != "" &&
		strings.TrimSpace(plan.PlanMarkdownReplaceKey) == planMarkdownReplaceKey(plan.ID) &&
		strings.TrimSpace(plan.PlanMarkdownContentHash) == planMarkdownContentHash(markdown) &&
		plan.PlanMarkdownArtifactEpoch == planMarkdownArtifactEpoch(plan)
}

func (a *Architect) ensurePlanMarkdownArtifact(ctx context.Context, plan *DesignPlan) error {
	if planHasCurrentMarkdownArtifact(plan) {
		return nil
	}
	return a.publishPreparedHandoff(ctx, plan)
}
