package handoff

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/llmruntime"
)

// BridgeConfig configures a HandoffBridge for a specific agent.
type BridgeConfig struct {
	Descriptor     AgentDescriptor
	ManagerConfig  *HandoffManagerConfig
	QualityConfig  *QualityHookConfig
	ContextConfig  *ContextCheckConfig
	EvictionConfig *HandoffAwareEvictionConfig // nil for non-knowledge agents
	BriefSource    BriefSource                 // nil = no brief generation
}

// BridgeConfigForAgent creates an appropriate BridgeConfig based on agent descriptor.
// All sub-configs are derived from the descriptor — no magic numbers.
func BridgeConfigForAgent(desc AgentDescriptor) BridgeConfig {
	// Evaluation interval scales with context window: larger windows check less frequently.
	evalInterval := time.Duration(desc.ContextWindow/10_000) * time.Second
	if evalInterval < 5*time.Second {
		evalInterval = 5 * time.Second
	}
	evalInterval = llmruntime.AdjustEvalInterval(evalInterval, desc.RuntimeProfiles)

	// Context threshold: start evaluating at 75% of window.
	contextThreshold := llmruntime.AdjustContextThreshold(0.75, desc.RuntimeProfiles)

	managerCfg := DefaultHandoffManagerConfig()
	managerCfg.EvaluationInterval = evalInterval
	managerCfg.AgentID = desc.AgentType
	managerCfg.ModelName = desc.ModelID
	managerCfg.EnableLearning = true
	managerCfg.EnableContextMaintenance = true

	qualityCfg := DefaultQualityHookConfig()

	contextCfg := DefaultContextCheckConfig()
	contextCfg.Name = desc.AgentType + "-context-check"
	contextCfg.ContextThreshold = contextThreshold
	contextCfg.UseGPPrediction = true
	contextCfg.NonBlocking = true
	contextCfg.MinCheckInterval = evalInterval / 2

	cfg := BridgeConfig{
		Descriptor:    desc,
		ManagerConfig: managerCfg,
		QualityConfig: qualityCfg,
		ContextConfig: contextCfg,
	}

	// Knowledge agents get eviction config.
	if desc.Category == CategoryKnowledge {
		evCfg := DefaultHandoffAwareEvictionConfig()
		evCfg.UseGPPrediction = true
		// Preserve more recent turns for larger windows.
		evCfg.PreserveRecentTurns = desc.ContextWindow / 40_000
		if evCfg.PreserveRecentTurns < 3 {
			evCfg.PreserveRecentTurns = 3
		}
		evCfg.PreserveRecentTurns = llmruntime.AdjustPreserveRecentTurns(evCfg.PreserveRecentTurns, desc.RuntimeProfiles)
		cfg.EvictionConfig = evCfg
	}

	return cfg
}

// HandoffBridge integrates the handoff engine with a live agent instance.
// One bridge exists per agent. It owns the per-agent handoff components
// (GP, profile, quality hook, context check, eviction) and delegates
// shared state operations to the supervisor.
type HandoffBridge struct {
	mu sync.RWMutex

	config     BridgeConfig
	agent      HandoffableAgent
	supervisor *HandoffSupervisor

	// Per-agent owned components.
	manager      *HandoffManager
	qualityHook  *DefaultQualityAssessorHook
	gp           *AgentGaussianProcess
	profile      *AgentHandoffProfile
	contextCheck *ContextCheckHook
	eviction     *HandoffAwareEviction // nil for non-knowledge agents
	prepared     *PreparedContext

	// Parallel buffer components (nil for knowledge agents).
	parallelBuffer *ParallelBuffer
	overlapCoord   *OverlapCoordinator

	// Signal fusion (per-bridge, wired to supervisor's baseline registry).
	fuser *SignalFuser

	// Quality publisher — pushes GP predictions to the service registry.
	qualityPublisher func(agentID string, quality, stdDev float64)

	// Active traffic shift — non-nil during gradual handoff.
	activeShift *TrafficShiftController

	// Context brief generator — produces LLM-generated handoff summaries.
	briefGen *BriefGenerator

	// Optional activity publisher for UI-facing handoff state updates.
	activityPub events.ActivityPublisher

	// archivePublisher — HAND-05. Fire-and-forget sink for
	// ArchiveRequests the bridge emits at lifecycle transitions.
	// Propagated from HandoffSupervisor.SetArchivePublisher; nil when
	// the supervisor has no publisher configured (archival disabled).
	archivePublisher ArchivePublisher

	// Claims board provider for handoff lifecycle claims.
	boardProvider BoardProvider

	// Latest per-request trace metadata for async handoff recommendations.
	lastTrace bridgeTraceMeta

	turnCount         atomic.Int64
	started           atomic.Bool
	handoffInProgress atomic.Bool
	stopCh            chan struct{}
	doneCh            chan struct{}
}

type bridgeTraceMeta struct {
	EventLogger   *agentlog.SessionEventLogger
	CorrelationID string
	SessionID     string
}

// NewHandoffBridge creates a bridge for the given agent, wired to the supervisor.
func NewHandoffBridge(cfg BridgeConfig, agent HandoffableAgent, sup *HandoffSupervisor) *HandoffBridge {
	desc := cfg.Descriptor
	if cfg.ManagerConfig == nil {
		cfg.ManagerConfig = DefaultHandoffManagerConfig()
	}
	cfg.ManagerConfig.AgentID = agent.AgentID()
	if strings.TrimSpace(cfg.ManagerConfig.ModelName) == "" {
		cfg.ManagerConfig.ModelName = desc.ModelID
	}

	gp := NewAgentGaussianProcess(nil)
	profile := NewAgentHandoffProfile(desc.AgentType, desc.ModelID, agent.AgentID())
	controller := NewHandoffController(gp, profile, nil)

	// HAND-02: install the blender-lookup closure so getEffectiveThreshold
	// prefers the supervisor's hierarchical blended view over the local
	// profile when one is available. Captures identity triple by value
	// — bridges do not move between (agentType, modelID, agentID).
	agentType := desc.AgentType
	modelID := desc.ModelID
	instanceUID := agent.AgentID()
	controller.SetBlenderLookup(func() (float64, bool) {
		blender := sup.Blender()
		if blender == nil {
			return 0, false
		}
		blended, ok := blender.GetBlended(agentType, modelID, instanceUID, BlenderParamHandoffThreshold)
		if !ok || blended == nil {
			return 0, false
		}
		return blended.Value, true
	})

	qualityHook := NewDefaultQualityAssessorHook(cfg.QualityConfig, sup.profileLearner)
	contextCheck := NewContextCheckHook(controller, cfg.ContextConfig)
	prepared := NewPreparedContextDefault()

	executor := NewHandoffExecutor(sup.factoryAdapter, nil)
	manager := NewHandoffManager(cfg.ManagerConfig, controller, executor, sup.profileLearner)
	manager.SetPreparedContext(prepared)

	var eviction *HandoffAwareEviction
	if cfg.EvictionConfig != nil {
		eviction = NewHandoffAwareEviction(gp, cfg.EvictionConfig)
	}

	fuser := NewSignalFuser(
		BaselineKey{AgentType: desc.AgentType, ModelID: desc.ModelID},
		sup.baselineRegistry,
	)

	b := &HandoffBridge{
		config:           cfg,
		agent:            agent,
		supervisor:       sup,
		manager:          manager,
		qualityHook:      qualityHook,
		gp:               gp,
		profile:          profile,
		contextCheck:     contextCheck,
		eviction:         eviction,
		prepared:         prepared,
		fuser:            fuser,
		archivePublisher: sup.ArchivePublisher(),
		stopCh:           make(chan struct{}),
		doneCh:           make(chan struct{}),
	}

	// Standalone and pipeline agents get a parallel buffer for async
	// snapshot maintenance and overlap-based handoff.
	if desc.Category == CategoryStandalone || desc.Category == CategoryPipeline {
		b.parallelBuffer = NewParallelBuffer(desc)
		b.overlapCoord = NewOverlapCoordinator(desc)
		b.overlapCoord.SetWALWriter(func(entry OverlapWALEntry) {
			if sup.wal != nil {
				_ = sup.wal.WriteOverlapEvent(entry)
			}
		})
	}

	// Wire brief generator from BriefSource if provided.
	if cfg.BriefSource != nil {
		b.briefGen = NewBriefGenerator(cfg.BriefSource)
	}

	contextCheck.SetObserver(b.observeContextEvaluation)

	return b
}

// Start launches the bridge's monitor loop and starts the context check hook.
// HAND-01: also starts the HandoffManager's background evaluation loop. The
// manager's automatic periodic evaluation never ran in production before this
// because NewHandoffBridge constructed the manager but nothing called Start()
// on it; the bridge's monitor loop alone does not drive manager.EvaluateAndExecute.
func (b *HandoffBridge) Start() error {
	if b.started.Swap(true) {
		return nil
	}

	if err := b.contextCheck.Start(); err != nil {
		b.started.Store(false)
		return fmt.Errorf("start context check: %w", err)
	}

	if b.parallelBuffer != nil {
		if err := b.parallelBuffer.Start(); err != nil {
			_ = b.contextCheck.Stop()
			b.started.Store(false)
			return fmt.Errorf("start parallel buffer: %w", err)
		}
	}

	if err := b.manager.Start(); err != nil {
		if b.parallelBuffer != nil {
			_ = b.parallelBuffer.Stop()
		}
		_ = b.contextCheck.Stop()
		b.started.Store(false)
		return fmt.Errorf("start handoff manager: %w", err)
	}

	go b.monitorLoop()
	return nil
}

// Stop shuts down the bridge, stopping the monitor loop, parallel buffer,
// manager background loop, and context check. Stop() on HandoffManager is
// idempotent and honors GracefulShutdownTimeout.
func (b *HandoffBridge) Stop() error {
	if !b.started.Swap(false) {
		return nil
	}

	close(b.stopCh)
	<-b.doneCh

	if b.parallelBuffer != nil {
		_ = b.parallelBuffer.Stop()
	}

	// Abort any in-progress overlap.
	if b.overlapCoord != nil && b.overlapCoord.Phase() != OverlapIdle {
		b.overlapCoord.abortInternal("bridge stopped")
	}

	_ = b.manager.Stop()
	return b.contextCheck.Stop()
}

// RecordTurn records metrics from a single agent turn. This is the hot path
// called on every turn — it feeds data to the GP, quality hook, prepared
// context, profile learner, and context check.
func (b *HandoffBridge) RecordTurn(rec TurnRecord) {
	turn := b.turnCount.Add(1)
	b.captureTraceMeta(rec)
	b.syncPreparedMetadata(rec)

	// Inject provider/stream signals into fuser.
	b.injectProviderSignals(rec)
	b.injectStreamSignals(rec)

	// Flush fuser to get composite quality and stress.
	flushed := b.fuser.Flush(rec.ToolCalls, rec.ToolSuccesses)
	quality := b.turnQuality(flushed, rec)

	// Build GP observation — still 3D.
	gpObs := NewGPObservation(rec.ContextSize, rec.OutputTokens, rec.ToolCalls, quality)
	b.gp.AddObservation(gpObs)

	// Feed prepared context with a synthetic message. PreparedContext
	// is always maintained — it drives brief generation and GP quality
	// predictions even when the board is the primary state transfer.
	b.prepared.AddMessage(Message{
		Role:       "turn",
		Content:    turnSummary(turn, rec),
		TokenCount: rec.OutputTokens,
		Timestamp:  rec.Timestamp,
	})

	// Feed quality hook with enriched ResponseContext.
	respCtx := b.buildResponseContext(rec, flushed)
	b.qualityHook.OnResponseComplete(respCtx, &AssessmentContext{
		AgentID: b.agent.AgentID(),
		ModelID: b.config.Descriptor.ModelID,
	})

	// Record observation with profile learner.
	b.supervisor.profileLearner.RecordObservation(
		b.config.Descriptor.AgentType,
		b.config.Descriptor.ModelID,
		&HandoffObservation{
			ContextSize:  rec.ContextSize,
			TurnNumber:   rec.TurnNumber,
			QualityScore: quality,
			Timestamp:    rec.Timestamp,
		},
	)

	// HAND-02: push the updated profile's threshold posterior into the
	// hierarchical blender at the instance + agent-model levels, so
	// downstream callers (controllers on this or sibling bridges) can
	// consult a cross-bridge blended view. SyncBlenderFromProfile is
	// cheap (three clones + two map writes) and fail-safe on nil
	// anywhere in the chain.
	b.supervisor.SyncBlenderFromProfile(
		b.config.Descriptor.AgentType,
		b.config.Descriptor.ModelID,
		b.agent.AgentID(),
		b.profile,
	)

	ctxState := &ContextState{
		AgentID:        b.agent.AgentID(),
		AgentType:      b.agent.AgentType(),
		ContextSize:    rec.ContextSize,
		MaxContextSize: b.config.Descriptor.ContextWindow,
		TokenCount:     rec.OutputTokens,
		ToolCallCount:  rec.ToolCalls,
		TurnNumber:     rec.TurnNumber,
	}
	if recommendation := b.contextCheck.EvaluateContext(ctxState); recommendation != nil && recommendation.ShouldHandoff {
		b.triggerHandoff(recommendation)
	}

	// Persist observation to WAL if available.
	if b.supervisor.wal != nil {
		_ = b.supervisor.wal.WriteObservation(
			&HandoffObservation{
				ContextSize:  rec.ContextSize,
				TurnNumber:   rec.TurnNumber,
				QualityScore: quality,
				Timestamp:    rec.Timestamp,
			},
			gpObs,
		)
		// HAND-07: rate-limited profile checkpoint. Internal ShouldPersist
		// gate in ProfileLearner ensures at most one flush per
		// PersistenceInterval across the supervisor, so per-turn calls
		// amortize to near-zero cost outside the flush window.
		_ = b.supervisor.MaybeCheckpointProfiles()
	}

	// Feed parallel buffer with optional stress sideband.
	b.feedParallelBuffer(turn, rec, flushed)

	// Publish GP quality to service registry.
	b.publishQualityUpdate(rec)

	// Pre-generate brief when quality slope is negative.
	b.maybeGenerateBrief(rec)

	// If a traffic shift is active and this turn is from the new agent,
	// increment the new-agent turn counter.
	b.mu.RLock()
	shift := b.activeShift
	b.mu.RUnlock()
	if shift != nil {
		shift.RecordNewAgentTurn()
	}
}

// injectProviderSignals feeds provider-level signals into the fuser.
func (b *HandoffBridge) injectProviderSignals(rec TurnRecord) {
	if rec.StopReason == "" {
		return
	}
	b.fuser.SetProviderSignals(ProviderSignals{
		StopReason:       rec.StopReason,
		StopQual:         StopQualityFromReason(rec.StopReason),
		OutputTokens:     rec.OutputTokens,
		InputTokens:      rec.InputTokens,
		CacheReadTokens:  rec.CacheReadTokens,
		CacheWriteTokens: rec.CacheWriteTokens,
	})
}

// injectStreamSignals feeds stream-level signals into the fuser.
func (b *HandoffBridge) injectStreamSignals(rec TurnRecord) {
	if rec.StreamMetrics == nil {
		return
	}
	b.fuser.SetStreamSignals(ExtractStreamSignals(rec.StreamMetrics))
}

// turnQuality returns the composite quality from fused signals, falling
// back to legacy tool-ratio quality when the fuser has no data.
func (b *HandoffBridge) turnQuality(flushed FlushResult, rec TurnRecord) float64 {
	if flushed.HasData {
		return flushed.Quality.CompositeQuality
	}
	return legacyQuality(rec.ToolCalls, rec.ToolSuccesses)
}

// legacyQuality computes quality the pre-signal way: tool success ratio
// or a neutral default derived from StopQualityFromReason("") when no
// tool calls are present.
func legacyQuality(toolCalls, toolSuccesses int) float64 {
	if toolCalls > 0 {
		return float64(toolSuccesses) / float64(toolCalls)
	}
	// Neutral prior — same as StopQualityFromReason for unknown stop reason.
	return float64(StopQualityFromReason(""))
}

// buildResponseContext creates an enriched ResponseContext with fused signals.
func (b *HandoffBridge) buildResponseContext(rec TurnRecord, flushed FlushResult) *ResponseContext {
	rc := &ResponseContext{
		TokenCount:     rec.OutputTokens,
		TurnNumber:     rec.TurnNumber,
		ContextSize:    rec.ContextSize,
		Timestamp:      rec.Timestamp,
		GenerationTime: rec.Duration,
		StopReason:     rec.StopReason,
	}
	if flushed.HasData {
		rc.CacheEfficiency = flushed.Quality.CacheEfficiency
		rc.OutputRatio = flushed.Quality.OutputRatio
	}
	return rc
}

// feedParallelBuffer sends data to the parallel buffer, routing stress
// to the elastic sizer when present.
func (b *HandoffBridge) feedParallelBuffer(turn int64, rec TurnRecord, flushed FlushResult) {
	if b.parallelBuffer == nil {
		return
	}

	b.parallelBuffer.Ingest(
		Message{
			Role:       "turn",
			Content:    turnSummary(turn, rec),
			TokenCount: rec.OutputTokens,
			Timestamp:  rec.Timestamp,
		},
		rec,
	)

	pred := b.gp.Predict(rec.ContextSize, rec.OutputTokens, rec.ToolCalls)
	if pred == nil {
		return
	}

	if flushed.HasData && flushed.Stress.Severity > 0 {
		b.parallelBuffer.UpdateGPWithStress(pred, &flushed.Stress)
	} else {
		b.parallelBuffer.UpdateGP(pred)
	}
}

func turnSummary(turn int64, rec TurnRecord) string {
	summary := fmt.Sprintf("turn %d: ctx=%d tools=%d", turn, rec.ContextSize, rec.ToolCalls)
	if strings.TrimSpace(rec.Stage) != "" {
		summary += " stage=" + strings.TrimSpace(rec.Stage)
	}
	if strings.TrimSpace(rec.RuntimeProfile) != "" {
		summary += " profile=" + strings.TrimSpace(rec.RuntimeProfile)
	}
	return summary
}

// publishQualityUpdate sends the current GP prediction to the quality publisher.
func (b *HandoffBridge) publishQualityUpdate(rec TurnRecord) {
	b.mu.RLock()
	publisher := b.qualityPublisher
	agentID := b.agent.AgentID()
	b.mu.RUnlock()

	if publisher == nil {
		return
	}

	pred := b.gp.Predict(rec.ContextSize, rec.OutputTokens, rec.ToolCalls)
	if pred == nil {
		return
	}

	publisher(agentID, pred.Mean, pred.StdDev)
}

// briefLookaheadTokens is the context-size lookahead for detecting quality slope.
// Derived from typical per-turn context growth (~2000 tokens).
const briefLookaheadTokens = 2000

// briefSlopeThreshold is the minimum quality drop to trigger brief generation.
const briefSlopeThreshold = 0.05

// maybeGenerateBrief pre-generates a handoff brief when the GP predicts
// declining quality (negative slope). This runs asynchronously and stores
// the result for later injection into PreparedContext.
func (b *HandoffBridge) maybeGenerateBrief(rec TurnRecord) {
	if b.briefGen == nil {
		return
	}

	pred := b.gp.Predict(rec.ContextSize, rec.OutputTokens, rec.ToolCalls)
	if pred == nil {
		return
	}

	futurePred := b.gp.Predict(rec.ContextSize+briefLookaheadTokens, rec.OutputTokens, rec.ToolCalls)
	if futurePred == nil {
		return
	}

	if pred.Mean <= futurePred.Mean+briefSlopeThreshold {
		return
	}

	// Quality is dropping — pre-generate brief.
	msgs := b.prepared.RecentMessages()
	b.briefGen.Generate(context.Background(), msgs, b.config.Descriptor.AgentType, rec.ContextSize, rec.TurnNumber)
}

// buildShiftConfig derives a ShiftConfig from the bridge's current state.
// All values are derived from existing bridge data — no magic numbers.
func (b *HandoffBridge) buildShiftConfig() ShiftConfig {
	obsCount := b.gp.NumObservations()

	// InitialWeight = 1/(observations+1), clamped [0.05, 0.2].
	initial := 1.0 / float64(obsCount+1)
	if initial < 0.05 {
		initial = 0.05
	}
	if initial > 0.2 {
		initial = 0.2
	}

	// QualityThreshold from old agent's pessimistic bound.
	var threshold float64
	b.mu.RLock()
	oldPred := b.gp.PredictLatest()
	b.mu.RUnlock()
	if oldPred != nil {
		threshold = oldPred.Mean - oldPred.StdDev
	}

	// EvalInterval from context check config.
	evalInterval := b.config.ContextConfig.MinCheckInterval

	// MaxShiftDuration from overlap coordinator.
	var maxDuration time.Duration
	if b.overlapCoord != nil {
		maxDuration = b.overlapCoord.TotalTimeout()
	}
	if maxDuration == 0 {
		maxDuration = 2 * time.Minute
	}

	// GP minimum useful observations.
	const gpMinUsefulObs = 10

	return ShiftConfig{
		InitialWeight:    initial,
		WeightStep:       initial,
		EvalInterval:     evalInterval,
		MinNewAgentTurns: gpMinUsefulObs,
		QualityThreshold: threshold,
		MaxShiftDuration: maxDuration,
	}
}

// SetQualityPublisher sets the callback that publishes GP quality
// predictions to the service registry.
func (b *HandoffBridge) SetQualityPublisher(fn func(agentID string, quality, stdDev float64)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.qualityPublisher = fn
}

// SetActivityPublisher installs the UI-facing activity publisher used for
// handoff lifecycle state updates.
func (b *HandoffBridge) SetActivityPublisher(pub events.ActivityPublisher) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activityPub = pub
}

// SetBoardProvider injects the claims board provider for handoff claims.
func (b *HandoffBridge) SetBoardProvider(bp BoardProvider) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.boardProvider = bp
}

// RecordQualitySignal records external quality feedback.
func (b *HandoffBridge) RecordQualitySignal(sig QualitySignal) {
	// Inject behavior signals into fuser so the next Flush incorporates them.
	b.fuser.SetBehaviorSignals(ExtractBehaviorSignals(sig))

	b.qualityHook.OnUserFeedback(sig.FeedbackType, &FeedbackDetails{
		Rating:    sig.Score,
		Comment:   sig.Source,
		Timestamp: sig.Timestamp,
	})
}

// PreserveInFlightWork injects request-scoped metadata and synthetic messages
// into the prepared context before a forced handoff. This is intended for
// recovery paths where the active turn failed before RecordTurn could run.
func (b *HandoffBridge) PreserveInFlightWork(rec TurnRecord, messages ...Message) {
	if b == nil {
		return
	}

	b.captureTraceMeta(rec)
	b.syncPreparedMetadata(rec)

	for _, msg := range messages {
		msg.Role = strings.TrimSpace(msg.Role)
		msg.Content = strings.TrimSpace(msg.Content)
		if msg.Role == "" || msg.Content == "" {
			continue
		}
		if msg.Timestamp.IsZero() {
			msg.Timestamp = time.Now()
		}
		if msg.TokenCount <= 0 {
			msg.TokenCount = estimateTokens(msg.Content)
		}
		b.prepared.AddMessage(msg)
	}
}

// ForceHandoff triggers an immediate handoff with the given reason.
func (b *HandoffBridge) ForceHandoff(ctx context.Context, reason string) error {
	result := b.manager.ForceHandoff(ctx, reason)
	if result == nil {
		return fmt.Errorf("handoff returned nil result")
	}
	if !result.Success {
		return fmt.Errorf("handoff failed: %s", result.ErrorMessage)
	}
	return b.handleHandoffResult(result)
}

// QualityScore returns the current quality assessment.
func (b *HandoffBridge) QualityScore() *QualityScore {
	return b.qualityHook.GetCurrentScore()
}

// Profile returns the agent's handoff profile.
func (b *HandoffBridge) Profile() *AgentHandoffProfile {
	return b.profile
}

// ReplaceAgent atomically swaps the agent reference. Called after a
// successful handoff to point this bridge at the new agent instance.
func (b *HandoffBridge) ReplaceAgent(newAgent HandoffableAgent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.agent = newAgent
	if setter, ok := newAgent.(interface{ SetHandoffBridge(*HandoffBridge) }); ok {
		setter.SetHandoffBridge(b)
	}
	b.turnCount.Store(0)
	b.prepared.Clear()
}

// Agent returns the current agent.
func (b *HandoffBridge) Agent() HandoffableAgent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.agent
}

// Supervisor returns the shared supervisor that owns this bridge.
func (b *HandoffBridge) Supervisor() *HandoffSupervisor {
	if b == nil {
		return nil
	}
	return b.supervisor
}

// monitorLoop drains the context check notification channel and handles
// recommendations. Runs until Stop() is called.
func (b *HandoffBridge) monitorLoop() {
	defer close(b.doneCh)

	notifications := b.contextCheck.Notifications()
	for {
		select {
		case <-b.stopCh:
			return
		case rec, ok := <-notifications:
			if !ok {
				return
			}
			b.handleRecommendation(rec)
		}
	}
}

// handleRecommendation preserves the legacy async notification path, but the
// bridge now triggers handoff synchronously from RecordTurn via triggerHandoff.
func (b *HandoffBridge) handleRecommendation(rec *HandoffRecommendation) {
	b.triggerHandoff(rec)
}

func (b *HandoffBridge) triggerHandoff(rec *HandoffRecommendation) {
	if rec == nil || !rec.ShouldHandoff {
		return
	}
	if !b.handoffInProgress.CompareAndSwap(false, true) {
		b.emitHandoffTrace("handoff_trigger_ignored", "info", nil, map[string]any{
			"trigger":             rec.Trigger.String(),
			"reason":              rec.Reason,
			"context_utilization": rec.Metrics.ContextUtilization,
			"cause":               "handoff_already_in_progress",
		})
		return
	}
	defer b.handoffInProgress.Store(false)

	b.emitHandoffTrace("handoff_triggered", "info", nil, map[string]any{
		"trigger":             rec.Trigger.String(),
		"reason":              rec.Reason,
		"urgency":             rec.Urgency,
		"context_utilization": rec.Metrics.ContextUtilization,
		"predicted_quality":   rec.Metrics.PredictedQuality,
		"quality_uncertainty": rec.Metrics.QualityUncertainty,
	})
	b.emitHandoffActivity(events.EventTypeAgentDecision, "Context handoff triggered", events.OutcomePending, "triggered", rec.Metrics.ContextUtilization)

	// Post handoff trigger claim with failed validation.
	b.mu.RLock()
	bp := b.boardProvider
	agentID := ""
	if b.agent != nil {
		agentID = b.agent.AgentID()
	}
	b.mu.RUnlock()

	handoffPostClaim(bp, context.Background(),
		claims.Action{AgentID: "handoff_bridge", Type: claims.ActionTypeHandoff},
		handoffClaim(
			"Handoff "+agentID+" — "+rec.Trigger.String(),
			rec.Reason,
			agentID,
			[]claims.ClaimScopeEntry{
				{Kind: "agent", Key: agentID},
				{Kind: "resource", Key: "context_window"},
			},
			[]*claims.Validation{
				handoffFailedValidation(claims.ValidationTypeInspection, true,
					fmt.Sprintf("Context usage at %.0f%%, trigger: %s", rec.Metrics.ContextUtilization*100, rec.Trigger.String()),
					"Context usage below critical threshold"),
			},
		),
	)

	b.mu.RLock()
	category := b.config.Descriptor.Category
	b.mu.RUnlock()

	switch category {
	case CategoryKnowledge:
		b.executeKnowledgeEviction()
	case CategoryStandalone, CategoryPipeline:
		b.executeStandaloneHandoff(rec)
	}
}

// executeStandaloneHandoff performs a full handoff to a new agent instance.
// This path is intentionally single-owner: the current agent stays authoritative
// until the replacement is fully created, hydrated, and applied. We do not
// register a second visible active agent during this transition.
func (b *HandoffBridge) executeStandaloneHandoff(rec *HandoffRecommendation) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b.refreshPreparedBrief(ctx, rec)
	if b.briefGen != nil {
		b.briefGen.SetOnPreparedContext(b.prepared)
	}
	b.emitHandoffTrace("handoff_execute_started", "info", nil, map[string]any{
		"mode":                "direct",
		"trigger":             rec.Trigger.String(),
		"reason":              rec.Reason,
		"context_utilization": rec.Metrics.ContextUtilization,
	})
	b.emitHandoffActivity(events.EventTypeAgentAction, "Executing context handoff", events.OutcomePending, "executing", rec.Metrics.ContextUtilization)

	decision := &HandoffDecision{
		ShouldHandoff: true,
		Reason:        rec.Reason,
		Trigger:       rec.Trigger,
		Confidence:    rec.Urgency,
		Timestamp:     time.Now(),
	}

	transfer, err := b.manager.GetExecutor().PrepareTransfer(decision, b.prepared)
	if err != nil {
		b.emitHandoffTrace("handoff_execute_failed", "error", err, map[string]any{
			"mode": "direct",
			"step": "prepare_transfer",
		})
		b.emitHandoffActivity(events.EventTypeAgentError, "Context handoff failed", events.OutcomeFailure, "failed", rec.Metrics.ContextUtilization)
		return
	}

	// Attach board metadata so the new agent can use board-first injection.
	b.mu.RLock()
	transferSessionID := b.lastTrace.SessionID
	transferBP := b.boardProvider
	b.mu.RUnlock()
	if transfer.Metadata == nil {
		transfer.Metadata = make(map[string]string)
	}
	if transferSessionID != "" {
		transfer.Metadata["session_id"] = transferSessionID
	}
	if transferBP != nil {
		if board := transferBP(); board != nil {
			transfer.Metadata["board_id"] = board.BoardID()
		}
	}

	result := b.manager.GetExecutor().ExecuteHandoff(ctx, transfer)
	if result == nil || !result.Success {
		var execErr error
		if result != nil {
			execErr = result.Error
			if execErr == nil && strings.TrimSpace(result.ErrorMessage) != "" {
				execErr = fmt.Errorf("%s", result.ErrorMessage)
			}
		}
		b.emitHandoffTrace("handoff_execute_failed", "error", execErr, map[string]any{
			"mode": "direct",
			"step": "execute",
		})
		b.emitHandoffActivity(events.EventTypeAgentError, "Context handoff failed", events.OutcomeFailure, "failed", rec.Metrics.ContextUtilization)
		return
	}

	if err := b.handleHandoffResult(result); err != nil {
		b.emitHandoffTrace("handoff_execute_failed", "error", err, map[string]any{
			"mode": "direct",
			"step": "apply_result",
		})
		b.emitHandoffActivity(events.EventTypeAgentError, "Context handoff failed", events.OutcomeFailure, "failed", rec.Metrics.ContextUtilization)
		return
	}
}

// executeOverlapHandoff uses the parallel buffer's pre-materialized snapshot
// to begin an overlap where both agents run in parallel during the transition.
// Instead of an atomic swap, it initiates a gradual traffic shift: the new
// agent receives canary traffic first and weight ramps up as GP quality is
// confirmed.
func (b *HandoffBridge) executeOverlapHandoff(_ *HandoffRecommendation) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b.refreshPreparedBrief(ctx, nil)
	if b.briefGen != nil {
		b.briefGen.SetOnPreparedContext(b.prepared)
	}

	// Snapshot is already materialized — O(1) atomic read.
	snapshot := b.parallelBuffer.Snapshot()
	if snapshot == nil || snapshot.TotalTokens == 0 {
		return
	}

	oldID := b.Agent().AgentID()

	// Freeze elastic sizer during overlap.
	b.parallelBuffer.elastic.Freeze()

	// Begin overlap — creates new agent in parallel.
	handle, err := b.overlapCoord.BeginOverlap(
		oldID, snapshot,
		b.supervisor.factoryAdapter,
	)
	if err != nil {
		b.parallelBuffer.elastic.Reset()
		return
	}

	// Wait for new agent to be ready (context injected).
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := handle.WaitDrain(waitCtx); err != nil {
		handle.Abort(err.Error())
		b.parallelBuffer.elastic.Reset()
		return
	}

	newID := handle.NewAgent.AgentID()

	// Register the new agent alongside the old in the Guide's routing table.
	if b.supervisor.onShiftBegin != nil {
		b.supervisor.onShiftBegin(oldID, handle.NewAgent)
	}

	// Build shift config from bridge state and start gradual traffic shift.
	shiftCfg := b.buildShiftConfig()
	shift := NewTrafficShiftController(
		shiftCfg,
		b.supervisor.updateWeight,
		func(agentID string) (float64, float64, bool) {
			return b.getNewAgentQuality(agentID)
		},
		func(completedOldID, completedNewID string) {
			b.completeTrafficShift(handle, completedOldID, completedNewID)
		},
		func(abortedOldID, abortedNewID string) {
			b.rollbackTrafficShift(handle, abortedOldID, abortedNewID)
		},
		func(entry ShiftWALEntry) {
			if b.supervisor.wal != nil {
				_ = b.supervisor.wal.WriteShiftEvent(entry)
			}
		},
	)

	b.mu.Lock()
	b.activeShift = shift
	b.mu.Unlock()

	if err := shift.BeginShift(oldID, newID); err != nil {
		handle.Abort(err.Error())
		b.parallelBuffer.elastic.Reset()
		b.mu.Lock()
		b.activeShift = nil
		b.mu.Unlock()
	}
}

func (b *HandoffBridge) refreshPreparedBrief(ctx context.Context, rec *HandoffRecommendation) {
	if b == nil || b.briefGen == nil {
		return
	}

	var (
		contextSize int
		turnNumber  int
	)
	if contextSize <= 0 {
		contextSize = b.prepared.TokenCount()
	}
	if turnNumber <= 0 {
		turnNumber = int(b.turnCount.Load())
		if turnNumber <= 0 {
			turnNumber = 1
		}
	}

	if _, err := b.briefGen.Refresh(ctx, b.config.Descriptor.AgentType, contextSize, turnNumber); err != nil {
		b.emitHandoffTrace("handoff_brief_refresh_failed", "warn", err, map[string]any{
			"context_size": contextSize,
			"turn_number":  turnNumber,
		})
	}
}

// getNewAgentQuality reads the GP prediction for an agent from the
// supervisor's bridge registry.
func (b *HandoffBridge) getNewAgentQuality(agentID string) (mean, lowerBound float64, ok bool) {
	bridge := b.supervisor.GetBridge(agentID)
	if bridge == nil {
		return 0, 0, false
	}
	pred := bridge.gp.PredictLatest()
	if pred == nil {
		return 0, 0, false
	}
	return pred.Mean, pred.LowerBound, true
}

// completeTrafficShift is called when the gradual shift converges. It
// finalizes the overlap by completing the handle, replacing the agent,
// and resetting the parallel buffer.
func (b *HandoffBridge) completeTrafficShift(handle *OverlapHandle, oldID, newID string) {
	if err := handle.Complete(); err != nil {
		return
	}

	if b.supervisor.onAgentReplaced != nil {
		_ = b.supervisor.onAgentReplaced(oldID, newID, handle.NewAgent)
	}

	b.ReplaceAgent(handle.NewAgent)
	b.parallelBuffer.Reset()

	b.mu.Lock()
	b.activeShift = nil
	b.mu.Unlock()

	if b.supervisor.wal != nil {
		_ = b.supervisor.wal.WriteHandoffOutcome(
			&HandoffDecision{
				ShouldHandoff: true,
				Reason:        "traffic shift converged",
				Timestamp:     time.Now(),
			},
			true,
		)
	}
}

// rollbackTrafficShift is called when the new agent fails quality checks.
// It aborts the overlap, resets weights, and deregisters the new agent.
func (b *HandoffBridge) rollbackTrafficShift(handle *OverlapHandle, _, _ string) {
	handle.Abort("traffic shift quality rollback")
	b.parallelBuffer.elastic.Reset()

	b.mu.Lock()
	b.activeShift = nil
	b.mu.Unlock()

	if b.supervisor.wal != nil {
		_ = b.supervisor.wal.WriteHandoffOutcome(
			&HandoffDecision{
				ShouldHandoff: true,
				Reason:        "traffic shift aborted — quality rollback",
				Timestamp:     time.Now(),
			},
			false,
		)
	}
}

// handleHandoffResult processes a successful handoff, taking the new agent
// from the factory adapter and registering it.
func (b *HandoffBridge) handleHandoffResult(result *HandoffResult) error {
	newAgent, ok := b.supervisor.factoryAdapter.TakeAgent(result.NewSessionID)
	if !ok {
		b.emitHandoffTrace("handoff_result_apply_failed", "error", fmt.Errorf("no agent found for session %q", result.NewSessionID), nil)
		return fmt.Errorf("no agent found for session %q", result.NewSessionID)
	}

	b.mu.RLock()
	oldID := b.agent.AgentID()
	outgoing := b.agent
	bp := b.boardProvider
	sessionID := ""
	if b.lastTrace.SessionID != "" {
		sessionID = b.lastTrace.SessionID
	}
	b.mu.RUnlock()

	// Old agent testament: state extracted.
	handoffSubmitTestament(bp, context.Background(), handoffTestament(
		sessionID, oldID,
		"Extracted archivable state for handoff",
		"committed",
		[]*claims.Artifact{
			handoffArtifact(sessionID, oldID, "context_metrics", fmt.Sprintf("turns=%d, session=%s", b.turnCount.Load(), result.NewSessionID)),
			handoffJSONArtifact(sessionID, oldID, "transfer_metrics", result.Metrics),
		},
	))

	// HAND-05: emit an ArchiveRequest for the outgoing agent BEFORE the
	// replacement swap.
	b.publishArchiveRequest(outgoing, ArchiveReasonHandoffComplete)

	// Notify supervisor of replacement.
	if b.supervisor.onAgentReplaced != nil {
		if err := b.supervisor.onAgentReplaced(oldID, newAgent.AgentID(), newAgent); err != nil {
			return fmt.Errorf("agent replacement callback: %w", err)
		}
	}

	b.ReplaceAgent(newAgent)

	// New agent testament: context injected and ready.
	handoffSubmitTestament(bp, context.Background(), handoffTestament(
		sessionID, newAgent.AgentID(),
		"Injected prepared context, resuming work",
		"tentative",
		[]*claims.Artifact{
			handoffArtifact(sessionID, newAgent.AgentID(), "new_session_id", result.NewSessionID),
			handoffArtifact(sessionID, newAgent.AgentID(), "tokens_transferred", fmt.Sprintf("%d", result.Metrics.TokensTransferred)),
		},
	))

	// Post continuation claim so the new agent picks up directed work.
	if bp != nil {
		if board := bp(); board != nil {
			handoffPostClaim(bp, context.Background(),
				claims.Action{AgentID: "handoff_bridge", Type: claims.ActionTypeHandoff},
				handoffClaim(
					"Continue work from handoff",
					"Previous agent handed off. Resume directed claims.",
					newAgent.AgentID(),
					nil, nil,
				),
			)
		}
	}

	b.emitHandoffTrace("handoff_execute_completed", "info", nil, map[string]any{
		"new_agent_id": newAgent.AgentID(),
		"session_id":   result.NewSessionID,
		"duration_ms":  result.Duration.Milliseconds(),
	})
	b.emitHandoffActivity(events.EventTypeSuccess, "Context handoff complete", events.OutcomeSuccess, "completed", 0)

	// Persist outcome.
	if b.supervisor.wal != nil {
		_ = b.supervisor.wal.WriteHandoffOutcome(
			&HandoffDecision{
				ShouldHandoff: true,
				Reason:        "standalone handoff complete",
				Timestamp:     time.Now(),
			},
			true,
		)
	}

	return nil
}

// publishArchiveRequest fires an ArchiveRequest at the installed
// publisher. HAND-05 contract: fire-and-forget — the publisher must
// return quickly and any errors are recorded on the trace but never
// propagated to the handoff's critical path. A nil publisher is a
// valid configuration (archival disabled); the method becomes a
// no-op in that case.
func (b *HandoffBridge) publishArchiveRequest(agent HandoffableAgent, reason ArchiveReason) {
	b.mu.RLock()
	publisher := b.archivePublisher
	meta := b.lastTrace
	b.mu.RUnlock()
	if publisher == nil {
		return
	}
	req := newArchiveRequestFromBridge(agent, reason, meta.SessionID, meta.CorrelationID)
	if req == nil {
		return
	}
	// Use a detached context — the publisher may outlive the handoff's
	// ctx when it buffers or batches, and we never want archival writes
	// cancelled because the triggering ctx finished.
	if err := publisher.PublishArchive(context.Background(), req); err != nil {
		b.emitHandoffTrace("archive_publish_failed", "warn", err, map[string]any{
			"reason":    string(reason),
			"agent_id":  req.SourceAgentID,
			"agent_typ": req.AgentType,
		})
	}
}

func (b *HandoffBridge) observeContextEvaluation(ctx *ContextState, recommendation *HandoffRecommendation) {
	if ctx == nil {
		return
	}
	data := map[string]any{
		"agent_id":            ctx.GetAgentID(),
		"agent_type":          ctx.GetAgentType(),
		"context_size":        ctx.ContextSize,
		"max_context_size":    ctx.MaxContextSize,
		"context_utilization": ctx.Utilization(),
		"token_count":         ctx.TokenCount,
		"tool_call_count":     ctx.ToolCallCount,
		"turn_number":         ctx.TurnNumber,
		"should_handoff":      false,
	}
	if recommendation != nil {
		data["should_handoff"] = recommendation.ShouldHandoff
		data["urgency"] = recommendation.Urgency
		if recommendation.Trigger != TriggerNone {
			data["trigger"] = recommendation.Trigger.String()
		}
		if recommendation.Reason != "" {
			data["reason"] = recommendation.Reason
		}
		if recommendation.Decision != nil {
			data["predicted_quality"] = recommendation.Decision.Factors.PredictedQuality
			data["quality_uncertainty"] = recommendation.Decision.Factors.QualityUncertainty
		}
	}
	b.emitHandoffTrace("handoff_context_evaluated", "info", nil, data)
}

func (b *HandoffBridge) captureTraceMeta(rec TurnRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if rec.EventLogger != nil {
		b.lastTrace.EventLogger = rec.EventLogger
	}
	if strings.TrimSpace(rec.CorrelationID) != "" {
		b.lastTrace.CorrelationID = strings.TrimSpace(rec.CorrelationID)
	}
	if strings.TrimSpace(rec.SessionID) != "" {
		b.lastTrace.SessionID = strings.TrimSpace(rec.SessionID)
	}
}

func (b *HandoffBridge) syncPreparedMetadata(rec TurnRecord) {
	b.prepared.SetMetadata("agent_id", b.agent.AgentID())
	b.prepared.SetMetadata("agent_type", b.agent.AgentType())
	if trimmed := strings.TrimSpace(rec.CorrelationID); trimmed != "" {
		b.prepared.SetMetadata("correlation_id", trimmed)
	}
	if trimmed := strings.TrimSpace(rec.SessionID); trimmed != "" {
		b.prepared.SetMetadata("session_id", trimmed)
	}
	if state := b.agent.ExtractArchivableState(); state != nil {
		for key, value := range state.State {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				continue
			}
			b.prepared.SetMetadata(key, value)
		}
	}
}

func (b *HandoffBridge) emitHandoffTrace(event, level string, err error, data map[string]any) {
	b.mu.RLock()
	meta := b.lastTrace
	b.mu.RUnlock()
	if meta.EventLogger == nil {
		return
	}
	payload := b.decorateHandoffData(data)
	entry := agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     level,
		Agent:     b.agent.AgentID(),
		SessionID: meta.SessionID,
		Event:     event,
		EventCode: agentlog.EventHandoffTriggered,
		CorrID:    meta.CorrelationID,
		Data:      payload,
	}
	if err != nil {
		entry.Error = err.Error()
	}
	meta.EventLogger.LogEvent(entry)
}

func (b *HandoffBridge) emitHandoffActivity(
	eventType events.EventType,
	content string,
	outcome events.EventOutcome,
	handoffState string,
	contextUsage float64,
) {
	b.mu.RLock()
	pub := b.activityPub
	meta := b.lastTrace
	b.mu.RUnlock()
	if pub == nil {
		return
	}
	payload := b.decorateHandoffData(map[string]any{
		"handoff_state": handoffState,
		"context_usage": contextUsage,
	})
	if handoffState == "completed" {
		payload["context_tokens"] = 0
	}
	evt := events.NewActivityEvent(eventType, meta.SessionID, content)
	evt.CorrelationID = meta.CorrelationID
	evt.AgentID = b.agent.AgentID()
	evt.Outcome = outcome
	// Context handoffs are an internal LLM-context-window-management
	// mechanism — when an agent's context fills, it hands off to a
	// fresh instance and transfers state. The user doesn't need (or
	// want) to see this in the agent panel; it's housekeeping, not
	// real agent work. Painting it as user-visible activity makes
	// agents look like they're doing work during plan review when
	// they're really just rotating their context. System visibility
	// keeps the events for debugging/telemetry but suppresses them
	// from the user-facing agent panel.
	evt.Visibility = events.VisibilitySystem
	evt.Data = payload
	pub.PublishActivity(evt)
}

func (b *HandoffBridge) decorateHandoffData(data map[string]any) map[string]any {
	payload := make(map[string]any, len(data)+8)
	for key, value := range data {
		payload[key] = value
	}
	payload["agent_type"] = b.agent.AgentType()
	payload["agent_name"] = handoffAgentDisplayName(b.agent.AgentType())
	if state := b.agent.ExtractArchivableState(); state != nil {
		if state.State != nil {
			if pipelineID, ok := state.State["pipeline_id"]; ok {
				if pipelineID = strings.TrimSpace(pipelineID); pipelineID != "" {
					payload["pipeline_id"] = pipelineID
					if _, exists := payload["task_id"]; !exists {
						payload["task_id"] = pipelineID
					}
				}
			}
			if taskID, ok := state.State["task_id"]; ok {
				if taskID = strings.TrimSpace(taskID); taskID != "" {
					payload["task_id"] = taskID
				}
			}
			if taskSlug, ok := state.State["task_slug"]; ok {
				if taskSlug = strings.TrimSpace(taskSlug); taskSlug != "" {
					payload["task_slug"] = taskSlug
				}
			}
		}
	}
	return payload
}

func handoffAgentDisplayName(agentType string) string {
	switch strings.TrimSpace(agentType) {
	case "tester-pipeline":
		return "Pipeline Tester"
	case "inspector-pipeline":
		return "Pipeline Inspector"
	case "engineer":
		return "Engineer"
	case "designer":
		return "Designer"
	case "tester":
		return "Tester"
	case "inspector":
		return "Inspector"
	case "architect":
		return "Architect"
	case "orchestrator":
		return "Orchestrator"
	case "guardian":
		return "Guardian"
	case "academic":
		return "Academic"
	case "librarian":
		return "Librarian"
	case "archivalist":
		return "Archivalist"
	case "guide":
		return "Guide"
	default:
		return strings.TrimSpace(agentType)
	}
}

// executeKnowledgeEviction evicts low-value entries from a knowledge agent
// instead of performing a full handoff.
func (b *HandoffBridge) executeKnowledgeEviction() {
	evictable, ok := b.agent.(ContextEvictable)
	if !ok || b.eviction == nil {
		return
	}

	// Collect entries from prepared context messages as evictable candidates.
	msgs := b.prepared.RecentMessages()
	entries := make([]EvictableEntry, 0, len(msgs))
	for i := range msgs {
		entries = append(entries, &BasicEvictableEntry{
			ID:          fmt.Sprintf("msg-%d", i),
			TokenCount:  msgs[i].TokenCount,
			Timestamp:   msgs[i].Timestamp,
			TurnNumber:  i,
			ContentType: msgs[i].Role,
		})
	}

	// Target freeing 25% of context window.
	targetTokens := b.config.Descriptor.ContextWindow / 4

	ctx := context.Background()
	selected, err := b.eviction.SelectForEviction(ctx, entries, targetTokens)
	if err != nil || len(selected) == 0 {
		return
	}

	// Build EvictionCandidate list for the agent.
	candidates := make([]EvictionCandidate, len(selected))
	for i, entry := range selected {
		candidates[i] = EvictionCandidate{Entry: entry}
	}

	freedTokens, err := evictable.EvictEntries(candidates)
	if err != nil {
		return
	}

	// Record eviction as GP observation.
	b.gp.AddObservation(NewGPObservation(
		b.config.Descriptor.ContextWindow-freedTokens,
		freedTokens,
		0,
		0.7, // Eviction is a neutral quality event.
	))
}
