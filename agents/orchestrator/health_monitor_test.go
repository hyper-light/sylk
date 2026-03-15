package orchestrator

import (
	"testing"
	"time"
)

func TestHealthMonitorRunChecks_UsesTaskSnapshotsForTimeouts(t *testing.T) {
	o := &Orchestrator{
		config: DefaultConfig(),
		state:  NewState("sess-1"),
	}

	startedAt := time.Now().Add(-2 * time.Minute).UTC()
	o.state.Tasks["task-1"] = &TaskRecord{
		ID:              "task-1",
		Status:          TaskStatusRunning,
		AssignedAgentID: "tester",
		StartedAt:       &startedAt,
	}

	cfg := DefaultHealthConfig()
	cfg.TaskTimeout = time.Minute

	monitor := NewHealthMonitor(o, cfg)
	monitor.agents["tester"] = &AgentHealthMetrics{
		AgentID:   "tester",
		Level:     HealthLevelHealthy,
		FirstSeen: time.Now().UTC(),
		LastSeen:  time.Now().UTC(),
	}

	monitor.runChecks()

	metrics := monitor.agents["tester"]
	if metrics == nil {
		t.Fatal("expected tester metrics")
	}
	if metrics.TimedOutTasks != 1 {
		t.Fatalf("TimedOutTasks = %d, want 1", metrics.TimedOutTasks)
	}
	if len(metrics.ActiveAlerts) == 0 {
		t.Fatal("expected timeout alert to be recorded")
	}
}
