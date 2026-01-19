package inference

import (
	"fmt"
	"strings"
	"time"
)

// =============================================================================
// InferenceResult Type (IE.1.3)
// =============================================================================

// DerivedEdge represents an edge that was inferred by a rule.
type DerivedEdge struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	EdgeType string `json:"edge_type"`
}

// EvidenceEdge represents an edge from the graph that supported the inference.
type EvidenceEdge struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	EdgeType string `json:"edge_type"`
}

// InferenceResult tracks a derived edge along with its provenance.
// It captures which rule was applied, what evidence supported it,
// when it was derived, and how confident we are in the result.
type InferenceResult struct {
	DerivedEdge DerivedEdge    `json:"derived_edge"`
	RuleID      string         `json:"rule_id"`
	Evidence    []EvidenceEdge `json:"evidence"`
	DerivedAt   time.Time      `json:"derived_at"`
	Confidence  float64        `json:"confidence"`
	Provenance  string         `json:"provenance"`
}

// NewInferenceResult creates a new inference result with current timestamp.
func NewInferenceResult(derived DerivedEdge, ruleID string, evidence []EvidenceEdge, confidence float64) InferenceResult {
	return InferenceResult{
		DerivedEdge: derived,
		RuleID:      ruleID,
		Evidence:    evidence,
		DerivedAt:   time.Now(),
		Confidence:  confidence,
		Provenance:  "", // Will be set by GenerateProvenance
	}
}

// GenerateProvenance creates a human-readable explanation of the inference.
// Format: "Derived {edge} via rule {ruleName} from evidence: {edges}"
func (ir *InferenceResult) GenerateProvenance(ruleName string) {
	var b strings.Builder
	b.WriteString("Derived ")
	b.WriteString(formatEdge(ir.DerivedEdge))
	b.WriteString(" via rule ")
	b.WriteString(ruleName)
	b.WriteString(" from evidence: ")

	for i, ev := range ir.Evidence {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(formatEvidenceEdge(ev))
	}

	ir.Provenance = b.String()
}

// Validate checks if the inference result is valid.
func (ir InferenceResult) Validate() error {
	if err := ir.validateDerivedEdge(); err != nil {
		return err
	}
	if err := ir.validateMetadata(); err != nil {
		return err
	}
	return nil
}

// validateDerivedEdge checks the derived edge fields are valid.
func (ir InferenceResult) validateDerivedEdge() error {
	if ir.DerivedEdge.SourceID == "" {
		return fmt.Errorf("derived edge source_id cannot be empty")
	}
	if ir.DerivedEdge.TargetID == "" {
		return fmt.Errorf("derived edge target_id cannot be empty")
	}
	if ir.DerivedEdge.EdgeType == "" {
		return fmt.Errorf("derived edge edge_type cannot be empty")
	}
	return nil
}

// validateMetadata checks rule ID, evidence, and confidence.
func (ir InferenceResult) validateMetadata() error {
	if ir.RuleID == "" {
		return fmt.Errorf("rule_id cannot be empty")
	}
	if len(ir.Evidence) == 0 {
		return fmt.Errorf("evidence cannot be empty")
	}
	return ir.validateConfidence()
}

// validateConfidence checks confidence is between 0 and 1.
func (ir InferenceResult) validateConfidence() error {
	if ir.Confidence < 0 || ir.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1, got %f", ir.Confidence)
	}
	return nil
}

// formatEdge formats a derived edge for display.
func formatEdge(e DerivedEdge) string {
	return fmt.Sprintf("%s-%s->%s", e.SourceID, e.EdgeType, e.TargetID)
}

// formatEvidenceEdge formats an evidence edge for display.
func formatEvidenceEdge(e EvidenceEdge) string {
	return fmt.Sprintf("%s-%s->%s", e.SourceID, e.EdgeType, e.TargetID)
}
