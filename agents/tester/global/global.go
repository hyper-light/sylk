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

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// GlobalTester architects and runs integration/e2e/cross-cutting tests
// after a batch of concurrent pipelines completes.
type GlobalTester struct {
	id     string
	config shared.GlobalTesterConfig
	logger *slog.Logger

	// LLM provider (OpenAI gpt-5.3-codex with xhigh reasoning).
	provider *providers.OpenAIProvider

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
}

// New creates a new GlobalTester instance.
func New(cfg shared.GlobalTesterConfig, provider *providers.OpenAIProvider) (*GlobalTester, error) {
	cfg = applyConfigDefaults(cfg)

	gt := &GlobalTester{
		id:          fmt.Sprintf("tester_%s", uuid.New().String()[:8]),
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
	gt.channels = guide.NewAgentChannels(gt.id, "tester")

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

// Handle processes a forwarded request through the LLM tool loop.
func (gt *GlobalTester) Handle(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if gt.provider == nil {
		return nil, fmt.Errorf("global tester: no LLM provider configured")
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

	result, err := gt.Handle(ctx, fwd)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   gt.id,
		RespondingAgentName: "tester",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		errMsg := guide.NewErrorMessage(
			gt.generateMessageID(),
			fwd.CorrelationID,
			gt.id,
			err.Error(),
		)
		return gt.bus.Publish(gt.channels.Errors, errMsg)
	}

	resp.Data = result
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
	return &guide.AgentRoutingInfo{
		ID:      gt.id,
		Name:    "tester",
		Aliases: []string{"test", "testing", "qa"},
		ActionShortcuts: []guide.ActionShortcut{
			{Name: "test", Description: "Run tests", DefaultIntent: guide.IntentCheck, DefaultDomain: guide.DomainCode},
			{Name: "coverage", Description: "Coverage report", DefaultIntent: guide.IntentCheck, DefaultDomain: guide.DomainCode},
		},
		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"test", "testing", "coverage", "mutation",
				"integration test", "e2e test", "cross-pipeline",
			},
			WeakTriggers: []string{"verify", "check", "quality"},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentCheck: {"test", "run tests", "coverage", "integration test"},
			},
		},
		Registration: &guide.AgentRegistration{
			ID:      gt.id,
			Name:    "tester",
			Aliases: []string{"test", "testing", "qa"},
			Capabilities: guide.AgentCapabilities{
				Intents:  []guide.Intent{guide.IntentCheck, guide.IntentRecall, guide.IntentHelp},
				Domains:  []guide.Domain{guide.DomainCode},
				Tags:     []string{"testing", "quality", "coverage", "integration", "e2e", "sdet"},
				Keywords: []string{"test", "integration", "e2e", "coverage", "mutation", "quality"},
				Priority: 70,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description: "Cross-pipeline SDET. Architects integration/e2e test strategies with 7-phase LLM-driven protocol.",
			Priority:    70,
		},
	}
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

// ExtractArchivableState returns state for handoff persistence.
func (gt *GlobalTester) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   gt.AgentID(),
		AgentType: gt.AgentType(),
		Timestamp: time.Now(),
	}
}
