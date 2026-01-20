// Package inference provides performance benchmarks for the inference system.
//
// PF.6.3: Knowledge Graph Performance Benchmark Suite - Inference Components
//
// This file contains benchmarks for:
//   - RuleEvaluator memoization and edge indexing
//   - InferenceEngine rule evaluation
package inference

import (
	"context"
	"fmt"
	"testing"
)

// =============================================================================
// Test Fixtures
// =============================================================================

// createBenchEdges creates edges for rule evaluation benchmarks.
func createBenchEdges(count int) []Edge {
	edges := make([]Edge, count)
	predicates := []string{"imports", "calls", "implements", "extends", "uses"}

	for i := 0; i < count; i++ {
		edges[i] = Edge{
			Source:    fmt.Sprintf("node%d", i),
			Target:    fmt.Sprintf("node%d", (i+1)%count),
			Predicate: predicates[i%len(predicates)],
		}
	}
	return edges
}

// createChainEdges creates a chain of edges for transitive closure testing.
func createChainEdges(count int) []Edge {
	edges := make([]Edge, count)
	for i := 0; i < count; i++ {
		edges[i] = Edge{
			Source:    fmt.Sprintf("node%d", i),
			Target:    fmt.Sprintf("node%d", i+1),
			Predicate: "imports",
		}
	}
	return edges
}

// =============================================================================
// RuleEvaluator Benchmarks
// =============================================================================

// BenchmarkRuleEvaluator_EvaluateRule benchmarks single rule evaluation.
func BenchmarkRuleEvaluator_EvaluateRule(b *testing.B) {
	edgeCounts := []int{50, 100, 500}

	for _, count := range edgeCounts {
		b.Run(fmt.Sprintf("Edges_%d", count), func(b *testing.B) {
			evaluator := NewRuleEvaluator()
			ctx := context.Background()

			rule := InferenceRule{
				ID:   "r1",
				Name: "Import to Dependency",
				Head: RuleCondition{
					Subject:   "?x",
					Predicate: "depends_on",
					Object:    "?y",
				},
				Body: []RuleCondition{
					{Subject: "?x", Predicate: "imports", Object: "?y"},
				},
				Priority: 1,
				Enabled:  true,
			}

			edges := createBenchEdges(count)
			for i := 0; i < count/2; i++ {
				edges[i].Predicate = "imports"
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				evaluator.EvaluateRule(ctx, rule, edges)
			}
		})
	}
}

// BenchmarkRuleEvaluator_MultiCondition benchmarks multi-condition rules.
func BenchmarkRuleEvaluator_MultiCondition(b *testing.B) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	rule := InferenceRule{
		ID:   "r2",
		Name: "Transitive Dependency",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "depends_on",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
			{Subject: "?y", Predicate: "imports", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := createChainEdges(100)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.EvaluateRule(ctx, rule, edges)
	}
}

// BenchmarkRuleEvaluator_Memoization tests cache effectiveness.
func BenchmarkRuleEvaluator_Memoization(b *testing.B) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	rule := InferenceRule{
		ID:   "r3",
		Name: "Test Rule",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "result",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := createChainEdges(50)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.EvaluateRule(ctx, rule, edges)
		evaluator.EvaluateRule(ctx, rule, edges)
	}
}

// BenchmarkRuleEvaluator_EdgeIndex tests edge indexing performance.
func BenchmarkRuleEvaluator_EdgeIndex(b *testing.B) {
	edgeCounts := []int{100, 500, 1000}

	for _, count := range edgeCounts {
		b.Run(fmt.Sprintf("Edges_%d", count), func(b *testing.B) {
			evaluator := NewRuleEvaluator()
			edges := createBenchEdges(count)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				evaluator.buildEdgeIndex(edges)
			}
		})
	}
}

// BenchmarkRuleEvaluator_IndexedLookup tests indexed condition matching.
func BenchmarkRuleEvaluator_IndexedLookup(b *testing.B) {
	evaluator := NewRuleEvaluator()
	edges := createBenchEdges(500)
	evaluator.edgeIndex = evaluator.buildEdgeIndex(edges)

	condition := RuleCondition{
		Subject:   "node0",
		Predicate: "imports",
		Object:    "?y",
	}
	bindings := make(map[string]string)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.matchConditionIndexed(condition, bindings)
	}
}

// BenchmarkRuleEvaluator_CacheKey tests cache key generation.
func BenchmarkRuleEvaluator_CacheKey(b *testing.B) {
	evaluator := NewRuleEvaluator()

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "imports",
		Object:    "?y",
	}
	bindings := map[string]string{
		"?x": "node1",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.conditionCacheKey(condition, bindings)
	}
}

// =============================================================================
// ForwardChainer Benchmarks
// =============================================================================

// BenchmarkForwardChainer_Evaluate benchmarks forward chaining evaluation.
func BenchmarkForwardChainer_Evaluate(b *testing.B) {
	evaluator := NewRuleEvaluator()
	chainer := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Import to Dependency",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "depends_on",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "imports", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	edges := createChainEdges(100)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chainer.Evaluate(ctx, rules, edges)
	}
}

// BenchmarkForwardChainer_MultipleRules benchmarks multiple rule evaluation.
func BenchmarkForwardChainer_MultipleRules(b *testing.B) {
	ruleCounts := []int{1, 5, 10}

	for _, ruleCount := range ruleCounts {
		b.Run(fmt.Sprintf("Rules_%d", ruleCount), func(b *testing.B) {
			evaluator := NewRuleEvaluator()
			chainer := NewForwardChainer(evaluator, 100)
			ctx := context.Background()

			rules := make([]InferenceRule, ruleCount)
			predicates := []string{"imports", "calls", "uses", "extends", "implements"}
			for i := 0; i < ruleCount; i++ {
				rules[i] = InferenceRule{
					ID:   fmt.Sprintf("r%d", i),
					Name: fmt.Sprintf("Rule %d", i),
					Head: RuleCondition{
						Subject:   "?x",
						Predicate: fmt.Sprintf("derived%d", i),
						Object:    "?y",
					},
					Body: []RuleCondition{
						{Subject: "?x", Predicate: predicates[i%len(predicates)], Object: "?y"},
					},
					Priority: 1,
					Enabled:  true,
				}
			}

			edges := createBenchEdges(100)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				chainer.Evaluate(ctx, rules, edges)
			}
		})
	}
}

// =============================================================================
// Condition Benchmarks
// =============================================================================

// BenchmarkRuleCondition_Unify benchmarks unification.
func BenchmarkRuleCondition_Unify(b *testing.B) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "imports",
		Object:    "?y",
	}
	bindings := make(map[string]string)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		condition.Unify(bindings, "node1", "node2", "imports")
	}
}

// BenchmarkRuleCondition_Substitute benchmarks substitution.
func BenchmarkRuleCondition_Substitute(b *testing.B) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "depends_on",
		Object:    "?y",
	}
	bindings := map[string]string{
		"?x": "node1",
		"?y": "node2",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		condition.Substitute(bindings)
	}
}

// BenchmarkIsVariable benchmarks variable detection.
func BenchmarkIsVariable(b *testing.B) {
	terms := []string{"?x", "node1", "?variable", "constant"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, term := range terms {
			IsVariable(term)
		}
	}
}

// =============================================================================
// W4P.40: Hash-Based Cache Key Benchmarks
// =============================================================================

// BenchmarkCacheKey_Simple benchmarks cache key generation with simple conditions.
func BenchmarkCacheKey_Simple(b *testing.B) {
	evaluator := NewRuleEvaluator()
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}
	bindings := map[string]string{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.conditionCacheKey(condition, bindings)
	}
}

// BenchmarkCacheKey_WithBindings benchmarks cache key generation with bound variables.
func BenchmarkCacheKey_WithBindings(b *testing.B) {
	evaluator := NewRuleEvaluator()
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}
	bindings := map[string]string{"?x": "node1"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.conditionCacheKey(condition, bindings)
	}
}

// BenchmarkCacheKey_MultipleBindings benchmarks cache key with multiple bindings.
func BenchmarkCacheKey_MultipleBindings(b *testing.B) {
	evaluator := NewRuleEvaluator()
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "?p",
		Object:    "?y",
	}
	bindings := map[string]string{
		"?x": "node1",
		"?y": "node2",
		"?p": "calls",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.conditionCacheKey(condition, bindings)
	}
}

// BenchmarkCacheKey_LongStrings benchmarks cache key with longer string values.
func BenchmarkCacheKey_LongStrings(b *testing.B) {
	evaluator := NewRuleEvaluator()
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "implements_interface",
		Object:    "?y",
	}
	bindings := map[string]string{
		"?x": "com.example.application.services.UserServiceImpl",
		"?y": "com.example.application.interfaces.UserService",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.conditionCacheKey(condition, bindings)
	}
}

// BenchmarkCollectRelevantVars benchmarks the helper function.
func BenchmarkCollectRelevantVars(b *testing.B) {
	conditions := []RuleCondition{
		{Subject: "?x", Predicate: "?p", Object: "?y"},
		{Subject: "?x", Predicate: "calls", Object: "?y"},
		{Subject: "NodeA", Predicate: "calls", Object: "NodeB"},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, c := range conditions {
			collectRelevantVars(c)
		}
	}
}

// BenchmarkFormatHashKey benchmarks the hash formatting function.
func BenchmarkFormatHashKey(b *testing.B) {
	hash := uint64(0xdeadbeefcafebabe)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		formatHashKey(hash)
	}
}
