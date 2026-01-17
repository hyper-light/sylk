package guide

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// =============================================================================
// Guide Agent
// =============================================================================

// Guide is the central routing hub for all inter-agent communication.
// All requests and responses flow through the Guide via the EventBus:
// SourceAgent -> EventBus -> Guide -> EventBus -> TargetAgent -> EventBus -> Guide -> EventBus -> SourceAgent
type Guide struct {
	// Core components
	router *Router
	config Config

	// Event bus for async message passing
	bus EventBus

	// Subscription to Guide's own request channel (guide.requests)
	requestSub Subscription

	// Per-agent subscriptions to their response and error channels
	// Key: agentID, Value: subscriptions to <agent>.responses and <agent>.errors
	agentSubs *ShardedMap[string, *agentSubscriptions]

	// Agent registry for looking up registered agents (capabilities/constraints)
	registry AgentRegistry

	// Routing aggregator for dynamic shortcuts and triggers
	routing *RoutingAggregator

	// Trigger detector for NL routing hints
	triggers *TriggerDetector

	// Pending request store for correlation tracking
	pending *PendingStore

	// Route cache for avoiding repeat LLM classifications
	routeCache *RouteCache

	// Agent channels - tracks channel names for each agent
	agentChannels *ShardedMap[string, *AgentChannels]

	// Ready agents - agents that have completed initialization
	readyAgents *ShardedMap[string, bool]

	// Resilience components
	circuits       *CircuitBreakerRegistry
	health         *HealthMonitor
	dlq            *DeadLetterQueue
	pendingCleanup *PendingCleanup

	// LLM Skills and Hooks
	skills      *skills.Registry
	skillLoader *skills.Loader
	hooks       *skills.HookRegistry

	// Session metadata
	sessionID string
	agentID   string

	// Running state
	running bool
}

// agentSubscriptions holds the Guide's subscriptions to an agent's channels
type agentSubscriptions struct {
	responses Subscription
	errors    Subscription
}

// Config configures the Guide agent
type Config struct {
	// Router configuration
	RouterConfig RouterConfig

	// Event bus (required for message passing)
	Bus EventBus

	// Pending request configuration
	PendingTimeout     time.Duration // Default: 5 minutes
	MaxPendingPerAgent int           // Default: 1000

	// Route cache configuration
	RouteCacheConfig *RouteCacheConfig

	// Session information
	SessionID string
	AgentID   string

	// Agent registry for looking up target agents (uses default if nil)
	Registry AgentRegistry

	// Skills configuration
	SkillsConfig *skills.LoaderConfig
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		RouterConfig: DefaultRouterConfig(),
	}
}

// New creates a new Guide agent
func New(client *anthropic.Client, cfg Config) (*Guide, error) {
	cfg = ensureRouterConfig(cfg)
	if err := validateBus(cfg); err != nil {
		return nil, err
	}

	registry := resolveRegistry(cfg)
	routing := NewRoutingAggregator()
	routing.RegisterAgent(GuideRoutingInfo())

	parser := NewParserWithRouting(cfg.RouterConfig.DSLPrefix, routing)
	classifier := NewClassifierWithClient(NewRealClassifierClient(client), cfg.RouterConfig)
	router := &Router{
		config:     cfg.RouterConfig,
		parser:     parser,
		classifier: classifier,
	}

	pendingCfg := resolvePendingConfig(cfg)
	agentID := resolveAgentID(cfg)
	cacheCfg := resolveRouteCacheConfig(cfg)
	circuits := NewCircuitBreakerRegistry(DefaultCircuitBreakerConfig())
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{MaxSize: 10000})
	pendingCleanup := NewPendingCleanup(PendingCleanupConfig{
		CheckInterval:  1 * time.Second,
		DefaultTimeout: pendingCfg.DefaultTimeout,
		DLQ:            dlq,
		Circuits:       circuits,
	})
	health := NewHealthMonitor(cfg.Bus, HealthMonitorConfig{
		AgentConfig: DefaultAgentHealthConfig(),
		Circuits:    circuits,
	})

	skillsRegistry, skillLoader, hookRegistry := buildSkills(cfg)

	guide := &Guide{
		router:         router,
		config:         cfg,
		bus:            cfg.Bus,
		agentSubs:      NewStringMap[*agentSubscriptions](DefaultShardCount),
		agentChannels:  NewStringMap[*AgentChannels](DefaultShardCount),
		readyAgents:    NewStringMap[bool](DefaultShardCount),
		registry:       registry,
		routing:        routing,
		triggers:       NewTriggerDetector(routing),
		pending:        NewPendingStore(pendingCfg),
		routeCache:     NewRouteCache(cacheCfg),
		circuits:       circuits,
		health:         health,
		dlq:            dlq,
		pendingCleanup: pendingCleanup,
		skills:         skillsRegistry,
		skillLoader:    skillLoader,
		hooks:          hookRegistry,
		sessionID:      cfg.SessionID,
		agentID:        agentID,
	}

	guide.registerCoreSkills()
	guide.registerExtendedSkills()

	return guide, nil
}

func ensureRouterConfig(cfg Config) Config {
	if cfg.RouterConfig.Model == "" {
		cfg.RouterConfig = DefaultRouterConfig()
	}
	return cfg
}

func validateBus(cfg Config) error {
	if cfg.Bus == nil {
		return fmt.Errorf("EventBus is required")
	}
	return nil
}

func resolveRegistry(cfg Config) AgentRegistry {
	registry := cfg.Registry
	if registry != nil {
		return registry
	}
	return NewRegistryWithDefaults()
}

func resolvePendingConfig(cfg Config) PendingStoreConfig {
	pendingCfg := PendingStoreConfig{
		DefaultTimeout: cfg.PendingTimeout,
		MaxPerAgent:    cfg.MaxPendingPerAgent,
	}
	if pendingCfg.DefaultTimeout == 0 {
		pendingCfg.DefaultTimeout = 5 * time.Minute
	}
	if pendingCfg.MaxPerAgent == 0 {
		pendingCfg.MaxPerAgent = 1000
	}
	return pendingCfg
}

func resolveAgentID(cfg Config) string {
	if cfg.AgentID != "" {
		return cfg.AgentID
	}
	return "guide"
}

func resolveRouteCacheConfig(cfg Config) RouteCacheConfig {
	cacheCfg := DefaultRouteCacheConfig()
	if cfg.RouteCacheConfig != nil {
		return *cfg.RouteCacheConfig
	}
	return cacheCfg
}

func buildSkills(cfg Config) (*skills.Registry, *skills.Loader, *skills.HookRegistry) {
	skillsRegistry := skills.NewRegistry()
	skillsLoaderCfg := skills.DefaultLoaderConfig()
	if cfg.SkillsConfig != nil {
		skillsLoaderCfg = *cfg.SkillsConfig
	}
	skillsLoaderCfg.CoreSkills = []string{"route", "help", "status"}
	skillsLoaderCfg.AutoLoadDomains = []string{"routing"}
	return skillsRegistry, skills.NewLoader(skillsRegistry, skillsLoaderCfg), skills.NewHookRegistry()
}

// NewWithAPIKey creates a new Guide agent with an API key
func NewWithAPIKey(apiKey string, cfg Config) (*Guide, error) {
	if cfg.RouterConfig.Model == "" {
		cfg.RouterConfig = DefaultRouterConfig()
	}

	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	client := anthropic.NewClient(opts...)

	return New(&client, cfg)
}

// =============================================================================
// Public API - Request Routing
// =============================================================================

// Route routes a request and returns a ForwardedRequest for the target agent.
// The correlation ID is stored for response routing back to the source.
//
// Routing priority:
// 1. Explicit target (request.TargetAgentID) - bypass classification
// 2. DSL command - parsed deterministically (0 tokens)
// 3. Route cache hit - previously classified (0 tokens)
// 4. LLM classification - cache miss (~250 tokens)
func (g *Guide) Route(ctx context.Context, request *RouteRequest) (*ForwardedRequest, error) {
	g.ensureRequestDefaults(request)

	classification, targetAgentID, err := g.resolveClassification(ctx, request)
	if err != nil {
		return nil, err
	}

	corrID := g.resolveCorrelationID(request)
	if !request.FireAndForget {
		corrID = g.pending.Add(request, classification, targetAgentID)
	}

	return g.buildForwardedRequest(request, classification, corrID), nil
}

func (g *Guide) ensureRequestDefaults(request *RouteRequest) {
	if request.Timestamp.IsZero() {
		request.Timestamp = time.Now()
	}
	if request.SessionID == "" {
		request.SessionID = g.sessionID
	}
}

func (g *Guide) resolveClassification(ctx context.Context, request *RouteRequest) (*RouteResult, string, error) {
	if request.TargetAgentID != "" {
		classification := &RouteResult{
			TargetAgent:          TargetAgent(request.TargetAgentID),
			Confidence:           1.0,
			ClassificationMethod: "explicit",
		}
		return classification, request.TargetAgentID, nil
	}

	classification, targetAgentID, ok := g.cachedClassification(request)
	if ok {
		return classification, targetAgentID, nil
	}

	return g.classifyWithRouter(ctx, request)
}

func (g *Guide) cachedClassification(request *RouteRequest) (*RouteResult, string, bool) {
	if g.router.IsDSL(request.Input) {
		return nil, "", false
	}
	cached := g.routeCache.Get(request.Input)
	if cached == nil {
		return nil, "", false
	}
	classification := &RouteResult{
		TargetAgent:          TargetAgent(cached.TargetAgentID),
		Intent:               cached.Intent,
		Domain:               cached.Domain,
		Confidence:           cached.Confidence,
		ClassificationMethod: "cache",
	}
	return classification, cached.TargetAgentID, true
}

func (g *Guide) classifyWithRouter(ctx context.Context, request *RouteRequest) (*RouteResult, string, error) {
	classification, err := g.router.Route(ctx, request)
	if err != nil {
		return nil, "", err
	}

	targetAgentID := string(classification.TargetAgent)
	g.cacheAndBroadcastClassification(request, classification)
	return classification, targetAgentID, nil
}

func (g *Guide) cacheAndBroadcastClassification(request *RouteRequest, classification *RouteResult) {
	if classification.ClassificationMethod != "llm" {
		return
	}
	g.routeCache.Set(request.Input, classification)
	g.broadcastLearnedRoute(request.Input, classification)
}

func (g *Guide) resolveCorrelationID(request *RouteRequest) string {
	if request.CorrelationID != "" {
		return request.CorrelationID
	}
	return fmt.Sprintf("corr_%d", time.Now().UnixNano())
}

func (g *Guide) buildForwardedRequest(request *RouteRequest, classification *RouteResult, correlationID string) *ForwardedRequest {
	return &ForwardedRequest{
		CorrelationID:        correlationID,
		ParentCorrelationID:  request.ParentCorrelationID,
		Input:                request.Input,
		Intent:               classification.Intent,
		Domain:               classification.Domain,
		Entities:             classification.Entities,
		SourceAgentID:        request.SourceAgentID,
		SourceAgentName:      request.SourceAgentName,
		FireAndForget:        request.FireAndForget,
		Confidence:           classification.Confidence,
		ClassificationMethod: classification.ClassificationMethod,
	}
}

// broadcastLearnedRoute broadcasts a newly learned route to all agents
func (g *Guide) broadcastLearnedRoute(input string, result *RouteResult) {
	if g.bus == nil || !g.running {
		return
	}

	route := &LearnedRoute{
		Input:         input,
		TargetAgentID: string(result.TargetAgent),
		Intent:        result.Intent,
		Domain:        result.Domain,
		Confidence:    result.Confidence,
	}

	msg := NewRouteLearnedMessage(fmt.Sprintf("msg_%d", time.Now().UnixNano()), route)
	_ = g.bus.Publish(TopicRoutesLearned, msg)
}

// RouteSimple is a convenience method for simple routing without full request struct
func (g *Guide) RouteSimple(ctx context.Context, input string, sourceAgentID string) (*ForwardedRequest, error) {
	request := &RouteRequest{
		Input:         input,
		SourceAgentID: sourceAgentID,
		SessionID:     g.sessionID,
		Timestamp:     time.Now(),
	}
	return g.Route(ctx, request)
}

// =============================================================================
// Public API - Response Handling
// =============================================================================

// HandleResponse processes a response from a target agent and returns
// the pending request info for routing back to the source agent.
func (g *Guide) HandleResponse(ctx context.Context, response *RouteResponse) (*PendingRequest, error) {
	if response.CorrelationID == "" {
		return nil, fmt.Errorf("response missing correlation ID")
	}

	// Look up and remove pending request
	pending := g.pending.Remove(response.CorrelationID)
	if pending == nil {
		return nil, fmt.Errorf("no pending request for correlation ID: %s", response.CorrelationID)
	}

	return pending, nil
}

// GetPending retrieves a pending request by correlation ID without removing it
func (g *Guide) GetPending(correlationID string) *PendingRequest {
	return g.pending.Get(correlationID)
}

// GetPendingBySource retrieves all pending requests from a source agent
func (g *Guide) GetPendingBySource(sourceAgentID string) []*PendingRequest {
	return g.pending.GetBySource(sourceAgentID)
}

// GetPendingByTarget retrieves all pending requests to a target agent
func (g *Guide) GetPendingByTarget(targetAgentID string) []*PendingRequest {
	return g.pending.GetByTarget(targetAgentID)
}

// CleanupExpired removes expired pending requests
func (g *Guide) CleanupExpired() []*PendingRequest {
	return g.pending.CleanupExpired()
}

// PendingCount returns the number of pending requests
func (g *Guide) PendingCount() int {
	return g.pending.Count()
}

// PendingStats returns statistics about pending requests
func (g *Guide) PendingStats() PendingStats {
	return g.pending.Stats()
}

// =============================================================================
// Classification API
// =============================================================================

// Classify classifies input without creating a pending request.
// Use this for inspection/testing without routing.
func (g *Guide) Classify(ctx context.Context, input string) (*RouteResult, error) {
	request := &RouteRequest{
		Input:     input,
		SessionID: g.sessionID,
		Timestamp: time.Now(),
	}
	return g.router.Route(ctx, request)
}

// RecordCorrection records a correction to improve future LLM classification.
// This adds the correction as a few-shot example for the classifier.
func (g *Guide) RecordCorrection(input string, wrong, correct *RouteResult, reason string) {
	correction := CorrectionRecord{
		Input:         input,
		WrongIntent:   wrong.Intent,
		WrongDomain:   wrong.Domain,
		WrongTarget:   wrong.TargetAgent,
		CorrectIntent: correct.Intent,
		CorrectDomain: correct.Domain,
		CorrectTarget: correct.TargetAgent,
		CorrectedAt:   time.Now(),
		Reason:        reason,
	}
	g.router.AddCorrection(correction)
}

// IsDSL checks if input is a structured DSL command
func (g *Guide) IsDSL(input string) bool {
	return g.router.IsDSL(input)
}

// ParseDSL parses a DSL command without routing
func (g *Guide) ParseDSL(input string) (*DSLCommand, error) {
	return g.router.ParseDSL(input)
}

// FormatAsDSL formats a route result back to DSL syntax
func (g *Guide) FormatAsDSL(result *RouteResult) string {
	return g.router.FormatAsDSL(result)
}

// =============================================================================
// Help and Status
// =============================================================================

// Help returns help information for a specific topic
func (g *Guide) Help(topic string) string {
	switch topic {
	case "dsl", "syntax":
		return HelpDSLSyntax
	case "agents":
		return HelpAgents
	default:
		return GuideSystemPrompt
	}
}

// Status returns the current status of the guide
func (g *Guide) Status() *GuideStatus {
	status := &GuideStatus{
		AgentID:   g.agentID,
		SessionID: g.sessionID,
		Healthy:   true,
	}

	// Add registered agents
	if g.registry != nil {
		agents := g.registry.GetAll()
		status.RegisteredAgents = make([]string, 0, len(agents))
		for _, agent := range agents {
			status.RegisteredAgents = append(status.RegisteredAgents, agent.Name)
		}
	}

	return status
}

// GuideStatus contains status information about the guide
type GuideStatus struct {
	AgentID          string   `json:"agent_id"`
	SessionID        string   `json:"session_id"`
	Healthy          bool     `json:"healthy"`
	RegisteredAgents []string `json:"registered_agents,omitempty"`
}

// =============================================================================
// Agent Resolution
// =============================================================================

// ResolveTarget resolves the target agent for a route result using the registry
func (g *Guide) ResolveTarget(result *RouteResult) (*ResolvedTarget, error) {
	// First try to find a registered agent by target name
	agent := g.registry.GetByName(string(result.TargetAgent))

	// If not found by name, find best match based on capabilities
	if agent == nil {
		agent = g.registry.FindBestMatch(result)
	}

	if agent == nil {
		return nil, fmt.Errorf("no registered agent can handle: intent=%s, domain=%s", result.Intent, result.Domain)
	}

	// Verify the agent accepts this request
	if !agent.Accepts(result) {
		return nil, fmt.Errorf("agent %s does not accept: intent=%s, domain=%s, temporal=%s",
			agent.Name, result.Intent, result.Domain, result.TemporalFocus)
	}

	resolved := &ResolvedTarget{
		TargetAgent: TargetAgent(agent.ID),
		AgentID:     agent.ID,
		AgentName:   agent.Name,
		Intent:      result.Intent,
		Domain:      result.Domain,
		Entities:    result.Entities,
	}

	// Resolve tool name based on agent and intent/domain
	resolved.ToolName = g.resolveToolName(agent, result)

	return resolved, nil
}

// ResolvedTarget contains the resolved target for routing
type ResolvedTarget struct {
	TargetAgent TargetAgent        `json:"target_agent"`
	AgentID     string             `json:"agent_id"`
	AgentName   string             `json:"agent_name"`
	ToolName    string             `json:"tool_name"`
	Intent      Intent             `json:"intent"`
	Domain      Domain             `json:"domain"`
	Entities    *ExtractedEntities `json:"entities,omitempty"`
}

// resolveToolName determines the tool name based on agent and intent/domain.
// Uses convention: <agent_name>_<action>_<domain> unless agent provides custom resolution.
func (g *Guide) resolveToolName(agent *AgentRegistration, result *RouteResult) string {
	// Check if agent has a custom tool resolver in routing info
	if info := g.routing.GetRoutingInfo(agent.ID); info != nil {
		// Use agent ID for lookup, agents can provide tool mappings in the future
		_ = info // Reserved for future tool mapping support
	}

	// Default tool naming convention
	action := g.intentToAction(result.Intent)
	return fmt.Sprintf("%s_%s_%s", agent.Name, action, result.Domain)
}

// intentToAction maps intents to action verbs for tool names
func (g *Guide) intentToAction(intent Intent) string {
	if action, ok := intentActionMap()[intent]; ok {
		return action
	}
	return string(intent)
}

func intentActionMap() map[Intent]string {
	return map[Intent]string{
		IntentRecall:   "query",
		IntentStore:    "record",
		IntentCheck:    "check",
		IntentDeclare:  "declare",
		IntentComplete: "complete",
		IntentHelp:     "help",
		IntentStatus:   "status",
	}
}

// =============================================================================
// Agent Registration
// =============================================================================

// Register registers an agent with all its routing information.
// This is the preferred way to register agents - it handles:
// - Capabilities and constraints (for routing decisions)
// - DSL aliases (for @agent shortcuts)
// - Action shortcuts (for @action commands)
// - Trigger phrases (for NL detection)
// - Creating agent channels (<agent>.requests, <agent>.responses, <agent>.errors)
// - Subscribing to agent's response and error channels
// - Publishing announcement to event bus (so other agents are notified)
func (g *Guide) Register(info *AgentRoutingInfo) error {
	if info == nil {
		return fmt.Errorf("routing info is nil")
	}

	// Register with routing aggregator (aliases, actions, triggers)
	g.routing.RegisterAgent(info)

	// Register capabilities/constraints with registry
	if info.Registration != nil {
		g.registry.Register(info.Registration)
	}

	// Update parser with new aliases
	g.router.parser.SetRouting(g.routing)

	// Create and store agent channels
	channels := NewAgentChannels(info.ID)
	g.agentChannels.Set(info.ID, channels)

	// Mark agent as not ready yet (waiting for ready announcement)
	g.readyAgents.Set(info.ID, false)

	// Register with health monitor
	g.health.Register(info.ID)

	// Subscribe to agent's response and error channels (if bus is running)
	if g.bus != nil && g.running {
		subs, err := g.subscribeToAgentChannels(info.ID, channels)
		if err != nil {
			// Rollback registration on subscription failure
			g.routing.UnregisterAgent(info.ID)
			g.registry.Unregister(info.ID)
			g.agentChannels.Delete(info.ID)
			g.readyAgents.Delete(info.ID)
			g.health.Unregister(info.ID)
			return fmt.Errorf("failed to subscribe to agent channels: %w", err)
		}
		g.agentSubs.Set(info.ID, subs)

		// Publish registration announcement
		msg := NewAgentRegisteredMessage(generateMessageID(), info)
		_ = g.bus.Publish(TopicAgentRegistry, msg)
	}

	return nil
}

// subscribeToAgentChannels subscribes to an agent's response and error channels
func (g *Guide) subscribeToAgentChannels(agentID string, channels *AgentChannels) (*agentSubscriptions, error) {
	// Subscribe to responses channel
	respSub, err := g.bus.SubscribeAsync(channels.Responses, g.handleResponseMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to %s: %w", channels.Responses, err)
	}

	// Subscribe to errors channel
	errSub, err := g.bus.SubscribeAsync(channels.Errors, g.handleErrorMessage)
	if err != nil {
		respSub.Unsubscribe()
		return nil, fmt.Errorf("failed to subscribe to %s: %w", channels.Errors, err)
	}

	return &agentSubscriptions{
		responses: respSub,
		errors:    errSub,
	}, nil
}

// RegisterRouter registers an agent that implements AgentRouter
func (g *Guide) RegisterRouter(router AgentRouter) error {
	if router == nil {
		return fmt.Errorf("router is nil")
	}
	return g.Register(router.GetRoutingInfo())
}

// Unregister removes an agent from the guide and notifies other agents
func (g *Guide) Unregister(id string) {
	// Get agent info before unregistering (for the announcement)
	info := g.routing.GetRoutingInfo(id)
	agentName := id
	if info != nil {
		agentName = info.Name
	}

	// Unsubscribe from agent's response and error channels
	if subs, ok := g.agentSubs.Get(id); ok {
		if subs.responses != nil {
			subs.responses.Unsubscribe()
		}
		if subs.errors != nil {
			subs.errors.Unsubscribe()
		}
		g.agentSubs.Delete(id)
	}

	// Remove agent channels and ready state
	g.agentChannels.Delete(id)
	g.readyAgents.Delete(id)

	// Unregister from health monitor and circuit breakers
	g.health.Unregister(id)
	g.circuits.Remove(id)

	// Invalidate route cache entries for this agent
	g.routeCache.InvalidateForAgent(id)

	// Unregister from routing and registry
	g.routing.UnregisterAgent(id)
	g.registry.Unregister(id)

	// Publish unregistration announcement to event bus
	if g.bus != nil && g.running {
		msg := NewAgentUnregisteredMessage(generateMessageID(), id, agentName)
		_ = g.bus.Publish(TopicAgentRegistry, msg)
	}
}

// RegisterAgent registers an agent with just its capabilities (legacy).
// For full registration including shortcuts and triggers, use Register().
func (g *Guide) RegisterAgent(registration *AgentRegistration) {
	g.registry.Register(registration)
}

// UnregisterAgent removes an agent from the guide (legacy alias)
func (g *Guide) UnregisterAgent(id string) {
	g.Unregister(id)
}

// GetAgent retrieves an agent registration by ID
func (g *Guide) GetAgent(id string) *AgentRegistration {
	return g.registry.Get(id)
}

// GetAgentByName retrieves an agent registration by name or alias
func (g *Guide) GetAgentByName(name string) *AgentRegistration {
	return g.registry.GetByName(name)
}

// GetAllAgents returns all registered agents
func (g *Guide) GetAllAgents() []*AgentRegistration {
	return g.registry.GetAll()
}

// GetRoutingInfo returns routing info for an agent
func (g *Guide) GetRoutingInfo(agentID string) *AgentRoutingInfo {
	return g.routing.GetRoutingInfo(agentID)
}

// =============================================================================
// Trigger Detection
// =============================================================================

// DetectTrigger analyzes input and returns a routing recommendation.
// Uses registered agent triggers for detection.
func (g *Guide) DetectTrigger(input string) *TriggerResult {
	return g.triggers.Detect(input)
}

// ShouldRoute returns true if the input should be routed through the Guide
func (g *Guide) ShouldRoute(input string) bool {
	return g.triggers.Detect(input).ShouldRoute
}

// =============================================================================
// DSL Convenience Methods
// =============================================================================

// RouteToAgent creates a DSL command for routing to any agent
func RouteToAgent(agent string, intent Intent, domain Domain, params map[string]string) string {
	cmd := "@" + agent + ":" + string(intent) + ":" + string(domain)

	if len(params) > 0 {
		cmd += "?"
		first := true
		for k, v := range params {
			if !first {
				cmd += "&"
			}
			cmd += k + "=" + v
			first = false
		}
	}

	return cmd
}

// RouteToGuide creates a DSL command for guide queries
func RouteToGuide(intent Intent, domain Domain) string {
	return RouteToAgent("guide", intent, domain, nil)
}

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// Must be called after creating the Guide to enable message routing.
func (g *Guide) Start(ctx context.Context) error {
	if g.running {
		return fmt.Errorf("guide is already running")
	}

	// Subscribe to Guide's own request channel (guide.requests)
	requestSub, err := g.bus.SubscribeAsync(TopicGuideRequests, g.handleRequestMessage)
	if err != nil {
		return fmt.Errorf("failed to subscribe to guide.requests: %w", err)
	}
	g.requestSub = requestSub

	// Start resilience components
	g.pendingCleanup.Start()
	g.health.Start(ctx)

	g.running = true
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (g *Guide) Stop() error {
	if !g.running {
		return nil
	}

	err := g.stopComponents()
	g.running = false
	if err != nil {
		return err
	}
	return nil
}

func (g *Guide) stopComponents() error {
	var errs []error
	g.stopResilience()
	g.collectUnsubscribeErrors(&errs)
	return g.stopError(errs)
}

func (g *Guide) stopResilience() {
	g.pendingCleanup.Stop()
	g.health.Stop()
}

func (g *Guide) collectUnsubscribeErrors(errs *[]error) {
	g.unsubscribeRequest(errs)
	g.unsubscribeAgentChannels(errs)
}

func (g *Guide) unsubscribeRequest(errs *[]error) {
	if g.requestSub == nil {
		return
	}
	if err := g.requestSub.Unsubscribe(); err != nil {
		*errs = append(*errs, err)
	}
	g.requestSub = nil
}

func (g *Guide) unsubscribeAgentChannels(errs *[]error) {
	g.agentSubs.Range(func(agentID string, subs *agentSubscriptions) bool {
		g.unsubscribeAgentSubs(errs, subs)
		g.agentSubs.Delete(agentID)
		return true
	})
}

func (g *Guide) unsubscribeAgentSubs(errs *[]error, subs *agentSubscriptions) {
	if subs.responses != nil {
		if err := subs.responses.Unsubscribe(); err != nil {
			*errs = append(*errs, err)
		}
	}
	if subs.errors != nil {
		if err := subs.errors.Unsubscribe(); err != nil {
			*errs = append(*errs, err)
		}
	}
}

func (g *Guide) stopError(errs []error) error {
	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}
	return nil
}

// IsRunning returns true if the Guide is actively processing messages
func (g *Guide) IsRunning() bool {
	return g.running
}

// Bus returns the event bus used by the Guide
func (g *Guide) Bus() EventBus {
	return g.bus
}

// RouteCache returns the Guide's route cache
func (g *Guide) RouteCache() *RouteCache {
	return g.routeCache
}

// RouteCacheStats returns statistics about the route cache
func (g *Guide) RouteCacheStats() RouteCacheStats {
	return g.routeCache.Stats()
}

// handleRequestMessage processes incoming request messages from the event bus
func (g *Guide) handleRequestMessage(msg *Message) error {
	if msg.Type != MessageTypeRequest {
		return nil
	}

	req, ok := msg.GetRouteRequest()
	if !ok {
		return fmt.Errorf("invalid request payload")
	}

	forwarded, err := g.Route(context.Background(), req)
	if err != nil {
		return g.publishRouteError(msg.CorrelationID, req.SourceAgentID, err)
	}

	pending := g.pending.Get(forwarded.CorrelationID)
	if pending == nil {
		return fmt.Errorf("no pending request found for correlation ID: %s", forwarded.CorrelationID)
	}

	return g.publishForwardedRequest(pending.TargetAgentID, forwarded)
}

func (g *Guide) handleResponseMessage(msg *Message) error {
	switch msg.Type {
	case MessageTypeResponse:
		resp, ok := msg.GetRouteResponse()
		if !ok {
			return fmt.Errorf("invalid response payload")
		}

		pending, err := g.HandleResponse(context.Background(), resp)
		if err != nil {
			return nil
		}

		return g.publishResponseToSource(pending.SourceAgentID, resp)
	case MessageTypeStream:
		streamResp, ok := msg.GetStreamResponse()
		if !ok {
			return fmt.Errorf("invalid stream response payload")
		}
		pending := g.pending.Get(streamResp.CorrelationID)
		if pending == nil {
			return nil
		}
		streamResp.TargetAgentID = pending.SourceAgentID
		streamResp.RespondingAgentID = pending.TargetAgentID
		return g.publishStreamToSource(pending.SourceAgentID, streamResp)
	default:
		return nil
	}
}

// handleErrorMessage processes incoming error messages from agent error channels
func (g *Guide) handleErrorMessage(msg *Message) error {
	if msg.Type != MessageTypeError {
		return nil
	}

	errStr, ok := msg.GetError()
	if !ok {
		return fmt.Errorf("invalid error payload")
	}

	resp := g.routeResponseFromError(msg, errStr)
	pending, err := g.HandleResponse(context.Background(), resp)
	if err != nil {
		return nil
	}

	return g.publishErrorToSource(pending.SourceAgentID, msg.CorrelationID, msg.SourceAgentID, errStr)
}

func (g *Guide) publishRouteError(correlationID string, sourceAgentID string, err error) error {
	errMsg := NewErrorMessage(generateMessageID(), correlationID, g.agentID, err.Error())
	return g.bus.Publish(TopicResponses(sourceAgentID), errMsg)
}

func (g *Guide) publishForwardedRequest(targetAgentID string, forwarded *ForwardedRequest) error {
	fwdMsg := g.forwardMessage(targetAgentID, forwarded)
	return g.bus.Publish(TopicRequests(targetAgentID), fwdMsg)
}

func (g *Guide) forwardMessage(targetAgentID string, forwarded *ForwardedRequest) *Message {
	fwdMsg := NewForwardMessage(generateMessageID(), forwarded)
	fwdMsg.TargetAgentID = targetAgentID
	return fwdMsg
}

func (g *Guide) publishResponseToSource(sourceAgentID string, resp *RouteResponse) error {
	respMsg := NewResponseMessage(generateMessageID(), resp)
	return g.bus.Publish(TopicResponses(sourceAgentID), respMsg)
}

func (g *Guide) publishStreamToSource(sourceAgentID string, resp *StreamResponse) error {
	msg := &Message{
		ID:            generateMessageID(),
		CorrelationID: resp.CorrelationID,
		Type:          MessageTypeStream,
		Payload:       resp,
		SourceAgentID: resp.RespondingAgentID,
		TargetAgentID: sourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
	return g.bus.Publish(TopicResponses(sourceAgentID), msg)
}

func (g *Guide) publishErrorToSource(sourceAgentID string, correlationID string, sourceAgent string, errStr string) error {
	errMsg := NewErrorMessage(generateMessageID(), correlationID, sourceAgent, errStr)
	return g.bus.Publish(TopicResponses(sourceAgentID), errMsg)
}

func (g *Guide) routeResponseFromError(msg *Message, errStr string) *RouteResponse {
	return &RouteResponse{
		CorrelationID:     msg.CorrelationID,
		Success:           false,
		Error:             errStr,
		RespondingAgentID: msg.SourceAgentID,
	}
}

// PublishRequest publishes a route request to the event bus.
// This is the primary way agents send requests through the Guide.
func (g *Guide) PublishRequest(req *RouteRequest) error {
	if !g.running {
		return fmt.Errorf("guide is not running")
	}

	msg := NewRequestMessage(generateMessageID(), req)
	return g.bus.Publish(TopicGuideRequests, msg)
}

// GetAgentChannels returns the channels for a registered agent
func (g *Guide) GetAgentChannels(agentID string) *AgentChannels {
	channels, _ := g.agentChannels.Get(agentID)
	return channels
}

// GetAllAgentChannels returns channels for all registered agents
func (g *Guide) GetAllAgentChannels() map[string]*AgentChannels {
	return g.agentChannels.Snapshot()
}

// MarkAgentReady marks an agent as ready to receive requests.
// Called when an agent announces it has completed initialization.
func (g *Guide) MarkAgentReady(agentID string) {
	g.readyAgents.Set(agentID, true)
}

// IsAgentReady returns true if an agent is ready to receive requests
func (g *Guide) IsAgentReady(agentID string) bool {
	ready, _ := g.readyAgents.Get(agentID)
	return ready
}

// Stats returns Guide statistics including resilience component stats
func (g *Guide) Stats() GuideStats {
	pendingStats := g.pending.Stats()
	return GuideStats{
		RegisteredAgents: len(g.registry.GetAll()),
		ReadyAgents:      g.countReadyAgents(),
		PendingRequests:  pendingStats.TotalPending,
		CacheStats:       g.routeCache.Stats(),
		CircuitStats:     g.circuits.Stats(),
		HealthStats:      g.health.Stats(),
		DLQStats:         g.dlq.Stats(),
		SkillStats:       g.skills.Stats(),
		HookStats:        g.hooks.Stats(),
	}
}

func (g *Guide) countReadyAgents() int {
	count := 0
	g.readyAgents.Range(func(agentID string, ready bool) bool {
		if ready {
			count++
		}
		return true
	})
	return count
}

// GuideStats contains Guide statistics
type GuideStats struct {
	RegisteredAgents int                            `json:"registered_agents"`
	ReadyAgents      int                            `json:"ready_agents"`
	PendingRequests  int                            `json:"pending_requests"`
	CacheStats       RouteCacheStats                `json:"cache"`
	CircuitStats     map[string]CircuitBreakerStats `json:"circuits"`
	HealthStats      HealthMonitorStats             `json:"health"`
	DLQStats         DeadLetterStats                `json:"dlq"`
	SkillStats       skills.Stats                   `json:"skills"`
	HookStats        skills.HookStats               `json:"hooks"`
}

// generateMessageID creates a unique message ID
func generateMessageID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}

// SubscribeToRegistry subscribes to agent registration/unregistration events.
// Returns a subscription that can be used to unsubscribe.
// Handlers receive AgentAnnouncement payloads for both registered and unregistered events.
func (g *Guide) SubscribeToRegistry(handler MessageHandler) (Subscription, error) {
	if g.bus == nil {
		return nil, fmt.Errorf("event bus not configured")
	}
	return g.bus.SubscribeAsync(TopicAgentRegistry, handler)
}

// GetRegisteredAgentAnnouncements returns announcements for all currently registered agents.
// Useful for new agents to catch up on the current state of the agent ecosystem.
func (g *Guide) GetRegisteredAgentAnnouncements() []*AgentAnnouncement {
	agents := g.registry.GetAll()
	announcements := make([]*AgentAnnouncement, 0, len(agents))

	for _, reg := range agents {
		info := g.routing.GetRoutingInfo(reg.ID)
		ann := &AgentAnnouncement{
			AgentID:      reg.ID,
			AgentName:    reg.Name,
			Aliases:      reg.Aliases,
			Description:  reg.Description,
			Capabilities: &reg.Capabilities,
			Constraints:  &reg.Constraints,
		}
		if info != nil {
			ann.ActionShortcuts = info.ActionShortcuts
		}
		announcements = append(announcements, ann)
	}

	return announcements
}

// =============================================================================
// Skills and Hooks API
// =============================================================================

// Skills returns the Guide's skill registry
func (g *Guide) Skills() *skills.Registry {
	return g.skills
}

// SkillLoader returns the Guide's skill loader
func (g *Guide) SkillLoader() *skills.Loader {
	return g.skillLoader
}

// Hooks returns the Guide's hook registry
func (g *Guide) Hooks() *skills.HookRegistry {
	return g.hooks
}

// RegisterSkill registers a skill with the Guide's skill registry
func (g *Guide) RegisterSkill(skill *skills.Skill) error {
	return g.skills.Register(skill)
}

// LoadSkillsForInput loads skills based on input keywords
// Returns the list of skills that were loaded
func (g *Guide) LoadSkillsForInput(input string) []string {
	return g.skillLoader.LoadForInput(input)
}

// LoadSkillDomain loads all skills in a domain
func (g *Guide) LoadSkillDomain(domain string) (int, bool) {
	return g.skillLoader.LoadDomain(domain)
}

// GetLoadedSkillDefinitions returns tool definitions for all loaded skills
// These can be passed to the Anthropic API as tools
func (g *Guide) GetLoadedSkillDefinitions() []map[string]any {
	return g.skills.GetToolDefinitions()
}

// RegisterPrePromptHook registers a hook that runs before LLM prompts
func (g *Guide) RegisterPrePromptHook(name string, priority skills.HookPriority, fn skills.PromptHookFunc) {
	g.hooks.RegisterPrePromptHook(name, priority, fn)
}

// RegisterPostPromptHook registers a hook that runs after LLM responses
func (g *Guide) RegisterPostPromptHook(name string, priority skills.HookPriority, fn skills.PromptHookFunc) {
	g.hooks.RegisterPostPromptHook(name, priority, fn)
}

// RegisterPreToolCallHook registers a hook that runs before tool/skill calls
func (g *Guide) RegisterPreToolCallHook(name string, priority skills.HookPriority, fn skills.ToolCallHookFunc) {
	g.hooks.RegisterPreToolCallHook(name, priority, fn)
}

// RegisterPostToolCallHook registers a hook that runs after tool/skill calls
func (g *Guide) RegisterPostToolCallHook(name string, priority skills.HookPriority, fn skills.ToolCallHookFunc) {
	g.hooks.RegisterPostToolCallHook(name, priority, fn)
}

// ExecutePrePromptHooks runs all pre-prompt hooks
func (g *Guide) ExecutePrePromptHooks(ctx context.Context, data *skills.PromptHookData) (*skills.PromptHookData, error) {
	return g.hooks.ExecutePrePromptHooks(ctx, data)
}

// ExecutePostPromptHooks runs all post-prompt hooks
func (g *Guide) ExecutePostPromptHooks(ctx context.Context, data *skills.PromptHookData) (*skills.PromptHookData, error) {
	return g.hooks.ExecutePostPromptHooks(ctx, data)
}

// OptimizeSkillsForBudget unloads skills to fit within token budget
func (g *Guide) OptimizeSkillsForBudget() int {
	return g.skillLoader.OptimizeForBudget()
}

// LoadSkillsForContext performs context-aware skill loading
func (g *Guide) LoadSkillsForContext(ctx skills.LoadContext) skills.LoadResult {
	return g.skillLoader.LoadForContext(ctx)
}
