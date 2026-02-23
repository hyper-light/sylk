package orchestrator

import (
	"encoding/json"
	"sort"
	"time"
)

const (
	snapshotMaxWorkflows = 20
	snapshotMaxTasks     = 50
	snapshotMaxAlerts    = 10
	snapshotMaxDAGs      = 10
	snapshotMaxBuffers   = 30
	snapshotMaxPipeline  = 20
)

type stateSnapshot struct {
	Timestamp  time.Time         `json:"timestamp"`
	Workflows  []workflowSnap   `json:"workflows"`
	Tasks      []taskSnap        `json:"tasks"`
	Health     healthSnap        `json:"health"`
	Alerts     []alertSnap       `json:"alerts"`
	Stats      snapshotStats     `json:"stats"`
	DAGs       []dagSnap         `json:"dags,omitempty"`
	Buffers    []bufferSnap      `json:"buffers,omitempty"`
	Pipeline   []pipelineSnap    `json:"pipeline,omitempty"`
}

type workflowSnap struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Status   WorkflowStatus `json:"status"`
	Progress float64        `json:"progress"`
	Phase    string         `json:"phase,omitempty"`
}

type taskSnap struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Status   TaskStatus `json:"status"`
	AgentID  string     `json:"agent_id,omitempty"`
	Duration string     `json:"duration,omitempty"`
}

type healthSnap struct {
	Healthy   int `json:"healthy"`
	Degraded  int `json:"degraded"`
	Unhealthy int `json:"unhealthy"`
	Critical  int `json:"critical"`
}

type alertSnap struct {
	Type    AlertType `json:"type"`
	AgentID string    `json:"agent_id"`
	Message string    `json:"message"`
}

type snapshotStats struct {
	Completed int64 `json:"completed"`
	Failed    int64 `json:"failed"`
	Submitted int64 `json:"submitted"`
}

type dagSnap struct {
	ID           string  `json:"id"`
	PlanID       string  `json:"plan_id"`
	State        string  `json:"state"`
	CurrentLayer int     `json:"current_layer"`
	TotalLayers  int     `json:"total_layers"`
	Progress     float64 `json:"progress"`
	NodesFailed  int     `json:"nodes_failed,omitempty"`
	Duration     string  `json:"duration,omitempty"`
}

type bufferSnap struct {
	TaskID   string  `json:"task_id"`
	Count    int     `json:"count"`
	Latest   string  `json:"latest"`
	Progress float64 `json:"progress"`
}

type pipelineSnap struct {
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	NodeID    string `json:"node_id"`
	Status    string `json:"status"`
}

// buildStateSnapshot produces a compact JSON representation of current state
// for inclusion in LLM prompts. The caller must hold o.mu.RLock.
func (o *Orchestrator) buildStateSnapshot() string {
	snap := stateSnapshot{
		Timestamp: time.Now(),
		Stats: snapshotStats{
			Completed: o.state.Stats.CompletedTasks,
			Failed:    o.state.Stats.FailedTasks,
			Submitted: o.state.Stats.EventsSubmitted,
		},
	}

	snap.Workflows = collectWorkflowSnaps(o.state.Workflows)
	snap.Tasks = collectTaskSnaps(o.state.Tasks)
	snap.Health = collectHealthSnap(o.healthMonitor)
	snap.Alerts = collectAlertSnaps(o.healthMonitor)

	if o.dagBridge != nil {
		snap.DAGs = o.dagBridge.DAGSnapshots(snapshotMaxDAGs)
	}
	if o.bufferRegistry != nil {
		snap.Buffers = o.bufferRegistry.BufferSnapshots(snapshotMaxBuffers)
	}

	data, err := json.Marshal(snap)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func collectWorkflowSnaps(workflows map[string]*WorkflowState) []workflowSnap {
	snaps := make([]workflowSnap, 0, min(len(workflows), snapshotMaxWorkflows))
	for _, wf := range workflows {
		snaps = append(snaps, workflowSnap{
			ID:       wf.ID,
			Name:     wf.Name,
			Status:   wf.Status,
			Progress: wf.Progress,
			Phase:    wf.Phase,
		})
		if len(snaps) >= snapshotMaxWorkflows {
			break
		}
	}
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].ID > snaps[j].ID
	})
	return snaps
}

func collectTaskSnaps(tasks map[string]*TaskRecord) []taskSnap {
	snaps := make([]taskSnap, 0, min(len(tasks), snapshotMaxTasks))
	for _, task := range tasks {
		var dur string
		if task.StartedAt != nil {
			dur = time.Since(*task.StartedAt).Truncate(time.Second).String()
		}
		snaps = append(snaps, taskSnap{
			ID:       task.ID,
			Name:     task.Name,
			Status:   task.Status,
			AgentID:  task.AssignedAgentID,
			Duration: dur,
		})
		if len(snaps) >= snapshotMaxTasks {
			break
		}
	}
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].ID > snaps[j].ID
	})
	return snaps
}

func collectHealthSnap(hm *HealthMonitor) healthSnap {
	if hm == nil {
		return healthSnap{}
	}
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	var snap healthSnap
	for _, metrics := range hm.agents {
		switch metrics.Level {
		case HealthLevelHealthy:
			snap.Healthy++
		case HealthLevelDegraded:
			snap.Degraded++
		case HealthLevelUnhealthy:
			snap.Unhealthy++
		case HealthLevelCritical:
			snap.Critical++
		}
	}
	return snap
}

func collectAlertSnaps(hm *HealthMonitor) []alertSnap {
	if hm == nil {
		return nil
	}
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	snaps := make([]alertSnap, 0, snapshotMaxAlerts)
	for _, alert := range hm.alerts {
		if alert.ResolvedAt != nil {
			continue
		}
		snaps = append(snaps, alertSnap{
			Type:    alert.Type,
			AgentID: alert.AgentID,
			Message: alert.Message,
		})
		if len(snaps) >= snapshotMaxAlerts {
			break
		}
	}
	return snaps
}
