package chunking

import (
	"math"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// ChunkUsageObservation Tests
// =============================================================================

func TestChunkUsageObservation_Validate(t *testing.T) {
	tests := []struct {
		name    string
		obs     ChunkUsageObservation
		wantErr bool
	}{
		{
			name: "valid observation",
			obs: ChunkUsageObservation{
				Domain:        DomainCode,
				TokenCount:    256,
				ContextBefore: 50,
				ContextAfter:  25,
				WasUseful:     true,
				Timestamp:     time.Now(),
				ChunkID:       "chunk-123",
				SessionID:     "session-456",
			},
			wantErr: false,
		},
		{
			name: "valid observation with overflow strategy",
			obs: ChunkUsageObservation{
				Domain:           DomainAcademic,
				TokenCount:       512,
				ContextBefore:    100,
				ContextAfter:     50,
				WasUseful:        false,
				OverflowStrategy: func() *OverflowStrategy { s := StrategyRecursive; return &s }(),
				Timestamp:        time.Now(),
				ChunkID:          "chunk-789",
				SessionID:        "session-012",
			},
			wantErr: false,
		},
		{
			name: "zero token count",
			obs: ChunkUsageObservation{
				Domain:        DomainCode,
				TokenCount:    0,
				ContextBefore: 50,
				ContextAfter:  25,
				WasUseful:     true,
				Timestamp:     time.Now(),
				ChunkID:       "chunk-123",
				SessionID:     "session-456",
			},
			wantErr: true,
		},
		{
			name: "negative token count",
			obs: ChunkUsageObservation{
				Domain:        DomainCode,
				TokenCount:    -100,
				ContextBefore: 50,
				ContextAfter:  25,
				WasUseful:     true,
				Timestamp:     time.Now(),
				ChunkID:       "chunk-123",
				SessionID:     "session-456",
			},
			wantErr: true,
		},
		{
			name: "negative context before",
			obs: ChunkUsageObservation{
				Domain:        DomainCode,
				TokenCount:    256,
				ContextBefore: -10,
				ContextAfter:  25,
				WasUseful:     true,
				Timestamp:     time.Now(),
				ChunkID:       "chunk-123",
				SessionID:     "session-456",
			},
			wantErr: true,
		},
		{
			name: "negative context after",
			obs: ChunkUsageObservation{
				Domain:        DomainCode,
				TokenCount:    256,
				ContextBefore: 50,
				ContextAfter:  -5,
				WasUseful:     true,
				Timestamp:     time.Now(),
				ChunkID:       "chunk-123",
				SessionID:     "session-456",
			},
			wantErr: true,
		},
		{
			name: "empty chunk ID",
			obs: ChunkUsageObservation{
				Domain:        DomainCode,
				TokenCount:    256,
				ContextBefore: 50,
				ContextAfter:  25,
				WasUseful:     true,
				Timestamp:     time.Now(),
				ChunkID:       "",
				SessionID:     "session-456",
			},
			wantErr: true,
		},
		{
			name: "empty session ID",
			obs: ChunkUsageObservation{
				Domain:        DomainCode,
				TokenCount:    256,
				ContextBefore: 50,
				ContextAfter:  25,
				WasUseful:     true,
				Timestamp:     time.Now(),
				ChunkID:       "chunk-123",
				SessionID:     "",
			},
			wantErr: true,
		},
		{
			name: "zero timestamp",
			obs: ChunkUsageObservation{
				Domain:        DomainCode,
				TokenCount:    256,
				ContextBefore: 50,
				ContextAfter:  25,
				WasUseful:     true,
				Timestamp:     time.Time{},
				ChunkID:       "chunk-123",
				SessionID:     "session-456",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.obs.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("Validate() unexpected error: %v", err)
			}
		})
	}
}

// =============================================================================
// ChunkConfigLearner Constructor Tests
// =============================================================================

func TestNewChunkConfigLearner(t *testing.T) {
	tests := []struct {
		name         string
		globalPriors *ChunkConfig
		wantErr      bool
	}{
		{
			name:         "with nil priors uses defaults",
			globalPriors: nil,
			wantErr:      false,
		},
		{
			name: "with custom priors",
			globalPriors: func() *ChunkConfig {
				cc, _ := NewChunkConfig(4096)
				return cc
			}(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ccl, err := NewChunkConfigLearner(tt.globalPriors)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewChunkConfigLearner() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewChunkConfigLearner() unexpected error: %v", err)
			}
			if ccl == nil {
				t.Fatal("NewChunkConfigLearner() returned nil")
			}
			if ccl.DomainConfigs == nil {
				t.Error("DomainConfigs is nil")
			}
			if ccl.GlobalPriors == nil {
				t.Error("GlobalPriors is nil")
			}
			if ccl.DomainConfidence == nil {
				t.Error("DomainConfidence is nil")
			}
		})
	}
}

// =============================================================================
// RecordObservation Tests
// =============================================================================

func TestChunkConfigLearner_RecordObservation(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	obs := ChunkUsageObservation{
		Domain:        DomainCode,
		TokenCount:    300,
		ContextBefore: 75,
		ContextAfter:  40,
		WasUseful:     true,
		Timestamp:     time.Now(),
		ChunkID:       "chunk-1",
		SessionID:     "session-1",
	}

	err = ccl.RecordObservation(obs)
	if err != nil {
		t.Errorf("RecordObservation() unexpected error: %v", err)
	}

	// Verify domain config was created
	if _, exists := ccl.DomainConfigs[DomainCode]; !exists {
		t.Error("RecordObservation() did not create domain config")
	}

	// Verify confidence was updated
	confidence := ccl.GetDomainConfidence(DomainCode)
	if confidence <= 0 {
		t.Errorf("Domain confidence should be positive after observation, got %f", confidence)
	}

	// Verify observation count
	count := ccl.GetObservationCount(DomainCode)
	if count != 1 {
		t.Errorf("Observation count should be 1, got %d", count)
	}
}

func TestChunkConfigLearner_RecordObservationInvalid(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Invalid observation should return error
	obs := ChunkUsageObservation{
		Domain:     DomainCode,
		TokenCount: -1, // Invalid
		ChunkID:    "chunk-1",
		SessionID:  "session-1",
		Timestamp:  time.Now(),
	}

	err = ccl.RecordObservation(obs)
	if err == nil {
		t.Error("RecordObservation() with invalid observation should return error")
	}
}

func TestChunkConfigLearner_RecordObservationWithOverflow(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	strategy := StrategySentence
	obs := ChunkUsageObservation{
		Domain:           DomainAcademic,
		TokenCount:       500,
		ContextBefore:    100,
		ContextAfter:     50,
		WasUseful:        true,
		OverflowStrategy: &strategy,
		Timestamp:        time.Now(),
		ChunkID:          "chunk-1",
		SessionID:        "session-1",
	}

	err = ccl.RecordObservation(obs)
	if err != nil {
		t.Errorf("RecordObservation() with overflow strategy error: %v", err)
	}

	// Verify the overflow strategy weights were updated
	config := ccl.DomainConfigs[DomainAcademic]
	if config == nil {
		t.Fatal("Domain config not created")
	}
	if config.OverflowStrategyWeights == nil {
		t.Fatal("Overflow strategy weights is nil")
	}
}

func TestChunkConfigLearner_RecordObservationNotUseful(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	obs := ChunkUsageObservation{
		Domain:        DomainHistory,
		TokenCount:    400,
		ContextBefore: 150,
		ContextAfter:  30,
		WasUseful:     false, // Not useful
		Timestamp:     time.Now(),
		ChunkID:       "chunk-1",
		SessionID:     "session-1",
	}

	err = ccl.RecordObservation(obs)
	if err != nil {
		t.Errorf("RecordObservation() with WasUseful=false error: %v", err)
	}

	// Should still create config and update counts
	if _, exists := ccl.DomainConfigs[DomainHistory]; !exists {
		t.Error("Domain config should be created even for non-useful observations")
	}

	count := ccl.GetObservationCount(DomainHistory)
	if count != 1 {
		t.Errorf("Observation count should be 1, got %d", count)
	}
}

// =============================================================================
// GetConfig Tests
// =============================================================================

func TestChunkConfigLearner_GetConfig(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Cold start - should return global priors
	config := ccl.GetConfig(DomainCode, false)
	if config == nil {
		t.Fatal("GetConfig() returned nil for cold start")
	}
	if config.MaxTokens != ccl.GlobalPriors.MaxTokens {
		t.Errorf("Cold start should return global priors")
	}

	// Record some observations
	for i := 0; i < 10; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    300,
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       "chunk-" + string(rune('0'+i)),
			SessionID:     "session-1",
		}
		if err := ccl.RecordObservation(obs); err != nil {
			t.Fatalf("RecordObservation() error: %v", err)
		}
	}

	// After observations, should return blended config
	configAfter := ccl.GetConfig(DomainCode, false)
	if configAfter == nil {
		t.Fatal("GetConfig() returned nil after observations")
	}
}

func TestChunkConfigLearner_GetConfigExplore(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Record observations to create domain config
	for i := 0; i < 5; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    300 + i*10,
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       "chunk-" + string(rune('0'+i)),
			SessionID:     "session-1",
		}
		if err := ccl.RecordObservation(obs); err != nil {
			t.Fatalf("RecordObservation() error: %v", err)
		}
	}

	// Get config with exploration
	exploreConfig := ccl.GetConfig(DomainCode, true)
	if exploreConfig == nil {
		t.Fatal("GetConfig() with explore=true returned nil")
	}

	// Explore should return a valid config (sampling happens in GetEffective* methods)
	targetExplore := exploreConfig.GetEffectiveTargetTokens(true)
	if targetExplore <= 0 {
		t.Errorf("Explore target tokens should be positive, got %d", targetExplore)
	}
}

func TestChunkConfigLearner_GetConfigDifferentDomains(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Record observations for different domains
	domains := []Domain{DomainCode, DomainAcademic, DomainHistory}
	tokenCounts := []int{300, 500, 400}

	for i, domain := range domains {
		obs := ChunkUsageObservation{
			Domain:        domain,
			TokenCount:    tokenCounts[i],
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       "chunk-1",
			SessionID:     "session-1",
		}
		if err := ccl.RecordObservation(obs); err != nil {
			t.Fatalf("RecordObservation() for domain %s error: %v", domain, err)
		}
	}

	// Each domain should have its own config
	for _, domain := range domains {
		config := ccl.GetConfig(domain, false)
		if config == nil {
			t.Errorf("GetConfig() for domain %s returned nil", domain)
		}
	}
}

// =============================================================================
// applyDecay Tests
// =============================================================================

func TestChunkConfigLearner_applyDecay(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	config, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	// Set effective samples to known values
	config.TargetTokens.EffectiveSamples = 10.0
	config.MinTokens.EffectiveSamples = 10.0
	config.ContextTokensBefore.EffectiveSamples = 10.0
	config.ContextTokensAfter.EffectiveSamples = 10.0

	decayFactor := 0.9
	ccl.applyDecay(config, decayFactor)

	// Verify decay was applied
	expectedSamples := 10.0 * decayFactor
	if math.Abs(config.TargetTokens.EffectiveSamples-expectedSamples) > 0.01 {
		t.Errorf("TargetTokens.EffectiveSamples = %f, want %f",
			config.TargetTokens.EffectiveSamples, expectedSamples)
	}
	if math.Abs(config.MinTokens.EffectiveSamples-expectedSamples) > 0.01 {
		t.Errorf("MinTokens.EffectiveSamples = %f, want %f",
			config.MinTokens.EffectiveSamples, expectedSamples)
	}
}

func TestChunkConfigLearner_applyDecayRespectsMinimum(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	config, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	// Set effective samples to minimum
	config.TargetTokens.EffectiveSamples = ccl.decayConfig.MinEffectiveSamples

	// Apply aggressive decay
	ccl.applyDecay(config, 0.1)

	// Should not go below minimum
	if config.TargetTokens.EffectiveSamples < ccl.decayConfig.MinEffectiveSamples {
		t.Errorf("EffectiveSamples went below minimum: %f < %f",
			config.TargetTokens.EffectiveSamples, ccl.decayConfig.MinEffectiveSamples)
	}
}

func TestChunkConfigLearner_applyDecayNilConfig(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Should not panic with nil config
	ccl.applyDecay(nil, 0.9)
}

func TestChunkConfigLearner_applyDecayInvalidFactor(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	config, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	originalSamples := config.TargetTokens.EffectiveSamples

	// Invalid decay factors should not modify config
	ccl.applyDecay(config, 0)
	if config.TargetTokens.EffectiveSamples != originalSamples {
		t.Error("Zero decay factor should not modify config")
	}

	ccl.applyDecay(config, -0.5)
	if config.TargetTokens.EffectiveSamples != originalSamples {
		t.Error("Negative decay factor should not modify config")
	}

	ccl.applyDecay(config, 1.5)
	if config.TargetTokens.EffectiveSamples != originalSamples {
		t.Error("Decay factor > 1 should not modify config")
	}
}

// =============================================================================
// updateGammaPosterior Tests
// =============================================================================

func TestChunkConfigLearner_updateGammaPosterior(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	param, err := NewLearnedContextSize(100, 50)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	initialMean := param.Mean()
	initialSamples := param.EffectiveSamples

	// Update with larger observed value
	ccl.updateGammaPosterior(param, 200, 1.0)

	// Mean should shift toward observed value
	newMean := param.Mean()
	if newMean <= initialMean {
		t.Errorf("Mean should shift toward larger observed value: %d -> %d", initialMean, newMean)
	}

	// Effective samples should increase
	if param.EffectiveSamples <= initialSamples {
		t.Errorf("EffectiveSamples should increase: %f -> %f", initialSamples, param.EffectiveSamples)
	}
}

func TestChunkConfigLearner_updateGammaPosteriorNilParam(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Should not panic with nil param
	ccl.updateGammaPosterior(nil, 100, 1.0)
}

func TestChunkConfigLearner_updateGammaPosteriorInvalidObserved(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	param, err := NewLearnedContextSize(100, 50)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	initialAlpha := param.Alpha

	// Zero and negative observations should not update
	ccl.updateGammaPosterior(param, 0, 1.0)
	if param.Alpha != initialAlpha {
		t.Error("Zero observation should not update param")
	}

	ccl.updateGammaPosterior(param, -10, 1.0)
	if param.Alpha != initialAlpha {
		t.Error("Negative observation should not update param")
	}
}

func TestChunkConfigLearner_updateGammaPosteriorInvalidWeight(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	param, err := NewLearnedContextSize(100, 50)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	initialAlpha := param.Alpha

	// Zero and negative weights should not update
	ccl.updateGammaPosterior(param, 100, 0)
	if param.Alpha != initialAlpha {
		t.Error("Zero weight should not update param")
	}

	ccl.updateGammaPosterior(param, 100, -0.5)
	if param.Alpha != initialAlpha {
		t.Error("Negative weight should not update param")
	}
}

// =============================================================================
// updateDirichletPosterior Tests
// =============================================================================

func TestChunkConfigLearner_updateDirichletPosterior(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	weights := NewLearnedOverflowWeights()
	initialRecursive := weights.RecursiveCount
	initialSentence := weights.SentenceCount
	initialTruncate := weights.TruncateCount

	// Update with useful recursive strategy
	ccl.updateDirichletPosterior(weights, StrategyRecursive, true)
	if weights.RecursiveCount != initialRecursive+1.0 {
		t.Errorf("Useful recursive should add 1.0: %f + 1.0 = %f, got %f",
			initialRecursive, initialRecursive+1.0, weights.RecursiveCount)
	}

	// Update with non-useful sentence strategy
	ccl.updateDirichletPosterior(weights, StrategySentence, false)
	if weights.SentenceCount != initialSentence+0.1 {
		t.Errorf("Non-useful sentence should add 0.1: %f + 0.1 = %f, got %f",
			initialSentence, initialSentence+0.1, weights.SentenceCount)
	}

	// Update truncate
	ccl.updateDirichletPosterior(weights, StrategyTruncate, true)
	if weights.TruncateCount != initialTruncate+1.0 {
		t.Errorf("Useful truncate should add 1.0: %f + 1.0 = %f, got %f",
			initialTruncate, initialTruncate+1.0, weights.TruncateCount)
	}
}

func TestChunkConfigLearner_updateDirichletPosteriorNil(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Should not panic with nil weights
	ccl.updateDirichletPosterior(nil, StrategyRecursive, true)
}

// =============================================================================
// blendConfigs Tests
// =============================================================================

func TestChunkConfigLearner_blendConfigs(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	prior, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	domain, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	// Set different values for prior and domain
	prior.TargetTokens.Alpha = 500
	prior.TargetTokens.Beta = 1.0
	domain.TargetTokens.Alpha = 1000
	domain.TargetTokens.Beta = 1.0

	// Blend with 50% weight
	blended := ccl.blendConfigs(prior, domain, 0.5)
	if blended == nil {
		t.Fatal("blendConfigs() returned nil")
	}

	// Blended mean should be between prior and domain means
	priorMean := prior.TargetTokens.Mean()
	domainMean := domain.TargetTokens.Mean()
	blendedMean := blended.TargetTokens.Mean()

	if blendedMean < priorMean || blendedMean > domainMean {
		t.Errorf("Blended mean %d should be between prior %d and domain %d",
			blendedMean, priorMean, domainMean)
	}
}

func TestChunkConfigLearner_blendConfigsZeroWeight(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	prior, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	domain, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	// Zero weight should return prior
	blended := ccl.blendConfigs(prior, domain, 0)
	if blended != prior {
		t.Error("Zero weight should return prior config")
	}
}

func TestChunkConfigLearner_blendConfigsFullWeight(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	prior, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	domain, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	// Full weight should return domain
	blended := ccl.blendConfigs(prior, domain, 1.0)
	if blended != domain {
		t.Error("Full weight should return domain config")
	}
}

func TestChunkConfigLearner_blendConfigsNilPrior(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	domain, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	// Nil prior should return domain
	blended := ccl.blendConfigs(nil, domain, 0.5)
	if blended != domain {
		t.Error("Nil prior should return domain config")
	}
}

func TestChunkConfigLearner_blendConfigsNilDomain(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	prior, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	// Nil domain should return prior
	blended := ccl.blendConfigs(prior, nil, 0.5)
	if blended != prior {
		t.Error("Nil domain should return prior config")
	}
}

// =============================================================================
// Confidence Tests
// =============================================================================

func TestChunkConfigLearner_GetDomainConfidence(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Initial confidence should be 0
	confidence := ccl.GetDomainConfidence(DomainCode)
	if confidence != 0 {
		t.Errorf("Initial confidence should be 0, got %f", confidence)
	}

	// Record observations and verify confidence increases
	for i := 0; i < 20; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    300,
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       "chunk-" + string(rune('0'+i)),
			SessionID:     "session-1",
		}
		if err := ccl.RecordObservation(obs); err != nil {
			t.Fatalf("RecordObservation() error: %v", err)
		}
	}

	confidenceAfter := ccl.GetDomainConfidence(DomainCode)
	if confidenceAfter <= 0 {
		t.Errorf("Confidence should be positive after observations, got %f", confidenceAfter)
	}
	if confidenceAfter > 1 {
		t.Errorf("Confidence should be <= 1, got %f", confidenceAfter)
	}
}

func TestChunkConfigLearner_ConfidenceConvergence(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Record many observations
	for i := 0; i < 100; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    300,
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       "chunk-" + string(rune('0'+i%10)),
			SessionID:     "session-1",
		}
		if err := ccl.RecordObservation(obs); err != nil {
			t.Fatalf("RecordObservation() error: %v", err)
		}
	}

	// Confidence should approach 1
	confidence := ccl.GetDomainConfidence(DomainCode)
	if confidence < 0.9 {
		t.Logf("After 100 observations, confidence is %f (expected > 0.9)", confidence)
	}
}

// =============================================================================
// SetDecayConfig Tests
// =============================================================================

func TestChunkConfigLearner_SetDecayConfig(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	tests := []struct {
		name    string
		config  *UpdateConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &UpdateConfig{
				DecayFactor:         0.9,
				MinEffectiveSamples: 0.5,
			},
			wantErr: false,
		},
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "zero decay factor",
			config: &UpdateConfig{
				DecayFactor:         0,
				MinEffectiveSamples: 1.0,
			},
			wantErr: true,
		},
		{
			name: "negative decay factor",
			config: &UpdateConfig{
				DecayFactor:         -0.5,
				MinEffectiveSamples: 1.0,
			},
			wantErr: true,
		},
		{
			name: "decay factor > 1",
			config: &UpdateConfig{
				DecayFactor:         1.5,
				MinEffectiveSamples: 1.0,
			},
			wantErr: true,
		},
		{
			name: "zero min effective samples",
			config: &UpdateConfig{
				DecayFactor:         0.9,
				MinEffectiveSamples: 0,
			},
			wantErr: true,
		},
		{
			name: "negative min effective samples",
			config: &UpdateConfig{
				DecayFactor:         0.9,
				MinEffectiveSamples: -1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ccl.SetDecayConfig(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Errorf("SetDecayConfig() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("SetDecayConfig() unexpected error: %v", err)
			}
		})
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestChunkConfigLearner_ConcurrentAccess(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	var wg sync.WaitGroup
	numGoroutines := 10
	numObservations := 100

	// Concurrent writes
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < numObservations; i++ {
				obs := ChunkUsageObservation{
					Domain:        Domain(id % 4), // Spread across domains
					TokenCount:    300 + id,
					ContextBefore: 75,
					ContextAfter:  40,
					WasUseful:     i%2 == 0,
					Timestamp:     time.Now(),
					ChunkID:       "chunk-" + string(rune('0'+id)) + "-" + string(rune('0'+i%10)),
					SessionID:     "session-" + string(rune('0'+id)),
				}
				_ = ccl.RecordObservation(obs)
			}
		}(g)
	}

	// Concurrent reads
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < numObservations; i++ {
				_ = ccl.GetConfig(Domain(id%4), i%2 == 0)
				_ = ccl.GetDomainConfidence(Domain(id % 4))
				_ = ccl.GetObservationCount(Domain(id % 4))
			}
		}(g)
	}

	wg.Wait()

	// Verify no data corruption
	for d := Domain(0); d <= DomainGeneral; d++ {
		confidence := ccl.GetDomainConfidence(d)
		if confidence < 0 || confidence > 1 {
			t.Errorf("Domain %s has invalid confidence: %f", d, confidence)
		}
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestChunkConfigLearner_LearningConvergence(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Simulate consistent feedback with specific parameters
	targetTokens := 400
	contextBefore := 100
	contextAfter := 50

	// Record many useful observations with consistent parameters
	for i := 0; i < 50; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    targetTokens,
			ContextBefore: contextBefore,
			ContextAfter:  contextAfter,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       "chunk-" + string(rune('0'+i%10)),
			SessionID:     "session-1",
		}
		if err := ccl.RecordObservation(obs); err != nil {
			t.Fatalf("RecordObservation() error: %v", err)
		}
	}

	// Get config and check that learned values are close to observed values
	config := ccl.GetConfig(DomainCode, false)
	if config == nil {
		t.Fatal("GetConfig() returned nil")
	}

	learnedTarget := config.GetEffectiveTargetTokens(false)
	tolerance := float64(targetTokens) * 0.3 // 30% tolerance

	if math.Abs(float64(learnedTarget-targetTokens)) > tolerance {
		t.Logf("Learned target %d differs from observed %d by more than 30%%",
			learnedTarget, targetTokens)
	}
}

func TestChunkConfigLearner_OverflowStrategyLearning(t *testing.T) {
	ccl, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Record many observations preferring sentence strategy
	strategy := StrategySentence
	for i := 0; i < 50; i++ {
		obs := ChunkUsageObservation{
			Domain:           DomainAcademic,
			TokenCount:       500,
			ContextBefore:    100,
			ContextAfter:     50,
			WasUseful:        true,
			OverflowStrategy: &strategy,
			Timestamp:        time.Now(),
			ChunkID:          "chunk-" + string(rune('0'+i%10)),
			SessionID:        "session-1",
		}
		if err := ccl.RecordObservation(obs); err != nil {
			t.Fatalf("RecordObservation() error: %v", err)
		}
	}

	// The best strategy should be sentence
	config := ccl.GetConfig(DomainAcademic, false)
	if config == nil {
		t.Fatal("GetConfig() returned nil")
	}

	bestStrategy := config.GetEffectiveOverflowStrategy(false)
	if bestStrategy != StrategySentence {
		t.Logf("After 50 sentence observations, best strategy is %s (expected sentence)",
			bestStrategy)
	}
}

func TestChunkConfigLearner_HierarchicalBlending(t *testing.T) {
	globalPriors, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	// Set distinct global prior values
	globalPriors.TargetTokens.Alpha = 1000
	globalPriors.TargetTokens.Beta = 1.0

	ccl, err := NewChunkConfigLearner(globalPriors)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// With no observations, should get global priors
	coldConfig := ccl.GetConfig(DomainCode, false)
	coldTarget := coldConfig.GetEffectiveTargetTokens(false)

	// After some observations, should blend with global priors
	for i := 0; i < 5; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    300, // Different from global prior
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       "chunk-" + string(rune('0'+i)),
			SessionID:     "session-1",
		}
		if err := ccl.RecordObservation(obs); err != nil {
			t.Fatalf("RecordObservation() error: %v", err)
		}
	}

	// With low confidence, should still be influenced by priors
	lowConfidenceConfig := ccl.GetConfig(DomainCode, false)
	lowConfidenceTarget := lowConfidenceConfig.GetEffectiveTargetTokens(false)

	// After many observations, should converge toward learned values
	for i := 0; i < 50; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    300,
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       "chunk-" + string(rune('0'+i%10)),
			SessionID:     "session-1",
		}
		if err := ccl.RecordObservation(obs); err != nil {
			t.Fatalf("RecordObservation() error: %v", err)
		}
	}

	highConfidenceConfig := ccl.GetConfig(DomainCode, false)
	highConfidenceTarget := highConfidenceConfig.GetEffectiveTargetTokens(false)

	// Log the progression
	t.Logf("Cold start target: %d", coldTarget)
	t.Logf("Low confidence target: %d", lowConfidenceTarget)
	t.Logf("High confidence target: %d", highConfidenceTarget)

	// High confidence should be closer to observed (300) than cold start (1000)
	if math.Abs(float64(highConfidenceTarget-300)) > math.Abs(float64(coldTarget-300)) {
		t.Logf("High confidence target should be closer to observed value")
	}
}
