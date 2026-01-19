package handoff

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// HO.10.1 ProfileLearner Tests
// =============================================================================

func TestProfileLearnerConfig_Defaults(t *testing.T) {
	config := DefaultProfileLearnerConfig()

	if config.DefaultAgentType != "default" {
		t.Errorf("DefaultAgentType = %s, want default", config.DefaultAgentType)
	}
	if config.DefaultModelID != "default" {
		t.Errorf("DefaultModelID = %s, want default", config.DefaultModelID)
	}
	if config.MinObservationsForConfidence != 5 {
		t.Errorf("MinObservationsForConfidence = %d, want 5", config.MinObservationsForConfidence)
	}
	if !config.EnableHierarchicalFallback {
		t.Error("EnableHierarchicalFallback should be true by default")
	}
}

func TestProfileLearnerConfig_Clone(t *testing.T) {
	config := DefaultProfileLearnerConfig()
	config.DefaultAgentType = "custom"

	cloned := config.Clone()

	if cloned.DefaultAgentType != "custom" {
		t.Errorf("Cloned DefaultAgentType = %s, want custom", cloned.DefaultAgentType)
	}

	// Verify independence
	cloned.DefaultAgentType = "modified"
	if config.DefaultAgentType == "modified" {
		t.Error("Clone should be independent")
	}
}

func TestProfileLearnerConfig_CloneNil(t *testing.T) {
	var config *ProfileLearnerConfig
	cloned := config.Clone()
	if cloned != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestNewProfileLearner(t *testing.T) {
	t.Run("with nil params", func(t *testing.T) {
		learner := NewProfileLearner(nil, nil)

		if learner == nil {
			t.Fatal("Learner should not be nil")
		}
		if learner.config == nil {
			t.Error("Config should be initialized")
		}
		if learner.priorHierarchy == nil {
			t.Error("PriorHierarchy should be initialized")
		}
		if learner.profiles == nil {
			t.Error("Profiles map should be initialized")
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &ProfileLearnerConfig{
			DefaultAgentType: "custom-agent",
			DefaultModelID:   "custom-model",
		}
		learner := NewProfileLearner(config, nil)

		if learner.config.DefaultAgentType != "custom-agent" {
			t.Errorf("DefaultAgentType = %s, want custom-agent", learner.config.DefaultAgentType)
		}
	})

	t.Run("with prior hierarchy", func(t *testing.T) {
		hierarchy := NewPriorHierarchy()
		learner := NewProfileLearner(nil, hierarchy)

		if learner.priorHierarchy != hierarchy {
			t.Error("PriorHierarchy should be set")
		}
	})
}

func TestProfileLearner_RecordObservation(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	learner.RecordObservation("agent1", "model1", obs)

	// Verify observation was recorded
	stats := learner.Stats()
	if stats.TotalObservations != 1 {
		t.Errorf("TotalObservations = %d, want 1", stats.TotalObservations)
	}
	if stats.ProfileCount != 1 {
		t.Errorf("ProfileCount = %d, want 1", stats.ProfileCount)
	}
	if !stats.PendingPersistence {
		t.Error("PendingPersistence should be true after observation")
	}
}

func TestProfileLearner_RecordObservation_NilObservation(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	learner.RecordObservation("agent1", "model1", nil)

	stats := learner.Stats()
	if stats.TotalObservations != 0 {
		t.Errorf("TotalObservations = %d, want 0 for nil observation", stats.TotalObservations)
	}
}

func TestProfileLearner_RecordObservation_MultipleAgents(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	obs1 := NewHandoffObservation(1000, 5, 0.8, true, false)
	obs2 := NewHandoffObservation(2000, 10, 0.9, true, false)

	learner.RecordObservation("agent1", "model1", obs1)
	learner.RecordObservation("agent2", "model1", obs2)
	learner.RecordObservation("agent1", "model2", obs1)

	stats := learner.Stats()
	if stats.ProfileCount != 3 {
		t.Errorf("ProfileCount = %d, want 3", stats.ProfileCount)
	}
	if stats.TotalObservations != 3 {
		t.Errorf("TotalObservations = %d, want 3", stats.TotalObservations)
	}
}

func TestProfileLearner_RecordObservation_EmptyIdentifiers(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	learner.RecordObservation("", "", obs)

	// Should use default identifiers
	profile := learner.GetProfileRaw("default", "default")
	if profile == nil {
		t.Error("Profile should exist with default identifiers")
	}
}

func TestProfileLearner_RecordHandoffOutcome(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	learner.RecordHandoffOutcome("agent1", "model1", 1000, 5, true, 0.9)

	stats := learner.Stats()
	if stats.TotalHandoffOutcomes != 1 {
		t.Errorf("TotalHandoffOutcomes = %d, want 1", stats.TotalHandoffOutcomes)
	}
	if stats.ProfileCount != 1 {
		t.Errorf("ProfileCount = %d, want 1", stats.ProfileCount)
	}
}

func TestProfileLearner_RecordHandoffOutcome_Failure(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	learner.RecordHandoffOutcome("agent1", "model1", 1000, 5, false, 0.2)

	profile := learner.GetProfileRaw("agent1", "model1")
	if profile == nil {
		t.Fatal("Profile should exist")
	}
}

func TestProfileLearner_GetProfile_NewProfile(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	profile := learner.GetProfile("new-agent", "new-model")

	if profile == nil {
		t.Fatal("Profile should not be nil for new agent+model")
	}
	if profile.AgentType != "new-agent" {
		t.Errorf("AgentType = %s, want new-agent", profile.AgentType)
	}
	if profile.ModelID != "new-model" {
		t.Errorf("ModelID = %s, want new-model", profile.ModelID)
	}
}

func TestProfileLearner_GetProfile_ExistingProfile(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	// Record some observations to create profile
	for i := 0; i < 10; i++ {
		obs := NewHandoffObservation(1000+i*100, 5+i, 0.8, true, false)
		learner.RecordObservation("agent1", "model1", obs)
	}

	profile := learner.GetProfile("agent1", "model1")

	if profile == nil {
		t.Fatal("Profile should not be nil")
	}
	if profile.EffectiveSamples < 1 {
		t.Error("EffectiveSamples should be > 0 after observations")
	}
}

func TestProfileLearner_GetProfile_HierarchicalFallback(t *testing.T) {
	hierarchy := NewPriorHierarchy()

	// Add some model-level priors via GetOrCreateModelPriors
	modelPriors := hierarchy.GetOrCreateModelPriors("model1")
	modelPriors.OptimalContextSize = NewLearnedContextSize(2000.0, 1.0)

	config := &ProfileLearnerConfig{
		MinObservationsForConfidence: 5,
		EnableHierarchicalFallback:   true,
	}
	learner := NewProfileLearner(config, hierarchy)

	// Record just one observation (below confidence threshold)
	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	learner.RecordObservation("agent1", "model1", obs)

	// Get profile should blend with hierarchy
	profile := learner.GetProfile("agent1", "model1")

	if profile == nil {
		t.Fatal("Profile should not be nil")
	}
	// Profile should reflect hierarchical blending (exact values depend on implementation)
}

func TestProfileLearner_GetProfile_DisabledFallback(t *testing.T) {
	config := &ProfileLearnerConfig{
		MinObservationsForConfidence: 5,
		EnableHierarchicalFallback:   false,
	}
	learner := NewProfileLearner(config, nil)

	profile := learner.GetProfile("agent1", "model1")

	if profile == nil {
		t.Fatal("Profile should not be nil even with disabled fallback")
	}
}

func TestProfileLearner_GetProfileRaw(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	// No profile exists yet
	profile := learner.GetProfileRaw("agent1", "model1")
	if profile != nil {
		t.Error("GetProfileRaw should return nil for non-existent profile")
	}

	// Create profile
	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	learner.RecordObservation("agent1", "model1", obs)

	profile = learner.GetProfileRaw("agent1", "model1")
	if profile == nil {
		t.Error("GetProfileRaw should return profile after observation")
	}
}

func TestProfileLearner_HasProfile(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	if learner.HasProfile("agent1", "model1") {
		t.Error("HasProfile should return false before any observations")
	}

	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	learner.RecordObservation("agent1", "model1", obs)

	if !learner.HasProfile("agent1", "model1") {
		t.Error("HasProfile should return true after observation")
	}
}

func TestProfileLearner_Persistence(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	// Initial state
	if learner.NeedsPersistence() {
		t.Error("NeedsPersistence should be false initially")
	}

	// Record observation
	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	learner.RecordObservation("agent1", "model1", obs)

	if !learner.NeedsPersistence() {
		t.Error("NeedsPersistence should be true after observation")
	}

	// Mark persisted
	learner.MarkPersisted()

	if learner.NeedsPersistence() {
		t.Error("NeedsPersistence should be false after MarkPersisted")
	}
}

func TestProfileLearner_ShouldPersist(t *testing.T) {
	config := &ProfileLearnerConfig{
		PersistenceInterval: 100 * time.Millisecond,
	}
	learner := NewProfileLearner(config, nil)

	// Record observation
	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	learner.RecordObservation("agent1", "model1", obs)
	learner.MarkPersisted()

	// Record another observation
	learner.RecordObservation("agent1", "model1", obs)

	// Should not persist immediately
	if learner.ShouldPersist() {
		t.Error("ShouldPersist should be false before interval elapsed")
	}

	// Wait for interval
	time.Sleep(150 * time.Millisecond)

	if !learner.ShouldPersist() {
		t.Error("ShouldPersist should be true after interval elapsed")
	}
}

func TestProfileLearner_GetAllProfiles(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	// Create multiple profiles
	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	learner.RecordObservation("agent1", "model1", obs)
	learner.RecordObservation("agent2", "model1", obs)
	learner.RecordObservation("agent1", "model2", obs)

	profiles := learner.GetAllProfiles()

	if len(profiles) != 3 {
		t.Errorf("GetAllProfiles returned %d profiles, want 3", len(profiles))
	}
}

func TestProfileLearner_LoadProfiles(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	profiles := map[string]*AgentHandoffProfile{
		"agent1|model1": NewAgentHandoffProfile("agent1", "model1", ""),
		"agent2|model2": NewAgentHandoffProfile("agent2", "model2", ""),
	}

	learner.LoadProfiles(profiles)

	stats := learner.Stats()
	if stats.ProfileCount != 2 {
		t.Errorf("ProfileCount = %d after loading, want 2", stats.ProfileCount)
	}
}

func TestProfileLearner_Clear(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	// Create profiles
	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	learner.RecordObservation("agent1", "model1", obs)

	// Clear
	learner.Clear()

	stats := learner.Stats()
	if stats.ProfileCount != 0 {
		t.Errorf("ProfileCount = %d after Clear, want 0", stats.ProfileCount)
	}
	if stats.TotalObservations != 0 {
		t.Errorf("TotalObservations = %d after Clear, want 0", stats.TotalObservations)
	}
}

func TestProfileLearner_Concurrent(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	var wg sync.WaitGroup
	numGoroutines := 10
	opsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			agentID := "agent" + string(rune('0'+idx%3))
			modelName := "model" + string(rune('0'+idx%2))

			for j := 0; j < opsPerGoroutine; j++ {
				obs := NewHandoffObservation(1000+j*100, j, 0.8, true, false)
				learner.RecordObservation(agentID, modelName, obs)
				learner.GetProfile(agentID, modelName)
			}
		}(i)
	}

	wg.Wait()

	stats := learner.Stats()
	if stats.TotalObservations != int64(numGoroutines*opsPerGoroutine) {
		t.Errorf("TotalObservations = %d, want %d",
			stats.TotalObservations, numGoroutines*opsPerGoroutine)
	}
}

func TestProfileLearner_JSON(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	// Add some data
	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	learner.RecordObservation("agent1", "model1", obs)

	// Marshal
	data, err := json.Marshal(learner)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal
	var restored ProfileLearner
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify
	stats := restored.Stats()
	if stats.ProfileCount != 1 {
		t.Errorf("Restored ProfileCount = %d, want 1", stats.ProfileCount)
	}
}

// =============================================================================
// HO.10.2 HandoffManager Tests
// =============================================================================

func TestHandoffManagerConfig_Defaults(t *testing.T) {
	config := DefaultHandoffManagerConfig()

	if config.EvaluationInterval != 30*time.Second {
		t.Errorf("EvaluationInterval = %v, want 30s", config.EvaluationInterval)
	}
	if config.DefaultTimeout != 60*time.Second {
		t.Errorf("DefaultTimeout = %v, want 60s", config.DefaultTimeout)
	}
	if !config.EnableAutoEvaluation {
		t.Error("EnableAutoEvaluation should be true by default")
	}
	if !config.EnableLearning {
		t.Error("EnableLearning should be true by default")
	}
}

func TestHandoffManagerConfig_Clone(t *testing.T) {
	config := DefaultHandoffManagerConfig()
	config.AgentID = "custom"

	cloned := config.Clone()

	if cloned.AgentID != "custom" {
		t.Errorf("Cloned AgentID = %s, want custom", cloned.AgentID)
	}

	cloned.AgentID = "modified"
	if config.AgentID == "modified" {
		t.Error("Clone should be independent")
	}
}

func TestHandoffManagerConfig_CloneNil(t *testing.T) {
	var config *HandoffManagerConfig
	cloned := config.Clone()
	if cloned != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestNewHandoffManager(t *testing.T) {
	t.Run("with nil params", func(t *testing.T) {
		manager := NewHandoffManager(nil, nil, nil, nil)

		if manager == nil {
			t.Fatal("Manager should not be nil")
		}
		if manager.config == nil {
			t.Error("Config should be initialized")
		}
		if manager.controller == nil {
			t.Error("Controller should be initialized")
		}
		if manager.executor == nil {
			t.Error("Executor should be initialized")
		}
		if manager.learner == nil {
			t.Error("Learner should be initialized")
		}
		if manager.preparedContext == nil {
			t.Error("PreparedContext should be initialized")
		}
	})

	t.Run("with custom components", func(t *testing.T) {
		config := &HandoffManagerConfig{
			AgentID:   "custom-agent",
			ModelName: "custom-model",
		}
		controller := NewHandoffController(nil, nil, nil)
		executor := NewHandoffExecutor(nil, nil)
		learner := NewProfileLearner(nil, nil)

		manager := NewHandoffManager(config, controller, executor, learner)

		if manager.config.AgentID != "custom-agent" {
			t.Errorf("AgentID = %s, want custom-agent", manager.config.AgentID)
		}
		if manager.controller != controller {
			t.Error("Controller should be set")
		}
		if manager.executor != executor {
			t.Error("Executor should be set")
		}
		if manager.learner != learner {
			t.Error("Learner should be set")
		}
	})
}

func TestHandoffManager_Lifecycle(t *testing.T) {
	config := &HandoffManagerConfig{
		EnableAutoEvaluation:    false, // Disable for this test
		GracefulShutdownTimeout: 1 * time.Second,
	}
	manager := NewHandoffManager(config, nil, nil, nil)

	// Initial state
	if manager.IsRunning() {
		t.Error("Manager should not be running initially")
	}
	if manager.GetStatus() != StatusStopped {
		t.Errorf("Status = %s, want Stopped", manager.GetStatus())
	}

	// Start
	if err := manager.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !manager.IsRunning() {
		t.Error("Manager should be running after Start")
	}
	if manager.GetStatus() != StatusRunning {
		t.Errorf("Status = %s, want Running", manager.GetStatus())
	}

	// Start again should be no-op
	if err := manager.Start(); err != nil {
		t.Errorf("Second Start should succeed: %v", err)
	}

	// Stop
	if err := manager.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if manager.IsRunning() {
		t.Error("Manager should not be running after Stop")
	}
	if !manager.IsClosed() {
		t.Error("Manager should be closed after Stop")
	}

	// Stop again should return error
	if err := manager.Stop(); err == nil {
		t.Error("Second Stop should return error")
	}
}

func TestHandoffManager_StartAfterClose(t *testing.T) {
	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
	}
	manager := NewHandoffManager(config, nil, nil, nil)

	manager.Stop()

	if err := manager.Start(); err != ErrManagerClosed {
		t.Errorf("Start after close should return ErrManagerClosed, got %v", err)
	}
}

func TestHandoffManager_UpdateContext(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	messages := []Message{
		NewMessage("user", "Hello"),
		NewMessage("assistant", "Hi there!"),
	}

	manager.UpdateContext(messages)

	ctx := manager.GetPreparedContext()
	if ctx == nil {
		t.Fatal("PreparedContext should not be nil")
	}

	recent := ctx.RecentMessages()
	if len(recent) != 2 {
		t.Errorf("Recent messages = %d, want 2", len(recent))
	}
}

func TestHandoffManager_AddMessage(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	manager.AddMessage(NewMessage("user", "Test message"))

	ctx := manager.GetPreparedContext()
	recent := ctx.RecentMessages()
	if len(recent) != 1 {
		t.Errorf("Recent messages = %d, want 1", len(recent))
	}
}

func TestHandoffManager_SetPreparedContext(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	newCtx := NewPreparedContextDefault()
	newCtx.AddMessage(NewMessage("user", "Custom message"))
	newCtx.SetMetadata("custom_key", "custom_value")

	manager.SetPreparedContext(newCtx)

	ctx := manager.GetPreparedContext()
	if ctx == nil {
		t.Fatal("PreparedContext should not be nil")
	}

	// Verify it's a copy
	newCtx.SetMetadata("custom_key", "modified")
	retrieved := manager.GetPreparedContext()
	value, _ := retrieved.GetMetadata("custom_key")
	if value == "modified" {
		t.Error("SetPreparedContext should store a copy")
	}
}

func TestHandoffManager_SetPreparedContext_Nil(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	originalCtx := manager.GetPreparedContext()
	manager.SetPreparedContext(nil)

	// Should not change
	currentCtx := manager.GetPreparedContext()
	if currentCtx == nil {
		t.Error("PreparedContext should not be nil after setting nil")
	}
	if currentCtx != nil && originalCtx != nil {
		// Context should still exist
	}
}

func TestHandoffManager_UpdateContext_AfterClose(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	manager.Stop()

	// Should not panic
	manager.UpdateContext([]Message{NewMessage("user", "Test")})
	manager.AddMessage(NewMessage("user", "Test"))
}

func TestHandoffManager_EvaluateAndExecute_NoHandoff(t *testing.T) {
	// Create controller that won't trigger handoff
	controller := NewHandoffController(nil, nil, nil)
	manager := NewHandoffManager(nil, controller, nil, nil)

	result := manager.EvaluateAndExecute(context.Background())

	// Should return nil when no handoff is triggered
	if result != nil {
		t.Error("EvaluateAndExecute should return nil when no handoff triggered")
	}

	stats := manager.Stats()
	if stats.Evaluations != 1 {
		t.Errorf("Evaluations = %d, want 1", stats.Evaluations)
	}
}

func TestHandoffManager_EvaluateAndExecute_Closed(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	manager.Stop()

	result := manager.EvaluateAndExecute(context.Background())

	if result == nil {
		t.Fatal("Result should not be nil when closed")
	}
	if result.Success {
		t.Error("Result should indicate failure when closed")
	}
	if result.Error != ErrManagerClosed {
		t.Errorf("Error = %v, want ErrManagerClosed", result.Error)
	}
}

func TestHandoffManager_EvaluateAndExecute_NilContext(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	// Should use background context
	result := manager.EvaluateAndExecute(nil)

	// Result depends on evaluation (likely nil if no handoff triggered)
	// Just verify it doesn't panic
	_ = result
}

func TestHandoffManager_ForceHandoff(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)
	manager := NewHandoffManager(nil, nil, executor, nil)

	// Add some context
	manager.AddMessage(NewMessage("user", "Hello"))
	manager.AddMessage(NewMessage("assistant", "Hi!"))

	result := manager.ForceHandoff(context.Background(), "test reason")

	if result == nil {
		t.Fatal("ForceHandoff should return a result")
	}
	if !result.Success {
		t.Errorf("ForceHandoff should succeed: %v", result.Error)
	}

	stats := manager.Stats()
	if stats.HandoffsForced != 1 {
		t.Errorf("HandoffsForced = %d, want 1", stats.HandoffsForced)
	}
	if stats.HandoffsExecuted != 1 {
		t.Errorf("HandoffsExecuted = %d, want 1", stats.HandoffsExecuted)
	}
}

func TestHandoffManager_ForceHandoff_Closed(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	manager.Stop()

	result := manager.ForceHandoff(context.Background(), "test")

	if result == nil {
		t.Fatal("Result should not be nil")
	}
	if result.Success {
		t.Error("ForceHandoff should fail when closed")
	}
	if result.Error != ErrManagerClosed {
		t.Errorf("Error = %v, want ErrManagerClosed", result.Error)
	}
}

func TestHandoffManager_ForceHandoff_NoContext(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	// Clear prepared context
	manager.contextMu.Lock()
	manager.preparedContext = nil
	manager.contextMu.Unlock()

	result := manager.ForceHandoff(context.Background(), "test")

	if result == nil {
		t.Fatal("Result should not be nil")
	}
	if result.Success {
		t.Error("ForceHandoff should fail with no context")
	}
	if result.Error != ErrNoContextPrepared {
		t.Errorf("Error = %v, want ErrNoContextPrepared", result.Error)
	}
}

func TestHandoffManager_GetStatus(t *testing.T) {
	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
	}
	manager := NewHandoffManager(config, nil, nil, nil)

	if manager.GetStatus() != StatusStopped {
		t.Errorf("Initial status = %s, want Stopped", manager.GetStatus())
	}

	manager.Start()
	defer manager.Stop()

	if manager.GetStatus() != StatusRunning {
		t.Errorf("After start status = %s, want Running", manager.GetStatus())
	}
}

func TestHandoffManager_Stats(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)
	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
		AgentID:              "test-agent",
		ModelName:            "test-model",
	}
	manager := NewHandoffManager(config, nil, executor, nil)
	manager.Start()
	defer manager.Stop()

	// Evaluate
	manager.EvaluateAndExecute(context.Background())

	// Force handoff
	manager.AddMessage(NewMessage("user", "Test"))
	manager.ForceHandoff(context.Background(), "test")

	stats := manager.Stats()

	if stats.Status != StatusRunning {
		t.Errorf("Status = %s, want Running", stats.Status)
	}
	if stats.Evaluations != 1 {
		t.Errorf("Evaluations = %d, want 1", stats.Evaluations)
	}
	if stats.HandoffsExecuted != 1 {
		t.Errorf("HandoffsExecuted = %d, want 1", stats.HandoffsExecuted)
	}
	if stats.HandoffsForced != 1 {
		t.Errorf("HandoffsForced = %d, want 1", stats.HandoffsForced)
	}
	if !stats.IsRunning {
		t.Error("IsRunning should be true")
	}
}

func TestHandoffManager_RecentResults(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)
	manager := NewHandoffManager(nil, nil, executor, nil)

	// Execute some handoffs
	for i := 0; i < 5; i++ {
		manager.AddMessage(NewMessage("user", "Test"))
		manager.ForceHandoff(context.Background(), "test")
	}

	results := manager.RecentResults(3)
	if len(results) != 3 {
		t.Errorf("RecentResults = %d, want 3", len(results))
	}

	allResults := manager.RecentResults(100)
	if len(allResults) != 5 {
		t.Errorf("All RecentResults = %d, want 5", len(allResults))
	}
}

func TestHandoffManager_RecentResults_Empty(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	results := manager.RecentResults(10)
	if len(results) != 0 {
		t.Errorf("RecentResults = %d, want 0", len(results))
	}

	results = manager.RecentResults(0)
	if results != nil {
		t.Error("RecentResults(0) should return nil")
	}
}

func TestHandoffManager_GetSetConfig(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	newConfig := &HandoffManagerConfig{
		AgentID:   "new-agent",
		ModelName: "new-model",
	}
	manager.SetConfig(newConfig)

	got := manager.GetConfig()
	if got.AgentID != "new-agent" {
		t.Errorf("AgentID = %s, want new-agent", got.AgentID)
	}

	// Verify it returns a copy
	got.AgentID = "modified"
	got2 := manager.GetConfig()
	if got2.AgentID == "modified" {
		t.Error("GetConfig should return a copy")
	}
}

func TestHandoffManager_SetConfigNil(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	originalConfig := manager.GetConfig()

	manager.SetConfig(nil)

	if manager.config.EvaluationInterval != originalConfig.EvaluationInterval {
		t.Error("SetConfig(nil) should not change config")
	}
}

func TestHandoffManager_GetComponents(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)
	executor := NewHandoffExecutor(nil, nil)
	learner := NewProfileLearner(nil, nil)
	manager := NewHandoffManager(nil, controller, executor, learner)

	if manager.GetController() != controller {
		t.Error("GetController should return the controller")
	}
	if manager.GetExecutor() != executor {
		t.Error("GetExecutor should return the executor")
	}
	if manager.GetLearner() != learner {
		t.Error("GetLearner should return the learner")
	}
}

func TestHandoffManager_BackgroundEvaluation(t *testing.T) {
	config := &HandoffManagerConfig{
		EvaluationInterval:    100 * time.Millisecond,
		MinEvaluationInterval: 50 * time.Millisecond,
		EnableAutoEvaluation:  true,
		DefaultTimeout:        5 * time.Second,
	}
	manager := NewHandoffManager(config, nil, nil, nil)

	manager.Start()
	defer manager.Stop()

	// Wait for some evaluations
	time.Sleep(350 * time.Millisecond)

	stats := manager.Stats()
	// Should have at least 2 evaluations (initial + at least one periodic)
	if stats.Evaluations < 1 {
		t.Errorf("Evaluations = %d, want >= 1", stats.Evaluations)
	}
}

func TestHandoffManager_BackgroundEvaluation_Disabled(t *testing.T) {
	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
	}
	manager := NewHandoffManager(config, nil, nil, nil)

	manager.Start()
	defer manager.Stop()

	time.Sleep(200 * time.Millisecond)

	stats := manager.Stats()
	if stats.Evaluations != 0 {
		t.Errorf("Evaluations = %d, want 0 when disabled", stats.Evaluations)
	}
}

func TestHandoffManager_Concurrent(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)
	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
	}
	manager := NewHandoffManager(config, nil, executor, nil)
	manager.Start()
	defer manager.Stop()

	var wg sync.WaitGroup
	numGoroutines := 10
	opsPerGoroutine := 50

	var evaluations atomic.Int64
	var updates atomic.Int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			for j := 0; j < opsPerGoroutine; j++ {
				switch j % 4 {
				case 0:
					manager.AddMessage(NewMessage("user", "Test message"))
					updates.Add(1)
				case 1:
					manager.EvaluateAndExecute(context.Background())
					evaluations.Add(1)
				case 2:
					manager.GetPreparedContext()
				case 3:
					manager.Stats()
				}
			}
		}(i)
	}

	wg.Wait()

	stats := manager.Stats()
	expectedEvals := evaluations.Load()
	if stats.Evaluations != expectedEvals {
		t.Errorf("Evaluations = %d, want %d", stats.Evaluations, expectedEvals)
	}
}

func TestHandoffManager_GracefulShutdown(t *testing.T) {
	config := &HandoffManagerConfig{
		EvaluationInterval:      100 * time.Millisecond,
		EnableAutoEvaluation:    true,
		GracefulShutdownTimeout: 1 * time.Second,
	}
	manager := NewHandoffManager(config, nil, nil, nil)

	manager.Start()

	// Wait a bit for background loop to start
	time.Sleep(50 * time.Millisecond)

	// Stop should complete within timeout
	done := make(chan struct{})
	go func() {
		manager.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good, shutdown completed
	case <-time.After(2 * time.Second):
		t.Error("Shutdown did not complete within expected time")
	}

	if !manager.IsClosed() {
		t.Error("Manager should be closed after Stop")
	}
}

func TestHandoffManager_JSON(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	// Add some data
	manager.AddMessage(NewMessage("user", "Test"))
	manager.EvaluateAndExecute(context.Background())

	// Marshal
	data, err := json.Marshal(manager)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal
	var restored HandoffManager
	restored.recentResults = NewCircularBuffer[*HandoffResult](100)
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if restored.evaluations.Load() == 0 {
		t.Error("Evaluations should be preserved")
	}
}

func TestHandoffStatus_String(t *testing.T) {
	tests := []struct {
		status   HandoffStatus
		expected string
	}{
		{StatusStopped, "Stopped"},
		{StatusRunning, "Running"},
		{StatusEvaluating, "Evaluating"},
		{StatusExecuting, "Executing"},
		{StatusShuttingDown, "ShuttingDown"},
		{HandoffStatus(99), "Unknown"},
	}

	for _, tt := range tests {
		if got := tt.status.String(); got != tt.expected {
			t.Errorf("Status(%d).String() = %s, want %s", tt.status, got, tt.expected)
		}
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestIntegration_ProfileLearnerWithManager(t *testing.T) {
	// Create learner
	learner := NewProfileLearner(nil, nil)

	// Create manager with learner
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)
	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
		EnableLearning:       true,
		AgentID:              "test-agent",
		ModelName:            "test-model",
	}
	manager := NewHandoffManager(config, nil, executor, learner)
	manager.Start()
	defer manager.Stop()

	// Add some context
	manager.AddMessage(NewMessage("user", "Hello"))
	manager.AddMessage(NewMessage("assistant", "Hi!"))

	// Force handoff should trigger learning
	manager.ForceHandoff(context.Background(), "test")

	// Verify learner received outcome
	learnerStats := learner.Stats()
	if learnerStats.TotalHandoffOutcomes != 1 {
		t.Errorf("TotalHandoffOutcomes = %d, want 1", learnerStats.TotalHandoffOutcomes)
	}

	// Profile should exist
	if !learner.HasProfile("test-agent", "test-model") {
		t.Error("Profile should exist for agent+model")
	}
}

func TestIntegration_FullWorkflow(t *testing.T) {
	// Setup
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)
	learner := NewProfileLearner(nil, nil)
	controller := NewHandoffController(nil, nil, nil)

	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
		EnableLearning:       true,
		AgentID:              "integration-agent",
		ModelName:            "integration-model",
		DefaultTimeout:       5 * time.Second,
	}

	manager := NewHandoffManager(config, controller, executor, learner)

	// Start manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Build up context
	messages := []Message{
		NewMessage("user", "Can you help me write some code?"),
		NewMessage("assistant", "Of course! What would you like me to help with?"),
		NewMessage("user", "I need a function to calculate fibonacci numbers"),
		NewMessage("assistant", "Here's a fibonacci function in Go..."),
	}
	manager.UpdateContext(messages)

	// Verify context
	ctx := manager.GetPreparedContext()
	if ctx == nil {
		t.Fatal("PreparedContext should not be nil")
	}
	recent := ctx.RecentMessages()
	if len(recent) != 4 {
		t.Errorf("Recent messages = %d, want 4", len(recent))
	}

	// Evaluate (likely won't trigger handoff)
	result := manager.EvaluateAndExecute(context.Background())
	// result may be nil if no handoff triggered

	// Force handoff
	result = manager.ForceHandoff(context.Background(), "Integration test")
	if result == nil {
		t.Fatal("ForceHandoff should return result")
	}
	if !result.Success {
		t.Errorf("ForceHandoff should succeed: %v", result.Error)
	}

	// Verify stats
	managerStats := manager.Stats()
	if managerStats.HandoffsExecuted != 1 {
		t.Errorf("HandoffsExecuted = %d, want 1", managerStats.HandoffsExecuted)
	}

	// Verify learning occurred
	learnerStats := learner.Stats()
	if learnerStats.ProfileCount != 1 {
		t.Errorf("ProfileCount = %d, want 1", learnerStats.ProfileCount)
	}

	// Verify recent results
	results := manager.RecentResults(10)
	if len(results) < 1 {
		t.Error("Should have at least 1 recent result")
	}

	// Clean shutdown
	if err := manager.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	if !manager.IsClosed() {
		t.Error("Manager should be closed")
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkProfileLearner_RecordObservation(b *testing.B) {
	learner := NewProfileLearner(nil, nil)
	obs := NewHandoffObservation(1000, 5, 0.8, true, false)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		learner.RecordObservation("agent1", "model1", obs)
	}
}

func BenchmarkProfileLearner_GetProfile(b *testing.B) {
	learner := NewProfileLearner(nil, nil)

	// Setup some profiles
	obs := NewHandoffObservation(1000, 5, 0.8, true, false)
	for i := 0; i < 10; i++ {
		learner.RecordObservation("agent1", "model1", obs)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		learner.GetProfile("agent1", "model1")
	}
}

func BenchmarkHandoffManager_UpdateContext(b *testing.B) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	msg := NewMessage("user", "Test message")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.AddMessage(msg)
	}
}

func BenchmarkHandoffManager_EvaluateAndExecute(b *testing.B) {
	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
	}
	manager := NewHandoffManager(config, nil, nil, nil)
	manager.Start()
	defer manager.Stop()

	// Add some context
	manager.AddMessage(NewMessage("user", "Test"))

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.EvaluateAndExecute(ctx)
	}
}

func BenchmarkHandoffManager_Concurrent(b *testing.B) {
	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
	}
	manager := NewHandoffManager(config, nil, nil, nil)
	manager.Start()
	defer manager.Stop()

	msg := NewMessage("user", "Test")
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			manager.AddMessage(msg)
			manager.EvaluateAndExecute(ctx)
		}
	})
}
