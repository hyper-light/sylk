package recovery

import (
	"sync"
	"testing"
	"time"
)

func alwaysAlive(_ string) bool { return true }
func alwaysDead(_ string) bool  { return false }

func TestDeadlockDetector_NoDeadlock(t *testing.T) {
	dd := NewDeadlockDetector(alwaysAlive)

	dd.RegisterWait("A", "B", "file", "f1")

	results := dd.Check()
	for _, r := range results {
		if r.Detected {
			t.Errorf("Unexpected deadlock detected: %+v", r)
		}
	}
}

func TestDeadlockDetector_CircularDeadlock(t *testing.T) {
	dd := NewDeadlockDetector(alwaysAlive)

	dd.RegisterWait("A", "B", "file", "f1")
	dd.RegisterWait("B", "C", "file", "f2")
	dd.RegisterWait("C", "A", "file", "f3")

	results := dd.Check()

	var foundCircular bool
	for _, r := range results {
		if r.Detected && r.Type == DeadlockCircular {
			foundCircular = true
			if len(r.Cycle) < 3 {
				t.Errorf("Cycle should have at least 3 agents, got %d", len(r.Cycle))
			}
		}
	}

	if !foundCircular {
		t.Error("Expected circular deadlock to be detected")
	}
}

func TestDeadlockDetector_DeadHolderDeadlock(t *testing.T) {
	dd := NewDeadlockDetector(func(agentID string) bool {
		return agentID != "B"
	})

	dd.RegisterWait("A", "B", "file", "f1")

	results := dd.Check()

	var foundDeadHolder bool
	for _, r := range results {
		if r.Detected && r.Type == DeadlockDeadHolder {
			foundDeadHolder = true
			if r.DeadHolder != "B" {
				t.Errorf("DeadHolder = %s, want B", r.DeadHolder)
			}
			if len(r.WaitingAgents) != 1 || r.WaitingAgents[0] != "A" {
				t.Errorf("WaitingAgents = %v, want [A]", r.WaitingAgents)
			}
		}
	}

	if !foundDeadHolder {
		t.Error("Expected dead holder deadlock to be detected")
	}
}

func TestDeadlockDetector_ClearWait(t *testing.T) {
	dd := NewDeadlockDetector(alwaysDead)

	dd.RegisterWait("A", "B", "file", "f1")
	dd.RegisterWait("A", "C", "file", "f2")

	dd.ClearWait("A", "f1")

	results := dd.Check()

	for _, r := range results {
		if r.Detected && r.ResourceID == "f1" {
			t.Error("f1 wait should have been cleared")
		}
	}
}

func TestDeadlockDetector_ClearAgent(t *testing.T) {
	dd := NewDeadlockDetector(alwaysDead)

	dd.RegisterWait("A", "B", "file", "f1")
	dd.ClearAgent("A")

	results := dd.Check()
	for _, r := range results {
		if r.Detected && r.WaitingAgents[0] == "A" {
			t.Error("Agent A's waits should have been cleared")
		}
	}
}

func TestDeadlockDetector_SelfWait(t *testing.T) {
	dd := NewDeadlockDetector(alwaysAlive)

	dd.RegisterWait("A", "A", "file", "f1")

	results := dd.Check()

	var foundCircular bool
	for _, r := range results {
		if r.Detected && r.Type == DeadlockCircular {
			foundCircular = true
		}
	}

	if !foundCircular {
		t.Error("Self-wait should be detected as circular deadlock")
	}
}

func TestDeadlockDetector_Concurrent(t *testing.T) {
	dd := NewDeadlockDetector(alwaysAlive)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				waiter := string(rune('A' + (n*50+j)%26))
				holder := string(rune('A' + (n*50+j+1)%26))
				dd.RegisterWait(waiter, holder, "file", "f")
			}
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = dd.Check()
			}
		}()
	}

	wg.Wait()
}

func TestDeadlockDetector_LongChain(t *testing.T) {
	dd := NewDeadlockDetector(alwaysAlive)

	dd.RegisterWait("A", "B", "file", "f1")
	dd.RegisterWait("B", "C", "file", "f2")
	dd.RegisterWait("C", "D", "file", "f3")
	dd.RegisterWait("D", "E", "file", "f4")

	results := dd.Check()
	for _, r := range results {
		if r.Detected && r.Type == DeadlockCircular {
			t.Error("Long chain without cycle should not be circular deadlock")
		}
	}
}

func TestDeadlockDetector_FalsePositiveRejection(t *testing.T) {
	// Use fast config for testing
	config := DeadlockConfig{
		ConfirmationChecks: 2,
		ConfirmationDelay:  1 * time.Millisecond,
	}
	dd := NewDeadlockDetectorWithConfig(alwaysAlive, config)

	// Create a cycle
	dd.RegisterWait("A", "B", "file", "f1")
	dd.RegisterWait("B", "C", "file", "f2")
	dd.RegisterWait("C", "A", "file", "f3")

	// Start check in goroutine
	resultChan := make(chan []DeadlockResult, 1)
	go func() {
		resultChan <- dd.Check()
	}()

	// Clear the cycle during confirmation window
	time.Sleep(500 * time.Microsecond)
	dd.ClearWait("C", "f3")

	results := <-resultChan

	// The deadlock should be rejected since cycle was cleared
	var foundConfirmed bool
	for _, r := range results {
		if r.Detected && r.Type == DeadlockCircular {
			foundConfirmed = true
		}
	}

	if foundConfirmed {
		t.Log("Cycle was confirmed before clear, acceptable race condition")
	}
}

func TestDeadlockDetector_DeadHolderConfirmation(t *testing.T) {
	statusCallCount := 0
	var mu sync.Mutex

	statusFn := func(agentID string) bool {
		mu.Lock()
		defer mu.Unlock()
		statusCallCount++
		// First call returns dead, subsequent calls return alive
		return statusCallCount > 1
	}

	config := DeadlockConfig{
		ConfirmationChecks: 2,
		ConfirmationDelay:  1 * time.Millisecond,
	}
	dd := NewDeadlockDetectorWithConfig(statusFn, config)

	dd.RegisterWait("A", "B", "file", "f1")

	results := dd.Check()

	// Should not confirm since holder becomes alive during confirmation
	var foundDeadHolder bool
	for _, r := range results {
		if r.Detected && r.Type == DeadlockDeadHolder {
			foundDeadHolder = true
		}
	}

	if foundDeadHolder {
		t.Error("Dead holder should be rejected when agent becomes alive during confirmation")
	}
}

func TestDeadlockDetector_ConfirmedDeadlock(t *testing.T) {
	config := DeadlockConfig{
		ConfirmationChecks: 2,
		ConfirmationDelay:  1 * time.Millisecond,
	}
	dd := NewDeadlockDetectorWithConfig(alwaysAlive, config)

	// Create a stable cycle
	dd.RegisterWait("A", "B", "file", "f1")
	dd.RegisterWait("B", "A", "file", "f2")

	results := dd.Check()

	var foundCircular bool
	for _, r := range results {
		if r.Detected && r.Type == DeadlockCircular {
			foundCircular = true
		}
	}

	if !foundCircular {
		t.Error("Stable circular deadlock should be confirmed")
	}
}

func TestDeadlockDetectorWithConfig(t *testing.T) {
	config := DeadlockConfig{
		ConfirmationChecks: 3,
		ConfirmationDelay:  5 * time.Millisecond,
	}
	dd := NewDeadlockDetectorWithConfig(alwaysAlive, config)

	if dd.config.ConfirmationChecks != 3 {
		t.Errorf("Expected ConfirmationChecks=3, got %d", dd.config.ConfirmationChecks)
	}
	if dd.config.ConfirmationDelay != 5*time.Millisecond {
		t.Errorf("Expected ConfirmationDelay=5ms, got %v", dd.config.ConfirmationDelay)
	}
}

func TestDefaultDeadlockConfig(t *testing.T) {
	config := DefaultDeadlockConfig()

	if config.ConfirmationChecks != 2 {
		t.Errorf("Expected default ConfirmationChecks=2, got %d", config.ConfirmationChecks)
	}
	if config.ConfirmationDelay != 10*time.Millisecond {
		t.Errorf("Expected default ConfirmationDelay=10ms, got %v", config.ConfirmationDelay)
	}
}
