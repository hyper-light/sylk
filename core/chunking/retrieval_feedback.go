package chunking

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// CK.4.2 ChunkRetriever Feedback Hook
// =============================================================================

// RetrievalContext provides context about how a chunk was retrieved and used.
type RetrievalContext struct {
	// QueryText is the original query that triggered the retrieval.
	QueryText string `json:"query_text"`

	// RetrievalScore is the similarity/relevance score from the retrieval.
	RetrievalScore float64 `json:"retrieval_score"`

	// RankPosition is the position of this chunk in the retrieval results.
	RankPosition int `json:"rank_position"`

	// TotalRetrieved is the total number of chunks retrieved for this query.
	TotalRetrieved int `json:"total_retrieved"`

	// ResponseGenerated indicates if a response was generated using this chunk.
	ResponseGenerated bool `json:"response_generated"`

	// Timestamp when the retrieval occurred.
	Timestamp time.Time `json:"timestamp"`

	// Metadata holds additional context about the retrieval.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// RetrievalFeedbackHook defines the interface for recording chunk retrieval feedback.
// Implementations should integrate with ChunkConfigLearner to update learned parameters.
type RetrievalFeedbackHook interface {
	// RecordRetrieval records feedback about a retrieved chunk.
	// chunkID identifies the chunk that was retrieved.
	// wasUseful indicates whether the chunk was useful for the task.
	// ctx provides additional context about the retrieval.
	RecordRetrieval(chunkID string, wasUseful bool, ctx RetrievalContext) error

	// GetHitRate returns the hit rate (useful/total) for a specific chunk.
	GetHitRate(chunkID string) (hitRate float64, totalRetrievals int)

	// GetDomainHitRate returns the aggregate hit rate for a domain.
	GetDomainHitRate(domain Domain) (hitRate float64, totalRetrievals int)
}

// ChunkStats tracks retrieval statistics for a single chunk.
type ChunkStats struct {
	// ChunkID is the unique identifier for this chunk.
	ChunkID string `json:"chunk_id"`

	// Domain is the content domain of this chunk.
	Domain Domain `json:"domain"`

	// TokenCount is the number of tokens in this chunk.
	TokenCount int `json:"token_count"`

	// ContextBefore is the context size before the chunk.
	ContextBefore int `json:"context_before"`

	// ContextAfter is the context size after the chunk.
	ContextAfter int `json:"context_after"`

	// TotalRetrievals is the total number of times this chunk was retrieved.
	TotalRetrievals int64 `json:"total_retrievals"`

	// UsefulRetrievals is the number of times this chunk was useful.
	UsefulRetrievals int64 `json:"useful_retrievals"`

	// LastRetrieved is the timestamp of the last retrieval.
	LastRetrieved time.Time `json:"last_retrieved"`

	// CreatedAt is when this chunk was first tracked.
	CreatedAt time.Time `json:"created_at"`
}

// HitRate calculates the hit rate for this chunk.
func (cs *ChunkStats) HitRate() float64 {
	total := atomic.LoadInt64(&cs.TotalRetrievals)
	if total == 0 {
		return 0.0
	}
	useful := atomic.LoadInt64(&cs.UsefulRetrievals)
	return float64(useful) / float64(total)
}

// AsyncRetrievalFeedbackHook provides a non-blocking implementation of RetrievalFeedbackHook.
// It uses a background goroutine to process feedback asynchronously, ensuring that
// feedback recording does not block the main retrieval path.
type AsyncRetrievalFeedbackHook struct {
	mu sync.RWMutex

	// learner is the ChunkConfigLearner to update with observations.
	learner *ChunkConfigLearner

	// chunkStats tracks per-chunk retrieval statistics.
	chunkStats map[string]*ChunkStats

	// domainStats tracks aggregate statistics per domain.
	domainStats map[Domain]*domainAggregateStats

	// feedbackChan receives feedback to process asynchronously.
	feedbackChan chan feedbackEntry

	// sessionID identifies the current session.
	sessionID string

	// bufferSize is the size of the async feedback buffer.
	bufferSize int

	// ctx is the context for the background worker.
	ctx context.Context

	// cancel cancels the background worker.
	cancel context.CancelFunc

	// wg tracks the background worker goroutine.
	wg sync.WaitGroup

	// stopped indicates if the hook has been stopped.
	stopped atomic.Bool

	// droppedCount tracks how many feedback entries were dropped due to full buffer.
	droppedCount atomic.Int64
}

// feedbackEntry represents a single feedback entry to be processed.
type feedbackEntry struct {
	chunkID   string
	statsID   string // Reference by ID, not pointer - avoids race condition
	wasUseful bool
	context   RetrievalContext
	timestamp time.Time
}

// domainAggregateStats holds aggregate statistics for a domain.
type domainAggregateStats struct {
	totalRetrievals  int64
	usefulRetrievals int64
}

// AsyncFeedbackConfig holds configuration for AsyncRetrievalFeedbackHook.
type AsyncFeedbackConfig struct {
	// Learner is the ChunkConfigLearner to update (required).
	Learner *ChunkConfigLearner

	// SessionID identifies the current session (optional).
	SessionID string

	// BufferSize is the size of the async feedback buffer (default: 1000).
	BufferSize int

	// Context is the parent context (optional, defaults to background).
	Context context.Context
}

// NewAsyncRetrievalFeedbackHook creates a new AsyncRetrievalFeedbackHook.
func NewAsyncRetrievalFeedbackHook(config AsyncFeedbackConfig) (*AsyncRetrievalFeedbackHook, error) {
	if config.Learner == nil {
		return nil, fmt.Errorf("learner is required")
	}

	bufferSize := config.BufferSize
	if bufferSize <= 0 {
		bufferSize = 1000
	}

	sessionID := config.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("session_%d", time.Now().UnixNano())
	}

	parentCtx := config.Context
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(parentCtx)

	hook := &AsyncRetrievalFeedbackHook{
		learner:      config.Learner,
		chunkStats:   make(map[string]*ChunkStats),
		domainStats:  make(map[Domain]*domainAggregateStats),
		feedbackChan: make(chan feedbackEntry, bufferSize),
		sessionID:    sessionID,
		bufferSize:   bufferSize,
		ctx:          ctx,
		cancel:       cancel,
	}

	// Start background worker
	hook.wg.Add(1)
	go hook.processLoop()

	return hook, nil
}

// RegisterChunk registers a chunk for tracking. This should be called when
// a chunk is created to provide metadata for feedback recording.
func (h *AsyncRetrievalFeedbackHook) RegisterChunk(chunk Chunk) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.chunkStats[chunk.ID]; !exists {
		h.chunkStats[chunk.ID] = &ChunkStats{
			ChunkID:       chunk.ID,
			Domain:        chunk.Domain,
			TokenCount:    chunk.TokenCount,
			ContextBefore: len(chunk.ContextBefore),
			ContextAfter:  len(chunk.ContextAfter),
			CreatedAt:     time.Now(),
		}
	}
}

// ensureChunkStats ensures chunk stats exist and returns the chunk ID for reference.
// This avoids passing pointers across goroutine boundaries which would cause races.
func (h *AsyncRetrievalFeedbackHook) ensureChunkStats(chunkID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.chunkStats[chunkID]; !exists {
		h.chunkStats[chunkID] = &ChunkStats{
			ChunkID:   chunkID,
			Domain:    DomainGeneral, // Default domain for unknown chunks
			CreatedAt: time.Now(),
		}
	}
	return chunkID // Return the ID for reference, not the pointer
}

// RecordRetrieval records feedback about a retrieved chunk asynchronously.
// This method is non-blocking - if the buffer is full, the feedback is dropped.
func (h *AsyncRetrievalFeedbackHook) RecordRetrieval(chunkID string, wasUseful bool, ctx RetrievalContext) error {
	if h.stopped.Load() {
		return fmt.Errorf("feedback hook has been stopped")
	}

	// Ensure chunk stats exist and get the statsID for reference
	statsID := h.ensureChunkStats(chunkID)

	entry := feedbackEntry{
		chunkID:   chunkID,
		statsID:   statsID, // Reference by ID, not pointer
		wasUseful: wasUseful,
		context:   ctx,
		timestamp: time.Now(),
	}

	// Non-blocking send
	select {
	case h.feedbackChan <- entry:
		return nil
	default:
		// Buffer full, drop the feedback
		h.droppedCount.Add(1)
		return fmt.Errorf("feedback buffer full, entry dropped")
	}
}

// RecordRetrievalSync records feedback synchronously, blocking until processed.
// Use this when you need to ensure feedback is recorded before continuing.
func (h *AsyncRetrievalFeedbackHook) RecordRetrievalSync(chunkID string, wasUseful bool, ctx RetrievalContext) error {
	if h.stopped.Load() {
		return fmt.Errorf("feedback hook has been stopped")
	}

	// Ensure chunk stats exist and get the statsID for reference
	statsID := h.ensureChunkStats(chunkID)

	entry := feedbackEntry{
		chunkID:   chunkID,
		statsID:   statsID, // Reference by ID, not pointer
		wasUseful: wasUseful,
		context:   ctx,
		timestamp: time.Now(),
	}

	// Process immediately
	return h.processEntry(entry)
}

// processLoop is the background worker that processes feedback entries.
func (h *AsyncRetrievalFeedbackHook) processLoop() {
	defer h.wg.Done()

	for {
		select {
		case <-h.ctx.Done():
			// Drain remaining entries before exiting
			h.drainRemaining()
			return
		case entry := <-h.feedbackChan:
			_ = h.processEntry(entry)
		}
	}
}

// drainRemaining processes any remaining entries in the channel.
func (h *AsyncRetrievalFeedbackHook) drainRemaining() {
	for {
		select {
		case entry := <-h.feedbackChan:
			_ = h.processEntry(entry)
		default:
			return
		}
	}
}

// processEntry processes a single feedback entry.
// All stats access is done under lock to avoid races.
func (h *AsyncRetrievalFeedbackHook) processEntry(entry feedbackEntry) error {
	// Look up stats by ID under lock - avoids race condition
	obs, err := h.updateStatsAndBuildObservation(entry)
	if err != nil {
		return err
	}

	// Record observation to learner (outside lock to avoid holding it too long)
	if err := h.learner.RecordObservation(obs); err != nil {
		return fmt.Errorf("failed to record observation: %w", err)
	}

	return nil
}

// updateStatsAndBuildObservation updates stats under lock and builds the observation.
// Returns the observation for the learner.
func (h *AsyncRetrievalFeedbackHook) updateStatsAndBuildObservation(entry feedbackEntry) (ChunkUsageObservation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Look up stats by ID under lock
	stats, exists := h.chunkStats[entry.statsID]
	if !exists {
		return ChunkUsageObservation{}, fmt.Errorf("stats not found for chunk: %s", entry.statsID)
	}

	// Update chunk stats (now safe under lock)
	stats.TotalRetrievals++
	if entry.wasUseful {
		stats.UsefulRetrievals++
	}

	// Update last retrieved timestamp
	stats.LastRetrieved = entry.context.Timestamp
	if stats.LastRetrieved.IsZero() {
		stats.LastRetrieved = entry.timestamp
	}

	// Update domain aggregate stats
	domainStat, domainExists := h.domainStats[stats.Domain]
	if !domainExists {
		domainStat = &domainAggregateStats{}
		h.domainStats[stats.Domain] = domainStat
	}
	domainStat.totalRetrievals++
	if entry.wasUseful {
		domainStat.usefulRetrievals++
	}

	// Build observation from stats (copy values while under lock)
	obs := ChunkUsageObservation{
		Domain:        stats.Domain,
		TokenCount:    stats.TokenCount,
		ContextBefore: stats.ContextBefore,
		ContextAfter:  stats.ContextAfter,
		WasUseful:     entry.wasUseful,
		Timestamp:     entry.context.Timestamp,
		ChunkID:       entry.chunkID,
		SessionID:     h.sessionID,
	}

	// Set timestamp if not provided
	if obs.Timestamp.IsZero() {
		obs.Timestamp = entry.timestamp
	}

	// Ensure token count is valid for learner
	if obs.TokenCount <= 0 {
		obs.TokenCount = 100 // Default fallback
	}

	return obs, nil
}

// GetHitRate returns the hit rate for a specific chunk.
func (h *AsyncRetrievalFeedbackHook) GetHitRate(chunkID string) (hitRate float64, totalRetrievals int) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	stats, exists := h.chunkStats[chunkID]
	if !exists {
		return 0.0, 0
	}

	total := stats.TotalRetrievals
	if total == 0 {
		return 0.0, 0
	}
	return float64(stats.UsefulRetrievals) / float64(total), int(total)
}

// GetDomainHitRate returns the aggregate hit rate for a domain.
func (h *AsyncRetrievalFeedbackHook) GetDomainHitRate(domain Domain) (hitRate float64, totalRetrievals int) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	domainStat, exists := h.domainStats[domain]
	if !exists {
		return 0.0, 0
	}

	total := domainStat.totalRetrievals
	if total == 0 {
		return 0.0, 0
	}

	return float64(domainStat.usefulRetrievals) / float64(total), int(total)
}

// GetChunkStats returns the statistics for a specific chunk.
func (h *AsyncRetrievalFeedbackHook) GetChunkStats(chunkID string) (*ChunkStats, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	stats, exists := h.chunkStats[chunkID]
	if !exists {
		return nil, false
	}
	// Return a copy to avoid data races
	statsCopy := *stats
	return &statsCopy, true
}

// GetAllChunkStats returns statistics for all tracked chunks.
func (h *AsyncRetrievalFeedbackHook) GetAllChunkStats() map[string]*ChunkStats {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make(map[string]*ChunkStats, len(h.chunkStats))
	for id, stats := range h.chunkStats {
		statsCopy := *stats
		result[id] = &statsCopy
	}
	return result
}

// GetDroppedCount returns the number of feedback entries dropped due to full buffer.
func (h *AsyncRetrievalFeedbackHook) GetDroppedCount() int64 {
	return h.droppedCount.Load()
}

// SetSessionID updates the session ID for new observations.
func (h *AsyncRetrievalFeedbackHook) SetSessionID(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionID = sessionID
}

// Stop stops the background worker and waits for it to finish.
// Any remaining entries in the buffer will be processed before returning.
func (h *AsyncRetrievalFeedbackHook) Stop() {
	if h.stopped.Swap(true) {
		return // Already stopped
	}
	h.cancel()
	h.wg.Wait()
}

// =============================================================================
// Batch Feedback Recording
// =============================================================================

// BatchFeedback represents a batch of feedback entries to record.
type BatchFeedback struct {
	// Entries contains the individual feedback items.
	Entries []BatchFeedbackEntry `json:"entries"`
}

// BatchFeedbackEntry represents a single entry in a batch.
type BatchFeedbackEntry struct {
	// ChunkID identifies the chunk.
	ChunkID string `json:"chunk_id"`

	// WasUseful indicates if the chunk was useful.
	WasUseful bool `json:"was_useful"`

	// Context provides retrieval context.
	Context RetrievalContext `json:"context"`
}

// RecordBatch records multiple feedback entries at once.
// This is more efficient than recording entries one at a time.
func (h *AsyncRetrievalFeedbackHook) RecordBatch(batch BatchFeedback) (recorded int, dropped int) {
	for _, entry := range batch.Entries {
		err := h.RecordRetrieval(entry.ChunkID, entry.WasUseful, entry.Context)
		if err != nil {
			dropped++
		} else {
			recorded++
		}
	}
	return recorded, dropped
}

// =============================================================================
// Synchronous Feedback Hook (for testing)
// =============================================================================

// SyncRetrievalFeedbackHook provides a synchronous implementation of RetrievalFeedbackHook.
// This is primarily useful for testing where immediate feedback processing is needed.
type SyncRetrievalFeedbackHook struct {
	mu sync.RWMutex

	// learner is the ChunkConfigLearner to update with observations.
	learner *ChunkConfigLearner

	// chunkStats tracks per-chunk retrieval statistics.
	chunkStats map[string]*ChunkStats

	// domainStats tracks aggregate statistics per domain.
	domainStats map[Domain]*domainAggregateStats

	// sessionID identifies the current session.
	sessionID string
}

// NewSyncRetrievalFeedbackHook creates a new synchronous feedback hook.
func NewSyncRetrievalFeedbackHook(learner *ChunkConfigLearner, sessionID string) (*SyncRetrievalFeedbackHook, error) {
	if learner == nil {
		return nil, fmt.Errorf("learner is required")
	}

	if sessionID == "" {
		sessionID = fmt.Sprintf("session_%d", time.Now().UnixNano())
	}

	return &SyncRetrievalFeedbackHook{
		learner:     learner,
		chunkStats:  make(map[string]*ChunkStats),
		domainStats: make(map[Domain]*domainAggregateStats),
		sessionID:   sessionID,
	}, nil
}

// RegisterChunk registers a chunk for tracking.
func (h *SyncRetrievalFeedbackHook) RegisterChunk(chunk Chunk) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.chunkStats[chunk.ID]; !exists {
		h.chunkStats[chunk.ID] = &ChunkStats{
			ChunkID:       chunk.ID,
			Domain:        chunk.Domain,
			TokenCount:    chunk.TokenCount,
			ContextBefore: len(chunk.ContextBefore),
			ContextAfter:  len(chunk.ContextAfter),
			CreatedAt:     time.Now(),
		}
	}
}

// RecordRetrieval records feedback synchronously.
func (h *SyncRetrievalFeedbackHook) RecordRetrieval(chunkID string, wasUseful bool, ctx RetrievalContext) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Get or create chunk stats
	stats, exists := h.chunkStats[chunkID]
	if !exists {
		stats = &ChunkStats{
			ChunkID:   chunkID,
			Domain:    DomainGeneral,
			CreatedAt: time.Now(),
		}
		h.chunkStats[chunkID] = stats
	}

	// Update stats
	stats.TotalRetrievals++
	if wasUseful {
		stats.UsefulRetrievals++
	}
	stats.LastRetrieved = ctx.Timestamp
	if stats.LastRetrieved.IsZero() {
		stats.LastRetrieved = time.Now()
	}

	// Update domain stats
	domainStat, exists := h.domainStats[stats.Domain]
	if !exists {
		domainStat = &domainAggregateStats{}
		h.domainStats[stats.Domain] = domainStat
	}
	domainStat.totalRetrievals++
	if wasUseful {
		domainStat.usefulRetrievals++
	}

	// Create observation
	obs := ChunkUsageObservation{
		Domain:        stats.Domain,
		TokenCount:    stats.TokenCount,
		ContextBefore: stats.ContextBefore,
		ContextAfter:  stats.ContextAfter,
		WasUseful:     wasUseful,
		Timestamp:     ctx.Timestamp,
		ChunkID:       chunkID,
		SessionID:     h.sessionID,
	}

	if obs.Timestamp.IsZero() {
		obs.Timestamp = time.Now()
	}
	if obs.TokenCount <= 0 {
		obs.TokenCount = 100
	}

	return h.learner.RecordObservation(obs)
}

// GetHitRate returns the hit rate for a specific chunk.
func (h *SyncRetrievalFeedbackHook) GetHitRate(chunkID string) (hitRate float64, totalRetrievals int) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	stats, exists := h.chunkStats[chunkID]
	if !exists {
		return 0.0, 0
	}

	if stats.TotalRetrievals == 0 {
		return 0.0, 0
	}
	return float64(stats.UsefulRetrievals) / float64(stats.TotalRetrievals), int(stats.TotalRetrievals)
}

// GetDomainHitRate returns the aggregate hit rate for a domain.
func (h *SyncRetrievalFeedbackHook) GetDomainHitRate(domain Domain) (hitRate float64, totalRetrievals int) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	domainStat, exists := h.domainStats[domain]
	if !exists {
		return 0.0, 0
	}

	if domainStat.totalRetrievals == 0 {
		return 0.0, 0
	}
	return float64(domainStat.usefulRetrievals) / float64(domainStat.totalRetrievals), int(domainStat.totalRetrievals)
}
