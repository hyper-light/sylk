package boot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/google/uuid"
)

const bootClaimsAgent = "system:boot"

// newBootBoard creates an ephemeral claims board for boot phase tracking.
// Returns nil when scope is nil (tests, standalone), making all downstream
// calls no-ops.
func newBootBoard(scope claims.ScopeProvider) *claims.ClaimsBoard {
	if scope == nil {
		return nil
	}
	return claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID: "boot-" + uuid.NewString()[:8],
		TaskID:  "boot",
		Scope:   scope,
	})
}

// postBootPhaseClaim posts a claim for a boot phase. Returns the claim ID
// for testament linking. Returns empty string on nil board.
func postBootPhaseClaim(board *claims.ClaimsBoard, phase string, order int) string {
	if board == nil {
		return ""
	}
	action := claims.Action{
		AgentID: bootClaimsAgent,
		Type:    claims.ActionTypeBoot,
	}
	claim := claims.Claim{
		Title:       fmt.Sprintf("Boot phase: %s", phase),
		Description: fmt.Sprintf("Boot pipeline phase %d: %s", order, phase),
		ActionType:  claims.ActionTypeBoot,
		Priority:    order,
		Relations: []claims.Relation{
			{Related: bootClaimsAgent, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: bootClaimsAgent, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{
			Type:        claims.ValidationTypeReceipt,
			Required:    true,
			Description: fmt.Sprintf("Phase %s completed", phase),
			Status:      claims.ValidationStatusPending,
		}},
	}
	posted := []claims.Claim{claim}
	if err := board.PostAction(context.Background(), action, posted); err != nil {
		slog.Error("boot_phase_claim_failed", "phase", phase, "error", err.Error())
		return ""
	}
	if len(posted) > 0 {
		return posted[0].ID
	}
	return ""
}

// submitBootPhaseTestament submits a success testament for a completed phase.
func submitBootPhaseTestament(board *claims.ClaimsBoard, phase, claimID string, dur time.Duration, extra []*claims.Artifact) {
	if board == nil || claimID == "" {
		return
	}
	artifacts := []*claims.Artifact{phaseTimingArtifact(phase, dur)}
	artifacts = append(artifacts, extra...)

	action := claims.Action{AgentID: bootClaimsAgent, Type: claims.ActionTypeTestament}
	testament := claims.Testament{
		AgentID:    bootClaimsAgent,
		Summary:    fmt.Sprintf("Boot phase %s completed in %s", phase, dur.Round(time.Millisecond)),
		Confidence: "high",
		Artifacts:  artifacts,
		Relations:  claimRelation(claimID),
	}
	if err := board.SubmitTestaments(context.Background(), action, []claims.Testament{testament}); err != nil {
		slog.Error("boot_phase_testament_failed", "phase", phase, "error", err.Error())
	}
}

// submitBootPhaseError submits an error testament for a failed phase.
func submitBootPhaseError(board *claims.ClaimsBoard, phase, claimID string, phaseErr error) {
	if board == nil || claimID == "" {
		return
	}
	action := claims.Action{AgentID: bootClaimsAgent, Type: claims.ActionTypeTestament}
	testament := claims.Testament{
		AgentID:    bootClaimsAgent,
		Summary:    fmt.Sprintf("Boot phase %s failed: %s", phase, phaseErr.Error()),
		Confidence: "high",
		Artifacts: []*claims.Artifact{{
			Kind:      claims.ArtifactKindError,
			Reference: phaseErr.Error(),
			AgentID:   bootClaimsAgent,
		}},
		Relations: claimRelation(claimID),
	}
	if err := board.SubmitTestaments(context.Background(), action, []claims.Testament{testament}); err != nil {
		slog.Error("boot_phase_error_testament_failed", "phase", phase, "error", err.Error())
	}
}

// acceptBootClaims evaluates all receipt validations on the board to
// transition testified claims to accepted.
func acceptBootClaims(board *claims.ClaimsBoard) {
	if board == nil {
		return
	}
	proj := board.Projection()
	if proj == nil {
		return
	}
	for _, c := range proj.Claims {
		for _, v := range c.Validations {
			if v == nil || v.Status != claims.ValidationStatusPending {
				continue
			}
			_ = board.EvaluateValidation(context.Background(), c.ID, v.ID, claims.StatusChange{
				To:      string(claims.ValidationStatusPassed),
				Reason:  "boot phase testament received",
				AgentID: bootClaimsAgent,
				Changed: time.Now(),
			})
		}
	}
}

func phaseTimingArtifact(phase string, dur time.Duration) *claims.Artifact {
	return &claims.Artifact{
		Kind:      claims.ArtifactKindTiming,
		Reference: fmt.Sprintf("%s: %s", phase, dur.Round(time.Millisecond)),
		AgentID:   bootClaimsAgent,
		Metadata: map[string]any{
			"phase":    phase,
			"duration": dur.Nanoseconds(),
		},
	}
}

func phaseStatsArtifact(result *PipelineResult) *claims.Artifact {
	if result == nil {
		return nil
	}
	stats := map[string]any{
		"files_processed":    result.FilesProcessed,
		"nodes_created":      result.NodesCreated,
		"edges_created":      result.EdgesCreated,
		"docs_created":       result.DocsCreated,
		"vectors_created":    result.VectorsCreated,
		"chunk_refs_created": result.ChunkRefsCreated,
	}
	encoded, _ := json.Marshal(stats)
	return &claims.Artifact{
		Kind:      claims.ArtifactKindStats,
		Reference: string(encoded),
		AgentID:   bootClaimsAgent,
		Metadata:  stats,
	}
}

// claimRelation builds the Relation slice that links a testament to its claim.
func claimRelation(claimID string) []claims.Relation {
	if claimID == "" {
		return nil
	}
	return []claims.Relation{{
		Related:      claimID,
		RelatedType:  claims.RelatedTypeClaim,
		Relationship: claims.RelationshipClaim,
	}}
}
