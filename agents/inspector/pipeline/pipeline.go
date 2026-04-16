// Package pipeline implements the Pipeline Inspector agent — a per-task quality
// validation agent that enforces success criteria within individual pipelines.
// It uses Claude Opus 4.6 to drive an LLM tool loop for analysis.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
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

	// Activity publisher for UI agent-panel updates. Nil-safe.
	activityPub  events.ActivityPublisher
	pipelineID   string // Stable task-level pipeline ID for TUI grouping.
	pipelineSlug string
	pipelineName string

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

	// Sync RPC (for feedback loop).
	pendingMu  sync.Mutex
	pendingBus map[string]*agentShared.PendingSyncWait

	// State.
	criteria  map[string]*shared.InspectorCriteria
	taskFiles map[string][]string
	results   map[string]*shared.InspectorResult
	state     *shared.InspectorState
	mu        sync.RWMutex

	// Worker type for design-aware prompt selection.
	workerType string

	// Handoff integration.
	handoffBridge *handoff.HandoffBridge

	// Agent pod for Scribe feed.
	agentPod *agentShared.AgentPod

	fileAccess     versioning.FileAccess
	workspaceViews versioning.WorkspaceViewAccess

	// Request lifecycle.
	runCtx    context.Context
	runCancel context.CancelFunc

	// Steering ledger management.
	steering *agentShared.SteeringManager
	// Tracks Memory Forest branches surfaced during pipeline inspection.
	forestTracker *agentShared.MemoryForestTracker

	// Request serialization: ensures at most one forwarded request
	// executes at a time, preventing cancel/new-request interleaving.
	requestSerializer *agentShared.RequestSerializer
}

// New creates a new PipelineInspector instance.
func New(cfg shared.PipelineInspectorConfig, provider providers.ProviderAdapter) (*PipelineInspector, error) {
	cfg = applyConfigDefaults(cfg)
	agentID := strings.TrimSpace(cfg.AgentID)
	if agentID == "" {
		agentID = uuid.New().String()[:8]
	}

	pi := &PipelineInspector{
		id:                agentID,
		config:            cfg,
		logger:            slog.Default().With("agent", "inspector-pipeline"),
		provider:          provider,
		knownAgents:       make(map[string]*guide.AgentAnnouncement),
		pendingBus:        make(map[string]*agentShared.PendingSyncWait),
		criteria:          make(map[string]*shared.InspectorCriteria),
		taskFiles:         make(map[string][]string),
		results:           make(map[string]*shared.InspectorResult),
		forestTracker:     agentShared.NewMemoryForestTracker(),
		steering:          agentShared.NewSteeringManager(),
		requestSerializer: agentShared.NewRequestSerializer(),
		executionBroker:   purevfs.DefaultExecutionBroker(),
	}
	pi.toolRunner = shared.NewToolRunner(shared.ToolRunnerConfig{
		WorkingDir:    ".",
		Timeout:       cfg.DefaultTimeout,
		Logger:        slog.Default(),
		AgentID:       agentID,
		AgentType:     "inspector-pipeline",
		SessionID:     func() string { return pi.config.SessionID },
		FileAccess:    func() versioning.FileAccess { return pi.fileAccess },
		Broker:        func() purevfs.ExecutionBroker { return pi.executionBroker },
		RequireBroker: true,
	})

	pi.steering.InitLazy("inspector-pipeline", nil)

	pi.initState()
	if err := pi.initSkills(); err != nil {
		return nil, err
	}
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

func (pi *PipelineInspector) initSkills() error {
	pi.skills = skills.NewRegistry()
	pi.hooks = skills.NewHookRegistry()

	pi.registerCoreSkills()
	if err := agentShared.RegisterMemoryForestSkills(pi.skills, "inspector-pipeline", pi.config.Forest, pi.forestTracker); err != nil {
		return fmt.Errorf("register pipeline inspector forest skills: %w", err)
	}
	if err := agentShared.AttachForestOutcomeRecorder(
		pi.skills,
		"handoff_to_ot",
		pi.forestTracker,
		pi.config.Forest,
		func() string { return pi.id },
		"inspector-pipeline",
		func() string { return pi.config.SessionID },
		agentShared.OutcomeOnSuccess("pipeline inspector accepted the task for OT handoff"),
	); err != nil {
		return fmt.Errorf("attach pipeline inspector forest OT outcome: %w", err)
	}
	if err := agentShared.AttachForestOutcomeRecorder(
		pi.skills,
		"request_correction",
		pi.forestTracker,
		pi.config.Forest,
		func() string { return pi.id },
		"inspector-pipeline",
		func() string { return pi.config.SessionID },
		agentShared.OutcomeAlways(forest.OutcomeStatusFailed, "pipeline inspector requested a correction"),
	); err != nil {
		return fmt.Errorf("attach pipeline inspector forest correction outcome: %w", err)
	}
	pi.registerSafetyHook()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = pipelineInspectorVisibleSkillNames()
	loaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	pi.skillLoader = skills.NewLoader(pi.skills, loaderCfg)
	tools, err := toolruntime.New(toolruntime.Config{
		Registry: pi.skills,
		Hooks:    pi.hooks,
		Manifest: pipelineInspectorToolManifest(pi.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return fmt.Errorf("initialize pipeline inspector tool runtime: %w", err)
	}
	pi.tools = tools
	pi.tools.SyncActiveFromLoaded()
	return nil
}

func (pi *PipelineInspector) registerSafetyHook() {
	allowed := allowedInspectorPipelineTools(pi.skills)
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

func allowedInspectorPipelineTools(registry *skills.Registry) map[string]bool {
	allowed := make(map[string]bool)
	for _, name := range pipelineInspectorToolManifest(registry).AllowedNames() {
		allowed[name] = true
	}
	return allowed
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (pi *PipelineInspector) SetProvider(p pipelineInspectorProvider) {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.provider = p
}

// getProvider returns the current provider under read lock.
func (pi *PipelineInspector) getProvider() pipelineInspectorProvider {
	pi.mu.RLock()
	defer pi.mu.RUnlock()
	return pi.provider
}

// SwapModel implements container.ModelSwappable.
func (pi *PipelineInspector) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	pp, ok := provider.(pipelineInspectorProvider)
	if !ok {
		return fmt.Errorf("pipeline inspector swap model: provider does not satisfy pipelineInspectorProvider")
	}
	pi.SetProvider(pp)
	pi.mu.Lock()
	pi.config.Model = modelID
	pi.mu.Unlock()
	pi.logger.Info("model swapped", "model", modelID)
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (pi *PipelineInspector) CurrentModel() string {
	pi.mu.RLock()
	defer pi.mu.RUnlock()
	return pi.config.Model
}

// SupportedModels implements container.ModelSwappable.
func (pi *PipelineInspector) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
	}
}

// SetWorkerType sets the worker type for design-aware prompt and validation selection.
func (pi *PipelineInspector) SetWorkerType(wt string) {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.workerType = wt
}

// Close shuts down the pipeline inspector.
func (pi *PipelineInspector) Close() error {
	if pi.tools != nil {
		pi.tools.Close()
		pi.tools = nil
	}
	return pi.Stop()
}

// Start begins listening for messages on the event bus.
func (pi *PipelineInspector) Start(bus guide.EventBus) error {
	if pi.running {
		return fmt.Errorf("pipeline inspector is already running")
	}

	pi.bus = bus
	pi.channels = guide.NewAgentChannels("inspector-pipeline", pi.id)
	pi.runCtx, pi.runCancel = context.WithCancel(context.Background())

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

	pi.steering.CloseAll()
	if pi.runCancel != nil {
		pi.runCancel()
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

// pipelineTaskFields aliases the shared pipeline task wire contract so the
// inspector, tester, engineer, and designer all consume the same payload.
type pipelineTaskFields = agentShared.PipelineTaskInput

// decodePipelineTask tries to decode fwd.Input as a JSON PipelineTask.
// Returns nil if the input is not a valid pipeline task.
func decodePipelineTask(input string) *pipelineTaskFields {
	return agentShared.DecodePipelineTaskInput(input)
}

// composePipelineUserMessage builds a structured LLM user message from
// decoded pipeline task fields. This replaces the raw JSON blob with a
// clear, actionable instruction the LLM can act on.
func composePipelineUserMessage(task *pipelineTaskFields, contract *agentShared.TaskExecutionContract) string {
	base := agentShared.ComposePipelineTaskUserPrompt(task)
	instructions := strings.TrimSpace(stageInstructions(contract, extractPipelineStage(task.Context)))
	if instructions == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return instructions
	}
	return base + "\n\n" + instructions
}

// extractPipelineStage reads the pipeline_stage from task context.
func extractPipelineStage(ctx map[string]any) string {
	if ctx == nil {
		return "unknown"
	}
	if s, ok := ctx["pipeline_stage"].(string); ok && s != "" {
		return s
	}
	return "unknown"
}

// stageInstructions returns stage-specific instructions for the LLM.
func stageInstructions(contract *agentShared.TaskExecutionContract, stage string) string {
	if inspectorContractSynthesisMode(contract, stage) {
		return "### Instructions\n\n" +
			"This inspection is in **contract synthesis** mode. The required deliverables are a criteria contract, scope inspection, pending-validation state, and a reusable handoff artifact.\n" +
			"Use the task contract and the relevant tool definitions as the workflow source of truth. Missing implementation is expected evidence at this stage. Record pending validation with `get_validation_status` and publish the reusable handoff artifact once the criteria and scope are clear.\n"
	}
	return "### Instructions\n\n" +
		"Implementation evidence exists. Audit the returned work yourself before choosing the next protocol step.\n" +
		"Use `handoff_next` for ordinary phase progression and `challenge_agent` only when a specific returned deliverable is unclear, off-spec, incomplete, or otherwise needs targeted follow-up.\n" +
		"If another agent has already returned a response to one of your challenges, call `process_validation` immediately before choosing any next handoff, challenge, or closure action.\n" +
		"After `process_validation`, you may perform any final direct audit you still need in the same turn, but you must not end that turn without a concrete protocol tool call: `challenge_agent`, `handoff_next`, `finalize_pipeline`, or `handoff_to_ot`.\n" +
		"Do not use `finalize_pipeline` as a substitute for that targeted audit work. Call it only after the current inspector audit is complete and any challenge responses needed for that audit have already been processed.\n" +
		"When you do call `finalize_pipeline`, pass the strongest criteria, implementation, test, and challenge evidence from the current audit so it can determine whether the final tester-backed acceptance step is still needed or OT handoff is now justified.\n" +
		"If `finalize_pipeline` reports `ready_for_ot: true` or `must_handoff_to_ot: true`, immediately call `handoff_to_ot` as the next tool call. Do not answer in prose first.\n" +
		"Do not fan out into repeated audit or grading passes on unchanged workspace state. Use additional local validation tools only when a specific concrete gap remains that the current tester response or protocol state does not already answer.\n"
}

func inspectorContractSynthesisMode(contract *agentShared.TaskExecutionContract, stage string) bool {
	if contract == nil {
		return stage == "inspect"
	}
	return contract.PreImplementation
}

// Handle processes a forwarded request through the LLM tool loop.
func (pi *PipelineInspector) Handle(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	// Decode structured pipeline task from orchestrator dispatch.
	task := decodePipelineTask(fwd.Input)
	hadProtocolState := agentShared.PipelineProtocolStateFromContext(ctx) != nil
	ctx = agentShared.WithPipelineTaskProtocolState(ctx, task)
	if !hadProtocolState {
		defer agentShared.ClosePipelineProtocolState(ctx)
	}
	ctx = agentShared.WithPipelineTurnBaseline(ctx)

	// Only try static/conversational replies for non-pipeline inputs.
	// Pipeline task JSON contains keywords like "state" that would falsely
	// trigger TryStaticReply, short-circuiting actual inspection work.
	if task == nil {
		if staticResult := shared.TryStaticReply(fwd.Input, pi.getState(), pi.getCurrentIssues()); staticResult != nil {
			return staticResult, nil
		}
	}

	if pi.getProvider() == nil {
		return shared.ConversationFallback(pi.getState()), nil
	}

	systemPrompt := shared.PipelineInspectorSystemPrompt()
	contract := (*agentShared.TaskExecutionContract)(nil)

	// Build user message: structured for pipeline tasks, raw for conversation.
	userMessage := fwd.Input
	if task != nil {
		contract = agentShared.BuildTaskExecutionContract(task)
		if workerType := agentShared.PipelineWorkerType(task); workerType != "" {
			pi.SetWorkerType(workerType)
		}
		pi.seedCriteriaFromTask(task)
		contract = agentShared.EnrichTaskExecutionContractWithWorkspaceEvidence(ctx, contract, pi.workspaceViews, task)
		contract.CriteriaDefined = pi.hasCriteria(task.TaskID)
		contract.ValidationResultAvailable = pi.hasValidationResult(task.TaskID)
		contract = agentShared.RebuildTaskExecutionContract(task, contract)
		systemPrompt = shared.PipelineInspectorSystemPromptForContract(contract)
		userMessage = composePipelineUserMessage(task, contract)
		if workspaceContext := agentShared.BuildTaskWorkspaceRuntimeContext(ctx, pi.workspaceViews, task); workspaceContext != "" {
			userMessage += "\n\n" + workspaceContext
		}
		systemPrompt = agentShared.AppendPipelineSystemContext(systemPrompt, task)
	}
	pi.prepareSkillsForInput(userMessage)
	surface := pi.toolRuntime()
	ctx = agentShared.WithTaskExecutionContract(ctx, contract)
	ctx = agentShared.WithTaskExecutionState(ctx, agentShared.NewTaskExecutionState())
	tools := pi.buildToolDefinitionsWithSurface(surface)

	pi.mu.RLock()
	model := pi.config.Model
	maxTokens := pi.config.MaxTokens
	pi.mu.RUnlock()

	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: userMessage},
		},
		Model:     model,
		MaxTokens: maxTokens,
		Tools:     tools,
	}
	pi.applyLLMRuntimeProfile(req, "task")

	// Prepend conversation history as multi-turn message pairs.
	agentShared.PrependHistoryMessages(req, fwd.ConversationHistory)

	ledger := agentShared.SteeringLedgerFromContext(ctx)
	result, err := agentShared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return pi.executeToolLoopWithSurface(ctx, req, ledger, surface)
	})
	if err != nil {
		if task != nil {
			agentShared.PublishPipelineTaskTerminalErrorUpdate(pi.bus, pi.id, task, err, agentShared.PipelineTaskAttempt(task))
		}
		return nil, fmt.Errorf("pipeline inspector tool loop: %w", err)
	}

	return result, nil
}

func (pi *PipelineInspector) handleBusRequest(msg *guide.Message) error {
	if msg.Type == guide.MessageTypeAction {
		action, ok := msg.GetActionRequest()
		if ok && action != nil {
			pi.steering.HandleAction(action)
		}
		return nil
	}
	if msg.Type != guide.MessageTypeForward {
		// Check for pending RPC responses
		pi.deliverPendingMessage(msg)
		return nil
	}

	if !pi.requestSerializer.Acquire(pi.runCtx) {
		return nil // runCtx cancelled, agent shutting down
	}
	defer pi.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	pi.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	agentShared.LogIncomingRequest(pi.steering.EventLogger(), fwd, pi.id)

	if taskID, _ := fwd.Metadata["task_id"].(string); taskID != "" {
		pi.pipelineID = taskID
	}
	if taskSlug, _ := fwd.Metadata["task_slug"].(string); taskSlug != "" {
		pi.pipelineSlug = taskSlug
	}
	if taskName, _ := fwd.Metadata["task_name"].(string); taskName != "" {
		pi.pipelineName = taskName
	}

	agentShared.EmitDispatchACK(pi.bus, fwd.Metadata, pi.id, "inspector-pipeline", fwd.CorrelationID)
	pi.publishActivity(events.EventTypeAgentAction, "Validating implementation quality")

	reqCtx, cancel := context.WithCancel(pi.runCtx)
	reqCtx = versioning.WithSessionID(reqCtx, fwd.SessionID)
	reqCtx = agentShared.WithForwardedTaskScope(reqCtx, fwd.Metadata)
	reqCtx = agentShared.WithGuardianCommandGate(reqCtx, agentShared.GuardianCommandGateConfig{
		BusProvider:     func() guide.EventBus { return pi.bus },
		SourceAgentID:   func() string { return pi.id },
		SourceAgentType: "inspector-pipeline",
		SourceAgentName: "Inspector",
	})
	pi.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer cancel()

	ctx := reqCtx
	ctx = agentShared.WithForwardedStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID, fwd.ParentCorrelationID, agentShared.MergeStreamMetadata(fwd.Metadata, map[string]any{
		"pipeline_task": true,
		"agent_type":    "inspector-pipeline",
		"agent_name":    "Inspector",
		"pipeline_id":   pi.pipelineID,
		"task_id":       pi.pipelineID,
		"task_slug":     pi.pipelineSlug,
		"task_name":     pi.pipelineName,
		"dag_id":        stringValue(fwd.Metadata, "dag_id"),
		"node_id":       stringValue(fwd.Metadata, "node_id"),
	}))
	ctx, usageAcc := shared.WithUsageAccumulator(ctx)
	startTime := time.Now()

	// Create steering ledger for this request.
	ledger := pi.steering.Create(fwd.CorrelationID, pi.id, fwd.SessionID, nil, nil)
	defer pi.steering.Close(fwd.CorrelationID, ctx.Err() != nil)
	ctx = agentShared.WithSteeringLedger(ctx, ledger)
	ctx = agentShared.WithLogMeta(ctx, agentShared.LogMeta{
		EventLogger: pi.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     pi.id,
		SessionID:   fwd.SessionID,
	})
	allowedHandoff := agentShared.AutomaticHandoffAllowedForForwardedRequest(fwd)
	ctx = agentShared.WithAutomaticHandoffEnabled(ctx, allowedHandoff)
	ctx = handoff.WithTransportRetryHandoff(ctx, handoff.TransportRetryHandoffConfig{
		Enabled:       allowedHandoff,
		Bridge:        agentShared.EffectiveHandoffBridge(ctx, pi.handoffBridge),
		AgentID:       pi.id,
		AgentType:     "inspector-pipeline",
		UserRequest:   fwd.Input,
		CorrelationID: fwd.CorrelationID,
		SessionID:     fwd.SessionID,
		EventLogger:   pi.steering.EventLogger(),
		Scribe:        pi.agentPod,
	})
	protocolTask := decodePipelineTask(fwd.Input)
	ctx = agentShared.WithPipelineTaskProtocolState(ctx, protocolTask)
	defer agentShared.ClosePipelineProtocolState(ctx)

	toolEmitter := agentShared.NewToolCallEmitter(pi.bus, pi.channels, pi.id, fwd.CorrelationID, fwd.SourceAgentID)
	ctx = agentShared.WithToolCallEmitter(ctx, toolEmitter)
	gov := agentShared.NewContextGovernor(pi.config.Model, pi.config.MaxTokens, 0)
	if pi.handoffBridge != nil && agentShared.AutomaticHandoffEnabled(ctx) {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			bridge := agentShared.EffectiveHandoffBridge(bctx, pi.handoffBridge)
			if bridge == nil {
				return agentShared.ErrContextBudgetExhausted
			}
			return bridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	ctx = agentShared.WithContextGovernor(ctx, gov)
	ctx = agentShared.WithProgressPublisher(ctx, &agentShared.ProgressPublisher{
		Bus: pi.bus, Channels: pi.channels,
		AgentID: pi.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})

	publishStreamLifecycle := guide.ShouldPublishForwardedStreamLifecycle(fwd)
	if publishStreamLifecycle {
		shared.PublishStreamStart(pi.bus, pi.channels, ctx, pi.id)
		if pp := agentShared.ProgressPublisherFromContext(ctx); pp != nil {
			pp.Publish("Inspecting the task contract, acceptance criteria, and workspace layers to derive concrete implementation failures.")
		}
	}

	result, err := pi.Handle(ctx, fwd)
	agentShared.LogResponse(pi.steering.EventLogger(), fwd.CorrelationID, pi.id, fwd.SessionID, time.Since(startTime), err)

	if err != nil {
		if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		if publishStreamLifecycle {
			shared.PublishStreamError(pi.bus, pi.channels, ctx, pi.id, err)
			shared.PublishStreamComplete(pi.bus, pi.channels, ctx, pi.id, "", usageAcc.Total())
		}
		if fwd.FireAndForget {
			return nil
		}
		pi.publishActivity(events.EventTypeAgentError, fmt.Sprintf("Task failed: %s", err.Error()))
		errMsg := guide.NewErrorMessage(pi.generateMessageID(), fwd.CorrelationID, pi.id, err.Error())
		return pi.bus.Publish(pi.channels.Errors, errMsg)
	}

	if publishStreamLifecycle {
		shared.PublishStreamComplete(pi.bus, pi.channels, ctx, pi.id, "", usageAcc.Total())
	}
	if fwd.FireAndForget {
		return nil
	}
	pi.publishActivity(events.EventTypeAgentAction, "Inspection task completed")

	respData := result
	if protocolTask != nil {
		if snapshot, snapErr := pi.stageSnapshot(protocolTask.TaskID); snapErr == nil {
			respData = snapshot
		}
	}
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             true,
		Data:                agentShared.BuildPipelineTurnResponse(ctx, respData),
		RespondingAgentID:   pi.id,
		RespondingAgentName: "Inspector Pipeline",
		ProcessingTime:      time.Since(startTime),
	}
	respMsg := guide.NewResponseMessage(pi.generateMessageID(), resp)
	if pi.agentPod != nil {
		pi.agentPod.FeedScribe("inspector-pipeline", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}
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

	pi.knownAgentsMu.Lock()
	defer pi.knownAgentsMu.Unlock()

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		pi.knownAgents[ann.AgentID] = ann
		agentShared.LogAgentEvent(pi.steering.EventLogger(), agentlog.EventRegistryEvent,
			pi.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "registered",
			})
	case guide.MessageTypeAgentUnregistered:
		delete(pi.knownAgents, ann.AgentID)
		agentShared.LogAgentEvent(pi.steering.EventLogger(), agentlog.EventRegistryEvent,
			pi.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "unregistered",
			})
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
	if pi.state == nil || pi.state.CurrentTaskID == "" {
		return nil
	}
	if result := pi.results[pi.state.CurrentTaskID]; result != nil {
		return result.Issues
	}
	return nil
}

func (pi *PipelineInspector) hasCriteria(taskID string) bool {
	pi.mu.RLock()
	defer pi.mu.RUnlock()
	return pi.criteria[strings.TrimSpace(taskID)] != nil
}

func (pi *PipelineInspector) hasValidationResult(taskID string) bool {
	pi.mu.RLock()
	defer pi.mu.RUnlock()
	return pi.results[strings.TrimSpace(taskID)] != nil
}

func (pi *PipelineInspector) resolveTaskID(requested string) (string, bool) {
	requested = strings.TrimSpace(requested)

	pi.mu.RLock()
	defer pi.mu.RUnlock()

	current := ""
	if pi.state != nil {
		current = strings.TrimSpace(pi.state.CurrentTaskID)
	}

	if requested != "" {
		if requested == current {
			return requested, false
		}
		if pi.criteria[requested] != nil || pi.results[requested] != nil || len(pi.taskFiles[requested]) > 0 {
			return requested, false
		}
		if current != "" {
			return current, true
		}
		return requested, false
	}

	if current != "" {
		return current, true
	}

	if len(pi.criteria) == 1 {
		for taskID := range pi.criteria {
			return taskID, true
		}
	}
	if len(pi.results) == 1 {
		for taskID := range pi.results {
			return taskID, true
		}
	}
	if len(pi.taskFiles) == 1 {
		for taskID := range pi.taskFiles {
			return taskID, true
		}
	}

	return "", false
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
			Description:           "Pipeline quality inspector. Validates individual task output with LLM-driven analysis tools and TDD criteria.",
			Priority:              70,
			RuntimeProfiles:       pipelineInspectorRuntimeProfiles(),
			DefaultRuntimeProfile: pipelineInspectorDefaultRuntimeProfile(),
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

// DefineCriteria stores success criteria for a task.
func (pi *PipelineInspector) DefineCriteria(taskID string, criteria *shared.InspectorCriteria) {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.criteria[taskID] = criteria
	if pi.state != nil {
		pi.state.CurrentTaskID = taskID
		pi.state.LastActiveAt = time.Now()
	}
	if pi.steering != nil {
		if el := pi.steering.EventLogger(); el != nil {
			agentShared.LogAgentEvent(el, agentlog.EventValidationStarted,
				pi.id, "", "", "info",
				&agentlog.ValidationPayload{Phase: "criteria_defined", TaskID: taskID})
		}
	}
}

func (pi *PipelineInspector) storeValidationResult(result *shared.InspectorResult) {
	if result == nil {
		return
	}
	pi.mu.Lock()
	pi.results[result.TaskID] = result
	if pi.state != nil {
		pi.state.CurrentTaskID = result.TaskID
		pi.state.LastActiveAt = time.Now()
		pi.state.IssuesFound += len(result.Issues)
	}
	pi.mu.Unlock()
}

// ValidateAgainstCriteria validates files against stored criteria.
// Uses the LLM tool loop when a provider is available; falls back to a basic
// passing result otherwise.
func (pi *PipelineInspector) ValidateAgainstCriteria(ctx context.Context, taskID string, files []string, workerType string) (*shared.InspectorResult, error) {
	pi.mu.RLock()
	criteria := pi.criteria[taskID]
	pi.mu.RUnlock()
	if criteria == nil {
		return nil, fmt.Errorf("no criteria defined for task %s", taskID)
	}

	now := time.Now()
	eval, err := pi.runDeterministicValidation(ctx, files, workerType, criteria)
	if err != nil {
		if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("deterministic validation failed: %v", err)})
		}
		return nil, err
	}

	result := &shared.InspectorResult{
		TaskID:             taskID,
		Mode:               "pipeline",
		Passed:             !shared.HasBlockingIssues(eval.Issues) && len(eval.CriteriaFailed) == 0,
		Issues:             eval.Issues,
		CriteriaMet:        eval.CriteriaMet,
		CriteriaFailed:     eval.CriteriaFailed,
		QualityGateResults: eval.QualityGateResults,
		FeedbackHistory:    []shared.InspectorFeedback{},
		StartedAt:          now,
		CompletedAt:        time.Now(),
		LoopCount:          1,
	}
	pi.storeValidationResult(result)
	if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventValidationResult,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.ValidationPayload{Phase: "validated", TaskID: taskID, Success: result.Passed})
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

// SetActivityPublisher injects the activity publisher for TUI panel updates.
func (pi *PipelineInspector) SetActivityPublisher(pub events.ActivityPublisher) {
	pi.activityPub = pub
}

// publishActivity emits a user-visible activity event so the UI agent panel
// tracks this pipeline inspector's lifecycle.
func (pi *PipelineInspector) publishActivity(eventType events.EventType, content string) {
	if pi.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, pi.config.SessionID, content)
	evt.AgentID = pi.id
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "inspector-pipeline"
	evt.Data["agent_name"] = "Inspector"
	if pi.pipelineID != "" {
		evt.Data["pipeline_id"] = pi.pipelineID
		evt.Data["task_id"] = pi.pipelineID
	}
	if pi.pipelineSlug != "" {
		evt.Data["task_slug"] = pi.pipelineSlug
	}
	pi.activityPub.PublishActivity(evt)
}

// --- HandoffInjectable ---

func (pi *PipelineInspector) AgentID() string { return pi.id }

// SetCanonicalID overwrites the inspector's internal ID so a replacement
// instance can assume the original routing identity after handoff.
func (pi *PipelineInspector) SetCanonicalID(id string) {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.id = id
}

func (pi *PipelineInspector) AgentType() string { return "inspector-pipeline" }

func (pi *PipelineInspector) Descriptor() handoff.AgentDescriptor {
	modelID := pi.CurrentModel()
	return handoff.AgentDescriptor{
		AgentType:             "inspector-pipeline",
		ModelID:               modelID,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryPipeline,
		RuntimeProfiles:       pipelineInspectorRuntimeProfiles(),
		DefaultRuntimeProfile: pipelineInspectorDefaultRuntimeProfile(),
	}
}

func (pi *PipelineInspector) InjectPreparedContext(pc *handoff.PreparedContext) error {
	if pc == nil {
		return nil
	}
	if pipelineID, ok := pc.GetMetadata("pipeline_id"); ok && strings.TrimSpace(pipelineID) != "" {
		pi.pipelineID = strings.TrimSpace(pipelineID)
	}
	if taskID, ok := pc.GetMetadata("task_id"); ok && strings.TrimSpace(taskID) != "" {
		pi.pipelineID = strings.TrimSpace(taskID)
	}
	if taskSlug, ok := pc.GetMetadata("task_slug"); ok && strings.TrimSpace(taskSlug) != "" {
		pi.pipelineSlug = strings.TrimSpace(taskSlug)
	}
	return nil
}
func (pi *PipelineInspector) Terminate(_ context.Context) error { return pi.Stop() }
func (pi *PipelineInspector) SetFileAccess(fa versioning.FileAccess) {
	pi.fileAccess = authority.RestrictFileAccess("inspector-pipeline", fa)
}
func (pi *PipelineInspector) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	pi.workspaceViews = authority.RestrictWorkspaceViews("inspector-pipeline", views)
}

func (pi *PipelineInspector) SetExecutionBroker(broker purevfs.ExecutionBroker) {
	pi.executionBroker = broker
}

// SetAgentPod injects the agent pod for Scribe feed integration.
func (pi *PipelineInspector) SetAgentPod(pod *agentShared.AgentPod) {
	pi.agentPod = pod
}

// AgentPod returns the current task-scoped pod binding.
func (pi *PipelineInspector) AgentPod() *agentShared.AgentPod {
	return pi.agentPod
}

func (pi *PipelineInspector) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	pi.handoffBridge = bridge
	if bridge != nil && pi.activityPub != nil {
		bridge.SetActivityPublisher(pi.activityPub)
	}
}
func (pi *PipelineInspector) ExtractArchivableState() *handoff.ArchivableState {
	state := map[string]string{}
	if trimmed := strings.TrimSpace(pi.pipelineID); trimmed != "" {
		state["pipeline_id"] = trimmed
		state["task_id"] = trimmed
	}
	if trimmed := strings.TrimSpace(pi.pipelineSlug); trimmed != "" {
		state["task_slug"] = trimmed
	}
	return &handoff.ArchivableState{
		AgentID:   pi.AgentID(),
		AgentType: pi.AgentType(),
		State:     state,
		Timestamp: time.Now(),
	}
}
