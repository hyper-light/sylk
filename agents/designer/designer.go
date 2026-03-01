package designer

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/skills"
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

	provider        designerProvider
	providerWrapper gateway.ProviderWrapper
	handoffBridge   *handoff.HandoffBridge
	usageAccum      *designerUsageAccumulator

	state    *DesignerState
	stateMu  sync.RWMutex
	failures map[string]*FailureRecord

	skills      *skills.Registry
	skillLoader *skills.Loader

	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement

	consultations []Consultation
	consultMu     sync.RWMutex

	fileAccess versioning.FileAccess

	// Request-scoped context lifecycle (mirrors architect/engineer pattern).
	runCtx         context.Context
	runCancel      context.CancelFunc
	requestMu      sync.Mutex
	requestCancels map[string]context.CancelFunc
}

type Config struct {
	// Canonical agent ID. If empty, generates a UUID8 (pipeline use).
	ID string

	SystemPrompt    string
	MaxOutputTokens int

	DesignerConfig DesignerConfig

	// RequestGuard is called at handler entry to prevent activation demotion
	// during in-flight processing. Returns a release function. Nil-safe.
	RequestGuard func() func()

	Logger *slog.Logger

	SessionID string
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
		designerID = fmt.Sprintf("designer_%s", uuid.New().String()[:8])
	}

	d := &Designer{
		id:          designerID,
		config:      cfg,
		logger:      cfg.Logger,
		provider:    provider,
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
		consultations: make([]Consultation, 0),
	}

	d.initSkills()

	return d, nil
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (d *Designer) SetProvider(p designerProvider) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	d.provider = p
}

// SetProviderWrapper stores a callback that re-applies gateway rate limiting
// to fresh providers created during credential refresh.
func (d *Designer) SetProviderWrapper(w gateway.ProviderWrapper) {
	d.providerWrapper = w
}

// getProvider returns the current provider under read lock.
func (d *Designer) getProvider() designerProvider {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.provider
}

// ProviderType implements container.AuthRefreshable.
func (d *Designer) ProviderType() string { return "google" }

// RefreshProvider implements container.AuthRefreshable.
// Re-resolves Google credentials and replaces the provider.
func (d *Designer) RefreshProvider(ctx context.Context, authMethod string) error {
	cfg := providers.DefaultGoogleConfig()
	if authMethod != "" {
		cfg.AuthMode = authMethod
	}
	p, err := providers.NewGoogleProvider(ctx, cfg)
	if err != nil {
		return fmt.Errorf("designer refresh provider: %w", err)
	}
	var wrapped designerProvider = p
	if d.providerWrapper != nil {
		wrapped = d.providerWrapper(p)
	}
	d.SetProvider(wrapped)
	d.logger.Info("provider refreshed", "auth_method", authMethod)
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

func (d *Designer) initSkills() {
	d.skills = skills.NewRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"component_search", "component_create", "component_modify",
		"token_validate", "token_suggest",
		"a11y_audit", "a11y_fix_suggest", "contrast_check",
		"request_engineer_review", "request_inspector_check",
		"request_tester_validation", "ask_user_clarification",
		"report_to_engineer", "report_to_orchestrator",
	}
	loaderCfg.AutoLoadDomains = []string{"ui", "design", "accessibility", "collaboration"}
	d.skillLoader = skills.NewLoader(d.skills, loaderCfg)

	d.registerCoreSkills()
}

func (d *Designer) ID() string {
	return d.id
}

func (d *Designer) Close() error {
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

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	if d.config.RequestGuard != nil {
		release := d.config.RequestGuard()
		defer release()
	}

	// Request-scoped cancellable context.
	reqCtx, cancel := context.WithCancel(d.runCtx)
	d.registerRequestCancel(fwd.CorrelationID, cancel)
	defer d.clearRequestCancel(fwd.CorrelationID)
	defer cancel()

	emitter := shared.NewToolCallEmitter(d.bus, d.channels, "designer", fwd.CorrelationID, fwd.SourceAgentID)
	ctx := shared.WithToolCallEmitter(reqCtx, emitter)
	startTime := time.Now()

	result, err := d.handleDesign(ctx, fwd)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   "designer",
		RespondingAgentName: "designer",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		errMsg := guide.NewErrorMessage(
			d.generateMessageID(),
			fwd.CorrelationID,
			d.id,
			err.Error(),
		)
		return d.bus.Publish(d.channels.Errors, errMsg)
	}

	resp.Data = result

	respMsg := guide.NewResponseMessage(d.generateMessageID(), resp)
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
	if action.Action == "cancel" {
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

	toolDefs := d.buildToolDefinitions()

	req := &providers.Request{
		SystemPrompt: d.config.SystemPrompt,
		Messages: []providers.Message{
			{
				Role:    providers.RoleUser,
				Content: fwd.Input,
			},
		},
		Tools:           toolDefs,
		MaxTokens:       d.config.DesignerConfig.MaxTokens,
		ReasoningEffort: d.config.DesignerConfig.ReasoningEffort,
	}

	result, err := d.executeToolLoop(ctx, req)
	if err != nil {
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

func (d *Designer) handleBusResponse(msg *guide.Message) error {
	d.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	return nil
}

func (d *Designer) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		d.knownAgents[ann.AgentID] = ann
		d.logger.Debug("agent registered", "agent_id", ann.AgentID)
	case guide.MessageTypeAgentUnregistered:
		delete(d.knownAgents, ann.AgentID)
		d.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
	}

	return nil
}

func (d *Designer) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	result := make(map[string]*guide.AgentAnnouncement, len(d.knownAgents))
	for k, v := range d.knownAgents {
		result[k] = v
	}
	return result
}

// HandleRequest is the public entry point for direct invocations (e.g. from
// the TDD pipeline factory). It wraps the input into a ForwardedRequest and
// delegates to the LLM tool loop.
func (d *Designer) HandleRequest(ctx context.Context, input string) (any, error) {
	fwd := &guide.ForwardedRequest{
		Input:         input,
		Intent:        guide.IntentDesign,
		Domain:        guide.DomainCode,
		SourceAgentID: d.id,
		TargetAgentID: d.id,
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
	return d.skills.GetToolDefinitions()
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
	return handoff.AgentDescriptor{
		AgentType:       "designer",
		ModelID:         d.CurrentModel(),
		ReasoningEffort: "high",
		ContextWindow:   1_000_000,
		Category:        handoff.CategoryPipeline,
	}
}

// InjectPreparedContext accepts context from a handoff.
func (d *Designer) InjectPreparedContext(_ *handoff.PreparedContext) error {
	return nil
}

// ExtractArchivableState returns state for handoff persistence.
func (d *Designer) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   d.AgentID(),
		AgentType: d.AgentType(),
		Timestamp: time.Now(),
	}
}

// SetHandoffBridge assigns the handoff bridge for turn recording.
func (d *Designer) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	d.handoffBridge = bridge
}

// SetFileAccess assigns the per-pipeline file access layer.
func (d *Designer) SetFileAccess(fa versioning.FileAccess) { d.fileAccess = fa }

// Terminate gracefully shuts down the designer agent.
func (d *Designer) Terminate(_ context.Context) error {
	return d.Stop()
}

// Compile-time interface verification.
var _ handoff.HandoffInjectable = (*Designer)(nil)
