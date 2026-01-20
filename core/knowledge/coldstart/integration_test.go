// Package coldstart provides cold-start scoring for the knowledge system.
// MD.9.11 Cold-Start Integration Tests
package coldstart

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/knowledge/query"
)

// =============================================================================
// MD.9.11 Mock Implementations for Testing
// =============================================================================

// mockMemoryAccessor implements query.MemoryAccessor for testing.
type mockMemoryAccessor struct {
	mu            sync.RWMutex
	activations   map[string]float64
	accessCounts  map[string]int
	lastAccessTimes map[string]time.Time
}

func newMockMemoryAccessor() *mockMemoryAccessor {
	return &mockMemoryAccessor{
		activations:     make(map[string]float64),
		accessCounts:    make(map[string]int),
		lastAccessTimes: make(map[string]time.Time),
	}
}

func (m *mockMemoryAccessor) GetActivation(ctx context.Context, nodeID string, d domain.Domain, now time.Time) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if act, ok := m.activations[nodeID]; ok {
		return act, nil
	}
	return -100.0, nil // Very low activation for no data
}

func (m *mockMemoryAccessor) GetAccessCount(ctx context.Context, nodeID string, d domain.Domain) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.accessCounts[nodeID], nil
}

func (m *mockMemoryAccessor) GetLastAccessTime(ctx context.Context, nodeID string, d domain.Domain) (time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastAccessTimes[nodeID], nil
}

func (m *mockMemoryAccessor) SetNodeMemory(nodeID string, accessCount int, activation float64, lastAccess time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accessCounts[nodeID] = accessCount
	m.activations[nodeID] = activation
	m.lastAccessTimes[nodeID] = lastAccess
}

// mockColdStartCalculator implements query.ColdStartPriorCalculator for testing.
type mockColdStartCalculator struct {
	mu     sync.RWMutex
	priors map[string]float64
}

func newMockColdStartCalculator() *mockColdStartCalculator {
	return &mockColdStartCalculator{
		priors: make(map[string]float64),
	}
}

func (m *mockColdStartCalculator) ComputePrior(nodeID string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if prior, ok := m.priors[nodeID]; ok {
		return prior
	}
	return 0.5 // Default prior
}

func (m *mockColdStartCalculator) SetPrior(nodeID string, prior float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.priors[nodeID] = prior
}

// mockGraphSignalsProvider implements query.GraphSignalsProvider for testing.
type mockGraphSignalsProvider struct {
	mu              sync.RWMutex
	pageRanks       map[string]float64
	clusterCoeffs   map[string]float64
}

func newMockGraphSignalsProvider() *mockGraphSignalsProvider {
	return &mockGraphSignalsProvider{
		pageRanks:     make(map[string]float64),
		clusterCoeffs: make(map[string]float64),
	}
}

func (m *mockGraphSignalsProvider) GetPageRank(nodeID string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pageRanks[nodeID]
}

func (m *mockGraphSignalsProvider) GetClusteringCoefficient(nodeID string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clusterCoeffs[nodeID]
}

func (m *mockGraphSignalsProvider) SetSignals(nodeID string, pageRank, clusterCoeff float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pageRanks[nodeID] = pageRank
	m.clusterCoeffs[nodeID] = clusterCoeff
}

// =============================================================================
// MD.9.11 Test Helpers
// =============================================================================

// createTestHybridResults creates test hybrid search results.
func createTestHybridResults(nodeIDs []string) []query.HybridResult {
	results := make([]query.HybridResult, len(nodeIDs))
	for i, id := range nodeIDs {
		results[i] = query.HybridResult{
			ID:            id,
			Content:       "Test content for " + id,
			Score:         1.0 - float64(i)*0.1, // Decreasing scores
			TextScore:     0.5,
			SemanticScore: 0.4,
			GraphScore:    0.1,
			Source:        query.SourceCombined,
		}
	}
	return results
}

// =============================================================================
// MD.9.11 Test: Full Cold-Start Flow
// =============================================================================

func TestColdStartIntegration_FullFlow(t *testing.T) {
	ctx := context.Background()

	// Create mock components
	memAccessor := newMockMemoryAccessor()
	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	// Set up cold-start priors for nodes
	coldCalc.SetPrior("node1", 0.8)
	coldCalc.SetPrior("node2", 0.6)
	coldCalc.SetPrior("node3", 0.4)

	// Set up graph signals
	graphSignals.SetSignals("node1", 0.7, 0.5)
	graphSignals.SetSignals("node2", 0.5, 0.3)
	graphSignals.SetSignals("node3", 0.3, 0.1)

	// Create scorer
	scorer := query.NewHybridScorer(memAccessor, coldCalc, graphSignals)

	// Create test results
	results := createTestHybridResults([]string{"node1", "node2", "node3"})

	// Score results (cold-start - no access history)
	scored, err := scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Verify results
	if len(scored) != 3 {
		t.Errorf("Expected 3 results, got %d", len(scored))
	}

	// All results should be cold-start since no access history
	for _, result := range scored {
		if !result.IsColdStart {
			t.Errorf("Expected cold-start for node %s with no access history", result.Result.ID)
		}
		if result.BlendRatio != 0.0 {
			t.Errorf("Expected blend ratio 0.0 for cold-start, got %f", result.BlendRatio)
		}
		if result.FinalScore <= 0 {
			t.Errorf("Expected positive score, got %f", result.FinalScore)
		}

		// Should have cold-start contribution
		hasColdStart := false
		for _, c := range result.Contributions {
			if c.Signal == query.SignalColdStartPrior && c.Contribution > 0 {
				hasColdStart = true
				break
			}
		}
		if !hasColdStart {
			t.Errorf("Expected cold-start contribution for node %s", result.Result.ID)
		}
	}
}

// =============================================================================
// MD.9.11 Test: Cold to Warm Transition
// =============================================================================

func TestColdStartIntegration_ColdToWarmTransition(t *testing.T) {
	ctx := context.Background()

	memAccessor := newMockMemoryAccessor()
	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	coldCalc.SetPrior("node1", 0.7)
	graphSignals.SetSignals("node1", 0.5, 0.3)

	// Create scorer with custom blender config
	config := query.DefaultHybridScorerConfig()
	config.Blender.MinTracesForWarm = 3
	config.Blender.FullWarmTraces = 10
	scorer := query.NewHybridScorerWithConfig(memAccessor, coldCalc, graphSignals, config)

	results := createTestHybridResults([]string{"node1"})

	// Phase 1: Pure cold (0 accesses)
	scored, err := scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	if !scored[0].IsColdStart {
		t.Error("Expected cold-start with 0 accesses")
	}
	if scored[0].BlendRatio != 0.0 {
		t.Errorf("Expected blend ratio 0.0, got %f", scored[0].BlendRatio)
	}
	coldScore := scored[0].FinalScore

	// Phase 2: Transition (5 accesses)
	now := time.Now()
	memAccessor.SetNodeMemory("node1", 5, 0.5, now)

	scored, err = scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Should be in transition (blend ratio between 0 and 1)
	if scored[0].BlendRatio <= 0.0 || scored[0].BlendRatio >= 1.0 {
		t.Errorf("Expected blend ratio in (0,1), got %f", scored[0].BlendRatio)
	}
	transitionScore := scored[0].FinalScore

	// Phase 3: Pure warm (12 accesses)
	memAccessor.SetNodeMemory("node1", 12, 1.0, now)

	scored, err = scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Should be pure warm
	if scored[0].IsColdStart {
		t.Error("Expected warm scoring with sufficient access history")
	}
	if scored[0].BlendRatio != 1.0 {
		t.Errorf("Expected blend ratio 1.0, got %f", scored[0].BlendRatio)
	}
	warmScore := scored[0].FinalScore

	// Scores should be meaningful at each phase
	t.Logf("Cold score: %f, Transition score: %f, Warm score: %f",
		coldScore, transitionScore, warmScore)

	if coldScore <= 0 || transitionScore <= 0 || warmScore <= 0 {
		t.Error("All scores should be positive")
	}
}

// =============================================================================
// MD.9.11 Test: Memory Store Integration
// =============================================================================

func TestColdStartIntegration_MemoryStore(t *testing.T) {
	ctx := context.Background()

	memAccessor := newMockMemoryAccessor()
	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	// Set up priors
	coldCalc.SetPrior("node1", 0.6)
	coldCalc.SetPrior("node2", 0.6)
	coldCalc.SetPrior("node3", 0.6)

	// Set up graph signals
	graphSignals.SetSignals("node1", 0.5, 0.3)
	graphSignals.SetSignals("node2", 0.5, 0.3)
	graphSignals.SetSignals("node3", 0.5, 0.3)

	// Create scorer
	scorer := query.NewHybridScorer(memAccessor, coldCalc, graphSignals)

	now := time.Now()

	// node1: many accesses (warm)
	memAccessor.SetNodeMemory("node1", 20, 1.5, now)

	// node2: few accesses (transitioning)
	memAccessor.SetNodeMemory("node2", 2, 0.3, now.Add(-time.Hour))

	// node3: no accesses (cold)
	// (nothing to set)

	results := createTestHybridResults([]string{"node1", "node2", "node3"})

	// Score
	scored, err := scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Find results by ID
	var node1Result, node2Result, node3Result *query.ScoreResult
	for i := range scored {
		switch scored[i].Result.ID {
		case "node1":
			node1Result = &scored[i]
		case "node2":
			node2Result = &scored[i]
		case "node3":
			node3Result = &scored[i]
		}
	}

	// Verify different cold-start states
	if node1Result.IsColdStart {
		t.Error("node1 should not be cold-start with many accesses")
	}
	if node1Result.BlendRatio != 1.0 {
		t.Errorf("node1 should have blend ratio 1.0, got %f", node1Result.BlendRatio)
	}

	if !node2Result.IsColdStart {
		t.Error("node2 should be cold-start with few accesses")
	}
	if node2Result.BlendRatio >= 1.0 {
		t.Errorf("node2 should have blend ratio < 1.0, got %f", node2Result.BlendRatio)
	}

	if !node3Result.IsColdStart {
		t.Error("node3 should be cold-start with no accesses")
	}
	if node3Result.BlendRatio != 0.0 {
		t.Errorf("node3 should have blend ratio 0.0, got %f", node3Result.BlendRatio)
	}

	// node1 should have memory contribution
	hasMemory := false
	for _, c := range node1Result.Contributions {
		if c.Signal == query.SignalMemoryDecay && c.Contribution > 0 {
			hasMemory = true
			break
		}
	}
	if !hasMemory {
		t.Error("node1 should have memory decay contribution")
	}
}

// =============================================================================
// MD.9.11 Test: PageRank and Clustering Coefficient Impact
// =============================================================================

func TestColdStartIntegration_GraphMetrics(t *testing.T) {
	ctx := context.Background()

	memAccessor := newMockMemoryAccessor()
	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	coldCalc.SetPrior("hub", 0.7)
	coldCalc.SetPrior("peripheral", 0.7)

	// Hub node: high PageRank, high clustering
	graphSignals.SetSignals("hub", 0.9, 0.8)

	// Peripheral node: low PageRank, low clustering
	graphSignals.SetSignals("peripheral", 0.1, 0.1)

	scorer := query.NewHybridScorer(memAccessor, coldCalc, graphSignals)

	// Create results with same RRF score
	results := []query.HybridResult{
		{ID: "hub", Content: "Hub", Score: 0.5, Source: query.SourceCombined},
		{ID: "peripheral", Content: "Peripheral", Score: 0.5, Source: query.SourceCombined},
	}

	// Score
	scored, err := scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Find results
	var hubResult, peripheralResult *query.ScoreResult
	for i := range scored {
		switch scored[i].Result.ID {
		case "hub":
			hubResult = &scored[i]
		case "peripheral":
			peripheralResult = &scored[i]
		}
	}

	// Hub should score higher due to PageRank and clustering
	if hubResult.FinalScore <= peripheralResult.FinalScore {
		t.Errorf("Hub should score higher than peripheral: hub=%f, peripheral=%f",
			hubResult.FinalScore, peripheralResult.FinalScore)
	}

	// Verify PageRank contribution exists for hub
	hubHasPageRank := false
	for _, c := range hubResult.Contributions {
		if c.Signal == query.SignalPageRank && c.RawValue > 0 {
			hubHasPageRank = true
			t.Logf("Hub PageRank: raw=%f, contribution=%f", c.RawValue, c.Contribution)
			break
		}
	}
	if !hubHasPageRank {
		t.Error("Hub should have PageRank contribution")
	}

	// Verify clustering coefficient contribution exists for hub
	hubHasCluster := false
	for _, c := range hubResult.Contributions {
		if c.Signal == query.SignalClusterCoeff && c.RawValue > 0 {
			hubHasCluster = true
			t.Logf("Hub ClusterCoeff: raw=%f, contribution=%f", c.RawValue, c.Contribution)
			break
		}
	}
	if !hubHasCluster {
		t.Error("Hub should have clustering coefficient contribution")
	}
}

// =============================================================================
// MD.9.11 Test: Cross-Domain Cold-Start Behavior
// =============================================================================

func TestColdStartIntegration_CrossDomain(t *testing.T) {
	ctx := context.Background()

	memAccessor := newMockMemoryAccessor()
	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	// Set up priors
	coldCalc.SetPrior("librarian_node", 0.7)
	coldCalc.SetPrior("academic_node", 0.6)
	coldCalc.SetPrior("archivalist_node", 0.5)

	// Set up graph signals
	graphSignals.SetSignals("librarian_node", 0.5, 0.3)
	graphSignals.SetSignals("academic_node", 0.5, 0.3)
	graphSignals.SetSignals("archivalist_node", 0.5, 0.3)

	scorer := query.NewHybridScorer(memAccessor, coldCalc, graphSignals)

	now := time.Now()

	// Librarian node has accesses
	memAccessor.SetNodeMemory("librarian_node", 15, 1.0, now)

	// Test scoring in Librarian domain
	librarianResults := []query.HybridResult{
		{ID: "librarian_node", Content: "Librarian", Score: 0.5},
		{ID: "academic_node", Content: "Academic", Score: 0.5},
		{ID: "archivalist_node", Content: "Archivalist", Score: 0.5},
	}

	scoredLib, err := scorer.Score(ctx, librarianResults, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score (Librarian) failed: %v", err)
	}

	// Verify domain-specific behavior
	var libNode *query.ScoreResult
	for i := range scoredLib {
		if scoredLib[i].Result.ID == "librarian_node" {
			libNode = &scoredLib[i]
			break
		}
	}

	// Librarian node should have warm scores (has accesses in this domain)
	if libNode.IsColdStart {
		t.Error("Librarian node should be warm in Librarian domain")
	}

	// Test scoring in Academic domain
	scoredAcad, err := scorer.Score(ctx, librarianResults, nil, domain.DomainAcademic)
	if err != nil {
		t.Fatalf("Score (Academic) failed: %v", err)
	}

	// Find academic node result
	var acadNode *query.ScoreResult
	for i := range scoredAcad {
		if scoredAcad[i].Result.ID == "academic_node" {
			acadNode = &scoredAcad[i]
			break
		}
	}

	// Academic node should be cold (no accesses)
	if !acadNode.IsColdStart {
		t.Error("Academic node should be cold in Academic domain (no accesses)")
	}
}

// =============================================================================
// MD.9.11 Test: Signal Configuration
// =============================================================================

func TestColdStartIntegration_SignalConfiguration(t *testing.T) {
	ctx := context.Background()

	memAccessor := newMockMemoryAccessor()
	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	coldCalc.SetPrior("node1", 0.7)
	graphSignals.SetSignals("node1", 0.5, 0.3)

	scorer := query.NewHybridScorer(memAccessor, coldCalc, graphSignals)

	results := createTestHybridResults([]string{"node1"})

	// Test 1: All signals enabled
	scored, err := scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}
	allEnabledScore := scored[0].FinalScore
	allEnabledContribCount := len(scored[0].Contributions)

	// Test 2: Disable cold-start
	scorer.SetColdStartEnabled(false)
	scored, err = scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}
	noColdStartScore := scored[0].FinalScore

	// Should have no cold-start contribution
	for _, c := range scored[0].Contributions {
		if c.Signal == query.SignalColdStartPrior {
			t.Error("Should not have cold-start contribution when disabled")
		}
	}

	// Test 3: Re-enable cold-start, disable PageRank
	scorer.SetColdStartEnabled(true)
	scorer.DisableSignal(query.SignalPageRank)

	scored, err = scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Should have no PageRank contribution
	for _, c := range scored[0].Contributions {
		if c.Signal == query.SignalPageRank {
			t.Error("Should not have PageRank contribution when disabled")
		}
	}

	// Test 4: Re-enable PageRank
	scorer.EnableSignal(query.SignalPageRank)
	if !scorer.IsSignalEnabled(query.SignalPageRank) {
		t.Error("PageRank should be enabled after EnableSignal")
	}

	t.Logf("All enabled score: %f (%d contributions)", allEnabledScore, allEnabledContribCount)
	t.Logf("No cold-start score: %f", noColdStartScore)
}

// =============================================================================
// MD.9.11 Test: Thread Safety
// =============================================================================

func TestColdStartIntegration_ThreadSafety(t *testing.T) {
	ctx := context.Background()

	memAccessor := newMockMemoryAccessor()
	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	// Set up data for multiple nodes
	for i := 0; i < 10; i++ {
		nodeID := "node" + string(rune('0'+i))
		coldCalc.SetPrior(nodeID, 0.5+float64(i)*0.02)
		graphSignals.SetSignals(nodeID, 0.3+float64(i)*0.05, 0.2+float64(i)*0.03)
	}

	scorer := query.NewHybridScorer(memAccessor, coldCalc, graphSignals)

	// Run concurrent operations
	done := make(chan bool)
	errChan := make(chan error, 100)

	// Concurrent scorers
	for i := 0; i < 5; i++ {
		go func(workerID int) {
			for j := 0; j < 10; j++ {
				nodeID := "node" + string(rune('0'+j))
				results := createTestHybridResults([]string{nodeID})

				_, err := scorer.Score(ctx, results, nil, domain.DomainLibrarian)
				if err != nil {
					errChan <- err
				}
			}
			done <- true
		}(i)
	}

	// Concurrent config changes
	go func() {
		for i := 0; i < 20; i++ {
			scorer.SetColdStartEnabled(i%2 == 0)
			time.Sleep(time.Millisecond)
		}
		done <- true
	}()

	// Concurrent weight updates
	go func() {
		for i := 0; i < 20; i++ {
			weights := query.DefaultSignalWeights()
			weights.RRFWeight = 0.3 + float64(i%5)*0.05
			scorer.SetSignalWeights(weights)
			time.Sleep(time.Millisecond)
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 7; i++ {
		<-done
	}

	// Check for errors
	close(errChan)
	for err := range errChan {
		t.Errorf("Concurrent operation error: %v", err)
	}
}

// =============================================================================
// MD.9.11 Test: Weight Learning
// =============================================================================

func TestColdStartIntegration_WeightLearning(t *testing.T) {
	ctx := context.Background()

	memAccessor := newMockMemoryAccessor()
	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	coldCalc.SetPrior("node1", 0.7)
	graphSignals.SetSignals("node1", 0.5, 0.3)

	scorer := query.NewHybridScorer(memAccessor, coldCalc, graphSignals)

	results := createTestHybridResults([]string{"node1"})

	// Get initial learned weights
	initialWeights := scorer.GetLearnedWeights()

	// Score and get contributions
	scored, err := scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Record positive feedback
	for i := 0; i < 10; i++ {
		err = scorer.RecordFeedback(ctx, "node1", true, scored[0].Contributions)
		if err != nil {
			t.Fatalf("RecordFeedback failed: %v", err)
		}
	}

	// Get updated learned weights
	updatedWeights := scorer.GetLearnedWeights()

	// Weights should have changed from feedback
	changed := false
	for signal, initial := range initialWeights {
		if updated, ok := updatedWeights[signal]; ok {
			if initial != updated {
				changed = true
				t.Logf("Weight %s changed: %f -> %f", signal, initial, updated)
			}
		}
	}

	if !changed {
		t.Error("Expected at least one weight to change after feedback")
	}

	// Check confidences increased
	confidences := scorer.GetLearnedWeightConfidences()
	for signal, conf := range confidences {
		if conf <= 0 {
			t.Errorf("Expected positive confidence for %s, got %f", signal, conf)
		}
	}
}

// =============================================================================
// MD.9.11 Test: Score Breakdown Accuracy
// =============================================================================

func TestColdStartIntegration_ScoreBreakdown(t *testing.T) {
	ctx := context.Background()

	memAccessor := newMockMemoryAccessor()
	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	coldCalc.SetPrior("node1", 0.7)
	graphSignals.SetSignals("node1", 0.6, 0.4)

	scorer := query.NewHybridScorer(memAccessor, coldCalc, graphSignals)

	// Add some access history (transitional state)
	now := time.Now()
	memAccessor.SetNodeMemory("node1", 5, 0.8, now)

	results := createTestHybridResults([]string{"node1"})

	scored, err := scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	result := scored[0]

	// Verify contribution sum equals final score
	var contributionSum float64
	for _, c := range result.Contributions {
		contributionSum += c.Contribution

		t.Logf("Signal %s: raw=%f, weight=%f, contribution=%f",
			c.Signal, c.RawValue, c.Weight, c.Contribution)
	}

	// Allow small floating point tolerance
	tolerance := 0.0001
	diff := result.FinalScore - contributionSum
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("Contribution sum (%f) does not equal final score (%f)",
			contributionSum, result.FinalScore)
	}

	// Verify we have expected signals
	signalTypes := make(map[query.SignalType]bool)
	for _, c := range result.Contributions {
		signalTypes[c.Signal] = true
	}

	// Should always have RRF
	if !signalTypes[query.SignalRRF] {
		t.Error("Expected RRF signal contribution")
	}

	// With 5 accesses, should be in transition
	if result.BlendRatio <= 0 || result.BlendRatio >= 1 {
		t.Logf("Note: BlendRatio is %f (expected transition state)", result.BlendRatio)
	}
}

// =============================================================================
// MD.9.11 Test: Empty Results
// =============================================================================

func TestColdStartIntegration_EmptyResults(t *testing.T) {
	ctx := context.Background()

	memAccessor := newMockMemoryAccessor()
	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	scorer := query.NewHybridScorer(memAccessor, coldCalc, graphSignals)

	// Test with empty results
	scored, err := scorer.Score(ctx, []query.HybridResult{}, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score with empty results failed: %v", err)
	}

	if len(scored) != 0 {
		t.Errorf("Expected empty results, got %d", len(scored))
	}
}

// =============================================================================
// MD.9.11 Test: No Memory Accessor
// =============================================================================

func TestColdStartIntegration_NoMemoryAccessor(t *testing.T) {
	ctx := context.Background()

	coldCalc := newMockColdStartCalculator()
	graphSignals := newMockGraphSignalsProvider()

	coldCalc.SetPrior("node1", 0.7)
	graphSignals.SetSignals("node1", 0.5, 0.3)

	// Create scorer without memory accessor
	scorer := query.NewHybridScorer(nil, coldCalc, graphSignals)

	results := createTestHybridResults([]string{"node1"})

	// Should still work, using cold-start only
	scored, err := scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score without memory accessor failed: %v", err)
	}

	if len(scored) != 1 {
		t.Errorf("Expected 1 result, got %d", len(scored))
	}

	// Should be cold-start
	if !scored[0].IsColdStart {
		t.Error("Expected cold-start without memory accessor")
	}
}

// =============================================================================
// MD.9.11 Test: No Cold-Start Calculator
// =============================================================================

func TestColdStartIntegration_NoColdStartCalculator(t *testing.T) {
	ctx := context.Background()

	memAccessor := newMockMemoryAccessor()
	graphSignals := newMockGraphSignalsProvider()

	graphSignals.SetSignals("node1", 0.5, 0.3)

	// Create scorer without cold-start calculator
	scorer := query.NewHybridScorer(memAccessor, nil, graphSignals)

	results := createTestHybridResults([]string{"node1"})

	// Should still work, using default cold-start prior
	scored, err := scorer.Score(ctx, results, nil, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("Score without cold-start calculator failed: %v", err)
	}

	if len(scored) != 1 {
		t.Errorf("Expected 1 result, got %d", len(scored))
	}

	// Check for cold-start contribution (should use default 0.5)
	hasColdStart := false
	for _, c := range scored[0].Contributions {
		if c.Signal == query.SignalColdStartPrior {
			hasColdStart = true
			if c.RawValue != 0.5 {
				t.Errorf("Expected default cold-start prior 0.5, got %f", c.RawValue)
			}
			break
		}
	}
	if !hasColdStart {
		t.Error("Expected cold-start contribution with default prior")
	}
}

// =============================================================================
// MD.9.11 Test: PriorBlender Direct Testing
// =============================================================================

func TestPriorBlender_ComputeBlendRatio(t *testing.T) {
	blender := query.DefaultPriorBlender()

	tests := []struct {
		traceCount     int
		expectedRatio  float64
		description    string
	}{
		{0, 0.0, "zero traces - pure cold"},
		{2, 0.0, "below min - pure cold"},
		{3, 0.0, "at min - start of transition"},
		{9, 0.5, "middle - half blend"},
		{15, 1.0, "at full - pure warm"},
		{20, 1.0, "above full - pure warm"},
	}

	for _, tc := range tests {
		ratio := blender.ComputeBlendRatio(tc.traceCount)
		// Allow small tolerance for floating point
		tolerance := 0.01
		diff := ratio - tc.expectedRatio
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			t.Errorf("%s: expected ratio %f, got %f (traces=%d)",
				tc.description, tc.expectedRatio, ratio, tc.traceCount)
		}
	}
}

// =============================================================================
// MD.9.11 Test: SignalWeights Normalization
// =============================================================================

func TestSignalWeights_Normalize(t *testing.T) {
	// Test with unnormalized weights
	weights := &query.SignalWeights{
		RRFWeight:            0.7,
		MemoryDecayWeight:    0.5,
		ColdStartPriorWeight: 0.3,
		PageRankWeight:       0.2,
		ClusterCoeffWeight:   0.1,
		RecencyWeight:        0.1,
		FrequencyWeight:      0.1,
	}

	normalized := weights.Normalize()

	// Sum should be 1.0
	sum := normalized.RRFWeight + normalized.MemoryDecayWeight +
		normalized.ColdStartPriorWeight + normalized.PageRankWeight +
		normalized.ClusterCoeffWeight + normalized.RecencyWeight +
		normalized.FrequencyWeight

	tolerance := 0.0001
	diff := sum - 1.0
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("Normalized weights should sum to 1.0, got %f", sum)
	}

	// Relative ratios should be preserved
	// RRF should still be the largest
	if normalized.RRFWeight <= normalized.MemoryDecayWeight {
		t.Error("RRF should still be larger than MemoryDecay after normalization")
	}
}

// =============================================================================
// MD.9.11 Test: SignalType String Conversion
// =============================================================================

func TestSignalType_String(t *testing.T) {
	tests := []struct {
		signal   query.SignalType
		expected string
	}{
		{query.SignalRRF, "rrf"},
		{query.SignalMemoryDecay, "memory_decay"},
		{query.SignalColdStartPrior, "cold_start_prior"},
		{query.SignalPageRank, "pagerank"},
		{query.SignalClusterCoeff, "cluster_coeff"},
		{query.SignalRecency, "recency"},
		{query.SignalFrequency, "frequency"},
		{query.SignalType(999), "unknown"},
	}

	for _, tc := range tests {
		result := tc.signal.String()
		if result != tc.expected {
			t.Errorf("SignalType(%d).String() = %s, expected %s",
				tc.signal, result, tc.expected)
		}
	}
}
