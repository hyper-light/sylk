package handoff

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

	turnCount atomic.Int64
	started   atomic.Bool
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// NewHandoffBridge creates a bridge for the given agent, wired to the supervisor.
func NewHandoffBridge(cfg BridgeConfig, agent HandoffableAgent, sup *HandoffSupervisor) *HandoffBridge {
	desc := cfg.Descriptor

	gp := NewAgentGaussianProcess(nil)
	profile := NewAgentHandoffProfile(desc.AgentType, desc.ModelID, agent.AgentID())
	controller := NewHandoffController(gp, profile, nil)

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
		config:       cfg,
		agent:        agent,
		supervisor:   sup,
		manager:      manager,
		qualityHook:  qualityHook,
		gp:           gp,
		profile:      profile,
		contextCheck: contextCheck,
		eviction:     eviction,
		prepared:     prepared,
		fuser:        fuser,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
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

	return b
}

// Start launches the bridge's monitor loop and starts the context check hook.
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

	go b.monitorLoop()
	return nil
}

// Stop shuts down the bridge, stopping the monitor loop, parallel buffer,
// and context check.
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

	return b.contextCheck.Stop()
}

// RecordTurn records metrics from a single agent turn. This is the hot path
// called on every turn — it feeds data to the GP, quality hook, prepared
// context, profile learner, and context check.
func (b *HandoffBridge) RecordTurn(rec TurnRecord) {
	turn := b.turnCount.Add(1)

	// Inject provider/stream signals into fuser.
	b.injectProviderSignals(rec)
	b.injectStreamSignals(rec)

	// Flush fuser to get composite quality and stress.
	flushed := b.fuser.Flush(rec.ToolCalls, rec.ToolSuccesses)
	quality := b.turnQuality(flushed, rec)

	// Build GP observation — still 3D.
	gpObs := NewGPObservation(rec.ContextSize, rec.OutputTokens, rec.ToolCalls, quality)
	b.gp.AddObservation(gpObs)

	// Feed prepared context with a synthetic message.
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

	// Notify context check (async, non-blocking).
	b.contextCheck.OnContextUpdateAsync(&ContextState{
		ContextSize:    rec.ContextSize,
		MaxContextSize: b.config.Descriptor.ContextWindow,
		TokenCount:     rec.OutputTokens,
		ToolCallCount:  rec.ToolCalls,
		TurnNumber:     rec.TurnNumber,
	})

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
	b.turnCount.Store(0)
	b.prepared.Clear()
}

// Agent returns the current agent.
func (b *HandoffBridge) Agent() HandoffableAgent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.agent
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

// handleRecommendation processes a handoff recommendation from the context check.
func (b *HandoffBridge) handleRecommendation(rec *HandoffRecommendation) {
	if rec == nil || !rec.ShouldHandoff {
		return
	}

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
// If a parallel buffer is available, uses the overlap-based path where the
// snapshot is already materialized. Falls back to synchronous handoff otherwise.
func (b *HandoffBridge) executeStandaloneHandoff(rec *HandoffRecommendation) {
	if b.parallelBuffer != nil && b.overlapCoord != nil {
		b.executeOverlapHandoff(rec)
		return
	}

	// Fallback: synchronous handoff (no parallel buffer).
	if b.briefGen != nil {
		b.briefGen.SetOnPreparedContext(b.prepared)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	decision := &HandoffDecision{
		ShouldHandoff: true,
		Reason:        rec.Reason,
		Trigger:       rec.Trigger,
		Confidence:    rec.Urgency,
		Timestamp:     time.Now(),
	}

	transfer, err := b.manager.GetExecutor().PrepareTransfer(decision, b.prepared)
	if err != nil {
		return
	}

	result := b.manager.GetExecutor().ExecuteHandoff(ctx, transfer)
	if result == nil || !result.Success {
		return
	}

	_ = b.handleHandoffResult(result)
}

// executeOverlapHandoff uses the parallel buffer's pre-materialized snapshot
// to begin an overlap where both agents run in parallel during the transition.
// Instead of an atomic swap, it initiates a gradual traffic shift: the new
// agent receives canary traffic first and weight ramps up as GP quality is
// confirmed.
func (b *HandoffBridge) executeOverlapHandoff(_ *HandoffRecommendation) {
	// Inject latest brief into prepared context before snapshot.
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := handle.WaitDrain(ctx); err != nil {
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
		return fmt.Errorf("no agent found for session %q", result.NewSessionID)
	}

	b.mu.RLock()
	oldID := b.agent.AgentID()
	b.mu.RUnlock()

	// Notify supervisor of replacement.
	if b.supervisor.onAgentReplaced != nil {
		if err := b.supervisor.onAgentReplaced(oldID, newAgent.AgentID(), newAgent); err != nil {
			return fmt.Errorf("agent replacement callback: %w", err)
		}
	}

	b.ReplaceAgent(newAgent)

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
