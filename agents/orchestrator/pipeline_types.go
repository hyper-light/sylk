package orchestrator

import (
	"time"

	"github.com/adalundhe/sylk/core/dag"
)

// PipelineUpdate is pushed by pipeline agents to the orchestrator.
type PipelineUpdate struct {
	DAGID     string    `json:"dag_id"`
	NodeID    string    `json:"node_id"`
	TaskID    string    `json:"task_id"`
	AgentID   string    `json:"agent_id"`
	AgentType string    `json:"agent_type"`
	Status    string    `json:"status"`
	Stage     string    `json:"stage,omitempty"` // Pipeline stage: "inspect", "test", "execute"
	Progress  float64   `json:"progress"`
	Message   string    `json:"message"`
	Output    any       `json:"output,omitempty"`
	Error     string    `json:"error,omitempty"`
	Attempt   int       `json:"attempt"`
	Timestamp time.Time `json:"timestamp"`
}

// PipelineQuery is sent by the orchestrator to a pipeline agent.
type PipelineQuery struct {
	QueryID   string `json:"query_id"`
	DAGID     string `json:"dag_id"`
	NodeID    string `json:"node_id"`
	QueryType string `json:"query_type"` // status | output | diagnostic
}

// PipelineQueryResponse is the response from a pipeline agent.
type PipelineQueryResponse struct {
	QueryID   string    `json:"query_id"`
	AgentID   string    `json:"agent_id"`
	AgentType string    `json:"agent_type"`
	State     any       `json:"state"`
	Timestamp time.Time `json:"timestamp"`
}

// DAGModification is the architect's request to modify a running DAG.
type DAGModification struct {
	AddNodes    []dag.NodeConfig `json:"add_nodes,omitempty"`
	RemoveNodes []string         `json:"remove_nodes,omitempty"`
	Reason      string           `json:"reason"`
}

// isTerminalStatus returns true if the status represents a terminal pipeline state.
func isTerminalStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled", "timed_out":
		return true
	default:
		return false
	}
}
