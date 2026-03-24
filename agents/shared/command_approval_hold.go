package shared

import "time"

const ControlPlaneKindCommandApprovalHold = "command_approval_hold"

type CommandApprovalHoldAction string

const (
	CommandApprovalHoldBegin   CommandApprovalHoldAction = "begin"
	CommandApprovalHoldResolve CommandApprovalHoldAction = "resolve"
)

type CommandApprovalHoldRequest struct {
	Action              CommandApprovalHoldAction `json:"action"`
	HoldID              string                    `json:"hold_id,omitempty"`
	SessionID           string                    `json:"session_id,omitempty"`
	DAGID               string                    `json:"dag_id,omitempty"`
	NodeID              string                    `json:"node_id,omitempty"`
	TaskID              string                    `json:"task_id,omitempty"`
	PipelineID          string                    `json:"pipeline_id,omitempty"`
	SourceAgentID       string                    `json:"source_agent_id,omitempty"`
	SourceAgentType     string                    `json:"source_agent_type,omitempty"`
	SourceAgentName     string                    `json:"source_agent_name,omitempty"`
	ApprovalCorrelation string                    `json:"approval_correlation_id,omitempty"`
	ToolName            string                    `json:"tool_name,omitempty"`
	Command             string                    `json:"command,omitempty"`
	RequestedAt         time.Time                 `json:"requested_at,omitempty"`
}

type CommandApprovalHoldResult struct {
	Accepted   bool      `json:"accepted"`
	HoldID     string    `json:"hold_id,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
	DAGID      string    `json:"dag_id,omitempty"`
	NodeID     string    `json:"node_id,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
	PipelineID string    `json:"pipeline_id,omitempty"`
	State      string    `json:"state,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}
