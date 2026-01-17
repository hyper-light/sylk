package llm

import (
	"sync"
	"time"
)

// UsageRecord represents a single LLM usage event.
type UsageRecord struct {
	Timestamp    time.Time
	SessionID    string
	PipelineID   string
	TaskID       string
	AgentID      string
	AgentType    string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	Cost         float64
	Currency     string
	Latency      time.Duration
}

// UsageTracker stores usage records with cached aggregates for O(1) lookups.
type UsageTracker struct {
	mu               sync.RWMutex
	records          []UsageRecord
	totalTokens      int64
	tokensBySession  map[string]int64
	tokensByTask     map[string]int64
	tokensByAgent    map[string]int64
	tokensByProvider map[string]int64
}

// NewUsageTracker creates a new UsageTracker.
func NewUsageTracker() *UsageTracker {
	return &UsageTracker{
		records:          make([]UsageRecord, 0),
		tokensBySession:  make(map[string]int64),
		tokensByTask:     make(map[string]int64),
		tokensByAgent:    make(map[string]int64),
		tokensByProvider: make(map[string]int64),
	}
}

// Record adds a usage record and updates cached aggregates.
func (t *UsageTracker) Record(record UsageRecord) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.records = append(t.records, record)
	t.updateAggregates(record)
}

func (t *UsageTracker) updateAggregates(record UsageRecord) {
	t.totalTokens += record.TotalTokens
	t.tokensBySession[record.SessionID] += record.TotalTokens
	t.tokensByTask[record.TaskID] += record.TotalTokens
	t.tokensByAgent[record.AgentType] += record.TotalTokens
	t.tokensByProvider[record.Provider] += record.TotalTokens
}

// TotalTokens returns the global total of tokens used.
func (t *UsageTracker) TotalTokens() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.totalTokens
}

// TokensBySession returns the total tokens for a specific session.
func (t *UsageTracker) TokensBySession(sessionID string) int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tokensBySession[sessionID]
}

// TokensByTask returns the total tokens for a specific task.
func (t *UsageTracker) TokensByTask(taskID string) int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tokensByTask[taskID]
}

// TokensByProvider returns the total tokens for a specific provider.
func (t *UsageTracker) TokensByProvider(provider string) int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tokensByProvider[provider]
}

// TokensByAgent returns the total tokens for a specific agent type.
func (t *UsageTracker) TokensByAgent(agentType string) int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tokensByAgent[agentType]
}

// Records returns a copy of all usage records.
func (t *UsageTracker) Records() []UsageRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]UsageRecord, len(t.records))
	copy(result, t.records)
	return result
}

// CalculateCost computes the cost for given token counts and prices.
func CalculateCost(inputTokens, outputTokens int64, inputPrice, outputPrice float64) float64 {
	inputCost := float64(inputTokens) / 1_000_000 * inputPrice
	outputCost := float64(outputTokens) / 1_000_000 * outputPrice
	return inputCost + outputCost
}

// CalculateCostFromModel computes the cost using pricing loaded from ModelInfo.
func CalculateCostFromModel(inputTokens, outputTokens int64, info ModelInfo) float64 {
	return CalculateCost(inputTokens, outputTokens, info.InputPricePerM, info.OutputPricePerM)
}
