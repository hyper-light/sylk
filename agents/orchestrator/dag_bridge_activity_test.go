package orchestrator

import "testing"

func TestRecordDispatchActivity_RefreshesParentForPipelineSubNode(t *testing.T) {
	dispatcher := NewBusNodeDispatcher(nil, "orch", "sess", "dag-1", nil, nil, nil)
	bridge := &DAGBridge{
		activeDAGs: map[string]*ActiveDAGMeta{
			"dag-1": {
				Dispatcher: dispatcher,
			},
		},
	}

	bridge.RecordDispatchActivity("dag-1", "task_1:execute")

	if !dispatcher.hasRecentActivity("task_1:execute") {
		t.Fatal("expected sub-node activity to be recorded")
	}
	if !dispatcher.hasRecentActivity("task_1") {
		t.Fatal("expected parent-node activity to be refreshed from sub-node activity")
	}
}
