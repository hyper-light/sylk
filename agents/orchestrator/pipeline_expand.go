package orchestrator

import (
	"maps"
	"time"

	"github.com/adalundhe/sylk/core/dag"
)

// PipelineStage identifies a stage within an expanded pipeline node.
type PipelineStage string

const (
	StageInspect PipelineStage = "inspect"
	StageTest    PipelineStage = "test"
	StageExecute PipelineStage = "execute"
)

// stageTimeoutFraction maps each stage to its proportional timeout share.
var stageTimeoutFraction = map[PipelineStage]float64{
	StageInspect: 0.15,
	StageTest:    0.25,
	StageExecute: 0.60,
}

// pipelineExpandable returns true if a node should be expanded into
// inspect → test → execute sub-nodes.
func pipelineExpandable(agentType string) bool {
	return agentType == "engineer" || agentType == "designer"
}

// SubNodeID returns the sub-node ID for a given parent and stage.
func SubNodeID(parentID string, stage PipelineStage) string {
	return parentID + ":" + string(stage)
}

// ParentNodeID extracts the parent node ID from a sub-node ID.
// Returns the original ID and false if not a sub-node.
func ParentNodeID(subNodeID string) (string, bool) {
	for i := len(subNodeID) - 1; i >= 0; i-- {
		if subNodeID[i] == ':' {
			suffix := subNodeID[i+1:]
			if suffix == string(StageInspect) || suffix == string(StageTest) || suffix == string(StageExecute) {
				return subNodeID[:i], true
			}
			break
		}
	}
	return subNodeID, false
}

// StageFromSubNodeID extracts the stage from a sub-node ID.
func StageFromSubNodeID(subNodeID string) PipelineStage {
	for i := len(subNodeID) - 1; i >= 0; i-- {
		if subNodeID[i] == ':' {
			return PipelineStage(subNodeID[i+1:])
		}
	}
	return ""
}

// ExpandedPipeline holds the result of expanding a single node.
type ExpandedPipeline struct {
	ParentNodeID string
	SubNodes     []dag.NodeConfig
	SubNodeMap   map[string]string // subNodeID → parentNodeID
}

// ExpandPipelineNode expands a DAG node into 3 sub-nodes with structural
// dependencies: inspect → test → execute. The execute sub-node inherits
// the parent's agent type; inspect and test use dedicated pipeline agents.
//
// Each sub-node:
//   - Inherits the parent's timeout (divided proportionally)
//   - Inherits retry policy
//   - Carries pipeline_parent_id in metadata
//   - Has explicit dependency on the prior stage
func ExpandPipelineNode(node *dag.Node) *ExpandedPipeline {
	parentID := node.ID()
	parentTimeout := node.Timeout()
	parentRetries := node.Retries()
	parentCtx := node.Context()
	parentDeps := node.Dependencies()

	inspectID := SubNodeID(parentID, StageInspect)
	testID := SubNodeID(parentID, StageTest)
	executeID := SubNodeID(parentID, StageExecute)

	subNodeMap := map[string]string{
		inspectID: parentID,
		testID:    parentID,
		executeID: parentID,
	}

	buildMeta := func(stage PipelineStage) map[string]any {
		return map[string]any{
			"pipeline_parent_id": parentID,
			"pipeline_stage":     string(stage),
		}
	}

	buildCtx := func(stage PipelineStage) map[string]any {
		c := make(map[string]any, len(parentCtx)+2)
		maps.Copy(c, parentCtx)
		c["pipeline_parent_id"] = parentID
		c["pipeline_stage"] = string(stage)
		return c
	}

	stageTimeout := func(stage PipelineStage) time.Duration {
		if parentTimeout <= 0 {
			return 0
		}
		return time.Duration(float64(parentTimeout) * stageTimeoutFraction[stage])
	}

	// Inspect inherits the parent's external dependencies
	inspectDeps := make([]string, len(parentDeps))
	copy(inspectDeps, parentDeps)

	subNodes := []dag.NodeConfig{
		{
			ID:           inspectID,
			AgentType:    "inspector-pipeline",
			Prompt:       node.Prompt(),
			Context:      buildCtx(StageInspect),
			Dependencies: inspectDeps,
			Timeout:      stageTimeout(StageInspect),
			Retries:      parentRetries,
			Priority:     node.Priority(),
			Metadata:     buildMeta(StageInspect),
		},
		{
			ID:           testID,
			AgentType:    "tester-pipeline",
			Prompt:       node.Prompt(),
			Context:      buildCtx(StageTest),
			Dependencies: []string{inspectID},
			Timeout:      stageTimeout(StageTest),
			Retries:      parentRetries,
			Priority:     node.Priority(),
			Metadata:     buildMeta(StageTest),
		},
		{
			ID:           executeID,
			AgentType:    node.AgentType(),
			Prompt:       node.Prompt(),
			Context:      buildCtx(StageExecute),
			Dependencies: []string{testID},
			Timeout:      stageTimeout(StageExecute),
			Retries:      parentRetries,
			Priority:     node.Priority(),
			Metadata:     buildMeta(StageExecute),
		},
	}

	return &ExpandedPipeline{
		ParentNodeID: parentID,
		SubNodes:     subNodes,
		SubNodeMap:   subNodeMap,
	}
}

// ExpandDAG walks a DAG and expands all pipeline-eligible nodes into
// sub-DAGs. Returns a new DAG with the expanded nodes and the mapping
// of sub-node IDs to parent node IDs. Non-pipeline nodes are copied unchanged.
func ExpandDAG(d *dag.DAG) (*dag.DAG, map[string]string) {
	subNodeMap := make(map[string]string)
	expanded := dag.NewDAG(d.Name(), d.Policy())

	// First pass: identify which nodes will be expanded.
	expandedParents := make(map[string]struct{})
	for _, node := range d.Nodes() {
		if pipelineExpandable(node.AgentType()) {
			expandedParents[node.ID()] = struct{}{}
		}
	}

	// Second pass: add nodes, replacing expanded parents with sub-nodes.
	for _, node := range d.Nodes() {
		if _, isExpanded := expandedParents[node.ID()]; isExpanded {
			ep := ExpandPipelineNode(node)
			for k, v := range ep.SubNodeMap {
				subNodeMap[k] = v
			}
			for _, sub := range ep.SubNodes {
				// Rewrite dependencies: if a sub-node depends on an expanded
				// parent, it should depend on that parent's :execute sub-node.
				rewrittenDeps := rewriteDependencies(sub.Dependencies, expandedParents)
				sub.Dependencies = rewrittenDeps
				expanded.AddNode(sub)
			}
			continue
		}

		// Non-expanded node: rewrite dependencies to point at :execute sub-nodes.
		cfg := nodeToConfig(node)
		cfg.Dependencies = rewriteDependencies(cfg.Dependencies, expandedParents)
		expanded.AddNode(cfg)
	}

	return expanded, subNodeMap
}

// rewriteDependencies replaces dependency IDs that point to expanded parent
// nodes with their :execute sub-node IDs.
func rewriteDependencies(deps []string, expandedParents map[string]struct{}) []string {
	result := make([]string, len(deps))
	for i, dep := range deps {
		if _, isExpanded := expandedParents[dep]; isExpanded {
			result[i] = SubNodeID(dep, StageExecute)
		} else {
			result[i] = dep
		}
	}
	return result
}

// nodeToConfig extracts a NodeConfig from an existing Node.
func nodeToConfig(node *dag.Node) dag.NodeConfig {
	return dag.NodeConfig{
		ID:                node.ID(),
		AgentType:         node.AgentType(),
		Prompt:            node.Prompt(),
		Context:           node.Context(),
		Dependencies:      node.Dependencies(),
		Timeout:           node.Timeout(),
		Retries:           node.Retries(),
		Priority:          node.Priority(),
		Metadata:          node.Metadata(),
		CoAgents:          node.CoAgents(),
		CollaborationMode: node.CollaborationMode(),
		MaxReviewRounds:   node.MaxReviewRounds(),
	}
}
