package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/events"
)

// PipelineRegistrar registers an activated agent with the Guide's routing
// layer (RegisterRouter + MarkAgentReady). It is called once per unique
// agent type per pipeline — the pod deduplicates calls automatically.
type PipelineRegistrar func(ctx context.Context, agentType string) error

// PipelinePodConfig groups the dependencies for a PipelinePod.
type PipelinePodConfig struct {
	DAGID       string
	SessionID   string
	Activator   guide.AgentActivator
	Registrar   PipelineRegistrar
	ActivityPub events.ActivityPublisher
	Logger      *slog.Logger
}

// SubNodeMeta tracks a sub-node's agent assignment within a pipeline pod.
type SubNodeMeta struct {
	ParentNodeID string
	Stage        string // "inspect", "test", "execute"
	AgentType    string
	AgentID      string    // Set on ACK
	AckedAt      time.Time // When the agent ACKed
}

// nodeGuardEntry tracks the demotion guards held for a single DAG node.
type nodeGuardEntry struct {
	agentTypes []string  // agent types whose guards are held
	releases   []func()  // one release func per agent type
	acquiredAt time.Time // when the guards were acquired
}

// PipelinePod owns the lifecycle of all per-node demotion guards for a
// single DAG pipeline. It atomically activates agents (HoldActive),
// registers them with the Guide (deduplicating registrar calls across
// nodes), and tracks per-node guard state for observability.
//
// Guard lifecycle:
//   - HoldForNode: called by the dispatcher before dispatch — acquires guards,
//     registers with Guide, logs activation.
//   - ReleaseForNode: called on node completion — releases that node's guards,
//     logs release with held duration.
//   - Release: called on DAG cleanup — releases all remaining guards, logs
//     diagnostic summary of unreleased nodes.
//
// All methods are safe for concurrent use.
type PipelinePod struct {
	dagID       string
	sessionID   string
	activator   guide.AgentActivator
	registrar   PipelineRegistrar
	activityPub events.ActivityPublisher
	logger      *slog.Logger

	guardsMu   sync.Mutex
	nodeGuards map[string]*nodeGuardEntry // nodeID → guard entry
	released   bool

	// Registration dedup: agent types already registered with the Guide
	// for this pipeline. Prevents redundant registrar calls when multiple
	// nodes use the same agent type. Protected by guardsMu.
	registered map[string]struct{}

	// Sub-node tracking for expanded pipelines.
	subNodesMu sync.RWMutex
	subNodes   map[string]*SubNodeMeta // subNodeID → metadata
}

// NewPipelinePod creates a pod for the given DAG.
func NewPipelinePod(cfg PipelinePodConfig) *PipelinePod {
	return &PipelinePod{
		dagID:       cfg.DAGID,
		sessionID:   cfg.SessionID,
		activator:   cfg.Activator,
		registrar:   cfg.Registrar,
		activityPub: cfg.ActivityPub,
		logger:      cfg.Logger,
		nodeGuards:  make(map[string]*nodeGuardEntry),
		registered:  make(map[string]struct{}),
	}
}

// pipelineAgentTypes are the four agent types every pipeline requires.
var pipelineAgentTypes = [4]string{
	"inspector-pipeline",
	"tester-pipeline",
	"engineer",
	"designer",
}

// preActivateNodeID is the synthetic node ID under which pre-activation
// guards are tracked. Release() cleans them up with all other guards.
const preActivateNodeID = "__pre_activate__"

// PreActivate activates and registers all pipeline agent types upfront
// so they are visible in the TUI and subscribed to the bus before the
// first sub-node dispatches. Follows the same EnsureActive → RegisterRouter
// → MarkAgentReady path as boot-time agent activation, then publishes
// activity events so the TUI pipeline section renders each agent.
func (p *PipelinePod) PreActivate(ctx context.Context, _ *dag.DAG) {
	if p.activator == nil {
		return
	}

	var releases []func()
	var activated []string

	for _, agentType := range pipelineAgentTypes {
		release, err := p.activator.HoldActive(ctx, agentType)
		if err != nil {
			if p.logger != nil {
				p.logger.Warn("pipeline pod: pre-activate failed",
					"dag_id", p.dagID, "agent_type", agentType, "error", err)
			}
			continue
		}
		releases = append(releases, release)

		if err := p.registerOnce(ctx, agentType); err != nil {
			if p.logger != nil {
				p.logger.Warn("pipeline pod: pre-register failed",
					"dag_id", p.dagID, "agent_type", agentType, "error", err)
			}
			continue
		}
		activated = append(activated, agentType)
		p.publishPipelineActivity(agentType)
		p.logActivation(preActivateNodeID, agentType)
	}

	if len(releases) > 0 {
		p.guardsMu.Lock()
		p.nodeGuards[preActivateNodeID] = &nodeGuardEntry{
			agentTypes: activated,
			releases:   releases,
			acquiredAt: time.Now(),
		}
		p.guardsMu.Unlock()
	}
}

// publishPipelineActivity publishes an activity event so the TUI creates
// a pipeline-section entry for this agent type.
func (p *PipelinePod) publishPipelineActivity(agentType string) {
	if p.activityPub == nil {
		return
	}
	displayName := pipelineAgentDisplayNames[agentType]
	if displayName == "" {
		displayName = agentType
	}
	evt := events.NewActivityEvent(events.EventTypeAgentRegistered, p.sessionID,
		fmt.Sprintf("Pipeline agent registered: %s", agentType))
	evt.AgentID = agentType
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = agentType
	evt.Data["agent_name"] = displayName
	evt.Data["pipeline_id"] = p.dagID
	p.activityPub.PublishActivity(evt)
}

// HoldForNode atomically activates every agent required by a node
// (primary + co-agents), acquires demotion guards that prevent tier
// transitions while the node executes, and registers each unique agent
// type with the Guide's routing layer.
//
// On failure, all guards acquired for this node are released before
// the error is returned. Successful calls must be balanced by a
// corresponding ReleaseForNode or Release call.
func (p *PipelinePod) HoldForNode(ctx context.Context, nodeID string, node *dag.Node) error {
	if p.activator == nil {
		return nil
	}

	agentTypes := nodeAgentTypes(node)
	releases := make([]func(), 0, len(agentTypes))

	for _, agentType := range agentTypes {
		release, err := p.activator.HoldActive(ctx, agentType)
		if err != nil {
			// Rollback: release all guards acquired so far for this node.
			for _, r := range releases {
				r()
			}
			return fmt.Errorf("pipeline pod %s: hold %s for node %s: %w",
				p.dagID, agentType, nodeID, err)
		}
		releases = append(releases, release)

		// Register with Guide (deduped — only first occurrence per type).
		if err := p.registerOnce(ctx, agentType); err != nil {
			// Rollback: release all guards acquired so far including this one.
			for _, r := range releases {
				r()
			}
			return fmt.Errorf("pipeline pod %s: register %s for node %s: %w",
				p.dagID, agentType, nodeID, err)
		}

		p.logActivation(nodeID, agentType)
	}

	// Store the guard entry atomically.
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()

	if p.released {
		// Pod was released concurrently — roll back immediately.
		for _, r := range releases {
			r()
		}
		return fmt.Errorf("pipeline pod %s: released during activation of node %s",
			p.dagID, nodeID)
	}

	p.nodeGuards[nodeID] = &nodeGuardEntry{
		agentTypes: agentTypes,
		releases:   releases,
		acquiredAt: time.Now(),
	}
	return nil
}

// ReleaseForNode releases the demotion guards for a specific node.
// Logs the held duration for observability. Safe to call multiple times
// — subsequent calls for the same nodeID are no-ops.
func (p *PipelinePod) ReleaseForNode(nodeID string) {
	p.guardsMu.Lock()
	entry, ok := p.nodeGuards[nodeID]
	if !ok {
		p.guardsMu.Unlock()
		return
	}
	delete(p.nodeGuards, nodeID)
	p.guardsMu.Unlock()

	for _, r := range entry.releases {
		r()
	}

	if p.logger != nil {
		p.logger.Info("pipeline pod: node guards released",
			"dag_id", p.dagID,
			"node_id", nodeID,
			"agent_types", entry.agentTypes,
			"held_for", time.Since(entry.acquiredAt).Truncate(time.Millisecond),
		)
	}
}

// Release releases all outstanding demotion guards and logs a diagnostic
// summary. Safe for concurrent and repeated calls — only the first call
// releases, subsequent calls are no-ops.
func (p *PipelinePod) Release() {
	p.guardsMu.Lock()
	if p.released {
		p.guardsMu.Unlock()
		return
	}
	p.released = true

	// Snapshot and clear under lock to avoid races with concurrent
	// HoldForNode / ReleaseForNode calls.
	remaining := p.nodeGuards
	p.nodeGuards = make(map[string]*nodeGuardEntry)
	p.guardsMu.Unlock()

	if len(remaining) > 0 && p.logger != nil {
		nodeIDs := make([]string, 0, len(remaining))
		for id := range remaining {
			nodeIDs = append(nodeIDs, id)
		}
		p.logger.Warn("pipeline pod: releasing unreleased guards on cleanup",
			"dag_id", p.dagID,
			"unreleased_nodes", nodeIDs,
			"count", len(remaining),
		)
	}

	for _, entry := range remaining {
		for _, r := range entry.releases {
			r()
		}
	}

	if p.logger != nil {
		p.logger.Info("pipeline pod: all guards released",
			"dag_id", p.dagID,
			"released_count", len(remaining),
		)
	}
}

// ActiveGuardCount returns the number of nodes that currently hold
// demotion guards. For diagnostics and health monitoring.
func (p *PipelinePod) ActiveGuardCount() int {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()
	return len(p.nodeGuards)
}

// ActiveNodeIDs returns the IDs of nodes that currently hold demotion
// guards. For diagnostics and health monitoring.
func (p *PipelinePod) ActiveNodeIDs() []string {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()
	ids := make([]string, 0, len(p.nodeGuards))
	for id := range p.nodeGuards {
		ids = append(ids, id)
	}
	return ids
}

// RegisteredAgentTypes returns the agent types that have been registered
// with the Guide for this pipeline. For diagnostics.
func (p *PipelinePod) RegisteredAgentTypes() []string {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()
	types := make([]string, 0, len(p.registered))
	for t := range p.registered {
		types = append(types, t)
	}
	return types
}

// registerOnce calls the registrar for agentType if it hasn't been
// registered yet for this pipeline. Thread-safe.
func (p *PipelinePod) registerOnce(ctx context.Context, agentType string) error {
	if p.registrar == nil {
		return nil
	}

	p.guardsMu.Lock()
	if _, already := p.registered[agentType]; already {
		p.guardsMu.Unlock()
		return nil
	}
	p.guardsMu.Unlock()

	// Call registrar outside the lock (may be slow / involve I/O).
	if err := p.registrar(ctx, agentType); err != nil {
		return err
	}

	// Mark as registered. Double-registration is harmless (registrar is
	// idempotent) but we avoid it for performance.
	p.guardsMu.Lock()
	p.registered[agentType] = struct{}{}
	p.guardsMu.Unlock()
	return nil
}

// logActivation logs an agent activation event with structured fields.
func (p *PipelinePod) logActivation(nodeID, agentType string) {
	if p.logger == nil {
		return
	}
	p.logger.Info("pipeline pod: agent activated",
		"dag_id", p.dagID,
		"node_id", nodeID,
		"agent_type", agentType,
	)
}

// --------------------------------------------------------------------------
// Sub-node tracking
// --------------------------------------------------------------------------

// RegisterSubNode records a sub-node from pipeline expansion.
func (p *PipelinePod) RegisterSubNode(subNodeID, parentNodeID, stage, agentType string) {
	p.subNodesMu.Lock()
	defer p.subNodesMu.Unlock()

	if p.subNodes == nil {
		p.subNodes = make(map[string]*SubNodeMeta)
	}

	p.subNodes[subNodeID] = &SubNodeMeta{
		ParentNodeID: parentNodeID,
		Stage:        stage,
		AgentType:    agentType,
	}
}

// RecordSubNodeACK marks a sub-node as acknowledged by an agent.
func (p *PipelinePod) RecordSubNodeACK(subNodeID, agentID string, ackedAt time.Time) {
	p.subNodesMu.Lock()
	defer p.subNodesMu.Unlock()

	if meta, ok := p.subNodes[subNodeID]; ok {
		meta.AgentID = agentID
		meta.AckedAt = ackedAt
	}
}

// GetSubNode returns the metadata for a sub-node, or nil if not tracked.
func (p *PipelinePod) GetSubNode(subNodeID string) *SubNodeMeta {
	p.subNodesMu.RLock()
	defer p.subNodesMu.RUnlock()
	return p.subNodes[subNodeID]
}

// SubNodeCount returns the number of tracked sub-nodes.
func (p *PipelinePod) SubNodeCount() int {
	p.subNodesMu.RLock()
	defer p.subNodesMu.RUnlock()
	return len(p.subNodes)
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// nodeAgentTypes returns the primary agent type plus co-agents for a node.
func nodeAgentTypes(node *dag.Node) []string {
	coAgents := node.CoAgents()
	types := make([]string, 0, 1+len(coAgents))
	types = append(types, node.AgentType())
	types = append(types, coAgents...)
	return types
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

	for _, t := range pipelineAgentTypes {
		add(t)
	}

	return types
}
