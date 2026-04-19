package guardian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/logging"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// =============================================================================
// Guardian Agent
// =============================================================================

// guardianProvider is the minimal interface Guardian needs from its LLM.
// Satisfied by *providers.OpenAIProvider and *gateway.GatewayProvider.
// Used only for on-demand escalation of ambiguous findings.
type guardianProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
	Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error)
}

// Guardian is the sidecar safety agent for the Sylk system.
// It observes all agent activity, gates mutating git operations behind
// explicit user approval, validates content for injection/credential leaks,
// and monitors agent health. Guardian has NO filesystem write access.
type Guardian struct {
	id       string
	config   Config
	logger   *slog.Logger
	identity *identity.AgentIdentity
	factory  *identity.Factory

	// LLM provider — GPT-5.4 Pro for on-demand escalation.
	// Deterministic by default; LLM invoked only for ambiguous cases.
	provider   guardianProvider
	refresher  container.ProviderRefresher
	providerMu sync.RWMutex
	model      string

	// Skills system (Architect pattern).
	skills        *skills.Registry
	skillLoader   *skills.Loader
	hooks         *skills.HookRegistry
	tools         *toolruntime.Runtime
	toolDefsDirty bool

	// Activity publisher — emit events, never maintain own journal.
	activityPub events.ActivityPublisher

	// Bus integration (standard agent pattern).
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	activitySub guide.Subscription // observe all activity events
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement

	// Subsystems.
	gitObserver      *GitObserver
	checkpointMgr    *CheckpointManager
	contentValidator *ContentValidator
	externalScanner  *ExternalContentScanner
	contentSafety    *ContentSafetyValidator
	healthMon        *HealthMonitor
	diffGate         *DiffGate

	// Quarantine buffer — set via SetQuarantine after construction.
	quarantine *fetch.QuarantineBuffer

	// Domain reputation — session-scoped trust tracking.
	domainReputation *DomainReputationTracker

	// File access — always read-only.
	fileAccess     versioning.FileAccess
	workspaceViews versioning.WorkspaceViewAccess

	// User approval flow.
	pendingMu        sync.Mutex
	pendingApprovals map[string]chan ApprovalResult
	commandRules     *commandapproval.RuleStore

	// Conversation state.
	conversationHistory []guide.ConversationTurn
	conversationMu      sync.RWMutex

	// Request lifecycle.
	runMu      sync.RWMutex
	runCtx     context.Context
	runCancel  context.CancelFunc
	inFlightMu sync.Mutex
	inFlight   map[string]context.CancelFunc
	knownMu    sync.RWMutex

	// Steering ledger management.
	steering *shared.SteeringManager

	// Request serialization: ensures at most one forwarded request
	// executes at a time, preventing cancel/new-request interleaving.
	requestSerializer *shared.RequestSerializer

	// Handoff bridge for context-aware handoff.
	handoffBridge *handoff.HandoffBridge

	// Agent pod for Scribe feed.
	agentPod *shared.AgentPod
}

// ---------------------------------------------------------------------------
// Debug logger
// ---------------------------------------------------------------------------

var (
	guardianDebugLogger     *slog.Logger
	guardianDebugLoggerOnce sync.Once
)

func guardianDebugLog() *slog.Logger {
	guardianDebugLoggerOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".sylk", "logs")
		_ = os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(filepath.Join(dir, "guardian_debug.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			guardianDebugLogger = slog.Default()
			return
		}
		guardianDebugLogger = slog.New(slog.NewTextHandler(f, logging.HandlerOptions(slog.LevelDebug)))
	})
	return guardianDebugLogger
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// New creates a new Guardian agent with the given LLM provider.
func New(cfg Config, provider guardianProvider) (*Guardian, error) {
	cfg = applyDefaults(cfg)
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	guardianID := cfg.ID
	if guardianID == "" {
		guardianID = "guardian"
	}

	// Content validation defaults.
	if !cfg.InjectionScanEnabled {
		cfg.InjectionScanEnabled = true
	}
	if !cfg.CredentialScanEnabled {
		cfg.CredentialScanEnabled = true
	}
	if !cfg.DiffReviewEnabled {
		cfg.DiffReviewEnabled = true
	}

	g := &Guardian{
		id:                guardianID,
		config:            cfg,
		logger:            cfg.Logger,
		factory:           cfg.Factory,
		provider:          provider,
		model:             DefaultGuardianModel,
		activityPub:       cfg.ActivityPub,
		fileAccess:        authority.RestrictFileAccess("guardian", cfg.FileAccess),
		workspaceViews:    authority.RestrictWorkspaceViews("guardian", cfg.WorkspaceViews),
		knownAgents:       make(map[string]*guide.AgentAnnouncement),
		pendingApprovals:  make(map[string]chan ApprovalResult),
		commandRules:      commandapproval.NewRuleStore(""),
		inFlight:          make(map[string]context.CancelFunc),
		steering:          shared.NewSteeringManager(),
		requestSerializer: shared.NewRequestSerializer(),
	}

	g.factory = cfg.Factory
	guardianIdentity, err := cfg.Factory.Mint(identity.MintOptions{
		Kind: identity.AgentTypeGuardian,
		Pod:  identity.PodRef{ID: "guardian", Type: identity.PodTypeDaemon},
	})
	if err != nil {
		return nil, fmt.Errorf("guardian: mint identity: %w", err)
	}
	g.identity = guardianIdentity

	g.steering.InitLazy("guardian", cfg.ActivityPub)

	if err := g.initSkills(); err != nil {
		return nil, err
	}
	g.initSubsystems()

	return g, nil
}

func (g *Guardian) initSkills() error {
	g.skills = skills.NewRegistry()
	g.hooks = skills.NewHookRegistry()
	g.registerCoreSkills()

	if err := shared.RegisterMemoryForestSkills(g.skills, "guardian", g.config.Forest, nil); err != nil {
		return fmt.Errorf("register guardian forest skills: %w", err)
	}

	manifest := guardianToolManifestForRegistry(g.skills)

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = filterSyntheticGuardianRuntimeTools(manifest.DefaultVisibleNames())
	loaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	g.skillLoader = skills.NewLoader(g.skills, loaderCfg)

	registerGuardianSafetyHook(g.hooks, g.skills, manifest.AllowedNames())
	tools, err := toolruntime.New(toolruntime.Config{
		Registry: g.skills,
		Hooks:    g.hooks,
		Manifest: manifest,
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return fmt.Errorf("initialize guardian tool runtime: %w", err)
	}
	g.tools = tools
	g.tools.SyncActiveFromLoaded()
	return nil
}

func (g *Guardian) initSubsystems() {
	g.contentValidator = NewContentValidator(g.config.Sanitizer, g.config.InjectionScanEnabled)
	g.externalScanner = NewExternalContentScanner()
	g.contentSafety = NewContentSafetyValidator()
	g.domainReputation = NewDomainReputationTracker()
	g.healthMon = NewHealthMonitor(g.config.HealthCheckInterval, g.config.AgentTimeoutDefault, g.config.TokenBudget, g.config.CostBudget)
	g.healthMon.SetOnEvent(func(evt agentlog.EventType, level string, payload any) {
		shared.LogAgentEvent(g.steering.EventLogger(), evt, g.id, "", "", level, payload)
	})
	g.diffGate = NewDiffGate(g.config.Sanitizer, g.config.SuspiciousDiffPatterns)
}

// ---------------------------------------------------------------------------
// ContainerAgent interface
// ---------------------------------------------------------------------------

func (g *Guardian) AgentID() string   { return g.id }
func (g *Guardian) AgentType() string { return "guardian" }

// SetCanonicalID preserves the routing identity across handoff replacement.
func (g *Guardian) SetCanonicalID(id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	g.id = id
}

func (g *Guardian) Terminate(ctx context.Context) error {
	return g.Stop()
}

// ---------------------------------------------------------------------------
// Descriptor
// ---------------------------------------------------------------------------

func (g *Guardian) Descriptor() handoff.AgentDescriptor {
	modelID := g.CurrentModel()
	return handoff.AgentDescriptor{
		AgentType:             "guardian",
		ModelID:               modelID,
		ReasoningEffort:       "high",
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryStandalone,
		RuntimeProfiles:       guardianRuntimeProfiles(),
		DefaultRuntimeProfile: guardianDefaultRuntimeProfile(),
	}
}

// SetAgentPod injects the agent pod for Scribe feed integration.
func (g *Guardian) SetAgentPod(pod *shared.AgentPod) {
	g.agentPod = pod
}

// SetHandoffBridge stores the bridge for handoff context tracking.
func (g *Guardian) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	g.handoffBridge = bridge
	if bridge != nil {
		bridge.SetActivityPublisher(g.activityPub)
	}
}

// ---------------------------------------------------------------------------
// Provider management (thread-safe)
// ---------------------------------------------------------------------------

// SetProvider sets or replaces the LLM provider at runtime.
func (g *Guardian) SetProvider(p guardianProvider) {
	g.providerMu.Lock()
	defer g.providerMu.Unlock()
	g.provider = p
}

// SetProviderRefresher stores a callback that creates a fresh provider for
// the current model and auth method. Set by cmd/tui.go at bootstrap.
func (g *Guardian) SetProviderRefresher(fn container.ProviderRefresher) {
	g.providerMu.Lock()
	defer g.providerMu.Unlock()
	g.refresher = fn
}

func (g *Guardian) getProvider() guardianProvider {
	g.providerMu.RLock()
	defer g.providerMu.RUnlock()
	return g.provider
}

// ---------------------------------------------------------------------------
// AuthRefreshable
// ---------------------------------------------------------------------------

func (g *Guardian) ProviderType() string {
	return string(container.ProviderForModel(g.CurrentModel()))
}

func (g *Guardian) RefreshProvider(ctx context.Context, authMethod string) error {
	g.providerMu.RLock()
	fn := g.refresher
	g.providerMu.RUnlock()
	if fn == nil {
		return nil
	}
	p, err := fn(ctx, g.CurrentModel(), authMethod)
	if err != nil {
		return fmt.Errorf("guardian refresh provider: %w", err)
	}
	g.SetProvider(p)
	g.logger.Info("provider refreshed", "model", g.CurrentModel(), "auth_method", authMethod)
	return nil
}

// ---------------------------------------------------------------------------
// ModelSwappable
// ---------------------------------------------------------------------------

func (g *Guardian) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	g.SetProvider(provider)
	g.providerMu.Lock()
	g.model = modelID
	g.providerMu.Unlock()

	// Clear conversation history on model swap.
	g.conversationMu.Lock()
	g.conversationHistory = nil
	g.conversationMu.Unlock()

	g.logger.Info("model swapped", "model", modelID)
	return nil
}

func (g *Guardian) CurrentModel() string {
	g.providerMu.RLock()
	defer g.providerMu.RUnlock()
	if g.model != "" {
		return g.model
	}
	return DefaultGuardianModel
}

func (g *Guardian) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
	}
}

// ---------------------------------------------------------------------------
// Deferred Git Wiring
// ---------------------------------------------------------------------------

// SetRequestGuard wires the demotion guard after construction.
// Must be called before the first request to prevent activation demotion
// during in-flight LLM calls.
func (g *Guardian) SetRequestGuard(guard func() func()) {
	g.config.RequestGuard = guard
}

// SetObservabilityDeps wires observability dependencies after construction.
// Called in Phase 3, after activation and daemon controllers are available.
// Nil arguments are silently ignored.
func (g *Guardian) SetObservabilityDeps(ac ActivationQuerier, am ActivationMetricsQuerier, dc DaemonQuerier) {
	if ac != nil {
		g.config.ActivationController = ac
	}
	if am != nil {
		g.config.ActivationMetrics = am
	}
	if dc != nil {
		g.config.DaemonController = dc
	}
}

// SetGitSubsystems wires git dependencies after construction. Called in
// Phase 3 (initial boot — git goroutine just completed) or from the factory
// closure on daemon restart (gitSubsRef already populated).
func (g *Guardian) SetGitSubsystems(gitBus *git.GitBus, watcher *git.StatusWatcher) {
	if gitBus == nil {
		return
	}
	g.gitObserver = NewGitObserver(gitBus, g.config.ProtectedBranches, g.activityPub, g.requestApproval)
	g.gitObserver.SetOnEvent(func(evt agentlog.EventType, level string, payload any) {
		shared.LogAgentEvent(g.steering.EventLogger(), evt, g.id, "", "", level, payload)
	})
	g.gitObserver.Start()

	if watcher != nil {
		g.checkpointMgr = NewCheckpointManager(
			gitBus,
			watcher,
			g.config.CheckpointInterval,
			g.config.DirtyThreshold,
			g.activityPub,
			g.requestApproval,
		)
		g.checkpointMgr.SetOnEvent(func(evt agentlog.EventType, level string, payload any) {
			shared.LogAgentEvent(g.steering.EventLogger(), evt, g.id, "", "", level, payload)
		})
		g.checkpointMgr.Start(g.runCtx)
	}
}

// SetExtendedObservabilityDeps wires pipeline, gateway, knowledge, and
// concurrency queriers after construction. Nil arguments are ignored.
func (g *Guardian) SetExtendedObservabilityDeps(pq PipelineQuerier, gq GatewayQuerier, kq KnowledgeQuerier, cq ConcurrencyQuerier) {
	if pq != nil {
		g.config.Pipeline = pq
	}
	if gq != nil {
		g.config.Gateway = gq
	}
	if kq != nil {
		g.config.Knowledge = kq
	}
	if cq != nil {
		g.config.Concurrency = cq
	}
}

// SetVFSDeps wires VFS observability dependencies after construction.
// Called in Phase 3 or from the factory closure on daemon restart.
func (g *Guardian) SetVFSDeps(cvs CVSQuerier, vfs VFSManagerQuerier) {
	if cvs != nil {
		g.config.CVS = cvs
	}
	if vfs != nil {
		g.config.VFSManager = vfs
	}
}

// SetCostCalculator wires the cost calculator on the health monitor,
// enabling per-call cost accumulation from LLM activity events.
func (g *Guardian) SetCostCalculator(fn CostCalculator) {
	g.healthMon.SetCostCalculator(fn)
}

// SetQuarantine wires the quarantine buffer for external content inspection.
// Called during pipeline setup after construction.

func (g *Guardian) SetQuarantine(q *fetch.QuarantineBuffer) {
	g.quarantine = q
}

func (g *Guardian) SetCommandApprovalStore(store *commandapproval.RuleStore) {
	if g == nil || store == nil {
		return
	}
	g.commandRules = store
}

// ---------------------------------------------------------------------------
// Lifecycle: Start / Stop
// ---------------------------------------------------------------------------

func (g *Guardian) Start(bus guide.EventBus) error {
	g.runMu.Lock()
	if g.running {
		g.runMu.Unlock()
		return fmt.Errorf("guardian is already running")
	}
	g.running = true
	g.runMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	g.runCtx = ctx
	g.runCancel = cancel

	g.bus = bus
	g.channels = guide.NewAgentChannels("guardian", g.id)

	var err error
	g.requestSub, err = bus.SubscribeAsync(g.channels.Requests, g.handleBusRequest)
	if err != nil {
		cancel()
		g.setNotRunning()
		return fmt.Errorf("guardian: subscribe requests: %w", err)
	}

	g.responseSub, err = bus.SubscribeAsync(g.channels.Responses, g.handleBusResponse)
	if err != nil {
		g.requestSub.Unsubscribe()
		cancel()
		g.setNotRunning()
		return fmt.Errorf("guardian: subscribe responses: %w", err)
	}

	g.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, g.handleRegistryAnnouncement)
	if err != nil {
		g.requestSub.Unsubscribe()
		g.responseSub.Unsubscribe()
		cancel()
		g.setNotRunning()
		return fmt.Errorf("guardian: subscribe registry: %w", err)
	}

	g.activitySub, err = bus.SubscribeAsync(guide.TopicActivity, g.handleActivityEvent)
	if err != nil {
		g.requestSub.Unsubscribe()
		g.responseSub.Unsubscribe()
		g.registrySub.Unsubscribe()
		cancel()
		g.setNotRunning()
		return fmt.Errorf("guardian: subscribe activity: %w", err)
	}

	// Start git subsystems if GitBus is available.
	if g.config.GitBus != nil {
		g.gitObserver = NewGitObserver(g.config.GitBus, g.config.ProtectedBranches, g.activityPub, g.requestApproval)
		g.gitObserver.SetOnEvent(func(evt agentlog.EventType, level string, payload any) {
			shared.LogAgentEvent(g.steering.EventLogger(), evt, g.id, "", "", level, payload)
		})
		g.gitObserver.Start()

		if g.config.GitWatcher != nil {
			g.checkpointMgr = NewCheckpointManager(
				g.config.GitBus,
				g.config.GitWatcher,
				g.config.CheckpointInterval,
				g.config.DirtyThreshold,
				g.activityPub,
				g.requestApproval,
			)
			g.checkpointMgr.SetOnEvent(func(evt agentlog.EventType, level string, payload any) {
				shared.LogAgentEvent(g.steering.EventLogger(), evt, g.id, "", "", level, payload)
			})
			g.checkpointMgr.Start(ctx)
		}
	}

	// Start health monitor.
	g.healthMon.Start(ctx)

	g.publishActivityWithVisibility(events.EventTypeAgentAction, events.VisibilitySystem, "Guardian agent started")
	g.logger.Info("guardian started", "id", g.id)
	return nil
}

func (g *Guardian) Stop() error {
	g.runMu.Lock()
	if !g.running {
		g.runMu.Unlock()
		return nil
	}
	g.running = false
	g.runMu.Unlock()

	g.steering.CloseAll()
	g.inFlightMu.Lock()
	inFlightCount := len(g.inFlight)
	g.inFlightMu.Unlock()
	g.logger.Info("guardian: Stop() called", "in_flight_count", inFlightCount)

	// Stop subsystems.
	if g.checkpointMgr != nil {
		g.checkpointMgr.Stop()
	}
	if g.gitObserver != nil {
		g.gitObserver.Stop()
	}
	g.healthMon.Stop()

	// Unsubscribe.
	if g.activitySub != nil {
		g.activitySub.Unsubscribe()
	}
	if g.registrySub != nil {
		g.registrySub.Unsubscribe()
	}
	if g.responseSub != nil {
		g.responseSub.Unsubscribe()
	}
	if g.requestSub != nil {
		g.requestSub.Unsubscribe()
	}

	// Cancel run context.
	if g.runCancel != nil {
		g.runCancel()
	}

	// Drain pending approvals.
	g.pendingMu.Lock()
	for id, ch := range g.pendingApprovals {
		select {
		case ch <- ApprovalResult{Approved: false, Reason: "guardian stopped"}:
		default:
		}
		delete(g.pendingApprovals, id)
	}
	g.pendingMu.Unlock()

	if g.tools != nil {
		g.tools.Close()
		g.tools = nil
	}

	g.logger.Info("guardian stopped", "id", g.id)
	return nil
}

func (g *Guardian) setNotRunning() {
	g.runMu.Lock()
	g.running = false
	g.runMu.Unlock()
}

func (g *Guardian) isRunning() bool {
	g.runMu.RLock()
	defer g.runMu.RUnlock()
	return g.running
}

// processingContext returns the run context or background if nil.
func (g *Guardian) processingContext() context.Context {
	if g.runCtx != nil {
		return g.runCtx
	}
	return context.Background()
}

// ---------------------------------------------------------------------------
// Bus Handlers
// ---------------------------------------------------------------------------

func (g *Guardian) handleBusRequest(msg *guide.Message) error {
	ctx := g.processingContext()
	if err := ctx.Err(); err != nil {
		return nil
	}
	g.logInfo("handleBusRequest", "msg_type", string(msg.Type), "correlation_id", msg.CorrelationID)
	start := time.Now()
	err := g.dispatchBusRequest(ctx, msg)
	g.logInfo("handleBusRequest: exit", "elapsed", time.Since(start).String(), "err", err)
	return err
}

func (g *Guardian) dispatchBusRequest(ctx context.Context, msg *guide.Message) error {
	if msg == nil {
		return nil
	}
	switch msg.Type {
	case guide.MessageTypeForward:
		return g.handleForwardBusRequest(ctx, msg)
	case guide.MessageTypeAction:
		return g.handleActionBusRequest(ctx, msg)
	default:
		g.logInfo("dispatchBusRequest: unhandled type", "msg_type", string(msg.Type))
		return nil
	}
}

func (g *Guardian) handleForwardBusRequest(ctx context.Context, msg *guide.Message) error {
	if !g.requestSerializer.Acquire(ctx) {
		return nil // parent context done, agent shutting down
	}
	defer g.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	g.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	shared.LogIncomingRequest(g.steering.EventLogger(), fwd, g.id)

	if g.config.RequestGuard != nil {
		release := g.config.RequestGuard()
		defer release()
	}

	startTime := time.Now()
	reqCtx, cancel := context.WithCancel(ctx)
	reqCtx = versioning.WithSessionID(reqCtx, fwd.SessionID)
	reqCtx = shared.WithForwardedTaskScope(reqCtx, fwd.Metadata)
	reqCtx = shared.WithForwardedStreamContext(reqCtx, fwd.CorrelationID, fwd.SourceAgentID, fwd.ParentCorrelationID, fwd.Metadata)
	reqCtx = shared.WithOwnedStreamIdentity(reqCtx, "guardian", "Guardian")
	g.registerInFlight(fwd.CorrelationID, cancel)
	g.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer g.clearInFlight(fwd.CorrelationID)
	defer cancel()

	// Create steering ledger for this request.
	ledger := g.steering.Create(fwd.CorrelationID, g.id, fwd.SessionID, g.activityPub, nil)
	defer g.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)
	reqCtx = shared.WithSteeringLedger(reqCtx, ledger)
	reqCtx = shared.WithLogMeta(reqCtx, shared.LogMeta{
		EventLogger: g.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     g.id,
		SessionID:   fwd.SessionID,
	})
	if g.factory != nil && g.identity != nil {
		task, taskErr := g.factory.NewTask(identity.TaskOptions{
			DisplayID:   fwd.CorrelationID,
			Correlation: identity.CorrelationID(fwd.CorrelationID),
		})
		if taskErr != nil {
			return fmt.Errorf("guardian: mint task: %w", taskErr)
		}
		reqCtx = identity.WithIdentity(reqCtx, g.identity)
		reqCtx = identity.WithTask(reqCtx, task)
	}
	allowedHandoff := shared.AutomaticHandoffAllowedForForwardedRequest(fwd)
	reqCtx = shared.WithAutomaticHandoffEnabled(reqCtx, allowedHandoff)
	reqCtx = handoff.WithTransportRetryHandoff(reqCtx, handoff.TransportRetryHandoffConfig{
		Enabled:       allowedHandoff,
		Bridge:        shared.EffectiveHandoffBridge(reqCtx, g.handoffBridge),
		AgentID:       g.id,
		AgentType:     "guardian",
		UserRequest:   fwd.Input,
		CorrelationID: fwd.CorrelationID,
		SessionID:     fwd.SessionID,
		EventLogger:   g.steering.EventLogger(),
		Scribe:        g.agentPod,
	})
	gov := shared.NewContextGovernor(DefaultGuardianModel, DefaultMaxOutputTokens, 0)
	if g.handoffBridge != nil && shared.AutomaticHandoffEnabled(reqCtx) {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			bridge := shared.EffectiveHandoffBridge(bctx, g.handoffBridge)
			if bridge == nil {
				return shared.ErrContextBudgetExhausted
			}
			return bridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	reqCtx = shared.WithContextGovernor(reqCtx, gov)
	reqCtx = shared.WithProgressPublisher(reqCtx, &shared.ProgressPublisher{
		Bus: g.bus, Channels: g.channels,
		AgentID: g.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})
	reqCtx = shared.WithToolCallEmitter(reqCtx, shared.NewToolCallEmitter(
		g.bus, g.channels, g.id, fwd.CorrelationID, fwd.SourceAgentID,
	))

	skillName := guardianDirectSkillName(fwd)
	publishStream := guardianDirectSkillPublishesStream(skillName)
	if publishStream {
		g.publishStreamStart(reqCtx, fwd.CorrelationID)
	}

	// Execute either a deterministic direct skill or the conversation loop.
	var (
		result     any
		streamText string
		usage      *guide.StreamUsage
		err        error
	)
	if skillName != "" {
		reqCtx = withGuardianDirectSourceAgentID(reqCtx, fwd.SourceAgentID)
		result, streamText, err = g.executeDirectSkill(reqCtx, skillName, fwd.Input)
	} else {
		var conversationResult *ConversationResult
		conversationResult, err = g.executeConversation(reqCtx, fwd)
		if conversationResult != nil {
			result = conversationResult
			streamText = conversationResult.Response
			usage = conversationResult.Usage
		}
	}
	shared.LogResponse(g.steering.EventLogger(), fwd.CorrelationID, g.id, fwd.SessionID, time.Since(startTime), err)

	// Build response.
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   g.id,
		RespondingAgentName: "guardian",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		if lm := shared.LogMetaFromContext(reqCtx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		resp.Error = err.Error()
		if publishStream {
			g.publishStreamComplete(reqCtx, fwd.CorrelationID, resp.Error, nil)
		}
	} else {
		resp.Data = result
		if publishStream {
			g.publishStreamComplete(reqCtx, fwd.CorrelationID, streamText, usage)
		}
	}

	if g.agentPod != nil {
		g.agentPod.FeedScribe("guardian", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}
	return g.bus.Publish(g.channels.Responses, newResponseMessage(fwd.CorrelationID, g.id, resp))
}

func guardianDirectSkillName(fwd *guide.ForwardedRequest) string {
	if fwd == nil || len(fwd.Metadata) == 0 {
		return ""
	}
	name, _ := fwd.Metadata["direct_skill"].(string)
	return strings.TrimSpace(name)
}

func (g *Guardian) executeDirectSkill(ctx context.Context, name string, input string) (any, string, error) {
	result := g.InvokeSkill(ctx, name, json.RawMessage(input))
	if result == nil {
		return nil, "", fmt.Errorf("guardian direct skill %q returned no result", name)
	}
	if !result.Success {
		return nil, "", fmt.Errorf("guardian direct skill %q: %s", name, result.Error)
	}
	return result.Data, formatGuardianDirectSkillOutput(result.Data), nil
}

func guardianDirectSkillPublishesStream(name string) bool {
	return true
}

func formatGuardianDirectSkillOutput(data any) string {
	if data == nil {
		return ""
	}
	switch typed := data.(type) {
	case commandapproval.Evaluation:
		return formatGuardianApprovalEvaluation(typed)
	case *commandapproval.Evaluation:
		if typed != nil {
			return formatGuardianApprovalEvaluation(*typed)
		}
	case toolruntime.GuardianControlGrant:
		return formatGuardianControlGrant(typed)
	case *toolruntime.GuardianControlGrant:
		if typed != nil {
			return formatGuardianControlGrant(*typed)
		}
	}
	if payload, ok := data.(map[string]any); ok {
		if text := formatGuardianDirectSkillOutputMap(payload); strings.TrimSpace(text) != "" {
			return text
		}
		if message, _ := payload["user_message"].(string); strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Sprintf("%v", data)
	}
	return string(bytes)
}

func formatGuardianApprovalEvaluation(eval commandapproval.Evaluation) string {
	prefix := "Command approval"
	if strings.EqualFold(strings.TrimSpace(eval.Analysis.Program), "fetch") ||
		strings.HasPrefix(strings.TrimSpace(eval.Analysis.TemplateKey), "fetch|") {
		prefix = "Fetch approval"
	}
	switch eval.Decision {
	case commandapproval.DecisionAllow:
		return prefix + " allowed"
	case commandapproval.DecisionDeny:
		return prefix + " blocked"
	default:
		return "Validating " + strings.ToLower(prefix) + " request"
	}
}

func formatGuardianControlGrant(grant toolruntime.GuardianControlGrant) string {
	if grant.Approved {
		return "Tool execution allowed"
	}
	return "Tool execution blocked"
}

func formatGuardianDirectSkillOutputMap(payload map[string]any) string {
	decision, _ := payload["decision"].(string)
	decision = strings.TrimSpace(strings.ToLower(decision))
	if decision != "" {
		prefix := "Command approval"
		if analysis, _ := payload["analysis"].(map[string]any); analysis != nil {
			program, _ := analysis["program"].(string)
			templateKey, _ := analysis["template_key"].(string)
			if strings.EqualFold(strings.TrimSpace(program), "fetch") ||
				strings.HasPrefix(strings.TrimSpace(templateKey), "fetch|") {
				prefix = "Fetch approval"
			}
		}
		switch decision {
		case string(commandapproval.DecisionAllow):
			return prefix + " allowed"
		case string(commandapproval.DecisionDeny):
			return prefix + " blocked"
		default:
			return "Validating " + strings.ToLower(prefix) + " request"
		}
	}
	if approved, ok := payload["approved"].(bool); ok {
		if approved {
			return "Tool execution allowed"
		}
		return "Tool execution blocked"
	}
	return ""
}

func (g *Guardian) handleActionBusRequest(ctx context.Context, msg *guide.Message) error {
	g.logInfo("handleActionBusRequest", "correlation_id", msg.CorrelationID)
	action, ok := msg.GetActionRequest()
	if !ok || action == nil {
		return nil
	}
	g.logDebug("handleActionBusRequest: action received",
		"action", action.Action,
		"correlation_id", action.CorrelationID,
		"source_agent", action.SourceAgentID,
		"target_agent", action.TargetAgentID)
	if action.Action == "tool_execution_control" {
		return g.handleToolExecutionControlAction(ctx, msg, action)
	}
	g.steering.HandleAction(action)
	return nil
}

func (g *Guardian) handleToolExecutionControlAction(
	_ context.Context,
	msg *guide.Message,
	action *guide.ActionRequest,
) error {
	if action == nil {
		return nil
	}
	req, ok := action.Data.(*toolruntime.GuardianControlRequest)
	if !ok || req == nil {
		return g.publishToolControlReply(msg, action, nil, fmt.Errorf("invalid guardian tool control request payload"))
	}
	grant, err := g.evaluateToolExecutionControl(action.SourceAgentID, req)
	return g.publishToolControlReply(msg, action, grant, err)
}

func (g *Guardian) publishToolControlReply(
	msg *guide.Message,
	action *guide.ActionRequest,
	grant *toolruntime.GuardianControlGrant,
	err error,
) error {
	if g.bus == nil || msg == nil || msg.ReplyTo == nil || strings.TrimSpace(msg.ReplyTo.Topic) == "" {
		return err
	}
	resp := &guide.RouteResponse{
		CorrelationID:       action.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   g.id,
		RespondingAgentName: "guardian",
		ProcessingTime:      0,
		Data:                grant,
	}
	if err != nil {
		resp.Error = err.Error()
	}
	reply := guide.NewResponseMessage(uuid.New().String(), resp)
	reply.CorrelationID = action.CorrelationID
	reply.SourceAgentID = g.id
	return g.bus.Publish(msg.ReplyTo.Topic, reply)
}

func (g *Guardian) handleBusResponse(msg *guide.Message) error {
	if msg == nil {
		return nil
	}
	// Route approval responses to pending channels.
	g.pendingMu.Lock()
	ch, ok := g.pendingApprovals[msg.CorrelationID]
	g.pendingMu.Unlock()

	if ok {
		result := ApprovalResult{Approved: true}
		if payload, pOk := msg.Payload.(map[string]any); pOk {
			if approved, aOk := payload["approved"].(bool); aOk {
				result.Approved = approved
			}
			if decision, dOk := payload["decision"].(string); dOk {
				result.Decision = ApprovalDecision(strings.TrimSpace(decision))
			}
			if reason, rOk := payload["reason"].(string); rOk {
				result.Reason = reason
			}
		}
		select {
		case ch <- result:
		default:
		}
	}
	return nil
}

func (g *Guardian) handleRegistryAnnouncement(msg *guide.Message) error {
	if msg == nil {
		return nil
	}
	ann, ok := msg.Payload.(*guide.AgentAnnouncement)
	if !ok {
		return nil
	}
	g.knownMu.Lock()
	existing, exists := g.knownAgents[ann.AgentID]
	if exists {
		// Merge: never regress Ready from true→false, and preserve
		// AgentType when the incoming announcement omits it.
		if existing.Ready && !ann.Ready {
			ann.Ready = existing.Ready
			ann.ReadyAt = existing.ReadyAt
		}
		if ann.AgentType == "" && existing.AgentType != "" {
			ann.AgentType = existing.AgentType
		}
	}
	g.knownAgents[ann.AgentID] = ann
	g.knownMu.Unlock()

	action := "registered"
	if ann.Ready {
		action = "ready"
	}
	shared.LogAgentEvent(g.steering.EventLogger(), agentlog.EventRegistryEvent,
		g.id, "", "", "info", &agentlog.RegistryPayload{
			AgentID: ann.AgentID, AgentType: ann.AgentType, Action: action,
		})
	return nil
}

// SeedKnownAgents populates the registry from a snapshot of already-registered
// agents. Call this after Phase 3 wiring (or after daemon restart) so the
// Guardian doesn't start with an empty registry waiting for live announcements.
func (g *Guardian) SeedKnownAgents(agents []*guide.AgentAnnouncement) {
	g.knownMu.Lock()
	for _, ann := range agents {
		if ann == nil {
			continue
		}
		// Only seed if we don't already have a newer entry from a live event.
		if _, exists := g.knownAgents[ann.AgentID]; !exists {
			g.knownAgents[ann.AgentID] = ann
		}
	}
	g.knownMu.Unlock()
}

func (g *Guardian) handleActivityEvent(msg *guide.Message) error {
	if msg == nil {
		return nil
	}
	evt, ok := msg.Payload.(*events.ActivityEvent)
	if !ok {
		return nil
	}
	// Track token usage for budget monitoring. Only process response events
	// to avoid double-counting (request events carry estimated input tokens
	// that the response event supersedes with actuals).
	if evt.EventType == events.EventTypeLLMResponse {
		g.healthMon.RecordTokenUsage(evt)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Approval Flow
// ---------------------------------------------------------------------------

// requestApproval sends a proposal to the user and blocks until approved/denied/timeout.
func (g *Guardian) requestApproval(ctx context.Context, proposal *GitMutationProposal) (bool, error) {
	result, err := g.requestApprovalDetailed(ctx, proposal)
	return result.Approved, err
}

func (g *Guardian) requestApprovalDetailed(ctx context.Context, proposal *GitMutationProposal) (ApprovalResult, error) {
	correlationID := uuid.New().String()
	proposal.CorrelationID = correlationID

	ch := make(chan ApprovalResult, 1)
	g.pendingMu.Lock()
	g.pendingApprovals[correlationID] = ch
	g.pendingMu.Unlock()

	defer func() {
		g.pendingMu.Lock()
		delete(g.pendingApprovals, correlationID)
		g.pendingMu.Unlock()
	}()

	// Publish proposal to Guide.
	shared.LogAgentEvent(g.steering.EventLogger(), agentlog.EventApprovalRequested,
		g.id, "", correlationID, "info", &agentlog.DiffPayload{
			Verdict: "pending", Reason: proposal.Reason,
		})
	if err := g.bus.Publish(guide.TopicGuideRequests, newProposalMessage(correlationID, proposal)); err != nil {
		return ApprovalResult{}, fmt.Errorf("publish proposal: %w", err)
	}

	var approval ApprovalResult
	err := shared.RunWithContextLease(ctx, shared.ContextLeaseConfig{
		AttemptTimeout: DefaultApprovalTTL,
		MaxRefreshes:   shared.DefaultConsultationLeaseRefreshes,
		OnRefresh: func(info shared.ContextLeaseRefresh) {
			if g.logger != nil {
				g.logger.Info("approval wait lease refreshed",
					"correlation_id", correlationID,
					"refresh_count", info.RefreshCount,
					"attempt_timeout", info.AttemptTimeout.String(),
					"error", info.Error)
			}
		},
	}, func(waitCtx context.Context) error {
		select {
		case approval = <-ch:
			return nil
		case <-waitCtx.Done():
			return waitCtx.Err()
		}
	})
	if err != nil {
		verdict := "timeout"
		level := "warn"
		if ctx.Err() != nil {
			verdict = "cancelled"
		}
		shared.LogAgentEvent(g.steering.EventLogger(), agentlog.EventApprovalReceived,
			g.id, "", correlationID, level, &agentlog.DiffPayload{
				Verdict: verdict,
			})
		return ApprovalResult{}, shared.WrapLeaseTimeoutError("approval", DefaultApprovalTTL, err)
	}
	action := "approved"
	if !approval.Approved {
		action = "denied"
	}
	shared.LogAgentEvent(g.steering.EventLogger(), agentlog.EventApprovalReceived,
		g.id, "", correlationID, "info", &agentlog.DiffPayload{
			Verdict: action, Reason: approval.Reason,
		})
	return approval, nil
}

// ---------------------------------------------------------------------------
// In-flight tracking
// ---------------------------------------------------------------------------

func (g *Guardian) registerInFlight(correlationID string, cancel context.CancelFunc) {
	g.inFlightMu.Lock()
	g.inFlight[correlationID] = cancel
	g.inFlightMu.Unlock()
}

func (g *Guardian) clearInFlight(correlationID string) {
	g.inFlightMu.Lock()
	delete(g.inFlight, correlationID)
	g.inFlightMu.Unlock()
}

// ---------------------------------------------------------------------------
// Activity publishing
// ---------------------------------------------------------------------------

func (g *Guardian) publishActivity(eventType events.EventType, content string) {
	g.publishActivityStateWithVisibility(context.Background(), eventType, events.VisibilityUser, content, events.AgentUIStateNone)
}

func (g *Guardian) publishActivityWithVisibility(eventType events.EventType, visibility events.EventVisibility, content string) {
	g.publishActivityStateWithVisibility(context.Background(), eventType, visibility, content, events.AgentUIStateNone)
}

func (g *Guardian) publishActivityState(ctx context.Context, eventType events.EventType, content string, state events.AgentUIState) {
	g.publishActivityStateWithVisibility(ctx, eventType, events.VisibilityUser, content, state)
}

func (g *Guardian) publishActivityStateWithVisibility(
	ctx context.Context,
	eventType events.EventType,
	visibility events.EventVisibility,
	content string,
	state events.AgentUIState,
) {
	if g.activityPub == nil {
		return
	}
	logMeta := shared.LogMetaFromContext(ctx)
	sessionID := strings.TrimSpace(logMeta.SessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	evt := events.NewActivityEvent(eventType, sessionID, content)
	evt.AgentID = g.AgentID()
	evt.CorrelationID = strings.TrimSpace(logMeta.CorrID)
	evt.Visibility = visibility
	evt.Data["agent_type"] = "guardian"
	evt.Data["agent_name"] = "Guardian"
	events.SetAgentUIState(evt, state)
	g.activityPub.PublishActivity(evt)
}

// ---------------------------------------------------------------------------
// Logging helpers
// ---------------------------------------------------------------------------

func (g *Guardian) logInfo(msg string, args ...any) {
	if g != nil && g.logger != nil {
		g.logger.Info(msg, args...)
	}
}

func (g *Guardian) logWarn(msg string, args ...any) {
	if g != nil && g.logger != nil {
		g.logger.Warn(msg, args...)
	}
}

func (g *Guardian) logDebug(msg string, args ...any) {
	guardianDebugLog().Debug(msg, args...)
}

// ---------------------------------------------------------------------------
// Tool Loop Support
// ---------------------------------------------------------------------------

// executeToolCall invokes a scoped tool via the shared runtime kernel while
// preserving Guardian's outer-loop ownership.
func (g *Guardian) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	return g.toolRuntime().Execute(ctx, toolruntime.Invocation{
		ToolCall:        call,
		AgentID:         g.toolRuntime().AgentID(),
		CorrelationID:   shared.LogMetaFromContext(ctx).CorrID,
		CapabilityScope: g.toolRuntime().CapabilityScope(),
	})
}

// InvokeSkill executes a skill with hook enforcement.
func (g *Guardian) InvokeSkill(ctx context.Context, name string, input json.RawMessage) *skills.Result {
	if g.toolRuntime() == nil {
		return &skills.Result{SkillName: name, Success: false, Error: "tool runtime is not configured"}
	}
	if _, err := g.toolRuntime().Activate(name); err != nil {
		return &skills.Result{SkillName: name, Success: false, Error: err.Error()}
	}
	raw := strings.TrimSpace(string(input))
	if raw == "" {
		raw = "{}"
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = "invoke_" + uuid.NewString()
	}
	call := providers.ToolCall{
		ID:        "invoke_" + uuid.NewString(),
		Name:      name,
		Arguments: raw,
	}
	execCtx := shared.WithActiveToolCall(ctx, call)
	var execResult toolruntime.RawExecutionResult
	_, err := shared.TimedToolCall(execCtx, "guardian", call, func() (string, error) {
		var execErr error
		execResult, execErr = g.toolRuntime().ExecuteRaw(execCtx, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         g.toolRuntime().AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: g.toolRuntime().CapabilityScope(),
		})
		if execErr != nil {
			return "", execErr
		}
		return formatGuardianDirectSkillOutput(execResult.Data), nil
	})
	if err != nil {
		return &skills.Result{SkillName: name, Success: false, Error: err.Error()}
	}
	return &skills.Result{SkillName: name, Success: true, Data: execResult.Data}
}

func (g *Guardian) runPreToolHooks(ctx context.Context, name string, input json.RawMessage) error {
	if g.hooks == nil {
		return nil
	}
	hookData := &skills.ToolCallHookData{
		ToolName: name,
		AgentID:  "guardian",
		Input:    map[string]any{"raw": string(input)},
	}
	_, result, err := g.hooks.ExecutePreToolCallHooks(ctx, hookData)
	if err != nil {
		return err
	}
	if !result.Continue {
		if result.Error != nil {
			return result.Error
		}
		return fmt.Errorf("tool call blocked: %s", name)
	}
	return nil
}

func (g *Guardian) runPostToolHooks(ctx context.Context, name string, input json.RawMessage, result *skills.Result) {
	if g.hooks == nil {
		return
	}
	hookData := &skills.ToolCallHookData{
		ToolName: name,
		AgentID:  "guardian",
		Input:    map[string]any{"raw": string(input)},
	}
	if result != nil {
		hookData.Output = result.Data
		if !result.Success {
			hookData.Error = errors.New(result.Error)
		}
	}
	_, _, _ = g.hooks.ExecutePostToolCallHooks(ctx, hookData)
}

func (g *Guardian) ensureSkillLoaded(name string) {
	if g.skills == nil {
		return
	}
	_ = g.skills.Load(name)
}

// buildToolDefinitions converts loaded skills to provider Tool format.
func (g *Guardian) buildToolDefinitions() []providers.Tool {
	g.toolRuntime().SyncActiveFromLoaded()
	return g.toolRuntime().BuildToolDefinitions()
}

func (g *Guardian) toolRuntime() *toolruntime.Runtime {
	return g.tools
}

// prepareSkillsForInput progressively loads skills relevant to the user's
// input and optimizes to stay within the tool-definition token budget.
// Pinned skills are always loaded; remaining skills are loaded by keyword
// match and demand-paged by the shared tool runtime when the LLM invokes them.
func (g *Guardian) prepareSkillsForInput(input string, intent GuardianIntent) {
	if g.skills == nil {
		return
	}
	for _, name := range guardianPinnedSkillNames() {
		g.skills.Load(name)
	}
	if g.skillLoader != nil {
		g.skillLoader.LoadForInput(input)
		g.skillLoader.OptimizeForBudget()
	}
	for _, name := range guardianIntentSkillNames(intent) {
		g.skills.Load(name)
	}
	if g.tools != nil {
		g.tools.SyncActiveFromLoaded()
	}
}

func guardianIntentSkillNames(intent GuardianIntent) []string {
	switch intent {
	case IntentAssess:
		return []string{
			"content_scan",
			"review_gate",
			"git_safety",
			"quarantine_status",
			"read_file",
			"glob",
			"grep",
		}
	case IntentReport:
		return []string{
			"agent_health",
			"agent_logs",
			"system_status",
			"pipeline_status",
			"usage_breakdown",
			"quarantine_status",
		}
	case IntentCheckpoint:
		return []string{
			"git_safety",
			"rollback",
		}
	default:
		return nil
	}
}

// Skills returns the guardian's skill registry.
func (g *Guardian) Skills() *skills.Registry { return g.skills }
