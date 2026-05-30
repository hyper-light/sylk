package boot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/google/uuid"
)

const bootClaimsAgent = BootSequencerAgentID

// newBootBoard creates an ephemeral claims board for boot phase tracking.
// Returns nil when scope is nil (tests, standalone), making all downstream
// calls no-ops.
func newBootBoard(scope claims.ScopeProvider) *claims.ClaimsBoard {
	if scope == nil {
		return nil
	}
	boardID := "boot-" + uuid.NewString()
	return claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   boardID,
		SessionID: boardID,
		TaskID:    "boot",
		Scope:     scope,
	})
}

// postBootPhaseClaim posts a claim for a boot phase. Returns the claim ID
// for testament linking. Returns empty string on nil board.
func postBootPhaseClaim(board *claims.ClaimsBoard, phase string, order int) string {
	if board == nil {
		return ""
	}
	actorID := bootClaimsAgentID(board)
	generated, err := board.GenerateClaimAction(context.Background(), claims.Action{AgentID: actorID, Type: claims.ActionTypeBoot, Priority: order}, []claims.Claim{newPipelineBootClaim(board, phase, order)}, claims.GenerateClaimActionOptions{
		IdempotencyKey: pipelineBootClaimKey(phase),
		Reason:         "boot sequencer phase claim generated",
	})
	if err != nil {
		slog.Error("boot_phase_claim_failed", "phase", phase, "error", err.Error())
		return ""
	}
	claimID := generated.Claims[0].ID
	if err := postPipelineBootClaim(context.Background(), board, claimID); err != nil {
		slog.Error("boot_phase_claim_post_failed", "phase", phase, "error", err.Error())
		return ""
	}
	return claimID
}

// submitBootPhaseTestament submits a success testament for a completed phase.
func submitBootPhaseTestament(board *claims.ClaimsBoard, phase, claimID string, dur time.Duration, extra []*claims.Artifact) {
	if board == nil || claimID == "" {
		return
	}
	artifacts, err := pipelineBootPhaseArtifacts(context.Background(), board, phase, dur, "", extra)
	if err != nil {
		slog.Error("boot_phase_artifact_failed", "phase", phase, "error", err.Error())
		return
	}
	err = submitPipelineBootPhaseTestament(context.Background(), board, phase, claimID, dur, claims.ValidationStatusPassed, claims.TestamentLifecycleValidated, artifacts)
	if err != nil {
		slog.Error("boot_phase_testament_failed", "phase", phase, "error", err.Error())
	}
}

// submitBootPhaseError submits an error testament for a failed phase.
func submitBootPhaseError(board *claims.ClaimsBoard, phase, claimID string, phaseErr error) {
	if board == nil || claimID == "" {
		return
	}
	errText := "unknown boot phase error"
	if phaseErr != nil {
		errText = phaseErr.Error()
	}
	artifacts, err := pipelineBootPhaseArtifacts(context.Background(), board, phase, 0, errText, nil)
	if err != nil {
		slog.Error("boot_phase_error_artifact_failed", "phase", phase, "error", err.Error())
		return
	}
	err = submitPipelineBootPhaseTestament(context.Background(), board, phase, claimID, 0, claims.ValidationStatusFailed, claims.TestamentLifecycleValidationFailed, artifacts)
	if err != nil {
		slog.Error("boot_phase_error_testament_failed", "phase", phase, "error", err.Error())
	}
}

// acceptBootClaims is retained for the pipeline call site. Phase claims
// now transition only through generated testament validation; there is no
// fallback that marks pending receipt validations as passed.
func acceptBootClaims(board *claims.ClaimsBoard) {
	_ = board
}

func newPipelineBootClaim(board *claims.ClaimsBoard, phase string, order int) claims.Claim {
	processUID := bootClaimsProcessUID(board)
	actorID := bootClaimsAgentID(board)
	return claims.Claim{
		AgentID:     actorID,
		Title:       fmt.Sprintf("Boot phase: %s", phase),
		Description: fmt.Sprintf("Boot pipeline phase %d: %s", order, phase),
		ActionType:  claims.ActionTypeBoot,
		Priority:    order,
		Relations: []claims.Relation{
			{Related: actorID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: actorID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		ExpectedToolCalls: []claims.ExpectedToolCall{{
			Tool: BootPhaseToolFor(phase),
			Arguments: map[string]any{
				"phase":       phase,
				"phase_order": order,
				"process_uid": processUID,
			},
		}},
		Validations: []*claims.Validation{{
			ID:          validationIDForKey(pipelineBootClaimKey(phase)),
			Type:        claims.ValidationTypeReceipt,
			Required:    true,
			Description: fmt.Sprintf("Phase %s completed", phase),
			Status:      claims.ValidationStatusPending,
		}},
	}
}

func pipelineBootServiceClaim(board *claims.ClaimsBoard, phase string, data claims.BootPhaseArtifactData) *claims.Claim {
	if board == nil {
		return &claims.Claim{ExpectedToolCalls: []claims.ExpectedToolCall{{Tool: BootPhaseToolFor(phase), Arguments: bootPhaseArguments(data)}}}
	}
	claimID := ""
	for _, claim := range board.ClaimsByLifecycleStatus(claims.ClaimLifecycleProgressed) {
		if claim != nil && claimExpectedBootPhase(claim, phase) {
			claimID = claim.ID
			break
		}
	}
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		fallback := newPipelineBootClaim(board, phase, data.PhaseOrder)
		claim = &fallback
	}
	claim.ExpectedToolCalls = []claims.ExpectedToolCall{{Tool: BootPhaseToolFor(phase), Arguments: bootPhaseArguments(data)}}
	return claim
}

func claimExpectedBootPhase(claim *claims.Claim, phase string) bool {
	if claim == nil {
		return false
	}
	for _, call := range claim.ExpectedToolCalls {
		if bootPhaseForTool(call.Tool) == phase || bootPhaseStringArg(call.Arguments, "phase") == phase {
			return true
		}
	}
	return false
}

func bootPhaseArguments(data claims.BootPhaseArtifactData) map[string]any {
	return map[string]any{
		"phase":          data.Phase,
		"phase_order":    data.PhaseOrder,
		"process_uid":    data.ProcessUID,
		"started_at":     data.StartedAt,
		"ended_at":       data.EndedAt,
		"duration":       data.Duration,
		"ready":          data.Ready,
		"prior_phases":   data.PriorPhases,
		"artifact_refs":  data.ArtifactRefs,
		"status":         data.Status,
		"failure_reason": data.FailureReason,
		"metadata":       data.Metadata,
	}
}

func postPipelineBootClaim(ctx context.Context, board *claims.ClaimsBoard, claimID string) error {
	if err := postPipelineClaimIfGenerated(ctx, board, claimID); err != nil {
		return err
	}
	if err := acknowledgePipelineClaimIfPosted(ctx, board, claimID); err != nil {
		return err
	}
	return progressPipelineClaimIfOpen(ctx, board, claimID)
}

func postPipelineClaimIfGenerated(ctx context.Context, board *claims.ClaimsBoard, claimID string) error {
	claim, ok := board.CloneClaim(claimID)
	if !ok || claim.LifecycleStatus != claims.ClaimLifecycleGenerated {
		return nil
	}
	err := board.PostGeneratedClaim(ctx, claimID, bootClaimsAgentID(board), claims.ClaimPostOptions{Reason: "boot sequencer phase claim posted"})
	return ignorePostRace(err, func() bool { return pipelineClaimBeyond(board, claimID, claims.ClaimLifecycleGenerated) })
}

func acknowledgePipelineClaimIfPosted(ctx context.Context, board *claims.ClaimsBoard, claimID string) error {
	claim, ok := board.CloneClaim(claimID)
	if !ok || claim.LifecycleStatus != claims.ClaimLifecyclePosted {
		return nil
	}
	err := board.AcknowledgeClaimReceipt(ctx, claimID, bootClaimsAgentID(board))
	return ignorePostRace(err, func() bool { return pipelineClaimBeyond(board, claimID, claims.ClaimLifecyclePosted) })
}

func progressPipelineClaimIfOpen(ctx context.Context, board *claims.ClaimsBoard, claimID string) error {
	claim, ok := board.CloneClaim(claimID)
	if !ok || claim.Status.IsTerminal() || claim.LifecycleStatus == claims.ClaimLifecycleProgressed {
		return nil
	}
	err := board.UpdateClaimProgress(ctx, claimID, claims.ClaimProgressUpdate{WorkSummary: "boot phase in progress"}, bootClaimsAgentID(board))
	return ignorePostRace(err, func() bool { return pipelineClaimBeyond(board, claimID, claims.ClaimLifecycleReceived) })
}

func pipelineBootPhaseArtifacts(ctx context.Context, board *claims.ClaimsBoard, phase string, dur time.Duration, failure string, extra []*claims.Artifact) ([]*claims.Artifact, error) {
	ended := time.Now().UTC()
	status := claims.InfrastructureStatusOK
	ready := true
	if failure != "" {
		status = claims.InfrastructureStatusFailed
		ready = false
	}
	processUID := bootClaimsProcessUID(board)
	service := NewBootSequencerService(BootSequencerServiceConfig{ProcessUID: processUID})
	data := claims.BootPhaseArtifactData{
		Phase:         phase,
		PhaseOrder:    bootPhaseOrder(phase),
		ProcessUID:    processUID,
		StartedAt:     ended.Add(-dur),
		EndedAt:       ended,
		Duration:      dur,
		Ready:         ready,
		PriorPhases:   pipelinePriorPhases(phase),
		ArtifactRefs:  artifactRefs(extra),
		Status:        status,
		FailureReason: failure,
		Metadata: map[string]any{
			"phase":       phase,
			"duration_ns": dur.Nanoseconds(),
		},
	}
	claim := pipelineBootServiceClaim(board, phase, data)
	result, err := service.HandleServiceClaim(ctx, claims.ServiceClaimRequest{Board: board, Claim: claim})
	if err != nil {
		return nil, err
	}
	artifacts := make([]*claims.Artifact, 0, len(result.Artifacts)+len(extra))
	for _, artifact := range result.Artifacts {
		if artifact == nil {
			continue
		}
		artifact = scopeBootArtifact(board, artifact)
		artifacts = append(artifacts, artifact)
	}
	for _, artifact := range extra {
		if artifact == nil {
			continue
		}
		artifacts = append(artifacts, scopeBootArtifact(board, artifact))
	}
	return artifacts, nil
}

func submitPipelineBootPhaseTestament(ctx context.Context, board *claims.ClaimsBoard, phase, claimID string, dur time.Duration, validationStatus claims.ValidationStatus, testamentStatus claims.TestamentLifecycleStatus, artifacts []*claims.Artifact) error {
	actorID := bootClaimsAgentID(board)
	generated, err := board.GenerateTestamentAction(ctx, claims.Action{AgentID: actorID, Type: claims.ActionTypeTestament, Status: claims.ActionStatusComplete}, []claims.Testament{{
		AgentID:        actorID,
		Summary:        pipelineBootSummary(phase, validationStatus, artifacts),
		Confidence:     "deterministic",
		Duration:       dur,
		Artifacts:      artifacts,
		Relations:      claimRelation(claimID),
		IdempotencyKey: pipelineBootTestamentKey(phase, claimID, validationStatus),
	}}, claims.GenerateTestamentActionOptions{
		IdempotencyKey: pipelineBootTestamentKey(phase, claimID, validationStatus),
		Reason:         "boot sequencer phase testament generated",
	})
	if err != nil {
		return err
	}
	testamentID := generated.Testaments[0].ID
	if err := postPipelineTestamentIfGenerated(ctx, board, testamentID); err != nil {
		return err
	}
	return validatePipelineBootTestament(ctx, board, claimID, testamentID, validationStatus, testamentStatus)
}

func postPipelineTestamentIfGenerated(ctx context.Context, board *claims.ClaimsBoard, testamentID string) error {
	testament, ok := board.CloneTestament(testamentID)
	if !ok || testament.LifecycleStatus != claims.TestamentLifecycleGenerated {
		return nil
	}
	err := board.PostGeneratedTestament(ctx, testamentID, bootClaimsAgentID(board), claims.TestamentPostOptions{Reason: "boot sequencer phase testament posted"})
	return ignorePostRace(err, func() bool { return pipelineTestamentBeyond(board, testamentID, claims.TestamentLifecycleGenerated) })
}

func validatePipelineBootTestament(ctx context.Context, board *claims.ClaimsBoard, claimID, testamentID string, validationStatus claims.ValidationStatus, testamentStatus claims.TestamentLifecycleStatus) error {
	if err := acknowledgePipelineTestamentIfPosted(ctx, board, testamentID); err != nil {
		return err
	}
	if err := beginPipelineTestamentValidationIfReceived(ctx, board, testamentID); err != nil {
		return err
	}
	if err := evaluatePipelineReceiptValidations(ctx, board, claimID, validationStatus); err != nil {
		return err
	}
	testament, ok := board.CloneTestament(testamentID)
	if !ok || testament.LifecycleStatus != claims.TestamentLifecycleValidating {
		return terminalTestamentError(testament)
	}
	return board.CompleteTestamentValidation(ctx, testamentID, bootClaimsAgentID(board), testamentStatus, "boot phase validation completed")
}

func acknowledgePipelineTestamentIfPosted(ctx context.Context, board *claims.ClaimsBoard, testamentID string) error {
	testament, ok := board.CloneTestament(testamentID)
	if !ok || testament.LifecycleStatus != claims.TestamentLifecyclePosted {
		return nil
	}
	err := board.AcknowledgeTestamentReceipt(ctx, testamentID, bootClaimsAgentID(board))
	return ignorePostRace(err, func() bool { return pipelineTestamentBeyond(board, testamentID, claims.TestamentLifecyclePosted) })
}

func beginPipelineTestamentValidationIfReceived(ctx context.Context, board *claims.ClaimsBoard, testamentID string) error {
	testament, ok := board.CloneTestament(testamentID)
	if !ok || testament.LifecycleStatus != claims.TestamentLifecycleReceived {
		return nil
	}
	err := board.BeginTestamentValidation(ctx, testamentID, bootClaimsAgentID(board))
	return ignorePostRace(err, func() bool { return pipelineTestamentBeyond(board, testamentID, claims.TestamentLifecycleReceived) })
}

func evaluatePipelineReceiptValidations(ctx context.Context, board *claims.ClaimsBoard, claimID string, status claims.ValidationStatus) error {
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		return fmt.Errorf("boot claim %q not found", claimID)
	}
	for _, validation := range claim.Validations {
		if validation == nil || validation.Type != claims.ValidationTypeReceipt {
			continue
		}
		if validation.Status.IsTerminal() {
			continue
		}
		if err := board.EvaluateValidation(ctx, claimID, validation.ID, claims.StatusChange{AgentID: bootClaimsAgentID(board), To: string(status), Reason: "boot phase testament received"}); err != nil {
			return err
		}
	}
	return nil
}

func pipelineClaimBeyond(board *claims.ClaimsBoard, claimID string, status claims.ClaimLifecycleStatus) bool {
	claim, ok := board.CloneClaim(claimID)
	return ok && claim.LifecycleStatus != status
}

func pipelineTestamentBeyond(board *claims.ClaimsBoard, testamentID string, status claims.TestamentLifecycleStatus) bool {
	testament, ok := board.CloneTestament(testamentID)
	return ok && testament.LifecycleStatus != status
}

func pipelineBootClaimKey(phase string) string {
	return "boot.pipeline.phase." + phase
}

func pipelineBootTestamentKey(phase, claimID string, status claims.ValidationStatus) string {
	return "boot.pipeline.testament." + phase + "." + claimID + "." + string(status)
}

func pipelineBootSummary(phase string, status claims.ValidationStatus, artifacts []*claims.Artifact) string {
	if status == claims.ValidationStatusPassed {
		return "boot.phase_" + phase + "_complete"
	}
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		if data, err := claims.ArtifactData[claims.BootPhaseArtifactData](artifact); err == nil && data.FailureReason != "" {
			return "boot.phase_" + phase + "_failed: " + data.FailureReason
		}
		if artifact.Reference != "" {
			return "boot.phase_" + phase + "_failed: " + artifact.Reference
		}
	}
	return "boot.phase_" + phase + "_failed"
}

func pipelinePriorPhases(phase string) []string {
	order := bootPhaseOrder(phase)
	if order <= 1 {
		return nil
	}
	prior := bootPhaseSequence()[:order-1]
	return append([]string(nil), prior...)
}

func artifactRefs(artifacts []*claims.Artifact) []string {
	out := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		out = append(out, firstNonEmptyString(artifact.ID, artifact.ArtifactName, artifact.Kind, artifact.Reference))
	}
	return out
}

func phaseTimingArtifact(phase string, dur time.Duration) *claims.Artifact {
	return phaseTimingArtifactForAgent(bootClaimsAgent, phase, dur)
}

func scopeBootArtifact(board *claims.ClaimsBoard, artifact *claims.Artifact) *claims.Artifact {
	if artifact == nil {
		return nil
	}
	actorID := bootClaimsAgentID(board)
	artifact.AgentID = firstNonEmptyString(artifact.AgentID, actorID)
	artifact.ParticipantID = firstNonEmptyString(artifact.ParticipantID, actorID)
	if data, err := claims.ArtifactData[claims.BootPhaseArtifactData](artifact); err == nil {
		data.ProcessUID = firstNonEmptyString(data.ProcessUID, bootClaimsProcessUID(board))
		if setErr := claims.SetArtifactData(artifact, data); setErr != nil {
			artifact.Metadata = mergeBootMetadata(artifact.Metadata, map[string]any{"boot_artifact_data_error": setErr.Error()})
		}
	}
	return artifact
}

func mergeBootMetadata(left, right map[string]any) map[string]any {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	out := make(map[string]any, len(left)+len(right))
	for key, value := range left {
		out[strings.TrimSpace(key)] = value
	}
	for key, value := range right {
		out[strings.TrimSpace(key)] = value
	}
	return out
}

func bootClaimsAgentID(board *claims.ClaimsBoard) string {
	processUID := bootClaimsProcessUID(board)
	if processUID == "" {
		return bootClaimsAgent
	}
	return processScopedAgentID(bootClaimsAgent, processUID)
}

func bootClaimsProcessUID(board *claims.ClaimsBoard) string {
	if board == nil {
		return ""
	}
	return firstNonEmptyString(board.SessionID(), board.BoardID())
}

func phaseTimingArtifactForAgent(agentID, phase string, dur time.Duration) *claims.Artifact {
	ended := time.Now().UTC()
	artifact, err := claims.NewBootPhaseArtifact(claims.BootPhaseArtifactData{
		Phase:         phase,
		PhaseOrder:    firstNonEmptyInt(bootPhaseOrder(phase), phaseOrder(BootOperationPhase(phase))),
		ProcessUID:    processUIDFromBootAgentID(agentID),
		StartedAt:     ended.Add(-dur),
		EndedAt:       ended,
		Duration:      dur,
		Ready:         true,
		Status:        claims.InfrastructureStatusOK,
		FailureReason: "",
		Metadata: map[string]any{
			"phase":       phase,
			"duration_ns": dur.Nanoseconds(),
		},
	})
	if err == nil {
		artifact.AgentID = firstNonEmptyString(agentID, bootClaimsAgent)
		return artifact
	}
	return &claims.Artifact{Kind: claims.ArtifactKindTiming, Reference: fmt.Sprintf("%s: %s", phase, dur.Round(time.Millisecond)), AgentID: firstNonEmptyString(agentID, bootClaimsAgent)}
}

func firstNonEmptyInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func processUIDFromBootAgentID(agentID string) string {
	const marker = ":proc/"
	idx := strings.LastIndex(agentID, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(agentID[idx+len(marker):])
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
