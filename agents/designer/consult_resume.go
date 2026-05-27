package designer

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

// resumeContinuation is the designer's ResumeFn for the consult
// continuation framework. The continuation store invokes it from a
// tracked goroutine when all consults awaited by a yielded LLM turn
// have resolved.
func (d *Designer) resumeContinuation(
	ctx context.Context,
	snapshot *shared.TurnSnapshot,
	results map[string]*shared.AwaitedClaimResult,
) error {
	if snapshot == nil || snapshot.Request == nil {
		return fmt.Errorf("designer: nil consult-yield snapshot on resume")
	}

	if snapshot.AccumulatorState.AgentID != "" {
		acc := shared.RestoreAccumulatorFromSnapshot(snapshot.AccumulatorState)
		ctx = claims.WithTestamentAccumulator(ctx, acc)
		defer acc.Flush(ctx, d.designerBoard(), d.designerScope())
	}

	shared.RecordResumeReceiving(ctx, d.designerBoard(), snapshot, results)

	req := snapshot.Request
	toolResult := formatDesignerAwaitResults(results)
	shared.CompleteYieldedToolFromContinuation(ctx, snapshot, results, toolResult)
	req.Messages = append(req.Messages, providers.Message{
		Role:       providers.RoleTool,
		ToolCallID: snapshot.AwaitToolCallID,
		ToolName:   snapshot.AwaitToolName,
		Content:    toolResult,
	})

	ctx = shared.WithContinuationStore(ctx, d.continuationStore)
	ctx = shared.WithTurnContext(ctx, &shared.TurnContext{
		Request:       req,
		CorrelationID: snapshot.CorrelationID,
		AgentID:       d.id,
		SessionID:     d.config.SessionID,
	})

	ledger := steering.NewSteeringLedger(snapshot.CorrelationID, d.id, d.config.SessionID, nil, nil)

	_, err := shared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return d.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		if shared.IsConsultYielded(err) {
			slog.Info("designer_resume_yielded_again",
				"agent_id", d.id, "correlation_id", snapshot.CorrelationID,
			)
			return nil
		}
		slog.Error("designer_resume_failed",
			"agent_id", d.id, "correlation_id", snapshot.CorrelationID,
			"error", err.Error(),
		)
		return err
	}
	return nil
}

func designerLedgerCorrelation(ledger *steering.SteeringLedger, sessionID string) string {
	if ledger != nil && ledger.CorrelationID != "" {
		return ledger.CorrelationID
	}
	if sessionID != "" {
		return "session:" + sessionID
	}
	return "designer:turn"
}

func formatDesignerAwaitResults(results map[string]*shared.AwaitedClaimResult) string {
	formatted := shared.FormatConsultResults(results)
	encoded, err := json.Marshal(formatted)
	if err != nil {
		return fmt.Sprintf(`{"error":"await_consults result encoding failed: %s"}`, err.Error())
	}
	return string(encoded)
}
