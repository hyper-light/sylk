package query

import (
	"encoding/json"
	"fmt"
)

// =============================================================================
// Direction Types
// =============================================================================

// Direction represents the direction of edge traversal in a graph pattern.
type Direction int

const (
	DirectionOutgoing Direction = 0
	DirectionIncoming Direction = 1
	DirectionBoth     Direction = 2
)

// ValidDirections returns all valid Direction values.
func ValidDirections() []Direction {
	return []Direction{
		DirectionOutgoing,
		DirectionIncoming,
		DirectionBoth,
	}
}

// IsValid returns true if the direction is a recognized value.
func (d Direction) IsValid() bool {
	for _, valid := range ValidDirections() {
		if d == valid {
			return true
		}
	}
	return false
}

func (d Direction) String() string {
	switch d {
	case DirectionOutgoing:
		return "outgoing"
	case DirectionIncoming:
		return "incoming"
	case DirectionBoth:
		return "both"
	default:
		return fmt.Sprintf("direction(%d)", d)
	}
}

func ParseDirection(value string) (Direction, bool) {
	switch value {
	case "outgoing":
		return DirectionOutgoing, true
	case "incoming":
		return DirectionIncoming, true
	case "both":
		return DirectionBoth, true
	default:
		return Direction(0), false
	}
}

func (d Direction) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Direction) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseDirection(asString); ok {
			*d = parsed
			return nil
		}
		return fmt.Errorf("invalid direction: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*d = Direction(asInt)
		return nil
	}

	return fmt.Errorf("invalid direction")
}

// =============================================================================
// Node Matcher Structure
// =============================================================================

// NodeMatcher defines constraints for matching nodes in a graph pattern.
type NodeMatcher struct {
	// EntityType constraint (nil matches any type)
	EntityType *string `json:"entity_type,omitempty"`

	// NamePattern for matching node names (supports wildcards)
	NamePattern string `json:"name_pattern,omitempty"`

	// Properties that must match
	Properties map[string]any `json:"properties,omitempty"`
}

// Matches returns true if the matcher has any constraints.
func (nm *NodeMatcher) Matches() bool {
	return nm.EntityType != nil || nm.NamePattern != "" || len(nm.Properties) > 0
}

// =============================================================================
// Traversal Step Structure
// =============================================================================

// TraversalStep represents a single step in a graph traversal pattern.
type TraversalStep struct {
	// EdgeType to traverse (empty matches any edge type)
	EdgeType string `json:"edge_type,omitempty"`

	// Direction of traversal
	Direction Direction `json:"direction"`

	// TargetMatcher defines constraints for the target node
	TargetMatcher *NodeMatcher `json:"target_matcher,omitempty"`

	// MaxHops defines the maximum number of hops for this step (0 = exact, -1 = unlimited)
	MaxHops int `json:"max_hops"`
}

// Validate checks if the traversal step is well-formed.
func (ts *TraversalStep) Validate() error {
	if !ts.Direction.IsValid() {
		return fmt.Errorf("invalid direction: %v", ts.Direction)
	}

	if ts.MaxHops < -1 {
		return fmt.Errorf("max_hops must be -1 (unlimited) or >= 0")
	}

	return nil
}

// =============================================================================
// Graph Pattern Structure
// =============================================================================

// GraphPattern represents a multi-hop graph traversal pattern for queries.
type GraphPattern struct {
	// StartNode defines constraints for the starting node(s)
	StartNode *NodeMatcher `json:"start_node,omitempty"`

	// Traversals defines the sequence of traversal steps
	Traversals []TraversalStep `json:"traversals,omitempty"`
}

// Validate checks if the graph pattern is well-formed.
func (gp *GraphPattern) Validate() error {
	for i, step := range gp.Traversals {
		if err := step.Validate(); err != nil {
			return fmt.Errorf("traversal step %d: %w", i, err)
		}
	}
	return nil
}

// IsEmpty returns true if the pattern has no constraints or traversals.
func (gp *GraphPattern) IsEmpty() bool {
	hasStart := gp.StartNode != nil && gp.StartNode.Matches()
	return !hasStart && len(gp.Traversals) == 0
}

// TraversalCount returns the number of traversal steps.
func (gp *GraphPattern) TraversalCount() int {
	return len(gp.Traversals)
}
