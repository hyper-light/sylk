// Package global implements the Global Tester agent — a cross-pipeline SDET
// that architects and runs integration/e2e/cross-cutting tests after a batch
// of concurrent pipelines completes. It uses GPT-5.4 Pro with xhigh reasoning
// for global validation work.
package global

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
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// globalTesterProvider is the minimal interface the GlobalTester needs.
// Satisfied by *providers.OpenAIProvider and *gateway.GatewayProvider.
type globalTesterProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

// GlobalTester architects and runs integration/e2e/cross-cutting tests
// after a batch of concurrent pipelines completes.
type GlobalTester struct {
	id       string
	config   shared.GlobalTesterConfig
	logger   *slog.Logger
	identity *identity.AgentIdentity
	factory  *identity.Factory

	// LLM provider (OpenAI gpt-5.4-pro with xhigh reasoning).
	provider  globalTesterProvider
	refresher container.ProviderRefresher

	// State.
	currentPlan      *shared.TestPlan
	harness          *TestHarness
	executionHarness *globalTestHarnessState
	batchContext     *shared.BatchContext
	diagnoses        map[string]*shared.DiagnosisReport
	mu               sync.RWMutex

	// Diagnosis engine.
	diagEngine shared.DiagnosisEngine

	// Skills.
	skills        *skills.Registry
	skillLoader   *skills.Loader
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

	// Handoff integration.
	handoffBridge *handoff.HandoffBridge

	// Agent pod for cross-agent coordination (Scribe feed, etc.).
	agentPod *agentshared.AgentPod

	// File access (injected per-session at runtime).
	fileAccess      versioning.FileAccess
	workspaceViews  versioning.WorkspaceViewAccess
	executionBroker purevfs.ExecutionBroker

	// Request lifecycle.
	runCtx    context.Context
	runCancel context.CancelFunc

	// Steering ledger management.
	steering *agentshared.SteeringManager
	// Tracks Memory Forest branches surfaced during global testing.
	forestTracker *agentshared.MemoryForestTracker

	// Request serialization: ensures at most one forwarded request
	// executes at a time, preventing cancel/new-request interleaving.
	requestSerializer *agentshared.RequestSerializer
}

// New creates a new GlobalTester instance.
func New(cfg shared.GlobalTesterConfig, provider providers.ProviderAdapter) (*GlobalTester, error) {
	cfg = applyConfigDefaults(cfg)

	testerID := cfg.AgentID
	if testerID == "" {
		testerID = uuid.New().String()[:8]
	}

	gt := &GlobalTester{
		id:                testerID,
		config:            cfg,
		logger:            slog.Default().With("agent", "tester"),
		provider:          provider,
		diagnoses:         make(map[string]*shared.DiagnosisReport),
		knownAgents:       make(map[string]*guide.AgentAnnouncement),
		diagEngine:        shared.NewDiagnosisEngine(),
		forestTracker:     agentshared.NewMemoryForestTracker(),
		steering:          agentshared.NewSteeringManager(),
		requestSerializer: agentshared.NewRequestSerializer(),
		executionBroker:   purevfs.DefaultExecutionBroker(),
	}

	gt.factory = cfg.Factory
	globalTesterIdentity, err := cfg.Factory.Mint(identity.MintOptions{
		Kind: identity.AgentTypeTesterGlobal,
		Pod:  identity.PodRef{ID: "tester", Type: identity.PodTypeSingleton},
	})
	if err != nil {
		return nil, fmt.Errorf("tester: mint identity: %w", err)
	}
	gt.identity = globalTesterIdentity

	gt.steering.InitLazy("tester", cfg.ActivityPub)

	if err := gt.initSkills(); err != nil {
		return nil, err
	}
	return gt, nil
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
// Called by the auth-push mechanism when the user provides credentials after
// initial activation. A nil provider disables LLM features (static
// conversation replies continue to work).
func (gt *GlobalTester) SetProvider(p providers.Provider) {
	gt.mu.Lock()
	defer gt.mu.Unlock()
	gt.provider = p
}

// SetProviderRefresher stores a callback that creates a fresh provider for
// the current model and auth method. Set by cmd/tui.go at bootstrap.
func (gt *GlobalTester) SetProviderRefresher(fn container.ProviderRefresher) {
	gt.mu.Lock()
	defer gt.mu.Unlock()
	gt.refresher = fn
}

// HasProvider reports whether an LLM provider is currently configured.
func (gt *GlobalTester) HasProvider() bool {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	return gt.provider != nil
}

// getProvider returns the current provider under read lock.
func (gt *GlobalTester) getProvider() globalTesterProvider {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	return gt.provider
}

// Ready implements shared.ReadinessReporter.
func (gt *GlobalTester) Ready() (bool, string) {
	if gt.getProvider() == nil {
		return false, "LLM provider not yet wired (authenticate with OpenAI to enable)"
	}
	return true, ""
}

// ProviderType implements container.AuthRefreshable.
func (gt *GlobalTester) ProviderType() string {
	return string(container.ProviderForModel(gt.CurrentModel()))
}

// RefreshProvider implements container.AuthRefreshable.
func (gt *GlobalTester) RefreshProvider(ctx context.Context, authMethod string) error {
	gt.mu.RLock()
	fn := gt.refresher
	gt.mu.RUnlock()
	if fn == nil {
		return nil
	}
	p, err := fn(ctx, gt.CurrentModel(), authMethod)
	if err != nil {
		return fmt.Errorf("tester refresh provider: %w", err)
	}
	gt.SetProvider(p)
	gt.logger.Info("provider refreshed", "model", gt.CurrentModel(), "auth_method", authMethod)
	return nil
}

// SwapModel implements container.ModelSwappable.
// Re-creates the OpenAI provider with the given model ID, re-applying the
// gateway wrapper. Thread-safe via mu.
func (gt *GlobalTester) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	gt.SetProvider(provider)
	gt.mu.Lock()
	gt.config.Model = modelID
	gt.mu.Unlock()
	gt.logger.Info("model swapped", "model", modelID)
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (gt *GlobalTester) CurrentModel() string {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	return gt.config.Model
}

// SupportedModels implements container.ModelSwappable.
func (gt *GlobalTester) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
	}
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

func (gt *GlobalTester) initSkills() error {
	gt.skills = skills.NewRegistry()

	gt.registerCoreSkills()
	if err := agentshared.RegisterMemoryForestSkills(gt.skills, "tester", gt.config.Forest, gt.forestTracker); err != nil {
		return fmt.Errorf("register global tester forest skills: %w", err)
	}
	if err := agentshared.AttachForestOutcomeRecorder(
		gt.skills,
		"validate_work",
		gt.forestTracker,
		gt.config.Forest,
		func() string { return gt.id },
		"tester",
		func() string { return gt.config.SessionID },
		agentshared.OutcomeFromGlobalReviewValidation(
			"global tester validation passed",
			"global tester validation failed",
			"global tester validation was mixed or blocked",
		),
	); err != nil {
		return fmt.Errorf("attach global tester forest review outcome: %w", err)
	}
	for _, skillName := range []string{"report_to_orchestrator", "report_to_architect", "escalate_failure"} {
		if err := agentshared.AttachForestOutcomeRecorder(
			gt.skills,
			skillName,
			gt.forestTracker,
			gt.config.Forest,
			func() string { return gt.id },
			"tester",
			func() string { return gt.config.SessionID },
			agentshared.OutcomeAlways(forest.OutcomeStatusFailed, "global tester escalated a systemic failure"),
		); err != nil {
			return fmt.Errorf("attach global tester forest escalation outcome for %s: %w", skillName, err)
		}
	}

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = globalTesterVisibleSkillNames()
	loaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	gt.skillLoader = skills.NewLoader(gt.skills, loaderCfg)
	tools, err := toolruntime.New(toolruntime.Config{
		Registry: gt.skills,
		Manifest: globalTesterToolManifest(gt.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return fmt.Errorf("initialize global tester tool runtime: %w", err)
	}
	gt.tools = tools
	gt.tools.SyncActiveFromLoaded()
	return nil
}

func (gt *GlobalTester) registerCoreSkills() {
	writeCfg := versioning.WorkspaceWriteSkillConfig{
		GetFileAccess: func() versioning.FileAccess { return gt.fileAccess },
		GetViews:      func() versioning.WorkspaceViewAccess { return gt.workspaceViews },
	}

	// Shared skills.
	gt.skills.Register(versioning.NewReadFileSkillFunc(func() versioning.FileAccess { return gt.fileAccess }))
	gt.skills.Register(runCommandSkill(gt))
	gt.skills.Register(runShellScriptSkill(gt))
	gt.skills.Register(shared.AnalyzeRiskSkill(gt))
	gt.skills.Register(shared.PlanTestsSkill(gt))
	gt.skills.Register(writeTestSkill(gt))
	gt.skills.Register(shared.RunTestSuiteSkill(gt))
	gt.skills.Register(shared.DiagnoseFailureSkill(gt.diagEngine))
	gt.skills.Register(researchTestToolInstallSkill(gt))
	gt.skills.Register(installTestToolingSkill(gt))
	gt.skills.Register(shared.NewTesterReadWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return gt.workspaceViews }, nil))
	gt.skills.Register(versioning.NewWorkspaceGlobSkill(func() versioning.WorkspaceViewAccess { return gt.workspaceViews }, nil))
	gt.skills.Register(versioning.NewWorkspaceGrepSkill(func() versioning.WorkspaceViewAccess { return gt.workspaceViews }, nil))
	gt.skills.Register(versioning.NewInspectWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return gt.workspaceViews }, nil))
	gt.skills.Register(versioning.NewSummarizeWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return gt.workspaceViews }, nil))
	gt.skills.Register(versioning.NewDiffWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return gt.workspaceViews }, nil, nil))
	gt.skills.Register(versioning.NewPrepareGlobalWriteContextSkill(func() versioning.WorkspaceViewAccess { return gt.workspaceViews }, nil))
	gt.skills.Register(versioning.NewListGlobalChangesSkill(func() versioning.FileAccess { return gt.fileAccess }))
	gt.skills.Register(versioning.NewWriteGlobalFileSkill(writeCfg))
	gt.skills.Register(versioning.NewEditGlobalFileSkill(writeCfg))
	gt.skills.Register(versioning.NewDeleteGlobalFileSkill(writeCfg))
	gt.skills.Register(versioning.NewCreateGlobalDirectorySkill(writeCfg))

	// Global-tester-specific skills.
	gt.skills.Register(analyzeBatchSkill(gt))
	gt.skills.Register(analyzeIntegrationRisksSkill(gt))
	gt.skills.Register(planIntegrationTestsSkill(gt))
	gt.skills.Register(planE2ETestsSkill(gt))
	gt.skills.Register(buildHarnessSkill(gt))
	gt.skills.Register(writeIntegrationTestSkill(gt))
	gt.skills.Register(writeE2ETestSkill(gt))
	gt.skills.Register(reportToOrchestratorSkill(gt))
	gt.skills.Register(reportToArchitectSkill(gt))
	gt.skills.Register(escalateFailureSkill(gt))
	for _, skill := range agentshared.NewGlobalReviewProtocolSkills(agentshared.GlobalReviewProtocolSkillConfig{
		AgentType:      func() string { return "tester" },
		AgentID:        func() string { return gt.id },
		ResolveTarget:  func(agentType string) string { return gt.knownAgentIDByType(agentType, agentType) },
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return gt.workspaceViews },
		Route: agentshared.GlobalReviewRouteConfig{
			BusProvider: func() guide.EventBus { return gt.bus },
			SessionID:   func() string { return gt.config.SessionID },
		},
	}) {
		gt.skills.Register(skill)
	}
	// TODO: wire DecisionManifestSkills onto the global tester once it
	// gains a pending-wait infrastructure (registerPendingWait /
	// clearPendingWait equivalent). The screenshot bug is in the pipeline
	// tester path so phase 1 ships without global-tester wiring.

	// Activity Fabric: uniform awareness skills + cross-pipeline primitives.
	for _, skill := range agentshared.AwarenessSkills(agentshared.AwarenessSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return gt.config.SessionID },
		AgentID:        func() string { return gt.id },
		AgentType:      func() string { return "tester" },
	}) {
		gt.skills.Register(skill)
	}
	for _, skill := range agentshared.CrossPipelineSkills(agentshared.CrossPipelineSkillConfig{
		SessionID: func() string { return gt.config.SessionID },
		AgentID:   func() string { return gt.id },
		AgentType: func() string { return "tester" },
	}) {
		gt.skills.Register(skill)
	}
	// Phase 5 of SCRIBE_FABRIC.md: recall_my_history.
	for _, skill := range agentshared.RecallSkills(agentshared.RecallSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return gt.config.SessionID },
		AgentID:        func() string { return gt.id },
		AgentType:      func() string { return "tester" },
	}) {
		gt.skills.Register(skill)
	}

	// Diagnostics
	gt.skills.Register(agentshared.NewSelfDiagnosticSkill(&globalTesterDiag{gt: gt}))

	gt.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   gt.id,
		SessionID: func() string { return gt.config.SessionID },
		Publish:   gt.publishRerouteRequest,
	}))
}

type globalTesterDiag struct{ gt *GlobalTester }

func (d *globalTesterDiag) AgentName() string { return "tester_global" }
func (d *globalTesterDiag) SessionID() string { return d.gt.config.SessionID }
func (d *globalTesterDiag) LogsDir() string {
	return agentshared.LogsDirForAgent(d.gt.steering.SessionDir(), "tester_global")
}
func (d *globalTesterDiag) EventLogger() *agentlog.SessionEventLogger {
	return d.gt.steering.EventLogger()
}
func (d *globalTesterDiag) PeerLogsDirs() map[string]string { return nil }
func (d *globalTesterDiag) RecoveryHints() []string         { return nil }

func (d *globalTesterDiag) AgentSpecificDiagnostics() map[string]any {
	d.gt.mu.RLock()
	defer d.gt.mu.RUnlock()
	result := map[string]any{}
	if d.gt.currentPlan != nil {
		result["test_plan_id"] = d.gt.currentPlan.ID
		result["planned_cases"] = len(d.gt.currentPlan.PlannedCase)
	}
	return result
}

// Close shuts down the global tester.
func (gt *GlobalTester) Close() error {
	if gt.tools != nil {
		gt.tools.Close()
		gt.tools = nil
	}
	return gt.Stop()
}

// Start begins listening for messages on the event bus.
func (gt *GlobalTester) Start(bus guide.EventBus) error {
	if gt.running {
		return fmt.Errorf("global tester is already running")
	}

	gt.bus = bus
	gt.channels = guide.NewAgentChannels("tester", gt.id)
	gt.runCtx, gt.runCancel = context.WithCancel(context.Background())

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

	gt.steering.CloseAll()
	if gt.runCancel != nil {
		gt.runCancel()
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

// Handle processes a forwarded request, dispatching by intent.
// Conversational intents (Help, Chat, Unknown) route to the conversation
// pipeline with static fast-path, LLM fallback, and streaming. Task-oriented
// intents (Check, Recall) route to the full LLM tool loop with the testing
// system prompt.
func (gt *GlobalTester) Handle(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	ctx = versioning.WithSessionID(ctx, fwd.SessionID)
	ctx = agentshared.WithForwardedTaskScope(ctx, fwd.Metadata)
	ctx = agentshared.WithGuardianCommandGate(ctx, agentshared.GuardianCommandGateConfig{
		BusProvider:     func() guide.EventBus { return gt.bus },
		SourceAgentID:   func() string { return gt.id },
		SourceAgentType: "tester",
		SourceAgentName: "Tester",
	})
	switch fwd.Intent {
	case guide.IntentHelp, guide.IntentChat, guide.IntentUnknown:
		return gt.handleConversation(ctx, fwd)
	default:
		return gt.handleTaskRequest(ctx, fwd)
	}
}

// handleTaskRequest processes task-oriented requests through the full LLM tool loop.
func (gt *GlobalTester) handleTaskRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if gt.getProvider() == nil {
		return nil, fmt.Errorf("global tester: %w: LLM provider not yet wired — authenticate with OpenAI to enable", agentshared.ErrAgentNotReady)
	}

	var closeGlobalReviewState func()
	ctx, closeGlobalReviewState = agentshared.OpenGlobalReviewContextWithPublisher(
		ctx, fwd.Metadata, gt.bus, fwd.SessionID, "tester-global",
	)
	defer closeGlobalReviewState()
	contract := agentshared.BuildGlobalExecutionContract("tester-global", fwd.Intent, fwd.Input)
	ctx = agentshared.WithGlobalExecutionState(ctx, agentshared.NewGlobalExecutionState(contract))
	systemPrompt := shared.GlobalTesterSystemPromptForContract(contract)
	systemPrompt = agentshared.AppendGlobalExecutionGuidance(systemPrompt, contract, "tester-global")
	gt.prepareSkillsForInput(fwd.Input)
	tools := gt.buildToolDefinitions()

	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: fwd.Input},
		},
		Tools: tools,
	}
	gt.applyLLMRuntimeProfile(req, "testing")

	// Prepend conversation history as multi-turn message pairs.
	agentshared.PrependHistoryMessages(req, fwd.ConversationHistory)

	ledger := agentshared.SteeringLedgerFromContext(ctx)
	result, err := agentshared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return gt.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("tool loop: %v", err)})
		}
		return nil, fmt.Errorf("global tester tool loop: %w", err)
	}

	return map[string]any{
		"response": result,
		"agent_id": gt.id,
	}, nil
}

func (gt *GlobalTester) handleBusRequest(msg *guide.Message) error {
	if msg.Type == guide.MessageTypeAction {
		action, ok := msg.GetActionRequest()
		if ok && action != nil {
			gt.steering.HandleAction(action)
		}
		return nil
	}
	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	if !gt.requestSerializer.Acquire(gt.runCtx) {
		return nil // runCtx cancelled, agent shutting down
	}
	defer gt.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	gt.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	agentshared.LogIncomingRequest(gt.steering.EventLogger(), fwd, gt.id)

	reqCtx, cancel := context.WithCancel(gt.runCtx)
	gt.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer cancel()

	ctx := reqCtx
	startTime := time.Now()

	ctx = versioning.WithSessionID(ctx, fwd.SessionID)
	ctx = agentshared.WithForwardedTaskScope(ctx, fwd.Metadata)
	var closeGlobalReviewState func()
	ctx, closeGlobalReviewState = agentshared.OpenGlobalReviewContextWithPublisher(
		ctx, fwd.Metadata, gt.bus, fwd.SessionID, "tester-global",
	)
	defer closeGlobalReviewState()
	ctx = withTesterStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID)
	ctx = agentshared.WithForwardedStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID, fwd.ParentCorrelationID, fwd.Metadata)
	ctx = agentshared.WithOwnedStreamIdentity(ctx, "tester-global", "Tester")
	ctx, usageAcc := withTesterUsageAccumulator(ctx)

	// Create steering ledger for this request.
	ledger := gt.steering.Create(fwd.CorrelationID, gt.id, fwd.SessionID, nil, nil)
	defer gt.steering.Close(fwd.CorrelationID, ctx.Err() != nil)
	ctx = agentshared.WithSteeringLedger(ctx, ledger)
	ctx = agentshared.WithLogMeta(ctx, agentshared.LogMeta{
		EventLogger: gt.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     gt.id,
		SessionID:   fwd.SessionID,
	})
	if gt.factory != nil && gt.identity != nil {
		task, taskErr := gt.factory.NewTask(identity.TaskOptions{
			DisplayID:   fwd.CorrelationID,
			Correlation: identity.CorrelationID(fwd.CorrelationID),
		})
		if taskErr != nil {
			return fmt.Errorf("tester: mint task: %w", taskErr)
		}
		ctx = identity.WithIdentity(ctx, gt.identity)
		ctx = identity.WithTask(ctx, task)
	}
	allowedHandoff := agentshared.AutomaticHandoffAllowedForForwardedRequest(fwd)
	ctx = agentshared.WithAutomaticHandoffEnabled(ctx, allowedHandoff)
	ctx = handoff.WithTransportRetryHandoff(ctx, handoff.TransportRetryHandoffConfig{
		Enabled:       allowedHandoff,
		Bridge:        agentshared.EffectiveHandoffBridge(ctx, gt.handoffBridge),
		AgentID:       gt.id,
		AgentType:     "tester",
		UserRequest:   fwd.Input,
		CorrelationID: fwd.CorrelationID,
		SessionID:     fwd.SessionID,
		EventLogger:   gt.steering.EventLogger(),
		Scribe:        gt.agentPod,
	})

	toolEmitter := agentshared.NewToolCallEmitter(gt.bus, gt.channels, gt.id, fwd.CorrelationID, fwd.SourceAgentID)
	ctx = agentshared.WithToolCallEmitter(ctx, toolEmitter)
	gov := agentshared.NewContextGovernor(gt.config.Model, gt.config.MaxTokens, 0)
	if gt.handoffBridge != nil && agentshared.AutomaticHandoffEnabled(ctx) {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			bridge := agentshared.EffectiveHandoffBridge(bctx, gt.handoffBridge)
			if bridge == nil {
				return agentshared.ErrContextBudgetExhausted
			}
			return bridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	ctx = agentshared.WithContextGovernor(ctx, gov)
	ctx = agentshared.WithProgressPublisher(ctx, &agentshared.ProgressPublisher{
		Bus: gt.bus, Channels: gt.channels,
		AgentID: gt.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})
	if !fwd.FireAndForget {
		gt.publishStreamStart(ctx)
	}

	result, err := gt.Handle(ctx, fwd)
	agentshared.LogResponse(gt.steering.EventLogger(), fwd.CorrelationID, gt.id, fwd.SessionID, time.Since(startTime), err)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   gt.id,
		RespondingAgentName: "Tester",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		if streamErr := gt.publishStreamError(ctx, err); streamErr != nil {
			gt.logger.Warn("global_tester_stream_error_publish_failed",
				"correlation_id", fwd.CorrelationID,
				"underlying_error", err.Error(),
				"publish_error", streamErr.Error())
		}
		if completeErr := gt.publishStreamComplete(ctx, "", usageAcc.Total(), nil); completeErr != nil {
			gt.logger.Warn("global_tester_stream_complete_publish_failed",
				"correlation_id", fwd.CorrelationID,
				"publish_error", completeErr.Error())
		}
		resp.Error = err.Error()
		respMsg := guide.NewResponseMessage(gt.generateMessageID(), resp)
		if pubErr := gt.bus.Publish(gt.channels.Responses, respMsg); pubErr != nil {
			gt.logger.Warn("global_tester_error_response_publish_failed",
				"correlation_id", fwd.CorrelationID,
				"underlying_error", err.Error(),
				"publish_error", pubErr.Error())
		}
		errMsg := guide.NewErrorMessage(
			gt.generateMessageID(),
			fwd.CorrelationID,
			gt.id,
			err.Error(),
		)
		return gt.bus.Publish(gt.channels.Errors, errMsg)
	}

	// Conversation text is already streamed via chunks — send complete with
	// empty text so the bridge doesn't duplicate content.
	completeText := extractTesterUserResponse(result)
	directive := extractTesterResponseDirective(result)
	if isStreamedTesterConversation(result) {
		completeText = ""
	}
	gt.publishStreamComplete(ctx, completeText, usageAcc.Total(), directive)

	result = agentshared.WrapGlobalReviewTurnResult(ctx, result)
	resp.Data = result

	if gt.agentPod != nil {
		gt.agentPod.FeedScribe("tester", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}

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

	gt.knownAgentsMu.Lock()
	defer gt.knownAgentsMu.Unlock()

	action := "registered"
	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		gt.knownAgents[ann.AgentID] = ann
	case guide.MessageTypeAgentUnregistered:
		delete(gt.knownAgents, ann.AgentID)
		action = "unregistered"
	}
	if el := gt.steering.EventLogger(); el != nil {
		agentshared.LogAgentEvent(el, agentlog.EventRegistryEvent,
			gt.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: action,
			})
	}
	return nil
}

func (gt *GlobalTester) generateMessageID() string {
	return fmt.Sprintf("gt_msg_%s", uuid.New().String()[:8])
}

func (gt *GlobalTester) knownAgentIDByType(agentType, fallback string) string {
	if gt == nil {
		return fallback
	}
	targetType := strings.TrimSpace(agentType)
	if targetType == "" {
		return fallback
	}
	gt.knownAgentsMu.RLock()
	defer gt.knownAgentsMu.RUnlock()
	for _, ann := range gt.knownAgents {
		if ann == nil {
			continue
		}
		if strings.TrimSpace(ann.AgentType) == targetType && strings.TrimSpace(ann.AgentID) != "" {
			return strings.TrimSpace(ann.AgentID)
		}
	}
	return fallback
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
	return TesterRoutingInfo(gt.id)
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

// SetCanonicalID overwrites the tester's internal ID. Used during
// handoff swap so the new instance assumes the canonical identity.
func (gt *GlobalTester) SetCanonicalID(id string) {
	gt.mu.Lock()
	defer gt.mu.Unlock()
	gt.id = id
}

// AgentType returns the type classification.
func (gt *GlobalTester) AgentType() string { return "tester" }

// Descriptor returns immutable metadata.
func (gt *GlobalTester) Descriptor() handoff.AgentDescriptor {
	modelID := gt.CurrentModel()
	return handoff.AgentDescriptor{
		AgentType:             "tester",
		ModelID:               modelID,
		ReasoningEffort:       gt.config.ReasoningEffort,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryStandalone,
		RuntimeProfiles:       testerRuntimeProfiles(),
		DefaultRuntimeProfile: testerDefaultRuntimeProfile(),
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
	if bridge != nil {
		bridge.SetActivityPublisher(gt.config.ActivityPub)
	}
}

// SetAgentPod assigns the agent pod for cross-agent coordination.
func (gt *GlobalTester) SetAgentPod(pod *agentshared.AgentPod) {
	gt.agentPod = pod
}

// SetFileAccess injects the per-session file access layer.
func (gt *GlobalTester) SetFileAccess(fa versioning.FileAccess) {
	gt.fileAccess = authority.RestrictFileAccess("tester", fa)
}

// SetWorkspaceViews injects explicit disk/global/pipeline read access.
func (gt *GlobalTester) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	gt.workspaceViews = authority.RestrictWorkspaceViews("tester", views)
}

// SetExecutionBroker overrides the strict execution broker.
func (gt *GlobalTester) SetExecutionBroker(broker purevfs.ExecutionBroker) {
	gt.executionBroker = broker
}

// ExtractArchivableState returns state for handoff persistence.
func (gt *GlobalTester) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   gt.AgentID(),
		AgentType: gt.AgentType(),
		Timestamp: time.Now(),
	}
}
