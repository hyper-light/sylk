package tdd

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/tester/shared"
)

func newTestManager(t *testing.T) *PipelineManager {
	t.Helper()
	factory := NewAgentFactory(AgentFactoryConfig{
		InspectorConfig: inspShared.DefaultPipelineInspectorConfig(),
		TesterConfig:    shared.PipelineTesterConfig{},
		EngineerConfig:  engineer.Config{},
		DesignerConfig:  designer.Config{},
	})
	return NewPipelineManager(PipelineManagerConfig{
		MaxActivePipelines: 4,
	}, factory, nil)
}

func newTestPipelineConfig(taskID string) PipelineConfig {
	return PipelineConfig{
		TaskID:     taskID,
		SessionID:  "session-1",
		DAGNodeID:  "dag-1",
		WorkerType: WorkerEngineer,
		InitialCriteria: &inspShared.InspectorCriteria{
			TaskID: taskID,
			SuccessCriteria: []inspShared.SuccessCriterion{
				{ID: "sc1", Description: "compiles", Verifiable: true},
			},
		},
	}
}

func TestPipelineManager_CreateAndGet(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.CloseAll()

	id, err := mgr.Create(context.Background(), newTestPipelineConfig("task-1"))
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("expected non-empty pipeline ID")
	}

	p, err := mgr.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if p.TaskID != "task-1" {
		t.Errorf("got TaskID %q, want %q", p.TaskID, "task-1")
	}
	if p.Status != StatusPending {
		t.Errorf("got status %s, want %s", p.Status, StatusPending)
	}
}

func TestPipelineManager_GetNotFound(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.CloseAll()

	_, err := mgr.Get("nonexistent")
	if err != ErrPipelineNotFound {
		t.Errorf("expected ErrPipelineNotFound, got %v", err)
	}
}

func TestPipelineManager_GetBySession(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.CloseAll()

	cfg1 := newTestPipelineConfig("task-s1")
	cfg1.SessionID = "session-A"
	cfg2 := newTestPipelineConfig("task-s2")
	cfg2.SessionID = "session-A"
	cfg3 := newTestPipelineConfig("task-s3")
	cfg3.SessionID = "session-B"

	id1, _ := mgr.Create(context.Background(), cfg1)
	id2, _ := mgr.Create(context.Background(), cfg2)
	mgr.Create(context.Background(), cfg3)

	ids := mgr.GetBySession("session-A")
	if len(ids) != 2 {
		t.Errorf("expected 2 pipelines for session-A, got %d", len(ids))
	}
	found := map[string]bool{id1: false, id2: false}
	for _, id := range ids {
		found[id] = true
	}
	for id, ok := range found {
		if !ok {
			t.Errorf("missing pipeline %s", id)
		}
	}
}

func TestPipelineManager_GetByDAG(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.CloseAll()

	cfg := newTestPipelineConfig("task-dag")
	cfg.DAGNodeID = "dag-X"

	id, _ := mgr.Create(context.Background(), cfg)
	ids := mgr.GetByDAG("dag-X")
	if len(ids) != 1 || ids[0] != id {
		t.Errorf("expected [%s], got %v", id, ids)
	}

	empty := mgr.GetByDAG("dag-NOPE")
	if len(empty) != 0 {
		t.Errorf("expected empty, got %v", empty)
	}
}

func TestPipelineManager_GetActive(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.CloseAll()

	id, _ := mgr.Create(context.Background(), newTestPipelineConfig("task-active"))
	active := mgr.GetActive()
	if len(active) != 1 || active[0] != id {
		t.Errorf("expected [%s], got %v", id, active)
	}
}

func TestPipelineManager_MaxActivePipelines(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.CloseAll()

	ctx := context.Background()
	for i := range 4 {
		cfg := newTestPipelineConfig("task-cap-" + string(rune('A'+i)))
		_, err := mgr.Create(ctx, cfg)
		if err != nil {
			t.Fatalf("create pipeline %d: %v", i, err)
		}
	}

	// Fifth should fail.
	_, err := mgr.Create(ctx, newTestPipelineConfig("task-cap-E"))
	if err != ErrCapacityExhausted {
		t.Errorf("expected ErrCapacityExhausted, got %v", err)
	}
}

func TestPipelineManager_CloseAll(t *testing.T) {
	mgr := newTestManager(t)

	ctx := context.Background()
	id1, _ := mgr.Create(ctx, newTestPipelineConfig("task-close-1"))
	id2, _ := mgr.Create(ctx, newTestPipelineConfig("task-close-2"))

	mgr.CloseAll()

	// All pipelines should be terminal.
	p1, _ := mgr.Get(id1)
	p2, _ := mgr.Get(id2)
	if !IsTerminalStatus(p1.Status) {
		t.Errorf("pipeline 1 status %s, want terminal", p1.Status)
	}
	if !IsTerminalStatus(p2.Status) {
		t.Errorf("pipeline 2 status %s, want terminal", p2.Status)
	}

	// Manager should be closed.
	_, err := mgr.Create(ctx, newTestPipelineConfig("task-after-close"))
	if err != ErrPipelineClosed {
		t.Errorf("expected ErrPipelineClosed, got %v", err)
	}
}

func TestPipelineManager_StartAndCancel(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.CloseAll()

	ctx := context.Background()
	id, err := mgr.Create(ctx, newTestPipelineConfig("task-start-cancel"))
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.Start(ctx, id); err != nil {
		t.Fatal(err)
	}

	// Give executor time to start.
	time.Sleep(50 * time.Millisecond)

	if err := mgr.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}

	p, _ := mgr.Get(id)
	if !IsTerminalStatus(p.Status) {
		t.Errorf("expected terminal status after cancel, got %s", p.Status)
	}
}

func TestPipelineManager_ReleaseUnregistersManagedAgents(t *testing.T) {
	factory := NewAgentFactory(AgentFactoryConfig{
		InspectorConfig: inspShared.DefaultPipelineInspectorConfig(),
		TesterConfig:    shared.PipelineTesterConfig{},
		EngineerConfig:  engineer.Config{},
		DesignerConfig:  designer.Config{},
	})
	var unregistered []string
	mgr := NewPipelineManager(PipelineManagerConfig{
		UnregisterAgent: func(agentID string) {
			unregistered = append(unregistered, agentID)
		},
	}, factory, nil)

	id, err := mgr.Create(context.Background(), newTestPipelineConfig("task-release"))
	if err != nil {
		t.Fatal(err)
	}

	mgr.Release(id)

	want := map[string]bool{
		"task-release-inspector-pipeline": false,
		"task-release-tester-pipeline":    false,
		"task-release-engineer":           false,
	}
	for _, agentID := range unregistered {
		if _, ok := want[agentID]; ok {
			want[agentID] = true
		}
	}
	for agentID, seen := range want {
		if !seen {
			t.Fatalf("expected unregister for %s, got %v", agentID, unregistered)
		}
	}
	if _, err := mgr.Get(id); err != ErrPipelineNotFound {
		t.Fatalf("Get after Release error = %v, want %v", err, ErrPipelineNotFound)
	}
}

func TestPipelineManager_GetResult(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.CloseAll()

	ctx := context.Background()
	id, _ := mgr.Create(ctx, newTestPipelineConfig("task-result"))

	result, err := mgr.GetResult(id)
	if err != nil {
		t.Fatal(err)
	}
	if result.PipelineID != id {
		t.Errorf("got PipelineID %q, want %q", result.PipelineID, id)
	}
	if result.TaskID != "task-result" {
		t.Errorf("got TaskID %q, want %q", result.TaskID, "task-result")
	}
}

func TestPipelineManager_StartNotFound(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.CloseAll()

	err := mgr.Start(context.Background(), "nonexistent")
	if err != ErrPipelineNotFound {
		t.Errorf("expected ErrPipelineNotFound, got %v", err)
	}
}

func TestPipelineManager_CancelNotFound(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.CloseAll()

	err := mgr.Cancel(context.Background(), "nonexistent")
	if err != ErrPipelineNotFound {
		t.Errorf("expected ErrPipelineNotFound, got %v", err)
	}
}
