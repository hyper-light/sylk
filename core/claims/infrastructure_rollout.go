package claims

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ArtifactKindInfrastructureShadowDiff = "infrastructure_shadow_diff"
	infrastructureShadowActorID          = "sys:claims_rollout"
)

type InfrastructureShadowComparison struct {
	Subsystem   string    `json:"subsystem"`
	Critical    bool      `json:"critical"`
	ServiceHash string    `json:"service_hash,omitempty"`
	DirectHash  string    `json:"direct_hash,omitempty"`
	DiffReasons []string  `json:"diff_reasons,omitempty"`
	ComparedAt  time.Time `json:"compared_at"`
}

func CompareInfrastructureShadow(subsystem string, serviceArtifacts, directArtifacts []*Artifact) InfrastructureShadowComparison {
	comparison := InfrastructureShadowComparison{
		Subsystem:   strings.TrimSpace(subsystem),
		ServiceHash: rolloutArtifactSetHash(serviceArtifacts),
		DirectHash:  rolloutArtifactSetHash(directArtifacts),
		ComparedAt:  time.Now().UTC(),
	}
	if comparison.ServiceHash != comparison.DirectHash {
		comparison.Critical = true
		comparison.DiffReasons = append(comparison.DiffReasons, "artifact payload hash mismatch")
	}
	if len(serviceArtifacts) != len(directArtifacts) {
		comparison.Critical = true
		comparison.DiffReasons = append(comparison.DiffReasons, fmt.Sprintf("artifact count mismatch service=%d direct=%d", len(serviceArtifacts), len(directArtifacts)))
	}
	return comparison
}

func RecordInfrastructureShadowComparison(ctx context.Context, board *ClaimsBoard, comparison InfrastructureShadowComparison) (SystemEvidenceResult, error) {
	if board == nil || !comparison.Critical {
		return SystemEvidenceResult{}, nil
	}
	return RecordSystemEvidence(ctx, SystemEvidenceOptions{
		Board:        board,
		ActorID:      infrastructureShadowActorID,
		SubjectID:    infrastructureShadowActorID,
		Operation:    "infrastructure_shadow_compare",
		Status:       "diverged",
		ArtifactKind: ArtifactKindInfrastructureShadowDiff,
		ArtifactName: "infrastructure_shadow_diff",
		Reference:    "critical infrastructure shadow diff for " + comparison.Subsystem,
		Metadata: map[string]any{
			"comparison": comparison,
		},
	})
}

func rolloutArtifactSetHash(artifacts []*Artifact) string {
	signatures := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		signatures = append(signatures, stableInfrastructureHash(
			artifact.ArtifactName,
			artifact.Kind,
			artifact.DataType,
			artifact.ContentHash,
			artifact.Reference,
			stableMetadataForReplay(artifact.Metadata),
		))
	}
	sort.Strings(signatures)
	return stableInfrastructureHash(signatures)
}
