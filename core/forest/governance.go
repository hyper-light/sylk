package forest

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const (
	ForestProposalStatusProposed = "proposed"
	ForestProposalStatusAccepted = "accepted"
	ForestProposalStatusRejected = "rejected"

	ForestProposalKindPolicyPromotion   = PolicyPromotionArtifactKind
	ForestProposalKindQuarantine        = "forest_quarantine"
	ForestProposalKindRemediation       = "forest_remediation"
	ForestProposalKindPruning           = "forest_pruning"
	ForestProposalKindSkillCandidate    = SkillCandidateArtifactType
	ForestProposalKindClusterSpeciation = "forest_cluster_speciation"

	ForestProposalRollbackManualReview = "manual_guardian_review_required"
)

type ForestGovernedChange struct {
	Kind                   string
	SubjectType            string
	SubjectID              string
	Summary                string
	EvidenceRefs           []string
	CounterEvidenceRefs    []string
	RollbackPath           string
	GuardianReviewRequired bool
	PermissionDiff         ForestAuthorityDiff
	ApprovalClaimID        string
	Metadata               map[string]any
}

type ForestGovernanceProposal struct {
	ProposalID             string
	Kind                   string
	SubjectType            string
	SubjectID              string
	Status                 string
	ClaimID                string
	ArtifactID             string
	EvidenceRefs           []string
	RollbackPath           string
	GuardianReviewRequired bool
	PermissionDiff         ForestAuthorityDiff
	ApprovalClaimID        string
	ClaimEmittedAt         time.Time
	ClaimEmitError         string
	Metadata               map[string]any
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type ForestAuthorityDiff struct {
	ToolsAdded            []string `json:"tools_added,omitempty"`
	FilesystemScopesAdded []string `json:"filesystem_scopes_added,omitempty"`
	SandboxWidened        []string `json:"sandbox_widened,omitempty"`
	ApprovalBypass        []string `json:"approval_bypass,omitempty"`
	Ambiguous             []string `json:"ambiguous,omitempty"`
	Raw                   []string `json:"raw,omitempty"`
}

type ForestAuthorityApproval struct {
	ClaimID        string
	Actor          string
	Scope          []string
	ExpiresAt      time.Time
	PermissionDiff ForestAuthorityDiff
	Metadata       map[string]any
}

type GuardianApprovalRequest struct {
	Proposal       ForestGovernanceProposal
	PermissionDiff ForestAuthorityDiff
	EvidenceRefs   []string
}

//go:generate mockery --name=GuardianApprover --inpackage --output=. --filename=mock_guardian_approver_test.go --outpkg=forest
type GuardianApprover interface {
	ApproveForestProposal(ctx context.Context, request GuardianApprovalRequest) (ForestAuthorityApproval, error)
}

func (m *MemoryForest) ProposeGovernedForestChange(ctx context.Context, input ForestGovernedChange) (ForestGovernanceProposal, error) {
	if m == nil || m.db == nil {
		return ForestGovernanceProposal{}, fmt.Errorf("memory forest is required")
	}
	proposal, err := normalizeGovernedChange(input)
	if err != nil {
		return ForestGovernanceProposal{}, err
	}
	m.governanceMu.Lock()
	defer m.governanceMu.Unlock()
	inserted, err := m.insertGovernanceProposal(ctx, proposal)
	if err != nil {
		return ForestGovernanceProposal{}, err
	}
	if !inserted {
		proposal, err = m.loadGovernanceProposal(ctx, proposal.ProposalID)
		if err != nil {
			return ForestGovernanceProposal{}, err
		}
	}
	if err := emitGovernanceProposalClaim(ctx, m.db, m.proposalSink, proposal); err != nil {
		return ForestGovernanceProposal{}, err
	}
	return m.loadGovernanceProposal(ctx, proposal.ProposalID)
}

func normalizeGovernedChange(input ForestGovernedChange) (ForestGovernanceProposal, error) {
	input.Kind = strings.TrimSpace(input.Kind)
	input.SubjectType = strings.TrimSpace(input.SubjectType)
	input.SubjectID = strings.TrimSpace(input.SubjectID)
	input.Summary = strings.TrimSpace(input.Summary)
	input.EvidenceRefs = dedupeStrings(input.EvidenceRefs)
	input.RollbackPath = firstNonEmptyString(strings.TrimSpace(input.RollbackPath), ForestProposalRollbackManualReview)
	if err := validateGovernedChange(input); err != nil {
		return ForestGovernanceProposal{}, err
	}
	proposalID := governedProposalID(input.Kind, input.SubjectType, input.SubjectID, input.EvidenceRefs)
	now := time.Now().UTC()
	metadata := cloneStringAnyMap(input.Metadata)
	metadata["summary"] = input.Summary
	return ForestGovernanceProposal{
		ProposalID:             proposalID,
		Kind:                   input.Kind,
		SubjectType:            input.SubjectType,
		SubjectID:              input.SubjectID,
		Status:                 ForestProposalStatusProposed,
		ClaimID:                proposalID,
		ArtifactID:             stableID("forest_governance_artifact", proposalID),
		EvidenceRefs:           input.EvidenceRefs,
		RollbackPath:           input.RollbackPath,
		GuardianReviewRequired: input.GuardianReviewRequired,
		PermissionDiff:         input.PermissionDiff.Normalize(),
		ApprovalClaimID:        strings.TrimSpace(input.ApprovalClaimID),
		Metadata:               metadata,
		CreatedAt:              now,
		UpdatedAt:              now,
	}, nil
}

func validateGovernedChange(input ForestGovernedChange) error {
	switch {
	case input.Kind == "":
		return fmt.Errorf("governed proposal kind is required")
	case input.SubjectType == "":
		return fmt.Errorf("governed proposal subject type is required")
	case input.SubjectID == "":
		return fmt.Errorf("governed proposal subject id is required")
	case input.Summary == "":
		return fmt.Errorf("governed proposal summary is required")
	case len(input.EvidenceRefs) == 0:
		return fmt.Errorf("governed proposal requires validation evidence refs")
	case input.RollbackPath == "":
		return fmt.Errorf("governed proposal rollback path is required")
	default:
		return nil
	}
}

func (m *MemoryForest) insertGovernanceProposal(ctx context.Context, proposal ForestGovernanceProposal) (bool, error) {
	if err := ensureForestGovernanceSchema(m.db); err != nil {
		return false, err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin governance proposal tx: %w", err)
	}
	defer tx.Rollback()
	inserted, err := insertGovernanceProposalTx(ctx, tx, proposal)
	if err != nil {
		return false, err
	}
	if err := upsertGovernanceProposalArtifactTx(ctx, tx, proposal); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit governance proposal tx: %w", err)
	}
	return inserted, nil
}

func insertGovernanceProposalTx(ctx context.Context, tx *sql.Tx, proposal ForestGovernanceProposal) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO forest_governance_proposals
			(proposal_id, proposal_kind, subject_type, subject_id, status,
			 claim_id, artifact_id, evidence_refs, rollback_path,
			 guardian_review_required, permission_diff, approval_claim_id,
			 claim_emitted_at, claim_emit_error,
			 created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, proposal.ProposalID, proposal.Kind, proposal.SubjectType, proposal.SubjectID, proposal.Status,
		proposal.ClaimID, proposal.ArtifactID, encodeStringList(proposal.EvidenceRefs), proposal.RollbackPath,
		boolInt(proposal.GuardianReviewRequired), proposal.PermissionDiff.Canonical(),
		proposal.ApprovalClaimID, unixTimestampOrZero(proposal.ClaimEmittedAt), strings.TrimSpace(proposal.ClaimEmitError),
		proposal.CreatedAt.Unix(), proposal.UpdatedAt.Unix(), marshalJSON(proposal.Metadata))
	if err != nil {
		return false, fmt.Errorf("insert governance proposal: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count inserted governance proposal: %w", err)
	}
	return affected > 0, nil
}

func upsertGovernanceProposalArtifactTx(ctx context.Context, tx *sql.Tx, proposal ForestGovernanceProposal) error {
	now := time.Now().UTC().Unix()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_artifacts
			(artifact_id, claim_id, generator_participant, artifact_name, artifact_kind,
			 data_type, content_ref, status, validation_status, last_sequence,
			 first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
		ON CONFLICT(artifact_id) DO UPDATE SET
			status = excluded.status,
			validation_status = excluded.validation_status,
			last_seen_at = excluded.last_seen_at,
			metadata = excluded.metadata
	`, proposal.ArtifactID, proposal.ClaimID, "memory_forest", proposal.Summary(), proposal.Kind,
		"forest_governance_proposal", "forest_governance:"+proposal.ProposalID,
		proposal.Status, "requires_validation", now, now,
		stableID("forest_governance_payload", proposal.ProposalID, marshalJSON(proposal.Metadata)),
		marshalJSON(proposal))
	if err != nil {
		return fmt.Errorf("upsert governance proposal artifact: %w", err)
	}
	return nil
}

func (p ForestGovernanceProposal) toClaimProposal() ForestClaimProposal {
	return ForestClaimProposal{
		ID:                     p.ClaimID,
		ClusterID:              p.SubjectType,
		Dimension:              p.Kind,
		Summary:                p.Summary(),
		EvidenceRefs:           p.EvidenceRefs,
		GuardianReviewRequired: p.GuardianReviewRequired,
	}
}

func emitGovernanceProposalClaim(ctx context.Context, db *sql.DB, sink ForestProposalSink, proposal ForestGovernanceProposal) error {
	return emitGovernanceProposalClaimWithProposal(ctx, db, sink, proposal, proposal.toClaimProposal())
}

func emitGovernanceProposalClaimWithProposal(ctx context.Context, db *sql.DB, sink ForestProposalSink, proposal ForestGovernanceProposal, claimProposal ForestClaimProposal) error {
	if sink == nil {
		return nil
	}
	emitted, err := governanceProposalClaimAlreadyEmitted(ctx, db, proposal.ProposalID)
	if err != nil {
		return err
	}
	if emitted {
		return nil
	}
	if err := sink.ProposeClaim(ctx, claimProposal); err != nil {
		_ = markGovernanceProposalClaimEmissionError(ctx, db, proposal.ProposalID, err)
		return err
	}
	return markGovernanceProposalClaimEmitted(ctx, db, proposal.ProposalID)
}

func governanceProposalClaimAlreadyEmitted(ctx context.Context, db *sql.DB, proposalID string) (bool, error) {
	var emittedAt int64
	err := db.QueryRowContext(ctx, `
		SELECT claim_emitted_at
		FROM forest_governance_proposals
		WHERE proposal_id = ?
	`, strings.TrimSpace(proposalID)).Scan(&emittedAt)
	if err != nil {
		return false, fmt.Errorf("load governance proposal claim emission state: %w", err)
	}
	return emittedAt > 0, nil
}

func markGovernanceProposalClaimEmitted(ctx context.Context, db *sql.DB, proposalID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE forest_governance_proposals
		SET claim_emitted_at = ?, claim_emit_error = '', updated_at = ?
		WHERE proposal_id = ? AND claim_emitted_at = 0
	`, time.Now().UTC().Unix(), time.Now().UTC().Unix(), strings.TrimSpace(proposalID))
	if err != nil {
		return fmt.Errorf("mark governance proposal claim emitted: %w", err)
	}
	return nil
}

func markGovernanceProposalClaimEmissionError(ctx context.Context, db *sql.DB, proposalID string, cause error) error {
	if cause == nil {
		return nil
	}
	_, err := db.ExecContext(ctx, `
		UPDATE forest_governance_proposals
		SET claim_emit_error = ?, updated_at = ?
		WHERE proposal_id = ? AND claim_emitted_at = 0
	`, truncateRuntimeError(cause.Error()), time.Now().UTC().Unix(), strings.TrimSpace(proposalID))
	if err != nil {
		return fmt.Errorf("mark governance proposal claim emission error: %w", err)
	}
	return nil
}

func (p ForestGovernanceProposal) Summary() string {
	if raw, ok := p.Metadata["summary"].(string); ok && strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw)
	}
	return p.Kind + " proposed for " + p.SubjectType + " " + p.SubjectID
}

func (m *MemoryForest) loadGovernanceProposal(ctx context.Context, proposalID string) (ForestGovernanceProposal, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT proposal_id, proposal_kind, subject_type, subject_id, status,
		       claim_id, artifact_id, evidence_refs, rollback_path,
		       guardian_review_required, permission_diff, approval_claim_id,
		       claim_emitted_at, claim_emit_error,
		       created_at, updated_at, metadata
		FROM forest_governance_proposals
		WHERE proposal_id = ?
	`, strings.TrimSpace(proposalID))
	return scanGovernanceProposal(row)
}

func scanGovernanceProposal(row interface{ Scan(dest ...any) error }) (ForestGovernanceProposal, error) {
	var evidence, permissionDiff, metadata string
	var guardian int
	var claimEmittedAt, createdAt, updatedAt int64
	var proposal ForestGovernanceProposal
	if err := row.Scan(&proposal.ProposalID, &proposal.Kind, &proposal.SubjectType, &proposal.SubjectID, &proposal.Status,
		&proposal.ClaimID, &proposal.ArtifactID, &evidence, &proposal.RollbackPath, &guardian,
		&permissionDiff, &proposal.ApprovalClaimID, &claimEmittedAt, &proposal.ClaimEmitError,
		&createdAt, &updatedAt, &metadata); err != nil {
		return ForestGovernanceProposal{}, fmt.Errorf("scan governance proposal: %w", err)
	}
	_ = unmarshalJSON(permissionDiff, &proposal.PermissionDiff)
	_ = unmarshalJSON(metadata, &proposal.Metadata)
	proposal.EvidenceRefs = decodeStringList(evidence)
	proposal.GuardianReviewRequired = guardian != 0
	if claimEmittedAt > 0 {
		proposal.ClaimEmittedAt = time.Unix(claimEmittedAt, 0).UTC()
	}
	proposal.CreatedAt = time.Unix(createdAt, 0).UTC()
	proposal.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if proposal.Metadata == nil {
		proposal.Metadata = map[string]any{}
	}
	return proposal, nil
}

func (m *MemoryForest) RejectGovernanceProposal(ctx context.Context, proposalID, reason string) error {
	proposal, err := m.loadGovernanceProposal(ctx, proposalID)
	if err != nil {
		return err
	}
	if err := m.markGovernanceProposalStatus(ctx, proposal, ForestProposalStatusRejected, strings.TrimSpace(reason)); err != nil {
		return err
	}
	return m.recordGovernanceNegativeEvidence(ctx, proposal.SubjectType, proposal.SubjectID, reason, proposal.EvidenceRefs, proposal.ProposalID)
}

func (m *MemoryForest) AcceptGovernanceProposal(ctx context.Context, proposalID, acceptedClaimID string) error {
	proposal, err := m.loadGovernanceProposal(ctx, proposalID)
	if err != nil {
		return err
	}
	if err := m.requireAcceptedClaim(ctx, acceptedClaimID); err != nil {
		return err
	}
	return m.markGovernanceProposalStatus(ctx, proposal, ForestProposalStatusAccepted, acceptedClaimID)
}

func (m *MemoryForest) markGovernanceProposalStatus(ctx context.Context, proposal ForestGovernanceProposal, status, reason string) error {
	now := time.Now().UTC().Unix()
	metadata := cloneStringAnyMap(proposal.Metadata)
	metadata["status_reason"] = strings.TrimSpace(reason)
	_, err := m.db.ExecContext(ctx, `
		UPDATE forest_governance_proposals
		SET status = ?, updated_at = ?, metadata = ?
		WHERE proposal_id = ?
	`, status, now, marshalJSON(metadata), proposal.ProposalID)
	if err != nil {
		return fmt.Errorf("mark governance proposal %s: %w", status, err)
	}
	return updateGovernanceArtifactStatus(ctx, m.db, proposal.ArtifactID, status)
}

func updateGovernanceArtifactStatus(ctx context.Context, db *sql.DB, artifactID, status string) error {
	validationStatus := "requires_validation"
	if status == ForestProposalStatusAccepted {
		validationStatus = "validated"
	}
	if status == ForestProposalStatusRejected {
		validationStatus = "failed"
	}
	_, err := db.ExecContext(ctx, `
		UPDATE forest_artifacts
		SET status = ?, validation_status = ?, last_seen_at = ?
		WHERE artifact_id = ?
	`, status, validationStatus, time.Now().UTC().Unix(), artifactID)
	if err != nil {
		return fmt.Errorf("update governance artifact status: %w", err)
	}
	return nil
}

func updateGovernanceProposalStatusTx(ctx context.Context, tx *sql.Tx, proposalID, status, reason string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE forest_governance_proposals
		SET status = ?, updated_at = ?
		WHERE proposal_id = ?
	`, status, time.Now().UTC().Unix(), strings.TrimSpace(proposalID))
	if err != nil {
		return fmt.Errorf("update governance proposal status %s: %w", strings.TrimSpace(reason), err)
	}
	return nil
}

func (m *MemoryForest) recordGovernanceNegativeEvidence(ctx context.Context, subjectType, subjectID, reason string, evidenceRefs []string, proposalID string) error {
	if err := ensureForestGovernanceSchema(m.db); err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	quarantineID := stableID("forest_governance_quarantine", subjectType, subjectID, reason)
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO forest_governance_quarantine
			(quarantine_id, subject_type, subject_id, reason, evidence_refs, proposal_id, created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(subject_type, subject_id, reason) DO UPDATE SET
			evidence_refs = excluded.evidence_refs,
			proposal_id = excluded.proposal_id,
			created_at = excluded.created_at,
			metadata = excluded.metadata
	`, quarantineID, subjectType, subjectID, strings.TrimSpace(reason), encodeStringList(evidenceRefs),
		strings.TrimSpace(proposalID), now, marshalJSON(map[string]any{"negative_evidence": true}))
	if err != nil {
		return fmt.Errorf("record governance quarantine: %w", err)
	}
	return m.recordGovernanceNegativeMeme(ctx, subjectType, subjectID, reason, evidenceRefs)
}

func (m *MemoryForest) recordGovernanceNegativeMeme(ctx context.Context, subjectType, subjectID, reason string, evidenceRefs []string) error {
	if err := ensureForestMemeSchema(m.db); err != nil {
		return err
	}
	group := memeExtractionGroup{
		kind:       MemeKindNegativePattern,
		polarity:   MemePolarityNegative,
		signature:  memeSignature(subjectType + " " + subjectID + " " + reason),
		summary:    "Governance rejected " + subjectType + " " + subjectID + ": " + strings.TrimSpace(reason),
		nodeIDs:    []string{subjectID},
		grades:     []EvidenceGrade{EvidenceGradeFailed},
		confidence: 1,
	}
	if len(evidenceRefs) > 0 {
		group.edgeIDs = evidenceRefs
	}
	return upsertMemeExtraction(ctx, m.db, group, MemeStatusActive)
}

func governedProposalID(kind, subjectType, subjectID string, evidenceRefs []string) string {
	return strings.TrimSpace(kind) + ":" + stableID(strings.TrimSpace(subjectType), strings.TrimSpace(subjectID), encodeStringList(evidenceRefs))
}

func AuthorityDiffFromPermissions(values []string) ForestAuthorityDiff {
	diff := ForestAuthorityDiff{Raw: dedupeStrings(values)}
	for _, value := range diff.Raw {
		diff = diff.classify(strings.TrimSpace(value))
	}
	return diff.Normalize()
}

func (d ForestAuthorityDiff) classify(value string) ForestAuthorityDiff {
	normalized := normalizeText(value)
	switch {
	case normalized == "":
		return d
	case strings.Contains(normalized, "bypass") || strings.Contains(normalized, "approval"):
		d.ApprovalBypass = append(d.ApprovalBypass, value)
	case strings.Contains(normalized, "fs:") || strings.Contains(normalized, "filesystem") || strings.Contains(normalized, "file write"):
		d.FilesystemScopesAdded = append(d.FilesystemScopesAdded, value)
	case strings.Contains(normalized, "sandbox") || strings.Contains(normalized, "network"):
		d.SandboxWidened = append(d.SandboxWidened, value)
	case strings.Contains(normalized, "tool:") || strings.Contains(normalized, "tool "):
		d.ToolsAdded = append(d.ToolsAdded, value)
	default:
		d.Ambiguous = append(d.Ambiguous, value)
	}
	return d
}

func (d ForestAuthorityDiff) Normalize() ForestAuthorityDiff {
	return ForestAuthorityDiff{
		ToolsAdded:            sortedDedupe(d.ToolsAdded),
		FilesystemScopesAdded: sortedDedupe(d.FilesystemScopesAdded),
		SandboxWidened:        sortedDedupe(d.SandboxWidened),
		ApprovalBypass:        sortedDedupe(d.ApprovalBypass),
		Ambiguous:             sortedDedupe(d.Ambiguous),
		Raw:                   sortedDedupe(d.Raw),
	}
}

func (d ForestAuthorityDiff) ExpandsAuthority() bool {
	normalized := d.Normalize()
	return len(normalized.Raw) > 0 ||
		len(normalized.ToolsAdded)+len(normalized.FilesystemScopesAdded)+len(normalized.SandboxWidened)+len(normalized.ApprovalBypass) > 0
}

func (d ForestAuthorityDiff) UnsafeReason() string {
	normalized := d.Normalize()
	if len(normalized.Ambiguous) > 0 {
		return "ambiguous permission field fails closed"
	}
	if len(normalized.ApprovalBypass) > 0 {
		return "approval bypass is prohibited"
	}
	return ""
}

func (d ForestAuthorityDiff) Canonical() string {
	return marshalJSON(d.Normalize())
}

func (m *MemoryForest) RequireAuthorityBoundary(ctx context.Context, subject string, diff ForestAuthorityDiff, approval ForestAuthorityApproval) error {
	normalized := diff.Normalize()
	if !normalized.ExpandsAuthority() {
		return nil
	}
	if reason := normalized.UnsafeReason(); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	if err := approval.validateFor(subject, normalized); err != nil {
		return err
	}
	if err := m.requireAcceptedClaim(ctx, approval.ClaimID); err != nil {
		return err
	}
	return m.recordAuthorityApproval(ctx, approval.withDiff(normalized))
}

func (approval ForestAuthorityApproval) validateFor(subject string, diff ForestAuthorityDiff) error {
	switch {
	case strings.TrimSpace(approval.ClaimID) == "":
		return fmt.Errorf("permission expansion requires explicit approval claim")
	case strings.TrimSpace(approval.Actor) == "":
		return fmt.Errorf("permission approval actor is required")
	case len(approval.Scope) == 0:
		return fmt.Errorf("permission approval scope is required")
	case approval.ExpiresAt.IsZero() || !approval.ExpiresAt.After(time.Now().UTC()):
		return fmt.Errorf("permission approval expiry must be in the future")
	case !approvalScopeAllows(approval.Scope, subject):
		return fmt.Errorf("permission approval scope does not cover %s", subject)
	case approval.PermissionDiff.Normalize().Canonical() != diff.Canonical():
		return fmt.Errorf("permission approval diff does not match requested diff")
	default:
		return nil
	}
}

func (approval ForestAuthorityApproval) withDiff(diff ForestAuthorityDiff) ForestAuthorityApproval {
	approval.PermissionDiff = diff
	approval.Scope = sortedDedupe(approval.Scope)
	approval.Actor = strings.TrimSpace(approval.Actor)
	approval.ClaimID = strings.TrimSpace(approval.ClaimID)
	approval.Metadata = cloneStringAnyMap(approval.Metadata)
	return approval
}

func approvalScopeAllows(scope []string, subject string) bool {
	for _, item := range scope {
		if strings.TrimSpace(item) == subject {
			return true
		}
	}
	return false
}

func (m *MemoryForest) recordAuthorityApproval(ctx context.Context, approval ForestAuthorityApproval) error {
	if err := ensureForestGovernanceSchema(m.db); err != nil {
		return err
	}
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO forest_governance_approvals
			(approval_id, claim_id, actor, scope, expires_at, permission_diff, created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(claim_id) DO UPDATE SET
			actor = excluded.actor,
			scope = excluded.scope,
			expires_at = excluded.expires_at,
			permission_diff = excluded.permission_diff,
			metadata = excluded.metadata
	`, stableID("forest_authority_approval", approval.ClaimID, approval.Actor, encodeStringList(approval.Scope), approval.PermissionDiff.Canonical()),
		approval.ClaimID, approval.Actor, encodeStringList(approval.Scope), approval.ExpiresAt.UTC().Unix(),
		approval.PermissionDiff.Canonical(), time.Now().UTC().Unix(), marshalJSON(approval.Metadata))
	if err != nil {
		return fmt.Errorf("record authority approval: %w", err)
	}
	return nil
}

func (m *MemoryForest) requireAcceptedClaim(ctx context.Context, claimID string) error {
	accepted, err := acceptedForestClaimExists(ctx, m.db, claimID)
	if err != nil {
		return err
	}
	if !accepted {
		return fmt.Errorf("accepted forest claim %s not found", strings.TrimSpace(claimID))
	}
	return nil
}

func acceptedForestClaimExists(ctx context.Context, db *sql.DB, claimID string) (bool, error) {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return false, nil
	}
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_ledger
		WHERE subject_type = ?
		  AND subject_id = ?
		  AND event_kind = ?
	`, claims.RelatedTypeClaim, claimID, string(claims.DeltaActionClaimSatisfied)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check accepted forest claim: %w", err)
	}
	return count > 0, nil
}

func sortedDedupe(values []string) []string {
	out := dedupeStrings(values)
	sort.Strings(out)
	return out
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func unixTimestampOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().Unix()
}
