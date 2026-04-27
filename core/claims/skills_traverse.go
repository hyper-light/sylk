package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
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
			return map[string]any{
				"start_node": nodeID,
				"nodes":      nodes,
				"count":      len(nodes),
			}, nil
		}).
		Build()
}
