package designer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
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

// MaxTodosBeforeArchitect is the scope limit enforced by the system prompt.
// If the LLM determines a task requires more than this many steps, it should
// request Architect decomposition via the reroute skill.
const MaxTodosBeforeArchitect = 12

// designerProvider is the minimal interface the Designer needs from its LLM.
// Satisfied by any providers.ProviderAdapter (e.g. Google, Anthropic, gateway-wrapped).
type designerProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

type Designer struct {
	id     string
	config Config
	logger *slog.Logger

	provider      designerProvider
	refresher     container.ProviderRefresher
	handoffBridge *handoff.HandoffBridge
	agentPod      *shared.AgentPod
	activityPub   events.ActivityPublisher
	pipelineID    string // Stable task-level pipeline ID for TUI grouping.
	pipelineSlug  string
	pipelineName  string
	usageAccum    *designerUsageAccumulator

	state    *DesignerState
	stateMu  sync.RWMutex
	failures map[string]*FailureRecord

	skills        *skills.Registry
	skillLoader   *skills.Loader
	tools         *toolruntime.Runtime
	toolDefsDirty bool

	bus           guide.EventBus
	channels      *guide.AgentChannels
	requestSub    guide.Subscription
	responseSub   guide.Subscription
	registrySub   guide.Subscription
	running       bool
	knownAgentsMu sync.RWMutex
	knownAgents   map[string]*guide.AgentAnnouncement

	consultations []Consultation
	consultMu     sync.RWMutex
	pendingMu     sync.Mutex
	pendingBus    map[string]chan *guide.Message

	fileAccess      versioning.FileAccess
	workspaceViews  versioning.WorkspaceViewAccess
	executionBroker purevfs.ExecutionBroker

	// Request-scoped context lifecycle (mirrors architect/engineer pattern).
	runCtx         context.Context
	runCancel      context.CancelFunc
	requestMu      sync.Mutex
	requestCancels map[string]context.CancelFunc

	// Steering ledger management.
	steering *shared.SteeringManager
	// Tracks Memory Forest branches surfaced during design work.
	forestTracker *shared.MemoryForestTracker

	// Request serialization: ensures at most one forwarded request
	// executes at a time, preventing cancel/new-request interleaving.
	requestSerializer *shared.RequestSerializer
}

type Config struct {
	// Canonical agent ID. If empty, generates a UUID8 (pipeline use).
	ID string

	SystemPrompt    string
	MaxOutputTokens int

	DesignerConfig DesignerConfig

	// ActivityPub publishes activity events so the UI agent panel tracks
	// this agent's lifecycle. Nil-safe (events silently dropped).
	ActivityPub events.ActivityPublisher

	// RequestGuard is called at handler entry to prevent activation demotion
	// during in-flight processing. Returns a release function. Nil-safe.
	RequestGuard func() func()

	Logger *slog.Logger

	SessionID string

	// Forest exposes Memory Forest preference and precedent skills.
	Forest shared.MemoryForestService
}

const (
	DefaultMaxOutputTokens = 8192
)

// New creates a Designer backed by an LLM provider for tool-loop execution.
// The provider must satisfy designerProvider (any provider supporting Complete).
func New(cfg Config, provider designerProvider) (*Designer, error) {
	cfg = applyConfigDefaults(cfg)

	designerID := cfg.ID
	if designerID == "" {
		designerID = uuid.New().String()[:8]
	}

	d := &Designer{
		id:          designerID,
		config:      cfg,
		logger:      cfg.Logger,
		provider:    provider,
		activityPub: cfg.ActivityPub,
		usageAccum:  &designerUsageAccumulator{},
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		failures:    make(map[string]*FailureRecord),
		state: &DesignerState{
			ID:        designerID,
			SessionID: cfg.SessionID,
			Status:    AgentStatusIdle,
			TaskQueue: make([]string, 0),
			StartedAt: time.Now(),
		},
		consultations:     make([]Consultation, 0),
		pendingBus:        make(map[string]chan *guide.Message),
		forestTracker:     shared.NewMemoryForestTracker(),
		steering:          shared.NewSteeringManager(),
		requestSerializer: shared.NewRequestSerializer(),
		executionBroker:   purevfs.DefaultExecutionBroker(),
	}

	d.steering.InitLazy("designer", nil)

	if err := d.initSkills(); err != nil {
		return nil, err
	}

	return d, nil
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (d *Designer) SetProvider(p designerProvider) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	d.provider = p
}

// SetProviderRefresher stores a callback that creates a fresh provider for
// the current model and auth method. Set by cmd/tui.go at bootstrap.
func (d *Designer) SetProviderRefresher(fn container.ProviderRefresher) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	d.refresher = fn
}

// getProvider returns the current provider under read lock.
func (d *Designer) getProvider() designerProvider {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.provider
}

// ProviderType implements container.AuthRefreshable.
func (d *Designer) ProviderType() string {
	return string(container.ProviderForModel(d.CurrentModel()))
}

// RefreshProvider implements container.AuthRefreshable.
func (d *Designer) RefreshProvider(ctx context.Context, authMethod string) error {
	d.stateMu.RLock()
	fn := d.refresher
	d.stateMu.RUnlock()
	if fn == nil {
		return nil
	}
	p, err := fn(ctx, d.CurrentModel(), authMethod)
	if err != nil {
		return fmt.Errorf("designer refresh provider: %w", err)
	}
	d.SetProvider(p)
	d.logger.Info("provider refreshed", "model", d.CurrentModel(), "auth_method", authMethod)
	return nil
}

// SwapModel implements container.ModelSwappable.
// Installs the pre-built provider and updates the active model. Thread-safe.
func (d *Designer) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	d.SetProvider(provider)
	d.stateMu.Lock()
	d.config.DesignerConfig.Model = modelID
	d.stateMu.Unlock()
	d.logger.Info("model swapped", "model", modelID)
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (d *Designer) CurrentModel() string {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.config.DesignerConfig.Model
}

// SupportedModels implements container.ModelSwappable.
func (d *Designer) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
		{ID: "gemini-3-flash-preview", DisplayName: "Gemini 3 Flash"},
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
	}
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DesignerSystemPrompt()
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	defaults := DefaultDesignerToolLoopConfig()
	if cfg.DesignerConfig.MaxToolRuns == 0 {
		cfg.DesignerConfig.MaxToolRuns = defaults.MaxToolRuns
	}
	if cfg.DesignerConfig.MaxTokens == 0 {
		cfg.DesignerConfig.MaxTokens = defaults.MaxTokens
	}
	if cfg.DesignerConfig.DefaultTimeout == 0 {
		cfg.DesignerConfig.DefaultTimeout = defaults.DefaultTimeout
	}
	if cfg.DesignerConfig.ReasoningEffort == "" {
		cfg.DesignerConfig.ReasoningEffort = defaults.ReasoningEffort
	}
	if cfg.DesignerConfig.Model == "" {
		cfg.DesignerConfig.Model = defaults.Model
	}

	if cfg.DesignerConfig.MaxConcurrentTasks == 0 {
		cfg.DesignerConfig.MaxConcurrentTasks = 1
	}
	if cfg.DesignerConfig.MemoryThreshold.CheckpointThreshold == 0 {
		cfg.DesignerConfig.MemoryThreshold = DefaultMemoryThreshold()
	}
	if cfg.DesignerConfig.A11yLevel == "" {
		cfg.DesignerConfig.A11yLevel = "AA"
	}
	return cfg
}

func (d *Designer) initSkills() error {
	d.skills = skills.NewRegistry()
	d.registerCoreSkills()
	if err := shared.RegisterMemoryForestSkills(d.skills, "designer", d.config.Forest, d.forestTracker); err != nil {
		return fmt.Errorf("register designer forest skills: %w", err)
	}
	if err := shared.AttachForestOutcomeRecorder(
		d.skills,
		"handoff_next",
		d.forestTracker,
		d.config.Forest,
		func() string { return d.id },
		"designer",
		func() string { return d.config.SessionID },
		shared.OutcomeOnSuccess("designer handoff succeeded"),
	); err != nil {
		return fmt.Errorf("attach designer forest handoff outcome: %w", err)
	}
	if err := shared.AttachForestOutcomeRecorder(
		d.skills,
		"report_to_orchestrator",
		d.forestTracker,
		d.config.Forest,
		func() string { return d.id },
		"designer",
		func() string { return d.config.SessionID },
		shared.OutcomeAlways(forest.OutcomeStatusFailed, "designer escalated a design failure to the orchestrator"),
	); err != nil {
		return fmt.Errorf("attach designer forest escalation outcome: %w", err)
	}

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = designerVisibleSkillNames()
	loaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	d.skillLoader = skills.NewLoader(d.skills, loaderCfg)

	tools, err := toolruntime.New(toolruntime.Config{
		Registry: d.skills,
		Manifest: designerToolManifest(d.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return fmt.Errorf("initialize designer tool runtime: %w", err)
	}
	d.tools = tools
	d.tools.SyncActiveFromLoaded()
	return nil
}

func (d *Designer) ID() string {
	return d.id
}

func (d *Designer) Close() error {
	if d.tools != nil {
		d.tools.Close()
		d.tools = nil
	}
	d.Stop()
	return nil
}

func (d *Designer) Start(bus guide.EventBus) error {
	if d.running {
		return fmt.Errorf("designer is already running")
	}

	d.bus = bus
	d.channels = guide.NewAgentChannels("designer", d.id)

	var err error
	d.requestSub, err = bus.SubscribeAsync(d.channels.Requests, d.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", d.channels.Requests, err)
	}

	d.responseSub, err = bus.SubscribeAsync(d.channels.Responses, d.handleBusResponse)
	if err != nil {
		d.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", d.channels.Responses, err)
	}

	d.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, d.handleRegistryAnnouncement)
	if err != nil {
		d.requestSub.Unsubscribe()
		d.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	d.runCtx, d.runCancel = context.WithCancel(context.Background())
	d.requestCancels = make(map[string]context.CancelFunc)
	d.running = true
	d.logger.Info("designer started", "id", d.id, "channels", d.channels)
	return nil
}

func (d *Designer) Stop() error {
	if !d.running {
		return nil
	}

	d.steering.CloseAll()
	if d.runCancel != nil {
		d.runCancel()
	}
	errs := d.unsubscribeAll()
	d.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	d.logger.Info("designer stopped", "id", d.id)
	return nil
}

func (d *Designer) unsubscribeAll() []error {
	var errs []error
	if err := d.unsubscribeRequest(); err != nil {
		errs = append(errs, err)
	}
	if err := d.unsubscribeResponse(); err != nil {
		errs = append(errs, err)
	}
	if err := d.unsubscribeRegistry(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (d *Designer) unsubscribeRequest() error {
	if d.requestSub == nil {
		return nil
	}
	err := d.requestSub.Unsubscribe()
	d.requestSub = nil
	return err
}

func (d *Designer) unsubscribeResponse() error {
	if d.responseSub == nil {
		return nil
	}
	err := d.responseSub.Unsubscribe()
	d.responseSub = nil
	return err
}

func (d *Designer) unsubscribeRegistry() error {
	if d.registrySub == nil {
		return nil
	}
	err := d.registrySub.Unsubscribe()
	d.registrySub = nil
	return err
}

func (d *Designer) IsRunning() bool {
	return d.running
}

func (d *Designer) Bus() guide.EventBus {
	return d.bus
}

func (d *Designer) Channels() *guide.AgentChannels {
	return d.channels
}

// =============================================================================
// Request Handling — LLM Tool Loop
// =============================================================================

func (d *Designer) handleBusRequest(msg *guide.Message) error {
	if msg.Type == guide.MessageTypeAction {
		return d.handleActionMessage(msg)
	}
	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	if !d.requestSerializer.Acquire(d.runCtx) {
		return nil // parent context done, agent shutting down
	}
	defer d.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	d.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	shared.LogIncomingRequest(d.steering.EventLogger(), fwd, d.id)

	if taskID, _ := fwd.Metadata["task_id"].(string); taskID != "" {
		d.pipelineID = taskID
	}
	if taskSlug, _ := fwd.Metadata["task_slug"].(string); taskSlug != "" {
		d.pipelineSlug = taskSlug
	}
	if taskName, _ := fwd.Metadata["task_name"].(string); taskName != "" {
		d.pipelineName = taskName
	}

	shared.EmitDispatchACK(d.bus, fwd.Metadata, d.id, "designer", fwd.CorrelationID)
	d.publishActivity(events.EventTypeAgentAction, "Processing design task")

	if d.config.RequestGuard != nil {
		release := d.config.RequestGuard()
		defer release()
	}

	// Request-scoped cancellable context.
	reqCtx, cancel := context.WithCancel(d.runCtx)
	reqCtx = versioning.WithSessionID(reqCtx, fwd.SessionID)
	reqCtx = shared.WithGuardianCommandGate(reqCtx, shared.GuardianCommandGateConfig{
		BusProvider:     func() guide.EventBus { return d.bus },
		SourceAgentID:   func() string { return d.id },
		SourceAgentType: "designer",
		SourceAgentName: "Designer",
	})
	d.registerRequestCancel(fwd.CorrelationID, cancel)
	d.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer d.clearRequestCancel(fwd.CorrelationID)
	defer cancel()

	// Create steering ledger for this request.
	ledger := d.steering.Create(fwd.CorrelationID, d.id, fwd.SessionID, nil, nil)
	defer d.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)

	emitter := shared.NewToolCallEmitter(d.bus, d.channels, d.id, fwd.CorrelationID, fwd.SourceAgentID)
	metaString := func(key string) string {
		if fwd.Metadata == nil {
			return ""
		}
		value, _ := fwd.Metadata[key].(string)
		return strings.TrimSpace(value)
	}
	ctx := shared.WithForwardedStreamContext(reqCtx, fwd.CorrelationID, fwd.SourceAgentID, fwd.ParentCorrelationID, shared.MergeStreamMetadata(fwd.Metadata, map[string]any{
		"pipeline_task": true,
		"agent_type":    "designer",
		"agent_name":    "Designer",
		"pipeline_id":   d.pipelineID,
		"task_id":       d.pipelineID,
		"task_slug":     d.pipelineSlug,
		"task_name":     metaString("task_name"),
		"dag_id":        metaString("dag_id"),
		"node_id":       metaString("node_id"),
	}))
	ctx, usageAcc := shared.WithUsageAccumulator(ctx)
	ctx = shared.WithToolCallEmitter(ctx, emitter)
	ctx = shared.WithSteeringLedger(ctx, ledger)
	ctx = shared.WithLogMeta(ctx, shared.LogMeta{
		EventLogger: d.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     d.id,
		SessionID:   fwd.SessionID,
	})
	gov := shared.NewContextGovernor(d.config.DesignerConfig.Model, d.config.DesignerConfig.MaxTokens, 0)
	if d.handoffBridge != nil {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			return d.handoffBridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	ctx = shared.WithContextGovernor(ctx, gov)
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus: d.bus, Channels: d.channels,
		AgentID: d.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})
	if !fwd.FireAndForget {
		shared.PublishStreamStart(d.bus, d.channels, ctx, d.id)
		if pp := shared.ProgressPublisherFromContext(ctx); pp != nil {
			pp.Publish("Reviewing UX criteria, design constraints, and task-local workspace before updating the implementation.")
		}
	}
	startTime := time.Now()

	result, err := d.handleDesign(ctx, fwd)
	shared.LogResponse(d.steering.EventLogger(), fwd.CorrelationID, d.id, fwd.SessionID, time.Since(startTime), err)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   d.id,
		RespondingAgentName: "Designer",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		shared.PublishStreamError(d.bus, d.channels, ctx, d.id, err)
		shared.PublishStreamComplete(d.bus, d.channels, ctx, d.id, "", usageAcc.Total())
		resp.Error = err.Error()
		d.publishActivity(events.EventTypeAgentError, fmt.Sprintf("Task failed: %s", err.Error()))
		errMsg := guide.NewErrorMessage(
			d.generateMessageID(),
			fwd.CorrelationID,
			d.id,
			err.Error(),
		)
		return d.bus.Publish(d.channels.Errors, errMsg)
	}

	respData := result
	if shared.DecodePipelineTaskInput(fwd.Input) != nil {
		respData = shared.BuildPipelineTurnResponse(ctx, result)
	}
	resp.Data = respData
	shared.PublishStreamComplete(d.bus, d.channels, ctx, d.id, "", usageAcc.Total())
	d.publishActivity(events.EventTypeAgentAction, "Design task completed")

	respMsg := guide.NewResponseMessage(d.generateMessageID(), resp)
	if d.agentPod != nil {
		d.agentPod.FeedScribe("designer", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}
	return d.bus.Publish(d.channels.Responses, respMsg)
}

func (d *Designer) generateMessageID() string {
	return fmt.Sprintf("designer_msg_%s", uuid.New().String())
}

func (d *Designer) registerRequestCancel(correlationID string, cancel context.CancelFunc) {
	d.requestMu.Lock()
	if d.requestCancels != nil {
		d.requestCancels[correlationID] = cancel
	}
	d.requestMu.Unlock()
}

func (d *Designer) clearRequestCancel(correlationID string) {
	d.requestMu.Lock()
	delete(d.requestCancels, correlationID)
	d.requestMu.Unlock()
}

func (d *Designer) cancelRequest(correlationID string) {
	d.requestMu.Lock()
	cancel := d.requestCancels[correlationID]
	delete(d.requestCancels, correlationID)
	d.requestMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (d *Designer) handleActionMessage(msg *guide.Message) error {
	action, ok := msg.GetActionRequest()
	if !ok || action == nil {
		return nil
	}
	if d.steering.HandleAction(action) {
		return nil
	}
	if action.Action == "cancel" {
		if el := d.steering.EventLogger(); el != nil {
			shared.LogAgentEvent(el, agentlog.EventError,
				d.id, "", action.CorrelationID, "warn",
				&agentlog.ErrorPayload{Error: "request cancelled via action"})
		}
		d.cancelRequest(action.CorrelationID)
	}
	return nil
}

// handleDesign is the unified entry point for all design intents. It builds
// a provider request with the composed system prompt and full tool definitions,
// then executes the bounded LLM tool loop.
func (d *Designer) handleDesign(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if fwd.Input == "" {
		return nil, fmt.Errorf("design task input is required")
	}

	d.setStatus(AgentStatusBusy)
	defer d.setStatus(AgentStatusIdle)

	timeout := d.config.DesignerConfig.DefaultTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	userMessage := fwd.Input
	task := shared.DecodePipelineTaskInput(fwd.Input)
	ctx = shared.WithPipelineTaskProtocolState(ctx, task)
	defer shared.ClosePipelineProtocolState(ctx)
	contract := (*shared.TaskExecutionContract)(nil)
	if task != nil {
		contract = shared.BuildTaskExecutionContract(task)
		userMessage = shared.ComposePipelineTaskUserPrompt(task)
		if workspaceContext := shared.BuildTaskWorkspaceRuntimeContext(ctx, d.workspaceViews, task); workspaceContext != "" {
			userMessage += "\n\n" + workspaceContext
		}
	}
	systemPrompt := d.systemPromptForContract(contract)
	if task != nil {
		systemPrompt = shared.AppendPipelineSystemContext(systemPrompt, task)
	}

	d.prepareSkillsForInput(userMessage)
	surface := d.toolRuntime()
	ctx = shared.WithTaskExecutionContract(ctx, contract)
	ctx = shared.WithTaskExecutionState(ctx, shared.NewTaskExecutionState())
	toolDefs := d.buildToolDefinitionsWithSurface(surface)

	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{
				Role:    providers.RoleUser,
				Content: userMessage,
			},
		},
		Tools:     toolDefs,
		MaxTokens: d.config.DesignerConfig.MaxTokens,
	}
	d.applyDesignRuntimeProfile(req)

	// Prepend conversation history as multi-turn message pairs.
	shared.PrependHistoryMessages(req, fwd.ConversationHistory)

	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventPromptComposed,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.DesignPayload{Phase: "prompt_composed"})
	}

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return d.executeToolLoopWithSurface(ctx, req, ledger, surface)
	})
	if err != nil {
		if task != nil {
			shared.PublishPipelineTaskTerminalErrorUpdate(d.bus, d.id, task, err, shared.PipelineTaskAttempt(task))
		}
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: err.Error()})
		}
		d.recordFailure(fwd.CorrelationID, err.Error(), fwd.Input)
		return nil, err
	}

	inTok, outTok := d.usageAccum.Total()

	return map[string]any{
		"response":      result,
		"agent_id":      d.id,
		"input_tokens":  inTok,
		"output_tokens": outTok,
	}, nil
}

func (d *Designer) systemPromptForContract(contract *shared.TaskExecutionContract) string {
	if contract == nil {
		return d.config.SystemPrompt
	}
	if strings.TrimSpace(d.config.SystemPrompt) == "" || d.config.SystemPrompt == DesignerSystemPrompt() {
		return DesignerSystemPromptForContract(contract)
	}
	return d.config.SystemPrompt
}

func (d *Designer) handleBusResponse(msg *guide.Message) error {
	d.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	d.deliverPendingMessage(msg)
	return nil
}

func (d *Designer) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	d.knownAgentsMu.Lock()
	defer d.knownAgentsMu.Unlock()

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		d.knownAgents[ann.AgentID] = ann
		d.logger.Debug("agent registered", "agent_id", ann.AgentID)
		shared.LogAgentEvent(d.steering.EventLogger(), agentlog.EventRegistryEvent,
			d.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "registered",
			})
	case guide.MessageTypeAgentUnregistered:
		delete(d.knownAgents, ann.AgentID)
		d.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
		shared.LogAgentEvent(d.steering.EventLogger(), agentlog.EventRegistryEvent,
			d.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "unregistered",
			})
	}

	return nil
}

func (d *Designer) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	d.knownAgentsMu.RLock()
	defer d.knownAgentsMu.RUnlock()
	result := make(map[string]*guide.AgentAnnouncement, len(d.knownAgents))
	for k, v := range d.knownAgents {
		result[k] = v
	}
	return result
}

// HandleRequest is the public entry point for direct invocations (e.g. from
// the TDD pipeline factory). It wraps the input into a ForwardedRequest and
// delegates to the LLM tool loop.
func (d *Designer) HandleRequest(ctx context.Context, input string) (_ any, retErr error) {
	task := shared.DecodePipelineTaskInput(input)

	fwd := &guide.ForwardedRequest{
		Input:         input,
		Intent:        guide.IntentDesign,
		Domain:        guide.DomainCode,
		SourceAgentID: d.id,
		TargetAgentID: d.id,
	}
	if task != nil {
		fwd.SessionID = strings.TrimSpace(task.SessionID)
		taskSlug, _ := task.Context["task_slug"].(string)
		fwd.Metadata = map[string]any{
			"task_id":   strings.TrimSpace(task.TaskID),
			"task_slug": strings.TrimSpace(taskSlug),
		}
	}
	return d.handleDesign(ctx, fwd)
}

// =============================================================================
// State Management
// =============================================================================

func (d *Designer) setStatus(status AgentStatus) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	d.state.Status = status
	d.state.LastActiveAt = time.Now()
	shared.LogStatusUpdate(d.steering.EventLogger(), d.id, "", string(status))
}

// publishActivity emits a user-visible activity event so the UI agent panel
// tracks this designer's lifecycle.
func (d *Designer) publishActivity(eventType events.EventType, content string) {
	if d.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, d.config.SessionID, content)
	evt.AgentID = d.id
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "designer"
	evt.Data["agent_name"] = "Designer"
	if d.pipelineID != "" {
		evt.Data["pipeline_id"] = d.pipelineID
		evt.Data["task_id"] = d.pipelineID
	}
	if d.pipelineSlug != "" {
		evt.Data["task_slug"] = d.pipelineSlug
	}
	d.activityPub.PublishActivity(evt)
}

func (d *Designer) recordFailure(taskID, errorMsg, approach string) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()

	existing, ok := d.failures[taskID]
	if ok {
		existing.AttemptCount++
		existing.LastError = errorMsg
		existing.Timestamp = time.Now()
	} else {
		d.failures[taskID] = &FailureRecord{
			TaskID:       taskID,
			DesignerID:   d.id,
			AttemptCount: 1,
			LastError:    errorMsg,
			Approach:     approach,
			Timestamp:    time.Now(),
		}
	}

	d.state.FailedCount++
}

func (d *Designer) recordConsultation(c Consultation) {
	d.consultMu.Lock()
	defer d.consultMu.Unlock()
	d.consultations = append(d.consultations, c)
}

func (d *Designer) GetState() *DesignerState {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()

	stateCopy := *d.state
	return &stateCopy
}

func (d *Designer) GetConsultations() []Consultation {
	d.consultMu.RLock()
	defer d.consultMu.RUnlock()

	result := make([]Consultation, len(d.consultations))
	copy(result, d.consultations)
	return result
}

// =============================================================================
// Routing & Skills
// =============================================================================

// SetCanonicalID overwrites the designer's internal ID. Used during
// handoff swap so the new instance assumes the canonical identity.
func (d *Designer) SetCanonicalID(id string) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	d.id = id
	d.state.ID = id
}

func (d *Designer) GetRoutingInfo() *guide.AgentRoutingInfo {
	return DesignerRoutingInfo(d.id)
}

func (d *Designer) PublishRequest(req *guide.RouteRequest) error {
	if !d.running {
		return fmt.Errorf("designer is not running")
	}

	req.SourceAgentID = d.id
	req.SourceAgentName = "designer"

	msg := guide.NewRequestMessage(d.generateMessageID(), req)
	return d.bus.Publish(guide.TopicGuideRequests, msg)
}

func (d *Designer) Skills() *skills.Registry {
	return d.skills
}

func (d *Designer) GetToolDefinitions() []map[string]any {
	return shared.ProviderToolsToDefinitions(d.buildToolDefinitions())
}

// =============================================================================
// ContainerAgent & HandoffInjectable Interface
// =============================================================================

// AgentID returns the unique instance identifier for this designer.
func (d *Designer) AgentID() string {
	return d.id
}

// AgentType returns the type classification for this agent.
func (d *Designer) AgentType() string {
	return "designer"
}

// Descriptor returns immutable metadata for the handoff system.
func (d *Designer) Descriptor() handoff.AgentDescriptor {
	modelID := d.CurrentModel()
	return handoff.AgentDescriptor{
		AgentType:             "designer",
		ModelID:               modelID,
		ReasoningEffort:       d.config.DesignerConfig.ReasoningEffort,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryPipeline,
		RuntimeProfiles:       designerRuntimeProfiles(),
		DefaultRuntimeProfile: designerDefaultRuntimeProfile(),
	}
}

// InjectPreparedContext accepts context from a handoff.
func (d *Designer) InjectPreparedContext(pc *handoff.PreparedContext) error {
	if pc == nil {
		return nil
	}
	if pipelineID, ok := pc.GetMetadata("pipeline_id"); ok && strings.TrimSpace(pipelineID) != "" {
		d.pipelineID = strings.TrimSpace(pipelineID)
	}
	if taskID, ok := pc.GetMetadata("task_id"); ok && strings.TrimSpace(taskID) != "" {
		d.pipelineID = strings.TrimSpace(taskID)
	}
	if taskSlug, ok := pc.GetMetadata("task_slug"); ok && strings.TrimSpace(taskSlug) != "" {
		d.pipelineSlug = strings.TrimSpace(taskSlug)
	}
	return nil
}

// ExtractArchivableState returns state for handoff persistence.
func (d *Designer) ExtractArchivableState() *handoff.ArchivableState {
	state := map[string]string{}
	if trimmed := strings.TrimSpace(d.pipelineID); trimmed != "" {
		state["pipeline_id"] = trimmed
		state["task_id"] = trimmed
	}
	if trimmed := strings.TrimSpace(d.pipelineSlug); trimmed != "" {
		state["task_slug"] = trimmed
	}
	return &handoff.ArchivableState{
		AgentID:   d.AgentID(),
		AgentType: d.AgentType(),
		State:     state,
		Timestamp: time.Now(),
	}
}

// SetAgentPod injects the agent pod for Scribe feed integration.
func (d *Designer) SetAgentPod(pod *shared.AgentPod) {
	d.agentPod = pod
}

// AgentPod returns the current task-scoped pod binding.
func (d *Designer) AgentPod() *shared.AgentPod {
	return d.agentPod
}

// SetHandoffBridge assigns the handoff bridge for turn recording.
func (d *Designer) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	d.handoffBridge = bridge
	if bridge != nil && d.activityPub != nil {
		bridge.SetActivityPublisher(d.activityPub)
	}
}

// SetFileAccess assigns the per-pipeline file access layer.
func (d *Designer) SetFileAccess(fa versioning.FileAccess) {
	d.fileAccess = authority.RestrictFileAccess("designer", fa)
}

// SetWorkspaceViews injects explicit disk/global/pipeline read access.
func (d *Designer) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	d.workspaceViews = authority.RestrictWorkspaceViews("designer", views)
}

// SetExecutionBroker overrides the strict execution broker.
func (d *Designer) SetExecutionBroker(broker purevfs.ExecutionBroker) {
	d.executionBroker = broker
}

// Terminate gracefully shuts down the designer agent.
func (d *Designer) Terminate(_ context.Context) error {
	return d.Stop()
}

// Compile-time interface verification.
var _ handoff.HandoffInjectable = (*Designer)(nil)
