package engineer

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/escalation"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/skills"
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
// It uses GPT-5.3 Codex with xhigh reasoning to execute individual coding
// tasks via an LLM-driven tool loop with self-audit.
type Engineer struct {
	id     string
	config Config
	logger *slog.Logger

	// LLM provider
	provider        engineerProvider
	providerWrapper gateway.ProviderWrapper

	// State management
	state    *EngineerState
	stateMu  sync.RWMutex
	failures map[string]*FailureRecord // taskID -> failure record

	// Skills system
	skills      *skills.Registry
	skillLoader *skills.Loader

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

	// File access abstraction (injected per-pipeline by Orchestrator).
	fileAccess versioning.FileAccess

	// Self-audit configuration
	auditConfig AuditConfig

	// Refactor loop configuration
	refactorConfig shared.RefactorLoopConfig

	// Escalation
	escalator *escalation.Escalator
}

// Config holds configuration for the Engineer agent
type Config struct {
	// System prompt configuration
	SystemPrompt    string // Optional, uses DefaultEngineerSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Engineer-specific configuration
	EngineerConfig EngineerConfig // Task execution configuration

	// Logging
	Logger *slog.Logger // Optional, uses slog.Default() if nil

	// Session context
	SessionID string // Session identifier
}

// Default configuration values
const (
	DefaultMaxOutputTokens = 16384
	DefaultModel           = "gpt-5.3-codex"
	DefaultReasoningEffort = "xhigh"
	DefaultMaxToolRuns     = 16
	DefaultMaxTokens       = 16384
)

// New creates a new Engineer agent with the given LLM provider.
func New(cfg Config, provider engineerProvider) (*Engineer, error) {
	cfg = applyConfigDefaults(cfg)

	engineerID := fmt.Sprintf("engineer_%s", uuid.New().String()[:8])

	eng := &Engineer{
		id:              engineerID,
		config:          cfg,
		logger:          cfg.Logger,
		provider:        provider,
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
		consultations: make([]Consultation, 0),
	}

	eng.initSkills()

	return eng, nil
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (e *Engineer) SetProvider(p engineerProvider) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.provider = p
}

// SetProviderWrapper stores a callback that re-applies gateway rate limiting
// to fresh providers created during credential refresh.
func (e *Engineer) SetProviderWrapper(w gateway.ProviderWrapper) {
	e.providerWrapper = w
}

// getProvider returns the current provider under read lock.
func (e *Engineer) getProvider() engineerProvider {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return e.provider
}

// ProviderType implements container.AuthRefreshable.
func (e *Engineer) ProviderType() string { return "openai" }

// RefreshProvider implements container.AuthRefreshable.
// Re-resolves OpenAI credentials and replaces the provider.
func (e *Engineer) RefreshProvider(_ context.Context) error {
	cfg := providers.OpenAIConfig{
		BaseConfig: providers.BaseConfig{
			Model:     DefaultModel,
			MaxTokens: DefaultMaxTokens,
		},
		ReasoningEffort: DefaultReasoningEffort,
		AuthMode:        "api_key",
	}
	p, err := providers.NewOpenAIProvider(cfg)
	if err != nil {
		return fmt.Errorf("engineer refresh provider: %w", err)
	}
	var wrapped engineerProvider = p
	if e.providerWrapper != nil {
		wrapped = e.providerWrapper(p)
	}
	e.SetProvider(wrapped)
	e.logger.Info("provider refreshed")
	return nil
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

func (e *Engineer) initSkills() {
	e.skills = skills.NewRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"read_file", "write_file", "edit_file", "run_command",
		"run_tests", "glob", "grep",
	}
	loaderCfg.AutoLoadDomains = []string{"code", "filesystem", "testing"}
	e.skillLoader = skills.NewLoader(e.skills, loaderCfg)

	e.registerCoreSkills()
}

// ID returns the engineer's unique identifier
func (e *Engineer) ID() string {
	return e.id
}

// Close closes the engineer and its resources
func (e *Engineer) Close() error {
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
	e.channels = guide.NewAgentChannels("engineer", "engineer")

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

	e.running = true
	e.logger.Info("engineer started", "id", e.id, "channels", e.channels)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (e *Engineer) Stop() error {
	if !e.running {
		return nil
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
	if msg.Type != guide.MessageTypeForward {
		return nil // Ignore non-forward messages
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	// Process the request
	ctx := context.Background()
	startTime := time.Now()

	// Wire tool call emitter for inline visualization.
	emitter := shared.NewToolCallEmitter(e.bus, e.channels, "engineer", fwd.CorrelationID, fwd.SourceAgentID)
	ctx = shared.WithToolCallEmitter(ctx, emitter)

	result, err := e.processForwardedRequest(ctx, fwd)

	// Don't respond if fire-and-forget
	if fwd.FireAndForget {
		return nil
	}

	// Build response
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   "engineer",
		RespondingAgentName: "engineer",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		// Publish to error channel
		errMsg := guide.NewErrorMessage(
			e.generateMessageID(),
			fwd.CorrelationID,
			e.id,
			err.Error(),
		)
		return e.bus.Publish(e.channels.Errors, errMsg)
	}

	resp.Data = result

	// Publish response to own response channel
	respMsg := guide.NewResponseMessage(e.generateMessageID(), resp)
	return e.bus.Publish(e.channels.Responses, respMsg)
}

func (e *Engineer) generateMessageID() string {
	return fmt.Sprintf("engineer_msg_%s", uuid.New().String())
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
		ID:         uuid.New().String(),
		Intent:     IntentComplete,
		TaskID:     taskID,
		Prompt:     fwd.Input,
		EngineerID: e.id,
		SessionID:  e.config.SessionID,
		Timestamp:  time.Now(),
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
	case guide.MessageTypeAgentUnregistered:
		delete(e.knownAgents, ann.AgentID)
		e.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
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
		return e.failureResponse(req, err, startTime)
	}

	// Step 2: Synchronous Librarian consultation
	var consultContext string
	if e.bus != nil && e.running {
		evidence, err := e.requestConsultation(ctx, "librarian",
			fmt.Sprintf("Search for relevant patterns, similar implementations, and dependencies for: %s", req.Prompt),
			"", e.config.SessionID)
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
			evidence, err := e.requestConsultation(ctx, "academic",
				fmt.Sprintf("Task has failed %d times. Need alternative approach for: %s. Last error: %s",
					failure.AttemptCount, req.Prompt, failure.LastError),
				"", e.config.SessionID)
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
	llmReq := &providers.Request{
		SystemPrompt:    systemPrompt,
		Messages:        []providers.Message{{Role: providers.RoleUser, Content: req.Prompt}},
		Tools:           e.buildToolDefinitions(),
		Model:           e.config.EngineerConfig.Model,
		MaxTokens:       e.config.EngineerConfig.MaxTokens,
		ReasoningEffort: e.config.EngineerConfig.ReasoningEffort,
	}

	// Step 6: Execute tool loop
	result, err := e.executeToolLoop(ctx, llmReq)
	if err != nil {
		e.recordFailure(req.TaskID, err.Error(), req.Prompt)
		return e.failureResponse(req, err, startTime)
	}

	// Step 7: Self-audit (bounded iterations)
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
		result, err = e.executeToolLoop(ctx, llmReq)
		if err != nil {
			e.recordFailure(req.TaskID, err.Error(), req.Prompt)
			return e.failureResponse(req, err, startTime)
		}
	}

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
	return &guide.AgentRoutingInfo{
		ID:      e.id,
		Type:    "engineer",
		Name:    "engineer",
		Aliases: []string{"eng", "impl", "code", "implement"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "implement",
				Description:   "Implement a coding task",
				DefaultIntent: guide.IntentComplete,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "code",
				Description:   "Write code for a specific feature or fix",
				DefaultIntent: guide.IntentComplete,
				DefaultDomain: guide.DomainCode,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"implement", "code", "write", "create", "build",
				"fix", "refactor", "add feature", "modify", "update code",
			},
			WeakTriggers: []string{
				"function", "method", "class", "file", "module",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentComplete: {
					"implement", "code", "write", "create", "build",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      e.id,
			Name:    "engineer",
			Aliases: []string{"eng", "impl", "code"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentComplete,
				},
				Domains: []guide.Domain{
					guide.DomainCode,
					guide.DomainFiles,
				},
				Tags:     []string{"implementation", "code", "development", "testing"},
				Keywords: []string{"implement", "code", "write", "create", "build", "fix", "refactor", "test"},
				Priority: 70,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.7,
			},
			Description: "Staff-level implementation engineer. GPT-5.3 Codex with xhigh reasoning. Executes coding tasks with self-audit and consultation.",
			Priority:    70,
		},
	}
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
	return e.skills.GetToolDefinitions()
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
	return handoff.AgentDescriptor{
		AgentType:       "engineer",
		ModelID:         "gpt-5.3-codex",
		ReasoningEffort: "xhigh",
		ContextWindow:   200_000,
		Category:        handoff.CategoryPipeline,
	}
}

// InjectPreparedContext accepts a handoff context (no-op for now).
func (e *Engineer) InjectPreparedContext(_ *handoff.PreparedContext) error {
	return nil
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
