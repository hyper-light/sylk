package claims

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ArtifactKindHandlerNondeterministic   = "handler_nondeterministic"
	ArtifactKindValidatorNondeterministic = "validator_nondeterministic"
	ArtifactKindReplayAudit               = "replay_audit"

	replayAuditActorID      = "sys:claims_replay_auditor"
	defaultReplayAuditLimit = 128
)

type ReplayAuditOptions struct {
	SourceBoard       *ClaimsBoard
	AuditBoard        *ClaimsBoard
	Participants      []ParticipantRegistration
	Handlers          map[string]ServiceHandler
	ValidatorRegistry *ValidatorRegistry
	Limit             int
	ActorID           string
}

type ReplayAuditResult struct {
	Audited      int                  `json:"audited"`
	Matched      int                  `json:"matched"`
	Diverged     int                  `json:"diverged"`
	Trusted      int                  `json:"trusted"`
	Skipped      int                  `json:"skipped"`
	Bounded      bool                 `json:"bounded"`
	Findings     []ReplayAuditFinding `json:"findings,omitempty"`
	AuditClaimID string               `json:"audit_claim_id,omitempty"`
}

type ReplayAuditFinding struct {
	ClaimID        string             `json:"claim_id,omitempty"`
	ValidationID   string             `json:"validation_id,omitempty"`
	ParticipantUID string             `json:"participant_uid,omitempty"`
	ValidatorID    string             `json:"validator_id,omitempty"`
	Determinism    HandlerDeterminism `json:"determinism,omitempty"`
	Kind           string             `json:"kind,omitempty"`
	Status         string             `json:"status"`
	Reason         string             `json:"reason,omitempty"`
	StoredHash     string             `json:"stored_hash,omitempty"`
	ReplayedHash   string             `json:"replayed_hash,omitempty"`
}

func RunReplayAudit(ctx context.Context, opts ReplayAuditOptions) (ReplayAuditResult, error) {
	if opts.SourceBoard == nil || opts.AuditBoard == nil {
		return ReplayAuditResult{}, fmt.Errorf("replay audit requires source and audit boards")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := ReplayAuditResult{}
	limit := positiveOrDefault(opts.Limit, defaultReplayAuditLimit)
	participants := participantLookup(opts.Participants)
	for _, claim := range replayAuditableClaims(opts.SourceBoard.Projection(), participants) {
		if result.Audited >= limit {
			result.Bounded = true
			break
		}
		result.Audited++
		finding := auditServiceClaim(ctx, opts, claim, participants)
		result.Findings = append(result.Findings, finding)
		incrementReplayAuditCounts(&result, finding.Status)
		if err := ctx.Err(); err != nil {
			return result, err
		}
	}
	for _, item := range replayAuditableValidations(opts.SourceBoard.Projection()) {
		if result.Audited >= limit {
			result.Bounded = true
			break
		}
		result.Audited++
		finding := auditValidator(ctx, opts, item)
		result.Findings = append(result.Findings, finding)
		incrementReplayAuditCounts(&result, finding.Status)
		if err := ctx.Err(); err != nil {
			return result, err
		}
	}
	claimID, err := recordReplayAuditReport(ctx, opts, result)
	result.AuditClaimID = claimID
	return result, err
}

func auditServiceClaim(ctx context.Context, opts ReplayAuditOptions, claim Claim, participants map[string]ParticipantRegistration) ReplayAuditFinding {
	participant := participants[strings.TrimSpace(SubjectAgentID(claim.Relations))]
	finding := ReplayAuditFinding{ClaimID: claim.ID, ParticipantUID: participant.UID, Determinism: participant.Determinism, Kind: "handler"}
	if participant.UID == "" {
		finding.Status = "skipped"
		finding.Reason = "participant not registered"
		return finding
	}
	if !replayAuditExecutes(participant.Determinism) {
		finding.Status = "trusted"
		finding.Reason = "stored output trusted for non-replayable determinism"
		return finding
	}
	handler := opts.Handlers[participant.UID]
	if handler == nil {
		handler = opts.Handlers[participant.RouteKey]
	}
	if handler == nil {
		finding.Status = "skipped"
		finding.Reason = "handler not registered for replay audit"
		return finding
	}
	result, err := handler.HandleServiceClaim(ctx, ServiceClaimRequest{
		Board:       opts.AuditBoard,
		Claim:       &claim,
		Participant: participant,
	})
	if err != nil {
		finding.Status = "diverged"
		finding.Reason = err.Error()
		return finding
	}
	stored := storedArtifactsForClaim(opts.SourceBoard.Projection(), claim.ID, participant.RouteKey)
	finding.StoredHash = replayArtifactHash(participant.Determinism, stored)
	finding.ReplayedHash = replayArtifactHash(participant.Determinism, result.Artifacts)
	if finding.StoredHash != finding.ReplayedHash {
		finding.Status = "diverged"
		finding.Reason = "replayed artifacts differ from stored artifacts"
		return finding
	}
	finding.Status = "matched"
	return finding
}

func auditValidator(ctx context.Context, opts ReplayAuditOptions, item validationRecoveryItem) ReplayAuditFinding {
	finding := ReplayAuditFinding{ClaimID: item.Claim.ID, ValidationID: item.Validation.ID, Kind: "validator"}
	if opts.ValidatorRegistry == nil {
		finding.Status = "skipped"
		finding.Reason = "validator registry not configured for replay audit"
		return finding
	}
	reg, ok := opts.ValidatorRegistry.Lookup(&item.Claim, &item.Validation)
	if !ok {
		finding.Status = "skipped"
		finding.Reason = "validator not registered for replay audit"
		return finding
	}
	finding.ValidatorID = reg.ValidatorID
	finding.Determinism = reg.Determinism
	if !replayAuditExecutes(reg.Determinism) {
		finding.Status = "trusted"
		finding.Reason = "stored output trusted for non-replayable determinism"
		return finding
	}
	target, ok := opts.SourceBoard.cloneValidationTargetArtifact(item.Claim.ID, &item.Validation)
	if !ok {
		finding.Status = "skipped"
		finding.Reason = "validator target artifact not found"
		return finding
	}
	result, err := reg.Handler.ValidateArtifact(ctx, ValidatorHandlerRequest{Artifact: target, Validation: &item.Validation, StartedAt: time.Now().UTC()})
	if err != nil {
		finding.Status = "diverged"
		finding.Reason = err.Error()
		return finding
	}
	stored := validationResultArtifact(opts.SourceBoard, item.Validation.ResultArtifactID)
	replayed := validatorReplayArtifacts(reg, item, result)
	finding.StoredHash = replayArtifactHash(reg.Determinism, stored)
	finding.ReplayedHash = replayArtifactHash(reg.Determinism, replayed)
	if finding.StoredHash != finding.ReplayedHash {
		finding.Status = "diverged"
		finding.Reason = "replayed validator artifact differs from stored artifact"
		return finding
	}
	finding.Status = "matched"
	return finding
}

func replayAuditableClaims(proj *ClaimsBoardProjection, participants map[string]ParticipantRegistration) []Claim {
	if proj == nil {
		return nil
	}
	out := make([]Claim, 0)
	for _, claim := range proj.Claims {
		if _, ok := participants[strings.TrimSpace(SubjectAgentID(claim.Relations))]; ok {
			out = append(out, claim)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sequence != out[j].Sequence {
			return out[i].Sequence < out[j].Sequence
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func replayAuditableValidations(proj *ClaimsBoardProjection) []validationRecoveryItem {
	if proj == nil {
		return nil
	}
	out := make([]validationRecoveryItem, 0)
	for _, claim := range proj.Claims {
		for _, validation := range claim.Validations {
			if validation != nil && validation.Status.IsTerminal() {
				out = append(out, validationRecoveryItem{Claim: claim, Validation: *validation})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Claim.Sequence != out[j].Claim.Sequence {
			return out[i].Claim.Sequence < out[j].Claim.Sequence
		}
		return out[i].Validation.Sequence < out[j].Validation.Sequence
	})
	return out
}

func replayAuditExecutes(determinism HandlerDeterminism) bool {
	return determinism == HandlerDeterminismPure || determinism == HandlerDeterminismContent
}

func storedArtifactsForClaim(proj *ClaimsBoardProjection, claimID, participantRoute string) []*Artifact {
	if proj == nil {
		return nil
	}
	out := make([]*Artifact, 0)
	for i := range proj.Testaments {
		testament := &proj.Testaments[i]
		if ClaimIDFromRelations(testament.Relations) != claimID {
			continue
		}
		if strings.TrimSpace(participantRoute) != "" && strings.TrimSpace(testament.AgentID) != strings.TrimSpace(participantRoute) {
			continue
		}
		for _, artifact := range testament.Artifacts {
			out = append(out, CloneArtifact(artifact))
		}
	}
	return out
}

func replayArtifactHash(determinism HandlerDeterminism, artifacts []*Artifact) string {
	signatures := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		signatures = append(signatures, replayArtifactSignature(determinism, artifact))
	}
	sort.Strings(signatures)
	return stableInfrastructureHash(signatures)
}

func replayArtifactSignature(determinism HandlerDeterminism, artifact *Artifact) string {
	if artifact == nil {
		return ""
	}
	if determinism == HandlerDeterminismContent {
		return stableInfrastructureHash(artifact.ArtifactName, artifact.Kind, artifact.DataType, artifact.ContentHash)
	}
	return stableInfrastructureHash(artifact.ArtifactName, artifact.Kind, artifact.DataType, artifact.ContentHash, artifact.Reference, stableMetadataForReplay(artifact.Metadata))
}

func validationResultArtifact(board *ClaimsBoard, artifactID string) []*Artifact {
	if board == nil || strings.TrimSpace(artifactID) == "" {
		return nil
	}
	artifact, ok := board.CloneArtifact(artifactID)
	if !ok {
		return nil
	}
	return []*Artifact{artifact}
}

func validatorReplayArtifacts(reg ValidatorRegistration, item validationRecoveryItem, result ValidatorHandlerResult) []*Artifact {
	if result.ResultArtifact != nil {
		return []*Artifact{result.ResultArtifact}
	}
	if result.Error == nil {
		return nil
	}
	req := ValidationDispatchRequest{Claim: &item.Claim, Validation: &item.Validation}
	return []*Artifact{validatorErrorArtifact(reg, req, result.Error, nil)}
}

func stableMetadataForReplay(metadata map[string]any) map[string]any {
	out := cloneMetadata(metadata)
	for _, key := range []string{"created_at", "updated_at", "timestamp", "duration", "elapsed"} {
		delete(out, key)
	}
	return out
}

func recordReplayAuditReport(ctx context.Context, opts ReplayAuditOptions, result ReplayAuditResult) (string, error) {
	if len(result.Findings) == 0 {
		return "", nil
	}
	status := "matched"
	kind := ArtifactKindReplayAudit
	if result.Diverged > 0 {
		status, kind = replayAuditDivergenceKind(result.Findings)
	}
	report, err := RecordSystemEvidence(ctx, SystemEvidenceOptions{
		Board:        opts.AuditBoard,
		ActorID:      firstNonEmpty(opts.ActorID, replayAuditActorID),
		SubjectID:    firstNonEmpty(opts.ActorID, replayAuditActorID),
		Operation:    "replay_audit",
		Status:       status,
		ArtifactKind: kind,
		ArtifactName: "replay_audit_report",
		Reference:    fmt.Sprintf("replay audit %s: audited=%d diverged=%d", status, result.Audited, result.Diverged),
		Metadata: map[string]any{
			"audited":  result.Audited,
			"matched":  result.Matched,
			"diverged": result.Diverged,
			"trusted":  result.Trusted,
			"skipped":  result.Skipped,
			"findings": result.Findings,
		},
	})
	return report.ClaimID, err
}

func replayAuditDivergenceKind(findings []ReplayAuditFinding) (string, string) {
	for _, finding := range findings {
		if finding.Status == "diverged" && finding.Kind == "validator" {
			return "diverged", ArtifactKindValidatorNondeterministic
		}
	}
	return "diverged", ArtifactKindHandlerNondeterministic
}

func incrementReplayAuditCounts(result *ReplayAuditResult, status string) {
	switch status {
	case "matched":
		result.Matched++
	case "diverged":
		result.Diverged++
	case "trusted":
		result.Trusted++
	default:
		result.Skipped++
	}
}
