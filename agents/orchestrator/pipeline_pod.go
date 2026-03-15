package orchestrator

import (
	"context"

	"github.com/adalundhe/sylk/core/dag"
)

// PipelineRegistrar registers an activated agent with the Guide's routing
// layer. It is called once per unique agent type per pipeline.
type PipelineRegistrar func(ctx context.Context, podID, agentType string) error

// PipelineAgentTypes are the four agent types every pipeline requires.
var PipelineAgentTypes = [4]string{
	"inspector-pipeline",
	"tester-pipeline",
	"engineer",
	"designer",
}

// PipelinePanelAgentTypes are the agent rows shown for each pipeline in the UI.
// Only task-scoped worker agents belong in the pipeline section; the
// orchestrator remains a global control-plane agent.
var PipelinePanelAgentTypes = [4]string{
	"inspector-pipeline",
	"tester-pipeline",
	"engineer",
	"designer",
}

// PipelineAgentDisplayNames maps agent type strings to user-facing names.
var PipelineAgentDisplayNames = map[string]string{
	"orchestrator":       "Orchestrator",
	"engineer":           "Engineer",
	"designer":           "Designer",
	"inspector-pipeline": "Inspector",
	"tester-pipeline":    "Tester",
}

// NodeAgentTypes extracts the agent types needed by a DAG node.
// Returns the primary agent type plus co-agents.
func NodeAgentTypes(node *dag.Node) []string {
	coAgents := node.CoAgents()
	types := make([]string, 0, 1+len(coAgents))
	types = append(types, node.AgentType())
	types = append(types, coAgents...)
	return types
}

// collectAgentTypes returns deduplicated agent types required by a DAG.
func collectAgentTypes(d *dag.DAG) []string {
	seen := make(map[string]struct{})
	var types []string

	add := func(t string) {
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		types = append(types, t)
	}

	for _, node := range d.Nodes() {
		add(node.AgentType())
		for _, co := range node.CoAgents() {
			add(co)
		}
	}

	for _, t := range PipelineAgentTypes {
		add(t)
	}

	return types
}
