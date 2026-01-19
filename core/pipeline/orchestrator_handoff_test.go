package pipeline_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOrchestratorHandoffState(t *testing.T) {
	t.Run("creates state with initialized fields", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-123", "Build feature X")

		assert.Equal(t, "session-123", state.SessionID)
		assert.Equal(t, "Build feature X", state.OriginalGoal)
		assert.NotNil(t, state.PendingTasks)
		assert.NotNil(t, state.ActivePipelines)
		assert.NotNil(t, state.CompletedTasks)
		assert.NotNil(t, state.AgentStates)
		assert.NotNil(t, state.WaitingOn)
		assert.NotNil(t, state.NextActions)
		assert.NotNil(t, state.KeyDecisions)
		assert.NotNil(t, state.Blockers)
		assert.False(t, state.Timestamp.IsZero())
	})
}

func TestOrchestratorHandoffState_ArchivableInterface(t *testing.T) {
	t.Run("implements ArchivableHandoffState interface", func(t *testing.T) {
		state := createTestOrchestratorState()

		var _ pipeline.ArchivableHandoffState = state
	})

	t.Run("GetAgentType returns orchestrator", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-123", "goal")

		assert.Equal(t, "orchestrator", state.GetAgentType())
	})

	t.Run("GetSessionID returns session ID", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-abc", "goal")

		assert.Equal(t, "session-abc", state.GetSessionID())
	})

	t.Run("GetTriggerReason returns handoff reason", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-123", "goal")
		state.HandoffReason = "context_threshold_75%"

		assert.Equal(t, "context_threshold_75%", state.GetTriggerReason())
	})

	t.Run("GetHandoffIndex returns handoff index", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-123", "goal")
		state.HandoffIndex = 3

		assert.Equal(t, 3, state.GetHandoffIndex())
	})

	t.Run("GetCompletedAt returns timestamp", func(t *testing.T) {
		now := time.Now()
		state := pipeline.NewOrchestratorHandoffState("session-123", "goal")
		state.Timestamp = now

		assert.Equal(t, now, state.GetCompletedAt())
	})

	t.Run("GetTriggerContext returns default 0.75 without base state", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-123", "goal")

		assert.Equal(t, 0.75, state.GetTriggerContext())
	})

	t.Run("GetAgentID returns empty without base state", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-123", "goal")

		assert.Equal(t, "", state.GetAgentID())
	})

	t.Run("GetPipelineID returns empty without base state", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-123", "goal")

		assert.Equal(t, "", state.GetPipelineID())
	})

	t.Run("GetStartedAt returns workflow start time when available", func(t *testing.T) {
		startTime := time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC)
		state := pipeline.NewOrchestratorHandoffState("session-123", "goal")
		state.CurrentWorkflow = &pipeline.WorkflowState{
			ID:        "wf-1",
			StartTime: startTime,
		}

		assert.Equal(t, startTime, state.GetStartedAt())
	})

	t.Run("GetStartedAt returns zero time without workflow or base", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-123", "goal")

		assert.True(t, state.GetStartedAt().IsZero())
	})
}

func TestOrchestratorHandoffState_ToJSON(t *testing.T) {
	t.Run("serializes to valid JSON", func(t *testing.T) {
		state := createTestOrchestratorState()

		data, err := state.ToJSON()

		require.NoError(t, err)
		assert.Contains(t, string(data), `"session_id":"session-123"`)
		assert.Contains(t, string(data), `"original_goal":"Implement user authentication"`)
	})

	t.Run("includes all fields in JSON", func(t *testing.T) {
		state := createTestOrchestratorState()

		data, err := state.ToJSON()
		require.NoError(t, err)

		var unmarshaled map[string]any
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)

		assert.Contains(t, unmarshaled, "session_id")
		assert.Contains(t, unmarshaled, "original_goal")
		assert.Contains(t, unmarshaled, "current_workflow")
		assert.Contains(t, unmarshaled, "pending_tasks")
		assert.Contains(t, unmarshaled, "active_pipelines")
		assert.Contains(t, unmarshaled, "completed_tasks")
		assert.Contains(t, unmarshaled, "agent_states")
		assert.Contains(t, unmarshaled, "waiting_on")
		assert.Contains(t, unmarshaled, "next_actions")
	})
}

func TestOrchestratorHandoffState_GetSummary(t *testing.T) {
	t.Run("generates summary with all components", func(t *testing.T) {
		state := createTestOrchestratorState()
		state.Blockers = []string{"API rate limit", "Missing config"}
		state.HandoffReason = "context_threshold"

		summary := state.GetSummary()

		assert.Contains(t, summary, "Orchestrator handoff for session session-123")
		assert.Contains(t, summary, "Goal: Implement user authentication")
		assert.Contains(t, summary, "Workflow: Auth Implementation")
		assert.Contains(t, summary, "executing")
		assert.Contains(t, summary, "Tasks: 2 pending, 1 completed")
		assert.Contains(t, summary, "Pipelines: 1 active")
		assert.Contains(t, summary, "Blockers: API rate limit; Missing config")
		assert.Contains(t, summary, "Reason: context_threshold")
	})

	t.Run("generates minimal summary with empty state", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-456", "")

		summary := state.GetSummary()

		assert.Contains(t, summary, "Orchestrator handoff for session session-456")
		assert.Contains(t, summary, "Tasks: 0 pending, 0 completed")
	})
}

func TestOrchestratorHandoffState_SetBaseState(t *testing.T) {
	t.Run("sets base state for interface methods", func(t *testing.T) {
		state := pipeline.NewOrchestratorHandoffState("session-123", "goal")
		base := &pipeline.BaseArchivableState{
			AgentID:        "orch-agent-1",
			PipelineID:     "pipeline-999",
			TriggerContext: 0.80,
			StartedAt:      time.Date(2025, 1, 18, 9, 0, 0, 0, time.UTC),
		}

		state.SetBaseState(base)

		assert.Equal(t, "orch-agent-1", state.GetAgentID())
		assert.Equal(t, "pipeline-999", state.GetPipelineID())
		assert.Equal(t, 0.80, state.GetTriggerContext())
		assert.Equal(t, time.Date(2025, 1, 18, 9, 0, 0, 0, time.UTC), state.GetStartedAt())
	})
}

func TestOrchestratorHandoffBuilder(t *testing.T) {
	t.Run("builds state with all fields", func(t *testing.T) {
		workflow := &pipeline.WorkflowState{
			ID:       "wf-1",
			Name:     "Test Workflow",
			Phase:    "executing",
			Progress: 0.5,
		}

		state := pipeline.NewOrchestratorHandoffBuilder("session-123", "Build feature").
			WithAgentID("agent-1").
			WithPipelineID("pipeline-1").
			WithTriggerContext(0.78).
			WithWorkflow(workflow).
			WithHandoffIndex(2).
			WithHandoffReason("context_threshold").
			WithBlockers([]string{"blocker1"}).
			WithContextNotes("Important notes").
			Build()

		assert.Equal(t, "session-123", state.SessionID)
		assert.Equal(t, "Build feature", state.OriginalGoal)
		assert.Equal(t, "agent-1", state.GetAgentID())
		assert.Equal(t, "pipeline-1", state.GetPipelineID())
		assert.Equal(t, 0.78, state.GetTriggerContext())
		assert.Equal(t, workflow, state.CurrentWorkflow)
		assert.Equal(t, 2, state.HandoffIndex)
		assert.Equal(t, "context_threshold", state.HandoffReason)
		assert.Equal(t, []string{"blocker1"}, state.Blockers)
		assert.Equal(t, "Important notes", state.ContextNotes)
	})

	t.Run("builds state with pending tasks", func(t *testing.T) {
		tasks := []pipeline.OrchestratorTask{
			{ID: "task-1", Description: "Task 1"},
			{ID: "task-2", Description: "Task 2"},
		}

		state := pipeline.NewOrchestratorHandoffBuilder("session-123", "goal").
			WithPendingTasks(tasks).
			Build()

		assert.Len(t, state.PendingTasks, 2)
		assert.Equal(t, "task-1", state.PendingTasks[0].ID)
	})

	t.Run("builds state with active pipelines", func(t *testing.T) {
		pipelines := []pipeline.PipelineInfo{
			{ID: "pipe-1", TaskID: "task-1", Status: "running"},
		}

		state := pipeline.NewOrchestratorHandoffBuilder("session-123", "goal").
			WithActivePipelines(pipelines).
			Build()

		assert.Len(t, state.ActivePipelines, 1)
		assert.Equal(t, "pipe-1", state.ActivePipelines[0].ID)
	})

	t.Run("builds state with agent states", func(t *testing.T) {
		agents := map[string]pipeline.AgentStateSnapshot{
			"engineer-1": {
				AgentID:      "engineer-1",
				AgentType:    "engineer",
				ContextUsage: 0.65,
			},
		}

		state := pipeline.NewOrchestratorHandoffBuilder("session-123", "goal").
			WithAgentStates(agents).
			Build()

		assert.Len(t, state.AgentStates, 1)
		assert.Equal(t, 0.65, state.AgentStates["engineer-1"].ContextUsage)
	})

	t.Run("builds state with wait conditions", func(t *testing.T) {
		conditions := []pipeline.WaitCondition{
			{Type: "pipeline_complete", Target: "pipe-1", Description: "Waiting for pipe-1"},
		}

		state := pipeline.NewOrchestratorHandoffBuilder("session-123", "goal").
			WithWaitingOn(conditions).
			Build()

		assert.Len(t, state.WaitingOn, 1)
		assert.Equal(t, "pipeline_complete", state.WaitingOn[0].Type)
	})

	t.Run("builds state with next actions", func(t *testing.T) {
		actions := []pipeline.PlannedAction{
			{Action: "dispatch_task", Target: "engineer"},
		}

		state := pipeline.NewOrchestratorHandoffBuilder("session-123", "goal").
			WithNextActions(actions).
			Build()

		assert.Len(t, state.NextActions, 1)
		assert.Equal(t, "dispatch_task", state.NextActions[0].Action)
	})

	t.Run("builds state with key decisions", func(t *testing.T) {
		decisions := []string{"Use microservices", "PostgreSQL for DB"}

		state := pipeline.NewOrchestratorHandoffBuilder("session-123", "goal").
			WithKeyDecisions(decisions).
			Build()

		assert.Equal(t, decisions, state.KeyDecisions)
	})

	t.Run("builds state with completed tasks", func(t *testing.T) {
		tasks := []pipeline.OrchestratorTask{
			{ID: "done-1", Status: "completed"},
		}

		state := pipeline.NewOrchestratorHandoffBuilder("session-123", "goal").
			WithCompletedTasks(tasks).
			Build()

		assert.Len(t, state.CompletedTasks, 1)
	})

	t.Run("builds state with custom timestamp", func(t *testing.T) {
		ts := time.Date(2025, 1, 18, 15, 30, 0, 0, time.UTC)

		state := pipeline.NewOrchestratorHandoffBuilder("session-123", "goal").
			WithTimestamp(ts).
			Build()

		assert.Equal(t, ts, state.Timestamp)
	})

	t.Run("builds state with started at time", func(t *testing.T) {
		startTime := time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC)

		state := pipeline.NewOrchestratorHandoffBuilder("session-123", "goal").
			WithStartedAt(startTime).
			Build()

		assert.Equal(t, startTime, state.GetStartedAt())
	})
}

func TestBuildOrchestratorHandoffState(t *testing.T) {
	t.Run("creates complete state from parameters", func(t *testing.T) {
		workflow := &pipeline.WorkflowState{
			ID:       "wf-1",
			Name:     "Test",
			Phase:    "executing",
			Progress: 0.5,
		}
		startedAt := time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC)

		state := pipeline.BuildOrchestratorHandoffState(
			"agent-1",
			"session-123",
			"Build auth",
			workflow,
			[]pipeline.OrchestratorTask{{ID: "task-1"}},
			[]pipeline.PipelineInfo{{ID: "pipe-1"}},
			[]pipeline.OrchestratorTask{{ID: "done-1"}},
			map[string]pipeline.AgentStateSnapshot{"eng-1": {AgentID: "eng-1"}},
			[]pipeline.WaitCondition{{Type: "user_input"}},
			[]pipeline.PlannedAction{{Action: "dispatch"}},
			[]string{"decision1"},
			[]string{"blocker1"},
			"context notes",
			2,
			"context_threshold",
			0.78,
			startedAt,
		)

		assert.Equal(t, "agent-1", state.GetAgentID())
		assert.Equal(t, "session-123", state.SessionID)
		assert.Equal(t, "Build auth", state.OriginalGoal)
		assert.Equal(t, workflow, state.CurrentWorkflow)
		assert.Len(t, state.PendingTasks, 1)
		assert.Len(t, state.ActivePipelines, 1)
		assert.Len(t, state.CompletedTasks, 1)
		assert.Len(t, state.AgentStates, 1)
		assert.Len(t, state.WaitingOn, 1)
		assert.Len(t, state.NextActions, 1)
		assert.Equal(t, []string{"decision1"}, state.KeyDecisions)
		assert.Equal(t, []string{"blocker1"}, state.Blockers)
		assert.Equal(t, "context notes", state.ContextNotes)
		assert.Equal(t, 2, state.HandoffIndex)
		assert.Equal(t, "context_threshold", state.HandoffReason)
		assert.Equal(t, 0.78, state.GetTriggerContext())
		assert.Equal(t, startedAt, state.GetStartedAt())
	})
}

func TestInjectOrchestratorHandoffState(t *testing.T) {
	t.Run("extracts state components successfully", func(t *testing.T) {
		original := createTestOrchestratorState()
		original.HandoffIndex = 2

		injected, err := pipeline.InjectOrchestratorHandoffState(original)

		require.NoError(t, err)
		assert.Equal(t, original.SessionID, injected.SessionID)
		assert.Equal(t, original.OriginalGoal, injected.OriginalGoal)
		assert.Equal(t, original.CurrentWorkflow, injected.CurrentWorkflow)
		assert.Equal(t, original.PendingTasks, injected.PendingTasks)
		assert.Equal(t, original.ActivePipelines, injected.ActivePipelines)
		assert.Equal(t, original.CompletedTasks, injected.CompletedTasks)
		assert.Equal(t, original.AgentStates, injected.AgentStates)
		assert.Equal(t, original.WaitingOn, injected.WaitingOn)
		assert.Equal(t, original.NextActions, injected.NextActions)
		assert.Equal(t, original.KeyDecisions, injected.KeyDecisions)
		assert.Equal(t, original.Blockers, injected.Blockers)
		assert.Equal(t, original.ContextNotes, injected.ContextNotes)
		assert.Equal(t, 3, injected.HandoffIndex) // Incremented
	})

	t.Run("returns error for nil state", func(t *testing.T) {
		injected, err := pipeline.InjectOrchestratorHandoffState(nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "handoff state is nil")
		assert.Nil(t, injected)
	})

	t.Run("returns error for empty session ID", func(t *testing.T) {
		state := &pipeline.OrchestratorHandoffState{
			SessionID: "",
		}

		injected, err := pipeline.InjectOrchestratorHandoffState(state)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing session ID")
		assert.Nil(t, injected)
	})
}

func TestOrchestratorHandoffStateFromJSON(t *testing.T) {
	t.Run("deserializes from valid JSON", func(t *testing.T) {
		original := createTestOrchestratorState()
		data, err := original.ToJSON()
		require.NoError(t, err)

		restored, err := pipeline.OrchestratorHandoffStateFromJSON(data)

		require.NoError(t, err)
		assert.Equal(t, original.SessionID, restored.SessionID)
		assert.Equal(t, original.OriginalGoal, restored.OriginalGoal)
		assert.Equal(t, original.HandoffIndex, restored.HandoffIndex)
		assert.Equal(t, original.HandoffReason, restored.HandoffReason)
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		invalidJSON := []byte(`{"invalid": json}`)

		restored, err := pipeline.OrchestratorHandoffStateFromJSON(invalidJSON)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal")
		assert.Nil(t, restored)
	})

	t.Run("handles empty JSON object", func(t *testing.T) {
		emptyJSON := []byte(`{}`)

		restored, err := pipeline.OrchestratorHandoffStateFromJSON(emptyJSON)

		require.NoError(t, err)
		assert.NotNil(t, restored)
		assert.Equal(t, "", restored.SessionID)
	})
}

func TestOrchestratorHandoffState_Concurrency(t *testing.T) {
	t.Run("handles concurrent reads safely", func(t *testing.T) {
		state := createTestOrchestratorState()

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = state.GetAgentType()
				_ = state.GetSessionID()
				_ = state.GetSummary()
				_, _ = state.ToJSON()
			}()
		}
		wg.Wait()
	})
}

func TestWorkflowState(t *testing.T) {
	t.Run("serializes to JSON correctly", func(t *testing.T) {
		wf := pipeline.WorkflowState{
			ID:        "wf-123",
			Name:      "Auth Workflow",
			Phase:     "executing",
			Progress:  0.75,
			StartTime: time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC),
		}

		data, err := json.Marshal(wf)

		require.NoError(t, err)
		assert.Contains(t, string(data), `"id":"wf-123"`)
		assert.Contains(t, string(data), `"name":"Auth Workflow"`)
		assert.Contains(t, string(data), `"phase":"executing"`)
		assert.Contains(t, string(data), `"progress":0.75`)
	})
}

func TestOrchestratorTask(t *testing.T) {
	t.Run("serializes with all fields", func(t *testing.T) {
		task := pipeline.OrchestratorTask{
			ID:           "task-1",
			Description:  "Implement login",
			AssignedTo:   "engineer",
			PipelineID:   "pipe-1",
			Status:       "in_progress",
			Dependencies: []string{"task-0"},
		}

		data, err := json.Marshal(task)

		require.NoError(t, err)
		assert.Contains(t, string(data), `"id":"task-1"`)
		assert.Contains(t, string(data), `"assigned_to":"engineer"`)
		assert.Contains(t, string(data), `"dependencies":["task-0"]`)
	})

	t.Run("omits empty optional fields", func(t *testing.T) {
		task := pipeline.OrchestratorTask{
			ID:          "task-1",
			Description: "Task",
			Status:      "pending",
		}

		data, err := json.Marshal(task)

		require.NoError(t, err)
		assert.NotContains(t, string(data), "assigned_to")
		assert.NotContains(t, string(data), "pipeline_id")
		assert.NotContains(t, string(data), "dependencies")
	})
}

func TestPipelineInfo(t *testing.T) {
	t.Run("serializes correctly", func(t *testing.T) {
		info := pipeline.PipelineInfo{
			ID:           "pipe-1",
			TaskID:       "task-1",
			Status:       "running",
			ActiveAgents: []string{"engineer", "tester"},
		}

		data, err := json.Marshal(info)

		require.NoError(t, err)
		assert.Contains(t, string(data), `"id":"pipe-1"`)
		assert.Contains(t, string(data), `"active_agents":["engineer","tester"]`)
	})
}

func TestAgentStateSnapshot(t *testing.T) {
	t.Run("captures agent state", func(t *testing.T) {
		snapshot := pipeline.AgentStateSnapshot{
			AgentID:      "eng-1",
			AgentType:    "engineer",
			ContextUsage: 0.65,
			LastActivity: time.Date(2025, 1, 18, 11, 30, 0, 0, time.UTC),
			CurrentTask:  "task-1",
		}

		data, err := json.Marshal(snapshot)

		require.NoError(t, err)
		assert.Contains(t, string(data), `"agent_id":"eng-1"`)
		assert.Contains(t, string(data), `"context_usage":0.65`)
		assert.Contains(t, string(data), `"current_task":"task-1"`)
	})
}

func TestWaitCondition(t *testing.T) {
	t.Run("serializes all condition types", func(t *testing.T) {
		conditions := []pipeline.WaitCondition{
			{Type: "pipeline_complete", Target: "pipe-1", Description: "Wait for pipeline"},
			{Type: "agent_response", Target: "architect", Description: "Wait for plan"},
			{Type: "user_input", Target: "user", Description: "Wait for confirmation"},
		}

		for _, cond := range conditions {
			data, err := json.Marshal(cond)
			require.NoError(t, err)
			assert.Contains(t, string(data), cond.Type)
		}
	})
}

func TestPlannedAction(t *testing.T) {
	t.Run("serializes with details", func(t *testing.T) {
		action := pipeline.PlannedAction{
			Action:  "dispatch_task",
			Target:  "engineer",
			Details: map[string]any{"priority": "high", "timeout": 300},
		}

		data, err := json.Marshal(action)

		require.NoError(t, err)
		assert.Contains(t, string(data), `"action":"dispatch_task"`)
		assert.Contains(t, string(data), `"target":"engineer"`)
		assert.Contains(t, string(data), `"details"`)
	})

	t.Run("omits empty details", func(t *testing.T) {
		action := pipeline.PlannedAction{
			Action: "spawn_pipeline",
			Target: "tester",
		}

		data, err := json.Marshal(action)

		require.NoError(t, err)
		assert.NotContains(t, string(data), "details")
	})
}

func TestOrchestratorHandoffState_RoundTrip(t *testing.T) {
	t.Run("survives JSON round trip", func(t *testing.T) {
		original := createTestOrchestratorState()

		data, err := original.ToJSON()
		require.NoError(t, err)

		restored, err := pipeline.OrchestratorHandoffStateFromJSON(data)
		require.NoError(t, err)

		assert.Equal(t, original.SessionID, restored.SessionID)
		assert.Equal(t, original.OriginalGoal, restored.OriginalGoal)
		assert.Equal(t, original.CurrentWorkflow.ID, restored.CurrentWorkflow.ID)
		assert.Equal(t, original.CurrentWorkflow.Name, restored.CurrentWorkflow.Name)
		assert.Equal(t, original.CurrentWorkflow.Phase, restored.CurrentWorkflow.Phase)
		assert.Len(t, restored.PendingTasks, len(original.PendingTasks))
		assert.Len(t, restored.ActivePipelines, len(original.ActivePipelines))
		assert.Len(t, restored.CompletedTasks, len(original.CompletedTasks))
		assert.Equal(t, original.HandoffIndex, restored.HandoffIndex)
		assert.Equal(t, original.HandoffReason, restored.HandoffReason)
	})
}

func createTestOrchestratorState() *pipeline.OrchestratorHandoffState {
	state := pipeline.NewOrchestratorHandoffState("session-123", "Implement user authentication")

	state.CurrentWorkflow = &pipeline.WorkflowState{
		ID:        "wf-auth-1",
		Name:      "Auth Implementation",
		Phase:     "executing",
		Progress:  0.45,
		StartTime: time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC),
	}

	state.PendingTasks = []pipeline.OrchestratorTask{
		{ID: "task-2", Description: "Create JWT handler", AssignedTo: "engineer", Status: "pending"},
		{ID: "task-3", Description: "Add password hashing", AssignedTo: "engineer", Status: "blocked"},
	}

	state.ActivePipelines = []pipeline.PipelineInfo{
		{ID: "pipe-1", TaskID: "task-1", Status: "running", ActiveAgents: []string{"engineer", "tester"}},
	}

	state.CompletedTasks = []pipeline.OrchestratorTask{
		{ID: "task-1", Description: "Setup auth module", Status: "completed"},
	}

	state.AgentStates = map[string]pipeline.AgentStateSnapshot{
		"engineer-1": {AgentID: "engineer-1", AgentType: "engineer", ContextUsage: 0.65},
		"tester-1":   {AgentID: "tester-1", AgentType: "tester", ContextUsage: 0.32},
	}

	state.WaitingOn = []pipeline.WaitCondition{
		{Type: "pipeline_complete", Target: "pipe-1", Description: "Waiting for task-1 pipeline"},
	}

	state.NextActions = []pipeline.PlannedAction{
		{Action: "dispatch_task", Target: "engineer", Details: map[string]any{"task_id": "task-2"}},
	}

	state.KeyDecisions = []string{"Using JWT for tokens", "Bcrypt for password hashing"}
	state.ContextNotes = "User requested OAuth2 support in future iteration"
	state.HandoffIndex = 1
	state.HandoffReason = "context_threshold_75%"

	return state
}
