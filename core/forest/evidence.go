package forest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const (
	artifactEdgeGeneratedBy         = "generated_by"
	artifactEdgeAttachedToClaim     = "attached_to_claim"
	artifactEdgeAttachedToTestament = "attached_to_testament"
	artifactEdgeValidationResult    = "validation_result_for"
	artifactEdgeValidates           = "validates"
	artifactEdgeInvalidates         = "invalidates"
	artifactEdgeSupersedes          = "supersedes"
	artifactEdgeRemediates          = "remediates"
)

var errEvidenceProjectionRejected = errors.New("forest evidence projection rejected")

func projectCanonicalDeltaEvidenceTx(ctx context.Context, tx *sql.Tx, delta claims.CanonicalDelta, ledgerID string, seq int64, payloadHash string) error {
	classification, reason, sideEffect := classifyCanonicalDeltaProjection(delta.Action)
	if err := recordDeltaProjectionAuditTx(ctx, tx, ledgerID, delta.Action, classification, reason, sideEffect); err != nil {
		return err
	}
	if _, ok := claims.DeltaActionArtifactLifecycleStatus(delta.Action); ok {
		return projectArtifactDeltaTx(ctx, tx, delta, ledgerID, seq, payloadHash)
	}
	if _, ok := claims.DeltaActionValidationLifecycleStatus(delta.Action); ok {
		return projectValidationDeltaTx(ctx, tx, delta, ledgerID, seq, payloadHash)
	}
	if delta.Action == claims.DeltaActionValidationEvaluated {
		return projectValidationDeltaTx(ctx, tx, delta, ledgerID, seq, payloadHash)
	}
	return nil
}

func classifyCanonicalDeltaProjection(action claims.DeltaAction) (string, string, string) {
	if _, ok := claims.DeltaActionArtifactLifecycleStatus(action); ok {
		return "projected", "artifact lifecycle delta updates artifact evidence", "artifact_evidence"
	}
	if _, ok := claims.DeltaActionValidationLifecycleStatus(action); ok || action == claims.DeltaActionValidationEvaluated {
		return "projected", "validation delta updates validation evidence", "validation_evidence"
	}
	if _, ok := claims.DeltaActionClaimLifecycleStatus(action); ok {
		return "ledger_only", "claim lifecycle truth is preserved in forest_ledger for node projection", "claim_lifecycle_ledger"
	}
	if _, ok := claims.DeltaActionTestamentLifecycleStatus(action); ok {
		return "ledger_only", "testament lifecycle truth is preserved in forest_ledger for node projection", "testament_lifecycle_ledger"
	}
	return "unknown_observed_delta", "future canonical action recorded without projection side effect", "none"
}

func projectArtifactDeltaTx(ctx context.Context, tx *sql.Tx, delta claims.CanonicalDelta, ledgerID string, seq int64, payloadHash string) error {
	boardSeq := int64(delta.Sequence)
	artifactID := delta.RefID("artifact", claims.RelatedTypeArtifact)
	if artifactID == "" {
		return rejectEvidenceTx(ctx, tx, ledgerID, claims.RelatedTypeArtifact, "", "missing_artifact_ref", "artifact lifecycle delta has no artifact ref", boardSeq)
	}
	current, err := currentEvidenceSequenceTx(ctx, tx, "forest_artifacts", "artifact_id", artifactID)
	if err != nil {
		return err
	}
	if current > boardSeq {
		return rejectEvidenceTx(ctx, tx, ledgerID, claims.RelatedTypeArtifact, artifactID, "sequence_regression", "artifact lifecycle sequence regressed", boardSeq)
	}
	artifact := mapFromContext(delta.Context, "artifact")
	claim := mapFromContext(delta.Context, "claim")
	testament := mapFromContext(delta.Context, "testament")
	status, _ := claims.DeltaActionArtifactLifecycleStatus(delta.Action)
	statusText := firstNonEmptyString(string(status), mapString(artifact, "status"))
	claimID := firstNonEmptyString(delta.RefID("claim", claims.RelatedTypeClaim), mapString(artifact, "claim_id"), mapString(claim, "id"))
	testamentID := firstNonEmptyString(delta.RefID("testament", claims.RelatedTypeTestament), mapString(artifact, "testament_id"), mapString(testament, "id"))
	validationID := delta.RefID("validation", claims.RelatedTypeValidation)
	contentHash := firstNonEmptyString(mapString(artifact, "content_hash"), deriveInlineArtifactContentHash(artifact))
	contentRef := firstNonEmptyString(mapString(artifact, "reference"), mapString(artifact, "content_ref"))
	if contentHash == "" && contentRef == "" {
		unavailableReason := firstNonEmptyString(mapString(artifact, "unavailable_reason"), mapString(artifact, "content_unavailable_reason"))
		if unavailableReason == "" {
			return rejectEvidenceTx(ctx, tx, ledgerID, claims.RelatedTypeArtifact, artifactID, "missing_content_identity", "artifact has no content hash, content ref, inline content, or unavailable reason", boardSeq)
		}
		contentRef = "unavailable:" + unavailableReason
	}
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_artifacts
			(artifact_id, claim_id, testament_id, validation_id, generator_participant,
			 artifact_name, artifact_kind, data_type, content_hash, content_ref, status,
			 validation_status, last_sequence, first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?)
		ON CONFLICT(artifact_id) DO UPDATE SET
			claim_id = CASE WHEN excluded.claim_id != '' THEN excluded.claim_id ELSE forest_artifacts.claim_id END,
			testament_id = CASE WHEN excluded.testament_id != '' THEN excluded.testament_id ELSE forest_artifacts.testament_id END,
			validation_id = CASE WHEN excluded.validation_id != '' THEN excluded.validation_id ELSE forest_artifacts.validation_id END,
			generator_participant = CASE WHEN excluded.generator_participant != '' THEN excluded.generator_participant ELSE forest_artifacts.generator_participant END,
			artifact_name = CASE WHEN excluded.artifact_name != '' THEN excluded.artifact_name ELSE forest_artifacts.artifact_name END,
			artifact_kind = CASE WHEN excluded.artifact_kind != '' THEN excluded.artifact_kind ELSE forest_artifacts.artifact_kind END,
			data_type = CASE WHEN excluded.data_type != '' THEN excluded.data_type ELSE forest_artifacts.data_type END,
			content_hash = CASE WHEN excluded.content_hash != '' THEN excluded.content_hash ELSE forest_artifacts.content_hash END,
			content_ref = CASE WHEN excluded.content_ref != '' THEN excluded.content_ref ELSE forest_artifacts.content_ref END,
			status = CASE WHEN excluded.status != '' THEN excluded.status ELSE forest_artifacts.status END,
			last_sequence = excluded.last_sequence,
			last_seen_at = excluded.last_seen_at,
			payload_hash = excluded.payload_hash,
			metadata = excluded.metadata
	`, artifactID, claimID, testamentID, validationID, actorRoute(delta.Actor),
		mapString(artifact, "name"), mapString(artifact, "kind"), mapString(artifact, "data_type"),
		contentHash, contentRef, statusText, boardSeq, now, now, payloadHash, marshalJSON(artifact)); err != nil {
		return fmt.Errorf("upsert forest artifact evidence: %w", err)
	}
	if actor := actorRoute(delta.Actor); actor != "" {
		if err := insertArtifactEdgeTx(ctx, tx, artifactID, artifactEdgeGeneratedBy, "participant", actor, ledgerID, boardSeq); err != nil {
			return err
		}
	}
	if claimID != "" {
		if err := insertArtifactEdgeTx(ctx, tx, artifactID, artifactEdgeAttachedToClaim, claims.RelatedTypeClaim, claimID, ledgerID, boardSeq); err != nil {
			return err
		}
	}
	if testamentID != "" {
		if err := insertArtifactEdgeTx(ctx, tx, artifactID, artifactEdgeAttachedToTestament, claims.RelatedTypeTestament, testamentID, ledgerID, boardSeq); err != nil {
			return err
		}
	}
	if validationID != "" {
		if err := insertArtifactEdgeTx(ctx, tx, artifactID, artifactEdgeValidationResult, claims.RelatedTypeValidation, validationID, ledgerID, boardSeq); err != nil {
			return err
		}
	}
	if err := insertArtifactEdgesForRefsTx(ctx, tx, artifactID, ledgerID, boardSeq, delta.Refs); err != nil {
		return err
	}
	return nil
}

func projectValidationDeltaTx(ctx context.Context, tx *sql.Tx, delta claims.CanonicalDelta, ledgerID string, seq int64, payloadHash string) error {
	boardSeq := int64(delta.Sequence)
	validationID := delta.ValidationID()
	if validationID == "" {
		return rejectEvidenceTx(ctx, tx, ledgerID, claims.RelatedTypeValidation, "", "missing_validation_ref", "validation delta has no validation ref", boardSeq)
	}
	current, err := currentEvidenceSequenceTx(ctx, tx, "forest_validations", "validation_id", validationID)
	if err != nil {
		return err
	}
	if current > boardSeq {
		return rejectEvidenceTx(ctx, tx, ledgerID, claims.RelatedTypeValidation, validationID, "sequence_regression", "validation lifecycle sequence regressed", boardSeq)
	}
	validation := mapFromContext(delta.Context, "validation")
	claim := mapFromContext(delta.Context, "claim")
	transition := mapFromContext(delta.Context, "transition")
	status := validationStatusText(delta, validation)
	claimID := firstNonEmptyString(delta.ClaimID(), mapString(validation, "claim_id"), mapString(claim, "id"))
	targetArtifactID := firstNonEmptyString(delta.RefID("artifact", claims.RelatedTypeArtifact), mapString(validation, "target_artifact_id"))
	resultArtifactID := firstNonEmptyString(refIDByRole(delta.Refs, "result_artifact", claims.RelatedTypeArtifact), mapString(validation, "result_artifact_id"))
	required := mapBool(validation, "required")
	validationType := firstNonEmptyString(mapString(validation, "type"), mapString(validation, "validation_type"))
	failureReason := firstNonEmptyString(mapString(transition, "reason"), mapNestedString(delta.Context, "error", "description"), mapString(validation, "reason"))
	if validationTerminal(status) && targetArtifactID == "" && !receiptValidation(validationType) && delta.Action != claims.DeltaActionValidationEvaluated {
		return rejectEvidenceTx(ctx, tx, ledgerID, claims.RelatedTypeValidation, validationID, "missing_target_artifact", "terminal validation lifecycle delta has no target artifact", boardSeq)
	}
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_validations
			(validation_id, claim_id, target_artifact_id, evaluator_participant, validation_type,
			 status, result_artifact_id, failure_reason, required, last_sequence,
			 first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(validation_id) DO UPDATE SET
			claim_id = CASE WHEN excluded.claim_id != '' THEN excluded.claim_id ELSE forest_validations.claim_id END,
			target_artifact_id = CASE WHEN excluded.target_artifact_id != '' THEN excluded.target_artifact_id ELSE forest_validations.target_artifact_id END,
			evaluator_participant = CASE WHEN excluded.evaluator_participant != '' THEN excluded.evaluator_participant ELSE forest_validations.evaluator_participant END,
			validation_type = CASE WHEN excluded.validation_type != '' THEN excluded.validation_type ELSE forest_validations.validation_type END,
			status = CASE WHEN excluded.status != '' THEN excluded.status ELSE forest_validations.status END,
			result_artifact_id = CASE WHEN excluded.result_artifact_id != '' THEN excluded.result_artifact_id ELSE forest_validations.result_artifact_id END,
			failure_reason = CASE WHEN excluded.failure_reason != '' THEN excluded.failure_reason ELSE forest_validations.failure_reason END,
			required = excluded.required,
			last_sequence = excluded.last_sequence,
			last_seen_at = excluded.last_seen_at,
			payload_hash = excluded.payload_hash,
			metadata = excluded.metadata
	`, validationID, claimID, targetArtifactID, actorRoute(delta.Actor), validationType, status,
		resultArtifactID, failureReason, boolInt(required), boardSeq, now, now, payloadHash, marshalJSON(validation)); err != nil {
		return fmt.Errorf("upsert forest validation evidence: %w", err)
	}
	if targetArtifactID != "" {
		edgeKind := artifactEdgeValidates
		if validationFailureStatus(status) {
			edgeKind = artifactEdgeInvalidates
		}
		if err := insertArtifactEdgeTx(ctx, tx, targetArtifactID, edgeKind, claims.RelatedTypeValidation, validationID, ledgerID, boardSeq); err != nil {
			return err
		}
	}
	if resultArtifactID != "" {
		if err := ensureResultArtifactStubTx(ctx, tx, resultArtifactID, claimID, validationID, actorRoute(delta.Actor), payloadHash, boardSeq); err != nil {
			return err
		}
		if err := insertArtifactEdgeTx(ctx, tx, resultArtifactID, artifactEdgeValidationResult, claims.RelatedTypeValidation, validationID, ledgerID, boardSeq); err != nil {
			return err
		}
		if targetArtifactID != "" {
			edgeKind := artifactEdgeValidates
			if validationFailureStatus(status) {
				edgeKind = artifactEdgeInvalidates
			}
			if err := insertArtifactEdgeTx(ctx, tx, resultArtifactID, edgeKind, claims.RelatedTypeArtifact, targetArtifactID, ledgerID, boardSeq); err != nil {
				return err
			}
		}
	}
	if validationFailureStatus(status) && claimID != "" && targetArtifactID != "" {
		if err := insertArtifactEdgeTx(ctx, tx, targetArtifactID, artifactEdgeInvalidates, claims.RelatedTypeClaim, claimID, ledgerID, boardSeq); err != nil {
			return err
		}
	}
	if err := insertValidationRemediationEdgesTx(ctx, tx, targetArtifactID, resultArtifactID, ledgerID, boardSeq, delta.Refs); err != nil {
		return err
	}
	if err := updateValidationPatternTx(ctx, tx, delta, validationType, targetArtifactID, status, validationID); err != nil {
		return err
	}
	return nil
}

func currentEvidenceSequenceTx(ctx context.Context, tx *sql.Tx, table, idColumn, id string) (int64, error) {
	query := fmt.Sprintf("SELECT last_sequence FROM %s WHERE %s = ?", table, idColumn)
	var current int64
	err := tx.QueryRowContext(ctx, query, id).Scan(&current)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load evidence sequence: %w", err)
	}
	return current, nil
}

func insertArtifactEdgeTx(ctx context.Context, tx *sql.Tx, artifactID, edgeKind, targetType, targetID, ledgerID string, seq int64) error {
	if artifactID == "" || edgeKind == "" || targetType == "" || targetID == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO forest_artifact_edges
			(artifact_id, edge_kind, target_type, target_id, source_ledger_id, sequence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, artifactID, edgeKind, targetType, targetID, ledgerID, seq, time.Now().UTC().Unix()); err != nil {
		return fmt.Errorf("insert artifact evidence edge: %w", err)
	}
	return nil
}

func insertArtifactEdgesForRefsTx(ctx context.Context, tx *sql.Tx, artifactID, ledgerID string, seq int64, refs []claims.DeltaRef) error {
	for _, ref := range refs {
		switch ref.Role {
		case "supersedes":
			if err := insertArtifactEdgeTx(ctx, tx, artifactID, artifactEdgeSupersedes, ref.Type, ref.ID, ledgerID, seq); err != nil {
				return err
			}
		case "remediates", "remediation":
			if err := insertArtifactEdgeTx(ctx, tx, artifactID, artifactEdgeRemediates, ref.Type, ref.ID, ledgerID, seq); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertValidationRemediationEdgesTx(ctx context.Context, tx *sql.Tx, targetArtifactID, resultArtifactID, ledgerID string, seq int64, refs []claims.DeltaRef) error {
	sourceArtifactID := firstNonEmptyString(resultArtifactID, targetArtifactID)
	if sourceArtifactID == "" {
		return nil
	}
	for _, ref := range refs {
		if ref.Role != "remediation_claim" && ref.Role != "remediates" {
			continue
		}
		if err := insertArtifactEdgeTx(ctx, tx, sourceArtifactID, artifactEdgeRemediates, ref.Type, ref.ID, ledgerID, seq); err != nil {
			return err
		}
	}
	return nil
}

func ensureResultArtifactStubTx(ctx context.Context, tx *sql.Tx, artifactID, claimID, validationID, generator, payloadHash string, seq int64) error {
	if artifactID == "" {
		return nil
	}
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_artifacts
			(artifact_id, claim_id, validation_id, generator_participant, artifact_kind,
			 data_type, content_ref, status, last_sequence, first_seen_at, last_seen_at,
			 payload_hash, metadata)
		VALUES (?, ?, ?, ?, 'validation_result', 'unknown', 'unavailable:validation_result_pending',
			'generated', ?, ?, ?, ?, '{}')
		ON CONFLICT(artifact_id) DO UPDATE SET
			claim_id = CASE WHEN excluded.claim_id != '' THEN excluded.claim_id ELSE forest_artifacts.claim_id END,
			validation_id = CASE WHEN excluded.validation_id != '' THEN excluded.validation_id ELSE forest_artifacts.validation_id END,
			generator_participant = CASE WHEN excluded.generator_participant != '' THEN excluded.generator_participant ELSE forest_artifacts.generator_participant END,
			artifact_kind = CASE WHEN forest_artifacts.artifact_kind = '' THEN excluded.artifact_kind ELSE forest_artifacts.artifact_kind END,
			last_sequence = CASE WHEN forest_artifacts.last_sequence < excluded.last_sequence THEN excluded.last_sequence ELSE forest_artifacts.last_sequence END,
			last_seen_at = excluded.last_seen_at,
			payload_hash = CASE WHEN excluded.payload_hash != '' THEN excluded.payload_hash ELSE forest_artifacts.payload_hash END
	`, artifactID, claimID, validationID, generator, seq, now, now, payloadHash); err != nil {
		return fmt.Errorf("upsert validation result artifact stub: %w", err)
	}
	return nil
}

func rejectEvidenceTx(ctx context.Context, tx *sql.Tx, ledgerID, entityType, entityID, kind, message string, seq int64) error {
	if err := recordEvidenceErrorTx(ctx, tx, ledgerID, entityType, entityID, kind, message, seq); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s: %s", errEvidenceProjectionRejected, kind, message)
}

func recordEvidenceErrorTx(ctx context.Context, tx *sql.Tx, ledgerID, entityType, entityID, kind, message string, seq int64) error {
	id := "evidence_error_" + stableID(ledgerID, entityType, entityID, kind, message)
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO forest_evidence_errors
			(id, ledger_id, entity_type, entity_id, error_kind, message, sequence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, ledgerID, entityType, entityID, kind, message, seq, time.Now().UTC().Unix()); err != nil {
		return fmt.Errorf("record evidence projection error: %w", err)
	}
	return nil
}

func recordDeltaProjectionAuditTx(ctx context.Context, tx *sql.Tx, ledgerID string, action claims.DeltaAction, classification, reason, sideEffect string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO forest_delta_projection_audit
			(ledger_id, action, classification, reason, side_effect, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, ledgerID, string(action), classification, reason, sideEffect, time.Now().UTC().Unix()); err != nil {
		return fmt.Errorf("record delta projection audit: %w", err)
	}
	return nil
}

func updateValidationPatternTx(ctx context.Context, tx *sql.Tx, delta claims.CanonicalDelta, validationType, artifactID, status, validationID string) error {
	if validationType == "" || !validationTerminal(status) {
		return nil
	}
	claimAction := mapNestedString(delta.Context, "claim", "action")
	artifactKind := ""
	if artifactID != "" {
		_ = tx.QueryRowContext(ctx, `SELECT artifact_kind FROM forest_artifacts WHERE artifact_id = ?`, artifactID).Scan(&artifactKind)
	}
	key := stableID("validation_pattern", claimAction, artifactKind, validationType)
	success, failure := 0, 0
	if validationFailureStatus(status) {
		failure = 1
	} else {
		success = 1
	}
	res, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO forest_validation_pattern_observations
			(validation_id, pattern_key, status, recorded_at)
		VALUES (?, ?, ?, ?)
	`, validationID, key, status, time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("insert validation pattern observation: %w", err)
	}
	inserted, _ := res.RowsAffected()
	if inserted == 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO forest_validation_patterns
			(pattern_key, claim_action, artifact_kind, validation_type, success_count, failure_count, last_validation_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pattern_key) DO UPDATE SET
			success_count = success_count + excluded.success_count,
			failure_count = failure_count + excluded.failure_count,
			last_validation_id = excluded.last_validation_id,
			updated_at = excluded.updated_at
	`, key, claimAction, artifactKind, validationType, success, failure, validationID, time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("update validation pattern: %w", err)
	}
	return nil
}

func validationStatusText(delta claims.CanonicalDelta, validation map[string]any) string {
	if status, ok := claims.DeltaActionValidationLifecycleStatus(delta.Action); ok {
		return string(status)
	}
	if delta.Action == claims.DeltaActionValidationEvaluated {
		return firstNonEmptyString(mapString(validation, "status"), mapString(validation, "verdict"))
	}
	return mapString(validation, "status")
}

func validationTerminal(status string) bool {
	switch claims.ValidationStatus(strings.TrimSpace(status)) {
	case claims.ValidationStatusValidated,
		claims.ValidationStatusValidationFailed,
		claims.ValidationStatusValidationFailedNotRequired,
		claims.ValidationStatusErrored,
		claims.ValidationStatusErroredNotRequired,
		claims.ValidationStatusQualityBarValidationFailed,
		claims.ValidationStatusQualityBarValidationFailedNotRequired,
		claims.ValidationStatusPassed,
		claims.ValidationStatusFailed,
		claims.ValidationStatusSkipped:
		return true
	default:
		return false
	}
}

func validationFailureStatus(status string) bool {
	s := claims.ValidationStatus(strings.TrimSpace(status))
	return s.IsBlockingFailure() || s.IsOptionalFailure() || s == claims.ValidationStatusFailed
}

func receiptValidation(validationType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(validationType))
	return normalized == "receipt" || strings.Contains(normalized, "receipt")
}

func deriveInlineArtifactContentHash(artifact map[string]any) string {
	if artifact == nil {
		return ""
	}
	inline, ok := inlineArtifactContent(artifact)
	if !ok {
		return ""
	}
	sum := sha256.Sum256([]byte(inline))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func inlineArtifactContent(artifact map[string]any) (string, bool) {
	for _, key := range []string{"content", "body", "text", "inline_content"} {
		value, ok := artifact[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed, true
			}
		default:
			encoded := marshalJSON(typed)
			if encoded != "" {
				return encoded, true
			}
		}
	}
	return "", false
}

func refIDByRole(refs []claims.DeltaRef, role, typ string) string {
	for _, ref := range refs {
		if ref.Role == role && ref.Type == typ {
			return ref.ID
		}
	}
	return ""
}

func mapFromContext(context map[string]any, key string) map[string]any {
	if context == nil {
		return nil
	}
	if typed, ok := context[key].(map[string]any); ok {
		return typed
	}
	return nil
}

func mapString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch raw := values[key].(type) {
	case string:
		return strings.TrimSpace(raw)
	case fmt.Stringer:
		return strings.TrimSpace(raw.String())
	default:
		return ""
	}
}

func mapNestedString(values map[string]any, outer, inner string) string {
	return mapString(mapFromContext(values, outer), inner)
}

func mapBool(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	switch raw := values[key].(type) {
	case bool:
		return raw
	case string:
		return strings.EqualFold(strings.TrimSpace(raw), "true")
	default:
		return false
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func actorRoute(ref claims.AgentRef) string {
	ref = ref.Normalized()
	return ref.RouteKey()
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
