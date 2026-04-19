package engineer

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
	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/escalation"
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

// MaxTodosBeforeArchitect is the scope limit - if more todos are required,
// stop and request Architect decomposition.
const MaxTodosBeforeArchitect = 12

// engineerProvider is the minimal interface the Engineer needs from its LLM.
// Satisfied by *providers.OpenAIProvider and *gateway.GatewayProvider.
type engineerProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

// Engineer is the code implementation specialist agent for the Sylk system.
// It uses GPT-5.4 Pro with xhigh reasoning to execute individual coding
// tasks via an LLM-driven tool loop.
type Engineer struct {
	id       string
	config   Config
	logger   *slog.Logger
	identity *identity.AgentIdentity
	factory  *identity.Factory

	// LLM provider
	provider  engineerProvider
	refresher container.ProviderRefresher

	// State management
	state    *EngineerState
	stateMu  sync.RWMutex
	failures map[string]*FailureRecord // taskID -> failure record

	// Skills system
	skills        *skills.Registry
	skillLoader   *skills.Loader
	tools         *toolruntime.Runtime
	toolDefsDirty bool

	// Activity publisher for UI agent-panel updates.
	activityPub events.ActivityPublisher

	// pipelineID tracks the stable task-level pipeline identity for the
	// current dispatch. It is derived from ForwardedRequest.Metadata["task_id"]
	// so the TUI can group sub-stage agents under the correct task pipeline.
	pipelineID   string
	pipelineSlug string
	pipelineName string

	// Event bus integration
	bus           guide.EventBus
	channels      *guide.AgentChannels
	requestSub    guide.Subscription
	responseSub   guide.Subscription
	registrySub   guide.Subscription
	running       bool
	knownAgentsMu sync.RWMutex
	knownAgents   map[string]*guide.AgentAnnouncement

	// Consultation tracking
	consultations []Consultation
	consultMu     sync.RWMutex

	// Synchronous consultation bus
	pendingMu       sync.Mutex
	pendingConsults map[string]*shared.PendingSyncWait

	// Handoff bridge
	handoffBridge *handoff.HandoffBridge

	// Agent pod for Scribe feed.
	agentPod *shared.AgentPod

	// File access abstraction (injected per-pipeline by Orchestrator).
	fileAccess      versioning.FileAccess
	workspaceViews  versioning.WorkspaceViewAccess
	executionBroker purevfs.ExecutionBroker

	// Refactor loop configuration
	refactorConfig shared.RefactorLoopConfig

	// Escalation
	escalator *escalation.Escalator

	// Request-scoped context lifecycle (mirrors architect pattern).
	runCtx         context.Context
	runCancel      context.CancelFunc
	requestMu      sync.Mutex
	requestCancels map[string]context.CancelFunc

	// Steering ledger management.
	steering *shared.SteeringManager
	// Tracks Memory Forest branches surfaced during implementation.
	forestTracker *shared.MemoryForestTracker

	// Request serialization: ensures at most one forwarded request
	// executes at a time, preventing cancel/new-request interleaving.
	requestSerializer *shared.RequestSerializer
}

// Config holds configuration for the Engineer agent
type Config struct {
	// Canonical agent ID. If empty, generates a UUID8 (pipeline use).
	ID string

	// System prompt configuration
	SystemPrompt    string // Optional, uses DefaultEngineerSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Engineer-specific configuration
	EngineerConfig EngineerConfig // Task execution configuration

	// ActivityPub publishes activity events so the UI agent panel tracks
	// this agent's lifecycle. Nil-safe (events silently dropped).
	ActivityPub events.ActivityPublisher

	// RequestGuard is called at handler entry to prevent activation demotion
	// during in-flight processing. Returns a release function. Nil-safe.
	RequestGuard func() func()

	// Logging
	Logger *slog.Logger // Optional, uses slog.Default() if nil

	// Session context
	SessionID string // Session identifier

	// Forest exposes Memory Forest precedent-recall skills.
	Forest shared.MemoryForestService

	// Factory mints the Engineer's AgentIdentity at New() time.
	// Pipeline agent, created by the orchestrator's task router inside
	// the pipeline pod after phase4.
	Factory *identity.Factory
}

// Default configuration values
const (
	DefaultMaxOutputTokens = 16384
	DefaultModel           = "gpt-5.4-pro"
	DefaultReasoningEffort = "xhigh"
	DefaultMaxToolRuns     = 32
	DefaultMaxTokens       = 16384
)

// New creates a new Engineer agent with the given LLM provider.
func New(cfg Config, provider engineerProvider) (*Engineer, error) {
	cfg = applyConfigDefaults(cfg)

	engineerID := cfg.ID
	if engineerID == "" {
		engineerID = uuid.New().String()[:8]
	}

	eng := &Engineer{
		id:              engineerID,
		config:          cfg,
		logger:          cfg.Logger,
		provider:        provider,
		activityPub:     cfg.ActivityPub,
		knownAgents:     make(map[string]*guide.AgentAnnouncement),
		failures:        make(map[string]*FailureRecord),
		pendingConsults: make(map[string]*shared.PendingSyncWait),
		refactorConfig:  shared.DefaultRefactorLoopConfig(),
		state: &EngineerState{
			ID:        engineerID,
			SessionID: cfg.SessionID,
			Status:    AgentStatusIdle,
			TaskQueue: make([]string, 0),
			StartedAt: time.Now(),
		},
		consultations:     make([]Consultation, 0),
		forestTracker:     shared.NewMemoryForestTracker(),
		steering:          shared.NewSteeringManager(),
		requestSerializer: shared.NewRequestSerializer(),
		executionBroker:   purevfs.DefaultExecutionBroker(),
	}

	eng.factory = cfg.Factory
	engineerIdentity, err := cfg.Factory.Mint(identity.MintOptions{
		Kind: identity.AgentTypeEngineer,
		Pod:  identity.PodRef{ID: "pipeline", Type: identity.PodTypePipeline},
	})
	if err != nil {
		return nil, fmt.Errorf("engineer: mint identity: %w", err)
	}
	eng.identity = engineerIdentity

	eng.steering.InitLazy("engineer", nil)

	if err := eng.initSkills(); err != nil {
		return nil, err
	}

	return eng, nil
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (e *Engineer) SetProvider(p engineerProvider) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.provider = p
}

// SetProviderRefresher stores a callback that creates a fresh provider for
// the current model and auth method. Set by cmd/tui.go at bootstrap.
func (e *Engineer) SetProviderRefresher(fn container.ProviderRefresher) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.refresher = fn
}

// getProvider returns the current provider under read lock.
func (e *Engineer) getProvider() engineerProvider {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return e.provider
}

// Ready implements shared.ReadinessReporter.
func (e *Engineer) Ready() (bool, string) {
	if e.getProvider() == nil {
		return false, "LLM provider not yet wired (post-init / pre-auth window)"
	}
	return true, ""
}

// ProviderType implements container.AuthRefreshable.
func (e *Engineer) ProviderType() string {
	return string(container.ProviderForModel(e.CurrentModel()))
}

// RefreshProvider implements container.AuthRefreshable.
func (e *Engineer) RefreshProvider(ctx context.Context, authMethod string) error {
	e.stateMu.RLock()
	fn := e.refresher
	e.stateMu.RUnlock()
	if fn == nil {
		return nil
	}
	p, err := fn(ctx, e.CurrentModel(), authMethod)
	if err != nil {
		return fmt.Errorf("engineer refresh provider: %w", err)
	}
	e.SetProvider(p)
	e.logger.Info("provider refreshed", "model", e.CurrentModel(), "auth_method", authMethod)
	return nil
}

// SwapModel implements container.ModelSwappable.
// Re-creates the OpenAI provider with the given model ID, re-applying the
// gateway wrapper. Thread-safe via stateMu.
func (e *Engineer) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	e.SetProvider(provider)
	e.stateMu.Lock()
	e.config.EngineerConfig.Model = modelID
	e.stateMu.Unlock()
	e.logger.Info("model swapped", "model", modelID)
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (e *Engineer) CurrentModel() string {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	if e.config.EngineerConfig.Model != "" {
		return e.config.EngineerConfig.Model
	}
	return DefaultModel
}

// SupportedModels implements container.ModelSwappable.
func (e *Engineer) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
	}
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultEngineerSystemPrompt
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.EngineerConfig.Model == "" {
		cfg.EngineerConfig.Model = DefaultModel
	}
	if cfg.EngineerConfig.ReasoningEffort == "" {
		cfg.EngineerConfig.ReasoningEffort = DefaultReasoningEffort
	}
	if cfg.EngineerConfig.MaxToolRuns == 0 {
		cfg.EngineerConfig.MaxToolRuns = DefaultMaxToolRuns
	}
	if cfg.EngineerConfig.MaxTokens == 0 {
		cfg.EngineerConfig.MaxTokens = DefaultMaxTokens
	}
	if cfg.EngineerConfig.CommandTimeout == 0 {
		cfg.EngineerConfig.CommandTimeout = 30 * time.Second
	}
	if cfg.EngineerConfig.MaxConcurrentTasks == 0 {
		cfg.EngineerConfig.MaxConcurrentTasks = 1
	}
	if !cfg.EngineerConfig.EnableFileWrites {
		cfg.EngineerConfig.EnableFileWrites = true
	}
	if !cfg.EngineerConfig.EnableCommands {
		cfg.EngineerConfig.EnableCommands = true
	}
	if cfg.EngineerConfig.MemoryThreshold.CheckpointThreshold == 0 {
		cfg.EngineerConfig.MemoryThreshold = DefaultMemoryThreshold()
	}
	if len(cfg.EngineerConfig.ApprovedCommands.Patterns) == 0 {
		cfg.EngineerConfig.ApprovedCommands = DefaultApprovedPatterns()
	}
	if cfg.EngineerConfig.SessionID == "" {
		cfg.EngineerConfig.SessionID = cfg.SessionID
	}
	return cfg
}

func (e *Engineer) initSkills() error {
	e.skills = skills.NewRegistry()
	e.registerCoreSkills()
	if err := shared.RegisterMemoryForestSkills(e.skills, "engineer", e.config.Forest, e.forestTracker); err != nil {
		return fmt.Errorf("register engineer forest skills: %w", err)
	}
	if err := shared.AttachForestOutcomeRecorder(
		e.skills,
		"handoff_next",
		e.forestTracker,
		e.config.Forest,
		func() string { return e.id },
		"engineer",
		func() string { return e.config.SessionID },
		shared.OutcomeOnSuccess("engineer implementation handoff succeeded"),
	); err != nil {
		return fmt.Errorf("attach engineer forest handoff outcome: %w", err)
	}
	if err := shared.AttachForestOutcomeRecorder(
		e.skills,
		"report_confidence",
		e.forestTracker,
		e.config.Forest,
		func() string { return e.id },
		"engineer",
		func() string { return e.config.SessionID },
		shared.OutcomeAlways(forest.OutcomeStatusMixed, "engineer reported implementation confidence"),
	); err != nil {
		return fmt.Errorf("attach engineer forest confidence outcome: %w", err)
	}

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = engineerVisibleSkillNames()
	loaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	e.skillLoader = skills.NewLoader(e.skills, loaderCfg)

	tools, err := toolruntime.New(toolruntime.Config{
		Registry: e.skills,
		Manifest: engineerToolManifest(e.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return fmt.Errorf("initialize engineer tool runtime: %w", err)
	}
	e.tools = tools
	e.tools.SyncActiveFromLoaded()
	return nil
}

// ID returns the engineer's unique identifier
func (e *Engineer) ID() string {
	return e.id
}

// SetCanonicalID overwrites the engineer's internal ID. Used during
// handoff swap so the new instance assumes the canonical identity.
func (e *Engineer) SetCanonicalID(id string) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.id = id
	e.state.ID = id
}

// Close closes the engineer and its resources
func (e *Engineer) Close() error {
	if e.tools != nil {
		e.tools.Close()
		e.tools = nil
	}
	e.Stop()
	return nil
}

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// The engineer subscribes to its own channels and the registry topic.
func (e *Engineer) Start(bus guide.EventBus) error {
	if e.running {
		return fmt.Errorf("engineer is already running")
	}

	e.bus = bus
	e.channels = guide.NewAgentChannels("engineer", e.id)

	// Subscribe to own request channel (engineer.requests)
	var err error
	e.requestSub, err = bus.SubscribeAsync(e.channels.Requests, e.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", e.channels.Requests, err)
	}

	// Subscribe to own response channel (for replies to requests we make)
	e.responseSub, err = bus.SubscribeAsync(e.channels.Responses, e.handleBusResponse)
	if err != nil {
		e.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", e.channels.Responses, err)
	}

	// Subscribe to agent registry for announcements
	e.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, e.handleRegistryAnnouncement)
	if err != nil {
		e.requestSub.Unsubscribe()
		e.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	e.runCtx, e.runCancel = context.WithCancel(context.Background())
	e.requestCancels = make(map[string]context.CancelFunc)
	e.running = true
	e.logger.Info("engineer started", "id", e.id, "channels", e.channels)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (e *Engineer) Stop() error {
	if !e.running {
		return nil
	}

	e.steering.CloseAll()
	if e.runCancel != nil {
		e.runCancel()
	}
	errs := e.unsubscribeAll()
	e.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	e.logger.Info("engineer stopped", "id", e.id)
	return nil
}

func (e *Engineer) unsubscribeAll() []error {
	var errs []error
	if err := e.unsubscribeRequest(); err != nil {
		errs = append(errs, err)
	}
	if err := e.unsubscribeResponse(); err != nil {
		errs = append(errs, err)
	}
	if err := e.unsubscribeRegistry(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (e *Engineer) unsubscribeRequest() error {
	if e.requestSub == nil {
		return nil
	}
	err := e.requestSub.Unsubscribe()
	e.requestSub = nil
	return err
}

func (e *Engineer) unsubscribeResponse() error {
	if e.responseSub == nil {
		return nil
	}
	err := e.responseSub.Unsubscribe()
	e.responseSub = nil
	return err
}

func (e *Engineer) unsubscribeRegistry() error {
	if e.registrySub == nil {
		return nil
	}
	err := e.registrySub.Unsubscribe()
	e.registrySub = nil
	return err
}

// IsRunning returns true if the engineer is actively processing bus messages
func (e *Engineer) IsRunning() bool {
	return e.running
}

// Bus returns the event bus used by the engineer
func (e *Engineer) Bus() guide.EventBus {
	return e.bus
}

// Channels returns the engineer's channel configuration
func (e *Engineer) Channels() *guide.AgentChannels {
	return e.channels
}

// =============================================================================
// Request Handling
// =============================================================================

// handleBusRequest processes incoming forwarded requests from the event bus
func (e *Engineer) handleBusRequest(msg *guide.Message) error {
	if msg.Type == guide.MessageTypeAction {
		return e.handleActionMessage(msg)
	}
	if msg.Type != guide.MessageTypeForward {
		return nil // Ignore non-forward messages
	}

	if !e.requestSerializer.Acquire(e.runCtx) {
		return nil // parent context done, agent shutting down
	}
	defer e.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	e.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	shared.LogIncomingRequest(e.steering.EventLogger(), fwd, e.id)

	// Track pipeline association for activity event grouping.
	if taskID, _ := fwd.Metadata["task_id"].(string); taskID != "" {
		e.pipelineID = taskID
	}
	if taskSlug, _ := fwd.Metadata["task_slug"].(string); taskSlug != "" {
		e.pipelineSlug = taskSlug
	}
	if taskName, _ := fwd.Metadata["task_name"].(string); taskName != "" {
		e.pipelineName = taskName
	}

	shared.EmitDispatchACK(e.bus, fwd.Metadata, e.id, "engineer", fwd.CorrelationID)
	e.publishActivity(events.EventTypeAgentAction, "Processing implementation task")

	if e.config.RequestGuard != nil {
		release := e.config.RequestGuard()
		defer release()
	}

	// Process the request with a cancellable request-scoped context.
	reqCtx, cancel := context.WithCancel(e.runCtx)
	reqCtx = versioning.WithSessionID(reqCtx, fwd.SessionID)
	reqCtx = shared.WithForwardedTaskScope(reqCtx, fwd.Metadata)
	reqCtx = shared.WithGuardianCommandGate(reqCtx, shared.GuardianCommandGateConfig{
		BusProvider:     func() guide.EventBus { return e.bus },
		SourceAgentID:   func() string { return e.id },
		SourceAgentType: "engineer",
		SourceAgentName: "Engineer",
	})
	e.registerRequestCancel(fwd.CorrelationID, cancel)
	e.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer e.clearRequestCancel(fwd.CorrelationID)
	defer cancel()

	if e.factory != nil && e.identity != nil {
		pipelineID, _ := fwd.Metadata["task_id"].(string)
		var pipeline *identity.PipelineRef
		if pipelineID != "" {
			pipeline = &identity.PipelineRef{
				ID:    identity.PipelineID(pipelineID),
				Stage: "implement",
			}
		}
		task, taskErr := e.factory.NewTask(identity.TaskOptions{
			DisplayID:   fwd.CorrelationID,
			Correlation: identity.CorrelationID(fwd.CorrelationID),
			Pipeline:    pipeline,
		})
		if taskErr != nil {
			return fmt.Errorf("engineer: mint task: %w", taskErr)
		}
		reqCtx = identity.WithIdentity(reqCtx, e.identity)
		reqCtx = identity.WithTask(reqCtx, task)
	}

	// Create steering ledger for this request.
	ledger := e.steering.Create(fwd.CorrelationID, e.id, fwd.SessionID, nil, nil)
	defer e.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)

	startTime := time.Now()

	// Wire tool call emitter for inline visualization.
	emitter := shared.NewToolCallEmitter(e.bus, e.channels, e.id, fwd.CorrelationID, fwd.SourceAgentID)
	metaString := func(key string) string {
		if fwd.Metadata == nil {
			return ""
		}
		value, _ := fwd.Metadata[key].(string)
		return strings.TrimSpace(value)
	}
	ctx := shared.WithForwardedStreamContext(reqCtx, fwd.CorrelationID, fwd.SourceAgentID, fwd.ParentCorrelationID, shared.MergeStreamMetadata(fwd.Metadata, map[string]any{
		"pipeline_task": true,
		"pipeline_id":   e.pipelineID,
		"task_id":       e.pipelineID,
		"task_slug":     e.pipelineSlug,
		"task_name":     metaString("task_name"),
		"dag_id":        metaString("dag_id"),
		"node_id":       metaString("node_id"),
	}))
	ctx = shared.WithOwnedStreamIdentity(ctx, "engineer", "Engineer")
	ctx, usageAcc := shared.WithUsageAccumulator(ctx)
	ctx = shared.WithToolCallEmitter(ctx, emitter)
	ctx = shared.WithSteeringLedger(ctx, ledger)
	ctx = shared.WithLogMeta(ctx, shared.LogMeta{
		EventLogger: e.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     e.id,
		SessionID:   fwd.SessionID,
	})
	allowedHandoff := shared.AutomaticHandoffAllowedForForwardedRequest(fwd)
	ctx = shared.WithAutomaticHandoffEnabled(ctx, allowedHandoff)
	ctx = handoff.WithTransportRetryHandoff(ctx, handoff.TransportRetryHandoffConfig{
		Enabled:       allowedHandoff,
		Bridge:        shared.EffectiveHandoffBridge(ctx, e.handoffBridge),
		AgentID:       e.id,
		AgentType:     "engineer",
		UserRequest:   fwd.Input,
		CorrelationID: fwd.CorrelationID,
		SessionID:     fwd.SessionID,
		EventLogger:   e.steering.EventLogger(),
		Scribe:        e.agentPod,
	})
	gov := shared.NewContextGovernor(
		e.config.EngineerConfig.Model, e.config.EngineerConfig.MaxTokens, 0,
	)
	if e.handoffBridge != nil && shared.AutomaticHandoffEnabled(ctx) {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			bridge := shared.EffectiveHandoffBridge(bctx, e.handoffBridge)
			if bridge == nil {
				return shared.ErrContextBudgetExhausted
			}
			return bridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	ctx = shared.WithContextGovernor(ctx, gov)
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus: e.bus, Channels: e.channels,
		AgentID: e.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})
	protocolTask := shared.DecodePipelineTaskInput(fwd.Input)
	hadProtocolState := shared.PipelineProtocolStateFromContext(ctx) != nil
	ctx = shared.WithPipelineTaskProtocolState(ctx, protocolTask)
	if !hadProtocolState {
		defer shared.ClosePipelineProtocolState(ctx)
	}
	publishStreamLifecycle := guide.ShouldPublishForwardedStreamLifecycle(fwd)
	if publishStreamLifecycle {
		shared.PublishStreamStart(e.bus, e.channels, ctx, e.id)
		if pp := shared.ProgressPublisherFromContext(ctx); pp != nil {
			pp.Publish("Reviewing task criteria, active test failures, and task-local workspace before implementing changes.")
		}
	}

	result, err := e.processForwardedRequest(ctx, fwd)
	shared.LogResponse(e.steering.EventLogger(), fwd.CorrelationID, e.id, fwd.SessionID, time.Since(startTime), err)

	if err != nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		if publishStreamLifecycle {
			shared.PublishStreamError(e.bus, e.channels, ctx, e.id, err)
			shared.PublishStreamComplete(e.bus, e.channels, ctx, e.id, "", usageAcc.Total())
		}
		if fwd.FireAndForget {
			return nil
		}
		e.publishActivity(events.EventTypeAgentError, fmt.Sprintf("Task failed: %s", err.Error()))
		errMsg := guide.NewErrorMessage(
			e.generateMessageID(),
			fwd.CorrelationID,
			e.id,
			err.Error(),
		)
		return e.bus.Publish(e.channels.Errors, errMsg)
	}

	respData := result
	if protocolTask != nil {
		respData = shared.BuildPipelineTurnResponse(ctx, result)
	}
	if publishStreamLifecycle {
		shared.PublishStreamComplete(e.bus, e.channels, ctx, e.id, "", usageAcc.Total())
	}
	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             true,
		RespondingAgentID:   e.id,
		RespondingAgentName: "Engineer",
		ProcessingTime:      time.Since(startTime),
		Data:                respData,
	}
	e.publishActivity(events.EventTypeSuccess, "Implementation task completed")

	respMsg := guide.NewResponseMessage(e.generateMessageID(), resp)
	if e.agentPod != nil {
		e.agentPod.FeedScribe("engineer", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}
	return e.bus.Publish(e.channels.Responses, respMsg)
}

func (e *Engineer) generateMessageID() string {
	return fmt.Sprintf("engineer_msg_%s", uuid.New().String())
}

func (e *Engineer) registerRequestCancel(correlationID string, cancel context.CancelFunc) {
	e.requestMu.Lock()
	if e.requestCancels != nil {
		e.requestCancels[correlationID] = cancel
	}
	e.requestMu.Unlock()
}

func (e *Engineer) clearRequestCancel(correlationID string) {
	e.requestMu.Lock()
	delete(e.requestCancels, correlationID)
	e.requestMu.Unlock()
}

func (e *Engineer) cancelRequest(correlationID string) {
	e.requestMu.Lock()
	cancel := e.requestCancels[correlationID]
	delete(e.requestCancels, correlationID)
	e.requestMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *Engineer) handleActionMessage(msg *guide.Message) error {
	action, ok := msg.GetActionRequest()
	if !ok || action == nil {
		return nil
	}
	if e.steering.HandleAction(action) {
		return nil
	}
	if action.Action == "cancel" {
		shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventError,
			e.id, "", action.CorrelationID, "warn", &agentlog.ErrorPayload{Error: "request cancelled via action"})
		e.cancelRequest(action.CorrelationID)
	}
	return nil
}

// processForwardedRequest handles the actual request processing
func (e *Engineer) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := e.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (e *Engineer) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentComplete:
		return e.handleImplement, nil
	default:
		// Default to implementation for any coding task
		return e.handleImplement, nil
	}
}

// handleImplement processes implementation requests (coding tasks)
func (e *Engineer) handleImplement(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	taskID := uuid.New().String()
	prompt := fwd.Input
	var pipelineTask *shared.PipelineTaskInput
	if task := shared.DecodePipelineTaskInput(fwd.Input); task != nil {
		pipelineTask = task
		if strings.TrimSpace(task.TaskID) != "" {
			taskID = task.TaskID
		}
		prompt = shared.ComposePipelineTaskUserPrompt(task)
		if workspaceContext := shared.BuildTaskWorkspaceRuntimeContext(ctx, e.workspaceViews, task); workspaceContext != "" {
			prompt += "\n\n" + workspaceContext
		}
	}

	// Create task request
	req := &EngineerRequest{
		ID:                  uuid.New().String(),
		Intent:              IntentComplete,
		TaskID:              taskID,
		Prompt:              prompt,
		PipelineTask:        pipelineTask,
		ConversationHistory: fwd.ConversationHistory,
		EngineerID:          e.id,
		SessionID:           e.config.SessionID,
		Timestamp:           time.Now(),
	}

	// Handle the task using the composed engineer prompt and tool loop.
	return e.Handle(ctx, req)
}

// handleBusResponse processes responses to requests we made.
// Delivers to synchronous consultation waiters.
func (e *Engineer) handleBusResponse(msg *guide.Message) error {
	e.deliverConsultResponse(msg)
	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events
func (e *Engineer) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	e.knownAgentsMu.Lock()
	defer e.knownAgentsMu.Unlock()

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		e.knownAgents[ann.AgentID] = ann
		e.logger.Debug("agent registered", "agent_id", ann.AgentID)
		shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventRegistryEvent,
			e.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "registered",
			})
	case guide.MessageTypeAgentUnregistered:
		delete(e.knownAgents, ann.AgentID)
		e.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
		shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventRegistryEvent,
			e.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "unregistered",
			})
	}

	return nil
}

// GetKnownAgents returns all agents the engineer knows about
func (e *Engineer) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	e.knownAgentsMu.RLock()
	defer e.knownAgentsMu.RUnlock()
	result := make(map[string]*guide.AgentAnnouncement, len(e.knownAgents))
	for k, v := range e.knownAgents {
		result[k] = v
	}
	return result
}

// =============================================================================
// LLM-Driven Implementation Protocol
// =============================================================================

// Handle processes an EngineerRequest using the LLM-driven implementation protocol.
func (e *Engineer) Handle(ctx context.Context, req *EngineerRequest) (_ *EngineerResponse, retErr error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}
	hadProtocolState := shared.PipelineProtocolStateFromContext(ctx) != nil
	ctx = shared.WithPipelineTaskProtocolState(ctx, req.PipelineTask)
	if !hadProtocolState {
		defer shared.ClosePipelineProtocolState(ctx)
	}
	ctx = shared.WithPipelineTurnBaseline(ctx)

	startTime := time.Now()
	e.setStatus(AgentStatusBusy)
	defer e.setStatus(AgentStatusIdle)

	// Step 1: Validate scope
	if err := e.validateTaskScope(ctx, req); err != nil {
		shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventError,
			e.id, "", "", "error", &agentlog.ErrorPayload{Error: err.Error()})
		retErr = err
		return e.failureResponse(req, err, startTime)
	}

	// Step 2: Compose system prompt
	contract := shared.BuildTaskExecutionContract(req.PipelineTask)
	systemPrompt := e.systemPromptForContract(contract)
	if req.PipelineTask != nil {
		systemPrompt = shared.AppendPipelineSystemContext(systemPrompt, req.PipelineTask)
	}

	// Step 3: Build LLM request with tools
	e.prepareSkillsForInput(req.Prompt)
	surface := e.toolRuntime()
	ctx = shared.WithTaskExecutionContract(ctx, contract)
	ctx = shared.WithTaskExecutionState(ctx, shared.NewTaskExecutionState())
	llmReq := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: req.Prompt}},
		Tools:        e.buildToolDefinitionsWithSurface(surface),
		Model:        e.config.EngineerConfig.Model,
		MaxTokens:    e.config.EngineerConfig.MaxTokens,
	}
	e.applyLLMRuntimeProfile(llmReq, "implementation")

	// Prepend conversation history as multi-turn message pairs.
	shared.PrependHistoryMessages(llmReq, req.ConversationHistory)

	// Step 4: Execute tool loop
	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return e.executeToolLoopWithSurface(ctx, llmReq, ledger, surface)
	})
	if err != nil {
		shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventError,
			e.id, "", "", "error", &agentlog.ErrorPayload{Error: err.Error()})
		e.recordFailure(req.TaskID, err.Error(), req.Prompt)
		retErr = err
		return e.failureResponse(req, err, startTime)
	}

	shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventGenerationCompleted,
		e.id, "", "", "info", &agentlog.GenerationPayload{Phase: "completed"})

	return &EngineerResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   true,
		Result: &TaskResult{
			TaskID:       req.TaskID,
			Success:      true,
			Output:       result,
			Duration:     time.Since(startTime),
			FilesChanged: make([]FileChange, 0),
		},
		Timestamp: time.Now(),
	}, nil
}

func (e *Engineer) systemPromptForContract(contract *shared.TaskExecutionContract) string {
	if contract == nil {
		return e.config.SystemPrompt
	}
	if strings.TrimSpace(e.config.SystemPrompt) == "" || e.config.SystemPrompt == DefaultEngineerSystemPrompt {
		return EngineerSystemPromptForContract(contract)
	}
	return e.config.SystemPrompt
}

// =============================================================================
// Protocol Helpers
// =============================================================================

func (e *Engineer) validateTaskScope(_ context.Context, req *EngineerRequest) error {
	if req.Prompt == "" {
		return fmt.Errorf("task prompt is required")
	}
	return nil
}

func (e *Engineer) failureResponse(req *EngineerRequest, err error, _ time.Time) (*EngineerResponse, error) {
	if req != nil && req.PipelineTask != nil && err != nil {
		shared.PublishPipelineTaskTerminalErrorUpdate(e.bus, e.id, req.PipelineTask, err, shared.PipelineTaskAttempt(req.PipelineTask))
	}
	return &EngineerResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   false,
		Error:     err.Error(),
		Timestamp: time.Now(),
	}, err
}

// isTesterAvailable checks if any known agent has a tester type.
func (e *Engineer) isTesterAvailable() bool {
	e.knownAgentsMu.RLock()
	defer e.knownAgentsMu.RUnlock()
	for _, ann := range e.knownAgents {
		if ann.AgentType == "tester" || ann.AgentType == "tester-pipeline" {
			return true
		}
	}
	return false
}

// =============================================================================
// State Management
// =============================================================================

func (e *Engineer) setStatus(status AgentStatus) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.state.Status = status
	e.state.LastActiveAt = time.Now()
	shared.LogStatusUpdate(e.steering.EventLogger(), e.id, "", string(status))
}

// publishActivity emits a user-visible activity event so the UI agent panel
// tracks this engineer's lifecycle.
func (e *Engineer) publishActivity(eventType events.EventType, content string) {
	if e.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, e.config.SessionID, content)
	evt.AgentID = e.id
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "engineer"
	evt.Data["agent_name"] = "Engineer"
	if e.pipelineID != "" {
		evt.Data["pipeline_id"] = e.pipelineID
		evt.Data["task_id"] = e.pipelineID
	}
	if e.pipelineSlug != "" {
		evt.Data["task_slug"] = e.pipelineSlug
	}
	e.activityPub.PublishActivity(evt)
}

func (e *Engineer) recordFailure(taskID, errorMsg, approach string) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()

	existing, ok := e.failures[taskID]
	if ok {
		existing.AttemptCount++
		existing.LastError = errorMsg
		existing.Timestamp = time.Now()
	} else {
		e.failures[taskID] = &FailureRecord{
			TaskID:       taskID,
			EngineerID:   e.id,
			AttemptCount: 1,
			LastError:    errorMsg,
			Approach:     approach,
			Timestamp:    time.Now(),
		}
	}

	e.state.FailedCount++
}

func (e *Engineer) recordConsultation(c Consultation) {
	e.consultMu.Lock()
	defer e.consultMu.Unlock()
	e.consultations = append(e.consultations, c)
}

// GetState returns the current engineer state
func (e *Engineer) GetState() *EngineerState {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()

	// Return a copy
	stateCopy := *e.state
	return &stateCopy
}

// GetConsultations returns all recorded consultations
func (e *Engineer) GetConsultations() []Consultation {
	e.consultMu.RLock()
	defer e.consultMu.RUnlock()

	result := make([]Consultation, len(e.consultations))
	copy(result, e.consultations)
	return result
}

// =============================================================================
// Guide Registration
// =============================================================================

// GetRoutingInfo returns the engineer's routing information for Guide registration
func (e *Engineer) GetRoutingInfo() *guide.AgentRoutingInfo {
	return EngineerRoutingInfo(e.id)
}

// PublishRequest publishes a request to the Guide for routing
func (e *Engineer) PublishRequest(req *guide.RouteRequest) error {
	if !e.running {
		return fmt.Errorf("engineer is not running")
	}

	req.SourceAgentID = e.id
	req.SourceAgentName = "engineer"

	msg := guide.NewRequestMessage(e.generateMessageID(), req)
	return e.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// Skills System
// =============================================================================

// Skills returns the engineer's skill registry
func (e *Engineer) Skills() *skills.Registry {
	return e.skills
}

// GetToolDefinitions returns tool definitions for all loaded skills
func (e *Engineer) GetToolDefinitions() []map[string]any {
	return shared.ProviderToolsToDefinitions(e.buildToolDefinitions())
}

// =============================================================================
// HandoffInjectable Interface
// =============================================================================

// AgentID returns the unique instance identifier for this engineer.
func (e *Engineer) AgentID() string {
	return e.id
}

// AgentType returns the type classification for this agent.
func (e *Engineer) AgentType() string {
	return "engineer"
}

// Descriptor returns the agent's descriptor for handoff operations.
func (e *Engineer) Descriptor() handoff.AgentDescriptor {
	modelID := e.CurrentModel()
	return handoff.AgentDescriptor{
		AgentType:             "engineer",
		ModelID:               modelID,
		ReasoningEffort:       e.config.EngineerConfig.ReasoningEffort,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryPipeline,
		RuntimeProfiles:       engineerRuntimeProfiles(),
		DefaultRuntimeProfile: engineerDefaultRuntimeProfile(),
	}
}

// InjectPreparedContext accepts a handoff context.
func (e *Engineer) InjectPreparedContext(pc *handoff.PreparedContext) error {
	if pc == nil {
		return nil
	}
	if pipelineID, ok := pc.GetMetadata("pipeline_id"); ok && strings.TrimSpace(pipelineID) != "" {
		e.pipelineID = strings.TrimSpace(pipelineID)
	}
	if taskID, ok := pc.GetMetadata("task_id"); ok && strings.TrimSpace(taskID) != "" {
		e.pipelineID = strings.TrimSpace(taskID)
	}
	if taskSlug, ok := pc.GetMetadata("task_slug"); ok && strings.TrimSpace(taskSlug) != "" {
		e.pipelineSlug = strings.TrimSpace(taskSlug)
	}
	return nil
}

// SetAgentPod injects the agent pod for Scribe feed integration.
func (e *Engineer) SetAgentPod(pod *shared.AgentPod) {
	e.agentPod = pod
}

// AgentPod returns the current task-scoped pod binding.
func (e *Engineer) AgentPod() *shared.AgentPod {
	return e.agentPod
}

// SetHandoffBridge sets the handoff bridge for this engineer.
func (e *Engineer) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	e.handoffBridge = bridge
	if bridge != nil && e.activityPub != nil {
		bridge.SetActivityPublisher(e.activityPub)
	}
}

// SetFileAccess injects the FileAccess implementation for this pipeline.
// Called by the Orchestrator when dispatching the engineer to a pipeline.
func (e *Engineer) SetFileAccess(fa versioning.FileAccess) {
	e.fileAccess = authority.RestrictFileAccess("engineer", fa)
}

// SetWorkspaceViews injects explicit disk/global/pipeline read access.
func (e *Engineer) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	e.workspaceViews = authority.RestrictWorkspaceViews("engineer", views)
}

// SetExecutionBroker overrides the strict execution broker.
func (e *Engineer) SetExecutionBroker(broker purevfs.ExecutionBroker) {
	e.executionBroker = broker
}

// SetEscalator injects the confidence-based escalation evaluator.
func (e *Engineer) SetEscalator(esc *escalation.Escalator) {
	e.escalator = esc
}

// ExtractArchivableState returns the engineer's archivable state.
func (e *Engineer) ExtractArchivableState() *handoff.ArchivableState {
	state := map[string]string{}
	if trimmed := strings.TrimSpace(e.pipelineID); trimmed != "" {
		state["pipeline_id"] = trimmed
		state["task_id"] = trimmed
	}
	if trimmed := strings.TrimSpace(e.pipelineSlug); trimmed != "" {
		state["task_slug"] = trimmed
	}
	return &handoff.ArchivableState{
		AgentID:   e.id,
		AgentType: "engineer",
		State:     state,
		Timestamp: time.Now(),
	}
}

// Terminate gracefully shuts down the engineer agent.
func (e *Engineer) Terminate(_ context.Context) error {
	return e.Stop()
}
