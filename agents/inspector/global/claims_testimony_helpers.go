package global

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"

	shared "github.com/adalundhe/sylk/agents/inspector/shared"
)

// globalInspectorBoard resolves the claims board for the session.
func (gi *GlobalInspector) globalInspectorBoard() (*claims.ClaimsBoard, error) {
	sid := gi.config.SessionID
	if sid == "" {
		return nil, fmt.Errorf("inspector-global: no session ID configured")
	}
	board := claims.DefaultSessionBoardRegistry().Lookup(sid)
	if board == nil {
		return nil, fmt.Errorf("inspector-global: session %q has no claims board registered", sid)
	}
	return board, nil
}

// globalInspectorBoardOrNil returns the board or nil (for best-effort callers).
func (gi *GlobalInspector) globalInspectorBoardOrNil() *claims.ClaimsBoard {
	board, _ := gi.globalInspectorBoard()
	return board
}

// globalInspectorPostClaim posts a claim async via scope. Best-effort.
func (gi *GlobalInspector) globalInspectorPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := gi.globalInspectorBoardOrNil()
	if board == nil {
		return
	}
	if gi.scope != nil {
		if err := gi.scope.Go("global_inspector_post_claim", 5*time.Second, func(gctx context.Context) error {
			return board.PostAction(gctx, action, []claims.Claim{claim})
		}); err != nil {
			slog.Error("global_inspector_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("global inspector post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, []claims.Claim{claim}); err != nil {
		slog.Error("global_inspector_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("global inspector post claim: " + err.Error())
	}
}

// globalInspectorConsultClaim builds a consultation claim against a peer.
func globalInspectorConsultClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeConsultation,
		Relations: []claims.Relation{
			{Related: "inspector", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// globalInspectorChallengeClaim builds a challenge claim against a peer.
func globalInspectorChallengeClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeChallenge,
		Relations: []claims.Relation{
			{Related: "inspector", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// globalInspectorValidation builds a pending validation.
func globalInspectorValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPending,
	}
}

// globalInspectorSubmitTestament submits a testament async via scope. Best-effort.
func (gi *GlobalInspector) globalInspectorSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := gi.globalInspectorBoardOrNil()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "inspector", Type: claims.ActionTypeTestament}
	if gi.scope != nil {
		if err := gi.scope.Go("global_inspector_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("global_inspector_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("global inspector testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("global_inspector_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("global inspector testament: " + err.Error())
	}
}

// globalInspectorTestament builds a testament.
func (gi *GlobalInspector) globalInspectorTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID: "inspector", SessionID: gi.config.SessionID,
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "inspector", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// globalInspectorArtifact builds a single artifact.
func (gi *GlobalInspector) globalInspectorArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID: "inspector", SessionID: gi.config.SessionID,
		Kind: kind, Reference: reference,
	}
}

// globalInspectorJSONArtifact builds a JSON-serialized artifact.
func (gi *GlobalInspector) globalInspectorJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return gi.globalInspectorArtifact(kind, string(ref))
}

// truncateGlobalInspector truncates a string for claim titles.
func truncateGlobalInspector(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}

func (gi *GlobalInspector) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	p := gi.getProvider()
	if p == nil {
		return fmt.Errorf("inspector: LLM provider not configured")
	}

	acc := claims.NewTestamentAccumulator("inspector", gi.config.SessionID)
	defer acc.Flush(ctx, gi.globalInspectorBoardOrNil(), nil)
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	userMessage := agentShared.ComposeClaimsEntryPrompt(entry)
	board := gi.globalInspectorBoardOrNil()
	userMessage = claims.PrependBoardPreamble(userMessage, board, "inspector")
	gi.prepareSkillsForInput(userMessage)

	systemPrompt := shared.GlobalInspectorSystemPrompt()
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        gi.buildToolDefinitions(),
		Model:        gi.config.Model,
		MaxTokens:    gi.config.MaxTokens,
	}
	gi.applyLLMRuntimeProfile(req, "claims_entry")

	ledger := gi.steering.Create(entry.Delta.DeltaKey(), gi.id, gi.config.SessionID, nil, nil)
	defer gi.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	result, err := agentShared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return gi.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		slog.Error("inspector_global_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}
