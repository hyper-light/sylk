package inference

import "fmt"

// =============================================================================
// Edge Type (IE.3)
// =============================================================================

// Edge represents a simple triple (Subject-Predicate-Object) for inference.
// This is a lightweight representation used during rule evaluation.
type Edge struct {
	Source    string `json:"source"`
	Predicate string `json:"predicate"`
	Target    string `json:"target"`
}

// Key returns a unique string identifier for this edge.
// Used for deduplication in the forward chainer.
func (e Edge) Key() string {
	return fmt.Sprintf("%s|%s|%s", e.Source, e.Predicate, e.Target)
}

// String returns a human-readable representation of the edge.
func (e Edge) String() string {
	return fmt.Sprintf("%s-%s->%s", e.Source, e.Predicate, e.Target)
}

// Equals checks if two edges are identical.
func (e Edge) Equals(other Edge) bool {
	return e.Source == other.Source &&
		e.Predicate == other.Predicate &&
		e.Target == other.Target
}

// ToEvidenceEdge converts this Edge to an EvidenceEdge for result tracking.
func (e Edge) ToEvidenceEdge() EvidenceEdge {
	return EvidenceEdge{
		SourceID: e.Source,
		TargetID: e.Target,
		EdgeType: e.Predicate,
	}
}

// ToDerivedEdge converts this Edge to a DerivedEdge for result tracking.
func (e Edge) ToDerivedEdge() DerivedEdge {
	return DerivedEdge{
		SourceID: e.Source,
		TargetID: e.Target,
		EdgeType: e.Predicate,
	}
}

// NewEdge creates a new Edge with the given source, predicate, and target.
func NewEdge(source, predicate, target string) Edge {
	return Edge{
		Source:    source,
		Predicate: predicate,
		Target:    target,
	}
}

// EdgeFromEvidenceEdge creates an Edge from an EvidenceEdge.
func EdgeFromEvidenceEdge(e EvidenceEdge) Edge {
	return Edge{
		Source:    e.SourceID,
		Predicate: e.EdgeType,
		Target:    e.TargetID,
	}
}

// EdgeFromDerivedEdge creates an Edge from a DerivedEdge.
func EdgeFromDerivedEdge(e DerivedEdge) Edge {
	return Edge{
		Source:    e.SourceID,
		Predicate: e.EdgeType,
		Target:    e.TargetID,
	}
}
