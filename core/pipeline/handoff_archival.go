// Package pipeline provides pipeline agent handoff state archival to dual storage.
// PH.6: HandoffArchiver handles archival to both Bleve (Document DB) and VectorDB
// for audit trail, pattern analysis, and semantic similarity search.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// BleveStore is the interface for document search storage.
type BleveStore interface {
	Index(ctx context.Context, id string, doc any) error
	Search(ctx context.Context, query string, limit int) ([]string, error)
	Delete(ctx context.Context, id string) error
}

// VectorStore is the interface for vector storage.
type VectorStore interface {
	Insert(ctx context.Context, id string, embedding []float32, metadata map[string]any) error
	Search(ctx context.Context, embedding []float32, limit int, filter map[string]any) ([]VectorResult, error)
	Delete(ctx context.Context, id string) error
}

// VectorResult from vector search.
type VectorResult struct {
	ID       string
	Score    float32
	Metadata map[string]any
}

// ArchivalEmbedder generates embeddings for semantic search.
type ArchivalEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// ArchivableHandoffState represents the state transferred during agent handoff.
// This interface allows different agent types to provide their handoff state for archival.
type ArchivableHandoffState interface {
	// GetAgentID returns the agent identifier.
	GetAgentID() string
	// GetAgentType returns the agent type (engineer, designer, etc).
	GetAgentType() string
	// GetSessionID returns the session identifier.
	GetSessionID() string
	// GetPipelineID returns the pipeline identifier (optional).
	GetPipelineID() string
	// GetTriggerReason returns why the handoff was triggered.
	GetTriggerReason() string
	// GetTriggerContext returns the context value that triggered handoff.
	GetTriggerContext() float64
	// GetHandoffIndex returns the handoff sequence number.
	GetHandoffIndex() int
	// GetStartedAt returns when the agent started.
	GetStartedAt() time.Time
	// GetCompletedAt returns when handoff occurred.
	GetCompletedAt() time.Time
	// ToJSON serializes the full state to JSON.
	ToJSON() ([]byte, error)
	// GetSummary returns a text summary for embedding.
	GetSummary() string
}

// ArchiverConfig contains configuration for the archiver.
type ArchiverConfig struct {
	AsyncQueueSize int           // Buffer size for async queue
	WorkerCount    int           // Number of async workers
	EmbeddingDim   int           // Embedding dimension
	MaxRetries     int           // Max retries for failed archives
	RetryBackoff   time.Duration // Backoff between retries
}

// DefaultArchiverConfig returns sensible default configuration.
func DefaultArchiverConfig() ArchiverConfig {
	return ArchiverConfig{
		AsyncQueueSize: 100,
		WorkerCount:    2,
		EmbeddingDim:   1536,
		MaxRetries:     3,
		RetryBackoff:   time.Second,
	}
}

// ArchiveQuery for querying archived handoff states.
type ArchiveQuery struct {
	AgentID       string
	AgentType     string
	SessionID     string
	PipelineID    string
	FromTime      time.Time
	ToTime        time.Time
	TriggerReason string
	Limit         int
	SimilarTo     string // For semantic search - query text
}

// HandoffArchiveEntry is the stored format for archived handoff states.
type HandoffArchiveEntry struct {
	ID             string        `json:"id"`
	AgentID        string        `json:"agent_id"`
	AgentType      string        `json:"agent_type"`
	SessionID      string        `json:"session_id"`
	PipelineID     string        `json:"pipeline_id"`
	HandoffIndex   int           `json:"handoff_index"`
	TriggerReason  string        `json:"trigger_reason"`
	TriggerContext float64       `json:"trigger_context"`
	ArchivedAt     time.Time     `json:"archived_at"`
	StartedAt      time.Time     `json:"started_at"`
	CompletedAt    time.Time     `json:"completed_at"`
	Duration       time.Duration `json:"duration"`
	Status         string        `json:"status"`
	StateJSON      string        `json:"state_json"` // Serialized state
	Category       string        `json:"category"`   // agent_handoff_{agent_type}
	Summary        string        `json:"summary"`    // For vector embedding
}

// archiveJob represents an async archive operation.
type archiveJob struct {
	ctx     context.Context
	state   ArchivableHandoffState
	retries int
}

// HandoffArchiver handles dual-storage (Bleve + VectorDB) archival of handoff states.
type HandoffArchiver struct {
	mu         sync.RWMutex
	bleveStore BleveStore
	vectorDB   VectorStore
	embedder   ArchivalEmbedder
	asyncQueue chan *archiveJob
	wg         sync.WaitGroup
	closed     atomic.Bool
	config     ArchiverConfig
}

// NewHandoffArchiver creates a new HandoffArchiver with the given dependencies.
func NewHandoffArchiver(
	config ArchiverConfig,
	bleve BleveStore,
	vector VectorStore,
	embedder ArchivalEmbedder,
) *HandoffArchiver {
	if config.AsyncQueueSize == 0 {
		config = DefaultArchiverConfig()
	}

	return &HandoffArchiver{
		bleveStore: bleve,
		vectorDB:   vector,
		embedder:   embedder,
		asyncQueue: make(chan *archiveJob, config.AsyncQueueSize),
		config:     config,
	}
}

// Start begins the async worker goroutines.
func (a *HandoffArchiver) Start() {
	for i := 0; i < a.config.WorkerCount; i++ {
		a.wg.Add(1)
		go a.worker()
	}
}

// worker processes archive jobs from the async queue.
func (a *HandoffArchiver) worker() {
	defer a.wg.Done()

	for job := range a.asyncQueue {
		a.processJob(job)
	}
}

// processJob handles a single archive job with retry logic.
func (a *HandoffArchiver) processJob(job *archiveJob) {
	err := a.ArchiveSync(job.ctx, job.state)
	if err == nil {
		return
	}

	if job.retries < a.config.MaxRetries {
		a.retryJob(job)
	}
}

// retryJob requeues a failed job with backoff.
func (a *HandoffArchiver) retryJob(job *archiveJob) {
	job.retries++
	time.Sleep(a.config.RetryBackoff * time.Duration(job.retries))

	if !a.closed.Load() {
		select {
		case a.asyncQueue <- job:
		default:
			// Queue full, drop the job
		}
	}
}

// Archive submits a handoff state for async archival (non-blocking).
func (a *HandoffArchiver) Archive(ctx context.Context, state ArchivableHandoffState) error {
	if a.closed.Load() {
		return fmt.Errorf("archiver is closed")
	}

	job := &archiveJob{
		ctx:   ctx,
		state: state,
	}

	select {
	case a.asyncQueue <- job:
		return nil
	default:
		return fmt.Errorf("archive queue full")
	}
}

// ArchiveSync performs synchronous dual-storage archival.
func (a *HandoffArchiver) ArchiveSync(ctx context.Context, state ArchivableHandoffState) error {
	entry, err := a.buildEntry(state)
	if err != nil {
		return fmt.Errorf("failed to build entry: %w", err)
	}

	if err := a.indexToBleve(ctx, entry); err != nil {
		return fmt.Errorf("bleve indexing failed: %w", err)
	}

	if err := a.indexToVector(ctx, entry); err != nil {
		return fmt.Errorf("vector indexing failed: %w", err)
	}

	return nil
}

// buildEntry creates a HandoffArchiveEntry from an ArchivableHandoffState.
func (a *HandoffArchiver) buildEntry(state ArchivableHandoffState) (*HandoffArchiveEntry, error) {
	stateJSON, err := state.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize state: %w", err)
	}

	now := time.Now()
	return &HandoffArchiveEntry{
		ID:             generateArchiveID(state),
		AgentID:        state.GetAgentID(),
		AgentType:      state.GetAgentType(),
		SessionID:      state.GetSessionID(),
		PipelineID:     state.GetPipelineID(),
		HandoffIndex:   state.GetHandoffIndex(),
		TriggerReason:  state.GetTriggerReason(),
		TriggerContext: state.GetTriggerContext(),
		ArchivedAt:     now,
		StartedAt:      state.GetStartedAt(),
		CompletedAt:    state.GetCompletedAt(),
		Duration:       state.GetCompletedAt().Sub(state.GetStartedAt()),
		Status:         "archived",
		StateJSON:      string(stateJSON),
		Category:       buildCategory(state.GetAgentType()),
		Summary:        state.GetSummary(),
	}, nil
}

// indexToBleve indexes the entry in the document store.
func (a *HandoffArchiver) indexToBleve(ctx context.Context, entry *HandoffArchiveEntry) error {
	a.mu.RLock()
	store := a.bleveStore
	a.mu.RUnlock()

	if store == nil {
		return nil // Bleve not configured
	}

	return store.Index(ctx, entry.ID, entry)
}

// indexToVector generates embedding and stores in vector DB.
func (a *HandoffArchiver) indexToVector(ctx context.Context, entry *HandoffArchiveEntry) error {
	a.mu.RLock()
	vector := a.vectorDB
	embedder := a.embedder
	a.mu.RUnlock()

	if vector == nil || embedder == nil {
		return nil // Vector storage not configured
	}

	embedding, err := embedder.Embed(ctx, entry.Summary)
	if err != nil {
		return fmt.Errorf("embedding generation failed: %w", err)
	}

	metadata := buildVectorMetadata(entry)
	return vector.Insert(ctx, entry.ID, embedding, metadata)
}

// Query searches archived handoff states by criteria.
func (a *HandoffArchiver) Query(ctx context.Context, query ArchiveQuery) ([]*HandoffArchiveEntry, error) {
	a.mu.RLock()
	store := a.bleveStore
	a.mu.RUnlock()

	if store == nil {
		return nil, fmt.Errorf("bleve store not configured")
	}

	searchQuery := buildSearchQuery(query)
	limit := query.Limit
	if limit == 0 {
		limit = 100
	}

	ids, err := store.Search(ctx, searchQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	return a.loadEntries(ctx, ids)
}

// loadEntries loads archive entries by their IDs.
func (a *HandoffArchiver) loadEntries(_ context.Context, ids []string) ([]*HandoffArchiveEntry, error) {
	// In a real implementation, this would load from storage
	// For now, return empty slice as entries would be loaded from Bleve
	entries := make([]*HandoffArchiveEntry, 0, len(ids))
	return entries, nil
}

// QuerySimilar finds semantically similar handoff states.
func (a *HandoffArchiver) QuerySimilar(ctx context.Context, queryText string, limit int) ([]*HandoffArchiveEntry, error) {
	vector, embedder, err := a.getVectorDependencies()
	if err != nil {
		return nil, err
	}

	embedding, err := embedder.Embed(ctx, queryText)
	if err != nil {
		return nil, fmt.Errorf("embedding generation failed: %w", err)
	}

	results, err := vector.Search(ctx, embedding, normalizeLimit(limit), nil)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	return a.convertResults(results)
}

// getVectorDependencies returns vector store and embedder if configured.
func (a *HandoffArchiver) getVectorDependencies() (VectorStore, ArchivalEmbedder, error) {
	a.mu.RLock()
	vector := a.vectorDB
	embedder := a.embedder
	a.mu.RUnlock()

	if vector == nil || embedder == nil {
		return nil, nil, fmt.Errorf("vector search not configured")
	}
	return vector, embedder, nil
}

// normalizeLimit returns a default limit if the provided value is zero.
func normalizeLimit(limit int) int {
	if limit == 0 {
		return 10
	}
	return limit
}

// convertResults converts VectorResults to HandoffArchiveEntries.
func (a *HandoffArchiver) convertResults(results []VectorResult) ([]*HandoffArchiveEntry, error) {
	entries := make([]*HandoffArchiveEntry, 0, len(results))
	for _, r := range results {
		entry := entryFromMetadata(r.ID, r.Metadata)
		if entry != nil {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// Close performs graceful shutdown of the archiver.
func (a *HandoffArchiver) Close() error {
	if a.closed.Swap(true) {
		return nil // Already closed
	}

	close(a.asyncQueue)
	a.wg.Wait()
	return nil
}

// Helper functions

// generateArchiveID creates a unique ID for the archive entry.
func generateArchiveID(state ArchivableHandoffState) string {
	return fmt.Sprintf("handoff_%s_%s_%d_%d",
		state.GetAgentType(),
		state.GetSessionID(),
		state.GetHandoffIndex(),
		time.Now().UnixNano(),
	)
}

// buildCategory creates the category string for the agent type.
func buildCategory(agentType string) string {
	return fmt.Sprintf("agent_handoff_%s", agentType)
}

// buildVectorMetadata creates metadata for vector storage.
func buildVectorMetadata(entry *HandoffArchiveEntry) map[string]any {
	return map[string]any{
		"agent_id":        entry.AgentID,
		"agent_type":      entry.AgentType,
		"session_id":      entry.SessionID,
		"pipeline_id":     entry.PipelineID,
		"handoff_index":   entry.HandoffIndex,
		"trigger_reason":  entry.TriggerReason,
		"trigger_context": entry.TriggerContext,
		"archived_at":     entry.ArchivedAt.Format(time.RFC3339),
		"category":        entry.Category,
	}
}

// buildSearchQuery constructs a search query string from ArchiveQuery.
func buildSearchQuery(query ArchiveQuery) string {
	parts := collectQueryParts(query)

	if len(parts) == 0 {
		return "*"
	}

	return joinQueryParts(parts)
}

// collectQueryParts gathers non-empty query fields into field:value pairs.
func collectQueryParts(query ArchiveQuery) []string {
	fields := []struct {
		name  string
		value string
	}{
		{"agent_type", query.AgentType},
		{"session_id", query.SessionID},
		{"pipeline_id", query.PipelineID},
		{"trigger_reason", query.TriggerReason},
		{"agent_id", query.AgentID},
	}

	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		if f.value != "" {
			parts = append(parts, fmt.Sprintf("%s:%s", f.name, f.value))
		}
	}
	return parts
}

// joinQueryParts joins query parts with AND.
func joinQueryParts(parts []string) string {
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result = result + " AND " + parts[i]
	}
	return result
}

// entryFromMetadata reconstructs a partial entry from vector metadata.
func entryFromMetadata(id string, metadata map[string]any) *HandoffArchiveEntry {
	entry := &HandoffArchiveEntry{ID: id}
	populateStringFields(entry, metadata)
	populateNumericFields(entry, metadata)
	populateTimeFields(entry, metadata)
	return entry
}

// populateStringFields extracts string fields from metadata.
func populateStringFields(entry *HandoffArchiveEntry, metadata map[string]any) {
	entry.AgentID = getStringValue(metadata, "agent_id")
	entry.AgentType = getStringValue(metadata, "agent_type")
	entry.SessionID = getStringValue(metadata, "session_id")
	entry.PipelineID = getStringValue(metadata, "pipeline_id")
	entry.TriggerReason = getStringValue(metadata, "trigger_reason")
	entry.Category = getStringValue(metadata, "category")
}

// populateNumericFields extracts numeric fields from metadata.
func populateNumericFields(entry *HandoffArchiveEntry, metadata map[string]any) {
	entry.HandoffIndex = getIntValue(metadata, "handoff_index")
	entry.TriggerContext = getFloatValue(metadata, "trigger_context")
}

// populateTimeFields extracts time fields from metadata.
func populateTimeFields(entry *HandoffArchiveEntry, metadata map[string]any) {
	if v := getStringValue(metadata, "archived_at"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			entry.ArchivedAt = t
		}
	}
}

// getStringValue safely extracts a string from metadata.
func getStringValue(metadata map[string]any, key string) string {
	if v, ok := metadata[key].(string); ok {
		return v
	}
	return ""
}

// getIntValue safely extracts an int from metadata.
func getIntValue(metadata map[string]any, key string) int {
	if v, ok := metadata[key].(int); ok {
		return v
	}
	if v, ok := metadata[key].(float64); ok {
		return int(v)
	}
	return 0
}

// getFloatValue safely extracts a float64 from metadata.
func getFloatValue(metadata map[string]any, key string) float64 {
	if v, ok := metadata[key].(float64); ok {
		return v
	}
	return 0
}

// BaseArchivableState provides a basic implementation of ArchivableHandoffState.
type BaseArchivableState struct {
	AgentID        string          `json:"agent_id"`
	AgentType      string          `json:"agent_type"`
	SessionID      string          `json:"session_id"`
	PipelineID     string          `json:"pipeline_id"`
	TriggerReason  string          `json:"trigger_reason"`
	TriggerContext float64         `json:"trigger_context"`
	HandoffIndex   int             `json:"handoff_index"`
	StartedAt      time.Time       `json:"started_at"`
	CompletedAt    time.Time       `json:"completed_at"`
	Summary        string          `json:"summary"`
	Data           json.RawMessage `json:"data"`
}

// GetAgentID returns the agent identifier.
func (s *BaseArchivableState) GetAgentID() string { return s.AgentID }

// GetAgentType returns the agent type.
func (s *BaseArchivableState) GetAgentType() string { return s.AgentType }

// GetSessionID returns the session identifier.
func (s *BaseArchivableState) GetSessionID() string { return s.SessionID }

// GetPipelineID returns the pipeline identifier.
func (s *BaseArchivableState) GetPipelineID() string { return s.PipelineID }

// GetTriggerReason returns the handoff trigger reason.
func (s *BaseArchivableState) GetTriggerReason() string { return s.TriggerReason }

// GetTriggerContext returns the trigger context value.
func (s *BaseArchivableState) GetTriggerContext() float64 { return s.TriggerContext }

// GetHandoffIndex returns the handoff sequence number.
func (s *BaseArchivableState) GetHandoffIndex() int { return s.HandoffIndex }

// GetStartedAt returns when the agent started.
func (s *BaseArchivableState) GetStartedAt() time.Time { return s.StartedAt }

// GetCompletedAt returns when handoff occurred.
func (s *BaseArchivableState) GetCompletedAt() time.Time { return s.CompletedAt }

// GetSummary returns the text summary for embedding.
func (s *BaseArchivableState) GetSummary() string { return s.Summary }

// ToJSON serializes the state to JSON.
func (s *BaseArchivableState) ToJSON() ([]byte, error) {
	return json.Marshal(s)
}

// Ensure BaseArchivableState implements ArchivableHandoffState.
var _ ArchivableHandoffState = (*BaseArchivableState)(nil)
