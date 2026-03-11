package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/signal"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// =============================================================================
// Architect Agent
// =============================================================================

// Architect is the system design and planning agent for the Sylk system.
// It handles Pre-Delegation Planning Protocol and Atomic Task Generation.
// The Architect consults with Librarian for codebase patterns and creates
// workflow DAGs for task orchestration.
type Architect struct {
	id     string
	config Config
	logger *slog.Logger
	logWAL io.Closer

	// Cross-domain handling
	crossDomainHandler *CrossDomainHandler
	synthesizer        *ResultSynthesizer

	// Skills system
	skills      *skills.Registry
	skillLoader *skills.Loader
	hooks       *skills.HookRegistry
	tools       *toolruntime.Runtime
	planner     planningLLM
	plannerMu   sync.RWMutex

	// Activity publisher for UI agent-panel updates
	activityPub events.ActivityPublisher

	// Event bus integration
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement

	// Planning state — plans live in an externally-owned PlanStore singleton
	// that survives agent demotion/re-promotion.
	planStore        *PlanStore
	ownsPlanStore    bool
	controlStore     *ArchitectControlStore
	ownsControlStore bool
	planModes        map[string]*PlanModeState

	runMu         sync.RWMutex
	runCtx        context.Context
	runCancel     context.CancelFunc
	knownAgentsMu sync.RWMutex
	planModesMu   sync.RWMutex
	pendingMu     sync.Mutex
	pendingBus    map[string]chan *guide.Message
	inFlightMu    sync.Mutex
	inFlight      map[string]context.CancelFunc

	// Handoff bridge integration
	handoffBridge *handoff.HandoffBridge

	// Agent pod for cross-agent coordination (Scribe feed, etc.).
	agentPod *shared.AgentPod

	// File access abstraction (DiskFileAccess, set at creation).
	fileAccess     versioning.FileAccess
	workspaceViews versioning.WorkspaceViewAccess

	// toolDefsDirty is set when demand-paged skills are loaded mid-loop,
	// triggering a tool definition rebuild on the next LLM turn.
	toolDefsDirty bool

	// Signal bus for broadcasting degradation signals.
	signalBus     signal.SignalBusInterface
	signalAdapter *guide.ChannelBusSignalAdapter // non-nil only when self-created

	// Heartbeat subscription for executing plan lease renewal.
	heartbeatSub guide.Subscription

	// Steering ledger management.
	steering *shared.SteeringManager

	// Request serialization: ensures at most one forwarded request
	// executes at a time, preventing cancel/new-request interleaving.
	requestSerializer *shared.RequestSerializer
}

// Config holds configuration for the Architect agent
type Config struct {
	// Canonical agent ID. If empty, defaults to "architect".
	ID string

	// System prompt configuration
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// LLM planning configuration
	EnableLLM          bool
	AnthropicAuthMode  string
	AnthropicAPIKey    string
	Model              string
	ThinkingBudget     int
	LLMRequestTimeout  time.Duration
	LLMRetryMax        int
	DisablePromptCache bool
	PromptCacheTTL     time.Duration

	// Cross-domain configuration
	CrossDomainTimeout time.Duration // Optional, defaults to 30s
	MaxConcurrent      int           // Optional, defaults to 3

	// Synthesis configuration
	SimilarityThreshold float64 // Optional, defaults to 0.8
	ConflictThreshold   float64 // Optional, defaults to 0.3
	MaxContentLength    int     // Optional, defaults to 10000
	WorkingDirectory    string  // Optional, defaults to current working directory

	// Consultation/delegation policy
	MandatoryConsultation            bool          // Optional, defaults to true
	AllowPlanningWithoutConsultation bool          // Optional, defaults to false
	ConsultationTimeout              time.Duration // Optional, defaults to 20s
	ConsultationMaxAge               time.Duration // Optional, defaults to 5m

	// Plan approval policy
	AutoApprove bool // When true, plans are dispatched to the orchestrator
	// without waiting for user approval. Default: false.

	// File access (optional — if nil, Architect uses direct disk I/O).
	FileAccess     versioning.FileAccess
	WorkspaceViews versioning.WorkspaceViewAccess

	// PlannerProviderWrapper wraps the raw Anthropic provider for rate limiting.
	// The returned value must implement PlannerStreamProvider (e.g. *gateway.GatewayProvider).
	// Called each time the planner is lazily created, preserving auth refresh semantics.
	PlannerProviderWrapper func(*providers.AnthropicProvider) PlannerStreamProvider

	// ActivityPub for publishing agent activity events to the UI panel.
	ActivityPub events.ActivityPublisher

	// Tool dispatch loop
	MaxToolRuns int // Maximum tool-call turns per conversation request. Defaults to DefaultMaxToolRuns (12).

	// RequestGuard is called at handler entry to prevent activation demotion
	// during in-flight processing. Returns a release function. Nil-safe.
	RequestGuard func() func()

	// PlanStore is the process-lifetime plan store singleton. Required.
	PlanStore *PlanStore

	// ControlStore persists Architect control-plane state such as delegated
	// continuations and mirrored plan snapshots. Optional; created lazily when nil.
	ControlStore *ArchitectControlStore

	// Signal bus for broadcasting degradation / time-pressure signals.
	// Optional — if nil, the Architect creates a ChannelBusSignalAdapter
	// from its EventBus during Start.
	SignalBus signal.SignalBusInterface

	// Logging
	Logger *slog.Logger // Optional, defaults to per-agent WAL logger if nil.

	logWAL io.Closer
}

// Default configuration values
const (
	DefaultMaxOutputTokens     = 16384
	DefaultThinkingBudget      = 8192
	DefaultArchitectModel      = "claude-opus-4-6"
	DefaultLLMRequestTimeout   = 120 * time.Second
	DefaultLLMRetryMax         = 3
	DefaultPromptCacheTTL      = 1 * time.Hour
	DefaultCrossDomainTimeout  = 30 * time.Second
	DefaultMaxConcurrent       = 3
	DefaultSimilarityThreshold = 0.8
	DefaultConflictThreshold   = 0.3
	DefaultMaxContentLength    = 10000
	DefaultConsultationTimeout = 20 * time.Second
	DefaultConsultationMaxAge  = 5 * time.Minute
	DefaultSkillMaxLoaded      = 16
	DefaultSkillTokenBudget    = 3200
	DefaultMaxToolRuns         = 32
)

// ---------------------------------------------------------------------------
// Architect debug file logger — writes to ~/.sylk/logs/architect_debug.log
// at Debug level for thorough runtime diagnostics.
// ---------------------------------------------------------------------------

var (
	architectDebugLogger     *slog.Logger
	architectDebugLoggerOnce sync.Once
)

func architectDebugLog() *slog.Logger {
	architectDebugLoggerOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".sylk", "logs")
		_ = os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(filepath.Join(dir, "architect_debug.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			architectDebugLogger = slog.Default()
			return
		}
		architectDebugLogger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	return architectDebugLogger
}

// logInfo logs at Info level, safe to call when a.logger is nil.
func (a *Architect) logInfo(msg string, args ...any) {
	if a != nil && a.logger != nil {
		a.logger.Info(msg, args...)
	}
}

// logWarn logs at Warn level, safe to call when a.logger is nil.
func (a *Architect) logWarn(msg string, args ...any) {
	if a != nil && a.logger != nil {
		a.logger.Warn(msg, args...)
	}
}

// logDebug logs at Debug level to the dedicated architect debug file.
func (a *Architect) logDebug(msg string, args ...any) {
	architectDebugLog().Debug(msg, args...)
}

func (a *Architect) publishActivity(eventType events.EventType, content string) {
	a.publishActivityWithVisibility(eventType, events.VisibilityUser, content)
}

func (a *Architect) publishActivityWithVisibility(eventType events.EventType, visibility events.EventVisibility, content string) {
	if a.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, "default", content)
	evt.AgentID = a.AgentID()
	evt.Visibility = visibility
	evt.Data["agent_type"] = "architect"
	evt.Data["agent_name"] = "Architect"
	a.activityPub.PublishActivity(evt)
}

// operationTimeout derives the planning operation budget from model parameters.
// stages * budgetLevels * perRequestTimeout — no magic numbers.
func (a *Architect) operationTimeout() time.Duration {
	stages := 3 // analyze, design, generate
	budgetLevels := len(requirementsBudgets(a.config.MaxOutputTokens))
	return time.Duration(stages*budgetLevels) * a.config.LLMRequestTimeout
}

// resetPlannerOperationState resets per-operation state on the planner:
// operation budget for deadline-pressure fraction computation, and the
// pressure-emitted debounce flag so each operation gets at most one signal.
func (a *Architect) resetPlannerOperationState(budget time.Duration) {
	a.plannerMu.RLock()
	defer a.plannerMu.RUnlock()
	if ap, ok := a.planner.(*anthropicPlanner); ok && ap != nil {
		ap.resetOperationState(budget)
	}
}

// New creates a new Architect agent. The context is checked between
// initialization steps so construction can be interrupted during shutdown.
func New(ctx context.Context, cfg Config) (*Architect, error) {
	newStart := time.Now()
	cfg = applyConfigDefaults(cfg)
	if err := ensureArchitectLogger(&cfg); err != nil {
		return nil, err
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	ownedPlanStore := false
	if cfg.PlanStore == nil {
		cfg.PlanStore = NewPlanStore(
			cfg.WorkingDirectory,
			NewPlanLeaseManager(cfg.LLMRequestTimeout, ReadyPlanMaxAge),
			cfg.Logger,
		)
		ownedPlanStore = true
	}
	ownedControlStore := false
	if cfg.ControlStore == nil {
		controlPath := filepath.Join(cfg.WorkingDirectory, ".sylk", "state", "architect.db")
		if err := os.MkdirAll(filepath.Dir(controlPath), 0o755); err != nil {
			return nil, fmt.Errorf("create architect state directory: %w", err)
		}
		store, err := OpenArchitectControlStore(defaultArchitectControlStoreConfig(controlPath))
		if err != nil {
			return nil, err
		}
		cfg.ControlStore = store
		ownedControlStore = true
	}

	architectID := cfg.ID
	if architectID == "" {
		architectID = "architect"
	}

	guide.DebugFileLog().Info("DEBUG: architect_new_struct_init")
	architect := &Architect{
		id:                architectID,
		config:            cfg,
		logger:            cfg.Logger,
		logWAL:            cfg.logWAL,
		activityPub:       cfg.ActivityPub,
		fileAccess:        authority.RestrictFileAccess("architect", cfg.FileAccess),
		workspaceViews:    authority.RestrictWorkspaceViews("architect", cfg.WorkspaceViews),
		planStore:         cfg.PlanStore,
		ownsPlanStore:     ownedPlanStore,
		controlStore:      cfg.ControlStore,
		ownsControlStore:  ownedControlStore,
		knownAgents:       make(map[string]*guide.AgentAnnouncement),
		planModes:         make(map[string]*PlanModeState),
		pendingBus:        make(map[string]chan *guide.Message),
		inFlight:          make(map[string]context.CancelFunc),
		steering:          shared.NewSteeringManager(),
		requestSerializer: shared.NewRequestSerializer(),
	}

	architect.steering.InitLazy("architect", cfg.ActivityPub)
	if architect.planStore != nil {
		architect.planStore.SetMirror(func(plan *DesignPlan) error {
			if architect.controlStore == nil {
				return nil
			}
			return architect.controlStore.UpsertPlan(plan)
		})
		for _, plan := range architect.planStore.Snapshot() {
			if plan == nil || architect.controlStore == nil {
				continue
			}
			if err := architect.controlStore.UpsertPlan(plan); err != nil {
				return nil, err
			}
		}
	}
	architect.reconcilePendingContinuations()

	guide.DebugFileLog().Info("DEBUG: architect_new_init_cross_domain")
	architect.initCrossDomain(cfg)
	guide.DebugFileLog().Info("DEBUG: architect_new_init_synthesizer")
	architect.initSynthesizer(cfg)
	guide.DebugFileLog().Info("DEBUG: architect_new_init_skills")
	if err := architect.initSkills(); err != nil {
		return nil, err
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	guide.DebugFileLog().Info("DEBUG: architect_new_init_planner_start")
	if err := architect.initPlanner(ctx, cfg); err != nil {
		return nil, err
	}
	guide.DebugFileLog().Info("DEBUG: architect_new_init_planner_done", "elapsed_ms", time.Since(newStart).Milliseconds())

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	guide.DebugFileLog().Info("DEBUG: architect_new_complete", "total_elapsed_ms", time.Since(newStart).Milliseconds())
	return architect, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	cfg.SystemPrompt = shared.AppendNoFilesystemContext(cfg.SystemPrompt)
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.AnthropicAuthMode == "" {
		if strings.TrimSpace(cfg.AnthropicAPIKey) != "" {
			cfg.AnthropicAuthMode = providers.AnthropicAuthModeAPIKey
		} else {
			cfg.AnthropicAuthMode = providers.ResolveAnthropicAuthMode("")
		}
	}
	if cfg.Model == "" {
		cfg.Model = DefaultArchitectModel
	}
	if cfg.ThinkingBudget == 0 {
		cfg.ThinkingBudget = DefaultThinkingBudget
	}
	if cfg.LLMRequestTimeout == 0 {
		cfg.LLMRequestTimeout = DefaultLLMRequestTimeout
	}
	if cfg.LLMRetryMax == 0 {
		cfg.LLMRetryMax = DefaultLLMRetryMax
	}
	if cfg.PromptCacheTTL == 0 {
		cfg.PromptCacheTTL = DefaultPromptCacheTTL
	}
	if cfg.CrossDomainTimeout == 0 {
		cfg.CrossDomainTimeout = DefaultCrossDomainTimeout
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	if cfg.SimilarityThreshold == 0 {
		cfg.SimilarityThreshold = DefaultSimilarityThreshold
	}
	if cfg.ConflictThreshold == 0 {
		cfg.ConflictThreshold = DefaultConflictThreshold
	}
	if cfg.MaxContentLength == 0 {
		cfg.MaxContentLength = DefaultMaxContentLength
	}
	if cfg.WorkingDirectory == "" {
		if wd, err := os.Getwd(); err == nil && wd != "" {
			cfg.WorkingDirectory = wd
		} else {
			cfg.WorkingDirectory = "."
		}
	}
	if cfg.ConsultationTimeout == 0 {
		cfg.ConsultationTimeout = DefaultConsultationTimeout
	}
	if cfg.ConsultationMaxAge == 0 {
		cfg.ConsultationMaxAge = DefaultConsultationMaxAge
	}
	if cfg.MaxToolRuns == 0 {
		cfg.MaxToolRuns = DefaultMaxToolRuns
	}
	if cfg.AllowPlanningWithoutConsultation {
		cfg.MandatoryConsultation = false
	}
	if !cfg.MandatoryConsultation && !cfg.AllowPlanningWithoutConsultation {
		cfg.MandatoryConsultation = true
	}
	return cfg
}

func ensureArchitectLogger(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("architect config is nil")
	}
	if cfg.Logger != nil {
		return nil
	}
	logger, closer, err := agentlog.NewWALLogger("architect")
	if err != nil {
		return fmt.Errorf("create architect wal logger: %w", err)
	}
	cfg.Logger = logger
	cfg.logWAL = closer
	return nil
}

func (a *Architect) initCrossDomain(cfg Config) {
	a.crossDomainHandler = NewCrossDomainHandler(&CrossDomainHandlerConfig{
		Timeout:       cfg.CrossDomainTimeout,
		MaxConcurrent: cfg.MaxConcurrent,
		Logger:        cfg.Logger,
		QueryHandler:  a.handleDomainQuery,
	})
}

func (a *Architect) initSynthesizer(cfg Config) {
	a.synthesizer = NewResultSynthesizer(&SynthesizerConfig{
		SimilarityThreshold: cfg.SimilarityThreshold,
		ConflictThreshold:   cfg.ConflictThreshold,
		MaxContentLength:    cfg.MaxContentLength,
	})
}

func (a *Architect) initSkills() error {
	a.skills = skills.NewRegistry()
	a.hooks = skills.NewHookRegistry()
	a.registerCoreSkills()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.MaxLoadedSkills = DefaultSkillMaxLoaded
	loaderCfg.TokenBudget = DefaultSkillTokenBudget
	loaderCfg.CoreSkills = architectCoreSkillNames()
	loaderCfg.AutoLoadDomains = nil
	a.skillLoader = skills.NewLoader(a.skills, loaderCfg)
	registerArchitectSafetyHook(a.hooks, a.skills, architectAllSkillNames())
	tools, err := toolruntime.New(toolruntime.Config{
		Registry:             a.skills,
		Hooks:                a.hooks,
		Manifest:             architectToolManifest(),
		State:                toolruntime.NewState(),
		GuardianControlPlane: newArchitectGuardianControlPlane(a),
	})
	if err != nil {
		return fmt.Errorf("initialize architect tool runtime: %w", err)
	}
	a.tools = tools
	a.tools.SyncActiveFromLoaded()
	return nil
}

// Close closes the architect and its resources
func (a *Architect) Close() error {
	stopErr := a.Stop()
	if a.tools != nil {
		a.tools.Close()
		a.tools = nil
	}
	if a.ownsPlanStore && a.planStore != nil {
		a.planStore.SetMirror(nil)
		a.planStore.Close()
		a.planStore = nil
		a.ownsPlanStore = false
	} else if a.planStore != nil {
		a.planStore.SetMirror(nil)
	}
	if a.ownsControlStore && a.controlStore != nil {
		_ = a.controlStore.Close()
		a.controlStore = nil
		a.ownsControlStore = false
	}
	if a.logWAL == nil {
		return stopErr
	}
	closeErr := a.logWAL.Close()
	a.logWAL = nil
	return errors.Join(stopErr, closeErr)
}

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// The architect subscribes to its own channels and the registry topic.
func (a *Architect) Start(bus guide.EventBus) error {
	a.runMu.Lock()
	if a.running {
		a.runMu.Unlock()
		return fmt.Errorf("architect is already running")
	}
	a.running = true
	a.runMu.Unlock()

	a.setRunContext(context.Background())

	a.bus = bus
	a.channels = guide.NewAgentChannels("architect", a.id)

	// Subscribe to own request channel (architect.requests)
	var err error
	a.requestSub, err = bus.SubscribeAsync(a.channels.Requests, a.handleBusRequest)
	if err != nil {
		a.cancelRunContext()
		a.setNotRunning()
		return fmt.Errorf("failed to subscribe to %s: %w", a.channels.Requests, err)
	}

	// Subscribe to own response channel (for replies to requests we make)
	a.responseSub, err = bus.SubscribeAsync(a.channels.Responses, a.handleBusResponse)
	if err != nil {
		a.requestSub.Unsubscribe()
		a.cancelRunContext()
		a.setNotRunning()
		return fmt.Errorf("failed to subscribe to %s: %w", a.channels.Responses, err)
	}

	// Subscribe to agent registry for announcements
	a.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, a.handleRegistryAnnouncement)
	if err != nil {
		a.requestSub.Unsubscribe()
		a.responseSub.Unsubscribe()
		a.cancelRunContext()
		a.setNotRunning()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	// Wire signal bus: prefer injected, otherwise create adapter from EventBus.
	if a.config.SignalBus != nil {
		a.signalBus = a.config.SignalBus
	} else {
		adapter, adapterErr := guide.NewChannelBusSignalAdapter(bus, guide.ChannelBusSignalAdapterConfig{})
		if adapterErr != nil {
			a.logger.Warn("architect signal adapter creation failed", "error", adapterErr)
		} else {
			a.signalAdapter = adapter
			a.signalBus = adapter
		}
	}

	// Subscribe to heartbeat topic for executing plan lease renewal.
	a.heartbeatSub, _ = bus.SubscribeAsync("plan.heartbeat", a.handlePlanHeartbeat)

	a.logger.Info("architect started", "channels", a.channels)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (a *Architect) Stop() error {
	a.runMu.Lock()
	if !a.running {
		a.runMu.Unlock()
		return nil
	}
	a.running = false
	a.runMu.Unlock()

	a.steering.CloseAll()

	// Diagnostic: log stop with in-flight state so we can trace what
	// triggers Stop() while requests are still processing.
	a.inFlightMu.Lock()
	inFlightCount := len(a.inFlight)
	a.inFlightMu.Unlock()
	a.logInfo("architect: Stop() called",
		"in_flight_count", inFlightCount)
	architectDebugLog().Info("architect: STOP_CALLED",
		"in_flight_count", inFlightCount,
		"agent_id", a.id)

	// Close self-created signal adapter (if any).
	if a.signalAdapter != nil {
		a.signalAdapter.Close()
		a.signalAdapter = nil
	}

	a.cancelRunContext()
	errs := a.unsubscribeAll()

	// Unsubscribe heartbeat.
	if a.heartbeatSub != nil {
		if err := a.heartbeatSub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
		a.heartbeatSub = nil
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	a.logger.Info("architect stopped")
	return nil
}

func (a *Architect) unsubscribeAll() []error {
	var errs []error
	if err := a.unsubscribeRequest(); err != nil {
		errs = append(errs, err)
	}
	if err := a.unsubscribeResponse(); err != nil {
		errs = append(errs, err)
	}
	if err := a.unsubscribeRegistry(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (a *Architect) unsubscribeRequest() error {
	if a.requestSub == nil {
		return nil
	}
	err := a.requestSub.Unsubscribe()
	a.requestSub = nil
	return err
}

func (a *Architect) unsubscribeResponse() error {
	if a.responseSub == nil {
		return nil
	}
	err := a.responseSub.Unsubscribe()
	a.responseSub = nil
	return err
}

func (a *Architect) unsubscribeRegistry() error {
	if a.registrySub == nil {
		return nil
	}
	err := a.registrySub.Unsubscribe()
	a.registrySub = nil
	return err
}

// IsRunning returns true if the architect is actively processing bus messages
func (a *Architect) IsRunning() bool {
	return a.running
}

// Bus returns the event bus used by the architect
func (a *Architect) Bus() guide.EventBus {
	return a.bus
}

// Channels returns the architect's channel configuration
func (a *Architect) Channels() *guide.AgentChannels {
	return a.channels
}

// =============================================================================
// Request Handling
// =============================================================================

// handleBusRequest processes incoming forwarded requests from the event bus
func (a *Architect) handleBusRequest(msg *guide.Message) error {
	ctx := a.processingContext()
	if err := ctx.Err(); err != nil {
		a.logInfo("handleBusRequest: context already cancelled", "err", err)
		return nil
	}
	a.logInfo("handleBusRequest: entry",
		"msg_type", string(msg.Type),
		"correlation_id", msg.CorrelationID)
	start := time.Now()
	err := a.dispatchBusRequest(ctx, msg)
	a.logInfo("handleBusRequest: exit",
		"msg_type", string(msg.Type),
		"correlation_id", msg.CorrelationID,
		"elapsed", time.Since(start).String(),
		"err", err)
	return err
}

func (a *Architect) dispatchBusRequest(ctx context.Context, msg *guide.Message) error {
	if msg == nil {
		a.logInfo("dispatchBusRequest: nil message, skipping")
		return nil
	}
	a.logInfo("dispatchBusRequest: routing",
		"msg_type", string(msg.Type),
		"correlation_id", msg.CorrelationID)
	if msg.Type == guide.MessageTypeForward {
		return a.handleForwardBusRequest(ctx, msg)
	}
	if msg.Type == guide.MessageTypeAction {
		return a.handleActionBusRequest(ctx, msg)
	}
	if msg.Type == guide.MessageTypeProposal {
		return a.handleProposalBusRequest(ctx, msg)
	}
	a.logInfo("dispatchBusRequest: unhandled message type", "msg_type", string(msg.Type))
	return nil
}

func (a *Architect) handleForwardBusRequest(ctx context.Context, msg *guide.Message) error {
	if !a.requestSerializer.Acquire(ctx) {
		return nil // parent context done, agent shutting down
	}
	defer a.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		a.logWarn("handleForwardBusRequest: invalid forward request payload")
		return fmt.Errorf("invalid forward request payload")
	}

	a.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	shared.LogIncomingRequest(a.steering.EventLogger(), fwd, a.id)

	if a.config.RequestGuard != nil {
		release := a.config.RequestGuard()
		defer release()
	}

	a.logInfo("handleForwardBusRequest: entry",
		"correlation_id", fwd.CorrelationID,
		"intent", string(fwd.Intent),
		"input", truncateString(fwd.Input, 120),
		"session_id", fwd.SessionID,
		"fire_and_forget", fwd.FireAndForget,
		"history_len", len(fwd.ConversationHistory),
		"ctx_deadline", contextDeadlineString(ctx))

	startTime := time.Now()
	reqCtx, cancel := context.WithCancel(ctx)
	reqCtx = versioning.WithSessionID(reqCtx, fwd.SessionID)
	reqCtx = shared.WithGuardianCommandGate(reqCtx, shared.GuardianCommandGateConfig{
		BusProvider:     func() guide.EventBus { return a.bus },
		SourceAgentID:   func() string { return a.id },
		SourceAgentType: "architect",
		SourceAgentName: "Architect",
	})
	reqCtx = withRouteHops(reqCtx, fwd.Hops)
	reqCtx = withArchitectStreamContext(reqCtx, fwd.CorrelationID, fwd.SourceAgentID)
	reqCtx, usageAcc := withArchitectUsageAccumulator(reqCtx)
	reqCtx = withArchitectEarlyUsageEmitter(reqCtx, func(inputTokens int) {
		a.publishPlanStreamEarlyUsage(reqCtx, inputTokens)
	})
	reqCtx = withStreamRetryResetEmitter(reqCtx, func() {
		a.publishPlanStreamStart(reqCtx)
	})

	// Create steering ledger for this request.
	ledger := a.steering.Create(fwd.CorrelationID, a.id, fwd.SessionID, a.activityPub, nil)
	defer a.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)
	reqCtx = shared.WithSteeringLedger(reqCtx, ledger)
	reqCtx = shared.WithLogMeta(reqCtx, shared.LogMeta{
		EventLogger: a.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     a.id,
		SessionID:   fwd.SessionID,
	})
	gov := shared.NewContextGovernor(a.config.Model, a.config.MaxOutputTokens, 0)
	if a.handoffBridge != nil {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			return a.handoffBridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	reqCtx = shared.WithContextGovernor(reqCtx, gov)
	reqCtx = shared.WithProgressPublisher(reqCtx, &shared.ProgressPublisher{
		Bus: a.bus, Channels: a.channels,
		AgentID: a.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})
	reqCtx = shared.WithToolCallEmitter(reqCtx, shared.NewToolCallEmitter(
		a.bus, a.channels, a.id, fwd.CorrelationID, fwd.SourceAgentID,
	))

	a.registerInFlight(fwd.CorrelationID, cancel)
	a.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer a.clearInFlight(fwd.CorrelationID)
	defer cancel()
	if !fwd.FireAndForget {
		a.publishPlanStreamStart(reqCtx)
	}
	a.logInfo("handleForwardBusRequest: calling processForwardedRequest",
		"correlation_id", fwd.CorrelationID,
		"elapsed_before_process", time.Since(startTime).String())
	result, err := a.processForwardedRequest(reqCtx, fwd)
	shared.LogResponse(a.steering.EventLogger(), fwd.CorrelationID, a.id, fwd.SessionID, time.Since(startTime), err)
	a.logInfo("handleForwardBusRequest: processForwardedRequest returned",
		"correlation_id", fwd.CorrelationID,
		"elapsed", time.Since(startTime).String(),
		"has_result", result != nil,
		"err", err)

	// Don't respond if fire-and-forget
	if fwd.FireAndForget {
		a.logInfo("handleForwardBusRequest: fire-and-forget, not responding",
			"correlation_id", fwd.CorrelationID)
		return nil
	}

	// Build response
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   a.id,
		RespondingAgentName: "architect",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		if a.isInterruptError(err) {
			// Publish a stream complete FIRST so the TUI receives
			// StreamCompleteMsg and clears the activeRoute before the
			// GuideResponseMsg arrives. Without this, the stale
			// activeRoute causes subsequent messages to be enqueued
			// without a thinking indicator. Using StreamComplete
			// (rather than StreamError) means:
			//  1. For user Ctrl+C: shouldRenderStreamEvent returns
			//     false (CID in interruptedCorrelations) so no
			//     duplicate display.
			//  2. For system interrupts: the thinking entry resolves
			//     cleanly with the interrupted text.
			//  3. The subsequent GuideResponseMsg error is suppressed
			//     by shouldSuppressErrorAfterSuccess.
			a.publishPlanStreamComplete(reqCtx, "(interrupted)", usageAcc.Total(), nil)
			// Then publish a minimal response so the Guide records
			// something in conversation history rather than leaving
			// an empty agent reply that causes context loss.
			a.publishInterruptResponse(fwd)
			return nil
		}
		a.publishPlanStreamError(reqCtx, err)
		resp.Error = err.Error()
		// Publish to error channel
		errMsg := guide.NewErrorMessage(
			a.generateMessageID(),
			fwd.CorrelationID,
			"architect",
			err.Error(),
		)
		return a.bus.Publish(a.channels.Errors, errMsg)
	}

	resp.Data = result

	// Handoff reroute events are emitted INSIDE dispatchPlanExecution (and
	// stepAutoHandoff when AutoApprove is enabled) — before the synchronous
	// requestRouteSync call — so the TUI switches to the orchestrator while
	// it is actively processing. No reroute emission here; it already
	// happened at the right time.

	// Always include the authoritative response text in the completion
	// event. The bridge stores it as AuthoritativeText on StreamCompleteMsg
	// so the chat model can correct dropped or reordered stream chunks.
	directive := extractResponseDirective(result)
	completeText := extractUserResponse(result)
	architectDebugLog().Info("handleForwardBusRequest: DIRECTIVE_EXTRACT",
		"correlation_id", fwd.CorrelationID,
		"has_directive", directive != nil,
		"directive_phase", directivePhaseStr(directive),
		"directive_agent", directiveAgentStr(directive),
		"complete_text_len", len(completeText))
	a.publishPlanStreamComplete(reqCtx, completeText, usageAcc.Total(), directive)

	if a.agentPod != nil {
		a.agentPod.FeedScribe("architect", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}

	// Publish response to own response channel
	respMsg := guide.NewResponseMessage(a.generateMessageID(), resp)
	return a.bus.Publish(a.channels.Responses, respMsg)
}

// publishInterruptResponse sends a minimal response to the Guide when a
// request is interrupted (context canceled). Without this, the Guide's
// conversation history records the user input but no agent reply, causing
// context loss on subsequent turns.
//
// Success is set to true so the TUI's shouldSuppressStreamedRouteResponse
// suppresses this GuideResponseMsg (the stream-complete event already
// delivered the "(interrupted)" text and cleared the activeRoute).
func (a *Architect) publishInterruptResponse(fwd *guide.ForwardedRequest) {
	if a == nil || a.bus == nil || a.channels == nil || fwd == nil {
		return
	}
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             true,
		RespondingAgentID:   a.id,
		RespondingAgentName: "architect",
		Data:                &ConversationResult{Response: "(interrupted)", Intent: IntentConverse},
	}
	msg := guide.NewResponseMessage(a.generateMessageID(), resp)
	_ = a.bus.Publish(a.channels.Responses, msg)
}

func (a *Architect) handleActionBusRequest(ctx context.Context, msg *guide.Message) error {
	req, ok := msg.GetActionRequest()
	if !ok {
		return fmt.Errorf("invalid action request payload")
	}
	if req == nil {
		return nil
	}
	// Cancel must be checked BEFORE steering.HandleAction so the Architect's
	// custom handleCancelAction runs (publishes interrupt response + supersedes plans).
	if isCancelAction(req.Action) {
		return a.handleCancelAction(req)
	}
	if a.steering.HandleAction(req) {
		return nil
	}
	if strings.EqualFold(req.Action, "proposal") {
		return a.handleProposalAction(ctx, req)
	}
	if strings.EqualFold(req.Action, "read_research_paper") {
		return a.handleReadResearchAction(ctx, req)
	}
	return nil
}

func (a *Architect) handleProposalBusRequest(ctx context.Context, msg *guide.Message) error {
	req := &guide.ActionRequest{
		CorrelationID: msg.CorrelationID,
		SourceAgentID: msg.SourceAgentID,
		Action:        "proposal",
		Data:          msg.Payload,
	}
	return a.handleProposalAction(ctx, req)
}

func (a *Architect) generateMessageID() string {
	return fmt.Sprintf("architect_msg_%s", uuid.New().String())
}

func (a *Architect) registerInFlight(correlationID string, cancel context.CancelFunc) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" || cancel == nil {
		return
	}
	a.inFlightMu.Lock()
	a.inFlight[correlationID] = cancel
	a.inFlightMu.Unlock()
}

func (a *Architect) clearInFlight(correlationID string) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	a.inFlightMu.Lock()
	delete(a.inFlight, correlationID)
	a.inFlightMu.Unlock()
}

func (a *Architect) cancelInFlight(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	var cancel context.CancelFunc
	a.inFlightMu.Lock()
	cancel = a.inFlight[correlationID]
	delete(a.inFlight, correlationID)
	a.inFlightMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func isCancelAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "cancel", "interrupt", "stop":
		return true
	default:
		return false
	}
}

// processForwardedRequest handles the actual request processing
func (a *Architect) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	a.logInfo("processForwardedRequest: resolving intent handler",
		"intent", string(fwd.Intent),
		"ctx_deadline", contextDeadlineString(ctx))
	if controlResult, handled, controlErr := a.handleControlPlaneForward(ctx, fwd); handled {
		return controlResult, controlErr
	}
	architectDebugLog().Info("handoff: PROCESS_FWD_REQUEST",
		"intent", string(fwd.Intent),
		"user_input", truncateString(fwd.Input, 300),
		"session_id", fwd.SessionID,
		"correlation_id", fwd.CorrelationID,
		"has_metadata", fwd.Metadata != nil,
		"current_model", a.config.Model,
		"active_plans", a.activePlanCount())
	handler, err := a.intentHandler(fwd.Intent)
	if err != nil {
		a.logWarn("processForwardedRequest: no handler for intent",
			"intent", string(fwd.Intent), "err", err)
		return nil, err
	}
	a.logInfo("processForwardedRequest: dispatching to handler",
		"intent", string(fwd.Intent))
	start := time.Now()
	result, handlerErr := handler(ctx, fwd)
	a.logInfo("processForwardedRequest: handler returned",
		"intent", string(fwd.Intent),
		"elapsed", time.Since(start).String(),
		"has_result", result != nil,
		"err", handlerErr)
	architectDebugLog().Info("handoff: PROCESS_FWD_RESULT",
		"intent", string(fwd.Intent),
		"elapsed", time.Since(start).String(),
		"has_result", result != nil,
		"err", handlerErr,
		"result_type", fmt.Sprintf("%T", result))
	return result, handlerErr
}

func (a *Architect) setNotRunning() {
	a.runMu.Lock()
	a.running = false
	a.runMu.Unlock()
}

func (a *Architect) setRunContext(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	runCtx, cancel := context.WithCancel(parent)

	a.runMu.Lock()
	a.runCtx = runCtx
	a.runCancel = cancel
	a.runMu.Unlock()
}

func (a *Architect) processingContext() context.Context {
	a.runMu.RLock()
	ctx := a.runCtx
	a.runMu.RUnlock()
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func (a *Architect) cancelRunContext() {
	a.runMu.Lock()
	cancel := a.runCancel
	a.runCancel = nil
	a.runMu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (a *Architect) isInterruptError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (a *Architect) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentExecute:
		return a.handleExecute, nil
	case guide.IntentPlan, guide.IntentDesign:
		return a.handleConversation, nil
	case guide.IntentRecall:
		return a.handleRecall, nil
	case guide.IntentCheck:
		return a.handleCheck, nil
	case guide.IntentHelp, guide.IntentChat, guide.IntentUnknown:
		return a.handleConversation, nil
	default:
		return a.handleConversation, nil
	}
}

// handleRecall processes recall requests
func (a *Architect) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:                  uuid.New().String(),
		Intent:              IntentRecall,
		Query:               fwd.Input,
		SessionID:           sessionIDFromForwarded(fwd),
		Timestamp:           time.Now(),
		Params:              forwardedRequestParams(fwd),
		ConversationHistory: fwd.ConversationHistory,
	}

	return a.Handle(ctx, req)
}

// handleCheck processes check/verification requests
func (a *Architect) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:                  uuid.New().String(),
		Intent:              IntentCheck,
		Query:               fwd.Input,
		SessionID:           sessionIDFromForwarded(fwd),
		Timestamp:           time.Now(),
		Params:              forwardedRequestParams(fwd),
		ConversationHistory: fwd.ConversationHistory,
	}

	return a.Handle(ctx, req)
}

// handleConversation processes conversational requests from the event bus.
// Both IntentPlan and IntentDesign route here so the architect converses
// naturally before formalizing a plan.
func (a *Architect) handleConversation(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	a.logInfo("handleConversation: entry",
		"input", truncateString(fwd.Input, 120),
		"intent", string(fwd.Intent),
		"session_id", sessionIDFromForwarded(fwd),
		"history_len", len(fwd.ConversationHistory),
		"ctx_deadline", contextDeadlineString(ctx),
		"active_plans_count", a.activePlanCount())

	// Check clarifying plan FIRST. If the user is in a clarification flow,
	// route their response back into the protocol — never to handleExecute
	// or handlePlanFeedback. This prevents the bug where an affirmative
	// clarification answer dispatches a stale Ready plan.
	if plan := a.latestClarifyingPlan(sessionIDFromForwarded(fwd)); plan != nil {
		a.logInfo("handleConversation: ROUTE=clarification_response",
			"plan_id", plan.ID,
			"plan_query", truncateString(plan.Query, 80),
			"plan_status", plan.SM().State().String())
		return a.handleClarificationResponse(ctx, fwd, plan)
	}

	// When a ready plan exists, evaluate the user's response using the
	// same Guardian-gated acceptance flow the tool loop uses. This keeps
	// deterministic routing and LLM-driven routing on the same continuation
	// path instead of maintaining separate sync-only logic.
	if plan := a.latestReadyPlan(sessionIDFromForwarded(fwd)); plan != nil {
		architectDebugLog().Info("handoff: READY_PLAN_FOUND",
			"plan_id", plan.ID,
			"plan_state", plan.SM().State().String(),
			"plan_epoch", plan.SM().Epoch(),
			"plan_age", time.Since(plan.UpdatedAt).String(),
			"user_input", fwd.Input,
			"is_approval_signal", isApprovalSignal(fwd.Input),
			"current_model", a.config.Model)
		if plan.PendingWork != nil {
			message := strings.TrimSpace(plan.PendingWork.Message)
			if message == "" {
				message = "I already have a pending operation on this plan and will update you shortly."
			}
			return &ConversationResult{
				Response: message,
				Intent:   IntentConverse,
			}, nil
		}
		result, err := a.invokeConversationTool(ctx, "route_plan_acceptance", routePlanAcceptanceParams{
			PlanID:       plan.ID,
			UserResponse: fwd.Input,
		}, IntentConverse)
		if err != nil {
			a.logWarn("handleConversation: route_plan_acceptance invocation failed",
				"plan_id", plan.ID,
				"error", err)
			return &ConversationResult{
				Response: "I couldn't start the plan approval flow right now: " + err.Error(),
				Intent:   IntentConverse,
			}, nil
		}
		return result, nil
	}

	req := &ArchitectRequest{
		ID:                  uuid.New().String(),
		Intent:              mapGuideIntentToArchitect(fwd.Intent),
		Query:               fwd.Input,
		SessionID:           sessionIDFromForwarded(fwd),
		Timestamp:           time.Now(),
		Params:              forwardedRequestParams(fwd),
		ConversationHistory: fwd.ConversationHistory,
	}

	a.logInfo("handleConversation: ROUTE=conversation (no plan triggers matched)",
		"mapped_intent", string(req.Intent))
	return a.Handle(ctx, req)
}

// latestClarifyingPlan returns the most recently updated plan in Clarifying
// state for the given session. Returns nil if no matching plan exists.
func (a *Architect) latestClarifyingPlan(sessionID string) *DesignPlan {
	return a.planStore.LatestClarifying(sessionID)
}

// handleClarificationResponse processes the user's answer to a clarification
// question. Transitions the plan from Clarifying→Pending and re-runs the
// planning protocol with skip_clarification so the same questions are not
// re-asked.
func (a *Architect) handleClarificationResponse(ctx context.Context, fwd *guide.ForwardedRequest, plan *DesignPlan) (any, error) {
	a.logInfo("handleClarificationResponse: entry",
		"plan_id", plan.ID,
		"plan_query", truncateString(plan.Query, 80),
		"user_input", truncateString(fwd.Input, 120),
		"ctx_deadline", contextDeadlineString(ctx),
		"plan_state", plan.SM().State().String())

	if err := plan.SM().TransitionTo(PlanStatusPending, plan); err != nil {
		a.logWarn("handleClarificationResponse: transition failed", "plan_id", plan.ID, "error", err)
		return &ConversationResult{
			Response: "I couldn't process your clarification response. Could you rephrase?",
			Intent:   IntentConverse,
		}, nil
	}
	plan.Status = plan.SM().State()
	plan.UpdatedAt = time.Now()
	_ = a.persistPlanState(plan)

	req := &ArchitectRequest{
		ID:        uuid.New().String(),
		Intent:    IntentPlan,
		Query:     fwd.Input,
		SessionID: sessionIDFromForwarded(fwd),
		Timestamp: time.Now(),
		Params: map[string]any{
			"skip_clarification": true,
			"prior_plan_id":      plan.ID,
		},
		ConversationHistory: fwd.ConversationHistory,
	}

	return a.Handle(ctx, req)
}

// handlePlanFeedback processes user feedback on a ready plan. The phase gate
// classified the input as negative polarity (feedback/rejection), so we route
// to the LLM to address their concerns directly.
//
// Includes a programmatic approval fallback: if the LLM tool loop completes
// without dispatching (plan still Ready) but the user's input signals approval,
// we dispatch directly. This covers providers (e.g. OpenAI) whose models
// don't reliably invoke function-calling tools.
func (a *Architect) handlePlanFeedback(ctx context.Context, fwd *guide.ForwardedRequest, plan *DesignPlan) (any, error) {
	a.logInfo("handlePlanFeedback: entry",
		"input", truncateString(fwd.Input, 80),
		"plan_id", plan.ID)

	architectDebugLog().Info("handoff: FEEDBACK_ENTRY",
		"plan_id", plan.ID,
		"plan_state", plan.SM().State().String(),
		"user_input", fwd.Input,
		"is_approval_signal", isApprovalSignal(fwd.Input),
		"approval_required", !a.config.AutoApprove,
		"current_model", a.config.Model,
		"session_id", sessionIDFromForwarded(fwd),
		"history_len", len(fwd.ConversationHistory),
		"plan_summary_len", len(formatPlanForChat(plan)))

	ctx = withArchitectSessionID(ctx, sessionIDFromForwarded(fwd))
	ctx = withPlannerThoughtCallback(ctx, func(stage string, thought string) {
		a.publishPlanThought(ctx, stage, thought)
	})

	request := plannerConversationRequest{
		Mode:                plannerConversationModeFeedback,
		UserQuery:           fwd.Input,
		PlanID:              plan.ID,
		SessionID:           sessionIDFromForwarded(fwd),
		ApprovalRequired:    !a.config.AutoApprove,
		PlanSummary:         formatPlanForChat(plan),
		ConversationHistory: fwd.ConversationHistory,
		OnChunk: func(text string) {
			a.publishPlanStreamChunk(ctx, text)
		},
	}

	response, err := a.composeUserFacingResponse(ctx, request)
	if err != nil {
		a.logWarn("handlePlanFeedback: compose failed", "error", err)
		architectDebugLog().Warn("handoff: COMPOSE_FAILED",
			"plan_id", plan.ID,
			"error", err.Error(),
			"plan_state_after", plan.SM().State().String(),
			"is_approval_signal", isApprovalSignal(fwd.Input))
		// Compose failed — if user was approving, dispatch directly.
		if plan.SM().State() == PlanStatusReady && isApprovalSignal(fwd.Input) {
			a.logInfo("handlePlanFeedback: compose failed but approval detected, dispatching",
				"plan_id", plan.ID)
			architectDebugLog().Info("handoff: FALLBACK_DISPATCH_ON_ERROR",
				"plan_id", plan.ID)
			return a.handleExecute(ctx, fwd)
		}
		return &ConversationResult{
			Response:  "I couldn't process your feedback right now. Could you rephrase?",
			Intent:    IntentConverse,
			Directive: a.feedbackReadyDirective(plan),
		}, nil
	}

	planStateAfter := plan.SM().State().String()
	approvalDetected := isApprovalSignal(fwd.Input)
	architectDebugLog().Info("handoff: COMPOSE_SUCCESS",
		"plan_id", plan.ID,
		"plan_state_after", planStateAfter,
		"is_approval_signal", approvalDetected,
		"response_len", len(response),
		"response_preview", truncateString(response, 300))

	// Programmatic fallback: the LLM composed a response but didn't invoke
	// route_plan_acceptance (plan is still Ready). If user input signals
	// approval, bypass the LLM and dispatch directly. This handles providers
	// whose models don't reliably call function tools.
	if plan.SM().State() == PlanStatusReady && isApprovalSignal(fwd.Input) {
		a.logInfo("handlePlanFeedback: plan still Ready after compose, approval detected — dispatching",
			"plan_id", plan.ID,
			"response_preview", truncateString(response, 120))
		architectDebugLog().Info("handoff: FALLBACK_DISPATCH_POST_COMPOSE",
			"plan_id", plan.ID,
			"llm_response_discarded", truncateString(response, 200))
		return a.handleExecute(ctx, fwd)
	}

	architectDebugLog().Info("handoff: RETURNING_CONVERSATION_RESULT",
		"plan_id", plan.ID,
		"plan_state", planStateAfter,
		"has_directive", a.feedbackReadyDirective(plan) != nil)

	return &ConversationResult{
		Response:  response,
		Intent:    IntentConverse,
		Directive: a.feedbackReadyDirective(plan),
	}, nil
}

// feedbackReadyDirective returns a ResponseDirective that re-arms the Guide's
// plan-approval phase gate after the architect addresses user feedback. Returns
// nil if the plan is not in Ready status (e.g. already executing or expired).
// isApprovalSignal checks whether the user's input is an unambiguous plan
// approval. Returns false for inputs that contain modification language
// alongside approval words (e.g. "yes but change X"). Used as a programmatic
// fallback when the LLM doesn't invoke route_plan_acceptance.
func isApprovalSignal(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	// Reject inputs that contain modification qualifiers — these are
	// feedback, not pure approval.
	for _, qualifier := range approvalDisqualifiers {
		if strings.Contains(lower, qualifier) {
			architectDebugLog().Debug("handoff: APPROVAL_SIGNAL_DISQUALIFIED",
				"input_preview", truncateString(lower, 200),
				"disqualifier", qualifier)
			return false
		}
	}
	for _, phrase := range approvalPhrases {
		if strings.Contains(lower, phrase) {
			architectDebugLog().Debug("handoff: APPROVAL_SIGNAL_MATCHED",
				"input_preview", truncateString(lower, 200),
				"matched_phrase", phrase)
			return true
		}
	}
	architectDebugLog().Debug("handoff: APPROVAL_SIGNAL_NO_MATCH",
		"input_preview", truncateString(lower, 200))
	return false
}

// approvalPhrases are unambiguous approval signals. Ordered by specificity
// (multi-word first) so the contains check is greedy.
var approvalPhrases = []string{
	"looks good", "go ahead", "ship it", "do it", "kick it off",
	"run it", "start it", "go for it", "let's go", "fire it off",
	"lgtm", "approved", "proceed", "execute", "confirm",
	"yes", "yep", "yeah", "yup",
}

// approvalDisqualifiers are words that turn an approval into feedback
// (e.g. "yes but change X", "go ahead, except swap step 2").
var approvalDisqualifiers = []string{
	"but ", "except", "change", "modify", "update", "instead",
	"swap", "move", "add ", "remove", "however", "although",
	"before that", "first ", "wait", "hold on", "actually",
	"what about", "what if", "how about", "can you", "could you",
	"question", "why ", "?",
}

func (a *Architect) feedbackReadyDirective(plan *DesignPlan) *guide.ResponseDirective {
	if plan == nil {
		return nil
	}
	return plan.ReadyDirective()
}

// handleExecute processes explicit execution intents classified by the Guide.
// The classifier already determined the user wants to execute a plan, so no
// phrase matching is needed — just look for a ready plan and dispatch it.
// Validates epoch from phase-gate metadata to reject stale approvals.
func (a *Architect) handleExecute(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	a.logInfo("handleExecute: entry",
		"input", truncateString(fwd.Input, 80))
	sessionID := sessionIDFromForwarded(fwd)
	architectDebugLog().Info("handoff: EXECUTE_ENTRY",
		"user_input", truncateString(fwd.Input, 200),
		"session_id", sessionID,
		"has_metadata", fwd.Metadata != nil)

	now := time.Now().UTC()
	plan := a.latestReadyPlan(sessionID)
	if plan != nil && planHasActivePendingWork(plan, now) {
		return &ConversationResult{
			Response: pendingPlanUserMessage(plan),
			Intent:   IntentExecute,
		}, nil
	}
	if pending := a.latestActivePendingPlan(sessionID); pending != nil {
		if plan == nil || pending.ID == plan.ID || !plan.UpdatedAt.After(pending.UpdatedAt) {
			return &ConversationResult{
				Response: pendingPlanUserMessage(pending),
				Intent:   IntentExecute,
			}, nil
		}
	}
	if plan == nil {
		if pending := a.planStore.LatestByStatus(sessionID, PlanStatusOrchestrating, ReadyPlanMaxAge); pending != nil {
			return &ConversationResult{
				Response: "That plan is already being handed off to the orchestrator. I'll update you when it confirms ingestion.",
				Intent:   IntentExecute,
			}, nil
		}
		a.logInfo("handleExecute: no ready plan found")
		architectDebugLog().Warn("handoff: EXECUTE_NO_READY_PLAN",
			"session_id", sessionID,
			"active_plans", a.activePlanCount())
		return &ConversationResult{
			Response: "There's no ready plan to execute. Describe what you'd like to build and I'll create one.",
			Intent:   IntentConverse,
		}, nil
	}

	// Epoch validation: reject stale approvals where the plan was re-planned
	// or updated since the user last saw it.
	if requestEpoch, ok := epochFromMetadata(fwd.Metadata); ok {
		if plan.SM().Epoch() != requestEpoch {
			a.logWarn("handleExecute: stale epoch",
				"plan_id", plan.ID,
				"plan_epoch", plan.SM().Epoch(),
				"request_epoch", requestEpoch)
			architectDebugLog().Warn("handoff: EXECUTE_STALE_EPOCH",
				"plan_id", plan.ID,
				"plan_epoch", plan.SM().Epoch(),
				"request_epoch", requestEpoch)
			return &ConversationResult{
				Response:  "The plan has been updated since you last reviewed it. Please review the updated plan and approve again.",
				Intent:    IntentConverse,
				Directive: plan.ReadyDirective(),
			}, nil
		}
	}

	a.logInfo("handleExecute: dispatching plan",
		"plan_id", plan.ID,
		"tasks", len(plan.Tasks))
	architectDebugLog().Info("handoff: EXECUTE_DISPATCHING",
		"plan_id", plan.ID,
		"task_count", len(plan.Tasks),
		"plan_state", plan.SM().State().String())

	req := &ArchitectRequest{
		Query:     fwd.Input,
		SessionID: sessionIDFromForwarded(fwd),
	}
	result, err := a.dispatchPlanExecution(ctx, req, plan)
	architectDebugLog().Info("handoff: EXECUTE_DISPATCH_RESULT",
		"plan_id", plan.ID,
		"has_result", result != nil,
		"err", err)
	return result, nil
}

// epochFromMetadata extracts the epoch from phase-gate metadata.
func epochFromMetadata(metadata map[string]any) (uint64, bool) {
	if metadata == nil {
		return 0, false
	}
	v, ok := metadata["epoch"]
	if !ok {
		return 0, false
	}
	switch e := v.(type) {
	case uint64:
		return e, true
	case float64:
		return uint64(e), true
	case int:
		return uint64(e), true
	default:
		return 0, false
	}
}

// mapGuideIntentToArchitect preserves the original guide intent as an
// ArchitectIntent hint so the LLM receives the right context (e.g. "design"
// vs "plan" vs general conversation).
func mapGuideIntentToArchitect(intent guide.Intent) ArchitectIntent {
	switch intent {
	case guide.IntentDesign:
		return IntentDesign
	case guide.IntentPlan:
		return IntentPlan
	default:
		return IntentConverse
	}
}

func forwardedRequestParams(fwd *guide.ForwardedRequest) map[string]any {
	if fwd == nil {
		return nil
	}
	params := map[string]any{}
	if fwd.Entities != nil {
		if fwd.Entities.Scope != "" {
			params["scope"] = fwd.Entities.Scope
		}
	}
	if fwd.CrossDomain != nil {
		params["cross_domain"] = fwd.CrossDomain
		params["is_multi_agent"] = fwd.CrossDomain.IsMultiAgent
		params["primary_agent"] = fwd.CrossDomain.PrimaryAgent
		params["subtask_count"] = len(fwd.CrossDomain.SubTasks)
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

// handleBusResponse processes responses to requests we made
func (a *Architect) handleBusResponse(msg *guide.Message) error {
	a.deliverPendingBusMessage(msg)
	if a.hasPendingBusWait(msg.CorrelationID) {
		a.logger.Debug("received response for synchronous waiter", "correlation_id", msg.CorrelationID, "type", msg.Type)
		return nil
	}
	if err := a.handleContinuationResponse(msg); err != nil {
		a.logWarn("handleBusResponse: continuation processing failed",
			"correlation_id", msg.CorrelationID,
			"error", err)
		return err
	}
	a.logger.Debug("received response", "correlation_id", msg.CorrelationID, "type", msg.Type)
	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events
func (a *Architect) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		a.knownAgentsMu.Lock()
		a.knownAgents[ann.AgentID] = ann
		a.knownAgentsMu.Unlock()
		a.logger.Debug("agent registered", "agent_id", ann.AgentID)
	case guide.MessageTypeAgentUnregistered:
		a.knownAgentsMu.Lock()
		delete(a.knownAgents, ann.AgentID)
		a.knownAgentsMu.Unlock()
		a.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
	}

	return nil
}

// GetKnownAgents returns all agents the architect knows about
func (a *Architect) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	a.knownAgentsMu.RLock()
	defer a.knownAgentsMu.RUnlock()

	result := make(map[string]*guide.AgentAnnouncement, len(a.knownAgents))
	for k, v := range a.knownAgents {
		result[k] = v
	}
	return result
}

// =============================================================================
// Direct API Methods
// =============================================================================

// Handle processes an ArchitectRequest directly (without event bus)
func (a *Architect) Handle(ctx context.Context, req *ArchitectRequest) (*ArchitectResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}
	ctx = withArchitectSessionID(ctx, req.SessionID)
	a.logInfo("Handle: entry",
		"req_id", req.ID,
		"intent", string(req.Intent),
		"query", truncateString(req.Query, 120),
		"session_id", req.SessionID,
		"ctx_deadline", contextDeadlineString(ctx))
	a.prepareSkillsForRequest(req)

	start := time.Now()
	var result any
	var err error

	switch req.Intent {
	case IntentPlan, IntentDesign:
		a.logInfo("Handle: ROUTE=executeConversation (plan/design)", "intent", string(req.Intent))
		result, err = a.executeConversation(ctx, req)
	case IntentGenerateTasks:
		a.logInfo("Handle: ROUTE=executeGenerateTasks")
		result, err = a.executeGenerateTasks(ctx, req)
	case IntentCreateDAG:
		a.logInfo("Handle: ROUTE=executeCreateDAG")
		result, err = a.executeCreateDAG(ctx, req)
	case IntentRecall:
		a.logInfo("Handle: ROUTE=executeRecall")
		result, err = a.executeRecall(ctx, req)
	case IntentCheck:
		a.logInfo("Handle: ROUTE=executeCheck")
		result, err = a.executeCheck(ctx, req)
	case IntentHelp, IntentConsult, IntentEstimate, IntentConverse:
		a.logInfo("Handle: ROUTE=executeConversation (conversational)", "intent", string(req.Intent))
		result, err = a.executeConversation(ctx, req)
	default:
		a.logInfo("Handle: ROUTE=executeConversation (default)", "intent", string(req.Intent))
		result, err = a.executeConversation(ctx, req)
	}
	a.logInfo("Handle: handler returned",
		"intent", string(req.Intent),
		"elapsed", time.Since(start).String(),
		"has_result", result != nil,
		"err", err)

	if err != nil {
		resp := &ArchitectResponse{
			ID:        uuid.New().String(),
			RequestID: req.ID,
			Success:   false,
			Error:     err.Error(),
			Took:      time.Since(start),
			Timestamp: time.Now(),
		}
		return resp, err
	}

	return &ArchitectResponse{
		ID:           uuid.New().String(),
		RequestID:    req.ID,
		Success:      true,
		Data:         result,
		UserResponse: extractUserResponse(result),
		Took:         time.Since(start),
		Timestamp:    time.Now(),
	}, nil
}

// =============================================================================
// Pre-Delegation Planning Protocol
// =============================================================================

// executePlanningProtocol implements the Pre-Delegation Planning Protocol
// Steps:
// 1. Understand requirements
// 2. Consult Librarian for codebase patterns
// 3. Design solution architecture
// 4. Generate atomic tasks
// 5. Create workflow DAG
func (a *Architect) executePlanningProtocol(ctx context.Context, req *ArchitectRequest) (*DesignPlan, error) {
	return a.runPlanningProtocol(ctx, req)
}

func failPlan(plan *DesignPlan, err error) (*DesignPlan, error) {
	if smErr := plan.SM().TransitionToFailed(plan, err); smErr != nil {
		// Already in a terminal state — just record the error.
		plan.Error = err.Error()
		return plan, err
	}
	plan.Status = plan.SM().State()
	plan.Error = err.Error()
	return plan, err
}

func extractConstraints(params map[string]any) *PlanConstraints {
	constraints := &PlanConstraints{
		MaxTasksPerAgent: 5, // Default
	}

	if params == nil {
		return constraints
	}

	if maxTasks, ok := params["max_tasks_per_agent"].(int); ok {
		constraints.MaxTasksPerAgent = maxTasks
	}
	if scope, ok := params["scope"].(string); ok {
		constraints.Scope = scope
	}
	if parallel, ok := params["allow_parallel"].(bool); ok {
		constraints.AllowParallel = parallel
	} else {
		constraints.AllowParallel = true // Default to allowing parallel
	}

	return constraints
}

// analyzeRequirements extracts and structures requirements from the query
func (a *Architect) analyzeRequirements(ctx context.Context, query string, params map[string]any) (*Requirements, error) {
	if requirements, ok := a.tryAnalyzeRequirementsWithLLM(ctx, query, params); ok {
		a.logInfo("analyzeRequirements: LLM path used", "goals", len(requirements.Goals))
		return requirements, nil
	}
	a.logInfo("analyzeRequirements: deterministic fallback")

	requirements := &Requirements{
		Query:        query,
		Goals:        []string{},
		Constraints:  []string{},
		Dependencies: []string{},
		Scope:        "project",
	}

	// Extract scope if provided
	if params != nil {
		if scope, ok := params["scope"].(string); ok {
			requirements.Scope = scope
		}
		if goals, ok := params["goals"].([]string); ok {
			requirements.Goals = goals
		}
		if constraints, ok := params["constraints"].([]string); ok {
			requirements.Constraints = constraints
		}
	}

	// If no explicit goals, derive from query
	if len(requirements.Goals) == 0 {
		requirements.Goals = []string{query}
	}

	return requirements, nil
}

// consultLibrarian queries the Librarian for relevant codebase patterns
func (a *Architect) consultLibrarian(ctx context.Context, requirements *Requirements, sessionID string) (*CodebasePatterns, error) {
	if requirements == nil {
		return emptyCodebasePatterns(), nil
	}
	query := fmt.Sprintf("Find patterns related to: %s", requirements.Query)
	evidence, err := a.requestConsultation(ctx, "librarian", query, requirements.Scope, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to consult librarian: %w", err)
	}
	if evidence == nil || !evidence.Success {
		return nil, fmt.Errorf("librarian consultation did not return success")
	}
	return codebasePatternsFromEvidence(evidence), nil
}

// designArchitecture creates a solution architecture based on requirements
func (a *Architect) designArchitecture(ctx context.Context, requirements *Requirements, patterns *CodebasePatterns) (*SolutionArchitecture, error) {
	if architecture, ok := a.tryDesignArchitectureWithLLM(ctx, requirements, patterns); ok {
		a.logInfo("designArchitecture: LLM path used", "components", len(architecture.Components))
		return architecture, nil
	}
	a.logInfo("designArchitecture: deterministic fallback",
		"goals", len(requirements.Goals))

	architecture := &SolutionArchitecture{
		Name:        fmt.Sprintf("Architecture for: %s", truncateString(requirements.Query, 50)),
		Description: requirements.Query,
		Components:  deriveComponentsFromGoals(requirements),
		Interfaces:  []InterfaceSpec{},
		Patterns:    []string{},
	}

	// Add patterns from codebase analysis
	if patterns != nil {
		for _, p := range patterns.Patterns {
			architecture.Patterns = append(architecture.Patterns, p.Name)
		}
	}

	a.logInfo("designArchitecture: deterministic result",
		"components", len(architecture.Components))
	return architecture, nil
}

// deriveComponentsFromGoals creates one component per requirement goal
// so the deterministic task fallback has meaningful structure.
func deriveComponentsFromGoals(requirements *Requirements) []ComponentSpec {
	if requirements == nil || len(requirements.Goals) == 0 {
		return nil
	}
	components := make([]ComponentSpec, 0, len(requirements.Goals))
	for i, goal := range requirements.Goals {
		goal = strings.TrimSpace(goal)
		if goal == "" {
			continue
		}
		components = append(components, ComponentSpec{
			Name:        fmt.Sprintf("goal_%d", i+1),
			Type:        "backend",
			Description: goal,
		})
	}
	return components
}

// =============================================================================
// Atomic Task Generation System
// =============================================================================

// generateAtomicTasks creates atomic tasks from the architecture
// Rules:
// - Each task should be completable by a single agent
// - Tasks should have clear success criteria
// - Dependencies must be explicit
func (a *Architect) generateAtomicTasks(ctx context.Context, architecture *SolutionArchitecture, constraints *PlanConstraints) ([]*AtomicTask, error) {
	if tasks, ok := a.tryGenerateTasksWithLLM(ctx, architecture, constraints); ok {
		a.logInfo("generateAtomicTasks: LLM path used", "tasks", len(tasks))
		return tasks, nil
	}
	a.logInfo("generateAtomicTasks: deterministic fallback",
		"components", len(architecture.Components))

	tasks := make([]*AtomicTask, 0)

	// Generate tasks for each component
	for i, component := range architecture.Components {
		assignment := determineTaskAgents(component)
		task := &AtomicTask{
			ID:                fmt.Sprintf("task_%d", i+1),
			Name:              fmt.Sprintf("Implement %s", component.Name),
			Description:       component.Description,
			AgentType:         assignment.Primary,
			CoAgents:          assignment.CoAgents,
			CollaborationMode: assignment.Mode,
			SuccessCriteria:   generateSuccessCriteria(component),
			Dependencies:      component.Dependencies,
			EstimatedTokens:   estimateTaskTokens(component),
			Complexity:        estimateComplexity(component),
			Status:            TaskStatusPending,
			AcceptanceCriteria: []AcceptanceCriterion{{
				Given:    "the codebase is in a clean state",
				When:     fmt.Sprintf("the %s component is implemented", component.Name),
				Then:     "all success criteria are met and tests pass",
				Priority: "must",
			}},
			ImplementationGuide: fmt.Sprintf("Implement the %s component as described. Follow existing codebase patterns.", component.Name),
			AffectedFiles:       deterministicAffectedFiles(component),
		}
		tasks = append(tasks, task)
	}

	// If no components defined, create a single task from the architecture description
	if len(tasks) == 0 {
		task := &AtomicTask{
			ID:              "task_1",
			Name:            architecture.Name,
			Description:     architecture.Description,
			AgentType:       "engineer",
			SuccessCriteria: []string{"Implementation complete", "Tests passing"},
			Dependencies:    []string{},
			EstimatedTokens: 5000,
			Complexity:      ComplexityMedium,
			Status:          TaskStatusPending,
			AcceptanceCriteria: []AcceptanceCriterion{{
				Given:    "the codebase is in a clean state",
				When:     "the implementation is complete",
				Then:     "all tests pass and the feature works as described",
				Priority: "must",
			}},
			ImplementationGuide: fmt.Sprintf("Implement %s as described in the architecture.", architecture.Name),
			AffectedFiles:       []TaskFileTarget{{Path: "TBD", Operation: "create", Reason: "primary implementation file"}},
		}
		tasks = append(tasks, task)
	}

	return normalizeTaskGraph(tasks), nil
}

func deterministicAffectedFiles(component ComponentSpec) []TaskFileTarget {
	if component.FilePath != "" {
		return []TaskFileTarget{{
			Path:      component.FilePath,
			Operation: "modify",
			Reason:    fmt.Sprintf("primary file for %s component", component.Name),
		}}
	}
	return []TaskFileTarget{{
		Path:      fmt.Sprintf("%s.go", strings.ToLower(strings.ReplaceAll(component.Name, " ", "_"))),
		Operation: "create",
		Reason:    fmt.Sprintf("implementation file for %s component", component.Name),
	}}
}

// taskAgentAssignment describes the primary agent and optional co-tenants for
// a task derived from a component specification.
type taskAgentAssignment struct {
	Primary  string
	CoAgents []string
	Mode     dag.CollaborationMode
}

// determineTaskAgents inspects a component's type and metadata to decide
// whether it needs single-agent or compound-node execution.
func determineTaskAgents(component ComponentSpec) taskAgentAssignment {
	ctype := strings.ToLower(strings.TrimSpace(component.Type))
	hasDeps := len(component.Dependencies) > 0

	switch ctype {
	case "design", "ui":
		return taskAgentAssignment{Primary: "designer"}
	case "fullstack", "full-stack", "full_stack":
		return taskAgentAssignment{
			Primary:  "engineer",
			CoAgents: []string{"designer"},
			Mode:     dag.CollaborationAdversarial,
		}
	}

	// Heuristic: components with UI-related metadata that also touch backend
	// concerns benefit from Engineer+Designer co-tenancy.
	meta := component.Metadata
	if meta != nil {
		if hasUIIndicator(meta) && hasBackendIndicator(meta, hasDeps) {
			return taskAgentAssignment{
				Primary:  "engineer",
				CoAgents: []string{"designer"},
				Mode:     dag.CollaborationAdversarial,
			}
		}
	}

	return taskAgentAssignment{Primary: "engineer"}
}

func hasUIIndicator(meta map[string]any) bool {
	for _, key := range []string{"ui", "frontend", "design", "visual", "layout"} {
		if _, ok := meta[key]; ok {
			return true
		}
	}
	return false
}

func hasBackendIndicator(meta map[string]any, hasDeps bool) bool {
	if hasDeps {
		return true
	}
	for _, key := range []string{"backend", "api", "database", "server", "infra"} {
		if _, ok := meta[key]; ok {
			return true
		}
	}
	return false
}

func generateSuccessCriteria(component ComponentSpec) []string {
	criteria := []string{
		fmt.Sprintf("Component %s is implemented", component.Name),
		"Code compiles without errors",
		"Tests pass",
	}
	return criteria
}

// baseTokensPerInterface is the estimated token cost of implementing a single
// interface method or component interface. Derived empirically from observed
// LLM codegen: ~500 tokens per non-trivial function/method.
const baseTokensPerInterface = 500

// estimateTaskTokens computes a token budget from the component's structural
// complexity. Each interface and dependency adds surface area for the LLM.
func estimateTaskTokens(component ComponentSpec) int {
	interfaceCount := max(1, len(component.Interfaces))
	depCount := len(component.Dependencies)
	return interfaceCount*baseTokensPerInterface + depCount*baseTokensPerInterface
}

// estimateComplexity maps structural signals (dependency count, interface
// count) to the TaskComplexity enum. Thresholds are derived from the enum
// cardinality (4 levels) — each level spans ceil(maxDeps/4) dependencies.
func estimateComplexity(component ComponentSpec) TaskComplexity {
	// Composite signal: dependencies + interfaces
	signal := len(component.Dependencies) + len(component.Interfaces)
	switch {
	case signal >= int(ComplexityCritical)*2:
		return ComplexityCritical
	case signal >= int(ComplexityHigh)*2:
		return ComplexityHigh
	case signal >= int(ComplexityMedium)*2:
		return ComplexityMedium
	default:
		return ComplexityLow
	}
}

// reviewBudget computes the adversarial review round budget for a compound
// node from the task's structural entropy. Inspired by RL exploration budgets:
// tasks with more design surface area (interfaces, dependencies, files) have
// higher uncertainty and benefit from more review rounds.
//
// The budget uses the log2 of the total structural signal count, which gives
// diminishing returns: doubling the surface area adds only one more round.
// This mirrors the information-theoretic insight that marginal information
// gain decreases with each additional review pass.
//
// Floor is 1 (every compound node gets at least one review). Ceiling is
// ComplexityCritical (prevents runaway review chains).
func reviewBudget(task *AtomicTask) int {
	// Structural signals: each adds design surface area
	signal := len(task.Dependencies) + len(task.SuccessCriteria) + len(task.AffectedFiles)
	if task.ImplementationGuide != "" {
		signal++ // non-trivial task
	}
	signal += int(task.Complexity)

	// log2(signal+1) gives: signal=0→0, 1→1, 3→2, 7→3, 15→4
	// +1 prevents log2(0); the floor/ceil clamp handles the rest.
	rounds := int(math.Log2(float64(signal + 1)))
	rounds = max(1, rounds)
	return min(rounds, int(ComplexityCritical))
}

// =============================================================================
// Workflow DAG Creation
// =============================================================================

// createWorkflowDAG creates a workflow DAG for task orchestration
func (a *Architect) createWorkflowDAG(ctx context.Context, tasks []*AtomicTask) (*WorkflowDAG, error) {
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks to create workflow from")
	}

	// Create the DAG using the builder
	builder := dag.NewBuilder(fmt.Sprintf("Workflow with %d tasks", len(tasks)))

	// Add nodes for each task, populating compound node fields when present.
	for _, task := range tasks {
		nodeConfig := dag.NodeConfig{
			ID:                task.ID,
			AgentType:         task.AgentType,
			Prompt:            task.Description,
			Dependencies:      task.Dependencies,
			Priority:          taskPriority(task),
			CoAgents:          task.CoAgents,
			CollaborationMode: task.CollaborationMode,
			Context: map[string]any{
				"task_name":        task.Name,
				"success_criteria": task.SuccessCriteria,
				"complexity":       task.Complexity.String(),
			},
			Metadata: map[string]any{
				"estimated_tokens": task.EstimatedTokens,
			},
		}
		if len(task.CoAgents) > 0 && task.CollaborationMode == dag.CollaborationAdversarial {
			nodeConfig.MaxReviewRounds = reviewBudget(task)
		}
		builder.AddNode(nodeConfig)
	}

	// Build and validate the DAG
	d, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build workflow DAG: %w", err)
	}

	// Wrap in WorkflowDAG
	workflow := &WorkflowDAG{
		DAG:             d,
		Tasks:           tasks,
		TotalTasks:      len(tasks),
		EstimatedTokens: calculateTotalTokens(tasks),
		CreatedAt:       time.Now(),
	}

	return workflow, nil
}

// taskPriority derives scheduling priority from task structure. Tasks with
// fewer dependencies are scheduled first (higher priority). The complexity
// enum also factors in — more complex tasks get slight priority to keep the
// critical path from stalling.
func taskPriority(task *AtomicTask) int {
	depPenalty := len(task.Dependencies) * int(ComplexityCritical)
	complexityBoost := int(task.Complexity)
	return complexityBoost - depPenalty
}

func calculateTotalTokens(tasks []*AtomicTask) int {
	total := 0
	for _, t := range tasks {
		total += t.EstimatedTokens
	}
	return total
}

// =============================================================================
// Additional Handlers
// =============================================================================

func (a *Architect) executeDesignArchitecture(ctx context.Context, req *ArchitectRequest) (*SolutionArchitecture, error) {
	requirements := &Requirements{
		Query: req.Query,
		Goals: []string{req.Query},
		Scope: "project",
	}
	return a.designArchitecture(ctx, requirements, nil)
}

func (a *Architect) executeGenerateTasks(ctx context.Context, req *ArchitectRequest) ([]*AtomicTask, error) {
	// Get architecture from params or create minimal one
	architecture := &SolutionArchitecture{
		Name:        "Task Generation",
		Description: req.Query,
		Components:  []ComponentSpec{},
	}

	if req.Params != nil {
		if arch, ok := req.Params["architecture"].(*SolutionArchitecture); ok {
			architecture = arch
		}
	}

	constraints := extractConstraints(req.Params)
	return a.generateAtomicTasks(ctx, architecture, constraints)
}

func (a *Architect) executeCreateDAG(ctx context.Context, req *ArchitectRequest) (*WorkflowDAG, error) {
	// Get tasks from params or create minimal tasks
	var tasks []*AtomicTask

	if req.Params != nil {
		if t, ok := req.Params["tasks"].([]*AtomicTask); ok {
			tasks = t
		}
	}

	if len(tasks) == 0 {
		// Create a single task from the query
		tasks = []*AtomicTask{
			{
				ID:              "task_1",
				Name:            "Execute",
				Description:     req.Query,
				AgentType:       "engineer",
				SuccessCriteria: []string{"Task completed"},
				Dependencies:    []string{},
				EstimatedTokens: 3000,
				Complexity:      ComplexityMedium,
				Status:          TaskStatusPending,
			},
		}
	}

	return a.createWorkflowDAG(ctx, tasks)
}

func (a *Architect) executeRecall(ctx context.Context, req *ArchitectRequest) (any, error) {
	return a.planStore.MatchingQuery(req.Query), nil
}

func (a *Architect) executeCheck(ctx context.Context, req *ArchitectRequest) (any, error) {
	plan := a.planStore.FirstMatchingQuery(req.Query)
	if plan != nil {
		return map[string]any{
			"found":  true,
			"plan":   plan,
			"status": plan.Status.String(),
		}, nil
	}
	return map[string]any{
		"found": false,
	}, nil
}

// executeConversation handles conversational intents (help, consult, estimate,
// converse, plan, design, and unclassified) with a single LLM call instead of
// the full planning protocol.
func (a *Architect) executeConversation(ctx context.Context, req *ArchitectRequest) (any, error) {
	a.logInfo("executeConversation: entry",
		"intent", string(req.Intent),
		"query", truncateString(req.Query, 120),
		"session_id", req.SessionID,
		"ctx_deadline", contextDeadlineString(ctx),
		"planner_available", a.ensurePlanner(ctx) != nil)
	ctx = withArchitectSessionID(ctx, req.SessionID)
	ctx = withArchitectConversationContext(ctx, req.Query, req.ConversationHistory)
	ctx = withPlannerThoughtCallback(ctx, func(stage string, thought string) {
		a.publishPlanThought(ctx, stage, thought)
	})
	request := plannerConversationRequest{
		Mode:                plannerConversationModeConverse,
		UserQuery:           req.Query,
		IntentHint:          string(req.Intent),
		ConversationHistory: req.ConversationHistory,
		OnChunk: func(text string) {
			a.publishPlanStreamChunk(ctx, text)
		},
	}
	// Inject session plan context so the LLM has continuity even when
	// conversation history is degraded (e.g. prior protocols hung and
	// never delivered agent replies back to the Guide).
	a.enrichConversationWithPlanContext(&request, req.SessionID)
	a.logInfo("executeConversation: calling composeUserFacingResponse",
		"has_plan_summary", request.PlanSummary != "",
		"has_prior_query", request.PriorQuery != "")
	requestCorrelationID := originalCIDFromContext(ctx)
	composeStart := time.Now()
	response, composeErr := a.composeUserFacingResponse(ctx, request)
	a.logInfo("executeConversation: composeUserFacingResponse returned",
		"elapsed", time.Since(composeStart).String(),
		"response_len", len(response),
		"err", composeErr)
	if composeErr == nil {
		a.logInfo("executeConversation: LLM compose succeeded",
			"response_len", len(response))
		result := &ConversationResult{
			Response: response,
			Intent:   req.Intent,
		}
		// If a ready plan now exists (e.g. the tool loop called planning
		// skills that created one), arm the Guide's phase gate so the
		// next user message gets plan-approval classification.
		if plan := a.latestReadyPlan(req.SessionID); plan != nil {
			result.Directive = a.feedbackReadyDirective(plan)
			architectDebugLog().Info("executeConversation: DIRECTIVE_SET",
				"plan_id", plan.ID,
				"session_id", req.SessionID,
				"phase", string(result.Directive.Phase))
		} else if stalled := a.latestStalledPlanForRequest(req.SessionID, requestCorrelationID); stalled != nil {
			// The tool loop started a plan (via start_planning) but
			// couldn't complete it — e.g. API overloaded mid-protocol.
			// Recover via deterministic protocol so the plan reaches Ready.
			a.recoverStalledPlan(ctx, stalled)
			if plan := a.latestReadyPlan(req.SessionID); plan != nil {
				result.Directive = a.feedbackReadyDirective(plan)
			}
		} else {
			architectDebugLog().Info("executeConversation: NO_READY_PLAN",
				"session_id", req.SessionID)
		}
		return result, nil
	}
	// The tool loop may have successfully created a plan via start_planning
	// even though the outer context was cancelled before the final LLM turn
	// could compose a summary. Recover the plan's response and directive
	// rather than falling through to conversationFallback (which would run
	// a redundant planning protocol with a cancelled context).
	if plan := a.latestReadyPlan(req.SessionID); plan != nil {
		a.logInfo("executeConversation: compose failed but ready plan exists, recovering",
			"plan_id", plan.ID,
			"plan_response_len", len(plan.UserResponse))
		userResp := plan.UserResponse
		if strings.TrimSpace(userResp) == "" {
			userResp = fallbackReadyUserResponse(nil, plan)
		}
		return &ConversationResult{
			Response:  userResp,
			Intent:    req.Intent,
			Directive: a.feedbackReadyDirective(plan),
		}, nil
	}
	// Check for stalled plans before falling back to conversationFallback.
	if stalled := a.latestStalledPlanForRequest(req.SessionID, requestCorrelationID); stalled != nil {
		a.recoverStalledPlan(ctx, stalled)
		if plan := a.latestReadyPlan(req.SessionID); plan != nil {
			userResp := fallbackReadyUserResponse(nil, plan)
			return &ConversationResult{
				Response:  userResp,
				Intent:    req.Intent,
				Directive: a.feedbackReadyDirective(plan),
			}, nil
		}
	}
	a.logWarn("executeConversation: compose failed, using domain fallback",
		"intent", string(req.Intent), "error", composeErr)
	return a.conversationFallback(ctx, req, composeErr)
}

// enrichConversationWithPlanContext adds the latest plan summary and prior
// query to a conversation request when an active plan exists for the session.
// This prevents context loss when previous protocol runs hung before the
// Guide could record the architect's response in conversation history.
func (a *Architect) enrichConversationWithPlanContext(request *plannerConversationRequest, sessionID string) {
	plan := a.latestHistoricalPlanForSession(sessionID)
	if plan == nil {
		return
	}
	if summary := formatPlanForChat(plan); summary != "" {
		request.PlanSummary = summary
	}
	request.PriorQuery = plan.Query
	if plan.Requirements != nil {
		request.Scope = plan.Requirements.Scope
	}
}

// conversationFallback runs the domain-specific execution path when the
// conversational LLM is unavailable. Only starts the planning protocol
// for plan/design intents — conversational intents (chat, help, etc.)
// get a graceful text response instead of an inappropriate planning run.
func (a *Architect) conversationFallback(ctx context.Context, req *ArchitectRequest, composeErr error) (any, error) {
	a.logInfo("conversationFallback: entry",
		"intent", string(req.Intent),
		"planner_available", a.ensurePlanner(ctx) != nil)
	if a.ensurePlanner(ctx) == nil {
		a.logWarn("conversationFallback: no planner configured")
		return &ConversationResult{
			Response: "I can't generate a detailed plan right now — my LLM planner is not configured. " +
				"Please ensure Anthropic credentials are available (OAuth login or ANTHROPIC_API_KEY / secure credential store).",
			Intent: req.Intent,
		}, nil
	}
	if !isConversationFallbackPlanningIntent(req.Intent) {
		a.logInfo("conversationFallback: non-planning intent, returning text response",
			"intent", string(req.Intent))
		return &ConversationResult{
			Response: "I'm having trouble processing that right now. Could you rephrase or try again?",
			Intent:   req.Intent,
		}, nil
	}
	if req.Intent == IntentDesign {
		return a.executeDesignArchitecture(ctx, req)
	}
	plan, err := a.executePlanningProtocol(ctx, req)
	if err != nil {
		friendly := providers.FriendlyErrorMessage(err)
		a.logWarn("conversationFallback: protocol failed, returning friendly message",
			"raw_error", err, "friendly", friendly)
		return &ConversationResult{
			Response: friendly,
			Intent:   req.Intent,
		}, nil
	}
	return plan, nil
}

// isConversationFallbackPlanningIntent returns true for intents where
// starting a planning protocol is an appropriate fallback when the LLM
// conversation path fails. Conversational intents (chat, help, etc.)
// should never fall back to planning — that misinterprets the user's
// message and can trigger long-running protocol hangs.
func isConversationFallbackPlanningIntent(intent ArchitectIntent) bool {
	switch intent {
	case IntentPlan, IntentDesign, IntentGenerateTasks, IntentCreateDAG:
		return true
	default:
		return false
	}
}

// handleDomainQuery handles cross-domain queries
func (a *Architect) handleDomainQuery(ctx context.Context, d domain.Domain, query string) (*DomainResult, error) {
	target := crossDomainTarget(d)
	if target == "" || target == "architect" {
		return localDomainResult(d, query), nil
	}
	if !a.running || a.bus == nil {
		return localDomainResult(d, query), nil
	}
	evidence, err := a.requestConsultation(ctx, target, query, "", "")
	if err != nil {
		return nil, err
	}
	return consultedDomainResult(d, query, target, evidence), nil
}

func crossDomainTarget(d domain.Domain) string {
	switch d {
	case domain.DomainLibrarian, domain.DomainAcademic, domain.DomainArchivalist:
		return d.String()
	case domain.DomainEngineer, domain.DomainDesigner, domain.DomainInspector, domain.DomainTester:
		return d.String()
	case domain.DomainOrchestrator:
		return "orchestrator"
	default:
		return ""
	}
}

func localDomainResult(d domain.Domain, query string) *DomainResult {
	return &DomainResult{
		Domain:      d,
		Query:       query,
		Content:     "",
		Score:       0,
		Source:      "architect",
		RetrievedAt: time.Now(),
	}
}

func consultedDomainResult(
	d domain.Domain,
	query string,
	target string,
	evidence *ConsultationEvidence,
) *DomainResult {
	if evidence == nil {
		return localDomainResult(d, query)
	}
	return &DomainResult{
		Domain:      d,
		Query:       query,
		Content:     consultationContent(evidence.Data),
		Score:       consultationScore(evidence),
		Source:      target,
		RetrievedAt: evidence.ReceivedAt,
		ErrorMsg:    evidence.Error,
	}
}

func consultationContent(data any) string {
	if data == nil {
		return ""
	}
	if text, ok := data.(string); ok {
		return text
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Sprintf("%v", data)
	}
	return string(encoded)
}

func consultationScore(evidence *ConsultationEvidence) float64 {
	if evidence == nil {
		return 0
	}
	if evidence.Success {
		return 1
	}
	return 0
}

// =============================================================================
// Guide Registration
// =============================================================================

// GetRoutingInfo returns the architect's routing information for Guide registration
func (a *Architect) GetRoutingInfo() *guide.AgentRoutingInfo {
	return ArchitectRoutingInfo(a.id)
}

// PublishRequest publishes a request to the Guide for routing
func (a *Architect) PublishRequest(req *guide.RouteRequest) error {
	if !a.running {
		return fmt.Errorf("architect is not running")
	}

	req.SourceAgentID = a.id
	req.SourceAgentName = "architect"

	msg := guide.NewRequestMessage(a.generateMessageID(), req)
	return a.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// Helper Functions
// =============================================================================

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// contextDeadlineString returns a human-readable string for the context deadline.
// Returns "none" if no deadline is set.
func contextDeadlineString(ctx context.Context) string {
	if ctx == nil {
		return "nil_ctx"
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return "none"
	}
	remaining := time.Until(deadline)
	return fmt.Sprintf("%s (remaining: %s)", deadline.Format("15:04:05.000"), remaining.Truncate(time.Millisecond))
}

// activePlanCount returns the number of active plans (for diagnostic logging).
func (a *Architect) activePlanCount() int {
	return a.planStore.Count()
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// unwrapArchitectResult extracts the inner result from an ArchitectResponse
// wrapper. Returns the input unchanged for all other types.
func unwrapArchitectResult(data any) any {
	if v, ok := data.(*ArchitectResponse); ok && v != nil {
		return v.Data
	}
	return data
}

// extractUserResponse returns the human-readable UserResponse from a planning,
// conversation, or architect response result.
func extractUserResponse(data any) string {
	switch v := data.(type) {
	case *ArchitectResponse:
		if v != nil {
			return v.UserResponse
		}
	case *DesignPlan:
		if v != nil {
			return v.UserResponse
		}
	case *ConversationResult:
		if v != nil {
			return v.Response
		}
	case *SolutionArchitecture:
		if v != nil {
			return v.Description
		}
	}
	return ""
}

// GetActivePlan returns an active plan by ID
func (a *Architect) GetActivePlan(id string) (*DesignPlan, bool) {
	plan := a.planStore.Get(id)
	return plan, plan != nil
}

// GetAllActivePlans returns all active plans
func (a *Architect) GetAllActivePlans() []*DesignPlan {
	return a.planStore.Snapshot()
}

// =============================================================================
// HandoffableAgent + ContextEvictable Implementation
// =============================================================================

// SetHandoffBridge sets the handoff bridge for the architect agent.
func (a *Architect) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	a.handoffBridge = bridge
}

// SetAgentPod assigns the agent pod for cross-agent coordination.
func (a *Architect) SetAgentPod(pod *shared.AgentPod) {
	a.agentPod = pod
}

// AgentID returns the unique identifier for this architect instance.
func (a *Architect) AgentID() string {
	return a.id
}

// SetCanonicalID overwrites the architect's internal ID. Used during
// handoff swap so the new instance assumes the canonical identity.
func (a *Architect) SetCanonicalID(id string) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.id = id
}

// AgentType returns the agent type classification.
func (a *Architect) AgentType() string {
	return "architect"
}

// Descriptor returns the immutable agent descriptor for handoff participation.
func (a *Architect) Descriptor() handoff.AgentDescriptor {
	modelID := a.CurrentModel()
	return handoff.AgentDescriptor{
		AgentType:             "architect",
		ModelID:               modelID,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryKnowledge,
		RuntimeProfiles:       architectRuntimeProfiles(),
		DefaultRuntimeProfile: architectDefaultRuntimeProfile(),
	}
}

// ExtractArchivableState captures the architect's state for handoff persistence.
func (a *Architect) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   a.AgentID(),
		AgentType: a.AgentType(),
		Timestamp: time.Now(),
	}
}

// Terminate gracefully shuts down the architect agent.
func (a *Architect) Terminate(ctx context.Context) error {
	return a.Stop()
}

// EvictEntries frees context by evicting the given candidates.
// Returns the total number of tokens freed across all evicted entries.
func (a *Architect) EvictEntries(candidates []handoff.EvictionCandidate) (freedTokens int, err error) {
	for _, c := range candidates {
		freedTokens += c.Entry.GetTokenCount()
	}
	return freedTokens, nil
}
