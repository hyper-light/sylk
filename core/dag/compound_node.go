package dag

import (
	"fmt"
	"time"
)

// CollaborationMode defines how co-tenant agents interact within a compound node.
type CollaborationMode int

const (
	// CollaborationSequential means the primary agent acts first, then co-agents
	// review without pushback.
	CollaborationSequential CollaborationMode = iota
	// CollaborationAdversarial means co-agents can push back on the primary
	// agent's implementation, triggering bounded revision rounds.
	CollaborationAdversarial
)

// String returns the string representation of a CollaborationMode.
func (m CollaborationMode) String() string {
	switch m {
	case CollaborationSequential:
		return "sequential"
	case CollaborationAdversarial:
		return "adversarial"
	default:
		return "unknown"
	}
}

// CompoundNodeResult holds the result of a compound node execution,
// including the primary result, co-agent results, and collaboration metadata.
type CompoundNodeResult struct {
	PrimaryResult    *NodeResult            `json:"primary_result"`
	CoResults        map[string]*NodeResult `json:"co_results,omitempty"`
	Consensus        bool                   `json:"consensus"`
	Disputes         []Dispute              `json:"disputes,omitempty"`
	ReviewRoundsUsed int                    `json:"review_rounds_used"`
}

// Dispute records a disagreement between the primary agent and a co-agent
// during adversarial collaboration.
type Dispute struct {
	Challenger    string `json:"challenger"`
	Subject       string `json:"subject"`
	ChallengerArg string `json:"challenger_arg"`
	DefenderArg   string `json:"defender_arg"`
	Resolution    string `json:"resolution"`
	Round         int    `json:"round"`
}

// IsCompoundNode returns true if the node has co-tenant agents.
func IsCompoundNode(cfg *NodeConfig) bool {
	return len(cfg.CoAgents) > 0
}

// CoAgentTypes returns a copy of the node's co-agent type list.
func CoAgentTypes(cfg *NodeConfig) []string {
	if len(cfg.CoAgents) == 0 {
		return nil
	}
	result := make([]string, len(cfg.CoAgents))
	copy(result, cfg.CoAgents)
	return result
}

// ValidateCompoundNode checks that a compound node configuration is consistent.
func ValidateCompoundNode(cfg *NodeConfig) error {
	if len(cfg.CoAgents) == 0 {
		return nil // Not a compound node — nothing to validate
	}
	// No self-referential co-agents
	for _, co := range cfg.CoAgents {
		if co == cfg.AgentType {
			return fmt.Errorf("compound node %q: agent type %q cannot be its own co-agent", cfg.ID, cfg.AgentType)
		}
	}
	// Adversarial mode requires MaxReviewRounds > 0
	if cfg.CollaborationMode == CollaborationAdversarial && cfg.MaxReviewRounds <= 0 {
		return fmt.Errorf("compound node %q: adversarial mode requires MaxReviewRounds > 0", cfg.ID)
	}
	return nil
}

// ToNodeResult converts a CompoundNodeResult to a standard NodeResult
// for DAG state tracking. The primary result is used as the base.
func (cr *CompoundNodeResult) ToNodeResult() *NodeResult {
	if cr.PrimaryResult == nil {
		return &NodeResult{
			State:   NodeStateFailed,
			Error:   fmt.Errorf("compound node: no primary result"),
			EndTime: time.Now(),
		}
	}
	result := *cr.PrimaryResult
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	result.Metadata["consensus"] = cr.Consensus
	result.Metadata["review_rounds_used"] = cr.ReviewRoundsUsed
	if len(cr.Disputes) > 0 {
		result.Metadata["disputes_count"] = len(cr.Disputes)
	}
	return &result
}
