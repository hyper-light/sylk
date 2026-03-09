package orchestrator

import (
	"context"
	"log/slog"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/container/pod"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/events"
)

// PipelineRegistrar registers an activated agent with the Guide's routing
// layer. It is called once per unique agent type per pipeline.
type PipelineRegistrar func(ctx context.Context, podID, agentType string) error

// PipelinePodConfig groups the dependencies for creating a pipeline pod.
type PipelinePodConfig struct {
	DAGID         string
	PodID         string
	SessionID     string
	Activator     guide.PodActivator
	Registrar     PipelineRegistrar
	ActivityPub   events.ActivityPublisher
	Logger        *slog.Logger
	ScribeFactory shared.ScribeFactory

	// Managed is an optional ManagedPod for pod-level lifecycle.
	// When set, guard operations delegate to ManagedPod.
	Managed *pod.ManagedPod
}

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

// NewPipelinePod creates an AgentPod configured for pipeline use.
// This is a thin wrapper for backward compatibility.
func NewPipelinePod(cfg PipelinePodConfig) *shared.AgentPod {
	podID := cfg.PodID
	if podID == "" {
		podID = cfg.DAGID
	}
	return shared.NewAgentPod(shared.AgentPodConfig{
		PodID:     podID,
		SessionID: cfg.SessionID,
		Activator: cfg.Activator,
		Managed:   cfg.Managed,
		Registrar: func(ctx context.Context, agentType string) error {
			if cfg.Registrar == nil {
				return nil
			}
			return cfg.Registrar(ctx, podID, agentType)
		},
		ActivityPub:            cfg.ActivityPub,
		RegistrationVisibility: events.VisibilitySystem,
		Logger:                 cfg.Logger,
		MemberTypes:            PipelineAgentTypes[:],
		DisplayNames:           PipelineAgentDisplayNames,
		ScribeFactory:          cfg.ScribeFactory,
	})
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
