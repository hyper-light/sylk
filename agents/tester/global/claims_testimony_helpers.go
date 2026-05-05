package global

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	testerShared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
)

// globalTesterBoard resolves the claims board for the session.
func (gt *GlobalTester) globalTesterBoard() *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(gt.config.SessionID)
}

// globalTesterScope adapts *concurrency.GoroutineScope to claims.ScopeProvider.
func (gt *GlobalTester) globalTesterScope() claims.ScopeProvider {
	if gt.scope == nil {
		return nil
	}
	return &globalTesterScopeAdapter{scope: gt.scope}
}

type globalTesterScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

func (a *globalTesterScopeAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return a.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

// globalTesterSubmitTestament submits a testament async via scope. Best-effort.
func (gt *GlobalTester) globalTesterSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := gt.globalTesterBoard()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "tester", Type: claims.ActionTypeTestament}
	if gt.scope != nil {
		if err := gt.scope.Go("global_tester_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("global_tester_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("global tester testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("global_tester_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("global tester testament: " + err.Error())
	}
}

// globalTesterTestament builds a testament.
func (gt *GlobalTester) globalTesterTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID: "tester", SessionID: gt.config.SessionID,
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "tester", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// globalTesterArtifact builds a single artifact.
func (gt *GlobalTester) globalTesterArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID: "tester", SessionID: gt.config.SessionID,
		Kind: kind, Reference: reference,
	}
}

func (gt *GlobalTester) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	p := gt.getProvider()
	if p == nil {
		return fmt.Errorf("tester: LLM provider not configured")
	}

	acc := agentshared.NewClaimsEntryAccumulator("tester", gt.config.SessionID, entry)
	acc.WithBoard(gt.globalTesterBoard())
	defer acc.Flush(ctx, gt.globalTesterBoard(), gt.globalTesterScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	if entry.Node.Claim != nil {
		agentshared.RecordAgentState(ctx, gt.globalTesterBoard(), entry.Node.Claim.ID,
			"Acknowledging request", agentshared.AgentStateReasoning, nil)
	}

	userMessage := agentshared.ComposeClaimsEntryPrompt(entry)
	board := gt.globalTesterBoard()
	userMessage = claims.PrependBoardPreamble(userMessage, board, "tester")
	gt.prepareSkillsForInput(userMessage)

	systemPrompt := testerShared.GlobalTesterSystemPrompt()
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        gt.buildToolDefinitions(),
		Model:        gt.config.Model,
		MaxTokens:    gt.config.MaxTokens,
	}
	gt.applyLLMRuntimeProfile(req, "claims_entry")

	ledger := gt.steering.Create(entry.Delta.DeltaKey(), gt.id, gt.config.SessionID, nil, nil)
	defer gt.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	result, err := agentshared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return gt.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		slog.Error("tester_global_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}
