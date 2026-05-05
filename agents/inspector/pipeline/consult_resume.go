package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/steering"
)

// resumeContinuation is the pipeline inspector's ResumeFn for the
// consult continuation framework. The continuation store invokes it
// from a tracked goroutine when all consults awaited by a yielded
// LLM turn have resolved.
func (pi *PipelineInspector) resumeContinuation(
	ctx context.Context,
	snapshot *agentShared.TurnSnapshot,
	results map[string]*claims.ConsultResolvedDelta,
) error {
	if snapshot == nil || snapshot.Request == nil {
		return fmt.Errorf("pipeline_inspector: nil consult-yield snapshot on resume")
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
		defer acc.Flush(ctx, pi.claimsBoard, nil)
	}

	agentShared.RecordResumeReceiving(ctx, pi.claimsBoard, snapshot, results)

	req := snapshot.Request
	req.Messages = append(req.Messages, providers.Message{
		Role:       providers.RoleTool,
		ToolCallID: snapshot.AwaitToolCallID,
		ToolName:   snapshot.AwaitToolName,
		Content:    formatPipelineInspectorAwaitResults(results),
	})

	ctx = agentShared.WithContinuationStore(ctx, pi.continuationStore)
	ctx = agentShared.WithTurnContext(ctx, &agentShared.TurnContext{
		Request:       req,
		CorrelationID: snapshot.CorrelationID,
		AgentID:       pi.id,
		SessionID:     pi.config.SessionID,
	})

	ledger := steering.NewSteeringLedger(snapshot.CorrelationID, pi.id, pi.config.SessionID, nil, nil)

	_, err := agentShared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return pi.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		if agentShared.IsConsultYielded(err) {
			slog.Info("pipeline_inspector_resume_yielded_again",
				"agent_id", pi.id, "correlation_id", snapshot.CorrelationID,
			)
			return nil
		}
		slog.Error("pipeline_inspector_resume_failed",
			"agent_id", pi.id, "correlation_id", snapshot.CorrelationID,
			"error", err.Error(),
		)
		return err
	}
	return nil
}

func pipelineLedgerCorrelation(ledger *steering.SteeringLedger, sessionID string) string {
	if ledger != nil && ledger.CorrelationID != "" {
		return ledger.CorrelationID
	}
	if sessionID != "" {
		return "session:" + sessionID
	}
	return "pipeline_inspector:turn"
}

func formatPipelineInspectorAwaitResults(results map[string]*claims.ConsultResolvedDelta) string {
	formatted := agentShared.FormatConsultResults(results)
	encoded, err := json.Marshal(formatted)
	if err != nil {
		return fmt.Sprintf(`{"error":"await_consults result encoding failed: %s"}`, err.Error())
	}
	return string(encoded)
}
