// Package pipeline provides guide agent handoff state for archival.
// PH.3: GuideHandoffState captures conversation routing and user preference state
// for handoff to fresh agent instances.
package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TopicState represents the state of a conversation topic.
type TopicState struct {
	Name       string    `json:"name"`
	StartedAt  time.Time `json:"started_at"`
	LastActive time.Time `json:"last_active"`
	MessageIDs []string  `json:"message_ids"`
}

// RoutingDecision represents a past routing decision.
type RoutingDecision struct {
	MessageID   string    `json:"message_id"`
	TargetAgent string    `json:"target_agent"`
	Confidence  float64   `json:"confidence"`
	Reason      string    `json:"reason"`
	Timestamp   time.Time `json:"timestamp"`
}

// AgentAffinity represents user-agent interaction patterns.
type AgentAffinity struct {
	AgentType     string  `json:"agent_type"`
	InteractCount int     `json:"interact_count"`
	SuccessRate   float64 `json:"success_rate"`
	LastUsed      int64   `json:"last_used"` // Unix timestamp
}

// UserPreference captures user interaction preferences.
type UserPreference struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Source    string `json:"source"`     // explicit, inferred
	UpdatedAt int64  `json:"updated_at"` // Unix timestamp
}

// ConversationMessage is a minimal representation for handoff.
type ConversationMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"` // user, assistant
	Summary   string `json:"summary"`
	Timestamp int64  `json:"timestamp"`
}

// GuideHandoffState captures guide agent state for handoff archival.
type GuideHandoffState struct {
	// Base archival fields
	agentID        string
	agentType      string
	sessionID      string
	pipelineID     string
	triggerReason  string
	triggerContext float64
	handoffIndex   int
	startedAt      time.Time
	completedAt    time.Time

	// Guide-specific state
	ConversationHistory []ConversationMessage `json:"conversation_history"`
	CurrentIntent       string                `json:"current_intent"`
	ActiveTopics        []TopicState          `json:"active_topics"`
	RecentRoutings      []RoutingDecision     `json:"recent_routings"`
	AgentAffinities     []AgentAffinity       `json:"agent_affinities"`
	UserPreferences     []UserPreference      `json:"user_preferences"`
}

// GetAgentID returns the agent identifier.
func (s *GuideHandoffState) GetAgentID() string { return s.agentID }

// GetAgentType returns the agent type.
func (s *GuideHandoffState) GetAgentType() string { return s.agentType }

// GetSessionID returns the session identifier.
func (s *GuideHandoffState) GetSessionID() string { return s.sessionID }

// GetPipelineID returns the pipeline identifier.
func (s *GuideHandoffState) GetPipelineID() string { return s.pipelineID }

// GetTriggerReason returns the handoff trigger reason.
func (s *GuideHandoffState) GetTriggerReason() string { return s.triggerReason }

// GetTriggerContext returns the trigger context value.
func (s *GuideHandoffState) GetTriggerContext() float64 { return s.triggerContext }

// GetHandoffIndex returns the handoff sequence number.
func (s *GuideHandoffState) GetHandoffIndex() int { return s.handoffIndex }

// GetStartedAt returns when the agent started.
func (s *GuideHandoffState) GetStartedAt() time.Time { return s.startedAt }

// GetCompletedAt returns when handoff occurred.
func (s *GuideHandoffState) GetCompletedAt() time.Time { return s.completedAt }

// ToJSON serializes the full state to JSON.
func (s *GuideHandoffState) ToJSON() ([]byte, error) {
	return json.Marshal(s.toJSONStruct())
}

// toJSONStruct creates a JSON-serializable struct.
func (s *GuideHandoffState) toJSONStruct() map[string]any {
	return map[string]any{
		"agent_id":             s.agentID,
		"agent_type":           s.agentType,
		"session_id":           s.sessionID,
		"pipeline_id":          s.pipelineID,
		"trigger_reason":       s.triggerReason,
		"trigger_context":      s.triggerContext,
		"handoff_index":        s.handoffIndex,
		"started_at":           s.startedAt.Format(time.RFC3339),
		"completed_at":         s.completedAt.Format(time.RFC3339),
		"conversation_history": s.ConversationHistory,
		"current_intent":       s.CurrentIntent,
		"active_topics":        s.ActiveTopics,
		"recent_routings":      s.RecentRoutings,
		"agent_affinities":     s.AgentAffinities,
		"user_preferences":     s.UserPreferences,
	}
}

// GetSummary returns a text summary for embedding.
func (s *GuideHandoffState) GetSummary() string {
	var parts []string

	parts = append(parts, fmt.Sprintf("Guide agent handoff for session %s.", s.sessionID))

	if s.CurrentIntent != "" {
		parts = append(parts, fmt.Sprintf("Current intent: %s.", s.CurrentIntent))
	}

	parts = append(parts, s.summarizeTopics()...)
	parts = append(parts, s.summarizeRoutings()...)
	parts = append(parts, s.summarizeAffinities()...)

	return strings.Join(parts, " ")
}

// summarizeTopics generates topic summary parts.
func (s *GuideHandoffState) summarizeTopics() []string {
	if len(s.ActiveTopics) == 0 {
		return nil
	}

	names := make([]string, 0, len(s.ActiveTopics))
	for _, topic := range s.ActiveTopics {
		names = append(names, topic.Name)
	}

	return []string{fmt.Sprintf("Active topics: %s.", strings.Join(names, ", "))}
}

// summarizeRoutings generates routing summary parts.
func (s *GuideHandoffState) summarizeRoutings() []string {
	if len(s.RecentRoutings) == 0 {
		return nil
	}

	agents := collectUniqueAgents(s.RecentRoutings)
	return []string{fmt.Sprintf("Recent routing to: %s.", strings.Join(agents, ", "))}
}

// collectUniqueAgents extracts unique agent types from routings.
func collectUniqueAgents(routings []RoutingDecision) []string {
	seen := make(map[string]bool)
	agents := make([]string, 0, len(routings))

	for _, r := range routings {
		if !seen[r.TargetAgent] {
			seen[r.TargetAgent] = true
			agents = append(agents, r.TargetAgent)
		}
	}

	return agents
}

// summarizeAffinities generates affinity summary parts.
func (s *GuideHandoffState) summarizeAffinities() []string {
	if len(s.AgentAffinities) == 0 {
		return nil
	}

	top := findTopAffinity(s.AgentAffinities)
	if top == nil {
		return nil
	}

	return []string{fmt.Sprintf("Highest affinity: %s (%d interactions).",
		top.AgentType, top.InteractCount)}
}

// findTopAffinity returns the agent with highest interaction count.
func findTopAffinity(affinities []AgentAffinity) *AgentAffinity {
	if len(affinities) == 0 {
		return nil
	}

	top := &affinities[0]
	for i := 1; i < len(affinities); i++ {
		if affinities[i].InteractCount > top.InteractCount {
			top = &affinities[i]
		}
	}

	return top
}

// GuideHandoffConfig contains configuration for building guide handoff state.
type GuideHandoffConfig struct {
	AgentID        string
	SessionID      string
	PipelineID     string
	TriggerReason  string
	TriggerContext float64
	HandoffIndex   int
	StartedAt      time.Time
}

// BuildGuideHandoffState creates a new GuideHandoffState from guide agent data.
func BuildGuideHandoffState(
	config GuideHandoffConfig,
	history []ConversationMessage,
	intent string,
	topics []TopicState,
	routings []RoutingDecision,
	affinities []AgentAffinity,
	preferences []UserPreference,
) *GuideHandoffState {
	state := &GuideHandoffState{
		agentID:        config.AgentID,
		agentType:      "guide",
		sessionID:      config.SessionID,
		pipelineID:     config.PipelineID,
		triggerReason:  config.TriggerReason,
		triggerContext: config.TriggerContext,
		handoffIndex:   config.HandoffIndex,
		startedAt:      config.StartedAt,
		completedAt:    time.Now(),
	}

	state.ConversationHistory = copyHistory(history)
	state.CurrentIntent = intent
	state.ActiveTopics = copyTopics(topics)
	state.RecentRoutings = copyRoutings(routings)
	state.AgentAffinities = copyAffinities(affinities)
	state.UserPreferences = copyPreferences(preferences)

	return state
}

// copyHistory creates a defensive copy of conversation history.
func copyHistory(src []ConversationMessage) []ConversationMessage {
	if src == nil {
		return nil
	}
	dst := make([]ConversationMessage, len(src))
	copy(dst, src)
	return dst
}

// copyTopics creates a defensive copy of topics.
func copyTopics(src []TopicState) []TopicState {
	if src == nil {
		return nil
	}
	dst := make([]TopicState, len(src))
	for i, t := range src {
		dst[i] = TopicState{
			Name:       t.Name,
			StartedAt:  t.StartedAt,
			LastActive: t.LastActive,
			MessageIDs: copyStringSlice(t.MessageIDs),
		}
	}
	return dst
}

// copyRoutings creates a defensive copy of routings.
func copyRoutings(src []RoutingDecision) []RoutingDecision {
	if src == nil {
		return nil
	}
	dst := make([]RoutingDecision, len(src))
	copy(dst, src)
	return dst
}

// copyAffinities creates a defensive copy of affinities.
func copyAffinities(src []AgentAffinity) []AgentAffinity {
	if src == nil {
		return nil
	}
	dst := make([]AgentAffinity, len(src))
	copy(dst, src)
	return dst
}

// copyPreferences creates a defensive copy of preferences.
func copyPreferences(src []UserPreference) []UserPreference {
	if src == nil {
		return nil
	}
	dst := make([]UserPreference, len(src))
	copy(dst, src)
	return dst
}

// copyStringSlice creates a defensive copy of a string slice.
func copyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// InjectGuideHandoffState restores guide agent state from archived handoff data.
func InjectGuideHandoffState(data []byte) (*GuideHandoffState, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal handoff data: %w", err)
	}

	state := &GuideHandoffState{}

	if err := injectBaseFields(state, raw); err != nil {
		return nil, err
	}

	if err := injectGuideFields(state, raw); err != nil {
		return nil, err
	}

	return state, nil
}

// injectBaseFields populates base archival fields from raw data.
func injectBaseFields(state *GuideHandoffState, raw map[string]any) error {
	state.agentID = getString(raw, "agent_id")
	state.agentType = getString(raw, "agent_type")
	state.sessionID = getString(raw, "session_id")
	state.pipelineID = getString(raw, "pipeline_id")
	state.triggerReason = getString(raw, "trigger_reason")
	state.triggerContext = getFloat(raw, "trigger_context")
	state.handoffIndex = getInt(raw, "handoff_index")

	var err error
	state.startedAt, err = parseTime(raw, "started_at")
	if err != nil {
		return err
	}

	state.completedAt, err = parseTime(raw, "completed_at")
	if err != nil {
		return err
	}

	return nil
}

// injectGuideFields populates guide-specific fields from raw data.
func injectGuideFields(state *GuideHandoffState, raw map[string]any) error {
	state.CurrentIntent = getString(raw, "current_intent")

	if err := injectConversationHistory(state, raw); err != nil {
		return err
	}

	if err := injectTopicsAndRoutings(state, raw); err != nil {
		return err
	}

	return injectAffinitiesAndPreferences(state, raw)
}

// injectConversationHistory parses and sets conversation history.
func injectConversationHistory(state *GuideHandoffState, raw map[string]any) error {
	var err error
	state.ConversationHistory, err = parseHistory(raw)
	return err
}

// injectTopicsAndRoutings parses and sets topics and routings.
func injectTopicsAndRoutings(state *GuideHandoffState, raw map[string]any) error {
	var err error
	state.ActiveTopics, err = parseTopics(raw)
	if err != nil {
		return err
	}

	state.RecentRoutings, err = parseRoutings(raw)
	return err
}

// injectAffinitiesAndPreferences parses and sets affinities and preferences.
func injectAffinitiesAndPreferences(state *GuideHandoffState, raw map[string]any) error {
	var err error
	state.AgentAffinities, err = parseAffinities(raw)
	if err != nil {
		return err
	}

	state.UserPreferences, err = parsePreferences(raw)
	return err
}

// getString safely extracts a string from map.
func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// getFloat safely extracts a float64 from map.
func getFloat(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

// getInt safely extracts an int from map.
func getInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return 0
}

// parseTime parses a time field from raw data.
func parseTime(raw map[string]any, key string) (time.Time, error) {
	if v := getString(raw, key); v != "" {
		return time.Parse(time.RFC3339, v)
	}
	return time.Time{}, nil
}

// parseHistory parses conversation history from raw data.
func parseHistory(raw map[string]any) ([]ConversationMessage, error) {
	arr, ok := raw["conversation_history"].([]any)
	if !ok {
		return nil, nil
	}

	history := make([]ConversationMessage, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		history = append(history, ConversationMessage{
			ID:        getString(m, "id"),
			Role:      getString(m, "role"),
			Summary:   getString(m, "summary"),
			Timestamp: int64(getFloat(m, "timestamp")),
		})
	}

	return history, nil
}

// parseTopics parses active topics from raw data.
func parseTopics(raw map[string]any) ([]TopicState, error) {
	arr, ok := raw["active_topics"].([]any)
	if !ok {
		return nil, nil
	}

	topics := make([]TopicState, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		startedAt, _ := parseTimeFromMap(m, "started_at")
		lastActive, _ := parseTimeFromMap(m, "last_active")

		topics = append(topics, TopicState{
			Name:       getString(m, "name"),
			StartedAt:  startedAt,
			LastActive: lastActive,
			MessageIDs: parseStringSlice(m, "message_ids"),
		})
	}

	return topics, nil
}

// parseTimeFromMap parses time from a nested map.
func parseTimeFromMap(m map[string]any, key string) (time.Time, error) {
	if v := getString(m, key); v != "" {
		return time.Parse(time.RFC3339, v)
	}
	return time.Time{}, nil
}

// parseStringSlice parses a string slice from map.
func parseStringSlice(m map[string]any, key string) []string {
	arr, ok := m[key].([]any)
	if !ok {
		return nil
	}

	result := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}

	return result
}

// parseRoutings parses routing decisions from raw data.
func parseRoutings(raw map[string]any) ([]RoutingDecision, error) {
	arr, ok := raw["recent_routings"].([]any)
	if !ok {
		return nil, nil
	}

	routings := make([]RoutingDecision, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		ts, _ := parseTimeFromMap(m, "timestamp")

		routings = append(routings, RoutingDecision{
			MessageID:   getString(m, "message_id"),
			TargetAgent: getString(m, "target_agent"),
			Confidence:  getFloat(m, "confidence"),
			Reason:      getString(m, "reason"),
			Timestamp:   ts,
		})
	}

	return routings, nil
}

// parseAffinities parses agent affinities from raw data.
func parseAffinities(raw map[string]any) ([]AgentAffinity, error) {
	arr, ok := raw["agent_affinities"].([]any)
	if !ok {
		return nil, nil
	}

	affinities := make([]AgentAffinity, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		affinities = append(affinities, AgentAffinity{
			AgentType:     getString(m, "agent_type"),
			InteractCount: getInt(m, "interact_count"),
			SuccessRate:   getFloat(m, "success_rate"),
			LastUsed:      int64(getFloat(m, "last_used")),
		})
	}

	return affinities, nil
}

// parsePreferences parses user preferences from raw data.
func parsePreferences(raw map[string]any) ([]UserPreference, error) {
	arr, ok := raw["user_preferences"].([]any)
	if !ok {
		return nil, nil
	}

	preferences := make([]UserPreference, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		preferences = append(preferences, UserPreference{
			Key:       getString(m, "key"),
			Value:     getString(m, "value"),
			Source:    getString(m, "source"),
			UpdatedAt: int64(getFloat(m, "updated_at")),
		})
	}

	return preferences, nil
}

// Ensure GuideHandoffState implements ArchivableHandoffState.
var _ ArchivableHandoffState = (*GuideHandoffState)(nil)
