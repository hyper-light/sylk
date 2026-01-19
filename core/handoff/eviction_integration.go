package handoff

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// HO.11.3 HandoffAwareEviction - Eviction Strategy with Handoff Integration
// =============================================================================
//
// HandoffAwareEviction implements an eviction strategy that considers handoff
// worthiness when selecting items to evict. Higher-quality items that are more
// valuable for potential handoffs are preserved longer.
//
// Key features:
//   - Integration with GP predictions for quality scoring
//   - Considers handoff-worthiness when selecting eviction candidates
//   - Higher-quality items preserved longer
//   - Configurable quality thresholds and weights
//   - Metrics collection
//
// Thread Safety:
//   - All operations are protected by appropriate synchronization
//   - Safe for concurrent use from multiple goroutines
//
// Example usage:
//
//	gp := NewAgentGaussianProcess(nil)
//	eviction := NewHandoffAwareEviction(gp, config)
//	candidates := eviction.SelectForEviction(ctx, entries, targetTokens)

// =============================================================================
// EvictableEntry - Entry Interface for Eviction
// =============================================================================

// EvictableEntry represents an entry that can be evaluated for eviction.
type EvictableEntry interface {
	// GetID returns the unique identifier of the entry.
	GetID() string

	// GetTokenCount returns the token count of the entry.
	GetTokenCount() int

	// GetTimestamp returns when the entry was created.
	GetTimestamp() time.Time

	// GetTurnNumber returns the conversation turn number.
	GetTurnNumber() int

	// GetContentType returns the type of content.
	GetContentType() string

	// GetMetadata returns entry metadata.
	GetMetadata() map[string]string
}

// BasicEvictableEntry is a simple implementation of EvictableEntry.
type BasicEvictableEntry struct {
	ID          string            `json:"id"`
	TokenCount  int               `json:"token_count"`
	Timestamp   time.Time         `json:"timestamp"`
	TurnNumber  int               `json:"turn_number"`
	ContentType string            `json:"content_type"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// GetID returns the unique identifier.
func (e *BasicEvictableEntry) GetID() string { return e.ID }

// GetTokenCount returns the token count.
func (e *BasicEvictableEntry) GetTokenCount() int { return e.TokenCount }

// GetTimestamp returns the timestamp.
func (e *BasicEvictableEntry) GetTimestamp() time.Time { return e.Timestamp }

// GetTurnNumber returns the turn number.
func (e *BasicEvictableEntry) GetTurnNumber() int { return e.TurnNumber }

// GetContentType returns the content type.
func (e *BasicEvictableEntry) GetContentType() string { return e.ContentType }

// GetMetadata returns the metadata.
func (e *BasicEvictableEntry) GetMetadata() map[string]string { return e.Metadata }

// =============================================================================
// EvictionCandidate - Scored Entry for Eviction
// =============================================================================

// EvictionCandidate represents an entry with its eviction score.
type EvictionCandidate struct {
	// Entry is the original entry.
	Entry EvictableEntry `json:"entry"`

	// EvictionScore indicates how likely this should be evicted.
	// Higher score = more likely to be evicted.
	EvictionScore float64 `json:"eviction_score"`

	// HandoffWorthiness indicates how valuable this is for handoffs.
	// Higher score = more valuable, less likely to be evicted.
	HandoffWorthiness float64 `json:"handoff_worthiness"`

	// PredictedQuality is the GP-predicted quality score.
	PredictedQuality float64 `json:"predicted_quality"`

	// QualityUncertainty is the uncertainty in quality prediction.
	QualityUncertainty float64 `json:"quality_uncertainty"`

	// AgeScore is the age-based eviction component.
	AgeScore float64 `json:"age_score"`

	// SizeScore is the size-based eviction component.
	SizeScore float64 `json:"size_score"`

	// TypeScore is the content-type based eviction component.
	TypeScore float64 `json:"type_score"`
}

// =============================================================================
// HandoffAwareEvictionConfig - Configuration
// =============================================================================

// HandoffAwareEvictionConfig configures the HandoffAwareEviction strategy.
type HandoffAwareEvictionConfig struct {
	// Name is the strategy identifier.
	Name string `json:"name"`

	// Priority is the strategy priority (lower = higher priority).
	Priority int `json:"priority"`

	// UseGPPrediction enables GP-based quality prediction.
	UseGPPrediction bool `json:"use_gp_prediction"`

	// QualityThreshold is the minimum quality to consider handoff-worthy.
	QualityThreshold float64 `json:"quality_threshold"`

	// HandoffWorthinessWeight is the weight for handoff worthiness in scoring.
	HandoffWorthinessWeight float64 `json:"handoff_worthiness_weight"`

	// AgeWeight is the weight for age in eviction scoring.
	AgeWeight float64 `json:"age_weight"`

	// SizeWeight is the weight for size in eviction scoring.
	SizeWeight float64 `json:"size_weight"`

	// TypeWeight is the weight for content type in eviction scoring.
	TypeWeight float64 `json:"type_weight"`

	// PreserveRecentTurns is the number of recent turns to preserve.
	PreserveRecentTurns int `json:"preserve_recent_turns"`

	// PreservedTypes are content types that should not be evicted.
	PreservedTypes []string `json:"preserved_types,omitempty"`

	// TypeScores maps content types to eviction scores (higher = more likely to evict).
	TypeScores map[string]float64 `json:"type_scores,omitempty"`

	// MaxAgeHours is the maximum age after which items become highly evictable.
	MaxAgeHours float64 `json:"max_age_hours"`
}

// DefaultHandoffAwareEvictionConfig returns sensible defaults.
func DefaultHandoffAwareEvictionConfig() *HandoffAwareEvictionConfig {
	return &HandoffAwareEvictionConfig{
		Name:                    "handoff_aware_eviction",
		Priority:                50,
		UseGPPrediction:         true,
		QualityThreshold:        0.5,
		HandoffWorthinessWeight: 0.4,
		AgeWeight:               0.3,
		SizeWeight:              0.2,
		TypeWeight:              0.1,
		PreserveRecentTurns:     5,
		PreservedTypes:          []string{"handoff_state", "context_reference"},
		TypeScores: map[string]float64{
			"tool_result":       0.8, // High eviction priority
			"tool_call":         0.7,
			"assistant_reply":   0.5,
			"user_prompt":       0.4,
			"code_file":         0.3,
			"research_paper":    0.2,
			"context_reference": 0.1, // Low eviction priority
			"handoff_state":     0.0, // Never evict
		},
		MaxAgeHours: 24.0,
	}
}

// Clone creates a deep copy of the config.
func (c *HandoffAwareEvictionConfig) Clone() *HandoffAwareEvictionConfig {
	if c == nil {
		return nil
	}
	cloned := &HandoffAwareEvictionConfig{
		Name:                    c.Name,
		Priority:                c.Priority,
		UseGPPrediction:         c.UseGPPrediction,
		QualityThreshold:        c.QualityThreshold,
		HandoffWorthinessWeight: c.HandoffWorthinessWeight,
		AgeWeight:               c.AgeWeight,
		SizeWeight:              c.SizeWeight,
		TypeWeight:              c.TypeWeight,
		PreserveRecentTurns:     c.PreserveRecentTurns,
		MaxAgeHours:             c.MaxAgeHours,
	}

	if c.PreservedTypes != nil {
		cloned.PreservedTypes = make([]string, len(c.PreservedTypes))
		copy(cloned.PreservedTypes, c.PreservedTypes)
	}

	if c.TypeScores != nil {
		cloned.TypeScores = make(map[string]float64, len(c.TypeScores))
		for k, v := range c.TypeScores {
			cloned.TypeScores[k] = v
		}
	}

	return cloned
}

// =============================================================================
// HandoffAwareEviction
// =============================================================================

// HandoffAwareEviction implements eviction with handoff awareness.
type HandoffAwareEviction struct {
	mu sync.RWMutex

	// gp is the Gaussian Process for quality prediction.
	gp *AgentGaussianProcess

	// config holds the eviction configuration.
	config *HandoffAwareEvictionConfig

	// preservedTypeSet is a set of preserved content types.
	preservedTypeSet map[string]struct{}

	// statistics
	stats evictionStatsInternal
}

// evictionStatsInternal holds internal statistics.
type evictionStatsInternal struct {
	evictionsPerformed atomic.Int64
	entriesEvicted     atomic.Int64
	tokensFreed        atomic.Int64
	entriesPreserved   atomic.Int64
	gpPredictions      atomic.Int64
	totalDurationNs    atomic.Int64
}

// NewHandoffAwareEviction creates a new HandoffAwareEviction.
func NewHandoffAwareEviction(
	gp *AgentGaussianProcess,
	config *HandoffAwareEvictionConfig,
) *HandoffAwareEviction {
	if config == nil {
		config = DefaultHandoffAwareEvictionConfig()
	}

	// Build preserved type set
	preservedTypeSet := make(map[string]struct{})
	for _, t := range config.PreservedTypes {
		preservedTypeSet[t] = struct{}{}
	}

	return &HandoffAwareEviction{
		gp:               gp,
		config:           config.Clone(),
		preservedTypeSet: preservedTypeSet,
	}
}

// =============================================================================
// EvictionStrategy Interface Implementation
// =============================================================================

// Name returns the strategy identifier.
func (e *HandoffAwareEviction) Name() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.config.Name
}

// Priority returns the strategy priority.
func (e *HandoffAwareEviction) Priority() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.config.Priority
}

// SelectForEviction selects entries to evict to free the target number of tokens.
func (e *HandoffAwareEviction) SelectForEviction(
	ctx context.Context,
	entries []EvictableEntry,
	targetTokens int,
) ([]EvictableEntry, error) {
	startTime := time.Now()
	defer func() {
		e.stats.totalDurationNs.Add(int64(time.Since(startTime)))
	}()

	e.mu.RLock()
	config := e.config.Clone()
	e.mu.RUnlock()

	if len(entries) == 0 || targetTokens <= 0 {
		return nil, nil
	}

	e.stats.evictionsPerformed.Add(1)

	// Filter out preserved entries
	candidates := e.filterPreservedEntries(entries, config)

	// Calculate current turn for recent turn preservation
	currentTurn := e.findMaxTurn(entries)

	// Score all candidates
	scoredCandidates := e.scoreCandidates(candidates, currentTurn, config)

	// Sort by eviction score (highest first = most likely to evict)
	sort.Slice(scoredCandidates, func(i, j int) bool {
		return scoredCandidates[i].EvictionScore > scoredCandidates[j].EvictionScore
	})

	// Select entries until we reach target tokens
	selected := e.selectUntilTarget(scoredCandidates, targetTokens)

	e.stats.entriesEvicted.Add(int64(len(selected)))

	return selected, nil
}

// filterPreservedEntries removes entries that should not be evicted.
func (e *HandoffAwareEviction) filterPreservedEntries(
	entries []EvictableEntry,
	config *HandoffAwareEvictionConfig,
) []EvictableEntry {
	var filtered []EvictableEntry

	for _, entry := range entries {
		contentType := entry.GetContentType()

		// Skip preserved types
		if _, preserved := e.preservedTypeSet[contentType]; preserved {
			e.stats.entriesPreserved.Add(1)
			continue
		}

		filtered = append(filtered, entry)
	}

	return filtered
}

// findMaxTurn finds the maximum turn number in entries.
func (e *HandoffAwareEviction) findMaxTurn(entries []EvictableEntry) int {
	maxTurn := 0
	for _, entry := range entries {
		if entry.GetTurnNumber() > maxTurn {
			maxTurn = entry.GetTurnNumber()
		}
	}
	return maxTurn
}

// scoreCandidates assigns eviction scores to all candidates.
func (e *HandoffAwareEviction) scoreCandidates(
	entries []EvictableEntry,
	currentTurn int,
	config *HandoffAwareEvictionConfig,
) []*EvictionCandidate {
	candidates := make([]*EvictionCandidate, 0, len(entries))
	now := time.Now()

	// Find max token count for normalization
	maxTokens := e.findMaxTokenCount(entries)

	for _, entry := range entries {
		candidate := e.scoreEntry(entry, currentTurn, maxTokens, now, config)

		// Skip recent turns
		if currentTurn-entry.GetTurnNumber() < config.PreserveRecentTurns {
			e.stats.entriesPreserved.Add(1)
			continue
		}

		candidates = append(candidates, candidate)
	}

	return candidates
}

// findMaxTokenCount finds the maximum token count for normalization.
func (e *HandoffAwareEviction) findMaxTokenCount(entries []EvictableEntry) int {
	maxTokens := 1 // Avoid division by zero
	for _, entry := range entries {
		if entry.GetTokenCount() > maxTokens {
			maxTokens = entry.GetTokenCount()
		}
	}
	return maxTokens
}

// scoreEntry computes the eviction score for a single entry.
func (e *HandoffAwareEviction) scoreEntry(
	entry EvictableEntry,
	currentTurn int,
	maxTokens int,
	now time.Time,
	config *HandoffAwareEvictionConfig,
) *EvictionCandidate {
	candidate := &EvictionCandidate{
		Entry: entry,
	}

	// Calculate age score (older = higher score = more likely to evict)
	ageHours := now.Sub(entry.GetTimestamp()).Hours()
	candidate.AgeScore = ageHours / config.MaxAgeHours
	if candidate.AgeScore > 1.0 {
		candidate.AgeScore = 1.0
	}

	// Calculate size score (larger = higher score)
	candidate.SizeScore = float64(entry.GetTokenCount()) / float64(maxTokens)

	// Calculate type score
	contentType := entry.GetContentType()
	if score, ok := config.TypeScores[contentType]; ok {
		candidate.TypeScore = score
	} else {
		candidate.TypeScore = 0.5 // Default for unknown types
	}

	// Calculate handoff worthiness using GP if available
	if config.UseGPPrediction && e.gp != nil {
		e.stats.gpPredictions.Add(1)

		// Use turn number and token count as context features
		prediction := e.gp.Predict(
			entry.GetTurnNumber()*1000, // Approximate context size
			entry.GetTokenCount(),
			0, // No tool calls for individual entries
		)

		candidate.PredictedQuality = prediction.Mean
		candidate.QualityUncertainty = prediction.StdDev

		// Higher quality = higher handoff worthiness = less likely to evict
		if prediction.Mean >= config.QualityThreshold {
			candidate.HandoffWorthiness = prediction.Mean
		} else {
			candidate.HandoffWorthiness = 0.0
		}
	} else {
		// Without GP, use a simple heuristic based on content type and recency
		turnDistance := float64(currentTurn - entry.GetTurnNumber())
		candidate.HandoffWorthiness = 1.0 / (1.0 + turnDistance*0.1)
		candidate.PredictedQuality = candidate.HandoffWorthiness
	}

	// Calculate final eviction score
	// Higher score = more likely to be evicted
	// Handoff worthiness is inverted (higher worthiness = lower eviction score)
	candidate.EvictionScore =
		config.AgeWeight*candidate.AgeScore +
			config.SizeWeight*candidate.SizeScore +
			config.TypeWeight*candidate.TypeScore -
			config.HandoffWorthinessWeight*candidate.HandoffWorthiness

	// Clamp to [0, 1]
	if candidate.EvictionScore < 0 {
		candidate.EvictionScore = 0
	}
	if candidate.EvictionScore > 1 {
		candidate.EvictionScore = 1
	}

	return candidate
}

// selectUntilTarget selects entries until target tokens are reached.
func (e *HandoffAwareEviction) selectUntilTarget(
	candidates []*EvictionCandidate,
	targetTokens int,
) []EvictableEntry {
	var selected []EvictableEntry
	freedTokens := 0

	for _, candidate := range candidates {
		if freedTokens >= targetTokens {
			break
		}

		selected = append(selected, candidate.Entry)
		freedTokens += candidate.Entry.GetTokenCount()
	}

	e.stats.tokensFreed.Add(int64(freedTokens))

	return selected
}

// =============================================================================
// Additional Methods
// =============================================================================

// ScoreEntries returns scored candidates without actually evicting.
// Useful for analysis and preview.
func (e *HandoffAwareEviction) ScoreEntries(
	entries []EvictableEntry,
) []*EvictionCandidate {
	e.mu.RLock()
	config := e.config.Clone()
	e.mu.RUnlock()

	candidates := e.filterPreservedEntries(entries, config)
	currentTurn := e.findMaxTurn(entries)

	return e.scoreCandidates(candidates, currentTurn, config)
}

// GetHandoffWorthiness returns the handoff worthiness score for an entry.
func (e *HandoffAwareEviction) GetHandoffWorthiness(entry EvictableEntry) float64 {
	e.mu.RLock()
	config := e.config.Clone()
	e.mu.RUnlock()

	candidate := e.scoreEntry(entry, entry.GetTurnNumber(), entry.GetTokenCount(), time.Now(), config)
	return candidate.HandoffWorthiness
}

// =============================================================================
// Configuration Management
// =============================================================================

// GetConfig returns a copy of the current configuration.
func (e *HandoffAwareEviction) GetConfig() *HandoffAwareEvictionConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.config.Clone()
}

// SetConfig updates the eviction configuration.
func (e *HandoffAwareEviction) SetConfig(config *HandoffAwareEvictionConfig) {
	if config == nil {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.config = config.Clone()

	// Update preserved type set
	e.preservedTypeSet = make(map[string]struct{})
	for _, t := range config.PreservedTypes {
		e.preservedTypeSet[t] = struct{}{}
	}
}

// SetGP updates the Gaussian Process.
func (e *HandoffAwareEviction) SetGP(gp *AgentGaussianProcess) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.gp = gp
}

// GetGP returns the current Gaussian Process.
func (e *HandoffAwareEviction) GetGP() *AgentGaussianProcess {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.gp
}

// =============================================================================
// Statistics
// =============================================================================

// EvictionStats contains statistics about the eviction strategy.
type EvictionStats struct {
	EvictionsPerformed int64         `json:"evictions_performed"`
	EntriesEvicted     int64         `json:"entries_evicted"`
	TokensFreed        int64         `json:"tokens_freed"`
	EntriesPreserved   int64         `json:"entries_preserved"`
	GPPredictions      int64         `json:"gp_predictions"`
	TotalDuration      time.Duration `json:"total_duration"`
	AverageDuration    time.Duration `json:"average_duration"`
}

// Stats returns statistics about the eviction strategy.
func (e *HandoffAwareEviction) Stats() EvictionStats {
	evictions := e.stats.evictionsPerformed.Load()
	totalDuration := time.Duration(e.stats.totalDurationNs.Load())

	averageDuration := time.Duration(0)
	if evictions > 0 {
		averageDuration = totalDuration / time.Duration(evictions)
	}

	return EvictionStats{
		EvictionsPerformed: evictions,
		EntriesEvicted:     e.stats.entriesEvicted.Load(),
		TokensFreed:        e.stats.tokensFreed.Load(),
		EntriesPreserved:   e.stats.entriesPreserved.Load(),
		GPPredictions:      e.stats.gpPredictions.Load(),
		TotalDuration:      totalDuration,
		AverageDuration:    averageDuration,
	}
}

// =============================================================================
// JSON Serialization
// =============================================================================

// evictionJSON is used for JSON marshaling.
type evictionJSON struct {
	Config *HandoffAwareEvictionConfig `json:"config"`
	Stats  EvictionStats               `json:"stats"`
}

// MarshalJSON implements json.Marshaler.
func (e *HandoffAwareEviction) MarshalJSON() ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return json.Marshal(evictionJSON{
		Config: e.config,
		Stats:  e.Stats(),
	})
}

// =============================================================================
// EvictionResult - Extended Result for Handoff-Aware Eviction
// =============================================================================

// HandoffAwareEvictionResult extends the base eviction result with handoff metrics.
type HandoffAwareEvictionResult struct {
	// EntriesEvicted is the number of entries evicted.
	EntriesEvicted int `json:"entries_evicted"`

	// TokensFreed is the number of tokens freed.
	TokensFreed int `json:"tokens_freed"`

	// EntriesPreserved is the number of entries preserved.
	EntriesPreserved int `json:"entries_preserved"`

	// AverageHandoffWorthiness is the average handoff worthiness of evicted entries.
	AverageHandoffWorthiness float64 `json:"average_handoff_worthiness"`

	// AverageEvictionScore is the average eviction score of evicted entries.
	AverageEvictionScore float64 `json:"average_eviction_score"`

	// Duration is how long the eviction took.
	Duration time.Duration `json:"duration"`

	// EvictedCandidates contains details about evicted entries.
	EvictedCandidates []*EvictionCandidate `json:"evicted_candidates,omitempty"`
}

// SelectForEvictionWithDetails performs eviction and returns detailed results.
func (e *HandoffAwareEviction) SelectForEvictionWithDetails(
	ctx context.Context,
	entries []EvictableEntry,
	targetTokens int,
) (*HandoffAwareEvictionResult, error) {
	startTime := time.Now()

	e.mu.RLock()
	config := e.config.Clone()
	e.mu.RUnlock()

	result := &HandoffAwareEvictionResult{}

	if len(entries) == 0 || targetTokens <= 0 {
		result.Duration = time.Since(startTime)
		return result, nil
	}

	// Filter preserved entries
	candidates := e.filterPreservedEntries(entries, config)
	result.EntriesPreserved = len(entries) - len(candidates)

	// Score candidates
	currentTurn := e.findMaxTurn(entries)
	scoredCandidates := e.scoreCandidates(candidates, currentTurn, config)

	// Sort by eviction score
	sort.Slice(scoredCandidates, func(i, j int) bool {
		return scoredCandidates[i].EvictionScore > scoredCandidates[j].EvictionScore
	})

	// Select entries
	var evictedCandidates []*EvictionCandidate
	freedTokens := 0
	totalWorthiness := 0.0
	totalScore := 0.0

	for _, candidate := range scoredCandidates {
		if freedTokens >= targetTokens {
			break
		}

		evictedCandidates = append(evictedCandidates, candidate)
		freedTokens += candidate.Entry.GetTokenCount()
		totalWorthiness += candidate.HandoffWorthiness
		totalScore += candidate.EvictionScore
	}

	result.EntriesEvicted = len(evictedCandidates)
	result.TokensFreed = freedTokens
	result.EvictedCandidates = evictedCandidates
	result.Duration = time.Since(startTime)

	if result.EntriesEvicted > 0 {
		result.AverageHandoffWorthiness = totalWorthiness / float64(result.EntriesEvicted)
		result.AverageEvictionScore = totalScore / float64(result.EntriesEvicted)
	}

	return result, nil
}
