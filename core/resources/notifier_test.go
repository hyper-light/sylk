package resources

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"
)

func TestNoOpNotifier_ImplementsInterface(t *testing.T) {
	var n ResourceNotifier = &NoOpNotifier{}
	n.AgentWaiting("session", "agent", "file")
	n.AgentProceeding("session", "agent", "file")
}

func TestLoggingNotifier_LogsWaiting(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	n := NewLoggingNotifier(logger)

	n.AgentWaiting("sess-1", "agent-1", "file_handle")

	output := buf.String()
	if !containsAll(output, "waiting", "sess-1", "agent-1", "file_handle") {
		t.Errorf("expected log to contain waiting info, got: %s", output)
	}
}

func TestLoggingNotifier_LogsProceeding(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	n := NewLoggingNotifier(logger)

	n.AgentProceeding("sess-1", "agent-1", "file_handle")

	output := buf.String()
	if !containsAll(output, "proceeding", "sess-1", "agent-1", "file_handle") {
		t.Errorf("expected log to contain proceeding info, got: %s", output)
	}
}

func TestLoggingNotifier_NilLogger(t *testing.T) {
	n := NewLoggingNotifier(nil)
	n.AgentWaiting("s", "a", "r")
	n.AgentProceeding("s", "a", "r")
}

func TestCompositeNotifier_FansOut(t *testing.T) {
	var waitingCalls, proceedingCalls int
	var mu sync.Mutex

	mock1 := &mockNotifier{
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
	mock2 := &mockNotifier{
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

	c := NewCompositeNotifier(mock1, mock2)
	c.AgentWaiting("s", "a", "r")
	c.AgentProceeding("s", "a", "r")

	if waitingCalls != 2 {
		t.Errorf("expected 2 waiting calls, got %d", waitingCalls)
	}
	if proceedingCalls != 2 {
		t.Errorf("expected 2 proceeding calls, got %d", proceedingCalls)
	}
}

func TestCompositeNotifier_Add(t *testing.T) {
	var calls int
	mock := &mockNotifier{
		waiting: func(s, a, r string) { calls++ },
	}

	c := NewCompositeNotifier()
	c.Add(mock)
	c.AgentWaiting("s", "a", "r")

	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestCompositeNotifier_NilNotifiers(t *testing.T) {
	c := NewCompositeNotifier(nil, &NoOpNotifier{}, nil)
	c.Add(nil)
	c.AgentWaiting("s", "a", "r")
	c.AgentProceeding("s", "a", "r")
}

func TestCompositeNotifier_ConcurrentAccess(t *testing.T) {
	c := NewCompositeNotifier(&NoOpNotifier{})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			c.AgentWaiting("s", "a", "r")
		}()
		go func() {
			defer wg.Done()
			c.AgentProceeding("s", "a", "r")
		}()
		go func() {
			defer wg.Done()
			c.Add(&NoOpNotifier{})
		}()
	}
	wg.Wait()
}

type mockNotifier struct {
	waiting    func(sessionID, agentID, resource string)
	proceeding func(sessionID, agentID, resource string)
}

func (m *mockNotifier) AgentWaiting(sessionID, agentID, resource string) {
	if m.waiting != nil {
		m.waiting(sessionID, agentID, resource)
	}
}

func (m *mockNotifier) AgentProceeding(sessionID, agentID, resource string) {
	if m.proceeding != nil {
		m.proceeding(sessionID, agentID, resource)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
