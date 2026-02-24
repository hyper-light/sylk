package handoff

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ShiftPhase represents the current phase of a traffic shift.
type ShiftPhase int

const (
	// ShiftIdle indicates no shift is in progress.
	ShiftIdle ShiftPhase = iota
	// ShiftCanary indicates the new agent is receiving InitialWeight fraction.
	ShiftCanary
	// ShiftRamping indicates weight is increasing each eval period.
	ShiftRamping
	// ShiftConverged indicates the new agent is proven and finalizing.
	ShiftConverged
	// ShiftAborted indicates the new agent failed, reverted to old.
	ShiftAborted
)

func (p ShiftPhase) String() string {
	switch p {
	case ShiftIdle:
		return "idle"
	case ShiftCanary:
		return "canary"
	case ShiftRamping:
		return "ramping"
	case ShiftConverged:
		return "converged"
	case ShiftAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

// ShiftConfig configures a traffic shift. All values are derived from
// bridge state — no magic numbers.
type ShiftConfig struct {
	// InitialWeight is the starting weight for the new agent.
	// Derived: 1/(gpObsCount+1), clamped [0.05, 0.2].
	InitialWeight float64

	// WeightStep is the weight increment per eval period.
	// Derived: = InitialWeight (exponential doubling).
	WeightStep float64

	// EvalInterval is how often to evaluate the new agent's quality.
	// Derived: bridge ContextConfig.MinCheckInterval.
	EvalInterval time.Duration

	// MinNewAgentTurns is the minimum turns needed from the new agent
	// before quality can be meaningfully assessed.
	// Derived: GP needs ~10 observations for meaningful prediction.
	MinNewAgentTurns int

	// QualityThreshold is the minimum acceptable quality for the new agent.
	// Derived: oldAgent GP Mean - StdDev (pessimistic bound).
	QualityThreshold float64

	// MaxShiftDuration bounds total shift time.
	// Derived: OverlapCoordinator.totalTimeout.
	MaxShiftDuration time.Duration
}

// shiftState holds the mutable state of a traffic shift.
type shiftState struct {
	phase     ShiftPhase
	oldID     string
	newID     string
	oldWeight float64
	newWeight float64
	startedAt time.Time
}

// ShiftWALEntry records a traffic shift lifecycle event for persistence.
type ShiftWALEntry struct {
	Phase     ShiftPhase `json:"phase"`
	OldID     string     `json:"old_id"`
	NewID     string     `json:"new_id"`
	OldWeight float64    `json:"old_weight"`
	NewWeight float64    `json:"new_weight"`
	Timestamp time.Time  `json:"timestamp"`
	Error     string     `json:"error,omitempty"`
}

// TrafficShiftController manages the gradual shift of traffic from an
// old agent to a new one during handoff overlap. It evaluates the new
// agent's quality periodically and adjusts weights accordingly:
// accelerates on good quality, rolls back on bad.
type TrafficShiftController struct {
	mu     sync.Mutex
	config ShiftConfig
	state  shiftState

	// newAgentTurns counts turns recorded for the new agent.
	newAgentTurns atomic.Int64

	// Callbacks wired by the bridge/supervisor.
	updateWeight func(agentID string, weight float64)
	getQuality   func(agentID string) (mean, lowerBound float64, ok bool)
	onComplete   func(oldID, newID string)
	onAbort      func(oldID, newID string)
	walWriter    func(entry ShiftWALEntry)

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewTrafficShiftController creates a controller with the given config
// and callbacks. The controller is created in ShiftIdle phase.
func NewTrafficShiftController(
	cfg ShiftConfig,
	updateWeight func(agentID string, weight float64),
	getQuality func(agentID string) (mean, lowerBound float64, ok bool),
	onComplete func(oldID, newID string),
	onAbort func(oldID, newID string),
	walWriter func(entry ShiftWALEntry),
) *TrafficShiftController {
	return &TrafficShiftController{
		config:       cfg,
		updateWeight: updateWeight,
		getQuality:   getQuality,
		onComplete:   onComplete,
		onAbort:      onAbort,
		walWriter:    walWriter,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Phase returns the current shift phase.
func (c *TrafficShiftController) Phase() ShiftPhase {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.phase
}

// BeginShift starts a gradual traffic shift from oldID to newID.
// Returns an error if a shift is already in progress.
func (c *TrafficShiftController) BeginShift(oldID, newID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state.phase != ShiftIdle {
		return fmt.Errorf("shift already in progress (phase=%s)", c.state.phase)
	}

	initialNew := c.config.InitialWeight
	initialOld := 1.0 - initialNew

	c.state = shiftState{
		phase:     ShiftCanary,
		oldID:     oldID,
		newID:     newID,
		oldWeight: initialOld,
		newWeight: initialNew,
		startedAt: time.Now(),
	}

	c.applyWeightsLocked()
	c.writeWALLocked(ShiftCanary, "")

	slog.Info("traffic shift started",
		"old", oldID, "new", newID,
		"initial_weight", initialNew,
		"eval_interval", c.config.EvalInterval,
	)

	go c.evalLoop()
	return nil
}

// RecordNewAgentTurn increments the turn counter for the new agent.
// Called by the bridge when the new agent processes a turn.
func (c *TrafficShiftController) RecordNewAgentTurn() {
	c.newAgentTurns.Add(1)
}

// Stop signals the eval loop to terminate. Safe to call multiple times.
func (c *TrafficShiftController) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

// Done returns a channel that closes when the controller has finished
// (either converged, aborted, or stopped).
func (c *TrafficShiftController) Done() <-chan struct{} {
	return c.doneCh
}

// evalLoop runs periodically to assess the new agent's quality and
// adjust weights. Exits on convergence, abort, timeout, or stop.
func (c *TrafficShiftController) evalLoop() {
	defer close(c.doneCh)

	ticker := time.NewTicker(c.config.EvalInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			c.abortWithReason("controller stopped")
			return
		case <-ticker.C:
			if c.evaluate() {
				return
			}
		}
	}
}

// evaluate performs a single evaluation step. Returns true if the shift
// has reached a terminal state (converged or aborted).
func (c *TrafficShiftController) evaluate() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Terminal states — nothing to do.
	if c.state.phase == ShiftConverged || c.state.phase == ShiftAborted || c.state.phase == ShiftIdle {
		return true
	}

	// Check timeout.
	if time.Since(c.state.startedAt) > c.config.MaxShiftDuration {
		c.abortLocked("max shift duration exceeded")
		return true
	}

	// Wait for enough new-agent turns before assessing quality.
	turns := c.newAgentTurns.Load()
	if turns < int64(c.config.MinNewAgentTurns) {
		return false
	}

	// Read new agent's quality from GP.
	mean, lowerBound, ok := c.getQuality(c.state.newID)
	if !ok {
		// No quality data yet — keep waiting.
		return false
	}

	// Quality check: if the pessimistic bound is below threshold, abort.
	if lowerBound < c.config.QualityThreshold {
		c.abortLocked(fmt.Sprintf(
			"new agent quality below threshold: lower_bound=%.3f threshold=%.3f",
			lowerBound, c.config.QualityThreshold,
		))
		return true
	}

	// Quality acceptable — ramp weight.
	c.state.newWeight += c.config.WeightStep
	if c.state.newWeight > 1.0 {
		c.state.newWeight = 1.0
	}
	c.state.oldWeight = 1.0 - c.state.newWeight
	c.state.phase = ShiftRamping

	c.applyWeightsLocked()

	slog.Info("traffic shift ramp",
		"new", c.state.newID,
		"weight", c.state.newWeight,
		"quality_mean", mean,
		"quality_lower", lowerBound,
	)

	// Check convergence: new agent has full weight.
	if c.state.newWeight >= 1.0 {
		c.convergeLocked()
		return true
	}

	return false
}

// convergeLocked transitions to ShiftConverged. Must hold c.mu.
func (c *TrafficShiftController) convergeLocked() {
	c.state.phase = ShiftConverged
	c.state.newWeight = 1.0
	c.state.oldWeight = 0

	c.applyWeightsLocked()
	c.writeWALLocked(ShiftConverged, "")

	slog.Info("traffic shift converged",
		"old", c.state.oldID,
		"new", c.state.newID,
	)

	if c.onComplete != nil {
		c.onComplete(c.state.oldID, c.state.newID)
	}
}

// abortWithReason acquires the lock and aborts.
func (c *TrafficShiftController) abortWithReason(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.abortLocked(reason)
}

// abortLocked transitions to ShiftAborted, resets weights. Must hold c.mu.
func (c *TrafficShiftController) abortLocked(reason string) {
	if c.state.phase == ShiftAborted || c.state.phase == ShiftIdle {
		return
	}

	c.state.phase = ShiftAborted
	c.state.oldWeight = 1.0
	c.state.newWeight = 0

	c.applyWeightsLocked()
	c.writeWALLocked(ShiftAborted, reason)

	slog.Warn("traffic shift aborted",
		"old", c.state.oldID,
		"new", c.state.newID,
		"reason", reason,
	)

	if c.onAbort != nil {
		c.onAbort(c.state.oldID, c.state.newID)
	}
}

// applyWeightsLocked calls the updateWeight callback. Must hold c.mu.
func (c *TrafficShiftController) applyWeightsLocked() {
	if c.updateWeight == nil {
		return
	}
	c.updateWeight(c.state.oldID, c.state.oldWeight)
	c.updateWeight(c.state.newID, c.state.newWeight)
}

// writeWALLocked persists a shift event. Must hold c.mu.
func (c *TrafficShiftController) writeWALLocked(phase ShiftPhase, errMsg string) {
	if c.walWriter == nil {
		return
	}
	c.walWriter(ShiftWALEntry{
		Phase:     phase,
		OldID:     c.state.oldID,
		NewID:     c.state.newID,
		OldWeight: c.state.oldWeight,
		NewWeight: c.state.newWeight,
		Timestamp: time.Now(),
		Error:     errMsg,
	})
}
