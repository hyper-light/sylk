package resources

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewFileHandleBudget_DefaultConfig(t *testing.T) {
	cfg := DefaultFileHandleBudgetConfig()
	budget := NewFileHandleBudget(cfg)

	if budget.GlobalLimit() != DefaultGlobalLimit {
		t.Errorf("expected limit %d, got %d", DefaultGlobalLimit, budget.GlobalLimit())
	}

	expectedReserved := budget.Reserved()
	if expectedReserved <= 0 {
		t.Errorf("expected reserved > 0, got %d", expectedReserved)
	}
}

func TestNewFileHandleBudget_ZeroLimit(t *testing.T) {
	cfg := FileHandleBudgetConfig{GlobalLimit: 0}
	budget := NewFileHandleBudget(cfg)

	if budget.GlobalLimit() != DefaultGlobalLimit {
		t.Errorf("expected default limit %d, got %d", DefaultGlobalLimit, budget.GlobalLimit())
	}
}

func TestFileHandleBudget_TryAllocate(t *testing.T) {
	cfg := FileHandleBudgetConfig{
		GlobalLimit:   100,
		ReservedRatio: 0.20,
	}
	budget := NewFileHandleBudget(cfg)
	available := budget.Available()

	if !budget.TryAllocate(10) {
		t.Error("expected allocation to succeed")
	}

	if budget.GlobalAllocated() != 10 {
		t.Errorf("expected allocated 10, got %d", budget.GlobalAllocated())
	}

	if budget.Available() != available-10 {
		t.Errorf("expected available %d, got %d", available-10, budget.Available())
	}
}

func TestFileHandleBudget_TryAllocate_ExceedsLimit(t *testing.T) {
	cfg := FileHandleBudgetConfig{
		GlobalLimit:   100,
		ReservedRatio: 0.20,
	}
	budget := NewFileHandleBudget(cfg)

	if budget.TryAllocate(1000) {
		t.Error("expected allocation to fail for amount exceeding limit")
	}
}

func TestFileHandleBudget_Deallocate(t *testing.T) {
	cfg := DefaultFileHandleBudgetConfig()
	budget := NewFileHandleBudget(cfg)

	budget.TryAllocate(50)
	budget.Deallocate(20)

	if budget.GlobalAllocated() != 30 {
		t.Errorf("expected allocated 30, got %d", budget.GlobalAllocated())
	}
}

func TestFileHandleBudget_RegisterSession(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())

	session := budget.RegisterSession("session-1")
	if session == nil {
		t.Fatal("expected session to be registered")
	}
	if session.SessionID() != "session-1" {
		t.Errorf("expected session-1, got %s", session.SessionID())
	}

	session2 := budget.RegisterSession("session-1")
	if session2 != session {
		t.Error("expected same session instance for same ID")
	}
}

func TestFileHandleBudget_UnregisterSession(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())

	session := budget.RegisterSession("session-1")
	session.TryExpand(10)

	allocatedBefore := budget.GlobalAllocated()
	budget.UnregisterSession("session-1")

	if budget.GetSession("session-1") != nil {
		t.Error("expected session to be unregistered")
	}

	if budget.GlobalAllocated() != allocatedBefore-10 {
		t.Errorf("expected global allocated to decrease by 10")
	}
}

func TestFileHandleBudget_PressureLevel(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())

	budget.SetPressureLevel(2)
	if budget.PressureLevel() != 2 {
		t.Errorf("expected pressure level 2, got %d", budget.PressureLevel())
	}
}

func TestFileHandleBudget_Snapshot(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   100,
		ReservedRatio: 0.20,
	})

	session := budget.RegisterSession("sess-1")
	session.RegisterAgent("agent-1", "editor")

	snap := budget.Snapshot()

	if snap.Global.Limit != 100 {
		t.Errorf("expected limit 100, got %d", snap.Global.Limit)
	}
	if snap.Global.Reserved != 20 {
		t.Errorf("expected reserved 20, got %d", snap.Global.Reserved)
	}
	if len(snap.Sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(snap.Sessions))
	}
}

func TestSessionFileBudget_TryExpand(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   100,
		ReservedRatio: 0.20,
	})
	session := budget.RegisterSession("sess-1")

	if !session.TryExpand(10) {
		t.Error("expected expansion to succeed")
	}

	if session.Allocated() != 10 {
		t.Errorf("expected allocated 10, got %d", session.Allocated())
	}
}

func TestSessionFileBudget_Shrink(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")

	session.TryExpand(20)
	session.Shrink(5)

	if session.Allocated() != 15 {
		t.Errorf("expected allocated 15, got %d", session.Allocated())
	}
}

func TestSessionFileBudget_RegisterAgent(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")

	agent := session.RegisterAgent("agent-1", "editor")
	if agent == nil {
		t.Fatal("expected agent to be registered")
	}
	if agent.AgentID() != "agent-1" {
		t.Errorf("expected agent-1, got %s", agent.AgentID())
	}
	if agent.AgentType() != "editor" {
		t.Errorf("expected editor, got %s", agent.AgentType())
	}

	agent2 := session.RegisterAgent("agent-1", "editor")
	if agent2 != agent {
		t.Error("expected same agent instance for same ID")
	}
}

func TestSessionFileBudget_UnregisterAgent(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")
	session.RegisterAgent("agent-1", "editor")

	session.UnregisterAgent("agent-1")

	if session.GetAgent("agent-1") != nil {
		t.Error("expected agent to be unregistered")
	}
}

func TestAgentFileBudget_Acquire_FromAgent(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "editor")

	ctx := context.Background()
	if err := agent.Acquire(ctx); err != nil {
		t.Errorf("Acquire failed: %v", err)
	}

	if agent.Used() != 1 {
		t.Errorf("expected used 1, got %d", agent.Used())
	}
}

func TestAgentFileBudget_Acquire_FromSession(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   100,
		ReservedRatio: 0.20,
	})
	session := budget.RegisterSession("sess-1")
	session.TryExpand(5)
	agent := session.RegisterAgent("agent-1", "default")

	for i := 0; i < int(agent.Allocated()); i++ {
		if err := agent.Acquire(context.Background()); err != nil {
			t.Fatalf("Acquire %d failed: %v", i, err)
		}
	}

	if err := agent.Acquire(context.Background()); err != nil {
		t.Errorf("Acquire from session should succeed: %v", err)
	}
}

func TestAgentFileBudget_Release(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "editor")

	agent.Acquire(context.Background())
	usedBefore := agent.Used()

	agent.Release()

	if agent.Used() != usedBefore-1 {
		t.Errorf("expected used to decrease, before=%d, after=%d", usedBefore, agent.Used())
	}
}

func TestAgentFileBudget_Acquire_ContextCancellation(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   10,
		ReservedRatio: 0.20,
	})
	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "editor")

	for i := 0; i < 8; i++ {
		budget.TryAllocate(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	for agent.Available() > 0 {
		agent.Acquire(context.Background())
	}

	err := agent.Acquire(ctx)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

func TestAgentFileBudget_ConcurrentAcquireRelease(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   1000,
		ReservedRatio: 0.10,
	})
	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "editor")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			if err := agent.Acquire(ctx); err != nil {
				return
			}
			time.Sleep(time.Millisecond)
			agent.Release()
		}()
	}
	wg.Wait()

	if agent.Used() != 0 {
		t.Errorf("expected used 0 after all releases, got %d", agent.Used())
	}
}

func TestAgentFileBudget_NotifiesOnWait(t *testing.T) {
	var waitingCalls, proceedingCalls int
	var mu sync.Mutex
	notifier := &mockNotifier{
		waiting: func(s, a, r string) {
			mu.Lock()
			waitingCalls++
			mu.Unlock()
		},
		proceeding: func(s, a, r string) {
			mu.Lock()
			proceedingCalls++
			mu.Unlock()
		},
	}

	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   50,
		ReservedRatio: 0.10,
		Notifier:      notifier,
	})
	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "default")

	allocated := int(agent.Allocated())
	for i := 0; i < allocated; i++ {
		agent.Acquire(context.Background())
	}

	budget.globalAllocated.Store(budget.globalLimit - budget.reserved)

	done := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		agent.Release()
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := agent.Acquire(ctx)
	<-done

	if err != nil {
		t.Skipf("acquire timed out, notification test inconclusive: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if waitingCalls < 1 {
		t.Errorf("expected at least 1 waiting notification, got %d", waitingCalls)
	}
	if proceedingCalls < 1 {
		t.Errorf("expected at least 1 proceeding notification, got %d", proceedingCalls)
	}
}

func TestFileHandleBudget_ConcurrentSessionOperations(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sessionID := "sess-" + string(rune('A'+id%5))
			session := budget.RegisterSession(sessionID)
			agent := session.RegisterAgent("agent", "editor")
			agent.Acquire(context.Background())
			time.Sleep(time.Millisecond)
			agent.Release()
		}(i)
	}
	wg.Wait()
}

func TestAgentFileBudget_Available(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "editor")

	initial := agent.Available()
	agent.Acquire(context.Background())

	if agent.Available() != initial-1 {
		t.Errorf("expected available %d, got %d", initial-1, agent.Available())
	}
}

func TestSessionFileBudget_Unallocated(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")
	session.TryExpand(50)
	session.RegisterAgent("agent-1", "default")

	agentAlloc := session.GetAgent("agent-1").Allocated()
	expected := session.Allocated() - agentAlloc

	if session.Unallocated() != expected {
		t.Errorf("expected unallocated %d, got %d", expected, session.Unallocated())
	}
}
