package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/skills"
)

const priorityTraverse = 80

// TraverseSkill creates the traverse LLM-callable skill that walks
// the claims graph from a starting node. Returns neighboring nodes
// along filtered edge types, up to max_depth hops.
//
// This is the single read path into the claims graph from the agent's
// turn loop. The 8 former named query skills (trace_claim_ancestry,
// list_action_claims, etc.) are reimplemented on top of Traverse.
func TraverseSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("traverse").
		Description("Walk the claims graph from a starting node. Returns neighboring nodes along filtered edge types, up to max_depth hops. This is the primary tool for exploring claim ancestry, action siblings, causal chains, scope overlaps, validation history, testament lineage, and artifacts.").
		Domain("claims").
		Keywords("traverse", "graph", "walk", "edges", "relations", "ancestry", "causality", "scope", "overlap").
		Priority(priorityTraverse).
		StringParam("node_id", "ID of the starting node (any Action, Claim, Testament, Validation, or Artifact ID)", true).
		StringParam("edge_filter", "Pipe-separated relationship types to follow (e.g. 'supersedes|amends|refines'). Empty = all edges. Use 'scope' to find claims with overlapping scope entries.", false).
		IntParam("max_depth", "Maximum traversal hops. Default 1. Use 0 for the start node only (depth-0 read).", false).
		Usage("Start from a known node ID (from a delta, a prior traversal, or a board query). Follow edges to discover ancestry (supersedes|amends|refines), causality (caused_by), siblings (claim_action), scope overlaps (scope), or testament lineage (testament). Each returned node includes its own edges so you can decide whether to traverse further.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				NodeID     string `json:"node_id"`
				EdgeFilter string `json:"edge_filter"`
				MaxDepth   int    `json:"max_depth"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			nodeID := strings.TrimSpace(params.NodeID)
			if nodeID == "" {
				return nil, fmt.Errorf("node_id is required")
			}
			board, err := bp()
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			if board == nil {
				return nil, fmt.Errorf("claims board not available (no error returned)")
			}
			maxDepth := params.MaxDepth
			if maxDepth <= 0 {
				maxDepth = 1
			}
			nodes := Traverse(board, nodeID, params.EdgeFilter, maxDepth)
			emitTraversalObserved(ctx, board, nodeID, params.EdgeFilter, maxDepth, len(nodes))
			return map[string]any{
				"start_node": nodeID,
				"nodes":      nodes,
				"count":      len(nodes),
			}, nil
		}).
		Build()
}

// emitTraversalObserved publishes an ActionTraversalObserved Fabric
// activity capturing the (start_node, edge_filter, depth, count)
// tuple. The forest fabric observer consumes these as graph-walk
// context — over time the forest can learn which traversals at
// which start kinds yield useful downstream context. Best-effort:
// activity.Append is non-blocking by design and emission failure
// must not affect the returned traversal result.
func emitTraversalObserved(ctx context.Context, board *ClaimsBoard, nodeID, edgeFilter string, depth, count int) {
	if board == nil {
		return
	}
	startKind := traversalStartKind(nodeID)
	payload, err := json.Marshal(map[string]any{
		"start_node":  nodeID,
		"start_kind":  startKind,
		"edge_filter": strings.TrimSpace(edgeFilter),
		"depth":       depth,
		"count":       count,
	})
	if err != nil {
		return
	}
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(board.SessionID()),
		Timestamp:  time.Now().UTC(),
		Resolution: activity.ResolutionFor(activity.ActionTraversalObserved),
		Action:     activity.ActionTraversalObserved,
		Subject: activity.Subject{
			TargetArtifact: nodeID,
			Coordinates: map[string]string{
				"task_id":  board.TaskID(),
				"board_id": board.BoardID(),
			},
		},
		Payload: payload,
		State:   activity.StatePoint,
	}
	activity.Append(ctx, act)
}

// traversalStartKind returns the type prefix of a graph node ID. Node
// IDs follow the convention "<kind>_<id>" (e.g. "claim_abc",
// "testament_xyz"). When a node ID lacks a recognizable prefix, the
// kind is reported as "unknown" — the forest still indexes the
// traversal but groups unknown-kind walks together.
func traversalStartKind(nodeID string) string {
	trimmed := strings.TrimSpace(nodeID)
	if trimmed == "" {
		return "unknown"
	}
	if idx := strings.Index(trimmed, "_"); idx > 0 {
		return strings.ToLower(trimmed[:idx])
	}
	return "unknown"
}
