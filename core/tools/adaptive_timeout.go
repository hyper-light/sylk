package tools

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// Configuration
// =============================================================================

const (
	DefaultBaseTimeout    = 60 * time.Second
	DefaultExtendOnOutput = 30 * time.Second
	DefaultMaxTimeout     = 30 * time.Minute
	DefaultCheckInterval  = 1 * time.Second
)

// Default noise patterns that don't extend timeout
var DefaultNoisePatterns = []string{
	`^\.+$`,        // Dots only (...)
	`^[|/\-\\]+$`,  // Spinner characters
	`^\d+%$`,       // Percentage only (50%)
	`^[\s]*$`,      // Whitespace only
	`^\[=*>?\s*\]`, // Progress bars like [=====>    ]
}

// AdaptiveTimeoutConfig holds configuration for adaptive timeout
type AdaptiveTimeoutConfig struct {
	// BaseTimeout is the starting timeout duration
	BaseTimeout time.Duration

	// ExtendOnOutput is how much to extend on meaningful output
	ExtendOnOutput time.Duration

	// MaxTimeout is the hard ceiling for timeout
	MaxTimeout time.Duration

	// NoisePatterns are regex patterns for output that shouldn't extend timeout
	NoisePatterns []string

	// CheckInterval is how often to check for timeout (used by callers)
	CheckInterval time.Duration
}

// DefaultAdaptiveTimeoutConfig returns sensible defaults
func DefaultAdaptiveTimeoutConfig() AdaptiveTimeoutConfig {
	return AdaptiveTimeoutConfig{
		BaseTimeout:    DefaultBaseTimeout,
		ExtendOnOutput: DefaultExtendOnOutput,
		MaxTimeout:     DefaultMaxTimeout,
		NoisePatterns:  DefaultNoisePatterns,
		CheckInterval:  DefaultCheckInterval,
	}
}

// =============================================================================
// Adaptive Timeout
// =============================================================================

// AdaptiveTimeout extends timeout while meaningful output is being produced
type AdaptiveTimeout struct {
	baseTimeout    time.Duration
	extendOnOutput time.Duration
	maxTimeout     time.Duration
	checkInterval  time.Duration

	noisePatterns []*regexp.Regexp

	mu             sync.Mutex
	lastOutputTime time.Time
	started        time.Time
	outputCount    int64
}

// NewAdaptiveTimeout creates a new adaptive timeout manager
func NewAdaptiveTimeout(cfg AdaptiveTimeoutConfig) (*AdaptiveTimeout, error) {
	cfg = normalizeAdaptiveTimeoutConfig(cfg)

	patterns, err := compileNoisePatterns(cfg.NoisePatterns)
	if err != nil {
		return nil, err
	}

	return &AdaptiveTimeout{
		baseTimeout:    cfg.BaseTimeout,
		extendOnOutput: cfg.ExtendOnOutput,
		maxTimeout:     cfg.MaxTimeout,
		checkInterval:  cfg.CheckInterval,
		noisePatterns:  patterns,
	}, nil
}

func normalizeAdaptiveTimeoutConfig(cfg AdaptiveTimeoutConfig) AdaptiveTimeoutConfig {
	cfg.BaseTimeout = defaultDuration(cfg.BaseTimeout, DefaultBaseTimeout)
	cfg.ExtendOnOutput = defaultDuration(cfg.ExtendOnOutput, DefaultExtendOnOutput)
	cfg.MaxTimeout = defaultDuration(cfg.MaxTimeout, DefaultMaxTimeout)
	cfg.NoisePatterns = defaultPatterns(cfg.NoisePatterns, DefaultNoisePatterns)
	cfg.CheckInterval = defaultDuration(cfg.CheckInterval, DefaultCheckInterval)
	return cfg
}

func defaultDuration(val, def time.Duration) time.Duration {
	if val == 0 {
		return def
	}
	return val
}

func defaultPatterns(val, def []string) []string {
	if len(val) == 0 {
		return def
	}
	return val
}

func compileNoisePatterns(patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

// =============================================================================
// Timeout Operations
// =============================================================================

// Start marks the start of the timed operation
func (a *AdaptiveTimeout) Start() {
	a.mu.Lock()
	a.started = time.Now()
	a.lastOutputTime = a.started
	a.outputCount = 0
	a.mu.Unlock()
}

// OnOutput is called when output is received from the process
func (a *AdaptiveTimeout) OnOutput(line string) {
	if a.isNoise(line) {
		return
	}

	a.mu.Lock()
	a.lastOutputTime = time.Now()
	a.outputCount++
	a.mu.Unlock()
}

// isNoise checks if a line matches any noise pattern
func (a *AdaptiveTimeout) isNoise(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}

	for _, pattern := range a.noisePatterns {
		if pattern.MatchString(trimmed) {
			return true
		}
	}

	return false
}

// ShouldTimeout checks if the process should be timed out
func (a *AdaptiveTimeout) ShouldTimeout() bool {
	started, lastOutput := a.snapshotTimes()
	return a.checkTimeout(started, lastOutput)
}

func (a *AdaptiveTimeout) checkTimeout(started, lastOutput time.Time) bool {
	elapsed := time.Since(started)

	// Hard max timeout always applies
	if elapsed > a.maxTimeout {
		return true
	}

	// If recent meaningful output, don't timeout
	if time.Since(lastOutput) < a.extendOnOutput {
		return false
	}

	// Base timeout has passed and no recent output
	return elapsed > a.baseTimeout
}

func (a *AdaptiveTimeout) snapshotTimes() (time.Time, time.Time) {
	a.mu.Lock()
	started := a.started
	lastOutput := a.lastOutputTime
	a.mu.Unlock()
	return started, lastOutput
}

// ShouldTimeoutAt checks if the process should be timed out given a specific start time
func (a *AdaptiveTimeout) ShouldTimeoutAt(started time.Time) bool {
	_, lastOutput := a.snapshotTimes()
	return a.checkTimeout(started, lastOutput)
}

// =============================================================================
// State Accessors
// =============================================================================

// Elapsed returns the time elapsed since start
func (a *AdaptiveTimeout) Elapsed() time.Duration {
	started, _ := a.snapshotTimes()
	if started.IsZero() {
		return 0
	}
	return time.Since(started)
}

// TimeSinceLastOutput returns the time since last meaningful output
func (a *AdaptiveTimeout) TimeSinceLastOutput() time.Duration {
	_, lastOutput := a.snapshotTimes()
	if lastOutput.IsZero() {
		return 0
	}
	return time.Since(lastOutput)
}

// OutputCount returns the number of meaningful output lines received
func (a *AdaptiveTimeout) OutputCount() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.outputCount
}

// RemainingTime returns the estimated remaining time before timeout
func (a *AdaptiveTimeout) RemainingTime() time.Duration {
	started, lastOutput := a.snapshotTimes()
	return a.calculateRemaining(started, lastOutput)
}

func (a *AdaptiveTimeout) calculateRemaining(started, lastOutput time.Time) time.Duration {
	if started.IsZero() {
		return a.baseTimeout
	}

	elapsed := time.Since(started)
	maxRemaining := a.maxTimeout - elapsed

	if maxRemaining <= 0 {
		return 0
	}

	return a.calculateEffectiveRemaining(elapsed, lastOutput, maxRemaining)
}

func (a *AdaptiveTimeout) calculateEffectiveRemaining(elapsed time.Duration, lastOutput time.Time, maxRemaining time.Duration) time.Duration {
	sinceOutput := time.Since(lastOutput)
	extendedRemaining := a.extendOnOutput - sinceOutput

	if extendedRemaining > 0 {
		return minDuration(extendedRemaining, maxRemaining)
	}

	baseRemaining := a.baseTimeout - elapsed
	if baseRemaining <= 0 {
		return 0
	}

	return minDuration(baseRemaining, maxRemaining)
}

// CheckInterval returns the recommended interval for checking timeout
func (a *AdaptiveTimeout) CheckInterval() time.Duration {
	return a.checkInterval
}

// =============================================================================
// Configuration Accessors
// =============================================================================

// BaseTimeout returns the base timeout duration
func (a *AdaptiveTimeout) BaseTimeout() time.Duration {
	return a.baseTimeout
}

// ExtendOnOutput returns the extension duration on meaningful output
func (a *AdaptiveTimeout) ExtendOnOutput() time.Duration {
	return a.extendOnOutput
}

// MaxTimeout returns the maximum timeout duration
func (a *AdaptiveTimeout) MaxTimeout() time.Duration {
	return a.maxTimeout
}

// =============================================================================
// Reset
// =============================================================================

// Reset resets the timeout state for reuse
func (a *AdaptiveTimeout) Reset() {
	a.mu.Lock()
	a.started = time.Time{}
	a.lastOutputTime = time.Time{}
	a.outputCount = 0
	a.mu.Unlock()
}

// =============================================================================
// Per-Tool Timeout Management
// =============================================================================

// ToolTimeoutManager manages per-tool timeout configurations
type ToolTimeoutManager struct {
	mu            sync.RWMutex
	toolTimeouts  map[string]AdaptiveTimeoutConfig
	defaultConfig AdaptiveTimeoutConfig
}

// NewToolTimeoutManager creates a new tool timeout manager
func NewToolTimeoutManager(defaultConfig AdaptiveTimeoutConfig) *ToolTimeoutManager {
	return &ToolTimeoutManager{
		toolTimeouts:  make(map[string]AdaptiveTimeoutConfig),
		defaultConfig: normalizeAdaptiveTimeoutConfig(defaultConfig),
	}
}

// SetToolTimeout sets a custom timeout configuration for a specific tool
func (m *ToolTimeoutManager) SetToolTimeout(toolName string, cfg AdaptiveTimeoutConfig) {
	m.mu.Lock()
	m.toolTimeouts[toolName] = normalizeAdaptiveTimeoutConfig(cfg)
	m.mu.Unlock()
}

// GetTimeoutForTool returns an AdaptiveTimeout configured for the specified tool
func (m *ToolTimeoutManager) GetTimeoutForTool(toolName string) (*AdaptiveTimeout, error) {
	m.mu.RLock()
	cfg, exists := m.toolTimeouts[toolName]
	if !exists {
		cfg = m.defaultConfig
	}
	m.mu.RUnlock()

	return NewAdaptiveTimeout(cfg)
}

// RemoveToolTimeout removes a custom timeout configuration for a tool
func (m *ToolTimeoutManager) RemoveToolTimeout(toolName string) {
	m.mu.Lock()
	delete(m.toolTimeouts, toolName)
	m.mu.Unlock()
}

// ListToolTimeouts returns all tool names with custom timeout configurations
func (m *ToolTimeoutManager) ListToolTimeouts() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tools := make([]string, 0, len(m.toolTimeouts))
	for tool := range m.toolTimeouts {
		tools = append(tools, tool)
	}
	return tools
}

// =============================================================================
// Helpers
// =============================================================================

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
