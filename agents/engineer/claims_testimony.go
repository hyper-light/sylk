package engineer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
)

// engineerBoard returns the claims board (may be nil).
func (e *Engineer) engineerBoard() *claims.ClaimsBoard {
	return e.claimsBoard
}

// engineerScope adapts *concurrency.GoroutineScope to claims.ScopeProvider.
func (e *Engineer) engineerScope() claims.ScopeProvider {
	if e.scope == nil {
		return nil
	}
	return &engineerScopeAdapter{scope: e.scope}
}

type engineerScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

func (a *engineerScopeAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return a.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

// engineerSubmitTestament submits a testament async via scope. Best-effort.
func (e *Engineer) engineerSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := e.engineerBoard()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "engineer", Type: claims.ActionTypeTestament}
	if e.scope != nil {
		if err := e.scope.Go("engineer_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("engineer_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("engineer testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("engineer_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("engineer testament: " + err.Error())
	}
}

// engineerTestament builds a testament issued by the engineer.
func (e *Engineer) engineerTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID: "engineer", SessionID: e.config.SessionID,
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// engineerArtifact builds a single artifact.
func (e *Engineer) engineerArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID: "engineer", SessionID: e.config.SessionID,
		Kind: kind, Reference: reference,
	}
}

// engineerJSONArtifact builds a JSON-serialized artifact.
func (e *Engineer) engineerJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return e.engineerArtifact(kind, string(ref))
}

// engineerPostClaim posts a claim async via scope. Best-effort.
func (e *Engineer) engineerPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := e.engineerBoard()
	if board == nil {
		return
	}
	if e.scope != nil {
		if err := e.scope.Go("engineer_post_claim", 5*time.Second, func(gctx context.Context) error {
			return board.PostAction(gctx, action, []claims.Claim{claim})
		}); err != nil {
			slog.Error("engineer_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("engineer post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, []claims.Claim{claim}); err != nil {
		slog.Error("engineer_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("engineer post claim: " + err.Error())
	}
}

// engineerConsultClaim builds a consultation claim against a peer.
func engineerConsultClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeConsultation,
		Relations: []claims.Relation{
			{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// engineerChallengeClaim builds a challenge claim against a peer.
func engineerChallengeClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeChallenge,
		Relations: []claims.Relation{
			{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// engineerValidation builds a pending validation.
func engineerValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPending,
	}
}

// processClaimsEntry handles an event-driven claims delta by composing
// the GraphEntryPoint into a prompt and running the engineer's tool
// loop. The LLM uses its full skill surface (post_action, traverse,
// submit_testaments, evaluate_validation, workspace tools, etc.) to
// process the claim.
func (e *Engineer) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	p := e.getProvider()
	if p == nil {
		return fmt.Errorf("engineer: LLM provider not configured")
	}

	acc := claims.NewTestamentAccumulator("engineer", e.config.SessionID)
	defer acc.Flush(ctx, e.engineerBoard(), e.engineerScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	userMessage = claims.PrependBoardPreamble(userMessage, e.claimsBoard, "engineer")
	e.prepareSkillsForInput(userMessage)
	surface := e.toolRuntime()

	systemPrompt := e.systemPromptForContract(nil)
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        e.buildToolDefinitionsWithSurface(surface),
		Model:        e.config.EngineerConfig.Model,
		MaxTokens:    e.config.EngineerConfig.MaxTokens,
	}
	e.applyLLMRuntimeProfile(req, "implementation")

	ledger := e.steering.Create(entry.Delta.DeltaKey(), e.id, e.config.SessionID, nil, nil)
	defer e.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	result, err := shared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return e.executeToolLoopWithSurface(ctx, req, ledger, surface)
	})
	if err != nil {
		slog.Error("engineer_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateEngineer truncates a string for claim titles.
func truncateEngineer(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
