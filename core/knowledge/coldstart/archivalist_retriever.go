package coldstart

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/knowledge/memory"
	"github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"
)

// =============================================================================
// AE.5.4 Interfaces for Testability
// =============================================================================

// MemoryStoreInterface abstracts memory store operations for testability.
type MemoryStoreInterface interface {
	GetMemory(ctx context.Context, nodeID string, domain domain.Domain) (*memory.ACTRMemory, error)
	RecordAccess(ctx context.Context, nodeID string, accessType memory.AccessType, context string) error
}

// BleveSearcherInterface abstracts Bleve full-text search for testability.
type BleveSearcherInterface interface {
	Search(ctx context.Context, query string, limit int) ([]*events.ActivityEvent, error)
}

// VectorSearcherInterface abstracts vector similarity search for testability.
type VectorSearcherInterface interface {
	Search(ctx context.Context, queryText string, limit int) ([]*events.ActivityEvent, error)
}

// Default configuration constants for ArchivalistRetriever.
const (
	// DefaultMinTracesForWarm is the minimum trace count to start blending warm scores.
	DefaultMinTracesForWarm = 3

	// DefaultFullWarmTraces is the trace count at which pure ACT-R scoring is used.
	DefaultFullWarmTraces = 15
)

// =============================================================================
// AE.5.7 Cross-Session State Types
// =============================================================================

// DefaultCrossSessionDecayDays is the default time constant for cross-session
// activation decay. Events from 7 days ago will have ~37% of their activation.
const DefaultCrossSessionDecayDays = 7.0

// SessionSummary contains summary information about a prior session
// for cross-session state loading.
type SessionSummary struct {
	SessionID     string
	ProjectID     string
	StartedAt     time.Time
	EndedAt       time.Time
	EventCount    int
	TopEventIDs   []string
	TopEventTypes map[events.EventType]int
}

// SessionSummaryLoader provides access to session summaries and events
// from persistent storage for cross-session state loading.
type SessionSummaryLoader interface {
	// LoadRecentSessions loads the most recent sessions for a project.
	// Sessions are ordered by end time descending, limited to the specified count.
	LoadRecentSessions(ctx context.Context, projectID string, limit int) ([]SessionSummary, error)

	// LoadSessionEvents loads specific events from a session by their IDs.
	LoadSessionEvents(ctx context.Context, sessionID string, eventIDs []string) ([]*events.ActivityEvent, error)
}

// InheritedTrace represents an ACT-R trace inherited from a prior session
// with cross-session decay applied.
type InheritedTrace struct {
	EventID          string
	EventType        events.EventType
	OriginalActivation float64
	DecayedActivation  float64
	SessionID        string
	OriginalTimestamp time.Time
}

// CrossSessionState stores loaded cross-session state for use in searches.
type CrossSessionState struct {
	ProjectID           string
	LoadedAt            time.Time
	DaysSinceLastSession float64
	DecayFactor         float64
	Sessions            []SessionSummary
	InheritedTraces     []InheritedTrace
	TopEventTypes       map[events.EventType]int
}

// =============================================================================
// AE.5.4 ArchivalistRetriever Type
// =============================================================================

// ArchivalistRetriever combines cold-start priors with ACT-R memory and
// cross-session state loading for adaptive event retrieval. It blends cold
// scores (based on action type, recency, and semantic similarity) with warm
// ACT-R activation scores as more user behavior data is collected.
type ArchivalistRetriever struct {
	// Core dependencies (AE.5.4)
	actionPrior         *ActionTypePrior
	memoryStoreIntf     MemoryStoreInterface
	bleveSearcher       BleveSearcherInterface
	vectorSearcher      VectorSearcherInterface

	// Legacy memory store for backward compatibility
	memoryStore           *memory.MemoryStore

	// Cross-session dependencies
	sessionLoader         SessionSummaryLoader
	crossSessionDecayDays float64
	crossSessionState     *CrossSessionState

	// Scoring configuration (AE.5.4)
	minTracesForWarm int
	fullWarmTraces   int

	mu sync.RWMutex
}

// RetrieverOption configures an ArchivalistRetriever.
type RetrieverOption func(*ArchivalistRetriever)

// WithMinTracesForWarm sets the minimum trace count to start warm blending.
func WithMinTracesForWarm(n int) RetrieverOption {
	return func(r *ArchivalistRetriever) {
		if n > 0 {
			r.minTracesForWarm = n
		}
	}
}

// WithFullWarmTraces sets the trace count for full ACT-R scoring.
func WithFullWarmTraces(n int) RetrieverOption {
	return func(r *ArchivalistRetriever) {
		if n > 0 {
			r.fullWarmTraces = n
		}
	}
}

// WithCrossSessionDecayDaysOpt sets the cross-session decay half-life via option.
func WithCrossSessionDecayDaysOpt(days float64) RetrieverOption {
	return func(r *ArchivalistRetriever) {
		if days > 0 {
			r.crossSessionDecayDays = days
		}
	}
}

// WithColdPrior sets a custom ActionTypePrior.
func WithColdPrior(prior *ActionTypePrior) RetrieverOption {
	return func(r *ArchivalistRetriever) {
		if prior != nil {
			r.actionPrior = prior
		}
	}
}

// WithBleveSearcher sets the Bleve searcher dependency.
func WithBleveSearcher(searcher BleveSearcherInterface) RetrieverOption {
	return func(r *ArchivalistRetriever) {
		r.bleveSearcher = searcher
	}
}

// WithVectorSearcher sets the vector searcher dependency.
func WithVectorSearcher(searcher VectorSearcherInterface) RetrieverOption {
	return func(r *ArchivalistRetriever) {
		r.vectorSearcher = searcher
	}
}

// WithMemoryStoreInterface sets the memory store dependency using the interface.
func WithMemoryStoreInterface(store MemoryStoreInterface) RetrieverOption {
	return func(r *ArchivalistRetriever) {
		r.memoryStoreIntf = store
	}
}

// NewArchivalistRetriever creates a new ArchivalistRetriever with the given
// dependencies. This is the primary constructor supporting AE.5.4-5.8.
func NewArchivalistRetriever(
	actionPrior *ActionTypePrior,
	memoryStore *memory.MemoryStore,
	sessionLoader SessionSummaryLoader,
	opts ...RetrieverOption,
) *ArchivalistRetriever {
	if actionPrior == nil {
		actionPrior = DefaultActionTypePrior()
	}

	r := &ArchivalistRetriever{
		actionPrior:           actionPrior,
		memoryStore:           memoryStore,
		sessionLoader:         sessionLoader,
		crossSessionDecayDays: DefaultCrossSessionDecayDays,
		minTracesForWarm:      DefaultMinTracesForWarm,
		fullWarmTraces:        DefaultFullWarmTraces,
	}

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// NewArchivalistRetrieverWithInterfaces creates a retriever using interface-based
// dependencies for full testability (AE.5.4).
func NewArchivalistRetrieverWithInterfaces(
	memoryStore MemoryStoreInterface,
	bleveSearcher BleveSearcherInterface,
	vectorSearcher VectorSearcherInterface,
	opts ...RetrieverOption,
) *ArchivalistRetriever {
	r := &ArchivalistRetriever{
		actionPrior:           DefaultActionTypePrior(),
		memoryStoreIntf:       memoryStore,
		bleveSearcher:         bleveSearcher,
		vectorSearcher:        vectorSearcher,
		crossSessionDecayDays: DefaultCrossSessionDecayDays,
		minTracesForWarm:      DefaultMinTracesForWarm,
		fullWarmTraces:        DefaultFullWarmTraces,
	}

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// =============================================================================
// AE.5.5 Search Method and Types
// =============================================================================

// SearchOptions configures a search operation.
type SearchOptions struct {
	// Limit is the maximum number of results to return.
	Limit int

	// MinScore filters results below this score threshold.
	MinScore float64

	// EventTypes filters results to specific event types.
	EventTypes []events.EventType

	// SessionID is the current session ID for recency calculations.
	SessionID string
}

// DefaultSearchOptions returns search options with sensible defaults.
func DefaultSearchOptions() *SearchOptions {
	return &SearchOptions{
		Limit:      20,
		MinScore:   0.0,
		EventTypes: nil,
		SessionID:  "",
	}
}

// RankedActivityEvent wraps an ActivityEvent with scoring information.
type RankedActivityEvent struct {
	// Event is the underlying activity event.
	Event *events.ActivityEvent

	// Score is the final blended score.
	Score float64

	// ColdScore is the cold-start score component.
	ColdScore float64

	// WarmScore is the ACT-R activation score component.
	WarmScore float64

	// BlendRatio is the ratio of warm to cold scoring (0=pure cold, 1=pure warm).
	BlendRatio float64

	// Explanation describes how the score was computed.
	Explanation string
}

// Search performs a hybrid search combining Bleve and vector search results.
// Results are scored using a blend of cold-start priors and ACT-R activation.
func (ar *ArchivalistRetriever) Search(
	ctx context.Context,
	query string,
	queryEmbed []float32,
	opts *SearchOptions,
) ([]*RankedActivityEvent, error) {
	if opts == nil {
		opts = DefaultSearchOptions()
	}

	// Determine search limit (fetch more than needed for filtering)
	fetchLimit := opts.Limit * 2
	if fetchLimit < 50 {
		fetchLimit = 50
	}

	// Parallel search: Bleve + Vector
	var bleveResults, vectorResults []*events.ActivityEvent
	var bleveErr, vectorErr error
	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()
		if ar.bleveSearcher != nil {
			bleveResults, bleveErr = ar.bleveSearcher.Search(ctx, query, fetchLimit)
		}
	}()

	go func() {
		defer wg.Done()
		if ar.vectorSearcher != nil {
			vectorResults, vectorErr = ar.vectorSearcher.Search(ctx, query, fetchLimit)
		}
	}()

	wg.Wait()

	// Check for errors (return first error encountered)
	if bleveErr != nil && vectorErr != nil {
		return nil, fmt.Errorf("both searches failed: bleve=%v, vector=%v", bleveErr, vectorErr)
	}
	if bleveErr != nil && ar.vectorSearcher == nil {
		return nil, fmt.Errorf("bleve search failed: %w", bleveErr)
	}
	if vectorErr != nil && ar.bleveSearcher == nil {
		return nil, fmt.Errorf("vector search failed: %w", vectorErr)
	}

	// Merge and deduplicate results
	merged := ar.mergeAndDeduplicate(bleveResults, vectorResults)

	// Filter by event types if specified
	if len(opts.EventTypes) > 0 {
		merged = ar.filterByEventTypes(merged, opts.EventTypes)
	}

	// Score each event
	now := time.Now()
	scored := make([]*RankedActivityEvent, 0, len(merged))

	for _, event := range merged {
		ranked, err := ar.scoreEvent(ctx, event, queryEmbed, now)
		if err != nil {
			// Log error but continue with other events
			continue
		}

		// Apply minimum score filter
		if ranked.Score >= opts.MinScore {
			scored = append(scored, ranked)
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Apply limit
	if len(scored) > opts.Limit {
		scored = scored[:opts.Limit]
	}

	return scored, nil
}

// mergeAndDeduplicate combines results from multiple sources, removing duplicates.
func (ar *ArchivalistRetriever) mergeAndDeduplicate(
	bleveResults, vectorResults []*events.ActivityEvent,
) []*events.ActivityEvent {
	seen := make(map[string]bool)
	merged := make([]*events.ActivityEvent, 0, len(bleveResults)+len(vectorResults))

	// Add Bleve results first (typically more precise for keyword matches)
	for _, event := range bleveResults {
		if event != nil && !seen[event.ID] {
			seen[event.ID] = true
			merged = append(merged, event)
		}
	}

	// Add vector results
	for _, event := range vectorResults {
		if event != nil && !seen[event.ID] {
			seen[event.ID] = true
			merged = append(merged, event)
		}
	}

	return merged
}

// filterByEventTypes filters events to only include specified types.
func (ar *ArchivalistRetriever) filterByEventTypes(
	evts []*events.ActivityEvent,
	allowedTypes []events.EventType,
) []*events.ActivityEvent {
	if len(allowedTypes) == 0 {
		return evts
	}

	typeSet := make(map[events.EventType]bool)
	for _, t := range allowedTypes {
		typeSet[t] = true
	}

	filtered := make([]*events.ActivityEvent, 0, len(evts))
	for _, event := range evts {
		if typeSet[event.EventType] {
			filtered = append(filtered, event)
		}
	}

	return filtered
}

// =============================================================================
// AE.5.6 scoreEvent Method
// =============================================================================

// scoreEvent computes a blended score for an event using cold-start priors
// and ACT-R memory activation. The blend ratio depends on trace count:
//   - traceCount < minTracesForWarm: pure cold score
//   - traceCount >= fullWarmTraces: pure ACT-R score
//   - Otherwise: linear blend from cold to warm
func (ar *ArchivalistRetriever) scoreEvent(
	ctx context.Context,
	event *events.ActivityEvent,
	queryEmbed []float32,
	now time.Time,
) (*RankedActivityEvent, error) {
	// Compute cold score
	eventEmbed := ar.getEventEmbedding(event)
	currentTurn := ar.estimateCurrentTurn(now)
	eventTurn := ar.estimateEventTurn(event.Timestamp)

	coldScore := ar.actionPrior.ComputeColdScore(
		event.EventType,
		queryEmbed,
		eventEmbed,
		currentTurn,
		eventTurn,
	)

	// Apply cross-session decay if event is from a different session
	coldScore = ar.applyCrossSessionDecay(coldScore, event.Timestamp, now)

	// Get ACT-R memory for this event (if exists)
	var warmScore float64
	var traceCount int
	var blendRatio float64

	// Try interface-based memory store first, then legacy
	if ar.memoryStoreIntf != nil {
		mem, err := ar.memoryStoreIntf.GetMemory(ctx, event.ID, domain.DomainArchivalist)
		if err == nil && mem != nil {
			traceCount = len(mem.Traces)
			if traceCount > 0 {
				// Compute ACT-R activation and convert to [0,1] range
				activation := mem.Activation(now)
				warmScore = ar.activationToScore(activation)
			}
		}
	} else if ar.memoryStore != nil {
		mem, err := ar.memoryStore.GetMemory(ctx, event.ID, domain.DomainArchivalist)
		if err == nil && mem != nil {
			traceCount = len(mem.Traces)
			if traceCount > 0 {
				activation := mem.Activation(now)
				warmScore = ar.activationToScore(activation)
			}
		}
	}

	// Determine blend ratio based on trace count
	blendRatio = ar.computeBlendRatio(traceCount)

	// Compute final blended score
	finalScore := (1.0-blendRatio)*coldScore + blendRatio*warmScore

	// Generate explanation
	explanation := ar.generateExplanation(event, coldScore, warmScore, blendRatio, traceCount)

	return &RankedActivityEvent{
		Event:       event,
		Score:       finalScore,
		ColdScore:   coldScore,
		WarmScore:   warmScore,
		BlendRatio:  blendRatio,
		Explanation: explanation,
	}, nil
}

// computeBlendRatio calculates the ratio of warm to cold scoring.
func (ar *ArchivalistRetriever) computeBlendRatio(traceCount int) float64 {
	if traceCount < ar.minTracesForWarm {
		return 0.0 // Pure cold score
	}

	if traceCount >= ar.fullWarmTraces {
		return 1.0 // Pure warm score
	}

	// Linear interpolation between minTracesForWarm and fullWarmTraces
	rangeVal := float64(ar.fullWarmTraces - ar.minTracesForWarm)
	progress := float64(traceCount - ar.minTracesForWarm)

	return progress / rangeVal
}

// activationToScore converts ACT-R activation to a [0,1] score.
// Uses a sigmoid transformation centered around typical activation values.
func (ar *ArchivalistRetriever) activationToScore(activation float64) float64 {
	if activation <= -100 {
		return 0.0
	}

	// Sigmoid: 1 / (1 + exp(-activation))
	// Shift by +2 to center around typical values
	shifted := activation + 2.0

	if shifted > 20 {
		return 1.0
	}
	if shifted < -20 {
		return 0.0
	}

	return 1.0 / (1.0 + math.Exp(-shifted))
}

// applyCrossSessionDecay applies temporal decay for events from other sessions.
func (ar *ArchivalistRetriever) applyCrossSessionDecay(score float64, eventTime, now time.Time) float64 {
	daysSince := now.Sub(eventTime).Hours() / 24.0
	if daysSince <= 0 {
		return score
	}

	// Exponential decay: score * exp(-daysSince / halfLife)
	// halfLife = crossSessionDecayDays * ln(2)
	decayRate := 1.0 / (ar.crossSessionDecayDays * math.Ln2)
	decayFactor := math.Exp(-daysSince * decayRate)

	return score * decayFactor
}

// getEventEmbedding extracts or estimates an embedding for the event.
func (ar *ArchivalistRetriever) getEventEmbedding(event *events.ActivityEvent) []float32 {
	// Check if embedding is stored in event data
	if event.Data != nil {
		if embed, ok := event.Data["embedding"].([]float32); ok {
			return embed
		}
		// Handle []any that might contain float32 values
		if embedAny, ok := event.Data["embedding"].([]any); ok {
			embed := make([]float32, len(embedAny))
			for i, v := range embedAny {
				if f, ok := v.(float64); ok {
					embed[i] = float32(f)
				} else if f32, ok := v.(float32); ok {
					embed[i] = f32
				}
			}
			return embed
		}
	}

	// Return zero vector as fallback (will result in 0 semantic similarity)
	return make([]float32, 0)
}

// estimateCurrentTurn estimates the current turn number based on time.
// This is a simplification; in production, turn numbers would be tracked explicitly.
func (ar *ArchivalistRetriever) estimateCurrentTurn(now time.Time) int {
	// Assume ~2 minutes per turn, count from epoch
	return int(now.Unix() / 120)
}

// estimateEventTurn estimates the turn number when an event occurred.
func (ar *ArchivalistRetriever) estimateEventTurn(eventTime time.Time) int {
	// Use same estimation as current turn
	return int(eventTime.Unix() / 120)
}

// generateExplanation creates a human-readable explanation of the score.
func (ar *ArchivalistRetriever) generateExplanation(
	event *events.ActivityEvent,
	coldScore, warmScore, blendRatio float64,
	traceCount int,
) string {
	if blendRatio == 0 {
		return fmt.Sprintf(
			"Pure cold score (traces=%d < min=%d): cold=%.3f, type=%s",
			traceCount, ar.minTracesForWarm, coldScore, event.EventType.String(),
		)
	}

	if blendRatio == 1.0 {
		return fmt.Sprintf(
			"Pure warm score (traces=%d >= full=%d): warm=%.3f, type=%s",
			traceCount, ar.fullWarmTraces, warmScore, event.EventType.String(),
		)
	}

	return fmt.Sprintf(
		"Blended score (traces=%d): cold=%.3f * %.1f%% + warm=%.3f * %.1f%%, type=%s",
		traceCount,
		coldScore, (1.0-blendRatio)*100,
		warmScore, blendRatio*100,
		event.EventType.String(),
	)
}

// =============================================================================
// AE.5.4-5.6 Accessor Methods for Testing
// =============================================================================

// ColdPrior returns the underlying ActionTypePrior.
func (ar *ArchivalistRetriever) ColdPrior() *ActionTypePrior {
	return ar.actionPrior
}

// MinTracesForWarm returns the minimum trace count for warm blending.
func (ar *ArchivalistRetriever) MinTracesForWarm() int {
	return ar.minTracesForWarm
}

// FullWarmTraces returns the trace count for full ACT-R scoring.
func (ar *ArchivalistRetriever) FullWarmTraces() int {
	return ar.fullWarmTraces
}

// ScoreEventDirect exposes scoreEvent for testing purposes.
func (ar *ArchivalistRetriever) ScoreEventDirect(
	ctx context.Context,
	event *events.ActivityEvent,
	queryEmbed []float32,
	now time.Time,
) (*RankedActivityEvent, error) {
	return ar.scoreEvent(ctx, event, queryEmbed, now)
}

// ComputeBlendRatioDirect exposes computeBlendRatio for testing.
func (ar *ArchivalistRetriever) ComputeBlendRatioDirect(traceCount int) float64 {
	return ar.computeBlendRatio(traceCount)
}

// ActivationToScoreDirect exposes activationToScore for testing.
func (ar *ArchivalistRetriever) ActivationToScoreDirect(activation float64) float64 {
	return ar.activationToScore(activation)
}

// ApplyCrossSessionDecayDirect exposes applyCrossSessionDecay for testing.
func (ar *ArchivalistRetriever) ApplyCrossSessionDecayDirect(score float64, eventTime, now time.Time) float64 {
	return ar.applyCrossSessionDecay(score, eventTime, now)
}

// Ensure CosineSimilarityVectors is available (compile-time check)
var _ = hnsw.CosineSimilarityVectors

// =============================================================================
// AE.5.7 LoadCrossSessionState Implementation
// =============================================================================

// LoadCrossSessionState loads prior session summaries from the database and
// applies cross-session decay to inherited high-value event ACT-R traces.
//
// The decay formula is: decayFactor = e^(-daysSinceLastSession / crossSessionDecayDays)
//
// This allows the retriever to bootstrap activation values for events from
// prior sessions, enabling continuity of memory across session boundaries.
func (ar *ArchivalistRetriever) LoadCrossSessionState(
	ctx context.Context,
	projectID string,
	daysSinceLastSession float64,
) error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	if ar.sessionLoader == nil {
		// No session loader configured, nothing to load
		ar.crossSessionState = &CrossSessionState{
			ProjectID:            projectID,
			LoadedAt:             time.Now().UTC(),
			DaysSinceLastSession: daysSinceLastSession,
			DecayFactor:          0,
			Sessions:             nil,
			InheritedTraces:      nil,
			TopEventTypes:        make(map[events.EventType]int),
		}
		return nil
	}

	// Calculate cross-session decay factor
	decayFactor := math.Exp(-daysSinceLastSession / ar.crossSessionDecayDays)

	// Load recent sessions (limit to 10 most recent)
	sessions, err := ar.sessionLoader.LoadRecentSessions(ctx, projectID, 10)
	if err != nil {
		return err
	}

	// Initialize cross-session state
	state := &CrossSessionState{
		ProjectID:            projectID,
		LoadedAt:             time.Now().UTC(),
		DaysSinceLastSession: daysSinceLastSession,
		DecayFactor:          decayFactor,
		Sessions:             sessions,
		InheritedTraces:      make([]InheritedTrace, 0),
		TopEventTypes:        make(map[events.EventType]int),
	}

	// Aggregate event types across sessions
	for _, session := range sessions {
		for eventType, count := range session.TopEventTypes {
			state.TopEventTypes[eventType] += count
		}

		// Load high-value events and inherit their traces
		if len(session.TopEventIDs) > 0 {
			sessionEvents, err := ar.sessionLoader.LoadSessionEvents(ctx, session.SessionID, session.TopEventIDs)
			if err != nil {
				// Log error but continue with other sessions
				continue
			}

			for _, event := range sessionEvents {
				// Compute original activation based on event importance
				originalActivation := event.Importance

				// Apply cross-session decay
				decayedActivation := originalActivation * decayFactor

				// Only inherit traces with meaningful activation
				if decayedActivation > 0.01 {
					state.InheritedTraces = append(state.InheritedTraces, InheritedTrace{
						EventID:            event.ID,
						EventType:          event.EventType,
						OriginalActivation: originalActivation,
						DecayedActivation:  decayedActivation,
						SessionID:          session.SessionID,
						OriginalTimestamp:  event.Timestamp,
					})
				}
			}
		}
	}

	ar.crossSessionState = state
	return nil
}

// =============================================================================
// AE.5.8 RecordRetrieval Implementation
// =============================================================================

// RecordRetrieval records that an event was retrieved and whether it was useful.
// This delegates to the memory store to record the access and enables future
// learning of retrieval patterns.
func (ar *ArchivalistRetriever) RecordRetrieval(
	ctx context.Context,
	eventID string,
	wasUseful bool,
) error {
	if ar.memoryStore == nil {
		return nil
	}

	// Build context string for the access trace
	contextStr := "retrieval"
	if wasUseful {
		contextStr = "retrieval:useful"
	} else {
		contextStr = "retrieval:not_useful"
	}

	// Delegate to memory store using AccessRetrieval type
	return ar.memoryStore.RecordAccess(ctx, eventID, memory.AccessRetrieval, contextStr)
}

// =============================================================================
// Cross-Session State Accessors
// =============================================================================

// SetCrossSessionDecayDays sets the time constant for cross-session activation decay.
// Larger values mean slower decay (events remain relevant longer across sessions).
func (ar *ArchivalistRetriever) SetCrossSessionDecayDays(days float64) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	if days > 0 {
		ar.crossSessionDecayDays = days
	}
}

// GetCrossSessionDecayDays returns the current cross-session decay time constant.
func (ar *ArchivalistRetriever) GetCrossSessionDecayDays() float64 {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.crossSessionDecayDays
}

// ClearCrossSessionState clears any loaded cross-session state.
func (ar *ArchivalistRetriever) ClearCrossSessionState() {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	ar.crossSessionState = nil
}

// GetLoadedSessionCount returns the number of sessions in the loaded cross-session state.
// Returns 0 if no state is loaded.
func (ar *ArchivalistRetriever) GetLoadedSessionCount() int {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	if ar.crossSessionState == nil {
		return 0
	}
	return len(ar.crossSessionState.Sessions)
}

// GetCrossSessionState returns a copy of the current cross-session state.
// Returns nil if no state is loaded.
func (ar *ArchivalistRetriever) GetCrossSessionState() *CrossSessionState {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	if ar.crossSessionState == nil {
		return nil
	}

	// Return a shallow copy to prevent external modification
	stateCopy := *ar.crossSessionState
	return &stateCopy
}

// GetInheritedActivation returns the decayed activation for an event ID
// from the loaded cross-session state. Returns 0 if not found.
func (ar *ArchivalistRetriever) GetInheritedActivation(eventID string) float64 {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	if ar.crossSessionState == nil {
		return 0
	}

	for _, trace := range ar.crossSessionState.InheritedTraces {
		if trace.EventID == eventID {
			return trace.DecayedActivation
		}
	}

	return 0
}

// HasCrossSessionState returns true if cross-session state is loaded.
func (ar *ArchivalistRetriever) HasCrossSessionState() bool {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.crossSessionState != nil
}

// GetDecayFactor returns the current cross-session decay factor.
// Returns 0 if no state is loaded.
func (ar *ArchivalistRetriever) GetDecayFactor() float64 {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	if ar.crossSessionState == nil {
		return 0
	}
	return ar.crossSessionState.DecayFactor
}
