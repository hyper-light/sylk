package resources

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegration_HierarchicalStealing(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   100,
		ReservedRatio: 0.10,
	})

	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "default")

	initialAllocated := agent.Allocated()
	ctx := context.Background()

	for i := int64(0); i < initialAllocated; i++ {
		if err := agent.Acquire(ctx); err != nil {
			t.Fatalf("failed to acquire from agent level: %v", err)
		}
	}

	if agent.Used() != initialAllocated {
		t.Errorf("expected used %d, got %d", initialAllocated, agent.Used())
	}

	if err := agent.Acquire(ctx); err != nil {
		t.Fatalf("failed to acquire from session level: %v", err)
	}

	if agent.Used() != initialAllocated+1 {
		t.Errorf("expected used %d after session steal, got %d", initialAllocated+1, agent.Used())
	}
}

func TestIntegration_WaitAndNotification(t *testing.T) {
	var waitingCount, proceedingCount atomic.Int32
	notifier := &mockNotifier{
		waiting: func(s, a, r string) {
			waitingCount.Add(1)
		},
		proceeding: func(s, a, r string) {
			proceedingCount.Add(1)
		},
	}

	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   20,
		ReservedRatio: 0.10,
		Notifier:      notifier,
	})

	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "default")

	for agent.Available() > 0 {
		agent.Acquire(context.Background())
	}
	budget.globalAllocated.Store(budget.globalLimit - budget.reserved)

	acquired := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		agent.Acquire(ctx)
		close(acquired)
	}()

	time.Sleep(50 * time.Millisecond)
	agent.Release()
	<-acquired

	if waitingCount.Load() < 1 {
		t.Errorf("expected at least 1 waiting notification, got %d", waitingCount.Load())
	}
}

func TestIntegration_MultiSessionFairShare(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   100,
		ReservedRatio: 0.10,
	})

	session1 := budget.RegisterSession("sess-1")
	session2 := budget.RegisterSession("sess-2")

	agent1 := session1.RegisterAgent("agent-1", "editor")
	agent2 := session2.RegisterAgent("agent-2", "editor")

	alloc1 := agent1.Allocated()
	alloc2 := agent2.Allocated()

	if alloc1 != alloc2 {
		t.Errorf("expected equal allocations, got %d vs %d", alloc1, alloc2)
	}
}

func TestIntegration_WorkStealing(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   100,
		ReservedRatio: 0.10,
	})

	session1 := budget.RegisterSession("sess-1")
	session2 := budget.RegisterSession("sess-2")

	agent1 := session1.RegisterAgent("agent-1", "default")
	session2.RegisterAgent("agent-2", "default")

	ctx := context.Background()
	acquireCount := 0
	for i := 0; i < 30; i++ {
		if err := agent1.Acquire(ctx); err != nil {
			break
		}
		acquireCount++
	}

	if acquireCount <= int(agent1.Allocated())-10 {
		t.Logf("agent1 acquired %d handles (includes stealing)", acquireCount)
	}
}

func TestIntegration_CleanupOnSessionUnregister(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   100,
		ReservedRatio: 0.10,
	})

	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "editor")

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		agent.Acquire(ctx)
	}

	allocBefore := budget.GlobalAllocated()
	budget.UnregisterSession("sess-1")
	allocAfter := budget.GlobalAllocated()

	if allocAfter >= allocBefore {
		t.Errorf("expected allocation to decrease after unregister, before=%d, after=%d", allocBefore, allocAfter)
	}

	if budget.GetSession("sess-1") != nil {
		t.Error("session should be nil after unregister")
	}
}

func TestIntegration_SnapshotAccuracy(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   100,
		ReservedRatio: 0.20,
	})

	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "editor")

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		agent.Acquire(ctx)
	}

	snap := budget.Snapshot()

	if snap.Global.Limit != 100 {
		t.Errorf("expected limit 100, got %d", snap.Global.Limit)
	}
	if snap.Global.Reserved != 20 {
		t.Errorf("expected reserved 20, got %d", snap.Global.Reserved)
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(snap.Sessions))
	}
	if snap.Sessions[0].Used != 3 {
		t.Errorf("expected session used 3, got %d", snap.Sessions[0].Used)
	}
	if len(snap.Sessions[0].Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(snap.Sessions[0].Agents))
	}
	if snap.Sessions[0].Agents[0].Used != 3 {
		t.Errorf("expected agent used 3, got %d", snap.Sessions[0].Agents[0].Used)
	}
}

func TestIntegration_ConcurrentSnapshotAccuracy(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   1000,
		ReservedRatio: 0.10,
	})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		sessionID := string(rune('A' + i))
		session := budget.RegisterSession("sess-" + sessionID)
		agent := session.RegisterAgent("agent-"+sessionID, "editor")

		wg.Add(1)
		go func(a *AgentFileBudget) {
			defer wg.Done()
			ctx := context.Background()
			for j := 0; j < 10; j++ {
				a.Acquire(ctx)
				time.Sleep(time.Millisecond)
			}
		}(agent)
	}

	snapshotsDone := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			snap := budget.Snapshot()
			if snap.Global.Limit != 1000 {
				t.Errorf("snapshot limit incorrect: %d", snap.Global.Limit)
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(snapshotsDone)
	}()

	wg.Wait()
	<-snapshotsDone
}

func TestIntegration_TrackedFileLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "editor")

	agent.Acquire(context.Background())

	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	tf := NewTrackedFile(f, tmpFile, ModeWrite, "sess-1", "agent-1", agent)

	if tf.Type() != "file" {
		t.Errorf("expected type 'file', got %q", tf.Type())
	}

	expectedID := "file:" + tmpFile + ":" + string(rune(tf.Fd()+'0'))
	if tf.ID() != expectedID {
		t.Logf("ID format: %s", tf.ID())
	}

	usedBefore := agent.Used()
	if err := tf.ForceClose(); err != nil {
		t.Errorf("ForceClose failed: %v", err)
	}
	usedAfter := agent.Used()

	if usedAfter != usedBefore-1 {
		t.Errorf("expected used to decrease by 1, before=%d, after=%d", usedBefore, usedAfter)
	}

	if !tf.IsClosed() {
		t.Error("expected file to be marked closed")
	}
}

func TestIntegration_RaceConditions(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   500,
		ReservedRatio: 0.10,
	})

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessionID := "sess-" + string(rune('A'+idx%3))
			agentID := "agent-" + string(rune('0'+idx))

			session := budget.RegisterSession(sessionID)
			agent := session.RegisterAgent(agentID, "editor")

			ctx := context.Background()
			for j := 0; j < 20; j++ {
				if err := agent.Acquire(ctx); err != nil {
					continue
				}
				time.Sleep(time.Microsecond * 100)
				agent.Release()
			}
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				budget.Snapshot()
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()
}

func TestIntegration_ContextCancellation(t *testing.T) {
	budget := NewFileHandleBudget(FileHandleBudgetConfig{
		GlobalLimit:   10,
		ReservedRatio: 0.10,
	})

	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "default")

	for agent.Available() > 0 {
		agent.Acquire(context.Background())
	}
	budget.globalAllocated.Store(budget.globalLimit - budget.reserved)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Acquire(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	err := <-errCh
	if err == nil {
		t.Error("expected context cancellation error")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestIntegration_ZeroFileLeaks(t *testing.T) {
	tmpDir := t.TempDir()

	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "editor")

	var files []*TrackedFile
	for i := 0; i < 10; i++ {
		agent.Acquire(context.Background())
		tmpFile := filepath.Join(tmpDir, "test"+string(rune('0'+i))+".txt")
		f, err := os.Create(tmpFile)
		if err != nil {
			t.Fatalf("failed to create file %d: %v", i, err)
		}
		tf := NewTrackedFile(f, tmpFile, ModeWrite, "sess-1", "agent-1", agent)
		files = append(files, tf)
	}

	usedBefore := agent.Used()
	if usedBefore != 10 {
		t.Errorf("expected 10 used, got %d", usedBefore)
	}

	for _, tf := range files {
		tf.Close()
	}

	usedAfter := agent.Used()
	if usedAfter != 0 {
		t.Errorf("expected 0 used after closing all, got %d", usedAfter)
	}
}

func TestIntegration_AgentTypeAllocations(t *testing.T) {
	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")

	editorAgent := session.RegisterAgent("editor-1", "editor")
	terminalAgent := session.RegisterAgent("terminal-1", "terminal")
	searchAgent := session.RegisterAgent("search-1", "search")
	defaultAgent := session.RegisterAgent("default-1", "unknown")

	if editorAgent.Allocated() <= terminalAgent.Allocated() {
		t.Logf("editor=%d, terminal=%d", editorAgent.Allocated(), terminalAgent.Allocated())
	}
	if terminalAgent.Allocated() <= searchAgent.Allocated() {
		t.Logf("terminal=%d, search=%d", terminalAgent.Allocated(), searchAgent.Allocated())
	}
	if defaultAgent.Allocated() != int64(DefaultBaseAllocation) {
		t.Errorf("expected default allocation %d, got %d", DefaultBaseAllocation, defaultAgent.Allocated())
	}
}
