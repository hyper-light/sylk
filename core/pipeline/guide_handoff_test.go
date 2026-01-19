package pipeline_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGuideHandoffState_ImplementsInterface verifies interface compliance.
func TestGuideHandoffState_ImplementsInterface(t *testing.T) {
	t.Run("implements ArchivableHandoffState", func(t *testing.T) {
		state := createTestGuideState()

		// Verify all interface methods exist and return expected types
		assert.Equal(t, "guide-agent-1", state.GetAgentID())
		assert.Equal(t, "guide", state.GetAgentType())
		assert.Equal(t, "session-123", state.GetSessionID())
		assert.Equal(t, "pipeline-456", state.GetPipelineID())
		assert.Equal(t, "context_threshold", state.GetTriggerReason())
		assert.Equal(t, 0.75, state.GetTriggerContext())
		assert.Equal(t, 1, state.GetHandoffIndex())
		assert.False(t, state.GetStartedAt().IsZero())
		assert.False(t, state.GetCompletedAt().IsZero())
	})
}

// TestBuildGuideHandoffState tests the factory method.
func TestBuildGuideHandoffState(t *testing.T) {
	t.Run("creates state with all fields", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:        "guide-001",
			SessionID:      "sess-abc",
			PipelineID:     "pipe-xyz",
			TriggerReason:  "context_threshold",
			TriggerContext: 0.80,
			HandoffIndex:   2,
			StartedAt:      time.Now().Add(-10 * time.Minute),
		}

		history := []pipeline.ConversationMessage{
			{ID: "msg-1", Role: "user", Summary: "Help me", Timestamp: 1000},
			{ID: "msg-2", Role: "assistant", Summary: "Sure", Timestamp: 1001},
		}

		topics := []pipeline.TopicState{
			{Name: "authentication", StartedAt: time.Now(), LastActive: time.Now()},
		}

		routings := []pipeline.RoutingDecision{
			{MessageID: "msg-1", TargetAgent: "engineer", Confidence: 0.9},
		}

		affinities := []pipeline.AgentAffinity{
			{AgentType: "engineer", InteractCount: 5, SuccessRate: 0.9},
		}

		preferences := []pipeline.UserPreference{
			{Key: "verbose", Value: "true", Source: "explicit"},
		}

		state := pipeline.BuildGuideHandoffState(
			config, history, "implement auth", topics, routings, affinities, preferences,
		)

		assert.Equal(t, "guide-001", state.GetAgentID())
		assert.Equal(t, "guide", state.GetAgentType())
		assert.Equal(t, "sess-abc", state.GetSessionID())
		assert.Equal(t, "pipe-xyz", state.GetPipelineID())
		assert.Equal(t, 0.80, state.GetTriggerContext())
		assert.Equal(t, 2, state.GetHandoffIndex())
		assert.Equal(t, "implement auth", state.CurrentIntent)
		assert.Len(t, state.ConversationHistory, 2)
		assert.Len(t, state.ActiveTopics, 1)
		assert.Len(t, state.RecentRoutings, 1)
		assert.Len(t, state.AgentAffinities, 1)
		assert.Len(t, state.UserPreferences, 1)
	})

	t.Run("handles nil slices", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-002",
			SessionID: "sess-nil",
		}

		state := pipeline.BuildGuideHandoffState(config, nil, "", nil, nil, nil, nil)

		assert.Equal(t, "guide-002", state.GetAgentID())
		assert.Nil(t, state.ConversationHistory)
		assert.Nil(t, state.ActiveTopics)
		assert.Nil(t, state.RecentRoutings)
		assert.Nil(t, state.AgentAffinities)
		assert.Nil(t, state.UserPreferences)
	})

	t.Run("creates defensive copy of slices", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{AgentID: "guide-003", SessionID: "sess-copy"}

		history := []pipeline.ConversationMessage{
			{ID: "msg-1", Role: "user"},
		}

		state := pipeline.BuildGuideHandoffState(config, history, "", nil, nil, nil, nil)

		// Modify original
		history[0].ID = "modified"

		// State should be unchanged
		assert.Equal(t, "msg-1", state.ConversationHistory[0].ID)
	})
}

// TestGuideHandoffState_ToJSON tests JSON serialization.
func TestGuideHandoffState_ToJSON(t *testing.T) {
	t.Run("serializes all fields", func(t *testing.T) {
		state := createTestGuideState()

		data, err := state.ToJSON()

		require.NoError(t, err)
		assert.NotEmpty(t, data)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)

		assert.Equal(t, "guide-agent-1", parsed["agent_id"])
		assert.Equal(t, "guide", parsed["agent_type"])
		assert.Equal(t, "session-123", parsed["session_id"])
		assert.Equal(t, "implement authentication", parsed["current_intent"])
		assert.NotNil(t, parsed["conversation_history"])
		assert.NotNil(t, parsed["active_topics"])
		assert.NotNil(t, parsed["recent_routings"])
		assert.NotNil(t, parsed["agent_affinities"])
		assert.NotNil(t, parsed["user_preferences"])
	})

	t.Run("serializes empty state", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-empty",
			SessionID: "sess-empty",
		}
		state := pipeline.BuildGuideHandoffState(config, nil, "", nil, nil, nil, nil)

		data, err := state.ToJSON()

		require.NoError(t, err)
		assert.Contains(t, string(data), `"agent_id":"guide-empty"`)
	})
}

// TestInjectGuideHandoffState tests deserialization.
func TestInjectGuideHandoffState(t *testing.T) {
	t.Run("restores state from JSON", func(t *testing.T) {
		original := createTestGuideState()
		data, err := original.ToJSON()
		require.NoError(t, err)

		restored, err := pipeline.InjectGuideHandoffState(data)

		require.NoError(t, err)
		assert.Equal(t, original.GetAgentID(), restored.GetAgentID())
		assert.Equal(t, original.GetAgentType(), restored.GetAgentType())
		assert.Equal(t, original.GetSessionID(), restored.GetSessionID())
		assert.Equal(t, original.CurrentIntent, restored.CurrentIntent)
		assert.Equal(t, len(original.ConversationHistory), len(restored.ConversationHistory))
		assert.Equal(t, len(original.ActiveTopics), len(restored.ActiveTopics))
		assert.Equal(t, len(original.RecentRoutings), len(restored.RecentRoutings))
		assert.Equal(t, len(original.AgentAffinities), len(restored.AgentAffinities))
		assert.Equal(t, len(original.UserPreferences), len(restored.UserPreferences))
	})

	t.Run("fails on invalid JSON", func(t *testing.T) {
		_, err := pipeline.InjectGuideHandoffState([]byte("not json"))

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal")
	})

	t.Run("handles empty arrays", func(t *testing.T) {
		data := []byte(`{
			"agent_id": "guide-1",
			"agent_type": "guide",
			"session_id": "sess-1",
			"conversation_history": [],
			"active_topics": [],
			"recent_routings": [],
			"agent_affinities": [],
			"user_preferences": []
		}`)

		state, err := pipeline.InjectGuideHandoffState(data)

		require.NoError(t, err)
		assert.Equal(t, "guide-1", state.GetAgentID())
		assert.Empty(t, state.ConversationHistory)
		assert.Empty(t, state.ActiveTopics)
	})

	t.Run("handles missing optional fields", func(t *testing.T) {
		data := []byte(`{
			"agent_id": "guide-minimal",
			"session_id": "sess-minimal"
		}`)

		state, err := pipeline.InjectGuideHandoffState(data)

		require.NoError(t, err)
		assert.Equal(t, "guide-minimal", state.GetAgentID())
		assert.Nil(t, state.ConversationHistory)
		assert.Empty(t, state.CurrentIntent)
	})
}

// TestGuideHandoffState_GetSummary tests summary generation.
func TestGuideHandoffState_GetSummary(t *testing.T) {
	t.Run("includes all relevant info", func(t *testing.T) {
		state := createTestGuideState()

		summary := state.GetSummary()

		assert.Contains(t, summary, "Guide agent handoff")
		assert.Contains(t, summary, "session-123")
		assert.Contains(t, summary, "implement authentication")
		assert.Contains(t, summary, "Active topics")
		assert.Contains(t, summary, "authentication")
		assert.Contains(t, summary, "Recent routing")
		assert.Contains(t, summary, "engineer")
		assert.Contains(t, summary, "Highest affinity")
	})

	t.Run("handles empty state", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-empty",
			SessionID: "sess-empty",
		}
		state := pipeline.BuildGuideHandoffState(config, nil, "", nil, nil, nil, nil)

		summary := state.GetSummary()

		assert.Contains(t, summary, "Guide agent handoff")
		assert.Contains(t, summary, "sess-empty")
		assert.NotContains(t, summary, "Current intent")
		assert.NotContains(t, summary, "Active topics")
	})

	t.Run("handles state with only intent", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-intent",
			SessionID: "sess-intent",
		}
		state := pipeline.BuildGuideHandoffState(config, nil, "fix bug", nil, nil, nil, nil)

		summary := state.GetSummary()

		assert.Contains(t, summary, "fix bug")
	})
}

// TestGuideHandoffState_RoundTrip tests serialize/deserialize cycle.
func TestGuideHandoffState_RoundTrip(t *testing.T) {
	t.Run("preserves all data through round trip", func(t *testing.T) {
		original := createFullTestGuideState()

		// Serialize
		data, err := original.ToJSON()
		require.NoError(t, err)

		// Deserialize
		restored, err := pipeline.InjectGuideHandoffState(data)
		require.NoError(t, err)

		// Verify all fields
		assert.Equal(t, original.GetAgentID(), restored.GetAgentID())
		assert.Equal(t, original.GetAgentType(), restored.GetAgentType())
		assert.Equal(t, original.GetSessionID(), restored.GetSessionID())
		assert.Equal(t, original.GetPipelineID(), restored.GetPipelineID())
		assert.Equal(t, original.GetTriggerReason(), restored.GetTriggerReason())
		assert.Equal(t, original.GetTriggerContext(), restored.GetTriggerContext())
		assert.Equal(t, original.GetHandoffIndex(), restored.GetHandoffIndex())
		assert.Equal(t, original.CurrentIntent, restored.CurrentIntent)

		// Verify nested data
		require.Equal(t, len(original.ConversationHistory), len(restored.ConversationHistory))
		for i := range original.ConversationHistory {
			assert.Equal(t, original.ConversationHistory[i].ID, restored.ConversationHistory[i].ID)
			assert.Equal(t, original.ConversationHistory[i].Role, restored.ConversationHistory[i].Role)
		}

		require.Equal(t, len(original.ActiveTopics), len(restored.ActiveTopics))
		for i := range original.ActiveTopics {
			assert.Equal(t, original.ActiveTopics[i].Name, restored.ActiveTopics[i].Name)
		}

		require.Equal(t, len(original.RecentRoutings), len(restored.RecentRoutings))
		for i := range original.RecentRoutings {
			assert.Equal(t, original.RecentRoutings[i].TargetAgent, restored.RecentRoutings[i].TargetAgent)
			assert.Equal(t, original.RecentRoutings[i].Confidence, restored.RecentRoutings[i].Confidence)
		}

		require.Equal(t, len(original.AgentAffinities), len(restored.AgentAffinities))
		for i := range original.AgentAffinities {
			assert.Equal(t, original.AgentAffinities[i].AgentType, restored.AgentAffinities[i].AgentType)
			assert.Equal(t, original.AgentAffinities[i].InteractCount, restored.AgentAffinities[i].InteractCount)
		}

		require.Equal(t, len(original.UserPreferences), len(restored.UserPreferences))
		for i := range original.UserPreferences {
			assert.Equal(t, original.UserPreferences[i].Key, restored.UserPreferences[i].Key)
			assert.Equal(t, original.UserPreferences[i].Value, restored.UserPreferences[i].Value)
		}
	})
}

// TestGuideHandoffState_EdgeCases tests edge cases.
func TestGuideHandoffState_EdgeCases(t *testing.T) {
	t.Run("handles unicode in intent", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-unicode",
			SessionID: "sess-unicode",
		}
		state := pipeline.BuildGuideHandoffState(config, nil, "fix bug 日本語", nil, nil, nil, nil)

		data, err := state.ToJSON()
		require.NoError(t, err)

		restored, err := pipeline.InjectGuideHandoffState(data)
		require.NoError(t, err)
		assert.Equal(t, "fix bug 日本語", restored.CurrentIntent)
	})

	t.Run("handles empty string intent", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-empty-intent",
			SessionID: "sess-empty-intent",
		}
		state := pipeline.BuildGuideHandoffState(config, nil, "", nil, nil, nil, nil)

		assert.Empty(t, state.CurrentIntent)
		summary := state.GetSummary()
		assert.NotContains(t, summary, "Current intent:")
	})

	t.Run("handles zero values", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:        "guide-zero",
			SessionID:      "sess-zero",
			TriggerContext: 0.0,
			HandoffIndex:   0,
		}
		state := pipeline.BuildGuideHandoffState(config, nil, "", nil, nil, nil, nil)

		assert.Equal(t, 0.0, state.GetTriggerContext())
		assert.Equal(t, 0, state.GetHandoffIndex())
	})

	t.Run("handles large conversation history", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-large",
			SessionID: "sess-large",
		}

		history := make([]pipeline.ConversationMessage, 100)
		for i := range history {
			history[i] = pipeline.ConversationMessage{
				ID:        "msg-" + string(rune('0'+i%10)),
				Role:      "user",
				Summary:   "Message content",
				Timestamp: int64(i),
			}
		}

		state := pipeline.BuildGuideHandoffState(config, history, "", nil, nil, nil, nil)

		data, err := state.ToJSON()
		require.NoError(t, err)

		restored, err := pipeline.InjectGuideHandoffState(data)
		require.NoError(t, err)
		assert.Len(t, restored.ConversationHistory, 100)
	})
}

// TestGuideHandoffState_Affinities tests affinity handling.
func TestGuideHandoffState_Affinities(t *testing.T) {
	t.Run("finds top affinity correctly", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-affinity",
			SessionID: "sess-affinity",
		}

		affinities := []pipeline.AgentAffinity{
			{AgentType: "engineer", InteractCount: 10},
			{AgentType: "designer", InteractCount: 25},
			{AgentType: "tester", InteractCount: 5},
		}

		state := pipeline.BuildGuideHandoffState(config, nil, "", nil, nil, affinities, nil)

		summary := state.GetSummary()
		assert.Contains(t, summary, "designer")
		assert.Contains(t, summary, "25 interactions")
	})

	t.Run("handles single affinity", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-single-aff",
			SessionID: "sess-single-aff",
		}

		affinities := []pipeline.AgentAffinity{
			{AgentType: "inspector", InteractCount: 3},
		}

		state := pipeline.BuildGuideHandoffState(config, nil, "", nil, nil, affinities, nil)

		summary := state.GetSummary()
		assert.Contains(t, summary, "inspector")
	})
}

// TestGuideHandoffState_Topics tests topic handling.
func TestGuideHandoffState_Topics(t *testing.T) {
	t.Run("copies topic message IDs", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-topic",
			SessionID: "sess-topic",
		}

		topics := []pipeline.TopicState{
			{
				Name:       "auth",
				MessageIDs: []string{"msg-1", "msg-2", "msg-3"},
			},
		}

		state := pipeline.BuildGuideHandoffState(config, nil, "", topics, nil, nil, nil)

		// Modify original
		topics[0].MessageIDs[0] = "modified"

		// State should be unchanged
		assert.Equal(t, "msg-1", state.ActiveTopics[0].MessageIDs[0])
	})

	t.Run("handles topics with timestamps", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-topic-ts",
			SessionID: "sess-topic-ts",
		}

		now := time.Now()
		topics := []pipeline.TopicState{
			{
				Name:       "testing",
				StartedAt:  now.Add(-1 * time.Hour),
				LastActive: now,
			},
		}

		state := pipeline.BuildGuideHandoffState(config, nil, "", topics, nil, nil, nil)

		data, err := state.ToJSON()
		require.NoError(t, err)

		restored, err := pipeline.InjectGuideHandoffState(data)
		require.NoError(t, err)

		assert.Equal(t, "testing", restored.ActiveTopics[0].Name)
	})
}

// TestGuideHandoffState_Routings tests routing handling.
func TestGuideHandoffState_Routings(t *testing.T) {
	t.Run("collects unique agents in summary", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-routing",
			SessionID: "sess-routing",
		}

		routings := []pipeline.RoutingDecision{
			{TargetAgent: "engineer"},
			{TargetAgent: "designer"},
			{TargetAgent: "engineer"}, // Duplicate
			{TargetAgent: "tester"},
		}

		state := pipeline.BuildGuideHandoffState(config, nil, "", nil, routings, nil, nil)

		summary := state.GetSummary()
		assert.Contains(t, summary, "engineer")
		assert.Contains(t, summary, "designer")
		assert.Contains(t, summary, "tester")
	})

	t.Run("preserves routing details", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-routing-detail",
			SessionID: "sess-routing-detail",
		}

		routings := []pipeline.RoutingDecision{
			{
				MessageID:   "msg-100",
				TargetAgent: "engineer",
				Confidence:  0.95,
				Reason:      "code-related query",
			},
		}

		state := pipeline.BuildGuideHandoffState(config, nil, "", nil, routings, nil, nil)

		data, err := state.ToJSON()
		require.NoError(t, err)

		restored, err := pipeline.InjectGuideHandoffState(data)
		require.NoError(t, err)

		require.Len(t, restored.RecentRoutings, 1)
		assert.Equal(t, "msg-100", restored.RecentRoutings[0].MessageID)
		assert.Equal(t, 0.95, restored.RecentRoutings[0].Confidence)
		assert.Equal(t, "code-related query", restored.RecentRoutings[0].Reason)
	})
}

// TestGuideHandoffState_Preferences tests preference handling.
func TestGuideHandoffState_Preferences(t *testing.T) {
	t.Run("preserves preference sources", func(t *testing.T) {
		config := pipeline.GuideHandoffConfig{
			AgentID:   "guide-pref",
			SessionID: "sess-pref",
		}

		preferences := []pipeline.UserPreference{
			{Key: "verbose", Value: "true", Source: "explicit", UpdatedAt: 1000},
			{Key: "format", Value: "json", Source: "inferred", UpdatedAt: 2000},
		}

		state := pipeline.BuildGuideHandoffState(config, nil, "", nil, nil, nil, preferences)

		data, err := state.ToJSON()
		require.NoError(t, err)

		restored, err := pipeline.InjectGuideHandoffState(data)
		require.NoError(t, err)

		require.Len(t, restored.UserPreferences, 2)
		assert.Equal(t, "explicit", restored.UserPreferences[0].Source)
		assert.Equal(t, "inferred", restored.UserPreferences[1].Source)
		assert.Equal(t, int64(1000), restored.UserPreferences[0].UpdatedAt)
	})
}

// Helper functions

func createTestGuideState() *pipeline.GuideHandoffState {
	config := pipeline.GuideHandoffConfig{
		AgentID:        "guide-agent-1",
		SessionID:      "session-123",
		PipelineID:     "pipeline-456",
		TriggerReason:  "context_threshold",
		TriggerContext: 0.75,
		HandoffIndex:   1,
		StartedAt:      time.Now().Add(-5 * time.Minute),
	}

	history := []pipeline.ConversationMessage{
		{ID: "msg-1", Role: "user", Summary: "Help with auth", Timestamp: 1000},
		{ID: "msg-2", Role: "assistant", Summary: "I can help", Timestamp: 1001},
	}

	topics := []pipeline.TopicState{
		{Name: "authentication", StartedAt: time.Now(), LastActive: time.Now()},
	}

	routings := []pipeline.RoutingDecision{
		{MessageID: "msg-1", TargetAgent: "engineer", Confidence: 0.9},
	}

	affinities := []pipeline.AgentAffinity{
		{AgentType: "engineer", InteractCount: 10, SuccessRate: 0.9},
	}

	preferences := []pipeline.UserPreference{
		{Key: "verbose", Value: "true", Source: "explicit"},
	}

	return pipeline.BuildGuideHandoffState(
		config, history, "implement authentication", topics, routings, affinities, preferences,
	)
}

func createFullTestGuideState() *pipeline.GuideHandoffState {
	config := pipeline.GuideHandoffConfig{
		AgentID:        "guide-full",
		SessionID:      "session-full",
		PipelineID:     "pipeline-full",
		TriggerReason:  "context_threshold",
		TriggerContext: 0.85,
		HandoffIndex:   3,
		StartedAt:      time.Now().Add(-15 * time.Minute),
	}

	history := []pipeline.ConversationMessage{
		{ID: "msg-1", Role: "user", Summary: "Start project", Timestamp: 1000},
		{ID: "msg-2", Role: "assistant", Summary: "Starting", Timestamp: 1001},
		{ID: "msg-3", Role: "user", Summary: "Add auth", Timestamp: 1002},
		{ID: "msg-4", Role: "assistant", Summary: "Adding auth", Timestamp: 1003},
	}

	topics := []pipeline.TopicState{
		{
			Name:       "project-setup",
			StartedAt:  time.Now().Add(-10 * time.Minute),
			LastActive: time.Now().Add(-5 * time.Minute),
			MessageIDs: []string{"msg-1", "msg-2"},
		},
		{
			Name:       "authentication",
			StartedAt:  time.Now().Add(-5 * time.Minute),
			LastActive: time.Now(),
			MessageIDs: []string{"msg-3", "msg-4"},
		},
	}

	routings := []pipeline.RoutingDecision{
		{MessageID: "msg-1", TargetAgent: "architect", Confidence: 0.8, Reason: "planning"},
		{MessageID: "msg-3", TargetAgent: "engineer", Confidence: 0.95, Reason: "implementation"},
	}

	affinities := []pipeline.AgentAffinity{
		{AgentType: "engineer", InteractCount: 15, SuccessRate: 0.92, LastUsed: 1000},
		{AgentType: "architect", InteractCount: 5, SuccessRate: 0.85, LastUsed: 900},
		{AgentType: "designer", InteractCount: 3, SuccessRate: 0.90, LastUsed: 800},
	}

	preferences := []pipeline.UserPreference{
		{Key: "verbose", Value: "true", Source: "explicit", UpdatedAt: 1000},
		{Key: "format", Value: "detailed", Source: "inferred", UpdatedAt: 1100},
	}

	return pipeline.BuildGuideHandoffState(
		config, history, "implement OAuth authentication", topics, routings, affinities, preferences,
	)
}
