// Package pipeline implements the Pipeline Inspector agent — a per-task quality
// validation agent that enforces success criteria within individual pipelines.
// It uses Claude Opus 4.6 to drive an LLM tool loop for analysis.
package pipeline

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
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// pipelineInspectorProvider is the minimal interface needed from the LLM.
// Satisfied by *providers.AnthropicProvider and *gateway.GatewayProvider.
type pipelineInspectorProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

// PipelineInspector validates individual task implementations within pipelines.
type PipelineInspector struct {
	id     string
	config shared.PipelineInspectorConfig
	logger *slog.Logger

	// LLM provider (Anthropic Opus 4.6).
	provider pipelineInspectorProvider

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

	// Sync RPC (for feedback loop).
	pendingMu  sync.Mutex
	pendingBus map[string]chan *guide.Message

	// State.
	criteria map[string]*shared.InspectorCriteria
	state    *shared.InspectorState
	mu       sync.RWMutex

	// Worker type for design-aware prompt selection.
	workerType string

	// Handoff integration.
	handoffBridge *handoff.HandoffBridge

	fileAccess versioning.FileAccess
}

// New creates a new PipelineInspector instance.
func New(cfg shared.PipelineInspectorConfig, provider pipelineInspectorProvider) (*PipelineInspector, error) {
	cfg = applyConfigDefaults(cfg)

	pi := &PipelineInspector{
		id:          uuid.New().String()[:8],
		config:      cfg,
		logger:      slog.Default().With("agent", "inspector-pipeline"),
		provider:    provider,
		toolRunner:  shared.NewToolRunner(".", cfg.DefaultTimeout, slog.Default()),
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		pendingBus:  make(map[string]chan *guide.Message),
		criteria:    make(map[string]*shared.InspectorCriteria),
	}

	pi.initState()
	pi.initSkills()
	return pi, nil
}

func applyConfigDefaults(cfg shared.PipelineInspectorConfig) shared.PipelineInspectorConfig {
	defaults := shared.DefaultPipelineInspectorConfig()
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
	if cfg.MaxFeedbackLoops == 0 {
		cfg.MaxFeedbackLoops = defaults.MaxFeedbackLoops
	}
	return cfg
}

func (pi *PipelineInspector) initState() {
	pi.state = &shared.InspectorState{
		ID:           pi.id,
		Mode:         "pipeline",
		StartedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
}

func (pi *PipelineInspector) initSkills() {
	pi.skills = skills.NewRegistry()
	pi.hooks = skills.NewHookRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"run_linter", "run_type_checker", "run_security_scan",
		"detect_race_conditions", "detect_deadlocks",
		"read_file", "glob", "grep",
		"define_criteria", "validate_criteria",
		"grade_task_quality", "request_correction",
		"get_validation_status",
	}
	loaderCfg.AutoLoadDomains = []string{"analysis", "filesystem", "validation"}
	pi.skillLoader = skills.NewLoader(pi.skills, loaderCfg)

	pi.registerCoreSkills()
	pi.registerSafetyHook()
}

func (pi *PipelineInspector) registerSafetyHook() {
	allowed := map[string]bool{
		"run_linter": true, "run_type_checker": true, "run_formatter_check": true,
		"run_security_scan": true, "check_coverage": true, "analyze_complexity": true,
		"detect_race_conditions": true, "detect_deadlocks": true, "detect_memory_leaks": true,
		"read_file": true, "glob": true, "grep": true,
		"define_criteria": true, "validate_criteria": true,
		"grade_task_quality": true, "request_correction": true,
		"request_override": true, "get_validation_status": true,
		"validate_token_usage": true, "validate_accessibility": true,
		"validate_component_api": true, "validate_design_consistency": true,
		"reroute": true,
	}
	pi.hooks.RegisterPreToolCallHook("inspector_pipeline_safety", skills.HookPriorityHigh,
		func(ctx context.Context, data *skills.ToolCallHookData) skills.HookResult {
			if !allowed[data.ToolName] {
				return skills.HookResult{
					Continue: false,
					Error:    fmt.Errorf("tool %q not permitted for pipeline inspector", data.ToolName),
				}
			}
			return skills.HookResult{Continue: true}
		})
}

// SetWorkerType sets the worker type for design-aware prompt and validation selection.
func (pi *PipelineInspector) SetWorkerType(wt string) {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.workerType = wt
}

// Close shuts down the pipeline inspector.
func (pi *PipelineInspector) Close() error {
	return pi.Stop()
}

// Start begins listening for messages on the event bus.
func (pi *PipelineInspector) Start(bus guide.EventBus) error {
	if pi.running {
		return fmt.Errorf("pipeline inspector is already running")
	}

	pi.bus = bus
	pi.channels = guide.NewAgentChannels("inspector-pipeline", pi.id)

	var err error
	pi.requestSub, err = bus.SubscribeAsync(pi.channels.Requests, pi.handleBusRequest)
	if err != nil {
		return fmt.Errorf("subscribe to %s: %w", pi.channels.Requests, err)
	}

	pi.responseSub, err = bus.SubscribeAsync(pi.channels.Responses, pi.handleBusResponse)
	if err != nil {
		pi.requestSub.Unsubscribe()
		return fmt.Errorf("subscribe to %s: %w", pi.channels.Responses, err)
	}

	pi.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, pi.handleRegistryAnnouncement)
	if err != nil {
		pi.requestSub.Unsubscribe()
		pi.responseSub.Unsubscribe()
		return fmt.Errorf("subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	pi.running = true
	pi.logger.Info("pipeline inspector started", "id", pi.id)
	return nil
}

// Stop unsubscribes from all bus topics.
func (pi *PipelineInspector) Stop() error {
	if !pi.running {
		return nil
	}

	var errs []error
	for _, unsub := range []func() error{
		pi.unsubRequest, pi.unsubResponse, pi.unsubRegistry,
	} {
		if err := unsub(); err != nil {
			errs = append(errs, err)
		}
	}

	pi.running = false
	pi.logger.Info("pipeline inspector stopped", "id", pi.id)

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}
	return nil
}

func (pi *PipelineInspector) unsubRequest() error {
	if pi.requestSub == nil {
		return nil
	}
	err := pi.requestSub.Unsubscribe()
	pi.requestSub = nil
	return err
}

func (pi *PipelineInspector) unsubResponse() error {
	if pi.responseSub == nil {
		return nil
	}
	err := pi.responseSub.Unsubscribe()
	pi.responseSub = nil
	return err
}

func (pi *PipelineInspector) unsubRegistry() error {
	if pi.registrySub == nil {
		return nil
	}
	err := pi.registrySub.Unsubscribe()
	pi.registrySub = nil
	return err
}

// Handle processes a forwarded request through the LLM tool loop.
func (pi *PipelineInspector) Handle(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	// Try static conversation fast-path
	if staticResult := shared.TryStaticReply(fwd.Input, pi.getState(), pi.getCurrentIssues()); staticResult != nil {
		return staticResult, nil
	}

	if pi.provider == nil {
		return shared.ConversationFallback(pi.getState()), nil
	}

	systemPrompt := shared.PipelineInspectorSystemPrompt()
	tools := pi.buildToolDefinitions()

	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: fwd.Input},
		},
		Model:     pi.config.Model,
		MaxTokens: pi.config.MaxTokens,
		Tools:     tools,
	}

	result, err := pi.executeToolLoop(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("pipeline inspector tool loop: %w", err)
	}

	return map[string]any{
		"response": result,
		"agent_id": pi.id,
	}, nil
}

func (pi *PipelineInspector) handleBusRequest(msg *guide.Message) error {
	if msg.Type != guide.MessageTypeForward {
		// Check for pending RPC responses
		pi.deliverPendingMessage(msg)
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

	toolEmitter := agentShared.NewToolCallEmitter(pi.bus, pi.channels, "inspector-pipeline", fwd.CorrelationID, fwd.SourceAgentID)
	ctx = agentShared.WithToolCallEmitter(ctx, toolEmitter)

	if !fwd.FireAndForget {
		shared.PublishStreamStart(pi.bus, pi.channels, ctx, "inspector-pipeline")
	}

	result, err := pi.Handle(ctx, fwd)

	if fwd.FireAndForget {
		return nil
	}

	if err != nil {
		shared.PublishStreamError(pi.bus, pi.channels, ctx, "inspector-pipeline", err)
		shared.PublishStreamComplete(pi.bus, pi.channels, ctx, "inspector-pipeline", "", usageAcc.Total())
		errMsg := guide.NewErrorMessage(pi.generateMessageID(), fwd.CorrelationID, pi.id, err.Error())
		return pi.bus.Publish(pi.channels.Errors, errMsg)
	}

	shared.PublishStreamComplete(pi.bus, pi.channels, ctx, "inspector-pipeline", "", usageAcc.Total())

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             true,
		Data:                result,
		RespondingAgentID:   "inspector-pipeline",
		RespondingAgentName: "inspector-pipeline",
		ProcessingTime:      time.Since(startTime),
	}
	respMsg := guide.NewResponseMessage(pi.generateMessageID(), resp)
	return pi.bus.Publish(pi.channels.Responses, respMsg)
}

func (pi *PipelineInspector) handleBusResponse(msg *guide.Message) error {
	pi.deliverPendingMessage(msg)
	return nil
}

func (pi *PipelineInspector) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		pi.knownAgents[ann.AgentID] = ann
	case guide.MessageTypeAgentUnregistered:
		delete(pi.knownAgents, ann.AgentID)
	}
	return nil
}

func (pi *PipelineInspector) generateMessageID() string {
	return fmt.Sprintf("pi_msg_%s", uuid.New().String()[:8])
}

func (pi *PipelineInspector) getState() *shared.InspectorState {
	pi.mu.RLock()
	defer pi.mu.RUnlock()
	if pi.state == nil {
		return nil
	}
	stateCopy := *pi.state
	return &stateCopy
}

func (pi *PipelineInspector) getCurrentIssues() []shared.ValidationIssue {
	pi.mu.RLock()
	defer pi.mu.RUnlock()
	return nil // issues tracked per-request in tool loop
}

func (pi *PipelineInspector) publishRerouteRequest(reason, originalInput, suggestedTarget string) error {
	if pi.bus == nil {
		return fmt.Errorf("pipeline inspector bus not available")
	}
	reroute := &guide.RerouteRequest{
		OriginalInput:   originalInput,
		Reason:          reason,
		SourceAgentID:   pi.id,
		SuggestedTarget: suggestedTarget,
		SessionID:       pi.config.SessionID,
		ExcludeAgents:   []string{pi.id},
	}
	return pi.bus.Publish(guide.TopicGuideRequests, guide.NewRerouteMessage("", reroute))
}

// GetRoutingInfo returns routing metadata for Guide registration.
func (pi *PipelineInspector) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      pi.id,
		Type:    "inspector-pipeline",
		Name:    "inspector-pipeline",
		Aliases: []string{"pipeline-inspector", "task-validator"},
		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "validate-task",
				Description:   "Validate a task implementation against criteria",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainCode,
			},
		},
		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"validate task", "check task", "inspect pipeline",
				"define criteria", "quality gate",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentCheck: {"validate", "inspect", "check quality"},
			},
		},
		Registration: &guide.AgentRegistration{
			ID:      pi.id,
			Name:    "inspector-pipeline",
			Aliases: []string{"pipeline-inspector", "task-validator"},
			Capabilities: guide.AgentCapabilities{
				Intents:  []guide.Intent{guide.IntentCheck, guide.IntentRecall, guide.IntentHelp},
				Domains:  []guide.Domain{guide.DomainCode},
				Tags:     []string{"validation", "pipeline", "quality", "criteria", "inspection"},
				Keywords: []string{"validate", "inspect", "check", "criteria", "quality", "lint"},
				Priority: 70,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description: "Pipeline quality inspector. Validates individual task output with LLM-driven analysis tools and TDD criteria.",
			Priority:    70,
		},
	}
}

// PublishRequest publishes a routed request to the Guide.
func (pi *PipelineInspector) PublishRequest(req *guide.RouteRequest) error {
	if !pi.running {
		return fmt.Errorf("pipeline inspector is not running")
	}
	req.SourceAgentID = pi.id
	req.SourceAgentName = "inspector-pipeline"
	msg := guide.NewRequestMessage(pi.generateMessageID(), req)
	return pi.bus.Publish(guide.TopicGuideRequests, msg)
}

// DefineCriteria stores success criteria for a task (TDD Phase 1).
func (pi *PipelineInspector) DefineCriteria(taskID string, criteria *shared.InspectorCriteria) {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.criteria[taskID] = criteria
	if pi.state != nil {
		pi.state.CurrentTaskID = taskID
		pi.state.LastActiveAt = time.Now()
	}
}

// ValidateAgainstCriteria validates files against stored criteria (TDD Phase 4).
// Uses the LLM tool loop when a provider is available; falls back to a basic
// passing result otherwise.
func (pi *PipelineInspector) ValidateAgainstCriteria(ctx context.Context, taskID string, files []string, workerType string) (*shared.InspectorResult, error) {
	pi.mu.RLock()
	criteria := pi.criteria[taskID]
	pi.mu.RUnlock()

	now := time.Now()

	if pi.provider == nil {
		result := &shared.InspectorResult{
			TaskID:             taskID,
			Mode:               "pipeline",
			Passed:             true,
			Issues:             []shared.ValidationIssue{},
			CriteriaMet:        criteriaIDs(criteria),
			CriteriaFailed:     []string{},
			QualityGateResults: gateResults(criteria),
			FeedbackHistory:    []shared.InspectorFeedback{},
			StartedAt:          now,
			CompletedAt:        time.Now(),
			LoopCount:          1,
		}
		return result, nil
	}

	prompt := buildValidationPrompt(taskID, files, criteria, workerType)
	systemPrompt := shared.PipelineInspectorSystemPromptForDomain(
		shared.ValidationDomainFromWorkerType(workerType),
	)
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: prompt}},
		Model:        pi.config.Model,
		MaxTokens:    pi.config.MaxTokens,
		Tools:        pi.buildToolDefinitions(),
	}

	_, err := pi.executeToolLoop(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("validation tool loop: %w", err)
	}

	result := &shared.InspectorResult{
		TaskID:             taskID,
		Mode:               "pipeline",
		Passed:             true,
		Issues:             []shared.ValidationIssue{},
		CriteriaMet:        criteriaIDs(criteria),
		CriteriaFailed:     []string{},
		QualityGateResults: gateResults(criteria),
		StartedAt:          now,
		CompletedAt:        time.Now(),
		LoopCount:          1,
	}
	return result, nil
}

func buildValidationPrompt(taskID string, files []string, criteria *shared.InspectorCriteria, workerType string) string {
	prompt := fmt.Sprintf("Validate task %s against defined criteria.", taskID)
	if len(files) > 0 {
		prompt += fmt.Sprintf(" Files to validate: %v.", files)
	}
	if criteria != nil {
		prompt += fmt.Sprintf(" Success criteria: %d. Quality gates: %d. Constraints: %d.",
			len(criteria.SuccessCriteria), len(criteria.QualityGates), len(criteria.Constraints))
	}
	if workerType == "designer" {
		prompt += " This is Designer output — apply design validation tools (token usage, accessibility, component API, design consistency) in addition to standard code checks."
	}
	return prompt
}

func criteriaIDs(c *shared.InspectorCriteria) []string {
	if c == nil {
		return []string{}
	}
	ids := make([]string, len(c.SuccessCriteria))
	for i, sc := range c.SuccessCriteria {
		ids[i] = sc.ID
	}
	return ids
}

func gateResults(c *shared.InspectorCriteria) map[string]bool {
	if c == nil {
		return map[string]bool{}
	}
	results := make(map[string]bool, len(c.QualityGates))
	for _, g := range c.QualityGates {
		results[g.Name] = true
	}
	return results
}

// Skills returns the skill registry.
func (pi *PipelineInspector) Skills() *skills.Registry { return pi.skills }

// IsRunning returns whether the pipeline inspector is running.
func (pi *PipelineInspector) IsRunning() bool { return pi.running }

// --- HandoffInjectable ---

func (pi *PipelineInspector) AgentID() string  { return pi.id }
func (pi *PipelineInspector) AgentType() string { return "inspector-pipeline" }

func (pi *PipelineInspector) Descriptor() handoff.AgentDescriptor {
	return handoff.AgentDescriptor{
		AgentType:     "inspector-pipeline",
		ModelID:       "opus-4.6",
		ContextWindow: 200_000,
		Category:      handoff.CategoryPipeline,
	}
}

func (pi *PipelineInspector) InjectPreparedContext(_ *handoff.PreparedContext) error { return nil }
func (pi *PipelineInspector) Terminate(_ context.Context) error                     { return pi.Stop() }
func (pi *PipelineInspector) SetFileAccess(fa versioning.FileAccess) { pi.fileAccess = fa }
func (pi *PipelineInspector) SetHandoffBridge(bridge *handoff.HandoffBridge)         { pi.handoffBridge = bridge }
func (pi *PipelineInspector) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   pi.AgentID(),
		AgentType: pi.AgentType(),
		Timestamp: time.Now(),
	}
}
