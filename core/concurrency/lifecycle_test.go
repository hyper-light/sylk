package concurrency

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLifecycleState_String(t *testing.T) {
	tests := []struct {
		state    LifecycleState
		expected string
	}{
		{StateCreated, "created"},
		{StateStarting, "starting"},
		{StateRunning, "running"},
		{StateStopping, "stopping"},
		{StateStopped, "stopped"},
		{StateFailed, "failed"},
		{LifecycleState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestNewLifecycle(t *testing.T) {
	lc := NewLifecycle("test-id")

	if lc.ID() != "test-id" {
		t.Errorf("got ID %q, want %q", lc.ID(), "test-id")
	}

	if lc.State() != StateCreated {
		t.Errorf("got state %v, want %v", lc.State(), StateCreated)
	}
}

func TestLifecycle_ValidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from LifecycleState
		to   LifecycleState
	}{
		{"created->starting", StateCreated, StateStarting},
		{"created->failed", StateCreated, StateFailed},
		{"starting->running", StateStarting, StateRunning},
		{"starting->failed", StateStarting, StateFailed},
		{"starting->stopping", StateStarting, StateStopping},
		{"running->stopping", StateRunning, StateStopping},
		{"running->failed", StateRunning, StateFailed},
		{"stopping->stopped", StateStopping, StateStopped},
		{"stopping->failed", StateStopping, StateFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lc := NewLifecycle("test")
			lc.state.Store(int32(tt.from))

			err := lc.TransitionTo(tt.to)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if lc.State() != tt.to {
				t.Errorf("got state %v, want %v", lc.State(), tt.to)
			}
		})
	}
}

func TestLifecycle_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from LifecycleState
		to   LifecycleState
	}{
		{"created->running", StateCreated, StateRunning},
		{"created->stopping", StateCreated, StateStopping},
		{"created->stopped", StateCreated, StateStopped},
		{"running->created", StateRunning, StateCreated},
		{"running->starting", StateRunning, StateStarting},
		{"stopped->anything", StateStopped, StateRunning},
		{"failed->anything", StateFailed, StateRunning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lc := NewLifecycle("test")
			lc.state.Store(int32(tt.from))

			err := lc.TransitionTo(tt.to)
			if err != ErrInvalidStateTransition {
				t.Errorf("got error %v, want %v", err, ErrInvalidStateTransition)
			}

			if lc.State() != tt.from {
				t.Errorf("state changed unexpectedly from %v to %v", tt.from, lc.State())
			}
		})
	}
}

func TestLifecycle_TransitionToFailed(t *testing.T) {
	lc := NewLifecycle("test")
	_ = lc.TransitionTo(StateStarting)
	_ = lc.TransitionTo(StateRunning)

	testErr := ErrWaitTimeout
	err := lc.TransitionToFailed(testErr)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if lc.State() != StateFailed {
		t.Errorf("got state %v, want %v", lc.State(), StateFailed)
	}
}

func TestLifecycle_OnStateChange(t *testing.T) {
	lc := NewLifecycle("test")
	var events []StateChangeEvent
	var mu sync.Mutex

	lc.OnStateChange(func(e StateChangeEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	_ = lc.TransitionTo(StateStarting)
	_ = lc.TransitionTo(StateRunning)

	mu.Lock()
	defer mu.Unlock()

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	verifyEvent(t, events[0], StateCreated, StateStarting)
	verifyEvent(t, events[1], StateStarting, StateRunning)
}

func verifyEvent(t *testing.T, e StateChangeEvent, from, to LifecycleState) {
	t.Helper()
	if e.FromState != from || e.ToState != to {
		t.Errorf("event: got %v->%v, want %v->%v", e.FromState, e.ToState, from, to)
	}
}

func TestLifecycle_WaitForState_AlreadyInState(t *testing.T) {
	lc := NewLifecycle("test")

	err := lc.WaitForState(StateCreated, 100*time.Millisecond)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLifecycle_WaitForState_TransitionOccurs(t *testing.T) {
	lc := NewLifecycle("test")

	done := make(chan error, 1)
	go func() {
		done <- lc.WaitForState(StateRunning, time.Second)
	}()

	time.Sleep(10 * time.Millisecond)
	_ = lc.TransitionTo(StateStarting)
	_ = lc.TransitionTo(StateRunning)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("WaitForState did not return")
	}
}

func TestLifecycle_WaitForState_Timeout(t *testing.T) {
	lc := NewLifecycle("test")

	err := lc.WaitForState(StateRunning, 50*time.Millisecond)
	if err != ErrWaitTimeout {
		t.Errorf("got error %v, want %v", err, ErrWaitTimeout)
	}
}

func TestLifecycle_IsTerminal(t *testing.T) {
	tests := []struct {
		state    LifecycleState
		terminal bool
	}{
		{StateCreated, false},
		{StateStarting, false},
		{StateRunning, false},
		{StateStopping, false},
		{StateStopped, true},
		{StateFailed, true},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			lc := NewLifecycle("test")
			lc.state.Store(int32(tt.state))

			if got := lc.IsTerminal(); got != tt.terminal {
				t.Errorf("got %v, want %v", got, tt.terminal)
			}
		})
	}
}

func TestLifecycle_ConcurrentTransitions(t *testing.T) {
	lc := NewLifecycle("test")
	_ = lc.TransitionTo(StateStarting)
	_ = lc.TransitionTo(StateRunning)

	successCount := runConcurrentTransitions(lc, 10)

	if successCount != 1 {
		t.Errorf("got %d successful transitions, want 1", successCount)
	}

	if lc.State() != StateStopping {
		t.Errorf("got state %v, want %v", lc.State(), StateStopping)
	}
}

func runConcurrentTransitions(lc *Lifecycle, n int) int32 {
	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if lc.TransitionTo(StateStopping) == nil {
				successCount.Add(1)
			}
		}()
	}
	wg.Wait()
	return successCount.Load()
}

func TestLifecycle_MultipleWaiters(t *testing.T) {
	lc := NewLifecycle("test")

	var wg sync.WaitGroup
	results := make(chan error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- lc.WaitForState(StateRunning, time.Second)
		}()
	}

	time.Sleep(10 * time.Millisecond)
	_ = lc.TransitionTo(StateStarting)
	_ = lc.TransitionTo(StateRunning)

	wg.Wait()
	close(results)

	for err := range results {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

type mockPublisher struct {
	messages []interface{}
	mu       sync.Mutex
}

func (m *mockPublisher) Broadcast(msg interface{}) error {
	m.mu.Lock()
	m.messages = append(m.messages, msg)
	m.mu.Unlock()
	return nil
}

func TestLifecycle_ConnectToSignalBus(t *testing.T) {
	lc := NewLifecycle("test")
	pub := &mockPublisher{}

	lc.ConnectToSignalBus(pub, func(e StateChangeEvent) interface{} {
		return e
	})

	_ = lc.TransitionTo(StateStarting)
	_ = lc.TransitionTo(StateRunning)

	pub.mu.Lock()
	defer pub.mu.Unlock()

	if len(pub.messages) != 2 {
		t.Errorf("got %d messages, want 2", len(pub.messages))
	}
}
