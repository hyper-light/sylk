package recovery

import (
	"errors"
	"log/slog"
	"os"
	"testing"
)

type mockAcquirer struct {
	acquired  map[string][]string
	failOn    map[string]bool
	acquireOK bool
}

func (m *mockAcquirer) Acquire(agentID, resourceID string) error {
	if m.failOn != nil && m.failOn[resourceID] {
		return errors.New("acquire failed")
	}
	if m.acquired == nil {
		m.acquired = make(map[string][]string)
	}
	m.acquired[agentID] = append(m.acquired[agentID], resourceID)
	return nil
}

func TestResourceReacquisition_Success(t *testing.T) {
	acquirer := &mockAcquirer{}
	notifier := &testNotifier{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	rr := NewResourceReacquisition(acquirer, notifier, logger)

	err := rr.ReacquireResources("agent-1", []string{"r1", "r2", "r3"})

	if err != nil {
		t.Errorf("ReacquireResources failed: %v", err)
	}

	acquired := acquirer.acquired["agent-1"]
	if len(acquired) != 3 {
		t.Errorf("Expected 3 acquisitions, got %d", len(acquired))
	}
}

func TestResourceReacquisition_PartialFailure(t *testing.T) {
	acquirer := &mockAcquirer{
		failOn: map[string]bool{"r2": true},
	}
	notifier := &testNotifier{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	rr := NewResourceReacquisition(acquirer, notifier, logger)

	err := rr.ReacquireResources("agent-1", []string{"r1", "r2", "r3"})

	if err == nil {
		t.Error("Expected error for partial failure")
	}

	acquired := acquirer.acquired["agent-1"]
	if len(acquired) != 2 {
		t.Errorf("Expected 2 successful acquisitions, got %d", len(acquired))
	}
}

func TestResourceReacquisition_AllFail(t *testing.T) {
	acquirer := &mockAcquirer{
		failOn: map[string]bool{"r1": true, "r2": true},
	}
	notifier := &testNotifier{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	rr := NewResourceReacquisition(acquirer, notifier, logger)

	err := rr.ReacquireResources("agent-1", []string{"r1", "r2"})

	if err == nil {
		t.Error("Expected error when all fail")
	}
}

func TestResourceReacquisition_EmptyResources(t *testing.T) {
	acquirer := &mockAcquirer{}
	notifier := &testNotifier{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	rr := NewResourceReacquisition(acquirer, notifier, logger)

	err := rr.ReacquireResources("agent-1", []string{})

	if err != nil {
		t.Errorf("Empty resources should succeed: %v", err)
	}
}

func TestResourceReacquisition_NotifyAndReacquire(t *testing.T) {
	acquirer := &mockAcquirer{}
	notifier := &testNotifier{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	rr := NewResourceReacquisition(acquirer, notifier, logger)

	err := rr.NotifyAndReacquire("agent-1", []string{"r1", "r2"})

	if err != nil {
		t.Errorf("NotifyAndReacquire failed: %v", err)
	}

	notifier.mu.Lock()
	reacquireCount := len(notifier.reacquireNotifs)
	notifier.mu.Unlock()

	if reacquireCount != 1 {
		t.Errorf("Expected 1 reacquire notification, got %d", reacquireCount)
	}

	if len(acquirer.acquired["agent-1"]) != 2 {
		t.Errorf("Expected 2 acquisitions, got %d", len(acquirer.acquired["agent-1"]))
	}
}

func TestResourceReacquisition_NotifyAndReacquireWithFailure(t *testing.T) {
	acquirer := &mockAcquirer{
		failOn: map[string]bool{"r1": true},
	}
	notifier := &testNotifier{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	rr := NewResourceReacquisition(acquirer, notifier, logger)

	err := rr.NotifyAndReacquire("agent-1", []string{"r1", "r2"})

	if err == nil {
		t.Error("Expected error for partial failure")
	}

	notifier.mu.Lock()
	reacquireCount := len(notifier.reacquireNotifs)
	notifier.mu.Unlock()

	if reacquireCount != 1 {
		t.Error("Should still send notification even if reacquisition fails")
	}
}
