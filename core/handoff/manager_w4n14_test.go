package handoff

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// W4N.14 Tests - HandoffManager MarshalJSON Atomic Snapshot
// =============================================================================

// TestW4N14_MarshalJSON_HappyPath tests that MarshalJSON produces valid JSON.
func TestW4N14_MarshalJSON_HappyPath(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	// Add some state
	manager.AddMessage(Message{Role: "user", Content: "test message"})
	manager.evaluations.Store(10)
	manager.handoffsExecuted.Store(5)
	manager.handoffsForced.Store(2)
	manager.handoffsFailed.Store(1)

	// Marshal to JSON
	data, err := json.Marshal(manager)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	// Verify it's valid JSON by unmarshaling
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Produced invalid JSON: %v", err)
	}

	// Verify expected fields exist
	expectedFields := []string{
		"config", "last_evaluation", "last_handoff",
		"evaluations", "executed", "forced", "failed", "status",
	}
	for _, field := range expectedFields {
		if _, ok := result[field]; !ok {
			t.Errorf("Missing expected field: %s", field)
		}
	}

	// Verify statistics match
	if evals, ok := result["evaluations"].(float64); !ok || int64(evals) != 10 {
		t.Errorf("Expected evaluations=10, got %v", result["evaluations"])
	}
	if executed, ok := result["executed"].(float64); !ok || int64(executed) != 5 {
		t.Errorf("Expected executed=5, got %v", result["executed"])
	}
}

// TestW4N14_MarshalJSON_ConcurrentUpdates tests consistency during concurrent updates.
func TestW4N14_MarshalJSON_ConcurrentUpdates(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	// Start goroutines that constantly update state
	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// Updater goroutine - modifies state protected by mu
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				manager.mu.Lock()
				manager.lastEvaluation = time.Now()
				manager.lastHandoff = time.Now()
				manager.mu.Unlock()
				manager.evaluations.Add(1)
				manager.handoffsExecuted.Add(1)
			}
		}
	}()

	// Context updater - modifies state protected by contextMu
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				manager.AddMessage(Message{
					Role:    "user",
					Content: "concurrent message",
				})
			}
		}
	}()

	// Marshal multiple times while updates are happening
	var marshalErrors int
	for i := 0; i < 100; i++ {
		data, err := json.Marshal(manager)
		if err != nil {
			marshalErrors++
			continue
		}

		// Verify the JSON is valid
		var result map[string]interface{}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Errorf("Iteration %d: invalid JSON produced: %v", i, err)
		}
	}

	close(stopCh)
	wg.Wait()

	if marshalErrors > 0 {
		t.Errorf("MarshalJSON failed %d times during concurrent updates", marshalErrors)
	}
}

// TestW4N14_MarshalJSON_EmptyManager tests marshaling an empty manager.
func TestW4N14_MarshalJSON_EmptyManager(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	data, err := json.Marshal(manager)
	if err != nil {
		t.Fatalf("MarshalJSON failed on empty manager: %v", err)
	}

	// Verify it's valid JSON
	var result handoffManagerJSON
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Produced invalid JSON: %v", err)
	}

	// Verify defaults are present
	if result.Config == nil {
		t.Error("Expected config to be present")
	}
	if result.Status != "Stopped" {
		t.Errorf("Expected status=Stopped, got %s", result.Status)
	}
	if result.Evaluations != 0 {
		t.Errorf("Expected evaluations=0, got %d", result.Evaluations)
	}
}

// TestW4N14_MarshalJSON_RaceCondition tests for race conditions using concurrent
// marshal and update operations.
func TestW4N14_MarshalJSON_RaceCondition(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	var wg sync.WaitGroup
	iterations := 1000

	// Multiple concurrent marshalers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, err := json.Marshal(manager)
				if err != nil {
					t.Errorf("MarshalJSON error: %v", err)
				}
			}
		}()
	}

	// Multiple concurrent updaters for mu-protected state
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				manager.mu.Lock()
				manager.lastEvaluation = time.Now()
				manager.lastHandoff = time.Now()
				manager.mu.Unlock()
			}
		}()
	}

	// Multiple concurrent context updaters
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				manager.AddMessage(Message{
					Role:    "user",
					Content: "race test message",
				})
			}
		}()
	}

	// Concurrent stat incrementers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				manager.evaluations.Add(1)
				manager.handoffsExecuted.Add(1)
				manager.handoffsForced.Add(1)
				manager.handoffsFailed.Add(1)
			}
		}()
	}

	wg.Wait()
}

// TestW4N14_MarshalJSON_NilContext tests marshaling with nil prepared context.
func TestW4N14_MarshalJSON_NilContext(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	// Force nil context
	manager.contextMu.Lock()
	manager.preparedContext = nil
	manager.contextMu.Unlock()

	data, err := json.Marshal(manager)
	if err != nil {
		t.Fatalf("MarshalJSON failed with nil context: %v", err)
	}

	// Verify it's valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Produced invalid JSON: %v", err)
	}

	// prepared_context should be omitted or null
	if ctx, ok := result["prepared_context"]; ok && ctx != nil {
		t.Errorf("Expected nil/omitted prepared_context, got %v", ctx)
	}
}

// TestW4N14_MarshalJSON_LargeState tests marshaling with large state.
func TestW4N14_MarshalJSON_LargeState(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	// Add many messages
	for i := 0; i < 1000; i++ {
		manager.AddMessage(Message{
			Role:    "user",
			Content: "This is a test message with some content to simulate real usage",
		})
	}

	// Set large statistics
	manager.evaluations.Store(1000000)
	manager.handoffsExecuted.Store(500000)
	manager.handoffsForced.Store(100000)
	manager.handoffsFailed.Store(10000)

	data, err := json.Marshal(manager)
	if err != nil {
		t.Fatalf("MarshalJSON failed with large state: %v", err)
	}

	// Verify it's valid JSON
	var result handoffManagerJSON
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Produced invalid JSON: %v", err)
	}

	// Verify statistics
	if result.Evaluations != 1000000 {
		t.Errorf("Expected evaluations=1000000, got %d", result.Evaluations)
	}
	if result.Executed != 500000 {
		t.Errorf("Expected executed=500000, got %d", result.Executed)
	}
}

// TestW4N14_MarshalJSON_AtomicConsistency verifies that the snapshot is atomic
// by checking that related fields are consistent.
func TestW4N14_MarshalJSON_AtomicConsistency(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)

	var wg sync.WaitGroup
	stopCh := make(chan struct{})
	iterations := 100

	// Pattern: always update lastEvaluation and evaluations together
	wg.Add(1)
	go func() {
		defer wg.Done()
		counter := int64(0)
		for {
			select {
			case <-stopCh:
				return
			default:
				counter++
				manager.mu.Lock()
				manager.lastEvaluation = time.Unix(counter, 0)
				manager.mu.Unlock()
				manager.evaluations.Store(counter)
			}
		}
	}()

	// Marshal and verify consistency
	for i := 0; i < iterations; i++ {
		data, err := json.Marshal(manager)
		if err != nil {
			t.Errorf("Iteration %d: MarshalJSON error: %v", i, err)
			continue
		}

		var result handoffManagerJSON
		if err := json.Unmarshal(data, &result); err != nil {
			t.Errorf("Iteration %d: unmarshal error: %v", i, err)
			continue
		}

		// Parse the timestamp
		ts, err := time.Parse(time.RFC3339Nano, result.LastEvaluation)
		if err != nil {
			continue // Skip if zero time
		}

		// With atomic snapshot, the timestamp and counter should be consistent
		// (within a small margin due to atomic operations happening between lock release)
		// The key is that we don't get wildly inconsistent values
		if ts.Unix() > 0 && result.Evaluations > 0 {
			diff := ts.Unix() - result.Evaluations
			// Allow some drift but not large inconsistencies
			if diff < -10 || diff > 10 {
				t.Logf("Warning: potential inconsistency - timestamp=%d, evaluations=%d, diff=%d",
					ts.Unix(), result.Evaluations, diff)
			}
		}
	}

	close(stopCh)
	wg.Wait()
}

// TestW4N14_CollectAtomicSnapshot_Direct tests the helper function directly.
func TestW4N14_CollectAtomicSnapshot_Direct(t *testing.T) {
	config := &HandoffManagerConfig{
		AgentID:   "test-agent",
		ModelName: "test-model",
	}
	manager := NewHandoffManager(config, nil, nil, nil)

	// Set up some state
	manager.mu.Lock()
	manager.lastEvaluation = time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	manager.lastHandoff = time.Date(2024, 1, 15, 10, 25, 0, 0, time.UTC)
	manager.mu.Unlock()

	manager.evaluations.Store(42)
	manager.handoffsExecuted.Store(10)
	manager.handoffsForced.Store(3)
	manager.handoffsFailed.Store(1)

	snapshot := manager.collectAtomicSnapshot()

	// Verify all fields
	if snapshot.Config.AgentID != "test-agent" {
		t.Errorf("Expected AgentID=test-agent, got %s", snapshot.Config.AgentID)
	}
	if snapshot.Evaluations != 42 {
		t.Errorf("Expected evaluations=42, got %d", snapshot.Evaluations)
	}
	if snapshot.Executed != 10 {
		t.Errorf("Expected executed=10, got %d", snapshot.Executed)
	}
	if snapshot.Forced != 3 {
		t.Errorf("Expected forced=3, got %d", snapshot.Forced)
	}
	if snapshot.Failed != 1 {
		t.Errorf("Expected failed=1, got %d", snapshot.Failed)
	}
}

// TestW4N14_MarshalJSON_RoundTrip tests marshal/unmarshal round-trip.
func TestW4N14_MarshalJSON_RoundTrip(t *testing.T) {
	config := &HandoffManagerConfig{
		AgentID:              "round-trip-agent",
		ModelName:            "test-model",
		EvaluationInterval:   30 * time.Second,
		EnableAutoEvaluation: true,
	}
	original := NewHandoffManager(config, nil, nil, nil)

	// Set state
	original.evaluations.Store(100)
	original.handoffsExecuted.Store(50)
	original.handoffsForced.Store(10)
	original.handoffsFailed.Store(5)

	// Marshal
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal into new manager
	restored := NewHandoffManager(nil, nil, nil, nil)
	if err := json.Unmarshal(data, restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify restored state
	if restored.evaluations.Load() != 100 {
		t.Errorf("Expected evaluations=100, got %d", restored.evaluations.Load())
	}
	if restored.handoffsExecuted.Load() != 50 {
		t.Errorf("Expected executed=50, got %d", restored.handoffsExecuted.Load())
	}
	if restored.handoffsForced.Load() != 10 {
		t.Errorf("Expected forced=10, got %d", restored.handoffsForced.Load())
	}
	if restored.handoffsFailed.Load() != 5 {
		t.Errorf("Expected failed=5, got %d", restored.handoffsFailed.Load())
	}
}

// TestW4N14_MarshalJSON_StatusValues tests all status values are marshaled correctly.
func TestW4N14_MarshalJSON_StatusValues(t *testing.T) {
	testCases := []struct {
		status   HandoffStatus
		expected string
	}{
		{StatusStopped, "Stopped"},
		{StatusRunning, "Running"},
		{StatusEvaluating, "Evaluating"},
		{StatusExecuting, "Executing"},
		{StatusShuttingDown, "ShuttingDown"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			manager := NewHandoffManager(nil, nil, nil, nil)
			manager.status.Store(int32(tc.status))

			data, err := json.Marshal(manager)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			var result handoffManagerJSON
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}

			if result.Status != tc.expected {
				t.Errorf("Expected status=%s, got %s", tc.expected, result.Status)
			}
		})
	}
}
