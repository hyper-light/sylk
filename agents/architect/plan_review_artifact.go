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
	if correlationID := strings.TrimSpace(plan.RequestCorrelationID); correlationID != "" {
		artifact.Metadata["request_correlation_id"] = correlationID
		artifact.Metadata["stream_correlation_id"] = correlationID
		artifact.Metadata["correlation_id"] = correlationID
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

func (a *Architect) planHasCurrentMarkdownArtifactOnBoard(plan *DesignPlan) bool {
	if a == nil || !planHasCurrentMarkdownArtifact(plan) {
		return false
	}
	board := a.architectBoard()
	if board == nil {
		return false
	}
	artifact, ok := board.CloneArtifact(strings.TrimSpace(plan.PlanMarkdownArtifactID))
	if !ok || artifact == nil {
		return false
	}
	return planMarkdownArtifactMatchesPlan(artifact, plan)
}

func planMarkdownArtifactMatchesPlan(artifact *claims.Artifact, plan *DesignPlan) bool {
	if artifact == nil || plan == nil {
		return false
	}
	if strings.TrimSpace(artifact.ID) != strings.TrimSpace(plan.PlanMarkdownArtifactID) {
		return false
	}
	if strings.TrimSpace(artifact.Kind) != claims.ArtifactKindPlanMarkdown {
		return false
	}
	markdown := strings.TrimSpace(formatPlanForChat(plan))
	if markdown == "" || strings.TrimSpace(artifact.Reference) != markdown {
		return false
	}
	presentation := claims.NormalizePresentation(artifact.Presentation)
	if !claims.PresentationMatches(presentation, string(claims.PresentationAudienceUser), string(claims.PresentationSurfaceChat)) {
		return false
	}
	if strings.TrimSpace(presentation.ReplaceKey) != strings.TrimSpace(plan.PlanMarkdownReplaceKey) {
		return false
	}
	hash := planReviewArtifactMetadataString(artifact.Metadata, "content_hash", "plan_artifact_content_hash")
	return strings.TrimSpace(hash) == strings.TrimSpace(plan.PlanMarkdownContentHash)
}

func planReviewArtifactMetadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if metadata == nil {
			return ""
		}
		if value := strings.TrimSpace(stringFromAny(metadata[key])); value != "" {
			return value
		}
	}
	return ""
}

func (a *Architect) ensurePlanMarkdownArtifact(ctx context.Context, plan *DesignPlan) error {
	if a.planHasCurrentMarkdownArtifactOnBoard(plan) {
		return nil
	}
	return a.publishPreparedHandoff(ctx, plan)
}
