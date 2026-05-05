package guardian

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

// resumeContinuation is the guardian's ResumeFn for the consult
// continuation framework. The continuation store invokes it from a
// tracked goroutine when all consults awaited by a yielded LLM turn
// have resolved.
func (g *Guardian) resumeContinuation(
	ctx context.Context,
	snapshot *shared.TurnSnapshot,
	results map[string]*claims.ConsultResolvedDelta,
) error {
	if snapshot == nil || snapshot.Request == nil {
		return fmt.Errorf("guardian: nil consult-yield snapshot on resume")
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
		defer acc.Flush(ctx, g.guardianBoard(), nil)
	}

	shared.RecordResumeReceiving(ctx, g.guardianBoard(), snapshot, results)

	req := snapshot.Request
	req.Messages = append(req.Messages, providers.Message{
		Role:       providers.RoleTool,
		ToolCallID: snapshot.AwaitToolCallID,
		ToolName:   snapshot.AwaitToolName,
		Content:    formatGuardianAwaitResults(results),
	})

	ctx = shared.WithContinuationStore(ctx, g.continuationStore)
	ctx = shared.WithTurnContext(ctx, &shared.TurnContext{
		Request:       req,
		CorrelationID: snapshot.CorrelationID,
		AgentID:       g.id,
		SessionID:     g.activeSessionID,
	})

	ledger := steering.NewSteeringLedger(snapshot.CorrelationID, g.id, g.activeSessionID, nil, nil)

	_, err := shared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		content, _, loopErr := g.executeToolLoop(ctx, req, "resume", func(string) {}, ledger)
		return content, loopErr
	})
	if err != nil {
		if shared.IsConsultYielded(err) {
			slog.Info("guardian_resume_yielded_again",
				"agent_id", g.id, "correlation_id", snapshot.CorrelationID,
			)
			return nil
		}
		slog.Error("guardian_resume_failed",
			"agent_id", g.id, "correlation_id", snapshot.CorrelationID,
			"error", err.Error(),
		)
		return err
	}
	return nil
}

func guardianLedgerCorrelation(ledger *steering.SteeringLedger, sessionID string) string {
	if ledger != nil && ledger.CorrelationID != "" {
		return ledger.CorrelationID
	}
	if sessionID != "" {
		return "session:" + sessionID
	}
	return "guardian:turn"
}

func formatGuardianAwaitResults(results map[string]*claims.ConsultResolvedDelta) string {
	formatted := shared.FormatConsultResults(results)
	encoded, err := json.Marshal(formatted)
	if err != nil {
		return fmt.Sprintf(`{"error":"await_consults result encoding failed: %s"}`, err.Error())
	}
	return string(encoded)
}
