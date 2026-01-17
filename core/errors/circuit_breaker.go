package errors

import (
	"sync"
	"sync/atomic"
	"time"
)

type CircuitState int32

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

var circuitStateNames = map[CircuitState]string{
	CircuitClosed:   "closed",
	CircuitOpen:     "open",
	CircuitHalfOpen: "half_open",
}

func (s CircuitState) String() string {
	if name, ok := circuitStateNames[s]; ok {
		return name
	}
	return "unknown"
}

type CircuitBreakerConfig struct {
	ConsecutiveFailures  int           `yaml:"consecutive_failures"`
	FailureRateThreshold float64       `yaml:"failure_rate_threshold"`
	RateWindowSize       int           `yaml:"rate_window_size"`
	CooldownDuration     time.Duration `yaml:"cooldown_duration"`
	SuccessThreshold     int           `yaml:"success_threshold"`
	NotifyOnStateChange  bool          `yaml:"notify_on_state_change"`
}

func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		ConsecutiveFailures:  3,
		FailureRateThreshold: 0.5,
		RateWindowSize:       20,
		CooldownDuration:     30 * time.Second,
		SuccessThreshold:     3,
		NotifyOnStateChange:  true,
	}
}

type CircuitBreaker struct {
	state           int32
	failures        int32
	successes       int32
	lastStateChange int64
	config          CircuitBreakerConfig
	resourceID      string

	mu            sync.Mutex
	recentResults []bool
	windowIndex   int
}

func NewCircuitBreaker(resourceID string, config CircuitBreakerConfig) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:           int32(CircuitClosed),
		config:          config,
		resourceID:      resourceID,
		lastStateChange: time.Now().UnixNano(),
		recentResults:   make([]bool, config.RateWindowSize),
		windowIndex:     0,
	}
	cb.initializeWindow()
	return cb
}

func (cb *CircuitBreaker) initializeWindow() {
	for i := range cb.recentResults {
		cb.recentResults[i] = true
	}
}

func (cb *CircuitBreaker) State() CircuitState {
	return CircuitState(atomic.LoadInt32(&cb.state))
}

func (cb *CircuitBreaker) Failures() int {
	return int(atomic.LoadInt32(&cb.failures))
}

func (cb *CircuitBreaker) Allow() bool {
	state := CircuitState(atomic.LoadInt32(&cb.state))

	switch state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		return cb.checkCooldownExpired()
	case CircuitHalfOpen:
		return true
	default:
		return true
	}
}

func (cb *CircuitBreaker) checkCooldownExpired() bool {
	lastChange := atomic.LoadInt64(&cb.lastStateChange)
	elapsed := time.Duration(time.Now().UnixNano() - lastChange)

	if elapsed < cb.config.CooldownDuration {
		return false
	}

	if atomic.CompareAndSwapInt32(&cb.state, int32(CircuitOpen), int32(CircuitHalfOpen)) {
		atomic.StoreInt64(&cb.lastStateChange, time.Now().UnixNano())
		atomic.StoreInt32(&cb.successes, 0)
	}
	return true
}

func (cb *CircuitBreaker) RecordResult(success bool) {
	cb.mu.Lock()
	cb.recordToWindow(success)
	failureRate := cb.calculateFailureRate()
	cb.mu.Unlock()

	if success {
		cb.recordSuccess()
	} else {
		cb.recordFailure(failureRate)
	}
}

func (cb *CircuitBreaker) recordToWindow(success bool) {
	cb.recentResults[cb.windowIndex] = success
	cb.windowIndex = (cb.windowIndex + 1) % len(cb.recentResults)
}

func (cb *CircuitBreaker) recordSuccess() {
	atomic.StoreInt32(&cb.failures, 0)

	state := CircuitState(atomic.LoadInt32(&cb.state))
	if state == CircuitHalfOpen {
		newSuccesses := atomic.AddInt32(&cb.successes, 1)
		if int(newSuccesses) >= cb.config.SuccessThreshold {
			cb.transitionTo(CircuitClosed)
		}
	}
}

func (cb *CircuitBreaker) recordFailure(failureRate float64) {
	newFailures := atomic.AddInt32(&cb.failures, 1)
	atomic.StoreInt32(&cb.successes, 0)

	state := CircuitState(atomic.LoadInt32(&cb.state))
	if state == CircuitHalfOpen {
		cb.transitionTo(CircuitOpen)
		return
	}

	if state != CircuitOpen && cb.shouldTrip(int(newFailures), failureRate) {
		cb.transitionTo(CircuitOpen)
	}
}

func (cb *CircuitBreaker) shouldTrip(failures int, failureRate float64) bool {
	if failures >= cb.config.ConsecutiveFailures {
		return true
	}
	return failureRate >= cb.config.FailureRateThreshold
}

func (cb *CircuitBreaker) calculateFailureRate() float64 {
	if len(cb.recentResults) == 0 {
		return 0.0
	}

	failures := 0
	for _, success := range cb.recentResults {
		if !success {
			failures++
		}
	}

	return float64(failures) / float64(len(cb.recentResults))
}

func (cb *CircuitBreaker) transitionTo(state CircuitState) {
	atomic.StoreInt32(&cb.state, int32(state))
	atomic.StoreInt64(&cb.lastStateChange, time.Now().UnixNano())

	if state == CircuitClosed {
		atomic.StoreInt32(&cb.failures, 0)
		atomic.StoreInt32(&cb.successes, 0)
	} else if state == CircuitHalfOpen {
		atomic.StoreInt32(&cb.successes, 0)
	}
}

func (cb *CircuitBreaker) ForceReset() {
	cb.mu.Lock()
	cb.initializeWindow()
	cb.mu.Unlock()

	cb.transitionTo(CircuitClosed)
}

func (cb *CircuitBreaker) GetResourceID() string {
	return cb.resourceID
}

func (cb *CircuitBreaker) Config() CircuitBreakerConfig {
	return cb.config
}

func (cb *CircuitBreaker) FailureRate() float64 {
	cb.mu.Lock()
	rate := cb.calculateFailureRate()
	cb.mu.Unlock()
	return rate
}

func (cb *CircuitBreaker) LastStateChange() time.Time {
	nanos := atomic.LoadInt64(&cb.lastStateChange)
	return time.Unix(0, nanos)
}
