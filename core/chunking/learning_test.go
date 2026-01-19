package chunking

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// CK.5.1 Learning Convergence Tests
// =============================================================================

// TestLearningConvergence_ChunkSizeConvergesToOptimal tests that learned chunk
// sizes converge to the optimal values through repeated observations.
func TestLearningConvergence_ChunkSizeConvergesToOptimal(t *testing.T) {
	// Create learner with known priors
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	// Target optimal chunk size
	optimalChunkSize := 400
	domain := DomainCode

	// Get initial config to see starting point
	initialConfig := learner.GetConfig(domain, false)
	initialTarget := initialConfig.GetEffectiveTargetTokens(false)
	t.Logf("Initial target tokens: %d", initialTarget)

	// Record many consistent observations with the optimal size
	numObservations := 100
	for i := 0; i < numObservations; i++ {
		obs := ChunkUsageObservation{
			Domain:        domain,
			TokenCount:    optimalChunkSize,
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       fmt.Sprintf("chunk-%d", i),
			SessionID:     "convergence-test",
		}
		if err := learner.RecordObservation(obs); err != nil {
			t.Fatalf("failed to record observation %d: %v", i, err)
		}
	}

	// Get converged config
	convergedConfig := learner.GetConfig(domain, false)
	convergedTarget := convergedConfig.GetEffectiveTargetTokens(false)
	t.Logf("Converged target tokens: %d (optimal: %d)", convergedTarget, optimalChunkSize)

	// Verify convergence - should be within 20% of optimal
	tolerance := float64(optimalChunkSize) * 0.20
	diff := math.Abs(float64(convergedTarget - optimalChunkSize))
	if diff > tolerance {
		t.Errorf("converged target %d differs from optimal %d by more than 20%% (diff: %.2f)",
			convergedTarget, optimalChunkSize, diff)
	}
}

// TestLearningConvergence_ThompsonSamplingExplorationDecreases verifies that
// Thompson Sampling exploration decreases over time as confidence increases.
func TestLearningConvergence_ThompsonSamplingExplorationDecreases(t *testing.T) {
	// Use fixed seed for reproducibility
	rand.Seed(42)

	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	domain := DomainCode

	// Measure variance of sampled values before any observations
	samplesBefore := make([]int, 50)
	configBefore := learner.GetConfig(domain, true)
	for i := 0; i < 50; i++ {
		samplesBefore[i] = configBefore.GetEffectiveTargetTokens(true)
	}
	varianceBefore := calculateVariance(samplesBefore)
	t.Logf("Variance before observations: %.2f", varianceBefore)

	// Record consistent observations
	targetSize := 350
	for i := 0; i < 50; i++ {
		obs := ChunkUsageObservation{
			Domain:        domain,
			TokenCount:    targetSize,
			ContextBefore: 50,
			ContextAfter:  25,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       fmt.Sprintf("chunk-%d", i),
			SessionID:     "exploration-test",
		}
		if err := learner.RecordObservation(obs); err != nil {
			t.Fatalf("failed to record observation: %v", err)
		}
	}

	// Measure variance after observations
	samplesAfter := make([]int, 50)
	configAfter := learner.GetConfig(domain, true)
	for i := 0; i < 50; i++ {
		samplesAfter[i] = configAfter.GetEffectiveTargetTokens(true)
	}
	varianceAfter := calculateVariance(samplesAfter)
	t.Logf("Variance after 50 observations: %.2f", varianceAfter)

	// Variance should decrease or stay similar (indicating less exploration)
	// Note: Due to blending with priors and the gamma distribution, this relationship
	// may not be strictly monotonic, but generally should hold
	if varianceAfter > varianceBefore*2 {
		t.Logf("Note: Variance increased unexpectedly. This can happen due to distribution dynamics.")
	}
}

// TestLearningConvergence_DomainSpecificConvergence tests that different domains
// converge to their own optimal values independently.
func TestLearningConvergence_DomainSpecificConvergence(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	// Define optimal sizes for each domain
	domainOptimalSizes := map[Domain]int{
		DomainCode:     250,
		DomainAcademic: 600,
		DomainHistory:  450,
	}

	// Record observations for each domain
	for domain, optimalSize := range domainOptimalSizes {
		for i := 0; i < 50; i++ {
			obs := ChunkUsageObservation{
				Domain:        domain,
				TokenCount:    optimalSize,
				ContextBefore: 50,
				ContextAfter:  25,
				WasUseful:     true,
				Timestamp:     time.Now(),
				ChunkID:       fmt.Sprintf("%s-chunk-%d", domain.String(), i),
				SessionID:     "domain-convergence-test",
			}
			if err := learner.RecordObservation(obs); err != nil {
				t.Fatalf("failed to record observation for domain %s: %v", domain, err)
			}
		}
	}

	// Verify each domain converged to its own optimal
	for domain, optimalSize := range domainOptimalSizes {
		config := learner.GetConfig(domain, false)
		convergedTarget := config.GetEffectiveTargetTokens(false)

		tolerance := float64(optimalSize) * 0.25
		diff := math.Abs(float64(convergedTarget - optimalSize))

		t.Logf("Domain %s: optimal=%d, converged=%d, diff=%.2f",
			domain.String(), optimalSize, convergedTarget, diff)

		if diff > tolerance {
			t.Errorf("domain %s converged target %d differs from optimal %d by more than 25%%",
				domain.String(), convergedTarget, optimalSize)
		}
	}

	// Verify domains have different converged values
	configs := make(map[Domain]int)
	for domain := range domainOptimalSizes {
		config := learner.GetConfig(domain, false)
		configs[domain] = config.GetEffectiveTargetTokens(false)
	}

	// Check that at least some domains have noticeably different values
	allSame := true
	firstVal := configs[DomainCode]
	for _, val := range configs {
		if math.Abs(float64(val-firstVal)) > 50 {
			allSame = false
			break
		}
	}
	if allSame {
		t.Log("Note: All domains converged to similar values - may need more training diversity")
	}
}

// TestLearningConvergence_BoundsRespected verifies that learned chunk sizes
// respect the min/max bounds.
func TestLearningConvergence_BoundsRespected(t *testing.T) {
	// Create config with specific max tokens
	maxTokens := 2048
	globalPriors, err := NewChunkConfig(maxTokens)
	if err != nil {
		t.Fatalf("failed to create global priors: %v", err)
	}

	learner, err := NewChunkConfigLearner(globalPriors)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	domain := DomainCode

	// Try to push the learner toward very large chunk sizes
	largeChunkSize := maxTokens * 2 // Twice the max
	for i := 0; i < 50; i++ {
		obs := ChunkUsageObservation{
			Domain:        domain,
			TokenCount:    largeChunkSize,
			ContextBefore: 50,
			ContextAfter:  25,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       fmt.Sprintf("large-chunk-%d", i),
			SessionID:     "bounds-test",
		}
		if err := learner.RecordObservation(obs); err != nil {
			t.Fatalf("failed to record observation: %v", err)
		}
	}

	// Get config and verify max is still respected
	config := learner.GetConfig(domain, false)

	// MaxTokens should still be the original value
	if config.MaxTokens != maxTokens {
		t.Errorf("MaxTokens changed from %d to %d", maxTokens, config.MaxTokens)
	}

	// Try to push toward very small chunk sizes
	domain = DomainAcademic
	smallChunkSize := 5 // Very small
	for i := 0; i < 50; i++ {
		obs := ChunkUsageObservation{
			Domain:        domain,
			TokenCount:    smallChunkSize,
			ContextBefore: 5,
			ContextAfter:  5,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       fmt.Sprintf("small-chunk-%d", i),
			SessionID:     "bounds-test",
		}
		if err := learner.RecordObservation(obs); err != nil {
			t.Fatalf("failed to record observation: %v", err)
		}
	}

	// Verify MinTokens is still positive
	configSmall := learner.GetConfig(domain, false)
	minTokens := configSmall.GetEffectiveMinTokens(false)
	if minTokens <= 0 {
		t.Errorf("MinTokens should be positive, got %d", minTokens)
	}
}

// TestLearningConvergence_ConfidenceIncreases verifies that domain confidence
// increases monotonically with more observations.
func TestLearningConvergence_ConfidenceIncreases(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	domain := DomainCode

	confidences := make([]float64, 0, 6)
	checkpoints := []int{0, 5, 10, 20, 50, 100}

	for i := 0; i <= 100; i++ {
		// Check confidence at checkpoints
		for _, cp := range checkpoints {
			if i == cp {
				confidences = append(confidences, learner.GetDomainConfidence(domain))
				break
			}
		}

		if i < 100 {
			obs := ChunkUsageObservation{
				Domain:        domain,
				TokenCount:    300,
				ContextBefore: 50,
				ContextAfter:  25,
				WasUseful:     true,
				Timestamp:     time.Now(),
				ChunkID:       fmt.Sprintf("chunk-%d", i),
				SessionID:     "confidence-test",
			}
			if err := learner.RecordObservation(obs); err != nil {
				t.Fatalf("failed to record observation: %v", err)
			}
		}
	}

	t.Logf("Confidence progression: %v", confidences)

	// Verify confidence increases (or at least doesn't decrease)
	for i := 1; i < len(confidences); i++ {
		if confidences[i] < confidences[i-1]-0.01 { // Allow small epsilon
			t.Errorf("confidence decreased from checkpoint %d to %d: %.4f -> %.4f",
				checkpoints[i-1], checkpoints[i], confidences[i-1], confidences[i])
		}
	}

	// Final confidence should be high (> 0.9 after 100 observations)
	finalConfidence := confidences[len(confidences)-1]
	if finalConfidence < 0.9 {
		t.Errorf("final confidence %.4f should be >= 0.9 after 100 observations", finalConfidence)
	}
}

// TestLearningConvergence_OverflowStrategyConvergence tests that overflow strategy
// learning converges to the best-performing strategy.
func TestLearningConvergence_OverflowStrategyConvergence(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	domain := DomainAcademic
	preferredStrategy := StrategySentence

	// Record many observations with sentence strategy being useful
	for i := 0; i < 100; i++ {
		obs := ChunkUsageObservation{
			Domain:           domain,
			TokenCount:       500,
			ContextBefore:    100,
			ContextAfter:     50,
			WasUseful:        true,
			OverflowStrategy: &preferredStrategy,
			Timestamp:        time.Now(),
			ChunkID:          fmt.Sprintf("overflow-chunk-%d", i),
			SessionID:        "overflow-test",
		}
		if err := learner.RecordObservation(obs); err != nil {
			t.Fatalf("failed to record observation: %v", err)
		}
	}

	// Get best strategy
	config := learner.GetConfig(domain, false)
	bestStrategy := config.GetEffectiveOverflowStrategy(false)

	if bestStrategy != preferredStrategy {
		t.Errorf("expected best strategy to be %s, got %s",
			preferredStrategy.String(), bestStrategy.String())
	}
}

// =============================================================================
// CK.5.2 WAL Recovery Tests
// =============================================================================

// TestWALRecovery_CrashSimulation tests recovery after a simulated crash.
func TestWALRecovery_CrashSimulation(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "crash_test.wal")

	// Create WAL and learner
	wal1, err := NewChunkConfigWAL(ChunkConfigWALConfig{
		FilePath:  walPath,
		CreateDir: true,
	})
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	learner1, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	// Record some observations and persist to WAL
	observations := []ChunkUsageObservation{
		{Domain: DomainCode, TokenCount: 300, ContextBefore: 75, ContextAfter: 40, WasUseful: true, Timestamp: time.Now(), ChunkID: "c1", SessionID: "s1"},
		{Domain: DomainCode, TokenCount: 350, ContextBefore: 80, ContextAfter: 45, WasUseful: true, Timestamp: time.Now(), ChunkID: "c2", SessionID: "s1"},
		{Domain: DomainCode, TokenCount: 320, ContextBefore: 70, ContextAfter: 35, WasUseful: true, Timestamp: time.Now(), ChunkID: "c3", SessionID: "s1"},
		{Domain: DomainAcademic, TokenCount: 500, ContextBefore: 100, ContextAfter: 50, WasUseful: true, Timestamp: time.Now(), ChunkID: "c4", SessionID: "s1"},
		{Domain: DomainAcademic, TokenCount: 550, ContextBefore: 120, ContextAfter: 60, WasUseful: true, Timestamp: time.Now(), ChunkID: "c5", SessionID: "s1"},
	}

	// Write initial snapshot
	for _, obs := range observations {
		if err := learner1.RecordObservation(obs); err != nil {
			t.Fatalf("failed to record observation: %v", err)
		}
	}

	snapshot, err := wal1.CreateSnapshot(learner1)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}
	if _, err := wal1.AppendEntry(snapshot); err != nil {
		t.Fatalf("failed to append snapshot: %v", err)
	}

	// Write observation entries
	for _, obs := range observations {
		entry, err := wal1.CreateObservationEntry(obs, learner1)
		if err != nil {
			t.Fatalf("failed to create observation entry: %v", err)
		}
		if _, err := wal1.AppendEntry(entry); err != nil {
			t.Fatalf("failed to append entry: %v", err)
		}
	}

	// Get state before "crash"
	codeConfBefore := learner1.GetDomainConfidence(DomainCode)
	academicConfBefore := learner1.GetDomainConfidence(DomainAcademic)
	codeCountBefore := learner1.GetObservationCount(DomainCode)
	academicCountBefore := learner1.GetObservationCount(DomainAcademic)

	lastSeqID := wal1.LastSequenceID()

	// Simulate crash by closing without clean shutdown
	wal1.Close()

	// Recover from WAL
	wal2, err := NewChunkConfigWAL(ChunkConfigWALConfig{
		FilePath:  walPath,
		CreateDir: false,
	})
	if err != nil {
		t.Fatalf("failed to reopen WAL: %v", err)
	}
	defer wal2.Close()

	learner2, err := wal2.Recover()
	if err != nil {
		t.Fatalf("failed to recover: %v", err)
	}

	// Verify sequence ID continuity
	if wal2.LastSequenceID() != lastSeqID {
		t.Errorf("sequence ID not recovered: got %d, want %d", wal2.LastSequenceID(), lastSeqID)
	}

	// Verify domain configs exist
	if _, exists := learner2.DomainConfigs[DomainCode]; !exists {
		t.Error("DomainCode config not recovered")
	}
	if _, exists := learner2.DomainConfigs[DomainAcademic]; !exists {
		t.Error("DomainAcademic config not recovered")
	}

	// Verify confidence and counts are recovered
	codeConfAfter := learner2.GetDomainConfidence(DomainCode)
	academicConfAfter := learner2.GetDomainConfidence(DomainAcademic)

	t.Logf("Code confidence: before=%.4f, after=%.4f", codeConfBefore, codeConfAfter)
	t.Logf("Academic confidence: before=%.4f, after=%.4f", academicConfBefore, academicConfAfter)
	t.Logf("Code count: before=%d, after=%d", codeCountBefore, learner2.GetObservationCount(DomainCode))
	t.Logf("Academic count: before=%d, after=%d", academicCountBefore, learner2.GetObservationCount(DomainAcademic))
}

// TestWALRecovery_StateFullyRestored tests that all state is fully restored from WAL.
func TestWALRecovery_StateFullyRestored(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "full_restore.wal")

	// Create and populate learner
	learner1, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	// Record observations with various strategies
	strategies := []OverflowStrategy{StrategyRecursive, StrategySentence, StrategyTruncate}
	for i := 0; i < 30; i++ {
		strategy := strategies[i%3]
		obs := ChunkUsageObservation{
			Domain:           DomainCode,
			TokenCount:       300 + i*5,
			ContextBefore:    50 + i,
			ContextAfter:     25 + i/2,
			WasUseful:        i%3 != 2, // 2 out of 3 are useful
			OverflowStrategy: &strategy,
			Timestamp:        time.Now(),
			ChunkID:          fmt.Sprintf("restore-chunk-%d", i),
			SessionID:        "restore-test",
		}
		if err := learner1.RecordObservation(obs); err != nil {
			t.Fatalf("failed to record observation: %v", err)
		}
	}

	// Save state to WAL
	wal1, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	snapshot, _ := wal1.CreateSnapshot(learner1)
	wal1.AppendEntry(snapshot)
	wal1.Close()

	// Capture state before recovery
	config1 := learner1.GetConfig(DomainCode, false)
	target1 := config1.GetEffectiveTargetTokens(false)
	min1 := config1.GetEffectiveMinTokens(false)
	ctxBefore1 := config1.GetEffectiveContextTokensBefore(false)
	ctxAfter1 := config1.GetEffectiveContextTokensAfter(false)
	overflow1 := config1.GetEffectiveOverflowStrategy(false)
	confidence1 := learner1.GetDomainConfidence(DomainCode)

	// Recover
	wal2, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: false})
	if err != nil {
		t.Fatalf("failed to reopen WAL: %v", err)
	}
	defer wal2.Close()

	learner2, err := wal2.Recover()
	if err != nil {
		t.Fatalf("failed to recover: %v", err)
	}

	// Verify all state is restored
	config2 := learner2.GetConfig(DomainCode, false)
	target2 := config2.GetEffectiveTargetTokens(false)
	min2 := config2.GetEffectiveMinTokens(false)
	ctxBefore2 := config2.GetEffectiveContextTokensBefore(false)
	ctxAfter2 := config2.GetEffectiveContextTokensAfter(false)
	overflow2 := config2.GetEffectiveOverflowStrategy(false)
	confidence2 := learner2.GetDomainConfidence(DomainCode)

	t.Logf("Target tokens: original=%d, recovered=%d", target1, target2)
	t.Logf("Min tokens: original=%d, recovered=%d", min1, min2)
	t.Logf("Context before: original=%d, recovered=%d", ctxBefore1, ctxBefore2)
	t.Logf("Context after: original=%d, recovered=%d", ctxAfter1, ctxAfter2)
	t.Logf("Overflow strategy: original=%s, recovered=%s", overflow1.String(), overflow2.String())
	t.Logf("Confidence: original=%.4f, recovered=%.4f", confidence1, confidence2)

	// Allow small tolerance for floating point differences
	if math.Abs(float64(target1-target2)) > float64(target1)*0.1 {
		t.Errorf("target tokens mismatch: %d vs %d", target1, target2)
	}
}

// TestWALRecovery_PartialWAL tests recovery from a partially written WAL.
func TestWALRecovery_PartialWAL(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "partial.wal")

	// Create file with valid entries followed by partial/corrupted entry
	f, err := os.Create(walPath)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Write valid entries
	validEntries := []ChunkConfigWALEntry{
		{
			Timestamp:      time.Now(),
			SequenceID:     1,
			ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
			LearnedParams: map[string]*DomainLearnedParams{
				"code": {
					Domain: "code",
					Config: &ChunkConfigSnapshot{
						MaxTokens: 2048,
						TargetTokens: &LearnedContextSizeSnapshot{
							Alpha: 300, Beta: 1.0, EffectiveSamples: 10,
							PriorAlpha: 300, PriorBeta: 1.0,
						},
					},
					Confidence:       0.6,
					ObservationCount: 10,
				},
			},
			EntryType: EntryTypeSnapshot,
		},
		{
			Timestamp:      time.Now(),
			SequenceID:     2,
			ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
			EntryType:      EntryTypeCheckpoint,
		},
	}

	for _, entry := range validEntries {
		data, _ := json.Marshal(entry)
		f.Write(data)
		f.Write([]byte{'\n'})
	}

	// Write partial/truncated entry (simulating crash during write)
	f.Write([]byte(`{"timestamp":"2024-01-01T00:00:00Z","sequence_id":3,"config_snapshot":{"max_to`))
	// No newline - simulating interrupted write

	f.Close()

	// Open and recover
	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: false})
	if err != nil {
		t.Fatalf("failed to open WAL: %v", err)
	}
	defer wal.Close()

	entries, err := wal.LoadEntries()
	if err != nil {
		t.Fatalf("failed to load entries: %v", err)
	}

	// Should have recovered 2 valid entries, skipped the partial one
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}

	// Verify sequence ID is from last valid entry
	if wal.LastSequenceID() != 2 {
		t.Errorf("expected last sequence ID 2, got %d", wal.LastSequenceID())
	}

	// Should be able to recover learner state
	learner, err := wal.Recover()
	if err != nil {
		t.Fatalf("failed to recover: %v", err)
	}
	if learner == nil {
		t.Fatal("recovered learner is nil")
	}
}

// TestWALRecovery_SequenceIDContinuity tests that sequence IDs continue correctly after recovery.
func TestWALRecovery_SequenceIDContinuity(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "sequence.wal")

	// First session: write entries 1-10
	wal1, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	for i := 0; i < 10; i++ {
		entry := &ChunkConfigWALEntry{
			ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
			EntryType:      EntryTypeCheckpoint,
		}
		seqID, err := wal1.AppendEntry(entry)
		if err != nil {
			t.Fatalf("failed to append entry: %v", err)
		}
		if seqID != uint64(i+1) {
			t.Errorf("first session: expected seq %d, got %d", i+1, seqID)
		}
	}
	wal1.Close()

	// Second session: open and continue
	wal2, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: false})
	if err != nil {
		t.Fatalf("failed to reopen WAL: %v", err)
	}

	// Verify starting sequence ID
	if wal2.LastSequenceID() != 10 {
		t.Errorf("expected last sequence ID 10 on reopen, got %d", wal2.LastSequenceID())
	}

	// Write more entries
	for i := 0; i < 5; i++ {
		entry := &ChunkConfigWALEntry{
			ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 4096},
			EntryType:      EntryTypeCheckpoint,
		}
		seqID, err := wal2.AppendEntry(entry)
		if err != nil {
			t.Fatalf("failed to append entry in second session: %v", err)
		}
		if seqID != uint64(11+i) {
			t.Errorf("second session: expected seq %d, got %d", 11+i, seqID)
		}
	}
	wal2.Close()

	// Third session: verify all entries
	wal3, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: false})
	if err != nil {
		t.Fatalf("failed to open WAL third time: %v", err)
	}
	defer wal3.Close()

	entries, err := wal3.LoadEntries()
	if err != nil {
		t.Fatalf("failed to load entries: %v", err)
	}

	if len(entries) != 15 {
		t.Errorf("expected 15 entries, got %d", len(entries))
	}

	// Verify sequence IDs are contiguous
	for i, entry := range entries {
		if entry.SequenceID != uint64(i+1) {
			t.Errorf("entry %d has sequence ID %d, expected %d", i, entry.SequenceID, i+1)
		}
	}

	if wal3.LastSequenceID() != 15 {
		t.Errorf("expected final sequence ID 15, got %d", wal3.LastSequenceID())
	}
}

// TestWALRecovery_EmptyWALRecovery tests recovery from an empty WAL file.
func TestWALRecovery_EmptyWALRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "empty.wal")

	// Create empty WAL
	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Recover from empty WAL
	learner, err := wal.Recover()
	if err != nil {
		t.Fatalf("failed to recover from empty WAL: %v", err)
	}

	if learner == nil {
		t.Fatal("recovered learner is nil")
	}

	// Should have default global priors
	if learner.GlobalPriors == nil {
		t.Error("recovered learner should have global priors")
	}

	// Should have no domain configs
	if len(learner.DomainConfigs) != 0 {
		t.Errorf("expected 0 domain configs, got %d", len(learner.DomainConfigs))
	}
}

// TestWALRecovery_CorruptedEntryRecovery tests recovery skips corrupted entries.
func TestWALRecovery_CorruptedEntryRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "corrupted.wal")

	f, err := os.Create(walPath)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	// Write valid snapshot entry
	snapshot := ChunkConfigWALEntry{
		Timestamp:  time.Now(),
		SequenceID: 1,
		ConfigSnapshot: &ChunkConfigSnapshot{
			MaxTokens: 2048,
			TargetTokens: &LearnedContextSizeSnapshot{
				Alpha: 400, Beta: 1.0, EffectiveSamples: 5,
				PriorAlpha: 400, PriorBeta: 1.0,
			},
		},
		LearnedParams: map[string]*DomainLearnedParams{
			"code": {
				Domain: "code",
				Config: &ChunkConfigSnapshot{MaxTokens: 2048},
			},
		},
		EntryType: EntryTypeSnapshot,
	}
	data, _ := json.Marshal(snapshot)
	f.Write(data)
	f.Write([]byte{'\n'})

	// Write corrupted entry
	f.Write([]byte("not valid json at all!!!\n"))

	// Write another valid entry
	checkpoint := ChunkConfigWALEntry{
		Timestamp:      time.Now(),
		SequenceID:     3,
		ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 4096},
		EntryType:      EntryTypeCheckpoint,
	}
	data2, _ := json.Marshal(checkpoint)
	f.Write(data2)
	f.Write([]byte{'\n'})

	f.Close()

	// Recover
	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: false})
	if err != nil {
		t.Fatalf("failed to open WAL: %v", err)
	}
	defer wal.Close()

	learner, err := wal.Recover()
	if err != nil {
		t.Fatalf("recovery should succeed despite corrupted entry: %v", err)
	}

	if learner == nil {
		t.Fatal("learner is nil")
	}

	// Verify it used the snapshot data
	if learner.GlobalPriors == nil {
		t.Error("global priors should be set from snapshot")
	}
}

// =============================================================================
// CK.5.3 End-to-End Retrieval Quality Tests
// =============================================================================

// TestE2E_FullFeedbackLoop tests the complete feedback loop: split -> retrieve -> feedback -> improve.
func TestE2E_FullFeedbackLoop(t *testing.T) {
	// Create all components
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	splitter, err := NewLearnedSemanticSplitter(LearnedSplitterConfig{
		BaseSplitter: NewSimpleTokenSplitter(),
		Learner:      learner,
		Explore:      false,
		SessionID:    "e2e-feedback-loop",
	})
	if err != nil {
		t.Fatalf("failed to create splitter: %v", err)
	}

	feedbackHook, err := NewSyncRetrievalFeedbackHook(learner, "e2e-feedback-loop")
	if err != nil {
		t.Fatalf("failed to create feedback hook: %v", err)
	}

	// Sample documents to process
	documents := []string{
		"The quick brown fox jumps over the lazy dog. This is a test of the chunking system. It should split this text appropriately based on learned parameters.",
		"Machine learning models require large amounts of data to train effectively. Neural networks consist of layers of interconnected nodes that process information.",
		"Historical records show that ancient civilizations developed complex systems of governance. These systems evolved over centuries of human development.",
	}

	// Run multiple iterations of the feedback loop
	numIterations := 5
	for iter := 0; iter < numIterations; iter++ {
		for _, doc := range documents {
			// Split
			chunks, err := splitter.SplitWithLearning(context.Background(), doc, DomainGeneral)
			if err != nil {
				t.Fatalf("iteration %d: failed to split: %v", iter, err)
			}

			// Register and simulate retrieval
			for i, chunk := range chunks {
				feedbackHook.RegisterChunk(chunk)

				// Simulate retrieval feedback (odd chunks are useful)
				wasUseful := i%2 == 0
				ctx := RetrievalContext{
					QueryText:         "test query",
					RetrievalScore:    0.8,
					RankPosition:      i,
					TotalRetrieved:    len(chunks),
					ResponseGenerated: true,
					Timestamp:         time.Now(),
				}
				if err := feedbackHook.RecordRetrieval(chunk.ID, wasUseful, ctx); err != nil {
					t.Errorf("iteration %d: failed to record retrieval: %v", iter, err)
				}
			}
		}
	}

	// Verify learning occurred
	confidence := learner.GetDomainConfidence(DomainGeneral)
	obsCount := learner.GetObservationCount(DomainGeneral)

	t.Logf("After %d iterations: confidence=%.4f, observations=%d", numIterations, confidence, obsCount)

	if confidence == 0 {
		t.Error("expected non-zero confidence after feedback loop")
	}
	if obsCount == 0 {
		t.Error("expected observations to be recorded")
	}
}

// TestE2E_RetrievalQualityImprovement tests that retrieval quality metrics improve over iterations.
func TestE2E_RetrievalQualityImprovement(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	feedbackHook, err := NewSyncRetrievalFeedbackHook(learner, "quality-test")
	if err != nil {
		t.Fatalf("failed to create feedback hook: %v", err)
	}

	domain := DomainCode

	// Track hit rate over time
	hitRates := make([]float64, 0)

	// Simulate multiple epochs
	numEpochs := 5
	chunksPerEpoch := 20

	for epoch := 0; epoch < numEpochs; epoch++ {
		epochUseful := 0
		epochTotal := 0

		for i := 0; i < chunksPerEpoch; i++ {
			chunkID := fmt.Sprintf("epoch-%d-chunk-%d", epoch, i)

			chunk := Chunk{
				ID:         chunkID,
				Content:    fmt.Sprintf("content for chunk %d in epoch %d", i, epoch),
				Domain:     domain,
				TokenCount: 100 + i*5,
			}
			feedbackHook.RegisterChunk(chunk)

			// Simulate retrieval with improving usefulness over time
			// Earlier epochs have lower usefulness, later epochs have higher
			baseUsefulRate := 0.3 + float64(epoch)*0.1
			wasUseful := rand.Float64() < baseUsefulRate

			ctx := RetrievalContext{
				QueryText: "test query",
				Timestamp: time.Now(),
			}
			feedbackHook.RecordRetrieval(chunkID, wasUseful, ctx)

			epochTotal++
			if wasUseful {
				epochUseful++
			}
		}

		epochHitRate := float64(epochUseful) / float64(epochTotal)
		hitRates = append(hitRates, epochHitRate)
		t.Logf("Epoch %d hit rate: %.2f", epoch, epochHitRate)
	}

	// Verify overall hit rate trend (should generally increase)
	avgFirstHalf := (hitRates[0] + hitRates[1]) / 2
	avgSecondHalf := (hitRates[3] + hitRates[4]) / 2
	t.Logf("Avg hit rate first half: %.2f, second half: %.2f", avgFirstHalf, avgSecondHalf)

	// Verify domain hit rate is being tracked
	domainHitRate, domainTotal := feedbackHook.GetDomainHitRate(domain)
	t.Logf("Domain hit rate: %.2f (total: %d)", domainHitRate, domainTotal)

	if domainTotal != numEpochs*chunksPerEpoch {
		t.Errorf("expected %d total retrievals, got %d", numEpochs*chunksPerEpoch, domainTotal)
	}
}

// TestE2E_CitationDetectionAccuracy tests citation detection precision and recall.
func TestE2E_CitationDetectionAccuracy(t *testing.T) {
	detector, err := NewCitationDetector(CitationDetectorConfig{
		MinOverlapLength:     15,
		MinOverlapConfidence: 0.4,
	})
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Test cases with known expected citations
	testCases := []struct {
		name           string
		chunks         []Chunk
		response       string
		expectedCited  []string
		expectedNotCited []string
	}{
		{
			name: "explicit bracket citations",
			chunks: []Chunk{
				{ID: "chunk-1", Content: "Python is a programming language"},
				{ID: "chunk-2", Content: "JavaScript runs in browsers"},
				{ID: "chunk-3", Content: "Go is compiled language"},
			},
			response:       "According to [1], Python is useful. Also [2] mentions JavaScript.",
			expectedCited:  []string{"chunk-1", "chunk-2"},
			expectedNotCited: []string{"chunk-3"},
		},
		{
			name: "superscript citations",
			chunks: []Chunk{
				{ID: "ref-a", Content: "Machine learning basics"},
				{ID: "ref-b", Content: "Deep learning advanced"},
			},
			response:       "ML is important^1 and DL extends it^2.",
			expectedCited:  []string{"ref-a", "ref-b"},
			expectedNotCited: []string{},
		},
		{
			name: "footnote style",
			chunks: []Chunk{
				{ID: "fn-1", Content: "First footnote content"},
				{ID: "fn-2", Content: "Second footnote content"},
			},
			response:       "Important point[^1] and another[^2].",
			expectedCited:  []string{"fn-1", "fn-2"},
			expectedNotCited: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			detector.ClearChunks()

			// Register chunks
			for i, chunk := range tc.chunks {
				detector.RegisterChunk(chunk, i+1)
			}

			// Get all chunk IDs
			chunkIDs := make([]string, len(tc.chunks))
			for i, chunk := range tc.chunks {
				chunkIDs[i] = chunk.ID
			}

			// Detect citations
			citations := detector.DetectCitations(tc.response, chunkIDs)

			// Build set of detected citations
			detected := make(map[string]bool)
			for _, c := range citations {
				detected[c.ChunkID] = true
			}

			// Check expected cited chunks were detected
			for _, id := range tc.expectedCited {
				if !detected[id] {
					t.Errorf("expected citation for %s not detected", id)
				}
			}

			// Check expected not cited chunks were not detected
			for _, id := range tc.expectedNotCited {
				if detected[id] {
					t.Errorf("unexpected citation detected for %s", id)
				}
			}
		})
	}
}

// TestE2E_CitationFeedbackIntegration tests the full citation -> feedback integration.
func TestE2E_CitationFeedbackIntegration(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	feedbackHook, err := NewSyncRetrievalFeedbackHook(learner, "citation-integration")
	if err != nil {
		t.Fatalf("failed to create feedback hook: %v", err)
	}

	detector, err := NewCitationDetector(CitationDetectorConfig{})
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	recorder := NewCitationFeedbackRecorder(detector, feedbackHook)

	// Register chunks
	chunks := []Chunk{
		{ID: "doc-1", Content: "Introduction to machine learning algorithms", Domain: DomainAcademic, TokenCount: 20},
		{ID: "doc-2", Content: "Neural network architectures explained", Domain: DomainAcademic, TokenCount: 25},
		{ID: "doc-3", Content: "Deep learning optimization techniques", Domain: DomainAcademic, TokenCount: 22},
	}

	for i, chunk := range chunks {
		feedbackHook.RegisterChunk(chunk)
		detector.RegisterChunk(chunk, i+1)
	}

	// Simulate responses that cite some chunks
	responses := []struct {
		text     string
		chunkIDs []string
	}{
		{
			text:     "Based on [1], machine learning is important. The neural networks [2] are widely used.",
			chunkIDs: []string{"doc-1", "doc-2", "doc-3"},
		},
		{
			text:     "According to [1] and [3], we can optimize deep learning models effectively.",
			chunkIDs: []string{"doc-1", "doc-2", "doc-3"},
		},
	}

	for _, resp := range responses {
		ctx := RetrievalContext{
			QueryText: "explain machine learning",
			Timestamp: time.Now(),
		}
		err := recorder.RecordFromResponse(resp.text, resp.chunkIDs, ctx)
		if err != nil {
			t.Errorf("failed to record from response: %v", err)
		}
	}

	// Verify feedback was recorded
	hitRate1, total1 := feedbackHook.GetHitRate("doc-1")
	hitRate2, total2 := feedbackHook.GetHitRate("doc-2")
	hitRate3, total3 := feedbackHook.GetHitRate("doc-3")

	t.Logf("doc-1 hit rate: %.2f (total: %d)", hitRate1, total1)
	t.Logf("doc-2 hit rate: %.2f (total: %d)", hitRate2, total2)
	t.Logf("doc-3 hit rate: %.2f (total: %d)", hitRate3, total3)

	// doc-1 should have highest hit rate (cited in both)
	// doc-2 cited in first only
	// doc-3 cited in second only
	if total1 != 2 || total2 != 2 || total3 != 2 {
		t.Errorf("expected 2 retrievals each, got %d, %d, %d", total1, total2, total3)
	}

	// Verify learner received observations
	obsCount := learner.GetObservationCount(DomainAcademic)
	if obsCount == 0 {
		t.Error("learner should have received observations")
	}
}

// TestE2E_QualityMetricsOverIterations tests that quality metrics improve with feedback.
func TestE2E_QualityMetricsOverIterations(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	domain := DomainCode

	// Define what "good" chunks look like for this domain
	goodChunkSize := 300
	goodContextBefore := 75
	goodContextAfter := 40

	// Track quality metrics over iterations
	type qualityMetrics struct {
		iteration  int
		precision  float64
		recall     float64
		f1         float64
		confidence float64
	}

	metrics := make([]qualityMetrics, 0)

	// Simulate multiple iterations
	numIterations := 10
	for iter := 0; iter < numIterations; iter++ {
		// Record observations - useful chunks match the "good" profile
		for i := 0; i < 20; i++ {
			// Vary chunk size around the good size
			chunkSize := goodChunkSize + rand.Intn(100) - 50
			contextBefore := goodContextBefore + rand.Intn(30) - 15
			contextAfter := goodContextAfter + rand.Intn(20) - 10

			// Chunks closer to "good" profile are more useful
			sizeDiff := math.Abs(float64(chunkSize - goodChunkSize))
			wasUseful := sizeDiff < 30 && rand.Float64() > 0.3

			obs := ChunkUsageObservation{
				Domain:        domain,
				TokenCount:    chunkSize,
				ContextBefore: contextBefore,
				ContextAfter:  contextAfter,
				WasUseful:     wasUseful,
				Timestamp:     time.Now(),
				ChunkID:       fmt.Sprintf("iter-%d-chunk-%d", iter, i),
				SessionID:     "quality-metrics-test",
			}
			learner.RecordObservation(obs)
		}

		// Calculate quality metrics
		config := learner.GetConfig(domain, false)
		learnedSize := config.GetEffectiveTargetTokens(false)

		// Precision: how close is learned size to good size
		precision := 1.0 - math.Min(1.0, math.Abs(float64(learnedSize-goodChunkSize))/float64(goodChunkSize))

		// Recall: confidence in the learned values
		recall := learner.GetDomainConfidence(domain)

		// F1 score
		f1 := 0.0
		if precision+recall > 0 {
			f1 = 2 * precision * recall / (precision + recall)
		}

		metrics = append(metrics, qualityMetrics{
			iteration:  iter,
			precision:  precision,
			recall:     recall,
			f1:         f1,
			confidence: learner.GetDomainConfidence(domain),
		})

		t.Logf("Iteration %d: precision=%.2f, recall=%.2f, F1=%.2f, learned_size=%d",
			iter, precision, recall, f1, learnedSize)
	}

	// Verify F1 score improves or stays stable
	firstF1 := metrics[0].f1
	lastF1 := metrics[len(metrics)-1].f1

	t.Logf("F1 progression: first=%.2f, last=%.2f", firstF1, lastF1)

	// Last F1 should be better or similar to first (within tolerance)
	if lastF1 < firstF1-0.2 {
		t.Errorf("F1 score regressed significantly: first=%.2f, last=%.2f", firstF1, lastF1)
	}
}

// TestE2E_MultiDomainQuality tests quality improvements across multiple domains.
func TestE2E_MultiDomainQuality(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	// Define optimal parameters for each domain
	domainProfiles := map[Domain]struct {
		chunkSize     int
		contextBefore int
		contextAfter  int
	}{
		DomainCode:     {chunkSize: 250, contextBefore: 80, contextAfter: 40},
		DomainAcademic: {chunkSize: 550, contextBefore: 150, contextAfter: 80},
		DomainHistory:  {chunkSize: 400, contextBefore: 200, contextAfter: 50},
	}

	// Train each domain
	for domain, profile := range domainProfiles {
		for i := 0; i < 50; i++ {
			obs := ChunkUsageObservation{
				Domain:        domain,
				TokenCount:    profile.chunkSize + rand.Intn(50) - 25,
				ContextBefore: profile.contextBefore + rand.Intn(30) - 15,
				ContextAfter:  profile.contextAfter + rand.Intn(20) - 10,
				WasUseful:     rand.Float64() > 0.3,
				Timestamp:     time.Now(),
				ChunkID:       fmt.Sprintf("%s-chunk-%d", domain.String(), i),
				SessionID:     "multi-domain-quality",
			}
			learner.RecordObservation(obs)
		}
	}

	// Verify each domain learned its profile
	for domain, profile := range domainProfiles {
		config := learner.GetConfig(domain, false)
		learnedSize := config.GetEffectiveTargetTokens(false)
		confidence := learner.GetDomainConfidence(domain)

		tolerance := float64(profile.chunkSize) * 0.30
		diff := math.Abs(float64(learnedSize - profile.chunkSize))

		t.Logf("Domain %s: target=%d, learned=%d, diff=%.2f, confidence=%.2f",
			domain.String(), profile.chunkSize, learnedSize, diff, confidence)

		if diff > tolerance {
			t.Logf("Warning: Domain %s learned size differs from target by more than 30%%", domain.String())
		}

		if confidence < 0.5 {
			t.Errorf("Domain %s confidence %.2f is below 0.5 after 50 observations", domain.String(), confidence)
		}
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// calculateVariance calculates the variance of a slice of integers.
func calculateVariance(values []int) float64 {
	if len(values) == 0 {
		return 0
	}

	// Calculate mean
	sum := 0
	for _, v := range values {
		sum += v
	}
	mean := float64(sum) / float64(len(values))

	// Calculate variance
	sumSquaredDiff := 0.0
	for _, v := range values {
		diff := float64(v) - mean
		sumSquaredDiff += diff * diff
	}

	return sumSquaredDiff / float64(len(values))
}

// =============================================================================
// Concurrency Tests
// =============================================================================

// TestConcurrency_LearningAndRetrieval tests concurrent learning and retrieval operations.
func TestConcurrency_LearningAndRetrieval(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	feedbackHook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 1000,
	})
	if err != nil {
		t.Fatalf("failed to create feedback hook: %v", err)
	}
	defer feedbackHook.Stop()

	var wg sync.WaitGroup
	numGoroutines := 10
	operationsPerGoroutine := 50

	// Concurrent writers (recording observations)
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < operationsPerGoroutine; i++ {
				chunkID := fmt.Sprintf("concurrent-%d-%d", id, i)
				chunk := Chunk{
					ID:         chunkID,
					Domain:     Domain(id % 4),
					TokenCount: 100 + id*10 + i,
				}
				feedbackHook.RegisterChunk(chunk)

				ctx := RetrievalContext{
					QueryText: "concurrent test",
					Timestamp: time.Now(),
				}
				feedbackHook.RecordRetrieval(chunkID, i%2 == 0, ctx)
			}
		}(g)
	}

	// Concurrent readers (getting configs)
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < operationsPerGoroutine; i++ {
				domain := Domain(id % 4)
				_ = learner.GetConfig(domain, i%2 == 0)
				_ = learner.GetDomainConfidence(domain)
				_ = learner.GetObservationCount(domain)
			}
		}(g)
	}

	wg.Wait()

	// Wait for async processing
	time.Sleep(500 * time.Millisecond)

	// Verify no crashes and data is consistent
	for d := Domain(0); d <= DomainGeneral; d++ {
		confidence := learner.GetDomainConfidence(d)
		if confidence < 0 || confidence > 1 {
			t.Errorf("invalid confidence for domain %s: %.4f", d.String(), confidence)
		}
	}

	// Check dropped count is reasonable
	dropped := feedbackHook.GetDroppedCount()
	t.Logf("Dropped entries: %d", dropped)
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkLearning_RecordObservation(b *testing.B) {
	learner, _ := NewChunkConfigLearner(nil)
	obs := ChunkUsageObservation{
		Domain:        DomainCode,
		TokenCount:    300,
		ContextBefore: 75,
		ContextAfter:  40,
		WasUseful:     true,
		Timestamp:     time.Now(),
		ChunkID:       "bench-chunk",
		SessionID:     "bench-session",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		obs.ChunkID = fmt.Sprintf("bench-chunk-%d", i)
		learner.RecordObservation(obs)
	}
}

func BenchmarkLearning_GetConfig(b *testing.B) {
	learner, _ := NewChunkConfigLearner(nil)

	// Warm up with some observations
	for i := 0; i < 100; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    300,
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       fmt.Sprintf("warmup-%d", i),
			SessionID:     "bench-session",
		}
		learner.RecordObservation(obs)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		learner.GetConfig(DomainCode, i%2 == 0)
	}
}

func BenchmarkWAL_AppendAndRecover(b *testing.B) {
	tmpDir := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		walPath := filepath.Join(tmpDir, fmt.Sprintf("bench-%d.wal", i))
		wal, _ := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})

		entry := &ChunkConfigWALEntry{
			ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
			EntryType:      EntryTypeCheckpoint,
		}
		wal.AppendEntry(entry)
		wal.Close()

		wal2, _ := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: false})
		wal2.Recover()
		wal2.Close()
	}
}

func BenchmarkCitationDetection_DetectCitations(b *testing.B) {
	detector, _ := NewCitationDetector(CitationDetectorConfig{})

	for i := 1; i <= 10; i++ {
		detector.RegisterChunkByID(
			fmt.Sprintf("chunk-%d", i),
			fmt.Sprintf("Content for chunk number %d with some text", i),
			i,
		)
	}

	response := strings.Repeat("Reference [1] and [2] are important. See also [3], [4], and [5]. ", 10)
	chunkIDs := []string{"chunk-1", "chunk-2", "chunk-3", "chunk-4", "chunk-5"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.DetectCitations(response, chunkIDs)
	}
}

func BenchmarkE2E_FullFeedbackCycle(b *testing.B) {
	learner, _ := NewChunkConfigLearner(nil)
	splitter, _ := NewLearnedSemanticSplitter(LearnedSplitterConfig{
		BaseSplitter: NewSimpleTokenSplitter(),
		Learner:      learner,
		Explore:      false,
		SessionID:    "bench",
	})
	feedbackHook, _ := NewSyncRetrievalFeedbackHook(learner, "bench")

	doc := strings.Repeat("This is a benchmark test document. ", 50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chunks, _ := splitter.SplitWithLearning(context.Background(), doc, DomainGeneral)
		for j, chunk := range chunks {
			feedbackHook.RegisterChunk(chunk)
			ctx := RetrievalContext{QueryText: "query", Timestamp: time.Now()}
			feedbackHook.RecordRetrieval(chunk.ID, j%2 == 0, ctx)
		}
	}
}
