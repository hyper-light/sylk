package classifier

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

const (
	contextPriority       = 15
	contextMethodName     = "context"
	defaultDecayRate      = 0.1 // Exponential decay per message
	defaultMaxHistory     = 50
	contextMinConfidence  = 0.30
	contextBoostThreshold = 0.60
)

// SessionEntry represents a single entry in conversation history.
type SessionEntry struct {
	Domain    domain.Domain
	Timestamp time.Time
	Weight    float64
}

// SessionHistoryProvider retrieves session conversation history.
type SessionHistoryProvider interface {
	GetRecentDomains(ctx context.Context, sessionID string, limit int) ([]SessionEntry, error)
}

// ContextClassifier uses session history to infer domain affinity.
type ContextClassifier struct {
	provider   SessionHistoryProvider
	decayRate  float64
	maxHistory int
	threshold  float64
	mu         sync.RWMutex
}

// ContextClassifierConfig configures the context classifier.
type ContextClassifierConfig struct {
	DecayRate  float64
	MaxHistory int
	Threshold  float64
}

// NewContextClassifier creates a new ContextClassifier.
func NewContextClassifier(
	provider SessionHistoryProvider,
	config *ContextClassifierConfig,
) *ContextClassifier {
	cc := &ContextClassifier{
		provider:   provider,
		decayRate:  defaultDecayRate,
		maxHistory: defaultMaxHistory,
		threshold:  contextBoostThreshold,
	}

	if config != nil {
		cc.applyConfig(config)
	}

	return cc
}

func (c *ContextClassifier) applyConfig(config *ContextClassifierConfig) {
	if config.DecayRate > 0 {
		c.decayRate = config.DecayRate
	}
	if config.MaxHistory > 0 {
		c.maxHistory = config.MaxHistory
	}
	if config.Threshold > 0 {
		c.threshold = config.Threshold
	}
}

func (c *ContextClassifier) Classify(
	ctx context.Context,
	_ string,
	_ concurrency.AgentType,
) (*StageResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := NewStageResult()
	result.SetMethod(contextMethodName)

	sessionID := c.extractSessionID(ctx)
	if sessionID == "" || c.provider == nil {
		return result, nil
	}

	entries, err := c.fetchHistory(ctx, sessionID)
	if err != nil || len(entries) == 0 {
		return result, nil
	}

	scores := c.computeDecayedScores(entries)
	c.populateResult(result, scores)

	return result, nil
}

func (c *ContextClassifier) extractSessionID(ctx context.Context) string {
	if val := ctx.Value(sessionIDKey{}); val != nil {
		if sid, ok := val.(string); ok {
			return sid
		}
	}
	return ""
}

type sessionIDKey struct{}

// WithSessionID adds a session ID to the context.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, sessionID)
}

func (c *ContextClassifier) fetchHistory(
	ctx context.Context,
	sessionID string,
) ([]SessionEntry, error) {
	c.mu.RLock()
	limit := c.maxHistory
	c.mu.RUnlock()

	return c.provider.GetRecentDomains(ctx, sessionID, limit)
}

func (c *ContextClassifier) computeDecayedScores(entries []SessionEntry) map[domain.Domain]float64 {
	scores := make(map[domain.Domain]float64)
	c.mu.RLock()
	rate := c.decayRate
	c.mu.RUnlock()

	for i, entry := range entries {
		weight := c.calculateWeight(i, rate, entry.Weight)
		scores[entry.Domain] += weight
	}

	return c.normalizeScores(scores)
}

func (c *ContextClassifier) calculateWeight(position int, rate, entryWeight float64) float64 {
	decay := math.Exp(-rate * float64(position))
	if entryWeight > 0 {
		return decay * entryWeight
	}
	return decay
}

func (c *ContextClassifier) normalizeScores(scores map[domain.Domain]float64) map[domain.Domain]float64 {
	if len(scores) == 0 {
		return scores
	}

	var maxScore float64
	for _, score := range scores {
		if score > maxScore {
			maxScore = score
		}
	}

	if maxScore == 0 {
		return scores
	}

	normalized := make(map[domain.Domain]float64, len(scores))
	for d, score := range scores {
		normalized[d] = score / maxScore
	}
	return normalized
}

func (c *ContextClassifier) populateResult(
	result *StageResult,
	scores map[domain.Domain]float64,
) {
	for d, score := range scores {
		if score >= contextMinConfidence {
			result.AddDomain(d, score, []string{"session_history"})
		}
	}
}

func (c *ContextClassifier) Name() string {
	return contextMethodName
}

func (c *ContextClassifier) Priority() int {
	return contextPriority
}

// UpdateConfig updates the classifier configuration.
func (c *ContextClassifier) UpdateConfig(config *ContextClassifierConfig) {
	if config == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.applyConfig(config)
}

// GetDecayRate returns the current decay rate.
func (c *ContextClassifier) GetDecayRate() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.decayRate
}

// GetMaxHistory returns the maximum history entries to consider.
func (c *ContextClassifier) GetMaxHistory() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.maxHistory
}
