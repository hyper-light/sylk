package container

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// RestartController tracks restart attempts and computes exponential backoff.
// It implements K8s-style crash loop backoff: base * 2^attempts, capped.
type RestartController struct {
	mu sync.Mutex

	policy       RestartPolicy
	attempts     atomic.Int64
	lastRestart  time.Time
	baseDelay    time.Duration
	maxDelay     time.Duration
	resetTimeout time.Duration // After this long without restart, reset attempts
}

// RestartControllerConfig configures the restart controller.
type RestartControllerConfig struct {
	Policy       RestartPolicy
	BaseDelay    time.Duration
	MaxDelay     time.Duration
	ResetTimeout time.Duration
}

// DefaultRestartControllerConfig returns sensible defaults matching K8s behavior.
func DefaultRestartControllerConfig(policy RestartPolicy) RestartControllerConfig {
	return RestartControllerConfig{
		Policy:       policy,
		BaseDelay:    defaultBaseDelay,
		MaxDelay:     defaultMaxDelay,
		ResetTimeout: defaultResetTimeout,
	}
}

const (
	defaultBaseDelay    = 1 * time.Second
	defaultMaxDelay     = 5 * time.Minute
	defaultResetTimeout = 10 * time.Minute
)

// NewRestartController creates a new restart controller.
func NewRestartController(cfg RestartControllerConfig) *RestartController {
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = defaultBaseDelay
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = defaultMaxDelay
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = defaultResetTimeout
	}
	return &RestartController{
		policy:       cfg.Policy,
		baseDelay:    cfg.BaseDelay,
		maxDelay:     cfg.MaxDelay,
		resetTimeout: cfg.ResetTimeout,
	}
}

// ShouldRestart reports whether the container should be restarted given
// the exit error (nil means clean exit). Returns false for RestartNever.
func (rc *RestartController) ShouldRestart(exitErr error) bool {
	switch rc.policy {
	case RestartAlways:
		return true
	case RestartOnFailure:
		return exitErr != nil
	case RestartNever:
		return false
	default:
		return false
	}
}

// RecordRestart records a restart attempt and returns the backoff delay
// to wait before the next restart. Automatically resets the attempt counter
// if enough time has passed since the last restart.
func (rc *RestartController) RecordRestart() time.Duration {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.maybeResetLocked()
	attempts := rc.attempts.Add(1)
	rc.lastRestart = time.Now()
	return rc.computeBackoff(attempts)
}

func (rc *RestartController) maybeResetLocked() {
	if rc.lastRestart.IsZero() {
		return
	}
	if time.Since(rc.lastRestart) > rc.resetTimeout {
		rc.attempts.Store(0)
	}
}

func (rc *RestartController) computeBackoff(attempts int64) time.Duration {
	delay := float64(rc.baseDelay) * math.Pow(2, float64(attempts-1))
	if delay > float64(rc.maxDelay) {
		return rc.maxDelay
	}
	return time.Duration(delay)
}

// Attempts returns the current restart attempt count.
func (rc *RestartController) Attempts() int64 {
	return rc.attempts.Load()
}

// Reset clears the restart counter.
func (rc *RestartController) Reset() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.attempts.Store(0)
	rc.lastRestart = time.Time{}
}

// Policy returns the restart policy.
func (rc *RestartController) Policy() RestartPolicy {
	return rc.policy
}
