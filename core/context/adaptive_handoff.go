// Package context provides AR.13.2: Handoff Context Integration - bridges adaptive
// retrieval state with pipeline agent handoff for state preservation.
package context

import (
	"encoding/json"
	"time"
)

// =============================================================================
// Adaptive Handoff Payload
// =============================================================================

// AdaptiveHandoffPayload contains adaptive retrieval state for transfer during handoff.
type AdaptiveHandoffPayload struct {
	// AdaptiveState snapshot
	MeanWeights    RewardWeights     `json:"mean_weights"`
	MeanThresholds ThresholdConfig   `json:"mean_thresholds"`
	UserProfile    UserWeightProfile `json:"user_profile"`

	// Discovered contexts (max 10)
	DiscoveredContexts []DiscoveredContextEntry `json:"discovered_contexts"`

	// Hot cache content IDs for pre-warming
	HotContentIDs []string `json:"hot_content_ids"`

	// Metadata
	SourceAgentID     string    `json:"source_agent_id"`
	SourceAgentType   string    `json:"source_agent_type"`
	SessionID         string    `json:"session_id"`
	HandoffTime       time.Time `json:"handoff_time"`
	TotalObservations int64     `json:"total_observations"`
}

// DiscoveredContextEntry represents a discovered context for serialization.
type DiscoveredContextEntry struct {
	ID    TaskContext `json:"id"`
	Bias  ContextBias `json:"bias"`
	Count int64       `json:"count"`
}

// =============================================================================
// Build Payload
// =============================================================================

// BuildAdaptiveHandoffPayload creates an AdaptiveHandoffPayload from current state.
func BuildAdaptiveHandoffPayload(
	state *AdaptiveState,
	hotCache *HotCache,
	agentID string,
	agentType string,
	sessionID string,
) *AdaptiveHandoffPayload {
	payload := &AdaptiveHandoffPayload{
		SourceAgentID:   agentID,
		SourceAgentType: agentType,
		SessionID:       sessionID,
		HandoffTime:     time.Now(),
	}

	if state != nil {
		buildAdaptiveStatePayload(payload, state)
	}

	if hotCache != nil {
		buildAdaptiveHotCachePayload(payload, hotCache)
	}

	return payload
}

func buildAdaptiveStatePayload(payload *AdaptiveHandoffPayload, state *AdaptiveState) {
	payload.MeanWeights = state.GetMeanWeights()
	payload.MeanThresholds = state.GetMeanThresholds()
	payload.UserProfile = state.GetUserProfile()
	payload.TotalObservations = state.GetTotalObservations()

	buildAdaptiveDiscoveredContexts(payload, state)
}

func buildAdaptiveDiscoveredContexts(payload *AdaptiveHandoffPayload, state *AdaptiveState) {
	discovery := state.GetContextDiscovery()
	if discovery == nil {
		return
	}

	maxContexts := 10
	centroids := discovery.Centroids
	if len(centroids) > maxContexts {
		centroids = centroids[:maxContexts]
	}

	payload.DiscoveredContexts = make([]DiscoveredContextEntry, len(centroids))
	for i, c := range centroids {
		payload.DiscoveredContexts[i] = DiscoveredContextEntry{
			ID:    c.ID,
			Bias:  c.Bias,
			Count: c.Count,
		}
	}
}

func buildAdaptiveHotCachePayload(payload *AdaptiveHandoffPayload, hotCache *HotCache) {
	payload.HotContentIDs = hotCache.Keys()
}

// =============================================================================
// Apply Payload
// =============================================================================

// ApplyAdaptiveHandoffPayload restores adaptive state from a handoff payload.
// Note: This updates the new agent's adaptive state with learned parameters.
// Hot cache pre-warming is a hint; actual content fetching happens on demand.
func ApplyAdaptiveHandoffPayload(
	payload *AdaptiveHandoffPayload,
	newState *AdaptiveState,
) error {
	if payload == nil || newState == nil {
		return nil
	}

	applyAdaptiveStateFromPayload(payload, newState)
	return nil
}

func applyAdaptiveStateFromPayload(payload *AdaptiveHandoffPayload, state *AdaptiveState) {
	state.SetMeanWeights(payload.MeanWeights)
	state.SetMeanThresholds(payload.MeanThresholds)
	state.SetUserProfile(payload.UserProfile)

	applyAdaptiveDiscoveredContexts(payload, state)
}

func applyAdaptiveDiscoveredContexts(payload *AdaptiveHandoffPayload, state *AdaptiveState) {
	if len(payload.DiscoveredContexts) == 0 {
		return
	}

	discovery := state.GetContextDiscovery()
	if discovery == nil {
		return
	}

	for _, ctx := range payload.DiscoveredContexts {
		discovery.AddOrUpdateCentroid(ContextCentroid{
			ID:    ctx.ID,
			Bias:  ctx.Bias,
			Count: ctx.Count,
		})
	}
}

// GetAdaptiveHotCacheHints returns the content IDs that should be pre-warmed.
func GetAdaptiveHotCacheHints(payload *AdaptiveHandoffPayload) []string {
	if payload == nil {
		return nil
	}
	return payload.HotContentIDs
}

// =============================================================================
// Serialization
// =============================================================================

// MarshalAdaptiveHandoffPayload serializes an AdaptiveHandoffPayload to JSON.
func MarshalAdaptiveHandoffPayload(payload *AdaptiveHandoffPayload) ([]byte, error) {
	return json.Marshal(payload)
}

// UnmarshalAdaptiveHandoffPayload deserializes an AdaptiveHandoffPayload from JSON.
func UnmarshalAdaptiveHandoffPayload(data []byte) (*AdaptiveHandoffPayload, error) {
	var payload AdaptiveHandoffPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// =============================================================================
// Summary Generation
// =============================================================================

// GetAdaptivePayloadSummary generates a text summary of the handoff payload.
func GetAdaptivePayloadSummary(payload *AdaptiveHandoffPayload) string {
	if payload == nil {
		return ""
	}

	return buildAdaptivePayloadSummary(payload)
}

func buildAdaptivePayloadSummary(payload *AdaptiveHandoffPayload) string {
	summary := "Agent handoff: " + payload.SourceAgentType +
		" in session " + payload.SessionID

	summary += buildAdaptiveWeightsSummary(payload)
	summary += buildAdaptiveContextsSummary(payload)
	summary += buildAdaptiveHotCacheSummary(payload)

	return summary
}

func buildAdaptiveWeightsSummary(payload *AdaptiveHandoffPayload) string {
	return " | Observations: " + formatAdaptiveInt64(payload.TotalObservations)
}

func buildAdaptiveContextsSummary(payload *AdaptiveHandoffPayload) string {
	if len(payload.DiscoveredContexts) == 0 {
		return ""
	}
	return " | Contexts: " + formatAdaptiveInt(len(payload.DiscoveredContexts))
}

func buildAdaptiveHotCacheSummary(payload *AdaptiveHandoffPayload) string {
	if len(payload.HotContentIDs) == 0 {
		return ""
	}
	return " | Hot entries: " + formatAdaptiveInt(len(payload.HotContentIDs))
}

func formatAdaptiveInt(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 10 {
		return string(rune('0' + i))
	}
	return "10+"
}

func formatAdaptiveInt64(i int64) string {
	if i == 0 {
		return "0"
	}
	if i < 10 {
		return string(rune('0' + int(i)))
	}
	if i < 100 {
		return string(rune('0'+int(i/10))) + string(rune('0'+int(i%10)))
	}
	return "100+"
}
