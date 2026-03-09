package engineer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/escalation"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// MaxTodosBeforeArchitect is the scope limit - if more todos are required,
// stop and request Architect decomposition.
const MaxTodosBeforeArchitect = 12

// MaxAttemptsBeforeConsultation is the failure count threshold that triggers
// Academic consultation for alternative approaches. Derived from the audit
// config's MaxAuditIterations — if the agent can self-audit N times,
// escalation to Academic occurs after N failures of the full cycle.
var MaxAttemptsBeforeConsultation = DefaultAuditConfig().MaxAuditIterations

// engineerProvider is the minimal interface the Engineer needs from its LLM.
// Satisfied by *providers.OpenAIProvider and *gateway.GatewayProvider.
type engineerProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

// Engineer is the code implementation specialist agent for the Sylk system.
// It uses GPT-5.4 Pro with xhigh reasoning to execute individual coding
// tasks via an LLM-driven tool loop with self-audit.
type Engineer struct {
	id     string
	config Config
	logger *slog.Logger

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

	// Event bus integration
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement

	// Consultation tracking
	consultations []Consultation
	consultMu     sync.RWMutex

	// Synchronous consultation bus
	pendingMu       sync.Mutex
	pendingConsults map[string]chan *guide.Message

	// Handoff bridge
	handoffBridge *handoff.HandoffBridge

	// Agent pod for Scribe feed.
	agentPod *shared.AgentPod

	// File access abstraction (injected per-pipeline by Orchestrator).
	fileAccess versioning.FileAccess

	// Self-audit configuration
	auditConfig AuditConfig

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
		pendingConsults: make(map[string]chan *guide.Message),
		auditConfig:     DefaultAuditConfig(),
		refactorConfig:  shared.DefaultRefactorLoopConfig(),
		state: &EngineerState{
			ID:        engineerID,
			SessionID: cfg.SessionID,
			Status:    AgentStatusIdle,
			TaskQueue: make([]string, 0),
			StartedAt: time.Now(),
		},
		consultations:     make([]Consultation, 0),
		steering:          shared.NewSteeringManager(),
		requestSerializer: shared.NewRequestSerializer(),
	}

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
	} else if dagID, _ := fwd.Metadata["dag_id"].(string); dagID != "" {
		e.pipelineID = dagID
	}
	if taskSlug, _ := fwd.Metadata["task_slug"].(string); taskSlug != "" {
		e.pipelineSlug = taskSlug
	}

	shared.EmitDispatchACK(e.bus, fwd.Metadata, e.id, "engineer", fwd.CorrelationID)
	e.publishActivity(events.EventTypeAgentAction, "Processing implementation task")

	if e.config.RequestGuard != nil {
		release := e.config.RequestGuard()
		defer release()
	}

	// Process the request with a cancellable request-scoped context.
	reqCtx, cancel := context.WithCancel(e.runCtx)
	e.registerRequestCancel(fwd.CorrelationID, cancel)
	e.steering.RegisterCancel(fwd.CorrelationID, cancel)
	defer e.clearRequestCancel(fwd.CorrelationID)
	defer cancel()

	// Create steering ledger for this request.
	ledger := e.steering.Create(fwd.CorrelationID, e.id, fwd.SessionID, nil, nil)
	defer e.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)

	startTime := time.Now()

	// Wire tool call emitter for inline visualization.
	emitter := shared.NewToolCallEmitter(e.bus, e.channels, e.id, fwd.CorrelationID, fwd.SourceAgentID)
	ctx := shared.WithStreamContext(reqCtx, fwd.CorrelationID, fwd.SourceAgentID)
	ctx = shared.WithStreamContextMetadata(ctx, map[string]any{
		"agent_type":  "engineer",
		"agent_name":  "Engineer",
		"pipeline_id": e.pipelineID,
		"task_id":     e.pipelineID,
		"task_slug":   e.pipelineSlug,
	})
	ctx, usageAcc := shared.WithUsageAccumulator(ctx)
	ctx = shared.WithToolCallEmitter(ctx, emitter)
	ctx = shared.WithSteeringLedger(ctx, ledger)
	ctx = shared.WithLogMeta(ctx, shared.LogMeta{
		EventLogger: e.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     e.id,
		SessionID:   fwd.SessionID,
	})
	gov := shared.NewContextGovernor(
		e.config.EngineerConfig.Model, e.config.EngineerConfig.MaxTokens, 0,
	)
	if e.handoffBridge != nil {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			return e.handoffBridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	ctx = shared.WithContextGovernor(ctx, gov)
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus: e.bus, Channels: e.channels,
		AgentID: e.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})
	if !fwd.FireAndForget {
		shared.PublishStreamStart(e.bus, e.channels, ctx, e.id)
	}

	result, err := e.processForwardedRequest(ctx, fwd)
	shared.LogResponse(e.steering.EventLogger(), fwd.CorrelationID, e.id, fwd.SessionID, time.Since(startTime), err)

	// Don't respond if fire-and-forget
	if fwd.FireAndForget {
		return nil
	}

	// Build response
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   e.id,
		RespondingAgentName: "Engineer",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		shared.PublishStreamError(e.bus, e.channels, ctx, e.id, err)
		shared.PublishStreamComplete(e.bus, e.channels, ctx, e.id, "", usageAcc.Total())
		resp.Error = err.Error()
		e.publishActivity(events.EventTypeAgentError, fmt.Sprintf("Task failed: %s", err.Error()))
		errMsg := guide.NewErrorMessage(
			e.generateMessageID(),
			fwd.CorrelationID,
			e.id,
			err.Error(),
		)
		return e.bus.Publish(e.channels.Errors, errMsg)
	}

	resp.Data = result
	shared.PublishStreamComplete(e.bus, e.channels, ctx, e.id, "", usageAcc.Total())
	e.publishActivity(events.EventTypeAgentAction, "Implementation task completed")

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

	// Create task request
	req := &EngineerRequest{
		ID:                  uuid.New().String(),
		Intent:              IntentComplete,
		TaskID:              taskID,
		Prompt:              fwd.Input,
		ConversationHistory: fwd.ConversationHistory,
		EngineerID:          e.id,
		SessionID:           e.config.SessionID,
		Timestamp:           time.Now(),
	}

	// Handle the task using LLM-driven protocol
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
func (e *Engineer) Handle(ctx context.Context, req *EngineerRequest) (*EngineerResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	startTime := time.Now()
	e.setStatus(AgentStatusBusy)
	defer e.setStatus(AgentStatusIdle)

	// Step 1: Validate scope
	if err := e.validateTaskScope(ctx, req); err != nil {
		shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventError,
			e.id, "", "", "error", &agentlog.ErrorPayload{Error: err.Error()})
		return e.failureResponse(req, err, startTime)
	}

	// Step 2: Synchronous Librarian consultation
	var consultContext string
	if e.bus != nil && e.running {
		shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventConsultationSent,
			e.id, "", "", "info", &agentlog.ConsultPayload{Target: "librarian"})
		evidence, err := e.requestConsultation(ctx, "librarian",
			fmt.Sprintf("Search for relevant patterns, similar implementations, and dependencies for: %s", req.Prompt),
			"", e.config.SessionID)
		shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventConsultationRecv,
			e.id, "", "", "info", &agentlog.ConsultPayload{Target: "librarian", Success: err == nil})
		if err != nil {
			e.logger.Warn("librarian consultation failed", "error", err)
		} else if evidence.Success {
			consultContext = fmt.Sprintf("Librarian consultation evidence:\n%v", evidence.Data)
		}
	}

	// Step 3: Check previous failures → consult Academic if threshold exceeded
	if failure := e.checkPreviousFailures(req.TaskID); failure != nil {
		e.logger.Info("found previous failure", "task_id", req.TaskID, "attempts", failure.AttemptCount)
		if failure.AttemptCount >= MaxAttemptsBeforeConsultation && e.bus != nil && e.running {
			shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventConsultationSent,
				e.id, "", "", "info", &agentlog.ConsultPayload{Target: "academic"})
			evidence, err := e.requestConsultation(ctx, "academic",
				fmt.Sprintf("Task has failed %d times. Need alternative approach for: %s. Last error: %s",
					failure.AttemptCount, req.Prompt, failure.LastError),
				"", e.config.SessionID)
			shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventConsultationRecv,
				e.id, "", "", "info", &agentlog.ConsultPayload{Target: "academic", Success: err == nil})
			if err != nil {
				e.logger.Warn("academic consultation failed", "error", err)
			} else if evidence.Success {
				consultContext += fmt.Sprintf("\n\nAcademic consultation evidence:\n%v", evidence.Data)
			}
		}
	}

	// Step 4: Compose system prompt + consultation context
	systemPrompt := e.config.SystemPrompt
	if consultContext != "" {
		systemPrompt += "\n\n---\n\n# Consultation Context\n\n" + consultContext
	}

	// Step 5: Build LLM request with tools
	e.prepareSkillsForInput(req.Prompt)
	llmReq := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: req.Prompt}},
		Tools:        e.buildToolDefinitions(),
		Model:        e.config.EngineerConfig.Model,
		MaxTokens:    e.config.EngineerConfig.MaxTokens,
	}
	e.applyLLMRuntimeProfile(llmReq, "implementation")

	// Prepend conversation history as multi-turn message pairs.
	shared.PrependHistoryMessages(llmReq, req.ConversationHistory)

	// Step 6: Execute tool loop
	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return e.executeToolLoop(ctx, llmReq, ledger)
	})
	if err != nil {
		shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventError,
			e.id, "", "", "error", &agentlog.ErrorPayload{Error: err.Error()})
		e.recordFailure(req.TaskID, err.Error(), req.Prompt)
		return e.failureResponse(req, err, startTime)
	}

	// Step 7: Self-audit (bounded iterations)
	shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventDiscoveryStarted,
		e.id, "", "", "info", &agentlog.DiscoveryPayload{Phase: "started", Type: "self-audit"})
	for iteration := range e.auditConfig.MaxAuditIterations {
		verdict, auditErr := e.selfAudit(ctx, result, req.Prompt)
		if auditErr != nil {
			e.logger.Warn("self-audit failed", "error", auditErr, "iteration", iteration)
			break
		}
		if !shouldReimplement(verdict, iteration, e.auditConfig) {
			break
		}
		// Re-enter tool loop with audit feedback
		e.logger.Info("re-implementing after audit", "iteration", iteration, "score", verdict.QualityScore)
		llmReq.Messages = append(llmReq.Messages,
			providers.Message{Role: providers.RoleAssistant, Content: result},
			providers.Message{Role: providers.RoleUser, Content: e.buildAuditFeedback(verdict)},
		)
		result, err = shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
			return e.executeToolLoop(ctx, llmReq, ledger)
		})
		if err != nil {
			shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventError,
				e.id, "", "", "error", &agentlog.ErrorPayload{Error: fmt.Sprintf("audit re-implement: %v", err)})
			e.recordFailure(req.TaskID, err.Error(), req.Prompt)
			return e.failureResponse(req, err, startTime)
		}
	}

	shared.LogAgentEvent(e.steering.EventLogger(), agentlog.EventDiscoveryCompleted,
		e.id, "", "", "info", &agentlog.DiscoveryPayload{Phase: "completed", Type: "self-audit"})

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

func (e *Engineer) buildAuditFeedback(verdict *AuditVerdict) string {
	if verdict == nil || len(verdict.Issues) == 0 {
		return "The self-audit found issues. Please review and fix your implementation."
	}
	msg := fmt.Sprintf("Self-audit failed (score: %.2f). Fix the following issues:\n", verdict.QualityScore)
	for i, issue := range verdict.Issues {
		msg += fmt.Sprintf("%d. [%s/%s] %s", i+1, issue.Category, issue.Severity, issue.Description)
		if issue.File != "" {
			msg += fmt.Sprintf(" (in %s)", issue.File)
		}
		if issue.Suggestion != "" {
			msg += fmt.Sprintf(" — Suggestion: %s", issue.Suggestion)
		}
		msg += "\n"
	}
	return msg
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

func (e *Engineer) checkPreviousFailures(taskID string) *FailureRecord {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return e.failures[taskID]
}

func (e *Engineer) failureResponse(req *EngineerRequest, err error, _ time.Time) (*EngineerResponse, error) {
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

// InjectPreparedContext accepts a handoff context (no-op for now).
func (e *Engineer) InjectPreparedContext(_ *handoff.PreparedContext) error {
	return nil
}

// SetAgentPod injects the agent pod for Scribe feed integration.
func (e *Engineer) SetAgentPod(pod *shared.AgentPod) {
	e.agentPod = pod
}

// SetHandoffBridge sets the handoff bridge for this engineer.
func (e *Engineer) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	e.handoffBridge = bridge
}

// SetFileAccess injects the FileAccess implementation for this pipeline.
// Called by the Orchestrator when dispatching the engineer to a pipeline.
func (e *Engineer) SetFileAccess(fa versioning.FileAccess) {
	e.fileAccess = fa
}

// SetEscalator injects the confidence-based escalation evaluator.
func (e *Engineer) SetEscalator(esc *escalation.Escalator) {
	e.escalator = esc
}

// ExtractArchivableState returns the engineer's archivable state.
func (e *Engineer) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   e.id,
		AgentType: "engineer",
		Timestamp: time.Now(),
	}
}

// Terminate gracefully shuts down the engineer agent.
func (e *Engineer) Terminate(_ context.Context) error {
	return e.Stop()
}
