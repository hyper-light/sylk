package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	MemeStatusActive    = "active"
	MemeStatusDormant   = "dormant"
	MemeStatusCandidate = "candidate"
	MemeStatusRejected  = "rejected"

	MemePolarityPositive = "positive"
	MemePolarityNegative = "negative"

	MemeKindValidationRecipe = "validation_recipe"
	MemeKindRemediation      = "remediation_recipe"
	MemeKindNegativePattern  = "negative_pattern"
	MemeKindSkillPattern     = "skill_pattern"

	MemeOperationExtraction    = "extraction"
	MemeOperationRecombination = "recombination"
)

type ForestMeme struct {
	ID                   string
	Kind                 string
	Signature            string
	Status               string
	Polarity             string
	Summary              string
	SchemaVersion        string
	SourceNodeIDs        []string
	SourceArtifactRefs   []string
	SourceValidationRefs []string
	ParentMemeIDs        []string
	Fitness              float64
	CreatedAt            time.Time
	UpdatedAt            time.Time
	Metadata             map[string]any
}

type MemeExtractionResult struct {
	Extracted int
	Activated int
	Dormant   int
	Negative  int
}

type MemeCandidateGenerationResult struct {
	Candidates int
	Rejected   int
}

type memeExtractionGroup struct {
	kind       string
	polarity   string
	signature  string
	summary    string
	nodeIDs    []string
	grades     []EvidenceGrade
	confidence float64
}

func (m *MemoryForest) RunMemeExtraction(ctx context.Context, sessionID string, limit int) (MemeExtractionResult, error) {
	if m == nil || m.db == nil {
		return MemeExtractionResult{}, fmt.Errorf("memory forest is required")
	}
	if err := ensureForestMemeSchema(m.db); err != nil {
		return MemeExtractionResult{}, err
	}
	groups, err := loadMemeExtractionGroups(ctx, m.db, sessionID, resolveMemeLimit(limit))
	if err != nil {
		return MemeExtractionResult{}, err
	}
	var result MemeExtractionResult
	for _, group := range groups {
		status := MemeStatusDormant
		if len(group.nodeIDs) >= memeActivationThreshold() {
			status = MemeStatusActive
			result.Activated++
		} else {
			result.Dormant++
		}
		if group.polarity == MemePolarityNegative {
			result.Negative++
		}
		if err := upsertMemeExtraction(ctx, m.db, group, status); err != nil {
			return result, err
		}
		result.Extracted++
	}
	return result, nil
}

func loadMemeExtractionGroups(ctx context.Context, db *sql.DB, sessionID string, limit int) (map[string]memeExtractionGroup, error) {
	query := `
		SELECT node_id, node_kind, evidence_grade, summary, confidence
		FROM forest_nodes
		WHERE node_kind IN (?, ?, ?, ?)
		  AND evidence_grade IN (?, ?, ?)
	`
	args := []any{
		string(ForestNodeValidation), string(ForestNodeRemediation), string(ForestNodeOutcome), string(ForestNodeContradiction),
		string(EvidenceGradeValidated), string(EvidenceGradeFailed), string(EvidenceGradeContradicted),
	}
	if strings.TrimSpace(sessionID) != "" {
		query += ` AND session_id = ?`
		args = append(args, strings.TrimSpace(sessionID))
	}
	query += ` ORDER BY last_seen_at DESC, node_id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load meme extraction nodes: %w", err)
	}
	defer rows.Close()
	groups := make(map[string]memeExtractionGroup)
	for rows.Next() {
		var nodeID, nodeKind, grade, summary string
		var confidence float64
		if err := rows.Scan(&nodeID, &nodeKind, &grade, &summary, &confidence); err != nil {
			return nil, fmt.Errorf("scan meme extraction node: %w", err)
		}
		kind, polarity, ok := memeKindForNode(ForestNodeKind(nodeKind), EvidenceGrade(grade))
		if !ok {
			continue
		}
		signature := memeSignature(summary)
		if signature == "" {
			continue
		}
		key := strings.Join([]string{kind, polarity, signature}, "\x00")
		group := groups[key]
		if group.kind == "" {
			group.kind = kind
			group.polarity = polarity
			group.signature = signature
			group.summary = strings.TrimSpace(summary)
		}
		group.nodeIDs = append(group.nodeIDs, nodeID)
		group.grades = append(group.grades, EvidenceGrade(grade))
		group.confidence += clampFinite01(confidence)
		groups[key] = group
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate meme extraction nodes: %w", err)
	}
	return groups, nil
}

func memeKindForNode(kind ForestNodeKind, grade EvidenceGrade) (string, string, bool) {
	polarity := MemePolarityPositive
	if grade == EvidenceGradeFailed || grade == EvidenceGradeContradicted {
		polarity = MemePolarityNegative
	}
	switch kind {
	case ForestNodeValidation:
		if polarity == MemePolarityNegative {
			return MemeKindNegativePattern, polarity, true
		}
		return MemeKindValidationRecipe, polarity, true
	case ForestNodeRemediation:
		return MemeKindRemediation, polarity, true
	case ForestNodeOutcome:
		return MemeKindSkillPattern, polarity, true
	case ForestNodeContradiction:
		return MemeKindNegativePattern, MemePolarityNegative, true
	default:
		return "", "", false
	}
}

func upsertMemeExtraction(ctx context.Context, db *sql.DB, group memeExtractionGroup, status string) error {
	now := time.Now().UTC().Unix()
	memeID := stableID("forest_meme", group.kind, group.polarity, group.signature)
	fitness := memeFitness(group)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin meme extraction tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_memes
			(meme_id, meme_kind, signature, status, polarity, summary, schema_version,
			 source_node_ids, source_artifact_refs, source_validation_refs, parent_meme_ids,
			 fitness, created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?, '', ?, ?, ?, ?)
		ON CONFLICT(meme_kind, signature, polarity) DO UPDATE SET
			status = excluded.status,
			source_node_ids = excluded.source_node_ids,
			source_validation_refs = excluded.source_validation_refs,
			fitness = excluded.fitness,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata
	`, memeID, group.kind, group.signature, status, group.polarity, group.summary, memeSchemaVersion(),
		encodeStringList(group.nodeIDs), encodeStringList(memeValidationRefs(group.nodeIDs)), fitness, now, now,
		marshalJSON(map[string]any{"operation": MemeOperationExtraction, "evidence_count": len(group.nodeIDs)})); err != nil {
		return fmt.Errorf("upsert meme: %w", err)
	}
	for _, nodeID := range dedupeStrings(group.nodeIDs) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_meme_sources
				(meme_id, source_ref, source_kind, weight, evidence_grade, recorded_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(meme_id, source_ref) DO UPDATE SET
				weight = excluded.weight,
				evidence_grade = excluded.evidence_grade,
				recorded_at = excluded.recorded_at
		`, memeID, "forest_node:"+nodeID, "forest_node", memeSourceWeight(group), string(memeDominantGrade(group.grades)), now); err != nil {
			return fmt.Errorf("upsert meme source: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_meme_fitness
			(meme_id, fitness_version, fitness, success_count, failure_count,
			 suppression_count, evidence_refs, recorded_at, metadata)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?)
		ON CONFLICT(meme_id, fitness_version) DO UPDATE SET
			fitness = excluded.fitness,
			success_count = excluded.success_count,
			failure_count = excluded.failure_count,
			evidence_refs = excluded.evidence_refs,
			recorded_at = excluded.recorded_at,
			metadata = excluded.metadata
	`, memeID, memeSchemaVersion(), fitness, memeSuccessCount(group.grades), memeFailureCount(group.grades),
		encodeStringList(memeNodeEvidenceRefs(group.nodeIDs)), now, marshalJSON(map[string]any{"status": status})); err != nil {
		return fmt.Errorf("upsert meme fitness: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit meme extraction tx: %w", err)
	}
	return nil
}

func (m *MemoryForest) GenerateMemeCandidates(ctx context.Context, limit int) (MemeCandidateGenerationResult, error) {
	if m == nil || m.db == nil {
		return MemeCandidateGenerationResult{}, fmt.Errorf("memory forest is required")
	}
	memes, err := loadActivePositiveMemes(ctx, m.db, resolveMemeLimit(limit))
	if err != nil {
		return MemeCandidateGenerationResult{}, err
	}
	var result MemeCandidateGenerationResult
	for i := range memes {
		for j := i + 1; j < len(memes) && result.Candidates < resolveMemeLimit(limit); j++ {
			candidate, err := recombineMemes(memes[i], memes[j])
			if err != nil {
				result.Rejected++
				continue
			}
			inserted, err := insertMemeCandidate(ctx, m.db, candidate)
			if err != nil {
				return result, err
			}
			if inserted {
				result.Candidates++
			}
		}
	}
	return result, nil
}

func loadActivePositiveMemes(ctx context.Context, db *sql.DB, limit int) ([]ForestMeme, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT meme_id, meme_kind, signature, status, polarity, summary, schema_version,
		       source_node_ids, source_artifact_refs, source_validation_refs, parent_meme_ids,
		       fitness, created_at, updated_at, metadata
		FROM forest_memes
		WHERE status = ? AND polarity = ?
		ORDER BY fitness DESC, updated_at DESC
		LIMIT ?
	`, MemeStatusActive, MemePolarityPositive, limit)
	if err != nil {
		return nil, fmt.Errorf("load active positive memes: %w", err)
	}
	defer rows.Close()
	var memes []ForestMeme
	for rows.Next() {
		meme, err := scanForestMeme(rows)
		if err != nil {
			return nil, err
		}
		memes = append(memes, meme)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active positive memes: %w", err)
	}
	return memes, nil
}

func scanForestMeme(row interface{ Scan(dest ...any) error }) (ForestMeme, error) {
	var sourceNodeIDs, sourceArtifactRefs, sourceValidationRefs, parentMemeIDs, metadata string
	var createdAt, updatedAt int64
	var meme ForestMeme
	if err := row.Scan(&meme.ID, &meme.Kind, &meme.Signature, &meme.Status, &meme.Polarity, &meme.Summary, &meme.SchemaVersion,
		&sourceNodeIDs, &sourceArtifactRefs, &sourceValidationRefs, &parentMemeIDs, &meme.Fitness, &createdAt, &updatedAt, &metadata); err != nil {
		return ForestMeme{}, fmt.Errorf("scan forest meme: %w", err)
	}
	meme.SourceNodeIDs = decodeStringList(sourceNodeIDs)
	meme.SourceArtifactRefs = decodeStringList(sourceArtifactRefs)
	meme.SourceValidationRefs = decodeStringList(sourceValidationRefs)
	meme.ParentMemeIDs = decodeStringList(parentMemeIDs)
	meme.CreatedAt = time.Unix(createdAt, 0).UTC()
	meme.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	meme.Metadata = map[string]any{}
	_ = unmarshalJSON(metadata, &meme.Metadata)
	return meme, nil
}

func recombineMemes(left, right ForestMeme) (ForestMeme, error) {
	if left.Kind != right.Kind {
		return ForestMeme{}, fmt.Errorf("incompatible meme kinds: %s vs %s", left.Kind, right.Kind)
	}
	if left.Polarity != MemePolarityPositive || right.Polarity != MemePolarityPositive {
		return ForestMeme{}, fmt.Errorf("only positive memes can be recombined")
	}
	summary := strings.TrimSpace(left.Summary + "; " + right.Summary)
	signature := memeSignature(summary)
	if signature == "" || signature == left.Signature || signature == right.Signature {
		return ForestMeme{}, fmt.Errorf("meme recombination produced no behavioral novelty")
	}
	now := time.Now().UTC()
	return ForestMeme{
		ID:            stableID("forest_meme_candidate", left.ID, right.ID, signature),
		Kind:          left.Kind,
		Signature:     signature,
		Status:        MemeStatusCandidate,
		Polarity:      MemePolarityPositive,
		Summary:       summary,
		SchemaVersion: memeSchemaVersion(),
		SourceNodeIDs: dedupeStrings(append(append([]string{}, left.SourceNodeIDs...), right.SourceNodeIDs...)),
		ParentMemeIDs: dedupeStrings([]string{left.ID, right.ID}),
		Fitness:       (left.Fitness + right.Fitness) / float64(policyTrialArmCount()),
		CreatedAt:     now,
		UpdatedAt:     now,
		Metadata:      map[string]any{"operation": MemeOperationRecombination},
	}, nil
}

func insertMemeCandidate(ctx context.Context, db *sql.DB, meme ForestMeme) (bool, error) {
	result, err := db.ExecContext(ctx, `
		INSERT INTO forest_meme_candidates
			(candidate_id, meme_kind, status, parent_meme_ids, signature, summary,
			 generation, rejection_reason, created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?)
		ON CONFLICT(meme_kind, signature, parent_meme_ids) DO NOTHING
	`, meme.ID, meme.Kind, meme.Status, encodeStringList(meme.ParentMemeIDs), meme.Signature, meme.Summary,
		len(meme.ParentMemeIDs), meme.CreatedAt.Unix(), meme.UpdatedAt.Unix(), marshalJSON(meme.Metadata))
	if err != nil {
		return false, fmt.Errorf("insert meme candidate: %w", err)
	}
	inserted, _ := result.RowsAffected()
	if inserted == 0 {
		return false, nil
	}
	if err := insertMemeLineage(ctx, db, meme); err != nil {
		return false, err
	}
	return true, nil
}

func insertMemeLineage(ctx context.Context, db *sql.DB, meme ForestMeme) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO forest_meme_lineage
			(lineage_id, child_meme_id, parent_meme_ids, operation, reason,
			 created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(lineage_id) DO NOTHING
	`, stableID("forest_meme_lineage", meme.ID, encodeStringList(meme.ParentMemeIDs)),
		meme.ID, encodeStringList(meme.ParentMemeIDs), MemeOperationRecombination,
		"compatible positive meme recombination", time.Now().UTC().Unix(), marshalJSON(meme.Metadata))
	if err != nil {
		return fmt.Errorf("insert meme lineage: %w", err)
	}
	return nil
}

func (m *MemoryForest) ActiveNegativeMemeSuppresses(ctx context.Context, text string) (bool, string, error) {
	if m == nil || m.db == nil {
		return false, "", fmt.Errorf("memory forest is required")
	}
	query := normalizeText(text)
	if query == "" {
		return false, "", nil
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT meme_id, signature, summary
		FROM forest_memes
		WHERE status = ? AND polarity = ?
		ORDER BY fitness DESC, updated_at DESC
		LIMIT ?
	`, MemeStatusActive, MemePolarityNegative, resolveMemeLimit(0))
	if err != nil {
		return false, "", fmt.Errorf("query negative memes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var memeID, signature, summary string
		if err := rows.Scan(&memeID, &signature, &summary); err != nil {
			return false, "", fmt.Errorf("scan negative meme: %w", err)
		}
		if memeMatchesText(signature, summary, query) {
			if err := incrementMemeSuppression(ctx, m.db, memeID); err != nil {
				return false, "", err
			}
			return true, memeID, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, "", fmt.Errorf("iterate negative memes: %w", err)
	}
	return false, "", nil
}

func incrementMemeSuppression(ctx context.Context, db *sql.DB, memeID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE forest_meme_fitness
		SET suppression_count = suppression_count + 1,
		    recorded_at = ?
		WHERE meme_id = ?
	`, time.Now().UTC().Unix(), memeID)
	if err != nil {
		return fmt.Errorf("increment meme suppression: %w", err)
	}
	return nil
}

func memeMatchesText(signature, summary, query string) bool {
	return strings.Contains(query, normalizeText(summary)) || strings.Contains(query, normalizeText(signature))
}

func memeSignature(summary string) string {
	return stableID("meme_signature", normalizeText(summary))
}

func memeSchemaVersion() string {
	return "meme_v1"
}

func memeActivationThreshold() int {
	return nodeProjectionRetryLimit()
}

func resolveMemeLimit(limit int) int {
	if limit > 0 && limit <= policyCandidatePopulationLimit()*policyCandidatePopulationLimit() {
		return limit
	}
	return policyCandidatePopulationLimit()
}

func memeFitness(group memeExtractionGroup) float64 {
	total := memeSuccessCount(group.grades) + memeFailureCount(group.grades)
	if total == 0 {
		return 0
	}
	return float64(memeSuccessCount(group.grades)) / float64(total)
}

func memeSuccessCount(grades []EvidenceGrade) int {
	count := 0
	for _, grade := range grades {
		if grade == EvidenceGradeValidated {
			count++
		}
	}
	return count
}

func memeFailureCount(grades []EvidenceGrade) int {
	count := 0
	for _, grade := range grades {
		if grade == EvidenceGradeFailed || grade == EvidenceGradeContradicted {
			count++
		}
	}
	return count
}

func memeSourceWeight(group memeExtractionGroup) float64 {
	if len(group.nodeIDs) == 0 {
		return 0
	}
	return group.confidence / float64(len(group.nodeIDs))
}

func memeDominantGrade(grades []EvidenceGrade) EvidenceGrade {
	if memeFailureCount(grades) > 0 {
		return EvidenceGradeFailed
	}
	return EvidenceGradeValidated
}

func memeValidationRefs(nodeIDs []string) []string {
	refs := make([]string, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		refs = append(refs, "validation:"+nodeID)
	}
	return refs
}

func memeNodeEvidenceRefs(nodeIDs []string) []string {
	refs := make([]string, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		refs = append(refs, "forest_node:"+nodeID)
	}
	return refs
}
