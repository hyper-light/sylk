package context

import (
	"testing"
	"time"
)

// =============================================================================
// Build Payload Tests
// =============================================================================

func TestBuildAdaptiveHandoffPayload_NilInputs(t *testing.T) {
	t.Parallel()

	payload := BuildAdaptiveHandoffPayload(nil, nil, "agent-1", "engineer", "session-1")

	if payload == nil {
		t.Fatal("expected non-nil payload")
	}
	if payload.SourceAgentID != "agent-1" {
		t.Errorf("SourceAgentID = %s, want agent-1", payload.SourceAgentID)
	}
	if payload.SourceAgentType != "engineer" {
		t.Errorf("SourceAgentType = %s, want engineer", payload.SourceAgentType)
	}
	if payload.SessionID != "session-1" {
		t.Errorf("SessionID = %s, want session-1", payload.SessionID)
	}
	if payload.HandoffTime.IsZero() {
		t.Error("HandoffTime should be set")
	}
}

func TestBuildAdaptiveHandoffPayload_WithState(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()
	// Perform an update to set some observations
	obs := EpisodeObservation{
		TaskCompleted: true,
		Timestamp:     time.Now(),
	}
	state.UpdateFromOutcome(obs)

	payload := BuildAdaptiveHandoffPayload(state, nil, "agent-1", "engineer", "session-1")

	if payload.TotalObservations != 1 {
		t.Errorf("TotalObservations = %d, want 1", payload.TotalObservations)
	}
}

func TestBuildAdaptiveHandoffPayload_WithHotCache(t *testing.T) {
	t.Parallel()

	cache := NewHotCache(HotCacheConfig{MaxSize: 1024 * 1024})
	cache.Add("content-1", &ContentEntry{ID: "content-1", Content: "data1"})
	cache.Add("content-2", &ContentEntry{ID: "content-2", Content: "data2"})

	payload := BuildAdaptiveHandoffPayload(nil, cache, "agent-1", "engineer", "session-1")

	if len(payload.HotContentIDs) != 2 {
		t.Errorf("HotContentIDs length = %d, want 2", len(payload.HotContentIDs))
	}
}

func TestBuildAdaptiveHandoffPayload_WithDiscoveredContexts(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()
	discovery := state.GetContextDiscovery()
	discovery.AddOrUpdateCentroid(ContextCentroid{
		ID:    TaskContext("code_review"),
		Bias:  ContextBias{RelevanceMult: 1.2},
		Count: 100,
	})
	discovery.AddOrUpdateCentroid(ContextCentroid{
		ID:    TaskContext("debugging"),
		Bias:  ContextBias{RelevanceMult: 1.1},
		Count: 50,
	})

	payload := BuildAdaptiveHandoffPayload(state, nil, "agent-1", "engineer", "session-1")

	if len(payload.DiscoveredContexts) != 2 {
		t.Errorf("DiscoveredContexts length = %d, want 2", len(payload.DiscoveredContexts))
	}
}

func TestBuildAdaptiveHandoffPayload_LimitsContextsToTen(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()
	discovery := state.GetContextDiscovery()
	discovery.MaxContexts = 20 // Allow more to be added

	// Add 15 contexts
	for i := 0; i < 15; i++ {
		discovery.AddOrUpdateCentroid(ContextCentroid{
			ID:    TaskContext("context-" + string(rune('a'+i))),
			Count: int64(i + 1),
		})
	}

	payload := BuildAdaptiveHandoffPayload(state, nil, "agent-1", "engineer", "session-1")

	if len(payload.DiscoveredContexts) != 10 {
		t.Errorf("DiscoveredContexts length = %d, want 10 (capped)", len(payload.DiscoveredContexts))
	}
}

// =============================================================================
// Apply Payload Tests
// =============================================================================

func TestApplyAdaptiveHandoffPayload_NilPayload(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()
	err := ApplyAdaptiveHandoffPayload(nil, state)

	if err != nil {
		t.Errorf("expected no error for nil payload, got %v", err)
	}
}

func TestApplyAdaptiveHandoffPayload_NilState(t *testing.T) {
	t.Parallel()

	payload := &AdaptiveHandoffPayload{}
	err := ApplyAdaptiveHandoffPayload(payload, nil)

	if err != nil {
		t.Errorf("expected no error for nil state, got %v", err)
	}
}

func TestApplyAdaptiveHandoffPayload_TransfersWeights(t *testing.T) {
	t.Parallel()

	payload := &AdaptiveHandoffPayload{
		MeanWeights: RewardWeights{
			TaskSuccess:     0.9,
			RelevanceBonus:  0.5,
			StrugglePenalty: 0.3,
			WastePenalty:    0.2,
		},
	}

	state := NewAdaptiveState()
	err := ApplyAdaptiveHandoffPayload(payload, state)

	if err != nil {
		t.Fatalf("ApplyAdaptiveHandoffPayload error = %v", err)
	}

	weights := state.GetMeanWeights()
	if weights.TaskSuccess < 0.8 || weights.TaskSuccess > 1.0 {
		t.Errorf("TaskSuccess = %f, want ~0.9", weights.TaskSuccess)
	}
}

func TestApplyAdaptiveHandoffPayload_TransfersThresholds(t *testing.T) {
	t.Parallel()

	payload := &AdaptiveHandoffPayload{
		MeanThresholds: ThresholdConfig{
			Confidence: 0.85,
			Excerpt:    0.90,
			Budget:     0.15,
		},
	}

	state := NewAdaptiveState()
	err := ApplyAdaptiveHandoffPayload(payload, state)

	if err != nil {
		t.Fatalf("ApplyAdaptiveHandoffPayload error = %v", err)
	}

	thresholds := state.GetMeanThresholds()
	if thresholds.Confidence < 0.80 || thresholds.Confidence > 0.90 {
		t.Errorf("Confidence = %f, want ~0.85", thresholds.Confidence)
	}
}

func TestApplyAdaptiveHandoffPayload_TransfersUserProfile(t *testing.T) {
	t.Parallel()

	payload := &AdaptiveHandoffPayload{
		UserProfile: UserWeightProfile{
			WastePenaltyMult:    2.0,
			StrugglePenaltyMult: 1.5,
		},
	}

	state := NewAdaptiveState()
	err := ApplyAdaptiveHandoffPayload(payload, state)

	if err != nil {
		t.Fatalf("ApplyAdaptiveHandoffPayload error = %v", err)
	}

	profile := state.GetUserProfile()
	if profile.WastePenaltyMult != 2.0 {
		t.Errorf("WastePenaltyMult = %f, want 2.0", profile.WastePenaltyMult)
	}
}

func TestApplyAdaptiveHandoffPayload_TransfersContexts(t *testing.T) {
	t.Parallel()

	payload := &AdaptiveHandoffPayload{
		DiscoveredContexts: []DiscoveredContextEntry{
			{ID: "code_review", Bias: ContextBias{RelevanceMult: 1.2}, Count: 100},
			{ID: "debugging", Bias: ContextBias{RelevanceMult: 1.1}, Count: 50},
		},
	}

	state := NewAdaptiveState()
	err := ApplyAdaptiveHandoffPayload(payload, state)

	if err != nil {
		t.Fatalf("ApplyAdaptiveHandoffPayload error = %v", err)
	}

	discovery := state.GetContextDiscovery()
	if len(discovery.Centroids) != 2 {
		t.Errorf("Centroids length = %d, want 2", len(discovery.Centroids))
	}
}

// =============================================================================
// Round-trip Tests
// =============================================================================

func TestAdaptiveHandoffPayload_RoundTrip(t *testing.T) {
	t.Parallel()

	// Build original state
	originalState := NewAdaptiveState()
	discovery := originalState.GetContextDiscovery()
	discovery.AddOrUpdateCentroid(ContextCentroid{
		ID:    TaskContext("code_review"),
		Bias:  ContextBias{RelevanceMult: 1.2, StruggleMult: 0.8},
		Count: 100,
	})

	// Perform updates to build history
	for i := 0; i < 5; i++ {
		originalState.UpdateFromOutcome(EpisodeObservation{
			TaskCompleted: true,
			Timestamp:     time.Now(),
		})
	}

	hotCache := NewHotCache(HotCacheConfig{MaxSize: 1024 * 1024})
	hotCache.Add("content-1", &ContentEntry{ID: "content-1", Content: "data"})

	// Build payload
	payload := BuildAdaptiveHandoffPayload(
		originalState, hotCache,
		"agent-1", "engineer", "session-1",
	)

	// Serialize and deserialize
	data, err := MarshalAdaptiveHandoffPayload(payload)
	if err != nil {
		t.Fatalf("MarshalAdaptiveHandoffPayload error = %v", err)
	}

	restored, err := UnmarshalAdaptiveHandoffPayload(data)
	if err != nil {
		t.Fatalf("UnmarshalAdaptiveHandoffPayload error = %v", err)
	}

	// Apply to new state
	newState := NewAdaptiveState()
	err = ApplyAdaptiveHandoffPayload(restored, newState)
	if err != nil {
		t.Fatalf("ApplyAdaptiveHandoffPayload error = %v", err)
	}

	// Verify contexts were transferred
	newDiscovery := newState.GetContextDiscovery()
	if len(newDiscovery.Centroids) != 1 {
		t.Errorf("Centroids length = %d, want 1", len(newDiscovery.Centroids))
	}
}

// =============================================================================
// Serialization Tests
// =============================================================================

func TestMarshalAdaptiveHandoffPayload(t *testing.T) {
	t.Parallel()

	payload := &AdaptiveHandoffPayload{
		SourceAgentID:   "agent-1",
		SourceAgentType: "engineer",
		SessionID:       "session-1",
		HandoffTime:     time.Now(),
	}

	data, err := MarshalAdaptiveHandoffPayload(payload)

	if err != nil {
		t.Fatalf("MarshalAdaptiveHandoffPayload error = %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty data")
	}
}

func TestUnmarshalAdaptiveHandoffPayload(t *testing.T) {
	t.Parallel()

	jsonData := `{
		"source_agent_id": "agent-1",
		"source_agent_type": "engineer",
		"session_id": "session-1",
		"total_observations": 42
	}`

	payload, err := UnmarshalAdaptiveHandoffPayload([]byte(jsonData))

	if err != nil {
		t.Fatalf("UnmarshalAdaptiveHandoffPayload error = %v", err)
	}
	if payload.SourceAgentID != "agent-1" {
		t.Errorf("SourceAgentID = %s, want agent-1", payload.SourceAgentID)
	}
	if payload.TotalObservations != 42 {
		t.Errorf("TotalObservations = %d, want 42", payload.TotalObservations)
	}
}

func TestUnmarshalAdaptiveHandoffPayload_InvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := UnmarshalAdaptiveHandoffPayload([]byte("not json"))

	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// =============================================================================
// Hot Cache Hints Tests
// =============================================================================

func TestGetAdaptiveHotCacheHints_NilPayload(t *testing.T) {
	t.Parallel()

	hints := GetAdaptiveHotCacheHints(nil)

	if hints != nil {
		t.Errorf("expected nil for nil payload, got %v", hints)
	}
}

func TestGetAdaptiveHotCacheHints_WithIDs(t *testing.T) {
	t.Parallel()

	payload := &AdaptiveHandoffPayload{
		HotContentIDs: []string{"content-1", "content-2"},
	}

	hints := GetAdaptiveHotCacheHints(payload)

	if len(hints) != 2 {
		t.Errorf("hints length = %d, want 2", len(hints))
	}
}

// =============================================================================
// Summary Generation Tests
// =============================================================================

func TestGetAdaptivePayloadSummary_NilPayload(t *testing.T) {
	t.Parallel()

	summary := GetAdaptivePayloadSummary(nil)

	if summary != "" {
		t.Errorf("expected empty summary for nil payload, got %s", summary)
	}
}

func TestGetAdaptivePayloadSummary_BasicPayload(t *testing.T) {
	t.Parallel()

	payload := &AdaptiveHandoffPayload{
		SourceAgentType:   "engineer",
		SessionID:         "session-1",
		TotalObservations: 5,
	}

	summary := GetAdaptivePayloadSummary(payload)

	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if !containsSubstring(summary, "engineer") {
		t.Error("summary should contain agent type")
	}
	if !containsSubstring(summary, "session-1") {
		t.Error("summary should contain session ID")
	}
}

func TestGetAdaptivePayloadSummary_WithContexts(t *testing.T) {
	t.Parallel()

	payload := &AdaptiveHandoffPayload{
		SourceAgentType: "engineer",
		SessionID:       "session-1",
		DiscoveredContexts: []DiscoveredContextEntry{
			{ID: "ctx1"},
			{ID: "ctx2"},
		},
	}

	summary := GetAdaptivePayloadSummary(payload)

	if !containsSubstring(summary, "Contexts") {
		t.Error("summary should mention contexts")
	}
}

func TestGetAdaptivePayloadSummary_WithHotCache(t *testing.T) {
	t.Parallel()

	payload := &AdaptiveHandoffPayload{
		SourceAgentType: "engineer",
		SessionID:       "session-1",
		HotContentIDs:   []string{"c1", "c2", "c3"},
	}

	summary := GetAdaptivePayloadSummary(payload)

	if !containsSubstring(summary, "Hot entries") {
		t.Error("summary should mention hot entries")
	}
}

// =============================================================================
// Format Helper Tests
// =============================================================================

func TestFormatAdaptiveInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{5, "5"},
		{9, "9"},
		{10, "10+"},
		{100, "10+"},
	}

	for _, tt := range tests {
		got := formatAdaptiveInt(tt.input)
		if got != tt.want {
			t.Errorf("formatAdaptiveInt(%d) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestFormatAdaptiveInt64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{9, "9"},
		{10, "10"},
		{42, "42"},
		{99, "99"},
		{100, "100+"},
		{1000, "100+"},
	}

	for _, tt := range tests {
		got := formatAdaptiveInt64(tt.input)
		if got != tt.want {
			t.Errorf("formatAdaptiveInt64(%d) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

// =============================================================================
// ContextDiscovery AddOrUpdateCentroid Tests
// =============================================================================

func TestContextDiscovery_AddOrUpdateCentroid_Add(t *testing.T) {
	t.Parallel()

	discovery := &ContextDiscovery{MaxContexts: 10}
	centroid := ContextCentroid{
		ID:    TaskContext("test"),
		Count: 5,
	}

	discovery.AddOrUpdateCentroid(centroid)

	if len(discovery.Centroids) != 1 {
		t.Errorf("Centroids length = %d, want 1", len(discovery.Centroids))
	}
	if discovery.Centroids[0].ID != "test" {
		t.Errorf("ID = %s, want test", discovery.Centroids[0].ID)
	}
}

func TestContextDiscovery_AddOrUpdateCentroid_Update(t *testing.T) {
	t.Parallel()

	discovery := &ContextDiscovery{MaxContexts: 10}
	discovery.Centroids = []ContextCentroid{
		{ID: "test", Count: 5},
	}

	discovery.AddOrUpdateCentroid(ContextCentroid{
		ID:    "test",
		Count: 10,
	})

	if len(discovery.Centroids) != 1 {
		t.Errorf("Centroids length = %d, want 1", len(discovery.Centroids))
	}
	if discovery.Centroids[0].Count != 10 {
		t.Errorf("Count = %d, want 10", discovery.Centroids[0].Count)
	}
}

func TestContextDiscovery_AddOrUpdateCentroid_AtCapacity(t *testing.T) {
	t.Parallel()

	discovery := &ContextDiscovery{MaxContexts: 2}
	discovery.Centroids = []ContextCentroid{
		{ID: "one"},
		{ID: "two"},
	}

	discovery.AddOrUpdateCentroid(ContextCentroid{ID: "three"})

	if len(discovery.Centroids) != 2 {
		t.Errorf("Centroids length = %d, want 2 (at capacity)", len(discovery.Centroids))
	}
}

// Helper function
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
