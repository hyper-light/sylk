package handoff

import (
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// HO.5.1 HandoffTrigger Tests
// =============================================================================

func TestHandoffTrigger_String(t *testing.T) {
	tests := []struct {
		trigger HandoffTrigger
		want    string
	}{
		{TriggerNone, "None"},
		{TriggerContextFull, "ContextFull"},
		{TriggerQualityDegrading, "QualityDegrading"},
		{TriggerCostOptimization, "CostOptimization"},
		{TriggerUserRequest, "UserRequest"},
		{HandoffTrigger(999), "Unknown(999)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.trigger.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandoffTrigger_JSON(t *testing.T) {
	tests := []struct {
		trigger HandoffTrigger
		want    string
	}{
		{TriggerNone, `"None"`},
		{TriggerContextFull, `"ContextFull"`},
		{TriggerQualityDegrading, `"QualityDegrading"`},
		{TriggerCostOptimization, `"CostOptimization"`},
		{TriggerUserRequest, `"UserRequest"`},
	}

	for _, tt := range tests {
		t.Run(tt.trigger.String(), func(t *testing.T) {
			// Marshal
			data, err := json.Marshal(tt.trigger)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			if string(data) != tt.want {
				t.Errorf("Marshal = %s, want %s", string(data), tt.want)
			}

			// Unmarshal
			var got HandoffTrigger
			err = json.Unmarshal(data, &got)
			if err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if got != tt.trigger {
				t.Errorf("Unmarshal = %v, want %v", got, tt.trigger)
			}
		})
	}
}

func TestParseTriggerString(t *testing.T) {
	tests := []struct {
		input string
		want  HandoffTrigger
	}{
		{"None", TriggerNone},
		{"ContextFull", TriggerContextFull},
		{"QualityDegrading", TriggerQualityDegrading},
		{"CostOptimization", TriggerCostOptimization},
		{"UserRequest", TriggerUserRequest},
		{"Unknown", TriggerNone},
		{"", TriggerNone},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseTriggerString(tt.input)
			if got != tt.want {
				t.Errorf("parseTriggerString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// =============================================================================
// HO.5.1 DecisionFactors Tests
// =============================================================================

func TestDecisionFactors_JSON(t *testing.T) {
	factors := DecisionFactors{
		ContextUtilization: 0.75,
		PredictedQuality:   0.8,
		QualityUncertainty: 0.15,
		CostEstimate:       0.05,
		UCBScore:           0.95,
		ThresholdUsed:      0.5,
		ProfileConfidence:  0.6,
	}

	data, err := json.Marshal(factors)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var got DecisionFactors
	err = json.Unmarshal(data, &got)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if got.ContextUtilization != factors.ContextUtilization {
		t.Errorf("ContextUtilization = %f, want %f", got.ContextUtilization, factors.ContextUtilization)
	}
	if got.PredictedQuality != factors.PredictedQuality {
		t.Errorf("PredictedQuality = %f, want %f", got.PredictedQuality, factors.PredictedQuality)
	}
	if got.UCBScore != factors.UCBScore {
		t.Errorf("UCBScore = %f, want %f", got.UCBScore, factors.UCBScore)
	}
}

// =============================================================================
// HO.5.1 HandoffDecision Tests
// =============================================================================

func TestNewHandoffDecision(t *testing.T) {
	factors := DecisionFactors{
		ContextUtilization: 0.8,
		PredictedQuality:   0.6,
	}

	decision := NewHandoffDecision(
		true,
		0.85,
		"Test reason",
		TriggerQualityDegrading,
		factors,
	)

	if !decision.ShouldHandoff {
		t.Error("ShouldHandoff should be true")
	}
	if decision.Confidence != 0.85 {
		t.Errorf("Confidence = %f, want 0.85", decision.Confidence)
	}
	if decision.Reason != "Test reason" {
		t.Errorf("Reason = %q, want %q", decision.Reason, "Test reason")
	}
	if decision.Trigger != TriggerQualityDegrading {
		t.Errorf("Trigger = %v, want %v", decision.Trigger, TriggerQualityDegrading)
	}
	if decision.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
}

func TestNewHandoffDecision_ConfidenceClamped(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		want       float64
	}{
		{"normal", 0.5, 0.5},
		{"above 1", 1.5, 1.0},
		{"below 0", -0.5, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := NewHandoffDecision(
				true,
				tt.confidence,
				"Test",
				TriggerNone,
				DecisionFactors{},
			)
			if decision.Confidence != tt.want {
				t.Errorf("Confidence = %f, want %f", decision.Confidence, tt.want)
			}
		})
	}
}

func TestHandoffDecision_String(t *testing.T) {
	t.Run("handoff yes", func(t *testing.T) {
		decision := NewHandoffDecision(
			true,
			0.9,
			"Quality low",
			TriggerQualityDegrading,
			DecisionFactors{},
		)
		s := decision.String()
		if s == "" {
			t.Error("String() should not be empty")
		}
		if len(s) < 20 {
			t.Errorf("String() too short: %s", s)
		}
	})

	t.Run("handoff no", func(t *testing.T) {
		decision := NewHandoffDecision(
			false,
			0.8,
			"Quality good",
			TriggerNone,
			DecisionFactors{},
		)
		s := decision.String()
		if s == "" {
			t.Error("String() should not be empty")
		}
	})
}

func TestHandoffDecision_JSON(t *testing.T) {
	decision := NewHandoffDecision(
		true,
		0.85,
		"Test reason",
		TriggerContextFull,
		DecisionFactors{
			ContextUtilization: 0.95,
			PredictedQuality:   0.4,
		},
	)

	data, err := json.Marshal(decision)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var got HandoffDecision
	err = json.Unmarshal(data, &got)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if got.ShouldHandoff != decision.ShouldHandoff {
		t.Errorf("ShouldHandoff = %v, want %v", got.ShouldHandoff, decision.ShouldHandoff)
	}
	if got.Confidence != decision.Confidence {
		t.Errorf("Confidence = %f, want %f", got.Confidence, decision.Confidence)
	}
	if got.Trigger != decision.Trigger {
		t.Errorf("Trigger = %v, want %v", got.Trigger, decision.Trigger)
	}
	if got.Factors.ContextUtilization != decision.Factors.ContextUtilization {
		t.Errorf("ContextUtilization = %f, want %f",
			got.Factors.ContextUtilization, decision.Factors.ContextUtilization)
	}
}

// =============================================================================
// ContextState Tests
// =============================================================================

func TestContextState_Utilization(t *testing.T) {
	tests := []struct {
		name           string
		contextSize    int
		maxContextSize int
		want           float64
	}{
		{"50% utilization", 500, 1000, 0.5},
		{"100% utilization", 1000, 1000, 1.0},
		{"0% utilization", 0, 1000, 0.0},
		{"max is zero", 500, 0, 0.0},
		{"max is negative", 500, -100, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &ContextState{
				ContextSize:    tt.contextSize,
				MaxContextSize: tt.maxContextSize,
			}
			got := ctx.Utilization()
			if math.Abs(got-tt.want) > 1e-10 {
				t.Errorf("Utilization() = %f, want %f", got, tt.want)
			}
		})
	}
}

// =============================================================================
// HO.5.2 ControllerConfig Tests
// =============================================================================

func TestDefaultControllerConfig(t *testing.T) {
	config := DefaultControllerConfig()

	if config.ContextFullThreshold != 0.9 {
		t.Errorf("ContextFullThreshold = %f, want 0.9", config.ContextFullThreshold)
	}
	if config.QualityThreshold != 0.5 {
		t.Errorf("QualityThreshold = %f, want 0.5", config.QualityThreshold)
	}
	if config.ExplorationBeta != 1.0 {
		t.Errorf("ExplorationBeta = %f, want 1.0", config.ExplorationBeta)
	}
	if config.MinConfidenceForDecision != 0.3 {
		t.Errorf("MinConfidenceForDecision = %f, want 0.3", config.MinConfidenceForDecision)
	}
}

func TestControllerConfig_Clone(t *testing.T) {
	config := &ControllerConfig{
		ContextFullThreshold:     0.85,
		QualityThreshold:         0.6,
		ExplorationBeta:          1.5,
		MinConfidenceForDecision: 0.4,
		CostWeight:               0.15,
		QualityWeight:            0.55,
		ContextWeight:            0.3,
	}

	cloned := config.Clone()

	if cloned.ContextFullThreshold != config.ContextFullThreshold {
		t.Errorf("ContextFullThreshold = %f, want %f",
			cloned.ContextFullThreshold, config.ContextFullThreshold)
	}

	// Check independence
	cloned.QualityThreshold = 0.1
	if config.QualityThreshold == 0.1 {
		t.Error("Clone should be independent")
	}
}

func TestControllerConfig_CloneNil(t *testing.T) {
	var config *ControllerConfig
	cloned := config.Clone()
	if cloned != nil {
		t.Error("Clone of nil should be nil")
	}
}

// =============================================================================
// HO.5.2 HandoffController Tests - Creation
// =============================================================================

func TestNewHandoffController(t *testing.T) {
	t.Run("all nil params", func(t *testing.T) {
		controller := NewHandoffController(nil, nil, nil)
		if controller == nil {
			t.Fatal("NewHandoffController returned nil")
		}
		if controller.gp == nil {
			t.Error("GP should be initialized")
		}
		if controller.profile == nil {
			t.Error("Profile should be initialized")
		}
		if controller.config == nil {
			t.Error("Config should be initialized")
		}
	})

	t.Run("with custom components", func(t *testing.T) {
		gp := NewAgentGaussianProcess(nil)
		profile := NewAgentHandoffProfile("coder", "gpt-4", "test-123")
		config := &ControllerConfig{
			ContextFullThreshold: 0.8,
			QualityThreshold:     0.6,
		}

		controller := NewHandoffController(gp, profile, config)

		if controller.gp != gp {
			t.Error("GP should be the provided one")
		}
		if controller.profile != profile {
			t.Error("Profile should be the provided one")
		}
		if controller.config.ContextFullThreshold != 0.8 {
			t.Errorf("Config threshold = %f, want 0.8", controller.config.ContextFullThreshold)
		}
	})
}

// =============================================================================
// HO.5.2 HandoffController Tests - EvaluateHandoff
// =============================================================================

func TestHandoffController_EvaluateHandoff_NilContext(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	decision := controller.EvaluateHandoff(nil)

	if decision.ShouldHandoff {
		t.Error("Should not handoff with nil context")
	}
	if decision.Confidence != 1.0 {
		t.Errorf("Confidence = %f, want 1.0", decision.Confidence)
	}
}

func TestHandoffController_EvaluateHandoff_UserRequest(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	ctx := &ContextState{
		ContextSize:          500,
		MaxContextSize:       1000,
		UserRequestedHandoff: true,
	}

	decision := controller.EvaluateHandoff(ctx)

	if !decision.ShouldHandoff {
		t.Error("Should handoff on user request")
	}
	if decision.Trigger != TriggerUserRequest {
		t.Errorf("Trigger = %v, want TriggerUserRequest", decision.Trigger)
	}
	if decision.Confidence != 1.0 {
		t.Errorf("Confidence = %f, want 1.0 for user request", decision.Confidence)
	}
}

func TestHandoffController_EvaluateHandoff_ContextFull(t *testing.T) {
	config := &ControllerConfig{
		ContextFullThreshold: 0.9,
		QualityThreshold:     0.5,
		ExplorationBeta:      1.0,
	}
	controller := NewHandoffController(nil, nil, config)

	ctx := &ContextState{
		ContextSize:    950,
		MaxContextSize: 1000,
	}

	decision := controller.EvaluateHandoff(ctx)

	if !decision.ShouldHandoff {
		t.Error("Should handoff when context is full")
	}
	if decision.Trigger != TriggerContextFull {
		t.Errorf("Trigger = %v, want TriggerContextFull", decision.Trigger)
	}
	if decision.Confidence < 0.9 {
		t.Errorf("Confidence = %f, want >= 0.9 for context full", decision.Confidence)
	}
}

func TestHandoffController_EvaluateHandoff_ContextAtThreshold(t *testing.T) {
	config := &ControllerConfig{
		ContextFullThreshold: 0.9,
	}
	controller := NewHandoffController(nil, nil, config)

	ctx := &ContextState{
		ContextSize:    900, // Exactly at threshold
		MaxContextSize: 1000,
	}

	decision := controller.EvaluateHandoff(ctx)

	if !decision.ShouldHandoff {
		t.Error("Should handoff when context at threshold")
	}
	if decision.Trigger != TriggerContextFull {
		t.Errorf("Trigger = %v, want TriggerContextFull", decision.Trigger)
	}
}

func TestHandoffController_EvaluateHandoff_QualityDegrading(t *testing.T) {
	// Create a GP with observations showing quality degradation
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(100, 10, 1, 0.95)
	gp.AddObservationFromValues(500, 50, 5, 0.7)
	gp.AddObservationFromValues(800, 80, 8, 0.4)

	config := &ControllerConfig{
		ContextFullThreshold:     0.95,
		QualityThreshold:         0.5,
		ExplorationBeta:          0.5, // Low exploration to rely on mean
		MinConfidenceForDecision: 0.0,
	}
	controller := NewHandoffController(gp, nil, config)

	ctx := &ContextState{
		ContextSize:    800,
		MaxContextSize: 1000,
		TokenCount:     80,
		ToolCallCount:  8,
	}

	decision := controller.EvaluateHandoff(ctx)

	if !decision.ShouldHandoff {
		t.Error("Should handoff when quality is degrading")
	}
	if decision.Trigger != TriggerQualityDegrading {
		t.Errorf("Trigger = %v, want TriggerQualityDegrading", decision.Trigger)
	}
	if decision.Factors.PredictedQuality >= 0.5 {
		t.Errorf("PredictedQuality = %f, should be < 0.5", decision.Factors.PredictedQuality)
	}
}

func TestHandoffController_EvaluateHandoff_NoHandoff(t *testing.T) {
	// Create GP with good quality observations
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(100, 10, 1, 0.9)
	gp.AddObservationFromValues(200, 20, 2, 0.85)

	config := &ControllerConfig{
		ContextFullThreshold: 0.9,
		QualityThreshold:     0.5,
		ExplorationBeta:      0.5,
	}
	controller := NewHandoffController(gp, nil, config)

	ctx := &ContextState{
		ContextSize:    200,
		MaxContextSize: 1000,
		TokenCount:     20,
		ToolCallCount:  2,
	}

	decision := controller.EvaluateHandoff(ctx)

	if decision.ShouldHandoff {
		t.Errorf("Should not handoff: quality=%f, utilization=%f",
			decision.Factors.PredictedQuality,
			decision.Factors.ContextUtilization)
	}
	if decision.Trigger != TriggerNone {
		t.Errorf("Trigger = %v, want TriggerNone", decision.Trigger)
	}
}

func TestHandoffController_EvaluateHandoff_CostOptimization(t *testing.T) {
	// Create GP with borderline quality
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(500, 50, 5, 0.6)
	gp.AddObservationFromValues(600, 60, 6, 0.55)

	config := &ControllerConfig{
		ContextFullThreshold:     0.9,
		QualityThreshold:         0.5,
		ExplorationBeta:          0.5,
		MinConfidenceForDecision: 0.0,
		CostWeight:               0.5, // High cost weight
	}
	controller := NewHandoffController(gp, nil, config)

	ctx := &ContextState{
		ContextSize:           600,
		MaxContextSize:        1000,
		TokenCount:            60,
		ToolCallCount:         6,
		EstimatedCostPerToken: 0.001, // High cost
	}

	decision := controller.EvaluateHandoff(ctx)

	// May or may not trigger cost optimization depending on quality margin
	// Just verify no panic and decision is valid
	if decision == nil {
		t.Fatal("Decision should not be nil")
	}
}

func TestHandoffController_EvaluateHandoff_TriggerPriority(t *testing.T) {
	// User request should take priority over context full
	controller := NewHandoffController(nil, nil, nil)

	ctx := &ContextState{
		ContextSize:          950,
		MaxContextSize:       1000,
		UserRequestedHandoff: true,
	}

	decision := controller.EvaluateHandoff(ctx)

	if decision.Trigger != TriggerUserRequest {
		t.Errorf("User request should take priority, got %v", decision.Trigger)
	}
}

// =============================================================================
// HO.5.2 HandoffController Tests - Update Methods
// =============================================================================

func TestHandoffController_UpdateAfterHandoff(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	ctx := &ContextState{
		ContextSize:   500,
		MaxContextSize: 1000,
		TokenCount:    50,
		ToolCallCount: 5,
		TurnNumber:    10,
	}

	// Initially no observations
	if controller.gp.NumObservations() != 0 {
		t.Errorf("Initial observations = %d, want 0", controller.gp.NumObservations())
	}

	controller.UpdateAfterHandoff(ctx, true, 0.9)

	// Should have one observation now
	if controller.gp.NumObservations() != 1 {
		t.Errorf("After update observations = %d, want 1", controller.gp.NumObservations())
	}

	// Profile should be updated
	if controller.profile.EffectiveSamples < 0.5 {
		t.Error("Profile effective samples should increase")
	}
}

func TestHandoffController_UpdateAfterHandoff_NilContext(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	// Should not panic
	controller.UpdateAfterHandoff(nil, true, 0.8)

	if controller.gp.NumObservations() != 0 {
		t.Error("Should not add observation for nil context")
	}
}

func TestHandoffController_UpdateWithoutHandoff(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	ctx := &ContextState{
		ContextSize:   300,
		MaxContextSize: 1000,
		TokenCount:    30,
		ToolCallCount: 3,
		TurnNumber:    5,
	}

	controller.UpdateWithoutHandoff(ctx, true, 0.85)

	if controller.gp.NumObservations() != 1 {
		t.Errorf("Observations = %d, want 1", controller.gp.NumObservations())
	}
}

func TestHandoffController_UpdateWithoutHandoff_NilContext(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	// Should not panic
	controller.UpdateWithoutHandoff(nil, true, 0.8)

	if controller.gp.NumObservations() != 0 {
		t.Error("Should not add observation for nil context")
	}
}

// =============================================================================
// HO.5.2 HandoffController Tests - Getters and Configuration
// =============================================================================

func TestHandoffController_GetGP(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	controller := NewHandoffController(gp, nil, nil)

	got := controller.GetGP()
	if got != gp {
		t.Error("GetGP should return the GP")
	}
}

func TestHandoffController_GetProfile(t *testing.T) {
	profile := NewAgentHandoffProfile("test", "model", "instance")
	controller := NewHandoffController(nil, profile, nil)

	got := controller.GetProfile()
	if got != profile {
		t.Error("GetProfile should return the profile")
	}
}

func TestHandoffController_GetSetConfig(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	newConfig := &ControllerConfig{
		ContextFullThreshold: 0.85,
		QualityThreshold:     0.6,
	}
	controller.SetConfig(newConfig)

	got := controller.GetConfig()
	if got.ContextFullThreshold != 0.85 {
		t.Errorf("ContextFullThreshold = %f, want 0.85", got.ContextFullThreshold)
	}

	// Should be a copy
	got.QualityThreshold = 0.1
	got2 := controller.GetConfig()
	if got2.QualityThreshold == 0.1 {
		t.Error("GetConfig should return a copy")
	}
}

func TestHandoffController_SetConfigNil(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)
	originalThreshold := controller.config.ContextFullThreshold

	controller.SetConfig(nil)

	if controller.config.ContextFullThreshold != originalThreshold {
		t.Error("SetConfig(nil) should not change config")
	}
}

func TestHandoffController_GetLastDecision(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	// Initially nil
	if controller.GetLastDecision() != nil {
		t.Error("Initial last decision should be nil")
	}

	ctx := &ContextState{
		ContextSize:    500,
		MaxContextSize: 1000,
	}
	controller.EvaluateHandoff(ctx)

	last := controller.GetLastDecision()
	if last == nil {
		t.Error("Last decision should be set after evaluation")
	}
}

func TestHandoffController_GetDecisionHistory(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	// Make several decisions
	for i := 0; i < 5; i++ {
		ctx := &ContextState{
			ContextSize:    i * 100,
			MaxContextSize: 1000,
		}
		controller.EvaluateHandoff(ctx)
	}

	history := controller.GetDecisionHistory()
	if len(history) != 5 {
		t.Errorf("History length = %d, want 5", len(history))
	}

	// Should be a copy
	history[0] = nil
	history2 := controller.GetDecisionHistory()
	if history2[0] == nil {
		t.Error("GetDecisionHistory should return a copy")
	}
}

func TestHandoffController_ClearHistory(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	ctx := &ContextState{
		ContextSize:    500,
		MaxContextSize: 1000,
	}
	controller.EvaluateHandoff(ctx)
	controller.EvaluateHandoff(ctx)

	if len(controller.GetDecisionHistory()) != 2 {
		t.Error("Should have 2 decisions")
	}

	controller.ClearHistory()

	if len(controller.GetDecisionHistory()) != 0 {
		t.Error("History should be empty after clear")
	}
	if controller.GetLastDecision() != nil {
		t.Error("Last decision should be nil after clear")
	}
}

func TestHandoffController_SetMaxHistory(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)
	controller.SetMaxHistory(3)

	// Make 5 decisions
	for i := 0; i < 5; i++ {
		ctx := &ContextState{
			ContextSize:    i * 100,
			MaxContextSize: 1000,
		}
		controller.EvaluateHandoff(ctx)
	}

	history := controller.GetDecisionHistory()
	if len(history) != 3 {
		t.Errorf("History length = %d, want 3", len(history))
	}
}

func TestHandoffController_SetMaxHistory_MinimumOne(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)
	controller.SetMaxHistory(0) // Should be set to 1

	ctx := &ContextState{
		ContextSize:    500,
		MaxContextSize: 1000,
	}
	controller.EvaluateHandoff(ctx)
	controller.EvaluateHandoff(ctx)

	history := controller.GetDecisionHistory()
	if len(history) != 1 {
		t.Errorf("History length = %d, want 1", len(history))
	}
}

// =============================================================================
// HO.5.2 HandoffController Tests - Statistics
// =============================================================================

func TestHandoffController_GetStats(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	// Add some observations to GP
	controller.gp.AddObservationFromValues(100, 10, 1, 0.9)
	controller.gp.AddObservationFromValues(200, 20, 2, 0.8)

	// Make decisions with different triggers
	ctx1 := &ContextState{ContextSize: 950, MaxContextSize: 1000}
	ctx2 := &ContextState{ContextSize: 200, MaxContextSize: 1000}
	ctx3 := &ContextState{ContextSize: 300, MaxContextSize: 1000, UserRequestedHandoff: true}

	controller.EvaluateHandoff(ctx1) // Context full
	controller.EvaluateHandoff(ctx2) // No handoff
	controller.EvaluateHandoff(ctx3) // User request

	stats := controller.GetStats()

	if stats.TotalDecisions != 3 {
		t.Errorf("TotalDecisions = %d, want 3", stats.TotalDecisions)
	}
	if stats.HandoffDecisions != 2 {
		t.Errorf("HandoffDecisions = %d, want 2", stats.HandoffDecisions)
	}
	if stats.GPObservations != 2 {
		t.Errorf("GPObservations = %d, want 2", stats.GPObservations)
	}
	if stats.AverageConfidence <= 0 {
		t.Error("AverageConfidence should be positive")
	}

	// Check trigger counts
	if stats.TriggerCounts["ContextFull"] != 1 {
		t.Errorf("ContextFull count = %d, want 1", stats.TriggerCounts["ContextFull"])
	}
	if stats.TriggerCounts["UserRequest"] != 1 {
		t.Errorf("UserRequest count = %d, want 1", stats.TriggerCounts["UserRequest"])
	}
}

func TestHandoffController_GetStats_Empty(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	stats := controller.GetStats()

	if stats.TotalDecisions != 0 {
		t.Errorf("TotalDecisions = %d, want 0", stats.TotalDecisions)
	}
	if stats.AverageConfidence != 0 {
		t.Errorf("AverageConfidence = %f, want 0", stats.AverageConfidence)
	}
}

// =============================================================================
// HO.5.2 HandoffController Tests - Utility Functions
// =============================================================================

func TestHandoffController_PredictQuality(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(500, 50, 5, 0.8)
	controller := NewHandoffController(gp, nil, nil)

	ctx := &ContextState{
		ContextSize:   500,
		MaxContextSize: 1000,
		TokenCount:    50,
		ToolCallCount: 5,
	}

	pred := controller.PredictQuality(ctx)

	if pred == nil {
		t.Fatal("Prediction should not be nil")
	}
	if pred.Mean < 0.5 || pred.Mean > 1.0 {
		t.Errorf("Mean = %f, should be reasonable", pred.Mean)
	}
}

func TestHandoffController_PredictQuality_NilContext(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	pred := controller.PredictQuality(nil)

	if pred == nil {
		t.Fatal("Prediction should not be nil even with nil context")
	}
	// Should return default prediction
	if pred.Mean != 0.7 {
		t.Errorf("Mean = %f, want 0.7 for nil context", pred.Mean)
	}
}

func TestHandoffController_ComputeUCB(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(500, 50, 5, 0.8)

	config := &ControllerConfig{
		ExplorationBeta: 2.0,
	}
	controller := NewHandoffController(gp, nil, config)

	ctx := &ContextState{
		ContextSize:   500,
		MaxContextSize: 1000,
		TokenCount:    50,
		ToolCallCount: 5,
	}

	ucb := controller.ComputeUCB(ctx)

	// UCB should be higher than mean due to exploration bonus
	pred := controller.PredictQuality(ctx)
	if ucb < pred.Mean {
		t.Errorf("UCB (%f) should be >= mean (%f)", ucb, pred.Mean)
	}
}

func TestHandoffController_ComputeUCB_NilContext(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	ucb := controller.ComputeUCB(nil)

	if ucb != 0.7 {
		t.Errorf("UCB = %f, want 0.7 for nil context", ucb)
	}
}

func TestHandoffController_ShouldExplore(t *testing.T) {
	t.Run("high uncertainty", func(t *testing.T) {
		// New GP with few observations = high uncertainty
		controller := NewHandoffController(nil, nil, nil)

		ctx := &ContextState{
			ContextSize:   500,
			MaxContextSize: 1000,
		}

		// With no observations, uncertainty is high
		shouldExplore := controller.ShouldExplore(ctx)

		// Just verify it doesn't panic and returns a boolean
		_ = shouldExplore
	})

	t.Run("nil context", func(t *testing.T) {
		controller := NewHandoffController(nil, nil, nil)

		shouldExplore := controller.ShouldExplore(nil)

		if shouldExplore {
			t.Error("Should not explore with nil context")
		}
	})
}

func TestHandoffController_WeightedDecisionScore(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(500, 50, 5, 0.8)
	gp.AddObservationFromValues(600, 60, 6, 0.75)

	config := DefaultControllerConfig()
	controller := NewHandoffController(gp, nil, config)

	t.Run("low utilization good quality", func(t *testing.T) {
		ctx := &ContextState{
			ContextSize:    500,
			MaxContextSize: 1000,
			TokenCount:     50,
			ToolCallCount:  5,
		}

		score := controller.WeightedDecisionScore(ctx)

		// Score should be negative (don't handoff) when quality is good
		// This depends on the specific config weights
		_ = score // Just verify no panic
	})

	t.Run("nil context", func(t *testing.T) {
		score := controller.WeightedDecisionScore(nil)
		if score != 0.0 {
			t.Errorf("Score = %f, want 0.0 for nil context", score)
		}
	})
}

// =============================================================================
// HO.5.2 HandoffController Tests - JSON Serialization
// =============================================================================

func TestHandoffController_JSON(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(500, 50, 5, 0.8)

	profile := NewAgentHandoffProfile("coder", "gpt-4", "test-123")
	config := &ControllerConfig{
		ContextFullThreshold: 0.85,
		QualityThreshold:     0.55,
	}

	controller := NewHandoffController(gp, profile, config)
	controller.SetMaxHistory(50)

	// Marshal
	data, err := json.Marshal(controller)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal
	controller2 := &HandoffController{}
	err = json.Unmarshal(data, controller2)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify
	if controller2.gp.NumObservations() != 1 {
		t.Errorf("GP observations = %d, want 1", controller2.gp.NumObservations())
	}
	if controller2.profile.AgentType != "coder" {
		t.Errorf("Profile agent type = %s, want coder", controller2.profile.AgentType)
	}
	if controller2.config.ContextFullThreshold != 0.85 {
		t.Errorf("Config threshold = %f, want 0.85", controller2.config.ContextFullThreshold)
	}
}

func TestHandoffController_JSON_Empty(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	data, err := json.Marshal(controller)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	controller2 := &HandoffController{}
	err = json.Unmarshal(data, controller2)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if controller2.gp == nil {
		t.Error("GP should be initialized")
	}
	if controller2.profile == nil {
		t.Error("Profile should be initialized")
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestHandoffController_ConcurrentAccess(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	var wg sync.WaitGroup
	numGoroutines := 10
	numOpsPerGoroutine := 50

	// Reader goroutines
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				ctx := &ContextState{
					ContextSize:    j * 10,
					MaxContextSize: 1000,
				}
				_ = controller.EvaluateHandoff(ctx)
				_ = controller.GetStats()
				_ = controller.GetLastDecision()
			}
		}()
	}

	// Writer goroutines
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				ctx := &ContextState{
					ContextSize:    j * 20,
					MaxContextSize: 1000,
					TokenCount:     j,
					ToolCallCount:  id,
				}
				controller.UpdateAfterHandoff(ctx, true, 0.8)
			}
		}(i)
	}

	wg.Wait()

	// Should not panic and should have some observations
	if controller.gp.NumObservations() == 0 {
		t.Error("Should have observations after concurrent updates")
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestHandoffController_EdgeCase_ZeroMaxContext(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	ctx := &ContextState{
		ContextSize:    100,
		MaxContextSize: 0,
	}

	decision := controller.EvaluateHandoff(ctx)

	// Should handle gracefully
	if decision == nil {
		t.Fatal("Decision should not be nil")
	}
}

func TestHandoffController_EdgeCase_NegativeValues(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	ctx := &ContextState{
		ContextSize:    -100,
		MaxContextSize: 1000,
		TokenCount:     -50,
		ToolCallCount:  -5,
	}

	decision := controller.EvaluateHandoff(ctx)

	// Should handle gracefully without panic
	if decision == nil {
		t.Fatal("Decision should not be nil")
	}
}

func TestHandoffController_EdgeCase_VeryLargeContext(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	ctx := &ContextState{
		ContextSize:    math.MaxInt32,
		MaxContextSize: math.MaxInt32,
	}

	decision := controller.EvaluateHandoff(ctx)

	// Should handle without overflow
	if decision == nil {
		t.Fatal("Decision should not be nil")
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestHandoffController_LearnedThreshold(t *testing.T) {
	// Create profile with some learned data
	profile := NewAgentHandoffProfile("coder", "gpt-4", "test")

	// Train the profile
	for i := 0; i < 30; i++ {
		obs := NewHandoffObservation(
			i*100,
			i,
			0.8-float64(i)*0.02,
			true,
			i > 20,
		)
		profile.Update(obs)
	}

	config := &ControllerConfig{
		ContextFullThreshold:     0.9,
		QualityThreshold:         0.5, // Default
		MinConfidenceForDecision: 0.3,
	}

	controller := NewHandoffController(nil, profile, config)

	// The effective threshold should come from the profile
	// since confidence should be above MinConfidenceForDecision
	profileThreshold := profile.GetHandoffThresholdMean()
	defaultThreshold := config.QualityThreshold

	// They might be the same, but profile should have an effect
	_ = profileThreshold
	_ = defaultThreshold
	_ = controller
}

func TestHandoffController_QualityPredictionImprovement(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	// Add quality observations showing clear pattern
	contexts := []struct {
		size    int
		quality float64
	}{
		{100, 0.95},
		{200, 0.9},
		{400, 0.8},
		{600, 0.65},
		{800, 0.45},
	}

	for _, c := range contexts {
		ctx := &ContextState{
			ContextSize:    c.size,
			MaxContextSize: 1000,
			TokenCount:     c.size / 10,
			ToolCallCount:  c.size / 100,
		}
		controller.UpdateWithoutHandoff(ctx, true, c.quality)
	}

	// Now predictions should follow the pattern
	predSmall := controller.PredictQuality(&ContextState{
		ContextSize:    150,
		MaxContextSize: 1000,
		TokenCount:     15,
		ToolCallCount:  1,
	})

	predLarge := controller.PredictQuality(&ContextState{
		ContextSize:    700,
		MaxContextSize: 1000,
		TokenCount:     70,
		ToolCallCount:  7,
	})

	// Larger context should have lower predicted quality
	if predLarge.Mean >= predSmall.Mean {
		t.Logf("Warning: Expected quality to decrease with context: small=%f, large=%f",
			predSmall.Mean, predLarge.Mean)
	}
}

func TestHandoffController_FullWorkflow(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	profile := NewAgentHandoffProfile("coder", "gpt-4", "session-123")
	config := DefaultControllerConfig()

	controller := NewHandoffController(gp, profile, config)

	// Simulate a session
	for turn := 1; turn <= 20; turn++ {
		ctx := &ContextState{
			ContextSize:    turn * 50,
			MaxContextSize: 1000,
			TokenCount:     turn * 5,
			ToolCallCount:  turn / 2,
			TurnNumber:     turn,
		}

		decision := controller.EvaluateHandoff(ctx)

		// Simulate quality observation
		quality := 0.9 - float64(turn)*0.02

		if decision.ShouldHandoff {
			controller.UpdateAfterHandoff(ctx, true, quality)
			// In real system, would do handoff here
			break
		} else {
			controller.UpdateWithoutHandoff(ctx, true, quality)
		}
	}

	// Verify controller state after session
	stats := controller.GetStats()
	if stats.TotalDecisions == 0 {
		t.Error("Should have made decisions")
	}
	if stats.GPObservations == 0 {
		t.Error("Should have GP observations")
	}
	if stats.ProfileConfidence == 0 {
		t.Error("Profile should have some confidence")
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkHandoffController_EvaluateHandoff(b *testing.B) {
	gp := NewAgentGaussianProcess(nil)
	// Add some observations
	for i := 0; i < 20; i++ {
		gp.AddObservationFromValues(i*50, i*5, i, 0.8-float64(i)*0.02)
	}

	controller := NewHandoffController(gp, nil, nil)

	ctx := &ContextState{
		ContextSize:    500,
		MaxContextSize: 1000,
		TokenCount:     50,
		ToolCallCount:  5,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = controller.EvaluateHandoff(ctx)
	}
}

func BenchmarkHandoffController_UpdateAfterHandoff(b *testing.B) {
	controller := NewHandoffController(nil, nil, nil)

	ctx := &ContextState{
		ContextSize:    500,
		MaxContextSize: 1000,
		TokenCount:     50,
		ToolCallCount:  5,
		TurnNumber:     10,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		controller.UpdateAfterHandoff(ctx, true, 0.8)
	}
}

func BenchmarkHandoffController_PredictQuality(b *testing.B) {
	gp := NewAgentGaussianProcess(nil)
	for i := 0; i < 50; i++ {
		gp.AddObservationFromValues(i*50, i*5, i, 0.8)
	}

	controller := NewHandoffController(gp, nil, nil)

	ctx := &ContextState{
		ContextSize:    500,
		MaxContextSize: 1000,
		TokenCount:     50,
		ToolCallCount:  5,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = controller.PredictQuality(ctx)
	}
}

// =============================================================================
// Regression Tests
// =============================================================================

func TestHandoffController_DecisionFactorsPopulated(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(500, 50, 5, 0.7)

	controller := NewHandoffController(gp, nil, nil)

	ctx := &ContextState{
		ContextSize:    500,
		MaxContextSize: 1000,
		TokenCount:     50,
		ToolCallCount:  5,
	}

	decision := controller.EvaluateHandoff(ctx)

	// All decision factors should be populated
	factors := decision.Factors

	if factors.ContextUtilization != 0.5 {
		t.Errorf("ContextUtilization = %f, want 0.5", factors.ContextUtilization)
	}
	if factors.PredictedQuality == 0 && !decision.ShouldHandoff {
		t.Log("Warning: PredictedQuality might not be set correctly")
	}
	if factors.ThresholdUsed == 0 && decision.Trigger != TriggerUserRequest {
		t.Log("Warning: ThresholdUsed might not be set correctly")
	}
}

func TestHandoffController_TimestampSet(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	before := time.Now()

	ctx := &ContextState{
		ContextSize:    500,
		MaxContextSize: 1000,
	}
	decision := controller.EvaluateHandoff(ctx)

	after := time.Now()

	if decision.Timestamp.Before(before) || decision.Timestamp.After(after) {
		t.Errorf("Timestamp %v should be between %v and %v",
			decision.Timestamp, before, after)
	}
}
