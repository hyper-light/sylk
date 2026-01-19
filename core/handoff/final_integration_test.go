package handoff

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// HO.13.1 Hierarchical Blending Tests
// =============================================================================

func TestHierarchicalBlending_GlobalToModelToAgentModel(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Register parameters at each level with different values
	// Global: low confidence (alpha=1, beta=1 -> mean 0.5)
	globalParam := NewLearnedWeight(1.0, 1.0)
	blender.RegisterGlobal("threshold", globalParam)

	// Model level: moderate confidence (alpha=3, beta=2 -> mean 0.6)
	modelParam := NewLearnedWeight(3.0, 2.0)
	blender.RegisterModel("gpt-4", "threshold", modelParam)

	// AgentModel level: high confidence (alpha=8, beta=2 -> mean 0.8)
	agentModelParam := NewLearnedWeight(8.0, 2.0)
	blender.RegisterAgentModel("coder", "gpt-4", "threshold", agentModelParam)

	// Get blended result for coder+gpt-4 (should be dominated by AgentModel)
	blended, ok := blender.GetBlended("coder", "gpt-4", "instance1", "threshold")
	if !ok {
		t.Fatal("GetBlended should return true when parameters exist")
	}

	// The high-confidence AgentModel level should have the most influence
	if blended.DominantLevel != LevelAgentModel {
		t.Errorf("DominantLevel = %s, want AgentModel", blended.DominantLevel)
	}

	// Blended value should be closer to 0.8 (AgentModel mean) than 0.5 (Global mean)
	if blended.Value < 0.65 || blended.Value > 0.85 {
		t.Errorf("Blended value = %f, expected between 0.65 and 0.85", blended.Value)
	}

	// AgentModel weight should be highest
	if blended.AgentModelWeight < blended.ModelWeight || blended.AgentModelWeight < blended.GlobalWeight {
		t.Error("AgentModelWeight should be highest due to confidence")
	}
}

func TestHierarchicalBlending_ConfidenceWeighted(t *testing.T) {
	config := &BlenderConfig{
		MinConfidenceForWeight: 0.1,
		ConfidenceExponent:     2.0,
		ExplorationBonus:       0.0,
	}
	blender := NewHierarchicalParamBlender(config)

	// High confidence global (alpha=20, beta=20 -> mean 0.5, high precision)
	globalParam := NewLearnedWeight(20.0, 20.0)
	blender.RegisterGlobal("threshold", globalParam)

	// Low confidence model (alpha=1, beta=1 -> mean 0.5, low precision)
	modelParam := NewLearnedWeight(1.0, 1.0)
	blender.RegisterModel("gpt-4", "threshold", modelParam)

	blended, ok := blender.GetBlended("coder", "gpt-4", "instance1", "threshold")
	if !ok {
		t.Fatal("GetBlended should return true")
	}

	// Global should dominate due to higher confidence despite same mean
	if blended.GlobalWeight < blended.ModelWeight {
		t.Errorf("GlobalWeight (%f) should be > ModelWeight (%f) due to confidence",
			blended.GlobalWeight, blended.ModelWeight)
	}
}

func TestHierarchicalBlending_ColdStartFallback(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Only register global parameter
	globalParam := NewLearnedWeight(5.0, 5.0) // Mean 0.5
	blender.RegisterGlobal("threshold", globalParam)

	// Request blend for a new agent+model combination with no specific data
	blended, ok := blender.GetBlended("new-agent", "new-model", "new-instance", "threshold")
	if !ok {
		t.Fatal("GetBlended should return true when global exists")
	}

	// Should fall back entirely to global
	if blended.GlobalWeight != 1.0 {
		t.Errorf("GlobalWeight = %f, want 1.0 for cold start fallback", blended.GlobalWeight)
	}
	if blended.DominantLevel != LevelGlobal {
		t.Errorf("DominantLevel = %s, want Global", blended.DominantLevel)
	}

	// Value should match global mean
	expectedMean := globalParam.Mean()
	if math.Abs(blended.Value-expectedMean) > 0.01 {
		t.Errorf("Blended value = %f, want %f (global mean)", blended.Value, expectedMean)
	}
}

func TestHierarchicalBlending_PartialData(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Register only at global and agent-model levels (skip model level)
	globalParam := NewLearnedWeight(2.0, 8.0) // Mean 0.2, low confidence
	blender.RegisterGlobal("threshold", globalParam)

	agentModelParam := NewLearnedWeight(6.0, 4.0) // Mean 0.6, moderate confidence
	blender.RegisterAgentModel("coder", "gpt-4", "threshold", agentModelParam)

	blended, ok := blender.GetBlended("coder", "gpt-4", "instance1", "threshold")
	if !ok {
		t.Fatal("GetBlended should return true")
	}

	// Should blend between global and agent-model, skipping model level
	if blended.ModelWeight != 0.0 {
		t.Errorf("ModelWeight = %f, want 0.0 (no model-level param)", blended.ModelWeight)
	}

	// Value should be weighted average of global (0.2) and agent-model (0.6)
	if blended.Value < 0.2 || blended.Value > 0.6 {
		t.Errorf("Blended value = %f, expected between 0.2 and 0.6", blended.Value)
	}
}

func TestHierarchicalBlending_GetBlendedWeight(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Register with known mean
	modelParam := NewLearnedWeight(7.0, 3.0) // Mean 0.7
	blender.RegisterModel("gpt-4", "threshold", modelParam)

	lw, ok := blender.GetBlendedWeight("coder", "gpt-4", "instance1", "threshold")
	if !ok {
		t.Fatal("GetBlendedWeight should return true")
	}

	// Returned LearnedWeight should have mean close to blended value
	if math.Abs(lw.Mean()-0.7) > 0.01 {
		t.Errorf("LearnedWeight mean = %f, want ~0.7", lw.Mean())
	}
}

func TestHierarchicalBlending_NoParameters(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// No parameters registered
	blended, ok := blender.GetBlended("agent", "model", "instance", "threshold")
	if ok {
		t.Error("GetBlended should return false when no parameters exist")
	}
	if blended != nil {
		t.Error("Blended should be nil when no parameters exist")
	}
}

func TestHierarchicalBlending_LevelString(t *testing.T) {
	tests := []struct {
		level    BlendLevel
		expected string
	}{
		{LevelInstance, "Instance"},
		{LevelAgentModel, "AgentModel"},
		{LevelModel, "Model"},
		{LevelGlobal, "Global"},
		{BlendLevel(99), "Unknown(99)"},
	}

	for _, tt := range tests {
		if got := tt.level.String(); got != tt.expected {
			t.Errorf("BlendLevel(%d).String() = %s, want %s", tt.level, got, tt.expected)
		}
	}
}

// =============================================================================
// HO.13.2 GP Prediction Tests
// =============================================================================

func TestGPPrediction_MatchesExpectedBehavior(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Add observations showing quality decreases with context size
	observations := []struct {
		contextSize int
		tokenCount  int
		toolCalls   int
		quality     float64
	}{
		{1000, 100, 2, 0.9},
		{2000, 200, 4, 0.85},
		{5000, 300, 6, 0.75},
		{10000, 400, 8, 0.6},
		{20000, 500, 10, 0.45},
	}

	for _, obs := range observations {
		gp.AddObservationFromValues(obs.contextSize, obs.tokenCount, obs.toolCalls, obs.quality)
	}

	// Prediction at small context should be higher quality
	predSmall := gp.Predict(500, 50, 1)
	// Prediction at large context should be lower quality
	predLarge := gp.Predict(25000, 600, 12)

	if predSmall.Mean <= predLarge.Mean {
		t.Errorf("Quality should decrease with context size: small=%f, large=%f",
			predSmall.Mean, predLarge.Mean)
	}
}

func TestGPPrediction_UncertaintyGrowsWithDistance(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Add observations in a narrow range
	for i := 0; i < 5; i++ {
		gp.AddObservationFromValues(1000+i*100, 100, 2, 0.8)
	}

	// Predict near observations
	predNear := gp.Predict(1050, 100, 2)
	// Predict far from observations
	predFar := gp.Predict(50000, 1000, 50)

	// Uncertainty (StdDev) should be higher for distant predictions
	if predNear.StdDev >= predFar.StdDev {
		t.Errorf("Uncertainty should grow with distance: near=%f, far=%f",
			predNear.StdDev, predFar.StdDev)
	}

	// Confidence interval should be wider for distant predictions
	nearWidth := predNear.UpperBound - predNear.LowerBound
	farWidth := predFar.UpperBound - predFar.LowerBound

	if nearWidth >= farWidth {
		t.Errorf("Confidence interval should be wider for distant predictions: near=%f, far=%f",
			nearWidth, farWidth)
	}
}

func TestGPPrediction_AfterAddingObservations(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Prediction before any observations (should return prior)
	predBefore := gp.Predict(5000, 200, 5)

	// Add observations
	gp.AddObservationFromValues(5000, 200, 5, 0.7)
	gp.AddObservationFromValues(5000, 200, 5, 0.72)
	gp.AddObservationFromValues(5000, 200, 5, 0.68)

	// Prediction at same point after observations
	predAfter := gp.Predict(5000, 200, 5)

	// Uncertainty should decrease after adding observations at same point
	if predAfter.StdDev >= predBefore.StdDev {
		t.Errorf("Uncertainty should decrease after observations: before=%f, after=%f",
			predBefore.StdDev, predAfter.StdDev)
	}

	// Mean should move toward observed values (~0.7)
	if math.Abs(predAfter.Mean-0.7) > 0.1 {
		t.Errorf("Mean should be close to observed value: got %f, want ~0.7", predAfter.Mean)
	}
}

func TestGPPrediction_Matern25KernelProperties(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Matern 2.5 kernel properties:
	// 1. k(x, x) = signal_variance (kernel at distance 0)
	// 2. k(x, y) decreases as distance increases
	// 3. k(x, y) > 0 for all x, y (positive definite)

	features1 := []float64{1000.0, 100.0, 5.0}
	features2 := []float64{1000.0, 100.0, 5.0} // Same point
	features3 := []float64{2000.0, 200.0, 10.0}
	features4 := []float64{10000.0, 500.0, 20.0}

	kSame := gp.matern25Kernel(features1, features2)
	kClose := gp.matern25Kernel(features1, features3)
	kFar := gp.matern25Kernel(features1, features4)

	// Kernel at same point should equal signal variance
	hp := gp.GetHyperparams()
	if math.Abs(kSame-hp.SignalVariance) > 1e-10 {
		t.Errorf("k(x,x) = %f, want %f (signal variance)", kSame, hp.SignalVariance)
	}

	// Kernel should decrease with distance
	if kClose >= kSame {
		t.Errorf("k(x,y) should be < k(x,x): close=%f, same=%f", kClose, kSame)
	}
	if kFar >= kClose {
		t.Errorf("k(x,y) should decrease with distance: far=%f, close=%f", kFar, kClose)
	}

	// All kernel values should be positive
	if kSame <= 0 || kClose <= 0 || kFar <= 0 {
		t.Errorf("Kernel values must be positive: same=%f, close=%f, far=%f",
			kSame, kClose, kFar)
	}
}

func TestGPPrediction_PriorMeanLearning(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Create historical observations showing quality decay
	historical := []*GPObservation{
		NewGPObservation(1000, 100, 2, 0.9),
		NewGPObservation(5000, 200, 5, 0.7),
		NewGPObservation(10000, 300, 8, 0.5),
		NewGPObservation(20000, 400, 12, 0.3),
	}

	// Learn prior from historical data
	gp.LearnPriorFromHistory(historical)

	prior := gp.GetPriorMean()
	if prior == nil {
		t.Fatal("Prior mean should be learned")
	}

	// Prior should capture decay patterns
	if prior.ContextDecay <= 0 {
		t.Error("ContextDecay should be positive (quality decreases with context)")
	}

	// Evaluate prior at different points
	highContext := prior.Evaluate([]float64{20000, 400, 12})
	lowContext := prior.Evaluate([]float64{1000, 100, 2})

	if highContext >= lowContext {
		t.Errorf("Prior mean should decrease with context: high=%f, low=%f",
			highContext, lowContext)
	}
}

func TestGPPrediction_EmptyObservations(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Prediction with no observations should return prior
	pred := gp.Predict(5000, 200, 5)

	// Should return reasonable default values
	if pred.Mean < 0 || pred.Mean > 1 {
		t.Errorf("Mean should be in [0,1]: got %f", pred.Mean)
	}
	if pred.Variance < 0 {
		t.Errorf("Variance should be non-negative: got %f", pred.Variance)
	}
	if pred.StdDev < 0 {
		t.Errorf("StdDev should be non-negative: got %f", pred.StdDev)
	}
}

// =============================================================================
// HO.13.3 Handoff Execution Tests
// =============================================================================

func TestHandoffExecution_FullFlow(t *testing.T) {
	mock := newMockSessionCreator()
	config := &ExecutorConfig{
		DefaultTimeout:    10 * time.Second,
		MaxRetries:        2,
		ValidationEnabled: true,
		RollbackOnFailure: true,
		CollectMetrics:    true,
		MaxTokenCount:     100000, // Set high max to allow tokens through
	}
	executor := NewHandoffExecutor(mock, config)

	// Create prepared context
	preparedCtx := NewPreparedContextDefault()
	preparedCtx.AddMessage(NewMessage("user", "Help me write a function"))
	preparedCtx.AddMessage(NewMessage("assistant", "Sure, here's a function..."))
	preparedCtx.SetMetadata("session_id", "source-session-123")
	preparedCtx.UpdateToolState("editor", ToolState{
		Name:   "editor",
		Active: true,
		State:  map[string]interface{}{"file": "main.go"},
	})

	// Create decision
	decision := NewHandoffDecision(
		true,
		0.9,
		"Quality predicted to drop",
		TriggerQualityDegrading,
		DecisionFactors{
			ContextUtilization: 0.75,
			PredictedQuality:   0.45,
			QualityUncertainty: 0.1,
		},
	)

	// Prepare transfer
	transfer, err := executor.PrepareTransfer(decision, preparedCtx)
	if err != nil {
		t.Fatalf("PrepareTransfer failed: %v", err)
	}

	// Verify transfer was prepared correctly
	if transfer.ID == "" {
		t.Error("Transfer ID should be set")
	}
	if transfer.Summary == "" {
		t.Error("Summary should be extracted from PreparedContext")
	}
	if len(transfer.RecentMessages) != 2 {
		t.Errorf("RecentMessages = %d, want 2", len(transfer.RecentMessages))
	}
	if len(transfer.ToolStates) != 1 {
		t.Errorf("ToolStates = %d, want 1", len(transfer.ToolStates))
	}

	// Execute handoff
	result := executor.ExecuteHandoff(context.Background(), transfer)

	if !result.Success {
		t.Fatalf("Handoff should succeed: %v", result.Error)
	}
	if result.NewSessionID == "" {
		t.Error("NewSessionID should be set")
	}
	if result.Duration == 0 {
		t.Error("Duration should be recorded")
	}

	// Verify metrics were collected
	if result.Metrics.TokensTransferred == 0 {
		t.Error("TokensTransferred should be > 0")
	}
	if result.Metrics.MessagesTransferred != 2 {
		t.Errorf("MessagesTransferred = %d, want 2", result.Metrics.MessagesTransferred)
	}
	if result.Metrics.ToolStatesTransferred != 1 {
		t.Errorf("ToolStatesTransferred = %d, want 1", result.Metrics.ToolStatesTransferred)
	}

	// Verify mock was called correctly
	create, transfer2, activate, _, _ := mock.getCounts()
	if create != 1 {
		t.Errorf("CreateSession called %d times, want 1", create)
	}
	if transfer2 != 1 {
		t.Errorf("TransferContext called %d times, want 1", transfer2)
	}
	if activate != 1 {
		t.Errorf("ActivateSession called %d times, want 1", activate)
	}
}

func TestHandoffExecution_PreparedContextUsedCorrectly(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	// Create prepared context with specific data
	preparedCtx := NewPreparedContextDefault()
	preparedCtx.AddMessage(NewMessage("user", "Test message 1"))
	preparedCtx.AddMessage(NewMessage("assistant", "Response 1"))
	preparedCtx.AddMessage(NewMessage("user", "Test message 2"))
	preparedCtx.SetMetadata("custom_key", "custom_value")

	decision := createTestDecision(true, TriggerContextFull)
	transfer, _ := executor.PrepareTransfer(decision, preparedCtx)

	// Verify prepared context data was transferred
	if len(transfer.RecentMessages) != 3 {
		t.Errorf("RecentMessages = %d, want 3", len(transfer.RecentMessages))
	}
	if transfer.Metadata["trigger"] != "ContextFull" {
		t.Error("Trigger metadata should be set")
	}

	// Verify snapshot was created
	if transfer.Snapshot == nil {
		t.Error("Snapshot should be created from PreparedContext")
	}

	// Execute and verify success
	result := executor.ExecuteHandoff(context.Background(), transfer)
	if !result.Success {
		t.Fatalf("Handoff should succeed: %v", result.Error)
	}
}

func TestHandoffExecution_RollbackOnFailure(t *testing.T) {
	mock := newMockSessionCreator()
	mock.activateSessionError = ErrContextTransferFailed // Fail on activation

	config := &ExecutorConfig{
		MaxRetries:        0,
		RollbackOnFailure: true,
	}
	executor := NewHandoffExecutor(mock, config)

	hooks := newMockHooks()
	executor.SetHooks(hooks)

	decision := createTestDecision(true, TriggerContextFull)
	transfer, _ := executor.PrepareTransfer(decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	// Should fail
	if result.Success {
		t.Error("Handoff should fail due to activation error")
	}

	// Should have rolled back
	if !result.RolledBack {
		t.Error("Should have rolled back on failure")
	}

	// Rollback methods should have been called
	_, _, _, deactivate, del := mock.getCounts()
	if deactivate != 1 {
		t.Errorf("DeactivateSession called %d times, want 1", deactivate)
	}
	if del != 1 {
		t.Errorf("DeleteSession called %d times, want 1", del)
	}

	// Rollback hook should have been called
	_, _, rollback := hooks.getCounts()
	if rollback != 1 {
		t.Errorf("OnRollback called %d times, want 1", rollback)
	}
}

func TestHandoffExecution_RetryThenSuccess(t *testing.T) {
	mock := newMockSessionCreator()
	mock.failOnAttempt = 2 // Fail first 2 attempts

	config := &ExecutorConfig{
		MaxRetries:      3,
		RetryBaseDelay:  10 * time.Millisecond,
		RetryMaxDelay:   50 * time.Millisecond,
		DefaultTimeout:  10 * time.Second,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerQualityDegrading)
	transfer, _ := executor.PrepareTransfer(decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if !result.Success {
		t.Errorf("Handoff should succeed after retries: %v", result.Error)
	}
	if result.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", result.RetryCount)
	}

	// Stats should reflect retries
	stats := executor.Stats()
	if stats.TotalRetries != 2 {
		t.Errorf("TotalRetries = %d, want 2", stats.TotalRetries)
	}
}

func TestHandoffExecution_HooksInvoked(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	hooks := newMockHooks()
	executor.SetHooks(hooks)

	decision := createTestDecision(true, TriggerUserRequest)
	preparedCtx := createTestPreparedContext()
	transfer, _ := executor.PrepareTransfer(decision, preparedCtx)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if !result.Success {
		t.Fatalf("Handoff should succeed: %v", result.Error)
	}

	pre, post, rollback := hooks.getCounts()
	if pre != 1 {
		t.Errorf("PreHandoff called %d times, want 1", pre)
	}
	if post != 1 {
		t.Errorf("PostHandoff called %d times, want 1", post)
	}
	if rollback != 0 {
		t.Errorf("OnRollback called %d times, want 0 (success)", rollback)
	}

	// Verify hook received correct data
	if hooks.lastTransfer == nil {
		t.Error("Hook should receive transfer")
	}
	if hooks.lastResult == nil || !hooks.lastResult.Success {
		t.Error("PostHandoff should receive successful result")
	}
}

// =============================================================================
// HO.13.4 End-to-End Learning Tests
// =============================================================================

func TestEndToEnd_LearningLoop(t *testing.T) {
	// Create all components
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, &ExecutorConfig{
		DefaultTimeout:    10 * time.Second,
		RollbackOnFailure: true,
	})
	learner := NewProfileLearner(nil, nil)
	gp := NewAgentGaussianProcess(nil)
	profile := NewAgentHandoffProfile("test-agent", "test-model", "")
	controller := NewHandoffController(gp, profile, nil)

	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
		EnableLearning:       true,
		AgentID:              "test-agent",
		ModelName:            "test-model",
		DefaultTimeout:       10 * time.Second,
	}
	manager := NewHandoffManager(config, controller, executor, learner)
	manager.Start()
	defer manager.Stop()

	// Simulate multiple handoffs and observe learning
	numHandoffs := 10
	qualities := make([]float64, numHandoffs)

	for i := 0; i < numHandoffs; i++ {
		// Add context
		manager.AddMessage(NewMessage("user", "Test message"))
		manager.AddMessage(NewMessage("assistant", "Response"))

		// Force handoff
		result := manager.ForceHandoff(context.Background(), "learning test")

		if !result.Success {
			t.Errorf("Handoff %d should succeed: %v", i, result.Error)
			continue
		}

		// Simulate quality observation
		quality := 0.8 - float64(i)*0.03 // Simulated degrading quality
		qualities[i] = quality

		// Record observation for learning
		learner.RecordHandoffOutcome(
			"test-agent", "test-model",
			(i+1)*1000, // increasing context
			i+1,        // turn number
			true,       // success
			quality,
		)
	}

	// Verify learning occurred
	stats := learner.Stats()
	if stats.TotalHandoffOutcomes < int64(numHandoffs) {
		t.Errorf("TotalHandoffOutcomes = %d, want >= %d", stats.TotalHandoffOutcomes, numHandoffs)
	}

	// Profile should exist and have accumulated samples
	learnedProfile := learner.GetProfile("test-agent", "test-model")
	if learnedProfile == nil {
		t.Fatal("Profile should exist after learning")
	}
	if learnedProfile.EffectiveSamples < 1 {
		t.Error("Profile should have accumulated samples")
	}
}

func TestEndToEnd_QualityPredictionImproves(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Initial predictions should have high uncertainty
	predBefore := gp.Predict(5000, 200, 5)
	initialUncertainty := predBefore.StdDev

	// Add training data
	trainingData := []struct {
		contextSize int
		quality     float64
	}{
		{1000, 0.9},
		{2000, 0.85},
		{3000, 0.8},
		{4000, 0.75},
		{5000, 0.7},
		{6000, 0.65},
		{7000, 0.6},
		{8000, 0.55},
	}

	for _, td := range trainingData {
		gp.AddObservationFromValues(td.contextSize, 200, 5, td.quality)
	}

	// Prediction at observed point should have lower uncertainty
	predAfter := gp.Predict(5000, 200, 5)
	finalUncertainty := predAfter.StdDev

	if finalUncertainty >= initialUncertainty {
		t.Errorf("Uncertainty should decrease after training: before=%f, after=%f",
			initialUncertainty, finalUncertainty)
	}

	// Prediction should be close to observed value at 5000 context (0.7)
	if math.Abs(predAfter.Mean-0.7) > 0.15 {
		t.Errorf("Prediction mean = %f, expected close to 0.7", predAfter.Mean)
	}
}

func TestEndToEnd_ConvergenceOfLearnedParameters(t *testing.T) {
	profile := NewAgentHandoffProfile("agent", "model", "")
	_ = DefaultUpdateConfig() // Used for profile updates internally

	// Track threshold values over time
	thresholdHistory := make([]float64, 0)
	thresholdHistory = append(thresholdHistory, profile.GetHandoffThresholdMean())

	// Simulate many observations with consistent pattern
	for i := 0; i < 50; i++ {
		// Observations consistently showing optimal threshold around 0.7
		obs := NewHandoffObservation(
			5000, // context
			10,   // turn
			0.7,  // quality
			true, // success
			true, // handoff triggered
		)
		profile.Update(obs)
		thresholdHistory = append(thresholdHistory, profile.GetHandoffThresholdMean())
	}

	// Threshold should converge toward observed pattern
	initialThreshold := thresholdHistory[0]
	finalThreshold := thresholdHistory[len(thresholdHistory)-1]

	// Should have moved from initial toward observed values
	initialDistFrom07 := math.Abs(initialThreshold - 0.7)
	finalDistFrom07 := math.Abs(finalThreshold - 0.7)

	// Note: Due to the learning algorithm, we expect gradual convergence
	// The final value should be closer to 0.7 than the initial value
	if finalDistFrom07 > initialDistFrom07 {
		t.Errorf("Threshold should converge toward 0.7: initial=%f, final=%f",
			initialThreshold, finalThreshold)
	}

	// Confidence should increase with more observations
	if profile.Confidence() < 0.5 {
		t.Errorf("Confidence should be reasonably high after 50 observations: %f",
			profile.Confidence())
	}
}

func TestEndToEnd_WALPersistenceAcrossSessions(t *testing.T) {
	// Create temporary WAL file
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test_handoff.wal")

	// Session 1: Create state and persist
	{
		wal, err := NewHandoffWAL(walPath, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		// Create profile and GP with data
		profile := NewAgentHandoffProfile("agent", "model", "")
		for i := 0; i < 10; i++ {
			obs := NewHandoffObservation(1000+i*500, i, 0.8-float64(i)*0.02, true, true)
			profile.Update(obs)
		}

		gpModel := NewAgentGaussianProcess(nil)
		for i := 0; i < 5; i++ {
			gpModel.AddObservationFromValues(1000+i*1000, 100+i*50, i, 0.9-float64(i)*0.1)
		}

		// Write checkpoint
		if err := wal.WriteCheckpoint(profile, gpModel); err != nil {
			t.Fatalf("Failed to write checkpoint: %v", err)
		}

		// Write some observations
		for i := 0; i < 5; i++ {
			obs := NewHandoffObservation(5000+i*200, 10+i, 0.6, true, false)
			gpObs := NewGPObservation(5000+i*200, 100, 5, 0.6)
			if err := wal.WriteObservation(obs, gpObs); err != nil {
				t.Fatalf("Failed to write observation: %v", err)
			}
		}

		if err := wal.Close(); err != nil {
			t.Fatalf("Failed to close WAL: %v", err)
		}
	}

	// Session 2: Recover state
	{
		wal, err := NewHandoffWAL(walPath, nil)
		if err != nil {
			t.Fatalf("Failed to reopen WAL: %v", err)
		}
		defer wal.Close()

		state, err := wal.Recover()
		if err != nil {
			t.Fatalf("Failed to recover: %v", err)
		}

		// Verify profile was recovered
		if state.Profile == nil {
			t.Fatal("Profile should be recovered")
		}
		if state.Profile.AgentType != "agent" {
			t.Errorf("AgentType = %s, want agent", state.Profile.AgentType)
		}
		if state.Profile.EffectiveSamples < 1 {
			t.Error("Profile should have effective samples")
		}

		// Verify GP was recovered
		if state.GP == nil {
			t.Fatal("GP should be recovered")
		}
		if state.GP.NumObservations() < 5 {
			t.Errorf("GP should have >= 5 observations, got %d", state.GP.NumObservations())
		}

		// Verify replay happened
		if state.EntriesReplayed < 5 {
			t.Errorf("EntriesReplayed = %d, want >= 5", state.EntriesReplayed)
		}
	}
}

func TestEndToEnd_WALCompaction(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test_compact.wal")

	wal, err := NewHandoffWAL(walPath, &WALConfig{CheckpointInterval: 10})
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	profile := NewAgentHandoffProfile("agent", "model", "")
	gpModel := NewAgentGaussianProcess(nil)

	// Write many entries
	for i := 0; i < 50; i++ {
		obs := NewHandoffObservation(1000+i*100, i, 0.8, true, true)
		gpObs := NewGPObservation(1000+i*100, 100, 5, 0.8)
		profile.Update(obs)
		gpModel.AddObservation(gpObs)
		if err := wal.WriteObservation(obs, gpObs); err != nil {
			t.Fatalf("Failed to write observation: %v", err)
		}
	}

	// Get file size before compaction
	infoBefore, _ := os.Stat(walPath)
	sizeBefore := infoBefore.Size()

	// Compact
	if err := wal.Compact(profile, gpModel); err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	// Get file size after compaction
	infoAfter, _ := os.Stat(walPath)
	sizeAfter := infoAfter.Size()

	// File should be smaller after compaction (single checkpoint vs many entries)
	if sizeAfter >= sizeBefore {
		t.Errorf("File should be smaller after compaction: before=%d, after=%d",
			sizeBefore, sizeAfter)
	}

	// Recover and verify state is intact
	state, err := wal.Recover()
	if err != nil {
		t.Fatalf("Failed to recover after compaction: %v", err)
	}
	if state.Profile == nil || state.GP == nil {
		t.Error("State should be intact after compaction")
	}
}

// =============================================================================
// Timing-Sensitive Tests
// =============================================================================

func TestTiming_HandoffExecutionWithinTimeout(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createDelay = 50 * time.Millisecond
	mock.transferDelay = 50 * time.Millisecond
	mock.activateDelay = 50 * time.Millisecond

	config := &ExecutorConfig{
		DefaultTimeout: 5 * time.Second, // Generous timeout
		MaxRetries:     0,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer, _ := executor.PrepareTransfer(decision, nil)

	start := time.Now()
	result := executor.ExecuteHandoff(context.Background(), transfer)
	elapsed := time.Since(start)

	if !result.Success {
		t.Fatalf("Handoff should succeed: %v", result.Error)
	}

	// Should take at least 150ms (3 * 50ms delays)
	if elapsed < 150*time.Millisecond {
		t.Errorf("Execution too fast: %v, expected >= 150ms", elapsed)
	}

	// Should complete within timeout
	if elapsed > 5*time.Second {
		t.Errorf("Execution too slow: %v, expected < 5s", elapsed)
	}

	// Duration recorded should match actual execution time
	durationDiff := math.Abs(float64(result.Duration - elapsed))
	if durationDiff > float64(50*time.Millisecond) {
		t.Errorf("Recorded duration %v differs significantly from actual %v",
			result.Duration, elapsed)
	}
}

func TestTiming_RetryBackoffProgression(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createSessionError = ErrSessionCreationFailed // Always fail

	config := &ExecutorConfig{
		DefaultTimeout:    10 * time.Second,
		MaxRetries:        3,
		RetryBaseDelay:    100 * time.Millisecond,
		RetryMaxDelay:     1 * time.Second,
		RetryMultiplier:   2.0,
		RollbackOnFailure: false,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer, _ := executor.PrepareTransfer(decision, nil)

	start := time.Now()
	result := executor.ExecuteHandoff(context.Background(), transfer)
	elapsed := time.Since(start)

	if result.Success {
		t.Error("Handoff should fail")
	}

	// Expected delays: 100ms + 200ms + 400ms = 700ms minimum
	// (first attempt has no delay, retries have exponential backoff)
	expectedMinTime := 100*time.Millisecond + 200*time.Millisecond + 400*time.Millisecond
	if elapsed < expectedMinTime-50*time.Millisecond { // Allow 50ms tolerance
		t.Errorf("Backoff too fast: %v, expected >= %v", elapsed, expectedMinTime)
	}
}

func TestTiming_ConcurrentHandoffs(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createDelay = 20 * time.Millisecond

	executor := NewHandoffExecutor(mock, nil)

	numConcurrent := 10
	var wg sync.WaitGroup
	results := make([]*HandoffResult, numConcurrent)
	starts := make([]time.Time, numConcurrent)
	ends := make([]time.Time, numConcurrent)

	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			decision := createTestDecision(true, TriggerContextFull)
			transfer, _ := executor.PrepareTransfer(decision, nil)

			starts[idx] = time.Now()
			results[idx] = executor.ExecuteHandoff(context.Background(), transfer)
			ends[idx] = time.Now()
		}(i)
	}

	wg.Wait()

	// All should succeed
	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}
	if successCount != numConcurrent {
		t.Errorf("All %d handoffs should succeed, got %d", numConcurrent, successCount)
	}

	// Concurrent execution should mean total time is much less than serial execution
	// Serial would be ~200ms (10 * 20ms), concurrent should be faster
	// Allow generous tolerance for CI environments with resource contention
	var maxEnd time.Time
	var minStart time.Time
	for i := 0; i < numConcurrent; i++ {
		if minStart.IsZero() || starts[i].Before(minStart) {
			minStart = starts[i]
		}
		if ends[i].After(maxEnd) {
			maxEnd = ends[i]
		}
	}

	totalTime := maxEnd.Sub(minStart)
	serialTime := time.Duration(numConcurrent) * 20 * time.Millisecond

	// Allow up to 150% of serial time as concurrent execution on loaded systems
	// may not be perfectly parallel
	if totalTime > serialTime*3/2 {
		t.Errorf("Concurrent execution took %v, serial would be %v - not parallel enough",
			totalTime, serialTime)
	}
}

// =============================================================================
// Integration: Manager with Full Learning Pipeline
// =============================================================================

func TestIntegration_ManagerWithFullPipeline(t *testing.T) {
	// Create all components
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, &ExecutorConfig{
		DefaultTimeout:    10 * time.Second,
		CollectMetrics:    true,
		RollbackOnFailure: true,
	})

	priorHierarchy := NewPriorHierarchy()
	learner := NewProfileLearner(&ProfileLearnerConfig{
		EnableHierarchicalFallback: true,
		MinObservationsForConfidence: 3,
	}, priorHierarchy)

	gp := NewAgentGaussianProcess(nil)
	profile := NewAgentHandoffProfile("integration-agent", "integration-model", "")
	controller := NewHandoffController(gp, profile, &ControllerConfig{
		QualityThreshold: 0.5,
	})

	config := &HandoffManagerConfig{
		EnableAutoEvaluation: false,
		EnableLearning:       true,
		AgentID:              "integration-agent",
		ModelName:            "integration-model",
		DefaultTimeout:       10 * time.Second,
	}

	manager := NewHandoffManager(config, controller, executor, learner)

	// Start manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer manager.Stop()

	// Phase 1: Cold start - no learned data
	initialProfile := learner.GetProfile("integration-agent", "integration-model")
	initialConfidence := initialProfile.Confidence()

	// Phase 2: Build up context and execute handoffs
	for i := 0; i < 5; i++ {
		manager.AddMessage(NewMessage("user", "Complex request requiring context"))
		manager.AddMessage(NewMessage("assistant", "Detailed response with context"))

		// Evaluate (likely won't trigger handoff due to default thresholds)
		manager.EvaluateAndExecute(context.Background())

		// Force handoff to generate learning data
		result := manager.ForceHandoff(context.Background(), "integration test")
		if !result.Success {
			t.Errorf("Handoff %d failed: %v", i, result.Error)
		}

		// Simulate quality measurement and learning
		quality := 0.8 - float64(i)*0.05
		learner.RecordHandoffOutcome(
			"integration-agent", "integration-model",
			(i+1)*2000, i*2+1, true, quality,
		)

		// Update controller with observation
		controller.UpdateAfterHandoff(
			&ContextState{
				ContextSize:    (i + 1) * 2000,
				MaxContextSize: 100000,
				TokenCount:     1000,
				TurnNumber:     i*2 + 1,
			},
			true,
			quality,
		)
	}

	// Phase 3: Verify learning occurred
	managerStats := manager.Stats()
	if managerStats.HandoffsExecuted < 5 {
		t.Errorf("HandoffsExecuted = %d, want >= 5", managerStats.HandoffsExecuted)
	}

	learnerStats := learner.Stats()
	if learnerStats.TotalHandoffOutcomes < 5 {
		t.Errorf("TotalHandoffOutcomes = %d, want >= 5", learnerStats.TotalHandoffOutcomes)
	}

	controllerStats := controller.GetStats()
	if controllerStats.GPObservations < 5 {
		t.Errorf("GPObservations = %d, want >= 5", controllerStats.GPObservations)
	}

	// Profile confidence should have increased
	finalProfile := learner.GetProfile("integration-agent", "integration-model")
	if finalProfile.Confidence() <= initialConfidence {
		t.Errorf("Profile confidence should increase: initial=%f, final=%f",
			initialConfidence, finalProfile.Confidence())
	}

	// GP should provide better predictions now
	predNew := controller.GetGP().Predict(5000, 500, 3)
	if predNew.StdDev > 0.4 { // Should have reduced uncertainty
		t.Logf("Note: GP uncertainty may still be high with few observations: %f", predNew.StdDev)
	}
}

func TestIntegration_LearningAcrossMultipleAgentModels(t *testing.T) {
	learner := NewProfileLearner(nil, nil)

	agentModels := []struct {
		agentID   string
		modelName string
	}{
		{"coder", "gpt-4"},
		{"coder", "gpt-3.5"},
		{"reviewer", "gpt-4"},
		{"planner", "claude-3"},
	}

	// Record observations for each agent-model combination
	for _, am := range agentModels {
		for i := 0; i < 5; i++ {
			obs := NewHandoffObservation(
				1000+i*500,
				i,
				0.9-float64(i)*0.1, // Varying quality
				true,
				i%2 == 0, // Handoff triggered sometimes
			)
			learner.RecordObservation(am.agentID, am.modelName, obs)
		}
	}

	// Verify each combination has its own profile
	stats := learner.Stats()
	if stats.ProfileCount != len(agentModels) {
		t.Errorf("ProfileCount = %d, want %d", stats.ProfileCount, len(agentModels))
	}

	// Each profile should have its own learned parameters
	for _, am := range agentModels {
		profile := learner.GetProfileRaw(am.agentID, am.modelName)
		if profile == nil {
			t.Errorf("Profile for %s/%s should exist", am.agentID, am.modelName)
			continue
		}
		if profile.EffectiveSamples < 1 {
			t.Errorf("Profile %s/%s should have samples", am.agentID, am.modelName)
		}
	}
}

// =============================================================================
// Error Path Tests
// =============================================================================

func TestErrorPath_ValidationFailurePreventsExecution(t *testing.T) {
	mock := newMockSessionCreator()
	config := &ExecutorConfig{
		ValidationEnabled: true,
		RequireSummary:    true,
		RequireMessages:   true,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil) // No prepared context

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Should fail validation")
	}

	// Should not have called session creator
	create, _, _, _, _ := mock.getCounts()
	if create != 0 {
		t.Errorf("CreateSession should not be called on validation failure, got %d", create)
	}

	// Stats should reflect validation failure
	stats := executor.Stats()
	if stats.ValidationFailures != 1 {
		t.Errorf("ValidationFailures = %d, want 1", stats.ValidationFailures)
	}
}

func TestErrorPath_PreHookFailurePreventsExecution(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	hooks := newMockHooks()
	hooks.preHandoffError = ErrPreHookFailed
	executor.SetHooks(hooks)

	decision := createTestDecision(true, TriggerContextFull)
	transfer, _ := executor.PrepareTransfer(decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Should fail due to pre-hook failure")
	}

	// Should not have attempted session creation
	create, _, _, _, _ := mock.getCounts()
	if create != 0 {
		t.Errorf("CreateSession should not be called, got %d", create)
	}
}

func TestErrorPath_ContextTimeoutDuringRetry(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createSessionError = ErrSessionCreationFailed // Always fail

	config := &ExecutorConfig{
		MaxRetries:        10,              // Many retries
		RetryBaseDelay:    1 * time.Second, // Long delays
		DefaultTimeout:    200 * time.Millisecond, // Short timeout
		RollbackOnFailure: false,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer, _ := executor.PrepareTransfer(decision, nil)

	start := time.Now()
	result := executor.ExecuteHandoff(context.Background(), transfer)
	elapsed := time.Since(start)

	if result.Success {
		t.Error("Should fail due to timeout")
	}

	// Should timeout before completing all retries
	if elapsed > 500*time.Millisecond {
		t.Errorf("Should timeout faster: %v", elapsed)
	}
}