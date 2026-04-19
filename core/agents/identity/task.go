package identity

import (
	"fmt"
	"strings"
)

// TaskUID is the globally-unique identifier for a task. Produced by
// Factory (UUIDv7 in production).
type TaskUID string

// CorrelationID is the per-request trace identifier. One task may
// span multiple correlations (one per LLM call within the task's
// lifetime); each request carries exactly one correlation.
type CorrelationID string

// PipelineID groups related tasks into a pipeline. The orchestrator
// defines the pipeline; engineer/designer/tester-pipeline/
// inspector-pipeline containers execute against tasks within it.
type PipelineID string

// PipelineStage names the DAG layer / phase this task belongs to
// (e.g. "validate-tests", "implement", "audit"). Free-form but
// expected to come from a small enum maintained by the orchestrator.
type PipelineStage string

// PipelineRef describes a task's pipeline membership.
type PipelineRef struct {
	// ID names the pipeline. Required.
	ID PipelineID
	// Stage names the DAG layer within the pipeline. Optional —
	// standalone pipeline tasks may leave this empty.
	Stage PipelineStage
	// Parent is the TaskUID of the task that spawned this task, if
	// any. Empty for root tasks in the pipeline.
	Parent TaskUID
}

// TaskRef describes the unit of work currently being serviced.
// Carried on request contexts alongside an AgentIdentity. Immutable
// once constructed via Factory.
type TaskRef struct {
	uid         TaskUID
	displayID   string
	pipeline    *PipelineRef
	correlation CorrelationID
	namespace   Namespace
	labels      Labels
}

// UID returns the task's globally-unique identifier.
func (t *TaskRef) UID() TaskUID { return t.uid }

// DisplayID returns the human-readable slug for this task. Never
// used as a primary key; meant for log output and the UI.
func (t *TaskRef) DisplayID() string { return t.displayID }

// Pipeline returns the pipeline membership, or nil for standalone
// tasks (e.g. top-level user requests at the Guide).
func (t *TaskRef) Pipeline() *PipelineRef {
	if t.pipeline == nil {
		return nil
	}
	p := *t.pipeline
	return &p
}

// IsPipelineStep reports whether this task belongs to a pipeline.
func (t *TaskRef) IsPipelineStep() bool { return t.pipeline != nil }

// PipelineID returns the pipeline id (or ""). Convenience accessor.
func (t *TaskRef) PipelineID() PipelineID {
	if t.pipeline == nil {
		return ""
	}
	return t.pipeline.ID
}

// Stage returns the pipeline stage (or ""). Convenience accessor.
func (t *TaskRef) Stage() PipelineStage {
	if t.pipeline == nil {
		return ""
	}
	return t.pipeline.Stage
}

// ParentTask returns the parent task id (or ""). Convenience accessor.
func (t *TaskRef) ParentTask() TaskUID {
	if t.pipeline == nil {
		return ""
	}
	return t.pipeline.Parent
}

// Correlation returns the current request's correlation id.
func (t *TaskRef) Correlation() CorrelationID { return t.correlation }

// Namespace returns the session the task lives in. Must equal the
// Namespace of the AgentIdentity servicing it.
func (t *TaskRef) Namespace() Namespace { return t.namespace }

// Labels returns a defensive copy of the task's label map.
func (t *TaskRef) Labels() Labels { return t.labels.Clone() }

// Label returns the value for key, or "" when absent.
func (t *TaskRef) Label(key string) string { return t.labels.Get(key) }

// Visible returns the panel-facing task string. For pipeline tasks
// it's "<pipeline>/<stage>/<display-id>"; for standalone tasks it's
// just the display id.
func (t *TaskRef) Visible() string {
	if t == nil {
		return ""
	}
	display := t.displayID
	if display == "" {
		display = string(t.uid)
	}
	if t.pipeline == nil {
		return display
	}
	parts := make([]string, 0, 3)
	if t.pipeline.ID != "" {
		parts = append(parts, string(t.pipeline.ID))
	}
	if t.pipeline.Stage != "" {
		parts = append(parts, string(t.pipeline.Stage))
	}
	parts = append(parts, display)
	return strings.Join(parts, "/")
}

// String is the debug form. Never parsed.
func (t *TaskRef) String() string {
	if t == nil {
		return "<nil task>"
	}
	base := fmt.Sprintf("task[uid=%s corr=%s ns=%s", t.uid, t.correlation, t.namespace)
	if t.displayID != "" {
		base += " display=" + t.displayID
	}
	if t.pipeline != nil {
		base += fmt.Sprintf(" pipeline=%s stage=%s", t.pipeline.ID, t.pipeline.Stage)
		if t.pipeline.Parent != "" {
			base += " parent=" + string(t.pipeline.Parent)
		}
	}
	return base + "]"
}

// Equal reports structural equality.
func (t *TaskRef) Equal(other *TaskRef) bool {
	if t == nil || other == nil {
		return t == other
	}
	if t.uid != other.uid || t.displayID != other.displayID ||
		t.correlation != other.correlation || t.namespace != other.namespace {
		return false
	}
	if (t.pipeline == nil) != (other.pipeline == nil) {
		return false
	}
	if t.pipeline != nil && *t.pipeline != *other.pipeline {
		return false
	}
	if len(t.labels) != len(other.labels) {
		return false
	}
	for k, v := range t.labels {
		if other.labels[k] != v {
			return false
		}
	}
	return true
}

// AccountingKey is the (ContainerKey, TaskUID) composite under which
// token usage totals are aggregated. Identity + Task must both be
// present for a billable request; CategorySystem identities use a
// SystemTaskUID sentinel for their Task field.
type AccountingKey struct {
	Container ContainerKey
	Task      TaskUID
}

// String renders an AccountingKey for log output.
func (k AccountingKey) String() string {
	return fmt.Sprintf("%s/task=%s", k.Container, k.Task)
}

// SystemTaskUID is the sentinel TaskUID used for CategorySystem
// requests (OAuth refresh, probes). The accountant records these
// but marks them non-billable against any agent.
const SystemTaskUID TaskUID = "system"
