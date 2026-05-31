package forest

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkPhase101112PolicyFitness(b *testing.B) {
	champion := policyOutcomeSeries("candidate-bench", "policy-champion", PolicyOutcomeArmChampion, policyMinimumOutcomeSamples(), 0.65)
	challenger := policyOutcomeSeries("candidate-bench", "candidate-bench", PolicyOutcomeArmChallenger, policyMinimumOutcomeSamples(), 0.85)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fitness := ComputePolicyFitness(champion, challenger)
		if !fitness.PromotionAllowed {
			b.Fatalf("fitness blocked: %+v", fitness)
		}
	}
}

func BenchmarkPhase1112MemeExtractionBounded(b *testing.B) {
	forest, db := newTestForestWithConfig(b, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	for group := 0; group < policyGenerationLimit(); group++ {
		insertRepeatedMemeNodes(b, db, "bench-memes", ForestNodeValidation, EvidenceGradeValidated, fmt.Sprintf("validated harness pattern %d", group), nodeProjectionRetryLimit())
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := forest.RunMemeExtraction(context.Background(), "bench-memes", policyCandidatePopulationLimit()); err != nil {
			b.Fatalf("extract memes: %v", err)
		}
	}
}

func BenchmarkPhase12SkillCandidateValidation(b *testing.B) {
	forest, db := newTestForestWithConfig(b, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	candidate, err := forest.ProposeGeneratedSkillCandidate(context.Background(), SkillCandidateInput{
		Name:                 "Bench Remediation Author",
		RoleScope:            "guardian",
		Trigger:              "validated remediation pattern appears",
		SourceMemeIDs:        []string{"meme:bench"},
		SourceValidationRefs: []string{"validation:bench"},
		PromotionRationale:   "Benchmark validation for generated skill candidates.",
	})
	if err != nil {
		b.Fatalf("propose skill: %v", err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		validations, err := forest.ValidateSkillCandidate(context.Background(), candidate.CandidateID)
		if err != nil {
			b.Fatalf("validate skill: %v", err)
		}
		if skillValidationsFailed(validations) {
			b.Fatalf("validations failed: %+v", validations)
		}
	}
}
