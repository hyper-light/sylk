package engineer

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

// resumeContinuation is the engineer's ResumeFn for the consult
// continuation framework. The continuation store invokes it from a
// tracked goroutine when all consults awaited by a yielded LLM turn
// have resolved. It restores the snapshot's turn state, injects the
// await_consults tool result message, and re-enters the engineer's
// tool loop via ExecuteTurnLoop.
func (e *Engineer) resumeContinuation(
	ctx context.Context,
	snapshot *shared.TurnSnapshot,
	results map[string]*claims.ConsultResolvedDelta,
) error {
	if snapshot == nil || snapshot.Request == nil {
		return fmt.Errorf("engineer: nil consult-yield snapshot on resume")
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
		defer acc.Flush(ctx, e.engineerBoard(), e.engineerScope())
	}

	shared.RecordResumeReceiving(ctx, e.engineerBoard(), snapshot, results)

	req := snapshot.Request
	toolResult := formatEngineerAwaitResults(results)
	shared.CompleteYieldedToolFromContinuation(ctx, snapshot, results, toolResult)
	req.Messages = append(req.Messages, providers.Message{
		Role:       providers.RoleTool,
		ToolCallID: snapshot.AwaitToolCallID,
		ToolName:   snapshot.AwaitToolName,
		Content:    toolResult,
	})

	ctx = shared.WithContinuationStore(ctx, e.continuationStore)
	ctx = shared.WithTurnContext(ctx, &shared.TurnContext{
		Request:       req,
		CorrelationID: snapshot.CorrelationID,
		AgentID:       e.id,
		SessionID:     e.config.SessionID,
	})

	ledger := steering.NewSteeringLedger(snapshot.CorrelationID, e.id, e.config.SessionID, nil, nil)

	_, err := shared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return e.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		if shared.IsConsultYielded(err) {
			slog.Info("engineer_resume_yielded_again",
				"agent_id", e.id, "correlation_id", snapshot.CorrelationID,
			)
			return nil
		}
		slog.Error("engineer_resume_failed",
			"agent_id", e.id, "correlation_id", snapshot.CorrelationID,
			"error", err.Error(),
		)
		return err
	}
	return nil
}

// engineerLedgerCorrelation extracts a stable correlation id for
// turn-context stamping; mirrors architect's helper.
func engineerLedgerCorrelation(ledger *steering.SteeringLedger, sessionID string) string {
	if ledger != nil && ledger.CorrelationID != "" {
		return ledger.CorrelationID
	}
	if sessionID != "" {
		return "session:" + sessionID
	}
	return "engineer:turn"
}

func formatEngineerAwaitResults(results map[string]*claims.ConsultResolvedDelta) string {
	formatted := shared.FormatConsultResults(results)
	encoded, err := json.Marshal(formatted)
	if err != nil {
		return fmt.Sprintf(`{"error":"await_consults result encoding failed: %s"}`, err.Error())
	}
	return string(encoded)
}
