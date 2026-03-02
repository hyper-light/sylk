package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/dag"
)

// PipelineRegistrar registers an activated agent with the Guide's routing
// layer (RegisterRouter + MarkAgentReady). It is called once per agent type
// during pod activation.
type PipelineRegistrar func(ctx context.Context, agentType string) error

// PipelinePodConfig groups the dependencies for a PipelinePod.
type PipelinePodConfig struct {
	DAGID     string
	Activator guide.AgentActivator
	Registrar PipelineRegistrar
	Logger    *slog.Logger
}

// PipelinePod owns the lifecycle of all agents required by a single DAG
// pipeline. It activates every agent, acquires demotion guards to keep
// them hot for the pipeline's duration, and registers them with the Guide.
// Release must be called when the pipeline terminates.
type PipelinePod struct {
	dagID     string
	activator guide.AgentActivator
	registrar PipelineRegistrar
	logger    *slog.Logger

	mu       sync.Mutex
	guards   []func()
	released bool
}

// NewPipelinePod creates a pod for the given DAG.
func NewPipelinePod(cfg PipelinePodConfig) *PipelinePod {
	return &PipelinePod{
		dagID:     cfg.DAGID,
		activator: cfg.Activator,
		registrar: cfg.Registrar,
		logger:    cfg.Logger,
	}
}

// Activate brings every agent type required by the DAG to TierHot,
// acquires demotion guards, and registers each with the Guide. On any
// failure, all previously acquired guards are released and the error
// is returned.
func (p *PipelinePod) Activate(ctx context.Context, d *dag.DAG) error {
	types := collectAgentTypes(d)

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, agentType := range types {
		release, err := p.activator.HoldActive(ctx, agentType)
		if err != nil {
			p.releaseGuardsLocked()
			return fmt.Errorf("pipeline pod %s: hold %s: %w", p.dagID, agentType, err)
		}
		p.guards = append(p.guards, release)

		if p.registrar != nil {
			if err := p.registrar(ctx, agentType); err != nil {
				p.releaseGuardsLocked()
				return fmt.Errorf("pipeline pod %s: register %s: %w", p.dagID, agentType, err)
			}
		}

		if p.logger != nil {
			p.logger.Info("pipeline pod: agent activated",
				"dag_id", p.dagID,
				"agent_type", agentType,
			)
		}
	}
	return nil
}

// Release releases all demotion guards, allowing normal idle demotion
// to resume. Safe for concurrent and repeated calls.
func (p *PipelinePod) Release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.releaseGuardsLocked()
}

// releaseGuardsLocked releases all guards. Caller must hold p.mu.
func (p *PipelinePod) releaseGuardsLocked() {
	if p.released {
		return
	}
	for _, release := range p.guards {
		release()
	}
	p.guards = nil
	p.released = true
}

// collectAgentTypes returns deduplicated agent types required by a DAG:
// primary agent per node, co-agents per compound node, plus the
// pipeline-scoped inspector and tester.
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

	add("inspector-pipeline")
	add("tester-pipeline")

	return types
}
