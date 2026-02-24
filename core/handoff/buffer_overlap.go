package handoff

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// OverlapPhase tracks the overlap lifecycle.
type OverlapPhase int

const (
	// OverlapIdle indicates no overlap is in progress.
	OverlapIdle OverlapPhase = iota

	// OverlapStarting indicates the new agent is being created.
	OverlapStarting

	// OverlapRunning indicates both agents are active, new agent warming up.
	OverlapRunning

	// OverlapDraining indicates the old agent is finishing current work.
	OverlapDraining

	// OverlapComplete indicates the swap is done, old agent terminated.
	OverlapComplete

	// OverlapAborted indicates the overlap failed, old agent continues.
	OverlapAborted
)

// String returns the human-readable name of the overlap phase.
func (p OverlapPhase) String() string {
	switch p {
	case OverlapIdle:
		return "idle"
	case OverlapStarting:
		return "starting"
	case OverlapRunning:
		return "running"
	case OverlapDraining:
		return "draining"
	case OverlapComplete:
		return "complete"
	case OverlapAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

// OverlapWALEntry records an overlap lifecycle event for persistence.
type OverlapWALEntry struct {
	Phase       OverlapPhase `json:"phase"`
	OldAgentID  string       `json:"old_agent_id"`
	NewAgentID  string       `json:"new_agent_id,omitempty"`
	SnapshotSeq uint64       `json:"snapshot_seq"`
	Timestamp   time.Time    `json:"timestamp"`
	Error       string       `json:"error,omitempty"`
}

// OverlapHandle provides control over an in-progress overlap.
type OverlapHandle struct {
	// NewAgent is the newly created agent from the handoff.
	NewAgent HandoffableAgent

	// SessionID is the session ID for the new agent.
	SessionID string

	// Phase returns the current overlap phase.
	Phase func() OverlapPhase

	// Complete transitions to OverlapComplete. Returns an error if the
	// overlap is not in a completable state.
	Complete func() error

	// Abort transitions to OverlapAborted with the given reason.
	Abort func(reason string)

	// WaitDrain blocks until the draining phase completes or the context
	// expires. The overlap transitions to OverlapDraining before waiting.
	WaitDrain func(ctx context.Context) error
}

// OverlapCoordinator manages the parallel operation of old and new agents
// during a handoff. It enforces that at most one overlap is active at
// a time and provides lifecycle control via OverlapHandle.
type OverlapCoordinator struct {
	mu sync.Mutex

	agentType      string
	phase          OverlapPhase
	phaseEnteredAt time.Time
	oldAgentID     string
	newAgentID     string
	snapshotSeq    uint64

	// Timeouts derived from context window.
	creationTimeout time.Duration
	warmupTimeout   time.Duration
	drainTimeout    time.Duration
	totalTimeout    time.Duration

	timeoutTimer *time.Timer
	abortCh      chan struct{}
	drainDoneCh  chan struct{}

	// WAL integration.
	walWriter func(entry OverlapWALEntry)
}

// NewOverlapCoordinator creates a coordinator with timeouts derived from
// the agent descriptor's context window.
//
// Timeout derivation:
//
//	creationTimeout = max(4, contextWindow/50_000) seconds
//	warmupTimeout   = max(2, contextWindow/100_000) seconds
//	drainTimeout    = max(4, contextWindow/50_000) seconds
//	totalTimeout    = creation + warmup + drain + 5s
func NewOverlapCoordinator(desc AgentDescriptor) *OverlapCoordinator {
	cw := desc.ContextWindow
	if cw < 20_000 {
		cw = 20_000
	}

	creation := time.Duration(maxInt(4, cw/50_000)) * time.Second
	warmup := time.Duration(maxInt(2, cw/100_000)) * time.Second
	drain := time.Duration(maxInt(4, cw/50_000)) * time.Second
	total := creation + warmup + drain + 5*time.Second

	return &OverlapCoordinator{
		agentType:       desc.AgentType,
		phase:           OverlapIdle,
		phaseEnteredAt:  time.Now(),
		creationTimeout: creation,
		warmupTimeout:   warmup,
		drainTimeout:    drain,
		totalTimeout:    total,
	}
}

// Phase returns the current overlap phase.
func (oc *OverlapCoordinator) Phase() OverlapPhase {
	oc.mu.Lock()
	defer oc.mu.Unlock()
	return oc.phase
}

// TotalTimeout returns the configured total timeout for overlaps.
func (oc *OverlapCoordinator) TotalTimeout() time.Duration {
	return oc.totalTimeout
}

// SetWALWriter sets the WAL writer callback for persistence.
func (oc *OverlapCoordinator) SetWALWriter(fn func(OverlapWALEntry)) {
	oc.mu.Lock()
	defer oc.mu.Unlock()
	oc.walWriter = fn
}

// BeginOverlap starts the overlap lifecycle with the given snapshot.
// Returns an error if an overlap is already in progress.
//
// Flow:
//  1. Validate phase is OverlapIdle
//  2. Transition to OverlapStarting
//  3. Build session config and create session via factory adapter
//  4. Inject context if the agent is HandoffInjectable
//  5. Transition to OverlapRunning
//  6. Start timeout timer
//  7. Return OverlapHandle
func (oc *OverlapCoordinator) BeginOverlap(
	oldAgentID string,
	snapshot *COWSnapshot,
	factoryAdapter *FactorySessionAdapter,
) (*OverlapHandle, error) {
	oc.mu.Lock()

	if oc.phase != OverlapIdle {
		oc.mu.Unlock()
		return nil, fmt.Errorf("overlap already in progress (phase=%s)", oc.phase)
	}

	oc.oldAgentID = oldAgentID
	oc.snapshotSeq = snapshot.SequenceNum
	oc.abortCh = make(chan struct{})
	oc.drainDoneCh = make(chan struct{})
	oc.enterPhaseLocked(OverlapStarting)
	oc.mu.Unlock()

	// Create new agent session.
	ctx, cancel := context.WithTimeout(context.Background(), oc.creationTimeout)
	defer cancel()

	sessionCfg := SessionConfig{
		Name:        fmt.Sprintf("overlap-%s", oldAgentID),
		Description: "parallel buffer overlap handoff",
		Metadata: map[string]interface{}{
			"agent_type":   oc.agentType,
			"overlap_from": oldAgentID,
		},
	}

	sessionID, err := factoryAdapter.CreateSession(ctx, sessionCfg)
	if err != nil {
		oc.abortInternal(fmt.Sprintf("create session: %v", err))
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Take the agent from the adapter.
	newAgent, ok := factoryAdapter.TakeAgent(sessionID)
	if !ok {
		oc.abortInternal(fmt.Sprintf("no agent for session %s", sessionID))
		return nil, fmt.Errorf("no agent for session %q", sessionID)
	}

	// Inject context from snapshot if possible.
	if injectable, ok := newAgent.(HandoffInjectable); ok {
		pc := oc.snapshotToPreparedContext(snapshot)
		if err := injectable.InjectPreparedContext(pc); err != nil {
			oc.abortInternal(fmt.Sprintf("inject context: %v", err))
			return nil, fmt.Errorf("inject context: %w", err)
		}
	}

	oc.mu.Lock()
	oc.newAgentID = newAgent.AgentID()
	oc.enterPhaseLocked(OverlapRunning)

	// Start total timeout timer.
	oc.timeoutTimer = time.AfterFunc(oc.totalTimeout, func() {
		oc.abortInternal("total timeout exceeded")
	})

	oc.mu.Unlock()

	handle := &OverlapHandle{
		NewAgent:  newAgent,
		SessionID: sessionID,
		Phase:     oc.Phase,
		Complete:  oc.complete,
		Abort: func(reason string) {
			oc.abortInternal(reason)
		},
		WaitDrain: oc.waitDrain,
	}

	return handle, nil
}

// RecoverFromWAL restores overlap state after crash recovery.
// Orphaned overlaps (begin without complete/abort) are abandoned.
func (oc *OverlapCoordinator) RecoverFromWAL(entry OverlapWALEntry) error {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	switch entry.Phase {
	case OverlapStarting, OverlapRunning, OverlapDraining:
		// Orphaned overlap — the old agent survived, new agent doesn't exist.
		oc.phase = OverlapIdle
		oc.phaseEnteredAt = time.Now()
		oc.writeWALLocked(OverlapWALEntry{
			Phase:       OverlapAborted,
			OldAgentID:  entry.OldAgentID,
			SnapshotSeq: entry.SnapshotSeq,
			Timestamp:   time.Now(),
			Error:       "recovered from crash — overlap abandoned",
		})
	case OverlapComplete, OverlapAborted:
		// Terminal state — nothing to recover.
		oc.phase = OverlapIdle
		oc.phaseEnteredAt = time.Now()
	}

	return nil
}

// complete transitions to OverlapComplete. Only valid from OverlapRunning
// or OverlapDraining.
func (oc *OverlapCoordinator) complete() error {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	if oc.phase != OverlapRunning && oc.phase != OverlapDraining {
		return fmt.Errorf("cannot complete from phase %s", oc.phase)
	}

	oc.stopTimerLocked()
	oc.enterPhaseLocked(OverlapComplete)

	// Signal drain waiters.
	select {
	case <-oc.drainDoneCh:
	default:
		close(oc.drainDoneCh)
	}

	// Return to idle for next use.
	oc.phase = OverlapIdle
	oc.phaseEnteredAt = time.Now()

	return nil
}

// waitDrain transitions to OverlapDraining and blocks until complete
// or abort occurs, or the context expires.
func (oc *OverlapCoordinator) waitDrain(ctx context.Context) error {
	oc.mu.Lock()
	if oc.phase == OverlapRunning {
		oc.enterPhaseLocked(OverlapDraining)
	}
	drainCh := oc.drainDoneCh
	abortCh := oc.abortCh
	oc.mu.Unlock()

	select {
	case <-drainCh:
		return nil
	case <-abortCh:
		return fmt.Errorf("overlap aborted during drain")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// abortInternal transitions to OverlapAborted from any non-terminal phase.
func (oc *OverlapCoordinator) abortInternal(reason string) {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	if oc.phase == OverlapIdle || oc.phase == OverlapComplete || oc.phase == OverlapAborted {
		return
	}

	oc.stopTimerLocked()
	oc.writeWALLocked(OverlapWALEntry{
		Phase:       OverlapAborted,
		OldAgentID:  oc.oldAgentID,
		NewAgentID:  oc.newAgentID,
		SnapshotSeq: oc.snapshotSeq,
		Timestamp:   time.Now(),
		Error:       reason,
	})
	oc.phase = OverlapAborted
	oc.phaseEnteredAt = time.Now()

	// Signal abort to drain waiters.
	select {
	case <-oc.abortCh:
	default:
		close(oc.abortCh)
	}

	// Return to idle.
	oc.phase = OverlapIdle
}

// enterPhaseLocked transitions to the given phase and writes a WAL entry.
// Must be called with mu held.
func (oc *OverlapCoordinator) enterPhaseLocked(p OverlapPhase) {
	oc.phase = p
	oc.phaseEnteredAt = time.Now()
	oc.writeWALLocked(OverlapWALEntry{
		Phase:       p,
		OldAgentID:  oc.oldAgentID,
		NewAgentID:  oc.newAgentID,
		SnapshotSeq: oc.snapshotSeq,
		Timestamp:   oc.phaseEnteredAt,
	})
}

// writeWALLocked writes a WAL entry if a writer is set. Must be called with mu held.
func (oc *OverlapCoordinator) writeWALLocked(entry OverlapWALEntry) {
	if oc.walWriter != nil {
		oc.walWriter(entry)
	}
}

// stopTimerLocked stops the timeout timer. Must be called with mu held.
func (oc *OverlapCoordinator) stopTimerLocked() {
	if oc.timeoutTimer != nil {
		oc.timeoutTimer.Stop()
		oc.timeoutTimer = nil
	}
}

// snapshotToPreparedContext converts a COWSnapshot into a PreparedContext
// suitable for injection into the new agent.
func (oc *OverlapCoordinator) snapshotToPreparedContext(snapshot *COWSnapshot) *PreparedContext {
	pc := NewPreparedContextDefault()

	for _, seg := range snapshot.Segments {
		for _, msg := range seg.Messages {
			pc.AddMessage(msg)
		}
		for name, ts := range seg.ToolStates {
			pc.UpdateToolState(name, ts)
		}
	}

	return pc
}

// maxInt returns the larger of two ints.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
