package guide

import (
	"sync"
	"time"
)

// =============================================================================
// Dead Letter Queue
// =============================================================================
//
// DeadLetterQueue stores messages that failed to be delivered or processed.
// This enables:
// - Post-mortem analysis of failures
// - Manual retry of failed messages
// - Alerting on failure patterns
// - Audit trail

// DeadLetterReason indicates why a message ended up in DLQ
type DeadLetterReason string

const (
	DeadLetterReasonCircuitOpen    DeadLetterReason = "circuit_open"
	DeadLetterReasonRetryExhausted DeadLetterReason = "retry_exhausted"
	DeadLetterReasonTimeout        DeadLetterReason = "timeout"
	DeadLetterReasonUnroutable     DeadLetterReason = "unroutable"
	DeadLetterReasonRejected       DeadLetterReason = "rejected"
	DeadLetterReasonProcessingErr  DeadLetterReason = "processing_error"
)

// DeadLetter represents a failed message
type DeadLetter struct {
	// Original message
	Message *Message `json:"message"`

	// Failure info
	Reason   DeadLetterReason `json:"reason"`
	Error    string           `json:"error,omitempty"`
	Topic    string           `json:"topic"`
	Attempts int              `json:"attempts"`

	// Timing
	OriginalTimestamp time.Time `json:"original_timestamp"`
	FailedAt          time.Time `json:"failed_at"`

	// Context
	SourceAgentID string `json:"source_agent_id,omitempty"`
	TargetAgentID string `json:"target_agent_id,omitempty"`

	// Retry state
	Retried   bool      `json:"retried"`
	RetriedAt time.Time `json:"retried_at,omitempty"`
}

// DeadLetterQueue stores failed messages
type DeadLetterQueue struct {
	mu      sync.RWMutex
	letters []*DeadLetter
	maxSize int
	onAdd   func(*DeadLetter)

	// Stats
	totalAdded   int64
	totalRetried int64
}

// DeadLetterQueueConfig configures the DLQ
type DeadLetterQueueConfig struct {
	MaxSize int               // Maximum letters to store (default: 10000)
	OnAdd   func(*DeadLetter) // Callback when letter is added
}

// NewDeadLetterQueue creates a new DLQ
func NewDeadLetterQueue(cfg DeadLetterQueueConfig) *DeadLetterQueue {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 10000
	}

	return &DeadLetterQueue{
		letters: make([]*DeadLetter, 0),
		maxSize: cfg.MaxSize,
		onAdd:   cfg.OnAdd,
	}
}

// Add adds a dead letter to the queue
func (q *DeadLetterQueue) Add(letter *DeadLetter) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Evict oldest if at capacity
	if len(q.letters) >= q.maxSize {
		q.letters = q.letters[1:]
	}

	letter.FailedAt = time.Now()
	q.letters = append(q.letters, letter)
	q.totalAdded++

	// Callback
	if q.onAdd != nil {
		go q.onAdd(letter)
	}
}

// AddFromMessage creates and adds a dead letter from a message
func (q *DeadLetterQueue) AddFromMessage(msg *Message, topic string, reason DeadLetterReason, err error, attempts int) {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	q.Add(&DeadLetter{
		Message:           msg,
		Reason:            reason,
		Error:             errStr,
		Topic:             topic,
		Attempts:          attempts,
		OriginalTimestamp: msg.Timestamp,
		SourceAgentID:     msg.SourceAgentID,
		TargetAgentID:     msg.TargetAgentID,
	})
}

// Get returns dead letters matching the filter
func (q *DeadLetterQueue) Get(filter DeadLetterFilter) []*DeadLetter {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var result []*DeadLetter
	for _, letter := range q.letters {
		if filter.Matches(letter) {
			result = append(result, letter)
		}
	}
	return result
}

// GetByCorrelationID returns dead letter by correlation ID
func (q *DeadLetterQueue) GetByCorrelationID(correlationID string) *DeadLetter {
	q.mu.RLock()
	defer q.mu.RUnlock()

	for _, letter := range q.letters {
		if letter.Message != nil && letter.Message.CorrelationID == correlationID {
			return letter
		}
	}
	return nil
}

// MarkRetried marks a dead letter as retried
func (q *DeadLetterQueue) MarkRetried(correlationID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, letter := range q.letters {
		if letter.Message != nil && letter.Message.CorrelationID == correlationID {
			letter.Retried = true
			letter.RetriedAt = time.Now()
			q.totalRetried++
			return true
		}
	}
	return false
}

// Remove removes a dead letter by correlation ID
func (q *DeadLetterQueue) Remove(correlationID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, letter := range q.letters {
		if letter.Message != nil && letter.Message.CorrelationID == correlationID {
			q.letters = append(q.letters[:i], q.letters[i+1:]...)
			return true
		}
	}
	return false
}

// Clear removes all dead letters
func (q *DeadLetterQueue) Clear() {
	q.mu.Lock()
	q.letters = make([]*DeadLetter, 0)
	q.mu.Unlock()
}

// Len returns the number of dead letters
func (q *DeadLetterQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.letters)
}

// Stats returns DLQ statistics
func (q *DeadLetterQueue) Stats() DeadLetterStats {
	q.mu.RLock()
	defer q.mu.RUnlock()

	stats := DeadLetterStats{
		Size:         len(q.letters),
		MaxSize:      q.maxSize,
		TotalAdded:   q.totalAdded,
		TotalRetried: q.totalRetried,
		ByReason:     make(map[DeadLetterReason]int),
	}

	for _, letter := range q.letters {
		stats.ByReason[letter.Reason]++
	}

	return stats
}

// DeadLetterStats contains DLQ statistics
type DeadLetterStats struct {
	Size         int                      `json:"size"`
	MaxSize      int                      `json:"max_size"`
	TotalAdded   int64                    `json:"total_added"`
	TotalRetried int64                    `json:"total_retried"`
	ByReason     map[DeadLetterReason]int `json:"by_reason"`
}

// DeadLetterFilter filters dead letters
type DeadLetterFilter struct {
	Reason         DeadLetterReason
	SourceAgentID  string
	TargetAgentID  string
	Since          time.Time
	Until          time.Time
	IncludeRetried bool
}

// Matches returns true if the letter matches the filter
func (f DeadLetterFilter) Matches(letter *DeadLetter) bool {
	if !f.matchesReason(letter) {
		return false
	}
	if !f.matchesSourceAgent(letter) {
		return false
	}
	if !f.matchesTargetAgent(letter) {
		return false
	}
	if !f.matchesSince(letter) {
		return false
	}
	if !f.matchesUntil(letter) {
		return false
	}
	if !f.matchesRetried(letter) {
		return false
	}
	return true
}

func (f DeadLetterFilter) matchesReason(letter *DeadLetter) bool {
	if f.Reason == "" {
		return true
	}
	return letter.Reason == f.Reason
}

func (f DeadLetterFilter) matchesSourceAgent(letter *DeadLetter) bool {
	if f.SourceAgentID == "" {
		return true
	}
	return letter.SourceAgentID == f.SourceAgentID
}

func (f DeadLetterFilter) matchesTargetAgent(letter *DeadLetter) bool {
	if f.TargetAgentID == "" {
		return true
	}
	return letter.TargetAgentID == f.TargetAgentID
}

func (f DeadLetterFilter) matchesSince(letter *DeadLetter) bool {
	if f.Since.IsZero() {
		return true
	}
	return !letter.FailedAt.Before(f.Since)
}

func (f DeadLetterFilter) matchesUntil(letter *DeadLetter) bool {
	if f.Until.IsZero() {
		return true
	}
	return !letter.FailedAt.After(f.Until)
}

func (f DeadLetterFilter) matchesRetried(letter *DeadLetter) bool {
	if f.IncludeRetried {
		return true
	}
	return !letter.Retried
}
