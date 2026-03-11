package shared

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container/pod"
	"github.com/adalundhe/sylk/core/events"
)

// PodRegistrar registers an activated agent with the Guide's routing layer.
type PodRegistrar func(ctx context.Context, agentType string) error

// ScribeConfig configures Scribe sidecars for an AgentPod.
// Nil ScribeConfig means no Scribes are started.
type ScribeConfig struct {
	Provider ScribeProvider
	Model    string
	Bus      guide.EventBus
	Scope    *concurrency.GoroutineScope
}

// ScribeProvider is the minimal LLM interface Scribes need.
type ScribeProvider interface {
	Complete(ctx context.Context, req ScribeRequest) (string, error)
}

// ScribeRequest is the LLM request a Scribe sends.
type ScribeRequest struct {
	Model        string
	MaxTokens    int
	SystemPrompt string
	Messages     []ScribeMessage
}

// ScribeMessage is a single message in a Scribe's conversation.
type ScribeMessage struct {
	Role    string
	Content string
}

// ScribeFeed is a single turn's data from a parent agent.
type ScribeFeed struct {
	UserRequest   string
	AgentResponse string
	CorrelationID string
	Timestamp     time.Time
}

// Scribe is an interface for the per-agent summarization sidecar.
// The concrete implementation lives in agents/scribe to avoid circular imports.
type Scribe interface {
	Start() error
	Stop() error
	Feed(feed ScribeFeed)
}

// ScribeFactory creates a Scribe for a given parent agent type.
type ScribeFactory func(parentAgentType string, logger *slog.Logger) (Scribe, error)

// AgentPodConfig configures an AgentPod.
type AgentPodConfig struct {
	PodID       string
	SessionID   string
	Activator   guide.PodActivator
	Registrar   PodRegistrar
	ActivityPub events.ActivityPublisher
	// RegistrationVisibility controls the visibility of synthesized
	// registration events emitted during pre-activation. Zero defaults to
	// events.VisibilityUser for backward compatibility.
	RegistrationVisibility events.EventVisibility
	Logger                 *slog.Logger
	Scope                  *concurrency.GoroutineScope
	MemberTypes            []string
	DisplayNames           map[string]string

	// ScribeFactory creates Scribes for each member type. Nil = no Scribes.
	ScribeFactory ScribeFactory

	// Managed is the underlying ManagedPod for tier lifecycle and guard
	// tracking. When set, guard operations delegate to ManagedPod. When
	// nil, guards delegate to the PodActivator directly (legacy path).
	Managed *pod.ManagedPod
}

// PodGuardEntry tracks the demotion guards held for a single node.
type PodGuardEntry struct {
	AgentTypes []string
	Releases   []func()
	AcquiredAt time.Time
}

// SubNodeMeta tracks a sub-node's agent assignment within a pod.
type SubNodeMeta struct {
	ParentNodeID string
	Stage        string
	AgentType    string
	AgentID      string
	AckedAt      time.Time
}

// preActivateNodeID is the synthetic node ID under which pre-activation
// guards are tracked.
const preActivateNodeID = "__pre_activate__"

// AgentPod is a universal container for managing agent demotion guards,
// registration, and Scribe sidecars. It supersedes PipelinePod: pipeline
// agents and global agents both use AgentPod — only member composition differs.
//
// When Managed is set, guard operations delegate to the ManagedPod for
// pod-level tier lifecycle and resource tracking. When nil, the legacy
// path delegates directly to the PodActivator.
type AgentPod struct {
	podID        string
	sessionID    string
	activator    guide.PodActivator
	managed      *pod.ManagedPod
	registrar    PodRegistrar
	activityPub  events.ActivityPublisher
	regVis       events.EventVisibility
	logger       *slog.Logger
	scope        *concurrency.GoroutineScope
	memberTypes  []string
	displayNames map[string]string

	// Demotion guards — per-node (pipeline) or per-request (global).
	guardsMu   sync.Mutex
	nodeGuards map[string]*PodGuardEntry
	released   bool

	// Registration dedup.
	registered map[string]struct{}

	// Sub-node tracking (pipeline use).
	subNodesMu sync.RWMutex
	subNodes   map[string]*SubNodeMeta

	// Scribe management.
	scribeFactory ScribeFactory
	scribesMu     sync.RWMutex
	scribes       map[string]Scribe
}

// NewAgentPod creates a pod with the given configuration.
func NewAgentPod(cfg AgentPodConfig) *AgentPod {
	regVis := cfg.RegistrationVisibility
	if regVis == 0 {
		regVis = events.VisibilityUser
	}
	return &AgentPod{
		podID:         cfg.PodID,
		sessionID:     cfg.SessionID,
		activator:     cfg.Activator,
		managed:       cfg.Managed,
		registrar:     cfg.Registrar,
		activityPub:   cfg.ActivityPub,
		regVis:        regVis,
		logger:        cfg.Logger,
		scope:         cfg.Scope,
		memberTypes:   cfg.MemberTypes,
		displayNames:  cfg.DisplayNames,
		nodeGuards:    make(map[string]*PodGuardEntry),
		registered:    make(map[string]struct{}),
		scribes:       make(map[string]Scribe),
		scribeFactory: cfg.ScribeFactory,
	}
}

// ManagedPod returns the underlying ManagedPod, or nil if not set.
func (p *AgentPod) ManagedPod() *pod.ManagedPod {
	return p.managed
}

// PreActivate activates and registers all member types upfront so they
// are visible in the TUI and subscribed to the bus before the first
// dispatch. Best-effort: failures are logged and skipped.
func (p *AgentPod) PreActivate(ctx context.Context) {
	_ = p.preActivate(ctx, false)
}

// PreActivateStrict activates and registers all member types upfront,
// returning an error on the first activation/registration failure.
func (p *AgentPod) PreActivateStrict(ctx context.Context) error {
	return p.preActivate(ctx, true)
}

// AdvertiseMembers publishes waiting/registered activity for all member types
// without activating containers or acquiring guards. This keeps pipeline rows
// visible in the UI while preserving lazy runtime activation.
func (p *AgentPod) AdvertiseMembers() {
	for _, agentType := range p.memberTypes {
		p.publishActivity(agentType)
	}
}

func (p *AgentPod) preActivate(ctx context.Context, strict bool) error {
	if p.activator == nil && p.managed == nil {
		return nil
	}

	releases := make([]func(), 0, len(p.memberTypes))
	activated := make([]string, 0, len(p.memberTypes))

	for _, agentType := range p.memberTypes {
		release, err := p.preActivateMember(ctx, agentType)
		if err != nil {
			if strict {
				releaseAll(releases)
				return err
			}
			continue
		}
		releases = append(releases, release)
		activated = append(activated, agentType)
		p.publishActivity(agentType)
		p.logActivation(preActivateNodeID, agentType)
	}

	p.storePreActivationGuards(activated, releases)
	p.startScribes()
	return nil
}

func (p *AgentPod) preActivateMember(ctx context.Context, agentType string) (func(), error) {
	release, err := p.acquireGuard(ctx, agentType)
	if err != nil {
		p.logPreActivateFailure("pre-activate failed", agentType, err)
		return nil, fmt.Errorf("agent pod %s: pre-activate %s: %w", p.podID, agentType, err)
	}
	if err := p.registerOnce(ctx, agentType); err != nil {
		release()
		p.logPreActivateFailure("pre-register failed", agentType, err)
		return nil, fmt.Errorf("agent pod %s: pre-register %s: %w", p.podID, agentType, err)
	}
	return release, nil
}

func (p *AgentPod) logPreActivateFailure(message, agentType string, err error) {
	if p.logger == nil {
		return
	}
	p.logger.Warn("agent pod: "+message,
		"pod_id", p.podID,
		"agent_type", agentType,
		"error", err,
	)
}

func (p *AgentPod) storePreActivationGuards(agentTypes []string, releases []func()) {
	if len(releases) == 0 {
		return
	}
	p.guardsMu.Lock()
	p.nodeGuards[preActivateNodeID] = &PodGuardEntry{
		AgentTypes: agentTypes,
		Releases:   releases,
		AcquiredAt: time.Now(),
	}
	p.guardsMu.Unlock()
}

func releaseAll(releases []func()) {
	for _, release := range releases {
		release()
	}
}

// HoldForNode atomically activates every agent type in agentTypes,
// acquires demotion guards, and registers each unique agent type with
// the Guide. On failure, all guards acquired for this node are released.
func (p *AgentPod) HoldForNode(ctx context.Context, nodeID string, agentTypes []string) error {
	if p.activator == nil && p.managed == nil {
		return nil
	}

	releases := make([]func(), 0, len(agentTypes))

	for _, agentType := range agentTypes {
		release, err := p.acquireGuard(ctx, agentType)
		if err != nil {
			for _, r := range releases {
				r()
			}
			return fmt.Errorf("agent pod %s: hold %s for node %s: %w",
				p.podID, agentType, nodeID, err)
		}
		releases = append(releases, release)

		if err := p.registerOnce(ctx, agentType); err != nil {
			for _, r := range releases {
				r()
			}
			return fmt.Errorf("agent pod %s: register %s for node %s: %w",
				p.podID, agentType, nodeID, err)
		}

		p.logActivation(nodeID, agentType)
	}

	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()

	if p.released {
		for _, r := range releases {
			r()
		}
		return fmt.Errorf("agent pod %s: released during activation of node %s",
			p.podID, nodeID)
	}

	p.nodeGuards[nodeID] = &PodGuardEntry{
		AgentTypes: agentTypes,
		Releases:   releases,
		AcquiredAt: time.Now(),
	}
	return nil
}

// ReleaseForNode releases demotion guards for a specific node.
// Idempotent — subsequent calls for the same nodeID are no-ops.
func (p *AgentPod) ReleaseForNode(nodeID string) {
	p.guardsMu.Lock()
	entry, ok := p.nodeGuards[nodeID]
	if !ok {
		p.guardsMu.Unlock()
		return
	}
	delete(p.nodeGuards, nodeID)
	p.guardsMu.Unlock()

	for _, r := range entry.Releases {
		r()
	}

	if p.logger != nil {
		p.logger.Info("agent pod: node guards released",
			"pod_id", p.podID,
			"node_id", nodeID,
			"agent_types", entry.AgentTypes,
			"held_for", time.Since(entry.AcquiredAt).Truncate(time.Millisecond),
		)
	}
}

// Release releases all outstanding demotion guards and stops all Scribes.
// Safe for concurrent and repeated calls — only the first call acts.
func (p *AgentPod) Release() {
	p.guardsMu.Lock()
	if p.released {
		p.guardsMu.Unlock()
		return
	}
	p.released = true

	remaining := p.nodeGuards
	p.nodeGuards = make(map[string]*PodGuardEntry)
	p.guardsMu.Unlock()

	if len(remaining) > 0 && p.logger != nil {
		nodeIDs := make([]string, 0, len(remaining))
		for id := range remaining {
			nodeIDs = append(nodeIDs, id)
		}
		p.logger.Warn("agent pod: releasing unreleased guards on cleanup",
			"pod_id", p.podID,
			"unreleased_nodes", nodeIDs,
			"count", len(remaining),
		)
	}

	for _, entry := range remaining {
		for _, r := range entry.Releases {
			r()
		}
	}

	if p.logger != nil {
		p.logger.Info("agent pod: all guards released",
			"pod_id", p.podID,
			"released_count", len(remaining),
		)
	}

	p.stopScribes()
}

// ActiveGuardCount returns the number of nodes with active guards.
func (p *AgentPod) ActiveGuardCount() int {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()
	return len(p.nodeGuards)
}

// ActiveNodeIDs returns the IDs of nodes with active guards.
func (p *AgentPod) ActiveNodeIDs() []string {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()
	ids := make([]string, 0, len(p.nodeGuards))
	for id := range p.nodeGuards {
		ids = append(ids, id)
	}
	return ids
}

// RegisteredAgentTypes returns agent types registered with the Guide.
func (p *AgentPod) RegisteredAgentTypes() []string {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()
	types := make([]string, 0, len(p.registered))
	for t := range p.registered {
		types = append(types, t)
	}
	return types
}

// acquireGuard acquires a demotion guard for the given agent type.
// When ManagedPod is set, delegates to it for pod-level tracking.
// Otherwise falls back to the PodActivator.
func (p *AgentPod) acquireGuard(ctx context.Context, agentType string) (func(), error) {
	if p.managed != nil {
		if p.managed.LoadTier() != pod.TierHot {
			if err := p.managed.Promote(ctx, nil); err != nil {
				return nil, err
			}
		}
		return p.managed.AcquireRequestGuard(), nil
	}
	if p.activator != nil {
		return p.activator.HoldPodActive(ctx, p.activator.PodForAgent(agentType))
	}
	return func() {}, nil
}

// registerOnce calls the registrar for agentType if not already registered.
func (p *AgentPod) registerOnce(ctx context.Context, agentType string) error {
	if p.registrar == nil {
		return nil
	}

	p.guardsMu.Lock()
	if _, already := p.registered[agentType]; already {
		p.guardsMu.Unlock()
		return nil
	}
	p.guardsMu.Unlock()

	if err := p.registrar(ctx, agentType); err != nil {
		return err
	}

	p.guardsMu.Lock()
	p.registered[agentType] = struct{}{}
	p.guardsMu.Unlock()
	return nil
}

// logActivation logs an agent activation event.
func (p *AgentPod) logActivation(nodeID, agentType string) {
	if p.logger == nil {
		return
	}
	p.logger.Info("agent pod: agent activated",
		"pod_id", p.podID,
		"node_id", nodeID,
		"agent_type", agentType,
	)
}

// publishActivity publishes an activity event for agent registration.
func (p *AgentPod) publishActivity(agentType string) {
	if p.activityPub == nil {
		return
	}
	displayName := p.displayNames[agentType]
	if displayName == "" {
		displayName = agentType
	}
	content := fmt.Sprintf("Agent registered: %s", agentType)
	evt := events.NewActivityEvent(events.EventTypeAgentRegistered, p.sessionID, content)
	evt.AgentID = agentType
	evt.Visibility = p.regVis
	evt.Data["agent_type"] = agentType
	evt.Data["agent_name"] = displayName
	evt.Data["pod_id"] = p.podID
	if pipelineID, ok := pipelineActivityScope(p.podID, agentType); ok {
		evt.AgentID = pipelineID + ":" + agentType
		evt.Content = fmt.Sprintf("Pipeline agent registered: %s", agentType)
		evt.Data["pipeline_id"] = pipelineID
		evt.Data["task_id"] = pipelineID
	}
	p.activityPub.PublishActivity(evt)
}

func pipelineActivityScope(podID, agentType string) (string, bool) {
	podID = strings.TrimSpace(podID)
	if podID == "" || podID == agentType {
		return "", false
	}
	switch agentType {
	case "engineer", "designer", "inspector-pipeline", "tester-pipeline":
		return podID, true
	default:
		return "", false
	}
}

// --------------------------------------------------------------------------
// Sub-node tracking
// --------------------------------------------------------------------------

// RegisterSubNode records a sub-node from pipeline expansion.
func (p *AgentPod) RegisterSubNode(subNodeID, parentNodeID, stage, agentType string) {
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
func (p *AgentPod) RecordSubNodeACK(subNodeID, agentID string, ackedAt time.Time) {
	p.subNodesMu.Lock()
	defer p.subNodesMu.Unlock()

	if meta, ok := p.subNodes[subNodeID]; ok {
		meta.AgentID = agentID
		meta.AckedAt = ackedAt
	}
}

// GetSubNode returns the metadata for a sub-node, or nil if not tracked.
func (p *AgentPod) GetSubNode(subNodeID string) *SubNodeMeta {
	p.subNodesMu.RLock()
	defer p.subNodesMu.RUnlock()
	return p.subNodes[subNodeID]
}

// SubNodeCount returns the number of tracked sub-nodes.
func (p *AgentPod) SubNodeCount() int {
	p.subNodesMu.RLock()
	defer p.subNodesMu.RUnlock()
	return len(p.subNodes)
}

// --------------------------------------------------------------------------
// Scribe management
// --------------------------------------------------------------------------

// FeedScribe feeds turn data to the Scribe for parentAgentType.
// No-op if no Scribe exists for that type.
func (p *AgentPod) FeedScribe(parentAgentType, userRequest, agentResponse, correlationID string) {
	p.scribesMu.RLock()
	s, ok := p.scribes[parentAgentType]
	p.scribesMu.RUnlock()

	if !ok {
		return
	}
	s.Feed(ScribeFeed{
		UserRequest:   userRequest,
		AgentResponse: agentResponse,
		CorrelationID: correlationID,
		Timestamp:     time.Now(),
	})
}

// GetScribe returns the Scribe for a parent agent type, or nil.
func (p *AgentPod) GetScribe(parentAgentType string) Scribe {
	p.scribesMu.RLock()
	defer p.scribesMu.RUnlock()
	return p.scribes[parentAgentType]
}

// startScribes creates and starts a Scribe for each member type.
func (p *AgentPod) startScribes() {
	if p.scribeFactory == nil {
		return
	}

	for _, memberType := range p.memberTypes {
		s, err := p.scribeFactory(memberType, p.logger)
		if err != nil {
			if p.logger != nil {
				p.logger.Warn("agent pod: scribe creation failed",
					"parent", memberType, "error", err)
			}
			continue
		}
		if err := s.Start(); err != nil {
			if p.logger != nil {
				p.logger.Warn("agent pod: scribe start failed",
					"parent", memberType, "error", err)
			}
			continue
		}
		p.scribesMu.Lock()
		p.scribes[memberType] = s
		p.scribesMu.Unlock()
	}
}

// stopScribes stops all Scribes.
func (p *AgentPod) stopScribes() {
	p.scribesMu.Lock()
	scribes := p.scribes
	p.scribes = make(map[string]Scribe)
	p.scribesMu.Unlock()

	for memberType, s := range scribes {
		if err := s.Stop(); err != nil && p.logger != nil {
			p.logger.Warn("agent pod: scribe stop failed",
				"parent", memberType, "error", err)
		}
	}
}
