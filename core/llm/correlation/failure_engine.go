package correlation

import (
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// FailureCorrelationEngine detects provider-wide failures across sessions.
type FailureCorrelationEngine struct {
	config CorrelationConfig

	// Per-provider failure tracking
	providerFailures sync.Map // provider -> *failureTracker

	// Global backoff state per provider
	globalBackoffs sync.Map // provider -> *globalBackoff

	dispatcher SignalDispatcher // For broadcasting
}

// failureTracker tracks failures for a single provider.
type failureTracker struct {
	failures []FailureEvent
	mu       sync.Mutex
}

// globalBackoff represents the backoff state for a provider.
type globalBackoff struct {
	active       atomic.Bool
	until        atomic.Int64 // Unix nano
	attempt      atomic.Int32
	trafficRatio atomic.Int64 // Fixed-point: ratio * 1000
}

// NewFailureCorrelationEngine creates an engine with optional dispatcher.
func NewFailureCorrelationEngine(config CorrelationConfig, dispatcher SignalDispatcher) *FailureCorrelationEngine {
	return &FailureCorrelationEngine{
		config:     config,
		dispatcher: dispatcher,
	}
}

// RecordFailure records a failure and checks for correlation.
func (fce *FailureCorrelationEngine) RecordFailure(provider, sessionID string, err error) {
	if err == nil {
		return
	}

	tracker := fce.getOrCreateTracker(provider)
	event := fce.createFailureEvent(sessionID, err)

	fce.addFailureEvent(tracker, event)

	if fce.detectCorrelation(provider, tracker) {
		fce.triggerGlobalBackoff(provider)
	}
}

// getOrCreateTracker returns existing or creates new tracker for provider.
func (fce *FailureCorrelationEngine) getOrCreateTracker(provider string) *failureTracker {
	actual, _ := fce.providerFailures.LoadOrStore(provider, &failureTracker{})
	return actual.(*failureTracker)
}

// createFailureEvent creates a FailureEvent from an error.
func (fce *FailureCorrelationEngine) createFailureEvent(sessionID string, err error) FailureEvent {
	return FailureEvent{
		Timestamp: time.Now(),
		SessionID: sessionID,
		ErrorType: categorizeError(err),
	}
}

// addFailureEvent adds an event to the tracker and prunes old events.
func (fce *FailureCorrelationEngine) addFailureEvent(tracker *failureTracker, event FailureEvent) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	tracker.failures = append(tracker.failures, event)
	fce.pruneOldFailures(tracker)
}

// pruneOldFailures removes events outside the correlation window.
// Must be called with tracker.mu held.
func (fce *FailureCorrelationEngine) pruneOldFailures(tracker *failureTracker) {
	cutoff := time.Now().Add(-fce.config.CorrelationWindow)
	pruned := make([]FailureEvent, 0, len(tracker.failures))

	for _, f := range tracker.failures {
		if f.Timestamp.After(cutoff) {
			pruned = append(pruned, f)
		}
	}

	tracker.failures = pruned
}

// detectCorrelation checks if multiple sessions failed within window.
func (fce *FailureCorrelationEngine) detectCorrelation(provider string, tracker *failureTracker) bool {
	if fce.isBackoffActive(provider) {
		return false
	}

	return fce.hasEnoughUniqueFailures(tracker)
}

// isBackoffActive checks if a provider is currently in backoff.
func (fce *FailureCorrelationEngine) isBackoffActive(provider string) bool {
	val, ok := fce.globalBackoffs.Load(provider)
	if !ok {
		return false
	}

	backoff := val.(*globalBackoff)
	return backoff.active.Load()
}

// hasEnoughUniqueFailures checks if tracker has enough unique session failures.
func (fce *FailureCorrelationEngine) hasEnoughUniqueFailures(tracker *failureTracker) bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	uniqueSessions := fce.countUniqueSessions(tracker.failures)
	return uniqueSessions >= fce.config.MinFailuresForGlobal
}

// countUniqueSessions counts distinct session IDs in failures.
func (fce *FailureCorrelationEngine) countUniqueSessions(failures []FailureEvent) int {
	seen := make(map[string]struct{})
	for _, f := range failures {
		seen[f.SessionID] = struct{}{}
	}
	return len(seen)
}

// triggerGlobalBackoff activates global backoff for a provider.
func (fce *FailureCorrelationEngine) triggerGlobalBackoff(provider string) {
	backoff := fce.getOrCreateBackoff(provider)

	if backoff.active.Load() {
		return
	}

	duration := fce.calculateBackoffDuration(backoff)
	fce.activateBackoff(backoff, duration)
	fce.dispatchBackoffSignal(provider, duration)
	fce.scheduleRecovery(provider, backoff, duration)
}

// getOrCreateBackoff returns existing or creates new backoff for provider.
func (fce *FailureCorrelationEngine) getOrCreateBackoff(provider string) *globalBackoff {
	actual, _ := fce.globalBackoffs.LoadOrStore(provider, &globalBackoff{})
	return actual.(*globalBackoff)
}

// calculateBackoffDuration computes backoff duration with exponential increase.
func (fce *FailureCorrelationEngine) calculateBackoffDuration(backoff *globalBackoff) time.Duration {
	attempt := backoff.attempt.Add(1)
	multiplier := int64(1) << (attempt - 1)
	duration := time.Duration(int64(fce.config.GlobalBackoffBase) * multiplier)

	if duration > fce.config.GlobalBackoffMax {
		return fce.config.GlobalBackoffMax
	}
	return duration
}

// activateBackoff sets the backoff state to active.
func (fce *FailureCorrelationEngine) activateBackoff(backoff *globalBackoff, duration time.Duration) {
	backoff.until.Store(time.Now().Add(duration).UnixNano())
	backoff.trafficRatio.Store(0)
	backoff.active.Store(true)
}

// dispatchBackoffSignal sends backoff signal if dispatcher is available.
func (fce *FailureCorrelationEngine) dispatchBackoffSignal(provider string, duration time.Duration) {
	if fce.dispatcher == nil {
		return
	}
	fce.dispatcher.SendSignal(NewGlobalBackoffSignal(provider, duration))
}

// scheduleRecovery gradually ramps up traffic after backoff.
func (fce *FailureCorrelationEngine) scheduleRecovery(provider string, backoff *globalBackoff, duration time.Duration) {
	go fce.runRecovery(provider, backoff, duration)
}

// runRecovery executes the recovery process in a goroutine.
func (fce *FailureCorrelationEngine) runRecovery(provider string, backoff *globalBackoff, duration time.Duration) {
	time.Sleep(duration)
	fce.rampUpTraffic(backoff)
	fce.completeRecovery(provider, backoff)
}

// rampUpTraffic gradually increases the traffic ratio.
func (fce *FailureCorrelationEngine) rampUpTraffic(backoff *globalBackoff) {
	rampSteps := 10
	stepDuration := time.Second

	for i := 1; i <= rampSteps; i++ {
		ratio := fce.config.RecoveryRampUp + (1.0-fce.config.RecoveryRampUp)*float64(i)/float64(rampSteps)
		backoff.trafficRatio.Store(int64(ratio * 1000))
		time.Sleep(stepDuration)
	}
}

// completeRecovery finalizes the recovery process.
func (fce *FailureCorrelationEngine) completeRecovery(provider string, backoff *globalBackoff) {
	backoff.trafficRatio.Store(1000)
	backoff.active.Store(false)
	backoff.attempt.Store(0)
	fce.dispatchRecoverySignal(provider)
}

// dispatchRecoverySignal sends recovery signal if dispatcher is available.
func (fce *FailureCorrelationEngine) dispatchRecoverySignal(provider string) {
	if fce.dispatcher == nil {
		return
	}
	fce.dispatcher.SendSignal(NewGlobalRecoverySignal(provider))
}

// ShouldAllow returns whether a request should be allowed (probabilistic during recovery).
func (fce *FailureCorrelationEngine) ShouldAllow(provider string) bool {
	val, ok := fce.globalBackoffs.Load(provider)
	if !ok {
		return true
	}

	return fce.checkBackoffState(val.(*globalBackoff))
}

// checkBackoffState determines if request should be allowed based on backoff state.
func (fce *FailureCorrelationEngine) checkBackoffState(backoff *globalBackoff) bool {
	if !backoff.active.Load() {
		return true
	}

	if time.Now().UnixNano() < backoff.until.Load() {
		return false
	}

	return fce.allowByRatio(backoff)
}

// allowByRatio probabilistically allows traffic based on current ratio.
func (fce *FailureCorrelationEngine) allowByRatio(backoff *globalBackoff) bool {
	ratio := backoff.trafficRatio.Load()
	if ratio >= 1000 {
		return true
	}

	return rand.Int63n(1000) < ratio
}

// categorizeError converts an error to an ErrorCategory.
func categorizeError(err error) ErrorCategory {
	if err == nil {
		return ErrCategoryNone
	}
	return categorizeErrorString(strings.ToLower(err.Error()))
}

// categorizeErrorString categorizes a lowercase error string.
func categorizeErrorString(errStr string) ErrorCategory {
	if isRateLimitError(errStr) {
		return ErrCategoryRateLimit
	}
	if isAuthError(errStr) {
		return ErrCategoryAuth
	}
	if isTransientError(errStr) {
		return ErrCategoryTransient
	}
	return ErrCategoryPermanent
}

// isRateLimitError checks if error indicates rate limiting.
func isRateLimitError(errStr string) bool {
	return strings.Contains(errStr, "429") || strings.Contains(errStr, "rate")
}

// isAuthError checks if error indicates authentication failure.
func isAuthError(errStr string) bool {
	return strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "auth") ||
		strings.Contains(errStr, "key")
}

// isTransientError checks if error indicates transient failure.
func isTransientError(errStr string) bool {
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "temporary") ||
		strings.Contains(errStr, "503")
}

// GetBackoffState returns the current backoff state for testing.
func (fce *FailureCorrelationEngine) GetBackoffState(provider string) (active bool, ratio float64) {
	val, ok := fce.globalBackoffs.Load(provider)
	if !ok {
		return false, 1.0
	}

	backoff := val.(*globalBackoff)
	return backoff.active.Load(), float64(backoff.trafficRatio.Load()) / 1000.0
}

// GetFailureCount returns the current failure count for testing.
func (fce *FailureCorrelationEngine) GetFailureCount(provider string) int {
	val, ok := fce.providerFailures.Load(provider)
	if !ok {
		return 0
	}

	tracker := val.(*failureTracker)
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return len(tracker.failures)
}
