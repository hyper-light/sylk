package claims

import (
	"strings"
)

// ResolveEntryPoint resolves a delta into a GraphEntryPoint by reading
// the referenced entity from the board. When the board is nil or the
// node is not found, the entry point is still created with an empty
// node — the delta itself carries enough information for the agent to
// act. Returns nil only when the delta itself is nil.
func ResolveEntryPoint(board *ClaimsBoard, d Delta, priority WorkUnitPriority, expectation *Expectation) *GraphEntryPoint {
	if d == nil {
		return nil
	}
	var node GraphNode
	if board != nil {
		node = resolveGraphNode(board, d)
	}
	return &GraphEntryPoint{
		Delta:       d,
		Node:        node,
		Priority:    priority,
		Expectation: expectation,
	}
}

// resolveGraphNode resolves a delta's referenced entity into a
// GraphNode with its immediate edges. Reads the board under RLock.
func resolveGraphNode(board *ClaimsBoard, d Delta) GraphNode {
	if board == nil || d == nil {
		return GraphNode{}
	}
	switch delta := d.(type) {
	case *InboxDelta:
		return resolveClaimNode(board, delta.ClaimID)
	case InboxDelta:
		return resolveClaimNode(board, delta.ClaimID)
	case *TestamentDelta:
		return resolveTestamentNode(board, delta.TestamentID)
	case TestamentDelta:
		return resolveTestamentNode(board, delta.TestamentID)
	case *ValidationDelta:
		return resolveValidationNode(board, delta.ClaimID, delta.ValidationID)
	case ValidationDelta:
		return resolveValidationNode(board, delta.ClaimID, delta.ValidationID)
	case *ClaimStatusDelta:
		return resolveClaimNode(board, delta.ClaimID)
	case ClaimStatusDelta:
		return resolveClaimNode(board, delta.ClaimID)
	case *PhaseDelta:
		return GraphNode{} // phase deltas reference the board, not a node
	case PhaseDelta:
		return GraphNode{}
	}
	return GraphNode{}
}

func resolveClaimNode(board *ClaimsBoard, claimID string) GraphNode {
	c, ok := board.CloneClaim(claimID)
	if !ok {
		return GraphNode{}
	}
	return GraphNode{
		Claim: c,
		Edges: edgesFromRelations(c.Relations),
	}
}

func resolveTestamentNode(board *ClaimsBoard, testamentID string) GraphNode {
	t, ok := board.CloneTestament(testamentID)
	if !ok {
		return GraphNode{}
	}
	node := GraphNode{
		Testament: t,
		Edges:     edgesFromRelations(t.Relations),
	}
	// Populate the parent claim so the agent sees its validations
	// alongside the testament artifacts. The testament's Relations
	// carry a RelationshipClaim edge to the parent claim ID.
	if claimRel := FindRelation(t.Relations, RelationshipClaim); claimRel != nil {
		if c, ok := board.CloneClaim(claimRel.Related); ok {
			node.Claim = c
		}
	}
	return node
}

func resolveValidationNode(board *ClaimsBoard, claimID, validationID string) GraphNode {
	c, ok := board.CloneClaim(claimID)
	if !ok {
		return GraphNode{}
	}
	for _, v := range c.Validations {
		if v.ID == validationID {
			clone := *v
			return GraphNode{
				Validation: &clone,
				Edges:      edgesFromRelations(c.Relations),
			}
		}
	}
	return GraphNode{
		Claim: c,
		Edges: edgesFromRelations(c.Relations),
	}
}

// edgesFromRelations converts a slice of Relation into GraphEdge
// slice. Each Relation becomes one edge.
func edgesFromRelations(relations []Relation) []GraphEdge {
	if len(relations) == 0 {
		return nil
	}
	edges := make([]GraphEdge, len(relations))
	for idx, rel := range relations {
		edges[idx] = GraphEdge{
			TargetID:     rel.Related,
			TargetType:   rel.RelatedType,
			Relationship: rel.Relationship,
		}
	}
	return edges
}

// Traverse performs breadth-first traversal from nodeID, following
// edges whose Relationship matches edgeFilter (pipe-separated, or
// empty for all edges), up to maxDepth hops. Returns the discovered
// nodes. Each returned node includes its own Edges so the agent can
// decide whether to traverse further.
//
// Cycle-safe: visited IDs are tracked and never re-queued. The start
// node is NOT included in the result (the agent already has it from
// the GraphEntryPoint).
//
// Special filter "scope" queries the board's scope index instead of
// following Relations — returns claims whose ClaimScopeEntry overlaps
// with the start claim's scope.
func Traverse(board *ClaimsBoard, nodeID string, edgeFilter string, maxDepth int) []GraphNode {
	if board == nil || strings.TrimSpace(nodeID) == "" {
		return nil
	}
	nodeID = strings.TrimSpace(nodeID)
	if maxDepth <= 0 {
		maxDepth = 1
	}

	// Scope traversal: find overlapping claims by scope entries.
	if strings.TrimSpace(edgeFilter) == "scope" {
		return traverseByScope(board, nodeID)
	}

	allowed := parseEdgeFilter(edgeFilter)
	visited := map[string]struct{}{nodeID: {}}
	frontier := []string{nodeID}
	var result []GraphNode

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var nextFrontier []string
		for _, currentID := range frontier {
			node := resolveNodeByID(board, currentID)
			edges := node.Edges
			for _, edge := range edges {
				targetID := strings.TrimSpace(edge.TargetID)
				if targetID == "" {
					continue
				}
				if _, seen := visited[targetID]; seen {
					continue
				}
				if !edgeAllowed(allowed, edge.Relationship) {
					continue
				}
				visited[targetID] = struct{}{}
				resolved := resolveNodeByID(board, targetID)
				if resolved.NodeID() != "" {
					result = append(result, resolved)
					nextFrontier = append(nextFrontier, targetID)
				}
			}
		}
		frontier = nextFrontier
	}
	return result
}

// resolveNodeByID tries to resolve an ID as a claim, action,
// testament, or artifact — in that order. Returns the first hit.
func resolveNodeByID(board *ClaimsBoard, id string) GraphNode {
	if c, ok := board.CloneClaim(id); ok {
		return GraphNode{Claim: c, Edges: edgesFromRelations(c.Relations)}
	}
	if a, ok := board.CloneAction(id); ok {
		return GraphNode{Action: a, Edges: edgesFromRelations(a.Relations)}
	}
	if t, ok := board.CloneTestament(id); ok {
		return GraphNode{Testament: t, Edges: edgesFromRelations(t.Relations)}
	}
	return GraphNode{}
}

// traverseByScope finds claims whose scope overlaps with the start
// claim's scope entries.
func traverseByScope(board *ClaimsBoard, claimID string) []GraphNode {
	c, ok := board.CloneClaim(claimID)
	if !ok {
		return nil
	}
	seen := map[string]struct{}{claimID: {}}
	var result []GraphNode
	for _, entry := range c.Scope {
		for _, overlapID := range board.ClaimIDsWithScope(entry.Kind, entry.Key) {
			if _, dup := seen[overlapID]; dup {
				continue
			}
			seen[overlapID] = struct{}{}
			node := resolveNodeByID(board, overlapID)
			if node.NodeID() != "" {
				result = append(result, node)
			}
		}
	}
	return result
}

func parseEdgeFilter(filter string) map[string]struct{} {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil // nil = all edges allowed
	}
	parts := strings.Split(filter, "|")
	allowed := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

func edgeAllowed(allowed map[string]struct{}, relationship string) bool {
	if allowed == nil {
		return true
	}
	_, ok := allowed[strings.TrimSpace(relationship)]
	return ok
}
