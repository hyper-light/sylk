// Package global implements the Global Inspector agent -- a cross-file
// architectural quality auditor that validates DAG layer completions against
// the architect's plan. Uses Claude Opus 4.6 with an LLM tool loop.
package global

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/adalundhe/sylk/core/skills"
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
	provider        inspectorProvider
	providerWrapper gateway.ProviderWrapper

	// Tool runner for external analysis tools.
	toolRunner *shared.ToolRunner

	// Skills.
	skills      *skills.Registry
	skillLoader *skills.Loader
	hooks       *skills.HookRegistry

	// Bus (standard agent pattern).
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement

	// Sync RPC (for architect escalation).
	pendingMu  sync.Mutex
	pendingBus map[string]chan *guide.Message

	// Audit state.
	diffStore *AuditDiffStore
	mu        sync.RWMutex

	// Handoff integration.
	handoffBridge *handoff.HandoffBridge

	// File access (injected per-session at runtime).
	fileAccess versioning.FileAccess
}

// New creates a new GlobalInspector instance.
func New(cfg shared.GlobalInspectorConfig, provider inspectorProvider) (*GlobalInspector, error) {
	cfg = applyConfigDefaults(cfg)

	gi := &GlobalInspector{
		id:          uuid.New().String()[:8],
		config:      cfg,
		logger:      slog.Default().With("agent", "inspector"),
		provider:    provider,
		toolRunner:  shared.NewToolRunner(".", cfg.DefaultTimeout, slog.Default()),
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		pendingBus:  make(map[string]chan *guide.Message),
		diffStore:   NewAuditDiffStore(cfg.MaxAge),
	}

	gi.initSkills()
	return gi, nil
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (gi *GlobalInspector) SetProvider(p inspectorProvider) {
	gi.mu.Lock()
	defer gi.mu.Unlock()
	gi.provider = p
}

// SetProviderWrapper stores a callback that re-applies gateway rate limiting
// to fresh providers created during credential refresh.
func (gi *GlobalInspector) SetProviderWrapper(w gateway.ProviderWrapper) {
	gi.providerWrapper = w
}

// getProvider returns the current provider under read lock.
func (gi *GlobalInspector) getProvider() inspectorProvider {
	gi.mu.RLock()
	defer gi.mu.RUnlock()
	return gi.provider
}

// ProviderType implements container.AuthRefreshable.
func (gi *GlobalInspector) ProviderType() string { return "anthropic" }

// RefreshProvider implements container.AuthRefreshable.
// Re-resolves Anthropic credentials and replaces the provider.
func (gi *GlobalInspector) RefreshProvider(_ context.Context) error {
	cfg := providers.AnthropicConfig{
		BaseConfig: providers.BaseConfig{
			Model:     gi.config.Model,
			MaxTokens: gi.config.MaxTokens,
		},
		AuthMode: providers.AnthropicAuthModeAPIKey,
	}
	p, err := providers.NewAnthropicProvider(cfg)
	if err != nil {
		return fmt.Errorf("inspector refresh provider: %w", err)
	}
	var wrapped inspectorProvider = p
	if gi.providerWrapper != nil {
		wrapped = gi.providerWrapper(p)
	}
	gi.SetProvider(wrapped)
	gi.logger.Info("provider refreshed")
	return nil
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

func (gi *GlobalInspector) initSkills() {
	gi.skills = skills.NewRegistry()
	gi.hooks = skills.NewHookRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"run_linter", "run_type_checker", "run_security_scan",
		"detect_race_conditions", "detect_deadlocks", "detect_memory_leaks",
		"read_file", "glob", "grep",
		"audit_layer", "validate_plan_adherence",
		"cross_reference_changes", "grade_layer_quality",
		"escalate_findings",
	}
	loaderCfg.AutoLoadDomains = []string{"analysis", "filesystem", "audit"}
	gi.skillLoader = skills.NewLoader(gi.skills, loaderCfg)

	gi.registerCoreSkills()
	gi.registerSafetyHook()
}

func (gi *GlobalInspector) registerSafetyHook() {
	allowed := map[string]bool{
		"run_linter": true, "run_type_checker": true, "run_formatter_check": true,
		"run_security_scan": true, "check_coverage": true, "analyze_complexity": true,
		"detect_race_conditions": true, "detect_deadlocks": true, "detect_memory_leaks": true,
		"read_file": true, "glob": true, "grep": true,
		"audit_layer": true, "validate_plan_adherence": true,
		"cross_reference_changes": true, "grade_layer_quality": true,
		"request_architect_research": true, "request_user_clarification": true,
		"escalate_findings": true, "reroute": true,
	}
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

// Close shuts down the global inspector.
func (gi *GlobalInspector) Close() error {
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

	systemPrompt := shared.GlobalInspectorSystemPrompt()
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

	result, err := gi.executeToolLoop(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("global inspector tool loop: %w", err)
	}

	return &ConversationResult{
		Response: result,
		Intent:   string(fwd.Intent),
	}, nil
}

func (gi *GlobalInspector) handleBusRequest(msg *guide.Message) error {
	if msg.Type != guide.MessageTypeForward {
		gi.deliverPendingMessage(msg)
		return nil
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	ctx := context.Background()
	ctx = shared.WithStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID)
	ctx, usageAcc := shared.WithUsageAccumulator(ctx)
	startTime := time.Now()

	toolEmitter := agentShared.NewToolCallEmitter(gi.bus, gi.channels, "inspector", fwd.CorrelationID, fwd.SourceAgentID)
	ctx = agentShared.WithToolCallEmitter(ctx, toolEmitter)

	if !fwd.FireAndForget {
		shared.PublishStreamStart(gi.bus, gi.channels, ctx, "inspector")
	}

	result, err := gi.Handle(ctx, fwd)

	if fwd.FireAndForget {
		return nil
	}

	if err != nil {
		shared.PublishStreamError(gi.bus, gi.channels, ctx, "inspector", err)
		shared.PublishStreamComplete(gi.bus, gi.channels, ctx, "inspector", "", usageAcc.Total())
		errMsg := guide.NewErrorMessage(gi.generateMessageID(), fwd.CorrelationID, gi.id, err.Error())
		return gi.bus.Publish(gi.channels.Errors, errMsg)
	}

	// For streamed conversations, include the authoritative text in the
	// completion event so the chat model can correct dropped/reordered chunks.
	completeText := extractInspectorUserResponse(result)
	shared.PublishStreamComplete(gi.bus, gi.channels, ctx, "inspector", completeText, usageAcc.Total())

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             true,
		Data:                result,
		RespondingAgentID:   "inspector",
		RespondingAgentName: "Inspector",
		ProcessingTime:      time.Since(startTime),
	}
	respMsg := guide.NewResponseMessage(gi.generateMessageID(), resp)
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

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		gi.knownAgents[ann.AgentID] = ann
	case guide.MessageTypeAgentUnregistered:
		delete(gi.knownAgents, ann.AgentID)
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
	return &guide.AgentRoutingInfo{
		ID:      gi.id,
		Type:    "inspector",
		Name:    "Inspector",
		Aliases: []string{"global-inspector", "audit", "quality-audit"},
		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "audit",
				Description:   "Audit code quality across files",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "chat",
				Description:   "Conversational interaction",
				DefaultIntent: guide.IntentChat,
				DefaultDomain: guide.DomainCode,
			},
		},
		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"audit", "cross-file", "architectural review",
				"plan adherence", "quality audit",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentCheck: {"audit", "inspect", "review quality"},
			},
		},
		Registration: &guide.AgentRegistration{
			ID:      gi.id,
			Name:    "Inspector",
			Aliases: []string{"global-inspector", "audit"},
			Capabilities: guide.AgentCapabilities{
				Intents:  []guide.Intent{guide.IntentCheck, guide.IntentRecall, guide.IntentHelp, guide.IntentChat},
				Domains:  []guide.Domain{guide.DomainCode},
				Tags:     []string{"audit", "quality", "cross-file", "architecture", "plan"},
				Keywords: []string{"audit", "inspect", "quality", "cross-file", "plan", "architecture"},
				Priority: 75,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description: "Global quality inspector. Cross-file architectural auditing, plan adherence validation, and DAG layer gating.",
			Priority:    75,
		},
	}
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

// AgentType returns the type name of this agent.
func (gi *GlobalInspector) AgentType() string { return "inspector" }

// Descriptor returns the handoff descriptor for this agent.
func (gi *GlobalInspector) Descriptor() handoff.AgentDescriptor {
	return handoff.AgentDescriptor{
		AgentType:     "inspector",
		ModelID:       "opus-4.6",
		ContextWindow: 200_000,
		Category:      handoff.CategoryStandalone,
	}
}

// InjectPreparedContext accepts a prepared context (no-op for global inspector).
func (gi *GlobalInspector) InjectPreparedContext(_ *handoff.PreparedContext) error { return nil }

// Terminate shuts down the global inspector.
func (gi *GlobalInspector) Terminate(_ context.Context) error { return gi.Close() }

// SetHandoffBridge sets the handoff bridge for this agent.
func (gi *GlobalInspector) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	gi.handoffBridge = bridge
}

// SetFileAccess injects the per-session file access layer.
func (gi *GlobalInspector) SetFileAccess(fa versioning.FileAccess) {
	gi.fileAccess = fa
}

// ExtractArchivableState returns the archivable state of this agent.
func (gi *GlobalInspector) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   gi.AgentID(),
		AgentType: gi.AgentType(),
		Timestamp: time.Now(),
	}
}
