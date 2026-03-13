package shared

import "time"

const (
	ControlPlaneKindPlanHandoffStatus        = "plan_handoff_status"
	ControlPlaneKindPlanHandoffReceiptUpdate = "plan_handoff_receipt_update"
	ControlPlaneKindPlanHandoffReceiptResync = "plan_handoff_receipt_resync"
)

type PlanHandoffReceiptStatus string

const (
	PlanHandoffReceiptStatusAccepted  PlanHandoffReceiptStatus = "accepted"
	PlanHandoffReceiptStatusDAGBuilt  PlanHandoffReceiptStatus = "dag_built"
	PlanHandoffReceiptStatusSubmitted PlanHandoffReceiptStatus = "submitted"
	PlanHandoffReceiptStatusRunning   PlanHandoffReceiptStatus = "running"
	PlanHandoffReceiptStatusFailed    PlanHandoffReceiptStatus = "failed"
)

type PlanHandoffStatusRequest struct {
	SessionID string `json:"session_id"`
	PlanID    string `json:"plan_id"`
	Revision  int    `json:"revision"`
}

type PlanHandoffReceipt struct {
	ReceiptID   string                   `json:"receipt_id"`
	SessionID   string                   `json:"session_id"`
	PlanID      string                   `json:"plan_id"`
	Revision    int                      `json:"revision"`
	Attempt     int                      `json:"attempt"`
	Status      PlanHandoffReceiptStatus `json:"status"`
	WorkflowID  string                   `json:"workflow_id,omitempty"`
	DAGID       string                   `json:"dag_id,omitempty"`
	TaskCount   int                      `json:"task_count,omitempty"`
	LayerCount  int                      `json:"layer_count,omitempty"`
	ErrorText   string                   `json:"error_text,omitempty"`
	AcceptedAt  time.Time                `json:"accepted_at,omitempty"`
	DAGBuiltAt  time.Time                `json:"dag_built_at,omitempty"`
	SubmittedAt time.Time                `json:"submitted_at,omitempty"`
	RunningAt   time.Time                `json:"running_at,omitempty"`
	FailedAt    time.Time                `json:"failed_at,omitempty"`
	CreatedAt   time.Time                `json:"created_at,omitempty"`
	UpdatedAt   time.Time                `json:"updated_at,omitempty"`
}

type PlanHandoffStatusResponse struct {
	Found   bool                `json:"found"`
	Receipt *PlanHandoffReceipt `json:"receipt,omitempty"`
}

type PlanHandoffReceiptUpdate struct {
	SessionID     string              `json:"session_id,omitempty"`
	PlanID        string              `json:"plan_id,omitempty"`
	Revision      int                 `json:"revision,omitempty"`
	CorrelationID string              `json:"correlation_id,omitempty"`
	Found         bool                `json:"found"`
	Receipt       *PlanHandoffReceipt `json:"receipt,omitempty"`
	ErrorText     string              `json:"error_text,omitempty"`
	Reason        string              `json:"reason,omitempty"`
}

type PlanHandoffReceiptResyncRequest struct {
	SessionID     string `json:"session_id,omitempty"`
	PlanID        string `json:"plan_id,omitempty"`
	Revision      int    `json:"revision,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
}
