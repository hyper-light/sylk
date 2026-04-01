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
