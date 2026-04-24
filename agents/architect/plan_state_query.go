// Architect-side caller for the orchestrator's plan_state_query skill.
//
// The freshness audit calls this synchronously to learn the
// orchestrator's authoritative state for a plan: is it none / queued /
// running / completed / failed / aborted / stalled? The response feeds
// the dialog's OrchestratorStateHint so the user sees both file drift
// AND execution state in the dialog body.
//
// Read-only and best-effort. If the orchestrator is unreachable or the
// query times out, the freshness audit proceeds without orchestrator
// context — better to show the dialog with file-drift signals only
// than to block on a missing orchestrator response.
package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/google/uuid"
)

// planStateQuerySkillName is the orchestrator skill the architect
// invokes via bus to ask "what's the actual execution state of this
// plan?" Kept as a const so architect's caller and orchestrator's
// registration stay in lock-step.
const planStateQuerySkillName = "plan_state_query"

// planStateQueryTimeout bounds how long the architect waits for the
// orchestrator's reply. Conservative — the query is a single indexed
// SELECT plus a short classification, so 3 seconds is plenty even on
// a busy orchestrator. If it expires we proceed without orchestrator
// context rather than blocking the dialog.
const planStateQueryTimeout = 3 * time.Second

// orchestratorPlanState is the architect-side mirror of the
// orchestrator's PlanExecutionState. Defined here as a separate type
// to avoid an architect→orchestrator import cycle; the JSON shape is
// identical so encoding round-trips cleanly.
type orchestratorPlanState struct {
	PlanID             string     `json:"plan_id"`
	Status             string     `json:"status"`
	DAGID              string     `json:"dag_id,omitempty"`
	SessionID          string     `json:"session_id,omitempty"`
	Name               string     `json:"name,omitempty"`
	NodesTotal         int        `json:"nodes_total"`
	NodesSucceeded     int        `json:"nodes_succeeded"`
	NodesFailed        int        `json:"nodes_failed"`
	NodesSkipped       int        `json:"nodes_skipped"`
	NodesRemaining     int        `json:"nodes_remaining"`
	CurrentLayer       int        `json:"current_layer,omitempty"`
	TotalLayers        int        `json:"total_layers,omitempty"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	LastObservedAt     *time.Time `json:"last_observed_at,omitempty"`
	StalledForSeconds  int64      `json:"stalled_for_seconds,omitempty"`
	Error              string     `json:"error,omitempty"`
	HistoricalDAGCount int        `json:"historical_dag_count"`
}

// queryOrchestratorPlanState publishes a plan_state_query route
// request to the orchestrator and decodes the reply. Returns nil
// (no error) when the orchestrator is unreachable or the response is
// not parseable — the freshness audit treats that as "no orchestrator
// context available" and continues with whatever signals it does have.
func (a *Architect) queryOrchestratorPlanState(ctx context.Context, planID string) *orchestratorPlanState {
	if a == nil || a.bus == nil || !a.running {
		return nil
	}
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return nil
	}
	orchID := a.knownAgentIDByType("orchestrator", "")
	if orchID == "" {
		return nil
	}

	queryCtx, cancel := context.WithTimeout(ctx, planStateQueryTimeout)
	defer cancel()

	payload, err := json.Marshal(map[string]string{"plan_id": planID})
	if err != nil {
		return nil
	}

	correlationID := "plan_state_" + uuid.NewString()
	req := &guide.RouteRequest{
		CorrelationID: correlationID,
		Input:         string(payload),
		TargetAgentID: orchID,
		Metadata: map[string]any{
			"direct_skill": planStateQuerySkillName,
			"plan_id":      planID,
		},
	}
	a.architectPostClaim(ctx,
		architectClaimAction(claims.ActionTypeConsultation),
		architectClaimWithSubject(
			"Query orchestrator plan state: "+truncateArchitectString(planID, 40),
			"Plan state query via orchestrator",
			"orchestrator",
			[]claims.ClaimScopeEntry{{Kind: "consultation", Key: "orchestrator"}},
			claims.ActionTypeConsultation,
			[]*claims.Validation{
				architectValidation(claims.ValidationTypeReceipt, false, "Orchestrator returns plan state", "state != nil"),
			},
		),
	)
	resp, err := a.requestRouteSync(queryCtx, req)
	if err != nil || resp == nil {
		architectDebugLog().Info("queryOrchestratorPlanState: ROUTE_SYNC_FAILED",
			"plan_id", planID,
			"orchestrator_id", orchID,
			"error", routeSyncErrorMessage(err))
		return nil
	}
	state, err := decodeOrchestratorPlanState(resp)
	if err != nil {
		architectDebugLog().Info("queryOrchestratorPlanState: DECODE_FAILED",
			"plan_id", planID,
			"error", err.Error())
		return nil
	}
	return state
}

// decodeOrchestratorPlanState unwraps the route response and decodes
// the orchestratorPlanState payload. Tolerant of multiple delivery
// shapes (RouteResponse.Data as map vs typed pointer) so different
// transport paths produce identical results.
func decodeOrchestratorPlanState(msg *guide.Message) (*orchestratorPlanState, error) {
	resp, _, err := routeResponseFromMessage(msg)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("orchestrator plan_state_query failed: %s", resp.Error)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("orchestrator plan_state_query returned nil data")
	}
	switch typed := resp.Data.(type) {
	case *orchestratorPlanState:
		return typed, nil
	case orchestratorPlanState:
		return &typed, nil
	case map[string]any:
		raw, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		var state orchestratorPlanState
		if err := json.Unmarshal(raw, &state); err != nil {
			return nil, err
		}
		return &state, nil
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("encode plan_state_query data for fallback decode: %w", err)
	}
	var state orchestratorPlanState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode plan_state_query data: %w", err)
	}
	return &state, nil
}

// routeSyncErrorMessage returns "" for nil errors so log fields stay
// clean. Trivial helper but avoids if-else clutter at call sites.
func routeSyncErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// orchestratorStateHint converts the structured plan state into a
// short string suitable for the proposal's OrchestratorStateHint
// field. Empty when state is nil (architect couldn't query) so the
// dialog falls back to file/age signals only without an empty hint
// label cluttering the UI.
func orchestratorStateHint(state *orchestratorPlanState) string {
	if state == nil {
		return ""
	}
	switch state.Status {
	case "running":
		if state.NodesTotal > 0 {
			return fmt.Sprintf("orchestrator: running (%d/%d nodes)", state.NodesSucceeded, state.NodesTotal)
		}
		return "orchestrator: running"
	case "stalled":
		return fmt.Sprintf("orchestrator: stalled (no progress for %ds)", state.StalledForSeconds)
	case "completed":
		return "orchestrator: completed"
	case "failed":
		return "orchestrator: failed"
	case "aborted":
		return "orchestrator: aborted"
	case "queued":
		return "orchestrator: queued"
	case "none":
		// "none" is the default — no DAG row exists. Surface only when
		// the architect's PendingWork suggested otherwise; otherwise
		// the absence of orchestrator context isn't meaningful.
		return ""
	default:
		return ""
	}
}
