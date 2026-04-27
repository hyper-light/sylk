package ingestion

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	vgdb "github.com/adalundhe/sylk/core/vectorgraphdb"
)

// ClaimsIngester writes claims board entities (claims, testaments,
// artifacts) into the VectorGraphDB as typed nodes with relation-
// derived edges. Each entity gets an embedding from its description
// or summary text. Edges map claims.Relation types to graph EdgeTypes.
type ClaimsIngester struct {
	nodes *vgdb.NodeStore
	edges *vgdb.EdgeStore
}

// NewClaimsIngester creates an ingester bound to the given stores.
func NewClaimsIngester(nodes *vgdb.NodeStore, edges *vgdb.EdgeStore) *ClaimsIngester {
	return &ClaimsIngester{nodes: nodes, edges: edges}
}

// IngestClaim writes a claim as a DomainClaims/NodeTypeClaim node.
// The embedding vector should be computed from the claim description.
func (ci *ClaimsIngester) IngestClaim(c *claims.Claim, embedding []float32) error {
	if ci.nodes == nil || c == nil {
		return nil
	}
	node := &vgdb.GraphNode{
		ID:          claimsNodeID("claim", c.ID),
		Domain:      vgdb.DomainClaims,
		NodeType:    vgdb.NodeTypeClaim,
		Name:        c.Title,
		Content:     c.Description,
		ContentHash: contentHash(c.Title + c.Description),
		Confidence:  validationConfidence(c),
		Category:    string(c.ActionType),
		Timestamp:   time.Now(),
		Metadata: map[string]any{
			"claim_id":    c.ID,
			"status":      string(c.Status),
			"action_type": string(c.ActionType),
		},
	}
	return ci.nodes.InsertNode(node, embedding)
}

// IngestTestament writes a testament as a DomainClaims/NodeTypeTestament
// node and creates a Testifies edge to its parent claim.
func (ci *ClaimsIngester) IngestTestament(t *claims.Testament, claimID string, embedding []float32) error {
	if ci.nodes == nil || t == nil {
		return nil
	}
	node := &vgdb.GraphNode{
		ID:          claimsNodeID("testament", t.ID),
		Domain:      vgdb.DomainClaims,
		NodeType:    vgdb.NodeTypeTestament,
		Name:        truncate(t.Summary, 200),
		Content:     t.Summary,
		ContentHash: contentHash(t.Summary),
		Confidence:  confidenceValue(t.Confidence),
		Timestamp:   time.Now(),
		Metadata: map[string]any{
			"testament_id":   t.ID,
			"agent_id":       t.AgentID,
			"artifact_count": len(t.Artifacts),
		},
	}
	if err := ci.nodes.InsertNode(node, embedding); err != nil {
		return err
	}
	if claimID == "" || ci.edges == nil {
		return nil
	}
	return ci.edges.InsertEdge(&vgdb.GraphEdge{
		SourceID: node.ID,
		TargetID: claimsNodeID("claim", claimID),
		EdgeType: vgdb.EdgeTypeTestifies,
		Weight:   confidenceValue(t.Confidence),
	})
}

// IngestArtifact writes a semantic artifact as a DomainClaims/NodeTypeArtifact
// node and creates a Proves edge to its parent claim via the testament.
func (ci *ClaimsIngester) IngestArtifact(a *claims.Artifact, claimID string, embedding []float32) error {
	if ci.nodes == nil || a == nil || !isSemanticArtifact(a.Kind) {
		return nil
	}
	node := &vgdb.GraphNode{
		ID:          claimsNodeID("artifact", a.ID),
		Domain:      vgdb.DomainClaims,
		NodeType:    vgdb.NodeTypeArtifact,
		Name:        truncate(a.Reference, 200),
		Content:     a.Reference,
		ContentHash: contentHash(a.Kind + ":" + a.Reference),
		Category:    a.Kind,
		Timestamp:   time.Now(),
		Metadata: map[string]any{
			"artifact_id": a.ID,
			"kind":        a.Kind,
			"agent_id":    a.AgentID,
		},
	}
	if err := ci.nodes.InsertNode(node, embedding); err != nil {
		return err
	}
	if claimID == "" || ci.edges == nil {
		return nil
	}
	return ci.edges.InsertEdge(&vgdb.GraphEdge{
		SourceID: node.ID,
		TargetID: claimsNodeID("claim", claimID),
		EdgeType: vgdb.EdgeTypeProves,
		Weight:   1.0,
	})
}

// IngestRelations creates edges between claims entities based on the
// claims.Relation system. Each relation maps to a specific EdgeType.
func (ci *ClaimsIngester) IngestRelations(sourceNodeID string, relations []claims.Relation) error {
	if ci.edges == nil || sourceNodeID == "" {
		return nil
	}
	for _, r := range relations {
		edgeType, ok := relationToEdgeType(r.Relationship)
		if !ok {
			continue
		}
		targetID := claimsNodeID(r.RelatedType, r.Related)
		edge := &vgdb.GraphEdge{
			SourceID: sourceNodeID,
			TargetID: targetID,
			EdgeType: edgeType,
			Weight:   1.0,
		}
		if err := ci.edges.InsertEdge(edge); err != nil {
			// Best-effort: target node may not exist yet (out-of-order
			// ingestion). Log and continue rather than failing the batch.
			slog.Debug("claims_ingester_relation_skip",
				"source", sourceNodeID, "target", targetID,
				"edge_type", edgeType.String(), "error", err.Error())
			continue
		}
	}
	return nil
}

func relationToEdgeType(relationship string) (vgdb.EdgeType, bool) {
	switch relationship {
	case claims.RelationshipSupersedes:
		return vgdb.EdgeTypeSupersedes, true
	case claims.RelationshipDependsOn:
		return vgdb.EdgeTypeDependsOn, true
	case claims.RelationshipCausedBy:
		return vgdb.EdgeTypeCausedBy, true
	case claims.RelationshipRefines:
		return vgdb.EdgeTypeRefines, true
	case claims.RelationshipConflictsWith:
		return vgdb.EdgeTypeConflictsWith, true
	default:
		return 0, false
	}
}

func claimsNodeID(prefix, id string) string {
	return "claims:" + prefix + ":" + id
}

func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:16])
}

func validationConfidence(c *claims.Claim) float64 {
	switch c.Status {
	case claims.ClaimStatusAccepted:
		return 1.0
	case claims.ClaimStatusRejected:
		return 0.1
	case claims.ClaimStatusTestified:
		return 0.7
	default:
		return 0.5
	}
}

func confidenceValue(confidence string) float64 {
	switch confidence {
	case "consensus":
		return 0.95
	case "committed", "high":
		return 0.9
	case "tentative", "medium":
		return 0.7
	case "hint", "low":
		return 0.4
	default:
		return 0.5
	}
}

// isSemanticArtifact returns true for artifact kinds that carry
// meaningful content worth embedding. Structural artifacts (receipts,
// tokens) are stored as metadata on the testament node instead.
func isSemanticArtifact(kind string) bool {
	switch kind {
	case "code", "diff", "research", "design", "diagnosis", "test_output",
		"error", "error_trace", "error_diagnostic", "vfs_commit":
		return true
	default:
		return false
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "\u2026"
}
