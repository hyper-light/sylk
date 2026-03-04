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
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/skills"
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
}

// Guardian is the sidecar safety agent for the Sylk system.
// It observes all agent activity, gates mutating git operations behind
// explicit user approval, validates content for injection/credential leaks,
// and monitors agent health. Guardian has NO filesystem write access.
type Guardian struct {
	id     string
	config Config
	logger *slog.Logger

	// LLM provider — GPT-5.3 Codex for on-demand escalation.
	// Deterministic by default; LLM invoked only for ambiguous cases.
	provider        guardianProvider
	providerWrapper gateway.ProviderWrapper
	providerMu      sync.RWMutex
	model           string

	// Skills system (Architect pattern).
	skills        *skills.Registry
	skillLoader   *skills.Loader
	hooks         *skills.HookRegistry
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
	healthMon        *HealthMonitor
	diffGate         *DiffGate

	// File access — always read-only.
	fileAccess versioning.FileAccess

	// User approval flow.
	pendingMu        sync.Mutex
	pendingApprovals map[string]chan ApprovalResult

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
		guardianDebugLogger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
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
		id:               guardianID,
		config:           cfg,
		logger:           cfg.Logger,
		provider:         provider,
		model:            DefaultGuardianModel,
		activityPub:      cfg.ActivityPub,
		fileAccess:       cfg.FileAccess,
		knownAgents:      make(map[string]*guide.AgentAnnouncement),
		pendingApprovals: make(map[string]chan ApprovalResult),
		inFlight:          make(map[string]context.CancelFunc),
		steering:          shared.NewSteeringManager(),
		requestSerializer: shared.NewRequestSerializer(),
	}

	g.steering.InitLazy("guardian", cfg.ActivityPub)

	g.initSkills()
	g.initSubsystems()

	return g, nil
}

func (g *Guardian) initSkills() {
	g.skills = skills.NewRegistry()
	g.hooks = skills.NewHookRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = guardianCoreSkillNames()
	loaderCfg.AutoLoadDomains = []string{"safety", "validation", "health", "gate", "observability", "system"}
	g.skillLoader = skills.NewLoader(g.skills, loaderCfg)

	g.registerCoreSkills()
	registerGuardianSafetyHook(g.hooks, g.skills, guardianAllSkillNames())
}

func (g *Guardian) initSubsystems() {
	g.contentValidator = NewContentValidator(g.config.Sanitizer, g.config.InjectionScanEnabled)
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

func (g *Guardian) Terminate(ctx context.Context) error {
	return g.Stop()
}

// ---------------------------------------------------------------------------
// Descriptor
// ---------------------------------------------------------------------------

func (g *Guardian) Descriptor() handoff.AgentDescriptor {
	return handoff.AgentDescriptor{
		AgentType:     "guardian",
		ModelID:       g.CurrentModel(),
		ContextWindow: 8192,
		Category:      handoff.CategoryStandalone,
	}
}

// SetAgentPod injects the agent pod for Scribe feed integration.
func (g *Guardian) SetAgentPod(pod *shared.AgentPod) {
	g.agentPod = pod
}

// SetHandoffBridge stores the bridge for handoff context tracking.
func (g *Guardian) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	g.handoffBridge = bridge
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

// SetProviderWrapper stores a callback for gateway rate-limiting on refresh.
func (g *Guardian) SetProviderWrapper(w gateway.ProviderWrapper) {
	g.providerMu.Lock()
	defer g.providerMu.Unlock()
	g.providerWrapper = w
}

func (g *Guardian) getProvider() guardianProvider {
	g.providerMu.RLock()
	defer g.providerMu.RUnlock()
	return g.provider
}

// ---------------------------------------------------------------------------
// AuthRefreshable
// ---------------------------------------------------------------------------

func (g *Guardian) ProviderType() string { return "openai" }

func (g *Guardian) RefreshProvider(ctx context.Context, authMethod string) error {
	cfg := providers.OpenAIConfig{
		BaseConfig: providers.BaseConfig{
			Model:     g.CurrentModel(),
			MaxTokens: DefaultMaxOutputTokens,
		},
		ReasoningEffort: "high",
		AuthMode:        authMethod,
	}
	p, err := providers.NewOpenAIProvider(ctx, cfg)
	if err != nil {
		return fmt.Errorf("guardian refresh provider: %w", err)
	}
	g.providerMu.RLock()
	wrapper := g.providerWrapper
	g.providerMu.RUnlock()

	var wrapped guardianProvider = p
	if wrapper != nil {
		wrapped = wrapper(p)
	}
	g.SetProvider(wrapped)
	g.logger.Info("provider refreshed", "auth_method", authMethod)
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
		{ID: "gpt-5.3-codex", DisplayName: "GPT-5.3 Codex"},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
	}
}

// ---------------------------------------------------------------------------
// Deferred Git Wiring
// ---------------------------------------------------------------------------

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

	g.publishActivity(events.EventTypeAgentAction, "Guardian agent started")
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
	g.registerInFlight(fwd.CorrelationID, cancel)
	g.steering.RegisterCancel(fwd.CorrelationID, cancel)
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
	reqCtx = shared.WithContextGovernor(reqCtx, shared.NewContextGovernor(
		DefaultGuardianModel, DefaultMaxOutputTokens, 0,
	))

	// Publish stream start.
	g.publishStreamStart(reqCtx, fwd.CorrelationID)

	// Execute conversation.
	result, err := g.executeConversation(reqCtx, fwd)
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
		g.publishStreamComplete(reqCtx, fwd.CorrelationID, resp.Error, nil)
	} else {
		resp.Data = result
		text := ""
		if result != nil {
			text = result.Response
		}
		g.publishStreamComplete(reqCtx, fwd.CorrelationID, text, nil)
	}

	if g.agentPod != nil {
		g.agentPod.FeedScribe("guardian", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}
	return g.bus.Publish(g.channels.Responses, newResponseMessage(fwd.CorrelationID, g.id, resp))
}

func (g *Guardian) handleActionBusRequest(_ context.Context, msg *guide.Message) error {
	g.logInfo("handleActionBusRequest", "correlation_id", msg.CorrelationID)
	action, ok := msg.GetActionRequest()
	if !ok || action == nil {
		return nil
	}
	g.steering.HandleAction(action)
	return nil
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
	g.knownAgents[ann.AgentID] = ann
	g.knownMu.Unlock()
	shared.LogAgentEvent(g.steering.EventLogger(), agentlog.EventRegistryEvent,
		g.id, "", "", "info", &agentlog.RegistryPayload{
			AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "registered",
		})
	return nil
}

func (g *Guardian) handleActivityEvent(msg *guide.Message) error {
	if msg == nil {
		return nil
	}
	evt, ok := msg.Payload.(*events.ActivityEvent)
	if !ok {
		return nil
	}
	// Track token usage for budget monitoring.
	if evt.EventType == events.EventTypeLLMRequest || evt.EventType == events.EventTypeLLMResponse {
		g.healthMon.RecordTokenUsage(evt)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Approval Flow
// ---------------------------------------------------------------------------

// requestApproval sends a proposal to the user and blocks until approved/denied/timeout.
func (g *Guardian) requestApproval(ctx context.Context, proposal *GitMutationProposal) (bool, error) {
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
		return false, fmt.Errorf("publish proposal: %w", err)
	}

	// Wait for approval with TTL.
	timer := time.NewTimer(DefaultApprovalTTL)
	defer timer.Stop()

	select {
	case result := <-ch:
		action := "approved"
		if !result.Approved {
			action = "denied"
		}
		shared.LogAgentEvent(g.steering.EventLogger(), agentlog.EventApprovalReceived,
			g.id, "", correlationID, "info", &agentlog.DiffPayload{
				Verdict: action, Reason: result.Reason,
			})
		return result.Approved, nil
	case <-timer.C:
		shared.LogAgentEvent(g.steering.EventLogger(), agentlog.EventApprovalReceived,
			g.id, "", correlationID, "warn", &agentlog.DiffPayload{
				Verdict: "timeout",
			})
		return false, fmt.Errorf("approval timed out after %v", DefaultApprovalTTL)
	case <-ctx.Done():
		shared.LogAgentEvent(g.steering.EventLogger(), agentlog.EventApprovalReceived,
			g.id, "", correlationID, "warn", &agentlog.DiffPayload{
				Verdict: "cancelled",
			})
		return false, ctx.Err()
	}
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
	g.publishActivityWithVisibility(eventType, events.VisibilityUser, content)
}

func (g *Guardian) publishActivityWithVisibility(eventType events.EventType, visibility events.EventVisibility, content string) {
	if g.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, "default", content)
	evt.AgentID = g.AgentID()
	evt.Visibility = visibility
	evt.Data["agent_type"] = "guardian"
	evt.Data["agent_name"] = "Guardian"
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

// executeToolCall invokes a skill by name with JSON arguments.
func (g *Guardian) executeToolCall(ctx context.Context, call providers.ToolCall) (string, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return "", fmt.Errorf("tool name is required")
	}

	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return "", fmt.Errorf("tool arguments for %q are not valid JSON", name)
	}

	result := g.InvokeSkill(ctx, name, json.RawMessage(raw))
	if result == nil {
		return "", fmt.Errorf("tool %q returned nil", name)
	}
	if !result.Success {
		return "", fmt.Errorf("tool %q failed: %s", name, strings.TrimSpace(result.Error))
	}

	return shared.MarshalToolOutput(result.Data)
}

// InvokeSkill executes a skill with hook enforcement.
func (g *Guardian) InvokeSkill(ctx context.Context, name string, input json.RawMessage) *skills.Result {
	g.ensureSkillLoaded(name)
	if err := g.runPreToolHooks(ctx, name, input); err != nil {
		return &skills.Result{SkillName: name, Success: false, Error: err.Error()}
	}
	result := g.skills.Invoke(ctx, name, input)
	g.runPostToolHooks(ctx, name, input, result)
	return result
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
	loaded := g.skills.GetLoaded()
	if len(loaded) == 0 {
		return nil
	}

	tools := make([]providers.Tool, 0, len(loaded))
	for _, skill := range loaded {
		def := skill.ToToolDefinition()
		name, _ := def["name"].(string)
		if name == "" {
			continue
		}
		description, _ := def["description"].(string)
		parameters := shared.CoerceMap(def["input_schema"])
		if len(parameters) == 0 {
			parameters = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		tools = append(tools, providers.Tool{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		})
	}
	return tools
}

// ensureToolLoopSkillsLoaded loads core guardian skills for the LLM tool-call loop.
func (g *Guardian) ensureToolLoopSkillsLoaded() {
	if g.skills == nil {
		return
	}
	for _, name := range guardianCoreSkillNames() {
		g.skills.Load(name)
	}
	if g.skillLoader != nil {
		g.skillLoader.OptimizeForBudget()
	}
}

// Skills returns the guardian's skill registry.
func (g *Guardian) Skills() *skills.Registry { return g.skills }
