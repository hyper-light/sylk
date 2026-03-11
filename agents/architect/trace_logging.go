package architect

import (
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
)

func (a *Architect) logTrace(event, level, sessionID, corrID string, eventCode agentlog.EventType, data map[string]any) {
	if a == nil || a.steering == nil {
		return
	}
	el := a.steering.EventLogger()
	if el == nil {
		return
	}
	el.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     level,
		Agent:     a.id,
		SessionID: sessionID,
		Event:     event,
		EventCode: eventCode,
		CorrID:    corrID,
		Data:      data,
	})
}

func (a *Architect) completeContinuationBestEffort(
	record *ArchitectContinuation,
	state continuationStatus,
	responseJSON string,
	errorText string,
	reason string,
) {
	if a == nil || a.controlStore == nil || record == nil {
		return
	}
	if err := a.controlStore.CompleteContinuation(record, state, responseJSON, errorText); err != nil {
		a.logTrace("architect_continuation_complete_failed", "error", record.SessionID, record.ResponseCorrelationID, agentlog.EventError, map[string]any{
			"continuation_id":     record.ID,
			"kind":                string(record.Kind),
			"plan_id":             record.PlanID,
			"target_agent_id":     record.TargetAgentID,
			"requested_state":     string(state),
			"reason":              reason,
			"response_error_text": errorText,
			"store_error":         err.Error(),
		})
	}
}

func (a *Architect) clearPlanPendingContinuationBestEffort(plan *DesignPlan, correlationID string, reason string) {
	if a == nil || plan == nil {
		return
	}
	if err := a.clearPlanPendingContinuation(plan, correlationID); err != nil {
		a.logTrace("architect_plan_pending_clear_failed", "error", plan.SessionID, correlationID, agentlog.EventError, map[string]any{
			"plan_id":     plan.ID,
			"plan_status": plan.Status.String(),
			"reason":      reason,
			"error":       err.Error(),
		})
	}
}

func (a *Architect) persistPlanStateBestEffort(plan *DesignPlan, correlationID string, reason string) {
	if a == nil || plan == nil {
		return
	}
	if err := a.persistPlanState(plan); err != nil {
		a.logTrace("architect_plan_persist_failed", "error", plan.SessionID, correlationID, agentlog.EventError, map[string]any{
			"plan_id":     plan.ID,
			"plan_status": plan.Status.String(),
			"plan_epoch":  plan.Epoch,
			"reason":      reason,
			"error":       err.Error(),
		})
	}
}
