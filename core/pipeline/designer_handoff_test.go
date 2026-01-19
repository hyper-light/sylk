package pipeline

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildHandoffState(t *testing.T) {
	t.Parallel()

	startTime := time.Now().Add(-time.Hour)
	cfg := DesignerHandoffConfig{
		AgentID:        "designer-001",
		SessionID:      "session-abc",
		PipelineID:     "pipeline-xyz",
		TriggerReason:  "gp_degradation_detected",
		TriggerContext: 0.75,
		HandoffIndex:   1,
		StartedAt:      startTime,
	}

	state := BuildHandoffState(cfg)

	assert.Equal(t, "designer-001", state.GetAgentID())
	assert.Equal(t, "designer", state.GetAgentType())
	assert.Equal(t, "session-abc", state.GetSessionID())
	assert.Equal(t, "pipeline-xyz", state.GetPipelineID())
	assert.Equal(t, "gp_degradation_detected", state.GetTriggerReason())
	assert.Equal(t, 0.75, state.GetTriggerContext())
	assert.Equal(t, 1, state.GetHandoffIndex())
	assert.Equal(t, startTime, state.GetStartedAt())
	assert.NotZero(t, state.GetCompletedAt())

	assert.NotNil(t, state.Accomplished)
	assert.NotNil(t, state.ComponentsCreated)
	assert.NotNil(t, state.ComponentsModified)
	assert.NotNil(t, state.TokensUsed)
	assert.NotNil(t, state.Remaining)
	assert.Empty(t, state.Accomplished)
	assert.Empty(t, state.ComponentsCreated)
}

func TestBuildHandoffState_ZeroValues(t *testing.T) {
	t.Parallel()

	state := BuildHandoffState(DesignerHandoffConfig{})

	assert.Empty(t, state.GetAgentID())
	assert.Equal(t, "designer", state.GetAgentType())
	assert.Empty(t, state.GetSessionID())
	assert.Equal(t, 0, state.GetHandoffIndex())
	assert.NotNil(t, state.Accomplished)
}

func TestInjectHandoffState_Success(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	completedTime := time.Date(2025, 1, 15, 11, 0, 0, 0, time.UTC)

	originalState := BuildHandoffState(DesignerHandoffConfig{
		AgentID:        "designer-002",
		SessionID:      "session-def",
		PipelineID:     "pipeline-uvw",
		TriggerReason:  "context_threshold",
		TriggerContext: 0.80,
		HandoffIndex:   2,
		StartedAt:      startTime,
	})
	originalState.completedAt = completedTime
	originalState.SetOriginalPrompt("Create a dashboard component")
	originalState.AddAccomplished("Created header layout")
	originalState.AddComponentCreated(ComponentInfo{
		Name:    "Dashboard",
		Path:    "src/components/Dashboard.tsx",
		Type:    "component",
		Props:   []string{"title", "data"},
		Exports: []string{"Dashboard", "DashboardProps"},
	})
	originalState.AddComponentModified(ComponentChange{
		Name:        "Header",
		Path:        "src/components/Header.tsx",
		ChangeType:  "styling",
		Description: "Updated padding and colors",
	})
	originalState.AddTokenUsed(TokenUsage{
		TokenName: "--color-primary",
		Category:  "color",
		UsedIn:    "src/components/Dashboard.tsx",
	})
	originalState.AddRemaining("Add responsive breakpoints")
	originalState.SetContextNotes("Using design system v2")

	jsonData, err := originalState.ToJSON()
	require.NoError(t, err)

	restoredState, err := InjectHandoffState(jsonData)
	require.NoError(t, err)

	assert.Equal(t, originalState.GetAgentID(), restoredState.GetAgentID())
	assert.Equal(t, originalState.GetAgentType(), restoredState.GetAgentType())
	assert.Equal(t, originalState.GetSessionID(), restoredState.GetSessionID())
	assert.Equal(t, originalState.GetPipelineID(), restoredState.GetPipelineID())
	assert.Equal(t, originalState.GetTriggerReason(), restoredState.GetTriggerReason())
	assert.Equal(t, originalState.GetTriggerContext(), restoredState.GetTriggerContext())
	assert.Equal(t, originalState.GetHandoffIndex(), restoredState.GetHandoffIndex())
	assert.True(t, originalState.GetStartedAt().Equal(restoredState.GetStartedAt()))
	assert.True(t, originalState.GetCompletedAt().Equal(restoredState.GetCompletedAt()))

	assert.Equal(t, originalState.OriginalPrompt, restoredState.OriginalPrompt)
	assert.Equal(t, originalState.Accomplished, restoredState.Accomplished)
	assert.Equal(t, originalState.ComponentsCreated, restoredState.ComponentsCreated)
	assert.Equal(t, originalState.ComponentsModified, restoredState.ComponentsModified)
	assert.Equal(t, originalState.TokensUsed, restoredState.TokensUsed)
	assert.Equal(t, originalState.Remaining, restoredState.Remaining)
	assert.Equal(t, originalState.ContextNotes, restoredState.ContextNotes)
}

func TestInjectHandoffState_EmptyData(t *testing.T) {
	t.Parallel()

	state, err := InjectHandoffState([]byte{})

	assert.Nil(t, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty handoff state data")
}

func TestInjectHandoffState_InvalidJSON(t *testing.T) {
	t.Parallel()

	state, err := InjectHandoffState([]byte("not valid json"))

	assert.Nil(t, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal")
}

func TestInjectHandoffState_NilSlicesInitialized(t *testing.T) {
	t.Parallel()

	minimalJSON := `{"agent_id":"test","agent_type":"designer"}`

	state, err := InjectHandoffState([]byte(minimalJSON))
	require.NoError(t, err)

	assert.NotNil(t, state.Accomplished)
	assert.NotNil(t, state.ComponentsCreated)
	assert.NotNil(t, state.ComponentsModified)
	assert.NotNil(t, state.TokensUsed)
	assert.NotNil(t, state.Remaining)
}

func TestToJSON_ValidStructure(t *testing.T) {
	t.Parallel()

	state := BuildHandoffState(DesignerHandoffConfig{
		AgentID:      "designer-test",
		SessionID:    "session-test",
		HandoffIndex: 5,
	})
	state.SetOriginalPrompt("Test prompt")

	jsonData, err := state.ToJSON()
	require.NoError(t, err)

	var envelope map[string]any
	err = json.Unmarshal(jsonData, &envelope)
	require.NoError(t, err)

	assert.Equal(t, "designer-test", envelope["agent_id"])
	assert.Equal(t, "designer", envelope["agent_type"])
	assert.Equal(t, "session-test", envelope["session_id"])
	assert.Equal(t, float64(5), envelope["handoff_index"])
	assert.Equal(t, "Test prompt", envelope["original_prompt"])
}

func TestGetSummary_FullState(t *testing.T) {
	t.Parallel()

	state := BuildHandoffState(DesignerHandoffConfig{HandoffIndex: 3})
	state.SetOriginalPrompt("Build a user profile page with avatar upload")
	state.AddAccomplished("Created profile layout")
	state.AddAccomplished("Implemented avatar component")
	state.AddComponentCreated(ComponentInfo{Name: "ProfilePage"})
	state.AddComponentCreated(ComponentInfo{Name: "AvatarUpload"})
	state.AddComponentModified(ComponentChange{Name: "UserCard"})
	state.AddRemaining("Add form validation")

	summary := state.GetSummary()

	assert.Contains(t, summary, "Designer handoff #3")
	assert.Contains(t, summary, "Prompt:")
	assert.Contains(t, summary, "Done:")
	assert.Contains(t, summary, "Created: ProfilePage, AvatarUpload")
	assert.Contains(t, summary, "Modified: UserCard")
	assert.Contains(t, summary, "Remaining:")
}

func TestGetSummary_MinimalState(t *testing.T) {
	t.Parallel()

	state := BuildHandoffState(DesignerHandoffConfig{HandoffIndex: 1})

	summary := state.GetSummary()

	assert.Equal(t, "Designer handoff #1", summary)
}

func TestGetSummary_LongPromptTruncated(t *testing.T) {
	t.Parallel()

	state := BuildHandoffState(DesignerHandoffConfig{HandoffIndex: 1})
	longPrompt := strings.Repeat("a", 150)
	state.SetOriginalPrompt(longPrompt)

	summary := state.GetSummary()

	assert.Contains(t, summary, "...")
	assert.Less(t, len(summary), 150+50)
}

func TestAddMethods(t *testing.T) {
	t.Parallel()

	state := BuildHandoffState(DesignerHandoffConfig{})

	state.AddAccomplished("task1")
	state.AddAccomplished("task2")
	assert.Len(t, state.Accomplished, 2)

	state.AddComponentCreated(ComponentInfo{Name: "Comp1"})
	assert.Len(t, state.ComponentsCreated, 1)

	state.AddComponentModified(ComponentChange{Name: "ModComp"})
	assert.Len(t, state.ComponentsModified, 1)

	state.AddTokenUsed(TokenUsage{TokenName: "token1"})
	assert.Len(t, state.TokensUsed, 1)

	state.AddRemaining("remaining1")
	assert.Len(t, state.Remaining, 1)
}

func TestSetMethods(t *testing.T) {
	t.Parallel()

	state := BuildHandoffState(DesignerHandoffConfig{})

	state.SetOriginalPrompt("new prompt")
	assert.Equal(t, "new prompt", state.OriginalPrompt)

	state.SetContextNotes("important notes")
	assert.Equal(t, "important notes", state.ContextNotes)
}

func TestArchivableHandoffStateInterface(t *testing.T) {
	t.Parallel()

	var _ ArchivableHandoffState = (*DesignerHandoffState)(nil)

	state := BuildHandoffState(DesignerHandoffConfig{
		AgentID:        "agent-123",
		SessionID:      "session-456",
		PipelineID:     "pipeline-789",
		TriggerReason:  "quality_degradation",
		TriggerContext: 0.85,
		HandoffIndex:   7,
		StartedAt:      time.Now(),
	})

	var iface ArchivableHandoffState = state

	assert.Equal(t, "agent-123", iface.GetAgentID())
	assert.Equal(t, "designer", iface.GetAgentType())
	assert.Equal(t, "session-456", iface.GetSessionID())
	assert.Equal(t, "pipeline-789", iface.GetPipelineID())
	assert.Equal(t, "quality_degradation", iface.GetTriggerReason())
	assert.Equal(t, 0.85, iface.GetTriggerContext())
	assert.Equal(t, 7, iface.GetHandoffIndex())

	jsonData, err := iface.ToJSON()
	assert.NoError(t, err)
	assert.NotEmpty(t, jsonData)

	summary := iface.GetSummary()
	assert.NotEmpty(t, summary)
}

func TestRoundTrip_PreservesAllData(t *testing.T) {
	t.Parallel()

	original := BuildHandoffState(DesignerHandoffConfig{
		AgentID:        "roundtrip-agent",
		SessionID:      "roundtrip-session",
		PipelineID:     "roundtrip-pipeline",
		TriggerReason:  "memory_pressure",
		TriggerContext: 0.92,
		HandoffIndex:   10,
		StartedAt:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	})

	original.SetOriginalPrompt("Complex multi-step design task")
	original.AddAccomplished("Step 1")
	original.AddAccomplished("Step 2")
	original.AddAccomplished("Step 3")
	original.AddComponentCreated(ComponentInfo{
		Name:    "Widget",
		Path:    "src/Widget.tsx",
		Type:    "component",
		Props:   []string{"size", "color", "onClick"},
		Exports: []string{"Widget", "WidgetProps"},
	})
	original.AddComponentModified(ComponentChange{
		Name:        "Button",
		Path:        "src/Button.tsx",
		ChangeType:  "a11y",
		Description: "Added ARIA labels",
	})
	original.AddTokenUsed(TokenUsage{
		TokenName: "--spacing-md",
		Category:  "spacing",
		UsedIn:    "src/Widget.tsx",
	})
	original.AddRemaining("Final polish")
	original.SetContextNotes("Critical: maintain backward compatibility")

	jsonData, err := original.ToJSON()
	require.NoError(t, err)

	restored, err := InjectHandoffState(jsonData)
	require.NoError(t, err)

	assert.Equal(t, original.GetAgentID(), restored.GetAgentID())
	assert.Equal(t, original.GetAgentType(), restored.GetAgentType())
	assert.Equal(t, original.GetSessionID(), restored.GetSessionID())
	assert.Equal(t, original.GetPipelineID(), restored.GetPipelineID())
	assert.Equal(t, original.GetTriggerReason(), restored.GetTriggerReason())
	assert.Equal(t, original.GetTriggerContext(), restored.GetTriggerContext())
	assert.Equal(t, original.GetHandoffIndex(), restored.GetHandoffIndex())

	assert.Equal(t, original.OriginalPrompt, restored.OriginalPrompt)
	assert.Equal(t, original.ContextNotes, restored.ContextNotes)
	assert.Equal(t, len(original.Accomplished), len(restored.Accomplished))
	assert.Equal(t, len(original.ComponentsCreated), len(restored.ComponentsCreated))
	assert.Equal(t, len(original.ComponentsModified), len(restored.ComponentsModified))
	assert.Equal(t, len(original.TokensUsed), len(restored.TokensUsed))
	assert.Equal(t, len(original.Remaining), len(restored.Remaining))

	if len(original.ComponentsCreated) > 0 && len(restored.ComponentsCreated) > 0 {
		assert.Equal(t, original.ComponentsCreated[0].Name, restored.ComponentsCreated[0].Name)
		assert.Equal(t, original.ComponentsCreated[0].Props, restored.ComponentsCreated[0].Props)
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	state := BuildHandoffState(DesignerHandoffConfig{AgentID: "concurrent-test"})
	var wg sync.WaitGroup
	iterations := 100

	wg.Add(4)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			state.AddAccomplished("task")
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			state.AddComponentCreated(ComponentInfo{Name: "Comp"})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			state.AddRemaining("todo")
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = state.GetSummary()
		}
	}()

	wg.Wait()

	assert.GreaterOrEqual(t, len(state.Accomplished), 1)
	assert.GreaterOrEqual(t, len(state.ComponentsCreated), 1)
	assert.GreaterOrEqual(t, len(state.Remaining), 1)
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello..."},
		{"empty string", "", 5, ""},
		{"very short max", "hello", 4, "h..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := truncate(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractComponentNames(t *testing.T) {
	t.Parallel()

	components := []ComponentInfo{
		{Name: "Alpha"},
		{Name: "Beta"},
		{Name: "Gamma"},
	}

	names := extractComponentNames(components)

	assert.Equal(t, []string{"Alpha", "Beta", "Gamma"}, names)
}

func TestExtractComponentNames_Empty(t *testing.T) {
	t.Parallel()

	names := extractComponentNames([]ComponentInfo{})

	assert.Empty(t, names)
}

func TestExtractChangeNames(t *testing.T) {
	t.Parallel()

	changes := []ComponentChange{
		{Name: "Change1"},
		{Name: "Change2"},
	}

	names := extractChangeNames(changes)

	assert.Equal(t, []string{"Change1", "Change2"}, names)
}

func TestHandoffChaining(t *testing.T) {
	t.Parallel()

	states := make([]*DesignerHandoffState, 3)

	for i := 0; i < 3; i++ {
		states[i] = BuildHandoffState(DesignerHandoffConfig{
			AgentID:      "designer-chain",
			SessionID:    "session-chain",
			HandoffIndex: i + 1,
		})
		states[i].SetOriginalPrompt("Long running design task")

		if i > 0 {
			for _, acc := range states[i-1].Accomplished {
				states[i].AddAccomplished(acc)
			}
		}
		states[i].AddAccomplished("Work from handoff " + string(rune('0'+i+1)))
	}

	assert.Equal(t, 1, states[0].GetHandoffIndex())
	assert.Equal(t, 2, states[1].GetHandoffIndex())
	assert.Equal(t, 3, states[2].GetHandoffIndex())
	assert.Len(t, states[2].Accomplished, 3)
}

func TestComponentInfoFields(t *testing.T) {
	t.Parallel()

	info := ComponentInfo{
		Name:    "TestComponent",
		Path:    "src/components/TestComponent.tsx",
		Type:    "layout",
		Props:   []string{"children", "className"},
		Exports: []string{"TestComponent"},
	}

	jsonData, err := json.Marshal(info)
	require.NoError(t, err)

	var restored ComponentInfo
	err = json.Unmarshal(jsonData, &restored)
	require.NoError(t, err)

	assert.Equal(t, info, restored)
}

func TestComponentChangeFields(t *testing.T) {
	t.Parallel()

	change := ComponentChange{
		Name:        "ModifiedComponent",
		Path:        "src/ModifiedComponent.tsx",
		ChangeType:  "structure",
		Description: "Refactored to use hooks",
	}

	jsonData, err := json.Marshal(change)
	require.NoError(t, err)

	var restored ComponentChange
	err = json.Unmarshal(jsonData, &restored)
	require.NoError(t, err)

	assert.Equal(t, change, restored)
}

func TestTokenUsageFields(t *testing.T) {
	t.Parallel()

	token := TokenUsage{
		TokenName: "--font-size-lg",
		Category:  "typography",
		UsedIn:    "src/Heading.tsx",
	}

	jsonData, err := json.Marshal(token)
	require.NoError(t, err)

	var restored TokenUsage
	err = json.Unmarshal(jsonData, &restored)
	require.NoError(t, err)

	assert.Equal(t, token, restored)
}

func TestEmptyStateSerialization(t *testing.T) {
	t.Parallel()

	state := BuildHandoffState(DesignerHandoffConfig{})

	jsonData, err := state.ToJSON()
	require.NoError(t, err)

	restored, err := InjectHandoffState(jsonData)
	require.NoError(t, err)

	assert.Equal(t, "designer", restored.GetAgentType())
	assert.NotNil(t, restored.Accomplished)
	assert.NotNil(t, restored.ComponentsCreated)
	assert.NotNil(t, restored.ComponentsModified)
	assert.NotNil(t, restored.TokensUsed)
	assert.NotNil(t, restored.Remaining)
}
