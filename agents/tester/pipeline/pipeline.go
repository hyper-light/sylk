// Package pipeline implements the Pipeline Tester agent — a per-task quality
// engineer that validates individual task implementations within pipelines.
// It gates on Inspector completion and uses GPT-5.3 Codex with xhigh reasoning
// to drive a 6-phase testing protocol through an OpenAI tool loop.
package pipeline

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

// PipelineTester validates individual task implementations within pipelines.
type PipelineTester struct {
	id     string
	config shared.PipelineTesterConfig
	logger *slog.Logger

	// LLM provider (OpenAI gpt-5.3-codex with xhigh reasoning).
	provider *providers.OpenAIProvider

	// State.
	inspectorGate *shared.InspectorGate
	currentPlan   *shared.TestPlan
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

// New creates a new PipelineTester instance.
func New(cfg shared.PipelineTesterConfig, provider *providers.OpenAIProvider) (*PipelineTester, error) {
	cfg = applyConfigDefaults(cfg)

	pt := &PipelineTester{
		id:          fmt.Sprintf("tester-pipeline_%s", uuid.New().String()[:8]),
		config:      cfg,
		logger:      slog.Default().With("agent", "tester-pipeline"),
		provider:    provider,
		diagnoses:   make(map[string]*shared.DiagnosisReport),
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		diagEngine:  shared.NewDiagnosisEngine(),
	}

	pt.initSkills()
	return pt, nil
}

func applyConfigDefaults(cfg shared.PipelineTesterConfig) shared.PipelineTesterConfig {
	defaults := shared.DefaultPipelineTesterConfig()
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
	return cfg
}

func (pt *PipelineTester) initSkills() {
	pt.skills = skills.NewRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"check_inspector_gate",
		"analyze_risk",
		"plan_tests",
		"write_test",
		"run_test_suite",
		"diagnose_failure",
		"report_to_engineer",
		"report_to_designer",
	}
	loaderCfg.AutoLoadDomains = []string{"testing", "quality"}
	pt.skillLoader = skills.NewLoader(pt.skills, loaderCfg)

	pt.registerCoreSkills()
}

func (pt *PipelineTester) registerCoreSkills() {
	pt.skills.Register(shared.CheckInspectorGateSkill(pt.getInspectorGate))
	pt.skills.Register(shared.AnalyzeRiskSkill())
	pt.skills.Register(shared.PlanTestsSkill())
	pt.skills.Register(shared.WriteTestSkill())
	pt.skills.Register(shared.RunTestSuiteSkill())
	pt.skills.Register(shared.DiagnoseFailureSkill(pt.diagEngine))
	pt.skills.Register(reportToEngineerSkill(pt))
	pt.skills.Register(reportToDesignerSkill(pt))
	pt.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   pt.id,
		SessionID: func() string { return pt.config.SessionID },
		Publish:   pt.publishRerouteRequest,
	}))
}

func (pt *PipelineTester) getInspectorGate() *shared.InspectorGate {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.inspectorGate
}

// SetInspectorGate records that the Inspector has passed.
func (pt *PipelineTester) SetInspectorGate(gate *shared.InspectorGate) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.inspectorGate = gate
}

// Close shuts down the pipeline tester.
func (pt *PipelineTester) Close() error {
	return pt.Stop()
}

// Start begins listening for messages on the event bus.
func (pt *PipelineTester) Start(bus guide.EventBus) error {
	if pt.running {
		return fmt.Errorf("pipeline tester is already running")
	}

	pt.bus = bus
	pt.channels = guide.NewAgentChannels(pt.id, "tester-pipeline")

	var err error
	pt.requestSub, err = bus.SubscribeAsync(pt.channels.Requests, pt.handleBusRequest)
	if err != nil {
		return fmt.Errorf("subscribe to %s: %w", pt.channels.Requests, err)
	}

	pt.responseSub, err = bus.SubscribeAsync(pt.channels.Responses, pt.handleBusResponse)
	if err != nil {
		pt.requestSub.Unsubscribe()
		return fmt.Errorf("subscribe to %s: %w", pt.channels.Responses, err)
	}

	pt.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, pt.handleRegistryAnnouncement)
	if err != nil {
		pt.requestSub.Unsubscribe()
		pt.responseSub.Unsubscribe()
		return fmt.Errorf("subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	pt.running = true
	pt.logger.Info("pipeline tester started", "id", pt.id)
	return nil
}

// Stop unsubscribes from all bus topics.
func (pt *PipelineTester) Stop() error {
	if !pt.running {
		return nil
	}

	var errs []error
	for _, unsub := range []func() error{
		pt.unsubRequest, pt.unsubResponse, pt.unsubRegistry,
	} {
		if err := unsub(); err != nil {
			errs = append(errs, err)
		}
	}

	pt.running = false
	pt.logger.Info("pipeline tester stopped", "id", pt.id)

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}
	return nil
}

func (pt *PipelineTester) unsubRequest() error {
	if pt.requestSub == nil {
		return nil
	}
	err := pt.requestSub.Unsubscribe()
	pt.requestSub = nil
	return err
}

func (pt *PipelineTester) unsubResponse() error {
	if pt.responseSub == nil {
		return nil
	}
	err := pt.responseSub.Unsubscribe()
	pt.responseSub = nil
	return err
}

func (pt *PipelineTester) unsubRegistry() error {
	if pt.registrySub == nil {
		return nil
	}
	err := pt.registrySub.Unsubscribe()
	pt.registrySub = nil
	return err
}

// Handle processes a forwarded request through the LLM tool loop.
func (pt *PipelineTester) Handle(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if pt.provider == nil {
		return nil, fmt.Errorf("pipeline tester: no LLM provider configured")
	}

	systemPrompt := shared.PipelineTesterSystemPrompt()
	tools := pt.buildToolDefinitions()

	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: fwd.Input},
		},
		Tools: tools,
	}

	result, err := pt.executeToolLoop(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("pipeline tester tool loop: %w", err)
	}

	return map[string]any{
		"response": result,
		"agent_id": pt.id,
	}, nil
}

func (pt *PipelineTester) handleBusRequest(msg *guide.Message) error {
	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	ctx := context.Background()
	startTime := time.Now()

	result, err := pt.Handle(ctx, fwd)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   pt.id,
		RespondingAgentName: "tester-pipeline",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		errMsg := guide.NewErrorMessage(
			pt.generateMessageID(),
			fwd.CorrelationID,
			pt.id,
			err.Error(),
		)
		return pt.bus.Publish(pt.channels.Errors, errMsg)
	}

	resp.Data = result
	respMsg := guide.NewResponseMessage(pt.generateMessageID(), resp)
	return pt.bus.Publish(pt.channels.Responses, respMsg)
}

func (pt *PipelineTester) handleBusResponse(msg *guide.Message) error {
	pt.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	return nil
}

func (pt *PipelineTester) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		pt.knownAgents[ann.AgentID] = ann
	case guide.MessageTypeAgentUnregistered:
		delete(pt.knownAgents, ann.AgentID)
	}
	return nil
}

func (pt *PipelineTester) generateMessageID() string {
	return fmt.Sprintf("pt_msg_%s", uuid.New().String()[:8])
}

func (pt *PipelineTester) publishRerouteRequest(reason, originalInput, suggestedTarget string) error {
	if pt.bus == nil {
		return fmt.Errorf("pipeline tester bus not available")
	}
	reroute := &guide.RerouteRequest{
		OriginalInput:   originalInput,
		Reason:          reason,
		SourceAgentID:   pt.id,
		SuggestedTarget: suggestedTarget,
		SessionID:       pt.config.SessionID,
		ExcludeAgents:   []string{pt.id},
	}
	return pt.bus.Publish(guide.TopicGuideRequests, guide.NewRerouteMessage("", reroute))
}

// GetRoutingInfo returns routing metadata for Guide registration.
func (pt *PipelineTester) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      pt.id,
		Name:    "tester-pipeline",
		Aliases: []string{"pipeline-test", "task-test"},
		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"pipeline test", "task test", "unit test", "test this",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentCheck: {"test", "validate tests"},
			},
		},
		Registration: &guide.AgentRegistration{
			ID:      pt.id,
			Name:    "tester-pipeline",
			Aliases: []string{"pipeline-test", "task-test"},
			Capabilities: guide.AgentCapabilities{
				Intents:  []guide.Intent{guide.IntentCheck, guide.IntentRecall, guide.IntentHelp},
				Domains:  []guide.Domain{guide.DomainCode},
				Tags:     []string{"testing", "pipeline", "quality", "unit", "validation"},
				Keywords: []string{"test", "pipeline", "unit", "validate", "quality"},
				Priority: 65,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description: "Pipeline quality engineer. Validates individual task output with 6-phase LLM-driven testing protocol.",
			Priority:    65,
		},
	}
}

// Skills returns the skill registry.
func (pt *PipelineTester) Skills() *skills.Registry {
	return pt.skills
}

// IsRunning returns whether the pipeline tester is running.
func (pt *PipelineTester) IsRunning() bool {
	return pt.running
}

// PublishRequest publishes a routed request to the Guide.
func (pt *PipelineTester) PublishRequest(req *guide.RouteRequest) error {
	if !pt.running {
		return fmt.Errorf("pipeline tester is not running")
	}
	req.SourceAgentID = pt.id
	req.SourceAgentName = "tester-pipeline"
	msg := guide.NewRequestMessage(pt.generateMessageID(), req)
	return pt.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// HandoffInjectable Implementation
// =============================================================================

// AgentID returns the unique identifier.
func (pt *PipelineTester) AgentID() string { return pt.id }

// AgentType returns the type classification.
func (pt *PipelineTester) AgentType() string { return "tester-pipeline" }

// Descriptor returns immutable metadata.
func (pt *PipelineTester) Descriptor() handoff.AgentDescriptor {
	return handoff.AgentDescriptor{
		AgentType:       "tester-pipeline",
		ModelID:         "gpt-5.3-codex",
		ReasoningEffort: "xhigh",
		ContextWindow:   200_000,
		Category:        handoff.CategoryPipeline,
	}
}

// InjectPreparedContext accepts context from a handoff.
func (pt *PipelineTester) InjectPreparedContext(_ *handoff.PreparedContext) error {
	return nil
}

// Terminate gracefully shuts down.
func (pt *PipelineTester) Terminate(_ context.Context) error {
	return pt.Stop()
}

// SetHandoffBridge assigns the handoff bridge.
func (pt *PipelineTester) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	pt.handoffBridge = bridge
}

// ExtractArchivableState returns state for handoff persistence.
func (pt *PipelineTester) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   pt.AgentID(),
		AgentType: pt.AgentType(),
		Timestamp: time.Now(),
	}
}
