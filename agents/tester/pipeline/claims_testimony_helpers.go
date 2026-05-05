package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
)

// testerBoard returns the claims board (may be nil).
func (pt *PipelineTester) testerBoard() *claims.ClaimsBoard {
	return pt.claimsBoard
}

// testerScope adapts *concurrency.GoroutineScope to claims.ScopeProvider.
func (pt *PipelineTester) testerScope() claims.ScopeProvider {
	if pt.scope == nil {
		return nil
	}
	return &testerScopeAdapter{scope: pt.scope}
}

type testerScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

func (a *testerScopeAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return a.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

// testerSubmitTestament submits a testament async via scope. Best-effort.
func (pt *PipelineTester) testerSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := pt.testerBoard()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "tester-pipeline", Type: claims.ActionTypeTestament}
	if pt.scope != nil {
		if err := pt.scope.Go("tester_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("tester_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("tester testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("tester_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("tester testament: " + err.Error())
	}
}

// testerTestament builds a testament issued by the pipeline tester.
func (pt *PipelineTester) testerTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	sessionID := pt.config.SessionID
	return claims.Testament{
		AgentID: "tester-pipeline", SessionID: sessionID,
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "tester-pipeline", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// testerArtifact builds a single artifact.
func (pt *PipelineTester) testerArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID: "tester-pipeline", SessionID: pt.config.SessionID,
		Kind: kind, Reference: reference,
	}
}

// testerJSONArtifact builds a JSON-serialized artifact.
func (pt *PipelineTester) testerJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return pt.testerArtifact(kind, string(ref))
}

func (pt *PipelineTester) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	claimID := ""
	claimTitle := ""
	if entry.Node.Claim != nil {
		claimID = entry.Node.Claim.ID
		claimTitle = entry.Node.Claim.Title
	}
	slog.Info("tester_pipeline_process_claims_entry_begin",
		"tester_id", pt.id,
		"session_id", pt.config.SessionID,
		"delta_kind", entry.Delta.DeltaKind(),
		"delta_key", entry.Delta.DeltaKey(),
		"claim_id", claimID,
		"claim_title", claimTitle,
		"board_present", pt.testerBoard() != nil,
	)
	p := pt.getProvider()
	if p == nil {
		slog.Error("tester_pipeline_process_claims_entry_no_provider", "tester_id", pt.id)
		return fmt.Errorf("tester-pipeline: LLM provider not configured")
	}

	acc := agentshared.NewClaimsEntryAccumulator("tester-pipeline", pt.config.SessionID, entry)
	acc.WithBoard(pt.testerBoard())
	defer acc.Flush(ctx, pt.testerBoard(), pt.testerScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	if entry.Node.Claim != nil {
		agentshared.RecordAgentState(ctx, pt.testerBoard(), entry.Node.Claim.ID,
			"Acknowledging request", agentshared.AgentStateReasoning, nil)
	}

	userMessage := agentshared.ComposeClaimsEntryPrompt(entry)
	userMessage = claims.PrependBoardPreamble(userMessage, pt.claimsBoard, "tester-pipeline")
	pt.prepareSkillsForInput(userMessage)
	surface := pt.toolRuntime()

	systemPrompt := testershared.PipelineTesterSystemPrompt()
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        pt.buildToolDefinitionsWithSurface(surface),
		Model:        pt.config.Model,
		MaxTokens:    pt.config.MaxTokens,
	}
	pt.applyLLMRuntimeProfile(req, "claims_entry")

	ledger := pt.steering.Create(entry.Delta.DeltaKey(), pt.id, pt.config.SessionID, nil, nil)
	defer pt.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	result, err := agentshared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return pt.executeToolLoopWithSurface(ctx, req, ledger, surface)
	})
	if err != nil {
		if agentshared.IsConsultYielded(err) {
			slog.Info("tester_pipeline_claims_entry_yielded", "delta_key", entry.Delta.DeltaKey())
			return nil
		}
		slog.Error("tester_pipeline_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateTester truncates a string for claim titles.
func truncateTester(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
