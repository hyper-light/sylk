package forest

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultForestReplayDelay       = 10 * time.Second
	defaultForestSubstrateDebounce = 500 * time.Millisecond
	defaultForestSubstrateMaxDelay = 2 * time.Second
	defaultForestTrainingDebounce  = 2 * time.Second
	defaultForestTrainingMaxDelay  = 15 * time.Second
	defaultForestReplayBatchSize   = 8
	defaultForestSubstrateLimit    = 96
	defaultForestTrainingExamples  = 1024
	defaultForestReplayTimeout     = 5 * time.Second
	defaultForestSubstrateTimeout  = 5 * time.Second
	defaultForestTrainingTimeout   = 30 * time.Second
	defaultForestMaintenanceRetry  = 2 * time.Second

	// Issue #3 selection-bias defaults.
	defaultCounterfactualLabelWeight     = 0.3
	defaultCounterfactualWindow          = 24 * time.Hour
	defaultImplicitNegativeWeight        = 0.15
	defaultImplicitNegativeHorizon       = 1 * time.Hour
	defaultImplicitNegativeSweepInterval = 5 * time.Minute
	defaultExplorationRate               = 0.05
	defaultExplorationLabelWeight        = 1.5

	// Issue #10 — storage growth + archival defaults.
	//
	// Default retention windows: 30d for archive cutoffs (forensic
	// vs storage trade-off); 64 trace rows per branch (matches the
	// inline warmth pruner so the background path doesn't fight it).
	defaultTrainingExamplesRetention      = 30 * 24 * time.Hour
	defaultTrainingExamplesPruneInterval  = 1 * time.Hour
	defaultSubstrateStateRetention        = 30 * 24 * time.Hour
	defaultSubstrateStatePruneInterval    = 6 * time.Hour
	defaultEventArchiveAge                = 30 * 24 * time.Hour
	defaultRetrievalEventArchiveAge       = 30 * 24 * time.Hour
	defaultEventArchiveInterval           = 1 * time.Hour
	defaultEventArchiveBatchSize          = 1000

	// Issue #7 — substrate-mode A/B + replacement defaults.
	//
	// defaultSubstrateMode is the dominant substrate mode used when
	// Config.SubstrateMode isn't set. Starts as Full so existing
	// behavior is preserved until operators flip the default.
	defaultSubstrateMode = SubstrateModeFull

	// defaultSubstrateABRate is the per-retrieval probability of
	// swapping to a non-default substrate mode for measurement. 0
	// means "always run the dominant mode"; 0.1 = 10% A/B traffic.
	defaultSubstrateABRate = 0.0

	// Issue #8 — learned base scorer defaults.
	//
	// defaultBaseScoreTrainInterval is the cadence between training
	// passes. 1h amortizes training cost while staying responsive
	// enough that operators can iterate within a working day.
	defaultBaseScoreTrainInterval = 1 * time.Hour

	// defaultBaseScoreMinTrainingExamples is the minimum count of
	// labeled examples in the training window before we'll promote
	// a new model. Below this, accuracy estimates are too noisy.
	defaultBaseScoreMinTrainingExamples = 1000

	// defaultBaseScoreImprovementThreshold is the minimum accuracy
	// gain (over current champion) for a freshly-trained model to
	// be promoted. Prevents random-walk promotion from training-set
	// noise.
	defaultBaseScoreImprovementThreshold = 0.01

	// defaultBaseScoreLearningRate controls SGD step size. Small
	// enough that a single pass can't overshoot a stable optimum.
	defaultBaseScoreLearningRate = 0.005

	// defaultBaseScoreL1Reg / L2Reg regularization coefficients.
	// L1 drives uninformative weights toward zero (component pruning
	// signal); L2 prevents weight explosion.
	defaultBaseScoreL1Reg = 0.001
	defaultBaseScoreL2Reg = 0.01

	// defaultBaseScoreEpochs is the number of SGD passes per training
	// run. Bounded so a single training cycle is short.
	defaultBaseScoreEpochs = 8

	// defaultBaseScoreTrainBatch is the maximum number of training
	// examples sampled per cycle. Larger batches give better gradient
	// estimates but cost more time/memory.
	defaultBaseScoreTrainBatch = 4096

	// defaultBaseScoreABRate is the per-retrieval probability of
	// routing through the challenger instead of the champion.
	defaultBaseScoreABRate = 0.0

	// defaultBaseScoreMaxWeight clamps each weight to ±this value
	// after every SGD step, so a pathological training batch can't
	// produce a model that overpowers other signals.
	defaultBaseScoreMaxWeight = 5.0

	// defaultBaseScorePruningThreshold is the |weight| below which a
	// component is flagged as a pruning candidate. Operators decide
	// whether to actually drop the component from the feature path.
	defaultBaseScorePruningThreshold = 0.005

	// Issue #4 — diversity + cooldown defaults.
	//
	// defaultDiversityLambda balances relevance against novelty in the
	// MMR rerank: 1.0 = pure relevance (no diversity), 0.0 = pure
	// diversity. 0.7 keeps relevance dominant but makes the second/
	// third pick avoid near-duplicates of the first.
	defaultDiversityLambda = 0.7

	// defaultRetrievalCooldownWindow is how many recent retrievals (in
	// the same session) we look back to compute the cooldown signal.
	defaultRetrievalCooldownWindow = 32

	// defaultRetrievalCooldownPenalty is the maximum score scale-down
	// applied to a branch that appeared in every one of the last
	// `cooldownWindow` retrievals: score := score * (1 - penalty).
	// A branch that appeared in half of them gets score * (1 - 0.5*penalty).
	defaultRetrievalCooldownPenalty = 0.3
)

type scheduledForestWork struct {
	first time.Time
	due   time.Time
}

func resolveForestReplayDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return defaultForestReplayDelay
	}
	return delay
}

func resolveForestSubstrateDebounce(delay time.Duration) time.Duration {
	if delay <= 0 {
		return defaultForestSubstrateDebounce
	}
	return delay
}

func resolveForestTrainingDebounce(delay time.Duration) time.Duration {
	if delay <= 0 {
		return defaultForestTrainingDebounce
	}
	return delay
}

func resolveForestReplayBatchSize(size int) int {
	if size <= 0 {
		return defaultForestReplayBatchSize
	}
	return size
}

func resolveForestSubstrateLimit(limit int) int {
	if limit <= 0 {
		return defaultForestSubstrateLimit
	}
	return limit
}

func resolveForestTrainingExamples(limit int) int {
	if limit <= 0 {
		return defaultForestTrainingExamples
	}
	return limit
}

func resolveCounterfactualLabelWeight(w float64) float64 {
	if w <= 0 {
		return defaultCounterfactualLabelWeight
	}
	return w
}

func resolveCounterfactualWindow(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultCounterfactualWindow
	}
	return d
}

func resolveImplicitNegativeWeight(w float64) float64 {
	if w <= 0 {
		return defaultImplicitNegativeWeight
	}
	return w
}

func resolveImplicitNegativeHorizon(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultImplicitNegativeHorizon
	}
	return d
}

func resolveImplicitNegativeSweepInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultImplicitNegativeSweepInterval
	}
	return d
}

// resolveExplorationRate clamps the rate into [0, 1]. Negative or
// non-set defaults to defaultExplorationRate; >1 clamps to 1.0
// (always explore — useful for tests).
func resolveExplorationRate(rate float64) float64 {
	if rate < 0 {
		return defaultExplorationRate
	}
	if rate > 1 {
		return 1
	}
	return rate
}

func resolveExplorationLabelWeight(w float64) float64 {
	if w <= 0 {
		return defaultExplorationLabelWeight
	}
	return w
}

// resolveBaseScoreTrainInterval / others — Issue #8 base scorer.
// Negative or zero falls back to the documented default; positive
// values pass through.
func resolveBaseScoreTrainInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultBaseScoreTrainInterval
	}
	return d
}

func resolveBaseScoreMinTrainingExamples(n int) int {
	if n <= 0 {
		return defaultBaseScoreMinTrainingExamples
	}
	return n
}

// resolveBaseScoreImprovementThreshold clamps to [0,1]. 0 means
// "promote any model that doesn't regress"; 1 means "never promote."
func resolveBaseScoreImprovementThreshold(t float64) float64 {
	if t < 0 {
		return defaultBaseScoreImprovementThreshold
	}
	if t > 1 {
		return 1
	}
	return t
}

// resolveBaseScoreLearningRate caps to a safe upper bound to prevent
// training divergence on outlier batches.
func resolveBaseScoreLearningRate(r float64) float64 {
	if r <= 0 {
		return defaultBaseScoreLearningRate
	}
	if r > 1 {
		return 1
	}
	return r
}

func resolveBaseScoreL1Reg(r float64) float64 {
	if r < 0 {
		return defaultBaseScoreL1Reg
	}
	return r
}

func resolveBaseScoreL2Reg(r float64) float64 {
	if r < 0 {
		return defaultBaseScoreL2Reg
	}
	return r
}

func resolveBaseScoreEpochs(n int) int {
	if n <= 0 {
		return defaultBaseScoreEpochs
	}
	return n
}

func resolveBaseScoreTrainBatch(n int) int {
	if n <= 0 {
		return defaultBaseScoreTrainBatch
	}
	return n
}

// resolveBaseScoreABRate clamps to [0,1].
func resolveBaseScoreABRate(r float64) float64 {
	if r < 0 {
		return defaultBaseScoreABRate
	}
	if r > 1 {
		return 1
	}
	return r
}

func resolveBaseScoreMaxWeight(w float64) float64 {
	if w <= 0 {
		return defaultBaseScoreMaxWeight
	}
	return w
}

func resolveBaseScorePruningThreshold(t float64) float64 {
	if t < 0 {
		return defaultBaseScorePruningThreshold
	}
	return t
}

// Issue #10 resolvers — straight-line clamps for retention/interval
// duration values. Negative or zero falls back to documented defaults.
func resolveTrainingExamplesRetention(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultTrainingExamplesRetention
	}
	return d
}

func resolveTrainingExamplesPruneInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultTrainingExamplesPruneInterval
	}
	return d
}

func resolveSubstrateStateRetention(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultSubstrateStateRetention
	}
	return d
}

func resolveSubstrateStatePruneInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultSubstrateStatePruneInterval
	}
	return d
}

func resolveEventArchiveAge(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultEventArchiveAge
	}
	return d
}

func resolveRetrievalEventArchiveAge(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultRetrievalEventArchiveAge
	}
	return d
}

func resolveEventArchiveInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultEventArchiveInterval
	}
	return d
}

func resolveEventArchiveBatchSize(n int) int {
	if n <= 0 {
		return defaultEventArchiveBatchSize
	}
	return n
}

// resolveSubstrateMode validates the incoming mode against the
// known set; an empty or unrecognized value falls back to
// defaultSubstrateMode so a typo doesn't silently disable substrate.
func resolveSubstrateMode(mode SubstrateMode) SubstrateMode {
	switch mode {
	case SubstrateModeFull, SubstrateModePageRank, SubstrateModeWarmthOnly:
		return mode
	}
	return defaultSubstrateMode
}

// resolveSubstrateABRate clamps to [0,1]. 0 disables A/B sampling.
func resolveSubstrateABRate(r float64) float64 {
	if r < 0 {
		return defaultSubstrateABRate
	}
	if r > 1 {
		return 1
	}
	return r
}

// resolveDiversityLambda clamps to [0,1]. A negative or unset value
// defaults to defaultDiversityLambda; a value > 1 clamps to 1
// (effectively disables diversity reranking — the iterative pick
// reduces to "always pick the highest-relevance candidate").
func resolveDiversityLambda(l float64) float64 {
	if l < 0 {
		return defaultDiversityLambda
	}
	if l > 1 {
		return 1
	}
	return l
}

func resolveRetrievalCooldownWindow(n int) int {
	if n <= 0 {
		return defaultRetrievalCooldownWindow
	}
	return n
}

// resolveRetrievalCooldownPenalty clamps to [0,1]. 0 disables the
// cooldown entirely (no penalty applied). 1 means a branch that
// appeared in every recent retrieval is fully de-ranked. Negative
// inputs fall back to the default; > 1 clamps to 1.
func resolveRetrievalCooldownPenalty(p float64) float64 {
	if p < 0 {
		return defaultRetrievalCooldownPenalty
	}
	if p > 1 {
		return 1
	}
	return p
}

func (m *MemoryForest) startMaintenance() {
	m.bootstrapMaintenanceState()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.maintenanceLoop()
	}()
}

func (m *MemoryForest) maintenanceLoop() {
	var timer *time.Timer
	defer stopForestTimer(timer)

	for {
		timerCh, nextTimer := m.nextMaintenanceTimer(timer)
		timer = nextTimer
		select {
		case <-m.stopCh:
			return
		case <-m.maintenanceWake:
		case <-timerCh:
		}

		for m.runNextMaintenance() {
		}
	}
}

func (m *MemoryForest) runNextMaintenance() bool {
	now := time.Now().UTC()
	if m.runDueReplay(now) {
		return true
	}
	if m.runReadySubstrate(now) {
		return true
	}
	if m.runDueTraining(now) {
		return true
	}
	return false
}

func (m *MemoryForest) runDueReplay(now time.Time) bool {
	m.maintenanceMu.Lock()
	if m.replayDue.IsZero() || m.replayDue.After(now) {
		m.maintenanceMu.Unlock()
		return false
	}
	m.replayDue = time.Time{}
	m.maintenanceMu.Unlock()

	ctx, cancel := context.WithTimeout(m.runCtx, defaultForestReplayTimeout)
	defer cancel()

	m.maintenanceRunMu.Lock()
	defer m.maintenanceRunMu.Unlock()

	var touchedSessions []string
	for {
		result, sessions, err := m.runReplay(ctx, m.replayBatchSize)
		if err != nil {
			m.logMaintenanceFailure("replay", err)
			m.scheduleReplayRetry(time.Now().UTC().Add(defaultForestMaintenanceRetry))
			return true
		}
		touchedSessions = append(touchedSessions, sessions...)
		if result.Processed < m.replayBatchSize {
			break
		}
		if err := ctx.Err(); err != nil {
			m.scheduleReplayRetry(time.Now().UTC().Add(defaultForestMaintenanceRetry))
			return true
		}
	}

	nextDue, err := m.nextQueuedReplayDue(ctx)
	if err != nil {
		m.logMaintenanceFailure("replay-next-due", err)
		m.scheduleReplayRetry(time.Now().UTC().Add(defaultForestMaintenanceRetry))
	} else if !nextDue.IsZero() {
		m.scheduleReplayAt(nextDue)
	}
	for _, sessionID := range dedupeStrings(touchedSessions) {
		m.scheduleImmediateSubstrateRefresh(sessionID)
	}
	return true
}

func (m *MemoryForest) runReadySubstrate(now time.Time) bool {
	sessions := m.popReadySubstrateSessions(now)
	if len(sessions) == 0 {
		return false
	}
	for _, sessionID := range sessions {
		ctx, cancel := context.WithTimeout(m.runCtx, defaultForestSubstrateTimeout)
		_, err := m.RunSubstrateMaintenanceForSession(ctx, sessionID, m.substrateLimit)
		cancel()
		if err != nil {
			m.logMaintenanceFailure("substrate", err)
			m.scheduleSubstrateRefreshRetry(sessionID, defaultForestMaintenanceRetry)
		}
	}
	return true
}

func (m *MemoryForest) runDueTraining(now time.Time) bool {
	m.maintenanceMu.Lock()
	if !m.trainingDirty || m.trainingWork.due.IsZero() || m.trainingWork.due.After(now) {
		m.maintenanceMu.Unlock()
		return false
	}
	m.trainingDirty = false
	m.trainingWork = scheduledForestWork{}
	m.maintenanceMu.Unlock()

	ctx, cancel := context.WithTimeout(m.runCtx, defaultForestTrainingTimeout)
	defer cancel()
	if _, err := m.TrainModels(ctx, m.trainingMaxExamples); err != nil {
		m.logMaintenanceFailure("training", err)
		m.scheduleTrainingRetry(defaultForestMaintenanceRetry)
	}
	return true
}

func (m *MemoryForest) nextMaintenanceTimer(timer *time.Timer) (<-chan time.Time, *time.Timer) {
	m.maintenanceMu.Lock()
	due, ok := m.nextMaintenanceDueLocked()
	m.maintenanceMu.Unlock()
	return resetForestTimer(timer, due, ok)
}

func (m *MemoryForest) nextMaintenanceDueLocked() (time.Time, bool) {
	var due time.Time
	setDue := func(candidate time.Time) {
		if candidate.IsZero() {
			return
		}
		if due.IsZero() || candidate.Before(due) {
			due = candidate
		}
	}
	setDue(m.replayDue)
	if m.trainingDirty {
		setDue(m.trainingWork.due)
	}
	for _, scheduled := range m.pendingSubstrate {
		setDue(scheduled.due)
	}
	if due.IsZero() {
		return time.Time{}, false
	}
	return due, true
}

func resetForestTimer(timer *time.Timer, due time.Time, ok bool) (<-chan time.Time, *time.Timer) {
	if !ok {
		stopForestTimer(timer)
		return nil, nil
	}
	delay := time.Until(due)
	if delay < 0 {
		delay = 0
	}
	if timer == nil {
		timer = time.NewTimer(delay)
		return timer.C, timer
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
	return timer.C, timer
}

func stopForestTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (m *MemoryForest) bootstrapMaintenanceState() {
	ctx, cancel := context.WithTimeout(m.runCtx, 2*time.Second)
	defer cancel()

	dirtySessions, err := m.dirtySubstrateSessions(ctx)
	if err != nil {
		m.logMaintenanceFailure("bootstrap-substrate", err)
	} else {
		for _, sessionID := range dirtySessions {
			m.scheduleImmediateSubstrateRefresh(sessionID)
		}
	}

	nextReplay, err := m.nextQueuedReplayDue(ctx)
	if err != nil {
		m.logMaintenanceFailure("bootstrap-replay", err)
	} else if !nextReplay.IsZero() {
		m.scheduleReplayAt(nextReplay)
	}

	trainingBacklog, err := m.hasTrainingBacklog(ctx)
	if err != nil {
		m.logMaintenanceFailure("bootstrap-training", err)
	} else if trainingBacklog {
		m.scheduleImmediateTraining()
	}
}

func (m *MemoryForest) nextQueuedReplayDue(ctx context.Context) (time.Time, error) {
	var availableAt sql.NullInt64
	err := m.db.QueryRowContext(ctx, `
		SELECT MIN(available_at)
		FROM forest_replay_queue
		WHERE state = ?
	`, string(ReplayStateQueued)).Scan(&availableAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("query next replay due: %w", err)
	}
	if !availableAt.Valid || availableAt.Int64 == 0 {
		return time.Time{}, nil
	}
	return time.Unix(availableAt.Int64, 0).UTC(), nil
}

func (m *MemoryForest) hasTrainingBacklog(ctx context.Context) (bool, error) {
	var labeledAt sql.NullInt64
	if err := m.db.QueryRowContext(ctx, `
		SELECT MAX(updated_at)
		FROM forest_training_examples
		WHERE utility_label IS NOT NULL OR risk_label IS NOT NULL
	`).Scan(&labeledAt); err != nil {
		return false, fmt.Errorf("query latest labeled example: %w", err)
	}
	if !labeledAt.Valid || labeledAt.Int64 == 0 {
		return false, nil
	}

	var trainedAt sql.NullInt64
	if err := m.db.QueryRowContext(ctx, `
		SELECT MAX(trained_at)
		FROM forest_models
		WHERE active = 1
	`).Scan(&trainedAt); err != nil {
		return false, fmt.Errorf("query latest forest model: %w", err)
	}
	if !trainedAt.Valid {
		return true, nil
	}
	return labeledAt.Int64 > trainedAt.Int64, nil
}

func (m *MemoryForest) scheduleReplayAt(at time.Time) {
	if m == nil || at.IsZero() {
		return
	}
	m.maintenanceMu.Lock()
	if m.replayDue.IsZero() || at.Before(m.replayDue) {
		m.replayDue = at.UTC()
	}
	m.maintenanceMu.Unlock()
	m.wakeMaintenanceLoop()
}

func (m *MemoryForest) scheduleReplayRetry(at time.Time) {
	m.scheduleReplayAt(at)
}

func (m *MemoryForest) scheduleSubstrateRefresh(sessionID string) {
	m.scheduleSubstrateRefreshWithDelay(sessionID, m.substrateDebounce, defaultForestSubstrateMaxDelay)
}

func (m *MemoryForest) scheduleImmediateSubstrateRefresh(sessionID string) {
	m.scheduleSubstrateRefreshWithDelay(sessionID, 0, 0)
}

func (m *MemoryForest) scheduleSubstrateRefreshRetry(sessionID string, delay time.Duration) {
	m.scheduleSubstrateRefreshWithDelay(sessionID, delay, delay)
}

func (m *MemoryForest) scheduleSubstrateRefreshWithDelay(sessionID string, delay, maxDelay time.Duration) {
	if m == nil {
		return
	}
	sessionID = normalizeForestSessionID(sessionID)
	now := time.Now().UTC()
	m.maintenanceMu.Lock()
	m.pendingSubstrate[sessionID] = debounceForestWork(m.pendingSubstrate[sessionID], now, delay, maxDelay)
	m.maintenanceMu.Unlock()
	m.wakeMaintenanceLoop()
}

func (m *MemoryForest) scheduleTraining() {
	if m == nil {
		return
	}
	now := time.Now().UTC()
	m.maintenanceMu.Lock()
	m.trainingDirty = true
	m.trainingWork = debounceForestWork(m.trainingWork, now, m.trainingDebounce, defaultForestTrainingMaxDelay)
	m.maintenanceMu.Unlock()
	m.wakeMaintenanceLoop()
}

func (m *MemoryForest) scheduleImmediateTraining() {
	if m == nil {
		return
	}
	now := time.Now().UTC()
	m.maintenanceMu.Lock()
	m.trainingDirty = true
	m.trainingWork = scheduledForestWork{first: now, due: now}
	m.maintenanceMu.Unlock()
	m.wakeMaintenanceLoop()
}

func (m *MemoryForest) scheduleTrainingRetry(delay time.Duration) {
	if m == nil {
		return
	}
	now := time.Now().UTC()
	m.maintenanceMu.Lock()
	m.trainingDirty = true
	m.trainingWork = scheduledForestWork{first: now, due: now.Add(delay)}
	m.maintenanceMu.Unlock()
	m.wakeMaintenanceLoop()
}

func (m *MemoryForest) popReadySubstrateSessions(now time.Time) []string {
	m.maintenanceMu.Lock()
	defer m.maintenanceMu.Unlock()
	if len(m.pendingSubstrate) == 0 {
		return nil
	}
	sessions := make([]string, 0, len(m.pendingSubstrate))
	for sessionID, scheduled := range m.pendingSubstrate {
		if scheduled.due.IsZero() || scheduled.due.After(now) {
			continue
		}
		sessions = append(sessions, sessionID)
		delete(m.pendingSubstrate, sessionID)
	}
	sort.Strings(sessions)
	return sessions
}

func debounceForestWork(current scheduledForestWork, now time.Time, delay, maxDelay time.Duration) scheduledForestWork {
	if current.first.IsZero() {
		current.first = now
	}
	due := now.Add(delay)
	if maxDelay > 0 {
		maxDue := current.first.Add(maxDelay)
		if due.After(maxDue) {
			due = maxDue
		}
	}
	current.due = due
	return current
}

func normalizeForestSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "global"
	}
	return sessionID
}

func (m *MemoryForest) wakeMaintenanceLoop() {
	if m == nil || m.maintenanceWake == nil {
		return
	}
	select {
	case m.maintenanceWake <- struct{}{}:
	default:
	}
}

func (m *MemoryForest) logMaintenanceFailure(task string, err error) {
	if err == nil || m == nil || m.logger == nil {
		return
	}
	m.logger.Warn("forest: maintenance task failed", "task", task, "error", err)
}
