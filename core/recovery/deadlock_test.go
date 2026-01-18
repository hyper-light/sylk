package recovery

import (
	"sync"
	"testing"
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
