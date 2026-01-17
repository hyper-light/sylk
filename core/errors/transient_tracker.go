// Package errors implements a 5-tier error taxonomy with classification and handling behavior.
package errors

import (
	"sync"
	"time"
)

// TransientTrackerConfig configures the transient error frequency detection.
type TransientTrackerConfig struct {
	// FrequencyCount is the number of errors within the window to trigger notification.
	FrequencyCount int `yaml:"frequency_count"`

	// FrequencyWindow is the sliding time window for frequency detection.
	FrequencyWindow time.Duration `yaml:"frequency_window"`

	// NotificationCooldown is the cooldown period after notification before re-alerting.
	NotificationCooldown time.Duration `yaml:"notification_cooldown"`
}

// DefaultTransientTrackerConfig returns the default configuration for TransientTracker.
func DefaultTransientTrackerConfig() TransientTrackerConfig {
	return TransientTrackerConfig{
		FrequencyCount:       3,
		FrequencyWindow:      10 * time.Second,
		NotificationCooldown: 60 * time.Second,
	}
}

// TransientTracker monitors transient error frequency using a sliding window.
// It tracks timestamps (not full errors) for memory efficiency.
// Thread-safe via mutex.
type TransientTracker struct {
	mu               sync.Mutex
	errors           []time.Time
	config           TransientTrackerConfig
	lastNotification time.Time
}

// NewTransientTracker creates a new TransientTracker with the given configuration.
func NewTransientTracker(config TransientTrackerConfig) *TransientTracker {
	return &TransientTracker{
		errors: make([]time.Time, 0),
		config: config,
	}
}

// Record tracks a transient error occurrence and returns whether notification is needed.
// Returns true if the frequency threshold is exceeded and cooldown has elapsed.
// Returns false for isolated transient errors (silent handling).
func (t *TransientTracker) Record(err error) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	t.errors = append(t.errors, now)
	t.pruneOldErrors(now)

	return t.shouldNotify(now)
}

// pruneOldErrors removes error timestamps outside the frequency window.
func (t *TransientTracker) pruneOldErrors(now time.Time) {
	cutoff := now.Add(-t.config.FrequencyWindow)
	pruned := make([]time.Time, 0, len(t.errors))

	for _, errTime := range t.errors {
		if errTime.After(cutoff) {
			pruned = append(pruned, errTime)
		}
	}

	t.errors = pruned
}

// shouldNotify checks if notification is needed based on threshold and cooldown.
func (t *TransientTracker) shouldNotify(now time.Time) bool {
	if len(t.errors) < t.config.FrequencyCount {
		return false
	}

	if !t.cooldownElapsed(now) {
		return false
	}

	t.lastNotification = now
	return true
}

// cooldownElapsed checks if the notification cooldown period has passed.
func (t *TransientTracker) cooldownElapsed(now time.Time) bool {
	if t.lastNotification.IsZero() {
		return true
	}
	return now.Sub(t.lastNotification) >= t.config.NotificationCooldown
}
