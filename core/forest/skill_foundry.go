package forest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	SkillCandidateStatusProposed                  = "proposed"
	SkillCandidateStatusRejected                  = "rejected"
	SkillCandidateStatusValidated                 = "validated"
	SkillCandidateStatusPromotionProposed         = "promotion_proposed"
	SkillCandidateStatusAcceptedPendingActivation = "accepted_pending_activation"
	SkillCandidateStatusDuplicate                 = "duplicate"

	SkillCandidateArtifactType = "generated_skill_candidate"

	SkillValidationStatusPassed = "passed"
	SkillValidationStatusFailed = "failed"
)

//go:generate mockery --name=SkillArtifactWriter --inpackage --output=. --filename=mock_skill_artifact_writer_test.go --outpkg=forest
type SkillArtifactWriter interface {
	WriteSkillCandidate(ctx context.Context, candidate GeneratedSkillCandidate, files []SkillCandidateFile) error
}

type SkillCandidateInput struct {
	Name                      string
	RoleScope                 string
	Trigger                   string
	SourceMemeIDs             []string
	SourceNodeIDs             []string
	SourceClaimIDs            []string
	SourceValidationRefs      []string
	RejectedVariantIDs        []string
	RequestedPermissions      []string
	ExistingSkillNames        []string
	ExplicitPermissionClaimID string
	PermissionApprovalActor   string
	PermissionApprovalScope   []string
	PermissionApprovalExpires time.Time
	PromotionRationale        string
}

type GeneratedSkillCandidate struct {
	CandidateID               string
	CandidateKey              string
	Name                      string
	RoleScope                 string
	Trigger                   string
	Status                    string
	ArtifactType              string
	SourceMemeIDs             []string
	SourceNodeIDs             []string
	SourceClaimIDs            []string
	SourceValidationRefs      []string
	RejectedVariantIDs        []string
	PermissionDiff            []string
	ExplicitPermissionClaimID string
	PromotionRationale        string
	GuardianReviewRequired    bool
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	Metadata                  map[string]any
	Files                     []SkillCandidateFile
}

type SkillCandidateFile struct {
	Path        string
	ContentHash string
	Content     string
	FileKind    string
}

type SkillCandidateValidation struct {
	Validator    string
	Status       string
	Summary      string
	EvidenceRefs []string
}

func (m *MemoryForest) ProposeGeneratedSkillCandidate(ctx context.Context, input SkillCandidateInput) (*GeneratedSkillCandidate, error) {
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("memory forest is required")
	}
	if err := ensureForestSkillFoundrySchema(m.db); err != nil {
		return nil, err
	}
	if err := ensureForestGovernanceSchema(m.db); err != nil {
		return nil, err
	}
	candidate, files := buildSkillCandidate(input)
	if err := m.applySkillCandidateGuards(ctx, &candidate, input); err != nil {
		if persistErr := m.persistSkillCandidate(ctx, candidate, files); persistErr != nil {
			return &candidate, persistErr
		}
		return &candidate, err
	}
	candidate.Files = files
	if err := m.persistSkillCandidate(ctx, candidate, files); err != nil {
		return nil, err
	}
	if m.skillArtifactWriter != nil {
		if err := m.skillArtifactWriter.WriteSkillCandidate(ctx, candidate, files); err != nil {
			return nil, fmt.Errorf("write skill candidate artifact: %w", err)
		}
	}
	if err := m.proposeSkillCandidateClaim(ctx, candidate); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func buildSkillCandidate(input SkillCandidateInput) (GeneratedSkillCandidate, []SkillCandidateFile) {
	now := time.Now().UTC()
	name := strings.TrimSpace(input.Name)
	role := strings.TrimSpace(input.RoleScope)
	trigger := strings.TrimSpace(input.Trigger)
	key := stableID("skill_candidate", normalizeText(name), normalizeText(role), normalizeText(trigger), encodeStringList(input.SourceMemeIDs), encodeStringList(input.SourceValidationRefs))
	candidate := GeneratedSkillCandidate{
		CandidateID:               "skill_candidate:" + key,
		CandidateKey:              key,
		Name:                      name,
		RoleScope:                 role,
		Trigger:                   trigger,
		Status:                    SkillCandidateStatusProposed,
		ArtifactType:              SkillCandidateArtifactType,
		SourceMemeIDs:             dedupeStrings(input.SourceMemeIDs),
		SourceNodeIDs:             dedupeStrings(input.SourceNodeIDs),
		SourceClaimIDs:            dedupeStrings(input.SourceClaimIDs),
		SourceValidationRefs:      dedupeStrings(input.SourceValidationRefs),
		RejectedVariantIDs:        dedupeStrings(input.RejectedVariantIDs),
		PermissionDiff:            AuthorityDiffFromPermissions(input.RequestedPermissions).Raw,
		ExplicitPermissionClaimID: strings.TrimSpace(input.ExplicitPermissionClaimID),
		PromotionRationale:        strings.TrimSpace(input.PromotionRationale),
		GuardianReviewRequired:    true,
		CreatedAt:                 now,
		UpdatedAt:                 now,
		Metadata:                  map[string]any{"proposal_only": true},
	}
	files := skillCandidateFiles(candidate)
	return candidate, files
}

func (m *MemoryForest) applySkillCandidateGuards(ctx context.Context, candidate *GeneratedSkillCandidate, input SkillCandidateInput) error {
	if err := validateSkillCandidateInput(*candidate); err != nil {
		markSkillCandidateRejected(candidate, err.Error())
		return err
	}
	if skillNameExists(candidate.Name, input.ExistingSkillNames) {
		markSkillCandidateStatus(candidate, SkillCandidateStatusDuplicate, "existing_skill_name")
		return fmt.Errorf("generated skill candidate duplicates existing skill %q", candidate.Name)
	}
	if err := m.enforceSkillAuthorityBoundary(ctx, candidate, input); err != nil {
		markSkillCandidateRejected(candidate, err.Error())
		_ = m.recordGovernanceNegativeEvidence(ctx, SkillCandidateArtifactType, candidate.CandidateID, err.Error(), candidate.SourceValidationRefs, "")
		return err
	}
	if containsSelfActivationRequest(candidate) {
		markSkillCandidateRejected(candidate, "skill candidate cannot activate or install itself")
		_ = m.recordGovernanceNegativeEvidence(ctx, SkillCandidateArtifactType, candidate.CandidateID, "skill candidate cannot activate or install itself", candidate.SourceValidationRefs, "")
		return fmt.Errorf("skill candidate cannot activate or install itself")
	}
	suppressed, memeID, err := m.ActiveNegativeMemeSuppresses(ctx, candidate.Name+" "+candidate.Trigger+" "+candidate.PromotionRationale)
	if err != nil {
		return err
	}
	if suppressed {
		markSkillCandidateRejected(candidate, "suppressed by negative meme "+memeID)
		return fmt.Errorf("skill candidate suppressed by negative meme %s", memeID)
	}
	return nil
}

func (m *MemoryForest) enforceSkillAuthorityBoundary(ctx context.Context, candidate *GeneratedSkillCandidate, input SkillCandidateInput) error {
	diff := AuthorityDiffFromPermissions(input.RequestedPermissions)
	subject := SkillCandidatePermissionScope(candidate.CandidateID)
	approval := ForestAuthorityApproval{
		ClaimID:        candidate.ExplicitPermissionClaimID,
		Actor:          input.PermissionApprovalActor,
		Scope:          input.PermissionApprovalScope,
		ExpiresAt:      input.PermissionApprovalExpires,
		PermissionDiff: diff,
		Metadata: map[string]any{
			"candidate_id": candidate.CandidateID,
			"role_scope":   candidate.RoleScope,
		},
	}
	return m.RequireAuthorityBoundary(ctx, subject, diff, approval)
}

func SkillCandidatePermissionScope(candidateID string) string {
	return "generated_skill_candidate:" + strings.TrimSpace(candidateID)
}

func validateSkillCandidateInput(candidate GeneratedSkillCandidate) error {
	switch {
	case candidate.Name == "":
		return fmt.Errorf("skill candidate name is required")
	case candidate.RoleScope == "":
		return fmt.Errorf("skill candidate role scope is required")
	case candidate.Trigger == "":
		return fmt.Errorf("skill candidate trigger is required")
	case len(candidate.SourceValidationRefs) == 0:
		return fmt.Errorf("skill candidate requires validation evidence refs")
	case len(candidate.SourceMemeIDs)+len(candidate.SourceNodeIDs)+len(candidate.SourceClaimIDs) == 0:
		return fmt.Errorf("skill candidate requires source meme, node, or claim evidence")
	default:
		return nil
	}
}

func markSkillCandidateRejected(candidate *GeneratedSkillCandidate, reason string) {
	markSkillCandidateStatus(candidate, SkillCandidateStatusRejected, reason)
}

func markSkillCandidateStatus(candidate *GeneratedSkillCandidate, status, reason string) {
	candidate.Status = status
	candidate.Metadata["rejection_reason"] = strings.TrimSpace(reason)
	candidate.UpdatedAt = time.Now().UTC()
}

func skillNameExists(name string, existing []string) bool {
	needle := normalizeText(name)
	for _, value := range existing {
		if normalizeText(value) == needle {
			return true
		}
	}
	return false
}

func containsSelfActivationRequest(candidate *GeneratedSkillCandidate) bool {
	text := normalizeText(candidate.Name + " " + candidate.Trigger + " " + candidate.PromotionRationale + " " + encodeStringList(candidate.PermissionDiff))
	return containsAnyPhrase(text, deniedSelfActivationPhrases())
}

func containsAnyPhrase(text string, phrases []string) bool {
	for _, denied := range phrases {
		if strings.Contains(text, normalizeText(denied)) {
			return true
		}
	}
	return false
}

func deniedSelfActivationPhrases() []string {
	return []string{"install itself", "activate itself", "self activate", "self-install"}
}

func deniedSkillAuthorityPhrases() []string {
	return []string{"install skill", "activate skill", "load extension", "grant permission", "bypass guardian"}
}

func hiddenPermissionPhrases() []string {
	return []string{"network access", "filesystem write", "shell command", "external api", "user approval bypass"}
}

func skillFilesContent(files []SkillCandidateFile) string {
	var builder strings.Builder
	for _, file := range files {
		builder.WriteString(file.Content)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func skillCandidateFiles(candidate GeneratedSkillCandidate) []SkillCandidateFile {
	slug := skillCandidateSlug(candidate.Name)
	files := []SkillCandidateFile{
		{
			Path:     path.Join(slug, "SKILL.md"),
			FileKind: "skill_manifest",
			Content:  skillCandidateManifest(candidate),
		},
		{
			Path:     path.Join(slug, "examples", "positive.json"),
			FileKind: "example_fixture",
			Content: marshalJSON(map[string]any{
				"trigger":          candidate.Trigger,
				"role_scope":       candidate.RoleScope,
				"source_meme_ids":  candidate.SourceMemeIDs,
				"source_node_ids":  candidate.SourceNodeIDs,
				"source_claim_ids": candidate.SourceClaimIDs,
			}),
		},
		{
			Path:     path.Join(slug, "validators", "static.json"),
			FileKind: "validation_harness",
			Content: marshalJSON(map[string]any{
				"validators":    skillCandidateValidators(),
				"proposal_only": true,
				"regression_set": map[string]any{
					"baseline_behavior":  "no_active_skill_change",
					"candidate_behavior": candidate.Trigger,
				},
			}),
		},
		{
			Path:     path.Join(slug, "safety.md"),
			FileKind: "safety_case",
			Content:  skillCandidateSafetyCase(candidate),
		},
	}
	for i := range files {
		files[i].ContentHash = stableID("skill_candidate_file", files[i].Path, files[i].Content)
	}
	return files
}

func skillCandidateManifest(candidate GeneratedSkillCandidate) string {
	lines := []string{
		"# " + candidate.Name,
		"",
		"Role: " + candidate.RoleScope,
		"Trigger: " + candidate.Trigger,
		"Artifact-Type: " + candidate.ArtifactType,
		"Proposal-Only: true",
		"",
		"Source validation refs:",
	}
	for _, ref := range candidate.SourceValidationRefs {
		lines = append(lines, "- "+ref)
	}
	return strings.Join(lines, "\n") + "\n"
}

func skillCandidateSafetyCase(candidate GeneratedSkillCandidate) string {
	lines := []string{
		"# Safety Case",
		"",
		"This generated skill candidate is inert until guardian-approved activation.",
		"Architecture refs: docs/EMERGENT_FOREST.md, docs/MEMORY_FOREST.md",
		"Permission diff: " + encodeStringList(candidate.PermissionDiff),
		"Explicit permission claim: " + candidate.ExplicitPermissionClaimID,
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m *MemoryForest) persistSkillCandidate(ctx context.Context, candidate GeneratedSkillCandidate, files []SkillCandidateFile) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin skill candidate tx: %w", err)
	}
	defer tx.Rollback()
	if err := upsertSkillCandidateTx(ctx, tx, candidate); err != nil {
		return err
	}
	if err := m.upsertSkillCandidateArtifactTx(ctx, tx, candidate); err != nil {
		return err
	}
	if candidate.Status == SkillCandidateStatusProposed {
		if _, err := insertGovernanceProposalTx(ctx, tx, skillCandidateGovernanceProposal(candidate)); err != nil {
			return err
		}
	}
	for _, file := range files {
		if err := upsertSkillCandidateFileTx(ctx, tx, candidate.CandidateID, file); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit skill candidate tx: %w", err)
	}
	return nil
}

func (m *MemoryForest) upsertSkillCandidateArtifactTx(ctx context.Context, tx *sql.Tx, candidate GeneratedSkillCandidate) error {
	if ok, err := tableExistsTx(ctx, tx, "forest_artifacts"); err != nil || !ok {
		return err
	}
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
			payload_hash = excluded.payload_hash,
			metadata = excluded.metadata
	`, skillCandidateArtifactID(candidate.CandidateID), generatedSkillUtilityClaimID(candidate), "memory_forest",
		candidate.Name, SkillCandidateArtifactType, "skill_candidate", "forest_skill_candidate:"+candidate.CandidateID,
		skillCandidateArtifactStatus(candidate), skillCandidateArtifactValidationStatus(candidate), now, now, stableID("skill_candidate_payload", candidate.CandidateID, marshalJSON(candidate)),
		marshalJSON(map[string]any{
			"candidate_id":           candidate.CandidateID,
			"proposal_only":          true,
			"source_meme_ids":        candidate.SourceMemeIDs,
			"source_validation_refs": candidate.SourceValidationRefs,
			"permission_diff":        candidate.PermissionDiff,
		}))
	if err != nil {
		return fmt.Errorf("upsert skill candidate artifact: %w", err)
	}
	return nil
}

func skillCandidateArtifactID(candidateID string) string {
	return stableID(SkillCandidateArtifactType, strings.TrimSpace(candidateID))
}

func skillCandidateArtifactStatus(candidate GeneratedSkillCandidate) string {
	return skillCandidateArtifactStatusForStatus(candidate.Status)
}

func skillCandidateArtifactStatusForStatus(status string) string {
	if status == SkillCandidateStatusRejected || status == SkillCandidateStatusDuplicate {
		return "rejected"
	}
	if status == SkillCandidateStatusAcceptedPendingActivation {
		return "accepted"
	}
	return "proposed"
}

func skillCandidateArtifactValidationStatus(candidate GeneratedSkillCandidate) string {
	return skillCandidateArtifactValidationStatusForStatus(candidate.Status)
}

func skillCandidateArtifactValidationStatusForStatus(status string) string {
	if status == SkillCandidateStatusRejected || status == SkillCandidateStatusDuplicate {
		return "failed"
	}
	if status == SkillCandidateStatusValidated || status == SkillCandidateStatusPromotionProposed || status == SkillCandidateStatusAcceptedPendingActivation {
		return "validated"
	}
	return "requires_validation"
}

func upsertSkillCandidateTx(ctx context.Context, tx *sql.Tx, candidate GeneratedSkillCandidate) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_skill_candidates
			(candidate_id, candidate_key, name, role_scope, trigger, status,
			 artifact_type, source_meme_ids, source_node_ids, source_claim_ids,
			 source_validation_refs, rejected_variant_ids, permission_diff,
			 explicit_permission_claim_id, promotion_rationale,
			 guardian_review_required, created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(candidate_key) DO UPDATE SET
			status = excluded.status,
			source_meme_ids = excluded.source_meme_ids,
			source_node_ids = excluded.source_node_ids,
			source_claim_ids = excluded.source_claim_ids,
			source_validation_refs = excluded.source_validation_refs,
			rejected_variant_ids = excluded.rejected_variant_ids,
			permission_diff = excluded.permission_diff,
			explicit_permission_claim_id = excluded.explicit_permission_claim_id,
			promotion_rationale = excluded.promotion_rationale,
			guardian_review_required = excluded.guardian_review_required,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata
	`, candidate.CandidateID, candidate.CandidateKey, candidate.Name, candidate.RoleScope, candidate.Trigger, candidate.Status,
		candidate.ArtifactType, encodeStringList(candidate.SourceMemeIDs), encodeStringList(candidate.SourceNodeIDs),
		encodeStringList(candidate.SourceClaimIDs), encodeStringList(candidate.SourceValidationRefs),
		encodeStringList(candidate.RejectedVariantIDs), encodeStringList(candidate.PermissionDiff),
		candidate.ExplicitPermissionClaimID, candidate.PromotionRationale, boolInt(candidate.GuardianReviewRequired),
		candidate.CreatedAt.Unix(), candidate.UpdatedAt.Unix(), marshalJSON(candidate.Metadata))
	if err != nil {
		return fmt.Errorf("upsert skill candidate: %w", err)
	}
	return nil
}

func upsertSkillCandidateFileTx(ctx context.Context, tx *sql.Tx, candidateID string, file SkillCandidateFile) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_skill_candidate_files
			(candidate_id, path, content_hash, content, file_kind, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(candidate_id, path) DO UPDATE SET
			content_hash = excluded.content_hash,
			content = excluded.content,
			file_kind = excluded.file_kind
	`, candidateID, file.Path, file.ContentHash, file.Content, file.FileKind, time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("upsert skill candidate file: %w", err)
	}
	return nil
}

func (m *MemoryForest) proposeSkillCandidateClaim(ctx context.Context, candidate GeneratedSkillCandidate) error {
	if len(candidate.SourceValidationRefs) == 0 || candidate.Status != SkillCandidateStatusProposed {
		return nil
	}
	for _, proposal := range generatedSkillCandidateClaims(candidate) {
		if err := m.ProposeForestClaim(ctx, proposal); err != nil {
			return err
		}
	}
	return nil
}

func generatedSkillCandidateClaims(candidate GeneratedSkillCandidate) []ForestClaimProposal {
	dimensions := []string{
		"generated_skill_utility",
		"generated_skill_safety",
		"generated_skill_correctness",
		"generated_skill_permission_behavior",
		"generated_skill_regression_risk",
	}
	proposals := make([]ForestClaimProposal, 0, len(dimensions))
	for _, dimension := range dimensions {
		proposals = append(proposals, ForestClaimProposal{
			ID:                     generatedSkillClaimID(candidate, dimension),
			ClusterID:              "skill_foundry",
			Dimension:              dimension,
			Summary:                generatedSkillClaimSummary(candidate, dimension),
			EvidenceRefs:           candidate.SourceValidationRefs,
			GuardianReviewRequired: true,
		})
	}
	return proposals
}

func generatedSkillUtilityClaimID(candidate GeneratedSkillCandidate) string {
	return generatedSkillClaimID(candidate, "generated_skill_utility")
}

func skillCandidatePromotionProposalID(candidateID string) string {
	return "skill_candidate_promotion:" + stableID(strings.TrimSpace(candidateID))
}

func skillCandidateGovernanceProposal(candidate GeneratedSkillCandidate) ForestGovernanceProposal {
	now := time.Now().UTC()
	return ForestGovernanceProposal{
		ProposalID:             governedProposalID(SkillCandidateArtifactType, "skill_foundry", candidate.CandidateID, candidate.SourceValidationRefs),
		Kind:                   ForestProposalKindSkillCandidate,
		SubjectType:            "skill_foundry",
		SubjectID:              candidate.CandidateID,
		Status:                 ForestProposalStatusProposed,
		ClaimID:                generatedSkillUtilityClaimID(candidate),
		ArtifactID:             skillCandidateArtifactID(candidate.CandidateID),
		EvidenceRefs:           dedupeStrings(candidate.SourceValidationRefs),
		RollbackPath:           "delete_inert_generated_skill_candidate_artifacts",
		GuardianReviewRequired: true,
		PermissionDiff:         AuthorityDiffFromPermissions(candidate.PermissionDiff),
		ApprovalClaimID:        candidate.ExplicitPermissionClaimID,
		Metadata: map[string]any{
			"candidate_id":  candidate.CandidateID,
			"summary":       "Generated skill candidate proposed: " + candidate.Name,
			"proposal_only": true,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func generatedSkillClaimID(candidate GeneratedSkillCandidate, dimension string) string {
	return "skill_candidate_claim:" + stableID(candidate.CandidateID, dimension, encodeStringList(candidate.SourceValidationRefs))
}

func generatedSkillClaimSummary(candidate GeneratedSkillCandidate, dimension string) string {
	switch dimension {
	case "generated_skill_safety":
		return "Generated skill candidate safety case proposed: " + candidate.Name
	case "generated_skill_correctness":
		return "Generated skill candidate correctness case proposed: " + candidate.Name
	case "generated_skill_permission_behavior":
		return "Generated skill candidate permission behavior proposed: " + candidate.Name
	case "generated_skill_regression_risk":
		return "Generated skill candidate regression risk proposed: " + candidate.Name
	default:
		return "Generated skill candidate utility proposed: " + candidate.Name
	}
}

func (m *MemoryForest) ValidateSkillCandidate(ctx context.Context, candidateID string) ([]SkillCandidateValidation, error) {
	candidate, files, err := m.loadSkillCandidate(ctx, candidateID)
	if err != nil {
		return nil, err
	}
	validations := runSkillCandidateValidators(candidate, files)
	for _, validation := range validations {
		if err := m.persistSkillCandidateValidation(ctx, candidate.CandidateID, validation); err != nil {
			return nil, err
		}
	}
	status := SkillCandidateStatusValidated
	if skillValidationsFailed(validations) {
		status = SkillCandidateStatusRejected
		if err := m.recordSkillCandidateNegativeMeme(ctx, candidate, validations); err != nil {
			return validations, err
		}
	}
	if err := m.updateSkillCandidateStatus(ctx, candidate.CandidateID, status); err != nil {
		return validations, err
	}
	return validations, nil
}

func runSkillCandidateValidators(candidate GeneratedSkillCandidate, files []SkillCandidateFile) []SkillCandidateValidation {
	fileMap := make(map[string]SkillCandidateFile, len(files))
	for _, file := range files {
		fileMap[path.Base(file.Path)] = file
		fileMap[file.Path] = file
	}
	return []SkillCandidateValidation{
		validateSkillManifest(candidate, fileMap["SKILL.md"]),
		validateSkillTriggerSpecificity(candidate),
		validateSkillProposalSafety(candidate),
		validateSkillFixtures(fileMap),
		validateSkillSourceEvidence(candidate),
		validateSkillToolAuthority(candidate, files),
		validateSkillFixtureExecution(candidate, fileMap),
		validateSkillRegressionComparison(fileMap),
		validateSkillHiddenPermissionExpansion(candidate, files),
	}
}

func validateSkillManifest(candidate GeneratedSkillCandidate, file SkillCandidateFile) SkillCandidateValidation {
	ok := strings.Contains(file.Content, "# "+candidate.Name) &&
		strings.Contains(file.Content, "Trigger: "+candidate.Trigger) &&
		strings.Contains(file.Content, "Proposal-Only: true")
	return skillValidation("skill_manifest_structure", ok, "SKILL.md contains manifest, trigger, and proposal-only declaration", candidate.SourceValidationRefs)
}

func validateSkillTriggerSpecificity(candidate GeneratedSkillCandidate) SkillCandidateValidation {
	return skillValidation("trigger_specificity", len(strings.Fields(candidate.Trigger)) >= nodeProjectionRetryLimit(), "trigger is specific enough to avoid broad auto-activation", candidate.SourceValidationRefs)
}

func validateSkillProposalSafety(candidate GeneratedSkillCandidate) SkillCandidateValidation {
	ok := candidate.Status == SkillCandidateStatusProposed && !containsSelfActivationRequest(&candidate)
	return skillValidation("proposal_only_safety", ok, "candidate is inert and cannot self-activate", candidate.SourceValidationRefs)
}

func validateSkillFixtures(files map[string]SkillCandidateFile) SkillCandidateValidation {
	_, exampleOK := files["positive.json"]
	_, validatorOK := files["static.json"]
	return skillValidation("fixtures_present", exampleOK && validatorOK, "candidate includes example fixture and static validator harness", nil)
}

func validateSkillSourceEvidence(candidate GeneratedSkillCandidate) SkillCandidateValidation {
	ok := len(candidate.SourceValidationRefs) > 0 && len(candidate.SourceMemeIDs)+len(candidate.SourceNodeIDs)+len(candidate.SourceClaimIDs) > 0
	return skillValidation("source_evidence", ok, "candidate has source evidence and validation refs", candidate.SourceValidationRefs)
}

func validateSkillToolAuthority(candidate GeneratedSkillCandidate, files []SkillCandidateFile) SkillCandidateValidation {
	text := normalizeText(candidate.PromotionRationale + " " + skillFilesContent(files))
	ok := !containsAnyPhrase(text, deniedSkillAuthorityPhrases())
	return skillValidation("tool_authority", ok, "candidate does not grant tools, permissions, installation, or activation authority", candidate.SourceValidationRefs)
}

func validateSkillFixtureExecution(candidate GeneratedSkillCandidate, files map[string]SkillCandidateFile) SkillCandidateValidation {
	var fixture struct {
		Trigger     string   `json:"trigger"`
		Role        string   `json:"role_scope"`
		SourceMeme  []string `json:"source_meme_ids"`
		SourceNode  []string `json:"source_node_ids"`
		SourceClaim []string `json:"source_claim_ids"`
	}
	err := json.Unmarshal([]byte(files["positive.json"].Content), &fixture)
	sourceCount := len(fixture.SourceMeme) + len(fixture.SourceNode) + len(fixture.SourceClaim)
	ok := err == nil && strings.TrimSpace(fixture.Trigger) == candidate.Trigger && strings.TrimSpace(fixture.Role) == candidate.RoleScope && sourceCount > 0
	return skillValidation("fixture_execution", ok, "positive fixture parses and binds to the candidate trigger, role, and source memes", candidate.SourceValidationRefs)
}

func validateSkillRegressionComparison(files map[string]SkillCandidateFile) SkillCandidateValidation {
	var harness struct {
		Validators    []string       `json:"validators"`
		ProposalOnly  bool           `json:"proposal_only"`
		RegressionSet map[string]any `json:"regression_set"`
	}
	err := json.Unmarshal([]byte(files["static.json"].Content), &harness)
	ok := err == nil && harness.ProposalOnly && len(harness.Validators) >= len(skillCandidateValidators()) && len(harness.RegressionSet) > 0
	return skillValidation("regression_comparison", ok, "static validator harness declares proposal-only regression comparison fixtures", nil)
}

func validateSkillHiddenPermissionExpansion(candidate GeneratedSkillCandidate, files []SkillCandidateFile) SkillCandidateValidation {
	text := normalizeText(skillFilesContent(files))
	hasHiddenPermission := containsAnyPhrase(text, hiddenPermissionPhrases())
	ok := !hasHiddenPermission || len(candidate.PermissionDiff) > 0
	return skillValidation("hidden_permission_expansion", ok, "candidate files contain no hidden permission expansion beyond the declared diff", candidate.SourceValidationRefs)
}

func skillValidation(name string, ok bool, summary string, evidenceRefs []string) SkillCandidateValidation {
	status := SkillValidationStatusFailed
	if ok {
		status = SkillValidationStatusPassed
	}
	return SkillCandidateValidation{Validator: name, Status: status, Summary: summary, EvidenceRefs: evidenceRefs}
}

func (m *MemoryForest) persistSkillCandidateValidation(ctx context.Context, candidateID string, validation SkillCandidateValidation) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO forest_skill_candidate_validations
			(validation_id, candidate_id, validator, status, summary,
			 evidence_refs, recorded_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, '{}')
		ON CONFLICT(candidate_id, validator) DO UPDATE SET
			status = excluded.status,
			summary = excluded.summary,
			evidence_refs = excluded.evidence_refs,
			recorded_at = excluded.recorded_at
	`, stableID("skill_candidate_validation", candidateID, validation.Validator), candidateID, validation.Validator,
		validation.Status, validation.Summary, encodeStringList(validation.EvidenceRefs), time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("persist skill candidate validation: %w", err)
	}
	return nil
}

func (m *MemoryForest) updateSkillCandidateStatus(ctx context.Context, candidateID, status string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin skill candidate status tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE forest_skill_candidates
		SET status = ?, updated_at = ?
		WHERE candidate_id = ?
	`, status, time.Now().UTC().Unix(), candidateID); err != nil {
		return fmt.Errorf("update skill candidate status: %w", err)
	}
	if ok, err := tableExistsTx(ctx, tx, "forest_artifacts"); err != nil {
		return err
	} else if ok {
		if _, err := tx.ExecContext(ctx, `
			UPDATE forest_artifacts
			SET status = ?, validation_status = ?, last_seen_at = ?
			WHERE artifact_id = ?
		`, skillCandidateArtifactStatusForStatus(status), skillCandidateArtifactValidationStatusForStatus(status), time.Now().UTC().Unix(), skillCandidateArtifactID(candidateID)); err != nil {
			return fmt.Errorf("update skill candidate artifact status: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit skill candidate status tx: %w", err)
	}
	return nil
}

func (m *MemoryForest) PromoteSkillCandidate(ctx context.Context, candidateID string) error {
	candidate, _, err := m.loadSkillCandidate(ctx, candidateID)
	if err != nil {
		return err
	}
	passed, err := m.skillCandidateValidationGate(ctx, candidate.CandidateID)
	if err != nil {
		return err
	}
	if !passed {
		return fmt.Errorf("skill candidate %s has not passed required validations", candidate.CandidateID)
	}
	proposalID := skillCandidatePromotionProposalID(candidate.CandidateID)
	if _, err := m.ProposeGovernedForestChange(ctx, ForestGovernedChange{
		Kind:                   SkillCandidateArtifactType,
		SubjectType:            "skill_foundry",
		SubjectID:              candidate.CandidateID,
		Summary:                "Generated skill candidate promotion proposed: " + candidate.Name,
		EvidenceRefs:           candidate.SourceValidationRefs,
		RollbackPath:           "keep_generated_skill_candidate_inert",
		GuardianReviewRequired: true,
		Metadata: map[string]any{
			"promotion_proposal_id": proposalID,
			"status":                SkillCandidateStatusPromotionProposed,
		},
	}); err != nil {
		return err
	}
	if err := m.updateSkillCandidateStatus(ctx, candidate.CandidateID, SkillCandidateStatusPromotionProposed); err != nil {
		return err
	}
	return m.ProposeForestClaim(ctx, ForestClaimProposal{
		ID:                     proposalID,
		ClusterID:              "skill_foundry",
		Dimension:              "skill_candidate_promotion",
		Summary:                "Generated skill candidate promotion proposed: " + candidate.Name,
		EvidenceRefs:           candidate.SourceValidationRefs,
		GuardianReviewRequired: true,
	})
}

func (m *MemoryForest) AcceptSkillCandidatePromotion(ctx context.Context, candidateID, acceptedClaimID string) error {
	candidate, _, err := m.loadSkillCandidate(ctx, candidateID)
	if err != nil {
		return err
	}
	if candidate.Status != SkillCandidateStatusPromotionProposed {
		return fmt.Errorf("skill candidate %s status %s is not acceptance-ready", candidate.CandidateID, candidate.Status)
	}
	accepted, err := m.acceptedForestClaimExists(ctx, acceptedClaimID)
	if err != nil {
		return err
	}
	if !accepted {
		return fmt.Errorf("accepted skill promotion claim %s not found", acceptedClaimID)
	}
	if err := m.AcceptGovernanceProposal(ctx, governedProposalID(SkillCandidateArtifactType, "skill_foundry", candidate.CandidateID, candidate.SourceValidationRefs), acceptedClaimID); err != nil {
		return err
	}
	return m.updateSkillCandidateStatus(ctx, candidate.CandidateID, SkillCandidateStatusAcceptedPendingActivation)
}

func (m *MemoryForest) ActivateSkillCandidate(ctx context.Context, candidateID string) error {
	return fmt.Errorf("skill candidate activation is not supported by memory forest; approval must occur outside proposal-only generation")
}

func (m *MemoryForest) acceptedForestClaimExists(ctx context.Context, claimID string) (bool, error) {
	if m == nil || m.db == nil {
		return false, fmt.Errorf("memory forest is required")
	}
	return acceptedForestClaimExists(ctx, m.db, claimID)
}

func (m *MemoryForest) skillCandidateValidationGate(ctx context.Context, candidateID string) (bool, error) {
	var failed int
	if err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_skill_candidate_validations
		WHERE candidate_id = ? AND status != ?
	`, candidateID, SkillValidationStatusPassed).Scan(&failed); err != nil {
		return false, fmt.Errorf("count failed skill validations: %w", err)
	}
	var passed int
	if err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_skill_candidate_validations
		WHERE candidate_id = ? AND status = ?
	`, candidateID, SkillValidationStatusPassed).Scan(&passed); err != nil {
		return false, fmt.Errorf("count passed skill validations: %w", err)
	}
	return failed == 0 && passed >= len(skillCandidateValidators()), nil
}

func (m *MemoryForest) loadSkillCandidate(ctx context.Context, candidateID string) (GeneratedSkillCandidate, []SkillCandidateFile, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT candidate_id, candidate_key, name, role_scope, trigger, status,
		       artifact_type, source_meme_ids, source_node_ids, source_claim_ids,
		       source_validation_refs, rejected_variant_ids, permission_diff,
		       explicit_permission_claim_id, promotion_rationale,
		       guardian_review_required, created_at, updated_at, metadata
		FROM forest_skill_candidates
		WHERE candidate_id = ? OR candidate_key = ?
	`, strings.TrimSpace(candidateID), strings.TrimSpace(candidateID))
	candidate, err := scanSkillCandidate(row)
	if err != nil {
		return GeneratedSkillCandidate{}, nil, err
	}
	files, err := loadSkillCandidateFiles(ctx, m.db, candidate.CandidateID)
	if err != nil {
		return GeneratedSkillCandidate{}, nil, err
	}
	candidate.Files = files
	return candidate, files, nil
}

func scanSkillCandidate(row interface{ Scan(dest ...any) error }) (GeneratedSkillCandidate, error) {
	var sourceMemes, sourceNodes, sourceClaims, validations, rejected, permissions, metadata string
	var guardian int
	var createdAt, updatedAt int64
	var candidate GeneratedSkillCandidate
	if err := row.Scan(&candidate.CandidateID, &candidate.CandidateKey, &candidate.Name, &candidate.RoleScope, &candidate.Trigger, &candidate.Status,
		&candidate.ArtifactType, &sourceMemes, &sourceNodes, &sourceClaims, &validations, &rejected, &permissions,
		&candidate.ExplicitPermissionClaimID, &candidate.PromotionRationale, &guardian, &createdAt, &updatedAt, &metadata); err != nil {
		return GeneratedSkillCandidate{}, fmt.Errorf("scan skill candidate: %w", err)
	}
	candidate.SourceMemeIDs = decodeStringList(sourceMemes)
	candidate.SourceNodeIDs = decodeStringList(sourceNodes)
	candidate.SourceClaimIDs = decodeStringList(sourceClaims)
	candidate.SourceValidationRefs = decodeStringList(validations)
	candidate.RejectedVariantIDs = decodeStringList(rejected)
	candidate.PermissionDiff = decodeStringList(permissions)
	candidate.GuardianReviewRequired = guardian != 0
	candidate.CreatedAt = time.Unix(createdAt, 0).UTC()
	candidate.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	candidate.Metadata = map[string]any{}
	_ = unmarshalJSON(metadata, &candidate.Metadata)
	return candidate, nil
}

func loadSkillCandidateFiles(ctx context.Context, db *sql.DB, candidateID string) ([]SkillCandidateFile, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT path, content_hash, content, file_kind
		FROM forest_skill_candidate_files
		WHERE candidate_id = ?
		ORDER BY path ASC
	`, candidateID)
	if err != nil {
		return nil, fmt.Errorf("load skill candidate files: %w", err)
	}
	defer rows.Close()
	var files []SkillCandidateFile
	for rows.Next() {
		var file SkillCandidateFile
		if err := rows.Scan(&file.Path, &file.ContentHash, &file.Content, &file.FileKind); err != nil {
			return nil, fmt.Errorf("scan skill candidate file: %w", err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate skill candidate files: %w", err)
	}
	return files, nil
}

func skillValidationsFailed(validations []SkillCandidateValidation) bool {
	for _, validation := range validations {
		if validation.Status != SkillValidationStatusPassed {
			return true
		}
	}
	return false
}

func (m *MemoryForest) recordSkillCandidateNegativeMeme(ctx context.Context, candidate GeneratedSkillCandidate, validations []SkillCandidateValidation) error {
	if err := ensureForestMemeSchema(m.db); err != nil {
		return err
	}
	failed := make([]string, 0, len(validations))
	for _, validation := range validations {
		if validation.Status == SkillValidationStatusFailed {
			failed = append(failed, validation.Validator)
		}
	}
	group := memeExtractionGroup{
		kind:      MemeKindNegativePattern,
		polarity:  MemePolarityNegative,
		signature: memeSignature(candidate.Name + " " + encodeStringList(failed)),
		summary:   "Rejected skill candidate " + candidate.Name + " failed validators " + encodeStringList(failed),
		nodeIDs:   []string{candidate.CandidateID},
		grades:    []EvidenceGrade{EvidenceGradeFailed},
	}
	return upsertMemeExtraction(ctx, m.db, group, MemeStatusActive)
}

func skillCandidateValidators() []string {
	return []string{
		"skill_manifest_structure",
		"trigger_specificity",
		"proposal_only_safety",
		"fixtures_present",
		"source_evidence",
		"tool_authority",
		"fixture_execution",
		"regression_comparison",
		"hidden_permission_expansion",
	}
}

func skillCandidateSlug(name string) string {
	normalized := normalizeText(name)
	if normalized == "" {
		return "generated-skill"
	}
	parts := strings.Fields(normalized)
	sort.Strings(parts)
	return strings.Join(parts, "-")
}
