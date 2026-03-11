package activation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/container/pod"
	"github.com/adalundhe/sylk/core/handoff"
)

var (
	activationDebugLog     *slog.Logger
	activationDebugLogOnce sync.Once
)

func activationFileLog() *slog.Logger {
	activationDebugLogOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".sylk", "logs")
		_ = os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(filepath.Join(dir, "ui_events.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			activationDebugLog = slog.Default()
			return
		}
		activationDebugLog = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	return activationDebugLog
}

var (
	ErrControllerClosed     = errors.New("activation controller is closed")
	ErrUnknownAgentType     = errors.New("unknown agent type")
	ErrAlreadyAtOrBelowTier = errors.New("already at or below target tier")
)

// StatePreserver extracts and injects agent state for Cool tier transitions.
type StatePreserver interface {
	Extract(agent container.ContainerAgent) (*handoff.ArchivableState, error)
	Inject(agent container.ContainerAgent, state *handoff.ArchivableState) error
}

// DefaultStatePreserver uses the handoff.HandoffableAgent interface for
// extraction and carries ArchivableState through PreparedContext metadata
// for injection.
type DefaultStatePreserver struct{}

func (d *DefaultStatePreserver) Extract(agent container.ContainerAgent) (*handoff.ArchivableState, error) {
	if h, ok := agent.(handoff.HandoffableAgent); ok {
		return h.ExtractArchivableState(), nil
	}
	return nil, nil
}

func (d *DefaultStatePreserver) Inject(agent container.ContainerAgent, state *handoff.ArchivableState) error {
	if state == nil {
		return nil
	}
	injectable, ok := agent.(handoff.HandoffInjectable)
	if !ok {
		return nil // agent doesn't support injection
	}

	// Build a PreparedContext and carry the ArchivableState through
	// metadata so the agent can restore its internal state.
	pc := handoff.NewPreparedContextDefault()
	for k, v := range state.State {
		pc.SetMetadata(k, v)
	}
	pc.SetMetadata("_archivable_agent_id", state.AgentID)
	pc.SetMetadata("_archivable_agent_type", state.AgentType)
	if !state.Timestamp.IsZero() {
		ts, err := json.Marshal(state.Timestamp)
		if err == nil {
			pc.SetMetadata("_archivable_timestamp", string(ts))
		}
	}
	return injectable.InjectPreparedContext(pc)
}

// ActivationControllerConfig provides construction parameters.
type ActivationControllerConfig struct {
	Runtime      container.ContainerRuntime
	Registry     *container.ContainerRegistry
	Scope        *concurrency.GoroutineScope
	Preserver    StatePreserver
	Policies     []*ActivationPolicy
	StorageDir   string          // For CoolStore state files
	WarmPoolCap  int             // Max Warm containers
	CoolStoreCap int             // Max Cool entries on disk
	Predictor    PredictorConfig // Co-activation predictor tuning
	Pressure     PressureConfig  // Pressure evaluation tuning
	ScanPeriod   int             // Idle scan period in ms (0 = derived)
	Logger       *slog.Logger    // Structured logger (nil = slog.Default())
	OnActivated  func(*container.Container)
	OnRemoved    func(*container.Container)
}

// PressureSource evaluates resource pressure and produces eviction candidates.
type PressureSource interface {
	IsUnderPressure() bool
	Evaluate(entries []*ActivationEntry) []EvictionCandidate
}

// ActivationController coordinates on-demand agent activation with
// tiered state management. Thread-safe; all public methods are safe
// for concurrent use.
type ActivationController struct {
	mu        sync.RWMutex
	entries   map[string]*ActivationEntry // agentType -> entry
	gate      *ActivationGate
	warmPool  *WarmPool
	coolStore *CoolStore
	predictor *ActivationPredictor
	idle      *IdleMonitor
	pressure  PressureSource
	runtime   container.ContainerRuntime
	registry  *container.ContainerRegistry
	preserver StatePreserver
	scope     *concurrency.GoroutineScope
	metrics   *ActivationMetrics
	logger    *slog.Logger
	closed    atomic.Bool

	onActivated func(*container.Container)
	onRemoved   func(*container.Container)
}

// NewActivationController creates and initializes the controller.
// Does not start the idle monitor — call Start() after construction.
func NewActivationController(cfg ActivationControllerConfig) (*ActivationController, error) {
	coolStore, err := NewCoolStore(cfg.StorageDir, cfg.CoolStoreCap)
	if err != nil {
		return nil, err
	}

	preserver := cfg.Preserver
	if preserver == nil {
		preserver = &DefaultStatePreserver{}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	coolStore.SetLogger(logger)

	ac := &ActivationController{
		entries:     make(map[string]*ActivationEntry, len(cfg.Policies)),
		gate:        NewActivationGate(),
		warmPool:    NewWarmPool(cfg.WarmPoolCap),
		coolStore:   coolStore,
		predictor:   NewActivationPredictor(cfg.Predictor),
		runtime:     cfg.Runtime,
		registry:    cfg.Registry,
		preserver:   preserver,
		scope:       cfg.Scope,
		metrics:     &ActivationMetrics{},
		logger:      logger,
		onActivated: cfg.OnActivated,
		onRemoved:   cfg.OnRemoved,
	}

	for _, policy := range cfg.Policies {
		ac.entries[policy.AgentType] = &ActivationEntry{
			AgentType: policy.AgentType,
			Spec:      ac.specForAgent(policy.AgentType),
			Policy:    policy,
		}
	}

	return ac, nil
}

// Start launches the idle monitor, pressure evaluator, and pre-warms
// entries that have PreWarmOnStartup set.
func (ac *ActivationController) Start(budget *concurrency.GoroutineBudget, quota *container.ResourceQuota) error {
	ac.pressure = NewPressureEvaluator(budget, quota, PressureConfig{})
	ac.idle = NewIdleMonitor(ac, ac.scope, 0)
	if err := ac.idle.Start(); err != nil {
		return err
	}
	ac.warmStartupEntries()
	return nil
}

// warmStartupEntries launches pre-warm activations for entries with
// PreWarmOnStartup. Daemon entries are skipped — they are created by
// DaemonSetController and adopted via AdoptContainer.
func (ac *ActivationController) warmStartupEntries() {
	for _, entry := range ac.allEntries() {
		if !entry.Policy.PreWarmOnStartup || entry.Policy.IsDaemon {
			continue
		}
		if entry.LoadTier() >= TierWarm {
			continue
		}
		if err := ac.prepareWarmEntry(context.Background(), entry); err != nil {
			ac.logger.Warn("startup pre-warm failed",
				"agent_type", entry.AgentType,
				"error", err,
			)
			continue
		}
		ac.logger.Info("startup pre-warm complete", "agent_type", entry.AgentType)
	}
}

// EnsureActive guarantees a container of the given type is in TierHot.
// Concurrent callers for the same agentType coalesce on a single activation.
func (ac *ActivationController) EnsureActive(ctx context.Context, agentType string) (*container.Container, error) {
	if ac.closed.Load() {
		return nil, ErrControllerClosed
	}

	ac.metrics.ActivationsTotal.Add(1)

	deadline, hasDeadline := ctx.Deadline()
	activationFileLog().Info("DEBUG: ensure_active_start",
		"agent_type", agentType,
		"has_deadline", hasDeadline,
		"deadline", deadline,
		"ctx_err", ctx.Err())

	entry, err := ac.getEntry(agentType)
	if err != nil {
		activationFileLog().Info("DEBUG: ensure_active_entry_error", "agent_type", agentType, "error", err)
		return nil, err
	}
	if err := ac.waitForWarmPreparation(ctx, entry); err != nil {
		return nil, err
	}

	// Fast path: already hot and running.
	if entry.LoadTier() == TierHot {
		if c := entry.Container.Load(); c != nil && c.IsRunning() {
			ac.metrics.HotHits.Add(1)
			entry.TouchActivity()
			ac.notifyActivated(c)
			activationFileLog().Info("DEBUG: ensure_active_hot_hit", "agent_type", agentType)
			return c, nil
		}
	}

	activateStart := time.Now()
	activationFileLog().Info("DEBUG: ensure_active_gate_enter", "agent_type", agentType, "current_tier", entry.LoadTier())
	c, coalesced, err := ac.gate.DoOrWait(ctx, agentType, func(ctx context.Context) (*container.Container, error) {
		return ac.promote(ctx, entry)
	})
	activationFileLog().Info("DEBUG: ensure_active_gate_exit",
		"agent_type", agentType,
		"coalesced", coalesced,
		"elapsed_ms", time.Since(activateStart).Milliseconds(),
		"error", err)
	if err != nil {
		return nil, err
	}

	if coalesced {
		ac.metrics.CoalescedRequests.Add(1)
	}

	entry.TouchActivity()
	entry.ActivationCount.Add(1)
	ac.notifyActivated(c)

	// Record for predictor and trigger pre-warming asynchronously.
	ac.predictor.Record(agentType)
	ac.preWarmAsync(ctx, agentType)

	return c, nil
}

// TouchActivity marks the agent type as recently active.
func (ac *ActivationController) TouchActivity(agentType string) {
	entry, err := ac.getEntry(agentType)
	if err != nil {
		return
	}
	entry.TouchActivity()
}

// DemoteTo requests demotion of an agent type to the target tier.
// Respects MinTier from policy — will demote as far as allowed,
// stopping at MinTier if target is below it. No-op if already at
// or below target.
func (ac *ActivationController) DemoteTo(ctx context.Context, agentType string, target ActivationTier) error {
	if ac.closed.Load() {
		return ErrControllerClosed
	}

	entry, err := ac.getEntry(agentType)
	if err != nil {
		return err
	}

	current := entry.LoadTier()
	if current <= target {
		return nil
	}

	// Clamp target to MinTier — demote as far as policy allows.
	effectiveTarget := max(target, entry.Policy.MinTier)
	if current <= effectiveTarget {
		return nil
	}

	return ac.demote(ctx, entry, effectiveTarget)
}

// EvictUnderPressure evaluates resource pressure and demotes agents
// until pressure is relieved.
func (ac *ActivationController) EvictUnderPressure(ctx context.Context) {
	if ac.pressure == nil || !ac.pressure.IsUnderPressure() {
		return
	}

	candidates := ac.pressure.Evaluate(ac.allEntries())
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return
		}
		if !ac.pressure.IsUnderPressure() {
			return
		}

		current := candidate.Entry.LoadTier()
		target := current - 1
		if target < TierCold {
			continue
		}

		if err := ac.DemoteTo(ctx, candidate.Entry.AgentType, target); err != nil {
			ac.logger.Warn("pressure-driven demotion failed",
				"agent_type", candidate.Entry.AgentType,
				"target_tier", pod.TierString(target),
				"error", err,
			)
			continue
		}
		ac.metrics.EvictionsByPressure.Add(1)
	}
}

// Metrics returns the activation metrics.
func (ac *ActivationController) Metrics() *ActivationMetrics {
	return ac.metrics
}

// Shutdown gracefully stops all managed containers and the idle monitor.
func (ac *ActivationController) Shutdown(ctx context.Context) error {
	if !ac.closed.CompareAndSwap(false, true) {
		return nil
	}

	if ac.idle != nil {
		ac.idle.Stop()
	}

	ac.mu.RLock()
	entries := make([]*ActivationEntry, 0, len(ac.entries))
	for _, e := range ac.entries {
		entries = append(entries, e)
	}
	ac.mu.RUnlock()

	var firstErr error
	for _, entry := range entries {
		if err := ac.teardownEntry(ctx, entry); err != nil {
			ac.logger.Warn("teardown failed during shutdown",
				"agent_type", entry.AgentType,
				"error", err,
			)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// SetLifecycleCallbacks updates the activation lifecycle callbacks used to
// observe container hot/removal transitions.
func (ac *ActivationController) SetLifecycleCallbacks(
	onActivated func(*container.Container),
	onRemoved func(*container.Container),
) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.onActivated = onActivated
	ac.onRemoved = onRemoved
}

// promote brings an entry to TierHot from whatever tier it currently occupies.
func (ac *ActivationController) promote(ctx context.Context, entry *ActivationEntry) (*container.Container, error) {
	if ctx.Err() != nil {
		activationFileLog().Info("DEBUG: promote_ctx_already_cancelled", "agent_type", entry.AgentType, "error", ctx.Err())
		return nil, ctx.Err()
	}

	// Singleton guard: if a running container already exists, return it
	// and correct the tier. Covers stale-tier scenarios.
	if existing := entry.Container.Load(); existing != nil && existing.IsRunning() {
		entry.StoreTier(TierHot)
		activationFileLog().Info("DEBUG: promote_singleton_guard",
			"agent_type", entry.AgentType, "existing_id", existing.ID())
		return existing, nil
	}

	tier := entry.LoadTier()
	activationFileLog().Info("DEBUG: promote_start", "agent_type", entry.AgentType, "tier", tier)
	promoteStart := time.Now()

	var c *container.Container
	var err error
	switch tier {
	case TierHot:
		c = entry.Container.Load()
	case TierWarm:
		c, err = ac.promoteFromWarm(ctx, entry)
	case TierCool:
		c, err = ac.promoteFromCool(ctx, entry)
	default:
		c, err = ac.promoteFromCold(ctx, entry)
	}

	activationFileLog().Info("DEBUG: promote_done",
		"agent_type", entry.AgentType,
		"from_tier", tier,
		"elapsed_ms", time.Since(promoteStart).Milliseconds(),
		"error", err)
	return c, err
}

func (ac *ActivationController) promoteFromWarm(ctx context.Context, entry *ActivationEntry) (*container.Container, error) {
	c := ac.warmPool.Take(entry.AgentType)
	if c == nil {
		c = entry.Container.Load()
	}
	if c == nil {
		ac.logger.Info("warm pool miss, falling back to cold start",
			"agent_type", entry.AgentType,
		)
		return ac.promoteFromCold(ctx, entry)
	}

	if err := ac.resumeWarmContainer(ctx, c); err != nil {
		ac.logger.Warn("warm resume failed, falling back to cold start",
			"agent_type", entry.AgentType,
			"error", err,
		)
		if removeErr := ac.stopAndRemove(context.Background(), c); removeErr != nil {
			ac.logger.Warn("warm container cleanup failed after resume error",
				"agent_type", entry.AgentType,
				"error", removeErr,
			)
		}
		entry.Container.Store(nil)
		entry.StoreTier(TierCold)
		return ac.promoteFromCold(ctx, entry)
	}

	entry.Container.Store(c)
	entry.StoreTier(TierHot)
	ac.metrics.WarmStarts.Add(1)
	return c, nil
}

func (ac *ActivationController) promoteFromCool(ctx context.Context, entry *ActivationEntry) (*container.Container, error) {
	coolEntry, err := ac.coolStore.Load(entry.AgentType)
	if err != nil {
		ac.logger.Info("cool store miss, falling back to cold start",
			"agent_type", entry.AgentType,
			"error", err,
		)
		return ac.promoteFromCold(ctx, entry)
	}

	c, err := ac.runtime.CreateContainer(ctx, coolEntry.Spec)
	if err != nil {
		return nil, err
	}

	if coolEntry.State != nil && c.Agent() != nil {
		if injectErr := ac.preserver.Inject(c.Agent(), coolEntry.State); injectErr != nil {
			ac.logger.Warn("state injection failed during cool start",
				"agent_type", entry.AgentType,
				"error", injectErr,
			)
		}
	}

	if ctx.Err() != nil {
		_ = ac.runtime.RemoveContainer(context.Background(), c)
		return nil, ctx.Err()
	}

	if err := ac.runtime.StartContainer(ctx, c); err != nil {
		if removeErr := ac.runtime.RemoveContainer(ctx, c); removeErr != nil {
			ac.logger.Warn("container cleanup failed after start error",
				"agent_type", entry.AgentType,
				"error", removeErr,
			)
		}
		return nil, err
	}

	entry.Container.Store(c)
	entry.StoreTier(TierHot)
	if removeErr := ac.coolStore.Remove(entry.AgentType); removeErr != nil {
		ac.logger.Warn("cool store cleanup failed",
			"agent_type", entry.AgentType,
			"error", removeErr,
		)
	}
	ac.metrics.CoolStarts.Add(1)
	return c, nil
}

func (ac *ActivationController) promoteFromCold(ctx context.Context, entry *ActivationEntry) (*container.Container, error) {
	createStart := time.Now()
	activationFileLog().Info("DEBUG: cold_start_create_container", "agent_type", entry.AgentType)
	c, err := ac.runtime.CreateContainer(ctx, entry.Spec)
	activationFileLog().Info("DEBUG: cold_start_create_done",
		"agent_type", entry.AgentType,
		"elapsed_ms", time.Since(createStart).Milliseconds(),
		"error", err)
	if err != nil {
		return nil, err
	}

	// Check cancellation between creation and start — if the scope is
	// shutting down, clean up the created container immediately.
	if ctx.Err() != nil {
		activationFileLog().Info("DEBUG: cold_start_ctx_cancelled_before_start", "agent_type", entry.AgentType, "error", ctx.Err())
		_ = ac.runtime.RemoveContainer(context.Background(), c)
		return nil, ctx.Err()
	}

	startTime := time.Now()
	activationFileLog().Info("DEBUG: cold_start_start_container", "agent_type", entry.AgentType)
	if err := ac.runtime.StartContainer(ctx, c); err != nil {
		activationFileLog().Info("DEBUG: cold_start_start_failed",
			"agent_type", entry.AgentType,
			"elapsed_ms", time.Since(startTime).Milliseconds(),
			"error", err)
		if removeErr := ac.runtime.RemoveContainer(ctx, c); removeErr != nil {
			ac.logger.Warn("container cleanup failed after start error",
				"agent_type", entry.AgentType,
				"error", removeErr,
			)
		}
		return nil, err
	}
	activationFileLog().Info("DEBUG: cold_start_start_done",
		"agent_type", entry.AgentType,
		"elapsed_ms", time.Since(startTime).Milliseconds())

	entry.Container.Store(c)
	entry.StoreTier(TierHot)
	ac.metrics.ColdStarts.Add(1)
	return c, nil
}

// demote transitions an entry down through tiers to reach the target.
func (ac *ActivationController) demote(ctx context.Context, entry *ActivationEntry, target ActivationTier) error {
	current := entry.LoadTier()

	// Step down one tier at a time, respecting MinTier.
	for current > target && current > entry.Policy.MinTier {
		nextTier := current - 1
		if !entry.Policy.CanDemoteTo(nextTier) {
			return nil
		}

		var err error
		switch current {
		case TierHot:
			err = ac.demoteHotToWarm(ctx, entry)
		case TierWarm:
			err = ac.demoteWarmToCool(ctx, entry)
		case TierCool:
			err = ac.demoteCoolToCold(entry)
		}
		if err != nil {
			return err
		}
		next := entry.LoadTier()
		if next >= current {
			return nil
		}
		current = next
	}
	return nil
}

func (ac *ActivationController) demoteHotToWarm(ctx context.Context, entry *ActivationEntry) error {
	c := entry.Container.Load()
	if c == nil {
		entry.StoreTier(TierCold)
		return nil
	}

	if err := ac.runtime.PauseContainer(ctx, c); err != nil {
		return err
	}

	evicted := ac.warmPool.Put(entry.AgentType, c)
	entry.StoreTier(TierWarm)
	ac.metrics.DemotionsToWarm.Add(1)

	// Tear down any evicted container asynchronously.
	if evicted != nil {
		ac.teardownContainerAsync(ctx, evicted)
	}
	return nil
}

// AcquireRequestGuard increments the active-request counter for the given
// agent type and returns a release function. While the counter is > 0,
// demoteWarmToCool and idle demotion refuse to stop the agent's container,
// preventing in-flight work from being killed by chain-demotion.
//
// The returned release function is idempotent and safe to call multiple times.
// For unknown agent types, returns a no-op function.
func (ac *ActivationController) AcquireRequestGuard(agentType string) func() {
	entry, err := ac.getEntry(agentType)
	if err != nil {
		return func() {}
	}
	entry.ActiveRequests.Add(1)
	entry.TouchActivity()
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.ActiveRequests.Add(-1)
			entry.TouchActivity()
		})
	}
}

func (ac *ActivationController) demoteWarmToCool(ctx context.Context, entry *ActivationEntry) error {
	if entry.HasActiveRequests() {
		entry.TouchActivity()
		return nil
	}
	c := ac.warmPool.Take(entry.AgentType)
	if c == nil {
		c = entry.Container.Load()
	}
	if c == nil {
		entry.StoreTier(TierCold)
		return nil
	}

	var state *handoff.ArchivableState
	if c.Agent() != nil {
		var extractErr error
		state, extractErr = ac.preserver.Extract(c.Agent())
		if extractErr != nil {
			ac.logger.Warn("state extraction failed during warm→cool demotion",
				"agent_type", entry.AgentType,
				"error", extractErr,
			)
		}
	}

	if err := ac.coolStore.Store(entry.AgentType, entry.Spec, state); err != nil {
		return err
	}

	if err := ac.stopAndRemove(ctx, c); err != nil {
		ac.logger.Warn("container stop/remove failed during warm→cool demotion",
			"agent_type", entry.AgentType,
			"error", err,
		)
	}
	entry.Container.Store(nil)
	entry.StoreTier(TierCool)
	ac.metrics.DemotionsToCool.Add(1)

	// Decay predictor affinity for this agent to forget stale patterns.
	ac.predictor.Decay(entry.AgentType)

	return nil
}

func (ac *ActivationController) demoteCoolToCold(entry *ActivationEntry) error {
	if removeErr := ac.coolStore.Remove(entry.AgentType); removeErr != nil {
		ac.logger.Warn("cool store removal failed during cool→cold demotion",
			"agent_type", entry.AgentType,
			"error", removeErr,
		)
	}
	entry.StoreTier(TierCold)
	ac.metrics.DemotionsToCold.Add(1)
	return nil
}

// preWarmAsync triggers predictive pre-warming without blocking the caller.
// Uses CAS on the entry tier to prevent concurrent pre-warms for the same type.
func (ac *ActivationController) preWarmAsync(ctx context.Context, agentType string) {
	predictions := ac.predictor.Predict(agentType)
	for _, predicted := range predictions {
		predicted := predicted
		entry, err := ac.getEntry(predicted)
		if err != nil {
			continue
		}
		if entry.LoadTier() >= TierWarm {
			continue
		}

		if scopeErr := ac.scope.Go("prewarm-"+predicted, 0, func(ctx context.Context) error {
			if entry.LoadTier() >= TierWarm || entry.Prewarming.Load() {
				return nil // another pre-warm or promote already in progress
			}
			if err := ac.prepareWarmEntry(ctx, entry); err != nil {
				ac.logger.Warn("pre-warm failed",
					"agent_type", predicted,
					"error", err,
				)
				return nil
			}
			ac.metrics.PredictorHits.Add(1)
			return nil
		}); scopeErr != nil {
			ac.logger.Warn("failed to launch pre-warm goroutine",
				"agent_type", predicted,
				"error", scopeErr,
			)
		}
	}
}

func (ac *ActivationController) teardownEntry(ctx context.Context, entry *ActivationEntry) error {
	tier := entry.LoadTier()
	var err error

	switch tier {
	case TierHot:
		if c := entry.Container.Load(); c != nil {
			err = ac.stopAndRemove(ctx, c)
		}
	case TierWarm:
		c := ac.warmPool.Take(entry.AgentType)
		if c == nil {
			c = entry.Container.Load()
		}
		if c != nil {
			err = ac.stopAndRemove(ctx, c)
		}
	case TierCool:
		if removeErr := ac.coolStore.Remove(entry.AgentType); removeErr != nil {
			ac.logger.Warn("cool store cleanup failed during teardown",
				"agent_type", entry.AgentType,
				"error", removeErr,
			)
			err = removeErr
		}
	}

	entry.Container.Store(nil)
	entry.StoreTier(TierCold)
	return err
}

func (ac *ActivationController) waitForWarmPreparation(ctx context.Context, entry *ActivationEntry) error {
	if entry == nil {
		return nil
	}
	for entry.Prewarming.Load() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (ac *ActivationController) prepareWarmEntry(ctx context.Context, entry *ActivationEntry) error {
	if entry == nil {
		return nil
	}
	if entry.LoadTier() >= TierWarm {
		return nil
	}
	if !entry.Prewarming.CompareAndSwap(false, true) {
		return nil
	}
	defer entry.Prewarming.Store(false)

	c, err := ac.runtime.CreateContainer(ctx, entry.Spec)
	if err != nil {
		return err
	}
	if err := ac.runtime.StartContainer(ctx, c); err != nil {
		if removeErr := ac.runtime.RemoveContainer(ctx, c); removeErr != nil {
			ac.logger.Warn("warm preparation cleanup failed after start error",
				"agent_type", entry.AgentType,
				"error", removeErr,
			)
		}
		return err
	}
	if err := ac.runtime.PauseContainer(ctx, c); err != nil {
		if stopErr := ac.stopAndRemove(ctx, c); stopErr != nil {
			ac.logger.Warn("warm preparation cleanup failed after pause error",
				"agent_type", entry.AgentType,
				"error", stopErr,
			)
		}
		return err
	}

	evicted := ac.warmPool.Put(entry.AgentType, c)
	entry.Container.Store(c)
	entry.StoreTier(TierWarm)
	entry.TouchActivity()
	if evicted != nil {
		if err := ac.stopAndRemove(ctx, evicted); err != nil {
			ac.logger.Warn("warm preparation eviction cleanup failed",
				"agent_type", entry.AgentType,
				"error", err,
			)
		}
	}
	return nil
}

func (ac *ActivationController) resumeWarmContainer(ctx context.Context, c *container.Container) error {
	switch {
	case c == nil:
		return nil
	case c.IsRunning():
		return nil
	case c.IsPaused():
		return ac.runtime.ResumeContainer(ctx, c)
	default:
		return ac.runtime.StartContainer(ctx, c)
	}
}

func (ac *ActivationController) teardownContainerAsync(ctx context.Context, c *container.Container) {
	if scopeErr := ac.scope.Go("teardown-"+string(c.ID()), 0, func(ctx context.Context) error {
		if err := ac.stopAndRemove(ctx, c); err != nil {
			ac.logger.Warn("async container teardown failed",
				"container_id", c.ID(),
				"error", err,
			)
		}
		return nil
	}); scopeErr != nil {
		ac.logger.Warn("failed to launch teardown goroutine",
			"container_id", c.ID(),
			"error", scopeErr,
		)
	}
}

func (ac *ActivationController) stopAndRemove(ctx context.Context, c *container.Container) error {
	if c.IsRunning() {
		if err := ac.runtime.StopContainer(ctx, c); err != nil {
			ac.logger.Warn("container stop failed",
				"container_id", c.ID(),
				"error", err,
			)
		}
	} else if c.IsPaused() {
		// Resume first so we can cleanly stop (Paused -> Resuming -> Running -> Stopping -> Stopped).
		if err := ac.runtime.ResumeContainer(ctx, c); err != nil {
			ac.logger.Warn("container resume-before-stop failed",
				"container_id", c.ID(),
				"error", err,
			)
		}
		if err := ac.runtime.StopContainer(ctx, c); err != nil {
			ac.logger.Warn("container stop-after-resume failed",
				"container_id", c.ID(),
				"error", err,
			)
		}
	}
	err := ac.runtime.RemoveContainer(ctx, c)
	if err == nil {
		ac.notifyRemoved(c)
	}
	return err
}

func (ac *ActivationController) notifyActivated(c *container.Container) {
	if c == nil {
		return
	}
	ac.mu.RLock()
	fn := ac.onActivated
	ac.mu.RUnlock()
	if fn != nil {
		fn(c)
	}
}

func (ac *ActivationController) notifyRemoved(c *container.Container) {
	if c == nil {
		return
	}
	ac.mu.RLock()
	fn := ac.onRemoved
	ac.mu.RUnlock()
	if fn != nil {
		fn(c)
	}
}

func (ac *ActivationController) getEntry(agentType string) (*ActivationEntry, error) {
	ac.mu.RLock()
	entry, ok := ac.entries[agentType]
	ac.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownAgentType
	}
	return entry, nil
}

// AdoptContainer registers an externally-created container as the active
// instance for the given agent type. Returns the existing container if
// one is already hot and running (singleton invariant).
func (ac *ActivationController) AdoptContainer(agentType string, c *container.Container) (*container.Container, error) {
	if ac.closed.Load() {
		return nil, ErrControllerClosed
	}
	entry, err := ac.getEntry(agentType)
	if err != nil {
		return nil, err
	}
	if entry.LoadTier() == TierHot {
		if existing := entry.Container.Load(); existing != nil && existing.IsRunning() {
			return existing, nil
		}
	}
	entry.Container.Store(c)
	entry.StoreTier(TierHot)
	entry.TouchActivity()
	ac.notifyActivated(c)
	return c, nil
}

// allEntries returns a snapshot of all entries for iteration.
func (ac *ActivationController) allEntries() []*ActivationEntry {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	entries := make([]*ActivationEntry, 0, len(ac.entries))
	for _, e := range ac.entries {
		entries = append(entries, e)
	}
	return entries
}

// RegisterPolicy adds or updates a policy for an agent type at runtime.
func (ac *ActivationController) RegisterPolicy(policy *ActivationPolicy) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	entry, ok := ac.entries[policy.AgentType]
	if !ok {
		entry = &ActivationEntry{
			AgentType: policy.AgentType,
			Spec:      ac.specForAgent(policy.AgentType),
			Policy:    policy,
		}
		ac.entries[policy.AgentType] = entry
		return
	}
	entry.Policy = policy
}

// specForAgent builds a ContainerSpec for the given agent type.
// This is a minimal spec — the runtime's CreateContainer fills in
// details via admission and agent creation.
func (ac *ActivationController) specForAgent(agentType string) container.ContainerSpec {
	return container.ContainerSpec{
		Name:      agentType,
		AgentType: agentType,
	}
}

// EntryCount returns the number of registered agent types.
func (ac *ActivationController) EntryCount() int {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return len(ac.entries)
}

// ActiveAgentTypes returns the currently hot agent types.
func (ac *ActivationController) ActiveAgentTypes() []string {
	entries := ac.allEntries()
	active := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.LoadTier() != TierHot {
			continue
		}
		active = append(active, entry.AgentType)
	}
	return active
}

// TierOf returns the current tier of the given agent type.
func (ac *ActivationController) TierOf(agentType string) (ActivationTier, error) {
	entry, err := ac.getEntry(agentType)
	if err != nil {
		return TierCold, err
	}
	return entry.LoadTier(), nil
}
