package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/steering"
)

// resumeContinuation is the orchestrator's ResumeFn for the consult
// continuation framework. The continuation store invokes it from a
// tracked goroutine when all consults awaited by a yielded LLM turn
// have resolved. It restores the snapshot's turn state, injects the
// await_consults tool result message, and re-enters the orchestrator's
// tool loop via ExecuteTurnLoop.
func (o *Orchestrator) resumeContinuation(
	ctx context.Context,
	snapshot *shared.TurnSnapshot,
	results map[string]*claims.ConsultResolvedDelta,
) error {
	if snapshot == nil || snapshot.Request == nil {
		return fmt.Errorf("orchestrator: nil consult-yield snapshot on resume")
	}

	if snapshot.AccumulatorState.AgentID != "" {
		acc := claims.RestoreAccumulator(
			snapshot.AccumulatorState.AgentID,
			snapshot.AccumulatorState.SessionID,
			snapshot.AccumulatorState.ClaimID,
			snapshot.AccumulatorState.Started,
			snapshot.AccumulatorState.Artifacts,
			snapshot.AccumulatorState.Notes,
		)
		ctx = claims.WithTestamentAccumulator(ctx, acc)
		defer acc.Flush(ctx, o.orchestratorBoardOrNil(), o.orchestratorScope())
	}

	shared.RecordResumeReceiving(ctx, o.orchestratorBoardOrNil(), snapshot, results)

	req := snapshot.Request
	req.Messages = append(req.Messages, providers.Message{
		Role:       providers.RoleTool,
		ToolCallID: snapshot.AwaitToolCallID,
		ToolName:   snapshot.AwaitToolName,
		Content:    formatOrchestratorAwaitResults(results),
	})

	ctx = shared.WithContinuationStore(ctx, o.continuationStore)
	ctx = shared.WithTurnContext(ctx, &shared.TurnContext{
		Request:       req,
		CorrelationID: snapshot.CorrelationID,
		AgentID:       o.config.AgentID,
		SessionID:     o.SessionID(),
	})

	ledger := steering.NewSteeringLedger(snapshot.CorrelationID, o.config.AgentID, o.SessionID(), nil, nil)

	_, err := shared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return o.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		if shared.IsConsultYielded(err) {
			slog.Info("orchestrator_resume_yielded_again",
				"agent_id", o.config.AgentID, "correlation_id", snapshot.CorrelationID,
			)
			return nil
		}
		slog.Error("orchestrator_resume_failed",
			"agent_id", o.config.AgentID, "correlation_id", snapshot.CorrelationID,
			"error", err.Error(),
		)
		return err
	}
	return nil
}

func orchestratorLedgerCorrelation(ledger *steering.SteeringLedger, sessionID string) string {
	if ledger != nil && ledger.CorrelationID != "" {
		return ledger.CorrelationID
	}
	if sessionID != "" {
		return "session:" + sessionID
	}
	return "orchestrator:turn"
}

func formatOrchestratorAwaitResults(results map[string]*claims.ConsultResolvedDelta) string {
	formatted := shared.FormatConsultResults(results)
	encoded, err := json.Marshal(formatted)
	if err != nil {
		return fmt.Sprintf(`{"error":"await_consults result encoding failed: %s"}`, err.Error())
	}
	return string(encoded)
}
