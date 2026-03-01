package guide

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/container"
	corecontext "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"golang.org/x/sync/singleflight"
)

var (
	guideDebugLog     *slog.Logger
	guideDebugLogOnce sync.Once
)

// DebugFileLog returns the shared file logger for debug tracing.
// Writes to ~/.sylk/logs/ui_events.log.
func DebugFileLog() *slog.Logger { return guideFileLog() }

func guideFileLog() *slog.Logger {
	guideDebugLogOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".sylk", "logs")
		os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(filepath.Join(dir, "ui_events.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			guideDebugLog = slog.Default()
			return
		}
		guideDebugLog = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	return guideDebugLog
}

func truncateLogStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// defaultGuideRequestTimeout bounds the entire request lifecycle:
// classification + activation + self-response (or routing handoff).
// Individual phases (classification 60s, self-response 90s) inherit and
// shrink this deadline via DeadlinePropagator.
const defaultGuideRequestTimeout = 120 * time.Second


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

	// Activity publisher for UI agent-panel updates
	activityPub events.ActivityPublisher

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

	// typeIndex maps agent IDs (including UUIDs) to their agent type name.
	// Populated during Register and never cleared — bounded to the number
	// of distinct agent registrations (≤16). Used to resolve UUIDs back to
	// type names for the activation controller, which indexes by type.
	typeIndex *ShardedMap[string, string]

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

	// On-demand agent activator — ensures the target agent's container
	// is hot before forwarding a request, and resets the idle timer
	// after successful activation and on response handling.
	activator AgentActivator

	// Agent registrar callback — called after on-demand activation to
	// register the agent's routing info (capabilities, intents, channels)
	// with the Guide. Bridges the container→guide registration gap for
	// agents that weren't pre-activated during bootstrap.
	agentRegistrar func(agentType string)

	// Service registry for health-aware routing. When set, isAgentHealthy
	// uses healthy endpoints from the registry instead of the local
	// readyAgents map.
	serviceRegistry ServiceHealthChecker

	// Quality-aware routing for weighted endpoint selection during overlap.
	qualityChecker  ServiceQualityChecker
	qualitySelector *QualityAwareSelector

	// Conversation history buffer for self-response turns.
	selfResponseHistory []providers.Message
	selfResponseMu      sync.RWMutex

	// Self-managed provider lifecycle (supports cross-provider model swap)
	googleConfig    *providers.GoogleConfig
	provider        providers.ProviderAdapter
	activeModel     string // Currently active model ID (set on swap).
	providerMu      sync.RWMutex
	providerWrapper gateway.ProviderWrapper
	// Auth credential change subscription.
	authSub Subscription

	// Running state
	running bool

	runMu     sync.RWMutex
	runCtx    context.Context
	runCancel context.CancelFunc

	requestCancelMu sync.Mutex
	requestCancels  map[string]context.CancelFunc

	// streamReroutes stores CID → stream target agent ID for rerouted
	// streams. Populated when a reroute event arrives (before the pending
	// for the new CID may exist). Applied to the pending's
	// StreamTargetOverride when the pending is created or when the first
	// stream event arrives for that CID.
	streamReroutesMu sync.Mutex
	streamReroutes   map[string]string

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
	// When set, RefreshAuth() rebuilds the classifier and responder from this config.
	GoogleConfig *providers.GoogleConfig

	// ActivityPub for publishing agent activity events to the UI panel.
	ActivityPub events.ActivityPublisher
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
	selfResponder := resolveGuideSelfResponder(cfg, nil, "", nil)

	guide := &Guide{
		router:         router,
		config:         cfg,
		bus:            cfg.Bus,
		activityPub:    cfg.ActivityPub,
		agentSubs:      NewStringMap[*agentSubscriptions](DefaultShardCount),
		agentChannels:  NewStringMap[*AgentChannels](DefaultShardCount),
		readyAgents:    NewStringMap[bool](DefaultShardCount),
		typeIndex:      NewStringMap[string](DefaultShardCount),
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
		streamReroutes: make(map[string]string),
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
	skillsLoaderCfg.CoreSkills = []string{"route", "clarify", "guide_route", "help", "status", "agents", "self_diagnostic", "evaluate_plan_acceptance"}
	skillsLoaderCfg.AutoLoadDomains = nil

	hooks := skills.NewHookRegistry()

	// CRITICAL SAFETY CATCH
	// The Guide must NEVER be able to execute generic state-mutating tools like write_file or run_shell_command
	// even if they somehow leak into its context. We explicitly whitelist its approved tools.
	allowedTools := map[string]bool{
		"route": true, "clarify": true, "guide_route": true, "help": true, "status": true,
		"agents": true, "conversation_context": true, "route_to": true, "reply_to": true, "broadcast": true,
		"task_interact": true, "get_routing_history": true, "get_agent_capabilities": true,
		"self_diagnostic": true, "evaluate_plan_acceptance": true,
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
	selfResponder := resolveGuideSelfResponder(cfg, nil, "", nil)

	guide := &Guide{
		router:         router,
		config:         cfg,
		bus:            cfg.Bus,
		activityPub:    cfg.ActivityPub,
		agentSubs:      NewStringMap[*agentSubscriptions](DefaultShardCount),
		agentChannels:  NewStringMap[*AgentChannels](DefaultShardCount),
		readyAgents:    NewStringMap[bool](DefaultShardCount),
		typeIndex:      NewStringMap[string](DefaultShardCount),
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
		streamReroutes: make(map[string]string),
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

// NewWithProvider creates a new Guide agent with any LLM provider.
// The provider is used for both classification and self-response.
func NewWithProvider(provider providers.ProviderAdapter, model string, cfg Config) (*Guide, error) {
	cfg = ensureRouterConfig(cfg)
	if err := validateBus(cfg); err != nil {
		return nil, err
	}

	registry := resolveRegistry(cfg)
	routing := NewRoutingAggregator()
	routing.RegisterAgent(GuideRoutingInfo())

	parser := NewParserWithRouting(cfg.RouterConfig.DSLPrefix, routing)
	llmClassifier := NewLLMClassifier(provider, model, cfg.RouterConfig)
	classifier := NewFallbackClassifier(llmClassifier, NewRuleClassifierService())
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
	selfResponder := resolveGuideSelfResponder(cfg, provider, model, nil)

	guide := &Guide{
		router:         router,
		config:         cfg,
		bus:            cfg.Bus,
		activityPub:    cfg.ActivityPub,
		agentSubs:      NewStringMap[*agentSubscriptions](DefaultShardCount),
		agentChannels:  NewStringMap[*AgentChannels](DefaultShardCount),
		readyAgents:    NewStringMap[bool](DefaultShardCount),
		typeIndex:      NewStringMap[string](DefaultShardCount),
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
		provider:       provider,
		activeModel:    model,
		sessionID:      cfg.SessionID,
		agentID:        agentID,
		requestCancels: make(map[string]context.CancelFunc),
		streamReroutes: make(map[string]string),
	}

	guide.registerCoreSkills()
	guide.registerExtendedSkills()
	guide.skillLoader = skills.NewLoader(guide.skills, loaderCfg)
	if cfg.SelfResponder == nil {
		guide.selfResponder = resolveGuideSelfResponder(cfg, provider, model, guide)
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
	g.applyPendingPlanMetadata(request.SessionID, classification)

	corrID := g.resolveCorrelationID(request)
	if !request.FireAndForget {
		corrID = g.pending.Add(request, classification, targetAgentID)
	}

	forwarded := g.buildForwardedRequest(request, classification, corrID)
	g.attachEnrichment(ctx, request, classification, forwarded)
	return forwarded, nil
}

// RouteAndForward routes the request AND publishes it to the target agent.
// Used by tool/skill handlers where the caller cannot do the forwarding itself
// (e.g. the self-response tool loop). For guide-targeted requests, the forwarded
// request is returned without publishing — the caller handles the response.
func (g *Guide) RouteAndForward(ctx context.Context, request *RouteRequest) (*ForwardedRequest, error) {
	forwarded, err := g.Route(ctx, request)
	if err != nil {
		return nil, err
	}
	pending := g.pending.Get(forwarded.CorrelationID)
	if pending == nil {
		return forwarded, nil
	}
	if g.isGuideTarget(pending.TargetAgentID) {
		return forwarded, nil
	}
	if err := g.publishForwardedRequest(pending.TargetAgentID, forwarded, nil); err != nil {
		return nil, fmt.Errorf("forward to %s: %w", pending.TargetAgentID, err)
	}
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
	classStart := time.Now()
	guideFileLog().Info("DEBUG: resolve_classification_start",
		"explicit_target", request.ExplicitTarget,
		"target_agent", request.TargetAgentID,
		"input_preview", truncateLogStr(request.Input, 80))
	g.observeUserConversationSignal(request)
	ctx = g.augmentClassificationContext(ctx, request)

	// Fast-path: when an active conversation agent has high ACT-R activation
	// and no explicit target was specified, skip LLM classification entirely.
	// Suppressed when a plan is pending — the classifier must see plan context
	// to disambiguate bare affirmatives from general follow-ups.
	if !request.ExplicitTarget && request.TargetAgentID == "" && g.conversation.PendingPlan(request.SessionID) == nil {
		if result, target, ok := g.tryConversationFastPath(ctx, request); ok {
			guideFileLog().Info("DEBUG: resolve_classification_fastpath_hit", "target", target, "elapsed_ms", time.Since(classStart).Milliseconds())
			return result, target, nil
		}
	}

	if request.TargetAgentID != "" {
		resolvedTarget, err := g.ensureExplicitTargetReady(ctx, request.TargetAgentID)
		if err != nil && request.ExplicitTarget {
			return nil, "", fmt.Errorf("cannot route to @%s: %w", request.TargetAgentID, err)
		}
		if resolvedTarget != "" {
			request.TargetAgentID = resolvedTarget
		}
		classification := g.explicitTargetClassification(request.TargetAgentID)
		classification.Domain = g.supportedDomainForTarget(request.TargetAgentID, classification.Domain, classification.Intent)

		var targetAgentID string
		if request.ExplicitTarget {
			// User deliberately selected this agent (panel click or @agent
			// prefix). Honor the choice — skip routability demotion that
			// would redirect to Guide when the synthetic IntentHelp is not
			// in the agent's declared intents.
			targetAgentID = classificationTargetAgentID(request.TargetAgentID, classification)
		} else {
			classification, targetAgentID = g.ensureRoutableClassification(ctx, classification, request.TargetAgentID)
		}

		// Explicit non-guide targets should bypass conversation-flow remapping.
		// Explicit "guide" requests still represent "route this through guide",
		// so follow-up continuity should remain active.
		if request.ExplicitTarget && !explicitGuideFollowupAllowed(request.TargetAgentID) {
			g.observeRoutedConversationTarget(request, targetAgentID)
		} else if explicitGuideFollowupAllowed(request.TargetAgentID) {
			classification, targetAgentID = g.applyConversationFlow(ctx, request, classification, targetAgentID)
		} else {
			g.observeRoutedConversationTarget(request, targetAgentID)
		}
		return classification, targetAgentID, nil
	}

	domainCtx := g.preclassifyDomain(ctx, request)
	if request.SessionID != "" && g.sessionRouter != nil {
		guideFileLog().Info("DEBUG: classify_session_start", "elapsed_ms", time.Since(classStart).Milliseconds())
		classification, targetAgentID, err := g.classifyWithSingleflight(ctx, request, domainCtx, true)
		guideFileLog().Info("DEBUG: classify_session_done", "target", targetAgentID, "elapsed_ms", time.Since(classStart).Milliseconds(), "error", err)
		if err != nil {
			return nil, "", err
		}
		guideFileLog().Info("DEBUG: finalize_start", "target", targetAgentID, "elapsed_ms", time.Since(classStart).Milliseconds())
		classification, targetAgentID = g.finalizeClassificationWithExclude(ctx, request.Input, classification, targetAgentID)
		guideFileLog().Info("DEBUG: finalize_done", "target", targetAgentID, "elapsed_ms", time.Since(classStart).Milliseconds())
		classification, targetAgentID = g.applyConversationFlow(ctx, request, classification, targetAgentID)
		guideFileLog().Info("DEBUG: conversation_flow_done", "target", targetAgentID, "elapsed_ms", time.Since(classStart).Milliseconds())
		return classification, targetAgentID, nil
	}

	classification, targetAgentID, ok := g.cachedClassification(request)
	if ok {
		guideFileLog().Info("DEBUG: classify_cache_hit", "target", targetAgentID, "elapsed_ms", time.Since(classStart).Milliseconds())
		classification = g.applyDomainHints(classification, domainCtx)
		classification, targetAgentID = g.finalizeClassificationWithExclude(ctx, request.Input, classification, targetAgentID)
		classification, targetAgentID = g.applyConversationFlow(ctx, request, classification, targetAgentID)
		return classification, targetAgentID, nil
	}

	guideFileLog().Info("DEBUG: classify_llm_start", "elapsed_ms", time.Since(classStart).Milliseconds())
	classification, targetAgentID, err := g.classifyWithSingleflight(ctx, request, domainCtx, false)
	guideFileLog().Info("DEBUG: classify_llm_done", "target", targetAgentID, "elapsed_ms", time.Since(classStart).Milliseconds(), "error", err)
	if err != nil {
		return nil, "", err
	}
	guideFileLog().Info("DEBUG: finalize_start", "target", targetAgentID, "elapsed_ms", time.Since(classStart).Milliseconds())
	classification, targetAgentID = g.finalizeClassificationWithExclude(ctx, request.Input, classification, targetAgentID)
	guideFileLog().Info("DEBUG: finalize_done", "target", targetAgentID, "elapsed_ms", time.Since(classStart).Milliseconds())
	classification, targetAgentID = g.applyConversationFlow(ctx, request, classification, targetAgentID)
	guideFileLog().Info("DEBUG: conversation_flow_done", "target", targetAgentID, "elapsed_ms", time.Since(classStart).Milliseconds())
	return classification, targetAgentID, nil
}

// tryConversationFastPath checks whether the conversation flow manager has a
// strong enough active agent to skip LLM classification entirely. Returns a
// synthetic RouteResult, the target agent ID, and true when the fast-path fires.
func (g *Guide) tryConversationFastPath(ctx context.Context, request *RouteRequest) (*RouteResult, string, bool) {
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
	if g.resolveAgent(activeAgentID) == nil {
		// Agent may have been demoted — try on-demand activation.
		resolved := g.ensureClassifiedTargetReady(ctx, activeAgentID)
		if g.resolveAgent(resolved) == nil {
			return nil, "", false
		}
		activeAgentID = resolved
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
	// NOTE: Do NOT call observeRoutedConversationTarget here. The fast path
	// must not boost its own ACT-R activation — otherwise each hit refreshes
	// UpdatedAt and increments Turns, creating a self-reinforcing loop that
	// prevents the score from ever decaying below threshold.
	return result, activeAgentID, true
}

// applyPendingPlanMetadata attaches plan_id and epoch metadata to a
// classification when the LLM classifier routes to the pending plan's agent
// with a planning intent. Clears the pending plan on consume.
func (g *Guide) applyPendingPlanMetadata(sessionID string, classification *RouteResult) {
	if g == nil || g.conversation == nil || classification == nil {
		return
	}
	pending := g.conversation.PendingPlan(sessionID)
	if pending == nil {
		return
	}
	target := strings.ToLower(string(classification.TargetAgent))
	if target != strings.ToLower(pending.AgentID) {
		return
	}
	switch {
	case classification.Intent == IntentExecute && classification.Domain == DomainPlanning:
		classification.PhaseMetadata = map[string]any{"plan_id": pending.PlanID, "epoch": pending.Epoch}
		g.conversation.ClearPendingPlan(sessionID)
	case classification.Intent == IntentPlan && classification.Domain == DomainPlanning:
		classification.PhaseMetadata = map[string]any{"plan_id": pending.PlanID, "epoch": pending.Epoch}
		g.conversation.ClearPendingPlan(sessionID)
	}
}

func (g *Guide) explicitTargetClassification(targetAgentID string) *RouteResult {
	target := strings.TrimSpace(targetAgentID)
	intent := g.defaultIntentForTarget(target)
	return &RouteResult{
		TargetAgent:          TargetAgent(target),
		Intent:               intent,
		Domain:               DomainGeneral,
		Confidence:           1.0,
		Action:               RouteActionExecute,
		ClassificationMethod: "explicit",
		Reason:               "explicit target requested",
	}
}

// defaultIntentForTarget returns the first declared intent for the target
// agent, falling back to IntentHelp when the agent is unknown or has no
// declared intents. This prevents explicit-target routing from assigning
// IntentHelp to agents that don't declare it (e.g. Engineer → IntentComplete,
// Designer → IntentDesign), which would cause ensureRoutableClassification
// to demote the request to the Guide.
func (g *Guide) defaultIntentForTarget(targetAgentID string) Intent {
	agent := g.resolveAgent(targetAgentID)
	if agent == nil || len(agent.Capabilities.Intents) == 0 {
		return IntentHelp
	}
	return agent.Capabilities.Intents[0]
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
	cc := ClassificationContext{
		SessionID:               request.SessionID,
		ActiveConversationAgent: snapshot.ActiveAgentID,
		ActiveConversationTurns: snapshot.Turns,
		ActiveConversationAge:   int(snapshot.Age.Seconds()),
		ActiveConversationScore: snapshot.ActivationHint,
	}
	if pp := g.conversation.PendingPlan(request.SessionID); pp != nil {
		cc.PendingPlanID = pp.PlanID
		cc.PendingPlanAgent = pp.AgentID
	}
	return withClassificationContext(ctx, cc)
}

// classificationProgressKey is a context key for emitting classification
// progress messages (e.g. "Activating Architect...") back to the UI.
type classificationProgressKey struct{}

func withClassificationProgress(ctx context.Context, emit func(string)) context.Context {
	return context.WithValue(ctx, classificationProgressKey{}, emit)
}

func emitClassificationProgress(ctx context.Context, message string) {
	emit, _ := ctx.Value(classificationProgressKey{}).(func(string))
	if emit != nil && strings.TrimSpace(message) != "" {
		emit(message)
	}
}

func (g *Guide) finalizeClassification(ctx context.Context, input string, classification *RouteResult, targetAgentID string) (*RouteResult, string) {
	classification, targetAgentID = g.ensureRoutableClassification(ctx, classification, targetAgentID)
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
	classification, targetAgentID = g.finalizeClassification(ctx, input, classification, targetAgentID)
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

func (g *Guide) ensureRoutableClassification(ctx context.Context, classification *RouteResult, targetAgentID string) (*RouteResult, string) {
	target := classificationTargetAgentID(targetAgentID, classification)
	classification = g.normalizeConversationalIntentForTarget(classification, target)
	if g.classificationSupportedByTarget(classification, target) {
		return classification, target
	}
	// Target is not routable — attempt on-demand activation before falling
	// back to guide. This is the universal activation gate: every
	// classification path (explicit, cached, LLM, conversation flow) flows
	// through here, so a single activation check covers all of them.
	resolved := g.ensureClassifiedTargetReady(ctx, target)
	if resolved != target {
		target = resolved
		classification.TargetAgent = TargetAgent(target)
	}
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
	// If the classifier says IntentChat and the target agent explicitly
	// supports IntentChat, forward directly — no promotion needed.
	if classification.Intent == IntentChat && g.agentSupportsIntent(targetAgentID, IntentChat) {
		return classification, targetAgentID
	}
	// Otherwise, if the target supports IntentHelp (conversational), promote
	// chat to help and forward instead of redirecting to guide. This lets
	// agents handle substantive conversations without being blocked.
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

// resolveAgent looks up an agent registration by ID first, then by name/alias.
// Agents like the tester register with UUID-based IDs but are referenced by
// type name ("tester") in routing requests, so the name fallback is essential.
func (g *Guide) resolveAgent(idOrName string) *AgentRegistration {
	if g == nil || g.registry == nil {
		return nil
	}
	if agent := g.registry.Get(idOrName); agent != nil {
		return agent
	}
	return g.registry.GetByName(idOrName)
}

// resolveAgentID converts a name/alias to the actual registration ID.
// Returns the input unchanged when no registration is found.
func (g *Guide) resolveAgentID(idOrName string) string {
	if agent := g.resolveAgent(idOrName); agent != nil {
		return agent.ID
	}
	return idOrName
}

// resolveAgentDisplayName converts a UUID-based agent ID to the
// human-readable registration name (e.g. "a1b2c3d4" → "inspector").
// Returns the input unchanged when no registration is found.
func (g *Guide) resolveAgentDisplayName(agentID string) string {
	if agent := g.resolveAgent(agentID); agent != nil && agent.Name != "" {
		return agent.Name
	}
	return agentID
}

// resolveActivationType maps an agent ID (UUID or type name) to the agent type
// name expected by the activation controller. Returns the input unchanged when
// no mapping exists — the caller should assume it is already a type name.
func (g *Guide) resolveActivationType(idOrName string) string {
	// Fast path: routing aggregator knows both UUID-keyed and name-keyed agents.
	if info := g.routing.GetRoutingInfo(idOrName); info != nil && info.Type != "" {
		return info.Type
	}
	// Persistent index survives agent unregistration (e.g. after demotion).
	if agentType, ok := g.typeIndex.Get(idOrName); ok {
		return agentType
	}
	return idOrName
}

// ensureExplicitTargetReady activates and registers the target agent if it
// isn't already known to the Guide. On-demand agents (tester, inspector) may
// not have been pre-activated during bootstrap — their containers are created
// lazily. This method bridges the gap by:
//  1. Calling the activation hook (creates the container and starts the agent).
//  2. Calling the agent registrar (registers routing info: capabilities,
//     intents, channels) synchronously before routing checks capabilities.
//
// Both steps are idempotent — they no-op if the agent is already active/registered.
//
// Returns the resolved agent ID (which may differ from targetAgentID when
// re-activation creates a new agent with a fresh UUID) and an error when the
// agent cannot be activated or remains unresolvable after activation.
func (g *Guide) ensureExplicitTargetReady(ctx context.Context, targetAgentID string) (string, error) {
	if g.resolveAgent(targetAgentID) != nil && g.IsAgentReady(targetAgentID) {
		return targetAgentID, nil
	}

	// Resolve UUID → type for the activation controller, which indexes
	// by type name ("tester"), not by agent UUID.
	activationType := g.resolveActivationType(targetAgentID)

	if g.activator != nil {
		deadline, hasDL := ctx.Deadline()
		guideFileLog().Info("DEBUG: guide_ensure_active_call",
			"target", targetAgentID,
			"activation_type", activationType,
			"has_deadline", hasDL,
			"deadline", deadline)
		activateStart := time.Now()
		if err := g.activator.EnsureActive(ctx, activationType); err != nil {
			guideFileLog().Info("DEBUG: guide_ensure_active_failed",
				"target", targetAgentID,
				"elapsed_ms", time.Since(activateStart).Milliseconds(),
				"error", err)
			return "", fmt.Errorf("activate %s: %w", targetAgentID, err)
		}
		guideFileLog().Info("DEBUG: guide_ensure_active_ok",
			"target", targetAgentID,
			"elapsed_ms", time.Since(activateStart).Milliseconds())
	}
	if g.agentRegistrar != nil {
		g.agentRegistrar(activationType)
	}

	// The original UUID still resolves — agent was just paused, not replaced.
	if g.resolveAgent(targetAgentID) != nil {
		return targetAgentID, nil
	}
	// Re-activation created a new agent with a fresh UUID. Resolve via
	// the type name which is indexed by the registry's name lookup.
	if resolved := g.resolveAgentID(activationType); g.resolveAgent(resolved) != nil {
		return resolved, nil
	}
	return "", fmt.Errorf("agent %q not available after activation", targetAgentID)
}

// ensureClassifiedTargetReady activates a non-guide target agent on demand.
// Called from classification and conversation fast-path before routability
// checks. Errors are swallowed — the downstream routability check falls back
// to guide gracefully. Returns the resolved agent ID (may differ from input
// after cold-start creates a new UUID).
func (g *Guide) ensureClassifiedTargetReady(ctx context.Context, targetAgentID string) string {
	if targetAgentID == "" || g.isGuideTarget(targetAgentID) {
		return targetAgentID
	}
	// An agent is fully ready only when it has been activated (channels
	// created, bus subscriptions wired) AND marked ready. Pre-registered
	// agents are resolvable but not ready — they still need activation.
	if g.resolveAgent(targetAgentID) != nil && g.IsAgentReady(targetAgentID) {
		guideFileLog().Info("DEBUG: target_already_registered", "target", targetAgentID)
		if g.activator != nil {
			g.activator.TouchActivity(g.resolveActivationType(targetAgentID))
		}
		return targetAgentID
	}
	displayName := g.resolveAgentDisplayName(targetAgentID)
	guideFileLog().Info("DEBUG: target_not_registered_activating", "target", targetAgentID, "display_name", displayName, "activation_type", g.resolveActivationType(targetAgentID))
	emitClassificationProgress(ctx, "Activating "+displayName+"...")
	activateStart := time.Now()
	resolved, err := g.ensureExplicitTargetReady(ctx, targetAgentID)
	guideFileLog().Info("DEBUG: activation_done", "target", targetAgentID, "resolved", resolved, "elapsed_ms", time.Since(activateStart).Milliseconds(), "error", err)
	if err != nil {
		return targetAgentID
	}
	if g.activator != nil {
		g.activator.TouchActivity(g.resolveActivationType(resolved))
	}
	if resolved != "" {
		return resolved
	}
	return targetAgentID
}

func (g *Guide) agentSupportsIntent(targetAgentID string, intent Intent) bool {
	if intent == "" || intent == IntentUnknown {
		return false
	}
	agent := g.resolveAgent(targetAgentID)
	if agent == nil {
		return false
	}
	return agent.Capabilities.SupportsIntent(intent)
}

func (g *Guide) agentSupportsDomain(targetAgentID string, domain Domain) bool {
	if domain == "" || domain == DomainUnknown {
		return false
	}
	agent := g.resolveAgent(targetAgentID)
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
	agent := g.resolveAgent(targetAgentID)
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
		Metadata:             classification.PhaseMetadata,
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
	go func() { _ = g.bus.Publish(g.agentRequestTopic("archivalist"), msg) }()
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

	// Persist UUID → type mapping for activation resolution.
	// Never cleared — bounded to ≤16 agent registrations.
	if info.Type != "" {
		g.typeIndex.Set(info.ID, info.Type)
	}

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
		// If existing subscriptions are still active, skip teardown/recreation.
		// Register may be called on every forwarded request (via agentRegistrar);
		// tearing down active subscriptions creates a delivery gap where
		// STREAM_COMPLETE and RouteResponse messages are lost.
		if existing, ok := g.agentSubs.Get(info.ID); ok {
			if existing.responses != nil && existing.responses.IsActive() &&
				existing.errors != nil && existing.errors.IsActive() {
				return nil
			}
			// Existing subs are stale — clean up before creating new ones.
			if existing.responses != nil {
				_ = existing.responses.Unsubscribe()
			}
			if existing.errors != nil {
				_ = existing.errors.Unsubscribe()
			}
			g.agentSubs.Delete(info.ID)
		}
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

// PreRegister registers static routing info for an agent that hasn't
// activated yet. The agent becomes visible to the classifier and
// routable (resolveAgent finds it) but not ready for requests.
// Channels, subscriptions, and bus announcements are deferred to
// full Register() after activation.
func (g *Guide) PreRegister(info *AgentRoutingInfo) error {
	if info == nil {
		return fmt.Errorf("routing info is nil")
	}
	g.routing.RegisterAgent(info)
	if info.Type != "" {
		g.typeIndex.Set(info.ID, info.Type)
	}
	if info.Registration != nil {
		g.registry.Register(info.Registration)
	}
	g.router.parser.SetRouting(g.routing)
	return nil
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

	// Subscribe to credential changes so the Guide refreshes its own
	// Google provider when the user authenticates after startup.
	// This replaces the legacy auto-upgrade polling loop — the AuthRegistry
	// broadcasts events reactively when credentials become available,
	// eliminating the 15-second polling interval that contributed to
	// rate limiting.
	authSub, authErr := g.bus.SubscribeAsync(TopicAuthCredentials, g.handleAuthChanged)
	if authErr != nil {
		slog.Warn("guide: failed to subscribe to auth credentials topic", "error", authErr)
	} else {
		g.authSub = authSub
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
	g.unsubscribeAuth(errs)
	g.unsubscribeAgentChannels(errs)
}

func (g *Guide) unsubscribeAuth(errs *[]error) {
	if g.authSub == nil {
		return
	}
	if err := g.authSub.Unsubscribe(); err != nil {
		*errs = append(*errs, err)
	}
	g.authSub = nil
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
	guideFileLog().Info("guide: handleRouteRequestMessage",
		"source_agent", req.SourceAgentID,
		"target_agent", req.TargetAgentID,
		"input_len", len(req.Input),
		"input_preview", truncateLogStr(req.Input, 100),
		"session_id", req.SessionID,
		"msg_type", msg.Type,
		"msg_source", msg.SourceAgentID)
	correlationID := routeCorrelationID(msg, req)
	req.CorrelationID = correlationID
	reqCtx, cancel := context.WithTimeout(ctx, defaultGuideRequestTimeout)
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

	vis := classifyRequestVisibility(req)
	reqCtx = withClassificationProgress(reqCtx, func(message string) {
		g.publishGuideStreamProgress(correlationID, req.SourceAgentID, message, vis)
	})

	g.publishActivity(events.EventTypeAgentDecision, vis, "Classifying request...")
	routeStart := time.Now()
	guideFileLog().Info("DEBUG: route_start", "correlation_id", correlationID, "input_preview", truncateLogStr(req.Input, 80))
	forwarded, err := g.routeWithRetry(reqCtx, req, correlationID, req.SourceAgentID, vis)
	guideFileLog().Info("DEBUG: route_done", "correlation_id", correlationID, "elapsed_ms", time.Since(routeStart).Milliseconds(), "error", err)
	if err != nil {
		if g.isInterruptError(err) {
			guideFileLog().Info("DEBUG: route_interrupted", "correlation_id", correlationID, "error", err)
			return nil
		}
		guideFileLog().Info("DEBUG: route_error", "correlation_id", correlationID, "error", err)
		return g.publishRouteError(correlationID, req.SourceAgentID, err)
	}
	pending := g.pending.Get(forwarded.CorrelationID)
	if pending == nil {
		return fmt.Errorf("no pending request found for correlation ID: %s", forwarded.CorrelationID)
	}
	guideFileLog().Info("DEBUG: route_resolved", "correlation_id", correlationID, "target", pending.TargetAgentID, "is_guide", g.isGuideTarget(pending.TargetAgentID))
	if g.isGuideTarget(pending.TargetAgentID) {
		return g.respondToGuideRequest(reqCtx, pending, req)
	}
	g.publishActivity(events.EventTypeAgentAction, vis, "Routing to "+pending.TargetAgentID)
	g.publishRouteHandoffProgress(forwarded.CorrelationID, pending.SourceAgentID, pending.TargetAgentID, vis)
	return g.publishForwardedRequest(pending.TargetAgentID, forwarded, msg.ReplyTo)
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

	reqCtx, cancel := context.WithTimeout(ctx, defaultGuideRequestTimeout)
	g.registerRequestCancel(correlationID, cancel)
	defer g.clearRequestCancel(correlationID)
	defer cancel()

	// Inject exclude list into context for loop prevention.
	reqCtx = withRerouteExcludeAgents(reqCtx, reroute.ExcludeAgents)

	vis := classifyRequestVisibility(req)
	reqCtx = withClassificationProgress(reqCtx, func(message string) {
		g.publishGuideStreamProgress(correlationID, req.SourceAgentID, message, vis)
	})

	forwarded, err := g.routeWithRetry(reqCtx, req, correlationID, req.SourceAgentID, vis)
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
	g.publishRouteHandoffProgress(forwarded.CorrelationID, pending.SourceAgentID, pending.TargetAgentID, classifyRequestVisibility(req))
	return g.publishForwardedRequest(pending.TargetAgentID, forwarded, msg.ReplyTo)
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
	_ = g.bus.Publish(g.agentResponseTopic(sourceAgentID), msg)
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
	// Direct skill invocation for structured requests (e.g. plan acceptance).
	// The architect sends "evaluate-plan-acceptance: {json}" — intercept here
	// so the skill handler returns map[string]any instead of an LLM string.
	if data, ok := g.tryDirectSkillInvocation(ctx, req.Input); ok {
		resp := &RouteResponse{
			CorrelationID:       pending.CorrelationID,
			RespondingAgentID:   g.agentID,
			RespondingAgentName: "Guide",
			Success:             true,
			Data:                data,
		}
		resolved, handleErr := g.HandleResponse(ctx, resp)
		if handleErr != nil {
			return nil
		}
		return g.publishResponseToSource(resolved.SourceAgentID, resp)
	}

	g.publishGuideStreamStart(pending.CorrelationID, pending.SourceAgentID)
	ctx = withGuideThoughtEmitter(ctx, func(thought string) {
		g.publishGuideStreamProgress(pending.CorrelationID, pending.SourceAgentID, thought, events.VisibilityUser)
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
	// Self-response tool loop routed to a non-guide agent — complete the
	// guide stream cleanly and let the target agent take over.
	if errors.Is(err, skills.ErrRerouteRequested) {
		g.publishGuideStreamComplete(pending.CorrelationID, pending.SourceAgentID, nil)
		_, _ = g.HandleResponse(ctx, &RouteResponse{
			CorrelationID:       pending.CorrelationID,
			RespondingAgentID:   g.agentID,
			RespondingAgentName: "Guide",
			Success:             true,
		})
		return nil
	}
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
	g.appendSelfResponseTurn(input, reply)
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

// SetProviderWrapper stores a callback that re-applies gateway rate limiting
// to fresh providers created during credential refresh.
func (g *Guide) SetProviderWrapper(w gateway.ProviderWrapper) {
	g.providerWrapper = w
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
	case "apikey", providers.GoogleAuthModeAPIKey:
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
	promptSkills := DiscoverGuidePromptSkills(g.skills.GetAll())
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
	var wrapped providers.ProviderAdapter = provider
	if g.providerWrapper != nil {
		wrapped = g.providerWrapper(provider)
	}
	modelID := cfgOverride.Model
	g.providerMu.Lock()
	g.provider = wrapped
	g.activeModel = modelID
	g.googleConfig = &cfgOverride
	g.providerMu.Unlock()

	cfg := g.config.RouterConfig
	llmClassifier := NewLLMClassifier(wrapped, modelID, cfg)
	g.SetClassifier(NewFallbackClassifier(llmClassifier, NewRuleClassifierService()))
	g.SetSelfResponder(resolveGuideSelfResponder(g.config, wrapped, modelID, g))
	g.clearSelfResponseHistory()
	if cache := g.RouteCache(); cache != nil {
		cache.Clear()
	}
	return nil
}


// SwapModel implements container.ModelSwappable.
// Installs the pre-built, gateway-wrapped provider and rewires the
// classifier and self-responder. Thread-safe.
func (g *Guide) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	if g == nil {
		return nil
	}
	g.providerMu.Lock()
	g.provider = provider
	g.activeModel = modelID
	g.providerMu.Unlock()

	cfg := g.config.RouterConfig
	classifier := NewLLMClassifier(provider, modelID, cfg)
	g.SetClassifier(NewFallbackClassifier(classifier, NewRuleClassifierService()))
	g.SetSelfResponder(resolveGuideSelfResponder(g.config, provider, modelID, g))
	g.clearSelfResponseHistory()
	if cache := g.RouteCache(); cache != nil {
		cache.Clear()
	}
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (g *Guide) CurrentModel() string {
	g.providerMu.RLock()
	defer g.providerMu.RUnlock()
	if g.activeModel != "" {
		return g.activeModel
	}
	if g.googleConfig == nil {
		return ""
	}
	return g.googleConfig.Model
}

// SupportedModels implements container.ModelSwappable.
func (g *Guide) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
	}
}

func (g *Guide) newGuideSelfResponseRequest(input string, sessionID string) GuideSelfResponseRequest {
	request := GuideSelfResponseRequest{
		Input:               input,
		AgentID:             g.agentID,
		SessionID:           sessionID,
		PendingRequests:     g.pending.Count(),
		RegisteredAgentIDs:  g.registeredAgentIDs(),
		LoadedSkillNames:    g.loadedGuideSkillNames(),
		ConversationHistory: g.selfResponseHistorySnapshot(),
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

// maxSelfResponseHistory is the maximum number of messages (user+assistant
// pairs count as 2) retained for conversation context in self-responses.
const maxSelfResponseHistory = 10

// selfResponseHistorySnapshot returns a copy of the current conversation
// history buffer. Safe for concurrent access.
func (g *Guide) selfResponseHistorySnapshot() []providers.Message {
	g.selfResponseMu.RLock()
	defer g.selfResponseMu.RUnlock()
	if len(g.selfResponseHistory) == 0 {
		return nil
	}
	snapshot := make([]providers.Message, len(g.selfResponseHistory))
	copy(snapshot, g.selfResponseHistory)
	return snapshot
}

// appendSelfResponseTurn records a user→assistant exchange in the bounded
// conversation history buffer. Evicts the oldest pair when full.
func (g *Guide) appendSelfResponseTurn(userInput string, assistantReply string) {
	trimmedInput := strings.TrimSpace(userInput)
	trimmedReply := strings.TrimSpace(assistantReply)
	if trimmedInput == "" || trimmedReply == "" {
		return
	}
	g.selfResponseMu.Lock()
	defer g.selfResponseMu.Unlock()
	g.selfResponseHistory = append(g.selfResponseHistory,
		providers.Message{Role: providers.RoleUser, Content: trimmedInput},
		providers.Message{Role: providers.RoleAssistant, Content: trimmedReply},
	)
	if len(g.selfResponseHistory) > maxSelfResponseHistory {
		excess := len(g.selfResponseHistory) - maxSelfResponseHistory
		// Align to pair boundary (evict full pairs).
		excess = ((excess + 1) / 2) * 2
		g.selfResponseHistory = g.selfResponseHistory[excess:]
	}
}

// clearSelfResponseHistory resets the conversation buffer, e.g. on model swap.
func (g *Guide) clearSelfResponseHistory() {
	g.selfResponseMu.Lock()
	g.selfResponseHistory = nil
	g.selfResponseMu.Unlock()
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

func (g *Guide) routeWithRetry(ctx context.Context, req *RouteRequest, correlationID, sourceAgentID string, visibility events.EventVisibility) (*ForwardedRequest, error) {
	g.publishGuideStreamProgress(correlationID, sourceAgentID, "Classifying request...", visibility)
	result, err := g.Route(ctx, req)
	return result, err
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
	_ = g.bus.Publish(g.agentResponseTopic(sourceAgentID), busMsg)
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

func (g *Guide) publishGuideStreamProgress(correlationID, sourceAgentID, message string, visibility events.EventVisibility) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	event := &StreamEvent{
		Type: StreamEventProgress,
		Data: &ProgressData{
			Message: message,
		},
		Visibility: visibility,
		Timestamp:  time.Now(),
	}
	g.publishGuideStreamEvent(correlationID, sourceAgentID, event)
}

func (g *Guide) publishActivity(eventType events.EventType, visibility events.EventVisibility, content string) {
	if g.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, g.sessionID, content)
	evt.AgentID = g.agentID
	evt.Visibility = visibility
	evt.Data["agent_type"] = "guide"
	evt.Data["agent_name"] = "Guide"
	g.activityPub.PublishActivity(evt)
}

// classifyRequestVisibility determines EventVisibility from a RouteRequest.
// TUI-originated requests are user-visible. Fire-and-forget or agent-sourced
// requests are classified as system or agent respectively.
func classifyRequestVisibility(req *RouteRequest) events.EventVisibility {
	if req == nil {
		return events.VisibilityUser
	}
	if req.FireAndForget {
		return events.VisibilitySystem
	}
	src := strings.TrimSpace(req.SourceAgentID)
	if src == "" || src == "tui" {
		return events.VisibilityUser
	}
	return events.VisibilityAgent
}

func (g *Guide) publishRouteHandoffProgress(correlationID, sourceAgentID, targetAgentID string, visibility events.EventVisibility) {
	targetAgentID = strings.TrimSpace(targetAgentID)
	if targetAgentID == "" {
		return
	}
	event := &StreamEvent{
		Type: StreamEventProgress,
		Data: &ProgressData{
			Message: "Handing off to " + targetAgentID + "...",
		},
		Visibility: visibility,
		Timestamp:  time.Now(),
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
	_ = g.bus.Publish(g.agentResponseTopic(sourceAgentID), msg)
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
		DebugFileLog().Warn("handleResponseMessage: PROCESSING_CTX_CANCELLED",
			"msg_type", string(msg.Type),
			"correlation_id", msg.CorrelationID,
			"source_agent", msg.SourceAgentID,
			"ctx_err", err)
		return nil
	}

	// Touch activity for the responding agent to prevent idle demotion
	// during active conversation flow.
	if g.activator != nil && msg.SourceAgentID != "" {
		g.activator.TouchActivity(msg.SourceAgentID)
	}

	if msg.Type == MessageTypeStream {
		if streamResp, ok := msg.GetStreamResponse(); ok && streamResp.Event != nil {
			if streamResp.Event.Type == StreamEventComplete {
				DebugFileLog().Info("handleResponseMessage: STREAM_COMPLETE_RECEIVED",
					"correlation_id", msg.CorrelationID,
					"source_agent", msg.SourceAgentID,
					"has_directive", streamResp.Event.Directive != nil)
			}
		}
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
		resp.RespondingAgentID = g.resolveAgentDisplayName(resp.RespondingAgentID)

		return g.publishResponseToSource(pending.SourceAgentID, resp)
	case MessageTypeStream:
		streamResp, ok := msg.GetStreamResponse()
		if !ok {
			return fmt.Errorf("invalid stream response payload")
		}

		// Agent-initiated push: create synthetic pending before stream events.
		// Runs in the same subscriber goroutine as subsequent stream events
		// from this agent, so FIFO ordering guarantees the pending exists
		// when StreamStart arrives.
		if streamResp.Event != nil && streamResp.Event.Type == StreamEventPush {
			return g.handleAgentPush(streamResp, msg)
		}

		g.recordIncomingStreamEvent(streamResp)
		g.observeStreamDirective(streamResp)
		g.observeStreamReroute(streamResp)
		pending := g.pending.Get(streamResp.CorrelationID)
		if pending == nil {
			if streamResp.Event != nil && streamResp.Event.Type == StreamEventComplete {
				DebugFileLog().Warn("handleResponseMessage: STREAM_COMPLETE_NO_PENDING",
					"correlation_id", streamResp.CorrelationID,
					"source_agent", msg.SourceAgentID)
			}
			return nil
		}
		streamTarget := g.resolveStreamTarget(pending)
		streamResp.TargetAgentID = streamTarget
		streamResp.RespondingAgentID = g.resolveAgentDisplayName(pending.TargetAgentID)
		return g.publishStreamToSource(streamTarget, streamResp)
	default:
		return nil
	}
}

// handleAgentPush processes a StreamEventPush event from an agent.
// Dispatches to the appropriate handler based on push type.
func (g *Guide) handleAgentPush(streamResp *StreamResponse, msg *Message) error {
	push, ok := streamResp.Event.Data.(*AgentPush)
	if !ok {
		return fmt.Errorf("invalid agent push payload")
	}

	DebugFileLog().Info("guide: handleAgentPush",
		"push_id", push.PushID,
		"agent_id", push.AgentID,
		"push_type", string(push.PushType),
		"source_agent", msg.SourceAgentID)

	switch push.PushType {
	case PushTypeStream:
		return g.handleStreamPush(push, msg)
	case PushTypeNotification:
		return g.handleNotificationPush(push, msg)
	default:
		return fmt.Errorf("unknown push type: %s", push.PushType)
	}
}

// handleStreamPush creates a synthetic pending so subsequent stream events
// from the same agent channel route to the TUI. This runs in the same
// subscriber goroutine as stream events, so FIFO ordering guarantees the
// pending exists when StreamStart arrives.
func (g *Guide) handleStreamPush(push *AgentPush, _ *Message) error {
	pending := &PendingRequest{
		CorrelationID:        push.PushID,
		SourceAgentID:        "tui",
		TargetAgentID:        push.AgentID,
		CreatedAt:            time.Now(),
		ExpiresAt:            time.Now().Add(10 * time.Minute),
		StreamTargetOverride: "tui",
	}
	g.pending.Set(push.PushID, pending)

	g.publishActivity(events.EventTypeAgentAction, events.VisibilityAgent,
		fmt.Sprintf("Agent push registered: %s → user", push.AgentID))

	return nil
}

// handleNotificationPush sends a one-shot stream sequence (Start + Data + Complete)
// to the TUI for a notification push.
func (g *Guide) handleNotificationPush(push *AgentPush, _ *Message) error {
	agentName := g.resolveAgentDisplayName(push.AgentID)

	for _, event := range []*StreamEvent{
		{Type: StreamEventStart, Timestamp: time.Now()},
		{Type: StreamEventData, Text: push.Content, Timestamp: time.Now()},
		{Type: StreamEventComplete, Timestamp: time.Now()},
	} {
		resp := &StreamResponse{
			CorrelationID:     push.PushID,
			RespondingAgentID: agentName,
			TargetAgentID:     "tui",
			Event:             event,
		}
		g.publishStreamToSource("tui", resp)
	}

	g.publishActivity(events.EventTypeAgentAction, events.VisibilityAgent,
		fmt.Sprintf("Agent notification: %s → user", push.AgentID))

	return nil
}

func (g *Guide) recordIncomingStreamEvent(resp *StreamResponse) {
	if g == nil || g.streams == nil || resp == nil || resp.Event == nil {
		return
	}
	stream := g.streams.GetStream(resp.CorrelationID)
	if stream == nil {
		if resp.Event.Type == StreamEventComplete {
			DebugFileLog().Warn("recordIncomingStreamEvent: STREAM_COMPLETE_NO_STREAM",
				"correlation_id", resp.CorrelationID)
		}
		return
	}
	if resp.Event.Type == StreamEventComplete {
		DebugFileLog().Info("recordIncomingStreamEvent: FORWARDING_COMPLETE",
			"correlation_id", resp.CorrelationID,
			"stream_closed", stream.IsClosed())
	}
	stream.ForwardEvent(resp.Event)
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
	// Epoch-based reclamation: sweep all state tagged with this
	// correlation epoch. Prevents stale fast-path routing, ghost
	// streams, and orphaned request contexts.
	g.reclaimRequestEpoch(correlationID, pending)
	if pending != nil {
		g.forwardUserInterruptToTarget(req, pending)
	}
	if g.streams != nil {
		g.streams.CloseStream(correlationID)
	}
	return nil
}

// reclaimRequestEpoch sweeps all request-scoped state tagged with the given
// correlation epoch. Called on abort to prevent stale routing, ghost sessions,
// and orphaned caches. The pattern is extensible — any new request-scoped
// state only needs an entry here.
func (g *Guide) reclaimRequestEpoch(correlationID string, pending *PendingRequest) {
	sid := g.resolveSessionID(pending)

	// 1. Conversation flow: clear routing stickiness so the next
	//    message goes through full LLM classification, but preserve
	//    conversation history so the agent retains context.
	if g.conversation != nil && sid != "" {
		g.conversation.ClearStickiness(sid)
		if pending != nil && pending.TargetAgentID != "" {
			g.conversation.RecordInterruption(sid, pending.TargetAgentID)
		}
	}

	// 2. Route cache: invalidate any cached classification that would
	//    short-circuit the next request to the same (now-stale) route.
	if g.routeCache != nil && pending != nil && pending.TargetAgentID != "" {
		g.routeCache.InvalidateForAgent(pending.TargetAgentID)
	}

	// 3. Session router: remove per-session route bindings so the
	//    session doesn't carry stale agent preferences.
	if g.sessionRouter != nil && sid != "" {
		g.sessionRouter.RemoveSession(sid)
	}
}

// resolveSessionID extracts the session ID from a pending request,
// falling back to the guide's own session ID.
func (g *Guide) resolveSessionID(pending *PendingRequest) string {
	if pending != nil && pending.Request != nil {
		if sid := strings.TrimSpace(pending.Request.SessionID); sid != "" {
			return sid
		}
	}
	return g.sessionID
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
	_ = g.bus.Publish(g.agentRequestTopic(pending.TargetAgentID), msg)
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
	// Carry the correlation epoch so the target agent can independently
	// reclaim its own request-scoped state — no coupling required.
	if pending.CorrelationID != "" {
		data["epoch"] = pending.CorrelationID
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

// agentRequestTopic returns the request topic for a registered agent,
// falling back to TopicRequests(id, id) for unregistered agents (e.g. guide itself).
func (g *Guide) agentRequestTopic(agentID string) string {
	if ch, ok := g.agentChannels.Get(agentID); ok {
		return ch.Requests
	}
	return TopicRequests(agentID, agentID)
}

// agentResponseTopic returns the response topic for a registered agent,
// falling back to TopicResponses(id, id) for unregistered agents (e.g. guide itself).
func (g *Guide) agentResponseTopic(agentID string) string {
	if ch, ok := g.agentChannels.Get(agentID); ok {
		return ch.Responses
	}
	return TopicResponses(agentID, agentID)
}

func (g *Guide) publishRouteError(correlationID string, sourceAgentID string, err error) error {
	errMsg := NewErrorMessage(generateMessageID(), correlationID, g.agentID, err.Error())
	return g.bus.Publish(g.agentResponseTopic(sourceAgentID), errMsg)
}

func (g *Guide) publishForwardedRequest(targetAgentID string, forwarded *ForwardedRequest, replyTo *ReplyTo) error {
	// Resolve name/alias to the actual registration ID. Agents with UUID-based
	// IDs (tester, inspector) register channels under the UUID but routing uses
	// the type name ("tester", "inspector") as the target.
	resolved := g.resolveAgentID(targetAgentID)

	// The activation controller indexes by type name, not UUID.
	activationType := g.resolveActivationType(resolved)

	if g.activator != nil {
		if err := g.activator.EnsureActive(g.processingContext(), activationType); err != nil {
			// Check if agent is already reachable despite activation error.
			if g.resolveAgent(resolved) == nil {
				return fmt.Errorf("activate %s for forwarding: %w", activationType, err)
			}
		}
	}

	// Re-register routing info after activation (handles new UUIDs from cold start).
	// Skip when the resolved agent already has active bus subscriptions —
	// re-registration is only needed when activation created a fresh agent.
	if g.agentRegistrar != nil && !g.hasActiveAgentSubs(resolved) {
		g.agentRegistrar(activationType)
	}

	// Re-resolve — cold start may create new agent with fresh UUID.
	resolved = g.resolveAgentID(targetAgentID)

	if g.activator != nil {
		g.activator.TouchActivity(activationType)
	}

	// Weighted target resolution: during overlap, multiple endpoints may
	// exist for the same agent type. Select based on quality weights.
	resolvedTarget := g.resolveWeightedTarget(resolved)

	fwdMsg := g.forwardMessage(resolvedTarget, forwarded, replyTo)
	return g.bus.Publish(g.agentRequestTopic(resolvedTarget), fwdMsg)
}

func (g *Guide) forwardMessage(targetAgentID string, forwarded *ForwardedRequest, replyTo *ReplyTo) *Message {
	fwdMsg := NewForwardMessage(generateMessageID(), forwarded)
	fwdMsg.TargetAgentID = targetAgentID
	fwdMsg.ReplyTo = replyTo
	return fwdMsg
}

func (g *Guide) publishResponseToSource(sourceAgentID string, resp *RouteResponse) error {
	respMsg := NewResponseMessage(generateMessageID(), resp)
	return g.bus.Publish(g.agentResponseTopic(sourceAgentID), respMsg)
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
	topic := g.agentResponseTopic(sourceAgentID)
	err := g.bus.Publish(topic, msg)
	if resp.Event != nil && resp.Event.Type == StreamEventComplete {
		DebugFileLog().Info("publishStreamToSource: STREAM_COMPLETE_FORWARDED",
			"correlation_id", resp.CorrelationID,
			"target", sourceAgentID,
			"topic", topic,
			"publish_err", err)
	}
	return err
}

func (g *Guide) publishErrorToSource(sourceAgentID string, correlationID string, sourceAgent string, errStr string) error {
	errMsg := NewErrorMessage(generateMessageID(), correlationID, sourceAgent, errStr)
	return g.bus.Publish(g.agentResponseTopic(sourceAgentID), errMsg)
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
	modelID := g.CurrentModel()
	if modelID == "" {
		modelID = "haiku-4.5-200k"
	}
	return handoff.AgentDescriptor{
		AgentType:     "guide",
		ModelID:       modelID,
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

// SetActivator installs the on-demand agent activator used before
// forwarding requests and after response handling to manage container
// lifecycle (activation and idle timer reset).
func (g *Guide) SetActivator(a AgentActivator) {
	g.activator = a
}

// SetAgentRegistrar sets a callback invoked after on-demand activation to
// register the agent's routing info with the Guide. This bridges the gap
// between container activation (which only marks readiness) and Guide
// registration (which provides capabilities, intents, and channels).
func (g *Guide) SetAgentRegistrar(registrar func(agentType string)) {
	g.agentRegistrar = registrar
}

// hasActiveAgentSubs returns true if the given agent ID has active bus
// subscriptions (both response and error channels). Used to skip redundant
// agentRegistrar calls on forwarded requests.
//
// When the direct UUID lookup misses, falls back to a type-based scan:
// resolve agentID → type via typeIndex, then check if ANY agent ID with
// the same type has active subscriptions. This prevents re-registration
// churn when a cold-started agent receives a new UUID but the old UUID's
// subscriptions are still alive.
func (g *Guide) hasActiveAgentSubs(agentID string) bool {
	if g.isActiveAgentSub(agentID) {
		return true
	}

	// Fallback: resolve to agent type and scan all agentSubs for a match.
	agentType := g.resolveActivationType(agentID)
	if agentType == "" || agentType == agentID {
		return false
	}

	found := false
	g.agentSubs.Range(func(id string, subs *agentSubscriptions) bool {
		if id == agentID {
			return true // already checked above
		}
		resolvedType, ok := g.typeIndex.Get(id)
		if !ok || resolvedType != agentType {
			return true
		}
		if subs.responses != nil && subs.responses.IsActive() &&
			subs.errors != nil && subs.errors.IsActive() {
			found = true
			return false // stop iteration
		}
		return true
	})
	return found
}

// isActiveAgentSub checks a single agent ID for active subscriptions.
func (g *Guide) isActiveAgentSub(agentID string) bool {
	subs, ok := g.agentSubs.Get(agentID)
	if !ok || subs == nil {
		return false
	}
	return subs.responses != nil && subs.responses.IsActive() &&
		subs.errors != nil && subs.errors.IsActive()
}

// SetServiceRegistry sets the health checker used for routing decisions.
// When set, isAgentHealthy delegates to the registry instead of the
// local readyAgents map.
func (g *Guide) SetServiceRegistry(reg ServiceHealthChecker) {
	g.serviceRegistry = reg
}

// handleAuthChanged processes credential change events from the AuthRegistry.
// Currently reacts to "google" provider changes for credential refresh.
func (g *Guide) handleAuthChanged(msg *Message) error {
	ev, ok := msg.GetAuthEvent()
	if !ok || ev == nil {
		return nil
	}
	if ev.ProviderType != "google" || !ev.Available {
		return nil
	}
	ctx := g.processingContext()
	if err := g.RefreshAuthWithMethod(ctx, ev.AuthMethod); err != nil {
		slog.Warn("guide: auth refresh from bus event failed",
			"method", ev.AuthMethod,
			"error", err)
	}
	return nil
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
