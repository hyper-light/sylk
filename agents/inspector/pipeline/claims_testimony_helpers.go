package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"

	shared "github.com/adalundhe/sylk/agents/inspector/shared"
)

// inspectorBoard returns the claims board (may be nil).
func (pi *PipelineInspector) inspectorBoard() *claims.ClaimsBoard {
	return pi.claimsBoard
}

// inspectorScope adapts *concurrency.GoroutineScope to claims.ScopeProvider.
func (pi *PipelineInspector) inspectorScope() claims.ScopeProvider {
	if pi.scope == nil {
		return nil
	}
	return &inspectorScopeAdapter{scope: pi.scope}
}

type inspectorScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

func (a *inspectorScopeAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return a.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

// inspectorSubmitTestament submits a testament async via scope. Best-effort.
func (pi *PipelineInspector) inspectorSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := pi.inspectorBoard()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "inspector-pipeline", Type: claims.ActionTypeTestament}
	if pi.scope != nil {
		if err := pi.scope.Go("inspector_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("inspector_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("inspector testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("inspector_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("inspector testament: " + err.Error())
	}
}

// inspectorTestament builds a testament issued by the pipeline inspector.
func (pi *PipelineInspector) inspectorTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	sessionID := ""
	if pi.config.SessionID != "" {
		sessionID = pi.config.SessionID
	}
	return claims.Testament{
		AgentID: "inspector-pipeline", SessionID: sessionID,
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "inspector-pipeline", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// inspectorArtifact builds a single artifact.
func (pi *PipelineInspector) inspectorArtifact(kind, reference string) *claims.Artifact {
	sessionID := ""
	if pi.config.SessionID != "" {
		sessionID = pi.config.SessionID
	}
	return &claims.Artifact{
		AgentID: "inspector-pipeline", SessionID: sessionID,
		Kind: kind, Reference: reference,
	}
}

// inspectorJSONArtifact builds a JSON-serialized artifact.
func (pi *PipelineInspector) inspectorJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return pi.inspectorArtifact(kind, string(ref))
}

// inspectorPostClaim posts a claim async via scope. Best-effort.
// Registers a just-in-time response Expectation on the inspector's
// inbox after a successful PostAction (CLAIMS.md §5).
func (pi *PipelineInspector) inspectorPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := pi.inspectorBoard()
	if board == nil {
		return
	}
	posted := []claims.Claim{claim}
	if pi.scope != nil {
		if err := pi.scope.Go("inspector_post_claim", 5*time.Second, func(gctx context.Context) error {
			if err := board.PostAction(gctx, action, posted); err != nil {
				return err
			}
			claims.RegisterPostActionExpectations(pi.claimsInbox, action, posted)
			return nil
		}); err != nil {
			slog.Error("inspector_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("inspector post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, posted); err != nil {
		slog.Error("inspector_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("inspector post claim: " + err.Error())
		return
	}
	claims.RegisterPostActionExpectations(pi.claimsInbox, action, posted)
}

// inspectorCorrectiveClaim builds a corrective claim against a peer.
func inspectorCorrectiveClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeCorrective,
		Relations: []claims.Relation{
			{Related: "inspector-pipeline", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// inspectorChallengeClaim builds a challenge claim against a peer.
func inspectorChallengeClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeChallenge,
		Relations: []claims.Relation{
			{Related: "inspector-pipeline", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// inspectorValidation builds a pending validation.
func inspectorValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPending,
	}
}

// processClaimsEntry handles an event-driven claims delta.
func (pi *PipelineInspector) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	claimID := ""
	claimTitle := ""
	if entry.Node.Claim != nil {
		claimID = entry.Node.Claim.ID
		claimTitle = entry.Node.Claim.Title
	}
	slog.Info("inspector_pipeline_process_claims_entry_begin",
		"inspector_id", pi.id,
		"session_id", pi.config.SessionID,
		"delta_kind", entry.Delta.DeltaKind(),
		"delta_key", entry.Delta.DeltaKey(),
		"claim_id", claimID,
		"claim_title", claimTitle,
		"board_present", pi.inspectorBoard() != nil,
	)
	p := pi.getProvider()
	if p == nil {
		slog.Error("inspector_pipeline_process_claims_entry_no_provider", "inspector_id", pi.id)
		return fmt.Errorf("inspector-pipeline: LLM provider not configured")
	}

	acc := agentShared.NewClaimsEntryAccumulator("inspector-pipeline", pi.config.SessionID, entry)
	acc.WithBoard(pi.inspectorBoard())
	defer acc.Flush(ctx, pi.inspectorBoard(), pi.inspectorScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	if entry.Node.Claim != nil {
		agentShared.RecordAgentState(ctx, pi.inspectorBoard(), entry.Node.Claim.ID,
			"Acknowledging request", agentShared.AgentStateReasoning, nil)
	}

	userMessage := agentShared.ComposeClaimsEntryPrompt(entry)
	userMessage = claims.PrependBoardPreamble(userMessage, pi.claimsBoard, "inspector-pipeline")
	pi.prepareSkillsForInput(userMessage)
	surface := pi.toolRuntime()

	systemPrompt := shared.PipelineInspectorSystemPrompt()
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        pi.buildToolDefinitionsWithSurface(surface),
		Model:        pi.config.Model,
		MaxTokens:    pi.config.MaxTokens,
	}
	pi.applyLLMRuntimeProfile(req, "claims_entry")

	ledger := pi.steering.Create(entry.Delta.DeltaKey(), pi.id, pi.config.SessionID, nil, nil)
	defer pi.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	result, err := agentShared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return pi.executeToolLoopWithSurface(ctx, req, ledger, surface)
	})
	if err != nil {
		slog.Error("inspector_pipeline_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateInspector truncates a string for claim titles.
func truncateInspector(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
