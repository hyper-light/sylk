// Architect-side callers for the orchestrator's cancel_orchestration
// and resume_orchestration verbs. Mirrors plan_state_query.go in
// shape — bus-routed, decode-tolerant, best-effort logging.
//
// These callers exist so state-aware verdict routing can speak in
// the right verb ("resume this stalled plan" vs "dispatch this new
// plan") rather than overloading dispatch for every case.
package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

// orchestrationLifecycleTimeout bounds how long the architect waits
// for the orchestrator's reply when invoking a cancel/resume verb.
// 5 seconds — these are administrative ops (DB write + dispatch);
// they should return quickly. Past the timeout the architect treats
// the call as failed and surfaces the error.
const orchestrationLifecycleTimeout = 5 * time.Second

const (
	cancelOrchestrationSkillName = "cancel_orchestration"
	resumeOrchestrationSkillName = "resume_orchestration"
)

// orchestratorCancelResult mirrors orchestrator.cancelOrchestrationResult.
// Defined here as a separate type to avoid an import cycle.
type orchestratorCancelResult struct {
	PlanID            string `json:"plan_id"`
	Cancelled         bool   `json:"cancelled"`
	DAGID             string `json:"dag_id,omitempty"`
	PriorState        string `json:"prior_state,omitempty"`
	NoActiveExecution bool   `json:"no_active_execution"`
	Reason            string `json:"reason,omitempty"`
}

// orchestratorResumeResult mirrors orchestrator.resumeOrchestrationResult.
type orchestratorResumeResult struct {
	PlanID     string `json:"plan_id"`
	Action     string `json:"action"`
	NewDAGID   string `json:"new_dag_id,omitempty"`
	PriorDAG   string `json:"prior_dag,omitempty"`
	PriorState string `json:"prior_state,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// cancelOrchestrationViaOrchestrator publishes a cancel_orchestration
// route request and decodes the reply. Returns nil + error if the
// orchestrator is unreachable or the call fails — unlike
// queryOrchestratorPlanState (which silently degrades), cancel is a
// state-changing action where caller MUST know if it failed.
func (a *Architect) cancelOrchestrationViaOrchestrator(ctx context.Context, planID, reason string) (*orchestratorCancelResult, error) {
	if a == nil {
		return nil, fmt.Errorf("architect is nil")
	}
	if a.bus == nil || !a.running {
		return nil, fmt.Errorf("architect bus is unavailable for cancel_orchestration")
	}
	planID = strings.TrimSpace(planID)
	reason = strings.TrimSpace(reason)
	if planID == "" {
		return nil, fmt.Errorf("cancel_orchestration: plan_id is required")
	}
	if reason == "" {
		return nil, fmt.Errorf("cancel_orchestration: reason is required")
	}
	orchID := a.knownAgentIDByType("orchestrator", "")
	if orchID == "" {
		return nil, fmt.Errorf("cancel_orchestration: no orchestrator registered")
	}

	callCtx, cancel := context.WithTimeout(ctx, orchestrationLifecycleTimeout)
	defer cancel()

	payload, err := json.Marshal(map[string]string{
		"plan_id": planID,
		"reason":  reason,
	})
	if err != nil {
		return nil, fmt.Errorf("encode cancel_orchestration payload: %w", err)
	}

	correlationID := "cancel_orch_" + uuid.NewString()
	req := &guide.RouteRequest{
		CorrelationID: correlationID,
		Input:         string(payload),
		TargetAgentID: orchID,
		Metadata: map[string]any{
			"direct_skill": cancelOrchestrationSkillName,
			"plan_id":      planID,
		},
	}
	resp, err := a.requestRouteSync(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("cancel_orchestration: route_sync: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("cancel_orchestration: nil response")
	}
	return decodeOrchestratorCancelResult(resp)
}

// resumeOrchestrationViaOrchestrator publishes a resume_orchestration
// route request and decodes the reply. Like cancel, returns explicit
// error on failure — resume is a state-changing action.
func (a *Architect) resumeOrchestrationViaOrchestrator(ctx context.Context, planID, mode, reason string) (*orchestratorResumeResult, error) {
	if a == nil {
		return nil, fmt.Errorf("architect is nil")
	}
	if a.bus == nil || !a.running {
		return nil, fmt.Errorf("architect bus is unavailable for resume_orchestration")
	}
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return nil, fmt.Errorf("resume_orchestration: plan_id is required")
	}
	orchID := a.knownAgentIDByType("orchestrator", "")
	if orchID == "" {
		return nil, fmt.Errorf("resume_orchestration: no orchestrator registered")
	}

	callCtx, cancel := context.WithTimeout(ctx, orchestrationLifecycleTimeout)
	defer cancel()

	body := map[string]string{
		"plan_id": planID,
	}
	if mode != "" {
		body["mode"] = mode
	}
	if reason != "" {
		body["reason"] = reason
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode resume_orchestration payload: %w", err)
	}

	correlationID := "resume_orch_" + uuid.NewString()
	req := &guide.RouteRequest{
		CorrelationID: correlationID,
		Input:         string(payload),
		TargetAgentID: orchID,
		Metadata: map[string]any{
			"direct_skill": resumeOrchestrationSkillName,
			"plan_id":      planID,
		},
	}
	resp, err := a.requestRouteSync(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("resume_orchestration: route_sync: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("resume_orchestration: nil response")
	}
	return decodeOrchestratorResumeResult(resp)
}

func decodeOrchestratorCancelResult(msg *guide.Message) (*orchestratorCancelResult, error) {
	resp, _, err := routeResponseFromMessage(msg)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("orchestrator cancel_orchestration failed: %s", resp.Error)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("orchestrator cancel_orchestration returned nil data")
	}
	switch typed := resp.Data.(type) {
	case *orchestratorCancelResult:
		return typed, nil
	case orchestratorCancelResult:
		return &typed, nil
	case map[string]any:
		raw, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		var result orchestratorCancelResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		return &result, nil
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("encode cancel result for fallback decode: %w", err)
	}
	var result orchestratorCancelResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode cancel result: %w", err)
	}
	return &result, nil
}

func decodeOrchestratorResumeResult(msg *guide.Message) (*orchestratorResumeResult, error) {
	resp, _, err := routeResponseFromMessage(msg)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("orchestrator resume_orchestration failed: %s", resp.Error)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("orchestrator resume_orchestration returned nil data")
	}
	switch typed := resp.Data.(type) {
	case *orchestratorResumeResult:
		return typed, nil
	case orchestratorResumeResult:
		return &typed, nil
	case map[string]any:
		raw, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		var result orchestratorResumeResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		return &result, nil
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("encode resume result for fallback decode: %w", err)
	}
	var result orchestratorResumeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode resume result: %w", err)
	}
	return &result, nil
}
