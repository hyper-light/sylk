package handoff

import (
	"encoding/json"
	"sync"
	"time"
)

// =============================================================================
// HO.6.1 PreparedContext - Pre-computed Context Ready for Handoff
// =============================================================================
//
// PreparedContext maintains a continuously updated, pre-computed context
// that is ready for agent handoffs. Unlike computing context at handoff time,
// PreparedContext is maintained incrementally as the conversation progresses,
// ensuring fast handoff operations.
//
// Features:
//   - Pre-computed summary from RollingSummary
//   - Recent messages buffer
//   - Tool state tracking
//   - Token count monitoring
//   - Stale detection and refresh
//   - Serialization for handoff
//
// Thread Safety:
//   - All operations are protected by a read-write mutex
//   - Safe for concurrent access from multiple goroutines
//
// Example usage:
//
//	ctx := NewPreparedContext(PreparedContextConfig{
//	    MaxSummaryTokens: 1000,
//	    MaxRecentMessages: 10,
//	    MaxAge: 5 * time.Minute,
//	})
//	ctx.AddMessage(NewMessage("user", "Hello"))
//	ctx.UpdateToolState("editor", ToolState{Active: true})
//	if ctx.RefreshIfStale(5 * time.Minute) {
//	    // Context was refreshed
//	}
//	data := ctx.ToBytes()

// ToolState represents the state of a tool for context preservation.
type ToolState struct {
	// Name is the identifier for the tool.
	Name string `json:"name"`

	// Active indicates if the tool is currently active.
	Active bool `json:"active"`

	// State contains tool-specific state data.
	State map[string]interface{} `json:"state,omitempty"`

	// LastUsed is when the tool was last used.
	LastUsed time.Time `json:"last_used"`

	// UseCount tracks how many times the tool has been used.
	UseCount int `json:"use_count"`
}

// PreparedContextConfig controls the behavior of PreparedContext.
type PreparedContextConfig struct {
	// MaxSummaryTokens is the token budget for the rolling summary.
	MaxSummaryTokens int `json:"max_summary_tokens"`

	// MaxRecentMessages is how many recent messages to keep.
	MaxRecentMessages int `json:"max_recent_messages"`

	// MaxAge is the maximum age before context is considered stale.
	MaxAge time.Duration `json:"max_age"`

	// AutoRefreshEnabled enables automatic refresh on stale detection.
	AutoRefreshEnabled bool `json:"auto_refresh_enabled"`

	// MaxToolStates limits the number of tracked tool states.
	MaxToolStates int `json:"max_tool_states"`
}

// DefaultPreparedContextConfig returns sensible defaults.
func DefaultPreparedContextConfig() PreparedContextConfig {
	return PreparedContextConfig{
		MaxSummaryTokens:   1000,
		MaxRecentMessages:  10,
		MaxAge:             5 * time.Minute,
		AutoRefreshEnabled: true,
		MaxToolStates:      20,
	}
}

// PreparedContext maintains pre-computed context ready for agent handoffs.
type PreparedContext struct {
	mu sync.RWMutex

	// summary is the rolling summary of the conversation.
	summary *RollingSummary

	// recentMessages stores the most recent messages.
	recentMessages *CircularBuffer[Message]

	// toolStates tracks the state of active tools.
	toolStates map[string]*ToolState

	// tokenCount is the total estimated token count.
	tokenCount int

	// lastUpdated is when the context was last updated.
	lastUpdated time.Time

	// lastRefreshed is when the context was last fully refreshed.
	lastRefreshed time.Time

	// createdAt is when the context was created.
	createdAt time.Time

	// config holds configuration options.
	config PreparedContextConfig

	// version is incremented on each update for change detection.
	version int64

	// metadata stores additional context metadata.
	metadata map[string]string
}

// NewPreparedContext creates a new PreparedContext with the given configuration.
// If config has zero values, defaults are applied.
func NewPreparedContext(config PreparedContextConfig) *PreparedContext {
	// Apply defaults for zero values
	if config.MaxSummaryTokens <= 0 {
		config.MaxSummaryTokens = DefaultPreparedContextConfig().MaxSummaryTokens
	}
	if config.MaxRecentMessages <= 0 {
		config.MaxRecentMessages = DefaultPreparedContextConfig().MaxRecentMessages
	}
	if config.MaxAge <= 0 {
		config.MaxAge = DefaultPreparedContextConfig().MaxAge
	}
	if config.MaxToolStates <= 0 {
		config.MaxToolStates = DefaultPreparedContextConfig().MaxToolStates
	}

	now := time.Now()
	return &PreparedContext{
		summary:        NewRollingSummary(config.MaxSummaryTokens),
		recentMessages: NewCircularBuffer[Message](config.MaxRecentMessages),
		toolStates:     make(map[string]*ToolState),
		tokenCount:     0,
		lastUpdated:    now,
		lastRefreshed:  now,
		createdAt:      now,
		config:         config,
		version:        0,
		metadata:       make(map[string]string),
	}
}

// NewPreparedContextDefault creates a new PreparedContext with default configuration.
func NewPreparedContextDefault() *PreparedContext {
	return NewPreparedContext(DefaultPreparedContextConfig())
}

// =============================================================================
// Message Operations
// =============================================================================

// AddMessage adds a new message to the prepared context.
// Updates both the rolling summary and recent messages buffer.
func (pc *PreparedContext) AddMessage(msg Message) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Ensure timestamp is set
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	// Ensure token count is estimated
	if msg.TokenCount == 0 {
		msg.TokenCount = estimateTokens(msg.Content)
	}

	// Add to rolling summary
	pc.summary.AddMessage(msg)

	// Add to recent messages buffer
	pc.recentMessages.Push(msg)

	// Update token count
	pc.recalculateTokenCount()

	pc.lastUpdated = time.Now()
	pc.version++
}

// RecentMessages returns the recent messages from the buffer.
func (pc *PreparedContext) RecentMessages() []Message {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.recentMessages.Items()
}

// =============================================================================
// Tool State Operations
// =============================================================================

// UpdateToolState updates or adds a tool state.
func (pc *PreparedContext) UpdateToolState(name string, state ToolState) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	state.Name = name
	if state.LastUsed.IsZero() {
		state.LastUsed = time.Now()
	}

	// Check if we need to prune tool states
	if len(pc.toolStates) >= pc.config.MaxToolStates {
		if _, exists := pc.toolStates[name]; !exists {
			pc.pruneOldestToolState()
		}
	}

	pc.toolStates[name] = &state
	pc.recalculateTokenCount()
	pc.lastUpdated = time.Now()
	pc.version++
}

// GetToolState returns the state for a specific tool.
func (pc *PreparedContext) GetToolState(name string) (*ToolState, bool) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	state, ok := pc.toolStates[name]
	if !ok {
		return nil, false
	}

	// Return a copy to prevent mutation
	copy := *state
	return &copy, true
}

// ToolStates returns all tool states.
func (pc *PreparedContext) ToolStates() map[string]ToolState {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	result := make(map[string]ToolState, len(pc.toolStates))
	for name, state := range pc.toolStates {
		result[name] = *state
	}
	return result
}

// RemoveToolState removes a tool state.
func (pc *PreparedContext) RemoveToolState(name string) bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if _, exists := pc.toolStates[name]; exists {
		delete(pc.toolStates, name)
		pc.recalculateTokenCount()
		pc.lastUpdated = time.Now()
		pc.version++
		return true
	}
	return false
}

// pruneOldestToolState removes the least recently used tool state.
func (pc *PreparedContext) pruneOldestToolState() {
	var oldestName string
	var oldestTime time.Time

	for name, state := range pc.toolStates {
		if oldestName == "" || state.LastUsed.Before(oldestTime) {
			oldestName = name
			oldestTime = state.LastUsed
		}
	}

	if oldestName != "" {
		delete(pc.toolStates, oldestName)
	}
}

// =============================================================================
// Summary Access
// =============================================================================

// Summary returns the current rolling summary text.
func (pc *PreparedContext) Summary() string {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.summary.Summary()
}

// KeyTopics returns the key topics from the rolling summary.
func (pc *PreparedContext) KeyTopics() []string {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.summary.KeyTopics()
}

// =============================================================================
// Token Count
// =============================================================================

// TokenCount returns the current estimated token count.
func (pc *PreparedContext) TokenCount() int {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.tokenCount
}

// recalculateTokenCount updates the total token count.
// Caller must hold the write lock.
func (pc *PreparedContext) recalculateTokenCount() {
	count := 0

	// Summary tokens
	count += pc.summary.TokenCount()

	// Recent messages tokens (separate from summary for recent access)
	for _, msg := range pc.recentMessages.Items() {
		count += msg.TokenCount
	}

	// Tool states tokens (rough estimate)
	for _, state := range pc.toolStates {
		count += estimateToolStateTokens(state)
	}

	pc.tokenCount = count
}

// estimateToolStateTokens provides a rough token estimate for a tool state.
func estimateToolStateTokens(state *ToolState) int {
	base := 10 // Base overhead for the state structure
	if state.State != nil {
		// Rough estimate based on state map size
		base += len(state.State) * 5
	}
	return base
}

// =============================================================================
// Staleness and Refresh
// =============================================================================

// IsStale returns true if the context is older than the specified maxAge.
func (pc *PreparedContext) IsStale(maxAge time.Duration) bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return time.Since(pc.lastUpdated) > maxAge
}

// RefreshIfStale updates the context if it's older than the specified maxAge.
// Returns true if a refresh was performed.
func (pc *PreparedContext) RefreshIfStale(maxAge time.Duration) bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if time.Since(pc.lastUpdated) <= maxAge {
		return false
	}

	pc.performRefresh()
	return true
}

// Refresh forces a refresh of the prepared context.
func (pc *PreparedContext) Refresh() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.performRefresh()
}

// performRefresh performs the actual refresh operation.
// Caller must hold the write lock.
func (pc *PreparedContext) performRefresh() {
	// Recalculate token count
	pc.recalculateTokenCount()

	// Prune inactive tool states
	pc.pruneInactiveToolStates()

	pc.lastRefreshed = time.Now()
	pc.lastUpdated = time.Now()
	pc.version++
}

// pruneInactiveToolStates removes tool states that haven't been used recently.
func (pc *PreparedContext) pruneInactiveToolStates() {
	cutoff := time.Now().Add(-pc.config.MaxAge * 2)

	// Collect keys to delete
	var toDelete []string
	for name, state := range pc.toolStates {
		if !state.Active && state.LastUsed.Before(cutoff) {
			toDelete = append(toDelete, name)
		}
	}

	// Delete after iteration
	for _, name := range toDelete {
		delete(pc.toolStates, name)
	}
}

// =============================================================================
// Serialization
// =============================================================================

// ToBytes serializes the prepared context for handoff.
func (pc *PreparedContext) ToBytes() ([]byte, error) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	return json.Marshal(pc.toSnapshot())
}

// PreparedContextSnapshot is a serializable snapshot of the prepared context.
type PreparedContextSnapshot struct {
	Summary        string               `json:"summary"`
	RecentMessages []Message            `json:"recent_messages"`
	ToolStates     map[string]ToolState `json:"tool_states"`
	TokenCount     int                  `json:"token_count"`
	KeyTopics      []string             `json:"key_topics"`
	LastUpdated    string               `json:"last_updated"`
	LastRefreshed  string               `json:"last_refreshed"`
	CreatedAt      string               `json:"created_at"`
	Version        int64                `json:"version"`
	Metadata       map[string]string    `json:"metadata,omitempty"`
	Config         PreparedContextConfig `json:"config"`
}

// toSnapshot creates a snapshot of the current state.
// Caller must hold at least a read lock.
func (pc *PreparedContext) toSnapshot() PreparedContextSnapshot {
	toolStates := make(map[string]ToolState, len(pc.toolStates))
	for name, state := range pc.toolStates {
		toolStates[name] = *state
	}

	metadata := make(map[string]string, len(pc.metadata))
	for k, v := range pc.metadata {
		metadata[k] = v
	}

	return PreparedContextSnapshot{
		Summary:        pc.summary.Summary(),
		RecentMessages: pc.recentMessages.Items(),
		ToolStates:     toolStates,
		TokenCount:     pc.tokenCount,
		KeyTopics:      pc.summary.KeyTopics(),
		LastUpdated:    pc.lastUpdated.Format(time.RFC3339Nano),
		LastRefreshed:  pc.lastRefreshed.Format(time.RFC3339Nano),
		CreatedAt:      pc.createdAt.Format(time.RFC3339Nano),
		Version:        pc.version,
		Metadata:       metadata,
		Config:         pc.config,
	}
}

// Snapshot returns a thread-safe snapshot of the prepared context.
func (pc *PreparedContext) Snapshot() PreparedContextSnapshot {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.toSnapshot()
}

// FromBytes deserializes a prepared context from bytes.
func FromBytes(data []byte) (*PreparedContext, error) {
	var snapshot PreparedContextSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}

	return FromSnapshot(snapshot)
}

// FromSnapshot creates a PreparedContext from a snapshot.
func FromSnapshot(snapshot PreparedContextSnapshot) (*PreparedContext, error) {
	pc := NewPreparedContext(snapshot.Config)

	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Restore messages
	for _, msg := range snapshot.RecentMessages {
		pc.summary.AddMessage(msg)
		pc.recentMessages.Push(msg)
	}

	// Restore tool states
	for name, state := range snapshot.ToolStates {
		stateCopy := state
		pc.toolStates[name] = &stateCopy
	}

	// Restore metadata
	for k, v := range snapshot.Metadata {
		pc.metadata[k] = v
	}

	// Restore timestamps
	if t, err := time.Parse(time.RFC3339Nano, snapshot.LastUpdated); err == nil {
		pc.lastUpdated = t
	}
	if t, err := time.Parse(time.RFC3339Nano, snapshot.LastRefreshed); err == nil {
		pc.lastRefreshed = t
	}
	if t, err := time.Parse(time.RFC3339Nano, snapshot.CreatedAt); err == nil {
		pc.createdAt = t
	}

	pc.version = snapshot.Version
	pc.recalculateTokenCount()

	return pc, nil
}

// =============================================================================
// JSON Serialization (full state)
// =============================================================================

// preparedContextJSON is the full JSON representation.
type preparedContextJSON struct {
	Summary        *RollingSummary           `json:"summary"`
	RecentMessages *CircularBuffer[Message]  `json:"recent_messages"`
	ToolStates     map[string]*ToolState     `json:"tool_states"`
	TokenCount     int                       `json:"token_count"`
	LastUpdated    string                    `json:"last_updated"`
	LastRefreshed  string                    `json:"last_refreshed"`
	CreatedAt      string                    `json:"created_at"`
	Config         PreparedContextConfig     `json:"config"`
	Version        int64                     `json:"version"`
	Metadata       map[string]string         `json:"metadata"`
}

// MarshalJSON implements json.Marshaler.
func (pc *PreparedContext) MarshalJSON() ([]byte, error) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	return json.Marshal(preparedContextJSON{
		Summary:        pc.summary,
		RecentMessages: pc.recentMessages,
		ToolStates:     pc.toolStates,
		TokenCount:     pc.tokenCount,
		LastUpdated:    pc.lastUpdated.Format(time.RFC3339Nano),
		LastRefreshed:  pc.lastRefreshed.Format(time.RFC3339Nano),
		CreatedAt:      pc.createdAt.Format(time.RFC3339Nano),
		Config:         pc.config,
		Version:        pc.version,
		Metadata:       pc.metadata,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (pc *PreparedContext) UnmarshalJSON(data []byte) error {
	var temp preparedContextJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	pc.mu.Lock()
	defer pc.mu.Unlock()

	pc.summary = temp.Summary
	if pc.summary == nil {
		pc.summary = NewRollingSummary(temp.Config.MaxSummaryTokens)
	}

	pc.recentMessages = temp.RecentMessages
	if pc.recentMessages == nil {
		pc.recentMessages = NewCircularBuffer[Message](temp.Config.MaxRecentMessages)
	}

	pc.toolStates = temp.ToolStates
	if pc.toolStates == nil {
		pc.toolStates = make(map[string]*ToolState)
	}

	pc.tokenCount = temp.TokenCount
	pc.config = temp.Config
	pc.version = temp.Version

	pc.metadata = temp.Metadata
	if pc.metadata == nil {
		pc.metadata = make(map[string]string)
	}

	// Parse timestamps
	if temp.LastUpdated != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.LastUpdated); err == nil {
			pc.lastUpdated = t
		}
	}
	if temp.LastRefreshed != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.LastRefreshed); err == nil {
			pc.lastRefreshed = t
		}
	}
	if temp.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.CreatedAt); err == nil {
			pc.createdAt = t
		}
	}

	return nil
}

// =============================================================================
// Metadata Operations
// =============================================================================

// SetMetadata sets a metadata key-value pair.
func (pc *PreparedContext) SetMetadata(key, value string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.metadata[key] = value
	pc.version++
}

// GetMetadata returns a metadata value.
func (pc *PreparedContext) GetMetadata(key string) (string, bool) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	v, ok := pc.metadata[key]
	return v, ok
}

// Metadata returns all metadata.
func (pc *PreparedContext) Metadata() map[string]string {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	result := make(map[string]string, len(pc.metadata))
	for k, v := range pc.metadata {
		result[k] = v
	}
	return result
}

// =============================================================================
// Status Information
// =============================================================================

// Version returns the current version number.
func (pc *PreparedContext) Version() int64 {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.version
}

// LastUpdated returns when the context was last updated.
func (pc *PreparedContext) LastUpdated() time.Time {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.lastUpdated
}

// LastRefreshed returns when the context was last refreshed.
func (pc *PreparedContext) LastRefreshed() time.Time {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.lastRefreshed
}

// CreatedAt returns when the context was created.
func (pc *PreparedContext) CreatedAt() time.Time {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.createdAt
}

// Config returns the current configuration.
func (pc *PreparedContext) Config() PreparedContextConfig {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.config
}

// =============================================================================
// Statistics
// =============================================================================

// PreparedContextStats contains statistics about the prepared context.
type PreparedContextStats struct {
	TokenCount       int           `json:"token_count"`
	SummaryTokens    int           `json:"summary_tokens"`
	MessageCount     int           `json:"message_count"`
	BufferedMessages int           `json:"buffered_messages"`
	ToolStateCount   int           `json:"tool_state_count"`
	ActiveTools      int           `json:"active_tools"`
	KeyTopicCount    int           `json:"key_topic_count"`
	Version          int64         `json:"version"`
	Age              time.Duration `json:"age"`
	TimeSinceRefresh time.Duration `json:"time_since_refresh"`
}

// Stats returns statistics about the prepared context.
func (pc *PreparedContext) Stats() PreparedContextStats {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	activeTools := 0
	for _, state := range pc.toolStates {
		if state.Active {
			activeTools++
		}
	}

	return PreparedContextStats{
		TokenCount:       pc.tokenCount,
		SummaryTokens:    pc.summary.TokenCount(),
		MessageCount:     pc.summary.MessageCount(),
		BufferedMessages: pc.recentMessages.Len(),
		ToolStateCount:   len(pc.toolStates),
		ActiveTools:      activeTools,
		KeyTopicCount:    len(pc.summary.KeyTopics()),
		Version:          pc.version,
		Age:              time.Since(pc.createdAt),
		TimeSinceRefresh: time.Since(pc.lastRefreshed),
	}
}

// Clear resets the prepared context to its initial state.
func (pc *PreparedContext) Clear() {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	pc.summary.Clear()
	pc.recentMessages.Clear()
	pc.toolStates = make(map[string]*ToolState)
	pc.metadata = make(map[string]string)
	pc.tokenCount = 0
	pc.lastUpdated = time.Now()
	pc.lastRefreshed = time.Now()
	pc.version++
}
