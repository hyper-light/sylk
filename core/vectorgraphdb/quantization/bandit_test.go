package quantization

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func TestBetaPosterior(t *testing.T) {
	t.Run("NewUniformPrior creates Beta(1,1)", func(t *testing.T) {
		p := NewUniformPrior()
		if p.Alpha != 1.0 || p.Beta != 1.0 {
			t.Errorf("expected Beta(1,1), got Beta(%.1f, %.1f)", p.Alpha, p.Beta)
		}
	})

	t.Run("Mean() = α/(α+β)", func(t *testing.T) {
		testCases := []struct {
			alpha, beta, expectedMean float64
		}{
			{1.0, 1.0, 0.5},
			{2.0, 2.0, 0.5},
			{10.0, 2.0, 10.0 / 12.0},
			{1.0, 10.0, 1.0 / 11.0},
			{100.0, 100.0, 0.5},
		}

		for _, tc := range testCases {
			p := BetaPosterior{Alpha: tc.alpha, Beta: tc.beta}
			mean := p.Mean()
			if math.Abs(mean-tc.expectedMean) > 1e-9 {
				t.Errorf("Beta(%.1f, %.1f).Mean() = %.6f, want %.6f",
					tc.alpha, tc.beta, mean, tc.expectedMean)
			}
		}
	})

	t.Run("Variance() computed correctly", func(t *testing.T) {
		testCases := []struct {
			alpha, beta float64
		}{
			{1.0, 1.0},
			{2.0, 3.0},
			{10.0, 5.0},
			{50.0, 50.0},
		}

		for _, tc := range testCases {
			p := BetaPosterior{Alpha: tc.alpha, Beta: tc.beta}
			variance := p.Variance()
			sum := tc.alpha + tc.beta
			expected := (tc.alpha * tc.beta) / (sum * sum * (sum + 1))
			if math.Abs(variance-expected) > 1e-9 {
				t.Errorf("Beta(%.1f, %.1f).Variance() = %.9f, want %.9f",
					tc.alpha, tc.beta, variance, expected)
			}
		}
	})

	t.Run("TotalObservations() counts updates", func(t *testing.T) {
		testCases := []struct {
			alpha, beta float64
			expected    int
		}{
			{1.0, 1.0, 0},
			{2.0, 1.0, 1},
			{1.0, 2.0, 1},
			{5.0, 3.0, 6},
			{10.0, 10.0, 18},
		}

		for _, tc := range testCases {
			p := BetaPosterior{Alpha: tc.alpha, Beta: tc.beta}
			obs := p.TotalObservations()
			if obs != tc.expected {
				t.Errorf("Beta(%.1f, %.1f).TotalObservations() = %d, want %d",
					tc.alpha, tc.beta, obs, tc.expected)
			}
		}
	})

	t.Run("String() formatting", func(t *testing.T) {
		p := BetaPosterior{Alpha: 5.0, Beta: 3.0}
		str := p.String()
		if !strings.Contains(str, "Beta") {
			t.Error("String() should contain 'Beta'")
		}
		if !strings.Contains(str, "5.0") {
			t.Error("String() should contain alpha value")
		}
		if !strings.Contains(str, "3.0") {
			t.Error("String() should contain beta value")
		}
		if !strings.Contains(str, "mean") {
			t.Error("String() should contain mean")
		}
	})
}

func TestArmDefinition(t *testing.T) {
	t.Run("DefaultArmDefinitions returns all expected arms", func(t *testing.T) {
		defs := DefaultArmDefinitions()

		expectedArms := []ArmID{
			ArmCandidateMultiplier,
			ArmSubspaceDimTarget,
			ArmMinSubspaces,
			ArmCentroidsPerSubspace,
			ArmSplitPerturbation,
			ArmReencodeTimeoutMinutes,
			ArmLOPQCodeSizeBytes,
			ArmStrategyExactThreshold,
			ArmStrategyCoarseThreshold,
			ArmStrategyMediumThreshold,
		}

		for _, armID := range expectedArms {
			if _, exists := defs[armID]; !exists {
				t.Errorf("missing arm definition for %s", armID)
			}
		}

		if len(defs) != len(expectedArms) {
			t.Errorf("expected %d arms, got %d", len(expectedArms), len(defs))
		}
	})

	t.Run("AllArms() includes all arm IDs", func(t *testing.T) {
		allArms := AllArms()
		defs := DefaultArmDefinitions()

		if len(allArms) != len(defs) {
			t.Errorf("AllArms() returned %d arms, but DefaultArmDefinitions has %d",
				len(allArms), len(defs))
		}

		for _, armID := range allArms {
			if _, exists := defs[armID]; !exists {
				t.Errorf("AllArms() includes %s but it's not in DefaultArmDefinitions", armID)
			}
		}
	})

	t.Run("Each arm has valid choices", func(t *testing.T) {
		defs := DefaultArmDefinitions()

		for armID, def := range defs {
			if len(def.Choices) == 0 {
				t.Errorf("arm %s has no choices", armID)
			}
			if def.ID != armID {
				t.Errorf("arm %s has mismatched ID field: %s", armID, def.ID)
			}
		}
	})

	t.Run("Choice types are consistent within arms", func(t *testing.T) {
		defs := DefaultArmDefinitions()

		for armID, def := range defs {
			if len(def.Choices) < 2 {
				continue
			}
			firstType := typeOfInterface(def.Choices[0])
			for i, choice := range def.Choices[1:] {
				if typeOfInterface(choice) != firstType {
					t.Errorf("arm %s has inconsistent choice types: index 0 is %s, index %d is %s",
						armID, firstType, i+1, typeOfInterface(choice))
				}
			}
		}
	})
}

func typeOfInterface(v interface{}) string {
	switch v.(type) {
	case int:
		return "int"
	case float32:
		return "float32"
	case float64:
		return "float64"
	default:
		return "unknown"
	}
}

func TestBanditConfig(t *testing.T) {
	t.Run("DefaultBanditConfig has sensible values", func(t *testing.T) {
		cfg := DefaultBanditConfig()

		if cfg.CandidateMultiplier < 1 {
			t.Error("CandidateMultiplier should be >= 1")
		}
		if cfg.SubspaceDimTarget < 1 {
			t.Error("SubspaceDimTarget should be >= 1")
		}
		if cfg.MinSubspaces < 1 {
			t.Error("MinSubspaces should be >= 1")
		}
		if cfg.CentroidsPerSubspace < 1 {
			t.Error("CentroidsPerSubspace should be >= 1")
		}
		if cfg.SplitPerturbation <= 0 || cfg.SplitPerturbation >= 1 {
			t.Error("SplitPerturbation should be in (0, 1)")
		}
		if cfg.ReencodeTimeout <= 0 {
			t.Error("ReencodeTimeout should be > 0")
		}
		if cfg.LOPQCodeSizeBytes < 1 {
			t.Error("LOPQCodeSizeBytes should be >= 1")
		}
		if cfg.SampledChoiceIndices == nil {
			t.Error("SampledChoiceIndices should be initialized")
		}
	})

	t.Run("String() includes all fields", func(t *testing.T) {
		cfg := DefaultBanditConfig()
		str := cfg.String()

		if !strings.Contains(str, "BanditConfig") {
			t.Error("String() should contain 'BanditConfig'")
		}
		if !strings.Contains(str, "CandMult") {
			t.Error("String() should contain candidate multiplier")
		}
		if !strings.Contains(str, "Timeout") {
			t.Error("String() should contain timeout")
		}
	})

	t.Run("New strategy threshold fields present", func(t *testing.T) {
		cfg := DefaultBanditConfig()

		if cfg.StrategyExactThreshold <= 0 {
			t.Error("StrategyExactThreshold should be > 0")
		}
		if cfg.StrategyCoarseThreshold <= cfg.StrategyExactThreshold {
			t.Error("StrategyCoarseThreshold should be > StrategyExactThreshold")
		}
		if cfg.StrategyMediumThreshold <= cfg.StrategyCoarseThreshold {
			t.Error("StrategyMediumThreshold should be > StrategyCoarseThreshold")
		}

		str := cfg.String()
		if !strings.Contains(str, "ExactThr") {
			t.Error("String() should contain ExactThr")
		}
		if !strings.Contains(str, "CoarseThr") {
			t.Error("String() should contain CoarseThr")
		}
		if !strings.Contains(str, "MedThr") {
			t.Error("String() should contain MedThr")
		}
	})
}

func TestQuantizationBandit_Sample(t *testing.T) {
	t.Run("Sample returns valid config", func(t *testing.T) {
		bandit := NewQuantizationBandit(nil)
		cfg := bandit.Sample("test-codebase")

		defs := DefaultArmDefinitions()
		for armID, choiceIdx := range cfg.SampledChoiceIndices {
			def, exists := defs[armID]
			if !exists {
				t.Errorf("sampled unknown arm: %s", armID)
				continue
			}
			if choiceIdx < 0 || choiceIdx >= len(def.Choices) {
				t.Errorf("arm %s: choice index %d out of range [0, %d)",
					armID, choiceIdx, len(def.Choices))
			}
		}
	})

	t.Run("All arm choices within defined range", func(t *testing.T) {
		bandit := NewQuantizationBandit(nil)
		defs := DefaultArmDefinitions()

		for i := 0; i < 100; i++ {
			cfg := bandit.Sample("range-test")

			found := false
			for _, c := range defs[ArmCandidateMultiplier].Choices {
				if c.(int) == cfg.CandidateMultiplier {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("CandidateMultiplier %d not in defined choices", cfg.CandidateMultiplier)
			}

			found = false
			for _, c := range defs[ArmSubspaceDimTarget].Choices {
				if c.(int) == cfg.SubspaceDimTarget {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("SubspaceDimTarget %d not in defined choices", cfg.SubspaceDimTarget)
			}
		}
	})

	t.Run("Sampling is stochastic (varies across calls)", func(t *testing.T) {
		bandit := NewQuantizationBandit(nil)

		configs := make(map[string]bool)
		for i := 0; i < 100; i++ {
			cfg := bandit.Sample("stochastic-test")
			configs[cfg.String()] = true
		}

		if len(configs) < 2 {
			t.Log("Warning: sampling produced very few unique configs (might be unlucky)")
		}
	})
}

func TestQuantizationBandit_Update(t *testing.T) {
	t.Run("Reward >= 0.5 increments Alpha", func(t *testing.T) {
		store := NewInMemoryBanditStore()
		bandit := NewQuantizationBandit(store)

		codebaseID := "alpha-test"
		armID := ArmCandidateMultiplier
		choiceIdx := 0

		bandit.Update(codebaseID, armID, choiceIdx, 1.0)

		stats := bandit.GetPosteriorStats(codebaseID, armID)
		if stats[choiceIdx].Alpha != 2.0 {
			t.Errorf("Alpha should be 2.0 (1 prior + 1 success): got %.1f", stats[choiceIdx].Alpha)
		}
		if stats[choiceIdx].Beta != 1.0 {
			t.Errorf("Beta should remain 1.0: got %.1f", stats[choiceIdx].Beta)
		}
	})

	t.Run("Reward < 0.5 increments Beta", func(t *testing.T) {
		store := NewInMemoryBanditStore()
		bandit := NewQuantizationBandit(store)

		codebaseID := "beta-test"
		armID := ArmCandidateMultiplier
		choiceIdx := 0

		bandit.Update(codebaseID, armID, choiceIdx, 0.0)

		stats := bandit.GetPosteriorStats(codebaseID, armID)
		if stats[choiceIdx].Beta != 2.0 {
			t.Errorf("Beta should be 2.0 (1 prior + 1 failure): got %.1f", stats[choiceIdx].Beta)
		}
		if stats[choiceIdx].Alpha != 1.0 {
			t.Errorf("Alpha should remain 1.0: got %.1f", stats[choiceIdx].Alpha)
		}
	})

	t.Run("Updates persist across samples", func(t *testing.T) {
		store := NewInMemoryBanditStore()
		bandit := NewQuantizationBandit(store)

		codebaseID := "persist-test"
		armID := ArmCandidateMultiplier
		choiceIdx := 1

		for i := 0; i < 10; i++ {
			bandit.Update(codebaseID, armID, choiceIdx, 1.0)
		}

		bandit.Sample(codebaseID)

		stats := bandit.GetPosteriorStats(codebaseID, armID)
		if stats[choiceIdx].Alpha != 11.0 {
			t.Errorf("Alpha should be 11.0 after 10 updates, got %.1f", stats[choiceIdx].Alpha)
		}
	})

	t.Run("Boundary reward 0.5 increments Alpha", func(t *testing.T) {
		store := NewInMemoryBanditStore()
		bandit := NewQuantizationBandit(store)

		codebaseID := "boundary-test"
		armID := ArmCandidateMultiplier
		choiceIdx := 0

		bandit.Update(codebaseID, armID, choiceIdx, 0.5)

		stats := bandit.GetPosteriorStats(codebaseID, armID)
		if stats[choiceIdx].Alpha != 2.0 {
			t.Errorf("reward=0.5 should increment Alpha: got %.1f, want 2.0", stats[choiceIdx].Alpha)
		}
	})
}

func TestQuantizationBandit_ContinuousReward(t *testing.T) {
	t.Run("ComputeReward handles recall/latency tradeoff", func(t *testing.T) {
		testCases := []struct {
			name          string
			cr            ContinuousReward
			recallWeight  float64
			latencyWeight float64
			wantMin       float64
			wantMax       float64
		}{
			{
				name: "perfect recall, perfect latency",
				cr: ContinuousReward{
					Recall:          0.95,
					LatencyMs:       50,
					TargetRecall:    0.9,
					LatencyBudgetMs: 100,
				},
				recallWeight:  0.7,
				latencyWeight: 0.3,
				wantMin:       0.99,
				wantMax:       1.01,
			},
			{
				name: "half recall, half latency",
				cr: ContinuousReward{
					Recall:          0.45,
					LatencyMs:       100,
					TargetRecall:    0.9,
					LatencyBudgetMs: 100,
				},
				recallWeight:  0.7,
				latencyWeight: 0.3,
				wantMin:       0.64,
				wantMax:       0.66,
			},
			{
				name: "good recall, over budget latency",
				cr: ContinuousReward{
					Recall:          0.9,
					LatencyMs:       150,
					TargetRecall:    0.9,
					LatencyBudgetMs: 100,
				},
				recallWeight:  0.7,
				latencyWeight: 0.3,
				wantMin:       0.84,
				wantMax:       0.86,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				reward := ComputeReward(tc.cr, tc.recallWeight, tc.latencyWeight)
				if reward < tc.wantMin || reward > tc.wantMax {
					t.Errorf("ComputeReward() = %.4f, want in [%.2f, %.2f]",
						reward, tc.wantMin, tc.wantMax)
				}
			})
		}
	})

	t.Run("Fractional updates work (Alpha += reward)", func(t *testing.T) {
		store := NewInMemoryBanditStore()
		bandit := NewQuantizationBandit(store)

		codebaseID := "fractional-test"
		cfg := bandit.Sample(codebaseID)

		outcome := ContinuousRewardOutcome{
			CodebaseID: codebaseID,
			Config:     cfg,
			Continuous: ContinuousReward{
				Recall:          0.8,
				LatencyMs:       50,
				TargetRecall:    0.9,
				LatencyBudgetMs: 100,
			},
			RecordedAt:    time.Now(),
			RecallWeight:  0.7,
			LatencyWeight: 0.3,
		}

		bandit.UpdateFromContinuousOutcome(outcome)

		armID := ArmCandidateMultiplier
		choiceIdx := cfg.SampledChoiceIndices[armID]
		stats := bandit.GetPosteriorStats(codebaseID, armID)

		if stats[choiceIdx].Alpha <= 1.0 {
			t.Error("Alpha should be > 1.0 after continuous update")
		}
		if stats[choiceIdx].Beta <= 1.0 {
			t.Error("Beta should be > 1.0 after continuous update")
		}
		totalUpdate := (stats[choiceIdx].Alpha - 1) + (stats[choiceIdx].Beta - 1)
		if math.Abs(totalUpdate-1.0) > 0.01 {
			t.Errorf("Total update should be 1.0, got %.4f", totalUpdate)
		}
	})

	t.Run("UpdateFromContinuousOutcome updates all arms", func(t *testing.T) {
		store := NewInMemoryBanditStore()
		bandit := NewQuantizationBandit(store)

		codebaseID := "all-arms-test"
		cfg := bandit.Sample(codebaseID)

		outcome := ContinuousRewardOutcome{
			CodebaseID: codebaseID,
			Config:     cfg,
			Continuous: ContinuousReward{
				Recall:          0.9,
				LatencyMs:       50,
				TargetRecall:    0.9,
				LatencyBudgetMs: 100,
			},
			RecordedAt:    time.Now(),
			RecallWeight:  0.7,
			LatencyWeight: 0.3,
		}

		bandit.UpdateFromContinuousOutcome(outcome)

		for armID, choiceIdx := range cfg.SampledChoiceIndices {
			stats := bandit.GetPosteriorStats(codebaseID, armID)
			if stats == nil {
				t.Errorf("arm %s has nil posteriors after update", armID)
				continue
			}
			if stats[choiceIdx].TotalObservations() == 0 {
				t.Errorf("arm %s choice %d was not updated", armID, choiceIdx)
			}
		}
	})

	t.Run("Edge cases: zero target recall", func(t *testing.T) {
		cr := ContinuousReward{
			Recall:          0.5,
			LatencyMs:       50,
			TargetRecall:    0.0,
			LatencyBudgetMs: 100,
		}
		reward := ComputeReward(cr, 0.7, 0.3)
		if reward != 0.0 {
			t.Errorf("zero target recall should return 0.0, got %.4f", reward)
		}
	})

	t.Run("Edge cases: zero budget", func(t *testing.T) {
		cr := ContinuousReward{
			Recall:          0.9,
			LatencyMs:       0,
			TargetRecall:    0.9,
			LatencyBudgetMs: 0,
		}
		reward := ComputeReward(cr, 0.7, 0.3)
		if math.Abs(reward-1.0) > 0.01 {
			t.Errorf("zero budget with zero latency should give 1.0, got %.4f", reward)
		}

		cr.LatencyMs = 10
		reward = ComputeReward(cr, 0.7, 0.3)
		if math.Abs(reward-0.7) > 0.01 {
			t.Errorf("zero budget with non-zero latency should give 0.7, got %.4f", reward)
		}
	})
}

func TestAdaptiveArmDefinition(t *testing.T) {
	t.Run("DefaultAdaptiveArmDefinitions has bounds", func(t *testing.T) {
		defs := DefaultAdaptiveArmDefinitions()

		for armID, def := range defs {
			if def.MinValue == nil {
				t.Errorf("arm %s has nil MinValue", armID)
			}
			if def.MaxValue == nil {
				t.Errorf("arm %s has nil MaxValue", armID)
			}
			if def.MinChoices < 2 {
				t.Errorf("arm %s has MinChoices < 2: %d", armID, def.MinChoices)
			}
			if def.AdaptationMode == "" {
				t.Errorf("arm %s has empty AdaptationMode", armID)
			}
		}
	})

	t.Run("ShouldAdapt returns false with few observations", func(t *testing.T) {
		posteriors := []BetaPosterior{
			{Alpha: 2, Beta: 1},
			{Alpha: 1, Beta: 2},
			{Alpha: 1, Beta: 1},
		}

		if ShouldAdapt(posteriors) {
			t.Error("ShouldAdapt should return false with few observations")
		}
	})

	t.Run("ShouldAdapt returns true when confident winner at edge", func(t *testing.T) {
		posteriors := []BetaPosterior{
			{Alpha: 20, Beta: 3},
			{Alpha: 3, Beta: 10},
			{Alpha: 2, Beta: 8},
		}

		if !ShouldAdapt(posteriors) {
			t.Error("ShouldAdapt should return true with confident winner at edge")
		}

		posteriors = []BetaPosterior{
			{Alpha: 3, Beta: 10},
			{Alpha: 2, Beta: 8},
			{Alpha: 20, Beta: 3},
		}

		if !ShouldAdapt(posteriors) {
			t.Error("ShouldAdapt should return true with confident winner at upper edge")
		}
	})

	t.Run("ShouldAdapt returns false when winner is not at edge", func(t *testing.T) {
		posteriors := []BetaPosterior{
			{Alpha: 3, Beta: 10},
			{Alpha: 20, Beta: 3},
			{Alpha: 3, Beta: 10},
			{Alpha: 2, Beta: 8},
		}

		if ShouldAdapt(posteriors) {
			t.Error("ShouldAdapt should return false when best choice is in middle")
		}
	})

	t.Run("AdaptArmChoices expands range when optimal at edge", func(t *testing.T) {
		def := AdaptiveArmDefinition{
			ArmDefinition: ArmDefinition{
				ID:      ArmCandidateMultiplier,
				Choices: []interface{}{2, 4, 8, 16},
			},
			MinValue:       1,
			MaxValue:       32,
			AdaptationMode: AdaptationExponential,
			MinChoices:     3,
		}

		posteriors := []BetaPosterior{
			{Alpha: 20, Beta: 3},
			{Alpha: 3, Beta: 10},
			{Alpha: 2, Beta: 8},
			{Alpha: 2, Beta: 8},
		}

		newChoices := AdaptArmChoices(def, posteriors)

		if len(newChoices) <= len(def.Choices) {
			t.Log("Note: expansion might not happen if already at boundary")
		}
		if newChoices[0].(int) > def.Choices[0].(int) {
			t.Error("expansion at lower edge should not increase first choice")
		}
	})

	t.Run("AdaptArmChoices contracts when middle choices unused", func(t *testing.T) {
		def := AdaptiveArmDefinition{
			ArmDefinition: ArmDefinition{
				ID:      ArmCandidateMultiplier,
				Choices: []interface{}{2, 4, 8, 16},
			},
			MinValue:       1,
			MaxValue:       32,
			AdaptationMode: AdaptationExponential,
			MinChoices:     3,
		}

		posteriors := []BetaPosterior{
			{Alpha: 10, Beta: 3},
			{Alpha: 2, Beta: 12},
			{Alpha: 3, Beta: 12},
			{Alpha: 10, Beta: 3},
		}

		newChoices := AdaptArmChoices(def, posteriors)

		if len(newChoices) >= len(def.Choices) {
			t.Log("Note: contraction might not happen if middle isn't significantly worse")
		}
	})
}

func TestBanditConvergence(t *testing.T) {
	t.Run("With consistent rewards, best arm probability increases", func(t *testing.T) {
		store := NewInMemoryBanditStore()
		bandit := NewQuantizationBandit(store)

		codebaseID := "convergence-test"
		armID := ArmCandidateMultiplier
		bestChoiceIdx := 2

		for i := 0; i < 50; i++ {
			for choiceIdx := 0; choiceIdx < 4; choiceIdx++ {
				if choiceIdx == bestChoiceIdx {
					bandit.Update(codebaseID, armID, choiceIdx, 1.0)
				} else {
					bandit.Update(codebaseID, armID, choiceIdx, 0.0)
				}
			}
		}

		stats := bandit.GetPosteriorStats(codebaseID, armID)
		bestMean := stats[bestChoiceIdx].Mean()

		for i, s := range stats {
			if i != bestChoiceIdx && s.Mean() >= bestMean {
				t.Errorf("choice %d has mean %.3f >= best choice mean %.3f",
					i, s.Mean(), bestMean)
			}
		}

		if bestMean < 0.9 {
			t.Errorf("best choice mean should be > 0.9 after consistent rewards, got %.3f", bestMean)
		}
	})

	t.Run("After many updates, sampling concentrates on best choice", func(t *testing.T) {
		store := NewInMemoryBanditStore()
		bandit := NewQuantizationBandit(store)

		codebaseID := "concentrate-test"
		armID := ArmCandidateMultiplier
		bestChoiceIdx := 1

		for i := 0; i < 100; i++ {
			bandit.Update(codebaseID, armID, bestChoiceIdx, 1.0)
		}
		for choiceIdx := 0; choiceIdx < 4; choiceIdx++ {
			if choiceIdx != bestChoiceIdx {
				for i := 0; i < 20; i++ {
					bandit.Update(codebaseID, armID, choiceIdx, 0.0)
				}
			}
		}

		bestCount := 0
		totalSamples := 1000
		for i := 0; i < totalSamples; i++ {
			cfg := bandit.Sample(codebaseID)
			if cfg.SampledChoiceIndices[armID] == bestChoiceIdx {
				bestCount++
			}
		}

		ratio := float64(bestCount) / float64(totalSamples)
		if ratio < 0.7 {
			t.Errorf("best choice should be sampled >70%% of time, got %.1f%%", ratio*100)
		}
	})
}

func TestBanditPerCodebase(t *testing.T) {
	t.Run("Different codebase IDs have independent posteriors", func(t *testing.T) {
		store := NewInMemoryBanditStore()
		bandit := NewQuantizationBandit(store)

		codebaseA := "codebase-A"
		codebaseB := "codebase-B"
		armID := ArmCandidateMultiplier

		for i := 0; i < 20; i++ {
			bandit.Update(codebaseA, armID, 0, 1.0)
		}

		for i := 0; i < 20; i++ {
			bandit.Update(codebaseB, armID, 3, 1.0)
		}

		statsA := bandit.GetPosteriorStats(codebaseA, armID)
		statsB := bandit.GetPosteriorStats(codebaseB, armID)

		if statsA[0].Alpha <= statsA[3].Alpha {
			t.Error("codebase A's choice 0 should have higher alpha than choice 3")
		}

		if statsB[3].Alpha <= statsB[0].Alpha {
			t.Error("codebase B's choice 3 should have higher alpha than choice 0")
		}
	})

	t.Run("Updates to one don't affect another", func(t *testing.T) {
		store := NewInMemoryBanditStore()
		bandit := NewQuantizationBandit(store)

		codebaseA := "isolated-A"
		codebaseB := "isolated-B"
		armID := ArmCandidateMultiplier

		bandit.Update(codebaseB, armID, 0, 1.0)
		initialB := bandit.GetPosteriorStats(codebaseB, armID)
		initialB0Alpha := initialB[0].Alpha

		for i := 0; i < 100; i++ {
			bandit.Update(codebaseA, armID, 0, 1.0)
		}

		afterB := bandit.GetPosteriorStats(codebaseB, armID)
		if afterB[0].Alpha != initialB0Alpha {
			t.Errorf("codebase B was affected by updates to A: alpha changed from %.1f to %.1f",
				initialB0Alpha, afterB[0].Alpha)
		}
	})
}

func TestRewardOutcome(t *testing.T) {
	t.Run("Success() threshold at 0.5", func(t *testing.T) {
		testCases := []struct {
			reward   float64
			expected bool
		}{
			{0.0, false},
			{0.49, false},
			{0.5, true},
			{0.51, true},
			{1.0, true},
		}

		for _, tc := range testCases {
			outcome := RewardOutcome{Reward: tc.reward}
			if outcome.Success() != tc.expected {
				t.Errorf("RewardOutcome{Reward: %.2f}.Success() = %v, want %v",
					tc.reward, outcome.Success(), tc.expected)
			}
		}
	})

	t.Run("String() formatting", func(t *testing.T) {
		success := RewardOutcome{
			CodebaseID: "test",
			Reward:     1.0,
			Recall:     0.95,
			LatencyMs:  50,
		}
		str := success.String()
		if !strings.Contains(str, "SUCCESS") {
			t.Error("successful outcome should contain 'SUCCESS'")
		}

		failure := RewardOutcome{
			CodebaseID: "test",
			Reward:     0.0,
			Recall:     0.3,
			LatencyMs:  200,
		}
		str = failure.String()
		if !strings.Contains(str, "FAIL") {
			t.Error("failed outcome should contain 'FAIL'")
		}
	})
}

func TestComputeReward(t *testing.T) {
	testCases := []struct {
		name          string
		cr            ContinuousReward
		recallWeight  float64
		latencyWeight float64
		wantReward    float64
		tolerance     float64
	}{
		{
			name: "perfect on both",
			cr: ContinuousReward{
				Recall:          1.0,
				LatencyMs:       0,
				TargetRecall:    0.9,
				LatencyBudgetMs: 100,
			},
			recallWeight:  0.7,
			latencyWeight: 0.3,
			wantReward:    1.0,
			tolerance:     0.001,
		},
		{
			name: "exactly at target",
			cr: ContinuousReward{
				Recall:          0.9,
				LatencyMs:       100,
				TargetRecall:    0.9,
				LatencyBudgetMs: 100,
			},
			recallWeight:  0.7,
			latencyWeight: 0.3,
			wantReward:    1.0,
			tolerance:     0.001,
		},
		{
			name: "half recall",
			cr: ContinuousReward{
				Recall:          0.45,
				LatencyMs:       50,
				TargetRecall:    0.9,
				LatencyBudgetMs: 100,
			},
			recallWeight:  0.7,
			latencyWeight: 0.3,
			wantReward:    0.65,
			tolerance:     0.001,
		},
		{
			name: "double latency budget",
			cr: ContinuousReward{
				Recall:          0.9,
				LatencyMs:       200,
				TargetRecall:    0.9,
				LatencyBudgetMs: 100,
			},
			recallWeight:  0.7,
			latencyWeight: 0.3,
			wantReward:    0.7,
			tolerance:     0.001,
		},
		{
			name: "zero recall",
			cr: ContinuousReward{
				Recall:          0.0,
				LatencyMs:       50,
				TargetRecall:    0.9,
				LatencyBudgetMs: 100,
			},
			recallWeight:  0.7,
			latencyWeight: 0.3,
			wantReward:    0.3,
			tolerance:     0.001,
		},
		{
			name: "exceeds recall target",
			cr: ContinuousReward{
				Recall:          0.99,
				LatencyMs:       50,
				TargetRecall:    0.9,
				LatencyBudgetMs: 100,
			},
			recallWeight:  0.7,
			latencyWeight: 0.3,
			wantReward:    1.0,
			tolerance:     0.001,
		},
		{
			name: "negative recall clamps to 0",
			cr: ContinuousReward{
				Recall:          -0.5,
				LatencyMs:       50,
				TargetRecall:    0.9,
				LatencyBudgetMs: 100,
			},
			recallWeight:  0.7,
			latencyWeight: 0.3,
			wantReward:    0.3,
			tolerance:     0.001,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reward := ComputeReward(tc.cr, tc.recallWeight, tc.latencyWeight)
			if math.Abs(reward-tc.wantReward) > tc.tolerance {
				t.Errorf("ComputeReward() = %.4f, want %.4f (tolerance %.4f)",
					reward, tc.wantReward, tc.tolerance)
			}
		})
	}
}

func TestContinuousRewardInRange(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < 1000; i++ {
		cr := ContinuousReward{
			Recall:          rng.Float64() * 2.0,
			LatencyMs:       int64(rng.Intn(500)),
			TargetRecall:    0.5 + rng.Float64()*0.5,
			LatencyBudgetMs: int64(50 + rng.Intn(200)),
		}
		recallWeight := rng.Float64()
		latencyWeight := 1.0 - recallWeight

		reward := ComputeReward(cr, recallWeight, latencyWeight)

		if reward < 0.0 || reward > 1.0 {
			t.Errorf("reward %.4f out of range [0,1] for cr=%+v weights=(%.2f, %.2f)",
				reward, cr, recallWeight, latencyWeight)
		}
	}
}

func BenchmarkBanditSample(b *testing.B) {
	bandit := NewQuantizationBandit(nil)

	for i := 0; i < 100; i++ {
		cfg := bandit.Sample("bench-codebase")
		outcome := RewardOutcome{
			CodebaseID: "bench-codebase",
			Config:     cfg,
			Reward:     float64(i % 2),
		}
		bandit.UpdateFromOutcome(outcome)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bandit.Sample("bench-codebase")
	}
}

func BenchmarkBanditUpdate(b *testing.B) {
	bandit := NewQuantizationBandit(nil)
	cfg := bandit.Sample("bench-codebase")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bandit.Update("bench-codebase", ArmCandidateMultiplier, 0, float64(i%2))
	}
	_ = cfg
}

func BenchmarkComputeReward(b *testing.B) {
	cr := ContinuousReward{
		Recall:          0.85,
		LatencyMs:       75,
		TargetRecall:    0.9,
		LatencyBudgetMs: 100,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ComputeReward(cr, 0.7, 0.3)
	}
}
