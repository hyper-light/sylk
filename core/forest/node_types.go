package forest

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// ForestNodeKind is the closed ontology for phase-4 emergent memory nodes.
// Additions must be explicit because projection, clustering, substrate, and
// policy validation all depend on the same vocabulary.
type ForestNodeKind string

const (
	ForestNodeClaim          ForestNodeKind = "claim"
	ForestNodeArtifact       ForestNodeKind = "artifact"
	ForestNodeValidation     ForestNodeKind = "validation"
	ForestNodeInteraction    ForestNodeKind = "interaction"
	ForestNodeContradiction  ForestNodeKind = "contradiction"
	ForestNodePolicyTrial    ForestNodeKind = "policy_trial"
	ForestNodeSkillCandidate ForestNodeKind = "skill_candidate"
	ForestNodeTestament      ForestNodeKind = "testament"
	ForestNodeContent        ForestNodeKind = "content"
	ForestNodeOutcome        ForestNodeKind = "outcome"
	ForestNodeAgent          ForestNodeKind = "agent"
	ForestNodeMaintenance    ForestNodeKind = "maintenance"
	ForestNodeRemediation    ForestNodeKind = "remediation"
)

// ForestEdgeKind is the closed relation set required by the emergent graph.
type ForestEdgeKind string

const (
	ForestEdgeCausality     ForestEdgeKind = "causality"
	ForestEdgeSupport       ForestEdgeKind = "support"
	ForestEdgeContradiction ForestEdgeKind = "contradiction"
	ForestEdgeValidation    ForestEdgeKind = "validation"
	ForestEdgeLineage       ForestEdgeKind = "lineage"
	ForestEdgeCoUse         ForestEdgeKind = "co_use"
	ForestEdgeSimilarity    ForestEdgeKind = "similarity"
	ForestEdgeRemediation   ForestEdgeKind = "remediation"
	ForestEdgeSuppression   ForestEdgeKind = "suppression"
	ForestEdgeTraversal     ForestEdgeKind = "traversal"
)

// EvidenceGrade records how strongly the forest should treat a projected fact.
type EvidenceGrade string

const (
	EvidenceGradeObserved     EvidenceGrade = "observed"
	EvidenceGradeAsserted     EvidenceGrade = "asserted"
	EvidenceGradeGenerated    EvidenceGrade = "generated"
	EvidenceGradeReference    EvidenceGrade = "reference"
	EvidenceGradeValidated    EvidenceGrade = "validated"
	EvidenceGradeContradicted EvidenceGrade = "contradicted"
	EvidenceGradeFailed       EvidenceGrade = "failed"
)

type ForestSubjectRef struct {
	Type string
	ID   string
}

type ForestNode struct {
	ID              string
	Kind            ForestNodeKind
	SourceKind      string
	SourcePartition string
	SourceKey       string
	SourceSeq       int64
	Subject         ForestSubjectRef
	SessionID       string
	TaskID          string
	Title           string
	Summary         string
	EvidenceGrade   EvidenceGrade
	Confidence      float64
	Salience        float64
	Utility         float64
	Novelty         float64
	Status          string
	PolicyVersion   string
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	PayloadHash     string
	Metadata        map[string]any
}

type ForestEdge struct {
	ID              string
	SourceNodeID    string
	TargetNodeID    string
	Kind            ForestEdgeKind
	SourceKind      string
	SourcePartition string
	SourceKey       string
	SourceSeq       int64
	Weight          float64
	EvidenceGrade   EvidenceGrade
	CreatedAt       time.Time
	Metadata        map[string]any
}

type ForestPacket struct {
	Node              ForestNode                    `json:"node"`
	ClusterIDs        []string                      `json:"cluster_ids,omitempty"`
	Edges             []ForestEdge                  `json:"edges,omitempty"`
	Paths             []ForestPath                  `json:"paths,omitempty"`
	Evidence          []ForestEvidence              `json:"evidence,omitempty"`
	CounterEvidence   []ForestEvidence              `json:"counter_evidence,omitempty"`
	Artifacts         []ArtifactEvidenceRecord      `json:"artifacts,omitempty"`
	Validations       []ValidationEvidenceRecord    `json:"validations,omitempty"`
	POIs              []ForestPOI                   `json:"pois,omitempty"`
	BridgeRisks       []ForestBridgeRisk            `json:"bridge_risks,omitempty"`
	Quarantine        ForestQuarantine              `json:"quarantine"`
	ProposedClaims    []ForestClaimProposalTemplate `json:"proposed_claims,omitempty"`
	SkippedEvidence   string                        `json:"skipped_evidence,omitempty"`
	Source            RetrievalSource               `json:"source,omitempty"`
	Score             float64                       `json:"score"`
	RiskScore         float64                       `json:"risk_score"`
	ValidationNeed    float64                       `json:"validation_need"`
	PolicyVersion     string                        `json:"policy_version,omitempty"`
	AssemblyTimestamp time.Time                     `json:"assembly_timestamp,omitempty"`
}

type ForestPath struct {
	PathID      string           `json:"path_id"`
	NodeIDs     []string         `json:"node_ids"`
	EdgeIDs     []string         `json:"edge_ids"`
	Kinds       []ForestEdgeKind `json:"kinds"`
	Weight      float64          `json:"weight"`
	Explanation string           `json:"explanation"`
}

type ForestEvidence struct {
	RefType      string        `json:"ref_type"`
	RefID        string        `json:"ref_id"`
	NodeID       string        `json:"node_id,omitempty"`
	ArtifactID   string        `json:"artifact_id,omitempty"`
	ValidationID string        `json:"validation_id,omitempty"`
	EdgeID       string        `json:"edge_id,omitempty"`
	Grade        EvidenceGrade `json:"grade"`
	Summary      string        `json:"summary"`
	Confidence   float64       `json:"confidence"`
	Counter      bool          `json:"counter"`
}

type ForestPOI struct {
	POIID                string  `json:"poi_id"`
	ClusterID            string  `json:"cluster_id"`
	NodeID               string  `json:"node_id"`
	Reason               string  `json:"reason"`
	Priority             float64 `json:"priority"`
	InvalidationSequence int64   `json:"invalidation_sequence"`
}

type ForestBridgeRisk struct {
	BridgeID               string   `json:"bridge_id"`
	NodeID                 string   `json:"node_id"`
	SourceClusterID        string   `json:"source_cluster_id"`
	TargetClusterID        string   `json:"target_cluster_id"`
	Score                  float64  `json:"score"`
	ContradictionRisk      float64  `json:"contradiction_risk"`
	GuardianReviewRequired bool     `json:"guardian_review_required"`
	SourceEdgeIDs          []string `json:"source_edge_ids,omitempty"`
}

type ForestQuarantine struct {
	Quarantined      bool     `json:"quarantined"`
	Factor           float64  `json:"factor"`
	Corruption       float64  `json:"corruption"`
	Immunity         float64  `json:"immunity"`
	Dimensions       []string `json:"dimensions,omitempty"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
	RecoveryRequired bool     `json:"recovery_required"`
}

type ForestClaimProposalTemplate struct {
	ProposalID             string   `json:"proposal_id"`
	ClusterID              string   `json:"cluster_id,omitempty"`
	Dimension              string   `json:"dimension,omitempty"`
	Summary                string   `json:"summary"`
	EvidenceRefs           []string `json:"evidence_refs,omitempty"`
	CounterEvidenceRefs    []string `json:"counter_evidence_refs,omitempty"`
	GuardianReviewRequired bool     `json:"guardian_review_required"`
}

type ForestCursor struct {
	ID               string         `json:"id"`
	SessionID        string         `json:"session_id,omitempty"`
	TaskID           string         `json:"task_id,omitempty"`
	AgentID          string         `json:"agent_id,omitempty"`
	TurnID           string         `json:"turn_id,omitempty"`
	ActiveClusterIDs []string       `json:"active_cluster_ids,omitempty"`
	ActiveNodeIDs    []string       `json:"active_node_ids,omitempty"`
	POIRefs          []string       `json:"poi_refs,omitempty"`
	BridgeRefs       []string       `json:"bridge_refs,omitempty"`
	RiskFlags        []string       `json:"risk_flags,omitempty"`
	NoCursorReason   string         `json:"no_cursor_reason,omitempty"`
	PolicyVersion    string         `json:"policy_version"`
	CreatedAt        time.Time      `json:"created_at"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

func allForestNodeKinds() map[ForestNodeKind]struct{} {
	return map[ForestNodeKind]struct{}{
		ForestNodeClaim:          {},
		ForestNodeArtifact:       {},
		ForestNodeValidation:     {},
		ForestNodeInteraction:    {},
		ForestNodeContradiction:  {},
		ForestNodePolicyTrial:    {},
		ForestNodeSkillCandidate: {},
		ForestNodeTestament:      {},
		ForestNodeContent:        {},
		ForestNodeOutcome:        {},
		ForestNodeAgent:          {},
		ForestNodeMaintenance:    {},
		ForestNodeRemediation:    {},
	}
}

func allForestEdgeKinds() map[ForestEdgeKind]struct{} {
	return map[ForestEdgeKind]struct{}{
		ForestEdgeCausality:     {},
		ForestEdgeSupport:       {},
		ForestEdgeContradiction: {},
		ForestEdgeValidation:    {},
		ForestEdgeLineage:       {},
		ForestEdgeCoUse:         {},
		ForestEdgeSimilarity:    {},
		ForestEdgeRemediation:   {},
		ForestEdgeSuppression:   {},
		ForestEdgeTraversal:     {},
	}
}

func validateForestNodeKind(kind ForestNodeKind) error {
	if _, ok := allForestNodeKinds()[kind]; ok {
		return nil
	}
	return fmt.Errorf("unsupported forest node kind %q", kind)
}

func validateForestEdgeKind(kind ForestEdgeKind) error {
	if _, ok := allForestEdgeKinds()[kind]; ok {
		return nil
	}
	return fmt.Errorf("unsupported forest edge kind %q", kind)
}

func stableForestNodeID(sourcePartition, sourceKey string, kind ForestNodeKind, subject ForestSubjectRef) string {
	return stableID(
		"forest_node",
		strings.TrimSpace(sourcePartition),
		strings.TrimSpace(sourceKey),
		string(kind),
		strings.TrimSpace(subject.Type),
		strings.TrimSpace(subject.ID),
	)
}

func stableForestReferenceNodeID(subject ForestSubjectRef) string {
	key := strings.TrimSpace(subject.Type) + ":" + strings.TrimSpace(subject.ID)
	return stableForestNodeID("reference", key, forestNodeKindForSubject(subject.Type, ""), subject)
}

func stableForestEdgeID(sourceNodeID, targetNodeID string, kind ForestEdgeKind, sourceKey string) string {
	return stableID(
		"forest_edge",
		strings.TrimSpace(sourceNodeID),
		strings.TrimSpace(targetNodeID),
		string(kind),
		strings.TrimSpace(sourceKey),
	)
}

func normalizeEvidenceGrade(grade EvidenceGrade) EvidenceGrade {
	switch grade {
	case EvidenceGradeObserved,
		EvidenceGradeAsserted,
		EvidenceGradeGenerated,
		EvidenceGradeReference,
		EvidenceGradeValidated,
		EvidenceGradeContradicted,
		EvidenceGradeFailed:
		return grade
	default:
		return EvidenceGradeObserved
	}
}

func normalizeForestNode(node ForestNode) (ForestNode, error) {
	node.Kind = ForestNodeKind(strings.TrimSpace(string(node.Kind)))
	if err := validateForestNodeKind(node.Kind); err != nil {
		return node, err
	}
	node.SourceKind = strings.TrimSpace(node.SourceKind)
	node.SourcePartition = strings.TrimSpace(node.SourcePartition)
	node.SourceKey = strings.TrimSpace(node.SourceKey)
	node.Subject.Type = strings.TrimSpace(node.Subject.Type)
	node.Subject.ID = strings.TrimSpace(node.Subject.ID)
	node.SessionID = normalizeForestSessionID(node.SessionID)
	node.TaskID = normalizeForestTaskID(node.TaskID)
	node.Title = summarizeText(firstNonEmptyString(node.Title, node.Subject.ID, string(node.Kind)), 120)
	node.Summary = summarizeText(firstNonEmptyString(node.Summary, node.Title), 512)
	node.EvidenceGrade = normalizeEvidenceGrade(node.EvidenceGrade)
	node.Confidence = clampFinite01(node.Confidence)
	node.Salience = clampFinite01(node.Salience)
	node.Utility = clampFinite01(node.Utility)
	node.Novelty = clampFinite01(node.Novelty)
	node.Status = strings.TrimSpace(node.Status)
	node.PolicyVersion = strings.TrimSpace(node.PolicyVersion)
	node.PayloadHash = strings.TrimSpace(node.PayloadHash)
	now := time.Now().UTC()
	if node.FirstSeenAt.IsZero() {
		node.FirstSeenAt = now
	}
	if node.LastSeenAt.IsZero() {
		node.LastSeenAt = node.FirstSeenAt
	}
	if node.SourcePartition == "" {
		node.SourcePartition = node.SourceKind + ":" + node.SessionID
	}
	if node.SourceKey == "" {
		return node, errors.New("forest node source_key is required")
	}
	if node.SourceKind == "" {
		return node, errors.New("forest node source_kind is required")
	}
	if node.Subject.Type == "" || node.Subject.ID == "" {
		return node, errors.New("forest node subject ref is required")
	}
	if node.ID == "" {
		node.ID = stableForestNodeID(node.SourcePartition, node.SourceKey, node.Kind, node.Subject)
	}
	return node, nil
}

func normalizeForestEdge(edge ForestEdge) (ForestEdge, error) {
	edge.Kind = ForestEdgeKind(strings.TrimSpace(string(edge.Kind)))
	if err := validateForestEdgeKind(edge.Kind); err != nil {
		return edge, err
	}
	edge.SourceNodeID = strings.TrimSpace(edge.SourceNodeID)
	edge.TargetNodeID = strings.TrimSpace(edge.TargetNodeID)
	edge.SourceKind = strings.TrimSpace(edge.SourceKind)
	edge.SourcePartition = strings.TrimSpace(edge.SourcePartition)
	edge.SourceKey = strings.TrimSpace(edge.SourceKey)
	edge.Weight = clampFinite01(edge.Weight)
	edge.EvidenceGrade = normalizeEvidenceGrade(edge.EvidenceGrade)
	if edge.CreatedAt.IsZero() {
		edge.CreatedAt = time.Now().UTC()
	}
	if edge.SourceNodeID == "" || edge.TargetNodeID == "" {
		return edge, errors.New("forest edge source and target nodes are required")
	}
	if edge.SourceKey == "" {
		return edge, errors.New("forest edge source_key is required")
	}
	if edge.SourceKind == "" {
		return edge, errors.New("forest edge source_kind is required")
	}
	if edge.ID == "" {
		edge.ID = stableForestEdgeID(edge.SourceNodeID, edge.TargetNodeID, edge.Kind, edge.SourceKey)
	}
	return edge, nil
}

func clampFinite01(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return clamp01(v)
}

func sortedForestNodeIDs(nodes []ForestNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if strings.TrimSpace(node.ID) != "" {
			ids = append(ids, node.ID)
		}
	}
	sort.Strings(ids)
	return ids
}
