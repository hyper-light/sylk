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
	case *CanonicalDelta:
		if delta == nil {
			return GraphNode{}
		}
		return resolveCanonicalGraphNode(board, *delta)
	case CanonicalDelta:
		return resolveCanonicalGraphNode(board, delta)
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

func resolveCanonicalGraphNode(board *ClaimsBoard, delta CanonicalDelta) GraphNode {
	if _, ok := DeltaActionClaimLifecycleStatus(delta.Action); ok {
		return resolveClaimNode(board, delta.ClaimID())
	}
	if _, ok := DeltaActionTestamentLifecycleStatus(delta.Action); ok {
		return resolveTestamentNode(board, delta.TestamentID())
	}
	switch delta.Action {
	case DeltaActionValidationEvaluated:
		return resolveValidationNode(board, delta.ClaimID(), delta.ValidationID())
	default:
		return GraphNode{}
	}
}

func resolveClaimNode(board *ClaimsBoard, claimID string) GraphNode {
	c, ok := board.CloneClaim(claimID)
	if !ok {
		return GraphNode{}
	}
	edges := edgesFromRelations(c.Relations)
	for _, t := range board.TestamentsByClaim(c.ID) {
		if t == nil || strings.TrimSpace(t.ID) == "" {
			continue
		}
		edges = appendGraphEdge(edges, GraphEdge{
			TargetID:     t.ID,
			TargetType:   RelatedTypeTestament,
			Relationship: RelationshipTestament,
		})
	}
	return GraphNode{
		Claim: c,
		Edges: edges,
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
	for _, artifact := range t.Artifacts {
		if artifact == nil || strings.TrimSpace(artifact.ID) == "" {
			continue
		}
		node.Edges = appendGraphEdge(node.Edges, GraphEdge{
			TargetID:     artifact.ID,
			TargetType:   RelatedTypeArtifact,
			Relationship: RelationshipTestament,
		})
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

func resolveArtifactNode(board *ClaimsBoard, artifactID string) GraphNode {
	a, ok := board.CloneArtifact(artifactID)
	if !ok {
		return GraphNode{}
	}
	edges := edgesFromRelations(a.Relations)
	if testamentID := strings.TrimSpace(a.TestamentID); testamentID != "" {
		edges = appendGraphEdge(edges, GraphEdge{
			TargetID:     testamentID,
			TargetType:   RelatedTypeTestament,
			Relationship: RelationshipTestament,
		})
	}
	return GraphNode{Artifact: a, Edges: edges}
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

func appendGraphEdge(edges []GraphEdge, edge GraphEdge) []GraphEdge {
	edge.TargetID = strings.TrimSpace(edge.TargetID)
	edge.TargetType = strings.TrimSpace(edge.TargetType)
	edge.Relationship = strings.TrimSpace(edge.Relationship)
	if edge.TargetID == "" || edge.TargetType == "" || edge.Relationship == "" {
		return edges
	}
	for _, existing := range edges {
		if strings.TrimSpace(existing.TargetID) == edge.TargetID &&
			strings.TrimSpace(existing.TargetType) == edge.TargetType &&
			strings.TrimSpace(existing.Relationship) == edge.Relationship {
			return edges
		}
	}
	return append(edges, edge)
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
		return resolveClaimNode(board, c.ID)
	}
	if a, ok := board.CloneAction(id); ok {
		edges := edgesFromRelations(a.Relations)
		for _, objectID := range board.ObjectIDsWithRelation(RelatedTypeAction, RelationshipClaimAction, a.ID) {
			if _, ok := board.CloneClaim(objectID); ok {
				edges = appendGraphEdge(edges, GraphEdge{
					TargetID:     objectID,
					TargetType:   RelatedTypeClaim,
					Relationship: RelationshipClaimAction,
				})
			}
		}
		for _, objectID := range board.ObjectIDsWithRelation(RelatedTypeAction, RelationshipTestamentAction, a.ID) {
			if _, ok := board.CloneTestament(objectID); ok {
				edges = appendGraphEdge(edges, GraphEdge{
					TargetID:     objectID,
					TargetType:   RelatedTypeTestament,
					Relationship: RelationshipTestamentAction,
				})
			}
		}
		return GraphNode{Action: a, Edges: edges}
	}
	if t, ok := board.CloneTestament(id); ok {
		return resolveTestamentNode(board, t.ID)
	}
	if a, ok := board.CloneArtifact(id); ok {
		return resolveArtifactNode(board, a.ID)
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
