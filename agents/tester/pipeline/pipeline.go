// Package pipeline implements the Pipeline Tester agent — a per-task quality
// engineer that validates individual task implementations within pipelines.
// It gates on Inspector completion and uses GPT-5.4 Pro with xhigh reasoning
// to drive a 6-phase testing protocol through an OpenAI tool loop.
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
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// pipelineTesterProvider is the minimal interface the PipelineTester needs.
// Satisfied by *providers.OpenAIProvider and *gateway.GatewayProvider.
type pipelineTesterProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

// PipelineTester validates individual task implementations within pipelines.
type PipelineTester struct {
	id     string
	config shared.PipelineTesterConfig
	logger *slog.Logger

	// LLM provider (OpenAI gpt-5.4-pro with xhigh reasoning).
	provider pipelineTesterProvider

	// Activity publisher for UI agent-panel updates. Nil-safe.
	activityPub  events.ActivityPublisher
	pipelineID   string // Stable task-level pipeline ID for TUI grouping.
	pipelineSlug string
	pipelineName string

	// State.
	currentPlan *shared.TestPlan
	currentTask *agentshared.PipelineTaskInput
	harness     *testHarnessState
	diagnoses   map[string]*shared.DiagnosisReport
	gatePassed  bool
	mu          sync.RWMutex

	// Diagnosis engine.
	diagEngine shared.DiagnosisEngine

	// Skills.
	skills        *skills.Registry
	skillLoader   *skills.Loader
	tools         *toolruntime.Runtime
	toolDefsDirty bool

	// Bus (standard agent pattern).
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement
	pendingMu   sync.Mutex
	pendingBus  map[string]chan *guide.Message

	// Worker type for design-aware prompt selection.
	workerType string

	// Handoff integration.
	handoffBridge *handoff.HandoffBridge

	// Agent pod for cross-agent coordination (Scribe feed, etc.).
	agentPod *agentshared.AgentPod

	fileAccess      versioning.FileAccess
	workspaceViews  versioning.WorkspaceViewAccess
	executionBroker purevfs.ExecutionBroker

	// Request lifecycle.
	runCtx    context.Context
	runCancel context.CancelFunc

	// Steering ledger management.
	steering *agentshared.SteeringManager

	// Request serialization: ensures at most one forwarded request
	// executes at a time, preventing cancel/new-request interleaving.
	requestSerializer *agentshared.RequestSerializer
}

// New creates a new PipelineTester instance.
func New(cfg shared.PipelineTesterConfig, provider pipelineTesterProvider) (*PipelineTester, error) {
	cfg = applyConfigDefaults(cfg)
	agentID := strings.TrimSpace(cfg.AgentID)
	if agentID == "" {
		agentID = uuid.New().String()[:8]
	}

	pt := &PipelineTester{
		id:                agentID,
		config:            cfg,
		logger:            slog.Default().With("agent", "tester-pipeline"),
		provider:          provider,
		diagnoses:         make(map[string]*shared.DiagnosisReport),
		knownAgents:       make(map[string]*guide.AgentAnnouncement),
		pendingBus:        make(map[string]chan *guide.Message),
		diagEngine:        shared.NewDiagnosisEngine(),
		steering:          agentshared.NewSteeringManager(),
		requestSerializer: agentshared.NewRequestSerializer(),
		executionBroker:   purevfs.DefaultExecutionBroker(),
	}

	pt.steering.InitLazy("tester-pipeline", nil)

	if err := pt.initSkills(); err != nil {
		return nil, err
	}
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

func (pt *PipelineTester) initSkills() error {
	pt.skills = skills.NewRegistry()

	pt.registerCoreSkills()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = pipelineTesterVisibleSkillNames()
	loaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	pt.skillLoader = skills.NewLoader(pt.skills, loaderCfg)
	tools, err := toolruntime.New(toolruntime.Config{
		Registry: pt.skills,
		Manifest: pipelineTesterToolManifest(pt.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return fmt.Errorf("initialize pipeline tester tool runtime: %w", err)
	}
	pt.tools = tools
	pt.tools.SyncActiveFromLoaded()
	return nil
}

func (pt *PipelineTester) registerCoreSkills() {
	pt.skills.Register(versioning.NewReadFileSkillFunc(func() versioning.FileAccess { return pt.fileAccess }))
	pt.skills.Register(versioning.NewWriteFileSkillFunc(func() versioning.FileAccess { return pt.fileAccess }))
	pt.skills.Register(versioning.NewEditFileSkillFunc(func() versioning.FileAccess { return pt.fileAccess }))
	pt.skills.Register(versioning.NewCreateDirectorySkillFunc(func() versioning.FileAccess { return pt.fileAccess }))
	pt.skills.Register(checkInspectorGateSkill(pt))
	pt.skills.Register(detectTestHarnessSkill(pt))
	pt.skills.Register(prepareTestHarnessSkill(pt))
	pt.skills.Register(analyzeRiskSkill(pt))
	pt.skills.Register(planTestsSkill(pt))
	pt.skills.Register(writeTestSkill(pt))
	pt.skills.Register(runTestSuiteSkill(pt))
	pt.skills.Register(shared.DiagnoseFailureSkill(pt.diagEngine))
	pt.skills.Register(shared.NewTesterReadWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return pt.workspaceViews }, func() string { return pt.pipelineID }))
	pt.skills.Register(versioning.NewWorkspaceGlobSkill(func() versioning.WorkspaceViewAccess { return pt.workspaceViews }, func() string { return pt.pipelineID }))
	pt.skills.Register(versioning.NewWorkspaceGrepSkill(func() versioning.WorkspaceViewAccess { return pt.workspaceViews }, func() string { return pt.pipelineID }))
	pt.skills.Register(versioning.NewInspectWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return pt.workspaceViews }, func() string { return pt.pipelineID }))
	pt.skills.Register(versioning.NewSummarizeWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return pt.workspaceViews }, func() string { return pt.pipelineID }))
	pt.skills.Register(reportToEngineerSkill(pt))
	pt.skills.Register(reportToDesignerSkill(pt))
	for _, skill := range agentshared.CoordinationSkills(agentshared.CoordinationSkillConfig{
		Client: agentshared.CoordinationClient{
			BusProvider:     func() guide.EventBus { return pt.bus },
			SourceAgentID:   func() string { return pt.id },
			SourceAgentType: func() string { return "tester-pipeline" },
			SessionID:       func() string { return pt.config.SessionID },
			RegisterPending: pt.registerPendingWait,
			ClearPending:    pt.clearPendingWait,
			Timeout:         agentshared.DefaultConsultationTimeout,
		},
		CurrentTaskID:   func() string { return pt.pipelineID },
		CurrentTaskName: func() string { return firstNonEmptyCoordinationName(pt.pipelineName, pt.pipelineSlug) },
		WorkerType:      func() string { return "tester-pipeline" },
	}) {
		pt.skills.Register(skill)
	}
	pt.skills.Register(agentshared.NewSelfDiagnosticSkill(&pipelineTesterDiag{pt: pt}))
	pt.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   pt.id,
		SessionID: func() string { return pt.config.SessionID },
		Publish:   pt.publishRerouteRequest,
	}))
}

func firstNonEmptyCoordinationName(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type pipelineTesterDiag struct{ pt *PipelineTester }

func (d *pipelineTesterDiag) AgentName() string { return "tester_pipeline" }
func (d *pipelineTesterDiag) SessionID() string { return d.pt.config.SessionID }
func (d *pipelineTesterDiag) LogsDir() string {
	return agentshared.LogsDirForAgent(d.pt.steering.SessionDir(), "tester_pipeline")
}
func (d *pipelineTesterDiag) EventLogger() *agentlog.SessionEventLogger {
	return d.pt.steering.EventLogger()
}
func (d *pipelineTesterDiag) PeerLogsDirs() map[string]string { return nil }
func (d *pipelineTesterDiag) RecoveryHints() []string         { return nil }

func (d *pipelineTesterDiag) AgentSpecificDiagnostics() map[string]any {
	d.pt.mu.RLock()
	defer d.pt.mu.RUnlock()
	result := map[string]any{
		"worker_type": d.pt.workerType,
	}
	if d.pt.currentPlan != nil {
		result["test_plan_id"] = d.pt.currentPlan.ID
		result["planned_cases"] = len(d.pt.currentPlan.PlannedCase)
	}
	return result
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (pt *PipelineTester) SetProvider(p pipelineTesterProvider) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.provider = p
}

// getProvider returns the current provider under read lock.
func (pt *PipelineTester) getProvider() pipelineTesterProvider {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.provider
}

// SwapModel implements container.ModelSwappable.
func (pt *PipelineTester) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	pp, ok := provider.(pipelineTesterProvider)
	if !ok {
		return fmt.Errorf("pipeline tester swap model: provider does not satisfy pipelineTesterProvider")
	}
	pt.SetProvider(pp)
	pt.mu.Lock()
	pt.config.Model = modelID
	pt.mu.Unlock()
	pt.logger.Info("model swapped", "model", modelID)
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (pt *PipelineTester) CurrentModel() string {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.config.Model
}

// SupportedModels implements container.ModelSwappable.
func (pt *PipelineTester) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
	}
}

// SetWorkerType sets the worker type for design-aware prompt selection.
func (pt *PipelineTester) SetWorkerType(wt string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.workerType = wt
}

// Close shuts down the pipeline tester.
func (pt *PipelineTester) Close() error {
	if pt.tools != nil {
		pt.tools.Close()
		pt.tools = nil
	}
	return pt.Stop()
}

// Start begins listening for messages on the event bus.
func (pt *PipelineTester) Start(bus guide.EventBus) error {
	if pt.running {
		return fmt.Errorf("pipeline tester is already running")
	}

	pt.bus = bus
	pt.channels = guide.NewAgentChannels("tester-pipeline", pt.id)
	pt.runCtx, pt.runCancel = context.WithCancel(context.Background())

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

	pt.steering.CloseAll()
	if pt.runCancel != nil {
		pt.runCancel()
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
	if pt.getProvider() == nil {
		return nil, fmt.Errorf("pipeline tester: no LLM provider configured")
	}

	task := agentshared.DecodePipelineTaskInput(fwd.Input)
	prevRuntime := pt.swapTaskRuntime(task, gatePassedForTask(task))
	defer pt.restoreTaskRuntime(prevRuntime)
	userMessage := fwd.Input

	pt.mu.RLock()
	wt := pt.workerType
	model := pt.config.Model
	maxTokens := pt.config.MaxTokens
	pt.mu.RUnlock()
	if task != nil {
		if workerType := agentshared.PipelineWorkerType(task); workerType != "" {
			pt.SetWorkerType(workerType)
			wt = workerType
		}
		userMessage = agentshared.ComposePipelineTaskUserPrompt(task)
		if workspaceContext := agentshared.BuildTaskWorkspaceRuntimeContext(ctx, pt.workspaceViews, task); workspaceContext != "" {
			userMessage += "\n\n" + workspaceContext
		}
	}

	systemPrompt := shared.PipelineTesterSystemPromptForWorker(wt)
	if task != nil {
		systemPrompt = agentshared.AppendPipelineSystemContext(systemPrompt, task)
	}
	pt.prepareSkillsForInput(userMessage)
	tools := pt.buildToolDefinitions()

	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: userMessage},
		},
		Model:     model,
		MaxTokens: maxTokens,
		Tools:     tools,
	}
	pt.applyLLMRuntimeProfile(req, "validation")

	// Prepend conversation history as multi-turn message pairs.
	agentshared.PrependHistoryMessages(req, fwd.ConversationHistory)

	ledger := agentshared.SteeringLedgerFromContext(ctx)
	result, err := agentshared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return pt.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("tool loop: %v", err)})
		}
		return nil, fmt.Errorf("pipeline tester tool loop: %w", err)
	}

	return result, nil
}

func (pt *PipelineTester) handleBusRequest(msg *guide.Message) error {
	if msg.Type == guide.MessageTypeAction {
		action, ok := msg.GetActionRequest()
		if ok && action != nil {
			pt.steering.HandleAction(action)
		}
		return nil
	}

	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	if !pt.requestSerializer.Acquire(pt.runCtx) {
		return nil // runCtx cancelled, agent shutting down
	}
	defer pt.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	pt.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	agentshared.LogIncomingRequest(pt.steering.EventLogger(), fwd, pt.id)

	if taskID, _ := fwd.Metadata["task_id"].(string); taskID != "" {
		pt.pipelineID = taskID
	} else if dagID, _ := fwd.Metadata["dag_id"].(string); dagID != "" {
		pt.pipelineID = dagID
	}
	if taskSlug, _ := fwd.Metadata["task_slug"].(string); taskSlug != "" {
		pt.pipelineSlug = taskSlug
	}
	if taskName, _ := fwd.Metadata["task_name"].(string); taskName != "" {
		pt.pipelineName = taskName
	}

	agentshared.EmitDispatchACK(pt.bus, fwd.Metadata, pt.id, "tester-pipeline", fwd.CorrelationID)
	pt.publishActivity(events.EventTypeAgentAction, "Validating implementation quality")

	reqCtx, cancel := context.WithCancel(pt.runCtx)
	reqCtx = versioning.WithSessionID(reqCtx, fwd.SessionID)
	reqCtx = agentshared.WithGuardianCommandGate(reqCtx, agentshared.GuardianCommandGateConfig{
		BusProvider:     func() guide.EventBus { return pt.bus },
		SourceAgentID:   func() string { return pt.id },
		SourceAgentType: "tester-pipeline",
		SourceAgentName: "Tester",
	})
	pt.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer cancel()

	ledger := pt.steering.Create(fwd.CorrelationID, pt.id, fwd.SessionID, nil, nil)
	defer pt.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)

	ctx := reqCtx
	ctx = agentshared.WithStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID)
	ctx = agentshared.WithStreamContextMetadata(ctx, map[string]any{
		"agent_type":  "tester-pipeline",
		"agent_name":  "Tester",
		"pipeline_id": pt.pipelineID,
		"task_id":     pt.pipelineID,
		"task_slug":   pt.pipelineSlug,
	})
	ctx, usageAcc := agentshared.WithUsageAccumulator(ctx)
	ctx = agentshared.WithSteeringLedger(ctx, ledger)
	ctx = agentshared.WithLogMeta(ctx, agentshared.LogMeta{
		EventLogger: pt.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     pt.id,
		SessionID:   fwd.SessionID,
	})
	startTime := time.Now()
	toolEmitter := agentshared.NewToolCallEmitter(pt.bus, pt.channels, pt.id, fwd.CorrelationID, fwd.SourceAgentID)
	ctx = agentshared.WithToolCallEmitter(ctx, toolEmitter)
	gov := agentshared.NewContextGovernor(pt.config.Model, pt.config.MaxTokens, 0)
	if pt.handoffBridge != nil {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			return pt.handoffBridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	ctx = agentshared.WithContextGovernor(ctx, gov)
	ctx = agentshared.WithProgressPublisher(ctx, &agentshared.ProgressPublisher{
		Bus: pt.bus, Channels: pt.channels,
		AgentID: pt.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})
	if !fwd.FireAndForget {
		agentshared.PublishStreamStart(pt.bus, pt.channels, ctx, pt.id)
		if pp := agentshared.ProgressPublisherFromContext(ctx); pp != nil {
			pp.Publish("Translating task and inspector criteria into executable tests and validating the current failure surface.")
		}
	}

	result, err := pt.Handle(ctx, fwd)
	agentshared.LogResponse(pt.steering.EventLogger(), fwd.CorrelationID, pt.id, fwd.SessionID, time.Since(startTime), err)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   pt.id,
		RespondingAgentName: "Tester Pipeline",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		agentshared.PublishStreamError(pt.bus, pt.channels, ctx, pt.id, err)
		agentshared.PublishStreamComplete(pt.bus, pt.channels, ctx, pt.id, "", usageAcc.Total())
		resp.Error = err.Error()
		pt.publishActivity(events.EventTypeAgentError, fmt.Sprintf("Task failed: %s", err.Error()))
		errMsg := guide.NewErrorMessage(
			pt.generateMessageID(),
			fwd.CorrelationID,
			pt.id,
			err.Error(),
		)
		return pt.bus.Publish(pt.channels.Errors, errMsg)
	}

	resp.Data = result
	agentshared.PublishStreamComplete(pt.bus, pt.channels, ctx, pt.id, "", usageAcc.Total())
	pt.publishActivity(events.EventTypeAgentAction, "Validation task completed")

	if pt.agentPod != nil {
		pt.agentPod.FeedScribe("tester-pipeline", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}

	respMsg := guide.NewResponseMessage(pt.generateMessageID(), resp)
	return pt.bus.Publish(pt.channels.Responses, respMsg)
}

func (pt *PipelineTester) handleBusResponse(msg *guide.Message) error {
	pt.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	pt.deliverPendingMessage(msg)
	return nil
}

func (pt *PipelineTester) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	action := "registered"
	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		pt.knownAgents[ann.AgentID] = ann
	case guide.MessageTypeAgentUnregistered:
		delete(pt.knownAgents, ann.AgentID)
		action = "unregistered"
	}
	if el := pt.steering.EventLogger(); el != nil {
		agentshared.LogAgentEvent(el, agentlog.EventRegistryEvent,
			pt.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: action,
			})
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
		Type:    "tester-pipeline",
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
			Description:           "Pipeline quality engineer. Validates individual task output with 6-phase LLM-driven testing protocol.",
			Priority:              65,
			RuntimeProfiles:       pipelineTesterRuntimeProfiles(),
			DefaultRuntimeProfile: pipelineTesterDefaultRuntimeProfile(),
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

// SetActivityPublisher injects the activity publisher for TUI panel updates.
func (pt *PipelineTester) SetActivityPublisher(pub events.ActivityPublisher) {
	pt.activityPub = pub
}

// publishActivity emits a user-visible activity event so the UI agent panel
// tracks this pipeline tester's lifecycle.
func (pt *PipelineTester) publishActivity(eventType events.EventType, content string) {
	if pt.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, pt.config.SessionID, content)
	evt.AgentID = pt.id
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "tester-pipeline"
	evt.Data["agent_name"] = "Tester"
	if pt.pipelineID != "" {
		evt.Data["pipeline_id"] = pt.pipelineID
		evt.Data["task_id"] = pt.pipelineID
	}
	if pt.pipelineSlug != "" {
		evt.Data["task_slug"] = pt.pipelineSlug
	}
	pt.activityPub.PublishActivity(evt)
}

// AgentID returns the unique identifier.
func (pt *PipelineTester) AgentID() string { return pt.id }

// SetCanonicalID overwrites the tester's internal ID so a replacement
// instance can assume the original routing identity after handoff.
func (pt *PipelineTester) SetCanonicalID(id string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.id = id
}

// AgentType returns the type classification.
func (pt *PipelineTester) AgentType() string { return "tester-pipeline" }

// Descriptor returns metadata reflecting the current model.
func (pt *PipelineTester) Descriptor() handoff.AgentDescriptor {
	pt.mu.RLock()
	model := pt.config.Model
	reasoning := pt.config.ReasoningEffort
	pt.mu.RUnlock()
	return handoff.AgentDescriptor{
		AgentType:             "tester-pipeline",
		ModelID:               model,
		ReasoningEffort:       reasoning,
		ContextWindow:         handoff.ContextWindowForModel(model),
		Category:              handoff.CategoryPipeline,
		RuntimeProfiles:       pipelineTesterRuntimeProfiles(),
		DefaultRuntimeProfile: pipelineTesterDefaultRuntimeProfile(),
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

// SetFileAccess assigns the per-pipeline file access layer.
func (pt *PipelineTester) SetFileAccess(fa versioning.FileAccess) {
	pt.fileAccess = authority.RestrictFileAccess("tester-pipeline", fa)
}

// SetWorkspaceViews injects explicit disk/global/pipeline read access.
func (pt *PipelineTester) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	pt.workspaceViews = authority.RestrictWorkspaceViews("tester-pipeline", views)
}

// SetExecutionBroker overrides the strict execution broker.
func (pt *PipelineTester) SetExecutionBroker(broker purevfs.ExecutionBroker) {
	pt.executionBroker = broker
}

// SetHandoffBridge assigns the handoff bridge.
func (pt *PipelineTester) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	pt.handoffBridge = bridge
}

// SetAgentPod assigns the agent pod for cross-agent coordination.
func (pt *PipelineTester) SetAgentPod(pod *agentshared.AgentPod) {
	pt.agentPod = pod
}

// ExtractArchivableState returns state for handoff persistence.
func (pt *PipelineTester) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   pt.AgentID(),
		AgentType: pt.AgentType(),
		Timestamp: time.Now(),
	}
}
