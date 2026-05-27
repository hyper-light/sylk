package global

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

// resumeContinuation is the global inspector's ResumeFn for the
// consult continuation framework. The continuation store invokes it
// from a tracked goroutine when all consults awaited by a yielded
// LLM turn have resolved.
func (gi *GlobalInspector) resumeContinuation(
	ctx context.Context,
	snapshot *agentShared.TurnSnapshot,
	results map[string]*agentShared.AwaitedClaimResult,
) error {
	if snapshot == nil || snapshot.Request == nil {
		return fmt.Errorf("global_inspector: nil consult-yield snapshot on resume")
	}

	if snapshot.AccumulatorState.AgentID != "" {
		acc := agentShared.RestoreAccumulatorFromSnapshot(snapshot.AccumulatorState)
		ctx = claims.WithTestamentAccumulator(ctx, acc)
		defer acc.Flush(ctx, gi.globalInspectorBoardOrNil(), nil)
	}

	agentShared.RecordResumeReceiving(ctx, gi.globalInspectorBoardOrNil(), snapshot, results)

	req := snapshot.Request
	toolResult := formatGlobalInspectorAwaitResults(results)
	agentShared.CompleteYieldedToolFromContinuation(ctx, snapshot, results, toolResult)
	req.Messages = append(req.Messages, providers.Message{
		Role:       providers.RoleTool,
		ToolCallID: snapshot.AwaitToolCallID,
		ToolName:   snapshot.AwaitToolName,
		Content:    toolResult,
	})

	ctx = agentShared.WithContinuationStore(ctx, gi.continuationStore)
	ctx = agentShared.WithTurnContext(ctx, &agentShared.TurnContext{
		Request:       req,
		CorrelationID: snapshot.CorrelationID,
		AgentID:       gi.id,
		SessionID:     gi.config.SessionID,
	})

	ledger := steering.NewSteeringLedger(snapshot.CorrelationID, gi.id, gi.config.SessionID, nil, nil)

	_, err := agentShared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return gi.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		if agentShared.IsConsultYielded(err) {
			slog.Info("global_inspector_resume_yielded_again",
				"agent_id", gi.id, "correlation_id", snapshot.CorrelationID,
			)
			return nil
		}
		slog.Error("global_inspector_resume_failed",
			"agent_id", gi.id, "correlation_id", snapshot.CorrelationID,
			"error", err.Error(),
		)
		return err
	}
	return nil
}

func formatGlobalInspectorAwaitResults(results map[string]*agentShared.AwaitedClaimResult) string {
	formatted := agentShared.FormatConsultResults(results)
	encoded, err := json.Marshal(formatted)
	if err != nil {
		return fmt.Sprintf(`{"error":"await_consults result encoding failed: %s"}`, err.Error())
	}
	return string(encoded)
}
