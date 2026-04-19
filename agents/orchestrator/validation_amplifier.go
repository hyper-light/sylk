package orchestrator

import (
	"context"
	"encoding/json"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// Activity Fabric validation amplifier (Tier 2). Validation epochs,
// execution holds, and remediation cases stay sovereign in their own
// SQLite tables; the amplifier emits typed projections after each
// successful commit. The fabric activity carries SourceTable + SourceID
// for back-reference.

func emitValidationStarted(ctx context.Context, record *ValidationEpochRecord) {
	if record == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"plan_id":      record.PlanID,
		"workflow_ids": record.WorkflowIDs,
		"dag_ids":      record.DAGIDs,
		"task_ids":     record.TaskIDs,
		"summary":      record.Summary,
		"reason":       record.Reason,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(record.SessionID),
		Timestamp:  record.CreatedAt,
		Resolution: activity.ResolutionFor(activity.ActionValidationStarted),
		Action:     activity.ActionValidationStarted,
		Actor: activity.Actor{
			AgentID:   "orchestrator",
			AgentType: "orchestrator",
		},
		Subject: activity.Subject{
			TargetArtifact: record.EpochID,
		},
		Payload:     payload,
		State:       activity.StateInFlight,
		SourceTable: "validation_epochs",
		SourceID:    record.EpochID,
	}
	activity.Append(ctx, act)
}

func emitValidationCompleted(ctx context.Context, record *ValidationEpochRecord) {
	if record == nil {
		return
	}
	kind := activity.ActionValidationAccepted
	state := activity.StateResolved
	switch record.Status {
	case ValidationEpochStatusFailedBlocking, ValidationEpochStatusFailedRetryable:
		kind = activity.ActionValidationRejected
	case ValidationEpochStatusPassed, ValidationEpochStatusReadyToFlush, ValidationEpochStatusFlushed:
		kind = activity.ActionValidationAccepted
	}
	payload, _ := json.Marshal(map[string]any{
		"status":  string(record.Status),
		"summary": record.Summary,
		"verdict": record.ValidatorVerdict,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(record.SessionID),
		Timestamp:  record.UpdatedAt,
		Resolution: activity.ResolutionFor(kind),
		Action:     kind,
		Actor: activity.Actor{
			AgentID:   "orchestrator",
			AgentType: "orchestrator",
		},
		Subject: activity.Subject{
			TargetArtifact: record.EpochID,
		},
		Payload:     payload,
		State:       state,
		SourceTable: "validation_epochs",
		SourceID:    record.EpochID,
	}
	activity.Append(ctx, act)
}

func emitHoldAcquired(ctx context.Context, record *ExecutionHoldRecord) {
	if record == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"epoch_id":             record.EpochID,
		"remediation_case_id":  record.RemediationCaseID,
		"created_by_agent_id":  record.CreatedByAgentID,
		"created_by_type":      record.CreatedByAgentType,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(record.SessionID),
		Timestamp:  record.CreatedAt,
		Resolution: activity.ResolutionFor(activity.ActionHoldAcquired),
		Action:     activity.ActionHoldAcquired,
		Actor: activity.Actor{
			AgentID:   record.CreatedByAgentID,
			AgentType: record.CreatedByAgentType,
		},
		Subject: activity.Subject{
			TargetArtifact: record.HoldID,
		},
		Payload:     payload,
		State:       activity.StateInFlight,
		SourceTable: "execution_holds",
		SourceID:    record.HoldID,
	}
	activity.Append(ctx, act)
}

func emitHoldReleased(ctx context.Context, record *ExecutionHoldRecord) {
	if record == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"status":     string(record.Status),
		"released_at": record.ReleasedAt,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(record.SessionID),
		Timestamp:  record.UpdatedAt,
		Resolution: activity.ResolutionFor(activity.ActionHoldReleased),
		Action:     activity.ActionHoldReleased,
		Actor: activity.Actor{
			AgentID:   record.CreatedByAgentID,
			AgentType: record.CreatedByAgentType,
		},
		Subject: activity.Subject{
			TargetArtifact: record.HoldID,
		},
		Payload:     payload,
		State:       activity.StateResolved,
		SourceTable: "execution_holds",
		SourceID:    record.HoldID,
	}
	activity.Append(ctx, act)
}

func emitRemediationOpened(ctx context.Context, record *RemediationCaseRecord) {
	if record == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"epoch_id": record.EpochID,
		"hold_id":  record.HoldID,
		"request":  record.Request,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(record.SessionID),
		Timestamp:  record.CreatedAt,
		Resolution: activity.ResolutionFor(activity.ActionRemediationOpened),
		Action:     activity.ActionRemediationOpened,
		Actor: activity.Actor{
			AgentID:   "orchestrator",
			AgentType: "orchestrator",
		},
		Subject: activity.Subject{
			TargetArtifact: record.CaseID,
		},
		Payload:     payload,
		State:       activity.StateInFlight,
		SourceTable: "remediation_cases",
		SourceID:    record.CaseID,
	}
	activity.Append(ctx, act)
}

func emitRemediationResolved(ctx context.Context, record *RemediationCaseRecord) {
	if record == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"status": string(record.Status),
		"result": record.Result,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(record.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionRemediationResolved),
		Action:     activity.ActionRemediationResolved,
		Actor: activity.Actor{
			AgentID:   "orchestrator",
			AgentType: "orchestrator",
		},
		Subject: activity.Subject{
			TargetArtifact: record.CaseID,
		},
		Payload:     payload,
		State:       activity.StateResolved,
		SourceTable: "remediation_cases",
		SourceID:    record.CaseID,
	}
	activity.Append(ctx, act)
}
