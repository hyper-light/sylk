package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/pipeline"
)

// mockArchivableState implements pipeline.ArchivableHandoffState for testing.
type mockArchivableState struct {
	agentID        string
	agentType      string
	sessionID      string
	pipelineID     string
	triggerReason  string
	triggerContext float64
	handoffIndex   int
	startedAt      time.Time
	completedAt    time.Time
	summary        string
}

func (s *mockArchivableState) GetAgentID() string         { return s.agentID }
func (s *mockArchivableState) GetAgentType() string       { return s.agentType }
func (s *mockArchivableState) GetSessionID() string       { return s.sessionID }
func (s *mockArchivableState) GetPipelineID() string      { return s.pipelineID }
func (s *mockArchivableState) GetTriggerReason() string   { return s.triggerReason }
func (s *mockArchivableState) GetTriggerContext() float64 { return s.triggerContext }
func (s *mockArchivableState) GetHandoffIndex() int       { return s.handoffIndex }
func (s *mockArchivableState) GetStartedAt() time.Time    { return s.startedAt }
func (s *mockArchivableState) GetCompletedAt() time.Time  { return s.completedAt }
func (s *mockArchivableState) GetSummary() string         { return s.summary }
func (s *mockArchivableState) ToJSON() ([]byte, error)    { return []byte(`{}`), nil }

// mockHandoffableAgent implements HandoffableAgent for testing.
type mockHandoffableAgent struct {
	id           string
	agentType    string
	contextUsage float64
	state        *mockArchivableState
	injectErr    error
	buildErr     error
}

func newMockHandoffableAgent(id, agentType string, usage float64) *mockHandoffableAgent {
	return &mockHandoffableAgent{
		id:           id,
		agentType:    agentType,
		contextUsage: usage,
		state: &mockArchivableState{
			agentID:        id,
			agentType:      agentType,
			sessionID:      "session-1",
			pipelineID:     "pipeline-1",
			triggerReason:  "context_threshold",
			triggerContext: usage,
			handoffIndex:   0,
			startedAt:      time.Now().Add(-time.Hour),
			completedAt:    time.Now(),
			summary:        "Test agent state",
		},
	}
}

func (a *mockHandoffableAgent) BuildHandoffState(ctx context.Context) (pipeline.ArchivableHandoffState, error) {
	if a.buildErr != nil {
		return nil, a.buildErr
	}
	return a.state, nil
}

func (a *mockHandoffableAgent) InjectHandoffState(state any) error {
	return a.injectErr
}

func (a *mockHandoffableAgent) GetContextUsage() float64 {
	return a.contextUsage
}

func (a *mockHandoffableAgent) ID() string {
	return a.id
}

func (a *mockHandoffableAgent) Type() string {
	return a.agentType
}

// mockHandoffArchiver implements pipeline.HandoffArchiverService for testing.
type mockHandoffArchiver struct {
	archiveErr error
	archived   []pipeline.ArchivableHandoffState
	mu         sync.Mutex
}

func (a *mockHandoffArchiver) Archive(ctx context.Context, state pipeline.ArchivableHandoffState) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.archiveErr != nil {
		return a.archiveErr
	}
	a.archived = append(a.archived, state)
	return nil
}

func (a *mockHandoffArchiver) Query(ctx context.Context, query pipeline.ArchiveQuery) ([]*pipeline.HandoffArchiveEntry, error) {
	return nil, nil
}

// mockAgentFactory implements pipeline.AgentFactoryService for testing.
type mockAgentFactory struct {
	createErr error
	agent     pipeline.AgentService
}

func (f *mockAgentFactory) CreateAgent(
	ctx context.Context,
	agentType string,
	config pipeline.AgentConfig,
) (pipeline.AgentService, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.agent, nil
}

// mockAgentService implements pipeline.AgentService for testing.
type mockAgentService struct {
	id           string
	agentType    string
	injectErr    error
	terminateErr error
}

func (a *mockAgentService) ID() string   { return a.id }
func (a *mockAgentService) Type() string { return a.agentType }

func (a *mockAgentService) InjectHandoffState(state any) error {
	return a.injectErr
}

func (a *mockAgentService) Terminate(ctx context.Context) error {
	return a.terminateErr
}

// TestNewPipelineHandoffController tests constructor.
func TestNewPipelineHandoffController(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
	hm := pipeline.NewHandoffManager(
		pipeline.DefaultHandoffConfig(),
		&mockHandoffArchiver{},
		&mockAgentFactory{},
	)

	phc := NewPipelineHandoffController(pc, hm, PipelineHandoffConfig{})

	if phc.Controller() != pc {
		t.Error("expected controller to match")
	}
	if phc.HandoffManager() != hm {
		t.Error("expected handoff manager to match")
	}
	if phc.Config().DefaultThreshold != 0.75 {
		t.Errorf("expected default threshold 0.75, got %f", phc.Config().DefaultThreshold)
	}
}

// TestUpdateContextUsage tests context usage tracking.
func TestUpdateContextUsage(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
	phc := NewPipelineHandoffController(pc, nil, PipelineHandoffConfig{})

	t.Run("valid usage", func(t *testing.T) {
		err := phc.UpdateContextUsage("agent-1", 0.5)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}

		usage, exists := phc.GetContextUsage("agent-1")
		if !exists {
			t.Error("expected agent to exist")
		}
		if usage != 0.5 {
			t.Errorf("expected 0.5, got %f", usage)
		}
	})

	t.Run("boundary values", func(t *testing.T) {
		if err := phc.UpdateContextUsage("agent-2", 0.0); err != nil {
			t.Errorf("0.0 should be valid: %v", err)
		}
		if err := phc.UpdateContextUsage("agent-3", 1.0); err != nil {
			t.Errorf("1.0 should be valid: %v", err)
		}
	})

	t.Run("invalid negative", func(t *testing.T) {
		err := phc.UpdateContextUsage("agent-4", -0.1)
		if !errors.Is(err, ErrInvalidContextUsage) {
			t.Errorf("expected ErrInvalidContextUsage, got %v", err)
		}
	})

	t.Run("invalid above one", func(t *testing.T) {
		err := phc.UpdateContextUsage("agent-5", 1.1)
		if !errors.Is(err, ErrInvalidContextUsage) {
			t.Errorf("expected ErrInvalidContextUsage, got %v", err)
		}
	})
}

// TestShouldHandoff tests handoff threshold logic.
func TestShouldHandoff(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
	phc := NewPipelineHandoffController(pc, nil, PipelineHandoffConfig{
		DefaultThreshold: 0.75,
	})

	t.Run("no handoff below threshold", func(t *testing.T) {
		_ = phc.UpdateContextUsage("agent-1", 0.74)
		if phc.ShouldHandoff("agent-1") {
			t.Error("should not handoff at 74%")
		}
	})

	t.Run("handoff at threshold", func(t *testing.T) {
		_ = phc.UpdateContextUsage("agent-2", 0.75)
		if !phc.ShouldHandoff("agent-2") {
			t.Error("should handoff at 75%")
		}
	})

	t.Run("handoff above threshold", func(t *testing.T) {
		_ = phc.UpdateContextUsage("agent-3", 0.90)
		if !phc.ShouldHandoff("agent-3") {
			t.Error("should handoff at 90%")
		}
	})

	t.Run("unknown agent returns false", func(t *testing.T) {
		if phc.ShouldHandoff("unknown-agent") {
			t.Error("unknown agent should not trigger handoff")
		}
	})
}

// TestTriggerAgentHandoff tests the handoff trigger.
func TestTriggerAgentHandoff(t *testing.T) {
	t.Run("successful handoff", func(t *testing.T) {
		pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
		archiver := &mockHandoffArchiver{}
		newAgent := &mockAgentService{id: "new-agent-1", agentType: "engineer"}
		factory := &mockAgentFactory{agent: newAgent}
		hm := pipeline.NewHandoffManager(pipeline.DefaultHandoffConfig(), archiver, factory)

		phc := NewPipelineHandoffController(pc, hm, PipelineHandoffConfig{})
		agent := newMockHandoffableAgent("agent-1", "engineer", 0.80)

		ctx := context.Background()
		result, err := phc.TriggerAgentHandoff(ctx, agent)

		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if !result.Success {
			t.Error("expected success")
		}
		if result.OldAgentID != "agent-1" {
			t.Errorf("expected old agent-1, got %s", result.OldAgentID)
		}
		if result.NewAgentID != "new-agent-1" {
			t.Errorf("expected new agent new-agent-1, got %s", result.NewAgentID)
		}
	})

	t.Run("no handoff manager", func(t *testing.T) {
		pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
		phc := NewPipelineHandoffController(pc, nil, PipelineHandoffConfig{})
		agent := newMockHandoffableAgent("agent-1", "engineer", 0.80)

		_, err := phc.TriggerAgentHandoff(context.Background(), agent)
		if !errors.Is(err, ErrNoHandoffManager) {
			t.Errorf("expected ErrNoHandoffManager, got %v", err)
		}
	})

	t.Run("build state error", func(t *testing.T) {
		pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
		hm := pipeline.NewHandoffManager(
			pipeline.DefaultHandoffConfig(),
			&mockHandoffArchiver{},
			&mockAgentFactory{},
		)
		phc := NewPipelineHandoffController(pc, hm, PipelineHandoffConfig{})

		agent := newMockHandoffableAgent("agent-1", "engineer", 0.80)
		agent.buildErr = errors.New("build failed")

		result, err := phc.TriggerAgentHandoff(context.Background(), agent)
		if err == nil {
			t.Error("expected error")
		}
		if result.Success {
			t.Error("expected failure")
		}
	})
}

// TestReplaceAgentSupervisor tests supervisor replacement.
func TestReplaceAgentSupervisor(t *testing.T) {
	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("old-agent", "engineer")
	budget.RegisterAgent("new-agent", "engineer")

	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
	phc := NewPipelineHandoffController(pc, nil, PipelineHandoffConfig{})

	oldSupervisor := NewAgentSupervisor(
		context.Background(),
		"old-agent", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	pc.RegisterSupervisor("old-agent", oldSupervisor)
	_ = phc.UpdateContextUsage("old-agent", 0.80)

	newSupervisor := NewAgentSupervisor(
		context.Background(),
		"new-agent", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)

	t.Run("successful replacement", func(t *testing.T) {
		err := phc.ReplaceAgentSupervisor(context.Background(), "old-agent", newSupervisor)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}

		if pc.SupervisorCount() != 1 {
			t.Errorf("expected 1 supervisor, got %d", pc.SupervisorCount())
		}

		_, exists := phc.GetContextUsage("old-agent")
		if exists {
			t.Error("old agent context usage should be removed")
		}
	})

	t.Run("nil supervisor", func(t *testing.T) {
		err := phc.ReplaceAgentSupervisor(context.Background(), "any-agent", nil)
		if !errors.Is(err, ErrSupervisorNotFound) {
			t.Errorf("expected ErrSupervisorNotFound, got %v", err)
		}
	})
}

// TestAgentRegistration tests agent registration/unregistration.
func TestAgentRegistration(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
	phc := NewPipelineHandoffController(pc, nil, PipelineHandoffConfig{})

	phc.RegisterAgent("agent-1")
	phc.RegisterAgent("agent-2")

	if phc.AgentCount() != 2 {
		t.Errorf("expected 2 agents, got %d", phc.AgentCount())
	}

	usage, exists := phc.GetContextUsage("agent-1")
	if !exists || usage != 0 {
		t.Error("registered agent should have 0 usage")
	}

	phc.UnregisterAgent("agent-1")
	if phc.AgentCount() != 1 {
		t.Errorf("expected 1 agent after unregister, got %d", phc.AgentCount())
	}
}

// TestAllAgentUsages tests snapshot functionality.
func TestAllAgentUsages(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
	phc := NewPipelineHandoffController(pc, nil, PipelineHandoffConfig{})

	_ = phc.UpdateContextUsage("agent-1", 0.5)
	_ = phc.UpdateContextUsage("agent-2", 0.7)
	_ = phc.UpdateContextUsage("agent-3", 0.9)

	usages := phc.AllAgentUsages()

	if len(usages) != 3 {
		t.Errorf("expected 3 usages, got %d", len(usages))
	}
	if usages["agent-1"] != 0.5 {
		t.Errorf("expected 0.5 for agent-1, got %f", usages["agent-1"])
	}
	if usages["agent-2"] != 0.7 {
		t.Errorf("expected 0.7 for agent-2, got %f", usages["agent-2"])
	}
	if usages["agent-3"] != 0.9 {
		t.Errorf("expected 0.9 for agent-3, got %f", usages["agent-3"])
	}
}

// TestSetThreshold tests threshold modification.
func TestSetThreshold(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
	phc := NewPipelineHandoffController(pc, nil, PipelineHandoffConfig{})

	t.Run("valid threshold", func(t *testing.T) {
		err := phc.SetThreshold(0.80)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if phc.Config().DefaultThreshold != 0.80 {
			t.Errorf("expected 0.80, got %f", phc.Config().DefaultThreshold)
		}
	})

	t.Run("invalid threshold", func(t *testing.T) {
		err := phc.SetThreshold(1.5)
		if !errors.Is(err, ErrInvalidContextUsage) {
			t.Errorf("expected ErrInvalidContextUsage, got %v", err)
		}
	})
}

// TestConcurrentContextUpdates tests thread-safety of context updates.
func TestConcurrentContextUpdates(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
	phc := NewPipelineHandoffController(pc, nil, PipelineHandoffConfig{})

	const numAgents = 10
	const numUpdates = 100

	var wg sync.WaitGroup

	for i := 0; i < numAgents; i++ {
		agentID := "agent-" + string(rune('0'+i))
		phc.RegisterAgent(agentID)

		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for j := 0; j < numUpdates; j++ {
				usage := float64(j%100) / 100.0
				_ = phc.UpdateContextUsage(id, usage)
				_ = phc.ShouldHandoff(id)
				_, _ = phc.GetContextUsage(id)
			}
		}(agentID)
	}

	wg.Wait()

	if phc.AgentCount() != numAgents {
		t.Errorf("expected %d agents, got %d", numAgents, phc.AgentCount())
	}
}

// TestConcurrentHandoffCheck tests concurrent ShouldHandoff calls.
func TestConcurrentHandoffCheck(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
	phc := NewPipelineHandoffController(pc, nil, PipelineHandoffConfig{
		DefaultThreshold: 0.75,
	})

	_ = phc.UpdateContextUsage("agent-1", 0.80)

	var wg sync.WaitGroup
	results := make([]bool, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = phc.ShouldHandoff("agent-1")
		}(i)
	}

	wg.Wait()

	for i, result := range results {
		if !result {
			t.Errorf("result %d should be true", i)
		}
	}
}

// TestDefaultPipelineHandoffConfig tests default configuration.
func TestDefaultPipelineHandoffConfig(t *testing.T) {
	cfg := DefaultPipelineHandoffConfig()

	if cfg.DefaultThreshold != 0.75 {
		t.Errorf("expected 0.75, got %f", cfg.DefaultThreshold)
	}
}

// TestHandoffResultConversion tests result conversion.
func TestHandoffResultConversion(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})
	phc := NewPipelineHandoffController(pc, nil, PipelineHandoffConfig{})

	t.Run("nil result", func(t *testing.T) {
		testErr := errors.New("test error")
		result := phc.convertResult(nil, testErr)
		if result.Success {
			t.Error("expected failure")
		}
		if result.Error != testErr {
			t.Error("error should be preserved")
		}
	})

	t.Run("valid result", func(t *testing.T) {
		pipelineResult := &pipeline.HandoffResult{
			Success:    true,
			NewAgentID: "new-1",
			OldAgentID: "old-1",
			HandoffID:  "handoff-1",
			Duration:   time.Second,
		}
		result := phc.convertResult(pipelineResult, nil)
		if !result.Success {
			t.Error("expected success")
		}
		if result.NewAgentID != "new-1" {
			t.Errorf("expected new-1, got %s", result.NewAgentID)
		}
		if result.Duration != time.Second {
			t.Errorf("expected 1s, got %v", result.Duration)
		}
	})
}

// Verify mockArchivableState implements ArchivableHandoffState.
var _ pipeline.ArchivableHandoffState = (*mockArchivableState)(nil)

// Verify mockHandoffableAgent implements HandoffableAgent.
var _ HandoffableAgent = (*mockHandoffableAgent)(nil)
