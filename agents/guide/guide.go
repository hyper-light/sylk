package guide

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	corecontext "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"golang.org/x/sync/singleflight"
)

const defaultAutoUpgradeInterval = 15 * time.Second

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
	// Session-aware router with per-session caches/preferences
	sessionRouter *SessionRouter
	// Domain classifier cascade used for pre-classification hints
	domainClassifier *DomainClassifier
	// Versioned route store for auditability and rollback
	routeVersions *RouteVersionStore
	// Optional enrichment fanout before forwarding requests
	enrichment *EnrichmentService
	// Local stream manager for real-time routing/response status
	streams *StreamManager

	// Agent channels - tracks channel names for each agent
	agentChannels *ShardedMap[string, *AgentChannels]

	// Ready agents - agents that have completed initialization
	readyAgents *ShardedMap[string, bool]

	// Resilience components
	circuits       *CircuitBreakerRegistry
	health         *HealthMonitor
	dlq            *DeadLetterQueue
	pendingCleanup *PendingCleanup
	observer       *ConsultationObserver

	// LLM Skills and Hooks
	skills        *skills.Registry
	skillLoader   *skills.Loader
	hooks         *skills.HookRegistry
	selfResponder GuideSelfResponder
	selfMu        sync.RWMutex

	// Session metadata
	sessionID string
	agentID   string
	// Session-scoped conversation flow tracking for interactive handoffs.
	conversation *ConversationFlowManager

	// Handoff bridge for context-exhaustion lifecycle management
	handoffBridge *handoff.HandoffBridge

	// Container activation hook — called before forwarding a request to
	// an agent, triggering the ActivationController to ensure the target
	// agent's container is hot.
	activationHook func(ctx context.Context, agentType string) error

	// Container activity hook — called after successful activation and on
	// response handling to reset the idle timer, preventing active
	// conversation agents from being demoted.
	touchActivityHook func(agentType string)

	// Service registry for health-aware routing. When set, isAgentHealthy
	// uses healthy endpoints from the registry instead of the local
	// readyAgents map.
	serviceRegistry ServiceHealthChecker

	// Quality-aware routing for weighted endpoint selection during overlap.
	qualityChecker  ServiceQualityChecker
	qualitySelector *QualityAwareSelector

	// Self-managed Google provider lifecycle
	googleConfig   *providers.GoogleConfig
	googleProvider *providers.GoogleProvider
	providerMu     sync.RWMutex
	autoUpgradeWg  sync.WaitGroup

	// Running state
	running bool

	runMu     sync.RWMutex
	runCtx    context.Context
	runCancel context.CancelFunc

	requestCancelMu sync.Mutex
	requestCancels  map[string]context.CancelFunc

	classifyGroup singleflight.Group
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

	// Optional self-response handler for requests explicitly routed to guide.
	// If nil, the Guide uses the default static responder.
	SelfResponder GuideSelfResponder

	// Optional domain-classifier configuration.
	DomainClassifierConfig *DomainClassifierConfig

	// Optional local stream-manager configuration.
	StreamConfig *StreamConfig

	// Optional conversation-flow tuning for sticky follow-ups and completion signals.
	ConversationFlowConfig *ConversationFlowConfig

	// GoogleConfig enables self-managed provider lifecycle within the Guide.
	// When set, RefreshAuth() rebuilds the classifier and responder from this config,
	// and Start() auto-upgrades from rule-based to Gemini when credentials appear.
	GoogleConfig *providers.GoogleConfig

	// AutoUpgradeInterval controls the retry interval for background auto-upgrade
	// from rule-based to Gemini classification. 0 uses defaultAutoUpgradeInterval.
	AutoUpgradeInterval time.Duration
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

	skillsRegistry, loaderCfg, hookRegistry := buildSkills(cfg)
	selfResponder := resolveGuideSelfResponder(cfg, nil, nil)

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
		observer:       NewConsultationObserver(cfg.Bus, cfg.SessionID),
		skills:         skillsRegistry,
		skillLoader:    nil,
		hooks:          hookRegistry,
		selfResponder:  selfResponder,
		sessionID:      cfg.SessionID,
		agentID:        agentID,
		requestCancels: make(map[string]context.CancelFunc),
	}

	guide.registerCoreSkills()
	guide.registerExtendedSkills()
	guide.skillLoader = skills.NewLoader(guide.skills, loaderCfg)
	if err := guide.initRuntimeExtensions(cfg); err != nil {
		return nil, err
	}

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

func buildSkills(cfg Config) (*skills.Registry, skills.LoaderConfig, *skills.HookRegistry) {
	skillsRegistry := skills.NewRegistry()
	skillsLoaderCfg := guideSkillsLoaderConfig(cfg)
	skillsLoaderCfg.CoreSkills = []string{"route", "clarify", "guide_route", "help", "status", "agents", "self_diagnostic"}
	skillsLoaderCfg.AutoLoadDomains = nil

	hooks := skills.NewHookRegistry()

	// CRITICAL SAFETY CATCH
	// The Guide must NEVER be able to execute generic state-mutating tools like write_file or run_shell_command
	// even if they somehow leak into its context. We explicitly whitelist its approved tools.
	allowedTools := map[string]bool{
		"route": true, "clarify": true, "guide_route": true, "help": true, "status": true,
		"agents": true, "conversation_context": true, "route_to": true, "reply_to": true, "broadcast": true,
		"task_interact": true, "get_routing_history": true, "get_agent_capabilities": true,
		"self_diagnostic": true,
		"sessions": true, "metrics": true, "switch_session": true, "create_session": true, "close_session": true,
	}

	hooks.RegisterPreToolCallHook("guide_safety_catch", skills.HookPriorityHigh, func(ctx context.Context, data *skills.ToolCallHookData) skills.HookResult {
		if !allowedTools[data.ToolName] {
			return skills.HookResult{
				Continue: false,
				Error:    fmt.Errorf("SECURITY VIOLATION: Guide agent is not permitted to execute tool %q", data.ToolName),
			}
		}
		return skills.HookResult{Continue: true}
	})

	return skillsRegistry, skillsLoaderCfg, hooks
}

func guideSkillsLoaderConfig(cfg Config) skills.LoaderConfig {
	skillsLoaderCfg := skills.DefaultLoaderConfig()
	if cfg.SkillsConfig != nil {
		return *cfg.SkillsConfig
	}
	skillsLoaderCfg.MaxLoadedSkills = 10
	skillsLoaderCfg.TokenBudget = 2600
	return skillsLoaderCfg
}

// NewWithClassifier creates a new Guide agent with a custom ClassifierClient.
// This allows creating a Guide without a real LLM client (e.g., for mock/test mode).
func NewWithClassifier(client ClassifierClient, cfg Config) (*Guide, error) {
	cfg = ensureRouterConfig(cfg)
	if err := validateBus(cfg); err != nil {
		return nil, err
	}

	registry := resolveRegistry(cfg)
	routing := NewRoutingAggregator()
	routing.RegisterAgent(GuideRoutingInfo())

	parser := NewParserWithRouting(cfg.RouterConfig.DSLPrefix, routing)
	classifier := NewClassifierWithClient(client, cfg.RouterConfig)
	router := &Router{
		config:            cfg.RouterConfig,
		parser:            parser,
		classifier:        classifier,
		riskSampler:       NewRiskSampler(),
		crossDomainRouter: NewCrossDomainRouter(nil),
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

	skillsRegistry, loaderCfg, hookRegistry := buildSkills(cfg)
	selfResponder := resolveGuideSelfResponder(cfg, nil, nil)

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
		observer:       NewConsultationObserver(cfg.Bus, cfg.SessionID),
		skills:         skillsRegistry,
		skillLoader:    nil,
		hooks:          hookRegistry,
		selfResponder:  selfResponder,
		googleConfig:   cfg.GoogleConfig,
		sessionID:      cfg.SessionID,
		agentID:        agentID,
		requestCancels: make(map[string]context.CancelFunc),
	}

	guide.registerCoreSkills()
	guide.registerExtendedSkills()
	guide.skillLoader = skills.NewLoader(guide.skills, loaderCfg)
	if err := guide.initRuntimeExtensions(cfg); err != nil {
		return nil, err
	}

	return guide, nil
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

// NewWithGeminiClient creates a new Guide agent with a Google provider.
func NewWithGeminiClient(provider *providers.GoogleProvider, cfg Config) (*Guide, error) {
	cfg = ensureRouterConfig(cfg)
	if err := validateBus(cfg); err != nil {
		return nil, err
	}

	registry := resolveRegistry(cfg)
	routing := NewRoutingAggregator()
	routing.RegisterAgent(GuideRoutingInfo())

	parser := NewParserWithRouting(cfg.RouterConfig.DSLPrefix, routing)
	classifier := NewGeminiClassifier(provider, cfg.RouterConfig)
	router := &Router{
		config:            cfg.RouterConfig,
		parser:            parser,
		classifier:        classifier,
		riskSampler:       NewRiskSampler(),
		crossDomainRouter: NewCrossDomainRouter(nil),
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

	skillsRegistry, loaderCfg, hookRegistry := buildSkills(cfg)
	selfResponder := resolveGuideSelfResponder(cfg, provider, nil)

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
		observer:       NewConsultationObserver(cfg.Bus, cfg.SessionID),
		skills:         skillsRegistry,
		skillLoader:    nil,
		hooks:          hookRegistry,
		selfResponder:  selfResponder,
		googleConfig:   cfg.GoogleConfig,
		googleProvider: provider,
		sessionID:      cfg.SessionID,
		agentID:        agentID,
		requestCancels: make(map[string]context.CancelFunc),
	}

	guide.registerCoreSkills()
	guide.registerExtendedSkills()
	guide.skillLoader = skills.NewLoader(guide.skills, loaderCfg)
	if cfg.SelfResponder == nil {
		guide.selfResponder = resolveGuideSelfResponder(cfg, provider, guide)
	}
	if err := guide.initRuntimeExtensions(cfg); err != nil {
		return nil, err
	}

	return guide, nil
}

func (g *Guide) initRuntimeExtensions(cfg Config) error {
	g.sessionRouter = NewSessionRouter(g)
	g.routeVersions = NewRouteVersionStore(g.routeCache)
	g.enrichment = NewEnrichmentService(g.bus, g.registry, g.health)
	g.streams = newGuideStreamManager(cfg.StreamConfig)
	g.conversation = newGuideConversationFlow(cfg.ConversationFlowConfig)
	domainClassifier, err := newGuideDomainClassifier(cfg.DomainClassifierConfig)
	if err != nil {
		return err
	}
	g.domainClassifier = domainClassifier
	return nil
}

func newGuideStreamManager(cfg *StreamConfig) *StreamManager {
	if cfg == nil {
		return NewStreamManager(DefaultStreamConfig())
	}
	return NewStreamManager(*cfg)
}

func newGuideDomainClassifier(cfg *DomainClassifierConfig) (*DomainClassifier, error) {
	if cfg == nil {
		return NewDomainClassifier(nil)
	}
	return NewDomainClassifier(cfg)
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
	g.prepareSkillsForRouting(request)

	classification, targetAgentID, err := g.resolveClassification(ctx, request)
	if err != nil {
		return nil, err
	}

	corrID := g.resolveCorrelationID(request)
	if !request.FireAndForget {
		corrID = g.pending.Add(request, classification, targetAgentID)
	}

	forwarded := g.buildForwardedRequest(request, classification, corrID)
	g.attachEnrichment(ctx, request, classification, forwarded)
	return forwarded, nil
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
	g.observeUserConversationSignal(request)
	ctx = g.augmentClassificationContext(ctx, request)

	// Fast-path: when an active conversation agent has high ACT-R activation
	// and no explicit target was specified, skip LLM classification entirely.
	if !request.ExplicitTarget && request.TargetAgentID == "" {
		if result, target, ok := g.tryConversationFastPath(request); ok {
			return result, target, nil
		}
	}

	if request.TargetAgentID != "" {
		classification := g.explicitTargetClassification(request.TargetAgentID)
		classification.Domain = g.supportedDomainForTarget(request.TargetAgentID, classification.Domain, classification.Intent)
		classification, targetAgentID := g.ensureRoutableClassification(classification, request.TargetAgentID)
		// Explicit non-guide targets should bypass conversation-flow remapping.
		// Explicit "guide" requests still represent "route this through guide",
		// so follow-up continuity should remain active.
		if request.ExplicitTarget && !explicitGuideFollowupAllowed(request.TargetAgentID) {
			g.observeRoutedConversationTarget(request, targetAgentID)
		} else if explicitGuideFollowupAllowed(request.TargetAgentID) {
			classification, targetAgentID = g.applyConversationFlow(request, classification, targetAgentID)
		} else {
			g.observeRoutedConversationTarget(request, targetAgentID)
		}
		return classification, targetAgentID, nil
	}

	domainCtx := g.preclassifyDomain(ctx, request)
	if request.SessionID != "" && g.sessionRouter != nil {
		classification, targetAgentID, err := g.classifyWithSingleflight(ctx, request, domainCtx, true)
		if err != nil {
			return nil, "", err
		}
		classification, targetAgentID = g.finalizeClassificationWithExclude(ctx, request.Input, classification, targetAgentID)
		classification, targetAgentID = g.applyConversationFlow(request, classification, targetAgentID)
		return classification, targetAgentID, nil
	}

	classification, targetAgentID, ok := g.cachedClassification(request)
	if ok {
		classification = g.applyDomainHints(classification, domainCtx)
		classification, targetAgentID = g.finalizeClassificationWithExclude(ctx, request.Input, classification, targetAgentID)
		classification, targetAgentID = g.applyConversationFlow(request, classification, targetAgentID)
		return classification, targetAgentID, nil
	}

	classification, targetAgentID, err := g.classifyWithSingleflight(ctx, request, domainCtx, false)
	if err != nil {
		return nil, "", err
	}
	classification, targetAgentID = g.finalizeClassificationWithExclude(ctx, request.Input, classification, targetAgentID)
	classification, targetAgentID = g.applyConversationFlow(request, classification, targetAgentID)
	return classification, targetAgentID, nil
}

// tryConversationFastPath checks whether the conversation flow manager has a
// strong enough active agent to skip LLM classification entirely. Returns a
// synthetic RouteResult, the target agent ID, and true when the fast-path fires.
func (g *Guide) tryConversationFastPath(request *RouteRequest) (*RouteResult, string, bool) {
	if g == nil || g.conversation == nil || request == nil {
		return nil, "", false
	}
	activeAgentID, ok := g.conversation.IsActiveForFastPath(request.SessionID)
	if !ok {
		return nil, "", false
	}
	if g.isGuideTarget(activeAgentID) {
		return nil, "", false
	}
	if g.registry != nil && g.registry.Get(activeAgentID) == nil {
		return nil, "", false
	}
	result := &RouteResult{
		TargetAgent:          TargetAgent(activeAgentID),
		Intent:               IntentChat,
		Domain:               DomainGeneral,
		Confidence:           0.85,
		Action:               RouteActionExecute,
		ClassificationMethod: "conversation_fast_path",
		Reason:               "active conversation agent with high activation score",
	}
	result.Intent = g.supportedIntentForTarget(activeAgentID, result.Intent)
	result.Domain = g.supportedDomainForTarget(activeAgentID, result.Domain, result.Intent)
	g.observeRoutedConversationTarget(request, activeAgentID)
	return result, activeAgentID, true
}

func (g *Guide) explicitTargetClassification(targetAgentID string) *RouteResult {
	target := strings.TrimSpace(targetAgentID)
	return &RouteResult{
		TargetAgent:          TargetAgent(target),
		Intent:               IntentHelp,
		Domain:               DomainGeneral,
		Confidence:           1.0,
		Action:               RouteActionExecute,
		ClassificationMethod: "explicit",
		Reason:               "explicit target requested",
	}
}

func explicitGuideFollowupAllowed(targetAgentID string) bool {
	target := strings.ToLower(strings.TrimSpace(targetAgentID))
	return target == "guide" || target == "g"
}

func (g *Guide) augmentClassificationContext(ctx context.Context, request *RouteRequest) context.Context {
	if g == nil || request == nil || g.conversation == nil {
		return ctx
	}
	snapshot, ok := g.conversation.Snapshot(request.SessionID)
	if !ok {
		return ctx
	}
	return withClassificationContext(ctx, ClassificationContext{
		SessionID:               request.SessionID,
		ActiveConversationAgent: snapshot.ActiveAgentID,
		ActiveConversationTurns: snapshot.Turns,
		ActiveConversationAge:   int(snapshot.Age.Seconds()),
		ActiveConversationScore: snapshot.ActivationHint,
	})
}

func (g *Guide) finalizeClassification(input string, classification *RouteResult, targetAgentID string) (*RouteResult, string) {
	classification, targetAgentID = g.ensureRoutableClassification(classification, targetAgentID)
	return g.applyGuidePreferencePolicy(input, classification, targetAgentID)
}

// finalizeClassificationWithExclude wraps finalizeClassification and checks
// the reroute exclude list. If the classified target is excluded, picks the
// next best match from the agent registry.
func (g *Guide) finalizeClassificationWithExclude(
	ctx context.Context,
	input string,
	classification *RouteResult,
	targetAgentID string,
) (*RouteResult, string) {
	classification, targetAgentID = g.finalizeClassification(input, classification, targetAgentID)
	excludeAgents := rerouteExcludeAgentsFromContext(ctx)
	if len(excludeAgents) == 0 {
		return classification, targetAgentID
	}
	if !isExcludedAgent(targetAgentID, excludeAgents) {
		return classification, targetAgentID
	}
	// Target is excluded — try to find an alternative via the registry.
	if g.registry != nil {
		for _, reg := range g.registry.GetAll() {
			if reg.ID == targetAgentID || isExcludedAgent(reg.ID, excludeAgents) {
				continue
			}
			if reg.Accepts(classification) {
				classification.TargetAgent = TargetAgent(reg.ID)
				classification.Reason = "rerouted away from excluded agent " + targetAgentID
				return classification, reg.ID
			}
		}
	}
	// No alternative found — fall back to Guide self-response.
	fallback := g.guideFallbackClassification(classification)
	return fallback, string(fallback.TargetAgent)
}

func isExcludedAgent(agentID string, excludeAgents []string) bool {
	normalized := strings.ToLower(strings.TrimSpace(agentID))
	for _, excluded := range excludeAgents {
		if strings.EqualFold(strings.TrimSpace(excluded), normalized) {
			return true
		}
	}
	return false
}

func (g *Guide) ensureRoutableClassification(classification *RouteResult, targetAgentID string) (*RouteResult, string) {
	target := classificationTargetAgentID(targetAgentID, classification)
	classification = g.normalizeConversationalIntentForTarget(classification, target)
	if g.classificationSupportedByTarget(classification, target) {
		return classification, target
	}
	fallback := g.guideFallbackClassification(classification)
	return fallback, string(fallback.TargetAgent)
}

func (g *Guide) normalizeConversationalIntentForTarget(
	classification *RouteResult,
	targetAgentID string,
) *RouteResult {
	if classification == nil || targetAgentID == "" || g.isGuideTarget(targetAgentID) {
		return classification
	}
	if classification.Intent != IntentChat {
		return classification
	}
	// Keep chat when target explicitly supports it.
	if g.agentSupportsIntent(targetAgentID, IntentChat) {
		return classification
	}
	// Promote chat to help for specialists that support conversational help but
	// do not advertise chat intent.
	if !g.agentSupportsIntent(targetAgentID, IntentHelp) {
		return classification
	}
	promoted := *classification
	promoted.Intent = IntentHelp
	return &promoted
}

func (g *Guide) applyGuidePreferencePolicy(input string, classification *RouteResult, targetAgentID string) (*RouteResult, string) {
	if !preferGuideTargetForClassification(input, classification, targetAgentID) {
		return classification, targetAgentID
	}
	// If the classifier says IntentChat but the target agent supports
	// IntentHelp (conversational), promote the intent and forward instead
	// of redirecting to guide. This lets agents like the architect handle
	// substantive conversations without being blocked by the chat policy.
	if classification.Intent == IntentChat && g.agentSupportsIntent(targetAgentID, IntentHelp) {
		promoted := *classification
		promoted.Intent = IntentHelp
		return &promoted, targetAgentID
	}
	fallback := g.guideFallbackClassification(classification)
	return fallback, string(fallback.TargetAgent)
}

func preferGuideTargetForClassification(input string, classification *RouteResult, targetAgentID string) bool {
	if classification == nil || targetAgentID == "" {
		return false
	}
	if strings.EqualFold(targetAgentID, "guide") || strings.EqualFold(targetAgentID, "g") {
		return false
	}
	if classification.Intent == IntentChat {
		return true
	}
	return isGuideSystemMetaStatus(input, classification)
}

func isGuideSystemMetaStatus(input string, classification *RouteResult) bool {
	if classification == nil || classification.Intent != IntentStatus {
		return false
	}
	query := normalizeGuideQuery(input)
	if query == "" {
		return false
	}
	hasAgentSignal := containsAny(query, "agent", "registry")
	hasGuideSignal := containsAny(query, "guide", "sylk")
	return hasAgentSignal || hasGuideSignal
}

func classificationTargetAgentID(targetAgentID string, classification *RouteResult) string {
	if targetAgentID != "" {
		return targetAgentID
	}
	if classification == nil {
		return ""
	}
	return string(classification.TargetAgent)
}

func (g *Guide) classificationSupportedByTarget(classification *RouteResult, targetAgentID string) bool {
	if classification == nil || targetAgentID == "" {
		return false
	}
	if g.isGuideTarget(targetAgentID) {
		return true
	}
	return g.agentSupportsIntent(targetAgentID, classification.Intent)
}

func (g *Guide) agentSupportsIntent(targetAgentID string, intent Intent) bool {
	if g == nil || g.registry == nil || intent == "" || intent == IntentUnknown {
		return false
	}
	agent := g.registry.Get(targetAgentID)
	if agent == nil {
		return false
	}
	return agent.Capabilities.SupportsIntent(intent)
}

func (g *Guide) agentSupportsDomain(targetAgentID string, domain Domain) bool {
	if g == nil || g.registry == nil || domain == "" || domain == DomainUnknown {
		return false
	}
	agent := g.registry.Get(targetAgentID)
	if agent == nil {
		return false
	}
	return agent.Capabilities.SupportsDomain(domain)
}

func (g *Guide) supportedDomainForTarget(targetAgentID string, current Domain, intent Intent) Domain {
	if strings.TrimSpace(targetAgentID) == "" || g.isGuideTarget(targetAgentID) {
		return current
	}
	if g.agentSupportsDomain(targetAgentID, current) {
		return current
	}
	candidates := candidateDomainsForIntent(intent)
	for _, candidate := range candidates {
		if g.agentSupportsDomain(targetAgentID, candidate) {
			return candidate
		}
	}
	if g == nil || g.registry == nil {
		return current
	}
	agent := g.registry.Get(targetAgentID)
	if agent == nil || len(agent.Capabilities.Domains) == 0 {
		return current
	}
	return agent.Capabilities.Domains[0]
}

func candidateDomainsForIntent(intent Intent) []Domain {
	switch intent {
	case IntentPlan, IntentDesign:
		return []Domain{DomainDesign, DomainTasks, DomainGeneral}
	case IntentCheck:
		return []Domain{DomainCode, DomainDesign, DomainGeneral}
	case IntentRecall:
		return []Domain{DomainHistory, DomainGeneral}
	case IntentFind, IntentSearch:
		return []Domain{DomainLocal, DomainCode, DomainGeneral}
	case IntentStatus:
		return []Domain{DomainSystem, DomainGeneral}
	default:
		return []Domain{DomainGeneral, DomainSystem}
	}
}

func (g *Guide) guideFallbackClassification(classification *RouteResult) *RouteResult {
	if classification == nil {
		return &RouteResult{
			Intent:               IntentHelp,
			Domain:               DomainGeneral,
			TargetAgent:          g.guideTargetAgent(),
			Confidence:           0.51,
			Action:               RouteActionExecute,
			ClassificationMethod: "guide_fallback",
			Reason:               "routing fallback to guide for clarification",
		}
	}
	fallback := *classification
	fallback.Intent = guideFallbackIntent(classification.Intent)
	fallback.Domain = guideFallbackDomain(classification.Domain, fallback.Intent)
	fallback.TargetAgent = g.guideTargetAgent()
	fallback.Rejected = false
	fallback.Action = RouteActionExecute
	fallback.Reason = "routing fallback to guide for clarification"
	if fallback.Confidence <= 0 {
		fallback.Confidence = 0.51
	}
	fallback.ClassificationMethod = fallbackClassificationMethod(fallback.ClassificationMethod)
	return &fallback
}

func guideFallbackIntent(current Intent) Intent {
	switch current {
	case IntentChat:
		return IntentChat
	case IntentStatus:
		return IntentStatus
	default:
		return IntentHelp
	}
}

func guideFallbackDomain(current Domain, intent Intent) Domain {
	if intent == IntentStatus {
		return DomainSystem
	}
	if current == DomainGeneral {
		return DomainGeneral
	}
	return DomainGeneral
}

func fallbackClassificationMethod(current string) string {
	if current == "" {
		return "guide_fallback"
	}
	return current + "+guide_fallback"
}

func (g *Guide) guideTargetAgent() TargetAgent {
	if g != nil && g.agentID != "" {
		return TargetAgent(g.agentID)
	}
	return TargetGuide
}

type classificationTuple struct {
	result *RouteResult
	target string
}

func (g *Guide) classifyWithSingleflight(
	ctx context.Context,
	request *RouteRequest,
	domainCtx *domain.DomainContext,
	sessionAware bool,
) (*RouteResult, string, error) {
	if g == nil {
		return nil, "", fmt.Errorf("guide is nil")
	}
	key := classificationSingleflightKey(request, sessionAware)
	value, err, _ := g.classifyGroup.Do(key, func() (any, error) {
		return g.classifyByMode(ctx, request, domainCtx, sessionAware)
	})
	if err != nil {
		return nil, "", err
	}
	tuple, ok := value.(*classificationTuple)
	if !ok || tuple == nil {
		return nil, "", fmt.Errorf("invalid classification result")
	}
	return tuple.result, tuple.target, nil
}

func (g *Guide) classifyByMode(
	ctx context.Context,
	request *RouteRequest,
	domainCtx *domain.DomainContext,
	sessionAware bool,
) (any, error) {
	if sessionAware {
		result, target, err := g.classifyWithSessionRouter(ctx, request, domainCtx)
		if err != nil {
			return nil, err
		}
		return &classificationTuple{result: result, target: target}, nil
	}
	result, target, err := g.classifyWithRouter(ctx, request, domainCtx)
	if err != nil {
		return nil, err
	}
	return &classificationTuple{result: result, target: target}, nil
}

func classificationSingleflightKey(request *RouteRequest, sessionAware bool) string {
	if request == nil {
		return "classify:nil"
	}
	normalized := normalizeInput(request.Input)
	if sessionAware {
		return "classify:session:" + request.SessionID + ":" + normalized
	}
	return "classify:global:" + normalized
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

func (g *Guide) classifyWithRouter(ctx context.Context, request *RouteRequest, domainCtx *domain.DomainContext) (*RouteResult, string, error) {
	classification, err := g.router.Route(ctx, request)
	if err != nil {
		return nil, "", err
	}

	classification = g.applyDomainHints(classification, domainCtx)
	targetAgentID := string(classification.TargetAgent)
	g.cacheAndBroadcastClassification(request, classification)
	return classification, targetAgentID, nil
}

func (g *Guide) classifyWithSessionRouter(
	ctx context.Context,
	request *RouteRequest,
	domainCtx *domain.DomainContext,
) (*RouteResult, string, error) {
	classification, err := g.sessionRouter.Route(ctx, request.SessionID, request)
	if err != nil {
		return nil, "", err
	}
	classification = g.applyDomainHints(classification, domainCtx)
	g.cacheAndBroadcastClassification(request, classification)
	return classification, string(classification.TargetAgent), nil
}

func (g *Guide) preclassifyDomain(ctx context.Context, request *RouteRequest) *domain.DomainContext {
	if g == nil || g.domainClassifier == nil || request == nil {
		return nil
	}
	if g.router != nil && g.router.IsDSL(request.Input) {
		return nil
	}
	domainCtx, err := g.domainClassifier.Classify(ctx, request.Input, request.SessionID)
	if err != nil {
		return nil
	}
	return domainCtx
}

func (g *Guide) applyDomainHints(result *RouteResult, domainCtx *domain.DomainContext) *RouteResult {
	if result == nil || domainCtx == nil {
		return result
	}
	if result.Rejected {
		return result
	}
	if result.Confidence >= 0.75 && result.TargetAgent != TargetUnknown {
		return result
	}
	hintDomain, hintTarget, ok := mapGuideDomainHint(domainCtx.PrimaryDomain.String())
	if !ok {
		return result
	}
	if result.Domain == DomainUnknown || result.Domain == DomainGeneral {
		result.Domain = hintDomain
	}
	if result.TargetAgent == TargetUnknown {
		result.TargetAgent = hintTarget
	}
	if result.ClassificationMethod == "" {
		result.ClassificationMethod = "domain_hint"
	}
	return result
}

func mapGuideDomainHint(raw string) (Domain, TargetAgent, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "librarian":
		return DomainLocal, TargetLibrarian, true
	case "academic":
		return DomainResearch, TargetAcademic, true
	case "archivalist":
		return DomainHistory, TargetArchivalist, true
	case "architect":
		return DomainPlanning, TargetArchitect, true
	case "orchestrator":
		return DomainSystem, TargetOrchestrator, true
	case "guide":
		return DomainGeneral, TargetGuide, true
	default:
		return DomainUnknown, TargetUnknown, false
	}
}

func (g *Guide) cacheAndBroadcastClassification(request *RouteRequest, classification *RouteResult) {
	if classification.ClassificationMethod != "llm" {
		return
	}
	g.routeCache.Set(request.Input, classification)
	g.upsertRouteVersion(request.Input, classification)
	g.broadcastLearnedRoute(request.Input, classification)
}

func (g *Guide) upsertRouteVersion(input string, result *RouteResult) {
	if g == nil || g.routeVersions == nil || result == nil {
		return
	}
	normalized := normalizeInput(input)
	existing := g.routeVersions.GetRouteByInput(normalized)
	if existing == nil {
		_ = g.routeVersions.AddRoute(&VersionedRoute{
			Input:         normalized,
			TargetAgentID: string(result.TargetAgent),
			Intent:        result.Intent,
			Domain:        result.Domain,
			Confidence:    result.Confidence,
			Source:        RouteSourceLLM,
		})
		g.routeVersions.CreateVersion("guide", "learned route")
		return
	}
	_ = g.routeVersions.UpdateRoute(existing.ID, func(route *VersionedRoute) {
		if route == nil {
			return
		}
		route.TargetAgentID = string(result.TargetAgent)
		route.Intent = result.Intent
		route.Domain = result.Domain
		route.Confidence = result.Confidence
		route.Source = RouteSourceLLM
	})
	g.routeVersions.CreateVersion("guide", "updated learned route")
}

func (g *Guide) resolveCorrelationID(request *RouteRequest) string {
	if request.CorrelationID != "" {
		return request.CorrelationID
	}
	return fmt.Sprintf("corr_%d", time.Now().UnixNano())
}

func (g *Guide) buildForwardedRequest(request *RouteRequest, classification *RouteResult, correlationID string) *ForwardedRequest {
	fwd := &ForwardedRequest{
		CorrelationID:        correlationID,
		ParentCorrelationID:  request.ParentCorrelationID,
		Input:                request.Input,
		Intent:               classification.Intent,
		Domain:               classification.Domain,
		Entities:             classification.Entities,
		SourceAgentID:        request.SourceAgentID,
		SourceAgentName:      request.SourceAgentName,
		SessionID:            request.SessionID,
		TargetAgentID:        string(classification.TargetAgent),
		FireAndForget:        request.FireAndForget,
		Confidence:           classification.Confidence,
		ClassificationMethod: classification.ClassificationMethod,
		CrossDomain:          classification.CrossDomain,
		ConversationHistory:  g.conversationHistory(request.SessionID, string(classification.TargetAgent)),
	}
	return fwd
}

func (g *Guide) conversationHistory(sessionID string, targetAgentID string) []ConversationTurn {
	if g == nil || g.conversation == nil || sessionID == "" {
		return nil
	}
	return g.conversation.HistoryForSessionAgent(sessionID, targetAgentID)
}

func (g *Guide) attachEnrichment(
	ctx context.Context,
	request *RouteRequest,
	classification *RouteResult,
	forwarded *ForwardedRequest,
) {
	if g == nil || g.enrichment == nil || forwarded == nil || request == nil || classification == nil {
		return
	}
	if classification.Rejected || classification.Action == RouteActionReject {
		return
	}
	payloads := g.enrichment.Enrich(ctx, request, classification)
	if len(payloads) == 0 {
		return
	}
	forwarded.Entities = mergeEnrichmentEntities(forwarded.Entities, payloads)
}

func mergeEnrichmentEntities(
	entities *ExtractedEntities,
	payloads []corecontext.PrefetchSharePayload,
) *ExtractedEntities {
	if len(payloads) == 0 {
		return entities
	}
	if entities == nil {
		entities = &ExtractedEntities{}
	}
	if entities.Data == nil {
		entities.Data = map[string]any{}
	}
	entities.Data["enrichment"] = payloads
	return entities
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
	classification, _, err := g.resolveClassification(ctx, request)
	if err != nil {
		return nil, err
	}
	return classification, nil
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
		CorrectedBy:   g.agentID,
		CorrectedAt:   time.Now(),
		Reason:        reason,
	}
	g.router.AddCorrection(correction)
	g.persistCorrectionToArchivalist(correction)
}

func (g *Guide) persistCorrectionToArchivalist(correction CorrectionRecord) {
	if g == nil || g.bus == nil || !g.running {
		return
	}
	forwarded := &ForwardedRequest{
		CorrelationID:        generateMessageID(),
		Input:                "store guide route correction",
		Intent:               IntentStore,
		Domain:               DomainHistory,
		Entities:             &ExtractedEntities{Data: map[string]any{"event_type": "guide_route_correction", "correction": correction}},
		SourceAgentID:        g.agentID,
		SourceAgentName:      g.agentID,
		SessionID:            g.sessionID,
		TargetAgentID:        "archivalist",
		FireAndForget:        true,
		Confidence:           1.0,
		ClassificationMethod: "system",
	}
	msg := NewForwardMessage(generateMessageID(), forwarded)
	go func() { _ = g.bus.Publish(TopicRequests("archivalist", "archivalist"), msg) }()
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
	channels := NewAgentChannels(info.Type, info.ID)
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
	respSub, err := g.bus.Subscribe(channels.Responses, g.handleResponseMessage)
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

	g.setRunContext(ctx)

	// Subscribe to Guide's own request channel (guide.requests)
	requestSub, err := g.bus.SubscribeAsync(TopicGuideRequests, g.handleRequestMessage)
	if err != nil {
		g.cancelRunContext()
		return fmt.Errorf("failed to subscribe to guide.requests: %w", err)
	}
	g.requestSub = requestSub

	// Start resilience components
	g.pendingCleanup.Start()
	g.health.Start(g.processingContext())

	if g.observer != nil {
		if err := g.observer.Start(g.processingContext()); err != nil {
			g.stopResilience()
			_ = g.requestSub.Unsubscribe()
			g.requestSub = nil
			return fmt.Errorf("failed to start consultation observer: %w", err)
		}
	}

	g.running = true
	if err := g.subscribeRegisteredAgentChannels(); err != nil {
		_ = g.requestSub.Unsubscribe()
		g.requestSub = nil
		g.stopResilience()
		g.running = false
		return err
	}

	// If started with a rule-based classifier and a GoogleConfig is available,
	// launch a background goroutine to auto-upgrade to Gemini classification
	// once credentials become available.
	if g.googleConfig != nil && g.googleProvider == nil {
		g.startAuthAutoUpgrade()
	}

	return nil
}

func (g *Guide) subscribeRegisteredAgentChannels() error {
	if g == nil || g.bus == nil {
		return nil
	}
	channelsByAgent := g.agentChannels.Snapshot()
	created := make([]*agentSubscriptions, 0, len(channelsByAgent))
	for agentID, channels := range channelsByAgent {
		if channels == nil {
			continue
		}
		if _, exists := g.agentSubs.Get(agentID); exists {
			continue
		}
		subs, err := g.subscribeToAgentChannels(agentID, channels)
		if err != nil {
			g.unsubscribeAgentSubsBatch(created)
			return fmt.Errorf("failed to subscribe to %s channels: %w", agentID, err)
		}
		g.agentSubs.Set(agentID, subs)
		created = append(created, subs)
	}
	return nil
}

func (g *Guide) unsubscribeAgentSubsBatch(subs []*agentSubscriptions) {
	if len(subs) == 0 {
		return
	}
	var errs []error
	for _, sub := range subs {
		g.unsubscribeAgentSubs(&errs, sub)
	}
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
	g.cancelRunContext()
	g.autoUpgradeWg.Wait()
	g.pendingCleanup.Stop()
	g.health.Stop()
	if g.streams != nil {
		g.streams.CloseAll()
	}
	if g.domainClassifier != nil {
		g.domainClassifier.Close()
	}
	if g.observer != nil {
		g.observer.Stop()
	}
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
	snapshot := g.agentSubs.Snapshot()
	for _, subs := range snapshot {
		g.unsubscribeAgentSubs(errs, subs)
	}
	g.agentSubs.Clear()
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
	ctx := g.processingContext()
	if err := ctx.Err(); err != nil {
		return nil
	}
	switch msg.Type {
	case MessageTypeRequest:
		return g.handleRouteRequestMessage(ctx, msg)
	case MessageTypeReroute:
		return g.handleRerouteMessage(ctx, msg)
	case MessageTypeUserInterrupt:
		return g.handleUserInterruptMessage(msg)
	default:
		return nil
	}
}

func (g *Guide) handleRouteRequestMessage(ctx context.Context, msg *Message) error {
	req, ok := msg.GetRouteRequest()
	if !ok {
		return fmt.Errorf("invalid request payload")
	}
	if req == nil {
		return nil
	}
	correlationID := routeCorrelationID(msg, req)
	req.CorrelationID = correlationID
	reqCtx, cancel := context.WithCancel(ctx)
	g.registerRequestCancel(correlationID, cancel)
	defer g.clearRequestCancel(correlationID)
	defer cancel()

	reqCtx = providers.WithRetryObserver(reqCtx, func(event providers.RetryEvent) {
		g.publishRetryStatus(correlationID, req.SourceAgentID, RetryStatus{
			Attempt:     event.Attempt,
			MaxAttempts: event.MaxAttempts,
			Delay:       event.Delay,
			Err:         event.Err,
		})
	})

	forwarded, err := g.routeWithRetry(reqCtx, req, correlationID, req.SourceAgentID)
	if err != nil {
		if g.isInterruptError(err) {
			return nil
		}
		return g.publishRouteError(correlationID, req.SourceAgentID, err)
	}
	pending := g.pending.Get(forwarded.CorrelationID)
	if pending == nil {
		return fmt.Errorf("no pending request found for correlation ID: %s", forwarded.CorrelationID)
	}
	if g.isGuideTarget(pending.TargetAgentID) {
		return g.respondToGuideRequest(reqCtx, pending, req)
	}
	g.publishRouteHandoffProgress(forwarded.CorrelationID, pending.SourceAgentID, pending.TargetAgentID)
	return g.publishForwardedRequest(pending.TargetAgentID, forwarded)
}

func (g *Guide) handleRerouteMessage(ctx context.Context, msg *Message) error {
	reroute, ok := msg.GetRerouteRequest()
	if !ok || reroute == nil {
		return fmt.Errorf("invalid reroute payload")
	}

	// Break conversation stickiness so the fast-path won't fire.
	if g.conversation != nil && reroute.SessionID != "" {
		g.conversation.Clear(reroute.SessionID)
	}

	// Build a fresh RouteRequest from the reroute payload.
	req := &RouteRequest{
		Input:         reroute.OriginalInput,
		SourceAgentID: reroute.SourceAgentID,
		SessionID:     reroute.SessionID,
		Timestamp:     msg.Timestamp,
	}

	// If the source agent suggested a target, try that first.
	if reroute.SuggestedTarget != "" {
		req.TargetAgentID = reroute.SuggestedTarget
	}

	g.ensureRequestDefaults(req)
	correlationID := generateMessageID()
	req.CorrelationID = correlationID

	reqCtx, cancel := context.WithCancel(ctx)
	g.registerRequestCancel(correlationID, cancel)
	defer g.clearRequestCancel(correlationID)
	defer cancel()

	// Inject exclude list into context for loop prevention.
	reqCtx = withRerouteExcludeAgents(reqCtx, reroute.ExcludeAgents)

	forwarded, err := g.routeWithRetry(reqCtx, req, correlationID, req.SourceAgentID)
	if err != nil {
		if g.isInterruptError(err) {
			return nil
		}
		return g.publishRouteError(correlationID, req.SourceAgentID, err)
	}

	// Publish reroute notification for UI.
	g.publishRerouteEvent(correlationID, req.SourceAgentID, reroute)

	pending := g.pending.Get(forwarded.CorrelationID)
	if pending == nil {
		return fmt.Errorf("no pending request found for rerouted correlation ID: %s", forwarded.CorrelationID)
	}
	if g.isGuideTarget(pending.TargetAgentID) {
		return g.respondToGuideRequest(reqCtx, pending, req)
	}
	g.publishRouteHandoffProgress(forwarded.CorrelationID, pending.SourceAgentID, pending.TargetAgentID)
	return g.publishForwardedRequest(pending.TargetAgentID, forwarded)
}

func (g *Guide) publishRerouteEvent(correlationID, sourceAgentID string, reroute *RerouteRequest) {
	if g == nil || g.bus == nil || reroute == nil {
		return
	}
	sourceAgentID = strings.TrimSpace(sourceAgentID)
	if sourceAgentID == "" {
		return
	}
	event := &StreamEvent{
		Type: StreamEventReroute,
		Data: map[string]string{
			"from_agent":              reroute.SourceAgentID,
			"to_agent":               reroute.SuggestedTarget,
			"reason":                 reroute.Reason,
			"original_correlation_id": reroute.OriginalCorrelationID,
			"new_correlation_id":      correlationID,
		},
	}
	resp := &StreamResponse{
		CorrelationID:     correlationID,
		RespondingAgentID: "guide",
		TargetAgentID:     sourceAgentID,
		Event:             event,
	}
	msg := &Message{
		ID:            generateMessageID(),
		CorrelationID: correlationID,
		Type:          MessageTypeStream,
		Payload:       resp,
		SourceAgentID: "guide",
		TargetAgentID: sourceAgentID,
		Timestamp:     time.Now(),
	}
	_ = g.bus.Publish(TopicResponses(sourceAgentID, sourceAgentID), msg)
}

// rerouteExcludeKey is the context key for reroute excluded agent IDs.
type rerouteExcludeKey struct{}

func withRerouteExcludeAgents(ctx context.Context, agents []string) context.Context {
	if len(agents) == 0 {
		return ctx
	}
	return context.WithValue(ctx, rerouteExcludeKey{}, agents)
}

func rerouteExcludeAgentsFromContext(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	agents, _ := ctx.Value(rerouteExcludeKey{}).([]string)
	return agents
}

func (g *Guide) isGuideTarget(targetAgentID string) bool {
	return strings.EqualFold(targetAgentID, g.agentID) ||
		strings.EqualFold(targetAgentID, "guide") ||
		strings.EqualFold(targetAgentID, "g")
}

func routeCorrelationID(msg *Message, req *RouteRequest) string {
	if req != nil && strings.TrimSpace(req.CorrelationID) != "" {
		return strings.TrimSpace(req.CorrelationID)
	}
	if msg != nil && strings.TrimSpace(msg.CorrelationID) != "" {
		return strings.TrimSpace(msg.CorrelationID)
	}
	return generateMessageID()
}

func (g *Guide) respondToGuideRequest(ctx context.Context, pending *PendingRequest, req *RouteRequest) error {
	g.publishGuideStreamStart(pending.CorrelationID, pending.SourceAgentID)
	ctx = withGuideThoughtEmitter(ctx, func(thought string) {
		g.publishGuideStreamProgress(pending.CorrelationID, pending.SourceAgentID, thought)
	})
	ctx = withGuideEarlyUsageEmitter(ctx, func(inputTokens int) {
		g.publishGuideStreamEarlyUsage(pending.CorrelationID, pending.SourceAgentID, inputTokens)
	})
	ctx = providers.WithRetryObserver(ctx, func(event providers.RetryEvent) {
		g.publishRetryStatus(pending.CorrelationID, pending.SourceAgentID, RetryStatus{
			Attempt:     event.Attempt,
			MaxAttempts: event.MaxAttempts,
			Delay:       event.Delay,
			Err:         event.Err,
		})
	})
	reply, usage, err := g.generateGuideReply(
		ctx,
		req.Input,
		req.SessionID,
		pending.CorrelationID,
		pending.SourceAgentID,
		func(chunk string) {
			g.publishGuideStreamChunk(pending.CorrelationID, pending.SourceAgentID, chunk)
		},
	)
	resp := &RouteResponse{
		CorrelationID:       pending.CorrelationID,
		RespondingAgentID:   g.agentID,
		RespondingAgentName: "Guide",
	}
	if err != nil {
		resp.Success = false
		resp.Error = err.Error()
		g.publishGuideStreamError(pending.CorrelationID, pending.SourceAgentID, err)
	} else {
		resp.Success = true
		resp.Data = reply
		g.publishGuideStreamComplete(pending.CorrelationID, pending.SourceAgentID, usage)
	}
	resolved, handleErr := g.HandleResponse(ctx, resp)
	if handleErr != nil {
		return nil
	}
	return g.publishResponseToSource(resolved.SourceAgentID, resp)
}

func (g *Guide) generateGuideReply(
	ctx context.Context,
	input string,
	sessionID string,
	_ string,
	_ string,
	onChunk func(string),
) (string, *StreamUsage, error) {
	request := g.newGuideSelfResponseRequest(input, sessionID)
	responder := g.guideSelfResponder()
	reply, providerUsage, err := RespondGuideSelf(ctx, responder, request, func(chunk string) error {
		if onChunk != nil {
			onChunk(chunk)
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	var usage *StreamUsage
	if providerUsage != nil {
		usage = &StreamUsage{
			InputTokens:  providerUsage.InputTokens,
			OutputTokens: providerUsage.OutputTokens,
		}
	}
	return strings.TrimSpace(reply), usage, nil
}

func (g *Guide) guideSelfResponder() GuideSelfResponder {
	g.selfMu.RLock()
	responder := g.selfResponder
	g.selfMu.RUnlock()
	if responder != nil {
		return responder
	}
	return NewStaticGuideResponder()
}

// SetSelfResponder updates the responder used for guide-targeted queries.
func (g *Guide) SetSelfResponder(responder GuideSelfResponder) {
	g.selfMu.Lock()
	g.selfResponder = responder
	g.selfMu.Unlock()
}

// SetClassifier updates the routing classifier used for natural-language requests.
func (g *Guide) SetClassifier(classifier ClassifierService) {
	if g == nil || g.router == nil || classifier == nil {
		return
	}
	g.router.SetClassifier(classifier)
}

// RefreshAuth rebuilds the classifier and self-responder from the stored
// GoogleConfig by creating a fresh provider. This is the Guide's equivalent
// of Architect.RefreshPlannerAuth(): the caller only needs to say "credentials
// changed" — the Guide handles all internal reconstruction.
func (g *Guide) RefreshAuth(ctx context.Context) error {
	return g.refreshAuthWithMode(ctx, "", false)
}

// RefreshAuthWithMethod rebuilds auth-sensitive runtime components using the
// login method that triggered refresh. OAuth refreshes are strict: if provider
// construction silently falls back to a different auth mode, the refresh fails.
func (g *Guide) RefreshAuthWithMethod(ctx context.Context, method string) error {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "oauth":
		const (
			maxAttempts = 8
			retryDelay  = 350 * time.Millisecond
		)
		var lastErr error
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			err := g.refreshAuthWithMode(ctx, providers.GoogleAuthModeOAuth, true)
			if err == nil {
				return nil
			}
			lastErr = err
			if attempt == maxAttempts {
				break
			}
			geminiTrace("auth", "refresh_retry", map[string]any{
				"requested_auth_mode": providers.GoogleAuthModeOAuth,
				"attempt":             attempt,
				"next_attempt":        attempt + 1,
				"error":               err.Error(),
			})
			if sleepErr := sleepContext(ctx, retryDelay); sleepErr != nil {
				return sleepErr
			}
		}
		return lastErr
	case "apikey":
		return g.refreshAuthWithMode(ctx, providers.GoogleAuthModeAPIKey, false)
	default:
		return g.refreshAuthWithMode(ctx, "", false)
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (g *Guide) refreshAuthWithMode(ctx context.Context, mode string, strict bool) error {
	if g == nil || g.googleConfig == nil {
		return nil
	}
	cfgOverride := *g.googleConfig
	if trimmed := strings.TrimSpace(mode); trimmed != "" {
		cfgOverride.AuthMode = trimmed
	}
	promptSkills := DiscoverGuidePromptSkills()
	provider, err := providers.NewGoogleProvider(ctx, cfgOverride, promptSkills...)
	if err != nil {
		geminiTrace("auth", "refresh_failed", map[string]any{
			"requested_auth_mode": strings.TrimSpace(cfgOverride.AuthMode),
			"strict":              strict,
			"error":               err.Error(),
		})
		return err
	}
	resolvedMode := provider.AuthMode()
	if strict && strings.TrimSpace(cfgOverride.AuthMode) != "" && !strings.EqualFold(resolvedMode, cfgOverride.AuthMode) {
		err := fmt.Errorf(
			"google auth refresh requested %q but resolved %q",
			cfgOverride.AuthMode,
			resolvedMode,
		)
		geminiTrace("auth", "refresh_mode_mismatch", map[string]any{
			"requested_auth_mode": strings.TrimSpace(cfgOverride.AuthMode),
			"resolved_auth_mode":  strings.TrimSpace(resolvedMode),
			"use_vertex_ai":       provider.UsesVertexAI(),
		})
		return err
	}
	geminiTrace("auth", "refresh_success", map[string]any{
		"requested_auth_mode": strings.TrimSpace(cfgOverride.AuthMode),
		"resolved_auth_mode":  strings.TrimSpace(resolvedMode),
		"use_vertex_ai":       provider.UsesVertexAI(),
	})
	g.providerMu.Lock()
	g.googleProvider = provider
	g.googleConfig = &cfgOverride
	g.providerMu.Unlock()

	cfg := g.config.RouterConfig
	g.SetClassifier(NewGeminiClassifier(provider, cfg))
	g.SetSelfResponder(resolveGuideSelfResponder(g.config, provider, g))
	if cache := g.RouteCache(); cache != nil {
		cache.Clear()
	}
	return nil
}

func (g *Guide) startAuthAutoUpgrade() {
	interval := g.config.AutoUpgradeInterval
	if interval <= 0 {
		interval = defaultAutoUpgradeInterval
	}
	ctx := g.processingContext()
	g.autoUpgradeWg.Add(1)
	go func() {
		defer g.autoUpgradeWg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := g.RefreshAuth(ctx); err == nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (g *Guide) newGuideSelfResponseRequest(input string, sessionID string) GuideSelfResponseRequest {
	request := GuideSelfResponseRequest{
		Input:              input,
		AgentID:            g.agentID,
		SessionID:          sessionID,
		PendingRequests:    g.pending.Count(),
		RegisteredAgentIDs: g.registeredAgentIDs(),
		LoadedSkillNames:   g.loadedGuideSkillNames(),
	}
	if g == nil || g.conversation == nil {
		return request
	}
	snapshot, ok := g.conversation.Snapshot(sessionID)
	if !ok {
		return request
	}
	request.ActiveConversationAgent = snapshot.ActiveAgentID
	request.ActiveConversationTurns = snapshot.Turns
	request.ActiveConversationAge = int(snapshot.Age.Seconds())
	request.ActiveConversationScore = snapshot.ActivationHint
	return request
}

func (g *Guide) loadedGuideSkillNames() []string {
	if g == nil || g.skills == nil {
		return nil
	}
	loaded := g.skills.GetLoaded()
	if len(loaded) == 0 {
		return nil
	}
	names := make([]string, 0, len(loaded))
	for _, skill := range loaded {
		if skill == nil {
			continue
		}
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (g *Guide) routeWithRetry(ctx context.Context, req *RouteRequest, _, _ string) (*ForwardedRequest, error) {
	stream := g.beginRoutingStream(req.CorrelationID, req)
	result, err := g.Route(ctx, req)
	g.completeRoutingStream(stream, result, err)
	return result, err
}

func (g *Guide) beginRoutingStream(correlationID string, req *RouteRequest) *ResponseStream {
	if g == nil || g.streams == nil || correlationID == "" || req == nil {
		return nil
	}
	stream, err := g.streams.CreateStream(correlationID, req.SessionID)
	if err != nil || stream == nil {
		return nil
	}
	stream.SendProgress(0, 1, "routing request")
	return stream
}

func (g *Guide) completeRoutingStream(stream *ResponseStream, result *ForwardedRequest, err error) {
	if g == nil || g.streams == nil || stream == nil {
		return
	}
	if err != nil {
		stream.SendError(err)
		g.streams.CloseStream(stream.CorrelationID)
		return
	}
	if result != nil {
		stream.SendComplete(result)
	}
	g.streams.CloseStream(stream.CorrelationID)
}

func (g *Guide) recordRetryStreamEvent(stream *ResponseStream, attempt, max int, err error) {
	if stream == nil {
		return
	}
	stream.SendData(map[string]any{
		"event":        "retry",
		"attempt":      attempt,
		"max_attempts": max,
		"error":        err.Error(),
	})
}

func (g *Guide) publishRetryStatus(correlationID, sourceAgentID string, status RetryStatus) {
	if g.bus == nil || !g.running {
		return
	}
	event := &StreamEvent{
		Type:      StreamEventRetry,
		Data:      status,
		Timestamp: time.Now(),
	}
	resp := &StreamResponse{
		CorrelationID:     correlationID,
		RespondingAgentID: g.agentID,
		TargetAgentID:     sourceAgentID,
		Event:             event,
	}
	busMsg := &Message{
		ID:            generateMessageID(),
		CorrelationID: correlationID,
		Type:          MessageTypeStream,
		Payload:       resp,
	}
	_ = g.bus.Publish(TopicResponses(sourceAgentID, sourceAgentID), busMsg)
}

func (g *Guide) publishGuideStreamStart(correlationID, sourceAgentID string) {
	event := &StreamEvent{
		Type:      StreamEventStart,
		Timestamp: time.Now(),
	}
	g.publishGuideStreamEvent(correlationID, sourceAgentID, event)
}

func (g *Guide) publishGuideStreamChunk(correlationID, sourceAgentID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	event := &StreamEvent{
		Type:      StreamEventData,
		Text:      text,
		Timestamp: time.Now(),
	}
	g.publishGuideStreamEvent(correlationID, sourceAgentID, event)
}

func (g *Guide) publishGuideStreamProgress(correlationID, sourceAgentID, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	event := &StreamEvent{
		Type: StreamEventProgress,
		Data: &ProgressData{
			Message: message,
		},
		Timestamp: time.Now(),
	}
	g.publishGuideStreamEvent(correlationID, sourceAgentID, event)
}

func (g *Guide) publishRouteHandoffProgress(correlationID, sourceAgentID, targetAgentID string) {
	targetAgentID = strings.TrimSpace(targetAgentID)
	if targetAgentID == "" {
		return
	}
	event := &StreamEvent{
		Type: StreamEventProgress,
		Data: &ProgressData{
			Message: "Handing off to " + targetAgentID + "...",
		},
		Timestamp: time.Now(),
	}
	g.publishStreamEventForResponder(correlationID, sourceAgentID, targetAgentID, event)
}

func (g *Guide) publishGuideStreamEarlyUsage(correlationID, sourceAgentID string, inputTokens int) {
	if inputTokens <= 0 {
		return
	}
	event := &StreamEvent{
		Type:      StreamEventData,
		Usage:     &StreamUsage{InputTokens: inputTokens},
		Timestamp: time.Now(),
	}
	g.publishGuideStreamEvent(correlationID, sourceAgentID, event)
}

func (g *Guide) publishGuideStreamComplete(correlationID, sourceAgentID string, usage *StreamUsage) {
	event := &StreamEvent{
		Type:      StreamEventComplete,
		Usage:     usage,
		Timestamp: time.Now(),
	}
	g.publishGuideStreamEvent(correlationID, sourceAgentID, event)
}

func (g *Guide) publishGuideStreamError(correlationID, sourceAgentID string, err error) {
	if err == nil {
		return
	}
	event := &StreamEvent{
		Type:      StreamEventError,
		Data:      map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
	}
	g.publishGuideStreamEvent(correlationID, sourceAgentID, event)
}

func (g *Guide) publishGuideStreamEvent(correlationID, sourceAgentID string, event *StreamEvent) {
	g.publishStreamEventForResponder(correlationID, sourceAgentID, g.agentID, event)
}

func (g *Guide) publishStreamEventForResponder(correlationID, sourceAgentID, responderID string, event *StreamEvent) {
	if g == nil || g.bus == nil || event == nil {
		return
	}
	correlationID = strings.TrimSpace(correlationID)
	sourceAgentID = strings.TrimSpace(sourceAgentID)
	responderID = strings.TrimSpace(responderID)
	if correlationID == "" || sourceAgentID == "" || responderID == "" {
		return
	}
	resp := &StreamResponse{
		CorrelationID:     correlationID,
		RespondingAgentID: responderID,
		TargetAgentID:     sourceAgentID,
		Event:             event,
	}
	msg := &Message{
		ID:            generateMessageID(),
		CorrelationID: correlationID,
		Type:          MessageTypeStream,
		Payload:       resp,
		SourceAgentID: g.agentID,
		TargetAgentID: sourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
	_ = g.bus.Publish(TopicResponses(sourceAgentID, sourceAgentID), msg)
}

func (g *Guide) registeredAgentIDs() []string {
	if g.registry == nil {
		return nil
	}
	agents := g.registry.GetAll()
	ids := make([]string, 0, len(agents))
	for _, agent := range agents {
		if agent == nil || agent.ID == "" {
			continue
		}
		ids = append(ids, agent.ID)
	}
	return ids
}

func (g *Guide) handleResponseMessage(msg *Message) error {
	ctx := g.processingContext()
	if err := ctx.Err(); err != nil {
		return nil
	}

	// Touch activity for the responding agent to prevent idle demotion
	// during active conversation flow.
	if g.touchActivityHook != nil && msg.SourceAgentID != "" {
		g.touchActivityHook(msg.SourceAgentID)
	}

	switch msg.Type {
	case MessageTypeResponse:
		resp, ok := msg.GetRouteResponse()
		if !ok {
			return fmt.Errorf("invalid response payload")
		}

		pending, err := g.HandleResponse(ctx, resp)
		if err != nil {
			return nil
		}
		g.observeConversationResponse(pending, resp)

		return g.publishResponseToSource(pending.SourceAgentID, resp)
	case MessageTypeStream:
		streamResp, ok := msg.GetStreamResponse()
		if !ok {
			return fmt.Errorf("invalid stream response payload")
		}
		g.recordIncomingStreamEvent(streamResp)
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

func (g *Guide) recordIncomingStreamEvent(resp *StreamResponse) {
	if g == nil || g.streams == nil || resp == nil || resp.Event == nil {
		return
	}
	stream := g.streams.GetStream(resp.CorrelationID)
	if stream == nil {
		return
	}
	stream.SendData(resp.Event)
}

func (g *Guide) registerRequestCancel(correlationID string, cancel context.CancelFunc) {
	if g == nil || cancel == nil {
		return
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	g.requestCancelMu.Lock()
	g.requestCancels[correlationID] = cancel
	g.requestCancelMu.Unlock()
}

func (g *Guide) clearRequestCancel(correlationID string) {
	if g == nil {
		return
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	g.requestCancelMu.Lock()
	delete(g.requestCancels, correlationID)
	g.requestCancelMu.Unlock()
}

func (g *Guide) cancelRequestContext(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	var cancel context.CancelFunc
	g.requestCancelMu.Lock()
	cancel = g.requestCancels[correlationID]
	delete(g.requestCancels, correlationID)
	g.requestCancelMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (g *Guide) handleUserInterruptMessage(msg *Message) error {
	req, correlationID := g.interruptRequestFromMessage(msg)
	if correlationID == "" {
		return nil
	}
	g.cancelRequestContext(correlationID)
	pending := g.pending.Remove(correlationID)
	if pending != nil {
		g.forwardUserInterruptToTarget(req, pending)
	}
	if g.streams != nil {
		g.streams.CloseStream(correlationID)
	}
	return nil
}

func (g *Guide) interruptRequestFromMessage(msg *Message) (*UserInterruptRequest, string) {
	if msg == nil {
		return nil, ""
	}
	req, _ := msg.GetUserInterruptRequest()
	correlationID := strings.TrimSpace(msg.CorrelationID)
	if req != nil && strings.TrimSpace(req.CorrelationID) != "" {
		correlationID = strings.TrimSpace(req.CorrelationID)
	}
	return req, correlationID
}

func (g *Guide) forwardUserInterruptToTarget(req *UserInterruptRequest, pending *PendingRequest) {
	if g == nil || pending == nil || strings.TrimSpace(pending.TargetAgentID) == "" {
		return
	}
	if g.isGuideTarget(pending.TargetAgentID) {
		return
	}
	action := g.userInterruptAction(req, pending)
	msg := NewActionMessage(generateMessageID(), action)
	_ = g.bus.Publish(guideTargetTopic(pending.TargetAgentID), msg)
}

func (g *Guide) userInterruptAction(req *UserInterruptRequest, pending *PendingRequest) *ActionRequest {
	reason := ""
	if req != nil {
		reason = req.Reason
	}
	data := map[string]any{
		"correlation_id": pending.CorrelationID,
	}
	if reason != "" {
		data["reason"] = reason
	}
	return &ActionRequest{
		CorrelationID: pending.CorrelationID,
		SourceAgentID: g.agentID,
		TargetAgentID: pending.TargetAgentID,
		Action:        "cancel",
		Data:          data,
		FireAndForget: true,
		Timestamp:     time.Now(),
	}
}

func guideTargetTopic(targetAgentID string) string {
	return TopicRequests(targetAgentID, targetAgentID)
}

// handleErrorMessage processes incoming error messages from agent error channels
func (g *Guide) handleErrorMessage(msg *Message) error {
	ctx := g.processingContext()
	if err := ctx.Err(); err != nil {
		return nil
	}

	if msg.Type != MessageTypeError {
		return nil
	}

	errStr, ok := msg.GetError()
	if !ok {
		return fmt.Errorf("invalid error payload")
	}

	resp := g.routeResponseFromError(msg, errStr)
	pending, err := g.HandleResponse(ctx, resp)
	if err != nil {
		return nil
	}

	return g.publishErrorToSource(pending.SourceAgentID, msg.CorrelationID, msg.SourceAgentID, errStr)
}

func (g *Guide) setRunContext(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	runCtx, cancel := context.WithCancel(parent)

	g.runMu.Lock()
	g.runCtx = runCtx
	g.runCancel = cancel
	g.runMu.Unlock()
}

func (g *Guide) processingContext() context.Context {
	g.runMu.RLock()
	ctx := g.runCtx
	g.runMu.RUnlock()
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func (g *Guide) cancelRunContext() {
	g.runMu.Lock()
	cancel := g.runCancel
	g.runCancel = nil
	g.runMu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (g *Guide) isInterruptError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (g *Guide) publishRouteError(correlationID string, sourceAgentID string, err error) error {
	errMsg := NewErrorMessage(generateMessageID(), correlationID, g.agentID, err.Error())
	return g.bus.Publish(TopicResponses(sourceAgentID, sourceAgentID), errMsg)
}

func (g *Guide) publishForwardedRequest(targetAgentID string, forwarded *ForwardedRequest) error {
	if g.activationHook != nil {
		if err := g.activationHook(g.processingContext(), targetAgentID); err != nil {
			slog.Warn("activation hook failed", "target", targetAgentID, "error", err)
			// Still attempt delivery — agent might already be hot.
		}
	}
	if g.touchActivityHook != nil {
		g.touchActivityHook(targetAgentID)
	}

	// Weighted target resolution: during overlap, multiple endpoints may
	// exist for the same agent type. Select based on quality weights.
	resolvedTarget := g.resolveWeightedTarget(targetAgentID)

	fwdMsg := g.forwardMessage(resolvedTarget, forwarded)
	return g.bus.Publish(TopicRequests(resolvedTarget, resolvedTarget), fwdMsg)
}

func (g *Guide) forwardMessage(targetAgentID string, forwarded *ForwardedRequest) *Message {
	fwdMsg := NewForwardMessage(generateMessageID(), forwarded)
	fwdMsg.TargetAgentID = targetAgentID
	return fwdMsg
}

func (g *Guide) publishResponseToSource(sourceAgentID string, resp *RouteResponse) error {
	respMsg := NewResponseMessage(generateMessageID(), resp)
	return g.bus.Publish(TopicResponses(sourceAgentID, sourceAgentID), respMsg)
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
	return g.bus.Publish(TopicResponses(sourceAgentID, sourceAgentID), msg)
}

func (g *Guide) publishErrorToSource(sourceAgentID string, correlationID string, sourceAgent string, errStr string) error {
	errMsg := NewErrorMessage(generateMessageID(), correlationID, sourceAgent, errStr)
	return g.bus.Publish(TopicResponses(sourceAgentID, sourceAgentID), errMsg)
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
		RouteVersions:    g.routeVersionStats(),
		StreamStats:      g.streamStats(),
		DomainHints:      g.domainHintStats(),
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
	RouteVersions    RouteVersionStats              `json:"route_versions"`
	StreamStats      StreamStats                    `json:"streams"`
	DomainHints      map[string]any                 `json:"domain_hints,omitempty"`
	CircuitStats     map[string]CircuitBreakerStats `json:"circuits"`
	HealthStats      HealthMonitorStats             `json:"health"`
	DLQStats         DeadLetterStats                `json:"dlq"`
	SkillStats       skills.Stats                   `json:"skills"`
	HookStats        skills.HookStats               `json:"hooks"`
}

func (g *Guide) routeVersionStats() RouteVersionStats {
	if g == nil || g.routeVersions == nil {
		return RouteVersionStats{}
	}
	return g.routeVersions.Stats()
}

func (g *Guide) streamStats() StreamStats {
	if g == nil || g.streams == nil {
		return StreamStats{}
	}
	return g.streams.Stats()
}

func (g *Guide) domainHintStats() map[string]any {
	if g == nil || g.domainClassifier == nil {
		return nil
	}
	stats := map[string]any{
		"enabled":     g.domainClassifier.IsEnabled(),
		"stage_count": g.domainClassifier.StageCount(),
	}
	if cacheStats := g.domainClassifier.CacheStats(); cacheStats != nil {
		stats["cache"] = cacheStats
	}
	return stats
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

// =============================================================================
// HandoffInjectable Interface
// =============================================================================

// SetHandoffBridge attaches a HandoffBridge to this Guide instance.
func (g *Guide) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	g.handoffBridge = bridge
}

// AgentID returns the unique identifier for this Guide instance.
func (g *Guide) AgentID() string {
	return g.config.AgentID
}

// AgentType returns the agent type string for the Guide.
func (g *Guide) AgentType() string {
	return "guide"
}

// Descriptor returns the immutable metadata describing this agent type.
func (g *Guide) Descriptor() handoff.AgentDescriptor {
	return handoff.AgentDescriptor{
		AgentType:     "guide",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      handoff.CategoryStandalone,
	}
}

// ExtractArchivableState captures the Guide's current state for handoff persistence.
func (g *Guide) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   g.config.AgentID,
		AgentType: "guide",
		Timestamp: time.Now(),
	}
}

// Terminate gracefully shuts down the Guide, delegating to Stop.
func (g *Guide) Terminate(_ context.Context) error {
	return g.Stop()
}

// InjectPreparedContext accepts a prepared context from a handoff.
// Guide rebuilds router state from scratch, so the prepared context is
// acknowledged but not applied.
func (g *Guide) InjectPreparedContext(_ *handoff.PreparedContext) error {
	return nil
}

// ServiceHealthChecker abstracts health checking to avoid importing
// the network package directly. The container bootstrap layer provides
// an implementation backed by the ServiceRegistry.
type ServiceHealthChecker interface {
	HasHealthyEndpoints(agentType string) bool
}

// ServiceQualityChecker extends ServiceHealthChecker with quality-aware
// endpoint selection. When set on the Guide, it enables weighted routing
// during overlap handoffs.
type ServiceQualityChecker interface {
	ServiceHealthChecker
	GetWeightedEndpoints(agentType string) []QualityEndpoint
}

// SetActivationHook sets a callback invoked before forwarding a request
// to ensure the target agent's container is active. The hook is typically
// bound to ActivationController.EnsureActive.
func (g *Guide) SetActivationHook(hook func(ctx context.Context, agentType string) error) {
	g.activationHook = hook
}

// SetTouchActivityHook installs a callback invoked after request
// forwarding and response handling to reset the idle timer on the
// target agent's container, preventing demotion during active
// conversations.
func (g *Guide) SetTouchActivityHook(hook func(agentType string)) {
	g.touchActivityHook = hook
}

// SetServiceRegistry sets the health checker used for routing decisions.
// When set, isAgentHealthy delegates to the registry instead of the
// local readyAgents map.
func (g *Guide) SetServiceRegistry(reg ServiceHealthChecker) {
	g.serviceRegistry = reg
}

// SetServiceQualityRegistry sets the quality-aware health checker.
// This enables weighted endpoint selection during overlap handoffs.
// It also sets the base serviceRegistry (satisfies ServiceHealthChecker).
func (g *Guide) SetServiceQualityRegistry(reg ServiceQualityChecker) {
	g.serviceRegistry = reg
	g.qualityChecker = reg
	g.qualitySelector = NewQualityAwareSelector(nil)
}

// resolveWeightedTarget selects the best endpoint for the given target
// agent type using quality-weighted routing. If no quality checker is
// set or only one endpoint exists, returns the original target unchanged.
func (g *Guide) resolveWeightedTarget(targetAgentID string) string {
	if g.qualityChecker == nil || g.qualitySelector == nil {
		return targetAgentID
	}

	endpoints := g.qualityChecker.GetWeightedEndpoints(targetAgentID)
	if len(endpoints) <= 1 {
		return targetAgentID
	}

	selected := g.qualitySelector.Select(endpoints)
	if selected.AgentID == "" {
		return targetAgentID
	}
	return selected.AgentID
}

// isAgentHealthy checks whether an agent is healthy enough to receive
// traffic. Prefers the service registry (probe-driven) when available,
// falling back to the local readyAgents map.
func (g *Guide) isAgentHealthy(agentType string) bool {
	if g.serviceRegistry != nil {
		return g.serviceRegistry.HasHealthyEndpoints(agentType)
	}
	return g.IsAgentReady(agentType)
}
