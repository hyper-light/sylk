package architect

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

// resumeContinuation is the architect's ResumeFn for the consult
// continuation framework. The continuation store invokes it from a
// tracked goroutine when all consults awaited by a yielded LLM turn
// have resolved. It restores the snapshot's turn state, injects the
// await_consults tool result message, and re-enters the architect's
// tool loop via ExecuteTurnLoop.
//
// Distinct from the architect's plan-handoff continuations
// (continuations.go) which are durable, plan-state, non-LLM-yield
// continuations. Same domain word ("continuation") but different
// concern: this is mid-LLM-turn parking; that is plan-acceptance
// state-machine waiting.
func (a *Architect) resumeContinuation(
	ctx context.Context,
	snapshot *shared.TurnSnapshot,
	results map[string]*claims.ConsultResolvedDelta,
) error {
	if snapshot == nil || snapshot.Request == nil {
		return fmt.Errorf("architect: nil consult-yield snapshot on resume")
	}

	// Restore the in-flight accumulator so the resumed turn keeps
	// producing into the same testament.
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
		defer acc.Flush(ctx, a.architectBoard(), a.architectScope())
	}

	// Push: narrate the Receiving transition on the architect's
	// claim. Surfaces "Received from <peer>" on the UI row before
	// the resumed LLM turn produces visible output. docs/CLAIMS_UI.md.
	shared.RecordResumeReceiving(ctx, a.architectBoard(), snapshot, results)

	// Inject the await_consults tool result into req.Messages so the
	// LLM's next inference reads "your await_consults call returned
	// these results" and proceeds.
	req := snapshot.Request
	toolResult := formatAwaitResultsContent(results)
	shared.CompleteYieldedToolFromContinuation(ctx, snapshot, results, toolResult)
	a.runContinuationPostToolHooks(ctx, snapshot, toolResult)
	req.Messages = append(req.Messages, providers.Message{
		Role:       providers.RoleTool,
		ToolCallID: snapshot.AwaitToolCallID,
		ToolName:   snapshot.AwaitToolName,
		Content:    toolResult,
	})

	// Re-stamp ctx so any nested consult_peer in the resumed turn
	// stays in ticket mode rather than degrading to legacy sync.
	ctx = shared.WithContinuationStore(ctx, a.continuationStore)
	ctx = shared.WithTurnContext(ctx, &shared.TurnContext{
		Request:       req,
		CorrelationID: snapshot.CorrelationID,
		AgentID:       a.id,
		SessionID:     a.config.SessionID,
	})

	ledger := steering.NewSteeringLedger(snapshot.CorrelationID, a.id, a.config.SessionID, nil, nil)

	_, err := shared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return a.executeToolLoop(ctx, req, "resume", func(string) {}, ledger)
	})
	if err != nil {
		// A second yield is legitimate — the resumed turn issued
		// more consult_peers and yielded again. The framework has
		// already persisted the new continuation; no error to
		// propagate here.
		if shared.IsConsultYielded(err) {
			slog.Info("architect_resume_yielded_again",
				"agent_id", a.id, "correlation_id", snapshot.CorrelationID,
			)
			return nil
		}
		slog.Error("architect_resume_failed",
			"agent_id", a.id, "correlation_id", snapshot.CorrelationID,
			"error", err.Error(),
		)
		return err
	}
	return nil
}

// ledgerCorrelationOrDefault extracts the steering ledger's
// correlation ID, falling back to the session ID prefixed for
// readability when no ledger is on context. Used to populate
// TurnContext.CorrelationID at turn-stamp time.
func ledgerCorrelationOrDefault(ledger *steering.SteeringLedger, sessionID string) string {
	if ledger != nil && ledger.CorrelationID != "" {
		return ledger.CorrelationID
	}
	if sessionID != "" {
		return "session:" + sessionID
	}
	return "architect:turn"
}

// formatAwaitResultsContent serializes resolved consults into the
// JSON payload the LLM expects from await_consults — same shape the
// fast path returns, so the LLM cannot tell whether the await
// resolved inline or after a yield.
func formatAwaitResultsContent(results map[string]*claims.ConsultResolvedDelta) string {
	formatted := shared.FormatConsultResults(results)
	encoded, err := json.Marshal(formatted)
	if err != nil {
		return fmt.Sprintf(`{"error":"await_consults result encoding failed: %s"}`, err.Error())
	}
	return string(encoded)
}
