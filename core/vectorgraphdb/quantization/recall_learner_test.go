package quantization

import (
	"math"
	"sync"
	"testing"
)

func TestNewRecallLearner(t *testing.T) {
	t.Run("creates with correct codebaseID", func(t *testing.T) {
		rl := NewRecallLearner("test-codebase")
		if rl.GetCodebaseID() != "test-codebase" {
			t.Errorf("expected codebaseID 'test-codebase', got %q", rl.GetCodebaseID())
		}
	})

	t.Run("initial recall target is default", func(t *testing.T) {
		rl := NewRecallLearner("test")
		target := rl.GetLearnedRecallTarget()
		if target != DefaultRecallTarget {
			t.Errorf("expected initial target %v, got %v", DefaultRecallTarget, target)
		}
	})

	t.Run("initial confidence is low", func(t *testing.T) {
		rl := NewRecallLearner("test")
		confidence := rl.GetConfidence()
		if confidence != 0.0 {
			t.Errorf("expected initial confidence 0.0, got %v", confidence)
		}
	})

	t.Run("position usage initialized", func(t *testing.T) {
		rl := NewRecallLearner("test")
		stats := rl.GetPositionStats()
		if len(stats) != MaxTrackedPositions {
			t.Errorf("expected %d position stats, got %d", MaxTrackedPositions, len(stats))
		}
		for i, p := range stats {
			if p.Alpha != 1.0 || p.Beta != 1.0 {
				t.Errorf("position %d: expected Beta(1,1), got Beta(%.1f,%.1f)", i, p.Alpha, p.Beta)
			}
		}
	})
}

func TestRecallLearner_ObserveUsage(t *testing.T) {
	t.Run("empty prefetched handles gracefully", func(t *testing.T) {
		rl := NewRecallLearner("test")
		rl.ObserveUsage(nil, []string{"a", "b"}, false)
		if rl.GetObservationCount() != 1 {
			t.Errorf("expected 1 observation, got %d", rl.GetObservationCount())
		}
	})

	t.Run("empty used handles gracefully", func(t *testing.T) {
		rl := NewRecallLearner("test")
		rl.ObserveUsage([]string{"a", "b", "c"}, nil, false)
		if rl.GetObservationCount() != 1 {
			t.Errorf("expected 1 observation, got %d", rl.GetObservationCount())
		}
	})

	t.Run("single used ID at position 0", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := []string{"a", "b", "c", "d", "e"}
		used := []string{"a"}
		rl.ObserveUsage(prefetched, used, false)

		stats := rl.GetPositionStats()
		if stats[0].Alpha != 2.0 {
			t.Errorf("position 0: expected Alpha=2.0, got Alpha=%.1f", stats[0].Alpha)
		}
		if stats[1].Beta != 2.0 {
			t.Errorf("position 1: expected Beta=2.0, got Beta=%.1f", stats[1].Beta)
		}
	})

	t.Run("used ID deep in list position 50", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := make([]string, 100)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + i%26))
		}
		prefetched[50] = "target"
		used := []string{"target"}
		rl.ObserveUsage(prefetched, used, false)

		stats := rl.GetPositionStats()
		for i := 0; i <= 50; i++ {
			if stats[i].Alpha != 2.0 {
				t.Errorf("position %d: expected Alpha=2.0, got Alpha=%.1f", i, stats[i].Alpha)
			}
		}
		for i := 51; i < len(stats); i++ {
			if stats[i].Beta != 2.0 {
				t.Errorf("position %d: expected Beta=2.0, got Beta=%.1f", i, stats[i].Beta)
			}
		}
	})

	t.Run("multiple used IDs at various positions", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
		used := []string{"a", "c", "h"}
		rl.ObserveUsage(prefetched, used, false)

		stats := rl.GetPositionStats()
		for i := 0; i <= 7; i++ {
			if stats[i].Alpha != 2.0 {
				t.Errorf("position %d: expected Alpha=2.0, got Alpha=%.1f", i, stats[i].Alpha)
			}
		}
		for i := 8; i < 10; i++ {
			if stats[i].Beta != 2.0 {
				t.Errorf("position %d: expected Beta=2.0, got Beta=%.1f", i, stats[i].Beta)
			}
		}
	})

	t.Run("searchedAgain flag recorded", func(t *testing.T) {
		rl := NewRecallLearner("test")
		rl.ObserveUsage([]string{"a"}, []string{"a"}, true)
		rl.ObserveUsage([]string{"b"}, []string{"b"}, false)
		rl.ObserveUsage([]string{"c"}, []string{"c"}, true)

		rate := rl.GetSearchedAgainRate()
		expected := 2.0 / 3.0
		if math.Abs(rate-expected) > 0.001 {
			t.Errorf("expected searchedAgain rate %.3f, got %.3f", expected, rate)
		}
	})
}

func TestRecallLearner_GetLearnedRecallTarget(t *testing.T) {
	t.Run("returns default with no observations", func(t *testing.T) {
		rl := NewRecallLearner("test")
		target := rl.GetLearnedRecallTarget()
		if target != DefaultRecallTarget {
			t.Errorf("expected %v, got %v", DefaultRecallTarget, target)
		}
	})

	t.Run("returns default with few observations", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := []string{"a", "b", "c"}
		for i := 0; i < 4; i++ {
			rl.ObserveUsage(prefetched, []string{"a"}, false)
		}
		target := rl.GetLearnedRecallTarget()
		if target != DefaultRecallTarget {
			t.Errorf("expected %v with <5 observations, got %v", DefaultRecallTarget, target)
		}
	})

	t.Run("target increases when agents use deep results", func(t *testing.T) {
		rlDeep := NewRecallLearner("deep")
		rlShallow := NewRecallLearner("shallow")

		prefetched := make([]string, 100)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + (i % 26)))
		}

		deepPrefetched := make([]string, 100)
		copy(deepPrefetched, prefetched)
		deepPrefetched[80] = "deep_target"
		for i := 0; i < 30; i++ {
			rlDeep.ObserveUsage(deepPrefetched, []string{"deep_target"}, false)
		}

		shallowPrefetched := make([]string, 100)
		copy(shallowPrefetched, prefetched)
		shallowPrefetched[5] = "shallow_target"
		for i := 0; i < 30; i++ {
			rlShallow.ObserveUsage(shallowPrefetched, []string{"shallow_target"}, false)
		}

		deepStats := rlDeep.GetPositionStats()
		shallowStats := rlShallow.GetPositionStats()

		if deepStats[80].Alpha <= shallowStats[80].Alpha {
			t.Errorf("deep usage at position 80 should have higher alpha than shallow: deep=%v, shallow=%v",
				deepStats[80], shallowStats[80])
		}

		if deepStats[6].Beta >= shallowStats[6].Beta {
			t.Errorf("position 6 (after shallow cutoff) should have less beta for deep usage: deep=%v, shallow=%v",
				deepStats[6], shallowStats[6])
		}

		t.Logf("deep stats[80]: %v, shallow stats[80]: %v", deepStats[80], shallowStats[80])
		t.Logf("deep stats[6]: %v, shallow stats[6]: %v", deepStats[6], shallowStats[6])
	})

	t.Run("target decreases when agents only use top results", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := make([]string, 100)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + (i % 26)))
		}
		prefetched[0] = "top_target"

		for i := 0; i < 20; i++ {
			rl.ObserveUsage(prefetched, []string{"top_target"}, false)
		}

		target := rl.GetLearnedRecallTarget()
		if target > 0.5 {
			t.Errorf("expected target <= 0.5 for top-only usage, got %v", target)
		}
	})

	t.Run("target bounded 0.1 to 1.0", func(t *testing.T) {
		tests := []struct {
			name       string
			usedPos    int
			expectLow  float64
			expectHigh float64
		}{
			{"position 0", 0, 0.1, 1.0},
			{"position 99", 99, 0.1, 1.0},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rl := NewRecallLearner("test")
				prefetched := make([]string, 100)
				for i := range prefetched {
					prefetched[i] = string(rune('a' + (i % 26)))
				}
				prefetched[tt.usedPos] = "target"

				for i := 0; i < 50; i++ {
					rl.ObserveUsage(prefetched, []string{"target"}, false)
				}

				target := rl.GetLearnedRecallTarget()
				if target < tt.expectLow || target > tt.expectHigh {
					t.Errorf("target %v outside bounds [%v, %v]", target, tt.expectLow, tt.expectHigh)
				}
			})
		}
	})
}

func TestRecallLearner_GetConfidence(t *testing.T) {
	t.Run("low confidence with few observations", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := []string{"a", "b", "c"}
		for i := 0; i < 5; i++ {
			rl.ObserveUsage(prefetched, []string{"a"}, false)
		}

		confidence := rl.GetConfidence()
		if confidence > 0.2 {
			t.Errorf("expected low confidence (<0.2) with 5 observations, got %v", confidence)
		}
	})

	t.Run("confidence increases with observations", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := []string{"a", "b", "c"}

		var prevConfidence float64
		for obs := 10; obs <= 100; obs += 10 {
			for i := 0; i < 10; i++ {
				rl.ObserveUsage(prefetched, []string{"a"}, false)
			}
			confidence := rl.GetConfidence()
			if confidence <= prevConfidence && obs > 10 {
				t.Errorf("confidence should increase with observations: obs=%d, prev=%v, current=%v",
					obs, prevConfidence, confidence)
			}
			prevConfidence = confidence
		}
	})

	t.Run("confidence based on variance reduction", func(t *testing.T) {
		rlConsistent := NewRecallLearner("consistent")
		rlVaried := NewRecallLearner("varied")

		prefetched := make([]string, 50)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + (i % 26)))
		}

		prefetched[10] = "consistent_target"
		for i := 0; i < 50; i++ {
			rlConsistent.ObserveUsage(prefetched, []string{"consistent_target"}, false)
		}

		for i := 0; i < 50; i++ {
			pos := i % 50
			variedTarget := "varied_target_" + string(rune('a'+(pos%26)))
			prefetched[pos] = variedTarget
			rlVaried.ObserveUsage(prefetched, []string{variedTarget}, false)
		}

		consistentConf := rlConsistent.GetConfidence()
		variedConf := rlVaried.GetConfidence()

		t.Logf("consistent confidence: %v, varied confidence: %v", consistentConf, variedConf)
		if consistentConf <= 0 || variedConf <= 0 {
			t.Error("confidence should be positive with observations")
		}
	})
}

func TestRecallLearner_SearchedAgainBoost(t *testing.T) {
	t.Run("high searchedAgain rate increases target", func(t *testing.T) {
		rlWithSearchAgain := NewRecallLearner("with-search")
		rlWithoutSearchAgain := NewRecallLearner("without-search")

		prefetched := make([]string, 50)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + (i % 26)))
		}
		prefetched[25] = "target"

		for i := 0; i < 20; i++ {
			rlWithSearchAgain.ObserveUsage(prefetched, []string{"target"}, true)
			rlWithoutSearchAgain.ObserveUsage(prefetched, []string{"target"}, false)
		}

		targetWith := rlWithSearchAgain.GetLearnedRecallTarget()
		targetWithout := rlWithoutSearchAgain.GetLearnedRecallTarget()

		if targetWith <= targetWithout {
			t.Errorf("target with searchedAgain (%v) should be > target without (%v)",
				targetWith, targetWithout)
		}

		rateWith := rlWithSearchAgain.GetSearchedAgainRate()
		rateWithout := rlWithoutSearchAgain.GetSearchedAgainRate()
		if math.Abs(rateWith-1.0) > 0.001 {
			t.Errorf("expected 100%% searchedAgain rate, got %v", rateWith)
		}
		if rateWithout != 0.0 {
			t.Errorf("expected 0%% searchedAgain rate, got %v", rateWithout)
		}
	})

	t.Run("low searchedAgain rate doesn't affect much", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := make([]string, 50)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + (i % 26)))
		}
		prefetched[25] = "target"

		for i := 0; i < 10; i++ {
			searchAgain := i == 0
			rl.ObserveUsage(prefetched, []string{"target"}, searchAgain)
		}

		rate := rl.GetSearchedAgainRate()
		if math.Abs(rate-0.1) > 0.001 {
			t.Errorf("expected searchedAgain rate 0.1, got %v", rate)
		}
	})
}

func TestRecallLearner_Determinism(t *testing.T) {
	t.Run("same observations produce consistent target range", func(t *testing.T) {
		targets := make([]float64, 10)

		prefetched := make([]string, 50)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + (i % 26)))
		}
		prefetched[25] = "target"

		for trial := 0; trial < 10; trial++ {
			rl := NewRecallLearner("test")
			for i := 0; i < 30; i++ {
				rl.ObserveUsage(prefetched, []string{"target"}, false)
			}
			targets[trial] = rl.GetLearnedRecallTarget()
		}

		var sum float64
		for _, t := range targets {
			sum += t
		}
		mean := sum / float64(len(targets))

		var variance float64
		for _, t := range targets {
			variance += (t - mean) * (t - mean)
		}
		variance /= float64(len(targets))

		stddev := math.Sqrt(variance)
		if stddev > 0.15 {
			t.Errorf("targets have high variance: mean=%v, stddev=%v, values=%v", mean, stddev, targets)
		}
	})

	t.Run("order of observations doesn't change final target significantly", func(t *testing.T) {
		prefetched := make([]string, 50)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + (i % 26)))
		}

		observations := []struct {
			targetID string
			pos      int
		}{
			{"target10", 10},
			{"target20", 20},
			{"target30", 30},
		}

		var forwardTargets, reverseTargets []float64

		for trial := 0; trial < 5; trial++ {
			rlForward := NewRecallLearner("forward")
			for i := 0; i < 30; i++ {
				obs := observations[i%3]
				pf := make([]string, 50)
				copy(pf, prefetched)
				pf[obs.pos] = obs.targetID
				rlForward.ObserveUsage(pf, []string{obs.targetID}, false)
			}
			forwardTargets = append(forwardTargets, rlForward.GetLearnedRecallTarget())

			rlReverse := NewRecallLearner("reverse")
			for i := 29; i >= 0; i-- {
				obs := observations[i%3]
				pf := make([]string, 50)
				copy(pf, prefetched)
				pf[obs.pos] = obs.targetID
				rlReverse.ObserveUsage(pf, []string{obs.targetID}, false)
			}
			reverseTargets = append(reverseTargets, rlReverse.GetLearnedRecallTarget())
		}

		var forwardMean, reverseMean float64
		for i := range forwardTargets {
			forwardMean += forwardTargets[i]
			reverseMean += reverseTargets[i]
		}
		forwardMean /= float64(len(forwardTargets))
		reverseMean /= float64(len(reverseTargets))

		diff := math.Abs(forwardMean - reverseMean)
		if diff > 0.2 {
			t.Errorf("order significantly affects target: forward mean=%v, reverse mean=%v, diff=%v",
				forwardMean, reverseMean, diff)
		}
	})
}

func TestRecallLearner_ThreadSafety(t *testing.T) {
	t.Run("concurrent ObserveUsage calls don't panic", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := make([]string, 50)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + (i % 26)))
		}

		var wg sync.WaitGroup
		numGoroutines := 100
		observationsPerGoroutine := 50

		for g := 0; g < numGoroutines; g++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := 0; i < observationsPerGoroutine; i++ {
					pos := (id + i) % 50
					pf := make([]string, 50)
					copy(pf, prefetched)
					target := string(rune('A' + (id % 26)))
					pf[pos] = target
					rl.ObserveUsage(pf, []string{target}, i%3 == 0)
				}
			}(g)
		}

		wg.Wait()

		expectedCount := numGoroutines * observationsPerGoroutine
		actualCount := rl.GetObservationCount()
		if actualCount != expectedCount {
			t.Errorf("expected %d observations, got %d", expectedCount, actualCount)
		}
	})

	t.Run("concurrent GetLearnedRecallTarget during updates", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := make([]string, 50)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + (i % 26)))
		}
		prefetched[25] = "target"

		var wg sync.WaitGroup

		for g := 0; g < 10; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 100; i++ {
					rl.ObserveUsage(prefetched, []string{"target"}, false)
				}
			}()
		}

		for g := 0; g < 10; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 100; i++ {
					target := rl.GetLearnedRecallTarget()
					if target < 0.1 || target > 1.0 {
						t.Errorf("invalid target during concurrent read: %v", target)
					}
					_ = rl.GetConfidence()
					_ = rl.GetObservationCount()
					_ = rl.GetSearchedAgainRate()
				}
			}()
		}

		wg.Wait()
	})
}

func TestRecallLearner_EdgeCases(t *testing.T) {
	t.Run("all prefetched IDs used", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := []string{"a", "b", "c", "d", "e"}
		used := []string{"a", "b", "c", "d", "e"}

		rl.ObserveUsage(prefetched, used, false)

		stats := rl.GetPositionStats()
		for i := 0; i <= 4; i++ {
			if stats[i].Alpha != 2.0 {
				t.Errorf("position %d: expected Alpha=2.0 when all used, got %.1f", i, stats[i].Alpha)
			}
		}
	})

	t.Run("no prefetched IDs used", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := []string{"a", "b", "c", "d", "e"}
		used := []string{"x", "y", "z"}

		rl.ObserveUsage(prefetched, used, false)

		stats := rl.GetPositionStats()
		for i := 0; i < len(stats); i++ {
			if stats[i].Beta != 2.0 {
				t.Errorf("position %d: expected Beta=2.0 when none used, got %.1f", i, stats[i].Beta)
			}
		}
	})

	t.Run("used ID not in prefetched list", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := []string{"a", "b", "c"}
		used := []string{"not_in_list"}

		rl.ObserveUsage(prefetched, used, false)

		if rl.GetObservationCount() != 1 {
			t.Errorf("expected 1 observation, got %d", rl.GetObservationCount())
		}
	})

	t.Run("very long prefetched list over 100", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := make([]string, 500)
		for i := range prefetched {
			prefetched[i] = string(rune('a' + (i % 26)))
		}
		prefetched[400] = "deep_target"

		rl.ObserveUsage(prefetched, []string{"deep_target"}, false)

		stats := rl.GetPositionStats()

		for i := 0; i <= 80; i++ {
			if stats[i].Alpha != 2.0 {
				t.Errorf("position %d: expected Alpha=2.0 for scaled position, got %.1f", i, stats[i].Alpha)
			}
		}

		for i := 81; i < MaxTrackedPositions; i++ {
			if stats[i].Beta != 2.0 {
				t.Errorf("position %d: expected Beta=2.0 for scaled position, got %.1f", i, stats[i].Beta)
			}
		}
	})

	t.Run("duplicate IDs in used list", func(t *testing.T) {
		rl := NewRecallLearner("test")
		prefetched := []string{"a", "b", "c", "d", "e"}
		used := []string{"c", "c", "c"}

		rl.ObserveUsage(prefetched, used, false)

		stats := rl.GetPositionStats()
		if stats[2].Alpha != 2.0 {
			t.Errorf("position 2: expected Alpha=2.0, got %.1f", stats[2].Alpha)
		}
	})

	t.Run("both empty lists", func(t *testing.T) {
		rl := NewRecallLearner("test")
		rl.ObserveUsage(nil, nil, false)
		rl.ObserveUsage([]string{}, []string{}, false)

		if rl.GetObservationCount() != 2 {
			t.Errorf("expected 2 observations, got %d", rl.GetObservationCount())
		}
	})
}

func TestLearnRecallTarget_Stateless(t *testing.T) {
	tests := []struct {
		name          string
		prefetched    []string
		used          []string
		searchedAgain bool
		expectDelta   float64
		tolerance     float64
	}{
		{
			name:          "no prefetched returns 0",
			prefetched:    nil,
			used:          []string{"a"},
			searchedAgain: false,
			expectDelta:   0.0,
			tolerance:     0.001,
		},
		{
			name:          "empty prefetched returns 0",
			prefetched:    []string{},
			used:          []string{"a"},
			searchedAgain: false,
			expectDelta:   0.0,
			tolerance:     0.001,
		},
		{
			name:          "used at position 0 of 10",
			prefetched:    []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
			used:          []string{"a"},
			searchedAgain: false,
			expectDelta:   -0.8,
			tolerance:     0.001,
		},
		{
			name:          "used at position 9 of 10",
			prefetched:    []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
			used:          []string{"j"},
			searchedAgain: false,
			expectDelta:   0.1,
			tolerance:     0.001,
		},
		{
			name:          "used at middle position",
			prefetched:    []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
			used:          []string{"e"},
			searchedAgain: false,
			expectDelta:   -0.4,
			tolerance:     0.001,
		},
		{
			name:          "searchedAgain adds 0.05 boost",
			prefetched:    []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
			used:          []string{"e"},
			searchedAgain: true,
			expectDelta:   -0.35,
			tolerance:     0.001,
		},
		{
			name:          "none used returns negative delta",
			prefetched:    []string{"a", "b", "c"},
			used:          []string{"x", "y"},
			searchedAgain: false,
			expectDelta:   -0.9,
			tolerance:     0.001,
		},
		{
			name:          "multiple used takes deepest",
			prefetched:    []string{"a", "b", "c", "d", "e"},
			used:          []string{"a", "d"},
			searchedAgain: false,
			expectDelta:   -0.1,
			tolerance:     0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewRecallLearner("test")
			delta := rl.LearnRecallTarget(tt.prefetched, tt.used, tt.searchedAgain)
			if math.Abs(delta-tt.expectDelta) > tt.tolerance {
				t.Errorf("expected delta %.3f, got %.3f", tt.expectDelta, delta)
			}
		})
	}
}

func BenchmarkRecallLearner_ObserveUsage(b *testing.B) {
	prefetched := make([]string, 100)
	for i := range prefetched {
		prefetched[i] = string(rune('a' + (i % 26)))
	}
	used := []string{prefetched[50]}

	b.Run("single observation", func(b *testing.B) {
		rl := NewRecallLearner("bench")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rl.ObserveUsage(prefetched, used, false)
		}
	})

	b.Run("with searchedAgain", func(b *testing.B) {
		rl := NewRecallLearner("bench")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rl.ObserveUsage(prefetched, used, i%2 == 0)
		}
	})

	b.Run("multiple used IDs", func(b *testing.B) {
		rl := NewRecallLearner("bench")
		multiUsed := []string{prefetched[10], prefetched[30], prefetched[50], prefetched[70]}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rl.ObserveUsage(prefetched, multiUsed, false)
		}
	})

	b.Run("long prefetched list", func(b *testing.B) {
		rl := NewRecallLearner("bench")
		longPrefetched := make([]string, 1000)
		for i := range longPrefetched {
			longPrefetched[i] = string(rune('a' + (i % 26)))
		}
		longUsed := []string{longPrefetched[500]}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rl.ObserveUsage(longPrefetched, longUsed, false)
		}
	})
}

func BenchmarkRecallLearner_GetLearnedRecallTarget(b *testing.B) {
	rl := NewRecallLearner("bench")
	prefetched := make([]string, 100)
	for i := range prefetched {
		prefetched[i] = string(rune('a' + (i % 26)))
	}

	for i := 0; i < 100; i++ {
		pos := i % 100
		prefetched[pos] = "target"
		rl.ObserveUsage(prefetched, []string{"target"}, i%5 == 0)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rl.GetLearnedRecallTarget()
	}
}

func BenchmarkRecallLearner_Concurrent(b *testing.B) {
	rl := NewRecallLearner("bench")
	prefetched := make([]string, 100)
	for i := range prefetched {
		prefetched[i] = string(rune('a' + (i % 26)))
	}
	prefetched[50] = "target"

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				_ = rl.GetLearnedRecallTarget()
			} else {
				rl.ObserveUsage(prefetched, []string{"target"}, i%3 == 0)
			}
			i++
		}
	})
}
