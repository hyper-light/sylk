package container

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	csecurity "github.com/adalundhe/sylk/core/container/security"
)

// mockAgent implements ContainerAgent for testing.
type mockAgent struct {
	id         string
	agentType  string
	terminated bool
}

func (m *mockAgent) AgentID() string                        { return m.id }
func (m *mockAgent) AgentType() string                      { return m.agentType }
func (m *mockAgent) Terminate(_ context.Context) error       { m.terminated = true; return nil }

func newTestContainer(t *testing.T) (*Container, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	scope := concurrency.NewGoroutineScope(ctx, "test-container", nil)
	secCtx := csecurity.NewSecurityContext(csecurity.SecurityContextConfig{
		ContainerID: "test-id",
		AgentID:     "agent-1",
		Role:        "worker",
	})
	agent := &mockAgent{id: "agent-1", agentType: "engineer"}

	c := NewContainer(ContainerConfig{
		ID:   "test-id",
		Spec: validContainerSpec(),
		Scope:  scope,
		SecCtx: secCtx,
		Agent:  agent,
	})
	return c, cancel
}

func TestContainer_InitialState(t *testing.T) {
	c, cancel := newTestContainer(t)
	defer cancel()

	if c.State() != concurrency.StateCreated {
		t.Fatalf("expected Created, got %v", c.State())
	}
	if c.IsRunning() {
		t.Fatal("should not be running")
	}
	if c.IsTerminal() {
		t.Fatal("should not be terminal")
	}
}

func TestContainer_StartTransitionsToRunning(t *testing.T) {
	c, cancel := newTestContainer(t)
	defer cancel()

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if c.State() != concurrency.StateRunning {
		t.Fatalf("expected Running, got %v", c.State())
	}
	if !c.IsRunning() {
		t.Fatal("should be running")
	}
	if c.Metrics().StartTime.IsZero() {
		t.Fatal("start time should be set")
	}
}

func TestContainer_StopTransitionsToStopped(t *testing.T) {
	c, cancel := newTestContainer(t)
	defer cancel()

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if c.State() != concurrency.StateStopped {
		t.Fatalf("expected Stopped, got %v", c.State())
	}
	if !c.IsTerminal() {
		t.Fatal("should be terminal")
	}
	if c.Metrics().StopTime.IsZero() {
		t.Fatal("stop time should be set")
	}
	if !c.Agent().(*mockAgent).terminated {
		t.Fatal("agent should have been terminated")
	}
}

func TestContainer_FailTransitionsToFailed(t *testing.T) {
	c, cancel := newTestContainer(t)
	defer cancel()

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	testErr := context.DeadlineExceeded
	if err := c.Fail(testErr); err != nil {
		t.Fatalf("Fail failed: %v", err)
	}

	if c.State() != concurrency.StateFailed {
		t.Fatalf("expected Failed, got %v", c.State())
	}
	if c.ExitError() != testErr {
		t.Fatalf("expected %v, got %v", testErr, c.ExitError())
	}
}

func TestContainer_DoubleStartFails(t *testing.T) {
	c, cancel := newTestContainer(t)
	defer cancel()

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	err := c.Start(context.Background())
	if err == nil {
		t.Fatal("expected error on double start")
	}
}

func TestContainer_StopWithoutStartFails(t *testing.T) {
	c, cancel := newTestContainer(t)
	defer cancel()

	err := c.Stop(context.Background())
	if err == nil {
		t.Fatal("expected error stopping unstarted container")
	}
}

func TestContainer_IsReady_NoProbes(t *testing.T) {
	c, cancel := newTestContainer(t)
	defer cancel()

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// No probes configured = implicitly ready
	if !c.IsReady() {
		t.Fatal("should be ready with no probes configured")
	}
}

func TestContainer_Metrics_Uptime(t *testing.T) {
	c, cancel := newTestContainer(t)
	defer cancel()

	if c.Metrics().Uptime() != 0 {
		t.Fatal("uptime should be 0 before start")
	}

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	uptime := c.Metrics().Uptime()
	if uptime < 10*time.Millisecond {
		t.Fatalf("uptime too low: %v", uptime)
	}
}
