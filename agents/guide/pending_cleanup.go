package guide

import (
	"sync"
	"time"
)

// =============================================================================
// Pending Request Cleanup
// =============================================================================
//
// PendingCleanup periodically cleans up stale pending requests that have
// exceeded their timeout. This prevents memory leaks and enables detection
// of unresponsive agents.

// PendingEntry represents a pending request with timeout info
type PendingEntry struct {
	CorrelationID string
	TargetAgentID string
	SentAt        time.Time
	Timeout       time.Duration
	FireAndForget bool

	// Callback when timeout occurs
	OnTimeout func(entry *PendingEntry)
}

// IsExpired returns true if the entry has exceeded its timeout
func (e *PendingEntry) IsExpired() bool {
	return time.Since(e.SentAt) > e.Timeout
}

// TimeRemaining returns how much time is left before timeout
func (e *PendingEntry) TimeRemaining() time.Duration {
	remaining := e.Timeout - time.Since(e.SentAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// PendingCleanup manages pending request timeouts
type PendingCleanup struct {
	pending  *ShardedMap[string, *PendingEntry]
	dlq      *DeadLetterQueue
	circuits *CircuitBreakerRegistry
	health   *HealthMonitor

	// Configuration
	checkInterval  time.Duration
	defaultTimeout time.Duration

	// Control
	stopCh  chan struct{}
	stopped bool
	mu      sync.Mutex

	// Stats
	totalExpired int64
}

// PendingCleanupConfig configures the pending cleanup
type PendingCleanupConfig struct {
	CheckInterval  time.Duration // Default: 1s
	DefaultTimeout time.Duration // Default: 30s
	DLQ            *DeadLetterQueue
	Circuits       *CircuitBreakerRegistry
	Health         *HealthMonitor
}

// NewPendingCleanup creates a new pending cleanup manager
func NewPendingCleanup(cfg PendingCleanupConfig) *PendingCleanup {
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 1 * time.Second
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 30 * time.Second
	}

	return &PendingCleanup{
		pending:        NewStringMap[*PendingEntry](DefaultShardCount),
		dlq:            cfg.DLQ,
		circuits:       cfg.Circuits,
		health:         cfg.Health,
		checkInterval:  cfg.CheckInterval,
		defaultTimeout: cfg.DefaultTimeout,
		stopCh:         make(chan struct{}),
	}
}

// Start begins cleanup processing
func (c *PendingCleanup) Start() {
	go c.cleanupLoop()
}

// Stop halts cleanup processing
func (c *PendingCleanup) Stop() {
	c.mu.Lock()
	if !c.stopped {
		c.stopped = true
		close(c.stopCh)
	}
	c.mu.Unlock()
}

// Add adds a pending request entry
func (c *PendingCleanup) Add(entry *PendingEntry) {
	if entry.Timeout <= 0 {
		entry.Timeout = c.defaultTimeout
	}
	c.pending.Set(entry.CorrelationID, entry)
}

// Remove removes a pending request (when response received)
func (c *PendingCleanup) Remove(correlationID string) {
	c.pending.Delete(correlationID)
}

// Get returns a pending entry by correlation ID
func (c *PendingCleanup) Get(correlationID string) (*PendingEntry, bool) {
	return c.pending.Get(correlationID)
}

// cleanupLoop periodically cleans up expired entries
func (c *PendingCleanup) cleanupLoop() {
	ticker := time.NewTicker(c.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.cleanup()
		}
	}
}

// cleanup removes expired entries
func (c *PendingCleanup) cleanup() {
	var expired []*PendingEntry

	// Find expired entries
	c.pending.Range(func(correlationID string, entry *PendingEntry) bool {
		if entry.IsExpired() {
			expired = append(expired, entry)
		}
		return true
	})

	// Process expired entries
	for _, entry := range expired {
		c.handleExpired(entry)
	}
}

// handleExpired processes an expired entry
func (c *PendingCleanup) handleExpired(entry *PendingEntry) {
	// Remove from pending
	c.pending.Delete(entry.CorrelationID)
	c.totalExpired++

	// Record failure in circuit breaker
	if c.circuits != nil && entry.TargetAgentID != "" {
		c.circuits.RecordFailure(entry.TargetAgentID)
	}

	// Mark agent as degraded in health monitor
	if c.health != nil && entry.TargetAgentID != "" {
		if health, ok := c.health.agents.Get(entry.TargetAgentID); ok {
			health.SetDegraded()
		}
	}

	// Add to DLQ if configured
	if c.dlq != nil {
		c.dlq.Add(&DeadLetter{
			Message: &Message{
				CorrelationID: entry.CorrelationID,
				TargetAgentID: entry.TargetAgentID,
				Timestamp:     entry.SentAt,
			},
			Reason:            DeadLetterReasonTimeout,
			Error:             "request timed out",
			Attempts:          1,
			OriginalTimestamp: entry.SentAt,
			TargetAgentID:     entry.TargetAgentID,
		})
	}

	// Call timeout callback
	if entry.OnTimeout != nil {
		go entry.OnTimeout(entry)
	}
}

// Len returns the number of pending requests
func (c *PendingCleanup) Len() int {
	return c.pending.Len()
}

// Stats returns cleanup statistics
func (c *PendingCleanup) Stats() PendingCleanupStats {
	return PendingCleanupStats{
		Pending:        c.pending.Len(),
		TotalExpired:   c.totalExpired,
		DefaultTimeout: c.defaultTimeout.String(),
	}
}

// PendingCleanupStats contains cleanup statistics
type PendingCleanupStats struct {
	Pending        int    `json:"pending"`
	TotalExpired   int64  `json:"total_expired"`
	DefaultTimeout string `json:"default_timeout"`
}

// GetByAgent returns all pending requests for an agent
func (c *PendingCleanup) GetByAgent(agentID string) []*PendingEntry {
	var result []*PendingEntry
	c.pending.Range(func(correlationID string, entry *PendingEntry) bool {
		if entry.TargetAgentID == agentID {
			result = append(result, entry)
		}
		return true
	})
	return result
}

// TimeoutAllForAgent times out all pending requests for an agent
// Use when an agent is confirmed dead
func (c *PendingCleanup) TimeoutAllForAgent(agentID string) int {
	entries := c.GetByAgent(agentID)
	for _, entry := range entries {
		c.handleExpired(entry)
	}
	return len(entries)
}
