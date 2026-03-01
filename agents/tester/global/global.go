// Package global implements the Global Tester agent — a cross-pipeline SDET
// that architects and runs integration/e2e/cross-cutting tests after a batch
// of concurrent pipelines completes. It gates on Inspector completion and uses
// GPT-5.3 Codex with xhigh reasoning to drive a 7-phase testing protocol.
package global

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// globalTesterProvider is the minimal interface the GlobalTester needs.
// Satisfied by *providers.OpenAIProvider and *gateway.GatewayProvider.
type globalTesterProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

// GlobalTester architects and runs integration/e2e/cross-cutting tests
// after a batch of concurrent pipelines completes.
type GlobalTester struct {
	id     string
	config shared.GlobalTesterConfig
	logger *slog.Logger

	// LLM provider (OpenAI gpt-5.3-codex with xhigh reasoning).
	provider        globalTesterProvider
	providerWrapper gateway.ProviderWrapper

	// State.
	inspectorGate *shared.InspectorGate
	currentPlan   *shared.TestPlan
	harness       *TestHarness
	batchContext   *shared.BatchContext
	diagnoses     map[string]*shared.DiagnosisReport
	mu            sync.RWMutex

	// Diagnosis engine.
	diagEngine shared.DiagnosisEngine

	// Skills.
	skills      *skills.Registry
	skillLoader *skills.Loader

	// Bus (standard agent pattern).
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement

	// Handoff integration.
	handoffBridge *handoff.HandoffBridge

	// File access (injected per-session at runtime).
	fileAccess versioning.FileAccess
}

// New creates a new GlobalTester instance.
func New(cfg shared.GlobalTesterConfig, provider globalTesterProvider) (*GlobalTester, error) {
	cfg = applyConfigDefaults(cfg)

	testerID := cfg.AgentID
	if testerID == "" {
		testerID = fmt.Sprintf("tester_%s", uuid.New().String()[:8])
	}

	gt := &GlobalTester{
		id:          testerID,
		config:      cfg,
		logger:      slog.Default().With("agent", "tester"),
		provider:    provider,
		diagnoses:   make(map[string]*shared.DiagnosisReport),
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		diagEngine:  shared.NewDiagnosisEngine(),
	}

	gt.initSkills()
	return gt, nil
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
// Called by the auth-push mechanism when the user provides credentials after
// initial activation. A nil provider disables LLM features (static
// conversation replies continue to work).
func (gt *GlobalTester) SetProvider(p providers.Provider) {
	gt.mu.Lock()
	defer gt.mu.Unlock()
	gt.provider = p
}

// SetProviderWrapper stores a callback that re-applies gateway rate limiting
// to fresh providers created during credential refresh.
func (gt *GlobalTester) SetProviderWrapper(w gateway.ProviderWrapper) {
	gt.providerWrapper = w
}

// HasProvider reports whether an LLM provider is currently configured.
func (gt *GlobalTester) HasProvider() bool {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	return gt.provider != nil
}

// getProvider returns the current provider under read lock.
func (gt *GlobalTester) getProvider() globalTesterProvider {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	return gt.provider
}

// ProviderType implements container.AuthRefreshable.
func (gt *GlobalTester) ProviderType() string { return "openai" }

// RefreshProvider implements container.AuthRefreshable.
// Re-resolves OpenAI credentials and replaces the provider.
func (gt *GlobalTester) RefreshProvider(ctx context.Context, authMethod string) error {
	cfg := providers.OpenAIConfig{
		BaseConfig: providers.BaseConfig{
			Model:     gt.config.Model,
			MaxTokens: gt.config.MaxTokens,
		},
		ReasoningEffort: gt.config.ReasoningEffort,
		AuthMode:        authMethod,
	}
	p, err := providers.NewOpenAIProvider(ctx, cfg)
	if err != nil {
		return fmt.Errorf("tester refresh provider: %w", err)
	}
	if gt.providerWrapper != nil {
		gt.SetProvider(gt.providerWrapper(p))
	} else {
		gt.SetProvider(p)
	}
	gt.logger.Info("provider refreshed", "auth_method", authMethod)
	return nil
}

// SwapModel implements container.ModelSwappable.
// Re-creates the OpenAI provider with the given model ID, re-applying the
// gateway wrapper. Thread-safe via mu.
func (gt *GlobalTester) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	gt.SetProvider(provider)
	gt.mu.Lock()
	gt.config.Model = modelID
	gt.mu.Unlock()
	gt.logger.Info("model swapped", "model", modelID)
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (gt *GlobalTester) CurrentModel() string {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	return gt.config.Model
}

// SupportedModels implements container.ModelSwappable.
func (gt *GlobalTester) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gpt-5.3-codex", DisplayName: "GPT-5.3 Codex"},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
	}
}

func applyConfigDefaults(cfg shared.GlobalTesterConfig) shared.GlobalTesterConfig {
	defaults := shared.DefaultGlobalTesterConfig()
	if cfg.Model == "" {
		cfg.Model = defaults.Model
	}
	if cfg.ReasoningEffort == "" {
		cfg.ReasoningEffort = defaults.ReasoningEffort
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
	if cfg.CoverageThreshold == 0 {
		cfg.CoverageThreshold = defaults.CoverageThreshold
	}
	if cfg.MutationScoreThreshold == 0 {
		cfg.MutationScoreThreshold = defaults.MutationScoreThreshold
	}
	if cfg.FlakyThreshold == 0 {
		cfg.FlakyThreshold = defaults.FlakyThreshold
	}
	if cfg.FlakyRunCount == 0 {
		cfg.FlakyRunCount = defaults.FlakyRunCount
	}
	if cfg.ParallelTests == 0 {
		cfg.ParallelTests = defaults.ParallelTests
	}
	return cfg
}

func (gt *GlobalTester) initSkills() {
	gt.skills = skills.NewRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"check_inspector_gate",
		"analyze_risk",
		"plan_tests",
		"write_test",
		"run_test_suite",
		"diagnose_failure",
		"analyze_batch",
		"analyze_integration_risks",
		"plan_integration_tests",
		"plan_e2e_tests",
		"build_harness",
		"write_integration_test",
		"write_e2e_test",
		"report_to_orchestrator",
		"report_to_architect",
		"escalate_failure",
	}
	loaderCfg.AutoLoadDomains = []string{"testing", "quality"}
	gt.skillLoader = skills.NewLoader(gt.skills, loaderCfg)

	gt.registerCoreSkills()
}

func (gt *GlobalTester) registerCoreSkills() {
	// Shared skills.
	gt.skills.Register(shared.CheckInspectorGateSkill(gt.getInspectorGate))
	gt.skills.Register(shared.AnalyzeRiskSkill())
	gt.skills.Register(shared.PlanTestsSkill())
	gt.skills.Register(shared.WriteTestSkill())
	gt.skills.Register(shared.RunTestSuiteSkill())
	gt.skills.Register(shared.DiagnoseFailureSkill(gt.diagEngine))

	// Global-tester-specific skills.
	gt.skills.Register(analyzeBatchSkill(gt))
	gt.skills.Register(analyzeIntegrationRisksSkill())
	gt.skills.Register(planIntegrationTestsSkill())
	gt.skills.Register(planE2ETestsSkill())
	gt.skills.Register(buildHarnessSkill(gt))
	gt.skills.Register(writeIntegrationTestSkill())
	gt.skills.Register(writeE2ETestSkill())
	gt.skills.Register(reportToOrchestratorSkill(gt))
	gt.skills.Register(reportToArchitectSkill(gt))
	gt.skills.Register(escalateFailureSkill(gt))

	gt.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   gt.id,
		SessionID: func() string { return gt.config.SessionID },
		Publish:   gt.publishRerouteRequest,
	}))
}

func (gt *GlobalTester) getInspectorGate() *shared.InspectorGate {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	return gt.inspectorGate
}

// SetInspectorGate records Inspector completion for batch-level gating.
func (gt *GlobalTester) SetInspectorGate(gate *shared.InspectorGate) {
	gt.mu.Lock()
	defer gt.mu.Unlock()
	gt.inspectorGate = gate
}

// Close shuts down the global tester.
func (gt *GlobalTester) Close() error {
	return gt.Stop()
}

// Start begins listening for messages on the event bus.
func (gt *GlobalTester) Start(bus guide.EventBus) error {
	if gt.running {
		return fmt.Errorf("global tester is already running")
	}

	gt.bus = bus
	gt.channels = guide.NewAgentChannels("tester", gt.id)

	var err error
	gt.requestSub, err = bus.SubscribeAsync(gt.channels.Requests, gt.handleBusRequest)
	if err != nil {
		return fmt.Errorf("subscribe to %s: %w", gt.channels.Requests, err)
	}

	gt.responseSub, err = bus.SubscribeAsync(gt.channels.Responses, gt.handleBusResponse)
	if err != nil {
		gt.requestSub.Unsubscribe()
		return fmt.Errorf("subscribe to %s: %w", gt.channels.Responses, err)
	}

	gt.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, gt.handleRegistryAnnouncement)
	if err != nil {
		gt.requestSub.Unsubscribe()
		gt.responseSub.Unsubscribe()
		return fmt.Errorf("subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	gt.running = true
	gt.logger.Info("global tester started", "id", gt.id)
	return nil
}

// Stop unsubscribes from all bus topics.
func (gt *GlobalTester) Stop() error {
	if !gt.running {
		return nil
	}

	var errs []error
	for _, unsub := range []func() error{
		gt.unsubRequest, gt.unsubResponse, gt.unsubRegistry,
	} {
		if err := unsub(); err != nil {
			errs = append(errs, err)
		}
	}

	gt.running = false
	gt.logger.Info("global tester stopped", "id", gt.id)

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}
	return nil
}

func (gt *GlobalTester) unsubRequest() error {
	if gt.requestSub == nil {
		return nil
	}
	err := gt.requestSub.Unsubscribe()
	gt.requestSub = nil
	return err
}

func (gt *GlobalTester) unsubResponse() error {
	if gt.responseSub == nil {
		return nil
	}
	err := gt.responseSub.Unsubscribe()
	gt.responseSub = nil
	return err
}

func (gt *GlobalTester) unsubRegistry() error {
	if gt.registrySub == nil {
		return nil
	}
	err := gt.registrySub.Unsubscribe()
	gt.registrySub = nil
	return err
}

// Handle processes a forwarded request, dispatching by intent.
// Conversational intents (Help, Chat, Unknown) route to the conversation
// pipeline with static fast-path, LLM fallback, and streaming. Task-oriented
// intents (Check, Recall) route to the full LLM tool loop with the testing
// system prompt.
func (gt *GlobalTester) Handle(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	switch fwd.Intent {
	case guide.IntentHelp, guide.IntentChat, guide.IntentUnknown:
		return gt.handleConversation(ctx, fwd)
	default:
		return gt.handleTaskRequest(ctx, fwd)
	}
}

// handleTaskRequest processes task-oriented requests through the full LLM tool loop.
func (gt *GlobalTester) handleTaskRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if gt.getProvider() == nil {
		return nil, fmt.Errorf("global tester: no LLM provider configured — authenticate with OpenAI to enable")
	}

	systemPrompt := shared.GlobalTesterSystemPrompt()
	tools := gt.buildToolDefinitions()

	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: fwd.Input},
		},
		Tools: tools,
	}

	result, err := gt.executeToolLoop(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("global tester tool loop: %w", err)
	}

	return map[string]any{
		"response": result,
		"agent_id": gt.id,
	}, nil
}

func (gt *GlobalTester) handleBusRequest(msg *guide.Message) error {
	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	ctx := context.Background()
	startTime := time.Now()

	// Always set up stream context — the Guide may promote IntentChat to
	// IntentHelp, so we cannot predicate streaming on the incoming intent.
	// This mirrors the orchestrator's handleBusRequest pattern.
	ctx = withTesterStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID)
	ctx, usageAcc := withTesterUsageAccumulator(ctx)
	toolEmitter := agentshared.NewToolCallEmitter(gt.bus, gt.channels, "tester", fwd.CorrelationID, fwd.SourceAgentID)
	ctx = agentshared.WithToolCallEmitter(ctx, toolEmitter)
	if !fwd.FireAndForget {
		gt.publishStreamStart(ctx)
	}

	result, err := gt.Handle(ctx, fwd)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   "tester",
		RespondingAgentName: "Tester",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		gt.publishStreamError(ctx, err)
		gt.publishStreamComplete(ctx, "", usageAcc.Total())
		resp.Error = err.Error()
		respMsg := guide.NewResponseMessage(gt.generateMessageID(), resp)
		_ = gt.bus.Publish(gt.channels.Responses, respMsg)
		errMsg := guide.NewErrorMessage(
			gt.generateMessageID(),
			fwd.CorrelationID,
			gt.id,
			err.Error(),
		)
		return gt.bus.Publish(gt.channels.Errors, errMsg)
	}

	resp.Data = result

	// Conversation text is already streamed via chunks — send complete with
	// empty text so the bridge doesn't duplicate content.
	completeText := extractTesterUserResponse(result)
	if isStreamedTesterConversation(result) {
		completeText = ""
	}
	gt.publishStreamComplete(ctx, completeText, usageAcc.Total())

	respMsg := guide.NewResponseMessage(gt.generateMessageID(), resp)
	return gt.bus.Publish(gt.channels.Responses, respMsg)
}

func (gt *GlobalTester) handleBusResponse(msg *guide.Message) error {
	gt.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	return nil
}

func (gt *GlobalTester) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		gt.knownAgents[ann.AgentID] = ann
	case guide.MessageTypeAgentUnregistered:
		delete(gt.knownAgents, ann.AgentID)
	}
	return nil
}

func (gt *GlobalTester) generateMessageID() string {
	return fmt.Sprintf("gt_msg_%s", uuid.New().String()[:8])
}

func (gt *GlobalTester) publishRerouteRequest(reason, originalInput, suggestedTarget string) error {
	if gt.bus == nil {
		return fmt.Errorf("global tester bus not available")
	}
	reroute := &guide.RerouteRequest{
		OriginalInput:   originalInput,
		Reason:          reason,
		SourceAgentID:   gt.id,
		SuggestedTarget: suggestedTarget,
		SessionID:       gt.config.SessionID,
		ExcludeAgents:   []string{gt.id},
	}
	return gt.bus.Publish(guide.TopicGuideRequests, guide.NewRerouteMessage("", reroute))
}

// GetRoutingInfo returns routing metadata for Guide registration.
func (gt *GlobalTester) GetRoutingInfo() *guide.AgentRoutingInfo {
	return TesterRoutingInfo(gt.id)
}

// Skills returns the skill registry.
func (gt *GlobalTester) Skills() *skills.Registry {
	return gt.skills
}

// IsRunning returns whether the global tester is running.
func (gt *GlobalTester) IsRunning() bool {
	return gt.running
}

// PublishRequest publishes a routed request to the Guide.
func (gt *GlobalTester) PublishRequest(req *guide.RouteRequest) error {
	if !gt.running {
		return fmt.Errorf("global tester is not running")
	}
	req.SourceAgentID = gt.id
	req.SourceAgentName = "tester"
	msg := guide.NewRequestMessage(gt.generateMessageID(), req)
	return gt.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// HandoffInjectable Implementation
// =============================================================================

// AgentID returns the unique identifier.
func (gt *GlobalTester) AgentID() string { return gt.id }

// SetCanonicalID overwrites the tester's internal ID. Used during
// handoff swap so the new instance assumes the canonical identity.
func (gt *GlobalTester) SetCanonicalID(id string) {
	gt.mu.Lock()
	defer gt.mu.Unlock()
	gt.id = id
}

// AgentType returns the type classification.
func (gt *GlobalTester) AgentType() string { return "tester" }

// Descriptor returns immutable metadata.
func (gt *GlobalTester) Descriptor() handoff.AgentDescriptor {
	return handoff.AgentDescriptor{
		AgentType:       "tester",
		ModelID:         "gpt-5.3-codex",
		ReasoningEffort: "xhigh",
		ContextWindow:   200_000,
		Category:        handoff.CategoryStandalone,
	}
}

// InjectPreparedContext accepts context from a handoff.
func (gt *GlobalTester) InjectPreparedContext(_ *handoff.PreparedContext) error {
	return nil
}

// Terminate gracefully shuts down.
func (gt *GlobalTester) Terminate(_ context.Context) error {
	return gt.Stop()
}

// SetHandoffBridge assigns the handoff bridge.
func (gt *GlobalTester) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	gt.handoffBridge = bridge
}

// SetFileAccess injects the per-session file access layer.
func (gt *GlobalTester) SetFileAccess(fa versioning.FileAccess) {
	gt.fileAccess = fa
}

// ExtractArchivableState returns state for handoff persistence.
func (gt *GlobalTester) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   gt.AgentID(),
		AgentType: gt.AgentType(),
		Timestamp: time.Now(),
	}
}
