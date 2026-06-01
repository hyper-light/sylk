package forest

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	antigenicPolicyVersion = "antigenic_v1"

	CorruptionStaleEvidence    = "stale_evidence"
	CorruptionFailedValidation = "failed_validation"
	CorruptionContradiction    = "contradiction"
	CorruptionBrokenArtifact   = "broken_artifact"
	CorruptionHarmfulPrecedent = "harmful_precedent"
	CorruptionBadPrediction    = "bad_prediction"

	ImmunityCitedValidation       = "cited_validation"
	ImmunityRepeatedSuccess       = "repeated_success"
	ImmunityCrossAgentAgreement   = "cross_agent_agreement"
	ImmunityDurableArtifact       = "durable_artifact"
	ImmunityResolvedContradiction = "resolved_contradiction"

	OutbreakStatusOpen       = "open"
	OutbreakStatusProposed   = "proposed"
	OutbreakStatusSuperseded = "superseded"
)

type AntigenicMaintenanceResult struct {
	AntigenicInserted int
	ImmunityInserted  int
}

type OutbreakMaintenanceResult struct {
	OutbreaksUpdated int
	ProposalsEmitted int
	Superseded       int
}

type ForestClaimProposal struct {
	ID                     string   `json:"id"`
	ClusterID              string   `json:"cluster_id"`
	Dimension              string   `json:"dimension"`
	Summary                string   `json:"summary"`
	EvidenceRefs           []string `json:"evidence_refs"`
	CounterEvidenceRefs    []string `json:"counter_evidence_refs,omitempty"`
	GuardianReviewRequired bool     `json:"guardian_review_required"`
	SourceOutbreakID       string   `json:"source_outbreak_id"`
}

//go:generate mockery --name=ForestProposalSink --inpackage --output=. --filename=mock_forest_proposal_sink_test.go --outpkg=forest
type ForestProposalSink interface {
	ProposeClaim(ctx context.Context, proposal ForestClaimProposal) error
}

type ClaimProposalSink interface {
	ForestProposalSink
}

func (m *MemoryForest) ProposeForestClaim(ctx context.Context, proposal ForestClaimProposal) error {
	proposal = normalizeForestClaimProposal(proposal)
	if proposal.ID == "" {
		return fmt.Errorf("forest claim proposal id is required")
	}
	if proposal.Summary == "" {
		return fmt.Errorf("forest claim proposal summary is required")
	}
	if len(proposal.EvidenceRefs) == 0 {
		return fmt.Errorf("forest claim proposal requires evidence refs")
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin forest claim proposal tx: %w", err)
	}
	defer tx.Rollback()
	if err := upsertForestClaimProposalArtifactTx(ctx, tx, proposal); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit forest claim proposal tx: %w", err)
	}
	if m.proposalSink != nil {
		if err := m.proposalSink.ProposeClaim(ctx, proposal); err != nil {
			return fmt.Errorf("emit forest claim proposal: %w", err)
		}
	}
	return nil
}

func normalizeForestClaimProposal(proposal ForestClaimProposal) ForestClaimProposal {
	proposal.ID = strings.TrimSpace(proposal.ID)
	proposal.ClusterID = strings.TrimSpace(proposal.ClusterID)
	proposal.Dimension = strings.TrimSpace(proposal.Dimension)
	proposal.Summary = strings.TrimSpace(proposal.Summary)
	proposal.EvidenceRefs = dedupeStrings(proposal.EvidenceRefs)
	proposal.CounterEvidenceRefs = dedupeStrings(proposal.CounterEvidenceRefs)
	if proposal.ID == "" {
		proposal.ID = "forest_claim_proposal:" + stableID(proposal.ClusterID, proposal.Dimension, proposal.Summary, encodeStringList(proposal.EvidenceRefs))
	}
	return proposal
}

func upsertForestClaimProposalArtifactTx(ctx context.Context, tx *sql.Tx, proposal ForestClaimProposal) error {
	now := time.Now().UTC().Unix()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_artifacts
			(artifact_id, claim_id, generator_participant, artifact_name, artifact_kind,
			 data_type, content_ref, status, validation_status, last_sequence,
			 first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
		ON CONFLICT(artifact_id) DO UPDATE SET
			last_seen_at = excluded.last_seen_at,
			metadata = excluded.metadata
	`, proposal.ID, proposal.ID, "memory_forest", "Forest claim proposal", "claim_proposal",
		"claim_proposal", "forest_claim_proposal:"+proposal.ID, "proposed", "requires_validation",
		now, now, stableID("proposal_payload", proposal.ID), marshalJSON(proposal))
	if err != nil {
		return fmt.Errorf("upsert forest claim proposal artifact: %w", err)
	}
	return nil
}

type vectorObservation struct {
	ScopeType    string
	ScopeID      string
	Dimension    string
	SourceKey    string
	Magnitude    float64
	Factor       float64
	EvidenceRefs []string
	RecordedAt   int64
	Metadata     map[string]any
}

func (m *MemoryForest) RunAntigenicMaintenanceForSession(ctx context.Context, sessionID string, limit int) (AntigenicMaintenanceResult, error) {
	m.maintenanceRunMu.Lock()
	defer m.maintenanceRunMu.Unlock()
	return m.runAntigenicMaintenanceForSession(ctx, sessionID, limit)
}

func (m *MemoryForest) RunAntigenicMaintenance(ctx context.Context, limit int) (AntigenicMaintenanceResult, error) {
	m.maintenanceRunMu.Lock()
	defer m.maintenanceRunMu.Unlock()
	return m.runAntigenicMaintenance(ctx, limit)
}

func (m *MemoryForest) runAntigenicMaintenance(ctx context.Context, limit int) (AntigenicMaintenanceResult, error) {
	sessions, err := m.recentNodeSubstrateSessions(ctx)
	if err != nil {
		return AntigenicMaintenanceResult{}, err
	}
	if len(sessions) == 0 {
		sessions = []string{"global"}
	}
	var total AntigenicMaintenanceResult
	for _, sessionID := range sessions {
		result, err := m.runAntigenicMaintenanceForSession(ctx, sessionID, limit)
		if err != nil {
			return total, err
		}
		total.AntigenicInserted += result.AntigenicInserted
		total.ImmunityInserted += result.ImmunityInserted
	}
	return total, nil
}

func (m *MemoryForest) runAntigenicMaintenanceForSession(ctx context.Context, sessionID string, limit int) (AntigenicMaintenanceResult, error) {
	if limit <= 0 {
		limit = m.substrateLimit
	}
	nodes, err := m.loadSubstrateNodes(ctx, sessionID, limit)
	if err != nil {
		return AntigenicMaintenanceResult{}, err
	}
	if len(nodes) == 0 {
		return AntigenicMaintenanceResult{}, nil
	}
	edges, err := m.loadSubstrateNodeEdges(ctx, sortedForestNodeIDs(nodes))
	if err != nil {
		return AntigenicMaintenanceResult{}, err
	}
	antigenic, immunity := vectorObservations(nodes, edges)
	m.substrateStateMu.Lock()
	defer m.substrateStateMu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return AntigenicMaintenanceResult{}, fmt.Errorf("begin antigenic tx: %w", err)
	}
	defer tx.Rollback()
	result, err := insertVectorObservationsTx(ctx, tx, antigenic, immunity)
	if err != nil {
		return AntigenicMaintenanceResult{}, err
	}
	if err := insertClusterVectorObservationsTx(ctx, tx, append(antigenic, immunity...)); err != nil {
		return AntigenicMaintenanceResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AntigenicMaintenanceResult{}, fmt.Errorf("commit antigenic tx: %w", err)
	}
	return result, nil
}

func vectorObservations(nodes []ForestNode, edges []ForestEdge) ([]vectorObservation, []vectorObservation) {
	now := time.Now().UTC().Unix()
	antigenic := make([]vectorObservation, 0, len(nodes)+len(edges))
	immunity := make([]vectorObservation, 0, len(nodes)+len(edges))
	for _, node := range nodes {
		antigenic = append(antigenic, antigenicObservationsForNode(node, now)...)
		immunity = append(immunity, immunityObservationsForNode(node, now)...)
	}
	for _, edge := range edges {
		a, i := vectorObservationsForEdge(edge, now)
		antigenic = append(antigenic, a...)
		immunity = append(immunity, i...)
	}
	return antigenic, immunity
}

func antigenicObservationsForNode(node ForestNode, now int64) []vectorObservation {
	refs := []string{"node:" + node.ID}
	source := vectorSourceKey(node.ID, node.SourceKey, node.SourceSeq)
	switch {
	case node.Kind == ForestNodeContradiction || node.EvidenceGrade == EvidenceGradeContradicted:
		return []vectorObservation{newVectorObservation("node", node.ID, CorruptionContradiction, source, 1, refs, now)}
	case node.EvidenceGrade == EvidenceGradeFailed:
		return []vectorObservation{newVectorObservation("node", node.ID, CorruptionFailedValidation, source, 1, refs, now)}
	case node.Kind == ForestNodeArtifact && strings.Contains(strings.ToLower(node.Status), "failed"):
		return []vectorObservation{newVectorObservation("node", node.ID, CorruptionBrokenArtifact, source, node.Salience, refs, now)}
	default:
		return nil
	}
}

func immunityObservationsForNode(node ForestNode, now int64) []vectorObservation {
	refs := []string{"node:" + node.ID}
	source := vectorSourceKey(node.ID, node.SourceKey, node.SourceSeq)
	switch {
	case node.Kind == ForestNodeRemediation:
		return []vectorObservation{newVectorObservation("node", node.ID, ImmunityResolvedContradiction, source, node.Utility+node.Confidence, refs, now)}
	case node.Kind == ForestNodeValidation && node.EvidenceGrade == EvidenceGradeValidated:
		return []vectorObservation{newVectorObservation("node", node.ID, ImmunityCitedValidation, source, 1, refs, now)}
	case node.Kind == ForestNodeArtifact && node.EvidenceGrade == EvidenceGradeValidated:
		return []vectorObservation{newVectorObservation("node", node.ID, ImmunityDurableArtifact, source, node.Utility+node.Confidence, refs, now)}
	case node.Kind == ForestNodeOutcome && node.EvidenceGrade == EvidenceGradeValidated:
		return []vectorObservation{newVectorObservation("node", node.ID, ImmunityRepeatedSuccess, source, node.Utility+node.Confidence, refs, now)}
	default:
		return nil
	}
}

func vectorObservationsForEdge(edge ForestEdge, now int64) ([]vectorObservation, []vectorObservation) {
	refs := []string{"edge:" + edge.ID}
	source := vectorSourceKey(edge.ID, edge.SourceKey, edge.SourceSeq)
	switch edge.Kind {
	case ForestEdgeContradiction:
		return edgeVectorPair(edge, CorruptionContradiction, source, edge.Weight, refs, now), nil
	case ForestEdgeValidation:
		return nil, edgeVectorPair(edge, ImmunityCitedValidation, source, edge.Weight, refs, now)
	case ForestEdgeRemediation:
		return nil, edgeVectorPair(edge, ImmunityResolvedContradiction, source, edge.Weight, refs, now)
	default:
		return nil, nil
	}
}

func edgeVectorPair(edge ForestEdge, dimension, source string, magnitude float64, refs []string, now int64) []vectorObservation {
	return []vectorObservation{
		newVectorObservation("node", edge.SourceNodeID, dimension, source+":source", magnitude, refs, now),
		newVectorObservation("node", edge.TargetNodeID, dimension, source+":target", magnitude, refs, now),
	}
}

func newVectorObservation(scopeType, scopeID, dimension, sourceKey string, magnitude float64, refs []string, now int64) vectorObservation {
	magnitude = clampFinite01(magnitude)
	return vectorObservation{
		ScopeType:    strings.TrimSpace(scopeType),
		ScopeID:      strings.TrimSpace(scopeID),
		Dimension:    strings.TrimSpace(dimension),
		SourceKey:    strings.TrimSpace(sourceKey),
		Magnitude:    magnitude,
		Factor:       magnitude,
		EvidenceRefs: dedupeStrings(refs),
		RecordedAt:   now,
		Metadata:     map[string]any{"source_refs": refs},
	}
}

func vectorSourceKey(id, sourceKey string, sourceSeq int64) string {
	return stableID("vector_source", id, sourceKey, fmt.Sprint(sourceSeq))
}

func insertVectorObservationsTx(ctx context.Context, tx *sql.Tx, antigenic, immunity []vectorObservation) (AntigenicMaintenanceResult, error) {
	var result AntigenicMaintenanceResult
	for _, observation := range antigenic {
		inserted, err := insertAntigenicVectorTx(ctx, tx, observation)
		if err != nil {
			return AntigenicMaintenanceResult{}, err
		}
		if inserted {
			result.AntigenicInserted++
		}
	}
	for _, observation := range immunity {
		inserted, err := insertImmunityVectorTx(ctx, tx, observation)
		if err != nil {
			return AntigenicMaintenanceResult{}, err
		}
		if inserted {
			result.ImmunityInserted++
		}
	}
	return result, nil
}

func insertAntigenicVectorTx(ctx context.Context, tx *sql.Tx, observation vectorObservation) (bool, error) {
	if err := validateVectorObservation(observation); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO forest_antigenic_vectors
			(vector_id, scope_type, scope_id, dimension, source_key, magnitude,
			 quarantine_factor, evidence_refs, policy_version, recorded_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope_type, scope_id, dimension, source_key, policy_version) DO NOTHING
	`, stableID("antigenic", observation.ScopeType, observation.ScopeID, observation.Dimension, observation.SourceKey),
		observation.ScopeType, observation.ScopeID, observation.Dimension, observation.SourceKey, observation.Magnitude,
		observation.Factor, encodeStringList(observation.EvidenceRefs), antigenicPolicyVersion, observation.RecordedAt, marshalJSON(observation.Metadata))
	if err != nil {
		return false, fmt.Errorf("insert antigenic vector: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

func insertImmunityVectorTx(ctx context.Context, tx *sql.Tx, observation vectorObservation) (bool, error) {
	if err := validateVectorObservation(observation); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO forest_immunity_vectors
			(vector_id, scope_type, scope_id, dimension, source_key, magnitude,
			 recovery_factor, evidence_refs, policy_version, recorded_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope_type, scope_id, dimension, source_key, policy_version) DO NOTHING
	`, stableID("immunity", observation.ScopeType, observation.ScopeID, observation.Dimension, observation.SourceKey),
		observation.ScopeType, observation.ScopeID, observation.Dimension, observation.SourceKey, observation.Magnitude,
		observation.Factor, encodeStringList(observation.EvidenceRefs), antigenicPolicyVersion, observation.RecordedAt, marshalJSON(observation.Metadata))
	if err != nil {
		return false, fmt.Errorf("insert immunity vector: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

func validateVectorObservation(observation vectorObservation) error {
	if observation.ScopeType == "" || observation.ScopeID == "" {
		return fmt.Errorf("vector scope is required")
	}
	if observation.Dimension == "" || observation.SourceKey == "" {
		return fmt.Errorf("vector dimension and source key are required")
	}
	if len(observation.EvidenceRefs) == 0 {
		return fmt.Errorf("vector observation requires evidence refs")
	}
	return nil
}

func insertClusterVectorObservationsTx(ctx context.Context, tx *sql.Tx, observations []vectorObservation) error {
	for _, observation := range observations {
		if observation.ScopeType != "node" {
			continue
		}
		clusterIDs, err := clusterIDsForVectorNodeTx(ctx, tx, observation.ScopeID)
		if err != nil {
			return err
		}
		for _, clusterID := range clusterIDs {
			clusterObservation := observation
			clusterObservation.ScopeType = "cluster"
			clusterObservation.ScopeID = clusterID
			clusterObservation.SourceKey = "cluster:" + clusterID + ":" + observation.SourceKey
			if isImmunityDimension(observation.Dimension) {
				if _, err := insertImmunityVectorTx(ctx, tx, clusterObservation); err != nil {
					return err
				}
				continue
			}
			if _, err := insertAntigenicVectorTx(ctx, tx, clusterObservation); err != nil {
				return err
			}
		}
	}
	return nil
}

func clusterIDsForVectorNodeTx(ctx context.Context, tx *sql.Tx, nodeID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT cluster_id
		FROM forest_cluster_membership
		WHERE node_id = ?
		ORDER BY membership_weight DESC, cluster_id ASC
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query vector memberships: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan vector membership: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func isImmunityDimension(dimension string) bool {
	switch dimension {
	case ImmunityCitedValidation, ImmunityRepeatedSuccess, ImmunityCrossAgentAgreement, ImmunityDurableArtifact, ImmunityResolvedContradiction:
		return true
	default:
		return false
	}
}

func (m *MemoryForest) RunOutbreakMaintenance(ctx context.Context, limit int) (OutbreakMaintenanceResult, error) {
	m.maintenanceRunMu.Lock()
	defer m.maintenanceRunMu.Unlock()
	return m.runOutbreakMaintenance(ctx, limit)
}

func (m *MemoryForest) runOutbreakMaintenance(ctx context.Context, limit int) (OutbreakMaintenanceResult, error) {
	if limit <= 0 {
		limit = m.substrateLimit
	}
	candidates, err := m.loadOutbreakCandidates(ctx, limit)
	if err != nil {
		return OutbreakMaintenanceResult{}, err
	}
	var result OutbreakMaintenanceResult
	for _, candidate := range candidates {
		updated, err := m.upsertOutbreak(ctx, candidate)
		if err != nil {
			return result, err
		}
		result.OutbreaksUpdated += updated.OutbreaksUpdated
		result.ProposalsEmitted += updated.ProposalsEmitted
		result.Superseded += updated.Superseded
	}
	return result, nil
}

type outbreakCandidate struct {
	ClusterID           string
	Dimension           string
	Corruption          float64
	Immunity            float64
	EvidenceCount       int
	EvidenceRefs        []string
	CounterEvidenceRefs []string
	SourceSeq           int64
	BridgeSevere        bool
}

func (m *MemoryForest) loadOutbreakCandidates(ctx context.Context, limit int) ([]outbreakCandidate, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT scope_id, dimension, magnitude, quarantine_factor, recorded_at, evidence_refs
		FROM forest_antigenic_vectors
		WHERE scope_type = 'cluster' AND policy_version = ?
		ORDER BY recorded_at DESC
		LIMIT ?
	`, antigenicPolicyVersion, limit*len(defaultEcologyPolicy().Channels))
	if err != nil {
		return nil, fmt.Errorf("query outbreak candidates: %w", err)
	}
	defer rows.Close()
	grouped, err := scanOutbreakCandidateDeltas(rows, time.Now().UTC().Unix())
	if err != nil {
		return nil, err
	}
	candidates, err := m.hydrateOutbreakCandidates(ctx, grouped)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].SourceSeq == candidates[j].SourceSeq {
			return candidates[i].Corruption > candidates[j].Corruption
		}
		return candidates[i].SourceSeq > candidates[j].SourceSeq
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, rows.Err()
}

func scanOutbreakCandidateDeltas(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}, now int64) (map[string]outbreakCandidate, error) {
	grouped := make(map[string]outbreakCandidate)
	for rows.Next() {
		if err := appendOutbreakCandidateDelta(rows, grouped, now); err != nil {
			return nil, err
		}
	}
	return grouped, rows.Err()
}

func appendOutbreakCandidateDelta(rows interface{ Scan(dest ...any) error }, grouped map[string]outbreakCandidate, now int64) error {
	var refs string
	var magnitude, factor float64
	candidate := outbreakCandidate{}
	if err := rows.Scan(&candidate.ClusterID, &candidate.Dimension, &magnitude, &factor, &candidate.SourceSeq, &refs); err != nil {
		return fmt.Errorf("scan outbreak candidate: %w", err)
	}
	key := outbreakCandidateKey(candidate.ClusterID, candidate.Dimension)
	existing := grouped[key]
	existing.ClusterID = candidate.ClusterID
	existing.Dimension = candidate.Dimension
	existing.Corruption = clampFinite01(existing.Corruption + decayedVectorContribution(magnitude, factor, candidate.SourceSeq, now))
	existing.EvidenceCount++
	if candidate.SourceSeq > existing.SourceSeq {
		existing.SourceSeq = candidate.SourceSeq
	}
	existing.EvidenceRefs = append(existing.EvidenceRefs, decodeStringList(refs)...)
	grouped[key] = existing
	return nil
}

func outbreakCandidateKey(clusterID, dimension string) string {
	return strings.TrimSpace(clusterID) + "\x00" + strings.TrimSpace(dimension)
}

func (m *MemoryForest) hydrateOutbreakCandidates(ctx context.Context, grouped map[string]outbreakCandidate) ([]outbreakCandidate, error) {
	candidates := make([]outbreakCandidate, 0, len(grouped))
	for _, candidate := range grouped {
		hydrated, err := m.hydrateOutbreakCandidate(ctx, candidate)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, hydrated)
	}
	return candidates, nil
}

func (m *MemoryForest) hydrateOutbreakCandidate(ctx context.Context, candidate outbreakCandidate) (outbreakCandidate, error) {
	candidate.EvidenceRefs = dedupeStrings(candidate.EvidenceRefs)
	immunity, counterRefs, err := m.clusterImmunityForDimension(ctx, candidate.ClusterID, candidate.Dimension)
	if err != nil {
		return outbreakCandidate{}, err
	}
	candidate.Immunity = immunity
	candidate.CounterEvidenceRefs = counterRefs
	severe, err := m.clusterHasBridgeRisk(ctx, candidate.ClusterID)
	if err != nil {
		return outbreakCandidate{}, err
	}
	candidate.BridgeSevere = severe
	return candidate, nil
}

func (m *MemoryForest) clusterImmunityForDimension(ctx context.Context, clusterID, dimension string) (float64, []string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT magnitude, recorded_at, evidence_refs
		FROM forest_immunity_vectors
		WHERE scope_type = 'cluster' AND scope_id = ? AND policy_version = ?
	`, clusterID, antigenicPolicyVersion)
	if err != nil {
		return 0, nil, fmt.Errorf("query outbreak immunity: %w", err)
	}
	defer rows.Close()
	var total float64
	var refs []string
	now := time.Now().UTC().Unix()
	for rows.Next() {
		var magnitude float64
		var recordedAt int64
		var encoded string
		if err := rows.Scan(&magnitude, &recordedAt, &encoded); err != nil {
			return 0, nil, fmt.Errorf("scan outbreak immunity: %w", err)
		}
		total += immunityRelevance(dimension) * decayedVectorContribution(magnitude, 1, recordedAt, now)
		refs = append(refs, decodeStringList(encoded)...)
	}
	return clampFinite01(total), dedupeStrings(refs), rows.Err()
}

func immunityRelevance(corruptionDimension string) float64 {
	if corruptionDimension == CorruptionContradiction {
		return 1
	}
	return 1 / float64(nodeProjectionRetryLimit())
}

func decayedVectorContribution(magnitude, factor float64, recordedAt, now int64) float64 {
	return clampFinite01(magnitude) * clampFinite01(factor) * vectorDecayWeight(recordedAt, now)
}

func vectorDecayWeight(recordedAt, now int64) float64 {
	if recordedAt <= 0 || now <= recordedAt {
		return 1
	}
	retention := activationHistoryRetentionBuckets(defaultEcologyPolicy())
	if retention <= 0 {
		return 1
	}
	age := activationBucket(now) - activationBucket(recordedAt)
	if age <= 0 {
		return 1
	}
	return clampFinite01(1 / (1 + float64(age)/float64(retention)))
}

func (m *MemoryForest) clusterHasBridgeRisk(ctx context.Context, clusterID string) (bool, error) {
	var count int
	if err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_bridge_nodes
		WHERE source_cluster_id = ? OR target_cluster_id = ?
	`, clusterID, clusterID).Scan(&count); err != nil {
		return false, fmt.Errorf("count cluster bridge risk: %w", err)
	}
	return count > 0, nil
}

func (m *MemoryForest) upsertOutbreak(ctx context.Context, candidate outbreakCandidate) (OutbreakMaintenanceResult, error) {
	status := OutbreakStatusProposed
	if outbreakResolved(candidate) {
		status = OutbreakStatusSuperseded
	}
	if !outbreakHasEvidence(candidate) && status != OutbreakStatusSuperseded {
		return OutbreakMaintenanceResult{}, nil
	}
	proposal := claimProposalForOutbreak(candidate)
	alreadyProposed, err := m.outbreakProposalAlreadyOpen(ctx, candidate, proposal)
	if err != nil {
		return OutbreakMaintenanceResult{}, err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return OutbreakMaintenanceResult{}, fmt.Errorf("begin outbreak tx: %w", err)
	}
	defer tx.Rollback()
	outbreakID := stableID("outbreak", candidate.ClusterID, candidate.Dimension, antigenicPolicyVersion)
	if err := upsertOutbreakTx(ctx, tx, outbreakID, status, proposal, candidate); err != nil {
		return OutbreakMaintenanceResult{}, err
	}
	if status == OutbreakStatusProposed {
		if err := upsertOutbreakProposalArtifactTx(ctx, tx, outbreakID, proposal, candidate); err != nil {
			return OutbreakMaintenanceResult{}, err
		}
		governanceProposal := outbreakGovernanceProposal(outbreakID, proposal, candidate)
		if _, err := insertGovernanceProposalTx(ctx, tx, governanceProposal); err != nil {
			return OutbreakMaintenanceResult{}, err
		}
		if err := upsertGovernanceProposalArtifactTx(ctx, tx, governanceProposal); err != nil {
			return OutbreakMaintenanceResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return OutbreakMaintenanceResult{}, fmt.Errorf("commit outbreak tx: %w", err)
	}
	emitted := false
	if status == OutbreakStatusProposed {
		var err error
		emitted, err = m.emitOutbreakGovernanceClaim(ctx, outbreakID, proposal)
		if err != nil {
			return OutbreakMaintenanceResult{}, err
		}
	}
	if status == OutbreakStatusProposed && proposal.GuardianReviewRequired {
		if err := m.emitGuardianOutbreakReview(ctx, proposal); err != nil {
			return OutbreakMaintenanceResult{}, err
		}
	}
	if status == OutbreakStatusSuperseded {
		return OutbreakMaintenanceResult{OutbreaksUpdated: 1, Superseded: 1}, nil
	}
	if alreadyProposed && !emitted {
		return OutbreakMaintenanceResult{OutbreaksUpdated: 1}, nil
	}
	return OutbreakMaintenanceResult{OutbreaksUpdated: 1, ProposalsEmitted: 1}, nil
}

func outbreakGovernanceProposal(outbreakID string, proposal ForestClaimProposal, candidate outbreakCandidate) ForestGovernanceProposal {
	now := time.Now().UTC()
	return ForestGovernanceProposal{
		ProposalID:             governedProposalID(ForestProposalKindRemediation, "forest_outbreak", outbreakID, proposal.EvidenceRefs),
		Kind:                   ForestProposalKindRemediation,
		SubjectType:            "forest_outbreak",
		SubjectID:              outbreakID,
		Status:                 ForestProposalStatusProposed,
		ClaimID:                proposal.ID,
		ArtifactID:             stableID("forest_governance_artifact", ForestProposalKindRemediation, outbreakID),
		EvidenceRefs:           dedupeStrings(proposal.EvidenceRefs),
		RollbackPath:           "close_remediation_without_runtime_state_change",
		GuardianReviewRequired: proposal.GuardianReviewRequired,
		Metadata: map[string]any{
			"summary":               proposal.Summary,
			"cluster_id":            candidate.ClusterID,
			"dimension":             candidate.Dimension,
			"counter_evidence_refs": dedupeStrings(proposal.CounterEvidenceRefs),
			"outbreak_id":           outbreakID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (m *MemoryForest) emitOutbreakGovernanceClaim(ctx context.Context, outbreakID string, proposal ForestClaimProposal) (bool, error) {
	if m.proposalSink == nil {
		return false, nil
	}
	proposalID := governedProposalID(ForestProposalKindRemediation, "forest_outbreak", outbreakID, proposal.EvidenceRefs)
	emitted, err := governanceProposalClaimAlreadyEmitted(ctx, m.db, proposalID)
	if err != nil {
		return false, err
	}
	if emitted {
		return false, nil
	}
	if err := m.proposalSink.ProposeClaim(ctx, proposal); err != nil {
		_ = markGovernanceProposalClaimEmissionError(ctx, m.db, proposalID, err)
		return false, fmt.Errorf("emit remediation proposal: %w", err)
	}
	if err := markGovernanceProposalClaimEmitted(ctx, m.db, proposalID); err != nil {
		return false, err
	}
	return true, nil
}

func (m *MemoryForest) emitGuardianOutbreakReview(ctx context.Context, proposal ForestClaimProposal) error {
	_, err := m.AppendLedgerRecord(ctx, LedgerRecord{
		SourceKind:  LedgerSourceMaintenance,
		SourceID:    proposal.ID,
		SourceKey:   "guardian_outbreak_review:" + proposal.ID,
		EventKind:   "guardian.review_required",
		SessionID:   "global",
		SubjectType: "forest_claim_proposal",
		SubjectID:   proposal.ID,
		OccurredAt:  time.Now().UTC(),
		Payload: map[string]any{
			"proposal_id":   proposal.ID,
			"cluster_id":    proposal.ClusterID,
			"dimension":     proposal.Dimension,
			"outbreak_id":   proposal.SourceOutbreakID,
			"evidence_refs": proposal.EvidenceRefs,
		},
	})
	if err != nil {
		return fmt.Errorf("emit guardian outbreak review: %w", err)
	}
	return nil
}

func (m *MemoryForest) outbreakProposalAlreadyOpen(ctx context.Context, candidate outbreakCandidate, proposal ForestClaimProposal) (bool, error) {
	if proposal.ID == "" {
		return false, nil
	}
	var count int
	if err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_outbreaks
		WHERE cluster_id = ? AND dimension = ? AND policy_version = ?
		  AND status = ? AND proposed_claim_id = ?
	`, candidate.ClusterID, candidate.Dimension, antigenicPolicyVersion, OutbreakStatusProposed, proposal.ID).Scan(&count); err != nil {
		return false, fmt.Errorf("check existing outbreak proposal: %w", err)
	}
	return count > 0, nil
}

func outbreakResolved(candidate outbreakCandidate) bool {
	return candidate.Immunity >= clampFinite01(candidate.Corruption)
}

func outbreakHasEvidence(candidate outbreakCandidate) bool {
	return candidate.EvidenceCount >= nodeProjectionRetryLimit()
}

func claimProposalForOutbreak(candidate outbreakCandidate) ForestClaimProposal {
	id := "forest_remediation:" + stableID(candidate.ClusterID, candidate.Dimension, encodeStringList(candidate.EvidenceRefs))
	return ForestClaimProposal{
		ID:                     id,
		ClusterID:              candidate.ClusterID,
		Dimension:              candidate.Dimension,
		Summary:                "Remediate forest outbreak " + candidate.Dimension + " in cluster " + candidate.ClusterID,
		EvidenceRefs:           dedupeStrings(candidate.EvidenceRefs),
		CounterEvidenceRefs:    dedupeStrings(candidate.CounterEvidenceRefs),
		GuardianReviewRequired: candidate.BridgeSevere,
		SourceOutbreakID:       stableID("outbreak", candidate.ClusterID, candidate.Dimension, antigenicPolicyVersion),
	}
}

func upsertOutbreakTx(ctx context.Context, tx *sql.Tx, outbreakID, status string, proposal ForestClaimProposal, candidate outbreakCandidate) error {
	now := time.Now().UTC().Unix()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_outbreaks
			(outbreak_id, cluster_id, dimension, growth_rate, evidence_refs, counter_evidence_refs,
			 status, proposed_claim_id, guardian_review_required, source_seq, policy_version,
			 first_seen_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cluster_id, dimension, policy_version) DO UPDATE SET
			growth_rate = excluded.growth_rate,
			evidence_refs = excluded.evidence_refs,
			counter_evidence_refs = excluded.counter_evidence_refs,
			status = excluded.status,
			proposed_claim_id = excluded.proposed_claim_id,
			guardian_review_required = excluded.guardian_review_required,
			source_seq = excluded.source_seq,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata
	`, outbreakID, candidate.ClusterID, candidate.Dimension, outbreakGrowthRate(candidate), encodeStringList(candidate.EvidenceRefs),
		encodeStringList(candidate.CounterEvidenceRefs), status, proposal.ID, boolInt(proposal.GuardianReviewRequired),
		candidate.SourceSeq, antigenicPolicyVersion, now, now, marshalJSON(outbreakMetadata(candidate, status)))
	if err != nil {
		return fmt.Errorf("upsert outbreak: %w", err)
	}
	return nil
}

func outbreakMetadata(candidate outbreakCandidate, status string) map[string]any {
	metadata := map[string]any{
		"candidate": candidate,
		"reason":    outbreakStatusReason(candidate, status),
	}
	return metadata
}

func outbreakStatusReason(candidate outbreakCandidate, status string) string {
	switch status {
	case OutbreakStatusSuperseded:
		return "immunity_recovered_before_dispatch"
	case OutbreakStatusProposed:
		if candidate.BridgeSevere {
			return "bridge_or_cross_cluster_outbreak_requires_guardian_review"
		}
		return "corruption_exceeds_immunity_threshold"
	default:
		return "outbreak_state_updated"
	}
}

func outbreakGrowthRate(candidate outbreakCandidate) float64 {
	return clampFinite01((candidate.Corruption - candidate.Immunity) / float64(candidate.EvidenceCount+len(defaultEcologyPolicy().Channels)))
}

func upsertOutbreakProposalArtifactTx(ctx context.Context, tx *sql.Tx, outbreakID string, proposal ForestClaimProposal, candidate outbreakCandidate) error {
	now := time.Now().UTC().Unix()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_artifacts
			(artifact_id, claim_id, generator_participant, artifact_name, artifact_kind,
			 data_type, content_ref, status, validation_status, last_sequence,
			 first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(artifact_id) DO UPDATE SET
			last_seen_at = excluded.last_seen_at,
			metadata = excluded.metadata
	`, proposal.ID, proposal.ID, "memory_forest", "Forest remediation proposal", "remediation_claim_proposal",
		"claim_proposal", "forest_outbreak:"+outbreakID, "proposed", "guardian_review_required",
		candidate.SourceSeq, now, now, stableID("proposal_payload", proposal.ID), marshalJSON(proposal))
	if err != nil {
		return fmt.Errorf("upsert outbreak proposal artifact: %w", err)
	}
	return nil
}

func encodeStringList(values []string) string {
	values = dedupeStrings(values)
	sort.Strings(values)
	return strings.Join(values, ",")
}
