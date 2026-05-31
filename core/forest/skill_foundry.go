package forest

import (
	"context"
	"database/sql"
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
	SkillCandidateStatusAcceptedPendingActivation = "accepted_pending_activation"
	SkillCandidateStatusDuplicate                 = "duplicate"

	SkillCandidateArtifactType = "generated_skill_candidate"

	SkillValidationStatusPassed = "passed"
	SkillValidationStatusFailed = "failed"
)

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
		PermissionDiff:            dedupeStrings(input.RequestedPermissions),
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
	if len(candidate.PermissionDiff) > 0 && candidate.ExplicitPermissionClaimID == "" {
		markSkillCandidateRejected(candidate, "permission expansion requires explicit approval claim")
		return fmt.Errorf("permission expansion requires explicit approval claim")
	}
	if containsSelfActivationRequest(candidate) {
		markSkillCandidateRejected(candidate, "skill candidate cannot activate or install itself")
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
	for _, denied := range []string{"install itself", "activate itself", "self activate", "self-install"} {
		if strings.Contains(text, denied) {
			return true
		}
	}
	return false
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
			Content:  marshalJSON(map[string]any{"trigger": candidate.Trigger, "role_scope": candidate.RoleScope, "source_meme_ids": candidate.SourceMemeIDs}),
		},
		{
			Path:     path.Join(slug, "validators", "static.json"),
			FileKind: "validation_harness",
			Content: marshalJSON(map[string]any{
				"validators":    skillCandidateValidators(),
				"proposal_only": true,
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
	return m.ProposeForestClaim(ctx, ForestClaimProposal{
		ID:                     "skill_candidate_claim:" + stableID(candidate.CandidateID, encodeStringList(candidate.SourceValidationRefs)),
		ClusterID:              "skill_foundry",
		Dimension:              "generated_skill_candidate",
		Summary:                "Generated skill candidate proposed: " + candidate.Name,
		EvidenceRefs:           candidate.SourceValidationRefs,
		GuardianReviewRequired: true,
	})
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
	_, err := m.db.ExecContext(ctx, `
		UPDATE forest_skill_candidates
		SET status = ?, updated_at = ?
		WHERE candidate_id = ?
	`, status, time.Now().UTC().Unix(), candidateID)
	if err != nil {
		return fmt.Errorf("update skill candidate status: %w", err)
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
	if err := m.updateSkillCandidateStatus(ctx, candidate.CandidateID, SkillCandidateStatusAcceptedPendingActivation); err != nil {
		return err
	}
	return m.ProposeForestClaim(ctx, ForestClaimProposal{
		ID:                     "skill_candidate_promotion:" + stableID(candidate.CandidateID),
		ClusterID:              "skill_foundry",
		Dimension:              "skill_candidate_promotion",
		Summary:                "Generated skill candidate accepted pending activation: " + candidate.Name,
		EvidenceRefs:           candidate.SourceValidationRefs,
		GuardianReviewRequired: true,
	})
}

func (m *MemoryForest) ActivateSkillCandidate(ctx context.Context, candidateID string) error {
	return fmt.Errorf("skill candidate activation is not supported by memory forest; approval must occur outside proposal-only generation")
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
