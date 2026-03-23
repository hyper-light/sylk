// Package global implements the Global Inspector agent -- a cross-file
// architectural quality auditor that validates DAG layer completions against
// the architect's plan. Uses Claude Opus 4.6 with an LLM tool loop.
package global

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// inspectorProvider is the minimal interface the GlobalInspector needs from its LLM.
// Satisfied by *providers.AnthropicProvider and *gateway.GatewayProvider.
type inspectorProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
	StreamWithHandler(ctx context.Context, req *providers.Request, handler providers.StreamHandler) error
}

// GlobalInspector validates cross-file architectural quality on DAG layer completion.
type GlobalInspector struct {
	id     string
	config shared.GlobalInspectorConfig
	logger *slog.Logger

	// LLM provider (Anthropic Opus 4.6).
	provider  inspectorProvider
	refresher container.ProviderRefresher

	// Tool runner for external analysis tools.
	toolRunner      *shared.ToolRunner
	executionBroker purevfs.ExecutionBroker

	// Skills.
	skills        *skills.Registry
	skillLoader   *skills.Loader
	hooks         *skills.HookRegistry
	tools         *toolruntime.Runtime
	toolDefsDirty bool

	// Bus (standard agent pattern).
	bus           guide.EventBus
	channels      *guide.AgentChannels
	requestSub    guide.Subscription
	responseSub   guide.Subscription
	registrySub   guide.Subscription
	running       bool
	knownAgentsMu sync.RWMutex
	knownAgents   map[string]*guide.AgentAnnouncement

	// Sync RPC (for architect escalation).
	pendingMu  sync.Mutex
	pendingBus map[string]chan *guide.Message

	// Audit state.
	diffStore *AuditDiffStore
	mu        sync.RWMutex

	// Handoff integration.
	handoffBridge *handoff.HandoffBridge

	// Agent pod for Scribe feed.
	agentPod *agentShared.AgentPod

	// File access (injected per-session at runtime).
	fileAccess     versioning.FileAccess
	workspaceViews versioning.WorkspaceViewAccess

	// Request lifecycle.
	runCtx    context.Context
	runCancel context.CancelFunc

	// Steering ledger management.
	steering *agentShared.SteeringManager

	// Request serialization: ensures at most one forwarded request
	// executes at a time, preventing cancel/new-request interleaving.
	requestSerializer *agentShared.RequestSerializer
}

// New creates a new GlobalInspector instance. The provider must implement
// both Complete and StreamWithHandler (satisfied by all gateway-wrapped providers).
func New(cfg shared.GlobalInspectorConfig, provider providers.ProviderAdapter) (*GlobalInspector, error) {
	cfg = applyConfigDefaults(cfg)

	ip, ok := provider.(inspectorProvider)
	if !ok {
		return nil, fmt.Errorf("inspector: provider does not support StreamWithHandler")
	}

	inspectorID := cfg.AgentID
	if inspectorID == "" {
		inspectorID = uuid.New().String()[:8]
	}

	gi := &GlobalInspector{
		id:                inspectorID,
		config:            cfg,
		logger:            slog.Default().With("agent", "inspector"),
		provider:          ip,
		knownAgents:       make(map[string]*guide.AgentAnnouncement),
		pendingBus:        make(map[string]chan *guide.Message),
		diffStore:         NewAuditDiffStore(cfg.MaxAge),
		steering:          agentShared.NewSteeringManager(),
		requestSerializer: agentShared.NewRequestSerializer(),
		executionBroker:   purevfs.DefaultExecutionBroker(),
	}
	gi.toolRunner = shared.NewToolRunner(shared.ToolRunnerConfig{
		WorkingDir:    ".",
		Timeout:       cfg.DefaultTimeout,
		Logger:        slog.Default(),
		AgentID:       inspectorID,
		AgentType:     "inspector",
		SessionID:     func() string { return gi.config.SessionID },
		FileAccess:    func() versioning.FileAccess { return gi.fileAccess },
		Broker:        func() purevfs.ExecutionBroker { return gi.executionBroker },
		RequireBroker: true,
	})

	gi.steering.InitLazy("inspector", cfg.ActivityPub)

	if err := gi.initSkills(); err != nil {
		return nil, err
	}
	return gi, nil
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (gi *GlobalInspector) SetProvider(p inspectorProvider) {
	gi.mu.Lock()
	defer gi.mu.Unlock()
	gi.provider = p
}

// SetProviderRefresher stores a callback that creates a fresh provider for
// the current model and auth method. Set by cmd/tui.go at bootstrap.
func (gi *GlobalInspector) SetProviderRefresher(fn container.ProviderRefresher) {
	gi.mu.Lock()
	defer gi.mu.Unlock()
	gi.refresher = fn
}

// getProvider returns the current provider under read lock.
func (gi *GlobalInspector) getProvider() inspectorProvider {
	gi.mu.RLock()
	defer gi.mu.RUnlock()
	return gi.provider
}

// ProviderType implements container.AuthRefreshable.
func (gi *GlobalInspector) ProviderType() string {
	return string(container.ProviderForModel(gi.CurrentModel()))
}

// RefreshProvider implements container.AuthRefreshable.
func (gi *GlobalInspector) RefreshProvider(ctx context.Context, authMethod string) error {
	gi.mu.RLock()
	fn := gi.refresher
	gi.mu.RUnlock()
	if fn == nil {
		return nil
	}
	p, err := fn(ctx, gi.CurrentModel(), authMethod)
	if err != nil {
		return fmt.Errorf("inspector refresh provider: %w", err)
	}
	ip, ok := p.(inspectorProvider)
	if !ok {
		return fmt.Errorf("inspector refresh provider: provider does not satisfy inspectorProvider")
	}
	gi.SetProvider(ip)
	gi.logger.Info("provider refreshed", "model", gi.CurrentModel(), "auth_method", authMethod)
	return nil
}

// SwapModel implements container.ModelSwappable.
// Re-creates the Anthropic provider with the given model ID, re-applying the
// gateway wrapper. Thread-safe via mu.
func (gi *GlobalInspector) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	ip, ok := provider.(inspectorProvider)
	if !ok {
		return fmt.Errorf("inspector swap model: provider does not satisfy inspectorProvider")
	}
	gi.SetProvider(ip)
	gi.mu.Lock()
	gi.config.Model = modelID
	gi.mu.Unlock()
	gi.logger.Info("model swapped", "model", modelID)
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (gi *GlobalInspector) CurrentModel() string {
	gi.mu.RLock()
	defer gi.mu.RUnlock()
	return gi.config.Model
}

// SupportedModels implements container.ModelSwappable.
func (gi *GlobalInspector) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
	}
}

func applyConfigDefaults(cfg shared.GlobalInspectorConfig) shared.GlobalInspectorConfig {
	defaults := shared.DefaultGlobalInspectorConfig()
	if cfg.Model == "" {
		cfg.Model = defaults.Model
	}
	if cfg.MaxToolRuns == 0 {
		cfg.MaxToolRuns = defaults.MaxToolRuns
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = defaults.MaxTokens
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = defaults.DefaultTimeout
	}
	if cfg.AuditTimeout == 0 {
		cfg.AuditTimeout = defaults.AuditTimeout
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = defaults.MaxAge
	}
	return cfg
}

func (gi *GlobalInspector) initSkills() error {
	gi.skills = skills.NewRegistry()
	gi.hooks = skills.NewHookRegistry()

	gi.registerCoreSkills()
	gi.registerSafetyHook()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = globalInspectorVisibleSkillNames()
	loaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	gi.skillLoader = skills.NewLoader(gi.skills, loaderCfg)
	tools, err := toolruntime.New(toolruntime.Config{
		Registry: gi.skills,
		Hooks:    gi.hooks,
		Manifest: globalInspectorToolManifest(gi.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return fmt.Errorf("initialize global inspector tool runtime: %w", err)
	}
	gi.tools = tools
	gi.tools.SyncActiveFromLoaded()
	return nil
}

func (gi *GlobalInspector) registerSafetyHook() {
	allowed := allowedInspectorGlobalTools(gi.skills)
	gi.hooks.RegisterPreToolCallHook("inspector_global_safety", skills.HookPriorityHigh,
		func(ctx context.Context, data *skills.ToolCallHookData) skills.HookResult {
			if !allowed[data.ToolName] {
				return skills.HookResult{
					Continue: false,
					Error:    fmt.Errorf("tool %q not permitted for global inspector", data.ToolName),
				}
			}
			return skills.HookResult{Continue: true}
		})
}

func allowedInspectorGlobalTools(registry *skills.Registry) map[string]bool {
	allowed := make(map[string]bool)
	for _, name := range globalInspectorToolManifest(registry).AllowedNames() {
		allowed[name] = true
	}
	return allowed
}

// Close shuts down the global inspector.
func (gi *GlobalInspector) Close() error {
	if gi.tools != nil {
		gi.tools.Close()
		gi.tools = nil
	}
	gi.diffStore.Close()
	return gi.Stop()
}

// Start begins listening for messages on the event bus.
func (gi *GlobalInspector) Start(bus guide.EventBus) error {
	if gi.running {
		return fmt.Errorf("global inspector is already running")
	}

	gi.bus = bus
	gi.channels = guide.NewAgentChannels("inspector", gi.id)
	gi.runCtx, gi.runCancel = context.WithCancel(context.Background())

	var err error
	gi.requestSub, err = bus.SubscribeAsync(gi.channels.Requests, gi.handleBusRequest)
	if err != nil {
		return fmt.Errorf("subscribe to %s: %w", gi.channels.Requests, err)
	}

	gi.responseSub, err = bus.SubscribeAsync(gi.channels.Responses, gi.handleBusResponse)
	if err != nil {
		gi.requestSub.Unsubscribe()
		return fmt.Errorf("subscribe to %s: %w", gi.channels.Responses, err)
	}

	gi.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, gi.handleRegistryAnnouncement)
	if err != nil {
		gi.requestSub.Unsubscribe()
		gi.responseSub.Unsubscribe()
		return fmt.Errorf("subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	gi.running = true
	gi.logger.Info("global inspector started", "id", gi.id)
	return nil
}

// Stop unsubscribes from all bus topics.
func (gi *GlobalInspector) Stop() error {
	if !gi.running {
		return nil
	}

	gi.steering.CloseAll()
	if gi.runCancel != nil {
		gi.runCancel()
	}

	var errs []error
	for _, unsub := range []func() error{
		gi.unsubRequest, gi.unsubResponse, gi.unsubRegistry,
	} {
		if err := unsub(); err != nil {
			errs = append(errs, err)
		}
	}

	gi.running = false
	gi.logger.Info("global inspector stopped", "id", gi.id)

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}
	return nil
}

func (gi *GlobalInspector) unsubRequest() error {
	if gi.requestSub == nil {
		return nil
	}
	err := gi.requestSub.Unsubscribe()
	gi.requestSub = nil
	return err
}

func (gi *GlobalInspector) unsubResponse() error {
	if gi.responseSub == nil {
		return nil
	}
	err := gi.responseSub.Unsubscribe()
	gi.responseSub = nil
	return err
}

func (gi *GlobalInspector) unsubRegistry() error {
	if gi.registrySub == nil {
		return nil
	}
	err := gi.registrySub.Unsubscribe()
	gi.registrySub = nil
	return err
}

// Handle processes a forwarded request with intent dispatch.
func (gi *GlobalInspector) Handle(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	ctx = versioning.WithSessionID(ctx, fwd.SessionID)
	ctx = agentShared.WithGuardianCommandGate(ctx, agentShared.GuardianCommandGateConfig{
		BusProvider:     func() guide.EventBus { return gi.bus },
		SourceAgentID:   func() string { return gi.id },
		SourceAgentType: "inspector",
		SourceAgentName: "Inspector",
	})
	switch fwd.Intent {
	case guide.IntentHelp, guide.IntentChat, guide.IntentUnknown:
		return gi.handleConversation(ctx, fwd)
	default:
		return gi.handleTaskRequest(ctx, fwd)
	}
}

// handleTaskRequest processes non-conversational task requests (audits, checks).
func (gi *GlobalInspector) handleTaskRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if gi.getProvider() == nil {
		return shared.ConversationFallback(gi.getState()), nil
	}

	contract := agentShared.BuildGlobalExecutionContract("inspector-global", fwd.Intent, fwd.Input)
	systemPrompt := shared.GlobalInspectorSystemPromptForContract(contract)
	systemPrompt = agentShared.AppendGlobalExecutionGuidance(systemPrompt, contract, "inspector-global")
	gi.prepareSkillsForInput(fwd.Input)
	tools := gi.buildToolDefinitions()

	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: fwd.Input},
		},
		Model:     gi.config.Model,
		MaxTokens: gi.config.MaxTokens,
		Tools:     tools,
	}
	gi.applyLLMRuntimeProfile(req, "task")

	ledger := agentShared.SteeringLedgerFromContext(ctx)
	result, err := agentShared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return gi.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("tool loop: %v", err)})
		}
		return nil, fmt.Errorf("global inspector tool loop: %w", err)
	}

	return &ConversationResult{
		Response: result,
		Intent:   string(fwd.Intent),
	}, nil
}

func (gi *GlobalInspector) handleBusRequest(msg *guide.Message) error {
	if msg.Type == guide.MessageTypeAction {
		action, ok := msg.GetActionRequest()
		if ok && action != nil {
			gi.steering.HandleAction(action)
		}
		return nil
	}
	if msg.Type != guide.MessageTypeForward {
		gi.deliverPendingMessage(msg)
		return nil
	}

	if !gi.requestSerializer.Acquire(gi.runCtx) {
		return nil // runCtx cancelled, agent shutting down
	}
	defer gi.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	gi.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	agentShared.LogIncomingRequest(gi.steering.EventLogger(), fwd, gi.id)

	reqCtx, cancel := context.WithCancel(gi.runCtx)
	gi.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer cancel()

	ctx := reqCtx
	ctx = shared.WithStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID)
	ctx, usageAcc := shared.WithUsageAccumulator(ctx)
	startTime := time.Now()

	// Create steering ledger for this request.
	ledger := gi.steering.Create(fwd.CorrelationID, gi.id, fwd.SessionID, nil, nil)
	defer gi.steering.Close(fwd.CorrelationID, ctx.Err() != nil)
	ctx = agentShared.WithSteeringLedger(ctx, ledger)
	ctx = agentShared.WithLogMeta(ctx, agentShared.LogMeta{
		EventLogger: gi.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     gi.id,
		SessionID:   fwd.SessionID,
	})

	toolEmitter := agentShared.NewToolCallEmitter(gi.bus, gi.channels, gi.id, fwd.CorrelationID, fwd.SourceAgentID)
	ctx = agentShared.WithToolCallEmitter(ctx, toolEmitter)
	gov := agentShared.NewContextGovernor(gi.config.Model, gi.config.MaxTokens, 0)
	if gi.handoffBridge != nil {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			return gi.handoffBridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	ctx = agentShared.WithContextGovernor(ctx, gov)
	ctx = agentShared.WithProgressPublisher(ctx, &agentShared.ProgressPublisher{
		Bus: gi.bus, Channels: gi.channels,
		AgentID: gi.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})

	if !fwd.FireAndForget {
		shared.PublishStreamStart(gi.bus, gi.channels, ctx, gi.id)
	}

	result, err := gi.Handle(ctx, fwd)
	agentShared.LogResponse(gi.steering.EventLogger(), fwd.CorrelationID, gi.id, fwd.SessionID, time.Since(startTime), err)

	if fwd.FireAndForget {
		return nil
	}

	if err != nil {
		if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		shared.PublishStreamError(gi.bus, gi.channels, ctx, gi.id, err)
		shared.PublishStreamComplete(gi.bus, gi.channels, ctx, gi.id, "", usageAcc.Total())
		errMsg := guide.NewErrorMessage(gi.generateMessageID(), fwd.CorrelationID, gi.id, err.Error())
		return gi.bus.Publish(gi.channels.Errors, errMsg)
	}

	// For streamed conversations, include the authoritative text in the
	// completion event so the chat model can correct dropped/reordered chunks.
	completeText := extractInspectorUserResponse(result)
	shared.PublishStreamComplete(gi.bus, gi.channels, ctx, gi.id, completeText, usageAcc.Total())

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             true,
		Data:                result,
		RespondingAgentID:   gi.id,
		RespondingAgentName: "Inspector",
		ProcessingTime:      time.Since(startTime),
	}
	respMsg := guide.NewResponseMessage(gi.generateMessageID(), resp)
	if gi.agentPod != nil {
		gi.agentPod.FeedScribe("inspector", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}
	return gi.bus.Publish(gi.channels.Responses, respMsg)
}

func (gi *GlobalInspector) handleBusResponse(msg *guide.Message) error {
	gi.deliverPendingMessage(msg)
	return nil
}

func (gi *GlobalInspector) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	gi.knownAgentsMu.Lock()
	defer gi.knownAgentsMu.Unlock()

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		gi.knownAgents[ann.AgentID] = ann
		agentShared.LogAgentEvent(gi.steering.EventLogger(), agentlog.EventRegistryEvent,
			gi.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "registered",
			})
	case guide.MessageTypeAgentUnregistered:
		delete(gi.knownAgents, ann.AgentID)
		agentShared.LogAgentEvent(gi.steering.EventLogger(), agentlog.EventRegistryEvent,
			gi.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "unregistered",
			})
	}
	return nil
}

func (gi *GlobalInspector) generateMessageID() string {
	return fmt.Sprintf("gi_msg_%s", uuid.New().String()[:8])
}

func (gi *GlobalInspector) getState() *shared.InspectorState {
	gi.mu.RLock()
	defer gi.mu.RUnlock()
	return nil // state tracked during audit operations
}

func (gi *GlobalInspector) publishRerouteRequest(reason, originalInput, suggestedTarget string) error {
	if gi.bus == nil {
		return fmt.Errorf("global inspector bus not available")
	}
	reroute := &guide.RerouteRequest{
		OriginalInput:   originalInput,
		Reason:          reason,
		SourceAgentID:   gi.id,
		SuggestedTarget: suggestedTarget,
		ExcludeAgents:   []string{gi.id},
	}
	return gi.bus.Publish(guide.TopicGuideRequests, guide.NewRerouteMessage("", reroute))
}

// DiffStore returns the audit diff store for external capture points.
func (gi *GlobalInspector) DiffStore() *AuditDiffStore { return gi.diffStore }

// GetRoutingInfo returns routing metadata for Guide registration.
func (gi *GlobalInspector) GetRoutingInfo() *guide.AgentRoutingInfo {
	return InspectorRoutingInfo(gi.id)
}

// PublishRequest publishes a routed request to the Guide.
func (gi *GlobalInspector) PublishRequest(req *guide.RouteRequest) error {
	if !gi.running {
		return fmt.Errorf("global inspector is not running")
	}
	req.SourceAgentID = gi.id
	req.SourceAgentName = "inspector"
	msg := guide.NewRequestMessage(gi.generateMessageID(), req)
	return gi.bus.Publish(guide.TopicGuideRequests, msg)
}

// Skills returns the skill registry.
func (gi *GlobalInspector) Skills() *skills.Registry { return gi.skills }

// IsRunning returns whether the global inspector is running.
func (gi *GlobalInspector) IsRunning() bool { return gi.running }

// --- HandoffInjectable ---

// AgentID returns the unique identifier of this inspector instance.
func (gi *GlobalInspector) AgentID() string { return gi.id }

// SetCanonicalID overwrites the inspector's internal ID. Used during
// handoff swap so the new instance assumes the canonical identity.
func (gi *GlobalInspector) SetCanonicalID(id string) {
	gi.mu.Lock()
	defer gi.mu.Unlock()
	gi.id = id
}

// AgentType returns the type name of this agent.
func (gi *GlobalInspector) AgentType() string { return "inspector" }

// Descriptor returns the handoff descriptor for this agent.
func (gi *GlobalInspector) Descriptor() handoff.AgentDescriptor {
	modelID := gi.CurrentModel()
	return handoff.AgentDescriptor{
		AgentType:             "inspector",
		ModelID:               modelID,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryStandalone,
		RuntimeProfiles:       inspectorRuntimeProfiles(),
		DefaultRuntimeProfile: inspectorDefaultRuntimeProfile(),
	}
}

// InjectPreparedContext accepts a prepared context (no-op for global inspector).
func (gi *GlobalInspector) InjectPreparedContext(_ *handoff.PreparedContext) error { return nil }

// Terminate shuts down the global inspector.
func (gi *GlobalInspector) Terminate(_ context.Context) error { return gi.Close() }

// SetAgentPod injects the agent pod for Scribe feed integration.
func (gi *GlobalInspector) SetAgentPod(pod *agentShared.AgentPod) {
	gi.agentPod = pod
}

// SetHandoffBridge sets the handoff bridge for this agent.
func (gi *GlobalInspector) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	gi.handoffBridge = bridge
	if bridge != nil {
		bridge.SetActivityPublisher(gi.config.ActivityPub)
	}
}

// SetFileAccess injects the per-session file access layer.
func (gi *GlobalInspector) SetFileAccess(fa versioning.FileAccess) {
	gi.fileAccess = authority.RestrictFileAccess("inspector", fa)
}

// SetWorkspaceViews injects explicit disk/global/pipeline read access.
func (gi *GlobalInspector) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	gi.workspaceViews = authority.RestrictWorkspaceViews("inspector", views)
}

func (gi *GlobalInspector) SetExecutionBroker(broker purevfs.ExecutionBroker) {
	gi.executionBroker = broker
}

// ExtractArchivableState returns the archivable state of this agent.
func (gi *GlobalInspector) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   gi.AgentID(),
		AgentType: gi.AgentType(),
		Timestamp: time.Now(),
	}
}
